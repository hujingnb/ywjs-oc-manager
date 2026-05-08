# 深色主题 + Naive UI 全面接入 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 oc-manager 管理后台从浅色主题全面迁移到 AI 科技深色风格（深蓝+青色），并将手写 CSS 类替换为 Naive UI 深色主题组件。

**Architecture:** 以 `NConfigProvider` 的 `darkTheme + themeOverrides` 为主题入口；`base.css` 重写全局颜色变量使所有页面自动继承深色底色；`DashboardLayout.vue` 换为 `n-layout + n-layout-sider + n-menu`；各页面的 `<table>` 换 `n-data-table`，按钮换 `n-button`，状态标签换 `n-tag`；`DashboardHome.vue` 新增 metric 卡片区和双列主体布局；业务逻辑、API 调用、路由配置不动。

**Tech Stack:** Vue 3, Naive UI（darkTheme + themeOverrides），Lucide Vue Next，TanStack Vue Query，Vite

---

## 文件改动一览

| 文件 | 改动类型 |
|---|---|
| `web/src/App.vue` | 修改 — darkTheme + themeOverrides |
| `web/src/styles/base.css` | 修改 — 全局深色变量 |
| `web/src/layouts/DashboardLayout.vue` | 修改 — n-layout + n-menu |
| `web/src/pages/dashboard/DashboardHome.vue` | 重构 — metric cards + data table |
| `web/src/components/AppStatusTag.vue` | 修改 — n-tag |
| `web/src/components/RuntimeStatusTag.vue` | 修改 — n-tag |
| `web/src/components/ConfirmActionModal.vue` | 修改 — n-modal |
| `web/src/components/JobProgressPanel.vue` | 修改 — n-card + n-progress |
| `web/src/components/DataTableToolbar.vue` | 修改 — n-button |
| `web/src/components/AuthChallengeRenderer.vue` | 修改 — 深色 scoped CSS（无按钮，只更新代码框和链接颜色）|
| `web/src/pages/apps/AppsPage.vue` | 修改 — n-data-table + n-button |
| `web/src/pages/org/MembersPage.vue` | 修改 — n-data-table + n-button + n-form |
| `web/src/pages/platform/OrganizationsPage.vue` | 修改 — n-data-table + n-button + n-form |
| `web/src/pages/audit/AuditLogsPage.vue` | 修改 — n-data-table |
| `web/src/pages/runtime-nodes/RuntimeNodesPage.vue` | 修改 — n-data-table + n-button + n-form |
| `web/src/pages/platform/PlatformDashboardPage.vue` | 修改 — n-card + n-statistic |
| `web/src/pages/usage/UsagePage.vue` | 修改 — n-tabs（本地）+ n-input + n-select |
| `web/src/pages/usage/UsageSummary.vue` | 修改 — n-card |
| `web/src/pages/knowledge/OrgKnowledgePage.vue` | 修改 — n-card |
| `web/src/pages/org/PersonaPage.vue` | 修改 — n-card + n-form |
| `web/src/pages/org/CreateMemberPage.vue` | 修改 — n-card + n-form |
| `web/src/pages/platform/RechargePage.vue` | 修改 — n-card + n-button |
| `web/src/pages/apps/AppDetailPage.vue` | 修改 — n-tabs（router-aware）|
| `web/src/pages/apps/AppOverviewTab.vue` | 修改 — n-card + n-button + 暗色 scoped CSS |
| `web/src/pages/apps/AppRuntimeTab.vue` | 修改 — n-card + n-button + 暗色 scoped CSS |
| `web/src/pages/apps/AppChannelsTab.vue` | 修改 — n-card |
| `web/src/pages/apps/AppKnowledgeTab.vue` | 修改 — n-card |
| `web/src/pages/apps/AppWorkspaceTab.vue` | 修改 — n-card |
| `web/src/pages/login/LoginPage.vue` | 修改 — n-input + n-button |

验收命令（每个 chunk 结束时跑）：
```bash
cd web && npm run typecheck 2>&1 | tail -10
```

---

## Chunk 1: 主题基础层

### Task 1: App.vue — 启用 darkTheme + themeOverrides

**文件：** `web/src/App.vue`

- [ ] **修改 App.vue**

将文件替换为：

```vue
<template>
  <NConfigProvider :theme="darkTheme" :theme-overrides="themeOverrides">
    <RouterView />
  </NConfigProvider>
</template>

<script setup lang="ts">
import { darkTheme, type GlobalThemeOverrides } from 'naive-ui'
import { NConfigProvider } from 'naive-ui'

const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#00F0FF',
    primaryColorHover: '#33F5FF',
    primaryColorPressed: '#00C8D4',
    primaryColorSuppl: '#00C8D4',
    bodyColor: '#0A0E27',
    cardColor: 'rgba(20,28,58,0.8)',
    modalColor: 'rgba(15,21,53,0.98)',
    tableColor: 'rgba(20,28,58,0.6)',
    tableColorStriped: 'rgba(20,28,58,0.3)',
    borderColor: 'rgba(0,240,255,0.2)',
    dividerColor: 'rgba(0,240,255,0.12)',
    textColorBase: '#FFFFFF',
    textColor1: '#FFFFFF',
    textColor2: '#CBD6E5',
    textColor3: '#8A94C6',
    successColor: '#00FF88',
    warningColor: '#FFB800',
    errorColor: '#FF3B5C',
    inputColor: 'rgba(15,21,53,0.8)',
    inputColorDisabled: 'rgba(15,21,53,0.4)',
    placeholderColor: '#8A94C6',
  },
  Layout: {
    siderColor: 'rgba(10,14,39,0.95)',
    headerColor: 'rgba(10,14,39,0.6)',
    footerColor: 'transparent',
    color: '#0A0E27',
  },
  Menu: {
    itemTextColor: '#8A94C6',
    itemTextColorHover: '#FFFFFF',
    itemTextColorActive: '#FFFFFF',
    itemTextColorActiveHover: '#FFFFFF',
    itemColorActive: 'rgba(0,240,255,0.15)',
    itemColorActiveHover: 'rgba(0,240,255,0.18)',
    itemColorHover: 'rgba(255,255,255,0.05)',
    borderColorActive: 'rgba(0,240,255,0.4)',
  },
  DataTable: {
    thColor: 'rgba(10,14,39,0.8)',
    tdColor: 'transparent',
    tdColorHover: 'rgba(0,240,255,0.05)',
    borderColor: 'rgba(0,240,255,0.12)',
    thTextColor: '#8A94C6',
  },
}
</script>
```

- [ ] **验证 TypeScript**

```bash
cd web && npm run typecheck 2>&1 | tail -5
```
期望：无错误（或仅已有错误，不新增）

- [ ] **Commit**

```bash
git add web/src/App.vue
git commit -m "feat(ui): App.vue 启用 Naive UI darkTheme + 青色科技风 themeOverrides"
```

---

### Task 2: base.css — 全局深色颜色变量

**文件：** `web/src/styles/base.css`

- [ ] **重写 base.css**

将文件完整替换为：

