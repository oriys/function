// Package domain 定义了函数计算平台的核心领域模型。
package domain

import (
	"encoding/json"
	"time"
)

// LogEntry 表示一条平台侧的日志事件。
// 主要用于 Web 控制台 / CLI 的实时日志流。
type LogEntry struct {
	Timestamp    time.Time       `json:"timestamp"`
	Level        string          `json:"level"`
	FunctionID   string          `json:"function_id"`
	FunctionName string          `json:"function_name"`
	Message      string          `json:"message"`
	RequestID    string          `json:"request_id,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	Output       json.RawMessage `json:"output,omitempty"`
	Error        string          `json:"error,omitempty"`
	DurationMs   int64           `json:"duration_ms,omitempty"`
}
