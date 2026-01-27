#!/bin/bash
# ============================================================================
# Nimbus Platform - OrbStack 停止脚本
# ============================================================================
# 用法: ./stop.sh [--clean] [--all]
#   --clean   删除所有资源，包括 PVC 数据
#   --all     删除整个 namespace
# ============================================================================

set -e

NAMESPACE="nimbus"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# 解析参数
CLEAN=false
DELETE_ALL=false
for arg in "$@"; do
    case $arg in
        --clean) CLEAN=true ;;
        --all) DELETE_ALL=true ;;
    esac
done

echo ""
echo "=============================================="
echo "   Nimbus Platform - 停止服务"
echo "=============================================="
echo ""

# 检查 namespace 是否存在
if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
    log_warn "Namespace $NAMESPACE 不存在，无需停止"
    exit 0
fi

if [ "$DELETE_ALL" = true ]; then
    # 删除整个 namespace
    log_info "删除整个 namespace: $NAMESPACE"
    kubectl delete namespace "$NAMESPACE" --wait=false
    log_success "Namespace 删除命令已发送"
    log_info "等待资源清理完成..."
    kubectl wait --for=delete namespace/"$NAMESPACE" --timeout=120s 2>/dev/null || true
    log_success "Nimbus Platform 已完全删除"
else
    # 停止部署但保留数据
    log_info "停止 Nimbus Platform 服务..."

    # 删除 Deployments
    log_info "停止 Deployments..."
    kubectl delete deployment --all -n "$NAMESPACE" --wait=false 2>/dev/null || true

    # 删除 Jobs
    log_info "删除 Jobs..."
    kubectl delete job --all -n "$NAMESPACE" --wait=false 2>/dev/null || true

    # 删除 Services（保留 ClusterIP 类型以便快速重启）
    if [ "$CLEAN" = true ]; then
        log_info "删除 Services..."
        kubectl delete service --all -n "$NAMESPACE" --wait=false 2>/dev/null || true
    fi

    # 删除 ConfigMaps 和 Secrets（可选）
    if [ "$CLEAN" = true ]; then
        log_info "删除 ConfigMaps..."
        kubectl delete configmap --all -n "$NAMESPACE" --wait=false 2>/dev/null || true

        log_info "删除 Secrets..."
        kubectl delete secret --all -n "$NAMESPACE" --wait=false 2>/dev/null || true

        log_info "删除 PVCs..."
        kubectl delete pvc --all -n "$NAMESPACE" --wait=false 2>/dev/null || true
    fi

    # 等待 Pod 终止
    log_info "等待 Pod 终止..."
    for i in {1..30}; do
        pod_count=$(kubectl get pods -n "$NAMESPACE" --no-headers 2>/dev/null | wc -l | tr -d ' ')
        if [ "$pod_count" = "0" ]; then
            break
        fi
        sleep 2
    done

    log_success "Nimbus Platform 已停止"

    if [ "$CLEAN" = false ]; then
        echo ""
        log_info "提示: PVC 数据已保留，重新启动时数据不会丢失"
        log_info "如需完全清理，请使用: ./stop.sh --clean"
    fi
fi

echo ""
echo "=============================================="
echo "   停止完成"
echo "=============================================="
echo ""

# 显示剩余资源
remaining=$(kubectl get all -n "$NAMESPACE" 2>/dev/null | grep -v "^$" | wc -l | tr -d ' ')
if [ "$remaining" -gt 1 ]; then
    log_info "剩余资源:"
    kubectl get all -n "$NAMESPACE" 2>/dev/null || true
fi

echo ""
echo -e "${GREEN}重新启动:${NC} ./start.sh --skip-images"
echo ""
