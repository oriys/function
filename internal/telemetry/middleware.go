// Package telemetry 提供 OpenTelemetry 分布式追踪功能的封装。
// 本文件实现了 HTTP 中间件和客户端传输层的追踪集成，
// 用于自动为 HTTP 请求创建和传播追踪上下文。
package telemetry

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// HTTPMiddleware 返回一个 HTTP 中间件，用于为传入的 HTTP 请求自动创建追踪 Span。
// 该中间件会：
//   - 从请求头中提取追踪上下文（如果存在）
//   - 创建新的 Span 来追踪请求处理
//   - 自动记录请求方法、路径、状态码等信息
//   - 将追踪上下文传递给下游处理器
//
// 参数：
//   - serviceName: 服务名称，用于标识追踪数据来源
//
// 返回：
//   - func(http.Handler) http.Handler: HTTP 中间件函数
//
// 使用示例：
//
//	router := mux.NewRouter()
//	router.Use(telemetry.HTTPMiddleware("my-service"))
func HTTPMiddleware(serviceName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		// 使用 otelhttp 包装处理器，自动添加追踪功能
		return otelhttp.NewHandler(next, serviceName,
			// 使用全局追踪提供者
			otelhttp.WithTracerProvider(otel.GetTracerProvider()),
			// 使用全局上下文传播器，支持从请求头提取和注入追踪上下文
			otelhttp.WithPropagators(otel.GetTextMapPropagator()),
			// 为每个 Span 添加服务名称属性
			otelhttp.WithSpanOptions(
				trace.WithAttributes(
					attribute.String("service.name", serviceName),
				),
			),
			// 自定义 Span 名称格式：HTTP方法 + 路径（如 "GET /api/users"）
			otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
				return r.Method + " " + r.URL.Path
			}),
		)
	}
}

// HTTPClientTransport 返回一个带追踪功能的 http.RoundTripper。
// 该传输层会为所有发出的 HTTP 请求自动：
//   - 创建客户端 Span 来追踪请求
//   - 将追踪上下文注入到请求头中（用于跨服务追踪传播）
//   - 记录请求的 URL、方法、响应状态码等信息
//
// 参数：
//   - base: 基础传输层，如果为 nil 则使用 http.DefaultTransport
//
// 返回：
//   - http.RoundTripper: 带追踪功能的传输层
//
// 使用示例：
//
//	client := &http.Client{
//	    Transport: telemetry.HTTPClientTransport(nil),
//	}
func HTTPClientTransport(base http.RoundTripper) http.RoundTripper {
	// 如果未提供基础传输层，使用默认传输层
	if base == nil {
		base = http.DefaultTransport
	}
	// 使用 otelhttp 包装传输层，添加追踪功能
	return otelhttp.NewTransport(base,
		// 使用全局追踪提供者
		otelhttp.WithTracerProvider(otel.GetTracerProvider()),
		// 使用全局传播器，将追踪上下文注入到出站请求头
		otelhttp.WithPropagators(otel.GetTextMapPropagator()),
	)
}

// InstrumentedHTTPClient 返回一个预配置了追踪功能的 HTTP 客户端。
// 使用该客户端发出的所有请求都会自动被追踪，并且追踪上下文
// 会通过 HTTP 头传播到下游服务。
//
// 返回：
//   - *http.Client: 带追踪功能的 HTTP 客户端
//
// 使用示例：
//
//	client := telemetry.InstrumentedHTTPClient()
//	resp, err := client.Get("https://api.example.com/data")
func InstrumentedHTTPClient() *http.Client {
	return &http.Client{
		// 使用带追踪功能的传输层
		Transport: HTTPClientTransport(nil),
	}
}
