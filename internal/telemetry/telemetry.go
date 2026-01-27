// Package telemetry 提供 OpenTelemetry 分布式追踪功能的封装。
// 该包实现了基于 OpenTelemetry 标准的遥测数据收集，支持分布式链路追踪，
// 可将追踪数据导出到兼容 OTLP 协议的后端（如 Tempo、Jaeger 等）。
// 主要功能包括：
//   - 初始化和配置 OpenTelemetry 追踪器
//   - 创建和管理追踪 Span
//   - 从上下文中提取追踪信息（Trace ID、Span ID）
//   - 支持采样率配置以控制追踪数据量
package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Config 定义遥测配置结构体。
// 该结构体包含了初始化 OpenTelemetry 追踪所需的所有配置项，
// 支持通过 YAML 配置文件进行配置。
type Config struct {
	// Enabled 控制是否启用遥测功能，设为 false 时将跳过追踪器初始化
	Enabled bool `yaml:"enabled"`
	// Endpoint 指定 OTLP 接收器的 gRPC 端点地址，例如 "tempo:4317"
	Endpoint string `yaml:"endpoint"` // e.g., tempo:4317
	// ServiceName 标识当前服务的名称，将作为追踪数据的服务标识
	ServiceName string `yaml:"service_name"` // e.g., function-gateway
	// SampleRate 采样率，取值范围 0.0 到 1.0（1.0 表示 100% 采样）
	SampleRate float64 `yaml:"sample_rate"` // 0.0 to 1.0 (1.0 = 100%)
	// Environment 标识当前运行环境，如 production、staging、development
	Environment string `yaml:"environment"` // e.g., production, staging
}

// Telemetry 封装了 OpenTelemetry 的追踪提供者和导出器。
// 该结构体是遥测功能的核心，持有配置信息、追踪提供者和追踪器实例，
// 负责管理追踪数据的生命周期。
type Telemetry struct {
	// config 保存遥测配置
	config Config
	// tracerProvider 是 OpenTelemetry SDK 的追踪提供者，负责创建追踪器和管理 Span 处理
	tracerProvider *sdktrace.TracerProvider
	// tracer 是用于创建 Span 的追踪器实例
	tracer trace.Tracer
}

// New 根据给定配置创建新的 Telemetry 实例。
// 该函数执行以下操作：
//  1. 如果未启用遥测，返回仅包含空操作追踪器的实例
//  2. 设置配置默认值（服务名、采样率、端点）
//  3. 建立到 OTLP 接收器的 gRPC 连接
//  4. 创建资源对象，包含服务信息和环境属性
//  5. 配置采样器和追踪提供者
//  6. 设置全局追踪提供者和上下文传播器
//
// 参数：
//   - ctx: 上下文，用于控制连接超时
//   - cfg: 遥测配置
//
// 返回：
//   - *Telemetry: 初始化完成的遥测实例
//   - error: 初始化过程中的错误
func New(ctx context.Context, cfg Config) (*Telemetry, error) {
	// 如果遥测功能未启用，返回一个仅包含空操作追踪器的实例
	if !cfg.Enabled {
		return &Telemetry{
			config: cfg,
			tracer: otel.Tracer(cfg.ServiceName),
		}, nil
	}

	// 设置配置默认值
	if cfg.ServiceName == "" {
		cfg.ServiceName = "nimbus-gateway" // 默认服务名
	}
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 0.1 // 默认采样率 10%，平衡追踪覆盖率和性能开销
	}
	if cfg.SampleRate > 1 {
		cfg.SampleRate = 1.0 // 采样率上限为 100%
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "tempo:4317" // 默认 Tempo gRPC 端点
	}

	// 创建带超时的上下文，限制 gRPC 连接建立时间为 10 秒
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// 建立到 OTLP 接收器的 gRPC 连接
	// 使用不安全凭据（内网通信场景）和阻塞模式确保连接成功
	conn, err := grpc.DialContext(ctx, cfg.Endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()), // 使用不安全传输（适用于内网环境）
		grpc.WithBlock(), // 阻塞直到连接建立成功
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to %s: %w", cfg.Endpoint, err)
	}

	// 创建 OTLP gRPC 追踪导出器，用于将追踪数据发送到后端
	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
	}

	// 创建资源对象，包含服务的元数据信息
	// 这些信息会附加到所有追踪数据上，用于标识数据来源
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),            // 服务名称（遵循 OpenTelemetry 语义约定）
			semconv.ServiceVersion("1.0.0"),                 // 服务版本
			attribute.String("environment", cfg.Environment), // 运行环境
		),
		resource.WithHost(),    // 添加主机信息
		resource.WithOS(),      // 添加操作系统信息
		resource.WithProcess(), // 添加进程信息
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// 根据采样率创建采样器
	// 采样器决定哪些追踪会被记录和导出
	var sampler sdktrace.Sampler
	if cfg.SampleRate >= 1.0 {
		sampler = sdktrace.AlwaysSample() // 100% 采样：记录所有追踪
	} else if cfg.SampleRate <= 0 {
		sampler = sdktrace.NeverSample() // 0% 采样：不记录任何追踪
	} else {
		// 基于 TraceID 的比率采样，确保同一追踪的所有 Span 采样决策一致
		sampler = sdktrace.TraceIDRatioBased(cfg.SampleRate)
	}

	// 创建追踪提供者，配置批量处理器和采样策略
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter), // 使用批量处理器，异步发送追踪数据以提高性能
		sdktrace.WithResource(res),     // 关联资源信息
		// 使用父级采样策略：如果父 Span 已被采样，子 Span 也会被采样
		sdktrace.WithSampler(sdktrace.ParentBased(sampler)),
	)

	// 设置全局追踪提供者，使其他代码可以通过 otel.Tracer() 获取追踪器
	otel.SetTracerProvider(tp)
	// 设置全局上下文传播器，支持跨服务追踪上下文传递
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, // W3C Trace Context 标准，传播 traceparent 头
		propagation.Baggage{},      // W3C Baggage 标准，传播自定义键值对
	))

	return &Telemetry{
		config:         cfg,
		tracerProvider: tp,
		tracer:         tp.Tracer(cfg.ServiceName), // 使用服务名创建追踪器
	}, nil
}

