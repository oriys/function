//go:build linux
// +build linux

// Package vmpool 提供 Firecracker 虚拟机池管理功能。
// 该包实现了虚拟机的预热池（warm pool）机制，通过预先创建和维护一组就绪的虚拟机，
// 减少函数调用时的冷启动延迟。支持自动扩缩容、健康检查和快照恢复等功能。
package vmpool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/oriys/nimbus/internal/config"
	fc "github.com/oriys/nimbus/internal/firecracker"
	"github.com/oriys/nimbus/internal/metrics"
	"github.com/oriys/nimbus/internal/storage"
	"github.com/sirupsen/logrus"
)

// PooledVM 表示池中的一个虚拟机实例。
// 包装了底层的 VM 和 vsock 客户端，添加了池管理所需的元数据。
type PooledVM struct {
	VM        *fc.VM          // 底层 Firecracker 虚拟机
	Client    *fc.VsockClient // 与虚拟机内 agent 通信的 vsock 客户端
	Runtime   string          // 运行时类型
	Status    string          // 状态：warm（预热）、busy（忙碌）、cold（冷）
	CreatedAt time.Time       // 创建时间
	LastUsed  time.Time       // 最后使用时间
	UseCount  int             // 使用次数
}

// Pool 是虚拟机池的主结构。
// 管理多个运行时的虚拟机池，提供获取和释放虚拟机的接口。
type Pool struct {
	cfg         config.PoolConfig    // 池配置
	machinesMgr *fc.MachineManager   // Firecracker 虚拟机管理器
	redis       *storage.RedisStore  // Redis 存储（用于分布式场景）
	metrics     *metrics.Metrics     // 指标收集器
	logger      *logrus.Logger       // 日志记录器

	mu    sync.RWMutex              // 保护 pools 的读写锁
	pools map[string]*RuntimePool   // 运行时名称到运行时池的映射

	ctx    context.Context    // 池的上下文
	cancel context.CancelFunc // 用于取消池的后台任务
}

// RuntimePool 表示特定运行时的虚拟机池。
// 每种运行时（如 python3.11、nodejs20）有独立的池。
type RuntimePool struct {
	runtime string               // 运行时类型
	config  config.RuntimeConfig // 运行时配置（最小/最大 VM 数、内存等）
	warmVMs chan *PooledVM       // 预热虚拟机的缓冲通道
	mu      sync.Mutex           // 保护 allVMs 的互斥锁
	allVMs  map[string]*PooledVM // 所有虚拟机的映射（ID -> VM）
}

// NewPool 创建新的虚拟机池。
// 参数：
//   - cfg: 池配置
//   - machinesMgr: Firecracker 虚拟机管理器
//   - redis: Redis 存储（可为 nil）
//   - m: 指标收集器（可为 nil）
//   - logger: 日志记录器
func NewPool(
	cfg config.PoolConfig,
	machinesMgr *fc.MachineManager,
	redis *storage.RedisStore,
	m *metrics.Metrics,
	logger *logrus.Logger,
) *Pool {
	ctx, cancel := context.WithCancel(context.Background())

	p := &Pool{
		cfg:         cfg,
		machinesMgr: machinesMgr,
		redis:       redis,
		metrics:     m,
		logger:      logger,
		pools:       make(map[string]*RuntimePool),
		ctx:         ctx,
		cancel:      cancel,
	}

	// 为每种配置的运行时初始化池
	for _, rtCfg := range cfg.Runtimes {
		p.pools[rtCfg.Runtime] = &RuntimePool{
			runtime: rtCfg.Runtime,
			config:  rtCfg,
			warmVMs: make(chan *PooledVM, rtCfg.MaxTotal), // 预热 VM 缓冲通道
			allVMs:  make(map[string]*PooledVM),
		}
	}

	return p
}

// Start 启动虚拟机池。
// 包括预热虚拟机和启动后台工作协程。
func (p *Pool) Start() error {
	// 为每种运行时预热虚拟机
	for runtime, pool := range p.pools {
		p.logger.WithField("runtime", runtime).Info("Pre-warming VMs")
		// 并发创建预热虚拟机
		for i := 0; i < pool.config.MinWarm; i++ {
			go func(rt string) {
				if _, err := p.createWarmVM(rt); err != nil {
					p.logger.WithError(err).WithField("runtime", rt).Error("Failed to pre-warm VM")
				}
			}(runtime)
		}
	}

	// 启动后台工作协程
	go p.healthCheckWorker()  // 健康检查
	go p.scalingWorker()      // 自动扩缩容
	if p.metrics != nil {
		go p.metricsWorker()   // 指标上报
	}

	return nil
}

