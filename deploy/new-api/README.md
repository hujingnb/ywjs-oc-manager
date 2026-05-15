# new-api 网关生产部署

> 本运行包部署到 new-api 服务器，提供 new-api 服务 + 自带 PostgreSQL + Redis。
> new-api 是 LLM 网关，负责汇聚多个模型渠道并对 manager 提供统一 API 访问入口。

## 1. 启动

```bash
cp .env.example .env
${EDITOR:-vi} .env
docker compose up -d
```

首次启动后，进入 new-api 后台（`http://<host>:<NEWAPI_PORT>`）完成以下初始化：

1. 使用默认管理员账号 `admin` / `123456` 登录，立即修改密码。
2. 在「渠道」中添加 Ollama 渠道，base URL 填写 Ollama 服务地址（如 `http://<ollama-host>:11434`）。
3. 在「令牌」中为 manager 生成系统访问令牌，填入 manager 的 `config/manager.yaml`。

## 2. 必改配置

### `.env`

| 变量 | 说明 |
|------|------|
| `NEWAPI_IMAGE` | new-api 镜像，aliyun ywjs_public 私有 ACR 镜像（原 `docker.io/calciumion/new-api`）；使用具体 tag 或 `@sha256:` digest（禁用 `latest`） |
| `NEWAPI_POSTGRES_IMAGE` | PostgreSQL 镜像，aliyun ywjs_public 私有 ACR 镜像（原 `docker.io/library/postgres:17-alpine`） |
| `NEWAPI_REDIS_IMAGE` | Redis 镜像，aliyun ywjs_public 私有 ACR 镜像（原 `docker.io/library/redis:7`） |
| `NEWAPI_PORT` | new-api 对外监听端口，默认 `3000` |
| `NEWAPI_NODE_NAME` | 节点标识，按生产节点命名，默认 `new-api-prod-1` |
| `NEWAPI_STREAMING_TIMEOUT` | 流式响应超时（秒），默认 `600` |
| `NEWAPI_POSTGRES_PASSWORD` | PostgreSQL 容器原始密码 |
| `NEWAPI_POSTGRES_DSN_PASSWORD` | 同一密码的 URL 编码形式，拼入 `SQL_DSN` |
| `NEWAPI_REDIS_PASSWORD` | Redis 原始密码 |
| `NEWAPI_REDIS_URL_PASSWORD` | 同一密码的 URL 编码形式，拼入 `REDIS_CONN_STRING` |

密码包含 `@`、`:`、`/` 等 URL 保留字符时，DSN 和 URL 编码变量必须填写编码后的值。

### 对接 manager

manager 的 `config/manager.yaml` 中填写 new-api 地址和令牌：

```yaml
newapi:
  base_url: "https://new-api.example.com"
  admin_token: "<在 new-api 后台生成的令牌>"
hermes:
  llm:
    base_url: "https://new-api.example.com/v1"
```

未配置 HTTPS 反代时可临时使用 `http://<new-api-host>:3000`。

## 3. 防火墙

| 端口 | 协议 | 允许来源 |
|------|------|----------|
| `3000` (NEWAPI_PORT) | TCP | manager 服务器；如需直接访问后台也可开放内网 |

new-api-postgres 和 new-api-redis 不对外暴露端口，仅在 Docker 内部网络通信。

生产建议在 new-api 前面部署 nginx/SLB 反代，终止 TLS 后转发到 3000 端口。

## 4. 状态检查 / 验证

```bash
# 查看所有容器状态
docker compose ps

# 检查 new-api 是否正常响应
curl http://localhost:3000/api/status

# 查看日志
docker compose logs -f --tail=100 new-api
```

## 5. 数据目录

| 路径 | 内容 |
|------|------|
| `./data/postgres` | PostgreSQL 持久化数据 |
| `./data/redis` | Redis AOF 持久化数据 |
| `./data/new-api` | new-api 运行数据（令牌、渠道配置等） |
| `./logs` | new-api 运行日志 |

备份前建议先确认 `docker compose ps` 中所有服务健康。

## 6. 常见问题

- **容器启动后无法登录**：new-api 首次启动会自动初始化 admin 账号，若数据库未就绪可能失败，查看 `docker compose logs new-api` 确认。
- **manager 调用 new-api 返回 401**：检查令牌是否正确，以及令牌在 new-api 后台是否启用。
- **流式响应中断**：适当增大 `NEWAPI_STREAMING_TIMEOUT`，默认 600 秒。
