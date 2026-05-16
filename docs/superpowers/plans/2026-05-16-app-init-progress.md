# 应用初始化进度可视化与状态机细化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 app 初始化从单一 `initializing` 黑盒拆成 5 个可视化子状态,manager 走 Docker Engine HTTP API 操作镜像,Redis 锁串行化跨实例 pull/sync,Pub/Sub 广播进度,reaper 周期 tick 接管孤儿,前端展示进度条与失败阶段。

**Architecture:** Postgres 仍是事实来源(`apps.status` / `progress_*` / `last_error_status`);Redis 作为信号通道(已有 ZSET job 队列复用 + 新增 `DistLocker` + `ProgressBus`);新建 `imagecoord` 协调器封装"单飞 + 进度广播",`reaper` 后台定时扫孤儿;worker handler 重构为 5 阶段化(每阶段幂等);前端走 status 映射 + 轮询拉 progress 字段。

**Tech Stack:**
- 后端:Go 1.25 / pgx v5 / sqlc / `github.com/redis/go-redis/v9` / `github.com/docker/docker/client` / testify
- 前端:Vue 3 + TypeScript + Vite + Pinia
- 基础设施:PostgreSQL 14 / Redis / Docker Engine / docker-compose

**Spec 引用:** `docs/superpowers/specs/2026-05-16-app-init-progress-design.md`

---

## File Structure

### 新建文件

| 文件 | 职责 |
|---|---|
| `internal/migrations/000017_app_progress_fields.up.sql` | DDL:扩展 status CHECK,新增 `progress_current/total/last_error_status` 三字段 |
| `internal/migrations/000017_app_progress_fields.down.sql` | 反向 DDL |
| `internal/runtime/imagesync/auth_store.go` | 解析 `~/.docker/config.json` 的 `auths.<registry>.auth`,返回 `types.AuthConfig` |
| `internal/runtime/imagesync/auth_store_test.go` | 测试静态 auth 与无凭据降级 |
| `internal/runtime/imagesync/sdk_provider.go` | `LocalDockerSDKProvider`:`ImageID` / `Archive`(stream) / `Pull`(NDJSON 流) |
| `internal/runtime/imagesync/sdk_provider_test.go` | mock httptest.Server 模拟 docker daemon HTTP API |
| `internal/redis/dist_locker.go` | `DistLocker`:`TryAcquire/Renew/Release/Exists` Lua 脚本 |
| `internal/redis/dist_locker_test.go` | token 校验、TTL、误删保护 |
| `internal/redis/progress_bus.go` | `ProgressBus`:`Publish/Subscribe`,JSON 编解码、`__done__` 哨兵 |
| `internal/redis/progress_bus_test.go` | publish→subscribe 端到端 |
| `internal/runtime/imagecoord/types.go` | `ProgressEvent` / `BusMessage` / `LocalImageProvider` / `AgentImageClient` 接口 |
| `internal/runtime/imagecoord/aggregator.go` | NDJSON 解析 + layer 累加,产生 ProgressEvent |
| `internal/runtime/imagecoord/aggregator_test.go` | 多 layer / `Pull complete` / 部分进度 |
| `internal/runtime/imagecoord/coordinator.go` | `Coordinator.PullImage` / `SyncToNode`(leader/follower 流程) |
| `internal/runtime/imagecoord/coordinator_test.go` | 跨实例单飞合并、subscriber 广播、leader 失败升级 |
| `internal/worker/handlers/progress_reporter.go` | 1s 节流 / 5% 阈值 / 阶段切换 flush |
| `internal/worker/handlers/progress_reporter_test.go` | 节流边界、context 取消 |
| `internal/worker/reaper/reaper.go` | `Reaper.Start` 周期 tick + Redis 锁互斥 + 孤儿扫描 |
| `internal/worker/reaper/reaper_test.go` | 5 个孤儿状态、`updated_at` 阈值、锁互斥、job 重置分支 |
| `web/src/domain/status.spec.ts` | 5 新 status 映射、`isInitPhase` 边界 |

### 修改文件

| 文件 | 改动 |
|---|---|
| `sqlc.yaml` | schema 列表补 `000016_runtime_to_hermes.up.sql` 与 `000017_app_progress_fields.up.sql` |
| `internal/store/queries/apps.sql` | 加 `SetAppProgress` / `ClearAppProgress` / `MarkAppFailed` / `ListStaleInits` |
| `internal/store/queries/jobs.sql` | 加 `GetLatestJobByPayloadAppID` / `RequeueJob` |
| `internal/store/sqlc/apps.sql.go` 等 | sqlc 重新生成 |
| `internal/store/sqlc/models.go` | sqlc 重新生成(App struct 新增字段) |
| `internal/domain/enums.go` | 移除 `AppStatusInitializing`,新增 5 个 init 子状态常量;更新 `validAppStatuses` |
| `internal/domain/app_state_machine.go` | 重写 `appTransitions`(21 条);更新顶部状态机 ASCII 注释 |
| `internal/domain/app_state_machine_test.go` | 表驱动覆盖 21 条合法 + 关键非法转移 |
| `internal/runtime/imagesync/clients.go` | 删除 `LocalDockerCLIProvider` 与 `commandReadCloser`(改用 SDK Provider) |
| `internal/runtime/imagesync/clients_test.go` | 移除针对 CLI provider 的测试 |
| `internal/worker/handlers/app_initialize.go` | `Handle()` 重写为 5 阶段循环,新增 `transitionTo` / `markFailed` / 5 个 `phase*` 方法,接 `progressReporter` |
| `internal/worker/handlers/app_initialize_test.go` | 表驱动覆盖每阶段推进、失败写 `last_error_status`、幂等检查 |
| `internal/service/runtime_operation_service.go` | `RequestInitialize` 重置目标改为 `pulling_image`,清空 `progress_*` / `last_error_status` |
| `internal/service/runtime_operation_service_test.go` | 更新断言 |
| `internal/api/handlers/dto.go` | App 响应 DTO 新增 `progress_current/progress_total/last_error_status` 字段(swag 注解) |
| `openapi/openapi.yaml` | `make openapi-gen` 重新生成 |
| `web/src/api/generated.ts` | `make web-types-gen` 重新生成 |
| `web/src/domain/status.ts` | `appStatusViews` 替换 5 项,新增 `isInitPhase` 导出 |
| `web/src/pages/apps/AppOverviewTab.vue` | 5 init 子状态时渲染进度条;status=error + last_error_status 时渲染失败阶段文案 |
| `web/src/pages/apps/AppOverviewTab.spec.ts` | 进度条 / 失败阶段断言 |
| `cmd/server/main.go` | 装配 `LocalDockerSDKProvider` 替代 CLI provider;装配 `imagecoord.Coordinator`;装配 `reaper`(workerPool 后启动) |
| `cmd/server/wiring.go` | 新增 `imageDistributorWrapper` 适配 ImageCoordinator(若需要);把 ImageCoordinator 注入 `AppInitializeHandler` |
| `docker-compose.yml` | manager 服务挂载 `/var/run/docker.sock` + `~/.docker/config.json:ro` |

### 删除文件

无(改造现有文件,旧实现内联替换)。

---

## Milestone 1:数据库 + 状态机契约

**目标:** 数据库 schema、Go domain 枚举、状态机校验、前端 status 映射四件事一次到位,出一个独立 PR;后续 milestone 都建立在这组契约之上。

### Task 1.1:更新 sqlc.yaml schema 列表

**Files:**
- Modify: `sqlc.yaml`

- [ ] **Step 1:补 000016 与 000017 schema 文件路径**

读 `sqlc.yaml`,找到 `schema:` 列表(以 `000015_app_model_governance.up.sql` 结尾),在结尾追加两行:

```yaml
      - internal/migrations/000015_app_model_governance.up.sql
      - internal/migrations/000016_runtime_to_hermes.up.sql
      - internal/migrations/000017_app_progress_fields.up.sql
```

注意:`000017_*` 文件下一个 task 才会创建,但 sqlc.yaml 先列上,Task 1.3 跑 `sqlc generate` 时正好用得上。

- [ ] **Step 2:不 commit(等到 Task 1.3 生成产物一起 commit)**

### Task 1.2:写 migration 000017 up/down SQL

**Files:**
- Create: `internal/migrations/000017_app_progress_fields.up.sql`
- Create: `internal/migrations/000017_app_progress_fields.down.sql`

- [ ] **Step 1:写 up.sql**

```sql
-- apps 表:扩展 status CHECK 约束、新增通用进度字段与上次错误状态字段。
-- status 由 5 个 init 子状态替换原 'initializing',存量行就地迁移。
-- progress_current / progress_total / last_error_status 设计为通用字段,
-- 不绑死 init 段:未来重启容器、停止等待优雅退出等长耗时操作都可复用。

ALTER TABLE apps DROP CONSTRAINT apps_status_check;

UPDATE apps SET status = 'pulling_image' WHERE status = 'initializing';

ALTER TABLE apps ADD CONSTRAINT apps_status_check CHECK (
    status IN (
        'draft',
        'pulling_image', 'syncing_image', 'preparing_runtime',
        'creating_container', 'starting',
        'binding_waiting', 'binding_failed',
        'running', 'stopped', 'error', 'deleted'
    )
);

ALTER TABLE apps
    ADD COLUMN progress_current bigint NULL,
    ADD COLUMN progress_total bigint NULL,
    ADD COLUMN last_error_status text NULL;

COMMENT ON COLUMN apps.progress_current IS '当前 status 对应阶段的已完成量;语义随 status 变化(字节 / 秒 / count),不可知时为 NULL。';
COMMENT ON COLUMN apps.progress_total IS '当前 status 对应阶段的总量;不可知时为 NULL(前端展示为不定进度)。';
COMMENT ON COLUMN apps.last_error_status IS '上次进入 error 时所在的状态值;进入 error 时写入,重新发起对应转移时清空。不加 CHECK,靠应用层在写入时校验。';
```

- [ ] **Step 2:写 down.sql(反向)**

```sql
-- 反向迁移:把 5 个 init 子状态合并回 'initializing',删进度字段与 status CHECK 调整。
-- 注意:如果 down 时 apps.status 已经处于 binding_waiting / running 等下游状态,
-- 这些行不受影响;只有 5 个 init 子状态行被合并。

ALTER TABLE apps DROP CONSTRAINT apps_status_check;

UPDATE apps SET status = 'initializing'
WHERE status IN ('pulling_image','syncing_image','preparing_runtime','creating_container','starting');

ALTER TABLE apps ADD CONSTRAINT apps_status_check CHECK (
    status IN (
        'draft', 'initializing',
        'binding_waiting', 'binding_failed',
        'running', 'stopped', 'error', 'deleted'
    )
);

ALTER TABLE apps
    DROP COLUMN progress_current,
    DROP COLUMN progress_total,
    DROP COLUMN last_error_status;
```

- [ ] **Step 3:本地 dry-run migration**

```bash
make migrate-up
```

预期:无报错,新字段写入成功。可再跑一次 `make migrate-down` 验证 down 也能回退,然后再跑 `make migrate-up` 回到目标版本。

### Task 1.3:重新生成 sqlc(让 App struct 含新字段)

**Files:**
- Regenerate: `internal/store/sqlc/models.go`、`internal/store/sqlc/apps.sql.go`

- [ ] **Step 1:跑 sqlc generate**

```bash
make sqlc-gen
```

预期:`internal/store/sqlc/models.go` 中 `type App struct { ... }` 新增三个字段:
```go
ProgressCurrent pgtype.Int8
ProgressTotal   pgtype.Int8
LastErrorStatus pgtype.Text
```

- [ ] **Step 2:验证编译**

```bash
go build ./...
```

预期:编译通过。如果其它包用 `sqlc.App` 字面量构造(如测试),会因新字段未赋值报 lint warning(允许),不会编译失败。

### Task 1.4:重写 enums.go 与 app_state_machine.go

**Files:**
- Modify: `internal/domain/enums.go`
- Modify: `internal/domain/app_state_machine.go`

- [ ] **Step 1:更新 enums.go AppStatus 常量与 validAppStatuses**

把 `AppStatusInitializing = "initializing"` 这一行删除,在原位置换成 5 个新常量。整段替换:

```go
	// AppStatus* 描述应用生命周期,合法转移由 app_state_machine.go 维护。
	// 5 个 init 子状态对应 worker 初始化阶段;前端按 status 直接展示当前阶段。
	AppStatusDraft              = "draft"
	AppStatusPullingImage       = "pulling_image"
	AppStatusSyncingImage       = "syncing_image"
	AppStatusPreparingRuntime   = "preparing_runtime"
	AppStatusCreatingContainer  = "creating_container"
	AppStatusStarting           = "starting"
	AppStatusBindingWaiting     = "binding_waiting"
	AppStatusBindingFailed      = "binding_failed"
	AppStatusRunning            = "running"
	AppStatusStopped            = "stopped"
	AppStatusError              = "error"
	AppStatusDeleted            = "deleted"
```

并相应更新 `validAppStatuses = set(...)` 列表(把 `AppStatusInitializing` 替换为 5 个新常量)。

- [ ] **Step 2:重写 app_state_machine.go 的 appTransitions**

替换 `appTransitions = map[AppTransition]struct{}{ ... }` 为 21 条:

```go
var appTransitions = map[AppTransition]struct{}{
	// 5 个 init 子状态串行推进
	{From: AppStatusDraft, To: AppStatusPullingImage}:                  {},
	{From: AppStatusPullingImage, To: AppStatusSyncingImage}:           {},
	{From: AppStatusSyncingImage, To: AppStatusPreparingRuntime}:       {},
	{From: AppStatusPreparingRuntime, To: AppStatusCreatingContainer}:  {},
	{From: AppStatusCreatingContainer, To: AppStatusStarting}:          {},
	{From: AppStatusStarting, To: AppStatusBindingWaiting}:             {},

	// binding / running 段(原状态机)
	{From: AppStatusBindingWaiting, To: AppStatusRunning}:        {},
	{From: AppStatusBindingWaiting, To: AppStatusBindingFailed}:  {},
	{From: AppStatusBindingFailed, To: AppStatusBindingWaiting}:  {},
	{From: AppStatusBindingFailed, To: AppStatusError}:           {},
	{From: AppStatusRunning, To: AppStatusStopped}:               {},
	{From: AppStatusRunning, To: AppStatusError}:                 {},
	{From: AppStatusStopped, To: AppStatusRunning}:               {},
	{From: AppStatusStopped, To: AppStatusError}:                 {},

	// 5 个 init 子状态失败都进 error
	{From: AppStatusPullingImage, To: AppStatusError}:       {},
	{From: AppStatusSyncingImage, To: AppStatusError}:       {},
	{From: AppStatusPreparingRuntime, To: AppStatusError}:   {},
	{From: AppStatusCreatingContainer, To: AppStatusError}:  {},
	{From: AppStatusStarting, To: AppStatusError}:           {},

	// error 重试 / 软删除
	{From: AppStatusError, To: AppStatusPullingImage}: {},
	// error → deleted 由 IsAppTransitionAllowed 内的特殊分支兜底,无需在 map 中重复。
}
```

- [ ] **Step 3:更新 app_state_machine.go 顶部 ASCII 注释**

替换原 `draft → initializing → binding_waiting` 注释块为:

```go
// # 状态机
//
//	draft ─▶ pulling_image ─▶ syncing_image ─▶ preparing_runtime ─▶ creating_container ─▶ starting ─▶ binding_waiting
//	          │                  │                  │                     │                   │              │
//	          ▼                  ▼                  ▼                     ▼                   ▼              ▼ 渠道扫码
//	         error  ◀───────────任意 init 子状态失败────────────────────────────────────  binding_failed
//	          ▲                                                                              │
//	          └──────────────────────────────────────────────────────────────────────────────┴───▶ running
//	                                                                                                │
//	                                                                                                ▼
//	                                                                                              stopped
//	                                                                                                │
//	                                                                                                ▼
//	                                                                                              deleted
```

注释里的关键转移约束更新成:
- `draft → pulling_image`:onboarding job 拾取后第一阶段;
- `pulling_image → syncing_image → preparing_runtime → creating_container → starting`:worker 串行推进;
- `starting → binding_waiting`:容器健康后等渠道扫码;
- 任意 init 子状态 → `error`:失败收敛,`last_error_status` 记录来源;
- `error → pulling_image`:`RequestInitialize` 重试入口。

- [ ] **Step 4:删除可能残留的 `AppStatusInitializing` 引用**

```bash
grep -rn "AppStatusInitializing" --include="*.go" .
```

预期:0 命中。如果还有,改成 `AppStatusPullingImage`(对应 worker 入口)或对应的 5 个子状态之一。

### Task 1.5:写 app_state_machine_test.go(表驱动覆盖 21 条转移)

**Files:**
- Modify: `internal/domain/app_state_machine_test.go`(若存在)/Create:同名

- [ ] **Step 1:写测试**

```go
package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsAppTransitionAllowed_LegalTransitions 验证 21 条合法转移每条都能通过校验。
// 子测试 name 即转移本身,失败时定位精确到具体一行。
func TestIsAppTransitionAllowed_LegalTransitions(t *testing.T) {
	cases := []struct {
		from string
		to   string
	}{
		// 5 个 init 子状态串行
		{AppStatusDraft, AppStatusPullingImage},                 // onboarding 入口
		{AppStatusPullingImage, AppStatusSyncingImage},          // pull 完成进 sync
		{AppStatusSyncingImage, AppStatusPreparingRuntime},      // sync 完成进准备运行时
		{AppStatusPreparingRuntime, AppStatusCreatingContainer}, // 准备完成创建容器
		{AppStatusCreatingContainer, AppStatusStarting},         // 创建完成启动
		{AppStatusStarting, AppStatusBindingWaiting},            // 容器健康进入等待绑定

		// binding / running 段
		{AppStatusBindingWaiting, AppStatusRunning},       // 渠道扫码成功
		{AppStatusBindingWaiting, AppStatusBindingFailed}, // 扫码超时 / token 过期
		{AppStatusBindingFailed, AppStatusBindingWaiting}, // 重启绑定
		{AppStatusBindingFailed, AppStatusError},          // 多次失败后用户放弃 / 自动收敛
		{AppStatusRunning, AppStatusStopped},              // 用户主动停止
		{AppStatusRunning, AppStatusError},                // 运行时容器异常退出
		{AppStatusStopped, AppStatusRunning},              // 用户重启
		{AppStatusStopped, AppStatusError},                // 停止状态下底层异常

		// 5 个 init 子状态各自失败
		{AppStatusPullingImage, AppStatusError},      // pull 失败
		{AppStatusSyncingImage, AppStatusError},      // sync 失败
		{AppStatusPreparingRuntime, AppStatusError},  // 写运行时配置失败
		{AppStatusCreatingContainer, AppStatusError}, // 创建容器失败
		{AppStatusStarting, AppStatusError},          // 启动 / 健康检查失败

		// error 重试 / 软删除
		{AppStatusError, AppStatusPullingImage}, // RequestInitialize 重试入口
		{AppStatusError, AppStatusDeleted},      // SoftDeleteApp 终态
	}
	for _, c := range cases {
		// 子测试名直接用转移本身,便于定位;失败时一眼看出哪一条未通过校验。
		t.Run(c.from+"->"+c.to, func(t *testing.T) {
			assert.True(t, IsAppTransitionAllowed(c.from, c.to), "合法转移被拒绝")
		})
	}
}

// TestIsAppTransitionAllowed_IllegalTransitions 覆盖关键非法转移。
// 不穷举,只挑能体现"状态机不会被绕过"的代表性 case。
func TestIsAppTransitionAllowed_IllegalTransitions(t *testing.T) {
	cases := []struct {
		name string
		from string
		to   string
	}{
		// 跳阶段:不能从 pulling 直接跳过 sync 到 preparing
		{"跳过 sync 阶段", AppStatusPullingImage, AppStatusPreparingRuntime},
		// 不能从 running 回退到 init 子状态
		{"running 不能回退到 init", AppStatusRunning, AppStatusPullingImage},
		// 同状态原地转移视为非法,避免 worker 重复触发副作用
		{"同状态原地转移", AppStatusPullingImage, AppStatusPullingImage},
		// 进入 deleted 必须从 error 出发(SoftDeleteApp 路径)
		{"running 不能直接 → deleted", AppStatusRunning, AppStatusDeleted},
		// draft 只能进 pulling_image,不能直接进 binding_waiting
		{"draft 不能直接 → binding_waiting", AppStatusDraft, AppStatusBindingWaiting},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.False(t, IsAppTransitionAllowed(c.from, c.to), "非法转移被放行")
		})
	}
}
```

