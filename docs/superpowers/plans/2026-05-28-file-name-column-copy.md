# File Name Column Copy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将工作目录、企业知识库、实例知识库三个文件列表的首列标题从「名称」改为「文件名称」。

**Architecture:** 这是纯前端展示文案变更。保留现有数据结构、接口字段和表格列 `key: 'name'`，只修改用户可见的 `title`，并用组件测试覆盖知识库与工作目录列头渲染。

**Tech Stack:** Vue 3、TypeScript、Naive UI、Vitest、Vue Test Utils。

---

## File Structure

- Modify: `web/src/pages/apps/AppWorkspaceTab.vue`
  - 负责展示实例工作目录文件和目录列表。
  - 只把首列 `title: '名称'` 改为 `title: '文件名称'`。
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`
  - 负责展示企业知识库文件列表。
  - 只把首列 `title: '名称'` 改为 `title: '文件名称'`。
- Modify: `web/src/pages/apps/AppKnowledgeTab.vue`
  - 负责展示实例知识库文件列表。
  - 只把首列 `title: '名称'` 改为 `title: '文件名称'`。
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`
  - 让 `NDataTable` stub 渲染列标题，并断言企业知识库列头显示「文件名称」。
- Modify: `web/src/pages/apps/AppKnowledgeTab.spec.ts`
  - 让 `NDataTable` stub 渲染列标题，并断言实例知识库列头显示「文件名称」。
- Create: `web/src/pages/apps/AppWorkspaceTab.spec.ts`
  - 新增最小组件测试，mock 工作目录 hook，断言工作目录列头显示「文件名称」。

### Task 1: 企业知识库列头与测试

**Files:**
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`

- [ ] **Step 1: 写企业知识库列头测试**

在 `web/src/pages/knowledge/OrgKnowledgePage.spec.ts` 中更新 `RenderableColumn`，增加 `title` 字段：

```ts
type RenderableColumn = {
  key: string
  title?: string
  render?: (row: unknown) => VNodeChild
}
```

把 `DataTableStub` 改为先渲染表头，再渲染单元格：

```ts
const DataTableStub = defineComponent({
  props: {
    columns: { type: Array as PropType<RenderableColumn[]>, default: () => [] },
    data: { type: Array as PropType<unknown[]>, default: () => [] },
  },
  setup(props) {
    return () => h('div', [
      h('div', { class: 'headers' }, props.columns.map((column) => h('span', { class: `header-${column.key}` }, column.title))),
      ...props.data.flatMap((row) => props.columns.map((column) => h('div', { class: `cell-${column.key}` }, renderCellChildren(column, row)))),
    ])
  },
})
```

在 `describe('OrgKnowledgePage', () => { ... })` 内新增测试：

```ts
  // 覆盖企业知识库文件列表列头文案：文件名列必须明确显示为「文件名称」。
  it('企业知识库文件列表首列展示文件名称', () => {
    const wrapper = mountPage()

    expect(wrapper.find('.header-name').text()).toBe('文件名称')
  })
```

- [ ] **Step 2: 运行测试确认失败**

Run:

```bash
rtk npm --prefix web run test -- OrgKnowledgePage.spec.ts
```

Expected: FAIL，断言显示 `.header-name` 的文本仍为 `名称`。

- [ ] **Step 3: 修改企业知识库列头**

在 `web/src/pages/knowledge/OrgKnowledgePage.vue` 中把首列从：

```ts
  {
    title: '名称', key: 'name',
    render: (row) => h('strong', row.name),
  },
```

改为：

```ts
  {
    title: '文件名称', key: 'name',
    render: (row) => h('strong', row.name),
  },
