# ==============================================================================
# Nimbus 函数计算平台 Dockerfile
# ==============================================================================
# 此 Dockerfile 构建函数计算平台的完整镜像
# 包含网关服务、Agent 程序和各运行时的根文件系统
#
# 构建命令: docker build -t nimbus-gateway .

# ==============================================================================
# 第一阶段：构建 Go 二进制文件
# ==============================================================================
FROM golang:1.24-alpine AS builder

# 安装构建工具
# git: 用于获取版本信息
# make: 用于执行 Makefile
# curl: 用于下载依赖
RUN apk add --no-cache git make curl

WORKDIR /app

# 设置目标平台（支持多架构构建）
ARG TARGETOS
ARG TARGETARCH

# 首先复制依赖文件，利用 Docker 缓存加速构建
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码并编译
COPY . .

# 编译网关服务（静态链接，无 CGO 依赖）
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -o /out/gateway ./cmd/gateway

# 编译 Agent 程序（运行在 Firecracker 虚拟机内）
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -o /out/agent ./cmd/agent

# ==============================================================================
# 第二阶段：构建各运行时的根文件系统 (ext4 格式)
# ==============================================================================
# Firecracker 虚拟机需要 ext4 格式的根文件系统镜像
FROM alpine:3.19 AS rootfs

# 安装 ext4 文件系统工具
RUN apk add --no-cache e2fsprogs

# 复制 Agent 二进制文件（将被安装到每个根文件系统中）
COPY --from=builder /out/agent /agent

# 构建所有运行时的根文件系统
# 这个脚本为每个运行时创建一个最小化的 Alpine Linux 根文件系统
RUN set -eux; \
  # 定义构建根文件系统的函数
  # 参数: runtime(运行时名称) pkgs(需要安装的包) size_mb(镜像大小)
  build_rootfs() { \
    runtime="$1"; \
    pkgs="$2"; \
    size_mb="${3:-512}"; \
    rootdir="$(mktemp -d)"; \
    # 安装基础系统包
    apk add --root "$rootdir" --keys-dir /etc/apk/keys --repositories-file /etc/apk/repositories --initdb --no-cache \
      alpine-base openrc util-linux ca-certificates curl; \
    # 安装运行时特定的包
    if [ -n "$pkgs" ]; then \
      apk add --root "$rootdir" --keys-dir /etc/apk/keys --repositories-file /etc/apk/repositories --no-cache $pkgs; \
    fi; \
    # 配置 init 系统
    ln -sf /sbin/init "$rootdir/init"; \
    echo "ttyS0::respawn:/sbin/getty -L ttyS0 115200 vt100" >> "$rootdir/etc/inittab"; \
    mkdir -p "$rootdir/etc/runlevels/boot" "$rootdir/etc/runlevels/default"; \
    # 配置启动服务
    ln -sf /etc/init.d/devfs "$rootdir/etc/runlevels/boot/devfs" 2>/dev/null || true; \
    ln -sf /etc/init.d/procfs "$rootdir/etc/runlevels/boot/procfs" 2>/dev/null || true; \
    ln -sf /etc/init.d/sysfs "$rootdir/etc/runlevels/boot/sysfs" 2>/dev/null || true; \
    # 安装 Agent 程序
    mkdir -p "$rootdir/usr/local/bin"; \
    cp /agent "$rootdir/usr/local/bin/agent"; \
    chmod +x "$rootdir/usr/local/bin/agent"; \
    # 创建 Agent 服务（OpenRC 格式）
    printf '%s\n' \
      '#!/sbin/openrc-run' \
      'name="nimbus-agent"' \
      'command="/usr/local/bin/agent"' \
      'command_background="yes"' \
      'pidfile="/run/agent.pid"' \
      > "$rootdir/etc/init.d/agent"; \
    chmod +x "$rootdir/etc/init.d/agent"; \
    # 设置 Agent 服务开机自启
    ln -sf /etc/init.d/agent "$rootdir/etc/runlevels/default/agent"; \
    # 创建函数代码存储目录
    mkdir -p "$rootdir/var/function"; \
    # 配置网络
    printf '%s\n' \
      'auto lo' \
      'iface lo inet loopback' \
      '' \
      'auto eth0' \
      'iface eth0 inet dhcp' \
      > "$rootdir/etc/network/interfaces"; \
    echo "nameserver 8.8.8.8" > "$rootdir/etc/resolv.conf"; \
    # 创建 ext4 镜像
    outdir="/out/$runtime"; \
    mkdir -p "$outdir"; \
    output="$outdir/rootfs.ext4"; \
    # 创建稀疏文件（节省磁盘空间）
    dd if=/dev/zero of="$output" bs=1M count=0 seek="$size_mb" 2>/dev/null; \
    # 格式化并填充内容
    mkfs.ext4 -F -q -d "$rootdir" "$output"; \
    rm -rf "$rootdir"; \
  }; \
  # 构建各运行时的根文件系统
  # Python 3.11: 包含 Python 解释器和 pip
  build_rootfs "python3.11" "python3 py3-pip py3-setuptools" 512; \
  # Node.js 20: 包含 Node.js 运行时和 npm
  build_rootfs "nodejs20" "nodejs npm" 512; \
  # Go 1.24: 仅包含基础系统（Go 程序是静态编译的）
  build_rootfs "go1.24" "" 256; \
  # WebAssembly: 仅包含基础系统
  build_rootfs "wasm" "" 256

