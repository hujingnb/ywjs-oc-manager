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

## 跨机部署（多 runtime node）

manager 单机 + 多 runtime node 是 v1.0 的标准拓扑。每个新节点需要做：

1. **节点 OS 准备**：
   - Linux + Docker ≥ 24，内核 ≥ 5.10。
   - 开放出口：节点 → manager 7443（TLS 反代后 HTTPS）。
   - 开放入口：manager → 节点 7001（gRPC）/ 7002（HTTP file API）。
   - 节点不要直接对公网暴露 7001/7002，只接受 manager 内网 IP。

2. **agent TLS SAN 配置**：
   - agent 启动时自动生成自签 CA 与 server cert。如果节点对外名是
     `node-1.internal`，需要把它写到 `AGENT_TLS_DNS_SAN` 环境变量，
     否则 manager 用域名连节点会因为 SAN mismatch 拒绝。
   - 如果节点 IP 固定，也支持 `AGENT_TLS_IP_SAN=10.0.0.5`。
   - 改 SAN 后必须删 `/var/lib/oc-agent/state/agent-tls*` 让 agent 重新生成证书。

3. **防火墙规则**：
   - 节点 ufw / iptables 只放行 manager 子网到 7001/7002。
   - 节点出口允许到 new-api / 镜像仓库 / DNS。

4. **manager 端注册**：
   - 在 `/runtime-nodes` 页点"注册节点"，填节点名 + bootstrap_token（一次性显示）。
   - 在节点 agent 容器环境变量加 `MANAGER_ADDR=https://manager.internal:7443`
     与 `BOOTSTRAP_TOKEN=...`，启动 agent 后会自动完成 register + heartbeat。
   - manager 端节点状态翻转到 `active` 后即可在该节点跑应用。

5. **跨机演练（v1.0 RC 验收）**：
   - 本机 docker compose 启第二个 agent 容器 `oc-runtime-agent-2`
     （不同 NODE_ID + bootstrap_token），共享同一 docker socket。
   - 跑 onboard 在两节点各创建一个应用，组织级知识库上传后两节点同步状态都到 `synced`。
   - 关一个 agent 容器，sync-status 翻 `failed`、应用 `error`；重启 agent 后状态恢复。

## 备份与升级

- 数据备份与恢复：参见 [`backup.md`](./backup.md)。
- 版本升级与回滚：参见 [`upgrade.md`](./upgrade.md)。

## UAT 准备清单

- 平台管理员账号 + 测试组织 + 至少 2 名测试成员。
- 反馈表模板：`docs/uat/feedback-template.md`（按场景列「现象 / 复现步骤 / 期望 / 截图」）。
- 问题分级规则：阻塞 > 高 > 中 > 低；阻塞与高级别问题进入 release blocker。
- release notes 模板：`docs/uat/release-notes-template.md`。
