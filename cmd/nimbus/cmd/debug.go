package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// debugCmd represents the debug command
var debugCmd = &cobra.Command{
	Use:   "debug <name>",
	Short: "Debug a function locally using Docker",
	Long: `Start a local Docker container with the function code and expose a debugging port.
This allows you to attach a debugger (like VS Code or GoLand) to the running function.

Supported runtimes:
  - python3.11 (Default port: 5678, requires debugpy)
  - nodejs20   (Default port: 9229, built-in inspector)

Examples:
  # Debug a Python function
  nimbus debug my-func --runtime python3.11 --file handler.py --port 5678

  # Debug a Node.js function
  nimbus debug my-func --runtime nodejs20 --file index.js --port 9229`,
	RunE: runDebug,
}

var (
	debugRuntime string
	debugFile    string
	debugPort    int
	debugEnv     []string
)

func init() {
	rootCmd.AddCommand(debugCmd)

	debugCmd.Flags().StringVarP(&debugRuntime, "runtime", "r", "", "Runtime (python3.11, nodejs20)")
	debugCmd.Flags().StringVarP(&debugFile, "file", "f", "", "Code file path")
	debugCmd.Flags().IntVarP(&debugPort, "port", "p", 0, "Debugger port (default depends on runtime)")
	debugCmd.Flags().StringArrayVarP(&debugEnv, "env", "e", nil, "Environment variables (KEY=VALUE)")

	debugCmd.MarkFlagRequired("runtime")
	debugCmd.MarkFlagRequired("file")
}

func runDebug(cmd *cobra.Command, args []string) error {
	// 1. 验证文件是否存在
	if debugFile == "" {
		// 如果未指定文件，尝试在 args 中查找（或者报错）
		return fmt.Errorf("--file is required")
	}
	absPath, err := filepath.Abs(debugFile)
	if err != nil {
		return fmt.Errorf("invalid file path: %w", err)
	}
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return fmt.Errorf("file not found: %s", absPath)
	}

	// 2. 确定镜像和调试命令
	var image string
	var debugCommand []string
	var defaultPort int
	var containerPath string

	// 函数名用于容器命名
	funcName := "unknown"
	if len(args) > 0 {
		funcName = args[0]
	}

	switch debugRuntime {
	case "python3.11":
		// 使用开发镜像或标准运行时镜像
		// 注意：这里假设本地有 function-runtime-python:latest 镜像
		image = "function-runtime-python:latest"
		defaultPort = 5678
		containerPath = "/var/task/" + filepath.Base(absPath)
		// Python 需要安装 debugpy。我们尝试在启动时安装。
		// --wait-for-client 确保脚本在调试器连接前不会执行
		// 我们假设入口是 handler.main，但这里只是运行文件。如果需要特定的 handler 调用，
		// 可能需要一个 wrapper 脚本。这里为了简单，直接运行文件（假设文件里有执行逻辑或 infinite loop）。
		// 实际上，FaaS handler 通常是被调用的。更好的方式是启动一个 mock server。
		// 但为了"最简单"的调试，我们假设用户的文件可以被 python 直接执行，或者我们注入一个 wrapper。
		
		// 方案修正：为了让用户能调试 Handler，我们需要一个简单的 Runner。
		// 这里我们使用一段 Python 单行脚本来加载模块并等待。
		// 或者，最简单的方式：只是启动环境，让用户 exec 进去？不，用户想要 Attach。
		// 让我们假设用户在文件底部写了 `if __name__ == "__main__": handler(...)` 用于调试。
		debugCommand = []string{
			"/bin/sh", "-c",
			fmt.Sprintf("pip install debugpy -q && echo 'Waiting for debugger on port %d...' && python -m debugpy --listen 0.0.0.0:%d --wait-for-client %s", defaultPort, defaultPort, containerPath),
		}
	case "nodejs20":
		image = "function-runtime-nodejs:latest"
		defaultPort = 9229
		containerPath = "/var/task/" + filepath.Base(absPath)
		debugCommand = []string{
			"node",
			fmt.Sprintf("--inspect-brk=0.0.0.0:%d", defaultPort),
			containerPath,
		}
	default:
		return fmt.Errorf("unsupported runtime for debugging: %s (currently supports python3.11, nodejs20)", debugRuntime)
	}

	// 如果用户指定了端口，使用用户的，否则用默认的
	port := debugPort
	if port == 0 {
		port = defaultPort
	}

	// 3. 构建 docker run 命令
	// 使用 host.docker.internal 允许容器访问宿主机（可选，视需求而定）
	dockerArgs := []string{
		"run", "--rm", "-it",
		"-p", fmt.Sprintf("%d:%d", port, defaultPort),
		"-v", fmt.Sprintf("%s:%s", absPath, containerPath),
		"--name", fmt.Sprintf("nimbus-debug-%s", funcName),
	}

	// 添加环境变量
	for _, env := range debugEnv {
		dockerArgs = append(dockerArgs, "-e", env)
	}

	// 镜像
	dockerArgs = append(dockerArgs, image)

	// 覆盖入口点/命令
	dockerArgs = append(dockerArgs, debugCommand...)

	// 4. 执行
	fmt.Printf("Starting debug container for %s (%s)...\n", funcName, debugRuntime)
	fmt.Printf("Mapping port: localhost:%d -> container:%d\n", port, defaultPort)
	fmt.Printf("Mounting: %s -> %s\n", absPath, containerPath)
	fmt.Println("---------------------------------------------------------")

	if debugRuntime == "python3.11" {
		fmt.Println("👉 For VS Code (Python):")
		fmt.Println("   1. Create a launch.json with configuration type 'python' and request 'attach'.")
		fmt.Printf("   2. Set 'connect': {\"host\": \"localhost\", \"port\": %d}\n", port)
		fmt.Println("   3. Start debugging (F5).")
	} else if debugRuntime == "nodejs20" {
		fmt.Println("👉 For VS Code (Node.js):")
		fmt.Println("   1. Create a launch.json with type 'node', request 'attach'.")
		fmt.Printf("   2. Set 'port': %d\n", port)
		fmt.Println("   3. Start debugging (F5).")
		fmt.Println("   (Or open chrome://inspect in Chrome)")
	}
	fmt.Println("---------------------------------------------------------")

	dockerCmd := exec.CommandContext(context.Background(), "docker", dockerArgs...)
	dockerCmd.Stdout = os.Stdout
	dockerCmd.Stderr = os.Stderr
	dockerCmd.Stdin = os.Stdin

	return dockerCmd.Run()
}
