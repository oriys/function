// Package api 提供 HTTP API 处理器。
// 本文件实现 Web 控制台专用的 API 端点。
package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/oriys/nimbus/internal/domain"
	"github.com/oriys/nimbus/internal/storage"
	"github.com/sirupsen/logrus"
)

// 全局日志广播器
var globalLogBroadcaster *LogBroadcaster
var globalLogStore *storage.PostgresStore
var globalLogLogger *logrus.Logger

// LogMessage 是日志流中推送的消息结构（与 domain.LogEntry 保持一致）。
type LogMessage = domain.LogEntry

// LogBroadcaster 日志广播器
type LogBroadcaster struct {
	subscribers   map[chan LogMessage]struct{}
	subscribersMu sync.RWMutex
}

// NewLogBroadcaster 创建日志广播器
func NewLogBroadcaster() *LogBroadcaster {
	return &LogBroadcaster{
		subscribers: make(map[chan LogMessage]struct{}),
	}
}

// Subscribe 订阅日志
func (b *LogBroadcaster) Subscribe(ch chan LogMessage) {
	b.subscribersMu.Lock()
	b.subscribers[ch] = struct{}{}
	b.subscribersMu.Unlock()
}

// Unsubscribe 取消订阅
func (b *LogBroadcaster) Unsubscribe(ch chan LogMessage) {
	b.subscribersMu.Lock()
	delete(b.subscribers, ch)
	b.subscribersMu.Unlock()
}

// Broadcast 广播日志
func (b *LogBroadcaster) Broadcast(log LogMessage) {
	b.subscribersMu.RLock()
	defer b.subscribersMu.RUnlock()

	for ch := range b.subscribers {
		select {
		case ch <- log:
		default:
			// 通道满了，丢弃日志
		}
	}
}

// BroadcastLog 全局广播日志函数
func BroadcastLog(log LogMessage) {
	// 先落库（采集），再推送（广播）
	if globalLogStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := globalLogStore.CreateLogEntry(ctx, &log); err != nil {
			if globalLogLogger != nil {
				globalLogLogger.WithError(err).Warn("Failed to persist log entry")
			}
			return
		}
	}

	if globalLogBroadcaster != nil {
		globalLogBroadcaster.Broadcast(log)
	}
}

// ConsoleHandler 处理 Web 控制台相关的 API 请求
type ConsoleHandler struct {
	handler *Handler
	store   *storage.PostgresStore
	logger  *logrus.Logger

	// WebSocket 升级器
	upgrader websocket.Upgrader

	// 日志广播
	logSubscribers   map[chan LogMessage]struct{}
	logSubscribersMu sync.RWMutex
}

// NewConsoleHandler 创建控制台处理器
func NewConsoleHandler(h *Handler, store *storage.PostgresStore, logger *logrus.Logger) *ConsoleHandler {
	// 初始化全局日志广播器
	if globalLogBroadcaster == nil {
		globalLogBroadcaster = NewLogBroadcaster()
	}
	// 初始化全局日志存储（用于“先落库再推送”）
	if globalLogStore == nil {
		globalLogStore = store
	}
	if globalLogLogger == nil {
		globalLogLogger = logger
	}

	return &ConsoleHandler{
		handler: h,
		store:   store,
		logger:  logger,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true // 开发环境允许所有来源
			},
		},
		logSubscribers: make(map[chan LogMessage]struct{}),
	}
}

// RegisterRoutes 注册控制台路由
func (c *ConsoleHandler) RegisterRoutes(r chi.Router) {
	r.Route("/console", func(r chi.Router) {
		// 仪表板 API
		r.Get("/dashboard/stats", c.GetDashboardStats)
		r.Get("/dashboard/trends", c.GetInvocationTrends)
		r.Get("/dashboard/top-functions", c.GetTopFunctions)
		r.Get("/dashboard/recent-invocations", c.GetRecentInvocations)

		// 系统状态
		r.Get("/system/status", c.GetSystemStatus)

		// 函数测试
		r.Post("/functions/{id}/test", c.TestFunction)

		// 函数分析
		r.Get("/functions/{id}/stats", c.GetFunctionStats)
		r.Get("/functions/{id}/trends", c.GetFunctionTrends)
		r.Get("/functions/{id}/latency-distribution", c.GetFunctionLatencyDistribution)

		// 实时日志 WebSocket
		r.Get("/logs", c.ListLogs)
		r.Get("/logs/stream", c.LogStream)

		// 实时指标 WebSocket
		r.Get("/metrics/stream", c.MetricsStream)

		// API Key 管理
		r.Get("/apikeys", c.ListAPIKeys)
		r.Post("/apikeys", c.CreateAPIKey)
		r.Delete("/apikeys/{id}", c.DeleteAPIKey)
	})
}

