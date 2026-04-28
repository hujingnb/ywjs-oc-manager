# OpenClaw Manager 技术实现设计文档

日期：2026-04-28

关联需求文档：[openclaw-manager-design.md](./openclaw-manager-design.md)

## 修订记录

- 2026-04-29：apps 表新增 `workspace_path` 字段，加部分唯一索引 `unique(owner_user_id) where deleted_at is null`（账号即客户端）；移除 `apps.published_at` 和 `ready` 状态；`wechat_bindings` 重构为 `channel_bindings`（通用渠道抽象，第一版仅微信）；`POST /apps` 路由废弃，应用创建改由 `POST /members` 在事务里隐式触发；新增工作目录浏览/下载 API（`/apps/{appId}/workspace`）；OpenClawAdapter 拆分出独立的 `ChannelAdapter`；新增 `openclaw.system_prompt_template` 与 `openclaw.workspace.*` 配置项；新增 `runtime.channel_plugins` / `runtime.archive_retention_days` 配置项；部分 `wechat_*` job 类型重命名为 `channel_*`；新增 `workspace_archive_cleanup` job。

## 1. 技术目标

OpenClaw Manager 第一版采用单体后端、前后端分离、单机一体化部署的技术路线。

核心目标：

- 用 Go 后端稳定编排 `new-api`、Docker、OpenClaw CLI 三类外部系统。
- 用 PostgreSQL 作为唯一业务状态源，用 Redis 承载运行队列、短期状态和分布式锁。
- 用 Vue 3 管理后台承载组织、成员、应用、预算、微信绑定、运维和统计页面。
- 所有跨系统长流程通过 PostgreSQL job 记录、Redis 队列和 worker 执行，避免 HTTP 请求同步阻塞。
- 通过状态机、幂等 job、补偿流程和审计日志保证故障可恢复、可追踪。
- 技术设计预留 Runtime Node，多节点能力后续扩展，第一版不做调度系统。

## 2. 总体架构

整体架构：

```text
Browser
  |
  | Vue 3 SPA
  v
Go Manager API
  |-- HTTP API
  |-- Worker
  |-- Scheduler
  |-- PostgreSQL store
  |-- Redis runtime queue/cache
  |-- new-api adapter
  |-- Docker runtime adapter
  |-- OpenClaw CLI adapter
  |
  +--> PostgreSQL
  +--> Redis
  +--> new-api
  +--> Docker Engine
          |
          +--> OpenClaw runtime containers
```

部署形态：

- `manager-api + worker + scheduler`：一个 Go 进程。
- `manager-web`：Vue 3 静态资源，可由 Nginx 或 Go 静态文件服务承载。
- `PostgreSQL`：业务库、job 持久记录、审计、可选统计快照。
- `Redis`：manager 运行队列、短期状态、分布式锁；同时供 `new-api` 使用。
- `new-api`：模型网关、账号余额、api_key、token 计费和用量统计。
- `ollama`：本地模型运行，由 `new-api` 或运维侧管理。
- `OpenClaw runtime containers`：每个应用一个容器。

manager 不启动、不管理 `new-api` 和 `ollama`。manager 只调用 `new-api` API，并通过 Docker 创建和管理 OpenClaw 应用容器。

## 3. 技术选型

### 3.1 后端

后端技术栈：

- Go
- Gin
- pgx
- sqlc
- golang-migrate
- go-redis
- Docker Go SDK
- JWT access token + refresh token
- YAML 配置文件

选型理由：

- Go 适合单机部署、系统编排、并发 worker 和 Docker SDK 集成。
- Gin 足够轻量，适合清晰组织 REST API。
- sqlc + pgx 保持 SQL 显式可控，适合状态机、任务领取锁、审计查询和统计聚合。
- golang-migrate 管理数据库迁移，避免隐式 schema 变更。
- go-redis 管理 job 分发、短期状态和互斥锁。
- Docker SDK 比调用 CLI 更适合结构化错误处理、日志、stats 和 exec。

### 3.2 前端

前端技术栈：

- Vue 3
- Vite
- TypeScript
- Naive UI
- Vue Router
- Pinia
- TanStack Query for Vue
- OpenAPI generated TypeScript client

选型理由：

- Naive UI 与 Vue 3 和 TypeScript 契合度高，主题能力、Provider 模型和组件 API 更现代。
- TanStack Query 负责服务端数据缓存、轮询、刷新、重试和 loading/error 状态。
- Pinia 只存登录态、用户、权限菜单和少量 UI 状态，避免远程数据散落在 store。
- OpenAPI 生成 client，保证前后端接口契约稳定。

### 3.3 数据库与任务队列

第一版引入 PostgreSQL 和 Redis。

PostgreSQL 用途：

- 业务数据。
- 状态机。
- 审计日志。
- refresh token。
- job 持久记录。
- 可选 usage snapshot 缓存。

Redis 用途：

- job ready queue。
- job 延迟队列或短期调度状态。
- 微信二维码、容器状态等短期运行状态缓存。
- 预算检查、容器操作等互斥锁。
- 与 `new-api` 本地开发环境共用 Redis 服务。

异步任务采用 PostgreSQL + Redis 混合模型：

- PostgreSQL `jobs` 表是任务事实记录，保存任务状态、payload、尝试次数和错误。
- Redis queue 保存可执行 job ID，提升 worker 领取效率。
- worker 从 Redis 取 job ID 后，仍然在 PostgreSQL 中锁定并校验 job 状态。
- 如果 Redis 丢失队列，scheduler/reconciler 根据 PostgreSQL 中的 `pending` job 重新入队。

## 4. 后端模块边界

建议目录结构：

```text
cmd/server/
  main.go

cmd/migrate/
  main.go

internal/api/
  router.go
  middleware/
  handlers/
  dto/

internal/auth/
  jwt.go
  password.go
  refresh_token.go
  csrf.go

internal/config/
  config.go
  loader.go

internal/redis/
  client.go
  queue.go
  lock.go

internal/domain/
  enums.go
  errors.go
  permissions.go
  app_state_machine.go
  job_state_machine.go

internal/service/
  organization_service.go
  member_service.go
  app_service.go
  budget_service.go
  wechat_service.go
  knowledge_service.go
  usage_service.go
  audit_service.go

internal/store/
  queries/
  sqlc/
  tx.go

internal/worker/
  worker.go
  handlers/

internal/scheduler/
  scheduler.go

internal/integrations/newapi/
  client.go
  types.go

internal/integrations/runtime/
  runtime.go
  docker/

internal/integrations/openclaw/
  adapter.go
  parser.go

internal/files/
  app_dirs.go
  uploads.go

migrations/
  000001_init.up.sql
  000001_init.down.sql
```

分层原则：

- handler 只处理 HTTP、DTO、认证上下文和响应，不直接调用 Docker、OpenClaw 或 `new-api`。
- service 负责权限、事务、状态机和业务决策。
- worker 执行外部副作用，并把结果回写数据库。
- adapter 层封装外部系统细节，不写组织、成员、预算等业务判断。
- store 层由 sqlc 生成查询，复杂事务由 service 或 store transaction helper 组织。

## 5. 数据库设计

