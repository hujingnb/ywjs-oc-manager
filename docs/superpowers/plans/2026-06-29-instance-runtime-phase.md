# 实例运行时就绪维度(runtime_phase)Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给实例(app)新增与业务态 `app.status` 正交的运行时就绪维度 `runtime_phase`(ready/starting/restarting/unknown),让 `Ready` 信号真实反映 oc-ops sidecar 就绪,堵住「实例显示运行中但渠道发起/对话拿到 502」的空窗,并提供覆盖所有重启来源的全局可观测重启标识。

**Architecture:** 双轴模型——`app.status` 保持业务生命周期语义不动,新增 `runtime_phase` 列描述「pod 此刻能否服务」。oc-ops 容器补 readinessProbe;`orch.Status.Ready` 改为「hermes 且 oc-ops 都 Ready」;reconciler 周期写 runtime_phase(not-ready→restarting),init worker 首次拉起写 starting、就绪写 ready,解绑/重启同步置 restarting;前后端闸门改为 `allowlist(status) && runtime_phase==ready`。migration 纯增量 + 乐观回填 + reconciler 自愈实现平滑升级;业务态 `restarting` 本次保留留后清理。

**Tech Stack:** Go(stdlib + testify)、sqlc(MySQL 8.0)、k8s client-go、swag/OpenAPI、Vue 3 + TypeScript。

**Spec:** `docs/superpowers/specs/2026-06-29-instance-runtime-phase-design.md`

---

## 文件结构

| 文件 | 责任 | 动作 |
|---|---|---|
| `internal/migrations/000019_apps_runtime_phase.{up,down}.sql` | 新增 runtime_phase 列 + CHECK + 乐观回填 | Create |
| `sqlc.yaml` | schema 列表追加 000019 | Modify |
| `internal/store/queries/apps.sql` | 新增 `SetAppRuntimePhase` 查询 | Modify |
| `internal/store/sqlc/*`(生成) | App 结构体 + SetAppRuntimePhase 方法 | 生成 |
| `internal/domain/enums.go` | RuntimePhase 常量 + 校验 | Modify |
| `internal/domain/app_state_machine.go` | `AppCanInitiateChannelAuth` 双维度 | Modify |
| `internal/integrations/k8sorch/adapter.go` | `Status.Ready` = hermes && oc-ops | Modify |
| `internal/integrations/k8sorch/render.go` | oc-ops 容器加 readinessProbe | Modify |
| `internal/integrations/k8sorch/testdata/deployment.golden.yaml` | golden 同步 probe | Modify |
| `internal/service/app_status_reconciler.go` | Tick 写 runtime_phase + `runtimePhaseFor` | Modify |
| `internal/worker/handlers/app_initialize.go` | 首启写 starting、就绪写 ready | Modify |
| `internal/service/channel_service.go` | 解绑置 runtime_phase=restarting(替代业务态 restarting) | Modify |
| `internal/service/app_service.go` | `AppResult.RuntimePhase` + 映射 | Modify |
| `openapi/openapi.yaml`、`web/src/api/generated.ts` | 重新生成 | 生成 |
| `web/src/pages/apps/AppChannelsTab.vue` | instanceReady 双维度 + phase 文案 | Modify |
| `web/src/i18n/locales/{zh,en}/apps/root.ts` | starting/restarting/unknown 文案 | Modify |

---

## Task 1: migration 000019 — 新增 runtime_phase 列(纯增量 + 乐观回填)

**Files:**
- Create: `internal/migrations/000019_apps_runtime_phase.up.sql`
- Create: `internal/migrations/000019_apps_runtime_phase.down.sql`
- Modify: `sqlc.yaml`(schema 列表)

- [ ] **Step 1: 写 up migration**

写 `internal/migrations/000019_apps_runtime_phase.up.sql`:

```sql
-- 实例运行时就绪维度 runtime_phase：与业务态 status 正交，描述 pod 此刻能否服务。
-- ready=所有关键容器就绪可服务；starting=首次拉起中未就绪；restarting=重启窗口
-- (解绑/升级/k8s 自发)oc-ops 暂不可用；unknown=未探明。
-- 纯增量：不动 apps_status_check，滚动部署期新旧 manager 代码并存安全；
-- DEFAULT 'unknown' 让旧二进制 INSERT(不带本列)与新建 draft 实例都拿到合理初值。
ALTER TABLE apps
    ADD COLUMN runtime_phase VARCHAR(20) NOT NULL DEFAULT 'unknown'
        COMMENT '运行时就绪维度(与status正交):ready/starting/restarting/unknown',
    ADD CONSTRAINT apps_runtime_phase_check
        CHECK (runtime_phase IN ('ready','starting','restarting','unknown'));

-- 存量行乐观回填：running→ready(避免升级后所有运行实例被闸门拦~15s)，
-- restarting→restarting(存量解绑重启窗)，其余→unknown；reconciler 下一个 tick(~15s)
-- 用真实 pod 探测自愈纠正任何回填偏差。
UPDATE apps SET runtime_phase = CASE status
    WHEN 'running'    THEN 'ready'
    WHEN 'restarting' THEN 'restarting'
    ELSE 'unknown'
END;
```

- [ ] **Step 2: 写 down migration**

写 `internal/migrations/000019_apps_runtime_phase.down.sql`:

```sql
-- 回滚：仅 DROP 本列与约束；apps_status_check 从未改动，回滚后旧代码完全不受影响。
ALTER TABLE apps
    DROP CONSTRAINT apps_runtime_phase_check,
    DROP COLUMN runtime_phase;
```

- [ ] **Step 3: sqlc.yaml 追加 000019 到 schema 列表**

在 `sqlc.yaml` 的 `schema:` 列表末尾(`000018_conversation_files.up.sql` 之后)追加一行:

```yaml
      - internal/migrations/000019_apps_runtime_phase.up.sql
```

> 关键:sqlc 用 schema 列表里的 migration 推导列类型,漏加这行 → 生成的 `App` 结构体没有 `RuntimePhase` 字段,后续任务全部编译失败。

