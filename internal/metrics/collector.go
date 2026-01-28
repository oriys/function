// Package metrics 提供 Prometheus 指标采集与上报的统一封装。
// 该包集中定义平台关键指标（调用、虚拟机池、调度器等），便于在各模块复用并保持标签一致。
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics 封装平台运行时指标集合。
// 所有字段均为 Prometheus 指标类型，通过辅助方法更新指标值。
//
// 指标分类:
//   - 调用指标: 跟踪函数调用的数量、耗时和错误
//   - 虚拟机池指标: 监控 VM 池的容量和使用情况
//   - 函数指标: 统计注册的函数数量
//   - 调度器指标: 监控调度队列和工作线程
type Metrics struct {
	// ========== 调用相关指标 ==========

	// InvocationsTotal 函数调用总次数计数器
	// 标签: function_id, function_name, runtime, status
	InvocationsTotal *prometheus.CounterVec

	// InvocationDuration 函数调用耗时直方图（单位：毫秒）
	// 标签: function_id, function_name, runtime, cold_start
	// 桶边界: 10, 50, 100, 250, 500, 1000, 2500, 5000, 10000 ms
	InvocationDuration *prometheus.HistogramVec

	// InvocationErrors 调用错误计数器，按错误类型分类
	// 标签: function_id, function_name, error_type
	InvocationErrors *prometheus.CounterVec

	// ========== 虚拟机池相关指标 ==========

	// VMPoolSize 虚拟机池总容量
	// 标签: runtime
	VMPoolSize *prometheus.GaugeVec

	// VMPoolWarm 预热状态的虚拟机数量（可立即使用）
	// 标签: runtime
	VMPoolWarm *prometheus.GaugeVec

	// VMPoolBusy 忙碌状态的虚拟机数量（正在执行任务）
	// 标签: runtime
	VMPoolBusy *prometheus.GaugeVec

	// ColdStarts 冷启动次数计数器（需要创建新 VM）
	// 标签: function_id, function_name, runtime
	ColdStarts *prometheus.CounterVec

	// WarmStarts 热启动次数计数器（复用预热 VM）
	// 标签: function_id, function_name, runtime
	WarmStarts *prometheus.CounterVec

	// VMBootDuration 虚拟机启动耗时直方图（单位：毫秒）
	// 标签: runtime, from_snapshot
	VMBootDuration *prometheus.HistogramVec

	// VMRestoreDuration 从快照恢复虚拟机的耗时直方图（单位：毫秒）
	// 标签: runtime
	VMRestoreDuration *prometheus.HistogramVec

	// ========== 函数相关指标 ==========

	// FunctionsTotal 注册的函数总数
	FunctionsTotal prometheus.Gauge

	// ActiveFunctions 当前活跃的函数数量
	ActiveFunctions prometheus.Gauge

	// ========== 调度器相关指标 ==========

	// SchedulerQueueSize 调度器等待队列中的任务数
	SchedulerQueueSize prometheus.Gauge

	// SchedulerWorkers 调度器工作线程数量
	SchedulerWorkers prometheus.Gauge

	// ========== 状态操作相关指标 ==========

	// StateOperationsTotal 状态操作总次数计数器
	// 标签: function_id, operation, scope, success
	StateOperationsTotal *prometheus.CounterVec

	// StateOperationDuration 状态操作耗时直方图（单位：毫秒）
	// 标签: function_id, operation
	StateOperationDuration *prometheus.HistogramVec

	// SessionRouteTotal 会话路由次数计数器
	// 标签: function_id, result (hit/miss/new)
	SessionRouteTotal *prometheus.CounterVec

	// ActiveSessionsGauge 活跃会话数
	// 标签: function_id
	ActiveSessionsGauge *prometheus.GaugeVec

	// StateSizeBytes 状态数据大小（字节）
	// 标签: function_id, scope
	StateSizeBytes *prometheus.GaugeVec

	// ========== 快照相关指标 ==========

	// SnapshotsTotal 快照总数
	// 标签: status (ready/building/failed/expired)
	SnapshotsTotal *prometheus.GaugeVec

	// SnapshotBuildDuration 快照构建耗时直方图（单位：毫秒）
	// 标签: runtime, success
	SnapshotBuildDuration *prometheus.HistogramVec

	// SnapshotRestoreDuration 快照恢复耗时直方图（单位：毫秒）
	// 标签: runtime
	SnapshotRestoreDuration *prometheus.HistogramVec

	// SnapshotSizeBytes 快照文件大小
	// 标签: function_id
	SnapshotSizeBytes *prometheus.GaugeVec
}