主键建议使用 UUID。所有业务表保留 `created_at`、`updated_at`。软删除表保留 `deleted_at`。点数和额度使用整数 `credit_amount`，避免浮点误差。

### 5.1 users

```text
id uuid primary key
org_id uuid null references organizations(id)
username text not null unique
password_hash text not null
display_name text not null
role text not null
status text not null
last_login_at timestamptz null
created_at timestamptz not null
updated_at timestamptz not null
```

角色：

- `platform_admin`
- `org_admin`
- `org_member`

状态：

- `active`
- `disabled`

### 5.2 organizations

```text
id uuid primary key
name text not null unique
status text not null
contact_name text null
contact_phone text null
remark text null
newapi_user_id text null
budget_policy text not null
credit_warning_threshold integer null
created_at timestamptz not null
updated_at timestamptz not null
deleted_at timestamptz null
```

预算策略：

- `warn_only`
- `auto_disable_keys`

### 5.3 organization_personas

```text
id uuid primary key
org_id uuid not null references organizations(id)
system_prompt text not null
conversation_rules text null
forbidden_rules text null
reply_style text null
allow_member_override boolean not null
version integer not null
created_by uuid not null references users(id)
created_at timestamptz not null
```

人设采用版本表。当前生效版本取同组织最大 `version`。

### 5.4 member_budgets

```text
id uuid primary key
org_id uuid not null references organizations(id)
user_id uuid not null references users(id)
budget_credit bigint not null
warning_threshold integer not null
used_credit_snapshot bigint not null default 0
limited_at timestamptz null
created_at timestamptz not null
updated_at timestamptz not null
unique(org_id, user_id)
```

`used_credit_snapshot` 不是计费事实来源，只是最近一次预算检查结果。

### 5.5 apps

```text
id uuid primary key
org_id uuid not null references organizations(id)
owner_user_id uuid not null references users(id)
name text not null
description text null
status text not null
persona_mode text not null
app_prompt text null
runtime_node_id uuid null references runtime_nodes(id)
container_id text null
container_name text null
newapi_key_id text null
newapi_key_ciphertext text null
api_key_status text not null
workspace_path text not null
created_at timestamptz not null
updated_at timestamptz not null
deleted_at timestamptz null
```

唯一约束：

- `create unique index apps_owner_active on apps(owner_user_id) where deleted_at is null`：每个未删除成员账号最多对应一个未删除应用（账号即客户端的 schema 兜底）。

应用状态：

- `draft`
- `initializing`
- `binding_waiting`
- `binding_failed`
- `running`
- `stopped`
- `error`
- `budget_limited`
- `deleted`

人设模式：

- `org_inherited`
- `app_override`

api_key 状态：

- `pending`
- `active`
- `disabled`
- `error`

### 5.6 runtime_nodes

```text
id uuid primary key
name text not null unique
kind text not null
docker_endpoint text not null
status text not null
resource_snapshot_json jsonb null
created_at timestamptz not null
updated_at timestamptz not null
```

第一版默认只有一个 `local` 节点。

### 5.7 channel_bindings

```text
id uuid primary key
app_id uuid not null references apps(id)
channel_type text not null
status text not null
bound_identity text null
channel_name text null
metadata_json jsonb null
bound_at timestamptz null
last_online_at timestamptz null
last_error text null
created_at timestamptz not null
updated_at timestamptz not null
```

通用渠道状态机（语义不绑定具体渠道）：

- `unbound`
- `pending_auth`
- `bound`
- `failed`
- `expired`
- `unbound_by_user`

字段说明：

- `channel_type`：渠道类型枚举，第一版仅 `wechat`，未来扩展 `telegram` / `wecom` / `feishu` 等。
- `bound_identity`：该渠道下的账号唯一标识（如微信 wxid）。
- `metadata_json`：渠道特有数据（如微信的 `qr_payload`、`qr_expires_at`），由 ChannelAdapter 写入。

唯一约束：

- `create unique index channel_bindings_app_active on channel_bindings(app_id) where status <> 'deleted'`：第一版每应用 1 个 binding。
- 未来要解锁多渠道时，约束改为 `unique(app_id, channel_type)`，无需迁移数据。

### 5.8 knowledge_files

```text
id uuid primary key
app_id uuid not null references apps(id)
uploaded_by uuid not null references users(id)
original_name text not null
stored_path text not null
mime_type text not null
size_bytes bigint not null
openclaw_file_id text null
status text not null
last_error text null
created_at timestamptz not null
updated_at timestamptz not null
deleted_at timestamptz null
```

文件状态：

- `uploaded`
- `importing`
- `ready`
- `failed`
- `deleted`

### 5.9 recharge_records

```text
id uuid primary key
org_id uuid not null references organizations(id)
operator_id uuid not null references users(id)
credit_amount bigint not null
remark text null
newapi_ref_id text null
status text not null
error_message text null
created_at timestamptz not null
```

状态：

- `succeeded`
- `failed`

### 5.10 usage_snapshots

```text
id uuid primary key
scope_type text not null
scope_id uuid not null
period_start timestamptz not null
period_end timestamptz not null
prompt_tokens bigint not null default 0
completion_tokens bigint not null default 0
total_tokens bigint not null default 0
request_count bigint not null default 0
used_credit bigint not null default 0
source text not null
created_at timestamptz not null
```

这张表是报表缓存，不是计费事实来源。计费事实来源始终是 `new-api`。

### 5.11 jobs

```text
id uuid primary key
type text not null
status text not null
priority integer not null default 0
run_after timestamptz not null
attempts integer not null default 0
max_attempts integer not null default 5
payload_json jsonb not null
locked_by text null
locked_at timestamptz null
last_error text null
created_at timestamptz not null
updated_at timestamptz not null
finished_at timestamptz null
```

job 状态：

- `pending`
- `running`
- `succeeded`
- `failed`
- `canceled`

job 类型：

- `app_initialize`
- `app_start_container`
- `app_stop_container`
- `app_restart_container`
- `app_delete`
- `channel_start_login`
- `channel_check_binding`
- `knowledge_import`
- `knowledge_delete`
- `budget_check_member`
- `runtime_refresh_status`
- `app_health_check`
- `newapi_disable_key`
- `newapi_restore_key`
- `workspace_archive_cleanup`

### 5.12 audit_logs

```text
id uuid primary key
actor_id uuid null references users(id)
actor_role text not null
org_id uuid null references organizations(id)
target_type text not null
target_id text not null
action text not null
result text not null
error_message text null
ip_address inet null
metadata_json jsonb null
created_at timestamptz not null
```

审计日志不可通过普通业务 API 修改或删除。

### 5.13 refresh_tokens

```text
id uuid primary key
user_id uuid not null references users(id)
token_hash text not null
expires_at timestamptz not null
revoked_at timestamptz null
created_at timestamptz not null
```

### 5.14 索引建议

```text
users(org_id, role, status)
organizations(status, name)
apps(org_id, owner_user_id, status)
apps(newapi_key_id)
channel_bindings(app_id, channel_type, status)
knowledge_files(app_id, status)
jobs(status, run_after, priority)
audit_logs(org_id, created_at)
recharge_records(org_id, created_at)
refresh_tokens(user_id, expires_at)
usage_snapshots(scope_type, scope_id, period_start, period_end)
```

