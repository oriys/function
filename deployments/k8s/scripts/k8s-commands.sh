#!/bin/bash
# ============================================================================
# Kubernetes 快速诊断命令
# ============================================================================
# 用法: 直接复制粘贴命令或 source 此文件后使用函数
#
# 面试常用命令速查:
# - kubectl get / describe / logs / exec / port-forward
# - kubectl top / events / explain
# - kubectl rollout / scale / edit
# ============================================================================

# ============================================================================
# 1. 基础查询命令
# ============================================================================

# 查看所有命名空间的 Pod
alias k-pods-all='kubectl get pods --all-namespaces -o wide'

# 查看特定命名空间的资源
k-ns() {
    local ns="${1:-default}"
    echo "=== Pods ==="
    kubectl get pods -n $ns -o wide
    echo ""
    echo "=== Services ==="
    kubectl get svc -n $ns
    echo ""
    echo "=== Deployments ==="
    kubectl get deploy -n $ns
}

# ============================================================================
# 2. Pod 状态快速诊断
# ============================================================================

# 查看非 Running 状态的 Pod
k-not-running() {
    kubectl get pods --all-namespaces --field-selector=status.phase!=Running
}

# 查看重启次数最多的 Pod
k-restarts() {
    kubectl get pods --all-namespaces -o jsonpath='{range .items[*]}{.metadata.namespace}{"\t"}{.metadata.name}{"\t"}{.status.containerStatuses[0].restartCount}{"\n"}{end}' | sort -t$'\t' -k3 -rn | head -10
}

# 查看 Pod 的 IP 地址
k-pod-ips() {
    local ns="${1:---all-namespaces}"
    kubectl get pods $ns -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.podIP}{"\n"}{end}'
}

# ============================================================================
# 3. 日志相关
# ============================================================================

# 查看 Pod 日志 (带时间戳)
k-logs() {
    local pod="$1"
    local ns="${2:-default}"
    kubectl logs -n $ns $pod --timestamps --tail=100
}

# 实时跟踪日志
k-logs-follow() {
    local pod="$1"
    local ns="${2:-default}"
    kubectl logs -n $ns $pod -f --timestamps
}

# 查看之前容器的日志 (崩溃后)
k-logs-prev() {
    local pod="$1"
    local ns="${2:-default}"
    kubectl logs -n $ns $pod --previous --tail=100
}

# 查看所有容器的日志
k-logs-all() {
    local pod="$1"
    local ns="${2:-default}"
    kubectl logs -n $ns $pod --all-containers=true --tail=50
}

# ============================================================================
# 4. 资源使用
# ============================================================================

# 节点资源使用
alias k-top-nodes='kubectl top nodes'

# Pod 资源使用 (按 CPU 排序)
k-top-cpu() {
    kubectl top pods --all-namespaces --sort-by=cpu | head -20
}

# Pod 资源使用 (按内存排序)
k-top-mem() {
    kubectl top pods --all-namespaces --sort-by=memory | head -20
}

# 查看节点资源分配情况
k-node-resources() {
    kubectl describe nodes | grep -A 10 "Allocated resources"
}

# ============================================================================
# 5. 事件和问题排查
# ============================================================================

# 查看最近事件
k-events() {
    local ns="${1:-default}"
    kubectl get events -n $ns --sort-by='.lastTimestamp' | tail -20
}

# 查看警告事件
k-warnings() {
    kubectl get events --all-namespaces --field-selector type=Warning
}

# 查看失败的调度
k-failed-scheduling() {
    kubectl get events --all-namespaces --field-selector reason=FailedScheduling
}

# ============================================================================
# 6. 网络诊断
# ============================================================================

# 测试 Service 连通性
k-test-svc() {
    local svc="$1"
    local ns="${2:-default}"
    local port="${3:-80}"
    kubectl run curl-test --rm -it --restart=Never --image=curlimages/curl -- curl -v http://$svc.$ns.svc.cluster.local:$port
}

# 测试 DNS 解析
k-test-dns() {
    local domain="${1:-kubernetes.default}"
    kubectl run dns-test --rm -it --restart=Never --image=busybox:1.36 -- nslookup $domain
}

# 端口转发
k-pf() {
    local resource="$1"
    local ports="$2"
    local ns="${3:-default}"
    kubectl port-forward -n $ns $resource $ports
}

# ============================================================================
# 7. 调试工具
# ============================================================================

# 进入 Pod 执行命令
k-exec() {
    local pod="$1"
    local ns="${2:-default}"
    local cmd="${3:-/bin/sh}"
    kubectl exec -it -n $ns $pod -- $cmd
}

# 启动调试容器 (nicolaka/netshoot 包含各种网络工具)
k-debug() {
    local ns="${1:-default}"
    kubectl run debug --rm -it --restart=Never -n $ns --image=nicolaka/netshoot -- /bin/bash
}

