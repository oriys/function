//go:build linux
// +build linux

// Package firecracker 提供 Firecracker 微虚拟机的管理功能。
// Firecracker 是 AWS 开发的轻量级虚拟机监控器（VMM），专为无服务器计算场景设计，
// 提供毫秒级启动时间和强隔离性。
// 该包包含网络配置、vsock 通信和虚拟机生命周期管理功能。
package firecracker

import (
	"crypto/rand"
	"encoding/binary"
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
	ipByVMID   map[string]string // vmID -> guestIP
	tapDevices map[string]string // vmID -> TAP 设备名称的映射

	subnet      *net.IPNet // 可分配的子网（IPv4）
	subnetFirst uint32     // 第一个可用主机地址（含）
	subnetLast  uint32     // 最后一个可用主机地址（含）
	nextIP      uint32     // 下一次尝试分配的 IP（uint32）
	netmask     string     // 子网掩码（点分十进制）
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
	subnet, first, last, netmask, err := parseIPv4Subnet(cfg.SubnetCIDR)
	if err != nil {
		return nil, fmt.Errorf("invalid subnet_cidr %q: %w", cfg.SubnetCIDR, err)
	}
	if ip := net.ParseIP(cfg.BridgeIP); ip == nil || ip.To4() == nil {
		return nil, fmt.Errorf("invalid bridge_ip %q", cfg.BridgeIP)
	} else if !subnet.Contains(ip) {
		return nil, fmt.Errorf("bridge_ip %q is not within subnet_cidr %q", cfg.BridgeIP, cfg.SubnetCIDR)
	}

	nm := &NetworkManager{
		cfg:         cfg,
		logger:      logger,
		usedIPs:     make(map[string]bool),
		ipByVMID:    make(map[string]string),
		tapDevices:  make(map[string]string),
		subnet:      subnet,
		subnetFirst: first,
		subnetLast:  last,
		nextIP:      first, // 从子网第一个可用地址开始，分配时会跳过网关等保留地址
		netmask:     netmask,
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

	// 为网桥设置 IP 地址（使用 subnet_cidr 的掩码长度）
	prefixLen, err := subnetPrefixLen(nm.subnet)
	if err != nil {
		return fmt.Errorf("invalid subnet mask: %w", err)
	}
	if err := exec.Command("ip", "addr", "add", fmt.Sprintf("%s/%d", nm.cfg.BridgeIP, prefixLen), "dev", nm.cfg.BridgeName).Run(); err != nil {
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
	guestIP, err := nm.allocateIPLocked()
	if err != nil {
		exec.Command("ip", "link", "del", tapName).Run()
		return nil, err
	}

	// 生成随机 MAC 地址
	mac := nm.generateMAC()

	// 记录分配的资源
	nm.tapDevices[vmID] = tapName
	nm.usedIPs[guestIP] = true
	nm.ipByVMID[vmID] = guestIP

	config := &NetworkConfig{
		TapDevice:  tapName,
		MacAddress: mac,
		GuestIP:    guestIP,
		GatewayIP:  nm.cfg.BridgeIP,
		SubnetMask: nm.netmask,
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

	// 释放 IP
	if ip, ok := nm.ipByVMID[vmID]; ok {
		delete(nm.usedIPs, ip)
		delete(nm.ipByVMID, vmID)
	}

	nm.logger.WithFields(logrus.Fields{
		"vm_id": vmID,
		"tap":   tapName,
	}).Debug("Network cleaned up for VM")

	return nil
}

// allocateIPLocked 分配一个未占用的 IPv4 地址。
// 必须在 nm.mu 持有状态下调用。
func (nm *NetworkManager) allocateIPLocked() (string, error) {
	gateway, err := parseIPv4ToUint32(nm.cfg.BridgeIP)
	if err != nil {
		return "", fmt.Errorf("invalid bridge_ip %q: %w", nm.cfg.BridgeIP, err)
	}

	// 完整遍历一轮，找不到则认为耗尽
	start := nm.nextIP
	for {
		candidate := nm.nextIP
		nm.nextIP++
		if nm.nextIP > nm.subnetLast {
			nm.nextIP = nm.subnetFirst
		}

		// 跳过网关地址 / 已占用地址
		if candidate != gateway {
			ipStr := uint32ToIPv4(candidate).String()
			if !nm.usedIPs[ipStr] {
				return ipStr, nil
			}
		}

		if nm.nextIP == start {
			return "", fmt.Errorf("no available IPs in subnet %s", nm.cfg.SubnetCIDR)
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
		if ip, ok := nm.ipByVMID[vmID]; ok {
			delete(nm.usedIPs, ip)
			delete(nm.ipByVMID, vmID)
		}
	}

	return nil
}

func parseIPv4Subnet(cidr string) (*net.IPNet, uint32, uint32, string, error) {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, 0, 0, "", err
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return nil, 0, 0, "", fmt.Errorf("only IPv4 is supported")
	}
	ones, bits := ipNet.Mask.Size()
	if bits != 32 {
		return nil, 0, 0, "", fmt.Errorf("only IPv4 is supported")
	}
	if ones > 30 {
		return nil, 0, 0, "", fmt.Errorf("subnet too small: /%d", ones)
	}

	base, err := ipv4ToUint32(ipNet.IP)
	if err != nil {
		return nil, 0, 0, "", err
	}
	hostBits := uint32(32 - ones)
	// usable: [base+1, base+(2^hostBits)-2]
	first := base + 1
	last := base + (1 << hostBits) - 2

	_ = ip4 // validated above; ipNet.IP already contains the masked network address
	return &net.IPNet{IP: ipNet.IP.To4(), Mask: ipNet.Mask}, first, last, maskToDottedDecimal(ipNet.Mask), nil
}

func subnetPrefixLen(subnet *net.IPNet) (int, error) {
	if subnet == nil {
		return 0, fmt.Errorf("nil subnet")
	}
	ones, bits := subnet.Mask.Size()
	if bits != 32 {
		return 0, fmt.Errorf("only IPv4 is supported")
	}
	return ones, nil
}

func maskToDottedDecimal(mask net.IPMask) string {
	if len(mask) != 4 {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d.%d", mask[0], mask[1], mask[2], mask[3])
}

func parseIPv4ToUint32(s string) (uint32, error) {
	ip := net.ParseIP(s)
	if ip == nil {
		return 0, fmt.Errorf("invalid IP")
	}
	return ipv4ToUint32(ip)
}

func ipv4ToUint32(ip net.IP) (uint32, error) {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0, fmt.Errorf("not IPv4")
	}
	return binary.BigEndian.Uint32(ip4), nil
}

func uint32ToIPv4(n uint32) net.IP {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, n)
	return net.IP(b)
}
