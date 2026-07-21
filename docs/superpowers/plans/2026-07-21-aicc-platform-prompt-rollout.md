# AICC 平台提示词静默下发 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 当 AICC 平台提示词哈希更新时，自动逐台静默重启所有仍使用旧提示词的 active 智能客服。

**Architecture:** 新增全局 `aicc_platform_prompt_rollout` job，与按企业模型 revision 下发的 `aicc_model_rollout` 完全分离。启动协调器只在数据库不存在同类活跃任务且存在 hash 落后客服时创建 job；handler 按 app ID 稳定顺序持久化单台 marker、重启、等待 generation ready、确认 bootstrap 写入当前 hash，再继续下一台。

**Tech Stack:** Go、sqlc、PostgreSQL、Testify、Kubernetes orchestrator、Playwright、本地 k3d。

---

### Task 1: 数据库查询、任务类型和启动协调器

**Files:**
- Modify: `internal/domain/enums.go`
- Modify: `internal/migrations/000040_aicc_assistant_version_isolation.up.sql`
- Modify: `internal/migrations/000040_aicc_assistant_version_isolation.down.sql`
- Modify: `internal/store/queries/aicc_configs.sql`
- Modify: `internal/store/queries/jobs.sql`
- Modify: `internal/store/sqlc/*.go` (generated)
- Create: `internal/service/aicc_platform_prompt_rollout.go`
- Create: `internal/service/aicc_platform_prompt_rollout_test.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: 写协调器失败测试**

定义最小 store 接口并写相邻中文注释的 Testify 测试，覆盖：存在旧 hash active 客服时创建一个 job；无落后客服不创建；已有 pending/running job 时不重复创建；创建后调用 notifier。

```go
func TestAICCPlatformPromptRolloutCoordinatorCreatesOneJobForStaleAgents(t *testing.T) {
    store := &fakePromptRolloutStore{hasStale: true}
    notifier := &fakePromptRolloutNotifier{}
    coordinator := NewAICCPlatformPromptRolloutCoordinator(store, notifier)

    require.NoError(t, coordinator.EnqueueIfNeeded(context.Background()))
    require.Len(t, store.jobs, 1)
    assert.Equal(t, domain.JobTypeAICCPlatformPromptRollout, store.jobs[0].Type)
    assert.JSONEq(t, `{"target_prompt_hash":"`+config.PlatformPromptHash(domain.AppTypeAICC)+`"}`, string(store.jobs[0].PayloadJson))
    assert.Equal(t, 1, notifier.calls)
}
```

- [ ] **Step 2: 运行协调器测试确认 RED**

Run: `go test ./internal/service -run TestAICCPlatformPromptRolloutCoordinator -count=1`

Expected: FAIL，缺少 job type、协调器或 store 方法。

- [ ] **Step 3: 新增窄查询与任务类型**

新增 job type：

```go
// JobTypeAICCPlatformPromptRollout 逐台下发平台提示词更新，不与企业模型 revision 复用。
JobTypeAICCPlatformPromptRollout = "aicc_platform_prompt_rollout"
```

在 `aicc_configs.sql` 增加：

```sql
-- name: HasStaleAICCPlatformPromptAgents :one
SELECT EXISTS (
  SELECT 1 FROM aicc_agents aa
  JOIN apps a ON a.id = aa.app_id AND a.deleted_at IS NULL
  WHERE aa.deleted_at IS NULL AND aa.status = 'active'
    AND a.applied_platform_prompt_hash <> ?
);

-- name: ListPendingAICCPlatformPromptRolloutAgents :many
SELECT aa.*
FROM aicc_agents aa
JOIN apps a ON a.id = aa.app_id AND a.deleted_at IS NULL
WHERE aa.deleted_at IS NULL AND aa.status = 'active'
  AND a.applied_platform_prompt_hash <> ?
ORDER BY aa.id
LIMIT ?;
```

在 `jobs.sql` 增加 `GetAICCPlatformPromptRolloutLeaderJob` 与 `HasActiveAICCPlatformPromptRolloutJob`，均只匹配本 job type 的 pending/running 行。迁移中的 jobs type CHECK 同时加入新值；down migration 先删除此类 job 后再收回约束。运行 `make sqlc-generate`。

- [ ] **Step 4: 实现启动协调器并在 server 注册前调用**

协调器用 `HasActiveAICCPlatformPromptRolloutJob` 防止重复；仅 `HasStaleAICCPlatformPromptAgents=true` 时写入一个 priority 100、max attempts 20、payload 为当前 AICC prompt hash 的 job。server 组装后调用 `EnqueueIfNeeded`，并在成功创建时通知 redis queue；读取/创建错误必须阻止启动并返回带上下文错误。

- [ ] **Step 5: 运行协调器、迁移和 sqlc 回归**

Run: `go test ./internal/service ./internal/migrations ./internal/store/... -run 'TestAICCPlatformPromptRolloutCoordinator|Test.*Migration' -count=1 && make sqlc-generate && git diff --check`

Expected: PASS，生成物同步。

- [ ] **Step 6: 提交基础设施**

```bash
git add internal/domain/enums.go internal/migrations/000040_aicc_assistant_version_isolation.* internal/store/queries internal/store/sqlc internal/service/aicc_platform_prompt_rollout.go internal/service/aicc_platform_prompt_rollout_test.go cmd/server/main.go
git commit -m "feat(aicc): 下发存量客服平台提示词" -m "平台提示词变化时创建独立 rollout 任务。\n\n任务与企业模型 revision 分离，避免两类变更相互覆盖。"
```

### Task 2: 逐台提示词 rollout worker

**Files:**
- Create: `internal/worker/handlers/aicc_platform_prompt_rollout.go`
- Create: `internal/worker/handlers/aicc_platform_prompt_rollout_test.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: 写 handler RED 测试**

