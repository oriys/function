//go:build linux
// +build linux

// Package firecracker 提供 Firecracker 微虚拟机的管理功能。
// Firecracker 是 AWS 开发的轻量级虚拟机监控器（VMM），专为无服务器计算场景设计，
// 提供毫秒级启动时间和强隔离性。
// 该包包含网络配置、vsock 通信和虚拟机生命周期管理功能。
package firecracker

import (
	"crypto/rand"
	"fmt"
	"net"
	"os/exec"
	"sync"

	"github.com/oriys/nimbus/internal/config"
	"github.com/sirupsen/logrus"
)

// NetworkConfig 表示虚拟机的网络配置。
// 包含 TAP 设备、MAC 地址和 IP 配置等信息。
type NetworkConfig struct {
	TapDevice  string // TAP 网络设备名称，用于虚拟机网络连接
	MacAddress string // 虚拟机网卡的 MAC 地址
	GuestIP    string // 虚拟机内部的 IP 地址
	GatewayIP  string // 网关 IP 地址（通常是网桥地址）
	SubnetMask string // 子网掩码
}

// NetworkManager 管理 Firecracker 虚拟机的网络配置。
// 负责创建和管理网桥、TAP 设备以及 IP 地址分配。
type NetworkManager struct {
	cfg    config.NetworkConfig // 网络配置
	logger *logrus.Logger       // 日志记录器

	mu         sync.Mutex        // 保护并发访问的互斥锁
	usedIPs    map[string]bool   // 已分配的 IP 地址集合
	tapDevices map[string]string // vmID -> TAP 设备名称的映射
	nextIP     int               // 下一个可分配的 IP 地址（末位）
}

// NewNetworkManager 创建新的网络管理器。
// 会自动初始化网桥（如果不存在）。
// 参数：
//   - cfg: 网络配置
//   - logger: 日志记录器
//
// 返回：
//   - *NetworkManager: 配置好的网络管理器
//   - error: 初始化过程中的错误
func NewNetworkManager(cfg config.NetworkConfig, logger *logrus.Logger) (*NetworkManager, error) {
	nm := &NetworkManager{
		cfg:        cfg,
		logger:     logger,
		usedIPs:    make(map[string]bool),
		tapDevices: make(map[string]string),
		nextIP:     2, // 从 .2 开始分配，.1 保留给网关
	}

	// 初始化网桥（如果不存在）
	if err := nm.setupBridge(); err != nil {
		return nil, fmt.Errorf("failed to setup bridge: %w", err)
	}

	return nm, nil
}

// setupBridge 设置 Linux 网桥。
// 如果网桥已存在则跳过，否则创建新网桥并配置 IP 地址。
func (nm *NetworkManager) setupBridge() error {
	// 检查网桥是否已存在
	_, err := net.InterfaceByName(nm.cfg.BridgeName)
	if err == nil {
		nm.logger.WithField("bridge", nm.cfg.BridgeName).Debug("Bridge already exists")
		return nil
	}

	// 创建网桥设备
	if err := exec.Command("ip", "link", "add", nm.cfg.BridgeName, "type", "bridge").Run(); err != nil {
		return fmt.Errorf("failed to create bridge: %w", err)
	}

	// 为网桥设置 IP 地址（使用 /16 子网）
	if err := exec.Command("ip", "addr", "add", nm.cfg.BridgeIP+"/16", "dev", nm.cfg.BridgeName).Run(); err != nil {
		return fmt.Errorf("failed to set bridge IP: %w", err)
	}

	// 启用网桥
	if err := exec.Command("ip", "link", "set", nm.cfg.BridgeName, "up").Run(); err != nil {
		return fmt.Errorf("failed to bring up bridge: %w", err)
	}

	// 启用 IP 转发以支持虚拟机访问外部网络
	if err := exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run(); err != nil {
		nm.logger.WithError(err).Warn("Failed to enable IP forwarding")
	}

	// 如果启用了 NAT，设置 NAT 规则
	if nm.cfg.UseNAT && nm.cfg.ExternalInterface != "" {
		if err := nm.setupNAT(); err != nil {
			nm.logger.WithError(err).Warn("Failed to setup NAT")
		}
	}

	nm.logger.WithField("bridge", nm.cfg.BridgeName).Info("Bridge created")
	return nil
}

// setupNAT 设置 NAT（网络地址转换）规则。
// 使虚拟机能够通过主机访问外部网络。
func (nm *NetworkManager) setupNAT() error {
	// 添加 MASQUERADE 规则，使虚拟机的出站流量使用主机 IP
	cmd := exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING",
		"-o", nm.cfg.ExternalInterface, "-j", "MASQUERADE")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to add MASQUERADE rule: %w", err)
	}

	// 允许从网桥接口转发流量
	cmd = exec.Command("iptables", "-A", "FORWARD", "-i", nm.cfg.BridgeName, "-j", "ACCEPT")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to add FORWARD rule: %w", err)
	}

	// 允许转发到网桥接口的流量
	cmd = exec.Command("iptables", "-A", "FORWARD", "-o", nm.cfg.BridgeName, "-j", "ACCEPT")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to add FORWARD rule: %w", err)
	}

	nm.logger.Info("NAT configured")
	return nil
}

