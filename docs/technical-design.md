# 技术设计

> 后端模块边界、状态机、接口契约、job 与 worker 模型。本文回答"实现是怎么组织的"，业务规则见 [product-design.md](./product-design.md)。

---

## 1 模块清单

本节按 `internal/` 顶层目录分节列出各包职责。部署拓扑与跨进程依赖关系见
[architecture.md](./architecture.md)。

### 1.1 `internal/api`

| 子目录 / 文件 | 职责 |
|---|---|
| `handlers/` | Gin handler 函数，每个业务域一个文件；请求体类型统一放 `dto.go` |
| `middleware/` | CSRF 防护、请求 ID 注入、安全响应头 |
| `router.go` | 路由组装，通过 `Dependencies` 结构体注入各 service |

`dto.go` 约定：所有请求体结构体导出（大写命名）、放在 `handlers` 包，字段使用
`json` + `binding` tag；swag 扫描 `@Param` / `@Success` 注解以生成 OpenAPI，
响应类型仍用各 `service.XxxResult`（跨包扫描）。

### 1.2 `internal/service`

业务逻辑层，负责编排 store 查询、调度 job 创建、调用集成包。每个业务对象
对应一个 `_service.go` 文件。重要子结构：

| 文件 | 职责 |
|---|---|
| `app_service.go` | 应用查询与状态读取 |
| `auth_service.go` | 登录、刷新令牌、登出 |
| `channel_service.go` | 渠道绑定生命周期 |
| `knowledge_service.go` | 组织 / 应用知识库 CRUD 与同步触发 |
| `knowledge_sync_status.go` | 知识库同步状态聚合 |
| `member_service.go` | 成员 CRUD、角色、状态切换 |
| `onboarding_service.go` | 成员 + 应用一体化创建流程 |
| `organization_service.go` | 组织 CRUD、new-api 组织映射 |
| `runtime_node_service.go` | runtime 节点注册、心跳、探活 |
| `runtime_operation_service.go` | 应用启 / 停 / 重启 / 删除运行时操作 |
| `resource_metrics_service.go` | 容器与节点资源指标查询 |
| `workspace_service.go` | 应用工作目录代理读写 |
| `usage_service.go` | new-api 用量代理 |
| `recharge_service.go` | 组织充值与余额查询 |
| `persona_service.go` | 组织人设读写 |
| `audit_service.go` | 审计日志写入与查询 |
| `reconciler.go` / `probe_reconciler.go` | 节点健康对账与主动探活 |
| `image_distribution_service.go` | 触发镜像同步 job |

### 1.3 `internal/domain`

跨 handler / service / worker 共享的业务枚举和状态机约束。不依赖任何外部包。

| 文件 | 职责 |
|---|---|
| `enums.go` | 所有状态枚举常量与 `Is*` 校验函数 |
| `app_state_machine.go` | AppStatus / APIKeyStatus 合法迁移表与 `EnsureAppTransition` |
| `job_state_machine.go` | JobStatus 合法迁移表与 `EnsureJobTransition` |

### 1.4 `internal/store`

数据访问层，封装 pgx 连接池与 sqlc 生成的类型安全查询。

| 文件 / 目录 | 职责 |
|---|---|
| `store.go` | `Store` 结构体：连接池、`Queries`、`WithTx` 事务辅助 |
| `queries/` | sqlc 源 `.sql` 文件，每个业务表一个文件 |
| `sqlc/` | sqlc 生成产物（不手工编辑） |
| `app_runner.go` / `onboarding_runner.go` | 需要事务的多步 store 操作组合 |
| `agent_token_store.go` | agent token 的加密写入与查询 |
| `persona_store.go` | 组织人设的存取 |
| `runtime_node_store.go` | 节点 store 辅助 |

### 1.5 `internal/scheduler`

| 文件 | 职责 |
|---|---|
| `scheduler.go` | `Scheduler.Tick`：扫 `jobs` 表 pending 记录重新入 Redis 队列 |
| `runner.go` | 按固定 ticker 调用 `Tick` 的运行循环 |