```css
:root {
  color: #FFFFFF;
  background: #0A0E27;
  font-family:
    Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-synthesis: none;
  text-rendering: optimizeLegibility;
}

* {
  box-sizing: border-box;
}

body {
  margin: 0;
  min-width: 320px;
  min-height: 100vh;
  background: linear-gradient(160deg, #0A0E27 0%, #050817 100%);
}

a {
  color: inherit;
  text-decoration: none;
}

button,
input {
  font: inherit;
}

/* ===== 布局壳 ===== */
.dashboard-shell {
  display: grid;
  grid-template-columns: 220px minmax(0, 1fr);
  min-height: 100vh;
}

.sidebar {
  display: flex;
  flex-direction: column;
  gap: 0;
  background: rgba(10, 14, 39, 0.95);
  border-right: 1px solid rgba(0, 240, 255, 0.2);
}

.brand-block {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 20px 16px 16px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  min-height: 64px;
}

.brand-block strong,
.brand-block span {
  display: block;
}

.brand-block span {
  color: #8A94C6;
  font-size: 12px;
}

.brand-mark {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border-radius: 10px;
  background: linear-gradient(135deg, #00F0FF, #7B2EDA);
  box-shadow: 0 0 16px rgba(0, 240, 255, 0.3);
  font-weight: 800;
  font-size: 14px;
  flex-shrink: 0;
}

.nav-list {
  display: grid;
  gap: 2px;
  padding: 12px 10px;
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 40px;
  padding: 0 12px;
  border-radius: 10px;
  color: #8A94C6;
  cursor: pointer;
  transition: all 0.15s;
}

.nav-item.active,
.nav-item:hover {
  color: #ffffff;
  background: linear-gradient(90deg, rgba(0, 240, 255, 0.15), rgba(123, 46, 218, 0.08));
  box-shadow: inset 0 0 0 1px rgba(0, 240, 255, 0.2);
}

.workspace {
  min-width: 0;
  display: flex;
  flex-direction: column;
}

/* ===== 顶栏 ===== */
.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-height: 64px;
  padding: 12px 24px;
  border-bottom: 1px solid rgba(0, 240, 255, 0.12);
  background: rgba(10, 14, 39, 0.5);
  backdrop-filter: blur(12px);
}

.topbar h1,
.login-form h1,
.panel h2 {
  margin: 0;
  letter-spacing: 0;
}

.topbar h1 {
  font-size: 20px;
}

.topbar-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.eyebrow {
  margin: 0 0 4px;
  color: #8A94C6;
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.icon-button {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border: 1px solid rgba(0, 240, 255, 0.2);
  border-radius: 8px;
  color: #8A94C6;
  background: rgba(255, 255, 255, 0.04);
  cursor: pointer;
  transition: all 0.15s;
}

.icon-button:hover {
  color: #00F0FF;
  border-color: rgba(0, 240, 255, 0.4);
}

/* ===== 状态 pill（兼容旧组件，新代码用 n-tag）===== */
.status-pill {
  display: inline-flex;
  align-items: center;
  min-height: 24px;
  padding: 0 9px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 700;
  white-space: nowrap;
}

.status-pill.ok,
.status-pill.success {
  color: #00FF88;
  background: rgba(0, 255, 136, 0.12);
  border: 1px solid rgba(0, 255, 136, 0.25);
}

.status-pill.warn,
.status-pill.warning {
  color: #FFB800;
  background: rgba(255, 184, 0, 0.12);
  border: 1px solid rgba(255, 184, 0, 0.25);
}

.status-pill.danger {
  color: #FF3B5C;
  background: rgba(255, 59, 92, 0.12);
  border: 1px solid rgba(255, 59, 92, 0.25);
}

.status-pill.neutral {
  color: #8A94C6;
  background: rgba(138, 148, 198, 0.12);
  border: 1px solid rgba(138, 148, 198, 0.2);
}

/* ===== 主体内容区 ===== */
.dashboard-main {
  display: grid;
  gap: 18px;
  padding: 24px;
}

.metric-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 14px;
}

.metric-card,
.panel {
  border: 1px solid rgba(0, 240, 255, 0.2);
  border-radius: 12px;
  background: rgba(20, 28, 58, 0.8);
  backdrop-filter: blur(12px);
}

.metric-card {
  display: grid;
  gap: 8px;
  min-height: 100px;
  padding: 18px;
}

.metric-card span,
.metric-card small {
  color: #8A94C6;
}

.metric-card strong {
  font-size: 28px;
  line-height: 1;
  color: #FFFFFF;
}

.content-grid {
  display: grid;
  grid-template-columns: minmax(320px, 0.85fr) minmax(420px, 1.15fr);
  gap: 18px;
}

.panel {
  min-width: 0;
  padding: 20px;
}

.panel-heading,
.node-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
}

.node-row {
  margin-top: 22px;
  justify-content: flex-start;
  padding: 14px;
  border: 1px solid rgba(0, 240, 255, 0.15);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.03);
}

.node-row div {
  display: grid;
  gap: 2px;
  min-width: 0;
  flex: 1;
}

/* ===== 表格（兼容旧组件，新代码用 n-data-table）===== */
table {
  width: 100%;
  margin-top: 14px;
  border-collapse: collapse;
}

th,
td {
  padding: 12px 8px;
  border-bottom: 1px solid rgba(0, 240, 255, 0.08);
  text-align: left;
  font-size: 14px;
  color: #CBD6E5;
}

th {
  color: #8A94C6;
  font-weight: 700;
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.3px;
}

/* ===== 登录页 ===== */
.auth-shell {
  display: grid;
  min-height: 100vh;
  place-items: center;
  padding: 24px;
  background: radial-gradient(circle at top left, rgba(0,240,255,0.12), transparent 40%),
              radial-gradient(circle at bottom right, rgba(123,46,218,0.16), transparent 40%),
              linear-gradient(135deg, #0A0E27 0%, #050817 100%);
}

.auth-panel {
  width: min(420px, 100%);
}

.login-form {
  display: grid;
  gap: 18px;
  padding: 32px;
  border: 1px solid rgba(0, 240, 255, 0.2);
  border-radius: 16px;
  background: rgba(20, 28, 58, 0.9);
  backdrop-filter: blur(16px);
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4), 0 0 0 1px rgba(0, 240, 255, 0.1);
}

.login-form label {
  display: grid;
  gap: 8px;
  color: #8A94C6;
  font-weight: 600;
  font-size: 13px;
}

.login-form input {
  width: 100%;
  min-height: 42px;
  padding: 0 12px;
  border: 1px solid rgba(0, 240, 255, 0.2);
  border-radius: 8px;
  background: rgba(15, 21, 53, 0.8);
  color: #FFFFFF;
}

/* ===== 按钮 ===== */
.primary-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-height: 38px;
  padding: 0 16px;
  border: 1px solid rgba(0, 240, 255, 0.4);
  border-radius: 8px;
  color: #ffffff;
  background: linear-gradient(135deg, rgba(0, 240, 255, 0.25), rgba(123, 46, 218, 0.25));
  font-weight: 700;
  cursor: pointer;
  transition: all 0.15s;
}

.primary-button:hover {
  background: linear-gradient(135deg, rgba(0, 240, 255, 0.35), rgba(123, 46, 218, 0.35));
  box-shadow: 0 0 16px rgba(0, 240, 255, 0.2);
}

.primary-button:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.secondary-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-height: 34px;
  padding: 0 12px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 8px;
  color: #CBD6E5;
  background: rgba(255, 255, 255, 0.05);
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s;
}

.secondary-button:hover {
  border-color: rgba(255, 255, 255, 0.22);
  color: #FFFFFF;
  background: rgba(255, 255, 255, 0.08);
}

.secondary-button:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.secondary-button.danger {
  color: #FF3B5C;
  border-color: rgba(255, 59, 92, 0.3);
}

.secondary-button.danger:hover {
  background: rgba(255, 59, 92, 0.1);
}

.actions-column {
  text-align: right;
  white-space: nowrap;
}

.state-text {
  padding: 12px 8px;
  color: #8A94C6;
  text-align: center;
  font-size: 14px;
}

.state-text.danger,
.danger-text {
  display: block;
  color: #FF3B5C;
}

/* ===== 表单 ===== */
.form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
  margin-top: 14px;
}

.form-grid label {
  display: grid;
  gap: 6px;
  color: #8A94C6;
  font-weight: 600;
  font-size: 13px;
}

.form-grid input,
.form-grid select,
.form-grid textarea {
  min-height: 38px;
  padding: 8px 10px;
  border: 1px solid rgba(0, 240, 255, 0.2);
  border-radius: 8px;
  font: inherit;
  background: rgba(15, 21, 53, 0.8);
  color: #FFFFFF;
}

.form-grid textarea {
  min-height: 80px;
  resize: vertical;
}

.form-grid-full {
  grid-column: 1 / -1;
}

.form-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  grid-column: 1 / -1;
}

/* ===== 侧边栏底部 ===== */
.sidebar-footer {
  margin-top: auto;
  display: grid;
  gap: 10px;
  padding: 14px 14px 16px;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
}

.me-info {
  display: grid;
  margin: 0;
  color: #CBD6E5;
  font-size: 13px;
}

.me-info small {
  color: #8A94C6;
}

/* ===== 响应式 ===== */
@media (max-width: 860px) {
  .dashboard-shell {
    grid-template-columns: 1fr;
  }

  .sidebar {
    position: sticky;
    top: 0;
    z-index: 2;
    padding: 14px;
    border-right: none;
    border-bottom: 1px solid rgba(0, 240, 255, 0.2);
  }

  .nav-list {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }

  .nav-item {
    justify-content: center;
    padding: 0 8px;
  }

  .nav-item span:last-child {
    display: none;
  }

  .topbar {
    align-items: flex-start;
    flex-direction: column;
    padding: 16px;
  }

  .dashboard-main {
    padding: 16px;
  }

  .metric-grid,
  .content-grid {
    grid-template-columns: 1fr;
  }
}
```

- [ ] **在浏览器中检查视觉效果**

```bash
cd web && npm run dev
```

打开 http://localhost:5173，确认：背景已变深色，侧边栏深蓝，顶栏模糊背景，按钮和表格颜色已更新。

- [ ] **TypeScript 检查**

```bash
cd web && npm run typecheck 2>&1 | tail -5
```

- [ ] **Commit**

```bash
git add web/src/styles/base.css
git commit -m "feat(ui): base.css 全面重写为深色科技风颜色变量"
```

---

## Chunk 2: DashboardLayout 布局壳

### Task 3: DashboardLayout.vue — n-layout + n-menu

**文件：** `web/src/layouts/DashboardLayout.vue`

> `n-menu` 用 `value` + `@update:value` 与 Vue Router 集成，不在 label 中嵌套 RouterLink。
> 当前路由 path 可能是 `/apps/xxx/overview` 这样的子路径，需要计算最长前缀匹配。

- [ ] **重写 DashboardLayout.vue**

```vue
<template>
  <n-layout has-sider style="min-height: 100vh">
    <n-layout-sider
      bordered
      :width="220"
      :collapsed-width="64"
      content-style="display: flex; flex-direction: column; height: 100%"
    >
      <!-- Logo -->
      <div class="brand-block">
        <div class="brand-mark">🦞</div>
        <div class="logo-text">
          <strong>OpenClaw</strong>
          <span>Manager</span>
        </div>
      </div>

      <!-- Nav -->
      <n-menu
        :value="activeKey"
        :options="menuOptions"
        :collapsed-width="64"
        :collapsed-icon-size="22"
        :indent="16"
        style="flex: 1"
        @update:value="onNav"
      />

      <!-- User footer -->
      <div class="sidebar-footer">
        <p v-if="auth.user" class="me-info">
          <strong>{{ auth.user.display_name }}</strong>
          <small>{{ auth.user.username }}</small>
        </p>
        <n-button
          v-if="auth.user"
          size="small"
          quaternary
          style="width: 100%; justify-content: flex-start; color: #8A94C6"
          @click="onLogout"
        >
          <template #icon><LogOut :size="15" /></template>
          退出
        </n-button>
      </div>
    </n-layout-sider>

    <n-layout>
      <n-layout-header bordered style="padding: 0 24px; display: flex; align-items: center; justify-content: space-between; min-height: 64px">
        <div>
          <p class="eyebrow">{{ environmentLabel }}</p>
          <h1 style="margin: 0; font-size: 20px">控制台</h1>
        </div>
        <div class="topbar-actions">
          <n-tag type="success" size="small" :bordered="false">API 正常</n-tag>
          <n-tag type="warning" size="small" :bordered="false">Ollama 待配置模型</n-tag>
          <n-button quaternary circle @click="reload">
            <template #icon><RefreshCw :size="17" /></template>
          </n-button>
        </div>
      </n-layout-header>

      <n-layout-content content-style="padding: 24px">
        <RouterView />
      </n-layout-content>
    </n-layout>
  </n-layout>
</template>

<script setup lang="ts">
import { computed, h } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NButton, NLayout, NLayoutContent, NLayoutHeader, NLayoutSider, NMenu, NTag,
  type MenuOption,
} from 'naive-ui'
import {
  BarChart3, BookOpen, Bot, Building2, FileSearch, Gauge,
  LayoutDashboard, LogOut, RefreshCw, Server, Users,
} from 'lucide-vue-next'

import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

const environmentLabel = computed(() => {
  if (!auth.user) return '本地调试环境'
  return `本地调试环境 · ${auth.user.role}`
})

// 根据当前路由计算激活的菜单项 key（前缀匹配）
const activeKey = computed(() => {
  const p = route.path
  if (p === '/') return '/'
  const prefixes = [
    '/platform/dashboard',
    '/organizations',
    '/members',
    '/apps',
    '/knowledge',
    '/usage',
    '/audit-logs',
    '/runtime-nodes',
    '/persona',
  ]
  return prefixes.find(k => p.startsWith(k)) ?? '/'
})

const isPlatformAdmin = computed(() => auth.user?.role === 'platform_admin')

const menuOptions = computed<MenuOption[]>(() => {
  const items: MenuOption[] = [
    { key: '/', label: '总览', icon: () => h(LayoutDashboard, { size: 18 }) },
  ]
  if (isPlatformAdmin.value) {
    items.push({ key: '/platform/dashboard', label: '平台', icon: () => h(Gauge, { size: 18 }) })
    items.push({ key: '/organizations', label: '组织', icon: () => h(Building2, { size: 18 }) })
  }
  items.push(
    { key: '/members', label: '成员', icon: () => h(Users, { size: 18 }) },
    { key: '/apps', label: '应用', icon: () => h(Bot, { size: 18 }) },
    { key: '/knowledge', label: '知识库', icon: () => h(BookOpen, { size: 18 }) },
    { key: '/usage', label: '用量', icon: () => h(BarChart3, { size: 18 }) },
    { key: '/audit-logs', label: '审计', icon: () => h(FileSearch, { size: 18 }) },
  )
  if (isPlatformAdmin.value) {
    items.push({ key: '/runtime-nodes', label: '运行节点', icon: () => h(Server, { size: 18 }) })
  }
  return items
})

function onNav(key: string) {
  router.push(key)
}

async function onLogout() {
  await auth.logout()
  await router.replace('/login')
}

function reload() {
  window.location.reload()
}
</script>

<style scoped>
.brand-block {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  min-height: 64px;
}

.brand-mark {
  width: 36px;
  height: 36px;
  border-radius: 10px;
  display: grid;
  place-items: center;
  background: linear-gradient(135deg, #00F0FF, #7B2EDA);
  box-shadow: 0 0 14px rgba(0, 240, 255, 0.3);
  font-size: 18px;
  flex-shrink: 0;
}

.logo-text strong { display: block; font-size: 15px; }
.logo-text span { display: block; font-size: 11px; color: #8A94C6; }

.sidebar-footer {
  padding: 12px 14px 16px;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
}
</style>
```

