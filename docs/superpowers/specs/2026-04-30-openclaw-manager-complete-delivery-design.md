# OpenClaw Manager 完整交付设计

日期：2026-04-30  
版本：v1（覆盖至最终可交付）  
关联文档：[openclaw-manager-design.md](../../openclaw-manager-design.md) · [openclaw-manager-technical-design.md](../../openclaw-manager-technical-design.md)

## 0. 文档定位

本文是 OpenClaw Manager 从当前实现到“可对外交付”的**完整设计**：覆盖容器闭环、治理与运维、发布工程三段。每段对应一段实施周期，但彼此共用同一份架构、数据模型、API 表面与质量标准。

## 1. 当前基线

提交快照 `c7f46a5` 起，已落地：

- 后端：auth、organization、member、onboarding、audit、runtime node 注册/心跳、agent file client、镜像分发、Redis 队列与 job 状态机、worker/scheduler 框架（Tick 函数）、app 状态机、new-api client（基础三方法）、prompt 渲染、app_initialize handler 的非容器步骤、channel registry + 微信协议解析、knowledge service、workspace service（未挂路由）、usage service、runtime operation service、app service、apps handler。
- 前端：login/dashboard 占位、Organizations、Members、CreateMember、AuditLogs、Apps（list）、RuntimeNodes（list+detail）、OrgKnowledge、AppWorkspaceTab、AppChannelsTab、Pinia auth store、TanStack Query hooks、共用组件库（StatusTag/Confirm/Toolbar/JobProgress/AuthChallengeRenderer）。
- 测试：所有现有 service/adapter/前端有单元测试覆盖，`go test ./...` 与 `vitest --run` 全绿。
- 文档：本地开发指南、阶段验证报告。

仍缺的功能/质量见第 4 节按 Phase 拆分；本设计在统一的架构下按顺序落地。

## 2. 总体目标

最终可交付状态：

1. 平台/组织/成员三类用户走完“创建 → 初始化 → 绑定 → 使用 → 删除”全链路无人为干预；
2. 平台管理员可完成组织充值、风控（禁/启用 api_key）、节点心跳异常监控；
3. 组织管理员可设置 AI 人设、查看节点级知识库同步状态、按维度查看用量；
4. 节点 agent 通过 Docker 代理 + 文件 API 完成全部容器与文件操作，manager 不直接接触 docker.sock 或节点文件系统；
5. 全部敏感字段加密入库；agent token、master_key、JWT secret 走环境变量；
6. 集成测试覆盖 PostgreSQL 迁移、sqlc、Redis 队列与刷新令牌生命周期；Playwright 跑通至少 6 个 E2E 场景；
7. OpenAPI 文档生成 TypeScript client，前端调用类型对齐；
8. docker compose 与 Kubernetes manifest 双套部署样例，并附部署/运维文档。

## 3. 架构

```
                        ┌──────────────────┐
Browser ──HTTPS──►  manager-web (Vite/SPA)
                        └──────────────────┘
                                 │ /api/v1/*
                                 ▼
┌──────────────────────────────────────────────────────────┐
│ manager-api (单 Go 进程)                                 │
│  ├ HTTP server (Gin)                                     │
│  ├ worker pool × N goroutine    ◄── Redis ZSET (信号)   │
│  ├ scheduler × 1 goroutine      ──► PostgreSQL pending  │
│  └ background jobs (周期性 reconcile)                    │
└────┬───────────────┬───────────────┬─────────────┬──────┘
     │               │               │             │
   pgxpool        go-redis        new-api      Docker SDK over HTTPS+Bearer
     ▼               ▼               ▼             ▼
 PostgreSQL       Redis         new-api      oc-runtime-agent (节点)
                                                ├ /v1/docker/*  (透传 docker.sock)
                                                ├ /v1/files/*   (沙箱)
                                                └ /healthz
```

**关键边界**：
- manager 永远不直接持有节点 docker.sock 或节点文件系统访问权；
- PostgreSQL 是 jobs 与业务状态唯一事实来源，Redis 仅作信号通道；
- 加密原语集中在 `internal/auth/crypto.go`，避免散落多处实现；
- agent 与 manager 双向认证：agent 端 Bearer agent_token 防伪 manager；manager 端 self-signed CA 防伪 agent；
- 进程入口 `cmd/server` 用 `errgroup.Group` 并发 HTTP server + worker pool + scheduler，SIGTERM 取消 root ctx → 30s 内全部退出。

