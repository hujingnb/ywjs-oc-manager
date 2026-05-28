# 任务看板状态汉化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将任务看板页面的 Kanban 状态统一展示为中文，并保留未知状态的可诊断降级文案。

**Architecture:** 前端继续使用后端返回的状态原值作为数据契约，只在展示层集中格式化。`web/src/domain/status.ts` 负责状态到 `StatusView` 的映射，任务列表和详情组件只消费格式化结果，避免组件内重复维护状态字典。

**Tech Stack:** Vue 3 `<script setup>`、TypeScript、Vitest、Vue Test Utils、Naive UI。

---

## File Structure

- Modify: `web/src/domain/status.ts`
  - 新增 `formatKanbanStatus(status)`。
  - 按 `running/ready/todo/blocked/triage/done/archived` 返回中文标签和视觉语义。
  - 未知状态返回 `未知状态：<原始状态>` 和 `warning`。

- Modify: `web/src/domain/status.test.ts`
  - 补充 Kanban 状态映射测试。
  - 覆盖全部已知状态和未知状态降级。

- Modify: `web/src/pages/apps/kanban/KanbanTaskList.vue`
  - 状态分组顺序保持不变。
  - 分组标题改为 `formatKanbanStatus(status).label`。

- Modify: `web/src/pages/apps/kanban/KanbanTaskDetail.vue`
  - 顶部状态条改为中文状态。
  - 历次执行状态列复用 `formatKanbanStatus`。

---

### Task 1: 增加 Kanban 状态格式化契约

**Files:**
- Modify: `web/src/domain/status.test.ts`
- Modify: `web/src/domain/status.ts`

- [ ] **Step 1: Write failing tests for Kanban status labels**

In `web/src/domain/status.test.ts`, add `formatKanbanStatus` to the import list:

```ts
import {
  formatAppStatus,
  formatKanbanStatus,
  formatMemberRole,
  formatMemberStatus,
  formatOrgStatus,
  formatRuntimeNodeStatus,
} from './status'
```

Then append this test block after the `formatAppStatus` tests:

```ts
describe('formatKanbanStatus', () => {
  // 覆盖任务看板全部已知状态：页面应展示中文文案，而不是 Hermes 原始英文状态值。
  it.each([
    ['running', { label: '运行中', tone: 'warning' }], // running：任务正在执行，仍属于过程态。
    ['ready', { label: '就绪', tone: 'warning' }], // ready：任务已准备执行，等待调度。
    ['todo', { label: '待办', tone: 'neutral' }], // todo：任务待处理。
    ['blocked', { label: '阻塞', tone: 'danger' }], // blocked：任务被阻塞，需要人工处理。
    ['triage', { label: '待分诊', tone: 'neutral' }], // triage：任务等待分类或确认。
    ['done', { label: '已完成', tone: 'success' }], // done：任务已完成。
    ['archived', { label: '已归档', tone: 'neutral' }], // archived：任务已归档。
  ] as const)('maps %s to Chinese label', (status, expected) => {
    expect(formatKanbanStatus(status)).toEqual(expected)
  })

  // 覆盖未知状态降级：Hermes 新增状态时仍显示原值，便于定位前端映射未同步。
  it('falls back for unknown Kanban statuses', () => {
    expect(formatKanbanStatus('paused_by_policy')).toEqual({
      label: '未知状态：paused_by_policy',
      tone: 'warning',
    })
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd web && npm test -- --run src/domain/status.test.ts
```

Expected: FAIL because `formatKanbanStatus` is not exported from `web/src/domain/status.ts`.

- [ ] **Step 3: Implement `formatKanbanStatus`**

In `web/src/domain/status.ts`, add this block after `formatAppStatus`:

```ts
// kanbanStatusViews 覆盖 Hermes Kanban 任务状态。
// 状态原值仍作为前后端契约保留，页面展示统一通过 formatKanbanStatus 汉化。
const kanbanStatusViews: Record<string, StatusView> = {
  running: { label: '运行中', tone: 'warning' },
  ready: { label: '就绪', tone: 'warning' },
  todo: { label: '待办', tone: 'neutral' },
  blocked: { label: '阻塞', tone: 'danger' },
  triage: { label: '待分诊', tone: 'neutral' },
  done: { label: '已完成', tone: 'success' },
  archived: { label: '已归档', tone: 'neutral' },
}

// formatKanbanStatus 将任务看板状态映射为中文文案和视觉语义。
// 未知状态保留原值，便于 Hermes 灰度新增状态时从页面直接发现映射缺口。
export function formatKanbanStatus(status: string): StatusView {
  return kanbanStatusViews[status] ?? { label: `未知状态：${status}`, tone: 'warning' }
}
```