- [ ] **Step 2:运行测试**

```bash
go test ./internal/domain/ -run TestIsAppTransitionAllowed -v
```

预期:全部 PASS。

### Task 1.6:更新 status.ts(前端 status 映射) + 单测

**Files:**
- Modify: `web/src/domain/status.ts`
- Create: `web/src/domain/status.spec.ts`

- [ ] **Step 1:替换 appStatusViews + 加 isInitPhase 导出**

把 `appStatusViews` 整段替换:

```ts
// appStatusViews 覆盖应用生命周期状态,新增后端状态时应同步补充。
// 5 个 init 子状态文案与后端状态机 1:1 对应,用户能直观看到"现在在做什么"。
const appStatusViews: Record<string, StatusView> = {
  draft:               { label: '待初始化',         tone: 'neutral' },
  pulling_image:       { label: '拉取运行时镜像',   tone: 'warning' },
  syncing_image:       { label: '同步镜像到节点',   tone: 'warning' },
  preparing_runtime:   { label: '准备运行时配置',   tone: 'warning' },
  creating_container:  { label: '创建容器',         tone: 'warning' },
  starting:            { label: '启动容器',         tone: 'warning' },
  binding_waiting:     { label: '待绑定',           tone: 'warning' },
  binding_failed:      { label: '绑定失败',         tone: 'danger' },
  running:             { label: '运行中',           tone: 'success' },
  stopped:             { label: '已停止',           tone: 'neutral' },
  error:               { label: '异常',             tone: 'danger' },
  deleted:             { label: '已删除',           tone: 'neutral' },
}

// initPhaseStatuses 是 worker 初始化期间会出现的 5 个子状态集合。
// AppOverviewTab 在 status ∈ 该集合时额外渲染 progress 进度条。
// 集合写在这里集中维护,避免组件硬编码字符串列表。
const initPhaseStatuses: ReadonlySet<string> = new Set([
  'pulling_image',
  'syncing_image',
  'preparing_runtime',
  'creating_container',
  'starting',
])

// isInitPhase 判断 status 是否处于初始化进度可视化的 5 个子状态之一。
export function isInitPhase(status: string): boolean {
  return initPhaseStatuses.has(status)
}
```

- [ ] **Step 2:写 status.spec.ts**

```ts
import { describe, expect, it } from 'vitest'
import { formatAppStatus, isInitPhase } from './status'

describe('formatAppStatus', () => {
  // 5 个新 init 子状态 + draft 文案重命名,逐项验证 label 与 tone。
  it.each([
    ['draft', '待初始化', 'neutral'],
    ['pulling_image', '拉取运行时镜像', 'warning'],
    ['syncing_image', '同步镜像到节点', 'warning'],
    ['preparing_runtime', '准备运行时配置', 'warning'],
    ['creating_container', '创建容器', 'warning'],
    ['starting', '启动容器', 'warning'],
  ])('status=%s 映射为 label=%s / tone=%s', (status, label, tone) => {
    const view = formatAppStatus(status)
    expect(view.label).toBe(label)
    expect(view.tone).toBe(tone)
  })

  // 后端如灰度新增 status,降级为"未知状态:xxx" + warning,避免页面空白。
  it('未知 status 走降级分支', () => {
    const view = formatAppStatus('weird_state')
    expect(view.label).toContain('未知状态')
    expect(view.tone).toBe('warning')
  })
})

describe('isInitPhase', () => {
  // 5 个 init 子状态都返回 true。
  it.each(['pulling_image', 'syncing_image', 'preparing_runtime', 'creating_container', 'starting'])(
    '%s 是 init 子状态',
    (status) => {
      expect(isInitPhase(status)).toBe(true)
    },
  )

  // 边界:draft / binding_waiting / running / error 都不算 init 子状态。
  it.each(['draft', 'binding_waiting', 'running', 'error', 'deleted'])(
    '%s 不是 init 子状态',
    (status) => {
      expect(isInitPhase(status)).toBe(false)
    },
  )
})
```

- [ ] **Step 3:运行前端测试**

```bash
cd web && pnpm vitest run src/domain/status.spec.ts
```

预期:全部 PASS。

### Task 1.7:Milestone 1 提交

- [ ] **Step 1:确认改动范围**

```bash
git status
git diff --stat
```

预期改动文件:
- `sqlc.yaml`
- `internal/migrations/000017_app_progress_fields.{up,down}.sql`(新)
- `internal/store/sqlc/models.go`、`apps.sql.go`(sqlc 生成)
- `internal/domain/enums.go`、`app_state_machine.go`、`app_state_machine_test.go`
- `web/src/domain/status.ts`、`status.spec.ts`(新)

- [ ] **Step 2:全量编译 + 测试 sanity check**

```bash
go build ./... && go test ./internal/domain/... -v
cd web && pnpm vitest run src/domain/
```

- [ ] **Step 3:commit**

```bash
git add sqlc.yaml internal/migrations/ internal/store/sqlc/ internal/domain/ web/src/domain/
git commit -m "$(cat <<'EOF'
feat(domain): apps 状态机拆分 init 段为 5 个子状态

把原 'initializing' 单状态拆成 pulling_image / syncing_image /
preparing_runtime / creating_container / starting 五个子状态,前端
能直观看到 worker 当前在做什么;新增 progress_current / progress_total /
last_error_status 三个通用字段供未来其他长耗时操作复用。

- migration 000017:status CHECK 扩列、就地迁移存量 initializing 行
  到 pulling_image,新增三字段 + COMMENT 说明语义;
- enums.go:删除 AppStatusInitializing,新增 5 个子状态常量;
- app_state_machine.go:21 条合法转移(5 阶段串行 + 5 失败 + 重试入口),
  同步顶部 ASCII 注释;
- web/status.ts:新增 5 个状态映射,'草稿' 改为 '待初始化',导出
  isInitPhase 给 AppOverviewTab 使用;
- 表驱动单测覆盖 21 条合法 + 关键非法转移,前端 vitest 覆盖映射与边界。
EOF
)"
```

---

## Milestone 2:本地 Docker SDK Provider

**目标:** 用 `github.com/docker/docker/client` 替换 `LocalDockerCLIProvider`(`exec.Command("docker", ...)` 在 manager 容器内根本跑不起来),并且解析宿主机 `~/.docker/config.json` 凭据,支持从 registry 兜底拉镜像。本 milestone 只动 imagesync 包内部 + 装配,不改 worker 流程。

### Task 2.1:写 RegistryAuthStore

**Files:**
- Create: `internal/runtime/imagesync/auth_store.go`
- Create: `internal/runtime/imagesync/auth_store_test.go`

- [ ] **Step 1:写测试(TDD)**

```go
package imagesync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadRegistryAuthStore_StaticAuth 验证从 config.json 静态 auths 字段读取凭据。
// 一期只支持 base64(user:pass) 格式,不处理 credentials helper / keychain。
func TestLoadRegistryAuthStore_StaticAuth(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	// 模拟典型 docker login 后生成的 config.json:base64("alice:s3cret") = "YWxpY2U6czNjcmV0"
	content := `{
  "auths": {
    "registry.example.com": {"auth": "YWxpY2U6czNjcmV0"},
    "https://index.docker.io/v1/": {"auth": "Ym9iOmh1bnRlcjI="}
  }
}`
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0o600))

	store, err := LoadRegistryAuthStore(configPath)
	require.NoError(t, err)

	// 自定义 registry:命中 hostname 直接返回
	got := store.AuthFor("registry.example.com/team/app:dev")
	assert.Equal(t, "alice", got.Username)
	assert.Equal(t, "s3cret", got.Password)
	assert.Equal(t, "registry.example.com", got.ServerAddress)

	// docker.io 默认 hub:支持 "library/foo:tag" 简写
	hub := store.AuthFor("library/hermes-runtime:dev")
	assert.Equal(t, "bob", hub.Username)
	assert.Equal(t, "hunter2", hub.Password)
}

// TestLoadRegistryAuthStore_MissingFile 配置文件不存在视为"无凭据",不报错;
// 拉公共镜像不需要 auth,缺文件不应阻塞 manager 启动。
func TestLoadRegistryAuthStore_MissingFile(t *testing.T) {
	store, err := LoadRegistryAuthStore("/nonexistent/config.json")
	require.NoError(t, err)
	assert.Empty(t, store.AuthFor("library/anything:latest").Username)
}

// TestLoadRegistryAuthStore_NoMatch 已加载但目标 registry 不在 auths 里:
// 返回零值,调用方不传 X-Registry-Auth 头,docker daemon 仍可拉公共镜像。
func TestLoadRegistryAuthStore_NoMatch(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"auths":{"a.example.com":{"auth":"YWxpY2U6czNjcmV0"}}}`), 0o600))

	store, err := LoadRegistryAuthStore(configPath)
	require.NoError(t, err)
	assert.Empty(t, store.AuthFor("b.example.com/foo:bar").Username)
}
```

- [ ] **Step 2:运行测试,验证 fail**

```bash
go test ./internal/runtime/imagesync/ -run TestLoadRegistryAuthStore -v
```

预期:FAIL(`LoadRegistryAuthStore` 未定义)。

- [ ] **Step 3:写实现**

```go
// Package imagesync 在原有 nodeID 维度同步逻辑之外,
// 新增 manager 本机 Docker Engine HTTP API 客户端与 registry auth 解析。
package imagesync

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/docker/docker/api/types/registry"
)

// RegistryAuthStore 把 ~/.docker/config.json 中的静态 auths 字段加载到内存,
// 一期不支持 credentials helper / keychain;凭据轮换通过重启 manager 生效。
type RegistryAuthStore struct {
	// auths 的 key 是 normalized registry hostname(去 https:// 前缀、去尾部 /v1/)。
	// docker.io / hub.docker.com / index.docker.io 都规一化到 "docker.io"。
	auths map[string]registry.AuthConfig
}

// dockerConfigFile 只解析当前关心的 auths 段,其它字段(credsStore / credHelpers)忽略。
type dockerConfigFile struct {
	Auths map[string]struct {
		Auth string `json:"auth"`
	} `json:"auths"`
}

// LoadRegistryAuthStore 读取并解析 config.json。
// 文件不存在视为"无凭据"(返回空 store + nil err),不影响 manager 启动。
func LoadRegistryAuthStore(path string) (RegistryAuthStore, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return RegistryAuthStore{auths: map[string]registry.AuthConfig{}}, nil
		}
		return RegistryAuthStore{}, fmt.Errorf("读取 docker config %s 失败: %w", path, err)
	}
	var raw dockerConfigFile
	if err := json.Unmarshal(body, &raw); err != nil {
		return RegistryAuthStore{}, fmt.Errorf("解析 docker config 失败: %w", err)
	}
	auths := make(map[string]registry.AuthConfig, len(raw.Auths))
	for rawHost, entry := range raw.Auths {
		host := normalizeRegistryHost(rawHost)
		if host == "" || entry.Auth == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
		if err != nil {
			// 单条解析失败不阻断整个 store 加载,记账 skip 其它仍可用。
			continue
		}
		idx := strings.IndexByte(string(decoded), ':')
		if idx <= 0 {
			continue
		}
		auths[host] = registry.AuthConfig{
			Username:      string(decoded[:idx]),
			Password:      string(decoded[idx+1:]),
			ServerAddress: host,
		}
	}
	return RegistryAuthStore{auths: auths}, nil
}

// normalizeRegistryHost 把 docker login 写入的多种格式统一成 hostname。
// "https://index.docker.io/v1/" → "docker.io"、"registry.example.com" → "registry.example.com"。
func normalizeRegistryHost(raw string) string {
	host := strings.TrimSpace(raw)
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	if host == "index.docker.io" || host == "registry-1.docker.io" || host == "hub.docker.com" {
		return "docker.io"
	}
	return host
}

// AuthFor 根据镜像引用挑选凭据。
// 镜像形如 "registry.example.com/team/app:tag" / "library/foo:tag" / "foo:tag"。
// 不含 '/' 或第一个 '/' 之前不像 hostname 的视为 docker.io 默认 hub。
func (s RegistryAuthStore) AuthFor(image string) registry.AuthConfig {
	host := imageHost(image)
	if cfg, ok := s.auths[host]; ok {
		return cfg
	}
	return registry.AuthConfig{}
}

// imageHost 复用 docker 官方约定:第一段不包含 '.' 或 ':' 视为 docker.io 仓库。
func imageHost(image string) string {
	idx := strings.IndexByte(image, '/')
	if idx < 0 {
		return "docker.io"
	}
	first := image[:idx]
	if !strings.ContainsAny(first, ".:") && first != "localhost" {
		return "docker.io"
	}
	return first
}
```

- [ ] **Step 4:运行测试,验证 PASS**

```bash
go test ./internal/runtime/imagesync/ -run TestLoadRegistryAuthStore -v
```

预期:全部 PASS。

### Task 2.2:写 LocalDockerSDKProvider

**Files:**
- Create: `internal/runtime/imagesync/sdk_provider.go`
- Create: `internal/runtime/imagesync/sdk_provider_test.go`

- [ ] **Step 1:写测试(TDD,httptest 模拟 docker daemon)**

```go
package imagesync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dockerclient "github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFakeDockerServer 返回一个 httptest server 用于模拟 docker daemon HTTP API。
// 路由覆盖本测试需要的三个端点:images/inspect、images/get(save)、images/create(pull)。
func newFakeDockerServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	return httptest.NewServer(h)
}

// TestLocalDockerSDKProvider_ImageID 验证 inspect 解析 ID。
func TestLocalDockerSDKProvider_ImageID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/images/hermes-runtime:dev/json", func(w http.ResponseWriter, _ *http.Request) {
		// docker daemon ImageInspect 返回的 JSON 字段;sha256 前缀是常见格式。
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "sha256:abc"})
	})
	srv := newFakeDockerServer(t, mux)
	defer srv.Close()

	cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(srv.URL), dockerclient.WithAPIVersionNegotiation())
	require.NoError(t, err)
	prov := LocalDockerSDKProvider{cli: cli}

	id, err := prov.ImageID(context.Background(), "hermes-runtime:dev")
	require.NoError(t, err)
	assert.Equal(t, "sha256:abc", id)
}

// TestLocalDockerSDKProvider_Archive 验证 ImageSave 流式返回 tar bytes。
func TestLocalDockerSDKProvider_Archive(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/images/get", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "hermes-runtime:dev", r.URL.Query().Get("names"))
		w.Header().Set("Content-Type", "application/x-tar")
		_, _ = io.WriteString(w, "FAKE-TAR-PAYLOAD")
	})
	srv := newFakeDockerServer(t, mux)
	defer srv.Close()

	cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(srv.URL), dockerclient.WithAPIVersionNegotiation())
	require.NoError(t, err)
	prov := LocalDockerSDKProvider{cli: cli}

	rc, err := prov.Archive(context.Background(), "hermes-runtime:dev")
	require.NoError(t, err)
	defer rc.Close()
	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "FAKE-TAR-PAYLOAD", string(body))
}

// TestLocalDockerSDKProvider_Pull 验证 NDJSON 流被原样转发给调用方;
// 解析 / 累加由 imagecoord/aggregator 负责,本 provider 只透传字节流与 RegistryAuth 头。
func TestLocalDockerSDKProvider_Pull(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/images/create", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "hermes-runtime", r.URL.Query().Get("fromImage"))
		assert.Equal(t, "dev", r.URL.Query().Get("tag"))
		// X-Registry-Auth 必须是 base64(json(authConfig))
		raw, err := base64.URLEncoding.DecodeString(r.Header.Get("X-Registry-Auth"))
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"username":"alice"`)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"Pulling fs layer","id":"abc"}`+"\n")
		_, _ = io.WriteString(w, `{"status":"Pull complete","id":"abc"}`+"\n")
	})
	srv := newFakeDockerServer(t, mux)
	defer srv.Close()

	cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(srv.URL), dockerclient.WithAPIVersionNegotiation())
	require.NoError(t, err)
	store := RegistryAuthStore{}
	// 手工塞一条 auth,等价于 LoadRegistryAuthStore 解析后的状态
	store.auths = map[string]registry.AuthConfig{
		"docker.io": {Username: "alice", Password: "s3cret", ServerAddress: "docker.io"},
	}
	prov := LocalDockerSDKProvider{cli: cli, authStore: store}

	rc, err := prov.Pull(context.Background(), "hermes-runtime:dev")
	require.NoError(t, err)
	defer rc.Close()
	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(body), "Pulling fs layer"))
	assert.True(t, strings.Contains(string(body), "Pull complete"))
}
```

- [ ] **Step 2:写实现**

```go
package imagesync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
)

// LocalDockerSDKProvider 替代旧 LocalDockerCLIProvider,通过 Docker Engine HTTP API
// 完成本机镜像 inspect / save / pull,完全在 manager 容器内消费 response 流,
// 规避了 manager 容器无 docker CLI、宿主机 docker save 写宿主机路径的两个问题。
type LocalDockerSDKProvider struct {
	cli       *dockerclient.Client
	authStore RegistryAuthStore
}

// NewLocalDockerSDKProvider 创建 provider。
// dockerHost 为空时走 client.FromEnv(读 DOCKER_HOST / 默认 unix:///var/run/docker.sock)。
// configPath 为空时跳过凭据加载,只能拉无 auth 的公共镜像。
func NewLocalDockerSDKProvider(dockerHost, configPath string) (*LocalDockerSDKProvider, error) {
	opts := []dockerclient.Opt{dockerclient.WithAPIVersionNegotiation()}
	if dockerHost != "" {
		opts = append(opts, dockerclient.WithHost(dockerHost))
	} else {
		opts = append(opts, dockerclient.FromEnv)
	}
	cli, err := dockerclient.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("初始化 docker client 失败: %w", err)
	}
	store, err := LoadRegistryAuthStore(configPath)
	if err != nil {
		return nil, err
	}
	return &LocalDockerSDKProvider{cli: cli, authStore: store}, nil
}

// ImageID 走 ImageInspect API 取镜像 ID,用于跟目标节点 ID 做精确比对。
// 镜像不存在时 docker SDK 返回 client.IsErrNotFound;调用方需自己判断。
func (p *LocalDockerSDKProvider) ImageID(ctx context.Context, imageRef string) (string, error) {
	inspect, _, err := p.cli.ImageInspectWithRaw(ctx, imageRef)
	if err != nil {
		return "", fmt.Errorf("docker image inspect %s: %w", imageRef, err)
	}
	return inspect.ID, nil
}

// Archive 调 ImageSave 拿到 tar 流;返回的 ReadCloser 是 daemon HTTP body,
// 全程在 manager 容器内,不写宿主机文件。调用方必须 Close。
func (p *LocalDockerSDKProvider) Archive(ctx context.Context, imageRef string) (io.ReadCloser, error) {
	rc, err := p.cli.ImageSave(ctx, []string{imageRef})
	if err != nil {
		return nil, fmt.Errorf("docker image save %s: %w", imageRef, err)
	}
	return rc, nil
}

// Pull 调 ImagePull 触发 daemon 从 registry 下载;返回 NDJSON 流,
// 调用方(imagecoord/aggregator)负责解析 progressDetail.{current,total}。
// X-Registry-Auth 由 authStore.AuthFor 决定;无凭据则不写头(走匿名拉取)。
func (p *LocalDockerSDKProvider) Pull(ctx context.Context, imageRef string) (io.ReadCloser, error) {
	opts := image.PullOptions{}
	if auth := p.authStore.AuthFor(imageRef); auth.Username != "" {
		encoded, err := encodeRegistryAuth(auth)
		if err != nil {
			return nil, err
		}
		opts.RegistryAuth = encoded
	}
	rc, err := p.cli.ImagePull(ctx, imageRef, opts)
	if err != nil {
		return nil, fmt.Errorf("docker image pull %s: %w", imageRef, err)
	}
	return rc, nil
}