scheduler 是 PostgreSQL→Redis 的单向补偿通道，不直接修改 job 状态。

### 1.6 `internal/worker`

| 文件 / 目录 | 职责 |
|---|---|
| `worker.go` | `Worker.Tick`：从 Redis 队列预定 job → 执行 handler → 写回结果 |
| `runner.go` | 按固定 ticker 调用 `Tick` 的运行循环 |
| `handlers/` | 按业务分文件的 handler 实现 + `registry.go` 注册表 |

### 1.7 `internal/integrations`

| 子目录 | 职责 |
|---|---|
| `agent/` | manager 访问 runtime-agent 的 HTTP/TLS 客户端（docker 代理、文件 API、探活） |
| `channel/` | 渠道协议适配；当前实现微信（wechat）；通过 `Registry` 路由到具体 `ChannelAdapter` |
| `hermes/` | Hermes runtime 配置渲染：`SOUL.md`、`config.yaml`、`.env`、skills 目录、微信登录脚本调用 |
| `newapi/` | new-api 管理接口封装；错误映射为 sentinel error 便于 worker 区分重试策略 |
| `runtime/` | 通过 runtime-agent 操作远端 Docker 的接口定义与 agent-backed 实现 |
| `httpclient/` | `BaseHTTPClient`：URL 拼接、鉴权头注入、JSON 序列化、状态码→sentinel error |

### 1.8 `internal/runtime/imagesync`

| 文件 | 职责 |
|---|---|
| `service.go` | `SyncRuntimeImage`：以 ImageID 对账本地镜像与目标节点；不一致时 docker save/load |
| `clients.go` | `AgentImageClient`、`LocalImageProvider` 接口与默认实现 |

同步粒度是 nodeID：同一镜像对不同节点独立判断，不复用结果。

### 1.9 `internal/auth`

| 文件 | 职责 |
|---|---|
| `authorizer.go` | 所有 `Can*` 权限谓词（见 §6.2） |
| `token.go` | JWT access token 签发与解析 |
| `crypto.go` | 敏感字段对称加密（AES-GCM，master_key 来自 `config.Security`） |
| `password.go` | bcrypt 密码 hash 与验证 |

### 1.10 `internal/files`

| 文件 | 职责 |
|---|---|
| `safe_path.go` | `SafeRoot`：路径沙箱化，拦截 `..` 跳出、符号链接、非常规文件、大文件 |
| `knowledge_master.go` | 知识库主副本读写（基于 `SafeRoot`） |

### 1.11 `internal/migrations`

使用 `golang-migrate` 管理 SQL 迁移文件（`000001_init` 至 `000016_runtime_to_hermes`）。
`migrations.go` 暴露 `MigrationFS`，由 `main.go` 在启动时自动执行未完成迁移。

### 1.12 `internal/audit`

| 文件 | 职责 |
|---|---|
| `newapi_audit.go` | `NewAPIAuditHelper`：统一把 new-api 调用失败落到 `audit_logs.target_type=newapi_call`；失败不阻塞业务主路径 |

### 1.13 `internal/config`

| 文件 | 职责 |
|---|---|
| `config.go` | `Config` 根结构体及各子配置（App / Database / Redis / Auth / Security / Hermes / Agent / Runtime / NewAPI） |
| `loader.go` | 从 YAML 文件加载并校验必需项，缺失时 fail-fast |

### 1.14 `internal/log`

| 文件 | 职责 |
|---|---|
| `slog.go` | `requestIDHandler`：自动从 ctx 提取 trace_id 并附加到 slog record |
| `redact.go` | 日志字段脱敏（token、password 等敏感键） |
| `safe_error.go` | 包装错误，屏蔽底层细节后暴露给 HTTP 响应 |

### 1.15 `internal/redis`

| 文件 | 职责 |
|---|---|
| `queue.go` | `RedisQueue`：基于 ZSET 的 job 信号队列；`Enqueue` / `EnqueueDelayed` / `Reserve` 均幂等 |
| `format.go` | Redis key 格式化辅助 |

