//go:build linux
// +build linux

// Package scheduler 提供函数调度器的实现。
// 该包负责管理函数调用请求的调度和执行，支持同步和异步两种调用模式。
// 调度器使用工作队列模式，通过多个工作协程并行处理函数调用请求。
// 主要功能包括：
//   - 函数调用请求的排队和分发
//   - 虚拟机资源的获取和释放
//   - 函数执行状态的追踪和记录
//   - 调用指标的收集和上报
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/oriys/nimbus/internal/config"
	"github.com/oriys/nimbus/internal/domain"
	fc "github.com/oriys/nimbus/internal/firecracker"
	"github.com/oriys/nimbus/internal/metrics"
	"github.com/oriys/nimbus/internal/snapshot"
	"github.com/oriys/nimbus/internal/storage"
	"github.com/oriys/nimbus/internal/telemetry"
	"github.com/oriys/nimbus/internal/vmpool"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Scheduler 是基于 Firecracker 虚拟机的函数调度器。
// 它负责接收函数调用请求，将其分配给工作协程处理，
// 并管理虚拟机资源的获取和释放。
// Scheduler 支持同步调用（等待结果返回）和异步调用（立即返回调用ID）两种模式。
type Scheduler struct {
	cfg       config.SchedulerConfig   // 调度器配置，包括工作协程数量、队列大小等
	store     *storage.PostgresStore   // PostgreSQL 存储，用于持久化函数和调用记录
	redis     *storage.RedisStore      // Redis 存储，用于异步调用的队列溢出处理
	pool      *vmpool.Pool             // 虚拟机池，管理 Firecracker 虚拟机资源
	router    *TrafficRouter           // 流量路由器，用于版本选择和流量分配
	snapshotMgr *snapshot.Manager      // 快照管理器，用于函数级快照
	metrics   *metrics.Metrics         // 指标收集器，用于记录调度器性能指标
	logger    *logrus.Logger           // 日志记录器

	workQueue chan *workItem           // 工作队列，存放待处理的调用请求
	workers   []*worker                // 工作协程列表
	wg        sync.WaitGroup           // 等待组，用于优雅关闭时等待所有工作协程完成

	ctx    context.Context             // 调度器上下文，用于控制生命周期
	cancel context.CancelFunc          // 取消函数，用于停止调度器
}

// workItem 表示一个待处理的工作项。
// 它封装了函数调用所需的所有信息，包括调用记录、函数定义和结果通道。
type workItem struct {
	invocation *domain.Invocation              // 调用记录，包含调用ID、输入参数等
	function   *domain.Function                // 函数定义，包含运行时、处理器、超时配置等
	version    *domain.FunctionVersion         // 要执行的版本（如果指定了版本/别名）
	resultCh   chan *domain.InvokeResponse     // 结果通道，用于同步调用时返回执行结果；异步调用时为 nil
}

// worker 表示一个工作协程。
// 每个 worker 从工作队列中获取任务并处理，直到调度器停止。
type worker struct {
	id        int         // 工作协程的唯一标识符
	scheduler *Scheduler  // 所属的调度器实例
}

// NewScheduler 创建一个新的函数调度器实例。
//
// 参数:
//   - cfg: 调度器配置，包含工作协程数量、队列大小、默认超时等设置
//   - store: PostgreSQL 存储实例，用于持久化函数定义和调用记录
//   - redis: Redis 存储实例，用于处理工作队列溢出时的异步调用
//   - pool: 虚拟机池实例，管理 Firecracker 虚拟机资源
//   - m: 指标收集器，用于记录调度器运行指标
//   - logger: 日志记录器实例
//
// 返回值:
//   - *Scheduler: 初始化完成的调度器实例，调用 Start() 方法后开始处理请求
func NewScheduler(
	cfg config.SchedulerConfig,
	store *storage.PostgresStore,
	redis *storage.RedisStore,
	pool *vmpool.Pool,
	m *metrics.Metrics,
	logger *logrus.Logger,
) *Scheduler {
	// 创建可取消的上下文，用于控制调度器的生命周期
	ctx, cancel := context.WithCancel(context.Background())

	// 初始化调度器实例
	s := &Scheduler{
		cfg:       cfg,
		store:     store,
		redis:     redis,
		pool:      pool,
		router:    NewTrafficRouter(store, logger),
		metrics:   m,
		logger:    logger,
		workQueue: make(chan *workItem, cfg.QueueSize), // 创建带缓冲的工作队列
		ctx:       ctx,
		cancel:    cancel,
	}

	return s
}

