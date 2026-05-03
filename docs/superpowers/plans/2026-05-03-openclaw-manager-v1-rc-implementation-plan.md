# OpenClaw Manager v1.0 RC Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把当前 OpenClaw Manager 从已跑到 `binding_waiting + healthy` 的基线推进到可进入 UAT 的 v1.0 RC 商用候选版本。

**Architecture:** 计划按四个可验收 Chunk 推进：端到端业务闭环、组织治理、运维增强、商用 hardening。每个 Chunk 都保持后端 service/worker/adapter 与前端页面分层，跨系统副作用继续通过 job + worker 推进，浏览器验收统一使用 chrome-devtools MCP。

**Tech Stack:** Go、Gin、pgx、sqlc、PostgreSQL、Redis、Docker SDK via runtime agent、Vue 3、Vite、TypeScript、Naive UI、TanStack Query、Playwright、chrome-devtools MCP、OpenClaw runtime、new-api。

---

## Scope Check

v1.0 RC 覆盖多个子系统，不适合一次性无分界实施。本计划是总实施计划，按四个 Chunk 拆分，每个 Chunk 结束都必须有可运行软件、自动化检查和浏览器验证记录。

执行原则：

- Chunk 1 是关键路径，必须先完成。
- Chunk 2 和 Chunk 3 可在 Chunk 1 稳定后并行，但合入前必须分别跑完整回归。
- Chunk 4 必须最后做，因为 CSRF、错误响应和部署文档会影响所有已完成链路。
- 每个任务先写失败测试，再实现，再运行相关测试，最后提交。
- 所有涉及真实微信扫码或微信发消息的验证必须暂停并通知用户操作。

## File Structure

### 后端核心

- `internal/api/handlers/*`：HTTP 入参、认证上下文、响应封装。不得直接写 Docker/new-api/OpenClaw 业务逻辑。
- `internal/service/*`：权限、事务、状态机、审计、业务决策。
- `internal/worker/handlers/*`：跨系统副作用，负责调用 runtime/newapi/agent/channel adapter。
- `internal/scheduler/*`：周期性 job 创建与 pending 状态推进。
- `internal/integrations/runtime/*`：agent-backed Docker 操作。
- `internal/integrations/agent/*`：agent 文件 API client 与 docker proxy client。
- `internal/integrations/channel/*`：微信渠道登录、轮询、解绑与 plugin state 解析。
- `internal/integrations/newapi/*`：充值、api_key、用量、余额 API。
- `internal/files/*`：知识库主副本和安全路径处理。
- `migrations/*` 与 `internal/store/queries/*`：schema 和 sqlc 查询。

### Runtime Agent

- `runtime/agent/scopes.go`：文件 API scope、路径沙箱、workspace/knowledge/archive 操作。
- `runtime/agent/proxy.go`：Docker socket 代理。
- `runtime/agent/tls.go`：agent 自签 TLS。
- `runtime/openclaw/*`：OpenClaw 镜像、healthcheck、集成契约和 wrapper。

### 前端

- `web/src/api/hooks/*`：TanStack Query hooks。
- `web/src/pages/apps/*`：应用详情 Overview/Runtime/Channels/Knowledge/Workspace。
- `web/src/pages/knowledge/OrgKnowledgePage.vue`：组织级知识库。
- `web/src/pages/dashboard/*`：平台/组织/成员首页。
- `web/src/pages/platform/*`：组织、充值、跨组织平台视图。
- `web/src/pages/audit/AuditLogsPage.vue`：审计日志。
- `web/tests/e2e/*`：Playwright 核心场景。

### 文档和验证记录

- `docs/verification-report.md`：每个 Chunk 的最终验证结果。
- `docs/local-development.md`：本地验证命令更新。
- `deploy/README.md`：部署、备份、升级说明入口。
- `docs/superpowers/poc/*`：临时人工验证笔记；若目录被忽略，最终要把结论沉淀到 `docs/verification-report.md`。

## Global Verification Commands

每个 Chunk 完成前必须运行：

```bash
go test ./... -count=1
go vet ./...
go build ./...
```

```bash
cd web
npm run typecheck
npm test -- --run
npm run build
```

浏览器验证必须使用 chrome-devtools MCP：