## 4. 实施分段

| 段 | 主题 | 交付的可演示能力 |
|---|---|---|
| Phase A | 容器与渠道闭环 | 创建成员 → 应用容器跑起 → 微信扫码 → 工作目录有产物；启停删除可用 |
| Phase B | 治理与运维 | 充值、人设、节点心跳超时、知识库节点同步、风控禁 key、多维度用量、三类角色完整页面 |
| Phase C | 发布工程 | 集成测试、E2E、OpenAPI client 生成、部署文档与 hardening、可对外交付 |

实施顺序固定为 A → B → C；B 的部分页面（充值、人设）依赖 A 的容器能力跑通才有展示价值。

---

## 5. Phase A：容器与渠道闭环

### A1 · Docker proxy + 自签 TLS + Bearer transport

**agent 端（`runtime/agent/`）**：

- 新增 `proxy.go`：`httputil.ReverseProxy` 把 `/v1/docker/*` 请求 path 重写为 `/<rest>` 后转发到 `unix:///var/run/docker.sock`
- 中间件：`Authorization: Bearer <agent_token>` 校验；可选环境变量 `AGENT_TRUSTED_CIDR` 限制源 IP
- TLS：启动期生成或加载 `state/agent-tls.{crt,key,ca.crt}`；CA 在 register 时上报 manager
- 入口 main.go：`:7001` (docker proxy, TLS 强制)、`:7002` (file API)、目录扩展 `/var/lib/oc-agent/{orgs,apps,archived}/`

**manager 端（`internal/integrations/agent/`）**：

- `docker_proxy.go`：`NewDockerClientForNode(node sqlc.RuntimeNode, agentToken string) (*client.Client, error)` —— 用 `agent_tls_ca_cert` 构造 RootCAs；自定义 RoundTripper 注入 Bearer；`client.NewClientWithOpts(WithHost, WithHTTPClient)`
- `agent_token_resolver.go`：进程内 `agentTokenCache map[nodeID]string`，由 register handler 在响应里写入；进程重启需重新 rotate-bootstrap（**A 阶段已知妥协**，B 阶段会替换为加密入库）

### A2 · AgentBackedAdapter 容器 ops + worker handlers

替换 `internal/integrations/runtime/agent_backed.go` 6 个 `ErrUnimplemented`，命名 `ocm-{app_id}`，挂载/环境变量按 design.md §11.2：

| 节点路径 | 容器路径 | 模式 | env |
|---|---|---|---|
| `{node_data_root}/apps/{app_id}/workspace` | `/workspace` | rw | `OPENCLAW_WORKSPACE_DIR=/workspace` |
| `{node_data_root}/orgs/{org_id}/knowledge` | `/knowledge/org` | ro | `OPENCLAW_KNOWLEDGE_ORG_DIR=/knowledge/org` |
| `{node_data_root}/apps/{app_id}/knowledge` | `/knowledge/app` | ro | `OPENCLAW_KNOWLEDGE_APP_DIR=/knowledge/app` |
| `{node_data_root}/apps/{app_id}/state` | `/state` | rw | — |
| `{node_data_root}/apps/{app_id}/logs` | `/logs` | rw | — |

env 还包括 `OPENCLAW_API_BASE` / `OPENCLAW_API_KEY`（解密后的 plaintext）/ `OPENCLAW_SYSTEM_PROMPT` / `OPENCLAW_CHANNEL_PLUGIN`。

新增 worker handler：

- `app_start_container` / `app_stop_container` / `app_restart_container`：lookup app → 调 adapter → 写审计 → 状态推进；幂等
- `app_delete`：Stop → Remove → `newapi.SetAPIKeyStatus(disable)` → `AgentFileClient.ArchiveApp` → 删 manager 主副本 → `apps.SoftDeleteApp`
- 扩展现有 `app_initialize`：在 prompt 渲染后真正调 `RuntimeAdapter.CreateContainer/InspectContainer`，写 container_id/name 入库

### A3 · WeChat CommandRunner

- `internal/integrations/channel/wechat_runner.go`：`DockerCommandRunner.StreamWeChatLogin(ctx, input)` 通过 adapter 拿 docker client → `ContainerExecCreate({Cmd: ["openclaw","channels","login","--channel","openclaw-weixin","--json"]})` + `ContainerExecAttach`
- 输出按行写入 `chan string`，stderr 合并；ctx 超时 30s
- cmd/server 启动期把 runner 注入 `WeChatAdapter` 后注册到 `channel.Registry`