- [ ] **Step 4: Run status tests to verify formatter passes**

Run:

```bash
cd web && npm test -- --run src/domain/status.test.ts
```

Expected: PASS for `formatAppStatus`, `formatKanbanStatus`, org/member/runtime status tests.

---

### Task 2: 接入任务看板页面状态展示

**Files:**
- Modify: `web/src/pages/apps/kanban/KanbanTaskList.vue`
- Modify: `web/src/pages/apps/kanban/KanbanTaskDetail.vue`

- [ ] **Step 1: Localize group titles in `KanbanTaskList.vue`**

In `web/src/pages/apps/kanban/KanbanTaskList.vue`, add the import:

```ts
import { formatKanbanStatus } from '@/domain/status'
```

Replace `GROUP_DEFS` with:

```ts
// 状态分组顺序与看板状态流转保持一致；label 统一由状态格式化函数生成。
const GROUP_DEFS: ReadonlyArray<{ status: KanbanStatus; label: string }> = [
  'running',
  'ready',
  'todo',
  'blocked',
  'triage',
  'done',
  'archived',
].map((status) => ({
  status,
  label: formatKanbanStatus(status).label,
}))
```

- [ ] **Step 2: Localize detail status and run history in `KanbanTaskDetail.vue`**

In `web/src/pages/apps/kanban/KanbanTaskDetail.vue`, add the import:

```ts
import { computed } from 'vue'
import { formatKanbanStatus } from '@/domain/status'
```

If the file already has no Vue import, add the `computed` import before other local imports.

Replace the status bar template line with:

```vue
        <div class="status-bar">● {{ taskStatusLabel }}</div>
```

Replace the run status table cell with:

```vue
              <td>{{ run.status ? formatKanbanStatus(run.status).label : '—' }}</td>
```

Change the `defineProps` call into a `props` constant:

```ts
const props = defineProps<{
  // detail 为 null 时显示引导文案「从左侧选择任务」。
  // 真实结构：{ task, comments, events, parents, children, latest_summary }
  detail: KanbanTaskDetail | null
  // board 当前 board slug，显示在 task_id 元信息行。
  board: string
  // runs 由父组件通过 useKanbanRunsQuery 注入。
  runs: KanbanTaskRun[]
  // liveEvents 是当前任务的实时事件文本行，由父组件从 SSE 流注入。
  liveEvents: string[]
  // appId 透传给 KanbanTaskActions，用于查询 oc-kanban capabilities 降级 UI。
  appId?: string
}>()
```

Add this computed value below `defineEmits`:

```ts
// taskStatusLabel 统一汉化任务状态；状态缺失时仍显示 unknown 的降级文案。
const taskStatusLabel = computed(() => formatKanbanStatus(props.detail?.task?.status ?? 'unknown').label)
```

- [ ] **Step 3: Run targeted frontend tests**

Run:

```bash
cd web && npm test -- --run src/domain/status.test.ts src/pages/apps/AppKanbanTab.spec.ts
```

Expected: PASS for status mapping and existing Kanban tab rendering tests.

- [ ] **Step 4: Run type check**

Run:

```bash
cd web && npm run typecheck
```

Expected: PASS with no TypeScript errors from the new imports, computed value, or formatter calls.

- [ ] **Step 5: Inspect diff and avoid unrelated files**

Run:

```bash
git diff -- web/src/domain/status.ts web/src/domain/status.test.ts web/src/pages/apps/kanban/KanbanTaskList.vue web/src/pages/apps/kanban/KanbanTaskDetail.vue
git status --short
```

Expected: The implementation diff only touches the four listed source/test files. Existing unrelated worktree entries such as `scripts/check-compose-bind-mounts.sh` and `docs/reports/` remain untouched.

- [ ] **Step 6: Commit implementation**

Run:

```bash
git add web/src/domain/status.ts web/src/domain/status.test.ts web/src/pages/apps/kanban/KanbanTaskList.vue web/src/pages/apps/kanban/KanbanTaskDetail.vue docs/superpowers/plans/2026-05-28-kanban-status-localization.md
git commit -m "feat(web): 汉化任务看板状态" -m "集中维护 Kanban 状态展示文案，并在任务列表、详情状态条和执行历史中复用。\\n\\n补充状态格式化测试，覆盖已知状态和未知状态降级。"
```

Expected: Commit succeeds and includes only the implementation files plus this plan document.
