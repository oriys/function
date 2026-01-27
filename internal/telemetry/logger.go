// Package telemetry 提供 OpenTelemetry 分布式追踪功能的封装。
// 本文件实现了日志与追踪的集成，通过 Logrus Hook 和辅助函数
// 自动将追踪上下文（Trace ID、Span ID）注入到日志条目中，
// 便于在日志系统中关联追踪数据进行问题排查。
package telemetry

import (
	"context"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
)

// LogrusHook 是一个 Logrus 钩子，用于自动将追踪上下文添加到日志条目中。
// 当日志条目包含有效的追踪上下文时，会自动添加 trace_id、span_id 和
// trace_sampled 字段，实现日志与追踪数据的关联。
type LogrusHook struct{}

// NewLogrusHook 创建一个新的 LogrusHook 实例。
// 将返回的钩子添加到 Logrus Logger 即可启用自动追踪上下文注入。
//
// 使用示例：
//
//	logger := logrus.New()
//	logger.AddHook(telemetry.NewLogrusHook())
func NewLogrusHook() *LogrusHook {
	return &LogrusHook{}
}

// Levels 返回该钩子应该触发的日志级别列表。
// 返回 logrus.AllLevels 表示该钩子会在所有日志级别触发，
// 确保所有日志都能关联追踪上下文。
func (h *LogrusHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

// Fire 在日志条目生成时被调用，用于向日志添加追踪上下文信息。
// 该方法会检查日志条目的上下文中是否包含有效的 Span，如果有，
// 则将 trace_id、span_id 和 trace_sampled 字段添加到日志数据中。
//
// 参数：
//   - entry: 即将被写入的日志条目
//
// 返回：
//   - error: 处理过程中的错误（通常返回 nil）
func (h *LogrusHook) Fire(entry *logrus.Entry) error {
	// 获取日志条目关联的上下文
	ctx := entry.Context
	if ctx == nil {
		// 如果没有上下文，跳过处理
		return nil
	}

	// 从上下文中提取当前 Span
	span := trace.SpanFromContext(ctx)
	// 检查 Span 上下文是否有效
	if !span.SpanContext().IsValid() {
		// 如果 Span 无效，跳过处理
		return nil
	}

	// 获取 Span 上下文，包含 Trace ID 和 Span ID
	spanCtx := span.SpanContext()
	// 将 Trace ID 添加到日志字段，用于跨服务追踪关联
	entry.Data["trace_id"] = spanCtx.TraceID().String()
	// 将 Span ID 添加到日志字段，标识当前操作
	entry.Data["span_id"] = spanCtx.SpanID().String()

	// 如果该追踪被采样（将被导出），添加采样标记
	if spanCtx.IsSampled() {
		entry.Data["trace_sampled"] = true
	}

	return nil
}

// LoggerWithTraceContext 返回一个带有追踪上下文字段的日志条目。
// 该函数从上下文中提取追踪信息，并创建包含 trace_id、span_id
// 和 trace_sampled 字段的日志条目。
//
// 参数：
//   - ctx: 包含追踪信息的上下文
//   - logger: Logrus Logger 实例
//
// 返回：
//   - *logrus.Entry: 带有追踪上下文字段的日志条目
//
// 使用示例：
//
//	entry := telemetry.LoggerWithTraceContext(ctx, logger)
//	entry.Info("处理请求")
func LoggerWithTraceContext(ctx context.Context, logger *logrus.Logger) *logrus.Entry {
	// 从上下文中获取当前 Span
	span := trace.SpanFromContext(ctx)
	// 如果 Span 上下文无效，返回不带追踪字段的日志条目
	if !span.SpanContext().IsValid() {
		return logrus.NewEntry(logger)
	}

	// 获取 Span 上下文并创建带追踪字段的日志条目
	spanCtx := span.SpanContext()
	return logger.WithFields(logrus.Fields{
		"trace_id":      spanCtx.TraceID().String(),  // 追踪链路唯一标识
		"span_id":       spanCtx.SpanID().String(),   // 当前操作唯一标识
		"trace_sampled": spanCtx.IsSampled(),         // 是否被采样
	})
}

// EntryWithTraceContext 向现有日志条目添加追踪上下文字段。
// 与 LoggerWithTraceContext 类似，但接受现有的日志条目而非 Logger，
// 便于在已有日志条目基础上追加追踪信息。
//
// 参数：
//   - ctx: 包含追踪信息的上下文
//   - entry: 现有的日志条目
//
// 返回：
//   - *logrus.Entry: 带有追踪上下文字段的日志条目
//
// 使用示例：
//
//	entry := logger.WithField("user_id", userID)
//	entry = telemetry.EntryWithTraceContext(ctx, entry)
//	entry.Info("用户操作")
func EntryWithTraceContext(ctx context.Context, entry *logrus.Entry) *logrus.Entry {
	// 从上下文中获取当前 Span
	span := trace.SpanFromContext(ctx)
	// 如果 Span 上下文无效，返回原始日志条目
	if !span.SpanContext().IsValid() {
		return entry
	}

	// 获取 Span 上下文并添加追踪字段到现有条目
	spanCtx := span.SpanContext()
	return entry.WithFields(logrus.Fields{
		"trace_id":      spanCtx.TraceID().String(),  // 追踪链路唯一标识
		"span_id":       spanCtx.SpanID().String(),   // 当前操作唯一标识
		"trace_sampled": spanCtx.IsSampled(),         // 是否被采样
	})
}

// loggerKey 是用于在上下文中存储 Logger 的键类型。
// 使用空结构体作为键类型是 Go 的惯用模式，避免键冲突。
type loggerKey struct{}

// WithLoggerContext 将上下文附加到日志条目，以便追踪关联。
// 这使得 LogrusHook 能够从日志条目中访问上下文，
// 从而自动提取和注入追踪信息。
//
// 参数：
//   - entry: 日志条目
//   - ctx: 包含追踪信息的上下文
//
// 返回：
//   - *logrus.Entry: 带有上下文的日志条目
//
// 使用示例：
//
//	entry := telemetry.WithLoggerContext(logger.WithField("key", "value"), ctx)
//	entry.Info("带追踪上下文的日志")
func WithLoggerContext(entry *logrus.Entry, ctx context.Context) *logrus.Entry {
	return entry.WithContext(ctx)
}
