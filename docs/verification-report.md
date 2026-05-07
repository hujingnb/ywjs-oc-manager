# OpenClaw Manager 端到端验证报告

> 最新更新：v1.0 RC Chunk-1 真扫码闭环达成（2026-05-06）。
> 真实微信扫码绑定 → 微信发消息 → OpenClaw embedded agent → openai SDK →
> new-api → ollama qwen2.5:0.5b → write tool → /workspace/hello.txt →
> manager workspace API 单文件下载校验通过；spec §4.3 task 6 退出标准全过。
> 5 个 atomic commits 入主干（`5584c22` `a705816` `c2d4bfb` `83028fc` `044f6ed`），
> 加 Chunk-Z 的 3 个 commit（`02b0ab6` `0244404` `31a2e78`）共 8 commit。
> 当前分支：`master`。

## v1.0 RC Chunk 1 起始基线（2026-05-03）

承接 v1.0 RC 实施计划，开始执行端到端业务闭环前的基线验证。

### 自动化检查

| 命令 | 结果 | 备注 |
|---|---|---|
| `go test ./... -count=1` | ✅ 通过 | 全包单测通过。 |
| `go vet ./...` | ✅ 通过 | 全包零警告。 |
| `go build ./...` | ✅ 通过 | 全部 Go binary 可编译。 |
| `npm run typecheck`（web/） | ✅ 通过 | `vue-tsc --noEmit` 无报错。 |
| `npm test -- --run`（web/） | ✅ 通过 | vitest 1 个测试文件 / 8 个用例通过。 |
| `npm run build`（web/） | ✅ 通过 | 首次失败原因为 `web/dist` 是 root-owned 忽略产物；用 Docker 仅修正 `web/dist` 所有权后重跑通过。 |

### Compose 与迁移

| 步骤 | 结果 | 备注 |
|---|---|---|
| `docker compose up -d manager-postgres manager-redis manager-api manager-web oc-runtime-agent` | ✅ 通过 | 服务启动，PostgreSQL / Redis healthy。 |
| `make migrate-up` | ✅ 通过 | 迁移命令成功退出。 |
| `curl -fsS http://localhost:8080/healthz` | ✅ 通过 | 返回 `{"status":"ok",...}`。 |
| `go run ./cmd/seed-admin admin admin123 平台管理员`（compose run） | ✅ 通过 | `admin` 已存在，幂等跳过。 |

### chrome-devtools MCP 页面验证

| 路由 | 结果 | 备注 |
|---|---|---|
| `/login` | ✅ | 登录表单渲染，使用 `admin/admin123` 登录成功。 |
| `/` | ✅ | 平台管理员首页渲染，快捷入口正常。 |
| `/organizations` | ✅ | 组织列表渲染正常。 |
| `/runtime-nodes` | ✅ | Runtime Node 列表渲染正常。 |
| `/members` | ✅ | platform_admin 未关联组织时显示预期提示。 |
| `/apps` | ✅ | platform_admin 未关联组织时显示预期提示。 |
| `/knowledge` | ✅ | platform_admin 未关联组织时显示预期提示。 |
| `/audit-logs` | ✅ | platform_admin 未关联组织时显示预期提示。 |

Console 检查：主路由验证后仅保留 Vite debug 连接信息；`/favicon.ico` 404 为非关键静态资源缺失。

## Sprint 1+2 全栈 smoke（2026-05-03，approach C）

承接 Sprint 0 真扫码 POC + Sprint 1+2 主线代码合入后做的真容器端到端验证。

### 退出标准回归（Sprint 1 plan T16）

| spec §3 Sprint 1 退出标准 | 实测结果 |
|---|---|
| onboard → `apps.status=binding_waiting` | ✅ 实测 ~45 秒（22:24:40 → 22:25:35） |
| 节点上 `apps/{id}/{knowledge,workspace,state,logs}` 4 个子目录就位 | ✅（agent InitAppDirs） |
| 节点 `docker ps -f name=ocm-` 看到容器 | ✅ `ocm-542cabf4-... Up 5 seconds (healthy)` |
| 容器 5 个 bind mount 全挂载 | ✅ `/workspace` `/knowledge/{org,app}` `/state` `/logs` |
| 容器 9 个环境变量正确 | ✅ `OPENAI_API_KEY` / `OPENAI_BASE_URL` / `OPENCLAW_DISABLE_BONJOUR=1` / `OPENCLAW_SYSTEM_PROMPT` 三层 prompt |
| `WaitForOpenClawHealthy` 命中 | ✅ 容器 `(healthy)` + `curl /healthz` 返回 `{"ok":true,"status":"live"}` |
| 跨机演练 | ⏸ 单机 docker compose 已验证；多机演练留 Sprint 5 |

### Smoke 中暴露并修复的 3 类运行时错误（commit `1cde071`）

1. **manager-api 容器缺 docker CLI**：imageDistribution 走 LocalDockerCLIProvider，
   base 镜像 `golang:1.25-bookworm` 不含 docker。修法：docker-compose.yml manager-api
   command 启动时 `apt install -y docker.io`，并 mount `/var/run/docker.sock`。
2. **Go interface nil 陷阱导致 worker panic**：`var newapiClient *newapi.Client`
   在 `cfg.NewAPI.BaseURL` 为空时保持 nil，把 nil 传给 `NewAPIClient` interface 参数
   会变成"接口非 nil 但底层指针 nil"。修法：用 `handlers.NewAPIClient` /
   `handlers.APIKeyDisabler` interface 类型变量，仅在真客户端创建时赋值。
3. **worker panic 没栈难诊断**：`safeRecoverTick` 只 fmt.Errorf 没记 stack。
   修法：err 加 `runtime/debug.Stack()` 输出。

### Smoke 中暴露的 deployment 限制（留 Sprint 5 hardening）

1. **agent 自签证书 SAN 不含 docker compose 服务名 / 容器 IP**：agent 生成的 cert 只
   含 `localhost` + 容器 hostname + `127.0.0.1`，manager-api 容器从 `oc-runtime-agent:7001`
   走 TLS 失败。绕过：register 时用容器 hostname。
2. **agent state cert 持久化导致 hostname 变更后复用旧证书**：容器重建 hostname 变了
   但证书没重生。绕过：手工删 `agent-tls*` 文件触发重生成。

### 重要时序实测（Sprint 2 commit `5c9530d` `WaitForOpenClawHealthy` 验证）

单容器实测：docker run → `/healthz` 200 = **15 秒**。
WaitForOpenClawHealthy 配置 `startWait=8s + step=4s × 10`，命中第 3 次 probe（+16s），
对实测 15s healthy 留 29s buffer。

## 自动化检查（合入 Sprint 0/1/2 后回归）

| 命令 | 结果 | 备注 |
|---|---|---|
| `go test ./... -count=1` | ✅ 通过 | 20 个 Go 包，含 Sprint 0/1/2 新增测试 |
| `go vet ./...` | ✅ 通过 | 全包零警告 |
| `go build ./...` | ✅ 通过 | 全部 binary 干净编译 |
| `npm run typecheck`（web/） | ✅ 通过 | vue-tsc 无报错（含 qrcode 依赖） |
| `npm test -- --run`（web/） | ✅ 通过 | vitest 全绿 |

## 历史 Phase A+B+C smoke（2026-04-30，approach B）

## 自动化检查

| 命令 | 结果 | 备注 |
|---|---|---|
| `go test ./... -count=1` | ✅ 通过 | 20 个 Go 包，包含修复后的 audit_service_test 与 member_service_test。 |
| `go vet ./...` | ✅ 通过 | 全包零警告。 |
| `go build ./...` | ✅ 通过 | manager / migrate / runtime/agent / seed-admin 四个 binary 干净编译。 |
| `npm run typecheck`（web/） | ✅ 通过 | vue-tsc 无报错。 |
| `npm test -- --run`（web/） | ✅ 通过 | vitest 全绿。 |

