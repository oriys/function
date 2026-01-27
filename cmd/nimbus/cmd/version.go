// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 version 命令，用于显示 CLI 工具的版本信息。
//
// 显示的版本信息包括：
//   - CLI 版本号
//   - Git 提交哈希
//   - 构建日期
//   - Go 版本
//   - 操作系统和架构
//
// 这些信息在编译时通过 -ldflags 注入。
package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// 版本信息变量，在构建时通过 ldflags 设置。
// 例如: go build -ldflags "-X cmd.Version=1.0.0 -X cmd.GitCommit=abc123"
var (
	// Version 是 CLI 的版本号，默认为 "dev" 表示开发版本
	Version = "dev"
	// GitCommit 是构建时的 Git 提交哈希
	GitCommit = "unknown"
	// BuildDate 是构建日期
	BuildDate = "unknown"
)

// versionCmd 是 version 命令的 cobra.Command 实例。
// 该命令用于显示 CLI 的版本和构建信息，帮助用户了解当前使用的版本。
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	// Run 直接打印版本信息，不返回错误
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("nimbus version %s\n", Version)
		fmt.Printf("  Git commit: %s\n", GitCommit)
		fmt.Printf("  Build date: %s\n", BuildDate)
		fmt.Printf("  Go version: %s\n", runtime.Version())
		fmt.Printf("  OS/Arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
	},
}

// init 注册 version 命令到根命令。
func init() {
	rootCmd.AddCommand(versionCmd)
}
