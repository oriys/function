// Package api 提供了函数即服务(FaaS)平台的HTTP API处理程序。
// 该包实现了RESTful API接口，用于管理云函数的创建、查询、更新、删除和调用。
// 主要功能包括：
//   - 函数的CRUD操作（创建、读取、更新、删除）
//   - 同步和异步函数调用
//   - 调用记录的查询
//   - 健康检查和统计信息
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/oriys/nimbus/internal/compiler"
	"github.com/oriys/nimbus/internal/domain"
	"github.com/oriys/nimbus/internal/scheduler"
	"github.com/oriys/nimbus/internal/storage"
	"github.com/sirupsen/logrus"
)

// Handler 是API请求处理器的核心结构体。
// 它封装了数据存储层和调度器的依赖，负责处理所有HTTP请求。
//
// 字段说明：
//   - store: PostgreSQL数据库存储接口，用于持久化函数和调用记录
//   - redis: Redis存储接口，用于缓存和临时数据存储
//   - scheduler: 函数调度器接口，负责函数的实际执行调度
//   - compiler: 代码编译器，用于编译Go/Rust源代码
//   - cronManager: 定时任务管理器，负责管理函数的定时触发
//   - logger: 日志记录器，用于记录调试和错误信息
type Handler struct {
	store       *storage.PostgresStore
	redis       *storage.RedisStore
	scheduler   Scheduler
	compiler    *compiler.Compiler
	cronManager *scheduler.CronManager
	logger      *logrus.Logger
}

// Scheduler 定义了函数调度器的接口。
// 实现该接口的调度器负责管理函数的执行环境和调用流程。
//
// 方法说明：
//   - Invoke: 同步调用函数，等待执行完成并返回结果
//   - InvokeAsync: 异步调用函数，立即返回请求ID，后台执行
type Scheduler interface {
	// Invoke 同步调用函数
	// 参数 req: 调用请求，包含函数ID、载荷等信息
	// 返回值: 调用响应（包含状态码和响应体）和可能的错误
	Invoke(req *domain.InvokeRequest) (*domain.InvokeResponse, error)

	// InvokeAsync 异步调用函数
	// 参数 req: 调用请求，包含函数ID、载荷等信息
	// 返回值: 请求ID（用于后续查询执行结果）和可能的错误
	InvokeAsync(req *domain.InvokeRequest) (string, error)
}

// NewHandler 创建并返回一个新的Handler实例。
//
// 参数：
//   - store: PostgreSQL存储实例，用于数据持久化
//   - redis: Redis存储实例，用于缓存操作
//   - scheduler: 函数调度器实例，用于执行函数调用
//   - cronManager: 定时任务管理器实例
//   - logger: 日志记录器实例，用于记录调试信息
//
// 返回值：
//   - *Handler: 初始化完成的处理器实例
func NewHandler(store *storage.PostgresStore, redis *storage.RedisStore, scheduler Scheduler, cronManager *scheduler.CronManager, logger *logrus.Logger) *Handler {
	return &Handler{
		store:       store,
		redis:       redis,
		scheduler:   scheduler,
		compiler:    compiler.NewCompiler(),
		cronManager: cronManager,
		logger:      logger,
	}
}

// RecoverPendingCompileTasks 恢复未完成的编译任务
// 在服务启动时调用，检查并重新触发所有处于 creating/updating/building 状态的函数编译
func (h *Handler) RecoverPendingCompileTasks() {
	h.logger.Info("检查未完成的编译任务...")

	// 查询所有需要恢复的函数
	pendingFunctions, err := h.store.GetFunctionsByStatuses([]string{
		string(domain.FunctionStatusCreating),
		string(domain.FunctionStatusUpdating),
		string(domain.FunctionStatusBuilding),
	})
	if err != nil {
		h.logger.WithError(err).Error("查询未完成编译任务失败")
		return
	}

	if len(pendingFunctions) == 0 {
		h.logger.Info("没有需要恢复的编译任务")
		return
	}

	h.logger.WithField("count", len(pendingFunctions)).Info("发现未完成的编译任务，开始恢复")

	for _, fn := range pendingFunctions {
		// 检查是否需要编译
		if fn.Binary != "" || !compiler.IsSourceCode(string(fn.Runtime), fn.Code) {
			// 不需要编译，直接设为 active
			h.logger.WithField("function", fn.Name).Info("函数无需编译，直接激活")
			h.store.SetFunctionDeployed(fn.ID)
			continue
		}

		// 生成新的任务ID
		taskID := uuid.New().String()

		// 更新函数状态
		h.store.UpdateFunctionStatus(fn.ID, domain.FunctionStatusBuilding, "正在恢复编译任务", taskID)

		// 创建新的任务记录
		task := &domain.FunctionTask{
			ID:         taskID,
			FunctionID: fn.ID,
			Type:       domain.FunctionTaskCreate,
			Status:     domain.FunctionTaskPending,
		}
		h.store.CreateFunctionTask(task)

		// 异步执行编译
		go h.processCreateFunctionTask(fn.ID, taskID)

		h.logger.WithFields(logrus.Fields{
			"function": fn.Name,
			"task_id":  taskID,
		}).Info("已恢复编译任务")
	}
}

// CreateFunction 处理创建函数的请求。
// HTTP端点: POST /api/v1/functions
//
// 功能说明：
//   - 解析并验证请求体中的函数配置
//   - 检查函数名称是否已存在（防止重复）
//   - 计算代码的SHA256哈希值用于版本控制
//   - 异步创建函数，立即返回带有任务ID的响应
//
// 请求体格式: domain.CreateFunctionRequest (JSON)
// 响应格式: 成功返回202和函数信息（含任务ID）
func (h *Handler) CreateFunction(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetReqID(r.Context())
	h.logInfo(r, "CreateFunction", "开始创建函数", logrus.Fields{"request_id": requestID})

	// 解析请求体中的JSON数据
	var req domain.CreateFunctionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logError(r, "CreateFunction", "解析请求体失败", err, nil)
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	h.logDebug(r, "CreateFunction", "请求参数", logrus.Fields{
		"name":    req.Name,
		"runtime": req.Runtime,
		"handler": req.Handler,
	})

	// 验证请求参数的有效性
	if err := req.Validate(); err != nil {
		h.logError(r, "CreateFunction", "参数验证失败", err, logrus.Fields{"name": req.Name})
		writeErrorWithContext(w, r, http.StatusBadRequest, err.Error())
		return
	}

	// 检查是否存在同名函数，防止重复创建
	existing, _ := h.store.GetFunctionByName(req.Name)
	if existing != nil {
		h.logWarn(r, "CreateFunction", "函数名称已存在", logrus.Fields{"name": req.Name})
		writeErrorWithContext(w, r, http.StatusConflict, "function with this name already exists")
		return
	}

	// 计算代码的SHA256哈希值，用于版本控制和变更检测
	hash := sha256.Sum256([]byte(req.Code))
	codeHash := hex.EncodeToString(hash[:])

	// 生成任务ID
	taskID := uuid.New().String()

	// 构建函数对象，初始状态为 creating
	fn := &domain.Function{
		Name:           req.Name,
		Description:    req.Description,
		Tags:           req.Tags,
		Runtime:        req.Runtime,
		Handler:        req.Handler,
		Code:           req.Code,
		Binary:         req.Binary,
		CodeHash:       codeHash,
		MemoryMB:       req.MemoryMB,
		TimeoutSec:     req.TimeoutSec,
		MaxConcurrency: req.MaxConcurrency,
		EnvVars:        req.EnvVars,
		CronExpression: req.CronExpression,
		HTTPPath:       req.HTTPPath,
		HTTPMethods:    req.HTTPMethods,
		Status:         domain.FunctionStatusCreating,
		StatusMessage:  "函数正在创建中",
		TaskID:         taskID,
		Version:        1,
	}

	// 保存函数到数据库（状态为 creating）
	if err := h.store.CreateFunction(fn); err != nil {
		h.logError(r, "CreateFunction", "保存函数失败", err, logrus.Fields{"name": req.Name})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create function: "+err.Error())
		return
	}

	// 创建异步任务
	taskInput, _ := json.Marshal(req)
	task := &domain.FunctionTask{
		ID:         taskID,
		FunctionID: fn.ID,
		Type:       domain.FunctionTaskCreate,
		Status:     domain.FunctionTaskPending,
		Input:      taskInput,
	}
	if err := h.store.CreateFunctionTask(task); err != nil {
		h.logError(r, "CreateFunction", "创建任务失败", err, logrus.Fields{"name": req.Name})
		// 清理已创建的函数
		h.store.DeleteFunction(fn.ID)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create task: "+err.Error())
		return
	}

	// 异步处理编译任务
	go h.processCreateFunctionTask(fn.ID, taskID)

	h.logInfo(r, "CreateFunction", "函数已创建，编译任务已提交", logrus.Fields{"name": fn.Name, "id": fn.ID, "task_id": taskID})

	// 返回 200 OK，源代码已保存，编译在后台进行
	writeJSON(w, http.StatusOK, fn)
}

// processCreateFunctionTask 异步处理函数创建任务
// 流程：源代码已在 CreateFunction 中保存 → 编译 → 更新二进制和状态
func (h *Handler) processCreateFunctionTask(functionID, taskID string) {
	// 更新任务状态为 running
	now := time.Now()
	h.store.UpdateFunctionTask(&domain.FunctionTask{
		ID:        taskID,
		Status:    domain.FunctionTaskRunning,
		StartedAt: &now,
	})

	// 获取函数信息（源代码已保存）
	fn, err := h.store.GetFunctionByID(functionID)
	if err != nil {
		h.completeTaskWithError(taskID, functionID, "failed to get function: "+err.Error())
		return
	}

	h.logger.WithFields(logrus.Fields{
		"function_id": functionID,
		"task_id":     taskID,
		"runtime":     fn.Runtime,
	}).Info("开始编译函数，源代码已保存")

	// 处理编译型语言（Go/Rust/WASM）
	if fn.Binary == "" && compiler.IsSourceCode(string(fn.Runtime), fn.Code) {
		// 更新状态为 building
		h.store.UpdateFunctionStatus(functionID, domain.FunctionStatusBuilding, "正在编译源代码", taskID)

		// 执行编译（使用带超时的 context）
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		compileResp, err := h.compiler.Compile(ctx, &compiler.CompileRequest{
			Runtime: string(fn.Runtime),
			Code:    fn.Code,
		})
		if err != nil {
			h.completeTaskWithError(taskID, functionID, "compilation error: "+err.Error())
			return
		}
		if !compileResp.Success {
			h.completeTaskWithError(taskID, functionID, "compilation failed: "+compileResp.Error)
			return
		}

		// 编译成功，仅更新二进制字段
		if err := h.store.UpdateFunctionBinary(functionID, compileResp.Binary); err != nil {
			h.completeTaskWithError(taskID, functionID, "failed to save binary: "+err.Error())
			return
		}
		fn.Binary = compileResp.Binary

		h.logger.WithFields(logrus.Fields{
			"function_id": functionID,
			"task_id":     taskID,
		}).Info("编译完成，二进制已保存")
	}

	// 更新函数状态为 active
	if err := h.store.SetFunctionDeployed(functionID); err != nil {
		h.completeTaskWithError(taskID, functionID, "failed to update function status: "+err.Error())
		return
	}

	// 同步定时任务
	if h.cronManager != nil && fn.CronExpression != "" {
		// 重新获取最新的函数信息
		latestFn, _ := h.store.GetFunctionByID(functionID)
		if latestFn != nil {
			h.cronManager.AddOrUpdateFunction(latestFn)
		}
	}

	// 完成任务
	completedAt := time.Now()
	h.store.UpdateFunctionTask(&domain.FunctionTask{
		ID:          taskID,
		Status:      domain.FunctionTaskCompleted,
		CompletedAt: &completedAt,
	})

	h.logger.WithFields(logrus.Fields{
		"function_id": functionID,
		"task_id":     taskID,
	}).Info("函数创建任务完成")
}

// completeTaskWithError 将任务标记为失败
func (h *Handler) completeTaskWithError(taskID, functionID, errorMsg string) {
	completedAt := time.Now()
	h.store.UpdateFunctionTask(&domain.FunctionTask{
		ID:          taskID,
		Status:      domain.FunctionTaskFailed,
		Error:       errorMsg,
		CompletedAt: &completedAt,
	})
	h.store.UpdateFunctionStatus(functionID, domain.FunctionStatusFailed, errorMsg, "")

	h.logger.WithFields(logrus.Fields{
		"function_id": functionID,
		"task_id":     taskID,
		"error":       errorMsg,
	}).Error("函数任务失败")
}

// GetFunction 处理获取单个函数详情的请求。
// HTTP端点: GET /api/v1/functions/{id}
//
// 功能说明：
//   - 支持通过函数ID或函数名称进行查询
//   - 先尝试按ID查询，如果未找到则按名称查询
//
// 路径参数：
//   - id: 函数的唯一标识符或名称
//
// 返回值：
//   - 200: 成功，返回函数详情
//   - 404: 函数不存在
//   - 500: 服务器内部错误
func (h *Handler) GetFunction(w http.ResponseWriter, r *http.Request) {
	// 从URL路径中提取函数ID或名称
	idOrName := chi.URLParam(r, "id")
	if idOrName == "" {
		h.logError(r, "GetFunction", "缺少函数标识", nil, nil)
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id or name required")
		return
	}

	h.logDebug(r, "GetFunction", "查询函数", logrus.Fields{"function": idOrName})

	// 首先尝试按ID查询函数
	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		// 如果按ID未找到，尝试按名称查询
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		h.logWarn(r, "GetFunction", "函数不存在", logrus.Fields{"function": idOrName})
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found: "+idOrName)
		return
	}
	if err != nil {
		h.logError(r, "GetFunction", "查询函数失败", err, logrus.Fields{"function": idOrName})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	h.logDebug(r, "GetFunction", "查询成功", logrus.Fields{"function": fn.Name, "id": fn.ID})

	// 构建响应，包含代码大小信息
	response := map[string]interface{}{
		"id":              fn.ID,
		"name":            fn.Name,
		"description":     fn.Description,
		"tags":            fn.Tags,
		"pinned":          fn.Pinned,
		"runtime":         fn.Runtime,
		"handler":         fn.Handler,
		"code":            fn.Code,
		"binary":          fn.Binary,
		"code_hash":       fn.CodeHash,
		"memory_mb":       fn.MemoryMB,
		"timeout_sec":     fn.TimeoutSec,
		"max_concurrency": fn.MaxConcurrency,
		"env_vars":        fn.EnvVars,
		"status":          fn.Status,
		"status_message":  fn.StatusMessage,
		"task_id":         fn.TaskID,
		"version":         fn.Version,
		"cron_expression": fn.CronExpression,
		"http_path":       fn.HTTPPath,
		"http_methods":    fn.HTTPMethods,
		"webhook_enabled": fn.WebhookEnabled,
		"webhook_key":     fn.WebhookKey,
		"last_deployed_at": fn.LastDeployedAt,
		"created_at":      fn.CreatedAt,
		"updated_at":      fn.UpdatedAt,
		"code_size":       len(fn.Code),
		"code_size_limit": domain.MaxCodeSize,
	}
	writeJSON(w, http.StatusOK, response)
}

