# 国际化改动全面测试验证 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用五层验证（翻译完整性单测 / 单测集成 / E2E / 逐页浏览器走查 / hermes 端到端）证明本次国际化改动在三面 × 中英 × 三角色下文案完整、切换正确、持久化可靠，并把关键校验沉淀为入库自动化测试。

**Architecture:** L1 新增 vitest 单测遍历 `locales/{en,zh}` 两棵 message 树做六维一致性断言；L2 跑既有 Go + vitest 套件确认全绿；L3 扩展 Playwright E2E 覆盖非 platform_admin 角色持久化与 app locale API 链路；L4 用 chrome-devtools MCP 真实浏览器逐页截图走查产出矩阵；L5 本地 k3d 起 hermes 实例验证 bot 语言。发现问题先修再复验。

**Tech Stack:** vue-i18n 11 / vitest（jsdom）/ Playwright 1.59 / Go test / 本地 k3d（`make local-up`）/ chrome-devtools MCP。

**前置约束：**
- L4 一律真实浏览器（curl 不能替代前端验证），逐页带截图。
- 三角色本地账号：platform_admin = `admin`/组织标识留空/`admin123`；org_admin、org_member 来自 `make seed-e2e` fixture 或本地组织账号。
- app locale 后端有端点但**无前端 UI**，仅创建时快照 owner locale + API 可改；L3/L5 经认证 API 设置。

---

## Task 1: L1 翻译完整性 vitest 单测（入库）

**Files:**
- Create: `web/src/i18n/locales/completeness.spec.ts`
- 可能 Modify（修补 L1 暴露的缺口）: `web/src/i18n/locales/en/*.ts`、`web/src/i18n/locales/zh/*.ts`

- [ ] **Step 1: 写完整性单测**

创建 `web/src/i18n/locales/completeness.spec.ts`，完整内容如下：

```ts
import { describe, expect, it } from 'vitest'

import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'

type Tree = { [k: string]: string | Tree }

// flattenLeaves 把消息树压成 path->string 映射；遇到非字符串/对象抛错以暴露异常结构。
function flattenLeaves(tree: Tree, prefix = '', out = new Map<string, string>()): Map<string, string> {
  for (const [key, val] of Object.entries(tree)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (typeof val === 'string') out.set(path, val)
    else if (val && typeof val === 'object') flattenLeaves(val as Tree, path, out)
    else throw new Error(`非法 message 节点类型 ${path}: ${typeof val}`)
  }
  return out
}

// nodeTypes 记录每个 path 的类型（leaf/branch），用于嵌套结构一致性比对。
function nodeTypes(tree: Tree, prefix = '', out = new Map<string, 'leaf' | 'branch'>()): Map<string, 'leaf' | 'branch'> {
  for (const [key, val] of Object.entries(tree)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (typeof val === 'string') out.set(path, 'leaf')
    else if (val && typeof val === 'object') {
      out.set(path, 'branch')
      nodeTypes(val as Tree, path, out)
    }
  }
  return out
}

// namedTokens 抽取 vue-i18n 命名插值 {name}/{0}；ICU 的 {x, plural,...} 含逗号不匹配，天然排除。
function namedTokens(s: string): Set<string> {
  return new Set([...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]))
}

// pipeBranches 按 vue-i18n 管道复数分隔符切分，返回分支数（无管道则为 1）。
function pipeBranches(s: string): number {
  return s.split(/\s*\|\s*/).length
}

// icuCategories 抽取 ICU plural/select 的分支类别集合（如 one/other/=0）；无则空集。
function icuCategories(s: string): Set<string> {
  const m = s.match(/\{\s*\w+\s*,\s*(?:plural|select)\s*,([\s\S]*)\}/)
  if (!m) return new Set()
  return new Set([...m[1].matchAll(/(=\d+|\w+)\s*\{/g)].map((x) => x[1]))
}

const enLeaves = flattenLeaves(en as Tree)
const zhLeaves = flattenLeaves(zh as Tree)
const enTypes = nodeTypes(en as Tree)
const zhTypes = nodeTypes(zh as Tree)
const sharedLeaves = [...enLeaves.keys()].filter((k) => zhLeaves.has(k))

describe('i18n 翻译完整性', () => {
  // 双向 key 对齐：列出仅在一侧出现的 leaf path，缺/多均失败。
  it('en 与 zh 的 key 完全对齐', () => {
    const onlyEn = [...enLeaves.keys()].filter((k) => !zhLeaves.has(k))
    const onlyZh = [...zhLeaves.keys()].filter((k) => !enLeaves.has(k))
    expect({ onlyEn, onlyZh }).toEqual({ onlyEn: [], onlyZh: [] })
  })

  // 空值检测：任一侧 leaf 为空/纯空白即失败。
  it('两侧均无空文案', () => {
    const empties: string[] = []
    for (const [k, v] of enLeaves) if (!v.trim()) empties.push(`en:${k}`)
    for (const [k, v] of zhLeaves) if (!v.trim()) empties.push(`zh:${k}`)
    expect(empties).toEqual([])
  })

  // 命名插值占位符集合一致：同一 key 两侧 {name} 名字集合必须相等。
  it('共享 key 的命名占位符集合一致', () => {
    const mismatches: string[] = []
    for (const k of sharedLeaves) {
      const a = [...namedTokens(enLeaves.get(k)!)].sort().join(',')
      const b = [...namedTokens(zhLeaves.get(k)!)].sort().join(',')
      if (a !== b) mismatches.push(`${k}: en{${a}} zh{${b}}`)
    }
    expect(mismatches).toEqual([])
  })

  // en 文案不得残留中日韩表意文字（漏译防御）。
  it('en 文案无中日韩表意文字', () => {
    const cjk = [...enLeaves].filter(([, v]) => /[\u4e00-\u9fff]/.test(v)).map(([k]) => k)
    expect(cjk).toEqual([])
  })

  // 嵌套结构一致：同一 path 两侧 leaf/branch 类型一致，无单侧多/缺子树。
  it('en 与 zh 嵌套结构一致', () => {
    const allPaths = new Set([...enTypes.keys(), ...zhTypes.keys()])
    const diffs: string[] = []
    for (const p of allPaths) {
      if (enTypes.get(p) !== zhTypes.get(p)) diffs.push(`${p}: en=${enTypes.get(p)} zh=${zhTypes.get(p)}`)
    }
    expect(diffs).toEqual([])
  })

  // 复数结构一致：管道分支数 + ICU 分支类别两侧相等（现状无复数，防御性兜底）。
  it('共享 key 的复数结构一致', () => {
    const diffs: string[] = []
    for (const k of sharedLeaves) {
      const ev = enLeaves.get(k)!
      const zv = zhLeaves.get(k)!
      if (pipeBranches(ev) !== pipeBranches(zv)) diffs.push(`${k}: 管道分支 en=${pipeBranches(ev)} zh=${pipeBranches(zv)}`)
      const ec = [...icuCategories(ev)].sort().join(',')
      const zc = [...icuCategories(zv)].sort().join(',')
      if (ec !== zc) diffs.push(`${k}: ICU 分支 en{${ec}} zh{${zc}}`)
    }
    expect(diffs).toEqual([])
  })
})
```

