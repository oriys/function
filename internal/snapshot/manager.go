// Package snapshot 提供函数级快照管理功能。
// 该包负责创建、存储、恢复和清理函数的执行快照，
// 以实现毫秒级冷启动优化。
package snapshot

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/oriys/nimbus/internal/config"
	"github.com/oriys/nimbus/internal/domain"
	"github.com/sirupsen/logrus"
)

// SnapshotStatus 快照状态常量
const (
	StatusBuilding = "building"
	StatusReady    = "ready"
	StatusFailed   = "failed"
	StatusExpired  = "expired"
)

// SnapshotInfo 快照信息
type SnapshotInfo struct {
	ID            string    `json:"id"`
	FunctionID    string    `json:"function_id"`
	Version       int       `json:"version"`
	CodeHash      string    `json:"code_hash"`
	Runtime       string    `json:"runtime"`
	MemoryMB      int       `json:"memory_mb"`
	EnvVarsHash   string    `json:"env_vars_hash"`
	SnapshotPath  string    `json:"snapshot_path"`
	MemFileSize   int64     `json:"mem_file_size"`
	StateFileSize int64     `json:"state_file_size"`
	Status        string    `json:"status"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	RestoreCount  int       `json:"restore_count"`
	AvgRestoreMs  float64   `json:"avg_restore_ms"`
	CreatedAt     time.Time `json:"created_at"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}

// SnapshotMetadata 快照元数据（存储在文件系统）
type SnapshotMetadata struct {
	SnapshotID          string            `json:"snapshot_id"`
	FunctionID          string            `json:"function_id"`
	Version             int               `json:"version"`
	CodeHash            string            `json:"code_hash"`
	Runtime             string            `json:"runtime"`
	MemoryMB            int               `json:"memory_mb"`
	VCPUs               int               `json:"vcpus"`
	EnvVarsHash         string            `json:"env_vars_hash"`
	CreatedAt           time.Time         `json:"created_at"`
	FirecrackerVersion  string            `json:"firecracker_version"`
	VsockCID            uint32            `json:"vsock_cid"`
	NetworkConfig       *NetworkConfig    `json:"network_config,omitempty"`
}

// NetworkConfig 网络配置
type NetworkConfig struct {
	GuestIP    string `json:"guest_ip"`
	GatewayIP  string `json:"gateway_ip"`
	MacAddress string `json:"mac_address"`
}

// buildTask 快照构建任务
type buildTask struct {
	function *domain.Function
	version  int
	resultCh chan error
}

// DBExecutor 数据库执行接口
type DBExecutor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
}

// Manager 管理函数级快照
type Manager struct {
	cfg    config.SnapshotConfig
	db     DBExecutor
	logger *logrus.Logger

	// 构建任务队列
	buildQueue chan *buildTask
	// 正在构建的快照（防止重复构建）
	building   map[string]bool
	buildingMu sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
}

// NewManager 创建新的快照管理器
func NewManager(cfg config.SnapshotConfig, db DBExecutor, logger *logrus.Logger) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	m := &Manager{
		cfg:        cfg,
		db:         db,
		logger:     logger,
		buildQueue: make(chan *buildTask, 100),
		building:   make(map[string]bool),
		ctx:        ctx,
		cancel:     cancel,
	}

	// 确保快照目录存在
	if err := os.MkdirAll(cfg.SnapshotDir, 0755); err != nil {
		logger.WithError(err).Warn("Failed to create snapshot directory")
	}

	// 启动构建 worker
	for i := 0; i < cfg.BuildWorkers; i++ {
		go m.buildWorker(i)
	}

	// 启动清理 worker
	go m.cleanupWorker()

	logger.WithFields(logrus.Fields{
		"snapshot_dir":   cfg.SnapshotDir,
		"build_workers":  cfg.BuildWorkers,
		"snapshot_ttl":   cfg.SnapshotTTL,
	}).Info("Snapshot manager started")

	return m
}