## 6. API 设计

API 前缀为 `/api/v1`。后端暴露 OpenAPI 3.0。前端通过 OpenAPI generator 生成 TypeScript client。

### 6.1 认证

```text
POST /auth/login
POST /auth/refresh
POST /auth/logout
GET  /auth/me
```

### 6.2 组织

```text
GET    /organizations
POST   /organizations
GET    /organizations/{orgId}
PATCH  /organizations/{orgId}
POST   /organizations/{orgId}/disable
POST   /organizations/{orgId}/enable
POST   /organizations/{orgId}/recharge
GET    /organizations/{orgId}/recharges
```

### 6.3 成员

```text
GET    /members
POST   /members          # 创建成员账号同步创建关联应用并入队 app_initialize
DELETE /members/{memberId}  # 删除账号联动应用软删
GET    /members/{memberId}
PATCH  /members/{memberId}
POST   /members/{memberId}/disable
POST   /members/{memberId}/enable
POST   /members/{memberId}/reset-password
```

`POST /members` 入参（部分）：

```json
{
  "username": "...",
  "display_name": "...",
  "password": "...",
  "role": "org_member",
  "app": {
    "name": "默认与 display_name 同名",
    "description": null,
    "persona_mode": "org_inherited",
    "app_prompt": null,
    "channel_type": "wechat"
  }
}
```

后端流程：

1. 同一事务里创建 `users` + `apps`（`status=draft`）+ `channel_bindings`（`status=unbound`）行。
2. 写复合审计日志（`actor=admin`，target 同时含 `member_id` 和 `app_id`）。
3. 事务提交后入队 `app_initialize` job。
4. 返回 `{ member, app, job_id }`。

事务里任一行写入失败 → 整事务回滚；不会出现"账号建好但应用没有"的中间状态。

### 6.4 AI 人设与预算

```text
GET  /org/persona
PUT  /org/persona

GET  /budgets/members
PUT  /budgets/members/{memberId}
POST /budgets/members/{memberId}/restore-keys
```

### 6.5 应用

```text
GET    /apps
GET    /apps/{appId}
PATCH  /apps/{appId}
POST   /apps/{appId}/initialize       # 失败后重试初始化
POST   /apps/{appId}/start
POST   /apps/{appId}/stop
POST   /apps/{appId}/restart
DELETE /apps/{appId}                   # 仅平台/组织管理员，软删除
GET    /apps/{appId}/logs
GET    /apps/{appId}/runtime
GET    /apps/{appId}/jobs
```

应用创建由 `POST /members` 隐式触发，无独立 `POST /apps`。无 `POST /apps/{appId}/publish`——渠道绑定成功后自动进入 `running`。

### 6.6 渠道绑定

```text
GET  /apps/{appId}/channels                              # 列出可用渠道（v1 仅 wechat）
POST /apps/{appId}/channels/{channelType}/login
GET  /apps/{appId}/channels/{channelType}
POST /apps/{appId}/channels/{channelType}/retry
POST /apps/{appId}/channels/{channelType}/unbind
```

`POST /channels/{channelType}/login` 返回 `AuthChallenge`：

```json
{
  "type": "qr_code",
  "payload": { "qr_image_base64": "...", "raw_qr": "..." },
  "expires_at": "2026-04-29T12:34:56Z"
}
```

第一版 `type` 仅有 `qr_code`。前端按 `type` 选择渲染组件（QR 渲染器、未来 OAuth 跳转、未来 Token 输入框等）。

### 6.7 知识库

```text
GET    /apps/{appId}/knowledge-files
POST   /apps/{appId}/knowledge-files
DELETE /apps/{appId}/knowledge-files/{fileId}
```

### 6.7.1 工作目录

```text
GET /apps/{appId}/workspace?path=/sub/dir
GET /apps/{appId}/workspace/download?path=/sub/dir/file.pdf
GET /apps/{appId}/workspace/archive?path=/sub/dir
```

`GET /workspace` 返回：

```json
{
  "path": "/sub/dir",
  "entries": [
    { "name": "report.pdf", "type": "file", "size": 12345, "modified_at": "2026-04-29T..." },
    { "name": "images",     "type": "dir",                   "modified_at": "2026-04-29T..." }
  ]
}
```

- `download` 返回单文件流式下载，附带 `Content-Disposition: attachment; filename=...`。
- `archive` 流式生成 zip，仅打包 `path` 指定的目录。
- 后端不提供创建目录、上传、删除、重命名接口。

后端 path 校验：

- 拼接 `{data_root}/apps/{app_id}/workspace` + 入参 path。
- `filepath.Clean` 后必须仍以工作目录前缀开头（`strings.HasPrefix` 校验）。
- `os.Lstat` 检查目标，非常规文件（symlink/socket/device）直接 reject。
- 单文件下载和 archive 大小、archive 条目数有 config 上限。
- 所有访问写审计日志（actor、app_id、relPath、action、result）。

### 6.8 统计与审计

```text
GET /usage/apps/{appId}
GET /usage/members/{memberId}
GET /usage/organizations/{orgId}
GET /usage/platform
GET /audit-logs
```

### 6.9 运行节点

```text
GET /runtime-nodes
GET /runtime-nodes/{nodeId}
```

### 6.10 异步接口返回格式

适用于应用初始化、容器启停、应用删除、微信登录、知识库导入、api_key 恢复等接口。

```json
{
  "job_id": "uuid",
  "resource_id": "uuid",
  "status": "pending"
}
```

### 6.11 错误响应

统一错误结构：

```json
{
  "code": "APP_CONTAINER_START_FAILED",
  "message": "容器启动失败",
  "request_id": "uuid",
  "details": {}
}
```

错误码应稳定，中文 `message` 面向前端展示或二次翻译。

## 7. 异步 Job 与状态机

所有跨系统副作用都通过 `jobs` 表和 Redis queue 执行。API 只校验权限和状态，创建 job 后返回。

### 7.1 Job 领取

创建 job 时，service 在同一业务事务中写入 PostgreSQL `jobs` 表。事务提交后，将 job ID 推入 Redis ready queue。worker 从 Redis 领取 job ID，再回到 PostgreSQL 锁定任务行：

```sql
SELECT *
FROM jobs
WHERE id = $1
FOR UPDATE;
```

领取后必须校验：

- `status = pending`
- `run_after <= now()`
- `attempts < max_attempts`

校验通过后更新：

```text
status = running
locked_by = instance_id
locked_at = now()
attempts = attempts + 1
```

执行成功：

```text
status = succeeded
finished_at = now()
```

执行失败：

- 可重试错误：状态回到 `pending`，`run_after` 设置为指数退避后的时间。
- 对可重试错误，worker 将 job ID 写入 Redis 延迟队列，或由 scheduler 到期后重新入 ready queue。
- 不可重试错误：状态为 `failed`，写入 `last_error`，推进资源到错误状态。
- 达到 `max_attempts`：状态为 `failed`，写审计。

Redis 只负责运行时分发，不是任务事实来源。服务启动时必须运行 reconciler：