// Start 启动调度器，开始处理函数调用请求。
// 该方法会启动配置数量的工作协程，并开始指标收集。
//
// 返回值:
//   - error: 启动过程中的错误，当前实现始终返回 nil
func (s *Scheduler) Start() error {
	// 启动工作协程池
	s.workers = make([]*worker, s.cfg.Workers)
	for i := 0; i < s.cfg.Workers; i++ {
		w := &worker{id: i, scheduler: s}
		s.workers[i] = w
		s.wg.Add(1)
		go w.run() // 启动工作协程
	}
	// 如果启用了指标收集，初始化工作协程数量指标并启动指标上报协程
	if s.metrics != nil {
		s.metrics.SchedulerWorkers.Set(float64(s.cfg.Workers))
		go s.metricsWorker()
	}

	s.logger.WithField("workers", s.cfg.Workers).Info("Scheduler started")
	return nil
}

// metricsWorker 定期收集并上报调度器队列大小指标。
// 该方法在独立的协程中运行，每秒更新一次队列大小。
func (s *Scheduler) metricsWorker() {
	ticker := time.NewTicker(1 * time.Second) // 创建1秒间隔的定时器
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			// 调度器停止，退出指标收集循环
			return
		case <-ticker.C:
			// 更新队列大小指标
			s.metrics.SchedulerQueueSize.Set(float64(len(s.workQueue)))
		}
	}
}

// Stop 优雅地停止调度器。
// 该方法会：
//  1. 发送取消信号给所有工作协程
//  2. 关闭工作队列
//  3. 等待所有工作协程完成当前任务
//  4. 重置调度器指标
//
// 返回值:
//   - error: 停止过程中的错误，当前实现始终返回 nil
func (s *Scheduler) Stop() error {
	s.cancel()          // 发送取消信号
	close(s.workQueue)  // 关闭工作队列，通知工作协程退出
	s.wg.Wait()         // 等待所有工作协程完成
	// 重置指标
	if s.metrics != nil {
		s.metrics.SchedulerQueueSize.Set(0)
		s.metrics.SchedulerWorkers.Set(0)
	}
	s.logger.Info("Scheduler stopped")
	return nil
}

// Invoke 执行同步函数调用。
// 该方法会阻塞等待函数执行完成并返回结果，适用于需要立即获取响应的场景。
//
// 调用流程：
//  1. 从存储中获取函数定义
//  2. 解析版本（根据 alias/version 参数或默认 latest）
//  3. 创建调用记录并持久化
//  4. 将工作项提交到工作队列
//  5. 等待执行结果或超时
//
// 参数:
//   - req: 调用请求，包含函数ID和输入负载
//
// 返回值:
//   - *domain.InvokeResponse: 函数执行结果，包含状态码、响应体、执行时间等
//   - error: 调用过程中的错误，如函数不存在、队列已满等
func (s *Scheduler) Invoke(req *domain.InvokeRequest) (*domain.InvokeResponse, error) {
	// 从存储中获取函数定义
	fn, err := s.store.GetFunctionByID(req.FunctionID)
	if err != nil {
		return nil, err
	}

	// 解析版本
	version, aliasUsed, versionData, err := s.resolveVersion(fn, req)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve version: %w", err)
	}

	// 创建调用记录，用于追踪调用状态和持久化
	inv := domain.NewInvocation(fn.ID, fn.Name, domain.TriggerHTTP, req.Payload)
	inv.ID = uuid.New().String()
	inv.Version = version
	inv.AliasUsed = aliasUsed
	inv.SessionKey = req.SessionKey // 设置会话标识（有状态函数）

	// 持久化调用记录
	if err := s.store.CreateInvocation(inv); err != nil {
		return nil, fmt.Errorf("failed to create invocation: %w", err)
	}

	// 创建工作项，包含结果通道用于接收执行结果
	resultCh := make(chan *domain.InvokeResponse, 1)
	item := &workItem{
		invocation: inv,
		function:   fn,
		version:    versionData,
		resultCh:   resultCh,
	}

	// 非阻塞方式提交工作项到队列
	select {
	case s.workQueue <- item:
		// 成功提交到队列
	default:
		// 队列已满，返回错误
		return nil, fmt.Errorf("work queue is full")
	}

	// 计算超时时间：函数配置的超时 + 5秒缓冲
	timeout := time.Duration(fn.TimeoutSec) * time.Second
	if timeout == 0 {
		timeout = s.cfg.DefaultTimeout // 使用默认超时
	}

	// 等待执行结果或超时
	select {
	case resp := <-resultCh:
		// 成功获取执行结果
		return resp, nil
	case <-time.After(timeout + 5*time.Second):
		// 超时处理：更新调用状态并返回超时响应
		inv.Timeout()
		s.store.UpdateInvocation(inv)
		return &domain.InvokeResponse{
			RequestID:  inv.ID,
			StatusCode: 504, // Gateway Timeout
			Error:      "function execution timed out",
			Version:    version,
			AliasUsed:  aliasUsed,
			SessionKey: req.SessionKey,
		}, nil
	}
}