// GetSnapshot 获取函数的有效快照
func (m *Manager) GetSnapshot(ctx context.Context, fn *domain.Function, version int) (*SnapshotInfo, error) {
	envVarsHash := m.hashEnvVars(fn.EnvVars)

	query := `
		SELECT id, function_id, version, code_hash, runtime, memory_mb,
		       env_vars_hash, snapshot_path, mem_file_size, state_file_size,
		       status, error_message, restore_count, avg_restore_ms,
		       created_at, last_used_at, expires_at
		FROM function_snapshots
		WHERE function_id = $1
		  AND version = $2
		  AND code_hash = $3
		  AND env_vars_hash = $4
		  AND status = 'ready'
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at DESC
		LIMIT 1`

	var snap SnapshotInfo
	var lastUsedAt, expiresAt sql.NullTime
	var errorMessage sql.NullString

	err := m.db.QueryRowContext(ctx, query,
		fn.ID, version, fn.CodeHash, envVarsHash).Scan(
		&snap.ID, &snap.FunctionID, &snap.Version, &snap.CodeHash,
		&snap.Runtime, &snap.MemoryMB, &snap.EnvVarsHash,
		&snap.SnapshotPath, &snap.MemFileSize, &snap.StateFileSize,
		&snap.Status, &errorMessage, &snap.RestoreCount, &snap.AvgRestoreMs,
		&snap.CreatedAt, &lastUsedAt, &expiresAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no valid snapshot found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query snapshot: %w", err)
	}

	if errorMessage.Valid {
		snap.ErrorMessage = errorMessage.String
	}
	if lastUsedAt.Valid {
		snap.LastUsedAt = &lastUsedAt.Time
	}
	if expiresAt.Valid {
		snap.ExpiresAt = &expiresAt.Time
	}

	// 验证快照文件存在
	memPath := filepath.Join(snap.SnapshotPath, "mem")
	if _, err := os.Stat(memPath); os.IsNotExist(err) {
		// 快照文件丢失，标记为失效
		m.markSnapshotExpired(ctx, snap.ID)
		return nil, fmt.Errorf("snapshot files missing")
	}

	return &snap, nil
}

// RequestBuild 请求构建快照（异步）
func (m *Manager) RequestBuild(fn *domain.Function, version int) error {
	buildKey := fmt.Sprintf("%s:%d:%s", fn.ID, version, fn.CodeHash)

	m.buildingMu.Lock()
	if m.building[buildKey] {
		m.buildingMu.Unlock()
		return nil // 已在构建中
	}
	m.building[buildKey] = true
	m.buildingMu.Unlock()

	task := &buildTask{
		function: fn,
		version:  version,
		resultCh: make(chan error, 1),
	}

	select {
	case m.buildQueue <- task:
		return nil
	default:
		m.buildingMu.Lock()
		delete(m.building, buildKey)
		m.buildingMu.Unlock()
		return fmt.Errorf("build queue full")
	}
}

