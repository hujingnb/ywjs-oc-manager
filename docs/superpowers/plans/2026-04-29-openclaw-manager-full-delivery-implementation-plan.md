# OpenClaw Manager 完整交付实施计划

> **给自动化执行代理的要求：** 必须使用 superpowers:subagent-driven-development（如果环境支持 subagents）或 superpowers:executing-plans 执行本计划。所有步骤使用 checkbox（`- [ ]`）语法追踪。

**目标：** 按已确认规格完成完整 OpenClaw Manager 产品，包括 Docker 化本地开发、完整测试、中文注释、浏览器验证页面、OpenClaw runtime 镜像构建与分发、Ollama 调试、new-api 浏览器配置。

**架构：** 后端采用 Go/Gin，包含 Manager API、worker、scheduler、PostgreSQL、Redis、agent-backed runtime 操作；前端采用 Vue 3 + Naive UI 的运维控制台式后台。所有本地调试服务由 Docker Compose 管理，Go 在 `manager-api` 容器内用 `air` 运行，前端在 `manager-web` 容器内用 Vite dev server 运行，所有持久化目录使用本地 bind mount。OpenClaw runtime 镜像必须从仓库内 Dockerfile 自行构建，并在应用容器启动前按 digest/hash 分发到 runtime node。

**技术栈：** Go、Gin、pgx、sqlc、golang-migrate、go-redis、Docker SDK、Vue 3、Vite、TypeScript、Naive UI、Pinia、TanStack Query、OpenAPI、Docker Compose、Air、PostgreSQL、Redis、new-api、Ollama。

---

## 范围检查

这是完整产品的主实施计划，按已确认规格拆成 9 个 chunk。每个 chunk 必须产出可运行、可测试的软件，并以提交结束。不得跳过测试、中文注释、浏览器验证或 compose 校验。

主要参考文档：

- `docs/openclaw-manager-design.md`
- `docs/openclaw-manager-technical-design.md`
- `docs/superpowers/specs/2026-04-29-openclaw-manager-full-delivery-design.md`

全局规则：

- 本地调试不直接在宿主机运行后端或前端，必须使用 Docker Compose。
- 不定义 Docker named volume，只使用本地 bind mount。
- 每次新增实现都要同步补齐完整单元测试。
- 公开 Go 类型、公开方法、DTO 字段、service 方法、状态机、job handler、adapter、权限函数、补偿逻辑、安全边界和复杂事务必须有详细中文注释。
- 所有涉及页面的 chunk 必须通过 chrome-devtools MCP 验证。
- 任何验证失败都必须先修复并重测，再进入下一步。
- 项目文档默认使用中文输出；命令、代码标识、API 路径和专有英文名称保持原文。

---

## 文件结构规划

逐步创建并演进以下结构：

```text
.
├── Makefile
├── docker-compose.yml
├── .env.example
├── .air.toml
├── config/
│   └── config.yaml.example
├── cmd/
│   ├── server/main.go
│   └── migrate/main.go
├── internal/
│   ├── api/
│   ├── auth/
│   ├── config/
│   ├── domain/
│   ├── files/
│   ├── integrations/
│   │   ├── agent/
│   │   ├── channel/
│   │   ├── newapi/
│   │   ├── openclaw/
│   │   └── runtime/
│   ├── redis/
│   ├── scheduler/
│   ├── service/
│   ├── store/
│   │   ├── queries/
│   │   └── sqlc/
│   └── worker/
├── migrations/
├── openapi/
│   └── openapi.yaml
├── runtime/
│   ├── agent/
│   └── openclaw/
├── scripts/
│   ├── check-compose-bind-mounts.sh
│   ├── debug-newapi.sh
│   ├── debug-ollama.sh
│   └── verify-openclaw-runtime.sh
└── web/
```

---

## 实施块 1：工程基础与 Docker 化本地调试

### 任务 1.1：创建后端骨架、配置、健康检查和测试

