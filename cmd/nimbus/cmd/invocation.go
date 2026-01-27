// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 invocation 命令，用于查询函数调用的详细信息。
//
// 该命令主要用于：
//   - 查看特定调用的详细信息（状态、输入、输出、错误等）
//   - 等待异步调用完成（--wait 参数）
//
// 对于异步调用的函数，可以使用此命令配合 --wait 参数
// 轮询等待调用完成并获取结果。
package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// invocationCmd 是 invocation 命令的 cobra.Command 实例。
// 该命令用于查询特定调用的详细信息，支持等待异步调用完成。
// 支持 inv 作为命令别名。
var invocationCmd = &cobra.Command{
	Use:     "invocation <id>",
	Aliases: []string{"inv"},
	Short:   "Get invocation details",
	Long: `Get details about a function invocation.

Examples:
  # Get invocation by ID
  nimbus invocation inv_abc123

  # Wait for async invocation to complete
  nimbus invocation inv_abc123 --wait

  # Output as JSON
  nimbus invocation inv_abc123 -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runInvocation,
}

// invocation 命令的标志变量
var invocationWait bool   // 是否等待调用完成
var invocationTimeout int // 等待超时时间（秒）

// init 注册 invocation 命令并设置命令行标志。
func init() {
	rootCmd.AddCommand(invocationCmd)
	invocationCmd.Flags().BoolVarP(&invocationWait, "wait", "w", false, "Wait for completion")
	invocationCmd.Flags().IntVar(&invocationTimeout, "timeout", 60, "Wait timeout in seconds")
}

// runInvocation 是 invocation 命令的执行函数。
// 该函数执行以下操作：
//  1. 获取调用ID
//  2. 如果指定了 --wait，则轮询等待调用完成
//  3. 否则直接获取调用详情并输出
//
// 参数：
//   - cmd: cobra 命令对象
//   - args: 命令行参数，args[0] 是调用ID
//
// 返回值：
//   - error: 查询失败时返回错误信息
func runInvocation(cmd *cobra.Command, args []string) error {
	id := args[0]
	client := NewClient()

	if invocationWait {
		return waitForInvocation(client, id, time.Duration(invocationTimeout)*time.Second)
	}

	inv, err := client.GetInvocation(id)
	if err != nil {
		return err
	}

	return printInvocationDetail(inv)
}

// waitForInvocation 等待异步调用完成。
// 该函数每 500 毫秒轮询一次调用状态，直到调用完成或超时。
//
// 参数：
//   - client: API 客户端
//   - id: 调用ID
//   - timeout: 最大等待时间
//
// 返回值：
//   - error: 等待失败或超时时返回错误信息
func waitForInvocation(client *Client, id string, timeout time.Duration) error {
	start := time.Now()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	fmt.Printf("Waiting for invocation %s...\n", id)

	for {
		select {
		case <-ticker.C:
			inv, err := client.GetInvocation(id)
			if err != nil {
				return err
			}

			if inv.Status != "pending" && inv.Status != "running" {
				fmt.Printf("\nInvocation completed in %s\n\n", time.Since(start).Round(time.Millisecond))
				return printInvocationDetail(inv)
			}

			fmt.Printf(".")
		default:
			if time.Since(start) > timeout {
				return fmt.Errorf("timeout waiting for invocation")
			}
		}
	}
}

// printInvocationDetail 打印调用的详细信息。
// 根据配置的输出格式（table/json/yaml）格式化输出。
// 对于 table 格式，显示调用ID、函数ID、状态、耗时、冷启动、时间戳、输入和输出。
//
// 参数：
//   - inv: 调用信息
//
// 返回值：
//   - error: 打印失败时返回错误信息
func printInvocationDetail(inv *Invocation) error {
	printer := NewPrinter()

	format := viper.GetString("output")
	if format == "json" || format == "yaml" {
		return printer.printJSON(inv)
	}

	fmt.Printf("Invocation ID: %s\n", inv.ID)
	fmt.Printf("Function ID:   %s\n", inv.FunctionID)
	fmt.Printf("Status:        %s\n", colorStatus(inv.Status))
	fmt.Printf("Duration:      %d ms\n", inv.DurationMs)

	coldStart := "No"
	if inv.ColdStart {
		coldStart = "Yes"
	}
	fmt.Printf("Cold Start:    %s\n", coldStart)
	fmt.Printf("Started:       %s\n", inv.StartedAt.Format(time.RFC3339))

	if !inv.CompletedAt.IsZero() {
		fmt.Printf("Completed:     %s\n", inv.CompletedAt.Format(time.RFC3339))
	}

	if inv.Error != "" {
		fmt.Printf("\nError: %s\n", inv.Error)
	}

	if len(inv.Input) > 0 {
		fmt.Println("\nInput:")
		printFormattedJSON(inv.Input)
	}

	if len(inv.Output) > 0 {
		fmt.Println("\nOutput:")
		printFormattedJSON(inv.Output)
	}

	return nil
}

// printFormattedJSON 格式化打印 JSON 数据。
// 如果是有效的 JSON，会进行美化缩进后输出；否则原样输出。
//
// 参数：
//   - data: 要打印的 JSON 数据
func printFormattedJSON(data json.RawMessage) {
	var obj interface{}
	if json.Unmarshal(data, &obj) == nil {
		prettyJSON, _ := json.MarshalIndent(obj, "", "  ")
		fmt.Println(string(prettyJSON))
	} else {
		fmt.Println(string(data))
	}
}
