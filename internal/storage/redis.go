// Package storage 提供数据存储层的实现，包括 Redis 和 PostgreSQL 两种存储方式。
// 本文件实现了基于 Redis 的缓存和状态存储功能，主要用于：
//   - 虚拟机(VM)池的状态管理（预热池和繁忙池）
//   - 分布式锁的实现
//   - 函数代码缓存
//   - 函数调用队列管理
package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oriys/nimbus/internal/config"
	"github.com/redis/go-redis/v9"
)

// RedisStore 是 Redis 存储的封装结构体。
// 提供虚拟机池管理、分布式锁、函数缓存和调用队列等功能。
type RedisStore struct {
	client *redis.Client // Redis 客户端实例
}

// NewRedisStore 创建并初始化一个新的 Redis 存储实例。
//
// 参数:
//   - cfg: Redis 配置信息，包含地址、密码和数据库编号
//
// 返回值:
//   - *RedisStore: 初始化完成的 Redis 存储实例
//   - error: 连接失败时返回错误信息
func NewRedisStore(cfg config.RedisConfig) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Address,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	// 使用 5 秒超时测试 Redis 连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &RedisStore{client: client}, nil
}

// Close 关闭 Redis 连接。
//
// 返回值:
//   - error: 关闭连接时的错误信息，成功则为 nil
func (s *RedisStore) Close() error {
	return s.client.Close()
}

// ==================== VM 池操作相关 ====================

// Redis 键前缀常量定义
const (
	vmPoolKeyPrefix    = "vmpool:"         // VM 池键前缀，用于存储预热池和繁忙池的 VM ID 集合
	vmStateKeyPrefix   = "vm:state:"       // VM 状态键前缀，用于存储单个 VM 的详细状态信息
	vmLockKeyPrefix    = "vm:lock:"        // VM 锁键前缀，用于实现分布式锁
	functionCacheKey   = "function:cache:" // 函数缓存键前缀，用于缓存函数代码
	invocationQueueKey = "invocation:queue" // 函数调用队列键，用于异步调用排队
)

// VMState 表示虚拟机的状态信息。
// 用于跟踪 VM 的生命周期和使用情况。
type VMState struct {
	ID        string    `json:"id"`         // VM 唯一标识符
	Runtime   string    `json:"runtime"`    // 运行时类型（如 python3.9, nodejs18 等）
	Status    string    `json:"status"`     // VM 状态（warm/busy/cold 等）
	IP        string    `json:"ip"`         // VM 的 IP 地址
	VsockCID  uint32    `json:"vsock_cid"`  // Vsock 通信 ID，用于宿主机与 VM 通信
	CreatedAt time.Time `json:"created_at"` // VM 创建时间
	LastUsed  time.Time `json:"last_used"`  // 最后使用时间
	UseCount  int       `json:"use_count"`  // 使用次数统计
}

// AddVMToPool 将虚拟机添加到预热池中。
// 使用 Redis Pipeline 批量执行以下操作：
//   - 将 VM ID 添加到对应运行时的预热集合中
//   - 存储 VM 的完整状态信息
//
// 参数:
//   - ctx: 上下文，用于超时控制和取消操作
//   - runtime: 运行时类型
//   - vm: VM 状态信息
//
// 返回值:
//   - error: 操作失败时返回错误信息
func (s *RedisStore) AddVMToPool(ctx context.Context, runtime string, vm *VMState) error {
	// 将 VM 状态序列化为 JSON
	data, err := json.Marshal(vm)
	if err != nil {
		return err
	}

	// 使用 Pipeline 批量执行，减少网络往返
	pipe := s.client.Pipeline()
	// SADD vmpool:<runtime>:warm <vm_id> - 将 VM ID 添加到预热集合
	pipe.SAdd(ctx, vmPoolKeyPrefix+runtime+":warm", vm.ID)
	// SET vm:state:<vm_id> <json_data> - 存储 VM 状态
	pipe.Set(ctx, vmStateKeyPrefix+vm.ID, data, 0)
	_, err = pipe.Exec(ctx)
	return err
}

