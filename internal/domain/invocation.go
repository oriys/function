// Package domain 定义了函数计算平台的核心领域模型。
package domain

import (
	"encoding/json"
	"time"
)

// InvocationStatus 表示函数调用的状态类型。
// 调用在其生命周期中会经历不同的状态转换。
type InvocationStatus string

// 调用状态常量定义
const (
	// InvocationStatusPending 表示调用正在等待执行
	InvocationStatusPending InvocationStatus = "pending"
	// InvocationStatusRunning 表示调用正在执行中
	InvocationStatusRunning InvocationStatus = "running"
	// InvocationStatusSuccess 表示调用执行成功
	InvocationStatusSuccess InvocationStatus = "success"
	// InvocationStatusFailed 表示调用执行失败
	InvocationStatusFailed InvocationStatus = "failed"
	// InvocationStatusTimeout 表示调用执行超时
	InvocationStatusTimeout InvocationStatus = "timeout"
	// InvocationStatusCancelled 表示调用被取消
	InvocationStatusCancelled InvocationStatus = "cancelled"
)

// TriggerType 表示触发函数调用的方式类型。
type TriggerType string

// 触发类型常量定义
const (
	// TriggerHTTP 表示通过 HTTP 请求触发
	TriggerHTTP TriggerType = "http"
	// TriggerEvent 表示通过事件触发（如消息队列）
	TriggerEvent TriggerType = "event"
	// TriggerCron 表示通过定时任务触发
	TriggerCron TriggerType = "cron"
)

// Invocation 表示一次函数调用记录。
// 该结构体记录了函数调用的完整信息，包括输入、输出、执行时间和计费信息。
type Invocation struct {
	// ID 是调用记录的唯一标识符
	ID string `json:"id"`
	// FunctionID 是被调用函数的 ID
	FunctionID string `json:"function_id"`
	// FunctionName 是被调用函数的名称
	FunctionName string `json:"function_name"`
	// TriggerType 是触发调用的方式
	TriggerType TriggerType `json:"trigger_type"`
	// Status 是调用的当前状态
	Status InvocationStatus `json:"status"`
	// Input 是调用的输入参数，以 JSON 格式存储
	Input json.RawMessage `json:"input,omitempty"`
	// Output 是调用的输出结果，以 JSON 格式存储
	Output json.RawMessage `json:"output,omitempty"`
	// Error 是调用执行过程中的错误信息
	Error string `json:"error,omitempty"`
	// ColdStart 表示本次调用是否为冷启动（需要启动新的虚拟机）
	ColdStart bool `json:"cold_start"`
	// VMID 是执行本次调用的虚拟机 ID
	VMID string `json:"vm_id,omitempty"`
	// Version 是实际执行的函数版本号
	Version int `json:"version,omitempty"`
	// AliasUsed 是调用时使用的别名（如果有）
	AliasUsed string `json:"alias_used,omitempty"`
	// SessionKey 是会话标识（用于有状态函数）
	SessionKey string `json:"session_key,omitempty"`
	// StartedAt 是调用开始执行的时间
	StartedAt *time.Time `json:"started_at,omitempty"`
	// CompletedAt 是调用执行完成的时间
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// DurationMs 是调用的实际执行时长（单位：毫秒）
	DurationMs int64 `json:"duration_ms"`
	// BilledTimeMs 是计费时长（单位：毫秒），按最小计费单位向上取整
	BilledTimeMs int64 `json:"billed_time_ms"`
	// MemoryUsedMB 是调用执行过程中使用的内存（单位：MB）
	MemoryUsedMB int `json:"memory_used_mb"`
	// RetryCount 是调用的重试次数
	RetryCount int `json:"retry_count"`
	// CreatedAt 是调用记录的创建时间
	CreatedAt time.Time `json:"created_at"`
}

// NewInvocation 创建一个新的调用记录。
// 初始状态为 pending，等待被执行。
//
// 参数:
//   - functionID: 被调用函数的 ID
//   - functionName: 被调用函数的名称
//   - triggerType: 触发调用的方式
//   - input: 调用的输入参数
//
// 返回:
//   - *Invocation: 新创建的调用记录指针
func NewInvocation(functionID, functionName string, triggerType TriggerType, input json.RawMessage) *Invocation {
	now := time.Now()
	return &Invocation{
		FunctionID:   functionID,
		FunctionName: functionName,
		TriggerType:  triggerType,
		Status:       InvocationStatusPending,
		Input:        input,
		RetryCount:   0,
		CreatedAt:    now,
	}
}

// Start 标记调用开始执行。
// 将状态更新为 running，并记录执行的虚拟机信息和冷启动状态。
//
// 参数:
//   - vmID: 执行调用的虚拟机 ID
//   - coldStart: 是否为冷启动
func (i *Invocation) Start(vmID string, coldStart bool) {
	now := time.Now()
	i.Status = InvocationStatusRunning
	i.VMID = vmID
	i.ColdStart = coldStart
	i.StartedAt = &now
}

