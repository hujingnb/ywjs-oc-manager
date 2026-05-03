# OpenClaw Manager 端到端验证报告

> 最新更新：Sprint 1+2 全栈 smoke 跑通到 binding_waiting + healthy 容器（2026-05-03）。
> 在此基础上 Sprint 0 OpenClaw 集成契约、Sprint 1 agent 文件 API 全套、
> Sprint 2 plugin state userId / QR PNG / workspace scope / app_delete archive /
> WaitForOpenClawHealthy / status running 联动均已合入主分支。
> 起始基线：commit `c7f46a5`；当前分支 `feat/phase-a-container-channel-loop`。

## v1.0 RC Chunk 1 起始基线（2026-05-03）

承接 `docs/superpowers/plans/2026-05-03-openclaw-manager-v1-rc-implementation-plan.md`
开始执行端到端业务闭环前的基线验证。

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
