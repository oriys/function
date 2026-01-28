// Package config 提供了函数计算平台的配置管理功能。
// 该包负责从 YAML 配置文件加载配置，并支持通过环境变量覆盖敏感配置项（如密码和密钥）。
// 配置包含了服务器、认证、运行时、网络、存储、日志、指标和遥测等多个方面的设置。
package config

import (
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是应用程序的主配置结构体，包含所有子系统的配置。
// 该结构体通过 YAML 标签与配置文件进行映射。
type Config struct {
	// Server 服务器配置，包括 HTTP 端口、调用端口等
	Server ServerConfig `yaml:"server"`
	// Auth 认证配置，包括 JWT 和 API Key 相关设置
	Auth AuthConfig `yaml:"auth"`
	// Runtime 运行时模式配置，指定使用 Firecracker 或 Docker
	Runtime RuntimeMode `yaml:"runtime"`
	// Firecracker Firecracker 微虚拟机的相关配置
	Firecracker FirecrackerConfig `yaml:"firecracker"`
	// Docker Docker 容器运行时的相关配置
	Docker DockerConfig `yaml:"docker"`
	// Network 网络配置，包括网桥、子网等设置
	Network NetworkConfig `yaml:"network"`
	// Pool 虚拟机/容器池配置，包括健康检查和扩缩容策略
	Pool PoolConfig `yaml:"pool"`
	// Scheduler 调度器配置，包括工作线程数和队列大小等
	Scheduler SchedulerConfig `yaml:"scheduler"`
	// Storage 存储配置，包括 PostgreSQL 和 Redis 连接信息
	Storage StorageConfig `yaml:"storage"`
	// Events 事件配置，包括 NATS 消息队列连接信息
	Events EventsConfig `yaml:"events"`
	// Logging 日志配置，包括日志级别和格式
	Logging LoggingConfig `yaml:"logging"`
	// Metrics 指标配置，用于 Prometheus 监控
	Metrics MetricsConfig `yaml:"metrics"`
	// Telemetry 遥测配置，用于分布式追踪
	Telemetry TelemetryConfig `yaml:"telemetry"`
	// Workflow 工作流引擎配置
	Workflow WorkflowConfig `yaml:"workflow"`
	// Snapshot 函数级快照配置
	Snapshot SnapshotConfig `yaml:"snapshot"`
	// State 有状态函数配置
	State StateConfig `yaml:"state"`
}

// RuntimeMode 运行时模式配置结构体。
// 用于指定函数执行的底层运行时环境。
type RuntimeMode struct {
	// Mode 运行时模式，可选值为 "firecracker"（微虚拟机）或 "docker"（容器）
	// 默认值：docker
	Mode string `yaml:"mode"`
}

// DockerConfig Docker 容器运行时配置结构体。
// 当 RuntimeMode 设置为 "docker" 时使用此配置。
type DockerConfig struct {
	// NetworkMode Docker 网络模式，可选值：
	// - "none"：无网络（最安全，默认值）
	// - "bridge"：桥接网络
	// - "host"：主机网络
	NetworkMode string `yaml:"network_mode"`
	// Images 运行时镜像映射表，键为运行时名称（如 python3.9），值为 Docker 镜像名称
	Images map[string]string `yaml:"images,omitempty"`
	// Pool Docker 容器池配置
	Pool DockerPoolConfig `yaml:"pool"`
}

// DockerPoolConfig Docker 容器池配置结构体。
// 用于管理预热容器池，提高函数冷启动性能。
type DockerPoolConfig struct {
	// Enabled 是否启用容器池
	Enabled bool `yaml:"enabled"`
	// MaxTotal 容器池中容器的最大总数
	// 默认值：与 Scheduler.Workers 相同，如果未设置则为 10
	MaxTotal int `yaml:"max_total"`
	// MinWarm 最小预热容器数量，保持随时可用的容器数
	// 默认值：0
	MinWarm int `yaml:"min_warm"`
	// MaxInvocations 单个容器的最大调用次数，达到后容器将被回收
	// 默认值：1000
	MaxInvocations int `yaml:"max_invocations"`
	// MaxContainerAge 容器的最大存活时间，超过后将被回收
	// 默认值：1 小时
	MaxContainerAge time.Duration `yaml:"max_container_age"`
	// TmpfsSizeMB 容器 tmpfs 挂载的大小（MB），用于临时文件存储
	// 默认值：64 MB
	TmpfsSizeMB int `yaml:"tmpfs_size_mb"`
	// DisableResourceLimits 禁用容器资源限制（--memory、--cpus）
	// 在 Docker-in-Docker 环境中使用 cgroup v2 时可能需要启用此选项
	// 默认值：false
	DisableResourceLimits bool `yaml:"disable_resource_limits"`
}

// ServerConfig 服务器配置结构体。
// 定义了各种服务端口和超时设置。
type ServerConfig struct {
	// HTTPPort HTTP API 服务端口，用于函数管理 API
	// 默认值：8080
	HTTPPort int `yaml:"http_port"`
	// InvokePort 函数调用服务端口，用于函数执行请求
	// 默认值：8081
	InvokePort int `yaml:"invoke_port"`
	// MetricsPort 指标服务端口，用于 Prometheus 指标暴露
	// 默认值：9090
	MetricsPort int `yaml:"metrics_port"`
	// ShutdownTimeout 优雅关闭超时时间
	// 默认值：30 秒
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// AuthConfig 认证配置结构体。
// 定义了 JWT 和 API Key 认证相关的设置。
type AuthConfig struct {
	// Enabled 是否启用认证
	Enabled bool `yaml:"enabled"`
	// JWTSecret JWT 签名密钥，可通过环境变量 NIMBUS_AUTH_JWT_SECRET 或
	// NIMBUS_AUTH_JWT_SECRET_FILE（文件路径）覆盖（兼容旧的 FUNCTION_AUTH_JWT_SECRET / *_FILE）
	JWTSecret string `yaml:"jwt_secret"`
	// JWTExpiration JWT 令牌过期时间
	// 默认值：24 小时
	JWTExpiration time.Duration `yaml:"jwt_expiration"`
	// APIKeyHeader API Key 请求头名称
	// 默认值：X-API-Key
	APIKeyHeader string `yaml:"api_key_header"`
}

// FirecrackerConfig Firecracker 微虚拟机配置结构体。
// 当 RuntimeMode 设置为 "firecracker" 时使用此配置。
type FirecrackerConfig struct {
	// Binary Firecracker 可执行文件路径
	Binary string `yaml:"binary"`
	// Kernel Linux 内核镜像文件路径
	Kernel string `yaml:"kernel"`
	// RootfsDir 根文件系统镜像存放目录
	RootfsDir string `yaml:"rootfs_dir"`
	// SocketDir Unix 套接字文件存放目录，用于与虚拟机通信
	SocketDir string `yaml:"socket_dir"`
	// VsockDir Vsock 套接字文件存放目录，用于高性能虚拟机通信
	VsockDir string `yaml:"vsock_dir"`
	// SnapshotDir 虚拟机快照存放目录，用于加速启动
	SnapshotDir string `yaml:"snapshot_dir"`
	// LogDir Firecracker 日志文件存放目录
	LogDir string `yaml:"log_dir"`
	// BootTimeout 虚拟机启动超时时间
	// 默认值：10 秒
	BootTimeout time.Duration `yaml:"boot_timeout"`
}

// NetworkConfig 网络配置结构体。
// 定义了虚拟机/容器网络相关的设置。
type NetworkConfig struct {
	// BridgeName 网桥名称
	BridgeName string `yaml:"bridge_name"`
	// BridgeIP 网桥 IP 地址
	BridgeIP string `yaml:"bridge_ip"`
	// SubnetCIDR 子网 CIDR 表示法，如 "192.168.1.0/24"
	SubnetCIDR string `yaml:"subnet_cidr"`
	// CNIConfigDir CNI（容器网络接口）配置文件目录
	CNIConfigDir string `yaml:"cni_config_dir"`
	// CNIBinDir CNI 插件可执行文件目录
	CNIBinDir string `yaml:"cni_bin_dir"`
	// UseNAT 是否启用 NAT 网络地址转换
	UseNAT bool `yaml:"use_nat"`
	// ExternalInterface 外部网络接口名称，用于 NAT 出口
	ExternalInterface string `yaml:"external_interface"`
}

// PoolConfig 虚拟机/容器池配置结构体。
// 定义了资源池的管理策略和运行时配置。
type PoolConfig struct {
	// HealthCheckInterval 健康检查间隔时间
	HealthCheckInterval time.Duration `yaml:"health_check_interval"`
	// ScaleCheckInterval 扩缩容检查间隔时间
	ScaleCheckInterval time.Duration `yaml:"scale_check_interval"`
	// MaxVMAge 虚拟机最大存活时间
	MaxVMAge time.Duration `yaml:"max_vm_age"`
	// MaxInvocations 单个虚拟机的最大调用次数
	MaxInvocations int `yaml:"max_invocations"`
	// UseSnapshots 是否使用快照加速启动
	UseSnapshots bool `yaml:"use_snapshots"`
	// SnapshotWarmup 快照预热数量
	SnapshotWarmup int `yaml:"snapshot_warmup"`
	// Runtimes 各运行时的具体配置列表
	Runtimes []RuntimeConfig `yaml:"runtimes"`
}

// RuntimeConfig 单个运行时配置结构体。
// 定义了特定编程语言运行时的资源和扩缩容策略。
type RuntimeConfig struct {
	// Runtime 运行时标识符，如 "python3.9"、"nodejs18" 等
	Runtime string `yaml:"runtime"`
	// MinWarm 最小预热实例数
	MinWarm int `yaml:"min_warm"`
	// MaxTotal 最大实例总数
	MaxTotal int `yaml:"max_total"`
	// TargetWarm 目标预热实例数
	TargetWarm int `yaml:"target_warm"`
	// ScaleUpFactor 扩容因子，用于计算扩容数量
	ScaleUpFactor float64 `yaml:"scale_up_factor"`
	// ScaleDownFactor 缩容因子，用于计算缩容数量
	ScaleDownFactor float64 `yaml:"scale_down_factor"`
	// MemoryMB 实例内存大小（MB）
	MemoryMB int `yaml:"memory_mb"`
	// VCPUs 虚拟 CPU 数量
	VCPUs int `yaml:"vcpus"`
}

// SchedulerConfig 调度器配置结构体。
// 定义了函数调度和执行相关的设置。
type SchedulerConfig struct {
	// Workers 工作线程数，决定并发执行函数的能力
	// 默认值：10
	Workers int `yaml:"workers"`
	// QueueSize 请求队列大小
	// 默认值：1000
	QueueSize int `yaml:"queue_size"`
	// DefaultTimeout 函数执行默认超时时间
	// 默认值：30 秒
	DefaultTimeout time.Duration `yaml:"default_timeout"`
	// MaxRetries 失败重试最大次数
	// 默认值：3
	MaxRetries int `yaml:"max_retries"`
}

// StorageConfig 存储配置结构体。
// 包含各种数据存储后端的配置。
type StorageConfig struct {
	// Postgres PostgreSQL 数据库配置
	Postgres PostgresConfig `yaml:"postgres"`
	// Redis Redis 缓存配置
	Redis RedisConfig `yaml:"redis"`
}

// PostgresConfig PostgreSQL 数据库配置结构体。
// 定义了数据库连接的相关参数。
type PostgresConfig struct {
	// Host 数据库主机地址
	Host string `yaml:"host"`
	// Port 数据库端口号
	Port int `yaml:"port"`
	// Database 数据库名称
	Database string `yaml:"database"`
	// User 数据库用户名
	User string `yaml:"user"`
	// Password 数据库密码，可通过环境变量 FUNCTION_POSTGRES_PASSWORD 或
	// FUNCTION_POSTGRES_PASSWORD_FILE（文件路径）覆盖
	Password string `yaml:"password"`
	// MaxConnections 最大连接数
	MaxConnections int `yaml:"max_connections"`
}

// RedisConfig Redis 缓存配置结构体。
// 定义了 Redis 连接的相关参数。
type RedisConfig struct {
	// Address Redis 服务器地址，格式为 "host:port"
	Address string `yaml:"address"`
	// Password Redis 密码，可通过环境变量 FUNCTION_REDIS_PASSWORD 或
	// FUNCTION_REDIS_PASSWORD_FILE（文件路径）覆盖
	Password string `yaml:"password"`
	// DB Redis 数据库编号（0-15）
	DB int `yaml:"db"`
}

// EventsConfig 事件配置结构体。
// 定义了事件消息队列的连接信息。
type EventsConfig struct {
	// NatsURL NATS 消息服务器 URL，如 "nats://localhost:4222"
	NatsURL string `yaml:"nats_url"`
}

// LoggingConfig 日志配置结构体。
// 定义了日志输出的级别和格式。
type LoggingConfig struct {
	// Level 日志级别，可选值：debug、info、warn、error
	Level string `yaml:"level"`
	// Format 日志格式，可选值：json、text
	Format string `yaml:"format"`
}

// MetricsConfig 指标配置结构体。
// 定义了 Prometheus 指标收集的相关设置。
type MetricsConfig struct {
	// Enabled 是否启用指标收集
	Enabled bool `yaml:"enabled"`
	// Namespace 指标命名空间前缀
	Namespace string `yaml:"namespace"`
}

// TelemetryConfig 遥测配置结构体。
// 定义了分布式追踪的相关设置，支持 OpenTelemetry 协议。
type TelemetryConfig struct {
	// Enabled 是否启用遥测
	Enabled bool `yaml:"enabled"`
	// Endpoint OTLP 端点地址（如 "tempo:4317"）
	// 默认值：tempo:4317
	Endpoint string `yaml:"endpoint"`
	// ServiceName 服务名称，用于追踪标识
	// 默认值：nimbus-gateway
	ServiceName string `yaml:"service_name"`
	// SampleRate 采样率，范围 0.0 到 1.0
	// 默认值：0.1（10% 采样）
	SampleRate float64 `yaml:"sample_rate"`
	// Environment 环境标识（如 production、staging、development）
	// 默认值：development
	Environment string `yaml:"environment"`
}

// WorkflowConfig 工作流引擎配置结构体。
// 定义了工作流编排引擎的相关设置。
type WorkflowConfig struct {
	// Enabled 是否启用工作流引擎
	// 默认值：true
	Enabled bool `yaml:"enabled"`
	// Workers Worker Pool 的工作线程数
	// 默认值：10
	Workers int `yaml:"workers"`
	// QueueSize 执行队列大小
	// 默认值：1000
	QueueSize int `yaml:"queue_size"`
	// DefaultTimeout 默认执行超时时间（秒）
	// 默认值：3600
	DefaultTimeout int `yaml:"default_timeout"`
	// RecoveryEnabled 是否启用执行恢复（崩溃后重启恢复未完成的执行）
	// 默认值：true
	RecoveryEnabled bool `yaml:"recovery_enabled"`
	// RecoveryInterval 恢复检查间隔
	// 默认值：30s
	RecoveryInterval time.Duration `yaml:"recovery_interval"`
}

// SnapshotConfig 函数级快照配置结构体。
// 定义了函数快照的存储、构建和清理策略。
type SnapshotConfig struct {
	// Enabled 是否启用函数级快照
	Enabled bool `yaml:"enabled"`
	// SnapshotDir 快照存储目录
	SnapshotDir string `yaml:"snapshot_dir"`
	// BuildWorkers 构建工作协程数
	BuildWorkers int `yaml:"build_workers"`
	// BuildTimeout 构建超时时间
	BuildTimeout time.Duration `yaml:"build_timeout"`
	// WarmupOnBuild 是否在构建时执行预热调用
	WarmupOnBuild bool `yaml:"warmup_on_build"`
	// SnapshotTTL 快照 TTL（默认 7 天）
	SnapshotTTL time.Duration `yaml:"snapshot_ttl"`
	// CleanupInterval 清理间隔
	CleanupInterval time.Duration `yaml:"cleanup_interval"`
	// MaxSnapshotsPerFunction 单个函数最大快照数
	MaxSnapshotsPerFunction int `yaml:"max_snapshots_per_function"`
}

// StateConfig 有状态函数配置结构体。
// 用于配置函数状态管理功能。
type StateConfig struct {
	// Enabled 是否启用状态功能
	Enabled bool `yaml:"enabled"`
	// RedisDB 状态存储使用的 Redis DB 号（与其他功能隔离）
	RedisDB int `yaml:"redis_db"`
	// DefaultTTL 默认状态 TTL（秒），0 表示永不过期
	DefaultTTL int `yaml:"default_ttl"`
	// MaxStateSize 单个状态值的最大大小（字节）
	MaxStateSize int `yaml:"max_state_size"`
	// MaxKeysPerSession 每个会话的最大 key 数量
	MaxKeysPerSession int `yaml:"max_keys_per_session"`
	// SessionAffinityEnabled 是否启用会话亲和性
	SessionAffinityEnabled bool `yaml:"session_affinity_enabled"`
	// SessionTimeout 会话超时（秒）
	SessionTimeout int `yaml:"session_timeout"`
	// CacheTTL 本地缓存 TTL（秒）
	CacheTTL int `yaml:"cache_ttl"`
}

// Load 从指定路径加载配置文件。
// 该函数会读取 YAML 配置文件，应用默认值，并处理环境变量覆盖。
//
// 参数：
//   - path: 配置文件的路径
//
// 返回值：
//   - *Config: 加载并处理后的配置对象
//   - error: 如果读取或解析失败则返回错误
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	cfg.applyDefaults()
	cfg.applyEnvOverrides()
	return cfg, nil
}

// applyEnvOverrides 应用环境变量覆盖。
// 该方法允许通过环境变量覆盖敏感配置项，支持两种方式：
// 1. 直接设置环境变量（如 NIMBUS_POSTGRES_PASSWORD）
// 2. 通过 _FILE 后缀指定包含密钥的文件路径（如 NIMBUS_POSTGRES_PASSWORD_FILE）
// _FILE 方式优先级更高，适用于 Docker Secrets 等场景。
func (c *Config) applyEnvOverrides() {
	// 敏感配置项：支持通过 *_FILE（推荐）或直接环境变量设置

	if v := readEnvOrFileAny(
		[]string{"NIMBUS_POSTGRES_PASSWORD", "FUNCTION_POSTGRES_PASSWORD"},
		[]string{"NIMBUS_POSTGRES_PASSWORD_FILE", "FUNCTION_POSTGRES_PASSWORD_FILE"},
	); v != "" {
		c.Storage.Postgres.Password = v
	}
	if v := readEnvOrFileAny(
		[]string{"NIMBUS_REDIS_PASSWORD", "FUNCTION_REDIS_PASSWORD"},
		[]string{"NIMBUS_REDIS_PASSWORD_FILE", "FUNCTION_REDIS_PASSWORD_FILE"},
	); v != "" {
		c.Storage.Redis.Password = v
	}
	if v := readEnvOrFileAny(
		[]string{"NIMBUS_AUTH_JWT_SECRET", "FUNCTION_AUTH_JWT_SECRET"},
		[]string{"NIMBUS_AUTH_JWT_SECRET_FILE", "FUNCTION_AUTH_JWT_SECRET_FILE"},
	); v != "" {
		c.Auth.JWTSecret = v
	}
}

// readEnvOrFileAny 从环境变量或文件读取配置值。
// 优先从 fileKeys 指定的文件路径读取，如果文件不存在或读取失败，
// 则从 envKeys 指定的环境变量读取。
//
// 参数：
//   - envKeys: 直接存储值的环境变量名（按优先级从高到低）
//   - fileKeys: 存储文件路径的环境变量名（按优先级从高到低）
//
// 返回值：
//   - string: 读取到的配置值，如果都未设置则返回空字符串
func readEnvOrFileAny(envKeys []string, fileKeys []string) string {
	for _, fileKey := range fileKeys {
		if filePath := strings.TrimSpace(os.Getenv(fileKey)); filePath != "" {
			if b, err := os.ReadFile(filePath); err == nil {
				return strings.TrimSpace(string(b))
			}
		}
	}

	for _, envKey := range envKeys {
		if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
			return v
		}
	}

	return ""
}