## 端到端 Smoke（approach B）

通过 docker compose 起 manager 全栈 + chrome-devtools MCP + REST API 完成。

### 基础设施

| 步骤 | 结果 | 备注 |
|---|---|---|
| `docker compose up -d manager-postgres manager-redis manager-api manager-web` | ✅ | 健康检查通过。 |
| `go run ./cmd/migrate up` | ✅ | 跑到迁移版本 3，含 000003_agent_token_ciphertext。 |
| `go run ./cmd/seed-admin admin admin123 平台管理员` | ✅ | 创建 platform_admin 账号。 |
| `GET /healthz` | ✅ 200 | manager-api 在 ~5s 内 ready（air 增量重编译）。 |

### 浏览器主要路由（chrome-devtools MCP）

| 路由 | 加载 | console error | 备注 |
|---|---|---|---|
| `/login` | ✅ | 无 | 登录表单渲染、提交、登录后跳 `/`。 |
| `/`（RoleAwareHome） | ✅ | 无 | platform_admin 看到 3 张快捷卡片（组织/Runtime Node/审计）。 |
| `/organizations` | ✅ | 无 | 列表 + 新增按钮 + 弹窗表单 + 创建后立刻可见。 |
| `/runtime-nodes` | ✅ | 无 | 空列表 + 注册按钮。 |
| `/members` | ✅ | 无 | platform_admin 显示"未关联组织"提示（设计预期）。 |
| `/apps` | ✅ | 无 | 同上。 |
| `/knowledge` | ✅ | 无 | 渲染正常。 |
| `/audit-logs` | ✅ | 无 | 同上。 |
| `/dashboard` | ✅ | 无 | 兜底首页可访问。 |

### 业务事务流

| 步骤 | 结果 | 数据校验 |
|---|---|---|
| 创建组织 `测试组织 A`（浏览器 UI） | ✅ | 列表立即出现新行。 |
| 创建 `runtime-node-test-1`（POST `/runtime-nodes`） | ✅ | 返回 bootstrap_token。 |
| Agent register（POST `/agent/register`） | ✅ | 返回 agent_token + node_id；DB `runtime_nodes.agent_token_ciphertext` 写入 124 字节密文。 |
| Agent heartbeat（POST `/agent/heartbeat`） | ✅ | 节点 status 推到 `active`，`last_heartbeat_at` 更新。 |
| Onboard alice + alice-bot（POST `/organizations/{orgId}/members/onboard`） | ✅ | 单事务里写入 user/app/channel_binding/audit/job 全部成功。 |
| Worker 拉取 app_initialize job | ✅ | 看 `jobs.attempts` 在 5s tick 内增长到 2/3/...。 |
| 重试到终态 | ✅ failed | 5 次重试耗尽（attempts=5），错误明确："分发 OpenClaw 镜像失败" — 这是 approach B 边界外的预期失败：mock 节点没有真实 docker。 |
| 审计落库 | ✅ | `audit_logs` 一行 `actor_role=platform_admin / target_type=member / action=create_with_app / result=succeeded`。 |

## Smoke 中发现的问题与修复

| 症状 | 根因 | 修复 commit |
|---|---|---|
| manager-api 启动后 fail-fast `master_key 校验失败` | docker-compose 未把 `MASTER_KEY` 注入容器 | `277b4b0` |
| `go install air` 永久 timeout | 国内访问 proxy.golang.org 需要镜像 | `277b4b0`（GOPROXY=goproxy.cn） |
| air `error obtaining VCS status` | go build 默认开 buildvcs，挂载源码缺 `.git` 索引 | `277b4b0`（`.air.toml cmd` 加 `-buildvcs=false`） |
| onboard 返 500 `audit_logs_result_check` | service 写 `result='success'`，但 schema 约束只允许 `succeeded`/`failed` | `dc60450` |
| onboard 返 500 `jobs.run_after NOT NULL` | service 4 处 `CreateJob` 留 `RunAfter` Valid=false，sqlc 显式传 NULL 时不走 schema default | `dc60450`（统一 `time.Now()`） |
| 错误响应吞掉具体原因 | `writeMemberError` default 分支没记日志 | `dc60450`（加 log.Printf） |

## Approach B 的边界外项（已知，留 verification-report 跟踪）

- 容器创建 / 微信扫码 / 真实镜像分发 → 需要在节点上跑真实 `oc-runtime-agent` 容器并挂 `/var/run/docker.sock`，不在本轮 smoke 范围。
- platform_admin 角色在 `/apps`、`/audit-logs`、`/members` 看到"未关联组织"提示 → 当前前端 `effectiveOrgId = auth.user.org_id ?? prop.orgId`，platform_admin 没绑组织时 fallback 为空。后续 UX 优化方向：`?org_id=...` query 选择器或下拉切换。
- Phase A 妥协项（容器周期 health_check / 知识库 tar 全量同步）依赖真实 docker 环境，留 Phase B+ 做。
- Playwright E2E 6 场景未自动化（C2/C3）。

## 验收清单

- [x] 全量自动化测试绿
- [x] docker compose 起来 + 迁移到 v3
- [x] platform_admin 账号通过 seed-admin 命令幂等创建
- [x] 主要路由（9 个）chrome-devtools 验证无 JS 错
- [x] 创建组织 → 创建节点 → agent register/heartbeat → onboard 全链路通
- [x] agent token 加密入库（124 字节密文）
- [x] worker 实际拉取并处理 job（重试 / 失败终态行为正确）
- [x] 审计日志写入数据库且字段值符合 schema 约束
- [ ] 真实容器创建 / 微信扫码（依赖真实运维环境）
- [ ] platform_admin 跨组织查看应用 / 审计的 UX 优化

## 后续可演进项

- 多节点自动调度
- API/worker 进程拆分
- WebSocket / SSE 推送替代 polling
- master_key 自动轮换
- Prometheus metrics 与告警接入
- 集成测试套件扩展（refresh_token 生命周期、newapi httptest record/replay）
- platform_admin 跨组织视图（query 参数或下拉切换）

## v1.0 RC Chunk 2：渠道登录 worker 异步化

执行时间：2026-05-03 23:22 CST。

### 自动化验证

| 命令 | 结果 | 备注 |
|---|---|---|
| `go test ./... -count=1` | ✅ | 覆盖新增 channel_start_login / channel_check_binding worker handler 与 ChannelService 异步入队逻辑。 |
| `cd web && npm run typecheck` | ✅ | 渠道页从 progress metadata 渲染 challenge 的类型检查通过。 |
| `cd web && npm test -- --run` | ✅ | 前端单测 12/12 通过，含渠道 progress metadata 转 challenge 与 terminal 状态等待提示。 |
| `cd web && npm run build` | ✅ | Vite production build 通过。 |

### 浏览器验证（chrome-devtools MCP）

| 步骤 | 结果 | 备注 |
|---|---|---|
| 打开 `/apps/542cabf4-eec5-4333-ab64-436b9ffea3b5/channels` | ✅ | 渠道页渲染，初始状态为 `unbound`。 |
| 点击“发起登录” | ✅ | POST 成功后页面进入 `pending_auth`，显示“正在生成登录二维码…”。 |
| worker 处理 `channel_start_login` | ✅ failed | job 被 worker 实际消费并把 binding 写成 `failed`，说明 server registry 与队列通知链路已接通。失败原因为测试数据绑定旧 runtime endpoint：`lookup 6ffbbc520ecc on 127.0.0.11:53: server misbehaving`。 |
| failed 状态 UI | ✅ | 页面只展示最近错误，不再继续显示等待二维码。 |
| console 检查 | ✅ | 仅有既有 `favicon.ico` 404；未发现本次渠道页 JS error。 |

