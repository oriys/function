#!/bin/bash
# ============================================================================
# Nimbus Platform - OrbStack 一键启动脚本
# ============================================================================
# 用法:
#   ./start.sh [--profile orbstack|kvm|auto] [--context <ctx>] [--image <gateway-image>] [--rebuild] [--skip-images]
#   --rebuild     强制重新构建所有镜像
#   --skip-images 跳过镜像构建（假设镜像已存在）
#   --profile     选择部署 profile：
#                - orbstack: 本地开发（Docker 模式 + DinD）
#                - kvm:      Linux(KVM) 集群（Firecracker 模式）
#                - auto:     根据当前 kubectl context 自动选择（默认）
#   --context     指定 kubectl context
#   --image       覆盖 gateway 镜像（仅对 kvm profile 生效，直接 set image）
# ============================================================================

set -e
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../../../.." && pwd)"
NAMESPACE="nimbus"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# 解析参数
REBUILD=false
SKIP_IMAGES=false
PROFILE="auto"
KUBE_CONTEXT=""
GATEWAY_IMAGE=""

usage() {
    cat <<EOF
Usage: ./start.sh [--profile orbstack|kvm|auto] [--context <ctx>] [--image <gateway-image>] [--rebuild] [--skip-images]

Examples:
  # OrbStack 本地开发（Docker 模式）
  ./start.sh --profile orbstack

  # Linux(KVM) 集群（Firecracker 模式）
  ./start.sh --profile kvm --context my-cluster
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --rebuild) REBUILD=true; shift ;;
        --skip-images) SKIP_IMAGES=true; shift ;;
        --profile) PROFILE="${2:-}"; shift 2 ;;
        --context) KUBE_CONTEXT="${2:-}"; shift 2 ;;
        --image) GATEWAY_IMAGE="${2:-}"; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) log_error "未知参数: $1"; usage; exit 1 ;;
    esac
done

echo ""
echo "=============================================="
echo "   Nimbus Platform - OrbStack 一键启动"
echo "=============================================="
echo ""

# 检查 kubectl
log_info "检查依赖..."
command -v kubectl >/dev/null 2>&1 || { log_error "kubectl 未安装"; exit 1; }

# 切换 context（可选）
if [ -n "$KUBE_CONTEXT" ]; then
    log_info "切换 context: $KUBE_CONTEXT"
    kubectl config use-context "$KUBE_CONTEXT" >/dev/null || { log_error "无法切换到 context: $KUBE_CONTEXT"; exit 1; }
fi

CURRENT_CONTEXT="$(kubectl config current-context 2>/dev/null || true)"
if [ -z "$CURRENT_CONTEXT" ]; then
    log_error "无法获取 kubectl current-context"
    exit 1
fi

# auto: 根据 context 选择
if [ "$PROFILE" = "auto" ]; then
    if echo "$CURRENT_CONTEXT" | grep -qi "orbstack"; then
        PROFILE="orbstack"
    else
        PROFILE="kvm"
    fi
fi

# kvm: 直接转发到 kvm overlay 的 start.sh
if [ "$PROFILE" = "kvm" ]; then
    log_success "Kubernetes context: $CURRENT_CONTEXT (profile=kvm)"
    KVM_START="$PROJECT_ROOT/deployments/k8s/overlays/kvm/start.sh"
    if [ ! -x "$KVM_START" ]; then
        log_error "找不到 kvm start 脚本: $KVM_START"
        exit 1
    fi
    args=()
    if [ -n "$KUBE_CONTEXT" ]; then
        args+=(--context "$KUBE_CONTEXT")
    fi
    if [ -n "$GATEWAY_IMAGE" ]; then
        args+=(--image "$GATEWAY_IMAGE")
    fi
    exec "$KVM_START" "${args[@]}"
fi

# orbstack: 继续走原逻辑
command -v docker >/dev/null 2>&1 || { log_error "docker 未安装"; exit 1; }

if ! echo "$CURRENT_CONTEXT" | grep -qi "orbstack"; then
    log_warn "当前 context 不是 orbstack（profile=orbstack），尝试切换..."
    kubectl config use-context orbstack >/dev/null || { log_error "无法切换到 orbstack context"; exit 1; }
    CURRENT_CONTEXT="$(kubectl config current-context 2>/dev/null || true)"
fi

log_success "Kubernetes context: $CURRENT_CONTEXT (profile=orbstack)"