- 登录页。
- 首页。
- 组织管理。
- Runtime Node 列表和详情。
- 成员创建。
- 应用列表。
- 应用详情 Overview / Runtime / Channels / Knowledge / Workspace。
- 组织知识库。
- 审计日志。

真实微信验证暂停协议：

```text
现在需要你用微信扫码验证应用绑定。
请打开当前页面显示的二维码完成扫码和确认。
完成后回复“已扫码”，我会继续检查 channel_bindings.status、bound_identity 和 apps.status。
```

```text
现在需要你给刚绑定的微信助手发送消息：
“请生成一个 hello.txt 到工作目录，内容为 OpenClaw workspace ok”
发送完成后回复“已发送”，我会继续检查 workspace 文件列表、下载文件内容和审计日志。
```

## Chunk 1: 端到端业务闭环

目标：平台创建组织和节点，组织管理员创建成员应用，应用初始化到 `binding_waiting`，用户扫码绑定后应用进入 `running`，OpenClaw 生成文件后可在 workspace 下载。

### Task 1: 固化当前 Sprint 1+2 回归基线

**Files:**
- Modify: `docs/verification-report.md`
- Inspect: `internal/worker/handlers/app_initialize.go`
- Inspect: `internal/worker/handlers/app_runtime_ops.go`
- Inspect: `runtime/agent/scopes.go`
- Inspect: `web/src/pages/apps/AppChannelsTab.vue`
- Inspect: `web/src/pages/apps/AppWorkspaceTab.vue`

- [ ] **Step 1: 运行后端全量检查**

Run:

```bash
go test ./... -count=1
go vet ./...
go build ./...
```

Expected: 全部通过。

- [ ] **Step 2: 运行前端全量检查**

Run:

```bash
cd web
npm run typecheck
npm test -- --run
npm run build
```

Expected: 全部通过。

- [ ] **Step 3: 启动 compose 基础服务**

Run:

```bash
docker compose up -d manager-postgres manager-redis manager-api manager-web oc-runtime-agent
go run ./cmd/migrate up
curl -fsS http://localhost:3001/healthz
```

Expected: `/healthz` 返回 200。

- [ ] **Step 4: 用 chrome-devtools MCP 验证主页面**

验证：登录、组织、Runtime Node、成员、应用详情 5 tab、审计日志无关键 console error。

- [ ] **Step 5: 更新验证报告**

在 `docs/verification-report.md` 增加 v1.0 RC Chunk 1 起始基线，记录命令结果和浏览器验证结果。

- [ ] **Step 6: Commit**

```bash
git add docs/verification-report.md
git commit -m "docs(verify): 记录 v1.0 RC 闭环基线"
```

### Task 2: 验证并补齐微信绑定 worker 链路

**Files:**
- Modify: `internal/service/channel_service.go`
- Modify: `internal/worker/handlers/registry.go`
- Modify: `internal/worker/handlers/channel_start_login.go`
- Modify: `internal/worker/handlers/channel_check_binding.go`
- Modify: `internal/worker/handlers/channel_start_login_test.go`
- Modify: `internal/worker/handlers/channel_check_binding_test.go`
- Modify: `internal/integrations/channel/wechat_runner.go`
- Modify: `internal/integrations/channel/wechat_identity.go`
- Modify: `internal/integrations/channel/wechat_identity_test.go`

- [ ] **Step 1: 写失败测试：开始绑定必须写入二维码 metadata**

测试点：

- `channel_start_login` job 调用 WeChatAdapter。
- `channel_bindings.status` 从 `unbound` 或 `failed` 变为 `pending_auth`。
- `metadata_json` 包含 `qr_image_base64` 或 `raw_qr`、`expires_at`。

Run:

```bash
go test ./internal/worker/handlers -run 'TestChannelStartLogin' -count=1
```

Expected: 实现不完整时失败。

- [ ] **Step 2: 实现最小修复**

保持 `ChannelService.BeginAuth` 只创建 job，不同步阻塞 OpenClaw CLI。worker 负责调用 adapter、解析 QR、更新 binding。

- [ ] **Step 3: 写失败测试：绑定成功必须推动应用 running**

测试点：

- `channel_check_binding` 识别 bound。
- 从 plugin state 读取 `userId` 写入 `bound_identity`。
- 应用状态从 `binding_waiting` 变为 `running`。

Run:

```bash
go test ./internal/worker/handlers -run 'TestChannelCheckBinding' -count=1
```

