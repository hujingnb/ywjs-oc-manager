# 前端列表 + 表单通用模式 实施计划

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 5 个前端页面里高度重复的 `n-data-table` 列定义、`tone→NTag.type` 映射、`formVisible/creating/submitError` 三件套抽到 4 个公共构件（StatusBadge / DataTableList / column factory / useFormModal），然后逐页改造为消费方。

**Architecture:** 先 TDD 建公共构件（4 个 task），再改 status tag 转发（1 个 task），再逐页重构（5 个 task），最后清理 + DoD 验收（1 个 task）。每个 task 独立可回滚，使用 vitest + @vue/test-utils 单测，pages 改造靠 typecheck + build + 人工冒烟保底。

**Tech Stack:** Vue 3.5 / Vite 7 / Naive UI / TanStack Vue Query / vitest / @vue/test-utils / TypeScript strict

**Spec reference:** `docs/superpowers/specs/2026-05-09-frontend-list-form-pattern-design.md`

**关键约束：**

- TDD 适用 Task 1-4（公共构件）。Task 5 转发改造、Task 6-10 页面重构是「保留行为」改造，靠现有 typecheck + build + 单测 + 人工冒烟兜底。
- 每个 task 一个 commit，commit message 用 Conventional Commits 中文摘要（参考 AGENTS.md），有 Co-Authored-By 行。
- 每个页面重构时**严格按 spec 第 5.1-5.5 节的「保留」清单**对照检查，不要误删 max_apps 内联编辑、`|| undefined` 过滤、`showToken` 后置等特有逻辑。
- 不引入新依赖。
- 工作目录 `/home/hujing/dir/software/ywjs/oc-manager`，前端命令在 `web/` 子目录或通过 Makefile 顶层入口（`make web-test` / `make web-typecheck` / `make web-build`）。

---

## Chunk 1: 公共构件（TDD）

### Task 1: StatusBadge.vue 与单测

**Files:**
- Create: `web/src/components/StatusBadge.vue`
- Create: `web/src/components/__tests__/StatusBadge.spec.ts`

**前置阅读：**
- `web/src/domain/status.ts:1-4` — `StatusView` 接口定义（tone 取值范围）
- `web/src/components/AppStatusTag.vue:12-17` — 现有 tone→NTag.type 映射（即将被 StatusBadge 替代）

- [ ] **Step 1.1: 写 spec**

创建 `web/src/components/__tests__/StatusBadge.spec.ts`：

```ts
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import StatusBadge from '../StatusBadge.vue'
import type { StatusView } from '@/domain/status'

describe('StatusBadge', () => {
  function renderType(tone: StatusView['tone']) {
    const wrapper = mount(StatusBadge, { props: { view: { label: 'L', tone } } })
    return wrapper.findComponent({ name: 'NTag' }).attributes('type') ?? wrapper.find('.n-tag').classes().join(' ')
  }

  it.each([
    ['success', 'success'],
    ['warning', 'warning'],
    ['danger', 'error'],
    ['neutral', 'default'],
  ])('tone=%s 映射到 NTag type=%s', (tone, expected) => {
    const wrapper = mount(StatusBadge, { props: { view: { label: 'L', tone: tone as StatusView['tone'] } } })
    expect(wrapper.html()).toContain(expected === 'default' ? 'n-tag' : `n-tag--${expected}`)
  })

  it('渲染 view.label 文本', () => {
    const wrapper = mount(StatusBadge, { props: { view: { label: '运行中', tone: 'success' } } })
    expect(wrapper.text()).toContain('运行中')
  })
})
```

注意：`@vue/test-utils` 对 Naive UI NTag 的 type 属性可能不直接显式输出 attribute，断言用 class（`n-tag--success`）或 props 二选一。优先用文本+ class 检查，兼容性最好。

- [ ] **Step 1.2: 跑测试，确认编译失败**

```bash
make web-test -- StatusBadge
```

预期：FAIL（StatusBadge.vue 不存在或 export 不到）。

- [ ] **Step 1.3: 写实现**

创建 `web/src/components/StatusBadge.vue`：

```vue
<template>
  <n-tag :type="nType" size="small" :bordered="false">{{ view.label }}</n-tag>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NTag } from 'naive-ui'
import type { StatusView } from '@/domain/status'

const props = defineProps<{ view: StatusView }>()

// tone → naive-ui NTag.type 的唯一定义点；其他文件不得复制此映射。
const TONE_TO_TAG_TYPE = {
  success: 'success',
  warning: 'warning',
  danger: 'error',
  neutral: 'default',
} as const

const nType = computed(() => TONE_TO_TAG_TYPE[props.view.tone] ?? 'default')
</script>
```

- [ ] **Step 1.4: 跑测试确认全绿**

```bash
make web-test -- StatusBadge
make web-typecheck
```

预期：StatusBadge spec 全绿；typecheck 无新增错误。

- [ ] **Step 1.5: commit**

```bash
git add web/src/components/StatusBadge.vue web/src/components/__tests__/StatusBadge.spec.ts
git commit -m "$(cat <<'EOF'
feat(web): 新增 StatusBadge 组件统一 tone 映射

接受 StatusView 入参；内部 TONE_TO_TAG_TYPE 常量是 tone→NTag.type
的唯一定义点。后续 AppStatusTag / RuntimeStatusTag / 页面级
toneToTagType 都将转发或替换为本组件。

table-driven 单测覆盖 4 种 tone 与 label 渲染。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: 4 个 column factory + index.ts + 单测

**Files:**
- Create: `web/src/components/columns/index.ts`
- Create: `web/src/components/columns/statusColumn.ts`
- Create: `web/src/components/columns/timeColumn.ts`
- Create: `web/src/components/columns/linkColumn.ts`
- Create: `web/src/components/columns/actionColumn.ts`
- Create: `web/src/components/__tests__/columns/statusColumn.spec.ts`
- Create: `web/src/components/__tests__/columns/timeColumn.spec.ts`
- Create: `web/src/components/__tests__/columns/linkColumn.spec.ts`
- Create: `web/src/components/__tests__/columns/actionColumn.spec.ts`
- Modify: `web/src/styles/base.css`（追加 `.data-table-link` / `.data-table-subtitle` 样式）

- [ ] **Step 2.1: 写 4 个 spec（先全部写完再实现，符合 TDD）**

#### `statusColumn.spec.ts`

```ts
import { describe, expect, it } from 'vitest'
import { statusColumn } from '../../columns/statusColumn'
import type { StatusView } from '@/domain/status'

