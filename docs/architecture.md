# 架构总览

> manager 与 runtime-agent 的模块图、节点拓扑与关键数据流。新协作者读完本文，
> 应能理解 manager / agent / new-api / Hermes 容器之间的边界、调用方向与持久化划分。

## 1. 系统拓扑

```text
┌──────────────────────────── 核心层（manager 所在机器）────────────────────────────┐
│                                                                                  │
│   Browser ──► Vue 3 SPA (web/) ──► manager-api (Go / Gin)                        │
│                                         │                                        │
│                                         ├── PostgreSQL（业务库 / job / 审计）    │
│                                         ├── Redis（队列 / 短期状态 / 锁）        │
│                                         ├── new-api（model gateway / 计费）      │
│                                         └── ollama（本地模型，可选）             │
└──────────────────────────────────────────┬───────────────────────────────────────┘
                                           │ HTTPS + Bearer token（自签 CA）
                          ┌────────────────┴────────────────┐
                          ▼                                 ▼
                 ┌─────────────────┐               ┌─────────────────┐
                 │ Runtime Node A  │               │ Runtime Node B  │
                 │ oc-runtime-     │               │ oc-runtime-     │
                 │  agent          │   …………         │  agent          │
                 │  ├─ docker      │               │  ├─ docker      │
                 │  │  proxy :7001 │               │  │  proxy :7001 │
                 │  └─ file /      │               │  └─ file /      │
                 │     scope API   │               │     scope API   │
                 │     :7002       │               │     :7002       │
                 │  heartbeat ──►  │               │  heartbeat ──►  │
                 │  manager-api    │               │  manager-api    │
                 │                 │               │                 │
                 │  Hermes 容器×N  │               │  Hermes 容器×N  │
                 │  bind mount:    │               │                 │
                 │  .hermes/──►    │               │                 │
                 │  /opt/data      │               │                 │
                 └─────────────────┘               └─────────────────┘
```

manager 不持有任何节点的 Docker socket，也不直接读节点文件系统。所有节点级操作
经由部署在节点上的 `oc-runtime-agent` 完成。agent 以自签 TLS + Bearer token 暴露
两个端口；manager 持有 agent CA 与 `agent_token` 发起调用，并对来源 IP 做
`trusted_cidr` 校验。

## 2. 模块边界

### 2.1 manager 后端（internal/）

后端分三层，从 HTTP 入口到数据库依次调用，不跨层：

```text
HTTP 请求
   │
   ▼
internal/api/middleware       JWT / CSRF double-submit / CORS / panic recover
   │
   ▼
internal/api/handlers         协议层：参数解析 / 校验 / 序列化 / 路由
   │   只做 HTTP 约定，不持有跨模块事务
   ▼
internal/service              业务层：权限校验 / 跨模块编排 / 事务 / 外部副作用
   │   同步副作用直接调 integrations；长流程写 PostgreSQL jobs + 通知 Redis 队列
   ▼
internal/store/sqlc           数据层：sqlc 生成的 typed repository
   │
   ▼
PostgreSQL
```

各目录职责：

| 目录 | 职责 |
|---|---|
| `internal/api/handlers` | HTTP handler：参数绑定、校验、调 service、序列化响应；请求/响应类型定义在 `dto.go` |
| `internal/api/middleware` | JWT 认证、CSRF double-submit cookie、CORS、请求 ID 注入 |
| `internal/service` | 业务逻辑层，含 auth / member / app / org / runtime / knowledge / usage 等服务 |
| `internal/domain` | 状态机（AppStateMachine / JobStateMachine）与全局枚举（角色、状态、job 类型） |
| `internal/store` | sqlc 生成的 repository；`store.go` 聚合全部查询接口；`queries/` 存 SQL 源文件 |
| `internal/migrations` | golang-migrate up/down SQL，数据库 schema 唯一来源 |
| `internal/scheduler` | 定时扫描 PostgreSQL jobs 表，把到期任务推入 Redis 队列；兜底机制 |
| `internal/worker` | 从 Redis 队列消费 job ID，派发到 `handlers/` 下对应 handler 执行 |
| `internal/worker/handlers` | 各 job 类型的执行逻辑：`app_initialize` / `app_health_check` / `app_runtime_ops` / `channel_login` / `knowledge_sync` / `newapi_key_status` / `runtime_refresh_status` |
| `internal/integrations` | 外部系统 HTTP client 封装：`agent`（docker proxy + file client + scope client）/ `newapi`（渠道 / 计费）/ `hermes`（配置渲染）/ `channel`（微信扫码）/ `runtime`（节点层抽象）/ `httpclient`（基础 HTTP 工具） |
| `internal/runtime/imagesync` | 把宿主 `hermes-runtime` 镜像 `docker save` 后流给 agent `docker load`，以 Docker ImageID 为对账锚点 |
| `internal/auth` | JWT 签发与校验 / CSRF token / 密码哈希 / 密钥加密（AES-256-GCM） |
| `internal/files` | 路径沙箱（`SafeRoot`），防止节点文件 API 路径遍历越权 |
| `internal/redis` | Redis 客户端封装与队列（Stream-based job 信号） |
| `internal/log` | 结构化日志（slog 封装）与日志脱敏（`redact`） |

