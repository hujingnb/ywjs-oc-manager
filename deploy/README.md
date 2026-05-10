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

1. 准备 `.env`。这里只放镜像名等 compose 启动参数，应用配置写入 yaml：

   ```env
   OCM_MANAGER_IMAGE=ghcr.io/your-org/openclaw-manager:0.1.0
   OCM_WEB_IMAGE=ghcr.io/your-org/openclaw-manager-web:0.1.0
   ```

2. 准备 YAML 配置文件，见下一节。

3. 准备 TLS 证书放到 `deploy/tls/{fullchain.pem,privkey.pem}`。

4. 启动：

   ```sh
   docker compose -f deploy/docker-compose.prod.yml --env-file .env up -d
   ```

5. 第一次部署需要跑迁移：

   ```sh
   docker compose -f deploy/docker-compose.prod.yml run --rm manager-api ./migrate up
   ```

## YAML 配置文件

manager-api 与 runtime-agent 的所有应用配置都集中在两份 yaml 文件中，环境变量仅保留：

- `OCM_CONFIG`：manager binary（cmd/server / cmd/migrate / cmd/seed-admin）的 yaml 路径，默认 `config/manager.yaml`
- `OC_AGENT_CONFIG`：runtime-agent 的 yaml 路径，默认 `config/agent.yaml`

### 文件命名

- `config/manager.example.yaml`：进 git 的脱敏模板，每字段附中文注释
- `config/agent.example.yaml`：进 git 的脱敏模板
- `config/manager.yaml`：本地实际值，**已加入 .gitignore**，禁止提交
- `config/agent.yaml`：本地实际值，**已加入 .gitignore**，禁止提交

### 首次启动 / 升级流程

```bash
# 1. 复制脱敏模板
cp config/manager.example.yaml config/manager.yaml
cp config/agent.example.yaml config/agent.yaml

# 2. 编辑实际值（密钥 / DSN / token / api_key 等）
${EDITOR:-vi} config/manager.yaml
${EDITOR:-vi} config/agent.yaml

# 3. 启动
docker compose up -d
```

### 从老版本升级

老版本通过 `.env` + 环境变量注入应用配置；本次升级后这些 env 不再被读取。请把 `.env` 中以下 key 的真实值搬到 `config/manager.yaml` 的对应字段，然后从 `.env` 中删除：

- `DATABASE_URL` → `database.url`
- `REDIS_ADDR` / `REDIS_PASSWORD` → `redis.addr` / `redis.password`
- `JWT_ACCESS_SECRET` / `JWT_REFRESH_SECRET` / `CSRF_SECRET` → `auth.*`
- `MASTER_KEY` → `security.master_key`
- `NEWAPI_BASE_URL` / `NEWAPI_ADMIN_TOKEN` / `NEWAPI_ADMIN_USER_ID` → `newapi.*`
- `OPENCLAW_LLM_BASE_URL` / `OPENCLAW_LLM_DEFAULT_PROVIDER` / `OPENCLAW_LLM_DEFAULT_MODEL` → `openclaw.llm.*`
- `OPENCLAW_LLM_OPENAI_API_KEY` → `openclaw.llm.openai_compat.api_key`
- `OCM_KNOWLEDGE_ROOT` → `app.knowledge_root`

`.env` 升级后只保留端口映射 env（`MANAGER_API_PORT` / `RUNTIME_AGENT_GRPC_PORT` 等）。

## Runtime Agent 部署

每个 runtime node 上运行：

```sh
docker run -d --name oc-runtime-agent \
  --restart=always \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /var/lib/oc-agent:/var/lib/oc-agent \
  -v /etc/oc-agent/agent.yaml:/etc/oc-agent/agent.yaml:ro \
  -p 7001:7001 -p 7002:7002 \
  -e OC_AGENT_CONFIG=/etc/oc-agent/agent.yaml \
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

PostgreSQL 由生产基础设施提供，不在 `deploy/docker-compose.prod.yml` 内启动。
备份应在数据库运维入口执行，例如：

```sh
pg_dump "$OCM_DATABASE_URL" | gzip > /backups/ocm-$(date +%Y%m%d).sql.gz
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
   - 开放入口：manager → 节点 7001（Docker TLS proxy）/ 7002（File TLS API）。
   - 节点不要直接对公网暴露 7001/7002，只接受 manager 内网 IP。

