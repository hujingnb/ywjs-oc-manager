# OpenClaw Manager 技术实现设计文档

日期：2026-04-28

关联需求文档：[openclaw-manager-design.md](./openclaw-manager-design.md)

## 修订记录

- 2026-04-29：apps 表新增 `workspace_path` 字段，加部分唯一索引 `unique(owner_user_id) where deleted_at is null`（账号即客户端）；移除 `apps.published_at` 和 `ready` 状态；`wechat_bindings` 重构为 `channel_bindings`（通用渠道抽象，第一版仅微信）；`POST /apps` 路由废弃，应用创建改由 `POST /members` 在事务里隐式触发；新增工作目录浏览/下载 API（`/apps/{appId}/workspace`）；OpenClawAdapter 拆分出独立的 `ChannelAdapter`；新增 `openclaw.system_prompt_template` 与 `openclaw.workspace.*` 配置项；新增 `runtime.channel_plugins` / `runtime.archive_retention_days` 配置项；部分 `wechat_*` job 类型重命名为 `channel_*`；新增 `workspace_archive_cleanup` job。
- 2026-04-29（第二轮）：删除 `member_budgets`、`knowledge_files`、`usage_snapshots` 三张表；删除 `apps.workspace_path` 与 `apps.status=budget_limited`；删除 `organizations.budget_policy`；`runtime_nodes` 重写为完整 agent 注册模型（bootstrap_token / agent_token / agent_docker_endpoint / agent_file_endpoint / heartbeat 等）；`RuntimeAdapter` 仅保留 agent-backed 实现，删除本地直连 Docker socket 实现；`OpenClawAdapter` 调用全部经由 agent；新增 agent 文件 API 客户端接口；删除 `knowledge_import` / `knowledge_delete` / `budget_check_member` job 类型，新增 `knowledge_sync_node` / `runtime_node_health_reconcile`；删除 `/budgets/*` 与 `/apps/{appId}/knowledge-files` API，新增 `/orgs/{orgId}/knowledge`、`/apps/{appId}/knowledge`、`/runtime-nodes/*`、`/agent/runtime-nodes/{id}/{register,heartbeat}` 路由；YAML 配置加 `security.master_key`、`agent.*`、删除 budget/runtime.docker_host 配置；数据目录上 manager 端只剩组织/应用知识库主副本，所有节点级目录由 agent 维护。

## 1. 技术目标

OpenClaw Manager 第一版采用单体后端、前后端分离、单机一体化部署的技术路线。

核心目标：

- 用 Go 后端稳定编排 `new-api`、Docker、OpenClaw CLI 三类外部系统。
- 用 PostgreSQL 作为唯一业务状态源，用 Redis 承载运行队列、短期状态和分布式锁。
- 用 Vue 3 管理后台承载组织、成员、应用、渠道绑定、知识库、Runtime Node 管理、运维和统计页面。
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
- 容器操作、节点同步、知识库节点推送等互斥锁。
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
  channel_service.go
  knowledge_service.go      # 主副本写入 + 节点同步触发
  workspace_service.go      # 通过 agent 文件 API 代理
  runtime_node_service.go   # 节点 CRUD、注册、心跳
  usage_service.go          # 直查 new-api
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
  adapter.go            # RuntimeAdapter 接口
  agent_backed.go       # 基于 agent endpoint 的 Docker SDK client 实现

internal/integrations/agent/
  file_client.go        # AgentFileClient 接口与 HTTP 实现
  docker_proxy.go       # Docker SDK 用 agent endpoint 的 transport
  endpoints.go          # 注册 / 心跳处理（manager 接收 agent 调用）

internal/integrations/channel/
  adapter.go            # ChannelAdapter 接口
  registry.go
  wechat.go             # 微信渠道实现

internal/integrations/openclaw/
  adapter.go            # 配置渲染、健康检查、知识库导入命令
  prompt.go             # 三层 prompt 拼接逻辑
  parser.go             # CLI 输出解析

internal/files/
  knowledge_master.go    # manager 本地知识库主副本管理
  uploads.go             # 上传校验

migrations/
  000001_init.up.sql
  000001_init.down.sql
```

分层原则：

- handler 只处理 HTTP、DTO、认证上下文和响应，不直接调用 Docker、OpenClaw 或 `new-api`。
- service 负责权限、事务、状态机和业务决策。
- worker 执行外部副作用，并把结果回写数据库。
- adapter 层封装外部系统细节，不写组织、成员、知识库等业务判断。
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
credit_warning_threshold integer null
created_at timestamptz not null
updated_at timestamptz not null
deleted_at timestamptz null
```

`credit_warning_threshold`：组织余额预警阈值（百分比），可选；不再有成员级预算字段或策略。

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

### 5.5 apps

```text
id uuid primary key
org_id uuid not null references organizations(id)
owner_user_id uuid not null references users(id)
runtime_node_id uuid not null references runtime_nodes(id)
name text not null
description text null
status text not null
persona_mode text not null
app_prompt text null
container_id text null
container_name text null
newapi_key_id text null
newapi_key_ciphertext text null
api_key_status text not null
created_at timestamptz not null
updated_at timestamptz not null
deleted_at timestamptz null
```

`runtime_node_id` 改为 not null：第一版必须先注册节点才能创建应用。`workspace_path` 字段已移除——manager 不需要保存路径，节点上的工作目录由 agent 按 `apps/{app_id}/workspace` 自行推算；manager 通过 agent 文件 API 访问。

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
- `deleted`

api_key 是否可用由 `api_key_status` 单独反映（`active` / `disabled` / `error`），不进入主状态机。前端若发现"应用 running 但 api_key disabled"则展示"已禁用"提示。