- [ ] **Step 2: 跑测试，观察是否暴露真实缺口**

Run: `cd web && npm run test -- --run completeness`
Expected：要么全 PASS，要么某些 `it` FAIL 并打印**真实**的翻译缺口（缺 key / 空值 / 占位符不一致 / en 残留中文 / 结构不一致）。FAIL 即发现 bug，进入 Step 3；全 PASS 跳到 Step 4。

- [ ] **Step 3: 按报错修补 locale 文件（条件步骤）**

针对每条失败明细，在对应 `web/src/i18n/locales/en/*.ts` 或 `zh/*.ts` 中：补齐缺失 key、填空值、对齐占位符名、把 en 残留中文翻成英文、修正一边 leaf 一边 object 的结构错位。**只改 locale 文案文件，不动业务逻辑**。每修一类后回到 Step 2 重跑直到对应 `it` 变绿。

- [ ] **Step 4: 跑全套 vitest，确认未引入回归**

Run: `cd web && npm run test -- --run`
Expected：所有单测 PASS（含 `completeness.spec.ts` 与既有 `*.spec.ts`）。

- [ ] **Step 5: 提交**

```bash
git add web/src/i18n/locales/completeness.spec.ts web/src/i18n/locales/
git commit -m "test(i18n): 新增翻译完整性单测并修补暴露的中英文案缺口

遍历 en/zh 两棵 message 树做六维一致性断言：双向 key 对齐、空值、命名插值
占位符集合、en 残留中文、嵌套结构 leaf/branch 类型、复数管道与 ICU 分支结构。
修补单测暴露的缺失 key 与未翻译文案。"
```

---

## Task 2: L2 后端与前端单测全绿（验证检查点）