// InvokeAsync 执行异步函数调用。
// 该方法立即返回调用ID，函数在后台异步执行，适用于不需要等待结果的场景。
//
// 调用流程：
//  1. 从存储中获取函数定义
//  2. 解析版本（根据 alias/version 参数或默认 latest）
//  3. 创建调用记录并持久化
//  4. 将工作项提交到工作队列（如果队列满则推送到Redis）
//  5. 立即返回调用ID
//
// 参数:
//   - req: 调用请求，包含函数ID和输入负载
//
// 返回值:
//   - string: 调用ID，可用于后续查询调用状态和结果
//   - error: 调用过程中的错误，如函数不存在、队列和Redis都不可用等
func (s *Scheduler) InvokeAsync(req *domain.InvokeRequest) (string, error) {
	// 从存储中获取函数定义
	fn, err := s.store.GetFunctionByID(req.FunctionID)
	if err != nil {
		return "", err
	}

	// 解析版本
	version, aliasUsed, versionData, err := s.resolveVersion(fn, req)
	if err != nil {
		return "", fmt.Errorf("failed to resolve version: %w", err)
	}

	// 创建调用记录
	inv := domain.NewInvocation(fn.ID, fn.Name, domain.TriggerHTTP, req.Payload)
	inv.ID = uuid.New().String()
	inv.Version = version
	inv.AliasUsed = aliasUsed
	inv.SessionKey = req.SessionKey // 设置会话标识（有状态函数）

	// 持久化调用记录
	if err := s.store.CreateInvocation(inv); err != nil {
		return "", fmt.Errorf("failed to create invocation: %w", err)
	}

	// 创建工作项，异步调用不需要结果通道
	item := &workItem{
		invocation: inv,
		function:   fn,
		version:    versionData,
		resultCh:   nil, // 异步调用不需要等待结果
	}

	// 尝试提交到工作队列
	select {
	case s.workQueue <- item:
		// 成功提交到队列
		return inv.ID, nil
	default:
		// 队列已满，将调用ID推送到Redis作为备用队列
		// 后续可由其他工作进程从Redis拉取并处理
		if err := s.redis.PushInvocation(context.Background(), inv.ID); err != nil {
			return "", fmt.Errorf("work queue is full and failed to push to redis: %w", err)
		}
		return inv.ID, nil
	}
}

// resolveVersion 解析要执行的版本
// 优先级：显式指定版本 > 别名 > 默认 latest
func (s *Scheduler) resolveVersion(fn *domain.Function, req *domain.InvokeRequest) (version int, alias string, versionData *domain.FunctionVersion, err error) {
	ctx := context.Background()

	// 优先使用显式指定的版本号
	if req.Version > 0 {
		versionData, err = s.store.GetFunctionVersion(fn.ID, req.Version)
		if err != nil {
			return 0, "", nil, fmt.Errorf("version %d not found: %w", req.Version, err)
		}
		return req.Version, "", versionData, nil
	}

	// 使用别名解析版本
	aliasName := req.Alias
	if aliasName == "" {
		aliasName = "latest" // 默认使用 latest 别名
	}

	// 尝试通过路由器选择版本
	version, err = s.router.SelectVersion(ctx, fn.ID, aliasName)
	if err != nil {
		// 如果别名不存在，回退到函数当前版本
		s.logger.WithFields(logrus.Fields{
			"function_id": fn.ID,
			"alias":       aliasName,
			"error":       err.Error(),
		}).Debug("Alias not found, falling back to current function version")

		// 使用函数当前的代码（不从版本表加载）
		return fn.Version, "", nil, nil
	}

	// 加载版本数据
	versionData, err = s.store.GetFunctionVersion(fn.ID, version)
	if err != nil {
		return 0, "", nil, fmt.Errorf("failed to load version %d: %w", version, err)
	}

	return version, aliasName, versionData, nil
}

