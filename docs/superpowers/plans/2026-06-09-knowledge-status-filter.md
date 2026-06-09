# 知识库按解析状态筛选 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给企业 / 实例 / 行业三个知识库文件列表加一个「解析状态」下拉筛选。

**Architecture:** 纯前端。后端 service/handler/SQL 及三个查询 hook（org/app 在 `useKnowledge.ts`，行业在 `useIndustryKnowledge.ts`）**均已支持 `status`**，本次不动后端、不动 hook、不重新生成 OpenAPI。新建一个共享模块收敛解析状态的文案/标签色/筛选选项，三页改为引用它，并各加一个 `n-select`（clearable + 占位「全部状态」）把 `status` 接进现有查询与 watch。

**Tech Stack:** Vue 3 `<script setup>` + TypeScript、naive-ui、@tanstack/vue-query、vitest（前端单测）、pnpm/make。

---

## File Map

| Path | Change |
|---|---|
| `web/src/domain/parseStatus.ts` | 新建：解析状态文案 `parseStatusLabel`、标签色 `parseStatusTagType`、筛选选项 `PARSE_STATUS_FILTER_OPTIONS`。 |
| `web/src/domain/parseStatus.spec.ts` | 新建：共享模块单测。 |
| `web/src/pages/knowledge/OrgKnowledgePage.vue` | 加状态下拉 + `status` ref，接入查询/watch；删本地 `parseStatusLabel`/`parseTagType`，改用共享模块。 |
| `web/src/pages/apps/AppKnowledgeTab.vue` | 同上。 |
| `web/src/pages/platform/IndustryKnowledgePage.vue` | 同上（行业页 file query/watch）。 |

三个查询 hook **不改**（已支持 `status`）。

---

### Task 1: 共享解析状态模块

**Files:**
- Create: `web/src/domain/parseStatus.ts`
- Create: `web/src/domain/parseStatus.spec.ts`

- [ ] **Step 1: 写失败单测**

Create `web/src/domain/parseStatus.spec.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { parseStatusLabel, parseStatusTagType, PARSE_STATUS_FILTER_OPTIONS } from './parseStatus'

describe('parseStatus', () => {
  it('已知状态返回中文文案', () => {
    // 覆盖五个已知解析状态的文案映射。
    expect(parseStatusLabel('queued')).toBe('等待解析')
    expect(parseStatusLabel('running')).toBe('解析中')
    expect(parseStatusLabel('completed')).toBe('已完成')
    expect(parseStatusLabel('failed')).toBe('解析失败')
    expect(parseStatusLabel('stopped')).toBe('已停止')
  })

  it('未知状态原样透出便于排障', () => {
    // 服务端若新增状态，前端不应吞掉，原样显示。
    expect(parseStatusLabel('weird')).toBe('weird')
  })

  it('标签色按状态语义映射，未知状态用默认色', () => {
    // completed→success，进行中→warning，失败/停止→error，其它→default。
    expect(parseStatusTagType('completed')).toBe('success')
    expect(parseStatusTagType('queued')).toBe('warning')
    expect(parseStatusTagType('running')).toBe('warning')
    expect(parseStatusTagType('failed')).toBe('error')
    expect(parseStatusTagType('stopped')).toBe('error')
    expect(parseStatusTagType('weird')).toBe('default')
  })

  it('筛选选项覆盖五个状态、value 为状态原值、label 为中文文案', () => {
    // 下拉用此选项；不含「全部」（由 n-select clearable 表达）。
    expect(PARSE_STATUS_FILTER_OPTIONS).toEqual([
      { label: '等待解析', value: 'queued' },
      { label: '解析中', value: 'running' },
      { label: '已完成', value: 'completed' },
      { label: '解析失败', value: 'failed' },
      { label: '已停止', value: 'stopped' },
    ])
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run:
```bash
cd web && rtk pnpm vitest run src/domain/parseStatus.spec.ts
```
Expected: FAIL（模块不存在）。

- [ ] **Step 3: 实现共享模块**

Create `web/src/domain/parseStatus.ts`:

```ts
// parseStatus 收敛 RAGFlow 解析状态在前端的文案、标签色与筛选选项，避免在各知识库页重复定义。

// PARSE_STATUS_LABELS 是已知解析状态到中文文案的映射；服务端新增状态时由 parseStatusLabel 原样兜底。
export const PARSE_STATUS_LABELS: Record<string, string> = {
  queued: '等待解析',
  running: '解析中',
  completed: '已完成',
  failed: '解析失败',
  stopped: '已停止',
}

