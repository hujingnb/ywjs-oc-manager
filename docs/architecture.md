# 架构概览

面向新协作者的「能在一两个小时内建立心智」的入门文档。深入设计请读
[`openclaw-manager-design.md`](./openclaw-manager-design.md)（PRD）和
[`openclaw-manager-technical-design.md`](./openclaw-manager-technical-design.md)（技术设计）。

## 1. 角色与组件

```text
┌────────────────────────────── 浏览器（用户） ──────────────────────────────┐
│                                                                          │
│   平台管理员 / 组织管理员 / 组织成员                                       │
│       ▲                                                                  │
│       │ Vue 3 SPA  (web/)                                                │
└───────┼──────────────────────────────────────────────────────────────────┘
        │ HTTPS（生产）/ HTTP（本地）
┌───────┼─────────────────── 核心层 manager 机器 ──────────────────────────┐
│   manager-api（Go / Gin）                                                │
│   ├── api/handlers           HTTP 协议层，校验 + 调 service              │
│   ├── api/middleware         JWT、CSRF double-submit、CORS               │
│   ├── service/*Service       业务逻辑层（auth/member/app/runtime/...）    │
│   ├── store/sqlc             PostgreSQL repository（sqlc 生成）          │
│   ├── domain                 状态机 + 枚举                               │
│   ├── runtime/imagesync      把 OpenClaw runtime 镜像分发到节点          │
│   ├── scheduler              job 队列调度器                              │
│   ├── worker                 job 执行器                                  │
│   └── integrations           new-api / agent client                      │
│            │                                                             │
│            ▼                                                             │
│   PostgreSQL  Redis  new-api  ollama                                     │
└───────────────────────────────────┬──────────────────────────────────────┘
                                    │ HTTPS + Bearer + 自签 CA
                ┌───────────────────┼───────────────────┐
                ▼                                       ▼
   ┌─── Runtime Node A ───────────┐    ┌─── Runtime Node B ───────────┐
   │ oc-runtime-agent（runtime/   │    │ oc-runtime-agent             │
   │   agent/）                   │    │                              │
   │  ├ 7001 docker proxy         │    │  ├ 7001 docker proxy         │
   │  ├ 7002 file / scope API     │    │  ├ 7002 file / scope API     │
   │  └ heartbeat ticker          │    │  └ heartbeat ticker          │
   │                              │    │                              │
   │  /var/run/docker.sock        │    │  /var/run/docker.sock        │
   │       │                      │    │       │                      │
   │       ▼                      │    │       ▼                      │
   │  OpenClaw 容器 ×N            │    │  OpenClaw 容器 ×N            │
   │  data_root：                 │    │                              │
   │   apps/<app_id>/workspace    │    │                              │
   │   apps/<app_id>/knowledge    │    │                              │
   │   orgs/<org_id>/knowledge    │    │                              │
   └──────────────────────────────┘    └──────────────────────────────┘
```

manager 不持有任何 Docker socket，也不直接读节点文件系统：所有节点级动作经
agent。agent 用自签 TLS 暴露 7001 / 7002，manager 拿 agent CA + bearer token 调用。

## 2. 主要进程

| 进程 | 入口 | 职责 |
|---|---|---|
| manager-api | `cmd/server` | HTTP API + 内嵌 worker + 内嵌 scheduler |
| migrate     | `cmd/migrate` | 数据库迁移 CLI（`up` / `down`） |
| seed-admin  | `cmd/seed-admin` | 平台管理员账号种子 |
| seed-e2e    | `cmd/seed-e2e` | Playwright e2e fixture 注入（OCM_E2E=1 守门） |
| oc-runtime-agent | `runtime/agent` | 节点上常驻；docker proxy + 文件 API + 心跳 |
| manager-web | `web/` Vite | Vue 3 SPA |

第一版 worker 与 scheduler 与 manager-api 同进程内嵌；后续可拆分进程。

## 3. 后端分层

```text
HTTP 请求
   │
   ▼
internal/api/middleware       JWT / CSRF / CORS / panic recover
   │
   ▼
internal/api/handlers         协议层：参数解析 / 校验 / 序列化
   │   只做 HTTP 约定，不持有事务
   ▼
internal/service              业务层：权限、跨模块编排、事务、外部副作用
   │   - 同步副作用：直接调 integrations
   │   - 长流程：写 PostgreSQL `jobs` 表 + 通知 Redis 队列
   ▼
internal/store/sqlc           数据层：sqlc 生成的 typed repository
   │
   ▼
PostgreSQL
```

