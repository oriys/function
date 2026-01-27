// Package workflow 实现了工作流编排引擎。
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

// Executor 状态执行器
type Executor struct {
	store     *storage.PostgresStore
	scheduler Scheduler
	logger    *logrus.Logger
	evaluator *Evaluator
	jsonpath  *JSONPathProcessor
}

// NewExecutor 创建执行器实例
func NewExecutor(store *storage.PostgresStore, scheduler Scheduler, logger *logrus.Logger) *Executor {
	return &Executor{
		store:     store,
		scheduler: scheduler,
		logger:    logger,
		evaluator: NewEvaluator(),
		jsonpath:  NewJSONPathProcessor(),
	}
}

// ExecuteState 执行单个状态
func (e *Executor) ExecuteState(ctx context.Context, exec *domain.WorkflowExecution, stateName string, state *domain.State, input json.RawMessage) *domain.StateResult {
	log := e.logger.WithFields(logrus.Fields{
		"execution_id": exec.ID,
		"state":        stateName,
		"type":         state.Type,
	})

	// 创建状态执行记录
	now := time.Now()
	stateExec := &domain.StateExecution{
		ID:          uuid.New().String(),
		ExecutionID: exec.ID,
		StateName:   stateName,
		StateType:   state.Type,
		Status:      domain.StateExecutionStatusRunning,
		Input:       input,
		StartedAt:   &now,
	}
	if err := e.store.CreateStateExecution(stateExec); err != nil {
		log.WithError(err).Error("Failed to create state execution record")
	}

	// 处理输入路径
	processedInput, err := e.jsonpath.ProcessInput(input, state.InputPath, state.Parameters)
	if err != nil {
		return e.completeStateExecution(stateExec, nil, domain.ErrorTypeParameterPathFailure, err, "")
	}

	// 根据状态类型执行
	var result *domain.StateResult
	switch state.Type {
	case domain.StateTypeTask:
		result = e.executeTaskState(ctx, exec, stateName, state, processedInput, stateExec)
	case domain.StateTypeChoice:
		result = e.executeChoiceState(state, processedInput)
	case domain.StateTypeWait:
		result = e.executeWaitState(ctx, state, processedInput)
	case domain.StateTypeParallel:
		result = e.executeParallelState(ctx, exec, stateName, state, processedInput)
	case domain.StateTypePass:
		result = e.executePassState(state, processedInput)
	case domain.StateTypeFail:
		result = e.executeFailState(state)
	case domain.StateTypeSucceed:
		result = e.executeSucceedState(processedInput)
	default:
		result = &domain.StateResult{
			Error:     fmt.Errorf("unknown state type: %s", state.Type),
			ErrorCode: "States.InvalidStateType",
		}
	}

	// 处理输出路径
	if result.Error == nil && result.Output != nil {
		processedOutput, err := e.jsonpath.ProcessOutput(input, result.Output, state.OutputPath, state.ResultPath, state.ResultSelector)
		if err != nil {
			result.Error = err
			result.ErrorCode = domain.ErrorTypeResultPathMatchFailure
		} else {
			result.Output = processedOutput
		}
	}

	// 完成状态执行记录
	if result.Error != nil {
		e.completeStateExecution(stateExec, result.Output, result.ErrorCode, result.Error, result.CaughtByState)
	} else {
		e.completeStateExecutionSuccess(stateExec, result.Output)
	}

	return result
}