- [ ] **删除 base.css 中不再需要的 .dashboard-shell 响应式媒体查询中的 sidebar 相关样式**

不需要删除，n-layout 会完全接管布局，这些类名不再被引用，保留无害。

- [ ] **浏览器验证**

```bash
cd web && npm run dev
```

检查：侧边栏显示正确，导航点击跳转正常，激活项高亮正确，顶栏正常。

- [ ] **TypeScript 检查**

```bash
cd web && npm run typecheck 2>&1 | tail -5
```

- [ ] **Commit**

```bash
git add web/src/layouts/DashboardLayout.vue
git commit -m "feat(ui): DashboardLayout 换为 n-layout + n-layout-sider + n-menu"
```

---

## Chunk 3: 仪表盘首页重构

### Task 4: DashboardHome.vue — metric 卡片 + 应用队列 + 侧面板

**文件：** `web/src/pages/dashboard/DashboardHome.vue`

> 保留现有 API hook（useAppsByOrgQuery 等），只改模板和布局。
> metric 数值初期静态，快捷操作按钮无后端调用（保持现状）。

- [ ] **重写 DashboardHome.vue**

```vue
<template>
  <div style="display: grid; gap: 18px">
    <!-- Metric 卡片行 -->
    <n-grid :cols="4" :x-gap="14" :y-gap="14" responsive="screen" :item-responsive="true">
      <n-grid-item v-for="m in metrics" :key="m.label" :span="1" :xs="2" :sm="1">
        <n-card size="small" :bordered="true" style="height: 100%">
          <n-statistic :label="m.label" :value="m.value">
            <template #suffix>
              <span style="font-size: 12px; color: #8A94C6">{{ m.unit }}</span>
            </template>
          </n-statistic>
          <n-progress
            type="line"
            :percentage="m.pct"
            :show-indicator="false"
            :height="4"
            style="margin-top: 10px"
          />
          <div style="font-size: 11px; color: #8A94C6; margin-top: 4px">{{ m.note }}</div>
        </n-card>
      </n-grid-item>
    </n-grid>

    <!-- 图表占位 -->
    <n-card size="small" :bordered="true">
      <template #header>
        <span style="font-size: 14px; font-weight: 600">Token 趋势</span>
      </template>
      <div style="display: grid; place-items: center; min-height: 80px; color: #8A94C6; font-size: 13px">
        即将上线 · 引入 vue-echarts 后填充
      </div>
    </n-card>

    <!-- 主体双列 -->
    <n-grid :cols="24" :x-gap="14">
      <!-- 应用队列 -->
      <n-grid-item :span="17" :xs="24" :md="17">
        <n-card size="small" :bordered="true">
          <template #header>
            <span style="font-size: 14px; font-weight: 600">应用队列</span>
          </template>
          <template #header-extra>
            <n-button size="small" type="primary" tag="a" href="/apps">前往应用列表</n-button>
          </template>
          <n-data-table
            :columns="appColumns"
            :data="apps ?? []"
            :loading="appsLoading"
            size="small"
            :bordered="false"
          />
        </n-card>
      </n-grid-item>

      <!-- 右侧面板 -->
      <n-grid-item :span="7" :xs="24" :md="7">
        <div style="display: grid; gap: 14px">
          <!-- 节点状态 -->
          <n-card size="small" :bordered="true" title="节点状态">
            <div class="node-row">
              <Server :size="18" style="color: #8A94C6; flex-shrink: 0" />
              <div>
                <strong>node-local-dev</strong>
                <span style="display: block; font-size: 12px; color: #8A94C6">Docker proxy 与文件 API 待注册</span>
              </div>
              <n-tag type="warning" size="small">pending</n-tag>
            </div>
          </n-card>

          <!-- 快捷操作 -->
          <n-card size="small" :bordered="true" title="快捷操作">
            <div style="display: grid; gap: 8px">
              <n-button block>重启系统服务</n-button>
              <n-button block>清理系统缓存</n-button>
              <n-button block>查看系统日志</n-button>
            </div>
          </n-card>
        </div>
      </n-grid-item>
    </n-grid>
  </div>
</template>

<script setup lang="ts">
import { computed, h } from 'vue'
import {
  NButton, NCard, NDataTable, NGrid, NGridItem, NProgress, NStatistic, NTag,
  type DataTableColumns,
} from 'naive-ui'
import { Server } from 'lucide-vue-next'

import { useAppsByOrgQuery, type AppDTO } from '@/api/hooks/useApps'
import AppStatusTag from '@/components/AppStatusTag.vue'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const effectiveOrgId = computed(() => auth.user?.org_id)
const { data: apps, isLoading: appsLoading } = useAppsByOrgQuery(effectiveOrgId)

const metrics = [
  { label: '组织', value: '0', unit: '', pct: 0, note: '等待初始化' },
  { label: '应用', value: '0', unit: '', pct: 0, note: '尚未创建' },
  { label: '运行节点', value: '1', unit: '', pct: 100, note: '本地调试节点' },
  { label: '今日调用', value: '0', unit: '', pct: 0, note: '直查 new-api' },
]

const appColumns: DataTableColumns<AppDTO> = [
  { title: '应用名称', key: 'name', render: (row) => h('strong', row.name) },
  { title: '节点', key: 'runtime_node_id', render: (row) => row.runtime_node_id ?? '—' },
  {
    title: '状态',
    key: 'status',
    render: (row) => h(AppStatusTag, { status: row.status }),
  },
]
</script>
```

- [ ] **浏览器验证**

```bash
cd web && npm run dev
```

导航到首页，确认：4 个 metric 卡片、图表占位、应用队列表格、节点状态和快捷操作侧栏均正常渲染。

- [ ] **TypeScript 检查**

```bash
cd web && npm run typecheck 2>&1 | tail -5
```

- [ ] **Commit**

```bash
git add web/src/pages/dashboard/DashboardHome.vue
git commit -m "feat(ui): DashboardHome 重构为 metric 卡片 + n-data-table + 侧面板布局"
```

---

## Chunk 4: 通用组件

### Task 5: AppStatusTag + RuntimeStatusTag — 换 n-tag

**文件：**
- `web/src/components/AppStatusTag.vue`
- `web/src/components/RuntimeStatusTag.vue`

> `formatAppStatus` 和 `formatRuntimeNodeStatus` 返回 `{ label, tone }`，tone 值为 'success'|'warning'|'danger'|'neutral'。
> Naive UI `n-tag` 的 type 对应：success→success，warning→warning，danger→error，neutral→default。

- [ ] **修改 AppStatusTag.vue**

```vue
<template>
  <n-tag :type="nType" size="small" :bordered="false">{{ view.label }}</n-tag>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NTag } from 'naive-ui'
import { formatAppStatus } from '@/domain/status'

const props = defineProps<{ status: string }>()
const view = computed(() => formatAppStatus(props.status))
const nType = computed(() => {
  const map: Record<string, 'success' | 'warning' | 'error' | 'default'> = {
    success: 'success', warning: 'warning', danger: 'error', neutral: 'default',
  }
  return map[view.value.tone] ?? 'default'
})
</script>
```

- [ ] **修改 RuntimeStatusTag.vue**（结构相同，改用 `formatRuntimeNodeStatus`）

```vue
<template>
  <n-tag :type="nType" size="small" :bordered="false">{{ view.label }}</n-tag>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NTag } from 'naive-ui'
import { formatRuntimeNodeStatus } from '@/domain/status'

const props = defineProps<{ status: string }>()
const view = computed(() => formatRuntimeNodeStatus(props.status))
const nType = computed(() => {
  const map: Record<string, 'success' | 'warning' | 'error' | 'default'> = {
    success: 'success', warning: 'warning', danger: 'error', neutral: 'default',
  }
  return map[view.value.tone] ?? 'default'
})
</script>
```

- [ ] **TypeScript 检查**

```bash
cd web && npm run typecheck 2>&1 | tail -5
```

- [ ] **Commit**

```bash
git add web/src/components/AppStatusTag.vue web/src/components/RuntimeStatusTag.vue
git commit -m "feat(ui): AppStatusTag / RuntimeStatusTag 换为 n-tag"
```

---

### Task 6: ConfirmActionModal — 换 n-modal

**文件：** `web/src/components/ConfirmActionModal.vue`

> props/emits 接口不变，保持对所有调用方的兼容性。

- [ ] **重写 ConfirmActionModal.vue**

