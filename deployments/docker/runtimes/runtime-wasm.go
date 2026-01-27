//go:build docker_runtime_wasm
// +build docker_runtime_wasm

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type Input struct {
	Handler string            `json:"handler"`
	Code    string            `json:"code"` // base64 encoded wasm
	Payload json.RawMessage   `json:"payload"`
	Env     map[string]string `json:"env"`
}

func main() {
	// Read input from stdin
	inputBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		fatal("failed to read stdin: " + err.Error())
	}

	var input Input
	if err := json.Unmarshal(inputBytes, &input); err != nil {
		fatal("failed to parse input: " + err.Error())
	}

	// Decode wasm binary
	wasmBytes, err := base64.StdEncoding.DecodeString(input.Code)
	if err != nil {
		fatal("failed to decode wasm: " + err.Error())
	}

	// Execute wasm
	output, err := executeWasm(wasmBytes, input.Payload)
	if err != nil {
		fatal("wasm execution failed: " + err.Error())
	}

	fmt.Print(string(output))
}

func executeWasm(wasmBytes []byte, payload json.RawMessage) ([]byte, error) {
	ctx := context.Background()

	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)

	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)

	module, err := runtime.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("compile failed: %w", err)
	}

	instance, err := runtime.InstantiateModule(ctx, module, wazero.NewModuleConfig())
	if err != nil {
		return nil, fmt.Errorf("instantiate failed: %w", err)
	}
	defer instance.Close(ctx)

	// Get exported functions
	alloc := instance.ExportedFunction("alloc")
	handle := instance.ExportedFunction("handle")

	if alloc == nil || handle == nil {
		return nil, fmt.Errorf("wasm must export 'alloc' and 'handle' functions")
	}

	// Allocate input buffer
	inputBytes := []byte(payload)
	results, err := alloc.Call(ctx, uint64(len(inputBytes)))
	if err != nil {
		return nil, fmt.Errorf("alloc failed: %w", err)
	}
	inputPtr := uint32(results[0])

	// Write input to memory
	memory := instance.Memory()
	if !memory.Write(inputPtr, inputBytes) {
		return nil, fmt.Errorf("failed to write to memory")
	}

	// Call handle
	results, err = handle.Call(ctx, uint64(inputPtr), uint64(len(inputBytes)))
	if err != nil {
		return nil, fmt.Errorf("handle failed: %w", err)
	}

	// Parse result
	packed := results[0]
	outPtr := uint32(packed >> 32)
	outLen := uint32(packed & 0xFFFFFFFF)

	output, ok := memory.Read(outPtr, outLen)
	if !ok {
		return nil, fmt.Errorf("failed to read output")
	}

	return output, nil
}

func fatal(msg string) {
	fmt.Fprintf(os.Stderr, `{"error":%q}`, msg)
	os.Exit(1)
}

// Ensure api.Module is used (for compilation)
var _ api.Module