// GetWarmVM 从预热池中获取一个可用的虚拟机。
// 使用 SPOP 原子操作从集合中随机弹出一个 VM ID，确保并发安全。
//
// 参数:
//   - ctx: 上下文，用于超时控制
//   - runtime: 运行时类型
//
// 返回值:
//   - *VMState: VM 状态信息，如果池为空则返回 nil
//   - error: 操作失败时返回错误信息
func (s *RedisStore) GetWarmVM(ctx context.Context, runtime string) (*VMState, error) {
	// SPOP vmpool:<runtime>:warm - 原子性地随机弹出一个 VM ID
	vmID, err := s.client.SPop(ctx, vmPoolKeyPrefix+runtime+":warm").Result()
	if err == redis.Nil {
		return nil, nil // 预热池为空
	}
	if err != nil {
		return nil, err
	}

	// GET vm:state:<vm_id> - 获取 VM 的完整状态信息
	data, err := s.client.Get(ctx, vmStateKeyPrefix+vmID).Bytes()
	if err != nil {
		return nil, err
	}

	vm := &VMState{}
	if err := json.Unmarshal(data, vm); err != nil {
		return nil, err
	}
	return vm, nil
}

// SetVMBusy 将虚拟机从预热池移动到繁忙池。
// 当 VM 被分配执行函数时调用此方法。
//
// 参数:
//   - ctx: 上下文
//   - runtime: 运行时类型
//   - vmID: 虚拟机 ID
//
// 返回值:
//   - error: 操作失败时返回错误信息
func (s *RedisStore) SetVMBusy(ctx context.Context, runtime, vmID string) error {
	pipe := s.client.Pipeline()
	// SREM vmpool:<runtime>:warm <vm_id> - 从预热集合移除
	pipe.SRem(ctx, vmPoolKeyPrefix+runtime+":warm", vmID)
	// SADD vmpool:<runtime>:busy <vm_id> - 添加到繁忙集合
	pipe.SAdd(ctx, vmPoolKeyPrefix+runtime+":busy", vmID)
	_, err := pipe.Exec(ctx)
	return err
}

// SetVMWarm 将虚拟机从繁忙池移回预热池。
// 当 VM 完成函数执行后调用此方法，使其可被复用。
//
// 参数:
//   - ctx: 上下文
//   - runtime: 运行时类型
//   - vmID: 虚拟机 ID
//
// 返回值:
//   - error: 操作失败时返回错误信息
func (s *RedisStore) SetVMWarm(ctx context.Context, runtime, vmID string) error {
	pipe := s.client.Pipeline()
	// SREM vmpool:<runtime>:busy <vm_id> - 从繁忙集合移除
	pipe.SRem(ctx, vmPoolKeyPrefix+runtime+":busy", vmID)
	// SADD vmpool:<runtime>:warm <vm_id> - 添加到预热集合
	pipe.SAdd(ctx, vmPoolKeyPrefix+runtime+":warm", vmID)
	_, err := pipe.Exec(ctx)
	return err
}

// RemoveVM 从所有池中移除虚拟机并删除其状态信息。
// 当 VM 需要被销毁时调用此方法。
//
// 参数:
//   - ctx: 上下文
//   - runtime: 运行时类型
//   - vmID: 虚拟机 ID
//
// 返回值:
//   - error: 操作失败时返回错误信息
func (s *RedisStore) RemoveVM(ctx context.Context, runtime, vmID string) error {
	pipe := s.client.Pipeline()
	// 从预热池移除
	pipe.SRem(ctx, vmPoolKeyPrefix+runtime+":warm", vmID)
	// 从繁忙池移除
	pipe.SRem(ctx, vmPoolKeyPrefix+runtime+":busy", vmID)
	// DEL vm:state:<vm_id> - 删除 VM 状态信息
	pipe.Del(ctx, vmStateKeyPrefix+vmID)
	_, err := pipe.Exec(ctx)
	return err
}

