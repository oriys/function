#!/bin/bash
# ============================================================================
# Nimbus (KVM/Firecracker) - 基础设施安装脚本（单机/单节点）
# ============================================================================
# 目标：在一台 Linux + KVM 服务器上安装运行 Nimbus 所需的基础设施：
# - KVM/TUN 设备检查/加载
# - Docker（用于构建 Nimbus 镜像；可选）
# - K3s（单节点 Kubernetes；提供 kubectl/containerd）
# -（可选）放行防火墙端口：6443/30080/30090
#
# 用法:
#   sudo ./install-infra.sh --context nimbus
#
# 可选参数:
#   --context <name>     kubeconfig context 名（默认: nimbus）
#   --no-docker          不安装 Docker（你需要自己准备 docker/nerdctl 来构建镜像）
#   --k3s-version <ver>  指定 k3s 版本（例如 v1.30.6+k3s1）
#   --skip-firewall      不尝试修改 ufw/firewalld
# ============================================================================

set -euo pipefail

CONTEXT_NAME="nimbus"
INSTALL_DOCKER=true
K3S_VERSION=""
SKIP_FIREWALL=false

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

usage() {
	cat <<EOF
Usage: sudo ./install-infra.sh [options]

Options:
  --context <name>     kubeconfig context 名（默认: nimbus）
  --no-docker          不安装 Docker
  --k3s-version <ver>  指定 k3s 版本（例如 v1.30.6+k3s1）
  --skip-firewall      不尝试修改 ufw/firewalld
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
		--context)
			CONTEXT_NAME="${2:-}"
			shift 2
			;;
		--no-docker)
			INSTALL_DOCKER=false
			shift
			;;
		--k3s-version)
			K3S_VERSION="${2:-}"
			shift 2
			;;
		--skip-firewall)
			SKIP_FIREWALL=true
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

if [ "$(id -u)" -ne 0 ]; then
	log_error "请用 root 执行（例如 sudo ./install-infra.sh）"
	exit 1
fi

if [ "$(uname -s)" != "Linux" ]; then
	log_error "仅支持 Linux"
	exit 1
fi

install_pkgs() {
	# 尽量只安装必要的基础工具（curl/cert/iptables/iproute）
	if command -v apt-get >/dev/null 2>&1; then
		log_info "使用 apt-get 安装依赖..."
		apt-get update -y
		DEBIAN_FRONTEND=noninteractive apt-get install -y \
			ca-certificates curl iptables iproute2 socat
	elif command -v dnf >/dev/null 2>&1; then
		log_info "使用 dnf 安装依赖..."
		dnf install -y ca-certificates curl iptables iproute socat
	elif command -v yum >/dev/null 2>&1; then
		log_info "使用 yum 安装依赖..."
		yum install -y ca-certificates curl iptables iproute socat
	else
		log_warn "未识别包管理器，跳过依赖安装（请确保已安装 curl/iptables/ip 命令）"
	fi
}

ensure_kvm() {
	log_info "检查/加载 KVM 与 TUN..."

	# KVM
	if [ ! -e /dev/kvm ]; then
		modprobe kvm >/dev/null 2>&1 || true
		modprobe kvm_intel >/dev/null 2>&1 || modprobe kvm_amd >/dev/null 2>&1 || true
	fi
	if [ ! -e /dev/kvm ]; then
		log_error "未检测到 /dev/kvm：请确认 CPU 支持虚拟化且 BIOS/宿主机已开启（vmx/svm），并加载 kvm 模块"
		exit 1
	fi

	# TUN
	if [ ! -c /dev/net/tun ]; then
		modprobe tun >/dev/null 2>&1 || true
		mkdir -p /dev/net
		mknod /dev/net/tun c 10 200 >/dev/null 2>&1 || true
		chmod 666 /dev/net/tun >/dev/null 2>&1 || true
	fi
	if [ ! -c /dev/net/tun ]; then
		log_error "未检测到 /dev/net/tun：请确认内核启用 tun 模块"
		exit 1
	fi

	log_success "KVM/TUN OK"
}

install_docker() {
	if [ "$INSTALL_DOCKER" = false ]; then
		log_info "跳过 Docker 安装（--no-docker）"
		return 0
	fi

	if command -v docker >/dev/null 2>&1 && docker version >/dev/null 2>&1; then
		log_info "Docker 已安装，跳过"
		return 0
	fi

	log_info "安装 Docker（get.docker.com）..."
	curl -fsSL https://get.docker.com | sh

	# 启用并启动
	if command -v systemctl >/dev/null 2>&1; then
		systemctl enable --now docker >/dev/null 2>&1 || true
	fi

	log_success "Docker 安装完成"
}

