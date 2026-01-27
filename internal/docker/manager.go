// Package docker 提供 Docker 容器管理功能，用于函数执行环境的创建和管理。
// 该包实现了基于 Docker 容器的函数执行器，支持容器池化以减少冷启动时间。
package docker

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oriys/nimbus/internal/config"
	"github.com/oriys/nimbus/internal/domain"
	"github.com/oriys/nimbus/internal/metrics"
	"github.com/sirupsen/logrus"
)

// 容器标签常量，用于标识和管理由本管理器创建的容器。
// 这些标签在以下场景中使用：
//   - 服务启动时清理上次运行遗留的陈旧容器
//   - 区分本平台创建的容器与其他 Docker 容器
//   - 通过 docker ps --filter 快速查询托管容器
const (
	// managedLabelKey 是容器标签的键名
	managedLabelKey = "function.managed"
	// managedLabelValue 是容器标签的值，"1" 表示由本管理器托管
	managedLabelValue = "1"
	// layerCacheDir 是层缓存目录的默认路径
	layerCacheDir = "/tmp/nimbus-layers"
)

// Manager 是 Docker 容器管理器，负责管理用于函数执行的 Docker 容器。
// 支持两种执行模式：一次性容器模式和池化容器模式。
// 池化模式可以复用容器，减少冷启动时间。
type Manager struct {
	mu          sync.RWMutex              // 保护并发访问的读写锁
	images      map[string]string         // 运行时名称到镜像名称的映射，如 "python3.11" -> "function-runtime-python:latest"
	execCmd     map[string][]string       // 运行时名称到执行命令的映射
	networkMode string                    // Docker 网络模式，默认为 "none" 以增强安全性
	poolCfg     config.DockerPoolConfig   // 容器池配置
	pools       map[string]*containerPool // 容器池映射，键为 "运行时:内存" 格式
	metrics     *metrics.Metrics          // 指标收集器
	logger      *logrus.Logger            // 日志记录器
	bufferPool  sync.Pool                 // 复用 bytes.Buffer，减少热路径分配
}

// pooledContainer 表示池中的一个容器实例。
// 包含容器的元数据和状态信息。
type pooledContainer struct {
	ID        string    // Docker 容器 ID
	Runtime   string    // 运行时类型（如 python3.11, nodejs20）
	MemoryMB  int       // 分配的内存大小（MB）
	CreatedAt time.Time // 容器创建时间
	LastUsed  time.Time // 最后使用时间
	UseCount  int       // 使用次数计数
	Status    string    // 容器状态：warm（预热）或 busy（忙碌）
}

// containerPool 表示特定运行时和内存配置的容器池。
// 管理一组可复用的预热容器。
type containerPool struct {
	runtime  string // 运行时类型
	memoryMB int    // 内存配置（MB）

	warm chan *pooledContainer // 预热容器的缓冲通道

	mu       sync.Mutex                  // 保护 all 和 creating 的互斥锁
	all      map[string]*pooledContainer // 所有容器的映射（包括预热和忙碌状态）
	creating int                         // 正在创建中的容器数量
}

// poolKey 生成容器池的唯一键。
// 格式为 "运行时:内存MB"，如 "python3.11:128"
func poolKey(runtime string, memoryMB int) string {
	return runtime + ":" + strconv.Itoa(memoryMB)
}

