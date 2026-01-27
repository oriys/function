// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 init 命令，用于初始化一个新的函数项目。
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// initCmd 是 init 命令的 cobra.Command 实例。
var initCmd = &cobra.Command{
	Use:   "init [name]",
	Short: "Initialize a new function project",
	Long: `Initialize a new function project by creating a starter file.

This command creates a boilerplate function handler in the current directory or
a specified subdirectory.

Examples:
  # Initialize a Python function in the current directory
  nimbus init my-func --runtime python3.11

  # Initialize a Go function
  nimbus init my-func --runtime go1.24`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

var (
	initRuntime string
	initDir     string
)

func init() {
	rootCmd.AddCommand(initCmd)

	initCmd.Flags().StringVarP(&initRuntime, "runtime", "r", "python3.11", "Runtime (python3.11, nodejs20, go1.24)")
	initCmd.Flags().StringVarP(&initDir, "dir", "d", ".", "Directory to initialize")
}

func runInit(cmd *cobra.Command, args []string) error {
	name := "hello"
	if len(args) > 0 {
		name = args[0]
	}

	// Create directory if it doesn't exist
	if initDir != "." {
		if err := os.MkdirAll(initDir, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
	}

	var filename, content, handler string

	switch initRuntime {
	case "python3.11":
		filename = "handler.py"
		handler = "handler.handler"
		content = `def handler(event, context):
    """
    Nimbus Python Function Handler
    
    :param event: The event data (dict)
    :param context: The execution context
    :return: The response data (dict)
    """
    name = event.get("name", "World")
    return {
        "message": f"Hello, {name} from Nimbus!",
        "runtime": "python3.11"
    }
`
	case "nodejs20":
		filename = "index.js"
		handler = "index.handler"
		content = `/**
 * Nimbus Node.js Function Handler
 * 
 * @param {Object} event - The event data
 * @param {Object} context - The execution context
 * @returns {Promise<Object>} The response data
 */
exports.handler = async (event, context) => {
    const name = event.name || "World";
    return {
        message: "Hello, " + name + " from Nimbus!",
        runtime: "nodejs20"
    };
};
`
	case "go1.24":
		filename = "main.go"
		handler = "main.Handler"
		content = `package main

import (
	"fmt"
)

// Context defines the execution context
type Context struct {
	FunctionName string
}

// Handler is the entry point for the Nimbus function
func Handler(event map[string]interface{}, ctx *Context) (map[string]interface{}, error) {
	name, ok := event["name"].(string)
	if !ok {
		name = "World"
	}

	return map[string]interface{}{
		"message": fmt.Sprintf("Hello, %s from Nimbus!", name),
		"runtime": "go1.24",
	}, nil
}
`
	default:
		return fmt.Errorf("unsupported runtime: %s", initRuntime)
	}

	filePath := filepath.Join(initDir, filename)
	
	// Check if file already exists
	if _, err := os.Stat(filePath); err == nil {
		return fmt.Errorf("file already exists: %s", filePath)
	}

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	fmt.Printf("✨ Initialized %s function '%s' in %s\n", initRuntime, name, filePath)
	fmt.Println("\nNext steps:")
	fmt.Printf("  1. Run locally:  nimbus local %s --runtime %s --handler %s --watch\n", filePath, initRuntime, handler)
	fmt.Printf("  2. Test it:      curl -X POST http://localhost:9000/invoke -d '{\"name\": \"Nimbus\"}'\n")
	fmt.Printf("  3. Deploy:       nimbus create %s --runtime %s --handler %s --file %s\n", name, initRuntime, handler, filePath)

	return nil
}