权限校验规则（`platform_admin / org_admin / org_member` 三层）全部集中在
`internal/auth/authorizer.go`；service 层不再定义局部 `canX` 函数。

### 2.2 manager 前端（web/src/）

```text
web/src/
├── main.ts             createApp + Pinia + vue-router + Naive UI 初始化
├── App.vue             根组件，路由出口
├── app/
│   └── router.ts       路由表 + 鉴权守卫（token 校验 / fetchCurrentUser）
├── pages/
│   ├── login/          登录页
│   ├── dashboard/      角色感知首页
│   ├── platform/       平台总览、组织管理、充值
│   ├── org/            成员管理、创建成员、人设配置
│   ├── apps/           应用列表 + 应用详情（概览 / 运行时 / 渠道 / 知识库 / 工作目录 / 审计 六个 tab）
│   ├── runtime-nodes/  运行节点列表与节点详情
│   ├── knowledge/      组织级知识库
│   ├── usage/          token 用量看板
│   └── audit/          审计日志
├── api/
│   ├── client.ts       Axios 实例，拦截器，自动刷新 token
│   ├── index.ts        全部 endpoint 函数封装
│   └── generated.ts    由 make web-types-gen 自动生成，禁止手动编辑
├── stores/             Pinia store（auth 状态、当前用户、权限）
├── components/         通用组件（状态标签、确认弹窗、资源趋势图等）
├── layouts/
│   ├── AuthLayout.vue      登录页布局
│   └── DashboardLayout.vue 左侧导航 + 顶部用户菜单
└── domain/             与后端枚举和状态机镜像（权限谓词、状态常量）
```

数据请求统一走 TanStack Query；写操作经 Naive UI confirm modal 二次确认，高风险
操作（如删除组织）通过 `ConfirmActionModal` 要求输入名称校验。

### 2.3 runtime-agent（runtime/agent/）

`oc-runtime-agent` 是部署在每个 Runtime Node 上的常驻进程，源码位于
`runtime/agent/`。它暴露两个 HTTP 端口，均使用自签 TLS + Bearer token 鉴权：

| 端口 | 路由前缀 | 用途 |
|---|---|---|
| 7001 | `/v1/docker/*` | Docker daemon 反向代理（manager 通过 Docker Go SDK 调用） |
| 7002 | `/v1/scopes/*`、`/v1/files/ping`、`/healthz` | 文件管理、scope 管理（.hermes 目录读写）、健康检查 |

其他职责：

- **自动注册（enroll）**：agent 启动时用 `manager.enrollment_secret` 调
  `POST /api/v1/agent/enroll`，manager 按 `agent_id` 幂等创建或刷新
  `runtime_nodes` 记录，返回 `node_id` 与长效 `agent_token`。
- **心跳**：默认每 30 秒向 manager 发送心跳；manager 超过 90 秒未收到心跳则
  将节点标记为 `unreachable`，下次心跳恢复后自动翻回 `active`。
- **节点资源采集**：heartbeat 附带 CPU / 内存 / 磁盘使用率，由 manager 持久化
  为资源时序样本。

### 2.4 Hermes 容器（runtime/hermes/）

`runtime/hermes/` 是 Hermes 容器镜像的构建上下文，包含 `Dockerfile`、`scripts/`
和 `CONTRACT.md`（挂载约定）。每个应用对应节点上的一个 Hermes 容器，由
`app_initialize` job 通过 agent docker proxy 创建。

挂载约定：节点本地 `{data_root}/apps/<app_id>/.hermes/` 整目录
bind mount 到容器 `/opt/data`（Hermes 主目录）；容器启动工作目录为
`/opt/data/workspace`。详细约定见 `runtime/hermes/CONTRACT.md`，Hermes 容器机制
专题见 `docs/hermes-container.md`。

## 3. 数据持久化划分

| 存储 | 内容 | 备份策略 |
|---|---|---|
| PostgreSQL | 业务库（组织 / 成员 / 应用 / 节点 / 知识库元数据）/ job 队列状态 / 审计日志 / 资源指标样本 | 见 `deploy/operations.md` |
| Redis | job 信号队列（Stream）/ 短期锁 / 无需长期保存的状态 | 不需要长期备份；重启后 scheduler 从 PostgreSQL jobs 表重建队列 |
| agent 文件系统 | `{data_root}/apps/<id>/.hermes/`（挂载到 Hermes `/opt/data`）/ 组织与应用知识库同步副本（`{data_root}/orgs/<org_id>/...`、`{data_root}/apps/<app_id>/...`） | 节点本地磁盘，建议定期快照；参考 `docs/hermes-container.md` |
| manager 文件系统 | 知识库主副本（`{manager.knowledge_root}/...`） | 与 PostgreSQL 同步备份；主副本丢失则所有节点的知识库同步将失效 |
| Hermes 镜像 | skills 内置库 / Hermes bin | 由 `runtime/hermes/` 构建产物决定；通过 `make build-hermes-runtime` 构建后由 `imagesync` 分发到节点 |

