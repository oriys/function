# 设计文档 001: 函数版本与别名 (Versions & Aliases)

> 版本：1.0
> 状态：草案
> 作者：Claude
> 创建日期：2026-01-29

---

## 1. 概述

### 1.1 背景

当前 Nimbus 平台修改函数会直接覆盖原有代码，缺乏版本历史和灰度发布能力。这对生产环境的稳定性和可追溯性造成影响。

### 1.2 目标

1. **版本管理**：每次部署生成不可变的版本快照 (v1, v2, ...)
2. **别名系统**：支持语义化别名（如 `prod`, `canary`, `latest`）
3. **流量分配**：支持加权随机路由，实现金丝雀发布
4. **无损回滚**：秒级回滚到任意历史版本

### 1.3 现有基础

领域模型已在 `internal/domain/function.go` 中定义：
- `FunctionVersion` (L446-465)
- `FunctionAlias` (L469-486)
- `RoutingConfig` + `VersionWeight` (L488-500)

---

## 2. 架构设计

### 2.1 系统架构

```
┌─────────────────────────────────────────────────────────────────┐
│                         API Gateway                              │
│    POST /functions/{id}/publish  →  创建新版本                   │
│    POST /functions/{id}/invoke?alias=prod  →  通过别名调用        │
└───────────────────────────┬─────────────────────────────────────┘
                            │
┌───────────────────────────▼─────────────────────────────────────┐
│                     Version Manager                              │
│  ┌──────────────────┐  ┌──────────────────┐  ┌───────────────┐ │
│  │  Version Store   │  │   Alias Store    │  │ Traffic Router│ │
│  │  (PostgreSQL)    │  │   (PostgreSQL)   │  │  (内存+Redis) │ │
│  └──────────────────┘  └──────────────────┘  └───────────────┘ │
└───────────────────────────┬─────────────────────────────────────┘
                            │
┌───────────────────────────▼─────────────────────────────────────┐
│                       Scheduler                                  │
│            selectVersion(alias) → 加权随机选择版本号              │
│            loadVersionCode(functionID, version) → 加载版本代码   │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 数据流

```
[发布新版本]
    │
    ├── 1. 创建 FunctionVersion 记录
    │       └── 复制当前 Function 的 code, handler, binary 等
    ├── 2. 递增 Function.Version
    ├── 3. 自动更新 "latest" 别名指向新版本
    └── 4. 返回版本信息

[通过别名调用]
    │
    ├── 1. 解析别名 → FunctionAlias
    ├── 2. 根据 RoutingConfig.Weights 加权随机选择版本
    ├── 3. 加载对应版本的代码
    ├── 4. 执行函数
    └── 5. 记录调用使用的版本号到 Invocation
```

---

## 3. 数据库设计

### 3.1 新增表

```sql
-- ============================================
-- 函数版本表
-- ============================================
CREATE TABLE function_versions (
    id              VARCHAR(36) PRIMARY KEY,
    function_id     VARCHAR(36) NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
    version         INTEGER NOT NULL,

    -- 版本快照数据
    handler         VARCHAR(256) NOT NULL,
    code            TEXT,
    binary          BYTEA,                -- 编译后的二进制（用于 Go/WASM）
    code_hash       VARCHAR(64),
    env_vars        JSONB DEFAULT '{}',
    memory_mb       INTEGER DEFAULT 256,
    timeout_sec     INTEGER DEFAULT 30,

    -- 元数据
    description     TEXT,                 -- 版本描述/发布说明
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    created_by      VARCHAR(64),          -- 发布者

    -- 唯一约束：同一函数的版本号唯一
    UNIQUE(function_id, version)
);

-- 索引优化
CREATE INDEX idx_function_versions_function_id ON function_versions(function_id);
CREATE INDEX idx_function_versions_created_at ON function_versions(created_at DESC);

-- ============================================
-- 函数别名表
-- ============================================
CREATE TABLE function_aliases (
    id              VARCHAR(36) PRIMARY KEY,
    function_id     VARCHAR(36) NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
    name            VARCHAR(64) NOT NULL,
    description     TEXT,

    -- 路由配置 (JSON)
    -- 格式: {"weights": [{"version": 1, "weight": 90}, {"version": 2, "weight": 10}]}
    routing_config  JSONB NOT NULL,

    -- 元数据
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),

    -- 唯一约束：同一函数的别名名称唯一
    UNIQUE(function_id, name)
);