// Router 返回流量路由器实例
func (s *Scheduler) Router() *TrafficRouter {
	return s.router
}

// SetSnapshotManager 设置快照管理器
func (s *Scheduler) SetSnapshotManager(mgr *snapshot.Manager) {
	s.snapshotMgr = mgr
}

// SnapshotManager 返回快照管理器实例
func (s *Scheduler) SnapshotManager() *snapshot.Manager {
	return s.snapshotMgr
}

// OnFunctionDeployed 函数部署后触发快照构建
func (s *Scheduler) OnFunctionDeployed(ctx context.Context, fn *domain.Function, version int) {
	if s.snapshotMgr != nil {
		// 异步构建快照
		go func() {
			if err := s.snapshotMgr.RequestBuild(fn, version); err != nil {
				s.logger.WithError(err).WithFields(logrus.Fields{
					"function_id": fn.ID,
					"version":     version,
				}).Warn("Failed to queue snapshot build")
			}
		}()
	}
}

// OnFunctionUpdated 函数更新后使旧快照失效
func (s *Scheduler) OnFunctionUpdated(ctx context.Context, fn *domain.Function) {
	if s.snapshotMgr != nil {
		go func() {
			if err := s.snapshotMgr.InvalidateSnapshots(ctx, fn.ID); err != nil {
				s.logger.WithError(err).WithField("function_id", fn.ID).Warn("Failed to invalidate snapshots")
			}
		}()
	}
}

// run 是工作协程的主循环。
// 它持续从工作队列获取任务并处理，直到调度器停止或队列关闭。
func (w *worker) run() {
	defer w.scheduler.wg.Done() // 协程退出时通知等待组

	for {
		select {
		case <-w.scheduler.ctx.Done():
			// 收到停止信号，退出循环
			return
		case item, ok := <-w.scheduler.workQueue:
			if !ok {
				// 工作队列已关闭，退出循环
				return
			}
			// 处理工作项
			w.process(item)
		}
	}
}

