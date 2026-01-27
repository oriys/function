// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 stats 命令，用于显示系统统计信息。
package cmd

import (
	"github.com/spf13/cobra"
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show system statistics",
	RunE:  runStats,
}

func init() {
	rootCmd.AddCommand(statsCmd)
}

func runStats(cmd *cobra.Command, args []string) error {
	client := NewClient()
	stats, err := client.GetStats()
	if err != nil {
		return err
	}

	cmd.Printf("Functions:    %d\n", stats.Functions)
	cmd.Printf("Invocations:  %d\n", stats.Invocations)
	
	return nil
}