**Files:**
- 仅运行既有套件；如发现失败再 Modify 对应被测代码/测试。
- 重点既有覆盖：`internal/api/handlers/apps_test.go`、`auth_test.go`、`config_test.go`、`internal/service/app_service_test.go`、`auth_service_test.go`、`internal/integrations/hermes/build_manifest_test.go`、`web/src/stores/locale.spec.ts`。

- [ ] **Step 1: 跑 Go 全量单测**

Run: `make test`
Expected：`ok` 全绿，无 FAIL。覆盖 locale 业务（创建快照 owner locale、PATCH 端点、manifest Language、users/apps locale CHECK 回退）。

- [ ] **Step 2: 跑前端全量单测**

Run: `cd web && npm run test -- --run`
Expected：全 PASS（含 Task 1 新增）。

- [ ] **Step 3: 跑前端类型检查**

Run: `cd web && npm run typecheck`
Expected：无类型错误（locale store / i18n 类型一致）。

- [ ] **Step 4: 记录结果（若全绿无需提交）**

把三条命令的 PASS/FAIL 摘要记入最终报告（Task 6）。若 Step 1-3 有失败，先定位修复（补测试或修代码），修完重跑变绿后单独提交：

```bash
git add <修复涉及文件>
git commit -m "test(i18n): 修复 locale 相关单测/类型缺口"
```

---

## Task 3: L3 扩展 Playwright E2E（入库）

**Files:**
- Modify: `web/tests/e2e/locale.spec.ts`（在文件末尾追加用例，不动既有用例）

现有用例覆盖：登录页语言选择器、登录页切换写 localStorage、按钮文案跟随、platform_admin 登录后切换刷新保持、退出重登保持。**缺口**：非 platform_admin 角色持久化、app locale API 设置链路。

- [ ] **Step 1: 追加 org_member 角色持久化用例**

在 `web/tests/e2e/locale.spec.ts` 末尾追加（沿用既有 `loadE2EFixture` / `loginAs` / `test.skip` 无后端跳过模式）：

```ts
// ─── org_member 登录后切换语言退出重登仍保持（覆盖非 platform_admin 角色）────────

test('org_member 切换语言后退出重登仍保持（DB 持久化）', async ({ page }) => {
  // 依赖后端与 seed-e2e fixture；无后端时跳过，避免误报。
  let fx
  try {
    fx = loadE2EFixture()
  } catch {
    test.skip(true, '无后端 fixture，跳过 DB 持久化断言')
    return
  }

  // 以 org_member 登录，初始语言为平台默认（en）。
  await loginAs(page, 'org_member', fx)

  // 顶栏语言选择器切到中文。
  const switcher = page.getByRole('button', { name: /^(Language|语言|English|简体中文)$/ })
  await switcher.click()
  await page.getByRole('option', { name: '简体中文' }).click()

  // 等持久化请求落库：断言再次出现中文按钮标签。
  await expect(page.getByRole('button', { name: /^语言$/ })).toBeVisible()

  // 清掉 localStorage 模拟新设备，退出重登，验证语言来自 DB user.locale。
  await page.evaluate(() => localStorage.removeItem('ocm.locale'))
  await loginAs(page, 'org_member', fx)
  await expect(page.getByRole('button', { name: /^语言$/ })).toBeVisible()
})
```

- [ ] **Step 2: 追加 app locale API 设置链路用例**

继续在末尾追加（app locale 无前端 UI，用 Playwright request context 经登录态 cookie 走真实后端端点）：

```ts
// ─── app locale API 链路：PATCH 后 GET 反映新语言（端点端到端）────────────────

test('app locale PATCH 后实例语言更新为 en', async ({ page }) => {
  // 依赖后端与 fixture（fixture 含 app_id）；无后端跳过。
  let fx
  try {
    fx = loadE2EFixture()
  } catch {
    test.skip(true, '无后端 fixture，跳过 app locale 端点断言')
    return
  }

  // 平台管理员登录，使 page.request 携带认证 cookie。
  await loginAs(page, 'platform_admin', fx)

  // PATCH 实例语言为 en，断言 2xx。
  const patch = await page.request.patch(`/api/v1/apps/${fx.app_id}/locale`, {
    data: { locale: 'en' },
  })
  expect(patch.ok()).toBeTruthy()

  // GET 实例详情，断言 locale 已变为 en（字段名以 generated.ts AppDetail 为准）。
  const detail = await page.request.get(`/api/v1/apps/${fx.app_id}`)
  expect(detail.ok()).toBeTruthy()
  const body = await detail.json()
  expect(body.locale).toBe('en')
})
```

> 注：若 `GET /api/v1/apps/:appId` 响应字段名非 `locale`，按 `web/src/api/generated.ts` 中 AppDetail schema 调整断言路径。