真实微信扫码未执行：当前本地测试应用缺少可用 OpenClaw 运行容器 / runtime endpoint，尚未生成二维码。后续具备真实 runtime container 后，需要继续验证扫码 bound 后 worker 写入 `bound_identity` 并把 app 从 `binding_waiting` 推进到 `running`。

## v1.0 RC Chunk 1：应用级知识库上传 / 删除

执行时间：2026-05-03 23:27 CST。

### 自动化验证

| 命令 | 结果 | 备注 |
|---|---|---|
| `cd web && npm run typecheck` | ✅ | 应用知识库上传 / 删除 hooks 与页面类型检查通过。 |
| `cd web && npm test -- --run` | ✅ | 前端单测 12/12 通过。 |
| `cd web && npm run build` | ✅ | Vite production build 通过。 |

### 浏览器验证（chrome-devtools MCP）

| 步骤 | 结果 | 备注 |
|---|---|---|
| 打开 `/apps/542cabf4-eec5-4333-ab64-436b9ffea3b5/knowledge` | ✅ | 应用知识库页渲染上传按钮、列表和操作列。 |
| 上传 `/tmp/ocm-app-knowledge-smoke.txt` | ✅ | POST `/api/v1/apps/{appId}/knowledge` 返回 204，列表刷新显示文件 `31 B`。 |
| 删除上传文件 | ✅ | DELETE 返回 204，列表刷新为空。 |
| console 检查 | ✅ | 仅有既有 `favicon.ico` 404；未发现应用知识库页面 JS error。 |

备注：后端应用级主副本写入后会派发 `knowledge_sync_node` 到应用所在节点；当前验证环境的 runtime endpoint 不可用，因此只验证了 manager 主副本和前端闭环，节点同步结果留真实 runtime 环境继续验证。

## v1.0 RC Chunk 1：工作目录 API 装配与下载路径修复

执行时间：2026-05-03 23:32 CST。

### 自动化验证

| 命令 | 结果 | 备注 |
|---|---|---|
| `go test ./... -count=1` | ✅ | 覆盖 WorkspaceService 路径清洗、下载代理、归档代理与 server wiring。 |
| `cd web && npm run typecheck` | ✅ | 工作目录 fetch + Blob 下载实现类型检查通过。 |
| `cd web && npm test -- --run` | ✅ | 前端单测 12/12 通过。 |
| `cd web && npm run build` | ✅ | Vite production build 通过。 |

### 浏览器验证（chrome-devtools MCP）

| 步骤 | 结果 | 备注 |
|---|---|---|
| 打开 `/apps/542cabf4-eec5-4333-ab64-436b9ffea3b5/workspace` | ✅ | 工作目录页渲染，API 路由已从 404 修复为实际进入后端 service。 |
| 查询 workspace | ⚠️ 500 | 当前测试应用指向旧 runtime endpoint，后端返回“工作目录暂不可用”；这是环境问题，非路由缺失。 |
| console 检查 | ✅ | 仅有既有 `favicon.ico` 404；未发现工作目录页 JS error。 |

真实文件下载未执行：需要应用容器运行在可达 runtime agent 上。后续真实微信消息生成 `hello.txt` 后继续验证列表、单文件下载和 zip 归档。

## v1.0 RC Chunk-Z：new-api 实例初始化（2026-05-04）

承接 v1.0 RC 收尾计划 Chunk-Z。

### 本 Chunk 范围扩大说明

按计划只是"前置初始化 + 探针验证"，实际探针提前发现 manager 与 new-api 鉴权契约错配（缺 `New-Api-User` header），属于 v1.0 RC 路径硬阻塞，故在本 Chunk 一并修复。

### 自动化检查

| 命令 | 结果 | 备注 |
|---|---|---|
| 清掉旧 new-api 数据并重启 `new-api / new-api-postgres / new-api-redis` | ✅ | 使用一次性 alpine 容器清空 `.local/data/new-api/{data,logs,postgres,redis}`，避免 sudo 污染宿主机 |
| `docker compose up -d` 全栈 | ✅ | 9 个服务全部 healthy |
| `docker exec ollama ollama list` | ✅ | `qwen2.5:0.5b 397 MB` 已就位，无需重新 pull |
| 用户在 new-api 浏览器后台完成首次配置 | ✅ | 注册 admin/admin123!（已存 memory）+ 创建 ollama 渠道 + 生成 access_token |
| 第一次探针（用 sk- 形式 token） | ❌ 8/8 | new-api 返回 `Unauthorized, invalid access token`；定位到 sk- 是 API token，admin API 需要"系统访问令牌"形式 |
| 第二次探针（access_token 但缺 New-Api-User） | ❌ 8/8 | new-api 返回 `Unauthorized, New-Api-User header not provided` — **暴露 manager bug** |
| 修复 manager `internal/integrations/newapi/client.go` + config + cmd/server wiring | ✅ | `Client` 增加 `AdminUserID`，`do()` 注入 `New-Api-User` header；`NewAPIConfig` 增加 `admin_user_id`；`NewClient` 新签名；`client_test.go` 增加正反断言 |
| `go test ./... -count=1` | ✅ | 全 21 个 Go 包通过 |
| `go vet ./...` | ✅ | 零警告 |
| `go build ./...` | ✅ | 全 binary 干净编译 |
| `npm run typecheck` | ✅ | vue-tsc 无报错 |
| `npm test -- --run` | ✅ | vitest 12/12 通过 |
| `npm run build` | ✅ | Vite production build 通过 |
| `make newapi-probe`（修复后） | ✅ | **8/8 通过** |
| `docker compose up -d --force-recreate manager-api` | ✅ | 注意：`docker compose restart` 不重新读 compose env，必须 `force-recreate`；之前 restart 一次失败 fail-fast `缺少环境变量: NEWAPI_ADMIN_TOKEN, NEWAPI_ADMIN_USER_ID`，迫使本 Chunk 文档明确这一陷阱 |
| `curl -fsS http://localhost:8080/healthz` | ✅ | 重新装配后 manager-api 启动正常 |

### new-api 接口能力清单

8 个 API 全部 200 + `success=true`。

### 决策

- **Chunk-Z 退出标准（≥6 / 8）**：✅ 8/8 全过。
- **Chunk-2 Task 9 / Chunk-3 Task 12 是否走 mock**：❌ 不需要，new-api 端能力齐全。
- **manager 仍待修复的 bug**（Chunk-Z 暴露但未在本 Chunk 修）：
  - `RechargeUser`（client.go:156）调 `/api/user/recharge`，new-api v1.0.0-alpha.1 实际是 `PUT /api/user/`（GET → 改 quota → PUT）
  - `CreateAPIKey`（client.go:71）的响应 `data.id` 在当前 new-api 版本上为 null，需要在创建后立即用 token name 走 `/api/token/?p=1&size=...` 回查 id 写回
  - 这两项修复列入 Chunk-1 范围（详见 PoC 文档 §4）

### Chunk-Z 改动文件

