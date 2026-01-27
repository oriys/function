// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 local 命令，用于在本地开发环境中模拟运行 serverless 函数。
//
// 功能特点：
//   - 启动本地 HTTP 服务器接收函数调用请求
//   - 支持多种运行时 (Python, Node.js, Go)
//   - 支持文件变化自动重载 (--watch)
//   - 支持环境变量配置
package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

// localCmd 是 local 命令的 cobra.Command 实例。
// 该命令用于在本地启动一个开发服务器，模拟函数运行环境。
var localCmd = &cobra.Command{
	Use:   "local [path]",
	Short: "Run a function locally for development",
	Long: `Start a local development server to test functions.

The local server simulates the function execution environment, allowing you to
test and debug your functions without deploying them.

Examples:
  # Run a Python function
  nimbus local ./handler.py -r python3.11 -H handler.handler

  # Run a Node.js function with file watching
  nimbus local ./index.js -r nodejs20 -H index.handler --watch

  # Run with environment variables
  nimbus local ./main.go -r go1.24 -H main.Handler -e API_KEY=secret

  # Run with environment file
  nimbus local ./handler.py -r python3.11 -H handler.handler --env-file .env`,
	Args: cobra.MaximumNArgs(1),
	RunE: runLocal,
}

// local 命令的标志变量
var (
	localRuntime string   // 运行时类型
	localHandler string   // 函数入口点
	localPort    int      // 服务端口
	localWatch   bool     // 是否监听文件变化
	localEnv     []string // 环境变量 (KEY=VALUE 格式)
	localEnvFile string   // 环境变量文件
	localData    string   // 默认测试数据 (JSON)
)

// init 注册 local 命令并设置命令行标志。
func init() {
	rootCmd.AddCommand(localCmd)

	localCmd.Flags().StringVarP(&localRuntime, "runtime", "r", "", "Runtime (python3.11, nodejs20, go1.24)")
	localCmd.Flags().StringVarP(&localHandler, "handler", "H", "", "Handler function (e.g., handler.handler)")
	localCmd.Flags().IntVarP(&localPort, "port", "p", 9000, "Server port")
	localCmd.Flags().BoolVarP(&localWatch, "watch", "w", false, "Watch for file changes and reload")
	localCmd.Flags().StringArrayVarP(&localEnv, "env", "e", nil, "Environment variables (KEY=VALUE)")
	localCmd.Flags().StringVar(&localEnvFile, "env-file", "", "Environment variables file (.env)")
	localCmd.Flags().StringVar(&localData, "data", "", "Default test payload (JSON)")

	localCmd.MarkFlagRequired("runtime")
	localCmd.MarkFlagRequired("handler")
}

// LocalServer 表示本地开发服务器
type LocalServer struct {
	Runtime     string
	Handler     string
	SourcePath  string
	Port        int
	EnvVars     map[string]string
	Watch       bool
	DefaultData json.RawMessage

	mu       sync.RWMutex
	code     string
	watcher  *fsnotify.Watcher
	server   *http.Server
	lastLoad time.Time

	// Go build cache
	buildDir   string
	binaryPath string
	lastBuild  time.Time
}

// NewLocalServer 创建一个新的本地服务器实例
func NewLocalServer(runtime, handler, sourcePath string, port int) *LocalServer {
	return &LocalServer{
		Runtime:    runtime,
		Handler:    handler,
		SourcePath: sourcePath,
		Port:       port,
		EnvVars:    make(map[string]string),
	}
}

// Cleanup 清理临时资源
func (s *LocalServer) Cleanup() {
	if s.buildDir != "" {
		os.RemoveAll(s.buildDir)
	}
}

// LoadCode 加载函数代码
func (s *LocalServer) LoadCode() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 读取源文件
	data, err := os.ReadFile(s.SourcePath)
	if err != nil {
		return fmt.Errorf("failed to read source file: %w", err)
	}

	s.code = string(data)
	s.lastLoad = time.Now()

	// If Go runtime, we can trigger a pre-build here if we wanted
	return nil
}

// GetCode 获取当前代码
func (s *LocalServer) GetCode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.code
}

