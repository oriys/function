// Package domain 定义了函数计算平台的核心领域模型。
// 该包包含了函数、调用、错误等核心实体的定义，以及相关的接口和请求/响应结构体。
// 这是整个应用程序的领域层，遵循领域驱动设计(DDD)原则。
package domain

import (
	"encoding/json"
	"time"

	"github.com/robfig/cron/v3"
)

// Runtime 表示函数运行时类型。
// 运行时决定了函数代码的执行环境，如 Python、Node.js、Go 等。
type Runtime string

// 支持的运行时常量定义
const (
	// RuntimePython311 表示 Python 3.11 运行时环境
	RuntimePython311 Runtime = "python3.11"
	// RuntimeNodeJS20 表示 Node.js 20 运行时环境
	RuntimeNodeJS20 Runtime = "nodejs20"
	// RuntimeGo124 表示 Go 1.24 运行时环境
	RuntimeGo124 Runtime = "go1.24"
	// RuntimeWasm 表示 WebAssembly 运行时环境
	RuntimeWasm Runtime = "wasm"
)

// 代码大小限制常量
const (
	// MaxCodeSize 是函数源代码的最大大小（512KB）
	MaxCodeSize = 512 * 1024
	// MaxBinarySize 是编译后二进制的最大大小（50MB）
	MaxBinarySize = 50 * 1024 * 1024
)

// ValidateCodeSize 验证代码大小是否在限制范围内
// 返回 nil 表示验证通过，否则返回 ErrCodeSizeExceeded
func ValidateCodeSize(code string) error {
	if len(code) > MaxCodeSize {
		return ErrCodeSizeExceeded
	}
	return nil
}

// ValidateBinarySize 验证二进制大小是否在限制范围内
// 返回 nil 表示验证通过，否则返回 ErrBinarySizeExceeded
func ValidateBinarySize(binary string) error {
	if len(binary) > MaxBinarySize {
		return ErrBinarySizeExceeded
	}
	return nil
}

// ValidateCronExpression 验证 cron 表达式是否有效
// 支持标准 6 字段格式（包含秒）：秒 分 时 日 月 星期
// 返回 nil 表示验证通过，否则返回 ErrInvalidCronExpression
func ValidateCronExpression(expr string) error {
	if expr == "" {
		return nil // 空表达式是有效的（表示无定时任务）
	}
	// 使用支持秒级的 cron 解析器进行验证
	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	_, err := parser.Parse(expr)
	if err != nil {
		return ErrInvalidCronExpression
	}
	return nil
}

// GetCodeSizeInfo 返回代码大小信息
func GetCodeSizeInfo(code string) (size int, limit int, percentage float64) {
	size = len(code)
	limit = MaxCodeSize
	percentage = float64(size) / float64(limit) * 100
	return
}

// IsValid 检查运行时类型是否有效。
// 返回 true 表示该运行时是受支持的，返回 false 表示不受支持。
func (r Runtime) IsValid() bool {
	switch r {
	case RuntimePython311, RuntimeNodeJS20, RuntimeGo124, RuntimeWasm:
		return true
	default:
		return false
	}
}

// FunctionStatus 表示函数的状态类型。
// 函数在其生命周期中可能处于不同的状态。
type FunctionStatus string

// 函数状态常量定义
const (
	// FunctionStatusCreating 表示函数正在创建中（异步）
	FunctionStatusCreating FunctionStatus = "creating"
	// FunctionStatusActive 表示函数处于活跃状态，可以被调用
	FunctionStatusActive FunctionStatus = "active"
	// FunctionStatusUpdating 表示函数正在更新中（异步）
	FunctionStatusUpdating FunctionStatus = "updating"
	// FunctionStatusOffline 表示函数已下线，暂停服务但可恢复
	FunctionStatusOffline FunctionStatus = "offline"
	// FunctionStatusInactive 表示函数处于非活跃状态，暂停服务
	FunctionStatusInactive FunctionStatus = "inactive"
	// FunctionStatusBuilding 表示函数正在构建中
	FunctionStatusBuilding FunctionStatus = "building"
	// FunctionStatusFailed 表示函数构建或部署失败
	FunctionStatusFailed FunctionStatus = "failed"
)