// NewManager 创建新的 Docker 容器管理器。
// 参数：
//   - cfg: Docker 配置，包含镜像映射和池配置
//   - m: 指标收集器，可为 nil
//   - logger: 日志记录器
//
// 返回配置好的 Manager 实例
func NewManager(cfg config.DockerConfig, m *metrics.Metrics, logger *logrus.Logger) *Manager {
	// 设置默认网络模式为 "none"，禁用网络访问以增强安全性
	networkMode := cfg.NetworkMode
	if networkMode == "" {
		networkMode = "none"
	}

	// 初始化默认运行时镜像映射
	images := map[string]string{
		"python3.11": "function-runtime-python:latest",
		"nodejs20":   "function-runtime-nodejs:latest",
		"go1.24":     "function-runtime-go:latest",
		"rust1.75":   "function-runtime-go:latest",
		"wasm":       "function-runtime-wasm:latest",
	}
	// 用配置中的自定义镜像覆盖默认值
	for runtime, image := range cfg.Images {
		image = strings.TrimSpace(image)
		if image != "" {
			images[runtime] = image
		}
	}

	mgr := &Manager{
		images: images,
		// 各运行时的执行命令配置
		execCmd: map[string][]string{
			"python3.11": {"python3", "/app/runtime.py"},
			"nodejs20":   {"node", "/app/runtime.js"},
			"go1.24":     {"/app/runtime"},
			"rust1.75":   {"/app/runtime"},
			"wasm":       {"/app/runtime"},
		},
		networkMode: networkMode,
		poolCfg:     cfg.Pool,
		pools:       make(map[string]*containerPool),
		metrics:     m,
		logger:      logger,
		bufferPool: sync.Pool{
			New: func() interface{} {
				return bytes.NewBuffer(make([]byte, 0, 4096)) // 预分配 4KB
			},
		},
	}

	// 如果启用了容器池，尝试清理之前运行遗留的陈旧容器
	if mgr.poolCfg.Enabled {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := mgr.cleanupStaleContainers(ctx); err != nil {
			logger.WithError(err).Warn("Failed to cleanup stale docker containers")
		}
	}

	return mgr
}

// cleanupStaleContainers 清理之前运行遗留的陈旧容器。
// 通过标签查找并强制删除所有由本管理器创建的容器。
func (m *Manager) cleanupStaleContainers(ctx context.Context) error {
	// 使用标签过滤查找所有由本管理器创建的容器
	cmd := exec.CommandContext(
		ctx,
		"docker",
		"ps",
		"-aq",
		"--filter",
		fmt.Sprintf("label=%s=%s", managedLabelKey, managedLabelValue),
	)
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	// 逐个删除找到的容器
	ids := strings.Fields(string(out))
	for _, id := range ids {
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", id).Run()
	}
	return nil
}

// Execute 在 Docker 容器中执行函数。
// 实现 executor.Executor 接口。
// 根据配置选择一次性执行模式或池化执行模式。
// 参数：
//   - ctx: 上下文，用于超时和取消控制
//   - fn: 要执行的函数定义
//   - payload: 函数输入参数（JSON 格式）
//
// 返回：
//   - *domain.InvokeResponse: 执行结果，包含输出、状态码和执行时间等
//   - error: 执行过程中的错误
func (m *Manager) Execute(ctx context.Context, fn *domain.Function, payload json.RawMessage) (*domain.InvokeResponse, error) {
	if !m.poolCfg.Enabled {
		return m.executeOneOff(ctx, fn, payload, nil)
	}
	return m.executePooled(ctx, fn, payload, nil)
}

// ExecuteWithLayers 在 Docker 容器中执行函数，支持加载函数层。
// 参数：
//   - ctx: 上下文，用于超时和取消控制
//   - fn: 要执行的函数定义
//   - payload: 函数输入参数（JSON 格式）
//   - layers: 函数层列表
//
// 返回：
//   - *domain.InvokeResponse: 执行结果，包含输出、状态码和执行时间等
//   - error: 执行过程中的错误
func (m *Manager) ExecuteWithLayers(ctx context.Context, fn *domain.Function, payload json.RawMessage, layers []domain.RuntimeLayerInfo) (*domain.InvokeResponse, error) {
	if !m.poolCfg.Enabled {
		return m.executeOneOff(ctx, fn, payload, layers)
	}
	return m.executePooled(ctx, fn, payload, layers)
}