# ============================================================================
# Step 1: 构建镜像
# ============================================================================
if [ "$SKIP_IMAGES" = false ]; then
    log_info "Step 1: 构建镜像..."

    RUNTIMES_DIR="$PROJECT_ROOT/deployments/docker/runtimes"

    build_image() {
        local name=$1
        local dockerfile=$2
        if [ "$REBUILD" = true ] || ! docker image inspect "$name" >/dev/null 2>&1; then
            log_info "  构建 $name..."
            docker build -t "$name" -f "$RUNTIMES_DIR/$dockerfile" "$RUNTIMES_DIR" -q
        else
            log_info "  $name 已存在，跳过"
        fi
    }

    # 构建所有运行时镜像
    build_image "function-runtime-python:latest" "Dockerfile.python3.11"
    build_image "function-runtime-nodejs:latest" "Dockerfile.nodejs20"
    build_image "function-runtime-go:latest" "Dockerfile.go1.24"
    build_image "function-runtime-wasm:latest" "Dockerfile.wasm"

    # 构建编译器镜像（用于在集群内编译 Rust WASM）
    build_image "nimbus-rust-wasm-compiler:latest" "Dockerfile.rust-wasm-compiler"

    # 构建 Web 前端镜像
    WEB_DIR="$PROJECT_ROOT/web"
    if [ -d "$WEB_DIR" ] && [ -f "$WEB_DIR/Dockerfile" ]; then
        if [ "$REBUILD" = true ] || ! docker image inspect "nimbus-web:latest" >/dev/null 2>&1; then
            log_info "  构建 nimbus-web:latest..."
            docker build -t nimbus-web:latest "$WEB_DIR" -q
        else
            log_info "  nimbus-web:latest 已存在，跳过"
        fi
    else
        log_warn "  Web 目录不存在，跳过前端构建"
    fi

    # 构建 Gateway 镜像
    if [ "$REBUILD" = true ] || ! docker image inspect "nimbus-gateway:latest" >/dev/null 2>&1; then
        log_info "  构建 nimbus-gateway:latest..."
        docker build -t nimbus-gateway:latest -f "$PROJECT_ROOT/deployments/docker/Dockerfile" "$PROJECT_ROOT" -q
    else
        log_info "  nimbus-gateway:latest 已存在，跳过"
    fi

    # 拉取编译器镜像（用于在集群内编译 Go/Rust 函数）
    log_info "  拉取编译器镜像..."
    if ! docker image inspect "golang:1.24-alpine" >/dev/null 2>&1; then
        log_info "  拉取 golang:1.24-alpine..."
        docker pull golang:1.24-alpine -q || log_warn "  golang:1.24-alpine 拉取失败，Go 编译功能可能不可用"
    else
        log_info "  golang:1.24-alpine 已存在，跳过"
    fi
    if ! docker image inspect "rust:1.75" >/dev/null 2>&1; then
        log_info "  拉取 rust:1.75..."
        docker pull rust:1.75 -q || log_warn "  rust:1.75 拉取失败，Rust 编译功能可能不可用"
    else
        log_info "  rust:1.75 已存在，跳过"
    fi

    log_success "镜像构建完成"
else
    log_info "Step 1: 跳过镜像构建"
fi

# ============================================================================
# Step 2: 部署到 Kubernetes
# ============================================================================
log_info "Step 2: 部署到 Kubernetes..."

# 创建 namespace（如果不存在）
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null 2>&1

# 删除旧的 seed job（如果存在）以便重新运行
kubectl delete job seed-functions -n "$NAMESPACE" --ignore-not-found=true >/dev/null 2>&1

# 应用 Kustomize 配置
kubectl apply -k "$SCRIPT_DIR" 2>&1 | grep -v "unchanged" || true

# 确保 web 服务存在（可能因 LoadBalancer quota 失败）
if ! kubectl get svc nimbus-web -n "$NAMESPACE" >/dev/null 2>&1; then
    log_info "  创建 nimbus-web 服务..."
    kubectl apply -f "$SCRIPT_DIR/web-service.yaml" -n "$NAMESPACE" 2>/dev/null || true
    kubectl patch svc nimbus-web -n "$NAMESPACE" -p '{"spec": {"type": "NodePort"}}' 2>/dev/null || true
fi

log_success "Kubernetes 资源已部署"

# ============================================================================
# Step 3: 等待核心服务就绪
# ============================================================================
log_info "Step 3: 等待核心服务就绪..."

wait_for_deployment() {
    local name=$1
    local timeout=${2:-120}
    log_info "  等待 $name..."
    kubectl rollout status deployment/"$name" -n "$NAMESPACE" --timeout="${timeout}s" >/dev/null 2>&1 || {
        log_warn "  $name 超时，继续..."
    }
}

wait_for_deployment "postgres" 60
wait_for_deployment "redis" 60
wait_for_deployment "nats" 60
wait_for_deployment "nimbus-gateway" 120
wait_for_deployment "nimbus-web" 60

log_success "核心服务已就绪"

