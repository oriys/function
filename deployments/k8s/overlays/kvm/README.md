# Nimbus 在 Linux(KVM) Kubernetes 上部署（Firecracker）

本目录用于在 **Linux + KVM** 的 Kubernetes 集群中运行 Nimbus（`runtime.mode=firecracker`）。

对外访问默认使用 **NodePort**：

- Gateway HTTP：`30080`
- Metrics：`30090`

## 前置条件

- 集群节点支持 KVM：节点上存在 `/dev/kvm`
- 节点支持 TUN/TAP：存在 `/dev/net/tun`
- 允许创建 **privileged** Pod（或至少允许挂载 `/dev/kvm`、创建 bridge/tap、配置 iptables）
- 已安装 `kubectl`

快速检查（在任意节点）：

```bash
ls -l /dev/kvm
ls -l /dev/net/tun
```

### （可选）在新服务器上安装基础设施

如果你的 KVM 服务器还没装 Kubernetes / Docker，可以用一键脚本安装基础设施（K3s + Docker + 基础依赖）：

```bash
cd deployments/k8s/overlays/kvm
sudo ./install-infra.sh --context nimbus
```

## 1) 准备 Gateway 镜像（推荐自己构建并推送）

Gateway 镜像需要包含 Firecracker + kernel + 各运行时 rootfs（本仓库的 `deployments/docker/Dockerfile` 已内置）。

在你的构建机/CI 上执行：

```bash
docker build -t <your-registry>/nimbus:<tag> -f deployments/docker/Dockerfile .
docker push <your-registry>/nimbus:<tag>
```

（可选）多架构：

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t <your-registry>/nimbus:<tag> \
  -f deployments/docker/Dockerfile . --push
```

### 没有 registry 怎么办（本地导入到节点运行时）

先看你的集群用的容器运行时：

```bash
kubectl get nodes -o jsonpath='{.items[0].status.nodeInfo.containerRuntimeVersion}{"\n"}'
```

然后在 **能构建镜像的机器**（通常就是这台 KVM 服务器）构建：

```bash
docker build -t nimbus:local -f deployments/docker/Dockerfile .
```

把镜像导入到节点运行时（按你的 runtime 选一个）：

- **containerd**（kubeadm/多数发行版常见）：
  ```bash
  docker save nimbus:local | sudo ctr -n k8s.io images import -
  ```
- **k3s**：
  ```bash
  docker save nimbus:local | sudo k3s ctr images import -
  ```
- **microk8s**：
  ```bash
  docker save nimbus:local | sudo microk8s ctr image import -
  ```

多节点集群需要把镜像导入到 **所有可能调度 Gateway 的节点**（或把 Gateway 约束到某个节点）。

## 2) 部署到集群

```bash
kubectl config use-context <your-context>
cd deployments/k8s/overlays/kvm

# 如果你使用自己构建的镜像（推荐）：
./start.sh --context <your-context> --image <your-registry>/nimbus:<tag>

# 如果你直接用默认镜像（overlay/base 里写死的镜像）：
# ./start.sh --context <your-context>
```

如果你是“没有 registry”的方案：

```bash
./start.sh --context <your-context> --image nimbus:local
```

也可以直接用一键脚本（构建 + 导入 + 部署，一条命令）：

```bash
./oneclick.sh --context <your-context>
```

## 3) 验证

```bash
kubectl -n nimbus get pods -o wide
kubectl -n nimbus get svc nimbus-gateway-external -o wide
kubectl -n nimbus logs -l app=nimbus-gateway -f
```

获取节点 IP：

```bash
kubectl get nodes -o wide
```

然后访问：

- API：`http://<node-ip>:30080`
- Metrics：`http://<node-ip>:30090/metrics`

如果是云服务器，记得在安全组/防火墙放行 `30080/30090`。

## 4) 创建/调用函数（示例）

```bash
curl -X POST http://<node-ip>:30080/api/v1/functions \
  -H "Content-Type: application/json" \
  -d '{
    "name": "hello",
    "runtime": "python3.11",
    "handler": "handler.main",
    "code": "def main(event):\n    return {\"message\": \"Hello \" + event.get(\"name\", \"World\")}"
  }'

curl -X POST http://<node-ip>:30080/api/v1/functions/hello/invoke \
  -H "Content-Type: application/json" \
  -d '{"name":"Nimbus"}'
```

## 常见问题

- Pod 起不来/CrashLoop：优先看 `kubectl -n nimbus logs -l app=nimbus-gateway -f`，通常是 `/dev/kvm` 不存在/权限不足，或 PSA/安全策略禁止 privileged。
- Pod 内看不到设备：`kubectl -n nimbus exec -it deploy/nimbus-gateway -- ls -l /dev/kvm /dev/net/tun`
- NodePort 访问不到：检查云防火墙/安全组，或节点没有对外 IP。

## 说明（生产注意）

- 本 overlay 内的 Postgres/Redis/NATS 使用 `Deployment + emptyDir`（演示用），Pod 重建会丢数据；生产请改用 StatefulSet + PVC 或外部托管服务。
- 为避免不同 CNI/NetworkPolicy 环境下的“默认拒绝”导致依赖不可达，本 overlay 默认删除了 base 的 `NetworkPolicy`（见 `delete-network-policies.yaml`）。你可以按需恢复并完善规则。
