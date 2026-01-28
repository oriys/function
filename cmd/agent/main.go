//go:build linux
// +build linux

// Package main 是函数执行代理的入口点
// Agent 运行在 Firecracker 虚拟机内部，负责接收和执行函数调用
// 它通过 vsock 与宿主机通信，支持多种运行时（Python、Node.js、Go、WebAssembly）
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mdlayher/vsock"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// 常量定义
const (
	VsockPort        = 9999 // vsock 监听端口，用于与宿主机通信
	MessageTypeInit  = 1    // 消息类型：初始化
	MessageTypeExec  = 2    // 消息类型：执行函数
	MessageTypeResp  = 3    // 消息类型：响应
	MessageTypePing  = 4    // 消息类型：心跳检测
	MessageTypePong  = 5    // 消息类型：心跳响应
	MessageTypeDebug = 6    // 消息类型：调试
	MessageTypeState = 7    // 消息类型：状态操作

	FunctionDir = "/var/function" // 函数代码存储目录
	LayersDir   = "/opt/layers"   // 层内容存储目录
	StateAPIPort = 9998           // 状态 API 监听端口（HTTP）
)

// Message 定义 Agent 与宿主机之间的通信消息格式
// 所有通信都通过 JSON 序列化的消息进行
type Message struct {
	Type      uint8           `json:"type"`               // 消息类型
	RequestID string          `json:"request_id"`         // 请求唯一标识，用于关联请求和响应
	Payload   json.RawMessage `json:"payload,omitempty"`  // 消息载荷，具体内容取决于消息类型
}

// InitPayload 定义函数初始化请求的载荷结构
// 宿主机发送此载荷来配置 Agent 执行特定函数
type InitPayload struct {
	FunctionID    string            `json:"function_id"`              // 函数唯一标识
	Handler       string            `json:"handler"`                  // 处理函数入口点（如 handler.main）
	Code          string            `json:"code"`                     // 函数代码（base64 编码或明文）
	Runtime       string            `json:"runtime"`                  // 运行时类型（python3.11、nodejs20、go1.24、wasm）
	EnvVars       map[string]string `json:"env_vars,omitempty"`       // 环境变量
	MemoryLimitMB int               `json:"memory_limit_mb"`          // 内存限制（MB）
	TimeoutSec    int               `json:"timeout_sec"`              // 执行超时时间（秒）
	Layers        []LayerInfo       `json:"layers,omitempty"`         // 函数层列表（可选）
	StateEnabled  bool              `json:"state_enabled,omitempty"`  // 是否启用状态功能
	SessionKey    string            `json:"session_key,omitempty"`    // 会话标识（有状态函数）
}

// LayerInfo 表示函数层的信息
// 包含层的标识、版本、内容和加载顺序
type LayerInfo struct {
	LayerID string `json:"layer_id"` // 层唯一标识符
	Version int    `json:"version"`  // 层版本号
	Content []byte `json:"content"`  // 层内容（ZIP 压缩包）
	Order   int    `json:"order"`    // 加载顺序（小的先加载）
}

// ExecPayload 定义函数执行请求的载荷结构
type ExecPayload struct {
	Input      json.RawMessage `json:"input"`                  // 函数输入参数，作为 JSON 传递给函数
	SessionKey string          `json:"session_key,omitempty"`  // 会话标识（有状态函数）
}

// StatePayload 定义状态操作请求的载荷结构
type StatePayload struct {
	Operation string          `json:"operation"`           // 操作类型: get, set, delete, incr, exists, keys, expire
	Scope     string          `json:"scope"`               // 作用域: session, function, invocation
	Key       string          `json:"key"`                 // 状态键
	Value     json.RawMessage `json:"value,omitempty"`     // 状态值（set 时使用）
	TTL       int             `json:"ttl,omitempty"`       // 过期时间（秒）
	Delta     int64           `json:"delta,omitempty"`     // 增量（incr 时使用）
	Version   int64           `json:"version,omitempty"`   // 版本号（乐观锁）
}