---

## 2 状态机

### 2.1 AppStatus

定义位置：`internal/domain/enums.go`（枚举值）、`internal/domain/app_state_machine.go`（迁移表）。

| 状态值 | 含义 |
|---|---|
| `draft` | 初始草稿，尚未提交初始化 |
| `initializing` | onboarding job 已触发，worker 正在拉镜像和配置 new-api |
| `binding_waiting` | 初始化完成，等待渠道（微信）扫码绑定 |
| `binding_failed` | 渠道绑定超时或 token 过期 |
| `running` | 渠道绑定成功，容器运行中 |
| `stopped` | 用户主动停止 |
| `error` | 任意步骤失败后的吸入态 |
| `deleted` | 终态，`deleted_at` 非空 |

合法迁移：

```
draft          → initializing
initializing   → binding_waiting  |  error
binding_waiting → running  |  binding_failed
binding_failed  → binding_waiting  |  error
running         → stopped  |  error
stopped         → running   |  error
error           → initializing  |  deleted
```

约束（`internal/domain/app_state_machine.go:57`）：
- `deleted` 是终态；除 `error → deleted` 外，进入 `deleted` 必须由 `SoftDeleteApp` 调用单独完成。
- 任何 service 写库前必须调用 `EnsureAppTransition`，不得绕过状态机直接 SQL 改状态。

### 2.2 APIKeyStatus

定义位置：`internal/domain/enums.go`、`internal/domain/app_state_machine.go`。

| 状态值 | 含义 |
|---|---|
| `pending` | 初始创建，尚未在 new-api 侧激活 |
| `active` | new-api 侧可用 |
| `disabled` | 应用停止时由 worker 禁用 |
| `error` | new-api 操作失败 |

合法迁移：

```
pending   → active  |  error
active    → disabled  |  error
disabled  → active
error     → pending
```

API key 与 app 状态相互独立；`app.status=stopped` 时 api_key 可处于 `disabled`，
`app.status=binding_waiting` 时 api_key 可处于 `error`。

### 2.3 JobStatus

定义位置：`internal/domain/enums.go`、`internal/domain/job_state_machine.go`。

| 状态值 | 含义 |
|---|---|
| `pending` | 等待调度执行 |
| `running` | worker 已锁定，正在执行 |
| `succeeded` | 执行成功（终态） |
| `failed` | 达到最大重试次数后失败（终态） |
| `canceled` | 被取消（终态） |

合法迁移：

```
pending  → running  |  canceled
running  → succeeded  |  failed  |  pending（重新排队）
failed   → pending（手工重试）
```

终态（`succeeded` / `failed` / `canceled`）由 `JobIsTerminal` 判断；
调度器扫库仅取 `pending` 且 `run_after <= now` 的 job。

### 2.4 RuntimeNodeStatus

定义位置：`internal/domain/enums.go`。

| 状态值 | 含义 |
|---|---|
| `pending` | agent 已提交注册请求，等待平台审核或自动激活 |
| `active` | 心跳正常，可接受 job 分配 |
| `unreachable` | 心跳超时，manager 主动探活失败 |
| `disabled` | 平台管理员手动停用 |
| `degraded` | 节点可达但容量受限或部分检查未通过 |

迁移由 `runtime_node_service.go` 和 `probe_reconciler.go` 根据心跳时间与探活结果驱动，
无显式迁移表；`domain.IsRuntimeNodeStatus` 用于入库前校验合法值。

### 2.5 ChannelStatus

定义位置：`internal/domain/enums.go`。

| 状态值 | 含义 |
|---|---|
| `unbound` | 尚未发起绑定 |
| `pending_auth` | 已生成二维码，等待用户扫码 |
| `bound` | 渠道绑定成功 |
| `failed` | 扫码或 token 交换失败 |
| `expired` | token 已过期 |
| `unbound_by_user` | 用户主动解绑 |
| `deleted` | 对应应用已删除 |

### 2.6 通用 Status（users / organizations）

定义位置：`internal/domain/enums.go`。