// applyDefaults 应用默认配置值。
// 该方法为未设置的配置项填充合理的默认值，确保应用可以正常运行。
func (c *Config) applyDefaults() {
	// 运行时模式默认为 docker
	if c.Runtime.Mode == "" {
		c.Runtime.Mode = "docker"
	}
	// Docker 网络模式默认为 none（最安全）
	if c.Docker.NetworkMode == "" {
		c.Docker.NetworkMode = "none"
	}
	// Docker 容器池最大数量默认与调度器工作线程数相同
	if c.Docker.Pool.MaxTotal == 0 {
		c.Docker.Pool.MaxTotal = c.Scheduler.Workers
	}
	// 如果仍为 0，则设置为 10
	if c.Docker.Pool.MaxTotal <= 0 {
		c.Docker.Pool.MaxTotal = 10
	}
	// 最小预热数量不能为负数
	if c.Docker.Pool.MinWarm < 0 {
		c.Docker.Pool.MinWarm = 0
	}
	// 最小预热数量不能超过最大总数
	if c.Docker.Pool.MinWarm > c.Docker.Pool.MaxTotal {
		c.Docker.Pool.MinWarm = c.Docker.Pool.MaxTotal
	}
	// 容器最大调用次数默认为 1000
	if c.Docker.Pool.MaxInvocations == 0 {
		c.Docker.Pool.MaxInvocations = 1000
	}
	// 容器最大存活时间默认为 1 小时
	if c.Docker.Pool.MaxContainerAge == 0 {
		c.Docker.Pool.MaxContainerAge = time.Hour
	}
	// tmpfs 大小默认为 64 MB
	if c.Docker.Pool.TmpfsSizeMB == 0 {
		c.Docker.Pool.TmpfsSizeMB = 64
	}
	// HTTP 端口默认为 8080
	if c.Server.HTTPPort == 0 {
		c.Server.HTTPPort = 8080
	}
	// 调用端口默认为 8081
	if c.Server.InvokePort == 0 {
		c.Server.InvokePort = 8081
	}
	// 指标端口默认为 9090
	if c.Server.MetricsPort == 0 {
		c.Server.MetricsPort = 9090
	}
	// 优雅关闭超时默认为 30 秒
	if c.Server.ShutdownTimeout == 0 {
		c.Server.ShutdownTimeout = 30 * time.Second
	}
	// Firecracker 启动超时默认为 10 秒
	if c.Firecracker.BootTimeout == 0 {
		c.Firecracker.BootTimeout = 10 * time.Second
	}
	// 调度器工作线程数默认为 10
	if c.Scheduler.Workers == 0 {
		c.Scheduler.Workers = 10
	}
	// 调度器队列大小默认为 1000
	if c.Scheduler.QueueSize == 0 {
		c.Scheduler.QueueSize = 1000
	}
	// 函数执行默认超时为 30 秒
	if c.Scheduler.DefaultTimeout == 0 {
		c.Scheduler.DefaultTimeout = 30 * time.Second
	}
	// 最大重试次数默认为 3
	if c.Scheduler.MaxRetries == 0 {
		c.Scheduler.MaxRetries = 3
	}
	// JWT 过期时间默认为 24 小时
	if c.Auth.JWTExpiration == 0 {
		c.Auth.JWTExpiration = 24 * time.Hour
	}
	// API Key 请求头默认为 X-API-Key
	if c.Auth.APIKeyHeader == "" {
		c.Auth.APIKeyHeader = "X-API-Key"
	}
	// 遥测服务名称默认为 nimbus-gateway
	if c.Telemetry.ServiceName == "" {
		c.Telemetry.ServiceName = "nimbus-gateway"
	}
	// OTLP 端点默认为 tempo:4317
	if c.Telemetry.Endpoint == "" {
		c.Telemetry.Endpoint = "tempo:4317"
	}
	// 采样率默认为 10%
	if c.Telemetry.SampleRate == 0 {
		c.Telemetry.SampleRate = 0.1
	}
	// 环境标识默认为 development
	if c.Telemetry.Environment == "" {
		c.Telemetry.Environment = "development"
	}
	// 工作流引擎默认启用
	// 注意：Enabled 为 false 时工作流功能被禁用，这里只设置其他默认值
	// Workers 默认为 10
	if c.Workflow.Workers == 0 {
		c.Workflow.Workers = 10
	}
	// QueueSize 默认为 1000
	if c.Workflow.QueueSize == 0 {
		c.Workflow.QueueSize = 1000
	}
	// DefaultTimeout 默认为 3600 秒（1 小时）
	if c.Workflow.DefaultTimeout == 0 {
		c.Workflow.DefaultTimeout = 3600
	}
	// RecoveryInterval 默认为 30 秒
	if c.Workflow.RecoveryInterval == 0 {
		c.Workflow.RecoveryInterval = 30 * time.Second
	}
	// 快照配置默认值
	if c.Snapshot.SnapshotDir == "" {
		c.Snapshot.SnapshotDir = "/var/nimbus/snapshots"
	}
	if c.Snapshot.BuildWorkers == 0 {
		c.Snapshot.BuildWorkers = 2
	}
	if c.Snapshot.BuildTimeout == 0 {
		c.Snapshot.BuildTimeout = 60 * time.Second
	}
	if c.Snapshot.SnapshotTTL == 0 {
		c.Snapshot.SnapshotTTL = 168 * time.Hour // 7 days
	}
	if c.Snapshot.CleanupInterval == 0 {
		c.Snapshot.CleanupInterval = time.Hour
	}
	if c.Snapshot.MaxSnapshotsPerFunction == 0 {
		c.Snapshot.MaxSnapshotsPerFunction = 3
	}
}