// ListLogs 查询已落库的日志记录（用于页面刷新后的“历史日志”回放）。
//
// Query 参数：
//   - limit: 返回数量（默认 200，最大 1000）
//   - offset: 偏移量（默认 0）
//   - function_id / function_name / request_id / level: 过滤条件（可选）
//   - before / after: RFC3339 或 RFC3339Nano 时间戳（可选）
func (c *ConsoleHandler) ListLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}

	parseTime := func(v string) (*time.Time, error) {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, nil
		}
		if ts, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return &ts, nil
		}
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, err
		}
		return &ts, nil
	}

	before, err := parseTime(q.Get("before"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid 'before' timestamp")
		return
	}
	after, err := parseTime(q.Get("after"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid 'after' timestamp")
		return
	}

	entries, err := c.store.ListLogEntries(r.Context(), storage.ListLogEntriesOptions{
		FunctionID:   strings.TrimSpace(q.Get("function_id")),
		FunctionName: strings.TrimSpace(q.Get("function_name")),
		RequestID:    strings.TrimSpace(q.Get("request_id")),
		Level:        strings.TrimSpace(q.Get("level")),
		Before:       before,
		After:        after,
		Limit:        limit,
		Offset:       offset,
	})
	if err != nil {
		c.logger.WithError(err).Error("Failed to list log entries")
		writeError(w, http.StatusInternalServerError, "failed to list logs")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": entries,
	})
}

// DashboardStats 仪表板统计数据
type DashboardStats struct {
	TotalInvocations  int64   `json:"total_invocations"`
	SuccessRate       float64 `json:"success_rate"`
	P99LatencyMs      float64 `json:"p99_latency_ms"`
	ColdStartRate     float64 `json:"cold_start_rate"`
	TotalFunctions    int     `json:"total_functions"`
	ActiveFunctions   int     `json:"active_functions"`
	InvocationsChange float64 `json:"invocations_change"`
	SuccessRateChange float64 `json:"success_rate_change"`
	LatencyChange     float64 `json:"latency_change"`
	ColdStartChange   float64 `json:"cold_start_change"`
}

// parsePeriodHours 解析时间段参数
func parsePeriodHours(period string) int {
	switch period {
	case "1h":
		return 1
	case "6h":
		return 6
	case "24h":
		return 24
	case "7d":
		return 168
	default:
		return 24
	}
}

