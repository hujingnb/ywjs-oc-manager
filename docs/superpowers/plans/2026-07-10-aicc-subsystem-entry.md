# AICC Subsystem Entry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make AICC an independent enterprise-admin subsystem entered from the enterprise overview page, not from the main left navigation.

**Architecture:** The normal manager shell keeps the enterprise overview and core management navigation. The AICC management UI moves to `/aicc-console` under a dedicated `AICCConsoleLayout`, with its own top bar and internal navigation while reusing existing auth, org permission checks, AICC hooks, and public chat routes. The existing AICC page is preserved as the implementation source, then split into workbench sections only where needed for routing and layout.

**Tech Stack:** Vue 3, Vue Router, Naive UI, Pinia auth store, Vue I18n, Vitest, Playwright/Chrome DevTools browser verification.

---

## File Structure

- Modify `web/src/pages/dashboard/RoleAwareHome.vue`: keep platform/member redirect behavior, but let `org_admin` stay on the enterprise overview page; add AICC subsystem card gated by `organization.aicc_enabled`.
- Modify `web/src/pages/dashboard/RoleAwareHome.spec.ts`: cover org-admin overview rendering, AICC card visibility, hidden disabled state, and click target.
- Modify `web/src/app/router.ts`: add `/aicc-console` route outside `DashboardLayout`; remove the old authenticated `/aicc` management route or redirect it to `/aicc-console`; keep public `/aicc/:publicToken` untouched.
- Create `web/src/layouts/AICCConsoleLayout.vue`: independent AICC shell with top bar, AICC-only navigation, return-to-console action, locale switcher, and nested `<RouterView>`.
- Modify `web/src/pages/aicc/AICCManagerPage.vue`: keep it as the first-pass workbench content and settings surface.
- Modify `web/src/pages/aicc/AICCSessionsPage.vue`, `web/src/pages/aicc/AICCLeadsPage.vue`, `web/src/pages/aicc/AICCAnalyticsPage.vue`: reuse existing sections in the new workbench routes; avoid behavior changes beyond route/layout integration.
- Modify `web/src/layouts/DashboardLayout.vue`: remove AICC from left menu and active prefixes; preserve the organization query if still needed by overview or web-publish.
- Modify `web/src/layouts/DashboardLayout.spec.ts`: update org-admin menu expectations so AICC no longer appears in the left menu.
- Modify `web/src/i18n/locales/zh/dashboard.ts`, `web/src/i18n/locales/en/dashboard.ts`: add overview/subsystem/AICC card copy.
- Modify `web/src/i18n/locales/zh/layout.ts`, `web/src/i18n/locales/en/layout.ts`: remove the left-nav AICC copy after confirming no references remain.
- Modify `web/src/i18n/locales/zh/aicc.ts`, `web/src/i18n/locales/en/aicc.ts`: add workbench nav/topbar labels for `接待台 / 会话 / 知识库 / 线索 / 分析 / 设置`.
- Add tests near changed files instead of adding a new test framework.

---

### Task 1: Enterprise Overview Entry

**Files:**
- Modify: `web/src/pages/dashboard/RoleAwareHome.vue`
- Modify: `web/src/pages/dashboard/RoleAwareHome.spec.ts`
- Modify: `web/src/i18n/locales/zh/dashboard.ts`
- Modify: `web/src/i18n/locales/en/dashboard.ts`

- [ ] **Step 1: Write failing tests for org-admin overview and AICC visibility**

Add or update these tests in `web/src/pages/dashboard/RoleAwareHome.spec.ts`:

```ts
const organizationState = vi.hoisted(() => {
  const { ref } = require('vue') as typeof import('vue')
  return {
    data: ref({
      id: 'org-1',
      name: '测试企业',
      status: 'enabled',
      code: 'test-org',
      aicc_enabled: true,
    }),
  }
})

vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationQuery: () => organizationState,
}))

// 覆盖企业管理员默认落点：org_admin 不再被首页自动替换到 /org-console，而是在概览页看到子系统入口。
it('keeps org_admin on enterprise overview and shows enabled AICC subsystem card', async () => {
  authState.user = { id: 'owner-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
  organizationState.data.value = {
    id: 'org-1',
    name: '测试企业',
    status: 'enabled',
    code: 'test-org',
    aicc_enabled: true,
  }

  const wrapper = mountHome()
  await nextTick()

  expect(routerReplace).not.toHaveBeenCalledWith('/org-console')
  expect(wrapper.text()).toContain('子系统入口')
  expect(wrapper.text()).toContain('AICC 客服')
  expect(wrapper.find('a[href="/aicc-console"]').exists()).toBe(true)
})

// 覆盖未开通企业边界：未开通 AICC 时概览页不能暴露客服子系统入口。
it('hides AICC subsystem card for org_admin when AICC is disabled', () => {
  authState.user = { id: 'owner-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
  organizationState.data.value = {
    id: 'org-1',
    name: '测试企业',
    status: 'enabled',
    code: 'test-org',
    aicc_enabled: false,
  }

  const wrapper = mountHome()

  expect(wrapper.text()).not.toContain('AICC 客服')
  expect(wrapper.find('a[href="/aicc-console"]').exists()).toBe(false)
})
```

- [ ] **Step 2: Run the failing tests**

Run:

```bash
cd web
npm test -- RoleAwareHome.spec.ts --run
```

Expected: FAIL because `useOrganizationQuery` is not used by `RoleAwareHome`, org-admin still redirects to `/org-console`, and no `/aicc-console` card exists.

- [ ] **Step 3: Implement the overview AICC card**

In `web/src/pages/dashboard/RoleAwareHome.vue`, import `useOrganizationQuery`, remove the org-admin redirect branch, and add a separate subsystem section. Use this shape:

```ts
import { useOrganizationQuery } from '@/api/hooks/useOrganizations'

const ownOrgId = computed(() => auth.user?.role === 'org_admin' ? auth.user.org_id ?? undefined : undefined)
const { data: ownOrganization } = useOrganizationQuery(ownOrgId)
const aiccEnabledForOrg = computed(() => Boolean(ownOrganization.value?.aicc_enabled))

interface SubsystemCard { path: string; title: string; subtitle: string; action: string }

const subsystemCards = computed<SubsystemCard[]>(() => {
  if (auth.user?.role !== 'org_admin' || !aiccEnabledForOrg.value) return []
  return [{
    path: '/aicc-console',
    title: t('dashboard.subsystems.aicc.title'),
    subtitle: t('dashboard.subsystems.aicc.subtitle'),
    action: t('dashboard.subsystems.aicc.action'),
  }]
})
```

Template addition after the existing quick cards:

```vue
<section v-if="subsystemCards.length" class="subsystem-section">
  <div class="section-heading">
    <div>
      <p class="eyebrow">{{ t('dashboard.subsystems.eyebrow') }}</p>
      <h3>{{ t('dashboard.subsystems.title') }}</h3>
    </div>
  </div>
  <div class="subsystem-grid">
    <RouterLink v-for="card in subsystemCards" :key="card.path" class="subsystem-card" :to="card.path">
      <span class="subsystem-mark">AI</span>
      <span>
        <strong>{{ card.title }}</strong>
        <small>{{ card.subtitle }}</small>
      </span>
      <em>{{ card.action }}</em>
    </RouterLink>
  </div>
</section>
```

Keep the existing platform and member redirects. Do not redirect `org_admin` from `/` to `/org-console`.

- [ ] **Step 4: Add i18n copy**

Add matching keys to `web/src/i18n/locales/zh/dashboard.ts`:

```ts
subsystems: {
  eyebrow: '子系统',
  title: '子系统入口',
  aicc: {
    title: 'AICC 客服',
    subtitle: '进入独立客服工作台，管理接待、会话、线索和投放配置',
    action: '进入工作台',
  },
},
```

Add matching keys to `web/src/i18n/locales/en/dashboard.ts`:

```ts
subsystems: {
  eyebrow: 'Subsystems',
  title: 'Subsystem Entry',
  aicc: {
    title: 'AICC Service',
    subtitle: 'Open the dedicated service workspace for reception, sessions, leads, and deployment',
    action: 'Open workspace',
  },
},
```

- [ ] **Step 5: Run tests and commit**

Run:

```bash
cd web
npm test -- RoleAwareHome.spec.ts --run
npm test -- i18n/locales/completeness.spec.ts --run
```

Expected: PASS.

Commit:

```bash
git add web/src/pages/dashboard/RoleAwareHome.vue web/src/pages/dashboard/RoleAwareHome.spec.ts web/src/i18n/locales/zh/dashboard.ts web/src/i18n/locales/en/dashboard.ts
git commit -m "feat(aicc): 在企业概览页增加客服子系统入口" -m "企业管理员默认停留在概览页，并在企业已开通 AICC 时展示进入 /aicc-console 的子系统卡片。补齐中英文 i18n 与概览页单元测试。"
```

---

### Task 2: Remove AICC From Main Left Navigation

**Files:**
- Modify: `web/src/layouts/DashboardLayout.vue`
- Modify: `web/src/layouts/DashboardLayout.spec.ts`
- Modify: `web/src/i18n/locales/zh/layout.ts`
- Modify: `web/src/i18n/locales/en/layout.ts`

- [ ] **Step 1: Write/update navigation tests**

Update the org-admin management menu test in `web/src/layouts/DashboardLayout.spec.ts`:

```ts
// 覆盖 org_admin 企业管理视角菜单：AICC 不再作为左侧菜单项出现，入口由企业概览页承载。
it('renders management menu without skills or AICC for org_admin manage perspective', () => {
  routeState.path = '/'
  authState.user = { id: 'org-admin-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
  authState.isPlatformAdmin = false
  authState.isOrgAdmin = true
  authState.isOrgMember = false
  adminPerspectiveState.perspective.value = 'manage'

  const wrapper = mountLayout()

  expect(menuLabels(wrapper)).toEqual(['总览', '成员', '已发布站点', '实例', '企业知识库', '账户余额', '审计', '用量'])
  expect(menuLabels(wrapper)).not.toContain('技能')
  expect(menuLabels(wrapper)).not.toContain('AICC 客服')
})
```

Change the existing disabled-AICC test into a stronger invariant:

```ts
// 覆盖 AICC 入口归属：即使企业已开通 AICC，左侧菜单也不展示客服入口。
it('does not render AICC in the main menu even when organization has enabled AICC', () => {
  routeState.path = '/'
  authState.user = { id: 'org-admin-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
  authState.isPlatformAdmin = false
  authState.isOrgAdmin = true
  authState.isOrgMember = false
  organizationState.data.value = {
    id: 'org-1',
    name: '测试企业',
    status: 'enabled',
    code: 'test-org',
    aicc_enabled: true,
  }

  const wrapper = mountLayout()

  expect(menuLabels(wrapper)).not.toContain('AICC 客服')
  expect(menuKeys(wrapper)).not.toContain('/aicc')
  expect(menuKeys(wrapper)).not.toContain('/aicc-console')
})
```

- [ ] **Step 2: Run the failing layout test**

Run:

```bash
cd web
npm test -- DashboardLayout.spec.ts --run
```

Expected: FAIL because the menu still includes `AICC 客服`.

- [ ] **Step 3: Remove AICC menu branch**

In `web/src/layouts/DashboardLayout.vue`:

Remove `Headphones` from the lucide import if it is no longer used.

Remove `'/aicc'` from the `prefixes` array.

Remove this block:

```ts
if (isOrgAdmin.value && aiccEnabledForOrg.value) {
  items.push({ key: '/aicc', label: t('layout.nav.aicc'), icon: () => h(Headphones, { size: 18 }) })
}
```

If `ownOrganization` is no longer used in `DashboardLayout.vue`, remove:

```ts
import { useOrganizationQuery } from '@/api/hooks/useOrganizations'
const { data: ownOrganization } = useOrganizationQuery(ownOrgId)
const aiccEnabledForOrg = computed(() => Boolean(ownOrganization.value?.aicc_enabled))
```

Keep `ownOrgId` because web-publish still uses it.

- [ ] **Step 4: Clean unused i18n key only if no references remain**

Run:

```bash
rg "layout\\.nav\\.aicc|nav\\.aicc|t\\('layout.nav.aicc'" web/src
```

If no references remain, remove `aicc` from `web/src/i18n/locales/zh/layout.ts` and `web/src/i18n/locales/en/layout.ts`. If a reference remains in an approved location, keep the key.

- [ ] **Step 5: Run tests and commit**

Run:

```bash
cd web
npm test -- DashboardLayout.spec.ts --run
npm test -- i18n/locales/completeness.spec.ts --run
```

Expected: PASS.

Commit:

```bash
git add web/src/layouts/DashboardLayout.vue web/src/layouts/DashboardLayout.spec.ts web/src/i18n/locales/zh/layout.ts web/src/i18n/locales/en/layout.ts
git commit -m "feat(aicc): 从主后台菜单移除客服入口" -m "AICC 入口改由企业概览页承载，主后台左侧菜单不再展示客服项。同步更新菜单测试和不再使用的布局文案。"
```

---

### Task 3: Add Independent AICC Console Route And Shell

**Files:**
- Modify: `web/src/app/router.ts`
- Create: `web/src/layouts/AICCConsoleLayout.vue`
- Create: `web/src/layouts/AICCConsoleLayout.spec.ts`
- Modify: `web/src/i18n/locales/zh/aicc.ts`
- Modify: `web/src/i18n/locales/en/aicc.ts`

- [ ] **Step 1: Add shell tests**

Create `web/src/layouts/AICCConsoleLayout.spec.ts`:

```ts
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import AICCConsoleLayout from './AICCConsoleLayout.vue'

const routerPush = vi.hoisted(() => vi.fn())
const routeState = vi.hoisted(() => ({ path: '/aicc-console' }))

vi.mock('vue-router', () => ({
  RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
  RouterView: { template: '<div data-test="aicc-router-view" />' },
  useRoute: () => routeState,
  useRouter: () => ({ push: routerPush }),
}))

vi.mock('@/components/LocaleSwitcher.vue', () => ({
  default: { template: '<button data-test="locale-switcher">locale</button>' },
}))

function mountShell() {
  i18n.global.locale.value = 'zh'
  return mount(AICCConsoleLayout, { global: { plugins: [i18n] } })
}

describe('AICCConsoleLayout', () => {
  // 覆盖 AICC 独立工作台骨架：必须展示专属导航和嵌套路由出口，不能依赖主后台左侧菜单。
  it('renders dedicated AICC shell navigation', () => {
    const wrapper = mountShell()

    expect(wrapper.text()).toContain('AICC 工作台')
    expect(wrapper.text()).toContain('接待台')
    expect(wrapper.text()).toContain('会话')
    expect(wrapper.text()).toContain('知识库')
    expect(wrapper.text()).toContain('线索')
    expect(wrapper.text()).toContain('分析')
    expect(wrapper.text()).toContain('设置')
    expect(wrapper.find('[data-test="aicc-router-view"]').exists()).toBe(true)
  })

  // 覆盖返回主后台行为：工作台顶栏提供明确返回入口，避免用户被困在子系统。
  it('returns to enterprise overview from top bar', async () => {
    const wrapper = mountShell()

    await wrapper.find('[data-test="aicc-return"]').trigger('click')

    expect(routerPush).toHaveBeenCalledWith('/')
  })
})
```

- [ ] **Step 2: Run the failing shell test**

Run:

```bash
cd web
npm test -- AICCConsoleLayout.spec.ts --run
```

Expected: FAIL because the layout file does not exist.

- [ ] **Step 3: Implement `AICCConsoleLayout.vue`**