- [ ] **Step 3: 起本地全栈后跑 E2E**

Run:
```bash
make local-up          # 起 k3d 全栈（已起则跳过）
make seed-e2e          # 注入 fixture，打印 JSON
cd web && npm run dev   # 另开终端：起前端 dev server
cd web && npm run test:e2e -- locale.spec.ts
```
Expected：全部用例 PASS（新增 2 条不被 skip）。

- [ ] **Step 4: 提交**

```bash
git add web/tests/e2e/locale.spec.ts
git commit -m "test(i18n): E2E 补 org_member 语言持久化与 app locale 端点链路

新增 org_member 切换语言退出重登仍保持（覆盖非 platform_admin DB 持久化），
及 app locale PATCH 后 GET 反映新语言的端点端到端用例（app locale 无前端 UI，
经登录态 request context 验证）。"
```

---

## Task 4: L4 三角色 × 中英 × 逐页浏览器走查（验证报告）

**Files:**
- Create: `docs/superpowers/verifications/2026-06-23-i18n-validation-report.md`（含逐页矩阵）

用 chrome-devtools MCP 真实浏览器，基地址 `http://ocm.localhost`。每个角色登录后，对其门控可见页面逐个：切到 en 截图、切到 zh 截图，逐项核对五检查项。

**页面清单（path 相对 `http://ocm.localhost`）：**

- 登录页（未登录）：`/login`
- 全角色（org_member / org_admin / platform_admin 各走一遍）：
  `/`、`/knowledge`、`/skills`、`/usage`、`/apps`、`/apps/empty`，
  App 详情 8 tab：`/apps/:appId/overview|kanban|cron|channels|knowledge|skills|workspace|audit`，
  工单详情 `/skill-tickets/:id`
- org_admin 以上（org_admin + platform_admin）：`/members`、`/audit-logs`
- org_admin 独有：`/org-console`、`/members/new`、`/balance`
- platform_admin 独有：`/console`、`/organizations`、`/assistant-versions`、`/platform/industry-knowledge`、`/platform/skills`、`/platform/custom-skills`、`/platform/permissions`、充值页 `/platform/organizations/:orgId/recharge`、App `/apps/:appId/runtime` tab

**每页五检查项：**① en 页无中文裸串 / zh 页无英文裸串 ② 无 i18n key 裸露（如界面直接显示 `common.save`）③ 切换语言即时生效无刷新空窗 ④ 布局无溢出/截断/错位 ⑤ 动态文案（toast/表单校验/空态/分页「共 N 条」）同步切换。

- [ ] **Step 1: 创建报告骨架与逐页矩阵模板**

创建 `docs/superpowers/verifications/2026-06-23-i18n-validation-report.md`：

```markdown
# 国际化改动验证报告 · 2026-06-23

## 环境
- 本地 k3d（make local-up）+ web dev server；基地址 http://ocm.localhost
- 浏览器：chrome-devtools MCP（真实浏览器，逐页截图）

## L1/L2/L3 自动化结果
（Task 1/2/3 命令输出摘要：PASS/FAIL + 发现并修复的缺口）

## L4 逐页矩阵
| 角色 | 页面 | en | zh | 五检查项结论 | 截图 | 备注 |
|---|---|---|---|---|---|---|
| platform_admin | /console | ✅ | ✅ | 1-5 全通过 | console-en.png / console-zh.png | |
| ... | ... | | | | | |

## L5 hermes 端到端
（Task 5 结果）

## 发现的问题与修复
| # | 页面/位置 | 问题 | 修复 commit | 复验 |
|---|---|---|---|---|
```

- [ ] **Step 2: 起环境并确认三角色可登录**

Run（若 Task 3 已起则复用）：
```bash
make local-up
cd web && npm run dev
```
用 chrome-devtools MCP 打开 `http://ocm.localhost/login`，分别用三角色登录成功（platform_admin: admin/留空组织/admin123；org_admin、org_member 用本地组织账号或 fixture 账号）。

- [ ] **Step 3: 登录页 + 默认语言回退走查**

`/login` 未登录态：en/zh 各截图，核对五检查项；额外验证把后端 `i18n.default_locale` 配为 `zh` 重启后、清掉 `localStorage.ocm.locale` 时登录页回退中文（验证后改回 en）。填入矩阵。

- [ ] **Step 4: 逐角色逐页走查（org_member → org_admin → platform_admin）**

