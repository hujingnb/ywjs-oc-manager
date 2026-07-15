# AICC 实现反查覆盖表

- 历史基线：`1726141080159c3e3b8842b7b72370095a583b1e`
- 历史表范围：下表原始条目用于记录基线时的 AICC 业务面，不等同于本轮客服能力实施状态；其中 `BASELINE-FAIL` 仅表示 Task 1 当时的预期差异。覆盖表只说明代码映射和已执行的局部命令，**需求验收状态以 `aicc-conversation-requirement-matrix.md` 为唯一权威来源**。
- 规则：每个当前分支相对 `master` 的 AICC 实现入口必须映射到需求矩阵；生成的 sqlc、OpenAPI 与前端类型跟随其源文件验证。

## 当前客服能力变更（2026-07-16）

| 实现入口 | 本轮职责 | 需求矩阵 ID | 当前证据 | 状态 |
|---|---|---|---|---|
| `runtime/hermes/hermes-aicc/aicc_tools/*`、`patches/patch_aicc_tool_policy.py`、`lib/manifest.py`、`Dockerfile` | 不可变工具白名单、伪造调用拒绝、受控知识工具与客服镜像能力裁剪 | AICC-CAP-001..004 | `pytest -q runtime/hermes/hermes-aicc/tests/test_render_config_yaml.py runtime/hermes/hermes-aicc/tests/test_aicc_tool_policy.py`：23 passed | CAP-001 已验证；CAP-003/004 仍以矩阵 PENDING 为准 |
| `internal/config/platform_prompt.go`、`platform_prompt_test.go` | 审核客服 Skill 白名单与通用能力回退禁止 | AICC-CAP-002 | `go test ./internal/config -run TestPlatformPrompts_Invariants -count=1` | PASS |
| `internal/service/aicc_{context,response,channel,intent,dispatcher,public_chat}.go` 及测试、`internal/migrations/000037_*`、`000038_*` | 无状态上下文、来源白名单、结构化动作、意向证据/重试、会话渠道与一次邀约 | AICC-SRC-*、AICC-INT-*、AICC-STATE-*、AICC-BOOT-001、AICC-CH-* | `go test ./internal/service -run 'TestAICCDispatcherBuildsTurnFromDatabaseContext|TestEnableLocalAICCIntentFailureOnce|TestAICCIntentRetryLeaseBehavior' -count=1` | PASS（单元范围） |
| `runtime/hermes/hermes-aicc/{oc-entrypoint.py,entrypoint_helpers.py,renderer/*,lib/atomic.py}`、`tests/test_entrypoint_integration.py`、`test_render_*.py` | 无状态幂等启动、临时渲染、Skill/配置裁剪 | AICC-BOOT-002..004、AICC-CAP-002 | `PYTHONPATH=runtime/hermes/hermes-aicc pytest -q runtime/hermes/hermes-aicc/tests` | 已映射；端到端 Pod 重建仍以矩阵 BLOCKED 为准 |
| `internal/integrations/k8sorch/*`、`internal/service/bootstrap_service.go`、`internal/api/handlers/runtime_knowledge.go`、`internal/service/knowledge_service.go` 及测试 | AICC 容器约束、运行时知识只读检索、AICC token 写拒绝 | AICC-CAP-003、AICC-BOOT-004、AICC-SRC-001 | `go test ./internal/service ./internal/api/handlers -run AICC -count=1` | 已映射；CAP-003 验收仍以矩阵 PENDING 为准 |
| `internal/worker/aicc/{message_dispatch_loop,retention_loop}.go` 及测试 | 异步消息、意向重试、任务租约和保留期清理 | AICC-INT-001..003、AICC-BOOT-001、AICC-RETENTION-01 | `go test ./internal/worker/aicc -run AICC -count=1` | PASS（worker 单元范围） |
| `cmd/server/main.go`、`internal/service/aicc_dispatcher.go` | 仅 local 可用的一次性意向失败/暂停控制面 | AICC-INT-001..003 | 上述 Go 测试；Chrome 实跑仍依赖 RAGFlow | PASS（控制面单测）；E2E BLOCKED |
| `web/src/pages/aicc/PublicAICCChatPage.vue`、`PublicAICCChatPage.spec.ts` | 公开来源 HTTPS 安全外链、当前轮图片和结构化交互展示 | AICC-SRC-003、AICC-CH-002 | `npm test -- --run src/pages/aicc/PublicAICCChatPage.spec.ts`：31 passed | PASS |
| `web/playwright.config.ts`、`web/tests/e2e/aicc-conversation-*.spec.ts`、`helpers.ts` | Chrome Stable 挂件、隔离、状态、意向、重启、故障与来源验收 | AICC-E2E-001..003 | `npx playwright test --list --project=chrome-headed ...`：34 tests；真实运行时受 RAGFlow 与专用 fixture 阻塞 | BLOCKED |
| `docs/testing/aicc-conversation-{requirement-matrix,validation-report}.md` | 当前需求、证据、历史基线与阻塞原因 | 全部 AICC-CAP/SRC/INT/STATE/BOOT/CH/E2E | 本文档与验收报告 | PASS（文档一致性） |