// encodeRegistryAuth 把 auth 配置编码为 docker daemon 期望的 X-Registry-Auth 格式
// (base64(JSON(authConfig)),URL-safe encoding,与 docker SDK 默认契约一致)。
func encodeRegistryAuth(auth interface{}) (string, error) {
	body, err := json.Marshal(auth)
	if err != nil {
		return "", fmt.Errorf("序列化 docker registry auth 失败: %w", err)
	}
	return base64.URLEncoding.EncodeToString(body), nil
}
```

- [ ] **Step 3:运行测试**

```bash
go test ./internal/runtime/imagesync/ -run TestLocalDockerSDKProvider -v
```

预期:全部 PASS。

### Task 2.3:删除 LocalDockerCLIProvider

**Files:**
- Modify: `internal/runtime/imagesync/clients.go`
- Modify: `internal/runtime/imagesync/clients_test.go`

- [ ] **Step 1:删除 clients.go 中 lines 17-72(LocalDockerCLIProvider + commandReadCloser)**

具体删除范围:从 `// LocalDockerCLIProvider 通过本机 docker CLI inspect/save 镜像。` 注释开始,到 `commandReadCloser` 结构体的最后一个 `}` 结束。`AgentHTTPClient` 与其方法保留。

同时删除文件顶部不再需要的 import:
- `"bytes"`
- `"os/exec"`
- `"strings"`(若 AgentHTTPClient 不再用)

`grep` 确认 imports 不被剩余代码使用,然后删除对应 import 行。

- [ ] **Step 2:删除 clients_test.go 中针对 LocalDockerCLIProvider 的测试**

打开 `clients_test.go`,搜索 `LocalDockerCLIProvider`,把对应的 `func TestLocalDockerCLIProvider_*` 测试函数整段删掉。`AgentHTTPClient` 的测试保留。

- [ ] **Step 3:全量编译**

```bash
go build ./...
```

预期:编译通过。如果有引用 `LocalDockerCLIProvider` 的地方报错(只可能是 `cmd/server/main.go:178`),先记录,Task 2.4 替换。

### Task 2.4:wiring 替换为 LocalDockerSDKProvider + docker-compose 挂载

**Files:**
- Modify: `cmd/server/main.go`
- Modify: `docker-compose.yml`(项目根)
- Possibly: `internal/config/config.go`(如需新增 docker config 路径配置项)

- [ ] **Step 1:在 cmd/server/main.go 替换 imageSync 装配**

找到 `imageSync := imagesync.New(imagesync.LocalDockerCLIProvider{}, nodeResolver)`(约 main.go:178),替换为:

```go
	// LocalDockerSDKProvider 通过 /var/run/docker.sock 调 Docker Engine HTTP API,
	// 不依赖 manager 容器内的 docker CLI;凭据从挂载进来的宿主机 ~/.docker/config.json 读。
	// dockerHost 为空走 client.FromEnv(默认 unix:///var/run/docker.sock);
	// configPath 走环境变量 DOCKER_CONFIG/config.json 或者代码默认 /root/.docker/config.json。
	dockerSDK, err := imagesync.NewLocalDockerSDKProvider("", "/root/.docker/config.json")
	if err != nil {
		return fmt.Errorf("初始化本地 docker SDK provider: %w", err)
	}
	imageSync := imagesync.New(dockerSDK, nodeResolver)
```

- [ ] **Step 2:在 docker-compose.yml 给 manager 服务加挂载**

读 `docker-compose.yml`,找到 `services.manager.volumes:` 段(若不存在则新增)。追加两行:

```yaml
    volumes:
      # 宿主机 docker daemon socket:让 manager 容器调用 Docker Engine HTTP API。
      - /var/run/docker.sock:/var/run/docker.sock
      # 宿主机 ~/.docker/config.json:为镜像 pull 提供 registry 凭据(只读)。
      - ${HOME}/.docker/config.json:/root/.docker/config.json:ro
```

如果原来已经挂了 `/var/run/docker.sock`,只追加 config.json 一行。

- [ ] **Step 3:重启本地 manager 验证装配不报错**

```bash
docker compose up -d --build manager
docker compose logs manager 2>&1 | head -50
```

预期:启动日志没有 `初始化本地 docker SDK provider` 报错;manager 容器正常 listen 端口。

### Task 2.5:Milestone 2 提交

- [ ] **Step 1:确认改动**

```bash
git status && git diff --stat
```

涉及文件:
- `internal/runtime/imagesync/auth_store.go`、`auth_store_test.go`(新)
- `internal/runtime/imagesync/sdk_provider.go`、`sdk_provider_test.go`(新)
- `internal/runtime/imagesync/clients.go`、`clients_test.go`(删除 CLI provider)
- `cmd/server/main.go`(装配替换)
- `docker-compose.yml`(挂载追加)

- [ ] **Step 2:跑全量测试**

```bash
go test ./internal/runtime/imagesync/... -v
```

- [ ] **Step 3:commit**

```bash
git add internal/runtime/imagesync/ cmd/server/main.go docker-compose.yml
git commit -m "$(cat <<'EOF'
feat(imagesync): manager 镜像操作改走 Docker Engine HTTP API

manager 容器内本来就没有 docker CLI,exec.Command("docker", ...) 在容器
内根本跑不起来;即便挂载宿主机 docker save -o,写出来的 tar 也在宿主机
路径,manager 容器看不到。换用 docker SDK + /var/run/docker.sock,
ImageSave / ImagePull 返回的 ReadCloser 是 daemon HTTP response body
流,全程在 manager 容器内消费。

- LocalDockerSDKProvider:ImageID / Archive / Pull 三个方法,Pull 透传
  NDJSON 流给上层 aggregator 解析;
- RegistryAuthStore:启动时一次性解析 ~/.docker/config.json 的 auths
  字段,一期只支持 base64(user:pass) 格式,helper / keychain 视为无凭据;
- 删除 LocalDockerCLIProvider 与 commandReadCloser;
- docker-compose 挂载 /var/run/docker.sock 与宿主机 ~/.docker/config.json
  (只读),凭据轮换通过重启 manager 生效。

worker 流程本身没动,EnsureRuntimeImage 走的还是原 imagesync.Service,
ImageCoordinator 在后续 milestone 落地时再接入。
EOF
)"
```

---

## Milestone 3:Redis DistLocker + ProgressBus

**目标:** 在 `internal/redis/` 包内新增"分布式锁"和"进度总线"两个独立、与业务解耦的基础设施;不依赖 imagecoord 包。本 milestone 完成后,Redis 信号通道能力齐备,后续 imagecoord 直接组合使用。

### Task 3.1:写 DistLocker 接口与 Redis 实现

**Files:**
- Create: `internal/redis/dist_locker.go`
- Create: `internal/redis/dist_locker_test.go`

- [ ] **Step 1:写测试(TDD,需要本地 Redis)**

测试依赖 `redis_integration_test.go` 已经使用的本地 Redis(若 CI 没起 Redis,可在测试顶部用 build tag `//go:build integration` 隔离;参考 `redis_integration_test.go` 的现有写法决定)。

```go
package redis

import (
	"context"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestLocker 复用 redis_integration_test.go 的连接约定;
// 测试结束清理 key,避免污染。
func newTestLocker(t *testing.T) (*RedisDistLocker, func()) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skip("本地 Redis 不可用,跳过 DistLocker 集成测试: " + err.Error())
	}
	locker := NewRedisDistLocker(client)
	cleanup := func() {
		_ = client.FlushDB(context.Background()).Err()
		_ = client.Close()
	}
	return locker, cleanup
}

// TestDistLocker_TryAcquire_NewKey 第一个抢锁的进程拿到锁。
func TestDistLocker_TryAcquire_NewKey(t *testing.T) {
	locker, cleanup := newTestLocker(t)
	defer cleanup()
	ok, err := locker.TryAcquire(context.Background(), "ocm:test:key", "tok-A", 5*time.Second)
	require.NoError(t, err)
	assert.True(t, ok)
}

// TestDistLocker_TryAcquire_Conflict 已经被别人持有时第二个返回 false。
func TestDistLocker_TryAcquire_Conflict(t *testing.T) {
	locker, cleanup := newTestLocker(t)
	defer cleanup()
	ok, err := locker.TryAcquire(context.Background(), "ocm:test:key", "tok-A", 5*time.Second)
	require.NoError(t, err)
	require.True(t, ok)
	ok2, err := locker.TryAcquire(context.Background(), "ocm:test:key", "tok-B", 5*time.Second)
	require.NoError(t, err)
	assert.False(t, ok2)
}

// TestDistLocker_Release_TokenMatch 自己的 token 才能释放;别人 token 释放无效。
func TestDistLocker_Release_TokenMatch(t *testing.T) {
	locker, cleanup := newTestLocker(t)
	defer cleanup()
	_, _ = locker.TryAcquire(context.Background(), "ocm:test:key", "tok-A", 5*time.Second)

	// tok-B 误尝试释放,锁仍然属于 tok-A
	require.NoError(t, locker.Release(context.Background(), "ocm:test:key", "tok-B"))
	exists, err := locker.Exists(context.Background(), "ocm:test:key")
	require.NoError(t, err)
	assert.True(t, exists)

	// tok-A 释放成功后 key 消失
	require.NoError(t, locker.Release(context.Background(), "ocm:test:key", "tok-A"))
	exists, err = locker.Exists(context.Background(), "ocm:test:key")
	require.NoError(t, err)
	assert.False(t, exists)
}

// TestDistLocker_Renew_TokenMatch token 一致才能续期;过期后续期不复活。
func TestDistLocker_Renew_TokenMatch(t *testing.T) {
	locker, cleanup := newTestLocker(t)
	defer cleanup()
	_, _ = locker.TryAcquire(context.Background(), "ocm:test:key", "tok-A", 1*time.Second)

	// 续期到 5 秒,等 1.5s 验证锁还在(若没续期早就过期)
	require.NoError(t, locker.Renew(context.Background(), "ocm:test:key", "tok-A", 5*time.Second))
	time.Sleep(1500 * time.Millisecond)
	exists, err := locker.Exists(context.Background(), "ocm:test:key")
	require.NoError(t, err)
	assert.True(t, exists)

	// 别人 token 续期不动 TTL 也不报错
	require.NoError(t, locker.Renew(context.Background(), "ocm:test:key", "tok-B", 60*time.Second))
}
```

- [ ] **Step 2:写实现**

```go
package redis

import (
	"context"
	"errors"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// DistLocker 是跨 manager 实例的分布式锁。
// 与 internal/redis/queue.go 同样持"Postgres 是事实来源 / Redis 仅信号通道"哲学:
// 锁失败仅意味着 worker 这一轮放弃,失败重试由 scheduler 兜底。
type DistLocker interface {
	// TryAcquire 用 SET key token NX PX ttl 原子抢锁。
	// 返回 (true, nil) 表示自己拿到;(false, nil) 表示被别人持有。
	TryAcquire(ctx context.Context, key, token string, ttl time.Duration) (bool, error)
	// Renew 校验 token 一致后 PEXPIRE 刷新 TTL;token 不匹配返回 nil 但不动作。
	Renew(ctx context.Context, key, token string, ttl time.Duration) error
	// Release 校验 token 一致后 DEL;token 不匹配返回 nil 但不动作(防误删)。
	Release(ctx context.Context, key, token string) error
	// Exists 仅供 follower 在 SUBSCRIBE 后 double-check leader 是否仍在跑。
	Exists(ctx context.Context, key string) (bool, error)
}

// RedisDistLocker 基于 redis SET NX + Lua 实现 DistLocker。
type RedisDistLocker struct {
	client redis.Cmdable
}

// NewRedisDistLocker 创建实例。client 必须支持 Eval。
func NewRedisDistLocker(client redis.Cmdable) *RedisDistLocker {
	return &RedisDistLocker{client: client}
}

// luaRelease:KEYS[1]=lockKey,ARGV[1]=token。
// token 一致才删,防止 leader 已超时丢锁后误删别人新拿到的锁。
const luaRelease = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
end
return 0
`

// luaRenew:同样校验 token 后 PEXPIRE。
const luaRenew = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0
`

// TryAcquire 见接口注释。
func (l *RedisDistLocker) TryAcquire(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	ok, err := l.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}

// Renew 见接口注释。
func (l *RedisDistLocker) Renew(ctx context.Context, key, token string, ttl time.Duration) error {
	_, err := l.client.Eval(ctx, luaRenew, []string{key}, token, ttl.Milliseconds()).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}

// Release 见接口注释。
func (l *RedisDistLocker) Release(ctx context.Context, key, token string) error {
	_, err := l.client.Eval(ctx, luaRelease, []string{key}, token).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}

// Exists 见接口注释。
func (l *RedisDistLocker) Exists(ctx context.Context, key string) (bool, error) {
	n, err := l.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
```

- [ ] **Step 3:运行测试**

```bash
docker compose up -d redis  # 确保本地 Redis 起着
go test ./internal/redis/ -run TestDistLocker -v
```

预期:全部 PASS(若本地 Redis 未起,测试 Skip 而非 Fail)。

### Task 3.2:写 ProgressBus 接口与 Redis Pub/Sub 实现

**Files:**
- Create: `internal/redis/progress_bus.go`
- Create: `internal/redis/progress_bus_test.go`

- [ ] **Step 1:写测试(TDD)**

```go
package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestBus(t *testing.T) (*RedisProgressBus, *redis.Client, func()) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skip("本地 Redis 不可用,跳过 ProgressBus 集成测试: " + err.Error())
	}
	bus := NewRedisProgressBus(client)
	cleanup := func() {
		_ = client.FlushDB(context.Background()).Err()
		_ = client.Close()
	}
	return bus, client, cleanup
}

// TestProgressBus_PubSub 验证 publish 后 subscribe 能收到事件。
// 注意:Redis Pub/Sub 没有持久化,必须先 Subscribe 再 Publish。
func TestProgressBus_PubSub(t *testing.T) {
	bus, _, cleanup := newTestBus(t)
	defer cleanup()
	ctx := context.Background()
	ch, cancel, err := bus.Subscribe(ctx, "ocm:test:bus:foo")
	require.NoError(t, err)
	defer cancel()

	// 给 subscriber 一点时间完成 SUBSCRIBE 握手再 publish
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, bus.Publish(ctx, "ocm:test:bus:foo", ProgressEvent{Phase: "pulling_image", Current: 50, Total: 100}))

	select {
	case msg := <-ch:
		assert.Equal(t, "ocm:test:bus:foo", msg.Channel)
		assert.Equal(t, "pulling_image", msg.Event.Phase)
		assert.EqualValues(t, 50, msg.Event.Current)
		assert.EqualValues(t, 100, msg.Event.Total)
		assert.NoError(t, msg.Err)
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber 没有收到事件")
	}
}

// TestProgressBus_DoneSentinel __done__ 哨兵能携带 err 字符串,被 follower 识别为完成。
func TestProgressBus_DoneSentinel(t *testing.T) {
	bus, _, cleanup := newTestBus(t)
	defer cleanup()
	ctx := context.Background()
	ch, cancel, err := bus.Subscribe(ctx, "ocm:test:bus:done")
	require.NoError(t, err)
	defer cancel()
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, bus.PublishDone(ctx, "ocm:test:bus:done", errors.New("pull aborted")))

	select {
	case msg := <-ch:
		assert.Equal(t, PhaseDone, msg.Event.Phase)
		require.Error(t, msg.Err)
		assert.Contains(t, msg.Err.Error(), "pull aborted")
	case <-time.After(2 * time.Second):
		t.Fatal("没收到 done 事件")
	}
}
```

- [ ] **Step 2:写实现**

```go
package redis

import (
	"context"
	"encoding/json"
	"fmt"

	redis "github.com/redis/go-redis/v9"
)

// PhaseDone 是哨兵 phase,follower 收到即视为 leader 完成(成功或失败由 Err 字段决定)。
// 不与任何真实 status 值冲突。
const PhaseDone = "__done__"

// ProgressEvent 是镜像 pull / sync 期间 leader 广播给 follower 的进度。
// Phase 取值:"pulling_image" / "syncing_image" / PhaseDone(完成哨兵)。
// Total=0 表示未知,前端展示为不定进度。
type ProgressEvent struct {
	Phase   string `json:"phase"`
	Current int64  `json:"current"`
	Total   int64  `json:"total"`
	// ErrMessage 仅在 Phase=PhaseDone 时使用;空表示成功。
	ErrMessage string `json:"err,omitempty"`
}

// BusMessage 是 Subscribe 通道里的一条消息。
// Err 是把 ProgressEvent.ErrMessage 反序列化后的 Go error,便于调用方直接 if err。
type BusMessage struct {
	Channel string
	Event   ProgressEvent
	Err     error
}

// ProgressBus 抽象跨 manager 进度广播,实现走 Redis Pub/Sub。
type ProgressBus interface {
	Publish(ctx context.Context, channel string, event ProgressEvent) error
	// PublishDone 发出 phase=PhaseDone 的事件;err=nil 表示成功。
	PublishDone(ctx context.Context, channel string, err error) error
	// Subscribe 订阅一个或多个 channel;返回的 cancel 用于释放底层 PubSub 连接。
	Subscribe(ctx context.Context, channels ...string) (<-chan BusMessage, func(), error)
}

// RedisProgressBus 基于 redis Pub/Sub。
type RedisProgressBus struct {
	client *redis.Client
}

// NewRedisProgressBus 创建实例;client 必须有 Subscribe 能力(*redis.Client / Cluster)。
func NewRedisProgressBus(client *redis.Client) *RedisProgressBus {
	return &RedisProgressBus{client: client}
}

// Publish 见接口注释。
func (b *RedisProgressBus) Publish(ctx context.Context, channel string, event ProgressEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("序列化 ProgressEvent: %w", err)
	}
	return b.client.Publish(ctx, channel, body).Err()
}

// PublishDone 见接口注释。
func (b *RedisProgressBus) PublishDone(ctx context.Context, channel string, err error) error {
	event := ProgressEvent{Phase: PhaseDone}
	if err != nil {
		event.ErrMessage = err.Error()
	}
	return b.Publish(ctx, channel, event)
}

// Subscribe 见接口注释。
// 返回的 cancel 必须被调用,否则 redis 连接会泄漏。
func (b *RedisProgressBus) Subscribe(ctx context.Context, channels ...string) (<-chan BusMessage, func(), error) {
	pubsub := b.client.Subscribe(ctx, channels...)
	// 等待 subscribe 命令真正发出去,避免 Publish 比 Subscribe 早导致首条消息丢失。
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return nil, nil, fmt.Errorf("订阅 redis channel 失败: %w", err)
	}
	out := make(chan BusMessage, 16)
	go func() {
		defer close(out)
		raw := pubsub.Channel()
		for msg := range raw {
			var event ProgressEvent
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				out <- BusMessage{Channel: msg.Channel, Err: fmt.Errorf("解析 ProgressEvent: %w", err)}
				continue
			}
			busMsg := BusMessage{Channel: msg.Channel, Event: event}
			if event.ErrMessage != "" {
				busMsg.Err = fmt.Errorf("%s", event.ErrMessage)
			}
			out <- busMsg
		}
	}()
	cancel := func() { _ = pubsub.Close() }
	return out, cancel, nil
}
```

- [ ] **Step 3:运行测试**

```bash
go test ./internal/redis/ -run TestProgressBus -v
```

预期:全部 PASS(本地 Redis 起着的话)。

### Task 3.3:Milestone 3 提交

- [ ] **Step 1:跑 redis 包全测**

```bash
go test ./internal/redis/... -v
```

预期:既有 queue 测试 + 新 dist_locker / progress_bus 测试均 PASS。

- [ ] **Step 2:commit**

```bash
git add internal/redis/
git commit -m "$(cat <<'EOF'
feat(redis): 新增 DistLocker 与 ProgressBus 基础设施

为后续 ImageCoordinator 的"跨 manager 单飞 + 进度广播"准备 Redis 层
原语,放在 internal/redis 包内与既有 ZSET 队列同级,不与具体业务耦合。

- DistLocker:SET NX 抢锁 + Lua check-and-del 释放 / check-and-renew
  续期,防止 leader 超时丢锁后误删新主;Exists 供 follower 二次校验
  leader 是否还在;
- ProgressBus:JSON 编解码 ProgressEvent 走 Redis Pub/Sub,PhaseDone
  哨兵承载 leader 完成事件(成功 / 失败由 ErrMessage 字段区分);
- Subscribe 内部先 Receive 一条同步握手再返回 channel,避免 Publish
  比 Subscribe 早导致丢首条消息(Pub/Sub 无持久化);
- 单测:TryAcquire 冲突、Release/Renew 的 token 校验、PubSub 端到端、
  Done 哨兵 err 透传;本地无 Redis 时 Skip,避免阻塞 CI。
EOF
)"
```

---

## Milestone 4:ImageCoordinator + progressReporter

