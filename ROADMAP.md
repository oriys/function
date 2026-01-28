# Nimbus Serverless Platform - 功能路线图

> 最后更新：2026-01-28

本文档描述了 Nimbus Serverless Platform 的功能规划和开发路线图。

---

## 目录

- [当前状态概览](#当前状态概览)
- [Phase 1: 补全缺失功能](#phase-1-补全缺失功能)
- [Phase 2: 功能增强](#phase-2-功能增强)
- [Phase 3: 高级特性](#phase-3-高级特性)
- [Phase 4: 企业级功能](#phase-4-企业级功能)
- [技术债务](#技术债务)

---

## 当前状态概览

### 已完成功能

| 模块 | 功能 | 后端 | 前端 | 状态 |
|------|------|:----:|:----:|------|
| 函数管理 | CRUD 操作 | ✅ | ✅ | 完成 |
| 函数管理 | 多运行时 (Python/Node.js/Go/Rust/WASM) | ✅ | ✅ | 完成 |
| 函数管理 | 代码编辑器 (Monaco) | ✅ | ✅ | 完成 |
| 函数管理 | 在线测试/调试 | ✅ | ✅ | 完成 |
| 函数管理 | 函数模板 | ✅ | ✅ | 完成 |
| 工作流 | DAG 可视化编排 | ✅ | ✅ | 完成 |
| 工作流 | 执行追踪 | ✅ | ✅ | 完成 |
| 工作流 | 断点调试 | ✅ | ✅ | 完成 |
| 调用管理 | 调用历史 | ✅ | ✅ | 完成 |
| 调用管理 | 日志查看 | ✅ | ✅ | 完成 |
| 定时任务 | Cron 表达式 | ✅ | ✅ | 完成 |
| 层管理 | 共享依赖层 | ✅ | ✅ | 完成 |
| 环境管理 | 多环境配置 | ✅ | ⚠️ | 部分完成 |
| 监控 | 基础指标 | ✅ | ✅ | 完成 |

### 待完成功能

| 模块 | 功能 | 后端 | 前端 | 状态 |
|------|------|:----:|:----:|------|
| 死信队列 | DLQ 管理 | ✅ | ❌ | 缺少 UI |
| 系统设置 | 配置管理 | ✅ | ⚠️ | UI 未连接 |
| 审计日志 | 操作追踪 | ✅ | ❌ | 缺少 UI |
| 配额管理 | 资源限制 | ✅ | ❌ | 缺少 UI |
| 数据保留 | 清理策略 | ✅ | ❌ | 缺少 UI |

---

## Phase 1: 补全缺失功能

> 预计周期：1-2 周
> 优先级：高
> 目标：将已有后端 API 的功能补全前端 UI

### 1.1 死信队列 (DLQ) 管理页面

**背景**：函数执行失败的消息会进入死信队列，目前无法通过 UI 管理。

**需求描述**：
- 查看 DLQ 消息列表（分页、筛选）
- 查看消息详情（原始输入、错误信息、失败时间）
- 重试单条/批量消息
- 丢弃单条/批量消息
- 清空队列（需二次确认）
- DLQ 统计卡片（总数、待处理、已重试）

**技术实现**：

```
新增文件：
├── web/src/pages/DLQ/
│   ├── index.tsx          # DLQ 列表页面
│   └── Detail.tsx         # DLQ 消息详情页面
├── web/src/components/
│   └── DLQStatsCard.tsx   # 统计卡片组件

修改文件：
├── web/src/App.tsx        # 添加路由
├── web/src/components/Layout/Sidebar.tsx  # 添加菜单项
```

**API 端点**（已实现）：
- `GET /api/v1/dlq` - 列表
- `GET /api/v1/dlq/{id}` - 详情
- `GET /api/v1/dlq/stats` - 统计
- `POST /api/v1/dlq/{id}/retry` - 重试
- `POST /api/v1/dlq/{id}/discard` - 丢弃
- `DELETE /api/v1/dlq` - 清空

**验收标准**：
- [ ] 可查看 DLQ 消息列表
- [ ] 可查看单条消息详情
- [ ] 可重试/丢弃消息
- [ ] 显示 DLQ 统计信息
- [ ] 支持批量操作

---

### 1.2 系统设置页面连接

**背景**：Settings 页面存在但显示硬编码值，未连接后端 API。

**需求描述**：
- 连接 `/api/v1/settings` API
- 显示所有系统配置项
- 支持修改配置值
- 配置分组显示（通用、函数、工作流、安全）
- 修改后即时生效或需重启的提示

**技术实现**：

```
修改文件：
├── web/src/pages/Settings/index.tsx   # 重构连接 API
├── web/src/services/settings.ts       # 新增 settings service

新增配置分组：
├── 通用设置
│   ├── API 地址
│   └── 默认超时时间
├── 函数设置
│   ├── 默认内存限制
│   ├── 最大代码大小
│   └── 最大并发数
├── 工作流设置
│   ├── 默认超时时间
│   └── 最大状态数
└── 安全设置
    ├── API Key 过期时间
    └── 允许的 CORS 域名
```

**API 端点**（已实现）：
- `GET /api/v1/settings` - 获取所有设置
- `GET /api/v1/settings/{key}` - 获取单个设置
- `PUT /api/v1/settings/{key}` - 更新设置

**验收标准**：
- [ ] 正确加载后端配置
- [ ] 可修改配置项
- [ ] 修改后显示成功提示
- [ ] 敏感配置需要确认

---

### 1.3 审计日志页面

**背景**：系统操作审计对安全和问题排查至关重要。

**需求描述**：
- 查看操作日志列表
- 按操作类型、用户、时间范围筛选
- 查看操作详情（操作前后状态对比）
- 导出日志功能

**技术实现**：

```
新增文件：
├── web/src/pages/Audit/
│   └── index.tsx          # 审计日志页面
├── web/src/services/audit.ts
├── web/src/types/audit.ts

审计事件类型：
├── function.create / update / delete
├── workflow.create / update / delete / execute
├── settings.update
├── dlq.retry / discard
└── user.login / logout
```

**API 端点**（已实现）：
- `GET /api/v1/audit` - 列表
- `GET /api/v1/audit/actions` - 操作类型枚举

**验收标准**：
- [ ] 可查看审计日志列表
- [ ] 支持多条件筛选
- [ ] 可查看操作详情
- [ ] 支持导出 CSV/JSON

---

### 1.4 配额管理仪表盘

**背景**：需要让用户了解资源使用情况。

**需求描述**：
- 显示当前配额使用情况
- 可视化图表展示
- 配额告警阈值设置
- 历史使用趋势

**技术实现**：

```
新增文件：
├── web/src/pages/Quota/
│   └── index.tsx          # 配额仪表盘
├── web/src/components/
│   ├── QuotaCard.tsx      # 配额卡片
│   └── QuotaChart.tsx     # 配额图表

配额指标：
├── 函数数量限制
├── 调用次数限制
├── 存储空间限制
├── 并发执行限制
└── 工作流数量限制
```

**API 端点**（已实现）：
- `GET /api/v1/quota` - 配额使用情况

**验收标准**：
- [ ] 显示各项配额使用百分比
- [ ] 进度条可视化
- [ ] 接近限制时显示警告

---

### 1.5 数据保留策略管理

**背景**：调用日志和执行记录会持续增长，需要清理策略。

**需求描述**：
- 查看当前数据统计
- 配置保留天数
- 手动触发清理
- 清理历史记录

**技术实现**：

```
新增文件：
├── web/src/pages/Retention/
│   └── index.tsx          # 保留策略页面
├── web/src/services/retention.ts

保留策略配置：
├── 调用记录保留天数 (默认 30 天)
├── DLQ 消息保留天数 (默认 7 天)
├── 执行记录保留天数 (默认 30 天)
├── 日志保留天数 (默认 14 天)
└── 自动清理开关
```

**API 端点**（已实现）：
- `GET /api/v1/retention/stats` - 统计信息
- `POST /api/v1/retention/cleanup` - 执行清理

**验收标准**：
- [ ] 显示各类数据统计
- [ ] 可配置保留策略
- [ ] 可手动触发清理
- [ ] 显示清理结果

---

## Phase 2: 功能增强

> 预计周期：2-3 周
> 优先级：中
> 目标：增强现有功能的用户体验

### 2.1 函数版本管理

**需求描述**：
- 查看函数版本历史
- 对比不同版本代码差异
- 回滚到指定版本
- 版本备注/标签

**技术实现**：

```
后端修改：
├── internal/api/handler.go
│   ├── ListFunctionVersions()
│   ├── GetFunctionVersion()
│   └── RollbackFunctionVersion()
├── internal/storage/postgres.go
│   └── 新增 function_versions 表

前端新增：
├── web/src/pages/Functions/Versions.tsx
├── web/src/components/CodeDiff.tsx
```

**数据模型**：
```sql
CREATE TABLE function_versions (
    id VARCHAR(36) PRIMARY KEY,
    function_id VARCHAR(36) NOT NULL,
    version INTEGER NOT NULL,
    code TEXT NOT NULL,
    handler VARCHAR(256),
    description TEXT,
    created_at TIMESTAMP WITH TIME ZONE,
    created_by VARCHAR(64)
);
```

---

### 2.2 函数批量操作

**需求描述**：
- 批量启用/禁用函数
- 批量删除函数
- 批量导出函数
- 批量修改标签

**技术实现**：

```
后端新增：
├── POST /api/v1/functions/batch/enable
├── POST /api/v1/functions/batch/disable
├── POST /api/v1/functions/batch/delete
├── POST /api/v1/functions/batch/export
├── POST /api/v1/functions/batch/tags

前端修改：
├── web/src/pages/Functions/List.tsx
│   ├── 添加多选框
│   ├── 批量操作工具栏
│   └── 批量确认对话框
```

---

### 2.3 函数导入/导出

**需求描述**：
- 导出单个/多个函数为 ZIP 包
- 导入 ZIP 包创建函数
- 包含代码、配置、环境变量
- 支持跨环境迁移

**技术实现**：

```
ZIP 包结构：
function-export/
├── manifest.json          # 元数据
├── functions/
│   ├── my-function/
│   │   ├── config.json    # 函数配置
│   │   ├── code.py        # 源代码
│   │   └── env.json       # 环境变量
│   └── another-function/
│       └── ...
└── workflows/             # 可选：关联的工作流
    └── ...
```

**API 端点**：
- `POST /api/v1/functions/export` - 导出
- `POST /api/v1/functions/import` - 导入

---

### 2.4 执行性能分析

**需求描述**：
- 冷启动时间统计
- P50/P95/P99 延迟分布
- 内存使用趋势
- 执行时间热力图

**技术实现**：

```
前端新增：
├── web/src/pages/Functions/Performance.tsx
├── web/src/components/
│   ├── LatencyChart.tsx       # 延迟分布图
│   ├── ColdStartChart.tsx     # 冷启动统计
│   ├── MemoryUsageChart.tsx   # 内存趋势
│   └── ExecutionHeatmap.tsx   # 执行热力图
```

**指标数据**：
```typescript
interface PerformanceMetrics {
  coldStartRate: number;        // 冷启动率
  avgColdStartTime: number;     // 平均冷启动时间
  p50Latency: number;
  p95Latency: number;
  p99Latency: number;
  avgMemoryUsed: number;
  peakMemoryUsed: number;
  executionsByHour: number[];   // 24小时分布
}
```

---

### 2.5 Webhook 增强

**需求描述**：
- 独立的 Webhook 管理页面
- Webhook 调用历史
- 签名验证配置
- IP 白名单
- 速率限制

**技术实现**：

```
前端新增：
├── web/src/pages/Functions/Webhook.tsx
├── web/src/components/
│   ├── WebhookConfig.tsx
│   └── WebhookHistory.tsx

配置选项：
├── 启用/禁用
├── 密钥管理
├── 签名算法 (HMAC-SHA256, etc.)
├── IP 白名单
├── 速率限制 (requests/minute)
└── 重试策略
```

---

## Phase 3: 高级特性

> 预计周期：3-4 周
> 优先级：中低
> 目标：添加高级功能提升平台能力

### 3.1 告警系统

**需求描述**：
- 自定义告警规则
- 多通知渠道（邮件、Webhook、钉钉、企业微信）
- 告警历史
- 告警静默

**告警规则示例**：
```yaml
rules:
  - name: "高错误率告警"
    condition: "error_rate > 5%"
    duration: "5m"
    severity: "critical"
    channels: ["email", "webhook"]

  - name: "延迟告警"
    condition: "p95_latency > 1000ms"
    duration: "10m"
    severity: "warning"
    channels: ["email"]
```

---

### 3.2 A/B 测试 / 金丝雀发布

**需求描述**：
- 函数流量分配
- 按百分比/用户分组分流
- 实时指标对比
- 一键回滚

**技术实现**：
```typescript
interface TrafficSplit {
  functionId: string;
  rules: {
    version: number;
    weight: number;        // 0-100
    conditions?: {
      headers?: Record<string, string>;
      queryParams?: Record<string, string>;
    };
  }[];
}
```

---

### 3.3 函数预热

**需求描述**：
- 配置预热实例数
- 定时预热
- 预热状态监控
- 预热成本估算

---

### 3.4 依赖分析

**需求描述**：
- 函数调用关系图
- 工作流依赖可视化
- 影响分析（修改某函数会影响哪些工作流）
- 未使用函数检测

---

## Phase 4: 企业级功能

> 预计周期：4-6 周
> 优先级：低（按需求）
> 目标：支持企业级部署和多租户

### 4.1 用户与权限管理

**需求描述**：
- 用户注册/登录
- 角色定义（Admin/Developer/Viewer）
- 资源级权限控制
- SSO 集成（LDAP/OAuth2/SAML）

**角色权限矩阵**：
| 操作 | Admin | Developer | Viewer |
|------|:-----:|:---------:|:------:|
| 查看函数 | ✅ | ✅ | ✅ |
| 创建函数 | ✅ | ✅ | ❌ |
| 删除函数 | ✅ | ❌ | ❌ |
| 系统设置 | ✅ | ❌ | ❌ |
| 用户管理 | ✅ | ❌ | ❌ |

---

### 4.2 多租户支持

**需求描述**：
- 租户隔离
- 租户级配额
- 租户级计费
- 租户管理后台

---

### 4.3 计费系统

**需求描述**：
- 执行时长计费
- 内存使用计费
- 调用次数计费
- 账单生成
- 用量报告

**计费模型**：
```typescript
interface BillingMetrics {
  invocations: number;           // 调用次数
  computeTimeMs: number;         // 计算时长 (GB-秒)
  dataTransferBytes: number;     // 数据传输量
  storageBytes: number;          // 存储使用量
}

interface PricingPlan {
  invocationPrice: number;       // 每百万次调用
  computePrice: number;          // 每 GB-秒
  transferPrice: number;         // 每 GB 传输
  storagePrice: number;          // 每 GB 存储/月
}
```

---

### 4.4 高可用部署

**需求描述**：
- 多区域部署
- 自动故障转移
- 数据同步
- 灾难恢复

---

## 技术债务

> 需要持续关注和改进的技术问题

### 代码质量

- [ ] 增加单元测试覆盖率（目标 > 60%）
- [ ] 添加集成测试
- [ ] 统一错误处理模式
- [ ] 完善 API 文档（OpenAPI/Swagger）

### 性能优化

- [ ] 数据库查询优化
- [ ] 添加 Redis 缓存层
- [ ] 实现游标分页（替代 offset）
- [ ] 静态资源 CDN

### 安全加固

- [ ] API 限流
- [ ] 输入验证增强
- [ ] CORS 配置优化
- [ ] 敏感数据加密

### 可观测性

- [ ] 分布式链路追踪
- [ ] 结构化日志
- [ ] 自定义指标
- [ ] 健康检查增强

---

## 版本规划

| 版本 | 主要内容 | 预计时间 |
|------|----------|----------|
| v0.2.0 | Phase 1 完成 | Q1 2026 |
| v0.3.0 | Phase 2 完成 | Q2 2026 |
| v0.4.0 | Phase 3 完成 | Q3 2026 |
| v1.0.0 | 企业级功能 + 稳定性 | Q4 2026 |

---

## 贡献指南

如果你想参与开发，请：

1. 选择一个感兴趣的功能
2. 在 Issues 中创建任务
3. Fork 仓库并创建分支
4. 提交 PR 并关联 Issue

## 联系方式

- GitHub Issues: [提交问题或建议](https://github.com/oriys/nimbus/issues)
- 讨论区: [参与讨论](https://github.com/oriys/nimbus/discussions)
