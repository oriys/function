#!/bin/bash
# ============================================================================
# Kubernetes 故障排查脚本
# ============================================================================
# 用法: ./troubleshoot.sh [命令] [参数]
#
# 常用命令:
#   status      - 查看集群状态概览
#   pods        - 查看 Pod 状态和问题
#   logs        - 查看 Pod 日志
#   events      - 查看集群事件
#   resources   - 查看资源使用情况
#   network     - 网络诊断
#   storage     - 存储诊断
#   debug       - 启动调试 Pod
#   all         - 运行所有诊断
#
# 面试常问排查流程:
# 1. kubectl get pods - 查看 Pod 状态
# 2. kubectl describe pod - 查看详细信息和事件
# 3. kubectl logs - 查看容器日志
# 4. kubectl exec - 进入容器调试
# 5. kubectl get events - 查看集群事件
# ============================================================================

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 默认命名空间
NAMESPACE="${NAMESPACE:-function}"

# 打印分隔线
print_header() {
    echo ""
    echo -e "${BLUE}============================================${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}============================================${NC}"
}

print_subheader() {
    echo ""
    echo -e "${YELLOW}--- $1 ---${NC}"
}

print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠ $1${NC}"
}

print_error() {
    echo -e "${RED}✗ $1${NC}"
}

# ============================================================================
# 1. 集群状态概览
# ============================================================================
check_cluster_status() {
    print_header "集群状态概览"

    print_subheader "节点状态"
    kubectl get nodes -o wide

    print_subheader "节点资源使用 (需要 metrics-server)"
    kubectl top nodes 2>/dev/null || print_warning "metrics-server 未安装"

    print_subheader "命名空间列表"
    kubectl get namespaces

    print_subheader "集群组件状态"
    kubectl get componentstatuses 2>/dev/null || kubectl get --raw='/readyz?verbose' 2>/dev/null | head -20 || print_warning "无法获取组件状态"
}

# ============================================================================
# 2. Pod 状态检查
# ============================================================================
check_pods() {
    print_header "Pod 状态检查 - 命名空间: $NAMESPACE"

    print_subheader "所有 Pod 状态"
    kubectl get pods -n $NAMESPACE -o wide

    print_subheader "Pod 资源使用"
    kubectl top pods -n $NAMESPACE 2>/dev/null || print_warning "metrics-server 未安装"

    print_subheader "非 Running 状态的 Pod"
    NOT_RUNNING=$(kubectl get pods -n $NAMESPACE --field-selector=status.phase!=Running --no-headers 2>/dev/null | wc -l)
    if [ "$NOT_RUNNING" -gt 0 ]; then
        print_error "发现 $NOT_RUNNING 个非 Running 状态的 Pod:"
        kubectl get pods -n $NAMESPACE --field-selector=status.phase!=Running
    else
        print_success "所有 Pod 都在 Running 状态"
    fi

    print_subheader "重启次数较多的 Pod (>3)"
    kubectl get pods -n $NAMESPACE -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{range .status.containerStatuses[*]}{.restartCount}{" "}{end}{"\n"}{end}' | \
        awk '{sum=0; for(i=2;i<=NF;i++) sum+=$i; if(sum>3) print $1"\t重启次数: "sum}'

    print_subheader "Pending Pod 原因分析"
    PENDING_PODS=$(kubectl get pods -n $NAMESPACE --field-selector=status.phase=Pending -o name 2>/dev/null)
    if [ -n "$PENDING_PODS" ]; then
        for pod in $PENDING_PODS; do
            echo "分析: $pod"
            kubectl describe $pod -n $NAMESPACE | grep -A 10 "Events:" | tail -5
        done
    else
        print_success "没有 Pending 状态的 Pod"
    fi
}