2. **agent TLS 证书**：
   - agent 启动时自动生成自签 CA 与 server cert，证书状态写入 `agent.state_dir`。
   - 如果节点 hostname 变化，必须删 `/var/lib/oc-agent/state/agent-tls*` 让 agent 重新生成证书。

3. **防火墙规则**：
   - 节点 ufw / iptables 只放行 manager 子网到 7001/7002。
   - 节点出口允许到 new-api / 镜像仓库 / DNS。

4. **自动注册配置**：
   - 生成 `runtime.enrollment_secret`，写入 manager 和所有 agent。
   - 每台 agent 配置 `agent.name`、`agent.advertise_host`、`manager.endpoint`。
   - 启动 agent 后会自动调用 `/api/v1/agent/enroll`，manager 后台无需创建节点。

5. **跨机演练（v1.0 RC 验收）**：
   - 本机 docker compose 启第二个 agent 容器 `oc-runtime-agent-2`
     （独立 `agent.state_dir`），共享同一 docker socket。
   - 跑 onboard 在两节点各创建一个应用，组织级知识库上传后两节点同步状态都到 `synced`。
   - 关一个 agent 容器，sync-status 翻 `failed`、应用 `error`；重启 agent 后状态恢复。

## 双 agent 同宿主演练（T3 隔离配置）

v1.0.1 修复了 v1.0 GA T4 实测发现的双 agent 共享同一份 `./config/agent.yaml`、
后注册的 token 互相覆盖前注册的 token 这一问题。`deploy/docker-compose.two-agent.yml`
现在给 agent-2 mount 独立 host 目录 `.local/data/agent-2-config:/app/config`，
两份 yaml 互不踩。

步骤：

1. 准备 agent-2 独立 config 目录（compose up 之前必做，否则 agent-2 容器找不到
   `/app/config/agent.yaml` 会 fail-fast）：

   ```bash
   mkdir -p .local/data/agent-2-config
   cp config/agent.example.yaml .local/data/agent-2-config/agent.yaml
   # 至少改：
   # - agent.name / agent.advertise_host：两台节点使用不同展示名与可达地址
   # - manager.endpoint / manager.enrollment_secret：与 manager.yaml 保持一致
   # - agent.docker_addr / file_addr：仍然写容器内 ":7001" / ":7002"
   #   （宿主端口 7003/7004 由 compose ports 映射）
   # - agent.data_root / state_dir：和 agent-1 保持不同路径以防万一
   ```

2. 启动双 agent 栈：

   ```bash
   docker compose -f docker-compose.yml -f deploy/docker-compose.two-agent.yml up -d
   ```

3. 两个 agent 启动后会自动 enroll；30s 内 manager `/runtime-nodes` 列表两个节点的
   `last_heartbeat_at` 应都在持续刷新。

4. 演练自愈：`docker network disconnect oc-manager_default oc-runtime-agent-2`
   等 reconciler grace 阈值（默认 90s）后节点 `status` 翻 `unreachable`，
   `docker network connect ...` 重连后下一个 heartbeat tick 节点自动回 `active`。

更完整的节点部署步骤见 [`docs/runtime-agent-deployment.md`](../docs/runtime-agent-deployment.md)。

## 备份与升级

- 数据备份与恢复：参见 [`backup.md`](./backup.md)。
- 版本升级与回滚：参见 [`upgrade.md`](./upgrade.md)。

## UAT 准备清单

- 平台管理员账号 + 测试组织 + 至少 2 名测试成员。
- 反馈表模板：`docs/uat/feedback-template.md`（按场景列「现象 / 复现步骤 / 期望 / 截图」）。
- 问题分级规则：阻塞 > 高 > 中 > 低；阻塞与高级别问题进入 release blocker。
- release notes 模板：`docs/uat/release-notes-template.md`。
