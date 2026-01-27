// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 top 命令，用于交互式监控系统状态。
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// topCmd 是 top 命令的 cobra.Command 实例。
var topCmd = &cobra.Command{
	Use:   "top",
	Short: "Interactive system monitor",
	Long:  `Display real-time system status and VM pool metrics.`,
	RunE:  runTop,
}

var topInterval int

func init() {
	rootCmd.AddCommand(topCmd)
	topCmd.Flags().IntVarP(&topInterval, "interval", "i", 2, "Refresh interval in seconds")
}

func runTop(cmd *cobra.Command, args []string) error {
	client := NewClient()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Clear screen first
	fmt.Print("\033[H\033[2J")

	ticker := time.NewTicker(time.Duration(topInterval) * time.Second)
	defer ticker.Stop()

	for {
		status, err := client.GetStatus()
		if err != nil {
			fmt.Printf("Error fetching status: %v\n", err)
		} else {
			// Move cursor to home
			fmt.Print("\033[H")
			
			fmt.Printf("Nimbus Top - %s\n", time.Now().Format("15:04:05"))
			fmt.Printf("Status: %s | Version: %s | Uptime: %s\n", 
				colorStatus(status.Status), status.Version, status.Uptime)
			fmt.Println("\nVM Pool Metrics:")
		
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
			fmt.Fprintln(w, "RUNTIME\tWARM\tBUSY\tTOTAL\tMAX\tUSAGE %")
			
			for _, ps := range status.PoolStats {
				usage := 0.0
				if ps.MaxVMs > 0 {
					usage = float64(ps.TotalVMs) / float64(ps.MaxVMs) * 100
				}
				
				fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%.1f%%\n",
					ps.Runtime,
					ps.WarmVMs,
					ps.BusyVMs,
					ps.TotalVMs,
					ps.MaxVMs,
					usage,
				)
			}
			w.Flush()
			
			fmt.Println("\n(Press Ctrl+C to exit)")
		}

		select {
		case <-ctx.Done():
			fmt.Println("\nExit.")
			return nil
		case <-ticker.C:
			// continue loop
		}
	}
}