**文件：**
- 新建：`go.mod`
- 新建：`cmd/server/main.go`
- 新建：`internal/config/config.go`
- 新建：`internal/config/loader.go`
- 新建：`internal/config/loader_test.go`
- 新建：`internal/api/router.go`
- 新建：`internal/api/handlers/health.go`
- 新建：`internal/api/handlers/health_test.go`
- 新建：`config/config.yaml.example`
- 新建：`.air.toml`

- [ ] 编写失败测试：配置环境变量展开、必填字段校验、`/healthz` 响应。
- [ ] 运行：`docker compose run --rm manager-api go test ./internal/config ./internal/api/handlers`
  预期：实现前失败。
- [ ] 实现配置加载器，并为公开类型和校验行为添加中文注释。
- [ ] 实现 Gin router 和健康检查 handler。
- [ ] 运行：`docker compose run --rm manager-api go test ./...`
  预期：通过。
- [ ] 运行：`docker compose run --rm manager-api go vet ./...`
  预期：通过。
- [ ] 提交：`feat: add backend foundation and health check`。

### 任务 1.2：创建 Compose、Makefile、bind mount 检查和本地配置

**文件：**
- 新建：`docker-compose.yml`
- 新建：`Makefile`
- 新建：`.env.example`
- 修改：`.gitignore`
- 新建：`scripts/check-compose-bind-mounts.sh`

- [ ] 编写 `scripts/check-compose-bind-mounts.sh`，如果 compose 包含顶层 `volumes:` 或 service 挂载不是本地路径则失败。
- [ ] 增加 Make targets：`dev-up`、`dev-down`、`test`、`vet`、`build`、`migrate-up`、`migrate-down`、`check-compose`、`logs`。
- [ ] 增加 compose 服务：`manager-postgres`、`redis`、`new-api-postgres`、`new-api`、`ollama`、`manager-api`、`manager-web`、`oc-runtime-agent`。
- [ ] 确保所有持久化挂载都使用 `./data/...:/container/path`。
- [ ] 运行：`make check-compose`
  预期：通过。
- [ ] 运行：`make dev-up`
  预期：基础容器可启动或构建。
- [ ] 运行：`make dev-down`
  预期：容器停止。
- [ ] 提交：`chore: add dockerized local development`。

### 任务 1.3：创建 migration、OpenAPI 基线和迁移验证

**文件：**
- 新建：`cmd/migrate/main.go`
- 新建：`migrations/000001_init.up.sql`
- 新建：`migrations/000001_init.down.sql`
- 新建：`openapi/openapi.yaml`
- 修改：`Makefile`

- [ ] 添加 migration 命令，并用中文注释说明生产环境不自动强制迁移。
- [ ] 添加空但合法的初始 schema，用于验证迁移机制。
- [ ] 添加 OpenAPI 初始契约，包含 `/healthz` 和 `/api/v1/auth/me`。
- [ ] 运行：`make dev-up`。
- [ ] 运行：`make migrate-up`
  预期：成功。
- [ ] 运行：`make migrate-down`
  预期：成功回滚。
- [ ] 运行：`make build`
  预期：后端和前端构建目标在前端骨架完成后通过。
- [ ] 提交：`chore: add migrations and openapi baseline`。

### 任务 1.4：创建前端骨架并用浏览器验证布局壳

**文件：**
- 新建：`web/package.json`
- 新建：`web/vite.config.ts`
- 新建：`web/tsconfig.json`
- 新建：`web/src/main.ts`
- 新建：`web/src/app/router.ts`
- 新建：`web/src/app/query-client.ts`
- 新建：`web/src/layouts/AuthLayout.vue`
- 新建：`web/src/layouts/DashboardLayout.vue`
- 新建：`web/src/pages/login/LoginPage.vue`
- 新建：`web/src/pages/dashboard/DashboardHome.vue`
- 新建：`web/src/styles/base.css`
- 新建：`web/src/domain/status.ts`
- 测试：`web/src/domain/status.test.ts`

