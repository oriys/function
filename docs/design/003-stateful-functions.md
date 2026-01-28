# 设计文档 003: 有状态函数 (Stateful Functions)

> 版本：1.0
> 状态：草案
> 作者：Claude
> 创建日期：2026-01-29

---

## 1. 概述

### 1.1 背景

Serverless 函数通常是无状态的，每次调用独立执行。但在某些场景下，函数需要在多次调用间保持状态：

- **游戏后端**：玩家会话状态、游戏进度
- **购物车**：用户购物车内容
- **对话 AI**：多轮对话上下文
- **工作流编排**：长时间运行的任务状态
- **计数器/限流**：API 调用计数

### 1.2 目标

1. **State API**：为函数提供简单的状态读写接口
2. **会话亲和性**：相同 session_key 的请求路由到同一 VM
3. **状态持久化**：基于 Redis 实现跨调用状态保存
4. **TTL 支持**：状态自动过期清理
5. **一致性保证**：支持原子操作和乐观锁

### 1.3 现有基础

- `internal/storage/redis.go`：Redis 连接池已存在
- `cmd/agent/main.go`：Agent 支持扩展消息类型

---

## 2. 架构设计

### 2.1 整体架构

```
┌─────────────────────────────────────────────────────────────────┐
│                         API Gateway                              │
│     POST /invoke?session_key=user_123                           │
└───────────────────────────┬─────────────────────────────────────┘
                            │
┌───────────────────────────▼─────────────────────────────────────┐
│                    Stateful Router                               │
│  ┌───────────────────┐  ┌───────────────────┐                   │
│  │ Session Registry  │  │ Consistent Hash   │                   │
│  │    (Redis)        │  │    Ring           │                   │
│  └───────────────────┘  └───────────────────┘                   │
│            │                      │                              │
│            └──────────┬───────────┘                              │
│                       ▼                                          │
│              Route to Target VM                                  │
└───────────────────────────┬─────────────────────────────────────┘
                            │
┌───────────────────────────▼─────────────────────────────────────┐
│                        VM (Agent)                                │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │                    State Client                            │ │
│  │  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────────┐  │ │
│  │  │   Get   │  │   Set   │  │  Delete │  │  Atomic Ops │  │ │
│  │  └─────────┘  └─────────┘  └─────────┘  └─────────────┘  │ │
│  └───────────────────────────────────────────────────────────┘ │
│                            │                                     │
│                            ▼                                     │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │                   Function Code                            │ │
│  │    state.get("counter")                                    │ │
│  │    state.set("counter", value, ttl=3600)                   │ │
│  │    state.incr("counter")                                   │ │
│  └───────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│                         Redis                                    │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │  Key: state:{function_id}:{session_key}:{user_key}        │ │
│  │  Value: JSON/Binary data                                   │ │
│  │  TTL: Configurable per key                                 │ │
│  └───────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 状态作用域

```
┌─────────────────────────────────────────────────────────────────┐
│                      State Scopes                                │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  1. Session State (会话级)                                       │
│     Key: state:fn_abc:{session_key}:*                           │
│     Scope: 同一 session_key 的所有调用共享                        │
│     Use Case: 购物车、对话上下文                                  │
│                                                                  │
│  2. Function State (函数级)                                      │
│     Key: state:fn_abc:_global:*                                 │
│     Scope: 函数的所有调用共享                                     │
│     Use Case: 全局配置、计数器                                    │
│                                                                  │
│  3. Invocation State (调用级)                                    │
│     Key: state:fn_abc:{invocation_id}:*                         │
│     Scope: 单次调用内                                            │
│     Use Case: 临时变量、中间结果                                  │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 2.3 会话路由策略

```
[Stateless Mode - 默认]
    调用请求 → 负载均衡 → 任意可用 VM

[Stateful Mode - 启用 session_key]
    调用请求 → 一致性哈希 → 特定 VM
                   │
                   ├── VM 存活 → 路由到该 VM
                   │
                   └── VM 不存在 → 创建新 VM
                                   └── 从 Redis 加载状态
```

---

## 3. 数据模型

### 3.1 函数配置扩展

```go
// Function 扩展字段
type Function struct {
    // ... 现有字段 ...

    // 状态配置
    StateConfig *StateConfig `json:"state_config,omitempty"`
}

// StateConfig 状态配置
type StateConfig struct {
    // 是否启用状态功能
    Enabled bool `json:"enabled"`

    // 默认 TTL（秒），0 表示永不过期
    DefaultTTL int `json:"default_ttl,omitempty"`

    // 最大状态大小（字节）
    MaxStateSize int `json:"max_state_size,omitempty"`

    // 最大 key 数量（每个 session）
    MaxKeys int `json:"max_keys,omitempty"`

    // 是否启用会话亲和性
    SessionAffinity bool `json:"session_affinity,omitempty"`

    // 会话超时（秒）
    SessionTimeout int `json:"session_timeout,omitempty"`
}
```

### 3.2 调用请求扩展

```go
// InvokeRequest 扩展字段
type InvokeRequest struct {
    // ... 现有字段 ...

    // 会话标识（用于状态隔离和亲和性路由）
    SessionKey string `json:"session_key,omitempty"`
}
```