-- 索引优化
CREATE INDEX idx_function_aliases_function_id ON function_aliases(function_id);
CREATE INDEX idx_function_aliases_name ON function_aliases(name);
```

### 3.2 修改现有表

```sql
-- functions 表新增字段
ALTER TABLE functions ADD COLUMN IF NOT EXISTS latest_version INTEGER DEFAULT 1;

-- invocations 表新增字段（记录调用使用的版本）
ALTER TABLE invocations ADD COLUMN IF NOT EXISTS version INTEGER;
ALTER TABLE invocations ADD COLUMN IF NOT EXISTS alias_used VARCHAR(64);
```

---

## 4. API 设计

### 4.1 版本管理 API

#### 发布新版本
```
POST /api/v1/functions/{functionId}/versions
```

**请求体：**
```json
{
  "description": "Fix memory leak in handler",
  "publish_alias": "latest"  // 可选：自动更新别名
}
```

**响应：**
```json
{
  "id": "ver_abc123",
  "function_id": "fn_xyz789",
  "version": 3,
  "code_hash": "sha256:...",
  "description": "Fix memory leak in handler",
  "created_at": "2026-01-29T10:00:00Z"
}
```

#### 列出版本
```
GET /api/v1/functions/{functionId}/versions?limit=20&offset=0
```

**响应：**
```json
{
  "versions": [
    {
      "id": "ver_abc123",
      "version": 3,
      "code_hash": "sha256:...",
      "description": "Fix memory leak",
      "created_at": "2026-01-29T10:00:00Z",
      "created_by": "user@example.com"
    },
    {
      "id": "ver_abc122",
      "version": 2,
      "code_hash": "sha256:...",
      "description": "Add logging",
      "created_at": "2026-01-28T10:00:00Z",
      "created_by": "user@example.com"
    }
  ],
  "total": 3,
  "limit": 20,
  "offset": 0
}
```

#### 获取版本详情
```
GET /api/v1/functions/{functionId}/versions/{version}
```

**响应：**
```json
{
  "id": "ver_abc123",
  "function_id": "fn_xyz789",
  "version": 3,
  "handler": "handler.main",
  "code": "def main(event):\n    return {'status': 'ok'}",
  "code_hash": "sha256:...",
  "env_vars": {"DEBUG": "true"},
  "memory_mb": 256,
  "timeout_sec": 30,
  "description": "Fix memory leak in handler",
  "created_at": "2026-01-29T10:00:00Z",
  "created_by": "user@example.com"
}
```

#### 删除版本
```
DELETE /api/v1/functions/{functionId}/versions/{version}
```

> 注意：被别名引用的版本不能删除

### 4.2 别名管理 API

#### 创建别名
```
POST /api/v1/functions/{functionId}/aliases
```

**请求体：**
```json
{
  "name": "prod",
  "description": "Production alias",
  "routing_config": {
    "weights": [
      {"version": 2, "weight": 100}
    ]
  }
}
```

#### 列出别名
```
GET /api/v1/functions/{functionId}/aliases
```

**响应：**
```json
{
  "aliases": [
    {
      "id": "alias_001",
      "name": "prod",
      "description": "Production alias",
      "routing_config": {
        "weights": [{"version": 2, "weight": 100}]
      },
      "created_at": "2026-01-20T10:00:00Z",
      "updated_at": "2026-01-29T10:00:00Z"
    },
    {
      "id": "alias_002",
      "name": "canary",
      "description": "Canary deployment",
      "routing_config": {
        "weights": [
          {"version": 2, "weight": 90},
          {"version": 3, "weight": 10}
        ]
      }
    },
    {
      "id": "alias_003",
      "name": "latest",
      "description": "Auto-updated to latest version",
      "routing_config": {
        "weights": [{"version": 3, "weight": 100}]
      }
    }
  ]
}
```

#### 更新别名路由
```
PUT /api/v1/functions/{functionId}/aliases/{aliasName}
```

**请求体（金丝雀发布）：**
```json
{
  "routing_config": {
    "weights": [
      {"version": 2, "weight": 90},
      {"version": 3, "weight": 10}
    ]
  }
}
```

#### 删除别名
```
DELETE /api/v1/functions/{functionId}/aliases/{aliasName}
```

> 注意：`latest` 别名不能删除

### 4.3 调用 API 增强

#### 通过别名调用
```
POST /api/v1/functions/{functionId}/invoke?alias=prod
```

#### 通过指定版本调用
```
POST /api/v1/functions/{functionId}/invoke?version=2
```

#### 调用响应增强
```json
{
  "request_id": "inv_abc123",
  "status_code": 200,
  "body": {"result": "success"},
  "duration_ms": 45,
  "cold_start": false,
  "version": 2,           // 新增：实际执行的版本
  "alias_used": "prod"    // 新增：使用的别名（如果有）
}
```

---

## 5. 核心实现

### 5.1 存储层实现

**文件：** `internal/storage/postgres.go`

```go
// ==================== 版本存储接口 ====================

