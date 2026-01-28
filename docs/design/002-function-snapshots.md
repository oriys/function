# 设计文档 002: 函数级快照 (Function-Level Snapshots)

> 版本：1.0
> 状态：草案
> 作者：Claude
> 创建日期：2026-01-29

---

## 1. 概述

### 1.1 背景

当前 Nimbus 的 VM 预热是**运行时级别**的：
- 预热一个空白的 Python/Node.js VM
- 调用时才注入函数代码并初始化
- 冷启动仍需 ~125ms（VM 启动）+ ~50ms（代码初始化）

现有快照功能（`machine.go:378-541`）也是运行时级别的，无法跳过代码初始化阶段。

### 1.2 目标

1. **函数级快照**：在代码注入并完成初始化后创建快照
2. **毫秒级冷启动**：从快照恢复实现 <100ms 的冷启动
3. **按需拉起**：快照存储在磁盘，不占用运行时内存
4. **自动失效**：代码变更时自动重建快照

### 1.3 现有基础

- `internal/firecracker/machine.go`:
  - `CreateSnapshot()` (L384-416)
  - `RestoreFromSnapshot()` (L418-541)
- `internal/vmpool/pool.go`:
  - `SnapshotPool` (L490-564)
  - `CreateSnapshot()` / `AcquireFromSnapshot()`

---

## 2. 架构设计

### 2.1 系统架构

```
┌─────────────────────────────────────────────────────────────────┐
│                      Snapshot Manager                            │
│  ┌──────────────────┐  ┌──────────────────┐  ┌───────────────┐ │
│  │  Snapshot Index  │  │  Snapshot Store  │  │   Lifecycle   │ │
│  │   (PostgreSQL)   │  │    (文件系统)     │  │   Manager     │ │
│  └──────────────────┘  └──────────────────┘  └───────────────┘ │
└───────────────────────────┬─────────────────────────────────────┘
                            │
┌───────────────────────────▼─────────────────────────────────────┐
│                         VM Pool                                  │
│  ┌─────────────┐  ┌─────────────────────┐  ┌─────────────────┐ │
│  │  Warm Pool  │  │  Snapshot Restore   │  │  Cold Create    │ │
│  │  (现有)     │  │  (快照恢复路径)     │  │  (完整启动)     │ │
│  └─────────────┘  └─────────────────────┘  └─────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│                   快照文件存储                                   │
│  /var/nimbus/snapshots/                                          │
│  ├── {function_id}_{code_hash}/                                  │
│  │   ├── mem           # 内存快照 (压缩)                         │
│  │   ├── snapshot      # CPU/设备状态                            │
│  │   └── metadata.json # 快照元数据                              │
│  └── ...                                                         │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 快照生命周期

```
[函数部署/更新]
    │
    ├── 1. 保存函数代码到数据库
    ├── 2. 触发快照构建任务
    │       │
    │       ├── 创建新 VM
    │       ├── 发送 InitPayload（注入代码）
    │       ├── 执行预热调用（可选）
    │       ├── 创建快照
    │       │       ├── mem 文件（内存快照）
    │       │       └── snapshot 文件（状态快照）
    │       ├── 注册快照到索引
    │       └── 销毁预热 VM
    │
    └── 3. 函数状态变为 active

[函数调用 - 优化路径]
    │
    ├── 1. 检查是否有可用的函数快照
    │       │
    │       ├── 存在 → 从快照恢复 VM (~50-100ms)
    │       │            └── 直接执行函数
    │       │
    │       └── 不存在 → 回退到原有流程
    │                     ├── 获取 Warm VM (~2ms)
    │                     └── 发送 Init + Exec (~50ms)
    │
    └── 2. 执行函数

[快照失效]
    │
    ├── 触发条件：
    │   ├── 函数代码更新（code_hash 变化）
    │   ├── 运行时配置变更（memory_mb, env_vars）
    │   └── 手动清理 / TTL 过期
    │
    └── 清理旧快照文件 + 索引记录