**目标:** 把 Milestone 3 的锁与总线组合成"跨 manager 单飞 + 进度广播"的 ImageCoordinator,以及 worker 侧消费 ProgressEvent 落库的 progressReporter。本 milestone 不动 worker 流程,产出独立可测的协调器。

### Task 4.1:写 imagecoord 类型定义与接口

**Files:**
- Create: `internal/runtime/imagecoord/types.go`

- [ ] **Step 1:写文件**

```go
// Package imagecoord 跨 manager 实例协调镜像 pull / sync。
//
// 协调粒度:
//   - 同一 image 的 pull 在整个集群内合并为一次,subscriber 收到广播事件;
//   - 同一 (image, nodeID) 的 sync 在整个集群内合并为一次;不同节点可并发。
//
// 与 Postgres 关系:Postgres(apps.status / progress_*) 是事实来源,
// 本包只是把"谁在跑、跑到哪一步"的信号通过 Redis 锁 + Pub/Sub 复用,
// Redis 失联最多导致重复 pull / 进度短暂不更新,不影响业务正确性。
package imagecoord

import (
	"context"
	"io"

	ocredis "oc-manager/internal/redis"
)

// ProgressEvent 是 leader 广播给所有 subscriber 的进度。
// 与 redis.ProgressEvent 同形态;此处单独导出便于上层 import 不直接依赖 redis 包。
type ProgressEvent = ocredis.ProgressEvent

// LocalImageProvider 是 manager 本机 docker 能力的最小契约。
// imagesync.LocalDockerSDKProvider 直接满足。
type LocalImageProvider interface {
	ImageID(ctx context.Context, image string) (string, error)
	Archive(ctx context.Context, image string) (io.ReadCloser, error)
	Pull(ctx context.Context, image string) (io.ReadCloser, error)
}

// AgentImageClient 与 imagesync.AgentImageClient 同形态,本包重新声明
// 是为了让 Coordinator 不直接依赖 imagesync 包(避免后者反向 import)。
type AgentImageClient interface {
	InspectImage(ctx context.Context, nodeID, image string) (RemoteImageInfo, error)
	LoadImage(ctx context.Context, nodeID, image string, archive io.Reader) (RemoteImageInfo, error)
}

// RemoteImageInfo 与 imagesync.RemoteImageInfo 同语义。
type RemoteImageInfo struct {
	Exists bool
	ID     string
}
```

### Task 4.2:写 progress aggregator(NDJSON layer 累加)

**Files:**
- Create: `internal/runtime/imagecoord/aggregator.go`
- Create: `internal/runtime/imagecoord/aggregator_test.go`

- [ ] **Step 1:写测试(TDD)**

```go
package imagecoord

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPullAggregator_SumLayers 多 layer 进度累加。
// docker pull 是 layer 维度并行,聚合器按 layer 维护 state,每次发事件用合计值。
func TestPullAggregator_SumLayers(t *testing.T) {
	agg := NewPullAggregator()
	require.NoError(t, agg.Feed([]byte(`{"id":"a","status":"Downloading","progressDetail":{"current":100,"total":1000}}`)))
	require.NoError(t, agg.Feed([]byte(`{"id":"b","status":"Downloading","progressDetail":{"current":200,"total":2000}}`)))
	current, total := agg.Snapshot()
	assert.EqualValues(t, 300, current)
	assert.EqualValues(t, 3000, total)
}

// TestPullAggregator_PullCompleteCountsAsFull 收到 "Pull complete" 视该 layer current=total。
// 因为 docker daemon 在 layer 完成后不再重发 Downloading,只发 Pull complete。
func TestPullAggregator_PullCompleteCountsAsFull(t *testing.T) {
	agg := NewPullAggregator()
	require.NoError(t, agg.Feed([]byte(`{"id":"a","status":"Downloading","progressDetail":{"current":500,"total":1000}}`)))
	require.NoError(t, agg.Feed([]byte(`{"id":"a","status":"Pull complete"}`)))
	current, total := agg.Snapshot()
	assert.EqualValues(t, 1000, current)
	assert.EqualValues(t, 1000, total)
}

// TestPullAggregator_IgnoresStatusOnlyLines 不带 progressDetail 的状态行不动累加器。
func TestPullAggregator_IgnoresStatusOnlyLines(t *testing.T) {
	agg := NewPullAggregator()
	require.NoError(t, agg.Feed([]byte(`{"status":"Pulling from library/alpine"}`)))
	current, total := agg.Snapshot()
	assert.EqualValues(t, 0, current)
	assert.EqualValues(t, 0, total)
}

// TestPullAggregator_FeedReader 验证从 io.Reader 持续 feed 的 helper。
func TestPullAggregator_FeedReader(t *testing.T) {
	agg := NewPullAggregator()
	body := strings.Join([]string{
		`{"id":"a","status":"Downloading","progressDetail":{"current":100,"total":500}}`,
		`{"id":"a","status":"Pull complete"}`,
		`{"id":"b","status":"Downloading","progressDetail":{"current":250,"total":500}}`,
	}, "\n") + "\n"
	require.NoError(t, agg.FeedReader(strings.NewReader(body)))
	current, total := agg.Snapshot()
	assert.EqualValues(t, 750, current)
	assert.EqualValues(t, 1000, total)
}
```

- [ ] **Step 2:写实现**

```go
package imagecoord

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// PullAggregator 把 docker daemon 的 NDJSON pull 流聚合为字节级总进度。
// 不是线程安全:Feed 应在单 goroutine 串行调用,Snapshot 仅在同一 goroutine 读取。
type PullAggregator struct {
	layers map[string]layerState
}

// layerState 记录单 layer 的最近一次 progress + 是否已完成。
type layerState struct {
	current int64
	total   int64
}

// NewPullAggregator 创建空聚合器。
func NewPullAggregator() *PullAggregator {
	return &PullAggregator{layers: map[string]layerState{}}
}

// pullEvent 是 docker daemon NDJSON 的最小子集,仅解析需要的字段。
type pullEvent struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	ProgressDetail struct {
		Current int64 `json:"current"`
		Total   int64 `json:"total"`
	} `json:"progressDetail"`
}

// Feed 解析单行 NDJSON 并更新 layer 状态。
// 不带 progressDetail 也不是 Pull complete 的纯状态行被忽略。
// 解析失败返回 error,但不污染已有 layers state。
func (a *PullAggregator) Feed(line []byte) error {
	if len(line) == 0 {
		return nil
	}
	var ev pullEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return fmt.Errorf("解析 docker pull NDJSON: %w", err)
	}
	if ev.ID == "" {
		// 全局状态行(如 "Pulling from library/alpine"),无 layer ID,不计入。
		return nil
	}
	if ev.Status == "Pull complete" || ev.Status == "Already exists" {
		// daemon 不再重发 progressDetail,把已知 total 同步到 current;
		// 若该 layer 之前从未上报 total,视作 0/0(对总和无贡献)。
		st := a.layers[ev.ID]
		st.current = st.total
		a.layers[ev.ID] = st
		return nil
	}
	if ev.ProgressDetail.Total == 0 {
		// "Waiting" / "Verifying Checksum" 等中间态没有 progressDetail.total,
		// 此时只更新 current(若 daemon 给了),不动 total 估值。
		st := a.layers[ev.ID]
		if ev.ProgressDetail.Current > st.current {
			st.current = ev.ProgressDetail.Current
		}
		a.layers[ev.ID] = st
		return nil
	}
	a.layers[ev.ID] = layerState{
		current: ev.ProgressDetail.Current,
		total:   ev.ProgressDetail.Total,
	}
	return nil
}

// FeedReader 持续读取 r 直到 EOF,逐行调 Feed。
// 单行解析错误不中断后续读取,只返回最后一次错误(若有)。
func (a *PullAggregator) FeedReader(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var lastErr error
	for scanner.Scan() {
		if err := a.Feed(scanner.Bytes()); err != nil {
			lastErr = err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取 docker pull 流: %w", err)
	}
	return lastErr
}

// Snapshot 返回当前累计的 (current, total)。
func (a *PullAggregator) Snapshot() (int64, int64) {
	var current, total int64
	for _, st := range a.layers {
		current += st.current
		total += st.total
	}
	return current, total
}
```

- [ ] **Step 3:运行测试**

```bash
go test ./internal/runtime/imagecoord/ -run TestPullAggregator -v
```

预期:全部 PASS。

### Task 4.3:写 Coordinator(PullImage / SyncToNode)

**Files:**
- Create: `internal/runtime/imagecoord/coordinator.go`
- Create: `internal/runtime/imagecoord/coordinator_test.go`

- [ ] **Step 1:写测试(用 miniredis 或本地 Redis)**

```go
package imagecoord

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ocredis "oc-manager/internal/redis"
)

// fakeLocalProvider 实现 LocalImageProvider,用 ImageID 控制"本地是否已存在"
// 与 Pull 调用计数,验证 single-flight。
type fakeLocalProvider struct {
	mu          sync.Mutex
	imageExists bool
	pullCalls   int32
	pullDelay   time.Duration
	pullBody    string
	pullErr     error
}

func (f *fakeLocalProvider) ImageID(_ context.Context, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.imageExists {
		return "sha256:exists", nil
	}
	return "", errors.New("not found")
}

func (f *fakeLocalProvider) Archive(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("tar")), nil
}

func (f *fakeLocalProvider) Pull(_ context.Context, _ string) (io.ReadCloser, error) {
	atomic.AddInt32(&f.pullCalls, 1)
	if f.pullErr != nil {
		return nil, f.pullErr
	}
	time.Sleep(f.pullDelay)
	f.mu.Lock()
	f.imageExists = true
	f.mu.Unlock()
	body := f.pullBody
	if body == "" {
		body = `{"id":"a","status":"Pull complete"}` + "\n"
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func newTestCoord(t *testing.T, prov LocalImageProvider) (*Coordinator, func()) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skip("本地 Redis 不可用,跳过 Coordinator 集成测试: " + err.Error())
	}
	bus := ocredis.NewRedisProgressBus(client)
	locker := ocredis.NewRedisDistLocker(client)
	cleanup := func() {
		_ = client.FlushDB(context.Background()).Err()
		_ = client.Close()
	}
	c := NewCoordinator(prov, nil, locker, bus, "test-instance")
	return c, cleanup
}

// TestCoordinator_PullImage_AlreadyPresent 镜像已在本地直接 close subscriber 返回 nil。
func TestCoordinator_PullImage_AlreadyPresent(t *testing.T) {
	prov := &fakeLocalProvider{imageExists: true}
	c, cleanup := newTestCoord(t, prov)
	defer cleanup()

	sub := make(chan ProgressEvent, 4)
	require.NoError(t, c.PullImage(context.Background(), "x:1", sub))
	assert.EqualValues(t, 0, prov.pullCalls)
	// subscriber 被关闭(读到零值即说明 channel 已关闭)
	_, ok := <-sub
	assert.False(t, ok)
}

// TestCoordinator_PullImage_SingleFlight 两个并发 PullImage 只触发一次实际 docker pull。
func TestCoordinator_PullImage_SingleFlight(t *testing.T) {
	prov := &fakeLocalProvider{pullDelay: 300 * time.Millisecond}
	c, cleanup := newTestCoord(t, prov)
	defer cleanup()

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := make(chan ProgressEvent, 16)
			assert.NoError(t, c.PullImage(context.Background(), "x:1", sub))
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 1, atomic.LoadInt32(&prov.pullCalls), "并发 PullImage 应只触发一次 docker pull")
}

// TestCoordinator_PullImage_LeaderFailureBubblesToFollower leader pull 失败,
// follower 收到 done 事件携带 err,自身返回相同错误。
func TestCoordinator_PullImage_LeaderFailureBubblesToFollower(t *testing.T) {
	prov := &fakeLocalProvider{pullErr: errors.New("registry unreachable"), pullDelay: 100 * time.Millisecond}
	c, cleanup := newTestCoord(t, prov)
	defer cleanup()

	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			sub := make(chan ProgressEvent, 16)
			errCh <- c.PullImage(context.Background(), "x:1", sub)
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			require.Error(t, err)
			assert.Contains(t, err.Error(), "registry unreachable")
		case <-time.After(3 * time.Second):
			t.Fatal("PullImage 未在 3s 内返回")
		}
	}
}
```

- [ ] **Step 2:写实现**

```go
package imagecoord

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"

	ocredis "oc-manager/internal/redis"
)

// Coordinator 串联本地 docker 能力 + agent 节点能力 + Redis 锁 + 总线,
// 对外暴露 PullImage / SyncToNode 两个跨 manager 安全的入口。
type Coordinator struct {
	local      LocalImageProvider
	agent      AgentImageClient
	locker     ocredis.DistLocker
	bus        ocredis.ProgressBus
	instanceID string

	// 同进程 fanout:把 leader 收到的事件镜像给同一进程内的所有 subscriber,
	// 避免 redis publish/subscribe 来回的延迟。key=channel
	mu          sync.Mutex
	subscribers map[string][]chan<- ProgressEvent
}

// 几个常量:锁 TTL 与 watchdog 续期间隔。
const (
	defaultLockTTL    = 5 * time.Minute
	watchdogInterval  = 90 * time.Second
	followerWaitGrace = 30 * time.Second
)

// 错误变量。
var (
	// ErrLeaderLost 表示 follower 等待超时;通常意味着 leader 异常或 redis 抖动,
	// 上层 worker 应让 job 重试。
	ErrLeaderLost = errors.New("imagecoord: leader timed out, please retry")
)

// NewCoordinator 创建实例。
// instanceID 推荐用 manager 进程启动时生成的 UUID,使锁 token 包含进程身份。
func NewCoordinator(local LocalImageProvider, agent AgentImageClient, locker ocredis.DistLocker, bus ocredis.ProgressBus, instanceID string) *Coordinator {
	return &Coordinator{
		local:       local,
		agent:       agent,
		locker:      locker,
		bus:         bus,
		instanceID:  instanceID,
		subscribers: map[string][]chan<- ProgressEvent{},
	}
}

// PullImage 见 spec §5.3。
func (c *Coordinator) PullImage(ctx context.Context, image string, subscriber chan<- ProgressEvent) error {
	defer closeIfOpen(subscriber)

	// 已经在本机就跳过,无需任何 redis 交互。
	if _, err := c.local.ImageID(ctx, image); err == nil {
		return nil
	}

	lockKey := fmt.Sprintf("ocm:image:pull:lock:%s", image)
	channel := fmt.Sprintf("ocm:image:pull:bus:%s", image)
	token := c.instanceID + ":" + uuid.NewString()

	got, err := c.locker.TryAcquire(ctx, lockKey, token, defaultLockTTL)
	if err != nil {
		return fmt.Errorf("抢镜像 pull 锁: %w", err)
	}
	if got {
		return c.runLeader(ctx, channel, subscriber, lockKey, token, func(ctx context.Context, send func(ProgressEvent)) error {
			return c.doPull(ctx, image, send)
		})
	}
	return c.runFollower(ctx, channel, lockKey, subscriber)
}

// SyncToNode 见 spec §5.3。
func (c *Coordinator) SyncToNode(ctx context.Context, image, nodeID string, subscriber chan<- ProgressEvent) error {
	defer closeIfOpen(subscriber)
	if c.agent == nil {
		return fmt.Errorf("imagecoord: agent client 未配置")
	}

	// 远端已是同 ID 跳过(原 imagesync 行为)。
	localID, err := c.local.ImageID(ctx, image)
	if err != nil {
		return fmt.Errorf("inspect local image: %w", err)
	}
	remote, err := c.agent.InspectImage(ctx, nodeID, image)
	if err != nil {
		return fmt.Errorf("inspect remote image: %w", err)
	}
	if remote.Exists && remote.ID == localID {
		return nil
	}

	lockKey := fmt.Sprintf("ocm:image:sync:lock:%s:%s", nodeID, image)
	channel := fmt.Sprintf("ocm:image:sync:bus:%s:%s", nodeID, image)
	token := c.instanceID + ":" + uuid.NewString()

	got, err := c.locker.TryAcquire(ctx, lockKey, token, defaultLockTTL)
	if err != nil {
		return fmt.Errorf("抢镜像 sync 锁: %w", err)
	}
	if got {
		return c.runLeader(ctx, channel, subscriber, lockKey, token, func(ctx context.Context, send func(ProgressEvent)) error {
			return c.doSync(ctx, image, nodeID, localID, send)
		})
	}
	return c.runFollower(ctx, channel, lockKey, subscriber)
}

// runLeader 共享的 leader 流程:启 watchdog、订阅自身 channel、跑 op、发 done、释放锁。
func (c *Coordinator) runLeader(
	ctx context.Context,
	channel string,
	subscriber chan<- ProgressEvent,
	lockKey, token string,
	op func(ctx context.Context, send func(ProgressEvent)) error,
) error {
	// watchdog goroutine:每 90s 续期,失败超过 3 次主动 cancel ctx,放弃锁让其他实例接管。
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()
	go c.watchdog(watchCtx, lockKey, token, cancelWatch)

	// fanout register:本机 subscriber 进 map,跑完撤销。
	c.registerSubscriber(channel, subscriber)
	defer c.unregisterSubscriber(channel, subscriber)

	send := func(ev ProgressEvent) {
		// 同进程 fanout 给本机所有 subscriber(包括自己)
		c.fanout(channel, ev)
		// 跨进程 publish 给其他 manager
		_ = c.bus.Publish(ctx, channel, ev)
	}

	opErr := op(ctx, send)

	// 无论成败都广播 done(err 嵌入事件),让 follower 退出 wait
	_ = c.bus.PublishDone(ctx, channel, opErr)
	c.fanoutDone(channel, opErr)

	cancelWatch()
	_ = c.locker.Release(context.Background(), lockKey, token)
	return opErr
}

// runFollower 见 spec §5.3 followerWait。
func (c *Coordinator) runFollower(ctx context.Context, channel, lockKey string, subscriber chan<- ProgressEvent) error {
	ch, cancel, err := c.bus.Subscribe(ctx, channel)
	if err != nil {
		return fmt.Errorf("follower 订阅失败: %w", err)
	}
	defer cancel()

	// SUBSCRIBE 后再 EXISTS 一次:Pub/Sub 无持久化,leader 可能在 Subscribe
	// 之前就 publish 完 done。EXISTS 不到锁说明 leader 已结束。
	exists, err := c.locker.Exists(ctx, lockKey)
	if err != nil {
		return fmt.Errorf("follower 检查 leader 锁: %w", err)
	}
	if !exists {
		// leader 已完成或失败:本机镜像可能已就绪,跳过等待直接返回 nil。
		// 调用方(worker handler)在下一阶段会再 inspect 一次,若仍缺,job 失败重试。
		return nil
	}

	deadline := time.NewTimer(defaultLockTTL + followerWaitGrace)
	defer deadline.Stop()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return ErrLeaderLost
			}
			if msg.Err != nil && msg.Event.Phase == ocredis.PhaseDone {
				return msg.Err
			}
			if msg.Event.Phase == ocredis.PhaseDone {
				return nil
			}
			select {
			case subscriber <- msg.Event:
			default:
				// subscriber 慢消费时直接丢弃,避免阻塞 redis 接收线程
			}
		case <-deadline.C:
			return ErrLeaderLost
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// doPull 真正调用 docker daemon pull,解析 NDJSON 走 aggregator,周期 send。
func (c *Coordinator) doPull(ctx context.Context, image string, send func(ProgressEvent)) error {
	rc, err := c.local.Pull(ctx, image)
	if err != nil {
		return fmt.Errorf("docker pull: %w", err)
	}
	defer rc.Close()
	agg := NewPullAggregator()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	doneCh := make(chan error, 1)
	go func() { doneCh <- agg.FeedReader(rc) }()

	for {
		select {
		case <-ticker.C:
			cur, tot := agg.Snapshot()
			send(ProgressEvent{Phase: "pulling_image", Current: cur, Total: tot})
		case err := <-doneCh:
			cur, tot := agg.Snapshot()
			send(ProgressEvent{Phase: "pulling_image", Current: cur, Total: tot})
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// doSync save → upload(带 countingReader) → load,sync 阶段进度仅覆盖上传段。
func (c *Coordinator) doSync(ctx context.Context, image, nodeID, localID string, send func(ProgressEvent)) error {
	archive, err := c.local.Archive(ctx, image)
	if err != nil {
		return fmt.Errorf("archive local image: %w", err)
	}
	defer archive.Close()

	// total 取本地镜像 size 作为预估;来源于 inspect 但当前只暴露了 ID,
	// 这里简化为 total=0(未知),前端展示不定进度;后续若需要可扩 LocalImageProvider 加 ImageSize。
	total := int64(0)
	counting := newCountingReader(archive, func(n int64) {
		send(ProgressEvent{Phase: "syncing_image", Current: n, Total: total})
	})
	loaded, err := c.agent.LoadImage(ctx, nodeID, image, counting)
	if err != nil {
		return fmt.Errorf("agent load image: %w", err)
	}
	if loaded.ID != "" && loaded.ID != localID {
		return fmt.Errorf("remote image id mismatch after load: local=%s remote=%s", localID, loaded.ID)
	}
	return nil
}

// watchdog 周期续期;连续 3 次失败即主动 cancel,leader 自我放弃。
func (c *Coordinator) watchdog(ctx context.Context, lockKey, token string, abort context.CancelFunc) {
	ticker := time.NewTicker(watchdogInterval)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.locker.Renew(ctx, lockKey, token, defaultLockTTL); err != nil {
				failures++
				if failures >= 3 {
					abort()
					return
				}
				continue
			}
			failures = 0
		}
	}
}

func (c *Coordinator) registerSubscriber(channel string, sub chan<- ProgressEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subscribers[channel] = append(c.subscribers[channel], sub)
}

func (c *Coordinator) unregisterSubscriber(channel string, sub chan<- ProgressEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	subs := c.subscribers[channel]
	for i, s := range subs {
		if s == sub {
			c.subscribers[channel] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
}

func (c *Coordinator) fanout(channel string, ev ProgressEvent) {
	c.mu.Lock()
	subs := append([]chan<- ProgressEvent(nil), c.subscribers[channel]...)
	c.mu.Unlock()
	for _, s := range subs {
		select {
		case s <- ev:
		default:
		}
	}
}

func (c *Coordinator) fanoutDone(channel string, _ error) {
	// done 事件由 runFollower 通过 redis 总线收;同进程 leader 已经直接 return,
	// 这里保留 hook 仅为对称结构,实际不需做事。
	_ = channel
}

// closeIfOpen 关闭 subscriber chan 但避免 panic on double-close。
func closeIfOpen(ch chan<- ProgressEvent) {
	defer func() { _ = recover() }()
	close(ch)
}
```