// ListFunctions 处理获取函数列表的请求。
// HTTP端点: GET /api/v1/functions
//
// 功能说明：
//   - 支持分页查询，通过offset和limit参数控制
//   - limit默认值为20，最大值为100
//
// 查询参数：
//   - offset: 偏移量，跳过前N条记录（默认0）
//   - limit: 每页数量，范围1-100（默认20）
//
// 返回值：
//   - functions: 函数列表
//   - total: 总数量
//   - offset: 当前偏移量
//   - limit: 当前每页数量
func (h *Handler) ListFunctions(w http.ResponseWriter, r *http.Request) {
	h.logDebug(r, "ListFunctions", "查询函数列表", nil)

	// 解析分页参数
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	// 设置limit的默认值和最大值限制
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	// 解析筛选参数
	filter := &domain.FunctionFilter{
		Name:    r.URL.Query().Get("name"),
		Runtime: domain.Runtime(r.URL.Query().Get("runtime")),
		Status:  domain.FunctionStatus(r.URL.Query().Get("status")),
	}

	// 解析标签参数（逗号分隔）
	if tagsParam := r.URL.Query().Get("tags"); tagsParam != "" {
		filter.Tags = strings.Split(tagsParam, ",")
	}

	// 检查是否有筛选条件
	hasFilter := filter.Name != "" || len(filter.Tags) > 0 || filter.Runtime != "" || filter.Status != ""

	var functions []*domain.Function
	var total int
	var err error

	// 根据是否有筛选条件选择不同的查询方法
	if hasFilter {
		functions, total, err = h.store.ListFunctionsWithFilter(filter, offset, limit)
	} else {
		functions, total, err = h.store.ListFunctions(offset, limit)
	}

	if err != nil {
		h.logError(r, "ListFunctions", "查询函数列表失败", err, logrus.Fields{"offset": offset, "limit": limit})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to list functions: "+err.Error())
		return
	}

	// 获取所有函数的基础统计（最近24小时）
	stats, err := h.store.GetAllFunctionsBasicStats(24)
	if err != nil {
		h.logError(r, "ListFunctions", "获取函数统计失败", err, nil)
		// 继续返回函数列表，只是没有统计数据
		stats = make(map[string]*storage.FunctionBasicStats)
	}

	// 构建响应，包含统计数据
	type FunctionWithStats struct {
		*domain.Function
		Invocations  int64   `json:"invocations,omitempty"`
		SuccessRate  float64 `json:"success_rate,omitempty"`
		AvgLatencyMs float64 `json:"avg_latency_ms,omitempty"`
		ErrorCount   int64   `json:"error_count,omitempty"`
		CodeSize     int     `json:"code_size"`
		CodeSizeLimit int    `json:"code_size_limit"`
	}

	functionsWithStats := make([]FunctionWithStats, len(functions))
	for i, fn := range functions {
		fws := FunctionWithStats{
			Function:      fn,
			CodeSize:      len(fn.Code),
			CodeSizeLimit: domain.MaxCodeSize,
		}
		if s, ok := stats[fn.ID]; ok {
			fws.Invocations = s.Invocations
			fws.SuccessRate = s.SuccessRate
			fws.AvgLatencyMs = s.AvgLatencyMs
			fws.ErrorCount = s.ErrorCount
		}
		functionsWithStats[i] = fws
	}

	h.logDebug(r, "ListFunctions", "查询成功", logrus.Fields{"total": total, "count": len(functions)})
	// 返回分页结果
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"functions": functionsWithStats,
		"total":     total,
		"offset":    offset,
		"limit":     limit,
	})
}

// UpdateFunction 处理更新函数配置的请求。
// HTTP端点: PUT /api/v1/functions/{id}
//
// 功能说明：
//   - 支持部分更新，只更新请求中提供的字段
//   - 如果更新了代码，会异步重新编译（返回202）
//   - 支持通过函数ID或名称定位要更新的函数
//
// 路径参数：
//   - id: 函数的唯一标识符或名称
//
// 请求体：domain.UpdateFunctionRequest (JSON)，所有字段可选
func (h *Handler) UpdateFunction(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetReqID(r.Context())

	// 从URL路径中提取函数ID或名称
	idOrName := chi.URLParam(r, "id")
	if idOrName == "" {
		h.logError(r, "UpdateFunction", "缺少函数标识", nil, nil)
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id or name required")
		return
	}

	h.logInfo(r, "UpdateFunction", "开始更新函数", logrus.Fields{"function": idOrName})

	// 查找要更新的函数
	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		h.logWarn(r, "UpdateFunction", "函数不存在", logrus.Fields{"function": idOrName})
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found: "+idOrName)
		return
	}
	if err != nil {
		h.logError(r, "UpdateFunction", "查询函数失败", err, logrus.Fields{"function": idOrName})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	// 检查函数状态是否允许更新
	if !fn.Status.CanUpdate() {
		h.logWarn(r, "UpdateFunction", "函数状态不允许更新", logrus.Fields{
			"function": fn.Name,
			"status":   fn.Status,
		})
		writeErrorWithContext(w, r, http.StatusBadRequest, "function cannot be updated in current status: "+string(fn.Status))
		return
	}

	// 解析更新请求
	var req domain.UpdateFunctionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logError(r, "UpdateFunction", "解析请求体失败", err, logrus.Fields{"function": fn.Name})
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	h.logDebug(r, "UpdateFunction", "更新参数", logrus.Fields{"function": fn.Name, "id": fn.ID, "request_id": requestID})

	// 按需更新各个字段（部分更新模式）
	if req.Description != nil {
		fn.Description = *req.Description
	}
	if req.Tags != nil {
		fn.Tags = *req.Tags
	}
	if req.Handler != nil {
		fn.Handler = *req.Handler
	}
	needRecompile := false
	if req.Code != nil {
		// 验证代码大小
		if err := domain.ValidateCodeSize(*req.Code); err != nil {
			h.logWarn(r, "UpdateFunction", "代码大小超出限制", logrus.Fields{
				"function":  fn.Name,
				"code_size": len(*req.Code),
				"max_size":  domain.MaxCodeSize,
			})
			writeErrorWithContext(w, r, http.StatusBadRequest, fmt.Sprintf("code size %d exceeds limit %d bytes", len(*req.Code), domain.MaxCodeSize))
			return
		}
		fn.Code = *req.Code
		// 代码更新时重新计算哈希值
		hash := sha256.Sum256([]byte(*req.Code))
		fn.CodeHash = hex.EncodeToString(hash[:])
		needRecompile = true
	}
	if req.MemoryMB != nil {
		fn.MemoryMB = *req.MemoryMB
	}
	if req.TimeoutSec != nil {
		fn.TimeoutSec = *req.TimeoutSec
	}
	if req.MaxConcurrency != nil {
		fn.MaxConcurrency = *req.MaxConcurrency
	}
	if req.EnvVars != nil {
		fn.EnvVars = *req.EnvVars
	}

	if req.CronExpression != nil {
		// 验证 cron 表达式
		if err := domain.ValidateCronExpression(*req.CronExpression); err != nil {
			writeErrorWithContext(w, r, http.StatusBadRequest, "invalid cron expression")
			return
		}
		fn.CronExpression = *req.CronExpression
	}
	if req.HTTPPath != nil {
		fn.HTTPPath = *req.HTTPPath
	}
	if req.HTTPMethods != nil {
		fn.HTTPMethods = *req.HTTPMethods
	}

	// 如果代码更新且是需要编译的运行时，异步处理
	if needRecompile && compiler.IsSourceCode(string(fn.Runtime), fn.Code) {
		h.logInfo(r, "UpdateFunction", "代码变更，异步重新编译", logrus.Fields{"function": fn.Name, "runtime": fn.Runtime})

		// 生成任务ID
		taskID := uuid.New().String()

		// 更新函数状态为 updating
		fn.Status = domain.FunctionStatusUpdating
		fn.StatusMessage = "函数正在更新中"
		fn.TaskID = taskID

		// 保存更新后的函数（状态为 updating）
		if err := h.store.UpdateFunction(fn); err != nil {
			h.logError(r, "UpdateFunction", "保存函数失败", err, logrus.Fields{"function": fn.Name})
			writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to update function: "+err.Error())
			return
		}

		// 创建异步任务
		taskInput, _ := json.Marshal(req)
		task := &domain.FunctionTask{
			ID:         taskID,
			FunctionID: fn.ID,
			Type:       domain.FunctionTaskUpdate,
			Status:     domain.FunctionTaskPending,
			Input:      taskInput,
		}
		if err := h.store.CreateFunctionTask(task); err != nil {
			h.logError(r, "UpdateFunction", "创建任务失败", err, logrus.Fields{"function": fn.Name})
			writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create task: "+err.Error())
			return
		}

		// 异步处理编译任务
		go h.processUpdateFunctionTask(fn.ID, taskID)

		h.logInfo(r, "UpdateFunction", "函数已更新，编译任务已提交", logrus.Fields{"function": fn.Name, "id": fn.ID, "task_id": taskID})

		// 返回 200 OK，源代码已保存，编译在后台进行
		writeJSON(w, http.StatusOK, fn)
		return
	}

	// 如果代码有变更但不需要编译，直接创建版本快照
	if needRecompile {
		latestVersion, _ := h.store.GetLatestFunctionVersion(fn.ID)
		versionSnapshot := &domain.FunctionVersion{
			FunctionID:  fn.ID,
			Version:     latestVersion + 1,
			Handler:     fn.Handler,
			Code:        fn.Code,
			Binary:      fn.Binary,
			CodeHash:    fn.CodeHash,
			Description: "Auto-saved version",
		}
		if err := h.store.CreateFunctionVersion(versionSnapshot); err != nil {
			h.logWarn(r, "UpdateFunction", "创建版本快照失败", logrus.Fields{"function": fn.Name, "error": err.Error()})
		} else {
			h.logDebug(r, "UpdateFunction", "版本快照已创建", logrus.Fields{"function": fn.Name, "version": versionSnapshot.Version})
		}
	}

	// 保存更新后的函数
	if err := h.store.UpdateFunction(fn); err != nil {
		h.logError(r, "UpdateFunction", "保存函数失败", err, logrus.Fields{"function": fn.Name})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to update function: "+err.Error())
		return
	}

	// 同步定时任务
	if h.cronManager != nil {
		h.cronManager.AddOrUpdateFunction(fn)
		h.logDebug(r, "UpdateFunction", "同步定时任务", logrus.Fields{"function": fn.Name, "cron": fn.CronExpression})
	}

	h.logInfo(r, "UpdateFunction", "函数更新成功", logrus.Fields{"function": fn.Name, "id": fn.ID})
	writeJSON(w, http.StatusOK, fn)
}

// processUpdateFunctionTask 异步处理函数更新任务
// 流程：源代码已在 UpdateFunction 中保存 → 编译 → 更新二进制和状态
func (h *Handler) processUpdateFunctionTask(functionID, taskID string) {
	// 更新任务状态为 running
	now := time.Now()
	h.store.UpdateFunctionTask(&domain.FunctionTask{
		ID:        taskID,
		Status:    domain.FunctionTaskRunning,
		StartedAt: &now,
	})

	// 获取函数信息（源代码已保存）
	fn, err := h.store.GetFunctionByID(functionID)
	if err != nil {
		h.completeTaskWithError(taskID, functionID, "failed to get function: "+err.Error())
		return
	}

	h.logger.WithFields(logrus.Fields{
		"function_id": functionID,
		"task_id":     taskID,
		"runtime":     fn.Runtime,
	}).Info("开始编译函数，源代码已保存")

	// 更新状态为 building
	h.store.UpdateFunctionStatus(functionID, domain.FunctionStatusBuilding, "正在编译源代码", taskID)

	// 执行编译（使用带超时的 context）
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	compileResp, err := h.compiler.Compile(ctx, &compiler.CompileRequest{
		Runtime: string(fn.Runtime),
		Code:    fn.Code,
	})
	if err != nil {
		h.completeTaskWithError(taskID, functionID, "compilation error: "+err.Error())
		return
	}
	if !compileResp.Success {
		h.completeTaskWithError(taskID, functionID, "compilation failed: "+compileResp.Error)
		return
	}

	// 编译成功，仅更新二进制字段
	if err := h.store.UpdateFunctionBinary(functionID, compileResp.Binary); err != nil {
		h.completeTaskWithError(taskID, functionID, "failed to save binary: "+err.Error())
		return
	}

	h.logger.WithFields(logrus.Fields{
		"function_id": functionID,
		"task_id":     taskID,
	}).Info("编译完成，二进制已保存")

	// 创建版本快照
	latestVersion, _ := h.store.GetLatestFunctionVersion(fn.ID)
	versionSnapshot := &domain.FunctionVersion{
		FunctionID:  fn.ID,
		Version:     latestVersion + 1,
		Handler:     fn.Handler,
		Code:        fn.Code,
		Binary:      compileResp.Binary,
		CodeHash:    fn.CodeHash,
		Description: "Auto-saved version",
	}
	h.store.CreateFunctionVersion(versionSnapshot)

	// 更新函数状态为 active
	if err := h.store.SetFunctionDeployed(functionID); err != nil {
		h.completeTaskWithError(taskID, functionID, "failed to update function status: "+err.Error())
		return
	}

	// 同步定时任务
	if h.cronManager != nil {
		// 重新获取最新的函数信息
		latestFn, _ := h.store.GetFunctionByID(functionID)
		if latestFn != nil {
			h.cronManager.AddOrUpdateFunction(latestFn)
		}
	}

	// 完成任务
	completedAt := time.Now()
	h.store.UpdateFunctionTask(&domain.FunctionTask{
		ID:          taskID,
		Status:      domain.FunctionTaskCompleted,
		CompletedAt: &completedAt,
	})

	h.logger.WithFields(logrus.Fields{
		"function_id": functionID,
		"task_id":     taskID,
	}).Info("函数更新任务完成")
}

// DeleteFunction 处理删除函数的请求。
// HTTP端点: DELETE /api/v1/functions/{id}
//
// 功能说明：
//   - 永久删除指定的函数
//   - 支持通过函数ID或名称定位要删除的函数
//
// 路径参数：
//   - id: 函数的唯一标识符或名称
//
// 返回值：
//   - 204: 删除成功（无内容返回）
//   - 404: 函数不存在
//   - 500: 服务器内部错误
func (h *Handler) DeleteFunction(w http.ResponseWriter, r *http.Request) {
	// 从URL路径中提取函数ID或名称
	idOrName := chi.URLParam(r, "id")
	if idOrName == "" {
		h.logError(r, "DeleteFunction", "缺少函数标识", nil, nil)
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id or name required")
		return
	}

	h.logInfo(r, "DeleteFunction", "开始删除函数", logrus.Fields{"function": idOrName})

	// 解析函数标识符，如果提供的是名称则转换为ID
	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		h.logWarn(r, "DeleteFunction", "函数不存在", logrus.Fields{"function": idOrName})
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found: "+idOrName)
		return
	}
	if err != nil {
		h.logError(r, "DeleteFunction", "查询函数失败", err, logrus.Fields{"function": idOrName})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	// 执行删除操作
	if err := h.store.DeleteFunction(fn.ID); err != nil {
		h.logError(r, "DeleteFunction", "删除函数失败", err, logrus.Fields{"function": fn.Name, "id": fn.ID})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to delete function: "+err.Error())
		return
	}

	// 移除定时任务
	if h.cronManager != nil {
		h.cronManager.RemoveFunction(fn.ID)
		h.logDebug(r, "DeleteFunction", "移除定时任务", logrus.Fields{"function": fn.Name})
	}

	h.logInfo(r, "DeleteFunction", "函数删除成功", logrus.Fields{"function": fn.Name, "id": fn.ID})
	// 返回204 No Content表示删除成功
	w.WriteHeader(http.StatusNoContent)
}