旁路依赖：

- `internal/redis` Redis 客户端封装
- `internal/auth` JWT / 密钥 / CSRF
- `internal/files` 路径沙箱（防止节点文件 API 越权）
- `internal/log` 结构化日志
- `internal/integrations` `new-api` HTTP client、agent HTTP client、Docker 代理 client
- `internal/runtime/imagesync` 把宿主 `openclaw-runtime` 镜像 `docker save` 后流给 agent `docker load`

权限校验**必须在后端 service 层执行**，前端菜单/按钮隐藏只是 UX。

## 4. 主要服务

`internal/service/` 下每个 Service 对应一组 handler：

| Service | 责任 |
|---|---|
| `AuthService` | 登录、refresh、CSRF token 颁发；`refresh_tokens` 表持久化 |
| `OrganizationService` | 组织 CRUD + 与 `new-api` 账号关联 |
| `MemberService` | 组织成员 CRUD（普通成员 / 组织管理员） |
| `MemberOnboardingService` | 创建成员账号 + 应用 + 渠道占位 + audit + job 在同一事务内完成 |
| `AppService` | 应用查询、状态聚合（容器状态 / api_key 状态 / 渠道绑定状态） |
| `RuntimeNodeService` | 节点自动 enroll / agent_token / 心跳 / max_apps |
| `RuntimeOperationService` | 应用容器 start / stop / restart / logs / stats |
| `KnowledgeService` | 组织级 + 应用级文件主副本写入；触发 `knowledge_sync_node` job |
| `WorkspaceService` | 应用工作目录浏览 / 单文件下载 / 文件夹打包 |
| `ChannelService` | 渠道绑定状态机（第一版仅微信扫码）；轮询 OpenClaw 容器内插件 |
| `RechargeService` | 平台管理员对组织充值 Token Credit；落 audit + 调 new-api |
| `PersonaService` | 组织级 / 应用级 AI 人设；含「平台默认 → 组织 → 应用」三层模板 |
| `UsageService` | 直查 `new-api` 用量，按 platform / org / app 聚合，不缓存 |
| `AuditService` | 审计日志查询（写入分散在各 service） |
| `PlatformOverviewService` | 平台总览六项计数 |
| `Reconciler` | 节点心跳超时 → 翻 `unreachable`；恢复后翻回 `active` |
| `ImageDistributionService` | imagesync 的对外接口 |

## 5. 状态机

集中在 `internal/domain/`：

- `app_state_machine.go` — 应用全生命周期：`pending` → `creating` → `running` / `error` / `stopped` / `deleted`
- `job_state_machine.go` — job：`queued` → `processing` → `succeeded` / `failed` / `dead_letter`
- `enums.go` — 角色、节点状态、渠道状态、知识库同步状态

写新功能改动状态前先看一遍这三份文件的测试，避免引入非法转移。

## 6. job 与 worker

长流程都通过 `jobs` 表 + Redis 队列 + worker 异步完成：

```text
Service 写 PostgreSQL jobs(payload, type, status=queued)
   │
   ▼
JobNotifier publish to Redis Stream
   │
   ▼
worker pop          ──► 处理 → UPDATE jobs SET status=succeeded
   │                          (失败累加 attempts，超过阈值翻 dead_letter)
   │
   └─ scheduler 定期扫 status=queued / processing 超时 job 兜底唤醒
```

主要 job 类型：

- `member_onboarding`（创建容器 + new-api api_key + 同步知识库）
- `knowledge_sync_node`（组织级知识库异步推送）
- `runtime_node_health_reconcile`（心跳超时巡检）
- `workspace_archive_cleanup`（工作目录归档保留期）
- `channel_bind_*`（渠道扫码状态轮询）

`scheduler.Reconciler` 是 worker 的兜底机制：即使 Redis 短暂不可用，
重启后从 `jobs` 表重建队列即可。

## 7. agent ↔ manager 协议

agent 暴露两个 HTTP 端口：

| 端口 | 路由前缀 | 用途 |
|---|---|---|
| 7001 | `/v1/docker/*` | Docker daemon 反向代理（manager 通过 Docker SDK 调用） |
| 7002 | `/v1/scopes/*`、`/v1/files/ping`、`/healthz` | 文件管理 / scope 管理 / 健康检查 |

