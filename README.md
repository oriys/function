# Nimbus Serverless Platform

轻量级 Serverless 函数计算平台，支持多语言运行时和容器化执行。

## 特性

- **多语言支持**: Python 3.11、Node.js 20、Go 1.24、WebAssembly (Rust/C)
- **容器隔离**: 基于 Docker 的安全执行环境
- **函数管理**: 版本控制、别名路由、灰度发布
- **触发方式**: HTTP API、Webhook、定时任务 (Cron)
- **工作流编排**: 支持复杂的多函数编排
- **可观测性**: Prometheus 指标、Grafana 仪表板、结构化日志
- **Web 控制台**: 现代化的函数管理界面

## 快速开始

### OrbStack / Kubernetes 部署

```bash
cd deployments/k8s/overlays/orbstack

# 一键启动
./start.sh

# 停止服务
./stop.sh
```

### Linux(KVM) / Kubernetes 部署（Firecracker）

适用于 Linux + KVM 的集群部署（Firecracker 模式）：

```bash
cd deployments/k8s/overlays/kvm
./start.sh --context <your-context> --image <your-registry>/nimbus:<tag>
```

详细说明见：`deployments/k8s/overlays/kvm/README.md`

启动后的服务地址：

| 服务 | 地址 |
|------|------|
| API Gateway | http://192.168.139.2:8080 |
| Web Console | http://localhost:32002 |
| Grafana | http://192.168.139.2:3000 |

### Linux (KVM) / Firecracker 单机部署（Docker Compose）

适用于你已经有 Linux + KVM 的单机服务器场景：

```bash
cd deployments/firecracker
docker compose up -d --build
```

详细说明见：`deployments/firecracker/README.md`

### 测试函数调用

```bash
# 创建函数
curl -X POST http://192.168.139.2:8080/api/v1/functions \
  -H "Content-Type: application/json" \
  -d '{
    "name": "hello",
    "runtime": "python3.11",
    "handler": "handler.main",
    "code": "def main(event):\n    return {\"message\": \"Hello \" + event.get(\"name\", \"World\")}"
  }'

# 调用函数
curl -X POST http://192.168.139.2:8080/api/v1/functions/hello/invoke \
  -H "Content-Type: application/json" \
  -d '{"name": "Nimbus"}'
```

## 支持的运行时

| 运行时 | 版本 | 说明 |
|--------|------|------|
| Python | 3.11 | 解释执行 |
| Node.js | 20 | 解释执行 |
| Go | 1.24 | 自动编译 |
| Rust | WASM | 编译为 WebAssembly |
| C | WASM | 编译为 WebAssembly |

## API 参考

### 函数管理

#### 创建函数
```http
POST /api/v1/functions
Content-Type: application/json

{
  "name": "my-function",
  "runtime": "python3.11",
  "handler": "handler.main",
  "code": "def main(event): return {'ok': True}",
  "memory_mb": 256,
  "timeout_sec": 30,
  "tags": ["api", "production"],
  "env_vars": {"DEBUG": "true"}
}
```

**代码大小限制**:
- 源代码: 最大 512KB
- 二进制: 最大 50MB

#### 列出函数（支持搜索过滤）
```http
GET /api/v1/functions?name=hello&tags=api,prod&runtime=python3.11&status=active&limit=20&offset=0
```

| 参数 | 说明 |
|------|------|
| `name` | 名称模糊匹配 |
| `tags` | 标签过滤（逗号分隔，必须包含所有） |
| `runtime` | 运行时精确匹配 |
| `status` | 状态精确匹配 (active/offline/creating/failed) |
| `limit` | 每页数量 (默认 20，最大 100) |
| `offset` | 偏移量 |

响应包含 `code_size` 和 `code_size_limit` 字段。

#### 获取函数详情
```http
GET /api/v1/functions/{id}
```

#### 更新函数
```http
PUT /api/v1/functions/{id}
Content-Type: application/json

{
  "code": "def main(event): return {'updated': True}",
  "tags": ["v2"]
}
```