// executeTaskState 执行 Task 状态
func (e *Executor) executeTaskState(ctx context.Context, exec *domain.WorkflowExecution, stateName string, state *domain.State, input json.RawMessage, stateExec *domain.StateExecution) *domain.StateResult {
	log := e.logger.WithFields(logrus.Fields{
		"execution_id": exec.ID,
		"state":        stateName,
		"function_id":  state.FunctionID,
	})

	// 准备重试策略
	maxAttempts := 1
	var retryPolicy *domain.RetryPolicy
	if state.Retry != nil {
		retryPolicy = state.Retry
		maxAttempts = retryPolicy.MaxAttempts + 1
	}

	var lastError error
	var lastErrorCode string

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// 计算重试间隔
			interval := e.calculateRetryInterval(retryPolicy, attempt)
			log.WithFields(logrus.Fields{
				"attempt":  attempt,
				"interval": interval,
			}).Info("Retrying task")

			// 更新状态执行记录
			stateExec.RetryCount = attempt
			stateExec.Status = domain.StateExecutionStatusRetrying
			e.store.UpdateStateExecution(stateExec)

			// 等待重试间隔
			select {
			case <-ctx.Done():
				return &domain.StateResult{
					Error:     ctx.Err(),
					ErrorCode: domain.ErrorTypeTimeout,
				}
			case <-time.After(interval):
			}
		}

		// 调用函数
		resp, err := e.scheduler.Invoke(&domain.InvokeRequest{
			FunctionID: state.FunctionID,
			Payload:    input,
		})

		if err != nil {
			lastError = err
			lastErrorCode = domain.ErrorTypeTaskFailed

			// 检查是否应该重试
			if retryPolicy != nil && e.shouldRetry(retryPolicy, lastErrorCode, attempt) {
				continue
			}

			// 检查是否有 Catch
			if catchNext := e.findCatch(state.Catch, lastErrorCode); catchNext != "" {
				return &domain.StateResult{
					Error:         lastError,
					ErrorCode:     lastErrorCode,
					CaughtByState: catchNext,
				}
			}

			return &domain.StateResult{
				Error:     lastError,
				ErrorCode: lastErrorCode,
			}
		}

		// 更新调用 ID
		stateExec.InvocationID = resp.RequestID
		e.store.UpdateStateExecution(stateExec)

		// 检查函数执行结果
		if resp.StatusCode >= 400 || resp.Error != "" {
			lastError = fmt.Errorf("function execution failed: %s", resp.Error)
			lastErrorCode = domain.ErrorTypeTaskFailed

			// 检查是否应该重试
			if retryPolicy != nil && e.shouldRetry(retryPolicy, lastErrorCode, attempt) {
				continue
			}

			// 检查是否有 Catch
			if catchNext := e.findCatch(state.Catch, lastErrorCode); catchNext != "" {
				return &domain.StateResult{
					Error:         lastError,
					ErrorCode:     lastErrorCode,
					CaughtByState: catchNext,
				}
			}

			return &domain.StateResult{
				Error:     lastError,
				ErrorCode: lastErrorCode,
			}
		}

		// 任务成功
		log.WithField("request_id", resp.RequestID).Debug("Task completed successfully")

		return &domain.StateResult{
			Output:    resp.Body,
			NextState: e.getNextState(state),
		}
	}

	// 所有重试都失败
	// 检查是否有 Catch
	if catchNext := e.findCatch(state.Catch, lastErrorCode); catchNext != "" {
		return &domain.StateResult{
			Error:         lastError,
			ErrorCode:     lastErrorCode,
			CaughtByState: catchNext,
		}
	}

	return &domain.StateResult{
		Error:     lastError,
		ErrorCode: lastErrorCode,
	}
}

// executeChoiceState 执行 Choice 状态
func (e *Executor) executeChoiceState(state *domain.State, input json.RawMessage) *domain.StateResult {
	// 评估每个选择规则
	for _, choice := range state.Choices {
		matched, err := e.evaluator.EvaluateChoice(&choice, input)
		if err != nil {
			return &domain.StateResult{
				Error:     err,
				ErrorCode: "States.ChoiceEvaluationError",
			}
		}
		if matched {
			return &domain.StateResult{
				Output:    input,
				NextState: choice.Next,
			}
		}
	}

	// 没有匹配，使用默认分支
	if state.Default != "" {
		return &domain.StateResult{
			Output:    input,
			NextState: state.Default,
		}
	}

	// 没有默认分支
	return &domain.StateResult{
		Error:     domain.ErrNoChoiceMatched,
		ErrorCode: domain.ErrorTypeNoChoiceMatched,
	}
}