人设模式：

- `org_inherited`
- `app_override`

api_key 状态：

- `pending`
- `active`
- `disabled`
- `error`

### 5.6 runtime_nodes

每个节点上常驻一个 agent 容器，agent 提供 Docker 代理（HTTP 转发到本机 Docker socket）和文件 API；manager 通过 agent 进行所有节点级操作，没有 `local` 节点直连模式。

```text
id uuid primary key
name text not null unique
status text not null
agent_docker_endpoint text null
agent_file_endpoint text null
agent_tls_ca_cert text null
agent_token_hash text null
bootstrap_token_hash text null
bootstrap_token_expires_at timestamptz null
agent_version text null
heartbeat_interval_seconds integer not null default 30
last_heartbeat_at timestamptz null
resource_snapshot_json jsonb null
metadata_json jsonb null
node_data_root text null
registered_at timestamptz null
created_at timestamptz not null
updated_at timestamptz not null
max_apps integer null
```

字段说明：

- `name`：节点名（如 `node-shanghai-1`），全局唯一。
- `status`：`pending` / `active` / `unreachable` / `disabled`。
- `agent_docker_endpoint`：agent 暴露的 Docker 代理 URL（如 `https://10.0.0.5:7001`），manager 用 Docker SDK 直连此地址；agent 内部转发到本机 Docker socket。
- `agent_file_endpoint`：agent 暴露的文件 API URL（如 `https://10.0.0.5:7002`）。
- `agent_tls_ca_cert`：agent 自签 CA 证书 PEM；manager 用它验证 agent TLS。
- `agent_token_hash`：长期通信令牌 hash，明文 agent 持有；manager 用 `Authorization: Bearer {agent_token}` 认证。
- `bootstrap_token_hash`：一次性注册令牌 hash；agent 注册成功后清空。
- `bootstrap_token_expires_at`：注册窗口（默认 24h）。
- `agent_version`：agent 上报的版本号。
- `heartbeat_interval_seconds`：心跳间隔，约定值。
- `last_heartbeat_at`：最近一次心跳时间；reconciler 据此判定 unreachable。
- `resource_snapshot_json`：agent 上报 CPU/内存/磁盘/容器数。
- `metadata_json`：OS / 内核 / Docker 版本等。
- `node_data_root`：agent 在节点上的数据根目录（如 `/var/lib/oc-agent`），便于排障。
- `registered_at`：首次注册时间。
- `max_apps`：节点最大未删除应用数；NULL 表示不限。`OnboardingService` 自动选节点时按「剩余容量 = max_apps - 当前应用数」过滤；NULL 视为 +∞ 优先级最高。仅平台管理员可改（`PATCH /api/v1/runtime-nodes/:id`）。0 与正数都合法：0 = 显式暂停接收新应用，正数 = 上限。

唯一约束：

- `unique(name)`。

加密说明：`agent_tls_ca_cert` 与 `agent_docker_endpoint` 等明文字段无需加密；`agent_token_hash` 与 `bootstrap_token_hash` 已是 hash；任何额外的 agent 私密配置使用 manager 配置文件中的 `security.master_key` 加密。

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

### 5.8 知识库（不再使用 DB 表）

知识库改为目录即事实来源：

- 组织级：`{data_root}/orgs/{org_id}/knowledge/`（manager 主副本）→ 同步到该组织所有应用所在节点的 `{node_data_root}/orgs/{org_id}/knowledge/`，bind mount 到容器 `/knowledge/org`。
- 应用级：`{data_root}/apps/{app_id}/knowledge/`（manager 主副本）→ 同步到该应用所在节点的 `{node_data_root}/apps/{app_id}/knowledge/`，bind mount 到容器 `/knowledge/app`。

文件元信息（上传者、原始文件名、上传时间、大小）通过 audit_logs 落库，target_type = `org_knowledge` / `app_knowledge`，target_id 编码为 `org:{org_id}:filename` / `app:{app_id}:filename`，metadata_json 携带 `{size, mime, uploader}`。

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

### 5.10 用量统计（不再使用 DB 表）

manager 不缓存用量数据。所有统计查询（应用 / 成员 / 组织 / 平台 维度）每次直查 new-api 对应账号或 api_key 的用量接口。短时间内重复查询可由 manager 进程内做轻量内存缓存（如 5 秒 TTL），属于实现细节，不入 schema。计费事实来源始终是 `new-api`。

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

- `app_initialize`：包含节点目录准备、知识库同步、容器创建启动、健康检查全过程，分步骤幂等
- `app_start_container` / `app_stop_container` / `app_restart_container`：通过 agent Docker 代理操作
- `app_delete`：通过 agent 停止 + 删除容器、归档节点目录、删除 manager 主副本
- `channel_start_login` / `channel_check_binding`
- `knowledge_sync_node`：组织级知识库异步推送到节点，重试到一致
- `runtime_node_health_reconcile`：扫描心跳超时节点，置 `unreachable` 并联动应用状态
- `runtime_refresh_status`：定时刷新应用容器状态（agent 拉取）
- `app_health_check`：定时 OpenClaw 健康检查
- `newapi_disable_key` / `newapi_restore_key`：手动风控触发
- `workspace_archive_cleanup`：定时清理过期归档（agent 自身亦可周期清理）

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
apps(runtime_node_id, status)
apps(newapi_key_id)
channel_bindings(app_id, channel_type, status)
runtime_nodes(status, last_heartbeat_at)
jobs(status, run_after, priority)
audit_logs(org_id, created_at)
audit_logs(target_type, target_id, created_at)
recharge_records(org_id, created_at)
refresh_tokens(user_id, expires_at)
organization_personas(org_id, version DESC)
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

