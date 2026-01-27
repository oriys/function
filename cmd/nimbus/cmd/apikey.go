// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 apikey 命令，用于管理 API 密钥。
package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var apikeyCmd = &cobra.Command{
	Use:   "apikey",
	Short: "Manage API keys",
	Long:  `Create, list, and delete API keys for accessing the platform.`, // Note: Backticks are correctly used here for multi-line strings.
}

var apikeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List API keys",
	RunE:  runApiKeyList,
}

var apikeyCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create an API key",
	Args:  cobra.ExactArgs(1),
	RunE:  runApiKeyCreate,
}

var apikeyDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete an API key",
	Args:  cobra.ExactArgs(1),
	RunE:  runApiKeyDelete,
}

func init() {
	rootCmd.AddCommand(apikeyCmd)
	apikeyCmd.AddCommand(apikeyListCmd)
	apikeyCmd.AddCommand(apikeyCreateCmd)
	apikeyCmd.AddCommand(apikeyDeleteCmd)
}

func runApiKeyList(cmd *cobra.Command, args []string) error {
	client := NewClient()
	keys, err := client.ListApiKeys()
	if err != nil {
		return err
	}

	if len(keys) == 0 {
		cmd.Println("No API keys found.")
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tCREATED")
	for _, key := range keys {
		fmt.Fprintf(w, "%s\t%s\t%s\n",
			key.ID, key.Name, key.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return w.Flush()
}

func runApiKeyCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	client := NewClient()
	resp, err := client.CreateApiKey(name)
	if err != nil {
		return err
	}

	cmd.Printf("✅ API Key '%s' created.\n", resp.Name)
	cmd.Printf("Key: %s\n", resp.ApiKey)
	cmd.Println("⚠️  Save this key! It will only be shown once.")
	return nil
}

func runApiKeyDelete(cmd *cobra.Command, args []string) error {
	client := NewClient()
	if err := client.DeleteApiKey(args[0]); err != nil {
		return err
	}
	cmd.Println("✅ API Key deleted.")
	return nil
}
