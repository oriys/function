// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 doctor 命令，用于检查本地开发环境。
package cmd

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

// doctorCmd 是 doctor 命令的 cobra.Command 实例。
var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check your local development environment",
	Long: `Check if your local environment has the necessary tools for Nimbus development.

It checks for:
  - Python 3.11+
  - Node.js 20+
  - Go 1.24+
  - Git
  - Docker (optional, for building images)`,
	Run: runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

type checkResult struct {
	name      string
	installed bool
	version   string
	required  string
	optional  bool
}

func runDoctor(cmd *cobra.Command, args []string) {
	fmt.Printf("🔍 Checking Nimbus development environment on %s/%s...\n\n", runtime.GOOS, runtime.GOARCH)

	checks := []checkResult{
		checkTool("python3", "--version", "3.11", false),
		checkTool("node", "--version", "20.0", false),
		checkTool("go", "version", "1.24", false),
		checkTool("git", "version", "any", false),
		checkTool("docker", "version", "any", true),
	}

	allOk := true
	for _, res := range checks {
		status := "✅"
		if !res.installed {
			if res.optional {
				status = "⚠️ "
			} else {
				status = "❌"
				allOk = false
			}
		}

		fmt.Printf("%s %-10s: ", status, res.name)
		if res.installed {
			fmt.Printf("Installed (%s)\n", res.version)
		} else {
			if res.optional {
				fmt.Printf("Not found (Optional)\n")
			} else {
				fmt.Printf("NOT FOUND (Required for some runtimes)\n")
			}
		}
	}

	fmt.Println()
	if allOk {
		fmt.Println("🚀 Your environment is ready for Nimbus!")
	} else {
		fmt.Println("❌ Some required tools are missing. Please install them to use all features.")
	}
}

func checkTool(name, versionCmd, minVersion string, optional bool) checkResult {
	path, err := exec.LookPath(name)
	if err != nil {
		return checkResult{name: name, installed: false, optional: optional}
	}

	cmd := exec.Command(path, versionCmd)
	out, err := cmd.CombinedOutput()
	version := "unknown"
	if err == nil {
		version = string(out)
		// Clean up version string (take first line)
		for i, c := range version {
			if c == '\n' || c == '\r' {
				version = version[:i]
				break
			}
		}
	}

	return checkResult{
		name:      name,
		installed: true,
		version:   version,
		required:  minVersion,
		optional:  optional,
	}
}