```

### 2.3 冷启动对比

| 场景 | 当前延迟 | 优化后延迟 | 说明 |
|------|----------|------------|------|
| 热启动 | ~2ms | ~2ms | 无变化 |
| 运行时快照恢复 | ~5ms | - | 仍需 Init |
| **函数快照恢复** | - | **~50-100ms** | 新增 |
| 完整冷启动 | ~175ms | ~175ms | 回退路径 |

---

## 3. 数据模型

### 3.1 快照索引表

```sql
CREATE TABLE function_snapshots (
    id              VARCHAR(36) PRIMARY KEY,
    function_id     VARCHAR(36) NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
    version         INTEGER NOT NULL,              -- 对应的函数版本
    code_hash       VARCHAR(64) NOT NULL,          -- 代码哈希（用于失效判断）

    -- 运行时配置（影响快照有效性）
    runtime         VARCHAR(32) NOT NULL,
    memory_mb       INTEGER NOT NULL,
    env_vars_hash   VARCHAR(64),                   -- 环境变量哈希

    -- 快照存储位置
    snapshot_path   VARCHAR(512) NOT NULL,         -- 快照目录路径
    mem_file_size   BIGINT,                        -- 内存快照大小（字节）
    state_file_size BIGINT,                        -- 状态快照大小（字节）

    -- 状态
    status          VARCHAR(32) DEFAULT 'building', -- building, ready, failed, expired
    error_message   TEXT,

    -- 统计
    restore_count   INTEGER DEFAULT 0,             -- 恢复次数
    avg_restore_ms  FLOAT DEFAULT 0,               -- 平均恢复时间

    -- 时间戳
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    last_used_at    TIMESTAMP WITH TIME ZONE,
    expires_at      TIMESTAMP WITH TIME ZONE,      -- TTL 过期时间

    -- 唯一约束：同一函数版本+配置只有一个有效快照
    UNIQUE(function_id, version, code_hash, env_vars_hash)
);

-- 索引
CREATE INDEX idx_snapshots_function_id ON function_snapshots(function_id);
CREATE INDEX idx_snapshots_status ON function_snapshots(status);
CREATE INDEX idx_snapshots_expires_at ON function_snapshots(expires_at);
```

### 3.2 快照元数据文件

**文件：** `/var/nimbus/snapshots/{snapshot_id}/metadata.json`

```json
{
  "snapshot_id": "snap_abc123",
  "function_id": "fn_xyz789",
  "version": 3,
  "code_hash": "sha256:abc123...",
  "runtime": "python3.11",
  "memory_mb": 256,
  "vcpus": 1,
  "env_vars_hash": "sha256:def456...",
  "created_at": "2026-01-29T10:00:00Z",
  "firecracker_version": "1.0.0",
  "kernel_version": "5.10.0",
  "agent_version": "1.0.0",
  "vsock_cid": 100,
  "network_config": {
    "guest_ip": "10.0.0.2",
    "gateway_ip": "10.0.0.1",
    "mac_address": "AA:BB:CC:DD:EE:FF"
  }
}
```

---

## 4. 核心实现

### 4.1 快照管理器

**文件：** `internal/snapshot/manager.go` (新文件)

```go
package snapshot

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "sync"
    "time"

    "github.com/google/uuid"
    "github.com/sirupsen/logrus"

    "nimbus/internal/config"
    "nimbus/internal/domain"
    fc "nimbus/internal/firecracker"
    "nimbus/internal/storage"
)

// Manager 管理函数级快照
type Manager struct {
    cfg        config.SnapshotConfig
    store      *storage.PostgresStore
    machinesMgr *fc.MachineManager
    logger     *logrus.Logger

    // 构建任务队列
    buildQueue chan *buildTask
    // 正在构建的快照（防止重复构建）
    building   map[string]bool
    buildingMu sync.Mutex

    ctx    context.Context
    cancel context.CancelFunc
}

type buildTask struct {
    function  *domain.Function
    version   int
    resultCh  chan error
}

// SnapshotInfo 快照信息
type SnapshotInfo struct {
    ID           string    `json:"id"`
    FunctionID   string    `json:"function_id"`
    Version      int       `json:"version"`
    CodeHash     string    `json:"code_hash"`
    Runtime      string    `json:"runtime"`
    MemoryMB     int       `json:"memory_mb"`
    EnvVarsHash  string    `json:"env_vars_hash"`
    SnapshotPath string    `json:"snapshot_path"`
    Status       string    `json:"status"`
    CreatedAt    time.Time `json:"created_at"`
}

func NewManager(cfg config.SnapshotConfig, store *storage.PostgresStore, machinesMgr *fc.MachineManager, logger *logrus.Logger) *Manager {
    ctx, cancel := context.WithCancel(context.Background())

    m := &Manager{
        cfg:         cfg,
        store:       store,
        machinesMgr: machinesMgr,
        logger:      logger,
        buildQueue:  make(chan *buildTask, 100),
        building:    make(map[string]bool),
        ctx:         ctx,
        cancel:      cancel,
    }

    // 启动构建 worker
    for i := 0; i < cfg.BuildWorkers; i++ {
        go m.buildWorker(i)
    }

    // 启动清理 worker
    go m.cleanupWorker()

    return m
}