// ==================== 批量操作处理器 ====================

// BulkDeleteFunctions 批量删除函数。
// HTTP端点: POST /api/v1/functions/bulk-delete
//
// 功能说明：
//   - 批量删除多个函数
//   - 返回成功和失败的详细信息
func (h *Handler) BulkDeleteFunctions(w http.ResponseWriter, r *http.Request) {
	h.logInfo(r, "BulkDeleteFunctions", "开始批量删除函数", nil)

	var req domain.BulkDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logError(r, "BulkDeleteFunctions", "解析请求体失败", err, nil)
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if len(req.IDs) == 0 {
		writeErrorWithContext(w, r, http.StatusBadRequest, "ids is required and cannot be empty")
		return
	}

	result := domain.BulkOperationResult{
		Success: make([]string, 0),
		Failed:  make([]domain.BulkOperationFailure, 0),
	}

	for _, id := range req.IDs {
		// 查找函数
		fn, err := h.store.GetFunctionByID(id)
		if err == domain.ErrFunctionNotFound {
			fn, err = h.store.GetFunctionByName(id)
		}
		if err == domain.ErrFunctionNotFound {
			result.Failed = append(result.Failed, domain.BulkOperationFailure{
				ID:    id,
				Error: "function not found",
			})
			continue
		}
		if err != nil {
			result.Failed = append(result.Failed, domain.BulkOperationFailure{
				ID:    id,
				Error: "failed to get function: " + err.Error(),
			})
			continue
		}

		// 执行删除
		if err := h.store.DeleteFunction(fn.ID); err != nil {
			result.Failed = append(result.Failed, domain.BulkOperationFailure{
				ID:    fn.ID,
				Error: "failed to delete: " + err.Error(),
			})
			continue
		}

		// 移除定时任务
		if h.cronManager != nil {
			h.cronManager.RemoveFunction(fn.ID)
		}

		result.Success = append(result.Success, fn.ID)
		h.logDebug(r, "BulkDeleteFunctions", "删除成功", logrus.Fields{"id": fn.ID, "name": fn.Name})
	}

	h.logInfo(r, "BulkDeleteFunctions", "批量删除完成", logrus.Fields{
		"success_count": len(result.Success),
		"failed_count":  len(result.Failed),
	})
	writeJSON(w, http.StatusOK, result)
}

// BulkUpdateFunctions 批量更新函数状态或标签。
// HTTP端点: POST /api/v1/functions/bulk-update
//
// 功能说明：
//   - 批量更新多个函数的状态或标签
//   - 返回成功和失败的详细信息
func (h *Handler) BulkUpdateFunctions(w http.ResponseWriter, r *http.Request) {
	h.logInfo(r, "BulkUpdateFunctions", "开始批量更新函数", nil)

	var req domain.BulkUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logError(r, "BulkUpdateFunctions", "解析请求体失败", err, nil)
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if len(req.IDs) == 0 {
		writeErrorWithContext(w, r, http.StatusBadRequest, "ids is required and cannot be empty")
		return
	}

	// 检查是否有更新内容
	if req.Status == "" && len(req.Tags) == 0 {
		writeErrorWithContext(w, r, http.StatusBadRequest, "at least one of status or tags must be provided")
		return
	}

	result := domain.BulkOperationResult{
		Success: make([]string, 0),
		Failed:  make([]domain.BulkOperationFailure, 0),
	}

	for _, id := range req.IDs {
		// 查找函数
		fn, err := h.store.GetFunctionByID(id)
		if err == domain.ErrFunctionNotFound {
			fn, err = h.store.GetFunctionByName(id)
		}
		if err == domain.ErrFunctionNotFound {
			result.Failed = append(result.Failed, domain.BulkOperationFailure{
				ID:    id,
				Error: "function not found",
			})
			continue
		}
		if err != nil {
			result.Failed = append(result.Failed, domain.BulkOperationFailure{
				ID:    id,
				Error: "failed to get function: " + err.Error(),
			})
			continue
		}

		// 更新状态
		if req.Status != "" {
			// 验证状态转换是否合法
			switch req.Status {
			case domain.FunctionStatusActive:
				if !fn.Status.CanOnline() && fn.Status != domain.FunctionStatusActive {
					result.Failed = append(result.Failed, domain.BulkOperationFailure{
						ID:    fn.ID,
						Error: fmt.Sprintf("cannot change status from %s to %s", fn.Status, req.Status),
					})
					continue
				}
			case domain.FunctionStatusOffline:
				if !fn.Status.CanOffline() && fn.Status != domain.FunctionStatusOffline {
					result.Failed = append(result.Failed, domain.BulkOperationFailure{
						ID:    fn.ID,
						Error: fmt.Sprintf("cannot change status from %s to %s", fn.Status, req.Status),
					})
					continue
				}
			case domain.FunctionStatusInactive:
				// 允许将任何状态设置为 inactive
			default:
				result.Failed = append(result.Failed, domain.BulkOperationFailure{
					ID:    fn.ID,
					Error: fmt.Sprintf("invalid status: %s", req.Status),
				})
				continue
			}
			fn.Status = req.Status
		}

		// 更新标签
		if len(req.Tags) > 0 {
			fn.Tags = req.Tags
		}

		// 保存更新
		if err := h.store.UpdateFunction(fn); err != nil {
			result.Failed = append(result.Failed, domain.BulkOperationFailure{
				ID:    fn.ID,
				Error: "failed to update: " + err.Error(),
			})
			continue
		}

		result.Success = append(result.Success, fn.ID)
		h.logDebug(r, "BulkUpdateFunctions", "更新成功", logrus.Fields{"id": fn.ID, "name": fn.Name})
	}

	h.logInfo(r, "BulkUpdateFunctions", "批量更新完成", logrus.Fields{
		"success_count": len(result.Success),
		"failed_count":  len(result.Failed),
	})
	writeJSON(w, http.StatusOK, result)
}

// CloneFunction 处理克隆函数的请求。
// HTTP端点: POST /api/v1/functions/{id}/clone
//
// 功能说明：
//   - 复制现有函数的所有配置和代码
//   - 使用新的名称创建新函数
//   - 异步创建，返回新函数ID和任务ID
//
// 路径参数：
//   - id: 要克隆的函数ID或名称
//
// 请求体：
//   - name: 新函数的名称（必填）
//   - description: 新函数的描述（可选，默认复制原函数描述）
//
// 返回值：成功返回202和新函数信息（含任务ID）
func (h *Handler) CloneFunction(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetReqID(r.Context())
	idOrName := chi.URLParam(r, "id")
	if idOrName == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id or name required")
		return
	}

	h.logInfo(r, "CloneFunction", "开始克隆函数", logrus.Fields{"request_id": requestID, "source": idOrName})

	// 获取源函数
	sourceFn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		sourceFn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		h.logWarn(r, "CloneFunction", "源函数不存在", logrus.Fields{"source": idOrName})
		writeErrorWithContext(w, r, http.StatusNotFound, "source function not found: "+idOrName)
		return
	}
	if err != nil {
		h.logError(r, "CloneFunction", "查询源函数失败", err, logrus.Fields{"source": idOrName})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get source function: "+err.Error())
		return
	}

	// 解析请求体
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logError(r, "CloneFunction", "解析请求体失败", err, nil)
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// 验证新名称
	if req.Name == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "new function name is required")
		return
	}

	// 检查新名称是否已存在
	existing, _ := h.store.GetFunctionByName(req.Name)
	if existing != nil {
		h.logWarn(r, "CloneFunction", "函数名称已存在", logrus.Fields{"name": req.Name})
		writeErrorWithContext(w, r, http.StatusConflict, "function with this name already exists")
		return
	}

	// 使用源函数的描述（如果未提供新描述）
	description := req.Description
	if description == "" {
		description = sourceFn.Description
	}

	// 计算代码哈希
	hash := sha256.Sum256([]byte(sourceFn.Code))
	codeHash := hex.EncodeToString(hash[:])

	// 生成任务ID
	taskID := uuid.New().String()

	// 复制标签
	var tags []string
	if len(sourceFn.Tags) > 0 {
		tags = make([]string, len(sourceFn.Tags))
		copy(tags, sourceFn.Tags)
	}

	// 复制环境变量
	var envVars map[string]string
	if len(sourceFn.EnvVars) > 0 {
		envVars = make(map[string]string)
		for k, v := range sourceFn.EnvVars {
			envVars[k] = v
		}
	}

	// 复制HTTP方法
	var httpMethods []string
	if len(sourceFn.HTTPMethods) > 0 {
		httpMethods = make([]string, len(sourceFn.HTTPMethods))
		copy(httpMethods, sourceFn.HTTPMethods)
	}

	// 构建新函数对象
	newFn := &domain.Function{
		Name:           req.Name,
		Description:    description,
		Tags:           tags,
		Runtime:        sourceFn.Runtime,
		Handler:        sourceFn.Handler,
		Code:           sourceFn.Code,
		Binary:         sourceFn.Binary,
		CodeHash:       codeHash,
		MemoryMB:       sourceFn.MemoryMB,
		TimeoutSec:     sourceFn.TimeoutSec,
		EnvVars:        envVars,
		CronExpression: sourceFn.CronExpression,
		HTTPPath:       "", // HTTP路径需要用户重新配置，避免冲突
		HTTPMethods:    httpMethods,
		Status:         domain.FunctionStatusCreating,
		StatusMessage:  "函数正在创建中（克隆自 " + sourceFn.Name + "）",
		TaskID:         taskID,
		Version:        1,
	}

	// 保存函数到数据库
	if err := h.store.CreateFunction(newFn); err != nil {
		h.logError(r, "CloneFunction", "保存函数失败", err, logrus.Fields{"name": req.Name})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create function: "+err.Error())
		return
	}

	// 创建异步任务
	createReq := domain.CreateFunctionRequest{
		Name:           req.Name,
		Description:    description,
		Tags:           tags,
		Runtime:        sourceFn.Runtime,
		Handler:        sourceFn.Handler,
		Code:           sourceFn.Code,
		Binary:         sourceFn.Binary,
		MemoryMB:       sourceFn.MemoryMB,
		TimeoutSec:     sourceFn.TimeoutSec,
		EnvVars:        envVars,
		CronExpression: sourceFn.CronExpression,
		HTTPMethods:    httpMethods,
	}
	taskInput, _ := json.Marshal(createReq)
	task := &domain.FunctionTask{
		ID:         taskID,
		FunctionID: newFn.ID,
		Type:       domain.FunctionTaskCreate,
		Status:     domain.FunctionTaskPending,
		Input:      taskInput,
	}
	if err := h.store.CreateFunctionTask(task); err != nil {
		h.logError(r, "CloneFunction", "创建任务失败", err, logrus.Fields{"name": req.Name})
		h.store.DeleteFunction(newFn.ID)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create task: "+err.Error())
		return
	}

	// 异步处理任务
	go h.processCreateFunctionTask(newFn.ID, taskID)

	h.logInfo(r, "CloneFunction", "函数克隆任务已提交", logrus.Fields{
		"source":  sourceFn.Name,
		"target":  newFn.Name,
		"id":      newFn.ID,
		"task_id": taskID,
	})

	// 返回 202 Accepted
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"function":        newFn,
		"task_id":         taskID,
		"cloned_from":     sourceFn.Name,
		"cloned_from_id":  sourceFn.ID,
		"message":         "函数正在创建中，请通过任务ID查询进度",
	})
}

// InvokeFunction 处理同步调用函数的请求。
// HTTP端点: POST /api/v1/functions/{id}/invoke
//
// 功能说明：
//   - 同步执行指定的函数并等待返回结果
//   - 请求体作为函数的输入载荷传递
//   - 只有状态为Active的函数才能被调用
//
// 路径参数：
//   - id: 函数的唯一标识符或名称
//
// 请求体：任意JSON格式的载荷，将作为函数的输入参数
//
// 返回值：函数执行的响应结果
func (h *Handler) InvokeFunction(w http.ResponseWriter, r *http.Request) {
	// 从URL路径中提取函数ID或名称
	idOrName := chi.URLParam(r, "id")
	if idOrName == "" {
		h.logError(r, "InvokeFunction", "缺少函数标识", nil, nil)
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id or name required")
		return
	}

	h.logInfo(r, "InvokeFunction", "开始调用函数", logrus.Fields{"function": idOrName})

	// 查找要调用的函数
	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		h.logWarn(r, "InvokeFunction", "函数不存在", logrus.Fields{"function": idOrName})
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found: "+idOrName)
		return
	}
	if err != nil {
		h.logError(r, "InvokeFunction", "查询函数失败", err, logrus.Fields{"function": idOrName})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	// 检查函数状态，只有Active状态的函数才能被调用
	if !fn.Status.CanInvoke() {
		h.logWarn(r, "InvokeFunction", "函数状态不可用", logrus.Fields{
			"function": fn.Name,
			"status":   fn.Status,
		})
		writeErrorWithContext(w, r, http.StatusBadRequest, "function is not active, current status: "+string(fn.Status))
		return
	}

	// 解析请求体作为函数输入载荷
	var payload json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && err.Error() != "EOF" {
		h.logError(r, "InvokeFunction", "解析请求体失败", err, logrus.Fields{"function": fn.Name})
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	// 如果没有提供载荷，使用空JSON对象作为默认值
	if payload == nil {
		payload = json.RawMessage("{}")
	}

	// 生成请求ID
	requestID := generateRequestID()

	h.logDebug(r, "InvokeFunction", "调用参数", logrus.Fields{
		"function":    fn.Name,
		"function_id": fn.ID,
		"request_id":  requestID,
		"payload":     string(payload),
	})

	// 广播调用开始日志
	BroadcastLog(LogMessage{
		Timestamp:    time.Now(),
		Level:        "INFO",
		FunctionID:   fn.ID,
		FunctionName: fn.Name,
		Message:      "函数调用开始",
		RequestID:    requestID,
		Input:        payload,
	})

	// 构建调用请求
	req := &domain.InvokeRequest{
		FunctionID: fn.ID,
		Payload:    payload,
		Async:      false,
	}

	// 记录开始时间
	startTime := time.Now()

	// 通过调度器同步执行函数
	resp, err := h.scheduler.Invoke(req)

	// 计算耗时
	durationMs := time.Since(startTime).Milliseconds()

	if err != nil {
		h.logError(r, "InvokeFunction", "函数调用失败", err, logrus.Fields{
			"function":    fn.Name,
			"function_id": fn.ID,
			"request_id":  requestID,
			"duration_ms": durationMs,
		})
		// 广播错误日志
		BroadcastLog(LogMessage{
			Timestamp:    time.Now(),
			Level:        "ERROR",
			FunctionID:   fn.ID,
			FunctionName: fn.Name,
			Message:      "函数调用失败",
			RequestID:    requestID,
			Input:        payload,
			Error:        err.Error(),
			DurationMs:   durationMs,
		})
		// 返回带堆栈的错误响应
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":       err.Error(),
			"stack":       getStackTrace(0),
			"request_id":  requestID,
			"function":    fn.Name,
			"duration_ms": durationMs,
		})
		return
	}

	// 广播调用完成日志
	BroadcastLog(LogMessage{
		Timestamp:    time.Now(),
		Level:        "INFO",
		FunctionID:   fn.ID,
		FunctionName: fn.Name,
		Message:      "函数调用完成",
		RequestID:    requestID,
		Input:        payload,
		Output:       resp.Body,
		DurationMs:   durationMs,
	})

	// 返回函数执行结果
	writeJSON(w, resp.StatusCode, resp)
}