Create `web/src/layouts/AICCConsoleLayout.vue` with this structure:

```vue
<template>
  <main class="aicc-console">
    <header class="aicc-console__topbar">
      <div>
        <p class="eyebrow">{{ t('aicc.console.eyebrow') }}</p>
        <h1>{{ t('aicc.console.title') }}</h1>
      </div>
      <div class="aicc-console__actions">
        <LocaleSwitcher :persist="true" />
        <n-button data-test="aicc-return" quaternary @click="router.push('/')">
          {{ t('aicc.console.returnToOverview') }}
        </n-button>
      </div>
    </header>

    <section class="aicc-console__body">
      <nav class="aicc-console__nav" :aria-label="t('aicc.console.navLabel')">
        <RouterLink v-for="item in navItems" :key="item.to" :to="item.to" class="aicc-console__nav-item" :class="{ active: isActive(item.to) }">
          <component :is="item.icon" :size="18" />
          <span>{{ item.label }}</span>
        </RouterLink>
      </nav>
      <section class="aicc-console__content">
        <RouterView />
      </section>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { BarChart3, BookOpen, Home, MessageSquareText, Settings, UserRoundSearch } from 'lucide-vue-next'

import LocaleSwitcher from '@/components/LocaleSwitcher.vue'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()

const navItems = computed(() => [
  { to: '/aicc-console', label: t('aicc.console.nav.reception'), icon: Home },
  { to: '/aicc-console/sessions', label: t('aicc.console.nav.sessions'), icon: MessageSquareText },
  { to: '/aicc-console/knowledge', label: t('aicc.console.nav.knowledge'), icon: BookOpen },
  { to: '/aicc-console/leads', label: t('aicc.console.nav.leads'), icon: UserRoundSearch },
  { to: '/aicc-console/analytics', label: t('aicc.console.nav.analytics'), icon: BarChart3 },
  { to: '/aicc-console/settings', label: t('aicc.console.nav.settings'), icon: Settings },
])

function isActive(path: string) {
  return path === '/aicc-console' ? route.path === path : route.path.startsWith(path)
}
</script>
```

Add scoped CSS for a full-height workbench shell, fixed-width AICC nav, and scrollable content. Keep card radii at `8px` or less.

- [ ] **Step 4: Add AICC console i18n keys**

Add to `web/src/i18n/locales/zh/aicc.ts`:

```ts
console: {
  eyebrow: 'AI Contact Center',
  title: 'AICC 工作台',
  returnToOverview: '返回概览',
  navLabel: 'AICC 工作台导航',
  nav: {
    reception: '接待台',
    sessions: '会话',
    knowledge: '知识库',
    leads: '线索',
    analytics: '分析',
    settings: '设置',
  },
},
```

Add to `web/src/i18n/locales/en/aicc.ts`:

```ts
console: {
  eyebrow: 'AI Contact Center',
  title: 'AICC Workspace',
  returnToOverview: 'Back to overview',
  navLabel: 'AICC workspace navigation',
  nav: {
    reception: 'Reception',
    sessions: 'Sessions',
    knowledge: 'Knowledge',
    leads: 'Leads',
    analytics: 'Analytics',
    settings: 'Settings',
  },
},
```

- [ ] **Step 5: Add routes**

In `web/src/app/router.ts`, import the new layout:

```ts
import AICCConsoleLayout from '@/layouts/AICCConsoleLayout.vue'
```

Add an authenticated top-level route before the `/` DashboardLayout route:

```ts
{
  path: '/aicc-console',
  component: AICCConsoleLayout,
  meta: { allowedRoles: ORG_ADMIN_ONLY },
  children: [
    { path: '', component: AICCManagerPage },
    { path: 'settings', component: AICCManagerPage },
  ],
},
```

For the old route, temporarily redirect:

```ts
{ path: 'aicc', redirect: '/aicc-console', meta: { allowedRoles: ORG_ADMIN_ONLY } },
```

Keep public route exactly as:

```ts
{
  path: '/aicc/:publicToken',
  component: PublicAICCChatPage,
  meta: { public: true },
},
```

- [ ] **Step 6: Run tests and commit**

Run:

```bash
cd web
npm test -- AICCConsoleLayout.spec.ts --run
npm test -- i18n/locales/completeness.spec.ts --run
npm run typecheck
```

Expected: PASS.

Commit:

```bash
git add web/src/app/router.ts web/src/layouts/AICCConsoleLayout.vue web/src/layouts/AICCConsoleLayout.spec.ts web/src/i18n/locales/zh/aicc.ts web/src/i18n/locales/en/aicc.ts
git commit -m "feat(aicc): 增加独立客服工作台路由" -m "新增 /aicc-console 独立工作台外壳，保留公开访客 /aicc/:publicToken 路由，并为旧管理入口提供重定向兼容。"
```

---

### Task 4: Split AICC Workbench Pages Enough For Independent Navigation

**Files:**
- Modify: `web/src/pages/aicc/AICCManagerPage.vue`
- Create: `web/src/pages/aicc/AICCWorkbenchSessionsPage.vue`
- Create: `web/src/pages/aicc/AICCWorkbenchKnowledgePage.vue`
- Create: `web/src/pages/aicc/AICCWorkbenchLeadsPage.vue`
- Create: `web/src/pages/aicc/AICCWorkbenchAnalyticsPage.vue`
- Modify: `web/src/app/router.ts`
- Modify if needed: `web/src/pages/aicc/AICCSessionsPage.vue`
- Modify if needed: `web/src/pages/aicc/AICCAnalyticsPage.vue`
- Add tests only for route-level wrappers if existing lower-level components already cover behavior.

- [ ] **Step 1: Define minimal route wrappers**

Create `web/src/pages/aicc/AICCWorkbenchSessionsPage.vue`:

```vue
<template>
  <AICCManagerPage initial-section="sessions" />
</template>

<script setup lang="ts">
import AICCManagerPage from './AICCManagerPage.vue'
</script>
```

Create `web/src/pages/aicc/AICCWorkbenchKnowledgePage.vue`:

```vue
<template>
  <AICCManagerPage initial-section="knowledge" />
</template>

<script setup lang="ts">
import AICCManagerPage from './AICCManagerPage.vue'
</script>
```

Create `web/src/pages/aicc/AICCWorkbenchAnalyticsPage.vue`:

```vue
<template>
  <AICCManagerPage initial-section="analytics" />
</template>

<script setup lang="ts">
import AICCManagerPage from './AICCManagerPage.vue'
</script>
```

Create `web/src/pages/aicc/AICCWorkbenchLeadsPage.vue`:

```vue
<template>
  <AICCManagerPage initial-section="leads" />
</template>

<script setup lang="ts">
import AICCManagerPage from './AICCManagerPage.vue'
</script>
```

- [ ] **Step 2: Add route-section prop to `AICCManagerPage.vue`**

At the top of `<script setup>`, add:

```ts
const props = withDefaults(defineProps<{
  initialSection?: 'reception' | 'sessions' | 'knowledge' | 'leads' | 'analytics' | 'settings'
}>(), {
  initialSection: 'reception',
})
```

Change `<n-tabs>` from uncontrolled:

```vue
<n-tabs type="segment" animated class="aicc-tabs">
```

to controlled:

```vue
<n-tabs v-model:value="activeSection" type="segment" animated class="aicc-tabs">
```

Add:

```ts
const activeSection = ref(props.initialSection === 'settings' || props.initialSection === 'reception' ? 'config' : props.initialSection)
```

When `initialSection` is `knowledge`, set `activeSection` to `config` and scroll the existing `.knowledge-panel` into view on mount with `nextTick`. Do not duplicate knowledge save logic in this task.

- [ ] **Step 3: Keep first pass conservative**

Do not fully refactor `AICCManagerPage.vue` into six independent components in this task. The goal is to make the independent workbench route and navigation usable with existing behavior. A deeper split is only needed after browser verification shows the combined page blocks the target workflow.