### 6.4 AI 人设

```text
GET  /org/persona
PUT  /org/persona
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

无 DB 表，filesystem 即事实来源。manager 持有主副本，路径 `{data_root}/orgs/{org_id}/knowledge/` 与 `{data_root}/apps/{app_id}/knowledge/`；目录变更同步到对应 runtime node。

```text
# 组织级知识库
GET    /orgs/{orgId}/knowledge?path=…              # 列目录或返回单文件元信息
POST   /orgs/{orgId}/knowledge                     # multipart 上传单文件
DELETE /orgs/{orgId}/knowledge?path=…              # 删除单文件或子目录
GET    /orgs/{orgId}/knowledge/sync-status         # 各节点同步状态（异步推送场景）

# 应用级知识库
GET    /apps/{appId}/knowledge?path=…
POST   /apps/{appId}/knowledge
DELETE /apps/{appId}/knowledge?path=…
```

实现：

- 上传/删除先写 manager 主副本 → 调对应 runtime node 的 agent 文件 API 同步副本：
  - 应用级：节点单一，**同步**调用，全部成功才返回。
  - 组织级：受影响节点可能多个，主副本写完即返回；为每个受影响节点入队 `knowledge_sync_node` job 异步推送，前端通过 `/sync-status` 查节点状态。
- 上传文件元信息（uploader、original_name、size、mime）写 audit_logs，无独立 DB 表。
- 路径校验：`filepath.Clean` 后必须仍以 `{data_root}/orgs/{org_id}/knowledge/` 或 `{data_root}/apps/{app_id}/knowledge/` 为前缀；agent 端再做一层校验。

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

实现：manager **不读取本地文件系统**。manager 收到请求后：

1. 校验权限（应用 owner / 组织管理员 / 平台管理员）。
2. 查 `apps.runtime_node_id` → 取该节点的 `agent_file_endpoint` 与 `agent_token`。
3. 用 Bearer token 调 agent 文件 API：
   ```
   GET  {agent_file_endpoint}/v1/scopes/apps/{app_id}/workspace?path=…
   GET  {agent_file_endpoint}/v1/scopes/apps/{app_id}/workspace/download?path=…
   GET  {agent_file_endpoint}/v1/scopes/apps/{app_id}/workspace/archive?path=…
   ```
4. 流式 proxy 响应给前端。

校验双层：

- manager 侧：`filepath.Clean(path)` 必须不含 `..`，仅允许相对路径，长度合理。
- agent 侧：拼接 scope 根目录后再 `filepath.Clean`，必须仍以 scope 前缀开头；`os.Lstat` 拒绝非常规文件（symlink/socket/device）。
- 单文件下载、archive 大小、archive 条目数上限（manager 配置 + agent 配置双重校验）。
- 所有访问写审计日志（actor、app_id、relPath、action、result，metadata_json 含目标节点）。

### 6.8 统计与审计

```text
GET /usage/apps/{appId}
GET /usage/members/{memberId}
GET /usage/organizations/{orgId}
GET /usage/platform
GET /audit-logs
```

### 6.9 运行节点

平台管理员侧：

```text
GET    /runtime-nodes
POST   /runtime-nodes                                   # 创建节点，返回 bootstrap_token（仅此次可见）
GET    /runtime-nodes/{nodeId}
PATCH  /runtime-nodes/{nodeId}                          # 修改节点元信息
POST   /runtime-nodes/{nodeId}/disable
POST   /runtime-nodes/{nodeId}/enable
POST   /runtime-nodes/{nodeId}/rotate-bootstrap-token   # bootstrap 失败后重生成
DELETE /runtime-nodes/{nodeId}                          # 仅当节点上无未删除应用时允许
```

agent 侧（路径前缀 `/agent`，认证方式特殊）：

```text
POST /agent/runtime-nodes/{nodeId}/register
  Authorization: Bearer {bootstrap_token}
  Body: {
    agent_docker_endpoint, agent_file_endpoint, agent_tls_ca_cert,
    agent_version, os, kernel, docker_version, node_data_root
  }
  → 返回 { agent_token, heartbeat_interval_seconds }

POST /agent/runtime-nodes/{nodeId}/heartbeat
  Authorization: Bearer {agent_token}
  Body: { resource_snapshot, agent_version }
  → 返回 { ack: true }
