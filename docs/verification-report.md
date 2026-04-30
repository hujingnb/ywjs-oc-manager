# OpenClaw Manager 端到端验证报告

> 最新更新：Phase A + B + C 全部 sub-phase 完成 + 端到端 smoke 跑通至业务事务流。  
> 起始基线：commit `c7f46a5`；当前分支 `feat/phase-a-container-channel-loop`。

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