// metricsWorker 定期上报池状态指标。
func (p *Pool) metricsWorker() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			// 获取并上报各运行时的池状态
			stats := p.GetStats()
			for runtime, st := range stats {
				p.metrics.UpdatePoolStats(runtime, st.WarmVMs, st.BusyVMs, st.TotalVMs)
			}
		}
	}
}

// Stop 停止虚拟机池并释放所有资源。
// 应在程序关闭时调用。
func (p *Pool) Stop() error {
	// 取消所有后台任务
	p.cancel()

	// 停止所有虚拟机
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, pool := range p.pools {
		pool.mu.Lock()
		for _, pvm := range pool.allVMs {
			pvm.Client.Close()
			p.machinesMgr.StopVM(context.Background(), pvm.VM.ID)
		}
		pool.mu.Unlock()
	}

	return nil
}

// AcquireVM 从池中获取一个虚拟机。
// 优先返回预热的虚拟机（热启动），如果没有可用的则创建新的（冷启动）。
// 参数：
//   - ctx: 上下文，用于超时控制
//   - runtime: 运行时类型
//
// 返回：
//   - *PooledVM: 获取到的虚拟机
//   - bool: 是否为冷启动（true 表示冷启动）
//   - error: 错误信息
func (p *Pool) AcquireVM(ctx context.Context, runtime string) (*PooledVM, bool, error) {
	pool, ok := p.pools[runtime]
	if !ok {
		return nil, false, fmt.Errorf("unknown runtime: %s", runtime)
	}

	// 尝试获取预热虚拟机（非阻塞）
	select {
	case pvm := <-pool.warmVMs:
		// 获取到预热虚拟机，更新状态
		pool.mu.Lock()
		pvm.Status = "busy"
		pvm.LastUsed = time.Now()
		pvm.UseCount++
		pool.mu.Unlock()

		p.logger.WithFields(logrus.Fields{
			"vm_id":   pvm.VM.ID,
			"runtime": runtime,
		}).Debug("Acquired warm VM")

		return pvm, false, nil // false = 热启动
	default:
		// 没有预热虚拟机可用
	}

	// 检查是否可以创建新虚拟机
	pool.mu.Lock()
	totalVMs := len(pool.allVMs)
	pool.mu.Unlock()

	if totalVMs >= pool.config.MaxTotal {
		// 池已满，等待预热虚拟机
		select {
		case pvm := <-pool.warmVMs:
			pool.mu.Lock()
			pvm.Status = "busy"
			pvm.LastUsed = time.Now()
			pvm.UseCount++
			pool.mu.Unlock()
			return pvm, false, nil
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}

	// 创建新虚拟机（冷启动）
	pvm, err := p.createVM(ctx, runtime)
	if err != nil {
		return nil, false, err
	}

	pool.mu.Lock()
	pvm.Status = "busy"
	pool.allVMs[pvm.VM.ID] = pvm
	pool.mu.Unlock()

	p.logger.WithFields(logrus.Fields{
		"vm_id":   pvm.VM.ID,
		"runtime": runtime,
	}).Debug("Created new VM (cold start)")

	return pvm, true, nil // true = 冷启动
}

// ReleaseVM 释放虚拟机回池中。
// 根据虚拟机的使用情况决定是回收还是销毁。
func (p *Pool) ReleaseVM(runtime, vmID string) error {
	pool, ok := p.pools[runtime]
	if !ok {
		return fmt.Errorf("unknown runtime: %s", runtime)
	}

	pool.mu.Lock()
	pvm, ok := pool.allVMs[vmID]
	if !ok {
		pool.mu.Unlock()
		return fmt.Errorf("vm not found: %s", vmID)
	}

	// 检查是否应该销毁虚拟机：
	// 1. 使用次数超过限制
	// 2. 存活时间超过限制
	if pvm.UseCount >= p.cfg.MaxInvocations || time.Since(pvm.CreatedAt) > p.cfg.MaxVMAge {
		delete(pool.allVMs, vmID)
		pool.mu.Unlock()

		// 销毁虚拟机
		pvm.Client.Close()
		return p.machinesMgr.StopVM(context.Background(), vmID)
	}

	// 标记为预热状态
	pvm.Status = "warm"
	pool.mu.Unlock()

	// 尝试放回预热队列
	select {
	case pool.warmVMs <- pvm:
		p.logger.WithField("vm_id", vmID).Debug("VM returned to warm pool")
	default:
		// 预热队列已满，销毁虚拟机
		pool.mu.Lock()
		delete(pool.allVMs, vmID)
		pool.mu.Unlock()
		pvm.Client.Close()
		return p.machinesMgr.StopVM(context.Background(), vmID)
	}

	return nil
}

// createVM 创建一个新的虚拟机并建立 vsock 连接。
func (p *Pool) createVM(ctx context.Context, runtime string) (*PooledVM, error) {
	pool := p.pools[runtime]

	// 创建 Firecracker 虚拟机
	vm, err := p.machinesMgr.CreateVM(ctx, runtime, int64(pool.config.MemoryMB), int64(pool.config.VCPUs))
	if err != nil {
		return nil, err
	}

	// 创建 vsock 客户端并连接
	client := fc.NewVsockClient(vm.VsockCID, p.logger)
	if err := client.Connect(ctx); err != nil {
		p.machinesMgr.StopVM(ctx, vm.ID)
		return nil, fmt.Errorf("failed to connect vsock: %w", err)
	}

	// 发送心跳验证连接
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx); err != nil {
		client.Close()
		p.machinesMgr.StopVM(ctx, vm.ID)
		return nil, fmt.Errorf("failed to ping agent: %w", err)
	}

	return &PooledVM{
		VM:        vm,
		Client:    client,
		Runtime:   runtime,
		Status:    "warm",
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
		UseCount:  0,
	}, nil
}