- 新建 `scripts/newapi-probe.sh`（探针脚本）
- 修改 `internal/integrations/newapi/client.go`（`Client.AdminUserID` + `New-Api-User` header）
- 修改 `internal/integrations/newapi/client_test.go`（断言 + 回归用例）
- 修改 `internal/config/config.go`（`NewAPIConfig.AdminUserID`）
- 修改 `cmd/server/main.go`（`NewClient` 调用增加 admin_user_id 参数）
- 修改 `config/config.yaml.example`（增加 `newapi:` 段）
- 修改 `docker-compose.yml`（`manager-api` 注入 `NEWAPI_ADMIN_TOKEN` + `NEWAPI_ADMIN_USER_ID`）
- 修改 `.env.example`（注释样例）
- 修改 `Makefile`（`newapi-probe` target）

`.env` 中的 `NEWAPI_ADMIN_TOKEN` 为本地 dev 实例的 access_token（不进 git）；`admin / admin123!` 凭证存于 `~/.claude/projects/.../memory/`。

### chrome-devtools MCP sanity

合入 master 后 manager-api 容器最终完成 `apt-get install docker.io` 与 air 编译并 ready（force-recreate 总耗时约 30 min，主要来自国内 deb.debian.org 网络），随后补做主路由 sanity：

| 路由 | 结果 | 备注 |
|---|---|---|
| `/login` | ✅ | 表单渲染正常；console 仅 1 个非关键 `favicon.ico` 404（与历史 verification-report 一致） |
| `/`（RoleAwareHome） | ✅ | 用 `admin / admin123` 登录成功；侧边栏 7 个导航 + 顶栏 PLATFORM_ADMIN tag + "API 正常" + "Ollama 待配置模型" + 3 张快捷卡片（组织 / Runtime Node / 审计）全部渲染 |
| 登录后 console 错误 | ✅ | 0 个 |

manager-api healthz 校验：`{"status":"ok","time":"2026-05-04T..."}`；容器内 `NEWAPI_ADMIN_TOKEN`、`NEWAPI_ADMIN_USER_ID`、`NEWAPI_BASE_URL` 三项 env 均正确注入。

会话踩坑笔记：`docker compose restart` **不**重新读 compose env，必须 `force-recreate` 让新增环境变量真正注入容器；这次踩坑迫使 manager-api 重新 apt-get install docker.io 30 min。Follow-up：把 `manager-api` 启动 command 中的 `apt-get install docker.io` 替换为预制 docker-CLI 层（baseline image 改造），消除每次 force-recreate 都重新 apt 的代价；放在 Chunk-4 hardening 范围。

## v1.0 RC Chunk-1：Task 6 真扫码闭环（2026-05-06）

承接 v1.0 RC 收尾计划 Chunk-1。

### 退出标准实测对账

按 v1.0 RC spec §4.3 退出标准 + Chunk-1 Verification Gate：

| 退出条件 | 实测结果 |
|---|---|
| 真实微信扫码绑定成功 | ✅ 多次扫码 `channel_bindings.status=bound`、`bound_identity=o9cq800xszCM8jyoS9YpRKpvAN9c@im.wechat`、`apps.status=running` |
| 真实微信消息能生成 workspace 文件并下载 | ✅ 消息 → embedded agent → openai SDK → new-api → ollama qwen2.5:0.5b → write tool → `/workspace/hello.txt`（21 字节 `OpenClaw workspace ok`） |
| Manager workspace API 列目录 | ✅ `GET /api/v1/apps/{id}/workspace?path=/` 返回 entries（agent file API 代理透传） |
| Manager workspace API 单文件下载 | ✅ `GET /api/v1/apps/{id}/workspace/file?path=hello.txt` → `OpenClaw workspace ok` |

### 实测时序

| 时间 | 事件 |
|---|---|
| 11:48:49 | 第三次 / 决定性扫码 → `bound_identity` 写入 |
| 11:48:53 | OpenClaw weixin plugin `[d2eee0e15483-im-bot]` 启动 + `accounts.json` 持久化 |
| 15:35:25 | 容器 `fce0c97412bf` 完整配置启动（OPENAI_API_KEY 51 字符 + oc-manager_default network + 完整 catalog） |
| 15:48 | hello.txt 写入 `.local/data/agent/apps/{id}/workspace/`（节点）+ 容器 bind mount 同步 |
| 15:55 | manager workspace API 列目录 + 下载校验通过 |

### Chunk-1 期间发现并修复的 manager / 部署 bug

10 个非平凡 bug 修复，分散在 5 个 atomic commit：

| Commit | 主题 | 关键修复 |
|---|---|---|
| `5584c22` | `fix(newapi): CreateAPIKey 不强制 key 字段` | new-api admin API 不返回完整 key（POST 不返回，GET 截断 18 字符），manager 不再依赖 admin API 拿 sk- token |
| `a705816` | `fix(runtime): hijack-over-https` | docker SDK ContainerExecAttach 走 hijack TCP 时把 `https` 当 network type 失败，改用 `tcp://host:port/v1/docker` 形式 + WithHTTPHeaders 注入 Authorization；删除 bearerProxyTransport |
| `c2d4bfb` | `fix(channel): plugin state fallback + 前端「刷新二维码」按钮` | weixin plugin 已授权账号重扫静默成功不 emit "bound" 事件，改读 plugin state 文件直接推 bound；前端按钮覆盖 expired 状态 |
| `83028fc` | `feat(openclaw): 容器内嵌 agent 模型链路全套 yaml 配置` | `openclaw.llm.{base_url,default_provider,default_model,openai_compat.api_key,container_networks}` 全套；agent scopes 加 openclaw-config + runtime/file PUT；buildContainerSpec mount models.json + 加 oc-manager_default network；ensureAPIKey 默认 unlimited_quota=true |
| `044f6ed` | `fix(web): API 401 自动清 token 跳 login` | client.ts 加 setUnauthorizedHandler；main.ts 注入 router.replace；避免 mutation 失败被业务组件 catch 吞掉 |

### 已知降级（写入 spec 范围之外，本 chunk 不修）

| 降级项 | 原因 | 长期方向 |
|---|---|---|
| **per-app token 隔离失效** | new-api admin POST/GET 不返回新建 token 完整 sk- key（只在 web UI 创建时一次性显示），manager 拿不到。dev 部署用 yaml 全局 sk- token，所有应用共用 | 待 new-api 提供"创建后短窗口可读完整 key"的 API 或 manager 与 new-api 共享 secret 服务 |
| **OpenClaw startup model warmup EBUSY warning** | file-level bind mount 不允许 rename target == mount point，OpenClaw warmup 报 `EBUSY: rename .json.tmp -> .json`，但 mode=replace 后用 openclaw.json providers，warmup 失败属 cosmetic | OpenClaw 上游不在 RW mount 文件上做 rename，或 manager 改 docker exec 注入而非 mount |
| **qwen2.5:0.5b 模型质量** | 0.5B 参数小模型理解中文指令偶尔选错 tool（如把"生成 hello.txt"理解成 video_generate）。链路本身完全打通 | 部署侧选更大模型（qwen2.5:1.5b / 7b 等） |
| **OpenClaw 容器网络硬编码 `oc-manager_default`** | docker compose project name 派生的默认 network；不同 project name 部署需修改 yaml | yaml `openclaw.container_networks` 已支持配置，部署文档需明确说明 |

### 自动化检查

| 命令 | 结果 | 备注 |
|---|---|---|
| `go test ./... -count=1` | ✅ | 21 个包，含本 chunk 新增 newapi / channel_login / app_initialize 单测 |
| `go vet ./...` | ✅ | 零警告 |
| `go build ./...` | ✅ | 全 binary 干净编译 |
| `npm run typecheck` | ✅ | vue-tsc 无报错 |
| `npm test -- --run` | ✅ | vitest 12/12 通过 |
| docker compose 全栈 healthy | ✅ | manager-api / oc-runtime-agent / new-api / new-api-postgres / new-api-redis / ollama / manager-postgres / manager-redis / manager-web 9 服务 |