// CanInvoke 检查当前状态是否可以调用函数
func (s FunctionStatus) CanInvoke() bool {
	return s == FunctionStatusActive
}

// CanUpdate 检查当前状态是否可以更新函数
func (s FunctionStatus) CanUpdate() bool {
	return s == FunctionStatusActive || s == FunctionStatusFailed || s == FunctionStatusOffline
}

// CanOffline 检查当前状态是否可以下线
func (s FunctionStatus) CanOffline() bool {
	return s == FunctionStatusActive
}

// CanOnline 检查当前状态是否可以上线
func (s FunctionStatus) CanOnline() bool {
	return s == FunctionStatusOffline
}

// Function 表示一个无服务器函数实体。
// 这是函数计算平台的核心领域对象，包含了函数的所有配置和元数据。
type Function struct {
	// ID 是函数的唯一标识符
	ID string `json:"id"`
	// Name 是函数的名称，用于用户识别
	Name string `json:"name"`
	// Description 是函数的描述信息，可选
	Description string `json:"description,omitempty"`
	// Tags 是函数的标签列表，用于分类和筛选
	Tags []string `json:"tags,omitempty"`
	// Pinned 表示函数是否被置顶/收藏
	Pinned bool `json:"pinned"`
	// Runtime 是函数的运行时环境
	Runtime Runtime `json:"runtime"`
	// Handler 是函数的入口点，格式取决于运行时（如 Python 为 "module.function"）
	Handler string `json:"handler"`
	// Code 是函数的源代码内容
	Code string `json:"code,omitempty"`
	// Binary 是编译后的二进制内容（base64 编码），用于 Go/Rust 等编译型语言
	Binary string `json:"binary,omitempty"`
	// CodeHash 是代码的哈希值，用于版本控制和缓存
	CodeHash string `json:"code_hash,omitempty"`
	// MemoryMB 是分配给函数的内存大小（单位：MB）
	MemoryMB int `json:"memory_mb"`
	// TimeoutSec 是函数执行的超时时间（单位：秒）
	TimeoutSec int `json:"timeout_sec"`
	// MaxConcurrency 是函数的最大并发执行数（0 表示无限制）
	MaxConcurrency int `json:"max_concurrency"`
	// EnvVars 是函数的环境变量配置
	EnvVars map[string]string `json:"env_vars,omitempty"`
	// Status 是函数的当前状态
	Status FunctionStatus `json:"status"`
	// StatusMessage 是状态相关的消息（如错误原因）
	StatusMessage string `json:"status_message,omitempty"`
	// TaskID 是当前正在执行的异步任务ID
	TaskID string `json:"task_id,omitempty"`
	// Version 是函数的版本号
	Version int `json:"version"`
	// CronExpression 是定时任务表达式（可选），如 "*/5 * * * *"
	CronExpression string `json:"cron_expression,omitempty"`
	// HTTPPath 是自定义 HTTP 路由路径（可选），如 "/api/hello"
	HTTPPath string `json:"http_path,omitempty"`
	// HTTPMethods 是自定义 HTTP 路由方法（可选），如 ["GET", "POST"]
	HTTPMethods []string `json:"http_methods,omitempty"`
	// WebhookEnabled 表示是否启用 Webhook 触发
	WebhookEnabled bool `json:"webhook_enabled"`
	// WebhookKey 是 Webhook 的唯一密钥，用于生成 Webhook URL
	WebhookKey string `json:"webhook_key,omitempty"`
	// LastDeployedAt 是最后一次成功部署的时间
	LastDeployedAt *time.Time `json:"last_deployed_at,omitempty"`
	// CreatedAt 是函数的创建时间
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt 是函数的最后更新时间
	UpdatedAt time.Time `json:"updated_at"`
}

// FunctionTaskType 表示函数任务类型
type FunctionTaskType string

