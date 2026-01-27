// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 context 命令，用于管理多个 API 环境。
package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// contextCmd 管理环境上下文
var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage API contexts (environments)",
}

var contextListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all contexts",
	RunE:  runContextList,
}

var contextSetCmd = &cobra.Command{
	Use:   "set <name> <url>",
	Short: "Add or update a context",
	Args:  cobra.ExactArgs(2),
	RunE:  runContextSet,
}

var contextUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Switch to a different context",
	Args:  cobra.ExactArgs(1),
	RunE:  runContextUse,
}

func init() {
	rootCmd.AddCommand(contextCmd)
	contextCmd.AddCommand(contextListCmd)
	contextCmd.AddCommand(contextSetCmd)
	contextCmd.AddCommand(contextUseCmd)
}

func runContextList(cmd *cobra.Command, args []string) error {
	contexts := viper.GetStringMapString("contexts")
	current := viper.GetString("current_context")

	if len(contexts) == 0 {
		fmt.Println("No contexts defined. Use 'nimbus context set <name> <url>' to add one.")
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CURRENT\tNAME\tAPI URL")
	for name, url := range contexts {
		prefix := ""
		if name == current {
			prefix = "*"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", prefix, name, url)
	}
	return w.Flush()
}

func runContextSet(cmd *cobra.Command, args []string) error {
	name := args[0]
	url := args[1]

	contexts := viper.GetStringMapString("contexts")
	if contexts == nil {
		contexts = make(map[string]string)
	}
	contexts[name] = url
	viper.Set("contexts", contexts)

	// If it's the first context, set it as current
	if viper.GetString("current_context") == "" {
		viper.Set("current_context", name)
		viper.Set("api_url", url)
	}

	if err := viper.WriteConfigAs(getConfigPath()); err != nil {
		return err
	}

	fmt.Printf("✅ Context '%s' set to %s\n", name, url)
	return nil
}

func runContextUse(cmd *cobra.Command, args []string) error {
	name := args[0]
	contexts := viper.GetStringMapString("contexts")
	
	url, ok := contexts[name]
	if !ok {
		return fmt.Errorf("context '%s' not found", name)
	}

	viper.Set("current_context", name)
	viper.Set("api_url", url)

	if err := viper.WriteConfigAs(getConfigPath()); err != nil {
		return err
	}

	fmt.Printf("✅ Switched to context '%s' (%s)\n", name, url)
	return nil
}