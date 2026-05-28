# 组织成员实例菜单拉平 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `org_member` 的唯一实例能力入口拉平到左侧菜单，并统一组织级知识库文案为「企业知识库」。

**Architecture:** 前端继续复用现有 `/apps/:appId/...` 路由和实例 tab 页面，只在菜单生成、首页重定向和详情页 tab 展示层做角色化处理。`DashboardLayout.vue` 负责成员菜单与高亮，`RoleAwareHome.vue` 负责成员默认落点，`AppDetailPage.vue` 负责成员隐藏重复 tab。

**Tech Stack:** Vue 3 `<script setup>`、Vue Router 4、Pinia、TanStack Vue Query、Naive UI、Vitest、Vue Test Utils、lucide-vue-next。

---

## File Structure

- Modify: `web/src/layouts/DashboardLayout.vue`
  - 生成 `org_member` 专属左侧菜单。
  - 将组织级「知识库」文案改为「企业知识库」。
  - 按 `/apps/:appId/<tab>` 末段计算成员菜单高亮。

- Modify: `web/src/layouts/DashboardLayout.spec.ts`
  - 用可变 mock 覆盖平台管理员、组织管理员、组织成员三种菜单状态。
  - 通过 `NMenu` stub 断言菜单 options、active value 和导航 key。

- Modify: `web/src/pages/apps/AppDetailPage.vue`
  - 对 `org_member` 隐藏详情页顶部 tab。
  - 管理员仍保留实例详情 tab。

- Modify: `web/src/pages/apps/AppDetailPage.spec.ts`
  - 覆盖成员隐藏 tab、非成员显示 tab。

- Modify: `web/src/pages/dashboard/RoleAwareHome.vue`
  - `org_member` 访问 `/` 时跳到唯一实例 overview。
  - 成员无实例时跳到 `/apps/empty`。
  - 组织管理员首页卡片使用「企业知识库」。

- Create: `web/src/pages/dashboard/RoleAwareHome.spec.ts`
  - 覆盖成员首页重定向、无实例空状态、组织管理员卡片文案。

---

### Task 1: DashboardLayout 成员菜单与高亮

**Files:**
- Modify: `web/src/layouts/DashboardLayout.spec.ts`
- Modify: `web/src/layouts/DashboardLayout.vue`

- [ ] **Step 1: Write failing tests for flattened member menu**

Replace `web/src/layouts/DashboardLayout.spec.ts` with this test file:

```ts
import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { NLayoutContent } from 'naive-ui'

import DashboardLayout from './DashboardLayout.vue'

const routerPush = vi.hoisted(() => vi.fn())
const routerReplace = vi.hoisted(() => vi.fn())
const routeState = vi.hoisted(() => ({ path: '/runtime-nodes' }))
const logout = vi.hoisted(() => vi.fn())
const authState = vi.hoisted(() => ({
  user: { id: 'admin-1', username: 'admin', display_name: 'admin', role: 'platform_admin', org_id: 'org-1' },
  isPlatformAdmin: true,
  isOrgAdmin: false,
  isOrgMember: false,
  logout,
}))
const memberAppState = vi.hoisted(() => ({
  appId: { value: undefined as string | undefined },
  hasApp: { value: false },
  isLoading: { value: false },
}))

vi.mock('vue-router', () => ({
  RouterView: { template: '<section class="route-page">页面内容</section>' },
  useRoute: () => routeState,
  useRouter: () => ({ push: routerPush, replace: routerReplace }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

vi.mock('@/composables/useMemberApp', () => ({
  useMemberApp: () => memberAppState,
}))

const MenuStub = {
  props: ['options', 'value'],
  emits: ['update:value'],
  template: `
    <nav data-test="menu" :data-value="value">
      <button
        v-for="option in options"
        :key="option.key"
        data-test="menu-item"
        :data-key="option.key"
        @click="$emit('update:value', option.key)"
      >
        {{ option.label }}
      </button>
    </nav>
  `,
}

function mountLayout() {
  return mount(DashboardLayout, {
    global: {
      stubs: {
        RouterView: { template: '<section class="route-page">页面内容</section>' },
        NMenu: MenuStub,
      },
    },
  })
}

function menuLabels(wrapper: ReturnType<typeof mountLayout>) {
  return wrapper.findAll('[data-test="menu-item"]').map(item => item.text())
}

function menuKeys(wrapper: ReturnType<typeof mountLayout>) {
  return wrapper.findAll('[data-test="menu-item"]').map(item => item.attributes('data-key'))
}

describe('DashboardLayout', () => {
  beforeEach(() => {
    routeState.path = '/runtime-nodes'
    routerPush.mockClear()
    routerReplace.mockClear()
    logout.mockClear()
    authState.user = { id: 'admin-1', username: 'admin', display_name: 'admin', role: 'platform_admin', org_id: 'org-1' }
    authState.isPlatformAdmin = true
    authState.isOrgAdmin = false
    authState.isOrgMember = false
    memberAppState.appId.value = undefined
    memberAppState.hasApp.value = false
    memberAppState.isLoading.value = false
  })

  // 覆盖后台整体骨架：内容区必须给子页面提供可撑满的剩余高度。
  it('wraps routed pages in a fill-height content frame', () => {
    const wrapper = mountLayout()
    const content = wrapper.findComponent(NLayoutContent)

    expect(content.props('contentStyle')).toContain('height: calc(100vh - 64px)')
    expect(content.props('contentStyle')).toContain('display: flex')
    expect(wrapper.find('.dashboard-page-frame').exists()).toBe(true)
  })

  // 覆盖组织成员菜单：唯一实例的各个业务 tab 被拉平到左侧菜单。
  it('renders flattened app entries for org_member', () => {
    routeState.path = '/apps/app-1/overview'
    authState.user = { id: 'member-1', username: 'member', display_name: '成员', role: 'org_member', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = false
    authState.isOrgMember = true
    memberAppState.appId.value = 'app-1'
    memberAppState.hasApp.value = true

    const wrapper = mountLayout()

    expect(menuLabels(wrapper)).toEqual(['总览', '任务', '定时任务', '渠道', '个人知识库', '工作目录', '企业知识库', '用量'])
    expect(menuKeys(wrapper)).toEqual([
      '/apps/app-1/overview',
      '/apps/app-1/kanban',
      '/apps/app-1/cron',
      '/apps/app-1/channels',
      '/apps/app-1/knowledge',
      '/apps/app-1/workspace',
      '/knowledge',
      '/usage',
    ])
  })

  // 覆盖组织成员当前路由高亮：任务页应选中左侧「任务」而不是旧的「实例」入口。
  it('selects the matching flattened member app entry', () => {
    routeState.path = '/apps/app-1/kanban'
    authState.user = { id: 'member-1', username: 'member', display_name: '成员', role: 'org_member', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = false
    authState.isOrgMember = true
    memberAppState.appId.value = 'app-1'
    memberAppState.hasApp.value = true

    const wrapper = mountLayout()

    expect(wrapper.find('[data-test="menu"]').attributes('data-value')).toBe('/apps/app-1/kanban')
  })

  // 覆盖成员无实例边界：实例能力入口统一落到空状态页，避免生成缺少 appId 的坏路由。
  it('points member app entries to empty state when member has no app', () => {
    routeState.path = '/apps/empty'
    authState.user = { id: 'member-1', username: 'member', display_name: '成员', role: 'org_member', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = false
    authState.isOrgMember = true
    memberAppState.appId.value = undefined
    memberAppState.hasApp.value = false

    const wrapper = mountLayout()

    expect(menuKeys(wrapper).slice(0, 6)).toEqual(['/apps/empty', '/apps/empty', '/apps/empty', '/apps/empty', '/apps/empty', '/apps/empty'])
    expect(wrapper.find('[data-test="menu"]').attributes('data-value')).toBe('/apps/empty')
  })

  // 覆盖非成员菜单文案：组织级知识库统一叫「企业知识库」，但管理员仍保留「实例」入口。
  it('renames organization knowledge entry for non-member menus', () => {
    routeState.path = '/knowledge'
    authState.user = { id: 'org-admin-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = true
    authState.isOrgMember = false

    const wrapper = mountLayout()

    expect(menuLabels(wrapper)).toContain('实例')
    expect(menuLabels(wrapper)).toContain('企业知识库')
    expect(menuLabels(wrapper)).not.toContain('知识库')
  })
})
```

- [ ] **Step 2: Run DashboardLayout tests to verify they fail**

Run:

```bash
npm --prefix web test -- DashboardLayout
```