// StartWatching 开始监听文件变化
func (s *LocalServer) StartWatching() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	s.watcher = watcher

	// 监听源文件所在目录
	dir := filepath.Dir(s.SourcePath)
	if err := watcher.Add(dir); err != nil {
		return fmt.Errorf("failed to watch directory: %w", err)
	}

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// 只处理写入事件
				if event.Op&fsnotify.Write == fsnotify.Write {
					// 检查是否是我们监听的文件
					if filepath.Base(event.Name) == filepath.Base(s.SourcePath) {
						fmt.Printf("\n[%s] 🔄 File changed, reloading...\n", time.Now().Format("15:04:05"))
						if err := s.LoadCode(); err != nil {
							fmt.Printf("[%s] ❌ Failed to reload: %v\n", time.Now().Format("15:04:05"), err)
						} else {
							fmt.Printf("[%s] ✅ Reloaded successfully\n", time.Now().Format("15:04:05"))
						}
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				fmt.Printf("Watcher error: %v\n", err)
			}
		}
	}()

	return nil
}

// StopWatching 停止文件监听
func (s *LocalServer) StopWatching() {
	if s.watcher != nil {
		s.watcher.Close()
	}
}

// ServeHTTP 处理 HTTP 请求
func (s *LocalServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/invoke":
		s.handleInvoke(w, r)
	case "/health":
		s.handleHealth(w, r)
	case "/":
		s.handleIndex(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleInvoke 处理函数调用请求
func (s *LocalServer) handleInvoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	start := time.Now()
	fmt.Printf("\n[%s] 📨 Request received\n", start.Format("15:04:05"))

	// 读取请求体
	var payload json.RawMessage
	if r.Body != nil {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		if len(body) > 0 {
			payload = body
		}
	}

	// 如果没有 payload，使用默认数据
	if len(payload) == 0 && len(s.DefaultData) > 0 {
		payload = s.DefaultData
	}
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}

	// 执行函数
	result, err := s.Execute(payload)
	if err != nil {
		fmt.Printf("[%s] ❌ Execution failed: %v\n", time.Now().Format("15:04:05"), err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	duration := time.Since(start)
	fmt.Printf("[%s] ✅ Execution completed in %s\n", time.Now().Format("15:04:05"), duration)

	// 格式化输出结果
	var prettyResult interface{}
	if json.Unmarshal(result, &prettyResult) == nil {
		prettyBytes, _ := json.MarshalIndent(prettyResult, "           ", "  ")
		fmt.Printf("           Response: %s\n", string(prettyBytes))
	} else {
		fmt.Printf("           Response: %s\n", string(result))
	}
	fmt.Println("---")

	w.Header().Set("Content-Type", "application/json")
	w.Write(result)
}

// handleHealth 处理健康检查请求
func (s *LocalServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"runtime":   s.Runtime,
		"handler":   s.Handler,
		"lastLoad":  s.lastLoad,
		"timestamp": time.Now(),
	})
}