#### 删除函数
```http
DELETE /api/v1/functions/{id}
```

### 批量操作

#### 批量删除
```http
POST /api/v1/functions/bulk-delete
Content-Type: application/json

{
  "ids": ["func-id-1", "func-id-2", "func-id-3"]
}
```

响应：
```json
{
  "success": ["func-id-1", "func-id-2"],
  "failed": [
    {"id": "func-id-3", "error": "function not found"}
  ]
}
```

#### 批量更新
```http
POST /api/v1/functions/bulk-update
Content-Type: application/json

{
  "ids": ["func-id-1", "func-id-2"],
  "status": "offline",
  "tags": ["deprecated"]
}
```

### 函数调用

#### 同步调用
```http
POST /api/v1/functions/{id}/invoke
Content-Type: application/json

{"key": "value"}
```

#### 异步调用
```http
POST /api/v1/functions/{id}/async
Content-Type: application/json

{"key": "value"}
```

#### Webhook 触发
```http
POST /webhook/{webhook_key}
Content-Type: application/json

{"event": "data"}
```

### 版本管理

```http
GET  /api/v1/functions/{id}/versions           # 列出版本
GET  /api/v1/functions/{id}/versions/{version} # 获取版本
POST /api/v1/functions/{id}/versions/{version}/rollback  # 回滚
```

### 别名管理

```http
GET    /api/v1/functions/{id}/aliases          # 列出别名
POST   /api/v1/functions/{id}/aliases          # 创建别名
PUT    /api/v1/functions/{id}/aliases/{name}   # 更新别名
DELETE /api/v1/functions/{id}/aliases/{name}   # 删除别名
```

### 工作流

```http
POST /api/v1/workflows                    # 创建工作流
GET  /api/v1/workflows                    # 列出工作流
POST /api/v1/workflows/{id}/executions    # 启动执行
GET  /api/v1/executions/{id}              # 获取执行状态
```

### 系统接口

```http
GET /health          # 健康检查
GET /metrics         # Prometheus 指标
GET /api/v1/stats    # 系统统计
GET /api/v1/quota    # 配额使用情况
```

## CLI 工具

```bash
# 安装
make build && sudo make install-cli

# 配置
nimbus config init --api-url http://localhost:8080

# 函数管理
nimbus create hello --runtime python3.11 --handler main.handler --code 'def handler(e): return e'
nimbus list
nimbus get hello
nimbus delete hello

# 函数调用
nimbus invoke hello --data '{"name": "World"}'
nimbus invoke hello --async
```

## MCP Server

支持 Claude Desktop、Cursor 等 AI 工具调用：

```json
{
  "mcpServers": {
    "nimbus": {
      "command": "/path/to/mcp-server",
      "args": ["--api-url", "http://localhost:8080"]
    }
  }
}
```

## 架构

```
┌─────────────────────────────────────────────────────────────┐
│                      API Gateway                            │
│              (Auth, Rate Limit, Routing)                    │
└─────────────────────────┬───────────────────────────────────┘
                          │
        ┌─────────────────┼─────────────────┐
        ▼                 ▼                 ▼
┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│   Function   │  │   Workflow   │  │    Event     │
│   Manager    │  │   Engine     │  │     Bus      │
└──────┬───────┘  └──────┬───────┘  └──────┬───────┘
       │                 │                 │
       └─────────────────┼─────────────────┘
                         ▼
              ┌─────────────────────┐
              │   Docker Executor   │
              │   (Container Pool)  │
              └─────────────────────┘
                         │
       ┌─────────────────┼─────────────────┐
       ▼                 ▼                 ▼
┌────────────┐    ┌────────────┐    ┌────────────┐
│ PostgreSQL │    │   Redis    │    │Prometheus  │
└────────────┘    └────────────┘    └────────────┘
```

## 开发

```bash
# 构建
make build

# 测试
make test

# 启动本地开发环境
docker compose up -d
make run

# Web 控制台开发
cd web && npm install && npm run dev
```

## 许可证

MIT License
