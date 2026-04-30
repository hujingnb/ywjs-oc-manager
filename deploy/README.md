# OpenClaw Manager 部署指南

本目录提供生产环境样例配置。本仓库不直接发布镜像，部署前需要先在自有 CI 中构建。

## 镜像构建

```sh
# manager 后端
docker build -t openclaw-manager:0.1.0 .

# 前端静态资源
docker build -t openclaw-manager-web:0.1.0 web/

# runtime agent（部署到每个节点）
docker build -t oc-runtime-agent:0.1.0 -f runtime/agent/Dockerfile runtime/agent/
```

## docker-compose 部署

1. 准备 `.env`：

   ```env
   OCM_MANAGER_IMAGE=ghcr.io/your-org/openclaw-manager:0.1.0
   OCM_WEB_IMAGE=ghcr.io/your-org/openclaw-manager-web:0.1.0
   DATABASE_URL=postgres://ocm:secret@db.internal:5432/ocm?sslmode=require
   REDIS_ADDR=redis.internal:6379
   REDIS_PASSWORD=...
   JWT_ACCESS_SECRET=...   # 32 字节随机
   JWT_REFRESH_SECRET=...  # 32 字节随机
   CSRF_SECRET=...
   MASTER_KEY=$(openssl rand -base64 32)  # AES-256-GCM 根密钥
   NEWAPI_BASE_URL=https://newapi.internal
   NEWAPI_ADMIN_TOKEN=...
   ```

2. 准备 TLS 证书放到 `deploy/tls/{fullchain.pem,privkey.pem}`。

3. 启动：

   ```sh
   docker compose -f deploy/docker-compose.prod.yml --env-file .env up -d
   ```

4. 第一次部署需要跑迁移：

   ```sh
   docker compose -f deploy/docker-compose.prod.yml run --rm manager-api ./migrate up
   ```

## Runtime Agent 部署

每个 runtime node 上运行：

```sh
docker run -d --name oc-runtime-agent \
  --restart=always \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /var/lib/oc-agent:/var/lib/oc-agent \
  -p 7001:7001 -p 7002:7002 \
  -e DATA_ROOT=/var/lib/oc-agent \
  -e AGENT_STATE_DIR=/var/lib/oc-agent/state \
  -e DOCKER_SOCKET=/var/run/docker.sock \
  oc-runtime-agent:0.1.0
```

启动后从 stdout 抓取 `agent-ca-pem-base64: ...` 那一行作为 manager 注册节点时的 CA。

## 安全要点

- `MASTER_KEY` 一旦生成不要轮换：旧数据无法重新解密。
- `JWT_*` 与 `CSRF_SECRET` 至少 32 字节。
- 启动期 manager 会校验 master_key 长度与 system_prompt_template 占位符，缺失立即 fail-fast。
- 所有外部流量经过 nginx 终止 TLS；manager-api 容器只对 manager-internal 网络开放。
- agent 与 manager 之间用自签 CA + bearer token 双向校验，不要把 7001/7002 暴露到公网。

## 备份

PostgreSQL 备份示例（保留 7 天）：

```sh
docker compose -f deploy/docker-compose.prod.yml exec manager-postgres \
  pg_dump -U ocm ocm | gzip > /backups/ocm-$(date +%Y%m%d).sql.gz
find /backups -name "ocm-*.sql.gz" -mtime +7 -delete
```

知识库主副本备份：rsync `manager-knowledge` volume 到副站点。

## 健康检查

- `GET /healthz` → 200 = manager 正常
- `GET /readyz`（C 阶段后续接入）→ 200 = 依赖（DB/Redis）就绪

## 监控接入

后续 task：开启 Prometheus `/metrics`、worker 队列深度告警、节点心跳超时告警。