// GetDashboardStats 获取仪表板统计数据
func (c *ConsoleHandler) GetDashboardStats(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	periodHours := parsePeriodHours(period)

	// 从数据库获取当前周期统计
	dbStats, _ := c.store.GetDashboardStats(periodHours)

	// 获取上一周期数据用于计算变化
	prevStats, _ := c.store.GetDashboardStats(periodHours * 2)

	stats := DashboardStats{
		TotalInvocations:  dbStats.TotalInvocations,
		SuccessRate:       dbStats.SuccessRate,
		P99LatencyMs:      dbStats.P99LatencyMs,
		ColdStartRate:     dbStats.ColdStartRate,
		TotalFunctions:    dbStats.TotalFunctions,
		ActiveFunctions:   dbStats.ActiveFunctions,
		InvocationsChange: 0,
		SuccessRateChange: 0,
		LatencyChange:     0,
		ColdStartChange:   0,
	}

	// 计算变化百分比
	if prevStats != nil && prevStats.TotalInvocations > dbStats.TotalInvocations {
		prevPeriodInv := prevStats.TotalInvocations - dbStats.TotalInvocations
		if prevPeriodInv > 0 {
			stats.InvocationsChange = float64(dbStats.TotalInvocations-prevPeriodInv) / float64(prevPeriodInv) * 100
		}
		stats.SuccessRateChange = dbStats.SuccessRate - prevStats.SuccessRate
		stats.LatencyChange = dbStats.P99LatencyMs - prevStats.P99LatencyMs
		stats.ColdStartChange = dbStats.ColdStartRate - prevStats.ColdStartRate
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// TrendDataPoint 趋势数据点
type TrendDataPoint struct {
	Timestamp    time.Time `json:"timestamp"`
	Invocations  int64     `json:"invocations"`
	Errors       int64     `json:"errors"`
	AvgLatencyMs float64   `json:"avg_latency_ms"`
}

// GetInvocationTrends 获取调用趋势数据
func (c *ConsoleHandler) GetInvocationTrends(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	periodHours := parsePeriodHours(period)

	// 从数据库获取真实数据
	data, err := c.store.GetInvocationTrends(periodHours, 1)
	if err != nil {
		c.logger.WithError(err).Error("Failed to get invocation trends")
		data = []storage.TrendDataPoint{}
	}

	// 转换为API响应格式
	trends := make([]TrendDataPoint, len(data))
	for i, d := range data {
		trends[i] = TrendDataPoint{
			Timestamp:    d.Timestamp,
			Invocations:  d.Invocations,
			Errors:       d.Errors,
			AvgLatencyMs: d.AvgLatencyMs,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data": trends,
	})
}

// TopFunction 热门函数
type TopFunction struct {
	FunctionID   string  `json:"function_id"`
	FunctionName string  `json:"function_name"`
	Invocations  int64   `json:"invocations"`
	Percentage   float64 `json:"percentage"`
}

// GetTopFunctions 获取热门函数
func (c *ConsoleHandler) GetTopFunctions(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	periodHours := parsePeriodHours(period)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 5
	}

	// 从数据库获取真实数据
	data, err := c.store.GetTopFunctions(periodHours, limit)
	if err != nil {
		c.logger.WithError(err).Error("Failed to get top functions")
		data = []storage.TopFunction{}
	}

	// 转换为API响应格式
	tops := make([]TopFunction, len(data))
	for i, d := range data {
		tops[i] = TopFunction{
			FunctionID:   d.FunctionID,
			FunctionName: d.FunctionName,
			Invocations:  d.Invocations,
			Percentage:   d.Percentage,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data": tops,
	})
}

// RecentInvocation 最近调用
type RecentInvocation struct {
	ID           string `json:"id"`
	FunctionID   string `json:"function_id"`
	FunctionName string `json:"function_name"`
	Status       string `json:"status"`
	DurationMs   int64  `json:"duration_ms"`
	ColdStart    bool   `json:"cold_start"`
	CreatedAt    string `json:"created_at"`
}

// GetRecentInvocations 获取最近调用
func (c *ConsoleHandler) GetRecentInvocations(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 10
	}

	// 从数据库获取真实数据
	data, err := c.store.GetRecentInvocations(limit)
	if err != nil {
		c.logger.WithError(err).Error("Failed to get recent invocations")
		data = []storage.RecentInvocation{}
	}

	// 转换为API响应格式
	invocations := make([]RecentInvocation, len(data))
	for i, d := range data {
		invocations[i] = RecentInvocation{
			ID:           d.ID,
			FunctionID:   d.FunctionID,
			FunctionName: d.FunctionName,
			Status:       d.Status,
			DurationMs:   d.DurationMs,
			ColdStart:    d.ColdStart,
			CreatedAt:    d.CreatedAt.Format(time.RFC3339),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data": invocations,
	})
}

// PoolStats 虚拟机池统计
type PoolStats struct {
	Runtime  string `json:"runtime"`
	WarmVMs  int    `json:"warm_vms"`
	BusyVMs  int    `json:"busy_vms"`
	TotalVMs int    `json:"total_vms"`
	MaxVMs   int    `json:"max_vms"`
}

// SystemStatusResponse 系统状态响应
type SystemStatusResponse struct {
	Status    string      `json:"status"`
	Version   string      `json:"version"`
	Uptime    string      `json:"uptime"`
	PoolStats []PoolStats `json:"pool_stats"`
}

// startTime 记录服务启动时间
var startTime = time.Now()

// GetSystemStatus 获取系统状态
func (c *ConsoleHandler) GetSystemStatus(w http.ResponseWriter, r *http.Request) {
	// 计算运行时间
	uptime := time.Since(startTime)
	uptimeStr := formatUptime(uptime)

	// 检查数据库连接以确定健康状态
	status := "healthy"
	if err := c.store.Ping(); err != nil {
		status = "degraded"
	}

	// 构建虚拟机池统计（示例数据，实际应从 VM 池管理器获取）
	poolStats := []PoolStats{
		{Runtime: "python3.11", WarmVMs: 2, BusyVMs: 1, TotalVMs: 3, MaxVMs: 10},
		{Runtime: "nodejs20", WarmVMs: 1, BusyVMs: 0, TotalVMs: 1, MaxVMs: 10},
		{Runtime: "go1.24", WarmVMs: 1, BusyVMs: 0, TotalVMs: 1, MaxVMs: 10},
	}

	response := SystemStatusResponse{
		Status:    status,
		Version:   "1.0.0",
		Uptime:    uptimeStr,
		PoolStats: poolStats,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// formatUptime 格式化运行时间
func formatUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%d天 %d小时 %d分钟", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%d小时 %d分钟", hours, minutes)
	}
	return fmt.Sprintf("%d分钟", minutes)
}

