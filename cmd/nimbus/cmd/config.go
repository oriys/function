// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 config 命令及其子命令，用于管理 CLI 配置。
//
// 支持的子命令：
//   - config view: 查看当前配置
//   - config set:  设置配置项
//   - config init: 初始化配置文件
//
// 配置文件默认存储在 ~/.nimbus.yaml，支持的配置项包括：
//   - api_url: API 服务器地址
//   - output:  默认输出格式（table/json/yaml）
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// configCmd 是 config 命令的 cobra.Command 实例。
// 该命令是配置管理的父命令，包含 view、set、init 等子命令。
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage CLI configuration",
	Long: `Manage the nimbus CLI configuration.

The configuration file is stored at ~/.nimbus.yaml by default.`,
}

// configViewCmd 是 config view 子命令的 cobra.Command 实例。
// 用于显示当前的配置内容和配置文件路径。
var configViewCmd = &cobra.Command{
	Use:   "view",
	Short: "View current configuration",
	RunE:  runConfigView,
}

// configSetCmd 是 config set 子命令的 cobra.Command 实例。
// 用于设置单个配置项的值。
var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Long: `Set a configuration value.

Available keys:
  api_url   - API server URL (default: http://localhost:8080)
  output    - Default output format (table, json, yaml)

Examples:
  nimbus config set api_url http://api.example.com:8080
  nimbus config set output json`,
	Args: cobra.ExactArgs(2),
	RunE: runConfigSet,
}

// configInitCmd 是 config init 子命令的 cobra.Command 实例。
// 用于创建默认配置文件。
var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize configuration file",
	Long: `Create a new configuration file with default values.

Examples:
  nimbus config init
  nimbus config init --api-url http://api.example.com:8080`,
	RunE: runConfigInit,
}

// configInitAPIURL 是 config init 命令的 API URL 参数。
var configInitAPIURL string

// init 注册 config 命令及其子命令，并设置命令行标志。
func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configViewCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configInitCmd)

	configInitCmd.Flags().StringVar(&configInitAPIURL, "api-url", "http://localhost:8080", "API server URL")
}

// runConfigView 是 config view 命令的执行函数。
// 该函数读取当前配置并以 YAML 格式显示，同时显示配置文件的路径。
// 如果没有找到配置，会提示用户运行 config init 命令。
//
// 参数：
//   - cmd: cobra 命令对象
//   - args: 命令行参数（此命令不需要参数）
//
// 返回值：
//   - error: 读取配置失败时返回错误信息
func runConfigView(cmd *cobra.Command, args []string) error {
	settings := viper.AllSettings()

	if len(settings) == 0 {
		fmt.Println("No configuration found.")
		fmt.Println("Run 'nimbus config init' to create a configuration file.")
		return nil
	}

	data, err := yaml.Marshal(settings)
	if err != nil {
		return err
	}

	fmt.Printf("Configuration file: %s\n\n", getConfigPath())
	fmt.Println(string(data))
	return nil
}

// runConfigSet 是 config set 命令的执行函数。
// 该函数设置指定配置项的值并保存到配置文件。
// 只支持预定义的配置项（api_url 和 output）。
//
// 参数：
//   - cmd: cobra 命令对象
//   - args: 命令行参数，args[0] 是配置项名称，args[1] 是配置值
//
// 返回值：
//   - error: 设置配置失败时返回错误信息
func runConfigSet(cmd *cobra.Command, args []string) error {
	key := args[0]
	value := args[1]

	// 验证配置项名称是否有效
	validKeys := map[string]bool{
		"api_url": true,
		"output":  true,
	}

	if !validKeys[key] {
		return fmt.Errorf("unknown configuration key: %s", key)
	}

	viper.Set(key, value)

	configPath := getConfigPath()
	if err := viper.WriteConfigAs(configPath); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	fmt.Printf("Set %s = %s\n", key, value)
	return nil
}

// runConfigInit 是 config init 命令的执行函数。
// 该函数创建一个包含默认值的新配置文件。
// 如果配置文件已存在，会返回错误以防止覆盖。
//
// 参数：
//   - cmd: cobra 命令对象
//   - args: 命令行参数（此命令不需要参数）
//
// 返回值：
//   - error: 初始化配置失败时返回错误信息
func runConfigInit(cmd *cobra.Command, args []string) error {
	configPath := getConfigPath()

	// Check if config already exists
	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf("configuration file already exists at %s", configPath)
	}

	config := map[string]interface{}{
		"api_url": configInitAPIURL,
		"output":  "table",
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	fmt.Printf("Configuration file created at %s\n\n", configPath)
	fmt.Println(string(data))
	return nil
}