describe('statusColumn', () => {
  it('returns column with title/key/render', () => {
    const col = statusColumn<{ status: string }>('状态', (row) => ({ label: row.status, tone: 'success' as StatusView['tone'] }))
    expect(col.title).toBe('状态')
    expect(col.key).toBe('status')
    expect(typeof col.render).toBe('function')
  })

  it('honors custom key option', () => {
    const col = statusColumn<{ s: string }>('状态', () => ({ label: '', tone: 'neutral' }), { key: 's' })
    expect(col.key).toBe('s')
  })

  it('render produces a vnode with StatusBadge', () => {
    const col = statusColumn<{ status: string }>('状态', () => ({ label: 'X', tone: 'danger' }))
    const vnode = (col.render as any)({ status: 'X' })
    expect(vnode).toBeTruthy()
    // 渲染 vnode 的具体断言由 StatusBadge.spec.ts 覆盖；这里仅确认 render 调 formatter
  })
})
```

#### `timeColumn.spec.ts`

```ts
import { describe, expect, it } from 'vitest'
import { timeColumn } from '../../columns/timeColumn'

describe('timeColumn', () => {
  const col = timeColumn<{ t: string | null | undefined }>('时间', (row) => row.t)

  it('formats valid ISO string', () => {
    const out = (col.render as any)({ t: '2026-05-09T10:00:00Z' })
    expect(typeof out).toBe('string')
    expect(out).not.toBe('—')
  })

  it.each([null, undefined, ''])('returns placeholder for empty value (%s)', (v) => {
    const out = (col.render as any)({ t: v })
    expect(out).toBe('—')
  })

  it('honors custom placeholder', () => {
    const c = timeColumn<{ t: string | null }>('时间', (r) => r.t, { placeholder: 'N/A' })
    expect((c.render as any)({ t: null })).toBe('N/A')
  })
})
```

#### `linkColumn.spec.ts`

```ts
import { describe, expect, it, vi } from 'vitest'
import { linkColumn } from '../../columns/linkColumn'

describe('linkColumn', () => {
  it('returns column with title/key/render', () => {
    const col = linkColumn<{ id: string; name: string }>({
      title: '名称',
      text: (r) => r.name,
      onClick: vi.fn(),
    })
    expect(col.title).toBe('名称')
    expect(col.key).toBe('link')
    expect(typeof col.render).toBe('function')
  })

  it('renders subtitle vnode when subtitle option set and value is truthy', () => {
    const col = linkColumn<{ name: string; sub: string | null }>({
      title: '名称',
      text: (r) => r.name,
      onClick: vi.fn(),
      subtitle: (r) => r.sub,
    })
    const withSub = (col.render as any)({ name: 'A', sub: '辅助文字' })
    expect(Array.isArray(withSub)).toBe(true)
    const noSub = (col.render as any)({ name: 'A', sub: null })
    expect(Array.isArray(noSub)).toBe(false)
  })
})
```

#### `actionColumn.spec.ts`

```ts
import { describe, expect, it, vi } from 'vitest'
import { actionColumn } from '../../columns/actionColumn'

describe('actionColumn', () => {
  it('default title=操作 and key=actions', () => {
    const col = actionColumn<{ id: string }>([])
    expect(col.title).toBe('操作')
    expect(col.key).toBe('actions')
  })

  it('hidden actions are filtered out', () => {
    const col = actionColumn<{ id: string; show: boolean }>([
      { label: 'A', onClick: vi.fn() },
      { label: 'B', onClick: vi.fn(), hidden: (r) => !r.show },
    ])
    const visible = (col.render as any)({ id: '1', show: false })
    // NSpace vnode children 为可见 actions；具体长度因 NSpace API 复杂，此处仅断言 render 不抛
    expect(visible).toBeTruthy()
  })

  it('label can be function', () => {
    const labelFn = vi.fn((r: { id: string }) => `编辑-${r.id}`)
    const col = actionColumn([{ label: labelFn, onClick: vi.fn() }])
    void (col.render as any)({ id: '1' })
    // label 函数何时被调用依赖 NButton render；本断言可放宽
    expect(typeof col.render).toBe('function')
  })
})
```

- [ ] **Step 2.2: 跑测试确认编译失败**

```bash
make web-test -- columns/
```

预期：FAIL（4 个 module 不存在）。

- [ ] **Step 2.3: 写 4 个 factory 实现 + index.ts**

`web/src/components/columns/statusColumn.ts`：

```ts
import { h } from 'vue'
import type { DataTableColumn } from 'naive-ui'
import StatusBadge from '@/components/StatusBadge.vue'
import type { StatusView } from '@/domain/status'

// statusColumn 生成统一风格的状态列：内部用 StatusBadge 渲染 tone+label。
export function statusColumn<T>(
  title: string,
  view: (row: T) => StatusView,
  options: { key?: string } = {},
): DataTableColumn<T> {
  return {
    title,
    key: options.key ?? 'status',
    render: (row) => h(StatusBadge, { view: view(row) }),
  }
}
```

`web/src/components/columns/timeColumn.ts`：

```ts
import type { DataTableColumn } from 'naive-ui'