// executeWaitState 执行 Wait 状态
func (e *Executor) executeWaitState(ctx context.Context, state *domain.State, input json.RawMessage) *domain.StateResult {
	var waitDuration time.Duration

	if state.Seconds > 0 {
		waitDuration = time.Duration(state.Seconds) * time.Second
	} else if state.Timestamp != "" {
		// 解析 ISO 8601 时间戳
		t, err := time.Parse(time.RFC3339, state.Timestamp)
		if err != nil {
			return &domain.StateResult{
				Error:     fmt.Errorf("invalid timestamp: %w", err),
				ErrorCode: "States.InvalidTimestamp",
			}
		}
		waitDuration = time.Until(t)
		if waitDuration < 0 {
			waitDuration = 0
		}
	}

	// 等待
	select {
	case <-ctx.Done():
		return &domain.StateResult{
			Error:     ctx.Err(),
			ErrorCode: domain.ErrorTypeTimeout,
		}
	case <-time.After(waitDuration):
	}

	return &domain.StateResult{
		Output:    input,
		NextState: e.getNextState(state),
	}
}

// executeParallelState 执行 Parallel 状态
func (e *Executor) executeParallelState(ctx context.Context, exec *domain.WorkflowExecution, stateName string, state *domain.State, input json.RawMessage) *domain.StateResult {
	if len(state.Branches) == 0 {
		return &domain.StateResult{
			Output:    json.RawMessage("[]"),
			NextState: e.getNextState(state),
		}
	}

	// 并行执行所有分支
	results := make(chan *domain.ParallelBranchResult, len(state.Branches))
	var wg sync.WaitGroup

	for i, branch := range state.Branches {
		wg.Add(1)
		go func(index int, b domain.Branch) {
			defer wg.Done()
			result := e.executeBranch(ctx, exec, stateName, index, &b, input)
			results <- result
		}(i, branch)
	}

	// 等待所有分支完成，支持超时取消
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// 等待完成或上下文取消
	select {
	case <-done:
		close(results)
	case <-ctx.Done():
		// 上下文被取消（超时或手动取消），返回超时错误
		return &domain.StateResult{
			Error:     ctx.Err(),
			ErrorCode: domain.ErrorTypeTimeout,
		}
	}

	// 收集结果
	branchOutputs := make([]json.RawMessage, len(state.Branches))
	var firstError error
	var firstErrorCode string

	for result := range results {
		if result.Error != nil && firstError == nil {
			firstError = result.Error
			firstErrorCode = result.ErrorCode
		} else if result.Error == nil {
			branchOutputs[result.BranchIndex] = result.Output
		}
	}

	// 如果有任何分支失败
	if firstError != nil {
		// 检查是否有 Catch
		if catchNext := e.findCatch(state.Catch, firstErrorCode); catchNext != "" {
			return &domain.StateResult{
				Error:         firstError,
				ErrorCode:     firstErrorCode,
				CaughtByState: catchNext,
			}
		}
		return &domain.StateResult{
			Error:     firstError,
			ErrorCode: firstErrorCode,
		}
	}

	// 合并所有分支输出
	output, err := json.Marshal(branchOutputs)
	if err != nil {
		return &domain.StateResult{
			Error:     err,
			ErrorCode: domain.ErrorTypeBranchFailed,
		}
	}

	return &domain.StateResult{
		Output:    output,
		NextState: e.getNextState(state),
	}
}

// executeBranch 执行并行分支
func (e *Executor) executeBranch(ctx context.Context, exec *domain.WorkflowExecution, parentState string, branchIndex int, branch *domain.Branch, input json.RawMessage) *domain.ParallelBranchResult {
	currentState := branch.StartAt
	currentInput := input

	for {
		// 检查上下文
		if ctx.Err() != nil {
			return &domain.ParallelBranchResult{
				BranchIndex: branchIndex,
				Error:       ctx.Err(),
				ErrorCode:   domain.ErrorTypeTimeout,
			}
		}

		// 获取状态定义
		state, ok := branch.States[currentState]
		if !ok {
			return &domain.ParallelBranchResult{
				BranchIndex: branchIndex,
				Error:       fmt.Errorf("state %s not found in branch %d", currentState, branchIndex),
				ErrorCode:   "States.InvalidState",
			}
		}

		// 执行状态
		result := e.ExecuteState(ctx, exec, fmt.Sprintf("%s.Branch[%d].%s", parentState, branchIndex, currentState), &state, currentInput)

		if result.Error != nil {
			return &domain.ParallelBranchResult{
				BranchIndex: branchIndex,
				Error:       result.Error,
				ErrorCode:   result.ErrorCode,
			}
		}

		// 检查是否为终止状态
		if result.NextState == "" {
			return &domain.ParallelBranchResult{
				BranchIndex: branchIndex,
				Output:      result.Output,
			}
		}

		currentState = result.NextState
		currentInput = result.Output
	}
}

