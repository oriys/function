// Package storage 提供数据存储层的实现，包括 Redis 和 PostgreSQL 两种存储方式。
// 本文件实现了基于 PostgreSQL 的持久化存储功能，主要用于：
//   - 函数(Function)的 CRUD 操作
//   - 函数调用记录(Invocation)的存储和查询
//   - API 密钥的管理
//   - 数据库迁移
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq" // PostgreSQL 驱动
	"github.com/oriys/nimbus/internal/config"
	"github.com/oriys/nimbus/internal/domain"
)

// PostgresStore 是 PostgreSQL 存储的封装结构体。
// 提供函数、调用记录和 API 密钥的持久化存储功能。
type PostgresStore struct {
	db *sql.DB // 数据库连接池
}

// NewPostgresStore 创建并初始化一个新的 PostgreSQL 存储实例。
// 该函数会建立数据库连接、配置连接池参数并执行数据库迁移。
//
// 参数:
//   - cfg: PostgreSQL 配置信息，包含主机、端口、用户名、密码和数据库名等
//
// 返回值:
//   - *PostgresStore: 初始化完成的 PostgreSQL 存储实例
//   - error: 连接失败或迁移失败时返回错误信息
func NewPostgresStore(cfg config.PostgresConfig) (*PostgresStore, error) {
	// 构建 PostgreSQL 连接字符串 (DSN)
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database,
	)

	// 打开数据库连接
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// 配置连接池参数
	db.SetMaxOpenConns(cfg.MaxConnections)      // 最大打开连接数
	db.SetMaxIdleConns(cfg.MaxConnections / 2)  // 最大空闲连接数（设为最大连接数的一半）
	db.SetConnMaxLifetime(30 * time.Minute)     // 连接最大生命周期为 30 分钟（避免长连接问题）
	db.SetConnMaxIdleTime(5 * time.Minute)      // 空闲连接最大存活时间（释放闲置资源）

	// 使用 5 秒超时测试数据库连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	store := &PostgresStore{db: db}
	// 执行数据库迁移，创建所需的表结构
	if err := store.migrate(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return store, nil
}

// migrate 执行数据库迁移，创建应用所需的表结构和索引。
// 使用 IF NOT EXISTS 确保迁移的幂等性。
//
// 返回值:
//   - error: 迁移失败时返回错误信息
func (s *PostgresStore) migrate() error {
	migrations := []string{
		// 创建 functions 表 - 存储函数定义
		// 字段说明：
		//   - id: 函数唯一标识符 (UUID)
		//   - name: 函数名称，全局唯一
		//   - description: 函数描述
		//   - runtime: 运行时类型（如 python3.9, nodejs18 等）
		//   - handler: 函数入口点（如 main.handler）
		//   - code: 函数源代码
		//   - code_hash: 代码哈希值，用于检测代码变更
		//   - memory_mb: 分配的内存大小（MB）
		//   - timeout_sec: 函数执行超时时间（秒）
		//   - env_vars: 环境变量（JSONB 格式）
		//   - status: 函数状态（active/inactive 等）
		//   - version: 函数版本号
		//   - created_at: 创建时间
		//   - updated_at: 最后更新时间
		`CREATE TABLE IF NOT EXISTS functions (
			id VARCHAR(36) PRIMARY KEY,
			name VARCHAR(64) UNIQUE NOT NULL,
			description TEXT,
			runtime VARCHAR(32) NOT NULL,
			handler VARCHAR(256) NOT NULL,
			code TEXT,
			"binary" TEXT,
			code_hash VARCHAR(64),
			memory_mb INTEGER NOT NULL DEFAULT 256,
			timeout_sec INTEGER NOT NULL DEFAULT 30,
			env_vars JSONB DEFAULT '{}',
			status VARCHAR(32) NOT NULL DEFAULT 'active',
			version INTEGER NOT NULL DEFAULT 1,
			cron_expression VARCHAR(128),
			http_path VARCHAR(256),
			http_methods JSONB DEFAULT '[]',
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		// 为函数名称创建索引，加速按名称查询
		`CREATE INDEX IF NOT EXISTS idx_functions_name ON functions(name)`,
		// 为函数状态创建索引，加速按状态筛选
		`CREATE INDEX IF NOT EXISTS idx_functions_status ON functions(status)`,
		// 为定时任务创建索引，加速扫描定时任务
		`CREATE INDEX IF NOT EXISTS idx_functions_cron ON functions(cron_expression) WHERE cron_expression IS NOT NULL`,
		// 为 HTTP 路由创建索引，加速路由匹配
		`CREATE INDEX IF NOT EXISTS idx_functions_http_path ON functions(http_path) WHERE http_path IS NOT NULL`,

		// 创建 invocations 表 - 存储函数调用记录
		// 字段说明：
		//   - id: 调用记录唯一标识符 (UUID)
		//   - function_id: 关联的函数 ID（外键）
		//   - function_name: 函数名称（冗余存储，便于查询）
		//   - trigger_type: 触发类型（http/event/schedule 等）
		//   - status: 调用状态（pending/running/completed/failed 等）
		//   - input: 输入参数（JSONB 格式）
		//   - output: 输出结果（JSONB 格式）
		//   - error: 错误信息
		//   - cold_start: 是否冷启动
		//   - vm_id: 执行该调用的虚拟机 ID
		//   - started_at: 开始执行时间
		//   - completed_at: 完成时间
		//   - duration_ms: 执行耗时（毫秒）
		//   - billed_time_ms: 计费时长（毫秒）
		//   - memory_used_mb: 实际使用内存（MB）
		//   - retry_count: 重试次数
		//   - created_at: 记录创建时间
		`CREATE TABLE IF NOT EXISTS invocations (
			id VARCHAR(36) PRIMARY KEY,
			function_id VARCHAR(36) NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
			function_name VARCHAR(64) NOT NULL,
			trigger_type VARCHAR(32) NOT NULL,
			status VARCHAR(32) NOT NULL,
			input JSONB,
			output JSONB,
			error TEXT,
			cold_start BOOLEAN NOT NULL DEFAULT false,
			vm_id VARCHAR(36),
			started_at TIMESTAMP WITH TIME ZONE,
			completed_at TIMESTAMP WITH TIME ZONE,
			duration_ms BIGINT DEFAULT 0,
			billed_time_ms BIGINT DEFAULT 0,
			memory_used_mb INTEGER DEFAULT 0,
			retry_count INTEGER DEFAULT 0,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		// 为函数 ID 创建索引，加速按函数查询调用记录
		`CREATE INDEX IF NOT EXISTS idx_invocations_function_id ON invocations(function_id)`,
		// 为调用状态创建索引，加速按状态筛选
		`CREATE INDEX IF NOT EXISTS idx_invocations_status ON invocations(status)`,
		// 为创建时间创建索引，加速时间范围查询和排序
		`CREATE INDEX IF NOT EXISTS idx_invocations_created_at ON invocations(created_at)`,
		// 复合索引：函数ID + 创建时间，优化按函数查询最近调用
		`CREATE INDEX IF NOT EXISTS idx_invocations_function_created ON invocations(function_id, created_at DESC)`,
		// 复合索引：状态 + 创建时间，优化按状态筛选并排序
		`CREATE INDEX IF NOT EXISTS idx_invocations_status_created ON invocations(status, created_at DESC)`,

		// 创建 api_keys 表 - 存储 API 密钥
		// 字段说明：
		//   - id: 密钥记录唯一标识符 (UUID)
		//   - name: 密钥名称（便于识别）
		//   - key_hash: 密钥哈希值（不存储明文密钥）
		//   - user_id: 关联的用户 ID
		//   - role: 角色权限（user/admin 等）
		//   - created_at: 创建时间
		//   - expires_at: 过期时间（可选）
		`CREATE TABLE IF NOT EXISTS api_keys (
			id VARCHAR(36) PRIMARY KEY,
			name VARCHAR(64) NOT NULL,
			key_hash VARCHAR(64) UNIQUE NOT NULL,
			user_id VARCHAR(36) NOT NULL,
			role VARCHAR(32) NOT NULL DEFAULT 'user',
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMP WITH TIME ZONE
		)`,
		// 为密钥哈希创建索引，加速密钥验证查询
		`CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash)`,

		// 确保 functions 表中有 binary 字段 (用于升级现有数据库)
		`ALTER TABLE functions ADD COLUMN IF NOT EXISTS "binary" TEXT`,
		// 确保 functions 表中有 cron_expression 字段
		`ALTER TABLE functions ADD COLUMN IF NOT EXISTS cron_expression VARCHAR(128)`,
		// 确保 functions 表中有 http_path 和 http_methods 字段
		`ALTER TABLE functions ADD COLUMN IF NOT EXISTS http_path VARCHAR(256)`,
		`ALTER TABLE functions ADD COLUMN IF NOT EXISTS http_methods JSONB DEFAULT '[]'`,

		// 创建 logs 表 - 存储平台实时日志流（用于控制台/CLI）
		// 字段说明：
		//   - id: 自增主键
		//   - ts: 日志时间戳
		//   - level: 日志级别（DEBUG/INFO/WARN/ERROR）
		//   - function_id/function_name: 关联函数信息（便于过滤）
		//   - message: 日志内容（人类可读）
		//   - request_id: 关联请求/调用的标识（可选）
		//   - input/output: 输入/输出（JSONB，可选）
		//   - error: 错误信息（可选）
		//   - duration_ms: 耗时（毫秒，可选）
		//   - created_at: 记录写入时间
		`CREATE TABLE IF NOT EXISTS logs (
			id BIGSERIAL PRIMARY KEY,
			ts TIMESTAMP WITH TIME ZONE NOT NULL,
			level VARCHAR(16) NOT NULL,
			function_id VARCHAR(36) NOT NULL,
			function_name VARCHAR(64) NOT NULL,
			message TEXT NOT NULL,
			request_id VARCHAR(64),
			input JSONB,
			output JSONB,
			error TEXT,
			duration_ms BIGINT DEFAULT 0,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_ts ON logs(ts)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_function_id ON logs(function_id)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_request_id ON logs(request_id)`,
		// 复合索引：函数ID + 时间戳，优化按函数过滤日志
		`CREATE INDEX IF NOT EXISTS idx_logs_function_ts ON logs(function_id, ts DESC)`,
		// 复合索引：级别 + 时间戳，优化按级别过滤日志
		`CREATE INDEX IF NOT EXISTS idx_logs_level_ts ON logs(level, ts DESC)`,

		// ==================== 版本管理表 ====================
		// 创建 function_versions 表 - 存储函数版本快照
		`CREATE TABLE IF NOT EXISTS function_versions (
			id VARCHAR(36) PRIMARY KEY,
			function_id VARCHAR(36) NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
			version INTEGER NOT NULL,
			handler VARCHAR(256) NOT NULL,
			code TEXT,
			"binary" TEXT,
			code_hash VARCHAR(64) NOT NULL,
			description TEXT,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			UNIQUE(function_id, version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_function_versions_function_id ON function_versions(function_id)`,

		// ==================== 别名与流量分配表 ====================
		// 创建 function_aliases 表 - 存储函数别名和流量路由配置
		`CREATE TABLE IF NOT EXISTS function_aliases (
			id VARCHAR(36) PRIMARY KEY,
			function_id VARCHAR(36) NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
			name VARCHAR(64) NOT NULL,
			description TEXT,
			routing_config JSONB NOT NULL DEFAULT '{}',
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			UNIQUE(function_id, name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_function_aliases_function_id ON function_aliases(function_id)`,

		// ==================== 函数层表 ====================
		// 创建 layers 表 - 存储共享层定义
		`CREATE TABLE IF NOT EXISTS layers (
			id VARCHAR(36) PRIMARY KEY,
			name VARCHAR(128) UNIQUE NOT NULL,
			description TEXT,
			compatible_runtimes TEXT[] NOT NULL,
			latest_version INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_layers_name ON layers(name)`,

		// 创建 layer_versions 表 - 存储层版本
		`CREATE TABLE IF NOT EXISTS layer_versions (
			id VARCHAR(36) PRIMARY KEY,
			layer_id VARCHAR(36) NOT NULL REFERENCES layers(id) ON DELETE CASCADE,
			version INTEGER NOT NULL,
			content BYTEA NOT NULL,
			content_hash VARCHAR(64) NOT NULL,
			size_bytes BIGINT NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			UNIQUE(layer_id, version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_layer_versions_layer_id ON layer_versions(layer_id)`,

		// 创建 function_layers 表 - 存储函数与层的关联
		`CREATE TABLE IF NOT EXISTS function_layers (
			function_id VARCHAR(36) NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
			layer_id VARCHAR(36) NOT NULL REFERENCES layers(id) ON DELETE CASCADE,
			layer_version INTEGER NOT NULL,
			order_index INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(function_id, layer_id)
		)`,

		// ==================== 环境管理表 ====================
		// 创建 environments 表 - 存储环境定义
		`CREATE TABLE IF NOT EXISTS environments (
			id VARCHAR(36) PRIMARY KEY,
			name VARCHAR(64) UNIQUE NOT NULL,
			description TEXT,
			is_default BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_environments_name ON environments(name)`,

		// 创建 function_environment_configs 表 - 存储函数环境配置
		`CREATE TABLE IF NOT EXISTS function_environment_configs (
			function_id VARCHAR(36) NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
			environment_id VARCHAR(36) NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
			env_vars JSONB NOT NULL DEFAULT '{}',
			memory_mb INTEGER,
			timeout_sec INTEGER,
			active_alias VARCHAR(64),
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			PRIMARY KEY(function_id, environment_id)
		)`,

		// 插入默认环境（幂等操作）
		`INSERT INTO environments (id, name, description, is_default)
		 SELECT gen_random_uuid()::text, 'dev', 'Development environment', TRUE
		 WHERE NOT EXISTS (SELECT 1 FROM environments WHERE name = 'dev')`,
		`INSERT INTO environments (id, name, description, is_default)
		 SELECT gen_random_uuid()::text, 'staging', 'Staging environment', FALSE
		 WHERE NOT EXISTS (SELECT 1 FROM environments WHERE name = 'staging')`,
		`INSERT INTO environments (id, name, description, is_default)
		 SELECT gen_random_uuid()::text, 'prod', 'Production environment', FALSE
		 WHERE NOT EXISTS (SELECT 1 FROM environments WHERE name = 'prod')`,

		// ==================== 函数状态流转相关 ====================
		// 为 functions 表添加状态相关字段
		`ALTER TABLE functions ADD COLUMN IF NOT EXISTS status_message TEXT`,
		`ALTER TABLE functions ADD COLUMN IF NOT EXISTS task_id VARCHAR(36)`,
		`ALTER TABLE functions ADD COLUMN IF NOT EXISTS last_deployed_at TIMESTAMP WITH TIME ZONE`,

		// ==================== 函数标签相关 ====================
		// 为 functions 表添加标签字段
		`ALTER TABLE functions ADD COLUMN IF NOT EXISTS tags TEXT[] DEFAULT '{}'`,
		// 为标签创建 GIN 索引，支持数组元素查询
		`CREATE INDEX IF NOT EXISTS idx_functions_tags ON functions USING GIN(tags)`,

		// ==================== 函数置顶/收藏 ====================
		// 为 functions 表添加置顶字段
		`ALTER TABLE functions ADD COLUMN IF NOT EXISTS pinned BOOLEAN DEFAULT FALSE`,
		// 为置顶创建索引，便于排序
		`CREATE INDEX IF NOT EXISTS idx_functions_pinned ON functions(pinned DESC)`,

		// ==================== 并发限制 ====================
		// 为 functions 表添加最大并发数字段
		`ALTER TABLE functions ADD COLUMN IF NOT EXISTS max_concurrency INTEGER DEFAULT 0`,

		// 创建 function_tasks 表 - 存储函数异步任务
		`CREATE TABLE IF NOT EXISTS function_tasks (
			id VARCHAR(36) PRIMARY KEY,
			function_id VARCHAR(36) NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
			type VARCHAR(32) NOT NULL,
			status VARCHAR(32) NOT NULL DEFAULT 'pending',
			input JSONB,
			output JSONB,
			error TEXT,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			started_at TIMESTAMP WITH TIME ZONE,
			completed_at TIMESTAMP WITH TIME ZONE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_function_tasks_function_id ON function_tasks(function_id)`,
		`CREATE INDEX IF NOT EXISTS idx_function_tasks_status ON function_tasks(status)`,

		// ==================== 死信队列 (DLQ) ====================
		// 创建 dead_letter_queue 表 - 存储失败的函数调用
		`CREATE TABLE IF NOT EXISTS dead_letter_queue (
			id VARCHAR(36) PRIMARY KEY,
			function_id VARCHAR(36) NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
			original_request_id VARCHAR(36) NOT NULL,
			payload JSONB NOT NULL,
			error TEXT NOT NULL,
			retry_count INTEGER NOT NULL DEFAULT 0,
			status VARCHAR(32) NOT NULL DEFAULT 'pending',
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			last_retry_at TIMESTAMP WITH TIME ZONE,
			resolved_at TIMESTAMP WITH TIME ZONE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_dlq_function_id ON dead_letter_queue(function_id)`,
		`CREATE INDEX IF NOT EXISTS idx_dlq_status ON dead_letter_queue(status)`,
		`CREATE INDEX IF NOT EXISTS idx_dlq_created_at ON dead_letter_queue(created_at DESC)`,

		// ==================== 系统设置 ====================
		// 创建 system_settings 表 - 存储全局配置
		`CREATE TABLE IF NOT EXISTS system_settings (
			key VARCHAR(64) PRIMARY KEY,
			value TEXT NOT NULL,
			description TEXT,
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		// 插入默认设置
		`INSERT INTO system_settings (key, value, description)
		 SELECT 'log_retention_days', '30', '日志保留天数'
		 WHERE NOT EXISTS (SELECT 1 FROM system_settings WHERE key = 'log_retention_days')`,
		`INSERT INTO system_settings (key, value, description)
		 SELECT 'dlq_retention_days', '90', '死信队列保留天数'
		 WHERE NOT EXISTS (SELECT 1 FROM system_settings WHERE key = 'dlq_retention_days')`,
		// 配额设置
		`INSERT INTO system_settings (key, value, description)
		 SELECT 'quota_max_functions', '100', '最大函数数量'
		 WHERE NOT EXISTS (SELECT 1 FROM system_settings WHERE key = 'quota_max_functions')`,
		`INSERT INTO system_settings (key, value, description)
		 SELECT 'quota_max_memory_mb', '10240', '最大总内存 (MB)'
		 WHERE NOT EXISTS (SELECT 1 FROM system_settings WHERE key = 'quota_max_memory_mb')`,
		`INSERT INTO system_settings (key, value, description)
		 SELECT 'quota_max_invocations_per_day', '100000', '每日最大调用次数'
		 WHERE NOT EXISTS (SELECT 1 FROM system_settings WHERE key = 'quota_max_invocations_per_day')`,
		`INSERT INTO system_settings (key, value, description)
		 SELECT 'quota_max_code_size_kb', '5120', '最大代码大小 (KB)'
		 WHERE NOT EXISTS (SELECT 1 FROM system_settings WHERE key = 'quota_max_code_size_kb')`,

		// ==================== 审计日志 ====================
		// 创建 audit_logs 表 - 存储操作审计日志
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id VARCHAR(36) PRIMARY KEY,
			action VARCHAR(64) NOT NULL,
			resource_type VARCHAR(64) NOT NULL,
			resource_id VARCHAR(36),
			resource_name VARCHAR(256),
			actor VARCHAR(256),
			actor_ip VARCHAR(64),
			details JSONB,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON audit_logs(action)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_resource_type ON audit_logs(resource_type)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_resource_id ON audit_logs(resource_id)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at DESC)`,

		// ==================== Webhook 触发器 ====================
		// 为 functions 表添加 webhook 相关字段
		`ALTER TABLE functions ADD COLUMN IF NOT EXISTS webhook_enabled BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE functions ADD COLUMN IF NOT EXISTS webhook_key VARCHAR(64) UNIQUE`,
		// 为 webhook_key 创建索引，便于通过 webhook_key 查找函数
		`CREATE INDEX IF NOT EXISTS idx_functions_webhook_key ON functions(webhook_key) WHERE webhook_key IS NOT NULL`,

		// ==================== 有状态函数 ====================
		// 为 functions 表添加状态配置字段
		`ALTER TABLE functions ADD COLUMN IF NOT EXISTS state_config JSONB`,

		// ==================== 工作流编排 ====================
		// 创建 workflows 表 - 存储工作流定义
		`CREATE TABLE IF NOT EXISTS workflows (
			id VARCHAR(36) PRIMARY KEY,
			name VARCHAR(64) UNIQUE NOT NULL,
			description TEXT,
			version INTEGER NOT NULL DEFAULT 1,
			status VARCHAR(32) NOT NULL DEFAULT 'active',
			definition JSONB NOT NULL,
			timeout_sec INTEGER NOT NULL DEFAULT 3600,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workflows_name ON workflows(name)`,
		`CREATE INDEX IF NOT EXISTS idx_workflows_status ON workflows(status)`,

		// 创建 workflow_executions 表 - 存储工作流执行实例
		`CREATE TABLE IF NOT EXISTS workflow_executions (
			id VARCHAR(36) PRIMARY KEY,
			workflow_id VARCHAR(36) NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
			workflow_name VARCHAR(64) NOT NULL,
			workflow_version INTEGER NOT NULL DEFAULT 1,
			workflow_definition JSONB,
			status VARCHAR(32) NOT NULL DEFAULT 'pending',
			input JSONB,
			output JSONB,
			error TEXT,
			error_code VARCHAR(128),
			current_state VARCHAR(128),
			started_at TIMESTAMP WITH TIME ZONE,
			completed_at TIMESTAMP WITH TIME ZONE,
			timeout_at TIMESTAMP WITH TIME ZONE,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_executions_workflow_id ON workflow_executions(workflow_id)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_executions_status ON workflow_executions(status)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_executions_created_at ON workflow_executions(created_at DESC)`,
		// 复合索引：工作流ID + 状态，优化按工作流筛选执行
		`CREATE INDEX IF NOT EXISTS idx_workflow_executions_workflow_status ON workflow_executions(workflow_id, status)`,
		// 复合索引：状态 + 创建时间，优化运行中执行列表
		`CREATE INDEX IF NOT EXISTS idx_workflow_executions_status_created ON workflow_executions(status, created_at DESC)`,

		// 创建 state_executions 表 - 存储状态执行历史
		`CREATE TABLE IF NOT EXISTS state_executions (
			id VARCHAR(36) PRIMARY KEY,
			execution_id VARCHAR(36) NOT NULL REFERENCES workflow_executions(id) ON DELETE CASCADE,
			state_name VARCHAR(128) NOT NULL,
			state_type VARCHAR(32) NOT NULL,
			status VARCHAR(32) NOT NULL DEFAULT 'pending',
			input JSONB,
			output JSONB,
			error TEXT,
			error_code VARCHAR(128),
			retry_count INTEGER DEFAULT 0,
			invocation_id VARCHAR(36),
			started_at TIMESTAMP WITH TIME ZONE,
			completed_at TIMESTAMP WITH TIME ZONE,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_state_executions_execution_id ON state_executions(execution_id)`,
		`CREATE INDEX IF NOT EXISTS idx_state_executions_status ON state_executions(status)`,
		// 复合索引：执行ID + 状态名，优化状态查询
		`CREATE INDEX IF NOT EXISTS idx_state_executions_execution_state ON state_executions(execution_id, state_name)`,

		// ==================== 函数模板 ====================
		// 创建 templates 表 - 存储函数模板定义
		`CREATE TABLE IF NOT EXISTS templates (
			id VARCHAR(36) PRIMARY KEY,
			name VARCHAR(64) UNIQUE NOT NULL,
			display_name VARCHAR(128) NOT NULL,
			description TEXT,
			category VARCHAR(32) NOT NULL,
			runtime VARCHAR(32) NOT NULL,
			handler VARCHAR(256) NOT NULL,
			code TEXT NOT NULL,
			variables JSONB DEFAULT '[]',
			default_memory INTEGER DEFAULT 256,
			default_timeout INTEGER DEFAULT 30,
			tags TEXT[] DEFAULT '{}',
			icon VARCHAR(64),
			popular BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_templates_category ON templates(category)`,
		`CREATE INDEX IF NOT EXISTS idx_templates_runtime ON templates(runtime)`,
		`CREATE INDEX IF NOT EXISTS idx_templates_popular ON templates(popular)`,

		// ==================== 工作流调试断点 ====================
		// 创建 execution_breakpoints 表 - 存储执行断点
		`CREATE TABLE IF NOT EXISTS execution_breakpoints (
			id VARCHAR(36) PRIMARY KEY,
			execution_id VARCHAR(36) NOT NULL REFERENCES workflow_executions(id) ON DELETE CASCADE,
			before_state VARCHAR(128) NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_breakpoints_execution_id ON execution_breakpoints(execution_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_execution_breakpoints_unique ON execution_breakpoints(execution_id, before_state)`,

		// 添加暂停相关字段到 workflow_executions
		`ALTER TABLE workflow_executions ADD COLUMN IF NOT EXISTS paused_at_state VARCHAR(128)`,
		`ALTER TABLE workflow_executions ADD COLUMN IF NOT EXISTS paused_input JSONB`,
		`ALTER TABLE workflow_executions ADD COLUMN IF NOT EXISTS paused_at TIMESTAMP WITH TIME ZONE`,

		// 创建 function_snapshots 表 - 存储函数级快照
		`CREATE TABLE IF NOT EXISTS function_snapshots (
			id VARCHAR(36) PRIMARY KEY,
			function_id VARCHAR(36) NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
			version INTEGER NOT NULL,
			code_hash VARCHAR(64) NOT NULL,
			runtime VARCHAR(32) NOT NULL,
			memory_mb INTEGER NOT NULL,
			env_vars_hash VARCHAR(64),
			snapshot_path VARCHAR(512) NOT NULL,
			mem_file_size BIGINT,
			state_file_size BIGINT,
			status VARCHAR(32) DEFAULT 'building',
			error_message TEXT,
			restore_count INTEGER DEFAULT 0,
			avg_restore_ms FLOAT DEFAULT 0,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			last_used_at TIMESTAMP WITH TIME ZONE,
			expires_at TIMESTAMP WITH TIME ZONE,
			UNIQUE(function_id, version, code_hash, env_vars_hash)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_snapshots_function_id ON function_snapshots(function_id)`,
		`CREATE INDEX IF NOT EXISTS idx_snapshots_status ON function_snapshots(status)`,
		`CREATE INDEX IF NOT EXISTS idx_snapshots_expires_at ON function_snapshots(expires_at)`,
	}

	// 依次执行所有迁移语句
	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return err
		}
	}
	return nil
}