// GetVMState 获取指定虚拟机的状态信息。
//
// 参数:
//   - ctx: 上下文
//   - vmID: 虚拟机 ID
//
// 返回值:
//   - *VMState: VM 状态信息，如果不存在则返回 nil
//   - error: 操作失败时返回错误信息
func (s *RedisStore) GetVMState(ctx context.Context, vmID string) (*VMState, error) {
	// GET vm:state:<vm_id> - 获取 VM 状态 JSON 数据
	data, err := s.client.Get(ctx, vmStateKeyPrefix+vmID).Bytes()
	if err == redis.Nil {
		return nil, nil // VM 不存在
	}
	if err != nil {
		return nil, err
	}

	vm := &VMState{}
	if err := json.Unmarshal(data, vm); err != nil {
		return nil, err
	}
	return vm, nil
}

// UpdateVMState 更新虚拟机的状态信息。
//
// 参数:
//   - ctx: 上下文
//   - vm: 包含更新后数据的 VM 状态信息
//
// 返回值:
//   - error: 操作失败时返回错误信息
func (s *RedisStore) UpdateVMState(ctx context.Context, vm *VMState) error {
	data, err := json.Marshal(vm)
	if err != nil {
		return err
	}
	// SET vm:state:<vm_id> <json_data> - 覆盖存储 VM 状态
	return s.client.Set(ctx, vmStateKeyPrefix+vm.ID, data, 0).Err()
}

// GetPoolStats 获取指定运行时的 VM 池统计信息。
//
// 参数:
//   - ctx: 上下文
//   - runtime: 运行时类型
//
// 返回值:
//   - warm: 预热池中的 VM 数量
//   - busy: 繁忙池中的 VM 数量
//   - err: 操作失败时返回错误信息
func (s *RedisStore) GetPoolStats(ctx context.Context, runtime string) (warm, busy int, err error) {
	// SCARD vmpool:<runtime>:warm - 获取预热集合的元素数量
	warmCount, err := s.client.SCard(ctx, vmPoolKeyPrefix+runtime+":warm").Result()
	if err != nil {
		return 0, 0, err
	}
	// SCARD vmpool:<runtime>:busy - 获取繁忙集合的元素数量
	busyCount, err := s.client.SCard(ctx, vmPoolKeyPrefix+runtime+":busy").Result()
	if err != nil {
		return 0, 0, err
	}
	return int(warmCount), int(busyCount), nil
}

// GetAllWarmVMs 获取指定运行时预热池中的所有虚拟机 ID。
//
// 参数:
//   - ctx: 上下文
//   - runtime: 运行时类型
//
// 返回值:
//   - []string: VM ID 列表
//   - error: 操作失败时返回错误信息
func (s *RedisStore) GetAllWarmVMs(ctx context.Context, runtime string) ([]string, error) {
	// SMEMBERS vmpool:<runtime>:warm - 获取预热集合的所有成员
	return s.client.SMembers(ctx, vmPoolKeyPrefix+runtime+":warm").Result()
}

// GetAllBusyVMs 获取指定运行时繁忙池中的所有虚拟机 ID。
//
// 参数:
//   - ctx: 上下文
//   - runtime: 运行时类型
//
// 返回值:
//   - []string: VM ID 列表
//   - error: 操作失败时返回错误信息
func (s *RedisStore) GetAllBusyVMs(ctx context.Context, runtime string) ([]string, error) {
	// SMEMBERS vmpool:<runtime>:busy - 获取繁忙集合的所有成员
	return s.client.SMembers(ctx, vmPoolKeyPrefix+runtime+":busy").Result()
}

// ==================== 分布式锁相关 ====================

// AcquireLock 尝试获取分布式锁。
// 使用 Redis 的 SETNX 命令实现，确保原子性。
//
// 参数:
//   - ctx: 上下文
//   - key: 锁的键名
//   - ttl: 锁的过期时间，防止死锁
//
// 返回值:
//   - bool: 是否成功获取锁，true 表示获取成功
//   - error: 操作失败时返回错误信息
func (s *RedisStore) AcquireLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	// SETNX vm:lock:<key> "1" EX <ttl> - 仅当键不存在时设置，并设置过期时间
	return s.client.SetNX(ctx, vmLockKeyPrefix+key, "1", ttl).Result()
}