// executePassState 执行 Pass 状态
func (e *Executor) executePassState(state *domain.State, input json.RawMessage) *domain.StateResult {
	output := input
	if state.Result != nil {
		output = state.Result
	}

	return &domain.StateResult{
		Output:    output,
		NextState: e.getNextState(state),
	}
}

// executeFailState 执行 Fail 状态
func (e *Executor) executeFailState(state *domain.State) *domain.StateResult {
	errorCode := state.Error
	if errorCode == "" {
		errorCode = "States.Fail"
	}
	cause := state.Cause
	if cause == "" {
		cause = "Execution failed"
	}

	return &domain.StateResult{
		Error:     fmt.Errorf("%s", cause),
		ErrorCode: errorCode,
	}
}

// executeSucceedState 执行 Succeed 状态
func (e *Executor) executeSucceedState(input json.RawMessage) *domain.StateResult {
	return &domain.StateResult{
		Output:    input,
		NextState: "", // 空表示终止
	}
}

// getNextState 获取下一个状态
func (e *Executor) getNextState(state *domain.State) string {
	if state.End {
		return ""
	}
	return state.Next
}

// calculateRetryInterval 计算重试间隔
func (e *Executor) calculateRetryInterval(policy *domain.RetryPolicy, attempt int) time.Duration {
	if policy == nil {
		return time.Second
	}

	interval := float64(policy.IntervalSeconds)
	if policy.BackoffRate > 1 {
		for i := 0; i < attempt; i++ {
			interval *= policy.BackoffRate
		}
	}

	// 限制最大间隔
	if policy.MaxIntervalSeconds > 0 && int(interval) > policy.MaxIntervalSeconds {
		interval = float64(policy.MaxIntervalSeconds)
	}

	return time.Duration(interval) * time.Second
}

// shouldRetry 判断是否应该重试
func (e *Executor) shouldRetry(policy *domain.RetryPolicy, errorCode string, attempt int) bool {
	if policy == nil || attempt >= policy.MaxAttempts {
		return false
	}

	// 检查错误类型是否匹配
	for _, errType := range policy.ErrorEquals {
		if errType == domain.ErrorTypeAllErrors || errType == errorCode {
			return true
		}
	}

	return false
}

// findCatch 查找匹配的 Catch 配置
func (e *Executor) findCatch(catches []domain.CatchConfig, errorCode string) string {
	for _, catch := range catches {
		for _, errType := range catch.ErrorEquals {
			if errType == domain.ErrorTypeAllErrors || errType == errorCode {
				return catch.Next
			}
		}
	}
	return ""
}

// completeStateExecution 完成状态执行记录（失败）
func (e *Executor) completeStateExecution(stateExec *domain.StateExecution, output json.RawMessage, errorCode string, err error, caughtBy string) *domain.StateResult {
	now := time.Now()
	if caughtBy != "" {
		stateExec.Status = domain.StateExecutionStatusCaught
	} else {
		stateExec.Status = domain.StateExecutionStatusFailed
	}
	stateExec.Output = output
	stateExec.Error = err.Error()
	stateExec.ErrorCode = errorCode
	stateExec.CompletedAt = &now
	e.store.UpdateStateExecution(stateExec)

	return &domain.StateResult{
		Output:        output,
		Error:         err,
		ErrorCode:     errorCode,
		CaughtByState: caughtBy,
	}
}

// completeStateExecutionSuccess 完成状态执行记录（成功）
func (e *Executor) completeStateExecutionSuccess(stateExec *domain.StateExecution, output json.RawMessage) {
	now := time.Now()
	stateExec.Status = domain.StateExecutionStatusSucceeded
	stateExec.Output = output
	stateExec.CompletedAt = &now
	e.store.UpdateStateExecution(stateExec)
}