// InvokeFunctionAsync 处理异步调用函数的请求。
// HTTP端点: POST /api/v1/functions/{id}/async
//
// 功能说明：
//   - 异步执行指定的函数，立即返回请求ID
//   - 函数在后台执行，可通过请求ID查询执行结果
//   - 适用于长时间运行的任务
//
// 路径参数：
//   - id: 函数的唯一标识符或名称
//
// 请求体：任意JSON格式的载荷，将作为函数的输入参数
//
// 返回值：
//   - 202 Accepted: 请求已接受，返回request_id用于后续查询
func (h *Handler) InvokeFunctionAsync(w http.ResponseWriter, r *http.Request) {
	// 从URL路径中提取函数ID或名称
	idOrName := chi.URLParam(r, "id")
	if idOrName == "" {
		writeError(w, http.StatusBadRequest, "function id or name required")
		return
	}

	// 查找要调用的函数
	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeError(w, http.StatusNotFound, "function not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get function")
		return
	}

	// 检查函数状态，只有Active状态的函数才能被调用
	if !fn.Status.CanInvoke() {
		writeError(w, http.StatusBadRequest, "function is not active, current status: "+string(fn.Status))
		return
	}

	// 解析请求体作为函数输入载荷
	var payload json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && err.Error() != "EOF" {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// 如果没有提供载荷，使用空JSON对象作为默认值
	if payload == nil {
		payload = json.RawMessage("{}")
	}

	// 构建异步调用请求
	req := &domain.InvokeRequest{
		FunctionID: fn.ID,
		Payload:    payload,
		Async:      true,
	}

	// 通过调度器提交异步执行请求
	requestID, err := h.scheduler.InvokeAsync(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 返回请求ID，表示异步调用已被接受
	writeJSON(w, http.StatusAccepted, map[string]string{
		"request_id": requestID,
		"status":     "accepted",
	})
}

// GetInvocation 处理获取单个调用记录详情的请求。
// HTTP端点: GET /api/v1/invocations/{id}
//
// 功能说明：
//   - 查询指定ID的函数调用记录
//   - 包含调用的输入、输出、状态和执行时间等信息
//
// 路径参数：
//   - id: 调用记录的唯一标识符
//
// 返回值：
//   - 200: 成功，返回调用记录详情
//   - 404: 调用记录不存在
func (h *Handler) GetInvocation(w http.ResponseWriter, r *http.Request) {
	// 从URL路径中提取调用记录ID
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invocation id required")
		return
	}

	// 查询调用记录
	inv, err := h.store.GetInvocationByID(id)
	if err == domain.ErrInvocationNotFound {
		writeError(w, http.StatusNotFound, "invocation not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get invocation")
		return
	}

	writeJSON(w, http.StatusOK, inv)
}

// ReplayInvocation 处理重放调用记录的请求。
// HTTP端点: POST /api/v1/invocations/{id}/replay
//
// 功能说明：
//   - 使用历史调用记录的输入参数重新执行函数
//   - 适用于调试和问题重现
//
// 路径参数：
//   - id: 调用记录的唯一标识符
//
// 返回值：
//   - 200: 成功，返回新的调用结果
//   - 404: 调用记录不存在或函数已删除
func (h *Handler) ReplayInvocation(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetReqID(r.Context())

	// 从URL路径中提取调用记录ID
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invocation id required")
		return
	}

	h.logInfo(r, "ReplayInvocation", "开始重放调用", logrus.Fields{"invocation_id": id, "request_id": requestID})

	// 查询原始调用记录
	inv, err := h.store.GetInvocationByID(id)
	if err == domain.ErrInvocationNotFound {
		h.logWarn(r, "ReplayInvocation", "调用记录不存在", logrus.Fields{"invocation_id": id})
		writeError(w, http.StatusNotFound, "invocation not found")
		return
	}
	if err != nil {
		h.logError(r, "ReplayInvocation", "查询调用记录失败", err, logrus.Fields{"invocation_id": id})
		writeError(w, http.StatusInternalServerError, "failed to get invocation: "+err.Error())
		return
	}

	// 查询函数信息
	fn, err := h.store.GetFunctionByID(inv.FunctionID)
	if err == domain.ErrFunctionNotFound {
		h.logWarn(r, "ReplayInvocation", "函数已删除", logrus.Fields{"function_id": inv.FunctionID})
		writeError(w, http.StatusNotFound, "function not found (may have been deleted)")
		return
	}
	if err != nil {
		h.logError(r, "ReplayInvocation", "查询函数失败", err, logrus.Fields{"function_id": inv.FunctionID})
		writeError(w, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	// 检查函数状态
	if !fn.Status.CanInvoke() {
		h.logWarn(r, "ReplayInvocation", "函数当前状态不可调用", logrus.Fields{
			"function": fn.Name,
			"status":   fn.Status,
		})
		writeError(w, http.StatusBadRequest, "function is not active, current status: "+string(fn.Status))
		return
	}

	h.logInfo(r, "ReplayInvocation", "重放调用", logrus.Fields{
		"function":            fn.Name,
		"original_invocation": id,
		"request_id":          requestID,
	})

	startTime := time.Now()

	// 构建调用请求
	req := &domain.InvokeRequest{
		FunctionID: fn.ID,
		Payload:    inv.Input,
	}

	// 执行函数调用
	resp, err := h.scheduler.Invoke(req)
	durationMs := time.Since(startTime).Milliseconds()

	if err != nil {
		h.logError(r, "ReplayInvocation", "函数调用失败", err, logrus.Fields{
			"function":            fn.Name,
			"original_invocation": id,
			"duration_ms":         durationMs,
		})
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":                err.Error(),
			"request_id":           requestID,
			"original_invocation":  id,
			"duration_ms":          durationMs,
		})
		return
	}

	h.logInfo(r, "ReplayInvocation", "重放调用完成", logrus.Fields{
		"function":            fn.Name,
		"original_invocation": id,
		"duration_ms":         durationMs,
		"status_code":         resp.StatusCode,
	})

	// 返回结果（包含原始调用ID以便追踪）
	writeJSON(w, resp.StatusCode, map[string]interface{}{
		"request_id":          requestID,
		"original_invocation": id,
		"status_code":         resp.StatusCode,
		"body":                resp.Body,
		"duration_ms":         resp.DurationMs,
		"cold_start":          resp.ColdStart,
		"billed_time_ms":      resp.BilledTimeMs,
	})
}

// ListInvocations 处理获取函数调用记录列表的请求。
// HTTP端点: GET /api/v1/functions/{id}/invocations
//
// 功能说明：
//   - 查询指定函数的所有调用记录
//   - 支持分页查询
//
// 路径参数：
//   - id: 函数的唯一标识符或名称
//
// 查询参数：
//   - offset: 偏移量（默认0）
//   - limit: 每页数量，范围1-100（默认20）
//
// 返回值：
//   - invocations: 调用记录列表
//   - total: 总数量
//   - offset/limit: 分页信息
func (h *Handler) ListInvocations(w http.ResponseWriter, r *http.Request) {
	// 从URL路径中提取函数ID或名称
	idOrName := chi.URLParam(r, "id")
	if idOrName == "" {
		writeError(w, http.StatusBadRequest, "function id or name required")
		return
	}

	// 解析函数标识符，如果提供的是名称则转换为ID
	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeError(w, http.StatusNotFound, "function not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get function")
		return
	}

	// 解析分页参数
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	// 设置limit的默认值和最大值限制
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	// 查询该函数的调用记录
	invocations, total, err := h.store.ListInvocationsByFunction(fn.ID, offset, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list invocations")
		return
	}

	// 返回分页结果
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"invocations": invocations,
		"total":       total,
		"offset":      offset,
		"limit":       limit,
	})
}

// ListAllInvocations 处理获取所有调用记录列表的请求。
// HTTP端点: GET /api/v1/invocations
//
// 功能说明：
//   - 查询所有函数的调用记录
//   - 支持按状态过滤和分页查询
//
// 查询参数：
//   - status: 状态过滤（可选）
//   - offset: 偏移量（默认0）
//   - limit: 每页数量，范围1-100（默认20）
//
// 返回值：
//   - invocations: 调用记录列表
//   - total: 总数量
//   - offset/limit: 分页信息
func (h *Handler) ListAllInvocations(w http.ResponseWriter, r *http.Request) {
	// 解析参数
	status := r.URL.Query().Get("status")
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	// 设置limit的默认值和最大值限制
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	// 查询所有调用记录
	invocations, total, err := h.store.ListAllInvocations(status, offset, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list invocations")
		return
	}

	// 返回分页结果
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"invocations": invocations,
		"total":       total,
		"offset":      offset,
		"limit":       limit,
	})
}

// Health 处理基本健康检查请求。
// HTTP端点: GET /health
//
// 功能说明：
//   - 返回服务的基本运行状态
//   - 用于负载均衡器的健康检查
//
// 返回值：{"status": "healthy"}
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

// Ready 处理Kubernetes就绪探针请求。
// HTTP端点: GET /health/ready
//
// 功能说明：
//   - 检查服务是否已准备好接收流量
//   - 验证数据库连接是否正常
//   - 用于Kubernetes的readiness probe
//
// 返回值：
//   - 200: 服务就绪
//   - 503: 服务未就绪（如数据库连接失败）
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	// 检查数据库连接
	if err := h.store.Ping(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// Live 处理Kubernetes存活探针请求。
// HTTP端点: GET /health/live
//
// 功能说明：
//   - 检查服务进程是否存活
//   - 用于Kubernetes的liveness probe
//   - 如果该端点无响应，Kubernetes将重启Pod
//
// 返回值：{"status": "alive"}
func (h *Handler) Live(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

// Stats 处理获取系统统计信息的请求。
// HTTP端点: GET /api/v1/stats
//
// 功能说明：
//   - 返回系统的基本统计数据
//   - 包括函数总数和调用总数
//
// 返回值：
//   - functions: 系统中的函数总数
//   - invocations: 累计调用次数
func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	// 获取函数和调用的统计数量
	fnCount, _ := h.store.CountFunctions()
	invCount, _ := h.store.CountInvocations()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"functions":   fnCount,
		"invocations": invCount,
	})
}

// generateRequestID 生成唯一的请求ID
func generateRequestID() string {
	return uuid.New().String()[:8]
}

// writeJSON 将数据以JSON格式写入HTTP响应。
//
// 参数：
//   - w: HTTP响应写入器
//   - status: HTTP状态码
//   - data: 要序列化为JSON的数据对象
//
// 功能说明：
//   - 设置Content-Type为application/json
//   - 写入指定的HTTP状态码
//   - 将data序列化为JSON并写入响应体
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// ErrorResponse 是增强的错误响应结构体。
// 包含错误信息、堆栈跟踪和请求追踪信息，方便前端和CLI调试。
type ErrorResponse struct {
	Error     string `json:"error"`                // 错误消息
	Stack     string `json:"stack,omitempty"`      // 堆栈跟踪信息
	RequestID string `json:"request_id,omitempty"` // 请求ID，用于关联日志
	TraceID   string `json:"trace_id,omitempty"`   // 链路追踪ID
}

// getStackTrace 获取当前调用堆栈信息。
// skip 参数指定跳过的调用层数（不包含 getStackTrace 自身）。
func getStackTrace(skip int) string {
	const maxDepth = 32
	var pcs [maxDepth]uintptr
	n := runtime.Callers(skip+2, pcs[:]) // +2 跳过 Callers 和 getStackTrace
	if n == 0 {
		return ""
	}

	frames := runtime.CallersFrames(pcs[:n])
	var sb strings.Builder
	for {
		frame, more := frames.Next()
		// 过滤掉标准库和第三方库的调用
		if strings.Contains(frame.File, "runtime/") ||
			strings.Contains(frame.File, "net/http") {
			if !more {
				break
			}
			continue
		}
		sb.WriteString(frame.Function)
		sb.WriteString("\n\t")
		sb.WriteString(frame.File)
		sb.WriteString(":")
		sb.WriteString(strconv.Itoa(frame.Line))
		sb.WriteString("\n")
		if !more {
			break
		}
	}
	return sb.String()
}

// writeError 将错误信息以JSON格式写入HTTP响应。
//
// 参数：
//   - w: HTTP响应写入器
//   - status: HTTP错误状态码
//   - message: 错误描述信息
//
// 功能说明：
//   - 封装错误信息为统一的JSON格式，包含堆栈跟踪
//   - 自动从请求上下文中提取 request_id
//   - 便于客户端统一处理错误响应
func writeError(w http.ResponseWriter, status int, message string) {
	// 获取堆栈信息
	stack := getStackTrace(1)

	// 尝试从响应头获取 request_id（由 middleware.RequestID 设置）
	requestID := w.Header().Get("X-Request-Id")
	if requestID == "" {
		requestID = middleware.GetReqID(nil) // 尝试从其他方式获取
	}

	errResp := ErrorResponse{
		Error:     message,
		Stack:     stack,
		RequestID: requestID,
	}

	writeJSON(w, status, errResp)
}

// writeErrorWithContext 将错误信息以JSON格式写入HTTP响应，带请求上下文。
//
// 参数：
//   - w: HTTP响应写入器
//   - r: HTTP请求，用于提取上下文信息
//   - status: HTTP错误状态码
//   - message: 错误描述信息
//
// 功能说明：
//   - 封装错误信息为统一的JSON格式，包含堆栈跟踪
//   - 从请求上下文中提取 request_id 和 trace_id
//   - 便于客户端统一处理错误响应和调试
func writeErrorWithContext(w http.ResponseWriter, r *http.Request, status int, message string) {
	// 获取堆栈信息
	stack := getStackTrace(1)

	// 从请求上下文获取 request_id
	requestID := middleware.GetReqID(r.Context())

	errResp := ErrorResponse{
		Error:     message,
		Stack:     stack,
		RequestID: requestID,
	}

	writeJSON(w, status, errResp)
}

// CompileCode 处理代码编译请求。
// HTTP端点: POST /api/v1/compile
//
// 功能说明：
//   - 接收 Go 或 Rust 源代码
//   - 编译为二进制文件（Go）或 WebAssembly（Rust）
//   - 返回 base64 编码的二进制
//
// 请求体格式:
//
//	{
//	"runtime": "go1.24" 或 "wasm",