# 启动临时 Pod 并挂载 PVC
k-debug-pvc() {
    local pvc="$1"
    local ns="${2:-default}"
    kubectl run pvc-debug --rm -it --restart=Never -n $ns \
        --image=busybox:1.36 \
        --overrides='{
          "spec": {
            "containers": [{
              "name": "pvc-debug",
              "image": "busybox:1.36",
              "command": ["/bin/sh"],
              "volumeMounts": [{
                "name": "data",
                "mountPath": "/data"
              }]
            }],
            "volumes": [{
              "name": "data",
              "persistentVolumeClaim": {
                "claimName": "'$pvc'"
              }
            }]
          }
        }'
}

# ============================================================================
# 8. 部署操作
# ============================================================================

# 查看 Deployment 状态
k-deploy-status() {
    local deploy="$1"
    local ns="${2:-default}"
    kubectl rollout status deployment/$deploy -n $ns
}

# 查看部署历史
k-deploy-history() {
    local deploy="$1"
    local ns="${2:-default}"
    kubectl rollout history deployment/$deploy -n $ns
}

# 回滚到上一版本
k-rollback() {
    local deploy="$1"
    local ns="${2:-default}"
    kubectl rollout undo deployment/$deploy -n $ns
}

# 重启 Deployment (触发滚动更新)
k-restart() {
    local deploy="$1"
    local ns="${2:-default}"
    kubectl rollout restart deployment/$deploy -n $ns
}

# 扩缩容
k-scale() {
    local deploy="$1"
    local replicas="$2"
    local ns="${3:-default}"
    kubectl scale deployment/$deploy --replicas=$replicas -n $ns
}

# ============================================================================
# 9. YAML 输出和编辑
# ============================================================================

# 获取资源的 YAML (去除状态信息)
k-yaml() {
    local resource="$1"
    local ns="${2:-default}"
    kubectl get $resource -n $ns -o yaml | kubectl neat 2>/dev/null || kubectl get $resource -n $ns -o yaml
}

# 解释 API 字段
alias k-explain='kubectl explain'

# 快速编辑
k-edit() {
    local resource="$1"
    local ns="${2:-default}"
    KUBE_EDITOR="${EDITOR:-vim}" kubectl edit $resource -n $ns
}

# ============================================================================
# 10. 常见问题快速修复
# ============================================================================

# 强制删除 Terminating 状态的 Pod
k-force-delete-pod() {
    local pod="$1"
    local ns="${2:-default}"
    kubectl delete pod $pod -n $ns --grace-period=0 --force
}

# 清理 Evicted Pod
k-cleanup-evicted() {
    kubectl get pods --all-namespaces -o json | \
        jq -r '.items[] | select(.status.reason == "Evicted") | .metadata.namespace + "/" + .metadata.name' | \
        xargs -r -I {} kubectl delete pod {} 2>/dev/null
    echo "Evicted pods cleaned up"
}

# 清理 Completed/Failed Job
k-cleanup-jobs() {
    local ns="${1:-default}"
    kubectl delete jobs -n $ns --field-selector status.successful=1
    kubectl delete jobs -n $ns --field-selector status.failed=1
}

# ============================================================================
# 快速参考卡片
# ============================================================================

k-help() {
    cat << 'EOF'
Kubernetes 快速诊断命令参考

=== Pod 状态 ===
kubectl get pods -o wide                    # 查看 Pod 详情
kubectl describe pod <pod>                   # 详细描述
kubectl logs <pod> --tail=100                # 查看日志
kubectl logs <pod> --previous               # 崩溃前日志
kubectl exec -it <pod> -- /bin/sh            # 进入容器

=== 常见 Pod 状态排查 ===
Pending     → describe 看 Events，通常是资源不足或调度问题
ImagePullBackOff → 检查镜像名称、仓库凭证
CrashLoopBackOff → logs --previous 看崩溃原因
Error       → describe 和 logs 看具体错误
OOMKilled   → 增加内存限制

=== 资源和事件 ===
kubectl top nodes/pods                       # 资源使用
kubectl get events --sort-by='.lastTimestamp' # 事件
kubectl describe node <node>                 # 节点详情

=== 网络诊断 ===
kubectl get svc,ep                           # Service 和 Endpoints
kubectl run test --rm -it --image=busybox -- wget -qO- <svc>

=== 部署操作 ===
kubectl rollout status deploy/<name>         # 部署状态
kubectl rollout history deploy/<name>        # 历史版本
kubectl rollout undo deploy/<name>           # 回滚
kubectl rollout restart deploy/<name>        # 重启

=== 调试 ===
kubectl run debug --rm -it --image=nicolaka/netshoot -- /bin/bash
kubectl debug <pod> --image=busybox --target=<container>
kubectl port-forward <pod> 8080:80
EOF
}

echo "K8s 诊断命令已加载。使用 'k-help' 查看帮助。"
