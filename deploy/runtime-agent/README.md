# Runtime Agent 生产部署包

本目录部署到每台 Runtime Node。agent 通过宿主 Docker socket 管理 OpenClaw 容器，并主动注册到 manager。

## 启动

```bash
cp .env.example .env
cp config/agent.example.yaml config/agent.yaml
${EDITOR:-vi} .env
${EDITOR:-vi} config/agent.yaml
docker compose up -d
```

## 必改配置

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

```bash
docker compose ps
docker compose logs -f --tail=100 oc-runtime-agent
```
