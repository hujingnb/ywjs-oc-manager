# AICC Console Navigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current left-side AICC module menu with the confirmed merged layout: top module navigation, a compact current-agent context bar, and content-area agent selection.

**Architecture:** Keep `AICCConsoleLayout.vue` responsible for access gating and the brand/header shell. Add an enabled-only `AICCConsoleWorkspace.vue` child that owns AICC agents query, top module navigation, current-agent context, and `RouterView`; this avoids AICC API requests before `aicc_enabled === true`. `AICCManagerPage.vue` consumes the provided console context so the context bar and page content share the same selected agent.

**Tech Stack:** Vue 3 `<script setup>`, Vue Router, Vue I18n, Naive UI, Vitest, Vue Test Utils, existing `useAICC` hooks.

---

## File Structure

- Create `web/src/pages/aicc/aiccConsoleContext.ts`
  - Provides a typed Vue injection key for shared AICC console state.
  - Exposes `useRequiredAICCConsoleContext()` so pages fail loudly if mounted outside `/aicc-console`.
- Create `web/src/layouts/AICCConsoleWorkspace.vue`
  - Renders top module navigation, current-agent context bar, content frame, and `RouterView`.
  - Owns the AICC agents query only after `AICCConsoleLayout` has confirmed access.
- Create `web/src/layouts/AICCConsoleWorkspace.spec.ts`
  - Covers top module navigation, current-agent context states, route pushes, and RouterView rendering.
- Modify `web/src/layouts/AICCConsoleLayout.vue`
  - Remove `n-layout-sider` and old left menu logic.
  - Keep brand/header, locale switcher, return button, organization enablement guard, and loading state.
  - Render `AICCConsoleWorkspace` only when `canRenderConsole` is true.
- Modify `web/src/layouts/AICCConsoleLayout.spec.ts`
  - Update expectations to assert no left module menu and no child workspace while loading/disabled.
- Modify `web/src/pages/aicc/AICCManagerPage.vue`
  - Consume shared selected agent state from context.
  - Keep the content-area agent list.
  - Remove duplicate hero/title block and reduce internal tab duplication where it conflicts with top module navigation.
- Modify `web/src/i18n/locales/zh/aicc.ts` and `web/src/i18n/locales/en/aicc.ts`
  - Add context-bar and agent action labels.
- Test existing generated or domain files only if typecheck requires it. No backend, database, OpenAPI, or generated API changes are expected.

---

### Task 1: Add Shared AICC Console Context

**Files:**
- Create: `web/src/pages/aicc/aiccConsoleContext.ts`

- [ ] **Step 1: Create context module**

Add this file:

```ts
import { inject, type ComputedRef, type InjectionKey, type Ref } from 'vue'

import type { AICCAgent } from '@/domain/aicc'

export interface AICCConsoleContext {
  agents: ComputedRef<AICCAgent[]>
  selectedAgentId: Ref<string | undefined>
  selectedAgent: ComputedRef<AICCAgent | undefined>
  agentsLoading: ComputedRef<boolean>
  agentsError: ComputedRef<Error | null>
  selectAgent: (agentId?: string) => void
  startCreateAgent: () => void
}

export const AICCConsoleContextKey: InjectionKey<AICCConsoleContext> = Symbol('AICCConsoleContext')

// AICC 管理页面必须挂载在 /aicc-console 工作台内；缺少上下文说明路由结构被误用。
export function useRequiredAICCConsoleContext(): AICCConsoleContext {
  const context = inject(AICCConsoleContextKey)
  if (!context) {
    throw new Error('AICC console context is not provided')
  }
  return context
}
```

- [ ] **Step 2: Verify type-only change**

Run:

```bash
cd web && npm run typecheck
```

