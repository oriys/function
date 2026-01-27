// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 list 命令，用于列出平台上所有的 serverless 函数。
//
// 默认以表格形式显示函数列表，包括名称、运行时、状态、内存配置等信息。
// 支持以 JSON 或 YAML 格式输出，也支持 ls 作为命令别名。
package cmd

import (
	"strings"

	"github.com/spf13/cobra"
)

// listCmd 是 list 命令的 cobra.Command 实例。
// 该命令用于列出平台上所有已注册的函数。
// 支持 ls 作为命令别名，可配置输出格式（table/json/yaml）。
var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all functions",
	Long: `List all functions in the platform.

Examples:
  # List all functions
  nimbus list

  # Output as JSON
  nimbus list -o json

  # Output as YAML
  nimbus list -o yaml`,
	RunE: runList,
}

var (
	listRuntime string // Filter by runtime
	listStatus  string // Filter by status
	listSearch  string // Search query
)

// init 注册 list 命令到根命令。
func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().StringVarP(&listRuntime, "runtime", "r", "", "Filter by runtime")
	listCmd.Flags().StringVarP(&listStatus, "status", "s", "", "Filter by status")
	listCmd.Flags().StringVarP(&listSearch, "search", "q", "", "Search by name or description")
}

// runList 是 list 命令的执行函数。
func runList(cmd *cobra.Command, args []string) error {
	client := NewClient()
	functions, err := client.ListFunctions()
	if err != nil {
		return err
	}

	// Apply client-side filtering
	filtered := make([]Function, 0)
	for _, fn := range functions {
		if listRuntime != "" && !strings.Contains(strings.ToLower(fn.Runtime), strings.ToLower(listRuntime)) {
			continue
		}
		if listStatus != "" && !strings.Contains(strings.ToLower(fn.Status), strings.ToLower(listStatus)) {
			continue
		}
		if listSearch != "" {
			query := strings.ToLower(listSearch)
			if !strings.Contains(strings.ToLower(fn.Name), query) &&
				!strings.Contains(strings.ToLower(fn.ID), query) &&
				!strings.Contains(strings.ToLower(fn.Runtime), query) {
				continue
			}
		}
		filtered = append(filtered, fn)
	}

	printer := NewPrinter()
	return printer.PrintFunctions(filtered)
}
