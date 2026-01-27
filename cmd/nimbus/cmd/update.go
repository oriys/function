// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 update 命令，用于更新已存在的 serverless 函数配置。
//
// 支持更新以下配置项：
//   - 处理函数（handler）
//   - 函数代码（通过 --code 或 --file）
//   - 内存限制（memory）
//   - 超时时间（timeout）
//   - 环境变量（env）
//
// 所有参数都是可选的，只会更新提供的参数对应的配置。
package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// updateCmd 是 update 命令的 cobra.Command 实例。
// 该命令用于更新已存在函数的配置，包括代码、处理函数、内存、超时和环境变量。
// 只有明确指定的参数才会被更新。
var updateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update a function",
	Long: `Update an existing function.

Examples:
  # Update code from file
  nimbus update hello --file handler.py

  # Update memory and timeout
  nimbus update hello --memory 512 --timeout 60

  # Update environment variables
  nimbus update hello --env DEBUG=false`,
	Args: cobra.ExactArgs(1),
	RunE: runUpdate,
}

// update 命令的标志变量
var (
	updateHandler string   // 新的处理函数入口点
	updateCode    string   // 新的内联代码
	updateFile    string   // 新代码的文件路径
	updateMemory  int      // 新的内存限制（MB）
	updateTimeout int      // 新的超时时间（秒）
	updateEnv     []string // 新的环境变量列表
	updateCron    string   // 新的定时任务表达式
	updateHTTPPath string  // 新的 HTTP 路径
	updateHTTPMethods []string // 新的 HTTP 方法
)

// init 注册 update 命令并设置命令行标志。
func init() {
	rootCmd.AddCommand(updateCmd)

	updateCmd.Flags().StringVarP(&updateHandler, "handler", "H", "", "Handler function")
	updateCmd.Flags().StringVarP(&updateCode, "code", "c", "", "Inline code")
	updateCmd.Flags().StringVarP(&updateFile, "file", "f", "", "Code file path")
	updateCmd.Flags().IntVarP(&updateMemory, "memory", "m", 0, "Memory limit in MB")
	updateCmd.Flags().IntVarP(&updateTimeout, "timeout", "t", 0, "Timeout in seconds")
	updateCmd.Flags().StringArrayVarP(&updateEnv, "env", "e", nil, "Environment variables (KEY=VALUE)")
	updateCmd.Flags().StringVar(&updateCron, "cron", "", "Cron expression for scheduled trigger")
	updateCmd.Flags().StringVar(&updateHTTPPath, "http-path", "", "New custom HTTP path")
	updateCmd.Flags().StringSliceVar(&updateHTTPMethods, "http-methods", nil, "New allowed HTTP methods")
}

// runUpdate 是 update 命令的执行函数。
// 该函数执行以下操作：
//  1. 获取要更新的函数名称
//  2. 收集所有需要更新的配置项
//  3. 调用 API 更新函数
//  4. 输出更新后的函数信息
//
// 只有明确指定的参数才会被更新，未指定的参数保持原值。
//
// 参数：
//   - cmd: cobra 命令对象
//   - args: 命令行参数，args[0] 是函数名称
//
// 返回值：
//   - error: 更新失败时返回错误信息
func runUpdate(cmd *cobra.Command, args []string) error {
	name := args[0]

	req := &UpdateFunctionRequest{}

	// Get code from file or inline
	if updateFile != "" {
		data, err := os.ReadFile(updateFile)
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}
		code := string(data)
		req.Code = &code
	} else if updateCode != "" {
		req.Code = &updateCode
	}

	if updateHandler != "" {
		req.Handler = &updateHandler
	}
	if updateMemory > 0 {
		req.MemoryMB = &updateMemory
	}
	if updateTimeout > 0 {
		req.TimeoutSec = &updateTimeout
	}
	if updateCron != "" {
		req.CronExpression = &updateCron
	}
	if updateHTTPPath != "" {
		req.HTTPPath = &updateHTTPPath
	}
	if updateHTTPMethods != nil {
		req.HTTPMethods = &updateHTTPMethods
	}

	// Parse environment variables
	if len(updateEnv) > 0 {
		envVars := make(map[string]string)
		for _, env := range updateEnv {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid env format: %s (expected KEY=VALUE)", env)
			}
			envVars[parts[0]] = parts[1]
		}
		req.EnvVars = &envVars
	}

	client := NewClient()
	fn, err := client.UpdateFunction(name, req)
	if err != nil {
		return err
	}

	printer := NewPrinter()
	cmd.Printf("Function '%s' updated successfully.\n\n", fn.Name)
	return printer.PrintFunction(fn)
}