Expected: PASS. If it fails, the error should be unrelated to the new file because it has no consumers yet.

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/aicc/aiccConsoleContext.ts
git commit -m "feat(aicc): 增加工作台共享上下文" -m "为 AICC 工作台外壳和内容页提供共享的智能体选择状态。"
```

---

### Task 2: Build Enabled-Only Workspace Shell With Top Navigation

**Files:**
- Create: `web/src/layouts/AICCConsoleWorkspace.vue`
- Create: `web/src/layouts/AICCConsoleWorkspace.spec.ts`
- Modify: `web/src/i18n/locales/zh/aicc.ts`
- Modify: `web/src/i18n/locales/en/aicc.ts`

- [ ] **Step 1: Write failing workspace tests**

Create `web/src/layouts/AICCConsoleWorkspace.spec.ts`:

```ts
import { mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import AICCConsoleWorkspace from './AICCConsoleWorkspace.vue'

const routerPush = vi.hoisted(() => vi.fn())
const routeState = vi.hoisted(() => ({ path: '/aicc-console' }))
const agentsState = vi.hoisted(() => {
  const { ref } = require('vue') as typeof import('vue')
  return {
    data: ref([
      {
        id: 'agent-1',
        name: '售前接待员',
        status: 'active',
        public_token: 'public-1',
        retention_days: 180,
      },
      {
        id: 'agent-2',
        name: '售后接待员',
        status: 'paused',
        public_token: undefined,
        retention_days: 90,
      },
    ]),
    isLoading: ref(false),
    error: ref<Error | null>(null),
  }
})

vi.mock('vue-router', () => ({
  RouterView: { template: '<main data-test="router-view">AICC 子页面</main>' },
  useRoute: () => routeState,
  useRouter: () => ({ push: routerPush }),
}))

vi.mock('@/api/hooks/useAICC', () => ({
  useAICCAgentsQuery: () => agentsState,
}))

const ButtonStub = defineComponent({
  emits: ['click'],
  setup(_, { slots, emit }) {
    return () => h('button', { onClick: () => emit('click') }, slots.default?.())
  },
})

function mountWorkspace() {
  i18n.global.locale.value = 'zh'
  return mount(AICCConsoleWorkspace, {
    global: {
      plugins: [i18n],
      stubs: {
        NButton: ButtonStub,
        'n-button': ButtonStub,
      },
    },
  })
}

describe('AICCConsoleWorkspace', () => {
  beforeEach(() => {
    routeState.path = '/aicc-console'
    routerPush.mockClear()
    agentsState.data.value = [
      { id: 'agent-1', name: '售前接待员', status: 'active', public_token: 'public-1', retention_days: 180 },
      { id: 'agent-2', name: '售后接待员', status: 'paused', public_token: undefined, retention_days: 90 },
    ]
    agentsState.isLoading.value = false
    agentsState.error.value = null
  })

  // 覆盖合并版导航：模块导航必须位于顶部，并保留 /aicc-console 深链。
  it('renders top module navigation and pushes registered console routes', async () => {
    const wrapper = mountWorkspace()
    const navItems = wrapper.findAll('[data-test="aicc-module-nav-item"]')

    expect(navItems.map(item => item.text())).toEqual(['接待台', '会话', '线索', '知识库', '分析', '设置'])
    expect(navItems.map(item => item.attributes('data-path'))).toEqual([
      '/aicc-console',
      '/aicc-console/sessions',
      '/aicc-console/leads',
      '/aicc-console/knowledge',
      '/aicc-console/analytics',
      '/aicc-console/settings',
    ])

    await navItems.find(item => item.text() === '会话')!.trigger('click')

    expect(routerPush).toHaveBeenCalledWith('/aicc-console/sessions')
  })

  // 覆盖当前智能体上下文：顶部摘要条应固定展示当前智能体、状态、公开入口和保留天数。
  it('renders the compact current-agent context bar', () => {
    const wrapper = mountWorkspace()

    expect(wrapper.find('[data-test="aicc-agent-context"]').text()).toContain('当前智能体')
    expect(wrapper.find('[data-test="aicc-agent-context"]').text()).toContain('售前接待员')
    expect(wrapper.find('[data-test="aicc-agent-context"]').text()).toContain('接待中')
    expect(wrapper.find('[data-test="aicc-agent-context"]').text()).toContain('已生成')
    expect(wrapper.find('[data-test="aicc-agent-context"]').text()).toContain('180 天')
  })

  // 覆盖空智能体状态：没有智能体时上下文条不能空白，必须引导新建。
  it('renders empty current-agent context when no agents exist', () => {
    agentsState.data.value = []

    const wrapper = mountWorkspace()

    expect(wrapper.find('[data-test="aicc-agent-context"]').text()).toContain('未选择智能体')
    expect(wrapper.find('[data-test="aicc-agent-context"]').text()).toContain('新建智能体')
  })

  // 覆盖子路由出口：工作台通过 RouterView 承载右侧模块页面。
  it('renders routed module content', () => {
    const wrapper = mountWorkspace()

    expect(wrapper.find('[data-test="router-view"]').text()).toBe('AICC 子页面')
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd web && npm test -- AICCConsoleWorkspace.spec.ts --run
```

Expected: FAIL because `AICCConsoleWorkspace.vue` does not exist.

- [ ] **Step 3: Add i18n keys**

In `web/src/i18n/locales/zh/aicc.ts`, extend `console`:

```ts
    currentAgent: '当前智能体',
    noAgentSelected: '未选择智能体',
    switchAgent: '切换智能体',
    createAgent: '新建智能体',
    agentLoading: '正在加载智能体',
    agentLoadFailed: '智能体加载失败',
```

In `web/src/i18n/locales/en/aicc.ts`, extend `console`:

```ts
    currentAgent: 'Current agent',
    noAgentSelected: 'No agent selected',
    switchAgent: 'Switch agent',
    createAgent: 'New agent',
    agentLoading: 'Loading agents',
    agentLoadFailed: 'Failed to load agents',
```

- [ ] **Step 4: Implement workspace shell**

Create `web/src/layouts/AICCConsoleWorkspace.vue`:

```vue
<template>
  <div class="aicc-console-workspace">
    <nav class="aicc-module-nav" :aria-label="t('aicc.console.navLabel')">
      <button
        v-for="item in navItems"
        :key="item.path"
        class="aicc-module-nav-item"
        :class="{ active: activeKey === item.path }"
        type="button"
        data-test="aicc-module-nav-item"
        :data-path="item.path"
        @click="onNav(item.path)"
      >
        <component :is="item.icon" :size="16" />
        <span>{{ t(item.labelKey) }}</span>
      </button>
    </nav>

    <section class="aicc-agent-context" data-test="aicc-agent-context">
      <div class="aicc-agent-context-main">
        <span class="aicc-context-label">{{ t('aicc.console.currentAgent') }}</span>
        <strong>{{ selectedAgent?.name ?? t('aicc.console.noAgentSelected') }}</strong>
        <span v-if="selectedAgent" class="aicc-context-meta">
          {{ statusMeta(selectedAgent.status).label }}
          · {{ selectedAgent.public_token ? t('aicc.manager.status.generated') : t('aicc.manager.status.generatedAfterSave') }}
          · {{ t('aicc.manager.status.days', { count: selectedAgent.retention_days || 0 }) }}
        </span>
        <span v-else-if="agentsQuery.isLoading.value" class="aicc-context-meta">{{ t('aicc.console.agentLoading') }}</span>
        <span v-else-if="agentsQuery.error.value" class="aicc-context-meta danger">{{ t('aicc.console.agentLoadFailed') }}</span>
      </div>
      <div class="aicc-agent-context-actions">
        <n-button secondary size="small">{{ t('aicc.console.switchAgent') }}</n-button>
        <n-button type="primary" size="small" @click="startCreateAgent">{{ t('aicc.console.createAgent') }}</n-button>
      </div>
    </section>

    <section class="aicc-content-frame">
      <RouterView />
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, provide, ref, watch } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NButton } from 'naive-ui'
import { BarChart3, BookOpen, LayoutDashboard, MessageSquare, Settings, Users } from 'lucide-vue-next'

import { useAICCAgentsQuery } from '@/api/hooks/useAICC'
import type { AICCAgentStatus } from '@/domain/aicc'
import { AICCConsoleContextKey } from '@/pages/aicc/aiccConsoleContext'

interface ConsoleNavItem {
  path: string
  labelKey: string
  icon: typeof LayoutDashboard
}

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const selectedAgentId = ref<string | undefined>()
const agentsQuery = useAICCAgentsQuery()
const agents = computed(() => agentsQuery.data.value ?? [])
const selectedAgent = computed(() => agents.value.find(agent => agent.id === selectedAgentId.value))
const agentsLoading = computed(() => agentsQuery.isLoading.value)
const agentsError = computed(() => agentsQuery.error.value)

const navItems: ConsoleNavItem[] = [
  { path: '/aicc-console', labelKey: 'aicc.console.nav.reception', icon: LayoutDashboard },
  { path: '/aicc-console/sessions', labelKey: 'aicc.console.nav.sessions', icon: MessageSquare },
  { path: '/aicc-console/leads', labelKey: 'aicc.console.nav.leads', icon: Users },
  { path: '/aicc-console/knowledge', labelKey: 'aicc.console.nav.knowledge', icon: BookOpen },
  { path: '/aicc-console/analytics', labelKey: 'aicc.console.nav.analytics', icon: BarChart3 },
  { path: '/aicc-console/settings', labelKey: 'aicc.console.nav.settings', icon: Settings },
]

const activeKey = computed(() => {
  if (route.path === '/aicc-console') return '/aicc-console'
  return navItems.find(item => route.path === item.path || route.path.startsWith(`${item.path}/`))?.path ?? '/aicc-console'
})

watch(
  agents,
  (items) => {
    if (!selectedAgentId.value && items.length > 0) selectedAgentId.value = items[0].id
  },
  { immediate: true },
)

provide(AICCConsoleContextKey, {
  agents,
  selectedAgentId,
  selectedAgent,
  agentsLoading,
  agentsError,
  selectAgent: (agentId?: string) => {
    selectedAgentId.value = agentId
  },
  startCreateAgent,
})

function onNav(path: string) {
  router.push(path)
}

function startCreateAgent() {
  selectedAgentId.value = undefined
}

function statusMeta(status?: AICCAgentStatus): { label: string; type: 'success' | 'warning' | 'default' | 'error' } {
  if (status === 'active') return { label: t('aicc.manager.status.active'), type: 'success' }
  if (status === 'paused') return { label: t('aicc.manager.status.paused'), type: 'warning' }
  if (status === 'deleted') return { label: t('aicc.manager.status.deleted'), type: 'error' }
  return { label: t('aicc.manager.status.draft'), type: 'default' }
}
</script>
```

Add scoped CSS in the same file:

```css
.aicc-console-workspace {
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
  gap: 12px;
}

.aicc-module-nav,
.aicc-agent-context {
  display: flex;
  align-items: center;
  border: 1px solid var(--color-divider);
  border-radius: 8px;
  background: var(--color-surface);
}

.aicc-module-nav {
  gap: 4px;
  padding: 6px;
  overflow-x: auto;
}

.aicc-module-nav-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 34px;
  padding: 0 12px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--color-text-secondary);
  cursor: pointer;
  font: inherit;
  white-space: nowrap;
}

.aicc-module-nav-item.active {
  background: #0f766e;
  color: #ffffff;
}

.aicc-agent-context {
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
}

.aicc-agent-context-main,
.aicc-agent-context-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.aicc-context-label {
  color: var(--color-text-secondary);
  font-size: 12px;
}

.aicc-context-meta {
  color: var(--color-text-secondary);
  font-size: 12px;
}

.aicc-context-meta.danger {
  color: var(--color-danger);
}

.aicc-content-frame {
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
}

@media (max-width: 768px) {
  .aicc-agent-context {
    align-items: flex-start;
    flex-direction: column;
  }
}
```

- [ ] **Step 5: Run workspace tests**

Run:

```bash
cd web && npm test -- AICCConsoleWorkspace.spec.ts --run
```

Expected: PASS with 4 tests.

- [ ] **Step 6: Commit**

```bash
git add web/src/layouts/AICCConsoleWorkspace.vue web/src/layouts/AICCConsoleWorkspace.spec.ts web/src/i18n/locales/zh/aicc.ts web/src/i18n/locales/en/aicc.ts
git commit -m "feat(aicc): 增加顶部导航工作台外壳" -m "新增启用后才挂载的 AICC 工作台内容外壳，承载顶部模块导航和当前智能体上下文条。"
```

---

### Task 3: Replace Old Left Sider Layout

**Files:**
- Modify: `web/src/layouts/AICCConsoleLayout.vue`
- Modify: `web/src/layouts/AICCConsoleLayout.spec.ts`

- [ ] **Step 1: Update layout tests first**

Edit `web/src/layouts/AICCConsoleLayout.spec.ts`:

1. Replace the `RouterView` mock with workspace mock:

```ts
vi.mock('./AICCConsoleWorkspace.vue', () => ({
  default: { template: '<main data-test="aicc-workspace">AICC 工作区</main>' },
}))
```

2. Remove the `NMenu` stub and `navLabels/navKeys` helpers.

3. Replace the first test body with:

```ts
  // 覆盖独立客服工作台骨架：外壳只保留品牌栏和返回入口，模块导航由启用后的工作区承载。
  it('renders the independent AICC console shell without the old left module menu', () => {
    const wrapper = mountLayout()

    expect(wrapper.text()).toContain('AI Contact Center')
    expect(wrapper.text()).toContain('AICC 工作台')
    expect(wrapper.find('[data-test="locale-switcher"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="aicc-workspace"]').text()).toBe('AICC 工作区')
    expect(wrapper.find('[data-test="aicc-nav"]').exists()).toBe(false)
  })
```

4. Delete the old navigation-push test from this file. Navigation is now covered by `AICCConsoleWorkspace.spec.ts`.

5. Update loading test expectation:

```ts
    expect(wrapper.find('[data-test="aicc-workspace"]').exists()).toBe(false)
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
cd web && npm test -- AICCConsoleLayout.spec.ts --run
```

Expected: FAIL because `AICCConsoleLayout.vue` still renders the old left sider and RouterView.

- [ ] **Step 3: Refactor layout**

In `web/src/layouts/AICCConsoleLayout.vue`:

1. Replace the template with a single vertical layout:

```vue
<template>
  <n-layout class="aicc-console-layout">
    <n-layout-header bordered class="aicc-console-header">
      <div class="aicc-brand aicc-brand-inline">
        <div class="aicc-brand-mark">AI</div>
        <div>
          <p>{{ t('aicc.console.eyebrow') }}</p>
          <strong>{{ t('aicc.console.title') }}</strong>
        </div>
      </div>
      <div class="aicc-header-actions">
        <LocaleSwitcher :persist="true" />
        <n-button secondary @click="returnToOverview">
          <template #icon><ArrowLeft :size="16" /></template>
          {{ t('aicc.console.returnToOverview') }}
        </n-button>
      </div>
    </n-layout-header>

    <n-layout-content content-style="height: calc(100vh - 64px); padding: 16px 20px; display: flex; flex-direction: column; overflow: auto">
      <div v-if="orgLoading" class="aicc-loading-state" role="status">
        {{ t('aicc.console.checkingAccess') }}
      </div>
      <AICCConsoleWorkspace v-else-if="canRenderConsole" />
    </n-layout-content>
  </n-layout>
</template>
```

2. Replace imports:

```ts
import { computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NButton, NLayout, NLayoutContent, NLayoutHeader } from 'naive-ui'
import { ArrowLeft } from 'lucide-vue-next'

import LocaleSwitcher from '@/components/LocaleSwitcher.vue'
import AICCConsoleWorkspace from './AICCConsoleWorkspace.vue'
```

3. Delete `useRoute`, `RouterView`, `NLayoutSider`, `NMenu`, `MenuOption`, all lucide module icons, `ConsoleNavItem`, `navItems`, `activeKey`, `navOptions`, and `onNav`.

4. Keep the organization guard and `returnToOverview`.

5. Remove CSS that only applies to the old side menu:

```css
.aicc-title h1 { ... }
.aicc-content-frame { ... }
.aicc-content-frame :deep(> *) { ... }
```

6. Keep and adjust header/brand CSS so the header remains compact:

```css
.aicc-console-layout {
  height: 100vh;
  background: var(--color-bg);
}

.aicc-console-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 64px;
  padding: 0 20px;
  background: var(--color-surface);
}

