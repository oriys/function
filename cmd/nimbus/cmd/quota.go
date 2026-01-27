// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 quota 命令，用于查看资源配额。
package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var quotaCmd = &cobra.Command{
	Use:   "quota",
	Short: "View resource quota and usage",
	RunE:  runQuota,
}

func init() {
	rootCmd.AddCommand(quotaCmd)
}

func runQuota(cmd *cobra.Command, args []string) error {
	client := NewClient()
	usage, err := client.GetQuotaUsage()
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RESOURCE\tUSAGE\tLIMIT\tPERCENT")
	
	fmt.Fprintf(w, "Functions\t%d\t%d\t%.1f%%\n", 
		usage.FunctionCount, usage.MaxFunctions, usage.FunctionUsagePercent)
	
	fmt.Fprintf(w, "Memory (MB)\t%d\t%d\t%.1f%%\n", 
		usage.TotalMemoryMB, usage.MaxMemoryMB, usage.MemoryUsagePercent)
	
	fmt.Fprintf(w, "Daily Invocations\t%d\t%d\t%.1f%%\n", 
		usage.TodayInvocations, usage.MaxInvocationsPerDay, usage.InvocationUsagePercent)
	
	fmt.Fprintf(w, "Code Size (KB)\t%d\t%d\t%.1f%%\n", 
		usage.TotalCodeSizeKB, usage.MaxCodeSizeKB, usage.CodeUsagePercent)
	
	return w.Flush()
}