### 3.3 Redis Key 结构

```
# 会话状态
state:{function_id}:{session_key}:{user_key}
例: state:fn_abc:user_123:cart

# 函数全局状态
state:{function_id}:_global:{user_key}
例: state:fn_abc:_global:total_invocations

# 会话元数据
session:{function_id}:{session_key}
例: session:fn_abc:user_123
内容: {"vm_id": "vm_xyz", "created_at": "...", "last_access": "..."}

# VM 会话映射
vm_sessions:{vm_id}
例: vm_sessions:vm_xyz
类型: Set
成员: ["fn_abc:user_123", "fn_abc:user_456"]
```

---

## 4. 核心实现

### 4.1 State Client (Agent 侧)

**文件：** `cmd/agent/state.go` (新文件)

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "time"
)

// StateClient 提供给函数使用的状态 API
type StateClient struct {
    functionID   string
    sessionKey   string
    scope        string  // "session", "function", "invocation"
    sendMessage  func(msg *StateMessage) (*StateResponse, error)
}

// StateMessage 状态操作消息
type StateMessage struct {
    Operation  string `json:"operation"`  // get, set, delete, incr, decr, exists, keys, expire
    Scope      string `json:"scope"`      // session, function, invocation
    Key        string `json:"key"`
    Value      []byte `json:"value,omitempty"`
    TTL        int    `json:"ttl,omitempty"`         // 秒
    Delta      int64  `json:"delta,omitempty"`       // 用于 incr/decr
    Version    int64  `json:"version,omitempty"`     // 用于乐观锁
}

// StateResponse 状态操作响应
type StateResponse struct {
    Success bool            `json:"success"`
    Value   json.RawMessage `json:"value,omitempty"`
    Version int64           `json:"version,omitempty"`
    Error   string          `json:"error,omitempty"`
}

// NewStateClient 创建状态客户端
func NewStateClient(functionID, sessionKey string, sendFn func(*StateMessage) (*StateResponse, error)) *StateClient {
    return &StateClient{
        functionID:  functionID,
        sessionKey:  sessionKey,
        scope:       "session",
        sendMessage: sendFn,
    }
}

// WithScope 设置作用域
func (c *StateClient) WithScope(scope string) *StateClient {
    return &StateClient{
        functionID:  c.functionID,
        sessionKey:  c.sessionKey,
        scope:       scope,
        sendMessage: c.sendMessage,
    }
}

// Get 获取状态值
func (c *StateClient) Get(key string) ([]byte, error) {
    resp, err := c.sendMessage(&StateMessage{
        Operation: "get",
        Scope:     c.scope,
        Key:       key,
    })
    if err != nil {
        return nil, err
    }
    if !resp.Success {
        return nil, fmt.Errorf(resp.Error)
    }
    return resp.Value, nil
}

// GetJSON 获取 JSON 状态值
func (c *StateClient) GetJSON(key string, v interface{}) error {
    data, err := c.Get(key)
    if err != nil {
        return err
    }
    if data == nil {
        return nil // key 不存在
    }
    return json.Unmarshal(data, v)
}

// Set 设置状态值
func (c *StateClient) Set(key string, value []byte, ttl time.Duration) error {
    resp, err := c.sendMessage(&StateMessage{
        Operation: "set",
        Scope:     c.scope,
        Key:       key,
        Value:     value,
        TTL:       int(ttl.Seconds()),
    })
    if err != nil {
        return err
    }
    if !resp.Success {
        return fmt.Errorf(resp.Error)
    }
    return nil
}

// SetJSON 设置 JSON 状态值
func (c *StateClient) SetJSON(key string, value interface{}, ttl time.Duration) error {
    data, err := json.Marshal(value)
    if err != nil {
        return err
    }
    return c.Set(key, data, ttl)
}

// Delete 删除状态
func (c *StateClient) Delete(key string) error {
    resp, err := c.sendMessage(&StateMessage{
        Operation: "delete",
        Scope:     c.scope,
        Key:       key,
    })
    if err != nil {
        return err
    }
    if !resp.Success {
        return fmt.Errorf(resp.Error)
    }
    return nil
}

// Incr 原子递增
func (c *StateClient) Incr(key string) (int64, error) {
    return c.IncrBy(key, 1)
}

// IncrBy 原子递增指定值
func (c *StateClient) IncrBy(key string, delta int64) (int64, error) {
    resp, err := c.sendMessage(&StateMessage{
        Operation: "incr",
        Scope:     c.scope,
        Key:       key,
        Delta:     delta,
    })
    if err != nil {
        return 0, err
    }
    if !resp.Success {
        return 0, fmt.Errorf(resp.Error)
    }

    var result int64
    json.Unmarshal(resp.Value, &result)
    return result, nil
}

// Decr 原子递减
func (c *StateClient) Decr(key string) (int64, error) {
    return c.IncrBy(key, -1)
}

