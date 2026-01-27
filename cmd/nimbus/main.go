// Package main 是 nimbus 命令行工具的入口点
// nimbus 是用于管理函数计算平台的 CLI 工具
// 它提供创建、列出、调用、删除函数等操作
package main

import (
	"os"

	"github.com/oriys/nimbus/cmd/nimbus/cmd"
)

// main 是 CLI 工具的主函数
// 它调用 cmd 包的 Execute 函数来解析和执行用户命令
func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