// CreateVersion 创建新版本
func (s *PostgresStore) CreateVersion(ctx context.Context, v *domain.FunctionVersion) error {
    query := `
        INSERT INTO function_versions
        (id, function_id, version, handler, code, binary, code_hash,
         env_vars, memory_mb, timeout_sec, description, created_at, created_by)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`

    envVarsJSON, _ := json.Marshal(v.EnvVars)

    _, err := s.db.ExecContext(ctx, query,
        v.ID, v.FunctionID, v.Version, v.Handler, v.Code, v.Binary,
        v.CodeHash, envVarsJSON, v.MemoryMB, v.TimeoutSec,
        v.Description, v.CreatedAt, v.CreatedBy)

    return err
}

// GetVersion 获取指定版本
func (s *PostgresStore) GetVersion(ctx context.Context, functionID string, version int) (*domain.FunctionVersion, error) {
    query := `
        SELECT id, function_id, version, handler, code, binary, code_hash,
               env_vars, memory_mb, timeout_sec, description, created_at, created_by
        FROM function_versions
        WHERE function_id = $1 AND version = $2`

    var v domain.FunctionVersion
    var envVarsJSON []byte

    err := s.db.QueryRowContext(ctx, query, functionID, version).Scan(
        &v.ID, &v.FunctionID, &v.Version, &v.Handler, &v.Code, &v.Binary,
        &v.CodeHash, &envVarsJSON, &v.MemoryMB, &v.TimeoutSec,
        &v.Description, &v.CreatedAt, &v.CreatedBy)

    if err == sql.ErrNoRows {
        return nil, domain.ErrVersionNotFound
    }
    if err != nil {
        return nil, err
    }

    json.Unmarshal(envVarsJSON, &v.EnvVars)
    return &v, nil
}

// ListVersions 列出函数的所有版本
func (s *PostgresStore) ListVersions(ctx context.Context, functionID string, offset, limit int) ([]*domain.FunctionVersion, int, error) {
    // 获取总数
    var total int
    countQuery := `SELECT COUNT(*) FROM function_versions WHERE function_id = $1`
    s.db.QueryRowContext(ctx, countQuery, functionID).Scan(&total)

    // 获取版本列表（不包含 code/binary 以节省带宽）
    query := `
        SELECT id, function_id, version, handler, code_hash,
               description, created_at, created_by
        FROM function_versions
        WHERE function_id = $1
        ORDER BY version DESC
        LIMIT $2 OFFSET $3`

    rows, err := s.db.QueryContext(ctx, query, functionID, limit, offset)
    if err != nil {
        return nil, 0, err
    }
    defer rows.Close()

    var versions []*domain.FunctionVersion
    for rows.Next() {
        var v domain.FunctionVersion
        rows.Scan(&v.ID, &v.FunctionID, &v.Version, &v.Handler,
                  &v.CodeHash, &v.Description, &v.CreatedAt, &v.CreatedBy)
        versions = append(versions, &v)
    }

    return versions, total, nil
}

// DeleteVersion 删除版本
func (s *PostgresStore) DeleteVersion(ctx context.Context, functionID string, version int) error {
    // 检查是否被别名引用
    checkQuery := `
        SELECT COUNT(*) FROM function_aliases
        WHERE function_id = $1
        AND routing_config @> $2::jsonb`

    pattern := fmt.Sprintf(`{"weights":[{"version":%d}]}`, version)
    var count int
    s.db.QueryRowContext(ctx, checkQuery, functionID, pattern).Scan(&count)

    if count > 0 {
        return domain.ErrVersionInUse
    }

    query := `DELETE FROM function_versions WHERE function_id = $1 AND version = $2`
    _, err := s.db.ExecContext(ctx, query, functionID, version)
    return err
}

// ==================== 别名存储接口 ====================