// Exists 检查 key 是否存在
func (c *StateClient) Exists(key string) (bool, error) {
    resp, err := c.sendMessage(&StateMessage{
        Operation: "exists",
        Scope:     c.scope,
        Key:       key,
    })
    if err != nil {
        return false, err
    }
    if !resp.Success {
        return false, fmt.Errorf(resp.Error)
    }

    var exists bool
    json.Unmarshal(resp.Value, &exists)
    return exists, nil
}

// Keys 列出所有 key（支持模式匹配）
func (c *StateClient) Keys(pattern string) ([]string, error) {
    resp, err := c.sendMessage(&StateMessage{
        Operation: "keys",
        Scope:     c.scope,
        Key:       pattern,
    })
    if err != nil {
        return nil, err
    }
    if !resp.Success {
        return nil, fmt.Errorf(resp.Error)
    }

    var keys []string
    json.Unmarshal(resp.Value, &keys)
    return keys, nil
}

// SetWithVersion 带版本的设置（乐观锁）
func (c *StateClient) SetWithVersion(key string, value []byte, version int64, ttl time.Duration) (int64, error) {
    resp, err := c.sendMessage(&StateMessage{
        Operation: "set_with_version",
        Scope:     c.scope,
        Key:       key,
        Value:     value,
        Version:   version,
        TTL:       int(ttl.Seconds()),
    })
    if err != nil {
        return 0, err
    }
    if !resp.Success {
        return 0, fmt.Errorf(resp.Error)
    }
    return resp.Version, nil
}

// GetWithVersion 获取值和版本
func (c *StateClient) GetWithVersion(key string) ([]byte, int64, error) {
    resp, err := c.sendMessage(&StateMessage{
        Operation: "get_with_version",
        Scope:     c.scope,
        Key:       key,
    })
    if err != nil {
        return nil, 0, err
    }
    if !resp.Success {
        return nil, 0, fmt.Errorf(resp.Error)
    }
    return resp.Value, resp.Version, nil
}

// Expire 设置过期时间
func (c *StateClient) Expire(key string, ttl time.Duration) error {
    resp, err := c.sendMessage(&StateMessage{
        Operation: "expire",
        Scope:     c.scope,
        Key:       key,
        TTL:       int(ttl.Seconds()),
    })
    if err != nil {
        return err
    }
    if !resp.Success {
        return fmt.Errorf(resp.Error)
    }
    return nil
}
```

### 4.2 State Handler (Host 侧)

**文件：** `internal/state/handler.go` (新文件)

```go
package state

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "github.com/redis/go-redis/v9"
    "github.com/sirupsen/logrus"

    "nimbus/internal/domain"
)

// Handler 处理状态操作请求
type Handler struct {
    redis  *redis.Client
    logger *logrus.Logger
    config *domain.StateConfig
}

// StateRequest 状态请求
type StateRequest struct {
    FunctionID   string          `json:"function_id"`
    SessionKey   string          `json:"session_key"`
    InvocationID string          `json:"invocation_id"`
    Operation    string          `json:"operation"`
    Scope        string          `json:"scope"`
    Key          string          `json:"key"`
    Value        json.RawMessage `json:"value,omitempty"`
    TTL          int             `json:"ttl,omitempty"`
    Delta        int64           `json:"delta,omitempty"`
    Version      int64           `json:"version,omitempty"`
}

// StateResult 状态响应
type StateResult struct {
    Success bool            `json:"success"`
    Value   json.RawMessage `json:"value,omitempty"`
    Version int64           `json:"version,omitempty"`
    Error   string          `json:"error,omitempty"`
}

func NewHandler(redis *redis.Client, config *domain.StateConfig, logger *logrus.Logger) *Handler {
    return &Handler{
        redis:  redis,
        logger: logger,
        config: config,
    }
}

// Handle 处理状态操作
func (h *Handler) Handle(ctx context.Context, req *StateRequest) *StateResult {
    // 构建完整的 Redis key
    redisKey := h.buildKey(req)

    // 验证 key 大小
    if len(redisKey) > 256 {
        return &StateResult{Success: false, Error: "key too long"}
    }

    switch req.Operation {
    case "get":
        return h.handleGet(ctx, redisKey)
    case "get_with_version":
        return h.handleGetWithVersion(ctx, redisKey)
    case "set":
        return h.handleSet(ctx, redisKey, req)
    case "set_with_version":
        return h.handleSetWithVersion(ctx, redisKey, req)
    case "delete":
        return h.handleDelete(ctx, redisKey)
    case "incr":
        return h.handleIncr(ctx, redisKey, req.Delta)
    case "exists":
        return h.handleExists(ctx, redisKey)
    case "keys":
        return h.handleKeys(ctx, req)
    case "expire":
        return h.handleExpire(ctx, redisKey, req.TTL)
    default:
        return &StateResult{Success: false, Error: "unknown operation"}
    }
}

// buildKey 构建 Redis key
func (h *Handler) buildKey(req *StateRequest) string {
    var scopeKey string
    switch req.Scope {
    case "function":
        scopeKey = "_global"
    case "invocation":
        scopeKey = req.InvocationID
    default: // session
        scopeKey = req.SessionKey
        if scopeKey == "" {
            scopeKey = "_default"
        }
    }
    return fmt.Sprintf("state:%s:%s:%s", req.FunctionID, scopeKey, req.Key)
}