//	  "code": "源代码内容"
//	}
//
// 响应格式:
//
//	{
//	  "binary": "base64编码的二进制",
//	  "success": true/false,
//	  "error": "错误信息（如果失败）",
//	  "output": "编译输出"
//	}
func (h *Handler) CompileCode(w http.ResponseWriter, r *http.Request) {
	var req compiler.CompileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 验证运行时
	if req.Runtime != "go1.24" && req.Runtime != "wasm" && req.Runtime != "rust1.75" {
		writeError(w, http.StatusBadRequest, "only go1.24, wasm and rust1.75 runtimes support compilation")
		return
	}

	// 验证代码不为空
	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	// 执行编译
	resp, err := h.compiler.Compile(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if !resp.Success {
		writeJSON(w, http.StatusBadRequest, resp)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleCustomRoute 处理自定义 HTTP 路由请求。
func (h *Handler) HandleCustomRoute(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	// 查找匹配该路径的函数
	fn, err := h.store.GetFunctionByPath(path)
	if err == domain.ErrFunctionNotFound {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query custom route")
		return
	}

	// 检查函数状态，只有Active状态的函数才能被调用
	if !fn.Status.CanInvoke() {
		writeError(w, http.StatusBadRequest, "function is not active, current status: "+string(fn.Status))
		return
	}

	// 检查方法是否允许 (如果设置了方法限制)
	if len(fn.HTTPMethods) > 0 {
		allowed := false
		for _, m := range fn.HTTPMethods {
			if strings.EqualFold(m, method) {
				allowed = true
				break
			}
		}
		if !allowed {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed for this route")
			return
		}
	}

	// 读取请求体作为函数输入
	var payload json.RawMessage
	if r.Body != nil {
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			payload = json.RawMessage(body)
		}
	}
	if payload == nil {
		payload = json.RawMessage("{}")
	}

	// 同步执行函数
	req := &domain.InvokeRequest{
		FunctionID: fn.ID,
		Payload:    payload,
		Async:      false,
	}

	resp, err := h.scheduler.Invoke(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 尝试解析 Lambda 样式的响应格式 (含 statusCode 和 body)
	var lambdaResp struct {
		StatusCode int               `json:"statusCode"`
		Headers    map[string]string `json:"headers"`
		Body       json.RawMessage   `json:"body"`
	}

	if err := json.Unmarshal(resp.Body, &lambdaResp); err == nil && lambdaResp.StatusCode != 0 {
		for k, v := range lambdaResp.Headers {
			w.Header().Set(k, v)
		}
		writeJSON(w, lambdaResp.StatusCode, lambdaResp.Body)
		return
	}

	// 默认返回原样响应
	writeJSON(w, resp.StatusCode, resp.Body)
}

// ========== 日志辅助方法 ==========

// logInfo 记录信息级别日志
func (h *Handler) logInfo(r *http.Request, method, message string, fields logrus.Fields) {
	if h.logger == nil {
		return
	}
	entry := h.logger.WithFields(logrus.Fields{
		"method":     method,
		"path":       r.URL.Path,
		"remote_ip":  r.RemoteAddr,
		"request_id": middleware.GetReqID(r.Context()),
	})
	if fields != nil {
		entry = entry.WithFields(fields)
	}
	entry.Info(message)
}

// logDebug 记录调试级别日志
func (h *Handler) logDebug(r *http.Request, method, message string, fields logrus.Fields) {
	if h.logger == nil {
		return
	}
	entry := h.logger.WithFields(logrus.Fields{
		"method":     method,
		"path":       r.URL.Path,
		"remote_ip":  r.RemoteAddr,
		"request_id": middleware.GetReqID(r.Context()),
	})
	if fields != nil {
		entry = entry.WithFields(fields)
	}
	entry.Debug(message)
}

// logWarn 记录警告级别日志
func (h *Handler) logWarn(r *http.Request, method, message string, fields logrus.Fields) {
	if h.logger == nil {
		return
	}
	entry := h.logger.WithFields(logrus.Fields{
		"method":     method,
		"path":       r.URL.Path,
		"remote_ip":  r.RemoteAddr,
		"request_id": middleware.GetReqID(r.Context()),
	})
	if fields != nil {
		entry = entry.WithFields(fields)
	}
	entry.Warn(message)
}

// logError 记录错误级别日志
func (h *Handler) logError(r *http.Request, method, message string, err error, fields logrus.Fields) {
	if h.logger == nil {
		return
	}
	entry := h.logger.WithFields(logrus.Fields{
		"method":     method,
		"path":       r.URL.Path,
		"remote_ip":  r.RemoteAddr,
		"request_id": middleware.GetReqID(r.Context()),
		"stack":      getStackTrace(1),
	})
	if fields != nil {
		entry = entry.WithFields(fields)
	}
	if err != nil {
		entry = entry.WithError(err)
	}
	entry.Error(message)
}

// ==================== 函数版本管理处理器 ====================

// ListFunctionVersions 获取函数的所有版本。
// HTTP端点: GET /api/v1/functions/{id}/versions
func (h *Handler) ListFunctionVersions(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")
	if idOrName == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id or name required")
		return
	}

	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	versions, err := h.store.ListFunctionVersions(fn.ID)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to list versions: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"versions": versions,
		"total":    len(versions),
	})
}

// GetFunctionVersion 获取函数的指定版本。
// HTTP端点: GET /api/v1/functions/{id}/versions/{version}
func (h *Handler) GetFunctionVersion(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")
	versionStr := chi.URLParam(r, "version")

	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid version number")
		return
	}

	v, err := h.store.GetFunctionVersion(fn.ID, version)
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "version not found")
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get version: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, v)
}

// RollbackFunction 回滚函数到指定版本。
// HTTP端点: POST /api/v1/functions/{id}/versions/{version}/rollback
func (h *Handler) RollbackFunction(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")
	versionStr := chi.URLParam(r, "version")

	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid version number")
		return
	}

	v, err := h.store.GetFunctionVersion(fn.ID, version)
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "version not found")
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get version: "+err.Error())
		return
	}

	// 更新函数到目标版本
	fn.Handler = v.Handler
	fn.Code = v.Code
	fn.Binary = v.Binary
	fn.CodeHash = v.CodeHash

	if err := h.store.UpdateFunction(fn); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to rollback function: "+err.Error())
		return
	}

	// 同步定时任务
	if h.cronManager != nil {
		h.cronManager.AddOrUpdateFunction(fn)
	}

	h.logInfo(r, "RollbackFunction", "函数回滚成功", logrus.Fields{"function": fn.Name, "version": version})
	writeJSON(w, http.StatusOK, fn)
}

// ==================== 函数别名管理处理器 ====================

// ListFunctionAliases 获取函数的所有别名。
// HTTP端点: GET /api/v1/functions/{id}/aliases
func (h *Handler) ListFunctionAliases(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")

	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	aliases, err := h.store.ListFunctionAliases(fn.ID)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to list aliases: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"aliases": aliases,
		"total":   len(aliases),
	})
}

// CreateFunctionAlias 创建函数别名。
// HTTP端点: POST /api/v1/functions/{id}/aliases
func (h *Handler) CreateFunctionAlias(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")

	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	var req domain.CreateAliasRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Name == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "alias name is required")
		return
	}

	// 验证权重总和
	totalWeight := 0
	for _, w := range req.RoutingConfig.Weights {
		totalWeight += w.Weight
	}
	if totalWeight != 100 {
		writeErrorWithContext(w, r, http.StatusBadRequest, "routing weights must sum to 100")
		return
	}

	alias := &domain.FunctionAlias{
		FunctionID:    fn.ID,
		Name:          req.Name,
		Description:   req.Description,
		RoutingConfig: req.RoutingConfig,
	}

	if err := h.store.CreateFunctionAlias(alias); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create alias: "+err.Error())
		return
	}

	h.logInfo(r, "CreateFunctionAlias", "别名创建成功", logrus.Fields{"function": fn.Name, "alias": req.Name})
	writeJSON(w, http.StatusCreated, alias)
}

// UpdateFunctionAlias 更新函数别名。
// HTTP端点: PUT /api/v1/functions/{id}/aliases/{name}
func (h *Handler) UpdateFunctionAlias(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")
	aliasName := chi.URLParam(r, "name")

	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	alias, err := h.store.GetFunctionAlias(fn.ID, aliasName)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "alias not found")
		return
	}

	var req domain.UpdateAliasRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Description != nil {
		alias.Description = *req.Description
	}
	if req.RoutingConfig != nil {
		// 验证权重总和
		totalWeight := 0
		for _, w := range req.RoutingConfig.Weights {
			totalWeight += w.Weight
		}
		if totalWeight != 100 {
			writeErrorWithContext(w, r, http.StatusBadRequest, "routing weights must sum to 100")
			return
		}
		alias.RoutingConfig = *req.RoutingConfig
	}

	if err := h.store.UpdateFunctionAlias(alias); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to update alias: "+err.Error())
		return
	}

	h.logInfo(r, "UpdateFunctionAlias", "别名更新成功", logrus.Fields{"function": fn.Name, "alias": aliasName})
	writeJSON(w, http.StatusOK, alias)
}

// DeleteFunctionAlias 删除函数别名。
// HTTP端点: DELETE /api/v1/functions/{id}/aliases/{name}
func (h *Handler) DeleteFunctionAlias(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")
	aliasName := chi.URLParam(r, "name")

	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	if err := h.store.DeleteFunctionAlias(fn.ID, aliasName); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to delete alias: "+err.Error())
		return
	}

	h.logInfo(r, "DeleteFunctionAlias", "别名删除成功", logrus.Fields{"function": fn.Name, "alias": aliasName})
	w.WriteHeader(http.StatusNoContent)
}

// ==================== 函数层管理处理器 ====================

// ListLayers 获取所有层。
// HTTP端点: GET /api/v1/layers
func (h *Handler) ListLayers(w http.ResponseWriter, r *http.Request) {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	layers, total, err := h.store.ListLayers(offset, limit)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to list layers: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"layers": layers,
		"total":  total,
		"offset": offset,
		"limit":  limit,
	})
}

// CreateLayer 创建层。
// HTTP端点: POST /api/v1/layers
func (h *Handler) CreateLayer(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateLayerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Name == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "layer name is required")
		return
	}
	if len(req.CompatibleRuntimes) == 0 {
		writeErrorWithContext(w, r, http.StatusBadRequest, "at least one compatible runtime is required")
		return
	}

	// 检查是否存在同名层
	existing, _ := h.store.GetLayerByName(req.Name)
	if existing != nil {
		writeErrorWithContext(w, r, http.StatusConflict, "layer with this name already exists")
		return
	}

	layer := &domain.Layer{
		Name:               req.Name,
		Description:        req.Description,
		CompatibleRuntimes: req.CompatibleRuntimes,
		LatestVersion:      0,
	}

	if err := h.store.CreateLayer(layer); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create layer: "+err.Error())
		return
	}

	h.logInfo(r, "CreateLayer", "层创建成功", logrus.Fields{"layer": req.Name})
	writeJSON(w, http.StatusCreated, layer)
}

// GetLayer 获取层详情。
// HTTP端点: GET /api/v1/layers/{id}
func (h *Handler) GetLayer(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")

	layer, err := h.store.GetLayerByID(idOrName)
	if err != nil {
		layer, err = h.store.GetLayerByName(idOrName)
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "layer not found")
		return
	}

	// 获取版本列表
	versions, err := h.store.ListLayerVersions(layer.ID)
	if err != nil {
		versions = []*domain.LayerVersion{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"layer":    layer,
		"versions": versions,
	})
}

// DeleteLayer 删除层。
// HTTP端点: DELETE /api/v1/layers/{id}
func (h *Handler) DeleteLayer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.store.DeleteLayer(id); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to delete layer: "+err.Error())
		return
	}

	h.logInfo(r, "DeleteLayer", "层删除成功", logrus.Fields{"layer_id": id})
	w.WriteHeader(http.StatusNoContent)
}

// CreateLayerVersion 创建层版本。
// HTTP端点: POST /api/v1/layers/{id}/versions
func (h *Handler) CreateLayerVersion(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")

	layer, err := h.store.GetLayerByID(idOrName)
	if err != nil {
		layer, err = h.store.GetLayerByName(idOrName)
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "layer not found")
		return
	}

	// 解析 multipart form
	if err := r.ParseMultipartForm(100 << 20); err != nil { // 100MB max
		writeErrorWithContext(w, r, http.StatusBadRequest, "failed to parse form: "+err.Error())
		return
	}

	file, _, err := r.FormFile("content")
	if err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "content file is required")
		return
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to read content: "+err.Error())
		return
	}

	// 计算哈希
	hash := sha256.Sum256(content)
	contentHash := hex.EncodeToString(hash[:])

	// 创建新版本
	newVersion := layer.LatestVersion + 1
	lv := &domain.LayerVersion{
		LayerID:     layer.ID,
		Version:     newVersion,
		ContentHash: contentHash,
		SizeBytes:   int64(len(content)),
	}

	if err := h.store.CreateLayerVersion(lv, content); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create layer version: "+err.Error())
		return
	}

	// 更新层的最新版本号
	layer.LatestVersion = newVersion
	if err := h.store.UpdateLayer(layer); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to update layer: "+err.Error())
		return
	}

	h.logInfo(r, "CreateLayerVersion", "层版本创建成功", logrus.Fields{"layer": layer.Name, "version": newVersion})
	writeJSON(w, http.StatusCreated, lv)
}

// GetFunctionLayers 获取函数的层。
// HTTP端点: GET /api/v1/functions/{id}/layers
func (h *Handler) GetFunctionLayers(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")

	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	layers, err := h.store.GetFunctionLayers(fn.ID)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function layers: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"layers": layers,
		"total":  len(layers),
	})
}

// SetFunctionLayers 设置函数的层。
// HTTP端点: PUT /api/v1/functions/{id}/layers
func (h *Handler) SetFunctionLayers(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")

	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	var layers []domain.FunctionLayer
	if err := json.NewDecoder(r.Body).Decode(&layers); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// 设置顺序
	for i := range layers {
		layers[i].Order = i
	}

	if err := h.store.SetFunctionLayers(fn.ID, layers); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to set function layers: "+err.Error())
		return
	}

	h.logInfo(r, "SetFunctionLayers", "函数层设置成功", logrus.Fields{"function": fn.Name, "layer_count": len(layers)})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"layers": layers,
		"total":  len(layers),
	})
}

// ==================== 环境管理处理器 ====================

// ListEnvironments 获取所有环境。
// HTTP端点: GET /api/v1/environments
func (h *Handler) ListEnvironments(w http.ResponseWriter, r *http.Request) {
	envs, err := h.store.ListEnvironments()
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to list environments: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"environments": envs,
		"total":        len(envs),
	})
}

// CreateEnvironment 创建环境。
// HTTP端点: POST /api/v1/environments
func (h *Handler) CreateEnvironment(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateEnvironmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Name == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "environment name is required")
		return
	}

	// 检查是否存在同名环境
	existing, _ := h.store.GetEnvironmentByName(req.Name)
	if existing != nil {
		writeErrorWithContext(w, r, http.StatusConflict, "environment with this name already exists")
		return
	}

	env := &domain.Environment{
		Name:        req.Name,
		Description: req.Description,
		IsDefault:   req.IsDefault,
	}

	if err := h.store.CreateEnvironment(env); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create environment: "+err.Error())
		return
	}

	h.logInfo(r, "CreateEnvironment", "环境创建成功", logrus.Fields{"environment": req.Name})
	writeJSON(w, http.StatusCreated, env)
}