## 4. 关键数据流

### 4.1 成员 onboarding（注册 → 容器就绪）

```text
组织管理员 POST /api/v1/members
       │
       ▼
MembersHandler.Create
       │
       ▼
MemberOnboardingService.OnboardMember
   ├─ 事务内：
   │    ├─ CreateUser
   │    ├─ NodeSelector 选剩余容量最大的 active 节点
   │    ├─ CreateApp（owner_user_id 唯一索引保证 1:1）
   │    ├─ CreateChannelBinding（status=unbound 占位）
   │    ├─ CreateAuditLog
   │    └─ CreateJob(type=app_initialize)
   └─ JobNotifier 发送 Redis 信号，返回 201

                ▼（异步）
worker app_initialize handler：
   ├─ ImageDistributionService 把 hermes-runtime 镜像 push 到目标节点
   ├─ 上传 .hermes/ 配置文件到节点（SOUL.md / persona / knowledge skills）
   ├─ DockerProxy 创建容器（单一 bind mount：.hermes/ → /opt/data）
   ├─ new-api 创建用户 + api_key（sk- 写入 Hermes 配置）
   ├─ 启动容器
   └─ UPDATE apps SET status='running'
```

### 4.2 知识库同步（manager 主副本 → 节点）

```text
上传文件：PUT /api/v1/orgs/{orgId}/knowledge?path=foo/bar.pdf
       │
       ▼
KnowledgeHandler → KnowledgeService.PutOrgFile
   ├─ files.SafeRoot 沙箱化路径
   ├─ 写本地主副本（manager knowledge_root）
   ├─ 写 audit
   ├─ 列出该组织所有 active 应用所在节点
   └─ 为每个节点入队 knowledge_sync_node job

                ▼（异步）
worker knowledge_sync_node handler：
   ├─ 读主副本，向 agent :7002 /v1/scopes/orgs/<id>/knowledge PUT（tar 流）
   ├─ 写 knowledge_sync_status（per node：synced / failed / pending）
   └─ 失败按指数退避重试；超过 max_attempts 标记 failed
```

### 4.3 容器生命周期

```text
app_initialize job   → 创建容器 + 启动（见 §4.1）
app_health_check job → 检测 Hermes 健康探针；被停掉的容器自动拉起
app restart          → RuntimeOperationService.Restart
                         ├─ ContainerStop / ContainerStart（经 agent :7001）
                         ├─ 清空 Hermes session（让新配置进入对话）
                         └─ 写 audit
```

`app_health_check` 由 scheduler 定期触发，负责检测容器状态并在容器异常停止时
自动拉起，实现基础的自愈能力。

### 4.4 token 用量直查 new-api

```text
UI 用量看板 GET /api/v1/usage/...
   │
   ▼
UsageHandler → UsageService
   └─ 直接调 new-api HTTP API（按平台 / 组织 / 应用三级聚合）
      不缓存；每次查询实时透传，保证数据一致性
```

## 5. 跨模块约束

### 5.1 权限校验

所有 `Can*` 权限谓词（`platform_admin / org_admin / org_member` 三层判断）
集中定义在 `internal/auth/authorizer.go`。service 包不再定义本地 `canX` 函数；
handler 或 service 内不允许内联 `if principal.Role == "..."` 判断。

新增权限规则时，优先扩展现有 `Can*` 函数；如确需新增函数，提交时需说明设计取舍。

### 5.2 OpenAPI 同步

API 契约通过三步链保持同步：

```text
handler 函数 swag 注解
       │  make openapi-gen
       ▼
openapi/openapi.yaml
       │  make web-types-gen
       ▼
web/src/api/generated.ts
```

修改 handler 签名 / 请求体 / 响应类型 / 路由后，必须同步运行
`make openapi-gen` + `make web-types-gen`，将生成产物与代码变更一同提交。
`make openapi-check` 可在本地验证 yaml 是否与代码对齐（工作区应保持干净）。
`openapi/openapi.yaml` 与 `web/src/api/generated.ts` 均为生成产物，禁止手动编辑。

### 5.3 镜像同步

`internal/runtime/imagesync` 负责把 manager 宿主上的 `hermes-runtime` 镜像分发
到各 Runtime Node。同步时以 Docker ImageID 为对账锚点：如果节点本地镜像
ImageID 与 manager 侧一致，则跳过传输，避免重复推送大镜像。