- [ ] 为状态标签格式化编写前端单元测试。
- [ ] 实现已确认的运维控制台式布局：固定左侧导航、顶部状态栏、高密度主内容区。
- [ ] 增加 Make targets：`web-typecheck`、`web-test`、`web-build`。
- [ ] 运行：`docker compose run --rm manager-web npm test -- --run`
  预期：通过。
- [ ] 运行：`docker compose run --rm manager-web npm run typecheck`
  预期：通过。
- [ ] 运行：`docker compose run --rm manager-web npm run build`
  预期：通过。
- [ ] 运行：`make dev-up`。
- [ ] 使用 chrome-devtools MCP 打开 manager web URL。
  验证：页面可加载，左侧导航存在，顶部状态栏存在，文本不重叠。
- [ ] 提交：`feat: add web console shell`。

### 任务 1.5：构建 OpenClaw runtime 镜像并调试 Ollama/new-api

**文件：**
- 新建：`runtime/openclaw/Dockerfile`
- 新建：`runtime/openclaw/healthcheck.sh`
- 新建：`runtime/openclaw/verify-install.sh`
- 新建：`runtime/agent/Dockerfile`
- 新建：`runtime/agent/main.go`
- 新建：`scripts/verify-openclaw-runtime.sh`
- 新建：`scripts/debug-ollama.sh`
- 新建：`scripts/debug-newapi.sh`
- 修改：`Makefile`
- 修改：`docker-compose.yml`

- [ ] 增加 `make build-openclaw-runtime`。
- [ ] 添加 OpenClaw runtime Dockerfile，安装 OpenClaw、微信插件、依赖和验证脚本。
- [ ] 添加阶段 1 用的最小 `oc-runtime-agent` 健康检查和 fake API 容器。
- [ ] 添加 `make debug-ollama`，验证 API、列出模型、拉取配置的小模型，并完成一次最小调用。
- [ ] 添加 `make debug-newapi`，验证 HTTP、健康/管理访问、数据库连接和 Ollama 渠道可用性。
- [ ] 运行：`make build-openclaw-runtime`
  预期：镜像构建成功。
- [ ] 运行：`scripts/verify-openclaw-runtime.sh`
  预期：OpenClaw 和微信插件安装检查通过。
- [ ] 运行：`make debug-ollama`
  预期：Ollama 可访问，小模型调用成功。
- [ ] 使用 chrome-devtools MCP 在浏览器中打开 new-api。
  验证：管理页面可加载，Ollama 渠道已配置且可用。
- [ ] 运行：`make debug-newapi`
  预期：服务和数据库检查通过。
- [ ] 提交：`chore: add runtime image and external service debug`。

---

## 实施块 2：认证、组织、权限和审计

### 任务 2.1：添加 schema 和 sqlc 查询

**文件：**
- 新建：`migrations/000002_identity.up.sql`
- 新建：`migrations/000002_identity.down.sql`
- 新建：`internal/store/queries/users.sql`
- 新建：`internal/store/queries/organizations.sql`
- 新建：`internal/store/queries/audit_logs.sql`
- 新建：`internal/store/queries/recharge_records.sql`
- 新建：`internal/store/queries/refresh_tokens.sql`
- 新建：`sqlc.yaml`

- [ ] 编写 users、organizations、personas、recharge_records、audit_logs、refresh_tokens 迁移。
- [ ] 添加技术设计要求的唯一索引和查询索引。
- [ ] 在容器中运行 migration up/down。
- [ ] 生成 sqlc 代码。
- [ ] 提交：`feat: add identity schema`。

### 任务 2.2：实现认证和权限，并补齐单元测试

**文件：**
- 新建：`internal/auth/password.go`
- 新建：`internal/auth/password_test.go`
- 新建：`internal/auth/jwt.go`
- 新建：`internal/auth/jwt_test.go`
- 新建：`internal/auth/refresh_token.go`
- 新建：`internal/domain/permissions.go`
- 新建：`internal/domain/permissions_test.go`
- 新建：`internal/service/auth_service.go`
- 新建：`internal/service/auth_service_test.go`
- 新建：`internal/api/middleware/auth.go`
- 新建：`internal/api/handlers/auth.go`

