// Package scheduler 提供函数调度器的实现。
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

	"github.com/oriys/nimbus/internal/domain"
)

// VMPool 定义了会话路由器所需的虚拟机池接口。
type VMPool interface {
	IsVMAlive(vmID string) bool
	GetAllVMIDs(runtime string) []string
}

// SessionRouter 会话路由器，用于有状态函数的会话亲和性路由。
// 通过一致性哈希确保相同 session_key 的请求路由到同一 VM。
type SessionRouter struct {
	redis  *redis.Client
	logger *logrus.Logger
	pool   VMPool

	// 一致性哈希环
	hashRing   *ConsistentHash
	hashRingMu sync.RWMutex

	// 会话到 VM 的映射缓存
	sessionCache map[string]*sessionCacheEntry
	cacheMu      sync.RWMutex
	cacheTTL     time.Duration
}

// sessionCacheEntry 缓存条目
type sessionCacheEntry struct {
	vmID      string
	expiresAt time.Time
}

// ConsistentHash 一致性哈希实现
type ConsistentHash struct {
	replicas int               // 虚拟节点数
	ring     []uint32          // 哈希环
	nodes    map[uint32]string // 哈希值到节点的映射
}

// NewSessionRouter 创建新的会话路由器
func NewSessionRouter(redisClient *redis.Client, pool VMPool, logger *logrus.Logger) *SessionRouter {
	return &SessionRouter{
		redis:        redisClient,
		logger:       logger,
		pool:         pool,
		hashRing:     NewConsistentHash(100), // 100 个虚拟节点
		sessionCache: make(map[string]*sessionCacheEntry),
		cacheTTL:     30 * time.Second,
	}
}

// RouteSession 路由会话到 VM
// 返回空字符串表示需要创建新 VM 或不需要会话亲和性
func (r *SessionRouter) RouteSession(ctx context.Context, fn *domain.Function, sessionKey string) (string, error) {
	// 检查是否需要会话亲和性
	if sessionKey == "" || fn.StateConfig == nil || !fn.StateConfig.SessionAffinity {
		return "", nil // 不需要会话亲和性
	}

	cacheKey := fmt.Sprintf("%s:%s", fn.ID, sessionKey)

	// 检查本地缓存
	r.cacheMu.RLock()
	if entry, ok := r.sessionCache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		r.cacheMu.RUnlock()
		// 验证 VM 是否仍存活
		if r.pool.IsVMAlive(entry.vmID) {
			return entry.vmID, nil
		}
	} else {
		r.cacheMu.RUnlock()
	}

	// 从 Redis 获取会话绑定
	redisKey := fmt.Sprintf("session:%s:%s", fn.ID, sessionKey)
	vmID, err := r.redis.HGet(ctx, redisKey, "vm_id").Result()
	if err == nil && vmID != "" {
		if r.pool.IsVMAlive(vmID) {
			r.updateCache(cacheKey, vmID)
			// 更新最后访问时间
			r.redis.HSet(ctx, redisKey, "last_access", time.Now().Unix())
			return vmID, nil
		}
		// VM 已不存活，清理旧绑定
		r.redis.Del(ctx, redisKey)
	}

	// 使用一致性哈希选择 VM
	r.hashRingMu.RLock()
	vmID = r.hashRing.Get(cacheKey)
	r.hashRingMu.RUnlock()

	if vmID != "" && r.pool.IsVMAlive(vmID) {
		// 绑定会话到 VM
		r.bindSession(ctx, fn.ID, sessionKey, vmID, fn.StateConfig.SessionTimeout)
		r.updateCache(cacheKey, vmID)
		return vmID, nil
	}

	return "", nil // 需要创建新 VM
}

// BindSession 绑定会话到 VM
func (r *SessionRouter) BindSession(ctx context.Context, functionID, sessionKey, vmID string, timeoutSec int) error {
	return r.bindSession(ctx, functionID, sessionKey, vmID, timeoutSec)
}

func (r *SessionRouter) bindSession(ctx context.Context, functionID, sessionKey, vmID string, timeoutSec int) error {
	redisKey := fmt.Sprintf("session:%s:%s", functionID, sessionKey)

	// 设置会话信息
	pipe := r.redis.Pipeline()
	pipe.HSet(ctx, redisKey, map[string]interface{}{
		"vm_id":       vmID,
		"function_id": functionID,
		"created_at":  time.Now().Unix(),
		"last_access": time.Now().Unix(),
	})

	// 设置过期时间
	ttl := time.Duration(timeoutSec) * time.Second
	if ttl == 0 {
		ttl = 1 * time.Hour // 默认 1 小时
	}
	pipe.Expire(ctx, redisKey, ttl)
	_, err := pipe.Exec(ctx)

	if err != nil {
		return err
	}

	// 记录 VM 的会话
	vmSessionsKey := fmt.Sprintf("vm_sessions:%s", vmID)
	r.redis.SAdd(ctx, vmSessionsKey, fmt.Sprintf("%s:%s", functionID, sessionKey))
	r.redis.Expire(ctx, vmSessionsKey, ttl)

	r.logger.WithFields(logrus.Fields{
		"function_id": functionID,
		"session_key": sessionKey,
		"vm_id":       vmID,
	}).Debug("Session bound to VM")

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

	r.logger.WithFields(logrus.Fields{
		"function_id": functionID,
		"session_key": sessionKey,
	}).Debug("Session unbound")

	return nil
}

