// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 deploy 命令，用于自动部署函数（创建或更新）。
package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// deployCmd 是 deploy 命令的 cobra.Command 实例。
var deployCmd = &cobra.Command{
	Use:   "deploy <name>",
	Short: "Deploy a function (create if new, update if exists)",
	Long: `Deploy a serverless function automatically.

This command checks if the function already exists:
- If it doesn't exist, it creates a new function.
- If it exists, it updates the existing function's code and configuration.`,
	Args: cobra.ExactArgs(1),
	RunE: runDeploy,
}

var (
	deployRuntime string
	deployHandler string
	deployFile    string
	deployEnv     []string
)

func init() {
	rootCmd.AddCommand(deployCmd)

	deployCmd.Flags().StringVarP(&deployRuntime, "runtime", "r", "", "Runtime (required if creating)")
	deployCmd.Flags().StringVarP(&deployHandler, "handler", "H", "", "Handler function (e.g., main.handler)")
	deployCmd.Flags().StringVarP(&deployFile, "file", "f", "", "Code file path")
	deployCmd.Flags().StringArrayVarP(&deployEnv, "env", "e", nil, "Environment variables (KEY=VALUE)")
}

func runDeploy(cmd *cobra.Command, args []string) error {
	name := args[0]
	client := NewClient()

	// 1. Try to get existing function
	exists := true
	fn, err := client.GetFunction(name)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			exists = false
		} else {
			return err
		}
	}

	// 2. Prepare code
	if deployFile == "" {
		// Try to guess file
		if deployRuntime == "" && exists {
			deployRuntime = fn.Runtime
		}
		
		switch deployRuntime {
		case "python3.11":
			deployFile = "handler.py"
		case "nodejs20":
			deployFile = "index.js"
		case "go1.24":
			deployFile = "main.go"
		}
	}

	if deployFile == "" {
		return fmt.Errorf("--file is required or must be guessable from runtime")
	}

	data, err := os.ReadFile(deployFile)
	if err != nil {
		return fmt.Errorf("failed to read code file: %w", err)
	}
	code := string(data)

	// 3. Parse env vars
	envVars := make(map[string]string)
	for _, env := range deployEnv {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			envVars[parts[0]] = parts[1]
		}
	}

	if !exists {
		// Create new
		if deployRuntime == "" {
			return fmt.Errorf("--runtime is required when creating a new function")
		}
		if deployHandler == "" {
			// Guess handler
			switch deployRuntime {
			case "python3.11":
				deployHandler = "handler.handler"
			case "nodejs20":
				deployHandler = "index.handler"
			case "go1.24":
				deployHandler = "main.Handler"
			}
		}

		cmd.Printf("🚀 Creating new function '%s'...\n", name)
		fn, err = client.CreateFunction(&CreateFunctionRequest{
			Name:     name,
			Runtime:  deployRuntime,
			Handler:  deployHandler,
			Code:     code,
			EnvVars:  envVars,
		})
	} else {
		// Update existing
		cmd.Printf("🔄 Updating existing function '%s'...\n", name)
		req := &UpdateFunctionRequest{
			Code: &code,
		}
		if deployHandler != "" {
			req.Handler = &deployHandler
		}
		if len(envVars) > 0 {
			req.EnvVars = &envVars
		}
		fn, err = client.UpdateFunction(name, req)
	}

	if err != nil {
		return err
	}

	cmd.Printf("✅ Function '%s' deployed successfully.\n", fn.Name)
	return nil
}
