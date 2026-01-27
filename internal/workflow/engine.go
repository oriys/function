// Package workflow 实现了工作流编排引擎。
// 该包提供类似 AWS Step Functions 的工作流编排功能，支持将多个函数组合成复杂业务流程。
package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/oriys/nimbus/internal/domain"
	"github.com/oriys/nimbus/internal/storage"
	"github.com/sirupsen/logrus"
)

// Scheduler 定义了函数调度器接口
type Scheduler interface {
	Invoke(req *domain.InvokeRequest) (*domain.InvokeResponse, error)
	InvokeAsync(req *domain.InvokeRequest) (string, error)
}

// Config 工作流引擎配置
type Config struct {
	// Workers Worker Pool 的工作线程数
	Workers int
	// QueueSize 执行队列大小
	QueueSize int
	// DefaultTimeout 默认执行超时时间（秒）
	DefaultTimeout int
	// RecoveryEnabled 是否启用执行恢复
	RecoveryEnabled bool
	// RecoveryInterval 恢复检查间隔
	RecoveryInterval time.Duration
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		Workers:          10,
		QueueSize:        1000,
		DefaultTimeout:   3600,
		RecoveryEnabled:  true,
		RecoveryInterval: 30 * time.Second,
	}
}

// executionTask 执行任务
type executionTask struct {
	execution   *domain.WorkflowExecution
	workflow    *domain.Workflow
	resumeState string
	resumeInput json.RawMessage
}

// Engine 工作流引擎
type Engine struct {
	config    Config
	store     *storage.PostgresStore
	scheduler Scheduler
	logger    *logrus.Logger

	// 执行队列和控制
	executionQueue chan *executionTask
	workers        int
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup

	// 执行器
	executor *Executor
}

// NewEngine 创建工作流引擎实例
func NewEngine(config Config, store *storage.PostgresStore, scheduler Scheduler, logger *logrus.Logger) *Engine {
	ctx, cancel := context.WithCancel(context.Background())

	if config.Workers <= 0 {
		config.Workers = DefaultConfig().Workers
	}
	if config.QueueSize <= 0 {
		config.QueueSize = DefaultConfig().QueueSize
	}
	if config.DefaultTimeout <= 0 {
		config.DefaultTimeout = DefaultConfig().DefaultTimeout
	}

	engine := &Engine{
		config:         config,
		store:          store,
		scheduler:      scheduler,
		logger:         logger,
		executionQueue: make(chan *executionTask, config.QueueSize),
		workers:        config.Workers,
		ctx:            ctx,
		cancel:         cancel,
	}

	// 创建执行器
	engine.executor = NewExecutor(store, scheduler, logger)

	return engine
}

// Start 启动工作流引擎
func (e *Engine) Start() error {
	e.logger.WithField("workers", e.workers).Info("Starting workflow engine")

	// 启动 Worker Pool
	for i := 0; i < e.workers; i++ {
		e.wg.Add(1)
		go e.worker(i)
	}

	// 启动执行恢复
	if e.config.RecoveryEnabled {
		e.wg.Add(1)
		go e.recoveryLoop()
	}

	// 加载默认工作流
	if err := e.SeedDefaultWorkflows(); err != nil {
		e.logger.WithError(err).Warn("Failed to seed default workflows")
	}

	e.logger.Info("Workflow engine started")
	return nil
}

// Stop 停止工作流引擎
func (e *Engine) Stop() error {
	e.logger.Info("Stopping workflow engine")
	e.cancel()

	// 等待所有 worker 完成
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	// 等待超时
	select {
	case <-done:
		e.logger.Info("Workflow engine stopped")
	case <-time.After(30 * time.Second):
		e.logger.Warn("Workflow engine stop timeout, some workers may still be running")
	}

	return nil
}

// worker 工作线程
func (e *Engine) worker(id int) {
	defer e.wg.Done()

	log := e.logger.WithField("worker_id", id)
	log.Debug("Workflow worker started")

	for {
		select {
		case <-e.ctx.Done():
			log.Debug("Workflow worker stopped")
			return
		case task := <-e.executionQueue:
			e.executeWorkflowTask(task)
		}
	}
}