// Complete 标记调用执行成功完成。
// 将状态更新为 success，记录输出结果和内存使用情况，并计算执行时长和计费时长。
//
// 参数:
//   - output: 函数执行的输出结果
//   - memoryUsedMB: 执行过程中使用的内存（单位：MB）
func (i *Invocation) Complete(output json.RawMessage, memoryUsedMB int) {
	now := time.Now()
	i.Status = InvocationStatusSuccess
	i.Output = output
	i.CompletedAt = &now
	i.MemoryUsedMB = memoryUsedMB
	if i.StartedAt != nil {
		i.DurationMs = now.Sub(*i.StartedAt).Milliseconds()
	}
	i.calculateBilledTime()
}

// Fail 标记调用执行失败。
// 将状态更新为 failed，记录错误信息，并计算执行时长和计费时长。
//
// 参数:
//   - errMsg: 错误信息描述
func (i *Invocation) Fail(errMsg string) {
	now := time.Now()
	i.Status = InvocationStatusFailed
	i.Error = errMsg
	i.CompletedAt = &now
	if i.StartedAt != nil {
		i.DurationMs = now.Sub(*i.StartedAt).Milliseconds()
	}
	i.calculateBilledTime()
}

// Timeout 标记调用执行超时。
// 将状态更新为 timeout，记录超时错误信息，并计算执行时长和计费时长。
func (i *Invocation) Timeout() {
	now := time.Now()
	i.Status = InvocationStatusTimeout
	i.Error = "function execution timed out"
	i.CompletedAt = &now
	if i.StartedAt != nil {
		i.DurationMs = now.Sub(*i.StartedAt).Milliseconds()
	}
	i.calculateBilledTime()
}

// calculateBilledTime 计算计费时长。
//
// 计费规则说明：
//   - 最小计费单位: 100 毫秒
//   - 向上取整: 实际执行时间向上取整到最近的 100 毫秒
//   - 最小计费: 即使执行时间小于 100ms，也按 100ms 计费
//
// 数学公式: billedMs = ceil(durationMs / 100) * 100
// 实现方式: (durationMs + 99) / 100 * 100 利用整数除法实现向上取整
//
// 示例:
//   - 执行 50ms  -> 计费 100ms
//   - 执行 100ms -> 计费 100ms
//   - 执行 150ms -> 计费 200ms
//   - 执行 1ms   -> 计费 100ms（最小计费）
func (i *Invocation) calculateBilledTime() {
	// 向上取整公式: (x + 单位 - 1) / 单位 * 单位
	// 例如: (150 + 99) / 100 * 100 = 249 / 100 * 100 = 2 * 100 = 200
	billedMs := ((i.DurationMs + 99) / 100) * 100
	if billedMs < 100 {
		billedMs = 100 // 最小计费时长为 100 毫秒
	}
	i.BilledTimeMs = billedMs
}

// InvocationRepository 定义了调用记录存储的接口。
// 该接口抽象了调用记录的持久化操作。
type InvocationRepository interface {
	// Create 创建一条新的调用记录
	Create(inv *Invocation) error
	// GetByID 根据 ID 获取调用记录
	GetByID(id string) (*Invocation, error)
	// ListByFunction 根据函数 ID 分页获取调用记录列表，返回调用列表、总数和可能的错误
	ListByFunction(functionID string, offset, limit int) ([]*Invocation, int, error)
	// Update 更新调用记录
	Update(inv *Invocation) error
}

// InvocationStats 表示函数调用的统计信息。
// 用于监控和分析函数的执行情况。
type InvocationStats struct {
	// FunctionID 是函数的 ID
	FunctionID string `json:"function_id"`
	// TotalInvocations 是总调用次数
	TotalInvocations int64 `json:"total_invocations"`
	// SuccessCount 是成功调用次数
	SuccessCount int64 `json:"success_count"`
	// FailureCount 是失败调用次数
	FailureCount int64 `json:"failure_count"`
	// TimeoutCount 是超时调用次数
	TimeoutCount int64 `json:"timeout_count"`
	// AvgDurationMs 是平均执行时长（单位：毫秒）
	AvgDurationMs float64 `json:"avg_duration_ms"`
	// P50DurationMs 是执行时长的 50 分位数（中位数）
	P50DurationMs float64 `json:"p50_duration_ms"`
	// P95DurationMs 是执行时长的 95 分位数
	P95DurationMs float64 `json:"p95_duration_ms"`
	// P99DurationMs 是执行时长的 99 分位数
	P99DurationMs float64 `json:"p99_duration_ms"`
	// ColdStartRate 是冷启动率（冷启动次数 / 总调用次数）
	ColdStartRate float64 `json:"cold_start_rate"`
}