// SetupNetwork 为指定的虚拟机配置网络。
// 创建 TAP 设备并分配 IP 地址。
// 参数：
//   - vmID: 虚拟机的唯一标识符
//
// 返回：
//   - *NetworkConfig: 网络配置信息
//   - error: 配置过程中的错误
func (nm *NetworkManager) SetupNetwork(vmID string) (*NetworkConfig, error) {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	// 生成 TAP 设备名称（使用 vmID 前 8 个字符）
	tapName := fmt.Sprintf("tap%s", vmID[:8])

	// 创建 TAP 设备
	if err := exec.Command("ip", "tuntap", "add", tapName, "mode", "tap").Run(); err != nil {
		return nil, fmt.Errorf("failed to create tap device: %w", err)
	}

	// 将 TAP 设备连接到网桥
	if err := exec.Command("ip", "link", "set", tapName, "master", nm.cfg.BridgeName).Run(); err != nil {
		// 创建失败时清理 TAP 设备
		exec.Command("ip", "link", "del", tapName).Run()
		return nil, fmt.Errorf("failed to attach tap to bridge: %w", err)
	}

	// 启用 TAP 设备
	if err := exec.Command("ip", "link", "set", tapName, "up").Run(); err != nil {
		exec.Command("ip", "link", "del", tapName).Run()
		return nil, fmt.Errorf("failed to bring up tap: %w", err)
	}

	// 分配 IP 地址
	guestIP := nm.allocateIP()

	// 生成随机 MAC 地址
	mac := nm.generateMAC()

	// 记录分配的资源
	nm.tapDevices[vmID] = tapName
	nm.usedIPs[guestIP] = true

	config := &NetworkConfig{
		TapDevice:  tapName,
		MacAddress: mac,
		GuestIP:    guestIP,
		GatewayIP:  nm.cfg.BridgeIP,
		SubnetMask: "255.255.0.0",
	}

	nm.logger.WithFields(logrus.Fields{
		"vm_id": vmID,
		"tap":   tapName,
		"ip":    guestIP,
		"mac":   mac,
	}).Debug("Network configured for VM")

	return config, nil
}

// CleanupNetwork 清理指定虚拟机的网络资源。
// 删除 TAP 设备并释放 IP 地址。
func (nm *NetworkManager) CleanupNetwork(vmID string) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	tapName, ok := nm.tapDevices[vmID]
	if !ok {
		return nil // 没有找到该虚拟机的网络配置
	}

	// 删除 TAP 设备
	if err := exec.Command("ip", "link", "del", tapName).Run(); err != nil {
		nm.logger.WithError(err).WithField("tap", tapName).Warn("Failed to delete tap device")
	}

	delete(nm.tapDevices, vmID)

	nm.logger.WithFields(logrus.Fields{
		"vm_id": vmID,
		"tap":   tapName,
	}).Debug("Network cleaned up for VM")

	return nil
}

// allocateIP 分配一个 IP 地址。
// 使用简单的顺序分配策略，在 172.20.0.0/16 子网内分配。
func (nm *NetworkManager) allocateIP() string {
	// 在 172.20.0.0/16 子网内顺序分配 IP
	for {
		// 计算 IP 地址的第三和第四字节
		ip := fmt.Sprintf("172.20.%d.%d", nm.nextIP/256, nm.nextIP%256)
		nm.nextIP++
		// 如果超过范围，回到起始位置
		if nm.nextIP > 65534 {
			nm.nextIP = 2
		}
		// 确保 IP 未被使用
		if !nm.usedIPs[ip] {
			return ip
		}
	}
}

// generateMAC 生成一个随机的 MAC 地址。
// 设置本地管理位并清除组播位，确保是有效的单播地址。
func (nm *NetworkManager) generateMAC() string {
	buf := make([]byte, 6)
	rand.Read(buf)
	// 设置本地管理位（第二位）并清除组播位（第一位）
	// 0x02 表示本地管理，0xfe 清除组播位
	buf[0] = (buf[0] | 0x02) & 0xfe
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
		buf[0], buf[1], buf[2], buf[3], buf[4], buf[5])
}

// Shutdown 关闭网络管理器并清理所有资源。
// 删除所有 TAP 设备。
func (nm *NetworkManager) Shutdown() error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	// 清理所有 TAP 设备
	for vmID, tapName := range nm.tapDevices {
		if err := exec.Command("ip", "link", "del", tapName).Run(); err != nil {
			nm.logger.WithError(err).WithField("tap", tapName).Warn("Failed to delete tap device")
		}
		delete(nm.tapDevices, vmID)
	}

	return nil
}
