// Package domain 定义了函数计算平台的核心领域模型。
package domain

import (
	"encoding/json"
	"time"
)

// ==================== 工作流状态类型 ====================

// WorkflowStatus 表示工作流的状态
type WorkflowStatus string

const (
	// WorkflowStatusActive 工作流处于活跃状态，可以被执行
	WorkflowStatusActive WorkflowStatus = "active"
	// WorkflowStatusInactive 工作流处于非活跃状态，暂停服务
	WorkflowStatusInactive WorkflowStatus = "inactive"
)

// ExecutionStatus 表示工作流执行的状态
type ExecutionStatus string

const (
	// ExecutionStatusPending 执行等待中
	ExecutionStatusPending ExecutionStatus = "pending"
	// ExecutionStatusRunning 执行运行中
	ExecutionStatusRunning ExecutionStatus = "running"
	// ExecutionStatusSucceeded 执行成功
	ExecutionStatusSucceeded ExecutionStatus = "succeeded"
	// ExecutionStatusFailed 执行失败
	ExecutionStatusFailed ExecutionStatus = "failed"
	// ExecutionStatusTimeout 执行超时
	ExecutionStatusTimeout ExecutionStatus = "timeout"
	// ExecutionStatusPaused 执行已暂停（断点）
	ExecutionStatusPaused ExecutionStatus = "paused"
	// ExecutionStatusCancelled 执行被取消
	ExecutionStatusCancelled ExecutionStatus = "cancelled"
)

// StateExecutionStatus 表示单个状态执行的状态
type StateExecutionStatus string

const (
	// StateExecutionStatusPending 状态执行等待中
	StateExecutionStatusPending StateExecutionStatus = "pending"
	// StateExecutionStatusRunning 状态执行运行中
	StateExecutionStatusRunning StateExecutionStatus = "running"
	// StateExecutionStatusSucceeded 状态执行成功
	StateExecutionStatusSucceeded StateExecutionStatus = "succeeded"
	// StateExecutionStatusFailed 状态执行失败
	StateExecutionStatusFailed StateExecutionStatus = "failed"
	// StateExecutionStatusRetrying 状态执行重试中
	StateExecutionStatusRetrying StateExecutionStatus = "retrying"
	// StateExecutionStatusCaught 状态执行被捕获（错误处理）
	StateExecutionStatusCaught StateExecutionStatus = "caught"
)

// StateType 表示状态的类型
type StateType string

const (
	// StateTypeTask 任务状态，调用函数执行任务
	StateTypeTask StateType = "Task"
	// StateTypeChoice 选择状态，根据条件选择下一个状态
	StateTypeChoice StateType = "Choice"
	// StateTypeWait 等待状态，等待指定时间
	StateTypeWait StateType = "Wait"
	// StateTypeParallel 并行状态，并行执行多个分支
	StateTypeParallel StateType = "Parallel"
	// StateTypePass 透传状态，透传输入到输出
	StateTypePass StateType = "Pass"
	// StateTypeFail 失败状态，以失败终止执行
	StateTypeFail StateType = "Fail"
	// StateTypeSucceed 成功状态，以成功终止执行
	StateTypeSucceed StateType = "Succeed"
)

// ==================== 工作流定义 ====================

// Workflow 工作流定义
type Workflow struct {
	// ID 工作流唯一标识符
	ID string `json:"id"`
	// Name 工作流名称（唯一）
	Name string `json:"name"`
	// Description 工作流描述
	Description string `json:"description,omitempty"`
	// Version 工作流版本号
	Version int `json:"version"`
	// Status 工作流状态
	Status WorkflowStatus `json:"status"`
	// Definition 工作流定义（DAG 结构）
	Definition WorkflowDefinition `json:"definition"`
	// TimeoutSec 工作流整体超时时间（秒）
	TimeoutSec int `json:"timeout_sec"`
	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt 更新时间
	UpdatedAt time.Time `json:"updated_at"`
}

// WorkflowDefinition 工作流定义的 DAG 结构
type WorkflowDefinition struct {
	// StartAt 起始状态名称
	StartAt string `json:"start_at"`
	// States 状态定义映射
	States map[string]State `json:"states"`
}

