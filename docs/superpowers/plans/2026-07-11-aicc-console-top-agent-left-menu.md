# AICC Console Top-Agent Left-Menu Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Adjust `/aicc-console` so the top bar is the only agent selector, the left side is the module menu, and the content area no longer repeats the agent list.

**Architecture:** Keep `AICCConsoleLayout.vue` as the access-gated outer shell. Update `AICCConsoleWorkspace.vue` to own the top agent selector and left module menu while continuing to provide selected-agent context to routed pages. Update `AICCManagerPage.vue` to consume only the shared selected agent and render module content without its own agent list.

**Tech Stack:** Vue 3 `<script setup>`, Vue Router, Vue I18n, Naive UI, Vitest, Vue Test Utils, existing `useAICC` hooks and global CSS variables in `web/src/styles/base.css`.

## Global Constraints

- Use main-site colors through existing CSS variables: `--color-brand`, `--color-brand-soft`, `--color-page`, `--color-surface`, `--color-border`, `--color-divider`.
- Do not introduce an independent teal/green AICC theme.
- All user-visible text must use the existing i18n system.
- Do not change backend, database migrations, OpenAPI, or generated API types.
- Every completed stage must be committed with a Chinese Conventional Commit message.
- Before final delivery, run automated tests, typecheck, build, and real-browser verification through `ocm.localhost`.

---

## File Structure

- Modify `web/src/layouts/AICCConsoleWorkspace.vue`
  - Move module navigation from the top into a left rail.
  - Keep the top area as the single agent selector/context area.
  - Use main-site CSS variables for brand and active states.
- Modify `web/src/layouts/AICCConsoleWorkspace.spec.ts`
  - Assert the left menu exists, includes all module routes, and clicking each route calls `router.push`.
  - Assert the top agent selector/context still provides the selected-agent context.
- Modify `web/src/pages/aicc/AICCManagerPage.vue`
  - Remove the content-area agent list from the page layout.
  - Keep selected-agent behavior through `useRequiredAICCConsoleContext()`.
  - Ensure the new-agent flow still clears the form when `selectedAgent` becomes undefined.
- Add or modify focused tests for `AICCManagerPage.vue` if no current test covers content-area agent-list removal and new-agent reset.
- Modify i18n only if new labels are introduced. Prefer reusing existing keys.

---

### Task 1: Move Module Navigation to the Left Rail

**Files:**
- Modify: `web/src/layouts/AICCConsoleWorkspace.vue`
- Modify: `web/src/layouts/AICCConsoleWorkspace.spec.ts`

**Interfaces:**
- Consumes: `navItems: WorkspaceNavItem[]`, `activeKey`, `navigateTo(path: string)`, selected-agent context state.
- Produces: A workspace shell with top agent selector and left module menu. Routed pages still receive `AICCConsoleContextKey`.

- [ ] **Step 1: Write/update failing workspace layout test**

Update `web/src/layouts/AICCConsoleWorkspace.spec.ts` so the first test checks left-menu semantics and all route clicks:

```ts
// 覆盖最终工作台结构：顶部只做智能体选择，左侧菜单负责模块切换。
it('renders the module menu in the left rail and pushes all console routes', async () => {
  const wrapper = mountWorkspace()

  const menu = wrapper.find('[data-test="workspace-module-menu"]')
  expect(menu.exists()).toBe(true)
  expect(wrapper.find('[data-test="workspace-agent-bar"]').exists()).toBe(true)

  const items = navItems(wrapper)
  expect(items.map(item => item.text())).toEqual(['接待台', '会话', '线索', '知识库', '分析', '设置'])
  expect(items.map(item => item.attributes('href'))).toEqual([
    '/aicc-console',
    '/aicc-console/sessions',
    '/aicc-console/leads',
    '/aicc-console/knowledge',
    '/aicc-console/analytics',
    '/aicc-console/settings',
  ])

  for (const item of items) {
    await item.trigger('click')
  }

  expect(routerPush.mock.calls.map(call => call[0])).toEqual([
    '/aicc-console',
    '/aicc-console/sessions',
    '/aicc-console/leads',
    '/aicc-console/knowledge',
    '/aicc-console/analytics',
    '/aicc-console/settings',
  ])
})
```

The helper `navItems()` should continue to query `[data-test="workspace-nav-item"]`.

- [ ] **Step 2: Run the focused test and verify it fails**

Run:

```bash
cd web && npm test -- AICCConsoleWorkspace.spec.ts --run
```

Expected before implementation: FAIL because `[data-test="workspace-module-menu"]` and `[data-test="workspace-agent-bar"]` do not exist.

- [ ] **Step 3: Update `AICCConsoleWorkspace.vue` template**