// GetSnapshot 获取函数的有效快照
func (m *Manager) GetSnapshot(ctx context.Context, fn *domain.Function, version int) (*SnapshotInfo, error) {
    envVarsHash := m.hashEnvVars(fn.EnvVars)

    query := `
        SELECT id, function_id, version, code_hash, runtime, memory_mb,
               env_vars_hash, snapshot_path, status, created_at
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
    err := m.store.DB().QueryRowContext(ctx, query,
        fn.ID, version, fn.CodeHash, envVarsHash).Scan(
        &snap.ID, &snap.FunctionID, &snap.Version, &snap.CodeHash,
        &snap.Runtime, &snap.MemoryMB, &snap.EnvVarsHash,
        &snap.SnapshotPath, &snap.Status, &snap.CreatedAt)

    if err != nil {
        return nil, err
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
    snapshotPath := filepath.Join(m.cfg.SnapshotDir, fmt.Sprintf("%s_%s", fn.ID, fn.CodeHash[:16]))

    // 创建快照目录
    if err := os.MkdirAll(snapshotPath, 0755); err != nil {
        return fmt.Errorf("failed to create snapshot dir: %w", err)
    }

    // 创建数据库记录
    if err := m.createSnapshotRecord(ctx, snapshotID, fn, version, envVarsHash, snapshotPath); err != nil {
        return fmt.Errorf("failed to create snapshot record: %w", err)
    }

    // 创建并初始化 VM
    vm, err := m.machinesMgr.CreateVM(ctx, string(fn.Runtime), fn.MemoryMB, 1)
    if err != nil {
        m.updateSnapshotStatus(ctx, snapshotID, "failed", err.Error())
        return fmt.Errorf("failed to create VM: %w", err)
    }
    defer m.machinesMgr.StopVM(ctx, vm.ID)

    // 连接 vsock
    client := fc.NewVsockClient(vm.VsockCID, m.logger)
    if err := client.Connect(ctx); err != nil {
        m.updateSnapshotStatus(ctx, snapshotID, "failed", err.Error())
        return fmt.Errorf("failed to connect vsock: %w", err)
    }
    defer client.Close()

    // 加载函数版本代码
    versionData, err := m.store.GetVersion(ctx, fn.ID, version)
    if err != nil {
        m.updateSnapshotStatus(ctx, snapshotID, "failed", err.Error())
        return fmt.Errorf("failed to get version: %w", err)
    }

    // 发送 Init 消息
    initPayload := &fc.InitPayload{
        FunctionID: fn.ID,
        Handler:    versionData.Handler,
        Code:       versionData.Code,
        Binary:     versionData.Binary,
        Runtime:    string(fn.Runtime),
        EnvVars:    versionData.EnvVars,
        MemoryMB:   versionData.MemoryMB,
        TimeoutSec: versionData.TimeoutSec,
    }

    if err := client.SendInit(ctx, initPayload); err != nil {
        m.updateSnapshotStatus(ctx, snapshotID, "failed", err.Error())
        return fmt.Errorf("failed to init function: %w", err)
    }

    // 可选：执行预热调用
    if m.cfg.WarmupOnBuild {
        warmupPayload := []byte(`{"warmup": true}`)
        if _, err := client.SendExec(ctx, warmupPayload); err != nil {
            m.logger.WithError(err).Warn("Warmup call failed, continuing with snapshot")
        }
    }

    // 创建快照
    memPath := filepath.Join(snapshotPath, "mem")
    statePath := filepath.Join(snapshotPath, "snapshot")

    if err := m.machinesMgr.CreateSnapshotWithPath(ctx, vm.ID, memPath, statePath); err != nil {
        m.updateSnapshotStatus(ctx, snapshotID, "failed", err.Error())
        return fmt.Errorf("failed to create snapshot: %w", err)
    }

    // 保存元数据
    metadata := map[string]interface{}{
        "snapshot_id":         snapshotID,
        "function_id":         fn.ID,
        "version":             version,
        "code_hash":           fn.CodeHash,
        "runtime":             fn.Runtime,
        "memory_mb":           fn.MemoryMB,
        "vcpus":               1,
        "env_vars_hash":       envVarsHash,
        "created_at":          time.Now().UTC(),
        "firecracker_version": "1.0.0",
        "vsock_cid":           vm.VsockCID,
    }

    metadataPath := filepath.Join(snapshotPath, "metadata.json")
    metadataJSON, _ := json.MarshalIndent(metadata, "", "  ")
    if err := os.WriteFile(metadataPath, metadataJSON, 0644); err != nil {
        m.logger.WithError(err).Warn("Failed to write metadata")
    }

    // 获取文件大小
    memInfo, _ := os.Stat(memPath)
    stateInfo, _ := os.Stat(statePath)

    // 更新数据库记录
    if err := m.updateSnapshotReady(ctx, snapshotID, memInfo.Size(), stateInfo.Size()); err != nil {
        return fmt.Errorf("failed to update snapshot record: %w", err)
    }

    m.logger.WithFields(logrus.Fields{
        "snapshot_id":   snapshotID,
        "function_id":   fn.ID,
        "mem_size_mb":   memInfo.Size() / 1024 / 1024,
        "state_size_kb": stateInfo.Size() / 1024,
    }).Info("Function snapshot created successfully")

    return nil
}

// RestoreFromSnapshot 从快照恢复 VM
func (m *Manager) RestoreFromSnapshot(ctx context.Context, snap *SnapshotInfo) (*fc.VM, *fc.VsockClient, error) {
    startTime := time.Now()

    memPath := filepath.Join(snap.SnapshotPath, "mem")
    statePath := filepath.Join(snap.SnapshotPath, "snapshot")

    // 从快照恢复 VM
    vm, err := m.machinesMgr.RestoreFromSnapshotPath(ctx, memPath, statePath, snap.Runtime)
    if err != nil {
        return nil, nil, fmt.Errorf("failed to restore VM: %w", err)
    }

    // 连接 vsock
    client := fc.NewVsockClient(vm.VsockCID, m.logger)
    if err := client.Connect(ctx); err != nil {
        m.machinesMgr.StopVM(ctx, vm.ID)
        return nil, nil, fmt.Errorf("failed to connect vsock: %w", err)
    }

    // 更新统计
    restoreMs := float64(time.Since(startTime).Milliseconds())
    m.updateSnapshotStats(ctx, snap.ID, restoreMs)

    m.logger.WithFields(logrus.Fields{
        "snapshot_id": snap.ID,
        "function_id": snap.FunctionID,
        "restore_ms":  restoreMs,
    }).Debug("VM restored from snapshot")

    return vm, client, nil
}

// InvalidateSnapshots 使函数的所有快照失效
func (m *Manager) InvalidateSnapshots(ctx context.Context, functionID string) error {
    // 获取所有相关快照
    query := `
        SELECT id, snapshot_path FROM function_snapshots
        WHERE function_id = $1 AND status = 'ready'`

    rows, err := m.store.DB().QueryContext(ctx, query, functionID)
    if err != nil {
        return err
    }
    defer rows.Close()

    for rows.Next() {
        var id, path string
        rows.Scan(&id, &path)

        // 删除快照文件
        os.RemoveAll(path)

        // 更新状态
        m.updateSnapshotStatus(ctx, id, "expired", "Function updated")
    }

    return nil
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

    rows, err := m.store.DB().QueryContext(ctx, query)
    if err != nil {
        m.logger.WithError(err).Error("Failed to query expired snapshots")
        return
    }
    defer rows.Close()

    for rows.Next() {
        var id, path string
        rows.Scan(&id, &path)

        // 删除快照文件
        if err := os.RemoveAll(path); err != nil {
            m.logger.WithError(err).WithField("path", path).Warn("Failed to delete snapshot files")
        }

        // 删除数据库记录
        m.store.DB().ExecContext(ctx, "DELETE FROM function_snapshots WHERE id = $1", id)

        m.logger.WithField("snapshot_id", id).Debug("Cleaned up expired snapshot")
    }
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
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'building', NOW(), NOW() + INTERVAL '7 days')`

    _, err := m.store.DB().ExecContext(ctx, query, id, fn.ID, version, fn.CodeHash, fn.Runtime, fn.MemoryMB, envVarsHash, path)
    return err
}

