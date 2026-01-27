// Package api 提供了函数即服务(FaaS)平台的HTTP API处理程序。
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/oriys/nimbus/internal/domain"
	"github.com/oriys/nimbus/internal/storage"
	"github.com/oriys/nimbus/internal/workflow"
	"github.com/sirupsen/logrus"
)

// WorkflowHandler 工作流 API 处理器
type WorkflowHandler struct {
	store  *storage.PostgresStore
	engine *workflow.Engine
	logger *logrus.Logger
}

// NewWorkflowHandler 创建工作流处理器实例
func NewWorkflowHandler(store *storage.PostgresStore, engine *workflow.Engine, logger *logrus.Logger) *WorkflowHandler {
	return &WorkflowHandler{
		store:  store,
		engine: engine,
		logger: logger,
	}
}

// CreateWorkflow 创建工作流
// POST /api/v1/workflows
func (h *WorkflowHandler) CreateWorkflow(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// 验证请求
	if err := req.Validate(); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error(), err)
		return
	}

	// 检查名称是否已存在
	if existing, _ := h.store.GetWorkflowByName(req.Name); existing != nil {
		h.writeError(w, http.StatusConflict, "workflow already exists", domain.ErrWorkflowExists)
		return
	}

	// 创建工作流
	wf := &domain.Workflow{
		ID:          uuid.New().String(),
		Name:        req.Name,
		Description: req.Description,
		Version:     1,
		Status:      domain.WorkflowStatusActive,
		Definition:  req.Definition,
		TimeoutSec:  req.TimeoutSec,
	}

	if err := h.store.CreateWorkflow(wf); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to create workflow", err)
		return
	}

	h.logger.WithFields(logrus.Fields{
		"workflow_id":   wf.ID,
		"workflow_name": wf.Name,
	}).Info("Workflow created")

	h.writeJSON(w, http.StatusCreated, wf)
}

// ListWorkflows 列出工作流
// GET /api/v1/workflows
func (h *WorkflowHandler) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	offset, limit := h.getPagination(r)

	workflows, total, err := h.store.ListWorkflows(offset, limit)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list workflows", err)
		return
	}

	h.writeJSON(w, http.StatusOK, &domain.WorkflowListResponse{
		Workflows: workflows,
		Total:     total,
		Offset:    offset,
		Limit:     limit,
	})
}

// GetWorkflow 获取工作流详情
// GET /api/v1/workflows/{id}
func (h *WorkflowHandler) GetWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	wf, err := h.store.GetWorkflowByID(id)
	if err != nil {
		if err == domain.ErrWorkflowNotFound {
			h.writeError(w, http.StatusNotFound, "workflow not found", err)
		} else {
			h.writeError(w, http.StatusInternalServerError, "failed to get workflow", err)
		}
		return
	}

	h.writeJSON(w, http.StatusOK, wf)
}

// UpdateWorkflow 更新工作流
// PUT /api/v1/workflows/{id}
func (h *WorkflowHandler) UpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// 获取现有工作流
	wf, err := h.store.GetWorkflowByID(id)
	if err != nil {
		if err == domain.ErrWorkflowNotFound {
			h.writeError(w, http.StatusNotFound, "workflow not found", err)
		} else {
			h.writeError(w, http.StatusInternalServerError, "failed to get workflow", err)
		}
		return
	}

	// 解析更新请求
	var req domain.UpdateWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// 应用更新
	if req.Description != nil {
		wf.Description = *req.Description
	}
	if req.Definition != nil {
		wf.Definition = *req.Definition
		wf.Version++ // 定义更新时增加版本号
	}
	if req.TimeoutSec != nil {
		wf.TimeoutSec = *req.TimeoutSec
	}
	if req.Status != nil {
		wf.Status = *req.Status
	}

	if err := h.store.UpdateWorkflow(wf); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to update workflow", err)
		return
	}

	h.logger.WithFields(logrus.Fields{
		"workflow_id":   wf.ID,
		"workflow_name": wf.Name,
		"version":       wf.Version,
	}).Info("Workflow updated")

	h.writeJSON(w, http.StatusOK, wf)
}

