#!/bin/sh
set -e

# Firecracker networking setup is only needed when KVM is available (Firecracker mode).
if [ -e /dev/kvm ]; then
    # Setup network bridge if it doesn't exist
    if ! ip link show fcbr0 > /dev/null 2>&1; then
        ip link add fcbr0 type bridge
        ip addr add 172.20.0.1/16 dev fcbr0
        ip link set fcbr0 up

        # Enable IP forwarding
        echo 1 > /proc/sys/net/ipv4/ip_forward

        # Setup NAT
        iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE
        iptables -A FORWARD -i fcbr0 -j ACCEPT
        iptables -A FORWARD -o fcbr0 -j ACCEPT
    fi
fi

# Create required directories
mkdir -p /opt/firecracker/sockets
mkdir -p /opt/firecracker/vsock
mkdir -p /opt/firecracker/logs
mkdir -p /opt/firecracker/rootfs

# Populate rootfs templates if mounted volume is empty.
if [ -d /opt/firecracker/rootfs-base ]; then
    for rt in python3.11 nodejs20 go1.24 wasm; do
        if [ -f "/opt/firecracker/rootfs-base/${rt}/rootfs.ext4" ] && [ ! -f "/opt/firecracker/rootfs/${rt}/rootfs.ext4" ]; then
            mkdir -p "/opt/firecracker/rootfs/${rt}"
            cp "/opt/firecracker/rootfs-base/${rt}/rootfs.ext4" "/opt/firecracker/rootfs/${rt}/rootfs.ext4"
        fi
    done
fi

# Execute the command
exec "$@"