```

- [ ] **Step 4: 运行测试确认通过**

Run:

```bash
rtk npm --prefix web run test -- OrgKnowledgePage.spec.ts
```

Expected: PASS。

### Task 2: 实例知识库列头与测试

**Files:**
- Modify: `web/src/pages/apps/AppKnowledgeTab.vue`
- Modify: `web/src/pages/apps/AppKnowledgeTab.spec.ts`

- [ ] **Step 1: 写实例知识库列头测试**

在 `web/src/pages/apps/AppKnowledgeTab.spec.ts` 中更新 `RenderableColumn`，增加 `title` 字段：

```ts
type RenderableColumn = {
  key: string
  title?: string
  render?: (row: unknown) => VNodeChild
}
```

把 `DataTableStub` 改为先渲染表头，再渲染单元格：

```ts
const DataTableStub = defineComponent({
  props: {
    columns: { type: Array as PropType<RenderableColumn[]>, default: () => [] },
    data: { type: Array as PropType<unknown[]>, default: () => [] },
  },
  setup(props) {
    return () => h('div', [
      h('div', { class: 'headers' }, props.columns.map((column) => h('span', { class: `header-${column.key}` }, column.title))),
      ...props.data.flatMap((row) => props.columns.map((column) => h('div', { class: `cell-${column.key}` }, renderCellChildren(column, row)))),
    ])
  },
})
```

在 `describe('AppKnowledgeTab', () => { ... })` 内新增测试：

```ts
  // 覆盖实例知识库文件列表列头文案：文件名列必须明确显示为「文件名称」。
  it('实例知识库文件列表首列展示文件名称', () => {
    const wrapper = mountTab()

    expect(wrapper.find('.header-name').text()).toBe('文件名称')
  })
```

- [ ] **Step 2: 运行测试确认失败**

Run:

```bash
rtk npm --prefix web run test -- AppKnowledgeTab.spec.ts
```

Expected: FAIL，断言显示 `.header-name` 的文本仍为 `名称`。

- [ ] **Step 3: 修改实例知识库列头**

在 `web/src/pages/apps/AppKnowledgeTab.vue` 中把首列从：

```ts
  { title: '名称', key: 'name', render: (row) => h('strong', row.name) },
```

改为：

```ts
  { title: '文件名称', key: 'name', render: (row) => h('strong', row.name) },
```

- [ ] **Step 4: 运行测试确认通过**

Run:

```bash
rtk npm --prefix web run test -- AppKnowledgeTab.spec.ts
```

Expected: PASS。

### Task 3: 工作目录列头与测试

**Files:**
- Modify: `web/src/pages/apps/AppWorkspaceTab.vue`
- Create: `web/src/pages/apps/AppWorkspaceTab.spec.ts`

- [ ] **Step 1: 新增工作目录列头测试**

创建 `web/src/pages/apps/AppWorkspaceTab.spec.ts`：

```ts
import { mount } from '@vue/test-utils'
import { defineComponent, h, ref, type PropType, type VNodeChild } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import AppWorkspaceTab from './AppWorkspaceTab.vue'

type RenderableColumn = {
  key: string
  title?: string
  render?: (row: unknown) => VNodeChild
}

type RenderedChild = NonNullable<VNodeChild>

function renderCellChildren(column: RenderableColumn, row: unknown): RenderedChild[] {
  const child = column.render?.(row)
  return child == null ? [] : [child as RenderedChild]
}

// DataTableStub 渲染列标题和单元格，确保工作目录文件名列的用户可见文案被测试覆盖。
const DataTableStub = defineComponent({
  props: {
    columns: { type: Array as PropType<RenderableColumn[]>, default: () => [] },
    data: { type: Array as PropType<unknown[]>, default: () => [] },
  },
  setup(props) {
    return () => h('div', [
      h('div', { class: 'headers' }, props.columns.map((column) => h('span', { class: `header-${column.key}` }, column.title))),
      ...props.data.flatMap((row) => props.columns.map((column) => h('div', { class: `cell-${column.key}` }, renderCellChildren(column, row)))),
    ])
  },
})

vi.mock('@/api/hooks/useWorkspace', async () => {
  const actual = await vi.importActual<typeof import('@/api/hooks/useWorkspace')>('@/api/hooks/useWorkspace')
  return {
    ...actual,
    useWorkspaceQuery: () => ({
      data: ref({
        path: '',
        entries: [{ path: 'readme.md', name: 'readme.md', size: 12, is_dir: false }],
      }),
      isLoading: ref(false),
      error: ref(null),
    }),
  }
})