Expected: 实现不完整时失败。

- [ ] **Step 4: 实现最小修复**

复用 `wechat_identity.go` 的 plugin state 解析逻辑；不要把微信特有逻辑泄漏到 handler 外。

- [ ] **Step 5: 运行相关测试**

Run:

```bash
go test ./internal/service ./internal/worker/handlers ./internal/integrations/channel -count=1
```

Expected: 全部通过。

- [ ] **Step 6: Commit**

```bash
git add internal/service/channel_service.go internal/worker/handlers internal/integrations/channel
git commit -m "feat(channel): 完善微信绑定到 running 的 worker 链路"
```

### Task 3: 验证并补齐 AppChannelsTab 轮询和二维码渲染

**Files:**
- Modify: `web/src/api/hooks/useChannel.ts`
- Modify: `web/src/pages/apps/AppChannelsTab.vue`
- Modify: `web/src/components/AuthChallengeRenderer.vue`
- Test: `web/src/pages/apps/AppChannelsTab.test.ts` 或现有前端测试位置

- [ ] **Step 1: 写失败测试：QR challenge 渲染为图片**

测试点：

- hook 返回 `pending_auth` + QR payload。
- 页面显示二维码图片或 canvas。
- `bound` 后显示 `bound_identity`。

Run:

```bash
cd web
npm test -- --run AppChannelsTab
```

Expected: 测试先失败。

- [ ] **Step 2: 实现最小前端状态机**

状态：

- `unbound`：显示开始绑定。
- `pending_auth`：显示二维码和过期时间。
- `bound`：显示已绑定身份。
- `failed` / `expired`：显示失败原因和重试按钮。

- [ ] **Step 3: 运行前端检查**

Run:

```bash
cd web
npm run typecheck
npm test -- --run
```

Expected: 全部通过。

- [ ] **Step 4: chrome-devtools MCP 验证**

打开应用详情 Channels tab，点击开始绑定，确认 QR 区域可见，无 console error。

- [ ] **Step 5: Commit**

```bash
git add web/src/api/hooks/useChannel.ts web/src/pages/apps/AppChannelsTab.vue web/src/components/AuthChallengeRenderer.vue web/src/pages/apps/AppChannelsTab.test.ts
git commit -m "feat(web): 完善微信扫码绑定状态展示"
```

### Task 4: 验证并补齐应用级知识库同步推送

**Files:**
- Modify: `internal/service/knowledge_service.go`
- Modify: `internal/service/knowledge_service_test.go`
- Modify: `internal/api/handlers/knowledge.go`
- Modify: `web/src/api/hooks/useKnowledge.ts`
- Modify: `web/src/pages/apps/AppKnowledgeTab.vue`
- Test: `web/src/pages/apps/AppKnowledgeTab.test.ts` 或现有前端测试位置

- [ ] **Step 1: 写失败测试：应用级上传成功后必须调用 AgentFileClient**

测试点：

- 保存 manager 主副本。
- 查应用 runtime node。
- 调 `UploadAppKnowledgeFile`。
- agent 失败时回滚主副本并返回错误。

Run:

```bash
go test ./internal/service -run 'TestKnowledgeService.*App' -count=1
```

Expected: 缺同步推送时失败。

- [ ] **Step 2: 实现同步推送**

在 service 层注入最小接口：

```go
type AppNodeResolver interface {
    ResolveAppNode(ctx context.Context, appID string) (nodeID string, err error)
}
```

避免 handler 直接查询节点。

- [ ] **Step 3: 写前端失败测试：上传后刷新列表**

测试点：上传成功后 invalidate app knowledge query，删除成功后列表刷新。

- [ ] **Step 4: 实现前端上传/删除交互**

保持 UI 简洁：文件列表、上传按钮、删除确认、错误提示。

- [ ] **Step 5: 运行测试**

Run:

```bash
go test ./internal/service ./internal/api/handlers -run 'Knowledge' -count=1
cd web
npm run typecheck
npm test -- --run AppKnowledgeTab
```

Expected: 全部通过。

- [ ] **Step 6: Commit**

```bash
git add internal/service/knowledge_service.go internal/service/knowledge_service_test.go internal/api/handlers/knowledge.go web/src/api/hooks/useKnowledge.ts web/src/pages/apps/AppKnowledgeTab.vue
git commit -m "feat(knowledge): 应用级知识库同步推送到节点"
```