func (m *Manager) updateSnapshotStatus(ctx context.Context, id, status, errorMsg string) error {
    query := `UPDATE function_snapshots SET status = $1, error_message = $2 WHERE id = $3`
    _, err := m.store.DB().ExecContext(ctx, query, status, errorMsg, id)
    return err
}

func (m *Manager) updateSnapshotReady(ctx context.Context, id string, memSize, stateSize int64) error {
    query := `
        UPDATE function_snapshots
        SET status = 'ready', mem_file_size = $1, state_file_size = $2
        WHERE id = $3`
    _, err := m.store.DB().ExecContext(ctx, query, memSize, stateSize, id)
    return err
}

func (m *Manager) updateSnapshotStats(ctx context.Context, id string, restoreMs float64) {
    query := `
        UPDATE function_snapshots
        SET restore_count = restore_count + 1,
            avg_restore_ms = (avg_restore_ms * restore_count + $1) / (restore_count + 1),
            last_used_at = NOW()
        WHERE id = $2`
    m.store.DB().ExecContext(ctx, query, restoreMs, id)
}

func (m *Manager) markSnapshotExpired(ctx context.Context, id string) {
    m.updateSnapshotStatus(ctx, id, "expired", "Files missing")
}

// Shutdown 关闭管理器
func (m *Manager) Shutdown() {
    m.cancel()
}
```

### 4.2 配置结构

**文件：** `internal/config/config.go` (修改)

```go
// SnapshotConfig 快照配置
type SnapshotConfig struct {
    // 是否启用函数级快照
    Enabled bool `yaml:"enabled" default:"true"`

    // 快照存储目录
    SnapshotDir string `yaml:"snapshot_dir" default:"/var/nimbus/snapshots"`

    // 构建工作协程数
    BuildWorkers int `yaml:"build_workers" default:"2"`

    // 构建超时时间
    BuildTimeout time.Duration `yaml:"build_timeout" default:"60s"`

    // 是否在构建时执行预热调用
    WarmupOnBuild bool `yaml:"warmup_on_build" default:"true"`

    // 快照 TTL（默认 7 天）
    SnapshotTTL time.Duration `yaml:"snapshot_ttl" default:"168h"`

    // 清理间隔
    CleanupInterval time.Duration `yaml:"cleanup_interval" default:"1h"`

    // 单个函数最大快照数
    MaxSnapshotsPerFunction int `yaml:"max_snapshots_per_function" default:"3"`
}
```

### 4.3 VM Pool 集成

**文件：** `internal/vmpool/pool.go` (修改)

```go
// Pool 添加快照管理器
type Pool struct {
    // ... 现有字段 ...
    snapshotMgr *snapshot.Manager  // 新增
}

