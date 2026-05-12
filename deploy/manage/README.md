# Manager 生产部署包

本目录可独立复制到 manager 服务器运行，包含 manager-api、manager-web、nginx、PostgreSQL、Redis 和本地持久化目录。

## 启动

```bash
cp .env.example .env
cp config/manager.example.yaml config/manager.yaml
${EDITOR:-vi} .env
${EDITOR:-vi} config/manager.yaml
mkdir -p tls
# 放入 tls/fullchain.pem 和 tls/privkey.pem
docker compose up -d manager-postgres manager-redis
docker compose run --rm manager-api ./migrate up
docker compose up -d
```

## 必改配置

- `.env`：`OCM_MANAGER_IMAGE`、`OCM_WEB_IMAGE`、数据库密码、Redis 密码、HTTP/HTTPS 端口。
- `config/manager.yaml`：`app.public_base_url`、`auth.cookie_domain`、JWT/CSRF secret、`security.master_key`、`runtime.enrollment_secret`、new-api 地址和 admin token。
- `config/manager.yaml`：`database.url` 必须使用 `manager-postgres:5432`，`redis.addr` 必须使用 `manager-redis:6379`，容器内数据目录保持 `/var/lib/oc-manager/data` 与 `/var/lib/oc-manager/knowledge`。
- `tls/fullchain.pem` / `tls/privkey.pem`：生产 TLS 证书，nginx 固定从 `/etc/nginx/tls/` 读取。

## 镜像

`OCM_MANAGER_IMAGE` 和 `OCM_WEB_IMAGE` 必须固定到不可变 digest，例如：

```env
OCM_MANAGER_IMAGE=ghcr.io/your-org/openclaw-manager@sha256:<digest>
OCM_WEB_IMAGE=ghcr.io/your-org/openclaw-manager-web@sha256:<digest>
```

生产环境不要使用 `latest` 或可变 tag。`openclaw.runtime_image` 也应使用 runtime 镜像 digest，确保各 runtime node 运行的 OpenClaw 容器版本可追溯。

## 密码编码

`MANAGER_POSTGRES_PASSWORD` 是 PostgreSQL 容器接收的原始密码。`MANAGER_POSTGRES_DSN_PASSWORD` 是同一个密码的 URL 编码形式，用于写入 `config/manager.yaml` 的 `database.url`。

当原始密码包含 `@`、`:`、`/`、`?`、`#`、空格等 URL 保留字符时，必须把编码后的值填入 DSN。例如原始密码 `p@ss:word` 对应 DSN 密码 `p%40ss%3Aword`。

`MANAGER_REDIS_PASSWORD` 是 Redis 原始密码，同时写入 `.env` 和 `config/manager.yaml` 的 `redis.password`。

## 健康检查

`manager-api` 的 Compose healthcheck 使用：

```bash
wget -qO- http://localhost:8080/healthz
```

因此 manager-api 镜像需要内置 `wget`，否则容器会因健康检查命令不存在而保持 unhealthy。若未来镜像改用专用 healthcheck binary，应同步调整本 Compose 与 README。

## 运行检查

```bash
docker compose ps
curl -k https://manager.example.com/healthz
```

## 数据

- PostgreSQL：`./data/postgres`
- Redis：`./data/redis`
- manager 数据：`./data/manager`
- 知识库主副本：`./data/knowledge`
- TLS 证书：`./tls`

这些目录都是本地 bind mount；备份和迁移前建议先确认 `docker compose ps` 中服务健康。
