// Package api 提供 Phase 3 高级特性的 HTTP API 处理程序
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/oriys/nimbus/internal/domain"
	"github.com/sirupsen/logrus"
)

// ==================== 告警规则管理 ====================

// ListAlertRules 获取告警规则列表
// GET /api/v1/alerts/rules
func (h *Handler) ListAlertRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.store.ListAlertRules()
	if err != nil {
		h.logError(r, "ListAlertRules", "获取告警规则失败", err, nil)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to list alert rules")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"rules": rules,
		"total": len(rules),
	})
}

// CreateAlertRule 创建告警规则
// POST /api/v1/alerts/rules
func (h *Handler) CreateAlertRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string                   `json:"name"`
		Description string                   `json:"description"`
		FunctionID  string                   `json:"function_id"`
		Condition   domain.AlertConditionType `json:"condition"`
		Operator    string                   `json:"operator"`
		Threshold   float64                  `json:"threshold"`
		Duration    string                   `json:"duration"`
		Severity    domain.AlertSeverity     `json:"severity"`
		Channels    []string                 `json:"channels"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "name is required")
		return
	}

	rule := &domain.AlertRule{
		ID:          uuid.New().String(),
		Name:        req.Name,
		Description: req.Description,
		FunctionID:  req.FunctionID,
		Condition:   req.Condition,
		Operator:    req.Operator,
		Threshold:   req.Threshold,
		Duration:    req.Duration,
		Severity:    req.Severity,
		Enabled:     true,
		Channels:    req.Channels,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := h.store.CreateAlertRule(rule); err != nil {
		h.logError(r, "CreateAlertRule", "创建告警规则失败", err, nil)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create alert rule")
		return
	}

	h.auditLog(r, "alert_rule.create", "alert_rule", rule.ID, rule.Name, nil)
	writeJSON(w, http.StatusCreated, rule)
}

// GetAlertRule 获取告警规则详情
// GET /api/v1/alerts/rules/{id}
func (h *Handler) GetAlertRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rule, err := h.store.GetAlertRule(id)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "alert rule not found")
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// UpdateAlertRule 更新告警规则
// PUT /api/v1/alerts/rules/{id}
func (h *Handler) UpdateAlertRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	rule, err := h.store.GetAlertRule(id)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "alert rule not found")
		return
	}

	var req struct {
		Name        *string                   `json:"name"`
		Description *string                   `json:"description"`
		Condition   *domain.AlertConditionType `json:"condition"`
		Operator    *string                   `json:"operator"`
		Threshold   *float64                  `json:"threshold"`
		Duration    *string                   `json:"duration"`
		Severity    *domain.AlertSeverity     `json:"severity"`
		Enabled     *bool                     `json:"enabled"`
		Channels    []string                  `json:"channels"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name != nil {
		rule.Name = *req.Name
	}
	if req.Description != nil {
		rule.Description = *req.Description
	}
	if req.Condition != nil {
		rule.Condition = *req.Condition
	}
	if req.Operator != nil {
		rule.Operator = *req.Operator
	}
	if req.Threshold != nil {
		rule.Threshold = *req.Threshold
	}
	if req.Duration != nil {
		rule.Duration = *req.Duration
	}
	if req.Severity != nil {
		rule.Severity = *req.Severity
	}
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}
	if req.Channels != nil {
		rule.Channels = req.Channels
	}
	rule.UpdatedAt = time.Now()

	if err := h.store.UpdateAlertRule(rule); err != nil {
		h.logError(r, "UpdateAlertRule", "更新告警规则失败", err, nil)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to update alert rule")
		return
	}

	h.auditLog(r, "alert_rule.update", "alert_rule", rule.ID, rule.Name, nil)
	writeJSON(w, http.StatusOK, rule)
}

// DeleteAlertRule 删除告警规则
// DELETE /api/v1/alerts/rules/{id}
func (h *Handler) DeleteAlertRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	rule, err := h.store.GetAlertRule(id)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "alert rule not found")
		return
	}

	if err := h.store.DeleteAlertRule(id); err != nil {
		h.logError(r, "DeleteAlertRule", "删除告警规则失败", err, nil)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to delete alert rule")
		return
	}

	h.auditLog(r, "alert_rule.delete", "alert_rule", id, rule.Name, nil)
	writeJSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

// ListAlerts 获取告警列表
// GET /api/v1/alerts
func (h *Handler) ListAlerts(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	functionID := r.URL.Query().Get("function_id")

	alerts, err := h.store.ListAlerts(status, functionID)
	if err != nil {
		h.logError(r, "ListAlerts", "获取告警列表失败", err, nil)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to list alerts")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"alerts": alerts,
		"total":  len(alerts),
	})
}