// UpdateHashRing 更新哈希环（当 VM 池变化时调用）
func (r *SessionRouter) UpdateHashRing(vmIDs []string) {
	r.hashRingMu.Lock()
	defer r.hashRingMu.Unlock()

	r.hashRing = NewConsistentHash(100)
	for _, vmID := range vmIDs {
		r.hashRing.Add(vmID)
	}

	r.logger.WithField("vm_count", len(vmIDs)).Debug("Hash ring updated")
}

// OnVMRemoved VM 被移除时调用，清理相关会话绑定
func (r *SessionRouter) OnVMRemoved(ctx context.Context, vmID string) {
	// 获取该 VM 的所有会话
	vmSessionsKey := fmt.Sprintf("vm_sessions:%s", vmID)
	sessions, _ := r.redis.SMembers(ctx, vmSessionsKey).Result()

	// 清理会话绑定
	for _, session := range sessions {
		r.redis.Del(ctx, fmt.Sprintf("session:%s", session))
	}
	r.redis.Del(ctx, vmSessionsKey)

	// 从哈希环移除
	r.hashRingMu.Lock()
	r.hashRing.Remove(vmID)
	r.hashRingMu.Unlock()

	r.logger.WithFields(logrus.Fields{
		"vm_id":          vmID,
		"sessions_count": len(sessions),
	}).Info("VM removed, cleaned up sessions")
}

// GetSessionInfo 获取会话信息
func (r *SessionRouter) GetSessionInfo(ctx context.Context, functionID, sessionKey string) (*domain.SessionInfo, error) {
	redisKey := fmt.Sprintf("session:%s:%s", functionID, sessionKey)

	data, err := r.redis.HGetAll(ctx, redisKey).Result()
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("session not found")
	}

	info := &domain.SessionInfo{
		SessionKey: sessionKey,
		FunctionID: functionID,
		VMID:       data["vm_id"],
	}

	if createdAt, ok := data["created_at"]; ok {
		var ts int64
		fmt.Sscanf(createdAt, "%d", &ts)
		info.CreatedAt = time.Unix(ts, 0)
	}

	if lastAccess, ok := data["last_access"]; ok {
		var ts int64
		fmt.Sscanf(lastAccess, "%d", &ts)
		info.LastAccessAt = time.Unix(ts, 0)
	}

	return info, nil
}

// ListSessions 列出函数的所有会话
func (r *SessionRouter) ListSessions(ctx context.Context, functionID string, limit int) ([]*domain.SessionInfo, error) {
	pattern := fmt.Sprintf("session:%s:*", functionID)
	keys, err := r.redis.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, err
	}

	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
	}

	var sessions []*domain.SessionInfo
	prefix := fmt.Sprintf("session:%s:", functionID)

	for _, key := range keys {
		sessionKey := key[len(prefix):]
		info, err := r.GetSessionInfo(ctx, functionID, sessionKey)
		if err != nil {
			continue
		}
		sessions = append(sessions, info)
	}

	return sessions, nil
}

func (r *SessionRouter) updateCache(key, vmID string) {
	r.cacheMu.Lock()
	r.sessionCache[key] = &sessionCacheEntry{
		vmID:      vmID,
		expiresAt: time.Now().Add(r.cacheTTL),
	}
	r.cacheMu.Unlock()
}

// ==================== 一致性哈希实现 ====================

// NewConsistentHash 创建新的一致性哈希环
func NewConsistentHash(replicas int) *ConsistentHash {
	return &ConsistentHash{
		replicas: replicas,
		ring:     make([]uint32, 0),
		nodes:    make(map[uint32]string),
	}
}

// Add 添加节点到哈希环
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

// Remove 从哈希环移除节点
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

// Get 根据 key 获取对应的节点
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

// hash 计算字符串的哈希值
func (c *ConsistentHash) hash(key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	return h.Sum32()
}

// Size 返回哈希环中的节点数
func (c *ConsistentHash) Size() int {
	return len(c.ring) / c.replicas
}
