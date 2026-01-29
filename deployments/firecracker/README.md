# Nimbus (Firecracker) 单机部署（Linux + KVM）

这个目录提供一个**最小可用**的 Firecracker 版 Nimbus 部署方式：`docker compose` 一键拉起 PostgreSQL、Redis、Nimbus Gateway（内置 Firecracker + Kernel + rootfs）。

## 前置条件

- Linux 服务器已开启 KVM（存在 `/dev/kvm`）
- Docker + Docker Compose Plugin（`docker compose version` 能正常输出）

可选检查：

```bash
ls -l /dev/kvm
ls -l /dev/net/tun
```

## 启动

在仓库根目录执行：

```bash
cd deployments/firecracker
docker compose up -d --build
docker compose logs -f gateway
```

启动成功后：

- API：`http://localhost:8080`
- Metrics：`http://localhost:9090/metrics`

## 创建/调用函数（示例）

```bash
curl -X POST http://localhost:8080/api/v1/functions \
  -H "Content-Type: application/json" \
  -d '{
    "name": "hello",
    "runtime": "python3.11",
    "handler": "handler.main",
    "code": "def main(event):\\n    return {\"message\": \"Hello \" + event.get(\"name\", \"World\")}"
  }'

curl -X POST http://localhost:8080/api/v1/functions/hello/invoke \
  -H "Content-Type: application/json" \
  -d '{"name":"Nimbus"}'
```

## 常见问题

- **必须 privileged 吗？** 需要访问 `/dev/kvm`、创建 TAP/bridge、配置 iptables（容器内），因此 `privileged: true` 是最省事的方式。
- **VM 没网络？** 本项目会在启动参数里为 guest 配置静态 IP（无需 DHCP）。如仍无网络，先确认宿主机允许容器内 iptables、并且容器内 `eth0` 可出网。

## 停止/清理

```bash
docker compose down
# 如需清理数据卷（会删除 PG/Redis 数据与 Firecracker 目录）：
docker compose down -v
```
