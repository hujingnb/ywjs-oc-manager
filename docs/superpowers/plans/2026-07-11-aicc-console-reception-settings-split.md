# AICC Console Reception Settings Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 拆分 AICC 工作台「接待台」和「设置」的页面职责，避免两个左侧菜单展示同一套内容。

**Architecture:** 继续复用 `AICCManagerPage` 的数据查询和保存逻辑，但在模板层按 `initialSection` 分成接待台首页和设置页。接待台只展示运行状态、公开入口和嵌入代码等投放概览；设置页展示智能体资料、运营安全、知识范围和留资字段配置。

**Tech Stack:** Vue 3、TypeScript、Naive UI、Vue I18n、Vitest、Chrome DevTools MCP。

---

### Task 1: Add Regression Coverage

**Files:**
- Modify: `web/src/pages/aicc/AICCManagerPage.spec.ts`

- [ ] **Step 1: Write failing tests**

Add tests that mount `AICCManagerPage` with `initialSection: 'reception'` and `initialSection: 'settings'`.

Expected assertions:
- Reception page contains `公开链接` and `嵌入占位`.
- Reception page does not contain `智能体名称`, `单会话消息上限`, or `访客留资`.
- Settings page contains `智能体名称`, `单会话消息上限`, `知识库范围`, and `访客留资`.
- Settings page does not contain `嵌入占位`.

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
cd web && npm test -- AICCManagerPage.spec.ts
```

Expected: tests fail because reception and settings still render the same config content.

### Task 2: Split Template Sections

**Files:**
- Modify: `web/src/pages/aicc/AICCManagerPage.vue`
- Modify: `web/src/i18n/locales/zh/aicc.ts`
- Modify: `web/src/i18n/locales/en/aicc.ts`

- [ ] **Step 1: Update section routing**

Keep `sectionToTab()` mapping, but add computed guards:

```ts
const isReceptionRoute = computed(() => currentRouteSection.value === 'reception')
const isSettingsRoute = computed(() => currentRouteSection.value === 'settings')
```

- [ ] **Step 2: Move panels**

Render:
- Reception: status grid, public link, QR code, widget snippet.
- Settings: agent form, operations settings, knowledge scope, lead fields.

Do not add new backend calls or change API payloads.

- [ ] **Step 3: Update copy**

Change section descriptions so reception describes running/delivery overview and settings describes rules/configuration.

### Task 3: Verify and Commit

**Files:**
- Modified files from previous tasks.

- [ ] **Step 1: Run unit tests**

```bash
cd web && npm test -- AICCManagerPage.spec.ts AICCConsoleWorkspace.spec.ts
```

- [ ] **Step 2: Run browser verification**

Use Chrome DevTools MCP against `http://ocm.localhost/aicc-console` and `http://ocm.localhost/aicc-console/settings`.

Expected:
- Left menu active state changes correctly.
- Reception page shows public entry/embed overview and not settings form.
- Settings page shows configuration forms and not embed overview.

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/plans/2026-07-11-aicc-console-reception-settings-split.md web/src/pages/aicc/AICCManagerPage.vue web/src/pages/aicc/AICCManagerPage.spec.ts web/src/i18n/locales/zh/aicc.ts web/src/i18n/locales/en/aicc.ts
git commit -m "fix(aicc): 拆分接待台和设置页职责" -m "将 AICC 工作台接待台改为投放与运行概览，设置页保留智能体资料、运营策略、知识范围和留资字段配置，避免两个菜单展示同一页面。" -m "补充前端回归测试，并通过真实浏览器验证两个菜单的内容和选中态。"
```
