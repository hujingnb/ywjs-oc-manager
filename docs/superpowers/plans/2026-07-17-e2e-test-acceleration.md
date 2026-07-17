# E2E Test Acceleration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将现有 Playwright E2E 拆为 quick、regression、slow 三级入口，通过 `run_id` 与 worker 级 fixture 隔离实现安全并行，使 quick 稳定在 60 秒内、regression 稳定在 20 分钟内。

**Architecture:** `cmd/seed-e2e` 生成带运行标识和 worker 索引的 fixture 池，并提供同边界清理；Playwright 支持层解析 suite、为 worker 选择 fixture、通过登录 API 生成角色认证状态，并在 teardown 中按 `run_id` 清理。测试标签决定业务套件，Playwright project 只决定浏览器形态，Makefile 和 npm scripts 暴露三个语义明确的入口。

**Tech Stack:** Go 1.22+、MySQL、Playwright 1.59、TypeScript 5.9、Vitest 3、GNU Make、本地 k3d/kubectl

---

## 文件结构

- Modify: `cmd/seed-e2e/main.go` — 接收运行参数，创建 fixture 池，按 suite 控制昂贵依赖准备。
- Create: `cmd/seed-e2e/run.go` — 定义 seed/cleanup 参数、运行标识、fixture 命名和池结构。
- Create: `cmd/seed-e2e/cleanup.go` — 只删除目标 `run_id` 或过期 E2E 数据，不触碰其他本地数据。
- Modify: `cmd/seed-e2e/main_test.go` — 覆盖运行参数、池唯一性、new-api 用户名和清理边界。
- Create: `web/tests/e2e/suite.ts` — 定义 suite、worker 数、标签筛选、pool 解析和临时路径。
- Create: `web/tests/e2e/suite.test.ts` — 用 Vitest 锁定 suite 配置和 worker 映射。
- Create: `web/tests/e2e/auth-state.ts` — 通过登录 API 为每个 worker/role 生成 storage state。
- Create: `web/tests/e2e/preflight.ts` — slow 子标签依赖检查和本地 k3d 安全检查。
- Modify: `web/tests/e2e/global-setup.ts` — 一次生成 fixture 池和认证状态，移除 namespace 全量删除。
- Create: `web/tests/e2e/global-teardown.ts` — 成功时按 `run_id` 清理，失败时保留诊断标识。
- Modify: `web/tests/e2e/fixtures.ts` — 提供 worker-scoped fixture、角色认证页面和 UI 登录 helper。
- Create: `web/tests/e2e/timing-reporter.ts` — 输出总耗时、spec 耗时和慢用例排行。
- Create: `web/tests/e2e/run-suite.mjs` — 透传定向参数并保留 Playwright 原始退出码。
- Modify: `web/playwright.config.ts` — 接入 suite worker、teardown 和 reporter。
- Modify: `web/package.json` — 暴露三个 npm 入口，删除含义模糊的旧入口。
- Modify: `Makefile` — 暴露三个 Make 入口和 scoped cleanup。
- Modify: `web/tests/e2e/login.spec.ts`、`web/tests/e2e/locale.spec.ts`、`web/tests/e2e/organizations.spec.ts`、`web/tests/e2e/members.spec.ts`、`web/tests/e2e/app-detail.spec.ts`、`web/tests/e2e/console.spec.ts`、`web/tests/e2e/delete-cascade.spec.ts` — 添加 quick 标签并迁移认证 fixture。
- Modify: `web/tests/e2e/l4-i18n-sweep.spec.ts`、`web/tests/e2e/aicc.spec.ts`、`web/tests/e2e/aicc-knowledge.spec.ts`、`web/tests/e2e/aicc-conversation-intent.spec.ts`、`web/tests/e2e/aicc-conversation-runtime.spec.ts`、`web/tests/e2e/aicc-conversation-security.spec.ts` — 添加 slow 与能力标签。
- Modify: `web/tests/e2e/aicc-access-i18n.spec.ts` — 迁移确定性 AICC 权限回归。
- Modify: `docs/local-development.md` — 记录三级入口、worker 覆盖和 slow 前置条件。

实施时为每个新增文件、类型、字段、函数和非显然代码段补充相邻中文注释，说明运行边界、安全约束和失败语义；以下代码块聚焦接口与行为，注释要求不得因复制代码块而省略。所有新增测试方法、子测试和 table-driven 数据继续遵守仓库的相邻中文场景注释规范。

### Task 1: 建立 suite 配置契约

**Files:**
- Create: `web/tests/e2e/suite.ts`
- Create: `web/tests/e2e/suite.test.ts`

- [ ] **Step 1: 写 suite 解析和 worker 预算的失败测试**

```ts
// web/tests/e2e/suite.test.ts
import { describe, expect, it } from 'vitest'

import { authStatePath, parseE2ESuite, resolveWorkerCount, suiteGrep } from './suite'

describe('E2E suite 配置', () => {
  // 场景：未显式选择时使用 regression，避免裸命令意外运行 slow。
  it('默认选择 regression', () => {
    expect(parseE2ESuite(undefined)).toBe('regression')
  })

  // 场景：非法 suite 必须立即失败，避免错误值静默扩大测试范围。
  it('拒绝未知 suite', () => {
    expect(() => parseE2ESuite('all')).toThrow('未知 E2E suite: all')
  })

  // 场景：quick/regression 默认双 worker，slow 固定单 worker保护共享基础设施。
  it.each([
    // quick 使用双 worker满足一分钟反馈预算。
    ['quick', undefined, 2],
    // regression 使用双 worker作为稳定并行起点。
    ['regression', undefined, 2],
    // slow 即使传入更大值也保持单 worker。
    ['slow', '4', 1],
    // 低资源机器可把 regression 显式降为单 worker。
    ['regression', '1', 1],
  ] as const)('%s 解析 worker 数', (suite, override, expected) => {
    expect(resolveWorkerCount(suite, override)).toBe(expected)
  })

  // 场景：worker 覆盖值越界时快速失败，避免资源池与并发数不一致。
  it.each(['0', '5', 'abc'])('拒绝非法 worker 值 %s', (value) => {
    expect(() => resolveWorkerCount('regression', value)).toThrow('1 到 4')
  })

  // 场景：三个 suite 分别使用正向或反向标签过滤。
  it('返回稳定的标签过滤规则', () => {
    expect(suiteGrep('quick')).toEqual({ grep: /@quick/ })
    expect(suiteGrep('regression')).toEqual({ grepInvert: /@slow/ })
    expect(suiteGrep('slow')).toEqual({ grep: /@slow/ })
  })

  // 场景：认证路径包含 run、worker 和角色，禁止跨边界复用。
  it('认证状态路径包含全部隔离维度', () => {
    expect(authStatePath('run-a', 1, 'org_admin')).toContain('run-a/worker-1-org_admin.json')
  })
})
```

- [ ] **Step 2: 运行测试并确认因模块缺失失败**

Run: `cd web && npx vitest run tests/e2e/suite.test.ts`

Expected: FAIL，包含 `Cannot find module './suite'`。

- [ ] **Step 3: 实现 suite 纯函数**

```ts
// web/tests/e2e/suite.ts
import { resolve } from 'node:path'

export type E2ESuite = 'quick' | 'regression' | 'slow'

export function parseE2ESuite(value: string | undefined): E2ESuite {
  const suite = value ?? 'regression'
  if (suite !== 'quick' && suite !== 'regression' && suite !== 'slow') {
    throw new Error(`未知 E2E suite: ${suite}`)
  }
  return suite
}

export function resolveWorkerCount(suite: E2ESuite, value: string | undefined): number {
  if (suite === 'slow') return 1
  if (value === undefined) return 2
  const workers = Number.parseInt(value, 10)
  if (!Number.isInteger(workers) || workers < 1 || workers > 4) {
    throw new Error(`OCM_E2E_WORKERS 必须是 1 到 4 的整数，实际为 ${value}`)
  }
  return workers
}

export function suiteGrep(suite: E2ESuite): { grep?: RegExp, grepInvert?: RegExp } {
  if (suite === 'quick') return { grep: /@quick/ }
  if (suite === 'slow') return { grep: /@slow/ }
  return { grepInvert: /@slow/ }
}

export function authStatePath(runID: string, workerIndex: number, role: string): string {
  return resolve('test-results', '.auth', runID, `worker-${workerIndex}-${role}.json`)
}
```

- [ ] **Step 4: 运行测试并确认通过**

Run: `cd web && npx vitest run tests/e2e/suite.test.ts`

Expected: PASS。

- [ ] **Step 5: 提交 suite 配置契约**

```bash
git add web/tests/e2e/suite.ts web/tests/e2e/suite.test.ts
git commit -m "test(e2e): 定义三级测试套件配置" -m "增加 quick、regression 与 slow 的解析、标签过滤和 worker 数约束。"
```

### Task 2: 暴露三级命令并锁定用例分类

