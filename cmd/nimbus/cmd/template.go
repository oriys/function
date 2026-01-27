// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 template 命令，用于管理和使用函数模板。
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// templateCmd 管理函数模板
var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Manage and use function templates",
}

var templateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available templates",
	RunE:  runTemplateList,
}

var templateUseCmd = &cobra.Command{
	Use:   "use <template> <name>",
	Short: "Create a new function from a template",
	Args:  cobra.ExactArgs(2),
	RunE:  runTemplateUse,
}

func init() {
	rootCmd.AddCommand(templateCmd)
	templateCmd.AddCommand(templateListCmd)
	templateCmd.AddCommand(templateUseCmd)
}

func runTemplateList(cmd *cobra.Command, args []string) error {
	client := NewClient()
	templates, err := client.ListTemplates()
	if err != nil {
		return err
	}

	printer := NewPrinter()
	return printer.PrintTemplates(templates)
}

func runTemplateUse(cmd *cobra.Command, args []string) error {
	templateName := args[0]
	funcName := args[1]
	
	client := NewClient()
	tpl, err := client.GetTemplate(templateName)
	if err != nil {
		return err
	}

	fmt.Printf("🎨 Using template '%s' to create function '%s'...\n", tpl.DisplayName, funcName)
	
	fn, err := client.CreateFunction(&CreateFunctionRequest{
		Name:    funcName,
		Runtime: tpl.Runtime,
		Handler: tpl.Handler,
		Code:    tpl.Code,
	})
	if err != nil {
		return err
	}

	fmt.Printf("✅ Function '%s' created from template.\n", fn.Name)
	return nil
}