```sql
SELECT id
FROM jobs
WHERE status = 'pending'
  AND run_after <= now()
ORDER BY priority DESC, created_at ASC
LIMIT $1;
```

reconciler 将这些 job ID 补入 Redis ready queue，保证 Redis 重启后任务可恢复。

### 7.2 Job 幂等要求

每种 job 必须可重复执行：

- 创建 api_key 前检查 `apps.newapi_key_id`。
- 创建容器前检查 `apps.container_id`。
- 删除容器时，容器不存在视为删除成功。
- 禁用 api_key 时，已禁用视为成功。
- 知识库导入前检查文件状态。
- 微信登录重复执行时覆盖旧二维码和错误状态。

### 7.3 应用初始化

`app_initialize` 流程：

1. 校验应用处于 `draft` 或可重试的 `error` 状态。
2. 设置应用状态为 `initializing`。
3. 调用 `new-api` 创建 api_key。
4. 保存 `newapi_key_id` 和加密后的 api_key。
5. 创建应用目录（`config/`、`state/`、`knowledge/`、`workspace/`、`logs/`），写 `apps.workspace_path`。
6. 渲染拼接系统 prompt：平台默认模板（注入 `workspace_dir`、`knowledge_dir`、`app_id`、`org_id` 等变量）→ 组织 persona 当前生效版本 → 应用 persona（仅当 `persona_mode = app_override`）。
7. 渲染 OpenClaw 配置文件，写入 `config/`，连同环境变量（`OPENCLAW_WORKSPACE_DIR=/workspace`、`OPENCLAW_KNOWLEDGE_DIR=/knowledge`、应用 ID、组织 ID、api_key、`new-api` base URL、启用渠道插件名）准备就绪。
8. 用 Docker SDK 创建容器（bind mount `config/` `state/` `knowledge/` `workspace/` `logs/` 到容器内对应路径）。
9. 启动容器。
10. 执行健康检查。
11. 设置应用状态为 `binding_waiting`。
12. 不在初始化里自动触发渠道登录；用户在前端点"开始绑定"时由 service 入队 `channel_start_login` job。

### 7.4 渠道登录

`channel_start_login` 流程（payload 含 `app_id` 和 `channel_type`）：

1. 校验容器运行中。
2. 通过 ChannelAdapter registry 取出对应 adapter（v1 = `WeChatAdapter`）。
3. adapter 调用 Docker exec 执行 `openclaw channels login --channel <plugin_name>`（v1 plugin 取自 `runtime.channel_plugins.wechat` = `openclaw-weixin`）。
4. 捕获 stdout/stderr。
5. 解析为 `AuthChallenge`（`type=qr_code`、`payload={qr_image_base64,...}`、`expires_at`）。
6. 更新 `channel_bindings.status = pending_auth`，将 challenge 写入 `metadata_json`。
7. 创建后续 `channel_check_binding` job，或由前端轮询状态接口。

第一版通过解析 CLI 输出获取二维码和绑定信息。如果 OpenClaw CLI 输出不稳定，OpenClaw runtime 镜像应提供 wrapper 脚本输出 JSON，作为兼容增强。

### 7.5 应用删除

`app_delete` 流程（在删除成员账号或管理员主动删除应用时触发）：

1. 标记应用软删除流程开始。
2. 停止容器，已停止则跳过。
3. 删除容器，不存在则视为成功。
4. 禁用 `new-api api_key`。
5. 工作目录归档：`mv {data_root}/apps/{app_id}/workspace {data_root}/archived/{app_id}-{timestamp}/workspace`。
6. 应用 `config/` `state/` `knowledge/` `logs/` 默认删除（释放空间），可由 config 改为归档。
7. 设置应用状态为 `deleted`。
8. 写审计日志。

业务记录不物理删除。归档目录由 `workspace_archive_cleanup` job 在 `runtime.archive_retention_days` 期满后物理删除。

### 7.6 预算检查

scheduler 定期创建 `budget_check_member` job。

流程：

1. 查询成员预算和组织策略。
2. 查询该成员名下应用 api_key 用量。
3. 聚合 used credit。
4. 更新 `member_budgets.used_credit_snapshot`。
5. 如果达到阈值，记录预算风险。
6. 如果超额且策略为 `auto_disable_keys`，创建 `newapi_disable_key` job。
7. 将相关应用状态置为 `budget_limited`。

### 7.7 容器操作

启动、停止、重启都创建 job：

- `app_start_container`
- `app_stop_container`
- `app_restart_container`

worker 通过 runtime adapter 操作容器，完成后刷新 app runtime 状态并写审计。

## 8. 外部集成 Adapter

### 8.1 new-api adapter

接口：

```go
type NewAPIClient interface {
    CreateOrBindOrganization(ctx context.Context, input CreateOrBindOrganizationInput) (NewAPIUser, error)
    RechargeUser(ctx context.Context, newapiUserID string, creditAmount int64, remark string) (RechargeResult, error)
    CreateAPIKey(ctx context.Context, newapiUserID string, appName string) (APIKeyResult, error)
    DisableAPIKey(ctx context.Context, apiKeyID string) error
    RestoreAPIKey(ctx context.Context, apiKeyID string) error
    GetAPIKeyUsage(ctx context.Context, apiKeyID string, r UsageRange) (UsageResult, error)
    GetUserUsage(ctx context.Context, newapiUserID string, r UsageRange) (UsageResult, error)
    GetUserBalance(ctx context.Context, newapiUserID string) (BalanceResult, error)
}
```

要求：

- `new-api` 调用失败不能伪造成功。
- 所有外部 ID 保存到本地映射字段。
- api_key 明文只在创建后短暂存在，用于写入容器配置。
- 如果必须保存 api_key，使用服务端主密钥加密。
- 实施前必须验证 `new-api` 管理 API 是否支持账号、充值、api_key、禁用、恢复和用量查询。

### 8.2 runtime adapter

通用接口：

```go
type Runtime interface {
    CreateContainer(ctx context.Context, spec ContainerSpec) (ContainerRef, error)
    StartContainer(ctx context.Context, containerID string) error
    StopContainer(ctx context.Context, containerID string) error
    RestartContainer(ctx context.Context, containerID string) error
    RemoveContainer(ctx context.Context, containerID string) error
    InspectContainer(ctx context.Context, containerID string) (ContainerStatus, error)
    Logs(ctx context.Context, containerID string, opts LogOptions) (LogResult, error)
    Stats(ctx context.Context, containerID string) (ResourceStats, error)
    Exec(ctx context.Context, containerID string, cmd []string, opts ExecOptions) (ExecResult, error)
}
```

第一版实现为 Docker SDK。

容器命名规则：

```text
ocm-{app_id}
```

### 8.3 OpenClaw adapter

OpenClawAdapter 不再持有渠道特定方法。渠道相关操作拆到独立的 ChannelAdapter（见 8.3.1）。