function mountTab() {
  return mount(AppWorkspaceTab, {
    props: { appId: 'app-1' },
    global: {
      stubs: {
        NCard: { template: '<section><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        NSpace: { template: '<div><slot /></div>' },
        NDataTable: DataTableStub,
        NButton: { template: '<button><slot /></button>' },
      },
    },
  })
}

describe('AppWorkspaceTab', () => {
  // 覆盖工作目录文件列表列头文案：文件和目录的名称列必须明确显示为「文件名称」。
  it('工作目录文件列表首列展示文件名称', () => {
    const wrapper = mountTab()

    expect(wrapper.find('.header-name').text()).toBe('文件名称')
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run:

```bash
rtk npm --prefix web run test -- AppWorkspaceTab.spec.ts
```

Expected: FAIL，断言显示 `.header-name` 的文本仍为 `名称`。

- [ ] **Step 3: 修改工作目录列头**

在 `web/src/pages/apps/AppWorkspaceTab.vue` 中把首列从：

```ts
  {
    title: '名称', key: 'name',
    render: (row) => row.is_dir
      ? h('strong', { style: 'cursor: pointer; color: var(--color-info-text); text-decoration: underline dotted', onClick: () => enter(row) }, `${row.name}/`)
      : row.name,
  },
```

改为：

```ts
  {
    title: '文件名称', key: 'name',
    render: (row) => row.is_dir
      ? h('strong', { style: 'cursor: pointer; color: var(--color-info-text); text-decoration: underline dotted', onClick: () => enter(row) }, `${row.name}/`)
      : row.name,
  },
```

- [ ] **Step 4: 运行测试确认通过**

Run:

```bash
rtk npm --prefix web run test -- AppWorkspaceTab.spec.ts
```

Expected: PASS。

### Task 4: 集成验证与提交

**Files:**
- Verify only; no planned source edits.

- [ ] **Step 1: 运行三项相关组件测试**

Run:

```bash
rtk npm --prefix web run test -- OrgKnowledgePage.spec.ts AppKnowledgeTab.spec.ts AppWorkspaceTab.spec.ts
```

Expected: PASS。

- [ ] **Step 2: 运行前端类型检查**

Run:

```bash
rtk npm --prefix web run typecheck
```

Expected: PASS。

- [ ] **Step 3: 浏览器验证**

启动前端开发服务后，用真实浏览器分别查看：

- `/apps/<appId>/workspace`
- `/knowledge`
- `/apps/<appId>/knowledge`

Expected: 三个页面文件列表首列均显示「文件名称」，文件名、下载、删除、重解析、目录进入按钮仍按原逻辑展示。

- [ ] **Step 4: 检查工作区只包含相关改动**

Run:

```bash
rtk git status --short
```

Expected: 只出现本计划列出的源文件和测试文件改动；允许保留任务开始前已存在的 `scripts/check-compose-bind-mounts.sh` 与 `docs/reports/` 未归属改动，但不得暂存或提交它们。

- [ ] **Step 5: 提交实现**

Run:

```bash
rtk git add web/src/pages/apps/AppWorkspaceTab.vue web/src/pages/apps/AppWorkspaceTab.spec.ts web/src/pages/knowledge/OrgKnowledgePage.vue web/src/pages/knowledge/OrgKnowledgePage.spec.ts web/src/pages/apps/AppKnowledgeTab.vue web/src/pages/apps/AppKnowledgeTab.spec.ts docs/superpowers/plans/2026-05-28-file-name-column-copy.md
rtk git commit -m "fix(web): 调整文件列表名称列文案" -m "将工作目录、企业知识库、实例知识库的文件列表首列从「名称」改为「文件名称」。\n\n补充对应组件测试覆盖列头文案，并保持 API 字段、下载和知识库操作逻辑不变。"
```

Expected: 提交成功，且未包含任务开始前已存在的无关改动。

## Self-Review

- Spec coverage: 三个目标页面都各有一个实现任务，非范围中的 API 字段、后端接口、OpenAPI 和其他名称页面均未纳入修改。
- Placeholder scan: 本计划没有占位符或未展开的延后实现步骤。
- Type consistency: 三个测试 stub 都使用同一个 `RenderableColumn` 形状：`key`、可选 `title`、可选 `render`，与 Naive UI 列配置中本次使用的字段一致。
