//go:build linux
// +build linux

// Package snapshot 提供函数级快照管理功能。
package snapshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/oriys/nimbus/internal/domain"
	fc "github.com/oriys/nimbus/internal/firecracker"
	"github.com/sirupsen/logrus"
)

// FirecrackerBuilder 使用 Firecracker 创建真实快照
type FirecrackerBuilder struct {
	machinesMgr *fc.MachineManager
	logger      *logrus.Logger
}

// NewFirecrackerBuilder 创建 Firecracker 快照构建器
func NewFirecrackerBuilder(machinesMgr *fc.MachineManager, logger *logrus.Logger) *FirecrackerBuilder {
	return &FirecrackerBuilder{
		machinesMgr: machinesMgr,
		logger:      logger,
	}
}

// BuildSnapshot 构建函数快照
// 流程：
//  1. 创建临时 VM
//  2. 连接 vsock
//  3. 发送 InitPayload（注入代码）
//  4. 可选执行预热调用
//  5. 暂停 VM 并创建快照
//  6. 销毁临时 VM
func (b *FirecrackerBuilder) BuildSnapshot(ctx context.Context, fn *domain.Function, version int, snapshotPath string) (memSize, stateSize int64, err error) {
	b.logger.WithFields(logrus.Fields{
		"function_id":   fn.ID,
		"version":       version,
		"runtime":       fn.Runtime,
		"snapshot_path": snapshotPath,
	}).Info("Building Firecracker snapshot")

	// 1. 创建临时 VM
	vm, err := b.machinesMgr.CreateVM(ctx, string(fn.Runtime), int64(fn.MemoryMB), 1)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create temp VM: %w", err)
	}
	defer func() {
		// 确保清理临时 VM
		if stopErr := b.machinesMgr.StopVM(ctx, vm.ID); stopErr != nil {
			b.logger.WithError(stopErr).WithField("vm_id", vm.ID).Warn("Failed to stop temp VM")
		}
	}()

	b.logger.WithField("vm_id", vm.ID).Debug("Temp VM created for snapshot")

	// 2. 连接 vsock 并初始化函数
	client := fc.NewVsockClient(vm.VsockCID, b.logger)
	defer client.Close()

	if err := client.Connect(ctx); err != nil {
		return 0, 0, fmt.Errorf("failed to connect vsock: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx); err != nil {
		return 0, 0, fmt.Errorf("failed to ping agent: %w", err)
	}

	// 3. 发送 InitPayload
	initPayload := &fc.InitPayload{
		FunctionID:    fn.ID,
		Handler:       fn.Handler,
		Code:          fn.Code,
		Runtime:       string(fn.Runtime),
		EnvVars:       fn.EnvVars,
		MemoryLimitMB: fn.MemoryMB,
		TimeoutSec:    fn.TimeoutSec,
	}

	if err := client.InitFunction(ctx, initPayload); err != nil {
		return 0, 0, fmt.Errorf("failed to init function in VM: %w", err)
	}

	b.logger.WithField("vm_id", vm.ID).Debug("Function initialized in VM")

	// 4. 可选：执行预热调用（使运行时完全初始化）
	// 某些运行时在第一次执行时有额外的初始化开销
	// 通过执行一次空调用来完成这些初始化
	_, _ = client.Execute(ctx, "warmup", []byte(`{}`))

	// 5. 创建快照
	memPath := filepath.Join(snapshotPath, "mem")
	statePath := filepath.Join(snapshotPath, "snapshot")

	// 使用 MachineManager 的快照功能
	// 注意：CreateSnapshotWithPath 会暂停 VM、创建快照、然后可以恢复或销毁 VM
	if err := b.machinesMgr.CreateSnapshotWithPath(ctx, vm.ID, memPath, statePath); err != nil {
		return 0, 0, fmt.Errorf("failed to create snapshot: %w", err)
	}

	// 6. 获取文件大小
	memInfo, err := os.Stat(memPath)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to stat mem file: %w", err)
	}

	stateInfo, err := os.Stat(statePath)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to stat state file: %w", err)
	}

	b.logger.WithFields(logrus.Fields{
		"vm_id":      vm.ID,
		"mem_size":   memInfo.Size(),
		"state_size": stateInfo.Size(),
	}).Info("Firecracker snapshot created successfully")

	return memInfo.Size(), stateInfo.Size(), nil
}