复用 `aicc_model_rollout_test.go` 的 fake orchestrator 风格，覆盖一轮仅重启一台、等待指定 generation、完成后 app 的 `applied_platform_prompt_hash` 等于 payload hash；暂停客服不返回；payload marker 在 restart 之前持久化，worker 重试后从 marker 恢复。

```go
func TestAICCPlatformPromptRolloutRestartsOneStaleAgent(t *testing.T) {
    store := newPromptRolloutStore("old-hash", "current-hash")
    events := []string{}
    handler := NewAICCPlatformPromptRolloutHandler(store, &aiccRolloutOrchestrator{events: &events}, time.Second)

    require.NoError(t, handler.Handle(context.Background(), promptRolloutJob(t, "current-hash")))
    assert.Equal(t, []string{"restart:app-1", "wait:app-1:7"}, events)
    assert.Equal(t, "current-hash", store.appliedPromptHash["app-1"])
}
```

- [ ] **Step 2: 运行 handler 测试确认 RED**

Run: `go test ./internal/worker/handlers -run TestAICCPlatformPromptRollout -count=1`

Expected: FAIL，handler 与 payload 不存在。

- [ ] **Step 3: 实现独立 payload、leader 与恢复 marker**

payload 仅包含：

```go
type AICCPlatformPromptRolloutPayload struct {
    TargetPromptHash string `json:"target_prompt_hash"`
    RepairAgentID string `json:"repair_agent_id,omitempty"`
    RepairAppID string `json:"repair_app_id,omitempty"`
    RepairTargetGeneration int64 `json:"repair_target_generation"`
}
```

handler 只领取 `applied_platform_prompt_hash != TargetPromptHash` 的 active agent；每台先写 marker、设置 restarting、调用 `RolloutRestartAndGetGeneration`、持久化 generation、`WaitRolloutReady`，随后读取 app 的实际 `applied_platform_prompt_hash`。若 hash 仍不等于 target，返回错误让任务重试；相等后写 ready 并清 marker。禁止修改 `aicc_agents.applied_config_revision`，禁止读取企业模型配置。

- [ ] **Step 4: 注册 worker handler**

在 `cmd/server/main.go` 紧随模型 rollout 注册：

```go
aiccPromptRolloutHandler := handlers.NewAICCPlatformPromptRolloutHandler(dbStore.Queries, orch, 30*time.Minute)
if err := registry.Register(domain.JobTypeAICCPlatformPromptRollout, aiccPromptRolloutHandler.Handle); err != nil {
    return fmt.Errorf("注册 aicc_platform_prompt_rollout handler 失败: %w", err)
}
```

- [ ] **Step 5: 运行 worker 回归并提交**

Run: `go test ./internal/worker/handlers -run 'TestAICCPlatformPromptRollout|TestAICCModelRollout' -count=1 && git diff --check`

Expected: PASS。

```bash
git add internal/worker/handlers/aicc_platform_prompt_rollout.go internal/worker/handlers/aicc_platform_prompt_rollout_test.go cmd/server/main.go
git commit -m "feat(aicc): 逐台静默重启提示词落后客服" -m "任务以平台提示词 hash 确认 bootstrap 已应用新规则。\n\n暂停客服保持暂停，不参与本次下发。"
```

### Task 3: 本地浏览器验证与交付检查

**Files:**
- Modify: `web/tests/e2e/aicc.spec.ts` (仅在缺少存量客服 prompt rollout 可观察断言时)
- Test: `web/tests/e2e/aicc.spec.ts`

- [ ] **Step 1: 增加运行时验证断言**

在现有人设公开接待场景中，创建并启动客服后，通过本地只读 helper 读取 app `applied_platform_prompt_hash`，断言它等于当前 `config.PlatformPromptHash(domain.AppTypeAICC)`；不要让浏览器测试直接写数据库。

- [ ] **Step 2: 重建本地服务并执行定向 Chromium 场景**

Run: `make local-images && kubectl --context k3d-ocm -n ocm rollout restart deployment/manager-api && kubectl --context k3d-ocm -n ocm rollout status deployment/manager-api --timeout=180s && cd web && OCM_E2E_SUITE=slow npx playwright test tests/e2e/aicc.spec.ts --grep '独立客服模型创建有人设|运行中的智能客服更换模型' --project=chromium`

Expected: PASS；公开回复包含“海风你好”，模型 rollout 仍成功，存量 AICC 不会被并发模型任务错误标记。

- [ ] **Step 3: 恢复本地 fixture**

将本次 fixture 企业模型恢复到 `deepseek-chat`，等待其 `aicc_model_rollout` 成功，确认无临时模型 alias。若测试创建存量 hash 落后客服，确认其 `aicc_platform_prompt_rollout` 成功且 `applied_platform_prompt_hash` 收敛。

- [ ] **Step 4: 最终验证**

Run: `go test ./internal/config ./internal/service ./internal/worker/handlers -count=1 && cd web && npm run typecheck && cd .. && make openapi-check && git diff --check`

Expected: PASS；若 service 全包在 10 分钟内不结束，记录为未完成，并保留已通过的最小相关测试，不得声称全量通过。