// RequestBuildSync 同步构建快照（等待完成）
func (m *Manager) RequestBuildSync(ctx context.Context, fn *domain.Function, version int) error {
	task := &buildTask{
		function: fn,
		version:  version,
		resultCh: make(chan error, 1),
	}

	select {
	case m.buildQueue <- task:
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-task.resultCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// buildWorker 快照构建工作协程
func (m *Manager) buildWorker(id int) {
	m.logger.WithField("worker_id", id).Info("Snapshot build worker started")

	for {
		select {
		case <-m.ctx.Done():
			return
		case task := <-m.buildQueue:
			err := m.buildSnapshot(task.function, task.version)

			buildKey := fmt.Sprintf("%s:%d:%s", task.function.ID, task.version, task.function.CodeHash)
			m.buildingMu.Lock()
			delete(m.building, buildKey)
			m.buildingMu.Unlock()

			if task.resultCh != nil {
				task.resultCh <- err
			}
		}
	}
}

// buildSnapshot 构建函数快照
// 注意：实际的快照创建需要 Firecracker 集成，这里创建快照记录和目录结构
func (m *Manager) buildSnapshot(fn *domain.Function, version int) error {
	ctx, cancel := context.WithTimeout(m.ctx, m.cfg.BuildTimeout)
	defer cancel()

	m.logger.WithFields(logrus.Fields{
		"function_id": fn.ID,
		"version":     version,
		"code_hash":   fn.CodeHash,
	}).Info("Building function snapshot")

	// 生成快照 ID 和路径
	snapshotID := uuid.New().String()
	envVarsHash := m.hashEnvVars(fn.EnvVars)

	// 使用 function_id + code_hash 前 16 字符作为目录名
	dirName := fmt.Sprintf("%s_%s", fn.ID, fn.CodeHash[:min(16, len(fn.CodeHash))])
	snapshotPath := filepath.Join(m.cfg.SnapshotDir, dirName)

	// 创建快照目录
	if err := os.MkdirAll(snapshotPath, 0755); err != nil {
		return fmt.Errorf("failed to create snapshot dir: %w", err)
	}

	// 创建数据库记录（状态为 building）
	if err := m.createSnapshotRecord(ctx, snapshotID, fn, version, envVarsHash, snapshotPath); err != nil {
		return fmt.Errorf("failed to create snapshot record: %w", err)
	}

	// 保存元数据文件
	metadata := &SnapshotMetadata{
		SnapshotID:         snapshotID,
		FunctionID:         fn.ID,
		Version:            version,
		CodeHash:           fn.CodeHash,
		Runtime:            string(fn.Runtime),
		MemoryMB:           fn.MemoryMB,
		VCPUs:              1,
		EnvVarsHash:        envVarsHash,
		CreatedAt:          time.Now().UTC(),
		FirecrackerVersion: "1.0.0",
	}

	metadataPath := filepath.Join(snapshotPath, "metadata.json")
	metadataJSON, _ := json.MarshalIndent(metadata, "", "  ")
	if err := os.WriteFile(metadataPath, metadataJSON, 0644); err != nil {
		m.logger.WithError(err).Warn("Failed to write metadata")
	}

	// TODO: 实际的 Firecracker 快照创建流程：
	// 1. 创建新 VM
	// 2. 连接 vsock
	// 3. 发送 InitPayload（注入代码）
	// 4. 可选执行预热调用
	// 5. 调用 machinesMgr.CreateSnapshotWithPath(ctx, vm.ID, memPath, statePath)
	// 6. 销毁预热 VM
	//
	// 当前实现只创建记录，实际快照创建需要在 VM Pool 层面集成

	// 创建占位文件（实际快照会替换这些文件）
	memPath := filepath.Join(snapshotPath, "mem")
	statePath := filepath.Join(snapshotPath, "snapshot")

	// 写入占位内容（表示快照尚未实际创建）
	if err := os.WriteFile(memPath, []byte("placeholder"), 0644); err != nil {
		m.updateSnapshotStatus(ctx, snapshotID, StatusFailed, err.Error())
		return fmt.Errorf("failed to create mem placeholder: %w", err)
	}
	if err := os.WriteFile(statePath, []byte("placeholder"), 0644); err != nil {
		m.updateSnapshotStatus(ctx, snapshotID, StatusFailed, err.Error())
		return fmt.Errorf("failed to create state placeholder: %w", err)
	}

	// 获取文件大小
	memInfo, _ := os.Stat(memPath)
	stateInfo, _ := os.Stat(statePath)

	// 更新数据库记录为 ready
	if err := m.updateSnapshotReady(ctx, snapshotID, memInfo.Size(), stateInfo.Size()); err != nil {
		return fmt.Errorf("failed to update snapshot record: %w", err)
	}

	m.logger.WithFields(logrus.Fields{
		"snapshot_id":   snapshotID,
		"function_id":   fn.ID,
		"snapshot_path": snapshotPath,
	}).Info("Function snapshot record created (placeholder)")

	return nil
}

// InvalidateSnapshots 使函数的所有快照失效
func (m *Manager) InvalidateSnapshots(ctx context.Context, functionID string) error {
	// 获取所有相关快照
	query := `
		SELECT id, snapshot_path FROM function_snapshots
		WHERE function_id = $1 AND status = 'ready'`

	rows, err := m.db.QueryContext(ctx, query, functionID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			continue
		}

		// 删除快照文件
		os.RemoveAll(path)

		// 更新状态
		m.updateSnapshotStatus(ctx, id, StatusExpired, "Function updated")
	}

	m.logger.WithField("function_id", functionID).Info("Invalidated all snapshots for function")
	return nil
}

// ListSnapshots 列出函数的所有快照
func (m *Manager) ListSnapshots(ctx context.Context, functionID string) ([]*SnapshotInfo, error) {
	query := `
		SELECT id, function_id, version, code_hash, runtime, memory_mb,
		       env_vars_hash, snapshot_path, mem_file_size, state_file_size,
		       status, error_message, restore_count, avg_restore_ms,
		       created_at, last_used_at, expires_at
		FROM function_snapshots
		WHERE function_id = $1
		ORDER BY created_at DESC`

	rows, err := m.db.QueryContext(ctx, query, functionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query snapshots: %w", err)
	}
	defer rows.Close()

	var snapshots []*SnapshotInfo
	for rows.Next() {
		var snap SnapshotInfo
		var lastUsedAt, expiresAt sql.NullTime
		var errorMessage sql.NullString

		if err := rows.Scan(
			&snap.ID, &snap.FunctionID, &snap.Version, &snap.CodeHash,
			&snap.Runtime, &snap.MemoryMB, &snap.EnvVarsHash,
			&snap.SnapshotPath, &snap.MemFileSize, &snap.StateFileSize,
			&snap.Status, &errorMessage, &snap.RestoreCount, &snap.AvgRestoreMs,
			&snap.CreatedAt, &lastUsedAt, &expiresAt); err != nil {
			continue
		}

		if errorMessage.Valid {
			snap.ErrorMessage = errorMessage.String
		}
		if lastUsedAt.Valid {
			snap.LastUsedAt = &lastUsedAt.Time
		}
		if expiresAt.Valid {
			snap.ExpiresAt = &expiresAt.Time
		}

		snapshots = append(snapshots, &snap)
	}

	return snapshots, nil
}

// DeleteSnapshot 删除指定快照
func (m *Manager) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	// 获取快照路径
	var path string
	query := `SELECT snapshot_path FROM function_snapshots WHERE id = $1`
	if err := m.db.QueryRowContext(ctx, query, snapshotID).Scan(&path); err != nil {
		return fmt.Errorf("snapshot not found: %w", err)
	}

	// 删除文件
	if path != "" {
		os.RemoveAll(path)
	}

	// 删除数据库记录
	_, err := m.db.ExecContext(ctx, "DELETE FROM function_snapshots WHERE id = $1", snapshotID)
	if err != nil {
		return fmt.Errorf("failed to delete snapshot record: %w", err)
	}

	m.logger.WithField("snapshot_id", snapshotID).Info("Snapshot deleted")
	return nil
}

