//go:build linux
// +build linux

// Package main 是函数调度服务的入口点
// 调度服务负责管理函数执行任务的分发、调度和执行
// 在分布式部署中，调度器可以独立运行，与网关服务分离
package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/oriys/nimbus/internal/config"
	"github.com/oriys/nimbus/internal/scheduler"
	"github.com/oriys/nimbus/internal/storage"
	"github.com/oriys/nimbus/internal/vmpool"
	"github.com/sirupsen/logrus"
)

// main 是调度服务的主函数
// 它负责初始化存储连接、创建调度器并处理生命周期管理
func main() {
	// 初始化日志记录器
	// 使用 JSON 格式便于日志收集系统解析
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})

	// 加载配置文件
	// 配置文件包含数据库连接信息、调度参数等
	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		logger.WithError(err).Fatal("Failed to load configuration")
	}

	// 根据配置设置日志级别
	// 支持的级别：debug, info, warn, error
	level, _ := logrus.ParseLevel(cfg.Logging.Level)
	logger.SetLevel(level)

	logger.Info("Starting scheduler service...")

	// 初始化 PostgreSQL 存储
	// PostgreSQL 存储函数定义和调用历史记录
	// 调度器需要读取函数信息并记录调用状态
	pgStore, err := storage.NewPostgresStore(cfg.Storage.Postgres)
	if err != nil {
		logger.WithError(err).Fatal("Failed to connect to PostgreSQL")
	}
	defer pgStore.Close()

	// 初始化 Redis 存储
	// Redis 用于任务队列、状态缓存和分布式协调
	redisStore, err := storage.NewRedisStore(cfg.Storage.Redis)
	if err != nil {
		logger.WithError(err).Fatal("Failed to connect to Redis")
	}
	defer redisStore.Close()

	// 创建虚拟机池占位符
	// 在生产环境中，调度器通过 gRPC/HTTP 与 vmpool 服务通信
	// 这里创建一个占位符用于独立运行
	pool := createPoolPlaceholder(cfg, redisStore, logger)

	// 初始化调度器
	// 调度器负责：
	// 1. 从任务队列中获取待执行的函数调用请求
	// 2. 从虚拟机池中分配执行资源
	// 3. 监控执行状态并处理超时
	// 4. 记录执行结果
	sched := scheduler.NewScheduler(cfg.Scheduler, pgStore, redisStore, pool, nil, logger)

	// 启动调度器
	// 调度器会启动后台协程处理任务队列
	if err := sched.Start(); err != nil {
		logger.WithError(err).Fatal("Failed to start scheduler")
	}

	logger.Info("Scheduler service started successfully")

	// 等待关闭信号
	// 监听 SIGINT (Ctrl+C) 和 SIGTERM (容器停止) 信号
	// 收到信号后执行优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// 优雅关闭调度器
	// 等待当前正在执行的任务完成
	logger.Info("Shutting down scheduler...")
	if err := sched.Stop(); err != nil {
		logger.WithError(err).Error("Error stopping scheduler")
	}

	logger.Info("Scheduler stopped")
}

// createPoolPlaceholder 创建虚拟机池占位符
// 在实际的分布式部署中，调度器会通过 gRPC 或 HTTP 与 vmpool 服务通信
// 当前返回 nil 作为占位符，用于独立测试和开发
//
// 参数:
//   - cfg: 系统配置
//   - redis: Redis 存储实例
//   - logger: 日志记录器
//
// 返回:
//   - *vmpool.Pool: 虚拟机池实例（当前为 nil）
func createPoolPlaceholder(cfg *config.Config, redis *storage.RedisStore, logger *logrus.Logger) *vmpool.Pool {
	// 在真实部署中，调度器会通过 gRPC/HTTP 与 vmpool 服务通信
	// 目前创建本地池实例用于测试
	return nil
}
