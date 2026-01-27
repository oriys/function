// Package domain 定义了函数计算平台的核心领域模型。
package domain

import (
	"encoding/json"
	"time"
)

// ==================== 执行实例定义 ====================

// WorkflowExecution 工作流执行实例
type WorkflowExecution struct {
	// ID 执行实例唯一标识符
	ID string `json:"id"`
	// WorkflowID 关联的工作流 ID
	WorkflowID string `json:"workflow_id"`
	// WorkflowName 工作流名称（快照）
	WorkflowName string `json:"workflow_name"`
	// WorkflowVersion 执行时的工作流版本
	WorkflowVersion int `json:"workflow_version"`
	// WorkflowDefinition 执行时的工作流定义快照
	WorkflowDefinition json.RawMessage `json:"workflow_definition,omitempty"`
	// Status 执行状态
	Status ExecutionStatus `json:"status"`
	// Input 执行输入数据
	Input json.RawMessage `json:"input,omitempty"`
	// Output 执行输出数据
	Output json.RawMessage `json:"output,omitempty"`
	// Error 错误信息
	Error string `json:"error,omitempty"`
	// ErrorCode 错误代码
	ErrorCode string `json:"error_code,omitempty"`
	// CurrentState 当前执行状态名称
	CurrentState string `json:"current_state,omitempty"`
	// StartedAt 开始执行时间
	StartedAt *time.Time `json:"started_at,omitempty"`
	// CompletedAt 执行完成时间
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt 更新时间
	UpdatedAt time.Time `json:"updated_at"`
	// TimeoutAt 超时时间
	TimeoutAt *time.Time `json:"timeout_at,omitempty"`
	// PausedAtState 暂停时的目标状态名称（断点）
	PausedAtState string `json:"paused_at_state,omitempty"`
	// PausedInput 暂停时的输入数据
	PausedInput json.RawMessage `json:"paused_input,omitempty"`
	// PausedAt 暂停时间
	PausedAt *time.Time `json:"paused_at,omitempty"`
}

// IsTerminal 检查执行是否已终止
func (e *WorkflowExecution) IsTerminal() bool {
	switch e.Status {
	case ExecutionStatusSucceeded, ExecutionStatusFailed, ExecutionStatusTimeout, ExecutionStatusCancelled:
		return true
	default:
		return false
	}
}

