// Package api 提供 HTTP API 处理器。
package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/oriys/nimbus/internal/snapshot"
)

// SnapshotHandler 快照管理 API 处理器
type SnapshotHandler struct {
	handler     *Handler
	snapshotMgr *snapshot.Manager
}

// NewSnapshotHandler 创建快照处理器
func NewSnapshotHandler(h *Handler, mgr *snapshot.Manager) *SnapshotHandler {
	return &SnapshotHandler{
		handler:     h,
		snapshotMgr: mgr,
	}
}

// RegisterRoutes 注册快照相关路由
func (sh *SnapshotHandler) RegisterRoutes(r chi.Router) {
	// 函数级快照路由
	r.Route("/functions/{id}/snapshots", func(r chi.Router) {
		r.Get("/", sh.ListFunctionSnapshots)
		r.Post("/", sh.BuildSnapshot)
		r.Delete("/{snapshotId}", sh.DeleteSnapshot)
	})

	// 全局快照统计
	r.Get("/snapshots/stats", sh.GetSnapshotStats)
}

// ListFunctionSnapshots 列出函数的所有快照
// GET /api/v1/functions/{id}/snapshots
func (sh *SnapshotHandler) ListFunctionSnapshots(w http.ResponseWriter, r *http.Request) {
	functionID := chi.URLParam(r, "id")
	if functionID == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id required")
		return
	}

	snapshots, err := sh.snapshotMgr.ListSnapshots(r.Context(), functionID)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to list snapshots: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"snapshots": snapshots,
		"total":     len(snapshots),
	})
}

// BuildSnapshot 手动构建快照
// POST /api/v1/functions/{id}/snapshots
func (sh *SnapshotHandler) BuildSnapshot(w http.ResponseWriter, r *http.Request) {
	functionID := chi.URLParam(r, "id")
	if functionID == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "function id required")
		return
	}

	// 获取函数
	fn, err := sh.handler.store.GetFunctionByID(functionID)
	if err != nil {
		writeErrorWithContext(w, r, http.StatusNotFound, "function not found")
		return
	}

	// 解析请求
	var req struct {
		Version int  `json:"version"`
		Wait    bool `json:"wait"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	// 默认使用最新版本
	version := req.Version
	if version == 0 {
		version = fn.Version
	}

	if req.Wait {
		// 同步构建
		if err := sh.snapshotMgr.RequestBuildSync(r.Context(), fn, version); err != nil {
			writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to build snapshot: "+err.Error())
			return
		}

		// 获取构建后的快照
		snap, err := sh.snapshotMgr.GetSnapshot(r.Context(), fn, version)
		if err != nil {
			writeErrorWithContext(w, r, http.StatusInternalServerError, "snapshot built but not found: "+err.Error())
			return
		}

		writeJSON(w, http.StatusCreated, snap)
	} else {
		// 异步构建
		if err := sh.snapshotMgr.RequestBuild(fn, version); err != nil {
			writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to queue snapshot build: "+err.Error())
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]interface{}{
			"message":     "Snapshot build queued",
			"function_id": functionID,
			"version":     version,
		})
	}
}

// DeleteSnapshot 删除快照
// DELETE /api/v1/functions/{id}/snapshots/{snapshotId}
func (sh *SnapshotHandler) DeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshotID := chi.URLParam(r, "snapshotId")
	if snapshotID == "" {
		writeErrorWithContext(w, r, http.StatusBadRequest, "snapshot id required")
		return
	}

	if err := sh.snapshotMgr.DeleteSnapshot(r.Context(), snapshotID); err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to delete snapshot: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetSnapshotStats 获取快照统计
// GET /api/v1/snapshots/stats
func (sh *SnapshotHandler) GetSnapshotStats(w http.ResponseWriter, r *http.Request) {
	stats, err := sh.snapshotMgr.GetStats(r.Context())
	if err != nil {
		writeErrorWithContext(w, r, http.StatusInternalServerError, "failed to get snapshot stats: "+err.Error())
		return
	}

	// 转换字节为 GB
	totalSizeGB := float64(stats.TotalSizeBytes) / (1024 * 1024 * 1024)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total_snapshots":    stats.TotalSnapshots,
		"ready_snapshots":    stats.ReadySnapshots,
		"building_snapshots": stats.BuildingSnapshots,
		"failed_snapshots":   stats.FailedSnapshots,
		"total_size_gb":      roundFloat(totalSizeGB, 2),
		"avg_restore_ms":     roundFloat(stats.AvgRestoreMs, 1),
	})
}

// roundFloat 四舍五入到指定小数位
func roundFloat(val float64, precision int) float64 {
	ratio := float64(1)
	for i := 0; i < precision; i++ {
		ratio *= 10
	}
	return float64(int64(val*ratio+0.5)) / ratio
}

// Ensure strconv is used (for future use)
var _ = strconv.Itoa
