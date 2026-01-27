package domain

import (
	"encoding/json"
	"time"
)

// Breakpoint 表示工作流执行的断点
type Breakpoint struct {
	// ID 断点唯一标识符
	ID string `json:"id"`
	// ExecutionID 关联的执行实例 ID
	ExecutionID string `json:"execution_id"`
	// BeforeState 断点所在的目标状态名称（暂停在进入该状态之前）
	BeforeState string `json:"before_state"`
	// Enabled 断点是否启用
	Enabled bool `json:"enabled"`
	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`
}

// BreakpointRepository 断点存储接口
type BreakpointRepository interface {
	// CreateBreakpoint 创建断点
	CreateBreakpoint(bp *Breakpoint) error
	// GetBreakpoint 获取指定执行和状态的断点
	GetBreakpoint(executionID, beforeState string) (*Breakpoint, error)
	// ListBreakpoints 列出执行实例的所有断点
	ListBreakpoints(executionID string) ([]*Breakpoint, error)
	// DeleteBreakpoint 删除断点
	DeleteBreakpoint(executionID, beforeState string) error
}

// SetBreakpointRequest 设置断点请求
type SetBreakpointRequest struct {
	// BeforeState 目标状态名称
	BeforeState string `json:"before_state" validate:"required"`
}

// ResumeExecutionRequest 恢复执行请求
type ResumeExecutionRequest struct {
	// Input 可选的修改后输入数据（为空则使用原始暂停时的输入）
	Input *json.RawMessage `json:"input,omitempty"`
}