### chrome-devtools MCP 主路由 sanity

复用 Chunk-Z sanity 结果（路由层未引入回归）；本 chunk 新增前端页面验证：

| 路由 | 结果 | 备注 |
|---|---|---|
| `/apps/:appId/channels` | ✅ | 二维码 PNG 渲染 + "刷新二维码"按钮 + 过期提示 + 状态 `bound + bound_identity` |
| `/apps/:appId/workspace` | ✅ | 渲染 hello.txt 文件项（21 字节）+ 单文件下载触发链 |

### Chunk-1 Verification Gate 结论

✅ 通过：spec §4.3 task 6 退出标准全部达成；本 chunk 完整 5 个 atomic commit 已合入主干；遗留 4 项降级均明确标注且不阻塞 v1.0 RC 业务可用性。

### Chunk-1 累计文件改动

10 个 modified + 0 个新增（不含此 verification-report）：

- 后端：`internal/integrations/{newapi/client.go,newapi/client_test.go,agent/docker_proxy.go,agent/file_client.go,runtime/agent_backed.go}`、`internal/worker/handlers/{app_initialize.go,app_initialize_test.go,channel_login.go,channel_login_test.go}`、`internal/config/config.go`、`cmd/server/main.go`、`runtime/agent/scopes.go`
- 配置：`.env.example`、`config/config.yaml.example`、`docker-compose.yml`
- 前端：`web/src/api/client.ts`、`web/src/main.ts`、`web/src/pages/apps/AppChannelsTab.vue`

### 引用契约信息

- new-api admin API 鉴权：`Authorization: Bearer {access_token}` + `New-Api-User: {user_id}`（参见 https://www.newapi.ai/zh/docs/api/management/auth）
- 当前 dev 部署的 new-api 全局 sk- token 写在 `.env` `OPENCLAW_LLM_OPENAI_API_KEY`（不进 git）

---

## v1.0 RC Chunk-2：组织治理（Task 7+8、Task 9）

承接 v1.0 RC 收尾计划 Chunk-2。

### Task 7+8：组织级知识库节点同步状态 + 前端重试入口

提交：`3ac7925 feat(knowledge): 组织级知识库节点同步状态 + 重试入口（Task 7+8）`。

| 检查项 | 结果 | 备注 |
|---|---|---|
| migration 0004 `knowledge_sync_status` | ✅ | (org_id, node_id) 主键 + status enum (pending/synced/failed) |
| sqlc Upsert/List 查询生成 | ✅ | `internal/store/queries/knowledge_sync_status.sql` |
| `KnowledgeSyncStatusService` | ✅ | MarkOrgNodePending/Synced/Failed + ListByOrg；500 字符级 last_error 截断 |
| dispatcher 入队即写 pending | ✅ | wiring 经 `knowledgeSyncStatusMarker` 接口注入；retry 触发器复用同 dispatcher |
| handler 路由 | ✅ | `GET /api/v1/organizations/:orgId/knowledge/sync-status`、`POST .../sync-status/retry` |
| 前端 `OrgKnowledgePage.vue` | ✅ | 4s 轮询、徽章三色（pending 黄 / synced 绿 / failed 红）+ 行级"重试同步"按钮 |
| `go test ./...` | ✅ | service + worker handler 全绿 |
| `vue-tsc --noEmit` | ✅ | 通过 |

### Task 9：用量四维度 + 跨组织视图

| 检查项 | 结果 | 备注 |
|---|---|---|
| 后端 `UsageProvider` 5s 进程内缓存 | ✅ | `usageCacheTTL = 5*time.Second`；`TestUsageServiceCachesAPIKey` 校验 3 次调用只命中 1 次 provider |
| `AppResult` 暴露 `NewapiKeyID int64` | ✅ | text 列解析；snapshotForApp 真用 token id 调 GetAPIKey |
| `GetPlatformUsage` 跨组织聚合 | ✅ | service 强制校验 `principal.Role == platform_admin`；遍历所有组织所有应用 |
| `GET /api/v1/usage/platform` | ✅ | curl 返回 `{scope:"platform", total_remain_quota:-1865, apps:[...]}`，包含 4 个应用快照 |
| `GET /api/v1/usage/organizations/:orgId` 真数据 | ✅ | 测试组织 A 返回 3 个应用、smoke-org-2 返回 1 个，token 13 quota=-1865 与 new-api 一致 |
| `newapi.GetAPIKey` 把 `record not found` 映射 ErrNotFound | ✅ | `TestGetAPIKeyMapsRecordNotFound`；解决"被回收 token 让聚合 5xx"问题 |
| 前端 `UsagePage.vue` 4 维度 tab | ✅ | platform_admin 默认 platform tab；组织 / 成员 / 应用 / 平台四个按钮可切 |
| platform_admin 跨组织切换器 | ✅ | useOrganizationsQuery 拉到列表后 watch 自动选第一项；切换"测试组织 A"→ 合计 -1865，切换"smoke-org-2"→ 合计 0 |
| 边栏"用量"入口 | ✅ | `BarChart3` 图标，所有角色可见 |

### chrome-devtools MCP 路由验证

| URL | 结果 | 备注 |
|---|---|---|
| `/usage` | ✅ | platform_admin 进入即看到 platform 视图，4 个应用真数据；切换组织 tab + 下拉切到不同 org 后表格刷新 |

### Chunk-2 已知降级 / 遗留

- 用量缓存粒度仅在单 token 维度（5s），未做"按 org 整体缓存"。短期 UI 4s/8s 轮询已能从首次拉取受益；后续如果出现 token 数 100+ 影响首屏，可在 service 增加 org-level 缓存。
- 应用维度 tab 当前仅给指引（"前往应用列表"）；详细页用量信息已挂在 `/apps/:appId/overview` 的 useAppUsage 里，不再做重复入口避免割裂。
- new-api admin token 失效后由 ops 在 new-api `/api/user/token` (GET 带 cookie + `New-Api-User: 1` header) 手工生成；写到 `.env` `NEWAPI_ADMIN_TOKEN` 后 `docker compose up -d --force-recreate manager-api` 即可热加载。本次会话发现旧 token 已被 new-api 撤销，已替换为新值。

### Chunk-2 Verification Gate 结论

✅ 通过：spec §5.3 Chunk-2 退出标准（Task 7+8 sync-status 流转 + Task 9 4 维度 page 真数据）全部达成；chrome-devtools MCP 端到端验证完成。

---

## v1.0 RC Chunk-3：运维增强（Task 10 进度中）

承接 v1.0 RC 收尾计划 Chunk-3。

### Task 10：runtime_refresh_status + 资源监控 UI

提交：`43f4637 feat(runtime): runtime_refresh_status 周期采样 + 资源监控 UI（Task 10）`。

| 检查项 | 结果 | 备注 |
|---|---|---|
| migration 0005 `apps.runtime_snapshot_json/at` | ✅ | jsonb + timestamptz；nullable，未采集即 NULL |
| sqlc `ListRunningApps` / `SetAppRuntimeSnapshot` | ✅ | docker generate sqlc/sqlc:1.30.0 |
| `RuntimeAdapter.ContainerStats` 接口 | ✅ | docker SDK `ContainerStatsOneShot` + 推荐公式算 CPU%、所有 networks 累加 |
| worker handler `runtime_refresh_status` | ✅ | inspect + stats，任一失败仅写 `last_error` 不阻断；`go test ./internal/worker/handlers/...` 三个新用例全绿 |
| scheduler 30s 周期 dispatcher | ✅ | `runtimeRefreshDispatcher.Tick`；`PeriodicReconciler` 复用 |
| `GET /apps/:appId/runtime` 返回 snapshot | ✅ | RuntimeView 增 `Snapshot` 字段；container_id 为空时 nil |
| 前端 `AppRuntimeTab` 资源 4 格 | ✅ | CPU 1.1% / 内存 604.8 MB / 网络 RX 775.4 KB / TX 1.5 MB（chrome-devtools 实测真容器） |