```

注册成功后 manager：

- 校验 bootstrap_token 未过期且未使用过。
- 验证上报的 agent_tls_ca_cert 格式有效。
- 生成 agent_token，存 hash 入库，明文返回（仅本次响应可见）。
- 清空 bootstrap_token_hash，置 `status=active`，`registered_at=now()`。

心跳：

- agent 每 `heartbeat_interval_seconds` 秒发一次。
- manager 更新 `last_heartbeat_at` 与 `resource_snapshot_json`。
- `runtime_node_health_reconcile` job 周期性扫描，超过 `3 × heartbeat_interval_seconds` 未收到心跳 → `status=unreachable`，并把该节点上 `running` 应用置 `error`。

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

1. 校验应用处于 `draft` 或可重试的 `error` 状态；查 `apps.runtime_node_id` 对应节点必须为 `active`。
2. 设置应用状态为 `initializing`。
3. 调用 `new-api` 创建 api_key，保存 `newapi_key_id` 和用 `master_key` 加密的 api_key 密文。
4. 通过 agent 文件 API 在节点上准备目录：
   - `POST {agent_file_endpoint}/v1/scopes/apps/{app_id}/init` → 节点 agent 创建 `apps/{app_id}/{knowledge,workspace,state,logs}/`
5. 通过 agent 文件 API 推送知识库主副本：
   - 把 `{data_root}/orgs/{org_id}/knowledge/` 打 tar 流推送到 `POST {agent_file_endpoint}/v1/scopes/orgs/{org_id}/knowledge/sync`
   - 把 `{data_root}/apps/{app_id}/knowledge/` 打 tar 流推送到 `POST {agent_file_endpoint}/v1/scopes/apps/{app_id}/knowledge/sync`
6. 渲染拼接系统 prompt：平台默认模板（注入 `workspace_dir=/workspace`、`knowledge_org_dir=/knowledge/org`、`knowledge_app_dir=/knowledge/app`、`app_id`、`org_id`）→ 组织 persona 当前生效版本 → 应用 persona（仅当 `persona_mode = app_override`）。
7. 通过 agent Docker 代理创建容器：用 Docker SDK 连 `agent_docker_endpoint`，注入环境变量（含拼接 prompt、api_key、new-api base URL、渠道插件名、各路径变量），并 bind mount：
   - 节点 `apps/{app_id}/workspace/` → 容器 `/workspace`
   - 节点 `orgs/{org_id}/knowledge/` → 容器 `/knowledge/org`
   - 节点 `apps/{app_id}/knowledge/` → 容器 `/knowledge/app`
   - 节点 `apps/{app_id}/state/` → 容器 `/state`
   - 节点 `apps/{app_id}/logs/` → 容器 `/logs`
8. 通过 agent Docker 代理启动容器。
9. 执行健康检查（exec 命令或 HTTP 探针，经 agent Docker 代理）。
10. 设置应用状态为 `binding_waiting`。
11. 不在初始化里自动触发渠道登录；用户在前端点"开始绑定"时由 service 入队 `channel_start_login` job。

各步骤幂等：节点目录已存在 → skip 创建；api_key 已创建 → skip；容器已存在 → 校验配置一致后 skip。

### 7.4 渠道登录

`channel_start_login` 流程（payload 含 `app_id` 和 `channel_type`）：

1. 校验容器运行中（通过 agent Docker 代理 `inspect`）。
2. 通过 ChannelAdapter registry 取出对应 adapter（v1 = `WeChatAdapter`）。
3. adapter 通过 agent Docker 代理在容器内 exec：`openclaw channels login --channel <plugin_name>`（v1 plugin 取自 `runtime.channel_plugins.wechat` = `openclaw-weixin`）。
4. 捕获 stdout/stderr。
5. 解析为 `AuthChallenge`（`type=qr_code`、`payload={qr_image_base64,...}`、`expires_at`）。
6. 更新 `channel_bindings.status = pending_auth`，将 challenge 写入 `metadata_json`。
7. 创建后续 `channel_check_binding` job，或由前端轮询状态接口。

第一版通过解析 CLI 输出获取二维码和绑定信息。如果 OpenClaw CLI 输出不稳定，OpenClaw runtime 镜像应提供 wrapper 脚本输出 JSON，作为兼容增强。

### 7.5 应用删除

`app_delete` 流程（在删除成员账号或管理员主动删除应用时触发）：

1. 标记应用软删除流程开始。
2. 通过 agent Docker 代理停止容器，已停止则跳过。
3. 通过 agent Docker 代理删除容器，不存在则视为成功。
4. 禁用 `new-api api_key`。
5. 通过 agent 文件 API 调用 `POST {agent_file_endpoint}/v1/scopes/apps/{app_id}/archive` → agent 把节点上 `apps/{app_id}/` 整体 mv 到 `archived/{app_id}-{timestamp}/`（含 workspace/state/knowledge/logs）。
6. 删除 manager 本地主副本 `{data_root}/apps/{app_id}/knowledge/`。
7. 设置应用状态为 `deleted`。
8. 写审计日志。

业务记录不物理删除。节点上的归档目录由 agent 周期性清理（`workspace.archive_retention_days` 配置）或由 manager 的 `workspace_archive_cleanup` job 触发清理。

### 7.6 知识库节点同步

`knowledge_sync_node` job（payload 含 `org_id` 或 `app_id`、`node_id`、`change_type`）：

1. 查 manager 主副本目录的当前状态。
2. 调对应 runtime node 的 agent 文件 API：
   - 全量同步：tar 流推送整个目录到 agent，agent 替换本地副本。
   - 增量：只推送变化文件 / 删除单文件（payload 指定）。
3. 更新 manager 内部"该 (org/app, node) 对应的最近同步时间"，可以用 audit_logs 或专门的内存/Redis 状态。
4. 失败按 `max_attempts` 退避重试。

应用级知识库使用同步调用（API 直接调用 agent，不经过 job），节点单一、失败立即返回 5xx；组织级知识库使用此 job 异步推送到全部受影响节点。

### 7.7 节点健康检查

`runtime_node_health_reconcile` job（每 30s 由 scheduler 触发）：

1. 查询 `runtime_nodes` 中 `status=active` 且 `last_heartbeat_at < now() - 3 × heartbeat_interval_seconds` 的行。
2. 置 `status=unreachable`。
3. 把该节点上 `status=running` 的应用置为 `error`，记录"节点不可达"。
4. 写审计 `runtime_node.heartbeat_timeout`。

agent 心跳恢复时通过 `POST /heartbeat` 自动把节点置回 `active`，应用状态不会自动恢复（管理员手动重试 `app_start_container`）。

### 7.8 容器操作

启动、停止、重启都创建 job：

- `app_start_container`
- `app_stop_container`
- `app_restart_container`

worker 通过 RuntimeAdapter（agent-backed）操作容器，完成后刷新 app runtime 状态并写审计。

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

`RuntimeAdapter` 抽象 Docker 操作，第一版**仅有 agent-backed 实现**——manager 不直接连本地 Docker socket。

```go
type RuntimeAdapter interface {
    CreateContainer(ctx context.Context, nodeID uuid.UUID, spec ContainerSpec) (ContainerRef, error)
    StartContainer(ctx context.Context, nodeID uuid.UUID, containerID string) error
    StopContainer(ctx context.Context, nodeID uuid.UUID, containerID string) error
    RestartContainer(ctx context.Context, nodeID uuid.UUID, containerID string) error
    RemoveContainer(ctx context.Context, nodeID uuid.UUID, containerID string) error
    InspectContainer(ctx context.Context, nodeID uuid.UUID, containerID string) (ContainerStatus, error)
    Logs(ctx context.Context, nodeID uuid.UUID, containerID string, opts LogOptions) (LogResult, error)
    Stats(ctx context.Context, nodeID uuid.UUID, containerID string) (ResourceStats, error)
    Exec(ctx context.Context, nodeID uuid.UUID, containerID string, cmd []string, opts ExecOptions) (ExecResult, error)
}
```

实现要点：

- 所有方法的第一个参数 `nodeID` 决定走哪个节点。
- adapter 内部缓存 `nodeID → DockerClient` 映射（按 `runtime_nodes.agent_docker_endpoint` 创建 Docker SDK client，附带 Bearer Token transport）。
- 节点信息变更时（IP 变化 / 重新 rotate token）失效缓存。
- adapter 不感知"local 还是 remote"——只有"哪个 agent endpoint"。

容器命名规则：

```text
ocm-{app_id}
```

### 8.2.1 Agent 文件 API 客户端

```go
type AgentFileClient interface {
    InitAppDirs(ctx context.Context, nodeID uuid.UUID, appID uuid.UUID) error
    SyncOrgKnowledge(ctx context.Context, nodeID uuid.UUID, orgID uuid.UUID, tarStream io.Reader) error
    SyncAppKnowledge(ctx context.Context, nodeID uuid.UUID, appID uuid.UUID, tarStream io.Reader) error
    UploadOrgKnowledgeFile(ctx context.Context, nodeID uuid.UUID, orgID uuid.UUID, relPath string, content io.Reader) error
    UploadAppKnowledgeFile(ctx context.Context, nodeID uuid.UUID, appID uuid.UUID, relPath string, content io.Reader) error
    DeleteOrgKnowledge(ctx context.Context, nodeID uuid.UUID, orgID uuid.UUID, relPath string) error
    DeleteAppKnowledge(ctx context.Context, nodeID uuid.UUID, appID uuid.UUID, relPath string) error
    ListWorkspace(ctx context.Context, nodeID uuid.UUID, appID uuid.UUID, relPath string) ([]Entry, error)
    DownloadWorkspaceFile(ctx context.Context, nodeID uuid.UUID, appID uuid.UUID, relPath string) (io.ReadCloser, FileInfo, error)
    StreamWorkspaceArchive(ctx context.Context, nodeID uuid.UUID, appID uuid.UUID, relPath string, w io.Writer) error
    ArchiveApp(ctx context.Context, nodeID uuid.UUID, appID uuid.UUID) error
    CleanupArchive(ctx context.Context, nodeID uuid.UUID, retentionDays int) error
}
```

实现要点：

- 与 RuntimeAdapter 同源的 client 缓存（按 `nodeID → agent_file_endpoint`）。
- 强制 TLS（用 `agent_tls_ca_cert` 校验）+ Bearer agent_token。
- 流式 IO（避免在 manager 进程缓冲大文件）。
- 同步类操作传入 tar 流；agent 端解压时清空旧目录确保一致。

### 8.2.2 Agent 注册与心跳

manager 提供两个 HTTP 端点（已在 §6.9 列出），实现要点：

- `POST /agent/runtime-nodes/{id}/register`：原子操作，事务内校验并消费 bootstrap_token，返回 agent_token；并发或重放注册视为失败。
- `POST /agent/runtime-nodes/{id}/heartbeat`：仅更新 `last_heartbeat_at`、`resource_snapshot_json`、`agent_version`；如节点 `status=unreachable` 自动改回 `active`，写审计。
- agent_token 生成用 `crypto/rand` 32 字节 base64，hash 用 SHA-256（性能优先，不需 Argon2 因 token 已是高熵）。

agent 进程内主动心跳（v1.0.1 加入）：

- 当 agent yaml 的 `manager.endpoint`、`manager.node_id`、`manager.agent_token` 三字段齐全时，进程启动一个后台 goroutine（`runtime/agent/heartbeat.go`），按 `heartbeat.interval_seconds`（默认 30s，最小 5s）周期 POST `{endpoint}/agent/runtime-nodes/{node_id}/heartbeat`。
- 三字段在节点首次 register 之后由 ops 把响应里的 node_id / agent_token 回填到 yaml；填齐前 agent 不发心跳，仅 INFO 日志说明状态。
- 失败仅 WARN；连续失败到 `heartbeat.failure_log_threshold`（默认 5）打 ERROR，便于 ops 抓告警。请求成功后失败计数清零并打一条恢复 INFO。
- 该机制让节点从 `unreachable` 自动恢复到 `active`，无需 ops 手工干预。

`NodeHealthReconciler` 仍负责把超时未心跳的节点置为 `unreachable`；恢复路径完全由 agent 心跳驱动，这一对推拉机制相互配合形成自愈环。

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

### 8.4 目录管理

manager 本地目录（仅知识库主副本）：

```text
{data_root}/
  orgs/{org_id}/knowledge/    # 组织级知识库主副本
  apps/{app_id}/knowledge/    # 应用级知识库主副本
  tmp/                         # 临时文件