- [ ] 编写 Argon2id 密码校验、JWT 校验、refresh token 撤销、禁用组织/用户拒绝访问、角色边界测试。
- [ ] 实现认证逻辑，并围绕 token 安全和禁用用户行为添加详细中文注释。
- [ ] 运行：`docker compose run --rm manager-api go test ./internal/auth ./internal/domain ./internal/service`
- [ ] 提交：`feat: add authentication and permission checks`。

### 任务 2.3：实现组织、成员基础、充值和审计页面

**文件：**
- 新建：`internal/service/organization_service.go`
- 新建：`internal/service/member_service.go`
- 新建：`internal/service/audit_service.go`
- 新建：`internal/api/handlers/organizations.go`
- 新建：`internal/api/handlers/members.go`
- 新建：`internal/api/handlers/audit.go`
- 新建：`web/src/pages/platform/OrganizationsPage.vue`
- 新建：`web/src/pages/org/MembersPage.vue`
- 新建：`web/src/pages/audit/AuditLogsPage.vue`
- 新建：`web/src/api/hooks/useOrganizations.ts`
- 新建：`web/src/api/hooks/useMembers.ts`
- 新建：`web/src/api/hooks/useAuditLogs.ts`

- [ ] 编写 service 测试：组织 CRUD、启用/禁用、成员可见性、充值审计。
- [ ] 实现 API 并更新 OpenAPI。
- [ ] 使用已确认的列表页模板实现前端表格页面。
- [ ] 运行后端测试、前端测试、typecheck、build。
- [ ] 使用 chrome-devtools MCP 验证登录壳、组织列表、成员列表、审计列表。
- [ ] 提交：`feat: add organization and member management basics`。

---

## 实施块 3：Runtime Node、Agent 和镜像分发

### 任务 3.1：Runtime Node schema、服务和 agent 注册

**文件：**
- 新建：`migrations/000003_runtime_nodes.up.sql`
- 新建：`migrations/000003_runtime_nodes.down.sql`
- 新建：`internal/store/queries/runtime_nodes.sql`
- 新建：`internal/service/runtime_node_service.go`
- 新建：`internal/service/runtime_node_service_test.go`
- 新建：`internal/api/handlers/runtime_nodes.go`
- 新建：`internal/integrations/agent/endpoints.go`

- [ ] 测试 bootstrap token hash、过期、单次消费、并发注册、rotate、心跳恢复。
- [ ] 实现 runtime node CRUD 和 agent register/heartbeat 端点。
- [ ] 添加中文注释解释 bootstrap token 和 agent token 的安全边界。
- [ ] 运行：`docker compose run --rm manager-api go test ./internal/service ./internal/integrations/agent`。
- [ ] 提交：`feat: add runtime node registration and heartbeat`。

### 任务 3.2：Agent 文件 API、RuntimeAdapter 接口和镜像分发

**文件：**
- 新建：`internal/integrations/agent/file_client.go`
- 新建：`internal/integrations/agent/file_client_test.go`
- 新建：`internal/integrations/runtime/adapter.go`
- 新建：`internal/integrations/runtime/agent_backed.go`
- 新建：`internal/integrations/runtime/agent_backed_test.go`
- 新建：`internal/service/image_distribution_service.go`
- 新建：`internal/service/image_distribution_service_test.go`
- 修改：`runtime/agent/main.go`

- [ ] 测试 fake agent 镜像 digest 检查、镜像缺失、hash 不一致、tar 上传、`docker load` 成功/失败。
- [ ] 实现 client 接口，并用中文注释说明 TLS 和 Bearer 认证约束。
- [ ] 实现 ImageDistributionService：相同 digest 跳过，缺失或不一致时上传/load，失败返回可重试错误。
- [ ] 运行相关测试。
- [ ] 提交：`feat: add agent-backed runtime and image distribution`。

### 任务 3.3：Runtime Node 前端页面

