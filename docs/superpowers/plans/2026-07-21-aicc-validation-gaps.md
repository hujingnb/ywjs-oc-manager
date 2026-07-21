# AICC 验证缺口补齐 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐 AICC 手机号格式校验、暂停客服手动启动应用最新模型 revision、无关问题稳定拒绝三个验证缺口。

**Architecture:** 后端 service 作为最终行为边界，前端只提供即时输入反馈。暂停启动复用现有 AICC 配置 revision 与 rollout/restart 机制；无关问题拒绝在公开消息进入 Hermes 前短路，避免模型输出不稳定。

**Tech Stack:** Go service/sqlc、Vue 3/TypeScript、Playwright、MySQL/k3d。

---

## File Structure

- `internal/service/aicc_public_service.go`：新增留资字段格式校验和无关问题短路。
- `internal/service/aicc_public_service_test.go`：新增手机号格式和无关问题 service 单测。
- `internal/service/aicc_service.go`：启动暂停客服时应用最新企业 AICC revision。
- `internal/service/aicc_service_test.go`：新增启动落后 revision 的单测。
- `web/src/pages/aicc/PublicAICCChatPage.vue`：公开留资表单前端校验。
- `web/src/pages/aicc/PublicAICCChatPage.spec.ts`：新增非法手机号不提交 API 的单测。
- `web/tests/e2e/aicc.spec.ts`、`web/tests/e2e/aicc-conversation-security.spec.ts`：补 focused E2E。
- `docs/testing/aicc-conversation-validation-report.md`：记录新增验证结果。

## Task 1: 手机号格式校验

- [ ] 在 `internal/service/aicc_public_service_test.go` 新增失败用例：`phone` 字段提交 `12345` 返回 `ErrInvalidArgument`，且不写 `leadValues` 和 `leads`。
- [ ] 在 `internal/service/aicc_public_service.go` 新增 `validateAICCLeadFieldValue(field, value)`，`phone` 必须匹配 `^1[3-9][0-9]{9}$`。
- [ ] 在 `PublicAICCChatPage.spec.ts` 新增失败用例：非法手机号提交后显示错误，不调用 `submitAICCPublicLeadValues`。
- [ ] 在 `PublicAICCChatPage.vue` 的 `submitLeadForm` 中按 `field_type` 做前端校验。
- [ ] 运行 `go test ./internal/service -run 'TestAICCPublic.*Lead|TestAICCPublicLead' -count=1` 和 `cd web && npm test -- --run src/pages/aicc/PublicAICCChatPage.spec.ts`。

## Task 2: 暂停客服手动启动应用最新 revision

- [ ] 在 `internal/service/aicc_service_test.go` 新增失败用例：agent 为 `paused` 且 `applied_config_revision` 落后企业配置时，`SetAgentStatus(..., "start")` 创建应用最新 revision 的重启/初始化任务。
- [ ] 在 `internal/service/aicc_service.go` 的 start 分支读取企业 AICC 配置并复用现有任务创建逻辑。
- [ ] 确保 paused agent 的企业模型 rollout 仍不会自动启动；只有手动 start 触发应用。
- [ ] 运行 `go test ./internal/service -run 'TestAICCService.*Status|TestAICC.*Start' -count=1`。

## Task 3: 无关问题稳定拒绝

- [ ] 在 `internal/service/aicc_public_service_test.go` 新增失败用例：彩票预测/其他公司商业机密输入直接返回范围拒绝，不调用 Hermes chat，不写来源。
- [ ] 在 `aicc_public_service.go` 的公开消息入口进入 chat 前新增确定性无关问题判断。
- [ ] 在 `aicc-conversation-security.spec.ts` 增加“无关商业机密”场景，断言拒绝文本和来源审计为零。
- [ ] 运行 `go test ./internal/service -run 'TestAICCPublic.*Message|TestAICCPublic.*Irrelevant' -count=1`。

## Task 4: E2E 与文档

- [ ] 在 `aicc.spec.ts` 补非法手机号不落正式线索、手动启动后 revision 收敛的 focused E2E。
- [ ] 运行定向 Playwright：线索手机号、暂停模型、无关问题三个 grep。
- [ ] 更新 `docs/testing/aicc-conversation-validation-report.md`。
- [ ] 运行 `cd web && npm run typecheck`、`git diff --check`。
