# App Organization Name Display Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 应用详情页不再把业务用户无意义的应用和组织 UUID 作为主展示内容，并在概览中显示组织名称。

**Architecture:** 保持后端接口不变，前端继续使用 `app.org_id` 做权限和 API 上下文，只在展示层通过现有 `useOrganizationQuery` 获取组织名称。应用详情父页负责标题展示，概览 tab 负责组织名称展示和兜底文案。

**Tech Stack:** Vue 3, TypeScript, Naive UI, TanStack Vue Query, Vitest, Vue Test Utils.

---

## File Structure

- Modify: `web/src/pages/apps/AppDetailPage.vue`
  - 移除标题旁直接显示的 `app.id`，保留应用名称和状态。
- Modify: `web/src/pages/apps/AppOverviewTab.vue`
  - 引入 `useOrganizationQuery`，按 `app.org_id` 查询组织详情。
  - “所属组织”显示组织名称；名称不可用时显示 `未知组织`。
- Create: `web/src/pages/apps/AppDetailPage.spec.ts`
  - 覆盖应用详情标题不再展示应用 ID。
- Create: `web/src/pages/apps/AppOverviewTab.spec.ts`
  - 覆盖所属组织显示组织名称。
  - 覆盖组织名称缺失时显示兜底文案。

---

### Task 1: Lock Detail Header Behavior

**Files:**
- Create: `web/src/pages/apps/AppDetailPage.spec.ts`
- Modify: `web/src/pages/apps/AppDetailPage.vue`

- [ ] **Step 1: Write the failing test**

Create `web/src/pages/apps/AppDetailPage.spec.ts`:

```ts
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import AppDetailPage from './AppDetailPage.vue'

// 应用详情标题只展示业务可读名称，不把应用 UUID 作为主视觉信息展示给用户。
vi.mock('@/api/hooks/useApps', () => ({
  useAppQuery: () => ({
    data: ref({
      id: '00000000-0000-0000-0000-000000000001',
      org_id: '00000000-0000-0000-0000-000000000101',
      owner_user_id: '00000000-0000-0000-0000-000000000201',
      name: '测试应用',
      status: 'running',
      persona_mode: 'org_inherited',
      api_key_status: 'active',
    }),
    isLoading: ref(false),
    error: ref(null),
  }),
}))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => ({ params: { appId: '00000000-0000-0000-0000-000000000001' }, path: '/apps/00000000-0000-0000-0000-000000000001/overview' }),
    useRouter: () => ({ push: vi.fn() }),
    RouterView: { template: '<section />' },
  }
})

describe('AppDetailPage', () => {
  it('标题展示应用名称且不展示应用 UUID', () => {
    const wrapper = mount(AppDetailPage, {
      global: {
        stubs: {
          AppStatusTag: { template: '<span />' },
          NCard: { template: '<section><slot name="header" /><slot /></section>' },
          NTabs: { template: '<nav><slot /></nav>' },
          NTabPane: true,
        },
      },
    })

    expect(wrapper.text()).toContain('测试应用')
    expect(wrapper.text()).not.toContain('00000000-0000-0000-0000-000000000001')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
rtk npm --prefix web test -- AppDetailPage.spec.ts --run
```

Expected: FAIL because `AppDetailPage.vue` still renders `app.id` in the `<small>` element next to the title.

- [ ] **Step 3: Remove the title UUID display**

In `web/src/pages/apps/AppDetailPage.vue`, replace this block:

```vue
<h2 style="margin: 0">
  {{ app?.name ?? '应用详情' }}
  <small v-if="app" style="color: #8A94C6; font-size: 14px; font-weight: 400; margin-left: 8px">{{ app.id }}</small>
</h2>
```

with:

```vue
<h2 style="margin: 0">{{ app?.name ?? '应用详情' }}</h2>
```

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
rtk npm --prefix web test -- AppDetailPage.spec.ts --run
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
rtk git add web/src/pages/apps/AppDetailPage.vue web/src/pages/apps/AppDetailPage.spec.ts
rtk git commit -m "fix(app): 隐藏应用详情标题中的应用标识"
```

Expected: commit succeeds with only these two files staged.

---

### Task 2: Display Organization Name In App Overview

**Files:**
- Create: `web/src/pages/apps/AppOverviewTab.spec.ts`
- Modify: `web/src/pages/apps/AppOverviewTab.vue`

- [ ] **Step 1: Write the failing tests**

Create `web/src/pages/apps/AppOverviewTab.spec.ts`:

```ts
import { mount } from '@vue/test-utils'
import { computed, ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import AppOverviewTab from './AppOverviewTab.vue'

const organizationName = ref<string | undefined>('测试组织')

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: {
      id: '00000000-0000-0000-0000-000000000201',
      org_id: '00000000-0000-0000-0000-000000000101',
      role: 'org_admin',
    },
  }),
}))

vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationQuery: () => ({
    data: computed(() => organizationName.value
      ? { id: '00000000-0000-0000-0000-000000000101', name: organizationName.value, status: 'active' }
      : null),
    isLoading: ref(false),
    error: ref(null),
  }),
}))