// createWarmVM 创建一个预热虚拟机并加入池中。
func (p *Pool) createWarmVM(runtime string) (*PooledVM, error) {
	ctx, cancel := context.WithTimeout(p.ctx, p.cfg.HealthCheckInterval)
	defer cancel()

	pvm, err := p.createVM(ctx, runtime)
	if err != nil {
		return nil, err
	}

	// 注册到池中
	pool := p.pools[runtime]
	pool.mu.Lock()
	pool.allVMs[pvm.VM.ID] = pvm
	pool.mu.Unlock()

	// 放入预热队列
	select {
	case pool.warmVMs <- pvm:
	default:
		// 队列已满
	}

	return pvm, nil
}

// healthCheckWorker 定期执行健康检查。
// 移除不健康或过期的虚拟机。
func (p *Pool) healthCheckWorker() {
	ticker := time.NewTicker(p.cfg.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.runHealthChecks()
		}
	}
}

// runHealthChecks 执行一轮健康检查。
func (p *Pool) runHealthChecks() {
	for runtime, pool := range p.pools {
		pool.mu.Lock()
		toRemove := make([]string, 0)

		for vmID, pvm := range pool.allVMs {
			// 只检查预热状态的虚拟机
			if pvm.Status != "warm" {
				continue
			}

			// 发送心跳检测
			ctx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
			if err := pvm.Client.Ping(ctx); err != nil {
				p.logger.WithFields(logrus.Fields{
					"vm_id":   vmID,
					"runtime": runtime,
				}).Warn("VM health check failed")
				toRemove = append(toRemove, vmID)
			}
			cancel()

			// 检查虚拟机年龄
			if time.Since(pvm.CreatedAt) > p.cfg.MaxVMAge {
				toRemove = append(toRemove, vmID)
			}
		}

		// 移除不健康或过期的虚拟机
		for _, vmID := range toRemove {
			pvm := pool.allVMs[vmID]
			delete(pool.allVMs, vmID)
			pvm.Client.Close()
			p.machinesMgr.StopVM(context.Background(), vmID)
		}

		pool.mu.Unlock()
	}
}

// scalingWorker 定期检查并执行扩缩容。
func (p *Pool) scalingWorker() {
	ticker := time.NewTicker(p.cfg.ScaleCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.checkScaling()
		}
	}
}