// parseStatusLabel 把解析状态转成页面文案；未知值直接透出便于排障与兼容服务端新增状态。
export function parseStatusLabel(status: string): string {
  return PARSE_STATUS_LABELS[status] ?? status
}

// parseStatusTagType 把解析状态映射为 naive-ui 标签色：完成=成功色，进行中=警告色，失败/停止=错误色，
// 其它（含服务端新增的未知状态）保留默认色。
export function parseStatusTagType(status: string): 'success' | 'warning' | 'error' | 'default' {
  if (status === 'completed') return 'success'
  if (status === 'queued' || status === 'running') return 'warning'
  if (status === 'failed' || status === 'stopped') return 'error'
  return 'default'
}

// PARSE_STATUS_FILTER_OPTIONS 是文件列表「解析状态」下拉的选项，按解析生命周期顺序排列；
// 不含「全部」项——由 n-select 的 clearable 表达「清空即全部状态」。
export const PARSE_STATUS_FILTER_OPTIONS: { label: string; value: string }[] = [
  { label: PARSE_STATUS_LABELS.queued, value: 'queued' },
  { label: PARSE_STATUS_LABELS.running, value: 'running' },
  { label: PARSE_STATUS_LABELS.completed, value: 'completed' },
  { label: PARSE_STATUS_LABELS.failed, value: 'failed' },
  { label: PARSE_STATUS_LABELS.stopped, value: 'stopped' },
]
```

- [ ] **Step 4: 跑测试确认通过**

Run:
```bash
cd web && rtk pnpm vitest run src/domain/parseStatus.spec.ts
```
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
rtk git add web/src/domain/parseStatus.ts web/src/domain/parseStatus.spec.ts
rtk git commit -m "feat(knowledge): 增加解析状态共享模块" -m "收敛解析状态文案、标签色与筛选下拉选项，供三个知识库页复用。"
```

---

### Task 2: 企业知识库页加状态筛选

