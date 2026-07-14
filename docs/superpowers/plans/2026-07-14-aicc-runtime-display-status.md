# AICC 运行时展示状态 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 AICC 工作台展示隐藏运行时的实际就绪状态，区分启动中、待接待、接待中、已暂停、异常和已删除。

**Architecture:** 智能体的 `status` 保持接待意图，关联隐藏 App 的 `status` 与 `runtime_phase` 保持运行时事实。`AICCService` 在响应边界集中映射一个只读展示状态，前端只根据该字段展示文案、控制接待按钮和轮询，不复制后端生命周期规则。

**Tech Stack:** Go、sqlc、Gin/Swag OpenAPI、Vue 3、TypeScript、TanStack Vue Query、Vitest、k3d、Chrome DevTools。

---

## 文件结构

- `internal/domain/aicc.go`：定义 AICC 面向管理端的运行时展示状态常量。
- `internal/service/aicc_service.go`：读取隐藏 App 并集中计算智能体展示状态与安全提示。
- `internal/service/aicc_service_test.go`：覆盖展示状态映射、隐藏 App 查询失败和状态切换约束。
- `internal/api/handlers/aicc_test.go`：覆盖列表、详情和写接口返回新增只读字段。
- `openapi/openapi.yaml`、`web/src/api/generated.ts`：由命令生成，反映响应字段变化。
- `web/src/domain/aicc.ts`：声明运行时展示状态与响应字段。
- `web/src/api/hooks/useAICC.ts`：在运行时过渡阶段开启短周期刷新。
- `web/src/layouts/AICCConsoleWorkspace.vue`、`web/src/pages/aicc/AICCManagerPage.vue`：展示统一状态并禁用尚未就绪的接待操作。
- `web/src/layouts/AICCConsoleWorkspace.spec.ts`、`web/src/pages/aicc/AICCManagerPage.spec.ts`：覆盖页面状态与操作可用性。
- `web/src/i18n/locales/zh/aicc.ts`、`web/src/i18n/locales/en/aicc.ts`：补齐用户可见状态与等待提示。

### Task 1: 后端运行时展示状态映射

**Files:**
- Modify: `internal/domain/aicc.go`
- Modify: `internal/service/aicc_service.go`
- Test: `internal/service/aicc_service_test.go`

- [ ] **Step 1: 写入失败服务测试**

在 `internal/service/aicc_service_test.go` 的 fake store 增加 `GetAppWithVersion` 所需的隐藏 App 夹具，并增加表驱动用例：

```go
cases := []struct {
    name      string
    agent     string
    appStatus string
    phase     string
    want      string
}{
    {name: "运行时初始化中", agent: domain.AICCAgentStatusDraft, appStatus: domain.AppStatusStarting, phase: domain.RuntimePhaseStarting, want: domain.AICCRuntimeStatusStarting},
    {name: "运行时就绪未接待", agent: domain.AICCAgentStatusDraft, appStatus: domain.AppStatusBindingWaiting, phase: domain.RuntimePhaseReady, want: domain.AICCRuntimeStatusReady},
    {name: "运行时就绪接待中", agent: domain.AICCAgentStatusActive, appStatus: domain.AppStatusBindingWaiting, phase: domain.RuntimePhaseReady, want: domain.AICCRuntimeStatusReceiving},
    {name: "运行时就绪已暂停", agent: domain.AICCAgentStatusPaused, appStatus: domain.AppStatusBindingWaiting, phase: domain.RuntimePhaseReady, want: domain.AICCRuntimeStatusPaused},
    {name: "运行时失败", agent: domain.AICCAgentStatusDraft, appStatus: domain.AppStatusError, phase: domain.RuntimePhaseUnknown, want: domain.AICCRuntimeStatusError},
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/service -run TestAICCServiceRuntimeDisplayStatus -count=1`

Expected: FAIL，因为 `runtime_status` 与映射函数尚不存在。

- [ ] **Step 3: 实现最小映射**

在 `internal/domain/aicc.go` 定义 `starting`、`ready`、`receiving`、`paused`、`error`、`deleted` 常量；在 `AICCStore` 增加按 app ID 读取隐藏 App 的方法；扩展 `AICCAgentResult`：

```go
RuntimeStatus  string `json:"runtime_status"`
RuntimeMessage string `json:"runtime_message,omitempty"`
```

新增 `populateAICCAgentRuntimeStatus(ctx, result *AICCAgentResult)`：App 为 `error` 时返回 `error` 与裁剪后的错误消息；App 未 `ready` 时返回 `starting`；运行时 ready 时按智能体 `status` 返回 `ready`、`receiving`、`paused` 或 `deleted`。所有 Create、List、Get、Update、Start、Stop 返回结果均在 service 层调用该函数。

- [ ] **Step 4: 运行后端单元测试**

Run: `go test ./internal/service -run 'TestAICCService(RuntimeDisplayStatus|StatusAndDelete)' -count=1`

Expected: PASS。

- [ ] **Step 5: 提交后端状态映射**

```bash
git add internal/domain/aicc.go internal/service/aicc_service.go internal/service/aicc_service_test.go
git commit -m "feat(aicc): 返回客服运行时展示状态" -m "关联隐藏运行时的状态与就绪阶段，统一计算启动中、待接待、接待中、已暂停和异常状态。"
```

### Task 2: API 契约与前端领域类型

**Files:**
- Modify: `internal/api/handlers/aicc_test.go`
- Generated: `openapi/openapi.yaml`
- Generated: `web/src/api/generated.ts`
- Modify: `web/src/domain/aicc.ts`