- [ ] **Step 4: Add internal workbench child routes**

In `web/src/app/router.ts`, extend `/aicc-console` children:

```ts
{
  path: '/aicc-console',
  component: AICCConsoleLayout,
  meta: { allowedRoles: ORG_ADMIN_ONLY },
  children: [
    { path: '', component: AICCManagerPage },
    { path: 'settings', component: AICCManagerPage },
    { path: 'sessions', component: () => import('@/pages/aicc/AICCWorkbenchSessionsPage.vue') },
    { path: 'knowledge', component: () => import('@/pages/aicc/AICCWorkbenchKnowledgePage.vue') },
    { path: 'leads', component: () => import('@/pages/aicc/AICCWorkbenchLeadsPage.vue') },
    { path: 'analytics', component: () => import('@/pages/aicc/AICCWorkbenchAnalyticsPage.vue') },
  ],
}
```

- [ ] **Step 5: Run AICC-related tests and commit**

Run:

```bash
cd web
npm test -- AICCWidgetScript.spec.ts --run
npm test -- i18n/locales/aicc.spec.ts --run
npm run typecheck
```

Expected: PASS.

Commit:

```bash
git add web/src/app/router.ts web/src/pages/aicc/AICCManagerPage.vue web/src/pages/aicc/AICCWorkbenchSessionsPage.vue web/src/pages/aicc/AICCWorkbenchKnowledgePage.vue web/src/pages/aicc/AICCWorkbenchLeadsPage.vue web/src/pages/aicc/AICCWorkbenchAnalyticsPage.vue
git commit -m "feat(aicc): 接入工作台内部页面导航" -m "为 /aicc-console 下的会话、知识库、线索和分析入口提供路由级页面，复用现有 AICC 管理能力并保持首轮拆分范围可控。"
```

---

### Task 5: Authorization And Direct Access Guard

**Files:**
- Modify: `web/src/app/router.ts`
- Modify or create: `web/src/app/router.spec.ts` if router guard tests exist; otherwise add coverage in layout/page specs.
- Modify: `web/src/pages/dashboard/RoleAwareHome.spec.ts`

- [ ] **Step 1: Confirm existing route guard role behavior**

Inspect `web/src/app/router.ts` below `router.beforeEach`. Confirm `meta.allowedRoles` is applied to top-level routes as well as child routes. If it already is, no route-guard code change is needed for role checks.

- [ ] **Step 2: Enforce AICC enabled at page level**

Because `aicc_enabled` is organization data, enforce it in `AICCConsoleLayout.vue` or the first child page using `useOrganizationQuery(ownOrgId)`. Add this behavior:

```ts
const auth = useAuthStore()
const ownOrgId = computed(() => auth.user?.role === 'org_admin' ? auth.user.org_id ?? undefined : undefined)
const { data: ownOrganization, isLoading: orgLoading } = useOrganizationQuery(ownOrgId)

watch(
  () => ({ loading: orgLoading.value, enabled: ownOrganization.value?.aicc_enabled }),
  ({ loading, enabled }) => {
    if (!loading && enabled === false) {
      void router.replace('/')
    }
  },
  { immediate: true },
)
```

Add a Chinese comment above the watch:

```ts
// AICC 子系统入口由企业开通状态控制；直接访问 /aicc-console 时也必须兜底拦截未开通企业。
```

- [ ] **Step 3: Test disabled direct access**

Extend `AICCConsoleLayout.spec.ts` with:

```ts
// 覆盖未开通企业直接访问兜底：即使用户手动输入 /aicc-console，也会回到概览页。
it('redirects disabled organizations back to overview', async () => {
  organizationState.data.value = {
    id: 'org-1',
    name: '测试企业',
    status: 'enabled',
    code: 'test-org',
    aicc_enabled: false,
  }

  mountShell()
  await nextTick()

  expect(routerReplace).toHaveBeenCalledWith('/')
})
```