// DeleteEnvironment 删除环境。
// HTTP端点: DELETE /api/v1/environments/{id}
func (h *Handler) DeleteEnvironment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.store.DeleteEnvironment(id); err != nil {
		if err.Error() == "cannot delete default environment" {
			writeErrorWithContext(w, r, http.StatusBadRequest, err.Error())
			return
		}
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to delete environment: "+err.Error())
		return
	}

	h.logInfo(r, "DeleteEnvironment", "环境删除成功", logrus.Fields{"environment_id": id})
	w.WriteHeader(http.StatusNoContent)
}

// GetFunctionEnvConfigs 获取函数在所有环境下的配置。
// HTTP端点: GET /api/v1/functions/{id}/environments
func (h *Handler) GetFunctionEnvConfigs(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")

	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	configs, err := h.store.ListFunctionEnvConfigs(fn.ID)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to list env configs: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"configs": configs,
		"total":   len(configs),
	})
}

// UpdateFunctionEnvConfig 更新函数在特定环境下的配置。
// HTTP端点: PUT /api/v1/functions/{id}/environments/{env}
func (h *Handler) UpdateFunctionEnvConfig(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")
	envName := chi.URLParam(r, "env")

	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	env, err := h.store.GetEnvironmentByName(envName)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "environment not found")
		return
	}

	var req domain.UpdateFunctionEnvConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	cfg := &domain.FunctionEnvConfig{
		FunctionID:    fn.ID,
		EnvironmentID: env.ID,
	}

	if req.EnvVars != nil {
		cfg.EnvVars = *req.EnvVars
	}
	cfg.MemoryMB = req.MemoryMB
	cfg.TimeoutSec = req.TimeoutSec
	if req.ActiveAlias != nil {
		cfg.ActiveAlias = *req.ActiveAlias
	}

	if err := h.store.UpsertFunctionEnvConfig(cfg); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to update env config: "+err.Error())
		return
	}

	// 重新获取配置以包含环境名称
	cfg, _ = h.store.GetFunctionEnvConfig(fn.ID, env.ID)

	h.logInfo(r, "UpdateFunctionEnvConfig", "函数环境配置更新成功", logrus.Fields{"function": fn.Name, "environment": envName})
	writeJSON(w, http.StatusOK, cfg)
}

// ==================== 函数状态管理处理器 ====================

// GetFunctionTask 获取函数任务状态。
// HTTP端点: GET /api/v1/tasks/{id}
func (h *Handler) GetFunctionTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "task id required")
		return
	}

	task, err := h.store.GetFunctionTask(taskID)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "task not found")
		return
	}

	// 获取关联的函数信息
	fn, _ := h.store.GetFunctionByID(task.FunctionID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"task":     task,
		"function": fn,
	})
}

// OfflineFunction 将函数设置为下线状态。
// HTTP端点: POST /api/v1/functions/{id}/offline
//
// 功能说明：
//   - 将函数状态从 active 改为 offline
//   - 下线后的函数不能被调用
//   - 可以通过 OnlineFunction 恢复
func (h *Handler) OfflineFunction(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")
	if idOrName == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id or name required")
		return
	}

	h.logInfo(r, "OfflineFunction", "开始下线函数", logrus.Fields{"function": idOrName})

	// 查找函数
	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found: "+idOrName)
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	// 检查函数状态是否可以下线
	if !fn.Status.CanOffline() {
		h.logWarn(r, "OfflineFunction", "函数状态不允许下线", logrus.Fields{
			"function": fn.Name,
			"status":   fn.Status,
		})
		writeErrorWithContext(w, r, http.StatusBadRequest, "function cannot be offlined in current status: "+string(fn.Status))
		return
	}

	// 更新函数状态为 offline
	if err := h.store.UpdateFunctionStatus(fn.ID, domain.FunctionStatusOffline, "函数已下线", ""); err != nil {
		h.logError(r, "OfflineFunction", "更新函数状态失败", err, logrus.Fields{"function": fn.Name})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to offline function: "+err.Error())
		return
	}

	// 移除定时任务
	if h.cronManager != nil && fn.CronExpression != "" {
		h.cronManager.RemoveFunction(fn.ID)
		h.logDebug(r, "OfflineFunction", "移除定时任务", logrus.Fields{"function": fn.Name})
	}

	// 重新获取函数
	fn, _ = h.store.GetFunctionByID(fn.ID)

	h.logInfo(r, "OfflineFunction", "函数下线成功", logrus.Fields{"function": fn.Name, "id": fn.ID})
	writeJSON(w, http.StatusOK, fn)
}

// OnlineFunction 将函数设置为上线状态。
// HTTP端点: POST /api/v1/functions/{id}/online
//
// 功能说明：
//   - 将函数状态从 offline 改为 active
//   - 上线后的函数可以被调用
func (h *Handler) OnlineFunction(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")
	if idOrName == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id or name required")
		return
	}

	h.logInfo(r, "OnlineFunction", "开始上线函数", logrus.Fields{"function": idOrName})

	// 查找函数
	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found: "+idOrName)
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	// 检查函数状态是否可以上线
	if !fn.Status.CanOnline() {
		h.logWarn(r, "OnlineFunction", "函数状态不允许上线", logrus.Fields{
			"function": fn.Name,
			"status":   fn.Status,
		})
		writeErrorWithContext(w, r, http.StatusBadRequest, "function cannot be onlined in current status: "+string(fn.Status))
		return
	}

	// 更新函数状态为 active
	if err := h.store.UpdateFunctionStatus(fn.ID, domain.FunctionStatusActive, "", ""); err != nil {
		h.logError(r, "OnlineFunction", "更新函数状态失败", err, logrus.Fields{"function": fn.Name})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to online function: "+err.Error())
		return
	}

	// 恢复定时任务
	if h.cronManager != nil && fn.CronExpression != "" {
		fn.Status = domain.FunctionStatusActive // 临时设置状态以便 cronManager 使用
		h.cronManager.AddOrUpdateFunction(fn)
		h.logDebug(r, "OnlineFunction", "恢复定时任务", logrus.Fields{"function": fn.Name, "cron": fn.CronExpression})
	}

	// 重新获取函数
	fn, _ = h.store.GetFunctionByID(fn.ID)

	h.logInfo(r, "OnlineFunction", "函数上线成功", logrus.Fields{"function": fn.Name, "id": fn.ID})
	writeJSON(w, http.StatusOK, fn)
}

// RecompileFunction 重新编译函数。
// HTTP端点: POST /api/v1/functions/{id}/recompile
//
// 功能说明：
//   - 触发函数的重新编译
//   - 仅支持源代码类型的函数（Go/Rust/WASM）
//   - 函数必须处于 active 或 failed 状态
func (h *Handler) RecompileFunction(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")
	if idOrName == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id or name required")
		return
	}

	h.logInfo(r, "RecompileFunction", "开始重新编译函数", logrus.Fields{"function": idOrName})

	// 查找函数
	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found: "+idOrName)
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	// 检查函数是否需要编译
	if !compiler.IsSourceCode(string(fn.Runtime), fn.Code) {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function does not require compilation (not a source code runtime)")
		return
	}

	// 检查函数状态是否允许重新编译
	if fn.Status != domain.FunctionStatusActive && fn.Status != domain.FunctionStatusFailed {
		h.logWarn(r, "RecompileFunction", "函数状态不允许重新编译", logrus.Fields{
			"function": fn.Name,
			"status":   fn.Status,
		})
		writeErrorWithContext(w, r, http.StatusBadRequest, "function can only be recompiled when status is active or failed, current status: "+string(fn.Status))
		return
	}

	// 生成新的任务ID
	taskID := uuid.New().String()

	// 更新函数状态为 building
	if err := h.store.UpdateFunctionStatus(fn.ID, domain.FunctionStatusBuilding, "正在重新编译", taskID); err != nil {
		h.logError(r, "RecompileFunction", "更新函数状态失败", err, logrus.Fields{"function": fn.Name})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to update function status: "+err.Error())
		return
	}

	// 创建异步任务
	task := &domain.FunctionTask{
		ID:         taskID,
		FunctionID: fn.ID,
		Type:       domain.FunctionTaskUpdate,
		Status:     domain.FunctionTaskPending,
	}
	if err := h.store.CreateFunctionTask(task); err != nil {
		h.logError(r, "RecompileFunction", "创建任务失败", err, logrus.Fields{"function": fn.Name})
		// 恢复函数状态
		h.store.UpdateFunctionStatus(fn.ID, domain.FunctionStatusFailed, "创建编译任务失败", "")
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create task: "+err.Error())
		return
	}

	// 异步执行编译
	go h.processCreateFunctionTask(fn.ID, taskID)

	h.logInfo(r, "RecompileFunction", "重新编译任务已提交", logrus.Fields{"function": fn.Name, "id": fn.ID, "task_id": taskID})

	// 重新获取函数
	fn, _ = h.store.GetFunctionByID(fn.ID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"function": fn,
		"task_id":  taskID,
		"message":  "重新编译任务已提交",
	})
}

// PinFunction 将函数置顶/取消置顶。
// HTTP端点: POST /api/v1/functions/{id}/pin
//
// 功能说明：
//   - 切换函数的置顶状态
//   - 置顶的函数会在列表中优先显示
func (h *Handler) PinFunction(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")
	if idOrName == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id or name required")
		return
	}

	h.logInfo(r, "PinFunction", "切换函数置顶状态", logrus.Fields{"function": idOrName})

	// 查找函数
	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found: "+idOrName)
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	// 切换置顶状态
	newPinned := !fn.Pinned
	if err := h.store.UpdateFunctionPin(fn.ID, newPinned); err != nil {
		h.logError(r, "PinFunction", "更新函数置顶状态失败", err, logrus.Fields{"function": fn.Name})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to update pin status: "+err.Error())
		return
	}

	// 重新获取函数
	fn, _ = h.store.GetFunctionByID(fn.ID)

	action := "置顶"
	if !newPinned {
		action = "取消置顶"
	}
	h.logInfo(r, "PinFunction", "函数"+action+"成功", logrus.Fields{"function": fn.Name, "id": fn.ID, "pinned": newPinned})
	writeJSON(w, http.StatusOK, fn)
}

// ExportFunction 导出函数配置为JSON格式。
// HTTP端点: GET /api/v1/functions/{id}/export
//
// 功能说明：
//   - 导出函数的完整配置（不包括二进制）
//   - 可用于备份或迁移
func (h *Handler) ExportFunction(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")
	if idOrName == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id or name required")
		return
	}

	h.logInfo(r, "ExportFunction", "导出函数配置", logrus.Fields{"function": idOrName})

	// 查找函数
	fn, err := h.store.GetFunctionByID(idOrName)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(idOrName)
	}
	if err == domain.ErrFunctionNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found: "+idOrName)
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	// 创建导出数据结构
	export := map[string]interface{}{
		"name":            fn.Name,
		"description":     fn.Description,
		"tags":            fn.Tags,
		"runtime":         fn.Runtime,
		"handler":         fn.Handler,
		"code":            fn.Code,
		"memory_mb":       fn.MemoryMB,
		"timeout_sec":     fn.TimeoutSec,
		"env_vars":        fn.EnvVars,
		"cron_expression": fn.CronExpression,
		"http_path":       fn.HTTPPath,
		"http_methods":    fn.HTTPMethods,
		"exported_at":     time.Now().Format(time.RFC3339),
		"version":         fn.Version,
	}

	// 设置下载头
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.json", fn.Name))
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, http.StatusOK, export)
}

