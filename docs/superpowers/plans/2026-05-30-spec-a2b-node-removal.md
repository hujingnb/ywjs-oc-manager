# spec-A2b：节点概念删除 + 破坏性 migration + 渠道重载 k8s 化 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 彻底删除 runtime-agent 节点概念（代码/表/配置/前端），破坏性 migration 删 runtime_nodes 与两张资源采样表 + apps 三列，并补齐 A2a 推迟的渠道重载 k8s 化与 payload 残留清理。

**Architecture:** 三阶段安全顺序，每阶段保持 `go build ./... && go vet && go test` 全绿——① 解耦消费方（新增 RolloutRestart、渠道改 appID 重启、onboarding 去选节点、payload 残留清理、restart refresher 简化、RuntimeOpService 去 inspector、query 去节点列）→ ② 删死代码（节点 service/handler/包/路由/config/domain/wiring + node-upload 机器孤儿 + 前端节点页）→ ③ 破坏性 migration + sqlc 重生（最后做，此时已无代码引用待删 schema）。

**Tech Stack:** Go、k8s.io/client-go（RolloutRestart patch）、golang-migrate（000003）、sqlc、stretchr/testify、guregu/null/v5；前端 Vue 3 + vitest；本地 k3d MySQL。

**设计依据：** `docs/superpowers/specs/2026-05-30-spec-a2b-node-removal-design.md`（权威）。注意：本计划按 spec 的**破坏性**决策执行——payload 残留、App 三列、JobType 常量均**删除**，不保留。

---

## 文件结构总览

**整删文件：**
```
internal/integrations/agent/                  internal/service/runtime_node_service.go(+_test)
internal/integrations/runtime/                internal/service/probe_reconciler.go(+_test)
internal/runtime/imagecoord/                  internal/service/node_selector.go(+_test)
internal/runtime/agent/                        internal/service/resource_metrics_service.go(+_test)
cmd/runtime-agent/                             internal/api/handlers/agent.go(+_test)
internal/store/queries/runtime_nodes.sql       internal/api/handlers/runtime_nodes.go(+_test)
internal/store/queries/resource_samples.sql    internal/api/handlers/resource_metrics.go(+_test)
internal/store/sqlc/runtime_nodes.sql.go        internal/worker/handlers/app_health_check.go(+_test)
internal/store/sqlc/resource_samples.sql.go     internal/worker/handlers/runtime_refresh_status.go(+_test)
web/src/pages/runtime-nodes/                    web/src/api/hooks/useRuntimeNodes.ts
web/src/components/ResourceTrendChart.vue(+spec) web/src/components/RuntimeStatusTag.vue
```

**改动文件（关键）：**
```
internal/integrations/k8sorch/{orchestrator.go,adapter.go,adapter_test.go}  — +RolloutRestart
internal/worker/handlers/{channel_login.go,app_runtime_ops.go,app_initialize.go}  — 去节点
internal/integrations/channel/adapter.go  — AuthInput 去 NodeID/ContainerID
internal/service/{onboarding_service.go,runtime_operation_service.go,reconciler.go}
internal/worker/reaper/reaper.go
internal/store/queries/apps.sql + sqlc 重生
internal/domain/enums.go    internal/config/{config.go,loader.go}    internal/api/router.go
cmd/server/{main.go,wiring.go}
web/src/{app/router.ts,layouts/DashboardLayout.vue,pages/dashboard/RoleAwareHome.vue,pages/apps/AppRuntimeTab.vue}
internal/migrations/000003_drop_node_concept.{up,down}.sql   sqlc.yaml
```

**陷阱（待删邻近但必须保留）：**
- `cmd/server/main.go` 的 `imagecoordRedis` + `distLocker`：被 reaper 消费，**保留**。只删 `imagecoord.Coordinator`（A2a 已删）相关，不删 distLocker。
- `internal/service/reconciler.go` 的 `PeriodicReconciler`（109-143）：被 app_status_reconcile / ragflow / cleanup 消费，**保留**；只删同文件的 `NodeHealthReconciler`（30-107）。
- `internal/worker/handlers/app_runtime_ops.go` 的 `appOrchestrator.Status`、`app_status_reconciler`：k8s 核心，保留。
- `web/src/pages/usage/*`（new-api 计费用量，实时）：与资源采样无关，**保留不动**。

---

## Phase 1 — 解耦消费方（每任务结束 `go build ./... && go test ./...` 全绿）

### Task 1：Orchestrator.RolloutRestart

**Files:**
- Modify: `internal/integrations/k8sorch/orchestrator.go`（接口加方法，约 line 24 后）
- Modify: `internal/integrations/k8sorch/adapter.go`（实现，仿 UpdateImage line 100-114）
- Modify: `internal/integrations/k8sorch/adapter_test.go`（fake clientset 单测）
- Modify: `internal/worker/handlers/app_runtime_ops.go`（`appOrchestrator` 窄接口 line 40-50 加方法）

- [ ] **Step 1: 接口加方法**

`orchestrator.go` 的 `Orchestrator` 接口在 `Status(...)` 后加：
```go
	// RolloutRestart 触发 Deployment 滚动重启（patch pod template 注解），
	// 不改镜像/副本数，按 Recreate 策略重建 pod。渠道绑定后重载 hermes platform 用。
	RolloutRestart(ctx context.Context, appID string) error
```

- [ ] **Step 2: 写失败单测**

`adapter_test.go` 新增（fake clientset 建好 Deployment 后断言 RolloutRestart patch 了注解）：
```go
// TestRolloutRestartPatchesAnnotation 验证 RolloutRestart 给 pod template 写入 restartedAt 注解、不动镜像/副本。
func TestRolloutRestartPatchesAnnotation(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	require.NoError(t, a.EnsureApp(context.Background(), testSpec()))
	require.NoError(t, a.RolloutRestart(context.Background(), "a1"))
	d, err := cs.AppsV1().Deployments("oc-apps").Get(context.Background(), "app-a1", metav1.GetOptions{})
	require.NoError(t, err)
	// pod template 注解应含 restartedAt，触发 Deployment 重建 pod
	assert.NotEmpty(t, d.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"])
	// 副本数不变（仍为 1）
	require.NotNil(t, d.Spec.Replicas)
	assert.Equal(t, int32(1), *d.Spec.Replicas)
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/integrations/k8sorch/ -run TestRolloutRestart -v`
Expected: 编译失败（`RolloutRestart` 未定义）。

- [ ] **Step 4: 实现**

`adapter.go` 在 `UpdateImage`（line 114）后加（仿其 Get→改→Update 模式；时间戳不能用 `time.Now()` 以外的禁用 API，这里 `time.Now()` 在生产代码允许）：
```go
// RolloutRestart 给 Deployment 的 pod template 注解写入当前时间戳，触发 Deployment 按
// Recreate 策略重建 pod（等价 kubectl rollout restart）。用于渠道绑定后重载 hermes platform。
func (a *KubernetesAdapter) RolloutRestart(ctx context.Context, appID string) error {
	api := a.client.AppsV1().Deployments(a.namespace)
	d, err := api.Get(ctx, deploymentName(appID), metav1.GetOptions{})
	if err != nil {
		return wrapK8s("查询 Deployment", err)
	}
	if d.Spec.Template.Annotations == nil {
		d.Spec.Template.Annotations = map[string]string{}
	}
	d.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().UTC().Format(time.RFC3339)
	_, uerr := api.Update(ctx, d, metav1.UpdateOptions{})
	return wrapK8s("滚动重启 Deployment", uerr)
}
```
（`time` 已 import；若未 import 则加。）

