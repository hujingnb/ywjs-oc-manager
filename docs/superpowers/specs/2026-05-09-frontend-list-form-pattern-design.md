# 前端列表 + 表单通用模式：DataTableList、StatusBadge、column factory、useFormModal

- 日期：2026-05-09
- 范围：`web/src/`（Vue 3 + Vite + Naive UI + TanStack Query）
- 主线项编号：A-2（出自 2026-05-09 全面体检报告）

## 1. 背景

体检发现前端列表/表单页面存在**机械重复**：

| 重复模式 | 现状 | 证据 |
|---|---|---|
| `n-data-table` 列定义 | 5 个页面各自手写 columns 数组，状态列/时间列/操作列模板高度一致 | AppsPage.vue:71-92、MembersPage.vue:154-177、OrganizationsPage.vue:117-144、RuntimeNodesPage.vue:151-183、AuditLogsPage.vue:55-79 |
| `tone → NTag.type` 映射 | 5 处复制：MembersPage.vue:147-152、OrganizationsPage.vue:110-115、AppStatusTag.vue:12-17、RuntimeStatusTag.vue:12-17、JobProgressPanel.vue（未对齐） | — |
| 表单 modal 三件套 | `formVisible / creating / submitError` ref 在 3 个页面机械同构（重置字段、try-catch-finally、关闭 modal） | MembersPage.vue:135-192、OrganizationsPage.vue:102-167、RuntimeNodesPage.vue:143-202 |
| Toolbar 标题块 | DataTableToolbar.vue 已存在但仅 1 处使用；多数页面手写 h2 + actions | DataTableToolbar.vue:1-37 |

页面特有逻辑（不应被抽进通用层）已分别识别：
- AppsPage：操作列 3 按钮（重启/停止/删除），跳转链接列；
- MembersPage：角色下拉、重置密码、启用/禁用条件按钮；
- OrganizationsPage：可选字段 `|| undefined` 过滤；
- RuntimeNodesPage：max_apps 内联编辑、创建后 `showToken(...)` 副作用；
- AuditLogsPage：result 列 4 种状态自定义 `auditTagType`、纯只读无表单。

## 2. 目标

- 抽出 4 个新公共构件：`<DataTableList>`、`<StatusBadge>`、column factories（`statusColumn / timeColumn / linkColumn / actionColumn`）、`useFormModal`。
- 5 个页面（Apps / Audit / Members / Organizations / RuntimeNodes）改造为消费方，删除本地 `toneToTagType` 等重复代码。
- 现有 `AppStatusTag.vue` / `RuntimeStatusTag.vue` 改为转发到 `<StatusBadge>`，保持对外 API 兼容（不动调用方）。

## 3. 非目标（避免范围蔓延）

- **不**支持表单的「编辑模式」（YAGNI；现有 modal 都是创建专用，未来真要时再扩展 `useFormModal` API）。
- **不**抽列的 sort / filter / pagination —— 5 个页面都禁用了这些行为，等真有需求再加。
- **不**调整 `domain/status.ts` 现有 5 个 formatter 函数的签名或返回类型。
- **不**做 page-level 单元测试补齐（属于「前端清理合集」B+C spec 的内容）。
- **不**改 `JobProgressPanel.vue` 的内置 tone 映射（属另一种业务场景，本次不一并收编）。
- **不**动 `web/src/api/hooks/*` 与 Pinia store。

## 4. 设计

### 4.1 关键决策（已与决策方对齐）

| 决策点 | 选择 | 理由 |
|---|---|---|
| DataTableList 形态 | **中庸方案** —— 组件包 toolbar+n-data-table+loading/empty；列定义留给页面，由 column factory 装配 | 组件强约束 + 列工厂自由组合，页面可混用 factory 列与自写列 |
| useFormModal 范围 | **状态聚合 + submit 包装** | 包到 mutateAsync + 错误捕获 + 关 modal；页面只写 onSuccess 业务后置 |
| tone 映射归位 | **`<StatusBadge>` 组件** | 比工具函数减少 `h(NTag, ...)` 样板；AppStatusTag/RuntimeStatusTag 改为转发 |
| 编辑模式 | **不加（YAGNI）** | 当前所有 modal 都是创建专用 |

### 4.2 文件结构（新增）