```go
type OpenClawAdapter interface {
    RenderConfig(ctx context.Context, cfg AppRuntimeConfig) (RenderedConfig, error)
    HealthCheck(ctx context.Context, appID uuid.UUID) (HealthResult, error)
    ImportKnowledgeFile(ctx context.Context, appID uuid.UUID, filePath string) (KnowledgeImportResult, error)
    DeleteKnowledgeFile(ctx context.Context, appID uuid.UUID, openclawFileID string) error
}

type AppRuntimeConfig struct {
    AppID         uuid.UUID
    OrgID         uuid.UUID
    APIKey        string
    NewAPIBaseURL string
    PersonaPrompt string  // 平台默认 + 组织 + 应用，已拼接渲染
    WorkspaceDir  string  // 容器内路径，默认 /workspace
    KnowledgeDir  string  // 容器内路径，默认 /knowledge
    ChannelType   string  // 'wechat' 等
    ChannelPlugin string  // OpenClaw 插件标识，如 'openclaw-weixin'
}
```

实现要求：

- `RenderConfig` 拼接平台默认模板 + 组织 persona + 应用 persona，注入 `workspace_dir`、`knowledge_dir` 等变量，输出最终 prompt 和配置文件。
- 配置文件写入应用 `config/` 并挂载进容器。
- 环境变量在创建容器时传入。
- 健康检查优先使用 OpenClaw 提供的命令或 HTTP endpoint；如果没有，至少检查容器运行状态。
- 知识库文件先写入宿主机挂载目录，再 exec OpenClaw 导入命令。

### 8.3.1 ChannelAdapter

渠道适配器抽象登录、状态查询、解绑等渠道操作。第一版只注册微信实现。

```go
type ChannelAdapter interface {
    Type() string
    StartLogin(ctx context.Context, app App) (AuthChallenge, error)
    CheckBinding(ctx context.Context, binding ChannelBinding) (BindingStatus, error)
    Unbind(ctx context.Context, binding ChannelBinding) error
}

type AuthChallenge struct {
    Type      string                 // 'qr_code' | 'oauth_url' | 'bot_token'（v1 仅 qr_code）
    Payload   map[string]any         // type-specific
    ExpiresAt *time.Time
}

type BindingStatus struct {
    Status        string             // pending_auth | bound | failed | expired
    BoundIdentity string
    LastOnlineAt  *time.Time
    LastError     string
}
```

adapter registry：

```go
type Registry struct{ adapters map[string]ChannelAdapter }
func (r *Registry) Get(channelType string) (ChannelAdapter, error)
```

启动时注册：

```go
registry.Register("wechat", NewWeChatAdapter(dockerRuntime, plugin))
```

未来要加 Telegram，只需实现新 adapter 并注册到 registry。

WeChatAdapter 实现要点：

- `StartLogin` 通过 Docker exec 执行 `openclaw channels login --channel openclaw-weixin`，解析 stdout 得到二维码 payload，构造 `AuthChallenge{Type:"qr_code", Payload:{qr_image_base64,...}, ExpiresAt}`。
- `CheckBinding` 通过 OpenClaw 状态查询命令获取当前绑定状态。
- `Unbind` 调用 OpenClaw 解绑命令并清空 `metadata_json`。

### 8.4 应用目录管理

目录结构：

```text
{data_root}/apps/{app_id}/
  config/      # 容器内 /config，OpenClaw 配置文件（含拼接 prompt）
  state/       # 容器内 /state，OpenClaw 运行时状态、渠道凭证
  knowledge/   # 容器内 /knowledge，知识库上传文件
  workspace/   # 容器内 /workspace，OpenClaw 输出的生成文件（PDF/Word 等）
  logs/        # 容器内 /logs，可选日志目录
```

bind mount 同步：所有目录均使用宿主机 bind mount，manager 进程可直接读宿主机文件系统（这是工作目录浏览/下载能直接走文件 I/O 而非 OpenClaw 接口的前提）。

删除应用时：

- 业务记录软删除。
- 容器删除。
- api_key 禁用。
- workspace 移到 `{data_root}/archived/{app_id}-{timestamp}/workspace`，保留 N 天后由 `workspace_archive_cleanup` job 物理删除。
- 其他目录默认删除以释放资源（可由配置改为归档）。
- 归档保留期由 `runtime.archive_retention_days` 配置项控制。

### 8.5 工作目录访问层

manager 提供工作目录浏览/下载服务（独立 service，不通过 OpenClawAdapter）：

```go
type WorkspaceService interface {
    List(ctx context.Context, appID uuid.UUID, relPath string) ([]Entry, error)
    OpenFile(ctx context.Context, appID uuid.UUID, relPath string) (io.ReadCloser, FileInfo, error)
    StreamArchive(ctx context.Context, appID uuid.UUID, relPath string, w io.Writer) error
}

type Entry struct {
    Name       string
    Type       string  // 'file' | 'dir'
    Size       int64
    ModifiedAt time.Time
}
```

安全边界：

- 入参 `relPath` 必须经过 `filepath.Clean` 后位于 `{data_root}/apps/{app_id}/workspace/` 前缀内（用 `filepath.Rel` + 检查无 `..` 前缀）。
- 拒绝跟随符号链接：`os.Lstat`，非常规文件（symlink/socket/device）直接 reject。
- 单文件下载和 archive 有大小上限：`openclaw.workspace.max_download_size`、`openclaw.workspace.max_archive_size`、`openclaw.workspace.max_archive_entries`。
- 所有访问写审计日志（actor、app_id、relPath、action、result）。
- 写入接口不存在；写入只能由 OpenClaw 容器进程通过 bind mount 完成。

## 9. 文件上传与知识库导入

上传流程：

1. API 校验用户权限和应用归属。
2. 校验文件类型和大小。
3. 文件写入 `{data_root}/apps/{app_id}/knowledge/`。
4. 创建 `knowledge_files` 记录，状态为 `uploaded`。
5. 创建 `knowledge_import` job。
6. worker exec OpenClaw 导入命令。
7. 成功后保存 `openclaw_file_id`，状态为 `ready`。
8. 失败后状态为 `failed`，记录错误。

manager 不做 OCR、切分、embedding 或向量库写入。

## 10. 日志与运行监控

应用运行日志：

- 使用 Docker logs 读取最近 N 行。
- manager 不把应用运行日志写入 PostgreSQL。
- 页面支持手动刷新或短周期 polling。

资源监控：

- 使用 Docker stats 查询 CPU、内存、网络、磁盘相关信息。
- `runtime_refresh_status` job 定期刷新应用运行状态。
- `app_health_check` job 定期检查 OpenClaw 健康状态。

审计日志与应用日志分离：

- 应用日志来自 Docker，不落库。
- 审计日志来自业务操作，必须落 PostgreSQL。

## 11. 认证、权限与安全

### 11.1 认证

采用 JWT access token + refresh token。

- Access token 有效期建议 15 分钟。
- Refresh token 有效期建议 7 到 30 天。
- Refresh token 只保存 hash。
- 登出时撤销 refresh token。
- 密码哈希使用 Argon2id。
- 登录失败返回统一错误。

### 11.2 Token 存储

生产环境建议同域部署，使用 HttpOnly Cookie：

- Access token 和 refresh token 通过 HttpOnly Cookie 承载，或 access token 仅内存保存、refresh token 使用 HttpOnly Cookie。
- 写操作启用 CSRF token。
- Cookie 设置 `HttpOnly`、`Secure`、`SameSite=Lax`，按部署域名配置 `Domain`。

