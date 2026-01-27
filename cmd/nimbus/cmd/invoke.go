// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 invoke 命令，用于调用（执行）serverless 函数。
//
// 支持多种方式提供调用参数：
//   - 使用 --data 参数直接提供 JSON 数据
//   - 使用 --file 参数从文件读取 JSON 数据
//   - 通过标准输入（stdin）管道传递数据
//
// 支持同步调用（默认）和异步调用（--async 参数）两种模式。
// 同步调用会等待函数执行完成并返回结果，异步调用会立即返回调用ID。
package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// invokeCmd 是 invoke 命令的 cobra.Command 实例。
// 该命令用于调用指定的函数，支持同步和异步两种调用模式。
// 调用参数可以通过命令行参数、文件或标准输入提供。
var invokeCmd = &cobra.Command{
	Use:   "invoke <name>",
	Short: "Invoke a function",
	Long: `Invoke a function synchronously and wait for the result.

Examples:
  # Invoke with JSON data
  nimbus invoke hello --data '{"name": "World"}'

  # Invoke with data from file
  nimbus invoke hello --file event.json

  # Invoke from stdin
  echo '{"name": "World"}' | nimbus invoke hello

  # Invoke asynchronously
  nimbus invoke hello --data '{"name": "World"}' --async`,
	Args: cobra.ExactArgs(1),
	RunE: runInvoke,
}

// invoke 命令的标志变量
var (
	invokeData  string // JSON 格式的调用参数
	invokeFile  string // 包含 JSON 参数的文件路径
	invokeAsync bool   // 是否使用异步调用模式
)

// init 注册 invoke 命令并设置命令行标志。
func init() {
	rootCmd.AddCommand(invokeCmd)

	invokeCmd.Flags().StringVarP(&invokeData, "data", "d", "", "JSON payload")
	invokeCmd.Flags().StringVarP(&invokeFile, "file", "f", "", "JSON payload file")
	invokeCmd.Flags().BoolVarP(&invokeAsync, "async", "a", false, "Invoke asynchronously")
}

// runInvoke 是 invoke 命令的执行函数。
// 该函数执行以下操作：
//  1. 获取要调用的函数名称
//  2. 从命令行参数、文件或标准输入获取 JSON 参数
//  3. 验证 JSON 格式是否正确
//  4. 根据 --async 参数选择同步或异步调用
//  5. 输出调用结果或调用ID
//
// 参数：
//   - cmd: cobra 命令对象
//   - args: 命令行参数，args[0] 是函数名称
//
// 返回值：
//   - error: 调用失败时返回错误信息
func runInvoke(cmd *cobra.Command, args []string) error {
	name := args[0]

	// Get payload from various sources
	var payload json.RawMessage

	switch {
	case invokeData != "":
		payload = json.RawMessage(invokeData)
	case invokeFile != "":
		data, err := os.ReadFile(invokeFile)
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}
		payload = data
	default:
		// Try reading from stdin
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("failed to read stdin: %w", err)
			}
			if len(data) > 0 {
				payload = data
			}
		}
	}

	// Default to empty object if no payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}

	// Validate JSON
	if !json.Valid(payload) {
		return fmt.Errorf("invalid JSON payload")
	}

	client := NewClient()
	printer := NewPrinter()

	if invokeAsync {
		resp, err := client.InvokeFunctionAsync(name, payload)
		if err != nil {
			return err
		}
		fmt.Printf("Function '%s' invoked asynchronously.\n", name)
		fmt.Printf("Invocation ID: %s\n", resp.RequestID)
		fmt.Printf("Check status with: nimbus invocation %s\n", resp.RequestID)
		return nil
	}

	// Synchronous invocation
	start := time.Now()
	resp, err := client.InvokeFunction(name, payload)
	if err != nil {
		return err
	}

	// For JSON/YAML output, just print the result
	format := viper.GetString("output")
	if format == "json" || format == "yaml" {
		return printer.PrintInvokeResult(resp)
	}

	// For table output, show formatted result
	status := "success"
	if resp.StatusCode < 200 || resp.StatusCode > 299 || resp.Error != "" {
		status = "failed"
	}

	fmt.Printf("Function '%s' invoked (%s).\n\n", name, colorStatus(status))
	fmt.Printf("Invocation ID: %s\n", resp.RequestID)
	fmt.Printf("Status Code:   %d\n", resp.StatusCode)
	fmt.Printf("Duration:      %d ms (total: %s)\n", resp.DurationMs, time.Since(start).Round(time.Millisecond))

	coldStart := "No"
	if resp.ColdStart {
		coldStart = "Yes"
	}
	fmt.Printf("Cold Start:    %s\n", coldStart)

	if resp.Error != "" {
		fmt.Printf("\nError: %s\n", resp.Error)
		return nil
	}

	if len(resp.Body) > 0 {
		fmt.Println("\nResult:")
		var prettyJSON []byte
		var obj interface{}
		if json.Unmarshal(resp.Body, &obj) == nil {
			prettyJSON, _ = json.MarshalIndent(obj, "", "  ")
			fmt.Println(string(prettyJSON))
		} else {
			fmt.Println(string(resp.Body))
		}
	}

	return nil
}