### Task 11：app_health_check + 自动重启策略

提交：`4f2b61b`。

| 检查项 | 结果 | 备注 |
|---|---|---|
| migration 0006 `apps.restart_policy_json/health_state_json` | ✅ | 默认 mode=on_failure / max_per_window=5 / window_seconds=600 |
| `RuntimeAdapter.ContainerExec` 接口 | ✅ | docker SDK ContainerExec{Create,Attach,Inspect}；exit code + stdout 截断 4KB |
| worker handler `app_health_check` | ✅ | curl OpenClaw `/healthz`；失败入队 app_restart_container；超窗口推 status=error；mode=none 跳过 |
| scheduler 60s 周期 dispatcher | ✅ | `healthCheckDispatcher.Tick`；与 runtime_refresh 共用 enqueuePerRunningApp |
| 单测 5 条 | ✅ | success / failure_triggers / exhausted_budget / exec_error / mode_none 全绿 |
| 真容器周期跑通 | ✅ | postgres 验证 `last_success_at` 持续刷新 |

### Task 12 部分：API key 禁用 / 恢复 风控

提交：`8df8f81 feat(apikey): 应用 API key 禁用 / 恢复 风控（Task 12 部分）`。

| 检查项 | 结果 | 备注 |
|---|---|---|
| worker `NewAPIKeyStatusHandler`（disable/restore） | ✅ | newapi.SetAPIKeyStatus + 同步 apps.api_key_status |
| `RuntimeOperationDisable/RestoreAPIKey` 枚举 | ✅ | 复用 Trigger 入队 + 审计写入；普通成员被 service 拒绝 |
| handler `POST /apps/:appId/api-key/{disable,restore}` | ✅ | 沿用 trigger() 路径 |
| 前端 Overview tab 状态徽章 + 按钮 | ✅ | 禁用走 ConfirmActionModal；恢复直接触发 |
| chrome-devtools 端到端验证 | ✅ | apps.api_key_status: active→disabled→active；new-api token 13 status: 1→2→1 |

### Task 12 收尾：平台总览页 PlatformDashboardPage

| 检查项 | 结果 | 备注 |
|---|---|---|
| sqlc CountActive{Organizations,Users} / CountAppsByStatus | ✅ | users 无 soft-delete，按 status='active' 过滤；platform_admin 不计入成员 |
| service.PlatformOverviewService | ✅ | 聚合 5 项计数 + 复用 UsageService.GetPlatformUsage 拿总余额；usage 不可用降级 |
| `GET /api/v1/platform/overview` | ✅ | 仅 platform_admin；service 双层校验 |
| 前端 `PlatformDashboardPage` + 边栏「平台」入口 | ✅ | 6 格 10s 轮询；非平台管理员显示「仅平台管理员可访问」 |
| chrome-devtools 端到端 | ✅ | 组织 2 / 成员 4 / 应用 4 / 运行 1 / 异常 0 / 总余额 -3,531 |

### Chunk-3 已知遗留

- spec §5.4 要求"5 类高风险操作统一走 ConfirmActionModal 二次确认（删除应用 / 停止容器 / 重置密码 / 充值 / 禁用 api_key）"，本次仅禁用 api_key 的二次确认就位，其余 4 类已有的 modal 增加"输入应用名 / 组织名"等强校验项留作 Chunk-4 hardening 工作。
- Task 11 自动重启实测尚未做"docker exec ocm-{app_id} kill 1"破坏性测试；当前仅验证 60s 周期成功探针写入。后续 Chunk-4 终检时补做。

### Chunk-3 Verification Gate 结论

✅ 通过：spec §5.4 Chunk-3 三项任务全部交付（Task 10/11/12）；上述两个非阻塞遗留分别归属 Chunk-4 hardening 与 Chunk-4 终检。

---

## v1.0 RC Chunk-4：商用 hardening 与发布验收

承接 v1.0 RC 收尾计划 Chunk-4。

### Task 13：CSRF + refresh token 生命周期

提交：`708cfe3 feat(security): CSRF double-submit cookie + refresh token 生命周期 E2E（Task 13）`。

| 检查项 | 结果 | 备注 |
|---|---|---|
| `internal/api/middleware/csrf.go` RequireCSRF | ✅ | safe methods 直接放行；agent / login / refresh 路径白名单豁免；opt-in 模式向后兼容 |
| 7 条 middleware 单测 | ✅ | safe / opt-in / cookie 缺 header / header mismatch / header match / agent 豁免 / login 豁免 |
| login + refresh handler set csrf_token cookie | ✅ | 取 access token 末 32 字符，HttpOnly=false 让前端 JS 读取 |
| 前端 apiRequest 自动注入 X-CSRF-Token | ✅ | 写操作时 readCookie 写 header；GET/HEAD/OPTIONS 跳过 |
| refresh token E2E 单测 | ✅ | 已有 Login / Refresh rotation / Logout idempotent；新增 Refresh expired token / Refresh rotated token 拒绝 |
| chrome-devtools 实测 | ✅ | 登录后 `document.cookie` 含 `csrf_token=cAsNsDbKBNU8...`；platform dashboard 仍能正常加载 |

### Task 14：日志脱敏 + 错误响应去敏

提交：`feat(log): 日志脱敏 + 错误响应去敏（Task 14）`。

| 检查项 | 结果 | 备注 |
|---|---|---|
| `internal/log/redact.go` RedactSecrets | ✅ | 7 类敏感字段（password / api_key / bootstrap/agent/refresh/access_token / master_key）+ Bearer / sk- 前缀 |
| RedactingWriter io.Writer 包装 | ✅ | manager 启动时包到 logger，所有 log.Printf 自动脱敏 |
| `internal/log/safe_error.go` SafeErrorMessage | ✅ | 去 .go 文件路径 / SQL 片段 / 200 字符截断 |
| 6 处 handler 替换 err.Error() | ✅ | recharge / agent / knowledge / runtime_nodes / members / auth |
| master_key fail-fast 单测 | ✅ | TestLoad_RejectsMissingMasterKey / RejectsShortMasterKey / RejectsBadBase64MasterKey 早已就位 |
| 12 + 5 条单测 | ✅ | 7 类 redact + Writer 长度契约 + SafeError 5 种降级路径 |

### Task 15：Playwright 6 场景

提交：将随 Task 16 一并入库。

| 检查项 | 结果 | 备注 |
|---|---|---|
| @playwright/test 安装 | ✅ | devDependency 1.59.1，无 npm audit 漏洞 |
| `web/playwright.config.ts` | ✅ | chromium project；baseURL 走 PLAYWRIGHT_BASE_URL env |
| 6 场景脚手架 | ✅ | login（已可跑）/ organizations / runtime-nodes / members / app-detail / delete-cascade（5 个标 test.skip 留 fixture 接入） |
| `npm run test:e2e` script | ✅ | 含 test:e2e:install 装 chromium |
| vitest 与 playwright 共存 | ✅ | vite.config.ts 加 `test.exclude: ['tests/e2e/**']`，vitest 仍 12 用例全绿 |

**已知降级**：5 个非 login 场景因为依赖预置 fixture（组织 / 节点 / 成员 / 应用 ID）暂标 test.skip。CI 接入时由 fixture seed 脚本预置后去 skip，本次仅完成脚手架就位。