// StateResponsePayload 定义状态操作响应的载荷结构
type StateResponsePayload struct {
	Success bool            `json:"success"`
	Value   json.RawMessage `json:"value,omitempty"`
	Version int64           `json:"version,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// ResponsePayload 定义函数执行响应的载荷结构
type ResponsePayload struct {
	Success      bool            `json:"success"`                // 执行是否成功
	Output       json.RawMessage `json:"output,omitempty"`       // 函数输出结果
	Error        string          `json:"error,omitempty"`        // 错误信息（如果执行失败）
	DurationMs   int64           `json:"duration_ms"`            // 执行耗时（毫秒）
	MemoryUsedMB int             `json:"memory_used_mb"`         // 内存使用量（MB）
}

// Agent 是函数执行代理的核心结构
// 它管理运行时初始化和函数执行
type Agent struct {
	initialized  bool          // 是否已初始化
	config       *InitPayload  // 当前函数配置
	runtime      Runtime       // 当前使用的运行时
	debugManager *DebugManager // 调试管理器
	stateConn    net.Conn      // 状态操作连接（与宿主机通信）
	sessionKey   string        // 当前会话标识
}

// Runtime 定义运行时接口
// 所有支持的运行时（Python、Node.js、Go、WebAssembly）都必须实现此接口
type Runtime interface {
	// Init 初始化运行时环境
	// 包括创建包装脚本、编译代码等准备工作
	Init(config *InitPayload) error

	// Execute 执行函数
	// 接收 JSON 格式的输入，返回 JSON 格式的输出
	Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}

// main 是 Agent 的主函数
// 它启动 vsock 监听器，等待并处理来自宿主机的请求
func main() {
	fmt.Println("Function Agent starting...")

	agent := &Agent{
		debugManager: NewDebugManager(),
	}

	// 在 vsock 端口上监听连接
	// vsock 是 Firecracker 虚拟机与宿主机通信的机制
	// 相比网络通信，vsock 提供更低延迟和更好的安全隔离
	listener, err := vsock.Listen(VsockPort, nil)
	if err != nil {
		fmt.Printf("Failed to listen on vsock: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()

	fmt.Printf("Listening on vsock port %d\n", VsockPort)

	// 设置信号处理
	// 监听 SIGTERM 和 SIGINT 信号以优雅关闭
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		cancel()
		listener.Close()
	}()

	// 主循环：接受并处理连接
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				// 收到关闭信号，退出
				return
			default:
				fmt.Printf("Accept error: %v\n", err)
				continue
			}
		}
		// 为每个连接启动独立的处理协程
		go agent.handleConnection(ctx, conn)
	}
}

// handleConnection 处理单个 vsock 连接
// 持续读取消息并处理，直到连接关闭
//
// 参数:
//   - ctx: 上下文，用于取消操作
//   - conn: vsock 连接
func (a *Agent) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	// 循环处理消息
	for {
		// 读取下一条消息
		msg, err := readMessage(conn)
		if err != nil {
			if err != io.EOF {
				fmt.Printf("Read error: %v\n", err)
			}
			return
		}

		// 处理消息并发送响应
		resp := a.handleMessage(ctx, msg)
		if err := writeMessage(conn, resp); err != nil {
			fmt.Printf("Write error: %v\n", err)
			return
		}
	}
}

// handleMessage 根据消息类型分发处理
//
// 参数:
//   - ctx: 上下文
//   - msg: 接收到的消息
//
// 返回:
//   - *Message: 响应消息
func (a *Agent) handleMessage(ctx context.Context, msg *Message) *Message {
	switch msg.Type {
	case MessageTypePing:
		// 心跳检测，直接返回 Pong
		return &Message{
			Type:      MessageTypePong,
			RequestID: msg.RequestID,
		}

	case MessageTypeInit:
		// 初始化请求，配置函数运行时
		return a.handleInit(msg)

	case MessageTypeExec:
		// 执行请求，运行函数
		return a.handleExec(ctx, msg)

	case MessageTypeDebug:
		// 调试请求，处理 DAP 消息
		return a.handleDebug(ctx, msg)

	default:
		// 未知消息类型
		return errorResponse(msg.RequestID, fmt.Sprintf("unknown message type: %d", msg.Type))
	}
}

// handleInit 处理初始化请求
// 创建函数代码文件并初始化对应的运行时
//
// 参数:
//   - msg: 初始化消息
//
// 返回:
//   - *Message: 响应消息
func (a *Agent) handleInit(msg *Message) *Message {
	// 解析初始化载荷
	var payload InitPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return errorResponse(msg.RequestID, fmt.Sprintf("invalid init payload: %v", err))
	}

	// 创建函数代码目录
	os.MkdirAll(FunctionDir, 0755)

	// 处理函数层
	if err := a.setupLayers(&payload); err != nil {
		return errorResponse(msg.RequestID, fmt.Sprintf("failed to setup layers: %v", err))
	}

	// 将函数代码写入文件
	// 根据运行时类型确定文件名
	if err := a.writeCode(&payload); err != nil {
		return errorResponse(msg.RequestID, fmt.Sprintf("failed to write code: %v", err))
	}

	// 创建并初始化运行时
	rt, err := newRuntime(payload.Runtime)
	if err != nil {
		return errorResponse(msg.RequestID, fmt.Sprintf("failed to create runtime: %v", err))
	}

	if err := rt.Init(&payload); err != nil {
		return errorResponse(msg.RequestID, fmt.Sprintf("runtime init failed: %v", err))
	}

	// 保存运行时和配置
	a.runtime = rt
	a.config = &payload
	a.initialized = true

	return successResponse(msg.RequestID, nil)
}

// handleExec 处理函数执行请求
// 在配置的超时时间内执行函数并返回结果
//
// 参数:
//   - ctx: 上下文
//   - msg: 执行请求消息
//
// 返回:
//   - *Message: 包含执行结果的响应消息
func (a *Agent) handleExec(ctx context.Context, msg *Message) *Message {
	// 检查是否已初始化
	if !a.initialized {
		return errorResponse(msg.RequestID, "agent not initialized")
	}

	// 解析执行载荷
	var payload ExecPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return errorResponse(msg.RequestID, fmt.Sprintf("invalid exec payload: %v", err))
	}

	// 创建带超时的上下文
	// 确保函数不会无限期运行
	timeout := time.Duration(a.config.TimeoutSec) * time.Second
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 执行函数并记录耗时
	start := time.Now()
	output, err := a.runtime.Execute(execCtx, payload.Input)
	duration := time.Since(start)

	// 构建响应
	resp := &ResponsePayload{
		DurationMs:   duration.Milliseconds(),
		MemoryUsedMB: getMemoryUsage(),
	}

	if err != nil {
		resp.Success = false
		resp.Error = err.Error()
	} else {
		resp.Success = true
		resp.Output = output
	}

	data, _ := json.Marshal(resp)
	return &Message{
		Type:      MessageTypeResp,
		RequestID: msg.RequestID,
		Payload:   data,
	}
}

// DebugPayload 调试请求载荷
type DebugPayload struct {
	Action     string          `json:"action"`      // start, stop, dap
	DAPMessage json.RawMessage `json:"dap_message"` // DAP 消息（action=dap 时使用）
	Config     *DebugConfig    `json:"config"`      // 调试配置（action=start 时使用）
}

// DebugResponsePayload 调试响应载荷
type DebugResponsePayload struct {
	Success     bool              `json:"success"`
	Error       string            `json:"error,omitempty"`
	DAPMessages []json.RawMessage `json:"dap_messages,omitempty"`
	SessionID   string            `json:"session_id,omitempty"`
}

// handleDebug 处理调试请求
// 支持启动/停止调试会话和处理 DAP 消息
//
// 参数:
//   - ctx: 上下文
//   - msg: 调试请求消息
//
// 返回:
//   - *Message: 响应消息
func (a *Agent) handleDebug(ctx context.Context, msg *Message) *Message {
	// 检查是否已初始化
	if !a.initialized {
		return a.debugErrorResponse(msg.RequestID, "agent not initialized")
	}

	// 解析调试载荷
	var payload DebugPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return a.debugErrorResponse(msg.RequestID, fmt.Sprintf("invalid debug payload: %v", err))
	}

	switch payload.Action {
	case "start":
		return a.handleDebugStart(msg.RequestID, payload.Config)

	case "stop":
		return a.handleDebugStop(msg.RequestID)

	case "dap":
		return a.handleDebugDAP(msg.RequestID, payload.DAPMessage)

	default:
		return a.debugErrorResponse(msg.RequestID, fmt.Sprintf("unknown debug action: %s", payload.Action))
	}
}

// handleDebugStart 启动调试会话
func (a *Agent) handleDebugStart(requestID string, config *DebugConfig) *Message {
	if config == nil {
		config = &DebugConfig{}
	}

	// 设置默认配置
	config.FunctionID = a.config.FunctionID
	config.Handler = a.config.Handler
	config.CodePath = FunctionDir
	config.Runtime = a.config.Runtime
	config.EnvVars = a.config.EnvVars
	config.TimeoutSec = a.config.TimeoutSec

	// 启动调试器
	if err := a.debugManager.StartDebug(config); err != nil {
		return a.debugErrorResponse(requestID, fmt.Sprintf("failed to start debugger: %v", err))
	}

	// 启动事件转发协程
	go a.forwardDebugEvents()

	resp := &DebugResponsePayload{
		Success:   true,
		SessionID: a.config.FunctionID,
	}

	data, _ := json.Marshal(resp)
	return &Message{
		Type:      MessageTypeResp,
		RequestID: requestID,
		Payload:   data,
	}
}

// handleDebugStop 停止调试会话
func (a *Agent) handleDebugStop(requestID string) *Message {
	if err := a.debugManager.StopDebug(); err != nil {
		return a.debugErrorResponse(requestID, fmt.Sprintf("failed to stop debugger: %v", err))
	}

	resp := &DebugResponsePayload{
		Success: true,
	}

	data, _ := json.Marshal(resp)
	return &Message{
		Type:      MessageTypeResp,
		RequestID: requestID,
		Payload:   data,
	}
}

// handleDebugDAP 处理 DAP 消息
func (a *Agent) handleDebugDAP(requestID string, dapMsg json.RawMessage) *Message {
	if !a.debugManager.IsDebugging() {
		return a.debugErrorResponse(requestID, "no active debug session")
	}

	responses, err := a.debugManager.HandleDAP(dapMsg)
	if err != nil {
		return a.debugErrorResponse(requestID, fmt.Sprintf("DAP error: %v", err))
	}

	resp := &DebugResponsePayload{
		Success:     true,
		DAPMessages: responses,
	}

	data, _ := json.Marshal(resp)
	return &Message{
		Type:      MessageTypeResp,
		RequestID: requestID,
		Payload:   data,
	}
}

// forwardDebugEvents 转发调试事件
func (a *Agent) forwardDebugEvents() {
	events := a.debugManager.Events()
	for event := range events {
		// 将事件包装为消息发送
		// 注意：这需要一个持久连接来推送事件
		// 当前实现中，事件会被缓存在通道中，等待下次请求时返回
		fmt.Printf("Debug event: %s\n", string(event))
	}
}

// debugErrorResponse 创建调试错误响应
func (a *Agent) debugErrorResponse(requestID, errMsg string) *Message {
	resp := &DebugResponsePayload{
		Success: false,
		Error:   errMsg,
	}
	data, _ := json.Marshal(resp)
	return &Message{
		Type:      MessageTypeResp,
		RequestID: requestID,
		Payload:   data,
	}
}

// writeCode 将函数代码写入文件
// 根据运行时类型确定文件扩展名
//
// 参数:
//   - payload: 初始化载荷，包含代码和运行时信息
//
// 返回:
//   - error: 写入错误
func (a *Agent) writeCode(payload *InitPayload) error {
	var filename string
	switch payload.Runtime {
	case "python3.11":
		filename = "handler.py"
	case "nodejs20":
		filename = "handler.js"
	case "go1.24":
		filename = "handler.go"
	case "wasm":
		filename = "handler.wasm"
	default:
		return fmt.Errorf("unsupported runtime: %s", payload.Runtime)
	}

	path := filepath.Join(FunctionDir, filename)
	return os.WriteFile(path, []byte(payload.Code), 0644)
}

// setupLayers 处理函数层的解压和环境配置
// 将层内容解压到 LayersDir，并设置相应的环境变量
//
// 参数:
//   - payload: 初始化载荷，包含层信息
//
// 返回:
//   - error: 处理错误
func (a *Agent) setupLayers(payload *InitPayload) error {
	if len(payload.Layers) == 0 {
		return nil
	}

	// 创建层目录
	if err := os.MkdirAll(LayersDir, 0755); err != nil {
		return fmt.Errorf("failed to create layers directory: %w", err)
	}

	// 按 Order 排序（小的先加载）
	sort.Slice(payload.Layers, func(i, j int) bool {
		return payload.Layers[i].Order < payload.Layers[j].Order
	})

	var pythonPaths, nodePaths []string

	for _, layer := range payload.Layers {
		layerDir := filepath.Join(LayersDir, layer.LayerID)
		if err := os.MkdirAll(layerDir, 0755); err != nil {
			return fmt.Errorf("failed to create layer directory %s: %w", layer.LayerID, err)
		}

		// 解压 ZIP 内容
		if err := extractZip(layer.Content, layerDir); err != nil {
			return fmt.Errorf("failed to extract layer %s: %w", layer.LayerID, err)
		}

		// 根据运行时构建路径
		switch payload.Runtime {
		case "python3.11":
			pythonPaths = append(pythonPaths,
				filepath.Join(layerDir, "python"),
				filepath.Join(layerDir, "python", "lib", "python3.11", "site-packages"),
			)
		case "nodejs20":
			nodePaths = append(nodePaths,
				filepath.Join(layerDir, "nodejs", "node_modules"),
			)
		}

		fmt.Printf("Layer %s (v%d) extracted to %s\n", layer.LayerID, layer.Version, layerDir)
	}

	// 设置环境变量
	if len(pythonPaths) > 0 {
		existing := os.Getenv("PYTHONPATH")
		if existing != "" {
			pythonPaths = append(pythonPaths, existing)
		}
		os.Setenv("PYTHONPATH", strings.Join(pythonPaths, ":"))
		fmt.Printf("PYTHONPATH set to: %s\n", os.Getenv("PYTHONPATH"))
	}
	if len(nodePaths) > 0 {
		existing := os.Getenv("NODE_PATH")
		if existing != "" {
			nodePaths = append(nodePaths, existing)
		}
		os.Setenv("NODE_PATH", strings.Join(nodePaths, ":"))
		fmt.Printf("NODE_PATH set to: %s\n", os.Getenv("NODE_PATH"))
	}

	return nil
}

// extractZip 将 ZIP 内容解压到目标目录
//
// 参数:
//   - content: ZIP 文件内容
//   - destDir: 目标目录
//
// 返回:
//   - error: 解压错误
func extractZip(content []byte, destDir string) error {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return fmt.Errorf("failed to create zip reader: %w", err)
	}

	for _, file := range reader.File {
		path := filepath.Join(destDir, file.Name)

		// 防止路径遍历攻击
		cleanPath := filepath.Clean(path)
		cleanDest := filepath.Clean(destDir)
		if !strings.HasPrefix(cleanPath, cleanDest+string(os.PathSeparator)) && cleanPath != cleanDest {
			fmt.Printf("Warning: skipping potentially unsafe path: %s\n", file.Name)
			continue
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(path, file.Mode()); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", path, err)
			}
			continue
		}

		// 确保父目录存在
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory for %s: %w", path, err)
		}

		// 解压文件
		outFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			return fmt.Errorf("failed to create file %s: %w", path, err)
		}

		rc, err := file.Open()
		if err != nil {
			outFile.Close()
			return fmt.Errorf("failed to open zip entry %s: %w", file.Name, err)
		}

		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()

		if err != nil {
			return fmt.Errorf("failed to write file %s: %w", path, err)
		}
	}

	return nil
}

// newRuntime 根据运行时名称创建对应的运行时实例
//
// 参数:
//   - name: 运行时名称
//
// 返回:
//   - Runtime: 运行时实例
//   - error: 创建错误
func newRuntime(name string) (Runtime, error) {
	switch name {
	case "python3.11":
		return &PythonRuntime{}, nil
	case "nodejs20":
		return &NodeRuntime{}, nil
	case "go1.24":
		return &GoRuntime{}, nil
	case "wasm":
		return &WasmRuntime{}, nil
	default:
		return nil, fmt.Errorf("unsupported runtime: %s", name)
	}
}

// ============================================================================
// Python 运行时
// ============================================================================

// PythonRuntime 实现 Python 函数的执行
type PythonRuntime struct {
	handler string // 处理函数入口点
}

// Init 初始化 Python 运行时
// 创建一个包装脚本，用于加载和执行用户函数
//
// 参数:
//   - config: 初始化配置
//
// 返回:
//   - error: 初始化错误
func (r *PythonRuntime) Init(config *InitPayload) error {
	r.handler = config.Handler

	// 创建 Python 包装脚本
	// 这个脚本负责：
	// 1. 设置 Python 路径
	// 2. 导入用户的处理函数
	// 3. 从标准输入读取 JSON 输入
	// 4. 调用处理函数
	// 5. 将结果输出到标准输出
	wrapper := fmt.Sprintf(`
import sys
import json
sys.path.insert(0, '%s')

# 导入处理函数
parts = '%s'.rsplit('.', 1)
if len(parts) == 2:
    module_name, func_name = parts
else:
    module_name, func_name = 'handler', parts[0]

module = __import__(module_name)
handler = getattr(module, func_name)

# 从标准输入读取输入数据
input_data = json.loads(sys.stdin.read())

# 执行处理函数
result = handler(input_data)

# 将结果输出到标准输出
print(json.dumps(result))
`, FunctionDir, config.Handler)

	return os.WriteFile(filepath.Join(FunctionDir, "_wrapper.py"), []byte(wrapper), 0644)
}

// Execute 执行 Python 函数
// 通过子进程运行包装脚本
//
// 参数:
//   - ctx: 上下文，用于超时控制
//   - input: JSON 格式的输入参数
//
// 返回:
//   - json.RawMessage: 函数输出
//   - error: 执行错误
func (r *PythonRuntime) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	// 使用上下文创建可取消的命令
	cmd := exec.CommandContext(ctx, "python3", filepath.Join(FunctionDir, "_wrapper.py"))
	cmd.Stdin = jsonReader(input)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("python error: %s", string(exitErr.Stderr))
		}
		return nil, err
	}

	return json.RawMessage(output), nil
}

// ============================================================================
// Node.js 运行时
// ============================================================================

// NodeRuntime 实现 Node.js 函数的执行
type NodeRuntime struct {
	handler string // 处理函数入口点
}

// Init 初始化 Node.js 运行时
// 创建一个包装脚本，用于加载和执行用户函数
//
// 参数:
//   - config: 初始化配置
//
// 返回:
//   - error: 初始化错误
func (r *NodeRuntime) Init(config *InitPayload) error {
	r.handler = config.Handler

	// 创建 Node.js 包装脚本
	// 支持异步处理函数
	wrapper := fmt.Sprintf(`
const fs = require('fs');
const path = require('path');

// 加载处理函数
const parts = '%s'.split('.');
const modulePath = path.join('%s', parts[0] + '.js');
const handlerName = parts[1] || 'handler';
const handler = require(modulePath)[handlerName];

// 从标准输入读取输入数据
let input = '';
process.stdin.on('data', chunk => input += chunk);
process.stdin.on('end', async () => {
    try {
        const event = JSON.parse(input);
        const result = await handler(event);
        console.log(JSON.stringify(result));
    } catch (err) {
        console.error(err.message);
        process.exit(1);
    }
});
`, config.Handler, FunctionDir)

	return os.WriteFile(filepath.Join(FunctionDir, "_wrapper.js"), []byte(wrapper), 0644)
}

// Execute 执行 Node.js 函数
// 通过子进程运行包装脚本
//
// 参数:
//   - ctx: 上下文，用于超时控制
//   - input: JSON 格式的输入参数
//
// 返回:
//   - json.RawMessage: 函数输出
//   - error: 执行错误
func (r *NodeRuntime) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	cmd := exec.CommandContext(ctx, "node", filepath.Join(FunctionDir, "_wrapper.js"))
	cmd.Stdin = jsonReader(input)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("node error: %s", string(exitErr.Stderr))
		}
		return nil, err
	}

	return json.RawMessage(output), nil
}

// ============================================================================
// Go 运行时
// ============================================================================

// GoRuntime 实现预编译 Go 二进制的执行
// Go 函数需要预先编译为二进制文件
type GoRuntime struct{}

// Init 初始化 Go 运行时
// 对于 Go，代码应该是预编译的二进制文件
//
// 参数:
//   - config: 初始化配置
//
// 返回:
//   - error: 初始化错误
func (r *GoRuntime) Init(config *InitPayload) error {
	// Go 代码期望是预编译的二进制文件
	// 无需额外的初始化步骤
	return nil
}

// Execute 执行 Go 函数
// 直接运行预编译的二进制文件
//
// 参数:
//   - ctx: 上下文，用于超时控制
//   - input: JSON 格式的输入参数
//
// 返回:
//   - json.RawMessage: 函数输出
//   - error: 执行错误
func (r *GoRuntime) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	binaryPath := filepath.Join(FunctionDir, "handler")
	cmd := exec.CommandContext(ctx, binaryPath)
	cmd.Stdin = jsonReader(input)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("go error: %s", string(exitErr.Stderr))
		}
		return nil, err
	}

	return json.RawMessage(output), nil
}

// ============================================================================
// WebAssembly 运行时
// ============================================================================

// WasmRuntime 使用 wazero 实现 WebAssembly 函数执行
//
// WASM 模块必须导出以下函数：
//   - alloc(size: i32) -> i32          : 分配内存，返回指针
//   - handle(ptr: i32, len: i32) -> i64 : 处理请求，返回 (ptr << 32) | len
//
// 可选导出：
//   - dealloc(ptr: i32, size: i32)     : 释放内存
type WasmRuntime struct {
	runtime  wazero.Runtime         // wazero 运行时
	module   wazero.CompiledModule  // 编译后的 WASM 模块
	instance api.Module             // 模块实例
}

// Init 初始化 WebAssembly 运行时
// 编译并实例化 WASM 模块
//
// 参数:
//   - config: 初始化配置
//
// 返回:
//   - error: 初始化错误
func (r *WasmRuntime) Init(config *InitPayload) error {
	// 读取 WASM 文件
	wasmPath := filepath.Join(FunctionDir, "handler.wasm")
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return fmt.Errorf("failed to read wasm file: %w", err)
	}

	ctx := context.Background()
	r.runtime = wazero.NewRuntime(ctx)

	// 实例化 WASI（某些 WASM 模块可能需要它进行内存管理）
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r.runtime); err != nil {
		return fmt.Errorf("failed to instantiate WASI: %w", err)
	}

	// 编译模块
	r.module, err = r.runtime.CompileModule(ctx, wasmBytes)
	if err != nil {
		return fmt.Errorf("failed to compile wasm module: %w", err)
	}

	// 实例化模块（一次实例化，多次调用）
	r.instance, err = r.runtime.InstantiateModule(ctx, r.module, wazero.NewModuleConfig())
	if err != nil {
		return fmt.Errorf("failed to instantiate wasm module: %w", err)
	}

	// 验证必需的导出函数存在
	if r.instance.ExportedFunction("alloc") == nil {
		return fmt.Errorf("wasm module must export 'alloc(size: i32) -> i32' function")
	}
	if r.instance.ExportedFunction("handle") == nil {
		return fmt.Errorf("wasm module must export 'handle(ptr: i32, len: i32) -> i64' function")
	}

	return nil
}

// Execute 执行 WebAssembly 函数
// 通过 wazero 调用 WASM 模块中的 handle 函数
//
// 参数:
//   - ctx: 上下文，用于超时控制
//   - input: JSON 格式的输入参数
//
// 返回:
//   - json.RawMessage: 函数输出
//   - error: 执行错误
func (r *WasmRuntime) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	// 获取导出的函数
	alloc := r.instance.ExportedFunction("alloc")
	handle := r.instance.ExportedFunction("handle")
	dealloc := r.instance.ExportedFunction("dealloc") // 可选

	inputBytes := []byte(input)
	inputLen := uint64(len(inputBytes))

	// 在 WASM 内存中分配空间存储输入
	results, err := alloc.Call(ctx, inputLen)
	if err != nil {
		return nil, fmt.Errorf("failed to allocate input memory: %w", err)
	}
	inputPtr := uint32(results[0])

	// 将输入数据写入 WASM 内存
	memory := r.instance.Memory()
	if memory == nil {
		return nil, fmt.Errorf("wasm module has no memory export")
	}
	if !memory.Write(inputPtr, inputBytes) {
		return nil, fmt.Errorf("failed to write input to wasm memory")
	}

	// 调用 handle 函数
	results, err = handle.Call(ctx, uint64(inputPtr), inputLen)
	if err != nil {
		return nil, fmt.Errorf("handle function failed: %w", err)
	}

	// 解析返回值：高 32 位 = 指针，低 32 位 = 长度
	packedResult := results[0]
	outputPtr := uint32(packedResult >> 32)
	outputLen := uint32(packedResult & 0xFFFFFFFF)

	// 从 WASM 内存读取输出
	output, ok := memory.Read(outputPtr, outputLen)
	if !ok {
		return nil, fmt.Errorf("failed to read output from wasm memory")
	}

	// 如果有 dealloc 函数，释放输入内存
	if dealloc != nil {
		_, _ = dealloc.Call(ctx, uint64(inputPtr), inputLen)
	}

	if len(output) == 0 {
		return json.RawMessage("null"), nil
	}

	return json.RawMessage(output), nil
}

// ============================================================================
// 辅助函数
// ============================================================================

// readMessage 从连接读取一条消息
// 消息格式：4 字节长度（大端序）+ JSON 数据
//
// 参数:
//   - conn: 网络连接
//
// 返回:
//   - *Message: 解析后的消息
//   - error: 读取或解析错误
func readMessage(conn net.Conn) (*Message, error) {
	// 读取 4 字节长度前缀
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lenBuf)

	// 读取消息体
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}

	// 解析 JSON
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}

	return &msg, nil
}

// writeMessage 向连接写入一条消息
// 消息格式：4 字节长度（大端序）+ JSON 数据
//
// 参数:
//   - conn: 网络连接
//   - msg: 要发送的消息
//
// 返回:
//   - error: 写入错误
func writeMessage(conn net.Conn, msg *Message) error {
	// 序列化消息
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	// 写入长度前缀
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	if _, err := conn.Write(lenBuf); err != nil {
		return err
	}

	// 写入消息体
	if _, err := conn.Write(data); err != nil {
		return err
	}

	return nil
}

// successResponse 创建成功响应消息
//
// 参数:
//   - requestID: 请求 ID
//   - data: 响应数据
//
// 返回:
//   - *Message: 响应消息
func successResponse(requestID string, data interface{}) *Message {
	resp := &ResponsePayload{Success: true}
	if data != nil {
		output, _ := json.Marshal(data)
		resp.Output = output
	}
	payload, _ := json.Marshal(resp)
	return &Message{
		Type:      MessageTypeResp,
		RequestID: requestID,
		Payload:   payload,
	}
}

// errorResponse 创建错误响应消息
//
// 参数:
//   - requestID: 请求 ID
//   - errMsg: 错误信息
//
// 返回:
//   - *Message: 响应消息
func errorResponse(requestID, errMsg string) *Message {
	resp := &ResponsePayload{
		Success: false,
		Error:   errMsg,
	}
	payload, _ := json.Marshal(resp)
	return &Message{
		Type:      MessageTypeResp,
		RequestID: requestID,
		Payload:   payload,
	}
}

// jsonReader 创建一个从 JSON 数据读取的 io.Reader
//
// 参数:
//   - data: JSON 数据
//
// 返回:
//   - io.Reader: 读取器
func jsonReader(data json.RawMessage) io.Reader {
	return &jsonBytesReader{data: data}
}

// jsonBytesReader 实现 io.Reader 接口
// 用于将 JSON 数据作为标准输入传递给子进程
type jsonBytesReader struct {
	data json.RawMessage // JSON 数据
	pos  int             // 当前读取位置
}

// Read 实现 io.Reader 接口
func (r *jsonBytesReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// getMemoryUsage 获取当前进程的内存使用量（MB）
//
// 返回:
//   - int: 内存使用量（MB）
func getMemoryUsage() int {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int(m.Alloc / 1024 / 1024)
}