// executeOneOff 使用一次性容器执行函数。
// 每次调用都会创建新容器，执行完成后自动删除。
// 适用于不需要频繁调用或需要完全隔离的场景。
func (m *Manager) executeOneOff(ctx context.Context, fn *domain.Function, payload json.RawMessage, layers []domain.RuntimeLayerInfo) (*domain.InvokeResponse, error) {
	startTime := time.Now()

	// 获取运行时对应的 Docker 镜像
	image, ok := m.images[string(fn.Runtime)]
	if !ok {
		return nil, fmt.Errorf("unsupported runtime: %s", fn.Runtime)
	}

	envVars := fn.EnvVars
	if envVars == nil {
		envVars = map[string]string{}
	}

	// 优先使用编译后的二进制，如果不存在则使用源代码
	code := fn.Code
	if fn.Binary != "" {
		code = fn.Binary
	}

	// 准备函数代码和输入数据
	input := map[string]interface{}{
		"handler": fn.Handler,
		"code":    code,
		"payload": json.RawMessage(payload),
		"env":     envVars,
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	// 设置层并获取卷挂载和环境变量
	volumeMounts, layerEnvVars, err := m.setupLayers(layers, string(fn.Runtime))
	if err != nil {
		return nil, fmt.Errorf("failed to setup layers: %w", err)
	}

	// 构建 docker run 命令参数
	args := []string{
		"run", "--rm", // 运行后自动删除容器
		"--network", m.networkMode, // 网络模式
	}
	// 仅在未禁用资源限制时添加 --memory 和 --cpus
	// 在 Docker-in-Docker 环境中使用 cgroup v2 时可能需要禁用
	if !m.poolCfg.DisableResourceLimits {
		args = append(args, "--memory", fmt.Sprintf("%dm", fn.MemoryMB))
		args = append(args, "--cpus", "1")
	}

	// 添加层卷挂载
	for _, mount := range volumeMounts {
		args = append(args, "-v", mount)
	}

	// 添加层环境变量
	for key, value := range layerEnvVars {
		args = append(args, "-e", fmt.Sprintf("%s=%s", key, value))
	}

	args = append(args,
		"--read-only",                                                                 // 只读文件系统
		"--tmpfs", fmt.Sprintf("/tmp:rw,exec,nosuid,size=%dm", m.poolCfg.TmpfsSizeMB), // 临时文件系统
		"--security-opt", "no-new-privileges", // 安全选项：禁止提升权限
		"-i", // 交互模式（用于传入输入数据）
		image,
	)

	// 创建带超时的上下文
	cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(fn.TimeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "docker", args...)
	cmd.Stdin = bytes.NewReader(inputJSON)

	// 从池中获取缓冲区，减少热路径内存分配
	stdout := m.bufferPool.Get().(*bytes.Buffer)
	stderr := m.bufferPool.Get().(*bytes.Buffer)
	stdout.Reset()
	stderr.Reset()
	defer func() {
		m.bufferPool.Put(stdout)
		m.bufferPool.Put(stderr)
	}()

	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Run()
	duration := time.Since(startTime)

	// 记录详细的执行日志，方便调试
	m.logger.WithFields(logrus.Fields{
		"function_id":   fn.ID,
		"function_name": fn.Name,
		"runtime":       fn.Runtime,
		"duration_ms":   duration.Milliseconds(),
		"stdout_len":    len(stdout.Bytes()),
		"stderr_len":    len(stderr.Bytes()),
		"stdout":        truncateForError(stdout.Bytes(), 500),
		"stderr":        truncateForError(stderr.Bytes(), 500),
		"error":         err,
	}).Debug("Docker run completed")

	// 构建响应对象
	resp := &domain.InvokeResponse{
		DurationMs:   duration.Milliseconds(),
		BilledTimeMs: ((duration.Milliseconds() + 99) / 100) * 100, // 向上取整到 100ms（计费单位）
		ColdStart:    true,                                         // 一次性容器始终是冷启动
	}

	if err != nil {
		// 记录失败的详细信息
		m.logger.WithFields(logrus.Fields{
			"function_id":   fn.ID,
			"function_name": fn.Name,
			"runtime":       fn.Runtime,
			"error":         err,
			"stdout":        truncateForError(stdout.Bytes(), 1024),
			"stderr":        truncateForError(stderr.Bytes(), 1024),
		}).Error("Function execution failed in one-off container")

		// 区分超时和其他错误
		if cmdCtx.Err() == context.DeadlineExceeded {
			resp.StatusCode = 504
			resp.Error = "function timed out"
		} else {
			resp.StatusCode = 500
			// 优先使用 stderr 内容，如果为空则使用 stdout 或 err 信息
			stderrStr := strings.TrimSpace(stderr.String())
			stdoutStr := strings.TrimSpace(stdout.String())
			if stderrStr != "" {
				resp.Error = fmt.Sprintf("execution failed: %s", stderrStr)
			} else if stdoutStr != "" {
				resp.Error = fmt.Sprintf("execution failed (stdout): %s", truncateForError([]byte(stdoutStr), 512))
			} else {
				resp.Error = fmt.Sprintf("execution failed: %v", err)
			}
		}
		return resp, nil
	}

	// 解析输出
	body, ok := extractJSONFromStdout(stdout.Bytes())
	if !ok {
		// 非 JSON 输出，包装成 JSON 格式返回
		output := strings.TrimSpace(string(stdout.Bytes()))
		wrapped := map[string]string{"output": output}
		wrappedJSON, _ := json.Marshal(wrapped)
		resp.Body = wrappedJSON
		resp.StatusCode = 200
	} else {
		resp.Body = body
		resp.StatusCode = 200
	}

	m.logger.WithFields(logrus.Fields{
		"function_id": fn.ID,
		"runtime":     fn.Runtime,
		"duration_ms": duration.Milliseconds(),
	}).Info("Function executed in Docker")

	return resp, nil
}

// executePooled 使用池化容器执行函数。
// 从容器池获取预热容器执行，执行完成后归还到池中复用。
// 可以显著减少冷启动时间。
func (m *Manager) executePooled(ctx context.Context, fn *domain.Function, payload json.RawMessage, layers []domain.RuntimeLayerInfo) (*domain.InvokeResponse, error) {
	startTime := time.Now()

	// 获取运行时对应的镜像和执行命令
	image, ok := m.images[string(fn.Runtime)]
	if !ok {
		return nil, fmt.Errorf("unsupported runtime: %s", fn.Runtime)
	}
	execCmd, ok := m.execCmd[string(fn.Runtime)]
	if !ok {
		return nil, fmt.Errorf("unsupported runtime exec command: %s", fn.Runtime)
	}

	envVars := fn.EnvVars
	if envVars == nil {
		envVars = map[string]string{}
	}

	// 设置层并获取卷挂载和环境变量
	_, layerEnvVars, err := m.setupLayers(layers, string(fn.Runtime))
	if err != nil {
		return nil, fmt.Errorf("failed to setup layers: %w", err)
	}

	// 合并层环境变量到函数环境变量
	for key, value := range layerEnvVars {
		envVars[key] = value
	}

	// 优先使用编译后的二进制，如果不存在则使用源代码
	code := fn.Code
	if fn.Binary != "" {
		code = fn.Binary
	}

	// 准备函数代码和输入数据
	input := map[string]interface{}{
		"handler": fn.Handler,
		"code":    code,
		"payload": json.RawMessage(payload),
		"env":     envVars,
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	// 创建带超时的上下文
	cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(fn.TimeoutSec)*time.Second)
	defer cancel()

	// 从池中获取容器
	pc, coldStart, err := m.acquireContainer(cmdCtx, string(fn.Runtime), fn.MemoryMB, image)
	if err != nil {
		return nil, err
	}

	// 记录容器是否健康，用于决定是否归还到池中
	healthy := true
	defer func() {
		if err := m.releaseContainer(context.Background(), pc, healthy); err != nil {
			m.logger.WithError(err).WithField("container_id", pc.ID).Warn("Failed to release docker container")
		}
	}()

	// 使用 docker exec 在已运行的容器中执行函数
	args := []string{"exec", "-i", pc.ID}
	args = append(args, execCmd...)

	cmd := exec.CommandContext(cmdCtx, "docker", args...)
	cmd.Stdin = bytes.NewReader(inputJSON)

	// 从池中获取缓冲区，减少热路径内存分配
	stdout := m.bufferPool.Get().(*bytes.Buffer)
	stderr := m.bufferPool.Get().(*bytes.Buffer)
	stdout.Reset()
	stderr.Reset()
	defer func() {
		m.bufferPool.Put(stdout)
		m.bufferPool.Put(stderr)
	}()

	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()
	duration := time.Since(startTime)

	// 记录详细的执行日志，方便调试
	m.logger.WithFields(logrus.Fields{
		"function_id":   fn.ID,
		"function_name": fn.Name,
		"container_id":  pc.ID,
		"cold_start":    coldStart,
		"duration_ms":   duration.Milliseconds(),
		"stdout_len":    len(stdout.Bytes()),
		"stderr_len":    len(stderr.Bytes()),
		"stdout":        truncateForError(stdout.Bytes(), 500),
		"stderr":        truncateForError(stderr.Bytes(), 500),
		"error":         runErr,
	}).Debug("Docker exec completed")

	// 构建响应对象
	resp := &domain.InvokeResponse{
		DurationMs:   duration.Milliseconds(),
		BilledTimeMs: ((duration.Milliseconds() + 99) / 100) * 100, // 向上取整到 100ms
		ColdStart:    coldStart,
	}

	if runErr != nil {
		// 记录失败的详细信息
		m.logger.WithFields(logrus.Fields{
			"function_id":   fn.ID,
			"function_name": fn.Name,
			"container_id":  pc.ID,
			"error":         runErr,
			"stdout":        truncateForError(stdout.Bytes(), 1024),
			"stderr":        truncateForError(stderr.Bytes(), 1024),
		}).Error("Function execution failed in pooled container")

		if cmdCtx.Err() == context.DeadlineExceeded {
			resp.StatusCode = 504
			resp.Error = "function timed out"
			healthy = false // 超时后容器可能有残留进程，标记为不健康
		} else {
			resp.StatusCode = 500
			// 优先使用 stderr 内容，如果为空则使用 stdout 或 err 信息
			stderrStr := strings.TrimSpace(stderr.String())
			stdoutStr := strings.TrimSpace(stdout.String())
			if stderrStr != "" {
				resp.Error = fmt.Sprintf("execution failed: %s", stderrStr)
			} else if stdoutStr != "" {
				resp.Error = fmt.Sprintf("execution failed (stdout): %s", truncateForError([]byte(stdoutStr), 512))
			} else {
				resp.Error = fmt.Sprintf("execution failed: %v", runErr)
			}
		}
		return resp, nil
	}

	// 解析输出
	body, ok := extractJSONFromStdout(stdout.Bytes())
	if !ok {
		// 非 JSON 输出，包装成 JSON 格式返回
		output := strings.TrimSpace(string(stdout.Bytes()))
		wrapped := map[string]string{"output": output}
		wrappedJSON, _ := json.Marshal(wrapped)
		resp.Body = wrappedJSON
		resp.StatusCode = 200
	} else {
		resp.Body = body
		resp.StatusCode = 200
	}

	m.logger.WithFields(logrus.Fields{
		"function_id": fn.ID,
		"runtime":     fn.Runtime,
		"duration_ms": duration.Milliseconds(),
		"cold_start":  coldStart,
	}).Info("Function executed in pooled Docker container")

	return resp, nil
}

func extractJSONFromStdout(stdout []byte) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return nil, true
	}

	if json.Valid(trimmed) {
		return append([]byte(nil), trimmed...), true
	}

	// Some user code prints logs to stdout; try to recover by taking the last
	// non-empty line that is valid JSON.
	lines := bytes.Split(trimmed, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		if json.Valid(line) {
			return append([]byte(nil), line...), true
		}
	}

	return nil, false
}