// checkScaling 检查并执行扩缩容操作。
// 当预热虚拟机数量低于最小阈值时，创建新的预热虚拟机。
func (p *Pool) checkScaling() {
	for runtime, pool := range p.pools {
		warmCount := len(pool.warmVMs)

		pool.mu.Lock()
		totalCount := len(pool.allVMs)
		pool.mu.Unlock()

		// 如果预热虚拟机不足且未达到上限，则扩容
		if warmCount < pool.config.MinWarm && totalCount < pool.config.MaxTotal {
			// 计算需要创建的数量
			toCreate := pool.config.TargetWarm - warmCount
			if toCreate > pool.config.MaxTotal-totalCount {
				toCreate = pool.config.MaxTotal - totalCount
			}

			// 并发创建虚拟机
			for i := 0; i < toCreate; i++ {
				go func(rt string) {
					if _, err := p.createWarmVM(rt); err != nil {
						p.logger.WithError(err).WithField("runtime", rt).Error("Failed to scale up VM")
					}
				}(runtime)
			}
		}
	}
}

// GetStats 获取所有运行时的池状态统计。
func (p *Pool) GetStats() map[string]PoolStats {
	stats := make(map[string]PoolStats)

	for runtime, pool := range p.pools {
		pool.mu.Lock()
		var warmCount, busyCount int
		for _, pvm := range pool.allVMs {
			switch pvm.Status {
			case "warm":
				warmCount++
			case "busy":
				busyCount++
			}
		}
		stats[runtime] = PoolStats{
			WarmVMs:  warmCount,
			BusyVMs:  busyCount,
			TotalVMs: len(pool.allVMs),
			MaxVMs:   pool.config.MaxTotal,
		}
		pool.mu.Unlock()
	}

	return stats
}

// PoolStats 表示池的状态统计信息。
type PoolStats struct {
	WarmVMs  int `json:"warm_vms"`  // 预热虚拟机数量
	BusyVMs  int `json:"busy_vms"`  // 忙碌虚拟机数量
	TotalVMs int `json:"total_vms"` // 总虚拟机数量
	MaxVMs   int `json:"max_vms"`   // 最大虚拟机数量
}

// SnapshotPool 是基于快照的虚拟机池。
// 通过预先创建的快照快速恢复虚拟机，实现更快的冷启动。
type SnapshotPool struct {
	pool      *Pool             // 底层虚拟机池
	snapshots map[string]string // 运行时到快照 ID 的映射
	mu        sync.RWMutex      // 保护 snapshots 的读写锁
}

// NewSnapshotPool 创建基于快照的虚拟机池。
func NewSnapshotPool(pool *Pool) *SnapshotPool {
	return &SnapshotPool{
		pool:      pool,
		snapshots: make(map[string]string),
	}
}

// CreateSnapshot 为指定运行时创建快照。
// 创建一个新虚拟机，然后对其创建快照。
func (sp *SnapshotPool) CreateSnapshot(ctx context.Context, runtime string) (string, error) {
	// 创建一个新的虚拟机
	pvm, err := sp.pool.createVM(ctx, runtime)
	if err != nil {
		return "", err
	}
	defer sp.pool.ReleaseVM(runtime, pvm.VM.ID)

	// 创建快照
	snapshotID := uuid.New().String()
	if err := sp.pool.machinesMgr.CreateSnapshot(ctx, pvm.VM.ID, snapshotID); err != nil {
		return "", err
	}

	// 记录快照
	sp.mu.Lock()
	sp.snapshots[runtime] = snapshotID
	sp.mu.Unlock()

	return snapshotID, nil
}

// AcquireFromSnapshot 从快照恢复一个虚拟机。
// 比从头创建虚拟机更快。
func (sp *SnapshotPool) AcquireFromSnapshot(ctx context.Context, runtime string) (*PooledVM, error) {
	sp.mu.RLock()
	snapshotID, ok := sp.snapshots[runtime]
	sp.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no snapshot for runtime: %s", runtime)
	}

	// 从快照恢复虚拟机
	vm, err := sp.pool.machinesMgr.RestoreFromSnapshot(ctx, snapshotID, runtime)
	if err != nil {
		return nil, err
	}

	// 建立 vsock 连接
	client := fc.NewVsockClient(vm.VsockCID, sp.pool.logger)
	if err := client.Connect(ctx); err != nil {
		sp.pool.machinesMgr.StopVM(ctx, vm.ID)
		return nil, err
	}

	return &PooledVM{
		VM:        vm,
		Client:    client,
		Runtime:   runtime,
		Status:    "busy",
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
		UseCount:  0,
	}, nil
}