通信约定：

1. **自动注册**：agent 启动时生成或读取 `state_dir/agent-id`，使用
   `manager.enrollment_secret` 调 `POST /api/v1/agent/enroll`。manager 按
   `agent_id` 幂等创建或刷新 `runtime_nodes`，返回 `node_id` 与 long-lived
   `agent_token`（hash 校验 + aes-256-gcm 密文存表）。
2. **本地持久化**：agent 把 `node-id` / `agent-token` 写入 `state_dir`，后续重启
   直接复用；如果心跳收到 401，会自动重新 enroll 并刷新 token。
3. **心跳**：agent ticker 周期 `agent.heartbeat_interval_seconds`（默认 30s）调
   manager `POST /api/v1/agent/heartbeat`。manager 超过 90s 未收心跳标记节点
   `unreachable`；下一拍心跳恢复即翻回 `active`。
4. **主动探测**：manager 周期探测 agent 的 `/v1/docker/_ping` 与
   `/v1/files/ping`。入站探测连续失败时节点进入 `degraded`，连续恢复后回到
   `active`；`degraded` 不会把节点上的应用推 `error`。
5. **manager → agent**：所有调用走 TLS（agent 自签 CA）+ Bearer `agent_token`，
   并对客户端 IP 做 `trusted_cidr` 校验。

agent 的 docker proxy 不实现 Docker API 全集，只透传 manager 用得到的几个动词
（containers / images / volumes / events 子集）。

## 8. 知识库与工作目录文件流

层级：

- 组织级：`{manager.knowledge_root}/orgs/<org_id>/...`，由 manager 主副本主导，
  `knowledge_sync_node` 异步推送到该组织下应用所在的全部节点；
  节点上落 `{agent.data_root}/orgs/<org_id>/...`，挂载到 OpenClaw 容器
  `/knowledge/org/`（只读）。
- 应用级：`{manager.knowledge_root}/apps/<app_id>/...` →
  `{agent.data_root}/apps/<app_id>/...` → 容器 `/knowledge/app/`（只读）。
- 工作目录：仅在节点 `{agent.data_root}/apps/<app_id>/workspace/` 上，OpenClaw
  容器写，UI 通过 agent 文件 API 只读浏览 / 下载。

manager 不解析文件内容；OpenClaw 通过 system prompt 模板里的
`{{workspace_dir}}` / `{{knowledge_org_dir}}` / `{{knowledge_app_dir}}` 占位符
得知挂载位置，自行处理。

## 9. 配置与密钥

- `config/manager.yaml`（`OCM_CONFIG`）：manager-api / migrate / seed-admin 共用
- `config/agent.yaml`（`OC_AGENT_CONFIG`）：oc-runtime-agent
- `config/*.yaml` 由 `.gitignore` 屏蔽，仅 `*.example.yaml` 入仓
- `.env`：仅承载 docker-compose 端口映射，不再注入业务配置

字段速查见 [`docs/configuration.md`](./configuration.md)。

`security.master_key` 一旦生成不可轮换：旧数据无法重新解密。

## 10. 前端

```text
web/src/
├── main.ts             createApp + Pinia + router + Naive UI
├── app/
│   └── router.ts       路由表 + 鉴权守卫（getStoredAccessToken / fetchCurrentUser）
├── layouts/
│   ├── AuthLayout.vue        登录页布局
│   └── DashboardLayout.vue   左侧 nav + 顶部用户菜单
├── pages/
│   ├── login/                登录
│   ├── dashboard/            角色感知首页
│   ├── platform/             平台总览 / 组织管理 / 充值
│   ├── org/                  组织成员 / 创建成员（含初始化）/ 人设
│   ├── apps/                 应用列表 / 应用详情五个 tab
│   ├── runtime-nodes/        运行节点列表 / 节点详情
│   ├── knowledge/            组织级知识库
│   ├── usage/                用量
│   └── audit/                审计日志
├── api/                HTTP client + endpoint 封装
├── stores/             Pinia store（auth / app / org ...）
├── components/         通用组件
└── domain/             与后端枚举/状态机镜像
```

数据请求统一走 TanStack Query；写操作经 Naive UI confirm modal +
`ConfirmActionModal`（高风险操作要求输入名字校验）。