func (h *Handler) handleGet(ctx context.Context, key string) *StateResult {
    val, err := h.redis.Get(ctx, key).Bytes()
    if err == redis.Nil {
        return &StateResult{Success: true, Value: nil}
    }
    if err != nil {
        return &StateResult{Success: false, Error: err.Error()}
    }
    return &StateResult{Success: true, Value: val}
}

func (h *Handler) handleGetWithVersion(ctx context.Context, key string) *StateResult {
    versionKey := key + ":version"

    pipe := h.redis.Pipeline()
    valCmd := pipe.Get(ctx, key)
    verCmd := pipe.Get(ctx, versionKey)
    pipe.Exec(ctx)

    val, err := valCmd.Bytes()
    if err == redis.Nil {
        return &StateResult{Success: true, Value: nil, Version: 0}
    }
    if err != nil {
        return &StateResult{Success: false, Error: err.Error()}
    }

    version, _ := verCmd.Int64()
    return &StateResult{Success: true, Value: val, Version: version}
}

func (h *Handler) handleSet(ctx context.Context, key string, req *StateRequest) *StateResult {
    // 验证值大小
    if h.config != nil && h.config.MaxStateSize > 0 && len(req.Value) > h.config.MaxStateSize {
        return &StateResult{Success: false, Error: "value too large"}
    }

    var ttl time.Duration
    if req.TTL > 0 {
        ttl = time.Duration(req.TTL) * time.Second
    } else if h.config != nil && h.config.DefaultTTL > 0 {
        ttl = time.Duration(h.config.DefaultTTL) * time.Second
    }

    var err error
    if ttl > 0 {
        err = h.redis.Set(ctx, key, []byte(req.Value), ttl).Err()
    } else {
        err = h.redis.Set(ctx, key, []byte(req.Value), 0).Err()
    }

    if err != nil {
        return &StateResult{Success: false, Error: err.Error()}
    }
    return &StateResult{Success: true}
}

func (h *Handler) handleSetWithVersion(ctx context.Context, key string, req *StateRequest) *StateResult {
    versionKey := key + ":version"

    // 使用 Lua 脚本实现乐观锁
    script := redis.NewScript(`
        local current_version = tonumber(redis.call('GET', KEYS[2]) or '0')
        local expected_version = tonumber(ARGV[2])

        if expected_version > 0 and current_version ~= expected_version then
            return {0, current_version}  -- 版本冲突
        end

        local new_version = current_version + 1
        redis.call('SET', KEYS[1], ARGV[1])
        redis.call('SET', KEYS[2], new_version)

        if tonumber(ARGV[3]) > 0 then
            redis.call('EXPIRE', KEYS[1], ARGV[3])
            redis.call('EXPIRE', KEYS[2], ARGV[3])
        end

        return {1, new_version}
    `)

    result, err := script.Run(ctx, h.redis, []string{key, versionKey},
        string(req.Value), req.Version, req.TTL).Slice()

    if err != nil {
        return &StateResult{Success: false, Error: err.Error()}
    }

    success := result[0].(int64) == 1
    newVersion := result[1].(int64)

    if !success {
        return &StateResult{Success: false, Error: "version conflict", Version: newVersion}
    }
    return &StateResult{Success: true, Version: newVersion}
}

func (h *Handler) handleDelete(ctx context.Context, key string) *StateResult {
    err := h.redis.Del(ctx, key, key+":version").Err()
    if err != nil {
        return &StateResult{Success: false, Error: err.Error()}
    }
    return &StateResult{Success: true}
}

func (h *Handler) handleIncr(ctx context.Context, key string, delta int64) *StateResult {
    var result int64
    var err error

    if delta >= 0 {
        result, err = h.redis.IncrBy(ctx, key, delta).Result()
    } else {
        result, err = h.redis.DecrBy(ctx, key, -delta).Result()
    }

    if err != nil {
        return &StateResult{Success: false, Error: err.Error()}
    }

    valueJSON, _ := json.Marshal(result)
    return &StateResult{Success: true, Value: valueJSON}
}

func (h *Handler) handleExists(ctx context.Context, key string) *StateResult {
    exists, err := h.redis.Exists(ctx, key).Result()
    if err != nil {
        return &StateResult{Success: false, Error: err.Error()}
    }

    valueJSON, _ := json.Marshal(exists > 0)
    return &StateResult{Success: true, Value: valueJSON}
}