跨域部署时必须配置 CORS 白名单和 cookie 策略。

### 11.3 权限

固定三角色：

- `platform_admin`
- `org_admin`
- `org_member`

权限检查：

- middleware 解析身份、角色、组织 ID。
- service 层执行资源级权限校验。
- 禁用组织或禁用用户不能访问 API。

建议封装：

```go
CanAccessOrg(ctx, orgID)
CanAccessMember(ctx, memberID)
CanAccessApp(ctx, appID)
CanOperateAppRuntime(ctx, appID)
CanAccessWorkspace(ctx, appID)
CanCreateMemberWithApp(ctx, orgID)   // 仅组织/平台管理员
CanDeleteApp(ctx, appID)              // 仅组织/平台管理员（成员不可删）
CanManageBudget(ctx, orgID)
```

所有组织级查询必须带 `org_id` 约束，不能依赖前端传参实现隔离。

### 11.4 敏感信息

敏感项：

- 用户密码 hash。
- JWT signing key。
- `new-api` admin token。
- 应用 api_key。
- Docker endpoint 配置。
- OpenClaw runtime 环境变量。

要求：

- api_key 页面不展示明文。
- 数据库存储 api_key 时必须加密。
- 主密钥通过环境变量或部署密钥注入，不写入数据库。
- 日志、错误、审计中不得记录 api_key 明文。

## 12. 前端架构

目录结构：

```text
web/
  src/
    app/
      router.ts
      query-client.ts
      naive-provider.ts
    api/
      generated/
      client.ts
      hooks/
    stores/
      auth.ts
      ui.ts
    layouts/
      AuthLayout.vue
      DashboardLayout.vue
    pages/
      login/
      platform/
      org/
      apps/
      members/
      budgets/
      usage/
      audit/
    components/
      AppStatusTag.vue
      RuntimeStatusTag.vue
      JobProgressPanel.vue
      ConfirmActionModal.vue
      DataTableToolbar.vue
      UploadKnowledgeFile.vue
    domain/
      permissions.ts
      status.ts
      formatters.ts
```

API 使用方式：

- OpenAPI generator 生成原始 TypeScript client。
- `api/client.ts` 注入 base URL、认证刷新和统一错误处理。
- `api/hooks/` 封装业务 hooks。
- 页面不直接调用 generated client。

核心 hooks：

```text
useOrganizationsQuery
useCreateOrganizationMutation
useCreateMemberWithAppMutation       # 创建账号 + 应用
useAppDetailQuery
useInitializeAppMutation             # 失败重试
useAppJobsQuery
useChannelBindingQuery
useStartChannelLoginMutation
useAppRuntimeQuery
useAppLogsQuery
useWorkspaceListQuery
useUsageQuery
```

工作目录下载触发不走 mutation（不是 JSON 响应），通过 `<a href>` 或 `window.open` 直接打开 `/apps/{appId}/workspace/download?path=...`、`/apps/{appId}/workspace/archive?path=...`，由浏览器接管下载流。

TanStack Query 策略：

- 列表页使用筛选条件作为 query key。
- 详情页按 ID 缓存。
- 应用初始化、微信绑定、容器状态启用 polling。
- 日志手动刷新或短周期 polling。
- mutation 成功后 invalidate 相关 query。

Pinia 职责：

- 当前用户。
- 当前角色。
- 当前组织上下文。
- 权限菜单。
- UI 折叠状态。
- 主题偏好。

Pinia 不保存远程列表、详情和统计数据。

路由：

```text
/login

/platform
/platform/organizations
/platform/apps
/platform/usage
/platform/audit
/platform/runtime-nodes

/org
/org/members             # 列表 + 创建账号入口（包含应用配置字段）
/org/members/new         # 创建账号 + 应用页面
/org/budgets
/org/apps
/org/persona
/org/usage
/org/audit

/apps
/apps/:appId
/apps/:appId/channels    # 渠道绑定（v1 仅 wechat）
/apps/:appId/knowledge
/apps/:appId/workspace   # 工作目录浏览
/apps/:appId/runtime
```

`/apps/new` 路由废弃（应用创建只能通过 `/org/members/new`）。

## 13. 配置与部署

### 13.1 YAML 配置

后端使用 YAML 配置文件。默认读取 `config/config.yaml`，支持 `--config /path/to/config.yaml` 指定路径。

示例：

```yaml
app:
  env: dev
  http_addr: ":8080"
  public_base_url: "http://localhost:8080"
  data_root: "/var/lib/oc-manager"

database:
  url: "postgres://ocm:ocm@localhost:5432/ocm?sslmode=disable"

redis:
  addr: "localhost:6379"
  password: "123456"
  db: 0
  key_prefix: "ocm:"

auth:
  cookie_domain: "localhost"
  access_token_ttl: "15m"
  refresh_token_ttl: "720h"
  jwt_access_secret: "${JWT_ACCESS_SECRET}"
  jwt_refresh_secret: "${JWT_REFRESH_SECRET}"
  csrf_secret: "${CSRF_SECRET}"

newapi:
  base_url: "http://localhost:3000"
  admin_token: "${NEWAPI_ADMIN_TOKEN}"

openclaw:
  # 平台默认 AI 指令模板，作为所有应用 prompt 不可覆盖前缀。
  # 渲染时注入 {{app_id}} {{org_id}} {{workspace_dir}} {{knowledge_dir}}。
  system_prompt_template: |
    你是 OpenClaw 智能助手。
    当需要生成文件（PDF / Word / Excel / 图片等）时，必须将文件输出到目录 {{workspace_dir}}，
    按主题或日期建子目录组织，使用清晰可读的文件名。
    用户上传的知识库挂载在 {{knowledge_dir}}，仅供检索引用。
  workspace:
    max_download_size: 524288000     # 单文件 500 MB
    max_archive_size: 2147483648     # archive 总大小 2 GB
    max_archive_entries: 10000       # archive 最多条目

runtime:
  docker_host: "unix:///var/run/docker.sock"
  openclaw_image: "openclaw-runtime:dev"
  default_command: []
  archive_retention_days: 30
  channel_plugins:
    wechat: "openclaw-weixin"

worker:
  enabled: true
  concurrency: 4
  redis_queue: "jobs:ready"
  redis_delayed_queue: "jobs:delayed"

scheduler:
  enabled: true
  budget_check_interval: "10m"
  runtime_refresh_interval: "30s"
  job_reconcile_interval: "30s"
```

配置规则：

- YAML 支持 `${ENV_NAME}` 环境变量展开。
- 敏感字段生产环境通过环境变量注入。
- 启动时做配置校验，缺失关键配置直接失败。
- 不建议把生产密钥明文写入 YAML。

### 13.2 OpenClaw runtime 镜像

OpenClaw runtime 镜像由本地 Dockerfile 提前构建或加载，不在应用创建流程中动态 build。

镜像内置：

- OpenClaw。
- `openclaw-weixin` 插件。
- 插件运行依赖。
- 知识库导入依赖。
- 可选 wrapper 脚本。

配置项：

```text
runtime.openclaw_image: openclaw-runtime:dev
```

### 13.3 数据目录

默认：