**Files:**
- Modify: `web/playwright.config.ts`
- Modify: `web/package.json`
- Modify: `Makefile`
- Modify: `web/tests/e2e/login.spec.ts`
- Modify: `web/tests/e2e/locale.spec.ts`
- Modify: `web/tests/e2e/organizations.spec.ts`
- Modify: `web/tests/e2e/members.spec.ts`
- Modify: `web/tests/e2e/app-detail.spec.ts`
- Modify: `web/tests/e2e/console.spec.ts`
- Modify: `web/tests/e2e/delete-cascade.spec.ts`
- Modify: `web/tests/e2e/l4-i18n-sweep.spec.ts`
- Modify: `web/tests/e2e/aicc.spec.ts`
- Modify: `web/tests/e2e/aicc-knowledge.spec.ts`
- Modify: `web/tests/e2e/aicc-conversation-intent.spec.ts`
- Modify: `web/tests/e2e/aicc-conversation-runtime.spec.ts`
- Modify: `web/tests/e2e/aicc-conversation-security.spec.ts`

- [ ] **Step 1: 确认旧用例尚无 quick 标签**

Run: `cd web && npx playwright test --list --project=chromium --grep @quick`

Expected: `Total: 0 tests`。

- [ ] **Step 2: 在 Playwright 配置中接入 suite 与 worker**

Replace fixed `fullyParallel`/`workers` and add the suite filter:

```ts
import { parseE2ESuite, resolveWorkerCount, suiteGrep } from './tests/e2e/suite'

const suite = parseE2ESuite(process.env.OCM_E2E_SUITE)
const workers = resolveWorkerCount(suite, process.env.OCM_E2E_WORKERS)

export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: suite !== 'slow',
  retries: 0,
  workers,
  ...suiteGrep(suite),
  reporter: [['list']],
  timeout: 30_000,
  globalSetup: './tests/e2e/global-setup.ts',
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? 'http://ocm.localhost',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
    {
      name: 'chrome-headed',
      retries: 1,
      use: {
        ...devices['Desktop Chrome'], channel: 'chrome', headless: false,
        trace: 'on-first-retry', screenshot: 'only-on-failure', video: 'on-first-retry',
      },
    },
  ],
})
```

- [ ] **Step 3: 给 quick 用例添加标签**

For each exact test below, change the declaration prefix from `test('<name>', async` to `test('<name>', { tag: '@quick' }, async`; do not change its callback body:

```text
web/tests/e2e/login.spec.ts — 登录成功后跳转到平台总览
web/tests/e2e/login.spec.ts — 密码错误返回错误提示
web/tests/e2e/locale.spec.ts — 登录页右上角渲染语言选择器
web/tests/e2e/organizations.spec.ts — platform_admin 可创建组织
web/tests/e2e/members.spec.ts — org_admin 使用专用弹窗填写并清理成员新密码
web/tests/e2e/app-detail.spec.ts — 实例详情 5 tab 全部可渲染
web/tests/e2e/console.spec.ts — 平台控制台图表连续切换三轮后仍保持显示
web/tests/e2e/delete-cascade.spec.ts — 删除实例：输错名拒绝，输对名后触发删除请求
```

- [ ] **Step 4: 给专项慢测添加标签**

Apply this exact classification without changing assertions:

| Spec | Tags |
|---|---|
| `l4-i18n-sweep.spec.ts` | `@slow`, `@i18n-sweep` |
| `aicc.spec.ts` | `@slow`, `@model` |
| `aicc-knowledge.spec.ts` | `@slow`, `@model`, `@rag` |
| `aicc-conversation-intent.spec.ts` | `@slow`, `@model` |
| `aicc-conversation-security.spec.ts` | `@slow`, `@model`; knowledge-source tests also `@rag` |
| `aicc-conversation-runtime.spec.ts` | `@slow`, `@model`; Pod/fault tests also `@k8s-disruptive` |

Additionally tag the restricted-domain test `@widget-domain`, the vision-history test `@vision`, and the two intent fixture tests `@intent-retry`. For top-level tests use `const slowModel = { tag: ['@slow', '@model'] }`; for existing describes pass `{ tag: [...] }` as the second argument.

- [ ] **Step 5: 增加 npm 与 Makefile 入口并删除旧入口**

Set `web/package.json` scripts:

```json
{
  "test:e2e:quick": "OCM_E2E_SUITE=quick playwright test --project=chromium",
  "test:e2e:regression": "OCM_E2E_SUITE=regression playwright test --project=chromium",
  "test:e2e:slow": "OCM_E2E_SUITE=slow playwright test --project=chromium",
  "test:e2e:install": "playwright install chromium"
}
```

Remove `test:e2e`. Replace `.PHONY` and the old Make target with:

```make
.PHONY: e2e-quick e2e-regression e2e-slow

e2e-quick: ## 无头运行一分钟内核心 Playwright 冒烟
	cd web && npm run test:e2e:quick

e2e-regression: ## 无头并行运行全部确定性 Playwright 回归
	cd web && npm run test:e2e:regression

e2e-slow: ## 显式运行真实模型、RAG 与破坏性专项慢测
	cd web && npm run test:e2e:slow
```

- [ ] **Step 6: 校验三个清单**

Run:

```bash
cd web
npm run test:e2e:quick -- --list
npm run test:e2e:regression -- --list
npm run test:e2e:slow -- --list
```

Expected: quick 列出上述 8 条；regression 包含 quick 和全部非 slow；slow 只包含 AICC 真实链路与 L4 清扫；均不列 `chrome-headed`。

- [ ] **Step 7: 提交入口与标签**

```bash
git add Makefile web/package.json web/playwright.config.ts web/tests/e2e
git commit -m "test(e2e): 拆分三级浏览器测试入口" -m "区分日常冒烟、确定性回归与真实依赖专项测试。"
```

### Task 3: 将 seed-e2e 改为 fixture 池

**Files:**
- Create: `cmd/seed-e2e/run.go`
- Modify: `cmd/seed-e2e/main.go`
- Modify: `cmd/seed-e2e/main_test.go`

- [ ] **Step 1: 写运行参数与唯一性失败测试**

```go
// 验证默认参数兼容人工直接 seed，并保持单 worker。
func TestLoadRunOptionsUsesSafeDefaults(t *testing.T) {
	t.Setenv("OCM_E2E_RUN_ID", "")
	t.Setenv("OCM_E2E_SUITE", "")
	t.Setenv("OCM_E2E_WORKERS", "")
	opts, err := loadRunOptions()
	require.NoError(t, err)
	assert.Equal(t, runOptions{RunID: "manual", Suite: suiteRegression, Workers: 1, Action: actionSeed}, opts)
}

// 验证三个 worker 的组织、账号与实例命名空间互不相同。
func TestFixtureIdentitiesAreUniquePerWorker(t *testing.T) {
	items, err := fixtureIdentities(runOptions{RunID: "run-abc123", Suite: suiteRegression, Workers: 3, Action: actionSeed})
	require.NoError(t, err)
	require.Len(t, items, 3)
	assert.NotEqual(t, items[0].PlatformAdminLogin, items[1].PlatformAdminLogin)
	assert.NotEqual(t, items[0].OrgCode, items[1].OrgCode)
	assert.NotEqual(t, items[1].OrgAdminLogin, items[2].OrgAdminLogin)
	assert.NotEqual(t, items[0].AppName, items[2].AppName)
}

// 验证越界 worker 数被拒绝。
func TestLoadRunOptionsRejectsInvalidWorkers(t *testing.T) {
	t.Setenv("OCM_E2E_WORKERS", "5")
	_, err := loadRunOptions()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 到 4")
}
```

- [ ] **Step 2: 运行测试并确认新类型缺失**

Run: `go test ./cmd/seed-e2e -run 'TestLoadRunOptions|TestFixtureIdentities' -count=1`

Expected: FAIL，包含 `undefined: loadRunOptions`。

- [ ] **Step 3: 创建运行参数和 pool 类型**

```go
// cmd/seed-e2e/run.go
package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type e2eSuite string
type e2eAction string

const (
	suiteQuick e2eSuite = "quick"
	suiteRegression e2eSuite = "regression"
	suiteSlow e2eSuite = "slow"
	actionSeed e2eAction = "seed"
	actionCleanup e2eAction = "cleanup"
	actionCleanupExpired e2eAction = "cleanup-expired"
)

type runOptions struct { RunID string; Suite e2eSuite; Workers int; Action e2eAction }
type fixturePool struct { RunID string `json:"run_id"`; Suite e2eSuite `json:"suite"`; Fixtures []fixture `json:"fixtures"` }
type fixtureIdentity struct {
	RunID string; WorkerIndex int; OrgName string; OrgCode string
	PlatformAdminLogin string; OrgAdminLogin string; OrgMemberLogin string; AppName string
}

var unsafeRunID = regexp.MustCompile(`[^a-z0-9-]+`)

func loadRunOptions() (runOptions, error) {
	runID := strings.ToLower(strings.TrimSpace(os.Getenv("OCM_E2E_RUN_ID")))
	if runID == "" { runID = "manual" }
	runID = strings.Trim(unsafeRunID.ReplaceAllString(runID, "-"), "-")
	if runID == "" || len(runID) > 16 { return runOptions{}, fmt.Errorf("OCM_E2E_RUN_ID 必须为 1 到 16 个安全字符") }
	suite := e2eSuite(strings.TrimSpace(os.Getenv("OCM_E2E_SUITE")))
	if suite == "" { suite = suiteRegression }
	if suite != suiteQuick && suite != suiteRegression && suite != suiteSlow { return runOptions{}, fmt.Errorf("未知 OCM_E2E_SUITE: %s", suite) }
	action := e2eAction(strings.TrimSpace(os.Getenv("OCM_E2E_ACTION")))
	if action == "" { action = actionSeed }
	if action != actionSeed && action != actionCleanup && action != actionCleanupExpired { return runOptions{}, fmt.Errorf("未知 OCM_E2E_ACTION: %s", action) }
	workers := 1
	if raw := strings.TrimSpace(os.Getenv("OCM_E2E_WORKERS")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 4 { return runOptions{}, fmt.Errorf("OCM_E2E_WORKERS 必须是 1 到 4 的整数，实际为 %s", raw) }
		workers = parsed
	}
	if suite == suiteSlow { workers = 1 }
	return runOptions{RunID: runID, Suite: suite, Workers: workers, Action: action}, nil
}

func fixtureIdentities(opts runOptions) ([]fixtureIdentity, error) {
	items := make([]fixtureIdentity, 0, opts.Workers)
	for index := 0; index < opts.Workers; index++ {
		suffix := fmt.Sprintf("%s-w%d", opts.RunID, index)
		items = append(items, fixtureIdentity{
			RunID: opts.RunID, WorkerIndex: index, OrgName: "e2e-" + suffix, OrgCode: "e2e-" + suffix,
			PlatformAdminLogin: "e2e-" + suffix + "-platform",
			OrgAdminLogin: "e2e-" + suffix + "-admin", OrgMemberLogin: "e2e-" + suffix + "-member", AppName: "e2e-" + suffix + "-app",
		})
	}
	return items, nil
}
```

