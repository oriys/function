//go:build linux
// +build linux

// Package firecracker 提供 Firecracker 微虚拟机的管理功能。
package firecracker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/google/uuid"
	"github.com/oriys/nimbus/internal/config"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

// VMState 表示虚拟机的状态。
type VMState string

// 虚拟机状态常量
const (
	VMStateCreating VMState = "creating" // 创建中
	VMStateRunning  VMState = "running"  // 运行中
	VMStateStopped  VMState = "stopped"  // 已停止
	VMStateFailed   VMState = "failed"   // 失败
)

// VM 表示一个 Firecracker 微虚拟机实例。
// 包含虚拟机的配置、状态和元数据信息。
type VM struct {
	ID         string    // 虚拟机唯一标识符（UUID）
	Runtime    string    // 运行时类型（如 python3.11, nodejs20）
	State      VMState   // 当前状态
	IP         string    // 虚拟机的 IP 地址
	VsockCID   uint32    // vsock 通信的 CID（Context ID）
	MemoryMB   int64     // 分配的内存大小（MB）
	VCPUs      int64     // 分配的虚拟 CPU 数量
	SocketPath string    // Firecracker API socket 路径
	RootfsPath string    // 根文件系统镜像路径
	LogPath    string    // 日志文件路径
	CreatedAt  time.Time // 创建时间
	LastUsed   time.Time // 最后使用时间
	UseCount   int       // 使用次数

	machine *firecracker.Machine // Firecracker 机器实例
	cancel  context.CancelFunc   // 用于取消虚拟机上下文
	mu      sync.Mutex           // 保护虚拟机操作的互斥锁
}

// MachineManager 管理 Firecracker 虚拟机的生命周期。
// 负责创建、启动、停止虚拟机以及管理快照。
type MachineManager struct {
	cfg        config.FirecrackerConfig // Firecracker 配置
	networkMgr *NetworkManager          // 网络管理器
	logger     *logrus.Logger           // 日志记录器

	mu      sync.RWMutex      // 保护 vms 映射的读写锁
	vms     map[string]*VM    // vmID -> VM 的映射
	nextCID uint32            // 下一个可分配的 CID
}

// NewMachineManager 创建新的虚拟机管理器。
// 参数：
//   - cfg: Firecracker 配置
//   - networkMgr: 网络管理器
//   - logger: 日志记录器
func NewMachineManager(cfg config.FirecrackerConfig, networkMgr *NetworkManager, logger *logrus.Logger) *MachineManager {
	return &MachineManager{
		cfg:        cfg,
		networkMgr: networkMgr,
		logger:     logger,
		vms:        make(map[string]*VM),
		// CID (Context ID) 是 vsock 协议中用于标识虚拟机的唯一地址。
		// vsock CID 保留值说明：
		//   - 0: 表示 hypervisor（预留）
		//   - 1: 表示本地环回（预留，类似 localhost）
		//   - 2: 表示宿主机
		//   - 3-99: 通常被系统或其他服务使用
		// 因此从 100 开始分配，确保不会与系统保留值或其他服务冲突。
		nextCID: 100,
	}
}