// ImportFunction 从JSON配置导入函数。
// HTTP端点: POST /api/v1/functions/import
//
// 功能说明：
//   - 从JSON配置创建新函数
//   - 如果名称已存在，会提示错误
func (h *Handler) ImportFunction(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetReqID(r.Context())
	h.logInfo(r, "ImportFunction", "导入函数配置", logrus.Fields{"request_id": requestID})

	var req struct {
		Name           string            `json:"name"`
		Description    string            `json:"description"`
		Tags           []string          `json:"tags"`
		Runtime        domain.Runtime    `json:"runtime"`
		Handler        string            `json:"handler"`
		Code           string            `json:"code"`
		MemoryMB       int               `json:"memory_mb"`
		TimeoutSec     int               `json:"timeout_sec"`
		MaxConcurrency int               `json:"max_concurrency"`
		EnvVars        map[string]string `json:"env_vars"`
		CronExpression string            `json:"cron_expression"`
		HTTPPath       string            `json:"http_path"`
		HTTPMethods    []string          `json:"http_methods"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// 验证必填字段
	if req.Name == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "name is required")
		return
	}
	if !req.Runtime.IsValid() {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid runtime: "+string(req.Runtime))
		return
	}
	if req.Handler == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "handler is required")
		return
	}
	if req.Code == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "code is required")
		return
	}

	// 检查名称是否已存在
	if _, err := h.store.GetFunctionByName(req.Name); err == nil {
		writeErrorWithContext(w, r, http.StatusConflict, "function with this name already exists: "+req.Name)
		return
	}

	// 设置默认值
	if req.MemoryMB == 0 {
		req.MemoryMB = 256
	}
	if req.TimeoutSec == 0 {
		req.TimeoutSec = 30
	}

	// 创建函数
	now := time.Now()
	taskID := fmt.Sprintf("task-%s", requestID)
	fn := &domain.Function{
		ID:             uuid.New().String(),
		Name:           req.Name,
		Description:    req.Description,
		Tags:           req.Tags,
		Runtime:        req.Runtime,
		Handler:        req.Handler,
		Code:           req.Code,
		MemoryMB:       req.MemoryMB,
		TimeoutSec:     req.TimeoutSec,
		MaxConcurrency: req.MaxConcurrency,
		EnvVars:        req.EnvVars,
		CronExpression: req.CronExpression,
		HTTPPath:       req.HTTPPath,
		HTTPMethods:    req.HTTPMethods,
		Status:         domain.FunctionStatusCreating,
		StatusMessage:  "函数正在创建中（导入）",
		TaskID:         taskID,
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	// 保存函数
	if err := h.store.CreateFunction(fn); err != nil {
		h.logError(r, "ImportFunction", "创建函数失败", err, logrus.Fields{"name": req.Name})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create function: "+err.Error())
		return
	}

	// 创建任务记录
	task := &domain.FunctionTask{
		ID:         taskID,
		FunctionID: fn.ID,
		Type:       domain.FunctionTaskCreate,
		Status:     domain.FunctionTaskPending,
		CreatedAt:  now,
	}
	if err := h.store.CreateFunctionTask(task); err != nil {
		h.logError(r, "ImportFunction", "创建任务记录失败", err, nil)
	}

	// 异步处理函数创建
	go h.processCreateFunctionTask(fn.ID, taskID)

	h.logInfo(r, "ImportFunction", "函数导入成功", logrus.Fields{"function": fn.Name, "id": fn.ID})
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"function": fn,
		"task_id":  taskID,
		"message":  "函数导入任务已开始",
	})
}

// ==================== 死信队列 (DLQ) 处理器 ====================

// ListDLQMessages 获取死信消息列表。
// HTTP端点: GET /api/v1/dlq
//
// 功能说明：
//   - 查询死信队列中的消息列表
//   - 支持按函数ID和状态过滤
//   - 支持分页
//
// 查询参数：
//   - function_id: 函数ID过滤（可选）
//   - status: 状态过滤（可选，pending/retrying/resolved/discarded）
//   - offset: 偏移量（默认0）
//   - limit: 每页数量（默认20，最大100）
func (h *Handler) ListDLQMessages(w http.ResponseWriter, r *http.Request) {
	functionID := r.URL.Query().Get("function_id")
	status := r.URL.Query().Get("status")
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	messages, total, err := h.store.ListDLQMessages(functionID, status, offset, limit)
	if err != nil {
		h.logError(r, "ListDLQMessages", "查询死信消息失败", err, nil)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to list DLQ messages: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"messages": messages,
		"total":    total,
		"offset":   offset,
		"limit":    limit,
	})
}

// GetDLQMessage 获取死信消息详情。
// HTTP端点: GET /api/v1/dlq/{id}
func (h *Handler) GetDLQMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "message id required")
		return
	}

	msg, err := h.store.GetDLQMessage(id)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "message not found")
		return
	}

	writeJSON(w, http.StatusOK, msg)
}

// RetryDLQMessage 重试死信消息。
// HTTP端点: POST /api/v1/dlq/{id}/retry
//
// 功能说明：
//   - 使用原始载荷重新调用函数
//   - 更新重试计数和状态
func (h *Handler) RetryDLQMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "message id required")
		return
	}

	h.logInfo(r, "RetryDLQMessage", "重试死信消息", logrus.Fields{"message_id": id})

	// 获取死信消息
	msg, err := h.store.GetDLQMessage(id)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "message not found")
		return
	}

	// 检查状态
	if msg.Status == domain.DLQStatusResolved || msg.Status == domain.DLQStatusDiscarded {
		writeErrorWithContext(w, r, http.StatusBadRequest, "message already resolved or discarded")
		return
	}

	// 获取函数
	fn, err := h.store.GetFunctionByID(msg.FunctionID)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found (may have been deleted)")
		return
	}

	// 检查函数状态
	if !fn.Status.CanInvoke() {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function is not active: "+string(fn.Status))
		return
	}

	// 更新状态为重试中
	msg.Status = domain.DLQStatusRetrying
	now := time.Now()
	msg.LastRetryAt = &now
	msg.RetryCount++
	h.store.UpdateDLQMessage(msg)

	// 执行调用
	req := &domain.InvokeRequest{
		FunctionID: fn.ID,
		Payload:    msg.Payload,
	}

	resp, err := h.scheduler.Invoke(req)
	if err != nil {
		// 重试失败
		msg.Status = domain.DLQStatusPending
		msg.Error = err.Error()
		h.store.UpdateDLQMessage(msg)

		h.logError(r, "RetryDLQMessage", "重试失败", err, logrus.Fields{"message_id": id})
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":     false,
			"message":     msg,
			"retry_error": err.Error(),
		})
		return
	}

	// 重试成功
	msg.Status = domain.DLQStatusResolved
	resolvedAt := time.Now()
	msg.ResolvedAt = &resolvedAt
	h.store.UpdateDLQMessage(msg)

	h.logInfo(r, "RetryDLQMessage", "重试成功", logrus.Fields{"message_id": id})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"message":  msg,
		"response": resp,
	})
}

// DiscardDLQMessage 丢弃死信消息。
// HTTP端点: POST /api/v1/dlq/{id}/discard
func (h *Handler) DiscardDLQMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "message id required")
		return
	}

	msg, err := h.store.GetDLQMessage(id)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "message not found")
		return
	}

	msg.Status = domain.DLQStatusDiscarded
	resolvedAt := time.Now()
	msg.ResolvedAt = &resolvedAt

	if err := h.store.UpdateDLQMessage(msg); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to discard message: "+err.Error())
		return
	}

	h.logInfo(r, "DiscardDLQMessage", "死信消息已丢弃", logrus.Fields{"message_id": id})
	writeJSON(w, http.StatusOK, msg)
}

// DeleteDLQMessage 删除死信消息。
// HTTP端点: DELETE /api/v1/dlq/{id}
func (h *Handler) DeleteDLQMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "message id required")
		return
	}

	if err := h.store.DeleteDLQMessage(id); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to delete message: "+err.Error())
		return
	}

	h.logInfo(r, "DeleteDLQMessage", "死信消息已删除", logrus.Fields{"message_id": id})
	w.WriteHeader(http.StatusNoContent)
}

// PurgeDLQMessages 清空死信队列。
// HTTP端点: DELETE /api/v1/dlq
//
// 查询参数：
//   - function_id: 只清空指定函数的消息（可选）
func (h *Handler) PurgeDLQMessages(w http.ResponseWriter, r *http.Request) {
	functionID := r.URL.Query().Get("function_id")

	count, err := h.store.PurgeDLQMessages(functionID)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to purge DLQ: "+err.Error())
		return
	}

	h.logInfo(r, "PurgeDLQMessages", "死信队列已清空", logrus.Fields{"function_id": functionID, "count": count})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"deleted": count,
	})
}

// GetDLQStats 获取死信队列统计信息。
// HTTP端点: GET /api/v1/dlq/stats
func (h *Handler) GetDLQStats(w http.ResponseWriter, r *http.Request) {
	functionID := r.URL.Query().Get("function_id")

	total, err := h.store.CountDLQMessages(functionID)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get DLQ stats: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total":       total,
		"function_id": functionID,
	})
}

// ==================== 系统设置与保留策略处理器 ====================

// ListSystemSettings 获取所有系统设置。
// HTTP端点: GET /api/v1/settings
func (h *Handler) ListSystemSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.store.ListSystemSettings()
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to list settings: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"settings": settings,
	})
}

// GetSystemSetting 获取单个系统设置。
// HTTP端点: GET /api/v1/settings/{key}
func (h *Handler) GetSystemSetting(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if key == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "setting key required")
		return
	}

	setting, err := h.store.GetSystemSetting(key)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "setting not found")
		return
	}

	writeJSON(w, http.StatusOK, setting)
}

// UpdateSystemSetting 更新系统设置。
// HTTP端点: PUT /api/v1/settings/{key}
func (h *Handler) UpdateSystemSetting(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if key == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "setting key required")
		return
	}

	var req struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if err := h.store.SetSystemSetting(key, req.Value); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to update setting: "+err.Error())
		return
	}

	h.logInfo(r, "UpdateSystemSetting", "系统设置已更新", logrus.Fields{"key": key, "value": req.Value})

	setting, _ := h.store.GetSystemSetting(key)
	writeJSON(w, http.StatusOK, setting)
}

// GetRetentionStats 获取保留策略统计信息。
// HTTP端点: GET /api/v1/retention/stats
func (h *Handler) GetRetentionStats(w http.ResponseWriter, r *http.Request) {
	// 获取保留天数设置
	logRetentionDays := 30 // 默认值
	dlqRetentionDays := 90 // 默认值

	if setting, err := h.store.GetSystemSetting("log_retention_days"); err == nil {
		if days, err := strconv.Atoi(setting.Value); err == nil && days > 0 {
			logRetentionDays = days
		}
	}
	if setting, err := h.store.GetSystemSetting("dlq_retention_days"); err == nil {
		if days, err := strconv.Atoi(setting.Value); err == nil && days > 0 {
			dlqRetentionDays = days
		}
	}

	stats, err := h.store.GetRetentionStats(logRetentionDays, dlqRetentionDays)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get retention stats: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// RunRetentionCleanup 执行保留策略清理。
// HTTP端点: POST /api/v1/retention/cleanup
func (h *Handler) RunRetentionCleanup(w http.ResponseWriter, r *http.Request) {
	h.logInfo(r, "RunRetentionCleanup", "开始执行保留策略清理", nil)

	// 获取保留天数设置
	logRetentionDays := 30 // 默认值
	dlqRetentionDays := 90 // 默认值

	if setting, err := h.store.GetSystemSetting("log_retention_days"); err == nil {
		if days, err := strconv.Atoi(setting.Value); err == nil && days > 0 {
			logRetentionDays = days
		}
	}
	if setting, err := h.store.GetSystemSetting("dlq_retention_days"); err == nil {
		if days, err := strconv.Atoi(setting.Value); err == nil && days > 0 {
			dlqRetentionDays = days
		}
	}

	// 清理调用记录
	invocationsDeleted, err := h.store.CleanupOldInvocations(logRetentionDays)
	if err != nil {
		h.logError(r, "RunRetentionCleanup", "清理调用记录失败", err, nil)
	}

	// 清理死信队列
	dlqDeleted, err := h.store.CleanupOldDLQMessages(dlqRetentionDays)
	if err != nil {
		h.logError(r, "RunRetentionCleanup", "清理死信队列失败", err, nil)
	}

	// 清理任务记录
	tasksDeleted, err := h.store.CleanupOldTasks(logRetentionDays)
	if err != nil {
		h.logError(r, "RunRetentionCleanup", "清理任务记录失败", err, nil)
	}

	h.logInfo(r, "RunRetentionCleanup", "保留策略清理完成", logrus.Fields{
		"invocations_deleted": invocationsDeleted,
		"dlq_deleted":         dlqDeleted,
		"tasks_deleted":       tasksDeleted,
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"invocations_deleted": invocationsDeleted,
		"dlq_deleted":         dlqDeleted,
		"tasks_deleted":       tasksDeleted,
		"log_retention_days":  logRetentionDays,
		"dlq_retention_days":  dlqRetentionDays,
	})
}

// ==================== 审计日志处理器 ====================

// auditLog 记录审计日志的辅助方法
func (h *Handler) auditLog(r *http.Request, action, resourceType, resourceID, resourceName string, details map[string]interface{}) {
	log := &storage.AuditLog{
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		ResourceName: resourceName,
		ActorIP:      r.RemoteAddr,
		Details:      details,
	}

	// 尝试从请求头获取 actor (API Key 名称或用户名)
	if apiKey := r.Header.Get("X-API-Key"); apiKey != "" {
		log.Actor = "api-key:" + apiKey[:8] + "..."
	} else {
		log.Actor = "anonymous"
	}

	if err := h.store.CreateAuditLog(log); err != nil {
		h.logger.WithError(err).Warn("审计日志记录失败")
	}
}

// ListAuditLogs 获取审计日志列表。
// HTTP端点: GET /api/v1/audit
//
// 查询参数：
//   - action: 操作类型过滤（可选）
//   - resource_type: 资源类型过滤（可选）
//   - resource_id: 资源ID过滤（可选）
//   - offset: 偏移量（默认0）
//   - limit: 每页数量（默认20，最大100）
func (h *Handler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	resourceType := r.URL.Query().Get("resource_type")
	resourceID := r.URL.Query().Get("resource_id")
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	logs, total, err := h.store.ListAuditLogs(action, resourceType, resourceID, offset, limit)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to list audit logs: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"logs":   logs,
		"total":  total,
		"offset": offset,
		"limit":  limit,
	})
}

// GetAuditLogActions 获取所有审计操作类型。
// HTTP端点: GET /api/v1/audit/actions
func (h *Handler) GetAuditLogActions(w http.ResponseWriter, r *http.Request) {
	actions := []map[string]string{
		{"value": "function.create", "label": "创建函数"},
		{"value": "function.update", "label": "更新函数"},
		{"value": "function.delete", "label": "删除函数"},
		{"value": "function.clone", "label": "克隆函数"},
		{"value": "function.invoke", "label": "调用函数"},
		{"value": "function.offline", "label": "下线函数"},
		{"value": "function.online", "label": "上线函数"},
		{"value": "function.pin", "label": "置顶函数"},
		{"value": "function.export", "label": "导出函数"},
		{"value": "function.import", "label": "导入函数"},
		{"value": "function.rollback", "label": "回滚函数"},
		{"value": "alias.create", "label": "创建别名"},
		{"value": "alias.update", "label": "更新别名"},
		{"value": "alias.delete", "label": "删除别名"},
		{"value": "layer.create", "label": "创建层"},
		{"value": "layer.delete", "label": "删除层"},
		{"value": "environment.create", "label": "创建环境"},
		{"value": "environment.delete", "label": "删除环境"},
		{"value": "dlq.retry", "label": "重试死信"},
		{"value": "dlq.discard", "label": "丢弃死信"},
		{"value": "dlq.purge", "label": "清空死信队列"},
		{"value": "setting.update", "label": "更新设置"},
		{"value": "retention.cleanup", "label": "执行清理"},
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"actions": actions,
	})
}

// ==================== 配额管理处理器 ====================

// GetQuotaUsage 获取配额使用情况。
// HTTP端点: GET /api/v1/quota
func (h *Handler) GetQuotaUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := h.store.GetQuotaUsage()
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get quota usage: "+err.Error())
		return
	}

	// 计算使用百分比
	response := map[string]interface{}{
		"function_count":           usage.FunctionCount,
		"total_memory_mb":          usage.TotalMemoryMB,
		"today_invocations":        usage.TodayInvocations,
		"total_code_size_kb":       usage.TotalCodeSizeKB,
		"max_functions":            usage.MaxFunctions,
		"max_memory_mb":            usage.MaxMemoryMB,
		"max_invocations_per_day":  usage.MaxInvocationsPerDay,
		"max_code_size_kb":         usage.MaxCodeSizeKB,
		"function_usage_percent":   float64(usage.FunctionCount) / float64(usage.MaxFunctions) * 100,
		"memory_usage_percent":     float64(usage.TotalMemoryMB) / float64(usage.MaxMemoryMB) * 100,
		"invocation_usage_percent": float64(usage.TodayInvocations) / float64(usage.MaxInvocationsPerDay) * 100,
		"code_usage_percent":       float64(usage.TotalCodeSizeKB) / float64(usage.MaxCodeSizeKB) * 100,
	}

	writeJSON(w, http.StatusOK, response)
}

// ==================== Webhook 触发器处理器 ====================

// EnableWebhook 启用函数的 Webhook 触发器。
// HTTP端点: POST /api/v1/functions/{id}/webhook/enable
func (h *Handler) EnableWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// 获取函数
	fn, err := h.store.GetFunctionByID(id)
	if err != nil {
		if errors.Is(err, domain.ErrFunctionNotFound) {
			writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
			return
		}
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	// 如果没有 webhook key，生成一个
	if fn.WebhookKey == "" {
		fn.WebhookKey = generateWebhookKey()
	}
	fn.WebhookEnabled = true

	// 更新函数
	if err := h.store.UpdateFunction(fn); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to enable webhook: "+err.Error())
		return
	}

	// 记录审计日志
	h.auditLog(r, "webhook_enable", "function", fn.ID, fn.Name, map[string]interface{}{
		"webhook_key": fn.WebhookKey,
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":              fn.ID,
		"webhook_enabled": fn.WebhookEnabled,
		"webhook_key":     fn.WebhookKey,
		"webhook_url":     fmt.Sprintf("/webhook/%s", fn.WebhookKey),
	})
}

// DisableWebhook 禁用函数的 Webhook 触发器。
// HTTP端点: POST /api/v1/functions/{id}/webhook/disable
func (h *Handler) DisableWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// 获取函数
	fn, err := h.store.GetFunctionByID(id)
	if err != nil {
		if errors.Is(err, domain.ErrFunctionNotFound) {
			writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
			return
		}
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	fn.WebhookEnabled = false

	// 更新函数
	if err := h.store.UpdateFunction(fn); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to disable webhook: "+err.Error())
		return
	}

	// 记录审计日志
	h.auditLog(r, "webhook_disable", "function", fn.ID, fn.Name, nil)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":              fn.ID,
		"webhook_enabled": fn.WebhookEnabled,
	})
}

// RegenerateWebhookKey 重新生成函数的 Webhook 密钥。
// HTTP端点: POST /api/v1/functions/{id}/webhook/regenerate
func (h *Handler) RegenerateWebhookKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// 获取函数
	fn, err := h.store.GetFunctionByID(id)
	if err != nil {
		if errors.Is(err, domain.ErrFunctionNotFound) {
			writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
			return
		}
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	// 生成新的 webhook key
	oldKey := fn.WebhookKey
	fn.WebhookKey = generateWebhookKey()

	// 更新函数
	if err := h.store.UpdateFunction(fn); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to regenerate webhook key: "+err.Error())
		return
	}

	// 记录审计日志
	h.auditLog(r, "webhook_regenerate", "function", fn.ID, fn.Name, map[string]interface{}{
		"old_key": oldKey,
		"new_key": fn.WebhookKey,
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":              fn.ID,
		"webhook_enabled": fn.WebhookEnabled,
		"webhook_key":     fn.WebhookKey,
		"webhook_url":     fmt.Sprintf("/webhook/%s", fn.WebhookKey),
	})
}

// HandleWebhook 处理 Webhook 触发的函数调用。
// HTTP端点: POST /webhook/{key}
func (h *Handler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	webhookKey := chi.URLParam(r, "key")

	// 根据 webhook key 获取函数
	fn, err := h.store.GetFunctionByWebhookKey(webhookKey)
	if err != nil {
		if errors.Is(err, domain.ErrFunctionNotFound) {
			writeErrorWithContext(w, r, http.StatusNotFound, "webhook not found or disabled")
			return
		}
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get function: "+err.Error())
		return
	}

	// 检查函数状态
	if !fn.Status.CanInvoke() {
		writeErrorWithContext(w, r, http.StatusServiceUnavailable, fmt.Sprintf("function is not available (status: %s)", fn.Status))
		return
	}

	// 读取请求体作为 payload
	var payload interface{}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			// 如果不是 JSON，尝试读取为字符串
			payload = map[string]interface{}{
				"raw_body": err.Error(),
			}
		}
	}

	// 添加 webhook 元数据
	webhookPayload := map[string]interface{}{
		"source":      "webhook",
		"webhook_key": webhookKey,
		"method":      r.Method,
		"headers":     extractHeaders(r),
		"query":       r.URL.Query(),
		"body":        payload,
	}

	// 将 payload 转换为 JSON
	payloadBytes, _ := json.Marshal(webhookPayload)

	// 构建调用请求
	req := &domain.InvokeRequest{
		FunctionID: fn.ID,
		Payload:    payloadBytes,
		Async:      false,
	}

	// 通过调度器同步执行函数
	resp, err := h.scheduler.Invoke(req)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to invoke function: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// generateWebhookKey 生成唯一的 Webhook 密钥
func generateWebhookKey() string {
	return uuid.New().String()[:16] + uuid.New().String()[:16]
}

// extractHeaders 提取 HTTP 请求头
func extractHeaders(r *http.Request) map[string]string {
	headers := make(map[string]string)
	for key, values := range r.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	return headers
}

// ==================== 模板 API handlers ====================

// ListTemplates 处理获取模板列表的请求。
// HTTP端点: GET /api/v1/templates
//
// 功能说明：
//   - 支持分页查询，通过offset和limit参数控制
//   - 支持按分类和运行时筛选
//
// 查询参数：
//   - offset: 偏移量，跳过前N条记录（默认0）
//   - limit: 每页数量，范围1-100（默认20）
//   - category: 模板分类（可选）
//   - runtime: 运行时类型（可选）
func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	h.logDebug(r, "ListTemplates", "查询模板列表", nil)

	// 解析分页参数
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	category := r.URL.Query().Get("category")
	runtimeFilter := r.URL.Query().Get("runtime")

	// 设置limit的默认值和最大值限制
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	templates, total, err := h.store.ListTemplates(offset, limit, category, runtimeFilter)
	if err != nil {
		h.logError(r, "ListTemplates", "查询模板列表失败", err, nil)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to list templates: "+err.Error())
		return
	}

	h.logDebug(r, "ListTemplates", "查询成功", logrus.Fields{"count": len(templates), "total": total})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"templates": templates,
		"total":     total,
		"offset":    offset,
		"limit":     limit,
	})
}

// GetTemplate 处理获取单个模板详情的请求。
// HTTP端点: GET /api/v1/templates/{id}
//
// 路径参数：
//   - id: 模板的唯一标识符或名称
func (h *Handler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	idOrName := chi.URLParam(r, "id")
	if idOrName == "" {
		h.logError(r, "GetTemplate", "缺少模板标识", nil, nil)
		writeErrorWithContext(w, r, http.StatusBadRequest, "template id or name required")
		return
	}

	h.logDebug(r, "GetTemplate", "查询模板", logrus.Fields{"template": idOrName})

	// 首先尝试按ID查询
	template, err := h.store.GetTemplateByID(idOrName)
	if err == domain.ErrTemplateNotFound {
		// 如果按ID未找到，尝试按名称查询
		template, err = h.store.GetTemplateByName(idOrName)
	}
	if err == domain.ErrTemplateNotFound {
		h.logWarn(r, "GetTemplate", "模板不存在", logrus.Fields{"template": idOrName})
		writeErrorWithContext(w, r, http.StatusNotFound, "template not found: "+idOrName)
		return
	}
	if err != nil {
		h.logError(r, "GetTemplate", "查询模板失败", err, logrus.Fields{"template": idOrName})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get template: "+err.Error())
		return
	}

	h.logDebug(r, "GetTemplate", "查询成功", logrus.Fields{"template": template.Name, "id": template.ID})

	writeJSON(w, http.StatusOK, template)
}

// CreateTemplate 处理创建模板的请求。
// HTTP端点: POST /api/v1/templates
func (h *Handler) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetReqID(r.Context())
	h.logInfo(r, "CreateTemplate", "开始创建模板", logrus.Fields{"request_id": requestID})

	var req domain.CreateTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logError(r, "CreateTemplate", "解析请求体失败", err, nil)
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	h.logDebug(r, "CreateTemplate", "请求参数", logrus.Fields{
		"name":     req.Name,
		"category": req.Category,
		"runtime":  req.Runtime,
	})

	// 验证请求参数
	if err := req.Validate(); err != nil {
		h.logError(r, "CreateTemplate", "参数验证失败", err, logrus.Fields{"name": req.Name})
		writeErrorWithContext(w, r, http.StatusBadRequest, err.Error())
		return
	}

	// 检查是否存在同名模板
	existing, _ := h.store.GetTemplateByName(req.Name)
	if existing != nil {
		h.logWarn(r, "CreateTemplate", "模板名称已存在", logrus.Fields{"name": req.Name})
		writeErrorWithContext(w, r, http.StatusConflict, "template with this name already exists")
		return
	}

	// 构建模板对象
	template := &domain.Template{
		Name:           req.Name,
		DisplayName:    req.DisplayName,
		Description:    req.Description,
		Category:       req.Category,
		Runtime:        req.Runtime,
		Handler:        req.Handler,
		Code:           req.Code,
		Variables:      req.Variables,
		DefaultMemory:  req.DefaultMemory,
		DefaultTimeout: req.DefaultTimeout,
		Tags:           req.Tags,
		Icon:           req.Icon,
		Popular:        req.Popular,
	}

	// 保存模板
	if err := h.store.CreateTemplate(template); err != nil {
		h.logError(r, "CreateTemplate", "保存模板失败", err, logrus.Fields{"name": req.Name})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create template: "+err.Error())
		return
	}

	// 记录审计日志
	h.auditLog(r, "template_create", "template", template.ID, template.Name, map[string]interface{}{
		"category": template.Category,
		"runtime":  template.Runtime,
	})

	h.logInfo(r, "CreateTemplate", "模板创建成功", logrus.Fields{"name": template.Name, "id": template.ID})

	writeJSON(w, http.StatusCreated, template)
}

// UpdateTemplate 处理更新模板的请求。
// HTTP端点: PUT /api/v1/templates/{id}
func (h *Handler) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "template id required")
		return
	}

	h.logDebug(r, "UpdateTemplate", "更新模板", logrus.Fields{"id": id})

	// 获取现有模板
	template, err := h.store.GetTemplateByID(id)
	if err == domain.ErrTemplateNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "template not found: "+id)
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get template: "+err.Error())
		return
	}

	// 解析更新请求
	var req domain.UpdateTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// 应用更新
	if req.DisplayName != nil {
		template.DisplayName = *req.DisplayName
	}
	if req.Description != nil {
		template.Description = *req.Description
	}
	if req.Category != nil {
		template.Category = *req.Category
	}
	if req.Handler != nil {
		template.Handler = *req.Handler
	}
	if req.Code != nil {
		template.Code = *req.Code
	}
	if req.Variables != nil {
		template.Variables = *req.Variables
	}
	if req.DefaultMemory != nil {
		template.DefaultMemory = *req.DefaultMemory
	}
	if req.DefaultTimeout != nil {
		template.DefaultTimeout = *req.DefaultTimeout
	}
	if req.Tags != nil {
		template.Tags = *req.Tags
	}
	if req.Icon != nil {
		template.Icon = *req.Icon
	}
	if req.Popular != nil {
		template.Popular = *req.Popular
	}

	// 保存更新
	if err := h.store.UpdateTemplate(template); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to update template: "+err.Error())
		return
	}

	// 记录审计日志
	h.auditLog(r, "template_update", "template", template.ID, template.Name, nil)

	h.logInfo(r, "UpdateTemplate", "模板更新成功", logrus.Fields{"name": template.Name, "id": template.ID})

	writeJSON(w, http.StatusOK, template)
}

// DeleteTemplate 处理删除模板的请求。
// HTTP端点: DELETE /api/v1/templates/{id}
func (h *Handler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "template id required")
		return
	}

	h.logDebug(r, "DeleteTemplate", "删除模板", logrus.Fields{"id": id})

	// 获取模板信息用于审计日志
	template, err := h.store.GetTemplateByID(id)
	if err == domain.ErrTemplateNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "template not found: "+id)
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get template: "+err.Error())
		return
	}

	// 删除模板
	if err := h.store.DeleteTemplate(id); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to delete template: "+err.Error())
		return
	}

	// 记录审计日志
	h.auditLog(r, "template_delete", "template", template.ID, template.Name, nil)

	h.logInfo(r, "DeleteTemplate", "模板删除成功", logrus.Fields{"id": id, "name": template.Name})

	w.WriteHeader(http.StatusNoContent)
}

// CreateFunctionFromTemplate 从模板创建函数。
// HTTP端点: POST /api/v1/functions/from-template
func (h *Handler) CreateFunctionFromTemplate(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetReqID(r.Context())
	h.logInfo(r, "CreateFunctionFromTemplate", "从模板创建函数", logrus.Fields{"request_id": requestID})

	var req domain.CreateFunctionFromTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logError(r, "CreateFunctionFromTemplate", "解析请求体失败", err, nil)
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// 验证请求参数
	if err := req.Validate(); err != nil {
		h.logError(r, "CreateFunctionFromTemplate", "参数验证失败", err, nil)
		writeErrorWithContext(w, r, http.StatusBadRequest, err.Error())
		return
	}

	// 获取模板
	template, err := h.store.GetTemplateByID(req.TemplateID)
	if err == domain.ErrTemplateNotFound {
		writeErrorWithContext(w, r, http.StatusNotFound, "template not found: "+req.TemplateID)
		return
	}
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get template: "+err.Error())
		return
	}

	// 检查是否存在同名函数
	existing, _ := h.store.GetFunctionByName(req.FunctionName)
	if existing != nil {
		writeErrorWithContext(w, r, http.StatusConflict, "function with this name already exists")
		return
	}

	// 替换模板变量
	code := template.Code
	for _, v := range template.Variables {
		placeholder := "{{" + v.Name + "}}"
		value := v.Default
		if val, ok := req.Variables[v.Name]; ok {
			value = val
		}
		code = strings.ReplaceAll(code, placeholder, value)
	}

	// 设置内存和超时
	memoryMB := template.DefaultMemory
	if req.MemoryMB > 0 {
		memoryMB = req.MemoryMB
	}
	timeoutSec := template.DefaultTimeout
	if req.TimeoutSec > 0 {
		timeoutSec = req.TimeoutSec
	}

	// 计算代码哈希
	hash := sha256.Sum256([]byte(code))
	codeHash := hex.EncodeToString(hash[:])

	// 生成任务ID
	taskID := uuid.New().String()

	// 构建函数对象
	fn := &domain.Function{
		Name:          req.FunctionName,
		Description:   req.Description,
		Runtime:       template.Runtime,
		Handler:       template.Handler,
		Code:          code,
		CodeHash:      codeHash,
		MemoryMB:      memoryMB,
		TimeoutSec:    timeoutSec,
		EnvVars:       req.EnvVars,
		Status:        domain.FunctionStatusCreating,
		StatusMessage: "函数正在创建中",
		TaskID:        taskID,
		Version:       1,
	}

	// 保存函数
	if err := h.store.CreateFunction(fn); err != nil {
		h.logError(r, "CreateFunctionFromTemplate", "保存函数失败", err, logrus.Fields{"name": req.FunctionName})
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create function: "+err.Error())
		return
	}

	// 创建异步任务
	taskInput, _ := json.Marshal(map[string]interface{}{
		"template_id":   template.ID,
		"function_name": req.FunctionName,
	})
	task := &domain.FunctionTask{
		ID:         taskID,
		FunctionID: fn.ID,
		Type:       domain.FunctionTaskCreate,
		Status:     domain.FunctionTaskPending,
		Input:      taskInput,
	}
	if err := h.store.CreateFunctionTask(task); err != nil {
		h.logError(r, "CreateFunctionFromTemplate", "创建任务失败", err, logrus.Fields{"name": req.FunctionName})
		h.store.DeleteFunction(fn.ID)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create task: "+err.Error())
		return
	}

	// 异步处理任务
	go h.processCreateFunctionTask(fn.ID, taskID)

	// 记录审计日志
	h.auditLog(r, "function_create_from_template", "function", fn.ID, fn.Name, map[string]interface{}{
		"template_id":   template.ID,
		"template_name": template.Name,
	})

	h.logInfo(r, "CreateFunctionFromTemplate", "函数创建任务已提交", logrus.Fields{
		"name":        fn.Name,
		"id":          fn.ID,
		"task_id":     taskID,
		"template_id": template.ID,
	})

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"function": fn,
		"task_id":  taskID,
		"message":  "函数正在创建中，请通过任务ID查询进度",
	})
}