func (h *Handler) handleKeys(ctx context.Context, req *StateRequest) *StateResult {
    // 构建 pattern
    var scopeKey string
    switch req.Scope {
    case "function":
        scopeKey = "_global"
    case "invocation":
        scopeKey = req.InvocationID
    default:
        scopeKey = req.SessionKey
        if scopeKey == "" {
            scopeKey = "_default"
        }
    }

    pattern := fmt.Sprintf("state:%s:%s:%s", req.FunctionID, scopeKey, req.Key)

    keys, err := h.redis.Keys(ctx, pattern).Result()
    if err != nil {
        return &StateResult{Success: false, Error: err.Error()}
    }

    // 移除前缀，只返回用户 key
    prefix := fmt.Sprintf("state:%s:%s:", req.FunctionID, scopeKey)
    userKeys := make([]string, 0, len(keys))
    for _, k := range keys {
        if len(k) > len(prefix) {
            userKeys = append(userKeys, k[len(prefix):])
        }
    }

    valueJSON, _ := json.Marshal(userKeys)
    return &StateResult{Success: true, Value: valueJSON}
}

func (h *Handler) handleExpire(ctx context.Context, key string, ttl int) *StateResult {
    err := h.redis.Expire(ctx, key, time.Duration(ttl)*time.Second).Err()
    if err != nil {
        return &StateResult{Success: false, Error: err.Error()}
    }
    return &StateResult{Success: true}
}
```

### 4.3 会话路由器

**文件：** `internal/scheduler/session_router.go` (新文件)

```go
package scheduler

import (
    "context"
    "fmt"
    "hash/fnv"
    "sort"
    "sync"
    "time"

    "github.com/redis/go-redis/v9"
    "github.com/sirupsen/logrus"

    "nimbus/internal/domain"
)

// SessionRouter 会话路由器
type SessionRouter struct {
    redis    *redis.Client
    logger   *logrus.Logger
    pool     *Pool

    // 一致性哈希环
    hashRing     *ConsistentHash
    hashRingMu   sync.RWMutex

    // 会话到 VM 的映射缓存
    sessionCache map[string]string  // session_key -> vm_id
    cacheMu      sync.RWMutex
    cacheTTL     time.Duration
}

// ConsistentHash 一致性哈希实现
type ConsistentHash struct {
    replicas int              // 虚拟节点数
    ring     []uint32         // 哈希环
    nodes    map[uint32]string // 哈希值到节点的映射
}

func NewSessionRouter(redis *redis.Client, pool *Pool, logger *logrus.Logger) *SessionRouter {
    return &SessionRouter{
        redis:        redis,
        logger:       logger,
        pool:         pool,
        hashRing:     NewConsistentHash(100), // 100 个虚拟节点
        sessionCache: make(map[string]string),
        cacheTTL:     30 * time.Second,
    }
}

// RouteSession 路由会话到 VM
func (r *SessionRouter) RouteSession(ctx context.Context, fn *domain.Function, sessionKey string) (string, error) {
    if sessionKey == "" || fn.StateConfig == nil || !fn.StateConfig.SessionAffinity {
        return "", nil // 不需要会话亲和性
    }

    cacheKey := fmt.Sprintf("%s:%s", fn.ID, sessionKey)

    // 检查缓存
    r.cacheMu.RLock()
    if vmID, ok := r.sessionCache[cacheKey]; ok {
        r.cacheMu.RUnlock()
        // 验证 VM 是否仍存活
        if r.isVMAlive(ctx, vmID) {
            return vmID, nil
        }
    } else {
        r.cacheMu.RUnlock()
    }

    // 从 Redis 获取会话绑定
    redisKey := fmt.Sprintf("session:%s:%s", fn.ID, sessionKey)
    vmID, err := r.redis.HGet(ctx, redisKey, "vm_id").Result()
    if err == nil && vmID != "" {
        if r.isVMAlive(ctx, vmID) {
            r.updateCache(cacheKey, vmID)
            return vmID, nil
        }
    }

    // 使用一致性哈希选择 VM
    r.hashRingMu.RLock()
    vmID = r.hashRing.Get(cacheKey)
    r.hashRingMu.RUnlock()

    if vmID != "" && r.isVMAlive(ctx, vmID) {
        r.bindSession(ctx, fn.ID, sessionKey, vmID)
        r.updateCache(cacheKey, vmID)
        return vmID, nil
    }

    return "", nil // 需要创建新 VM
}

// BindSession 绑定会话到 VM
func (r *SessionRouter) BindSession(ctx context.Context, functionID, sessionKey, vmID string, timeout time.Duration) error {
    return r.bindSession(ctx, functionID, sessionKey, vmID)
}

func (r *SessionRouter) bindSession(ctx context.Context, functionID, sessionKey, vmID string) error {
    redisKey := fmt.Sprintf("session:%s:%s", functionID, sessionKey)

    pipe := r.redis.Pipeline()
    pipe.HSet(ctx, redisKey, map[string]interface{}{
        "vm_id":       vmID,
        "function_id": functionID,
        "created_at":  time.Now().Unix(),
        "last_access": time.Now().Unix(),
    })
    pipe.Expire(ctx, redisKey, 1*time.Hour) // 会话 1 小时过期
    _, err := pipe.Exec(ctx)

    if err != nil {
        return err
    }

    // 记录 VM 的会话
    vmSessionsKey := fmt.Sprintf("vm_sessions:%s", vmID)
    r.redis.SAdd(ctx, vmSessionsKey, fmt.Sprintf("%s:%s", functionID, sessionKey))
    r.redis.Expire(ctx, vmSessionsKey, 1*time.Hour)

    return nil
}

