//go:build !linux
// +build !linux

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "scheduler service is only supported on Linux (Firecracker runtime)")
	os.Exit(1)
}
