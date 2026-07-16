# AICC 智能体删除运行时清理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 删除接待台 AICC 智能体时保留业务历史，并异步回收关联隐藏 app 的 Kubernetes 运行时资源。

**Architecture:** `AICCService.DeleteAgent` 在软删除智能体前调用 `AppService` 的 AICC 专用删除入口。该入口软删除隐藏 app、写入 `app_delete` job 并通知既有队列；worker 复用现有幂等流程回收 Kubernetes、new-api 和 RAGFlow 资源。会话、消息、线索和审计行不参与删除。

**Tech Stack:** Go、sqlc、MySQL jobs 表、Redis job notifier、Kubernetes orchestrator、testify。

---

### Task 1: 为隐藏 app 删除增加异步回收入口

**Files:**
- Modify: `internal/service/app_service.go:17-45,307-333`
- Test: `internal/service/app_service_test.go`

- [ ] **Step 1: Write the failing test**

新增相邻中文注释的 `TestAppServiceDeleteHiddenAICCAppEnqueuesRuntimeCleanup`：预置 `app_type=aicc`，注入 `fakeNotifier`，调用 `DeleteHiddenAICCApp`，断言 `SoftDeleteApp` 被调用，创建的 job 为 `domain.JobTypeAppDelete`、payload 为 `{"app_id":"app-aicc"}`、优先级 100、最多重试 3 次，并由 notifier 立即入队。

```go
// 场景：删除 AICC 隐藏 app 时必须创建 app_delete 任务，使 worker 回收 Kubernetes 运行时资源。
func TestAppServiceDeleteHiddenAICCAppEnqueuesRuntimeCleanup(t *testing.T) {
    // 使用现有 app store stub 预置 app_type=aicc，创建 service 并注入 fakeNotifier。
    // 断言 SoftDeleteApp、CreateJob 与 notifier.Enqueue 均使用同一 app/job。
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service -run '^TestAppServiceDeleteHiddenAICCAppEnqueuesRuntimeCleanup$' -count=1 -v`

Expected: FAIL，因为 `DeleteHiddenAICCApp` 尚未定义。

- [ ] **Step 3: Write minimal implementation**

在 `AppService` 新增：

```go
// DeleteHiddenAICCApp 软删除 AICC 隐藏 app 并异步回收其运行时资源。
func (s *AppService) DeleteHiddenAICCApp(ctx context.Context, principal auth.Principal, appID string) error {
    // 复用 SoftDeleteHiddenAICCApp 的 appID、app_type 和权限校验。
    // SoftDeleteApp 成功后序列化 {"app_id": appID}，写入 app_delete job：
    // Priority=100、RunAfter=time.Now()、MaxAttempts=3；notifier 非 nil 时 Enqueue。
}
```

保留 `SoftDeleteHiddenAICCApp` 作为创建失败的纯补偿入口；不要让它入队任务，避免创建事务尚未完成时 worker 过早回收资源。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/service -run '^TestAppServiceDeleteHiddenAICCAppEnqueuesRuntimeCleanup$' -count=1 -v`

Expected: PASS。

- [ ] **Step 5: Commit**

Run: `git add internal/service/app_service.go internal/service/app_service_test.go && git commit -m "feat(aicc): 删除隐藏应用时回收运行时资源" -m "为 AICC 隐藏 app 创建 app_delete 任务，复用 worker 幂等清理 Kubernetes 与外部运行时资源。"`

### Task 2: 接待台删除智能体时调度隐藏 app 清理

**Files:**
- Modify: `internal/service/aicc_service.go:139-160,454-474`
- Test: `internal/service/aicc_service_test.go:639-664,1098-1110`

- [ ] **Step 1: Write the failing tests**

扩展 `fakeAICCHiddenAppCreator` 以实现新删除方法，并记录 app ID。新增下列相邻中文注释场景：

```go
// 场景：删除接待台智能体后，应请求关联隐藏 app 删除并保留会话和消息历史。
func TestAICCServiceDeleteAgentQueuesHiddenAppCleanup(t *testing.T) {
    // 删除 agent-1 后断言 deleter.deleteID == "app-hidden-1"，
    // store.sessions["session-1"] 和 store.messages["session-1"] 仍存在。
}