// Close 关闭数据库连接。
//
// 返回值:
//   - error: 关闭连接时的错误信息，成功则为 nil
func (s *PostgresStore) Close() error {
	return s.db.Close()
}

// DB 返回底层数据库连接，供需要直接访问数据库的组件使用。
//
// 返回值:
//   - *sql.DB: 数据库连接实例
func (s *PostgresStore) DB() *sql.DB {
	return s.db
}

// ==================== 函数仓库实现 ====================

// CreateFunction 创建一个新的函数记录。
// 如果未提供 ID，将自动生成 UUID。
//
// 参数:
//   - fn: 函数对象，包含所有函数属性
//
// 返回值:
//   - error: 创建失败时返回错误信息（如名称重复）
func (s *PostgresStore) CreateFunction(fn *domain.Function) error {
	// 自动生成 ID（如果未提供）
	if fn.ID == "" {
		fn.ID = uuid.New().String()
	}
	fn.CreatedAt = time.Now()
	fn.UpdatedAt = fn.CreatedAt

	// 将环境变量序列化为 JSON
	envVarsJSON, _ := json.Marshal(fn.EnvVars)
	httpMethodsJSON, _ := json.Marshal(fn.HTTPMethods)

	// 处理 WebhookKey：空字符串转为 NULL，避免 UNIQUE 约束冲突
	var webhookKey interface{}
	if fn.WebhookKey != "" {
		webhookKey = fn.WebhookKey
	}

	// SQL: 插入函数记录到 functions 表
	query := `
		INSERT INTO functions (id, name, description, tags, pinned, runtime, handler, code, "binary", code_hash, memory_mb, timeout_sec, max_concurrency, env_vars, status, status_message, task_id, version, cron_expression, http_path, http_methods, webhook_enabled, webhook_key, last_deployed_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26)
	`
	_, err := s.db.Exec(query,
		fn.ID, fn.Name, fn.Description, pq.Array(fn.Tags), fn.Pinned, fn.Runtime, fn.Handler, fn.Code, fn.Binary, fn.CodeHash,
		fn.MemoryMB, fn.TimeoutSec, fn.MaxConcurrency, envVarsJSON, fn.Status, fn.StatusMessage, fn.TaskID, fn.Version,
		fn.CronExpression, fn.HTTPPath, httpMethodsJSON, fn.WebhookEnabled, webhookKey, fn.LastDeployedAt, fn.CreatedAt, fn.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create function: %w", err)
	}
	return nil
}

// GetFunctionByID 根据函数 ID 获取函数详情。
//
// 参数:
//   - id: 函数唯一标识符
//
// 返回值:
//   - *domain.Function: 函数对象
//   - error: 函数不存在时返回 ErrFunctionNotFound，其他错误返回相应信息
func (s *PostgresStore) GetFunctionByID(id string) (*domain.Function, error) {
	// SQL: 根据 ID 查询函数的所有字段
	query := `
		SELECT id, name, description, tags, pinned, runtime, handler, code, "binary", code_hash, memory_mb, timeout_sec, max_concurrency, env_vars, status, status_message, task_id, version, cron_expression, http_path, http_methods, webhook_enabled, webhook_key, last_deployed_at, state_config, created_at, updated_at
		FROM functions WHERE id = $1
	`
	return s.scanFunction(s.db.QueryRow(query, id))
}

// GetFunctionByName 根据函数名称获取函数详情。
//
// 参数:
//   - name: 函数名称
//
// 返回值:
//   - *domain.Function: 函数对象
//   - error: 函数不存在时返回 ErrFunctionNotFound，其他错误返回相应信息
func (s *PostgresStore) GetFunctionByName(name string) (*domain.Function, error) {
	// SQL: 根据名称查询函数的所有字段
	query := `
		SELECT id, name, description, tags, pinned, runtime, handler, code, "binary", code_hash, memory_mb, timeout_sec, max_concurrency, env_vars, status, status_message, task_id, version, cron_expression, http_path, http_methods, webhook_enabled, webhook_key, last_deployed_at, state_config, created_at, updated_at
		FROM functions WHERE name = $1
	`
	return s.scanFunction(s.db.QueryRow(query, name))
}

// GetFunctionByWebhookKey 根据 Webhook 密钥获取函数详情。
//
// 参数:
//   - webhookKey: Webhook 的唯一密钥
//
// 返回值:
//   - *domain.Function: 函数对象
//   - error: 函数不存在时返回 ErrFunctionNotFound，其他错误返回相应信息
func (s *PostgresStore) GetFunctionByWebhookKey(webhookKey string) (*domain.Function, error) {
	// SQL: 根据 Webhook 密钥查询函数的所有字段
	query := `
		SELECT id, name, description, tags, pinned, runtime, handler, code, "binary", code_hash, memory_mb, timeout_sec, max_concurrency, env_vars, status, status_message, task_id, version, cron_expression, http_path, http_methods, webhook_enabled, webhook_key, last_deployed_at, state_config, created_at, updated_at
		FROM functions WHERE webhook_key = $1 AND webhook_enabled = TRUE
	`
	return s.scanFunction(s.db.QueryRow(query, webhookKey))
}