- [ ] **Step 4: 跑 migration 测试验证编号连续与可应用**

Run: `go test ./internal/migrations/ -run TestMigrations -v`
Expected: PASS(migrations_test.go 校验 up/down 配对与编号连续)

- [ ] **Step 5: Commit**

```bash
git add internal/migrations/000019_apps_runtime_phase.up.sql internal/migrations/000019_apps_runtime_phase.down.sql sqlc.yaml
git commit -m "feat(apps): 新增 runtime_phase 列(运行时就绪维度,纯增量+乐观回填)"
```

---

## Task 2: sqlc — SetAppRuntimePhase 查询 + 重新生成

**Files:**
- Modify: `internal/store/queries/apps.sql`
- 生成: `internal/store/sqlc/apps.sql.go`、`querier.go`、`models.go`

- [ ] **Step 1: 在 apps.sql 追加 SetAppRuntimePhase 查询**

在 `internal/store/queries/apps.sql` 的 `SetAppRuntimeSnapshot` 块之后追加:

```sql
-- name: SetAppRuntimePhase :exec
-- 裸 UPDATE runtime_phase(运行时就绪维度,与 status 正交,无状态机守卫,守卫不适用):
-- reconciler 周期写、init worker 首启/就绪写、渠道解绑/升级重启前置 restarting 用。
UPDATE apps
SET runtime_phase = ?, updated_at = now()
WHERE id = ?;
```

- [ ] **Step 2: 跑 sqlc 生成**

Run: `make sqlc-generate`
Expected: 无报错;`git status` 显示 `internal/store/sqlc/` 下 `apps.sql.go`/`querier.go`/`models.go` 变更——`models.go` 的 `App` 结构体新增 `RuntimePhase string`,新增 `SetAppRuntimePhase` 方法与 `SetAppRuntimePhaseParams`。

- [ ] **Step 3: 验证生成内容**

Run: `grep -n "RuntimePhase" internal/store/sqlc/models.go internal/store/sqlc/apps.sql.go`
Expected: `models.go` 含 `RuntimePhase string`;`apps.sql.go` 含 `func (q *Queries) SetAppRuntimePhase` 与 `type SetAppRuntimePhaseParams`。

- [ ] **Step 4: 确认整体编译通过**

Run: `go build ./...`
Expected: 通过(新字段/方法暂无调用方,不破坏现有代码)。

- [ ] **Step 5: Commit**

```bash
git add internal/store/queries/apps.sql internal/store/sqlc/
git commit -m "feat(apps): 生成 SetAppRuntimePhase 查询与 App.RuntimePhase 字段"
```

---

## Task 3: domain — RuntimePhase 枚举常量与校验

**Files:**
- Modify: `internal/domain/enums.go`
- Test: `internal/domain/enums_test.go`(若不存在则新建)

- [ ] **Step 1: 写失败测试**

在 `internal/domain/enums_test.go` 追加(无文件则新建,包名 `domain`,import `testing` 与 `github.com/stretchr/testify/assert`):

```go
// TestIsRuntimePhase 验证运行时就绪维度取值校验：4 个合法值通过、非法值与空串拒绝。
func TestIsRuntimePhase(t *testing.T) {
	// 合法取值：ready/starting/restarting/unknown 全部应通过。
	for _, v := range []string{RuntimePhaseReady, RuntimePhaseStarting, RuntimePhaseRestarting, RuntimePhaseUnknown} {
		assert.True(t, IsRuntimePhase(v), "合法 runtime_phase 应通过: %s", v)
	}
	// 非法取值：业务态字符串与空串都不是合法 runtime_phase。
	for _, v := range []string{"running", "", "bad"} {
		assert.False(t, IsRuntimePhase(v), "非法 runtime_phase 应拒绝: %q", v)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/domain/ -run TestIsRuntimePhase -v`
Expected: FAIL / 编译错误(`RuntimePhase*`、`IsRuntimePhase` 未定义)

- [ ] **Step 3: 加常量、校验集合与函数**

在 `internal/domain/enums.go` 的 `AppStatusRestarting = "restarting"`(第 36 行)之后、`APIKeyStatus*` 之前追加:

```go
	// RuntimePhase* 描述实例运行时就绪维度，与业务态 AppStatus* 正交：
	// AppStatus 管业务生命周期(draft→...→running/stopped/error)，RuntimePhase 管 pod
	// 此刻能否服务。渠道发起闸门 = AppCanInitiateChannelAuth(status, runtime_phase)，两维
	// 皆满足才放行。坏态归业务态 error(需人工/重试)，瞬态未就绪归 runtime_phase(只需稍候)。
	RuntimePhaseReady      = "ready"      // pod 所有关键容器(hermes+oc-ops)Ready，可服务(稳态)
	RuntimePhaseStarting   = "starting"   // 首次拉起中，pod 未就绪，k8s 预期自愈(init worker 写)
	RuntimePhaseRestarting = "restarting" // 重启窗口(解绑/升级/k8s 自发)，oc-ops 暂不可用
	RuntimePhaseUnknown    = "unknown"    // 未探明(查询失败 / reconciler 未跑 / 新建未初始化)
```

在 `validChannelStatuses` 块之后追加校验集合:

```go
	validRuntimePhases = set(
		RuntimePhaseReady,
		RuntimePhaseStarting,
		RuntimePhaseRestarting,
		RuntimePhaseUnknown,
	)
```

在 `IsChannelStatus` 函数之后追加:

```go
// IsRuntimePhase 校验运行时就绪维度取值是否合法。
func IsRuntimePhase(value string) bool {
	_, ok := validRuntimePhases[value]
	return ok
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/domain/ -run TestIsRuntimePhase -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/domain/enums.go internal/domain/enums_test.go
git commit -m "feat(domain): 新增 runtime_phase 枚举常量与校验"
```

---

## Task 4: domain — AppCanInitiateChannelAuth 改双维度 + 同步调用点