### A4 · master_key 加密 + system_prompt_template 配置

**配置**（`internal/config`）：

```yaml
security:
  master_key: "${MASTER_KEY}"   # base64(32 字节)
openclaw:
  runtime_image: "openclaw-runtime:dev"
  system_prompt_template: |
    你是 OpenClaw 智能助手。
    生成文件请输出到 {{workspace_dir}} ...
    组织级知识库：{{knowledge_org_dir}} (只读)
    应用级知识库：{{knowledge_app_dir}} (只读)
```

启动期 fail-fast：master_key 解码必须 32 字节；template 必含 `{{workspace_dir}}` / `{{knowledge_org_dir}}` / `{{knowledge_app_dir}}`。

**加密原语**（`internal/auth/crypto.go`）：

```go
type Cipher struct{ aead cipher.AEAD }
func NewCipher(masterKey []byte) (*Cipher, error)        // AES-256-GCM
func (c *Cipher) Encrypt(plaintext []byte) (string, error) // base64(nonce||ct||tag)
func (c *Cipher) Decrypt(token string) ([]byte, error)
```

cmd/server 装配：`auth.NewCipher(decodedKey)` → 注入到 `AppInitializeConfig{Cipher, PlatformPrompt}`。app_initialize worker 用 `Cipher.Encrypt` 写 `newapi_key_ciphertext`；启动容器时 `Cipher.Decrypt` 现解，**不打日志**。

### A5 · worker/scheduler 启动循环

`internal/worker/runner.go`：

```go
type Pool struct { worker *Worker; concurrency int; interval time.Duration }
func (p *Pool) Run(ctx context.Context) error  // 启动 N 个 goroutine
```

`internal/scheduler/runner.go`：

```go
type Loop struct { scheduler *Scheduler; interval time.Duration }
func (l *Loop) Run(ctx context.Context) error  // ticker 触发 Tick
```

cmd/server：`errgroup` 并发 `httpServer.ListenAndServe / pool.Run / loop.Run`；signal handler 取消 root ctx；每个 worker `defer recover` 防 panic 拖死整个进程。

### A6 · App 详情聚合页 + 初始化重试

**后端**：

- `POST /api/v1/apps/:appId/initialize`：仅 `status ∈ {error, draft}` 时把 `status=draft`、`api_key_status=pending` 后入队 `app_initialize`
- `GET /api/v1/apps/:appId/runtime`：透传 `RuntimeAdapter.InspectContainer`

**前端**：

```
/apps/:appId
  ├ overview     AppOverviewTab.vue   (状态 / 容器 / api_key / 最近 job / 重试初始化)
  ├ channels     (复用 AppChannelsTab.vue)
  ├ knowledge    AppKnowledgeTab.vue
  ├ workspace    (复用 AppWorkspaceTab.vue)
  └ runtime      AppRuntimeTab.vue    (Inspect 透传 + 启停按钮)
```

`AppDetailLayout.vue` 通过 `provide/inject` 把 `app: Ref<AppDTO>` 暴露给子 tab，避免重复请求。

### A 阶段已知妥协

| 项 | A 阶段 | 衔接 |
|---|---|---|
| agent_token 持久化 | 内存 cache | B6 加密入库 |
| 节点心跳超时 reconciler | 仅被动（Docker 调用失败计数） | B5 主动 reconcile |
| 容器健康检查 | 创建后单次 Inspect | B5 周期 `app_health_check` |
| 知识库 tar 流批量同步 | UploadFile 单文件循环 | B4 `SyncOrgKnowledge` tar |

---

## 6. Phase B：治理与运维

### B1 · 充值

**newapi adapter 扩展**：

```go
CreateOrBindOrganization(ctx, input) (NewAPIUser, error)
RechargeUser(ctx, newapiUserID, creditAmount, remark) (RechargeResult, error)
GetUserBalance(ctx, newapiUserID) (BalanceResult, error)
```

**service**：`internal/service/recharge_service.go`

- `Recharge(principal, orgID, amount, remark)`：仅 `platform_admin` 可调，调 `newapi.RechargeUser`，成功后写 `recharge_records`（已存在 sqlc query）；失败不写入
- `ListRecharges(principal, orgID, limit, offset)`