vi.mock('@/api/hooks/useApps', () => ({
  useInitializeAppMutation: () => ({
    isPending: ref(false),
    mutateAsync: vi.fn(),
  }),
  useJobQuery: () => ({
    data: ref(null),
  }),
  useToggleAppAPIKey: () => ({
    isPending: ref(false),
    mutateAsync: vi.fn(),
  }),
}))

const appRef = ref({
  id: '00000000-0000-0000-0000-000000000001',
  org_id: '00000000-0000-0000-0000-000000000101',
  owner_user_id: '00000000-0000-0000-0000-000000000201',
  name: '测试应用',
  status: 'running',
  persona_mode: 'org_inherited',
  api_key_status: 'active',
})

function mountOverview() {
  return mount(AppOverviewTab, {
    props: { appId: '00000000-0000-0000-0000-000000000001' },
    global: {
      provide: { app: appRef },
      stubs: {
        AppStatusTag: { template: '<span />' },
        ConfirmActionModal: true,
        JobProgressPanel: true,
        NButton: { template: '<button><slot /></button>' },
        NCard: { template: '<section><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        NDescriptions: { template: '<dl><slot /></dl>' },
        NDescriptionsItem: { props: ['label'], template: '<div><dt>{{ label }}</dt><dd><slot /></dd></div>' },
        NSpace: { template: '<span><slot /></span>' },
        NTag: { template: '<span><slot /></span>' },
      },
    },
  })
}

describe('AppOverviewTab', () => {
  it('所属组织展示组织名称而不是组织 UUID', () => {
    organizationName.value = '测试组织'

    const wrapper = mountOverview()

    expect(wrapper.text()).toContain('测试组织')
    expect(wrapper.text()).not.toContain('00000000-0000-0000-0000-000000000101')
  })

  it('组织名称缺失时展示友好兜底文案', () => {
    organizationName.value = undefined

    const wrapper = mountOverview()

    expect(wrapper.text()).toContain('未知组织')
    expect(wrapper.text()).not.toContain('00000000-0000-0000-0000-000000000101')
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
rtk npm --prefix web test -- AppOverviewTab.spec.ts --run
```

Expected: FAIL because `AppOverviewTab.vue` still renders `app.org_id`, so the first test sees the UUID and the organization name is not rendered.

- [ ] **Step 3: Add organization display query and computed name**

In `web/src/pages/apps/AppOverviewTab.vue`, update the import block from:

```ts
import {
  useInitializeAppMutation,
  useJobQuery,
  useToggleAppAPIKey,
  type AppDTO,
} from '@/api/hooks/useApps'
```

to:

```ts
import {
  useInitializeAppMutation,
  useJobQuery,
  useToggleAppAPIKey,
  type AppDTO,
} from '@/api/hooks/useApps'
import { useOrganizationQuery } from '@/api/hooks/useOrganizations'
```

After:

```ts
const app = inject<Ref<AppDTO | null>>('app')
const auth = useAuthStore()
```

add:

```ts
// orgId 只用于展示组织名称；权限和业务 API 仍继续使用 app.org_id。
const orgId = computed<string | undefined>(() => app?.value?.org_id)
const organizationQuery = useOrganizationQuery(orgId)
const organizationName = computed(() => organizationQuery.data.value?.name || '未知组织')
```

- [ ] **Step 4: Render organization name instead of organization UUID**

In `web/src/pages/apps/AppOverviewTab.vue`, replace:

```vue
<n-descriptions-item label="所属组织">
  <code>{{ app.org_id }}</code>
</n-descriptions-item>
```

with:

```vue
<n-descriptions-item label="所属组织">
  {{ organizationName }}
</n-descriptions-item>
```

- [ ] **Step 5: Run overview tests to verify they pass**

Run:

```bash
rtk npm --prefix web test -- AppOverviewTab.spec.ts --run
```

Expected: PASS.

- [ ] **Step 6: Run both app detail tests**

Run:

```bash
rtk npm --prefix web test -- AppDetailPage.spec.ts AppOverviewTab.spec.ts --run
```

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```bash
rtk git add web/src/pages/apps/AppOverviewTab.vue web/src/pages/apps/AppOverviewTab.spec.ts
rtk git commit -m "fix(app): 应用概览展示组织名称"
```

Expected: commit succeeds with only these two files staged.

---

### Task 3: Final Verification

**Files:**
- Verify only; no new file changes expected.

- [ ] **Step 1: Run related frontend tests**

Run:

```bash
rtk npm --prefix web test -- AppsPage.spec.ts AppDetailPage.spec.ts AppOverviewTab.spec.ts --run
```

Expected: PASS.

- [ ] **Step 2: Run frontend typecheck**

Run:

```bash
rtk npm --prefix web run typecheck
```

Expected: PASS.

- [ ] **Step 3: Confirm no OpenAPI regeneration is needed**

Run:

```bash
rtk git status --short
```

Expected: no changes to `openapi/openapi.yaml` or `web/src/api/generated.ts`, because the implementation is frontend-only and does not change handler signatures or response schemas.

- [ ] **Step 4: Inspect final diff**

Run:

```bash
rtk git show --stat --oneline --decorate -2
```

Expected: the latest implementation commits touch only:

- `web/src/pages/apps/AppDetailPage.vue`
- `web/src/pages/apps/AppDetailPage.spec.ts`
- `web/src/pages/apps/AppOverviewTab.vue`
- `web/src/pages/apps/AppOverviewTab.spec.ts`