**文件：**
- 新建：`web/src/pages/runtime-nodes/RuntimeNodesPage.vue`
- 新建：`web/src/pages/runtime-nodes/RuntimeNodeDetailPage.vue`
- 新建：`web/src/api/hooks/useRuntimeNodes.ts`
- 新建：`web/src/components/RuntimeStatusTag.vue`

- [ ] 添加状态标签和 rotate bootstrap UI 状态的组件测试。
- [ ] 按已确认列表/详情模板实现页面。
- [ ] 使用 chrome-devtools MCP 验证创建节点、查看节点、rotate bootstrap、状态展示。
- [ ] 提交：`feat: add runtime node console pages`。

---

## 实施块 4：Jobs、Worker、Scheduler 和状态机

### 任务 4.1：Jobs schema 和队列

**文件：**
- 新建：`migrations/000004_jobs.up.sql`
- 新建：`migrations/000004_jobs.down.sql`
- 新建：`internal/store/queries/jobs.sql`
- 新建：`internal/redis/queue.go`
- 新建：`internal/redis/queue_test.go`
- 新建：`internal/domain/job_state_machine.go`
- 新建：`internal/domain/job_state_machine_test.go`

- [ ] 测试 pending/running/succeeded/failed/canceled 转换、延迟队列行为、Redis 队列丢失恢复前提。
- [ ] 实现队列和状态机。
- [ ] 提交：`feat: add job persistence and redis queue`。

### 任务 4.2：Worker、Scheduler、Reconciler

**文件：**
- 新建：`internal/worker/worker.go`
- 新建：`internal/worker/worker_test.go`
- 新建：`internal/worker/handlers/registry.go`
- 新建：`internal/scheduler/scheduler.go`
- 新建：`internal/scheduler/scheduler_test.go`
- 新建：`internal/api/handlers/jobs.go`
- 新建：`web/src/components/JobProgressPanel.vue`

- [ ] 测试 job 锁定、最大尝试次数、指数退避、失败回写、pending job 重新入队。
- [ ] 实现 worker 和 scheduler，并用中文注释说明 PostgreSQL 是任务事实来源。
- [ ] 增加 job 进度 UI。
- [ ] 在浏览器中验证 job 页面或进度面板。
- [ ] 提交：`feat: add worker scheduler and job progress`。

---

## 实施块 5：成员创建联动应用初始化

### 任务 5.1：Apps 和 Channel Bindings schema

**文件：**
- 新建：`migrations/000005_apps.up.sql`
- 新建：`migrations/000005_apps.down.sql`
- 新建：`internal/store/queries/apps.sql`
- 新建：`internal/store/queries/channel_bindings.sql`
- 新建：`internal/domain/app_state_machine.go`
- 新建：`internal/domain/app_state_machine_test.go`

- [ ] 测试应用状态转换和 api_key 状态独立性。
- [ ] 添加 owner active app 唯一索引。
- [ ] 提交：`feat: add app and channel schema`。

### 任务 5.2：创建成员并在事务中创建应用

**文件：**
- 修改：`internal/service/member_service.go`
- 新建：`internal/service/app_service.go`
- 新建：`internal/service/app_service_test.go`
- 修改：`internal/api/handlers/members.go`
- 新建：`web/src/pages/org/CreateMemberPage.vue`

- [ ] 测试 user/app/binding/audit/job 任一步失败时事务回滚。
- [ ] 实现 `POST /members`：创建用户、应用、渠道绑定、审计和 `app_initialize` job。
- [ ] 实现单页分组表单。
- [ ] 浏览器验证：表单校验、提交、跳转应用详情并展示 job 状态。
- [ ] 提交：`feat: create members with linked apps`。

### 任务 5.3：new-api Adapter、Prompt 渲染和 app_initialize handler

**文件：**
- 新建：`internal/integrations/newapi/client.go`
- 新建：`internal/integrations/newapi/client_test.go`
- 新建：`internal/integrations/openclaw/prompt.go`
- 新建：`internal/integrations/openclaw/prompt_test.go`
- 新建：`internal/worker/handlers/app_initialize.go`
- 新建：`internal/worker/handlers/app_initialize_test.go`

