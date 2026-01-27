//go:build linux
// +build linux

// Package main 是函数计算网关服务的入口点
// 网关服务是整个函数计算平台的核心组件，负责接收和处理函数调用请求
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oriys/nimbus/internal/api"
	"github.com/oriys/nimbus/internal/config"
	"github.com/oriys/nimbus/internal/docker"
	"github.com/oriys/nimbus/internal/firecracker"
	"github.com/oriys/nimbus/internal/metrics"
	"github.com/oriys/nimbus/internal/scheduler"
	"github.com/oriys/nimbus/internal/storage"
	"github.com/oriys/nimbus/internal/telemetry"
	"github.com/oriys/nimbus/internal/vmpool"
	"github.com/oriys/nimbus/internal/workflow"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// main 是网关服务的主函数
// 它负责初始化所有依赖组件并启动 HTTP 服务器
func main() {
	// 解析命令行参数，获取配置文件路径
	// 默认配置文件路径为 /etc/nimbus/config.yaml
	configPath := flag.String("config", "/etc/nimbus/config.yaml", "Path to config file")
	flag.Parse()

	// 设置日志记录器
	// 使用 JSON 格式输出日志，便于日志收集和分析
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})
	logger.SetLevel(logrus.InfoLevel)

	// 加载配置文件
	// 配置文件包含数据库连接、服务端口、运行时模式等设置
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.WithError(err).Fatal("Failed to load config")
	}

	// 根据配置设置日志级别
	// 支持 debug 模式以获取更详细的日志输出
	if cfg.Logging.Level == "debug" {
		logger.SetLevel(logrus.DebugLevel)
	}

	logger.WithField("mode", cfg.Runtime.Mode).Info("Starting Nimbus Gateway")

	// 初始化遥测系统 (OpenTelemetry)
	// 遥测系统用于收集分布式追踪和指标数据
	var tel *telemetry.Telemetry
	if cfg.Telemetry.Enabled {
		telCfg := telemetry.Config{
			Enabled:     cfg.Telemetry.Enabled,     // 是否启用遥测
			Endpoint:    cfg.Telemetry.Endpoint,    // 遥测数据发送端点（如 Jaeger/Tempo）
			ServiceName: cfg.Telemetry.ServiceName, // 服务名称，用于标识追踪数据来源
			SampleRate:  cfg.Telemetry.SampleRate,  // 采样率，控制追踪数据量
			Environment: cfg.Telemetry.Environment, // 环境标识（dev/staging/prod）
		}
		var err error
		tel, err = telemetry.New(context.Background(), telCfg)
		if err != nil {
			// 遥测初始化失败不影响主服务运行，仅记录警告
			logger.WithError(err).Warn("Failed to initialize telemetry, continuing without tracing")
		} else {
			// 确保在服务关闭时正确清理遥测资源
			defer tel.Shutdown(context.Background())
			// 将遥测钩子添加到日志记录器，自动关联日志和追踪
			logger.AddHook(telemetry.NewLogrusHook())
			logger.WithFields(logrus.Fields{
				"endpoint":    cfg.Telemetry.Endpoint,
				"sample_rate": cfg.Telemetry.SampleRate,
			}).Info("Telemetry initialized")
		}
	}

	// 初始化 PostgreSQL 存储
	// PostgreSQL 用于持久化存储函数定义、调用记录等核心数据
	pgStore, err := storage.NewPostgresStore(cfg.Storage.Postgres)
	if err != nil {
		logger.WithError(err).Fatal("Failed to connect to PostgreSQL")
	}
	defer pgStore.Close()

	// 初始化 Redis 存储
	// Redis 用于缓存、会话管理和分布式锁等场景
	redisStore, err := storage.NewRedisStore(cfg.Storage.Redis)
	if err != nil {
		logger.WithError(err).Fatal("Failed to connect to Redis")
	}
	defer redisStore.Close()

	// 初始化 Prometheus 指标收集器
	// 指标收集器用于记录系统运行状态和性能数据
	var m *metrics.Metrics
	var metricsCancel context.CancelFunc
	if cfg.Metrics.Enabled {
		namespace := cfg.Metrics.Namespace
		if namespace == "" {
			namespace = "nimbus" // 默认指标命名空间
		}
		m = metrics.NewMetrics(namespace)

		// 创建用于取消指标更新协程的上下文
		ctx, cancel := context.WithCancel(context.Background())
		metricsCancel = cancel

		// 定义更新函数计数指标的函数
		// 定期从数据库获取函数统计信息
		updateFnCounts := func() {
			total, err := pgStore.CountFunctions()
			if err == nil {
				m.FunctionsTotal.Set(float64(total)) // 函数总数
			}
			active, err := pgStore.CountActiveFunctions()
			if err == nil {
				m.ActiveFunctions.Set(float64(active)) // 活跃函数数
			}
		}
		// 立即执行一次更新
		updateFnCounts()

		// 启动后台协程，每 5 秒更新一次函数计数指标
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					updateFnCounts()
				}
			}
		}()
	}

	// 初始化调度器和运行时管理器
	// 根据配置选择使用 Docker 模式或 Firecracker 模式
	var sched api.Scheduler
	var dockerMgr *docker.Manager

	if cfg.Runtime.Mode == "docker" {
		// Docker 模式 - 设置更简单，不需要 KVM 支持
		// 适用于开发环境和不支持 KVM 的平台
		dockerMgr = docker.NewManager(cfg.Docker, m, logger)
		sched = scheduler.NewDockerScheduler(cfg.Scheduler, pgStore, redisStore, dockerMgr, m, logger)
		logger.Info("Using Docker runtime mode")
	} else {
		// Firecracker 模式 - 需要 KVM 支持
		// 提供更好的隔离性和安全性，适用于生产环境

		// 初始化网络管理器，负责为虚拟机分配和管理网络
		networkMgr, err := firecracker.NewNetworkManager(cfg.Network, logger)
		if err != nil {
			logger.WithError(err).Fatal("Failed to initialize network manager")
		}
		defer networkMgr.Shutdown()

		// 初始化 Firecracker 虚拟机管理器
		machinesMgr := firecracker.NewMachineManager(cfg.Firecracker, networkMgr, logger)
		defer machinesMgr.Shutdown(context.Background())

		// 初始化虚拟机池
		// 预热的虚拟机池可以显著降低函数冷启动时间
		pool := vmpool.NewPool(cfg.Pool, machinesMgr, redisStore, m, logger)
		if err := pool.Start(); err != nil {
			logger.WithError(err).Fatal("Failed to start VM pool")
		}
		defer pool.Stop()

		// 创建基于 Firecracker 的调度器
		sched = scheduler.NewScheduler(cfg.Scheduler, pgStore, redisStore, pool, m, logger)
		logger.Info("Using Firecracker runtime mode")
	}

	// 启动调度器
	// 调度器负责管理函数执行任务的分发和执行
	if starter, ok := sched.(interface{ Start() error }); ok {
		if err := starter.Start(); err != nil {
			logger.WithError(err).Fatal("Failed to start scheduler")
		}
	}
	// 确保在服务关闭时停止调度器
	if stopper, ok := sched.(interface{ Stop() error }); ok {
		defer stopper.Stop()
	}

	// 初始化定时任务管理器
	// CronManager 负责处理函数的定时触发
	cronMgr := scheduler.NewCronManager(pgStore, sched.InvokeAsync, logger)
	if err := cronMgr.Start(); err != nil {
		logger.WithError(err).Error("Failed to start cron manager")
	}
	defer cronMgr.Stop()

	// 初始化工作流引擎
	var workflowEngine *workflow.Engine
	var workflowHandler *api.WorkflowHandler
	if cfg.Workflow.Enabled {
		workflowCfg := workflow.Config{
			Workers:          cfg.Workflow.Workers,
			QueueSize:        cfg.Workflow.QueueSize,
			DefaultTimeout:   cfg.Workflow.DefaultTimeout,
			RecoveryEnabled:  cfg.Workflow.RecoveryEnabled,
			RecoveryInterval: cfg.Workflow.RecoveryInterval,
		}
		workflowEngine = workflow.NewEngine(workflowCfg, pgStore, sched, logger)
		if err := workflowEngine.Start(); err != nil {
			logger.WithError(err).Error("Failed to start workflow engine")
		} else {
			defer workflowEngine.Stop()
			workflowHandler = api.NewWorkflowHandler(pgStore, workflowEngine, logger)
			logger.Info("Workflow engine started")
		}
	}

	// 初始化 API 处理器和路由
	// 处理器包含所有 API 端点的业务逻辑
	handler := api.NewHandler(pgStore, redisStore, sched, cronMgr, logger)

	// 恢复未完成的编译任务
	// 在服务重启时，检查并重新触发所有处于 creating/updating/building 状态的函数编译
	handler.RecoverPendingCompileTasks()

	router := api.NewRouter(&api.RouterConfig{
		Handler:         handler,
		WorkflowHandler: workflowHandler,
		Logger:          logger,
		WebFS:           nil, // 前端静态文件，可通过 embed 嵌入
	})

	// 如果指标端口与主服务端口不同，单独启动指标服务器
	// 这样可以将指标暴露在内部端口，避免公开暴露
	var metricsServer *http.Server
	if cfg.Metrics.Enabled && cfg.Server.MetricsPort != cfg.Server.HTTPPort {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler()) // Prometheus 指标端点
		metricsServer = &http.Server{
			Addr:         fmt.Sprintf(":%d", cfg.Server.MetricsPort),
			Handler:      mux,
			ReadTimeout:  10 * time.Second, // 读取超时
			WriteTimeout: 10 * time.Second, // 写入超时
			IdleTimeout:  60 * time.Second, // 空闲连接超时
		}
		go func() {
			logger.WithField("port", cfg.Server.MetricsPort).Info("Starting metrics server")
			if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.WithError(err).Fatal("Metrics server failed")
			}
		}()
	}

	// 配置并启动主 HTTP 服务器
	// 这是接收外部请求的主要入口
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.HTTPPort),
		Handler:      router,
		ReadTimeout:  30 * time.Second,  // 读取请求超时
		WriteTimeout: 60 * time.Second,  // 写入响应超时（函数执行可能较长）
		IdleTimeout:  120 * time.Second, // 空闲连接超时
	}

	// 在后台协程中启动 HTTP 服务器
	go func() {
		logger.WithField("port", cfg.Server.HTTPPort).Info("Starting HTTP server")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.WithError(err).Fatal("HTTP server failed")
		}
	}()

	// 等待关闭信号
	// 监听 SIGINT (Ctrl+C) 和 SIGTERM (容器停止) 信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	// 创建带超时的上下文用于优雅关闭
	// 确保在超时时间内完成所有清理工作
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	// 优雅关闭 HTTP 服务器
	// 等待现有请求处理完成
	if err := server.Shutdown(ctx); err != nil {
		logger.WithError(err).Error("Server shutdown error")
	}

	// 清理 Docker 资源（如果使用 Docker 模式）
	if dockerMgr != nil {
		if err := dockerMgr.Cleanup(ctx); err != nil {
			logger.WithError(err).Error("Docker manager cleanup error")
		}
	}

	// 停止指标更新协程
	if metricsCancel != nil {
		metricsCancel()
	}

	// 关闭指标服务器
	if metricsServer != nil {
		if err := metricsServer.Shutdown(ctx); err != nil {
			logger.WithError(err).Error("Metrics server shutdown error")
		}
	}

	logger.Info("Server stopped")
}
