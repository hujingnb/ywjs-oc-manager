# 概览「平台提示词已更新需重启」提示 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实例概览页在「平台层 prompt 常量已更新、但实例仍跑旧文本」时显示需重启提示 + 重启按钮，沿用既有 `web_publish_pending_restart` 快照模式。

**Architecture:** 加 `apps.applied_platform_prompt_hash` 列，`BootstrapService.Build`（pod 每次启动/重启都经此重渲染）里 stamp 当前常量 sha256；`AppService.Get` 用 `applied hash != config.PlatformPromptHash()` 算出 `AppResult.PlatformPromptPendingRestart`，随 `GET /apps/:id` 透出；前端 `AppOverviewTab.vue` 读该布尔渲染 warning 横幅，重启复用 `useTriggerRuntimeOperation`。

**Tech Stack:** Go、golang-migrate（`internal/migrations`）、sqlc、swag/OpenAPI、Vue3 + naive-ui + vue-i18n、vitest。

**依据文档：** `docs/superpowers/specs/2026-07-03-platform-prompt-pending-restart-design.md`

**关键事实（已核实）：**
- `GET /apps/:id`（`apps.go` Get）直接序列化 service 的 `AppResult`（带 json tag），**无独立 DTO**——新字段加在 `AppResult`（`internal/service/app_service.go`）上，swag 扫描该结构体自动进 openapi。
- **单 stamp 点**即可：k8s 下 restart 不写 platform-rules（`main.go:515-518` 注释），重渲染发生在 pod 重建时 initContainer 调 `BootstrapService.Build`；restart→pod 重建→Build 再次 stamp。故 `web_publish_applied` 就是单点写在 Build 里（`bootstrap_service.go:289`），本特性同样单点。
- hash 单一来源：平台 prompt 已是常量 `config.DefaultSystemPromptTemplate`（`main.go` 把 `PlatformPrompt`/`s.cfg.PlatformPrompt` 都设为它），故 stamp 与 compare 都用 `config.PlatformPromptHash()`。
- 前端有现成 web_publish `<n-alert>` 横幅模板（`AppOverviewTab.vue:19-39`），重启走 `onRestartForVersionSync` + `canRestartForWebPublish` 口径。

---

## Task 1: migration 加列 `applied_platform_prompt_hash`

**Files:**
- Create: `internal/migrations/000027_apps_platform_prompt_hash.up.sql`
- Create: `internal/migrations/000027_apps_platform_prompt_hash.down.sql`

- [ ] **Step 1: 写 up migration**

`internal/migrations/000027_apps_platform_prompt_hash.up.sql`：

```sql
-- apps 增加 applied_platform_prompt_hash：记录实例「最近一次 bootstrap 写入 input 的平台层
-- prompt 文本」的 sha256（hex）。平台层 prompt 已固化为代码常量 config.DefaultSystemPromptTemplate，
-- 只在 manager 重新部署时变化；实例只有走一次 bootstrap/重启才会重渲染 SOUL.md 平台层。
-- 该 hash 与当前常量 hash 比对即可判定实例是否「平台提示词已更新、需重启生效」
-- （与 web_publish_applied / applied_version_revision 的快照-比对思路一致）。
-- 默认 ''：存量实例在首次（重新）bootstrap 前 hash 为空，一律判为需重启。
ALTER TABLE apps
    ADD COLUMN applied_platform_prompt_hash CHAR(64) NOT NULL DEFAULT ''
        COMMENT '最近一次 bootstrap 写入 input 的平台层 prompt 文本 sha256（hex）：与当前常量 hash 比对判定是否需重启生效；空=存量/未 bootstrap，视为需重启';
```

- [ ] **Step 2: 写 down migration**

`internal/migrations/000027_apps_platform_prompt_hash.down.sql`：

```sql
-- 回滚：移除 applied_platform_prompt_hash 列。
ALTER TABLE apps DROP COLUMN applied_platform_prompt_hash;
```

- [ ] **Step 3: 跑 migration 往返测试**

Run: `go test ./internal/migrations/...`
Expected: PASS（migrations_test 对全部 up/down 做往返校验，自动覆盖 000027）