**handler + 路由**：

- `POST /api/v1/organizations/:orgId/recharge`
- `GET /api/v1/organizations/:orgId/recharges`

**前端**：平台路由 `/platform/organizations/:orgId/recharge`，列表 + 表单 + 余额（`GetUserBalance`）；UI 复用 `DataTableToolbar` + `ConfirmActionModal`。

### B2 · 组织 AI 人设

**service**：`internal/service/persona_service.go` 维护 `organization_personas` 版本表

- `GetCurrent(ctx, principal, orgID)` → 取 `version = MAX` 行
- `Replace(ctx, principal, orgID, input)` → 写 `version+1` 新行；只有 `org_admin`/`platform_admin` 可写
- 字段：`system_prompt`、`conversation_rules`、`forbidden_rules`、`reply_style`、`allow_member_override`

**集成**：`app_initialize` worker 渲染 prompt 时把当前生效的 organization_personas 注入第二层；如果应用 `persona_mode=app_override` 再叠加第三层（已实现的 `openclaw.Render` 支持三层拼接）。

**handler + 路由**：

- `GET /api/v1/orgs/:orgId/persona`
- `PUT /api/v1/orgs/:orgId/persona`

**前端**：`/org/persona` 页面，Naive UI textarea + checkbox（allow_member_override）；保存后给出“将于下次容器重建生效”提示。

### B3 · 成员删除联动应用软删

- `member_service.DeleteMember(principal, userID)`：事务里 `users.status='disabled'` + `apps.SoftDeleteApp`（如果存在）+ 入队 `app_delete` job
- `DELETE /api/v1/members/:userId` handler
- 前端 MembersPage 的“禁用”按钮旁加“删除”入口，走 `ConfirmActionModal` 二次确认

### B4 · 知识库节点同步

**job**：`knowledge_sync_node`（payload `{org_id|app_id, node_id, change_type:"upload_file"|"delete_file"|"full_sync", rel_path?}`）

- worker handler 调 `AgentFileClient.SyncOrgKnowledge`（tar 全量）或 `Upload/Delete` 单文件
- 失败按 `max_attempts` + 指数退避

**AgentFileClient 扩展**：

```go
SyncOrgKnowledge(ctx, nodeID, orgID, tarStream) error
SyncAppKnowledge(ctx, nodeID, appID, tarStream) error
```

agent 侧新增 `POST /v1/files/sync-tar?scope=orgs/{org_id}/knowledge`：清空目标目录后 `tar -xf` 解压。

**knowledge_service** 扩展：

- 写组织级文件后查“该组织所有未删除应用所在节点”，去重，每个节点入队一个 `knowledge_sync_node` job
- 应用级保持现有同步路径（直接调 agent，不经 job）

**API**：`GET /api/v1/orgs/:orgId/knowledge/sync-status` 返回 `{node_id, last_synced_at, last_error, status}` 列表

**前端**：OrgKnowledgePage 顶部加节点同步徽标矩阵（每个节点一个 pill：success/syncing/failed），失败可点“重试”重新入队。

### B5 · 周期任务

scheduler `Loop` 在 ticker 上挂多个 reconciler，使用配置项分别控制频率：

| job_type | 频率（默认） | 行为 |
|---|---|---|
| `runtime_node_health_reconcile` | 30s | `last_heartbeat_at < now() - 3*interval` 的 active 节点 → `unreachable`，并把该节点上 `running` app 置 `error` |
| `runtime_refresh_status` | 60s | 拉取节点上所有 ocm-* 容器的 inspect 信息，回写 `apps.container_id/status` |
| `app_health_check` | 120s | 对每个 `running` app exec OpenClaw 健康命令，失败累计 3 次置 `error` |
| `workspace_archive_cleanup` | 1h | 调 `AgentFileClient.CleanupArchive(retentionDays)` |

每个 reconciler 写成独立 worker handler；scheduler 只负责定时入队。

### B6 · 多维度用量 + 完整三类角色页面 + agent_token 加密

**Usage**：

- `usage_service` 增加 `GetMemberUsage(principal, memberID, range)`、`GetOrgUsage(principal, orgID, range)`、`GetPlatformUsage(principal, range)`
- 实现：聚合该范围内所有 api_key id → 调 `newapi.GetAPIKeyUsage` 汇总；范围按 `start_at`/`end_at` query 参数，5 秒进程内 cache 缓解突发查询
- handler：`GET /api/v1/usage/{members,organizations,platform}/...`