func truncateForError(b []byte, max int) string {
	b = bytes.TrimSpace(b)
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "...(truncated)"
}

// getPool 获取或创建指定运行时和内存配置的容器池。
// 线程安全。
func (m *Manager) getPool(runtime string, memoryMB int) *containerPool {
	key := poolKey(runtime, memoryMB)
	m.mu.Lock()
	defer m.mu.Unlock()

	// 如果池已存在，直接返回
	if p, ok := m.pools[key]; ok {
		return p
	}

	// 创建新的容器池
	p := &containerPool{
		runtime:  runtime,
		memoryMB: memoryMB,
		warm:     make(chan *pooledContainer, m.poolCfg.MaxTotal), // 预热容器缓冲通道
		all:      make(map[string]*pooledContainer),
	}
	m.pools[key] = p
	return p
}

// acquireContainer 从池中获取一个容器。
// 优先获取预热容器（热启动），如果没有则创建新容器（冷启动）。
// 返回：
//   - *pooledContainer: 获取到的容器
//   - bool: 是否为冷启动
//   - error: 错误信息
func (m *Manager) acquireContainer(ctx context.Context, runtime string, memoryMB int, image string) (*pooledContainer, bool, error) {
	pool := m.getPool(runtime, memoryMB)

	// 快速路径：尝试获取预热容器
	select {
	case pc := <-pool.warm:
		pc.Status = "busy"
		pc.LastUsed = time.Now()
		pc.UseCount++
		m.updatePoolMetrics(runtime)
		return pc, false, nil // false 表示热启动
	default:
		// 没有预热容器可用
	}

	// 检查是否可以创建新容器（未达到上限）
	pool.mu.Lock()
	canCreate := len(pool.all)+pool.creating < m.poolCfg.MaxTotal
	if canCreate {
		pool.creating++ // 增加正在创建计数，防止并发创建超出限制
	}
	pool.mu.Unlock()

	if canCreate {
		// 创建新容器（冷启动）
		pc, err := m.createContainer(ctx, runtime, memoryMB, image)
		pool.mu.Lock()
		pool.creating--
		if err == nil {
			pc.Status = "busy"
			pc.LastUsed = time.Now()
			pc.UseCount = 1
			pool.all[pc.ID] = pc
		}
		pool.mu.Unlock()
		if err != nil {
			return nil, false, err
		}
		m.updatePoolMetrics(runtime)
		return pc, true, nil // true 表示冷启动
	}

	// 池已满：等待预热容器变为可用
	select {
	case pc := <-pool.warm:
		pc.Status = "busy"
		pc.LastUsed = time.Now()
		pc.UseCount++
		m.updatePoolMetrics(runtime)
		return pc, false, nil
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
}

// createContainer 创建一个新的 Docker 容器。
// 容器创建后会启动并保持运行（使用 tail -f /dev/null）。
func (m *Manager) createContainer(ctx context.Context, runtime string, memoryMB int, image string) (*pooledContainer, error) {
	// 保持容器运行的命令
	keepalive := "tail -f /dev/null"

	// 确保层缓存目录存在
	if err := os.MkdirAll(layerCacheDir, 0755); err != nil {
		m.logger.WithError(err).Warn("Failed to create layer cache directory")
	}

	// 构建容器创建参数
	args := []string{
		"create",
		// 添加标签用于识别和清理
		"--label", fmt.Sprintf("%s=%s", managedLabelKey, managedLabelValue),
		"--label", fmt.Sprintf("function.runtime=%s", runtime),
		"--label", fmt.Sprintf("function.memory_mb=%d", memoryMB),
		"--network", m.networkMode, // 网络模式
		// 挂载层缓存目录（只读）
		"-v", fmt.Sprintf("%s:/opt/layers:ro", layerCacheDir),
	}
	// 仅在未禁用资源限制时添加 --memory 和 --cpus
	// 在 Docker-in-Docker 环境中使用 cgroup v2 时可能需要禁用
	if !m.poolCfg.DisableResourceLimits {
		args = append(args, "--memory", fmt.Sprintf("%dm", memoryMB))
		args = append(args, "--cpus", "1")
	}
	args = append(args,
		"--read-only",                                                                 // 只读文件系统
		"--tmpfs", fmt.Sprintf("/tmp:rw,exec,nosuid,size=%dm", m.poolCfg.TmpfsSizeMB), // 临时文件系统
		"--security-opt", "no-new-privileges", // 安全选项
		"--entrypoint", "/bin/sh", // 入口点
		image,
		"-c", keepalive, // 保持容器运行
	)

	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return nil, fmt.Errorf("docker create returned empty container id")
	}

	// 启动容器
	startCmd := exec.CommandContext(ctx, "docker", "start", id)
	if err := startCmd.Run(); err != nil {
		// 启动失败，清理创建的容器
		_ = exec.CommandContext(context.Background(), "docker", "rm", "-f", id).Run()
		return nil, err
	}

	now := time.Now()
	return &pooledContainer{
		ID:        id,
		Runtime:   runtime,
		MemoryMB:  memoryMB,
		CreatedAt: now,
		LastUsed:  now,
		Status:    "warm",
	}, nil
}