```vue
<template>
  <n-modal :show="visible" :mask-closable="true" @update:show="(v) => { if (!v) onCancel() }">
    <n-card
      :title="title"
      :bordered="false"
      role="dialog"
      aria-modal="true"
      style="width: min(440px, 92vw)"
    >
      <p style="margin: 0 0 16px; color: #CBD6E5">{{ message }}</p>

      <n-form-item v-if="verifyValue" :label="verifyHint || `输入 "${verifyValue}" 以确认`" :show-feedback="false">
        <n-input
          v-model:value="verifyInput"
          placeholder=""
          autocomplete="off"
          :spellcheck="false"
        />
      </n-form-item>

      <template #footer>
        <n-space justify="end">
          <n-button @click="onCancel">{{ cancelLabel }}</n-button>
          <n-button
            type="error"
            :disabled="busy || !canConfirm"
            :loading="busy"
            @click="onConfirm"
          >
            {{ confirmLabel }}
          </n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NButton, NCard, NFormItem, NInput, NModal, NSpace } from 'naive-ui'

const props = defineProps<{
  visible: boolean
  title: string
  message: string
  busy?: boolean
  confirmLabel?: string
  cancelLabel?: string
  verifyValue?: string
  verifyHint?: string
}>()

const emit = defineEmits<{
  (event: 'confirm'): void
  (event: 'cancel'): void
}>()

const confirmLabel = computed(() => props.confirmLabel ?? '确认')
const cancelLabel = computed(() => props.cancelLabel ?? '取消')
const verifyInput = ref('')

watch(
  () => props.visible,
  (visible) => { if (visible) verifyInput.value = '' },
)

const canConfirm = computed(() => {
  if (!props.verifyValue) return true
  return verifyInput.value.trim().toLowerCase() === props.verifyValue.trim().toLowerCase()
})

function onConfirm() { emit('confirm') }
function onCancel() { emit('cancel') }
</script>
```

- [ ] **TypeScript 检查**

```bash
cd web && npm run typecheck 2>&1 | tail -5
```

- [ ] **浏览器验证**

在有删除操作的页面（如应用列表）测试删除流程，确认 Modal 正常弹出、verify 输入生效、取消和确认均正常。

- [ ] **Commit**

```bash
git add web/src/components/ConfirmActionModal.vue
git commit -m "feat(ui): ConfirmActionModal 换为 n-modal"
```

---

### Task 7: JobProgressPanel + DataTableToolbar + AuthChallengeRenderer

**文件：**
- `web/src/components/JobProgressPanel.vue`
- `web/src/components/DataTableToolbar.vue`
- `web/src/components/AuthChallengeRenderer.vue`

- [ ] **重写 JobProgressPanel.vue**（用 `n-card` 替换 `.panel`，状态用 `n-tag`，进度用 `n-descriptions`）

```vue
<template>
  <n-card size="small" :bordered="true" style="margin-top: 16px">
    <template #header>
      <div style="display: flex; align-items: center; justify-content: space-between">
        <div>
          <p class="eyebrow">{{ subtitle ?? 'Job' }}</p>
          <strong>{{ title }}</strong>
        </div>
        <n-tag :type="tagType" size="small" :bordered="false">{{ labelFor(job?.status) }}</n-tag>
      </div>
    </template>

    <div v-if="!job" style="color: #8A94C6; font-size: 13px">尚未触发任务</div>
    <n-descriptions v-else :column="2" size="small" label-style="color:#8A94C6" content-style="font-weight:600">
      <n-descriptions-item label="类型">{{ job.type }}</n-descriptions-item>
      <n-descriptions-item label="尝试次数">{{ job.attempts }} / {{ job.max_attempts }}</n-descriptions-item>
      <n-descriptions-item label="下一次执行">{{ formatTime(job.run_after) }}</n-descriptions-item>
      <n-descriptions-item label="完成时间">{{ formatTime(job.finished_at) }}</n-descriptions-item>
      <n-descriptions-item v-if="job.last_error" label="最近错误" :span="2">
        <span style="color: #FF3B5C">{{ job.last_error }}</span>
      </n-descriptions-item>
    </n-descriptions>
  </n-card>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NCard, NDescriptions, NDescriptionsItem, NTag } from 'naive-ui'

const props = defineProps<{
  title: string
  subtitle?: string
  job?: {
    id: string; type: string; status: string; attempts: number; max_attempts: number;
    run_after?: string | null; finished_at?: string | null; last_error?: string
  } | null
}>()

const statusViews: Record<string, { label: string; tone: string }> = {
  pending: { label: '待执行', tone: 'warning' },
  running: { label: '执行中', tone: 'warning' },
  succeeded: { label: '已完成', tone: 'success' },
  failed: { label: '失败', tone: 'error' },
  canceled: { label: '已取消', tone: 'default' },
}

function labelFor(status?: string) {
  return status ? (statusViews[status]?.label ?? status) : '未触发'
}

const tagType = computed(() => {
  const tone = statusViews[props.job?.status ?? '']?.tone ?? 'default'
  return tone as 'success' | 'warning' | 'error' | 'default'
})

function formatTime(value?: string | null) {
  if (!value) return '—'
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString('zh-CN', { hour12: false })
}

void props
</script>
```

- [ ] **修改 DataTableToolbar.vue**（slot `actions` 不动，标题结构保持）

检查文件——当前模板只有 `<div>` 容器和 slot，不含按钮，**无需修改**（外部调用方的按钮在各页面中修改）。

- [ ] **修改 AuthChallengeRenderer.vue — 仅深色 scoped CSS**

该组件**没有任何按钮**，只渲染 QR 图片、验证码文字和状态文本。只需更新 scoped 颜色：

```vue
<style scoped>
.challenge-renderer {
  display: grid;
  gap: 8px;
  margin-top: 16px;
  text-align: center;
}

.challenge-qr {
  margin: 0 auto;
  max-width: 240px;
  border-radius: 8px;
  background: #ffffff; /* 保持白底确保 QR 可读 */
}

.challenge-code {
  margin: 0;
  padding: 16px;
  border: 1px dashed rgba(0, 240, 255, 0.3);
  border-radius: 8px;
  background: rgba(15, 21, 53, 0.8);
  color: #00F0FF;
  font-size: 22px;
  font-weight: 800;
  letter-spacing: 4px;
}

.fallback-hint {
  font-size: 12px;
  color: #8A94C6;
  word-break: break-all;
}
</style>
```

- [ ] **TypeScript 检查**

```bash
cd web && npm run typecheck 2>&1 | tail -5
```

- [ ] **Commit**

```bash
git add web/src/components/JobProgressPanel.vue web/src/components/AuthChallengeRenderer.vue
git commit -m "feat(ui): JobProgressPanel 换 n-card/n-descriptions，AuthChallengeRenderer 深色 scoped CSS"
```

---

## Chunk 5: 主要列表页

> **本 chunk 的统一操作模式：**
> - `<table>` → `<n-data-table :columns="columns" :data="rows" :loading="isLoading" size="small">`
> - columns 用 `render` 函数返回 h(组件, props) 处理复杂单元格
> - `<button class="primary-button">` → `<n-button type="primary">`
> - `<button class="secondary-button">` → `<n-button>`
> - `<button class="secondary-button danger">` → `<n-button type="error">`
> - `.dashboard-main` 外层保留（base.css 已适配深色）或换 `<div style="display:grid;gap:18px">`
> - `<section class="panel">` → `<n-card>`
> - `<div class="panel-heading">` → `<template #header>` + `<template #header-extra>`

### Task 8: AppsPage.vue

**文件：** `web/src/pages/apps/AppsPage.vue`

- [ ] **重写 AppsPage.vue**

```vue
<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">{{ auth.user?.role === 'platform_admin' ? 'Platform · Apps' : '组织 · Apps' }}</p>
        <h2 style="margin: 0">应用列表</h2>
      </div>
    </template>
    <template #header-extra>
      <n-button type="primary" tag="a" href="/members/new">创建成员并初始化</n-button>
    </template>

    <div v-if="!effectiveOrgId" class="state-text">当前账号未关联组织</div>
    <n-data-table
      v-else
      :columns="columns"
      :data="apps ?? []"
      :loading="isLoading"
      size="small"
      :bordered="false"
      :row-key="(row) => row.id"
    />

    <ConfirmActionModal
      :visible="!!toDelete"
      title="确认删除应用"
      :message='toDelete ? `将提交删除任务，应用 "${toDelete.name}" 关联的容器和 API key 都会被回收。是否继续？` : ""'
      confirm-label="确认删除"
      :busy="deleting"
      :verify-value="toDelete?.name"
      :verify-hint='toDelete ? `输入应用名 "${toDelete.name}" 以确认删除` : ""'
      @confirm="onConfirmDelete"
      @cancel="toDelete = null"
    />
  </n-card>
</template>

<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { NButton, NCard, NDataTable, NSpace, type DataTableColumns } from 'naive-ui'

import AppStatusTag from '@/components/AppStatusTag.vue'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import { apiRequest } from '@/api/client'
import { useAppsByOrgQuery, type AppDTO } from '@/api/hooks/useApps'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const client = useQueryClient()

const effectiveOrgId = computed(() => props.orgId ?? auth.user?.org_id)
const { data: apps, isLoading } = useAppsByOrgQuery(effectiveOrgId)

const toDelete = ref<AppDTO | null>(null)
const deleting = ref(false)

const columns: DataTableColumns<AppDTO> = [
  { title: '名称', key: 'name', render: (row) => h('strong', row.name) },
  { title: '状态', key: 'status', render: (row) => h(AppStatusTag, { status: row.status }) },
  { title: 'API Key', key: 'api_key_status' },
  { title: '容器', key: 'container_id', render: (row) => row.container_id ?? '—' },
  {
    title: '操作',
    key: 'actions',
    render: (row) => h(NSpace, { size: 'small' }, {
      default: () => [
        h(NButton, { size: 'small', onClick: () => trigger(row, 'restart') }, { default: () => '重启' }),
        h(NButton, { size: 'small', onClick: () => trigger(row, 'stop') }, { default: () => '停止' }),
        h(NButton, { size: 'small', type: 'error', onClick: () => confirmDelete(row) }, { default: () => '删除' }),
      ]
    }),
  },
]

function confirmDelete(app: AppDTO) { toDelete.value = app }

async function onConfirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  try { await trigger(toDelete.value, 'delete') }
  finally { deleting.value = false; toDelete.value = null }
}

async function trigger(app: AppDTO, op: 'start' | 'stop' | 'restart' | 'delete') {
  await apiRequest<{ runtime_operation: { job_id: string } }>(
    `/api/v1/apps/${app.id}/runtime/${op}`, { method: 'POST' },
  )
  await client.invalidateQueries({ queryKey: ['apps', 'org', effectiveOrgId.value] })
}
</script>
```

