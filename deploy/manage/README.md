# manager 服务生产部署

> 本运行包部署到 manager 服务器，提供 manager-api + manager-web + nginx 反代 + PostgreSQL 17 + Redis 7。
> 依赖 new-api 与 ollama 已部署并可从本机访问。

## 1. 启动

```bash
cp .env.example .env
cp config/manager.example.yaml config/manager.yaml
${EDITOR:-vi} .env
${EDITOR:-vi} config/manager.yaml
mkdir -p tls
# 放入 tls/fullchain.pem 和 tls/privkey.pem

# 先启动数据库
docker compose up -d manager-postgres manager-redis

# 首次部署：执行数据库迁移（manager-api 镜像内置 migrate 二进制）
docker compose run --rm manager-api migrate up

# 启动全部服务
docker compose up -d
```

## 2. 必改配置

### `.env`

| 变量 | 说明 |
|------|------|
| `OCM_MANAGER_IMAGE` | manager-api 生产镜像，必须固定 `@sha256:` digest |
| `OCM_WEB_IMAGE` | manager-web 生产镜像，必须固定 `@sha256:` digest |
| `MANAGER_POSTGRES_IMAGE` | PostgreSQL 镜像，固定 digest |
| `MANAGER_REDIS_IMAGE` | Redis 镜像，固定 digest |
| `MANAGER_NGINX_IMAGE` | nginx 镜像，固定 digest |
| `MANAGER_HTTP_PORT` | nginx 对外 HTTP 端口，默认 `80` |
| `MANAGER_HTTPS_PORT` | nginx 对外 HTTPS 端口，默认 `443` |
| `MANAGER_POSTGRES_PASSWORD` | PostgreSQL 容器原始密码 |
| `MANAGER_POSTGRES_DSN_PASSWORD` | 同一密码的 URL 编码形式，写入 `config/manager.yaml` 的 `database.url` |
| `MANAGER_REDIS_PASSWORD` | Redis 密码，同步写入 `config/manager.yaml` 的 `redis.password` |

密码包含 `@`、`:`、`/` 等 URL 保留字符时，`MANAGER_POSTGRES_DSN_PASSWORD` 必须填写编码后的值。

### `config/manager.yaml`

以下字段必须根据实际环境修改：

- `database.url`：格式 `postgresql://<MANAGER_POSTGRES_USER>:<DSN_PASSWORD>@manager-postgres:5432/<MANAGER_POSTGRES_DB>`
- `redis.addr`：填写 `manager-redis:6379`
- `redis.password`：与 `MANAGER_REDIS_PASSWORD` 一致
- `security.master_key`：系统主密钥，生产环境使用 `openssl rand -base64 32` 生成
- `runtime.enrollment_secret`：与每台 runtime-agent 的 `manager.enrollment_secret` 保持一致，使用 `openssl rand -base64 32` 生成
- `newapi.base_url`：new-api 服务地址，例如 `https://new-api.example.com`
- `newapi.admin_token`：在 new-api 后台生成的系统访问令牌
- `hermes.llm.base_url`：new-api OpenAI 兼容接口，例如 `https://new-api.example.com/v1`
- `app.public_base_url`：manager 对外访问地址，用于链接生成和 cookie domain 计算

### TLS 证书

`./tls/fullchain.pem` 和 `./tls/privkey.pem` 由 nginx 挂载到 `/etc/nginx/tls/`，nginx.conf 中的 `ssl_certificate` 路径已固定。

## 3. 防火墙

| 端口 | 协议 | 允许来源 |
|------|------|----------|
| `80` (MANAGER_HTTP_PORT) | TCP | 公网（用于 HTTP→HTTPS 跳转） |
| `443` (MANAGER_HTTPS_PORT) | TCP | 公网 |

manager-postgres、manager-redis、manager-api 不对外暴露端口，仅在 Docker 内部网络通信。

### Docker Socket 安全说明

`manager-api` 挂载宿主机 `/var/run/docker.sock` 用于镜像同步操作。Docker socket 具有 root 等价权限，因此 manager 服务器需视为高信任主机进行加固：限制 SSH 访问、最小化同机其他工作负载、及时更新 Docker 和内核安全补丁，并确保 `OCM_MANAGER_IMAGE` 来自可信构建链且固定 digest。

## 4. 状态检查 / 验证

```bash
# 查看所有容器状态
docker compose ps

# 检查 API 健康端点
curl -k https://manager.example.com/healthz

# 查看日志
docker compose logs -f --tail=100 manager-api
```

`manager-api` 的 Compose healthcheck 使用 `wget -qO- http://localhost:8080/healthz`，镜像内须内置 `wget`；若 healthcheck 持续 unhealthy，先检查数据库连接和配置文件是否正确。

## 5. 数据目录

| 路径 | 内容 |
|------|------|
| `./data/postgres` | PostgreSQL 持久化数据 |
| `./data/redis` | Redis AOF 持久化数据 |
| `./data/manager` | manager-api 运行数据 |
| `./data/knowledge` | 知识库文件主副本 |
| `./tls` | TLS 证书 |

备份和迁移前建议先确认 `docker compose ps` 中所有服务健康。

## 6. 常见问题

- **manager-api 保持 unhealthy**：检查 `manager-postgres` 和 `manager-redis` 是否已 healthy；查看 `docker compose logs manager-api` 确认配置错误信息。
- **首次启动缺少表**：确认已执行迁移命令 `docker compose run --rm manager-api migrate up`。
- **nginx 502**：manager-api healthcheck 未通过时 Compose 不会把 nginx 转发到 api 容器，排查 api 日志。
- **TLS 证书错误**：确认 `./tls/fullchain.pem` 和 `./tls/privkey.pem` 存在且匹配当前域名。
