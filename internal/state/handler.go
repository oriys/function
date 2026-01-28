// Package state 提供有状态函数的状态管理功能。
// 该包负责处理函数的状态读写操作，基于 Redis 实现跨调用状态保存。
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/oriys/nimbus/internal/domain"
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
	Scope        string          `json:"scope"` // "session", "function", "invocation"
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

// NewHandler 创建新的状态处理器
func NewHandler(redisClient *redis.Client, config *domain.StateConfig, logger *logrus.Logger) *Handler {
	if config == nil {
		config = domain.DefaultStateConfig()
	}
	return &Handler{
		redis:  redisClient,
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
		return &StateResult{Success: false, Error: "unknown operation: " + req.Operation}
	}
}

// buildKey 构建 Redis key
// 格式: state:{function_id}:{scope_key}:{user_key}
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
		return &StateResult{Success: false, Error: fmt.Sprintf("value too large: %d > %d", len(req.Value), h.config.MaxStateSize)}
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

	if delta == 0 {
		delta = 1 // 默认增加 1
	}

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
		if len(k) > len(prefix) && !strings.HasSuffix(k, ":version") {
			userKeys = append(userKeys, k[len(prefix):])
		}
	}

	valueJSON, _ := json.Marshal(userKeys)
	return &StateResult{Success: true, Value: valueJSON}
}

func (h *Handler) handleExpire(ctx context.Context, key string, ttl int) *StateResult {
	if ttl <= 0 {
		return &StateResult{Success: false, Error: "invalid TTL"}
	}
	err := h.redis.Expire(ctx, key, time.Duration(ttl)*time.Second).Err()
	if err != nil {
		return &StateResult{Success: false, Error: err.Error()}
	}
	return &StateResult{Success: true}
}

// GetSessionState 获取会话的所有状态 key 信息
func (h *Handler) GetSessionState(ctx context.Context, functionID, sessionKey string) ([]*domain.StateKeyInfo, int64, error) {
	pattern := fmt.Sprintf("state:%s:%s:*", functionID, sessionKey)
	keys, err := h.redis.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, 0, err
	}

	var keyInfos []*domain.StateKeyInfo
	var totalSize int64

	prefix := fmt.Sprintf("state:%s:%s:", functionID, sessionKey)
	for _, k := range keys {
		// 跳过版本 key
		if strings.HasSuffix(k, ":version") {
			continue
		}

		// 获取 key 的大小和 TTL
		pipe := h.redis.Pipeline()
		strLenCmd := pipe.StrLen(ctx, k)
		ttlCmd := pipe.TTL(ctx, k)
		pipe.Exec(ctx)

		size := strLenCmd.Val()
		ttlDuration := ttlCmd.Val()

		ttl := -1 // -1 表示永不过期
		if ttlDuration > 0 {
			ttl = int(ttlDuration.Seconds())
		} else if ttlDuration == -2 {
			continue // key 不存在
		}

		userKey := k[len(prefix):]
		keyInfos = append(keyInfos, &domain.StateKeyInfo{
			Key:  userKey,
			Size: size,
			TTL:  ttl,
		})
		totalSize += size
	}

	return keyInfos, totalSize, nil
}

// DeleteSessionState 删除会话的所有状态
func (h *Handler) DeleteSessionState(ctx context.Context, functionID, sessionKey string) error {
	pattern := fmt.Sprintf("state:%s:%s:*", functionID, sessionKey)
	keys, err := h.redis.Keys(ctx, pattern).Result()
	if err != nil {
		return err
	}

	if len(keys) > 0 {
		return h.redis.Del(ctx, keys...).Err()
	}
	return nil
}

// DeleteStateKey 删除指定的状态 key
func (h *Handler) DeleteStateKey(ctx context.Context, functionID, sessionKey, userKey string) error {
	key := fmt.Sprintf("state:%s:%s:%s", functionID, sessionKey, userKey)
	return h.redis.Del(ctx, key, key+":version").Err()
}
