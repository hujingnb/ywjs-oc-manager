# Runtime Agent 生产部署包

本目录部署到每台 Runtime Node。agent 通过宿主 Docker socket 管理 Hermes 容器，并主动注册到 manager。

## 启动

```bash
cp .env.example .env
cp config/agent.example.yaml config/agent.yaml
${EDITOR:-vi} .env
${EDITOR:-vi} config/agent.yaml
docker compose up -d
```

## 必改配置

- `OC_RUNTIME_AGENT_IMAGE`：生产环境使用 `@sha256:` 摘要固定镜像；发布标签只用于查询对应 digest，不写入 `.env`。
- `agent.name`：节点展示名。
- `agent.advertise_host`：manager 能访问到的节点 IP 或域名。
- `agent.trusted_cidr`：manager 出口网段，例如 `10.0.0.0/24`。
- `manager.endpoint`：manager API 地址，例如 `https://manager.example.com/api/v1`。
- `manager.enrollment_secret`：必须与 manager.yaml 的 `runtime.enrollment_secret` 一致。

## 防火墙

只允许 manager 出口网段访问：

- `7001`：Docker TLS proxy
- `7002`：File API

不要把这两个端口直接暴露到公网。

## 状态检查

容器 healthcheck 会在镜像内执行 `oc-runtime-agent healthcheck`，检查 Docker socket 是否为 Unix socket、注册凭据是否已写入 state 目录，以及 Docker TLS proxy 和 File API 两个本地 `/healthz` 端点是否返回 HTTP 200。

```bash
docker compose ps
docker compose logs -f --tail=100 oc-runtime-agent
```
