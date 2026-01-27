// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 create 命令，用于创建新的 serverless 函数。
//
// 创建函数时可以通过以下方式提供代码：
//   - 使用 --code 参数直接提供内联代码
//   - 使用 --file 参数从文件中读取代码
//
// 创建函数时必须指定运行时(runtime)和处理函数(handler)，
// 可选参数包括内存限制、超时时间和环境变量。
package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// createCmd 是 create 命令的 cobra.Command 实例。
// 该命令用于在平台上创建新的 serverless 函数。
// 支持从内联代码或文件创建函数，并可配置运行时、处理函数、内存、超时和环境变量。
var createCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new function",
	Long: `Create a new serverless function.

Examples:
  # Create from inline code
  nimbus create hello --runtime python3.11 --handler main.handler \
    --code 'def handler(event): return {"message": "Hello"}'

  # Create from file
  nimbus create hello --runtime python3.11 --handler main.handler --file handler.py

  # Create with environment variables
  nimbus create hello --runtime nodejs20 --handler index.handler --file index.js \
    --env DEBUG=true --env API_KEY=secret`,
	Args: cobra.ExactArgs(1),
	RunE: runCreate,
}

// create 命令的标志变量
var (
	createRuntime  string   // 函数运行时，如 python3.11、nodejs20、go1.24
	createHandler  string   // 处理函数入口点，如 main.handler
	createCode     string   // 内联代码内容
	createFile     string   // 代码文件路径
	createMemory   int      // 内存限制（MB）
	createTimeout  int      // 执行超时时间（秒）
	createEnv      []string // 环境变量列表，格式为 KEY=VALUE
	createCron     string   // 定时任务表达式
	createHTTPPath string   // HTTP 路径
	createHTTPMethods []string // HTTP 方法
)

// init 注册 create 命令并设置命令行标志。
// 该函数在包初始化时自动调用。
func init() {
	rootCmd.AddCommand(createCmd)

	createCmd.Flags().StringVarP(&createRuntime, "runtime", "r", "", "Runtime (python3.11, nodejs20, go1.24)")
	createCmd.Flags().StringVarP(&createHandler, "handler", "H", "", "Handler function (e.g., main.handler)")
	createCmd.Flags().StringVarP(&createCode, "code", "c", "", "Inline code")
	createCmd.Flags().StringVarP(&createFile, "file", "f", "", "Code file path")
	createCmd.Flags().IntVarP(&createMemory, "memory", "m", 256, "Memory limit in MB")
	createCmd.Flags().IntVarP(&createTimeout, "timeout", "t", 30, "Timeout in seconds")
	createCmd.Flags().StringArrayVarP(&createEnv, "env", "e", nil, "Environment variables (KEY=VALUE)")
	createCmd.Flags().StringVar(&createCron, "cron", "", "Cron expression for scheduled trigger (e.g., '*/5 * * * *')")
	createCmd.Flags().StringVar(&createHTTPPath, "http-path", "", "Custom HTTP path (e.g., '/hello')")
	createCmd.Flags().StringSliceVar(&createHTTPMethods, "http-methods", nil, "Allowed HTTP methods (e.g., 'GET,POST')")

	// 标记必需的参数
	createCmd.MarkFlagRequired("runtime")
	createCmd.MarkFlagRequired("handler")
}

// runCreate 是 create 命令的执行函数。
// 该函数执行以下操作：
//  1. 获取函数名称（从命令行参数）
//  2. 从文件或内联参数获取函数代码
//  3. 解析环境变量
//  4. 调用 API 创建函数
//  5. 输出创建结果
//
// 参数：
//   - cmd: cobra 命令对象
//   - args: 命令行参数，args[0] 是函数名称
//
// 返回值：
//   - error: 创建失败时返回错误信息
func runCreate(cmd *cobra.Command, args []string) error {
	name := args[0]

	// Get code from file or inline
	code := createCode
	if createFile != "" {
		data, err := os.ReadFile(createFile)
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}
		code = string(data)
	}

	if code == "" {
		return fmt.Errorf("either --code or --file is required")
	}

	// Parse environment variables
	envVars := make(map[string]string)
	for _, env := range createEnv {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid env format: %s (expected KEY=VALUE)", env)
		}
		envVars[parts[0]] = parts[1]
	}

	client := NewClient()
	fn, err := client.CreateFunction(&CreateFunctionRequest{
		Name:           name,
		Runtime:        createRuntime,
		Handler:        createHandler,
		Code:           code,
		MemoryMB:       createMemory,
		TimeoutSec:     createTimeout,
		EnvVars:        envVars,
		CronExpression: createCron,
		HTTPPath:       createHTTPPath,
		HTTPMethods:    createHTTPMethods,
	})
	if err != nil {
		return err
	}

	printer := NewPrinter()
	fmt.Printf("Function '%s' created successfully.\n\n", fn.Name)
	return printer.PrintFunction(fn)
}