disable_swap_runtime() {
	if command -v swapon >/dev/null 2>&1; then
		if swapon --summary 2>/dev/null | grep -q .; then
			log_warn "检测到 swap 已开启：临时关闭（不修改 /etc/fstab）"
			swapoff -a || true
			log_warn "如需永久关闭 swap，请自行修改 /etc/fstab 并重启"
		fi
	fi
}

install_k3s() {
	if command -v k3s >/dev/null 2>&1; then
		log_info "k3s 已安装，跳过"
	else
		log_info "安装 k3s（单节点）..."
		disable_swap_runtime
		if [ -n "$K3S_VERSION" ]; then
			ENV_VARS="INSTALL_K3S_VERSION=$K3S_VERSION"
		else
			ENV_VARS=""
		fi

		# 关闭 traefik（本项目默认用 NodePort/Ingress 可选）
		# 写 kubeconfig 权限 644，方便非 root 使用 kubectl
		eval "$ENV_VARS curl -sfL https://get.k3s.io | sh -s - server --write-kubeconfig-mode 644 --disable traefik"
	fi

	# 等待 kubeconfig
	for _ in $(seq 1 60); do
		if [ -f /etc/rancher/k3s/k3s.yaml ]; then
			break
		fi
		sleep 1
	done
	if [ ! -f /etc/rancher/k3s/k3s.yaml ]; then
		log_error "k3s kubeconfig 未生成：/etc/rancher/k3s/k3s.yaml"
		exit 1
	fi

	export KUBECONFIG=/etc/rancher/k3s/k3s.yaml

	# 等待节点 Ready
	log_info "等待节点就绪..."
	for _ in $(seq 1 60); do
		if kubectl get nodes >/dev/null 2>&1; then
			break
		fi
		sleep 2
	done

	# 尝试重命名 context
	# k3s 默认 context 往往叫 "default"
	kubectl config rename-context default "$CONTEXT_NAME" >/dev/null 2>&1 || true
	log_success "k3s 就绪，context: $CONTEXT_NAME"
}

setup_kubeconfig_for_users() {
	local kubeconfig=/etc/rancher/k3s/k3s.yaml

	# root
	mkdir -p /root/.kube
	cp -f "$kubeconfig" /root/.kube/config
	chmod 600 /root/.kube/config

	# sudo 用户（如果有）
	if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
		local home_dir
		home_dir="$(getent passwd "$SUDO_USER" | cut -d: -f6)"
		if [ -n "$home_dir" ] && [ -d "$home_dir" ]; then
			mkdir -p "$home_dir/.kube"
			cp -f "$kubeconfig" "$home_dir/.kube/config"
			chown -R "$SUDO_USER":"$SUDO_USER" "$home_dir/.kube"
			chmod 600 "$home_dir/.kube/config"
			log_success "已为用户 $SUDO_USER 写入 kubeconfig: $home_dir/.kube/config"
		fi
	fi
}

open_firewall_ports() {
	if [ "$SKIP_FIREWALL" = true ]; then
		log_info "跳过防火墙修改（--skip-firewall）"
		return 0
	fi

	# NodePort: 30080/30090，k3s apiserver: 6443
	local ports=("6443/tcp" "30080/tcp" "30090/tcp")

	if command -v ufw >/dev/null 2>&1; then
		if ufw status 2>/dev/null | grep -qi "Status: active"; then
			log_info "配置 ufw 端口放行..."
			for p in "${ports[@]}"; do
				ufw allow "$p" >/dev/null 2>&1 || true
			done
			log_success "ufw 放行完成"
		fi
	fi

	if command -v firewall-cmd >/dev/null 2>&1; then
		if firewall-cmd --state >/dev/null 2>&1; then
			log_info "配置 firewalld 端口放行..."
			for p in "${ports[@]}"; do
				firewall-cmd --permanent --add-port="$p" >/dev/null 2>&1 || true
			done
			firewall-cmd --reload >/dev/null 2>&1 || true
			log_success "firewalld 放行完成"
		fi
	fi
}

echo ""
echo "=============================================="
echo "   Nimbus KVM 基础设施安装"
echo "=============================================="
echo ""

install_pkgs
ensure_kvm
install_docker
install_k3s
setup_kubeconfig_for_users
open_firewall_ports

echo ""
log_success "基础设施安装完成"
echo ""
echo "下一步（无 registry 一键部署 Nimbus）："
echo "  cd deployments/k8s/overlays/kvm"
echo "  ./oneclick.sh --context $CONTEXT_NAME"
echo ""
