//go:build !linux
// +build !linux

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
	"github.com/oriys/nimbus/internal/metrics"
	"github.com/oriys/nimbus/internal/scheduler"
	"github.com/oriys/nimbus/internal/storage"
	"github.com/oriys/nimbus/internal/workflow"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

func main() {
	configPath := flag.String("config", "/etc/nimbus/config.yaml", "Path to config file")
	flag.Parse()

	// Setup logger
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})
	logger.SetLevel(logrus.InfoLevel)

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.WithError(err).Fatal("Failed to load config")
	}

	if cfg.Logging.Level == "debug" {
		logger.SetLevel(logrus.DebugLevel)
	}

	if cfg.Runtime.Mode != "docker" {
		logger.WithField("mode", cfg.Runtime.Mode).Fatal("Firecracker mode is only supported on Linux; set runtime.mode=docker")
	}

	logger.WithField("mode", cfg.Runtime.Mode).Info("Starting Nimbus Gateway")

	// Initialize storage
	pgStore, err := storage.NewPostgresStore(cfg.Storage.Postgres)
	if err != nil {
		logger.WithError(err).Fatal("Failed to connect to PostgreSQL")
	}
	defer pgStore.Close()

	redisStore, err := storage.NewRedisStore(cfg.Storage.Redis)
	if err != nil {
		logger.WithError(err).Fatal("Failed to connect to Redis")
	}
	defer redisStore.Close()

	var m *metrics.Metrics
	var metricsCancel context.CancelFunc
	if cfg.Metrics.Enabled {
		namespace := cfg.Metrics.Namespace
		if namespace == "" {
			namespace = "nimbus"
		}
		m = metrics.NewMetrics(namespace)

		ctx, cancel := context.WithCancel(context.Background())
		metricsCancel = cancel

		updateFnCounts := func() {
			total, err := pgStore.CountFunctions()
			if err == nil {
				m.FunctionsTotal.Set(float64(total))
			}
			active, err := pgStore.CountActiveFunctions()
			if err == nil {
				m.ActiveFunctions.Set(float64(active))
			}
		}
		updateFnCounts()
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

	// Docker mode - simpler setup, no KVM required
	dockerMgr := docker.NewManager(cfg.Docker, m, logger)
	sched := scheduler.NewDockerScheduler(cfg.Scheduler, pgStore, redisStore, dockerMgr, m, logger)
	logger.Info("Using Docker runtime mode")

	// Start scheduler
	if err := sched.Start(); err != nil {
		logger.WithError(err).Fatal("Failed to start scheduler")
	}
	defer sched.Stop()

	// Initialize cron manager
	cronMgr := scheduler.NewCronManager(pgStore, sched.InvokeAsync, logger)
	if err := cronMgr.Start(); err != nil {
		logger.WithError(err).Error("Failed to start cron manager")
	}
	defer cronMgr.Stop()

	// Initialize workflow engine
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

	// Initialize API handler
	handler := api.NewHandler(pgStore, redisStore, sched, cronMgr, logger)

	// 恢复未完成的编译任务
	handler.RecoverPendingCompileTasks()

	// 加载默认函数模板
	api.SeedDefaultTemplates(pgStore, logger)

	router := api.NewRouter(&api.RouterConfig{
		Handler:         handler,
		WorkflowHandler: workflowHandler,
		Logger:          logger,
		WebFS:           nil, // 前端静态文件，可通过 embed 嵌入
	})

	var metricsServer *http.Server
	if cfg.Metrics.Enabled && cfg.Server.MetricsPort != cfg.Server.HTTPPort {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		metricsServer = &http.Server{
			Addr:         fmt.Sprintf(":%d", cfg.Server.MetricsPort),
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  60 * time.Second,
		}
		go func() {
			logger.WithField("port", cfg.Server.MetricsPort).Info("Starting metrics server")
			if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.WithError(err).Fatal("Metrics server failed")
			}
		}()
	}

	// Start HTTP server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.HTTPPort),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		logger.WithField("port", cfg.Server.HTTPPort).Info("Starting HTTP server")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.WithError(err).Fatal("HTTP server failed")
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.WithError(err).Error("Server shutdown error")
	}
	if err := dockerMgr.Cleanup(ctx); err != nil {
		logger.WithError(err).Error("Docker manager cleanup error")
	}
	if metricsCancel != nil {
		metricsCancel()
	}
	if metricsServer != nil {
		if err := metricsServer.Shutdown(ctx); err != nil {
			logger.WithError(err).Error("Metrics server shutdown error")
		}
	}

	logger.Info("Server stopped")
}