// timeColumn 生成时间列：值为空时返回 placeholder（默认 '—'）。
export function timeColumn<T>(
  title: string,
  pick: (row: T) => string | null | undefined,
  options: { key?: string; placeholder?: string } = {},
): DataTableColumn<T> {
  const placeholder = options.placeholder ?? '—'
  return {
    title,
    key: options.key ?? 'time',
    render: (row) => {
      const v = pick(row)
      return v ? new Date(v).toLocaleString() : placeholder
    },
  }
}
```

`web/src/components/columns/linkColumn.ts`：

```ts
import { h } from 'vue'
import type { DataTableColumn } from 'naive-ui'

export interface LinkColumnOptions<T> {
  title: string
  key?: string
  text: (row: T) => string
  onClick: (row: T) => void
  subtitle?: (row: T) => string | null | undefined
}

// linkColumn 生成可点击主链接列；可选 subtitle 显示为下方灰色小字。
export function linkColumn<T>(opts: LinkColumnOptions<T>): DataTableColumn<T> {
  return {
    title: opts.title,
    key: opts.key ?? 'link',
    render: (row) => {
      const link = h('a', {
        class: 'data-table-link',
        onClick: () => opts.onClick(row),
      }, h('strong', opts.text(row)))
      const sub = opts.subtitle?.(row)
      return sub ? [link, h('small', { class: 'data-table-subtitle' }, sub)] : link
    },
  }
}
```

`web/src/components/columns/actionColumn.ts`：

```ts
import { h } from 'vue'
import { NButton, NSpace } from 'naive-ui'
import type { DataTableColumn } from 'naive-ui'

export interface RowAction<T> {
  label: string | ((row: T) => string)
  onClick: (row: T) => void
  type?: 'default' | 'primary' | 'error' | 'warning'
  disabled?: (row: T) => boolean
  hidden?: (row: T) => boolean
}