### Task 16：本机两 agent 模拟跨节点 + 安全扫描 + 部署文档

提交：将随当前 commit 一并入库。

| 检查项 | 结果 | 备注 |
|---|---|---|
| `deploy/docker-compose.two-agent.yml` | ✅ | override 文件，跑 `docker compose -f docker-compose.yml -f deploy/docker-compose.two-agent.yml up -d` 同时起两个 agent；7003/7004 端口 + 独立 .local/data/agent-2 目录 |
| `deploy/README.md` 跨机部署 | ✅ | 节点 OS 准备 / TLS SAN / 防火墙 / manager 端注册 / 跨机演练步骤 |
| `deploy/backup.md` | ✅ | postgres pg_dump / Redis BGSAVE / manager-knowledge tar / agent rsync / 季度灾难恢复演练流程 |
| `deploy/upgrade.md` | ✅ | SemVer / 升级 5 步 / 紧急回滚 / 镜像 tag 管理 / 常见问题 |
| `npm audit --omit=dev` | ✅ | 0 vulnerability（registry 用官方源，国内 npmmirror 不支持 audit endpoint） |
| `gosec ./...` | ⚠️ | 14 high 全部在 false positive 范围：runtime/agent/scopes.go 路径拼接受控、sqlc 生成代码硬编码 SQL 字符串误判 G101、auth/password.go argon2 参数 int→uint32 转换误判 G115。无真实安全风险，记录但不阻断 release |

### Chunk-4 已知降级 / 遗留

- Task 15 e2e 5 场景留 test.skip：CI 接入前需写 fixture seed 脚本（创建测试组织 / 成员 / 节点 / 应用），seed 后去 skip。
- spec §5.4 五类高风险操作的 ConfirmActionModal 强校验项（输入应用名 / 组织名 / 成员用户名）尚未做：当前仅二次确认按钮，未做强校验输入。留作 v1.0 GA 前的最后一道 hardening。
- Task 11 自动重启 `docker exec ocm-{app_id} kill 1` 破坏性实测尚未做：scheduler 60s 周期已实测正常路径，破坏性测试需要专门窗口。
- 跨机演练只到 compose override 就位，未实际两节点同时跑。

### Chunk-4 Verification Gate 结论

✅ 通过：spec §5.4 Chunk-4 四项任务核心交付物全部入库（CSRF + refresh E2E / 日志脱敏 + 错误去敏 / Playwright 脚手架 / 部署运维三件套 + 跨节点 compose override）。三项遗留均明确标注且不阻塞 v1.0 RC 业务可用性。

---

## v1.0 RC 总验收

| Chunk | 任务 | 状态 |
|---|---|---|
| Chunk-Z | new-api 实例初始化 | ✅ |
| Chunk-1 | Task 6 真扫码闭环 | ✅ |
| Chunk-2 | Task 7+8 知识库 sync-status / Task 9 用量四维度 | ✅ |
| Chunk-3 | Task 10 资源监控 / Task 11 健康检查 + 自动重启 / Task 12 API key 风控 + 平台总览 | ✅ |
| Chunk-4 | Task 13 CSRF / Task 14 日志脱敏 / Task 15 Playwright 脚手架 / Task 16 部署文档 + 安全扫描 | ✅ |

**v1.0 RC 推进至 release 候选状态。** 后续待 ops 决策：
1. UAT 排期与 fixture seed 脚本（解锁 Playwright 5 场景）；
2. 跨机真实部署演练（解锁两节点端到端）；
3. 上线前最后一道 ConfirmActionModal 强校验补做（保护误删 / 误停操作）。

---

## v1.0 GA：4 项遗留收尾 + 完整端到端验收（2026-05-07）

承接 v1.0 GA 收尾设计与实施计划。
本次 GA 收尾共 5 个 atomic commit 入主干：T1 / T2a / T2b / audit-fix（实测暴露的
backend bug，最小修复）/ T5 docs。

### Commit 链

| commit | 范围 | 备注 |
|---|---|---|
| `109050f` | T1 — ConfirmActionModal 强校验输入名（5 调用点） | 删除应用 / 停止容器 / 重置成员密码 / 组织充值四类接入 verifyValue；禁用 api_key 保留简单二次确认；4 条 vitest 单测 |
| `8952536` | T2a — `cmd/seed-e2e` 命令 + Makefile 目标 | OCM_E2E=1 守门 + truncate + fixture JSON 输出 |
| `cc303dd` | T2b — Playwright 5 场景去 skip + globalSetup | 6 场景全绿（含 login）；members + delete-cascade 顺带覆盖 T1 |
| `348feec` | audit-fix — runtime_operation_service `Result: submitted` → `succeeded` | 实测暴露 backend bug：audit_logs.result CHECK 仅允许 succeeded/failed，submitted 让 runtime API 全部返回 500 |
| 本节 docs commit | T5 — GA 验收报告追加 | 见下文 |

### T1 ConfirmActionModal 强校验（5 调用点）

| 调用点 | verifyValue 接入 | 验证 |
|---|---|---|
| 删除应用（AppsPage） | ✅ 应用名 | Playwright delete-cascade 输错→disabled / 输对→enabled |
| 删除应用（AppRuntimeTab 重复入口） | ✅ 应用名 | 同上 |
| 停止容器（AppRuntimeTab 新增 modal） | ✅ 应用名 | 新增 confirmStop ref + 拦截 onAction('stop') |
| 重置成员密码（MembersPage 新增 UI + modal） | ✅ 成员登录名 | Playwright members.spec.ts 双向校验通过 |
| 组织充值（RechargePage 新增 modal） | ✅ 组织名 | 新增 confirmRecharge modal + 拦截 onSubmit |
| 禁用 api_key（AppOverviewTab） | ⚠️ 显式不接（可逆操作） | 保留简单二次确认 |

vitest 4 条新增用例全绿（`web/src/components/__tests__/ConfirmActionModal.spec.ts`）。

### T2 seed-e2e + Playwright 5 场景解锁

| 检查项 | 结果 | 备注 |
|---|---|---|
| `cmd/seed-e2e` + Go 守门单测 | ✅ | OCM_E2E=1 守门验证；commit `8952536` |
| `make seed-e2e` 幂等 | ✅ | 多次连跑都打印合法 fixture JSON |
| `globalSetup.ts` + `fixtures.ts` | ✅ | OCM_E2E_FIXTURE 注入；E2EFixture id 类型为 string（schema 是 UUID，不是 plan 草稿写的 number） |
| 5 场景去 `test.skip` | ✅ | organizations / runtime-nodes / members / app-detail / delete-cascade |
| `npm run test:e2e` 全绿 | ✅ | 7 passed（login 2 + 解锁 5） |

T2a 实施时纠正 plan 偏离：UUID 主键、`org_id`（非 organization_id）、`agent_file_endpoint`
单数、`node_data_root`、`apps.persona_mode='org_inherited'`、`apps.owner_user_id` NOT NULL、
truncate 列表加 `refresh_tokens` 解外键依赖、`organizations` 改 `DELETE FROM` 防止
级联清掉 platform_admin。

### T3 自动重启破坏性实测

前置：注册真节点 t3-real-node + onboard 真应用 ga-app（org_admin=ga-admin / org=ga-org）+
完成微信扫码绑定（用户人工介入一次）后 status=running，容器 healthy。

时序：

| 时间（UTC） | 事件 | 证据 |
|---|---|---|
| 07:34:00 | `docker kill ocm-3a956600-...`（exit 137） | docker ps Exited (137) 1 second ago |
| 07:34:33.49 | dispatcher 触发 app_health_check | jobs 表 succeeded |
| 07:34:33.69 | health_check 检测失败、写 health_state_json + 入队 app_restart_container | failures + restarted_at + last_failure_at 写入 |
| 07:34:33.70 | app_restart_container job succeeded | jobs 表 |
| 07:34:35 | 容器 Up 1s (health: starting) | docker ps |
| 07:34:47 | 容器 Up healthy | T1 |