**新前端页面**：

| 路径 | 页面 | 内容 |
|---|---|---|
| `/platform` | `PlatformOverview` | 组织数 / 应用数 / 节点状态汇总 / 总用量趋势 |
| `/platform/usage` | `PlatformUsagePage` | 组织维度排行 + 时间范围筛选 |
| `/platform/admins` | `AdminsPage` | 平台管理员账号 CRUD（复用 member service，role=platform_admin） |
| `/org` | `OrgOverview` | 余额 / 预警 / 成员数 / 应用数 / 异常应用 / 用量趋势 |
| `/org/usage` | `OrgUsagePage` | 成员维度排行 + 应用维度排行 |
| `/me` | `MyOverview` | 自己的应用列表 + 状态 + 用量摘要 |
| `/me/settings` | `MySettingsPage` | 改密码、显示名 |
| `/apps/:appId/knowledge` | `AppKnowledgeTab` | 应用级知识库 CRUD（B 阶段从 A6 占位升级到完整功能） |
| `/apps/:appId/logs` | `AppLogsTab` | Docker logs 拉取（adapter `Logs` 方法 = `cli.ContainerLogs(stdout/stderr, tail=N)`） |
| `/apps/:appId/runtime` | `AppRuntimeTab` 升级 | A6 的基础上加 stats 折线（`cli.ContainerStats(stream=false)` 单点） |

**agent_token 加密入库**：

- 迁移 `000003_agent_token_ciphertext.up.sql`：`runtime_nodes` 增列 `agent_token_ciphertext text`
- register handler：成功后 `Cipher.Encrypt(agentToken)` 写入；同时保留 `agent_token_hash` 给鉴权用
- `agent_token_resolver` 改为先查内存 cache，miss 则从 `agent_token_ciphertext` 解密填回；进程重启不需重新 rotate

---

## 7. Phase C：发布工程

### C1 · 集成测试套件

- `internal/store/integration_test.go`：Docker compose 起 PostgreSQL，跑 migration up/down + 关键 sqlc query 端到端（CreateOrganization → CreateApp → SoftDeleteApp，断言唯一索引、外键、审计触发）
- `internal/redis/integration_test.go`：起 Redis，验证 ZSET 入队/出队/延迟到期/丢失恢复
- `internal/auth/integration_test.go`：refresh token 完整生命周期（签发 → 撤销 → 复用拒绝 → 过期清理）
- `internal/integrations/newapi/integration_test.go`：用 httptest + record/replay fixtures 验证全量 client 方法
- 测试入口 `make integration-test`，CI 中起独立 docker network

### C2 · Playwright E2E

`web/tests/e2e/`：

| 场景 | 步骤 |
|---|---|
| 登录 | 平台管理员账号登录 → 跳 dashboard |
| 创建组织+节点 | 创建组织 → 注册节点 → 复制 bootstrap → 触发 agent 注册 mock |
| 成员联动应用 | 组织管理员创建成员 → 看应用 status 推到 binding_waiting |
| 微信扫码 | 触发 channel login → 看到二维码 → 模拟 bound 事件 → status 推到 running |
| 知识库 | 上传 → 列表 → 删除 → 应用工作目录 list/download |
| 删除 | 删除应用 → status=deleted；删除成员 → 应用联动软删 |

E2E 用 mock newapi + mock OpenClaw runtime（提供 stub 二维码 / bound 事件）。

### C3 · chrome-devtools MCP 可视化验收

- 解决宿主 Chrome profile 占用：在 CI 用 `--user-data-dir=/tmp/playwright-mcp` 隔离
- 录制每个 sub-project 关键路径的 DOM snapshot + 截图，归档到 `docs/verification-report.md`

### C4 · OpenAPI client 生成 + 前端类型对齐

- `make openapi-generate` 用 `oapi-codegen` 生成 `internal/api/openapi.gen.go`（server stub）和 `web/src/api/generated/`（client）
- 前端 hooks 切到 typed client：`useOrganizationsQuery` 等改为复用生成的方法签名
- CI 增加 step：先 `go run ./cmd/openapi-dump` 输出当前 OpenAPI yaml，diff 仓库版本，不一致即失败