// Tracer 返回用于创建 Span 的追踪器实例。
// 通过返回的追踪器可以创建新的追踪 Span 来记录操作。
func (t *Telemetry) Tracer() trace.Tracer {
	return t.tracer
}

// Shutdown 优雅关闭遥测提供者。
// 该方法会刷新所有待发送的追踪数据并释放资源，
// 应在应用程序退出前调用以确保数据不丢失。
//
// 参数：
//   - ctx: 上下文，用于控制关闭超时
//
// 返回：
//   - error: 关闭过程中的错误
func (t *Telemetry) Shutdown(ctx context.Context) error {
	// 如果追踪提供者未初始化（遥测未启用），直接返回
	if t.tracerProvider == nil {
		return nil
	}
	return t.tracerProvider.Shutdown(ctx)
}

// IsEnabled 返回遥测功能是否已启用。
// 可用于在代码中条件性地执行追踪相关逻辑。
func (t *Telemetry) IsEnabled() bool {
	return t.config.Enabled
}

// GetTracer 返回具有指定名称的追踪器。
// 这是一个便捷函数，从全局追踪提供者获取追踪器实例。
//
// 参数：
//   - name: 追踪器名称，通常使用包名或模块名
//
// 返回：
//   - trace.Tracer: 追踪器实例
func GetTracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// SpanFromContext 从上下文中获取当前 Span。
// 如果上下文中没有 Span，返回一个空操作 Span。
//
// 参数：
//   - ctx: 包含 Span 的上下文
//
// 返回：
//   - trace.Span: 当前 Span
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// TraceIDFromContext 从上下文中提取 Trace ID。
// Trace ID 是追踪链路的唯一标识符，同一请求的所有 Span 共享相同的 Trace ID。
//
// 参数：
//   - ctx: 包含追踪信息的上下文
//
// 返回：
//   - string: Trace ID 字符串，如果上下文无效则返回空字符串
func TraceIDFromContext(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	// 检查 Span 上下文是否有效
	if !span.SpanContext().IsValid() {
		return ""
	}
	return span.SpanContext().TraceID().String()
}

// SpanIDFromContext 从上下文中提取 Span ID。
// Span ID 是单个操作的唯一标识符。
//
// 参数：
//   - ctx: 包含追踪信息的上下文
//
// 返回：
//   - string: Span ID 字符串，如果上下文无效则返回空字符串
func SpanIDFromContext(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	// 检查 Span 上下文是否有效
	if !span.SpanContext().IsValid() {
		return ""
	}
	return span.SpanContext().SpanID().String()
}

// StartSpan 创建一个具有指定名称和选项的新 Span。
// 新 Span 会自动成为上下文中当前 Span 的子 Span（如果存在）。
//
// 参数：
//   - ctx: 父上下文
//   - name: Span 名称，描述被追踪的操作
//   - opts: 可选的 Span 启动选项（如属性、链接等）
//
// 返回：
//   - context.Context: 包含新 Span 的上下文
//   - trace.Span: 新创建的 Span，使用完毕后需调用 End() 方法结束
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return otel.Tracer("function").Start(ctx, name, opts...)
}

// AddSpanAttributes 向当前 Span 添加属性。
// 属性是键值对，用于记录与操作相关的额外信息。
//
// 参数：
//   - ctx: 包含当前 Span 的上下文
//   - attrs: 要添加的属性列表
func AddSpanAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attrs...)
}

// RecordError 在当前 Span 上记录错误。
// 记录的错误会在追踪系统中显示，便于问题排查。
//
// 参数：
//   - ctx: 包含当前 Span 的上下文
//   - err: 要记录的错误
func RecordError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
}