// CreateVM 创建并启动一个新的 Firecracker 虚拟机。
// 参数：
//   - ctx: 上下文
//   - runtime: 运行时类型
//   - memoryMB: 内存大小（MB）
//   - vcpus: 虚拟 CPU 数量
//
// 返回：
//   - *VM: 创建的虚拟机实例
//   - error: 创建过程中的错误
func (m *MachineManager) CreateVM(ctx context.Context, runtime string, memoryMB, vcpus int64) (*VM, error) {
	vmID := uuid.New().String()

	// 分配唯一的 CID
	m.mu.Lock()
	cid := m.nextCID
	m.nextCID++
	m.mu.Unlock()

	// 设置各种路径
	socketPath := filepath.Join(m.cfg.SocketDir, vmID+".sock")
	logPath := filepath.Join(m.cfg.LogDir, vmID+".log")

	// 确保所需目录存在
	os.MkdirAll(m.cfg.SocketDir, 0755)
	os.MkdirAll(m.cfg.LogDir, 0755)
	os.MkdirAll(m.cfg.VsockDir, 0755)
	os.MkdirAll(m.cfg.SnapshotDir, 0755)

	// 获取运行时对应的根文件系统路径
	baseRootfsPath := filepath.Join(m.cfg.RootfsDir, runtime, "rootfs.ext4")
	if _, err := os.Stat(baseRootfsPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("rootfs not found for runtime %s: %s", runtime, baseRootfsPath)
	}

	// 克隆根文件系统（每个虚拟机使用独立副本）
	rootfsPath, err := m.cloneRootfs(runtime, vmID, baseRootfsPath)
	if err != nil {
		return nil, err
	}

	// 配置网络
	netConfig, err := m.networkMgr.SetupNetwork(vmID)
	if err != nil {
		_ = os.Remove(rootfsPath)
		return nil, fmt.Errorf("failed to setup network: %w", err)
	}

	// 初始化虚拟机结构
	vm := &VM{
		ID:         vmID,
		Runtime:    runtime,
		State:      VMStateCreating,
		VsockCID:   cid,
		MemoryMB:   memoryMB,
		VCPUs:      vcpus,
		SocketPath: socketPath,
		RootfsPath: rootfsPath,
		LogPath:    logPath,
		IP:         netConfig.GuestIP,
		CreatedAt:  time.Now(),
	}

	// 构建 Firecracker 配置
	fcConfig := m.buildFirecrackerConfig(vm, vm.RootfsPath, netConfig)

	// 创建日志文件
	logFile, err := os.Create(logPath)
	if err != nil {
		m.networkMgr.CleanupNetwork(vmID)
		_ = os.Remove(rootfsPath)
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	// 创建可取消的上下文
	machineCtx, cancel := context.WithCancel(ctx)
	vm.cancel = cancel

	// 构建 Firecracker 命令
	cmd := firecracker.VMCommandBuilder{}.
		WithBin(m.cfg.Binary).
		WithSocketPath(socketPath).
		WithStderr(logFile).
		WithStdout(logFile).
		Build(machineCtx)

	// 创建 Firecracker 机器实例
	machine, err := firecracker.NewMachine(machineCtx, fcConfig, firecracker.WithProcessRunner(cmd))
	if err != nil {
		cancel()
		m.networkMgr.CleanupNetwork(vmID)
		logFile.Close()
		_ = os.Remove(rootfsPath)
		return nil, fmt.Errorf("failed to create machine: %w", err)
	}

	vm.machine = machine

	// 启动虚拟机
	if err := machine.Start(machineCtx); err != nil {
		cancel()
		m.networkMgr.CleanupNetwork(vmID)
		logFile.Close()
		_ = os.Remove(rootfsPath)
		return nil, fmt.Errorf("failed to start machine: %w", err)
	}

	vm.State = VMStateRunning

	// 注册虚拟机
	m.mu.Lock()
	m.vms[vmID] = vm
	m.mu.Unlock()

	m.logger.WithFields(logrus.Fields{
		"vm_id":   vmID,
		"runtime": runtime,
		"ip":      vm.IP,
		"cid":     cid,
		"memory":  memoryMB,
		"vcpus":   vcpus,
	}).Info("VM created and started")

	return vm, nil
}

// buildFirecrackerConfig 构建 Firecracker 虚拟机配置。
// 包含内核、磁盘、网络和 vsock 配置。
func (m *MachineManager) buildFirecrackerConfig(vm *VM, rootfsPath string, netConfig *NetworkConfig) firecracker.Config {
	return firecracker.Config{
		SocketPath:      vm.SocketPath,
		KernelImagePath: m.cfg.Kernel,
		// 内核启动参数：控制台输出、panic 时重启、禁用 PCI、指定 init 进程
		KernelArgs: "console=ttyS0 reboot=k panic=1 pci=off init=/sbin/init",
		// 磁盘配置
		Drives: []models.Drive{
			{
				DriveID:      firecracker.String("rootfs"),
				PathOnHost:   firecracker.String(rootfsPath),
				IsRootDevice: firecracker.Bool(true),
				IsReadOnly:   firecracker.Bool(false),
			},
		},
		// 网络接口配置
		NetworkInterfaces: []firecracker.NetworkInterface{
			{
				StaticConfiguration: &firecracker.StaticNetworkConfiguration{
					MacAddress:  netConfig.MacAddress,
					HostDevName: netConfig.TapDevice,
				},
			},
		},
		// 机器配置（CPU 和内存）
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(vm.VCPUs),
			MemSizeMib: firecracker.Int64(vm.MemoryMB),
		},
		// vsock 设备配置，用于主机与虚拟机通信
		VsockDevices: []firecracker.VsockDevice{
			{
				Path: filepath.Join(m.cfg.VsockDir, vm.ID+".vsock"),
				CID:  vm.VsockCID,
			},
		},
	}
}

// GetVM 根据 ID 获取虚拟机。
// 返回虚拟机实例和是否找到的布尔值。
func (m *MachineManager) GetVM(vmID string) (*VM, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	vm, ok := m.vms[vmID]
	return vm, ok
}