.aicc-brand {
  display: flex;
  align-items: center;
  gap: 12px;
}
```

- [ ] **Step 4: Run layout and workspace tests**

Run:

```bash
cd web && npm test -- AICCConsoleLayout.spec.ts AICCConsoleWorkspace.spec.ts --run
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/layouts/AICCConsoleLayout.vue web/src/layouts/AICCConsoleLayout.spec.ts
git commit -m "refactor(aicc): 移除工作台左侧模块菜单" -m "将 AICC 工作台外壳改为顶部品牌栏加启用后工作区，模块导航不再使用左侧菜单承载。"
```

---

### Task 4: Make AICCManagerPage Consume Shared Agent Context

**Files:**
- Modify: `web/src/pages/aicc/AICCManagerPage.vue`

- [ ] **Step 1: Write a focused regression test if a spec file exists**

There is currently no `AICCManagerPage.spec.ts`. Do not create a broad snapshot test for this large page. This task is verified through `AICCConsoleWorkspace.spec.ts`, route tests, typecheck, and browser verification.

- [ ] **Step 2: Replace local agent state with injected context**

In `web/src/pages/aicc/AICCManagerPage.vue`, import the context:

```ts
import { useRequiredAICCConsoleContext } from '@/pages/aicc/aiccConsoleContext'
```

Replace:

```ts
const selectedAgentId = ref<string | undefined>()
const agentsQuery = useAICCAgentsQuery()
const agents = computed(() => agentsQuery.data.value ?? [])
const selectedAgent = computed(() => agents.value.find(agent => agent.id === selectedAgentId.value))
```

with:

```ts
const consoleContext = useRequiredAICCConsoleContext()
const selectedAgentId = consoleContext.selectedAgentId
const agents = consoleContext.agents
const selectedAgent = consoleContext.selectedAgent
const agentsLoading = consoleContext.agentsLoading
const agentsError = consoleContext.agentsError
```

Remove `useAICCAgentsQuery` from the imports.

- [ ] **Step 3: Replace local agent selection functions**

Update `selectAgent`:

```ts
function selectAgent(agentId?: string) {
  consoleContext.selectAgent(agentId)
}
```

Update `startCreate`:

```ts
function startCreate() {
  consoleContext.startCreateAgent()
  delete form.id
  Object.assign(form, emptyForm())
  feedback.value = ''
}
```

Delete the watcher that auto-selects the first agent:

```ts
watch(
  agents,
  (items) => {
    if (!selectedAgentId.value && items.length > 0) {
      selectedAgentId.value = items[0].id
    }
  },
  { immediate: true },
)
```

The workspace now owns first-agent selection.

- [ ] **Step 4: Update template references to loading/error**

Replace:

```vue
<div v-if="agentsQuery.isLoading.value" class="state-text">{{ t('aicc.manager.loading') }}</div>
<p v-else-if="agentsQuery.error.value" class="state-text danger">{{ agentsQuery.error.value.message }}</p>
```

with:

```vue
<div v-if="agentsLoading" class="state-text">{{ t('aicc.manager.loading') }}</div>
<p v-else-if="agentsError" class="state-text danger">{{ agentsError.message }}</p>
```

Replace:

```vue
<div v-if="!agentsQuery.isLoading.value && agents.length === 0" class="empty-block">
```

with:

```vue
<div v-if="!agentsLoading && agents.length === 0" class="empty-block">
```

- [ ] **Step 5: Run typecheck**

Run:

```bash
cd web && npm run typecheck
```

Expected: PASS. If it fails because a computed ref was used incorrectly in template or script, fix the specific type error without reintroducing local agent query state.

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/aicc/AICCManagerPage.vue
git commit -m "refactor(aicc): 共享工作台智能体选择状态" -m "让 AICC 内容页复用工作台外壳提供的智能体列表和当前智能体状态，保证顶部上下文条与内容区一致。"
```