// NewMetrics 创建并注册一组 Prometheus 指标。
// namespace 用于作为所有指标名前缀，便于在同一 Prometheus 中区分不同应用。
func NewMetrics(namespace string) *Metrics {
	return &Metrics{
		InvocationsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "invocations_total",
				Help:      "Total number of function invocations",
			},
			[]string{"function_id", "function_name", "runtime", "status"},
		),
		InvocationDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "invocation_duration_ms",
				Help:      "Function invocation duration in milliseconds",
				Buckets:   []float64{10, 50, 100, 250, 500, 1000, 2500, 5000, 10000},
			},
			[]string{"function_id", "function_name", "runtime", "cold_start"},
		),
		InvocationErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "invocation_errors_total",
				Help:      "Total number of invocation errors",
			},
			[]string{"function_id", "function_name", "error_type"},
		),
		VMPoolSize: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "vm_pool_size",
				Help:      "Total VMs in pool",
			},
			[]string{"runtime"},
		),
		VMPoolWarm: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "vm_pool_warm",
				Help:      "Warm VMs available in pool",
			},
			[]string{"runtime"},
		),
		VMPoolBusy: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "vm_pool_busy",
				Help:      "Busy VMs in pool",
			},
			[]string{"runtime"},
		),
		ColdStarts: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "cold_starts_total",
				Help:      "Total cold starts",
			},
			[]string{"function_id", "function_name", "runtime"},
		),
		WarmStarts: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "warm_starts_total",
				Help:      "Total warm starts",
			},
			[]string{"function_id", "function_name", "runtime"},
		),
		VMBootDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "vm_boot_duration_ms",
				Help:      "VM boot duration in milliseconds",
				Buckets:   []float64{100, 250, 500, 1000, 2000, 5000},
			},
			[]string{"runtime", "from_snapshot"},
		),
		VMRestoreDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "vm_restore_duration_ms",
				Help:      "VM restore from snapshot duration in milliseconds",
				Buckets:   []float64{5, 10, 25, 50, 100, 250},
			},
			[]string{"runtime"},
		),
		FunctionsTotal: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "functions_total",
				Help:      "Total number of registered functions",
			},
		),
		ActiveFunctions: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "active_functions",
				Help:      "Number of active functions",
			},
		),
		SchedulerQueueSize: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "scheduler_queue_size",
				Help:      "Current scheduler queue size",
			},
		),
		SchedulerWorkers: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "scheduler_workers",
				Help:      "Number of scheduler workers",
			},
		),
		// 状态操作指标
		StateOperationsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "state_operations_total",
				Help:      "Total number of state operations",
			},
			[]string{"function_id", "operation", "scope", "success"},
		),
		StateOperationDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "state_operation_duration_ms",
				Help:      "State operation duration in milliseconds",
				Buckets:   []float64{0.5, 1, 2, 5, 10, 25, 50, 100},
			},
			[]string{"function_id", "operation"},
		),
		SessionRouteTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "session_route_total",
				Help:      "Total number of session routing operations",
			},
			[]string{"function_id", "result"},
		),
		ActiveSessionsGauge: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "active_sessions",
				Help:      "Number of active sessions",
			},
			[]string{"function_id"},
		),
		StateSizeBytes: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "state_size_bytes",
				Help:      "Total size of state data in bytes",
			},
			[]string{"function_id", "scope"},
		),
		// 快照指标
		SnapshotsTotal: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "snapshots_total",
				Help:      "Total number of snapshots by status",
			},
			[]string{"status"},
		),
		SnapshotBuildDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "snapshot_build_duration_ms",
				Help:      "Snapshot build duration in milliseconds",
				Buckets:   []float64{1000, 2000, 5000, 10000, 30000, 60000},
			},
			[]string{"runtime", "success"},
		),
		SnapshotRestoreDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "snapshot_restore_duration_ms",
				Help:      "Snapshot restore duration in milliseconds",
				Buckets:   []float64{5, 10, 25, 50, 100, 250, 500},
			},
			[]string{"runtime"},
		),
		SnapshotSizeBytes: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "snapshot_size_bytes",
				Help:      "Snapshot file size in bytes",
			},
			[]string{"function_id"},
		),
	}
}