总耗时 **47 秒**（spec 期望 60-180s 内，超额完成）。

`apps.health_state_json` 字段：

```
{"failures": ["2026-05-07T15:34:33.698216667+08:00"],
 "last_error": "创建 exec 失败: container is not running",
 "restarted_at": ["2026-05-07T15:34:33.698216667+08:00"],
 "last_failure_at": "2026-05-07T15:34:33.698216667+08:00"}
```

> 实测发现 design 边界：`app_health_check` 仅在 `apps.status='running'` 时执行
> 探针；binding_waiting 时直接 return 跳过（避免假阳性）。这意味着 T3 必须先
> 完成扫码绑定推到 running 才能验破坏性。

### T4 双 agent 仿跨机演练

`docker compose -f docker-compose.yml -f deploy/docker-compose.two-agent.yml` 起两 agent。

| 检查项 | 结果 | 备注 |
|---|---|---|
| 双 agent 容器都 healthy | ✅ | 端口 7001/7002（agent-1） + 7003/7004（agent-2） |
| 注册两节点 status=active | ✅ | t3-real-node + t4-node-b |
| 组织级知识库上传 → sync_status 双节点 synced | ✅ | t3-real-node + t4-node-b 都 synced（同一 updated_at 15:43:28） |
| 两应用分两节点真容器 | ⏸ | 仅 fixture app 行（t4-app-b 直接 SQL 创建，无真容器）；真容器需再走一遍 onboard + 扫码 |

实测暴露的 design gap（明示，留 ops 后续）：

1. **agent 无主动心跳代码**：register 后节点 status=active，但没有 agent → manager 心跳逻辑。
   `NodeHealthReconciler` 周期把 last_heartbeat_at 太旧的节点标 unreachable，恢复要靠
   "agent 重新发心跳"——但代码路径不存在。本次实测中需 SQL 强制 status=active 才能让
   `DispatchOrgChange` 不跳过节点。
2. **双 agent 默认共享 `./config/agent.yaml`**：导致 agent token 互相覆盖。本次给
   agent-2 用独立 yaml（`/var/lib/oc-agent/agent.yaml`）+ 各自 token 重启容器才让
   两节点都通过 agent token 验证。`deploy/docker-compose.two-agent.yml` 后续应增加
   独立 yaml mount 或 OC_AGENT_CONFIG env 区分。
3. **跨真实物理机部署未演练**：本次仅在同一宿主 docker network 下双 agent；跨真机的
   网络分区、TLS SAN 跨子网、agent token 跨子网传输等场景未覆盖。

### T5 GA 验收

#### 自动化全套

| 命令 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全包通过 |
| `go vet ./...` | ✅ 0 警告 |
| `go build ./...` | ✅ 全 binary 编译通过 |
| `npm run typecheck` | ✅ 0 错误 |
| `npm test -- --run` | ✅ 16/16（12 旧 + 4 新 ConfirmActionModal） |
| `npm run build` | ✅ dist 273 kB |
| `make seed-e2e`（幂等） | ✅ 多次连跑均输出合法 fixture JSON |
| `npm run test:e2e` | ✅ 7 passed（login 2 + 解锁 5） |

#### chrome-devtools MCP 路由验证

本次 GA 验收覆盖路径：

- platform_admin（admin / admin123）：登录 → /organizations → /runtime-nodes → /apps → /usage → /audit-logs。
- org_admin（ga-admin）：登录 → 组织总览 → /members → 创建并初始化 → 应用详情 5 tab → /knowledge → /usage。
- org_member 视角已经在 onboard 流程中通过应用详情页 channels tab 触发渠道绑定。

5 类强校验路径（spec §3.1.3）实测覆盖：

| 路径 | 验证方式 |
|---|---|
| 删除应用 | Playwright delete-cascade.spec.ts：输错名 disabled / 输对名 enabled / 提交触发 |
| 停止容器 | T1 实现验证（confirmStop ref + 拦截 onAction）+ vitest 4 条用例覆盖 verifyValue 行为 |
| 重置成员密码 | Playwright members.spec.ts：输错→disabled / 输对→enabled / 提交成功 |
| 组织充值 | T1 实现验证（confirmRecharge modal + 拦截 onSubmit）+ vitest 4 条 |
| 禁用 api_key | 保留简单二次确认（spec §3.1.3 决策） |

#### 真扫码人工介入（已完成）

| 项 | 实测值 |
|---|---|
| 二维码渲染 | ✅ chrome-devtools `/apps/{id}/channels` 页面 base64 PNG 显示，metadata_json 含 wechat URL + expires_at |
| 用户扫码 | ✅ 第二次触发后用户在 5 分钟窗口内完成扫码 |
| `channel_bindings.status` | bound |
| `channel_bindings.bound_identity` | `o9cq800xszCM8jyoS9YpRKpvAN9c@im.wechat` |
| `channel_bindings.channel_name` | `openclaw-weixin` |
| `apps.status` | running（自动从 binding_waiting promote） |
| `bound_at` | 2026-05-07 15:33:33+08 |

### 实测中暴露并修复的 backend bug（commit `348feec`）

`internal/service/runtime_operation_service.go` 在 `Trigger`（line 247）和
`RequestInitialize`（line 332）写 audit_logs 时 Result 字段用 `submitted`，但
`audit_logs.result` CHECK 仅允许 `succeeded` / `failed`。后果：

- POST `/apps/:id/runtime/{start|stop|restart|delete}` → 500
- POST `/apps/:id/api-key/{disable|restore}` → 500
- POST `/apps/:id/initialize` → 500（v1.0 RC verification 跑过的某些路径会撞这个）

修复：与 `onboarding_service` / `member_service` 等其他 service 对齐，改用 `succeeded`
表示「操作已成功提交入队」，操作真实执行结果由 jobs 表与 apps.status 体现。

引入 commit：`0a5ad04 feat(service): 应用启停删除入队后立即推 Redis 信号`（v1.0 RC 期间）。

修复后 audit_logs 实测写入 `result=succeeded`；e2e delete-cascade 跑通；T3 破坏性实测
全程顺畅；T5 chrome-devtools 路由验证无关键 console error。

### v1.0 GA 总验收

| Chunk | 任务 | 状态 |
|---|---|---|
| T1 | ConfirmActionModal 强校验（5 调用点） | ✅ |
| T2a | seed-e2e 命令 + Makefile | ✅ |
| T2b | Playwright 5 场景解锁 + globalSetup | ✅ |
| T3 | 自动重启破坏性实测（kill→47s 自动恢复） | ✅ |
| T4 | 双 agent 仿跨机（双节点 sync_status synced） | ✅（含 3 项明示 design gap） |
| T5 | 自动化全套 + chrome-devtools + 真扫码 | ✅ |
| audit-fix | runtime_operation_service Result 修正 | ✅ |

**✅ 通过：spec §6.3 退出标准全部达成（含 4 项 RC 遗留收尾），无 P0/P1。v1.0 GA 完成。**

后续运维事项不阻塞 GA：

- 跨真实物理机部署演练待 ops 排期（T4 已在本机覆盖双节点 sync）。
- agent → manager 主动心跳能力补齐（design gap）。
- `deploy/docker-compose.two-agent.yml` 加独立 yaml mount 让 agent token 不互相覆盖。
- 两应用分两节点真容器实测留作 ops 跨机演练时一并补做。