// releaseContainer 释放容器回池中或销毁。
// 根据容器的使用次数和存活时间决定是回收还是销毁。
// 参数：
//   - ctx: 上下文
//   - pc: 要释放的容器
//   - healthy: 容器是否健康（如果不健康则直接销毁）
func (m *Manager) releaseContainer(ctx context.Context, pc *pooledContainer, healthy bool) error {
	pool := m.getPool(pc.Runtime, pc.MemoryMB)

	// 决定是否需要销毁容器：
	// 1. 容器不健康
	// 2. 使用次数超过限制
	// 3. 存活时间超过限制
	if !healthy || pc.UseCount >= m.poolCfg.MaxInvocations || time.Since(pc.CreatedAt) > m.poolCfg.MaxContainerAge {
		pool.mu.Lock()
		delete(pool.all, pc.ID)
		pool.mu.Unlock()
		m.updatePoolMetrics(pc.Runtime)
		return exec.CommandContext(ctx, "docker", "rm", "-f", pc.ID).Run()
	}

	// 将容器标记为预热状态
	pc.Status = "warm"
	pool.mu.Lock()
	pool.mu.Unlock()

	// 尝试将容器放回预热队列
	select {
	case pool.warm <- pc:
		m.updatePoolMetrics(pc.Runtime)
		return nil
	default:
		// 预热队列已满：销毁容器
		pool.mu.Lock()
		delete(pool.all, pc.ID)
		pool.mu.Unlock()
		m.updatePoolMetrics(pc.Runtime)
		return exec.CommandContext(ctx, "docker", "rm", "-f", pc.ID).Run()
	}
}