// UnbindSession 解绑会话
func (r *SessionRouter) UnbindSession(ctx context.Context, functionID, sessionKey string) error {
    redisKey := fmt.Sprintf("session:%s:%s", functionID, sessionKey)

    // 获取当前绑定的 VM
    vmID, _ := r.redis.HGet(ctx, redisKey, "vm_id").Result()

    // 删除会话记录
    r.redis.Del(ctx, redisKey)

    // 从 VM 的会话列表中移除
    if vmID != "" {
        vmSessionsKey := fmt.Sprintf("vm_sessions:%s", vmID)
        r.redis.SRem(ctx, vmSessionsKey, fmt.Sprintf("%s:%s", functionID, sessionKey))
    }

    // 清理缓存
    cacheKey := fmt.Sprintf("%s:%s", functionID, sessionKey)
    r.cacheMu.Lock()
    delete(r.sessionCache, cacheKey)
    r.cacheMu.Unlock()

    return nil
}

// UpdateHashRing 更新哈希环
func (r *SessionRouter) UpdateHashRing(vmIDs []string) {
    r.hashRingMu.Lock()
    defer r.hashRingMu.Unlock()

    r.hashRing = NewConsistentHash(100)
    for _, vmID := range vmIDs {
        r.hashRing.Add(vmID)
    }
}

// OnVMRemoved VM 被移除时调用
func (r *SessionRouter) OnVMRemoved(ctx context.Context, vmID string) {
    // 获取该 VM 的所有会话
    vmSessionsKey := fmt.Sprintf("vm_sessions:%s", vmID)
    sessions, _ := r.redis.SMembers(ctx, vmSessionsKey).Result()

    // 清理会话绑定
    for _, session := range sessions {
        // session 格式: "function_id:session_key"
        r.redis.Del(ctx, fmt.Sprintf("session:%s", session))
    }
    r.redis.Del(ctx, vmSessionsKey)

    // 从哈希环移除
    r.hashRingMu.Lock()
    r.hashRing.Remove(vmID)
    r.hashRingMu.Unlock()
}

func (r *SessionRouter) isVMAlive(ctx context.Context, vmID string) bool {
    return r.pool.IsVMAlive(vmID)
}

func (r *SessionRouter) updateCache(key, vmID string) {
    r.cacheMu.Lock()
    r.sessionCache[key] = vmID
    r.cacheMu.Unlock()
}

// ==================== 一致性哈希实现 ====================

func NewConsistentHash(replicas int) *ConsistentHash {
    return &ConsistentHash{
        replicas: replicas,
        ring:     make([]uint32, 0),
        nodes:    make(map[uint32]string),
    }
}

func (c *ConsistentHash) Add(node string) {
    for i := 0; i < c.replicas; i++ {
        hash := c.hash(fmt.Sprintf("%s:%d", node, i))
        c.ring = append(c.ring, hash)
        c.nodes[hash] = node
    }
    sort.Slice(c.ring, func(i, j int) bool {
        return c.ring[i] < c.ring[j]
    })
}

func (c *ConsistentHash) Remove(node string) {
    newRing := make([]uint32, 0)
    for _, hash := range c.ring {
        if c.nodes[hash] != node {
            newRing = append(newRing, hash)
        } else {
            delete(c.nodes, hash)
        }
    }
    c.ring = newRing
}

func (c *ConsistentHash) Get(key string) string {
    if len(c.ring) == 0 {
        return ""
    }

    hash := c.hash(key)

    // 二分查找
    idx := sort.Search(len(c.ring), func(i int) bool {
        return c.ring[i] >= hash
    })

    if idx >= len(c.ring) {
        idx = 0
    }

    return c.nodes[c.ring[idx]]
}

func (c *ConsistentHash) hash(key string) uint32 {
    h := fnv.New32a()
    h.Write([]byte(key))
    return h.Sum32()
}
```

### 4.4 Agent 集成

**文件：** `cmd/agent/main.go` (修改)

```go
// 新增消息类型
const (
    // ... 现有类型 ...
    MessageTypeState = 7  // 状态操作
)

// 新增 StatePayload
type StatePayload struct {
    Operation    string          `json:"operation"`
    Scope        string          `json:"scope"`
    Key          string          `json:"key"`
    Value        json.RawMessage `json:"value,omitempty"`
    TTL          int             `json:"ttl,omitempty"`
    Delta        int64           `json:"delta,omitempty"`
    Version      int64           `json:"version,omitempty"`
}

// handleState 处理状态操作
func (a *Agent) handleState(ctx context.Context, payload *StatePayload) ([]byte, error) {
    // 构建请求
    req := &state.StateRequest{
        FunctionID:   a.config.FunctionID,
        SessionKey:   a.config.SessionKey,
        InvocationID: a.currentInvocationID,
        Operation:    payload.Operation,
        Scope:        payload.Scope,
        Key:          payload.Key,
        Value:        payload.Value,
        TTL:          payload.TTL,
        Delta:        payload.Delta,
        Version:      payload.Version,
    }

    // 发送到 Host 处理
    result := a.stateHandler.Handle(ctx, req)

    return json.Marshal(result)
}