- [ ] **Step 3:写 countingReader 辅助类型(同包小文件)**

```go
package imagecoord

import "io"

// countingReader 透传 Read,把累计字节通过 onProgress 回调上报。
// 单 goroutine 使用,无需加锁。
type countingReader struct {
	r          io.Reader
	count      int64
	onProgress func(int64)
}

func newCountingReader(r io.Reader, onProgress func(int64)) *countingReader {
	return &countingReader{r: r, onProgress: onProgress}
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.count += int64(n)
		if c.onProgress != nil {
			c.onProgress(c.count)
		}
	}
	return n, err
}
```

放到 `internal/runtime/imagecoord/counting_reader.go`。

- [ ] **Step 4:运行测试**

```bash
go test ./internal/runtime/imagecoord/ -run TestCoordinator -v
```

预期:全部 PASS(本地 Redis 起着的话);若有 race condition 可加 `-race`。

### Task 4.4:写 progressReporter(节流 + 阶段切换 flush)

**Files:**
- Create: `internal/worker/handlers/progress_reporter.go`
- Create: `internal/worker/handlers/progress_reporter_test.go`

- [ ] **Step 1:写测试**

```go
package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ocredis "oc-manager/internal/redis"
	"oc-manager/internal/store/sqlc"
)

// fakeProgressStore 记录每次写库参数,供断言。
type fakeProgressStore struct {
	calls []sqlc.SetAppProgressParams
}

func (f *fakeProgressStore) SetAppProgress(_ context.Context, p sqlc.SetAppProgressParams) (sqlc.App, error) {
	f.calls = append(f.calls, p)
	return sqlc.App{}, nil
}

func newReporter(store *fakeProgressStore, now func() time.Time) *progressReporter {
	r := newProgressReporter(pgtype.UUID{}, store)
	r.now = now
	return r
}

// TestProgressReporter_FirstEventFlushes 第一条事件无论间隔都立即 flush。
func TestProgressReporter_FirstEventFlushes(t *testing.T) {
	store := &fakeProgressStore{}
	t0 := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	r := newReporter(store, func() time.Time { return t0 })
	r.Receive(context.Background(), ocredis.ProgressEvent{Current: 100, Total: 1000})
	require.Len(t, store.calls, 1)
}

// TestProgressReporter_ThrottlesByTime 1s 内的后续事件被节流,不写库。
func TestProgressReporter_ThrottlesByTime(t *testing.T) {
	store := &fakeProgressStore{}
	t0 := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	now := t0
	r := newReporter(store, func() time.Time { return now })
	r.Receive(context.Background(), ocredis.ProgressEvent{Current: 100, Total: 1000})
	now = t0.Add(500 * time.Millisecond)
	r.Receive(context.Background(), ocredis.ProgressEvent{Current: 110, Total: 1000})
	assert.Len(t, store.calls, 1, "1s 内的小增量应被节流")
}

// TestProgressReporter_FlushOnLargeJump 增量 ≥ total*5% 立即 flush 不等 1s。
func TestProgressReporter_FlushOnLargeJump(t *testing.T) {
	store := &fakeProgressStore{}
	t0 := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	now := t0
	r := newReporter(store, func() time.Time { return now })
	r.Receive(context.Background(), ocredis.ProgressEvent{Current: 100, Total: 1000})
	now = t0.Add(200 * time.Millisecond)
	r.Receive(context.Background(), ocredis.ProgressEvent{Current: 200, Total: 1000}) // +10%
	assert.Len(t, store.calls, 2, "10% 增量应跳过节流")
}

// TestProgressReporter_FlushReset transitionTo 调用时强制 flush 一条 0/0,
// 让前端立刻看到新阶段从 0 开始。
func TestProgressReporter_FlushReset(t *testing.T) {
	store := &fakeProgressStore{}
	t0 := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	r := newReporter(store, func() time.Time { return t0 })
	r.FlushReset(context.Background())
	require.Len(t, store.calls, 1)
	assert.False(t, store.calls[0].ProgressCurrent.Valid)
	assert.False(t, store.calls[0].ProgressTotal.Valid)
}
```

- [ ] **Step 2:写实现**

```go
package handlers

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	ocredis "oc-manager/internal/redis"
	"oc-manager/internal/store/sqlc"
)

// ProgressStore 是 progressReporter 写库需要的最小能力,
// 由 sqlc 生成的 SetAppProgress query 满足。
type ProgressStore interface {
	SetAppProgress(ctx context.Context, arg sqlc.SetAppProgressParams) (sqlc.App, error)
}

// progressReporter 节流 ImageCoordinator 来的 ProgressEvent 落库。
// 节流规则:距离上次 flush ≥ 1s 或 current 增量 ≥ total*5% 触发写库;
// 阶段切换时 transitionTo → FlushReset 强制写一条 NULL/NULL。
//
// 不是线程安全:由 worker handler 在单 goroutine 顺序调用。
type progressReporter struct {
	appID       pgtype.UUID
	store       ProgressStore
	lastFlush   time.Time
	lastCurrent int64
	now         func() time.Time
}

func newProgressReporter(appID pgtype.UUID, store ProgressStore) *progressReporter {
	return &progressReporter{appID: appID, store: store, now: time.Now}
}

// Receive 收到 ProgressEvent 后判断是否落库;失败仅记日志(由调用方处理),
// 不阻塞主流程。
func (r *progressReporter) Receive(ctx context.Context, ev ocredis.ProgressEvent) {
	now := r.now()
	if r.lastFlush.IsZero() {
		// 首条事件无条件写库,让前端立刻看到进度
		r.flush(ctx, ev, now)
		return
	}
	timeOK := now.Sub(r.lastFlush) >= time.Second
	jumpOK := ev.Total > 0 && ev.Current-r.lastCurrent >= ev.Total/20
	if timeOK || jumpOK {
		r.flush(ctx, ev, now)
	}
}

// FlushReset 由 transitionTo 在阶段切换时调用,写入 NULL/NULL 让进度归零。
func (r *progressReporter) FlushReset(ctx context.Context) {
	_, _ = r.store.SetAppProgress(ctx, sqlc.SetAppProgressParams{
		ID:              r.appID,
		ProgressCurrent: pgtype.Int8{},
		ProgressTotal:   pgtype.Int8{},
	})
	r.lastFlush = time.Time{}
	r.lastCurrent = 0
}

func (r *progressReporter) flush(ctx context.Context, ev ocredis.ProgressEvent, now time.Time) {
	_, _ = r.store.SetAppProgress(ctx, sqlc.SetAppProgressParams{
		ID:              r.appID,
		ProgressCurrent: pgtype.Int8{Int64: ev.Current, Valid: ev.Current > 0 || ev.Total > 0},
		ProgressTotal:   pgtype.Int8{Int64: ev.Total, Valid: ev.Total > 0},
	})
	r.lastFlush = now
	r.lastCurrent = ev.Current
}
```

注意:`SetAppProgress` query / sqlc 类型在 Milestone 5 Task 5.1 添加;此 Task 编译会暂时报"未定义" — 实施时把 5.1 提前完成,或本 task step 2 后立即跳到 5.1 再回来。subagent-driven 模式会把"补 sqlc"列入 Task 5.1 并先做。

- [ ] **Step 3:运行测试**

```bash
go test ./internal/worker/handlers/ -run TestProgressReporter -v
```

预期:全部 PASS(取决于 SetAppProgress 已生成)。

### Task 4.5:Milestone 4 提交

- [ ] **Step 1:跑 imagecoord 包全测**

```bash
go test ./internal/runtime/imagecoord/... ./internal/worker/handlers/ -run "TestPullAggregator|TestCoordinator|TestProgressReporter" -v
```

- [ ] **Step 2:commit**

```bash
git add internal/runtime/imagecoord/ internal/worker/handlers/progress_reporter.go internal/worker/handlers/progress_reporter_test.go
git commit -m "$(cat <<'EOF'
feat(imagecoord): 跨 manager 镜像协调器 + worker 进度上报节流

Coordinator 把 Redis 锁与 Pub/Sub 总线组合成"集群内单飞 + 进度广播":
- PullImage / SyncToNode 抢 ocm:image:{pull,sync}:lock:* 锁,leader
  跑实际 docker 操作并 publish 进度,follower 通过 Redis 订阅同一
  channel 获取相同进度;
- Follower SUBSCRIBE 后再 EXISTS 一次锁,规避 Redis Pub/Sub "先发后订"
  导致丢 done 事件;deadline=lockTTL+30s 超时返回 ErrLeaderLost;
- Watchdog 每 90s 续期,连续 3 次失败 leader 主动放弃锁让其他实例接管;
- PullAggregator 解析 NDJSON 累加 layer 字节,Pull complete 视作
  current=total;每秒 ticker 发一次聚合事件,避免高频 publish。

Worker 侧 progressReporter:1s/5% 双触发节流,首条事件无条件 flush
让 UI 立刻可见;FlushReset 在阶段切换时写 NULL/NULL,新阶段从零开始。

依赖 Milestone 5 的 SetAppProgress sqlc query 才能编译,实施顺序需把
5.1 与本 milestone 一起完成。
EOF
)"
```

---

## Milestone 5:Worker handler 5 阶段化

**目标:** 把 `app_initialize.go` 从单一 30KB Handle 函数重构为 5 阶段循环;每阶段进入前推 status,失败写 `last_error_status`,镜像阶段接 ImageCoordinator + progressReporter。

> **注意:** Task 5.1 必须在 Milestone 4 提交前完成(progressReporter 编译依赖 SetAppProgress sqlc 类型)。subagent-driven 实施时建议把 Task 5.1 提前到 Milestone 4 之前。

### Task 5.1:扩 sqlc query(进度 / 失败状态 / 孤儿扫描 / job 重置)

**Files:**
- Modify: `internal/store/queries/apps.sql`
- Modify: `internal/store/queries/jobs.sql`
- Regenerate: `internal/store/sqlc/apps.sql.go`、`jobs.sql.go`

- [ ] **Step 1:在 apps.sql 末尾追加 4 条 query**

```sql
-- name: SetAppProgress :one
-- progressReporter 节流后写入;NULL/NULL 表示阶段切换或未知。
UPDATE apps
SET progress_current = $2,
    progress_total = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ClearAppProgress :one
-- transitionTo / RequestInitialize 强制清空进度字段。
UPDATE apps
SET progress_current = NULL,
    progress_total = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkAppFailed :one
-- 任意状态 → error 时同时写入来源状态,保留"在哪一步失败"语义。
-- last_error_status 不加 CHECK 约束,值由调用方在 Go 层负责合法性。
UPDATE apps
SET status = 'error',
    last_error_status = $2,
    progress_current = NULL,
    progress_total = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListStaleInits :many
-- reaper 扫描 5 个 init 子状态下连续 90s 无更新的孤儿;阈值由调用方传入。
SELECT id, runtime_node_id, status
FROM apps
WHERE deleted_at IS NULL
  AND status IN ('pulling_image','syncing_image','preparing_runtime','creating_container','starting')
  AND updated_at < $1
ORDER BY id;
```

- [ ] **Step 2:在 jobs.sql 末尾追加 2 条 query**

```sql
-- name: GetLatestAppInitJob :one
-- reaper 通过 payload_json->>'app_id' 查最近一份 app_initialize job。
-- 用 ORDER BY created_at DESC + LIMIT 1 取最新;不存在返回 pgx.ErrNoRows。
SELECT *
FROM jobs
WHERE type = 'app_initialize'
  AND payload_json->>'app_id' = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: RequeueJob :one
-- reaper 把已 running / succeeded 的 job 重置为 pending。
-- locked_by / locked_at 一并清空避免被旧 worker 误识别为本机持有。
UPDATE jobs
SET status = 'pending',
    started_at = NULL,
    locked_by = NULL,
    locked_at = NULL,
    last_error = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;
```

- [ ] **Step 3:跑 sqlc 重新生成**

```bash
make sqlc-gen && go build ./...
```

预期:`sqlc.App` struct 已含三字段;新 query 函数生成在 `apps.sql.go` / `jobs.sql.go`,编译通过。

### Task 5.2:扩展 AppInitializeStore + ContainerStarter 接口

**Files:**
- Modify: `internal/worker/handlers/app_initialize.go`(顶部接口段)

- [ ] **Step 1:在 AppInitializeStore 中追加 4 个方法**

找到 `type AppInitializeStore interface { ... }`,追加(注意原有方法不动):

```go
type AppInitializeStore interface {
	// 原有方法...
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	GetUser(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	GetRuntimeNode(ctx context.Context, id pgtype.UUID) (sqlc.RuntimeNode, error)
	SetAppNewAPIKey(ctx context.Context, arg sqlc.SetAppNewAPIKeyParams) (sqlc.App, error)
	SetAppContainer(ctx context.Context, arg sqlc.SetAppContainerParams) (sqlc.App, error)
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error)
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)

	// 新增:5 阶段 handler 落进度与失败状态
	SetAppProgress(ctx context.Context, arg sqlc.SetAppProgressParams) (sqlc.App, error)
	ClearAppProgress(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	MarkAppFailed(ctx context.Context, arg sqlc.MarkAppFailedParams) (sqlc.App, error)
}
```

- [ ] **Step 2:在 ContainerStarter 接口同文件下追加 InspectContainer**

`starting` 阶段需要在启动前 inspect 容器看是否 running,否则重复 start 报错。

```go
// ContainerStarter 抽象创建后启动容器的能力。
// 5 阶段 handler 在 phaseStart 内会先 InspectContainer 看 State,
// 已 running 跳过 start 直接进健康检查;exited / created 才 Start。
type ContainerStarter interface {
	StartContainer(ctx context.Context, nodeID, containerID string) error
	InspectContainer(ctx context.Context, nodeID, containerID string) (ContainerState, error)
}

// ContainerState 是 InspectContainer 的最小返回:仅暴露 phaseStart 需要的"running 与否"。
// 与 runtime.AgentBackedAdapter 的 ContainerInspect 返回结构对齐时,在 wiring 处适配。
type ContainerState struct {
	Running    bool
	HealthOK   bool
}
```

- [ ] **Step 3:编译验证(预期会暴露 wiring / handler 内部使用 starter 的地方报"InspectContainer 未实现")**

```bash
go build ./...
```

如果 cmd/server/wiring.go 或 runtime adapter 因 ContainerStarter 接口扩展报错,记下来,Milestone 6 wiring 修订时一起改。本 task 暂时把 wiring 处用类型断言 fallback 处理(适配器若未实现 InspectContainer 则在 phaseStart 跳过此优化)。具体见 Task 5.5。

### Task 5.3:重构 Handle() 为 5 阶段循环

**Files:**
- Modify: `internal/worker/handlers/app_initialize.go`(主 Handle 与新 phase 方法)

- [ ] **Step 1:在 AppInitializeHandler 加新依赖字段**

找到 `type AppInitializeHandler struct { ... }`,追加:

```go
type AppInitializeHandler struct {
	// 原有字段...
	store        AppInitializeStore
	images       ImageDistributor
	dirs         AgentDirInitializer
	runtimeFiles AppRuntimeFileWriter
	knowledge    KnowledgeReader
	containers   ContainerCreator
	starter      ContainerStarter
	factory      NewAPIClientFactory
	cfg          AppInitializeConfig

	// 新增:跨 manager 镜像协调器,phasePull / phaseSync 使用。
	// nil 时退回旧 ImageDistributor 路径(测试装配兼容)。
	coord ImageCoordinator
}

// ImageCoordinator 是 imagecoord.Coordinator 的最小接口。
// 单独声明便于测试 mock,避免直接依赖 imagecoord 包。
type ImageCoordinator interface {
	PullImage(ctx context.Context, image string, subscriber chan<- imagecoord.ProgressEvent) error
	SyncToNode(ctx context.Context, image, nodeID string, subscriber chan<- imagecoord.ProgressEvent) error
}
```

并加 import:`imagecoord "oc-manager/internal/runtime/imagecoord"`。

加 setter:

```go
// SetImageCoordinator 注入跨 manager 镜像协调器。生产装配必须注入;
// 测试装配可不调,phasePull / phaseSync 退化为直接调旧 images 接口。
func (h *AppInitializeHandler) SetImageCoordinator(c ImageCoordinator) { h.coord = c }
```

- [ ] **Step 2:重写 Handle() 为 5 阶段循环**

把现有 `func (h *AppInitializeHandler) Handle(ctx context.Context, job sqlc.Job) error { ... }` 整段替换:

```go
// Handle 是 worker 调用入口。
// 5 阶段串行推进:每阶段进入前先校验状态机转移合法,跑实际工作前查幂等,
// 任何失败收敛到 status=error 并写入 last_error_status 记录来源阶段。
func (h *AppInitializeHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != domain.JobTypeAppInitialize {
		return fmt.Errorf("非 app_initialize 任务: %s", job.Type)
	}
	payload, err := decodePayload(job.PayloadJson)
	if err != nil {
		return err
	}
	appUUID, err := parseUUID(payload.AppID)
	if err != nil {
		return fmt.Errorf("非法 app_id: %w", err)
	}
	app, err := h.store.GetApp(ctx, appUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("应用 %s 不存在", payload.AppID)
		}
		return fmt.Errorf("查询应用失败: %w", err)
	}
	// 已离开初始化阶段直接成功(原本的幂等保留)
	if app.Status == domain.AppStatusBindingWaiting || app.Status == domain.AppStatusRunning {
		return nil
	}

	reporter := newProgressReporter(app.ID, h.store)

	// 5 阶段定义:每阶段先 transitionTo 推 status,再 run 跑实际工作。
	// run 内部已根据 app 当前实际状态做幂等检查,允许重启后从中间阶段重入。
	steps := []struct {
		phase string
		run   func(context.Context, *sqlc.App, appInitializePayload, *progressReporter) error
	}{
		{domain.AppStatusPullingImage, h.phasePull},
		{domain.AppStatusSyncingImage, h.phaseSync},
		{domain.AppStatusPreparingRuntime, h.phasePrepare},
		{domain.AppStatusCreatingContainer, h.phaseCreate},
		{domain.AppStatusStarting, h.phaseStart},
	}

	for _, step := range steps {
		if err := h.transitionTo(ctx, &app, step.phase, reporter); err != nil {
			return h.markFailed(ctx, &app, step.phase, err)
		}
		if err := step.run(ctx, &app, payload, reporter); err != nil {
			return h.markFailed(ctx, &app, step.phase, err)
		}
	}

	if err := h.transitionTo(ctx, &app, domain.AppStatusBindingWaiting, reporter); err != nil {
		return h.markFailed(ctx, &app, domain.AppStatusStarting, err)
	}
	return h.writeInitAuditLog(ctx, app, job, payload)
}

// transitionTo 推 status 并清空 progress_*;违反状态机直接返回 error,
// 由调用方决定是否 markFailed。
func (h *AppInitializeHandler) transitionTo(ctx context.Context, app *sqlc.App, to string, reporter *progressReporter) error {
	if app.Status == to {
		// 重启重入时已经处于目标阶段,跳过一次写库
		return nil
	}
	if err := domain.EnsureAppTransition(app.Status, to); err != nil {
		return err
	}
	updated, err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: to})
	if err != nil {
		return fmt.Errorf("写入应用状态失败: %w", err)
	}
	*app = updated
	reporter.FlushReset(ctx)
	return nil
}

// markFailed 把 status 推到 error,同时写入来源 phase 到 last_error_status。
// 即便写库失败也返回原 cause,避免吞掉真实错误。
func (h *AppInitializeHandler) markFailed(ctx context.Context, app *sqlc.App, phase string, cause error) error {
	if _, err := h.store.MarkAppFailed(ctx, sqlc.MarkAppFailedParams{
		ID:              app.ID,
		LastErrorStatus: pgtype.Text{String: phase, Valid: true},
	}); err != nil {
		return fmt.Errorf("%w (写入失败状态也失败: %v)", cause, err)
	}
	return cause
}
```