const (
	// FunctionTaskCreate 创建函数任务
	FunctionTaskCreate FunctionTaskType = "create"
	// FunctionTaskUpdate 更新函数任务
	FunctionTaskUpdate FunctionTaskType = "update"
)

// FunctionTaskStatus 表示函数任务状态
type FunctionTaskStatus string

const (
	// FunctionTaskPending 任务等待处理
	FunctionTaskPending FunctionTaskStatus = "pending"
	// FunctionTaskRunning 任务正在执行
	FunctionTaskRunning FunctionTaskStatus = "running"
	// FunctionTaskCompleted 任务执行成功
	FunctionTaskCompleted FunctionTaskStatus = "completed"
	// FunctionTaskFailed 任务执行失败
	FunctionTaskFailed FunctionTaskStatus = "failed"
)

// FunctionTask 表示函数的异步操作任务
type FunctionTask struct {
	// ID 是任务的唯一标识符
	ID string `json:"id"`
	// FunctionID 是关联的函数 ID
	FunctionID string `json:"function_id"`
	// Type 是任务类型（create/update）
	Type FunctionTaskType `json:"type"`
	// Status 是任务状态
	Status FunctionTaskStatus `json:"status"`
	// Input 是任务输入参数（JSON）
	Input json.RawMessage `json:"input,omitempty"`
	// Output 是任务输出结果（JSON）
	Output json.RawMessage `json:"output,omitempty"`
	// Error 是错误信息
	Error string `json:"error,omitempty"`
	// CreatedAt 是任务创建时间
	CreatedAt time.Time `json:"created_at"`
	// StartedAt 是任务开始执行时间
	StartedAt *time.Time `json:"started_at,omitempty"`
	// CompletedAt 是任务完成时间
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// CreateFunctionRequest 表示创建函数的请求结构体。
// 包含创建一个新函数所需的所有参数。
type CreateFunctionRequest struct {
	// Name 是函数名称，必填，长度限制为 1-64 字符
	Name string `json:"name" validate:"required,min=1,max=64"`
	// Description 是函数描述，可选
	Description string `json:"description,omitempty"`
	// Tags 是函数标签，可选
	Tags []string `json:"tags,omitempty"`
	// Runtime 是运行时类型，必填
	Runtime Runtime `json:"runtime" validate:"required"`
	// Handler 是函数入口点，必填
	Handler string `json:"handler" validate:"required"`
	// Code 是函数源代码，必填
	Code string `json:"code" validate:"required"`
	// Binary 是预编译的二进制（base64 编码），可选
	// 用于 Go/Rust 等编译型语言，如果提供则跳过编译步骤
	Binary string `json:"binary,omitempty"`
	// MemoryMB 是内存配置（单位：MB），可选，默认 256MB
	MemoryMB int `json:"memory_mb,omitempty"`
	// TimeoutSec 是超时配置（单位：秒），可选，默认 30 秒
	TimeoutSec int `json:"timeout_sec,omitempty"`
	// MaxConcurrency 是最大并发数，可选，默认 0（无限制）
	MaxConcurrency int `json:"max_concurrency,omitempty"`
	// EnvVars 是环境变量配置，可选
	EnvVars map[string]string `json:"env_vars,omitempty"`
	// CronExpression 是定时任务表达式（可选）
	CronExpression string `json:"cron_expression,omitempty"`
	// HTTPPath 是自定义 HTTP 路由路径（可选）
	HTTPPath string `json:"http_path,omitempty"`
	// HTTPMethods 是自定义 HTTP 路由方法（可选）
	HTTPMethods []string `json:"http_methods,omitempty"`
}

// Validate 验证创建函数请求的参数是否有效。
// 如果验证失败，返回相应的错误；验证通过则返回 nil。
// 该方法还会为可选参数设置默认值。
func (r *CreateFunctionRequest) Validate() error {
	if r.Name == "" {
		return ErrInvalidFunctionName
	}
	if !r.Runtime.IsValid() {
		return ErrInvalidRuntime
	}
	if r.Handler == "" {
		return ErrInvalidHandler
	}
	if r.Code == "" {
		return ErrInvalidCode
	}
	// 验证代码大小
	if err := ValidateCodeSize(r.Code); err != nil {
		return err
	}
	// 验证二进制大小（如果提供）
	if r.Binary != "" {
		if err := ValidateBinarySize(r.Binary); err != nil {
			return err
		}
	}
	// 验证 cron 表达式语法
	if err := ValidateCronExpression(r.CronExpression); err != nil {
		return err
	}
	// 如果未指定内存，设置默认值为 256MB
	if r.MemoryMB == 0 {
		r.MemoryMB = 256
	}
	// 验证内存范围：128MB - 3072MB
	if r.MemoryMB < 128 || r.MemoryMB > 3072 {
		return ErrInvalidMemory
	}
	// 如果未指定超时时间，设置默认值为 30 秒
	if r.TimeoutSec == 0 {
		r.TimeoutSec = 30
	}
	// 验证超时时间范围：1-300 秒
	if r.TimeoutSec < 1 || r.TimeoutSec > 300 {
		return ErrInvalidTimeout
	}
	return nil
}

// UpdateFunctionRequest 表示更新函数的请求结构体。
// 所有字段都是指针类型，允许部分更新（只更新非 nil 的字段）。
type UpdateFunctionRequest struct {
	// Description 是更新后的函数描述
	Description *string `json:"description,omitempty"`
	// Tags 是更新后的函数标签
	Tags *[]string `json:"tags,omitempty"`
	// Code 是更新后的函数源代码
	Code *string `json:"code,omitempty"`
	// Handler 是更新后的函数入口点
	Handler *string `json:"handler,omitempty"`
	// MemoryMB 是更新后的内存配置（单位：MB）
	MemoryMB *int `json:"memory_mb,omitempty"`
	// TimeoutSec 是更新后的超时配置（单位：秒）
	TimeoutSec *int `json:"timeout_sec,omitempty"`
	// MaxConcurrency 是更新后的最大并发数
	MaxConcurrency *int `json:"max_concurrency,omitempty"`
	// EnvVars 是更新后的环境变量配置
	EnvVars *map[string]string `json:"env_vars,omitempty"`
	// CronExpression 是更新后的定时任务表达式
	CronExpression *string `json:"cron_expression,omitempty"`
	// HTTPPath 是更新后的自定义 HTTP 路由路径
	HTTPPath *string `json:"http_path,omitempty"`
	// HTTPMethods 是更新后的自定义 HTTP 路由方法
	HTTPMethods *[]string `json:"http_methods,omitempty"`
}

// FunctionRepository 定义了函数存储的接口。
// 该接口抽象了函数的持久化操作，允许不同的存储实现（如数据库、内存等）。
type FunctionRepository interface {
	// Create 创建一个新的函数记录
	Create(fn *Function) error
	// GetByID 根据 ID 获取函数
	GetByID(id string) (*Function, error)
	// GetByName 根据名称获取函数
	GetByName(name string) (*Function, error)
	// List 分页获取函数列表，返回函数列表、总数和可能的错误
	List(offset, limit int) ([]*Function, int, error)
	// Update 更新函数信息
	Update(fn *Function) error
	// Delete 根据 ID 删除函数
	Delete(id string) error
}

// FunctionFilter 用于函数列表的筛选条件
type FunctionFilter struct {
	// Name 函数名称（模糊匹配）
	Name string `json:"name,omitempty"`
	// Tags 标签列表（必须包含所有指定标签）
	Tags []string `json:"tags,omitempty"`
	// Runtime 运行时类型（精确匹配）
	Runtime Runtime `json:"runtime,omitempty"`
	// Status 函数状态（精确匹配）
	Status FunctionStatus `json:"status,omitempty"`
}

// ==================== 批量操作相关类型 ====================

// BulkDeleteRequest 表示批量删除函数的请求
type BulkDeleteRequest struct {
	// IDs 要删除的函数 ID 列表
	IDs []string `json:"ids" validate:"required,min=1"`
}

// BulkUpdateRequest 表示批量更新函数的请求
type BulkUpdateRequest struct {
	// IDs 要更新的函数 ID 列表
	IDs []string `json:"ids" validate:"required,min=1"`
	// Status 要更新的状态（可选）
	Status FunctionStatus `json:"status,omitempty"`
	// Tags 要设置的标签（可选）
	Tags []string `json:"tags,omitempty"`
}

// BulkOperationResult 表示批量操作的结果
type BulkOperationResult struct {
	// Success 成功处理的函数 ID 列表
	Success []string `json:"success"`
	// Failed 处理失败的函数列表
	Failed []BulkOperationFailure `json:"failed"`
}

// BulkOperationFailure 表示单个失败的操作
type BulkOperationFailure struct {
	// ID 失败的函数 ID
	ID string `json:"id"`
	// Error 失败原因
	Error string `json:"error"`
}

// InvokeRequest 表示函数调用请求结构体。
// 包含调用一个函数所需的所有参数。
type InvokeRequest struct {
	// FunctionID 是要调用的函数 ID（从 URL 路径中获取，不参与 JSON 序列化）
	FunctionID string `json:"-"`
	// Payload 是传递给函数的输入参数，以 JSON 格式表示
	Payload json.RawMessage `json:"payload"`
	// Async 表示是否异步调用
	Async bool `json:"async,omitempty"`
	// Debug 表示是否开启调试模式
	Debug bool `json:"debug,omitempty"`
}

// InvokeResponse 表示函数调用响应结构体。
// 包含函数调用的结果和执行信息。
type InvokeResponse struct {
	// RequestID 是本次调用的唯一请求标识
	RequestID string `json:"request_id"`
	// StatusCode 是函数执行返回的状态码
	StatusCode int `json:"status_code"`
	// Body 是函数执行的返回结果，以 JSON 格式表示
	Body json.RawMessage `json:"body,omitempty"`
	// Error 是函数执行过程中的错误信息
	Error string `json:"error,omitempty"`
	// DurationMs 是函数执行耗时（单位：毫秒）
	DurationMs int64 `json:"duration_ms"`
	// ColdStart 表示本次调用是否为冷启动
	ColdStart bool `json:"cold_start"`
	// BilledTimeMs 是计费时长（单位：毫秒），按最小计费单位向上取整
	BilledTimeMs int64 `json:"billed_time_ms"`
}

// ==================== 版本管理相关类型 ====================

// FunctionVersion 表示函数的一个不可变版本快照。
// 每次函数代码更新时会自动创建新版本，支持回滚操作。
type FunctionVersion struct {
	// ID 是版本记录的唯一标识符
	ID string `json:"id"`
	// FunctionID 是关联的函数 ID
	FunctionID string `json:"function_id"`
	// Version 是版本号（从 1 开始递增）
	Version int `json:"version"`
	// Handler 是该版本的函数入口点
	Handler string `json:"handler"`
	// Code 是该版本的源代码
	Code string `json:"code,omitempty"`
	// Binary 是该版本的编译后二进制（base64 编码）
	Binary string `json:"binary,omitempty"`
	// CodeHash 是代码的哈希值
	CodeHash string `json:"code_hash"`
	// Description 是版本描述（可选）
	Description string `json:"description,omitempty"`
	// CreatedAt 是版本创建时间
	CreatedAt time.Time `json:"created_at"`
}

// ==================== 别名与流量分配相关类型 ====================

// FunctionAlias 表示函数别名，用于流量管理和灰度发布。
// 别名可以关联多个版本并按权重分配流量。
type FunctionAlias struct {
	// ID 是别名记录的唯一标识符
	ID string `json:"id"`
	// FunctionID 是关联的函数 ID
	FunctionID string `json:"function_id"`
	// Name 是别名名称（如 'prod', 'canary', 'latest'）
	Name string `json:"name"`
	// Description 是别名描述（可选）
	Description string `json:"description,omitempty"`
	// RoutingConfig 是流量路由配置
	RoutingConfig RoutingConfig `json:"routing_config"`
	// CreatedAt 是别名创建时间
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt 是别名最后更新时间
	UpdatedAt time.Time `json:"updated_at"`
}

// RoutingConfig 定义流量路由配置。
type RoutingConfig struct {
	// Weights 是版本权重列表，用于流量分配
	Weights []VersionWeight `json:"weights"`
}

// VersionWeight 定义单个版本的流量权重。
type VersionWeight struct {
	// Version 是目标版本号
	Version int `json:"version"`
	// Weight 是流量权重（百分比，0-100）
	Weight int `json:"weight"`
}

// CreateAliasRequest 表示创建别名的请求。
type CreateAliasRequest struct {
	// Name 是别名名称，必填
	Name string `json:"name" validate:"required,min=1,max=64"`
	// Description 是别名描述，可选
	Description string `json:"description,omitempty"`
	// RoutingConfig 是流量路由配置，必填
	RoutingConfig RoutingConfig `json:"routing_config" validate:"required"`
}

// UpdateAliasRequest 表示更新别名的请求。
type UpdateAliasRequest struct {
	// Description 是更新后的别名描述
	Description *string `json:"description,omitempty"`
	// RoutingConfig 是更新后的流量路由配置
	RoutingConfig *RoutingConfig `json:"routing_config,omitempty"`
}

// ==================== 函数层相关类型 ====================

// Layer 表示共享依赖层。
// 层可以被多个函数共享，用于存放公共依赖库。
type Layer struct {
	// ID 是层的唯一标识符
	ID string `json:"id"`
	// Name 是层名称（全局唯一）
	Name string `json:"name"`
	// Description 是层描述（可选）
	Description string `json:"description,omitempty"`
	// CompatibleRuntimes 是兼容的运行时列表
	CompatibleRuntimes []string `json:"compatible_runtimes"`
	// LatestVersion 是最新版本号
	LatestVersion int `json:"latest_version"`
	// CreatedAt 是层创建时间
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt 是层最后更新时间
	UpdatedAt time.Time `json:"updated_at"`
}

// LayerVersion 表示层的一个不可变版本。
type LayerVersion struct {
	// ID 是版本记录的唯一标识符
	ID string `json:"id"`
	// LayerID 是关联的层 ID
	LayerID string `json:"layer_id"`
	// Version 是版本号
	Version int `json:"version"`
	// ContentHash 是内容的哈希值
	ContentHash string `json:"content_hash"`
	// SizeBytes 是内容大小（字节）
	SizeBytes int64 `json:"size_bytes"`
	// CreatedAt 是版本创建时间
	CreatedAt time.Time `json:"created_at"`
}

// FunctionLayer 表示函数与层的关联关系。
type FunctionLayer struct {
	// LayerID 是层的 ID
	LayerID string `json:"layer_id"`
	// LayerName 是层的名称
	LayerName string `json:"layer_name"`
	// LayerVersion 是使用的层版本号
	LayerVersion int `json:"layer_version"`
	// Order 是层的加载顺序
	Order int `json:"order"`
}

// RuntimeLayerInfo 表示运行时加载层所需的信息。
// 用于在函数执行时传递层内容到执行器。
type RuntimeLayerInfo struct {
	// LayerID 是层的唯一标识符
	LayerID string `json:"layer_id"`
	// Version 是层的版本号
	Version int `json:"version"`
	// Content 是层的内容（ZIP 压缩包）
	Content []byte `json:"content"`
	// Order 是层的加载顺序（小的先加载）
	Order int `json:"order"`
}

// CreateLayerRequest 表示创建层的请求。
type CreateLayerRequest struct {
	// Name 是层名称，必填
	Name string `json:"name" validate:"required,min=1,max=128"`
	// Description 是层描述，可选
	Description string `json:"description,omitempty"`
	// CompatibleRuntimes 是兼容的运行时列表，必填
	CompatibleRuntimes []string `json:"compatible_runtimes" validate:"required,min=1"`
}

// ==================== 环境管理相关类型 ====================

// Environment 表示部署环境。
// 环境用于隔离不同阶段的函数配置（如开发、测试、生产）。
type Environment struct {
	// ID 是环境的唯一标识符
	ID string `json:"id"`
	// Name 是环境名称（如 'dev', 'staging', 'prod'）
	Name string `json:"name"`
	// Description 是环境描述（可选）
	Description string `json:"description,omitempty"`
	// IsDefault 表示是否为默认环境
	IsDefault bool `json:"is_default"`
	// CreatedAt 是环境创建时间
	CreatedAt time.Time `json:"created_at"`
}

// FunctionEnvConfig 表示函数在特定环境下的配置。
type FunctionEnvConfig struct {
	// FunctionID 是函数 ID
	FunctionID string `json:"function_id"`
	// EnvironmentID 是环境 ID
	EnvironmentID string `json:"environment_id"`
	// EnvironmentName 是环境名称
	EnvironmentName string `json:"environment_name,omitempty"`
	// EnvVars 是环境特定的环境变量
	EnvVars map[string]string `json:"env_vars,omitempty"`
	// MemoryMB 是环境特定的内存配置（可选，覆盖函数默认值）
	MemoryMB *int `json:"memory_mb,omitempty"`
	// TimeoutSec 是环境特定的超时配置（可选，覆盖函数默认值）
	TimeoutSec *int `json:"timeout_sec,omitempty"`
	// ActiveAlias 是该环境使用的别名
	ActiveAlias string `json:"active_alias,omitempty"`
	// CreatedAt 是配置创建时间
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt 是配置最后更新时间
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateEnvironmentRequest 表示创建环境的请求。
type CreateEnvironmentRequest struct {
	// Name 是环境名称，必填
	Name string `json:"name" validate:"required,min=1,max=64"`
	// Description 是环境描述，可选
	Description string `json:"description,omitempty"`
	// IsDefault 是否设为默认环境
	IsDefault bool `json:"is_default,omitempty"`
}

// UpdateFunctionEnvConfigRequest 表示更新函数环境配置的请求。
type UpdateFunctionEnvConfigRequest struct {
	// EnvVars 是环境特定的环境变量
	EnvVars *map[string]string `json:"env_vars,omitempty"`
	// MemoryMB 是环境特定的内存配置
	MemoryMB *int `json:"memory_mb,omitempty"`
	// TimeoutSec 是环境特定的超时配置
	TimeoutSec *int `json:"timeout_sec,omitempty"`
	// ActiveAlias 是该环境使用的别名
	ActiveAlias *string `json:"active_alias,omitempty"`
}

// ==================== 死信队列 (DLQ) 相关类型 ====================

// DeadLetterMessage 表示死信队列中的一条消息。
// 当函数调用失败并超过重试次数后，调用信息会被保存到死信队列。
type DeadLetterMessage struct {
	// ID 是死信消息的唯一标识符
	ID string `json:"id"`
	// FunctionID 是关联的函数 ID
	FunctionID string `json:"function_id"`
	// FunctionName 是函数名称
	FunctionName string `json:"function_name,omitempty"`
	// OriginalRequestID 是原始调用的请求 ID
	OriginalRequestID string `json:"original_request_id"`
	// Payload 是原始调用的输入载荷
	Payload json.RawMessage `json:"payload"`
	// Error 是失败原因
	Error string `json:"error"`
	// RetryCount 是已重试次数
	RetryCount int `json:"retry_count"`
	// Status 是消息状态（pending/retrying/resolved/discarded）
	Status string `json:"status"`
	// CreatedAt 是消息创建时间（首次失败时间）
	CreatedAt time.Time `json:"created_at"`
	// LastRetryAt 是最后一次重试时间
	LastRetryAt *time.Time `json:"last_retry_at,omitempty"`
	// ResolvedAt 是消息解决时间
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// DLQ消息状态常量
const (
	// DLQStatusPending 等待处理
	DLQStatusPending = "pending"
	// DLQStatusRetrying 正在重试
	DLQStatusRetrying = "retrying"
	// DLQStatusResolved 已解决（重试成功或手动标记）
	DLQStatusResolved = "resolved"
	// DLQStatusDiscarded 已丢弃
	DLQStatusDiscarded = "discarded"
)
