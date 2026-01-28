// Package domain 定义 Phase 3 高级特性的领域模型
package domain

import (
	"time"
)

// ==================== 告警系统类型 ====================

// AlertSeverity 告警严重程度
type AlertSeverity string

const (
	AlertSeverityCritical AlertSeverity = "critical" // 严重
	AlertSeverityWarning  AlertSeverity = "warning"  // 警告
	AlertSeverityInfo     AlertSeverity = "info"     // 信息
)

// AlertStatus 告警状态
type AlertStatus string

const (
	AlertStatusActive   AlertStatus = "active"   // 活跃
	AlertStatusResolved AlertStatus = "resolved" // 已解决
	AlertStatusSilenced AlertStatus = "silenced" // 已静默
)

// AlertConditionType 告警条件类型
type AlertConditionType string

const (
	AlertConditionErrorRate    AlertConditionType = "error_rate"    // 错误率
	AlertConditionLatencyP95   AlertConditionType = "latency_p95"   // P95延迟
	AlertConditionLatencyP99   AlertConditionType = "latency_p99"   // P99延迟
	AlertConditionColdStartRate AlertConditionType = "cold_start_rate" // 冷启动率
	AlertConditionInvocations  AlertConditionType = "invocations"   // 调用次数
)

// AlertRule 告警规则
type AlertRule struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	FunctionID  string             `json:"function_id,omitempty"` // 空表示全局规则
	Condition   AlertConditionType `json:"condition"`
	Operator    string             `json:"operator"` // >, <, >=, <=, ==
	Threshold   float64            `json:"threshold"`
	Duration    string             `json:"duration"` // 持续时间，如 "5m"
	Severity    AlertSeverity      `json:"severity"`
	Enabled     bool               `json:"enabled"`
	Channels    []string           `json:"channels"` // 通知渠道 ID 列表
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

// NotificationChannelType 通知渠道类型
type NotificationChannelType string

const (
	NotificationChannelEmail   NotificationChannelType = "email"
	NotificationChannelWebhook NotificationChannelType = "webhook"
	NotificationChannelSlack   NotificationChannelType = "slack"
	NotificationChannelDingtalk NotificationChannelType = "dingtalk"
)

// NotificationChannel 通知渠道
type NotificationChannel struct {
	ID        string                  `json:"id"`
	Name      string                  `json:"name"`
	Type      NotificationChannelType `json:"type"`
	Config    map[string]string       `json:"config"` // 渠道配置（URL、token等）
	Enabled   bool                    `json:"enabled"`
	CreatedAt time.Time               `json:"created_at"`
	UpdatedAt time.Time               `json:"updated_at"`
}

// Alert 告警实例
type Alert struct {
	ID           string        `json:"id"`
	RuleID       string        `json:"rule_id"`
	RuleName     string        `json:"rule_name"`
	FunctionID   string        `json:"function_id,omitempty"`
	FunctionName string        `json:"function_name,omitempty"`
	Severity     AlertSeverity `json:"severity"`
	Status       AlertStatus   `json:"status"`
	Message      string        `json:"message"`
	Value        float64       `json:"value"`     // 触发时的实际值
	Threshold    float64       `json:"threshold"` // 阈值
	FiredAt      time.Time     `json:"fired_at"`
	ResolvedAt   *time.Time    `json:"resolved_at,omitempty"`
}

// ==================== 函数预热类型 ====================

// WarmingPolicy 预热策略
type WarmingPolicy struct {
	ID            string    `json:"id"`
	FunctionID    string    `json:"function_id"`
	Enabled       bool      `json:"enabled"`
	MinInstances  int       `json:"min_instances"`  // 最小预热实例数
	MaxInstances  int       `json:"max_instances"`  // 最大预热实例数
	Schedule      string    `json:"schedule,omitempty"` // Cron 表达式，定时预热
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// WarmingStatus 预热状态
type WarmingStatus struct {
	FunctionID     string    `json:"function_id"`
	FunctionName   string    `json:"function_name"`
	WarmInstances  int       `json:"warm_instances"`  // 当前预热实例数
	BusyInstances  int       `json:"busy_instances"`  // 正在使用的实例数
	ColdStartRate  float64   `json:"cold_start_rate"` // 冷启动率
	LastWarmedAt   *time.Time `json:"last_warmed_at,omitempty"`
	Policy         *WarmingPolicy `json:"policy,omitempty"`
}

// ==================== 依赖分析类型 ====================

// DependencyType 依赖类型
type DependencyType string

const (
	DependencyTypeDirectCall   DependencyType = "direct_call"   // 直接调用
	DependencyTypeWorkflow     DependencyType = "workflow"      // 工作流依赖
	DependencyTypeHTTP         DependencyType = "http"          // HTTP 调用
)

// FunctionDependency 函数依赖关系
type FunctionDependency struct {
	SourceID       string         `json:"source_id"`       // 调用方函数 ID
	SourceName     string         `json:"source_name"`     // 调用方函数名
	TargetID       string         `json:"target_id"`       // 被调用函数 ID
	TargetName     string         `json:"target_name"`     // 被调用函数名
	Type           DependencyType `json:"type"`            // 依赖类型
	CallCount      int64          `json:"call_count"`      // 调用次数
	LastCalledAt   *time.Time     `json:"last_called_at,omitempty"`
}

// DependencyGraph 依赖图
type DependencyGraph struct {
	Nodes []DependencyNode `json:"nodes"`
	Edges []DependencyEdge `json:"edges"`
}

// DependencyNode 依赖图节点
type DependencyNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"` // function, workflow
	Runtime  string `json:"runtime,omitempty"`
	Status   string `json:"status"`
}

// DependencyEdge 依赖图边
type DependencyEdge struct {
	Source    string         `json:"source"`
	Target    string         `json:"target"`
	Type      DependencyType `json:"type"`
	CallCount int64          `json:"call_count"`
}

// ImpactAnalysis 影响分析结果
type ImpactAnalysis struct {
	FunctionID        string              `json:"function_id"`
	FunctionName      string              `json:"function_name"`
	DirectDependents  []DependencyNode    `json:"direct_dependents"`  // 直接依赖此函数的
	IndirectDependents []DependencyNode   `json:"indirect_dependents"` // 间接依赖此函数的
	AffectedWorkflows []string            `json:"affected_workflows"` // 受影响的工作流
	TotalImpactCount  int                 `json:"total_impact_count"` // 总影响数量
}