// updatePoolMetrics 更新容器池的 Prometheus 指标。
// 统计指定运行时的预热、忙碌和总容器数。
func (m *Manager) updatePoolMetrics(runtime string) {
	if m.metrics == nil {
		return
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	warm := 0
	total := 0
	// 遍历所有相关的池，统计容器数量
	for _, pool := range m.pools {
		if pool.runtime != runtime {
			continue
		}
		warm += len(pool.warm)
		pool.mu.Lock()
		total += len(pool.all)
		pool.mu.Unlock()
	}
	busy := total - warm
	if busy < 0 {
		busy = 0
	}
	m.metrics.UpdatePoolStats(runtime, warm, busy, total)
}

// BuildImages 构建所有运行时的 Docker 镜像。
// 参数：
//   - ctx: 上下文
//   - dockerfilesDir: 包含 Dockerfile 的目录
//
// 期望的 Dockerfile 命名格式为 Dockerfile.<runtime>，如 Dockerfile.python3.11
func (m *Manager) BuildImages(ctx context.Context, dockerfilesDir string) error {
	for runtime, image := range m.images {
		dockerfile := fmt.Sprintf("%s/Dockerfile.%s", dockerfilesDir, runtime)
		m.logger.Infof("Building image %s from %s", image, dockerfile)

		cmd := exec.CommandContext(ctx, "docker", "build", "-t", image, "-f", dockerfile, dockerfilesDir)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to build %s: %w\n%s", image, err, output)
		}
		m.logger.Infof("Built image: %s", image)
	}
	return nil
}

