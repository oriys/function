// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 environment 命令，用于管理部署环境。
package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var environmentCmd = &cobra.Command{
	Use:     "environment",
	Aliases: []string{"env-type"}, // Avoid conflict with existing 'env' command for function env vars
	Short:   "Manage deployment environments",
	Long:    `Create, list, and delete deployment environments (e.g., dev, prod).`,
}

var environmentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List environments",
	RunE:  runEnvironmentList,
}

var environmentCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create an environment",
	Args:  cobra.ExactArgs(1),
	RunE:  runEnvironmentCreate,
}

var environmentDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete an environment",
	Args:  cobra.ExactArgs(1),
	RunE:  runEnvironmentDelete,
}

var (
	envIsDefault bool
	envDesc      string
)

func init() {
	rootCmd.AddCommand(environmentCmd)
	environmentCmd.AddCommand(environmentListCmd)
	environmentCmd.AddCommand(environmentCreateCmd)
	environmentCmd.AddCommand(environmentDeleteCmd)

	environmentCreateCmd.Flags().BoolVar(&envIsDefault, "default", false, "Set as default environment")
	environmentCreateCmd.Flags().StringVarP(&envDesc, "description", "d", "", "Environment description")
}

func runEnvironmentList(cmd *cobra.Command, args []string) error {
	client := NewClient()
	envs, err := client.ListEnvironments()
	if err != nil {
		return err
	}

	if len(envs) == 0 {
		cmd.Println("No environments found.")
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tDEFAULT\tCREATED")
	for _, env := range envs {
		isDefault := ""
		if env.IsDefault {
			isDefault = "✅"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			env.ID, env.Name, isDefault, env.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return w.Flush()
}

func runEnvironmentCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	req := map[string]interface{}{
		"name":        name,
		"description": envDesc,
		"is_default":  envIsDefault,
	}

	client := NewClient()
	env, err := client.CreateEnvironment(req)
	if err != nil {
		return err
	}

	cmd.Printf("✅ Environment '%s' created with ID: %s\n", env.Name, env.ID)
	return nil
}

func runEnvironmentDelete(cmd *cobra.Command, args []string) error {
	client := NewClient()
	if err := client.DeleteEnvironment(args[0]); err != nil {
		return err
	}
	cmd.Println("✅ Environment deleted.")
	return nil
}
