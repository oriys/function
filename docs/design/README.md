# Nimbus 高级功能设计文档

> 版本：1.0
> 创建日期：2026-01-29

---

## 概述

本目录包含 Nimbus Serverless Platform 三个高级功能的详细技术设计文档。这些功能按开发优先级排序，旨在提升平台的生产可用性、性能和应用场景。

---

## 设计文档索引

| 编号 | 功能 | 文档 | 优先级 | 预估时间 |
|------|------|------|--------|----------|
| 001 | [版本与别名](#1-版本与别名) | [001-versions-aliases.md](./001-versions-aliases.md) | P0 | 5.5 天 |
| 002 | [函数级快照](#2-函数级快照) | [002-function-snapshots.md](./002-function-snapshots.md) | P1 | 7 天 |
| 003 | [有状态函数](#3-有状态函数) | [003-stateful-functions.md](./003-stateful-functions.md) | P2 | 7 天 |

**总预估时间：19.5 天**

---

## 1. 版本与别名

### 功能概述
实现函数的版本管理和灰度发布能力。

### 核心能力
- **版本快照**：每次部署生成不可变版本 (v1, v2, ...)
- **别名系统**：语义化别名（prod, canary, latest）
- **流量分配**：加权随机路由（90% v1, 10% v2）
- **秒级回滚**：一行命令回滚到任意版本

### 当前基础
- 领域模型已定义（`FunctionVersion`, `FunctionAlias`, `RoutingConfig`）
- 需要补充数据库表、存储层、API 和调度器集成

### 关键文件
| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/storage/postgres.go` | 修改 | 添加版本/别名 CRUD |
| `internal/scheduler/router.go` | 新增 | 流量路由器 |
| `internal/api/version_handler.go` | 新增 | API Handler |
| `migrations/007_versions_aliases.sql` | 新增 | 数据库迁移 |

### 金丝雀发布示例
```bash
# 发布新版本
curl -X POST /api/v1/functions/fn_123/versions -d '{"description": "New feature"}'

# 配置金丝雀（10% 流量到新版本）
curl -X PUT /api/v1/functions/fn_123/aliases/canary -d '{
  "routing_config": {"weights": [{"version": 2, "weight": 90}, {"version": 3, "weight": 10}]}
}'

# 逐步切换
curl -X PUT /api/v1/functions/fn_123/aliases/prod -d '{
  "routing_config": {"weights": [{"version": 3, "weight": 100}]}
}'
```

---

## 2. 函数级快照

### 功能概述
在代码注入并完成初始化后创建快照，实现毫秒级冷启动。

### 核心能力
- **函数级快照**：包含已初始化代码的 VM 快照
- **毫秒级冷启动**：从快照恢复 <100ms
- **按需拉起**：快照存储在磁盘，不占运行时内存
- **自动失效**：代码变更时自动重建

### 当前基础
- Firecracker 快照 API 已存在（运行时级别）
- 需要扩展为函数级别并集成到调度流程

### 性能提升

| 场景 | 当前延迟 | 优化后 |
|------|----------|--------|
| 热启动 | ~2ms | ~2ms |
| **冷启动** | ~175ms | **~50-100ms** |

### 关键文件
| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/snapshot/manager.go` | 新增 | 快照管理器核心 |
| `internal/vmpool/pool.go` | 修改 | 集成快照恢复路径 |
| `internal/firecracker/machine.go` | 修改 | 扩展快照 API |

### 快照生命周期
```
部署函数 → 创建 VM → 注入代码 → 初始化 → 创建快照 → 销毁 VM
                                              ↓
调用函数 → 检查快照 → 存在 → 从快照恢复 VM (~50ms) → 直接执行
                      ↓
                    不存在 → 传统路径 (~175ms)
```

---

## 3. 有状态函数

### 功能概述
为函数提供跨调用的状态持久化能力。

### 核心能力
- **State API**：简单的 get/set/delete/incr 接口
- **会话亲和性**：相同 session_key 路由到同一 VM
- **多作用域**：会话级 / 函数级 / 调用级
- **原子操作**：支持 incr/decr 和乐观锁

### 当前基础
- Redis 连接池已存在
- 需要实现 State Handler、Session Router 和运行时 SDK

### 关键文件
| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/state/handler.go` | 新增 | 状态操作处理器 |
| `internal/scheduler/session_router.go` | 新增 | 会话路由器 |
| `cmd/agent/state.go` | 新增 | Agent 侧 State Client |

### 使用示例 (Python)
```python
def handle(event, state):
    # 读取状态
    cart = state.get('cart') or []

    # 修改状态
    cart.append(event['item_id'])
    state.set('cart', cart, ttl=3600)

    # 原子操作
    count = state.incr('item_count')

    return {'cart_size': len(cart), 'total_items': count}
```

---

## 实施路线图

```
Week 1-2: 版本与别名 (P0)
├── Day 1-2: 数据库表 + 存储层
├── Day 3: TrafficRouter
├── Day 4-5: API Handler + 调度器集成
└── Day 6: 测试 + 文档

Week 2-3: 函数级快照 (P1)
├── Day 1-2: Snapshot Manager
├── Day 3-4: Firecracker 扩展 + VM Pool 集成
├── Day 5-6: 调度器集成 + API
└── Day 7: 测试 + 调优

Week 3-4: 有状态函数 (P2)
├── Day 1-2: State Handler + Client
├── Day 3: 运行时 SDK
├── Day 4-5: Session Router
├── Day 6: API + 配置
└── Day 7: 测试 + 文档
```

---

## 依赖关系

```
┌─────────────────┐
│  版本与别名 (P0) │  ← 独立，无依赖
└────────┬────────┘
         │ 可选依赖（快照按版本创建）
         ▼
┌─────────────────┐
│ 函数级快照 (P1) │  ← 依赖现有 Firecracker 快照
└────────┬────────┘
         │ 无依赖
         ▼
┌─────────────────┐
│ 有状态函数 (P2) │  ← 依赖现有 Redis
└─────────────────┘
```

---

## 风险评估

| 功能 | 主要风险 | 缓解措施 |
|------|----------|----------|
| 版本与别名 | 流量分配不均 | 单元测试 + 监控 |
| 函数级快照 | 快照文件损坏 | 校验和 + 自动降级 |
| 有状态函数 | Redis 单点故障 | Sentinel/Cluster |

---

## 后续规划

完成这三个功能后，可以考虑：

1. **事件总线 (Event Bus)** - 支持 S3/数据库变更触发
2. **VS Code 插件** - IDE 集成和远程调试
3. **网络隔离 (VPC)** - 多租户网络隔离

---

## 文档维护

- **更新频率**：每次实现完成后更新状态
- **版本控制**：重大变更需要更新版本号
- **审核流程**：实现前需要技术评审