Expected: FAIL. The failing assertions should mention missing member menu labels such as `任务` / `个人知识库`, or `data-value` still resolving to `/apps/app-1/overview` for every `/apps` route.

- [ ] **Step 3: Implement member menu generation**

In `web/src/layouts/DashboardLayout.vue`, update the lucide import:

```ts
import {
  BarChart3, BookOpen, Bot, Boxes, Building2, CalendarClock, FileSearch,
  FolderOpen, Gauge, LayoutDashboard, ListChecks, LogOut, Radio, RefreshCw,
  Server, ShieldCheck, Users, Wallet,
} from 'lucide-vue-next'
```

Add these helpers after `const { appId: memberAppId, hasApp: memberHasApp } = useMemberApp()`:

```ts
// MemberAppTab 是组织成员左侧菜单可直达的实例业务分区；值必须与 /apps/:appId/:tab 子路由末段一致。
type MemberAppTab = 'overview' | 'kanban' | 'cron' | 'channels' | 'knowledge' | 'workspace'

// memberAppTabs 用于从当前路由末段反查成员菜单高亮项，避免所有 /apps 路径都落到同一个「实例」入口。
const memberAppTabs: readonly MemberAppTab[] = ['overview', 'kanban', 'cron', 'channels', 'knowledge', 'workspace']

// memberAppTabPath 根据成员唯一实例生成现有详情页路由；无实例时统一落到空状态页。
function memberAppTabPath(tab: MemberAppTab) {
  if (!isOrgMember.value) return '/apps'
  if (memberHasApp.value && memberAppId.value) return `/apps/${memberAppId.value}/${tab}`
  return '/apps/empty'
}
```

Replace the existing `memberAppPath` computed with:

```ts
// org_member 的总览目标：有实例指向唯一实例 overview，无实例指向空状态。
const memberAppPath = computed(() => memberAppTabPath('overview'))
```

Replace the beginning of `activeKey` with this version:

```ts
const activeKey = computed(() => {
  const p = route.path
  if (p === '/') return isOrgMember.value ? memberAppPath.value : '/'
  // org_member 的实例 tab 已拉平到左侧菜单，需要按子路由末段分别高亮。
  if (isOrgMember.value && p.startsWith('/apps')) {
    if (p === '/apps/empty') return '/apps/empty'
    const tab = p.split('/')[3] as MemberAppTab | undefined
    return tab && memberAppTabs.includes(tab) ? memberAppTabPath(tab) : memberAppPath.value
  }
  if (p.startsWith('/apps')) return memberAppPath.value
  const prefixes = [
    '/console',
    '/organizations',
    '/assistant-versions',
    '/members',
    '/knowledge',
    '/usage',
    '/balance',
    '/audit-logs',
    '/runtime-nodes',
    '/platform/permissions',
  ]
  return prefixes.find(k => p.startsWith(k)) ?? '/'
})
```

At the start of `menuOptions`, before the existing platform/admin branch, add:

```ts
  if (isOrgMember.value) {
    return [
      { key: memberAppTabPath('overview'), label: '总览', icon: () => h(LayoutDashboard, { size: 18 }) },
      { key: memberAppTabPath('kanban'), label: '任务', icon: () => h(ListChecks, { size: 18 }) },
      { key: memberAppTabPath('cron'), label: '定时任务', icon: () => h(CalendarClock, { size: 18 }) },
      { key: memberAppTabPath('channels'), label: '渠道', icon: () => h(Radio, { size: 18 }) },
      { key: memberAppTabPath('knowledge'), label: '个人知识库', icon: () => h(BookOpen, { size: 18 }) },
      { key: memberAppTabPath('workspace'), label: '工作目录', icon: () => h(FolderOpen, { size: 18 }) },
      { key: '/knowledge', label: '企业知识库', icon: () => h(BookOpen, { size: 18 }) },
      { key: '/usage', label: '用量', icon: () => h(BarChart3, { size: 18 }) },
    ]
  }
```

In the existing non-member `items.push(...)`, change:

```ts
{ key: '/knowledge', label: '知识库', icon: () => h(BookOpen, { size: 18 }) },
```

to:

```ts
{ key: '/knowledge', label: '企业知识库', icon: () => h(BookOpen, { size: 18 }) },
```

- [ ] **Step 4: Run DashboardLayout tests to verify they pass**

Run:

```bash
npm --prefix web test -- DashboardLayout
```

