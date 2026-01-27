#!/bin/bash
set -e

# Firecracker setup script for Ubuntu (Alibaba Cloud)
# Run with: sudo bash scripts/setup-firecracker.sh

FC_VERSION="v1.6.0"
BASE_DIR="/opt/firecracker"
ARCH=$(uname -m)

# Alibaba Cloud mirrors
ALPINE_MIRROR="https://mirrors.aliyun.com/alpine"

# Get project directory
SCRIPT_PATH="$(readlink -f "$0")"
SCRIPT_DIR="$(dirname "$SCRIPT_PATH")"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== Setting up Firecracker ==="
echo "Architecture: ${ARCH}"
echo "Project: ${PROJECT_DIR}"

# Create directories
mkdir -p ${BASE_DIR}/{bin,kernel,rootfs,sockets,vsock,snapshots,logs}
mkdir -p ${BASE_DIR}/rootfs/{python3.11,nodejs20,go1.24,wasm}

# 1. Download Firecracker
echo "[1/4] Downloading Firecracker ${FC_VERSION}..."
cd /tmp
curl -sSL -o firecracker.tgz \
  "https://github.com/firecracker-microvm/firecracker/releases/download/${FC_VERSION}/firecracker-${FC_VERSION}-${ARCH}.tgz"
tar xzf firecracker.tgz
cp release-${FC_VERSION}-${ARCH}/firecracker-${FC_VERSION}-${ARCH} ${BASE_DIR}/bin/firecracker
chmod +x ${BASE_DIR}/bin/firecracker
rm -rf firecracker.tgz release-${FC_VERSION}-${ARCH}
echo "✓ Firecracker installed: ${BASE_DIR}/bin/firecracker"

# 2. Download kernel
echo "[2/4] Downloading Linux kernel for ${ARCH}..."
if [ "$ARCH" = "x86_64" ]; then
    KERNEL_URL="https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.6/x86_64/vmlinux-5.10.198"
elif [ "$ARCH" = "aarch64" ]; then
    KERNEL_URL="https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.6/aarch64/vmlinux-5.10.198"
else
    echo "Unsupported architecture: ${ARCH}"
    exit 1
fi

curl -sSL -o ${BASE_DIR}/kernel/vmlinux "${KERNEL_URL}"

# Verify kernel
if file ${BASE_DIR}/kernel/vmlinux | grep -qE "(ELF|Linux kernel)"; then
    echo "✓ Kernel installed: ${BASE_DIR}/kernel/vmlinux"
else
    echo "ERROR: Kernel download failed"
    exit 1
fi

# 3. Build agent first (needed for rootfs)
echo "[3/4] Building agent..."
cd "${PROJECT_DIR}"

# Install Go if not present
if ! command -v go &> /dev/null; then
    echo "  Installing Go..."
    curl -sSL https://go.dev/dl/go1.24.0.linux-${ARCH}.tar.gz | tar -C /usr/local -xz
    export PATH=$PATH:/usr/local/go/bin
fi

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ${BASE_DIR}/bin/agent ./cmd/agent/
echo "✓ Agent built: ${BASE_DIR}/bin/agent"

# 4. Create minimal Alpine rootfs
echo "[4/4] Creating rootfs images..."

# Determine Alpine arch name
if [ "$ARCH" = "x86_64" ]; then
    ALPINE_ARCH="x86_64"
elif [ "$ARCH" = "aarch64" ]; then
    ALPINE_ARCH="aarch64"
fi

create_rootfs() {
    local RUNTIME=$1
    local PACKAGES=$2
    local SIZE_MB=${3:-256}
    local IMG="${BASE_DIR}/rootfs/${RUNTIME}/rootfs.ext4"

    echo "  Creating ${RUNTIME} rootfs (${SIZE_MB}MB)..."

    # Create sparse file
    dd if=/dev/zero of=${IMG} bs=1M count=0 seek=${SIZE_MB} 2>/dev/null
    mkfs.ext4 -F -q ${IMG}

    # Mount
    MOUNT_DIR=$(mktemp -d)
    mount ${IMG} ${MOUNT_DIR}

    # Download Alpine minirootfs from Aliyun mirror
    curl -sSL "${ALPINE_MIRROR}/v3.19/releases/${ALPINE_ARCH}/alpine-minirootfs-3.19.0-${ALPINE_ARCH}.tar.gz" | tar xz -C ${MOUNT_DIR}

    # Setup networking and mirrors
    echo "nameserver 223.5.5.5" > ${MOUNT_DIR}/etc/resolv.conf
    mkdir -p ${MOUNT_DIR}/var/function

    # Use Aliyun Alpine mirror
    cat > ${MOUNT_DIR}/etc/apk/repositories << EOF
${ALPINE_MIRROR}/v3.19/main
${ALPINE_MIRROR}/v3.19/community
EOF

    # Install packages
    if [ -n "$PACKAGES" ]; then
        chroot ${MOUNT_DIR} /bin/sh -c "apk update && apk add --no-cache ${PACKAGES}" 2>/dev/null || true
    fi

    # Copy agent
    cp ${BASE_DIR}/bin/agent ${MOUNT_DIR}/usr/local/bin/agent
    chmod +x ${MOUNT_DIR}/usr/local/bin/agent

    # Create init script
    cat > ${MOUNT_DIR}/init << 'EOF'
#!/bin/sh
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs devtmpfs /dev
exec /usr/local/bin/agent
EOF
    chmod +x ${MOUNT_DIR}/init

    # Cleanup
    umount ${MOUNT_DIR}
    rmdir ${MOUNT_DIR}

    echo "  ✓ ${RUNTIME}: ${IMG}"
}

create_rootfs "python3.11" "python3"
create_rootfs "nodejs20" "nodejs npm"
create_rootfs "go1.24" ""
create_rootfs "wasm" ""

# 5. Setup KVM permissions
echo "[5/5] Setting up KVM permissions..."
if [ -e /dev/kvm ]; then
    chmod 666 /dev/kvm
    echo "✓ KVM permissions set"
else
    echo "WARNING: /dev/kvm not found. Loading KVM module..."
    modprobe kvm
    modprobe kvm_intel 2>/dev/null || modprobe kvm_amd 2>/dev/null || true
    if [ -e /dev/kvm ]; then
        chmod 666 /dev/kvm
        echo "✓ KVM module loaded and permissions set"
    else
        echo "ERROR: KVM not available"
        exit 1
    fi
fi

echo ""
echo "=== Setup complete ==="
echo ""
ls -la ${BASE_DIR}/bin/
ls -la ${BASE_DIR}/kernel/
ls -lh ${BASE_DIR}/rootfs/*/rootfs.ext4
echo ""
echo "Start gateway with:"
echo "  sudo go run ./cmd/gateway/ -config configs/config.yaml"