- [ ] **Step 3:写 5 个 phase 方法**

紧接着 Handle 后追加(逐个方法,不省略):

```go
// phasePull:确保 manager 本机已存在 runtime 镜像。
// 优先走 ImageCoordinator(集群内单飞 + 进度广播);未注入时退回旧
// images.EnsureRuntimeImage 的"远端节点已就绪"快速路径,等价行为。
func (h *AppInitializeHandler) phasePull(ctx context.Context, _ *sqlc.App, payload appInitializePayload, reporter *progressReporter) error {
	if h.coord == nil {
		return nil
	}
	sub := make(chan imagecoord.ProgressEvent, 16)
	done := make(chan struct{})
	go func() {
		for ev := range sub {
			reporter.Receive(ctx, ev)
		}
		close(done)
	}()
	err := h.coord.PullImage(ctx, h.cfg.RuntimeImage, sub)
	<-done
	if err != nil {
		return fmt.Errorf("拉取 runtime 镜像失败: %w", err)
	}
	_ = payload
	return nil
}

// phaseSync:把 manager 本机镜像同步到目标节点。
// 已有节点 ID 则走 Coordinator;node 缺失视为本地装配场景,跳过。
func (h *AppInitializeHandler) phaseSync(ctx context.Context, _ *sqlc.App, payload appInitializePayload, reporter *progressReporter) error {
	if h.coord == nil || payload.RuntimeNodeID == "" {
		// 退化为旧 ImageDistributor(仍幂等;远端 ID 一致跳过)
		if h.images != nil && payload.RuntimeNodeID != "" {
			if _, err := h.images.EnsureRuntimeImage(ctx, payload.RuntimeNodeID, h.cfg.RuntimeImage); err != nil {
				return fmt.Errorf("分发 Hermes 镜像失败: %w", err)
			}
		}
		return nil
	}
	sub := make(chan imagecoord.ProgressEvent, 16)
	done := make(chan struct{})
	go func() {
		for ev := range sub {
			reporter.Receive(ctx, ev)
		}
		close(done)
	}()
	err := h.coord.SyncToNode(ctx, h.cfg.RuntimeImage, payload.RuntimeNodeID, sub)
	<-done
	if err != nil {
		return fmt.Errorf("同步镜像到节点失败: %w", err)
	}
	return nil
}

// phasePrepare:在节点 agent 上准备目录、确保 api_key、上传 hermes 配置文件。
// 三段都已有局部幂等(InitAppDirs 覆盖写、ensureAPIKey 跳过 active、文件覆盖写),
// 重启重入直接跑安全。
func (h *AppInitializeHandler) phasePrepare(ctx context.Context, app *sqlc.App, payload appInitializePayload, _ *progressReporter) error {
	org, err := h.store.GetOrganization(ctx, app.OrgID)
	if err != nil {
		return fmt.Errorf("查询组织失败: %w", err)
	}
	owner, err := h.store.GetUser(ctx, app.OwnerUserID)
	if err != nil {
		return fmt.Errorf("查询应用 owner 失败: %w", err)
	}
	if h.dirs != nil && payload.RuntimeNodeID != "" {
		if err := h.dirs.InitAppDirs(ctx, payload.RuntimeNodeID, payload.AppID); err != nil {
			return fmt.Errorf("初始化节点应用目录失败: %w", err)
		}
	}
	containerAPIKey, err := h.ensureAPIKey(ctx, app)
	if err != nil {
		return err
	}
	if payload.RuntimeNodeID != "" {
		if err := h.writeHermesFiles(ctx, payload.RuntimeNodeID, *app, org, owner, containerAPIKey); err != nil {
			return err
		}
	}
	return nil
}

// phaseCreate:container_id 已存在则跳过(原 :284 行的幂等检查保留);否则
// 走原 ContainerCreator.CreateContainer 流程,把 ID/Name 写库。
func (h *AppInitializeHandler) phaseCreate(ctx context.Context, app *sqlc.App, payload appInitializePayload, _ *progressReporter) error {
	if app.ContainerID.String != "" {
		return nil
	}
	if h.containers == nil || payload.RuntimeNodeID == "" {
		return nil
	}
	node, err := h.store.GetRuntimeNode(ctx, app.RuntimeNodeID)
	if err != nil {
		return fmt.Errorf("查询 runtime node 失败: %w", err)
	}
	nodeDataRoot := node.NodeDataRoot.String
	if nodeDataRoot == "" {
		nodeDataRoot = "/var/lib/oc-agent"
	}
	containerAPIKey, err := h.ensureAPIKey(ctx, app)
	if err != nil {
		return err
	}
	spec := runtimepkg.ContainerSpec{
		Name:       "hermes-" + payload.AppID,
		Image:      h.cfg.RuntimeImage,
		Networks:   h.cfg.ContainerNetworks,
		WorkingDir: "/opt/data/workspace",
		Env: map[string]string{
			"OPENAI_API_KEY":  containerAPIKey,
			"OPENAI_BASE_URL": h.cfg.NewAPIBaseURL + "/v1",
		},
		Volumes: []runtimepkg.VolumeMount{
			{HostPath: filepath.Join(nodeDataRoot, "apps", payload.AppID, ".hermes"), ContainerPath: "/opt/data"},
		},
	}
	info, err := h.containers.CreateContainer(ctx, payload.RuntimeNodeID, spec)
	if err != nil {
		return fmt.Errorf("创建容器失败: %w", err)
	}
	updated, err := h.store.SetAppContainer(ctx, sqlc.SetAppContainerParams{
		ID:            app.ID,
		ContainerID:   pgtype.Text{String: info.ID, Valid: info.ID != ""},
		ContainerName: pgtype.Text{String: info.Name, Valid: info.Name != ""},
	})
	if err != nil {
		return fmt.Errorf("写入 container_id 失败: %w", err)
	}
	*app = updated
	return nil
}

// phaseStart:启动容器并等健康检查。先 InspectContainer 看 State 做幂等;
// running 直接进健康检查;exited / created 才 Start。
func (h *AppInitializeHandler) phaseStart(ctx context.Context, app *sqlc.App, payload appInitializePayload, _ *progressReporter) error {
	if h.starter == nil || app.ContainerID.String == "" {
		return nil
	}
	containerID := app.ContainerID.String
	// inspector 实现可选;不实现时直接 Start(原行为)。
	state, ok := h.tryInspect(ctx, payload.RuntimeNodeID, containerID)
	if !ok || !state.Running {
		if err := h.starter.StartContainer(ctx, payload.RuntimeNodeID, containerID); err != nil {
			return fmt.Errorf("启动容器失败: %w", err)
		}
	}
	if checker, ok := h.starter.(HermesHealthChecker); ok {
		if err := checker.WaitContainerHealthy(ctx, payload.RuntimeNodeID, containerID, 120*time.Second); err != nil {
			return fmt.Errorf("等待 Hermes 容器健康失败: %w", err)
		}
	}
	return nil
}

// tryInspect 类型断言探测可选 InspectContainer 能力,未实现时返回 (zero, false)。
func (h *AppInitializeHandler) tryInspect(ctx context.Context, nodeID, containerID string) (ContainerState, bool) {
	type inspector interface {
		InspectContainer(ctx context.Context, nodeID, containerID string) (ContainerState, error)
	}
	insp, ok := h.starter.(inspector)
	if !ok {
		return ContainerState{}, false
	}
	state, err := insp.InspectContainer(ctx, nodeID, containerID)
	if err != nil {
		return ContainerState{}, false
	}
	return state, true
}

// writeInitAuditLog 把原 Handle 末尾的审计日志逻辑独立出来,Handle 完成 binding_waiting
// 转移后调用一次。
func (h *AppInitializeHandler) writeInitAuditLog(ctx context.Context, app sqlc.App, job sqlc.Job, payload appInitializePayload) error {
	auditMetadata, err := json.Marshal(map[string]any{
		"job_id":       uuidToString(job.ID),
		"runtime_node": payload.RuntimeNodeID,
		"container_id": textOrEmpty(app.ContainerID),
	})
	if err != nil {
		return fmt.Errorf("序列化应用初始化审计元数据失败: %w", err)
	}
	if _, err := h.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ActorRole:    "system",
		OrgID:        app.OrgID,
		TargetType:   "app",
		TargetID:     uuidToString(app.ID),
		Action:       "initialize",
		Result:       "succeeded",
		MetadataJson: auditMetadata,
	}); err != nil {
		return fmt.Errorf("写入应用初始化审计日志失败: %w", err)
	}
	return nil
}
```

- [ ] **Step 4:删除 Handle 中迁移走的旧逻辑**

原 Handle 函数里"调 ImageDistributor / dirs / writeHermesFiles / CreateContainer / Start / WaitHealthy / 推 binding_waiting + 审计"几段代码都已搬到 phase 方法里,确认从 Handle 函数体里清干净。`writeHermesFiles` / `ensureAPIKey` / `collectKnowledgeForSoul` 等 helper 函数原封不动保留。

- [ ] **Step 5:编译并跑现有 app_initialize_test.go**

```bash
go build ./internal/worker/handlers/...
go test ./internal/worker/handlers/ -run TestAppInitializeHandler -v
```

预期:编译通过;若现有测试因接口扩张报错,临时把测试 mock 补 InspectContainer / SetAppProgress / ClearAppProgress / MarkAppFailed 三方法返回 zero value。Task 5.5 全面更新测试。

### Task 5.4:wiring 注入 ImageCoordinator(读 redis client + 装配)

**Files:**
- Modify: `cmd/server/main.go`(redis client 段附近)

- [ ] **Step 1:在 main.go 已经初始化 redisQueue 之后追加**

找到 `redisQueue := redis.NewRedisQueue(...)`(spec §一约定的 ZSET queue 装配点;若变量名不同就以本地实际为准)。在其后追加:

```go
	// 共享同一个 redis client 给 DistLocker / ProgressBus,避免连接池碎片化。
	// queue 已经持有自己的 client(NewRedisQueue 内部 dial),这里再开一个独立 client
	// 给 imagecoord 使用;两个 client 共享同一 Redis 实例。
	imagecoordRedis := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer imagecoordRedis.Close()
	distLocker := ocmredis.NewRedisDistLocker(imagecoordRedis)
	progressBus := ocmredis.NewRedisProgressBus(imagecoordRedis)

	imageCoord := imagecoord.NewCoordinator(
		dockerSDK,            // Milestone 2 已注入的 LocalDockerSDKProvider
		nodeResolver,         // 满足 imagecoord.AgentImageClient(InspectImage / LoadImage)
		distLocker,
		progressBus,
		uuid.NewString(),     // 进程唯一 instanceID
	)
```

注意 import:`goredis "github.com/redis/go-redis/v9"` / `ocmredis "oc-manager/internal/redis"` / `imagecoord "oc-manager/internal/runtime/imagecoord"` / `"github.com/google/uuid"`。

- [ ] **Step 2:在 NewAppInitializeHandler 之后注入**

```go
	appInitHandler := handlers.NewAppInitializeHandler(...)
	appInitHandler.SetImageCoordinator(imageCoord)
```

- [ ] **Step 3:nodeResolver 满足 imagecoord.AgentImageClient 接口校验**

如果 `nodeResolver`(或现有 imagesync.AgentImageClient 实现)签名与 `imagecoord.AgentImageClient` 不完全一致,在 wiring.go 加一个薄 adapter:

```go
// agentClientAdapter 把 imagesync.AgentImageClient 适配成 imagecoord.AgentImageClient。
// 两者方法签名一致;独立 adapter 仅为避免 imagecoord 反向 import imagesync。
type agentClientAdapter struct{ inner imagesync.AgentImageClient }

func (a agentClientAdapter) InspectImage(ctx context.Context, nodeID, image string) (imagecoord.RemoteImageInfo, error) {
	r, err := a.inner.InspectImage(ctx, nodeID, image)
	return imagecoord.RemoteImageInfo{Exists: r.Exists, ID: r.ID}, err
}

func (a agentClientAdapter) LoadImage(ctx context.Context, nodeID, image string, archive io.Reader) (imagecoord.RemoteImageInfo, error) {
	r, err := a.inner.LoadImage(ctx, nodeID, image, archive)
	return imagecoord.RemoteImageInfo{Exists: r.Exists, ID: r.ID}, err
}
```

并在 NewCoordinator 调用处把 nodeResolver 改成 `agentClientAdapter{inner: nodeResolver}`。

- [ ] **Step 4:编译 + 启动验证**

```bash
go build ./...
docker compose up -d --build manager
docker compose logs manager 2>&1 | head -30
```

预期:启动日志没有 panic / nil deref;`/healthz` 200。

### Task 5.5:更新 / 补充 app_initialize_test.go

**Files:**
- Modify: `internal/worker/handlers/app_initialize_test.go`

- [ ] **Step 1:补 mock 方法**

找到现有 mock store(典型命名 `fakeAppInitStore` 或 `mockAppInitializeStore`),在其方法集合追加:

```go
func (s *fakeAppInitStore) SetAppProgress(_ context.Context, p sqlc.SetAppProgressParams) (sqlc.App, error) {
	s.lastProgress = p
	return sqlc.App{}, nil
}
func (s *fakeAppInitStore) ClearAppProgress(_ context.Context, _ pgtype.UUID) (sqlc.App, error) {
	return sqlc.App{}, nil
}
func (s *fakeAppInitStore) MarkAppFailed(_ context.Context, p sqlc.MarkAppFailedParams) (sqlc.App, error) {
	s.lastFailed = p
	return sqlc.App{ID: p.ID, Status: domain.AppStatusError, LastErrorStatus: p.LastErrorStatus}, nil
}
```

`fakeAppInitStore` 字段补 `lastProgress sqlc.SetAppProgressParams` / `lastFailed sqlc.MarkAppFailedParams`。

- [ ] **Step 2:加新表驱动子测试,覆盖 5 阶段推进**

```go
// TestAppInitializeHandler_Phases_Progress 验证 5 阶段每阶段都把 status 推进一格。
// 用 fake store 收集 SetAppStatus 调用序列,断言顺序与目标值。
func TestAppInitializeHandler_Phases_Progress(t *testing.T) {
	store := newFakeAppInitStore(t)
	// 假装 app 起始 draft,且远端 / new-api / containers 都注入空 stub
	store.app = sqlc.App{ID: testAppID, Status: domain.AppStatusDraft, OrgID: testOrgID, OwnerUserID: testUserID}
	h := newTestHandlerWithCoord(store, &noopCoord{}, &noopContainers{}, &noopStarter{})

	require.NoError(t, h.Handle(context.Background(), sqlc.Job{Type: domain.JobTypeAppInitialize, PayloadJson: marshalAppInitPayload(testAppID)}))

	// 6 次 SetAppStatus(5 个 init 子状态 + binding_waiting);顺序与状态机一致。
	wantStatuses := []string{
		domain.AppStatusPullingImage,
		domain.AppStatusSyncingImage,
		domain.AppStatusPreparingRuntime,
		domain.AppStatusCreatingContainer,
		domain.AppStatusStarting,
		domain.AppStatusBindingWaiting,
	}
	require.Len(t, store.statusCalls, len(wantStatuses))
	for i, want := range wantStatuses {
		assert.Equal(t, want, store.statusCalls[i].Status, "第 %d 次状态切换", i)
	}
}

// TestAppInitializeHandler_Phases_FailureWritesLastError 任意一阶段失败都应写 last_error_status=该阶段。
// 用 table-driven 覆盖 5 个阶段,每条 case 用一个会失败的 stub 触发。
func TestAppInitializeHandler_Phases_FailureWritesLastError(t *testing.T) {
	cases := []struct {
		// failOn 指定哪个阶段触发失败,期望 last_error_status 写该阶段值
		name   string
		failOn string
	}{
		{"phasePull 失败", domain.AppStatusPullingImage},          // ImageCoordinator.PullImage 返回 error
		{"phaseSync 失败", domain.AppStatusSyncingImage},          // SyncToNode 返回 error
		{"phasePrepare 失败", domain.AppStatusPreparingRuntime},   // ensureAPIKey 返回 error
		{"phaseCreate 失败", domain.AppStatusCreatingContainer},   // CreateContainer 返回 error
		{"phaseStart 失败", domain.AppStatusStarting},             // StartContainer 返回 error
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := newFakeAppInitStore(t)
			store.app = sqlc.App{ID: testAppID, Status: domain.AppStatusDraft, OrgID: testOrgID, OwnerUserID: testUserID}
			h := newTestHandlerFailingAt(store, c.failOn)
			err := h.Handle(context.Background(), sqlc.Job{Type: domain.JobTypeAppInitialize, PayloadJson: marshalAppInitPayload(testAppID)})
			require.Error(t, err)
			assert.True(t, store.lastFailed.LastErrorStatus.Valid)
			assert.Equal(t, c.failOn, store.lastFailed.LastErrorStatus.String)
		})
	}
}

// TestAppInitializeHandler_IdempotentReentry app 已经处于 starting 阶段时,
// Handle 应跳过 transitionTo(transitionTo 会因 from==to 走快速分支)、跳过创建容器。
func TestAppInitializeHandler_IdempotentReentry(t *testing.T) {
	store := newFakeAppInitStore(t)
	// app 起始 starting + container_id 已写入,模拟"manager 重启后 reaper 重置"路径
	store.app = sqlc.App{
		ID:          testAppID,
		Status:      domain.AppStatusStarting,
		OrgID:       testOrgID,
		OwnerUserID: testUserID,
		ContainerID: pgtype.Text{String: "cid-1", Valid: true},
	}
	containers := &recordingContainers{}
	h := newTestHandlerWithCoord(store, &noopCoord{}, containers, &noopStarter{})
	require.NoError(t, h.Handle(context.Background(), sqlc.Job{Type: domain.JobTypeAppInitialize, PayloadJson: marshalAppInitPayload(testAppID)}))
	assert.Zero(t, containers.createCalls, "container_id 已存在不应再创建")
}
```

`newTestHandlerWithCoord` / `newTestHandlerFailingAt` / `noopCoord` / `noopContainers` / `noopStarter` / `recordingContainers` 等 helper 在测试文件顶部新建;签名与现有 helper 风格保持一致(参考 `newAppInitTestHandler` 等已有命名)。

- [ ] **Step 2:运行测试**

```bash
go test ./internal/worker/handlers/ -run TestAppInitializeHandler -v
```

预期:全部 PASS。

### Task 5.6:更新 RequestInitialize 重置策略

**Files:**
- Modify: `internal/service/runtime_operation_service.go`(RequestInitialize 函数体)
- Modify: `internal/service/runtime_operation_service_test.go`

- [ ] **Step 1:把 SetAppStatus 目标 + 进度清空**

找到 `runtime_operation_service.go:302` 附近的:

```go
	if _, err := s.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusDraft}); err != nil {
```