// handleIndex 处理首页请求，显示调试信息
func (s *LocalServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <title>Nimbus Local Server</title>
    <style>
        body { font-family: system-ui, -apple-system, sans-serif; max-width: 800px; margin: 50px auto; padding: 0 20px; }
        h1 { color: #2563eb; }
        .info { background: #f3f4f6; padding: 20px; border-radius: 8px; margin: 20px 0; }
        code { background: #e5e7eb; padding: 2px 6px; border-radius: 4px; }
        .endpoint { margin: 10px 0; }
        pre { background: #1f2937; color: #f9fafb; padding: 15px; border-radius: 8px; overflow-x: auto; }
    </style>
</head>
<body>
    <h1>🚀 Nimbus Local Server</h1>
    <div class="info">
        <p><strong>Runtime:</strong> <code>%s</code></p>
        <p><strong>Handler:</strong> <code>%s</code></p>
        <p><strong>Port:</strong> <code>%d</code></p>
    </div>
    <h2>Endpoints</h2>
    <div class="endpoint">
        <code>POST /invoke</code> - 调用函数
    </div>
    <div class="endpoint">
        <code>GET /health</code> - 健康检查
    </div>
    <h2>Example</h2>
    <pre>curl -X POST http://localhost:%d/invoke \
  -H "Content-Type: application/json" \
  -d '{"name": "World"}'</pre>
</body>
</html>`, s.Runtime, s.Handler, s.Port, s.Port)
}

// Execute 执行函数代码
func (s *LocalServer) Execute(payload json.RawMessage) ([]byte, error) {
	code := s.GetCode()

	switch s.Runtime {
	case "python3.11":
		return s.executePython(code, payload)
	case "nodejs20":
		return s.executeNodeJS(code, payload)
	case "go1.24":
		return s.executeGo(code, payload)
	default:
		return nil, fmt.Errorf("unsupported runtime: %s", s.Runtime)
	}
}

// executePython 执行 Python 函数
func (s *LocalServer) executePython(code string, payload json.RawMessage) ([]byte, error) {
	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "nimbus-local-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// 解析 handler (module.function)
	parts := strings.Split(s.Handler, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid handler format, expected 'module.function'")
	}
	moduleName := parts[0]
	funcName := parts[1]

	// 写入源代码
	codePath := filepath.Join(tmpDir, moduleName+".py")
	if err := os.WriteFile(codePath, []byte(code), 0644); err != nil {
		return nil, fmt.Errorf("failed to write code: %w", err)
	}

	// 创建 runner 脚本
	runner := fmt.Sprintf(`
import sys
import json
sys.path.insert(0, %q)

from %s import %s

# 读取输入
event = json.loads(sys.stdin.read())

# 模拟 context
class Context:
    function_name = "local-function"
    memory_limit_mb = 128
    timeout_sec = 30

# 调用函数
result = %s(event, Context())

# 输出结果
print(json.dumps(result))
`, tmpDir, moduleName, funcName, funcName)

	runnerPath := filepath.Join(tmpDir, "runner.py")
	if err := os.WriteFile(runnerPath, []byte(runner), 0644); err != nil {
		return nil, fmt.Errorf("failed to write runner: %w", err)
	}

	// 执行 Python
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "python3", runnerPath)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = append(os.Environ(), s.envSlice()...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("execution failed: %s", stderr.String())
		}
		return nil, fmt.Errorf("execution failed: %w", err)
	}

	return stdout.Bytes(), nil
}

// executeNodeJS 执行 Node.js 函数
func (s *LocalServer) executeNodeJS(code string, payload json.RawMessage) ([]byte, error) {
	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "nimbus-local-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// 解析 handler (module.function)
	parts := strings.Split(s.Handler, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid handler format, expected 'module.function'")
	}
	moduleName := parts[0]
	funcName := parts[1]

	// 写入源代码
	codePath := filepath.Join(tmpDir, moduleName+".js")
	if err := os.WriteFile(codePath, []byte(code), 0644); err != nil {
		return nil, fmt.Errorf("failed to write code: %w", err)
	}

	// 创建 runner 脚本
	runner := fmt.Sprintf(`
const path = require('path');
const mod = require(path.join(%q, '%s.js'));

// 读取输入
let input = '';
process.stdin.on('data', chunk => input += chunk);
process.stdin.on('end', async () => {
  const event = JSON.parse(input || '{}');

  // 模拟 context
  const context = {
    functionName: 'local-function',
    memoryLimitMB: 128,
    timeoutSec: 30,
  };

  try {
    const result = await mod.%s(event, context);
    console.log(JSON.stringify(result));
  } catch (error) {
    console.error(error.message);
    process.exit(1);
  }
});
`, tmpDir, moduleName, funcName)

	runnerPath := filepath.Join(tmpDir, "runner.js")
	if err := os.WriteFile(runnerPath, []byte(runner), 0644); err != nil {
		return nil, fmt.Errorf("failed to write runner: %w", err)
	}

	// 执行 Node.js
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "node", runnerPath)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = append(os.Environ(), s.envSlice()...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("execution failed: %s", stderr.String())
		}
		return nil, fmt.Errorf("execution failed: %w", err)
	}

	return stdout.Bytes(), nil
}

// executeGo 执行 Go 函数
func (s *LocalServer) executeGo(code string, payload json.RawMessage) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查是否需要重新编译
	needsBuild := s.binaryPath == "" || s.lastLoad.After(s.lastBuild)

	if needsBuild {
		// 如果没有 buildDir，创建一个
		if s.buildDir == "" {
			tmpDir, err := os.MkdirTemp("", "nimbus-local-go-*")
			if err != nil {
				return nil, fmt.Errorf("failed to create build dir: %w", err)
			}
			s.buildDir = tmpDir
		}

		// 写入源代码
		codePath := filepath.Join(s.buildDir, "main.go")

		// 包装代码，添加 main 函数
		wrappedCode := fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Context 模拟上下文
type Context struct {
	FunctionName  string
	MemoryLimitMB int
	TimeoutSec    int
}

%s

func main() {
	// 读取输入
	input, _ := io.ReadAll(os.Stdin)
	var event map[string]interface{}
	json.Unmarshal(input, &event)

	// 创建上下文
	ctx := &Context{
		FunctionName:  "local-function",
		MemoryLimitMB: 128,
		TimeoutSec:    30,
	}

	// 调用 Handler
	result, err := Handler(event, ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", err)
		os.Exit(1)
	}

	// 输出结果
	output, _ := json.Marshal(result)
	fmt.Println(string(output))
}
`, code)

		if err := os.WriteFile(codePath, []byte(wrappedCode), 0644); err != nil {
			return nil, fmt.Errorf("failed to write code: %w", err)
		}

		// 初始化 go.mod (如果不存在)
		goModPath := filepath.Join(s.buildDir, "go.mod")
		if _, err := os.Stat(goModPath); os.IsNotExist(err) {
			goMod := "module temp\n\ngo 1.21\n"
			if err := os.WriteFile(goModPath, []byte(goMod), 0644); err != nil {
				return nil, fmt.Errorf("failed to write go.mod: %w", err)
			}
		}

		// 编译
		buildCtx, buildCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer buildCancel()

		s.binaryPath = filepath.Join(s.buildDir, "main")
		buildCmd := exec.CommandContext(buildCtx, "go", "build", "-o", s.binaryPath, codePath)
		buildCmd.Dir = s.buildDir
		buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")

		var buildStderr bytes.Buffer
		buildCmd.Stderr = &buildStderr

		if err := buildCmd.Run(); err != nil {
			return nil, fmt.Errorf("build failed: %s", buildStderr.String())
		}
		s.lastBuild = time.Now()
	}

	// 执行
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.binaryPath)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = append(os.Environ(), s.envSlice()...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("execution failed: %s", stderr.String())
		}
		return nil, fmt.Errorf("execution failed: %w", err)
	}

	return stdout.Bytes(), nil
}

