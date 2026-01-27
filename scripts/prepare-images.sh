#!/bin/bash
# Prepare all Docker images required for Nimbus
# This includes compiler images and runtime images

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
RUNTIME_DIR="$PROJECT_ROOT/deployments/docker/runtimes"

echo "=== Preparing Nimbus Docker Images ==="
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

success() { echo -e "${GREEN}✓${NC} $1"; }
info() { echo -e "${YELLOW}→${NC} $1"; }
error() { echo -e "${RED}✗${NC} $1"; }

# Detect architecture
ARCH=$(docker version --format '{{.Server.Arch}}')
if [ "$ARCH" = "x86_64" ] || [ "$ARCH" = "amd64" ]; then
    RUST_MUSL_ARCH="x86_64"
else
    RUST_MUSL_ARCH="aarch64"
fi

echo "Detected architecture: $ARCH (Rust musl: $RUST_MUSL_ARCH)"
echo ""

# ============================================
# Compiler Images
# ============================================
echo "=== Compiler Images ==="

# Go compiler
info "Pulling golang:1.24-alpine..."
if docker pull golang:1.24-alpine > /dev/null 2>&1; then
    success "golang:1.24-alpine"
else
    error "Failed to pull golang:1.24-alpine"
fi

# Rust WASM compiler (custom image with wasm32-unknown-unknown pre-installed)
info "Building nimbus-rust-wasm-compiler:latest..."
if docker build -t nimbus-rust-wasm-compiler:latest \
    -f "$RUNTIME_DIR/Dockerfile.rust-wasm-compiler" \
    "$RUNTIME_DIR" > /dev/null 2>&1; then
    success "nimbus-rust-wasm-compiler:latest"
else
    error "Failed to build nimbus-rust-wasm-compiler:latest"
fi

# Rust native compiler (musl cross-compile)
info "Pulling messense/rust-musl-cross:${RUST_MUSL_ARCH}-musl..."
if docker pull "messense/rust-musl-cross:${RUST_MUSL_ARCH}-musl" > /dev/null 2>&1; then
    success "messense/rust-musl-cross:${RUST_MUSL_ARCH}-musl"
else
    error "Failed to pull messense/rust-musl-cross:${RUST_MUSL_ARCH}-musl"
fi

echo ""

# ============================================
# Runtime Images
# ============================================
echo "=== Runtime Images ==="

# Python runtime
info "Building nimbus-runtime-python3.11:latest..."
if docker build -t nimbus-runtime-python3.11:latest \
    -f "$RUNTIME_DIR/Dockerfile.python3.11" \
    "$RUNTIME_DIR" > /dev/null 2>&1; then
    success "nimbus-runtime-python3.11:latest"
else
    error "Failed to build nimbus-runtime-python3.11:latest"
fi

# Node.js runtime
info "Building nimbus-runtime-nodejs20:latest..."
if docker build -t nimbus-runtime-nodejs20:latest \
    -f "$RUNTIME_DIR/Dockerfile.nodejs20" \
    "$RUNTIME_DIR" > /dev/null 2>&1; then
    success "nimbus-runtime-nodejs20:latest"
else
    error "Failed to build nimbus-runtime-nodejs20:latest"
fi

# Go runtime
info "Building nimbus-runtime-go1.24:latest..."
if docker build -t nimbus-runtime-go1.24:latest \
    -f "$RUNTIME_DIR/Dockerfile.go1.24" \
    "$RUNTIME_DIR" > /dev/null 2>&1; then
    success "nimbus-runtime-go1.24:latest"
else
    error "Failed to build nimbus-runtime-go1.24:latest"
fi

# WASM runtime
info "Building nimbus-runtime-wasm:latest..."
if docker build -t nimbus-runtime-wasm:latest \
    -f "$RUNTIME_DIR/Dockerfile.wasm" \
    "$RUNTIME_DIR" > /dev/null 2>&1; then
    success "nimbus-runtime-wasm:latest"
else
    error "Failed to build nimbus-runtime-wasm:latest"
fi

echo ""
echo "=== Summary ==="
echo "Compiler images:"
docker images --format "  {{.Repository}}:{{.Tag}}\t{{.Size}}" | grep -E "golang:1.24|nimbus-rust-wasm-compiler|rust-musl-cross" || true
echo ""
echo "Runtime images:"
docker images --format "  {{.Repository}}:{{.Tag}}\t{{.Size}}" | grep "nimbus-runtime" || true
echo ""
echo "=== Done ==="
