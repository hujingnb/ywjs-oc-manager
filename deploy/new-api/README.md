# new-api 生产部署包

本目录可独立复制到 new-api 服务器运行，包含 new-api、PostgreSQL、Redis 和本地持久化目录。

## 启动

```bash
cp .env.example .env
${EDITOR:-vi} .env
docker compose up -d
```

首次启动后进入 new-api 后台完成管理员初始化、系统访问令牌生成、Ollama 渠道配置和模型测试。

## 配置要求

`NEWAPI_IMAGE` 生产环境必须使用 `@sha256:` 不可变 digest。SemVer tag 仅可作为发布标签用于查询对应 digest，不应写入生产 `.env`。

`NEWAPI_POSTGRES_PASSWORD` 和 `NEWAPI_REDIS_PASSWORD` 是数据库和 Redis 服务使用的原始密码。
当密码包含 `@`、`:`、`/`、`?`、`#` 等 URL 保留字符时，需将同一密码 URL 编码后分别填入
`NEWAPI_POSTGRES_DSN_PASSWORD` 和 `NEWAPI_REDIS_URL_PASSWORD`，供 new-api 连接串使用。

## 对接 manager

manager 的 `config/manager.yaml` 中：

```yaml
newapi:
  base_url: "https://new-api.example.com"
openclaw:
  llm:
    base_url: "https://new-api.example.com/v1"
```

如果未配置 HTTPS 反代，可临时使用 `http://<new-api-host>:3000` 和
`http://<new-api-host>:3000/v1`。

## 数据

- PostgreSQL：`./data/postgres`
- Redis：`./data/redis`
- new-api 数据：`./data/new-api`
- 日志：`./logs`

备份前建议先确认 `docker compose ps` 中服务健康。