- [ ] **Step 4: 参数化 buildFixture 并输出 pool**

Add `RunID string` and `WorkerIndex int` JSON fields to `fixture`. Change `buildFixture` to accept `fixtureIdentity` and use its names instead of fixed platform admin/organization/accounts/app. Call `ensurePlatformAdmin` with `identity.PlatformAdminLogin` and the fixed local password inside each worker build, so locale changes cannot cross workers. Name the assistant version `e2e-<run_id>-w<worker_index>-version`, matching scoped cleanup's `e2e-<run_id>-%` predicate.

In `main`, loop identities; each `buildFixture` creates its dedicated platform admin, organization users, version, and app before appending the fixture:

```go
pool := fixturePool{RunID: opts.RunID, Suite: opts.Suite, Fixtures: make([]fixture, 0, len(identities))}
for _, identity := range identities {
	fx, err := buildFixture(ctx, db, runtimeImageID, identity)
	if err != nil { log.Fatalf("构造 worker %d fixture 失败: %v", identity.WorkerIndex, err) }
	pool.Fixtures = append(pool.Fixtures, fx)
}
if err := json.NewEncoder(os.Stdout).Encode(pool); err != nil { log.Fatalf("打印 fixture pool 失败: %v", err) }
```

- [ ] **Step 5: 运行测试和构建**

Run: `go test ./cmd/seed-e2e -count=1 && go build ./cmd/seed-e2e`

Expected: PASS。

- [ ] **Step 6: 提交 fixture 池**

```bash
git add cmd/seed-e2e
git commit -m "test(e2e): 按 worker 生成隔离 fixture 池" -m "为组织、账号、实例和助手版本加入 run_id 与 worker 索引边界。"
```

### Task 4: 增加 suite 感知的 new-api 准备与 scoped cleanup

**Files:**
- Create: `cmd/seed-e2e/cleanup.go`
- Modify: `cmd/seed-e2e/main.go`
- Modify: `cmd/seed-e2e/main_test.go`
- Modify: `Makefile`

- [ ] **Step 1: 写 new-api 命名和清理选择器失败测试**

```go
// 验证 new-api 用户名包含 worker 边界且满足上游 12 字符限制。
func TestE2ENewAPIUsernameIsUniqueAndValid(t *testing.T) {
	first := e2eNewAPIUsername("run-abc123", 0)
	second := e2eNewAPIUsername("run-abc123", 1)
	otherRun := e2eNewAPIUsername("run-abc124", 0)
	assert.NotEqual(t, first, second)
	assert.NotEqual(t, first, otherRun)
	assert.LessOrEqual(t, len(first), 12)
	assert.LessOrEqual(t, len(second), 12)
}

// 验证 cleanup 只匹配当前 run，且拒绝空 run_id。
func TestRunOrgPatternRequiresRunID(t *testing.T) {
	pattern, err := runOrgPattern("run-abc123")
	require.NoError(t, err)
	assert.Equal(t, "e2e-run-abc123-%", pattern)
	_, err = runOrgPattern("")
	require.Error(t, err)
}
```

- [ ] **Step 2: 运行测试并确认新签名缺失**

Run: `go test ./cmd/seed-e2e -run 'TestE2ENewAPIUsername|TestRunOrgPattern' -count=1`

Expected: FAIL，包含参数数量不匹配或 `undefined: runOrgPattern`。

- [ ] **Step 3: 让 new-api 凭据只在 slow 准备**

Change the helper and provision function:

```go
func e2eNewAPIUsername(runID string, workerIndex int) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(runID))
	return fmt.Sprintf("e%07x%02d", hash.Sum32()&0x0fffffff, workerIndex)
}
```

Import `hash/fnv`; the hash avoids collisions between timestamp-based run IDs that share a long prefix while keeping the username under 12 characters.

`provisionE2ENewAPIUser` receives `runID`/`workerIndex`; `main` calls it only when `opts.Suite == suiteSlow`. Quick and regression must not create, recharge, or log in a new-api user.

- [ ] **Step 4: 实现按 run_id 查找组织和显式子表清理**

```go
// cmd/seed-e2e/cleanup.go
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func runOrgPattern(runID string) (string, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" { return "", errors.New("cleanup 禁止使用空 run_id") }
	return fmt.Sprintf("e2e-%s-%%", runID), nil
}

func cleanupRun(ctx context.Context, db *sql.DB, runID string) error {
	pattern, err := runOrgPattern(runID)
	if err != nil { return err }
	rows, err := db.QueryContext(ctx, `SELECT id FROM organizations WHERE code LIKE ?`, pattern)
	if err != nil { return fmt.Errorf("查询 run 组织: %w", err) }
	var orgIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil { return fmt.Errorf("读取 run 组织: %w", err) }
		orgIDs = append(orgIDs, id)
	}
	if err := rows.Err(); err != nil { return fmt.Errorf("遍历 run 组织: %w", err) }
	if err := rows.Close(); err != nil { return fmt.Errorf("关闭 run 组织结果集: %w", err) }
	for _, orgID := range orgIDs {
		if err := cleanupOrganization(ctx, db, orgID); err != nil { return fmt.Errorf("清理组织 %s: %w", orgID, err) }
	}
	if err := cleanupAssistantVersions(ctx, db, runID); err != nil { return err }
	if err := cleanupPlatformAdmins(ctx, db, runID); err != nil { return err }
	return nil
}
```

`cleanupPlatformAdmins` uses the username pattern `e2e-<run_id>-%-platform`, deletes matching actors' audit logs and refresh tokens first, then deletes only those `org_id IS NULL` users. Add a unit test asserting the pattern does not match the permanent local `admin` account.

`cleanupAssistantVersions` deletes `assistant_version_industry_knowledge_bases` and then `assistant_versions` matching `e2e-<run_id>-%`. It runs only after every organization app has been deleted, avoiding cross-worker version foreign-key failures.

Implement `cleanupOrganization(ctx, db, orgID)` using this explicit child-to-parent statement builder; foreign-key checks remain enabled throughout the transaction:

```go
type cleanupStatement struct { query string; args []any }

func cleanupStatements(orgID string) []cleanupStatement {
	bySession := `SELECT id FROM aicc_sessions WHERE org_id = ?`
	byMessage := `SELECT id FROM aicc_messages WHERE session_id IN (` + bySession + `)`
	byAgent := `SELECT id FROM aicc_agents WHERE org_id = ?`
	byApp := `SELECT id FROM apps WHERE org_id = ?`
	byTicket := `SELECT id FROM skill_tickets WHERE org_id = ?`
	return []cleanupStatement{
		{`DELETE FROM aicc_message_sources WHERE message_id IN (` + byMessage + `)`, []any{orgID}},
		{`DELETE FROM aicc_session_contexts WHERE session_id IN (` + bySession + `)`, []any{orgID}},
		{`DELETE FROM aicc_session_intents WHERE session_id IN (` + bySession + `)`, []any{orgID}},
		{`DELETE FROM aicc_intent_analysis_retries WHERE session_id IN (` + bySession + `)`, []any{orgID}},
		{`DELETE FROM aicc_feedback WHERE session_id IN (` + bySession + `)`, []any{orgID}},
		{`DELETE FROM aicc_message_tasks WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM aicc_lead_values WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM aicc_leads WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM aicc_images WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM aicc_messages WHERE session_id IN (` + bySession + `)`, []any{orgID}},
		{`DELETE FROM aicc_sessions WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM aicc_blocked_visitors WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM aicc_agent_settings WHERE agent_id IN (` + byAgent + `)`, []any{orgID}},
		{`DELETE FROM aicc_lead_fields WHERE agent_id IN (` + byAgent + `)`, []any{orgID}},
		{`DELETE FROM aicc_agent_knowledge WHERE agent_org_id = ? OR org_id = ?`, []any{orgID, orgID}},
		{`DELETE FROM aicc_agents WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM organization_industry_knowledge_bases WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM assistant_version_industry_knowledge_bases WHERE version_id IN (SELECT version_id FROM apps WHERE org_id = ?)`, []any{orgID}},
		{`DELETE FROM published_sites WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM conversation_files WHERE app_id IN (` + byApp + `)`, []any{orgID}},
		{`DELETE FROM app_skills WHERE app_id IN (` + byApp + `)`, []any{orgID}},
		{`DELETE FROM channel_bindings WHERE app_id IN (` + byApp + `)`, []any{orgID}},
		{`DELETE FROM ragflow_documents WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM ragflow_datasets WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM custom_skill_targets WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM custom_skills WHERE ticket_id IN (` + byTicket + `)`, []any{orgID}},
		{`DELETE FROM skill_ticket_messages WHERE ticket_id IN (` + byTicket + `)`, []any{orgID}},
		{`DELETE FROM skill_tickets WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM refresh_tokens WHERE user_id IN (SELECT id FROM users WHERE org_id = ?)`, []any{orgID}},
		{`DELETE FROM recharge_records WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM audit_logs WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM jobs WHERE JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.org_id')) = ? OR JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.app_id')) IN (` + byApp + `)`, []any{orgID, orgID}},
		{`DELETE FROM apps WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM users WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM org_web_publish_config WHERE org_id = ?`, []any{orgID}},
		{`DELETE FROM organizations WHERE id = ?`, []any{orgID}},
	}
}

func cleanupOrganization(ctx context.Context, db *sql.DB, orgID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback()
	for _, statement := range cleanupStatements(orgID) {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil { return fmt.Errorf("%s: %w", statement.query, err) }
	}
	return tx.Commit()
}
```

Add a unit test that iterates `cleanupStatements(orgID)` and asserts every query contains `?`, preventing future unscoped additions.

- [ ] **Step 5: 在删除组织前清理对应 new-api 用户**

Add `cleanupNewAPIUsers(ctx, db, cfg, runID)`. It queries `newapi_user_id` from organizations matched by `runOrgPattern`, parses each non-empty ID with `strconv.ParseInt`, and calls `newapi.Client.DeleteUser`; `newapi.ErrNotFound` is accepted, all other failures abort cleanup so the database row remains available for retry. Call this function before `cleanupRun` in the cleanup action. Add a unit-tested pure helper `parseE2ENewAPIUserIDs([]string) ([]int64, error)` covering empty values, valid numeric IDs, and malformed IDs.

Also add `cleanupExpiredRuns(ctx, db, cfg, now.Add(-24*time.Hour))`: select distinct run IDs from fixture organization codes matching `^e2e-(.+)-w[0-9]+$` with `created_at` before the cutoff, then call the same new-api and database cleanup functions. A code such as `e2e-run-abc-w0-c-123456` is cleaned with its owning fixture run, not treated as a new run. Unit-test parsing with run IDs containing hyphens and rejection of non-fixture codes.

- [ ] **Step 6: 用 action 分支替代 Playwright 默认 truncate**

After loading options in `main`:

```go
if opts.Action == actionCleanup {
	if err := cleanupNewAPIUsers(ctx, db, cfg, opts.RunID); err != nil { log.Fatalf("清理 E2E new-api 用户失败: %v", err) }
	if err := cleanupRun(ctx, db, opts.RunID); err != nil { log.Fatalf("清理 E2E run 失败: %v", err) }
	return
}
if opts.Action == actionCleanupExpired {
	if err := cleanupExpiredRuns(ctx, db, cfg, time.Now().Add(-24*time.Hour)); err != nil { log.Fatalf("清理过期 E2E run 失败: %v", err) }
	return
}
if err := cleanupRun(ctx, db, opts.RunID); err != nil { log.Fatalf("清理同名 E2E run 失败: %v", err) }
```

Delete the old `truncate` function and update `requireE2EGuard` to say the command creates or cleans isolated E2E data. Repository search shows no non-Playwright workflow depends on seed-e2e as a full-database reset, so no `reset-all` compatibility path is retained.

- [ ] **Step 7: 让 Makefile 传入运行参数**

```make
local-seed-e2e:
	@$(KUBECTL) -n $(K8S_NS) exec deploy/manager-api -- env \
		OCM_E2E=1 \
		OCM_E2E_ACTION="$${OCM_E2E_ACTION:-seed}" \
		OCM_E2E_RUN_ID="$${OCM_E2E_RUN_ID:-manual}" \
		OCM_E2E_SUITE="$${OCM_E2E_SUITE:-regression}" \
		OCM_E2E_WORKERS="$${OCM_E2E_WORKERS:-1}" \
		seed-e2e

cleanup-e2e: ## 仅清理指定 OCM_E2E_RUN_ID 的测试数据
	@OCM_E2E_ACTION=cleanup $(MAKE) local-seed-e2e

cleanup-e2e-expired: ## 清理超过 24 小时的 E2E 数据
	@OCM_E2E_ACTION=cleanup-expired $(MAKE) local-seed-e2e
```

Add `cleanup-e2e` and `cleanup-e2e-expired` to `.PHONY`.

- [ ] **Step 8: 运行 Go 验证并提交**

Run: `go test ./cmd/seed-e2e -count=1 && go build ./cmd/seed-e2e && git diff --check`

Expected: PASS，`git diff --check` 无输出。

```bash
git add cmd/seed-e2e Makefile
git commit -m "test(e2e): 按运行边界准备和清理数据" -m "移除 Playwright 对全表 truncate 的依赖，并仅为 slow fixture 准备 new-api 凭据。"
```

### Task 5: 接入 fixture pool 与 worker 映射

**Files:**
- Modify: `web/tests/e2e/suite.ts`
- Modify: `web/tests/e2e/suite.test.ts`
- Modify: `web/tests/e2e/global-setup.ts`
- Modify: `web/tests/e2e/fixtures.ts`

- [ ] **Step 1: 写 pool 解析和越界失败测试**

```ts
import { fixtureForWorker, parseFixturePool } from './suite'

const poolJSON = JSON.stringify({ run_id: 'run-a', suite: 'regression', fixtures: [
  { run_id: 'run-a', worker_index: 0, org_id: 'org-0' },
  { run_id: 'run-a', worker_index: 1, org_id: 'org-1' },
] })

// 场景：合法 pool 按 worker 索引返回唯一 fixture。
it('按 worker 索引选择 fixture', () => {
  const pool = parseFixturePool<{ worker_index: number, org_id: string }>(poolJSON)
  expect(fixtureForWorker(pool, 1).org_id).toBe('org-1')
})

// 场景：worker 越界立即失败，不退化到 worker 0。
it('拒绝越界 worker', () => {
  const pool = parseFixturePool<{ worker_index: number, org_id: string }>(poolJSON)
  expect(() => fixtureForWorker(pool, 2)).toThrow('worker 2')
})
```

- [ ] **Step 2: 运行测试并确认函数缺失**

Run: `cd web && npx vitest run tests/e2e/suite.test.ts`

Expected: FAIL，包含 `fixtureForWorker is not a function`。

- [ ] **Step 3: 实现 pool 类型与选择**

```ts
export interface FixturePool<T> { run_id: string; suite: E2ESuite; fixtures: T[] }

export function parseFixturePool<T>(raw: string): FixturePool<T> {
  const parsed = JSON.parse(raw) as Partial<FixturePool<T>>
  if (!parsed.run_id || !parsed.suite || !Array.isArray(parsed.fixtures) || parsed.fixtures.length === 0) {
    throw new Error('seed-e2e 未返回合法 fixture pool')
  }
  return parsed as FixturePool<T>
}

export function fixtureForWorker<T extends { worker_index: number }>(pool: FixturePool<T>, workerIndex: number): T {
  const fixture = pool.fixtures.find(item => item.worker_index === workerIndex)
  if (!fixture) throw new Error(`fixture pool 不包含 worker ${workerIndex}`)
  return fixture
}
```

- [ ] **Step 4: 改造 global setup 一次生成 pool**

Keep NO_PROXY handling; remove the `OCM_E2E_NO_SEED` branch and all unconditional kubectl delete/wait calls. Every supported suite must receive a valid isolated pool; one-off operational browser checks that must preserve hand-built data stay outside these three suite commands. Execute seed with explicit environment:

```ts
const suite = parseE2ESuite(process.env.OCM_E2E_SUITE)
const workers = resolveWorkerCount(suite, process.env.OCM_E2E_WORKERS)
const runID = `run-${Date.now().toString(36)}`
const stdout = execFileSync('make', ['seed-e2e'], {
  cwd: repoRoot,
  env: { ...process.env, OCM_E2E_ACTION: 'seed', OCM_E2E_RUN_ID: runID, OCM_E2E_SUITE: suite, OCM_E2E_WORKERS: String(workers) },
  encoding: 'utf8',
})
const fixtureLine = [...stdout.trim().split(/\r?\n/)].reverse().find(line => line.startsWith('{'))
if (!fixtureLine) throw new Error(`seed-e2e 输出未找到 fixture pool；完整输出：\n${stdout}`)
const pool = parseFixturePool<E2EFixture>(fixtureLine)
if (pool.run_id !== runID || pool.fixtures.length !== workers) {
  throw new Error(`fixture pool 与运行参数不一致：run=${pool.run_id} fixtures=${pool.fixtures.length}`)
}
process.env.OCM_E2E_RUN_ID = runID
process.env.OCM_E2E_FIXTURE_POOL = JSON.stringify(pool)
```