---

### Task 5: Reduce Duplicate Internal Navigation In Manager Page

**Files:**
- Modify: `web/src/pages/aicc/AICCManagerPage.vue`

- [ ] **Step 1: Identify duplicate internal tabs**

The current `n-tabs` contains:

```vue
<n-tabs v-model:value="activeSection" type="segment" animated class="aicc-tabs">
  <n-tab-pane name="config" :tab="t('aicc.manager.tabs.config')">
  <n-tab-pane name="sessions" :tab="t('aicc.manager.tabs.sessions')">
  <n-tab-pane name="leads" :tab="t('aicc.manager.tabs.leads')">
  <n-tab-pane name="analytics" :tab="t('aicc.manager.tabs.analytics')">
</n-tabs>
```

After top module navigation exists, these tabs duplicate module navigation for sessions/leads/analytics.

- [ ] **Step 2: Move module sections out of internal tabs**

Keep `activeSection` for routing behavior, but render sections with `v-if` instead of user-visible tabs:

```vue
<section v-if="activeSection === 'config'" class="aicc-section-panel">
  <!-- existing config tab pane content -->
</section>

<section v-else-if="activeSection === 'sessions'" class="aicc-section-panel">
  <AICCSessionsPage :agent-id="selectedAgentId" />
</section>

<section v-else-if="activeSection === 'leads'" class="aicc-section-panel">
  <AICCLeadsPage />
</section>

<section v-else-if="activeSection === 'analytics'" class="aicc-section-panel">
  <AICCAnalyticsPage :agent-id="selectedAgentId" :agent-count="agents.length" :active-agent-count="activeAgentCount" />
</section>
```

