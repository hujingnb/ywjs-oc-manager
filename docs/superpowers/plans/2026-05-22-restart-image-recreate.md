# 重启流程支持运行时镜像变更（重建容器）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 当实例 restart 时检测到绑定版本解析出的运行时镜像与容器当前镜像（`apps.runtime_image_ref`）不一致，restart 必须重建容器（拉新镜像 → stop → remove 旧容器 → create 新容器 → start），让 `version_synced` 真实反映运行时状态；镜像未变时保持原 `stop → clear sessions → start` 行为。

**Chosen approach: Approach A（委托给 re-initialize）。** restart handler 检测到镜像变更后：stop + remove 旧容器 → 清空 `apps.container_id` → `SetAppStatus = pulling_runtime_image`（raw，restart 一贯不走 EnsureAppTransition）→ 创建 `app_initialize` job → notifier 入队 → 返回 nil。已有 `AppInitializeHandler` 重跑 4 阶段（pull → prepare → create → start → binding_waiting），完整复用已测代码。

**为什么不选 Approach B：** B 需要给 restart handler 注入 `imagecoord.Coordinator` / `NodeDockerProvider` / `ContainerCreator` / `GetRuntimeNode` 并复制 `phasePullRuntimeImage` + `phaseCreate` + `ContainerSpec` 装配逻辑——大量新依赖与重复代码，而这些逻辑已被 `app_initialize_test.go` 充分覆盖。Approach A 唯一的微妙点（状态机 `running → pulling_runtime_image` 不在 `appTransitions` map、`ensureAPIKey` 复用 active key）经核实均已满足：restart 用 raw `SetAppStatus` 不受状态机约束；init `Handle` 在 status=pulling_runtime_image 时正常推进；`ensureAPIKey` 对 active key 直接返回明文不重新建 token。

**关键约束：**
- restart 只清空 `container_id`，**不**重置 api_key（与 `RequestInitialize` 不同）——`ensureAPIKey` 见 active key 复用密文，不会重新建 new-api token。
- 镜像变更检测 = `refreshResult.ImageRef != app.RuntimeImageRef`。
- 入队 `app_initialize` job 后 restart 立即返回 nil，不再走 `SetAppModelSynced` / `SetAppAppliedVersion`（这些由 init handler 在到达 binding_waiting 时负责，避免对镜像维度谎报 synced）。
- 幂等：stop → remove → 清 container_id → 设状态 → 建 job 这一序列里，若 restart job 被 worker 重试（MaxAttempts=3），重入时 `container_id` 已空，必须跳过 stop/remove，仅重新建 job/入队。

**Tech Stack:** Go, pgx/v5, sqlc, 现有 worker handler 框架。

---

### Task 1: AppRuntimeStore 接口扩展 + restart handler 增加 image-change 重建分支

**Files:**
- Modify: `internal/worker/handlers/app_runtime_ops.go`

- [ ] **Step 1: 扩展 `AppRuntimeStore` 接口**

在 `AppRuntimeStore` 增加两个方法，签名与 `sqlc.Queries` 对齐（`*sqlc.Queries` 已实现，无需改 wiring 的 store 传参）：

```go
// SetAppContainer 在重建容器前清空 apps.container_id / container_name。
SetAppContainer(ctx context.Context, arg sqlc.SetAppContainerParams) (sqlc.App, error)
// CreateJob 在 restart 检测到镜像变更后入队 app_initialize 重新初始化。
CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
```

- [ ] **Step 2: 给 `AppRestartContainerHandler` 增加 JobNotifier 依赖与 setter**

`AppRestartContainerHandler` 结构体增加 `notifier` 字段，类型为本包新定义的最小接口（避免反向依赖 service 包）：

```go
// RestartJobNotifier 抽象向 Redis 队列即时推送 jobID 的能力。
// 与 service.JobNotifier 同形态；nil 时由 scheduler 兜底入队。
type RestartJobNotifier interface {
	Enqueue(ctx context.Context, jobID string) error
}
```

增加 `SetJobNotifier(n RestartJobNotifier)` setter（与现有 `SetSessionCleaner` / `SetInputRefresher` 风格一致，nil 安全）。

- [ ] **Step 3: 在 `Handle` 中插入 image-change 检测与重建分支**

在 `Handle` 内，`RefreshAppInput` 成功返回 `refreshResult` 之后、容器 stop/start 三步之前，插入分支：