```

manager 不持有任何应用容器配置、状态、工作目录或日志。

节点上目录（agent 维护）：

```text
{node_data_root}/
  orgs/{org_id}/knowledge/         # 同步自 manager
  apps/{app_id}/
    knowledge/                     # 同步自 manager
    workspace/                     # OpenClaw 输出
    state/                         # OpenClaw 运行时状态、渠道凭证
    logs/                          # 可选日志
  archived/{app_id}-{timestamp}/   # 应用软删后的归档目录
```

容器 bind mount（在 agent 节点上）：

| 节点路径 | 容器路径 | 说明 |
|---|---|---|
| `{node_data_root}/orgs/{org_id}/knowledge/` | `/knowledge/org` | 组织级知识库 |
| `{node_data_root}/apps/{app_id}/knowledge/` | `/knowledge/app` | 应用级知识库 |
| `{node_data_root}/apps/{app_id}/workspace/` | `/workspace` | 输出 |
| `{node_data_root}/apps/{app_id}/state/` | `/state` | OpenClaw 状态 |
| `{node_data_root}/apps/{app_id}/logs/` | `/logs` | 日志（可选） |

OpenClaw 配置不挂目录，全部通过环境变量注入（含拼接 prompt）。

删除应用时：

- 业务记录软删除。
- 通过 agent Docker 代理删除容器。
- api_key 禁用。
- 通过 agent 文件 API 把节点上 `apps/{app_id}/` 整体 mv 到 `archived/{app_id}-{timestamp}/`。
- manager 本地 `apps/{app_id}/knowledge/` 目录删除。
- 节点归档目录由 agent 周期清理或 manager `workspace_archive_cleanup` job 触发，期满 `agent.archive_retention_days` 配置后物理删除。

### 8.5 工作目录访问层

manager 提供工作目录浏览/下载服务，所有调用都路由到对应 agent：

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

实现：

- service 查 `apps.runtime_node_id` → 找节点 agent → 调 `AgentFileClient.ListWorkspace` / `DownloadWorkspaceFile` / `StreamWorkspaceArchive`。
- agent 端做 path 沙箱；manager 端再做一层入参校验（`filepath.Clean`、拒绝 `..`、长度限制）。
- 单文件下载和 archive 大小上限：manager 配置 `openclaw.workspace.*` + agent 配置一致校验。
- 所有访问写审计日志（actor、app_id、relPath、action、result，metadata_json 含 node_id）。
- 写入接口不存在；写入只能由 OpenClaw 容器进程通过 bind mount 完成。

## 9. 知识库上传与节点同步

manager 不调用 OpenClaw 的导入接口；OpenClaw 通过容器内 bind mount 直接读取目录。流程仅涉及 manager 主副本写入和节点 agent 副本同步。

### 9.1 应用级上传（同步推送）

1. API 校验用户权限（应用 owner 或组织管理员）和应用归属。
2. 校验文件类型和大小。
3. 文件写入 manager 主副本 `{data_root}/apps/{app_id}/knowledge/{rel_path}`。
4. 查 `apps.runtime_node_id` → 调 `AgentFileClient.UploadAppKnowledgeFile(node, app_id, rel_path, content)`。
5. agent 失败：回滚主副本（删除该文件）→ 返回 5xx。
6. 写审计 `app_knowledge.upload`，metadata 携带 `{size, mime, uploader, node_id, rel_path}`。

删除：对称流程（先删主副本 → 调 agent 删除）。

### 9.2 组织级上传（主副本同步 + 节点异步推送）

1. API 校验用户权限（组织管理员）。
2. 校验文件类型和大小。
3. 文件写入 manager 主副本 `{data_root}/orgs/{org_id}/knowledge/{rel_path}`。
4. 查"该组织未删除应用所在的全部 runtime node"（去重）。
5. 为每个节点入队 `knowledge_sync_node` job（payload: `{org_id, node_id, change_type=upload_file, rel_path}`）。
6. 写审计 `org_knowledge.upload`，metadata 含 `{size, mime, uploader, target_nodes:[...]}`。
7. API 立即返回；前端通过 `/orgs/{orgId}/knowledge/sync-status` 轮询每节点同步状态。

删除：对称流程（删主副本 → 入队 `knowledge_sync_node` 异步推送删除指令）。

### 9.3 容器初始化时的批量同步

`app_initialize` job 推进到 §7.3 步骤 5 时，把 manager 主副本里的 org + app 知识库各打成 tar 流，分别调 `AgentFileClient.SyncOrgKnowledge` 和 `SyncAppKnowledge`，agent 收到后清空本地副本目录并解压（确保完整一致）。

manager 不做 OCR、切分、embedding 或向量库写入；OpenClaw 自行决定如何使用挂载目录中的文件。

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
CanReadOrgKnowledge(ctx, orgID)
CanWriteOrgKnowledge(ctx, orgID)             // 仅组织/平台管理员
CanReadAppKnowledge(ctx, appID)
CanWriteAppKnowledge(ctx, appID)
CanCreateMemberWithApp(ctx, orgID)           // 仅组织/平台管理员
CanDeleteApp(ctx, appID)                      // 仅组织/平台管理员（成员不可删）
CanManageRuntimeNode(ctx, nodeID)            // 仅平台管理员
```

