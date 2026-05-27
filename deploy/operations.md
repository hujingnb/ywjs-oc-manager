# 运维手册

> 备份 / 恢复 / 升级 / 紧急回滚 / 常见故障排查。
> 所有命令以当前 `deploy/` 目录结构和 `config/manager.example.yaml` 字段为准。

## 1. 数据范围

| 数据 | 宿主路径 | 备份优先级 | 备注 |
|---|---|---|---|
| manager 业务库 | `deploy/manage/data/postgres` | 关键 | 含组织 / 成员 / 应用 / 审计 / 任务 / refresh_tokens |
| manager Redis | `deploy/manage/data/redis` | 可选 | scheduler 从 jobs 表重建，丢一个调度周期后自愈 |
| manager 本地数据 | `deploy/manage/data/manager` | 关键 | manager-api 持久化业务数据（`app.data_root`） |
| RAGFlow 数据 | `deploy/ragflow/data` / `deploy/ragflow/logs` | 关键 | dataset/document 元数据、解析任务、MinIO 原文件、Elasticsearch 索引都在独立 RAGFlow 部署包内 |
| agent 节点数据 | `deploy/runtime-agent/data/agent`（各 Runtime Node） | 关键 | 应用 workspace / state / Hermes 历史会话；含 agent-id、agent-token 和 TLS 证书 |
| new-api 库 | `deploy/new-api/data/postgres` | 关键 | new-api 账号、渠道、令牌和用量数据 |
| new-api Redis | `deploy/new-api/data/redis` | 可选 | 丢失按 new-api 自身恢复策略处理 |
| Ollama 模型 | `deploy/ollama/data/ollama` | 视策略 | 可重新拉取的模型可不纳入关键备份 |
| TLS 证书 | `deploy/manage/tls/` | 注意 | 续期前备份，避免证书丢失 |

`MASTER_KEY` **不要写入备份**：与备份分离存储到密钥管理 KMS / 密码本，
否则备份泄露时所有 agent_token 密文都能被解密。

## 2. 备份策略

### manager PostgreSQL

在 manager 服务器上通过容器内 `pg_dump` 执行，避免依赖宿主机安装 PostgreSQL 客户端：

```sh
# manager 服务器：每日 dump，保留 7 天
cd deploy/manage
docker compose exec -T manager-postgres sh -c \
  'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' \
  > /backups/manager-$(date +%Y%m%d).dump
find /backups -name "manager-*.dump" -mtime +7 -delete
```

### new-api PostgreSQL

```sh
# new-api 服务器：每日 dump，保留 7 天
cd deploy/new-api
docker compose exec -T new-api-postgres sh -c \
  'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' \
  > /backups/new-api-$(date +%Y%m%d).dump
find /backups -name "new-api-*.dump" -mtime +7 -delete
```

### RAGFlow 数据与 manager 本地数据

```sh
# RAGFlow 服务器：备份 RAGFlow 数据目录
tar czf /backups/ragflow-$(date +%Y%m%d).tar.gz \
  -C deploy/ragflow data logs

# manager 服务器：备份 manager 本地数据（app.data_root 对应挂载点）
tar czf /backups/manager-data-$(date +%Y%m%d).tar.gz \
  -C deploy/manage/data manager
```

### runtime-agent 节点数据

每个 Runtime Node 上单独执行，包含 agent-id / agent-token / TLS 证书和应用数据：

```sh
# 每台 Runtime Node：备份 agent 数据
tar czf /backups/runtime-agent-$(hostname)-$(date +%Y%m%d).tar.gz \
  -C deploy/runtime-agent/data agent
```

## 3. 恢复演练步骤

### 3.1 恢复 manager PostgreSQL

```sh
# 停 manager-api，避免写入冲突
cd deploy/manage
docker compose stop manager-api

# 恢复（破坏性，确认目标库为空或允许覆盖）
docker compose exec -T manager-postgres sh -c \
  'pg_restore --clean --if-exists -U "$POSTGRES_USER" -d "$POSTGRES_DB"' \
  < /backups/manager-20260601.dump

# 恢复后跑迁移，确保 schema 升到当前版本
docker compose run --rm manager-api migrate up

# 重新启动服务
docker compose up -d
```

### 3.2 恢复 RAGFlow 数据 / manager 本地数据

```sh
# 先恢复独立 RAGFlow 数据
cd deploy/ragflow
docker compose stop ragflow ragflow-mysql ragflow-redis ragflow-minio ragflow-es

tar xzf /backups/ragflow-20260601.tar.gz

docker compose up -d

# 再恢复 manager 本地运行数据
cd ../manage
docker compose stop manager-api

tar xzf /backups/manager-data-20260601.tar.gz -C data

docker compose up -d
```

### 3.3 恢复 runtime-agent 节点数据

```sh
cd deploy/runtime-agent
docker compose stop oc-runtime-agent

tar xzf /backups/runtime-agent-<hostname>-20260601.tar.gz -C data

# 若 hostname 发生变化，删除旧 TLS 证书让 agent 重新签发，
# 避免 SAN mismatch 导致 mTLS 握手失败
rm -f data/agent/state/agent-tls*

docker compose up -d
```