Expected: PASS for `DashboardLayout`.

- [ ] **Step 5: Commit DashboardLayout changes**

Run:

```bash
git add web/src/layouts/DashboardLayout.vue web/src/layouts/DashboardLayout.spec.ts
git commit -m "feat(web): 拉平组织成员实例菜单" -m "为组织成员左侧菜单直接展示唯一实例的总览、任务、定时任务、渠道、个人知识库和工作目录入口。继续复用现有 /apps/:appId 子路由，并将组织级知识库入口统一命名为企业知识库。"
```

---

### Task 2: AppDetailPage 隐藏成员重复 tab

**Files:**
- Modify: `web/src/pages/apps/AppDetailPage.spec.ts`
- Modify: `web/src/pages/apps/AppDetailPage.vue`

- [ ] **Step 1: Write failing tests for member tab visibility**

Update `web/src/pages/apps/AppDetailPage.spec.ts` to use mutable auth state and add the member visibility cases:

```ts
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AppDetailPage from './AppDetailPage.vue'

const authState = vi.hoisted(() => ({
  isPlatformAdmin: false,
  isOrgMember: false,
}))

// 实例详情标题只展示业务可读名称，不把实例 UUID 作为主视觉信息展示给用户。
vi.mock('@/api/hooks/useApps', () => ({
  useAppQuery: () => ({
    data: ref({
      id: '00000000-0000-0000-0000-000000000001',
      org_id: '00000000-0000-0000-0000-000000000101',
      owner_user_id: '00000000-0000-0000-0000-000000000201',
      name: '测试实例',
      status: 'running',
      api_key_status: 'active',
    }),
    isLoading: ref(false),
    error: ref(null),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
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

function mountDetail() {
  return mount(AppDetailPage, {
    global: {
      stubs: {
        AppStatusTag: { template: '<span />' },
        NCard: { template: '<section><slot name="header" /><slot /></section>' },
      },
    },
  })
}

describe('AppDetailPage', () => {
  beforeEach(() => {
    authState.isPlatformAdmin = false
    authState.isOrgMember = false
  })

  // 覆盖管理员/组织管理员视角：实例详情页仍保留业务 tab 导航。
  it('非组织成员保留实例详情 tab 入口', () => {
    const wrapper = mountDetail()

    expect(wrapper.find('.tab-nav').exists()).toBe(true)
    expect(wrapper.text()).toContain('定时任务')
  })

  // 覆盖组织成员视角：实例能力已拉平到左侧菜单，详情页顶部不再重复显示 tab。
  it('组织成员隐藏实例详情 tab 入口', () => {
    authState.isOrgMember = true

    const wrapper = mountDetail()

    expect(wrapper.find('.tab-nav').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('定时任务')
  })

  // 覆盖实例详情标题展示规则，避免 UUID 泄露到主视觉标题。
  it('标题展示实例名称且不展示实例 UUID', () => {
    const wrapper = mountDetail()

    expect(wrapper.text()).toContain('测试实例')
    expect(wrapper.text()).not.toContain('00000000-0000-0000-0000-000000000001')
  })
})
```

- [ ] **Step 2: Run AppDetailPage tests to verify they fail**

Run:

```bash
npm --prefix web test -- AppDetailPage
```

Expected: FAIL. The member test should find `.tab-nav` still exists.

- [ ] **Step 3: Implement member tab hiding**

In `web/src/pages/apps/AppDetailPage.vue`, add this computed after `const app = computed<AppDTO | null>(() => appQuery.data.value ?? null)`:

```ts
// showTabNav 控制详情页顶部 tab 是否展示；组织成员已通过左侧菜单直达实例能力，隐藏可避免重复导航。
const showTabNav = computed(() => !auth.isOrgMember)
```

Change the tab nav template condition from:

```vue
<div v-if="app" class="tab-nav">
```

to:

```vue
<div v-if="app && showTabNav" class="tab-nav">
```

- [ ] **Step 4: Run AppDetailPage tests to verify they pass**

Run:

```bash
npm --prefix web test -- AppDetailPage
```

Expected: PASS for `AppDetailPage`.

- [ ] **Step 5: Commit AppDetailPage changes**

Run:

```bash
git add web/src/pages/apps/AppDetailPage.vue web/src/pages/apps/AppDetailPage.spec.ts
git commit -m "feat(web): 隐藏成员实例详情页重复标签" -m "组织成员的实例能力入口已迁移到左侧菜单，进入实例详情子页面时隐藏顶部 tab。管理员视角继续保留原实例详情 tab 导航。"
```

---

### Task 3: RoleAwareHome 成员默认落点

**Files:**
- Create: `web/src/pages/dashboard/RoleAwareHome.spec.ts`
- Modify: `web/src/pages/dashboard/RoleAwareHome.vue`

- [ ] **Step 1: Write failing tests for member home redirect and card copy**

Create `web/src/pages/dashboard/RoleAwareHome.spec.ts`:

```ts
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import RoleAwareHome from './RoleAwareHome.vue'

const routerReplace = vi.hoisted(() => vi.fn())
const authState = vi.hoisted(() => ({
  user: { id: 'member-1', username: 'member', display_name: '成员', role: 'org_member', org_id: 'org-1' },
}))
const memberAppState = vi.hoisted(() => ({
  appId: { value: 'app-1' as string | undefined },
  hasApp: { value: true },
  isLoading: { value: false },
}))

vi.mock('vue-router', () => ({
  RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
  useRouter: () => ({ replace: routerReplace }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

vi.mock('@/composables/useMemberApp', () => ({
  useMemberApp: () => memberAppState,
}))

function mountHome() {
  return mount(RoleAwareHome)
}

describe('RoleAwareHome', () => {
  beforeEach(() => {
    routerReplace.mockClear()
    authState.user = { id: 'member-1', username: 'member', display_name: '成员', role: 'org_member', org_id: 'org-1' }
    memberAppState.appId.value = 'app-1'
    memberAppState.hasApp.value = true
    memberAppState.isLoading.value = false
  })

  // 覆盖组织成员默认首页：有唯一实例时直接进入该实例的 overview。
  it('redirects org_member home to their app overview', async () => {
    mountHome()
    await nextTick()

    expect(routerReplace).toHaveBeenCalledWith('/apps/app-1/overview')
  })

  // 覆盖组织成员无实例边界：不能拼接缺失 appId 的路由，应进入空状态页。
  it('redirects org_member home to empty state when no app exists', async () => {
    memberAppState.appId.value = undefined
    memberAppState.hasApp.value = false

    mountHome()
    await nextTick()

    expect(routerReplace).toHaveBeenCalledWith('/apps/empty')
  })

  // 覆盖成员实例查询加载中边界：等待 useMemberApp 完成，避免先跳空状态再闪回。
  it('does not redirect org_member while member app query is loading', async () => {
    memberAppState.isLoading.value = true

    mountHome()
    await nextTick()

    expect(routerReplace).not.toHaveBeenCalled()
  })

  // 覆盖组织管理员首页文案：组织级知识库入口统一使用「企业知识库」。
  it('shows enterprise knowledge copy for org_admin quick card', () => {
    authState.user = { id: 'owner-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }

    const wrapper = mountHome()

    expect(wrapper.text()).toContain('企业知识库')
    expect(wrapper.text()).not.toContain('知识库上传共享文件')
  })
})
```

- [ ] **Step 2: Run RoleAwareHome tests to verify they fail**

Run:

```bash
npm --prefix web test -- RoleAwareHome
```

Expected: FAIL. The member redirect tests should fail because `RoleAwareHome.vue` currently only redirects platform and org admins.

- [ ] **Step 3: Implement member redirect and enterprise knowledge copy**

In `web/src/pages/dashboard/RoleAwareHome.vue`, update the Vue import:

```ts
import { computed, watch } from 'vue'
```

Change the `useMemberApp()` destructuring:

```ts
const { appId: memberAppId, hasApp: memberHasApp, isLoading: memberAppLoading } = useMemberApp()
```

Replace the existing `onMounted(...)` block with:

```ts
// memberHomePath 复用现有实例详情路由；无实例时进入空状态页，避免生成缺少 appId 的路径。
const memberHomePath = computed(() =>
  memberHasApp.value && memberAppId.value ? `/apps/${memberAppId.value}/overview` : '/apps/empty',
)

// 首页只承担按角色分流的职责；成员实例查询未完成前不跳转，避免误落到空状态。
watch(
  () => ({
    role: auth.user?.role,
    memberLoading: memberAppLoading.value,
    memberPath: memberHomePath.value,
  }),
  ({ role, memberLoading, memberPath }) => {
    if (role === 'platform_admin') {
      void router.replace('/console')
    } else if (role === 'org_admin') {
      void router.replace('/org-console')
    } else if (role === 'org_member' && !memberLoading) {
      void router.replace(memberPath)
    }
  },
  { immediate: true },
)
```