## 11. 重要数据流（按场景）

### 11.1 创建成员（含创建应用）

```
组织管理员 UI
  POST /api/v1/members            （含 password_plain + display_name + role）
       │
       ▼
 MembersHandler.Create
       │
       ▼
 MemberOnboardingService.OnboardMember
   ├─ TxRunner.WithTx：
   │    ├─ CreateUser
   │    ├─ NodeSelector.ListActiveNodesWithAppCounts → 选剩余容量最大的节点
   │    ├─ CreateApp（含 owner_user_id 唯一索引保证 1:1）
   │    ├─ CreateChannelBinding（占位 status=unbound）
   │    ├─ CreateAuditLog
   │    └─ CreateJob(type=member_onboarding)
   ├─ JobNotifier.Notify(Redis Stream)
   └─ return 201
                              │
                              ▼
       worker member_onboarding：
       ├─ ImageDistributionService 把 openclaw-runtime 镜像 push 到节点
       ├─ DockerProxy create container（含三个 bind mount）
       ├─ new-api: create user / api_key（写明文 sk- 到 OpenClaw 容器配置）
       ├─ 启动容器
       └─ UPDATE apps SET status='running'
```

### 11.2 上传组织级知识库文件

```
组织管理员 UI 上传 PDF
  PUT /api/v1/orgs/{orgId}/knowledge?path=foo/bar.pdf
       │
       ▼
 KnowledgeHandler → KnowledgeService.PutOrgFile
   ├─ files.SafeRoot 沙箱化路径
   ├─ 写本地主副本 {data_root}/orgs/<org_id>/...
   ├─ 写 audit
   ├─ 列出该组织所有 active 应用所在节点
   └─ 为每个节点入队 knowledge_sync_node job
                              │
                              ▼
       worker knowledge_sync_node：
       ├─ 调 agent /v1/files PUT（流式 tar）
       ├─ 写 knowledge_sync_status（per node 状态：synced / failed / pending）
       └─ 失败重试 + 超阈值翻 failed
```

### 11.3 应用容器启停

```
成员 UI 点击「启动」
  POST /api/v1/apps/{appId}/runtime/start
       │
       ▼
 AppRuntimeHandler → RuntimeOperationService.Start
   ├─ 校验权限（owner / 组织管理员 / 平台管理员）
   ├─ DockerProxyClient.ContainerStart（经 agent 7001）
   ├─ 写 audit
   └─ 返回新状态
```

### 11.4 节点心跳与自愈

```
runtime/agent ticker（30s）
  POST /api/v1/agent/runtime-nodes/{id}/heartbeat
       │
       ▼
 RuntimeNodeService.RecordHeartbeat → UPDATE last_heartbeat_at
                                       SET status='active' (if was unreachable)

scheduler.Reconciler 每 N 秒：
  SELECT 节点 last_heartbeat_at < now() - interval '90 seconds'
  UPDATE status='unreachable'
  受影响应用打 audit + UI 显示告警条
```

## 12. 测试金字塔

| 层 | 范围 | 跑法 |
|---|---|---|
| 单元（service / domain） | 内存桩，覆盖正常 / 异常 / 边界 | `make test` |
| 集成 | 真 PostgreSQL + Redis（docker compose） | `make integration-test` |
| 前端单测 | vitest（components + stores） | `make web-test` |
| 类型检查 | vue-tsc | `make web-typecheck` |
| e2e | Playwright（5 场景，OCM_E2E fixture） | `make seed-e2e` + `web/` 内 `npm run test:e2e` |

提交前最低必跑：与改动相关的单测 + 集成（如改了 store / handler）+ web 单测/类型检查（如改了前端）。

## 13. 何时回到设计文档

| 你想做什么 | 该看哪份 |
|---|---|
| 新增一个角色 / 权限点 | [PRD §4](./openclaw-manager-design.md) + 后端 middleware |
| 新增一个对象 / 字段 | [PRD §5](./openclaw-manager-design.md) + 技术设计「数据模型」+ 写 migration |
| 新增一个长流程 | 技术设计「job 与 worker」+ `internal/scheduler` |
| 新增一个角色页面 | `web/src/app/router.ts` + 对应 layout 的 nav |
| 改 agent 协议 | 技术设计「agent 接口契约」+ 同步改 manager `integrations` 与 agent handler |