所有组织级查询必须带 `org_id` 约束，不能依赖前端传参实现隔离。

### 11.4 敏感信息

敏感项：

- 用户密码 hash。
- JWT signing key（access + refresh）。
- CSRF secret。
- `new-api` admin token。
- 应用 api_key（明文密文同时存在时密文必须加密）。
- Runtime node bootstrap_token（一次性，使用前明文，使用后清空 hash 入库）。
- Runtime node agent_token（明文 agent 持有，hash 入库）。
- agent_tls_ca_cert（PEM 明文，公钥侧不敏感）。

主密钥要求：

- 使用 `security.master_key`（写在 manager 配置文件 yaml）作为对称密钥（AES-256-GCM）。
- 密钥加载：从配置文件读取 → 校验长度 → 启动时 fail-fast 若缺失或不合法。
- 密钥用途：加密 `apps.newapi_key_ciphertext` 与任何未来需要持久化的敏感字段。
- 密钥轮换：第一版不实现，文档保留扩展方式（双密钥并存、再加密迁移）。

实施要求：

- api_key 页面不展示明文。
- 数据库存储 api_key 时必须加密。
- 日志、错误、审计中不得记录 api_key、bootstrap_token、agent_token 明文（统一通过脱敏函数）。
- 配置文件中的 `master_key` 通过环境变量展开 `${MASTER_KEY}`，不直接写入版本控制；生产部署时必须设置。

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
      runtime-nodes/
      knowledge/
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
/org/members/new         # 创建账号 + 应用页面（含选择 runtime node）
/org/apps
/org/knowledge           # 组织级知识库管理
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