- [ ] **Step 4: Commit**

```bash
git add internal/migrations/000027_apps_platform_prompt_hash.up.sql internal/migrations/000027_apps_platform_prompt_hash.down.sql
git commit -m "feat(db): apps 增加 applied_platform_prompt_hash 列

记录实例最近一次 bootstrap 写入的平台层 prompt sha256，供「平台提示词已更新
需重启」检测。默认空，存量实例视为需重启。沿用 web_publish_applied 快照模式。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: sqlc setter + 重新生成

**Files:**
- Modify: `internal/store/queries/apps.sql`（在 `SetAppWebPublishApplied` 之后追加）
- Regenerate: `internal/store/sqlc/*.go`（`make sqlc-generate` 覆盖）

- [ ] **Step 1: 追加 setter query**

在 `internal/store/queries/apps.sql` 的 `-- name: SetAppWebPublishApplied :exec` 块之后追加：

```sql
-- name: SetAppAppliedPlatformPromptHash :exec
-- bootstrap 渲染时记录本次写入 input 的平台层 prompt sha256，用于「平台提示词已更新需重启」检测。
-- 不更新 updated_at：bootstrap 每次 pod 启动都会调用（与 SetAppWebPublishApplied 同因），
-- 避免无意义地刷新 updated_at。
UPDATE apps
SET applied_platform_prompt_hash = ?
WHERE id = ?;
```

- [ ] **Step 2: 重新生成 sqlc 代码**

Run: `make sqlc-generate`
Expected: 无报错；`internal/store/sqlc/models.go` 的 `App` 结构体新增 `AppliedPlatformPromptHash string`；`querier.go` 新增 `SetAppAppliedPlatformPromptHash`；`apps.sql.go` 生成对应实现与 `SetAppAppliedPlatformPromptHashParams{AppliedPlatformPromptHash string; ID string}`。

- [ ] **Step 3: 验证生成结果 + 编译**

Run: `grep -n "AppliedPlatformPromptHash" internal/store/sqlc/models.go internal/store/sqlc/querier.go && go build ./internal/store/...`
Expected: 两文件均出现该标识符；build 通过。

- [ ] **Step 4: Commit**

```bash
git add internal/store/queries/apps.sql internal/store/sqlc/
git commit -m "feat(store): 新增 SetAppAppliedPlatformPromptHash 查询

sqlc 生成 App.AppliedPlatformPromptHash 字段与 setter，供 bootstrap stamp
平台 prompt hash。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: `config.PlatformPromptHash()` 单一来源

**Files:**
- Modify: `internal/config/platform_prompt.go`
- Test: `internal/config/platform_prompt_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/config/platform_prompt_test.go` 追加（import 需含 `crypto/sha256`、`encoding/hex`）：

```go
// TestPlatformPromptHash 校验平台 prompt hash 稳定、64 位 hex、且严格等于常量的 sha256——
// 它是 bootstrap stamp 与概览 compare 的唯一期望来源，必须与常量绑定（改常量则 hash 变）。
func TestPlatformPromptHash(t *testing.T) {
	h := PlatformPromptHash()
	require.Len(t, h, 64)               // sha256 hex 定长 64
	require.Equal(t, h, PlatformPromptHash()) // 幂等：同输入同输出
	sum := sha256.Sum256([]byte(DefaultSystemPromptTemplate))
	assert.Equal(t, hex.EncodeToString(sum[:]), h) // 严格等于常量的 sha256
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/config/ -run TestPlatformPromptHash`
Expected: FAIL / 编译错误（`PlatformPromptHash` 未定义）

- [ ] **Step 3: 实现 `PlatformPromptHash`**

在 `internal/config/platform_prompt.go` 文件顶部补 import 并在常量之后追加：

```go
import (
	"crypto/sha256"
	"encoding/hex"
)

// PlatformPromptHash 返回固化平台层 prompt 常量的 sha256（hex）。
// 作为「当前期望平台 prompt」的单一 hash 来源：bootstrap 渲染时把它 stamp 进
// apps.applied_platform_prompt_hash；概览按「applied hash != 本值」判定实例是否
// 需重启重渲染 SOUL.md 平台层。平台 prompt 现为常量、无 per-app 变体，故全局一个值即可。
func PlatformPromptHash() string {
	sum := sha256.Sum256([]byte(DefaultSystemPromptTemplate))
	return hex.EncodeToString(sum[:])
}
```

> 注意：`platform_prompt.go` 原本无 import 块（只有 `package config` + 常量）。新增 import 块放在 `package config` 之后、常量之前。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/config/ -run TestPlatformPromptHash -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/platform_prompt.go internal/config/platform_prompt_test.go
git commit -m "feat(config): 新增 PlatformPromptHash 作为平台 prompt 期望 hash 单一来源

sha256(DefaultSystemPromptTemplate)，供 bootstrap stamp 与概览需重启判定共用。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: `BootstrapService.Build` stamp hash（单点覆盖 bootstrap+restart）

**Files:**
- Modify: `internal/service/bootstrap_service.go`（store 接口 + Build 内 stamp）
- Test: `internal/service/bootstrap_service_test.go`（store stub 补方法 + 断言）

- [ ] **Step 1: store 接口补方法**

在 `internal/service/bootstrap_service.go` 的 BootstrapService store 接口里（含 `SetAppWebPublishApplied` 那个，约 96-97 行）追加：

```go
	// SetAppAppliedPlatformPromptHash 记录本次 bootstrap 写入 input 的平台 prompt hash，用于「平台提示词已更新需重启」检测。
	SetAppAppliedPlatformPromptHash(ctx context.Context, arg sqlc.SetAppAppliedPlatformPromptHashParams) error
```

- [ ] **Step 2: Build 内 stamp（紧接 SetAppWebPublishApplied 块之后）**

在 `internal/service/bootstrap_service.go` 的 `SetAppWebPublishApplied` 调用块（约 289-295 行）之后追加，并确保文件已 `import "oc-manager/internal/config"`：

```go
	// 记录本次 bootstrap 写入 input 的平台层 prompt hash，用于「平台提示词已更新需重启」检测。
	// k8s 下 restart 会触发 pod 重建 → initContainer 再次走本 Build → 此处重新 stamp，
	// 故单点即覆盖 bootstrap 与 restart 两条路径（与 SetAppWebPublishApplied 单点同理）。
	// best-effort：写失败只 warn，不阻断实例启动（仅影响 needs-restart 提示）。
	if err := s.store.SetAppAppliedPlatformPromptHash(ctx, sqlc.SetAppAppliedPlatformPromptHashParams{
		AppliedPlatformPromptHash: config.PlatformPromptHash(),
		ID:                        app.ID,
	}); err != nil {
		slog.WarnContext(ctx, "记录 applied_platform_prompt_hash 失败", "app_id", app.ID, mlog.Err(err))
	}
```

- [ ] **Step 3: store stub 补方法 + 断言（写失败测试）**

在 `internal/service/bootstrap_service_test.go` 里，给 BootstrapService 用的 store stub（已实现 `SetAppWebPublishApplied` 的那个）追加捕获字段与方法：

```go
// 捕获 stamp 的平台 prompt hash，供断言 Build 是否用当前常量 hash 写入。
capturedPlatformPromptHash string
```

```go
func (s *<stubType>) SetAppAppliedPlatformPromptHash(_ context.Context, arg sqlc.SetAppAppliedPlatformPromptHashParams) error {
	s.capturedPlatformPromptHash = arg.AppliedPlatformPromptHash
	return nil
}
```

在现有「Build 成功」用例中补断言：

```go
// bootstrap 应把当前平台 prompt 常量 hash stamp 进 apps，供概览需重启检测。
assert.Equal(t, config.PlatformPromptHash(), stub.capturedPlatformPromptHash)
```

> `<stubType>` 用该测试文件里已实现 `SetAppWebPublishApplied` 的实际 stub 结构体名替换；测试 import 补 `oc-manager/internal/config`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/service/ -run Bootstrap -v`
Expected: PASS（含新断言）

- [ ] **Step 5: Commit**

```bash
git add internal/service/bootstrap_service.go internal/service/bootstrap_service_test.go
git commit -m "feat(bootstrap): stamp 平台 prompt hash 到 apps

Build 每次 pod 启动都写入当前常量 hash（restart 走 pod 重建同样经此），
单点覆盖 bootstrap 与 restart，供概览判定平台提示词是否需重启生效。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 5: `AppService.Get` 计算 `PlatformPromptPendingRestart`

**Files:**
- Modify: `internal/service/app_service.go`（AppResult 字段 + compute 调用 + 纯函数 + import config）
- Test: `internal/service/app_service_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/service/app_service_test.go` 追加（import 补 `oc-manager/internal/config`）：

```go
// TestComputePlatformPromptPendingRestart 校验「平台 prompt 需重启」判定：
// applied hash 等于当前常量 hash→不需；不等或为空（存量实例）→需重启。
func TestComputePlatformPromptPendingRestart(t *testing.T) {
	// 与当前常量 hash 一致：实例已是最新平台 prompt，不提示。
	assert.False(t, computePlatformPromptPendingRestart(sqlc.App{AppliedPlatformPromptHash: config.PlatformPromptHash()}))
	// hash 不同（旧文本）：需重启。
	assert.True(t, computePlatformPromptPendingRestart(sqlc.App{AppliedPlatformPromptHash: "deadbeef"}))
	// 空 hash（migration 默认，存量/未 bootstrap 实例）：需重启。
	assert.True(t, computePlatformPromptPendingRestart(sqlc.App{AppliedPlatformPromptHash: ""}))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run TestComputePlatformPromptPendingRestart`
Expected: FAIL / 编译错误（`computePlatformPromptPendingRestart` 未定义）

- [ ] **Step 3: 加 AppResult 字段**

在 `internal/service/app_service.go` 的 AppResult 结构体里，`WebPublishPendingRestart`（约 135 行）之后追加：

```go
	// PlatformPromptPendingRestart 标记「平台层身份 prompt 常量已更新，但本实例上次 bootstrap
	// 写入的是旧文本」——需重启重渲染 SOUL.md 平台层才能生效。
	PlatformPromptPendingRestart bool `json:"platform_prompt_pending_restart"`
```

- [ ] **Step 4: Get 内计算（紧接 WebPublishPendingRestart 赋值之后，约 160 行）**

```go
	// platform_prompt_pending_restart：实例上次 bootstrap stamp 的平台 prompt hash 与当前常量
	// hash 不一致（含存量实例 applied 为空）→ 需重启重渲染 SOUL.md 平台层。
	result.PlatformPromptPendingRestart = computePlatformPromptPendingRestart(row.App)
```

- [ ] **Step 5: 加纯函数（放在 computeWebPublishPendingRestart 附近）+ import config**

```go
// computePlatformPromptPendingRestart 判断实例是否「平台 prompt 已更新需重启」：
// 上次 bootstrap stamp 的 applied_platform_prompt_hash 与当前常量 hash 不等即为真
// （空 hash 的存量实例天然不等，一律判为需重启）。
func computePlatformPromptPendingRestart(app sqlc.App) bool {
	return app.AppliedPlatformPromptHash != config.PlatformPromptHash()
}
```

确保 `internal/service/app_service.go` 的 import 含 `"oc-manager/internal/config"`。

- [ ] **Step 6: 跑测试确认通过 + 全包编译**

Run: `go test ./internal/service/ -run TestComputePlatformPromptPendingRestart -v && go build ./...`
Expected: PASS + build 通过

- [ ] **Step 7: Commit**

```bash
git add internal/service/app_service.go internal/service/app_service_test.go
git commit -m "feat(app): Get 返回 platform_prompt_pending_restart

比对实例 applied_platform_prompt_hash 与当前常量 hash，随 GET /apps/:id 透出，
供概览显示「平台提示词已更新需重启」。存量实例（空 hash）判为需重启。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 6: 重新生成 OpenAPI + 前端类型

**Files:**
- Regenerate: `openapi/openapi.yaml`、`web/src/api/generated.ts`

- [ ] **Step 1: 生成**

Run: `make openapi-gen && make web-types-gen`
Expected: 无报错

- [ ] **Step 2: 验证字段进入生成产物**

Run: `grep -n "platform_prompt_pending_restart" openapi/openapi.yaml web/src/api/generated.ts`
Expected: 两文件均出现 `platform_prompt_pending_restart`

- [ ] **Step 3: 校验 openapi 同步（工作区应干净）**

Run: `make openapi-check`
Expected: 通过（生成后 git 工作区无额外 diff）

- [ ] **Step 4: Commit**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(openapi): 生成 platform_prompt_pending_restart 类型

随 AppResult 新字段重生成 openapi.yaml 与前端 generated.ts。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 7: 前端概览横幅 + i18n + 测试

**Files:**
- Modify: `web/src/pages/apps/AppOverviewTab.vue`（横幅 + computed）
- Modify: `web/src/i18n/locales/zh/apps/root.ts`、`web/src/i18n/locales/en/apps/root.ts`
- Test: `web/src/pages/apps/AppOverviewTab.spec.ts`

- [ ] **Step 1: 加 i18n 文案（zh）**

在 `web/src/i18n/locales/zh/apps/root.ts` 的 `overview` 下、`webPublish` 块附近追加：

```ts
    prompt: {
      pendingTitle: '平台提示词已更新，需重启实例生效',
      pendingDesc: '平台已更新助手的身份提示词，本实例仍在使用旧版本，重启后即可生效。',
    },
```

- [ ] **Step 2: 加 i18n 文案（en）**

在 `web/src/i18n/locales/en/apps/root.ts` 对应 `overview` 下追加：

```ts
    prompt: {
      pendingTitle: 'Platform prompt updated — restart to apply',
      pendingDesc: 'The platform updated the assistant identity prompt. This instance is still running the previous version; restart to apply it.',
    },
```

- [ ] **Step 3: 加横幅（紧接 web_publish 的 n-alert 之后，约 39 行）**

在 `web/src/pages/apps/AppOverviewTab.vue` 的 web_publish `</n-alert>` 之后插入：

```vue
    <!-- 平台层身份 prompt 常量已更新，但本实例上次 bootstrap 写入的是旧文本：
         提示需重启重渲染 SOUL.md，并提供直接重启入口（复用 restart 操作）。 -->
    <n-alert
      v-if="app && app.platform_prompt_pending_restart"
      type="warning"
      :title="t('apps.overview.prompt.pendingTitle')"
      style="margin-bottom: 12px"
    >
      <n-space align="center" :size="12">
        <span>{{ t('apps.overview.prompt.pendingDesc') }}</span>
        <n-button
          v-if="canRestartForPlatformPrompt"
          size="small"
          type="primary"
          :disabled="restartMutation.isPending.value"
          @click="onRestartForVersionSync"
        >
          {{ restartMutation.isPending.value ? t('apps.overview.restartNowPending') : t('apps.overview.restartNow') }}
        </n-button>
      </n-space>
    </n-alert>
```

- [ ] **Step 4: 加 computed（紧接 `canRestartForWebPublish` 定义之后）**

在 `web/src/pages/apps/AppOverviewTab.vue` 的 script 区，`canRestartForWebPublish` 之后追加：

```ts
// canRestartForPlatformPrompt 控制平台 prompt「需重启」横幅里的重启按钮：
// 口径与 canRestartForVersionSync 一致（有运行时操作权限 + 实例 running/binding_waiting），
// 仅触发条件换成 platform_prompt_pending_restart=true。
const canRestartForPlatformPrompt = computed(() => {
  if (!app?.value) return false
  if (!canTriggerRuntimeOperation(auth.user, app.value)) return false
  if (app.value.platform_prompt_pending_restart !== true) return false
  const status = app.value.status
  return status === 'running' || status === 'binding_waiting'
})
```

- [ ] **Step 5: 加前端测试**

在 `web/src/pages/apps/AppOverviewTab.spec.ts` 追加（沿用文件里已有的 `mountWithApp` helper）：

```ts
// platform_prompt_pending_restart=true 时应渲染「平台提示词已更新」需重启横幅。
it('platform_prompt_pending_restart=true 时展示需重启横幅', () => {
  const wrapper = mountWithApp({ platform_prompt_pending_restart: true })
  expect(wrapper.text()).toContain('平台提示词已更新')
})

// 字段为 false/缺省时不应出现该横幅，避免误导。
it('platform_prompt_pending_restart 非 true 时不展示横幅', () => {
  const wrapper = mountWithApp({ platform_prompt_pending_restart: false })
  expect(wrapper.text()).not.toContain('平台提示词已更新')
})
```

> 若 `mountWithApp` 的基础 mock app 未含新字段，给基础 mock 补 `platform_prompt_pending_restart: false`，避免其它用例 undefined 报错。

- [ ] **Step 6: 跑前端测试 + 类型检查**

Run: `cd web && npx vitest run src/pages/apps/AppOverviewTab.spec.ts && npm run type-check`
Expected: 新增 2 用例 PASS；type-check 无 `platform_prompt_pending_restart` 相关报错（generated.ts 已含该字段）。

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/apps/AppOverviewTab.vue web/src/pages/apps/AppOverviewTab.spec.ts web/src/i18n/locales/zh/apps/root.ts web/src/i18n/locales/en/apps/root.ts
git commit -m "feat(web): 概览显示「平台提示词已更新需重启」横幅

读 app.platform_prompt_pending_restart 渲染 warning 横幅+立即重启按钮，
复用 runtime/restart。中英文案齐备，补两条组件测试。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 8: 修订 spec 的单 stamp 点说明

**Files:**
- Modify: `docs/superpowers/specs/2026-07-03-platform-prompt-pending-restart-design.md`

- [ ] **Step 1: 修订 §4**

把 §4「写入点」由「bootstrap + restart 两处」订正为「单点」：k8s 下 restart 不写
platform-rules（`main.go:515-518`），重渲染在 pod 重建时 initContainer 调
`BootstrapService.Build` 完成，restart→pod 重建→Build 再次 stamp，故只在 `Build` 内
（紧挨 `SetAppWebPublishApplied`）stamp 一处即覆盖 bootstrap 与 restart。

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/specs/2026-07-03-platform-prompt-pending-restart-design.md
git commit -m "docs: 订正需重启提示 spec 为单 stamp 点

实现核实 k8s restart 经 pod 重建再走 BootstrapService.Build，单点 stamp 即
覆盖 bootstrap 与 restart，无需在 restart handler 另写。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-Review

- **Spec 覆盖：** §3 机制→Task1(列)+Task3(hash)；§4 stamp→Task4（单点，Task8 订正 spec）；§5 暴露/前端→Task2(sqlc)+Task5(compute/字段)+Task6(openapi/types)+Task7(前端)；§6 测试→Task3/4/5/7 各自单测 + Task1 migration 往返；§8/§9 生效与浏览器验证在交付说明中执行（见下）。全部有对应任务。
- **占位符：** 除 Task4 Step3 的 `<stubType>`（明确指示用实际 stub 名替换）外无占位符；所有 SQL/Go/Vue/命令均为可直接执行内容。
- **类型一致：** 列名 `applied_platform_prompt_hash`、sqlc `SetAppAppliedPlatformPromptHash` / `App.AppliedPlatformPromptHash`、Go `config.PlatformPromptHash()` / `computePlatformPromptPendingRestart` / `AppResult.PlatformPromptPendingRestart`、JSON `platform_prompt_pending_restart`、前端 `canRestartForPlatformPrompt` 全程一致。

## 交付前（浏览器验证，spec §9）

代码合并并本地重建 manager 后：
1. 概览打开一个存量实例 → 应显示「平台提示词已更新，需重启实例生效」横幅 + 运行中时有「立即重启」。
2. 点「立即重启」→ 等重启完成 → 横幅消失（stamp 已更新为当前 hash）。
3. exec 进实例确认 SOUL.md 平台层为当前常量内容。
4. 三角色（平台管理员 / 组织管理员 / 组织成员）走查一致。