# ============================================================================
# 3. 日志检查
# ============================================================================
check_logs() {
    local POD_NAME="${1:-}"
    local CONTAINER="${2:-}"

    print_header "日志检查"

    if [ -z "$POD_NAME" ]; then
        print_subheader "最近有错误日志的 Pod"
        for pod in $(kubectl get pods -n $NAMESPACE -o name); do
            ERRORS=$(kubectl logs $pod -n $NAMESPACE --tail=100 2>/dev/null | grep -ci "error\|exception\|fatal\|panic" || true)
            if [ "$ERRORS" -gt 0 ]; then
                print_warning "$pod - 发现 $ERRORS 条错误相关日志"
            fi
        done
    else
        print_subheader "Pod: $POD_NAME 的日志"

        # 获取容器列表
        CONTAINERS=$(kubectl get pod $POD_NAME -n $NAMESPACE -o jsonpath='{.spec.containers[*].name}' 2>/dev/null)

        if [ -n "$CONTAINER" ]; then
            echo "容器: $CONTAINER"
            kubectl logs $POD_NAME -n $NAMESPACE -c $CONTAINER --tail=100
        else
            for c in $CONTAINERS; do
                echo ""
                echo "=== 容器: $c ==="
                kubectl logs $POD_NAME -n $NAMESPACE -c $c --tail=50
            done
        fi

        print_subheader "之前容器的日志 (如果有)"
        kubectl logs $POD_NAME -n $NAMESPACE --previous --tail=50 2>/dev/null || print_warning "没有之前的容器日志"
    fi
}

# ============================================================================
# 4. 事件检查
# ============================================================================
check_events() {
    print_header "集群事件 - 命名空间: $NAMESPACE"

    print_subheader "最近事件 (按时间排序)"
    kubectl get events -n $NAMESPACE --sort-by='.lastTimestamp' | tail -30

    print_subheader "警告事件"
    WARNING_EVENTS=$(kubectl get events -n $NAMESPACE --field-selector type=Warning --no-headers 2>/dev/null | wc -l)
    if [ "$WARNING_EVENTS" -gt 0 ]; then
        print_warning "发现 $WARNING_EVENTS 条警告事件:"
        kubectl get events -n $NAMESPACE --field-selector type=Warning
    else
        print_success "没有警告事件"
    fi

    print_subheader "调度失败事件"
    kubectl get events -n $NAMESPACE --field-selector reason=FailedScheduling 2>/dev/null || true
}

# ============================================================================
# 5. 资源使用检查
# ============================================================================
check_resources() {
    print_header "资源使用检查"

    print_subheader "ResourceQuota 状态"
    kubectl get resourcequota -n $NAMESPACE 2>/dev/null || print_warning "没有配置 ResourceQuota"

    print_subheader "LimitRange 配置"
    kubectl get limitrange -n $NAMESPACE 2>/dev/null || print_warning "没有配置 LimitRange"

    print_subheader "PVC 使用情况"
    kubectl get pvc -n $NAMESPACE

    print_subheader "节点资源分配"
    echo "请求资源 vs 可分配资源:"
    kubectl describe nodes | grep -A 5 "Allocated resources"
}

# ============================================================================
# 6. 网络诊断
# ============================================================================
check_network() {
    print_header "网络诊断"

    print_subheader "Service 列表"
    kubectl get svc -n $NAMESPACE -o wide

    print_subheader "Endpoints 状态"
    for svc in $(kubectl get svc -n $NAMESPACE -o name); do
        SVC_NAME=$(echo $svc | cut -d'/' -f2)
        EP_COUNT=$(kubectl get endpoints $SVC_NAME -n $NAMESPACE -o jsonpath='{.subsets[*].addresses}' 2>/dev/null | grep -c "ip" || echo "0")
        if [ "$EP_COUNT" -eq 0 ]; then
            print_warning "Service $SVC_NAME 没有可用的 Endpoints"
        else
            print_success "Service $SVC_NAME 有 $EP_COUNT 个 Endpoints"
        fi
    done

    print_subheader "Ingress 配置"
    kubectl get ingress -n $NAMESPACE 2>/dev/null || print_warning "没有配置 Ingress"

    print_subheader "NetworkPolicy"
    kubectl get networkpolicy -n $NAMESPACE 2>/dev/null || print_warning "没有配置 NetworkPolicy"

    print_subheader "DNS 解析测试 (需要运行中的 Pod)"
    FIRST_POD=$(kubectl get pods -n $NAMESPACE -o name | head -1)
    if [ -n "$FIRST_POD" ]; then
        echo "从 $FIRST_POD 测试 DNS:"
        kubectl exec $FIRST_POD -n $NAMESPACE -- nslookup kubernetes.default 2>/dev/null || print_warning "DNS 测试失败或 Pod 无 nslookup"
    fi
}