# ============================================================================
# Step 4: 加载运行时镜像到 DinD
# ============================================================================
log_info "Step 4: 加载运行时镜像到 DinD..."

# 等待 DinD 容器就绪
sleep 3

load_images_to_pod() {
    local pod=$1
    local pod_name=$(echo "$pod" | sed 's|pod/||')
    log_info "  加载镜像到 $pod_name..."

    # DinD 容器中 docker socket 路径
    DIND_DOCKER_HOST="unix:///var/run/docker/docker.sock"

    # 等待 dind 就绪
    for i in {1..30}; do
        if kubectl exec -n "$NAMESPACE" "$pod_name" -c dind -- env DOCKER_HOST="$DIND_DOCKER_HOST" docker info >/dev/null 2>&1; then
            break
        fi
        sleep 2
    done

    # 加载所有运行时镜像
    docker save \
        function-runtime-python:latest \
        function-runtime-nodejs:latest \
        function-runtime-go:latest \
        function-runtime-wasm:latest \
        2>/dev/null | \
        kubectl exec -i -n "$NAMESPACE" "$pod_name" -c dind -- env DOCKER_HOST="$DIND_DOCKER_HOST" docker load >/dev/null 2>&1 || {
            log_warn "  运行时镜像加载到 $pod_name 失败"
            return 1
        }

    # 加载编译器镜像（用于 Go/Rust 编译）
    log_info "  加载编译器镜像到 $pod_name..."
    if docker image inspect "golang:1.24-alpine" >/dev/null 2>&1; then
        docker save golang:1.24-alpine 2>/dev/null | \
            kubectl exec -i -n "$NAMESPACE" "$pod_name" -c dind -- env DOCKER_HOST="$DIND_DOCKER_HOST" docker load >/dev/null 2>&1 || {
                log_warn "  Go 编译器镜像加载到 $pod_name 失败"
            }
    fi
    if docker image inspect "nimbus-rust-wasm-compiler:latest" >/dev/null 2>&1; then
        docker save nimbus-rust-wasm-compiler:latest 2>/dev/null | \
            kubectl exec -i -n "$NAMESPACE" "$pod_name" -c dind -- env DOCKER_HOST="$DIND_DOCKER_HOST" docker load >/dev/null 2>&1 || {
                log_warn "  Rust WASM 编译器镜像加载到 $pod_name 失败"
            }
    fi
    if docker image inspect "rust:1.75" >/dev/null 2>&1; then
        docker save rust:1.75 2>/dev/null | \
            kubectl exec -i -n "$NAMESPACE" "$pod_name" -c dind -- env DOCKER_HOST="$DIND_DOCKER_HOST" docker load >/dev/null 2>&1 || {
                log_warn "  Rust 编译器镜像加载到 $pod_name 失败"
            }
    fi

    log_success "  $pod_name 镜像加载完成"
}

# 获取所有 gateway pod 并加载镜像
for pod in $(kubectl get pods -n "$NAMESPACE" -l app=nimbus-gateway -o name); do
    load_images_to_pod "$pod" || true
done

log_success "运行时镜像已加载"

# ============================================================================
# Step 5: 等待 Seed Job 完成
# ============================================================================
log_info "Step 5: 等待示例函数创建..."

# 等待 job 完成
for i in {1..60}; do
    status=$(kubectl get job seed-functions -n "$NAMESPACE" -o jsonpath='{.status.succeeded}' 2>/dev/null)
    if [ "$status" = "1" ]; then
        log_success "示例函数已创建"
        break
    fi
    sleep 2
done

# ============================================================================
# Step 6: 创建 Go/Rust 示例函数（需要本地编译）
# ============================================================================
log_info "Step 6: 创建 Go/Rust 示例函数..."

# 获取 API URL
API_URL="http://$(kubectl get svc nimbus-gateway-external -n "$NAMESPACE" -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null):8080"
if [ -z "$API_URL" ] || [ "$API_URL" = "http://:8080" ]; then
    API_URL="http://localhost:8080"
    kubectl port-forward svc/nimbus-gateway -n "$NAMESPACE" 8080:8080 >/dev/null 2>&1 &
    PF_PID=$!
    sleep 2
fi