// 在各运行时中暴露 State API

// Python 运行时
const pythonStateWrapper = `
import json
import sys

class State:
    def __init__(self, scope='session'):
        self.scope = scope

    def _call(self, operation, key, **kwargs):
        msg = {'type': 7, 'payload': {
            'operation': operation,
            'scope': self.scope,
            'key': key,
            **kwargs
        }}
        print('\x00STATE\x00' + json.dumps(msg), file=sys.stderr)
        # Agent 会捕获这个输出并返回结果
        result = input()  # 从 stdin 读取结果
        return json.loads(result)

    def get(self, key):
        r = self._call('get', key)
        return r.get('value') if r['success'] else None

    def set(self, key, value, ttl=0):
        return self._call('set', key, value=value, ttl=ttl)['success']

    def delete(self, key):
        return self._call('delete', key)['success']

    def incr(self, key, delta=1):
        r = self._call('incr', key, delta=delta)
        return r.get('value') if r['success'] else None

    def exists(self, key):
        r = self._call('exists', key)
        return r.get('value', False) if r['success'] else False

    @classmethod
    def session(cls):
        return cls('session')

    @classmethod
    def function(cls):
        return cls('function')

state = State()
`

// Node.js 运行时
const nodeStateWrapper = `
class State {
    constructor(scope = 'session') {
        this.scope = scope;
    }

    async _call(operation, key, options = {}) {
        const msg = JSON.stringify({
            type: 7,
            payload: { operation, scope: this.scope, key, ...options }
        });
        process.stderr.write('\x00STATE\x00' + msg + '\n');
        // 读取结果
        const result = await new Promise(resolve => {
            process.stdin.once('data', data => resolve(JSON.parse(data.toString())));
        });
        return result;
    }

    async get(key) {
        const r = await this._call('get', key);
        return r.success ? r.value : null;
    }

    async set(key, value, ttl = 0) {
        const r = await this._call('set', key, { value, ttl });
        return r.success;
    }

    async delete(key) {
        const r = await this._call('delete', key);
        return r.success;
    }

    async incr(key, delta = 1) {
        const r = await this._call('incr', key, { delta });
        return r.success ? r.value : null;
    }

    async exists(key) {
        const r = await this._call('exists', key);
        return r.success ? r.value : false;
    }

    static session() { return new State('session'); }
    static function() { return new State('function'); }
}

const state = new State();
module.exports = { state, State };
`
```

---

## 5. API 设计

### 5.1 函数配置

#### 启用状态功能
```
PUT /api/v1/functions/{functionId}
```

**请求体：**
```json
{
  "state_config": {
    "enabled": true,
    "default_ttl": 3600,
    "max_state_size": 65536,
    "max_keys": 100,
    "session_affinity": true,
    "session_timeout": 3600
  }
}
```

### 5.2 调用请求

#### 带会话的调用
```
POST /api/v1/functions/{functionId}/invoke?session_key=user_123
```

**请求体：**
```json
{
  "payload": {"action": "add_to_cart", "item_id": "item_456"}
}
```

**响应：**
```json
{
  "request_id": "inv_abc123",
  "status_code": 200,
  "body": {"cart_size": 3},
  "duration_ms": 45,
  "session_key": "user_123"
}
```

### 5.3 会话管理

#### 获取会话信息
```
GET /api/v1/functions/{functionId}/sessions/{sessionKey}
```

**响应：**
```json
{
  "session_key": "user_123",
  "function_id": "fn_abc",
  "vm_id": "vm_xyz",
  "created_at": "2026-01-29T10:00:00Z",
  "last_access": "2026-01-29T15:00:00Z",
  "state_keys": ["cart", "preferences", "last_viewed"]
}
```

#### 清除会话
```
DELETE /api/v1/functions/{functionId}/sessions/{sessionKey}
```

#### 列出会话
```
GET /api/v1/functions/{functionId}/sessions?limit=20
```

### 5.4 状态管理（管理 API）

#### 查看会话状态
```
GET /api/v1/functions/{functionId}/state/{sessionKey}
```

**响应：**
```json
{
  "session_key": "user_123",
  "keys": [
    {"key": "cart", "size": 1024, "ttl": 3200},
    {"key": "preferences", "size": 256, "ttl": -1}
  ],
  "total_size": 1280
}
```

#### 删除状态
```
DELETE /api/v1/functions/{functionId}/state/{sessionKey}?key=cart
```

---

## 6. 使用示例

### 6.1 Python 购物车示例

```python
def handle(event, state):
    action = event.get('action')

    if action == 'add':
        item_id = event.get('item_id')
        # 获取当前购物车
        cart = state.get('cart') or []
        cart.append(item_id)
        state.set('cart', cart, ttl=3600)  # 1小时过期
        return {'cart_size': len(cart)}

    elif action == 'get':
        cart = state.get('cart') or []
        return {'cart': cart}

    elif action == 'clear':
        state.delete('cart')
        return {'cart_size': 0}

    elif action == 'checkout':
        cart = state.get('cart') or []
        # 处理结账...
        state.delete('cart')
        return {'order_id': 'order_123', 'items': cart}