# ============================================================================
# 7. 存储诊断
# ============================================================================
check_storage() {
    print_header "存储诊断"

    print_subheader "StorageClass 列表"
    kubectl get storageclass

    print_subheader "PersistentVolume 状态"
    kubectl get pv

    print_subheader "PersistentVolumeClaim 状态"
    kubectl get pvc -n $NAMESPACE

    print_subheader "未绑定的 PVC"
    UNBOUND_PVC=$(kubectl get pvc -n $NAMESPACE --field-selector status.phase!=Bound --no-headers 2>/dev/null | wc -l)
    if [ "$UNBOUND_PVC" -gt 0 ]; then
        print_error "发现 $UNBOUND_PVC 个未绑定的 PVC:"
        kubectl get pvc -n $NAMESPACE --field-selector status.phase!=Bound
    else
        print_success "所有 PVC 都已绑定"
    fi
}

# ============================================================================
# 8. 启动调试 Pod
# ============================================================================
start_debug_pod() {
    print_header "启动调试 Pod"

    local DEBUG_IMAGE="${1:-nicolaka/netshoot:latest}"

    echo "使用镜像: $DEBUG_IMAGE"
    echo "提供的工具: curl, wget, ping, nslookup, dig, tcpdump, netstat, iperf..."

    kubectl run debug-pod \
        --image=$DEBUG_IMAGE \
        --rm -it \
        --restart=Never \
        -n $NAMESPACE \
        -- /bin/bash
}

# ============================================================================
# 9. Pod 详细诊断
# ============================================================================
diagnose_pod() {
    local POD_NAME="$1"

    if [ -z "$POD_NAME" ]; then
        echo "用法: $0 diagnose <pod-name>"
        exit 1
    fi

    print_header "Pod 诊断: $POD_NAME"

    print_subheader "基本信息"
    kubectl get pod $POD_NAME -n $NAMESPACE -o wide

    print_subheader "详细描述"
    kubectl describe pod $POD_NAME -n $NAMESPACE

    print_subheader "容器状态"
    kubectl get pod $POD_NAME -n $NAMESPACE -o jsonpath='{range .status.containerStatuses[*]}{"容器: "}{.name}{"\n"}{"  状态: "}{.state}{"\n"}{"  重启次数: "}{.restartCount}{"\n"}{end}'

    print_subheader "最近日志"
    kubectl logs $POD_NAME -n $NAMESPACE --tail=50 --all-containers=true

    print_subheader "相关事件"
    kubectl get events -n $NAMESPACE --field-selector involvedObject.name=$POD_NAME --sort-by='.lastTimestamp'
}