// process 处理单个工作项，执行函数调用的完整流程。
// 该方法负责：
//  1. 从虚拟机池获取可用虚拟机
//  2. 在虚拟机中初始化函数
//  3. 执行函数并收集结果
//  4. 释放虚拟机资源
//  5. 更新调用记录和指标
//
// 参数:
//   - item: 待处理的工作项
func (w *worker) process(item *workItem) {
	inv := item.invocation
	fn := item.function

	// 启动分布式追踪 span，用于监控函数调用链路
	tracer := telemetry.GetTracer("function-scheduler")
	ctx, span := tracer.Start(w.scheduler.ctx, "function.invoke",
		trace.WithAttributes(
			attribute.String("function.id", fn.ID),
			attribute.String("function.name", fn.Name),
			attribute.String("function.runtime", string(fn.Runtime)),
			attribute.String("invocation.id", inv.ID),
			attribute.Int("invocation.version", inv.Version),
			attribute.String("invocation.alias", inv.AliasUsed),
			attribute.Int("worker.id", w.id),
		),
	)
	defer span.End()

	// 创建带有追踪上下文的日志记录器
	logger := w.scheduler.logger.WithFields(logrus.Fields{
		"worker_id":     w.id,
		"invocation_id": inv.ID,
		"function_id":   fn.ID,
		"function_name": fn.Name,
		"version":       inv.Version,
		"alias":         inv.AliasUsed,
	})
	logger = telemetry.EntryWithTraceContext(ctx, logger)

	// ========== 阶段1：获取虚拟机 ==========
	span.AddEvent("vm.acquire.start")
	// 创建带超时的上下文，防止无限等待虚拟机
	acquireCtx, cancel := context.WithTimeout(ctx, w.scheduler.cfg.DefaultTimeout)
	defer cancel()

	// 从虚拟机池获取可用虚拟机
	// coldStart 表示是否是冷启动（新创建的虚拟机）
	pvm, coldStart, err := w.scheduler.pool.AcquireVM(acquireCtx, string(fn.Runtime))
	if err != nil {
		// 获取虚拟机失败，记录错误并返回失败响应
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to acquire VM")
		logger.WithError(err).Error("Failed to acquire VM")
		w.fail(item, fmt.Sprintf("failed to acquire VM: %v", err), 500, "acquire_vm_failed")
		return
	}
	span.AddEvent("vm.acquire.complete", trace.WithAttributes(
		attribute.Bool("cold_start", coldStart),
		attribute.String("vm.id", pvm.VM.ID),
	))

	// 更新调用状态为运行中
	inv.Start(pvm.VM.ID, coldStart)
	w.scheduler.store.UpdateInvocation(inv)

	logger = logger.WithField("vm_id", pvm.VM.ID)
	logger.Debug("VM acquired")

	// ========== 阶段2：初始化函数 ==========
	span.AddEvent("function.init.start")

	// 获取函数关联的层
	functionLayers, err := w.scheduler.store.GetFunctionLayers(fn.ID)
	if err != nil {
		logger.WithError(err).Warn("Failed to get function layers")
		functionLayers = nil
	}

	// 获取每个层的内容
	var layerInfos []fc.LayerInfo
	for _, fl := range functionLayers {
		content, err := w.scheduler.store.GetLayerVersionContent(fl.LayerID, fl.LayerVersion)
		if err != nil {
			logger.WithError(err).WithFields(logrus.Fields{
				"layer_id":      fl.LayerID,
				"layer_version": fl.LayerVersion,
			}).Error("Failed to get layer content")
			continue
		}
		layerInfos = append(layerInfos, fc.LayerInfo{
			LayerID: fl.LayerID,
			Version: fl.LayerVersion,
			Content: content,
			Order:   fl.Order,
		})
		logger.WithFields(logrus.Fields{
			"layer_id":      fl.LayerID,
			"layer_version": fl.LayerVersion,
			"layer_size":    len(content),
		}).Debug("Layer content loaded")
	}

	// 构建函数初始化负载
	// 如果指定了版本，使用版本数据；否则使用函数当前代码
	var initPayload *fc.InitPayload
	if item.version != nil {
		// 使用指定版本的代码和配置
		initPayload = &fc.InitPayload{
			FunctionID:    fn.ID,
			Handler:       item.version.Handler,
			Code:          item.version.Code,
			Runtime:       string(fn.Runtime),
			EnvVars:       fn.EnvVars, // 环境变量使用函数级别的
			MemoryLimitMB: fn.MemoryMB,
			TimeoutSec:    fn.TimeoutSec,
			Layers:        layerInfos,
		}
		logger.WithField("version", item.version.Version).Debug("Using version-specific code")
	} else {
		// 使用函数当前代码
		initPayload = &fc.InitPayload{
			FunctionID:    fn.ID,
			Handler:       fn.Handler,
			Code:          fn.Code,
			Runtime:       string(fn.Runtime),
			EnvVars:       fn.EnvVars,
			MemoryLimitMB: fn.MemoryMB,
			TimeoutSec:    fn.TimeoutSec,
			Layers:        layerInfos,
		}
	}

	// 在虚拟机中初始化函数运行环境
	if err := pvm.Client.InitFunction(ctx, initPayload); err != nil {
		// 初始化失败，释放虚拟机并返回错误
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to initialize function")
		logger.WithError(err).Error("Failed to initialize function")
		w.scheduler.pool.ReleaseVM(string(fn.Runtime), pvm.VM.ID)
		w.fail(item, fmt.Sprintf("failed to initialize function: %v", err), 500, "init_failed")
		return
	}
	span.AddEvent("function.init.complete")

	// ========== 阶段3：执行函数 ==========
	span.AddEvent("function.execute.start")
	// 创建带函数超时的执行上下文
	execCtx, execCancel := context.WithTimeout(ctx, time.Duration(fn.TimeoutSec)*time.Second)
	defer execCancel()

	// 调用函数并等待结果
	resp, err := pvm.Client.Execute(execCtx, inv.ID, inv.Input)
	if err != nil {
		// 执行失败，处理错误类型
		span.RecordError(err)
		span.SetStatus(codes.Error, "function execution failed")
		logger.WithError(err).Error("Function execution failed")
		w.scheduler.pool.ReleaseVM(string(fn.Runtime), pvm.VM.ID)

		// 区分超时错误和其他错误
		statusCode := 500
		errType := "execute_failed"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			statusCode = 504 // Gateway Timeout
			errType = "timeout"
		}
		w.fail(item, fmt.Sprintf("function execution failed: %v", err), statusCode, errType)
		return
	}
	span.AddEvent("function.execute.complete")

	// 添加执行结果到追踪 span
	span.SetAttributes(
		attribute.Bool("invocation.cold_start", coldStart),
		attribute.Bool("invocation.success", resp.Success),
		attribute.Int64("invocation.duration_ms", int64(inv.DurationMs)),
	)

	// ========== 阶段4：释放虚拟机 ==========
	if err := w.scheduler.pool.ReleaseVM(string(fn.Runtime), pvm.VM.ID); err != nil {
		// 释放失败只记录警告，不影响调用结果
		logger.WithError(err).Warn("Failed to release VM")
	}

	// ========== 阶段5：更新调用记录 ==========
	if resp.Success {
		// 函数执行成功
		inv.Complete(resp.Output, resp.MemoryUsedMB)
	} else {
		// 函数执行返回错误
		inv.Fail(resp.Error)
	}
	w.scheduler.store.UpdateInvocation(inv)

	// 记录调用指标
	if w.scheduler.metrics != nil {
		statusCode := 200
		if !resp.Success {
			statusCode = 500
			w.scheduler.metrics.RecordError(fn.ID, fn.Name, "function_error")
		}
		w.scheduler.metrics.RecordInvocation(
			fn.ID,
			fn.Name,
			string(fn.Runtime),
			strconv.Itoa(statusCode),
			float64(inv.DurationMs),
			coldStart,
		)
	}

	// 如果是同步调用，通过结果通道返回响应
	if item.resultCh != nil {
		statusCode := 200
		if !resp.Success {
			statusCode = 500
		}

		item.resultCh <- &domain.InvokeResponse{
			RequestID:    inv.ID,
			StatusCode:   statusCode,
			Body:         resp.Output,
			Error:        resp.Error,
			DurationMs:   inv.DurationMs,
			ColdStart:    coldStart,
			BilledTimeMs: inv.BilledTimeMs,
			Version:      inv.Version,
			AliasUsed:    inv.AliasUsed,
			SessionKey:   inv.SessionKey,
		}
	}

	logger.WithFields(logrus.Fields{
		"duration_ms": inv.DurationMs,
		"cold_start":  coldStart,
		"success":     resp.Success,
	}).Info("Invocation completed")
}

