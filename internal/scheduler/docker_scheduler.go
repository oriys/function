// Package scheduler 提供函数调度器的实现。
// 该包负责管理函数调用请求的调度和执行，支持同步和异步两种调用模式。
// 调度器使用工作队列模式，通过多个工作协程并行处理函数调用请求。
// 本文件实现了基于 Docker 容器的调度器，适用于非 Linux 环境或不需要 Firecracker 的场景。
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/oriys/nimbus/internal/config"
	"github.com/oriys/nimbus/internal/domain"
	"github.com/oriys/nimbus/internal/metrics"
	"github.com/oriys/nimbus/internal/storage"
	"github.com/oriys/nimbus/internal/telemetry"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Executor 定义了函数执行器的接口。
// 不同的执行器实现（如 Docker、containerd 等）需要实现此接口。
type Executor interface {
	// Execute 执行函数并返回结果。
	//
	// 参数:
	//   - ctx: 上下文，用于控制执行超时和取消
	//   - fn: 要执行的函数定义
	//   - payload: 函数输入负载（JSON格式）
	//
	// 返回值:
	//   - *domain.InvokeResponse: 函数执行结果
	//   - error: 执行过程中的错误
	Execute(ctx context.Context, fn *domain.Function, payload json.RawMessage) (*domain.InvokeResponse, error)
}

// LayerExecutor 定义了支持函数层的执行器接口。
// 扩展了基本的 Executor 接口，增加了层支持。
type LayerExecutor interface {
	Executor
	// ExecuteWithLayers 执行带有层的函数。
	ExecuteWithLayers(ctx context.Context, fn *domain.Function, payload json.RawMessage, layers []domain.RuntimeLayerInfo) (*domain.InvokeResponse, error)
}

// DockerScheduler 是基于 Docker 容器的函数调度器。
// 与 Scheduler 不同，它使用 Docker 容器而非 Firecracker 虚拟机来执行函数，
// 适用于开发环境或不支持 Firecracker 的平台（如 macOS、Windows）。
type DockerScheduler struct {
	cfg      config.SchedulerConfig   // 调度器配置，包括工作协程数量、队列大小等
	store    *storage.PostgresStore   // PostgreSQL 存储，用于持久化函数和调用记录
	redis    *storage.RedisStore      // Redis 存储，用于异步调用的队列溢出处理
	executor Executor                 // 函数执行器，负责在 Docker 容器中运行函数
	metrics  *metrics.Metrics         // 指标收集器，用于记录调度器性能指标
	logger   *logrus.Logger           // 日志记录器

	workQueue chan *dockerWorkItem    // 工作队列，存放待处理的调用请求
	wg        sync.WaitGroup          // 等待组，用于优雅关闭时等待所有工作协程完成

	ctx    context.Context            // 调度器上下文，用于控制生命周期
	cancel context.CancelFunc         // 取消函数，用于停止调度器
}

// dockerWorkItem 表示 Docker 调度器的一个待处理工作项。
// 它封装了函数调用所需的所有信息。
type dockerWorkItem struct {
	invocation *domain.Invocation              // 调用记录，包含调用ID、输入参数等
	function   *domain.Function                // 函数定义，包含运行时、处理器、超时配置等
	resultCh   chan *domain.InvokeResponse     // 结果通道，用于同步调用时返回执行结果；异步调用时为 nil
}