Do not keep visible tabs for `sessions`, `leads`, or `analytics`. For `settings` and `knowledge`, `sectionToTab()` can continue mapping to `config` and use `scrollKnowledgePanelIntoView()`.

- [ ] **Step 3: Remove hero duplication**

Replace the current hero:

```vue
<section class="aicc-hero">
  ...
</section>
```

with a compact content header inside the page:

```vue
<section class="aicc-page-heading">
  <div>
    <p class="eyebrow">{{ currentSectionEyebrow }}</p>
    <h2>{{ currentSectionTitle }}</h2>
    <p>{{ currentSectionDescription }}</p>
  </div>
  <n-button type="primary" @click="startCreate">
    <template #icon><Plus :size="16" /></template>
    {{ t('aicc.manager.createAgent') }}
  </n-button>
</section>
```

Add computed values:

```ts
const currentSectionEyebrow = computed(() => t(`aicc.manager.sections.${activeSection.value}.eyebrow`))
const currentSectionTitle = computed(() => t(`aicc.manager.sections.${activeSection.value}.title`))
const currentSectionDescription = computed(() => t(`aicc.manager.sections.${activeSection.value}.description`))
```

- [ ] **Step 4: Add section i18n keys**

In `web/src/i18n/locales/zh/aicc.ts`, add under `manager`:

```ts
    sections: {
      config: {
        eyebrow: 'RECEPTION',
        title: '接待台',
        description: '维护智能体配置、投放入口、知识范围和运营策略。',
      },
      sessions: {
        eyebrow: 'SESSIONS',
        title: '会话',
        description: '查看访客会话、消息详情、来源和跟进状态。',
      },
      leads: {
        eyebrow: 'LEADS',
        title: '线索',
        description: '查看访客留资、未读线索和转化情况。',
      },
      analytics: {
        eyebrow: 'ANALYTICS',
        title: '分析',
        description: '查看会话趋势、活跃智能体和留资转化。',
      },
    },
```

Add the English equivalent in `web/src/i18n/locales/en/aicc.ts`.

- [ ] **Step 5: Run focused tests and typecheck**

Run:

```bash
cd web && npm test -- AICCConsoleWorkspace.spec.ts AICCConsoleLayout.spec.ts i18n/locales/completeness.spec.ts i18n/locales/aicc.spec.ts --run
cd web && npm run typecheck
```

Expected: tests PASS and typecheck PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/aicc/AICCManagerPage.vue web/src/i18n/locales/zh/aicc.ts web/src/i18n/locales/en/aicc.ts
git commit -m "refactor(aicc): 收敛工作台内部重复导航" -m "移除内容页内与顶部模块导航重复的分段 tabs，并改用路由驱动的模块内容区。"
```

---

### Task 6: Full Verification And Browser Regression

**Files:**
- No code changes expected.

- [ ] **Step 1: Run full focused automated verification**

Run:

```bash
cd web && npm test -- RoleAwareHome.spec.ts DashboardLayout.spec.ts AICCConsoleLayout.spec.ts AICCConsoleWorkspace.spec.ts i18n/locales/completeness.spec.ts i18n/locales/aicc.spec.ts --run
```

Expected: all test files PASS.

- [ ] **Step 2: Run typecheck**

Run:

```bash
cd web && npm run typecheck
```

Expected: PASS.

- [ ] **Step 3: Run production build**

Run:

```bash
cd web && npm run build
```

Expected: build completes. Existing chunk-size warnings are acceptable; new errors are not.

- [ ] **Step 4: Browser verification with Chrome DevTools MCP**

Use `http://ocm.localhost:5173` if the Vite dev server is the reachable local browser target.