### Task 5: 验证并补齐工作目录下载链路

**Files:**
- Modify: `internal/service/workspace_service.go`
- Modify: `internal/service/workspace_service_test.go`
- Modify: `internal/api/handlers/workspace.go`
- Modify: `web/src/api/hooks/useWorkspace.ts`
- Modify: `web/src/pages/apps/AppWorkspaceTab.vue`
- Test: `web/src/pages/apps/AppWorkspaceTab.test.ts` 或现有前端测试位置

- [ ] **Step 1: 写失败测试：下载路径必须被双层校验**

测试点：

- 拒绝 `..`、绝对路径、空路径下载。
- `ListWorkspace` 允许 `/` 或空 path 作为根目录。
- 下载和 archive 走 `AgentFileClient`，不读 manager 本地文件系统。

Run:

```bash
go test ./internal/service ./internal/api/handlers -run 'Workspace' -count=1
```

Expected: 有缺口时失败。

- [ ] **Step 2: 实现最小修复**

统一 path clean 逻辑；stream response 不在 manager 内存缓冲大文件。

- [ ] **Step 3: 写前端失败测试：面包屑和下载链接**

测试点：目录点击进入、文件点击下载、目录 archive 下载。

- [ ] **Step 4: 实现前端交互**

下载 URL 使用后端 API 直链，带 access token 的项目如不能直链，则使用 fetch blob 下载，避免 401。

- [ ] **Step 5: chrome-devtools MCP 验证**

用容器命令写入 `hello.txt` 到 `/workspace`，页面看到文件并下载，校验内容。

- [ ] **Step 6: Commit**

```bash
git add internal/service/workspace_service.go internal/service/workspace_service_test.go internal/api/handlers/workspace.go web/src/api/hooks/useWorkspace.ts web/src/pages/apps/AppWorkspaceTab.vue
git commit -m "feat(workspace): 打通工作目录浏览和下载链路"
```

### Task 6: 真实微信扫码和消息验证

**Files:**
- Modify: `docs/verification-report.md`

- [ ] **Step 1: 准备测试应用**

通过页面或 API 完成：

- 创建组织。
- 创建 Runtime Node 并完成 agent register/heartbeat。
- 创建成员和应用。
- 等待应用到 `binding_waiting`。

- [ ] **Step 2: 暂停并通知用户扫码**

发送：

```text
现在需要你用微信扫码验证应用绑定。
请打开当前页面显示的二维码完成扫码和确认。
完成后回复“已扫码”，我会继续检查 channel_bindings.status、bound_identity 和 apps.status。
```

- [ ] **Step 3: 用户回复后继续检查**

检查：

- `channel_bindings.status = bound`
- `channel_bindings.bound_identity` 非空
- `apps.status = running`
- 前端 Channels tab 显示已绑定

- [ ] **Step 4: 暂停并通知用户发消息**

发送：

```text
现在需要你给刚绑定的微信助手发送消息：
“请生成一个 hello.txt 到工作目录，内容为 OpenClaw workspace ok”
发送完成后回复“已发送”，我会继续检查 workspace 文件列表和下载内容。
```

- [ ] **Step 5: 用户回复后继续检查 workspace**

检查：

- AppWorkspaceTab 出现文件。
- 下载文件内容正确。
- 审计日志出现 workspace browse/download。

- [ ] **Step 6: 更新验证报告**

记录扫码时间、消息内容、DB 状态、页面验证、下载结果。

- [ ] **Step 7: Commit**

```bash
git add docs/verification-report.md
git commit -m "docs(verify): 记录微信扫码到工作目录下载闭环"
```

## Chunk 2: 组织治理

目标：组织管理员能维护组织级知识库、查看同步状态和用量；平台管理员能跨组织查看成员、应用、审计和用量。

### Task 7: 组织级知识库同步状态接口

**Files:**
- Modify: `internal/service/knowledge_service.go`
- Modify: `internal/service/knowledge_service_test.go`
- Modify: `internal/worker/handlers/knowledge_sync.go`
- Modify: `internal/worker/handlers/knowledge_sync_test.go`
- Modify: `internal/api/handlers/knowledge.go`
- Modify: `internal/api/handlers/knowledge_test.go`
- Modify: `internal/store/queries/audit_logs.sql` 或新增最小 sync status 查询