- [ ] **Step 5: 窄接口加方法**

`app_runtime_ops.go` 的 `appOrchestrator` 接口（line 40-50）在 `Status` 后加：
```go
	// RolloutRestart 触发 Deployment 滚动重启（渠道绑定后重载 hermes platform）。
	RolloutRestart(ctx context.Context, appID string) error
```
若 `app_runtime_ops.go` 内有 `fakeAppOrchestrator`（测试桩），给它加 `RolloutRestart` 空实现使其仍满足接口。同样检查 `app_runtime_ops` 各 _test.go 里的 fake。

- [ ] **Step 6: 运行确认通过 + Commit**

Run: `go build ./... && go test ./internal/integrations/k8sorch/ ./internal/worker/handlers/ -v`
Expected: PASS。
```bash
git add internal/integrations/k8sorch/orchestrator.go internal/integrations/k8sorch/adapter.go internal/integrations/k8sorch/adapter_test.go internal/worker/handlers/app_runtime_ops.go
git commit -F - <<'EOF'
feat(k8sorch): 新增 Orchestrator.RolloutRestart

KubernetesAdapter patch Deployment pod template 的 restartedAt 注解触发 Recreate
重建 pod（等价 kubectl rollout restart），不改镜像/副本。appOrchestrator 窄接口同步
加方法。供渠道绑定后重载 hermes platform 用（Task 2）。fake clientset 单测断言注解
写入且副本不变。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 2：渠道重载改 appID 维度（去 docker restarter + AuthInput 节点字段）

**Files:**
- Modify: `internal/worker/handlers/channel_login.go`（ChannelRestarter 接口 164-166、finalizeChannelBound 307、PollAuth/BeginAuth 注入 88-89/205-206）
- Modify: `internal/integrations/channel/adapter.go`（AuthInput 去 NodeID/ContainerID，61-70）
- Modify: `internal/worker/handlers/channel_login_test.go`（fake restarter 改签名）
- Create: `cmd/server/orch_channel_restarter.go`（orch 驱动的 restarter 适配，避免 main 臃肿）
- Modify: `cmd/server/main.go`（SetRestarter 注入改 orch 驱动）

- [ ] **Step 1: 改 ChannelRestarter 接口**

`channel_login.go` line 164-166：
```go
// ChannelRestarter 抽象重启 app 运行时（hermes）让其重载渠道 platform 配置的能力。
// k8s 下由 Orchestrator.RolloutRestart 实现（按 appID 重建 pod）。
type ChannelRestarter interface {
	RestartApp(ctx context.Context, appID string) error
}
```

- [ ] **Step 2: 改 finalizeChannelBound 调用点**

`channel_login.go` line 307：把
`h.restarter.RestartContainer(ctx, app.RuntimeNodeID.String, app.ContainerID.ValueOrZero())`
改为：
```go
		if err := h.restarter.RestartApp(ctx, app.ID); err != nil {
```
（保留其外层 `if h.restarter != nil` 与错误处理逻辑不变。）

- [ ] **Step 3: 去 AuthInput 注入的节点字段**

`channel_login.go` 的 BeginAuth 注入（约 85-91）删 line 88-89 的 `NodeID`/`ContainerID` 两行；PollAuth 注入（约 202-207）删 line 205-206 两行。保留 `AppID`/`OwnerUserID`/`Endpoint`。
`channel/adapter.go` `AuthInput` 结构（61-70）删 `NodeID string`（62）与 `ContainerID string`（63）字段及注释。

- [ ] **Step 4: grep 确认 AuthInput.NodeID/ContainerID 无其它读取**

Run: `grep -rn "\.NodeID\|\.ContainerID" internal/integrations/channel/ internal/integrations/hermes/ | grep -i auth`
Expected: 无（确认删字段安全）。若有读取处一并清理。

- [ ] **Step 5: 改测试 fake restarter**

`channel_login_test.go` 里实现 `ChannelRestarter` 的 fake：把方法签名由 `RestartContainer(ctx, nodeID, containerID string)` 改为 `RestartApp(ctx, appID string)`，断言相应改为按 appID。

- [ ] **Step 6: orch 驱动的 restarter 适配**

Create `cmd/server/orch_channel_restarter.go`：
```go
package main

import (
	"context"

	"oc-manager/internal/integrations/k8sorch"
)

// orchChannelRestarter 用 Orchestrator.RolloutRestart 实现 handlers.ChannelRestarter，
// 把渠道绑定后的 hermes 重载落到 k8s pod 重建（取代 docker AgentBackedAdapter.RestartContainer）。
type orchChannelRestarter struct{ orch k8sorch.Orchestrator }

func (r orchChannelRestarter) RestartApp(ctx context.Context, appID string) error {
	if r.orch == nil {
		// k8s 未启用（降级）：无编排器可重启，返回 nil 不阻断绑定状态闭环。
		return nil
	}
	return r.orch.RolloutRestart(ctx, appID)
}
```

- [ ] **Step 7: main 注入改 orch 驱动**

`main.go` 把 `channelCheckHandler.SetRestarter(runtimeAdapter)`（约 line 440，含其上方 A2a 标注的盲点注释块）改为：
```go
	// 渠道绑定后重载 hermes platform：经 Orchestrator.RolloutRestart 重建 pod（spec-A2b 落地）。
	channelCheckHandler.SetRestarter(orchChannelRestarter{orch: orch})
```
删除其上方 A2a 遗留的「docker restarter 失效/A2b 缺口」注释块。

- [ ] **Step 8: 编译测试 + Commit**

Run: `go build ./... && go test ./internal/worker/handlers/ ./internal/integrations/channel/ -v`
Expected: PASS。
```bash
git add internal/worker/handlers/channel_login.go internal/worker/handlers/channel_login_test.go internal/integrations/channel/adapter.go cmd/server/orch_channel_restarter.go cmd/server/main.go
git commit -F - <<'EOF'
feat(channel): 渠道绑定后重载 hermes 改 k8s rollout restart

ChannelRestarter 接口由 RestartContainer(nodeID,containerID) 改为 RestartApp(appID)，
finalizeChannelBound 按 appID 调用；main 注入 orch 驱动的 orchChannelRestarter（经
Orchestrator.RolloutRestart 重建 pod）取代失效的 docker restarter。AuthInput 去掉 k8s 下
恒空的 NodeID/ContainerID（oc-ops 经 Endpoint 寻址）。补齐 spec-A2a 推迟的渠道重载缺口。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 3：restart inputRefresher 简化为版本镜像解析（解除 runtimeAdapter 依赖）

**Files:**
- Modify: `cmd/server/wiring.go`（appInputRefresher 251-350+，去 uploader/skillBlobs/manifest 写入）
- Modify: `cmd/server/main.go`（newAppInputRefresher 调用去 runtimeAdapter/skillBlobs 实参）

- [ ] **Step 1: 简化 appInputRefresher 结构与构造**

`wiring.go` 把 `appInputRefresher` 结构（251-270）精简为只保留 image 解析所需：
```go
// appInputRefresher 实现 workerhandlers.AppInputRefresher：k8s 下 pod 配置由 bootstrap 在
// 启动时交付，restart 不再向节点写 manifest；这里只解析「当前绑定版本的镜像 ref 与 revision」，
// 供 restart handler 做镜像变更检测与记录 applied 版本。
type appInputRefresher struct {
	queries      appInputRefresherQueries
	resolveImage func(imageID string) (string, bool)
}
```
`newAppInputRefresher` 构造改为：
```go
func newAppInputRefresher(queries appInputRefresherQueries, resolveImage func(string) (string, bool)) *appInputRefresher {
	return &appInputRefresher{queries: queries, resolveImage: resolveImage}
}
```

- [ ] **Step 2: 简化 RefreshAppInput 为镜像解析**

`wiring.go` 把 `RefreshAppInput`（286-350+ 完整方法）替换为只解析版本镜像：
```go
// RefreshAppInput 只解析当前绑定版本的镜像 ref 与 revision（不再写节点 manifest）。
// nodeID 参数保留以匹配 workerhandlers.AppInputRefresher 接口，但 k8s 下忽略。
func (r *appInputRefresher) RefreshAppInput(ctx context.Context, _ string, app sqlc.App) (workerhandlers.AppInputRefreshResult, error) {
	if r.queries == nil || r.resolveImage == nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("appInputRefresher 依赖未注入")
	}
	if !app.VersionID.Valid {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("应用未绑定助手版本, 无法解析镜像")
	}
	version, err := r.queries.GetAssistantVersion(ctx, app.VersionID.String)
	if err != nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("加载助手版本失败: %w", err)
	}
	imageRef, ok := r.resolveImage(version.ImageID)
	if !ok {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("版本镜像 %s 未在配置中", version.ImageID)
	}
	return workerhandlers.AppInputRefreshResult{VersionRevision: version.Revision, ImageRef: imageRef}, nil
}
```
删除 `appInputRefresherQueries` 接口中此后不再用的方法（保留 `GetAssistantVersion`；删 `GetOrganization`/`GetUser`/`GetApp` 若仅此处用——grep 确认）。删 `wiring.go` 顶部因此变 unused 的 import（auth、hermes、bytes 等）。

- [ ] **Step 3: 改 main 调用**

`main.go` 的 `restartHandler.SetInputRefresher(newAppInputRefresher(dbStore.Queries, runtimeAdapter, cipher, func(imageID string)..., skillBlobStore, handlers.AppInputBuildOptions{...}))`（约 402-416）改为：
```go
	restartHandler.SetInputRefresher(newAppInputRefresher(
		dbStore.Queries,
		func(imageID string) (string, bool) {
			return config.ResolveRuntimeImage(cfg.Hermes.RuntimeImages, imageID)
		},
	))
```

- [ ] **Step 4: 编译测试 + Commit**

Run: `go build ./... && go test ./cmd/... ./internal/worker/handlers/ -v`
Expected: PASS（runtimeAdapter 仍被其它处引用，本任务不删它）。
```bash
git add cmd/server/wiring.go cmd/server/main.go
git commit -F - <<'EOF'
refactor(server): restart inputRefresher 简化为版本镜像解析

k8s 下 pod 配置由 bootstrap 启动时交付，restart 不再向节点写 manifest。appInputRefresher
去掉 uploader/cipher/skillBlobs/manifest 写入，只 GetAssistantVersion + resolveImage 返回
{ImageRef, VersionRevision} 供镜像变更检测。解除对 runtimeAdapter（uploader）的依赖，
为 Phase 2 删 runtimeAdapter 与 node-upload 机器铺路。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 4：Onboarding 去节点选择

**Files:**
- Modify: `internal/service/onboarding_service.go`（NodeSelector 注入、selectNode 80-114、NodeID 字段 124/139、payload 283-286/450-453）
- Modify: `internal/store/queries/apps.sql`（CreateApp 1-14 去 runtime_node_id）→ sqlc 重生
- Modify: `internal/service/onboarding_service_test.go`
- Modify: `cmd/server/main.go`（nodeSelector 140-141）

- [ ] **Step 1: CreateApp query 去 runtime_node_id**

`apps.sql` 的 `CreateApp`（1-14）删 `runtime_node_id,`（line 6）与对应一个 `?`，INSERT 列与占位同步减一。

- [ ] **Step 2: sqlc 重生**

Run: `make sqlc-generate`
Expected: `CreateAppParams` 去掉 `RuntimeNodeID` 字段（表列仍在，Phase 3 才删；此处仅 query 不再写它）。

- [ ] **Step 3: 去 NodeSelector 注入与 selectNode**

`onboarding_service.go`：
- 删 `MemberOnboardingService` 结构里的 `nodeSelector` 字段与 `NodeSelector` 接口声明。
- 删 `selectNode()` 方法（80-114）。
- `NewMemberOnboardingService` 构造去掉 `selector` 参数。
- `OnboardMember`：删 `input.NodeID = chosen`（181）及其上方调 `selectNode` 的块。
- `OnboardMemberInput`（124）删 `NodeID string` 字段；`CreateAppForMemberInput`（139）删 `NodeID string`。
- CreateApp 入队 payload（283-286、450-453）：删 map 里的 `"runtime_node": input.NodeID` 键（payload 只留 `"app_id"`）。
- `CreateApp` 调用去掉 `RuntimeNodeID` 实参（sqlc 重生后该字段已不存在）。

- [ ] **Step 4: 改测试**

`onboarding_service_test.go`：去掉对 NodeSelector 的 fake 注入与 NodeID 断言；`NewMemberOnboardingService` 调用去 selector 参数；断言入队 payload 不含 runtime_node。

- [ ] **Step 5: 改 main 装配**

`main.go` line 140-141：删 `nodeSelector := service.NewSQLNodeSelector(dbStore.Queries)`；`onboardingService := service.NewMemberOnboardingService(store.NewOnboardingRunner(dbStore), hashPasswordWithDefault)`（去 nodeSelector 实参）。

- [ ] **Step 6: 编译测试 + Commit**

Run: `go build ./... && go test ./internal/service/ ./cmd/... -v`
Expected: PASS。
```bash
git add internal/store/queries/apps.sql internal/store/sqlc/apps.sql.go internal/service/onboarding_service.go internal/service/onboarding_service_test.go cmd/server/main.go
git commit -F - <<'EOF'
refactor(onboarding): 去节点选择，交 k8s 调度落点

删 SQLNodeSelector 注入与 selectNode 自动选节点逻辑；OnboardMemberInput/
CreateAppForMemberInput 去 NodeID 字段；CreateApp query 与入队 payload 去 runtime_node。
k8s 模型下 app 是 namespace 内 Deployment，pod 落点由调度器决定，manager 不再选节点。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 5：payload 残留清理（app_initialize / app_runtime_ops）

**Files:**
- Modify: `internal/worker/handlers/app_initialize.go`（payload 605-608、audit 475-481、GetRuntimeNode 接口 32、restart nodeID）
- Modify: `internal/worker/handlers/app_runtime_ops.go`（入队 payload 321-327、SetAppContainer 接口 32 与 restart nodeID 286）
- Modify: 对应 _test.go（appInitStub.GetRuntimeNode 等）

- [ ] **Step 1: app_initialize payload 去 RuntimeNodeID**

`app_initialize.go`：
- `appInitializePayload`（605-608）删 `RuntimeNodeID string `json:"runtime_node"`` 字段，只留 `AppID`。
- `writeInitAuditLog`（475-481）的 `auditMetadata` map 删 `"runtime_node": payload.RuntimeNodeID` 键，只留 `"job_id"`。
- `AppInitializeStore` 接口（line 32）删 `GetRuntimeNode(...)` 方法。

- [ ] **Step 2: app_runtime_ops 入队 payload + SetAppContainer + restart nodeID**

`app_runtime_ops.go`：
- restart-镜像变 入队 app_initialize 的 payload（321-327）：map 删 `"runtime_node": nodeID` 键，只留 `"app_id"`。
- `AppRuntimeStore` 接口（line 32）删 `SetAppContainer(...)` 方法（无调用方，A2a 后已不写 container_id）。
- restart 的 `nodeID := app.RuntimeNodeID.String`（286）：删该行；`RefreshAppInput` 调用第二参（nodeID）传 `""`（接口签名保留 nodeID 形参但已忽略，见 Task 3）。

- [ ] **Step 3: 改测试**

去掉测试 stub 里的 `GetRuntimeNode` / `SetAppContainer` 实现（若 stub 实现了这些接口方法）；断言入队 payload 与 audit metadata 不含 runtime_node。

- [ ] **Step 4: grep 确认无残留**

Run: `grep -rn "runtime_node\|RuntimeNodeID\|GetRuntimeNode\|SetAppContainer" internal/worker/handlers/ | grep -v _test.go`
Expected: 仅剩读 `app.RuntimeNodeID`（若有，Phase 3 删列前应已清；本步后 handlers 不应再引用）。逐条核实并清理。

- [ ] **Step 5: 编译测试 + Commit**

Run: `go build ./... && go test ./internal/worker/handlers/ -v`
Expected: PASS。
```bash
git add internal/worker/handlers/app_initialize.go internal/worker/handlers/app_runtime_ops.go internal/worker/handlers/app_initialize_test.go internal/worker/handlers/app_runtime_ops_test.go
git commit -F - <<'EOF'
refactor(worker): 清理 app_initialize/app_runtime_ops 的 runtime_node 残留

删 appInitializePayload.RuntimeNodeID 字段、init audit 的 runtime_node 元数据、
AppInitializeStore.GetRuntimeNode 接口方法、AppRuntimeStore.SetAppContainer 接口方法、
restart 入队 payload 的 runtime_node 与 restart 的 nodeID 取值。k8s 路径不再有节点概念，
这些 docker 时代残留字段清空，为 Phase 3 删 apps 列铺路。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 6：reaper 去节点 + apps.sql 节点 query 清理

**Files:**
- Modify: `internal/worker/reaper/reaper.go`（node_id 日志 133、payload 181-184）
- Modify: `internal/store/queries/apps.sql`（ListRunningApps 83-93、ListStaleInits 144-152、删 ListAppsByRuntimeNode 33-38、删 SetAppContainer 45-48）→ sqlc 重生
- Modify: reaper 测试与 ListRunningApps/ListStaleInits 消费方

- [ ] **Step 1: apps.sql 改 query**

`apps.sql`：
- `ListRunningApps`（83-93）：SELECT 去掉 `runtime_node_id, container_id`，只留 `id`（消费方 app_status_reconciler 仅用 id）。
- `ListStaleInits`（144-152）：SELECT 去掉 `runtime_node_id`，留 `id, status`。
- 删整个 `ListAppsByRuntimeNode`（33-38）。
- 删整个 `SetAppContainer`（45-48）。

- [ ] **Step 2: sqlc 重生**

Run: `make sqlc-generate`
Expected: `ListRunningAppsRow` 只剩 `ID`；`ListStaleInitsRow` 去 `RuntimeNodeID`；`ListAppsByRuntimeNode`/`SetAppContainer` 方法消失。（表列仍在，Phase 3 删。）

- [ ] **Step 3: reaper 去节点**

`reaper.go`：
- line 133 删日志参数 `"node_id", row.RuntimeNodeID`。
- 入队 payload（181-184）map 删 `"runtime_node": row.RuntimeNodeID`，只留 `"app_id"`。
- reaper 的 `Store` 接口里若声明了 `ListAppsByRuntimeNode` 等已删 query，同步删。

- [ ] **Step 4: 修消费方编译**

`app_status_reconciler.go` 的 `ListRunningApps` 消费：现返回 `[]sqlc.ListRunningAppsRow{ID}`，确认只用 `.ID`（A2a Task 13 即如此）。其它 `ListRunningApps`/`ListStaleInits` 消费方随结构变更修正。

- [ ] **Step 5: 编译测试 + Commit**

Run: `go build ./... && go test ./internal/worker/... ./internal/service/ -v`
Expected: PASS。
```bash
git add internal/store/queries/apps.sql internal/store/sqlc/apps.sql.go internal/worker/reaper/reaper.go internal/worker/reaper/reaper_test.go
git commit -F - <<'EOF'
refactor(store): apps query 与 reaper 去节点列

ListRunningApps 去 runtime_node_id/container_id（消费方仅用 id），ListStaleInits 去
runtime_node_id，删 ListAppsByRuntimeNode 与 SetAppContainer query；reaper 去 node_id
日志与 payload 的 runtime_node。sqlc 重生。为 Phase 3 删 apps 列铺路。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 7：RuntimeOperationService 去 docker inspector

**Files:**
- Modify: `internal/service/runtime_operation_service.go`（RuntimeInspector 44-46、SetInspector 107-109、InspectApp 118-145）
- Modify: `internal/service/runtime_operation_service_test.go`
- Modify: `cmd/server/main.go`（SetInspector 200 + newRuntimeInspectorWrapper 定义）

- [ ] **Step 1: 简化 InspectApp 为库内状态**

`runtime_operation_service.go`：
- 删 `RuntimeInspector` 接口（44-46）、`RuntimeContainerInfo`（若仅此用）、`inspector` 字段、`SetInspector`（107-109）。
- `InspectApp`（118-145）简化为只读库内状态 + 快照：
```go
// InspectApp 返回应用运行时视图：k8s 下运行态由 status reconciler 写入 apps.status /
// runtime_snapshot_json，直接读库返回，不再 docker inspect。
func (s *RuntimeOperationService) InspectApp(ctx context.Context, principal auth.Principal, appID string) (RuntimeView, error) {
	app, err := s.loadAuthorizedApp(ctx, principal, appID)
	if err != nil {
		return RuntimeView{}, err
	}
	return RuntimeView{Status: app.Status, Snapshot: snapshotFromApp(app)}, nil
}
```
（`loadAuthorizedApp` / `snapshotFromApp` 沿用现有；若 `RuntimeView.Container` 字段仅 docker 用，删该字段并修 DTO/前端引用——前端在 Task 14，openapi 同步在 Task 15。）

- [ ] **Step 2: 改测试**

`runtime_operation_service_test.go`：删 inspector 注入与 docker 分支断言；保留/新增「InspectApp 返回库内 status + snapshot」用例。

- [ ] **Step 3: 改 main**

`main.go` line 200：删 `runtimeOpService.SetInspector(newRuntimeInspectorWrapper(runtimeAdapter))` 整行；删 `newRuntimeInspectorWrapper` 函数定义（cmd/server 内，grep 定位）与其因此 unused 的 import。

- [ ] **Step 4: 编译测试 + Commit**

Run: `go build ./... && go test ./internal/service/ ./cmd/... -v`
Expected: PASS。
```bash
git add internal/service/runtime_operation_service.go internal/service/runtime_operation_service_test.go cmd/server/main.go
git commit -F - <<'EOF'
refactor(service): RuntimeOperationService 去 docker inspector

InspectApp 不再经 agent docker 代理 inspect 容器，退回读库内 apps.status +
runtime_snapshot_json（k8s 下运行态由 status reconciler 写入）。删 RuntimeInspector
接口/SetInspector/newRuntimeInspectorWrapper。保留启停/重启触发能力。解除 InspectApp
对 runtimeAdapter 的依赖。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 2 — 删死代码（每任务连同其 main/router 装配一起删，保持 build 绿）

> **Phase 2 安全顺序原则（每 commit 必须编译通过）**：先删「消费方」再删「被消费方」。Task 8 移除全部
> **使用**节点/指标服务的代码（API handler + router + main 装配/Dependencies 传参 + 周期任务），此后这些
> service 类型虽仍定义但已无引用（Go 允许未引用的包级类型/函数，只禁止未使用的局部变量——构造已删）；
> Task 9 再删 service 文件本身。这样两个 commit 都编译通过，避免「删类型时 router/main 仍引用」的断裂。

### Task 8：移除节点/指标服务的全部消费方（API + router + main 装配）

**Files:**
- Delete: `internal/api/handlers/agent.go`(+_test)、`internal/api/handlers/runtime_nodes.go`(+_test)、`internal/api/handlers/resource_metrics.go`(+_test)
- Modify: `internal/api/router.go`（Dependencies 30/40/66、Register 块 107-114/154-155/172-173）
- Modify: `cmd/server/main.go`（runtimeNodeStore/runtimeNodeService 143-144、resourceMetricsService 162、nodeHealth/nodeHealthTask 525-526、nodeProbe/nodeProbeTask 530-539、eg.Go 591-592、Dependencies 传参）

- [ ] **Step 1: 删节点/指标 API handler 文件**

```bash
git rm internal/api/handlers/agent.go internal/api/handlers/agent_test.go \
  internal/api/handlers/runtime_nodes.go internal/api/handlers/runtime_nodes_test.go \
  internal/api/handlers/resource_metrics.go internal/api/handlers/resource_metrics_test.go
```

- [ ] **Step 2: router.go 删 Dependencies 字段与 Register 块**

`router.go` 删三个 Dependencies 字段：`RuntimeNodeService`（29-30）、`ResourceMetricsService`（39-40）、`EnrollmentSecret`（65-66）；删三个 Register 块：agent 路由（107-114）、runtime-node 路由（154-155）、resource-metrics 路由（172-173）。**保留** `RegisterAppRuntimeRoutes`（176，用 RuntimeOpService，留）。

- [ ] **Step 3: main 删服务构造、周期任务与 Dependencies 传参**

`main.go` 删：`runtimeNodeStore := store.NewRuntimeNodeStore(...)` + `runtimeNodeService := service.NewRuntimeNodeService(...)`（143-144）；`resourceMetricsService := service.NewResourceMetricsService(...)`（162）；`nodeHealth`/`nodeHealthTask`（525-526）、`nodeProbe`/`nodeProbeTask`（530-539）构造；`eg.Go(...nodeHealthTask.Run...)` / `eg.Go(...nodeProbeTask.Run...)`（591-592）；`api.Dependencies{...}` 里 `RuntimeNodeService` / `ResourceMetricsService` / `EnrollmentSecret` 三个传参（grep 定位）。
> 注：`nodeProbe` 构造用了 `agentTokenResolver` + `agent.NewProbeClient(...)`；删 nodeProbe 后这两者由 Task 11（删 agent 包）统一处理，本任务不删 agentTokenResolver。

- [ ] **Step 4: 编译测试 + Commit**

Run: `go build ./... && go test ./internal/api/... ./cmd/... -v`
Expected: PASS（runtime_node_service.go / resource_metrics_service.go / NodeHealthReconciler 等文件仍在，但已无引用——Go 不报未用包级类型）。
```bash
git add -u && git add internal/api/router.go cmd/server/main.go
git commit -F - <<'EOF'
refactor(api): 移除节点/资源指标 API 与 main 装配（消费方先行）

删 AgentEndpointsHandler/RuntimeNodesHandler/ResourceMetricsHandler 及单测、router 三个
Dependencies 字段与 Register 块、main 的 runtimeNodeService/resourceMetricsService 构造与
nodeHealth/nodeProbe 周期任务。保留 AppRuntime 路由。service 文件本身由 Task 9 删除，
本步先移除全部消费方以保证每个 commit 编译通过。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 9：删节点/指标 service 文件（已无消费方）

**Files:**
- Delete: `internal/service/runtime_node_service.go`(+_test)、`internal/service/probe_reconciler.go`(+_test 若有)、`internal/service/resource_metrics_service.go`(+_test)、`internal/service/node_selector.go`(+_test，Task 4 已删 selectNode 消费，此处删文件)
- Modify: `internal/service/reconciler.go`（删 NodeHealthReconciler 30-107，保留 PeriodicReconciler 109-143）+ `reconciler_test.go`

- [ ] **Step 1: 删 service 文件**

```bash
git rm internal/service/runtime_node_service.go internal/service/runtime_node_service_test.go \
  internal/service/probe_reconciler.go internal/service/resource_metrics_service.go \
  internal/service/resource_metrics_service_test.go internal/service/node_selector.go internal/service/node_selector_test.go
# probe_reconciler 若有专属 _test 同删
```
> `node_selector.go` 的 selectNode 消费方已在 Task 4 删除；这里删文件本身。若 Task 4 已 `git rm` node_selector，本行跳过。

- [ ] **Step 2: reconciler.go 删 NodeHealthReconciler 保留 PeriodicReconciler**

`reconciler.go` 删 `NodeHealthReconciler` 结构（30）、`NewNodeHealthReconciler`（38）、`SetClock`（46）、`Reconcile`（50）、`markRunningAppsAsError`（82），以及仅被它用的 `ReconcilerStore` 接口。**保留** `PeriodicReconciler`（109-143）与其方法（被 app_status_reconcile / ragflow / cleanup 用）。`reconciler_test.go` 删 NodeHealthReconciler 用例（58-122），保留 PeriodicReconciler 用例（若有）。

- [ ] **Step 3: grep 确认 + 编译测试 + Commit**

Run: `grep -rn "RuntimeNodeService\|NodeHealthReconciler\|RuntimeNodeProbeReconciler\|ResourceMetricsService\|NewRuntimeNodeStore\|NodeSelector\|SQLNodeSelector" internal/ cmd/ --include=*.go | grep -v _test.go`
Expected: 无。
Run: `go build ./... && go test ./internal/service/ ./cmd/... -v`
```bash
git add -u && git add internal/service/reconciler.go
git commit -F - <<'EOF'
refactor(service): 删除节点健康/探测/管理/指标 service 文件

删 RuntimeNodeService、probe_reconciler、ResourceMetricsService、node_selector 文件及单测；
reconciler.go 删 NodeHealthReconciler 保留通用 PeriodicReconciler。消费方已在 Task 8/4
移除，此处删文件本身，build 仍绿。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 10：删 docker worker handler + domain 节点常量/job 类型

> **顺序要点**：`app_health_check.go` / `runtime_refresh_status.go` **import `internal/integrations/runtime`**
> （ContainerInfo 等），必须**先**删这两个 handler（本任务），**再**删 integration 包（Task 11），否则删包时
> handler 编译断裂。

**Files:**
- Delete: `internal/worker/handlers/app_health_check.go`(+_test)、`runtime_refresh_status.go`(+_test)
- Modify: `internal/domain/enums.go`（RuntimeNodeStatus* 40-45、validRuntimeNodeStatuses 113-119、JobTypeRuntimeNodeHealthReconcile 83、JobTypeRuntimeRefreshStatus 85、JobTypeAppHealthCheck 87）
- Modify: `cmd/server/main.go`（healthCheckHandler/runtimeRefreshHandler 注册——A2a 后仍注册但 dormant）

- [ ] **Step 1: grep 确认 job 类型无在途消费**

Run: `grep -rn "JobTypeRuntimeNodeHealthReconcile\|JobTypeRuntimeRefreshStatus\|JobTypeAppHealthCheck" internal/ cmd/ --include=*.go | grep -v enums.go`
Expected: 仅 main.go 的 handler 注册 + 被删 handler 自身。确认无 dispatcher 入队（A2a 已删）。

- [ ] **Step 2: 删 handler 文件**

```bash
git rm internal/worker/handlers/app_health_check.go internal/worker/handlers/app_health_check_test.go \
  internal/worker/handlers/runtime_refresh_status.go internal/worker/handlers/runtime_refresh_status_test.go
```

- [ ] **Step 3: main 删 handler 注册**

`main.go` 删 `healthCheckHandler := handlers.NewAppHealthCheckHandler(...)` + `SetLifecycle` + `registry.Register(domain.JobTypeAppHealthCheck, ...)`；`runtimeRefreshHandler := handlers.NewRuntimeRefreshStatusHandler(...)` + `registry.Register(domain.JobTypeRuntimeRefreshStatus, ...)`。
> 此时 `runtimeAdapter` 仅剩 `SetStreamingDocker` 一个消费方（其余已在 Phase 1 Task 2/3/7 解除）；runtimeAdapter 本体由 Task 11 删除。

- [ ] **Step 4: domain 删常量**

`enums.go` 删 `RuntimeNodeStatusPending/Active/Unreachable/Disabled/Degraded`（40-45）、`validRuntimeNodeStatuses`（113-119）及 `IsRuntimeNodeStatus`（若有）、`JobTypeRuntimeNodeHealthReconcile`（83）、`JobTypeRuntimeRefreshStatus`（85）、`JobTypeAppHealthCheck`（87）。检查 `enums_test.go` 同步删用例。

- [ ] **Step 5: 编译测试 + Commit**

Run: `go build ./... && go test ./internal/... ./cmd/... 2>&1 | grep -E "FAIL" || echo OK`
```bash
git add -u && git add internal/domain/enums.go cmd/server/main.go
git commit -F - <<'EOF'
refactor(worker): 删除 docker 健康检查/状态刷新 handler 与节点 job 类型

删 AppHealthCheckHandler（docker inspect 自愈）、RuntimeRefreshStatusHandler（docker
stats）及 main 注册；domain 删 RuntimeNodeStatus* 枚举与 JobTypeRuntimeNodeHealthReconcile/
RuntimeRefreshStatus/AppHealthCheck 常量。app 状态同步已由 A2a 的 status reconciler 接管。
先于 Task 11 删除以解除 worker handler 对 integrations/runtime 的 import。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 11：删 integration/runtime 包 + node-upload 机器孤儿

> **前置**：Task 10 已删 worker handler（解除对 integrations/runtime 的 import）；Phase 1 Task 2/3/7 已解除
> channel/refresher/inspector 对 runtimeAdapter 的引用。本任务删后 integrations/agent、integrations/runtime
> 应无任何 importer。

**Files:**
- Delete: `internal/integrations/agent/`、`internal/integrations/runtime/`、`internal/runtime/imagecoord/`、`internal/runtime/agent/`、`cmd/runtime-agent/`
- Modify: `internal/worker/handlers/app_initialize.go`（删 node-upload 机器：AppInputUploader、BuildAppInputData、AssembleVersionInputData、pushVersionSkills、NewAppInputUploadAdapter、appInputUploadAdapter）
- Modify: `cmd/server/main.go` + `cmd/server/wiring.go`（runtimeAdapter、streamingResolver、nodeResolver、agentToken*、appDirInitializerAdapter）

- [ ] **Step 1: 删 node-upload 机器孤儿（app_initialize.go）**

Task 3 后 `AppInputUploader`/`BuildAppInputData`/`AssembleVersionInputData`/`pushVersionSkills`/`NewAppInputUploadAdapter`/`appInputUploadAdapter`/`AppInputVersionData`/`AppInputBuildOptions`/`versionSkillMeta` 已无消费方。
Run: `grep -rn "BuildAppInputData\|AssembleVersionInputData\|pushVersionSkills\|NewAppInputUploadAdapter\|AppInputUploader\b" internal/ cmd/ --include=*.go | grep -v app_initialize.go`
Expected: 无（确认孤儿）。删 `app_initialize.go` 中这些类型/函数（约 643-817 区段）。`SkillBlobReader` 若 `AppInitializeConfig.SkillBlobs` 仍用则**保留**（grep 确认）；不再用则一并删。

- [ ] **Step 2: 删整包**

```bash
git rm -r internal/integrations/agent internal/integrations/runtime internal/runtime/imagecoord internal/runtime/agent cmd/runtime-agent
```

- [ ] **Step 3: main/wiring 删 docker 装配**

`main.go` 删：`agentTokenResolver`/`agentTokenStore`/`agentTokenSink`（171-187）、`nodeResolver`（181）、`runtimeAdapter := runtime.NewAgentBackedAdapter(...)`（192）、`runtimeAdapter.SetStreamingDocker(...)`、`streamingResolver := newStreamingDockerResolver(...)`（198）、`appDirInitializerAdapter` 内联（A2a 已不传，确认无引用）。**保留** `imagecoordRedis`（185-190）与 `distLocker`（191）——reaper 用（reaper.New 第 3 参）。
`wiring.go` 删 `nodeClientResolver` / `newStreamingDockerResolver` / `newNodeClientResolver` 全部节点 client 路由代码；`newPersistentTokenLoader`/`persistAgentToken`（agent token）若仅 main agentToken 用则删。

- [ ] **Step 4: grep 确认 + 编译测试 + Commit**

Run: `grep -rn "AgentBackedAdapter\|newStreamingDockerResolver\|newNodeClientResolver\|imagecoord\|integrations/agent\|integrations/runtime" internal/ cmd/ --include=*.go`
Expected: 无（distLocker/imagecoordRedis 不匹配这些名）。
Run: `go build ./... && go test ./... 2>&1 | grep -E "FAIL|ok" | tail`
```bash
git add -u && git add internal/worker/handlers/app_initialize.go cmd/server/main.go cmd/server/wiring.go
git commit -F - <<'EOF'
refactor: 删除 agent/runtime docker 集成包与 node-upload 机器

删 integrations/agent、integrations/runtime（AgentBackedAdapter）、runtime/imagecoord、
runtime/agent、cmd/runtime-agent；app_initialize 删 AppInputUploader/BuildAppInputData/
AssembleVersionInputData/pushVersionSkills 等 manifest-to-node 上传机器（k8s 由 bootstrap
取代）；main/wiring 删 runtimeAdapter/streamingResolver/nodeResolver/agentToken* 装配。
保留 reaper 用的 distLocker/imagecoordRedis。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 12：删 config RuntimeConfig + EnrollmentSecret + Probe

**Files:**
- Modify: `internal/config/config.go`（Config.Runtime 30、RuntimeConfig 173、RuntimeProbeConfig）
- Modify: `internal/config/loader.go`（applyDefaults Probe 40-51、Validate validateEnrollmentSecret 134、Probe.validate 140、missing 里的 enrollment_secret）
- Modify: 本地/示例 config yaml（`deploy/manage/config/manager.example.yaml`、本地 config 的 runtime 段）
- Modify: `internal/config/loader_test.go`（去 enrollment/probe 校验用例）

- [ ] **Step 1: config.go 删结构**

`config.go` 删 `Config` 的 `Runtime RuntimeConfig`（30）字段、`RuntimeConfig`（173+）、`RuntimeProbeConfig` 结构。

- [ ] **Step 2: loader.go 删校验**

`loader.go` 删：applyDefaults 里 `c.Runtime.Probe.*` 默认值块（40-51）；Validate 里 `missing` 对 `runtime.enrollment_secret` 的检查、`validateEnrollmentSecret(c.Runtime.EnrollmentSecret)` 调用（134）、`c.Runtime.Probe.validate()` 调用（140）；`validateEnrollmentSecret` 函数与 `RuntimeProbeConfig.validate` 方法定义。

- [ ] **Step 3: 示例/本地 yaml 删 runtime 段**

删 `deploy/manage/config/manager.example.yaml` 的 `runtime:`（enrollment_secret/probe）段；本地实跑 config（如 `config/manager.yaml`，若 git 跟踪）同步删。

- [ ] **Step 4: 改测试 + 编译测试 + Commit**

`loader_test.go` 去 enrollment_secret/probe 相关用例与 fixture 字段。
Run: `grep -rn "EnrollmentSecret\|RuntimeProbeConfig\|RuntimeConfig\|\.Runtime\b" internal/ cmd/ --include=*.go | grep -v _test.go`
Expected: 无。
Run: `go build ./... && go test ./internal/config/ ./cmd/... -v`
```bash
git add -u && git add internal/config/config.go internal/config/loader.go internal/config/loader_test.go deploy/manage/config/manager.example.yaml
git commit -F - <<'EOF'
refactor(config): 删除 runtime 节点注册与探测配置

删 RuntimeConfig/RuntimeProbeConfig、Config.Runtime 字段、enrollment_secret 与 probe 的
默认值/校验、示例 yaml 的 runtime 段。runtime-agent 自注册与主动探测随节点概念退场。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 3 — 破坏性 migration + sqlc 重生

### Task 13：migration 000003 删表删列 + sqlc 重生

**Files:**
- Create: `internal/migrations/000003_drop_node_concept.up.sql` / `.down.sql`
- Delete: `internal/store/queries/runtime_nodes.sql`、`internal/store/queries/resource_samples.sql`、`internal/store/sqlc/runtime_nodes.sql.go`、`internal/store/sqlc/resource_samples.sql.go`
- Modify: `sqlc.yaml`（migration 列表加 000003，queries 列表删两文件）、`internal/store/sqlc/models.go`（regen 去 App 三字段 + 三 struct）

- [ ] **Step 1: 写 migration up**

参考 `000001_baseline.up.sql` 的 runtime_nodes（56-85）/node_resource_samples（330-347）/instance_resource_samples（350-372）建表语句与 apps 列（113/117/118）确定外键/索引名。`000003_drop_node_concept.up.sql`：
```sql
-- 删 apps 节点相关列前先删其上索引/外键（按 000001 实际索引名调整）。
-- runtime_node_id 在 A2a(000002) 已改 nullable，这里彻底删列。
ALTER TABLE apps
  DROP COLUMN runtime_node_id,
  DROP COLUMN container_id,
  DROP COLUMN container_name;

-- 资源采样表（含对 runtime_nodes 的外键）先删，再删 runtime_nodes。
DROP TABLE IF EXISTS instance_resource_samples;
DROP TABLE IF EXISTS node_resource_samples;
DROP TABLE IF EXISTS runtime_nodes;
```
> 执行前先 `grep -n "runtime_node_id\|INDEX\|FOREIGN KEY\|KEY " internal/migrations/000001_baseline.up.sql` 确认 apps 上是否有引用这三列的索引/外键，若有需在 DROP COLUMN 前 `DROP INDEX` / `DROP FOREIGN KEY`。

- [ ] **Step 2: 写 migration down（重建空 schema，数据不可恢复）**

`000003_drop_node_concept.down.sql`：把 000001 的三张表 `CREATE TABLE` 语句与 apps 三列 `ALTER TABLE apps ADD COLUMN` 原样复制回来（结构层面可逆，数据不恢复）。文件头注释标注「destructive：down 仅恢复结构，不恢复数据」。

- [ ] **Step 3: 删 query 文件 + 改 sqlc.yaml**

```bash
git rm internal/store/queries/runtime_nodes.sql internal/store/queries/resource_samples.sql \
  internal/store/sqlc/runtime_nodes.sql.go internal/store/sqlc/resource_samples.sql.go
```
`sqlc.yaml`：migration 列表加 `internal/migrations/000003_drop_node_concept.up.sql`；queries 若按文件列举则删两条（若按目录 glob 则无需改）。

- [ ] **Step 4: sqlc 重生**

Run: `make sqlc-generate`
Expected: `models.go` 的 `App` struct 去掉 `RuntimeNodeID`/`ContainerID`/`ContainerName`；`RuntimeNode`/`NodeResourceSample`/`InstanceResourceSample` struct 消失。

- [ ] **Step 5: 修编译（应已无消费方）**

Run: `go build ./... 2>&1 | head -30`
Expected: 通过（Phase 1/2 已清所有引用）。若有 `app.RuntimeNodeID`/`.ContainerID` 残留引用，逐处清理（理论上不应有）。

- [ ] **Step 6: 本地 k3d MySQL 实跑 migration up/down**

Run（用本地 k3d MySQL 连接串；参考 `make` 或 `cmd/migrate`）：
```bash
# up 到最新
go run ./cmd/migrate -database "$OCM_DB_URL" up
# 回滚一步（down 000003，验证可逆）
go run ./cmd/migrate -database "$OCM_DB_URL" down 1
# 再 up 回最新
go run ./cmd/migrate -database "$OCM_DB_URL" up
```
Expected: up/down/up 均成功，无报错。**如实记录结果**；若 cmd/migrate 接口不同，按其实际命令调整。

- [ ] **Step 7: 全量测试 + openapi-check + Commit**

Run: `go build ./... && go vet ./internal/... ./cmd/... && go test ./internal/... ./cmd/... 2>&1 | grep -E "FAIL" || echo OK`
Run: `make openapi-check`（若 DTO 变更需 `make openapi-gen` + `make web-types-gen`）
```bash
git add -u && git add internal/migrations/000003_drop_node_concept.up.sql internal/migrations/000003_drop_node_concept.down.sql sqlc.yaml internal/store/sqlc/models.go internal/store/sqlc/apps.sql.go
git commit -F - <<'EOF'
feat(db): 破坏性 migration 删除节点表与 apps 节点列

000003 DROP runtime_nodes / node_resource_samples / instance_resource_samples 三表，
DROP apps.runtime_node_id / container_id / container_name 三列；删 runtime_nodes.sql /
resource_samples.sql query 与 sqlc 生成；App struct 去三字段。down 仅恢复结构不恢复数据
（destructive）。本地 k3d MySQL 实跑 up/down/up 通过。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 4 — 前端清理

### Task 14：删前端节点页/资源图 + 改 AppRuntimeTab

**Files:**
- Delete: `web/src/pages/runtime-nodes/`、`web/src/api/hooks/useRuntimeNodes.ts`、`web/src/components/ResourceTrendChart.vue`(+__tests__)、`web/src/components/RuntimeStatusTag.vue`
- Modify: `web/src/app/router.ts`、`web/src/layouts/DashboardLayout.vue`、`web/src/pages/dashboard/RoleAwareHome.vue`、`web/src/pages/apps/AppRuntimeTab.vue`(+spec)

- [ ] **Step 1: 确认 usage 页隔离性（保留不动）**

Run: `grep -rn "useRuntimeNodes\|ResourceTrendChart\|RuntimeStatusTag\|runtime-nodes" web/src/pages/usage/ web/src/api/hooks/useUsage.ts`
Expected: 无（usage 计费页不依赖待删项，确认可独立保留）。

- [ ] **Step 2: 删前端文件**

```bash
git rm -r web/src/pages/runtime-nodes
git rm web/src/api/hooks/useRuntimeNodes.ts web/src/components/ResourceTrendChart.vue \
  web/src/components/__tests__/ResourceTrendChart.spec.ts web/src/components/RuntimeStatusTag.vue
```

- [ ] **Step 3: 删导航/路由入口**

`web/src/app/router.ts` 删 `/nodes`（runtime-nodes）路由定义与懒加载 import；`web/src/layouts/DashboardLayout.vue` 删「运行节点」菜单项；`web/src/pages/dashboard/RoleAwareHome.vue` 删节点概览卡片/统计（grep `runtime-nodes`/`useRuntimeNodes`/`节点` 定位）。

- [ ] **Step 4: 改 AppRuntimeTab.vue**

`web/src/pages/apps/AppRuntimeTab.vue`：删节点信息展示、`ResourceTrendChart` 引用与资源采样区块、`RuntimeStatusTag` 引用；**保留** 启停/重启触发按钮（`useTriggerRuntimeOperation`）与 k8s 运行状态/版本展示。`AppRuntimeTab.spec.ts` 同步去掉对已删组件的 mock 与断言。
若 `RuntimeStatusTag` 被其它页（如 RoleAwareHome）引用，一并替换为普通状态文案或删引用（grep 确认全部引用点）。

- [ ] **Step 5: 前端测试 + Commit**

Run（web 目录，包管理器见 `web/package.json` scripts，通常 pnpm）：
```bash
cd web && pnpm install --frozen-lockfile >/dev/null 2>&1; pnpm run test 2>&1 | tail -20; cd ..
```
Expected: 受影响用例已更新，全绿。
```bash
git add -u && git add web/src
git commit -F - <<'EOF'
refactor(web): 删除节点管理页与资源趋势图

删 runtime-nodes 页/详情、useRuntimeNodes hook、ResourceTrendChart、RuntimeStatusTag 及
导航/路由入口；AppRuntimeTab 去节点与资源采样展示，保留启停/重启触发与 k8s 状态。
保留 new-api 计费用量页（usage）不动。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 5 — 收尾验证

### Task 15：全量校验 + RolloutRestart k3d 集成测

**Files:** 无（校验任务）；可在 `internal/integrations/k8sorch/k3d_integration_test.go` 补 RolloutRestart 集成用例。

- [ ] **Step 1: 全量编译/vet/测试**

Run: `go build ./... && go vet ./internal/... ./cmd/... && go test ./internal/... ./cmd/... 2>&1 | grep -E "FAIL|panic" || echo "全部通过"`
Expected: 全绿。

- [ ] **Step 2: 全仓 grep 确认节点概念清零**

Run:
```bash
grep -rn "runtime_node\|RuntimeNode\|AgentBackedAdapter\|EnrollmentSecret\|node_resource_sample\|instance_resource_sample\|NodeSelector\|RuntimeRefreshStatus\|AppHealthCheck" \
  internal/ cmd/ --include=*.go | grep -v _test.go
```
Expected: 无（或仅剩有意保留的 distLocker 等非匹配项）。逐条核实无遗漏。

- [ ] **Step 3: openapi 同步**

Run: `make openapi-gen && make web-types-gen && make openapi-check`
Expected: 工作区干净（删 handler/改签名后 yaml 与 generated.ts 同步）。

- [ ] **Step 4: RolloutRestart 真实 k3d 集成测**

在 `k3d_integration_test.go` 补环境门控用例（沿用 `k3dEnv` helper）：
```go
// TestK3dRolloutRestartRecreatesPod 验证 RolloutRestart 在真实 k3d patch 注解后 Deployment 重建 pod。
func TestK3dRolloutRestartRecreatesPod(t *testing.T) {
	a, cs, ns := k3dEnv(t)
	ctx := context.Background()
	id := testAppID("it-a2b-rr-")
	t.Cleanup(func() { _ = a.Delete(context.Background(), id) })
	require.NoError(t, a.EnsureApp(ctx, k3dSpec(id)))
	require.NoError(t, a.RolloutRestart(ctx, id))
	d, err := cs.AppsV1().Deployments(ns).Get(ctx, "app-"+id, metav1.GetOptions{})
	require.NoError(t, err)
	// 注解写入触发 Deployment 重建
	assert.NotEmpty(t, d.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"])
}
```
（`k3dEnv`/`testAppID`/`k3dSpec` 沿用 Task 15-A2a 的 helper；若 helper 名不同按实际调整。）
Run（本地 k3d）：
```bash
OC_K8S_TEST_KUBECONFIG=$(k3d kubeconfig write ocm) OC_K8S_TEST_NS=oc-apps \
OC_K8S_TEST_HERMES_IMAGE=busybox:latest OC_K8S_TEST_OPS_IMAGE=busybox:latest \
go test ./internal/integrations/k8sorch/ -run 'TestK3d' -v
```
Expected: 资源/RolloutRestart 断言 PASS。**如实记录**。

- [ ] **Step 5: 工作区清洁 + Commit（如有集成测新增）**

Run: `git status --short`
Expected: 仅已提交改动 + 预存未跟踪 `docs/reports/`、`seed-e2e`（不提交）。
```bash
git add internal/integrations/k8sorch/k3d_integration_test.go
git commit -F - <<'EOF'
test(k8sorch): 补 RolloutRestart 真实 k3d 集成测

环境门控；EnsureApp 后 RolloutRestart 在真实 k3d patch restartedAt 注解触发 Deployment
重建 pod。证明渠道重载 k8s 路径可用。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## 验证范围说明（写入交付）

A2b 删除节点概念全套代码/表/前端、破坏性 migration、补齐渠道重载与 payload 残留收尾，验证为：全量
编译/vet/单测全绿 + 全仓 grep 确认节点概念清零 + migration 本地 k3d MySQL 实跑 up/down/up +
RolloutRestart 真实 k3d 集成测。**完整三角色（platform_admin/org_admin/org_member）真实浏览器走查 +
A/B/D/E 合并端到端 → spec-A2c**（迁移最终收口，吸收 A2a.4 推迟项）。

## 待 spec-A2c（本计划不做）

- 完整 A/B/D/E（S3 + bootstrap + oc-ops + k3d 部署 + 编排）合并端到端，本地 k3d 全栈跑通。
- 三角色真实浏览器走查（manager 后台 + Hermes 对话），带逐项证据。
- k8s 原生资源指标（metrics-server 采样 + 用量趋势重建）属未来独立 spec，A2c 也不做。