- 若 `h.inputRefresher != nil && refreshResult.ImageRef != "" && refreshResult.ImageRef != app.RuntimeImageRef`：进入重建分支：
  1. 若 `app.ContainerID.String != ""`：`StopContainer` 然后 `RemoveContainer`（任一失败冒泡重试）。
  2. `SetAppContainer(ctx, sqlc.SetAppContainerParams{ID: app.ID})`（空 `ContainerID`/`ContainerName` → 清空）。
  3. `SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusPullingRuntimeImage})`（raw，沿用 restart 一贯不走 EnsureAppTransition）。
  4. 构造 `app_initialize` job payload：`json.Marshal(map[string]any{"app_id": uuidToString(app.ID), "runtime_node": nodeID})`（与 `RequestInitialize` 入队的 payload 同形态；`decodePayload` 只读这两字段）。
  5. `CreateJob(ctx, sqlc.CreateJobParams{Type: domain.JobTypeAppInitialize, Priority: 100, RunAfter: now, MaxAttempts: 3, PayloadJson: payload})`。
  6. `notifier != nil` 时 `_ = h.notifier.Enqueue(ctx, uuidToString(job.ID))`（入队失败不阻塞，scheduler 兜底）。
  7. `return nil` —— 不再执行后续 stop/clear/start，也不调 `SetAppModelSynced` / `SetAppAppliedVersion`（由 init handler 到达 binding_waiting 时负责）。
- 否则（镜像未变 / inputRefresher 为 nil）：保持现有 `stop → clear sessions → start` + `SetAppModelSynced` + `SetAppAppliedVersion` 逻辑完全不变。

幂等说明写进代码注释：重入时（job 重试）`container_id` 可能已为空 —— 此时跳过 stop/remove，直接重新建 job 并入队即可。

- [ ] **Step 4: 验证编译** — `go build ./...`，无输出。

---

### Task 2: 单元测试 —— image-change 触发重建，image-unchanged 保持原行为

**Files:**
- Modify: `internal/worker/handlers/app_runtime_ops_test.go`

- [ ] **Step 1: 扩展 `runtimeOpStub`** —— 增加 `SetAppContainer`（记录被调用并把 `app.ContainerID` 置空）、`CreateJob`（记录入参、返回带固定 ID 的 `sqlc.Job`）；增加 `fakeRestartNotifier` 实现 `RestartJobNotifier` 记录 `enqueuedJobID`。`fakeInputRefresher` 已支持 `returnResult`，复用。

- [ ] **Step 2: `TestAppRestartContainerHandler_ImageChangeRecreatesViaInitJob`** —— `app.RuntimeImageRef="hermes-v1:old"`，refresher 返回 `{7,"hermes-v2:new"}`，注入 sessionCleaner + notifier。断言：返回 nil；stop=1、remove=1、start=0；`containerCleared`；statusUpdates 含 `pulling_runtime_image`；`createdJobs` 1 条且 `Type==JobTypeAppInitialize`、payload `app_id` 正确；notifier 入队；`appliedVersionSet==false`、ModelSynced 未置位。

- [ ] **Step 3: `TestAppRestartContainerHandler_ImageUnchangedKeepsRestart`** —— `RuntimeImageRef` 与 refresher `ImageRef` 同值，断言走原路径：stop=1、start=1、remove=0、`createdJobs` 空、`appliedVersionSet==true`。

- [ ] **Step 4: `TestAppRestartContainerHandler_ImageChangeRetryAfterContainerCleared`** —— `app.ContainerID` 已空、镜像变更，断言 stop=0、remove=0，仍建 job 并入队。

- [ ] **Step 5: 验证既有测试不回归** —— `go test ./internal/worker/...`，ok。

---

### Task 3: cmd/server wiring + 提交

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: 注入 notifier** —— `restartHandler.SetInputRefresher(...)` 之后增加 `restartHandler.SetJobNotifier(redisQueue)`（`redisQueue` 已用作 `JobNotifier`，其 `Enqueue` 满足 `handlers.RestartJobNotifier`）。

- [ ] **Step 2: 验证** —— `go build ./...`、`go vet ./...`、`go test ./internal/worker/... ./cmd/...` 全绿。

- [ ] **Step 3: Commit** —— `feat(worker/restart): 重启检测镜像变更时重建容器`。

---

## 验证清单
- `go build ./...`
- `go vet ./...`
- `go test ./internal/worker/... ./cmd/...`