security:
  # 对称密钥（AES-256-GCM，长度 32 字节，base64 编码后写入），加密 api_key 等敏感字段。
  # 必须由部署环境通过 ${MASTER_KEY} 注入，不允许在 yaml 中明文。
  master_key: "${MASTER_KEY}"

newapi:
  base_url: "http://localhost:3000"
  admin_token: "${NEWAPI_ADMIN_TOKEN}"

openclaw:
  # 平台默认 AI 指令模板，作为所有应用 prompt 不可覆盖前缀。
  # 渲染时注入 {{app_id}} {{org_id}} {{workspace_dir}} {{knowledge_org_dir}} {{knowledge_app_dir}}。
  system_prompt_template: |
    你是 OpenClaw 智能助手。
    当需要生成文件（PDF / Word / Excel / 图片等）时，必须将文件输出到目录 {{workspace_dir}}，
    按主题或日期建子目录组织，使用清晰可读的文件名。
    组织级知识库挂载在 {{knowledge_org_dir}}（同组织所有应用共享，仅读），
    应用级知识库挂载在 {{knowledge_app_dir}}（仅本应用，仅读）。
    检索时优先应用级，未找到再查组织级，仅作为信息来源使用。
  workspace:
    max_download_size: 524288000     # 单文件 500 MB
    max_archive_size: 2147483648     # archive 总大小 2 GB
    max_archive_entries: 10000       # archive 最多条目

runtime:
  openclaw_image: "openclaw-runtime:dev"
  default_command: []
  channel_plugins:
    wechat: "openclaw-weixin"

agent:
  # 节点 agent 通信相关参数，manager 侧默认值
  default_heartbeat_interval_seconds: 30
  heartbeat_timeout_multiplier: 3
  bootstrap_token_ttl: "24h"
  archive_retention_days: 30
  http_timeout: "30s"
  large_upload_timeout: "10m"

worker:
  enabled: true
  concurrency: 4
  redis_queue: "jobs:ready"
  redis_delayed_queue: "jobs:delayed"

scheduler:
  enabled: true
  knowledge_sync_interval: "30s"
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

manager 默认：

```text
/var/lib/oc-manager/
  orgs/{org_id}/knowledge/    # 组织级知识库主副本
  apps/{app_id}/knowledge/    # 应用级知识库主副本
  tmp/                         # 临时文件
```

manager 不再保存任何应用容器配置、状态、工作目录或归档目录——所有节点级目录由 agent 在节点上维护。

agent 默认（每个节点）：

```text
/var/lib/oc-agent/
  orgs/{org_id}/knowledge/         # 同步副本
  apps/{app_id}/
    knowledge/                     # 同步副本
    workspace/                     # OpenClaw 输出
    state/                         # OpenClaw 状态
    logs/
  archived/{app_id}-{timestamp}/   # 归档目录
  agent.token                      # agent 持久化 agent_token 明文（仅 agent 容器可读）
```

manager 负责创建目录并设置权限。

所有容器持久化目录必须使用宿主机本地目录 bind mount，不使用 Docker named volume。

本地开发推荐目录：

```text
data/
  manager/
    orgs/                         # 组织级知识库主副本
    apps/                         # 应用级知识库主副本
    tmp/
  manager-postgres/
  redis/
  new-api/
    data/
    logs/
    postgres/
  ollama/
  agent/                          # 本地开发的 agent 数据（如果在同机器跑 agent）
    orgs/
    apps/
    archived/
```

要求：

- manager 知识库主副本目录挂载到 manager 容器。
- agent 数据目录挂载到 agent 容器和宿主机 Docker socket。
- PostgreSQL 数据目录挂载到 `./data/...`。
- Redis 如启用 AOF/RDB，数据目录挂载到 `./data/redis`。
- new-api 的 `/data`、`/app/logs`、PostgreSQL 数据目录使用 `./data/new-api/...`。
- Ollama 的 `/root/.ollama` 使用 `./data/ollama`。
- compose 文件中不得定义 named volumes。
- 本地开发 agent 需要把宿主机 Docker socket bind mount 进 agent 容器（生产部署也是同样模式）。

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

  oc-runtime-agent:
    image: oc-runtime-agent:dev
    container_name: oc-runtime-agent
    restart: always
    ports:
      - "7001:7001"   # Docker 代理
      - "7002:7002"   # 文件 API
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./data/agent:/var/lib/oc-agent
    environment:
      MANAGER_URL: http://manager-api:8080
      NODE_ID: "${AGENT_NODE_ID}"
      BOOTSTRAP_TOKEN: "${AGENT_BOOTSTRAP_TOKEN}"
      AGENT_DOCKER_PORT: "7001"
      AGENT_FILE_PORT: "7002"
      DATA_ROOT: /var/lib/oc-agent
      TZ: Asia/Shanghai
    networks:
      - oc-manager-network