// ListFunctions 分页查询函数列表。
//
// 参数:
//   - offset: 跳过的记录数（用于分页）
//   - limit: 返回的最大记录数
//
// 返回值:
//   - []*domain.Function: 函数列表
//   - int: 函数总数（用于分页计算）
//   - error: 查询失败时返回错误信息
func (s *PostgresStore) ListFunctions(offset, limit int) ([]*domain.Function, int, error) {
	// SQL: 查询函数总数
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM functions").Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// SQL: 分页查询函数列表，置顶函数优先，按创建时间倒序排列
	query := `
		SELECT id, name, description, tags, pinned, runtime, handler, code, "binary", code_hash, memory_mb, timeout_sec, max_concurrency, env_vars, status, status_message, task_id, version, cron_expression, http_path, http_methods, webhook_enabled, webhook_key, last_deployed_at, state_config, created_at, updated_at
		FROM functions ORDER BY pinned DESC, created_at DESC LIMIT $1 OFFSET $2
	`
	rows, err := s.db.Query(query, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	// 预分配切片容量，减少 append 时的内存重分配
	functions := make([]*domain.Function, 0, limit)
	for rows.Next() {
		fn, err := s.scanFunctionRow(rows)
		if err != nil {
			return nil, 0, err
		}
		functions = append(functions, fn)
	}
	return functions, total, nil
}

// ListFunctionsWithFilter 根据筛选条件分页查询函数列表。
//
// 参数:
//   - filter: 筛选条件（名称模糊匹配、标签、运行时、状态）
//   - offset: 跳过的记录数（用于分页）
//   - limit: 返回的最大记录数
//
// 返回值:
//   - []*domain.Function: 函数列表
//   - int: 符合条件的函数总数（用于分页计算）
//   - error: 查询失败时返回错误信息
func (s *PostgresStore) ListFunctionsWithFilter(filter *domain.FunctionFilter, offset, limit int) ([]*domain.Function, int, error) {
	// 构建动态 WHERE 条件
	var conditions []string
	var args []interface{}
	argIndex := 1

	// 名称模糊匹配
	if filter.Name != "" {
		conditions = append(conditions, fmt.Sprintf("name ILIKE '%%' || $%d || '%%'", argIndex))
		args = append(args, filter.Name)
		argIndex++
	}

	// 标签过滤（必须包含所有指定标签）
	if len(filter.Tags) > 0 {
		conditions = append(conditions, fmt.Sprintf("tags @> $%d", argIndex))
		args = append(args, pq.Array(filter.Tags))
		argIndex++
	}

	// 运行时精确匹配
	if filter.Runtime != "" {
		conditions = append(conditions, fmt.Sprintf("runtime = $%d", argIndex))
		args = append(args, string(filter.Runtime))
		argIndex++
	}

	// 状态精确匹配
	if filter.Status != "" {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argIndex))
		args = append(args, string(filter.Status))
		argIndex++
	}

	// 构建 WHERE 子句
	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	// SQL: 查询符合条件的函数总数
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM functions %s", whereClause)
	var total int
	err := s.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// SQL: 分页查询函数列表，置顶函数优先，按更新时间倒序排列
	selectQuery := fmt.Sprintf(`
		SELECT id, name, description, tags, pinned, runtime, handler, code, "binary", code_hash, memory_mb, timeout_sec, max_concurrency, env_vars, status, status_message, task_id, version, cron_expression, http_path, http_methods, webhook_enabled, webhook_key, last_deployed_at, created_at, updated_at
		FROM functions %s ORDER BY pinned DESC, updated_at DESC LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)
	args = append(args, limit, offset)

	rows, err := s.db.Query(selectQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var functions []*domain.Function
	for rows.Next() {
		fn, err := s.scanFunctionRow(rows)
		if err != nil {
			return nil, 0, err
		}
		functions = append(functions, fn)
	}
	return functions, total, nil
}

// UpdateFunction 更新函数信息。
// 会自动更新 updated_at 时间戳并递增版本号。
//
// 参数:
//   - fn: 包含更新数据的函数对象
//
// 返回值:
//   - error: 函数不存在时返回 ErrFunctionNotFound，其他错误返回相应信息
func (s *PostgresStore) UpdateFunction(fn *domain.Function) error {
	fn.UpdatedAt = time.Now()
	fn.Version++ // 递增版本号

	envVarsJSON, _ := json.Marshal(fn.EnvVars)
	httpMethodsJSON, _ := json.Marshal(fn.HTTPMethods)

	// 处理 StateConfig JSON
	var stateConfigJSON []byte
	if fn.StateConfig != nil {
		stateConfigJSON, _ = json.Marshal(fn.StateConfig)
	}

	// 处理 WebhookKey：空字符串转为 NULL，避免 UNIQUE 约束冲突
	var webhookKey interface{}
	if fn.WebhookKey != "" {
		webhookKey = fn.WebhookKey
	}

	// SQL: 更新函数的可修改字段
	query := `
		UPDATE functions SET
			description = $2, tags = $3, pinned = $4, handler = $5, code = $6, "binary" = $7, code_hash = $8,
			memory_mb = $9, timeout_sec = $10, max_concurrency = $11, env_vars = $12, status = $13, status_message = $14, task_id = $15,
			version = $16, cron_expression = $17, http_path = $18, http_methods = $19, webhook_enabled = $20, webhook_key = $21, last_deployed_at = $22, state_config = $23, updated_at = $24
		WHERE id = $1
	`
	result, err := s.db.Exec(query,
		fn.ID, fn.Description, pq.Array(fn.Tags), fn.Pinned, fn.Handler, fn.Code, fn.Binary, fn.CodeHash,
		fn.MemoryMB, fn.TimeoutSec, fn.MaxConcurrency, envVarsJSON, fn.Status, fn.StatusMessage, fn.TaskID,
		fn.Version, fn.CronExpression, fn.HTTPPath, httpMethodsJSON, fn.WebhookEnabled, webhookKey, fn.LastDeployedAt, stateConfigJSON, fn.UpdatedAt,
	)
	if err != nil {
		return err
	}
	// 检查是否有记录被更新
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if affected == 0 {
		return domain.ErrFunctionNotFound
	}
	return nil
}

// UpdateFunctionBinary 仅更新函数的编译后二进制数据。
// 用于异步编译完成后单独更新二进制字段，避免覆盖其他并发修改。
//
// 参数:
//   - id: 函数唯一标识符
//   - binary: Base64 编码的编译后二进制数据
//
// 返回值:
//   - error: 函数不存在时返回 ErrFunctionNotFound，其他错误返回相应信息
func (s *PostgresStore) UpdateFunctionBinary(id, binary string) error {
	query := `UPDATE functions SET "binary" = $2, updated_at = $3 WHERE id = $1`
	result, err := s.db.Exec(query, id, binary, time.Now())
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if affected == 0 {
		return domain.ErrFunctionNotFound
	}
	return nil
}

// GetFunctionsByStatuses 根据多个状态查询函数列表
// 用于服务启动时恢复未完成的编译任务
//
// 参数:
//   - statuses: 状态列表，如 ["creating", "updating", "building"]
//
// 返回值:
//   - []*domain.Function: 符合条件的函数列表
//   - error: 查询错误
func (s *PostgresStore) GetFunctionsByStatuses(statuses []string) ([]*domain.Function, error) {
	if len(statuses) == 0 {
		return nil, nil
	}

	query := `
		SELECT id, name, description, tags, pinned, runtime, handler, code, "binary", code_hash, memory_mb, timeout_sec, max_concurrency, env_vars, status, status_message, task_id, version, cron_expression, http_path, http_methods, webhook_enabled, webhook_key, last_deployed_at, created_at, updated_at
		FROM functions WHERE status = ANY($1)
	`
	rows, err := s.db.Query(query, pq.Array(statuses))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var functions []*domain.Function
	for rows.Next() {
		fn, err := s.scanFunctionRow(rows)
		if err != nil {
			return nil, err
		}
		functions = append(functions, fn)
	}
	return functions, nil
}

// DeleteFunction 删除指定的函数。
// 关联的调用记录会因外键级联删除而自动清除。
//
// 参数:
//   - id: 函数唯一标识符
//
// 返回值:
//   - error: 函数不存在时返回 ErrFunctionNotFound，其他错误返回相应信息
func (s *PostgresStore) DeleteFunction(id string) error {
	// SQL: 根据 ID 删除函数
	result, err := s.db.Exec("DELETE FROM functions WHERE id = $1", id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if affected == 0 {
		return domain.ErrFunctionNotFound
	}
	return nil
}

// GetFunctionByPath 根据自定义 HTTP 路径获取函数。
func (s *PostgresStore) GetFunctionByPath(path string) (*domain.Function, error) {
	// SQL: 根据 http_path 查询函数
	query := `
		SELECT id, name, description, runtime, handler, code, "binary", code_hash, memory_mb, timeout_sec, env_vars, status, version, cron_expression, http_path, http_methods, created_at, updated_at
		FROM functions WHERE http_path = $1
	`
	return s.scanFunction(s.db.QueryRow(query, path))
}

// UpdateFunctionPin 更新函数的置顶状态。
//
// 参数:
//   - id: 函数唯一标识符
//   - pinned: 置顶状态
//
// 返回值:
//   - error: 函数不存在时返回 ErrFunctionNotFound，其他错误返回相应信息
func (s *PostgresStore) UpdateFunctionPin(id string, pinned bool) error {
	query := `UPDATE functions SET pinned = $2, updated_at = NOW() WHERE id = $1`
	result, err := s.db.Exec(query, id, pinned)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if affected == 0 {
		return domain.ErrFunctionNotFound
	}
	return nil
}

// scanFunction 从单行查询结果中扫描函数数据。
// 内部辅助方法，用于 GetFunctionByID 和 GetFunctionByName。
//
// 参数:
//   - row: 单行查询结果
//
// 返回值:
//   - *domain.Function: 解析后的函数对象
//   - error: 扫描失败或记录不存在时返回错误
func (s *PostgresStore) scanFunction(row *sql.Row) (*domain.Function, error) {
	fn := &domain.Function{}
	var envVarsJSON, httpMethodsJSON, stateConfigJSON []byte
	var description, code, binary, codeHash, cronExpression, httpPath, statusMessage, taskID, webhookKey sql.NullString
	var lastDeployedAt sql.NullTime
	err := row.Scan(
		&fn.ID, &fn.Name, &description, pq.Array(&fn.Tags), &fn.Pinned, &fn.Runtime, &fn.Handler, &code, &binary, &codeHash,
		&fn.MemoryMB, &fn.TimeoutSec, &fn.MaxConcurrency, &envVarsJSON, &fn.Status, &statusMessage, &taskID, &fn.Version,
		&cronExpression, &httpPath, &httpMethodsJSON, &fn.WebhookEnabled, &webhookKey, &lastDeployedAt, &stateConfigJSON, &fn.CreatedAt, &fn.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, domain.ErrFunctionNotFound
	}
	if err != nil {
		return nil, err
	}
	// 处理可空字段
	if description.Valid {
		fn.Description = description.String
	}
	if code.Valid {
		fn.Code = code.String
	}
	if binary.Valid {
		fn.Binary = binary.String
	}
	if codeHash.Valid {
		fn.CodeHash = codeHash.String
	}
	if cronExpression.Valid {
		fn.CronExpression = cronExpression.String
	}
	if httpPath.Valid {
		fn.HTTPPath = httpPath.String
	}
	if statusMessage.Valid {
		fn.StatusMessage = statusMessage.String
	}
	if taskID.Valid {
		fn.TaskID = taskID.String
	}
	if webhookKey.Valid {
		fn.WebhookKey = webhookKey.String
	}
	if lastDeployedAt.Valid {
		fn.LastDeployedAt = &lastDeployedAt.Time
	}
	// 反序列化 JSON 字段
	json.Unmarshal(envVarsJSON, &fn.EnvVars)
	json.Unmarshal(httpMethodsJSON, &fn.HTTPMethods)
	if len(stateConfigJSON) > 0 {
		json.Unmarshal(stateConfigJSON, &fn.StateConfig)
	}
	return fn, nil
}

// scanFunctionRow 从多行查询结果中扫描单个函数数据。
// 内部辅助方法，用于 ListFunctions。
//
// 参数:
//   - rows: 多行查询结果的当前行
//
// 返回值:
//   - *domain.Function: 解析后的函数对象
//   - error: 扫描失败时返回错误
func (s *PostgresStore) scanFunctionRow(rows *sql.Rows) (*domain.Function, error) {
	fn := &domain.Function{}
	var envVarsJSON, httpMethodsJSON, stateConfigJSON []byte
	var description, code, binary, codeHash, cronExpression, httpPath, statusMessage, taskID, webhookKey sql.NullString
	var lastDeployedAt sql.NullTime
	err := rows.Scan(
		&fn.ID, &fn.Name, &description, pq.Array(&fn.Tags), &fn.Pinned, &fn.Runtime, &fn.Handler, &code, &binary, &codeHash,
		&fn.MemoryMB, &fn.TimeoutSec, &fn.MaxConcurrency, &envVarsJSON, &fn.Status, &statusMessage, &taskID, &fn.Version,
		&cronExpression, &httpPath, &httpMethodsJSON, &fn.WebhookEnabled, &webhookKey, &lastDeployedAt, &stateConfigJSON, &fn.CreatedAt, &fn.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	// 处理可空字段
	if description.Valid {
		fn.Description = description.String
	}
	if code.Valid {
		fn.Code = code.String
	}
	if binary.Valid {
		fn.Binary = binary.String
	}
	if codeHash.Valid {
		fn.CodeHash = codeHash.String
	}
	if cronExpression.Valid {
		fn.CronExpression = cronExpression.String
	}
	if httpPath.Valid {
		fn.HTTPPath = httpPath.String
	}
	if statusMessage.Valid {
		fn.StatusMessage = statusMessage.String
	}
	if taskID.Valid {
		fn.TaskID = taskID.String
	}
	if webhookKey.Valid {
		fn.WebhookKey = webhookKey.String
	}
	if lastDeployedAt.Valid {
		fn.LastDeployedAt = &lastDeployedAt.Time
	}
	// 反序列化 JSON 字段
	json.Unmarshal(envVarsJSON, &fn.EnvVars)
	json.Unmarshal(httpMethodsJSON, &fn.HTTPMethods)
	if len(stateConfigJSON) > 0 {
		json.Unmarshal(stateConfigJSON, &fn.StateConfig)
	}
	return fn, nil
}

// ==================== 调用记录仓库实现 ====================

// CreateInvocation 创建一个新的函数调用记录。
// 如果未提供 ID，将自动生成 UUID。
//
// 参数:
//   - inv: 调用记录对象
//
// 返回值:
//   - error: 创建失败时返回错误信息
func (s *PostgresStore) CreateInvocation(inv *domain.Invocation) error {
	// 自动生成 ID（如果未提供）
	if inv.ID == "" {
		inv.ID = uuid.New().String()
	}

	// SQL: 插入调用记录的初始信息
	query := `
		INSERT INTO invocations (id, function_id, function_name, trigger_type, status, input, cold_start, retry_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := s.db.Exec(query,
		inv.ID, inv.FunctionID, inv.FunctionName, inv.TriggerType, inv.Status,
		inv.Input, inv.ColdStart, inv.RetryCount, inv.CreatedAt,
	)
	return err
}

// GetInvocationByID 根据调用 ID 获取调用记录详情。
//
// 参数:
//   - id: 调用记录唯一标识符
//
// 返回值:
//   - *domain.Invocation: 调用记录对象
//   - error: 记录不存在时返回 ErrInvocationNotFound，其他错误返回相应信息
func (s *PostgresStore) GetInvocationByID(id string) (*domain.Invocation, error) {
	// SQL: 根据 ID 查询调用记录的所有字段
	query := `
		SELECT id, function_id, function_name, trigger_type, status, input, output, error,
		       cold_start, vm_id, started_at, completed_at, duration_ms, billed_time_ms,
		       memory_used_mb, retry_count, created_at
		FROM invocations WHERE id = $1
	`
	inv := &domain.Invocation{}
	// 处理可能为空的字段
	var vmID sql.NullString
	var input, output []byte
	var errStr sql.NullString
	err := s.db.QueryRow(query, id).Scan(
		&inv.ID, &inv.FunctionID, &inv.FunctionName, &inv.TriggerType, &inv.Status,
		&input, &output, &errStr, &inv.ColdStart, &vmID,
		&inv.StartedAt, &inv.CompletedAt, &inv.DurationMs, &inv.BilledTimeMs,
		&inv.MemoryUsedMB, &inv.RetryCount, &inv.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, domain.ErrInvocationNotFound
	}
	if err != nil {
		return nil, err
	}
	// 处理可空字段
	if vmID.Valid {
		inv.VMID = vmID.String
	}
	if input != nil {
		inv.Input = input
	}
	if output != nil {
		inv.Output = output
	}
	if errStr.Valid {
		inv.Error = errStr.String
	}
	return inv, nil
}

// ListInvocationsByFunction 分页查询指定函数的调用记录。
//
// 参数:
//   - functionID: 函数唯一标识符
//   - offset: 跳过的记录数（用于分页）
//   - limit: 返回的最大记录数
//
// 返回值:
//   - []*domain.Invocation: 调用记录列表
//   - int: 调用记录总数（用于分页计算）
//   - error: 查询失败时返回错误信息
func (s *PostgresStore) ListInvocationsByFunction(functionID string, offset, limit int) ([]*domain.Invocation, int, error) {
	// SQL: 查询指定函数的调用记录总数
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM invocations WHERE function_id = $1", functionID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// SQL: 分页查询调用记录，按创建时间倒序排列
	query := `
		SELECT id, function_id, function_name, trigger_type, status, input, output, error,
		       cold_start, vm_id, started_at, completed_at, duration_ms, billed_time_ms,
		       memory_used_mb, retry_count, created_at
		FROM invocations WHERE function_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`
	rows, err := s.db.Query(query, functionID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var invocations []*domain.Invocation
	for rows.Next() {
		inv := &domain.Invocation{}
		var vmID sql.NullString
		var input, output []byte
		var errStr sql.NullString
		err := rows.Scan(
			&inv.ID, &inv.FunctionID, &inv.FunctionName, &inv.TriggerType, &inv.Status,
			&input, &output, &errStr, &inv.ColdStart, &vmID,
			&inv.StartedAt, &inv.CompletedAt, &inv.DurationMs, &inv.BilledTimeMs,
			&inv.MemoryUsedMB, &inv.RetryCount, &inv.CreatedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		if vmID.Valid {
			inv.VMID = vmID.String
		}
		if input != nil {
			inv.Input = input
		}
		if output != nil {
			inv.Output = output
		}
		if errStr.Valid {
			inv.Error = errStr.String
		}
		invocations = append(invocations, inv)
	}
	return invocations, total, nil
}

// UpdateInvocation 更新调用记录。
// 通常在调用完成后调用，更新输出结果、执行时间等信息。
//
// 参数:
//   - inv: 包含更新数据的调用记录对象
//
// 返回值:
//   - error: 记录不存在时返回 ErrInvocationNotFound，其他错误返回相应信息
func (s *PostgresStore) UpdateInvocation(inv *domain.Invocation) error {
	// JSONB 字段需要特别处理：如果传入的是“typed nil”（例如 json.RawMessage(nil)），
	// pq 会将其当作空字符串而不是 NULL，导致 JSON 解析失败。
	var output any
	if len(inv.Output) == 0 {
		output = nil
	} else {
		output = inv.Output
	}

	// SQL: 更新调用记录的执行结果相关字段
	query := `
		UPDATE invocations SET
			status = $2, output = $3, error = $4, cold_start = $5, vm_id = $6,
			started_at = $7, completed_at = $8, duration_ms = $9, billed_time_ms = $10,
			memory_used_mb = $11, retry_count = $12
		WHERE id = $1
	`
	result, err := s.db.Exec(query,
		inv.ID, inv.Status, output, inv.Error, inv.ColdStart, inv.VMID,
		inv.StartedAt, inv.CompletedAt, inv.DurationMs, inv.BilledTimeMs,
		inv.MemoryUsedMB, inv.RetryCount,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if affected == 0 {
		return domain.ErrInvocationNotFound
	}
	return nil
}

// ==================== 健康检查和统计方法 ====================

// Ping 检查数据库连接是否正常。
// 用于健康检查和连接池验证。
//
// 返回值:
//   - error: 连接异常时返回错误信息
func (s *PostgresStore) Ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.db.PingContext(ctx)
}

// CountFunctions 统计函数总数。
//
// 返回值:
//   - int: 函数总数
//   - error: 查询失败时返回错误信息
func (s *PostgresStore) CountFunctions() (int, error) {
	var count int
	// SQL: 统计 functions 表的记录总数
	err := s.db.QueryRow("SELECT COUNT(*) FROM functions").Scan(&count)
	return count, err
}

// CountActiveFunctions 统计活跃状态的函数数量。
//
// 返回值:
//   - int: 活跃函数数量
//   - error: 查询失败时返回错误信息
func (s *PostgresStore) CountActiveFunctions() (int, error) {
	var count int
	// SQL: 统计状态为 'active' 的函数数量
	err := s.db.QueryRow("SELECT COUNT(*) FROM functions WHERE status = 'active'").Scan(&count)
	return count, err
}

// CountInvocations 统计调用记录总数。
//
// 返回值:
//   - int: 调用记录总数
//   - error: 查询失败时返回错误信息
func (s *PostgresStore) CountInvocations() (int, error) {
	var count int
	// SQL: 统计 invocations 表的记录总数
	err := s.db.QueryRow("SELECT COUNT(*) FROM invocations").Scan(&count)
	return count, err
}

// ==================== API 密钥管理方法 ====================

// CreateAPIKey 创建一个新的 API 密钥记录。
// 注意：密钥以哈希形式存储，不保存明文。
//
// 参数:
//   - id: 密钥记录唯一标识符
//   - name: 密钥名称（用于识别）
//   - keyHash: 密钥的哈希值
//   - userID: 关联的用户 ID
//   - role: 角色权限（如 user, admin）
//
// 返回值:
//   - error: 创建失败时返回错误信息（如哈希值重复）
func (s *PostgresStore) CreateAPIKey(id, name, keyHash, userID, role string) error {
	// SQL: 插入 API 密钥记录
	query := `INSERT INTO api_keys (id, name, key_hash, user_id, role) VALUES ($1, $2, $3, $4, $5)`
	_, err := s.db.Exec(query, id, name, keyHash, userID, role)
	return err
}

// GetAPIKeyByHash 根据密钥哈希值获取 API 密钥信息。
// 同时验证密钥是否过期。
//
// 参数:
//   - keyHash: 密钥的哈希值
//
// 返回值:
//   - string: 密钥记录 ID
//   - string: 关联的用户 ID
//   - string: 角色权限
//   - error: 密钥不存在或已过期时返回错误信息
func (s *PostgresStore) GetAPIKeyByHash(keyHash string) (string, string, string, error) {
	var userID, role, id string
	// SQL: 根据哈希值查询未过期的 API 密钥
	// expires_at IS NULL 表示永不过期，或 expires_at > NOW() 表示尚未过期
	query := `SELECT id, user_id, role FROM api_keys WHERE key_hash = $1 AND (expires_at IS NULL OR expires_at > NOW())`
	err := s.db.QueryRow(query, keyHash).Scan(&id, &userID, &role)
	if err == sql.ErrNoRows {
		return "", "", "", errors.New("api key not found")
	}
	return id, userID, role, err
}

// DeleteAPIKey 删除指定的 API 密钥。
//
// 参数:
//   - id: 密钥记录唯一标识符
//
// 返回值:
//   - error: 删除失败时返回错误信息
func (s *PostgresStore) DeleteAPIKey(id string) error {
	// SQL: 根据 ID 删除 API 密钥
	_, err := s.db.Exec("DELETE FROM api_keys WHERE id = $1", id)
	return err
}

// APIKeyInfo 表示 API 密钥的基本信息（不包含敏感的哈希值）
type APIKeyInfo struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	UserID    string     `json:"user_id"`
	Role      string     `json:"role"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// ListAPIKeysByUser 获取指定用户的所有 API 密钥列表。
//
// 参数:
//   - userID: 用户 ID
//
// 返回值:
//   - []APIKeyInfo: API 密钥信息列表
//   - error: 查询失败时返回错误信息
func (s *PostgresStore) ListAPIKeysByUser(userID string) ([]APIKeyInfo, error) {
	query := `SELECT id, name, user_id, role, created_at, expires_at FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC`
	rows, err := s.db.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKeyInfo
	for rows.Next() {
		var key APIKeyInfo
		if err := rows.Scan(&key.ID, &key.Name, &key.UserID, &key.Role, &key.CreatedAt, &key.ExpiresAt); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// DeleteAPIKeyByUser 删除指定用户的 API 密钥（确保只能删除自己的密钥）。
//
// 参数:
//   - id: 密钥记录唯一标识符
//   - userID: 用户 ID
//
// 返回值:
//   - error: 删除失败时返回错误信息
func (s *PostgresStore) DeleteAPIKeyByUser(id, userID string) error {
	result, err := s.db.Exec("DELETE FROM api_keys WHERE id = $1 AND user_id = $2", id, userID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("api key not found or not owned by user")
	}
	return nil
}

// ==================== 仪表板统计方法 ====================

// DashboardStats 仪表板统计数据
type DashboardStats struct {
	TotalInvocations int64   `json:"total_invocations"`
	SuccessCount     int64   `json:"success_count"`
	FailedCount      int64   `json:"failed_count"`
	SuccessRate      float64 `json:"success_rate"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	P99LatencyMs     float64 `json:"p99_latency_ms"`
	ColdStartCount   int64   `json:"cold_start_count"`
	ColdStartRate    float64 `json:"cold_start_rate"`
	TotalFunctions   int     `json:"total_functions"`
	ActiveFunctions  int     `json:"active_functions"`
}

// GetDashboardStats 获取仪表板统计数据
func (s *PostgresStore) GetDashboardStats(periodHours int) (*DashboardStats, error) {
	stats := &DashboardStats{}

	// 获取函数统计
	s.db.QueryRow("SELECT COUNT(*) FROM functions").Scan(&stats.TotalFunctions)
	s.db.QueryRow("SELECT COUNT(*) FROM functions WHERE status = 'active'").Scan(&stats.ActiveFunctions)

	// 获取调用统计（基于时间段）
	query := `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'success' OR status = 'completed') as success,
			COUNT(*) FILTER (WHERE status = 'failed' OR status = 'timeout') as failed,
			COUNT(*) FILTER (WHERE cold_start = true) as cold_starts,
			COALESCE(AVG(duration_ms), 0) as avg_latency,
			COALESCE(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY duration_ms), 0) as p99_latency
		FROM invocations
		WHERE created_at >= NOW() - INTERVAL '1 hour' * $1
	`
	err := s.db.QueryRow(query, periodHours).Scan(
		&stats.TotalInvocations,
		&stats.SuccessCount,
		&stats.FailedCount,
		&stats.ColdStartCount,
		&stats.AvgLatencyMs,
		&stats.P99LatencyMs,
	)
	if err != nil {
		return stats, nil // 返回空统计而不是错误
	}

	// 计算比率
	if stats.TotalInvocations > 0 {
		stats.SuccessRate = float64(stats.SuccessCount) / float64(stats.TotalInvocations) * 100
		stats.ColdStartRate = float64(stats.ColdStartCount) / float64(stats.TotalInvocations) * 100
	}

	return stats, nil
}

// TrendDataPoint 趋势数据点
type TrendDataPoint struct {
	Timestamp    time.Time `json:"timestamp"`
	Invocations  int64     `json:"invocations"`
	Errors       int64     `json:"errors"`
	AvgLatencyMs float64   `json:"avg_latency_ms"`
}

// GetInvocationTrends 获取调用趋势数据
func (s *PostgresStore) GetInvocationTrends(periodHours int, granularityHours int) ([]TrendDataPoint, error) {
	query := `
		SELECT
			date_trunc('hour', created_at) as hour,
			COUNT(*) as invocations,
			COUNT(*) FILTER (WHERE status = 'failed' OR status = 'timeout') as errors,
			COALESCE(AVG(duration_ms), 0) as avg_latency
		FROM invocations
		WHERE created_at >= NOW() - INTERVAL '1 hour' * $1
		GROUP BY date_trunc('hour', created_at)
		ORDER BY hour ASC
	`
	rows, err := s.db.Query(query, periodHours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var trends []TrendDataPoint
	for rows.Next() {
		var t TrendDataPoint
		if err := rows.Scan(&t.Timestamp, &t.Invocations, &t.Errors, &t.AvgLatencyMs); err != nil {
			continue
		}
		trends = append(trends, t)
	}

	// 如果没有数据，生成空的时间点
	if len(trends) == 0 {
		now := time.Now().Truncate(time.Hour)
		for i := periodHours - 1; i >= 0; i-- {
			trends = append(trends, TrendDataPoint{
				Timestamp:    now.Add(-time.Duration(i) * time.Hour),
				Invocations:  0,
				Errors:       0,
				AvgLatencyMs: 0,
			})
		}
	}

	return trends, nil
}

// TopFunction 热门函数
type TopFunction struct {
	FunctionID   string  `json:"function_id"`
	FunctionName string  `json:"function_name"`
	Invocations  int64   `json:"invocations"`
	Percentage   float64 `json:"percentage"`
}

// GetTopFunctions 获取热门函数
func (s *PostgresStore) GetTopFunctions(periodHours int, limit int) ([]TopFunction, error) {
	// 首先获取总调用数
	var totalInvocations int64
	s.db.QueryRow(`
		SELECT COUNT(*) FROM invocations
		WHERE created_at >= NOW() - INTERVAL '1 hour' * $1
	`, periodHours).Scan(&totalInvocations)

	query := `
		SELECT
			function_id,
			function_name,
			COUNT(*) as invocations
		FROM invocations
		WHERE created_at >= NOW() - INTERVAL '1 hour' * $1
		GROUP BY function_id, function_name
		ORDER BY invocations DESC
		LIMIT $2
	`
	rows, err := s.db.Query(query, periodHours, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tops []TopFunction
	for rows.Next() {
		var t TopFunction
		if err := rows.Scan(&t.FunctionID, &t.FunctionName, &t.Invocations); err != nil {
			continue
		}
		if totalInvocations > 0 {
			t.Percentage = float64(t.Invocations) / float64(totalInvocations) * 100
		}
		tops = append(tops, t)
	}

	return tops, nil
}

// RecentInvocation 最近调用
type RecentInvocation struct {
	ID           string    `json:"id"`
	FunctionID   string    `json:"function_id"`
	FunctionName string    `json:"function_name"`
	Status       string    `json:"status"`
	DurationMs   int64     `json:"duration_ms"`
	ColdStart    bool      `json:"cold_start"`
	CreatedAt    time.Time `json:"created_at"`
}

// GetRecentInvocations 获取最近调用
func (s *PostgresStore) GetRecentInvocations(limit int) ([]RecentInvocation, error) {
	query := `
		SELECT id, function_id, function_name, status, duration_ms, cold_start, created_at
		FROM invocations
		ORDER BY created_at DESC
		LIMIT $1
	`
	rows, err := s.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invocations []RecentInvocation
	for rows.Next() {
		var inv RecentInvocation
		if err := rows.Scan(&inv.ID, &inv.FunctionID, &inv.FunctionName, &inv.Status, &inv.DurationMs, &inv.ColdStart, &inv.CreatedAt); err != nil {
			continue
		}
		invocations = append(invocations, inv)
	}

	return invocations, nil
}

// ListAllInvocations 分页查询所有调用记录
func (s *PostgresStore) ListAllInvocations(status string, offset, limit int) ([]*domain.Invocation, int, error) {
	var total int
	var countQuery, listQuery string
	var countArgs, listArgs []interface{}

	if status != "" {
		countQuery = "SELECT COUNT(*) FROM invocations WHERE status = $1"
		countArgs = []interface{}{status}
		listQuery = `
			SELECT id, function_id, function_name, trigger_type, status, input, output, error,
			       cold_start, vm_id, started_at, completed_at, duration_ms, billed_time_ms,
			       memory_used_mb, retry_count, created_at
			FROM invocations WHERE status = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3
		`
		listArgs = []interface{}{status, limit, offset}
	} else {
		countQuery = "SELECT COUNT(*) FROM invocations"
		countArgs = nil
		listQuery = `
			SELECT id, function_id, function_name, trigger_type, status, input, output, error,
			       cold_start, vm_id, started_at, completed_at, duration_ms, billed_time_ms,
			       memory_used_mb, retry_count, created_at
			FROM invocations ORDER BY created_at DESC LIMIT $1 OFFSET $2
		`
		listArgs = []interface{}{limit, offset}
	}

	if countArgs != nil {
		err := s.db.QueryRow(countQuery, countArgs...).Scan(&total)
		if err != nil {
			return nil, 0, err
		}
	} else {
		err := s.db.QueryRow(countQuery).Scan(&total)
		if err != nil {
			return nil, 0, err
		}
	}

	rows, err := s.db.Query(listQuery, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var invocations []*domain.Invocation
	for rows.Next() {
		inv := &domain.Invocation{}
		var vmID sql.NullString
		var input, output []byte
		var errStr sql.NullString
		err := rows.Scan(
			&inv.ID, &inv.FunctionID, &inv.FunctionName, &inv.TriggerType, &inv.Status,
			&input, &output, &errStr, &inv.ColdStart, &vmID,
			&inv.StartedAt, &inv.CompletedAt, &inv.DurationMs, &inv.BilledTimeMs,
			&inv.MemoryUsedMB, &inv.RetryCount, &inv.CreatedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		if vmID.Valid {
			inv.VMID = vmID.String
		}
		if input != nil {
			inv.Input = input
		}
		if output != nil {
			inv.Output = output
		}
		if errStr.Valid {
			inv.Error = errStr.String
		}
		invocations = append(invocations, inv)
	}
	return invocations, total, nil
}

// CreateLogEntry 写入一条日志记录到 logs 表。
// 该表用于“采集到数据库再推送”的日志流模式（先落库，再通过 WebSocket 推送）。
func (s *PostgresStore) CreateLogEntry(ctx context.Context, entry *domain.LogEntry) error {
	if entry == nil {
		return errors.New("log entry is nil")
	}

	ts := entry.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	var requestID any
	if entry.RequestID != "" {
		requestID = entry.RequestID
	}

	var input any
	if len(entry.Input) != 0 {
		input = []byte(entry.Input)
	}

	var output any
	if len(entry.Output) != 0 {
		output = []byte(entry.Output)
	}

	query := `
		INSERT INTO logs (ts, level, function_id, function_name, message, request_id, input, output, error, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := s.db.ExecContext(
		ctx,
		query,
		ts,
		entry.Level,
		entry.FunctionID,
		entry.FunctionName,
		entry.Message,
		requestID,
		input,
		output,
		entry.Error,
		entry.DurationMs,
	)
	if err != nil {
		return fmt.Errorf("failed to create log entry: %w", err)
	}
	return nil
}

// ListLogEntriesOptions 控制日志查询的过滤与分页。
type ListLogEntriesOptions struct {
	FunctionID   string
	FunctionName string
	RequestID    string
	Level        string
	Before       *time.Time
	After        *time.Time
	Limit        int
	Offset       int
}

// ListLogEntries 查询 logs 表中的日志记录。
// 结果按时间倒序返回（最新在前）。
func (s *PostgresStore) ListLogEntries(ctx context.Context, opts ListLogEntriesOptions) ([]*domain.LogEntry, error) {
	if opts.Limit <= 0 {
		opts.Limit = 200
	}
	if opts.Limit > 1000 {
		opts.Limit = 1000
	}
	if opts.Offset < 0 {
		opts.Offset = 0
	}

	args := make([]any, 0, 8)
	where := make([]string, 0, 6)

	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if opts.FunctionID != "" {
		where = append(where, "function_id = "+arg(opts.FunctionID))
	}
	if opts.FunctionName != "" {
		where = append(where, "function_name = "+arg(opts.FunctionName))
	}
	if opts.RequestID != "" {
		where = append(where, "request_id = "+arg(opts.RequestID))
	}
	if opts.Level != "" {
		where = append(where, "level = "+arg(opts.Level))
	}
	if opts.Before != nil {
		where = append(where, "ts < "+arg(*opts.Before))
	}
	if opts.After != nil {
		where = append(where, "ts > "+arg(*opts.After))
	}

	query := `
		SELECT ts, level, function_id, function_name, message, request_id, input, output, error, duration_ms
		FROM logs
	`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY ts DESC, id DESC LIMIT " + arg(opts.Limit) + " OFFSET " + arg(opts.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]*domain.LogEntry, 0, opts.Limit)
	for rows.Next() {
		entry := &domain.LogEntry{}
		var requestID sql.NullString
		var input, output []byte
		var errStr sql.NullString
		var duration sql.NullInt64
		if err := rows.Scan(
			&entry.Timestamp,
			&entry.Level,
			&entry.FunctionID,
			&entry.FunctionName,
			&entry.Message,
			&requestID,
			&input,
			&output,
			&errStr,
			&duration,
		); err != nil {
			return nil, err
		}
		if requestID.Valid {
			entry.RequestID = requestID.String
		}
		if input != nil {
			entry.Input = input
		}
		if output != nil {
			entry.Output = output
		}
		if errStr.Valid {
			entry.Error = errStr.String
		}
		if duration.Valid {
			entry.DurationMs = duration.Int64
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// FunctionBasicStats 函数基础统计（用于列表展示）
type FunctionBasicStats struct {
	FunctionID   string  `json:"function_id"`
	Invocations  int64   `json:"invocations"`
	SuccessRate  float64 `json:"success_rate"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	ErrorCount   int64   `json:"error_count"`
}

// GetAllFunctionsBasicStats 获取所有函数的基础统计（用于函数列表）
func (s *PostgresStore) GetAllFunctionsBasicStats(periodHours int) (map[string]*FunctionBasicStats, error) {
	query := `
		SELECT
			function_id,
			COUNT(*) as invocations,
			COUNT(*) FILTER (WHERE status = 'success' OR status = 'completed') as success,
			COUNT(*) FILTER (WHERE status = 'failed' OR status = 'timeout') as errors,
			COALESCE(AVG(duration_ms), 0) as avg_latency
		FROM invocations
		WHERE created_at >= NOW() - INTERVAL '1 hour' * $1
		GROUP BY function_id
	`
	rows, err := s.db.Query(query, periodHours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]*FunctionBasicStats)
	for rows.Next() {
		stats := &FunctionBasicStats{}
		var successCount int64
		err := rows.Scan(&stats.FunctionID, &stats.Invocations, &successCount, &stats.ErrorCount, &stats.AvgLatencyMs)
		if err != nil {
			return nil, err
		}
		if stats.Invocations > 0 {
			stats.SuccessRate = float64(successCount) / float64(stats.Invocations)
		}
		result[stats.FunctionID] = stats
	}
	return result, nil
}

// FunctionStats 函数统计数据
type FunctionStats struct {
	TotalInvocations int64   `json:"total_invocations"`
	SuccessCount     int64   `json:"success_count"`
	FailedCount      int64   `json:"failed_count"`
	SuccessRate      float64 `json:"success_rate"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	P50LatencyMs     float64 `json:"p50_latency_ms"`
	P95LatencyMs     float64 `json:"p95_latency_ms"`
	P99LatencyMs     float64 `json:"p99_latency_ms"`
	MinLatencyMs     float64 `json:"min_latency_ms"`
	MaxLatencyMs     float64 `json:"max_latency_ms"`
	ColdStartCount   int64   `json:"cold_start_count"`
	ColdStartRate    float64 `json:"cold_start_rate"`
	AvgColdStartMs   float64 `json:"avg_cold_start_ms"`
	TotalDurationMs  int64   `json:"total_duration_ms"`
	ErrorRate        float64 `json:"error_rate"`
	TimeoutCount     int64   `json:"timeout_count"`
}

// GetFunctionStats 获取单个函数的统计数据
func (s *PostgresStore) GetFunctionStats(functionID string, periodHours int) (*FunctionStats, error) {
	stats := &FunctionStats{}

	query := `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'success' OR status = 'completed') as success,
			COUNT(*) FILTER (WHERE status = 'failed') as failed,
			COUNT(*) FILTER (WHERE status = 'timeout') as timeout,
			COUNT(*) FILTER (WHERE cold_start = true) as cold_starts,
			COALESCE(AVG(duration_ms), 0) as avg_latency,
			COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY duration_ms), 0) as p50_latency,
			COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms), 0) as p95_latency,
			COALESCE(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY duration_ms), 0) as p99_latency,
			COALESCE(MIN(duration_ms), 0) as min_latency,
			COALESCE(MAX(duration_ms), 0) as max_latency,
			COALESCE(SUM(duration_ms), 0) as total_duration,
			COALESCE(AVG(duration_ms) FILTER (WHERE cold_start = true), 0) as avg_cold_start
		FROM invocations
		WHERE function_id = $1 AND created_at >= NOW() - INTERVAL '1 hour' * $2
	`
	err := s.db.QueryRow(query, functionID, periodHours).Scan(
		&stats.TotalInvocations,
		&stats.SuccessCount,
		&stats.FailedCount,
		&stats.TimeoutCount,
		&stats.ColdStartCount,
		&stats.AvgLatencyMs,
		&stats.P50LatencyMs,
		&stats.P95LatencyMs,
		&stats.P99LatencyMs,
		&stats.MinLatencyMs,
		&stats.MaxLatencyMs,
		&stats.TotalDurationMs,
		&stats.AvgColdStartMs,
	)
	if err != nil {
		return stats, nil
	}

	if stats.TotalInvocations > 0 {
		stats.SuccessRate = float64(stats.SuccessCount) / float64(stats.TotalInvocations) * 100
		stats.ErrorRate = float64(stats.FailedCount+stats.TimeoutCount) / float64(stats.TotalInvocations) * 100
		stats.ColdStartRate = float64(stats.ColdStartCount) / float64(stats.TotalInvocations) * 100
	}

	return stats, nil
}

// GetFunctionTrends 获取单个函数的趋势数据
func (s *PostgresStore) GetFunctionTrends(functionID string, periodHours int) ([]TrendDataPoint, error) {
	query := `
		SELECT
			date_trunc('hour', created_at) as hour,
			COUNT(*) as invocations,
			COUNT(*) FILTER (WHERE status = 'failed' OR status = 'timeout') as errors,
			COALESCE(AVG(duration_ms), 0) as avg_latency
		FROM invocations
		WHERE function_id = $1 AND created_at >= NOW() - INTERVAL '1 hour' * $2
		GROUP BY date_trunc('hour', created_at)
		ORDER BY hour ASC
	`
	rows, err := s.db.Query(query, functionID, periodHours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var trends []TrendDataPoint
	for rows.Next() {
		var t TrendDataPoint
		if err := rows.Scan(&t.Timestamp, &t.Invocations, &t.Errors, &t.AvgLatencyMs); err != nil {
			continue
		}
		trends = append(trends, t)
	}

	// 如果没有数据，生成空的时间点
	if len(trends) == 0 {
		now := time.Now().Truncate(time.Hour)
		for i := periodHours - 1; i >= 0; i-- {
			trends = append(trends, TrendDataPoint{
				Timestamp:    now.Add(-time.Duration(i) * time.Hour),
				Invocations:  0,
				Errors:       0,
				AvgLatencyMs: 0,
			})
		}
	}

	return trends, nil
}

// LatencyDistribution 延迟分布
type LatencyDistribution struct {
	Bucket string `json:"bucket"`
	Count  int64  `json:"count"`
}

// GetFunctionLatencyDistribution 获取函数延迟分布
func (s *PostgresStore) GetFunctionLatencyDistribution(functionID string, periodHours int) ([]LatencyDistribution, error) {
	query := `
		SELECT
			CASE
				WHEN duration_ms < 10 THEN '0-10ms'
				WHEN duration_ms < 50 THEN '10-50ms'
				WHEN duration_ms < 100 THEN '50-100ms'
				WHEN duration_ms < 200 THEN '100-200ms'
				WHEN duration_ms < 500 THEN '200-500ms'
				WHEN duration_ms < 1000 THEN '500ms-1s'
				WHEN duration_ms < 2000 THEN '1-2s'
				WHEN duration_ms < 5000 THEN '2-5s'
				ELSE '>5s'
			END as bucket,
			COUNT(*) as count
		FROM invocations
		WHERE function_id = $1 AND created_at >= NOW() - INTERVAL '1 hour' * $2
		GROUP BY bucket
		ORDER BY MIN(duration_ms)
	`
	rows, err := s.db.Query(query, functionID, periodHours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dist []LatencyDistribution
	for rows.Next() {
		var d LatencyDistribution
		if err := rows.Scan(&d.Bucket, &d.Count); err != nil {
			continue
		}
		dist = append(dist, d)
	}

	return dist, nil
}

// ==================== 函数版本管理方法 ====================

// CreateFunctionVersion 创建函数版本记录。
func (s *PostgresStore) CreateFunctionVersion(v *domain.FunctionVersion) error {
	if v.ID == "" {
		v.ID = uuid.New().String()
	}
	v.CreatedAt = time.Now()

	query := `
		INSERT INTO function_versions (id, function_id, version, handler, code, "binary", code_hash, description, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := s.db.Exec(query, v.ID, v.FunctionID, v.Version, v.Handler, v.Code, v.Binary, v.CodeHash, v.Description, v.CreatedAt)
	return err
}