// setupLayers 设置函数层，返回卷挂载列表和环境变量。
// 该函数会将层内容解压到主机缓存目录，并返回：
//   - volumeMounts: Docker -v 参数格式的卷挂载列表
//   - envVars: 需要设置的环境变量（如 PYTHONPATH、NODE_PATH）
//   - error: 设置过程中的错误
func (m *Manager) setupLayers(layers []domain.RuntimeLayerInfo, runtime string) ([]string, map[string]string, error) {
	if len(layers) == 0 {
		return nil, nil, nil
	}

	// 确保缓存目录存在
	if err := os.MkdirAll(layerCacheDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create layer cache directory: %w", err)
	}

	var volumeMounts []string
	var pythonPaths, nodePaths []string

	for _, layer := range layers {
		// 使用内容哈希作为缓存键，避免重复解压
		cacheKey := m.layerCacheKey(layer.LayerID, layer.Version, layer.Content)
		layerDir := filepath.Join(layerCacheDir, cacheKey)

		// 检查是否已缓存
		if _, err := os.Stat(layerDir); os.IsNotExist(err) {
			// 解压层内容
			if err := m.extractZipToDir(layer.Content, layerDir); err != nil {
				return nil, nil, fmt.Errorf("failed to extract layer %s: %w", layer.LayerID, err)
			}
			m.logger.WithFields(logrus.Fields{
				"layer_id": layer.LayerID,
				"version":  layer.Version,
				"path":     layerDir,
			}).Debug("Layer extracted to cache")
		} else {
			m.logger.WithFields(logrus.Fields{
				"layer_id": layer.LayerID,
				"version":  layer.Version,
			}).Debug("Layer found in cache")
		}

		// 添加卷挂载
		containerPath := fmt.Sprintf("/opt/layers/%s", layer.LayerID)
		volumeMounts = append(volumeMounts, fmt.Sprintf("%s:%s:ro", layerDir, containerPath))

		// 根据运行时构建路径
		switch runtime {
		case "python3.11":
			pythonPaths = append(pythonPaths,
				filepath.Join(containerPath, "python"),
				filepath.Join(containerPath, "python", "lib", "python3.11", "site-packages"),
			)
		case "nodejs20":
			nodePaths = append(nodePaths,
				filepath.Join(containerPath, "nodejs", "node_modules"),
			)
		}
	}

	// 构建环境变量
	envVars := make(map[string]string)
	if len(pythonPaths) > 0 {
		envVars["PYTHONPATH"] = strings.Join(pythonPaths, ":")
	}
	if len(nodePaths) > 0 {
		envVars["NODE_PATH"] = strings.Join(nodePaths, ":")
	}

	return volumeMounts, envVars, nil
}