**Files:**
- Modify: `internal/domain/app_state_machine.go:122-143`
- Modify: `internal/domain/app_state_machine_test.go:164-188`
- Modify: `internal/service/channel_service.go`(3 处调用:135、232、329)

- [ ] **Step 1: 改测试为双维度 truth table**

把 `internal/domain/app_state_machine_test.go` 的 `TestAppCanInitiateChannelAuth` 整体替换为(保留原中文注释风格):

```go
// TestAppCanInitiateChannelAuth 验证渠道发起就绪守卫的双维度逻辑：
// 仅当业务态在 allowlist(running/binding_waiting/binding_failed) 且 runtime_phase==ready
// 时才放行；任一维度不满足都拒绝。
func TestAppCanInitiateChannelAuth(t *testing.T) {
	cases := []struct {
		status       string // 业务态
		runtimePhase string // 运行时态
		want         bool
		reason       string
	}{
		{AppStatusRunning, RuntimePhaseReady, true, "running+ready：实例就绪，放行"},
		{AppStatusBindingWaiting, RuntimePhaseReady, true, "binding_waiting+ready：首次绑定合法发生在此态"},
		{AppStatusBindingFailed, RuntimePhaseReady, true, "binding_failed+ready：pod 在跑，允许重试"},
		{AppStatusRunning, RuntimePhaseRestarting, false, "running 但 pod 重启中：oc-ops 不可用，拦"},
		{AppStatusRunning, RuntimePhaseStarting, false, "running 但 pod 拉起中：未就绪，拦"},
		{AppStatusRunning, RuntimePhaseUnknown, false, "running 但就绪度未探明：保守不放行"},
		{AppStatusStopped, RuntimePhaseReady, false, "stopped：业务态不允许发起"},
		{AppStatusError, RuntimePhaseReady, false, "error：业务态不允许发起"},
		{AppStatusPullingRuntimeImage, RuntimePhaseUnknown, false, "初始化中：两维都不满足"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, AppCanInitiateChannelAuth(c.status, c.runtimePhase), c.reason)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/domain/ -run TestAppCanInitiateChannelAuth -v`
Expected: FAIL / 编译错误(参数个数不匹配)

- [ ] **Step 3: 改 AppCanInitiateChannelAuth 签名与实现**

把 `internal/domain/app_state_machine.go` 的函数(122-143 行)改为:

```go
// AppCanInitiateChannelAuth 判断 app 当前是否允许发起渠道登录授权（BeginAuth / BeginFeishuAuth）。
//
// 双维度守卫：业务态在 allowlist 且运行时态为 ready 才放行——否则发起会打到不可达 / 未就绪的
// oc-ops 拿到 502，前端透出 cryptic「ocops: hermes cli failed」像 bug。
//   - status 维度：running（就绪，重绑/新增渠道）、binding_waiting（首次 onboarding 等扫码，
//     微信首绑发起即在此态）、binding_failed（上轮超时，pod 仍在，允许重试）。
//   - runtime_phase 维度：必须 == ready（pod 所有关键容器含 oc-ops 都 Ready）。restarting
//     （解绑/升级/k8s 自发重启窗口）、starting（首次拉起中）、unknown（未探明）一律拦截。
//
// 业务态 allowlist 比「status==running」宽：严格 running-only 会误杀 binding_waiting 首绑与
// binding_failed 重试（二者 pod 均在服务），故按「pod 是否在服务」建模。
func AppCanInitiateChannelAuth(status, runtimePhase string) bool {
	if runtimePhase != RuntimePhaseReady {
		return false
	}
	switch status {
	case AppStatusRunning, AppStatusBindingWaiting, AppStatusBindingFailed:
		return true
	default:
		return false
	}
}
```

- [ ] **Step 4: 同步 3 处 service 调用点**

`internal/service/channel_service.go` 的 135、232、329 行,把 `domain.AppCanInitiateChannelAuth(app.Status)` 改为 `domain.AppCanInitiateChannelAuth(app.Status, app.RuntimePhase)`(`app` 为 `sqlc.App`,Task 2 后已含 `RuntimePhase` 字段)。三处一致:

```go
	if !domain.AppCanInitiateChannelAuth(app.Status, app.RuntimePhase) {
		return ChallengeResult{}, ErrInstanceNotReady
	}
```

- [ ] **Step 5: 跑 domain + service 测试确认通过、整体编译**

Run: `go test ./internal/domain/ -run TestAppCanInitiateChannelAuth -v && go build ./...`
Expected: domain 测试 PASS;`go build ./...` 通过。

- [ ] **Step 6: Commit**

```bash
git add internal/domain/app_state_machine.go internal/domain/app_state_machine_test.go internal/service/channel_service.go
git commit -m "feat(domain): 渠道发起守卫改双维度(status allowlist + runtime_phase==ready)"
```

---

## Task 5: k8sorch — Status.Ready 改为 hermes 且 oc-ops 都就绪

**Files:**
- Modify: `internal/integrations/k8sorch/adapter.go:210-219`
- Test: `internal/integrations/k8sorch/adapter_test.go`

- [ ] **Step 1: 写失败测试(oc-ops 未就绪时 Ready=false)**

在 `internal/integrations/k8sorch/adapter_test.go` 追加(沿用文件现有 fake client / Status 调用风格,参考已有 `ContainerStatuses` 用例 line 104-128):

```go
// TestStatus_RequiresBothHermesAndOcops 验证：pod 整体就绪需 hermes 与 oc-ops 容器都 Ready。
// 渠道登录/对话实际打 oc-ops sidecar，仅 hermes Ready 不代表服务可用。
func TestStatus_RequiresBothHermesAndOcops(t *testing.T) {
	// hermes Ready 但 oc-ops 未 Ready：整体 Ready 应为 false（旧逻辑会误报 true）。
	st := statusFromPod(t, corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "oc-apps", Labels: map[string]string{"app": "a1"}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
			{Name: "hermes", Ready: true, Image: "registry/hermes:v1"},
			{Name: "oc-ops", Ready: false},
		}},
	})
	assert.False(t, st.Ready, "oc-ops 未就绪时整体不应 Ready")

	// hermes 与 oc-ops 都 Ready：整体 Ready=true。
	st2 := statusFromPod(t, corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "oc-apps", Labels: map[string]string{"app": "a1"}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
			{Name: "hermes", Ready: true, Image: "registry/hermes:v1"},
			{Name: "oc-ops", Ready: true},
		}},
	})
	assert.True(t, st2.Ready, "两容器都 Ready 时整体应 Ready")
}
```