// StopVM 停止并清理指定的虚拟机。
// 包括关闭虚拟机进程、清理网络和删除临时文件。
func (m *MachineManager) StopVM(ctx context.Context, vmID string) error {
	// 从映射中移除虚拟机
	m.mu.Lock()
	vm, ok := m.vms[vmID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("vm not found: %s", vmID)
	}
	delete(m.vms, vmID)
	m.mu.Unlock()

	vm.mu.Lock()
	defer vm.mu.Unlock()

	// 优雅关闭虚拟机
	if vm.machine != nil {
		if err := vm.machine.Shutdown(ctx); err != nil {
			m.logger.WithError(err).Warn("Failed to gracefully shutdown VM, forcing stop")
			vm.machine.StopVMM() // 强制停止
		}
	}

	// 取消虚拟机上下文
	if vm.cancel != nil {
		vm.cancel()
	}

	// 清理网络资源
	m.networkMgr.CleanupNetwork(vmID)

	// 清理临时文件
	os.Remove(vm.SocketPath)
	os.Remove(filepath.Join(m.cfg.VsockDir, vm.ID+".vsock"))
	if vm.RootfsPath != "" {
		_ = os.Remove(vm.RootfsPath)
	}

	vm.State = VMStateStopped

	m.logger.WithField("vm_id", vmID).Info("VM stopped")
	return nil
}

// cloneRootfs 克隆根文件系统镜像。
// 每个虚拟机使用独立的根文件系统副本，避免相互影响。
// 优先使用 FICLONE（reflink）进行写时复制克隆，失败时回退到普通复制。
func (m *MachineManager) cloneRootfs(runtime, vmID, baseRootfsPath string) (string, error) {
	destDir := filepath.Join(m.cfg.SnapshotDir, "vmrootfs", runtime)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("create vm rootfs dir: %w", err)
	}
	destPath := filepath.Join(destDir, vmID+".ext4")
	if err := cloneOrCopyFile(baseRootfsPath, destPath); err != nil {
		return "", fmt.Errorf("clone rootfs: %w", err)
	}
	return destPath, nil
}

// cloneOrCopyFile 克隆或复制文件。
// 优先尝试使用 FICLONE ioctl（reflink）进行高效的写时复制克隆，
// 如果文件系统不支持则回退到传统的数据复制。
func cloneOrCopyFile(src, dst string) (err error) {
	// 打开源文件
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// 创建目标文件
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := out.Close()
		if err == nil {
			err = closeErr
		}
		// 如果出错，清理目标文件
		if err != nil {
			_ = os.Remove(dst)
		}
	}()

	// 尝试使用 FICLONE 进行写时复制克隆（仅在支持的文件系统上有效，如 Btrfs、XFS）
	if err := unix.IoctlFileClone(int(out.Fd()), int(in.Fd())); err == nil {
		return out.Sync()
	}

	// 回退到传统复制
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// ListVMs 返回所有虚拟机的列表。
func (m *MachineManager) ListVMs() []*VM {
	m.mu.RLock()
	defer m.mu.RUnlock()

	vms := make([]*VM, 0, len(m.vms))
	for _, vm := range m.vms {
		vms = append(vms, vm)
	}
	return vms
}

// CreateSnapshot 创建虚拟机的快照。
// 快照可用于快速恢复虚拟机状态，实现更快的冷启动。
// 参数：
//   - ctx: 上下文
//   - vmID: 虚拟机 ID
//   - snapshotID: 快照的唯一标识符
func (m *MachineManager) CreateSnapshot(ctx context.Context, vmID, snapshotID string) error {
	m.mu.RLock()
	vm, ok := m.vms[vmID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("vm not found: %s", vmID)
	}

	// 创建快照目录
	snapshotDir := filepath.Join(m.cfg.SnapshotDir, snapshotID)
	os.MkdirAll(snapshotDir, 0755)

	memFilePath := filepath.Join(snapshotDir, "mem")      // 内存快照文件
	snapshotPath := filepath.Join(snapshotDir, "snapshot") // 状态快照文件

	// 暂停虚拟机以创建一致的快照
	if err := vm.machine.PauseVM(ctx); err != nil {
		return fmt.Errorf("failed to pause VM: %w", err)
	}

	// 创建快照
	if err := vm.machine.CreateSnapshot(ctx, memFilePath, snapshotPath); err != nil {
		vm.machine.ResumeVM(ctx) // 恢复虚拟机
		return fmt.Errorf("failed to create snapshot: %w", err)
	}

	m.logger.WithFields(logrus.Fields{
		"vm_id":       vmID,
		"snapshot_id": snapshotID,
	}).Info("Snapshot created")

	return nil
}