// State 单个状态的定义
type State struct {
	// Type 状态类型
	Type StateType `json:"type"`
	// Next 下一个状态的名称
	Next string `json:"next,omitempty"`
	// End 是否为终止状态
	End bool `json:"end,omitempty"`

	// Comment 状态注释
	Comment string `json:"comment,omitempty"`

	// ===== Task 状态字段 =====
	// FunctionID 要调用的函数 ID
	FunctionID string `json:"function_id,omitempty"`
	// TimeoutSec 任务超时时间（秒）
	TimeoutSec int `json:"timeout_sec,omitempty"`
	// Retry 重试策略
	Retry *RetryPolicy `json:"retry,omitempty"`
	// Catch 错误捕获配置
	Catch []CatchConfig `json:"catch,omitempty"`

	// ===== Choice 状态字段 =====
	// Choices 条件分支列表
	Choices []ChoiceRule `json:"choices,omitempty"`
	// Default 默认分支（没有条件匹配时）
	Default string `json:"default,omitempty"`

	// ===== Wait 状态字段 =====
	// Seconds 等待秒数
	Seconds int `json:"seconds,omitempty"`
	// Timestamp 等待到指定时间戳
	Timestamp string `json:"timestamp,omitempty"`

	// ===== Parallel 状态字段 =====
	// Branches 并行分支列表
	Branches []Branch `json:"branches,omitempty"`

	// ===== Pass/Fail 状态字段 =====
	// Result 传递的结果值
	Result json.RawMessage `json:"result,omitempty"`
	// Error 错误代码（Fail 状态）
	Error string `json:"error,omitempty"`
	// Cause 错误原因（Fail 状态）
	Cause string `json:"cause,omitempty"`

	// ===== 输入/输出处理字段 =====
	// InputPath 输入路径（JSONPath）
	InputPath string `json:"input_path,omitempty"`
	// OutputPath 输出路径（JSONPath）
	OutputPath string `json:"output_path,omitempty"`
	// ResultPath 结果路径（JSONPath）
	ResultPath string `json:"result_path,omitempty"`
	// Parameters 参数模板
	Parameters json.RawMessage `json:"parameters,omitempty"`
	// ResultSelector 结果选择器
	ResultSelector json.RawMessage `json:"result_selector,omitempty"`
}

// RetryPolicy 重试策略配置
type RetryPolicy struct {
	// ErrorEquals 匹配的错误类型列表
	ErrorEquals []string `json:"error_equals"`
	// MaxAttempts 最大重试次数
	MaxAttempts int `json:"max_attempts"`
	// IntervalSeconds 重试间隔（秒）
	IntervalSeconds int `json:"interval_seconds"`
	// BackoffRate 退避率（指数退避）
	BackoffRate float64 `json:"backoff_rate"`
	// MaxIntervalSeconds 最大重试间隔（秒）
	MaxIntervalSeconds int `json:"max_interval_seconds,omitempty"`
}

// CatchConfig 错误捕获配置
type CatchConfig struct {
	// ErrorEquals 匹配的错误类型列表
	ErrorEquals []string `json:"error_equals"`
	// Next 捕获后转到的状态
	Next string `json:"next"`
	// ResultPath 错误信息存储路径
	ResultPath string `json:"result_path,omitempty"`
}