// ResolveAlert 解决告警
// POST /api/v1/alerts/{id}/resolve
func (h *Handler) ResolveAlert(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.store.ResolveAlert(id); err != nil {
		h.logError(r, "ResolveAlert", "解决告警失败", err, nil)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to resolve alert")
		return
	}

	h.auditLog(r, "alert.resolve", "alert", id, "", nil)
	writeJSON(w, http.StatusOK, map[string]string{"message": "resolved"})
}

// ==================== 通知渠道管理 ====================

// ListNotificationChannels 获取通知渠道列表
// GET /api/v1/alerts/channels
func (h *Handler) ListNotificationChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := h.store.ListNotificationChannels()
	if err != nil {
		h.logError(r, "ListNotificationChannels", "获取通知渠道失败", err, nil)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to list notification channels")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"channels": channels,
		"total":    len(channels),
	})
}

// CreateNotificationChannel 创建通知渠道
// POST /api/v1/alerts/channels
func (h *Handler) CreateNotificationChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string                       `json:"name"`
		Type   domain.NotificationChannelType `json:"type"`
		Config map[string]string            `json:"config"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	channel := &domain.NotificationChannel{
		ID:        uuid.New().String(),
		Name:      req.Name,
		Type:      req.Type,
		Config:    req.Config,
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := h.store.CreateNotificationChannel(channel); err != nil {
		h.logError(r, "CreateNotificationChannel", "创建通知渠道失败", err, nil)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to create notification channel")
		return
	}

	h.auditLog(r, "notification_channel.create", "notification_channel", channel.ID, channel.Name, nil)
	writeJSON(w, http.StatusCreated, channel)
}

// DeleteNotificationChannel 删除通知渠道
// DELETE /api/v1/alerts/channels/{id}
func (h *Handler) DeleteNotificationChannel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.store.DeleteNotificationChannel(id); err != nil {
		h.logError(r, "DeleteNotificationChannel", "删除通知渠道失败", err, nil)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to delete notification channel")
		return
	}

	h.auditLog(r, "notification_channel.delete", "notification_channel", id, "", nil)
	writeJSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

// ==================== 函数预热管理 ====================

// GetWarmingStatus 获取函数预热状态
// GET /api/v1/functions/{id}/warming
func (h *Handler) GetWarmingStatus(w http.ResponseWriter, r *http.Request) {
	functionID := chi.URLParam(r, "id")

	fn, err := h.store.GetFunctionByID(functionID)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}

	// 获取预热策略
	policy, _ := h.store.GetWarmingPolicy(functionID)

	status := &domain.WarmingStatus{
		FunctionID:    functionID,
		FunctionName:  fn.Name,
		WarmInstances: 0,
		BusyInstances: 0,
		ColdStartRate: 0,
		Policy:        policy,
	}

	writeJSON(w, http.StatusOK, status)
}

// UpdateWarmingPolicy 更新预热策略
// PUT /api/v1/functions/{id}/warming
func (h *Handler) UpdateWarmingPolicy(w http.ResponseWriter, r *http.Request) {
	functionID := chi.URLParam(r, "id")

	_, err := h.store.GetFunctionByID(functionID)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}

	var req struct {
		Enabled      bool   `json:"enabled"`
		MinInstances int    `json:"min_instances"`
		MaxInstances int    `json:"max_instances"`
		Schedule     string `json:"schedule"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	policy := &domain.WarmingPolicy{
		ID:           uuid.New().String(),
		FunctionID:   functionID,
		Enabled:      req.Enabled,
		MinInstances: req.MinInstances,
		MaxInstances: req.MaxInstances,
		Schedule:     req.Schedule,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := h.store.SaveWarmingPolicy(policy); err != nil {
		h.logError(r, "UpdateWarmingPolicy", "保存预热策略失败", err, nil)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to save warming policy")
		return
	}

	h.auditLog(r, "warming_policy.update", "function", functionID, "", nil)
	writeJSON(w, http.StatusOK, policy)
}