// envSlice 将环境变量 map 转换为 slice
func (s *LocalServer) envSlice() []string {
	var env []string
	for k, v := range s.EnvVars {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}

// Start 启动服务器
func (s *LocalServer) Start() error {
	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.Port),
		Handler: s,
	}

	return s.server.ListenAndServe()
}

// Shutdown 关闭服务器
func (s *LocalServer) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// runLocal 是 local 命令的执行函数
func runLocal(cmd *cobra.Command, args []string) error {
	// 获取源文件路径
	var sourcePath string
	if len(args) > 0 {
		sourcePath = args[0]
	} else {
		// 根据运行时猜测默认文件名
		switch localRuntime {
		case "python3.11":
			sourcePath = "handler.py"
		case "nodejs20":
			sourcePath = "index.js"
		case "go1.24":
			sourcePath = "main.go"
		default:
			return fmt.Errorf("please specify source file path")
		}
	}

	// 检查文件是否存在
	if _, err := os.Stat(sourcePath); os.IsNotExist(err) {
		return fmt.Errorf("source file not found: %s", sourcePath)
	}

	// 转换为绝对路径
	absPath, err := filepath.Abs(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	// 创建服务器
	server := NewLocalServer(localRuntime, localHandler, absPath, localPort)

	// 加载环境变量
	if localEnvFile != "" {
		envVars, err := loadEnvFile(localEnvFile)
		if err != nil {
			return fmt.Errorf("failed to load env file: %w", err)
		}
		for k, v := range envVars {
			server.EnvVars[k] = v
		}
	}
	for _, env := range localEnv {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			server.EnvVars[parts[0]] = parts[1]
		}
	}

	// 加载默认测试数据
	if localData != "" {
		if !json.Valid([]byte(localData)) {
			return fmt.Errorf("invalid JSON for --data")
		}
		server.DefaultData = json.RawMessage(localData)
	}

	// 加载代码
	if err := server.LoadCode(); err != nil {
		return err
	}

	// 启动文件监听
	if localWatch {
		if err := server.StartWatching(); err != nil {
			return err
		}
		defer server.StopWatching()
	}

	// 打印启动信息
	fmt.Println()
	fmt.Println("🚀 Nimbus Local Server started")
	fmt.Printf("   Runtime:  %s\n", localRuntime)
	fmt.Printf("   Handler:  %s\n", localHandler)
	fmt.Printf("   Port:     http://localhost:%d\n", localPort)
	fmt.Println()
	fmt.Println("📡 Endpoints:")
	fmt.Printf("   POST http://localhost:%d/invoke    - 调用函数\n", localPort)
	fmt.Printf("   GET  http://localhost:%d/health    - 健康检查\n", localPort)
	fmt.Println()

	if localWatch {
		fmt.Println("👀 Watching for file changes...")
		fmt.Println()
	}

	fmt.Println("---")

	// 处理优雅关闭
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-stop
		fmt.Println("\n\nShutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
		server.Cleanup()
	}()

	// 启动服务器
	if err := server.Start(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}

// loadEnvFile 加载 .env 文件
func loadEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	envVars := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// 跳过空行和注释
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 解析 KEY=VALUE
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			// 移除引号
			if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) ||
				(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
				value = value[1 : len(value)-1]
			}
			envVars[key] = value
		}
	}

	return envVars, scanner.Err()
}