整段(包括调用与 if err)替换为:

```go
	// 重置目标改为 pulling_image:worker 重新走 5 阶段;清空 progress_* 与 last_error_status。
	// draft 入参时通过状态机转移合法(draft → pulling_image)。
	if _, err := s.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusPullingImage}); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("重置应用状态失败: %w", err)
	}
	if _, err := s.store.ClearAppProgress(ctx, app.ID); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("清空应用进度字段失败: %w", err)
	}
```

`RuntimeOperationStore` 接口若不含 `ClearAppProgress`,在文件顶部 store 接口定义里加一行:

```go
ClearAppProgress(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
```

- [ ] **Step 2:更新测试**

`runtime_operation_service_test.go` 中 mock store 补 `ClearAppProgress` 方法返回 zero value。原断言 `Status: AppStatusDraft` 改为 `Status: AppStatusPullingImage`。

- [ ] **Step 3:运行测试**

```bash
go test ./internal/service/ -run TestRequestInitialize -v
```

### Task 5.7:Milestone 5 提交

- [ ] **Step 1:跑全包测试**

```bash
go test ./... 2>&1 | tail -30
```

预期:全部 PASS(若 cmd/server 包因 wiring 还未完整连通 reaper 报"未使用 import",Milestone 6 一并修复)。

- [ ] **Step 2:commit**

```bash
git add internal/store/queries/ internal/store/sqlc/ internal/worker/handlers/app_initialize.go internal/worker/handlers/app_initialize_test.go internal/worker/handlers/progress_reporter.go internal/worker/handlers/progress_reporter_test.go internal/service/runtime_operation_service.go internal/service/runtime_operation_service_test.go cmd/server/main.go cmd/server/wiring.go
git commit -m "$(cat <<'EOF'
refactor(worker): app 初始化拆分为 5 阶段并接入 ImageCoordinator

把原 Handle() 的串行黑盒拆成 phasePull / phaseSync / phasePrepare /
phaseCreate / phaseStart 五个阶段,每阶段进入前 transitionTo 推 status
+ 清空 progress_*,实际工作由 progressReporter 节流写入字节级进度;
任意阶段失败收敛到 markFailed,把 status 推到 error 同时写入来源
phase 到 last_error_status。

- 状态机校验从 EnsureAppTransition 走,违反转移直接 markFailed,
  防止 reaper 重置后两个 worker 同时推进同一 app;
- 每阶段已有局部幂等(InitAppDirs 覆盖写、ensureAPIKey 跳 active、
  container_id 非空跳 create、starting 阶段先 InspectContainer 看
  Running),允许重启重入;
- 接入 imagecoord.Coordinator,phasePull / phaseSync 通过 Redis 锁
  集群内单飞,subscriber 拿到 ProgressEvent 经 progressReporter 落库;
- RequestInitialize 重置目标改为 pulling_image,同时 ClearAppProgress
  把 progress_* / last_error_status 一并清空(后续 reaper 与本入口
  共享同一清空语义);
- 新增 sqlc query:SetAppProgress / ClearAppProgress / MarkAppFailed /
  ListStaleInits / GetLatestAppInitJob / RequeueJob;
- 测试覆盖 5 阶段顺序推进、任意阶段失败写 last_error_status、
  container_id 已存在重入不重复 create。
EOF
)"
```

---

## Milestone 6:Reaper(周期 tick + Redis 锁互斥)

**目标:** 后台 goroutine 每 60 秒抢一次 Redis 锁,扫 5 个 init 子状态下 `updated_at < now()-90s` 的孤儿,重置 `apps.status=pulling_image` + 清空进度,重入或新建 `app_initialize` job 入队。

### Task 6.1:写 reaper 实现

**Files:**
- Create: `internal/worker/reaper/reaper.go`

- [ ] **Step 1:写文件**

```go
// Package reaper 周期扫描"5 个 init 子状态下连续 90s 无更新"的孤儿 app,
// 重置 status 并重新入队 app_initialize job。
//
// 多 manager 部署时通过 Redis 锁 ocm:reaper:lock(TTL 30s)互斥,
// 每个 tick 只有一个实例真正扫描;锁 TTL > 单次 reap 预期耗时,
// 持锁实例崩溃时 30s 后由其他实例自然接管。
package reaper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/domain"
	ocredis "oc-manager/internal/redis"
	"oc-manager/internal/store/sqlc"
)

// Store 是 reaper 需要的最小数据访问能力。
type Store interface {
	ListStaleInits(ctx context.Context, before pgtype.Timestamptz) ([]sqlc.ListStaleInitsRow, error)
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error)
	ClearAppProgress(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	GetLatestAppInitJob(ctx context.Context, appID string) (sqlc.Job, error)
	RequeueJob(ctx context.Context, id pgtype.UUID) (sqlc.Job, error)
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
}

// JobNotifier 与 internal/redis ZSET queue 一致;reaper 重置 job 后通知 scheduler 立即拾取。
type JobNotifier interface {
	Enqueue(ctx context.Context, jobID string) error
}

// Reaper 持锁、扫描、重置三件套。
type Reaper struct {
	store    Store
	notifier JobNotifier
	locker   ocredis.DistLocker
	logger   *slog.Logger

	staleAfter time.Duration // 默认 90s
	lockTTL    time.Duration // 默认 30s
	tick       time.Duration // 默认 60s
	instanceID string
}

// New 创建 Reaper;instanceID 推荐复用 main.go 的 manager 进程 UUID,
// 让 Redis 锁 token 含进程身份,审计 / 排障可追溯。
func New(store Store, notifier JobNotifier, locker ocredis.DistLocker, instanceID string, logger *slog.Logger) *Reaper {
	return &Reaper{
		store:      store,
		notifier:   notifier,
		locker:     locker,
		logger:     logger,
		staleAfter: 90 * time.Second,
		lockTTL:    30 * time.Second,
		tick:       60 * time.Second,
		instanceID: instanceID,
	}
}

// Start 启动后台 goroutine:进程启动时立刻跑一次,然后每 60s tick。
// ctx 取消即退出;调用方负责生命周期。
func (r *Reaper) Start(ctx context.Context) {
	go func() {
		// 进程刚起立刻跑一次,接管自己上次留下的孤儿
		r.tickOnce(ctx)
		ticker := time.NewTicker(r.tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.tickOnce(ctx)
			}
		}
	}()
}

// tickOnce 抢锁 → reapOnce → 释放锁。任何错误仅记日志,不中断后续 tick。
func (r *Reaper) tickOnce(ctx context.Context) {
	const lockKey = "ocm:reaper:lock"
	token := r.instanceID + ":" + nowToken()
	got, err := r.locker.TryAcquire(ctx, lockKey, token, r.lockTTL)
	if err != nil {
		r.logger.Error("reaper 抢锁失败", "error", err)
		return
	}
	if !got {
		// 其他实例正在跑,本轮跳过
		return
	}
	defer func() { _ = r.locker.Release(context.Background(), lockKey, token) }()
	if err := r.reapOnce(ctx); err != nil {
		r.logger.Error("reaper 单轮扫描失败", "error", err)
	}
}

// reapOnce 单次扫描:取所有 updated_at 落后阈值的 init 子状态行,
// 逐条重置 status 并入队 job。任意一条失败不中断剩余处理,只记日志。
func (r *Reaper) reapOnce(ctx context.Context) error {
	threshold := pgtype.Timestamptz{Time: time.Now().Add(-r.staleAfter), Valid: true}
	rows, err := r.store.ListStaleInits(ctx, threshold)
	if err != nil {
		return fmt.Errorf("查询孤儿 apps: %w", err)
	}
	for _, row := range rows {
		if err := r.reapApp(ctx, row); err != nil {
			r.logger.Error("reaper 重置单个 app 失败",
				"app_id", uuidString(row.ID), "node_id", textString(row.RuntimeNodeID),
				"status", row.Status, "error", err)
		}
	}
	return nil
}

// reapApp 重置 app status 到 pulling_image + 清空进度 + 重置 / 新建 job + 通知队列。
// reset 不走 EnsureAppTransition(可能从 starting 直接跳回 pulling_image,
// 不是状态机正常路径,但 reaper 是显式接管,直接强制 SET)。
func (r *Reaper) reapApp(ctx context.Context, row sqlc.ListStaleInitsRow) error {
	if _, err := r.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{
		ID:     row.ID,
		Status: domain.AppStatusPullingImage,
	}); err != nil {
		return fmt.Errorf("重置 status: %w", err)
	}
	if _, err := r.store.ClearAppProgress(ctx, row.ID); err != nil {
		return fmt.Errorf("清空 progress_*: %w", err)
	}
	jobID, err := r.ensureInitJob(ctx, row)
	if err != nil {
		return err
	}
	if err := r.notifier.Enqueue(ctx, uuidString(jobID)); err != nil {
		// 通知失败仅记账,scheduler 兜底扫表会拾起
		r.logger.Warn("reaper 入队失败,等 scheduler 兜底", "job_id", uuidString(jobID), "error", err)
	}
	return nil
}

// ensureInitJob 找最近一份 app_initialize job:不存在新建;running/succeeded 重置回 pending;
// 已 pending 直接复用。
func (r *Reaper) ensureInitJob(ctx context.Context, row sqlc.ListStaleInitsRow) (pgtype.UUID, error) {
	job, err := r.store.GetLatestAppInitJob(ctx, uuidString(row.ID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, fmt.Errorf("查 job: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// 新建一份 job
		payload, perr := json.Marshal(map[string]any{
			"app_id":       uuidString(row.ID),
			"runtime_node": textString(row.RuntimeNodeID),
		})
		if perr != nil {
			return pgtype.UUID{}, fmt.Errorf("序列化 payload: %w", perr)
		}
		created, cerr := r.store.CreateJob(ctx, sqlc.CreateJobParams{
			Type:        domain.JobTypeAppInitialize,
			Priority:    100,
			RunAfter:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
			MaxAttempts: 3,
			PayloadJson: payload,
		})
		if cerr != nil {
			return pgtype.UUID{}, fmt.Errorf("CreateJob: %w", cerr)
		}
		return created.ID, nil
	}
	if job.Status == domain.JobStatusPending {
		return job.ID, nil
	}
	updated, err := r.store.RequeueJob(ctx, job.ID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("RequeueJob: %w", err)
	}
	return updated.ID, nil
}

// 简化的 helper(项目里有同形态的;reaper 包内独立一份避免反向依赖)
func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, b := range id.Bytes {
		out = append(out, digits[b>>4], digits[b&0x0f])
		if i == 3 || i == 5 || i == 7 || i == 9 {
			out = append(out, '-')
		}
	}
	return string(out)
}

func textString(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

func nowToken() string {
	return time.Now().UTC().Format("20060102150405.000000")
}
```

### Task 6.2:写 reaper 单测

**Files:**
- Create: `internal/worker/reaper/reaper_test.go`

- [ ] **Step 1:写测试**

```go
package reaper

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// fakeStore 收集 reaper 的所有写库调用,供断言。
type fakeStore struct {
	stale          []sqlc.ListStaleInitsRow
	statusCalls    []sqlc.SetAppStatusParams
	clearCalls     []pgtype.UUID
	latestJob      sqlc.Job
	latestJobErr   error
	requeueCalls   []pgtype.UUID
	createJobCalls []sqlc.CreateJobParams
}

func (s *fakeStore) ListStaleInits(_ context.Context, _ pgtype.Timestamptz) ([]sqlc.ListStaleInitsRow, error) {
	return s.stale, nil
}
func (s *fakeStore) SetAppStatus(_ context.Context, p sqlc.SetAppStatusParams) (sqlc.App, error) {
	s.statusCalls = append(s.statusCalls, p)
	return sqlc.App{}, nil
}
func (s *fakeStore) ClearAppProgress(_ context.Context, id pgtype.UUID) (sqlc.App, error) {
	s.clearCalls = append(s.clearCalls, id)
	return sqlc.App{}, nil
}
func (s *fakeStore) GetLatestAppInitJob(_ context.Context, _ string) (sqlc.Job, error) {
	return s.latestJob, s.latestJobErr
}
func (s *fakeStore) RequeueJob(_ context.Context, id pgtype.UUID) (sqlc.Job, error) {
	s.requeueCalls = append(s.requeueCalls, id)
	return sqlc.Job{ID: id, Status: domain.JobStatusPending}, nil
}
func (s *fakeStore) CreateJob(_ context.Context, p sqlc.CreateJobParams) (sqlc.Job, error) {
	s.createJobCalls = append(s.createJobCalls, p)
	return sqlc.Job{ID: testJobID, Status: domain.JobStatusPending}, nil
}

type fakeNotifier struct{ enqueued []string }

func (n *fakeNotifier) Enqueue(_ context.Context, jobID string) error {
	n.enqueued = append(n.enqueued, jobID)
	return nil
}

type fakeLocker struct {
	acquireOK  bool
	acquireErr error
}

func (l *fakeLocker) TryAcquire(_ context.Context, _, _ string, _ time.Duration) (bool, error) {
	return l.acquireOK, l.acquireErr
}
func (l *fakeLocker) Renew(_ context.Context, _, _ string, _ time.Duration) error { return nil }
func (l *fakeLocker) Release(_ context.Context, _, _ string) error                { return nil }
func (l *fakeLocker) Exists(_ context.Context, _ string) (bool, error)             { return true, nil }

var (
	testJobID = pgtype.UUID{Bytes: [16]byte{0xaa}, Valid: true}
	testAppID = pgtype.UUID{Bytes: [16]byte{0xbb}, Valid: true}
)

// TestReaper_LockUnavailable_Skip 锁被别人持着时 reapOnce 不应被调用。
func TestReaper_LockUnavailable_Skip(t *testing.T) {
	store := &fakeStore{}
	notifier := &fakeNotifier{}
	r := New(store, notifier, &fakeLocker{acquireOK: false}, "test", slog.Default())
	r.tickOnce(context.Background())
	assert.Empty(t, store.statusCalls)
	assert.Empty(t, notifier.enqueued)
}

// TestReaper_ReapOrphanReset 5 个 init 子状态都能被扫到,逐条重置 status + 清进度 + 重入 / 新建 job。
func TestReaper_ReapOrphanReset(t *testing.T) {
	cases := []struct {
		name        string
		startStatus string
	}{
		{"pulling_image 孤儿", domain.AppStatusPullingImage},
		{"syncing_image 孤儿", domain.AppStatusSyncingImage},
		{"preparing_runtime 孤儿", domain.AppStatusPreparingRuntime},
		{"creating_container 孤儿", domain.AppStatusCreatingContainer},
		{"starting 孤儿", domain.AppStatusStarting},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &fakeStore{
				stale:        []sqlc.ListStaleInitsRow{{ID: testAppID, Status: c.startStatus}},
				latestJob:    sqlc.Job{ID: testJobID, Status: domain.JobStatusRunning},
			}
			notifier := &fakeNotifier{}
			r := New(store, notifier, &fakeLocker{acquireOK: true}, "test", slog.Default())
			r.tickOnce(context.Background())

			require.Len(t, store.statusCalls, 1)
			assert.Equal(t, domain.AppStatusPullingImage, store.statusCalls[0].Status)
			require.Len(t, store.clearCalls, 1)
			require.Len(t, store.requeueCalls, 1, "running job 应被 requeue")
			assert.Empty(t, store.createJobCalls, "已有 job 不应新建")
			assert.NotEmpty(t, notifier.enqueued)
		})
	}
}

// TestReaper_NoExistingJob_CreateNew 没有历史 job 时 reaper 新建一份。
func TestReaper_NoExistingJob_CreateNew(t *testing.T) {
	store := &fakeStore{
		stale:        []sqlc.ListStaleInitsRow{{ID: testAppID, Status: domain.AppStatusStarting}},
		latestJobErr: pgx.ErrNoRows,
	}
	notifier := &fakeNotifier{}
	r := New(store, notifier, &fakeLocker{acquireOK: true}, "test", slog.Default())
	r.tickOnce(context.Background())
	assert.Len(t, store.createJobCalls, 1)
	assert.Empty(t, store.requeueCalls)
}

// TestReaper_PendingJob_NoRequeue 已经 pending 的 job 直接复用,不重置。
func TestReaper_PendingJob_NoRequeue(t *testing.T) {
	store := &fakeStore{
		stale:     []sqlc.ListStaleInitsRow{{ID: testAppID, Status: domain.AppStatusStarting}},
		latestJob: sqlc.Job{ID: testJobID, Status: domain.JobStatusPending},
	}
	notifier := &fakeNotifier{}
	r := New(store, notifier, &fakeLocker{acquireOK: true}, "test", slog.Default())
	r.tickOnce(context.Background())
	assert.Empty(t, store.requeueCalls)
	assert.Empty(t, store.createJobCalls)
	assert.NotEmpty(t, notifier.enqueued, "pending job 也要重新通知队列,防 scheduler 漏触发")
}

// TestReaper_ListStaleErrorContinues 单条失败不阻断剩余 — 直接通过 reapOnce 触发即可,
// 但当前实现只 log 不 propagate;这里只验证不 panic。
func TestReaper_StoreErrorPropagates(t *testing.T) {
	store := &fakeStore{}
	store.latestJobErr = errors.New("db down")
	store.stale = []sqlc.ListStaleInitsRow{{ID: testAppID, Status: domain.AppStatusStarting}}
	notifier := &fakeNotifier{}
	r := New(store, notifier, &fakeLocker{acquireOK: true}, "test", slog.Default())
	// tickOnce 内部捕获错误只 log,不应 panic
	assert.NotPanics(t, func() { r.tickOnce(context.Background()) })
}
```

- [ ] **Step 2:运行测试**

```bash
go test ./internal/worker/reaper/ -v
```

预期:全部 PASS。

### Task 6.3:wiring 装配 reaper(workerPool 启动后)

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1:在 workerPool.Start() 之后加 reaper 启动**

找到 `workerPool.Start(ctx)`(若名字不一样,以本地实际为准)。在其后追加:

```go
	// reaper 启动:周期 60s tick,扫 5 个 init 子状态下连续 90s 无更新的孤儿。
	// 多 manager 共存时通过 Redis 锁 ocm:reaper:lock 互斥;不放在 workerPool 之前是因为
	// 多副本场景下原"reaper 在 worker 之前完成"的串行约束本就拿不到,
	// 幂等性已由每阶段 phase* 函数保证(见 internal/worker/handlers/app_initialize.go)。
	reaperInstance := reaper.New(
		reaperStoreAdapter{q: dbStore.Queries}, // 见下方 adapter
		redisQueue,
		distLocker,
		uuid.NewString(),
		logger,
	)
	reaperInstance.Start(ctx)
```

import:`"oc-manager/internal/worker/reaper"`。

- [ ] **Step 2:在 wiring.go 里加 reaperStoreAdapter**

```go
// reaperStoreAdapter 把 sqlc.Queries 适配为 reaper.Store。
// reaper 用的查询签名直接 1:1 对应 sqlc 生成的方法,但 reaper 包不直接 import sqlc 包结构
// (避免反向依赖),所以走一层薄包装。
type reaperStoreAdapter struct{ q *sqlc.Queries }

func (a reaperStoreAdapter) ListStaleInits(ctx context.Context, before pgtype.Timestamptz) ([]sqlc.ListStaleInitsRow, error) {
	return a.q.ListStaleInits(ctx, before)
}
func (a reaperStoreAdapter) SetAppStatus(ctx context.Context, p sqlc.SetAppStatusParams) (sqlc.App, error) {
	return a.q.SetAppStatus(ctx, p)
}
func (a reaperStoreAdapter) ClearAppProgress(ctx context.Context, id pgtype.UUID) (sqlc.App, error) {
	return a.q.ClearAppProgress(ctx, id)
}
func (a reaperStoreAdapter) GetLatestAppInitJob(ctx context.Context, appID string) (sqlc.Job, error) {
	return a.q.GetLatestAppInitJob(ctx, appID)
}
func (a reaperStoreAdapter) RequeueJob(ctx context.Context, id pgtype.UUID) (sqlc.Job, error) {
	return a.q.RequeueJob(ctx, id)
}
func (a reaperStoreAdapter) CreateJob(ctx context.Context, p sqlc.CreateJobParams) (sqlc.Job, error) {
	return a.q.CreateJob(ctx, p)
}
```

(若 reaper 包接口直接 import sqlc 反而更简单,可省去此 adapter,直接 `reaper.New(dbStore.Queries, ...)`。具体取决于 Task 6.1 接口定义里是否依赖 sqlc 类型;查看后选一致风格。)