// TestFunction 测试函数（带详细日志）
func (c *ConsoleHandler) TestFunction(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "function id required", http.StatusBadRequest)
		return
	}

	// 解析请求体
	var payload json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		payload = json.RawMessage(`{}`)
	}

	// 调用函数（复用现有逻辑）
	c.handler.InvokeFunction(w, r)
}

// GetFunctionStats 获取函数统计数据
func (c *ConsoleHandler) GetFunctionStats(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "function id required", http.StatusBadRequest)
		return
	}

	period := r.URL.Query().Get("period")
	periodHours := parsePeriodHours(period)

	stats, err := c.store.GetFunctionStats(id, periodHours)
	if err != nil {
		c.logger.WithError(err).Error("Failed to get function stats")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// GetFunctionTrends 获取函数趋势数据
func (c *ConsoleHandler) GetFunctionTrends(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "function id required", http.StatusBadRequest)
		return
	}

	period := r.URL.Query().Get("period")
	periodHours := parsePeriodHours(period)

	trends, err := c.store.GetFunctionTrends(id, periodHours)
	if err != nil {
		c.logger.WithError(err).Error("Failed to get function trends")
		trends = []storage.TrendDataPoint{}
	}

	// 转换为API响应格式
	result := make([]TrendDataPoint, len(trends))
	for i, t := range trends {
		result[i] = TrendDataPoint{
			Timestamp:    t.Timestamp,
			Invocations:  t.Invocations,
			Errors:       t.Errors,
			AvgLatencyMs: t.AvgLatencyMs,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data": result,
	})
}

// LatencyDistribution 延迟分布
type LatencyDistribution struct {
	Bucket string `json:"bucket"`
	Count  int64  `json:"count"`
}

// GetFunctionLatencyDistribution 获取函数延迟分布
func (c *ConsoleHandler) GetFunctionLatencyDistribution(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "function id required", http.StatusBadRequest)
		return
	}

	period := r.URL.Query().Get("period")
	periodHours := parsePeriodHours(period)

	dist, err := c.store.GetFunctionLatencyDistribution(id, periodHours)
	if err != nil {
		c.logger.WithError(err).Error("Failed to get latency distribution")
		dist = []storage.LatencyDistribution{}
	}

	// 转换为API响应格式
	result := make([]LatencyDistribution, len(dist))
	for i, d := range dist {
		result[i] = LatencyDistribution{
			Bucket: d.Bucket,
			Count:  d.Count,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data": result,
	})
}

// LogStream 实时日志流 WebSocket
func (c *ConsoleHandler) LogStream(w http.ResponseWriter, r *http.Request) {
	// 获取可选的过滤参数
	filterFunctionID := r.URL.Query().Get("function_id")

	conn, err := c.upgrader.Upgrade(w, r, nil)
	if err != nil {
		c.logger.WithError(err).Error("WebSocket upgrade failed")
		return
	}
	defer conn.Close()

	// 创建订阅通道并订阅全局广播
	logChan := make(chan LogMessage, 100)
	if globalLogBroadcaster != nil {
		globalLogBroadcaster.Subscribe(logChan)
		defer globalLogBroadcaster.Unsubscribe(logChan)
	}

	// 监听客户端关闭
	done := make(chan struct{})
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				close(done)
				return
			}
		}
	}()

	// 发送日志
	for {
		select {
		case <-done:
			return
		case log := <-logChan:
			// 如果指定了过滤条件，则进行过滤
			if filterFunctionID != "" && log.FunctionID != filterFunctionID {
				continue
			}

			if err := conn.WriteJSON(log); err != nil {
				return
			}
		}
	}
}

// BroadcastLog 广播日志到所有订阅者
func (c *ConsoleHandler) BroadcastLog(log LogMessage) {
	c.logSubscribersMu.RLock()
	defer c.logSubscribersMu.RUnlock()

	for ch := range c.logSubscribers {
		select {
		case ch <- log:
		default:
			// 通道满了，丢弃日志
		}
	}
}