- [ ] **Step 1: 写入失败 API 测试**

在 `internal/api/handlers/aicc_test.go` 的智能体列表和详情用例中断言响应包含：

```go
assert.Equal(t, domain.AICCRuntimeStatusReady, body.Agent.RuntimeStatus)
```

并断言公开 token、运行时容器地址和原始快照不在 `runtime_message` 中。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/handlers -run TestAICC -count=1`

Expected: FAIL，因为 handler 响应模型尚未声明 `runtime_status`。

- [ ] **Step 3: 同步契约与前端类型**

为 `service.AICCAgentResult` 补充中文 Swag 注释，运行：

```bash
make openapi-gen
make web-types-gen
```

在 `web/src/domain/aicc.ts` 增加 `AICCRuntimeStatus` 联合类型及 `runtime_status`、`runtime_message` 字段；不让页面直接依赖 generated type。

- [ ] **Step 4: 运行 API 与生成物校验**

Run: `go test ./internal/api/handlers -run TestAICC -count=1 && make openapi-check && npm --prefix web run typecheck`

Expected: PASS，且 `git diff --check` 无输出。

- [ ] **Step 5: 提交契约变更**

```bash
git add internal/api/handlers/aicc_test.go openapi/openapi.yaml web/src/api/generated.ts web/src/domain/aicc.ts
git commit -m "feat(aicc): 暴露客服运行时状态契约" -m "管理端智能体响应增加只读运行时展示状态和安全错误摘要，并同步 OpenAPI 与前端类型。"
```

### Task 3: 工作台状态展示、操作门禁与轮询

**Files:**
- Modify: `web/src/api/hooks/useAICC.ts`
- Modify: `web/src/layouts/AICCConsoleWorkspace.vue`
- Modify: `web/src/pages/aicc/AICCManagerPage.vue`
- Modify: `web/src/i18n/locales/zh/aicc.ts`
- Modify: `web/src/i18n/locales/en/aicc.ts`
- Test: `web/src/layouts/AICCConsoleWorkspace.spec.ts`
- Test: `web/src/pages/aicc/AICCManagerPage.spec.ts`

- [ ] **Step 1: 写入失败前端测试**

在工作台测试中构造 `runtime_status: 'starting'`，断言显示“启动中”；在管理页测试中构造同一状态，断言“开始接待”按钮 disabled。构造 `runtime_status: 'ready'` 和 `status: 'draft'`，断言显示“待接待”且按钮可点击。

```ts
expect(wrapper.text()).toContain('启动中')
expect(startButton.attributes('disabled')).toBeDefined()
```

- [ ] **Step 2: 运行前端测试确认失败**

Run: `npm --prefix web test -- --run src/layouts/AICCConsoleWorkspace.spec.ts src/pages/aicc/AICCManagerPage.spec.ts`

Expected: FAIL，因为状态文案和禁用规则尚未实现。

- [ ] **Step 3: 实现前端最小行为**

增加中英文状态文案：`启动中`、`待接待`、`接待中`、`已暂停`、`异常`、`已删除`。页面只读取 `agent.runtime_status`：

```ts
const canStartReception = computed(() => selectedAgent.value?.runtime_status === 'ready')
const canStopReception = computed(() => selectedAgent.value?.runtime_status === 'receiving')
```

在 `useAICCAgentsQuery` 与单智能体查询中，当列表或详情包含 `starting` 时使用 `refetchInterval: 1500`，稳定状态时返回 `false`。在顶部和接待台复用同一状态标签映射，异常时展示 `runtime_message`。

- [ ] **Step 4: 运行前端测试、类型检查与构建**

Run: `npm --prefix web test -- --run src/layouts/AICCConsoleWorkspace.spec.ts src/pages/aicc/AICCManagerPage.spec.ts && npm --prefix web run typecheck && npm --prefix web run build`

Expected: PASS；仅允许既有 Vite chunk-size 警告。

- [ ] **Step 5: 本地 k3d 与真实浏览器验证**

Run: `make local-build && kubectl -n oc-aicc get deploy,pods`

在 `http://ocm.localhost/aicc-console` 通过真实 Chrome 登录后验证：新建并保存智能体时显示“启动中”；隐藏 App Pod Ready 后刷新为“待接待”；点击“开始接待”后显示“接待中”；点击暂停后显示“已暂停”。不在验证中发送公开访客消息或创建无关会话。

- [ ] **Step 6: 提交前端状态体验**

```bash
git add web/src/api/hooks/useAICC.ts web/src/layouts/AICCConsoleWorkspace.vue web/src/pages/aicc/AICCManagerPage.vue web/src/i18n/locales/zh/aicc.ts web/src/i18n/locales/en/aicc.ts web/src/layouts/AICCConsoleWorkspace.spec.ts web/src/pages/aicc/AICCManagerPage.spec.ts
git commit -m "feat(aicc): 展示客服运行时接待状态" -m "工作台区分启动中、待接待、接待中、已暂停和异常状态，并在运行时未就绪时限制接待操作。"
```

## 计划自检

- 设计文档定义的六个展示状态均由 Task 1 映射，并由 Task 3 展示。
- 状态来源、错误摘要与权限边界由 Task 1 和 Task 2 覆盖；未引入 AICC 数据库状态字段或迁移。
- API 改动包含 OpenAPI、生成 TypeScript 与类型检查。
- 每个行为变更均遵循失败测试、最小实现、通过验证、独立提交的顺序。