```

### 6.2 Node.js 计数器示例

```javascript
const { state } = require('nimbus-state');

async function handle(event) {
    const action = event.action;

    if (action === 'increment') {
        // 原子递增
        const count = await state.function().incr('global_counter');
        return { count };
    }

    if (action === 'get') {
        const count = await state.function().get('global_counter') || 0;
        return { count };
    }

    if (action === 'reset') {
        await state.function().set('global_counter', 0);
        return { count: 0 };
    }
}

module.exports = { handle };
```

### 6.3 对话 AI 上下文示例

```python
def handle(event, state):
    user_message = event.get('message')

    # 获取对话历史
    history = state.get('conversation_history') or []

    # 添加用户消息
    history.append({'role': 'user', 'content': user_message})

    # 调用 AI 模型
    ai_response = call_ai_model(history)

    # 保存 AI 响应
    history.append({'role': 'assistant', 'content': ai_response})

    # 限制历史长度
    if len(history) > 20:
        history = history[-20:]

    state.set('conversation_history', history, ttl=1800)  # 30分钟过期

    return {'response': ai_response}
```

---

## 7. 监控指标

### 7.1 Prometheus 指标

```go
var (
    stateOperationTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "nimbus_state_operation_total",
            Help: "Total state operations",
        },
        []string{"function_id", "operation", "scope", "status"},
    )

    stateOperationDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "nimbus_state_operation_duration_ms",
            Help:    "State operation duration in milliseconds",
            Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 25, 50},
        },
        []string{"operation"},
    )

    sessionCount = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "nimbus_session_count",
            Help: "Active session count",
        },
        []string{"function_id"},
    )

    sessionRouteHits = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "nimbus_session_route_total",
            Help: "Session routing results",
        },
        []string{"function_id", "result"}, // result: hit, miss, created
    )

    stateStorageBytes = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "nimbus_state_storage_bytes",
            Help: "Total state storage usage",
        },
        []string{"function_id"},
    )
)
```

---

## 8. 配置

### 8.1 全局配置

```yaml
state:
  # 是否启用状态功能
  enabled: true

  # Redis 配置
  redis:
    addr: "localhost:6379"
    password: ""
    db: 1  # 使用独立的 DB

  # 默认配置
  defaults:
    default_ttl: 3600          # 1小时
    max_state_size: 65536      # 64KB
    max_keys: 100

  # 会话配置
  session:
    affinity_enabled: true
    session_timeout: 3600      # 1小时
    cache_ttl: 30              # 本地缓存 30 秒
```

---

## 9. 测试计划

### 9.1 单元测试

- [ ] `StateClient` 各操作测试
- [ ] `StateHandler` Redis 操作测试
- [ ] `SessionRouter` 一致性哈希测试
- [ ] 乐观锁冲突处理测试

### 9.2 集成测试

- [ ] 完整的状态读写流程
- [ ] 会话亲和性路由
- [ ] VM 故障后会话重新绑定
- [ ] 状态 TTL 过期

### 9.3 性能测试

- [ ] 状态操作延迟 P50/P95/P99
- [ ] 高并发下的状态一致性
- [ ] 会话路由命中率

---

## 10. 实施计划

| 阶段 | 内容 | 预估时间 |
|------|------|----------|
| 1 | StateHandler (Host 侧) | 1 天 |
| 2 | StateClient (Agent 侧) | 1 天 |
| 3 | 各运行时 State API 包装 | 1 天 |
| 4 | SessionRouter 实现 | 1.5 天 |
| 5 | 调度器集成 | 0.5 天 |
| 6 | API + 配置 | 1 天 |
| 7 | 测试 + 文档 | 1 天 |
| **总计** | | **7 天** |

---

## 11. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| Redis 单点故障 | 状态不可用 | Redis Sentinel/Cluster |
| 状态数据过大 | 内存/网络压力 | 大小限制 + 分片 |
| 会话亲和性导致热点 | 负载不均 | 监控 + 自动迁移 |
| 乐观锁冲突频繁 | 性能下降 | 重试策略 + 分布式锁 |

---

## 12. 附录

### 12.1 与 AWS Lambda 状态对比

| 功能 | Nimbus | AWS Lambda |
|------|--------|------------|
| 内置状态 API | ✅ | ❌ (需外部服务) |
| 会话亲和性 | ✅ | ❌ |
| 原子操作 | ✅ | - |
| TTL 支持 | ✅ | - |
| 乐观锁 | ✅ | - |

### 12.2 数据库表（可选）

如需持久化会话元数据：

```sql
CREATE TABLE sessions (
    id VARCHAR(36) PRIMARY KEY,
    function_id VARCHAR(36) NOT NULL,
    session_key VARCHAR(256) NOT NULL,
    vm_id VARCHAR(36),
    state_keys JSONB DEFAULT '[]',
    total_size INTEGER DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    last_access TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE,

    UNIQUE(function_id, session_key)
);

CREATE INDEX idx_sessions_function ON sessions(function_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);
```