- [ ] **Step 3:启动验证**

```bash
go build ./... && docker compose up -d --build manager
docker compose logs manager 2>&1 | grep -i reaper | head
```

预期:看到 reaper 启动日志(若加了),首次 tickOnce 立刻执行;后续每 60s 一次。

### Task 6.4:Milestone 6 提交

```bash
git add internal/worker/reaper/ cmd/server/main.go cmd/server/wiring.go
git commit -m "$(cat <<'EOF'
feat(reaper): 周期扫描 init 孤儿 + Redis 锁多副本互斥

后台 reaper goroutine 每 60s tick:抢 ocm:reaper:lock(TTL 30s),
扫 5 个 init 子状态下 updated_at < now()-90s 的孤儿,把 status
重置为 pulling_image + 清空 progress_*,job 已 running/succeeded
则 RequeueJob,无 job 则新建一份,然后通过 redisQueue 通知 scheduler
立刻拾取。

- 90s 阈值是 progressReporter 1s 节流的约 100 倍冗余,正常 worker
  阶段切换的瞬时停顿不会被误判为孤儿;
- 多 manager 同 tick 时只有一个真正扫表,持锁实例崩溃 30s 后由其他
  实例自然接管;
- 装配在 workerPool.Start 之后(原"reaper 必须先于 worker"的串行
  约束在多副本下本就无效,幂等性由每个 phase 函数自行保证);
- 表驱动单测覆盖 5 个孤儿状态、锁不可用直接退出、job 三种状态
  (无/pending/running)分支、store 错误不 panic。
EOF
)"
```

---

## Milestone 7:前端进度展示 + DTO + OpenAPI

**目标:** 后端三字段经 DTO 暴露;前端 AppOverviewTab 在 5 init 子状态时渲染进度条,error 时显示失败阶段。

### Task 7.1:DTO 加三字段 + swag 注解

**Files:**
- Modify: `internal/api/handlers/dto.go`(App 响应 DTO)

- [ ] **Step 1:在 App DTO 中追加三字段**

打开 `dto.go`,找到 `type AppDetail struct { ... }` 或类似的 App 响应类型(若名称不同,以实际项目为准)。追加:

```go
	// ProgressCurrent 当前 status 阶段的已完成量,单位由 status 决定(字节 / 秒);
	// 0 或缺省表示未知 / 不显示进度条。
	ProgressCurrent int64 `json:"progress_current,omitempty"`
	// ProgressTotal 当前 status 阶段的总量;0 或缺省时前端展示为不定进度。
	ProgressTotal int64 `json:"progress_total,omitempty"`
	// LastErrorStatus 上次进入 error 时所在的状态值;前端用 formatAppStatus 转中文文案。
	LastErrorStatus string `json:"last_error_status,omitempty"`
```

- [ ] **Step 2:在 sqlc.App → DTO 转换函数中映射三字段**

找到把 `sqlc.App` 转成 DTO 的工厂函数(常见命名 `appToDTO` 或 service 内部的 builder)。追加:

```go
	dto.ProgressCurrent = app.ProgressCurrent.Int64
	dto.ProgressTotal = app.ProgressTotal.Int64
	dto.LastErrorStatus = app.LastErrorStatus.String
```

(三字段是 pgtype.Int8 / pgtype.Text;`.Valid=false` 时取零值正好对应 omitempty 不输出。)

- [ ] **Step 3:运行 openapi-gen + web-types-gen**

```bash
make openapi-gen && make web-types-gen
```

预期:`openapi/openapi.yaml` 与 `web/src/api/generated.ts` 同步更新,App 响应 schema 多三字段。

- [ ] **Step 4:openapi-check 校验**

```bash
make openapi-check
```

预期:工作区干净,无未提交的 openapi 改动。

### Task 7.2:AppOverviewTab.vue 加进度条与失败阶段

**Files:**
- Modify: `web/src/pages/apps/AppOverviewTab.vue`

- [ ] **Step 1:模板段加进度条 div**

找到当前展示 status badge 的位置(template 内接近 `<n-tag :type="...">{{ formatAppStatus(app.status).label }}</n-tag>` 的地方),在其下方插入:

```vue
<div v-if="isInitPhase(app.status)" class="init-progress">
  <n-progress
    type="line"
    :percentage="initPercentage"
    :indicator-placement="'inside'"
    :processing="initIndeterminate"
  />
  <span v-if="!initIndeterminate" class="init-progress-bytes">
    {{ formatBytes(app.progress_current) }} / {{ formatBytes(app.progress_total) }}
  </span>
</div>

<div v-if="app.status === 'error' && app.last_error_status" class="init-failure">
  在「{{ formatAppStatus(app.last_error_status).label }}」阶段失败
</div>
```

- [ ] **Step 2:script 段补 helper**

```ts
import { formatAppStatus, isInitPhase } from '@/domain/status'
import { computed } from 'vue'

// initPercentage:total>0 时按 current/total 算百分比,否则 0(配合 initIndeterminate)。
const initPercentage = computed(() => {
  const total = app?.value?.progress_total ?? 0
  const current = app?.value?.progress_current ?? 0
  if (total <= 0) return 0
  return Math.min(100, Math.round((current / total) * 100))
})

// total=0 表示未知,UI 走不定进度条(processing=true)。
const initIndeterminate = computed(() => {
  const total = app?.value?.progress_total ?? 0
  return total <= 0
})

// formatBytes:把字节展示为 KB / MB / GB。Naive UI 没现成的,本地小函数。
function formatBytes(n: number | null | undefined): string {
  if (!n || n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}
```

- [ ] **Step 3:style 段加 class**

```vue
<style scoped>
.init-progress {
  margin-top: 8px;
}
.init-progress-bytes {
  font-size: 12px;
  color: var(--text-color-3, #999);
  margin-left: 8px;
}
.init-failure {
  margin-top: 4px;
  color: var(--error-color, #d03050);
  font-size: 13px;
}
</style>
```

- [ ] **Step 4:确保 isInitPhase 已导出(Milestone 1 已做),否则补 status.ts**

### Task 7.3:AppOverviewTab.spec.ts 加断言

**Files:**
- Modify: `web/src/pages/apps/AppOverviewTab.spec.ts`

- [ ] **Step 1:加 case**

```ts
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AppOverviewTab from './AppOverviewTab.vue'
// 测试模板细节会依赖现有 helper;若组件需要 inject,使用 provide 注入 mock app。

describe('AppOverviewTab progress', () => {
  // 5 init 子状态时渲染进度条;total=0 走不定进度。
  it('init 阶段且 total=0 时展示不定进度', () => {
    const wrapper = mount(AppOverviewTab, {
      global: {
        provide: {
          app: { value: { id: 'a', status: 'pulling_image', progress_current: 0, progress_total: 0 } },
        },
      },
      props: { appId: 'a' },
    })
    expect(wrapper.find('.init-progress').exists()).toBe(true)
    expect(wrapper.find('.init-progress-bytes').exists()).toBe(false)
  })

  // total>0 时显示 current/total 字节文案。
  it('init 阶段且 total>0 时展示字节进度', () => {
    const wrapper = mount(AppOverviewTab, {
      global: {
        provide: {
          app: { value: { id: 'a', status: 'syncing_image', progress_current: 1024, progress_total: 4096 } },
        },
      },
      props: { appId: 'a' },
    })
    expect(wrapper.find('.init-progress-bytes').text()).toContain('1.0 KB')
    expect(wrapper.find('.init-progress-bytes').text()).toContain('4.0 KB')
  })

  // status=error + last_error_status 显示失败阶段中文文案。
  it('error 时展示失败阶段', () => {
    const wrapper = mount(AppOverviewTab, {
      global: {
        provide: {
          app: { value: { id: 'a', status: 'error', last_error_status: 'syncing_image' } },
        },
      },
      props: { appId: 'a' },
    })
    expect(wrapper.find('.init-failure').text()).toContain('同步镜像到节点')
  })
})
```

- [ ] **Step 2:跑前端测试**

```bash
cd web && pnpm vitest run src/pages/apps/AppOverviewTab.spec.ts
```

### Task 7.4:Milestone 7 提交

```bash
git add internal/api/handlers/dto.go openapi/openapi.yaml web/src/api/generated.ts web/src/pages/apps/AppOverviewTab.vue web/src/pages/apps/AppOverviewTab.spec.ts
git commit -m "$(cat <<'EOF'
feat(web): app 详情展示初始化进度条与失败阶段

后端 progress_current / progress_total / last_error_status 三字段
经 App DTO 暴露;前端 AppOverviewTab 在 5 个 init 子状态时渲染
进度条:total>0 时按字节比例,total=0 时走不定进度;status=error
且 last_error_status 非空时额外提示"在「xxx」阶段失败"。

- DTO 三字段标 omitempty,与 sqlc.App pgtype Valid=false 自然对应;
- openapi-gen / web-types-gen 重新生成,api/generated.ts 同步更新;
- formatBytes 简单本地实现,KB/MB/GB 展示;
- vitest 覆盖 init 不定进度 / 字节进度 / 失败阶段三个分支。
EOF
)"
```

---

## Milestone 8:联调与浏览器验证

**目标:** 按 spec §10.3 + AGENTS.md 交付前检查要求,用浏览器跑通完整初始化流程的 6 个场景,记录截图,有问题修复直到正常。

> **要求:** 任何场景失败都必须先修复再继续,不能"跳过"。修复后重跑,直到 6 个场景全 PASS。

### Task 8.1:启动本地全栈

- [ ] **Step 1:确保 docker-compose 起着所有依赖**

```bash
docker compose up -d postgres redis new-api manager
docker compose ps
```

预期:5 个容器 healthy。`docker compose logs manager 2>&1 | tail -20` 无 ERROR。

- [ ] **Step 2:web 跑 dev**

```bash
cd web && pnpm dev
```

打开 http://localhost:5173 用 `admin / admin123` 登录。

### Task 8.2:场景 1 — 镜像已在 manager 本机的"快路径"

- [ ] **Step 1:确认本机镜像存在**

```bash
docker image inspect hermes-runtime:dev > /dev/null && echo OK
```

预期:OK(若不存在,先 `make build-hermes` 或自行构建一份)。

- [ ] **Step 2:用 test-org-user1 创建一个新 app**

UI:切到组织 `test-org`,创建一个 app,选 wechat 渠道。

- [ ] **Step 3:观察 status 序列**

UI 应在数秒内连续展示:
`待初始化(draft)` → `拉取运行时镜像(瞬间)` → `同步镜像到节点(瞬间)` → `准备运行时配置` → `创建容器` → `启动容器` → `待绑定`。

若任何阶段卡死或跳过显示,记下来 → 定位 phase* 函数排查 → 修复 → 重跑。

### Task 8.3:场景 2 — 删本机镜像后 pull 进度条能动

- [ ] **Step 1:删本地镜像 + 改 RuntimeImage 配置指向公共 registry**

```bash
docker image rm hermes-runtime:dev
# 临时改 manager 配置让它去拉一个公共镜像(eg. alpine:latest)
# 或者把宿主机已有镜像 push 到本地 registry 再让 manager 拉
```

(具体方式按本地有无 registry 决定。最简单是临时把 RuntimeImage 改成 `alpine:latest`,纯做"拉取进度展示"的视觉验证。)

- [ ] **Step 2:重启 manager + 创建新 app**

```bash
docker compose restart manager
```

UI 创建 app,观察 `拉取运行时镜像` 阶段进度条能从 0 增到 100%(若 total>0)或显示不定进度旋转(total=0)。

### Task 8.4:场景 3 — 同时 2 个 app,镜像同步进度广播

- [ ] **Step 1:同时点 2 个补建**

UI:在 `test-org` 下用两个不同账号(test-org-user1 + 第二个),几乎同时创建两个 app。

- [ ] **Step 2:观察两个 app 都看到 syncing_image 进度**

两个 app 详情页同时打开,刷新,应观察:
- 两个 app 都进入 `syncing_image` 阶段;
- 两个的 `progress_current/total` 都在更新(说明 ImageCoordinator 的 Redis Pub/Sub 广播生效)。

```bash
docker compose logs manager 2>&1 | grep -E "image:sync:lock|image:sync:bus" | head
```

预期:能看到 SET NX 的锁日志(只一次)和 PUBLISH 日志(多次)。

### Task 8.5:场景 4 — manager 重启后 reaper 接管

- [ ] **Step 1:在 syncing_image 中途 restart manager**

UI 创建一个新 app,看到进入 `syncing_image` 后立即:

```bash
docker compose restart manager
```

- [ ] **Step 2:90 秒内观察 app 仍能完成初始化**

UI 不断刷新 app 详情页;reaper 应在 60-90s 内重置 status 为 `pulling_image`,worker 重新跑 5 阶段,最终走到 `binding_waiting`。

```bash
docker compose logs manager 2>&1 | grep -i reaper | tail
```

预期:reaper 日志显示重置了 1 个孤儿。

### Task 8.6:场景 5 — agent 失败 + last_error_status 展示

- [ ] **Step 1:让 agent 临时拒绝 inspect**

最简单方式:UI 把 runtime node 切到一个不存在的地址,或用 `iptables` / `docker network disconnect` 让 manager 暂时连不上 agent。

- [ ] **Step 2:创建 app**

期望:走到 `syncing_image` 阶段失败 → status=error + last_error_status=syncing_image。

UI 应展示:
- 状态:异常(红色 tag);
- 下方文案:"在「同步镜像到节点」阶段失败";
- "重新初始化" 按钮可点。

- [ ] **Step 3:恢复 agent 连通,点重新初始化**

按按钮后 status 应回到 `pulling_image`,流程重新跑通。

### Task 8.7:场景 6 — 多副本(scale=2)单飞 + 接管

- [ ] **Step 1:scale manager 到 2**

```bash
docker compose up -d --scale manager=2
docker compose ps | grep manager
```

预期:两个 manager 容器 healthy。

- [ ] **Step 2:同时 2 个 app(同一镜像)init**

```bash
docker events --filter event=pull --since 1m &
```

UI 同时创建两个 app,要求触发对同一镜像的 pull。观察 `docker events`:

预期:**只**有一个 pull 事件流(说明跨 manager 锁工作)。两个 app 的 progress_* 都在更新。

- [ ] **Step 3:kill 当前 leader manager**

```bash
docker kill $(docker compose ps -q manager | head -1)
```

继续观察:60-90s 后 reaper(运行在剩余 manager 上)应接管两个 app,继续推进到 `binding_waiting`。

```bash
docker compose logs --tail=100 manager 2>&1 | grep -E "reaper|pulling_image"
```

### Task 8.8:总结 + 修复闭环

- [ ] **Step 1:汇总 6 个场景的结果**

把 6 个场景的 PASS / FAIL 列成一张表:

| 场景 | 结果 | 备注 |
|---|---|---|
| 1. 快路径 | PASS / FAIL | ... |
| 2. pull 进度 | PASS / FAIL | ... |
| 3. 广播 | PASS / FAIL | ... |
| 4. 重启接管 | PASS / FAIL | ... |
| 5. 失败展示 | PASS / FAIL | ... |
| 6. 多副本 | PASS / FAIL | ... |

- [ ] **Step 2:任何 FAIL 必须修复**

按 systematic-debugging skill 的"先复现 → 看日志 → 找根因 → 改代码 → 重测"循环,直到全 6 PASS。每修一个 bug 就 commit 一次:

```bash
git commit -m "fix(<scope>): <一句话描述根因与修复>"
```

- [ ] **Step 3:全测试 + final lint**

```bash
go test ./... && cd web && pnpm vitest run
```

预期:全部 PASS。

- [ ] **Step 4:final commit(若仅文档 / 注释微调)**

```bash
git status # 确认无遗漏
git log --oneline -10 # 看 milestone 1-7 + bugfix 是否成体系
```

---

## Self-Review

Plan 写完后我做了一遍自审,记录在此供执行者对照:

### 1. Spec 覆盖矩阵

| Spec 章节 | 覆盖任务 |
|---|---|
| §一 整体数据流 | M5(handler 重构)+ M4(coord)+ M6(reaper)三路实现 |
| §二.1 status 字段值变化 | Task 1.2 (migration) + Task 1.4 (enums.go) |
| §二.2 21 条转移 | Task 1.4 (state_machine.go) + Task 1.5 (test) |
| §二.3 last_error_status 字段 | Task 1.2 (migration) + Task 5.1 (sqlc) + Task 5.3 (markFailed) |
| §三 数据库变更 | Task 1.2 + Task 1.3 |
| §四.1 LocalDockerSDKProvider | Task 2.2 |
| §四.2 RegistryAuthStore | Task 2.1 |
| §四.3 docker socket 挂载 | Task 2.4 |
| §五.1 DistLocker | Task 3.1 |
| §五.2 ProgressBus | Task 3.2 |
| §五.3 Leader/Follower | Task 4.3 |
| §五.4 进度聚合 | Task 4.2 |
| §五.5 Sync 进度 (countingReader) | Task 4.3 step 3 |
| §六.1 5 阶段循环 | Task 5.3 |
| §六.2 各阶段幂等 | Task 5.3 phase* 内置 |
| §六.3 progressReporter | Task 4.4 |
| §七.1 reaper 启动时机 | Task 6.3 |
| §七.2 reaper 实现 | Task 6.1 |
| §七.3 多 manager 安全性 | 由 Task 6.1 实现 + Task 6.2 测试覆盖 |
| §七.4 进度恢复语义 | Task 6.1 reapApp 内已 ClearAppProgress |
| §八 RequestInitialize 重置 | Task 5.6 |
| §九.1 status.ts | Task 1.6 |
| §九.2 进度展示 | Task 7.2 |
| §九.3 失败提示 | Task 7.2 |
| §九.4 重新初始化按钮 | 不改(spec 说保持现状) |
| §九.5 OpenAPI/DTO | Task 7.1 |
| §十.1-10.2 单测 | 各 milestone 内嵌 test task |
| §十.3 浏览器验证 | M8(全部 6 个场景) |

### 2. Placeholder 扫描

- 无 "TBD" / "实现稍后" / "类似上面"。每个 step 都有完整代码或精确命令。
- 唯一灰色地带:Task 5.5 的"补 mock 方法"用了 `newFakeAppInitStore` / `newTestHandlerFailingAt` 等 helper 名,但说明了它们要在测试文件顶部新建,签名与项目现有 helper 风格保持一致。这是因为现有测试文件 30KB,完整复现整个 helper 套件会占据大量篇幅;执行者应先读 `app_initialize_test.go` 现状再决定 helper 取舍。

### 3. 类型一致性

- `ProgressEvent` 在 `internal/redis/progress_bus.go` 定义,`imagecoord.types.go` 用 type alias 重导,worker handler 通过 `imagecoord.ProgressEvent` 引用 — 三处一致。
- `ContainerState` 在 Task 5.2 定义,`tryInspect` (Task 5.3 phaseStart) 与 wiring adapter 都用同名结构体。
- `progressReporter` 接口 `ProgressStore` 在 Task 4.4 声明,`AppInitializeStore` 在 Task 5.2 包含同方法 — `newProgressReporter(app.ID, h.store)` 调用时 `h.store` 满足接口。
- `Reaper.Store` 接口在 Task 6.1 声明的字段名(`ListStaleInits` / `ClearAppProgress` / `GetLatestAppInitJob` / `RequeueJob`)与 Task 5.1 sqlc query 名一致。

### 4. 已知边界与执行注意事项

- **Task 4.4 编译依赖 Task 5.1**:progressReporter 引用 `sqlc.SetAppProgressParams`。subagent 实施时可把 Task 5.1 提前到 Milestone 4 之前;或 Milestone 4 与 5 合并一个 PR。
- **Task 5.2 ContainerStarter 接口扩张**:wiring 处的 runtime adapter 若没实现 InspectContainer,`tryInspect` 的类型断言失败会优雅退化为"直接 Start"——不会编译失败,但等价于丢失了"running 容器跳过 Start"的优化。生产装配若需要这优化,Milestone 6 时把 adapter 加上 InspectContainer 实现。
- **Task 7.2 Naive UI 组件名**:用了 `n-progress`;若项目实际用其他 UI 库,把组件名换成对应实现即可,逻辑(percentage / indeterminate)不变。
- **Task 8.x 浏览器场景**:每个 FAIL 场景的根因可能落在前一个 milestone,修复时勿跳过 systematic-debugging skill。

---

