// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 env 命令，用于管理函数的环境变量。
package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// envCmd 是 env 命令的 cobra.Command 实例。
var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage function environment variables",
	Long:  `Manage environment variables for a function.`,
}

// envListCmd 列出函数的所有环境变量。
var envListCmd = &cobra.Command{
	Use:   "list <function>",
	Short: "List environment variables",
	Args:  cobra.ExactArgs(1),
	RunE:  runEnvList,
}

// envSetCmd 设置函数的一个或多个环境变量。
var envSetCmd = &cobra.Command{
	Use:   "set <function> KEY=VALUE [KEY2=VALUE2...]",
	Short: "Set environment variables",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runEnvSet,
}

// envUnsetCmd 移除函数的一个或多个环境变量。
var envUnsetCmd = &cobra.Command{
	Use:   "unset <function> KEY [KEY2...]",
	Short: "Remove environment variables",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runEnvUnset,
}

func init() {
	rootCmd.AddCommand(envCmd)
	envCmd.AddCommand(envListCmd)
	envCmd.AddCommand(envSetCmd)
	envCmd.AddCommand(envUnsetCmd)
}

func runEnvList(cmd *cobra.Command, args []string) error {
	client := NewClient()
	fn, err := client.GetFunction(args[0])
	if err != nil {
		return err
	}

	if len(fn.EnvVars) == 0 {
		fmt.Printf("No environment variables found for function '%s'.\n", fn.Name)
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tVALUE")
	for k, v := range fn.EnvVars {
		fmt.Fprintf(w, "%s\t%s\n", k, v)
	}
	return w.Flush()
}

func runEnvSet(cmd *cobra.Command, args []string) error {
	name := args[0]
	client := NewClient()
	
	fn, err := client.GetFunction(name)
	if err != nil {
		return err
	}

	envVars := fn.EnvVars
	if envVars == nil {
		envVars = make(map[string]string)
	}

	for _, pair := range args[1:] {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid environment variable format: %s (expected KEY=VALUE)", pair)
		}
		envVars[parts[0]] = parts[1]
	}

	_, err = client.UpdateFunction(name, &UpdateFunctionRequest{
		EnvVars: &envVars,
	})
	if err != nil {
		return err
	}

	fmt.Printf("✅ Environment variables updated for function '%s'.\n", name)
	return nil
}

func runEnvUnset(cmd *cobra.Command, args []string) error {
	name := args[0]
	client := NewClient()
	
	fn, err := client.GetFunction(name)
	if err != nil {
		return err
	}

	if len(fn.EnvVars) == 0 {
		fmt.Printf("No environment variables to unset for function '%s'.\n", name)
		return nil
	}

	envVars := fn.EnvVars
	for _, key := range args[1:] {
		delete(envVars, key)
	}

	_, err = client.UpdateFunction(name, &UpdateFunctionRequest{
		EnvVars: &envVars,
	})
	if err != nil {
		return err
	}

	fmt.Printf("✅ Environment variables updated for function '%s'.\n", name)
	return nil
}