# ============================================================================
# 10. 常见问题诊断
# ============================================================================
diagnose_common_issues() {
    print_header "常见问题诊断"

    # 检查 ImagePullBackOff
    print_subheader "镜像拉取问题"
    PULL_ERRORS=$(kubectl get pods -n $NAMESPACE -o jsonpath='{.items[*].status.containerStatuses[*].state.waiting.reason}' | grep -c "ImagePull" || echo "0")
    if [ "$PULL_ERRORS" -gt 0 ]; then
        print_error "发现镜像拉取问题:"
        kubectl get pods -n $NAMESPACE | grep -E "ImagePullBackOff|ErrImagePull"
    else
        print_success "没有镜像拉取问题"
    fi

    # 检查 CrashLoopBackOff
    print_subheader "容器崩溃问题"
    CRASH_PODS=$(kubectl get pods -n $NAMESPACE | grep -c "CrashLoopBackOff" || echo "0")
    if [ "$CRASH_PODS" -gt 0 ]; then
        print_error "发现 $CRASH_PODS 个崩溃循环的 Pod:"
        kubectl get pods -n $NAMESPACE | grep "CrashLoopBackOff"
        echo ""
        echo "排查建议:"
        echo "1. kubectl logs <pod> --previous  # 查看崩溃前的日志"
        echo "2. kubectl describe pod <pod>     # 查看退出码和原因"
    else
        print_success "没有崩溃循环的 Pod"
    fi

    # 检查 OOMKilled
    print_subheader "内存溢出问题 (OOMKilled)"
    OOM_PODS=$(kubectl get pods -n $NAMESPACE -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.status.containerStatuses[*].lastState.terminated.reason}{"\n"}{end}' | grep -c "OOMKilled" || echo "0")
    if [ "$OOM_PODS" -gt 0 ]; then
        print_error "发现 OOMKilled 的 Pod:"
        kubectl get pods -n $NAMESPACE -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.status.containerStatuses[*].lastState.terminated.reason}{"\n"}{end}' | grep "OOMKilled"
        echo ""
        echo "排查建议:"
        echo "1. 增加内存限制"
        echo "2. 检查应用内存泄漏"
        echo "3. 考虑使用 VPA 自动调整"
    else
        print_success "没有 OOMKilled 的 Pod"
    fi

    # 检查资源不足
    print_subheader "资源不足问题"
    INSUFFICIENT=$(kubectl get events -n $NAMESPACE --field-selector reason=FailedScheduling 2>/dev/null | grep -c "Insufficient" || echo "0")
    if [ "$INSUFFICIENT" -gt 0 ]; then
        print_error "发现资源不足问题:"
        kubectl get events -n $NAMESPACE --field-selector reason=FailedScheduling | grep "Insufficient"
        echo ""
        echo "排查建议:"
        echo "1. kubectl describe nodes | grep -A 5 'Allocated'  # 查看节点资源分配"
        echo "2. 考虑扩容节点或减少 Pod 资源请求"
    else
        print_success "没有资源不足问题"
    fi
}

# ============================================================================
# 主函数
# ============================================================================
main() {
    local COMMAND="${1:-all}"

    case "$COMMAND" in
        status)
            check_cluster_status
            ;;
        pods)
            check_pods
            ;;
        logs)
            check_logs "${2:-}" "${3:-}"
            ;;
        events)
            check_events
            ;;
        resources)
            check_resources
            ;;
        network)
            check_network
            ;;
        storage)
            check_storage
            ;;
        debug)
            start_debug_pod "${2:-}"
            ;;
        diagnose)
            diagnose_pod "${2:-}"
            ;;
        issues)
            diagnose_common_issues
            ;;
        all)
            check_cluster_status
            check_pods
            check_events
            check_resources
            check_network
            check_storage
            diagnose_common_issues
            ;;
        help|--help|-h)
            echo "Kubernetes 故障排查脚本"
            echo ""
            echo "用法: $0 [命令] [参数]"
            echo ""
            echo "命令:"
            echo "  status              - 集群状态概览"
            echo "  pods                - Pod 状态检查"
            echo "  logs [pod] [container] - 日志检查"
            echo "  events              - 事件检查"
            echo "  resources           - 资源使用检查"
            echo "  network             - 网络诊断"
            echo "  storage             - 存储诊断"
            echo "  debug [image]       - 启动调试 Pod"
            echo "  diagnose <pod>      - 诊断特定 Pod"
            echo "  issues              - 常见问题诊断"
            echo "  all                 - 运行所有诊断"
            echo ""
            echo "环境变量:"
            echo "  NAMESPACE           - 目标命名空间 (默认: function)"
            echo ""
            echo "示例:"
            echo "  $0 status"
            echo "  $0 logs nimbus-gateway-xxx"
            echo "  $0 diagnose nimbus-gateway-xxx"
            echo "  NAMESPACE=kube-system $0 pods"
            ;;
        *)
            echo "未知命令: $COMMAND"
            echo "使用 '$0 help' 查看帮助"
            exit 1
            ;;
    esac
}

main "$@"