// GetStats 获取快照统计信息
func (m *Manager) GetStats(ctx context.Context) (*SnapshotStats, error) {
	stats := &SnapshotStats{}

	// 获取各状态的快照数量
	query := `
		SELECT status, COUNT(*), COALESCE(SUM(mem_file_size + state_file_size), 0)
		FROM function_snapshots
		GROUP BY status`

	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		var size int64
		if err := rows.Scan(&status, &count, &size); err != nil {
			continue
		}

		stats.TotalSnapshots += count
		stats.TotalSizeBytes += size

		switch status {
		case StatusReady:
			stats.ReadySnapshots = count
		case StatusBuilding:
			stats.BuildingSnapshots = count
		case StatusFailed:
			stats.FailedSnapshots = count
		}
	}

	// 获取平均恢复时间
	avgQuery := `
		SELECT COALESCE(AVG(avg_restore_ms), 0)
		FROM function_snapshots
		WHERE status = 'ready' AND restore_count > 0`
	m.db.QueryRowContext(ctx, avgQuery).Scan(&stats.AvgRestoreMs)

	return stats, nil
}

// SnapshotStats 快照统计信息
type SnapshotStats struct {
	TotalSnapshots    int     `json:"total_snapshots"`
	ReadySnapshots    int     `json:"ready_snapshots"`
	BuildingSnapshots int     `json:"building_snapshots"`
	FailedSnapshots   int     `json:"failed_snapshots"`
	TotalSizeBytes    int64   `json:"total_size_bytes"`
	AvgRestoreMs      float64 `json:"avg_restore_ms"`
}