> 注:`statusFromPod` 是测试 helper——若文件已有等价构造(查看 line 97-128 现有用例如何造 fake client 调 `Status`),复用该方式而非新建 helper;若无,按现有用例同样用 `fake.NewSimpleClientset(&pod)` 构造 `KubernetesAdapter` 调 `Status`。同时**更新现有用例**:line 104-111 的「Ready=true」用例需补一个 `{Name: "oc-ops", Ready: true}` 容器,否则新逻辑下会变 false 而回归失败。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/integrations/k8sorch/ -run TestStatus_RequiresBothHermesAndOcops -v`
Expected: FAIL(当前只看 hermes,oc-ops 未 Ready 仍报 Ready=true)

- [ ] **Step 3: 改 Status 的 Ready 计算**

把 `internal/integrations/k8sorch/adapter.go` 的容器遍历(210-219 行)改为:对 hermes 取 RestartCount/ImageRef/Message,Ready 取「hermes 且 oc-ops 都 Ready」:

```go
	// pod 整体就绪需关键业务容器都 Ready：hermes(引擎)与 oc-ops(渠道登录/对话 API sidecar)。
	// 渠道登录实际打 oc-ops，仅 hermes Ready 会漏判 oc-ops 未起的 502 空窗。s3-sync 是数据
	// 持久化 sidecar、不在请求路径，不纳入就绪判定。RestartCount/ImageRef/Message 仍取 hermes
	// （主容器，IsTerminalBad 的重启阈值/镜像溯源沿用 hermes 口径）。
	var hermesReady, ocopsReady bool
	for _, cs := range p.Status.ContainerStatuses {
		switch cs.Name {
		case "hermes":
			hermesReady = cs.Ready
			st.RestartCount = cs.RestartCount
			st.ImageRef = cs.Image
			if cs.State.Waiting != nil {
				st.Message = cs.State.Waiting.Reason
			}
		case "oc-ops":
			ocopsReady = cs.Ready
		}
	}
	st.Ready = hermesReady && ocopsReady
```

> `AppStatus.Ready` 的字段注释(`adapter.go:86-87` 与 `orchestrator.go:86-87`「Ready 表示 hermes 容器是否 Ready」)同步改为「hermes 且 oc-ops 容器都 Ready」。

- [ ] **Step 4: 跑 k8sorch 全部测试确认通过**

Run: `go test ./internal/integrations/k8sorch/ -v`
Expected: PASS(新用例通过,补了 oc-ops 的旧用例也通过)

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/k8sorch/adapter.go internal/integrations/k8sorch/orchestrator.go internal/integrations/k8sorch/adapter_test.go
git commit -m "fix(k8sorch): pod 就绪判定纳入 oc-ops sidecar(堵 oc-ops 未起的 502 空窗)"
```

---

## Task 6: k8sorch — oc-ops 容器加 readinessProbe

**Files:**
- Modify: `internal/integrations/k8sorch/render.go`(oc-ops 容器,169-171 行附近)
- Modify: `internal/integrations/k8sorch/testdata/deployment.golden.yaml`

- [ ] **Step 1: 给 oc-ops 容器加 readinessProbe**

在 `internal/integrations/k8sorch/render.go` 的 oc-ops 容器定义里(`Ports: []corev1.ContainerPort{{ContainerPort: 8080}}` 之后、`VolumeMounts` 之前,170 行附近)加:

```go
							// readinessProbe：TCP 探 8080，uvicorn 接受连接即视 oc-ops 服务就绪。
							// 用 TCPSocket 而非 HTTP /health/detailed——后者会转发 hermes 平台健康检查，
							// 把 oc-ops 就绪耦合到平台连通(某平台 fatal 会让 oc-ops 永不 Ready)，过严。
							// TCP 仅表「uvicorn 在 listen」，是「oc-ops API 可达」的解耦最小信号。
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(8080)},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       10,
								FailureThreshold:    6,
							},
```

> 确认 `render.go` 已 import `"k8s.io/apimachinery/pkg/util/intstr"`;未引入则加(hermes 容器的 probe 用 Exec 未用 intstr,大概率需新增此 import)。

- [ ] **Step 2: 跑 render 测试看 golden 差异**

Run: `go test ./internal/integrations/k8sorch/ -run TestRender -v`
Expected: FAIL,diff 显示 oc-ops 容器多出 readinessProbe(golden 未含)

- [ ] **Step 3: 更新 golden 文件**

按测试报告的 diff,在 `internal/integrations/k8sorch/testdata/deployment.golden.yaml` 的 oc-ops 容器节点补对应 `readinessProbe`(tcpSocket port 8080 / initialDelaySeconds 10 / periodSeconds 10 / failureThreshold 6),缩进对齐同文件 hermes 容器的 readinessProbe 块。