// NewDockerScheduler 创建一个新的基于 Docker 的函数调度器实例。
//
// 参数:
//   - cfg: 调度器配置，包含工作协程数量、队列大小、默认超时等设置
//   - store: PostgreSQL 存储实例，用于持久化函数定义和调用记录
//   - redis: Redis 存储实例，用于处理工作队列溢出时的异步调用
//   - executor: 函数执行器实例，负责在 Docker 容器中执行函数
//   - m: 指标收集器，用于记录调度器运行指标
//   - logger: 日志记录器实例
//
// 返回值:
//   - *DockerScheduler: 初始化完成的调度器实例，调用 Start() 方法后开始处理请求
func NewDockerScheduler(
	cfg config.SchedulerConfig,
	store *storage.PostgresStore,
	redis *storage.RedisStore,
	executor Executor,
	m *metrics.Metrics,
	logger *logrus.Logger,
) *DockerScheduler {
	// 创建可取消的上下文，用于控制调度器的生命周期
	ctx, cancel := context.WithCancel(context.Background())

	return &DockerScheduler{
		cfg:       cfg,
		store:     store,
		redis:     redis,
		executor:  executor,
		metrics:   m,
		logger:    logger,
		workQueue: make(chan *dockerWorkItem, cfg.QueueSize), // 创建带缓冲的工作队列
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Start 启动 Docker 调度器，开始处理函数调用请求。
// 该方法会启动配置数量的工作协程，并开始指标收集。
//
// 返回值:
//   - error: 启动过程中的错误，当前实现始终返回 nil
func (s *DockerScheduler) Start() error {
	// 启动指定数量的工作协程
	for i := 0; i < s.cfg.Workers; i++ {
		s.wg.Add(1)
		go s.worker(i) // 每个工作协程使用唯一的ID
	}
	// 如果启用了指标收集，初始化工作协程数量指标并启动指标上报协程
	if s.metrics != nil {
		s.metrics.SchedulerWorkers.Set(float64(s.cfg.Workers))
		go s.metricsWorker()
	}
	s.logger.WithField("workers", s.cfg.Workers).Info("Docker scheduler started")
	return nil
}

// metricsWorker 定期收集并上报调度器队列大小指标。
// 该方法在独立的协程中运行，每秒更新一次队列大小。
func (s *DockerScheduler) metricsWorker() {
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

// Stop 优雅地停止 Docker 调度器。
// 该方法会：
//  1. 发送取消信号给所有工作协程
//  2. 关闭工作队列
//  3. 等待所有工作协程完成当前任务
//  4. 重置调度器指标
//
// 返回值:
//   - error: 停止过程中的错误，当前实现始终返回 nil
func (s *DockerScheduler) Stop() error {
	s.cancel()          // 发送取消信号
	close(s.workQueue)  // 关闭工作队列，通知工作协程退出
	s.wg.Wait()         // 等待所有工作协程完成
	// 重置指标
	if s.metrics != nil {
		s.metrics.SchedulerQueueSize.Set(0)
		s.metrics.SchedulerWorkers.Set(0)
	}
	s.logger.Info("Docker scheduler stopped")
	return nil
}

// Invoke 执行同步函数调用。
// 该方法会阻塞等待函数执行完成并返回结果，适用于需要立即获取响应的场景。
//
// 调用流程：
//  1. 从存储中获取函数定义
//  2. 创建调用记录并持久化
//  3. 将工作项提交到工作队列
//  4. 等待执行结果或超时
//
// 参数:
//   - req: 调用请求，包含函数ID和输入负载
//
// 返回值:
//   - *domain.InvokeResponse: 函数执行结果，包含状态码、响应体、执行时间等
//   - error: 调用过程中的错误，如函数不存在、队列已满等
func (s *DockerScheduler) Invoke(req *domain.InvokeRequest) (*domain.InvokeResponse, error) {
	// 从存储中获取函数定义
	fn, err := s.store.GetFunctionByID(req.FunctionID)
	if err != nil {
		return nil, err
	}

	// 创建调用记录，用于追踪调用状态和持久化
	inv := domain.NewInvocation(fn.ID, fn.Name, domain.TriggerHTTP, req.Payload)
	inv.ID = uuid.New().String()

	// 持久化调用记录
	if err := s.store.CreateInvocation(inv); err != nil {
		return nil, fmt.Errorf("failed to create invocation: %w", err)
	}

	// 创建工作项，包含结果通道用于接收执行结果
	resultCh := make(chan *domain.InvokeResponse, 1)
	item := &dockerWorkItem{
		invocation: inv,
		function:   fn,
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
	timeout := time.Duration(fn.TimeoutSec)*time.Second + 5*time.Second

	// 等待执行结果或超时
	select {
	case resp := <-resultCh:
		// 成功获取执行结果
		return resp, nil
	case <-time.After(timeout):
		// 超时处理：更新调用状态并返回超时响应
		inv.Timeout()
		s.store.UpdateInvocation(inv)
		return &domain.InvokeResponse{
			RequestID:  inv.ID,
			StatusCode: 504, // Gateway Timeout
			Error:      "function execution timed out",
		}, nil
	}
}

// InvokeAsync 执行异步函数调用。
// 该方法立即返回调用ID，函数在后台异步执行，适用于不需要等待结果的场景。
//
// 调用流程：
//  1. 从存储中获取函数定义
//  2. 创建调用记录并持久化
//  3. 将工作项提交到工作队列（如果队列满则推送到Redis）
//  4. 立即返回调用ID
//
// 参数:
//   - req: 调用请求，包含函数ID和输入负载
//
// 返回值:
//   - string: 调用ID，可用于后续查询调用状态和结果
//   - error: 调用过程中的错误，如函数不存在、队列和Redis都不可用等
func (s *DockerScheduler) InvokeAsync(req *domain.InvokeRequest) (string, error) {
	// 从存储中获取函数定义
	fn, err := s.store.GetFunctionByID(req.FunctionID)
	if err != nil {
		return "", err
	}

	// 创建调用记录
	inv := domain.NewInvocation(fn.ID, fn.Name, domain.TriggerHTTP, req.Payload)
	inv.ID = uuid.New().String()

	// 持久化调用记录
	if err := s.store.CreateInvocation(inv); err != nil {
		return "", fmt.Errorf("failed to create invocation: %w", err)
	}

	// 创建工作项，异步调用不需要结果通道
	item := &dockerWorkItem{
		invocation: inv,
		function:   fn,
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
			return "", fmt.Errorf("queue full and redis push failed: %w", err)
		}
		return inv.ID, nil
	}
}

// worker 是 Docker 调度器的工作协程主循环。
// 它持续从工作队列获取任务并处理，直到调度器停止或队列关闭。
//
// 参数:
//   - id: 工作协程的唯一标识符，用于日志和追踪
func (s *DockerScheduler) worker(id int) {
	defer s.wg.Done() // 协程退出时通知等待组

	for {
		select {
		case <-s.ctx.Done():
			// 收到停止信号，退出循环
			return
		case item, ok := <-s.workQueue:
			if !ok {
				// 工作队列已关闭，退出循环
				return
			}
			// 处理工作项
			s.processItem(id, item)
		}
	}
}

// processItem 处理单个工作项，执行函数调用的完整流程。
// 该方法负责：
//  1. 通过 Docker 执行器运行函数
//  2. 更新调用记录
//  3. 记录指标
//  4. 返回执行结果（同步调用时）
//
// 参数:
//   - workerID: 工作协程ID
//   - item: 待处理的工作项
func (s *DockerScheduler) processItem(workerID int, item *dockerWorkItem) {
	inv := item.invocation
	fn := item.function

	// 启动分布式追踪 span，用于监控函数调用链路
	tracer := telemetry.GetTracer("function-scheduler")
	ctx, span := tracer.Start(s.ctx, "function.invoke",
		trace.WithAttributes(
			attribute.String("function.id", fn.ID),
			attribute.String("function.name", fn.Name),
			attribute.String("function.runtime", string(fn.Runtime)),
			attribute.String("invocation.id", inv.ID),
			attribute.Int("worker.id", workerID),
		),
	)
	defer span.End()

	// 创建带有追踪上下文的日志记录器
	logger := s.logger.WithFields(logrus.Fields{
		"worker_id":     workerID,
		"invocation_id": inv.ID,
		"function_id":   fn.ID,
		"function_name": fn.Name,
	})
	logger = telemetry.EntryWithTraceContext(ctx, logger)

	// 标记调用状态为运行中
	// 注意：Docker 模式下默认为冷启动，实际值在执行后更新
	inv.Start("docker", true)
	s.store.UpdateInvocation(inv)
	span.AddEvent("invocation.started")

	// 获取函数关联的层
	functionLayers, err := s.store.GetFunctionLayers(fn.ID)
	if err != nil {
		logger.WithError(err).Warn("Failed to get function layers")
		functionLayers = nil
	}

	// 获取每个层的内容
	var layerInfos []domain.RuntimeLayerInfo
	for _, fl := range functionLayers {
		content, err := s.store.GetLayerVersionContent(fl.LayerID, fl.LayerVersion)
		if err != nil {
			logger.WithError(err).WithFields(logrus.Fields{
				"layer_id":      fl.LayerID,
				"layer_version": fl.LayerVersion,
			}).Error("Failed to get layer content")
			continue
		}
		layerInfos = append(layerInfos, domain.RuntimeLayerInfo{
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

	// 创建带函数超时的执行上下文
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(fn.TimeoutSec)*time.Second)
	defer cancel()

	// 通过 Docker 执行器执行函数
	span.AddEvent("execution.start")

	var resp *domain.InvokeResponse
	// 如果有层且执行器支持层，使用 ExecuteWithLayers
	if len(layerInfos) > 0 {
		if layerExec, ok := s.executor.(LayerExecutor); ok {
			resp, err = layerExec.ExecuteWithLayers(execCtx, fn, inv.Input, layerInfos)
		} else {
			logger.Warn("Executor does not support layers, executing without layers")
			resp, err = s.executor.Execute(execCtx, fn, inv.Input)
		}
	} else {
		resp, err = s.executor.Execute(execCtx, fn, inv.Input)
	}

	if err != nil {
		// 执行失败，处理错误类型
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		logger.WithError(err).Error("Function execution failed")

		// 区分超时错误和其他错误
		statusCode := 500
		errType := "executor_error"
		if errors.Is(err, context.DeadlineExceeded) {
			statusCode = 504 // Gateway Timeout
			errType = "timeout"
		}
		s.fail(item, fmt.Sprintf("execution failed: %v", err), statusCode, errType)
		return
	}
	span.AddEvent("execution.complete")

	// 根据执行器返回的实际结果更新冷启动标志
	// （例如：容器复用时为热启动，新创建容器时为冷启动）
	inv.ColdStart = resp.ColdStart

	// 添加执行结果到追踪 span
	span.SetAttributes(
		attribute.Bool("invocation.cold_start", resp.ColdStart),
		attribute.Int("invocation.status_code", resp.StatusCode),
		attribute.Int64("invocation.duration_ms", resp.DurationMs),
	)

	// 更新调用记录
	if resp.StatusCode == 200 {
		// 函数执行成功
		inv.Complete(resp.Body, 0)
	} else {
		// 函数执行返回错误，记录详细日志
		logger.WithFields(logrus.Fields{
			"status_code":  resp.StatusCode,
			"error":        resp.Error,
			"duration_ms":  resp.DurationMs,
			"cold_start":   resp.ColdStart,
			"function_id":  fn.ID,
			"function_name": fn.Name,
		}).Error("Function returned error status")
		inv.Fail(resp.Error)
	}
	inv.DurationMs = resp.DurationMs
	inv.BilledTimeMs = resp.BilledTimeMs
	s.store.UpdateInvocation(inv)

	// 记录调用指标
	if s.metrics != nil {
		statusStr := strconv.Itoa(resp.StatusCode)
		s.metrics.RecordInvocation(fn.ID, fn.Name, string(fn.Runtime), statusStr, float64(resp.DurationMs), inv.ColdStart)
		// 记录非 2xx 状态码的错误
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			s.metrics.RecordError(fn.ID, fn.Name, "function_error")
		}
	}

	// 如果是同步调用，通过结果通道返回响应
	if item.resultCh != nil {
		resp.RequestID = inv.ID
		item.resultCh <- resp
	}

	logger.WithFields(logrus.Fields{
		"duration_ms": resp.DurationMs,
		"status_code": resp.StatusCode,
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
func (s *DockerScheduler) fail(item *dockerWorkItem, errMsg string, statusCode int, errorType string) {
	// 根据状态码更新调用状态
	if statusCode == 504 {
		item.invocation.Timeout() // 超时
	} else {
		item.invocation.Fail(errMsg) // 其他错误
	}
	s.store.UpdateInvocation(item.invocation)

	// 记录错误指标
	if s.metrics != nil {
		s.metrics.RecordInvocation(
			item.function.ID,
			item.function.Name,
			string(item.function.Runtime),
			strconv.Itoa(statusCode),
			float64(item.invocation.DurationMs),
			item.invocation.ColdStart,
		)
		s.metrics.RecordError(item.function.ID, item.function.Name, errorType)
	}

	// 如果是同步调用，通过结果通道返回错误响应
	if item.resultCh != nil {
		item.resultCh <- &domain.InvokeResponse{
			RequestID:  item.invocation.ID,
			StatusCode: statusCode,
			Error:      errMsg,
			DurationMs: item.invocation.DurationMs,
			ColdStart:  item.invocation.ColdStart,
			BilledTimeMs: item.invocation.BilledTimeMs,
		}
	}
}
