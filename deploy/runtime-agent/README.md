# runtime-agent 生产部署

> 每台 Runtime Node 部署 oc-runtime-agent，自动注册到 manager 并管理本机 Hermes 容器。
> 原理详见 `docs/runtime-agent.md`。

## 1. 启动

```bash
cp .env.example .env
cp config/agent.example.yaml config/agent.yaml
${EDITOR:-vi} .env
${EDITOR:-vi} config/agent.yaml
docker compose up -d
```

首次启动时 agent 会生成自签 TLS 证书和节点 ID，然后调用 manager `POST /api/v1/agent/enroll` 完成注册。注册成功后 node-id 和 agent-token 写入 `state_dir`，后续重启直接复用。

若 state 被清空或 token 失效，重启 agent 会自动触发重新注册。

## 2. 必改配置

### `.env`

| 变量 | 说明 |
|------|------|
| `OC_RUNTIME_AGENT_IMAGE` | runtime-agent 镜像，aliyun ACR 私有仓库，使用具体版本 tag 或 `@sha256:` digest（禁用 `latest`） |
| `RUNTIME_AGENT_GRPC_PORT` | gRPC 控制面端口，默认 `7001` |
| `RUNTIME_AGENT_HTTP_PORT` | HTTP 文件/健康检查端口，默认 `7002` |

### `config/agent.yaml` 完整字段说明

#### `agent` 段

| 字段 | 必填 | 说明 |
|------|------|------|
| `agent.name` | 否 | 节点展示名；为空时 manager 使用 agent_id 前 8 位生成默认名 |
| `agent.advertise_host` | 建议填写 | agent 上报给 manager 的对外可达主机名或 IP；留空时使用本机 hostname，生产建议显式配置 |
| `agent.max_apps` | 否 | 节点最大可承载应用数；`null` 或留空表示不限，`0` 表示暂停接收新应用；修改后重启 agent 生效 |
| `agent.data_root` | 是 | agent 业务数据根目录，存放组织和应用的文件数据；为空时启动失败 |
| `agent.state_dir` | 是 | agent 自身状态目录，存放 TLS 证书、注册结果等元数据；为空时启动失败；推荐使用 `data_root` 子目录 |
| `agent.docker_socket` | 是 | 宿主机 Docker daemon socket 路径，容器部署时 mount 宿主 `/var/run/docker.sock` |
| `agent.trusted_cidr` | 是 | 允许调用 agent 接口的客户端 CIDR，填写 manager 服务器出口 CIDR；留空只适合本地调试 |
| `agent.docker_addr` | 是 | Docker TLS proxy 监听地址，形如 `:7001`；为空时启动失败 |
| `agent.file_addr` | 是 | 文件/scope API HTTP server 监听地址，形如 `:7002`；为空时启动失败 |

#### `manager` 段

| 字段 | 必填 | 说明 |
|------|------|------|
| `manager.endpoint` | 是 | manager API 地址，须包含 `/api/v1`，例如 `https://manager.example.com/api/v1` |
| `manager.enrollment_secret` | 是 | 与 manager `config/manager.yaml` 的 `runtime.enrollment_secret` 完全一致；使用 `openssl rand -base64 32` 生成 |
| `manager.ca_bundle` | 否 | manager 自签 TLS CA PEM 全文；留空则信任系统根证书 |
| `manager.skip_verify` | 否 | 生产必须保持 `false`；仅本地自签调试时可临时设为 `true` |

#### 生成共享密钥

manager 与所有 runtime-agent 使用同一个 enrollment secret：

```bash
openssl rand -base64 32
```

把结果同步写入：
- manager `config/manager.yaml`：`runtime.enrollment_secret`
- 每台节点 `config/agent.yaml`：`manager.enrollment_secret`

轮换时需同步修改 manager 与所有 agent 并重启。

#### 配置示例

```yaml
agent:
  name: "runtime-node-1"
  advertise_host: "runtime-node-1.example.com"
  max_apps: 3
  data_root: "/var/lib/oc-agent"
  state_dir: "/var/lib/oc-agent/state"
  docker_socket: "/var/run/docker.sock"
  trusted_cidr: "10.0.0.0/24"
  docker_addr: ":7001"
  file_addr: ":7002"

manager:
  endpoint: "https://manager.example.com/api/v1"
  enrollment_secret: "<与 manager 相同的 base64 密钥>"
  ca_bundle: ""
  skip_verify: false
```

`advertise_host` 必须是 manager 能访问到该节点的地址；agent 会把 `https://<advertise_host>:7001` 和 `https://<advertise_host>:7002` 上报给 manager。

## 3. 防火墙

| 端口 | 协议 | 允许来源 |
|------|------|----------|
| `7001` (RUNTIME_AGENT_GRPC_PORT) | TCP | 仅 manager 服务器出口网段 |
| `7002` (RUNTIME_AGENT_HTTP_PORT) | TCP | 仅 manager 服务器出口网段 |

这两个端口不要对公网开放，仅允许 manager 出口 IP 访问。

## 4. 状态检查 / 验证

```bash
# 查看容器状态
docker compose ps

# 查看注册日志
docker compose logs -f --tail=100 oc-runtime-agent
```

注册成功后在 manager 后台验证：

1. 打开「运行节点」，应看到新节点自动出现。
2. 节点状态为「在线」。
3. 探测列出现最近成功时间（manager 能连通 7001/7002 TLS 端口）。

## 5. 常见问题

- **`401 enrollment secret 无效`**：manager 与 agent 的 `enrollment_secret` 不一致，检查两侧配置是否相同。
- **探测异常 / 节点离线**：manager 到 agent 的 7001/7002 端口不可达，检查防火墙是否放行 manager 出口 IP；或 `trusted_cidr` 未覆盖 manager 出口 IP 导致 403。
- **agent token 不可用**：节点尚未 enroll 成功，或 state 目录数据丢失；重启 agent 触发重新注册。
- **healthcheck 失败**：检查 Docker socket 挂载是否正常（`ls -l /var/run/docker.sock`），以及 state 目录是否可写。