// recoveryLoop 执行恢复循环
func (e *Engine) recoveryLoop() {
	defer e.wg.Done()

	interval := e.config.RecoveryInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// 启动时立即检查一次
	e.recoverPendingExecutions()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.recoverPendingExecutions()
		}
	}
}

// recoverPendingExecutions 恢复待处理的执行
func (e *Engine) recoverPendingExecutions() {
	executions, err := e.store.ListPendingExecutions(100)
	if err != nil {
		e.logger.WithError(err).Error("Failed to list pending executions for recovery")
		return
	}

	for _, exec := range executions {
		// 检查是否超时
		if exec.TimeoutAt != nil && time.Now().After(*exec.TimeoutAt) {
			e.markExecutionTimeout(exec)
			continue
		}

		// 获取工作流定义
		workflow, err := e.store.GetWorkflowByID(exec.WorkflowID)
		if err != nil {
			e.logger.WithError(err).WithField("execution_id", exec.ID).Error("Failed to get workflow for recovery")
			continue
		}

		// 重新入队执行
		e.logger.WithFields(logrus.Fields{
			"execution_id": exec.ID,
			"workflow_id":  exec.WorkflowID,
			"status":       exec.Status,
		}).Info("Recovering execution")

		select {
		case e.executionQueue <- &executionTask{execution: exec, workflow: workflow}:
		default:
			e.logger.Warn("Execution queue full, skipping recovery for this execution")
		}
	}
}

// markExecutionTimeout 标记执行超时
func (e *Engine) markExecutionTimeout(exec *domain.WorkflowExecution) {
	now := time.Now()
	exec.Status = domain.ExecutionStatusTimeout
	exec.Error = "execution timed out"
	exec.ErrorCode = domain.ErrorTypeTimeout
	exec.CompletedAt = &now

	if err := e.store.UpdateExecution(exec); err != nil {
		e.logger.WithError(err).WithField("execution_id", exec.ID).Error("Failed to mark execution as timed out")
	}
}

// StartExecution 启动新的工作流执行
func (e *Engine) StartExecution(workflowID string, input json.RawMessage) (*domain.WorkflowExecution, error) {
	// 获取工作流定义
	workflow, err := e.store.GetWorkflowByID(workflowID)
	if err != nil {
		return nil, err
	}

	// 检查工作流状态
	if workflow.Status != domain.WorkflowStatusActive {
		return nil, domain.ErrWorkflowInactive
	}

	// 计算超时时间
	timeout := workflow.TimeoutSec
	if timeout <= 0 {
		timeout = e.config.DefaultTimeout
	}
	timeoutAt := time.Now().Add(time.Duration(timeout) * time.Second)

	// 序列化工作流定义快照
	definitionJSON, err := json.Marshal(workflow.Definition)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal workflow definition: %w", err)
	}

	// 创建执行实例
	exec := &domain.WorkflowExecution{
		ID:                 uuid.New().String(),
		WorkflowID:         workflow.ID,
		WorkflowName:       workflow.Name,
		WorkflowVersion:    workflow.Version,
		WorkflowDefinition: definitionJSON,
		Status:             domain.ExecutionStatusPending,
		Input:              input,
		TimeoutAt:          &timeoutAt,
	}

	// 持久化执行实例
	if err := e.store.CreateExecution(exec); err != nil {
		return nil, fmt.Errorf("failed to create execution: %w", err)
	}

	// 入队执行
	select {
	case e.executionQueue <- &executionTask{execution: exec, workflow: workflow}:
		e.logger.WithFields(logrus.Fields{
			"execution_id": exec.ID,
			"workflow_id":  workflow.ID,
			"workflow":     workflow.Name,
		}).Info("Execution queued")
	default:
		// 队列满，标记为失败
		exec.Status = domain.ExecutionStatusFailed
		exec.Error = "execution queue full"
		now := time.Now()
		exec.CompletedAt = &now
		e.store.UpdateExecution(exec)
		return nil, fmt.Errorf("execution queue full")
	}

	return exec, nil
}

