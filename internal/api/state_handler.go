// Package api 提供 HTTP API 处理器。
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/oriys/nimbus/internal/scheduler"
	"github.com/oriys/nimbus/internal/state"
)

// StateHandler 状态管理 API 处理器
type StateHandler struct {
	handler       *Handler
	stateHandler  *state.Handler
	sessionRouter *scheduler.SessionRouter
}

// NewStateHandler 创建状态处理器
func NewStateHandler(h *Handler, stateHandler *state.Handler, sessionRouter *scheduler.SessionRouter) *StateHandler {
	return &StateHandler{
		handler:       h,
		stateHandler:  stateHandler,
		sessionRouter: sessionRouter,
	}
}

// RegisterRoutes 注册状态相关路由
func (sh *StateHandler) RegisterRoutes(r chi.Router) {
	// 会话管理路由
	r.Route("/functions/{id}/sessions", func(r chi.Router) {
		r.Get("/", sh.ListSessions)
		r.Get("/{sessionKey}", sh.GetSession)
		r.Delete("/{sessionKey}", sh.DeleteSession)
	})

	// 状态管理路由
	r.Route("/functions/{id}/state", func(r chi.Router) {
		r.Get("/{sessionKey}", sh.GetSessionState)
		r.Delete("/{sessionKey}", sh.DeleteSessionState)
		r.Delete("/{sessionKey}/{key}", sh.DeleteStateKey)
	})
}

// ListSessions 列出函数的所有会话
// GET /api/v1/functions/{id}/sessions?limit=20
func (sh *StateHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	functionID := chi.URLParam(r, "id")
	if functionID == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id required")
		return
	}

	// 获取 limit 参数
	limit := 20
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsedLimit := parseInt(limitStr, 20); parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	sessions, err := sh.sessionRouter.ListSessions(r.Context(), functionID, limit)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to list sessions: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sessions": sessions,
		"total":    len(sessions),
	})
}

// GetSession 获取会话详情
// GET /api/v1/functions/{id}/sessions/{sessionKey}
func (sh *StateHandler) GetSession(w http.ResponseWriter, r *http.Request) {
	functionID := chi.URLParam(r, "id")
	sessionKey := chi.URLParam(r, "sessionKey")

	if functionID == "" || sessionKey == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id and session key required")
		return
	}

	session, err := sh.sessionRouter.GetSessionInfo(r.Context(), functionID, sessionKey)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "session not found")
		return
	}

	// 获取状态 key 信息
	if sh.stateHandler != nil {
		keyInfos, totalSize, err := sh.stateHandler.GetSessionState(r.Context(), functionID, sessionKey)
		if err == nil {
			session.TotalSize = totalSize
			for _, ki := range keyInfos {
				session.StateKeys = append(session.StateKeys, ki.Key)
			}
		}
	}

	writeJSON(w, http.StatusOK, session)
}

// DeleteSession 删除会话
// DELETE /api/v1/functions/{id}/sessions/{sessionKey}
func (sh *StateHandler) DeleteSession(w http.ResponseWriter, r *http.Request) {
	functionID := chi.URLParam(r, "id")
	sessionKey := chi.URLParam(r, "sessionKey")

	if functionID == "" || sessionKey == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id and session key required")
		return
	}

	// 删除会话绑定
	if err := sh.sessionRouter.UnbindSession(r.Context(), functionID, sessionKey); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to unbind session: "+err.Error())
		return
	}

	// 删除会话状态
	if sh.stateHandler != nil {
		if err := sh.stateHandler.DeleteSessionState(r.Context(), functionID, sessionKey); err != nil {
			sh.handler.logger.WithError(err).Warn("Failed to delete session state")
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetSessionState 获取会话的所有状态
// GET /api/v1/functions/{id}/state/{sessionKey}
func (sh *StateHandler) GetSessionState(w http.ResponseWriter, r *http.Request) {
	functionID := chi.URLParam(r, "id")
	sessionKey := chi.URLParam(r, "sessionKey")

	if functionID == "" || sessionKey == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id and session key required")
		return
	}

	if sh.stateHandler == nil {
		writeErrorWithContext(w, r, http.StatusServiceUnavailable, "state handler not configured")
		return
	}

	keyInfos, totalSize, err := sh.stateHandler.GetSessionState(r.Context(), functionID, sessionKey)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get session state: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"session_key": sessionKey,
		"keys":        keyInfos,
		"total_size":  totalSize,
	})
}

// DeleteSessionState 删除会话的所有状态
// DELETE /api/v1/functions/{id}/state/{sessionKey}
func (sh *StateHandler) DeleteSessionState(w http.ResponseWriter, r *http.Request) {
	functionID := chi.URLParam(r, "id")
	sessionKey := chi.URLParam(r, "sessionKey")

	if functionID == "" || sessionKey == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id and session key required")
		return
	}

	if sh.stateHandler == nil {
		writeErrorWithContext(w, r, http.StatusServiceUnavailable, "state handler not configured")
		return
	}

	if err := sh.stateHandler.DeleteSessionState(r.Context(), functionID, sessionKey); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to delete session state: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// DeleteStateKey 删除指定的状态 key
// DELETE /api/v1/functions/{id}/state/{sessionKey}/{key}
func (sh *StateHandler) DeleteStateKey(w http.ResponseWriter, r *http.Request) {
	functionID := chi.URLParam(r, "id")
	sessionKey := chi.URLParam(r, "sessionKey")
	key := chi.URLParam(r, "key")

	if functionID == "" || sessionKey == "" || key == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id, session key and key required")
		return
	}

	if sh.stateHandler == nil {
		writeErrorWithContext(w, r, http.StatusServiceUnavailable, "state handler not configured")
		return
	}

	if err := sh.stateHandler.DeleteStateKey(r.Context(), functionID, sessionKey, key); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to delete state key: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// parseInt 解析整数，失败时返回默认值
func parseInt(s string, defaultVal int) int {
	var val int
	if err := json.Unmarshal([]byte(s), &val); err != nil {
		return defaultVal
	}
	return val
}