Verify:

1. Log in as enterprise admin:
   - 企业标识：`test-org`
   - 账号：`e2e-org-admin`
   - 密码：`e2e-pass-123`
2. On overview page, click the AICC subsystem entry.
3. Confirm `/aicc-console` renders:
   - no left module sider,
   - top module navigation,
   - current-agent context bar,
   - content-area agent list,
   - current module content.
4. Click each module:
   - 接待台 -> `/aicc-console`
   - 会话 -> `/aicc-console/sessions`
   - 线索 -> `/aicc-console/leads`
   - 知识库 -> `/aicc-console/knowledge`
   - 分析 -> `/aicc-console/analytics`
   - 设置 -> `/aicc-console/settings`
5. Click a different agent in the content-area list.
6. Confirm the current-agent context bar updates to the selected agent.
7. Click 返回概览 and confirm it returns to `/`.
8. Check console messages for error/warn.
9. Check AICC-related XHR/fetch responses are 200.

- [ ] **Step 5: Commit only if verification required code changes**

If Step 1-4 required fixes, commit them with a focused message:

```bash
git add <fixed-files>
git commit -m "fix(aicc): 修复工作台导航回归问题" -m "根据自动化测试和真实浏览器验证修复 AICC 工作台导航重构后的交互问题。"
```

If no code changes were required, do not create an empty commit.

---

## Self-Review

- Spec coverage:
  - Top module navigation: Task 2 and Task 3.
  - Current-agent context bar: Task 2 and Task 4.
  - Left module menu removal: Task 3.
  - Content-area agent list remains: Task 4 and Task 5.
  - Deep links preserved: Task 2, Task 3, Task 6.
  - i18n coverage: Task 2 and Task 5.
  - Browser verification: Task 6.
- Placeholder scan:
  - No unfinished markers or unspecified implementation steps remain.
- Type consistency:
  - Shared context types are defined in Task 1 before Task 2/4 consume them.
  - `selectedAgentId`, `agents`, and `selectedAgent` are shared through Vue refs/computed refs consistently.
