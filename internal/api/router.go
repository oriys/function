// Package api 提供了函数即服务(FaaS)平台的HTTP API处理程序。
// 该文件负责配置HTTP路由器和中间件，将HTTP请求映射到相应的处理器方法。
package api

import (
	"io/fs"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/oriys/nimbus/internal/telemetry"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// RouterConfig 路由器配置选项
type RouterConfig struct {
	// Handler API处理器
	Handler *Handler
	// WorkflowHandler 工作流处理器（可选）
	WorkflowHandler *WorkflowHandler
	// SnapshotHandler 快照处理器（可选）
	SnapshotHandler *SnapshotHandler
	// StateHandler 状态处理器（可选）
	StateHandler *StateHandler
	// Logger 日志记录器
	Logger *logrus.Logger
	// WebFS 前端静态文件系统（可选，用于嵌入前端资源）
	WebFS fs.FS
}

// NewRouter 创建并配置HTTP路由器。
//
// 功能说明：
//   - 创建chi路由器实例并配置全局中间件
//   - 注册健康检查和指标端点
//   - 配置API v1版本的所有路由
//   - 配置Web控制台API和静态文件服务
//
// 参数：
//   - cfg: 路由器配置，包含Handler、Logger和可选的前端文件系统
//
// 返回值：
//   - *chi.Mux: 配置完成的路由器实例
//
// 路由结构：
//
//	/health              - 基本健康检查
//	/health/ready        - Kubernetes就绪探针
//	/health/live         - Kubernetes存活探针
//	/metrics             - Prometheus指标端点
//	/api/v1/functions    - 函数管理API
//	/api/v1/invocations  - 调用记录API
//	/api/v1/stats        - 系统统计API
//	/api/console/*       - Web控制台API
//	/*                   - 前端静态文件（如果配置了WebFS）
func NewRouter(cfg *RouterConfig) *chi.Mux {
	h := cfg.Handler
	// 创建新的chi路由器
	r := chi.NewRouter()

	// 配置中间件链
	// 中间件按照添加顺序执行，形成洋葱模型

	// 遥测中间件：记录HTTP请求的追踪信息
	r.Use(telemetry.HTTPMiddleware("nimbus-gateway"))

	// RequestID中间件：为每个请求生成唯一ID，便于日志追踪
	r.Use(middleware.RequestID)

	// RealIP中间件：从X-Forwarded-For等头部获取真实客户端IP
	r.Use(middleware.RealIP)

	// Compress中间件：对响应进行gzip压缩，减少网络传输
	r.Use(middleware.Compress(5, "application/json", "text/html", "text/plain", "text/css", "application/javascript"))

	// Logger中间件：记录请求日志
	r.Use(middleware.Logger)

	// Recoverer中间件：捕获panic并返回500错误，防止服务崩溃
	r.Use(middleware.Recoverer)

	// Timeout中间件：设置请求超时时间为60秒
	r.Use(middleware.Timeout(60 * time.Second))

	// CORS中间件：处理跨域请求
	r.Use(corsMiddleware)

	// 健康检查端点 - 用于负载均衡器和Kubernetes探针
	r.Get("/health", h.Health)           // 基本健康检查
	r.Get("/health/ready", h.Ready)      // Kubernetes就绪探针
	r.Get("/health/live", h.Live)        // Kubernetes存活探针

	// Prometheus指标端点 - 暴露应用程序指标供监控系统采集
	r.Handle("/metrics", promhttp.Handler())

	// Webhook 触发端点 - 外部系统通过此 URL 触发函数
	// POST /webhook/{key} - 通过 Webhook 密钥触发函数
	r.Post("/webhook/{key}", h.HandleWebhook)

	// API v1 路由组
	r.Route("/api/v1", func(r chi.Router) {
		// 函数管理路由组
		r.Route("/functions", func(r chi.Router) {
			// POST /api/v1/functions - 创建新函数
			r.Post("/", h.CreateFunction)
			// GET /api/v1/functions - 获取函数列表
			r.Get("/", h.ListFunctions)
			// POST /api/v1/functions/import - 导入函数
			r.Post("/import", h.ImportFunction)
			// POST /api/v1/functions/bulk-delete - 批量删除函数
			r.Post("/bulk-delete", h.BulkDeleteFunctions)
			// POST /api/v1/functions/bulk-update - 批量更新函数
			r.Post("/bulk-update", h.BulkUpdateFunctions)
			// POST /api/v1/functions/from-template - 从模板创建函数
			r.Post("/from-template", h.CreateFunctionFromTemplate)

			// 单个函数的操作路由组
			r.Route("/{id}", func(r chi.Router) {
				// GET /api/v1/functions/{id} - 获取函数详情
				r.Get("/", h.GetFunction)
				// PUT /api/v1/functions/{id} - 更新函数
				r.Put("/", h.UpdateFunction)
				// DELETE /api/v1/functions/{id} - 删除函数
				r.Delete("/", h.DeleteFunction)
				// POST /api/v1/functions/{id}/clone - 克隆函数
				r.Post("/clone", h.CloneFunction)
				// POST /api/v1/functions/{id}/invoke - 同步调用函数
				r.Post("/invoke", h.InvokeFunction)
				// POST /api/v1/functions/{id}/async - 异步调用函数
				r.Post("/async", h.InvokeFunctionAsync)
				// GET /api/v1/functions/{id}/invocations - 获取函数的调用记录
				r.Get("/invocations", h.ListInvocations)

				// 函数状态管理路由
				// POST /api/v1/functions/{id}/offline - 下线函数
				r.Post("/offline", h.OfflineFunction)
				// POST /api/v1/functions/{id}/online - 上线函数
				r.Post("/online", h.OnlineFunction)
				// POST /api/v1/functions/{id}/recompile - 重新编译函数
				r.Post("/recompile", h.RecompileFunction)
				// POST /api/v1/functions/{id}/pin - 置顶/取消置顶函数
				r.Post("/pin", h.PinFunction)
				// GET /api/v1/functions/{id}/export - 导出函数配置
				r.Get("/export", h.ExportFunction)

				// Webhook 管理路由组
				r.Route("/webhook", func(r chi.Router) {
					// POST /api/v1/functions/{id}/webhook/enable - 启用 Webhook
					r.Post("/enable", h.EnableWebhook)
					// POST /api/v1/functions/{id}/webhook/disable - 禁用 Webhook
					r.Post("/disable", h.DisableWebhook)
					// POST /api/v1/functions/{id}/webhook/regenerate - 重新生成 Webhook 密钥
					r.Post("/regenerate", h.RegenerateWebhookKey)
				})

				// 版本管理路由组
				r.Route("/versions", func(r chi.Router) {
					// POST /api/v1/functions/{id}/versions - 发布新版本
					r.Post("/", h.PublishVersion)
					// GET /api/v1/functions/{id}/versions - 获取函数版本列表
					r.Get("/", h.ListFunctionVersions)
					// GET /api/v1/functions/{id}/versions/{version} - 获取指定版本
					r.Get("/{version}", h.GetFunctionVersion)
					// POST /api/v1/functions/{id}/versions/{version}/rollback - 回滚到指定版本
					r.Post("/{version}/rollback", h.RollbackFunction)
				})

				// 别名管理路由组
				r.Route("/aliases", func(r chi.Router) {
					// GET /api/v1/functions/{id}/aliases - 获取函数别名列表
					r.Get("/", h.ListFunctionAliases)
					// POST /api/v1/functions/{id}/aliases - 创建函数别名
					r.Post("/", h.CreateFunctionAlias)
					// PUT /api/v1/functions/{id}/aliases/{name} - 更新函数别名
					r.Put("/{name}", h.UpdateFunctionAlias)
					// DELETE /api/v1/functions/{id}/aliases/{name} - 删除函数别名
					r.Delete("/{name}", h.DeleteFunctionAlias)
				})

				// 层管理路由组（函数级别）
				r.Route("/layers", func(r chi.Router) {
					// GET /api/v1/functions/{id}/layers - 获取函数的层
					r.Get("/", h.GetFunctionLayers)
					// PUT /api/v1/functions/{id}/layers - 设置函数的层
					r.Put("/", h.SetFunctionLayers)
				})

				// 环境配置路由组
				r.Route("/environments", func(r chi.Router) {
					// GET /api/v1/functions/{id}/environments - 获取函数环境配置列表
					r.Get("/", h.GetFunctionEnvConfigs)
					// PUT /api/v1/functions/{id}/environments/{env} - 更新函数环境配置
					r.Put("/{env}", h.UpdateFunctionEnvConfig)
				})

				// 预热管理路由组
				r.Route("/warming", func(r chi.Router) {
					// GET /api/v1/functions/{id}/warming - 获取预热状态
					r.Get("/", h.GetWarmingStatus)
					// PUT /api/v1/functions/{id}/warming - 更新预热策略
					r.Put("/", h.UpdateWarmingPolicy)
				})
				// POST /api/v1/functions/{id}/warm - 触发预热
				r.Post("/warm", h.TriggerWarming)

				// GET /api/v1/functions/{id}/dependencies - 获取函数依赖关系
				r.Get("/dependencies", h.GetFunctionDependencies)
				// GET /api/v1/functions/{id}/impact - 获取影响分析
				r.Get("/impact", h.GetImpactAnalysis)
			})
		})

		// 调用记录路由组
		r.Route("/invocations", func(r chi.Router) {
			// GET /api/v1/invocations - 获取所有调用记录列表
			r.Get("/", h.ListAllInvocations)
			// GET /api/v1/invocations/{id} - 获取调用记录详情
			r.Get("/{id}", h.GetInvocation)
			// POST /api/v1/invocations/{id}/replay - 重放调用
			r.Post("/{id}/replay", h.ReplayInvocation)
		})

		// GET /api/v1/stats - 获取系统统计信息
		r.Get("/stats", h.Stats)

		// POST /api/v1/compile - 编译源代码
		r.Post("/compile", h.CompileCode)

		// 任务管理路由组
		r.Route("/tasks", func(r chi.Router) {
			// GET /api/v1/tasks/{id} - 获取任务状态
			r.Get("/{id}", h.GetFunctionTask)
		})

		// 层管理路由组
		r.Route("/layers", func(r chi.Router) {
			// GET /api/v1/layers - 获取层列表
			r.Get("/", h.ListLayers)
			// POST /api/v1/layers - 创建层
			r.Post("/", h.CreateLayer)
			// GET /api/v1/layers/{id} - 获取层详情
			r.Get("/{id}", h.GetLayer)
			// DELETE /api/v1/layers/{id} - 删除层
			r.Delete("/{id}", h.DeleteLayer)
			// POST /api/v1/layers/{id}/versions - 创建层版本
			r.Post("/{id}/versions", h.CreateLayerVersion)
		})

		// 环境管理路由组
		r.Route("/environments", func(r chi.Router) {
			// GET /api/v1/environments - 获取环境列表
			r.Get("/", h.ListEnvironments)
			// POST /api/v1/environments - 创建环境
			r.Post("/", h.CreateEnvironment)
			// DELETE /api/v1/environments/{id} - 删除环境
			r.Delete("/{id}", h.DeleteEnvironment)
		})

		// 死信队列 (DLQ) 管理路由组
		r.Route("/dlq", func(r chi.Router) {
			// GET /api/v1/dlq - 获取死信消息列表
			r.Get("/", h.ListDLQMessages)
			// GET /api/v1/dlq/stats - 获取死信队列统计
			r.Get("/stats", h.GetDLQStats)
			// DELETE /api/v1/dlq - 清空死信队列
			r.Delete("/", h.PurgeDLQMessages)
			// GET /api/v1/dlq/{id} - 获取死信消息详情
			r.Get("/{id}", h.GetDLQMessage)
			// POST /api/v1/dlq/{id}/retry - 重试死信消息
			r.Post("/{id}/retry", h.RetryDLQMessage)
			// POST /api/v1/dlq/{id}/discard - 丢弃死信消息
			r.Post("/{id}/discard", h.DiscardDLQMessage)
			// DELETE /api/v1/dlq/{id} - 删除死信消息
			r.Delete("/{id}", h.DeleteDLQMessage)
		})

		// 系统设置管理路由组
		r.Route("/settings", func(r chi.Router) {
			// GET /api/v1/settings - 获取所有系统设置
			r.Get("/", h.ListSystemSettings)
			// GET /api/v1/settings/{key} - 获取单个设置
			r.Get("/{key}", h.GetSystemSetting)
			// PUT /api/v1/settings/{key} - 更新设置
			r.Put("/{key}", h.UpdateSystemSetting)
		})

		// 保留策略管理路由组
		r.Route("/retention", func(r chi.Router) {
			// GET /api/v1/retention/stats - 获取保留策略统计
			r.Get("/stats", h.GetRetentionStats)
			// POST /api/v1/retention/cleanup - 执行清理
			r.Post("/cleanup", h.RunRetentionCleanup)
		})

		// 审计日志管理路由组
		r.Route("/audit", func(r chi.Router) {
			// GET /api/v1/audit - 获取审计日志列表
			r.Get("/", h.ListAuditLogs)
			// GET /api/v1/audit/actions - 获取操作类型列表
			r.Get("/actions", h.GetAuditLogActions)
		})

		// 模板管理路由组
		r.Route("/templates", func(r chi.Router) {
			// GET /api/v1/templates - 获取模板列表
			r.Get("/", h.ListTemplates)
			// POST /api/v1/templates - 创建模板
			r.Post("/", h.CreateTemplate)

			r.Route("/{id}", func(r chi.Router) {
				// GET /api/v1/templates/{id} - 获取模板详情
				r.Get("/", h.GetTemplate)
				// PUT /api/v1/templates/{id} - 更新模板
				r.Put("/", h.UpdateTemplate)
				// DELETE /api/v1/templates/{id} - 删除模板
				r.Delete("/", h.DeleteTemplate)
			})
		})

		// 配额管理路由
		// GET /api/v1/quota - 获取配额使用情况
		r.Get("/quota", h.GetQuotaUsage)

		// 告警管理路由组
		r.Route("/alerts", func(r chi.Router) {
			// GET /api/v1/alerts - 获取告警列表
			r.Get("/", h.ListAlerts)
			// POST /api/v1/alerts/{id}/resolve - 解决告警
			r.Post("/{id}/resolve", h.ResolveAlert)

			// 告警规则
			r.Route("/rules", func(r chi.Router) {
				// GET /api/v1/alerts/rules - 获取告警规则列表
				r.Get("/", h.ListAlertRules)
				// POST /api/v1/alerts/rules - 创建告警规则
				r.Post("/", h.CreateAlertRule)
				// GET /api/v1/alerts/rules/{id} - 获取告警规则详情
				r.Get("/{id}", h.GetAlertRule)
				// PUT /api/v1/alerts/rules/{id} - 更新告警规则
				r.Put("/{id}", h.UpdateAlertRule)
				// DELETE /api/v1/alerts/rules/{id} - 删除告警规则
				r.Delete("/{id}", h.DeleteAlertRule)
			})

			// 通知渠道
			r.Route("/channels", func(r chi.Router) {
				// GET /api/v1/alerts/channels - 获取通知渠道列表
				r.Get("/", h.ListNotificationChannels)
				// POST /api/v1/alerts/channels - 创建通知渠道
				r.Post("/", h.CreateNotificationChannel)
				// DELETE /api/v1/alerts/channels/{id} - 删除通知渠道
				r.Delete("/{id}", h.DeleteNotificationChannel)
			})
		})

		// 依赖分析路由组
		r.Route("/dependencies", func(r chi.Router) {
			// GET /api/v1/dependencies/graph - 获取依赖关系图
			r.Get("/graph", h.GetDependencyGraph)
		})

		// 快照管理路由组
		if cfg.SnapshotHandler != nil {
			cfg.SnapshotHandler.RegisterRoutes(r)
		}

		// 状态管理路由组（有状态函数）
		if cfg.StateHandler != nil {
			cfg.StateHandler.RegisterRoutes(r)
		}

		// 工作流管理路由组
		if cfg.WorkflowHandler != nil {
			wh := cfg.WorkflowHandler
			r.Route("/workflows", func(r chi.Router) {
				// POST /api/v1/workflows - 创建工作流
				r.Post("/", wh.CreateWorkflow)
				// GET /api/v1/workflows - 获取工作流列表
				r.Get("/", wh.ListWorkflows)

				r.Route("/{id}", func(r chi.Router) {
					// GET /api/v1/workflows/{id} - 获取工作流详情
					r.Get("/", wh.GetWorkflow)
					// PUT /api/v1/workflows/{id} - 更新工作流
					r.Put("/", wh.UpdateWorkflow)
					// DELETE /api/v1/workflows/{id} - 删除工作流
					r.Delete("/", wh.DeleteWorkflow)
					// POST /api/v1/workflows/{id}/executions - 启动执行
					r.Post("/executions", wh.StartExecution)
					// GET /api/v1/workflows/{id}/executions - 获取执行列表
					r.Get("/executions", wh.ListExecutions)
				})
			})

			r.Route("/executions", func(r chi.Router) {
				// GET /api/v1/executions - 获取所有执行列表
				r.Get("/", wh.ListAllExecutions)

				r.Route("/{id}", func(r chi.Router) {
					// GET /api/v1/executions/{id} - 获取执行详情
					r.Get("/", wh.GetExecution)
					// POST /api/v1/executions/{id}/stop - 停止执行
					r.Post("/stop", wh.StopExecution)
					// GET /api/v1/executions/{id}/history - 获取执行历史
					r.Get("/history", wh.GetExecutionHistory)
					// POST /api/v1/executions/{id}/resume - 恢复暂停的执行
					r.Post("/resume", wh.ResumeExecution)

					// 断点管理
					r.Route("/breakpoints", func(r chi.Router) {
						// POST /api/v1/executions/{id}/breakpoints - 设置断点
						r.Post("/", wh.SetBreakpoint)
						// GET /api/v1/executions/{id}/breakpoints - 列出断点
						r.Get("/", wh.ListBreakpoints)
						// DELETE /api/v1/executions/{id}/breakpoints/{state} - 删除断点
						r.Delete("/{state}", wh.DeleteBreakpoint)
					})
				})
			})
		}
	})

	// Web 控制台 API 路由组
	// 提供仪表板、函数测试、实时日志等功能的API
	if cfg.Logger != nil {
		consoleHandler := NewConsoleHandler(h, h.store, cfg.Logger)
		debugHandler := NewDebugHandler(h.store, cfg.Logger)
		r.Route("/api", func(r chi.Router) {
			consoleHandler.RegisterRoutes(r)
			debugHandler.RegisterRoutes(r)
		})
	}

	// 前端静态文件服务
	// 如果配置了WebFS，则提供SPA前端的静态文件服务
	if cfg.WebFS != nil {
		// 静态文件服务器
		fileServer := http.FileServer(http.FS(cfg.WebFS))

		// 处理所有未匹配的路由，返回前端应用
		r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
			// 尝试直接提供文件
			path := r.URL.Path
			if path == "/" {
				path = "/index.html"
			}

			// 检查文件是否存在
			if _, err := fs.Stat(cfg.WebFS, path[1:]); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}

			// 文件不存在，返回index.html以支持SPA路由
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
		})
	}

	// 注册 NotFound 处理器，用于匹配自定义函数路由
	r.NotFound(h.HandleCustomRoute)

	return r
}

// corsMiddleware 是处理跨域资源共享(CORS)的中间件。
//
// 功能说明：
//   - 设置允许所有来源的跨域请求（Access-Control-Allow-Origin: *）
//   - 允许的HTTP方法：GET, POST, PUT, DELETE, OPTIONS
//   - 允许的请求头：Content-Type, Authorization
//   - 处理预检请求（OPTIONS方法）
//
// 参数：
//   - next: 下一个HTTP处理器
//
// 返回值：
//   - http.Handler: 包装了CORS逻辑的HTTP处理器
//
// 安全提示：
//
//	生产环境中应考虑限制Access-Control-Allow-Origin为特定域名
//	而不是使用通配符"*"，以提高安全性
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 设置CORS响应头
		// 允许所有来源访问（生产环境建议设置为特定域名）
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// 允许的HTTP方法
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")

		// 允许的请求头
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		// 处理预检请求（OPTIONS方法）
		// 浏览器在发送跨域请求前会先发送OPTIONS请求来检查服务器是否允许
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// 继续处理实际请求
		next.ServeHTTP(w, r)
	})
}