// CreateAlias 创建别名
func (s *PostgresStore) CreateAlias(ctx context.Context, a *domain.FunctionAlias) error {
    query := `
        INSERT INTO function_aliases
        (id, function_id, name, description, routing_config, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)`

    routingJSON, _ := json.Marshal(a.RoutingConfig)

    _, err := s.db.ExecContext(ctx, query,
        a.ID, a.FunctionID, a.Name, a.Description,
        routingJSON, a.CreatedAt, a.UpdatedAt)

    return err
}

// GetAlias 获取别名
func (s *PostgresStore) GetAlias(ctx context.Context, functionID, name string) (*domain.FunctionAlias, error) {
    query := `
        SELECT id, function_id, name, description, routing_config, created_at, updated_at
        FROM function_aliases
        WHERE function_id = $1 AND name = $2`

    var a domain.FunctionAlias
    var routingJSON []byte

    err := s.db.QueryRowContext(ctx, query, functionID, name).Scan(
        &a.ID, &a.FunctionID, &a.Name, &a.Description,
        &routingJSON, &a.CreatedAt, &a.UpdatedAt)

    if err == sql.ErrNoRows {
        return nil, domain.ErrAliasNotFound
    }
    if err != nil {
        return nil, err
    }

    json.Unmarshal(routingJSON, &a.RoutingConfig)
    return &a, nil
}

// UpdateAliasRouting 更新别名路由配置
func (s *PostgresStore) UpdateAliasRouting(ctx context.Context, functionID, name string, config domain.RoutingConfig) error {
    // 验证所有版本存在
    for _, w := range config.Weights {
        exists, _ := s.VersionExists(ctx, functionID, w.Version)
        if !exists {
            return fmt.Errorf("version %d not found", w.Version)
        }
    }

    // 验证权重总和为 100
    totalWeight := 0
    for _, w := range config.Weights {
        totalWeight += w.Weight
    }
    if totalWeight != 100 {
        return domain.ErrInvalidWeights
    }

    query := `
        UPDATE function_aliases
        SET routing_config = $1, updated_at = NOW()
        WHERE function_id = $2 AND name = $3`

    routingJSON, _ := json.Marshal(config)
    _, err := s.db.ExecContext(ctx, query, routingJSON, functionID, name)
    return err
}