- [ ] 用 fake HTTP server 测试 new-api 错误映射。
- [ ] 测试 prompt 变量、未替换占位符检测、平台/组织/应用拼接顺序。
- [ ] 测试 app_initialize 幂等：api_key 已存在、目录已存在、镜像 digest 相同、容器已存在。
- [ ] 实现初始化：active 节点检查、镜像分发、api_key 创建、目录、知识库同步、prompt、容器创建/启动、健康检查、状态更新。
- [ ] 提交：`feat: initialize linked OpenClaw apps`。

---

## 实施块 6：渠道绑定和 OpenClaw 集成

### 任务 6.1：ChannelAdapter Registry 和微信实现

**文件：**
- 新建：`internal/integrations/channel/adapter.go`
- 新建：`internal/integrations/channel/registry.go`
- 新建：`internal/integrations/channel/registry_test.go`
- 新建：`internal/integrations/channel/wechat.go`
- 新建：`internal/integrations/channel/wechat_test.go`
- 新建：`internal/integrations/openclaw/parser.go`
- 新建：`internal/integrations/openclaw/parser_test.go`

- [ ] 测试 registry 路由、二维码 challenge 解析、过期/失败输出、不可解析输出。
- [ ] 如果 CLI 输出无法稳定解析，实现 JSON wrapper 要求。
- [ ] 提交：`feat: add channel adapter and wechat parsing`。

### 任务 6.2：渠道 API 和前端

**文件：**
- 新建：`internal/service/channel_service.go`
- 新建：`internal/service/channel_service_test.go`
- 新建：`internal/api/handlers/channels.go`
- 新建：`web/src/pages/apps/AppChannelsTab.vue`
- 新建：`web/src/components/AuthChallengeRenderer.vue`

- [ ] 测试登录、轮询、重试、解绑、失败状态、权限拒绝。
- [ ] 实现 API 和二维码渲染器。
- [ ] 使用 chrome-devtools MCP 验证二维码展示、重试、过期状态、错误提示。
- [ ] 提交：`feat: add channel binding workflow`。

---

## 实施块 7：知识库和工作目录

### 任务 7.1：安全路径和知识库服务

**文件：**
- 新建：`internal/files/safe_path.go`
- 新建：`internal/files/safe_path_test.go`
- 新建：`internal/files/knowledge_master.go`
- 新建：`internal/files/knowledge_master_test.go`
- 新建：`internal/service/knowledge_service.go`
- 新建：`internal/service/knowledge_service_test.go`
- 新建：`internal/api/handlers/knowledge.go`

- [ ] 测试绝对路径、`..`、URL 编码、符号链接、socket/device、最大文件大小。
- [ ] 实现 manager 主副本和组织/应用上传、删除、列表。
- [ ] 实现应用级同步失败回滚和组织级异步同步 job。
- [ ] 提交：`feat: add knowledge master and sync APIs`。

### 任务 7.2：工作目录代理和文件页面

**文件：**
- 新建：`internal/service/workspace_service.go`
- 新建：`internal/service/workspace_service_test.go`
- 新建：`internal/api/handlers/workspace.go`
- 新建：`web/src/pages/apps/AppWorkspaceTab.vue`
- 新建：`web/src/pages/knowledge/OrgKnowledgePage.vue`
- 新建：`web/src/pages/apps/AppKnowledgeTab.vue`

- [ ] 测试列表/下载/archive 权限、路径安全、大小限制、审计。
- [ ] 通过 AgentFileClient 实现只读工作目录代理。
- [ ] 实现带面包屑和下载操作的只读文件管理器 UI。
- [ ] 浏览器验证：知识库上传/删除、同步状态、工作目录面包屑、文件下载/archive 操作。
- [ ] 提交：`feat: add knowledge and workspace pages`。

---

## 实施块 8：用量、运行操作和完整后台

### 任务 8.1：用量和运行操作 API

