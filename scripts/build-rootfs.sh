#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
OUTPUT_DIR="${OUTPUT_DIR:-/opt/firecracker/rootfs}"
AGENT_PATH="${PROJECT_DIR}/bin/agent-linux"

echo "Building rootfs images..."

# Build agent for Linux first
cd "$PROJECT_DIR"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$AGENT_PATH" ./cmd/agent

build_rootfs() {
    local runtime=$1
    local size_mb=${2:-512}
    local output_dir="$OUTPUT_DIR/$runtime"
    local output="$output_dir/rootfs.ext4"

    echo "Building rootfs for $runtime..."
    mkdir -p "$output_dir"

    # Create empty ext4 image
    dd if=/dev/zero of="$output" bs=1M count=$size_mb 2>/dev/null
    mkfs.ext4 -F "$output" > /dev/null 2>&1

    # Mount and populate
    local mountpoint=$(mktemp -d)
    mount "$output" "$mountpoint"

    # Use docker to create Alpine rootfs
    docker run --rm -v "$mountpoint:/rootfs" -v "$AGENT_PATH:/agent" alpine:3.19 sh -c '
        # Install base system
        apk add --root /rootfs --initdb --no-cache \
            alpine-base \
            openrc \
            util-linux \
            ca-certificates \
            curl \
            busybox-initscripts

        # Setup init
        ln -sf /sbin/init /rootfs/init

        # Configure serial console
        echo "ttyS0::respawn:/sbin/getty -L ttyS0 115200 vt100" >> /rootfs/etc/inittab

        # Setup boot services
        mkdir -p /rootfs/etc/runlevels/boot
        mkdir -p /rootfs/etc/runlevels/default
        ln -sf /etc/init.d/devfs /rootfs/etc/runlevels/boot/devfs 2>/dev/null || true
        ln -sf /etc/init.d/procfs /rootfs/etc/runlevels/boot/procfs 2>/dev/null || true
        ln -sf /etc/init.d/sysfs /rootfs/etc/runlevels/boot/sysfs 2>/dev/null || true

        # Copy agent
        cp /agent /rootfs/usr/local/bin/agent
        chmod +x /rootfs/usr/local/bin/agent

        # Create agent service
        cat > /rootfs/etc/init.d/agent << "EOF"
#!/sbin/openrc-run
name="nimbus-agent"
command="/usr/local/bin/agent"
command_background="yes"
pidfile="/run/agent.pid"
EOF
        chmod +x /rootfs/etc/init.d/agent

        # Enable agent on boot
        ln -sf /etc/init.d/agent /rootfs/etc/runlevels/default/agent

        # Create function directory
        mkdir -p /rootfs/var/function

        # Configure networking
        cat > /rootfs/etc/network/interfaces << "EOF"
auto lo
iface lo inet loopback

auto eth0
iface eth0 inet dhcp
EOF

        # Setup DNS
        echo "nameserver 8.8.8.8" > /rootfs/etc/resolv.conf
    '

    # Install runtime-specific packages
    case $runtime in
        python3.11)
            docker run --rm -v "$mountpoint:/rootfs" alpine:3.19 sh -c '
                apk add --root /rootfs --no-cache \
                    python3 \
                    py3-pip \
                    py3-setuptools
            '
            ;;
        nodejs20)
            docker run --rm -v "$mountpoint:/rootfs" alpine:3.19 sh -c '
                apk add --root /rootfs --no-cache nodejs npm
            '
            ;;
        go1.24)
            # Go functions are pre-compiled, minimal runtime needed
            echo "Go runtime: minimal rootfs (pre-compiled binaries)"
            ;;
    esac

    # Cleanup
    umount "$mountpoint"
    rmdir "$mountpoint"

    echo "Built: $output"
}

# Build all runtimes
for runtime in python3.11 nodejs20 go1.24; do
    build_rootfs "$runtime" 512
done

echo ""
echo "All rootfs images built successfully!"
echo "Location: $OUTPUT_DIR"
ls -la "$OUTPUT_DIR"