- [ ] **TypeScript 检查**

```bash
cd web && npm run typecheck 2>&1 | tail -5
```

- [ ] **Commit**

```bash
git add web/src/pages/apps/AppsPage.vue
git commit -m "feat(ui): AppsPage 换 n-data-table + n-button"
```

---

### Task 9: MembersPage.vue

**文件：** `web/src/pages/org/MembersPage.vue`

> 该页面含内联表单，换 `n-form` + `n-input` + `n-select`；
> 重置密码的 `window.prompt` 保持不动（业务逻辑）。

- [ ] **重写 MembersPage.vue**

将模板的 `.panel` 换 `n-card`，`<table>` 换 `n-data-table`，按钮换 `n-button`，内联创建表单换 `n-form`：

```vue
<template>
  <div style="display: grid; gap: 18px">
    <!-- 成员列表 -->
    <n-card :bordered="true">
      <template #header>
        <div>
          <p class="eyebrow">{{ orgEyebrow }}</p>
          <h2 style="margin: 0">成员列表</h2>
        </div>
      </template>
      <template #header-extra>
        <n-space>
          <n-button v-if="effectiveOrgId" tag="a" href="/members/new">
            创建并初始化
          </n-button>
          <n-button type="primary" :disabled="!effectiveOrgId" @click="openForm">
            新增成员
          </n-button>
        </n-space>
      </template>

      <div v-if="!effectiveOrgId" class="state-text">当前账号未关联组织，无法查看成员。</div>
      <n-data-table
        v-else
        :columns="columns"
        :data="members ?? []"
        :loading="isLoading"
        size="small"
        :bordered="false"
        :row-key="(row) => row.id"
      />

      <p v-if="resetFeedback" class="state-text" :class="{ danger: resetError }">{{ resetFeedback }}</p>
    </n-card>

    <!-- 创建表单 -->
    <n-card v-if="formVisible" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <h2 style="margin: 0">创建成员</h2>
          <n-button quaternary circle @click="formVisible = false">✕</n-button>
        </div>
      </template>
      <n-form :model="form" label-placement="top" @submit.prevent="onSubmit">
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item label="用户名 *">
              <n-input v-model:value="form.username" placeholder="username" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="显示名 *">
              <n-input v-model:value="form.display_name" placeholder="显示名称" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="初始密码 *">
              <n-input v-model:value="form.password" type="password" placeholder="至少 8 位" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="角色">
              <n-select v-model:value="form.role" :options="roleOptions" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-space justify="end">
              <n-button @click="formVisible = false">取消</n-button>
              <n-button type="primary" attr-type="submit" :loading="creating">保存</n-button>
            </n-space>
            <p v-if="submitError" class="state-text danger">{{ submitError }}</p>
          </n-grid-item>
        </n-grid>
      </n-form>
    </n-card>

    <!-- Modals -->
    <ConfirmActionModal
      :visible="!!memberToDelete"
      title="确认删除成员"
      :message="memberToDelete ? `将禁用账号 ${memberToDelete.username} 并提交其名下应用的删除任务，操作不可撤销。` : ''"
      confirm-label="确认删除"
      :busy="deleteMutation.isPending.value"
      @confirm="onConfirmDelete"
      @cancel="memberToDelete = null"
    />
    <ConfirmActionModal
      :visible="!!resetTarget"
      title="确认重置成员密码"
      :message="resetTarget ? `将强制重置成员 ${resetTarget.username} 的登录密码，原密码立即失效。` : ''"
      confirm-label="确认重置"
      :busy="resetMutation.isPending.value"
      :verify-value="resetTarget?.username"
      :verify-hint='resetTarget ? `输入成员登录名 "${resetTarget.username}" 以确认重置` : ""'
      @confirm="onConfirmReset"
      @cancel="resetTarget = null"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, h, reactive, ref } from 'vue'
import {
  NButton, NCard, NDataTable, NForm, NFormItem, NGrid, NGridItem,
  NInput, NSelect, NSpace, NTag, type DataTableColumns, type SelectOption,
} from 'naive-ui'

import { formatMemberRole, formatMemberStatus } from '@/domain/status'
import {
  useCreateMember, useDeleteMember, useMembersQuery, useResetMemberPassword,
  useSetMemberStatus, type MemberFormPayload,
} from '@/api/hooks/useMembers'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import type { Member } from '@/api/types'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const effectiveOrgId = computed(() => props.orgId ?? auth.user?.org_id)
const orgEyebrow = computed(() => auth.user?.role === 'platform_admin' ? 'Platform · 组织成员' : '我的组织')

const { data: members, isLoading, error: listError } = useMembersQuery(effectiveOrgId)
const createMutation = useCreateMember(effectiveOrgId)
const statusMutation = useSetMemberStatus(effectiveOrgId)
const deleteMutation = useDeleteMember(effectiveOrgId)
const memberToDelete = ref<Member | null>(null)
const resetTarget = ref<Member | null>(null)
const resetNewPassword = ref('')
const resetMutation = useResetMemberPassword()
const resetFeedback = ref('')
const resetError = ref(false)

const formVisible = ref(false)
const submitError = ref<string | null>(null)
const creating = ref(false)
const form = reactive<MemberFormPayload>({
  username: '', display_name: '', password: '', role: 'org_member',
})

const roleOptions: SelectOption[] = [
  { label: '组织成员', value: 'org_member' },
  { label: '组织管理员', value: 'org_admin' },
]

// 表格映射 tone → n-tag type
function toneToTagType(tone: string): 'success' | 'warning' | 'error' | 'default' {
  const m: Record<string, 'success' | 'warning' | 'error' | 'default'> = {
    success: 'success', warning: 'warning', danger: 'error', neutral: 'default',
  }
  return m[tone] ?? 'default'
}

const columns: DataTableColumns<Member> = [
  { title: '用户名', key: 'username' },
  { title: '姓名', key: 'display_name' },
  { title: '角色', key: 'role', render: (row) => formatMemberRole(row.role) },
  {
    title: '状态', key: 'status',
    render: (row) => {
      const v = formatMemberStatus(row.status)
      return h(NTag, { type: toneToTagType(v.tone), size: 'small', bordered: false }, { default: () => v.label })
    },
  },
  {
    title: '操作', key: 'actions',
    render: (row) => h(NSpace, { size: 'small' }, {
      default: () => [
        row.status === 'active'
          ? h(NButton, { size: 'small', onClick: () => onToggle(row, 'disable') }, { default: () => '禁用' })
          : h(NButton, { size: 'small', type: 'primary', onClick: () => onToggle(row, 'enable') }, { default: () => '启用' }),
        h(NButton, { size: 'small', onClick: () => openResetForm(row) }, { default: () => '重置密码' }),
        h(NButton, { size: 'small', type: 'error', onClick: () => { memberToDelete.value = row } }, { default: () => '删除' }),
      ]
    }),
  },
]

function openForm() {
  formVisible.value = true; submitError.value = null
  form.username = ''; form.display_name = ''; form.password = ''; form.role = 'org_member'
}

async function onSubmit() {
  submitError.value = null; creating.value = true
  try {
    await createMutation.mutateAsync({ ...form })
    formVisible.value = false
  } catch (err) {
    submitError.value = err instanceof Error ? err.message : '创建成员失败'
  } finally { creating.value = false }
}

function onToggle(member: Member, action: 'enable' | 'disable') {
  statusMutation.mutate({ userId: member.id, action })
}

async function onConfirmDelete() {
  if (!memberToDelete.value) return
  try { await deleteMutation.mutateAsync(memberToDelete.value.id) }
  catch (err) { submitError.value = err instanceof Error ? err.message : '删除成员失败' }
  finally { memberToDelete.value = null }
}

function openResetForm(member: Member) {
  const pwd = window.prompt(`输入成员 ${member.username} 的新密码（至少 8 位）`)
  if (!pwd || pwd.length < 8) return
  resetTarget.value = member; resetNewPassword.value = pwd
  resetFeedback.value = ''; resetError.value = false
}

async function onConfirmReset() {
  if (!resetTarget.value) return
  resetFeedback.value = ''; resetError.value = false
  try {
    await resetMutation.mutateAsync({ userId: resetTarget.value.id, password: resetNewPassword.value })
    resetFeedback.value = '已重置密码'; resetTarget.value = null
  } catch (err) {
    resetError.value = true
    resetFeedback.value = err instanceof Error ? err.message : '重置失败'
  }
}

void listError
</script>
```

- [ ] **TypeScript 检查 + Commit**

```bash
cd web && npm run typecheck 2>&1 | tail -5
git add web/src/pages/org/MembersPage.vue
git commit -m "feat(ui): MembersPage 换 n-data-table + n-form + n-button"
```

---

### Task 10: OrganizationsPage.vue

**文件：** `web/src/pages/platform/OrganizationsPage.vue`

- [ ] **重写 OrganizationsPage.vue**

