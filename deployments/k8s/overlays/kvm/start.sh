#!/bin/bash
# ============================================================================
# Nimbus Platform - KVM(Firecracker) 一键启动脚本
# ============================================================================
# 用法:
#   ./start.sh [--context <ctx>] [--image <gateway-image>]
#
# 默认:
# - 使用当前 kubectl context
# - 部署 firecracker 模式（需要 /dev/kvm）
# - 对外通过 NodePort 暴露：30080(HTTP) / 30090(metrics)
# ============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NAMESPACE="${NAMESPACE:-nimbus}"

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

KUBE_CONTEXT=""
GATEWAY_IMAGE=""

usage() {
  cat <<EOF
Usage: $0 [--context <ctx>] [--image <gateway-image>]

Options:
  --context   kubectl context name
  --image     override gateway image (Deployment/nimbus-gateway container "gateway")
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --context)
      KUBE_CONTEXT="${2:-}"
      shift 2
      ;;
    --image)
      GATEWAY_IMAGE="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      log_error "Unknown arg: $1"
      usage
      exit 1
      ;;
  esac
done

command -v kubectl >/dev/null 2>&1 || { log_error "kubectl 未安装"; exit 1; }

if [ -n "$KUBE_CONTEXT" ]; then
  log_info "切换 context: $KUBE_CONTEXT"
  kubectl config use-context "$KUBE_CONTEXT" >/dev/null
fi

log_success "Kubernetes context: $(kubectl config current-context)"

log_info "部署 Nimbus（kvm/firecracker）..."
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null 2>&1
kubectl delete job seed-functions -n "$NAMESPACE" --ignore-not-found=true >/dev/null 2>&1 || true

kubectl apply -k "$SCRIPT_DIR" 2>&1 | grep -v "unchanged" || true

if [ -n "$GATEWAY_IMAGE" ]; then
  log_info "设置 gateway 镜像: $GATEWAY_IMAGE"
  kubectl -n "$NAMESPACE" set image deployment/nimbus-gateway gateway="$GATEWAY_IMAGE" >/dev/null
fi

wait_for_deploy() {
  local name="$1"
  local timeout="${2:-180}"
  log_info "等待 Deployment/$name..."
  kubectl rollout status -n "$NAMESPACE" "deployment/$name" --timeout="${timeout}s" >/dev/null 2>&1 || {
    log_warn "Deployment/$name 等待超时（继续）"
  }
}

wait_for_deploy postgres 120
wait_for_deploy redis 120
wait_for_deploy nats 120
wait_for_deploy nimbus-gateway 240

log_info "等待 seed-functions Job..."
kubectl wait --for=condition=complete job/seed-functions -n "$NAMESPACE" --timeout=180s >/dev/null 2>&1 || {
  log_warn "seed-functions 未在超时内完成（可忽略，或查看 job 日志）"
}

echo ""
log_success "部署完成"
echo ""
echo "Gateway NodePort:"
echo "  http  : <node-ip>:30080"
echo "  metrics: <node-ip>:30090/metrics"
echo ""
echo "常用命令:"
echo "  kubectl -n $NAMESPACE get pods -o wide"
echo "  kubectl -n $NAMESPACE logs -l app=nimbus-gateway -f"
echo "  kubectl -n $NAMESPACE get svc nimbus-gateway-external -o wide"
echo ""