**文件：**
- 新建：`internal/service/usage_service.go`
- 新建：`internal/service/usage_service_test.go`
- 新建：`internal/service/runtime_operation_service.go`
- 新建：`internal/service/runtime_operation_service_test.go`
- 新建：`internal/api/handlers/usage.go`
- 新建：`internal/api/handlers/app_runtime.go`

- [ ] 测试用量权限和 new-api 失败。
- [ ] 测试启动/停止/重启 job 创建、高风险审计、禁用账号行为。
- [ ] 实现 API。
- [ ] 提交：`feat: add usage and runtime operations`。

### 任务 8.2：补齐三类角色控制台

**文件：**
- 新建/修改：`web/src/pages/platform/*`
- 新建/修改：`web/src/pages/org/*`
- 新建/修改：`web/src/pages/apps/*`
- 新建/修改：`web/src/pages/usage/*`
- 新建/修改：`web/src/components/AppStatusTag.vue`
- 新建/修改：`web/src/components/ConfirmActionModal.vue`
- 新建/修改：`web/src/components/DataTableToolbar.vue`

- [ ] 添加状态标签、确认弹窗、工具栏、角色菜单可见性的组件测试。
- [ ] 实现平台页面：总览、组织、充值、应用、Runtime Node、用量、管理员、审计。
- [ ] 实现组织页面：总览、成员、应用、人设、知识库、用量、审计。
- [ ] 实现成员页面：总览、我的应用、渠道、知识库、组织知识库只读、工作目录、用量、设置。
- [ ] 运行 web tests、typecheck、build。
- [ ] 使用 chrome-devtools MCP 验证关键页面、表单校验、按钮、弹窗、异步刷新、无文本重叠。
- [ ] 提交：`feat: complete role-based management console`。

---

## 实施块 9：最终验证、文档和发布就绪

### 任务 9.1：端到端本地验证

**文件：**
- 新建：`docs/local-development.md`
- 新建：`docs/verification-report.md`
- 修改：`Makefile`
- 修改：`README.md`

- [ ] 运行：`make check-compose`
- [ ] 运行：`make build-openclaw-runtime`
- [ ] 运行：`make dev-up`
- [ ] 运行：`make migrate-up`
- [ ] 运行：`make debug-ollama`
- [ ] 使用浏览器配置/验证 new-api Ollama 渠道。
- [ ] 运行：`make debug-newapi`
- [ ] 运行：`make test`
- [ ] 运行：`make vet`
- [ ] 运行：`make web-test`
- [ ] 运行：`make web-typecheck`
- [ ] 运行：`make web-build`
- [ ] 手动或用 Playwright 跑关键 E2E：登录、创建组织、创建节点、agent 注册、创建成员+应用、初始化、绑定渠道、知识库、工作目录、运行操作、删除。
- [ ] 在 `docs/verification-report.md` 记录精确命令、结果、截图或 DOM 快照说明。
- [ ] 提交：`docs: add local development and verification report`。

### 任务 9.2：最终自检

**文件：**
- 修改：`docs/verification-report.md`

- [ ] 搜索代码和文档里的未完成标记：`rg -n "TODO|TBD|待定|暂不明确|panic\\(|console\\.log"`。
- [ ] 检查所有公开 Go 符号都有有效中文注释：可用 golint 等价工具时运行工具，并做人工抽查。
- [ ] 检查没有 Docker named volume：`make check-compose`。
- [ ] 检查 OpenAPI client 生成可用。
- [ ] 检查生成代码没有被手工修改。
- [ ] 最终提交后检查 `git status` 干净。
- [ ] 提交：`chore: final readiness cleanup`。

---

## 执行说明

- 严格在 task 边界提交。
- 如果任务暴露缺失的设计决策，先停止并更新规格，再实现。
- 不得为了推进进度而降低测试或浏览器验证要求。
- 如果 new-api 管理 API 不支持所需操作，记录缺失 API，按确认的替代路径实现，并为替代路径补测试。
- 如果 OpenClaw CLI 输出无法可靠解析，在 `runtime/openclaw` 内增加 JSON wrapper，并让测试面向 wrapper 契约。

计划完成。