// ListAliases 列出函数的所有别名
func (s *PostgresStore) ListAliases(ctx context.Context, functionID string) ([]*domain.FunctionAlias, error) {
    query := `
        SELECT id, function_id, name, description, routing_config, created_at, updated_at
        FROM function_aliases
        WHERE function_id = $1
        ORDER BY name`

    rows, err := s.db.QueryContext(ctx, query, functionID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var aliases []*domain.FunctionAlias
    for rows.Next() {
        var a domain.FunctionAlias
        var routingJSON []byte
        rows.Scan(&a.ID, &a.FunctionID, &a.Name, &a.Description,
                  &routingJSON, &a.CreatedAt, &a.UpdatedAt)
        json.Unmarshal(routingJSON, &a.RoutingConfig)
        aliases = append(aliases, &a)
    }

    return aliases, nil
}
```

### 5.2 流量路由实现

**文件：** `internal/scheduler/router.go` (新文件)

```go
package scheduler

import (
    "context"
    "math/rand"
    "sync"
    "time"

    "nimbus/internal/domain"
    "nimbus/internal/storage"
)

// TrafficRouter 流量路由器
type TrafficRouter struct {
    store     *storage.PostgresStore
    cache     map[string]*cachedAlias  // functionID:aliasName -> alias
    cacheMu   sync.RWMutex
    cacheTTL  time.Duration
    rng       *rand.Rand
}

type cachedAlias struct {
    alias     *domain.FunctionAlias
    expiresAt time.Time
}

func NewTrafficRouter(store *storage.PostgresStore) *TrafficRouter {
    return &TrafficRouter{
        store:    store,
        cache:    make(map[string]*cachedAlias),
        cacheTTL: 30 * time.Second,  // 别名配置缓存 30 秒
        rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
    }
}

// SelectVersion 根据别名选择版本
func (r *TrafficRouter) SelectVersion(ctx context.Context, functionID, aliasName string) (int, error) {
    alias, err := r.getAlias(ctx, functionID, aliasName)
    if err != nil {
        return 0, err
    }

    return r.weightedSelect(alias.RoutingConfig.Weights), nil
}

// getAlias 获取别名（带缓存）
func (r *TrafficRouter) getAlias(ctx context.Context, functionID, aliasName string) (*domain.FunctionAlias, error) {
    cacheKey := functionID + ":" + aliasName

    // 检查缓存
    r.cacheMu.RLock()
    if cached, ok := r.cache[cacheKey]; ok && time.Now().Before(cached.expiresAt) {
        r.cacheMu.RUnlock()
        return cached.alias, nil
    }
    r.cacheMu.RUnlock()

    // 从数据库加载
    alias, err := r.store.GetAlias(ctx, functionID, aliasName)
    if err != nil {
        return nil, err
    }

    // 更新缓存
    r.cacheMu.Lock()
    r.cache[cacheKey] = &cachedAlias{
        alias:     alias,
        expiresAt: time.Now().Add(r.cacheTTL),
    }
    r.cacheMu.Unlock()

    return alias, nil
}

// weightedSelect 加权随机选择
func (r *TrafficRouter) weightedSelect(weights []domain.VersionWeight) int {
    if len(weights) == 0 {
        return 0
    }
    if len(weights) == 1 {
        return weights[0].Version
    }

    // 生成 0-99 的随机数
    random := r.rng.Intn(100)

    // 累计权重选择
    cumulative := 0
    for _, w := range weights {
        cumulative += w.Weight
        if random < cumulative {
            return w.Version
        }
    }

    // 兜底返回第一个
    return weights[0].Version
}

// InvalidateCache 使缓存失效（当别名更新时调用）
func (r *TrafficRouter) InvalidateCache(functionID, aliasName string) {
    cacheKey := functionID + ":" + aliasName
    r.cacheMu.Lock()
    delete(r.cache, cacheKey)
    r.cacheMu.Unlock()
}
```

### 5.3 调度器集成

**文件：** `internal/scheduler/scheduler.go` (修改)

```go
// 在 Scheduler 结构体中添加
type Scheduler struct {
    // ... 现有字段 ...
    router *TrafficRouter  // 新增
}

// 修改 Invoke 方法
func (s *Scheduler) Invoke(ctx context.Context, fn *domain.Function, req *domain.InvokeRequest) (*domain.InvokeResponse, error) {
    // 确定要执行的版本
    version, aliasUsed, err := s.resolveVersion(ctx, fn.ID, req)
    if err != nil {
        return nil, err
    }

    // 加载版本代码
    versionData, err := s.store.GetVersion(ctx, fn.ID, version)
    if err != nil {
        return nil, fmt.Errorf("failed to load version %d: %w", version, err)
    }

    // 创建调用记录，包含版本信息
    inv := domain.NewInvocation(fn.ID, fn.Name, domain.TriggerHTTP, req.Payload)
    inv.Version = version
    inv.AliasUsed = aliasUsed

    // ... 后续执行逻辑 ...

    // 使用 versionData 的代码/配置执行
    initPayload := &InitPayload{
        FunctionID: fn.ID,
        Handler:    versionData.Handler,
        Code:       versionData.Code,
        Binary:     versionData.Binary,
        Runtime:    fn.Runtime,
        EnvVars:    versionData.EnvVars,
        MemoryMB:   versionData.MemoryMB,
        TimeoutSec: versionData.TimeoutSec,
    }

    // ... 继续执行 ...
}

// resolveVersion 解析要执行的版本
func (s *Scheduler) resolveVersion(ctx context.Context, functionID string, req *domain.InvokeRequest) (version int, alias string, err error) {
    // 优先级：显式指定版本 > 别名 > 默认 latest

    if req.Version > 0 {
        // 显式指定版本
        return req.Version, "", nil
    }

    aliasName := req.Alias
    if aliasName == "" {
        aliasName = "latest"  // 默认使用 latest 别名
    }

    version, err = s.router.SelectVersion(ctx, functionID, aliasName)
    if err != nil {
        return 0, "", err
    }

    return version, aliasName, nil
}
```

### 5.4 API Handler 实现

**文件：** `internal/api/version_handler.go` (新文件)

```go
package api

import (
    "encoding/json"
    "net/http"
    "strconv"
    "time"

    "github.com/google/uuid"
    "github.com/gorilla/mux"
    "nimbus/internal/domain"
)

// PublishVersion 发布新版本
func (h *Handler) PublishVersion(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    functionID := vars["id"]

    // 获取函数
    fn, err := h.store.GetFunctionByID(r.Context(), functionID)
    if err != nil {
        h.respondError(w, http.StatusNotFound, "Function not found")
        return
    }

    // 解析请求
    var req struct {
        Description  string `json:"description"`
        PublishAlias string `json:"publish_alias"`
    }
    json.NewDecoder(r.Body).Decode(&req)

    // 创建版本快照
    newVersion := fn.Version + 1
    version := &domain.FunctionVersion{
        ID:          uuid.New().String(),
        FunctionID:  functionID,
        Version:     newVersion,
        Handler:     fn.Handler,
        Code:        fn.Code,
        Binary:      fn.Binary,
        CodeHash:    fn.CodeHash,
        EnvVars:     fn.EnvVars,
        MemoryMB:    fn.MemoryMB,
        TimeoutSec:  fn.TimeoutSec,
        Description: req.Description,
        CreatedAt:   time.Now(),
        CreatedBy:   r.Header.Get("X-User-ID"), // 从认证中获取
    }

    // 开始事务
    tx, _ := h.store.BeginTx(r.Context())
    defer tx.Rollback()

    // 保存版本
    if err := h.store.CreateVersionTx(tx, version); err != nil {
        h.respondError(w, http.StatusInternalServerError, "Failed to create version")
        return
    }

    // 更新函数的版本号
    if err := h.store.UpdateFunctionVersionTx(tx, functionID, newVersion); err != nil {
        h.respondError(w, http.StatusInternalServerError, "Failed to update function")
        return
    }

    // 更新 latest 别名
    latestConfig := domain.RoutingConfig{
        Weights: []domain.VersionWeight{{Version: newVersion, Weight: 100}},
    }
    if err := h.store.UpsertAliasTx(tx, functionID, "latest", latestConfig); err != nil {
        h.respondError(w, http.StatusInternalServerError, "Failed to update latest alias")
        return
    }

    // 如果指定了其他别名，也更新
    if req.PublishAlias != "" && req.PublishAlias != "latest" {
        aliasConfig := domain.RoutingConfig{
            Weights: []domain.VersionWeight{{Version: newVersion, Weight: 100}},
        }
        if err := h.store.UpsertAliasTx(tx, functionID, req.PublishAlias, aliasConfig); err != nil {
            h.respondError(w, http.StatusInternalServerError, "Failed to update alias")
            return
        }
    }

    tx.Commit()

    // 使缓存失效
    h.scheduler.Router().InvalidateCache(functionID, "latest")
    if req.PublishAlias != "" {
        h.scheduler.Router().InvalidateCache(functionID, req.PublishAlias)
    }

    h.respondJSON(w, http.StatusCreated, version)
}

// ListVersions 列出版本
func (h *Handler) ListVersions(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    functionID := vars["id"]

    limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
    offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
    if limit <= 0 {
        limit = 20
    }

    versions, total, err := h.store.ListVersions(r.Context(), functionID, offset, limit)
    if err != nil {
        h.respondError(w, http.StatusInternalServerError, "Failed to list versions")
        return
    }

    h.respondJSON(w, http.StatusOK, map[string]interface{}{
        "versions": versions,
        "total":    total,
        "limit":    limit,
        "offset":   offset,
    })
}

// GetVersion 获取版本详情
func (h *Handler) GetVersion(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    functionID := vars["id"]
    version, _ := strconv.Atoi(vars["version"])

    v, err := h.store.GetVersion(r.Context(), functionID, version)
    if err != nil {
        h.respondError(w, http.StatusNotFound, "Version not found")
        return
    }

    h.respondJSON(w, http.StatusOK, v)
}

// CreateAlias 创建别名
func (h *Handler) CreateAlias(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    functionID := vars["id"]

    var req domain.CreateAliasRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        h.respondError(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    // 验证权重总和
    totalWeight := 0
    for _, w := range req.RoutingConfig.Weights {
        totalWeight += w.Weight
    }
    if totalWeight != 100 {
        h.respondError(w, http.StatusBadRequest, "Weights must sum to 100")
        return
    }

    alias := &domain.FunctionAlias{
        ID:            uuid.New().String(),
        FunctionID:    functionID,
        Name:          req.Name,
        Description:   req.Description,
        RoutingConfig: req.RoutingConfig,
        CreatedAt:     time.Now(),
        UpdatedAt:     time.Now(),
    }

    if err := h.store.CreateAlias(r.Context(), alias); err != nil {
        h.respondError(w, http.StatusInternalServerError, "Failed to create alias")
        return
    }

    h.respondJSON(w, http.StatusCreated, alias)
}

// UpdateAliasRouting 更新别名路由
func (h *Handler) UpdateAliasRouting(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    functionID := vars["id"]
    aliasName := vars["alias"]

    var req domain.UpdateAliasRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        h.respondError(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    if req.RoutingConfig != nil {
        // 验证权重总和
        totalWeight := 0
        for _, w := range req.RoutingConfig.Weights {
            totalWeight += w.Weight
        }
        if totalWeight != 100 {
            h.respondError(w, http.StatusBadRequest, "Weights must sum to 100")
            return
        }

        if err := h.store.UpdateAliasRouting(r.Context(), functionID, aliasName, *req.RoutingConfig); err != nil {
            h.respondError(w, http.StatusInternalServerError, "Failed to update alias")
            return
        }

        // 使缓存失效
        h.scheduler.Router().InvalidateCache(functionID, aliasName)
    }

    // 返回更新后的别名
    alias, _ := h.store.GetAlias(r.Context(), functionID, aliasName)
    h.respondJSON(w, http.StatusOK, alias)
}

// ListAliases 列出别名
func (h *Handler) ListAliases(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    functionID := vars["id"]

    aliases, err := h.store.ListAliases(r.Context(), functionID)
    if err != nil {
        h.respondError(w, http.StatusInternalServerError, "Failed to list aliases")
        return
    }

    h.respondJSON(w, http.StatusOK, map[string]interface{}{
        "aliases": aliases,
    })
}
```

### 5.5 路由注册

**文件：** `internal/api/router.go` (修改)

```go
// 在 SetupRoutes 中添加
func (h *Handler) SetupRoutes(r *mux.Router) {
    // ... 现有路由 ...

    // 版本管理
    r.HandleFunc("/api/v1/functions/{id}/versions", h.PublishVersion).Methods("POST")
    r.HandleFunc("/api/v1/functions/{id}/versions", h.ListVersions).Methods("GET")
    r.HandleFunc("/api/v1/functions/{id}/versions/{version}", h.GetVersion).Methods("GET")
    r.HandleFunc("/api/v1/functions/{id}/versions/{version}", h.DeleteVersion).Methods("DELETE")

    // 别名管理
    r.HandleFunc("/api/v1/functions/{id}/aliases", h.CreateAlias).Methods("POST")
    r.HandleFunc("/api/v1/functions/{id}/aliases", h.ListAliases).Methods("GET")
    r.HandleFunc("/api/v1/functions/{id}/aliases/{alias}", h.GetAlias).Methods("GET")
    r.HandleFunc("/api/v1/functions/{id}/aliases/{alias}", h.UpdateAliasRouting).Methods("PUT")
    r.HandleFunc("/api/v1/functions/{id}/aliases/{alias}", h.DeleteAlias).Methods("DELETE")
}
```

---

## 6. 错误处理

### 6.1 新增错误类型

**文件：** `internal/domain/errors.go`

```go
var (
    // 版本相关错误
    ErrVersionNotFound   = errors.New("version not found")
    ErrVersionInUse      = errors.New("version is in use by an alias")
    ErrInvalidVersion    = errors.New("invalid version number")

    // 别名相关错误
    ErrAliasNotFound     = errors.New("alias not found")
    ErrAliasExists       = errors.New("alias already exists")
    ErrInvalidWeights    = errors.New("weights must sum to 100")
    ErrCannotDeleteLatest = errors.New("cannot delete 'latest' alias")
)
```

---

## 7. 迁移策略

### 7.1 数据迁移

对于现有函数，需要创建初始版本：

```sql
-- 为所有现有函数创建 v1 版本
INSERT INTO function_versions (id, function_id, version, handler, code, binary, code_hash,
                                env_vars, memory_mb, timeout_sec, created_at)
SELECT
    gen_random_uuid()::text,
    id,
    1,
    handler,
    code,
    binary,
    code_hash,
    env_vars,
    memory_mb,
    timeout_sec,
    created_at
FROM functions
WHERE NOT EXISTS (
    SELECT 1 FROM function_versions WHERE function_versions.function_id = functions.id
);

-- 为所有现有函数创建 latest 别名
INSERT INTO function_aliases (id, function_id, name, routing_config, created_at, updated_at)
SELECT
    gen_random_uuid()::text,
    id,
    'latest',
    '{"weights":[{"version":1,"weight":100}]}'::jsonb,
    NOW(),
    NOW()
FROM functions
WHERE NOT EXISTS (
    SELECT 1 FROM function_aliases
    WHERE function_aliases.function_id = functions.id AND name = 'latest'
);
```

### 7.2 API 兼容性

- 现有 `/invoke` 接口保持兼容，默认使用 `latest` 别名
- 新增可选的 `?alias=` 和 `?version=` 查询参数

---

## 8. 监控指标

### 8.1 新增 Prometheus 指标

```go
var (
    versionPublishTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "nimbus_version_publish_total",
            Help: "Total number of version publishes",
        },
        []string{"function_id", "runtime"},
    )

    aliasUpdateTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "nimbus_alias_update_total",
            Help: "Total number of alias updates",
        },
        []string{"function_id", "alias"},
    )

    invocationByVersion = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "nimbus_invocation_by_version_total",
            Help: "Total invocations by version",
        },
        []string{"function_id", "version", "alias"},
    )
)
```

---

## 9. 测试计划

### 9.1 单元测试

- [ ] `TrafficRouter.weightedSelect()` 权重分配测试
- [ ] `PostgresStore` 版本/别名 CRUD 测试
- [ ] 版本删除时的引用检查测试

### 9.2 集成测试

- [ ] 发布新版本 → 自动更新 latest
- [ ] 金丝雀发布流程（90/10 分流）
- [ ] 版本回滚流程
- [ ] 并发调用时的版本一致性

### 9.3 性能测试

- [ ] 别名缓存命中率
- [ ] 高并发下的流量分配准确性
- [ ] 数据库版本表的查询性能

---

## 10. 实施计划

| 阶段 | 内容 | 预估时间 |
|------|------|----------|
| 1 | 数据库表创建 + 迁移脚本 | 0.5 天 |
| 2 | 存储层实现 (CRUD) | 1 天 |
| 3 | TrafficRouter 实现 | 0.5 天 |
| 4 | 调度器集成 | 1 天 |
| 5 | API Handler 实现 | 1 天 |
| 6 | 单元测试 + 集成测试 | 1 天 |
| 7 | 文档更新 | 0.5 天 |
| **总计** | | **5.5 天** |

---

## 11. 附录

### 11.1 金丝雀发布示例

```bash
# 1. 发布新版本
curl -X POST http://localhost:8080/api/v1/functions/fn_123/versions \
  -H "Content-Type: application/json" \
  -d '{"description": "Add new feature"}'