# ==============================================================================
# 第三阶段：构建最终运行时镜像
# ==============================================================================
FROM alpine:3.19

# 安装运行时依赖
# docker-cli: 用于 Docker 模式
# iproute2/iptables: 用于网络配置
# e2fsprogs: 用于根文件系统操作
# 注意: Go 编译使用 Docker 容器 (golang:1.24-alpine)，无需安装 go
RUN apk add --no-cache \
    ca-certificates \
    curl \
    docker-cli \
    iproute2 \
    iptables \
    util-linux \
    e2fsprogs \
    && rm -rf /var/cache/apk/*

# 创建 Firecracker 所需的目录结构
RUN mkdir -p /opt/firecracker/bin \
    /opt/firecracker/kernel \
    /opt/firecracker/rootfs \
    /opt/firecracker/sockets \
    /opt/firecracker/vsock \
    /opt/firecracker/snapshots \
    /opt/firecracker/logs \
    /opt/cni/bin \
    /etc/cni/net.d \
    /etc/nimbus

# 下载 Firecracker 二进制文件
# Firecracker 是 AWS 开源的轻量级虚拟机管理器
ARG FIRECRACKER_VERSION=v1.6.0
ARG TARGETARCH
RUN fc_arch="x86_64"; \
    if [ "${TARGETARCH:-amd64}" = "arm64" ]; then fc_arch="aarch64"; fi; \
    curl -L -o /opt/firecracker/bin/firecracker \
      "https://github.com/firecracker-microvm/firecracker/releases/download/${FIRECRACKER_VERSION}/firecracker-${FIRECRACKER_VERSION}-${fc_arch}" \
      && chmod +x /opt/firecracker/bin/firecracker

# 下载 Linux 内核
# 使用 Firecracker 官方提供的精简内核
RUN kernel_arch="x86_64"; \
    if [ "${TARGETARCH:-amd64}" = "arm64" ]; then kernel_arch="aarch64"; fi; \
    curl -L -o /opt/firecracker/kernel/vmlinux \
      "https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.6/${kernel_arch}/vmlinux-5.10.198"

# 从构建阶段复制二进制文件
COPY --from=builder /out/gateway /usr/local/bin/gateway
COPY --from=builder /out/agent /opt/firecracker/agent

# 复制预构建的根文件系统镜像
# 入口脚本会在启动时将这些镜像复制到工作目录
COPY --from=rootfs /out/ /opt/firecracker/rootfs-base/

# 复制默认配置文件
COPY configs/config.yaml /etc/nimbus/config.yaml

# 设置环境变量
# FIRECRACKER_BIN: Firecracker 二进制路径
# KERNEL_PATH: 内核路径
# ROOTFS_DIR: 根文件系统目录
# SOCKET_DIR: Socket 目录
# VSOCK_DIR: vsock 目录
# SNAPSHOT_DIR: 快照目录
# LOG_DIR: 日志目录
ENV FIRECRACKER_BIN=/opt/firecracker/bin/firecracker
ENV KERNEL_PATH=/opt/firecracker/kernel/vmlinux
ENV ROOTFS_DIR=/opt/firecracker/rootfs
ENV SOCKET_DIR=/opt/firecracker/sockets
ENV VSOCK_DIR=/opt/firecracker/vsock
ENV SNAPSHOT_DIR=/opt/firecracker/snapshots
ENV LOG_DIR=/opt/firecracker/logs

# 暴露端口
# 8080: HTTP API
# 8081: 函数调用
# 9090: Prometheus 指标
EXPOSE 8080 8081 9090

# 复制并设置入口脚本
COPY scripts/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# 设置入口点和默认命令
ENTRYPOINT ["/entrypoint.sh"]
CMD ["gateway"]