// AcquireVM 获取 VM（优先使用函数快照）
func (p *Pool) AcquireVM(ctx context.Context, fn *domain.Function, version int) (*PooledVM, bool, error) {
    // 优先尝试从函数快照恢复
    if p.snapshotMgr != nil && p.cfg.UseSnapshots {
        snap, err := p.snapshotMgr.GetSnapshot(ctx, fn, version)
        if err == nil && snap != nil {
            vm, client, err := p.snapshotMgr.RestoreFromSnapshot(ctx, snap)
            if err == nil {
                pvm := &PooledVM{
                    VM:        vm,
                    Client:    client,
                    Runtime:   snap.Runtime,
                    Status:    "busy",
                    CreatedAt: time.Now(),
                    LastUsed:  time.Now(),
                    UseCount:  0,
                    FromSnapshot: true,           // 新增标记
                    SnapshotID:   snap.ID,        // 新增
                }

                p.logger.WithFields(logrus.Fields{
                    "vm_id":       vm.ID,
                    "snapshot_id": snap.ID,
                    "function_id": fn.ID,
                }).Debug("VM acquired from function snapshot")

                return pvm, false, nil  // coldStart=false（从快照恢复视为热启动）
            }
            p.logger.WithError(err).Warn("Failed to restore from snapshot, falling back")
        }
    }

    // 回退到原有逻辑：从 warm pool 获取或创建新 VM
    return p.acquireVMOriginal(ctx, string(fn.Runtime))
}

// ReleaseVM 释放 VM
func (p *Pool) ReleaseVM(runtime string, vmID string) {
    // 从快照恢复的 VM 不放回 warm pool，直接销毁
    // 因为它们已经包含了特定函数的初始化状态
    pool := p.pools[runtime]
    pool.mu.Lock()
    pvm, ok := pool.allVMs[vmID]
    pool.mu.Unlock()

    if ok && pvm.FromSnapshot {
        // 快照恢复的 VM 用完即销毁
        p.machinesMgr.StopVM(context.Background(), vmID)
        pool.mu.Lock()
        delete(pool.allVMs, vmID)
        pool.mu.Unlock()
        return
    }

    // 原有逻辑
    p.releaseVMOriginal(runtime, vmID)
}
```

### 4.4 调度器集成

**文件：** `internal/scheduler/scheduler.go` (修改)

```go
// Scheduler 添加快照触发
type Scheduler struct {
    // ... 现有字段 ...
    snapshotMgr *snapshot.Manager  // 新增
}