# 检查是否有 Go 编译器
if command -v go >/dev/null 2>&1; then
    # 检测目标架构
    GOARCH="arm64"
    ARCH=$(docker version --format '{{.Server.Arch}}' 2>/dev/null)
    if [ "$ARCH" = "x86_64" ] || [ "$ARCH" = "amd64" ]; then
        GOARCH="amd64"
    fi

    # 创建临时目录
    GO_ORIG_DIR=$(pwd)
    TMP_DIR=$(mktemp -d)
    trap "rm -rf $TMP_DIR" EXIT

    # 编译 hello-go
    log_info "  编译 hello-go..."
    GO_SOURCE='package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	var event map[string]interface{}
	json.NewDecoder(os.Stdin).Decode(&event)
	name, _ := event["name"].(string)
	if name == "" {
		name = "World"
	}
	resp := map[string]interface{}{
		"statusCode": 200,
		"body": map[string]string{
			"message": fmt.Sprintf("Hello, %s!", name),
			"runtime": "go1.24",
		},
	}
	json.NewEncoder(os.Stdout).Encode(resp)
}'
    echo "$GO_SOURCE" > "$TMP_DIR/main.go"

    cd "$TMP_DIR"
    CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build -ldflags="-s -w" -o handler main.go 2>/dev/null
    if [ -f handler ]; then
        BINARY=$(base64 -i handler)
        # 转义源代码用于 JSON
        CODE_ESCAPED=$(echo "$GO_SOURCE" | jq -Rs .)
        # 创建请求 JSON 文件（同时包含源代码和预编译二进制）
        cat > request.json << REQEOF
{
    "name": "hello-go",
    "description": "Hello World example in Go 1.24",
    "runtime": "go1.24",
    "handler": "main.Handler",
    "memory_mb": 128,
    "timeout_sec": 10,
    "code": $CODE_ESCAPED,
    "binary": "$BINARY"
}
REQEOF
        result=$(curl -s -w "%{http_code}" -o /dev/null -X POST "$API_URL/api/v1/functions" \
            -H "Content-Type: application/json" \
            -d @request.json 2>/dev/null)
        if [ "$result" = "201" ] || [ "$result" = "200" ]; then
            log_success "  hello-go 创建成功"
        elif [ "$result" = "409" ]; then
            log_info "  hello-go 已存在"
        else
            log_warn "  hello-go 创建失败 (HTTP $result)"
        fi
    else
        log_warn "  Go 编译失败"
    fi

    # 编译 fibonacci-go
    log_info "  编译 fibonacci-go..."
    GO_SOURCE='package main

import (
	"encoding/json"
	"os"
)

