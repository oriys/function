// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 delete 命令，用于删除已存在的 serverless 函数。
//
// 删除操作默认需要用户确认，可以通过 --force 参数跳过确认步骤。
// 该命令还支持 rm 和 remove 作为别名。
package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// deleteCmd 是 delete 命令的 cobra.Command 实例。
// 该命令用于从平台删除指定的函数。
// 支持 rm 和 remove 作为命令别名，删除前默认需要用户确认。
var deleteCmd = &cobra.Command{
	Use:     "delete [name...]",
	Aliases: []string{"rm", "remove"},
	Short:   "Delete one or more functions",
	Long: `Delete functions from the platform.

Examples:
  # Delete a function (with confirmation)
  nimbus delete hello

  # Delete multiple functions
  nimbus delete fn1 fn2 fn3 --force

  # Delete all functions
  nimbus delete --all --force`,
	RunE: runDelete,
}

// deleteForce 控制是否跳过删除确认。
// 当设置为 true 时，不会提示用户确认删除操作。
var (
	deleteForce bool
	deleteAll   bool
)

// init 注册 delete 命令并设置命令行标志。
func init() {
	rootCmd.AddCommand(deleteCmd)
	deleteCmd.Flags().BoolVarP(&deleteForce, "force", "f", false, "Force delete without confirmation")
	deleteCmd.Flags().BoolVar(&deleteAll, "all", false, "Delete all functions")
}

// runDelete 是 delete 命令的执行函数。
func runDelete(cmd *cobra.Command, args []string) error {
	client := NewClient()
	var names []string

	if deleteAll {
		functions, err := client.ListFunctions()
		if err != nil {
			return err
		}
		for _, fn := range functions {
			names = append(names, fn.Name)
		}
		if len(names) == 0 {
			fmt.Println("No functions to delete.")
			return nil
		}
	} else {
		if len(args) == 0 {
			return fmt.Errorf("accepts at least 1 arg(s), received 0")
		}
		names = args
	}

	if !deleteForce {
		msg := fmt.Sprintf("Are you sure you want to delete %d function(s)? [y/N]: ", len(names))
		if len(names) == 1 {
			msg = fmt.Sprintf("Are you sure you want to delete function '%s'? [y/N]: ", names[0])
		}
		fmt.Print(msg)
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))

		if response != "y" && response != "yes" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	for _, name := range names {
		if err := client.DeleteFunction(name); err != nil {
			fmt.Printf("❌ Failed to delete '%s': %v\n", name, err)
		} else {
			fmt.Printf("✅ Function '%s' deleted successfully.\n", name)
		}
	}

	return nil
}