Wrap seed parsing and later auth-state generation in `try/catch`. If setup fails after `runID` is allocated, invoke `make cleanup-e2e` with that run ID, report any cleanup failure as an attached message, then rethrow the original setup error. This prevents setup failures from leaking data while preserving their primary cause.

- [ ] **Step 5: 把 fixtures.ts 改为 worker-scoped fixture**

Extend `E2EFixture` with `run_id` and `worker_index`; remove stale `node_id`/`node_name` fields because Go fixture and the post-node schema no longer provide them. Then export:

```ts
import { test as base } from '@playwright/test'
import { fixtureForWorker, parseFixturePool } from './suite'

type E2EWorkerFixtures = { e2eFixture: E2EFixture }

export const test = base.extend<{}, E2EWorkerFixtures>({
  e2eFixture: [async ({}, use, workerInfo) => {
    const raw = process.env.OCM_E2E_FIXTURE_POOL
    if (!raw) throw new Error('OCM_E2E_FIXTURE_POOL 未注入')
    await use(fixtureForWorker(parseFixturePool<E2EFixture>(raw), workerInfo.parallelIndex))
  }, { scope: 'worker' }],
})

export { expect } from '@playwright/test'
```

Keep `loginAs` for tests whose purpose includes real UI login, but new/migrated business specs must prefer injected fixtures.

- [ ] **Step 6: 运行支持层验证并提交**

Run: `cd web && npx vitest run tests/e2e/suite.test.ts && npm run test:e2e:quick -- --list`

Expected: PASS，quick 仍列 8 条。

```bash
git add web/tests/e2e/suite.ts web/tests/e2e/suite.test.ts web/tests/e2e/global-setup.ts web/tests/e2e/fixtures.ts
git commit -m "test(e2e): 按 worker 分配 fixture" -m "global setup 一次生成 fixture pool，并拒绝越界或共享数据。"
```

### Task 6: 生成并复用角色认证状态

**Files:**
- Create: `web/tests/e2e/auth-state.ts`
- Create: `web/tests/e2e/auth-state.test.ts`
- Modify: `web/tests/e2e/global-setup.ts`
- Modify: `web/tests/e2e/fixtures.ts`
- Modify: `web/tests/e2e/app-detail.spec.ts`
- Modify: `web/tests/e2e/console.spec.ts`
- Modify: `web/tests/e2e/delete-cascade.spec.ts`
- Modify: `web/tests/e2e/members.spec.ts`
- Modify: `web/tests/e2e/organizations.spec.ts`
- Modify: `web/tests/e2e/login.spec.ts`

- [ ] **Step 1: 写 storage state 失败测试**

```ts
// web/tests/e2e/auth-state.test.ts
import { describe, expect, it } from 'vitest'
import { buildStorageState } from './auth-state'

describe('E2E 认证状态', () => {
  // 场景：token 与 CSRF cookie 同时写入，后续写操作可通过双提交校验。
  it('生成 localStorage token 与登录 cookie', () => {
    const state = buildStorageState('http://ocm.localhost', { access_token: 'access', refresh_token: 'refresh' }, [
      { name: 'csrf_token', value: 'csrf', domain: 'ocm.localhost', path: '/', expires: -1, httpOnly: false, secure: false, sameSite: 'Lax' },
    ], 'zh')
    expect(state.origins[0].localStorage).toEqual(expect.arrayContaining([
      { name: 'ocm.access_token', value: 'access' }, { name: 'ocm.refresh_token', value: 'refresh' }, { name: 'ocm.locale', value: 'zh' },
    ]))
    expect(state.cookies[0].name).toBe('csrf_token')
  })
})
```

- [ ] **Step 2: 运行测试并确认模块缺失**

Run: `cd web && npx vitest run tests/e2e/auth-state.test.ts`

Expected: FAIL，包含 `Cannot find module './auth-state'`。

- [ ] **Step 3: 实现登录 API 和 storage state**

```ts
// web/tests/e2e/auth-state.ts
import { mkdir, writeFile } from 'node:fs/promises'
import { dirname } from 'node:path'
import { request, type FullConfig, type StorageState } from '@playwright/test'
import type { E2EFixture } from './fixtures'
import { authStatePath } from './suite'

type TokenPair = { access_token: string, refresh_token: string }

export function buildStorageState(baseURL: string, tokens: TokenPair, cookies: StorageState['cookies'], locale: 'zh' | 'en'): StorageState {
  return { cookies, origins: [{ origin: new URL(baseURL).origin, localStorage: [
    { name: 'ocm.access_token', value: tokens.access_token },
    { name: 'ocm.refresh_token', value: tokens.refresh_token },
    { name: 'ocm.locale', value: locale },
  ] }] }
}

export async function writeWorkerAuthStates(config: FullConfig, fixture: E2EFixture): Promise<void> {
  const baseURL = config.projects[0].use.baseURL as string
  for (const role of ['platform_admin', 'org_admin', 'org_member'] as const) {
    const credentials = {
      platform_admin: { username: fixture.platform_admin_login, password: fixture.platform_admin_password },
      org_admin: { org_code: fixture.org_code, username: fixture.org_admin_login, password: fixture.org_admin_password },
      org_member: { org_code: fixture.org_code, username: fixture.org_member_login, password: fixture.org_member_password },
    }[role]
    const api = await request.newContext({ baseURL })
    try {
      const response = await api.post('/api/v1/auth/login', { data: credentials })
      if (!response.ok()) throw new Error(`${role} 登录失败: HTTP ${response.status()} ${await response.text()}`)
      const payload = await response.json() as { tokens: TokenPair }
      const state = await api.storageState()
      const path = authStatePath(fixture.run_id, fixture.worker_index, role)
      await mkdir(dirname(path), { recursive: true })
      await writeFile(path, JSON.stringify(buildStorageState(baseURL, payload.tokens, state.cookies, 'zh')), 'utf8')
    } finally { await api.dispose() }
  }
}
```

- [ ] **Step 4: 在 global setup 为每份 fixture 生成状态**

Change setup signature to accept `FullConfig`, then after pool validation:

```ts
for (const fixture of pool.fixtures) await writeWorkerAuthStates(config, fixture)
```

- [ ] **Step 5: 在 fixtures.ts 提供角色页面**

```ts
type RolePages = { platformAdminPage: Page; orgAdminPage: Page; orgMemberPage: Page }

async function useRolePage(browser: Browser, fixture: E2EFixture, role: 'platform_admin' | 'org_admin' | 'org_member', use: (page: Page) => Promise<void>): Promise<void> {
  const context = await browser.newContext({ storageState: authStatePath(fixture.run_id, fixture.worker_index, role) })
  try { await use(await context.newPage()) } finally { await context.close() }
}

export const test = base.extend<RolePages, E2EWorkerFixtures>({
  // 保留 e2eFixture 定义。
  platformAdminPage: async ({ browser, e2eFixture }, use) => useRolePage(browser, e2eFixture, 'platform_admin', use),
  orgAdminPage: async ({ browser, e2eFixture }, use) => useRolePage(browser, e2eFixture, 'org_admin', use),
  orgMemberPage: async ({ browser, e2eFixture }, use) => useRolePage(browser, e2eFixture, 'org_member', use),
})
```

- [ ] **Step 6: 迁移 quick 业务 spec**

Import `test`/`expect` from `./fixtures`; use `platformAdminPage` for console/organizations and `orgAdminPage` for members/app-detail/delete-cascade. Destructure `e2eFixture: fx`, remove `loadE2EFixture` and `loginAs`. Make the created organization name run-scoped:

```ts
const unique = `${fx.worker_index}${Date.now().toString(36).slice(-5)}`
const name = `e2e-${unique}-org`
const code = `${fx.org_code}-c-${unique}`
```

Fill the organization-name field with `name`, the organization-code field with `code`, and derive the initial admin login from `unique`. This keeps the new-api display name below 20 characters while the code remains matched by run-scoped cleanup.

`login.spec.ts` also imports the custom `test` to receive `e2eFixture`, but continues using the unauthenticated base `page`. Replace hard-coded `admin2`/`Admin@1234` with `fx.platform_admin_login`/`fx.platform_admin_password`; the failure case uses the same fixture username plus `wrong-password`. Keep both tests on the real login form, and use bilingual anchored label/button regexes already established in `loginAs` so persisted platform locale cannot make quick flaky.

- [ ] **Step 7: 运行认证单测和双 worker quick**

Run: `cd web && npx vitest run tests/e2e/auth-state.test.ts tests/e2e/suite.test.ts && npm run test:e2e:quick -- console.spec.ts members.spec.ts --workers=2`

Expected: PASS；两个 spec 使用不同 worker fixture。

- [ ] **Step 8: 提交认证复用**