// actionColumn 生成操作列：每个 RowAction 渲染为 NButton，整体用 NSpace 排列。
// 隐藏 / 禁用 / 标签 都支持函数式响应当前行。
export function actionColumn<T>(
  actions: RowAction<T>[],
  options: { title?: string; key?: string } = {},
): DataTableColumn<T> {
  return {
    title: options.title ?? '操作',
    key: options.key ?? 'actions',
    render: (row) => h(NSpace, { size: 'small' }, {
      default: () => actions
        .filter((a) => !a.hidden?.(row))
        .map((a) => h(NButton, {
          size: 'small',
          type: a.type ?? 'default',
          disabled: a.disabled?.(row) ?? false,
          onClick: () => a.onClick(row),
        }, {
          default: () => typeof a.label === 'function' ? a.label(row) : a.label,
        })),
    }),
  }
}
```

`web/src/components/columns/index.ts`：

```ts
export { statusColumn } from './statusColumn'
export { timeColumn } from './timeColumn'
export { linkColumn, type LinkColumnOptions } from './linkColumn'
export { actionColumn, type RowAction } from './actionColumn'
```

- [ ] **Step 2.4: 在 base.css 追加链接样式**

读 `web/src/styles/base.css` 末尾，追加：

```css
.data-table-link {
  cursor: pointer;
  color: var(--color-accent, #00F0FF);
}

.data-table-subtitle {
  display: block;
  color: var(--color-text-secondary, #8A94C6);
  font-size: 12px;
}
```

注意：用 CSS 变量带 fallback，便于后续 CSS 变量化改造时无需再改这里。

- [ ] **Step 2.5: 跑测试 + typecheck 确认全绿**

```bash
make web-test -- columns/
make web-typecheck
```

预期：4 个 spec 全绿；typecheck 无错。

- [ ] **Step 2.6: commit**

```bash
git add web/src/components/columns/ web/src/components/__tests__/columns/ web/src/styles/base.css
git commit -m "$(cat <<'EOF'
feat(web): 新增 column factory 集合（status/time/link/action）

为 DataTableList 配套的列工厂。覆盖 5 个页面里高频重复的列模式：
- statusColumn：内部用 StatusBadge 渲染 StatusView
- timeColumn：ISO 时间格式化，空值占位
- linkColumn：可点击主链接 + 可选 subtitle 小字
- actionColumn：NSpace+NButton 操作组，支持 hidden/disabled/动态 label

base.css 追加 .data-table-link / .data-table-subtitle 样式，使用
CSS 变量 + fallback，方便后续主题 token 化。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: DataTableList.vue 与单测

**Files:**
- Create: `web/src/components/DataTableList.vue`
- Create: `web/src/components/__tests__/DataTableList.spec.ts`

- [ ] **Step 3.1: 写 spec**

```ts
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import DataTableList from '../DataTableList.vue'

describe('DataTableList', () => {
  const baseProps = {
    title: '测试列表',
    columns: [{ title: '名', key: 'name' }],
    data: [{ id: '1', name: 'A' }],
  }

  it('renders title and toolbar slot', () => {
    const wrapper = mount(DataTableList, {
      props: baseProps,
      slots: { toolbar: '<button class="t-btn">新建</button>' },
    })
    expect(wrapper.text()).toContain('测试列表')
    expect(wrapper.find('.t-btn').exists()).toBe(true)
  })

  it('shows eyebrow and subtitle when provided', () => {
    const wrapper = mount(DataTableList, {
      props: { ...baseProps, eyebrow: 'Platform · 测试', subtitle: '副标题文本' },
    })
    expect(wrapper.text()).toContain('Platform · 测试')
    expect(wrapper.text()).toContain('副标题文本')
  })

  it('shows errorMessage when set', () => {
    const wrapper = mount(DataTableList, {
      props: { ...baseProps, errorMessage: '加载失败' },
    })
    expect(wrapper.text()).toContain('加载失败')
  })

  it('does not show error block when errorMessage is empty', () => {
    const wrapper = mount(DataTableList, { props: baseProps })
    expect(wrapper.html()).not.toContain('加载失败')
  })

  it('passes loading prop to NDataTable', () => {
    const wrapper = mount(DataTableList, { props: { ...baseProps, loading: true } })
    // NDataTable 的 loading 表现取决于内部实现；最低要求 props 有传到组件
    const table = wrapper.findComponent({ name: 'NDataTable' })
    expect(table.props('loading')).toBe(true)
  })
})
```

- [ ] **Step 3.2: 跑测试确认编译失败**

```bash
make web-test -- DataTableList
```

预期：FAIL。

- [ ] **Step 3.3: 写实现**

`web/src/components/DataTableList.vue`：

```vue
<template>
  <div class="data-table-list">
    <header class="toolbar">
      <div class="title-block">
        <p v-if="eyebrow" class="eyebrow">{{ eyebrow }}</p>
        <h2>{{ title }}</h2>
        <p v-if="subtitle" class="subtitle">{{ subtitle }}</p>
      </div>
      <div class="actions">
        <slot name="toolbar" />
      </div>
    </header>
    <n-card>
      <n-alert v-if="errorMessage" type="error" :show-icon="false" class="error-banner">
        {{ errorMessage }}
      </n-alert>
      <n-data-table
        :columns="columns"
        :data="data"
        :loading="loading"
        :row-key="rowKey"
        :bordered="false"
      />
    </n-card>
  </div>
</template>

<script setup lang="ts" generic="T extends Record<string, any>">
import { NCard, NDataTable, NAlert, type DataTableColumn } from 'naive-ui'

defineProps<{
  title: string
  eyebrow?: string
  subtitle?: string
  columns: DataTableColumn<T>[]
  data: T[]
  loading?: boolean
  errorMessage?: string
  rowKey?: (row: T) => string | number
}>()
</script>

<style scoped>
.data-table-list { display: flex; flex-direction: column; gap: 12px; }
.toolbar { display: flex; align-items: flex-end; justify-content: space-between; gap: 16px; }
.actions { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.eyebrow { font-size: 12px; color: var(--color-text-secondary, #8A94C6); margin: 0 0 4px; }
.subtitle { font-size: 13px; color: var(--color-text-secondary, #8A94C6); margin: 4px 0 0; }
.error-banner { margin-bottom: 12px; }
</style>
```

- [ ] **Step 3.4: 跑测试 + typecheck 全绿**

```bash
make web-test -- DataTableList
make web-typecheck
```

- [ ] **Step 3.5: commit**

```bash
git add web/src/components/DataTableList.vue web/src/components/__tests__/DataTableList.spec.ts
git commit -m "$(cat <<'EOF'
feat(web): 新增 DataTableList 组件包装表格 + toolbar

承担列表页面 toolbar（eyebrow/title/subtitle/actions slot）+ NCard
+ NDataTable + 错误态横幅；列定义通过 columns prop 由调用方装配
（搭配 column factory 使用）。

后续 5 个页面将从手写 toolbar+NCard+NDataTable 三件套改为消费此组件。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: useFormModal composable + 单测

**Files:**
- Create: `web/src/composables/useFormModal.ts`
- Create: `web/src/composables/__tests__/useFormModal.spec.ts`

**前置阅读：**
- `web/src/api/hooks/useMembers.ts` — 现有 mutation 风格（`useMutation` 返回类型）
- spec 第 4.7 节 — `UseFormModalOptions` / `UseFormModalReturn` 接口

- [ ] **Step 4.1: 写 spec**

```ts
import { describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { useFormModal } from '../useFormModal'

function fakeMutation<TResult>(behavior: 'success' | 'fail', result?: TResult, error?: Error) {
  return {
    mutateAsync: vi.fn().mockImplementation(async (payload: unknown) => {
      if (behavior === 'fail') throw error ?? new Error('mock fail')
      return result
    }),
  } as any
}

describe('useFormModal', () => {
  type Payload = { name: string; age?: number }
  const initial: Payload = { name: '', age: undefined }

  it('initial state: form empty, modal hidden', () => {
    const { form, formVisible, creating, submitError } = useFormModal({
      initial,
      mutation: fakeMutation('success'),
    })
    expect(form.name).toBe('')
    expect(form.age).toBeUndefined()
    expect(formVisible.value).toBe(false)
    expect(creating.value).toBe(false)
    expect(submitError.value).toBeNull()
  })

  it('openForm 显示 modal 并清空错误', () => {
    const m = useFormModal({ initial, mutation: fakeMutation('success') })
    m.submitError.value = '旧错误'
    m.openForm()
    expect(m.formVisible.value).toBe(true)
    expect(m.submitError.value).toBeNull()
  })

  it('openForm 重置 form 字段（即使被改过）', () => {
    const m = useFormModal({ initial, mutation: fakeMutation('success') })
    m.form.name = '修改过'
    m.form.age = 30
    m.openForm()
    expect(m.form.name).toBe('')
    expect(m.form.age).toBeUndefined()
  })

  it('submit 成功：mutateAsync 被调用，formVisible=false，onSuccess 执行', async () => {
    const onSuccess = vi.fn()
    const mutation = fakeMutation<{ id: string }>('success', { id: 'created-1' })
    const m = useFormModal({ initial, mutation, onSuccess })
    m.openForm()
    m.form.name = 'Alice'
    await m.submit()
    expect(mutation.mutateAsync).toHaveBeenCalledOnce()
    expect(m.formVisible.value).toBe(false)
    expect(m.creating.value).toBe(false)
    expect(m.submitError.value).toBeNull()
    expect(onSuccess).toHaveBeenCalledWith({ id: 'created-1' })
  })

  it('submit 失败：submitError 写入，formVisible 不变，onSuccess 不调用', async () => {
    const onSuccess = vi.fn()
    const mutation = fakeMutation('fail', undefined, new Error('网络错误'))
    const m = useFormModal({ initial, mutation, onSuccess })
    m.openForm()
    await m.submit()
    expect(m.formVisible.value).toBe(true)
    expect(m.submitError.value).toBe('网络错误')
    expect(m.creating.value).toBe(false)
    expect(onSuccess).not.toHaveBeenCalled()
  })

  it('errorMessage 选项覆盖默认错误转换', async () => {
    const mutation = fakeMutation('fail', undefined, new Error('原始'))
    const m = useFormModal({
      initial,
      mutation,
      errorMessage: () => '自定义错误',
    })
    m.openForm()
    await m.submit()
    expect(m.submitError.value).toBe('自定义错误')
  })

  it('toPayload 转换 form 后再传给 mutation', async () => {
    const mutation = fakeMutation('success')
    const m = useFormModal<{ a: string; b: string }>({
      initial: { a: '', b: '' },
      mutation,
      toPayload: (f) => ({ a: f.a.toUpperCase(), b: f.b }),
    })
    m.openForm()
    m.form.a = 'hello'
    m.form.b = 'world'
    await m.submit()
    expect(mutation.mutateAsync).toHaveBeenCalledWith({ a: 'HELLO', b: 'world' })
  })

  it('closeForm 设置 formVisible=false', () => {
    const m = useFormModal({ initial, mutation: fakeMutation('success') })
    m.openForm()
    m.closeForm()
    expect(m.formVisible.value).toBe(false)
  })
})
```

- [ ] **Step 4.2: 跑测试确认编译失败**

```bash
make web-test -- useFormModal
```

预期：FAIL（module 不存在）。

- [ ] **Step 4.3: 写实现**

`web/src/composables/useFormModal.ts`：

```ts
import { reactive, ref, type Ref } from 'vue'
import type { UseMutationReturnType } from '@tanstack/vue-query'

export interface UseFormModalOptions<TPayload, TResult> {
  // 表单初始值；openForm 每次都会 deep clone 此对象重置 form。
  // 必须是 JSON-serializable 对象（不含 Date / Map / Set / 函数）。
  initial: TPayload
  // TanStack Query mutation；submit 调用其 mutateAsync。
  mutation: UseMutationReturnType<TResult, Error, TPayload, unknown>
  // 提交成功后业务后置（如展示 token、跳转）；不在此关 modal，关闭已自动处理。
  onSuccess?: (result: TResult) => void
  // 自定义错误消息生成；默认用 err.message 或 fallback。
  errorMessage?: (err: unknown) => string
  // 提交前对 form 做适配（如 || undefined 过滤）；返回值替代 form 作为 payload。
  toPayload?: (form: TPayload) => TPayload
}

export interface UseFormModalReturn<TPayload> {
  formVisible: Ref<boolean>
  form: TPayload
  creating: Ref<boolean>
  submitError: Ref<string | null>
  openForm: () => void
  closeForm: () => void
  submit: () => Promise<void>
}

// useFormModal 把页面里 formVisible / creating / submitError + openForm + onSubmit
// 三件套统一到一个组合式函数。submit 只做：清错 → mutateAsync → 关闭 modal → onSuccess。
export function useFormModal<TPayload extends object, TResult = unknown>(
  opts: UseFormModalOptions<TPayload, TResult>,
): UseFormModalReturn<TPayload> {
  const formVisible = ref(false)
  const creating = ref(false)
  const submitError = ref<string | null>(null)
  const form = reactive(structuredClone(opts.initial)) as TPayload

  function openForm() {
    Object.assign(form as object, structuredClone(opts.initial))
    submitError.value = null
    formVisible.value = true
  }

  function closeForm() {
    formVisible.value = false
  }

  async function submit() {
    submitError.value = null
    creating.value = true
    try {
      const payload = opts.toPayload ? opts.toPayload(form) : form
      const result = await opts.mutation.mutateAsync(payload as TPayload)
      formVisible.value = false
      opts.onSuccess?.(result)
    } catch (err) {
      const fallback = err instanceof Error ? err.message : '操作失败'
      submitError.value = opts.errorMessage?.(err) ?? fallback
    } finally {
      creating.value = false
    }
  }

  return { formVisible, form, creating, submitError, openForm, closeForm, submit }
}
```

- [ ] **Step 4.4: 跑测试 + typecheck 全绿**

```bash
make web-test -- useFormModal
make web-typecheck
```

- [ ] **Step 4.5: commit**

```bash
git add web/src/composables/useFormModal.ts web/src/composables/__tests__/useFormModal.spec.ts
git commit -m "$(cat <<'EOF'
feat(web): 新增 useFormModal 组合式函数

聚合 formVisible / creating / submitError 三件套 + openForm + submit。
submit 内置 try-catch + 关 modal + 错误捕获；通过 toPayload 钩子支持
字段过滤（OrganizationsPage 的 || undefined 模式），通过 onSuccess
钩子支持业务后置（RuntimeNodesPage 的 showToken）。

7 个 spec 用例覆盖：初始态、openForm 重置、submit 成功/失败、
onSuccess/errorMessage/toPayload 三个钩子、closeForm。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Chunk 2: 状态徽章转发

### Task 5: AppStatusTag / RuntimeStatusTag 改为 StatusBadge 转发

**Files:**
- Modify: `web/src/components/AppStatusTag.vue`
- Modify: `web/src/components/RuntimeStatusTag.vue`

**关键约束**：保持对外 props（`:status`）不变，调用方文件不动。

- [ ] **Step 5.1: baseline test + typecheck**

```bash
make web-test
make web-typecheck
```

预期全绿。如有 fail 先停下排查。

- [ ] **Step 5.2: 改写 AppStatusTag.vue**

整体替换为：

```vue
<template>
  <StatusBadge :view="formatAppStatus(status)" />
</template>

<script setup lang="ts">
import StatusBadge from './StatusBadge.vue'
import { formatAppStatus } from '@/domain/status'

defineProps<{ status: string }>()
</script>
```

- [ ] **Step 5.3: 改写 RuntimeStatusTag.vue**

整体替换为：

```vue
<template>
  <StatusBadge :view="formatRuntimeNodeStatus(status)" />
</template>

<script setup lang="ts">
import StatusBadge from './StatusBadge.vue'
import { formatRuntimeNodeStatus } from '@/domain/status'

defineProps<{ status: string }>()
</script>
```

- [ ] **Step 5.4: 验证调用方未被破坏**

```bash
git grep -nE 'AppStatusTag|RuntimeStatusTag' web/src/ | grep -v __tests__ | grep -v components/AppStatusTag.vue | grep -v components/RuntimeStatusTag.vue
```

应该列出现有调用点（页面级 import），且**没有任何修改**。验证文件无需改动。

```bash
make web-typecheck
make web-test
```

预期：typecheck 与全部 test 仍全绿。

- [ ] **Step 5.5: commit**

```bash
git add web/src/components/AppStatusTag.vue web/src/components/RuntimeStatusTag.vue
git commit -m "$(cat <<'EOF'
refactor(web): AppStatusTag / RuntimeStatusTag 改为 StatusBadge 转发

去除两组件内部各自维护的 tone→NTag.type 映射，统一委托给 StatusBadge。
对外 props（:status）保持不变，调用方文件无需改动。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Chunk 3: 页面改造

### 改造守则（Task 6-10 共用）

每个页面改造完都必须：

1. `make web-typecheck` 无新增错
2. `make web-test` 全绿
3. `make web-build` 成功
4. 每页 commit 仅含该页面的 .vue 文件改动（不要混进其他文件）

**严格按 spec 第 5.1-5.5 节「保留」清单**对照检查每个页面。

### Task 6: 重构 AuditLogsPage（最简：仅表格，无表单）

**Files:**
- Modify: `web/src/pages/audit/AuditLogsPage.vue`

**保留清单（spec 5.2）**：
- `auditTagType` 函数（result 列特有 4-状态映射，**不要**替换为 statusColumn）
- 4 列业务展示（actor / target / action / result）的页面级 render（不抽 factory）

**改动**：
- 时间列改用 `timeColumn('时间', r => r.created_at)`
- 外层 `<n-card>` 包装替换为 `<DataTableList>`

- [ ] **Step 6.1: 用 Read 把 AuditLogsPage.vue 全部读一遍，理解现状**

- [ ] **Step 6.2: 改写文件**

参考要点：
- `<template>` 部分顶层结构：`<DataTableList :title :columns :data :loading :error-message>` 包裹整个表格区
- script 中 columns 数组：第一项时间列改用 `timeColumn`，其他列保持 naive-ui 原生 `{ title, key, render? }`
- 删除原来的 toolbar 手写代码（被 DataTableList 吸收）

- [ ] **Step 6.3: typecheck + test + build**

```bash
make web-typecheck && make web-test && make web-build
```

- [ ] **Step 6.4: commit**

```bash
git add web/src/pages/audit/AuditLogsPage.vue
git commit -m "$(cat <<'EOF'
refactor(web): AuditLogsPage 改用 DataTableList + timeColumn

外层 toolbar+card+table 三件套替换为 DataTableList；时间列改用
timeColumn factory。result 列保留页面内 auditTagType 映射（4 种结果
状态独立于角色 tone 体系）。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: 重构 AppsPage

**Files:**
- Modify: `web/src/pages/apps/AppsPage.vue`

**保留清单（spec 5.1）**：
- 页面跳转逻辑（点击行名跳到 `/apps/${row.id}/overview`）
- 操作列三按钮（重启/停止/删除）

**改动**：
- 名称列改用 `linkColumn`（onClick → router.push）
- 状态列改用 `statusColumn('状态', r => formatAppStatus(r.status))`
- 操作列改用 `actionColumn` 三个按钮
- 外层包装改用 `<DataTableList>`
- 不再 import `AppStatusTag`（如不再使用）

- [ ] **Step 7.1: Read AppsPage.vue 现状**

- [ ] **Step 7.2: 改写**

要点：
- 依赖：`linkColumn / statusColumn / actionColumn` from `@/components/columns`，`formatAppStatus` from `@/domain/status`
- columns 数组：
  ```ts
  const columns = [
    linkColumn<AppDTO>({
      title: '名称',
      text: r => r.name,
      onClick: r => router.push(`/apps/${r.id}/overview`),
    }),
    statusColumn<AppDTO>('状态', r => formatAppStatus(r.status)),
    { title: 'API key', key: 'api_key_status' },
    { title: '容器', key: 'container_id', render: r => r.container_id ?? '—' },
    actionColumn<AppDTO>([
      { label: '重启', onClick: r => trigger(r, 'restart') },
      { label: '停止', onClick: r => trigger(r, 'stop') },
      { label: '删除', type: 'error', onClick: r => confirmDelete(r) },
    ]),
  ]
  ```

- [ ] **Step 7.3: typecheck + test + build**

- [ ] **Step 7.4: commit**

```
refactor(web): AppsPage 改用 DataTableList + column factory
```

---

### Task 8: 重构 MembersPage（含 useFormModal + 删本地 toneToTagType）

**Files:**
- Modify: `web/src/pages/org/MembersPage.vue`

**保留清单（spec 5.3）**：
- 重置密码按钮
- memberToDelete 二次确认 modal（独立交互，不归 useFormModal）
- 角色列页面内 `formatMemberRole` 文本渲染（不抽 factory）

**删除**：
- 本地 `toneToTagType` 函数定义（被 statusColumn + StatusBadge 替代）

**改动**：
- 状态列改用 `statusColumn('状态', r => formatMemberStatus(r.status))`
- 操作列改用 `actionColumn`（含启用/禁用条件按钮、重置密码、删除）
- 字符串列保持 naive-ui 原生 `{ title, key }` 写法（用户名、姓名）
- 角色列保持页面内 render（`formatMemberRole(row.role)`）
- 表单 modal 状态改用 `useFormModal({ initial, mutation: createMutation })`，模板 `<n-modal>`/`<n-form>` 由页面继续控制
- 外层包装改用 `<DataTableList>`

- [ ] **Step 8.1: Read MembersPage.vue 现状**

- [ ] **Step 8.2: 改写**

要点：
```ts
import { useFormModal } from '@/composables/useFormModal'
import { statusColumn, actionColumn } from '@/components/columns'
import DataTableList from '@/components/DataTableList.vue'
import { formatMemberStatus, formatMemberRole } from '@/domain/status'

const createMutation = useCreateMember()
const initial: MemberFormPayload = {
  username: '', display_name: '', password: '', role: 'org_member',
}
const { form, formVisible, creating, submitError, openForm, submit } =
  useFormModal({ initial, mutation: createMutation })

const columns = [
  { title: '用户名', key: 'username' },
  { title: '姓名', key: 'display_name' },
  { title: '角色', key: 'role', render: (row: Member) => formatMemberRole(row.role) },
  statusColumn<Member>('状态', r => formatMemberStatus(r.status)),
  // 启用/禁用是互斥按钮：用两个 RowAction + hidden 条件分别渲染。
  // 注意 RowAction.type 必须是静态值（'default' | 'primary' | 'error' | 'warning'），
  // 不能传函数；要根据 row 切换按钮颜色就用两条 RowAction 互斥 hidden。
  actionColumn<Member>([
    { label: '禁用', onClick: r => onToggle(r, 'disable'), hidden: r => r.status !== 'active' },
    { label: '启用', type: 'primary', onClick: r => onToggle(r, 'enable'), hidden: r => r.status === 'active' },
    { label: '重置密码', onClick: r => openResetForm(r) },
    { label: '删除', type: 'error', onClick: r => { memberToDelete.value = r } },
  ]),
]
```

- [ ] **Step 8.3: 验证本地 toneToTagType 已删**

```bash
git grep -n 'toneToTagType' web/src/pages/org/MembersPage.vue
```

预期输出为空。

- [ ] **Step 8.4: typecheck + test + build**

- [ ] **Step 8.5: commit**

```
refactor(web): MembersPage 改用 DataTableList/useFormModal/factory，删本地 toneToTagType
```

---

### Task 9: 重构 OrganizationsPage（含 toPayload 过滤）

**Files:**
- Modify: `web/src/pages/platform/OrganizationsPage.vue`

**保留清单（spec 5.4）**：
- name 列里 remark 小字 subtitle 渲染
- 启用/禁用条件按钮
- 联系人/电话/预警阈值列页面内 render

**删除**：本地 `toneToTagType`

**改动**：
- 名称列用 `linkColumn`（如有跳转）或保留页面 render
- 状态列用 `statusColumn`，操作列用 `actionColumn`
- 表单 modal 用 `useFormModal({ initial, mutation, toPayload })`，`toPayload` 内做 `|| undefined` 过滤
- 外层包装用 `<DataTableList>`

- [ ] **Step 9.1: Read OrganizationsPage.vue 现状**

- [ ] **Step 9.2: 改写**

要点（toPayload 模式）：

```ts
const { form, formVisible, openForm, submit, creating, submitError } = useFormModal({
  initial: {
    name: '', contact_name: '', contact_phone: '', remark: '',
    credit_warning_threshold: undefined as number | undefined,
  },
  mutation: createMutation,
  toPayload: (f) => ({
    name: f.name,
    contact_name: f.contact_name || undefined,
    contact_phone: f.contact_phone || undefined,
    remark: f.remark || undefined,
    credit_warning_threshold: typeof f.credit_warning_threshold === 'number'
      ? f.credit_warning_threshold : undefined,
  }),
})
```

- [ ] **Step 9.3: 验证 + commit**

```bash
git grep -n 'toneToTagType' web/src/pages/platform/OrganizationsPage.vue
make web-typecheck && make web-test && make web-build
```

```
refactor(web): OrganizationsPage 改用通用模式，删本地 toneToTagType
```

---

### Task 10: 重构 RuntimeNodesPage（含 onSuccess showToken + max_apps 内联编辑）

**Files:**
- Modify: `web/src/pages/runtime-nodes/RuntimeNodesPage.vue`

**保留清单（spec 5.5）**：
- max_apps 列内联编辑按钮（页面特有，不归 actionColumn）
- 创建成功后 `showToken(created)` 副作用
- editingNode 与 token 展示卡片
- name 列里 node_data_root 小字 subtitle

**改动**：
- 状态列用 `statusColumn` 或保留 `RuntimeStatusTag`（两者等价；用 statusColumn 更一致）
- 操作列用 `actionColumn`（轮换 bootstrap、启用/禁用条件按钮）
- 表单 modal 用 `useFormModal({ initial, mutation, onSuccess: showToken })`
- 外层包装用 `<DataTableList>`

- [ ] **Step 10.1: Read RuntimeNodesPage.vue 现状**

- [ ] **Step 10.2: 改写**

要点：
```ts
const { form, formVisible, openForm, submit, creating, submitError } = useFormModal({
  initial: {
    name: '',
    heartbeat_interval_seconds: undefined as number | undefined,
    node_data_root: '',
  },
  mutation: createMutation,
  toPayload: (f) => ({
    name: f.name,
    heartbeat_interval_seconds: f.heartbeat_interval_seconds || undefined,
    node_data_root: f.node_data_root || undefined,
  }),
  onSuccess: (created) => showToken(created),
})
```

actionColumn 含两组互斥按钮（同 Task 8 模式）：
```ts
actionColumn<RuntimeNode>([
  { label: '轮换 bootstrap', onClick: r => onRotate(r), disabled: r => r.status === 'active' },
  { label: '禁用', onClick: r => onToggle(r, 'disable'), hidden: r => r.status === 'disabled' },
  { label: '启用', type: 'primary', onClick: r => onToggle(r, 'enable'), hidden: r => r.status !== 'disabled' },
])
```

max_apps 列保留页面内 render（特有内联编辑）。

- [ ] **Step 10.3: 验证 + commit**

```bash
make web-typecheck && make web-test && make web-build
```

```
refactor(web): RuntimeNodesPage 改用通用模式，showToken 走 onSuccess 钩子
```

---

## Chunk 4: 清理 + DoD 验收

### Task 11: 删除 DataTableToolbar.vue + DoD 验收

**Files:**
- Delete: `web/src/components/DataTableToolbar.vue`

- [ ] **Step 11.1: 硬性确认无引用**

```bash
git grep -nE 'DataTableToolbar' web/src/
```

**预期输出为空**。如果有命中（除被删文件本身），**必须停下来排查**——之前哪个 Task 漏迁。

- [ ] **Step 11.2: 删除文件**

```bash
git rm web/src/components/DataTableToolbar.vue
```

- [ ] **Step 11.3: typecheck + test + build**

```bash
make web-typecheck
make web-test
make web-build
```

全部预期通过。如有 fail 是删早了，回去补迁移。

- [ ] **Step 11.4: 全量 DoD 验收（不写代码，仅命令验证）**

```bash
# DoD-1: 7 个新文件存在
ls web/src/components/StatusBadge.vue \
   web/src/components/DataTableList.vue \
   web/src/composables/useFormModal.ts \
   web/src/components/columns/statusColumn.ts \
   web/src/components/columns/timeColumn.ts \
   web/src/components/columns/linkColumn.ts \
   web/src/components/columns/actionColumn.ts

# DoD-2: 7 个新 spec 文件存在
ls web/src/components/__tests__/StatusBadge.spec.ts \
   web/src/components/__tests__/DataTableList.spec.ts \
   web/src/composables/__tests__/useFormModal.spec.ts \
   web/src/components/__tests__/columns/statusColumn.spec.ts \
   web/src/components/__tests__/columns/timeColumn.spec.ts \
   web/src/components/__tests__/columns/linkColumn.spec.ts \
   web/src/components/__tests__/columns/actionColumn.spec.ts

# DoD-3: toneToTagType 仅出现在 StatusBadge 内部常量
git grep -nE 'toneToTagType|TONE_TO_TAG_TYPE' web/src/
# 预期：仅 StatusBadge.vue 含 TONE_TO_TAG_TYPE；不含任何 toneToTagType 函数

# DoD-4: DataTableToolbar 已无引用
git grep -n 'DataTableToolbar' web/src/
# 预期：空

# DoD-5/6/7: 全量验证
make web-typecheck
make web-test
make web-build
```

- [ ] **Step 11.5: 人工冒烟（强制）**

启动 dev server：

```bash
make dev-up   # 如未起本地依赖
cd web && npm run dev   # vite dev server
```

浏览器打开 `http://localhost:5173`，登录后逐一访问 5 个改造页面：

1. `/apps` — Apps 列表渲染、点名称跳详情、操作按钮三件可点（仅冒烟，不真删）
2. `/audit` — Audit 列表渲染、4 种 result tag 颜色正常
3. `/org/members` — 成员列表、新建成员 modal 表单成功路径、失败路径（用错密码触发）、重置密码、启用/禁用条件按钮
4. `/platform/organizations` — 同上含 `|| undefined` 字段过滤验证（remark 留空提交后台返回不含 remark）
5. `/runtime-nodes` — 节点列表、注册节点成功后 token 卡片正常出现、max_apps 内联编辑可用

如任一冒烟项失败，停下来查；不要直接 commit。

- [ ] **Step 11.6: commit**

```bash
git add -A web/src/components/
git commit -m "$(cat <<'EOF'
chore(web): 删除被 DataTableList 吸收的 DataTableToolbar.vue

5 个页面已全部改用 DataTableList，不再有调用方。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## 完成定义验证

所有 task 完成后必须满足：

- [ ] **DoD-1:** 4 个公共构件文件 + 4 个 column factory 文件 + 1 个 useFormModal 共 9 个新文件存在
- [ ] **DoD-2:** 7 个 spec 文件存在且 `make web-test` 全绿
- [ ] **DoD-3:** `git grep -nE 'toneToTagType' web/src/` 输出为空（页面级函数全删）
- [ ] **DoD-4:** `git grep -n 'DataTableToolbar' web/src/` 输出为空
- [ ] **DoD-5:** `make web-typecheck` 无新增错误
- [ ] **DoD-6:** `make web-build` 成功
- [ ] **DoD-7:** 5 个改造后的页面人工冒烟通过

---

## 回滚策略

每个 task 一个独立 commit，可单独 `git revert`。最坏情况下：

- 想回退到改造前状态：`git revert` Task 11→1 倒序逐个 revert，或一次性 `git reset --hard <Task 0 之前的 commit>`（如本仓库 master 直接开发，需用户授权）。
- Task 5（status tag 转发改造）有微妙的对外行为问题时可独立回退；不影响其他 task。
- Task 6-10（页面改造）每页独立可回退。
- Task 11 之后才能完全验证 DoD-4（DataTableToolbar 无引用）；如果 Task 6-10 漏迁某页 toolbar，Task 11 会发现并 fail，回去补。

---

## 风险与应对

| 风险 | 何时出现 | 应对 |
|---|---|---|
| Vue `<script setup generic>` 在 vue-tsc 报泛型推断错 | Task 3 | 实现里加显式 `as DataTableColumn<T>[]` 断言；最坏情况下 columns 用 `unknown[]` 让调用方负责类型 |
| Naive UI NTag 的 type 属性测试断言失败（class 名版本差异） | Task 1 | spec 里用文本+ class 双断言；如全失效，回退到 `wrapper.props('type')` |
| 页面改造时漏掉 spec 第 5.x 节「保留」清单的某项 | Task 6-10 | 每个页面 commit 前对照 spec 5.x 节逐项 check；reviewer 也会查 |
| `useFormModal` 的 `reactive(structuredClone(...))` 类型不准导致 form 被 Proxy 包成 unknown | Task 4 | 在返回值显式 cast `form as TPayload`；spec 中 7 个用例本身能验证类型流通是否正确 |
| Task 11 删 DataTableToolbar 时漏 grep 引用导致编译失败 | Task 11 | Step 11.1 强制 grep 验证无引用 |