// StopExecution 停止执行
func (e *Engine) StopExecution(executionID string) error {
	exec, err := e.store.GetExecutionByID(executionID)
	if err != nil {
		return err
	}

	if exec.IsTerminal() {
		return domain.ErrExecutionAlreadyComplete
	}

	now := time.Now()
	exec.Status = domain.ExecutionStatusCancelled
	exec.Error = "execution cancelled by user"
	exec.CompletedAt = &now

	return e.store.UpdateExecution(exec)
}

// executeWorkflow 执行工作流主循环
func (e *Engine) executeWorkflow(exec *domain.WorkflowExecution, workflow *domain.Workflow) {
	e.executeWorkflowTask(&executionTask{execution: exec, workflow: workflow})
}

// executeWorkflowTask 执行工作流主循环（支持恢复）
func (e *Engine) executeWorkflowTask(task *executionTask) {
	exec := task.execution
	workflow := task.workflow

	log := e.logger.WithFields(logrus.Fields{
		"execution_id": exec.ID,
		"workflow_id":  workflow.ID,
		"workflow":     workflow.Name,
	})

	// 判断是否从暂停状态恢复
	isResume := task.resumeState != ""

	if isResume {
		log.WithField("resume_state", task.resumeState).Info("Resuming workflow execution")
		// 恢复时更新状态为运行中，清除暂停字段
		exec.Status = domain.ExecutionStatusRunning
		exec.PausedAtState = ""
		exec.PausedInput = nil
		exec.PausedAt = nil
		exec.CurrentState = task.resumeState
		if err := e.store.UpdateExecution(exec); err != nil {
			log.WithError(err).Error("Failed to update execution status on resume")
			return
		}
	} else {
		log.Info("Starting workflow execution")

		// 更新执行状态为运行中
		now := time.Now()
		exec.Status = domain.ExecutionStatusRunning
		exec.StartedAt = &now
		exec.CurrentState = workflow.Definition.StartAt

		if err := e.store.UpdateExecution(exec); err != nil {
			log.WithError(err).Error("Failed to update execution status to running")
			return
		}
	}

	// 状态机主循环
	var currentState string
	var currentInput json.RawMessage

	if isResume {
		currentState = task.resumeState
		currentInput = task.resumeInput
	} else {
		currentState = workflow.Definition.StartAt
		currentInput = exec.Input
	}

	for {
		// 检查是否被取消或超时
		if e.ctx.Err() != nil {
			log.Info("Execution cancelled due to engine shutdown")
			return
		}

		// 重新加载执行实例以检查是否被取消
		latestExec, err := e.store.GetExecutionByID(exec.ID)
		if err != nil {
			log.WithError(err).Error("Failed to reload execution")
			return
		}
		if latestExec.Status == domain.ExecutionStatusCancelled {
			log.Info("Execution was cancelled")
			return
		}

		// 检查超时
		if exec.TimeoutAt != nil && time.Now().After(*exec.TimeoutAt) {
			e.completeExecution(exec, nil, domain.ErrorTypeTimeout, "execution timed out", domain.ExecutionStatusTimeout)
			return
		}

		// 检查断点（在进入状态之前）
		if bp, _ := e.store.GetBreakpoint(exec.ID, currentState); bp != nil && bp.Enabled {
			log.WithField("state", currentState).Info("Breakpoint hit, pausing execution")
			e.pauseExecution(exec, currentState, currentInput)
			return // Worker 退出，等待通过 API 恢复
		}

		// 获取状态定义
		state, ok := workflow.Definition.States[currentState]
		if !ok {
			e.completeExecution(exec, nil, "States.InvalidState", fmt.Sprintf("state %s not found", currentState), domain.ExecutionStatusFailed)
			return
		}

		// 更新当前状态
		exec.CurrentState = currentState
		e.store.UpdateExecution(exec)

		log.WithField("state", currentState).Debug("Executing state")

		// 执行状态
		result := e.executor.ExecuteState(e.ctx, exec, currentState, &state, currentInput)

		// 处理执行结果
		if result.Error != nil {
			// 检查是否有 Catch
			if result.CaughtByState != "" {
				log.WithFields(logrus.Fields{
					"state":       currentState,
					"caught_by":   result.CaughtByState,
					"error":       result.Error.Error(),
					"error_code":  result.ErrorCode,
				}).Info("Error caught, transitioning to catch state")
				currentState = result.CaughtByState
				// 构造错误信息作为下一个状态的输入
				errorData := map[string]interface{}{
					"Error": result.ErrorCode,
					"Cause": result.Error.Error(),
				}
				currentInput, _ = json.Marshal(errorData)
				continue
			}

			// 没有 Catch，执行失败
			e.completeExecution(exec, nil, result.ErrorCode, result.Error.Error(), domain.ExecutionStatusFailed)
			return
		}

		// 检查是否为终止状态
		if result.NextState == "" {
			// 执行成功完成
			e.completeExecution(exec, result.Output, "", "", domain.ExecutionStatusSucceeded)
			return
		}

		// 继续下一个状态
		currentState = result.NextState
		currentInput = result.Output
	}
}