// Invoke 修改调用流程
func (s *Scheduler) Invoke(ctx context.Context, fn *domain.Function, req *domain.InvokeRequest) (*domain.InvokeResponse, error) {
    version, aliasUsed, err := s.resolveVersion(ctx, fn.ID, req)
    if err != nil {
        return nil, err
    }

    // 获取 VM（优先使用函数快照）
    pvm, coldStart, err := s.pool.AcquireVM(ctx, fn, version)
    if err != nil {
        return nil, err
    }
    defer s.pool.ReleaseVM(string(fn.Runtime), pvm.VM.ID)

    // 如果是从快照恢复，跳过 Init 步骤
    if pvm.FromSnapshot {
        // 直接执行
        return s.executeFunction(ctx, pvm, fn, req, version, aliasUsed, coldStart)
    }

    // 原有逻辑：发送 Init + Exec
    return s.executeWithInit(ctx, pvm, fn, req, version, aliasUsed, coldStart)
}

// 函数部署后触发快照构建
func (s *Scheduler) OnFunctionDeployed(ctx context.Context, fn *domain.Function, version int) {
    if s.snapshotMgr != nil {
        // 异步构建快照
        go s.snapshotMgr.RequestBuild(fn, version)
    }
}

// 函数更新后使旧快照失效
func (s *Scheduler) OnFunctionUpdated(ctx context.Context, fn *domain.Function) {
    if s.snapshotMgr != nil {
        s.snapshotMgr.InvalidateSnapshots(ctx, fn.ID)
    }
}
```

### 4.5 Firecracker 扩展

**文件：** `internal/firecracker/machine.go` (修改)

```go
// CreateSnapshotWithPath 创建快照到指定路径
func (m *MachineManager) CreateSnapshotWithPath(ctx context.Context, vmID, memPath, statePath string) error {
    m.mu.RLock()
    vm, ok := m.vms[vmID]
    m.mu.RUnlock()
    if !ok {
        return fmt.Errorf("vm not found: %s", vmID)
    }

    // 暂停 VM
    if err := vm.machine.PauseVM(ctx); err != nil {
        return fmt.Errorf("failed to pause VM: %w", err)
    }

    // 创建快照
    if err := vm.machine.CreateSnapshot(ctx, memPath, statePath); err != nil {
        vm.machine.ResumeVM(ctx)
        return fmt.Errorf("failed to create snapshot: %w", err)
    }

    // 注意：创建快照后 VM 保持暂停状态
    // 调用方会销毁这个 VM

    m.logger.WithFields(logrus.Fields{
        "vm_id":      vmID,
        "mem_path":   memPath,
        "state_path": statePath,
    }).Info("Snapshot created with custom path")

    return nil
}