### C5 · 部署工程 + 安全 hardening

- **部署**：
  - `deploy/docker-compose.prod.yml` 完整生产 compose（带 nginx 终止 TLS、external 网络、健康检查、`restart: always`、资源限制）
  - `deploy/k8s/`：Helm chart 或 raw manifests（manager Deployment + worker Deployment 分离选项、PostgreSQL StatefulSet、Redis、Ingress、Secret）
  - `runtime/openclaw` 与 `runtime/agent` 镜像 multi-arch (amd64/arm64) build script
- **安全 hardening**：
  - CORS 白名单（`app.public_base_url`）
  - CSRF token middleware（仅 cookie 模式启用）
  - rate limit middleware（基于 Redis 计数，按 IP+endpoint）
  - 敏感字段日志脱敏（`auth.MaskSecret` helper，覆盖 api_key、agent_token、bootstrap_token）
  - `.env.example` 列出所有必需环境变量，部署文档强制要求
  - master_key 轮换流程文档（双密钥并存方案）
- **可观测性**：
  - 结构化日志（slog + JSON 输出）
  - Prometheus metrics endpoint `/metrics`：worker job 处理速率、jobs 表队列深度、Redis 队列大小、HTTP 请求 P50/P99
  - 关键告警阈值文档
- **备份**：
  - PostgreSQL 备份 cron 样例（pg_dump → S3 兼容存储）
  - 知识库主副本 rsync 到副站点的脚本样例

---

## 8. 数据模型变更总览

| 阶段 | 迁移 | 内容 |
|---|---|---|
| 现有 | `000001_init` + `000002_core_schema` | 已落地 |
| A | 无 | 仅复用 |
| B6 | `000003_agent_token_ciphertext.up.sql` | `runtime_nodes` 增 `agent_token_ciphertext text` |
| B (可选) | `000004_persona_audit.up.sql` | 如需要扩展人设审计字段；当前 schema 已足 |

迁移每次 up/down 必须可逆，`make migrate-down` 在 CI 验证。

## 9. API 表面（最终版）

```
POST   /api/v1/auth/login
POST   /api/v1/auth/refresh
POST   /api/v1/auth/logout
GET    /api/v1/auth/me

GET    /api/v1/organizations
POST   /api/v1/organizations
GET    /api/v1/organizations/{orgId}
PATCH  /api/v1/organizations/{orgId}
POST   /api/v1/organizations/{orgId}/{enable,disable}
POST   /api/v1/organizations/{orgId}/recharge        # B1
GET    /api/v1/organizations/{orgId}/recharges       # B1

GET    /api/v1/orgs/{orgId}/persona                  # B2
PUT    /api/v1/orgs/{orgId}/persona                  # B2

POST   /api/v1/organizations/{orgId}/members/onboard # 现有
GET    /api/v1/organizations/{orgId}/members
POST   /api/v1/organizations/{orgId}/members
GET    /api/v1/members/{userId}
PATCH  /api/v1/members/{userId}
POST   /api/v1/members/{userId}/{enable,disable}
POST   /api/v1/members/{userId}/password
DELETE /api/v1/members/{userId}                      # B3

GET    /api/v1/organizations/{orgId}/apps
GET    /api/v1/apps/{appId}
POST   /api/v1/apps/{appId}/initialize               # A6
GET    /api/v1/apps/{appId}/runtime                  # A6
GET    /api/v1/apps/{appId}/logs                     # B6
POST   /api/v1/apps/{appId}/runtime/{start,stop,restart,delete}  # 现有 + A2 真实生效
GET    /api/v1/apps/{appId}/usage

POST   /api/v1/apps/{appId}/channels/{type}/auth     # 现有，begin
GET    /api/v1/apps/{appId}/channels/{type}/auth     # 现有，poll
POST   /api/v1/apps/{appId}/channels/{type}/unbind

GET    /api/v1/apps/{appId}/workspace                # A 阶段挂路由
GET    /api/v1/apps/{appId}/workspace/file
GET    /api/v1/apps/{appId}/workspace/archive

GET    /api/v1/organizations/{orgId}/knowledge
POST   /api/v1/organizations/{orgId}/knowledge
DELETE /api/v1/organizations/{orgId}/knowledge
GET    /api/v1/organizations/{orgId}/knowledge/sync-status   # B4
GET    /api/v1/apps/{appId}/knowledge
POST   /api/v1/apps/{appId}/knowledge
DELETE /api/v1/apps/{appId}/knowledge

GET    /api/v1/runtime-nodes
POST   /api/v1/runtime-nodes
GET    /api/v1/runtime-nodes/{nodeId}
POST   /api/v1/runtime-nodes/{nodeId}/{rotate-bootstrap,enable,disable}
DELETE /api/v1/runtime-nodes/{nodeId}                # B 阶段加

POST   /api/v1/agent/register
POST   /api/v1/agent/heartbeat

GET    /api/v1/jobs/{jobId}

GET    /api/v1/usage/apps/{appId}                    # 现有
GET    /api/v1/usage/members/{memberId}              # B6
GET    /api/v1/usage/organizations/{orgId}           # B6
GET    /api/v1/usage/platform                        # B6

GET    /api/v1/audit-logs
GET    /api/v1/organizations/{orgId}/audit-logs
```