| 常量 | 值 | 用途 |
|---|---|---|
| `StatusActive` | `"active"` | 正常可用 |
| `StatusDisabled` | `"disabled"` | 禁用（users 同时写 deleted_at，见 §5.3） |
| `StatusDeleted` | `"deleted"` | 组织软删除终态 |

---

## 3 接口契约

所有 HTTP 接口由 swag 注解扫描生成，权威源文件：

**[openapi/openapi.yaml](../openapi/openapi.yaml)**

前端 TypeScript 类型由 `make web-types-gen` 从 yaml 生成：

**`web/src/api/generated.ts`**（不手工编辑）

更新流程：

```
handler 注解变更 → make openapi-gen → openapi/openapi.yaml
                                     → make web-types-gen → web/src/api/generated.ts
```

`make openapi-check` 在 CI 中校验：运行 `make openapi-gen` 后工作区应保持干净，
否则说明 yaml 未跟随代码更新。

**dto.go 约定**：请求体类型放 `internal/api/handlers/dto.go`，导出大写命名，
字段携带 `json` + `binding` tag；响应类型保留在各 `service.XxxResult` 中。

---

## 4 job 与 worker 模型

### 4.1 数据流概览

```
HTTP handler / reconciler
       │ CreateJob → jobs(pending)
       ▼
   Redis ZSET (快速信号)
       │ Reserve
       ▼
   Worker.Tick
       │ MarkJobRunning → jobs(running)
       │ handler(ctx, job)
       ├─ 成功 → MarkJobSucceeded → jobs(succeeded)
       └─ 失败 → attempts < max → RetryJob → jobs(pending, run_after+=backoff)
                 attempts >= max → MarkJobFailed → jobs(failed)

   Scheduler.Tick（每 5~10s）
       │ ListReadyJobs (pending, run_after<=now)
       └─ Enqueue 补偿入 Redis（信号丢失时兜底）
```

PostgreSQL 是 job 事实来源；Redis 信号丢失只降级为等待下次 scheduler 补偿。

### 4.2 scheduler 包（`internal/scheduler`）

- `Scheduler.Tick`：扫 `jobs` 表取满足 `run_after <= now` 的 pending 记录（`BatchSize` 默认 100），推入 Redis。
- 只读不改写 job 状态，幂等；Redis ZSET member 去重防重复入队。
- `runner.go`：按 ticker 驱动 `Tick`，Ticker 间隔由调用方配置。

### 4.3 worker 包（`internal/worker`）

**Worker 配置参数（`Config`）：**

| 字段 | 默认值 | 说明 |
|---|---|---|
| `WorkerID` | `"worker"` | 写入 `jobs.locked_by` 供排障 |
| `BatchSize` | 8 | 单次 Tick 预定 job 数量 |
| `BackoffBase` | 5s | 首次失败重试间隔 |
| `BackoffFactor` | 2 | 指数退避倍率 |
| `BackoffMax` | 5min | 退避上限 |

**handler 注册**：`handlers.Registry.MustRegister(jobType, fn)`，启动期一次性完成。
重复注册同一 `jobType` 会 panic，防止静默覆盖。

### 4.4 关键 handler 一览