// StateExecution 状态执行历史记录
type StateExecution struct {
	// ID 状态执行记录唯一标识符
	ID string `json:"id"`
	// ExecutionID 关联的工作流执行 ID
	ExecutionID string `json:"execution_id"`
	// StateName 状态名称
	StateName string `json:"state_name"`
	// StateType 状态类型
	StateType StateType `json:"state_type"`
	// Status 状态执行状态
	Status StateExecutionStatus `json:"status"`
	// Input 状态输入数据
	Input json.RawMessage `json:"input,omitempty"`
	// Output 状态输出数据
	Output json.RawMessage `json:"output,omitempty"`
	// Error 错误信息
	Error string `json:"error,omitempty"`
	// ErrorCode 错误代码
	ErrorCode string `json:"error_code,omitempty"`
	// RetryCount 重试次数
	RetryCount int `json:"retry_count"`
	// InvocationID 关联的函数调用 ID（Task 状态）
	InvocationID string `json:"invocation_id,omitempty"`
	// StartedAt 开始执行时间
	StartedAt *time.Time `json:"started_at,omitempty"`
	// CompletedAt 执行完成时间
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`
}

// IsTerminal 检查状态执行是否已终止
func (s *StateExecution) IsTerminal() bool {
	switch s.Status {
	case StateExecutionStatusSucceeded, StateExecutionStatusFailed, StateExecutionStatusCaught:
		return true
	default:
		return false
	}
}

// ==================== 执行结果定义 ====================

// StateResult 状态执行结果
type StateResult struct {
	// Output 状态输出数据
	Output json.RawMessage
	// NextState 下一个状态名称（空表示结束）
	NextState string
	// Error 错误信息
	Error error
	// ErrorCode 错误代码
	ErrorCode string
	// CaughtByState 被捕获后转到的状态（Catch 处理）
	CaughtByState string
}

// ParallelBranchResult 并行分支执行结果
type ParallelBranchResult struct {
	// BranchIndex 分支索引
	BranchIndex int
	// Output 分支输出
	Output json.RawMessage
	// Error 分支错误
	Error error
	// ErrorCode 错误代码
	ErrorCode string
}

// ==================== 执行存储接口 ====================

// ExecutionRepository 执行存储接口
type ExecutionRepository interface {
	// CreateExecution 创建执行实例
	CreateExecution(execution *WorkflowExecution) error
	// GetExecutionByID 根据 ID 获取执行实例
	GetExecutionByID(id string) (*WorkflowExecution, error)
	// ListExecutions 列出执行实例
	ListExecutions(workflowID string, offset, limit int) ([]*WorkflowExecution, int, error)
	// ListAllExecutions 列出所有执行实例
	ListAllExecutions(offset, limit int) ([]*WorkflowExecution, int, error)
	// UpdateExecution 更新执行实例
	UpdateExecution(execution *WorkflowExecution) error
	// ListPendingExecutions 列出待处理的执行实例（用于恢复）
	ListPendingExecutions(limit int) ([]*WorkflowExecution, error)

	// CreateStateExecution 创建状态执行记录
	CreateStateExecution(stateExec *StateExecution) error
	// GetStateExecutionByID 根据 ID 获取状态执行记录
	GetStateExecutionByID(id string) (*StateExecution, error)
	// ListStateExecutions 列出执行的状态执行历史
	ListStateExecutions(executionID string) ([]*StateExecution, error)
	// UpdateStateExecution 更新状态执行记录
	UpdateStateExecution(stateExec *StateExecution) error
}

// ==================== 执行响应定义 ====================

// ExecutionResponse 执行响应
type ExecutionResponse struct {
	// Execution 执行实例
	*WorkflowExecution
	// History 状态执行历史（可选）
	History []*StateExecution `json:"history,omitempty"`
}

// ExecutionListResponse 执行列表响应
type ExecutionListResponse struct {
	// Executions 执行列表
	Executions []*WorkflowExecution `json:"executions"`
	// Total 总数
	Total int `json:"total"`
	// Offset 偏移量
	Offset int `json:"offset"`
	// Limit 限制数
	Limit int `json:"limit"`
}

// WorkflowListResponse 工作流列表响应
type WorkflowListResponse struct {
	// Workflows 工作流列表
	Workflows []*Workflow `json:"workflows"`
	// Total 总数
	Total int `json:"total"`
	// Offset 偏移量
	Offset int `json:"offset"`
	// Limit 限制数
	Limit int `json:"limit"`
}

// ==================== 错误类型常量 ====================

// 标准错误类型，用于 Retry 和 Catch 的 ErrorEquals 匹配
const (
	// ErrorTypeAllErrors 匹配所有错误
	ErrorTypeAllErrors = "States.ALL"
	// ErrorTypeTimeout 超时错误
	ErrorTypeTimeout = "States.Timeout"
	// ErrorTypeTaskFailed 任务失败错误
	ErrorTypeTaskFailed = "States.TaskFailed"
	// ErrorTypePermissions 权限错误
	ErrorTypePermissions = "States.Permissions"
	// ErrorTypeResultPathMatchFailure 结果路径匹配失败
	ErrorTypeResultPathMatchFailure = "States.ResultPathMatchFailure"
	// ErrorTypeParameterPathFailure 参数路径失败
	ErrorTypeParameterPathFailure = "States.ParameterPathFailure"
	// ErrorTypeBranchFailed 分支失败
	ErrorTypeBranchFailed = "States.BranchFailed"
	// ErrorTypeNoChoiceMatched 没有匹配的 Choice 条件
	ErrorTypeNoChoiceMatched = "States.NoChoiceMatched"
	// ErrorTypeIntrinsicFailure 内置函数失败
	ErrorTypeIntrinsicFailure = "States.IntrinsicFailure"
)