// DeleteWorkflow 删除工作流
// DELETE /api/v1/workflows/{id}
func (h *WorkflowHandler) DeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.store.DeleteWorkflow(id); err != nil {
		if err == domain.ErrWorkflowNotFound {
			h.writeError(w, http.StatusNotFound, "workflow not found", err)
		} else {
			h.writeError(w, http.StatusInternalServerError, "failed to delete workflow", err)
		}
		return
	}

	h.logger.WithField("workflow_id", id).Info("Workflow deleted")

	w.WriteHeader(http.StatusNoContent)
}

// StartExecution 启动工作流执行
// POST /api/v1/workflows/{id}/executions
func (h *WorkflowHandler) StartExecution(w http.ResponseWriter, r *http.Request) {
	workflowID := chi.URLParam(r, "id")

	// 解析请求
	var req domain.StartExecutionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// 如果 body 为空，使用空输入
		req.Input = json.RawMessage("{}")
	}

	// 启动执行
	exec, err := h.engine.StartExecution(workflowID, req.Input)
	if err != nil {
		if err == domain.ErrWorkflowNotFound {
			h.writeError(w, http.StatusNotFound, "workflow not found", err)
		} else if err == domain.ErrWorkflowInactive {
			h.writeError(w, http.StatusBadRequest, "workflow is inactive", err)
		} else {
			h.writeError(w, http.StatusInternalServerError, "failed to start execution", err)
		}
		return
	}

	h.logger.WithFields(logrus.Fields{
		"execution_id": exec.ID,
		"workflow_id":  workflowID,
	}).Info("Execution started")

	h.writeJSON(w, http.StatusAccepted, exec)
}

// ListExecutions 列出工作流的执行实例
// GET /api/v1/workflows/{id}/executions
func (h *WorkflowHandler) ListExecutions(w http.ResponseWriter, r *http.Request) {
	workflowID := chi.URLParam(r, "id")
	offset, limit := h.getPagination(r)

	executions, total, err := h.store.ListExecutions(workflowID, offset, limit)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list executions", err)
		return
	}

	h.writeJSON(w, http.StatusOK, &domain.ExecutionListResponse{
		Executions: executions,
		Total:      total,
		Offset:     offset,
		Limit:      limit,
	})
}

// ListAllExecutions 列出所有执行实例
// GET /api/v1/executions
func (h *WorkflowHandler) ListAllExecutions(w http.ResponseWriter, r *http.Request) {
	offset, limit := h.getPagination(r)

	executions, total, err := h.store.ListAllExecutions(offset, limit)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list executions", err)
		return
	}

	h.writeJSON(w, http.StatusOK, &domain.ExecutionListResponse{
		Executions: executions,
		Total:      total,
		Offset:     offset,
		Limit:      limit,
	})
}

// GetExecution 获取执行详情
// GET /api/v1/executions/{id}
func (h *WorkflowHandler) GetExecution(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	exec, err := h.store.GetExecutionByID(id)
	if err != nil {
		if err == domain.ErrExecutionNotFound {
			h.writeError(w, http.StatusNotFound, "execution not found", err)
		} else {
			h.writeError(w, http.StatusInternalServerError, "failed to get execution", err)
		}
		return
	}

	h.writeJSON(w, http.StatusOK, exec)
}

// StopExecution 停止执行
// POST /api/v1/executions/{id}/stop
func (h *WorkflowHandler) StopExecution(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.engine.StopExecution(id); err != nil {
		if err == domain.ErrExecutionNotFound {
			h.writeError(w, http.StatusNotFound, "execution not found", err)
		} else if err == domain.ErrExecutionAlreadyComplete {
			h.writeError(w, http.StatusBadRequest, "execution already complete", err)
		} else {
			h.writeError(w, http.StatusInternalServerError, "failed to stop execution", err)
		}
		return
	}

	h.logger.WithField("execution_id", id).Info("Execution stopped")

	// 返回更新后的执行状态
	exec, _ := h.store.GetExecutionByID(id)
	h.writeJSON(w, http.StatusOK, exec)
}

// GetExecutionHistory 获取执行历史
// GET /api/v1/executions/{id}/history
func (h *WorkflowHandler) GetExecutionHistory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// 先检查执行是否存在
	exec, err := h.store.GetExecutionByID(id)
	if err != nil {
		if err == domain.ErrExecutionNotFound {
			h.writeError(w, http.StatusNotFound, "execution not found", err)
		} else {
			h.writeError(w, http.StatusInternalServerError, "failed to get execution", err)
		}
		return
	}

	// 获取状态执行历史
	history, err := h.store.ListStateExecutions(id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to get execution history", err)
		return
	}

	h.writeJSON(w, http.StatusOK, &domain.ExecutionResponse{
		WorkflowExecution: exec,
		History:           history,
	})
}

