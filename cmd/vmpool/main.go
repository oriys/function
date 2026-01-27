//go:build linux
// +build linux

// Package main 是虚拟机池服务的入口点
// 虚拟机池服务负责管理 Firecracker 轻量级虚拟机的生命周期
// 它维护一个预热的虚拟机池，以减少函数冷启动时间
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oriys/nimbus/internal/config"
	"github.com/oriys/nimbus/internal/firecracker"
	"github.com/oriys/nimbus/internal/storage"
	"github.com/oriys/nimbus/internal/vmpool"
	"github.com/sirupsen/logrus"
)

// main 是虚拟机池服务的主函数
// 它负责初始化 Firecracker 管理器、虚拟机池和状态监控服务器
func main() {
	// 初始化日志记录器
	// 使用 JSON 格式便于日志收集和分析
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})

	// 加载配置文件
	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		logger.WithError(err).Fatal("Failed to load configuration")
	}

	// 根据配置设置日志级别
	level, _ := logrus.ParseLevel(cfg.Logging.Level)
	logger.SetLevel(level)

	logger.Info("Starting VM pool service...")

	// 初始化 Redis
	// Redis 用于存储虚拟机状态和协调分布式操作
	redisStore, err := storage.NewRedisStore(cfg.Storage.Redis)
	if err != nil {
		logger.WithError(err).Fatal("Failed to connect to Redis")
	}
	defer redisStore.Close()

	// 初始化网络管理器
	// 网络管理器负责为每个虚拟机分配独立的网络接口
	// 使用 TAP 设备和网桥实现虚拟机网络隔离
	networkMgr, err := firecracker.NewNetworkManager(cfg.Network, logger)
	if err != nil {
		logger.WithError(err).Fatal("Failed to initialize network manager")
	}

	// 初始化 Firecracker 虚拟机管理器
	// 虚拟机管理器封装了 Firecracker API，提供虚拟机创建、启动、停止等操作
	machinesMgr := firecracker.NewMachineManager(cfg.Firecracker, networkMgr, logger)

	// 初始化虚拟机池
	// 虚拟机池维护多个预热的虚拟机，按运行时类型分组
	// 预热的虚拟机可以立即用于函数执行，显著降低冷启动延迟
	pool := vmpool.NewPool(cfg.Pool, machinesMgr, redisStore, nil, logger)

	// 启动虚拟机池
	// 池启动时会创建配置数量的预热虚拟机
	if err := pool.Start(); err != nil {
		logger.WithError(err).Fatal("Failed to start VM pool")
	}

	// 启动状态监控 HTTP 服务器
	// 提供健康检查和统计信息接口
	srv := startStatsServer(pool, logger)

	logger.Info("VM pool service started")

	// 等待关闭信号
	// 监听 SIGINT (Ctrl+C) 和 SIGTERM (容器停止) 信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down VM pool...")

	// 创建带超时的上下文用于优雅关闭
	// 确保在 30 秒内完成所有虚拟机的清理
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 关闭状态服务器
	srv.Shutdown(ctx)

	// 停止虚拟机池
	// 这会销毁所有虚拟机并释放资源
	pool.Stop()

	logger.Info("VM pool stopped")
}

// startStatsServer 启动状态监控 HTTP 服务器
// 提供以下端点：
//   - /health: 健康检查端点
//   - /stats: 虚拟机池统计信息
//
// 参数:
//   - pool: 虚拟机池实例
//   - logger: 日志记录器
//
// 返回:
//   - *http.Server: HTTP 服务器实例，用于后续关闭
func startStatsServer(pool *vmpool.Pool, logger *logrus.Logger) *http.Server {
	// 使用 chi 路由器
	r := chi.NewRouter()

	// 健康检查端点
	// Kubernetes 和负载均衡器使用此端点检测服务是否正常
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"healthy"}`))
	})

	// 统计信息端点
	// 返回各运行时的虚拟机池状态
	r.Get("/stats", func(w http.ResponseWriter, r *http.Request) {
		stats := pool.GetStats()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// 简单的 JSON 编码
		w.Write([]byte(`{"pool_stats":` + formatStats(stats) + `}`))
	})

	// 配置 HTTP 服务器
	srv := &http.Server{
		Addr:    ":8082", // 状态服务器监听端口
		Handler: r,
	}

	// 在后台协程中启动服务器
	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			logger.WithError(err).Error("Stats server error")
		}
	}()

	return srv
}

// formatStats 将虚拟机池统计信息格式化为 JSON 字符串。
//
// 注意: 此处使用手工字符串拼接而非 encoding/json 是因为本服务为轻量级状态端点，
// 输出格式固定且简单，手工拼接可避免反射开销。对于复杂或动态结构建议使用 json.Marshal。
//
// 参数:
//   - stats: 按运行时分组的池统计信息映射
//
// 返回:
//   - string: JSON 格式的统计信息，如 {"python3.11":{"warm":5,"busy":2,"total":7,"max":10}}
func formatStats(stats map[string]vmpool.PoolStats) string {
	// 手工拼接 JSON，格式固定无需通用序列化
	result := "{"
	first := true
	for runtime, s := range stats {
		if !first {
			result += ","
		}
		// 构建每个运行时的统计 JSON
		// warm: 预热的虚拟机数量
		// busy: 正在执行任务的虚拟机数量
		// total: 虚拟机总数
		// max: 该运行时的最大虚拟机数
		result += `"` + runtime + `":{"warm":` + itoa(s.WarmVMs) +
			`,"busy":` + itoa(s.BusyVMs) +
			`,"total":` + itoa(s.TotalVMs) +
			`,"max":` + itoa(s.MaxVMs) + `}`
		first = false
	}
	return result + "}"
}

// itoa 将非负整数转换为字符串。
//
// 注意: 此处使用手工实现而非 strconv.Itoa 是为了保持本文件零外部依赖（除必要的框架包），
// 作为轻量级辅助函数使用。仅支持非负整数，负数会返回空字符串。
// 生产环境中建议直接使用 strconv.Itoa 以获得更好的性能和完整性。
//
// 参数:
//   - i: 要转换的非负整数
//
// 返回:
//   - string: 整数的十进制字符串表示
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}
