// Package compiler 提供源代码编译服务
// 支持将 Go 和 Rust 源代码编译为可执行文件或 WebAssembly
package compiler

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CompileRequest 编译请求
type CompileRequest struct {
	Runtime string `json:"runtime"` // go1.24 或 wasm
	Code    string `json:"code"`    // 源代码
}

// CompileResponse 编译响应
type CompileResponse struct {
	Binary  string `json:"binary"`           // base64 编码的二进制
	Success bool   `json:"success"`          // 是否成功
	Error   string `json:"error,omitempty"`  // 错误信息
	Output  string `json:"output,omitempty"` // 编译输出
}

// Compiler 编译器服务
type Compiler struct {
	timeout time.Duration
}

// NewCompiler 创建编译器
func NewCompiler() *Compiler {
	return &Compiler{
		timeout: 60 * time.Second,
	}
}

// imageExists checks if a Docker image is available locally
func imageExists(ctx context.Context, image string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", image)
	return cmd.Run() == nil
}

// Compile 编译源代码
func (c *Compiler) Compile(ctx context.Context, req *CompileRequest) (*CompileResponse, error) {
	switch req.Runtime {
	case "go1.24":
		return c.compileGo(ctx, req.Code)
	case "wasm":
		return c.compileRustWasm(ctx, req.Code)
	case "rust1.75":
		return c.compileRust(ctx, req.Code)
	default:
		return &CompileResponse{
			Success: false,
			Error:   fmt.Sprintf("unsupported runtime for compilation: %s", req.Runtime),
		}, nil
	}
}

// compileGo 编译 Go 代码
func (c *Compiler) compileGo(ctx context.Context, code string) (*CompileResponse, error) {
	// Check if the Docker image exists locally
	const goImage = "golang:1.24-alpine"
	if !imageExists(ctx, goImage) {
		return &CompileResponse{
			Success: false,
			Error:   fmt.Sprintf("Docker image %s not found locally. Please pull the image first: docker pull %s", goImage, goImage),
		}, nil
	}

	// 创建临时目录 - use /tmp to ensure Docker can access it on macOS
	tmpDir, err := os.MkdirTemp("/tmp", "nimbus-go-compile-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// 写入源代码
	srcFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(srcFile, []byte(code), 0644); err != nil {
		return nil, fmt.Errorf("failed to write source: %w", err)
	}

	// 初始化 go.mod
	modContent := "module handler\n\ngo 1.24\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(modContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write go.mod: %w", err)
	}

	// 创建带超时的上下文
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// 检测目标架构（Docker Server 的架构）
	goarch := "arm64" // 默认 arm64 (Apple Silicon)
	archCmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Arch}}")
	if archOut, err := archCmd.Output(); err == nil {
		arch := strings.TrimSpace(string(archOut))
		if arch == "x86_64" || arch == "amd64" {
			goarch = "amd64"
		}
	}

	// 使用 Docker 编译 Go
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", tmpDir+":/work",
		"-w", "/work",
		"-e", "CGO_ENABLED=0",
		"-e", "GOOS=linux",
		"-e", "GOARCH="+goarch,
		goImage,
		"go", "build", "-o", "handler", "main.go",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return &CompileResponse{
			Success: false,
			Error:   fmt.Sprintf("compilation failed: %v", err),
			Output:  string(output),
		}, nil
	}

	// 读取编译后的二进制
	outFile := filepath.Join(tmpDir, "handler")
	binary, err := os.ReadFile(outFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read binary: %w", err)
	}

	return &CompileResponse{
		Success: true,
		Binary:  base64.StdEncoding.EncodeToString(binary),
		Output:  string(output),
	}, nil
}