func fib(n int) int {
	if n <= 1 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func main() {
	var event map[string]interface{}
	json.NewDecoder(os.Stdin).Decode(&event)
	n := 10
	if v, ok := event["n"].(float64); ok {
		n = int(v)
	}
	resp := map[string]interface{}{
		"statusCode": 200,
		"body": map[string]interface{}{
			"n":       n,
			"result":  fib(n),
			"runtime": "go1.24",
		},
	}
	json.NewEncoder(os.Stdout).Encode(resp)
}'
    echo "$GO_SOURCE" > "$TMP_DIR/main.go"

    CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build -ldflags="-s -w" -o handler main.go 2>/dev/null
    if [ -f handler ]; then
        BINARY=$(base64 -i handler)
        CODE_ESCAPED=$(echo "$GO_SOURCE" | jq -Rs .)
        cat > request.json << REQEOF
{
    "name": "fibonacci-go",
    "description": "Fibonacci calculation in Go (CPU intensive)",
    "runtime": "go1.24",
    "handler": "main.Handler",
    "memory_mb": 128,
    "timeout_sec": 30,
    "code": $CODE_ESCAPED,
    "binary": "$BINARY"
}
REQEOF
        result=$(curl -s -w "%{http_code}" -o /dev/null -X POST "$API_URL/api/v1/functions" \
            -H "Content-Type: application/json" \
            -d @request.json 2>/dev/null)
        if [ "$result" = "201" ] || [ "$result" = "200" ]; then
            log_success "  fibonacci-go 创建成功"
        elif [ "$result" = "409" ]; then
            log_info "  fibonacci-go 已存在"
        else
            log_warn "  fibonacci-go 创建失败 (HTTP $result)"
        fi
    fi

    cd "$GO_ORIG_DIR"
else
    log_warn "  Go 未安装，跳过 Go 函数"
fi

# 检查是否有 Rust 编译器和 wasm 目标
if command -v rustc >/dev/null 2>&1 && rustup target list --installed 2>/dev/null | grep -q "wasm32-unknown-unknown"; then
    log_info "  编译 Rust WASM 函数..."

    # 保存当前目录
    RUST_ORIG_DIR=$(pwd)
    # 创建临时目录
    RUST_TMP=$(mktemp -d)

    # hello-rust (WASM)
    log_info "  编译 hello-rust..."
    RUST_SOURCE='#![no_std]
#![no_main]

use core::panic::PanicInfo;
use core::slice;

static mut BUFFER: [u8; 4096] = [0; 4096];
static mut OUT_BUFFER: [u8; 4096] = [0; 4096];

#[panic_handler]
fn panic(_info: &PanicInfo) -> ! {
    loop {}
}

#[no_mangle]
pub extern "C" fn alloc(size: usize) -> *mut u8 {
    unsafe { BUFFER.as_mut_ptr() }
}

#[no_mangle]
pub extern "C" fn handle(ptr: *const u8, len: usize) -> u64 {
    let input = unsafe { slice::from_raw_parts(ptr, len) };

    // Simple JSON response
    let response = br#"{"statusCode":200,"body":{"message":"Hello from Rust WASM!","runtime":"wasm"}}"#;

    unsafe {
        let out_ptr = OUT_BUFFER.as_mut_ptr();
        for (i, &byte) in response.iter().enumerate() {
            if i < OUT_BUFFER.len() {
                *out_ptr.add(i) = byte;
            }
        }
        // Pack pointer and length into u64: (ptr << 32) | len
        ((out_ptr as u64) << 32) | (response.len() as u64)
    }
}'

    mkdir -p "$RUST_TMP/hello-rust"
    echo "$RUST_SOURCE" > "$RUST_TMP/hello-rust/lib.rs"

    cd "$RUST_TMP/hello-rust"
    rustc --target wasm32-unknown-unknown -O --crate-type cdylib -o hello.wasm lib.rs 2>/dev/null
    if [ -f hello.wasm ]; then
        BINARY=$(base64 -i hello.wasm)
        CODE_ESCAPED=$(echo "$RUST_SOURCE" | jq -Rs .)
        cat > request.json << REQEOF
{
    "name": "hello-rust",
    "description": "Hello World example in Rust (WebAssembly)",
    "runtime": "wasm",
    "handler": "handle",
    "memory_mb": 128,
    "timeout_sec": 10,
    "code": $CODE_ESCAPED,
    "binary": "$BINARY"
}
REQEOF
        result=$(curl -s -w "%{http_code}" -o /dev/null -X POST "$API_URL/api/v1/functions" \
            -H "Content-Type: application/json" \
            -d @request.json 2>/dev/null)
        if [ "$result" = "201" ] || [ "$result" = "200" ]; then
            log_success "  hello-rust 创建成功"
        elif [ "$result" = "409" ]; then
            log_info "  hello-rust 已存在"
        else
            log_warn "  hello-rust 创建失败 (HTTP $result)"
        fi
    else
        log_warn "  Rust WASM 编译失败"
    fi

    # fibonacci-rust (WASM)
    log_info "  编译 fibonacci-rust..."
    RUST_SOURCE='#![no_std]
#![no_main]

use core::panic::PanicInfo;
use core::slice;

static mut BUFFER: [u8; 4096] = [0; 4096];
static mut OUT_BUFFER: [u8; 4096] = [0; 4096];

const B0: u8 = 48; // ASCII for 0
const B9: u8 = 57; // ASCII for 9
const BQUOTE: u8 = 34; // ASCII for "
const BN: u8 = 110; // ASCII for n
const BCOLON: u8 = 58; // ASCII for :
const BSPACE: u8 = 32; // ASCII for space

#[panic_handler]
fn panic(_info: &PanicInfo) -> ! {
    loop {}
}

fn fib(n: u32) -> u32 {
    if n <= 1 { n } else { fib(n - 1) + fib(n - 2) }
}

fn parse_n(input: &[u8]) -> u32 {
    // Simple parser: look for "n":X pattern
    let mut i = 0;
    while i + 3 < input.len() {
        if input[i] == BQUOTE && input[i+1] == BN && input[i+2] == BQUOTE {
            // Skip to value
            i += 3;
            while i < input.len() && (input[i] == BCOLON || input[i] == BSPACE) { i += 1; }
            // Parse number
            let mut n: u32 = 0;
            while i < input.len() && input[i] >= B0 && input[i] <= B9 {
                n = n * 10 + (input[i] - B0) as u32;
                i += 1;
            }
            return if n == 0 { 10 } else { n };
        }
        i += 1;
    }
    10 // default
}

fn write_num(buf: &mut [u8], mut n: u32, start: usize) -> usize {
    if n == 0 {
        buf[start] = B0;
        return start + 1;
    }
    let mut digits = [0u8; 10];
    let mut len = 0;
    while n > 0 {
        digits[len] = (n % 10) as u8 + B0;
        n /= 10;
        len += 1;
    }
    for i in 0..len {
        buf[start + i] = digits[len - 1 - i];
    }
    start + len
}

#[no_mangle]
pub extern "C" fn alloc(size: usize) -> *mut u8 {
    unsafe { BUFFER.as_mut_ptr() }
}

#[no_mangle]
pub extern "C" fn handle(ptr: *const u8, len: usize) -> u64 {
    let input = unsafe { slice::from_raw_parts(ptr, len) };
    let n = parse_n(input);
    let result = fib(n);

    unsafe {
        let out = &mut OUT_BUFFER;
        let prefix = br#"{"statusCode":200,"body":{"n":"#;
        let mid1 = br#","result":"#;
        let suffix = br#","runtime":"wasm"}}"#;

        let mut pos = 0;
        for &b in prefix.iter() { out[pos] = b; pos += 1; }
        pos = write_num(out, n, pos);
        for &b in mid1.iter() { out[pos] = b; pos += 1; }
        pos = write_num(out, result, pos);
        for &b in suffix.iter() { out[pos] = b; pos += 1; }

        let out_ptr = OUT_BUFFER.as_mut_ptr();
        ((out_ptr as u64) << 32) | (pos as u64)
    }
}'

    mkdir -p "$RUST_TMP/fib-rust"
    echo "$RUST_SOURCE" > "$RUST_TMP/fib-rust/lib.rs"

    cd "$RUST_TMP/fib-rust"
    rustc --target wasm32-unknown-unknown -O --crate-type cdylib -o fib.wasm lib.rs 2>/dev/null
    if [ -f fib.wasm ]; then
        BINARY=$(base64 -i fib.wasm)
        CODE_ESCAPED=$(echo "$RUST_SOURCE" | jq -Rs .)
        cat > request.json << REQEOF
{
    "name": "fibonacci-rust",
    "description": "Fibonacci calculation in Rust (WebAssembly, CPU intensive)",
    "runtime": "wasm",
    "handler": "handle",
    "memory_mb": 128,
    "timeout_sec": 30,
    "code": $CODE_ESCAPED,
    "binary": "$BINARY"
}
REQEOF
        result=$(curl -s -w "%{http_code}" -o /dev/null -X POST "$API_URL/api/v1/functions" \
            -H "Content-Type: application/json" \
            -d @request.json 2>/dev/null)
        if [ "$result" = "201" ] || [ "$result" = "200" ]; then
            log_success "  fibonacci-rust 创建成功"
        elif [ "$result" = "409" ]; then
            log_info "  fibonacci-rust 已存在"
        else
            log_warn "  fibonacci-rust 创建失败 (HTTP $result)"
        fi
    fi

    cd "$RUST_ORIG_DIR"
    rm -rf "$RUST_TMP"
else
    log_warn "  Rust 或 wasm32-unknown-unknown 目标未安装，跳过 Rust WASM 函数"
    log_info "  安装方法: rustup target add wasm32-unknown-unknown"
fi

# 检查是否有 clang 并支持 wasm32 目标（用于 C WASM）
# 优先使用 Homebrew LLVM（支持 wasm32），否则尝试系统 clang
WASM_CLANG=""
if [ -x "/opt/homebrew/opt/llvm/bin/clang" ]; then
    /opt/homebrew/opt/llvm/bin/clang --print-targets 2>/dev/null | grep -q "wasm32" && WASM_CLANG="/opt/homebrew/opt/llvm/bin/clang"
elif [ -x "/usr/local/opt/llvm/bin/clang" ]; then
    /usr/local/opt/llvm/bin/clang --print-targets 2>/dev/null | grep -q "wasm32" && WASM_CLANG="/usr/local/opt/llvm/bin/clang"
elif command -v clang >/dev/null 2>&1 && clang --print-targets 2>/dev/null | grep -q "wasm32"; then
    WASM_CLANG="clang"
fi

if [ -n "$WASM_CLANG" ]; then
    log_info "  编译 C WASM 函数..."

    # 保存当前目录
    C_ORIG_DIR=$(pwd)
    C_TMP=$(mktemp -d)

    # hello-c (WASM)
    log_info "  编译 hello-c..."
    C_SOURCE='// Hello World in C (WebAssembly)
// Exports: alloc, handle

static unsigned char buffer[4096];
static unsigned char out_buffer[4096];

__attribute__((export_name("alloc")))
unsigned char* alloc(int size) {
    return buffer;
}

static int copy_str(unsigned char* dst, const char* src, int start) {
    int i = 0;
    while (src[i]) {
        dst[start + i] = src[i];
        i++;
    }
    return start + i;
}

__attribute__((export_name("handle")))
unsigned long long handle(unsigned char* ptr, int len) {
    const char* response = "{\"statusCode\":200,\"body\":{\"message\":\"Hello from C WASM!\",\"runtime\":\"wasm\"}}";

    int pos = copy_str(out_buffer, response, 0);

    // Pack pointer and length: (ptr << 32) | len
    unsigned long long out_ptr = (unsigned long long)out_buffer;
    return (out_ptr << 32) | (unsigned long long)pos;
}

void _start() {}'

    echo "$C_SOURCE" > "$C_TMP/hello.c"

    cd "$C_TMP"
    $WASM_CLANG --target=wasm32 -O2 -nostdlib -Wl,--no-entry -Wl,--export-all -o hello.wasm hello.c 2>/dev/null
    if [ -f hello.wasm ]; then
        BINARY=$(base64 -i hello.wasm)
        CODE_ESCAPED=$(echo "$C_SOURCE" | jq -Rs .)
        cat > request.json << REQEOF
{
    "name": "hello-c",
    "description": "Hello World example in C (WebAssembly)",
    "runtime": "wasm",
    "handler": "handle",
    "memory_mb": 128,
    "timeout_sec": 10,
    "code": $CODE_ESCAPED,
    "binary": "$BINARY"
}
REQEOF
        result=$(curl -s -w "%{http_code}" -o /dev/null -X POST "$API_URL/api/v1/functions" \
            -H "Content-Type: application/json" \
            -d @request.json 2>/dev/null)
        if [ "$result" = "201" ] || [ "$result" = "200" ]; then
            log_success "  hello-c 创建成功"
        elif [ "$result" = "409" ]; then
            log_info "  hello-c 已存在"
        else
            log_warn "  hello-c 创建失败 (HTTP $result)"
        fi
    else
        log_warn "  C WASM 编译失败"
    fi

    # fibonacci-c (WASM)
    log_info "  编译 fibonacci-c..."
    C_SOURCE='// Fibonacci in C (WebAssembly)
// Exports: alloc, handle

static unsigned char buffer[4096];
static unsigned char out_buffer[4096];

__attribute__((export_name("alloc")))
unsigned char* alloc(int size) {
    return buffer;
}

static int fib(int n) {
    if (n <= 1) return n;
    return fib(n - 1) + fib(n - 2);
}

static int parse_n(unsigned char* input, int len) {
    // Simple parser: look for "n":X pattern
    for (int i = 0; i + 3 < len; i++) {
        if (input[i] == 34 && input[i+1] == 110 && input[i+2] == 34) {
            i += 3;
            while (i < len && (input[i] == 58 || input[i] == 32)) i++;
            int n = 0;
            while (i < len && input[i] >= 48 && input[i] <= 57) {
                n = n * 10 + (input[i] - 48);
                i++;
            }
            return n == 0 ? 10 : n;
        }
    }
    return 10;
}

static int write_num(unsigned char* buf, int n, int start) {
    if (n == 0) {
        buf[start] = 48;
        return start + 1;
    }
    unsigned char digits[10];
    int len = 0;
    while (n > 0) {
        digits[len++] = (n % 10) + 48;
        n /= 10;
    }
    for (int i = 0; i < len; i++) {
        buf[start + i] = digits[len - 1 - i];
    }
    return start + len;
}

static int copy_str(unsigned char* dst, const char* src, int start) {
    int i = 0;
    while (src[i]) {
        dst[start + i] = src[i];
        i++;
    }
    return start + i;
}

__attribute__((export_name("handle")))
unsigned long long handle(unsigned char* ptr, int len) {
    int n = parse_n(ptr, len);
    int result = fib(n);

    int pos = 0;
    pos = copy_str(out_buffer, "{\"statusCode\":200,\"body\":{\"n\":", pos);
    pos = write_num(out_buffer, n, pos);
    pos = copy_str(out_buffer, ",\"result\":", pos);
    pos = write_num(out_buffer, result, pos);
    pos = copy_str(out_buffer, ",\"runtime\":\"wasm\"}}", pos);

    unsigned long long out_ptr = (unsigned long long)out_buffer;
    return (out_ptr << 32) | (unsigned long long)pos;
}

void _start() {}'

    echo "$C_SOURCE" > "$C_TMP/fib.c"

    $WASM_CLANG --target=wasm32 -O2 -nostdlib -Wl,--no-entry -Wl,--export-all -o fib.wasm fib.c 2>/dev/null
    if [ -f fib.wasm ]; then
        BINARY=$(base64 -i fib.wasm)
        CODE_ESCAPED=$(echo "$C_SOURCE" | jq -Rs .)
        cat > request.json << REQEOF
{
    "name": "fibonacci-c",
    "description": "Fibonacci calculation in C (WebAssembly, CPU intensive)",
    "runtime": "wasm",
    "handler": "handle",
    "memory_mb": 128,
    "timeout_sec": 30,
    "code": $CODE_ESCAPED,
    "binary": "$BINARY"
}
REQEOF
        result=$(curl -s -w "%{http_code}" -o /dev/null -X POST "$API_URL/api/v1/functions" \
            -H "Content-Type: application/json" \
            -d @request.json 2>/dev/null)
        if [ "$result" = "201" ] || [ "$result" = "200" ]; then
            log_success "  fibonacci-c 创建成功"
        elif [ "$result" = "409" ]; then
            log_info "  fibonacci-c 已存在"
        else
            log_warn "  fibonacci-c 创建失败 (HTTP $result)"
        fi
    fi

    cd "$C_ORIG_DIR"
    rm -rf "$C_TMP"
else
    log_warn "  Clang 或 wasm32 目标未安装，跳过 C WASM 函数"
    log_info "  macOS 安装方法: brew install llvm"
fi

# 清理 port-forward
if [ -n "$PF_PID" ]; then
    kill $PF_PID 2>/dev/null || true
fi

log_success "示例函数创建完成"

# ============================================================================
# Step 7: 显示访问信息
# ============================================================================
echo ""
echo "=============================================="
echo "   Nimbus Platform 启动完成!"
echo "=============================================="
echo ""

# 获取服务 URL
get_service_url() {
    local svc=$1
    local default_port=$2

    local svc_type=$(kubectl get svc "$svc" -n "$NAMESPACE" -o jsonpath='{.spec.type}' 2>/dev/null)

    if [ "$svc_type" = "LoadBalancer" ]; then
        local ip=$(kubectl get svc "$svc" -n "$NAMESPACE" -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null)
        local port=$(kubectl get svc "$svc" -n "$NAMESPACE" -o jsonpath='{.spec.ports[0].port}' 2>/dev/null)
        if [ -n "$ip" ]; then
            echo "http://${ip}:${port}"
            return
        fi
    fi

    if [ "$svc_type" = "NodePort" ]; then
        local node_port=$(kubectl get svc "$svc" -n "$NAMESPACE" -o jsonpath='{.spec.ports[0].nodePort}' 2>/dev/null)
        if [ -n "$node_port" ]; then
            echo "http://localhost:${node_port}"
            return
        fi
    fi

    echo "http://localhost:${default_port} (kubectl port-forward)"
}

GATEWAY_URL=$(get_service_url nimbus-gateway-external 8080)
WEB_URL=$(get_service_url nimbus-web 80)
PROMETHEUS_URL=$(get_service_url prometheus 9090)
GRAFANA_URL=$(get_service_url grafana 3000)

echo -e "${GREEN}服务访问地址:${NC}"
echo ""
echo -e "  ${BLUE}API Gateway:${NC}     $GATEWAY_URL"
echo -e "  ${BLUE}Web UI:${NC}          $WEB_URL"
echo -e "  ${BLUE}Prometheus:${NC}      $PROMETHEUS_URL"
echo -e "  ${BLUE}Grafana:${NC}         $GRAFANA_URL"
echo ""
echo -e "${GREEN}支持的运行时:${NC}"
echo ""
echo "  - Python 3.11"
echo "  - Node.js 20"
echo "  - Go 1.24 (需编译)"
echo "  - WebAssembly/Rust (需编译)"
echo ""
echo -e "${GREEN}已创建的示例函数:${NC}"
echo ""
echo "  Python:  echo-python, hello-python, fibonacci-python"
echo "  Node.js: hello-node, timestamp-node, api-aggregator-node"
echo "  Go:      hello-go, fibonacci-go"
echo "  Rust:    hello-rust, fibonacci-rust (WASM)"
echo "  C:       hello-c, fibonacci-c (WASM)"
echo "  网络:    http-fetch-python, json-transform-python"
echo ""
echo -e "${GREEN}测试命令:${NC}"
echo ""
echo "  # Python"
echo "  curl -X POST ${GATEWAY_URL}/api/v1/functions/echo-python/invoke \\"
echo "    -H 'Content-Type: application/json' -d '{\"message\": \"Hello!\"}'"
echo ""
echo "  # Go"
echo "  curl -X POST ${GATEWAY_URL}/api/v1/functions/hello-go/invoke \\"
echo "    -H 'Content-Type: application/json' -d '{\"name\": \"Nimbus\"}'"
echo ""
echo "  # Rust (WASM)"
echo "  curl -X POST ${GATEWAY_URL}/api/v1/functions/hello-rust/invoke \\"
echo "    -H 'Content-Type: application/json' -d '{}'"
echo ""
echo "  # C (WASM)"
echo "  curl -X POST ${GATEWAY_URL}/api/v1/functions/fibonacci-c/invoke \\"
echo "    -H 'Content-Type: application/json' -d '{\"n\": 15}'"
echo ""
echo -e "${GREEN}查看日志:${NC}"
echo ""
echo "  kubectl logs -f -n nimbus -l app=nimbus-gateway -c gateway"
echo ""
