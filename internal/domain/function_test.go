// Package domain 定义了函数计算平台的核心领域模型。
package domain

import (
	"testing"
)

// TestCreateFunctionRequest_Validate 测试 CreateFunctionRequest 的验证方法。
// 该测试覆盖了各种有效和无效的输入场景，包括：
// - 有效的请求参数
// - 无效的函数名称
// - 无效的运行时
// - 无效的处理器
// - 内存配置超出范围
// - 超时配置超出范围
func TestCreateFunctionRequest_Validate(t *testing.T) {
	// tests 定义了测试用例切片
	tests := []struct {
		name    string                // 测试用例名称
		req     CreateFunctionRequest // 测试输入的请求对象
		wantErr bool                  // 是否期望返回错误
	}{
		{
			// 测试用例：有效的请求参数
			name: "valid request",
			req: CreateFunctionRequest{
				Name:       "test-function",
				Runtime:    "python3.11",
				Handler:    "handler.main",
				Code:       "def main(event): return {}",
				MemoryMB:   256,
				TimeoutSec: 30,
			},
			wantErr: false,
		},
		{
			// 测试用例：函数名称为空
			name: "empty name",
			req: CreateFunctionRequest{
				Name:       "",
				Runtime:    "python3.11",
				Handler:    "handler.main",
				Code:       "def main(event): return {}",
				MemoryMB:   256,
				TimeoutSec: 30,
			},
			wantErr: true,
		},
		{
			// 测试用例：无效的运行时类型
			name: "invalid runtime",
			req: CreateFunctionRequest{
				Name:       "test-function",
				Runtime:    "invalid-runtime",
				Handler:    "handler.main",
				Code:       "def main(event): return {}",
				MemoryMB:   256,
				TimeoutSec: 30,
			},
			wantErr: true,
		},
		{
			// 测试用例：处理器为空
			name: "empty handler",
			req: CreateFunctionRequest{
				Name:       "test-function",
				Runtime:    "python3.11",
				Handler:    "",
				Code:       "def main(event): return {}",
				MemoryMB:   256,
				TimeoutSec: 30,
			},
			wantErr: true,
		},
		{
			// 测试用例：内存配置过低（低于 128MB）
			name: "memory too low",
			req: CreateFunctionRequest{
				Name:       "test-function",
				Runtime:    "python3.11",
				Handler:    "handler.main",
				Code:       "def main(event): return {}",
				MemoryMB:   64,
				TimeoutSec: 30,
			},
			wantErr: true,
		},
		{
			// 测试用例：内存配置过高（超过 3072MB）
			name: "memory too high",
			req: CreateFunctionRequest{
				Name:       "test-function",
				Runtime:    "python3.11",
				Handler:    "handler.main",
				Code:       "def main(event): return {}",
				MemoryMB:   4096,
				TimeoutSec: 30,
			},
			wantErr: true,
		},
		{
			// 测试用例：超时时间为 0 时应设置默认值 30 秒
			name: "timeout zero defaults to 30",
			req: CreateFunctionRequest{
				Name:       "test-function",
				Runtime:    "python3.11",
				Handler:    "handler.main",
				Code:       "def main(event): return {}",
				MemoryMB:   256,
				TimeoutSec: 0,
			},
			wantErr: false,
		},
		{
			// 测试用例：超时时间过长（超过 300 秒）
			name: "timeout too high",
			req: CreateFunctionRequest{
				Name:       "test-function",
				Runtime:    "python3.11",
				Handler:    "handler.main",
				Code:       "def main(event): return {}",
				MemoryMB:   256,
				TimeoutSec: 400,
			},
			wantErr: true,
		},
		{
			// 测试用例：有效的 Node.js 运行时
			name: "valid nodejs runtime",
			req: CreateFunctionRequest{
				Name:       "test-function",
				Runtime:    "nodejs20",
				Handler:    "index.handler",
				Code:       "exports.handler = async (event) => { return {}; }",
				MemoryMB:   256,
				TimeoutSec: 30,
			},
			wantErr: false,
		},
		{
			// 测试用例：有效的 Go 运行时
			name: "valid go runtime",
			req: CreateFunctionRequest{
				Name:       "test-function",
				Runtime:    "go1.24",
				Handler:    "main",
				Code:       "package main",
				MemoryMB:   128,
				TimeoutSec: 30,
			},
			wantErr: false,
		},
	}

	// 遍历所有测试用例
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestRuntime_IsValid 测试 Runtime 类型的 IsValid 方法。
// 该测试验证各种运行时类型是否被正确识别为有效或无效。
func TestRuntime_IsValid(t *testing.T) {
	// tests 定义了测试用例切片
	tests := []struct {
		runtime Runtime // 测试的运行时类型
		want    bool    // 期望的返回值
	}{
		{RuntimePython311, true},  // Python 3.11 应该是有效的
		{RuntimeNodeJS20, true},   // Node.js 20 应该是有效的
		{RuntimeGo124, true},      // Go 1.24 应该是有效的
		{Runtime("python3.10"), false}, // Python 3.10 不受支持
		{Runtime("nodejs18"), false},   // Node.js 18 不受支持
		{Runtime("java"), false},       // Java 不受支持
		{Runtime(""), false},           // 空字符串无效
	}

	// 遍历所有测试用例
	for _, tt := range tests {
		t.Run(string(tt.runtime), func(t *testing.T) {
			if got := tt.runtime.IsValid(); got != tt.want {
				t.Errorf("Runtime(%q).IsValid() = %v, want %v", tt.runtime, got, tt.want)
			}
		})
	}
}

// TestValidateCodeSize 测试代码大小验证
func TestValidateCodeSize(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		wantErr bool
	}{
		{
			name:    "empty code",
			code:    "",
			wantErr: false,
		},
		{
			name:    "small code",
			code:    "def handler(event): return {}",
			wantErr: false,
		},
		{
			name:    "code at limit",
			code:    string(make([]byte, MaxCodeSize)),
			wantErr: false,
		},
		{
			name:    "code exceeds limit",
			code:    string(make([]byte, MaxCodeSize+1)),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCodeSize(tt.code)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCodeSize() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateBinarySize 测试二进制大小验证
func TestValidateBinarySize(t *testing.T) {
	tests := []struct {
		name    string
		binary  string
		wantErr bool
	}{
		{
			name:    "empty binary",
			binary:  "",
			wantErr: false,
		},
		{
			name:    "small binary",
			binary:  "some-base64-encoded-binary",
			wantErr: false,
		},
		{
			name:    "binary at limit",
			binary:  string(make([]byte, MaxBinarySize)),
			wantErr: false,
		},
		{
			name:    "binary exceeds limit",
			binary:  string(make([]byte, MaxBinarySize+1)),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBinarySize(tt.binary)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateBinarySize() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestCreateFunctionRequest_Validate_CodeSize 测试创建函数请求中的代码大小验证
func TestCreateFunctionRequest_Validate_CodeSize(t *testing.T) {
	tests := []struct {
		name    string
		req     CreateFunctionRequest
		wantErr bool
	}{
		{
			name: "code exceeds limit",
			req: CreateFunctionRequest{
				Name:       "test-function",
				Runtime:    "python3.11",
				Handler:    "handler.main",
				Code:       string(make([]byte, MaxCodeSize+1)),
				MemoryMB:   256,
				TimeoutSec: 30,
			},
			wantErr: true,
		},
		{
			name: "binary exceeds limit",
			req: CreateFunctionRequest{
				Name:       "test-function",
				Runtime:    "go1.24",
				Handler:    "main",
				Code:       "package main",
				Binary:     string(make([]byte, MaxBinarySize+1)),
				MemoryMB:   256,
				TimeoutSec: 30,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
