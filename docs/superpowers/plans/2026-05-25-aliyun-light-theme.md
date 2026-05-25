# Aliyun Light Theme Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `web/` 前端从深蓝青紫暗色风格切换为阿里云控制台式浅色工作台风格。

**Architecture:** 先把 Naive UI 和全局 CSS 收敛到统一浅色 token，再按布局、通用组件、业务图表、剩余页面 fallback 逐层清理旧色。实现只修改样式、theme overrides、ECharts 配色和少量 class，不触碰路由、权限、接口和业务状态。

**Tech Stack:** Vue 3, Vite, TypeScript, Naive UI, ECharts, Pinia, Vitest, Playwright/Chromium for browser verification.

---

## File Structure

- Modify: `web/src/App.vue` - 切换 Naive UI 到默认浅色主题，并配置阿里云风格 theme overrides。
- Modify: `web/src/styles/base.css` - 定义全局 CSS 色彩 token，替换深色背景、面板、按钮、表格、表单和登录页样式。
- Modify: `web/src/layouts/DashboardLayout.vue` - 调整侧栏品牌、footer、退出按钮和菜单周边样式。
- Modify: `web/src/pages/login/LoginPage.vue` - 仅在需要时补 class，登录行为不变。
- Modify: `web/src/components/DataTableList.vue` - 让列表标题、说明和卡片使用全局浅色 token。
- Modify: `web/src/components/ConfirmActionModal.vue` - 删除深色内联文字色，使用 class。
- Modify: `web/src/components/AuthChallengeRenderer.vue` - 二维码挑战中的验证码块改成浅色控制台样式。
- Modify: `web/src/components/ResourceTrendChart.vue` - 确认复用图表使用全局浅色 token 和蓝/橙系列色。
- Modify: `web/src/pages/platform/ConsolePage.vue` - 平台控制台统计卡和 ECharts 配色切浅色。
- Modify: `web/src/pages/org/OrgConsolePage.vue` - 组织控制台统计卡和 ECharts 配色切浅色。
- Modify: `web/src/pages/usage/UsageSummary.vue` - SVG 趋势图从暗色面板切为白底浅色图表。
- Modify: `web/src/pages/audit/AuditLogsPage.vue`, `web/src/pages/apps/AppAuditTab.vue` - 审计列渲染内联颜色切换为浅色 token。
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`, `web/src/pages/apps/AppKnowledgeTab.vue`, `web/src/pages/apps/AppWorkspaceTab.vue` - 文件链接和说明文字切浅色 token。
- Modify: `web/src/pages/apps/AppDetailPage.vue`, `web/src/pages/apps/AppCronTab.vue`, `web/src/pages/apps/AppKanbanTab.vue`, `web/src/pages/apps/kanban/*.vue`, `web/src/pages/apps/cron/*.vue` - 清理深色 fallback。
- Modify: `web/src/pages/runtime-nodes/*.vue`, `web/src/pages/dashboard/RoleAwareHome.vue`, `web/src/pages/org/CreateMemberPage.vue` - 清理旧色 fallback 和局部卡片样式。

## Shared Color Contract

Use these exact values throughout the implementation:

```ts
const BRAND_COLOR = '#ff6a00'
const BRAND_HOVER = '#ff8126'
const BRAND_PRESSED = '#e65f00'
const INFO_COLOR = '#1677ff'
const SUCCESS_COLOR = '#16a34a'
const WARNING_COLOR = '#f59e0b'
const ERROR_COLOR = '#d93026'
const TEXT_PRIMARY = '#1f2329'
const TEXT_SECONDARY = '#6b7280'
const TEXT_TERTIARY = '#8a94a6'
const BORDER_COLOR = '#e5e7eb'
const DIVIDER_COLOR = '#edf0f5'
const PAGE_BG = '#f5f7fa'
const SURFACE_BG = '#ffffff'
```

CSS variable names to add in `web/src/styles/base.css`:

```css
:root {
  --color-brand: #ff6a00;
  --color-brand-hover: #ff8126;
  --color-brand-pressed: #e65f00;
  --color-info: #1677ff;
  --color-success: #16a34a;
  --color-warning: #f59e0b;
  --color-danger: #d93026;
  --color-text-primary: #1f2329;
  --color-text-secondary: #6b7280;
  --color-text-tertiary: #8a94a6;
  --color-border: #e5e7eb;
  --color-divider: #edf0f5;
  --color-page: #f5f7fa;
  --color-surface: #ffffff;
  --color-surface-muted: #fbfcfd;
  --color-brand-soft: #fff4ed;
  --color-info-soft: #edf6ff;
  color: var(--color-text-primary);
  background: var(--color-page);
  font-family:
    Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-synthesis: none;
  text-rendering: optimizeLegibility;
}
```

---

### Task 1: Convert Naive UI Theme To Aliyun Light Tokens

**Files:**
- Modify: `web/src/App.vue`

- [ ] **Step 1: Run the pre-change dark theme check**

Run:

```bash
rtk rg -n "darkTheme|#00F0FF|#0A0E27|#7B2EDA|rgba\\(0,240,255" web/src/App.vue
```

Expected: output includes the current `darkTheme` import, `:theme="darkTheme"`, and cyan/dark theme values.

- [ ] **Step 2: Replace the config provider template**

In `web/src/App.vue`, replace:

```vue
<NConfigProvider :theme="darkTheme" :theme-overrides="themeOverrides">
```

with:

```vue
<NConfigProvider :theme-overrides="themeOverrides">
```

- [ ] **Step 3: Replace the Naive UI imports**

In `web/src/App.vue`, replace:

```ts
import { darkTheme, type GlobalThemeOverrides } from 'naive-ui'
import { NConfigProvider, NMessageProvider } from 'naive-ui'
```

with:

```ts
import type { GlobalThemeOverrides } from 'naive-ui'
import { NConfigProvider, NMessageProvider } from 'naive-ui'
```

- [ ] **Step 4: Replace `themeOverrides`**

Replace the entire `themeOverrides` object in `web/src/App.vue` with:

```ts
const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#ff6a00',
    primaryColorHover: '#ff8126',
    primaryColorPressed: '#e65f00',
    primaryColorSuppl: '#ff6a00',
    infoColor: '#1677ff',
    infoColorHover: '#4096ff',
    infoColorPressed: '#0958d9',
    successColor: '#16a34a',
    warningColor: '#f59e0b',
    errorColor: '#d93026',
    bodyColor: '#f5f7fa',
    cardColor: '#ffffff',
    modalColor: '#ffffff',
    popoverColor: '#ffffff',
    tableColor: '#ffffff',
    tableColorStriped: '#fbfcfd',
    borderColor: '#e5e7eb',
    dividerColor: '#edf0f5',
    textColorBase: '#1f2329',
    textColor1: '#1f2329',
    textColor2: '#4b5563',
    textColor3: '#6b7280',
    inputColor: '#ffffff',
    inputColorDisabled: '#f3f5f8',
    placeholderColor: '#8a94a6',
  },
  Layout: {
    siderColor: '#ffffff',
    headerColor: '#ffffff',
    footerColor: '#ffffff',
    color: '#f5f7fa',
  },
  Menu: {
    itemTextColor: '#4b5563',
    itemTextColorHover: '#ff6a00',
    itemTextColorActive: '#ff6a00',
    itemTextColorActiveHover: '#ff6a00',
    itemColorActive: '#fff4ed',
    itemColorActiveHover: '#fff4ed',
    itemColorHover: '#f5f7fa',
    borderColorActive: '#ff6a00',
  },
  DataTable: {
    thColor: '#fbfcfd',
    tdColor: '#ffffff',
    tdColorHover: '#f8fafc',
    borderColor: '#edf0f5',
    thTextColor: '#6b7280',
  },
  Card: {
    borderColor: '#e5e7eb',
    color: '#ffffff',
  },
  Button: {
    borderRadiusMedium: '4px',
    borderRadiusSmall: '4px',
  },
}
```

- [ ] **Step 5: Run typecheck for theme syntax**

Run:

```bash
cd web && npm run typecheck
```

Expected: command exits 0.

- [ ] **Step 6: Commit theme conversion**

Run:

```bash
rtk git add web/src/App.vue
rtk git commit -m "feat(theme): 切换前端浅色主题" -m "将 Naive UI 从暗色主题切换为默认浅色主题，并配置阿里云风格的橙色主色、浅色表格和布局 token。"
```

Expected: commit succeeds with only `web/src/App.vue` staged.

---

### Task 2: Rebuild Global CSS Tokens And Base Surfaces

**Files:**
- Modify: `web/src/styles/base.css`

- [ ] **Step 1: Run the pre-change global CSS check**

Run:

```bash
rtk rg -n "#00F0FF|#0A0E27|#050817|#7B2EDA|rgba\\(0, 240, 255|rgba\\(123, 46, 218|radial-gradient|linear-gradient" web/src/styles/base.css
```

Expected: output includes old body background, brand mark gradient, nav active gradient, login radial gradients, and cyan borders.

- [ ] **Step 2: Replace the top-level root and body styles**

Replace the current `:root` and `body` blocks in `web/src/styles/base.css` with:

```css
:root {
  --color-brand: #ff6a00;
  --color-brand-hover: #ff8126;
  --color-brand-pressed: #e65f00;
  --color-info: #1677ff;
  --color-success: #16a34a;
  --color-warning: #f59e0b;
  --color-danger: #d93026;
  --color-text-primary: #1f2329;
  --color-text-secondary: #6b7280;
  --color-text-tertiary: #8a94a6;
  --color-border: #e5e7eb;
  --color-divider: #edf0f5;
  --color-page: #f5f7fa;
  --color-surface: #ffffff;
  --color-surface-muted: #fbfcfd;
  --color-brand-soft: #fff4ed;
  --color-info-soft: #edf6ff;
  color: var(--color-text-primary);
  background: var(--color-page);
  font-family:
    Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-synthesis: none;
  text-rendering: optimizeLegibility;
}

body {
  margin: 0;
  min-width: 320px;
  min-height: 100vh;
  background: var(--color-page);
}
```

- [ ] **Step 3: Replace base layout colors**

In `web/src/styles/base.css`, update these selectors to the exact declarations:

```css
.sidebar {
  display: flex;
  flex-direction: column;
  gap: 0;
  background: var(--color-surface);
  border-right: 1px solid var(--color-border);
}

.brand-block {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 20px 16px 16px;
  border-bottom: 1px solid var(--color-divider);
  min-height: 64px;
}

.brand-block span {
  color: var(--color-text-secondary);
  font-size: 12px;
}

.brand-mark {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border-radius: 6px;
  background: var(--color-brand);
  box-shadow: none;
  color: #ffffff;
  font-weight: 800;
  font-size: 14px;
  flex-shrink: 0;
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 40px;
  padding: 0 12px;
  border-radius: 6px;
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: all 0.15s;
}

.nav-item.active,
.nav-item:hover {
  color: var(--color-brand);
  background: var(--color-brand-soft);
  box-shadow: inset 3px 0 0 var(--color-brand);
}

.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-height: 64px;
  padding: 12px 24px;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-surface);
  backdrop-filter: none;
}
```

- [ ] **Step 4: Replace panel, metric, table, button, form, and state colors**

Use these exact color mappings inside `web/src/styles/base.css`:

```text
#FFFFFF -> var(--color-text-primary) for text declarations, #ffffff for button text on brand/danger backgrounds
#CBD6E5 -> var(--color-text-primary)
#8A94C6 -> var(--color-text-secondary)
#00F0FF -> var(--color-info)
#00FF88 -> var(--color-success)
#FFB800 -> var(--color-warning)
#FF3B5C -> var(--color-danger)
rgba(20, 28, 58, 0.8) -> var(--color-surface)
rgba(20, 28, 58, 0.9) -> var(--color-surface)
rgba(15, 21, 53, 0.8) -> var(--color-surface)
rgba(0, 240, 255, 0.2) -> var(--color-border)
rgba(0, 240, 255, 0.12) -> var(--color-divider)
```

Then make these selector-level replacements:

```css
.metric-card,
.panel {
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
  backdrop-filter: none;
}

.metric-card span,
.metric-card small {
  color: var(--color-text-secondary);
}

.metric-card strong {
  font-size: 28px;
  line-height: 1;
  color: var(--color-text-primary);
}

th,
td {
  padding: 12px 8px;
  border-bottom: 1px solid var(--color-divider);
  text-align: left;
  font-size: 14px;
  color: var(--color-text-primary);
}

th {
  color: var(--color-text-secondary);
  font-weight: 700;
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.3px;
}

.primary-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-height: 38px;
  padding: 0 16px;
  border: 1px solid var(--color-brand);
  border-radius: 4px;
  color: #ffffff;
  background: var(--color-brand);
  font-weight: 700;
  cursor: pointer;
  transition: all 0.15s;
}

.primary-button:hover {
  border-color: var(--color-brand-hover);
  background: var(--color-brand-hover);
  box-shadow: none;
}

.secondary-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-height: 34px;
  padding: 0 12px;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  color: var(--color-text-primary);
  background: var(--color-surface);
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s;
}

.secondary-button:hover {
  border-color: var(--color-brand);
  color: var(--color-brand);
  background: var(--color-brand-soft);
}

.data-table-link {
  cursor: pointer;
  color: var(--color-info);
}

.data-table-subtitle {
  display: block;
  color: var(--color-text-secondary);
  font-size: 12px;
}
```

- [ ] **Step 5: Replace login shell and form styles**

Replace the `.auth-shell`, `.login-form`, `.login-form label`, and `.login-form input` blocks with:

```css
.auth-shell {
  display: grid;
  min-height: 100vh;
  place-items: center;
  padding: 24px;
  background:
    linear-gradient(180deg, rgba(255, 106, 0, 0.05), transparent 220px),
    var(--color-page);
}

.login-form {
  display: grid;
  gap: 18px;
  padding: 32px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
  backdrop-filter: none;
  box-shadow: 0 12px 32px rgba(15, 23, 42, 0.08);
}

.login-form label {
  display: grid;
  gap: 8px;
  color: var(--color-text-secondary);
  font-weight: 600;
  font-size: 13px;
}

.login-form input {
  width: 100%;
  min-height: 42px;
  padding: 0 12px;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  background: var(--color-surface);
  color: var(--color-text-primary);
}
```

- [ ] **Step 6: Run the post-change global CSS check**

Run:

```bash
rtk rg -n "#00F0FF|#0A0E27|#050817|#7B2EDA|rgba\\(0, 240, 255|rgba\\(123, 46, 218|radial-gradient" web/src/styles/base.css
```

Expected: no output.

- [ ] **Step 7: Build after global CSS conversion**

Run:

```bash
cd web && npm run build
```

Expected: command exits 0 and produces `dist`.

- [ ] **Step 8: Commit global CSS conversion**

Run:

```bash
rtk git add web/src/styles/base.css
rtk git commit -m "feat(theme): 重建全局浅色样式" -m "将基础布局、面板、表格、按钮、表单和登录页样式切换为阿里云控制台式浅色 token。"
```

Expected: commit succeeds with only `web/src/styles/base.css` staged.

---

### Task 3: Polish Dashboard And Auth Layout Shells

**Files:**
- Modify: `web/src/layouts/DashboardLayout.vue`
- Modify: `web/src/pages/login/LoginPage.vue`

- [ ] **Step 1: Run the layout old-color check**

Run:

```bash
rtk rg -n "#00F0FF|#7B2EDA|#8A94C6|rgba\\(0, 240, 255|rgba\\(255, 255, 255" web/src/layouts/DashboardLayout.vue web/src/pages/login/LoginPage.vue
```

Expected: output includes logout inline color and brand mark gradient in `DashboardLayout.vue`.

- [ ] **Step 2: Replace logout button inline color with a class**

In `web/src/layouts/DashboardLayout.vue`, replace:

```vue
style="width: 100%; justify-content: flex-start; color: #8A94C6"
```

with:

```vue
class="logout-button"
style="width: 100%; justify-content: flex-start"
```

- [ ] **Step 3: Replace scoped layout styles**

In `web/src/layouts/DashboardLayout.vue`, replace the scoped style color declarations with:

```css
.brand-block {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  border-bottom: 1px solid var(--color-divider);
  min-height: 64px;
}

.brand-mark {
  width: 36px;
  height: 36px;
  border-radius: 6px;
  display: grid;
  place-items: center;
  background: var(--color-brand);
  box-shadow: none;
  color: #ffffff;
  font-size: 13px;
  font-weight: 800;
  flex-shrink: 0;
}

.logo-text strong { display: block; font-size: 15px; color: var(--color-text-primary); }
.logo-text span { display: block; font-size: 11px; color: var(--color-text-secondary); }

.sidebar-footer {
  padding: 12px 14px 16px;
  border-top: 1px solid var(--color-divider);
  background: var(--color-surface);
}

.logout-button {
  color: var(--color-text-secondary);
}

.logout-button:hover {
  color: var(--color-brand);
}
```

Keep the existing `.dashboard-page-frame` rules after these blocks.

- [ ] **Step 4: Add a login page brand class**

In `web/src/pages/login/LoginPage.vue`, replace:

```vue
<p class="eyebrow">Agent Runtime Manager</p>
```

with:

```vue
<p class="eyebrow login-eyebrow">Agent Runtime Manager</p>
```

Add this scoped style at the end of the file:

```vue
<style scoped>
.login-eyebrow {
  color: var(--color-brand);
}
</style>
```

- [ ] **Step 5: Run the layout post-check**

Run:

```bash
rtk rg -n "#00F0FF|#7B2EDA|#8A94C6|rgba\\(0, 240, 255" web/src/layouts/DashboardLayout.vue web/src/pages/login/LoginPage.vue
```

Expected: no output.

- [ ] **Step 6: Run layout unit tests**

Run:

```bash
cd web && npm run test -- --run src/layouts/DashboardLayout.spec.ts
```

Expected: command exits 0.

- [ ] **Step 7: Commit layout polish**

Run:

```bash
rtk git add web/src/layouts/DashboardLayout.vue web/src/pages/login/LoginPage.vue
rtk git commit -m "feat(theme): 调整后台和登录布局浅色风格" -m "更新后台侧栏、品牌区、退出按钮和登录页品牌色，使外壳与阿里云浅色控制台方向一致。"
```

Expected: commit succeeds with the two listed files staged.

---

### Task 4: Update Shared Components And Inline Render Colors

**Files:**
- Modify: `web/src/components/DataTableList.vue`
- Modify: `web/src/components/ConfirmActionModal.vue`
- Modify: `web/src/components/AuthChallengeRenderer.vue`
- Modify: `web/src/pages/audit/AuditLogsPage.vue`
- Modify: `web/src/pages/apps/AppAuditTab.vue`
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`
- Modify: `web/src/pages/apps/AppKnowledgeTab.vue`
- Modify: `web/src/pages/apps/AppWorkspaceTab.vue`
- Modify: `web/src/pages/org/CreateMemberPage.vue`

- [ ] **Step 1: Run the shared old-color check**

Run:

```bash
rtk rg -n "#00F0FF|#8A94C6|#CBD6E5|#FF3B5C|rgba\\(15, 21, 53|rgba\\(0, 240, 255|rgba\\(255, 255, 255, 0\\.64" web/src/components web/src/pages/audit web/src/pages/apps web/src/pages/knowledge web/src/pages/org/CreateMemberPage.vue
```

Expected: output includes `ConfirmActionModal.vue`, `AuthChallengeRenderer.vue`, audit render functions, knowledge link render functions, and selected app/org page styles.

- [ ] **Step 2: Update `DataTableList.vue` fallbacks**

Replace the two scoped style lines:

```css
.eyebrow { font-size: 12px; color: var(--color-text-secondary, #8A94C6); margin: 0 0 4px; }
.subtitle { font-size: 13px; color: var(--color-text-secondary, #8A94C6); margin: 4px 0 0; }
```

with:

```css
.eyebrow { font-size: 12px; color: var(--color-text-secondary, #6b7280); margin: 0 0 4px; }
.subtitle { font-size: 13px; color: var(--color-text-secondary, #6b7280); margin: 4px 0 0; }
```

- [ ] **Step 3: Update `ConfirmActionModal.vue` message color**

Replace:

```vue
<p style="margin: 0 0 16px; color: #CBD6E5">{{ message }}</p>
```

with:

```vue
<p class="confirm-message">{{ message }}</p>
```

Add this scoped style at the end of `ConfirmActionModal.vue`:

```vue
<style scoped>
.confirm-message {
  margin: 0 0 16px;
  color: var(--color-text-secondary);
}
</style>
```

- [ ] **Step 4: Update `AuthChallengeRenderer.vue` challenge styles**

Replace the `.challenge-code` and `.fallback-hint` blocks with:

```css
.challenge-code {
  margin: 0;
  padding: 16px;
  border: 1px dashed var(--color-border);
  border-radius: 6px;
  background: var(--color-info-soft);
  color: var(--color-info);
  font-size: 22px;
  font-weight: 800;
  letter-spacing: 4px;
}

.fallback-hint {
  font-size: 12px;
  color: var(--color-text-secondary);
  word-break: break-all;
}
```

Keep `.challenge-qr { background: #ffffff; }` unchanged so QR rendering stays readable.

- [ ] **Step 5: Replace audit render colors**

In both `web/src/pages/audit/AuditLogsPage.vue` and `web/src/pages/apps/AppAuditTab.vue`, replace inline style strings using this mapping:

```text
color:#8A94C6 -> color:var(--color-text-secondary)
color:#FF3B5C -> color:var(--color-danger)
```

For example:

```ts
h('span', { style: 'color:#8A94C6' }, '—')
```

becomes:

```ts
h('span', { style: 'color:var(--color-text-secondary)' }, '—')
```

- [ ] **Step 6: Replace knowledge and workspace link colors**

In `web/src/pages/knowledge/OrgKnowledgePage.vue` and `web/src/pages/apps/AppWorkspaceTab.vue`, replace:

```ts
style: 'cursor: pointer; color: #00F0FF; text-decoration: underline dotted'
```

with:

```ts
style: 'cursor: pointer; color: var(--color-info); text-decoration: underline dotted'
```

In `web/src/pages/knowledge/OrgKnowledgePage.vue`, replace:

```ts
style: 'color: #FF3B5C; font-size: 12px'
```

with:

```ts
style: 'color: var(--color-danger); font-size: 12px'
```

In `web/src/pages/knowledge/OrgKnowledgePage.vue` and `web/src/pages/apps/AppKnowledgeTab.vue`, replace:

```css
color: rgba(255, 255, 255, 0.64);
```

with:

```css
color: var(--color-text-secondary);
```

- [ ] **Step 7: Update `CreateMemberPage.vue` local color**

In `web/src/pages/org/CreateMemberPage.vue`, replace:

```css
color: #8A94C6;
```

with:

```css
color: var(--color-text-secondary);
```

- [ ] **Step 8: Run focused component tests**

Run:

```bash
cd web && npm run test -- --run \
  src/components/__tests__/DataTableList.spec.ts \
  src/components/__tests__/ConfirmActionModal.spec.ts \
  src/pages/audit/AuditLogsPage.spec.ts \
  src/pages/apps/AppAuditTab.spec.ts \
  src/pages/knowledge/OrgKnowledgePage.spec.ts \
  src/pages/apps/AppKnowledgeTab.spec.ts \
  src/pages/org/CreateMemberPage.spec.ts
```

Expected: command exits 0.

- [ ] **Step 9: Commit shared component cleanup**

Run:

```bash
rtk git add web/src/components/DataTableList.vue web/src/components/ConfirmActionModal.vue web/src/components/AuthChallengeRenderer.vue web/src/pages/audit/AuditLogsPage.vue web/src/pages/apps/AppAuditTab.vue web/src/pages/knowledge/OrgKnowledgePage.vue web/src/pages/apps/AppKnowledgeTab.vue web/src/pages/apps/AppWorkspaceTab.vue web/src/pages/org/CreateMemberPage.vue
rtk git commit -m "feat(theme): 清理通用组件旧色值" -m "将列表、确认弹窗、扫码挑战、审计列和知识库链接的旧暗色/青色样式切换为浅色主题变量。"
```

Expected: commit succeeds with only the listed files staged.

---

### Task 5: Convert Console And Usage Charts To Light Palette

**Files:**
- Modify: `web/src/pages/platform/ConsolePage.vue`
- Modify: `web/src/pages/org/OrgConsolePage.vue`
- Modify: `web/src/pages/usage/UsageSummary.vue`
- Modify: `web/src/components/ResourceTrendChart.vue`

- [ ] **Step 1: Run the chart old-color check**

Run:

```bash
rtk rg -n "#8A94C6|#30363d|#2d3139|#1f6feb|#18a058|#d03050|#0d1117|rgba\\(31,111,235|rgba\\(24,160,88|rgba\\(15, 23, 42|#f8fafc|#0f172a|rgba\\(226, 232, 240" web/src/pages/platform/ConsolePage.vue web/src/pages/org/OrgConsolePage.vue web/src/pages/usage/UsageSummary.vue web/src/components/ResourceTrendChart.vue
```

Expected: output includes platform/org ECharts colors and `UsageSummary.vue` dark chart panel styles.

- [ ] **Step 2: Add chart color constants to `ConsolePage.vue`**

After the comment `// ConsolePage 是平台管理员专属的控制台首页：统计条 + Token 趋势/组织用量/实例状态三图。`, add:

```ts
// 图表颜色与全局浅色主题保持一致，避免 ECharts 默认色回到深色控制台残留。
const CHART_TEXT_COLOR = '#6b7280'
const CHART_AXIS_COLOR = '#d9dde5'
const CHART_GRID_COLOR = '#edf0f5'
const CHART_INFO_COLOR = '#1677ff'
const CHART_INFO_AREA = 'rgba(22, 119, 255, 0.08)'
const CHART_SUCCESS_COLOR = '#16a34a'
const CHART_MUTED_COLOR = '#8a94a6'
const CHART_DANGER_COLOR = '#d93026'
const CHART_PIE_BORDER = '#ffffff'
```

- [ ] **Step 3: Replace `ConsolePage.vue` ECharts literal colors**

In `ConsolePage.vue`, make these replacements:

```text
'#8A94C6' -> CHART_TEXT_COLOR
'#30363d' -> CHART_AXIS_COLOR
'#2d3139' -> CHART_GRID_COLOR
'#1f6feb' -> CHART_INFO_COLOR
'rgba(31,111,235,0.08)' -> CHART_INFO_AREA
'#0d1117' -> CHART_PIE_BORDER
'#18a058' -> CHART_SUCCESS_COLOR
'#63748a' -> CHART_MUTED_COLOR
'#d03050' -> CHART_DANGER_COLOR
```

For template inline stat suffix and note fallback, replace:

```vue
<span style="font-size: 11px; color: #8A94C6">{{ stat.suffix }}</span>
```

with:

```vue
<span style="font-size: 11px; color: var(--color-text-secondary)">{{ stat.suffix }}</span>
```

Replace stat `noteColor` values:

```ts
noteColor: '#18a058'
noteColor: '#d03050'
```

with:

```ts
noteColor: 'var(--color-success)'
noteColor: 'var(--color-danger)'
```

Replace scoped `.chart-state` styles with:

```css
.chart-state {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 320px;
  color: var(--color-text-secondary);
  font-size: 13px;
}

.chart-state.danger { color: var(--color-danger); }
```

- [ ] **Step 4: Add chart color constants to `OrgConsolePage.vue`**

After the comment `// OrgConsolePage 是组织管理员专属的控制台首页：统计条 + 用量趋势/实例状态两图。`, add:

```ts
// 图表颜色与全局浅色主题保持一致，避免 ECharts 默认色回到深色控制台残留。
const CHART_TEXT_COLOR = '#6b7280'
const CHART_AXIS_COLOR = '#d9dde5'
const CHART_GRID_COLOR = '#edf0f5'
const CHART_INFO_COLOR = '#1677ff'
const CHART_INFO_AREA = 'rgba(22, 119, 255, 0.08)'
const CHART_SUCCESS_COLOR = '#16a34a'
const CHART_MUTED_COLOR = '#8a94a6'
const CHART_DANGER_COLOR = '#d93026'
const CHART_PIE_BORDER = '#ffffff'
```

- [ ] **Step 5: Replace `OrgConsolePage.vue` ECharts literal colors**

In `OrgConsolePage.vue`, make these replacements:

```text
'#8A94C6' -> CHART_TEXT_COLOR
'#30363d' -> CHART_AXIS_COLOR
'#2d3139' -> CHART_GRID_COLOR
'#18a058' -> CHART_INFO_COLOR for the usage line series only
'rgba(24,160,88,0.08)' -> CHART_INFO_AREA for the usage line area only
'#0d1117' -> CHART_PIE_BORDER
pie data '#18a058' -> CHART_SUCCESS_COLOR
'#63748a' -> CHART_MUTED_COLOR
'#d03050' -> CHART_DANGER_COLOR
```

Replace stat `noteColor` values:

```ts
noteColor: '#18a058'
noteColor: '#d03050'
```

with:

```ts
noteColor: 'var(--color-success)'
noteColor: 'var(--color-danger)'
```

Replace scoped `.chart-state` styles with the same block used in `ConsolePage.vue`.

- [ ] **Step 6: Convert `UsageSummary.vue` SVG chart styles**

In `web/src/pages/usage/UsageSummary.vue`, replace the chart style declarations with:

```css
.chart-panel {
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
}

.chart-header span,
.chart-legend {
  color: var(--color-text-secondary);
}

.trend-chart .grid-line {
  stroke: var(--color-divider);
}

.legend-token {
  stroke: var(--color-info);
  background: var(--color-info);
}

.legend-quota {
  stroke: var(--color-warning);
  background: var(--color-warning);
}

.trend-chart text {
  fill: var(--color-text-secondary);
}
```

If the file uses different class names for SVG grid and text, preserve the selectors already present and only replace their color values:

```text
rgba(148, 163, 184, 0.24) -> var(--color-border)
rgba(15, 23, 42, 0.36) -> var(--color-surface)
rgba(226, 232, 240, 0.68) -> var(--color-text-secondary)
#f8fafc -> var(--color-text-primary)
rgba(148, 163, 184, 0.25) -> var(--color-divider)
#38bdf8 -> var(--color-info)
#0f172a -> var(--color-text-primary)
rgba(226, 232, 240, 0.65) -> var(--color-text-secondary)
rgba(226, 232, 240, 0.72) -> var(--color-text-secondary)
```

- [ ] **Step 7: Confirm `ResourceTrendChart.vue` stays light**

In `web/src/components/ResourceTrendChart.vue`, keep the existing blue/orange series:

```ts
color: ['#2563eb', '#d97706'],
```

Update only fallbacks that still use non-contract grays:

```text
#66758a -> #6b7280
#d9ddea -> #e5e7eb
#eef2f7 -> #edf0f5
#8a94c6 -> #6b7280
```

- [ ] **Step 8: Run focused chart tests**

Run:

```bash
cd web && npm run test -- --run \
  src/pages/usage/__tests__/UsagePage.spec.ts \
  src/components/__tests__/ResourceTrendChart.spec.ts
```

Expected: command exits 0.

- [ ] **Step 9: Commit chart palette conversion**

Run:

```bash
rtk git add web/src/pages/platform/ConsolePage.vue web/src/pages/org/OrgConsolePage.vue web/src/pages/usage/UsageSummary.vue web/src/components/ResourceTrendChart.vue
rtk git commit -m "feat(theme): 调整控制台图表浅色配色" -m "将平台控制台、组织控制台、用量趋势和资源趋势图切换为浅色坐标轴、浅网格线和蓝橙信息色。"
```

Expected: commit succeeds with only the listed files staged.

---

### Task 6: Clean Remaining Page Fallbacks And App Detail Surfaces

**Files:**
- Modify: `web/src/pages/apps/AppDetailPage.vue`
- Modify: `web/src/pages/apps/AppCronTab.vue`
- Modify: `web/src/pages/apps/AppKanbanTab.vue`
- Modify: `web/src/pages/apps/AppOverviewTab.vue`
- Modify: `web/src/pages/apps/cron/CronRunHistory.vue`
- Modify: `web/src/pages/apps/cron/CronJobList.vue`
- Modify: `web/src/pages/apps/cron/CronJobDetail.vue`
- Modify: `web/src/pages/apps/kanban/KanbanTaskDetail.vue`
- Modify: `web/src/pages/apps/kanban/KanbanTaskList.vue`
- Modify: `web/src/pages/apps/kanban/KanbanTaskRow.vue`
- Modify: `web/src/pages/runtime-nodes/RuntimeNodesPage.vue`
- Modify: `web/src/pages/runtime-nodes/RuntimeNodeDetailPage.vue`
- Modify: `web/src/pages/dashboard/RoleAwareHome.vue`

- [ ] **Step 1: Run the remaining fallback check**

Run:

```bash
rtk rg -n "#00F0FF|#8A94C6|#CBD6E5|#FFB800|#FF3B5C|#18a058|#d03050|#707078|#a0a0a8|#2a2a30|#101014|#1f1f24|#333|#999|#fff\\)|rgba\\(255, 255, 255, 0\\.04\\)|rgba\\(24, 160, 88" web/src/pages web/src/components --glob '!**/*.spec.ts' --glob '!**/*.test.ts'
```

Expected: output includes remaining app detail, cron, kanban, runtime, and dashboard fallback colors.

- [ ] **Step 2: Apply exact fallback mapping**

Across the files listed in this task, apply these exact replacements:

```text
var(--primary-color, #18a058) -> var(--color-brand, #ff6a00)
var(--warning-color, #ffb800) -> var(--color-warning, #f59e0b)
var(--error-color, #d03050) -> var(--color-danger, #d93026)
var(--n-text-color-3, #707078) -> var(--color-text-secondary, #6b7280)
var(--n-text-color-2, #a0a0a8) -> var(--color-text-primary, #1f2329)
var(--n-text-color-2, #cbd6e5) -> var(--color-text-primary, #1f2329)
var(--n-border-color, #2a2a30) -> var(--color-border, #e5e7eb)
var(--n-border-color, #333) -> var(--color-border, #e5e7eb)
var(--n-color, #101014) -> var(--color-surface, #ffffff)
var(--n-color-embedded, #1f1f24) -> var(--color-surface-muted, #fbfcfd)
var(--n-color-hover, rgba(255, 255, 255, 0.04)) -> var(--color-surface-muted, #fbfcfd)
var(--n-text-color, #fff) -> var(--color-text-primary, #1f2329)
var(--text-color-3, #999) -> var(--color-text-secondary, #6b7280)
```

- [ ] **Step 3: Replace success-highlight backgrounds**

In cron and kanban files, replace:

```css
background: rgba(24, 160, 88, 0.1);
box-shadow: inset 3px 0 0 var(--primary-color, #18a058);
```

with:

```css
background: var(--color-brand-soft);
box-shadow: inset 3px 0 0 var(--color-brand);
```

Replace:

```css
background: rgba(24, 160, 88, 0.08);
border-color: var(--primary-color, #18a058);
```

with:

```css
background: var(--color-brand-soft);
border-color: var(--color-brand);
```

- [ ] **Step 4: Update `RoleAwareHome.vue` quick cards**

In `web/src/pages/dashboard/RoleAwareHome.vue`, replace the `.quick-card` style block with:

```css
.quick-card {
  display: block;
  padding: 16px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
  color: var(--color-text-primary);
  text-decoration: none;
  transition: transform 0.12s ease, box-shadow 0.12s ease, border-color 0.12s ease;
}

.quick-card:hover {
  transform: translateY(-2px);
  border-color: var(--color-brand);
  box-shadow: 0 8px 24px rgba(15, 23, 42, 0.08);
}

.quick-card h3 {
  margin: 0 0 6px;
  font-size: 16px;
}

.quick-card p {
  margin: 0;
  color: var(--color-text-secondary);
  font-size: 13px;
}
```

- [ ] **Step 5: Update runtime detail surface colors**

In `web/src/pages/runtime-nodes/RuntimeNodeDetailPage.vue`, replace:

```text
#e4eaf2 -> var(--color-border)
#f8fafc -> var(--color-surface-muted)
#66758a -> var(--color-text-secondary)
#172033 -> var(--color-text-primary)
```

In `web/src/pages/runtime-nodes/RuntimeNodesPage.vue`, replace fallback values:

```text
#1f2433 -> #1f2329
#8a94c6 -> #6b7280
#66758a -> #6b7280
#d9ddea -> #e5e7eb
```

- [ ] **Step 6: Run page-focused tests**

Run:

```bash
cd web && npm run test -- --run \
  src/pages/apps/AppDetailPage.spec.ts \
  src/pages/apps/AppCronTab.spec.ts \
  src/pages/apps/AppKanbanTab.spec.ts \
  src/pages/apps/AppOverviewTab.spec.ts \
  src/pages/runtime-nodes/RuntimeNodesPage.spec.ts
```

Expected: command exits 0.

- [ ] **Step 7: Run the remaining old-color check**

Run:

```bash
rtk rg -n "#00F0FF|#0A0E27|#050817|#7B2EDA|#8A94C6|#CBD6E5|#FFB800|#FF3B5C|rgba\\(0, ?240, ?255|rgba\\(123, ?46, ?218|rgba\\(15, ?21, ?53|#101014|#1f1f24|#2a2a30" web/src --glob '!**/*.spec.ts' --glob '!**/*.test.ts'
```

Expected: no output, except comments that explicitly explain QR white background or generated test fixtures. If any runtime style result remains, fix it before committing.

- [ ] **Step 8: Commit remaining cleanup**

Run:

```bash
rtk git add web/src/pages/apps/AppDetailPage.vue web/src/pages/apps/AppCronTab.vue web/src/pages/apps/AppKanbanTab.vue web/src/pages/apps/AppOverviewTab.vue web/src/pages/apps/cron/CronRunHistory.vue web/src/pages/apps/cron/CronJobList.vue web/src/pages/apps/cron/CronJobDetail.vue web/src/pages/apps/kanban/KanbanTaskDetail.vue web/src/pages/apps/kanban/KanbanTaskList.vue web/src/pages/apps/kanban/KanbanTaskRow.vue web/src/pages/runtime-nodes/RuntimeNodesPage.vue web/src/pages/runtime-nodes/RuntimeNodeDetailPage.vue web/src/pages/dashboard/RoleAwareHome.vue
rtk git commit -m "feat(theme): 清理业务页面浅色样式残留" -m "统一实例、定时任务、看板、运行节点和角色首页中的深色 fallback，使页面局部样式与浅色主题一致。"
```

Expected: commit succeeds with only the listed files staged.

---

### Task 7: Full Verification And Browser QA

**Files:**
- No source edits expected. Fix only visual regressions found during this task, then rerun the relevant checks.

- [ ] **Step 1: Run full frontend typecheck**

Run:

```bash
cd web && npm run typecheck
```

Expected: command exits 0.

- [ ] **Step 2: Run full frontend build**

Run:

```bash
cd web && npm run build
```

Expected: command exits 0.

- [ ] **Step 3: Run full unit test suite**

Run:

```bash
cd web && npm run test -- --run
```

Expected: command exits 0.

- [ ] **Step 4: Start the dev server**

Run:

```bash
cd web && npm run dev -- --host 127.0.0.1
```

Expected: Vite prints a local URL such as `http://127.0.0.1:5173/`. Keep the session running until browser QA finishes.

- [ ] **Step 5: Verify login page in a real browser**

Use the browser at the Vite URL and open `/login`.

Expected visual result:

```text
Page background is #f5f7fa or very light.
Login panel is white with a subtle border/shadow.
Primary login button is orange.
No cyan/purple glow or dark card is visible.
```

- [ ] **Step 6: Verify platform admin shell and console**

Login with manager platform admin credentials:

```text
组织标识: empty
账号: admin
密码: admin123
```

Open `/console`.

Expected visual result:

```text
Sidebar and header are light.
Active menu item is orange with a pale orange background or left indicator.
Statistic cards are white with light borders.
Token, org usage, and status charts have readable gray axes and blue/orange/green/red series.
No deep navy panels remain.
```

- [ ] **Step 7: Verify representative list and detail pages**

In the browser, visit these routes after login:

```text
/organizations
/members
/knowledge
/usage
/audit-logs
/runtime-nodes
```

Expected visual result:

```text
Tables are white/light gray, table headers are readable, links are blue, destructive text is red, and buttons use orange for primary actions.
```

- [ ] **Step 8: Verify organization admin console**

If org admin credentials exist in local data, login as that org admin and open `/org-console`. If local data only has the platform admin account, create or use an existing org admin through the UI before this check.

Expected visual result:

```text
Organization statistics, usage chart, and instance status chart match platform console palette.
```

- [ ] **Step 9: Verify mobile width**

Resize browser viewport to `390x844` and reload `/console`.

Expected visual result:

```text
No text overlaps.
Navigation remains usable.
Cards and chart containers fit within the viewport width.
```

- [ ] **Step 10: Stop the dev server**

Stop the Vite session that was started in Step 4.

Expected: no long-running dev server session remains.

- [ ] **Step 11: Commit QA fixes if any were made**

If browser QA required source changes, commit them:

```bash
rtk git add web/src
rtk git commit -m "fix(theme): 修复浅色主题浏览器验收问题" -m "根据真实浏览器检查结果修复浅色主题下的可读性、布局或旧色残留问题，并重新完成相关验证。"
```

Expected: commit succeeds only when QA fixes were made. If no source changes were made, skip this commit step.

- [ ] **Step 12: Record final status**

Run:

```bash
rtk git status --short
```

Expected: only unrelated pre-existing untracked files may remain. In this repository, `docs/reports/` was already untracked before this plan and should stay untouched unless the user gives separate instructions.