```
web/src/
├── components/
│   ├── StatusBadge.vue          ← 新增：tone → NTag.type 唯一定义点
│   ├── DataTableList.vue        ← 新增：toolbar + n-data-table 包装
│   └── columns/
│       ├── index.ts             ← 新增：re-export
│       ├── statusColumn.ts      ← 新增
│       ├── timeColumn.ts        ← 新增
│       ├── linkColumn.ts        ← 新增
│       └── actionColumn.ts      ← 新增（含 RowAction 类型）
├── composables/
│   └── useFormModal.ts          ← 新增
└── components/__tests__/
    ├── StatusBadge.spec.ts      ← 新增
    ├── DataTableList.spec.ts    ← 新增
    ├── columns/
    │   ├── statusColumn.spec.ts ← 新增
    │   ├── timeColumn.spec.ts   ← 新增
    │   ├── linkColumn.spec.ts   ← 新增
    │   └── actionColumn.spec.ts ← 新增
    └── useFormModal.spec.ts     ← 新增
```

### 4.3 文件结构（修改）

```
web/src/
├── components/
│   ├── AppStatusTag.vue         ← 改：内部转发到 <StatusBadge>
│   ├── RuntimeStatusTag.vue     ← 改：内部转发到 <StatusBadge>
│   └── DataTableToolbar.vue     ← 删除（被 DataTableList 吸收，仅 1 处使用）
└── pages/
    ├── apps/AppsPage.vue                  ← 改
    ├── audit/AuditLogsPage.vue            ← 改
    ├── org/MembersPage.vue                ← 改（含删本地 toneToTagType）
    ├── platform/OrganizationsPage.vue     ← 改（含删本地 toneToTagType）
    └── runtime-nodes/RuntimeNodesPage.vue ← 改
```

### 4.4 API：StatusBadge

`web/src/components/StatusBadge.vue`：

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

调用方式：
```vue
<StatusBadge :view="formatMemberStatus(row.status)" />
```

`AppStatusTag.vue` 与 `RuntimeStatusTag.vue` 改为：

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

### 4.5 API：DataTableList

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
      <n-alert v-if="errorMessage" type="error" :show-icon="false" style="margin-bottom: 12px">
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
```

调用方式：
```vue
<DataTableList
  :title="'成员列表'"
  :eyebrow="'Platform · 组织成员'"
  :columns="columns"
  :data="members ?? []"
  :loading="isLoading"
  :error-message="errorMessage"
  :row-key="row => row.id"
>
  <template #toolbar>
    <n-button type="primary" @click="openForm">新建成员</n-button>
  </template>
</DataTableList>
```

### 4.6 API：column factories

`web/src/components/columns/statusColumn.ts`：

```ts
import { h } from 'vue'
import type { DataTableColumn } from 'naive-ui'
import StatusBadge from '@/components/StatusBadge.vue'
import type { StatusView } from '@/domain/status'

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

