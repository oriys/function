#!/bin/bash
# ============================================================================
# Nimbus Platform - KVM(Firecracker) 停止脚本
# ============================================================================
# 用法: ./stop.sh [--clean] [--all]
#   --clean   删除所有资源（含 PVC/ConfigMap/Secret）
#   --all     删除整个 namespace
# ============================================================================

set -euo pipefail

NAMESPACE="${NAMESPACE:-nimbus}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

CLEAN=false
DELETE_ALL=false

for arg in "$@"; do
  case "$arg" in
    --clean) CLEAN=true ;;
    --all) DELETE_ALL=true ;;
    *) ;;
  esac
done

if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  log_warn "Namespace $NAMESPACE 不存在"
  exit 0
fi

if [ "$DELETE_ALL" = true ]; then
  log_info "删除 namespace: $NAMESPACE"
  kubectl delete namespace "$NAMESPACE" --wait=false
  log_success "删除命令已发送"
  exit 0
fi

log_info "停止 Nimbus 资源..."
kubectl delete deployment --all -n "$NAMESPACE" --wait=false >/dev/null 2>&1 || true
kubectl delete job --all -n "$NAMESPACE" --wait=false >/dev/null 2>&1 || true

if [ "$CLEAN" = true ]; then
  kubectl delete service --all -n "$NAMESPACE" --wait=false >/dev/null 2>&1 || true
  kubectl delete configmap --all -n "$NAMESPACE" --wait=false >/dev/null 2>&1 || true
  kubectl delete secret --all -n "$NAMESPACE" --wait=false >/dev/null 2>&1 || true
  kubectl delete pvc --all -n "$NAMESPACE" --wait=false >/dev/null 2>&1 || true
fi

log_success "已发送停止指令"