// RestoreFromSnapshot 从快照恢复创建新的虚拟机。
// 比从头创建虚拟机更快，适用于需要快速启动的场景。
// 参数：
//   - ctx: 上下文
//   - snapshotID: 快照 ID
//   - runtime: 运行时类型
func (m *MachineManager) RestoreFromSnapshot(ctx context.Context, snapshotID, runtime string) (*VM, error) {
	vmID := uuid.New().String()

	// 分配 CID
	m.mu.Lock()
	cid := m.nextCID
	m.nextCID++
	m.mu.Unlock()

	// 构建快照路径
	snapshotDir := filepath.Join(m.cfg.SnapshotDir, snapshotID)
	memFilePath := filepath.Join(snapshotDir, "mem")
	snapshotPath := filepath.Join(snapshotDir, "snapshot")

	// 检查快照是否存在
	if _, err := os.Stat(snapshotPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("snapshot not found: %s", snapshotID)
	}

	socketPath := filepath.Join(m.cfg.SocketDir, vmID+".sock")
	logPath := filepath.Join(m.cfg.LogDir, vmID+".log")

	// 配置网络
	netConfig, err := m.networkMgr.SetupNetwork(vmID)
	if err != nil {
		return nil, fmt.Errorf("failed to setup network: %w", err)
	}

	// 初始化虚拟机结构
	vm := &VM{
		ID:         vmID,
		Runtime:    runtime,
		State:      VMStateCreating,
		VsockCID:   cid,
		SocketPath: socketPath,
		LogPath:    logPath,
		IP:         netConfig.GuestIP,
		CreatedAt:  time.Now(),
	}

	// 创建日志文件
	logFile, err := os.Create(logPath)
	if err != nil {
		m.networkMgr.CleanupNetwork(vmID)
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	// 创建可取消的上下文
	machineCtx, cancel := context.WithCancel(ctx)
	vm.cancel = cancel

	// 构建 Firecracker 命令
	cmd := firecracker.VMCommandBuilder{}.
		WithBin(m.cfg.Binary).
		WithSocketPath(socketPath).
		WithStderr(logFile).
		WithStdout(logFile).
		Build(machineCtx)

	// 配置从快照恢复
	cfg := firecracker.Config{
		SocketPath: socketPath,
		Snapshot: firecracker.SnapshotConfig{
			MemFilePath:         memFilePath,
			SnapshotPath:        snapshotPath,
			EnableDiffSnapshots: false,
			ResumeVM:            true, // 恢复后自动运行
		},
		VsockDevices: []firecracker.VsockDevice{
			{
				Path: filepath.Join(m.cfg.VsockDir, vm.ID+".vsock"),
				CID:  cid,
			},
		},
		NetworkInterfaces: []firecracker.NetworkInterface{
			{
				StaticConfiguration: &firecracker.StaticNetworkConfiguration{
					MacAddress:  netConfig.MacAddress,
					HostDevName: netConfig.TapDevice,
				},
			},
		},
	}

	// 从快照创建机器实例
	machine, err := firecracker.NewMachine(machineCtx, cfg, firecracker.WithProcessRunner(cmd), firecracker.WithSnapshot(memFilePath, snapshotPath))
	if err != nil {
		cancel()
		m.networkMgr.CleanupNetwork(vmID)
		logFile.Close()
		return nil, fmt.Errorf("failed to create machine from snapshot: %w", err)
	}

	vm.machine = machine

	// 启动虚拟机
	if err := machine.Start(machineCtx); err != nil {
		cancel()
		m.networkMgr.CleanupNetwork(vmID)
		logFile.Close()
		return nil, fmt.Errorf("failed to start machine from snapshot: %w", err)
	}

	vm.State = VMStateRunning

	// 注册虚拟机
	m.mu.Lock()
	m.vms[vmID] = vm
	m.mu.Unlock()

	m.logger.WithFields(logrus.Fields{
		"vm_id":       vmID,
		"snapshot_id": snapshotID,
		"runtime":     runtime,
	}).Info("VM restored from snapshot")

	return vm, nil
}

// Shutdown 关闭虚拟机管理器并停止所有虚拟机。
// 应在程序退出时调用以确保所有资源被正确释放。
func (m *MachineManager) Shutdown(ctx context.Context) error {
	// 获取所有虚拟机列表
	m.mu.Lock()
	vms := make([]*VM, 0, len(m.vms))
	for _, vm := range m.vms {
		vms = append(vms, vm)
	}
	m.mu.Unlock()

	// 逐个停止虚拟机
	for _, vm := range vms {
		if err := m.StopVM(ctx, vm.ID); err != nil {
			m.logger.WithError(err).WithField("vm_id", vm.ID).Error("Failed to stop VM during shutdown")
		}
	}

	return nil
}