```vue
<template>
  <div style="display: grid; gap: 18px">
    <!-- 组织列表 -->
    <n-card :bordered="true">
      <template #header>
        <div>
          <p class="eyebrow">Platform</p>
          <h2 style="margin: 0">组织列表</h2>
        </div>
      </template>
      <template #header-extra>
        <n-button type="primary" @click="openForm">
          <template #icon><Plus :size="16" /></template>
          新增组织
        </n-button>
      </template>

      <div v-if="isLoading" class="state-text">加载中…</div>
      <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
      <n-data-table
        v-else
        :columns="columns"
        :data="organizations ?? []"
        size="small"
        :bordered="false"
        :row-key="(row) => row.id"
      />
    </n-card>

    <!-- 创建表单 -->
    <n-card v-if="formVisible" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">New</p>
            <h2 style="margin: 0">创建组织</h2>
          </div>
          <n-button quaternary circle @click="formVisible = false">
            <template #icon><X :size="18" /></template>
          </n-button>
        </div>
      </template>
      <n-form :model="form" label-placement="top" @submit.prevent="onSubmit">
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item label="名称 *">
              <n-input v-model:value="form.name" placeholder="组织名称" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="联系人">
              <n-input v-model:value="form.contact_name" placeholder="联系人姓名" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="联系电话">
              <n-input v-model:value="form.contact_phone" placeholder="手机号" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="余额预警阈值 (%)">
              <n-input-number v-model:value="form.credit_warning_threshold" :min="0" :max="100" style="width: 100%" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="备注">
              <n-input v-model:value="form.remark" type="textarea" :rows="2" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-space justify="end">
              <n-button @click="formVisible = false">取消</n-button>
              <n-button type="primary" attr-type="submit" :loading="creating">保存</n-button>
            </n-space>
            <p v-if="submitError" class="state-text danger">{{ submitError }}</p>
          </n-grid-item>
        </n-grid>
      </n-form>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { h, reactive, ref } from 'vue'
import { Plus, X } from 'lucide-vue-next'
import {
  NButton, NCard, NDataTable, NForm, NFormItem, NGrid, NGridItem,
  NInput, NInputNumber, NSpace, NTag, type DataTableColumns,
} from 'naive-ui'

import { formatOrgStatus } from '@/domain/status'
import {
  useCreateOrganization, useOrganizationsQuery, useUpdateOrganizationStatus,
  type OrganizationFormPayload,
} from '@/api/hooks/useOrganizations'
import type { Organization } from '@/api/types'

const { data: organizations, isLoading, error } = useOrganizationsQuery()
const createMutation = useCreateOrganization()
const statusMutation = useUpdateOrganizationStatus()

const formVisible = ref(false)
const submitError = ref<string | null>(null)
const creating = ref(false)
const form = reactive<OrganizationFormPayload>({
  name: '', contact_name: '', contact_phone: '', remark: '',
  credit_warning_threshold: undefined,
})

function toneToTagType(tone: string): 'success' | 'warning' | 'error' | 'default' {
  const m: Record<string, 'success' | 'warning' | 'error' | 'default'> = {
    success: 'success', warning: 'warning', danger: 'error', neutral: 'default',
  }
  return m[tone] ?? 'default'
}

const columns: DataTableColumns<Organization> = [
  {
    title: '名称', key: 'name',
    render: (row) => [
      h('strong', row.name),
      row.remark ? h('small', { style: 'display:block;color:#8A94C6;font-size:12px' }, row.remark) : null,
    ],
  },
  {
    title: '状态', key: 'status',
    render: (row) => {
      const v = formatOrgStatus(row.status)
      return h(NTag, { type: toneToTagType(v.tone), size: 'small', bordered: false }, { default: () => v.label })
    },
  },
  { title: '联系人', key: 'contact_name', render: (row) => row.contact_name || '—' },
  { title: '电话', key: 'contact_phone', render: (row) => row.contact_phone || '—' },
  { title: '预警阈值', key: 'credit_warning_threshold', render: (row) => typeof row.credit_warning_threshold === 'number' ? `${row.credit_warning_threshold}%` : '—' },
  {
    title: '操作', key: 'actions',
    render: (row) => row.status === 'active'
      ? h(NButton, { size: 'small', onClick: () => onToggle(row, 'disable') }, { default: () => '禁用' })
      : h(NButton, { size: 'small', type: 'primary', onClick: () => onToggle(row, 'enable') }, { default: () => '启用' }),
  },
]

function openForm() {
  formVisible.value = true; submitError.value = null
  form.name = ''; form.contact_name = ''; form.contact_phone = ''; form.remark = ''
  form.credit_warning_threshold = undefined
}

async function onSubmit() {
  submitError.value = null; creating.value = true
  try {
    await createMutation.mutateAsync({
      name: form.name,
      contact_name: form.contact_name || undefined,
      contact_phone: form.contact_phone || undefined,
      remark: form.remark || undefined,
      credit_warning_threshold: typeof form.credit_warning_threshold === 'number' ? form.credit_warning_threshold : undefined,
    })
    formVisible.value = false
  } catch (err) {
    submitError.value = err instanceof Error ? err.message : '创建组织失败'
  } finally { creating.value = false }
}

function onToggle(org: Organization, action: 'enable' | 'disable') {
  statusMutation.mutate({ orgId: org.id, action })
}
</script>
```

- [ ] **TypeScript 检查 + Commit**

```bash
cd web && npm run typecheck 2>&1 | tail -5
git add web/src/pages/platform/OrganizationsPage.vue
git commit -m "feat(ui): OrganizationsPage 换 n-data-table + n-form"
```

---

### Task 11: AuditLogsPage.vue

**文件：** `web/src/pages/audit/AuditLogsPage.vue`

- [ ] **重写 AuditLogsPage.vue**

- `<section class="panel">` → `<n-card>`
- `<table>` → `<n-data-table>`（列：时间、操作者、资源、操作、结果 n-tag）
- 结果 tone 沿用现有 `auditTone()` 函数

- [ ] **TypeScript 检查 + Commit**

```bash
cd web && npm run typecheck 2>&1 | tail -5
git add web/src/pages/audit/AuditLogsPage.vue
git commit -m "feat(ui): AuditLogsPage 换 n-data-table"
```

---

### Task 12: RuntimeNodesPage.vue

**文件：** `web/src/pages/runtime-nodes/RuntimeNodesPage.vue`

> 该页面有多个 panel（列表、注册表单、调整 max_apps 表单、token 展示）。
> `.token-block` `<pre>` 保留，仅更新 scoped 样式中的颜色。
> `.link-button` 在 max_apps 列内，换为 `<n-button text>`。

- [ ] **重写 RuntimeNodesPage.vue**

- 4 个 `<section class="panel">` → 各自换 `<n-card>`（v-if 条件保持）
- 列表表格 → `<n-data-table>`（列：名称+路径、状态 n-tag、Docker/File/版本/心跳/最大应用数、操作）
- 最大应用数列：`<span>{{ node.max_apps ?? '不限' }}</span>` + `<n-button text @click="openMaxAppsEdit(node)">编辑</n-button>`
- 注册表单和 max_apps 表单 → `<n-form>` + `<n-input>`
- `.token-block` scoped 样式颜色更新为深色（背景 `rgba(15,21,53,0.8)`，文字 `#00F0FF`）

- [ ] **TypeScript 检查 + Commit**

```bash
cd web && npm run typecheck 2>&1 | tail -5
git add web/src/pages/runtime-nodes/RuntimeNodesPage.vue
git commit -m "feat(ui): RuntimeNodesPage 换 n-data-table + n-form"
```

---

## Chunk 6: 其余功能页

### Task 13: PlatformDashboardPage.vue

**文件：** `web/src/pages/platform/PlatformDashboardPage.vue`

> 现有 `.overview-grid` / `.overview-card` 是 scoped CSS，需更新为深色，并用 `n-statistic` 替换纯文字数值。

- [ ] **重写 PlatformDashboardPage.vue**

```vue
<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">Platform · Dashboard</p>
        <h2 style="margin: 0">平台总览</h2>
      </div>
    </template>

    <div v-if="!isPlatformAdmin" class="state-text">仅平台管理员可访问。</div>
    <div v-else-if="isLoading" class="state-text">加载中…</div>
    <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
    <n-grid v-else-if="overview" :cols="6" :x-gap="14" :y-gap="14" responsive="screen" :item-responsive="true">
      <n-grid-item v-for="stat in stats" :key="stat.label" :span="1" :xs="2">
        <n-card size="small" :bordered="true">
          <n-statistic :label="stat.label" :value="stat.value" />
          <div v-if="stat.note" style="font-size: 11px; color: #8A94C6; margin-top: 4px">{{ stat.note }}</div>
        </n-card>
      </n-grid-item>
    </n-grid>
  </n-card>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NCard, NGrid, NGridItem, NStatistic } from 'naive-ui'

import { usePlatformOverviewQuery } from '@/api/hooks/usePlatform'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const isPlatformAdmin = computed(() => auth.user?.role === 'platform_admin')
const { data: overview, isLoading, error } = usePlatformOverviewQuery(isPlatformAdmin)

function formatQuota(value: number) { return value.toLocaleString('en-US') }

const stats = computed(() => {
  if (!overview.value) return []
  const o = overview.value
  return [
    { label: '组织数', value: String(o.organization_count), note: '' },
    { label: '成员数', value: String(o.member_count), note: '不含平台管理员' },
    { label: '应用数', value: String(o.app_count), note: '' },
    { label: '运行中', value: String(o.running_app_count), note: '' },
    { label: '异常', value: String(o.error_app_count), note: '' },
    {
      label: '总余额',
      value: o.usage_available ? formatQuota(o.total_remain_quota) : '—',
      note: o.usage_available ? 'new-api 实时' : '用量服务未启用',
    },
  ]
})
</script>
```

- [ ] **TypeScript 检查 + Commit**

```bash
cd web && npm run typecheck 2>&1 | tail -5
git add web/src/pages/platform/PlatformDashboardPage.vue
git commit -m "feat(ui): PlatformDashboardPage 换 n-statistic + n-card"
```

---

### Task 14: UsagePage.vue + UsageSummary.vue

**文件：**
- `web/src/pages/usage/UsagePage.vue`
- `web/src/pages/usage/UsageSummary.vue`

> `UsagePage` 有自己的 tab 切换（本地 activeTab ref）和过滤 input/select，换为 `n-tabs`（v-model:value，无路由）+ `n-input` + `n-select`。

- [ ] **修改 UsagePage.vue**

- `<section class="panel">` → `<n-card>`
- `.tab-group` button 组 → `<n-tabs v-model:value="activeTab" type="line">`，每个 tab 是 `<n-tab-pane :name="tab.key" :tab="tab.label">`
- `.filter-row` 中的 `<input>` → `<n-input>`，`<select>` → `<n-select>`
- 移除 `.tab-group`、`.filter-row`、`active` 类对应的 scoped CSS，改为 Naive UI 组件自带样式

- [ ] **修改 UsageSummary.vue**

> `UsageSummary.vue` 顶层是 `<div>`（**无** `.panel` 包裹），不需要换 `n-card`。
> 需要做的是：`<table>` → `n-data-table`；更新 scoped 颜色（这些类在 base.css 中没有对应定义，是组件私有的）。

修改内容：
1. 将 `<table>` 替换为 `n-data-table`，columns：App ID（`code` tag）、NewAPI Token（`code` 或"未绑定"）、剩余额度、状态（`n-tag`）
2. 更新 scoped CSS：
   - `.quota` → `color: #00F0FF`（由深绿 `#276d5c` 改为青色）
   - `.status-badge` → 保留基础样式
   - `.status-1` → `background: rgba(0,255,136,0.12); color: #00FF88; border: 1px solid rgba(0,255,136,0.25)`
   - `.status-2` → `background: rgba(255,59,92,0.12); color: #FF3B5C; border: 1px solid rgba(255,59,92,0.25)`

