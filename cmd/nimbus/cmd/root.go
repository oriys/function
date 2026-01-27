// Package cmd 包含 nimbus CLI 工具的所有命令实现
// 使用 cobra 框架构建命令行接口
package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// 全局命令行标志变量
var (
	cfgFile   string // 配置文件路径
	apiURL    string // API 服务器地址
	outputFmt string // 输出格式（table/json/yaml）
)

// rootCmd 是 CLI 的根命令
// 所有子命令都挂载在这个根命令下
var rootCmd = &cobra.Command{
	Use:   "nimbus",
	Short: "Nimbus - Firecracker Serverless CLI",
	Long: `nimbus 是用于管理基于 Firecracker 的无服务器函数平台的命令行工具。

使用示例:
  # 从文件创建函数
  nimbus create hello --runtime python3.11 --handler main.handler --file handler.py

  # 列出所有函数
  nimbus list

  # 调用函数
  nimbus invoke hello --data '{"name": "World"}'

  # 查看函数日志
  nimbus logs hello --follow`,
}

// Execute 执行根命令
// 这是 CLI 的入口函数，由 main 包调用
//
// 返回:
//   - error: 命令执行错误
func Execute() error {
	return rootCmd.Execute()
}

// init 初始化命令行工具
// 注册全局标志和配置初始化函数
func init() {
	// 在命令执行前初始化配置
	cobra.OnInitialize(initConfig)

	// 注册持久化标志（所有子命令都可使用）
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "配置文件路径（默认为 $HOME/.nimbus.yaml）")
	rootCmd.PersistentFlags().StringVarP(&apiURL, "api-url", "u", "http://localhost:8080", "API 服务器地址")
	rootCmd.PersistentFlags().StringVarP(&outputFmt, "output", "o", "table", "输出格式（table、json、yaml）")

	// 将标志绑定到 viper 配置
	viper.BindPFlag("api_url", rootCmd.PersistentFlags().Lookup("api-url"))
	viper.BindPFlag("output", rootCmd.PersistentFlags().Lookup("output"))
}

// initConfig 初始化配置
// 按优先级加载配置：命令行标志 > 环境变量 > 配置文件
func initConfig() {
	if cfgFile != "" {
		// 使用用户指定的配置文件
		viper.SetConfigFile(cfgFile)
	} else {
		// 获取用户主目录
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		// 搜索配置文件的路径
		viper.AddConfigPath(home) // 在主目录查找
		viper.AddConfigPath(".")  // 在当前目录查找
		viper.SetConfigType("yaml")
		viper.SetConfigName(".nimbus") // 配置文件名（不含扩展名）
	}

	// 设置环境变量前缀
	// 环境变量格式：NIMBUS_<KEY>，如 NIMBUS_API_URL
	viper.SetEnvPrefix("NIMBUS")
	viper.AutomaticEnv() // 自动读取环境变量

	// 兼容旧环境变量前缀：FN_*
	_ = viper.BindEnv("api_url", "NIMBUS_API_URL", "FN_API_URL")
	_ = viper.BindEnv("output", "NIMBUS_OUTPUT", "FN_OUTPUT")

	// 读取配置文件（如果存在），兼容旧配置文件 ~/.fn.yaml
	if err := viper.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) {
			viper.SetConfigName(".fn")
			_ = viper.ReadInConfig()
		}
	}
}

// getConfigPath 获取配置文件的完整路径
// 如果未指定配置文件，返回默认路径
//
// 返回:
//   - string: 配置文件路径
func getConfigPath() string {
	if cfgFile != "" {
		return cfgFile
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".nimbus.yaml")
}