OpenAPI yaml 在每个 sub-phase 完成时同步更新；C4 阶段引入自动 diff 校验。

## 10. 测试策略

| 层 | 工具 | 覆盖 |
|---|---|---|
| 单元 | `go test` + `vitest` | service / adapter / handler / 状态机 / 加密 / runner |
| 集成 | `go test -tags=integration` | PostgreSQL migration、sqlc、Redis 队列、refresh token、newapi httptest |
| E2E | Playwright | 6 个核心场景（C2 列表） |
| 浏览器验收 | chrome-devtools MCP | 关键页面 DOM snapshot 归档 |
| 契约 | OpenAPI diff | 防 yaml 与 server 生成不一致 |
| 安全 | `go vet` + `staticcheck` + `npm audit` | 静态扫描 |

CI 矩阵：每次 push 跑单元 + 集成；nightly 跑 E2E + 浏览器验收。

## 11. 风险与对策

| 风险 | 概率 | 应对 |
|---|---|---|
| agent docker proxy 被滥用 | 中 | TLS + Bearer + 源 IP 白名单 + agent 端 audit 日志 |
| master_key 泄露 | 低 | 仅环境变量；K8s Secret/Vault；不入仓 |
| OpenClaw CLI 输出格式变化 | 中 | 协议固化为 JSON wrapper（已实现）；解析失败立即 failed，监控告警 |
| 节点心跳抖动误判 | 中 | `3 × heartbeat` 阈值 + agent 本地心跳重试；恢复时只回 active，不自动恢复应用 |
| 知识库节点同步漂移 | 中 | sync-status 暴露每节点状态 + 重试按钮；容器创建时 tar 全量同步兜底 |
| Redis 丢失 | 中 | jobs 表是事实来源；scheduler reconcile 重新入队 |
| Docker daemon 故障 | 低 | adapter 错误冒泡 → app `error`；运维通过 audit 日志定位节点 |
| 数据迁移失败 | 低 | 每个迁移 up/down 在 CI 验证；生产部署前先在 staging 跑 |

## 12. 验收清单

最终交付前必须满足：

- [ ] 全量 `make test` / `make web-test` / `make integration-test` 绿
- [ ] Playwright E2E 6 个场景全过
- [ ] OpenAPI 文档自动生成的 client 与代码一致（diff 校验通过）
- [ ] master_key 缺失/非法时 manager 启动失败，日志清晰
- [ ] 部署文档涵盖 docker compose + k8s 两种形态，至少一种在测试环境完整跑通
- [ ] 安全 hardening：CORS、CSRF、rate limit、日志脱敏、敏感字段加密四项落地
- [ ] 备份与监控样例脚本提供
- [ ] `docs/verification-report.md` 更新到最终版本，每个 acceptance 项都有命令/截图佐证
- [ ] `docs/local-development.md` 更新，包含三类角色完整使用流程
- [ ] commit 历史按 sub-phase 划分，每个 task 独立 commit，可追溯

## 13. 已知妥协与后续可演进项

- 第一版多节点 agent 调度依赖手工指定 `runtime_node_id`；自动调度留给后续版本
- 单 manager 进程承载 API + worker + scheduler；后续可拆为 API/Worker 分进程部署
- 不实现 NATS / WebSocket 推送，前端走 polling；后续可考虑 SSE
- master_key 轮换机制留文档，第一版不实现自动轮换
- 组织级公共知识库、邀请注册、细粒度 RBAC 留给 v2