```vue
<template>
  <div>
    <div v-if="!view" class="state-text">{{ emptyText }}</div>
    <template v-else>
      <p class="summary-line">
        <strong>合计余额：</strong>
        <span class="quota">{{ formatQuota(view.total_remain_quota) }}</span>
        <span class="state-text">最近更新：{{ formatTime(view.updated_at) }}</span>
      </p>
      <n-data-table
        v-if="view.apps?.length"
        :columns="columns"
        :data="view.apps"
        size="small"
        :bordered="false"
      />
      <div v-else class="state-text">{{ emptyText }}</div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { h } from 'vue'
import { NDataTable, NTag, type DataTableColumns } from 'naive-ui'
import type { AggregatedUsage } from '@/api/hooks/useUsage'

defineProps<{ view?: AggregatedUsage; emptyText: string }>()

function statusLabel(s: number): string {
  if (s === 1) return '启用'
  if (s === 2) return '禁用'
  return '未知'
}

function statusTagType(s: number): 'success' | 'error' | 'default' {
  if (s === 1) return 'success'
  if (s === 2) return 'error'
  return 'default'
}

function formatQuota(value: number): string {
  return value.toLocaleString('en-US')
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

type AppUsage = AggregatedUsage['apps'][number]

const columns: DataTableColumns<AppUsage> = [
  { title: '应用 ID', key: 'app_id', render: (row) => h('code', row.app_id.slice(0, 12)) },
  {
    title: 'NewAPI Token', key: 'newapi_key_id',
    render: (row) => row.newapi_key_id ? h('code', row.newapi_key_id) : h('span', { class: 'state-text' }, '未绑定'),
  },
  { title: '剩余额度', key: 'remain_quota', render: (row) => formatQuota(row.remain_quota) },
  {
    title: '状态', key: 'status',
    render: (row) => h(NTag, { type: statusTagType(row.status), size: 'small', bordered: false }, { default: () => statusLabel(row.status) }),
  },
]
</script>

<style scoped>
.summary-line {
  display: flex;
  gap: 16px;
  align-items: baseline;
  margin-bottom: 12px;
}

.quota {
  font-size: 20px;
  font-weight: 600;
  color: #00F0FF;
}
</style>
```

- [ ] **TypeScript 检查 + Commit**

```bash
cd web && npm run typecheck 2>&1 | tail -5
git add web/src/pages/usage/UsagePage.vue web/src/pages/usage/UsageSummary.vue
git commit -m "feat(ui): UsagePage 换 n-tabs + n-input，UsageSummary 换 n-data-table + 深色 CSS"
```

---

### Task 15: OrgKnowledgePage.vue

**文件：** `web/src/pages/knowledge/OrgKnowledgePage.vue`

> 该页面的"上传文件"按钮是 `<label class="primary-button">` 包裹 `<input type="file">`。
> **不能**直接换成 `n-button`（因为需要触发 file input）。保留 label 方案，base.css 的 `.primary-button` 深色样式已生效。

- [ ] **修改 OrgKnowledgePage.vue**

改动内容：
1. `<main class="dashboard-main">` → `<div style="display: grid; gap: 18px">`
2. 两个 `<section class="panel">` → `<n-card>`（panel-heading → #header / #header-extra）
3. `<label class="primary-button">` 文件上传：**保留** label 结构，不换 n-button
4. 两个 `<table>` → `n-data-table`：
   - 文件列表表：名称（文件夹可点击）、大小、操作删除按钮
   - 节点同步表：节点 ID、状态（n-tag）、最近成功、最近错误、操作（重试 n-button）
5. `<button class="secondary-button">` → `<n-button>`
6. 更新 scoped CSS：
   - `.folder` → `color: #00F0FF; text-decoration: underline dotted`
   - `.sync-pending` → `background: rgba(255,184,0,0.12); color: #FFB800; border: 1px solid rgba(255,184,0,0.25)`
   - `.sync-synced` → `background: rgba(0,255,136,0.12); color: #00FF88; border: 1px solid rgba(0,255,136,0.25)`
   - `.sync-failed` → `background: rgba(255,59,92,0.12); color: #FF3B5C; border: 1px solid rgba(255,59,92,0.25)`

注意：文件夹行的点击跳转用 render 函数中 `h('strong', { class: 'folder', onClick: () => enter(entry) }, ...)` 实现。

- [ ] **TypeScript 检查 + Commit**

```bash
cd web && npm run typecheck 2>&1 | tail -5
git add web/src/pages/knowledge/OrgKnowledgePage.vue
git commit -m "feat(ui): OrgKnowledgePage 换 n-card + n-data-table，深色 scoped CSS"
```

---

### Task 16b: PersonaPage.vue

**文件：** `web/src/pages/org/PersonaPage.vue`

> 表单是 `<form class="form-stack">` 结构（纵向 flex，不是 form-grid）。
> 所有字段都是 `<textarea>`，另有一个 `<input type="checkbox">`。

- [ ] **修改 PersonaPage.vue**

改动内容：
1. `<main class="dashboard-main">` → `<div>`
2. `<section class="panel">` → `<n-card>`（panel 内已有 `DataTableToolbar` 组件处理标题）
3. 四个 `<textarea>` → `<n-input v-model:value="..." type="textarea" :rows="N" />`（保留 rows 数量不变）
4. `<input type="checkbox">` → `<n-checkbox v-model:checked="form.allow_member_override">`
5. `<button class="primary-button">` → `<n-button type="primary" attr-type="submit" :disabled="...">`
6. 更新 scoped CSS：
   - `.form-stack textarea` 样式可删除（n-input 自带深色样式）
   - `.label` → `color: #8A94C6`
   - `.danger` → `color: #FF3B5C`
   - `.checkbox-row` 的 flex-direction/gap 保留
   - `.actions-row` 保留

额外引入：`import { NCard, NCheckbox, NInput, NButton } from 'naive-ui'`

- [ ] **TypeScript 检查 + Commit**

```bash
cd web && npm run typecheck 2>&1 | tail -5
git add web/src/pages/org/PersonaPage.vue
git commit -m "feat(ui): PersonaPage 换 n-card + n-input textarea + n-checkbox"
```

---

### Task 16c: CreateMemberPage.vue

**文件：** `web/src/pages/org/CreateMemberPage.vue`

> 页面有两个 `<fieldset class="form-section">` 嵌套在 `<form class="form-grid">` 内。

- [ ] **修改 CreateMemberPage.vue**

改动内容：
1. `<main class="dashboard-main">` → `<div style="display: grid; gap: 18px">`
2. 外层 `<section class="panel">` → `<n-card>`
3. 两个 `<fieldset class="form-section">` 保留结构但更新 scoped CSS 颜色：
   - `.form-section` → `border: 1px solid rgba(0,240,255,0.15); border-radius: 8px; margin: 0; padding: 14px`
   - `.form-section legend` → `color: #8A94C6; font-size: 12px; font-weight: 700; text-transform: uppercase; padding: 0 6px`
4. 两个 fieldset 内的 `<div class="form-grid">` 保留（base.css 已有深色样式）
5. `<input>` → `<n-input>`，`<select>` → `<n-select>`，`<textarea>` → `<n-input type="textarea">`
6. `<RouterLink class="secondary-button" to="/members">` → `<n-button tag="a" href="/members">` 或保留 RouterLink 但用 `class="secondary-button"`（CSS 已有深色样式）
7. `<button class="primary-button">` → `<n-button type="primary" attr-type="submit" :disabled="creating">`
8. 结果展示 `<section v-if="lastResult" class="panel">` → `<n-card v-if="lastResult">`，`.status-pill.success` → `<n-tag type="success">`

- [ ] **TypeScript 检查 + Commit**

```bash
cd web && npm run typecheck 2>&1 | tail -5
git add web/src/pages/org/CreateMemberPage.vue
git commit -m "feat(ui): CreateMemberPage 换 n-card + n-input，fieldset 深色 CSS"
```

---

### Task 16d: RechargePage.vue

**文件：** `web/src/pages/platform/RechargePage.vue`

> 充值表单是三列 grid（1fr 2fr auto），不是标准 form-grid。
> 包含两个 panel：充值表单 + 充值历史表格。
> 已有 ConfirmActionModal（无需改动）。

- [ ] **修改 RechargePage.vue**

改动内容：
1. `<main class="dashboard-main">` → `<div style="display: grid; gap: 18px">`
2. 两个 `<section class="panel">` → `<n-card>`
3. 充值表单区：保留三列 grid 布局结构，`<input>` → `<n-input>`，`<button class="primary-button">` → `<n-button type="primary" attr-type="submit">`
4. 历史表格：`<table>` → `n-data-table`，columns：时间、金额、备注、状态（n-tag）、错误信息
   - 状态 n-tag：`succeeded` → `type="success"`，`failed` → `type="error"`，其他 → `type="default"`
5. 更新 scoped CSS：
   - `.form-grid input` → `border: 1px solid rgba(0,240,255,0.2); background: rgba(15,21,53,0.8); color: #FFFFFF`
   - 删除 `.status-pill.succeeded` 和 `.status-pill.failed` scoped 覆盖（改用 n-tag）
   - `.danger` → `color: #FF3B5C`

- [ ] **TypeScript 检查 + Commit**

```bash
cd web && npm run typecheck 2>&1 | tail -5
git add web/src/pages/platform/RechargePage.vue
git commit -m "feat(ui): RechargePage 换 n-card + n-data-table，充值历史状态换 n-tag"
```

---

## Chunk 7: App Detail + Tab 页

### Task 16: AppDetailPage.vue — n-tabs（router-aware）

**文件：** `web/src/pages/apps/AppDetailPage.vue`

> `n-tabs` value 绑定当前 tab 名称（路由最后一段），`@update:value` 触发 `router.push`。

- [ ] **修改 AppDetailPage.vue 中的 tab 导航**

将 `.tab-nav` RouterLink 替换为：

```vue
<!-- 替换 <nav class="tab-nav"> 区域 -->
<n-tabs
  :value="currentTab"
  type="line"
  animated
  style="margin-top: 12px"
  @update:value="(tab) => router.push(`/apps/${appIdRef}/${tab}`)"
>
  <n-tab v-for="tab in tabs" :key="tab.path" :name="tab.path" :tab="tab.label" />
</n-tabs>
```