// ReleaseLock 释放分布式锁。
//
// 参数:
//   - ctx: 上下文
//   - key: 锁的键名
//
// 返回值:
//   - error: 操作失败时返回错误信息
func (s *RedisStore) ReleaseLock(ctx context.Context, key string) error {
	// DEL vm:lock:<key> - 删除锁键
	return s.client.Del(ctx, vmLockKeyPrefix+key).Err()
}

// ==================== 函数缓存相关 ====================

// CacheFunction 缓存函数代码。
// 用于减少从数据库加载函数代码的次数，提高冷启动性能。
//
// 参数:
//   - ctx: 上下文
//   - functionID: 函数唯一标识符
//   - data: 函数代码数据
//   - ttl: 缓存过期时间
//
// 返回值:
//   - error: 操作失败时返回错误信息
func (s *RedisStore) CacheFunction(ctx context.Context, functionID string, data []byte, ttl time.Duration) error {
	// SET function:cache:<function_id> <data> EX <ttl> - 设置带过期时间的缓存
	return s.client.Set(ctx, functionCacheKey+functionID, data, ttl).Err()
}

// GetCachedFunction 获取缓存的函数代码。
//
// 参数:
//   - ctx: 上下文
//   - functionID: 函数唯一标识符
//
// 返回值:
//   - []byte: 函数代码数据，如果缓存不存在则返回 nil
//   - error: 操作失败时返回错误信息
func (s *RedisStore) GetCachedFunction(ctx context.Context, functionID string) ([]byte, error) {
	// GET function:cache:<function_id> - 获取缓存的函数代码
	data, err := s.client.Get(ctx, functionCacheKey+functionID).Bytes()
	if err == redis.Nil {
		return nil, nil // 缓存不存在
	}
	return data, err
}

// ==================== 调用队列相关 ====================

// PushInvocation 将函数调用 ID 推入队列尾部。
// 用于实现异步函数调用的排队机制。
//
// 参数:
//   - ctx: 上下文
//   - invocationID: 函数调用的唯一标识符
//
// 返回值:
//   - error: 操作失败时返回错误信息
func (s *RedisStore) PushInvocation(ctx context.Context, invocationID string) error {
	// RPUSH invocation:queue <invocation_id> - 将调用 ID 推入队列尾部
	return s.client.RPush(ctx, invocationQueueKey, invocationID).Err()
}

// PopInvocation 从队列头部弹出一个函数调用 ID。
// 使用阻塞式弹出，在指定超时时间内等待新的调用请求。
//
// 参数:
//   - ctx: 上下文
//   - timeout: 阻塞等待的最长时间
//
// 返回值:
//   - string: 调用 ID，如果超时则返回空字符串
//   - error: 操作失败时返回错误信息
func (s *RedisStore) PopInvocation(ctx context.Context, timeout time.Duration) (string, error) {
	// BLPOP invocation:queue <timeout> - 阻塞式从队列头部弹出
	result, err := s.client.BLPop(ctx, timeout, invocationQueueKey).Result()
	if err == redis.Nil {
		return "", nil // 超时，队列为空
	}
	if err != nil {
		return "", err
	}
	// BLPOP 返回 [key, value] 数组
	if len(result) < 2 {
		return "", nil
	}
	return result[1], nil
}

// InvocationQueueLen 获取调用队列的当前长度。
//
// 参数:
//   - ctx: 上下文
//
// 返回值:
//   - int64: 队列中等待处理的调用数量
//   - error: 操作失败时返回错误信息
func (s *RedisStore) InvocationQueueLen(ctx context.Context) (int64, error) {
	// LLEN invocation:queue - 获取列表长度
	return s.client.LLen(ctx, invocationQueueKey).Result()
}