// fail 处理工作项执行失败的情况。
// 该方法负责更新调用状态、记录指标，并在同步调用时返回错误响应。
//
// 参数:
//   - item: 失败的工作项
//   - errMsg: 错误消息
//   - statusCode: HTTP状态码（500=内部错误，504=超时）
//   - errorType: 错误类型，用于指标分类
func (w *worker) fail(item *workItem, errMsg string, statusCode int, errorType string) {
	// 根据状态码更新调用状态
	if statusCode == 504 {
		item.invocation.Timeout() // 超时
	} else {
		item.invocation.Fail(errMsg) // 其他错误
	}
	w.scheduler.store.UpdateInvocation(item.invocation)

	// 记录错误指标
	if w.scheduler.metrics != nil {
		w.scheduler.metrics.RecordInvocation(
			item.function.ID,
			item.function.Name,
			string(item.function.Runtime),
			strconv.Itoa(statusCode),
			float64(item.invocation.DurationMs),
			item.invocation.ColdStart,
		)
		w.scheduler.metrics.RecordError(item.function.ID, item.function.Name, errorType)
	}

	// 如果是同步调用，通过结果通道返回错误响应
	if item.resultCh != nil {
		item.resultCh <- &domain.InvokeResponse{
			RequestID:    item.invocation.ID,
			StatusCode:   statusCode,
			Error:        errMsg,
			DurationMs:   item.invocation.DurationMs,
			ColdStart:    item.invocation.ColdStart,
			BilledTimeMs: item.invocation.BilledTimeMs,
			Version:      item.invocation.Version,
			AliasUsed:    item.invocation.AliasUsed,
			SessionKey:   item.invocation.SessionKey,
		}
	}
}

// Stats 返回调度器的当前统计信息。
// 可用于健康检查和监控。
//
// 返回值:
//   - SchedulerStats: 包含队列长度、队列容量和工作协程数量的统计信息
func (s *Scheduler) Stats() SchedulerStats {
	return SchedulerStats{
		QueueLength: len(s.workQueue), // 当前队列中的任务数
		QueueCap:    cap(s.workQueue), // 队列最大容量
		Workers:     len(s.workers),   // 工作协程数量
	}
}

// SchedulerStats 包含调度器的运行时统计信息。
// 用于监控调度器的健康状态和负载情况。
type SchedulerStats struct {
	QueueLength int `json:"queue_length"` // 当前队列中等待处理的任务数量
	QueueCap    int `json:"queue_cap"`    // 队列的最大容量
	Workers     int `json:"workers"`      // 活跃的工作协程数量
}