# 响应: {"version": 3, ...}

# 2. 创建金丝雀别名（10% 流量到新版本）
curl -X PUT http://localhost:8080/api/v1/functions/fn_123/aliases/canary \
  -H "Content-Type: application/json" \
  -d '{
    "routing_config": {
      "weights": [
        {"version": 2, "weight": 90},
        {"version": 3, "weight": 10}
      ]
    }
  }'

# 3. 通过 canary 别名调用
curl -X POST http://localhost:8080/api/v1/functions/fn_123/invoke?alias=canary \
  -d '{"input": "test"}'

# 4. 逐步增加新版本流量
curl -X PUT http://localhost:8080/api/v1/functions/fn_123/aliases/canary \
  -d '{"routing_config": {"weights": [{"version": 2, "weight": 50}, {"version": 3, "weight": 50}]}}'

# 5. 完全切换到新版本
curl -X PUT http://localhost:8080/api/v1/functions/fn_123/aliases/prod \
  -d '{"routing_config": {"weights": [{"version": 3, "weight": 100}]}}'
```

### 11.2 相关文件清单

| 文件 | 操作 | 描述 |
|------|------|------|
| `internal/domain/function.go` | 已有 | 领域模型（无需修改） |
| `internal/domain/errors.go` | 修改 | 添加错误类型 |
| `internal/storage/postgres.go` | 修改 | 添加存储方法 |
| `internal/scheduler/router.go` | 新增 | 流量路由器 |
| `internal/scheduler/scheduler.go` | 修改 | 集成版本选择 |
| `internal/api/version_handler.go` | 新增 | API Handler |
| `internal/api/router.go` | 修改 | 路由注册 |
| `migrations/007_versions_aliases.sql` | 新增 | 数据库迁移 |