// compileRustWasm 编译 Rust 代码到 WebAssembly
func (c *Compiler) compileRustWasm(ctx context.Context, code string) (*CompileResponse, error) {
	// Use pre-built image with wasm32-unknown-unknown target already installed
	const rustWasmImage = "nimbus-rust-wasm-compiler:latest"
	if !imageExists(ctx, rustWasmImage) {
		return &CompileResponse{
			Success: false,
			Error:   fmt.Sprintf("Docker image %s not found locally. Please build it first: docker build -t %s -f deployments/docker/runtimes/Dockerfile.rust-wasm-compiler deployments/docker/runtimes/", rustWasmImage, rustWasmImage),
		}, nil
	}

	// 创建临时目录 - use /tmp to ensure Docker can access it on macOS
	tmpDir, err := os.MkdirTemp("/tmp", "nimbus-rust-compile-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// 写入源代码
	srcFile := filepath.Join(tmpDir, "handler.rs")
	if err := os.WriteFile(srcFile, []byte(code), 0644); err != nil {
		return nil, fmt.Errorf("failed to write source: %w", err)
	}

	// 输出文件
	outFile := filepath.Join(tmpDir, "handler.wasm")

	// 创建带超时的上下文
	timeout := 5 * time.Minute
	if c.timeout > timeout {
		timeout = c.timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 使用 Docker 编译 Rust - target is pre-installed in the image
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", tmpDir+":/work",
		"-w", "/work",
		rustWasmImage,
		"rustc", "--edition=2021", "--target", "wasm32-unknown-unknown",
		"-O", "-C", "panic=abort", "--crate-type=cdylib",
		"handler.rs", "-o", "handler.wasm",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return &CompileResponse{
			Success: false,
			Error:   fmt.Sprintf("compilation failed: %v", err),
			Output:  string(output),
		}, nil
	}

	// 读取编译后的 wasm
	binary, err := os.ReadFile(outFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read wasm: %w", err)
	}

	return &CompileResponse{
		Success: true,
		Binary:  base64.StdEncoding.EncodeToString(binary),
		Output:  string(output),
	}, nil
}

// compileRust 编译 Rust 代码到原生二进制
func (c *Compiler) compileRust(ctx context.Context, code string) (*CompileResponse, error) {
	// 创建临时目录 - use /tmp to ensure Docker can access it on macOS
	tmpDir, err := os.MkdirTemp("/tmp", "nimbus-rust-native-compile-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// 写入源代码
	srcFile := filepath.Join(tmpDir, "main.rs")
	if err := os.WriteFile(srcFile, []byte(code), 0644); err != nil {
		return nil, fmt.Errorf("failed to write source: %w", err)
	}

	// 输出文件
	outFile := filepath.Join(tmpDir, "handler")

	// 创建带超时的上下文
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// 检测目标架构（Docker Server 的架构）
	rustArch := "aarch64" // 默认 arm64 (Apple Silicon)
	archCmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Arch}}")
	if archOut, err := archCmd.Output(); err == nil {
		arch := strings.TrimSpace(string(archOut))
		if arch == "x86_64" || arch == "amd64" {
			rustArch = "x86_64"
		}
	}

	target := rustArch + "-unknown-linux-musl"

	// Check if the Docker image exists locally to avoid long pull timeouts
	rustImage := "messense/rust-musl-cross:" + rustArch + "-musl"
	if !imageExists(ctx, rustImage) {
		return &CompileResponse{
			Success: false,
			Error:   fmt.Sprintf("Docker image %s not found locally. Please pull the image first: docker pull %s", rustImage, rustImage),
		}, nil
	}

	// 使用 Docker 编译 Rust (musl 静态链接以便在 alpine 运行)
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", tmpDir+":/work",
		"-w", "/work",
		"messense/rust-musl-cross:"+rustArch+"-musl",
		"rustc", "--target", target, "-C", "opt-level=3", "main.rs", "-o", "handler",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return &CompileResponse{
			Success: false,
			Error:   fmt.Sprintf("compilation failed: %v", err),
			Output:  string(output),
		}, nil
	}

	// 读取编译后的二进制
	binary, err := os.ReadFile(outFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read binary: %w", err)
	}

	return &CompileResponse{
		Success: true,
		Binary:  base64.StdEncoding.EncodeToString(binary),
		Output:  string(output),
	}, nil
}

// IsSourceCode 检测代码是否是源代码（而非 base64 二进制）
func IsSourceCode(runtime, code string) bool {
	switch runtime {
	case "go1.24":
		// Go 源代码通常以 package 开头
		return strings.Contains(code, "package ") || strings.Contains(code, "func ")
	case "wasm", "rust1.75":
		// Rust 源代码通常包含 fn 或 pub
		return strings.Contains(code, "fn ") || strings.Contains(code, "#[no_mangle]") || strings.Contains(code, "pub ")
	default:
		return false
	}
}