每个角色登录后，按上面清单中该角色可见页面：导航 → 切 en 截图 → 切 zh 截图 → 核对五检查项 → 写入矩阵一行（截图存 `docs/superpowers/verifications/screenshots/`）。门控外页面不重复。发现问题记入「发现的问题」表，**先修 locale/组件再回到该页复验**。

- [ ] **Step 5: 修复回归后提交报告与修复**

```bash
git add docs/superpowers/verifications/ web/src/i18n/locales/ web/src/
git commit -m "test(i18n): 逐页浏览器走查矩阵及走查中发现的文案/布局修复

三角色 × 中英 × 全部门控可见页面逐页截图核对：未翻译残留、key 裸露、
切换生效、布局溢出、动态文案。记录矩阵与修复回归。"
```

---

## Task 5: L5 hermes 端到端语言验证（验证报告）

**Files:**
- Modify: `docs/superpowers/verifications/2026-06-23-i18n-validation-report.md`（填 L5 小节）

验证 app.locale 驱动 hermes bot 对终端用户说话的语言，且创建实例时快照 owner locale 生效。

- [ ] **Step 1: 准备两个实例并设不同语言**

平台管理员登录 `http://ocm.localhost`，在 `/apps` 新建两个实例（或复用现有）。app locale 无前端 UI，用浏览器登录态经 chrome-devtools MCP 的页面 fetch，或带认证 cookie 的请求对两实例分别设语言：

```
PATCH /api/v1/apps/{appId-A}/locale  body: {"locale":"zh"}
PATCH /api/v1/apps/{appId-B}/locale  body: {"locale":"en"}
```
断言均 2xx。

- [ ] **Step 2: 部署/重渲染两个实例**

在各自 App 详情页触发部署（runtime tab 的部署/重启动作，platform_admin 可见），等实例就绪。确认 manifest 已带 `app.language`（zh / en）。

- [ ] **Step 3: 浏览器实际对话验证语言**

进入两个实例的对话入口，各发一条相同问候。验证：实例 A 的 bot 用中文回应，实例 B 用英文回应。截图存证。

- [ ] **Step 4: 验证创建时快照 owner locale**

把某 owner 用户 `user.locale` 设为 zh，以该 owner 新建实例，确认新实例 `apps.locale` 自动快照为 zh（GET 实例详情或对话语言验证），证明「创建快照 owner locale」链路。

- [ ] **Step 5: 填报告并提交**

把 L5 对话截图与结论写入报告 L5 小节。

```bash
git add docs/superpowers/verifications/
git commit -m "test(i18n): hermes 端到端语言验证（app.locale 驱动 bot 语言 + owner 快照）"
```

---

## Task 6: 汇总验证报告与最终回归（交付）

**Files:**
- Modify: `docs/superpowers/verifications/2026-06-23-i18n-validation-report.md`

- [ ] **Step 1: 回填自动化结果摘要**

把 Task 1-3 的命令输出（L1 六维断言全绿、L2 Go+vitest+typecheck 全绿、L3 E2E 全绿）摘要写入报告 L1/L2/L3 小节，附发现并修复的缺口清单。

- [ ] **Step 2: 全量回归一次**

Run:
```bash
make test
cd web && npm run test -- --run && npm run typecheck
cd web && npm run test:e2e -- locale.spec.ts
```
Expected：全部 PASS。任一 FAIL 则回到对应 Task 修复再回归。

- [ ] **Step 3: 完整性自检**

核对报告：L4 矩阵每个 (角色, 页面, 语言) 都有结论与截图；「发现的问题」表每条都有修复 commit 与复验✅；无遗留未修项。

- [ ] **Step 4: 提交最终报告**

```bash
git add docs/superpowers/verifications/2026-06-23-i18n-validation-report.md
git commit -m "docs(i18n): 汇总国际化全面测试验证报告

合并 L1-L5 结果：翻译完整性单测、单测集成、E2E、逐页浏览器矩阵、hermes
端到端，附发现问题与修复回归清单，全量回归通过。"
```

---

## 自检对照（spec 覆盖）

- spec §3 L1 → Task 1（六维断言含嵌套结构与复数）
- spec §3 L2 → Task 2（Go + vitest + typecheck）
- spec §3 L3 → Task 3（org_member 持久化 + app locale API 链路）
- spec §3 L4 / §5 → Task 4（三角色逐页矩阵 + 五检查项 + 默认语言回退）
- spec §6 L5 → Task 5（双实例语言 + owner 快照）
- spec §7 交付物 → Task 4/5/6（报告矩阵 + 发现问题修复回归）
- spec §8 执行顺序 → Task 1→2→3→4→5→6