```bash
git add web/tests/e2e
git commit -m "test(e2e): 复用 worker 级角色认证状态" -m "登录 API 一次生成带 CSRF cookie 的 storage state，业务 spec 不再重复 UI 登录。"
```

### Task 7: 迁移 regression 并验证双 worker 隔离

**Files:**
- Modify: `web/tests/e2e/locale.spec.ts`
- Modify: `web/tests/e2e/aicc-access-i18n.spec.ts`
- Create: `web/tests/e2e/worker-isolation.spec.ts`

- [ ] **Step 1: 写双 worker 数据隔离浏览器测试**

```ts
// web/tests/e2e/worker-isolation.spec.ts
import { expect, test } from './fixtures'

// 场景：并行 worker 修改各自管理员语言时，只能读取本 worker 的组织和账号。
test('并行 worker 的组织与用户偏好互不串扰', async ({ orgAdminPage: page, e2eFixture: fx }) => {
  await page.goto('/members')
  await expect(page.locator('body')).toContainText(fx.org_member_login)
  await page.getByRole('button', { name: /^(Language|语言|English|简体中文)$/ }).click()
  await page.locator('.n-dropdown-option', { hasText: 'English' }).click()
  await page.reload()
  await expect(page.getByRole('button', { name: /English/ })).toBeVisible()
  expect(fx.worker_index).toBe(test.info().parallelIndex)
})
```

- [ ] **Step 2: 运行双 worker 重复用例**

Run: `cd web && npm run test:e2e:regression -- worker-isolation.spec.ts --workers=2 --repeat-each=2`

Expected: PASS；四次执行分配到两个不同组织，无共享 worker 0。

- [ ] **Step 3: 迁移 locale 持久化用例**

Keep the three unauthenticated login-page tests unchanged. Authenticated locale tests import the custom `test`, use `e2eFixture`, and begin from the matching role storage state. Tests whose purpose is logout/re-login must still exercise real logout and UI login after the initial context. Remove all `try/catch + test.skip` branches for missing fixture: configured runs fail in global setup instead of reporting skipped persistence tests.

- [ ] **Step 4: 迁移 AICC 管理入口回归**

Use `platformAdminPage` for platform entry, `orgAdminPage` for admin entry/i18n, and `orgMemberPage` for denial. A test requiring two roles uses separate authenticated contexts rather than clearing localStorage in one context. Preserve all response and permission assertions.

- [ ] **Step 5: 运行最小 regression 集合**

Run: `cd web && npm run test:e2e:regression -- worker-isolation.spec.ts locale.spec.ts aicc-access-i18n.spec.ts --workers=2`

Expected: PASS，无 skipped，日志中两个 worker 的 org code 不同。

- [ ] **Step 6: 提交 regression 迁移**

```bash
git add web/tests/e2e/locale.spec.ts web/tests/e2e/aicc-access-i18n.spec.ts web/tests/e2e/worker-isolation.spec.ts
git commit -m "test(e2e): 隔离确定性回归状态" -m "让语言持久化和 AICC 权限入口使用独立 worker fixture，并覆盖双 worker 写状态不串扰。"
```

### Task 8: 增加 slow 前置检查与破坏性互斥

**Files:**
- Create: `web/tests/e2e/preflight.ts`
- Create: `web/tests/e2e/preflight.test.ts`
- Modify: `web/tests/e2e/global-setup.ts`
- Modify: `web/tests/e2e/aicc-conversation-intent.spec.ts`
- Modify: `web/tests/e2e/aicc-conversation-runtime.spec.ts`
- Modify: `web/tests/e2e/aicc-conversation-security.spec.ts`

- [ ] **Step 1: 写 slow 缺失依赖失败测试**

```ts
// web/tests/e2e/preflight.test.ts
import { describe, expect, it } from 'vitest'
import { missingSlowRequirements } from './preflight'

describe('slow 前置检查', () => {
  // 场景：模型慢测必须显式授权真实会话，避免整组 skip 形成假绿色。
  it('报告模型会话开关缺失', () => {
    expect(missingSlowRequirements(['@model'], {})).toContain('OCM_AICC_CONVERSATION_E2E=1')
  })

  // 场景：破坏性测试必须具备本地 context 和故障注入授权。
  it('报告破坏性依赖缺失', () => {
    expect(missingSlowRequirements(['@k8s-disruptive'], { kubernetesContext: 'prod' })).toEqual(expect.arrayContaining([
      'kubectl context 必须为 k3d-ocm', 'OCM_AICC_FAULT_INJECTION=1',
    ]))
  })

  // 场景：vision 子集同时要求真实会话总开关与可重复图片 fixture。
  it('报告 vision fixture 缺失', () => {
    expect(missingSlowRequirements(['@vision'], {})).toEqual(expect.arrayContaining([
      'OCM_AICC_CONVERSATION_E2E=1', 'OCM_AICC_VISION_FIXTURE=1',
    ]))
  })
})
```

- [ ] **Step 2: 运行测试并确认模块缺失**

Run: `cd web && npx vitest run tests/e2e/preflight.test.ts`

Expected: FAIL，包含 `Cannot find module './preflight'`。

- [ ] **Step 3: 实现前置检查**

```ts
// web/tests/e2e/preflight.ts
import { execFileSync } from 'node:child_process'

type Input = Record<string, string | undefined> & { kubernetesContext?: string }

export function missingSlowRequirements(tags: string[], input: Input): string[] {
  const missing: string[] = []
  const needsConversation = tags.some(tag => ['@model', '@rag', '@vision', '@intent-retry'].includes(tag))
  if (needsConversation && input.OCM_AICC_CONVERSATION_E2E !== '1') missing.push('OCM_AICC_CONVERSATION_E2E=1')
  if (tags.includes('@rag') && input.OCM_AICC_KNOWLEDGE_FIXTURE !== '1') missing.push('OCM_AICC_KNOWLEDGE_FIXTURE=1')
  if (tags.includes('@widget-domain') && input.OCM_AICC_WIDGET_DOMAIN_FIXTURE !== '1') missing.push('OCM_AICC_WIDGET_DOMAIN_FIXTURE=1')
  if (tags.includes('@vision') && input.OCM_AICC_VISION_FIXTURE !== '1') missing.push('OCM_AICC_VISION_FIXTURE=1')
  if (tags.includes('@intent-retry') && input.OCM_AICC_INTENT_RETRY_FIXTURE !== '1') missing.push('OCM_AICC_INTENT_RETRY_FIXTURE=1')
  if (tags.includes('@k8s-disruptive')) {
    if (input.kubernetesContext !== 'k3d-ocm') missing.push('kubectl context 必须为 k3d-ocm')
    if (input.OCM_AICC_FAULT_INJECTION !== '1') missing.push('OCM_AICC_FAULT_INJECTION=1')
  }
  return missing
}

export function assertSlowPreflight(tags: string[]): void {
  const kubernetesContext = execFileSync('kubectl', ['config', 'current-context'], { encoding: 'utf8' }).trim()
  const missing = missingSlowRequirements(tags, { ...process.env, kubernetesContext })
  if (missing.length > 0) throw new Error(`slow E2E 前置条件缺失：\n- ${missing.join('\n- ')}`)
}
```

- [ ] **Step 4: 在 global setup 执行 selected-tag preflight**

When suite is slow, parse `OCM_E2E_SLOW_TAGS` as comma-separated tags. Default to `@model,@rag,@k8s-disruptive,@i18n-sweep,@widget-domain,@vision,@intent-retry` and call `assertSlowPreflight` before seed. The runner in Task 10 keeps this variable synchronized with `--grep`; quick/regression never execute kubectl context or slow dependency checks.

- [ ] **Step 5: 删除组级环境 skip 并保留安全恢复**

Remove group-level `OCM_AICC_CONVERSATION_E2E` skips now enforced by preflight. Parse `const selectedSlowTags = new Set((process.env.OCM_E2E_SLOW_TAGS ?? '').split(','))`; special domain/vision/intent tests use `test.skip(!selectedSlowTags.has('@vision'), '本次未选择 @vision')` and the equivalent exact capability tag. When selected, preflight requires its fixture and the test executes. Keep `workers: 1`; keep `assertLocalK3DContext()` before mutation and `try/finally` around `setLocalAICCIntentFailureOnce` so manager-api environment is restored.

- [ ] **Step 6: 运行单测和清单验证**

Run: `cd web && npx vitest run tests/e2e/preflight.test.ts && OCM_E2E_SUITE=slow OCM_E2E_SLOW_TAGS=@k8s-disruptive npx playwright test --list --project=chromium`

Expected: Vitest PASS；清单只列破坏性子标签且不执行场景。

- [ ] **Step 7: 提交 slow 前置检查**

```bash
git add web/tests/e2e
git commit -m "test(e2e): 为专项慢测增加前置检查" -m "按模型、RAG 与 Kubernetes 破坏标签快速报告缺失条件，避免整组 skip 形成假绿色。"
```

### Task 9: 增加结果标记、teardown 与耗时报告

**Files:**
- Create: `web/tests/e2e/global-teardown.ts`
- Create: `web/tests/e2e/timing-reporter.ts`
- Modify: `web/tests/e2e/global-setup.ts`
- Modify: `web/playwright.config.ts`
- Modify: `web/tests/e2e/suite.test.ts`