// MetricsStream 实时指标 WebSocket
func (c *ConsoleHandler) MetricsStream(w http.ResponseWriter, r *http.Request) {
	conn, err := c.upgrader.Upgrade(w, r, nil)
	if err != nil {
		c.logger.WithError(err).Error("WebSocket upgrade failed")
		return
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				close(done)
				return
			}
		}
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			metrics := map[string]interface{}{
				"timestamp":      time.Now().Format(time.RFC3339),
				"invocations_1m": 50 + time.Now().Unix()%20,
				"errors_1m":      time.Now().Unix() % 5,
				"avg_latency_ms": 30 + time.Now().Unix()%20,
				"active_vms":     10 + time.Now().Unix()%5,
				"warm_vms":       5 + time.Now().Unix()%3,
				"queue_size":     time.Now().Unix() % 10,
			}
			if err := conn.WriteJSON(metrics); err != nil {
				return
			}
		}
	}
}

// randomString 生成随机字符串
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
		time.Sleep(time.Nanosecond)
	}
	return string(b)
}

// ==================== API Key 管理 ====================

// ListAPIKeys 列出所有 API Key
func (c *ConsoleHandler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	// 使用默认用户（控制台无认证）
	userID := "console-user"

	keys, err := c.store.ListAPIKeysByUser(userID)
	if err != nil {
		c.logger.WithError(err).Error("Failed to list API keys")
		http.Error(w, "failed to list api keys", http.StatusInternalServerError)
		return
	}

	// 转换为响应格式
	result := make([]map[string]interface{}, len(keys))
	for i, key := range keys {
		result[i] = map[string]interface{}{
			"id":         key.ID,
			"name":       key.Name,
			"created_at": key.CreatedAt.Format(time.RFC3339),
		}
		if key.ExpiresAt != nil {
			result[i]["expires_at"] = key.ExpiresAt.Format(time.RFC3339)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"api_keys": result})
}

// CreateAPIKey 创建新的 API Key
func (c *ConsoleHandler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// 使用默认用户（控制台无认证）
	userID := "console-user"
	role := "user"

	// 生成 API Key
	key, hash, err := generateAPIKey()
	if err != nil {
		c.logger.WithError(err).Error("Failed to generate API key")
		http.Error(w, "failed to generate api key", http.StatusInternalServerError)
		return
	}

	// 生成唯一 ID
	id := randomUUID()

	// 保存到数据库
	if err := c.store.CreateAPIKey(id, req.Name, hash, userID, role); err != nil {
		c.logger.WithError(err).Error("Failed to create API key")
		http.Error(w, "failed to create api key", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      id,
		"name":    req.Name,
		"api_key": key, // 仅返回一次
	})
}

// DeleteAPIKey 删除 API Key
func (c *ConsoleHandler) DeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	// 使用默认用户（控制台无认证）
	userID := "console-user"

	if err := c.store.DeleteAPIKeyByUser(id, userID); err != nil {
		if err.Error() == "api key not found or not owned by user" {
			http.Error(w, "api key not found", http.StatusNotFound)
			return
		}
		c.logger.WithError(err).Error("Failed to delete API key")
		http.Error(w, "failed to delete api key", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// generateAPIKey 生成 API Key 和哈希值
func generateAPIKey() (string, string, error) {
	bytes := make([]byte, 32)
	for i := range bytes {
		bytes[i] = byte(time.Now().UnixNano() % 256)
		time.Sleep(time.Nanosecond)
	}
	key := "fn_" + hexEncode(bytes)
	hash := sha256Hash(key)
	return key, hash, nil
}

// hexEncode 十六进制编码
func hexEncode(b []byte) string {
	const hextable = "0123456789abcdef"
	dst := make([]byte, len(b)*2)
	for i, v := range b {
		dst[i*2] = hextable[v>>4]
		dst[i*2+1] = hextable[v&0x0f]
	}
	return string(dst)
}

// sha256Hash 计算 SHA256 哈希
func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hexEncode(h[:])
}

// randomUUID 生成 UUID
func randomUUID() string {
	bytes := make([]byte, 16)
	for i := range bytes {
		bytes[i] = byte(time.Now().UnixNano() % 256)
		time.Sleep(time.Nanosecond)
	}
	return hexEncode(bytes[:4]) + "-" + hexEncode(bytes[4:6]) + "-" + hexEncode(bytes[6:8]) + "-" + hexEncode(bytes[8:10]) + "-" + hexEncode(bytes[10:])
}
