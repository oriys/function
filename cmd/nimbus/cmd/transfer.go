// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 export 和 import 命令，用于函数的迁移和备份。
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// exportCmd 导出函数配置到文件
var exportCmd = &cobra.Command{
	Use:   "export <name>",
	Short: "Export function configuration to a YAML file",
	Args:  cobra.ExactArgs(1),
	RunE:  runExport,
}

// importCmd 从文件导入或恢复函数
var importCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "Import or restore a function from a YAML file",
	Args:  cobra.ExactArgs(1),
	RunE:  runImport,
}

func init() {
	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(importCmd)
}

func runExport(cmd *cobra.Command, args []string) error {
	name := args[0]
	client := NewClient()
	
	fn, err := client.GetFunction(name)
	if err != nil {
		return err
	}

	// 准备导出结构
	exportData := CreateFunctionRequest{
		Name:           fn.Name,
		Runtime:        fn.Runtime,
		Handler:        fn.Handler,
		Code:           fn.Code,
		MemoryMB:       fn.MemoryMB,
		TimeoutSec:     fn.TimeoutSec,
		EnvVars:        fn.EnvVars,
		CronExpression: fn.CronExpression,
		HTTPPath:       fn.HTTPPath,
		HTTPMethods:    fn.HTTPMethods,
	}

	data, err := yaml.Marshal(exportData)
	if err != nil {
		return err
	}

	fileName := fmt.Sprintf("%s.yaml", fn.Name)
	if err := os.WriteFile(fileName, data, 0644); err != nil {
		return err
	}

	fmt.Printf("✅ Function '%s' exported to %s\n", name, fileName)
	return nil
}

func runImport(cmd *cobra.Command, args []string) error {
	file := args[0]
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}

	var req CreateFunctionRequest
	if err := yaml.Unmarshal(data, &req); err != nil {
		return err
	}

	client := NewClient()
	fmt.Printf("🚀 Importing function '%s'...\n", req.Name)
	
	// 尝试先删除已存在的（可选，或者调用 deploy 逻辑）
	fn, err := client.CreateFunction(&req)
	if err != nil {
		return fmt.Errorf("import failed: %w", err)
	}

	fmt.Printf("✅ Function '%s' imported successfully (ID: %s)\n", fn.Name, fn.ID)
	return nil
}