**Files:**
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`

当前结构（已确认）：筛选行在 `<n-space align="center" style="margin-bottom: 12px">`（约 45-59 行，含「选择企业」`n-select` 与「搜索文件名称」`n-input`）；`keyword` ref 约 149 行；查询 `useOrgKnowledgeQuery(effectiveOrgId, { page, pageSize, keyword: normalizedKeyword })` 约 153-157 行；`watch([effectiveOrgId, normalizedKeyword], () => { page.value = 1 })` 约 191 行；本地 `parseTagType` 约 195 行、`parseStatusLabel` 约 203 行；模板里调用 `parseTagType(row.parse_status)` 与 `parseStatusLabel(row.parse_status)`。

- [ ] **Step 1: 引入共享模块、删除本地重复函数**

在 `<script setup>` 顶部 import 区加：
```ts
import { parseStatusLabel, parseStatusTagType, PARSE_STATUS_FILTER_OPTIONS } from '@/domain/parseStatus'
```
删除本文件内本地定义的 `function parseTagType(...) {...}` 和 `function parseStatusLabel(...) {...}` 两个函数整体。把模板/列定义里对 `parseTagType(` 的调用改为 `parseStatusTagType(`；`parseStatusLabel(` 调用保持不变（同名）。

- [ ] **Step 2: 加 `status` ref 并接入查询与 watch**

在 `const keyword = ref('')` 附近加：
```ts
// status 为「解析状态」筛选值，null/空＝不过滤（全部状态）。
const status = ref<string | null>(null)
const normalizedStatus = computed(() => status.value ?? undefined)
```
把查询选项改为带上 status：
```ts
const { data: listing, isLoading, error } = useOrgKnowledgeQuery(effectiveOrgId, {
  page,
  pageSize,
  keyword: normalizedKeyword,
  status: normalizedStatus,
})
```
把 watch 改为包含 status（切换筛选回到第 1 页）：
```ts
watch([effectiveOrgId, normalizedKeyword, normalizedStatus], () => {
  page.value = 1
})
```
（若 `computed` 尚未从 `vue` 导入，则补充导入。）

- [ ] **Step 3: 在筛选行加状态下拉**

在「搜索文件名称」`n-input` 之后、同一个 `<n-space>` 内加：
```vue
<n-select
  v-model:value="status"
  :options="PARSE_STATUS_FILTER_OPTIONS"
  clearable
  placeholder="全部状态"
  style="width: 160px"
/>
```

- [ ] **Step 4: 跑该页既有单测确认未回归**

Run:
```bash
cd web && rtk pnpm vitest run src/pages/knowledge/OrgKnowledgePage.spec.ts
```
Expected: PASS（重构后既有用例仍通过；本步保证删本地函数/改名未破坏渲染）。

- [ ] **Step 5: 提交**

```bash
rtk git add web/src/pages/knowledge/OrgKnowledgePage.vue
rtk git commit -m "feat(knowledge): 企业知识库文件列表加解析状态筛选" -m "新增解析状态下拉并接入查询与翻页重置，复用共享解析状态模块。"
```

---

### Task 3: 实例知识库页加状态筛选

**Files:**
- Modify: `web/src/pages/apps/AppKnowledgeTab.vue`

当前结构（已确认）：「搜索文件名称」`n-input` 约 34 行；`keyword` ref 约 115 行；查询 `useAppKnowledgeQuery(appIdRef, { page, pageSize, keyword: normalizedKeyword })` 约 119-122 行；`watch([appIdRef, normalizedKeyword], () => { page.value = 1 })` 约 171 行；本地 `parseTagType` 约 194 行、`parseStatusLabel` 约 202 行；`computed`/`watch` 已从 vue 导入（约 81 行）。

- [ ] **Step 1: 引入共享模块、删除本地重复函数**

import 区加：
```ts
import { parseStatusLabel, parseStatusTagType, PARSE_STATUS_FILTER_OPTIONS } from '@/domain/parseStatus'
```
删除本地 `function parseTagType(...)` 与 `function parseStatusLabel(...)`；模板/列定义里 `parseTagType(` 调用改为 `parseStatusTagType(`。

- [ ] **Step 2: 加 `status` ref 并接入查询与 watch**

在 `const keyword = ref('')` 附近加：
```ts
// status 为「解析状态」筛选值，null/空＝不过滤（全部状态）。
const status = ref<string | null>(null)
const normalizedStatus = computed(() => status.value ?? undefined)
```
查询选项改为：
```ts
const listing = useAppKnowledgeQuery(appIdRef, {
  page,
  pageSize,
  keyword: normalizedKeyword,
  status: normalizedStatus,
})
```
watch 改为：
```ts
watch([appIdRef, normalizedKeyword, normalizedStatus], () => {
  page.value = 1
})
```

- [ ] **Step 3: 在筛选行加状态下拉**

在「搜索文件名称」`n-input` 之后加（与其同一容器）：
```vue
<n-select
  v-model:value="status"
  :options="PARSE_STATUS_FILTER_OPTIONS"
  clearable
  placeholder="全部状态"
  style="width: 160px"
/>
```

- [ ] **Step 4: 跑该页既有单测确认未回归**

Run:
```bash
cd web && rtk pnpm vitest run src/pages/apps/AppKnowledgeTab.spec.ts
```
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
rtk git add web/src/pages/apps/AppKnowledgeTab.vue
rtk git commit -m "feat(knowledge): 实例知识库文件列表加解析状态筛选" -m "新增解析状态下拉并接入查询与翻页重置，复用共享解析状态模块。"
```

---

### Task 4: 行业知识库页加状态筛选

**Files:**
- Modify: `web/src/pages/platform/IndustryKnowledgePage.vue`

当前结构（已确认）：文件区筛选含「搜索文件名称」`fileKeyword` `n-input`（约 59 行）与创建日期 `createdFrom`/`createdTo`；`fileKeyword` ref 约 250 行、`normalizedFileKeyword` 约 251 行；文件查询 `useIndustryKnowledgeFilesQuery(selectedBaseIdRef, { page: filePage, pageSize: filePageSize, keyword: normalizedFileKeyword, createdFrom, createdTo })` 约 262-268 行；`watch([selectedBaseIdRef, normalizedFileKeyword, createdFrom, createdTo], () => { ... filePage 回到 1 ... })` 约 388 行；本地 `parseStatusLabel` 约 413 行（及对应 `parseTagType`）。行业文件查询 hook 已支持 `status`，无需改 hook。

- [ ] **Step 1: 引入共享模块、删除本地重复函数**

import 区加：
```ts
import { parseStatusLabel, parseStatusTagType, PARSE_STATUS_FILTER_OPTIONS } from '@/domain/parseStatus'
```
删除本地 `function parseStatusLabel(...)` 与本地 `parseTagType(...)`（若存在）；模板里 `parseTagType(` 调用改 `parseStatusTagType(`。

- [ ] **Step 2: 加 `fileStatus` ref 并接入查询与 watch**

在 `const fileKeyword = ref('')` 附近加（用 `fileStatus` 命名与该页 file 前缀风格一致）：
```ts
// fileStatus 为行业库文件「解析状态」筛选值，null/空＝不过滤（全部状态）。
const fileStatus = ref<string | null>(null)
const normalizedFileStatus = computed(() => fileStatus.value ?? undefined)
```
文件查询选项加 status：
```ts
const { data: files, isLoading: filesLoading, error: filesError } = useIndustryKnowledgeFilesQuery(selectedBaseIdRef, {
  page: filePage,
  pageSize: filePageSize,
  keyword: normalizedFileKeyword,
  status: normalizedFileStatus,
  createdFrom,
  createdTo,
})
```
把 watch 数组加入 `normalizedFileStatus`（保持原回调里 filePage 回到 1 的逻辑不变）：
```ts
watch([selectedBaseIdRef, normalizedFileKeyword, normalizedFileStatus, createdFrom, createdTo], () => {
  // 原有：切换筛选条件时文件分页回到第 1 页
  filePage.value = 1
})
```
> 注意：以该文件中 watch 回调的真实现有内容为准，只在依赖数组里加入 `normalizedFileStatus`，不要改动回调体里其它逻辑。

- [ ] **Step 3: 在文件筛选行加状态下拉**

在「搜索文件名称」`n-input` 之后加：
```vue
<n-select
  v-model:value="fileStatus"
  :options="PARSE_STATUS_FILTER_OPTIONS"
  clearable
  placeholder="全部状态"
  style="width: 160px"
/>
```

- [ ] **Step 4: 跑该页既有单测确认未回归**

Run:
```bash
cd web && rtk pnpm vitest run src/pages/platform/IndustryKnowledgePage.spec.ts
```
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
rtk git add web/src/pages/platform/IndustryKnowledgePage.vue
rtk git commit -m "feat(knowledge): 行业知识库文件列表加解析状态筛选" -m "新增解析状态下拉并接入文件查询与翻页重置，复用共享解析状态模块。"
```

---

### Task 5: 整体校验与真实浏览器验证

**Files:** 无源码改动（除非验证暴露问题）。

- [ ] **Step 1: 前端类型检查与单测**

Run:
```bash
cd web && rtk pnpm typecheck && rtk pnpm vitest run
```
Expected: PASS（类型无误、全部前端单测通过）。

- [ ] **Step 2: 前端构建**

Run:
```bash
cd web && rtk pnpm build
```
Expected: PASS。

- [ ] **Step 3: 确认无 OpenAPI / 后端漂移**

Run:
```bash
rtk make openapi-check
rtk proxy git status --short
```
Expected: 无 `openapi/openapi.yaml`、`web/src/api/generated.ts`、后端文件变更；仅本计划涉及的前端文件。

- [ ] **Step 4: 真实浏览器验证（三页各一遍）**

按 AGENTS.md「新功能必须真实浏览器验证」，本地 k3d 起新前端镜像后，用浏览器登录 `http://ocm.localhost`（admin/admin123）逐页验证：
1. 企业知识库、实例知识库、行业知识库三页都出现「解析状态」下拉，占位显示「全部状态」。
2. 选「解析失败」→ 列表只剩解析失败文件、分页回到第 1 页、请求带上 `status=failed`（可在 Network 看请求 query）。
3. 清空下拉（clearable）→ 恢复显示全部状态文件、请求不带 `status`。
4. 切换不同状态、与文件名关键词组合，结果正确。
若任一页异常，先修复并重跑本步，直至三页都正常。

- [ ] **Step 5: 收尾**

若校验仅确认前述提交、无新增改动，则不创建空提交。如校验暴露问题并修复，按「先加失败用例再修复」补一个小修复提交。

---

## 备注

- 本计划**不改任何查询 hook**：org/app（`useKnowledge.ts`）与行业（`useIndustryKnowledge.ts`）的列表 hook 选项类型与参数拼接均已含 `status`，页面把 `status` 传入即可生效。
- 不改后端、SQL、OpenAPI（`status` 已在 API 契约内）。
- 不做按状态排序、多选状态等未要求能力（YAGNI）。