// cleanupWorker 清理过期快照
func (m *Manager) cleanupWorker() {
	ticker := time.NewTicker(m.cfg.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.cleanupExpiredSnapshots()
		}
	}
}

func (m *Manager) cleanupExpiredSnapshots() {
	ctx := context.Background()

	// 查找过期快照
	query := `
		SELECT id, snapshot_path FROM function_snapshots
		WHERE expires_at < NOW() OR status = 'expired'`

	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		m.logger.WithError(err).Error("Failed to query expired snapshots")
		return
	}
	defer rows.Close()

	var cleanedCount int
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			continue
		}

		// 删除快照文件
		if err := os.RemoveAll(path); err != nil {
			m.logger.WithError(err).WithField("path", path).Warn("Failed to delete snapshot files")
		}

		// 删除数据库记录
		m.db.ExecContext(ctx, "DELETE FROM function_snapshots WHERE id = $1", id)
		cleanedCount++
	}

	if cleanedCount > 0 {
		m.logger.WithField("count", cleanedCount).Info("Cleaned up expired snapshots")
	}
}

// UpdateSnapshotStats 更新快照恢复统计（外部调用）
func (m *Manager) UpdateSnapshotStats(ctx context.Context, snapshotID string, restoreMs float64) {
	query := `
		UPDATE function_snapshots
		SET restore_count = restore_count + 1,
		    avg_restore_ms = (avg_restore_ms * restore_count + $1) / (restore_count + 1),
		    last_used_at = NOW()
		WHERE id = $2`
	m.db.ExecContext(ctx, query, restoreMs, snapshotID)
}

// 辅助方法

func (m *Manager) hashEnvVars(envVars map[string]string) string {
	if len(envVars) == 0 {
		return "empty"
	}
	data, _ := json.Marshal(envVars)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])[:16]
}

func (m *Manager) createSnapshotRecord(ctx context.Context, id string, fn *domain.Function, version int, envVarsHash, path string) error {
	query := `
		INSERT INTO function_snapshots
		(id, function_id, version, code_hash, runtime, memory_mb, env_vars_hash, snapshot_path, status, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'building', NOW(), NOW() + INTERVAL '7 days')
		ON CONFLICT (function_id, version, code_hash, env_vars_hash) DO UPDATE
		SET status = 'building', snapshot_path = $8, created_at = NOW(), expires_at = NOW() + INTERVAL '7 days'`

	_, err := m.db.ExecContext(ctx, query, id, fn.ID, version, fn.CodeHash, fn.Runtime, fn.MemoryMB, envVarsHash, path)
	return err
}

func (m *Manager) updateSnapshotStatus(ctx context.Context, id, status, errorMsg string) error {
	query := `UPDATE function_snapshots SET status = $1, error_message = $2 WHERE id = $3`
	_, err := m.db.ExecContext(ctx, query, status, errorMsg, id)
	return err
}

func (m *Manager) updateSnapshotReady(ctx context.Context, id string, memSize, stateSize int64) error {
	query := `
		UPDATE function_snapshots
		SET status = 'ready', mem_file_size = $1, state_file_size = $2
		WHERE id = $3`
	_, err := m.db.ExecContext(ctx, query, memSize, stateSize, id)
	return err
}

func (m *Manager) markSnapshotExpired(ctx context.Context, id string) {
	m.updateSnapshotStatus(ctx, id, StatusExpired, "Files missing")
}

// Shutdown 关闭管理器
func (m *Manager) Shutdown() {
	m.cancel()
	m.logger.Info("Snapshot manager shutdown")
}

// min 返回两个整数中的较小值
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
