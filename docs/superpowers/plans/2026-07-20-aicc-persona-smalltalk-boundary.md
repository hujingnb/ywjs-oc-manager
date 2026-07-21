# AICC 人设闲聊边界 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 AICC 在不涉及企业事实时遵守智能体人设完成寒暄和身份介绍，同时保持企业事实的知识检索边界。

**Architecture:** 只修改 AICC 平台层提示词：显式列出可直接回应的非事实表达，并保留对企业事实的首次检索要求。提示词哈希会触发现有 AICC 静默重启路径，无需新接口或数据表。测试分别锁定两类边界，再以真实浏览器验证公开接待。

**Tech Stack:** Go、Testify、Playwright、Vue/Naive UI、本地 k3d。

---

### Task 1: 平台提示词与边界单元测试

**Files:**
- Modify: `internal/config/platform_prompt.go:54-66`
- Modify: `internal/config/platform_prompt_test.go:16-92`

- [ ] **Step 1: 写出两类边界的失败断言**

在 `TestPlatformPrompts_Invariants` 的 AICC 子测试增加下列断言，并为其添加相邻中文注释：

```go
// 非企业事实的寒暄、身份介绍和人设表达允许直接答复，避免知识库空命中覆盖客服身份。
assert.Contains(t, testCase.prompt, "寒暄、身份介绍、礼貌回应和人设表达")
assert.Contains(t, testCase.prompt, "可以直接答复")
// 企业事实仍需先检索，防止人设例外被误解为产品资料的无依据回答许可。
assert.Contains(t, testCase.prompt, "涉及企业事实、产品、价格、政策、售后、行业或资料的问题")
assert.Contains(t, testCase.prompt, "必须先调用 aicc_knowledge_search")
```

- [ ] **Step 2: 运行测试确认当前提示词缺少例外**

Run: `go test ./internal/config -run TestPlatformPrompts_Invariants -count=1`

Expected: FAIL，AICC 提示词不包含“寒暄、身份介绍、礼貌回应和人设表达”。

- [ ] **Step 3: 最小化修改 AICC 平台提示词**

在 `DefaultAICCSystemPromptTemplate` 的“信息与能力边界”首行之前加入：

```text
- 寒暄、身份介绍、礼貌回应和人设表达不属于企业事实，可以直接答复，并应遵守当前智能体配置的人设；不得借此编造企业信息。
```

保留原有企业事实检索规则；将该规则开头调整为：

```text
- 涉及企业事实、产品、价格、政策、售后、行业或资料的问题，在输出最终答复或追问前必须先调用 aicc_knowledge_search；不得用澄清问题替代首次检索，不得自行猜测、编写脚本或伪称已经执行外部操作。
```

- [ ] **Step 4: 运行提示词与 hash 回归**

Run: `go test ./internal/config -run 'TestPlatformPrompts_Invariants|TestPlatformPromptHash' -count=1`

Expected: PASS。

- [ ] **Step 5: 提交提示词边界**

```bash
git add internal/config/platform_prompt.go internal/config/platform_prompt_test.go
git commit -m "fix(aicc): 允许人设寒暄直接答复" -m "将非事实的寒暄和身份介绍排除在企业知识检索前置条件之外。\n\n企业事实仍保持首次检索与如实答复边界。"
```

### Task 2: 真实浏览器回归与提示词静默生效

**Files:**
- Modify: `web/tests/e2e/aicc.spec.ts:571-601`
- Test: `web/tests/e2e/aicc.spec.ts`

- [ ] **Step 1: 保留人设公开接待断言并加入重启准备**

在“企业管理员可用独立客服模型创建有人设的智能体并公开接待”用例中，保持以下真实页面链路：平台配置模型、企业管理员填写 `#aicc-persona`、启动智能体、打开公开链接、发送“请介绍一下你自己”。断言公开回复包含 `海风你好`。

在创建智能体前调用已有 `setAICCConfigForFixtureOrg(page, true, 100)`；不要通过 API 或数据库写入人设。

- [ ] **Step 2: 重建本地镜像并滚动 manager 服务**

Run: `make local-images && kubectl --context k3d-ocm -n ocm rollout restart deployment/manager-api && kubectl --context k3d-ocm -n ocm rollout status deployment/manager-api --timeout=180s`

Expected: 本地 manager-api rollout 成功；不得修改 `deploy/k8s/prod/*`。

- [ ] **Step 3: 运行人设与模型切换定向 Chromium 回归**

Run: `cd web && OCM_E2E_SUITE=slow npx playwright test tests/e2e/aicc.spec.ts --grep '独立客服模型创建有人设|运行中的智能客服更换模型' --project=chromium`

Expected: 两个用例 PASS；人设场景公开回复包含“海风你好”，换模场景的 `aicc_model_rollout` 为 `succeeded`。

- [ ] **Step 4: 恢复本地 E2E fixture 并提交浏览器测试（仅有改动时）**

把测试创建的企业 AICC 模型恢复为 `deepseek-chat`，等待对应 rollout 成功；不得留下临时 channel 模型或修改仓库部署文件。

若本任务仅修改提示词和 Go 测试，此步骤不创建额外提交；若 E2E 定位器或断言确有必要改动，单独提交：

```bash
git add web/tests/e2e/aicc.spec.ts web/tests/e2e/aicc/helpers.ts
git commit -m "test(aicc): 验证人设公开寒暄" -m "通过真实浏览器确认非企业事实回复遵守智能客服人设。"
```

### Task 3: 交付前验证

**Files:**
- Verify only.

- [ ] **Step 1: 运行后端关联回归**

Run: `go test ./internal/config ./internal/service ./internal/worker/handlers -count=1`

Expected: PASS。

- [ ] **Step 2: 运行前端类型检查与工作区检查**

Run: `cd web && npm run typecheck && cd .. && git diff --check && git status --short`

Expected: 类型检查和 diff 检查 PASS；仅保留用户已有的 `deploy/k8s/prod/new-api.yaml` 与无关未跟踪文档改动。