| handler 文件 | job 类型 | 触发方式 | 主要副作用 |
|---|---|---|---|
| `app_initialize.go` | `app_initialize` | onboarding service 创建 | 拉取镜像同步到节点、创建容器、写 new-api token、更新 app 状态至 `binding_waiting` 或 `error` |
| `app_runtime_ops.go` | `app_start_container` | runtime_operation_service | 启动容器，更新 app 状态至 `running` |
| `app_runtime_ops.go` | `app_stop_container` | runtime_operation_service | 停止容器，触发 `newapi_disable_key` job，更新 app 状态至 `stopped` |
| `app_runtime_ops.go` | `app_restart_container` | runtime_operation_service | 停止后启动容器，清空 Hermes session |
| `app_runtime_ops.go` | `app_delete` | runtime_operation_service | 清理容器与运行时文件，禁用 new-api token，软删除 app |
| `app_health_check.go` | `app_health_check` | 定时 job / worker 探活 | docker inspect 健康状态写入 `apps.health_state_json`；按 restart_policy 自动拉起停掉的容器 |
| `channel_login.go` | `channel_start_login` | channel_service | 调用渠道 adapter 生成登录二维码，写 pending_auth |
| `channel_login.go` | `channel_check_binding` | 轮询 job（自我重新排队） | 轮询渠道授权结果，成功写 `bound`，超时写 `expired` |
| `knowledge_sync.go` | `knowledge_sync_node` | knowledge_service | 把管理端知识库主副本同步到指定 runtime node |
| `runtime_refresh_status.go` | `runtime_refresh_status` | 定时 job | docker inspect 容器快照写入 `apps.runtime_snapshot_json` |
| `newapi_key_status.go` | `newapi_disable_key` | app stop / delete 流程 | 在 new-api 侧禁用对应 token |
| `newapi_key_status.go` | `newapi_restore_key` | app start 流程 | 在 new-api 侧恢复对应 token |

> handler 文件位于 `internal/worker/handlers/`。每个 handler 以 `HandlerFunc` 签名
> `func(ctx context.Context, job sqlc.Job) error` 注册，自行从 `job.PayloadJSON` 反序列化业务参数。

---

## 5 数据访问层

### 5.1 sqlc 约定

- SQL 源文件：`internal/store/queries/<table>.sql`，每张表一个文件。
- 生成产物：`internal/store/sqlc/`，不手工编辑；由 `make sqlc-generate` 重新生成。
- 查询命名：`PascalCase` 动词 + 对象，例如 `CreateApp`、`ListReadyJobs`、`MarkJobRunning`。
- 注解格式：`-- name: QueryName :one|:many|:exec`。

常用命名模式：

| 模式 | 示例 | 场景 |
|---|---|---|
| `Create*` | `CreateApp`, `CreateJob` | 插入并返回完整行 |
| `Get*` | `GetApp`, `GetJobByID` | 单行查询，未找到返回 pgx.ErrNoRows |
| `List*` | `ListUsersByOrg`, `ListReadyJobs` | 分页或条件批量查询 |
| `Update*` / `Set*` | `UpdateUserProfile`, `SetUserStatus` | 部分字段更新 |
| `Mark*` | `MarkJobRunning`, `MarkJobSucceeded` | 状态推进类写操作 |
| `SoftDelete*` | `SoftDeleteUser` | 设置 `deleted_at` 不物理删除 |

### 5.2 事务辅助

`store.WithTx(ctx, func(*sqlc.Queries) error)` 包装 pgx 事务：
- `fn` 返回错误时自动 rollback；
- 提交失败返回提交错误；
- 调用方不得在 `fn` 内部自行 Commit / Rollback。

### 5.3 users.deleted_at 语义

`users.deleted_at` 是**下线时间戳**，不是"真删除时间"，与 `organizations.deleted_at`（真软删除）语义不同：

- `status=disabled` 时 SQL 自动 `SET deleted_at = NOW()`（见 `SetUserStatus` query）；
- 重新启用时 SQL 自动 `SET deleted_at = NULL`；
- 查询活跃用户：`WHERE deleted_at IS NULL`，走 `users_active_idx` 部分索引。

真软删除场景使用 `SoftDeleteUser`（仅设 `deleted_at`，不动 `status`）。

### 5.4 迁移管理

迁移文件按序号命名：`000001_init` ~ `000016_runtime_to_hermes`，
存放于 `internal/migrations/`。启动期由 `migrations.go` 暴露的 `MigrationFS`
自动 apply 未完成迁移，失败时 fail-fast。

---

## 6 跨模块约束

### 6.1 OpenAPI 同步

```
handler swag 注解
  └─ make openapi-gen ──→ openapi/openapi.yaml
                              └─ make web-types-gen ──→ web/src/api/generated.ts
```