### 3.4 验收清单

恢复完成后依次检查：

- `curl https://<manager-host>/healthz` → HTTP 200
- 平台总览各项计数与备份时一致
- 新建一个测试应用，验证完整 onboard → 容器创建 → 健康检查闭环
- runtime-agent 节点心跳正常（manager 控制台节点状态为 active）

## 4. 升级流程

### 4.1 发布风险分级

普通三镜像 tag 只表达构建时间和源码 commit；Hermes 镜像 tag 额外带
`HERMES_VERSION` 前缀。每次发布必须在 release notes 中标明风险等级：

- **破坏性变更**：含破坏性 API / schema 变更，必须读 release notes 并在 staging 完整演练后再升级。
- **兼容功能**：新功能，向后兼容；可能含 schema 增量迁移（仅 add column / new table，不做破坏性变更）。
- **缺陷修复**：bug fix，不动 schema 也不动 API。

### 4.2 镜像 tag 约定

生产镜像由 Makefile 统一生成可追溯 tag：`manager-api` / `runtime-agent` / `manager-web`
使用 `YYYY-MM-DD-HH-MM-SS-<commit8>`，`oc-manager-hermes` 使用
`<HERMES_VERSION>-YYYY-MM-DD-HH-MM-SS-<commit8>`，例如
`v2026.5.16-2026-05-21-12-00-00-be70e40a`。
该规则只覆盖本仓库发布的四个镜像，外部基础镜像和依赖镜像不在此规则内。
**生产禁止使用 `:latest`、分支 tag 或版本族 tag**（例如 `:2`、`:2.1`、`:stable`），
因为它们会随上游推送悄悄变更，破坏重启 / 扩容 / 回滚的可复现性。
进一步推荐把镜像引用固定到内容寻址的 `@sha256:<digest>`，
前提是发布时同步记录 digest 并写入 `.env` 或运行配置。

### 4.3 升级前检查

1. 拉最新 release notes，确认变更范围和迁移要求。
2. **先备份**：manager PostgreSQL dump 必做，RAGFlow 数据 tar 必做；agent 节点数据 tar 视改动量决定。
3. 若本次从本地知识库主副本切换到 RAGFlow，先导出旧知识库目录；迁移不会自动导入旧文件，升级后需通过知识库页面重新上传。
4. 在 staging 完整演练：拉新镜像 → 跑迁移 → 启动 → 跑 smoke 测试。
5. 准备回滚计划：记录当前生产环境各服务的 digest，备用。

### 4.4 滚动升级次序

升级时先升依赖底层的服务，再升上层，最后升 agent，保证 manager 升级期间 agent 仍可正常运行：

**Step 1：升级 manager**

```sh
cd deploy/manage
# 在 .env 中把 OCM_MANAGER_IMAGE / OCM_WEB_IMAGE 更新为 Makefile 生成的 tag 或 @sha256:<digest>
${EDITOR:-vi} .env
docker compose pull manager-api manager-web
docker compose run --rm manager-api migrate up
docker compose up -d manager-api manager-web manager-nginx
```

迁移失败时立即停止，按 release notes 走数据修复流程；不要在未确认数据兼容性的情况下继续。

**Step 2：逐节点升级 runtime-agent**

每台 Runtime Node 依次升级，避免同时下线所有节点：

```sh
# 每台 Runtime Node
cd deploy/runtime-agent
# 在 .env 中把 OC_RUNTIME_AGENT_IMAGE 更新为 Makefile 生成的 tag 或 @sha256:<digest>
${EDITOR:-vi} .env
docker compose pull oc-runtime-agent
docker compose up -d oc-runtime-agent
```

agent 重启期间 manager 调 inspect / stats 会短暂失败，UI 上显示「资源指标尚未采集」，正常现象。

**Step 3：验收**（同 3.4 验收清单）

## 5. 紧急回滚

### 5.1 镜像回滚

把 `.env` 改回升级前记录的 digest，重启对应服务：

```sh
# 回滚 manager
cd deploy/manage
${EDITOR:-vi} .env   # 恢复 OCM_MANAGER_IMAGE / OCM_WEB_IMAGE 到旧 digest
docker compose up -d manager-api manager-web manager-nginx

# 回滚 runtime-agent（逐节点）
cd deploy/runtime-agent
${EDITOR:-vi} .env   # 恢复 OC_RUNTIME_AGENT_IMAGE 到旧 digest
docker compose up -d oc-runtime-agent
```

### 5.2 schema 回滚边界

`internal/migrations/` 目录下包含各版本的 up / down SQL。
仅新增 schema 的兼容迁移只做 add column / new table，不做 drop / alter type，
直接回滚镜像后旧代码忽略新列，通常无兼容问题。

若某次破坏性迁移删除了列或改变了类型，无法通过单纯的镜像回滚还原数据；
此时必须先从 pg_dump 备份恢复数据库，再回滚镜像。