// ChoiceRule 条件分支规则
type ChoiceRule struct {
	// Next 条件满足时的下一个状态
	Next string `json:"next"`

	// ===== 比较条件 =====
	// Variable 要比较的变量（JSONPath）
	Variable string `json:"variable,omitempty"`

	// 字符串比较
	StringEquals    string `json:"string_equals,omitempty"`
	StringNotEquals string `json:"string_not_equals,omitempty"`
	StringLessThan  string `json:"string_less_than,omitempty"`
	StringGreaterThan string `json:"string_greater_than,omitempty"`
	StringLessThanEquals string `json:"string_less_than_equals,omitempty"`
	StringGreaterThanEquals string `json:"string_greater_than_equals,omitempty"`
	StringMatches   string `json:"string_matches,omitempty"`

	// 数字比较
	NumericEquals    *float64 `json:"numeric_equals,omitempty"`
	NumericNotEquals *float64 `json:"numeric_not_equals,omitempty"`
	NumericLessThan  *float64 `json:"numeric_less_than,omitempty"`
	NumericGreaterThan *float64 `json:"numeric_greater_than,omitempty"`
	NumericLessThanEquals *float64 `json:"numeric_less_than_equals,omitempty"`
	NumericGreaterThanEquals *float64 `json:"numeric_greater_than_equals,omitempty"`

	// 布尔比较
	BooleanEquals *bool `json:"boolean_equals,omitempty"`

	// 时间戳比较
	TimestampEquals    string `json:"timestamp_equals,omitempty"`
	TimestampNotEquals string `json:"timestamp_not_equals,omitempty"`
	TimestampLessThan  string `json:"timestamp_less_than,omitempty"`
	TimestampGreaterThan string `json:"timestamp_greater_than,omitempty"`
	TimestampLessThanEquals string `json:"timestamp_less_than_equals,omitempty"`
	TimestampGreaterThanEquals string `json:"timestamp_greater_than_equals,omitempty"`

	// 存在性检查
	IsNull    *bool `json:"is_null,omitempty"`
	IsPresent *bool `json:"is_present,omitempty"`
	IsNumeric *bool `json:"is_numeric,omitempty"`
	IsString  *bool `json:"is_string,omitempty"`
	IsBoolean *bool `json:"is_boolean,omitempty"`
	IsTimestamp *bool `json:"is_timestamp,omitempty"`

	// ===== 逻辑运算符 =====
	// And 逻辑与
	And []ChoiceRule `json:"and,omitempty"`
	// Or 逻辑或
	Or []ChoiceRule `json:"or,omitempty"`
	// Not 逻辑非
	Not *ChoiceRule `json:"not,omitempty"`
}

// Branch 并行分支定义
type Branch struct {
	// StartAt 分支起始状态
	StartAt string `json:"start_at"`
	// States 分支状态定义
	States map[string]State `json:"states"`
}

// ==================== 请求/响应结构体 ====================

// CreateWorkflowRequest 创建工作流请求
type CreateWorkflowRequest struct {
	// Name 工作流名称
	Name string `json:"name" validate:"required,min=1,max=64"`
	// Description 工作流描述
	Description string `json:"description,omitempty"`
	// Definition 工作流定义
	Definition WorkflowDefinition `json:"definition" validate:"required"`
	// TimeoutSec 整体超时时间（秒），默认 3600
	TimeoutSec int `json:"timeout_sec,omitempty"`
}

// Validate 验证创建工作流请求
func (r *CreateWorkflowRequest) Validate() error {
	if r.Name == "" {
		return ErrInvalidWorkflowName
	}
	if r.Definition.StartAt == "" {
		return ErrInvalidWorkflowDefinition
	}
	if len(r.Definition.States) == 0 {
		return ErrInvalidWorkflowDefinition
	}
	// 验证起始状态存在
	if _, ok := r.Definition.States[r.Definition.StartAt]; !ok {
		return ErrInvalidWorkflowDefinition
	}
	// 设置默认超时
	if r.TimeoutSec == 0 {
		r.TimeoutSec = 3600
	}
	// 验证超时范围
	if r.TimeoutSec < 1 || r.TimeoutSec > 86400 {
		return ErrInvalidWorkflowTimeout
	}
	return nil
}

// UpdateWorkflowRequest 更新工作流请求
type UpdateWorkflowRequest struct {
	// Description 工作流描述
	Description *string `json:"description,omitempty"`
	// Definition 工作流定义
	Definition *WorkflowDefinition `json:"definition,omitempty"`
	// TimeoutSec 整体超时时间（秒）
	TimeoutSec *int `json:"timeout_sec,omitempty"`
	// Status 工作流状态
	Status *WorkflowStatus `json:"status,omitempty"`
}

// StartExecutionRequest 启动执行请求
type StartExecutionRequest struct {
	// Input 执行输入数据
	Input json.RawMessage `json:"input,omitempty"`
	// Name 执行名称（可选，用于幂等性）
	Name string `json:"name,omitempty"`
}

// WorkflowRepository 工作流存储接口
type WorkflowRepository interface {
	// CreateWorkflow 创建工作流
	CreateWorkflow(workflow *Workflow) error
	// GetWorkflowByID 根据 ID 获取工作流
	GetWorkflowByID(id string) (*Workflow, error)
	// GetWorkflowByName 根据名称获取工作流
	GetWorkflowByName(name string) (*Workflow, error)
	// ListWorkflows 列出工作流
	ListWorkflows(offset, limit int) ([]*Workflow, int, error)
	// UpdateWorkflow 更新工作流
	UpdateWorkflow(workflow *Workflow) error
	// DeleteWorkflow 删除工作流
	DeleteWorkflow(id string) error
}
