// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 status 命令，用于显示 Nimbus 平台的系统状态。
//
// 状态信息包括：
//   - 服务健康状态
//   - 系统版本和运行时间
//   - 各运行时的 VM 池统计信息（热启动VM数、繁忙VM数等）
//
// 支持以 JSON 或 YAML 格式输出。
package cmd

import (
	"github.com/spf13/cobra"
)

// statusCmd 是 status 命令的 cobra.Command 实例。
// 该命令用于检查 Nimbus 平台的整体运行状态。
// 显示服务健康状况、版本信息、运行时间以及 VM 池的统计数据。
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show system status",
	Long: `Show the status of the Nimbus platform.

Displays:
  - Service health
  - VM pool statistics
  - System information

Examples:
  # Show status
  nimbus status

  # Output as JSON
  nimbus status -o json`,
	RunE: runStatus,
}

// init 注册 status 命令到根命令。
func init() {
	rootCmd.AddCommand(statusCmd)
}

// runStatus 是 status 命令的执行函数。
// 该函数执行以下操作：
//  1. 调用 API 获取系统健康状态
//  2. 以指定格式输出状态信息
//
// 状态信息包括服务健康状况、版本、运行时间和 VM 池统计。
//
// 参数：
//   - cmd: cobra 命令对象
//   - args: 命令行参数（此命令不需要参数）
//
// 返回值：
//   - error: 获取状态失败时返回错误信息
func runStatus(cmd *cobra.Command, args []string) error {
	client := NewClient()
	status, err := client.GetStatus()
	if err != nil {
		return err
	}

	printer := NewPrinter()
	return printer.PrintStatus(status)
}