In the `org_admin` cards section, change:

```ts
{ path: '/knowledge', title: '企业知识库', subtitle: '上传共享文件' },
```

to:

```ts
{ path: '/knowledge', title: '企业知识库', subtitle: '上传企业共享文件' },
```

The member cards branch can remain as a fallback display while a redirect is pending. Keep its enterprise knowledge card title as `企业知识库`.

- [ ] **Step 4: Run RoleAwareHome tests to verify they pass**

Run:

```bash
npm --prefix web test -- RoleAwareHome
```

Expected: PASS for `RoleAwareHome`.

- [ ] **Step 5: Commit RoleAwareHome changes**

Run:

```bash
git add web/src/pages/dashboard/RoleAwareHome.vue web/src/pages/dashboard/RoleAwareHome.spec.ts
git commit -m "feat(web): 成员首页直达唯一实例总览" -m "组织成员访问后台首页时等待唯一实例查询完成，并直接跳转到实例 overview；无实例时进入空状态页。同步组织管理员首页企业知识库文案。"
```

---

### Task 4: Integrated Verification

**Files:**
- Modify only if verification finds issues in files changed by Tasks 1-3.

- [ ] **Step 1: Run focused frontend tests**

Run:

```bash
npm --prefix web test -- DashboardLayout AppDetailPage RoleAwareHome
```

Expected: PASS for all focused tests.

- [ ] **Step 2: Run typecheck**

Run:

```bash
npm --prefix web run typecheck
```

Expected: PASS with no TypeScript errors. If a new lucide export is unavailable, remove that missing name from the import and use these already-imported fallbacks instead: `ListChecks` -> `Bot`, `CalendarClock` -> `Boxes`, `Radio` -> `Gauge`, `FolderOpen` -> `Bot`; then rerun typecheck.

- [ ] **Step 3: Start local frontend for browser verification**

Run:

```bash
npm --prefix web run dev -- --host 0.0.0.0
```

Expected: Vite prints a local URL such as `http://localhost:5173/`. Keep the server running for the browser checks.

- [ ] **Step 4: Verify in a real browser with org_member**

Use the local credentials from `AGENTS.md` for the manager backend if the docker-compose environment is running. Log in as an organization member account that has one initialized instance.

Verify:

- Left menu shows `总览 / 任务 / 定时任务 / 渠道 / 个人知识库 / 工作目录 / 企业知识库 / 用量`.
- `总览` displays the existing instance overview content.
- `任务` opens `/apps/:appId/kanban`.
- `定时任务` opens `/apps/:appId/cron`.
- `渠道` opens `/apps/:appId/channels`.
- `个人知识库` opens `/apps/:appId/knowledge`.
- `工作目录` opens `/apps/:appId/workspace`.
- Instance detail top tab navigation is not visible for `org_member`.
- `企业知识库` opens `/knowledge`.

- [ ] **Step 5: Verify admin behavior did not regress**

Log in as `org_admin` or use an existing authenticated admin session.

Verify:

- Left menu still contains `实例`.
- Left menu shows `企业知识库` instead of `知识库`.
- Instance detail pages still show the original top tab navigation.
- The instance `knowledge` tab still displays `实例知识库` in the tab label.

- [ ] **Step 6: Stop the Vite server**

Stop the dev server with `Ctrl+C`.

- [ ] **Step 7: Final status check**

Run:

```bash
git status --short
```

Expected: only files intentionally changed by this feature are modified or committed. Pre-existing unrelated changes such as `scripts/check-compose-bind-mounts.sh` and `docs/reports/` may still appear and must not be included in feature commits.

---

## Self-Review

- Spec coverage: The plan covers member flattened menu, overview default landing, member tab hiding, enterprise knowledge naming, admin preservation, no new routes, tests, typecheck, and browser verification.
- Placeholder scan: The plan contains no unfinished markers or unspecified implementation steps.
- Type consistency: `MemberAppTab`, `memberAppTabPath`, `showTabNav`, `memberHomePath`, and `memberAppLoading` are introduced before use and match the referenced files.