// 场景：隐藏 app 清理任务无法创建时，删除接口必须返回错误且不写成功删除审计。
func TestAICCServiceDeleteAgentReturnsHiddenAppCleanupError(t *testing.T) {
    // fake deleter 返回 errors.New("enqueue failed")，断言 DeleteAgent 返回包装错误且 audits 为空。
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/service -run '^TestAICCServiceDeleteAgent(QueuesHiddenAppCleanup|ReturnsHiddenAppCleanupError)$' -count=1 -v`

Expected: FAIL，因为当前 `DeleteAgent` 只调用 `SoftDeleteAICCAgent`。

- [ ] **Step 3: Write minimal implementation**

在 `aicc_service.go` 定义窄接口：

```go
// AICCHiddenAppDeleter 表示删除已创建 AICC 隐藏 app 并入队运行时回收的能力。
type AICCHiddenAppDeleter interface {
    DeleteHiddenAICCApp(ctx context.Context, principal auth.Principal, appID string) error
}
```

在 `DeleteAgent` 中完成权限检查后，先经该接口删除 `row.AppID`，接口缺失或任务创建失败均返回错误；成功后再软删除 `aicc_agents` 和写 `delete` 审计。不得删除 AICC 会话、消息或线索。

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/service -run '^TestAICCService(StatusAndDelete|DeleteAgentQueuesHiddenAppCleanup|DeleteAgentReturnsHiddenAppCleanupError)$' -count=1 -v`

Expected: PASS。

- [ ] **Step 5: Commit**

Run: `git add internal/service/aicc_service.go internal/service/aicc_service_test.go && git commit -m "feat(aicc): 删除智能体时清理隐藏运行时" -m "接待台删除会保留会话和线索历史，并调度隐藏 app 的异步运行时资源回收。"`

### Task 3: 验证 worker 的 AICC Kubernetes 清理合同

**Files:**
- Test: `internal/worker/handlers/app_runtime_ops_test.go`

- [ ] **Step 1: Write the failing test**

新增 `TestAppDeleteHandlerDeletesAICCRuntimeResources`：预置 `app_type=aicc`，执行 `AppDeleteHandler` 后断言 fake orchestrator 收到一次 app ID。此断言绑定 `NewAICCKubernetesAdapter.Delete` 的既有合同：Deployment、Service、Secret、HPA、NetworkPolicy 均按 app ID 删除。

```go
// 场景：AICC 隐藏 app 的 app_delete 任务必须调用编排器，回收其专属运行时资源。
func TestAppDeleteHandlerDeletesAICCRuntimeResources(t *testing.T) {
    // 预置 app_type=aicc，执行 handler 后断言 fake orchestrator 的 deletedAppID 为 app-aicc。
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/worker/handlers -run '^TestAppDeleteHandlerDeletesAICCRuntimeResources$' -count=1 -v`

Expected: FAIL，直到 fake orchestrator 记录删除调用。

- [ ] **Step 3: Write minimal test support**

若测试桩未记录 Delete 参数，仅为 fake orchestrator 增加 `deletedAppID string` 并在 `Delete` 写入该字段；不修改生产 `AppDeleteHandler`，因为它已调用 `orch.Delete`。

- [ ] **Step 4: Run focused regression**

Run: `go test ./internal/service -run '^(TestAppServiceDeleteHiddenAICCAppEnqueuesRuntimeCleanup|TestAICCServiceStatusAndDelete|TestAICCServiceDeleteAgentQueuesHiddenAppCleanup|TestAICCServiceDeleteAgentReturnsHiddenAppCleanupError)$' -count=1 -v && go test ./internal/worker/handlers -run '^TestAppDeleteHandlerDeletesAICCRuntimeResources$' -count=1 -v && go test ./internal/integrations/k8sorch -run '^(TestDeleteAICCDeletesHPA|TestDeleteAICCDeletesNetworkPolicyAfterPodReclaimed)$' -count=1 -v`

Expected: all PASS. Do not run full E2E; this is a backend deletion-flow change with focused unit and adapter coverage.

- [ ] **Step 5: Commit**

Run: `git add internal/worker/handlers/app_runtime_ops_test.go && git commit -m "test(aicc): 覆盖删除智能体的运行时回收合同" -m "验证 app_delete 通过 AICC 编排器清理隐藏应用的 Kubernetes 运行时资源。"`

## Plan Self-Review

- Spec coverage: Task 1 creates the retryable job, Task 2 invokes it from the reception deletion path while retaining business history, and Task 3 verifies the AICC orchestration cleanup contract.
- Placeholder scan: no implementation placeholders remain.
- Type consistency: `AICCHiddenAppDeleter.DeleteHiddenAICCApp` is implemented by `AppService`; the worker continues consuming the existing `domain.JobTypeAppDelete` and `{"app_id": ...}` payload.