// getPagination 获取分页参数
func (h *WorkflowHandler) getPagination(r *http.Request) (offset, limit int) {
	offset = 0
	limit = 20

	if o := r.URL.Query().Get("offset"); o != "" {
		if val, err := strconv.Atoi(o); err == nil && val >= 0 {
			offset = val
		}
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}

	return
}

// writeJSON 写入 JSON 响应
func (h *WorkflowHandler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// writeError 写入错误响应
func (h *WorkflowHandler) writeError(w http.ResponseWriter, status int, message string, err error) {
	h.logger.WithError(err).WithField("status", status).Debug(message)
	h.writeJSON(w, status, map[string]string{
		"error":   message,
		"details": err.Error(),
	})
}

// SetBreakpoint 设置断点
// POST /api/v1/executions/{id}/breakpoints
func (h *WorkflowHandler) SetBreakpoint(w http.ResponseWriter, r *http.Request) {
	executionID := chi.URLParam(r, "id")

	// 验证执行存在
	_, err := h.store.GetExecutionByID(executionID)
	if err != nil {
		if err == domain.ErrExecutionNotFound {
			h.writeError(w, http.StatusNotFound, "execution not found", err)
		} else {
			h.writeError(w, http.StatusInternalServerError, "failed to get execution", err)
		}
		return
	}

	var req domain.SetBreakpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if req.BeforeState == "" {
		h.writeError(w, http.StatusBadRequest, "before_state is required", fmt.Errorf("before_state is required"))
		return
	}

	bp := &domain.Breakpoint{
		ID:          uuid.New().String(),
		ExecutionID: executionID,
		BeforeState: req.BeforeState,
		Enabled:     true,
	}

	if err := h.store.CreateBreakpoint(bp); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to create breakpoint", err)
		return
	}

	h.logger.WithFields(logrus.Fields{
		"execution_id": executionID,
		"before_state": req.BeforeState,
	}).Info("Breakpoint set")

	h.writeJSON(w, http.StatusCreated, bp)
}

// ListBreakpoints 列出断点
// GET /api/v1/executions/{id}/breakpoints
func (h *WorkflowHandler) ListBreakpoints(w http.ResponseWriter, r *http.Request) {
	executionID := chi.URLParam(r, "id")

	breakpoints, err := h.store.ListBreakpoints(executionID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list breakpoints", err)
		return
	}

	if breakpoints == nil {
		breakpoints = []*domain.Breakpoint{}
	}

	h.writeJSON(w, http.StatusOK, breakpoints)
}

// DeleteBreakpoint 删除断点
// DELETE /api/v1/executions/{id}/breakpoints/{state}
func (h *WorkflowHandler) DeleteBreakpoint(w http.ResponseWriter, r *http.Request) {
	executionID := chi.URLParam(r, "id")
	state := chi.URLParam(r, "state")

	if err := h.store.DeleteBreakpoint(executionID, state); err != nil {
		h.writeError(w, http.StatusNotFound, "breakpoint not found", err)
		return
	}

	h.logger.WithFields(logrus.Fields{
		"execution_id": executionID,
		"before_state": state,
	}).Info("Breakpoint deleted")

	w.WriteHeader(http.StatusNoContent)
}

// ResumeExecution 恢复暂停的执行
// POST /api/v1/executions/{id}/resume
func (h *WorkflowHandler) ResumeExecution(w http.ResponseWriter, r *http.Request) {
	executionID := chi.URLParam(r, "id")

	var req domain.ResumeExecutionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// 允许空 body
		req.Input = nil
	}

	var modifiedInput json.RawMessage
	if req.Input != nil {
		modifiedInput = *req.Input
	}

	if err := h.engine.ResumeExecution(executionID, modifiedInput); err != nil {
		if err == domain.ErrExecutionNotFound {
			h.writeError(w, http.StatusNotFound, "execution not found", err)
		} else {
			h.writeError(w, http.StatusBadRequest, err.Error(), err)
		}
		return
	}

	h.logger.WithField("execution_id", executionID).Info("Execution resumed")

	// 返回更新后的执行状态
	exec, _ := h.store.GetExecutionByID(executionID)
	h.writeJSON(w, http.StatusOK, exec)
}