Change the template shape to:

```vue
<section class="aicc-console-workspace" :aria-label="t('aicc.console.navLabel')">
  <header class="aicc-agent-context" data-test="workspace-agent-bar">
    <!-- existing current-agent identity, meta, select, and create button stay here -->
  </header>

  <div class="aicc-workspace-shell">
    <nav class="aicc-workspace-nav" data-test="workspace-module-menu" :aria-label="t('aicc.console.navLabel')">
      <a
        v-for="item in navItems"
        :key="item.path"
        class="aicc-workspace-nav-item"
        :class="{ active: activeKey === item.path }"
        :href="item.path"
        data-test="workspace-nav-item"
        @click.prevent="navigateTo(item.path)"
      >
        <component :is="item.icon" :size="16" />
        <span>{{ t(item.labelKey) }}</span>
      </a>
    </nav>

    <main class="aicc-workspace-content">
      <RouterView />
    </main>
  </div>
</section>
```

Do not change `provide(AICCConsoleContextKey, ...)`, `selectAgent()`, or `startCreateAgent()`.

- [ ] **Step 4: Update `AICCConsoleWorkspace.vue` styles**

Replace the top-nav flex styles with a left rail:

```css
.aicc-console-workspace {
  display: flex;
  min-width: 0;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  gap: 12px;
}

.aicc-workspace-shell {
  display: grid;
  min-width: 0;
  min-height: 0;
  flex: 1;
  grid-template-columns: 212px minmax(0, 1fr);
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
  overflow: hidden;
}

.aicc-workspace-nav {
  display: flex;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
  gap: 4px;
  padding: 12px;
  border-right: 1px solid var(--color-divider);
  background: var(--color-surface-muted);
}

.aicc-workspace-nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 40px;
  padding: 0 12px;
  border-radius: 6px;
  color: var(--color-text-secondary);
  font-size: 14px;
  font-weight: 600;
  letter-spacing: 0;
  text-decoration: none;
}

.aicc-workspace-nav-item:hover,
.aicc-workspace-nav-item.active {
  background: var(--color-brand-soft);
  box-shadow: inset 3px 0 0 var(--color-brand);
  color: var(--color-brand-text);
}

.aicc-agent-context {
  display: grid;
  grid-template-columns: minmax(220px, 1fr) minmax(260px, 2fr) minmax(240px, auto);
  gap: 16px;
  align-items: center;
  min-height: 58px;
  padding: 10px 14px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
}

.aicc-agent-select {
  width: min(320px, 100%);
}

.aicc-workspace-content {
  display: flex;
  min-width: 0;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  padding: 16px;
  overflow: auto;
}

@media (max-width: 760px) {
  .aicc-workspace-shell {
    grid-template-columns: 1fr;
  }

  .aicc-workspace-nav {
    flex-direction: row;
    overflow-x: auto;
    border-right: 0;
    border-bottom: 1px solid var(--color-divider);
  }

  .aicc-workspace-nav-item {
    flex: 0 0 auto;
  }
}
```

Keep existing identity/meta/action responsive rules and adjust only if they conflict with this structure.

- [ ] **Step 5: Run focused workspace test**

Run:

```bash
cd web && npm test -- AICCConsoleWorkspace.spec.ts --run
```

Expected: PASS.

- [ ] **Step 6: Commit Task 1**

Run:

```bash
git add web/src/layouts/AICCConsoleWorkspace.vue web/src/layouts/AICCConsoleWorkspace.spec.ts
git commit -m "refactor(aicc): 将工作台模块菜单移到左侧" -m "顶部区域收敛为唯一智能体选择与状态摘要，左侧承载接待台、会话、线索、知识库、分析和设置菜单。"
```

---

### Task 2: Remove the Content-Area Agent List

**Files:**
- Modify: `web/src/pages/aicc/AICCManagerPage.vue`
- Create or modify: `web/src/pages/aicc/AICCManagerPage.spec.ts`

**Interfaces:**
- Consumes: `useRequiredAICCConsoleContext()` from `aiccConsoleContext.ts`.
- Produces: Manager page content that uses the top selected agent only and has no content-area agent list.

- [ ] **Step 1: Inspect whether an `AICCManagerPage` spec already exists**

Run:

```bash
rg --files web/src/pages/aicc | rg 'AICCManagerPage.*spec'
```

If no file exists, create `web/src/pages/aicc/AICCManagerPage.spec.ts`.

- [ ] **Step 2: Add a focused regression test**

The test must mount `AICCManagerPage.vue` with a provided `AICCConsoleContextKey`, stub Naive UI components, and assert:

- no `[data-test="manager-agent-list"]` exists,
- selected agent name from the context appears in the form,
- calling `startCreateAgent()` or setting the selected agent to undefined clears the name field.

Use this pattern for the context:

```ts
const selectedAgentId = ref<string | undefined>('agent-sales')
const selectedAgent = computed(() => agents.value.find(agent => agent.id === selectedAgentId.value))

provide(AICCConsoleContextKey, {
  agents,
  selectedAgentId: computed(() => selectedAgentId.value),
  selectedAgent,
  agentsLoading: computed(() => false),
  agentsError: computed(() => null),
  selectAgent: (agentId?: string) => { selectedAgentId.value = agentId },
  startCreateAgent: () => { selectedAgentId.value = undefined },
})
```

Add a Chinese comment directly above each test explaining the covered business case.

- [ ] **Step 3: Run the focused test and verify it fails**

Run:

```bash
cd web && npm test -- AICCManagerPage.spec.ts --run
```

Expected before implementation: FAIL because the content-area agent list still exists or the test file is new and points at the current behavior.

- [ ] **Step 4: Remove content-area agent list from `AICCManagerPage.vue`**

Remove the markup that renders the agent list column from the main page content. If the current template has a grid like:

```vue
<aside class="aicc-agent-list">...</aside>
<section class="aicc-manager-panel">...</section>
```

replace it with the module panel only:

```vue
<section class="aicc-manager-main">
  <section class="aicc-manager-panel">
    <!-- existing selected-agent form/module content -->
  </section>
</section>
```

Keep calls to `selectAgent()` only where still needed by explicit controls. The top selector remains the normal selection entry.

Add `data-test="manager-agent-list"` only if needed for the failing test before removal; after implementation the selector must not exist.

- [ ] **Step 5: Remove obsolete list styles**

Delete styles that only support the content-area agent list, such as:

- `.aicc-agent-list`
- `.aicc-agent-list-item`
- `.aicc-agent-list-item.active`

Keep layout styles needed by the module panel.

- [ ] **Step 6: Run focused manager test**

Run:

```bash
cd web && npm test -- AICCManagerPage.spec.ts --run
```

Expected: PASS.

- [ ] **Step 7: Commit Task 2**

Run:

```bash
git add web/src/pages/aicc/AICCManagerPage.vue web/src/pages/aicc/AICCManagerPage.spec.ts
git commit -m "refactor(aicc): 移除内容区智能体列表" -m "智能体选择统一放到工作台顶部，接待台内容区只展示当前智能体的配置和模块内容。"
```

---

### Task 3: Verify, Browser-Test, and Fix Regressions

**Files:**
- Modify only files needed to fix issues found by tests or browser verification.

**Interfaces:**
- Consumes: Task 1 and Task 2 output.
- Produces: Verified AICC console layout with clean worktree.

- [ ] **Step 1: Run focused tests**

Run:

```bash
cd web && npm test -- RoleAwareHome.spec.ts DashboardLayout.spec.ts AICCConsoleLayout.spec.ts AICCConsoleWorkspace.spec.ts AICCManagerPage.spec.ts i18n/locales/completeness.spec.ts i18n/locales/aicc.spec.ts --run
```

Expected: PASS.

- [ ] **Step 2: Run typecheck**

Run:

```bash
cd web && npm run typecheck
```

Expected: PASS.

- [ ] **Step 3: Run build**

Run:

```bash
cd web && npm run build
```

Expected: PASS. Existing Vite chunk-size or dynamic-import warnings are acceptable if there are no errors.

- [ ] **Step 4: Real-browser verification**

Use Chrome DevTools MCP against `http://ocm.localhost:5173` with local enterprise admin:

- org code: `test-org`
- username: `e2e-org-admin`
- password: `e2e-pass-123`

Verify:

- Overview page shows AICC subsystem entry; main dashboard left menu does not include AICC.
- Clicking the overview entry opens `/aicc-console`.
- Top bar shows the agent selector, current-agent summary, and new-agent button.
- Left rail shows 接待台、会话、线索、知识库、分析、设置.
- Content area has no repeated intelligent-agent list.
- Selecting a different top agent updates content.
- Clicking 新建智能体 clears the selected-agent form without saving.
- Each left menu item navigates to the expected route.
- Browser console has no JavaScript error/warn.
- AICC XHR/fetch requests return 200.

- [ ] **Step 5: Commit fixes if any**

If browser or tests reveal defects, fix them and commit:

```bash
git add <changed-files>
git commit -m "fix(aicc): 修正工作台左侧菜单验证问题" -m "根据自动化测试和真实浏览器验证结果修正布局或交互回归。"
```

- [ ] **Step 6: Final clean check**

Run:

```bash
git status --short
```

Expected: no output.