// RestoreFromSnapshotPath 从指定路径恢复快照
func (m *MachineManager) RestoreFromSnapshotPath(ctx context.Context, memPath, statePath, runtime string) (*VM, error) {
    vmID := uuid.New().String()

    // 分配 CID
    m.mu.Lock()
    cid := m.nextCID
    m.nextCID++
    m.mu.Unlock()

    socketPath := filepath.Join(m.cfg.SocketDir, vmID+".sock")
    logPath := filepath.Join(m.cfg.LogDir, vmID+".log")

    // 配置网络
    netConfig, err := m.networkMgr.SetupNetwork(vmID)
    if err != nil {
        return nil, fmt.Errorf("failed to setup network: %w", err)
    }

    vm := &VM{
        ID:         vmID,
        Runtime:    runtime,
        State:      VMStateCreating,
        VsockCID:   cid,
        SocketPath: socketPath,
        LogPath:    logPath,
        IP:         netConfig.GuestIP,
        CreatedAt:  time.Now(),
    }

    logFile, err := os.Create(logPath)
    if err != nil {
        m.networkMgr.CleanupNetwork(vmID)
        return nil, fmt.Errorf("failed to create log file: %w", err)
    }

    machineCtx, cancel := context.WithCancel(ctx)
    vm.cancel = cancel

    cmd := firecracker.VMCommandBuilder{}.
        WithBin(m.cfg.Binary).
        WithSocketPath(socketPath).
        WithStderr(logFile).
        WithStdout(logFile).
        Build(machineCtx)

    cfg := firecracker.Config{
        SocketPath: socketPath,
        Snapshot: firecracker.SnapshotConfig{
            MemFilePath:         memPath,
            SnapshotPath:        statePath,
            EnableDiffSnapshots: false,
            ResumeVM:            true,
        },
        VsockDevices: []firecracker.VsockDevice{
            {
                Path: filepath.Join(m.cfg.VsockDir, vmID+".vsock"),
                CID:  cid,
            },
        },
        NetworkInterfaces: []firecracker.NetworkInterface{
            {
                StaticConfiguration: &firecracker.StaticNetworkConfiguration{
                    MacAddress:  netConfig.MacAddress,
                    HostDevName: netConfig.TapDevice,
                },
            },
        },
    }

    machine, err := firecracker.NewMachine(machineCtx, cfg,
        firecracker.WithProcessRunner(cmd),
        firecracker.WithSnapshot(memPath, statePath))
    if err != nil {
        cancel()
        m.networkMgr.CleanupNetwork(vmID)
        logFile.Close()
        return nil, fmt.Errorf("failed to create machine: %w", err)
    }

    vm.machine = machine

    if err := machine.Start(machineCtx); err != nil {
        cancel()
        m.networkMgr.CleanupNetwork(vmID)
        logFile.Close()
        return nil, fmt.Errorf("failed to start machine: %w", err)
    }

    vm.State = VMStateRunning

    m.mu.Lock()
    m.vms[vmID] = vm
    m.mu.Unlock()

    m.logger.WithFields(logrus.Fields{
        "vm_id":   vmID,
        "runtime": runtime,
    }).Info("VM restored from snapshot path")

    return vm, nil
}
```

---

## 5. API 设计

### 5.1 快照管理 API

#### 列出函数快照
```
GET /api/v1/functions/{functionId}/snapshots
```

**响应：**
```json
{
  "snapshots": [
    {
      "id": "snap_abc123",
      "function_id": "fn_xyz789",
      "version": 3,
      "code_hash": "sha256:abc...",
      "runtime": "python3.11",
      "memory_mb": 256,
      "status": "ready",
      "mem_file_size": 268435456,
      "restore_count": 42,
      "avg_restore_ms": 65.3,
      "created_at": "2026-01-29T10:00:00Z",
      "last_used_at": "2026-01-29T15:00:00Z",
      "expires_at": "2026-02-05T10:00:00Z"
    }
  ]
}
```

#### 手动构建快照
```
POST /api/v1/functions/{functionId}/snapshots
```

**请求体：**
```json
{
  "version": 3,
  "wait": true  // 是否等待构建完成
}
```

#### 删除快照
```
DELETE /api/v1/functions/{functionId}/snapshots/{snapshotId}
```

#### 快照统计
```
GET /api/v1/snapshots/stats
```

**响应：**
```json
{
  "total_snapshots": 156,
  "ready_snapshots": 142,
  "building_snapshots": 8,
  "failed_snapshots": 6,
  "total_size_gb": 12.5,
  "avg_restore_ms": 72.4,
  "restore_success_rate": 0.98
}
```

---

## 6. 监控指标

### 6.1 Prometheus 指标

```go
var (
    snapshotBuildTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "nimbus_snapshot_build_total",
            Help: "Total snapshot build attempts",
        },
        []string{"function_id", "runtime", "status"}, // status: success, failed
    )

    snapshotBuildDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "nimbus_snapshot_build_duration_seconds",
            Help:    "Snapshot build duration",
            Buckets: []float64{1, 5, 10, 30, 60, 120},
        },
        []string{"runtime"},
    )

    snapshotRestoreTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "nimbus_snapshot_restore_total",
            Help: "Total snapshot restore attempts",
        },
        []string{"function_id", "status"},
    )

    snapshotRestoreDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "nimbus_snapshot_restore_duration_ms",
            Help:    "Snapshot restore duration in milliseconds",
            Buckets: []float64{10, 25, 50, 75, 100, 150, 200, 500},
        },
        []string{"runtime"},
    )

    snapshotStorageBytes = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "nimbus_snapshot_storage_bytes",
            Help: "Total snapshot storage usage",
        },
        []string{"runtime"},
    )

    snapshotCount = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "nimbus_snapshot_count",
            Help: "Current snapshot count by status",
        },
        []string{"status"},
    )
)
```

---

## 7. 错误处理

### 7.1 快照构建失败

```go
// 构建失败时的重试策略
type BuildRetryPolicy struct {
    MaxRetries     int           // 最大重试次数
    RetryInterval  time.Duration // 重试间隔
    BackoffFactor  float64       // 退避因子
}