- [ ] **Step 1: 写失败测试：组织级上传为每个节点创建 sync job**

Run:

```bash
go test ./internal/service ./internal/worker/handlers -run 'Knowledge.*Org|KnowledgeSync' -count=1
```

Expected: 缺节点状态记录时失败。

- [ ] **Step 2: 实现同步状态来源**

优先复用 audit/job 记录，不新增表；如果无法稳定表达“每节点最近状态”，新增轻量表 `knowledge_sync_status` 并用迁移 + sqlc 管理。

- [ ] **Step 3: 暴露 `/orgs/{orgId}/knowledge/sync-status`**

返回每个节点：

- `node_id`
- `node_name`
- `status`
- `last_success_at`
- `last_error`
- `pending_jobs`

- [ ] **Step 4: 运行测试并提交**

```bash
go test ./internal/service ./internal/worker/handlers ./internal/api/handlers -run 'Knowledge' -count=1
git add internal migrations
git commit -m "feat(knowledge): 增加组织级知识库同步状态"
```

### Task 8: OrgKnowledgePage 同步状态 UI

**Files:**
- Modify: `web/src/api/hooks/useKnowledge.ts`
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`
- Test: `web/src/pages/knowledge/OrgKnowledgePage.test.ts` 或现有测试位置

- [ ] **Step 1: 写失败测试：显示每节点同步状态**

Run:

```bash
cd web
npm test -- --run OrgKnowledgePage
```

Expected: UI 缺状态时失败。

- [ ] **Step 2: 实现状态徽章和失败详情**

状态：`pending`、`syncing`、`succeeded`、`failed`、`node_unreachable`。

- [ ] **Step 3: chrome-devtools MCP 验证**

两个本地 agent 模拟多节点；一个节点停止后上传文件，页面显示部分失败。

- [ ] **Step 4: Commit**

```bash
git add web/src/api/hooks/useKnowledge.ts web/src/pages/knowledge/OrgKnowledgePage.vue
git commit -m "feat(web): 展示组织知识库节点同步状态"
```

### Task 9: 用量报表和平台跨组织视图

**Files:**
- Modify: `internal/service/usage_service.go`
- Modify: `internal/service/usage_service_test.go`
- Modify: `internal/api/handlers/usage.go`
- Modify: `internal/service/app_service.go`
- Modify: `internal/service/app_service_test.go`
- Modify: `internal/service/member_service.go`
- Modify: `internal/service/member_service_test.go`
- Modify: `internal/service/audit_service.go`
- Modify: `internal/service/audit_service_test.go`
- Modify: `web/src/api/hooks/useApps.ts`
- Modify: `web/src/api/hooks/useMembers.ts`
- Modify: `web/src/api/hooks/useAuditLogs.ts`
- Create: `web/src/api/hooks/useUsage.ts`
- Modify: `web/src/pages/apps/AppsPage.vue`
- Modify: `web/src/pages/org/MembersPage.vue`
- Modify: `web/src/pages/audit/AuditLogsPage.vue`
- Modify: `web/src/pages/dashboard/RoleAwareHome.vue`

- [ ] **Step 1: 写后端失败测试：platform_admin 可指定 org_id 查询**

覆盖 apps、members、audit logs、usage。

- [ ] **Step 2: 实现后端权限扩展**

platform_admin 可读任意组织；写操作仍执行现有权限规则。

- [ ] **Step 3: 写前端失败测试：组织上下文切换**

RoleAwareHome 或 DashboardLayout 保存当前组织上下文，列表 query 带 `org_id`。

- [ ] **Step 4: 实现用量 hooks 和页面摘要**

报表最小可交付：

- 时间范围选择。
- 请求数。
- prompt/completion/total tokens。
- credit 消耗。
- 模型维度列表或图表。

- [ ] **Step 5: chrome-devtools MCP 验证**

用 mock/staging new-api 数据验证数字显示，切换组织后列表变化。

- [ ] **Step 6: Commit**

```bash
git add internal/service internal/api/handlers web/src
git commit -m "feat(usage): 增加用量报表和跨组织视图"
```

## Chunk 3: 运维增强

目标：管理员能看到应用资源和健康状态，容器异常能按策略自动重启，api_key 可手动禁用/恢复。

### Task 10: runtime_refresh_status 与资源监控 UI

**Files:**
- Modify: `internal/domain/enums.go`
- Modify: `internal/scheduler/runner.go`
- Modify: `internal/worker/handlers/registry.go`
- Create: `internal/worker/handlers/runtime_refresh_status.go`
- Create: `internal/worker/handlers/runtime_refresh_status_test.go`
- Modify: `internal/service/runtime_operation_service.go`
- Modify: `internal/service/runtime_operation_service_test.go`
- Modify: `internal/integrations/runtime/adapter.go`
- Modify: `internal/integrations/runtime/agent_backed.go`
- Modify: `web/src/api/hooks/useApps.ts`
- Modify: `web/src/pages/apps/AppRuntimeTab.vue`

- [ ] **Step 1: 写失败测试：worker 调 RuntimeAdapter.Stats 并缓存结果**

优先 Redis 短期缓存；若已有 runtime endpoint 可返回实时 stats，保持最小改动。

- [ ] **Step 2: 实现 worker 和 scheduler**

周期扫描 running / binding_waiting 应用，刷新容器状态。

- [ ] **Step 3: 写前端失败测试：Runtime tab 显示 CPU/内存/网络/磁盘**

- [ ] **Step 4: 实现 UI**

先用当前值和简易 sparkline；不引入重型图表库，除非现有依赖已有可用组件。

- [ ] **Step 5: chrome-devtools MCP 验证**

对测试容器制造负载，观察指标变化。

- [ ] **Step 6: Commit**

```bash
git add internal web/src/pages/apps/AppRuntimeTab.vue
git commit -m "feat(runtime): 增加容器资源状态刷新"
```

### Task 11: app_health_check 与自动重启策略

**Files:**
- Create: `migrations/000004_runtime_health.up.sql`
- Create: `migrations/000004_runtime_health.down.sql`
- Modify: `internal/store/queries/apps.sql`
- Modify: `internal/domain/app_state_machine.go`
- Create: `internal/worker/handlers/app_health_check.go`
- Create: `internal/worker/handlers/app_health_check_test.go`
- Modify: `internal/worker/handlers/registry.go`
- Modify: `internal/scheduler/runner.go`
- Modify: `internal/service/app_service.go`
- Modify: `internal/service/app_service_test.go`
- Modify: `web/src/pages/apps/AppRuntimeTab.vue`

- [ ] **Step 1: 写迁移**

字段建议：

- `restart_policy text not null default 'none'`
- `max_restarts_per_hour integer not null default 3`
- `last_health_status text null`
- `last_health_checked_at timestamptz null`
- `last_health_error text null`
- `restart_count_window_started_at timestamptz null`
- `restart_count_in_window integer not null default 0`

- [ ] **Step 2: 写失败测试：健康失败按策略重启**

覆盖：

- `none`：置 error，不重启。
- `on_failure`：重启一次。
- 超过频率：置 error。

- [ ] **Step 3: 实现 worker**

HealthCheck 失败时根据策略调用 RuntimeAdapter.RestartContainer。

- [ ] **Step 4: 实现前端策略设置**

Runtime tab 提供策略选择、最大次数输入、高风险确认。

- [ ] **Step 5: 运行迁移和测试**

```bash
go run ./cmd/migrate up
go test ./internal/worker/handlers ./internal/service -run 'Health|Restart' -count=1
```

- [ ] **Step 6: Commit**

```bash
git add migrations internal web/src/pages/apps/AppRuntimeTab.vue
git commit -m "feat(runtime): 增加健康检查和自动重启策略"
```

### Task 12: api_key 风控和平台总览

**Files:**
- Create: `internal/worker/handlers/newapi_key_ops.go`
- Create: `internal/worker/handlers/newapi_key_ops_test.go`
- Modify: `internal/service/app_service.go`
- Modify: `internal/service/app_service_test.go`
- Modify: `internal/api/handlers/apps.go`
- Modify: `internal/api/handlers/apps_test.go`
- Create: `internal/service/dashboard_service.go`
- Create: `internal/service/dashboard_service_test.go`
- Create: `internal/api/handlers/dashboard.go`
- Modify: `internal/api/router.go`
- Modify: `web/src/api/hooks/useApps.ts`
- Create: `web/src/api/hooks/useDashboard.ts`
- Modify: `web/src/pages/dashboard/DashboardHome.vue`
- Modify: `web/src/pages/dashboard/RoleAwareHome.vue`
- Modify: `web/src/pages/apps/AppOverviewTab.vue`

- [ ] **Step 1: 写失败测试：禁用 api_key 创建 job**

覆盖权限、审计、状态展示。

- [ ] **Step 2: 实现 newapi key ops worker**

调用 `DisableAPIKey` / `RestoreAPIKey`，更新 `apps.api_key_status`。

- [ ] **Step 3: 写失败测试：dashboard 聚合**

聚合组织数、成员数、应用数、运行中容器数、异常应用数。

- [ ] **Step 4: 实现 dashboard service 和 handler**

new-api 用量摘要失败时，dashboard 应展示局部错误，不影响 DB 聚合。

- [ ] **Step 5: 实现前端**

AppOverview 显示 api_key 状态，DashboardHome 显示平台/组织摘要。

- [ ] **Step 6: Commit**

```bash
git add internal web/src
git commit -m "feat(ops): 增加 api_key 风控和平台总览"
```

## Chunk 4: 商用 hardening 与发布验收

目标：安全、E2E、多节点和文档达到 UAT 前质量。

### Task 13: CSRF 和 refresh token 生命周期

**Files:**
- Modify: `internal/api/middleware/security.go`
- Modify: `internal/api/middleware/security_test.go`
- Modify: `internal/api/handlers/auth.go`
- Modify: `internal/api/handlers/auth_test.go`
- Modify: `internal/service/auth_service.go`
- Modify: `internal/service/auth_service_test.go`
- Modify: `web/src/api/client.ts`
- Modify: `web/src/stores/auth.ts`

- [ ] **Step 1: 写失败测试：无 CSRF 的写请求返回 403**

覆盖 POST、PATCH、DELETE。

- [ ] **Step 2: 实现 double-submit cookie**

登录/refresh 返回 CSRF cookie；前端写操作带 header。

- [ ] **Step 3: 写 refresh token 生命周期测试**

覆盖登录、刷新、旧 refresh 失效、logout 撤销、过期拒绝。

- [ ] **Step 4: 实现最小修复**

保持 access token 兼容现有前端存储策略；如果切 HttpOnly cookie，必须同步改前端和测试。

- [ ] **Step 5: Commit**

```bash
git add internal/api internal/service web/src/api/client.ts web/src/stores/auth.ts
git commit -m "fix(auth): 接入 CSRF 校验和刷新令牌生命周期测试"
```

### Task 14: 日志脱敏和错误响应去敏

**Files:**
- Create: `internal/auth/redact.go`
- Create: `internal/auth/redact_test.go`
- Modify: `internal/api/handlers/*.go`
- Modify: `internal/service/errors.go`
- Modify: `cmd/server/main.go`
- Modify: `internal/worker/runner.go`
- Modify: `internal/worker/worker.go`

- [ ] **Step 1: 写失败测试：敏感字段脱敏**

输入包含 `api_key`、`bootstrap_token`、`agent_token`、`refresh_token`、`master_key`、`Authorization: Bearer`，输出不得包含原值。

- [ ] **Step 2: 实现统一脱敏函数**

所有日志输出外部错误前调用。

- [ ] **Step 3: 写错误响应去敏测试**

触发 SQL 错误、内部路径错误、stack trace，HTTP response 不得包含敏感内部细节。

- [ ] **Step 4: 实现错误响应标准化**

稳定结构：

```json
{"code":"...","message":"...","request_id":"...","details":{}}
```

- [ ] **Step 5: Commit**

```bash
git add internal cmd/server
git commit -m "fix(security): 增加日志脱敏和错误响应去敏"
```

### Task 15: Playwright 6 个核心场景

**Files:**
- Create: `web/playwright.config.ts`
- Create: `web/tests/e2e/platform-org.spec.ts`
- Create: `web/tests/e2e/runtime-node.spec.ts`
- Create: `web/tests/e2e/member-onboard.spec.ts`
- Create: `web/tests/e2e/channel-binding.spec.ts`
- Create: `web/tests/e2e/knowledge-workspace.spec.ts`
- Create: `web/tests/e2e/usage-audit.spec.ts`
- Modify: `web/package.json`
- Modify: `docs/local-development.md`

- [ ] **Step 1: 添加 Playwright 配置和 npm script**

Script:

```json
"e2e": "playwright test"
```

- [ ] **Step 2: 写平台组织和充值场景**

登录 platform_admin，创建组织，进入充值页，mock new-api 充值成功。

- [ ] **Step 3: 写 Runtime Node 场景**

创建节点，模拟 agent register/heartbeat，验证 active。

- [ ] **Step 4: 写成员 onboard 场景**

创建成员和应用，验证应用出现并进入稳定状态。

- [ ] **Step 5: 写渠道绑定场景**

CI 使用 mock QR 和 mock bound；真实微信验证仍按人工暂停协议。

- [ ] **Step 6: 写知识库和 workspace 场景**

上传应用知识库，列 workspace，下载 mock 文件。

- [ ] **Step 7: 写用量和审计场景**

验证用量页面和审计列表。

- [ ] **Step 8: 运行**

```bash
cd web
npm run e2e
```

- [ ] **Step 9: Commit**

```bash
git add web docs/local-development.md
git commit -m "test(e2e): 增加 v1.0 RC 核心浏览器场景"
```

### Task 16: 安全扫描、多节点演练和发布文档

**Files:**
- Modify: `deploy/README.md`
- Create: `deploy/backup-restore.md`
- Create: `deploy/upgrade.md`
- Create: `docs/release/v1-rc-checklist.md`
- Modify: `docs/verification-report.md`
- Modify: `Makefile`

- [ ] **Step 1: 增加安全扫描命令**

Make targets:

- `security-go`
- `security-web`
- `verify-v1-rc`

- [ ] **Step 2: 运行 gosec 和 npm audit**

```bash
gosec ./...
cd web
npm audit --audit-level=high
```

Expected: 无 high 级别问题；如有，修复后重跑。

- [ ] **Step 3: 写部署文档**

覆盖：

- manager/postgres/redis/new-api/ollama/agent 部署拓扑。
- master_key、JWT secret、CSRF secret。
- agent bootstrap。
- 端口、防火墙、TLS。

- [ ] **Step 4: 写备份恢复文档**

覆盖：

- manager PostgreSQL。
- manager 知识库主副本。
- agent 节点数据目录。
- new-api 数据。

- [ ] **Step 5: 写升级文档**

覆盖：

- 停机窗口。
- DB migration。
- manager/agent/openclaw 镜像版本。
- 回滚。

- [ ] **Step 6: 多节点跨机演练**

需要用户提供节点条件时暂停：

```text
现在需要你确认跨机节点访问条件：两台 agent 机器地址、可用端口、防火墙/VPN 状态，以及是否可以启动 agent 容器。
确认后我会继续执行 agent register、heartbeat、容器创建、file API、workspace archive 验证。
```

- [ ] **Step 7: 更新验证报告**

记录跨机节点、命令、页面截图结论、性能基线和已知风险。

- [ ] **Step 8: Commit**

```bash
git add Makefile deploy docs
git commit -m "docs(deploy): 增加 v1.0 RC 部署验收文档"
```

## Final Release Verification

完成所有 Chunk 后运行：

```bash
go test ./... -count=1
go vet ./...
go build ./...
```

```bash
cd web
npm run typecheck
npm test -- --run
npm run build
npm run e2e
```

```bash
gosec ./...
cd web
npm audit --audit-level=high
```

```bash
docker compose up -d manager-postgres manager-redis manager-api manager-web oc-runtime-agent
go run ./cmd/migrate up
curl -fsS http://localhost:3001/healthz
```

再用 chrome-devtools MCP 完整浏览：

- `/login`
- `/`
- `/organizations`
- `/runtime-nodes`
- `/members`
- `/members/new`
- `/apps`
- `/apps/{appId}/overview`
- `/apps/{appId}/runtime`
- `/apps/{appId}/channels`
- `/apps/{appId}/knowledge`
- `/apps/{appId}/workspace`
- `/knowledge`
- `/audit-logs`

最后完成两个人工介入验证：

- 真实微信扫码绑定。
- 真实微信消息生成 workspace 文件并下载。

## Execution Handoff

执行本计划时，优先使用 `superpowers:subagent-driven-development`。如果当前 harness 不使用 subagent 执行，则使用 `superpowers:executing-plans`，按 Chunk 顺序执行，每个 Chunk 结束暂停给用户审查验证报告和提交记录。