- 修改 handler 签名 / 请求体 / 响应类型 / 路由后，必须运行 `make openapi-gen` + `make web-types-gen` 并一起提交。
- `make openapi-check`（`make openapi-gen` 后检查 git 工作区是否干净）作为守门验证。
- 两个生成文件均入 git，不手工编辑。

### 6.2 权限校验

所有 `Can*` 谓词集中在 `internal/auth/authorizer.go`，service 包不再定义本地 `canX` 函数。

当前已定义谓词：

| 谓词 | 适用资源 | 说明 |
|---|---|---|
| `CanManageOrg` / `CanViewOrg` | 组织 | 平台管理员跨组织；org_admin 限本组织 |
| `CanManageMember` / `CanViewMember` / `CanEditMember` | 成员 | org_admin 管本组织；成员可编辑自身 |
| `CanManageApp` / `CanViewApp` | 应用 | 平台管理员只读；org_admin 限本组织；成员限自身 app |
| `CanCreateAppForOrg` / `CanCreateAppForMember` | 应用创建 | org_admin 创建；平台管理员可跨组织复建 |
| `CanTriggerRuntimeOperation` | 运行时操作 | 等同 `CanManageApp` |
| `CanReadOrgKnowledge` / `CanWriteOrgKnowledge` | 组织知识库 | 写只允许本组织 org_admin |
| `CanReadAppKnowledge` / `CanWriteAppKnowledge` | 应用知识库 | 写等同 `CanManageApp` |
| `CanViewOrgKnowledgeSyncStatus` / `CanRetryOrgKnowledgeSync` | 同步状态 | 仅本组织 org_admin |
| `CanViewOrgPersona` / `CanManageOrgPersona` | 人设 | 等同组织读 / 写谓词 |
| `CanViewOrgUsage` / `CanViewMemberUsage` | 用量 | 平台管理员 + org_admin 跨组织（平台）或本组织 |
| `CanViewOrgAudit` / `CanViewAppAudit` / `CanViewOwnAudit` | 审计 | 组织级仅管理员；应用级等同 `CanViewApp` |

新增权限规则时优先扩展现有 `Can*` 函数，不在 handler 或 service 内内联 `if principal.Role == "..."` 判断。

### 6.3 审计日志

审计写入通过 `service.AuditService.Record` 完成，字段：

- `actor_id` / `actor_role`：操作者 ID 与角色；后台 worker 路径为空，回退为 `actor_role=system`。
- `org_id`：所属组织；平台级操作可为空。
- `target_type` / `target_id`：被操作资源类型与 ID。
- `action`：操作动词（`create` / `update` / `delete` / `start` / `stop` 等）。
- `error_message`：操作失败时记录，成功时为空。

new-api 调用失败通过 `internal/audit.NewAPIAuditHelper` 统一落到
`target_type=newapi_call`；失败不阻断主路径。

### 6.4 镜像同步

`internal/runtime/imagesync.Service.SyncRuntimeImage` 以 **ImageID** 对账：

1. `LocalImageProvider.ImageID` 获取 manager 本地镜像 ID；
2. `AgentImageClient.InspectImage` 获取目标节点镜像 ID；
3. 两者不同（或目标节点不存在）：`LocalImageProvider.Archive` 流式 docker save → `AgentImageClient.LoadImage`；
4. `SyncResult.Transferred` 标记是否实际传输；`LocalID == RemoteID` 确认同步完成。

同步结果按 nodeID 独立判断，不跨节点复用，避免误判。

### 6.5 日志与 trace_id

所有 slog 输出通过 `internal/log.requestIDHandler` 自动附加 `trace_id`；
提取函数由 `internal/api/middleware` 在启动期通过 `log.SetRequestIDExtractor` 注入，
避免 `internal/log` 直接引用 middleware 造成循环依赖。

敏感字段（token、password 等）通过 `internal/log.redact` 在写日志前移除。

### 6.6 路径安全

所有知识库主副本和工作目录读写必须经过 `internal/files.SafeRoot`，
拦截路径跳出（`..`）、符号链接、URL 编码绕过、非常规文件和超大文件。