| 实现入口 | 职责 | 需求矩阵 ID | 自动化与浏览器场景 | 状态 |
|---|---|---|---|---|
| `cmd/seed-e2e/main.go`、`cmd/seed-e2e/main_test.go` | 本地验收账号、企业、角色与客服种子数据 | AICC-ENTRY-01..03、AICC-AUTH-01..02 | Go 单测、Playwright 四角色登录 | BLOCKED |
| `deploy/k8s/local/secret.yaml`、`openapi/openapi.yaml` | 本地依赖配置与 API 契约 | AICC-KB-01..05、AICC-CHAT-01..03 | 配置健康检查、`make openapi-check` | BLOCKED |
| `internal/api/handlers/aicc.go`、`organizations.go` | 企业管理、智能体、会话、线索、统计管理 API | AICC-ORG-01..02、AICC-AGENT-01..02、AICC-SESSION-06..07、AICC-LEAD-01..03、AICC-ANALYTICS-01..02 | handler 单测、管理端浏览器闭环 | BLOCKED |
| `internal/api/handlers/public_aicc.go` | 公开会话、消息、留资、状态、文件 API | AICC-SESSION-01..05、AICC-STATUS-01..02、AICC-CHAT-02、AICC-SAFETY-01..02 | handler 单测、匿名公开页与挂件 | BLOCKED |
| `internal/api/handlers/*_test.go` | 管理端和匿名 API 的状态码、参数与错误映射 | AICC-AUTH-01..02、AICC-SAFETY-01..02 | Go handler 单测 | BLOCKED |
| `internal/auth/authorizer.go`、`authorizer_test.go` | 四类身份与跨企业权限谓词 | AICC-ENTRY-01..03、AICC-AUTH-01..02 | Go 单测、Playwright 权限矩阵 | BLOCKED |
| `internal/domain/aicc.go`、`internal/service/aicc_types.go` | AICC 领域模型、请求和统计类型 | AICC-AGENT-01..02、AICC-SESSION-07、AICC-ANALYTICS-01..02 | service 单测、界面断言 | BLOCKED |
| `internal/service/aicc_service.go`、`aicc_service_test.go` | 智能体、配置、会话、线索、统计与企业边界 | AICC-ORG-01..02、AICC-AGENT-01..02、AICC-SESSION-06..07、AICC-LEAD-01..03、AICC-ANALYTICS-01..02 | Go service 单测、管理端浏览器闭环 | BLOCKED |
| `internal/service/aicc_public_service.go`、`aicc_public_service_test.go` | 访客会话、状态、留资、封禁与幂等 | AICC-SESSION-01..05、AICC-STATUS-01..02、AICC-LEAD-01..02、AICC-SAFETY-01..02 | Go service 单测、公开页回归 | BLOCKED |
| `internal/service/aicc_public_chat.go`、`aicc_public_chat_test.go`、`internal/store/aicc_public_runner.go` | 模型消息转发、图片解析与存储访问 | AICC-CHAT-01..03、AICC-FAULT-01..02 | Go 单测、公开问答、故障恢复 | BLOCKED |
| `internal/service/aicc_rate_limiter.go` | 消息总数和频率限制 | AICC-SAFETY-01 | 边界值 service/API/浏览器测试 | BLOCKED |
| `internal/service/aicc_retention.go`、`aicc_retention_test.go`、`internal/worker/aicc/retention_loop.go` | 过期会话、线索关联和图片清理 | AICC-RETENTION-01 | Go 单测、定时任务环境验证 | BLOCKED |
| `internal/service/aicc_geoip.go`、`aicc_geoip_test.go` | IPv4/IPv6 地域查询与运行期更新 | AICC-GEOIP-01..02、AICC-SESSION-07 | Go 单测、镜像与更新环境验证 | BLOCKED |
| `internal/service/industry_knowledge_service.go`、`organization_service.go`、对应测试 | 行业知识库授权、企业关联和撤销 | AICC-KB-02..03 | Go 单测、平台和企业浏览器验证 | BLOCKED |
| `internal/store/queries/aicc.sql`、`industry_knowledge.sql`、`organizations.sql` 及 `internal/store/sqlc/*` | AICC、行业库和企业查询持久化 | AICC-ORG-01..02、AICC-SESSION-06..07、AICC-LEAD-03、AICC-KB-03、AICC-ANALYTICS-01..02 | SQL/store 单测、迁移与浏览器闭环 | BLOCKED |
| `internal/migrations/000031_aicc_org_industry_knowledge.*.sql` | 行业知识库企业授权 schema 变更 | AICC-KB-03、AICC-UPGRADE-01、AICC-ROLLBACK-01 | migration 单测、升级回滚演练 | BLOCKED |
| `runtime/hermes/hermes-v2026.7.1/oc-kb.py`、`renderer/render_skills.py`、测试 | 公开客服知识检索注入与运行时渲染 | AICC-KB-01..05、AICC-CHAT-01..03、AICC-FAULT-01 | Python 单测、真实知识问答与故障恢复 | BLOCKED |
| `internal/config/platform_prompt.go`、`platform_prompt_test.go` | AICC 平台规则与客服 Skill 发现边界 | AICC-CAP-002、AICC-SRC-001..004、AICC-INT-001 | 历史失败基线；当前见上方 PASS 证据 | HISTORICAL-BASELINE |
| `runtime/hermes/hermes-aicc/renderer/render_config_yaml.py`、`tests/test_render_config_yaml.py` | 客服镜像的终端、审批与跨会话记忆裁剪 | AICC-CAP-001、AICC-BOOT-001..004 | 历史失败基线；当前见上方 PASS 证据 | HISTORICAL-BASELINE |
| `docs/testing/aicc-conversation-requirement-matrix.md` | 客服能力、来源、意向、状态、启动、渠道与 Chrome 验收的唯一映射 | AICC-CAP-*、AICC-SRC-*、AICC-INT-*、AICC-STATE-*、AICC-BOOT-*、AICC-CH-*、AICC-E2E-* | 当前矩阵与验收报告；E2E 受依赖阻塞 | BLOCKED（E2E）；PASS（能力映射） |
| `web/public/aicc-widget.js` | 网页挂件、来源页与域名白名单 | AICC-DELIVERY-02..03、AICC-SESSION-01..02 | Playwright 掛件与未授权域名场景 | BLOCKED |
| `web/src/domain/aicc.ts`、`aicc.spec.ts`、`api/hooks/useAICC.ts` | 前端领域模型与 AICC API 调用 | AICC-AGENT-01..02、AICC-SESSION-01..07、AICC-STATUS-01..02、AICC-LEAD-01..03 | Vitest、Playwright 主流程 | BLOCKED |
| `web/src/layouts/AICCConsole*.vue`、`aiccConsoleContext.ts` 及测试 | 工作台路由、顶部智能体切换与模块导航 | AICC-ENTRY-01..03、AICC-AGENT-02、AICC-I18N-01、AICC-MOBILE-01 | Vitest、四角色桌面/移动浏览器 | BLOCKED |
| `web/src/pages/aicc/AICCManagerPage.vue` 及测试 | 智能体管理与设置 | AICC-ORG-01..02、AICC-AGENT-01..02、AICC-LEAD-01、AICC-SAFETY-01..02 | Vitest、管理端完整 CRUD | BLOCKED |
| `web/src/pages/aicc/AICCSessionsPage.vue`、`AICCLeadsPage.vue` 及测试 | 会话、来源地域、线索、关联会话与 CSV | AICC-SESSION-06..07、AICC-LEAD-01..03 | Vitest、管理端数据闭环 | BLOCKED |
| `web/src/pages/aicc/AICCAnalyticsPage.vue`、工作台转发页 | 统计筛选、趋势、地域、来源、问题与未解决率 | AICC-ANALYTICS-01..02 | Vitest、带固定种子数据的浏览器断言 | BLOCKED |
| `web/src/pages/aicc/AICCWidgetPreviewPage.vue`、`AICCWidgetScript.spec.ts` | 公开链接、二维码和挂件脚本展示 | AICC-DELIVERY-01..03 | Vitest、链接解码和挂件浏览器测试 | BLOCKED |
| `web/src/pages/aicc/PublicAICCChatPage.vue` 及测试 | 公开页、会话恢复、留资、状态、图片、安全提示 | AICC-SESSION-01..05、AICC-STATUS-01..02、AICC-LEAD-01..02、AICC-CHAT-02..03、AICC-SAFETY-01..02、AICC-MOBILE-01 | Vitest、匿名桌面/移动浏览器 | BLOCKED |
| `web/src/i18n/locales/{zh,en}/aicc.ts`、`aicc.spec.ts` | 中英文用户可见文案 | AICC-I18N-01 | Vitest、六模块和公开页浏览器清扫 | BLOCKED |
| `web/tests/e2e/aicc*.spec.ts`、`helpers.ts` | 最终浏览器业务、权限和知识库矩阵 | AICC-ENTRY-01..03、AICC-ORG-01..02、AICC-AGENT-01..02、AICC-DELIVERY-01..03、AICC-SESSION-01..07、AICC-STATUS-01..02、AICC-LEAD-01..03、AICC-KB-01..05、AICC-CHAT-01..03、AICC-I18N-01、AICC-MOBILE-01、AICC-ANALYTICS-01..02 | Playwright 最终复跑 | BLOCKED |
