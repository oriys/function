//go:build docker_runtime_go
// +build docker_runtime_go

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
)

type Input struct {
	Handler string            `json:"handler"`
	Code    string            `json:"code"` // base64 encoded binary
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

	// Set environment variables
	for key, value := range input.Env {
		os.Setenv(key, value)
	}

	// Decode binary
	binary, err := base64.StdEncoding.DecodeString(input.Code)
	if err != nil {
		fatal("failed to decode binary: " + err.Error())
	}

	// Write binary to temp file
	tmpFile, err := os.CreateTemp("", "handler-*")
	if err != nil {
		fatal("failed to create temp file: " + err.Error())
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(binary); err != nil {
		fatal("failed to write binary: " + err.Error())
	}
	tmpFile.Close()

	// Make executable
	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		fatal("failed to chmod: " + err.Error())
	}

	// Execute
	cmd := exec.Command(tmpFile.Name())
	cmd.Stdin = bytes.NewReader(input.Payload)
	cmd.Stderr = os.Stderr

	output, err := cmd.Output()
	if err != nil {
		fatal("execution failed: " + err.Error())
	}

	fmt.Print(string(output))
}

func fatal(msg string) {
	fmt.Fprintf(os.Stderr, `{"error":%q}`, msg)
	os.Exit(1)
}