func (m *Manager) buildWithRetry(fn *domain.Function, version int) error {
    var lastErr error
    for i := 0; i < m.cfg.MaxBuildRetries; i++ {
        if err := m.buildSnapshot(fn, version); err == nil {
            return nil
        } else {
            lastErr = err
            m.logger.WithError(err).WithField("attempt", i+1).Warn("Snapshot build failed, retrying")
            time.Sleep(m.cfg.RetryInterval * time.Duration(math.Pow(m.cfg.BackoffFactor, float64(i))))
        }
    }
    return fmt.Errorf("snapshot build failed after %d attempts: %w", m.cfg.MaxBuildRetries, lastErr)
}
```

### 7.2 快照恢复失败降级

```go
// 恢复失败时自动降级到原有流程
func (p *Pool) AcquireVMWithFallback(ctx context.Context, fn *domain.Function, version int) (*PooledVM, bool, error) {
    // 尝试快照恢复
    pvm, coldStart, err := p.AcquireVM(ctx, fn, version)
    if err == nil {
        return pvm, coldStart, nil
    }

    p.logger.WithError(err).Warn("Snapshot restore failed, falling back to warm pool")

    // 降级到 warm pool
    return p.acquireVMOriginal(ctx, string(fn.Runtime))
}
```

---

## 8. 存储管理

### 8.1 快照文件结构

```
/var/nimbus/snapshots/
├── fn_abc123_sha256abc/           # function_id + code_hash 前缀
│   ├── mem                        # 内存快照（压缩后约 50-200MB）
│   ├── snapshot                   # CPU/设备状态（约 1-5KB）
│   └── metadata.json              # 元数据
├── fn_def456_sha256def/
│   ├── mem
│   ├── snapshot
│   └── metadata.json
└── ...
```

### 8.2 存储估算

| 运行时 | 内存配置 | 内存快照大小 | 状态快照 |
|--------|----------|--------------|----------|
| Python 3.11 | 256MB | ~80-120MB | ~2KB |
| Node.js 20 | 256MB | ~100-150MB | ~2KB |
| Go 1.24 | 128MB | ~40-60MB | ~2KB |
| WASM | 128MB | ~30-50MB | ~2KB |

**存储公式：**
```
总存储 ≈ 函数数 × 平均版本数 × 平均快照大小
       ≈ 1000 × 2 × 100MB
       ≈ 200GB
```

### 8.3 清理策略

1. **TTL 过期**：默认 7 天未使用的快照自动清理
2. **版本限制**：每个函数最多保留 3 个版本的快照
3. **存储配额**：总存储超过阈值时，按 LRU 清理
4. **手动清理**：支持 API 手动删除

---

## 9. 测试计划

### 9.1 单元测试

- [ ] `Manager.hashEnvVars()` 哈希一致性
- [ ] `Manager.buildSnapshot()` 流程测试（使用 mock）
- [ ] 快照文件验证逻辑

### 9.2 集成测试

- [ ] 完整的快照构建 → 恢复流程
- [ ] 快照失效触发（代码更新）
- [ ] 并发构建去重
- [ ] 构建失败重试

### 9.3 性能测试

- [ ] 快照恢复延迟 P50/P95/P99
- [ ] 并发恢复吞吐量
- [ ] 大量快照下的管理开销

### 9.4 稳定性测试

- [ ] 快照文件损坏检测
- [ ] 磁盘空间不足处理
- [ ] 快照恢复失败降级

---

## 10. 实施计划

| 阶段 | 内容 | 预估时间 |
|------|------|----------|
| 1 | 数据库表 + 配置结构 | 0.5 天 |
| 2 | Snapshot Manager 核心实现 | 2 天 |
| 3 | Firecracker 扩展 | 0.5 天 |
| 4 | VM Pool 集成 | 1 天 |
| 5 | Scheduler 集成 | 0.5 天 |
| 6 | API + 监控指标 | 1 天 |
| 7 | 测试 + 调优 | 1.5 天 |
| **总计** | | **7 天** |

---

## 11. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 快照文件损坏 | 恢复失败 | 校验和验证 + 自动降级 |
| 磁盘空间耗尽 | 无法创建新快照 | 配额管理 + 告警 |
| 快照过期影响调用 | 冷启动延迟 | 后台预构建 + LRU |
| 并发构建资源争用 | 构建延迟 | 队列限流 + 优先级 |

---

## 12. 附录

### 12.1 配置示例

```yaml
snapshot:
  enabled: true
  snapshot_dir: /var/nimbus/snapshots
  build_workers: 2
  build_timeout: 60s
  warmup_on_build: true
  snapshot_ttl: 168h  # 7 days
  cleanup_interval: 1h
  max_snapshots_per_function: 3
  max_total_storage_gb: 500
```

### 12.2 CLI 命令

```bash
# 手动构建快照
nimbus snapshot build --function my-function --version 3

# 列出快照
nimbus snapshot list --function my-function

# 删除快照
nimbus snapshot delete --id snap_abc123

# 清理过期快照
nimbus snapshot cleanup --dry-run

# 查看快照统计
nimbus snapshot stats
```