在 `<script setup>` 中新增：

```ts
import { useRouter } from 'vue-router'
import { NTabs, NTab } from 'naive-ui'

const router = useRouter()
const currentTab = computed(() => {
  const seg = route.path.split('/').pop()
  return tabs.find(t => t.path === seg) ? seg : 'overview'
})
```

同时将 `<section class="panel">` 外层换为 `<n-card>`：

```vue
<n-card :bordered="true">
  <template #header>
    <div style="display:flex; align-items:center; justify-content:space-between">
      <div>
        <p class="eyebrow">App · Detail</p>
        <h2 style="margin:0">{{ app?.name ?? '应用详情' }} <small v-if="app">· {{ app.id }}</small></h2>
      </div>
      <AppStatusTag v-if="app" :status="app.status" />
    </div>
  </template>
  <!-- n-tabs -->
  <!-- RouterView -->
</n-card>
```

- [ ] **TypeScript 检查 + Commit**

```bash
cd web && npm run typecheck 2>&1 | tail -5
git add web/src/pages/apps/AppDetailPage.vue
git commit -m "feat(ui): AppDetailPage tab 导航换为 n-tabs（router-aware）"
```

---

### Task 17: App Tab 页（5 个）

**文件：**
- `web/src/pages/apps/AppOverviewTab.vue`
- `web/src/pages/apps/AppRuntimeTab.vue`
- `web/src/pages/apps/AppChannelsTab.vue`
- `web/src/pages/apps/AppKnowledgeTab.vue`
- `web/src/pages/apps/AppWorkspaceTab.vue`

> 这些文件的 `<section class="panel">` 换 `<n-card>`，按钮换 `n-button`。
> `AppOverviewTab` 的 `.key-tag` scoped CSS 颜色更新为深色版本。
> `AppRuntimeTab` 的 `.snapshot-cell` scoped CSS 颜色更新（背景 `rgba(20,28,58,0.6)`，文字 `#00F0FF` 主色，次要 `#8A94C6`）。

- [ ] **AppOverviewTab.vue**

- `<section class="panel">` → `<n-card>`
- `<button class="primary-button">` → `<n-button type="primary">`
- `<button class="secondary-button danger">` → `<n-button type="error">`
- `<button class="secondary-button">` → `<n-button>`
- `.key-tag` scoped 颜色更新：
  - `.key-active` → `background: rgba(0,255,136,0.12); color: #00FF88`
  - `.key-disabled` → `background: rgba(255,59,92,0.12); color: #FF3B5C`
  - `.key-pending` → `background: rgba(255,184,0,0.12); color: #FFB800`
- `.info-grid dt` 颜色 → `color: #8A94C6`

- [ ] **AppRuntimeTab.vue**

- `<section class="panel">` → `<n-card>`
- 按钮换 `n-button`
- `.snapshot-cell` scoped 颜色：
  - `background: rgba(20,28,58,0.6); border-color: rgba(0,240,255,0.15)`
  - `.snapshot-label` → `color: #8A94C6`
  - `.snapshot-value` → `color: #00F0FF`
  - `.snapshot-foot` → `color: #8A94C6`
- `.snapshot-meta` → `color: #8A94C6`

- [ ] **AppChannelsTab.vue**

- `<section class="panel">` → `<n-card>`（panel-heading → #header + #header-extra）
- 三个 `<button>` → `<n-button>`：
  - 主按钮"发起登录/重新生成/重新登录" → `<n-button type="primary">`
  - 次要按钮"刷新二维码" → `<n-button>`
  - "解绑" → `<n-button type="error">`
- `<div class="topbar-actions">` → `<n-space>`（放在 #header-extra slot 内）
- `AuthChallengeRenderer` 组件引用不变

- [ ] **AppKnowledgeTab.vue**

> 该文件的"上传文件"是 `<label class="secondary-button file-picker">` 包裹 `<input type="file">`（绝对定位 opacity:0）。**保留此 label 方案**，不换 n-button。

- `<section class="panel">` → `<n-card>`（panel-heading → #header + #header-extra，label 移入 #header-extra）
- 删除按钮：`<button class="secondary-button danger">` → `<n-button type="error" size="small">`
- `<table>` → `n-data-table`，columns：名称、大小、类型、操作（删除按钮）
- 保留 scoped CSS `.file-picker` 样式（input 绝对定位），更新 `.file-picker.disabled` opacity 即可

- [ ] **AppWorkspaceTab.vue**

- `<section class="panel">` → `<n-card>`
- 下载归档按钮（顶部）→ `<n-button>` 放入 #header-extra
- `<table>` → `n-data-table`，columns：名称（文件夹可点击）、大小、操作（下载文件）
  - 文件夹名：render 中 `h('strong', { class: 'folder', onClick: () => enter(entry) }, entry.name + '/')`
- 当前路径行中的"返回上级" `<button>` → `<n-button size="small" @click="goUp">`
- 下载文件 `<button>` → `<n-button size="small">`
- 更新 scoped CSS：`.folder` → `color: #00F0FF; text-decoration: underline dotted`

- [ ] **TypeScript 检查 + Commit**

```bash
cd web && npm run typecheck 2>&1 | tail -5
git add web/src/pages/apps/AppOverviewTab.vue \
        web/src/pages/apps/AppRuntimeTab.vue \
        web/src/pages/apps/AppChannelsTab.vue \
        web/src/pages/apps/AppKnowledgeTab.vue \
        web/src/pages/apps/AppWorkspaceTab.vue
git commit -m "feat(ui): App tab 页换 n-card + n-button + 深色 scoped CSS"
```

---

## Chunk 8: 登录页 + 视觉验收

### Task 18: LoginPage.vue — n-input + n-button

**文件：** `web/src/pages/login/LoginPage.vue`

> `AuthLayout.vue` 的 `.auth-shell` 背景已在 base.css 中更新，无需改 AuthLayout。
> 只需把 LoginPage 中的原生 `<input>` 换 `<n-input>`，`<button>` 换 `<n-button type="primary">`。

- [ ] **重写 LoginPage.vue**

```vue
<template>
  <div class="login-form">
    <div>
      <p class="eyebrow">OpenClaw Manager</p>
      <h1>登录控制台</h1>
    </div>

    <n-form-item label="账号" :show-feedback="false">
      <n-input
        v-model:value="username"
        autocomplete="username"
        placeholder="platform-admin"
        size="large"
        @keyup.enter="onSubmit"
      />
    </n-form-item>

    <n-form-item label="密码" :show-feedback="false">
      <n-input
        v-model:value="password"
        type="password"
        autocomplete="current-password"
        placeholder="请输入密码"
        size="large"
        show-password-on="click"
        @keyup.enter="onSubmit"
      />
    </n-form-item>

    <p v-if="errorMessage" class="state-text danger">{{ errorMessage }}</p>

    <n-button
      type="primary"
      size="large"
      block
      :loading="auth.loading"
      @click="onSubmit"
    >
      {{ auth.loading ? '登录中…' : '登录' }}
    </n-button>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NFormItem, NInput } from 'naive-ui'

import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()
const username = ref('')
const password = ref('')
const errorMessage = ref<string | null>(null)

async function onSubmit() {
  errorMessage.value = null
  try {
    await auth.login(username.value, password.value)
    const target = (router.currentRoute.value.query.redirect as string | undefined) ?? '/'
    await router.replace(target)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '登录失败'
  }
}
</script>
```

- [ ] **TypeScript 检查**

```bash
cd web && npm run typecheck 2>&1 | tail -5
```

- [ ] **Commit**

```bash
git add web/src/pages/login/LoginPage.vue
git commit -m "feat(ui): LoginPage 换 n-input + n-button，配合 base.css 深色背景"
```

---

### Task 19: 视觉验收

- [ ] **启动 dev server**

```bash
cd web && npm run dev
```

- [ ] **核对清单（在浏览器逐页检查）**

| 页面 | 检查点 |
|---|---|
| 登录页 | 深色背景+模糊光效、输入框深色、按钮青色渐变 |
| 仪表盘首页 | 4 个 metric 卡片、图表占位、应用队列表格、侧栏 |
| 侧边栏 | 深色背景、激活项青色高亮、Logo 渐变 |
| 顶栏 | 模糊玻璃背景、状态 tag、刷新按钮 |
| 应用列表 | n-data-table 深色、删除 modal 正常弹出 |
| 成员管理 | 表格 + 内联创建表单、delete/reset modal |
| 组织/审计/节点 | 表格深色、状态 tag 颜色正确 |
| 平台总览 | n-statistic 卡片格网 |
| 用量页 | n-tabs tab 切换、过滤 input |
| App 详情 | n-tabs 路由 tab、Runtime 资源指标卡片深色 |

- [ ] **TypeScript 最终检查**

```bash
cd web && npm run typecheck 2>&1
```

期望：0 errors

- [ ] **最终 Commit**

若前述各 Task 已逐一提交，此处无新文件需要提交，运行 `git status` 确认工作区干净即可。

若有遗漏文件未提交，按实际情况添加具体文件（勿使用 `git add -u` 以免带入不相关改动）：

```bash
git status  # 确认哪些文件未暂存
# 根据 git status 输出，按实际情况逐文件添加，例如：
# git add web/src/pages/xxx/YyyPage.vue
git commit -m "chore(ui): 深色主题 Naive UI 全面接入完成，视觉验收通过"
```

---

## 常见问题速查

**n-data-table render 函数中 h() 需要从 'vue' 导入**
```ts
import { h } from 'vue'
```

**n-menu 激活项不正确**
检查 `activeKey` computed 中的前缀列表是否覆盖所有路由前缀，确保路由 `/platform/dashboard` 排在 `/platform` 前（更长的前缀优先）。

**n-modal 关闭时调用两次 onCancel**
原因：`@update:show="(v) => { if (!v) onCancel() }"` + `@mask-click="onCancel"` 重叠。移除 `@mask-click`，只用 `@update:show`。

**scoped CSS 中深色颜色参考**
- 背景卡片：`rgba(20,28,58,0.6)` 或 `rgba(15,21,53,0.8)`
- 主要文字：`#FFFFFF`
- 次要文字：`#8A94C6`
- 边框：`rgba(0,240,255,0.15)`
- 成功绿：`#00FF88`、警告黄：`#FFB800`、错误红：`#FF3B5C`