> 若 render 测试支持 `-update` 之类的 golden 重写开关,优先用它重生成;否则手工对齐。重生成后**人工 review** golden diff 仅含 oc-ops 的 readinessProbe,无其它意外变更。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/integrations/k8sorch/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/k8sorch/render.go internal/integrations/k8sorch/testdata/deployment.golden.yaml
git commit -m "feat(k8sorch): oc-ops sidecar 加 readinessProbe(TCP 8080)"
```

---

## Task 7: reconciler — Tick 周期写 runtime_phase

**Files:**
- Modify: `internal/service/app_status_reconciler.go`(interface、Tick、新增 `runtimePhaseFor`)
- Test: `internal/service/app_status_reconciler_test.go`

- [ ] **Step 1: 写失败测试(runtimePhaseFor 映射)**

在 `internal/service/app_status_reconciler_test.go` 追加:

```go
// TestRuntimePhaseFor 验证 pod 观测态 → runtime_phase 的映射(仅对已过初始化、期望在服务的 app)：
// Ready→ready；确定坏死→unknown(业务态另推 error)；其余未就绪→restarting(发生重启/未就绪)。
func TestRuntimePhaseFor(t *testing.T) {
	// pod 两容器都 Ready：可服务。
	assert.Equal(t, domain.RuntimePhaseReady, runtimePhaseFor(k8sorch.AppStatus{Ready: true}))
	// CrashLoopBackOff：确定性坏死，runtime_phase=unknown(业务态由 Tick 推 error)。
	assert.Equal(t, domain.RuntimePhaseUnknown, runtimePhaseFor(k8sorch.AppStatus{Phase: "Running", Message: "CrashLoopBackOff"}))
	// Deployment 在但无 pod(Recreate 空窗)：未就绪非坏死 → restarting。
	assert.Equal(t, domain.RuntimePhaseRestarting, runtimePhaseFor(k8sorch.AppStatus{Phase: "Pending"}))
	// pod Running 但未 Ready(新 pod 起来中)：未就绪非坏死 → restarting。
	assert.Equal(t, domain.RuntimePhaseRestarting, runtimePhaseFor(k8sorch.AppStatus{Phase: "Running", Ready: false}))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run TestRuntimePhaseFor -v`
Expected: FAIL(`runtimePhaseFor` 未定义)

- [ ] **Step 3: 加 runtimePhaseFor + 接口方法 + Tick 写入**

在 `internal/service/app_status_reconciler.go` 的 `appStatusStore` 接口加方法(`SetAppStatus` 之后):

```go
	// SetAppRuntimePhase 裸 UPDATE runtime_phase(运行时就绪维度,与 status 正交)。
	SetAppRuntimePhase(ctx context.Context, arg sqlc.SetAppRuntimePhaseParams) error
```

在 `podIsBad` 函数之后加:

```go
// runtimePhaseFor 把 pod 观测态映射到 runtime_phase(运行时就绪维度)。仅对已过初始化、期望
// 在服务的 app(running/binding_waiting)调用——这类 app 之前已 Ready,故「未就绪且非坏死」
// 一律视为发生了重启/正在恢复(restarting),给出清晰的全局重启标识。starting 是首次冷启动语义,
// 由 init worker 在 phaseStart 写,reconciler 不产出。
//   - Ready                  → ready(可服务)
//   - 确定性坏死(IsTerminalBad)→ unknown(不可服务;业务态由 Tick 的 running→error 守卫另推 error)
//   - 其余(Pending 重建空窗 / Running 未 Ready) → restarting
func runtimePhaseFor(st k8sorch.AppStatus) string {
	switch {
	case st.Ready:
		return domain.RuntimePhaseReady
	case k8sorch.IsTerminalBad(st):
		return domain.RuntimePhaseUnknown
	default:
		return domain.RuntimePhaseRestarting
	}
}
```

在 `Tick` 的主循环里,把 runtime_phase 写在「写快照」之后、「podIsBad 业务态守卫」之前(94-105 行之间),即每轮都刷新运行时态:

```go
		// 刷新运行时就绪维度(与 status 正交):pod 真就绪→ready,Recreate 空窗/未就绪→restarting,
		// 坏死→unknown。写失败静默忽略,下一轮重试(与快照同口径,不阻塞业务态守卫)。
		_ = r.store.SetAppRuntimePhase(ctx, sqlc.SetAppRuntimePhaseParams{
			RuntimePhase: runtimePhaseFor(st),
			ID:           appID,
		})
```

> `convergeRestarting`/`recoverReadyButError` 保持不动:前者是**业务态 `restarting` 的过渡期排水逻辑**(滚动部署期旧代码/存量行仍可能写业务态 restarting),留后清理;后者管 error 自愈,与 runtime_phase 无关。

- [ ] **Step 4: 给测试 stub 加 SetAppRuntimePhase 空实现**

`app_status_reconciler_test.go` 里实现 `appStatusStore` 的 fake/stub 结构体需加方法(记录调用或 no-op):

```go
// SetAppRuntimePhase 记录最近一次写入的 runtime_phase,供断言 reconciler 写对了值。
func (s *fakeAppStatusStore) SetAppRuntimePhase(_ context.Context, arg sqlc.SetAppRuntimePhaseParams) error {
	s.lastRuntimePhase[arg.ID] = arg.RuntimePhase
	return nil
}
```

> 按 stub 现有字段命名风格调整;若 stub 用 map 记录其它 Set* 调用,照搬该模式。`lastRuntimePhase` 字段在 stub 结构体声明并在构造处 `make(map[string]string)`。

- [ ] **Step 5: 跑 service 测试确认通过**

Run: `go test ./internal/service/ -run 'TestRuntimePhaseFor|TestAppStatusReconciler|Tick' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/service/app_status_reconciler.go internal/service/app_status_reconciler_test.go
git commit -m "feat(reconciler): Tick 周期写 runtime_phase(就绪→ready/未就绪→restarting/坏死→unknown)"
```

---

## Task 8: init worker — 首启写 starting、就绪写 ready(快路径)

**Files:**
- Modify: `internal/worker/handlers/app_initialize.go`(interface、phaseStart、binding_waiting 转移后)
- Test: `internal/worker/handlers/app_initialize_test.go`

- [ ] **Step 1: 写失败测试(就绪后写 ready)**

在 `internal/worker/handlers/app_initialize_test.go` 追加(沿用文件现有 handler 装配 + fake store 风格;若现有测试已构造完整初始化成功路径,在其断言里追加对 runtime_phase 的检查):

```go
// TestInitialize_WritesRuntimePhaseReady 验证初始化成功(pod Ready、进 binding_waiting)后
// runtime_phase 被写成 ready，使前端发起闸门(status+runtime_phase 双维度)放行首次绑定。
func TestInitialize_WritesRuntimePhaseReady(t *testing.T) {
	// 装配:orch.WaitReady 立即成功、无已绑定渠道 → 终态 binding_waiting。
	// (按文件现有 newTestInitHandler / fakeStore 装配方式构造)
	h, store := newTestInitHandler(t)
	err := h.Handle(context.Background(), initJobFor(t, "app-1"))
	require.NoError(t, err)
	// 就绪写 ready：与业务态 binding_waiting 配合放行首绑。
	assert.Equal(t, domain.RuntimePhaseReady, store.runtimePhase["app-1"])
}
```

> 上述 helper 名(`newTestInitHandler`/`initJobFor`/`store.runtimePhase`)按文件实际命名替换;关键断言是「初始化成功后 fake store 记录的 runtime_phase == ready」。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/worker/handlers/ -run TestInitialize_WritesRuntimePhaseReady -v`
Expected: FAIL(尚未写 runtime_phase)

- [ ] **Step 3: 接口加方法 + phaseStart 写 starting + binding_waiting 后写 ready**

`app_initialize.go` handler 的 store 接口(32 行 `SetAppStatus` 附近)加:

```go
	// SetAppRuntimePhase 写运行时就绪维度:phaseStart 入口写 starting,pod 就绪进 binding_waiting 后写 ready。
	SetAppRuntimePhase(ctx context.Context, arg sqlc.SetAppRuntimePhaseParams) error
```

在 `phaseStart`(377 行)的 `WaitReady` 调用前,标记首次拉起中:

```go
	// 首次拉起:标 runtime_phase=starting(pod 调度/拉镜像/初始化中,未就绪)。业务态此时是 starting,
	// 发起闸门本就关闭,此写主要供观测;写失败不阻断等待主流程。
	_ = h.store.SetAppRuntimePhase(ctx, sqlc.SetAppRuntimePhaseParams{
		RuntimePhase: domain.RuntimePhaseStarting,
		ID:           app.ID,
	})
```

在主流程 `transitionTo(binding_waiting)` 成功之后(312-314 行之后、`SetAppAppliedVersion` 之前)写 ready:

```go
	// 快路径:pod 已 Ready(WaitReady 通过)且进入 binding_waiting,立即写 runtime_phase=ready,
	// 不必等 reconciler 下一个 tick(~15s)。使「binding_waiting + ready」双维度即刻放行首次绑定。
	// 写失败不阻断:reconciler 稳态路径会在 ~15s 内补写。
	_ = h.store.SetAppRuntimePhase(ctx, sqlc.SetAppRuntimePhaseParams{
		RuntimePhase: domain.RuntimePhaseReady,
		ID:           app.ID,
	})
```

- [ ] **Step 4: 测试 stub 加 SetAppRuntimePhase**

在 `app_initialize_test.go` 的 fake store 加方法记录 runtime_phase(同 Task 7 stub 风格):

```go
func (s *fakeInitStore) SetAppRuntimePhase(_ context.Context, arg sqlc.SetAppRuntimePhaseParams) error {
	s.runtimePhase[arg.ID] = arg.RuntimePhase
	return nil
}
```

(`runtimePhase map[string]string` 在 stub 结构体声明、构造处 `make`。)

- [ ] **Step 5: 跑 worker 测试确认通过**

Run: `go test ./internal/worker/handlers/ -run TestInitialize -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/worker/handlers/app_initialize.go internal/worker/handlers/app_initialize_test.go
git commit -m "feat(worker): 初始化首启写 runtime_phase=starting、就绪写 ready(快路径)"
```

---

## Task 9: channel_service — 解绑置 runtime_phase=restarting(替代业务态 restarting)

**Files:**
- Modify: `internal/service/channel_service.go`(interface、两处解绑 387-393 与 503-509)
- Test: `internal/service/channel_service_test.go`

- [ ] **Step 1: 写失败测试(解绑置 runtime_phase=restarting,业务态不变)**

在 `internal/service/channel_service_test.go` 追加(按文件现有解绑测试装配风格):

```go
// TestUnbind_SetsRuntimePhaseRestarting 验证解绑触发 RolloutRestart 前置 runtime_phase=restarting，
// 且业务态 status 保持不动(双轴模型:运行时态归 runtime_phase,业务态不再写 restarting)。
func TestUnbind_SetsRuntimePhaseRestarting(t *testing.T) {
	svc, store := newTestChannelService(t) // 按现有 helper 装配
	// 前置:一个 running 且已 bound 渠道的 app。
	store.seedApp("app-1", domain.AppStatusRunning, domain.RuntimePhaseReady)
	store.seedBinding("app-1", domain.ChannelTypeFeishu, domain.ChannelStatusBound)

	_, err := svc.Unbind(/* 按现有签名传 principal/appID/channelType */)
	require.NoError(t, err)

	// runtime_phase 置 restarting:标记重启窗口、闸门关。
	assert.Equal(t, domain.RuntimePhaseRestarting, store.runtimePhase["app-1"])
	// 业务态 status 不动:仍 running(不再写业务态 restarting)。
	assert.Equal(t, domain.AppStatusRunning, store.appStatus["app-1"])
}
```

> 按文件实际 helper / stub 字段名替换;关键断言两条:runtime_phase==restarting、status 仍 running。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run TestUnbind_SetsRuntimePhaseRestarting -v`
Expected: FAIL(当前置的是业务态 restarting)

- [ ] **Step 3: 接口加方法 + 两处解绑改为置 runtime_phase**

`channel_service.go` 的 store 接口(49 行 `SetAppStatus` 附近)加:

```go
	// SetAppRuntimePhase 写运行时就绪维度;解绑 RolloutRestart 前置 restarting(业务态 status 不动)。
	SetAppRuntimePhase(ctx context.Context, arg sqlc.SetAppRuntimePhaseParams) error
```

把**两处**置 restarting 的块(387-393 与 503-509,内容一致)各自替换为:

```go
		// 解绑触发 RolloutRestart 重建 pod(Recreate,~20s 停机),期间 oc-ops 不可用。
		// 双轴模型:置 runtime_phase=restarting 标记运行时不就绪(发起闸门据此关闭),业务态 status
		// 保持不动;reconciler 在 pod 重新 Ready 后写回 ready。置位失败只记日志、不阻断解绑——
		// channel_binding=unbound_by_user 才是 source of truth。
		if err := s.store.SetAppRuntimePhase(ctx, sqlc.SetAppRuntimePhaseParams{
			RuntimePhase: domain.RuntimePhaseRestarting,
			ID:           app.ID,
		}); err != nil {
			slog.ErrorContext(ctx, "解绑置 runtime_phase=restarting 失败", "app_id", app.ID, redactlog.Err(err))
		}
```

> 移除原 `domain.EnsureAppTransition(app.Status, domain.AppStatusRestarting)` 判断与 `SetAppStatus(restarting)` 调用。`SetAppStatus` 接口方法本任务后在 channel_service 内可能不再被引用——保留接口声明(stub 已实现,移除会牵连),不强制删;若 `go vet`/编译提示 `SetAppStatus` 在本文件已无调用,这是接口方法不影响编译,无需处理。

- [ ] **Step 4: 测试 stub 加 SetAppRuntimePhase**

`channel_service_test.go` 的 store stub 加方法记录(同前):

```go
func (s *fakeChannelStore) SetAppRuntimePhase(_ context.Context, arg sqlc.SetAppRuntimePhaseParams) error {
	s.runtimePhase[arg.ID] = arg.RuntimePhase
	return nil
}
```

- [ ] **Step 5: 跑 service 测试确认通过、整体编译**

Run: `go test ./internal/service/ -run 'TestUnbind' -v && go build ./...`
Expected: PASS;编译通过。

- [ ] **Step 6: Commit**

```bash
git add internal/service/channel_service.go internal/service/channel_service_test.go
git commit -m "feat(channels): 解绑置 runtime_phase=restarting 替代业务态 restarting(双轴模型)"
```

---

## Task 10: API — AppResult 暴露 runtime_phase + OpenAPI 同步

**Files:**
- Modify: `internal/service/app_service.go`(AppResult 结构体 + toAppResult)
- 生成: `openapi/openapi.yaml`、`web/src/api/generated.ts`

- [ ] **Step 1: AppResult 加 RuntimePhase 字段**

在 `internal/service/app_service.go` 的 `AppResult` 结构体(92-128 行)的 `Status` 字段之后加:

```go
	// RuntimePhase 是运行时就绪维度(与 status 正交):ready/starting/restarting/unknown。
	// 前端发起闸门 = status allowlist 且 runtime_phase==ready;非 ready 时按 phase 展示
	// 正在启动 / 重启中 / 状态确认中。
	RuntimePhase string `json:"runtime_phase"`
```

- [ ] **Step 2: toAppResult 映射**

在 `internal/service/app_service.go` 的 `toAppResult`(187 行起)的 `Status: app.Status,` 之后加:

```go
		RuntimePhase: app.RuntimePhase,
```

- [ ] **Step 3: 重新生成 OpenAPI 与前端类型**

Run: `make openapi-gen && make web-types-gen`
Expected: 无报错;`openapi/openapi.yaml` 的 AppResult schema 新增 `runtime_phase`,`web/src/api/generated.ts` 对应类型新增该字段。

- [ ] **Step 4: 校验生成同步**

Run: `make openapi-check`
Expected: PASS(跑完 openapi-gen 后 git 工作区对 yaml 干净)

- [ ] **Step 5: 后端编译 + 测试**

Run: `go build ./... && go test ./internal/service/ -run TestApp -v`
Expected: 通过

- [ ] **Step 6: Commit**

```bash
git add internal/service/app_service.go openapi/openapi.yaml web/src/api/generated.ts
git commit -m "feat(apps): AppResult 暴露 runtime_phase 并同步 OpenAPI/前端类型"
```

---

## Task 11: 前端 — instanceReady 双维度 + phase 化提示文案

**Files:**
- Modify: `web/src/pages/apps/AppChannelsTab.vue`(415-425 行)
- Modify: `web/src/i18n/locales/zh/apps/root.ts`、`web/src/i18n/locales/en/apps/root.ts`

- [ ] **Step 1: 加三条 i18n 文案(中/英)**

在 `web/src/i18n/locales/zh/apps/root.ts` 找到 `instanceNotReady` 所在的 `channels` 段,追加:

```ts
        instanceStarting: '实例正在启动，请稍候',
        instanceRestarting: '实例正在重启，请稍候重试',
        instanceUnknown: '实例状态确认中，请稍候',
```

在 `web/src/i18n/locales/en/apps/root.ts` 对应位置追加:

```ts
        instanceStarting: 'Instance is starting, please wait',
        instanceRestarting: 'Instance is restarting, please retry shortly',
        instanceUnknown: 'Confirming instance status, please wait',
```

> 保留现有 `instanceNotReady` 作为兜底(其它非就绪业务态如 stopped/error 仍用它)。

- [ ] **Step 2: instanceReady 改双维度 + 加 phase 提示 computed**

把 `web/src/pages/apps/AppChannelsTab.vue` 的 `instanceReady`(415-416 行)改为:

```ts
// instanceReady 闸门:渠道发起依赖实例内 oc-ops 可用,需业务态在 allowlist 且运行时态 ready。
// 与后端 domain.AppCanInitiateChannelAuth(status, runtime_phase) 双维度严格一致——pod 重启/
// 未就绪窗口(runtime_phase != ready)即使 status 仍 running 也拦,避免发起打到未就绪 oc-ops 拿 502。
const AUTH_READY_STATUSES = new Set(['running', 'binding_waiting', 'binding_failed'])
const instanceReady = computed(() =>
  AUTH_READY_STATUSES.has(app?.value?.status ?? '') && app?.value?.runtime_phase === 'ready',
)
```

把 `instanceStatusLabel`(422-425 行)改为按 runtime_phase 优先给出贴切提示,回退到业务态文案:

```ts
// instanceStatusLabel 供 instanceNotReady 提示按真实原因展示:运行时态非 ready 时(pod 启动/
// 重启/未探明)按 runtime_phase 给专属文案;否则(业务态本身不允许,如 stopped/error)回退到
// 业务态本地化文案,避免笼统提示让用户误判按钮失灵。
const instanceStatusLabel = computed(() => {
  const phase = app?.value?.runtime_phase
  const status = app?.value?.status ?? ''
  // 业务态在 allowlist 但 runtime_phase 非 ready:pod 运行时未就绪,给 phase 专属文案。
  if (AUTH_READY_STATUSES.has(status)) {
    if (phase === 'starting') return t('apps.channels.instanceStarting')
    if (phase === 'restarting') return t('apps.channels.instanceRestarting')
    if (phase === 'unknown') return t('apps.channels.instanceUnknown')
  }
  // 业务态本身不允许发起(stopped/error/初始化中等):回退业务态文案。
  const view = formatAppStatus(status)
  return t(view.label, view.params ?? {})
})
```

> 三处提示模板 `t('apps.channels.instanceNotReady', { status: instanceStatusLabel })`(97/136/158 行)无需改:`instanceStatusLabel` 现已是「成型的原因句」。若 `instanceNotReady` 文案形如「实例{status}」会读着重复,可把这三处直接改成 `{{ instanceStatusLabel }}`——按实际 `instanceNotReady` 中文模板决定(模板已含完整句则直接用 label)。

- [ ] **Step 3: 前端类型检查 + 构建**

Run: `make web-typecheck && make web-build`
Expected: 通过(`app.runtime_phase` 已由 Task 10 进 generated.ts)

- [ ] **Step 4: 前端单测(若有 AppChannelsTab / status 相关)**

Run: `make web-test`
Expected: 通过

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/apps/AppChannelsTab.vue web/src/i18n/locales/zh/apps/root.ts web/src/i18n/locales/en/apps/root.ts
git commit -m "feat(channels): 前端发起闸门改双维度 + 按 runtime_phase 细化未就绪提示"
```

---

## Task 12: 全量回归 + 真实浏览器端到端验证

**Files:** 无(验证任务)

- [ ] **Step 1: 后端全量测试 + vet**

Run: `go test ./internal/... && go vet ./...`
Expected: 全绿

- [ ] **Step 2: 前端全量**

Run: `make web-typecheck && make web-build && make web-test`
Expected: 全绿

- [ ] **Step 3: OpenAPI 同步校验**

Run: `make openapi-check`
Expected: PASS

- [ ] **Step 4: 本地部署到 k3d**

Run: `make local-build`(参考 [[local-k3d-env]];注意 `*.localhost` 须 `curl --noproxy '*'`)
用 l6-org/l6-admin 的 L6 验证版本实例(真实 v2026.6.5 镜像)。

- [ ] **Step 5: 浏览器验证「就绪门控」三态(冻结技巧)**

CLAUDE.md 硬性要求真实浏览器验证(curl 不能替代)。用冻结技巧稳定观测瞬态:

- `kubectl scale deploy app-<id> --replicas=0` → pod→Pending(非坏死),reconciler 写 `runtime_phase=restarting` → 浏览器 reload 渠道页:三渠道(微信/企业微信/飞书)发起按钮**禁用** + 提示「实例正在重启,请稍候重试」。
- DB 手改 `runtime_phase='starting'`/`'unknown'` 各验一次:提示分别为「正在启动」「状态确认中」,闸门均关。
- `kubectl scale deploy app-<id> --replicas=1` → pod 两容器(hermes+oc-ops)都 Ready 后,reconciler ~15s 内写回 `runtime_phase=ready` → 闸门**放行**,可正常发起。

- [ ] **Step 6: 浏览器验证「解绑重启闭环」**

对一个已 bound 渠道的实例点解绑 → 观测:`runtime_phase` 变 `restarting`(业务态 status 仍 `running`)、发起按钮禁用+「重启中」;pod 重建 Ready 后 ~15s 自动回 `ready`、闸门放行。验证业务态全程未变 restarting(双轴生效)。

- [ ] **Step 7: 浏览器验证「首次绑定不被误拦」**

新建/重置一个实例走到 `binding_waiting`,确认 init worker 已写 `runtime_phase=ready`(详情接口 / DB 查)→ 渠道页可正常发起首次扫码绑定(不被新闸门误杀)。

- [ ] **Step 8: 三角色权限不回归**

platform_admin(看他组织实例,canManage=false 仍禁用)、org_admin(全程可管理)、org_member(仅属主可管理)三角色各确认渠道页门控行为与改造前一致(权限维度不受 runtime_phase 影响)。

- [ ] **Step 9: 记录验证证据**

按 [[feedback_verification-rigor]] 给逐项验证矩阵(场景 × 角色 × 结果 + 截图/DB 快照)。发现问题先修再验,直到全绿再交付。

---

## 平滑升级与回滚说明(交付前确认)

- migration 000019 纯增量:不动 `apps_status_check`,存量行不违反约束;`runtime_phase` 带 `DEFAULT 'unknown'`,滚动部署期旧 manager 二进制 INSERT/SELECT 不受影响。
- 乐观回填 `running→ready`:升级后运行实例不被闸门瞬间拦死;reconciler ~15s 自愈纠偏。
- 业务态 `restarting` 本次**保留**:`convergeRestarting` 排水逻辑不动,兼容滚动期旧代码与存量行;后续清理 release 再从业务 CHECK 删值并移除排水。
- 已知接受的延迟:k8s 自发重启从发生到标 restarting 有 ~15s 探测窗(主动解绑/升级路径同步置位无延迟)。
- 回滚:`make migrate-down` 一步(DROP 列);业务 CHECK 从未改动,回滚安全。
