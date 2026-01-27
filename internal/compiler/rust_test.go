package compiler

import (
	"context"
	"encoding/base64"
	"testing"
)

func TestCompileRustWasm(t *testing.T) {
	// Inline Rust code for testing
	code := `
use std::mem;
use std::slice;
use std::str;

#[no_mangle]
pub extern "C" fn alloc(len: usize) -> *mut u8 {
    let mut buf = Vec::with_capacity(len);
    let ptr = buf.as_mut_ptr();
    mem::forget(buf);
    ptr
}

#[no_mangle]
pub unsafe extern "C" fn handle(ptr: *mut u8, len: usize) -> u64 {
    let input_slice = slice::from_raw_parts(ptr, len);
    let _input_str = str::from_utf8(input_slice).unwrap_or("{}");
    
    let output = "{\"message\": \"Hello from Rust WASM!\"}";
    let output_bytes = output.as_bytes();
    let out_len = output_bytes.len();
    let out_ptr = alloc(out_len);
    
    std::ptr::copy_nonoverlapping(output_bytes.as_ptr(), out_ptr, out_len);
    
    ((out_ptr as u64) << 32) | (out_len as u64)
}
`

	c := NewCompiler()
	ctx := context.Background()

	// Test compilation to WASM
	req := &CompileRequest{
		Runtime: "wasm",
		Code:    code,
	}

	resp, err := c.Compile(ctx, req)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	if !resp.Success {
		t.Fatalf("Compilation failed: %s\nOutput: %s", resp.Error, resp.Output)
	}

	if resp.Binary == "" {
		t.Fatal("Expected binary to be present, got empty string")
	}

	// Verify it's valid base64
	_, err = base64.StdEncoding.DecodeString(resp.Binary)
	if err != nil {
		t.Fatalf("Binary is not valid base64: %v", err)
	}
	
t.Logf("Successfully compiled Rust to WASM. Binary size (base64): %d", len(resp.Binary))
}

func TestIsSourceCodeRust(t *testing.T) {
	code := `
use std::mem;
#[no_mangle]
pub extern "C" fn alloc(len: usize) -> *mut u8 { ... }
`
	if !IsSourceCode("wasm", code) {
		t.Error("Expected code to be detected as source code for wasm")
	}
	
	if !IsSourceCode("rust1.75", code) {
		t.Error("Expected code to be detected as source code for rust1.75")
	}

	notCode := "some random string"
	if IsSourceCode("wasm", notCode) {
		t.Error("Expected random string NOT to be detected as source code")
	}
}
