// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 get 命令，用于获取单个函数的详细信息。
//
// 可以通过函数名称或函数ID查询函数，支持显示函数代码（需要 --code 参数），
// 支持以 JSON 或 YAML 格式输出。
package cmd

import (
	"github.com/spf13/cobra"
)

// getCmd 是 get 命令的 cobra.Command 实例。
// 该命令用于获取指定函数的详细信息，包括运行时、处理函数、内存配置等。
// 可通过 --code 参数显示函数源代码。
var getCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get function details",
	Long: `Get detailed information about a function.

Examples:
  # Get function by name
  nimbus get hello

  # Get function by ID
  nimbus get fn_abc123

  # Output as JSON
  nimbus get hello -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runGet,
}

// getShowCode 控制是否在输出中显示函数代码。
// 默认为 false，需要通过 --code 参数启用。
var getShowCode bool

// init 注册 get 命令并设置命令行标志。
func init() {
	rootCmd.AddCommand(getCmd)
	getCmd.Flags().BoolVar(&getShowCode, "code", false, "Show function code")
}

// runGet 是 get 命令的执行函数。
// 该函数执行以下操作：
//  1. 通过名称或ID获取函数信息
//  2. 根据 --code 参数决定是否显示代码
//  3. 以指定格式输出函数详情
//
// 参数：
//   - cmd: cobra 命令对象
//   - args: 命令行参数，args[0] 是函数名称或ID
//
// 返回值：
//   - error: 获取失败时返回错误信息
func runGet(cmd *cobra.Command, args []string) error {
	client := NewClient()
	fn, err := client.GetFunction(args[0])
	if err != nil {
		return err
	}

	if !getShowCode {
		fn.Code = ""
	}

	printer := NewPrinter()
	return printer.PrintFunction(fn)
}