// completeExecution 完成执行
func (e *Engine) completeExecution(exec *domain.WorkflowExecution, output json.RawMessage, errorCode, errorMsg string, status domain.ExecutionStatus) {
	now := time.Now()
	exec.Status = status
	exec.Output = output
	exec.Error = errorMsg
	exec.ErrorCode = errorCode
	exec.CompletedAt = &now

	if err := e.store.UpdateExecution(exec); err != nil {
		e.logger.WithError(err).WithField("execution_id", exec.ID).Error("Failed to complete execution")
	}

	e.logger.WithFields(logrus.Fields{
		"execution_id": exec.ID,
		"status":       status,
		"error":        errorMsg,
	}).Info("Workflow execution completed")
}

// GetExecution 获取执行详情
func (e *Engine) GetExecution(executionID string) (*domain.WorkflowExecution, error) {
	return e.store.GetExecutionByID(executionID)
}

// GetExecutionHistory 获取执行历史
func (e *Engine) GetExecutionHistory(executionID string) ([]*domain.StateExecution, error) {
	return e.store.ListStateExecutions(executionID)
}

// pauseExecution 暂停执行（断点命中时调用）
func (e *Engine) pauseExecution(exec *domain.WorkflowExecution, state string, input json.RawMessage) {
	now := time.Now()
	exec.Status = domain.ExecutionStatusPaused
	exec.PausedAtState = state
	exec.PausedInput = input
	exec.PausedAt = &now
	exec.CurrentState = state

	if err := e.store.UpdateExecution(exec); err != nil {
		e.logger.WithError(err).WithField("execution_id", exec.ID).Error("Failed to pause execution")
	}

	e.logger.WithFields(logrus.Fields{
		"execution_id":   exec.ID,
		"paused_at_state": state,
	}).Info("Workflow execution paused at breakpoint")
}

// ResumeExecution 恢复暂停的执行
func (e *Engine) ResumeExecution(executionID string, modifiedInput json.RawMessage) error {
	exec, err := e.store.GetExecutionByID(executionID)
	if err != nil {
		return err
	}

	if exec.Status != domain.ExecutionStatusPaused {
		return fmt.Errorf("execution is not paused (current status: %s)", exec.Status)
	}

	if exec.PausedAtState == "" {
		return fmt.Errorf("execution has no paused state information")
	}

	// 获取工作流定义
	workflow, err := e.store.GetWorkflowByID(exec.WorkflowID)
	if err != nil {
		return fmt.Errorf("failed to get workflow: %w", err)
	}

	// 确定恢复使用的输入
	resumeInput := exec.PausedInput
	if modifiedInput != nil {
		resumeInput = modifiedInput
	}

	// 创建恢复任务并入队
	task := &executionTask{
		execution:   exec,
		workflow:    workflow,
		resumeState: exec.PausedAtState,
		resumeInput: resumeInput,
	}

	select {
	case e.executionQueue <- task:
		e.logger.WithFields(logrus.Fields{
			"execution_id": exec.ID,
			"resume_state": exec.PausedAtState,
		}).Info("Execution resume queued")
	default:
		return fmt.Errorf("execution queue full")
	}

	return nil
}
