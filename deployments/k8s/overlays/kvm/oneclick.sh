#!/bin/bash
# ============================================================================
# Nimbus Platform - KVM(Firecracker) 一键启动脚本（无 registry）
# ============================================================================
# 适用场景:
# - 你没有镜像仓库（registry）
# - 你在 KVM 服务器本机运行 Kubernetes（单节点最简单）
# - 希望一条命令完成：构建镜像 -> 导入节点运行时 -> 部署
#
# 用法:
#   ./oneclick.sh --context nimbus
#
# 可选:
#   ./oneclick.sh --context nimbus --image nimbus:local
#   ./oneclick.sh --skip-build   # 已经有本地镜像
#   ./oneclick.sh --skip-import  # 节点运行时已存在镜像
# ============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../../../.." && pwd)"
NAMESPACE="${NAMESPACE:-nimbus}"

KUBE_CONTEXT=""
IMAGE="nimbus:local"
SKIP_BUILD=false
SKIP_IMPORT=false
ALLOW_MULTI_NODE=false

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

usage() {
	cat <<EOF
Usage: ./oneclick.sh --context <ctx> [options]

Options:
  --context <ctx>        kubectl context name (必填)
  --image <name:tag>     本地镜像名（默认: nimbus:local）
  --skip-build           跳过 docker/nerdctl 构建
  --skip-import          跳过导入到节点运行时
  --allow-multi-node     多节点集群也继续（不推荐：需要每个节点都导入镜像或使用 registry）
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
		--context)
			KUBE_CONTEXT="${2:-}"
			shift 2
			;;
		--image)
			IMAGE="${2:-}"
			shift 2
			;;
		--skip-build)
			SKIP_BUILD=true
			shift
			;;
		--skip-import)
			SKIP_IMPORT=true
			shift
			;;
		--allow-multi-node)
			ALLOW_MULTI_NODE=true
			shift
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

if [ -z "$KUBE_CONTEXT" ]; then
	log_error "--context 不能为空"
	usage
	exit 1
fi

command -v kubectl >/dev/null 2>&1 || { log_error "kubectl 未安装"; exit 1; }

log_info "切换 context: $KUBE_CONTEXT"
kubectl config use-context "$KUBE_CONTEXT" >/dev/null
log_success "Kubernetes context: $(kubectl config current-context)"

NODE_COUNT="$(kubectl get nodes --no-headers 2>/dev/null | wc -l | tr -d ' ')"
if [ "${NODE_COUNT:-0}" != "1" ] && [ "$ALLOW_MULTI_NODE" = false ]; then
	log_error "检测到多节点集群（nodes=$NODE_COUNT）。无 registry 场景需要把镜像导入到每个节点，或改用 registry。"
	log_error "如确认要继续（只导入本机，不保证调度到同一节点），加参数：--allow-multi-node"
	exit 1
fi

RUNTIME="$(kubectl get nodes -o jsonpath='{.items[0].status.nodeInfo.containerRuntimeVersion}' 2>/dev/null || true)"
if [ -z "$RUNTIME" ]; then
	log_warn "无法获取节点 containerRuntimeVersion（继续）"
fi
log_info "Detected node runtime: ${RUNTIME}"

BUILDER=""
SAVE_CMD=""

if [ "$SKIP_BUILD" = false ]; then
	log_info "构建镜像: $IMAGE"

	if command -v docker >/dev/null 2>&1; then
		if docker version >/dev/null 2>&1; then
			BUILDER="docker"
		elif sudo docker version >/dev/null 2>&1; then
			BUILDER="sudo docker"
		fi
	fi

	if [ -z "$BUILDER" ] && command -v nerdctl >/dev/null 2>&1; then
		if nerdctl version >/dev/null 2>&1; then
			BUILDER="nerdctl"
		elif sudo nerdctl version >/dev/null 2>&1; then
			BUILDER="sudo nerdctl"
		fi
	fi

	if [ -z "$BUILDER" ]; then
		log_error "未找到可用的镜像构建工具：docker 或 nerdctl"
		exit 1
	fi

	$BUILDER build -t "$IMAGE" -f "$PROJECT_ROOT/deployments/docker/Dockerfile" "$PROJECT_ROOT"
	log_success "镜像构建完成: $IMAGE"
else
	log_info "跳过镜像构建"
fi

if [ "$SKIP_IMPORT" = false ]; then
	case "$RUNTIME" in
		containerd://*)
			log_info "导入镜像到 containerd（namespace=k8s.io）..."
			if [ -z "$BUILDER" ]; then
				# build 被 skip 时，仍然需要一个 save 工具
				if command -v docker >/dev/null 2>&1 && docker version >/dev/null 2>&1; then
					BUILDER="docker"
				elif command -v nerdctl >/dev/null 2>&1 && nerdctl version >/dev/null 2>&1; then
					BUILDER="nerdctl"
				fi
			fi
			if [ -z "$BUILDER" ]; then
				log_error "无法导入：未找到 docker/nerdctl 用于 save 镜像"
				exit 1
			fi

			if command -v ctr >/dev/null 2>&1; then
				$BUILDER save "$IMAGE" | sudo ctr -n k8s.io images import -
			elif command -v k3s >/dev/null 2>&1; then
				$BUILDER save "$IMAGE" | sudo k3s ctr images import -
			elif command -v microk8s >/dev/null 2>&1; then
				$BUILDER save "$IMAGE" | sudo microk8s ctr image import -
			else
				log_error "找不到 ctr/k3s/microk8s 命令，无法导入到 containerd"
				exit 1
			fi
			log_success "导入完成"
			;;
		docker://*)
			log_info "节点运行时为 docker，已在本机构建镜像，无需额外导入"
			;;
		"")
			log_warn "未识别到节点运行时，跳过导入（可能需要 registry 或手动导入）"
			;;
		*)
			log_warn "暂不支持自动导入该运行时：$RUNTIME（建议使用 registry 或手动导入）"
			;;
	esac
else
	log_info "跳过镜像导入"
fi

log_info "开始部署..."
"$SCRIPT_DIR/start.sh" --context "$KUBE_CONTEXT" --image "$IMAGE"