// TriggerWarming 触发函数预热
// POST /api/v1/functions/{id}/warm
func (h *Handler) TriggerWarming(w http.ResponseWriter, r *http.Request) {
	functionID := chi.URLParam(r, "id")

	_, err := h.store.GetFunctionByID(functionID)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}

	var req struct {
		Instances int `json:"instances"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Instances = 1 // 默认预热 1 个实例
	}

	h.logInfo(r, "TriggerWarming", "触发函数预热", logrus.Fields{
		"function_id": functionID,
		"instances":   req.Instances,
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":   "warming triggered",
		"instances": req.Instances,
	})
}

// ==================== 依赖分析 ====================

// GetFunctionDependencies 获取函数依赖关系
// GET /api/v1/functions/{id}/dependencies
func (h *Handler) GetFunctionDependencies(w http.ResponseWriter, r *http.Request) {
	functionID := chi.URLParam(r, "id")

	fn, err := h.store.GetFunctionByID(functionID)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}

	// 获取该函数调用的其他函数
	callsTo, err := h.store.GetFunctionCallsTo(functionID)
	if err != nil {
		callsTo = []domain.FunctionDependency{}
	}

	// 获取调用该函数的其他函数
	calledBy, err := h.store.GetFunctionCalledBy(functionID)
	if err != nil {
		calledBy = []domain.FunctionDependency{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"function_id":   functionID,
		"function_name": fn.Name,
		"calls_to":      callsTo,
		"called_by":     calledBy,
	})
}

// GetDependencyGraph 获取依赖关系图
// GET /api/v1/dependencies/graph
func (h *Handler) GetDependencyGraph(w http.ResponseWriter, r *http.Request) {
	// 获取所有函数
	functions, _, err := h.store.ListFunctions(0, 1000)
	if err != nil {
		h.logError(r, "GetDependencyGraph", "获取函数列表失败", err, nil)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get functions")
		return
	}

	// 构建节点
	nodes := make([]domain.DependencyNode, 0, len(functions))
	for _, fn := range functions {
		nodes = append(nodes, domain.DependencyNode{
			ID:      fn.ID,
			Name:    fn.Name,
			Type:    "function",
			Runtime: string(fn.Runtime),
			Status:  string(fn.Status),
		})
	}

	// 获取所有依赖边
	edges, err := h.store.GetAllDependencyEdges()
	if err != nil {
		edges = []domain.DependencyEdge{}
	}

	graph := &domain.DependencyGraph{
		Nodes: nodes,
		Edges: edges,
	}

	writeJSON(w, http.StatusOK, graph)
}

// AddDependency 添加依赖关系
// POST /api/v1/dependencies
func (h *Handler) AddDependency(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceID string `json:"source_id"`
		TargetID string `json:"target_id"`
		Type     string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	// 验证函数存在
	if _, err := h.store.GetFunctionByID(req.SourceID); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "source function not found")
		return
	}
	if _, err := h.store.GetFunctionByID(req.TargetID); err != nil {
		writeErrorWithContext(w, r, http.StatusBadRequest, "target function not found")
		return
	}

	depType := domain.DependencyTypeDirectCall
	if req.Type != "" {
		depType = domain.DependencyType(req.Type)
	}

	if err := h.store.AddFunctionDependency(req.SourceID, req.TargetID, depType); err != nil {
		h.logError(r, "AddDependency", "添加依赖失败", err, nil)
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to add dependency")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

// GetImpactAnalysis 获取影响分析
// GET /api/v1/functions/{id}/impact
func (h *Handler) GetImpactAnalysis(w http.ResponseWriter, r *http.Request) {
	functionID := chi.URLParam(r, "id")

	fn, err := h.store.GetFunctionByID(functionID)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}

	// 获取直接依赖此函数的函数
	directDeps, _ := h.store.GetFunctionCalledBy(functionID)
	directNodes := make([]domain.DependencyNode, 0, len(directDeps))
	for _, dep := range directDeps {
		directNodes = append(directNodes, domain.DependencyNode{
			ID:   dep.SourceID,
			Name: dep.SourceName,
			Type: "function",
		})
	}

	// 获取间接依赖（递归）- 简化实现，只获取一层
	indirectNodes := []domain.DependencyNode{}

	// 获取受影响的工作流
	affectedWorkflows, _ := h.store.GetWorkflowsUsingFunction(functionID)
	if affectedWorkflows == nil {
		affectedWorkflows = []string{}
	}

	analysis := &domain.ImpactAnalysis{
		FunctionID:         functionID,
		FunctionName:       fn.Name,
		DirectDependents:   directNodes,
		IndirectDependents: indirectNodes,
		AffectedWorkflows:  affectedWorkflows,
		TotalImpactCount:   len(directNodes) + len(indirectNodes) + len(affectedWorkflows),
	}

	writeJSON(w, http.StatusOK, analysis)
}