// layerCacheKey 生成层的缓存键。
// 使用 LayerID、版本号和内容哈希组合，确保唯一性。
func (m *Manager) layerCacheKey(layerID string, version int, content []byte) string {
	hash := sha256.Sum256(content)
	hashStr := hex.EncodeToString(hash[:8]) // 使用前8字节
	return fmt.Sprintf("%s-v%d-%s", layerID, version, hashStr)
}

// extractZipToDir 将 ZIP 内容解压到目标目录。
func (m *Manager) extractZipToDir(content []byte, destDir string) error {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return fmt.Errorf("failed to create zip reader: %w", err)
	}

	// 创建临时目录用于解压，成功后再重命名
	tempDir := destDir + ".tmp"
	if err := os.RemoveAll(tempDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to clean temp directory: %w", err)
	}
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}

	for _, file := range reader.File {
		path := filepath.Join(tempDir, file.Name)

		// 防止路径遍历攻击
		cleanPath := filepath.Clean(path)
		cleanDest := filepath.Clean(tempDir)
		if !strings.HasPrefix(cleanPath, cleanDest+string(os.PathSeparator)) && cleanPath != cleanDest {
			m.logger.WithField("path", file.Name).Warn("Skipping potentially unsafe path in layer zip")
			continue
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(path, file.Mode()); err != nil {
				os.RemoveAll(tempDir)
				return fmt.Errorf("failed to create directory %s: %w", path, err)
			}
			continue
		}

		// 确保父目录存在
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			os.RemoveAll(tempDir)
			return fmt.Errorf("failed to create parent directory for %s: %w", path, err)
		}

		// 解压文件
		outFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			os.RemoveAll(tempDir)
			return fmt.Errorf("failed to create file %s: %w", path, err)
		}

		rc, err := file.Open()
		if err != nil {
			outFile.Close()
			os.RemoveAll(tempDir)
			return fmt.Errorf("failed to open zip entry %s: %w", file.Name, err)
		}

		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()

		if err != nil {
			os.RemoveAll(tempDir)
			return fmt.Errorf("failed to write file %s: %w", path, err)
		}
	}

	// 原子性地重命名目录
	if err := os.Rename(tempDir, destDir); err != nil {
		// 如果目标目录已存在（可能是并发创建），则清理临时目录
		os.RemoveAll(tempDir)
		if _, statErr := os.Stat(destDir); statErr == nil {
			return nil // 目标已存在，认为成功
		}
		return fmt.Errorf("failed to rename temp directory: %w", err)
	}

	return nil
}

// Cleanup 停止并删除由本管理器创建的所有池化容器。
// 应在程序关闭时调用以确保资源释放。
func (m *Manager) Cleanup(ctx context.Context) error {
	if !m.poolCfg.Enabled {
		return nil
	}

	// 获取并重置所有池
	m.mu.Lock()
	pools := m.pools
	m.pools = make(map[string]*containerPool)
	m.mu.Unlock()

	// 销毁所有池中的容器
	for _, p := range pools {
		p.mu.Lock()
		for id := range p.all {
			_ = exec.CommandContext(ctx, "docker", "rm", "-f", id).Run()
		}
		p.all = make(map[string]*pooledContainer)
		p.mu.Unlock()
	}

	// 额外清理：删除带有我们标签的所有陈旧容器
	_ = m.cleanupStaleContainers(ctx)
	return nil
}
