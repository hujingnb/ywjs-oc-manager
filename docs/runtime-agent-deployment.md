# Runtime Agent 部署说明

本文说明全新自动注册模式下如何部署 runtime-agent。管理员后台不再创建节点，也不会生成 bootstrap token；节点由 agent 启动时主动 enroll。

## 1. 生成共享密钥

manager 与所有 runtime-agent 使用同一个 enrollment secret：

```bash
openssl rand -base64 32
```

把结果写入：

- `config/manager.yaml`：`runtime.enrollment_secret`
- 每台节点的 `agent.yaml`：`manager.enrollment_secret`

该值必须是 base64 编码的 32 字节随机数。轮换时需要同步修改 manager 与所有 agent 并重启。

## 2. manager 配置

`config/manager.yaml` 必须包含：

```yaml
runtime:
  enrollment_secret: "<openssl rand -base64 32>"
  probe:
    interval_seconds: 60
    timeout_seconds: 3
    failure_threshold: 3
    recovery_threshold: 2
```

`runtime.probe.*` 控制 manager 主动探测 agent 的 docker/file 两个 TLS 端口。

## 3. agent 配置

每台 runtime node 准备一份独立 `agent.yaml`：

```yaml
agent:
  name: "runtime-node-1"
  advertise_host: "runtime-node-1.example.com"
  max_apps: 3
  data_root: "/var/lib/oc-agent"
  state_dir: "/var/lib/oc-agent/state"
  docker_socket: "/var/run/docker.sock"
  trusted_cidr: "10.0.0.0/8"
  docker_addr: ":7001"
  file_addr: ":7002"

manager:
  endpoint: "https://manager.example/api/v1"
  enrollment_secret: "<与 manager 相同>"
  ca_bundle: ""
  skip_verify: false
```

`advertise_host` 必须是 manager 能访问到该节点的地址。agent 会自动上报：

- `https://<advertise_host>:7001`
- `https://<advertise_host>:7002`

`max_apps` 由 agent 配置文件定义，启动后会随 enroll 自动同步到 manager。修改该值后，重启 agent 即可让 manager 重新接收新的上限。

## 4. 启动 agent

```bash
docker run -d --name oc-runtime-agent \
  --restart unless-stopped \
  -p 7001:7001 \
  -p 7002:7002 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /var/lib/oc-agent:/var/lib/oc-agent \
  -v /etc/oc-agent/agent.yaml:/etc/oc-agent/agent.yaml:ro \
  -e OC_AGENT_CONFIG=/etc/oc-agent/agent.yaml \
  oc-runtime-agent:0.1.0
```

首次启动时 agent 会生成 `state_dir/agent-id` 与 TLS 证书，然后调用 `POST /api/v1/agent/enroll`。成功后会把 `node-id` 和 `agent-token` 写入 `state_dir`，后续重启直接复用。

## 5. 验证

1. manager 后台打开「运行节点」，应看到新节点自动出现。
2. 节点状态应为 `在线`；如果 manager 能连通 docker/file 端口，探测列会出现最近成功时间。
3. 如 token 丢失、401 或 state 被清空，重启 agent 会重新 enroll。

常见问题：

- `401 enrollment secret 无效`：manager 与 agent 的 `enrollment_secret` 不一致。
- `探测异常`：manager 到 agent 的 `7001/7002` TLS 端口不可达，或 `trusted_cidr` 未放行 manager 出口 IP。
- `agent token 不可用`：节点尚未 enroll 成功，或数据库中的 token 密文丢失；重启 agent 触发重新 enroll。