export function linkColumn<T>(opts: {
  title: string
  key?: string
  text: (row: T) => string
  onClick: (row: T) => void
  subtitle?: (row: T) => string | null | undefined
}): DataTableColumn<T> {
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

样式 `data-table-link` / `data-table-subtitle` 加在 `web/src/styles/base.css` 全局；提供链接颜色与小字辅助行（替代当前页面里硬编码的 `style="color:#00F0FF"`、`color:#8A94C6;font-size:12px`）。

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

export function actionColumn<T>(
  actions: RowAction<T>[],
  options: { title?: string; key?: string } = {},
): DataTableColumn<T> {
  return {
    title: options.title ?? '操作',
    key: options.key ?? 'actions',
    render: (row) => h(NSpace, { size: 'small' }, {
      default: () => actions
        .filter(a => !a.hidden?.(row))
        .map(a => h(NButton, {
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
export { linkColumn } from './linkColumn'
export { actionColumn, type RowAction } from './actionColumn'
```

### 4.7 API：useFormModal

`web/src/composables/useFormModal.ts`：

```ts
import { reactive, ref, type Ref } from 'vue'
import type { UseMutationReturnType } from '@tanstack/vue-query'

export interface UseFormModalOptions<TPayload, TResult> {
  /** 表单初始值；openForm 每次都会 deep clone 此对象重置 form */
  initial: TPayload
  /** TanStack Query mutation；submit 调用其 mutateAsync */
  mutation: UseMutationReturnType<TResult, Error, TPayload, unknown>
  /** 提交成功后的业务后置（如展示 token、跳转）；不在此 hook 内做 modal 关闭，关闭已自动处理 */
  onSuccess?: (result: TResult) => void
  /** 自定义错误消息生成 */
  errorMessage?: (err: unknown) => string
  /** 提交前对 form 做适配（如 || undefined 过滤）；返回值会替代 form 作为 payload */
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

调用示例（OrganizationsPage 风格，含 `|| undefined` 过滤）：

```ts
const createMutation = useCreateOrganization()
const initial: OrganizationFormPayload = {
  name: '', contact_name: '', contact_phone: '', remark: '',
  credit_warning_threshold: undefined,
}
const { form, formVisible, creating, submitError, openForm, submit } = useFormModal({
  initial,
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

RuntimeNodesPage 的 `showToken(created)` 副作用通过 `onSuccess` 注入：
```ts
useFormModal({ initial, mutation: createMutation, onSuccess: (created) => showToken(created) })
```

## 5. 改造范围逐页对照

每个页面写明「保留」与「替换」，避免 implementer 误删特有逻辑。

### 5.1 AppsPage.vue

- 替换：手写 columns 数组 → `linkColumn('名称', ...) + statusColumn('状态', formatAppStatus) + textColumn(api_key) + 自写容器列 + actionColumn([restartAction, stopAction, deleteAction])`
- 替换：page 外层手写 `<n-card>` → `<DataTableList>` 包裹
- 保留：`AppStatusTag` 导入处不动（顺手用 StatusBadge 也行，但是已有调用方式不变更省事；本次直接用 statusColumn 即可，不再 import AppStatusTag）

### 5.2 AuditLogsPage.vue

- 替换：手写 columns → `timeColumn('时间', r => r.created_at)` 处理时间列；其他列保持 naive-ui 原生 `{ title, key, render? }` 形式（actor / target / action / result 列都属页面特有展示，不抽 factory）
- 替换：外层包装 → `<DataTableList>`
- 保留：`auditTagType` 函数（结果列特有 4-状态映射，不与三层角色 tone 共用）

### 5.3 MembersPage.vue

- 替换：手写 columns → 字符串列保留 naive-ui 原生写法（`{ title: '用户名', key: 'username' }` 等）；状态列改用 `statusColumn('状态', r => formatMemberStatus(r.status))`；操作列改用 `actionColumn([...])`；角色列因仅做 `formatMemberRole(row.role)` 文本转换，保留页面内 render 即可
- 替换：手写 form modal 状态 → `useFormModal({ initial, mutation: createMutation })`，模板 `<n-modal>`/`<n-form>` 由页面继续控制（字段不抽，组合式只管状态/submit）
- **删除**：本地 `toneToTagType` 函数定义
- 保留：重置密码按钮和 memberToDelete 二次确认（独立交互，不归 useFormModal）

### 5.4 OrganizationsPage.vue

- 与 5.3 相同模式
- **删除**：本地 `toneToTagType` 函数定义
- 通过 `toPayload` 注入 `|| undefined` 过滤逻辑

### 5.5 RuntimeNodesPage.vue

- 与 5.3 相同模式
- 通过 `onSuccess: (created) => showToken(created)` 注入业务后置
- 保留：max_apps 列内联编辑按钮（页面特有，不归 actionColumn）；保留 editingNode 与 token 展示卡片

## 6. 迁移步骤

每步独立可回滚，每步一个 commit。**Task 1-3 是新建公共构件并测试通过；Task 4 改造 status tag；Task 5-9 是逐页改造，每页一次 commit**。

| Step | 内容 | 完成判据 |
|---|---|---|
| 1 | 新建 `StatusBadge.vue` + `StatusBadge.spec.ts`（覆盖 4 种 tone 映射） | `npm run web-test -- StatusBadge` 全绿 |
| 2 | 新建 4 个 column factory 文件 + 各自 spec（每个 factory 至少 3 个用例覆盖：基本渲染、可选字段、edge case） | `npm run web-test -- columns/` 全绿；`npm run web-typecheck` 无新增错 |
| 3 | 新建 `DataTableList.vue` + spec（覆盖 toolbar slot / loading / errorMessage / 空数据） | 同 step 1 |
| 4 | 新建 `useFormModal.ts` + spec（覆盖 openForm 重置、submit 成功/失败、onSuccess 回调、toPayload 转换） | `npm run web-test -- useFormModal` 全绿 |
| 5 | 重构 `AppStatusTag.vue` 与 `RuntimeStatusTag.vue` 为 StatusBadge 转发；保持 props 签名不变 | 现有页面调用点不动；`npm run web-typecheck` 与 `web-test` 全绿 |
| 6 | 重构 AuditLogsPage（最简：仅表格，无表单） | 页面渲染与改造前等价；`web-typecheck` 全绿 |
| 7 | 重构 AppsPage | 同上 |
| 8 | 重构 MembersPage（表格 + useFormModal + 删本地 toneToTagType） | 同上；删除 `toneToTagType` 函数定义；保留 reset 密码与删除二次确认 |
| 9 | 重构 OrganizationsPage（同 8） | 同 8 |
| 10 | 重构 RuntimeNodesPage（含 `onSuccess: showToken` 注入；保留 max_apps 内联编辑） | 同 8 |
| 11 | 删除 `web/src/components/DataTableToolbar.vue`（已被 DataTableList 吸收；先 grep 确认无引用） | `git grep -n 'DataTableToolbar' web/src/` 输出为空 |
| 12 | 全量验收：`npm run web-typecheck && npm run web-test && npm run web-build` | 三条命令均通过 |

## 7. 测试策略

新公共构件按以下覆盖（vitest + @vue/test-utils）：

### StatusBadge.spec.ts
- 4 种 tone 各 1 个用例 → 渲染对应 NTag type
- 未识别 tone fallback 'default'

### column factories
- `statusColumn`: render 调用形如 `formatXxx(row.status)` 并传给 StatusBadge（mock formatter 验证传参）
- `timeColumn`: 有/无值 / null / undefined 三种渲染（含 placeholder）
- `linkColumn`: onClick 触发 / subtitle 有无两种渲染
- `actionColumn`: hidden / disabled / 多按钮顺序；点击触发 onClick

### DataTableList.spec.ts
- toolbar slot 渲染
- loading 透传到 n-data-table
- errorMessage 显示 / 不显示
- 空 data 时 n-data-table 默认空态显示

### useFormModal.spec.ts
- `openForm` 重置 form 字段（mutation 后再次 openForm，字段恢复 initial）
- `submit` 成功路径：mutateAsync resolves → formVisible=false / onSuccess 调用
- `submit` 失败路径：mutateAsync rejects → submitError 写入 / formVisible 不变
- `toPayload` 转换：mutation 收到的是 toPayload 输出，不是 form 原值

### Page 改造（不在本 spec 范围内补单测）
仅靠现有 `web-typecheck` 与人工冒烟（`npm run dev` 浏览器看 5 个页面）保底。页面级单测属于「前端清理合集」的工作。

## 8. 风险与缓解

| 风险 | 严重度 | 缓解 |
|---|---|---|
| `<DataTableList>` 用 `generic="T"` 后 vue-tsc 推断不准导致 columns 类型 narrow 失败 | 中 | step 3 的 spec 用真实业务类型（如 Member）做泛型测试；step 6/7/8/9/10 改造时若类型推断失败，实现里加显式 `as DataTableColumn<Member>[]` 断言 |
| 删除 `DataTableToolbar.vue` 时漏 grep 引用，line 11 即崩溃 | 中 | step 11 强制 grep 验证 |
| `<StatusBadge :view>` 与 `<AppStatusTag :status>` 调用方式不一致，迁移时混用 | 低 | step 5 保持 `AppStatusTag` 对外 API（仍接受 `:status`），内部转发；调用方文件不动 |
| `useFormModal` 的 `form = reactive(structuredClone(initial))` 对深嵌套对象（含 Date / Map）失败 | 低 | 当前 5 个页面的 form payload 都是 plain primitive 字段，不会触发；spec 中明确说"initial 必须是 JSON-serializable" |
| 改造页面时漏掉某个特有逻辑（如 RuntimeNodesPage 的 max_apps 内联编辑） | 中 | 5.5 节逐页对照表显式列出「保留」项，code review 时逐项对照 |
| Naive UI `<n-modal>` + `<n-form>` 在 useFormModal 抽象后丢失 v-model 绑定 | 低 | spec 明确：useFormModal 不抽 modal 模板，仅管状态；模板 `<n-form :model="form">` 仍由页面写 |

## 9. 完成定义（DoD）

- [ ] `web/src/components/StatusBadge.vue` 与 `DataTableList.vue` 与 `composables/useFormModal.ts` 与 4 个 column factory 文件全部创建
- [ ] 7 个 spec 文件全部存在且 `npm run web-test` 全绿
- [ ] `git grep -n 'toneToTagType' web/src/` 仅命中 `StatusBadge.vue` 内部 `TONE_TO_TAG_TYPE` 常量（不再有页面级函数定义）
- [ ] `git grep -n 'DataTableToolbar' web/src/` 输出为空
- [ ] `npm run web-typecheck` 无新增错误
- [ ] `npm run web-build` 成功
- [ ] 5 个改造后的页面（Apps / Audit / Members / Organizations / RuntimeNodes）人工冒烟通过：列表正常渲染、过滤/排序行为与改造前一致、modal 创建流程正常（含成功/失败路径与特殊 onSuccess）

## 10. 后续

- 本 spec 落地后进入 writing-plans 出更细的 task 拆分。
- 「页面级单测补齐」「`JobProgressPanel` 内置 tone 映射收编」「样式 token 化（CSS 变量替换硬编码 #00F0FF / #8A94C6 等）」归到「前端清理合集」spec。