// ListFunctionVersions 获取函数的所有版本。
func (s *PostgresStore) ListFunctionVersions(functionID string) ([]*domain.FunctionVersion, error) {
	query := `
		SELECT id, function_id, version, handler, code, "binary", code_hash, description, created_at
		FROM function_versions
		WHERE function_id = $1
		ORDER BY version DESC
	`
	rows, err := s.db.Query(query, functionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []*domain.FunctionVersion
	for rows.Next() {
		v := &domain.FunctionVersion{}
		var code, binary, description sql.NullString
		if err := rows.Scan(&v.ID, &v.FunctionID, &v.Version, &v.Handler, &code, &binary, &v.CodeHash, &description, &v.CreatedAt); err != nil {
			return nil, err
		}
		if code.Valid {
			v.Code = code.String
		}
		if binary.Valid {
			v.Binary = binary.String
		}
		if description.Valid {
			v.Description = description.String
		}
		versions = append(versions, v)
	}
	return versions, nil
}

// GetFunctionVersion 获取指定版本。
func (s *PostgresStore) GetFunctionVersion(functionID string, version int) (*domain.FunctionVersion, error) {
	query := `
		SELECT id, function_id, version, handler, code, "binary", code_hash, description, created_at
		FROM function_versions
		WHERE function_id = $1 AND version = $2
	`
	v := &domain.FunctionVersion{}
	var code, binary, description sql.NullString
	err := s.db.QueryRow(query, functionID, version).Scan(&v.ID, &v.FunctionID, &v.Version, &v.Handler, &code, &binary, &v.CodeHash, &description, &v.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, domain.ErrFunctionNotFound
	}
	if err != nil {
		return nil, err
	}
	if code.Valid {
		v.Code = code.String
	}
	if binary.Valid {
		v.Binary = binary.String
	}
	if description.Valid {
		v.Description = description.String
	}
	return v, nil
}

// GetLatestFunctionVersion 获取函数的最新版本号。
func (s *PostgresStore) GetLatestFunctionVersion(functionID string) (int, error) {
	var version int
	err := s.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM function_versions WHERE function_id = $1", functionID).Scan(&version)
	return version, err
}

// ==================== 函数别名管理方法 ====================

// CreateFunctionAlias 创建函数别名。
func (s *PostgresStore) CreateFunctionAlias(a *domain.FunctionAlias) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	a.CreatedAt = time.Now()
	a.UpdatedAt = a.CreatedAt

	routingJSON, _ := json.Marshal(a.RoutingConfig)
	query := `
		INSERT INTO function_aliases (id, function_id, name, description, routing_config, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := s.db.Exec(query, a.ID, a.FunctionID, a.Name, a.Description, routingJSON, a.CreatedAt, a.UpdatedAt)
	return err
}

// GetFunctionAlias 获取函数别名。
func (s *PostgresStore) GetFunctionAlias(functionID, name string) (*domain.FunctionAlias, error) {
	query := `
		SELECT id, function_id, name, description, routing_config, created_at, updated_at
		FROM function_aliases
		WHERE function_id = $1 AND name = $2
	`
	a := &domain.FunctionAlias{}
	var description sql.NullString
	var routingJSON []byte
	err := s.db.QueryRow(query, functionID, name).Scan(&a.ID, &a.FunctionID, &a.Name, &description, &routingJSON, &a.CreatedAt, &a.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, domain.ErrFunctionNotFound
	}
	if err != nil {
		return nil, err
	}
	if description.Valid {
		a.Description = description.String
	}
	json.Unmarshal(routingJSON, &a.RoutingConfig)
	return a, nil
}

// ListFunctionAliases 获取函数的所有别名。
func (s *PostgresStore) ListFunctionAliases(functionID string) ([]*domain.FunctionAlias, error) {
	query := `
		SELECT id, function_id, name, description, routing_config, created_at, updated_at
		FROM function_aliases
		WHERE function_id = $1
		ORDER BY name
	`
	rows, err := s.db.Query(query, functionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var aliases []*domain.FunctionAlias
	for rows.Next() {
		a := &domain.FunctionAlias{}
		var description sql.NullString
		var routingJSON []byte
		if err := rows.Scan(&a.ID, &a.FunctionID, &a.Name, &description, &routingJSON, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		if description.Valid {
			a.Description = description.String
		}
		json.Unmarshal(routingJSON, &a.RoutingConfig)
		aliases = append(aliases, a)
	}
	return aliases, nil
}

// UpdateFunctionAlias 更新函数别名。
func (s *PostgresStore) UpdateFunctionAlias(a *domain.FunctionAlias) error {
	a.UpdatedAt = time.Now()
	routingJSON, _ := json.Marshal(a.RoutingConfig)
	query := `
		UPDATE function_aliases SET description = $3, routing_config = $4, updated_at = $5
		WHERE function_id = $1 AND name = $2
	`
	result, err := s.db.Exec(query, a.FunctionID, a.Name, a.Description, routingJSON, a.UpdatedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if affected == 0 {
		return domain.ErrFunctionNotFound
	}
	return nil
}

// DeleteFunctionAlias 删除函数别名。
func (s *PostgresStore) DeleteFunctionAlias(functionID, name string) error {
	result, err := s.db.Exec("DELETE FROM function_aliases WHERE function_id = $1 AND name = $2", functionID, name)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if affected == 0 {
		return domain.ErrFunctionNotFound
	}
	return nil
}

// ==================== 函数层管理方法 ====================

// CreateLayer 创建层。
func (s *PostgresStore) CreateLayer(l *domain.Layer) error {
	if l.ID == "" {
		l.ID = uuid.New().String()
	}
	l.CreatedAt = time.Now()
	l.UpdatedAt = l.CreatedAt

	query := `
		INSERT INTO layers (id, name, description, compatible_runtimes, latest_version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := s.db.Exec(query, l.ID, l.Name, l.Description, pq.Array(l.CompatibleRuntimes), l.LatestVersion, l.CreatedAt, l.UpdatedAt)
	return err
}

// GetLayerByID 根据 ID 获取层。
func (s *PostgresStore) GetLayerByID(id string) (*domain.Layer, error) {
	query := `
		SELECT id, name, description, compatible_runtimes, latest_version, created_at, updated_at
		FROM layers WHERE id = $1
	`
	l := &domain.Layer{}
	var description sql.NullString
	err := s.db.QueryRow(query, id).Scan(&l.ID, &l.Name, &description, pq.Array(&l.CompatibleRuntimes), &l.LatestVersion, &l.CreatedAt, &l.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, errors.New("layer not found")
	}
	if err != nil {
		return nil, err
	}
	if description.Valid {
		l.Description = description.String
	}
	return l, nil
}

// GetLayerByName 根据名称获取层。
func (s *PostgresStore) GetLayerByName(name string) (*domain.Layer, error) {
	query := `
		SELECT id, name, description, compatible_runtimes, latest_version, created_at, updated_at
		FROM layers WHERE name = $1
	`
	l := &domain.Layer{}
	var description sql.NullString
	err := s.db.QueryRow(query, name).Scan(&l.ID, &l.Name, &description, pq.Array(&l.CompatibleRuntimes), &l.LatestVersion, &l.CreatedAt, &l.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, errors.New("layer not found")
	}
	if err != nil {
		return nil, err
	}
	if description.Valid {
		l.Description = description.String
	}
	return l, nil
}

// ListLayers 获取所有层。
func (s *PostgresStore) ListLayers(offset, limit int) ([]*domain.Layer, int, error) {
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM layers").Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	query := `
		SELECT id, name, description, compatible_runtimes, latest_version, created_at, updated_at
		FROM layers ORDER BY created_at DESC LIMIT $1 OFFSET $2
	`
	rows, err := s.db.Query(query, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var layers []*domain.Layer
	for rows.Next() {
		l := &domain.Layer{}
		var description sql.NullString
		if err := rows.Scan(&l.ID, &l.Name, &description, pq.Array(&l.CompatibleRuntimes), &l.LatestVersion, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, 0, err
		}
		if description.Valid {
			l.Description = description.String
		}
		layers = append(layers, l)
	}
	return layers, total, nil
}

// UpdateLayer 更新层。
func (s *PostgresStore) UpdateLayer(l *domain.Layer) error {
	l.UpdatedAt = time.Now()
	query := `
		UPDATE layers SET description = $2, compatible_runtimes = $3, latest_version = $4, updated_at = $5
		WHERE id = $1
	`
	result, err := s.db.Exec(query, l.ID, l.Description, pq.Array(l.CompatibleRuntimes), l.LatestVersion, l.UpdatedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if affected == 0 {
		return errors.New("layer not found")
	}
	return nil
}

// DeleteLayer 删除层。
func (s *PostgresStore) DeleteLayer(id string) error {
	result, err := s.db.Exec("DELETE FROM layers WHERE id = $1", id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if affected == 0 {
		return errors.New("layer not found")
	}
	return nil
}

// CreateLayerVersion 创建层版本。
func (s *PostgresStore) CreateLayerVersion(lv *domain.LayerVersion, content []byte) error {
	if lv.ID == "" {
		lv.ID = uuid.New().String()
	}
	lv.CreatedAt = time.Now()

	query := `
		INSERT INTO layer_versions (id, layer_id, version, content, content_hash, size_bytes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := s.db.Exec(query, lv.ID, lv.LayerID, lv.Version, content, lv.ContentHash, lv.SizeBytes, lv.CreatedAt)
	return err
}

// GetLayerVersion 获取层版本。
func (s *PostgresStore) GetLayerVersion(layerID string, version int) (*domain.LayerVersion, error) {
	query := `
		SELECT id, layer_id, version, content_hash, size_bytes, created_at
		FROM layer_versions
		WHERE layer_id = $1 AND version = $2
	`
	lv := &domain.LayerVersion{}
	err := s.db.QueryRow(query, layerID, version).Scan(&lv.ID, &lv.LayerID, &lv.Version, &lv.ContentHash, &lv.SizeBytes, &lv.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, errors.New("layer version not found")
	}
	return lv, err
}

// GetLayerVersionContent 获取层版本内容。
func (s *PostgresStore) GetLayerVersionContent(layerID string, version int) ([]byte, error) {
	var content []byte
	err := s.db.QueryRow("SELECT content FROM layer_versions WHERE layer_id = $1 AND version = $2", layerID, version).Scan(&content)
	if err == sql.ErrNoRows {
		return nil, errors.New("layer version not found")
	}
	return content, err
}

// ListLayerVersions 获取层的所有版本。
func (s *PostgresStore) ListLayerVersions(layerID string) ([]*domain.LayerVersion, error) {
	query := `
		SELECT id, layer_id, version, content_hash, size_bytes, created_at
		FROM layer_versions
		WHERE layer_id = $1
		ORDER BY version DESC
	`
	rows, err := s.db.Query(query, layerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []*domain.LayerVersion
	for rows.Next() {
		lv := &domain.LayerVersion{}
		if err := rows.Scan(&lv.ID, &lv.LayerID, &lv.Version, &lv.ContentHash, &lv.SizeBytes, &lv.CreatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, lv)
	}
	return versions, nil
}

// SetFunctionLayers 设置函数的层。
func (s *PostgresStore) SetFunctionLayers(functionID string, layers []domain.FunctionLayer) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 删除现有关联
	_, err = tx.Exec("DELETE FROM function_layers WHERE function_id = $1", functionID)
	if err != nil {
		return err
	}

	// 插入新关联
	for _, fl := range layers {
		_, err = tx.Exec(
			"INSERT INTO function_layers (function_id, layer_id, layer_version, order_index) VALUES ($1, $2, $3, $4)",
			functionID, fl.LayerID, fl.LayerVersion, fl.Order,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetFunctionLayers 获取函数的层。
func (s *PostgresStore) GetFunctionLayers(functionID string) ([]domain.FunctionLayer, error) {
	query := `
		SELECT fl.layer_id, l.name, fl.layer_version, fl.order_index
		FROM function_layers fl
		JOIN layers l ON fl.layer_id = l.id
		WHERE fl.function_id = $1
		ORDER BY fl.order_index
	`
	rows, err := s.db.Query(query, functionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var layers []domain.FunctionLayer
	for rows.Next() {
		fl := domain.FunctionLayer{}
		if err := rows.Scan(&fl.LayerID, &fl.LayerName, &fl.LayerVersion, &fl.Order); err != nil {
			return nil, err
		}
		layers = append(layers, fl)
	}
	return layers, nil
}

// ==================== 环境管理方法 ====================

// CreateEnvironment 创建环境。
func (s *PostgresStore) CreateEnvironment(e *domain.Environment) error {
	if e.ID == "" {
		e.ID = uuid.New().String()
	}
	e.CreatedAt = time.Now()

	// 如果设置为默认，先清除其他默认环境
	if e.IsDefault {
		_, err := s.db.Exec("UPDATE environments SET is_default = FALSE WHERE is_default = TRUE")
		if err != nil {
			return err
		}
	}

	query := `
		INSERT INTO environments (id, name, description, is_default, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := s.db.Exec(query, e.ID, e.Name, e.Description, e.IsDefault, e.CreatedAt)
	return err
}

// GetEnvironmentByID 根据 ID 获取环境。
func (s *PostgresStore) GetEnvironmentByID(id string) (*domain.Environment, error) {
	query := `SELECT id, name, description, is_default, created_at FROM environments WHERE id = $1`
	e := &domain.Environment{}
	var description sql.NullString
	err := s.db.QueryRow(query, id).Scan(&e.ID, &e.Name, &description, &e.IsDefault, &e.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, errors.New("environment not found")
	}
	if err != nil {
		return nil, err
	}
	if description.Valid {
		e.Description = description.String
	}
	return e, nil
}

// GetEnvironmentByName 根据名称获取环境。
func (s *PostgresStore) GetEnvironmentByName(name string) (*domain.Environment, error) {
	query := `SELECT id, name, description, is_default, created_at FROM environments WHERE name = $1`
	e := &domain.Environment{}
	var description sql.NullString
	err := s.db.QueryRow(query, name).Scan(&e.ID, &e.Name, &description, &e.IsDefault, &e.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, errors.New("environment not found")
	}
	if err != nil {
		return nil, err
	}
	if description.Valid {
		e.Description = description.String
	}
	return e, nil
}

// GetDefaultEnvironment 获取默认环境。
func (s *PostgresStore) GetDefaultEnvironment() (*domain.Environment, error) {
	query := `SELECT id, name, description, is_default, created_at FROM environments WHERE is_default = TRUE LIMIT 1`
	e := &domain.Environment{}
	var description sql.NullString
	err := s.db.QueryRow(query).Scan(&e.ID, &e.Name, &description, &e.IsDefault, &e.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, errors.New("default environment not found")
	}
	if err != nil {
		return nil, err
	}
	if description.Valid {
		e.Description = description.String
	}
	return e, nil
}

// ListEnvironments 获取所有环境。
func (s *PostgresStore) ListEnvironments() ([]*domain.Environment, error) {
	query := `SELECT id, name, description, is_default, created_at FROM environments ORDER BY created_at`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var envs []*domain.Environment
	for rows.Next() {
		e := &domain.Environment{}
		var description sql.NullString
		if err := rows.Scan(&e.ID, &e.Name, &description, &e.IsDefault, &e.CreatedAt); err != nil {
			return nil, err
		}
		if description.Valid {
			e.Description = description.String
		}
		envs = append(envs, e)
	}
	return envs, nil
}

// DeleteEnvironment 删除环境。
func (s *PostgresStore) DeleteEnvironment(id string) error {
	// 不允许删除默认环境
	var isDefault bool
	err := s.db.QueryRow("SELECT is_default FROM environments WHERE id = $1", id).Scan(&isDefault)
	if err == sql.ErrNoRows {
		return errors.New("environment not found")
	}
	if err != nil {
		return err
	}
	if isDefault {
		return errors.New("cannot delete default environment")
	}

	result, err := s.db.Exec("DELETE FROM environments WHERE id = $1", id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if affected == 0 {
		return errors.New("environment not found")
	}
	return nil
}

// GetFunctionEnvConfig 获取函数的环境配置。
func (s *PostgresStore) GetFunctionEnvConfig(functionID, environmentID string) (*domain.FunctionEnvConfig, error) {
	query := `
		SELECT fec.function_id, fec.environment_id, e.name, fec.env_vars, fec.memory_mb, fec.timeout_sec, fec.active_alias, fec.created_at, fec.updated_at
		FROM function_environment_configs fec
		JOIN environments e ON fec.environment_id = e.id
		WHERE fec.function_id = $1 AND fec.environment_id = $2
	`
	cfg := &domain.FunctionEnvConfig{}
	var envVarsJSON []byte
	var memoryMB, timeoutSec sql.NullInt32
	var activeAlias sql.NullString
	err := s.db.QueryRow(query, functionID, environmentID).Scan(
		&cfg.FunctionID, &cfg.EnvironmentID, &cfg.EnvironmentName, &envVarsJSON, &memoryMB, &timeoutSec, &activeAlias, &cfg.CreatedAt, &cfg.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, errors.New("config not found")
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal(envVarsJSON, &cfg.EnvVars)
	if memoryMB.Valid {
		val := int(memoryMB.Int32)
		cfg.MemoryMB = &val
	}
	if timeoutSec.Valid {
		val := int(timeoutSec.Int32)
		cfg.TimeoutSec = &val
	}
	if activeAlias.Valid {
		cfg.ActiveAlias = activeAlias.String
	}
	return cfg, nil
}

// ListFunctionEnvConfigs 获取函数在所有环境下的配置。
func (s *PostgresStore) ListFunctionEnvConfigs(functionID string) ([]*domain.FunctionEnvConfig, error) {
	query := `
		SELECT fec.function_id, fec.environment_id, e.name, fec.env_vars, fec.memory_mb, fec.timeout_sec, fec.active_alias, fec.created_at, fec.updated_at
		FROM function_environment_configs fec
		JOIN environments e ON fec.environment_id = e.id
		WHERE fec.function_id = $1
		ORDER BY e.name
	`
	rows, err := s.db.Query(query, functionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var configs []*domain.FunctionEnvConfig
	for rows.Next() {
		cfg := &domain.FunctionEnvConfig{}
		var envVarsJSON []byte
		var memoryMB, timeoutSec sql.NullInt32
		var activeAlias sql.NullString
		if err := rows.Scan(
			&cfg.FunctionID, &cfg.EnvironmentID, &cfg.EnvironmentName, &envVarsJSON, &memoryMB, &timeoutSec, &activeAlias, &cfg.CreatedAt, &cfg.UpdatedAt,
		); err != nil {
			return nil, err
		}
		json.Unmarshal(envVarsJSON, &cfg.EnvVars)
		if memoryMB.Valid {
			val := int(memoryMB.Int32)
			cfg.MemoryMB = &val
		}
		if timeoutSec.Valid {
			val := int(timeoutSec.Int32)
			cfg.TimeoutSec = &val
		}
		if activeAlias.Valid {
			cfg.ActiveAlias = activeAlias.String
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

// UpsertFunctionEnvConfig 创建或更新函数环境配置。
func (s *PostgresStore) UpsertFunctionEnvConfig(cfg *domain.FunctionEnvConfig) error {
	now := time.Now()
	envVarsJSON, _ := json.Marshal(cfg.EnvVars)

	query := `
		INSERT INTO function_environment_configs (function_id, environment_id, env_vars, memory_mb, timeout_sec, active_alias, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		ON CONFLICT (function_id, environment_id) DO UPDATE SET
			env_vars = EXCLUDED.env_vars,
			memory_mb = EXCLUDED.memory_mb,
			timeout_sec = EXCLUDED.timeout_sec,
			active_alias = EXCLUDED.active_alias,
			updated_at = EXCLUDED.updated_at
	`
	var memoryMB, timeoutSec any
	if cfg.MemoryMB != nil {
		memoryMB = *cfg.MemoryMB
	}
	if cfg.TimeoutSec != nil {
		timeoutSec = *cfg.TimeoutSec
	}
	var activeAlias any
	if cfg.ActiveAlias != "" {
		activeAlias = cfg.ActiveAlias
	}

	_, err := s.db.Exec(query, cfg.FunctionID, cfg.EnvironmentID, envVarsJSON, memoryMB, timeoutSec, activeAlias, now)
	return err
}

// ==================== 函数任务管理 ====================

// CreateFunctionTask 创建一个新的函数任务。
func (s *PostgresStore) CreateFunctionTask(task *domain.FunctionTask) error {
	if task.ID == "" {
		task.ID = uuid.New().String()
	}
	task.CreatedAt = time.Now()

	inputJSON, _ := json.Marshal(task.Input)

	query := `
		INSERT INTO function_tasks (id, function_id, type, status, input, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := s.db.Exec(query, task.ID, task.FunctionID, task.Type, task.Status, inputJSON, task.CreatedAt)
	return err
}

// GetFunctionTask 获取函数任务详情。
func (s *PostgresStore) GetFunctionTask(id string) (*domain.FunctionTask, error) {
	query := `
		SELECT id, function_id, type, status, input, output, error, created_at, started_at, completed_at
		FROM function_tasks WHERE id = $1
	`
	task := &domain.FunctionTask{}
	var input, output []byte
	var errorMsg sql.NullString
	var startedAt, completedAt sql.NullTime

	err := s.db.QueryRow(query, id).Scan(
		&task.ID, &task.FunctionID, &task.Type, &task.Status, &input, &output, &errorMsg,
		&task.CreatedAt, &startedAt, &completedAt,
	)
	if err == sql.ErrNoRows {
		return nil, errors.New("task not found")
	}
	if err != nil {
		return nil, err
	}

	task.Input = input
	task.Output = output
	if errorMsg.Valid {
		task.Error = errorMsg.String
	}
	if startedAt.Valid {
		task.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		task.CompletedAt = &completedAt.Time
	}
	return task, nil
}

// UpdateFunctionTask 更新函数任务状态。
func (s *PostgresStore) UpdateFunctionTask(task *domain.FunctionTask) error {
	outputJSON, _ := json.Marshal(task.Output)

	query := `
		UPDATE function_tasks SET status = $2, output = $3, error = $4, started_at = $5, completed_at = $6
		WHERE id = $1
	`
	_, err := s.db.Exec(query, task.ID, task.Status, outputJSON, task.Error, task.StartedAt, task.CompletedAt)
	return err
}

// GetPendingFunctionTasks 获取待处理的函数任务列表。
func (s *PostgresStore) GetPendingFunctionTasks(limit int) ([]*domain.FunctionTask, error) {
	query := `
		SELECT id, function_id, type, status, input, output, error, created_at, started_at, completed_at
		FROM function_tasks WHERE status = 'pending' ORDER BY created_at ASC LIMIT $1
	`
	rows, err := s.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*domain.FunctionTask
	for rows.Next() {
		task := &domain.FunctionTask{}
		var input, output []byte
		var errorMsg sql.NullString
		var startedAt, completedAt sql.NullTime

		err := rows.Scan(
			&task.ID, &task.FunctionID, &task.Type, &task.Status, &input, &output, &errorMsg,
			&task.CreatedAt, &startedAt, &completedAt,
		)
		if err != nil {
			return nil, err
		}

		task.Input = input
		task.Output = output
		if errorMsg.Valid {
			task.Error = errorMsg.String
		}
		if startedAt.Valid {
			task.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			task.CompletedAt = &completedAt.Time
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// UpdateFunctionStatus 更新函数状态（不递增版本号）。
func (s *PostgresStore) UpdateFunctionStatus(id string, status domain.FunctionStatus, statusMessage, taskID string) error {
	query := `UPDATE functions SET status = $2, status_message = $3, task_id = $4, updated_at = $5 WHERE id = $1`
	_, err := s.db.Exec(query, id, status, statusMessage, taskID, time.Now())
	return err
}

// SetFunctionDeployed 标记函数部署成功。
func (s *PostgresStore) SetFunctionDeployed(id string) error {
	now := time.Now()
	query := `UPDATE functions SET status = 'active', status_message = '', task_id = '', last_deployed_at = $2, updated_at = $2 WHERE id = $1`
	_, err := s.db.Exec(query, id, now)
	return err
}

// ==================== 死信队列 (DLQ) 存储方法 ====================

// CreateDLQMessage 创建死信消息。
func (s *PostgresStore) CreateDLQMessage(msg *domain.DeadLetterMessage) error {
	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}
	msg.CreatedAt = time.Now()

	query := `
		INSERT INTO dead_letter_queue (id, function_id, original_request_id, payload, error, retry_count, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := s.db.Exec(query, msg.ID, msg.FunctionID, msg.OriginalRequestID, msg.Payload, msg.Error, msg.RetryCount, msg.Status, msg.CreatedAt)
	return err
}

// GetDLQMessage 获取死信消息详情。
func (s *PostgresStore) GetDLQMessage(id string) (*domain.DeadLetterMessage, error) {
	query := `
		SELECT d.id, d.function_id, f.name, d.original_request_id, d.payload, d.error, d.retry_count, d.status, d.created_at, d.last_retry_at, d.resolved_at
		FROM dead_letter_queue d
		LEFT JOIN functions f ON d.function_id = f.id
		WHERE d.id = $1
	`
	row := s.db.QueryRow(query, id)

	msg := &domain.DeadLetterMessage{}
	var functionName sql.NullString
	var lastRetryAt, resolvedAt sql.NullTime

	err := row.Scan(&msg.ID, &msg.FunctionID, &functionName, &msg.OriginalRequestID, &msg.Payload, &msg.Error,
		&msg.RetryCount, &msg.Status, &msg.CreatedAt, &lastRetryAt, &resolvedAt)
	if err == sql.ErrNoRows {
		return nil, errors.New("dead letter message not found")
	}
	if err != nil {
		return nil, err
	}

	if functionName.Valid {
		msg.FunctionName = functionName.String
	}
	if lastRetryAt.Valid {
		msg.LastRetryAt = &lastRetryAt.Time
	}
	if resolvedAt.Valid {
		msg.ResolvedAt = &resolvedAt.Time
	}

	return msg, nil
}

// ListDLQMessages 分页查询死信消息列表。
func (s *PostgresStore) ListDLQMessages(functionID, status string, offset, limit int) ([]*domain.DeadLetterMessage, int, error) {
	// 构建查询条件
	conditions := []string{"1=1"}
	args := []interface{}{}
	argIndex := 1

	if functionID != "" {
		conditions = append(conditions, fmt.Sprintf("d.function_id = $%d", argIndex))
		args = append(args, functionID)
		argIndex++
	}
	if status != "" {
		conditions = append(conditions, fmt.Sprintf("d.status = $%d", argIndex))
		args = append(args, status)
		argIndex++
	}

	whereClause := strings.Join(conditions, " AND ")

	// 查询总数
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM dead_letter_queue d WHERE %s", whereClause)
	var total int
	err := s.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// 查询列表
	listQuery := fmt.Sprintf(`
		SELECT d.id, d.function_id, f.name, d.original_request_id, d.payload, d.error, d.retry_count, d.status, d.created_at, d.last_retry_at, d.resolved_at
		FROM dead_letter_queue d
		LEFT JOIN functions f ON d.function_id = f.id
		WHERE %s
		ORDER BY d.created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)
	args = append(args, limit, offset)

	rows, err := s.db.Query(listQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var messages []*domain.DeadLetterMessage
	for rows.Next() {
		msg := &domain.DeadLetterMessage{}
		var functionName sql.NullString
		var lastRetryAt, resolvedAt sql.NullTime

		err := rows.Scan(&msg.ID, &msg.FunctionID, &functionName, &msg.OriginalRequestID, &msg.Payload, &msg.Error,
			&msg.RetryCount, &msg.Status, &msg.CreatedAt, &lastRetryAt, &resolvedAt)
		if err != nil {
			return nil, 0, err
		}

		if functionName.Valid {
			msg.FunctionName = functionName.String
		}
		if lastRetryAt.Valid {
			msg.LastRetryAt = &lastRetryAt.Time
		}
		if resolvedAt.Valid {
			msg.ResolvedAt = &resolvedAt.Time
		}
		messages = append(messages, msg)
	}

	return messages, total, nil
}

// UpdateDLQMessage 更新死信消息。
func (s *PostgresStore) UpdateDLQMessage(msg *domain.DeadLetterMessage) error {
	query := `
		UPDATE dead_letter_queue
		SET retry_count = $2, status = $3, last_retry_at = $4, resolved_at = $5
		WHERE id = $1
	`
	_, err := s.db.Exec(query, msg.ID, msg.RetryCount, msg.Status, msg.LastRetryAt, msg.ResolvedAt)
	return err
}

// DeleteDLQMessage 删除死信消息。
func (s *PostgresStore) DeleteDLQMessage(id string) error {
	_, err := s.db.Exec("DELETE FROM dead_letter_queue WHERE id = $1", id)
	return err
}

// PurgeDLQMessages 清空指定函数的死信消息。
func (s *PostgresStore) PurgeDLQMessages(functionID string) (int64, error) {
	var result sql.Result
	var err error
	if functionID != "" {
		result, err = s.db.Exec("DELETE FROM dead_letter_queue WHERE function_id = $1", functionID)
	} else {
		result, err = s.db.Exec("DELETE FROM dead_letter_queue")
	}
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// CountDLQMessages 统计死信消息数量。
func (s *PostgresStore) CountDLQMessages(functionID string) (int, error) {
	var count int
	var err error
	if functionID != "" {
		err = s.db.QueryRow("SELECT COUNT(*) FROM dead_letter_queue WHERE function_id = $1", functionID).Scan(&count)
	} else {
		err = s.db.QueryRow("SELECT COUNT(*) FROM dead_letter_queue").Scan(&count)
	}
	return count, err
}

// ==================== 系统设置存储方法 ====================

// SystemSetting 系统设置项
type SystemSetting struct {
	Key         string    `json:"key"`
	Value       string    `json:"value"`
	Description string    `json:"description,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GetSystemSetting 获取系统设置。
func (s *PostgresStore) GetSystemSetting(key string) (*SystemSetting, error) {
	query := `SELECT key, value, description, updated_at FROM system_settings WHERE key = $1`
	row := s.db.QueryRow(query, key)

	setting := &SystemSetting{}
	var description sql.NullString
	err := row.Scan(&setting.Key, &setting.Value, &description, &setting.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, errors.New("setting not found")
	}
	if err != nil {
		return nil, err
	}
	if description.Valid {
		setting.Description = description.String
	}
	return setting, nil
}

// SetSystemSetting 设置系统设置。
func (s *PostgresStore) SetSystemSetting(key, value string) error {
	query := `
		INSERT INTO system_settings (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = $2, updated_at = NOW()
	`
	_, err := s.db.Exec(query, key, value)
	return err
}

// ListSystemSettings 获取所有系统设置。
func (s *PostgresStore) ListSystemSettings() ([]*SystemSetting, error) {
	query := `SELECT key, value, description, updated_at FROM system_settings ORDER BY key`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var settings []*SystemSetting
	for rows.Next() {
		setting := &SystemSetting{}
		var description sql.NullString
		if err := rows.Scan(&setting.Key, &setting.Value, &description, &setting.UpdatedAt); err != nil {
			return nil, err
		}
		if description.Valid {
			setting.Description = description.String
		}
		settings = append(settings, setting)
	}
	return settings, nil
}

// ==================== 数据清理方法 ====================

// CleanupOldInvocations 清理超过指定天数的调用记录。
func (s *PostgresStore) CleanupOldInvocations(retentionDays int) (int64, error) {
	query := `DELETE FROM invocations WHERE created_at < NOW() - INTERVAL '1 day' * $1`
	result, err := s.db.Exec(query, retentionDays)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// CleanupOldDLQMessages 清理超过指定天数的死信消息（仅清理已解决或已丢弃的）。
func (s *PostgresStore) CleanupOldDLQMessages(retentionDays int) (int64, error) {
	query := `DELETE FROM dead_letter_queue WHERE created_at < NOW() - INTERVAL '1 day' * $1 AND status IN ('resolved', 'discarded')`
	result, err := s.db.Exec(query, retentionDays)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// CleanupOldTasks 清理超过指定天数的任务记录（仅清理已完成或已失败的）。
func (s *PostgresStore) CleanupOldTasks(retentionDays int) (int64, error) {
	query := `DELETE FROM function_tasks WHERE created_at < NOW() - INTERVAL '1 day' * $1 AND status IN ('completed', 'failed')`
	result, err := s.db.Exec(query, retentionDays)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// RetentionStats 保留策略统计信息
type RetentionStats struct {
	TotalInvocations    int64 `json:"total_invocations"`
	OldInvocations      int64 `json:"old_invocations"`
	TotalDLQMessages    int64 `json:"total_dlq_messages"`
	OldDLQMessages      int64 `json:"old_dlq_messages"`
	TotalTasks          int64 `json:"total_tasks"`
	OldTasks            int64 `json:"old_tasks"`
	LogRetentionDays    int   `json:"log_retention_days"`
	DLQRetentionDays    int   `json:"dlq_retention_days"`
}

// GetRetentionStats 获取保留策略统计信息。
func (s *PostgresStore) GetRetentionStats(logRetentionDays, dlqRetentionDays int) (*RetentionStats, error) {
	stats := &RetentionStats{
		LogRetentionDays: logRetentionDays,
		DLQRetentionDays: dlqRetentionDays,
	}

	// 总调用记录数
	s.db.QueryRow("SELECT COUNT(*) FROM invocations").Scan(&stats.TotalInvocations)

	// 超期调用记录数
	s.db.QueryRow("SELECT COUNT(*) FROM invocations WHERE created_at < NOW() - INTERVAL '1 day' * $1", logRetentionDays).Scan(&stats.OldInvocations)

	// 总死信消息数
	s.db.QueryRow("SELECT COUNT(*) FROM dead_letter_queue").Scan(&stats.TotalDLQMessages)

	// 超期死信消息数
	s.db.QueryRow("SELECT COUNT(*) FROM dead_letter_queue WHERE created_at < NOW() - INTERVAL '1 day' * $1 AND status IN ('resolved', 'discarded')", dlqRetentionDays).Scan(&stats.OldDLQMessages)

	// 总任务数
	s.db.QueryRow("SELECT COUNT(*) FROM function_tasks").Scan(&stats.TotalTasks)

	// 超期任务数
	s.db.QueryRow("SELECT COUNT(*) FROM function_tasks WHERE created_at < NOW() - INTERVAL '1 day' * $1 AND status IN ('completed', 'failed')", logRetentionDays).Scan(&stats.OldTasks)

	return stats, nil
}

// ==================== 审计日志存储方法 ====================

// AuditLog 审计日志项
type AuditLog struct {
	ID           string                 `json:"id"`
	Action       string                 `json:"action"`       // 操作类型 (create/update/delete/invoke/etc)
	ResourceType string                 `json:"resource_type"` // 资源类型 (function/invocation/setting/etc)
	ResourceID   string                 `json:"resource_id,omitempty"`
	ResourceName string                 `json:"resource_name,omitempty"`
	Actor        string                 `json:"actor,omitempty"`   // 操作者 (用户名/API密钥/system)
	ActorIP      string                 `json:"actor_ip,omitempty"`
	Details      map[string]interface{} `json:"details,omitempty"` // 操作详情
	CreatedAt    time.Time              `json:"created_at"`
}

// CreateAuditLog 创建审计日志。
func (s *PostgresStore) CreateAuditLog(log *AuditLog) error {
	if log.ID == "" {
		log.ID = uuid.New().String()
	}
	log.CreatedAt = time.Now()

	detailsJSON, _ := json.Marshal(log.Details)

	query := `
		INSERT INTO audit_logs (id, action, resource_type, resource_id, resource_name, actor, actor_ip, details, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := s.db.Exec(query, log.ID, log.Action, log.ResourceType, log.ResourceID, log.ResourceName, log.Actor, log.ActorIP, detailsJSON, log.CreatedAt)
	return err
}

// ListAuditLogs 分页查询审计日志。
func (s *PostgresStore) ListAuditLogs(action, resourceType, resourceID string, offset, limit int) ([]*AuditLog, int, error) {
	// 构建查询条件
	conditions := []string{"1=1"}
	args := []interface{}{}
	argIndex := 1

	if action != "" {
		conditions = append(conditions, fmt.Sprintf("action = $%d", argIndex))
		args = append(args, action)
		argIndex++
	}
	if resourceType != "" {
		conditions = append(conditions, fmt.Sprintf("resource_type = $%d", argIndex))
		args = append(args, resourceType)
		argIndex++
	}
	if resourceID != "" {
		conditions = append(conditions, fmt.Sprintf("resource_id = $%d", argIndex))
		args = append(args, resourceID)
		argIndex++
	}

	whereClause := strings.Join(conditions, " AND ")

	// 查询总数
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM audit_logs WHERE %s", whereClause)
	var total int
	err := s.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// 查询列表
	listQuery := fmt.Sprintf(`
		SELECT id, action, resource_type, resource_id, resource_name, actor, actor_ip, details, created_at
		FROM audit_logs
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)
	args = append(args, limit, offset)

	rows, err := s.db.Query(listQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var logs []*AuditLog
	for rows.Next() {
		log := &AuditLog{}
		var resourceID, resourceName, actor, actorIP sql.NullString
		var details []byte

		err := rows.Scan(&log.ID, &log.Action, &log.ResourceType, &resourceID, &resourceName, &actor, &actorIP, &details, &log.CreatedAt)
		if err != nil {
			return nil, 0, err
		}

		if resourceID.Valid {
			log.ResourceID = resourceID.String
		}
		if resourceName.Valid {
			log.ResourceName = resourceName.String
		}
		if actor.Valid {
			log.Actor = actor.String
		}
		if actorIP.Valid {
			log.ActorIP = actorIP.String
		}
		if len(details) > 0 {
			json.Unmarshal(details, &log.Details)
		}
		logs = append(logs, log)
	}

	return logs, total, nil
}

// CleanupOldAuditLogs 清理超过指定天数的审计日志。
func (s *PostgresStore) CleanupOldAuditLogs(retentionDays int) (int64, error) {
	query := `DELETE FROM audit_logs WHERE created_at < NOW() - INTERVAL '1 day' * $1`
	result, err := s.db.Exec(query, retentionDays)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ==================== 配额管理存储方法 ====================

// QuotaUsage 配额使用情况
type QuotaUsage struct {
	FunctionCount       int   `json:"function_count"`
	TotalMemoryMB       int   `json:"total_memory_mb"`
	TodayInvocations    int64 `json:"today_invocations"`
	TotalCodeSizeKB     int64 `json:"total_code_size_kb"`
	MaxFunctions        int   `json:"max_functions"`
	MaxMemoryMB         int   `json:"max_memory_mb"`
	MaxInvocationsPerDay int  `json:"max_invocations_per_day"`
	MaxCodeSizeKB       int   `json:"max_code_size_kb"`
}

// GetQuotaUsage 获取当前配额使用情况。
func (s *PostgresStore) GetQuotaUsage() (*QuotaUsage, error) {
	usage := &QuotaUsage{
		// 默认限制值
		MaxFunctions:         100,
		MaxMemoryMB:          10240,
		MaxInvocationsPerDay: 100000,
		MaxCodeSizeKB:        5120,
	}

	// 获取配额设置
	if setting, err := s.GetSystemSetting("quota_max_functions"); err == nil {
		if v, err := strconv.Atoi(setting.Value); err == nil {
			usage.MaxFunctions = v
		}
	}
	if setting, err := s.GetSystemSetting("quota_max_memory_mb"); err == nil {
		if v, err := strconv.Atoi(setting.Value); err == nil {
			usage.MaxMemoryMB = v
		}
	}
	if setting, err := s.GetSystemSetting("quota_max_invocations_per_day"); err == nil {
		if v, err := strconv.Atoi(setting.Value); err == nil {
			usage.MaxInvocationsPerDay = v
		}
	}
	if setting, err := s.GetSystemSetting("quota_max_code_size_kb"); err == nil {
		if v, err := strconv.Atoi(setting.Value); err == nil {
			usage.MaxCodeSizeKB = v
		}
	}

	// 函数数量
	s.db.QueryRow("SELECT COUNT(*) FROM functions").Scan(&usage.FunctionCount)

	// 总内存
	s.db.QueryRow("SELECT COALESCE(SUM(memory_mb), 0) FROM functions").Scan(&usage.TotalMemoryMB)

	// 今日调用次数
	s.db.QueryRow("SELECT COUNT(*) FROM invocations WHERE created_at >= CURRENT_DATE").Scan(&usage.TodayInvocations)

	// 总代码大小
	s.db.QueryRow("SELECT COALESCE(SUM(LENGTH(code)), 0) / 1024 FROM functions").Scan(&usage.TotalCodeSizeKB)

	return usage, nil
}

// CheckQuota 检查是否超出配额。
// 返回 nil 表示配额正常，返回 error 表示超出配额。
func (s *PostgresStore) CheckQuota(additionalFunctions, additionalMemoryMB int, additionalCodeSizeKB int64) error {
	usage, err := s.GetQuotaUsage()
	if err != nil {
		return err
	}

	// 检查函数数量
	if usage.FunctionCount+additionalFunctions > usage.MaxFunctions {
		return fmt.Errorf("quota exceeded: max functions (%d/%d)", usage.FunctionCount+additionalFunctions, usage.MaxFunctions)
	}

	// 检查内存
	if usage.TotalMemoryMB+additionalMemoryMB > usage.MaxMemoryMB {
		return fmt.Errorf("quota exceeded: max memory (%d/%d MB)", usage.TotalMemoryMB+additionalMemoryMB, usage.MaxMemoryMB)
	}

	// 检查代码大小
	if usage.TotalCodeSizeKB+additionalCodeSizeKB > int64(usage.MaxCodeSizeKB) {
		return fmt.Errorf("quota exceeded: max code size (%d/%d KB)", usage.TotalCodeSizeKB+additionalCodeSizeKB, usage.MaxCodeSizeKB)
	}

	return nil
}

// CheckInvocationQuota 检查调用配额。
func (s *PostgresStore) CheckInvocationQuota() error {
	usage, err := s.GetQuotaUsage()
	if err != nil {
		return err
	}

	if usage.TodayInvocations >= int64(usage.MaxInvocationsPerDay) {
		return fmt.Errorf("quota exceeded: max invocations per day (%d/%d)", usage.TodayInvocations, usage.MaxInvocationsPerDay)
	}

	return nil
}

// ==================== 工作流存储方法 ====================

// CreateWorkflow 创建一个新的工作流。
func (s *PostgresStore) CreateWorkflow(workflow *domain.Workflow) error {
	if workflow.ID == "" {
		workflow.ID = uuid.New().String()
	}
	workflow.CreatedAt = time.Now()
	workflow.UpdatedAt = workflow.CreatedAt

	definitionJSON, err := json.Marshal(workflow.Definition)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow definition: %w", err)
	}

	query := `
		INSERT INTO workflows (id, name, description, version, status, definition, timeout_sec, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err = s.db.Exec(query,
		workflow.ID, workflow.Name, workflow.Description, workflow.Version, workflow.Status,
		definitionJSON, workflow.TimeoutSec, workflow.CreatedAt, workflow.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create workflow: %w", err)
	}
	return nil
}

// GetWorkflowByID 根据 ID 获取工作流。
func (s *PostgresStore) GetWorkflowByID(id string) (*domain.Workflow, error) {
	query := `
		SELECT id, name, description, version, status, definition, timeout_sec, created_at, updated_at
		FROM workflows WHERE id = $1
	`
	return s.scanWorkflow(s.db.QueryRow(query, id))
}

// GetWorkflowByName 根据名称获取工作流。
func (s *PostgresStore) GetWorkflowByName(name string) (*domain.Workflow, error) {
	query := `
		SELECT id, name, description, version, status, definition, timeout_sec, created_at, updated_at
		FROM workflows WHERE name = $1
	`
	return s.scanWorkflow(s.db.QueryRow(query, name))
}

// scanWorkflow 扫描工作流行
func (s *PostgresStore) scanWorkflow(row *sql.Row) (*domain.Workflow, error) {
	workflow := &domain.Workflow{}
	var description sql.NullString
	var definitionJSON []byte

	err := row.Scan(
		&workflow.ID, &workflow.Name, &description, &workflow.Version, &workflow.Status,
		&definitionJSON, &workflow.TimeoutSec, &workflow.CreatedAt, &workflow.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrWorkflowNotFound
		}
		return nil, err
	}

	if description.Valid {
		workflow.Description = description.String
	}
	if len(definitionJSON) > 0 {
		if err := json.Unmarshal(definitionJSON, &workflow.Definition); err != nil {
			return nil, fmt.Errorf("failed to unmarshal workflow definition: %w", err)
		}
	}

	return workflow, nil
}

// ListWorkflows 分页查询工作流列表。
func (s *PostgresStore) ListWorkflows(offset, limit int) ([]*domain.Workflow, int, error) {
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM workflows").Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	query := `
		SELECT id, name, description, version, status, definition, timeout_sec, created_at, updated_at
		FROM workflows ORDER BY created_at DESC LIMIT $1 OFFSET $2
	`
	rows, err := s.db.Query(query, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var workflows []*domain.Workflow
	for rows.Next() {
		workflow := &domain.Workflow{}
		var description sql.NullString
		var definitionJSON []byte

		err := rows.Scan(
			&workflow.ID, &workflow.Name, &description, &workflow.Version, &workflow.Status,
			&definitionJSON, &workflow.TimeoutSec, &workflow.CreatedAt, &workflow.UpdatedAt,
		)
		if err != nil {
			return nil, 0, err
		}

		if description.Valid {
			workflow.Description = description.String
		}
		if len(definitionJSON) > 0 {
			json.Unmarshal(definitionJSON, &workflow.Definition)
		}
		workflows = append(workflows, workflow)
	}

	return workflows, total, nil
}

// UpdateWorkflow 更新工作流。
func (s *PostgresStore) UpdateWorkflow(workflow *domain.Workflow) error {
	workflow.UpdatedAt = time.Now()

	definitionJSON, err := json.Marshal(workflow.Definition)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow definition: %w", err)
	}

	query := `
		UPDATE workflows
		SET name = $2, description = $3, version = $4, status = $5, definition = $6, timeout_sec = $7, updated_at = $8
		WHERE id = $1
	`
	result, err := s.db.Exec(query,
		workflow.ID, workflow.Name, workflow.Description, workflow.Version, workflow.Status,
		definitionJSON, workflow.TimeoutSec, workflow.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to update workflow: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return domain.ErrWorkflowNotFound
	}
	return nil
}

// DeleteWorkflow 删除工作流。
func (s *PostgresStore) DeleteWorkflow(id string) error {
	result, err := s.db.Exec("DELETE FROM workflows WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to delete workflow: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return domain.ErrWorkflowNotFound
	}
	return nil
}

// ==================== 工作流执行存储方法 ====================

// CreateExecution 创建一个新的执行实例。
func (s *PostgresStore) CreateExecution(exec *domain.WorkflowExecution) error {
	if exec.ID == "" {
		exec.ID = uuid.New().String()
	}
	exec.CreatedAt = time.Now()
	exec.UpdatedAt = exec.CreatedAt

	// 确保 JSON 字段不为 nil，PostgreSQL JSONB 列需要有效的 JSON
	input := exec.Input
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	output := exec.Output
	if len(output) == 0 {
		output = json.RawMessage("{}")
	}

	query := `
		INSERT INTO workflow_executions (id, workflow_id, workflow_name, workflow_version, workflow_definition, status, input, output, error, error_code, current_state, started_at, completed_at, timeout_at, paused_at_state, paused_input, paused_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
	`
	definition := exec.WorkflowDefinition
	if len(definition) == 0 {
		definition = json.RawMessage("{}")
	}
	// Convert []byte to string for JSONB columns (pq driver requires string for JSONB)
	var pausedInputStr *string
	if len(exec.PausedInput) > 0 {
		s := string(exec.PausedInput)
		pausedInputStr = &s
	}

	_, err := s.db.Exec(query,
		exec.ID, exec.WorkflowID, exec.WorkflowName, exec.WorkflowVersion, string(definition), exec.Status,
		string(input), string(output), exec.Error, exec.ErrorCode, exec.CurrentState,
		exec.StartedAt, exec.CompletedAt, exec.TimeoutAt,
		sql.NullString{String: exec.PausedAtState, Valid: exec.PausedAtState != ""},
		pausedInputStr, exec.PausedAt,
		exec.CreatedAt, exec.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create execution: %w", err)
	}
	return nil
}

// GetExecutionByID 根据 ID 获取执行实例。
func (s *PostgresStore) GetExecutionByID(id string) (*domain.WorkflowExecution, error) {
	query := `
		SELECT id, workflow_id, workflow_name, workflow_version, workflow_definition, status, input, output, error, error_code, current_state, started_at, completed_at, timeout_at, paused_at_state, paused_input, paused_at, created_at, updated_at
		FROM workflow_executions WHERE id = $1
	`
	return s.scanExecution(s.db.QueryRow(query, id))
}

// scanExecution 扫描执行实例行
func (s *PostgresStore) scanExecution(row *sql.Row) (*domain.WorkflowExecution, error) {
	exec := &domain.WorkflowExecution{}
	var input, output, definition, pausedInput []byte
	var errorMsg, errorCode, currentState, pausedAtState sql.NullString
	var startedAt, completedAt, timeoutAt, pausedAt sql.NullTime

	err := row.Scan(
		&exec.ID, &exec.WorkflowID, &exec.WorkflowName, &exec.WorkflowVersion, &definition, &exec.Status,
		&input, &output, &errorMsg, &errorCode, &currentState,
		&startedAt, &completedAt, &timeoutAt,
		&pausedAtState, &pausedInput, &pausedAt,
		&exec.CreatedAt, &exec.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrExecutionNotFound
		}
		return nil, err
	}

	exec.Input = input
	exec.Output = output
	exec.WorkflowDefinition = definition
	exec.PausedInput = pausedInput
	if errorMsg.Valid {
		exec.Error = errorMsg.String
	}
	if errorCode.Valid {
		exec.ErrorCode = errorCode.String
	}
	if currentState.Valid {
		exec.CurrentState = currentState.String
	}
	if pausedAtState.Valid {
		exec.PausedAtState = pausedAtState.String
	}
	if startedAt.Valid {
		exec.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		exec.CompletedAt = &completedAt.Time
	}
	if timeoutAt.Valid {
		exec.TimeoutAt = &timeoutAt.Time
	}
	if pausedAt.Valid {
		exec.PausedAt = &pausedAt.Time
	}

	return exec, nil
}

// ListExecutions 根据工作流 ID 分页查询执行实例列表。
func (s *PostgresStore) ListExecutions(workflowID string, offset, limit int) ([]*domain.WorkflowExecution, int, error) {
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM workflow_executions WHERE workflow_id = $1", workflowID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	query := `
		SELECT id, workflow_id, workflow_name, workflow_version, workflow_definition, status, input, output, error, error_code, current_state, started_at, completed_at, timeout_at, paused_at_state, paused_input, paused_at, created_at, updated_at
		FROM workflow_executions
		WHERE workflow_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`
	rows, err := s.db.Query(query, workflowID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	return s.scanExecutions(rows, total)
}

// ListAllExecutions 分页查询所有执行实例列表。
func (s *PostgresStore) ListAllExecutions(offset, limit int) ([]*domain.WorkflowExecution, int, error) {
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM workflow_executions").Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	query := `
		SELECT id, workflow_id, workflow_name, workflow_version, workflow_definition, status, input, output, error, error_code, current_state, started_at, completed_at, timeout_at, paused_at_state, paused_input, paused_at, created_at, updated_at
		FROM workflow_executions
		ORDER BY created_at DESC LIMIT $1 OFFSET $2
	`
	rows, err := s.db.Query(query, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	return s.scanExecutions(rows, total)
}

// scanExecutions 扫描多个执行实例行
func (s *PostgresStore) scanExecutions(rows *sql.Rows, total int) ([]*domain.WorkflowExecution, int, error) {
	var executions []*domain.WorkflowExecution
	for rows.Next() {
		exec := &domain.WorkflowExecution{}
		var input, output, definition, pausedInput []byte
		var errorMsg, errorCode, currentState, pausedAtState sql.NullString
		var startedAt, completedAt, timeoutAt, pausedAt sql.NullTime

		err := rows.Scan(
			&exec.ID, &exec.WorkflowID, &exec.WorkflowName, &exec.WorkflowVersion, &definition, &exec.Status,
			&input, &output, &errorMsg, &errorCode, &currentState,
			&startedAt, &completedAt, &timeoutAt,
			&pausedAtState, &pausedInput, &pausedAt,
			&exec.CreatedAt, &exec.UpdatedAt,
		)
		if err != nil {
			return nil, 0, err
		}

		exec.Input = input
		exec.Output = output
		exec.WorkflowDefinition = definition
		exec.PausedInput = pausedInput
		if errorMsg.Valid {
			exec.Error = errorMsg.String
		}
		if errorCode.Valid {
			exec.ErrorCode = errorCode.String
		}
		if currentState.Valid {
			exec.CurrentState = currentState.String
		}
		if pausedAtState.Valid {
			exec.PausedAtState = pausedAtState.String
		}
		if startedAt.Valid {
			exec.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			exec.CompletedAt = &completedAt.Time
		}
		if timeoutAt.Valid {
			exec.TimeoutAt = &timeoutAt.Time
		}
		if pausedAt.Valid {
			exec.PausedAt = &pausedAt.Time
		}
		executions = append(executions, exec)
	}

	return executions, total, nil
}

// UpdateExecution 更新执行实例。
func (s *PostgresStore) UpdateExecution(exec *domain.WorkflowExecution) error {
	exec.UpdatedAt = time.Now()

	// 确保 JSON 字段不为 nil，PostgreSQL JSONB 列需要有效的 JSON
	input := exec.Input
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	output := exec.Output
	if len(output) == 0 {
		output = json.RawMessage("{}")
	}

	query := `
		UPDATE workflow_executions
		SET status = $2, input = $3, output = $4, error = $5, error_code = $6, current_state = $7, started_at = $8, completed_at = $9, timeout_at = $10, paused_at_state = $11, paused_input = $12, paused_at = $13, updated_at = $14
		WHERE id = $1
	`
	// Convert []byte to string for JSONB columns (pq driver requires string for JSONB)
	var pausedInputStr *string
	if len(exec.PausedInput) > 0 {
		s := string(exec.PausedInput)
		pausedInputStr = &s
	}
	result, err := s.db.Exec(query,
		exec.ID, exec.Status, string(input), string(output), exec.Error, exec.ErrorCode, exec.CurrentState,
		exec.StartedAt, exec.CompletedAt, exec.TimeoutAt,
		sql.NullString{String: exec.PausedAtState, Valid: exec.PausedAtState != ""},
		pausedInputStr, exec.PausedAt,
		exec.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to update execution: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return domain.ErrExecutionNotFound
	}
	return nil
}

// ListPendingExecutions 列出待处理的执行实例（用于恢复）。
func (s *PostgresStore) ListPendingExecutions(limit int) ([]*domain.WorkflowExecution, error) {
	query := `
		SELECT id, workflow_id, workflow_name, workflow_version, workflow_definition, status, input, output, error, error_code, current_state, started_at, completed_at, timeout_at, paused_at_state, paused_input, paused_at, created_at, updated_at
		FROM workflow_executions
		WHERE status IN ('pending', 'running')
		ORDER BY created_at ASC LIMIT $1
	`
	rows, err := s.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	executions, _, err := s.scanExecutions(rows, 0)
	return executions, err
}

// ==================== 状态执行存储方法 ====================

// CreateStateExecution 创建状态执行记录。
func (s *PostgresStore) CreateStateExecution(stateExec *domain.StateExecution) error {
	if stateExec.ID == "" {
		stateExec.ID = uuid.New().String()
	}
	stateExec.CreatedAt = time.Now()

	// 确保 JSON 字段不为 nil，PostgreSQL JSONB 列需要有效的 JSON
	input := stateExec.Input
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	output := stateExec.Output
	if len(output) == 0 {
		output = json.RawMessage("{}")
	}

	query := `
		INSERT INTO state_executions (id, execution_id, state_name, state_type, status, input, output, error, error_code, retry_count, invocation_id, started_at, completed_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`
	_, err := s.db.Exec(query,
		stateExec.ID, stateExec.ExecutionID, stateExec.StateName, stateExec.StateType, stateExec.Status,
		input, output, stateExec.Error, stateExec.ErrorCode,
		stateExec.RetryCount, stateExec.InvocationID, stateExec.StartedAt, stateExec.CompletedAt, stateExec.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create state execution: %w", err)
	}
	return nil
}

// GetStateExecutionByID 根据 ID 获取状态执行记录。
func (s *PostgresStore) GetStateExecutionByID(id string) (*domain.StateExecution, error) {
	query := `
		SELECT id, execution_id, state_name, state_type, status, input, output, error, error_code, retry_count, invocation_id, started_at, completed_at, created_at
		FROM state_executions WHERE id = $1
	`
	stateExec := &domain.StateExecution{}
	var input, output []byte
	var errorMsg, errorCode, invocationID sql.NullString
	var startedAt, completedAt sql.NullTime

	err := s.db.QueryRow(query, id).Scan(
		&stateExec.ID, &stateExec.ExecutionID, &stateExec.StateName, &stateExec.StateType, &stateExec.Status,
		&input, &output, &errorMsg, &errorCode,
		&stateExec.RetryCount, &invocationID, &startedAt, &completedAt, &stateExec.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("state execution not found")
		}
		return nil, err
	}

	stateExec.Input = input
	stateExec.Output = output
	if errorMsg.Valid {
		stateExec.Error = errorMsg.String
	}
	if errorCode.Valid {
		stateExec.ErrorCode = errorCode.String
	}
	if invocationID.Valid {
		stateExec.InvocationID = invocationID.String
	}
	if startedAt.Valid {
		stateExec.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		stateExec.CompletedAt = &completedAt.Time
	}

	return stateExec, nil
}

// ListStateExecutions 列出执行的状态执行历史。
func (s *PostgresStore) ListStateExecutions(executionID string) ([]*domain.StateExecution, error) {
	query := `
		SELECT id, execution_id, state_name, state_type, status, input, output, error, error_code, retry_count, invocation_id, started_at, completed_at, created_at
		FROM state_executions
		WHERE execution_id = $1
		ORDER BY created_at ASC
	`
	rows, err := s.db.Query(query, executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stateExecutions []*domain.StateExecution
	for rows.Next() {
		stateExec := &domain.StateExecution{}
		var input, output []byte
		var errorMsg, errorCode, invocationID sql.NullString
		var startedAt, completedAt sql.NullTime

		err := rows.Scan(
			&stateExec.ID, &stateExec.ExecutionID, &stateExec.StateName, &stateExec.StateType, &stateExec.Status,
			&input, &output, &errorMsg, &errorCode,
			&stateExec.RetryCount, &invocationID, &startedAt, &completedAt, &stateExec.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

		stateExec.Input = input
		stateExec.Output = output
		if errorMsg.Valid {
			stateExec.Error = errorMsg.String
		}
		if errorCode.Valid {
			stateExec.ErrorCode = errorCode.String
		}
		if invocationID.Valid {
			stateExec.InvocationID = invocationID.String
		}
		if startedAt.Valid {
			stateExec.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			stateExec.CompletedAt = &completedAt.Time
		}
		stateExecutions = append(stateExecutions, stateExec)
	}

	return stateExecutions, nil
}

// UpdateStateExecution 更新状态执行记录。
func (s *PostgresStore) UpdateStateExecution(stateExec *domain.StateExecution) error {
	// 确保 JSON 字段不为 nil，PostgreSQL JSONB 列需要有效的 JSON
	input := stateExec.Input
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	output := stateExec.Output
	if len(output) == 0 {
		output = json.RawMessage("{}")
	}

	query := `
		UPDATE state_executions
		SET status = $2, input = $3, output = $4, error = $5, error_code = $6, retry_count = $7, invocation_id = $8, started_at = $9, completed_at = $10
		WHERE id = $1
	`
	result, err := s.db.Exec(query,
		stateExec.ID, stateExec.Status, input, output, stateExec.Error, stateExec.ErrorCode,
		stateExec.RetryCount, stateExec.InvocationID, stateExec.StartedAt, stateExec.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to update state execution: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("state execution not found")
	}
	return nil
}

// ==================== 模板仓库实现 ====================

// CreateTemplate 创建一个新的模板记录。
// 如果未提供 ID，将自动生成 UUID。
//
// 参数:
//   - template: 模板对象，包含所有模板属性
//
// 返回值:
//   - error: 创建失败时返回错误信息（如名称重复）
func (s *PostgresStore) CreateTemplate(template *domain.Template) error {
	// 自动生成 ID（如果未提供）
	if template.ID == "" {
		template.ID = uuid.New().String()
	}
	template.CreatedAt = time.Now()
	template.UpdatedAt = template.CreatedAt

	// 将变量列表序列化为 JSON
	variablesJSON, err := template.MarshalVariables()
	if err != nil {
		return fmt.Errorf("failed to marshal variables: %w", err)
	}

	// SQL: 插入模板记录到 templates 表
	query := `
		INSERT INTO templates (id, name, display_name, description, category, runtime, handler, code, variables, default_memory, default_timeout, tags, icon, popular, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`
	_, err = s.db.Exec(query,
		template.ID, template.Name, template.DisplayName, template.Description, template.Category, template.Runtime,
		template.Handler, template.Code, variablesJSON, template.DefaultMemory, template.DefaultTimeout,
		pq.Array(template.Tags), template.Icon, template.Popular, template.CreatedAt, template.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create template: %w", err)
	}
	return nil
}

// GetTemplateByID 根据模板 ID 获取模板详情。
//
// 参数:
//   - id: 模板唯一标识符
//
// 返回值:
//   - *domain.Template: 模板对象
//   - error: 模板不存在时返回 ErrTemplateNotFound，其他错误返回相应信息
func (s *PostgresStore) GetTemplateByID(id string) (*domain.Template, error) {
	query := `
		SELECT id, name, display_name, description, category, runtime, handler, code, variables, default_memory, default_timeout, tags, icon, popular, created_at, updated_at
		FROM templates WHERE id = $1
	`
	return s.scanTemplate(s.db.QueryRow(query, id))
}

// GetTemplateByName 根据模板名称获取模板详情。
//
// 参数:
//   - name: 模板名称
//
// 返回值:
//   - *domain.Template: 模板对象
//   - error: 模板不存在时返回 ErrTemplateNotFound，其他错误返回相应信息
func (s *PostgresStore) GetTemplateByName(name string) (*domain.Template, error) {
	query := `
		SELECT id, name, display_name, description, category, runtime, handler, code, variables, default_memory, default_timeout, tags, icon, popular, created_at, updated_at
		FROM templates WHERE name = $1
	`
	return s.scanTemplate(s.db.QueryRow(query, name))
}

// ListTemplates 分页查询模板列表，支持按分类和运行时筛选。
//
// 参数:
//   - offset: 跳过的记录数（用于分页）
//   - limit: 返回的最大记录数
//   - category: 模板分类（可选，为空则不筛选）
//   - runtime: 运行时类型（可选，为空则不筛选）
//
// 返回值:
//   - []*domain.Template: 模板列表
//   - int: 模板总数（用于分页计算）
//   - error: 查询失败时返回错误信息
func (s *PostgresStore) ListTemplates(offset, limit int, category, runtime string) ([]*domain.Template, int, error) {
	// 构建动态 WHERE 条件
	var conditions []string
	var args []interface{}
	argIndex := 1

	if category != "" {
		conditions = append(conditions, fmt.Sprintf("category = $%d", argIndex))
		args = append(args, category)
		argIndex++
	}

	if runtime != "" {
		conditions = append(conditions, fmt.Sprintf("runtime = $%d", argIndex))
		args = append(args, runtime)
		argIndex++
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	// SQL: 查询符合条件的模板总数
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM templates %s", whereClause)
	var total int
	err := s.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// SQL: 分页查询模板列表，热门优先，按创建时间倒序排列
	selectQuery := fmt.Sprintf(`
		SELECT id, name, display_name, description, category, runtime, handler, code, variables, default_memory, default_timeout, tags, icon, popular, created_at, updated_at
		FROM templates %s ORDER BY popular DESC, created_at DESC LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)
	args = append(args, limit, offset)

	rows, err := s.db.Query(selectQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var templates []*domain.Template
	for rows.Next() {
		template, err := s.scanTemplateRow(rows)
		if err != nil {
			return nil, 0, err
		}
		templates = append(templates, template)
	}
	return templates, total, nil
}

// UpdateTemplate 更新模板信息。
// 会自动更新 updated_at 时间戳。
//
// 参数:
//   - template: 包含更新数据的模板对象
//
// 返回值:
//   - error: 模板不存在时返回 ErrTemplateNotFound，其他错误返回相应信息
func (s *PostgresStore) UpdateTemplate(template *domain.Template) error {
	template.UpdatedAt = time.Now()

	variablesJSON, err := template.MarshalVariables()
	if err != nil {
		return fmt.Errorf("failed to marshal variables: %w", err)
	}

	query := `
		UPDATE templates SET
			display_name = $2, description = $3, category = $4, runtime = $5, handler = $6, code = $7,
			variables = $8, default_memory = $9, default_timeout = $10, tags = $11, icon = $12, popular = $13, updated_at = $14
		WHERE id = $1
	`
	result, err := s.db.Exec(query,
		template.ID, template.DisplayName, template.Description, template.Category, template.Runtime,
		template.Handler, template.Code, variablesJSON, template.DefaultMemory, template.DefaultTimeout,
		pq.Array(template.Tags), template.Icon, template.Popular, template.UpdatedAt,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if affected == 0 {
		return domain.ErrTemplateNotFound
	}
	return nil
}

// DeleteTemplate 删除指定的模板。
//
// 参数:
//   - id: 模板唯一标识符
//
// 返回值:
//   - error: 模板不存在时返回 ErrTemplateNotFound，其他错误返回相应信息
func (s *PostgresStore) DeleteTemplate(id string) error {
	result, err := s.db.Exec("DELETE FROM templates WHERE id = $1", id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if affected == 0 {
		return domain.ErrTemplateNotFound
	}
	return nil
}

// scanTemplate 从单行查询结果中扫描模板数据。
//
// 参数:
//   - row: 单行查询结果
//
// 返回值:
//   - *domain.Template: 解析后的模板对象
//   - error: 扫描失败或记录不存在时返回错误
func (s *PostgresStore) scanTemplate(row *sql.Row) (*domain.Template, error) {
	template := &domain.Template{}
	var variablesJSON []byte
	var description, icon sql.NullString
	err := row.Scan(
		&template.ID, &template.Name, &template.DisplayName, &description, &template.Category, &template.Runtime,
		&template.Handler, &template.Code, &variablesJSON, &template.DefaultMemory, &template.DefaultTimeout,
		pq.Array(&template.Tags), &icon, &template.Popular, &template.CreatedAt, &template.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, domain.ErrTemplateNotFound
	}
	if err != nil {
		return nil, err
	}
	// 处理可空字段
	if description.Valid {
		template.Description = description.String
	}
	if icon.Valid {
		template.Icon = icon.String
	}
	// 反序列化 JSON 字段
	template.UnmarshalVariables(variablesJSON)
	return template, nil
}

// scanTemplateRow 从多行查询结果中扫描单个模板数据。
//
// 参数:
//   - rows: 多行查询结果的当前行
//
// 返回值:
//   - *domain.Template: 解析后的模板对象
//   - error: 扫描失败时返回错误
func (s *PostgresStore) scanTemplateRow(rows *sql.Rows) (*domain.Template, error) {
	template := &domain.Template{}
	var variablesJSON []byte
	var description, icon sql.NullString
	err := rows.Scan(
		&template.ID, &template.Name, &template.DisplayName, &description, &template.Category, &template.Runtime,
		&template.Handler, &template.Code, &variablesJSON, &template.DefaultMemory, &template.DefaultTimeout,
		pq.Array(&template.Tags), &icon, &template.Popular, &template.CreatedAt, &template.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	// 处理可空字段
	if description.Valid {
		template.Description = description.String
	}
	if icon.Valid {
		template.Icon = icon.String
	}
	// 反序列化 JSON 字段
	template.UnmarshalVariables(variablesJSON)
	return template, nil
}

// ==================== 断点存储方法 ====================

// CreateBreakpoint 创建执行断点。
func (s *PostgresStore) CreateBreakpoint(bp *domain.Breakpoint) error {
	if bp.ID == "" {
		bp.ID = uuid.New().String()
	}
	bp.CreatedAt = time.Now()

	query := `
		INSERT INTO execution_breakpoints (id, execution_id, before_state, enabled, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (execution_id, before_state) DO UPDATE SET enabled = EXCLUDED.enabled
	`
	_, err := s.db.Exec(query, bp.ID, bp.ExecutionID, bp.BeforeState, bp.Enabled, bp.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to create breakpoint: %w", err)
	}
	return nil
}

// GetBreakpoint 获取指定执行和状态的断点。
func (s *PostgresStore) GetBreakpoint(executionID, beforeState string) (*domain.Breakpoint, error) {
	query := `
		SELECT id, execution_id, before_state, enabled, created_at
		FROM execution_breakpoints
		WHERE execution_id = $1 AND before_state = $2
	`
	bp := &domain.Breakpoint{}
	err := s.db.QueryRow(query, executionID, beforeState).Scan(
		&bp.ID, &bp.ExecutionID, &bp.BeforeState, &bp.Enabled, &bp.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get breakpoint: %w", err)
	}
	return bp, nil
}

// ListBreakpoints 列出执行实例的所有断点。
func (s *PostgresStore) ListBreakpoints(executionID string) ([]*domain.Breakpoint, error) {
	query := `
		SELECT id, execution_id, before_state, enabled, created_at
		FROM execution_breakpoints
		WHERE execution_id = $1
		ORDER BY created_at ASC
	`
	rows, err := s.db.Query(query, executionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list breakpoints: %w", err)
	}
	defer rows.Close()

	var breakpoints []*domain.Breakpoint
	for rows.Next() {
		bp := &domain.Breakpoint{}
		if err := rows.Scan(&bp.ID, &bp.ExecutionID, &bp.BeforeState, &bp.Enabled, &bp.CreatedAt); err != nil {
			return nil, err
		}
		breakpoints = append(breakpoints, bp)
	}
	return breakpoints, nil
}

// DeleteBreakpoint 删除断点。
func (s *PostgresStore) DeleteBreakpoint(executionID, beforeState string) error {
	query := `DELETE FROM execution_breakpoints WHERE execution_id = $1 AND before_state = $2`
	result, err := s.db.Exec(query, executionID, beforeState)
	if err != nil {
		return fmt.Errorf("failed to delete breakpoint: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("breakpoint not found")
	}
	return nil
}