- [ ] **Step 1: 写耗时排序失败测试**

```ts
import { slowestSpecs } from './timing-reporter'

// 场景：报告按 spec 累计耗时降序，优先暴露真实热点。
it('按 spec 总耗时排序', () => {
  expect(slowestSpecs([
    { file: 'a.spec.ts', duration: 100 }, { file: 'b.spec.ts', duration: 300 }, { file: 'a.spec.ts', duration: 50 },
  ], 2)).toEqual([{ file: 'b.spec.ts', duration: 300 }, { file: 'a.spec.ts', duration: 150 }])
})
```

- [ ] **Step 2: 实现 reporter 和结果文件**

```ts
// web/tests/e2e/timing-reporter.ts
import { mkdirSync, writeFileSync } from 'node:fs'
import type { FullConfig, FullResult, Reporter, Suite, TestCase, TestError, TestResult } from '@playwright/test/reporter'

export type Timing = { file: string, duration: number }
export function slowestSpecs(items: Timing[], limit = 10): Timing[] {
  const totals = new Map<string, number>()
  for (const item of items) totals.set(item.file, (totals.get(item.file) ?? 0) + item.duration)
  return [...totals].map(([file, duration]) => ({ file, duration })).sort((a, b) => b.duration - a.duration).slice(0, limit)
}

export default class TimingReporter implements Reporter {
  private readonly startedAt = Date.now()
  private readonly timings: Timing[] = []
  private markFailed(): void {
    mkdirSync('test-results', { recursive: true })
    writeFileSync('test-results/e2e-failed', '1', 'utf8')
  }
  onBegin(_config: FullConfig, suite: Suite): void { if (suite.allTests().length === 0) this.markFailed() }
  onError(_error: TestError): void { this.markFailed() }
  onTestEnd(test: TestCase, result: TestResult): void {
    this.timings.push({ file: test.location.file, duration: result.duration })
    if (result.status !== 'passed' && result.status !== 'skipped') this.markFailed()
  }
  onEnd(_result: FullResult): void {
    console.log(`E2E 总耗时: ${Date.now() - this.startedAt}ms`)
    console.log('最慢 spec:')
    for (const item of slowestSpecs(this.timings)) console.log(`  ${item.duration}ms ${item.file}`)
  }
}
```

- [ ] **Step 3: 在 setup 开始时清除上次失败标记**

Before seed in `global-setup.ts`, clear the marker and remove resources older than 24 hours:

```ts
rmSync(resolve(here, '../../test-results/e2e-failed'), { force: true })
deleteRunKubernetesResources(listExpiredAppIDs())
execFileSync('make', ['cleanup-e2e-expired'], { cwd: repoRoot, env: process.env, stdio: 'inherit' })
```

Import `rmSync` from `node:fs` and the two cleanup helpers from `global-teardown.ts`. Reporter 的 `onTestEnd`/`onError` 发生在 global teardown 之前，因此本轮失败标记可供 teardown 读取；`onBegin` 同时覆盖零用例错误。

- [ ] **Step 4: 实现按结果清理或保留的 teardown**

```ts
// web/tests/e2e/global-teardown.ts
import { execFileSync } from 'node:child_process'
import { existsSync, rmSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { authStatePath } from './suite'

export function listRunAppIDs(runID: string): string[] {
  const context = execFileSync('kubectl', ['config', 'current-context'], { encoding: 'utf8' }).trim()
  if (context !== 'k3d-ocm') throw new Error(`E2E cleanup 只允许 k3d-ocm，当前为 ${context}`)
  const pattern = `e2e-${runID}-%`
  const output = execFileSync('kubectl', [
    '--context', 'k3d-ocm', '-n', 'ocm', 'exec', 'mysql-0', '--', 'sh', '-c',
    `mysql -uroot -p"$MYSQL_ROOT_PASSWORD" ocm -N -e "SELECT a.id FROM apps a JOIN organizations o ON o.id=a.org_id WHERE o.code LIKE '${pattern}'" 2>/dev/null`,
  ], { encoding: 'utf8' })
  return output.split(/\r?\n/).map(value => value.trim()).filter(Boolean)
}

export function listExpiredAppIDs(): string[] {
  const context = execFileSync('kubectl', ['config', 'current-context'], { encoding: 'utf8' }).trim()
  if (context !== 'k3d-ocm') throw new Error(`E2E cleanup 只允许 k3d-ocm，当前为 ${context}`)
  const output = execFileSync('kubectl', [
    '--context', 'k3d-ocm', '-n', 'ocm', 'exec', 'mysql-0', '--', 'sh', '-c',
    `mysql -uroot -p"$MYSQL_ROOT_PASSWORD" ocm -N -e "SELECT a.id FROM apps a JOIN organizations o ON o.id=a.org_id WHERE o.code LIKE 'e2e-%' AND o.created_at < DATE_SUB(NOW(), INTERVAL 24 HOUR)" 2>/dev/null`,
  ], { encoding: 'utf8' })
  return output.split(/\r?\n/).map(value => value.trim()).filter(Boolean)
}

export function deleteRunKubernetesResources(appIDs: string[]): void {
  for (const appID of appIDs) {
    for (const namespace of ['oc-apps', 'oc-aicc']) {
      execFileSync('kubectl', [
        '--context', 'k3d-ocm', '-n', namespace, 'delete',
        'deployment,service,secret,networkpolicy,horizontalpodautoscaler',
        '-l', `app=${appID}`, '--ignore-not-found=true', '--wait=true',
      ], { stdio: 'inherit' })
    }
  }
}

export default async function globalTeardown(): Promise<void> {
  const runID = process.env.OCM_E2E_RUN_ID
  if (!runID) throw new Error('global teardown 缺少 OCM_E2E_RUN_ID')
  if (existsSync('test-results/e2e-failed')) {
    console.error(`保留失败 E2E 资源：run_id=${runID}`)
    return
  }
  const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../..')
  try {
    deleteRunKubernetesResources(listRunAppIDs(runID))
    execFileSync('make', ['cleanup-e2e'], { cwd: repoRoot, env: { ...process.env, OCM_E2E_RUN_ID: runID }, stdio: 'inherit' })
  } finally {
    rmSync(resolve(authStatePath(runID, 0, 'platform_admin'), '..'), { recursive: true, force: true })
  }
}
```

- [ ] **Step 5: 接入 teardown 与 reporter**

```ts
globalTeardown: './tests/e2e/global-teardown.ts',
reporter: [['list'], ['./tests/e2e/timing-reporter.ts']],
```

- [ ] **Step 6: 运行单测和定向 quick**

Run: `cd web && npx vitest run tests/e2e/suite.test.ts && npm run test:e2e:quick -- console.spec.ts`

Expected: PASS；输出总耗时和最慢 spec；成功 run 被清理。

- [ ] **Step 7: 提交 teardown 与报告**

```bash
git add web/playwright.config.ts web/tests/e2e/global-setup.ts web/tests/e2e/global-teardown.ts web/tests/e2e/timing-reporter.ts web/tests/e2e/suite.test.ts
git commit -m "test(e2e): 增加运行清理与耗时排行" -m "成功时按 run_id 清理，失败时保留诊断标识，并输出总耗时和最慢 spec。"
```

### Task 10: 透传定向参数并保留退出码

**Files:**
- Create: `web/tests/e2e/run-suite.mjs`
- Modify: `web/package.json`
- Modify: `web/tests/e2e/suite.ts`
- Modify: `web/tests/e2e/suite.test.ts`
- Modify: `web/playwright.config.ts`

- [ ] **Step 1: 先扩展 suite 过滤器以组合用户 grep**

Add tests proving quick plus a user pattern still requires `@quick`, regression retains `grepInvert: /@slow/`, and slow plus `@rag` requires both `@slow` and `@rag`. Change `suiteGrep` to:

```ts
export function suiteGrep(suite: E2ESuite, userPattern?: string): { grep?: RegExp, grepInvert?: RegExp } {
  const user = userPattern ? `(?=.*(?:${userPattern}))` : ''
  if (suite === 'quick') return { grep: new RegExp(`(?=.*@quick)${user}`) }
  if (suite === 'slow') return { grep: new RegExp(`(?=.*@slow)${user}`) }
  if (userPattern) return { grep: new RegExp(userPattern), grepInvert: /@slow/ }
  return { grepInvert: /@slow/ }
}
```

In `playwright.config.ts`, call `suiteGrep(suite, process.env.OCM_E2E_USER_GREP)`. Omit an undefined `grep` property in the regression/no-user case so the existing Task 1 assertion remains exact.

- [ ] **Step 2: 创建 suite 执行器**