// RecordInvocation 记录一次函数调用的统计信息。
// durationMs 为调用耗时（毫秒），coldStart 表示是否为冷启动。
func (m *Metrics) RecordInvocation(functionID, functionName, runtime, status string, durationMs float64, coldStart bool) {
	m.InvocationsTotal.WithLabelValues(functionID, functionName, runtime, status).Inc()

	coldStartStr := "false"
	if coldStart {
		coldStartStr = "true"
		m.ColdStarts.WithLabelValues(functionID, functionName, runtime).Inc()
	} else {
		m.WarmStarts.WithLabelValues(functionID, functionName, runtime).Inc()
	}

	m.InvocationDuration.WithLabelValues(functionID, functionName, runtime, coldStartStr).Observe(durationMs)
}

// RecordError 记录一次调用错误（按 error_type 聚合）。
func (m *Metrics) RecordError(functionID, functionName, errorType string) {
	m.InvocationErrors.WithLabelValues(functionID, functionName, errorType).Inc()
}

// UpdatePoolStats 更新虚拟机池统计指标。
func (m *Metrics) UpdatePoolStats(runtime string, warm, busy, total int) {
	m.VMPoolWarm.WithLabelValues(runtime).Set(float64(warm))
	m.VMPoolBusy.WithLabelValues(runtime).Set(float64(busy))
	m.VMPoolSize.WithLabelValues(runtime).Set(float64(total))
}

// RecordVMBoot 记录虚拟机启动耗时。
func (m *Metrics) RecordVMBoot(runtime string, durationMs float64, fromSnapshot bool) {
	snapshotStr := "false"
	if fromSnapshot {
		snapshotStr = "true"
	}
	m.VMBootDuration.WithLabelValues(runtime, snapshotStr).Observe(durationMs)
}

// RecordStateOperation 记录一次状态操作的统计信息。
func (m *Metrics) RecordStateOperation(functionID, operation, scope string, success bool, durationMs float64) {
	successStr := "true"
	if !success {
		successStr = "false"
	}
	m.StateOperationsTotal.WithLabelValues(functionID, operation, scope, successStr).Inc()
	m.StateOperationDuration.WithLabelValues(functionID, operation).Observe(durationMs)
}

// RecordSessionRoute 记录会话路由结果。
// result: "hit" (缓存命中), "miss" (缓存未命中), "new" (新会话)
func (m *Metrics) RecordSessionRoute(functionID, result string) {
	m.SessionRouteTotal.WithLabelValues(functionID, result).Inc()
}

// UpdateActiveSessions 更新活跃会话数。
func (m *Metrics) UpdateActiveSessions(functionID string, count int) {
	m.ActiveSessionsGauge.WithLabelValues(functionID).Set(float64(count))
}

// UpdateStateSize 更新状态数据大小。
func (m *Metrics) UpdateStateSize(functionID, scope string, sizeBytes int64) {
	m.StateSizeBytes.WithLabelValues(functionID, scope).Set(float64(sizeBytes))
}

// UpdateSnapshotStats 更新快照统计。
func (m *Metrics) UpdateSnapshotStats(ready, building, failed, expired int) {
	m.SnapshotsTotal.WithLabelValues("ready").Set(float64(ready))
	m.SnapshotsTotal.WithLabelValues("building").Set(float64(building))
	m.SnapshotsTotal.WithLabelValues("failed").Set(float64(failed))
	m.SnapshotsTotal.WithLabelValues("expired").Set(float64(expired))
}

// RecordSnapshotBuild 记录快照构建耗时。
func (m *Metrics) RecordSnapshotBuild(runtime string, durationMs float64, success bool) {
	successStr := "true"
	if !success {
		successStr = "false"
	}
	m.SnapshotBuildDuration.WithLabelValues(runtime, successStr).Observe(durationMs)
}

// RecordSnapshotRestore 记录快照恢复耗时。
func (m *Metrics) RecordSnapshotRestore(runtime string, durationMs float64) {
	m.SnapshotRestoreDuration.WithLabelValues(runtime).Observe(durationMs)
}

// UpdateSnapshotSize 更新快照大小。
func (m *Metrics) UpdateSnapshotSize(functionID string, sizeBytes int64) {
	m.SnapshotSizeBytes.WithLabelValues(functionID).Set(float64(sizeBytes))
}
