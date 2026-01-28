//go:build linux
// +build linux

// Package scheduler 提供函数调度器的实现。
package scheduler

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/oriys/nimbus/internal/domain"
	"github.com/oriys/nimbus/internal/storage"
	"github.com/sirupsen/logrus"
)

// TrafficRouter 负责根据别名配置进行流量路由。
// 支持加权随机选择，实现金丝雀发布和 A/B 测试。
type TrafficRouter struct {
	store    *storage.PostgresStore
	cache    map[string]*cachedAlias // functionID:aliasName -> alias
	cacheMu  sync.RWMutex
	cacheTTL time.Duration
	rng      *rand.Rand
	logger   *logrus.Logger
}

// cachedAlias 缓存的别名信息
type cachedAlias struct {
	alias     *domain.FunctionAlias
	expiresAt time.Time
}

// NewTrafficRouter 创建新的流量路由器
func NewTrafficRouter(store *storage.PostgresStore, logger *logrus.Logger) *TrafficRouter {
	return &TrafficRouter{
		store:    store,
		cache:    make(map[string]*cachedAlias),
		cacheTTL: 30 * time.Second, // 别名配置缓存 30 秒
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
		logger:   logger,
	}
}

// SelectVersion 根据别名选择要执行的版本号
// 返回选中的版本号
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
	alias, err := r.store.GetFunctionAlias(functionID, aliasName)
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

// weightedSelect 加权随机选择版本
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

// InvalidateCache 使指定别名的缓存失效
func (r *TrafficRouter) InvalidateCache(functionID, aliasName string) {
	cacheKey := functionID + ":" + aliasName
	r.cacheMu.Lock()
	delete(r.cache, cacheKey)
	r.cacheMu.Unlock()

	r.logger.WithFields(logrus.Fields{
		"function_id": functionID,
		"alias":       aliasName,
	}).Debug("Alias cache invalidated")
}

// InvalidateFunctionCache 使某个函数所有别名的缓存失效
func (r *TrafficRouter) InvalidateFunctionCache(functionID string) {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()

	prefix := functionID + ":"
	for key := range r.cache {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			delete(r.cache, key)
		}
	}

	r.logger.WithField("function_id", functionID).Debug("All alias caches for function invalidated")
}

// ClearCache 清空所有缓存
func (r *TrafficRouter) ClearCache() {
	r.cacheMu.Lock()
	r.cache = make(map[string]*cachedAlias)
	r.cacheMu.Unlock()

	r.logger.Debug("Traffic router cache cleared")
}

// ValidateRoutingConfig 验证路由配置
func ValidateRoutingConfig(config domain.RoutingConfig) error {
	if len(config.Weights) == 0 {
		return domain.ErrInvalidWeights
	}

	totalWeight := 0
	for _, w := range config.Weights {
		if w.Weight < 0 || w.Weight > 100 {
			return domain.ErrInvalidWeights
		}
		if w.Version <= 0 {
			return domain.ErrInvalidVersion
		}
		totalWeight += w.Weight
	}

	if totalWeight != 100 {
		return domain.ErrInvalidWeights
	}

	return nil
}