The test requires adding mocks for `useAuthStore`, `useOrganizationQuery`, and router `replace` in `AICCConsoleLayout.spec.ts`.

- [ ] **Step 4: Run tests and commit**

Run:

```bash
cd web
npm test -- AICCConsoleLayout.spec.ts --run
npm test -- RoleAwareHome.spec.ts --run
npm run typecheck
```

Expected: PASS.

Commit:

```bash
git add web/src/layouts/AICCConsoleLayout.vue web/src/layouts/AICCConsoleLayout.spec.ts web/src/pages/dashboard/RoleAwareHome.spec.ts web/src/app/router.ts
git commit -m "feat(aicc): 限制未开通企业访问客服工作台" -m "在 AICC 独立工作台补充企业开通状态兜底校验，确保概览入口隐藏和直接访问拒绝的产品口径一致。"
```

---

### Task 6: Full Verification

**Files:**
- Modify only if verification finds defects.

- [ ] **Step 1: Run focused unit tests**

Run:

```bash
cd web
npm test -- RoleAwareHome.spec.ts DashboardLayout.spec.ts AICCConsoleLayout.spec.ts i18n/locales/completeness.spec.ts i18n/locales/aicc.spec.ts --run
```

Expected: PASS.

- [ ] **Step 2: Run typecheck and build**

Run:

```bash
cd web
npm run typecheck
npm run build
```

Expected: both PASS.

- [ ] **Step 3: Start local web app**

Use the project’s existing local dev flow. If no server is running:

```bash
cd web
npm run dev -- --host 0.0.0.0
```

Use `http://ocm.localhost` for browser verification as requested by the user. If Vite is not the active local frontend path, use the repository’s k3d ingress workflow and confirm the deployed frontend contains the new build.

- [ ] **Step 4: Browser verify with Chrome DevTools MCP**

Use real browser interactions:

1. Open `http://ocm.localhost/login`.
2. Log in as local enterprise admin from `AGENTS.md` if available for the target org, or use the seeded org-admin account from the current local environment.
3. Verify default landing page is enterprise overview, not `/org-console`.
4. Verify left menu does not contain `AICC 客服`.
5. Verify overview page shows `子系统入口` and `AICC 客服` only when `aicc_enabled=true`.
6. Click `AICC 客服`.
7. Verify URL becomes `/aicc-console`.
8. Verify the page is not inside the normal main-console content shell and has AICC-specific top bar/navigation.
9. Click AICC internal nav: `接待台`, `会话`, `知识库`, `线索`, `分析`, `设置`.
10. Verify existing AICC features still work at least at smoke level: agent list loads, create/edit form renders, public link/QR area renders for selected agent, sessions/leads/analytics pages render.
11. Open an existing public chat URL `/aicc/:publicToken` and verify it still renders public chat, not the admin console.

If verification requires WeChat scan or sending an external visitor message, stop and notify the user with the exact needed action.

- [ ] **Step 5: Fix any browser-found defects**

For each defect:

1. Add or update the nearest unit test if the issue is deterministic.
2. Implement the smallest fix.
3. Run the failing test, then the focused test set.
4. Re-run the browser step that failed.
5. Commit with a focused Conventional Commit message.

- [ ] **Step 6: Final commit if verification changed no code**

If Task 6 only ran verification and changed no files, do not create an empty commit. If code changed during fixes, commit those fixes before final handoff.

---

## Self-Review

- Spec coverage: The plan covers enterprise overview entry, AICC not in left menu, `/aicc-console` independent workbench, internal AICC navigation, open-state gating, public route preservation, i18n, and real browser verification.
- 占位符扫描：没有未决占位符、未完成章节或含糊实现步骤。
- Scope check: The plan is focused on frontend entry/routing/layout changes. It intentionally avoids backend API changes because the accepted design reuses existing auth, organization, and AICC APIs.
- Risk: Task 4 keeps the first page split conservative. If the final UX needs fully independent content for all six AICC nav pages, add a follow-up plan after the first browser verification rather than expanding this task beyond the current requirement.