```text
/var/lib/oc-manager/
  apps/
    {app_id}/
      config/
      state/
      knowledge/
      workspace/
      logs/
  archived/
    {app_id}-{timestamp}/
      workspace/
  tmp/
```

manager 负责创建目录并设置权限。

所有容器持久化目录必须使用宿主机本地目录 bind mount，不使用 Docker named volume。

本地开发推荐目录：

```text
data/
  manager/
    apps/
    tmp/
  manager-postgres/
  redis/
  new-api/
    data/
    logs/
    postgres/
  ollama/
```

要求：

- manager 应用目录挂载到 OpenClaw 容器。
- PostgreSQL 数据目录挂载到 `./data/...`。
- Redis 如启用 AOF/RDB，数据目录挂载到 `./data/redis`。
- new-api 的 `/data`、`/app/logs`、PostgreSQL 数据目录使用 `./data/new-api/...`。
- Ollama 的 `/root/.ollama` 使用 `./data/ollama`。
- compose 文件中不得定义 named volumes。

### 13.4 数据库迁移

使用 `golang-migrate`：

```text
oc-manager migrate up
oc-manager migrate down
```

生产环境启动时不自动强制迁移数据库。

### 13.5 本地开发

本地开发必须使用 docker compose 统一管理：

- PostgreSQL
- Redis
- new-api
- ollama
- manager-api
- manager-web

Ollama 需要拉取一个小模型用于验证链路，例如：

```text
docker exec ollama ollama pull qwen2.5:0.5b
```

小模型只用于验证 `ollama -> new-api -> OpenClaw/manager` 链路，不作为生产模型建议。

docker compose 持久化约束：

- 只能使用宿主机目录 bind mount。
- 不使用 Docker named volume。
- compose 示例中的 service-level `volumes` 字段只能写 `./data/...:/container/path` 形式的 bind mount。
- 示例中的 `./data/...` 目录应加入 `.gitignore`。

本地开发 compose 服务建议：

```yaml
services:
  manager-postgres:
    image: postgres:15
    container_name: manager-postgres
    environment:
      POSTGRES_USER: ocm
      POSTGRES_PASSWORD: ocm
      POSTGRES_DB: ocm
      TZ: Asia/Shanghai
    volumes:
      - ./data/manager-postgres:/var/lib/postgresql/data
    networks:
      - oc-manager-network

  redis:
    image: redis:latest
    container_name: redis
    command: ["redis-server", "--requirepass", "123456", "--appendonly", "yes"]
    volumes:
      - ./data/redis:/data
    networks:
      - oc-manager-network

  new-api-postgres:
    image: postgres:15
    container_name: new-api-postgres
    environment:
      POSTGRES_USER: root
      POSTGRES_PASSWORD: 123456
      POSTGRES_DB: new-api
      TZ: Asia/Shanghai
    volumes:
      - ./data/new-api/postgres:/var/lib/postgresql/data
    networks:
      - oc-manager-network

  new-api:
    image: calciumion/new-api:latest
    container_name: new-api
    restart: always
    command: --log-dir /app/logs
    ports:
      - "3000:3000"
    volumes:
      - ./data/new-api/data:/data
      - ./data/new-api/logs:/app/logs
    environment:
      SQL_DSN: postgresql://root:123456@new-api-postgres:5432/new-api
      REDIS_CONN_STRING: redis://:123456@redis:6379
      TZ: Asia/Shanghai
      ERROR_LOG_ENABLED: "true"
      BATCH_UPDATE_ENABLED: "true"
      NODE_NAME: new-api-node-1
      STREAMING_TIMEOUT: "600"
    extra_hosts:
      - "host.docker.internal:host-gateway"
    depends_on:
      - redis
      - new-api-postgres
    networks:
      - oc-manager-network

  ollama:
    image: ollama/ollama:latest
    container_name: ollama
    restart: always
    ports:
      - "11434:11434"
    volumes:
      - ./data/ollama:/root/.ollama
    environment:
      OLLAMA_HOST: 0.0.0.0:11434
      OLLAMA_ORIGINS: "*"
      OLLAMA_NUM_PARALLEL: "4"
      OLLAMA_MAX_LOADED_MODELS: "2"
      OLLAMA_KEEP_ALIVE: 24h
      TZ: Asia/Shanghai
    networks:
      - oc-manager-network

networks:
  oc-manager-network:
    driver: bridge
```

如果开发机没有 GPU，Ollama compose 不配置 GPU reservation；只拉小模型验证链路。

OpenClaw runtime 镜像需提前构建：

```text
docker build -t openclaw-runtime:dev ./runtime/openclaw
```

如果仓库不包含 runtime Dockerfile，部署流程必须提前准备该镜像。

## 14. 工程规范

### 14.1 单元测试要求

项目要求完整的单元测试覆盖核心业务逻辑。

必须覆盖：

- domain 状态机。
- 权限判断（含工作目录访问校验、应用创建/删除限制）。
- 预算计算。
- job 重试和幂等逻辑。
- OpenClaw CLI 输出解析。
- ChannelAdapter 接口和 WeChat 实现的二维码解析。
- 平台 prompt 模板渲染、变量注入、三层拼接顺序。
- 工作目录路径安全校验（拒绝 `..` 逃逸、符号链接）。
- 创建成员账号 + 应用复合事务（成功路径 + 任一步骤失败的回滚）。
- `new-api` adapter 错误映射。
- Docker runtime adapter 参数构造。
- service 层关键业务分支。

要求：

- 新增业务逻辑必须配套测试。
- 核心 `domain` 和 `service` 目标覆盖率不低于 80%。
- adapter 对外部系统调用使用 fake/mock。
- 单元测试不得依赖真实 Docker、`new-api`、OpenClaw 或微信扫码。
- 真实 Docker、`new-api`、OpenClaw 测试放到集成测试或手工验收。

### 14.2 中文注释要求

项目要求完整、有效的中文注释。

必须写中文注释的内容：

- 公开类型和公开方法。
- 核心 service。
- 状态机。
- job handler。
- adapter 接口。
- 复杂事务。
- 补偿逻辑。
- 权限边界。
- 外部系统假设。
- OpenAPI DTO 字段说明。

注释原则：

- 注释解释为什么这样做、外部约束是什么、失败时如何恢复。
- 不为简单 getter、普通字段赋值和显而易见的代码写无效注释。
- 禁止用注释重复代码表面含义。
- 涉及安全和敏感信息的逻辑必须说明边界。

### 14.3 代码风格

- Go 代码使用 `gofmt` 和 `go vet`。
- SQL 由 migration 和 sqlc query 管理。
- 前端使用 TypeScript strict 模式。
- 生成代码不手动修改。
- 错误码必须稳定，不能随意改名。

### 14.4 分步验证要求

每个实施步骤完成后必须验证，不能只依赖代码检查或主观判断。

通用要求：

- 后端改动至少运行相关单元测试。
- 数据库改动必须验证 migration up/down 或至少验证 up 可执行。
- API 改动必须验证 OpenAPI schema 生成和关键接口请求。
- worker/job 改动必须验证 job 入队、执行、失败重试和状态回写。
- Docker/OpenClaw 改动必须验证容器创建、启动、exec、日志和目录挂载。
- new-api/ollama 链路改动必须用小模型验证一次最小调用链路。
- 前端页面改动必须运行类型检查和构建。
- 涉及页面、交互、布局或浏览器行为的改动，必须通过 `chrome-devtools` MCP 调用浏览器验证，不只看代码。