```js
// web/tests/e2e/run-suite.mjs
import { spawnSync } from 'node:child_process'

const suite = process.argv[2]
const extraArgs = process.argv.slice(3)
if (!['quick', 'regression', 'slow'].includes(suite)) throw new Error(`未知 E2E suite: ${suite}`)
if (extraArgs.includes('--grep-invert')) throw new Error('三级入口不允许覆盖 --grep-invert')
const grepIndex = extraArgs.indexOf('--grep')
const userGrep = grepIndex >= 0 ? extraArgs[grepIndex + 1] : undefined
if (grepIndex >= 0) {
  if (!userGrep) throw new Error('--grep 缺少表达式')
  extraArgs.splice(grepIndex, 2)
}
const workersIndex = extraArgs.indexOf('--workers')
const workersArg = workersIndex >= 0 ? extraArgs[workersIndex + 1] : undefined
if (workersIndex >= 0) {
  if (!workersArg) throw new Error('--workers 缺少数值')
  extraArgs.splice(workersIndex, 2)
}
const inlineWorkersIndex = extraArgs.findIndex(arg => arg.startsWith('--workers='))
const inlineWorkers = inlineWorkersIndex >= 0 ? extraArgs[inlineWorkersIndex].slice('--workers='.length) : undefined
if (inlineWorkersIndex >= 0) extraArgs.splice(inlineWorkersIndex, 1)
const knownSlowTags = ['@model', '@rag', '@k8s-disruptive', '@i18n-sweep', '@widget-domain', '@vision', '@intent-retry']
const explicitTags = knownSlowTags.filter(tag => userGrep?.includes(tag))
const configuredTags = process.env.OCM_E2E_SLOW_TAGS?.split(',').map(tag => tag.trim()).filter(Boolean) ?? []
const slowTags = explicitTags.length > 0 ? explicitTags : configuredTags
const effectiveGrep = userGrep ?? (suite === 'slow' && slowTags.length > 0 ? slowTags.join('|') : undefined)
const result = spawnSync(process.platform === 'win32' ? 'npx.cmd' : 'npx', ['playwright', 'test', '--project=chromium', ...extraArgs], {
  stdio: 'inherit', env: {
    ...process.env,
    OCM_E2E_SUITE: suite,
    OCM_E2E_WORKERS: workersArg ?? inlineWorkers ?? process.env.OCM_E2E_WORKERS ?? (suite === 'slow' ? '1' : '2'),
    OCM_E2E_USER_GREP: effectiveGrep ?? '',
    OCM_E2E_SLOW_TAGS: suite === 'slow' ? (slowTags.length > 0 ? slowTags : knownSlowTags).join(',') : '',
  },
})
if (result.error) throw result.error
process.exitCode = result.status ?? 1
```

- [ ] **Step 3: 更新 npm scripts**

```json
{
  "test:e2e:quick": "node tests/e2e/run-suite.mjs quick",
  "test:e2e:regression": "node tests/e2e/run-suite.mjs regression",
  "test:e2e:slow": "node tests/e2e/run-suite.mjs slow"
}
```

- [ ] **Step 4: 验证参数透传与失败保留**

Run:

```bash
cd web
npm run test:e2e:quick -- --list console.spec.ts
npm run test:e2e:regression -- --list locale.spec.ts
npm run test:e2e:quick -- console.spec.ts --grep '不存在的场景'
```

Expected: 前两条只列目标 spec；第三条非零退出并打印保留 `run_id`，cleanup 不覆盖原始退出码。用 `OCM_E2E_RUN_ID=<打印值> make cleanup-e2e` 可手工清理。

- [ ] **Step 5: 提交执行包装**

```bash
git add web/package.json web/playwright.config.ts web/tests/e2e/run-suite.mjs web/tests/e2e/suite.ts web/tests/e2e/suite.test.ts
git commit -m "test(e2e): 透传定向参数和原始退出码" -m "三个套件入口统一通过执行器调用 Playwright，失败资源由 teardown 保留。"
```

### Task 11: 更新活动文档与旧命令引用

**Files:**
- Modify: `docs/local-development.md`
- Modify: `docs/testing/aicc-conversation-validation-report.md`
- Modify: `web/playwright.config.ts`
- Modify: `web/tests/e2e/l4-i18n-sweep.spec.ts`
- Modify: `web/tests/e2e/locale.spec.ts`

- [ ] **Step 1: 搜索活动文件中的旧入口**

Run:

```bash
rg -n "npm run test:e2e|make e2e([^a-z-]|$)" Makefile web docs/local-development.md docs/testing \
  --glob '!docs/superpowers/plans/**' --glob '!docs/superpowers/specs/**'
```

Expected: 只列配置注释、locale/L4 注释和 AICC 验证报告中的旧命令。

- [ ] **Step 2: 增加本地 E2E 使用说明**

Add to `docs/local-development.md`:

```markdown
### 浏览器 E2E

- `make e2e-quick`：核心无头冒烟，目标 60 秒内；
- `make e2e-regression`：全部确定性无头回归，默认 2 worker；
- `make e2e-slow`：真实模型、RAG、Pod 恢复和全站 i18n 清扫，仅显式运行。

低资源机器可用 `OCM_E2E_WORKERS=1 make e2e-regression`。定向执行示例：

```bash
cd web
npm run test:e2e:quick -- console.spec.ts
npm run test:e2e:slow -- aicc-knowledge.spec.ts --grep @rag
```

slow 前置条件缺失时，命令会在浏览器执行前列出缺失环境变量并退出。测试失败会打印并保留 `run_id`；排查后使用 `OCM_E2E_RUN_ID=<run_id> make cleanup-e2e` 清理。
```

- [ ] **Step 3: 更新代码注释和验证报告**

Use `test:e2e:regression` for locale commands and `test:e2e:slow -- --grep @i18n-sweep` for L4. In the AICC report, use `--grep @model` for conversation runs, `--grep @rag` for knowledge runs, and `--grep @k8s-disruptive` for Pod/fault runs. Do not rewrite historical files under `docs/superpowers/plans/` or `docs/superpowers/specs/`.

- [ ] **Step 4: 确认旧入口清零并提交**

Run the Step 1 `rg` command again.

Expected: 无输出。

```bash
git add docs/local-development.md docs/testing/aicc-conversation-validation-report.md web/playwright.config.ts web/tests/e2e/l4-i18n-sweep.spec.ts web/tests/e2e/locale.spec.ts
git commit -m "docs(test): 更新三级 E2E 运行说明" -m "记录 worker 覆盖、失败保留、slow 前置条件和定向执行方式。"
```

### Task 12: 完成定向回归与性能验收

**Files:**
- Create: `docs/superpowers/reports/2026-07-17-e2e-test-acceleration-verification.md`

- [ ] **Step 1: 运行新增单元测试**

Run:

```bash
go test ./cmd/seed-e2e -count=1
cd web && npx vitest run tests/e2e/suite.test.ts tests/e2e/auth-state.test.ts tests/e2e/preflight.test.ts
```

Expected: 全部 PASS，无 skipped。

- [ ] **Step 2: 验证三级清单边界**

Run:

```bash
cd web
npm run test:e2e:quick -- --list
npm run test:e2e:regression -- --list
npm run test:e2e:slow -- --list
```

Expected: quick 是 regression 严格子集；regression 不含 `@slow`；slow 只含 `@slow`；默认入口均不含 `chrome-headed`。

- [ ] **Step 3: 连续三次运行 quick**

```bash
/usr/bin/time -f 'quick-1 %e s' make e2e-quick
/usr/bin/time -f 'quick-2 %e s' make e2e-quick
/usr/bin/time -f 'quick-3 %e s' make e2e-quick
```

Expected: 三次 PASS、`retries: 0`，每次墙钟时间 ≤60 秒。

- [ ] **Step 4: 连续三次运行 regression**

```bash
/usr/bin/time -f 'regression-1 %e s' make e2e-regression
/usr/bin/time -f 'regression-2 %e s' make e2e-regression
/usr/bin/time -f 'regression-3 %e s' make e2e-regression
```

Expected: 三次 PASS、无 skipped、每次墙钟时间 ≤1200 秒。若失败或超预算，只根据 reporter 排名运行最小相关 spec 定位，不在每次局部修改后重复完整三轮。

- [ ] **Step 5: 验证 slow 快速失败与一个可用子集**

Run without prerequisites: `make e2e-slow`

Expected: 浏览器场景开始前非零退出并列出缺失项，不显示大量 skipped。

Then run:

```bash
OCM_AICC_CONVERSATION_E2E=1 OCM_AICC_KNOWLEDGE_FIXTURE=1 \
  npm --prefix web run test:e2e:slow -- aicc-knowledge.spec.ts --grep @rag
```

Expected: 所选 RAG slow 子集真实执行，其他 slow 场景不运行。

- [ ] **Step 6: 写实测验收报告**

Create `docs/superpowers/reports/2026-07-17-e2e-test-acceleration-verification.md`. It must contain: the exact `make local-status` result; Chromium/headless/retries/worker configuration; the three `--list` test counts; a two-row table containing all six measured wall-clock values and the 60/1200-second verdicts; the reporter's three slowest specs; the worker-isolation command result; the retained run ID plus cleanup command result; the slow-preflight failure output; and a final PASS/FAIL statement for every success criterion. Copy measured values directly—do not commit bracketed template text or estimates.

- [ ] **Step 7: 检查工作区并提交验收证据**

Run: `git diff --check && git status --short`

Expected: 只有验收报告和测量证据驱动的聚焦修复；没有 trace、截图、认证状态、临时 JSON 或无关文件。

```bash
git add docs/superpowers/reports/2026-07-17-e2e-test-acceleration-verification.md
git commit -m "test(e2e): 记录三级回归性能验收" -m "验证时间预算、双 worker 隔离、失败资源保留和 slow 前置检查。"
```