networks:
  oc-manager-network:
    driver: bridge
```

如果开发机没有 GPU，Ollama compose 不配置 GPU reservation；只拉小模型验证链路。

OpenClaw runtime 镜像与 agent 镜像都需提前构建：

```text
docker build -t openclaw-runtime:dev ./runtime/openclaw
docker build -t oc-runtime-agent:dev ./runtime/agent
```

本地开发流程：

1. `docker compose up -d` 启动 manager-postgres、redis、new-api、new-api-postgres、ollama、agent。
2. `oc-manager migrate up` 跑数据库迁移。
3. 启动 manager（IDE 或 `go run ./cmd/server`）。
4. 平台管理员后台创建节点 → 复制 bootstrap_token → 设到 `AGENT_BOOTSTRAP_TOKEN` 环境变量 → `docker compose restart oc-runtime-agent` → 注册完成。
5. 后续即可创建组织、成员、应用走完整流程。

如果仓库不包含 runtime / agent Dockerfile，部署流程必须提前准备这两个镜像。

## 14. 工程规范

### 14.1 单元测试要求

项目要求完整的单元测试覆盖核心业务逻辑。

必须覆盖：

- domain 状态机。
- 权限判断（含工作目录访问校验、应用创建/删除限制、组织/应用知识库读写权限）。
- job 重试和幂等逻辑。
- OpenClaw CLI 输出解析。
- ChannelAdapter 接口和 WeChat 实现的二维码解析。
- 平台 prompt 模板渲染、变量注入、三层拼接顺序。
- 工作目录与知识库路径安全校验（拒绝 `..` 逃逸、符号链接、scope 越权）。
- 创建成员账号 + 应用复合事务（成功路径 + 任一步骤失败的回滚）。
- `new-api` adapter 错误映射。
- AgentFileClient 错误映射、TLS 校验、Bearer 认证。
- RuntimeAdapter（agent-backed）参数构造与多节点路由。
- 节点注册流程（bootstrap_token 单次消费、并发注册防御）。
- 心跳超时 reconciler。
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
- job backoff、max attempts、幂等。
- OpenClaw 二维码输出解析。
- 平台 prompt 模板渲染、变量注入、三层拼接顺序。
- 工作目录与知识库路径安全校验和大小上限。
- 创建账号 + 应用复合事务（成功 + 回滚）。
- 节点注册和心跳：bootstrap_token 单次消费、超时 reconciler。
- 知识库同步：应用级同步路径、组织级 `knowledge_sync_node` job 重试。
- `new-api` 错误响应映射。
- AgentFileClient / RuntimeAdapter 与 fake agent 的契约测试。
- 配置加载和环境变量展开（含 `master_key`）。
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

### 16.4 Docker socket 权限过高（节点 agent 持有）

风险：

- agent 容器挂载宿主机 Docker socket，等同拥有较高主机权限；agent 一旦被攻陷整台节点失守。
- manager → agent 的通信若被中间人，攻击者可以对 Docker 发任意指令。

应对：

- agent 容器以非特权用户运行，仅挂载 Docker socket，不挂其它系统目录。
- agent 暴露的 Docker 代理端口绑定内网，必要时用 firewall / 安全组限制 manager 来源 IP。
- agent 强制 TLS（自签 CA），manager 校验 server 证书；Bearer agent_token 鉴权。
- agent_token 存于 agent 容器挂载卷内，权限 0600，仅 agent 进程可读。
- agent 端 audit 日志记录所有 Docker 调用，便于审计。

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
- 启动时校验 `openclaw.system_prompt_template` 至少包含 `{{workspace_dir}}`、`{{knowledge_org_dir}}`、`{{knowledge_app_dir}}`。
- 容器创建时把渲染后的 prompt 作为环境变量注入；agent 端可通过容器 inspect 查看进行排查。

### 16.10 节点心跳超时误判

风险：

- 网络抖动导致心跳暂时超时，应用被误标记为 `error`。

应对：

- 心跳超时阈值用 `3 × heartbeat_interval_seconds`（默认 90s）。
- agent 心跳 POST 失败时本地重试。
- 节点恢复后 manager 自动把 `unreachable → active`，但应用状态需管理员手动恢复，避免抖动期间反复变换。
- 心跳超时事件写审计，便于排查。

### 16.11 知识库节点同步不一致

风险：

- 组织级知识库异步推送，部分节点同步失败导致内容不一致。

应对：

- `knowledge_sync_node` job 重试 + 退避。
- `/orgs/{orgId}/knowledge/sync-status` API 暴露每节点状态，前端展示并提供"重试同步"按钮。
- 容器初始化时 tar 流批量同步，确保新建容器看到的是当前主副本完整快照。
- 主副本即源数据，必要时可触发"全量重新同步到所有节点"管理员操作。

### 16.12 bootstrap_token 泄露

风险：

- 一次性令牌在传输或部署日志中泄露。

应对：

- 仅在创建节点时返回一次（前端要求管理员立即复制使用）。
- DB 仅存 hash。
- 设置 24h 过期窗口（配置项 `agent.bootstrap_token_ttl`）。
- 注册成功后立即清空 hash，绝不可二次使用。
- 如管理员遗漏使用，提供 `rotate-bootstrap-token` 接口重新生成。

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