### 5.3 回滚检查清单

- [ ] manager-api 健康检查 `GET /healthz` 返回 200
- [ ] agent 节点心跳正常，控制台节点状态为 active
- [ ] 平台总览各项计数与回滚前一致
- [ ] 测试应用 onboard 流程成功

## 6. 常见故障排查

### 6.1 manager 层

**健康检查返回非 200 或容器 restart loop**

```sh
cd deploy/manage
docker compose logs --tail=100 manager-api
```

常见原因：
- `config/manager.yaml` 配置项缺失或值不合法（启动时 fail-fast，日志有 `FATAL` 行）。
- PostgreSQL / Redis 未就绪：检查 `docker compose ps` 中 manager-postgres / manager-redis 的健康状态。
- `app.data_root` 挂载路径不存在，导致启动失败。
- `ragflow.base_url` 或 `ragflow.api_key` 只配置了一项；二者必须同时填写或同时留空。

**manager 调 new-api 返回 401**

检查 `config/manager.yaml` 中 `newapi.admin_token` 是否为 new-api「个人设置 → 安全设置 → 系统访问令牌」生成的令牌（非 `sk-` 开头的推理 token）；同时确认 `newapi.admin_user_id` 与 new-api 后台账号 ID 一致。

**数据库迁移失败或卡住**

```sh
cd deploy/manage
# 查看迁移日志
docker compose run --rm manager-api migrate up 2>&1
# 检查是否有持锁会话
docker compose exec manager-postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
  -c "SELECT pid, state, query, wait_event_type, wait_event FROM pg_stat_activity WHERE state != 'idle';"
```

持锁时优先 kill idle in transaction 会话，再重跑迁移。

### 6.2 runtime-agent 层

**enroll 失败（agent 启动后未出现在 manager 节点列表）**

```sh
cd deploy/runtime-agent
docker compose logs --tail=100 oc-runtime-agent
```

常见原因：
- `config/agent.yaml` 中 `runtime.enrollment_secret` 与 manager 端不一致。
- agent 无法访问 manager HTTP 地址：检查防火墙和 DNS 解析。
- TLS 证书初始化失败：查看日志中 `tls` 相关错误，确认 `data/agent/state/` 目录权限正常。

**心跳超时 / 节点进入 degraded**

manager 以 90s 为阈值判定节点离线。若节点网络正常但仍显示 degraded，检查：
- agent 进程是否存活：`docker compose ps oc-runtime-agent`
- manager 端探测日志：`docker compose logs manager-api | grep probe`
- 节点防火墙是否放行 manager 出口网段访问 `7001` / `7002`

更多 agent 排查细节参见 [docs/runtime-agent.md](../docs/runtime-agent.md)。

### 6.3 Hermes 容器层

**容器未启动或反复重启**

在 manager-api 日志中搜索 `hermes` 或 `container`：

```sh
docker compose logs manager-api | grep -i 'hermes\|container'
```

常见原因：
- `hermes.runtime_image` 配置的镜像不存在于 Runtime Node，需先通过 imagesync 分发。
- `hermes.container_networks` 中列出的 Docker network（默认 `oc-manager_default`）在节点上不存在。

**模型调用 Connection error**

manager 将 new-api 访问地址写入节点 `apps/<id>/input/manifest.yaml`，容器启动时由
`oc-entrypoint` 渲染到 `/opt/data/config.yaml` 的 `model.base_url`。若 chat
completions 报连接错误：
1. 确认 `hermes.llm.base_url` 指向 new-api 在 Docker 网络中可访问的地址（如 `http://new-api:3000/v1`）。
2. 确认 Hermes 容器已加入含 new-api 的 Docker 网络（参见 `hermes.container_networks` 配置）。
3. 在节点上检查 `<nodeDataRoot>/apps/<appID>/input/manifest.yaml` 与
   `<nodeDataRoot>/apps/<appID>/data/config.yaml`，确认 input 已刷新且容器已重启渲染。

**知识库在 Hermes 中检索不到**

Hermes 不直接读取本地知识库文件，也不持有 RAGFlow key。它通过镜像内 `oc-kb`
skill 调 manager runtime API，再由 manager 按实例 token 访问当前 app dataset 与所属
org dataset。若对话中检索不到知识库内容：
1. 检查 `deploy/ragflow` 服务是否正常：`cd deploy/ragflow && docker compose ps`。
2. 检查 manager 配置中的 `ragflow.base_url`、`ragflow.api_key` 是否可用。
3. 在 manager 知识库页面确认文档解析状态已完成；失败文件可点“重解析”。
4. 在节点 input 的 `manifest.yaml` 中确认已写入 `knowledge.runtime_base_url` 与 app token。
5. 在 Hermes 容器内执行 `oc-kb search "测试问题"`，确认 runtime API 可访问。

更多 Hermes 容器排查细节参见 [docs/hermes-container.md](../docs/hermes-container.md)。