页面验证要求：

- 登录页、组织/成员列表、应用向导、微信绑定、运行状态、知识库上传、审计列表等关键页面必须用浏览器打开验证。
- 验证内容包括页面是否能加载、关键按钮是否可点击、表单校验是否生效、异步状态是否刷新、错误提示是否可见。
- 如果页面设计发生变化，需要用浏览器截图或 DOM 快照确认文本不重叠、状态标签清晰、主要流程可操作。

## 15. 测试策略

### 15.1 后端单元测试

覆盖：

- 状态机转换。
- 权限边界。
- 预算阈值和超额策略。
- job backoff、max attempts、幂等。
- OpenClaw 二维码输出解析。
- 平台 prompt 模板渲染、变量注入、三层拼接顺序。
- 工作目录路径安全校验和大小上限。
- 创建账号 + 应用复合事务（成功 + 回滚）。
- `new-api` 错误响应映射。
- 配置加载和环境变量展开。
- api_key 加密和脱敏。

### 15.2 后端集成测试

覆盖：

- PostgreSQL migration。
- sqlc query。
- PostgreSQL job 记录与 Redis queue 协同。
- Redis queue 入队、出队、延迟重试和 reconciler 补偿。
- 应用初始化事务和补偿。
- refresh token 生命周期。
- 审计日志写入。
- 文件上传路径和权限。

### 15.3 外部系统测试

- Docker SDK 使用本地 Docker integration test 或 test container。
- `new-api` adapter 使用 fake HTTP server。
- OpenClaw adapter 使用 fake runtime exec 输出。
- ChannelAdapter 注册和分发使用 fake adapter 验证 routing。
- 真实渠道认证（如微信扫码）不进入自动化测试；只测试二维码解析、失败状态和重试流程。

### 15.4 前端测试

覆盖：

- Query hooks 的 loading、error、success。
- 权限菜单和路由守卫。
- 创建成员 + 应用表单（管理员视角）。
- 渠道绑定登录组件按 `AuthChallenge.type` 渲染分支。
- 工作目录文件浏览器（面包屑、目录跳转、下载触发）。
- 状态标签和操作按钮可见性（成员看不到删除/创建入口）。
- 文件上传错误展示。

关键 E2E 场景：

- 登录。
- 创建组织。
- 组织管理员创建成员账号同步创建应用，跳转到应用详情等待初始化完成。
- 触发渠道绑定（v1 微信）。
- 查看容器状态。
- 浏览和下载工作目录文件。
- 删除成员账号联动应用软删。

### 15.5 契约测试

因为前端依赖 OpenAPI 生成 client：

- CI 生成 OpenAPI schema。
- 检查 schema 能生成 TypeScript client。
- 前端类型检查必须通过。
- 关键 API response DTO 做 schema 测试。

## 16. 主要技术风险与应对

### 16.1 new-api 管理 API 能力不完整

风险：

- 账号创建、充值、api_key 创建/禁用/恢复、用量查询能力可能与需求不完全一致。

应对：

- 实施前做 API spike。
- adapter 保留人工绑定外部账号或保存外部 ID 的替代路径。
- 技术文档和实施计划中把 `new-api` 能力验证作为前置任务。

### 16.2 OpenClaw CLI 输出不稳定

风险：

- 渠道认证（如微信二维码）和绑定状态依赖 stdout/stderr 解析。

应对：

- 独立实现解析器并完整单元测试。
- 如果输出不稳定，在 runtime 镜像内增加 wrapper 脚本输出 JSON。

### 16.3 跨系统状态不一致

风险：

- api_key 创建成功但容器失败。
- 容器删除成功但 api_key 禁用失败。
- 渠道绑定流程中断。

应对：

- 所有长流程 job 化。
- job 幂等。
- 外部动作可重试。
- 失败状态可见。
- 审计日志可追踪。

### 16.4 Docker socket 权限过高

风险：

- manager 访问 Docker socket 等同拥有较高主机权限。

应对：

- 第一版限定私有化单机部署。
- 限制 manager 主机访问面。
- 后续多节点时改为受控 runtime agent，不直接暴露 Docker socket。

### 16.5 api_key 泄露

风险：

- api_key 泄露会导致模型调用滥用。

应对：

- 页面不展示明文。
- 数据库加密保存。
- 日志和审计脱敏。
- 容器配置文件权限收紧。

### 16.6 Redis 队列与 PostgreSQL 状态不一致

风险：

- Redis 中存在 job ID，但 PostgreSQL job 已取消、失败或不存在。
- Redis 重启后 ready queue 丢失，pending job 未被 worker 执行。

应对：

- PostgreSQL `jobs` 表是任务事实来源。
- worker 取到 Redis job ID 后必须回查并锁定 PostgreSQL job。
- 启动和周期性 reconciler 根据 PostgreSQL pending job 补队列。
- Redis 只保存运行分发状态，不作为最终状态来源。

### 16.7 任务堆积

风险：

- 外部系统慢或不可用导致 jobs 表积压。

应对：

- job priority。
- max attempts。
- 指数退避。
- 后台任务列表和失败原因展示。
- 管理员可重试失败任务。

### 16.8 工作目录路径越权

风险：

- 用户构造恶意 path 参数（`../../`、URL encoded `%2e%2e`、符号链接）越权读取宿主机文件。

应对：

- 后端 `filepath.Clean` 后用 `strings.HasPrefix` 校验仍在工作目录前缀内。
- `os.Lstat` 检查目标，非常规文件（symlink/socket/device）直接 reject。
- 不依赖前端入参语义，所有访问从应用 ID 反推根目录。
- 单元测试覆盖各种逃逸尝试（绝对路径、相对回退、符号链接、URL 编码）。

### 16.9 平台默认 prompt 注入失效

风险：

- 平台默认指令模板渲染失败、变量名拼写错误或拼接顺序错误，导致 OpenClaw 不知道工作目录路径，文件输出到容器内任意位置。

应对：

- 模板渲染走专用函数，单元测试覆盖变量注入和占位符未替换检测。
- 拼接顺序由 OpenClawAdapter 写死，不暴露顺序参数。
- 启动时校验 `openclaw.system_prompt_template` 至少包含 `{{workspace_dir}}`。
- 容器创建时把渲染后的 prompt 写入 `config/`，可在出问题时直接 cat 排查。

## 17. 后续演进

第一版技术边界：

- 单 Go 进程。
- 单 PostgreSQL。
- 单 Redis。
- 单 Docker host。
- 单 OpenClaw runtime 镜像配置。
- 无 WebSocket/SSE。
- 无多节点调度。

后续可演进：

- API 与 worker 分进程部署。
- Runtime Node 远程 agent。
- NATS 承载更复杂的跨节点事件。
- SSE/WebSocket 推送 job 和容器状态。
- OpenAPI client 生成 Query hooks。
- 组织公共知识库。
- 更细粒度 RBAC。
