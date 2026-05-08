# 深色主题 + Naive UI 全面接入 设计规格

## 背景与目标

参考 dashboard.html（AI 科技深色风格），将 oc-manager 管理后台从浅色主题重构为：

- **深色科技风主题**：深蓝背景 + 青色（#00F0FF）/ 紫色（#7B2EDA）发光渐变配色
- **全面接入 Naive UI**：以 Naive UI 的深色主题组件替换现有手写 CSS 类，减少自维护样式
- **仪表盘首页重构**：在现有 metric 卡片基础上增加应用队列、节点状态、快捷操作区布局

---

## 技术栈

| 依赖 | 现状 | 变更 |
|---|---|---|
| naive-ui | 已安装，未用深色主题 | 启用 darkTheme + 自定义 themeOverrides |
| lucide-vue-next | 已安装，继续使用 | 不变 |
| vue-echarts + echarts | 未安装 | **不在本次范围**，图表区留占位 card |
| tailwindcss | 未安装 | 不引入 |

---

## 文件改动范围

### 1. `web/src/App.vue`

在已有的 `NConfigProvider` 上补充 `darkTheme` 和 `themeOverrides`：

```ts
import { darkTheme } from 'naive-ui'

const themeOverrides = {
  common: {
    primaryColor: '#00F0FF',
    primaryColorHover: '#33F5FF',
    primaryColorPressed: '#00C8D4',
    bodyColor: '#0A0E27',
    cardColor: 'rgba(20,28,58,0.8)',
    borderColor: 'rgba(0,240,255,0.2)',
    textColorBase: '#FFFFFF',
    textColor1: '#FFFFFF',
    textColor2: '#8A94C6',
    textColor3: '#8A94C6',
  }
}
```

### 2. `web/src/styles/base.css`

重写全局 CSS 变量为深色系，保留所有现有类名（`.dashboard-shell`、`.sidebar`、`.panel` 等），只更新颜色值：

- `background: #f5f7fb` → `#0A0E27`
- `color: #172033` → `#FFFFFF`
- `.sidebar` 背景由 `#263548` → `rgba(10,14,39,0.95)`，`border-right` 加青色边框
- `.topbar` 背景 `#ffffff` → `rgba(10,14,39,0.5)` + `backdrop-filter: blur(12px)`
- `.panel` / `.metric-card` 背景 `#ffffff` → `rgba(20,28,58,0.8)`，边框改为青色半透明
- `.primary-button` 渐变替换纯色
- `.secondary-button` 改为深色半透明风格
- `.status-pill` 各色调适配深色背景

### 3. `web/src/layouts/DashboardLayout.vue`

将 `<aside class="sidebar">` + 手写 `<nav>` 替换为 Naive UI 布局组件：

- **整体壳**：`n-layout`（水平方向）
- **侧边栏**：`n-layout-sider`（collapsed-width=64，width=220，bordered）
- **导航**：`n-menu`（mode="inline"，:options 由 computed 生成，含 icon + label）
- **顶栏**：`n-layout-header`（bordered，position="absolute"）
- **内容区**：`n-layout-content`（`<RouterView />`）
- 底部用户信息 + 退出按钮保持现有逻辑，用 `n-button` + `n-avatar` 替换

`n-menu` 导航方式：`value` 绑定当前路由 path，监听 `@update:value` 调用 `router.push(key)`，不在 label 中嵌套 RouterLink：
```ts
// options computed
{ key: '/', label: '总览', icon: () => h(LayoutDashboard, { size: 18 }) }

// 模板
<n-menu
  :value="route.path"
  :options="menuOptions"
  @update:value="(key) => router.push(key)"
/>
```

### 4. `web/src/pages/dashboard/DashboardHome.vue`

全面重构，新增以下区域：

**Metric 卡片行**（`n-grid` cols=4 + `n-card` + `n-statistic`）：
- 组织数、应用总数、运行节点、今日调用 Token
- 每张卡下方加 `n-progress`（type="line"，色值跟主题色）
- 数值初期静态，后续替换为真实 API 数据

**主体双列网格**（`n-grid` cols=24）：
- 左侧（cols=17）：应用队列，用 `n-data-table`，columns 包含：应用名、节点、状态（`n-tag`）、操作（`n-button` 组）
- 右侧（cols=7）：
  - 节点状态 `n-card`：列出 RuntimeNode，含状态 dot + 名称
  - 快捷操作 `n-card`：重启服务、清理缓存、查看日志，各用 `n-button`

**图表区（本次范围外）**：留一个 `n-card` 占位，内部显示"Token 趋势 · 即将上线"文字，待后续迭代引入 vue-echarts 时填充。

### 5. 列表页统一替换（优先级顺序）

| 文件 | 改动 |
|---|---|
| `AppsPage.vue` | `<table>` → `n-data-table`；`.primary-button` → `n-button type=primary`；`.secondary-button` → `n-button` |
| `MembersPage.vue` | 同上；内联表单区替换为 `n-form` + `n-form-item` + `n-input` + `n-select` |
| `OrganizationsPage.vue` | `<table>` → `n-data-table` |
| `AuditLogsPage.vue` | `<table>` → `n-data-table` |
| `RuntimeNodesPage.vue` | `<table>` → `n-data-table` |
| `PlatformDashboardPage.vue` | `.metric-card` → `n-card` + `n-statistic`；`.panel` → `n-card` |
| `UsagePage.vue` + `UsageSummary.vue` | `.panel` → `n-card` |
| `OrgKnowledgePage.vue` | `.panel` → `n-card` |
| `PersonaPage.vue` | `.panel` → `n-card`；表单 → `n-form` |
| `AppDetailPage.vue` | `.tab-nav` → `n-tabs`（type="line"）；value 绑定 `route.params` 末段，`@update:value` 调用 `router.push(\`/apps/${appId}/${tab}\`)` |
| 各 App Tab（Overview/Runtime/Channels/Knowledge/Workspace）| `.panel` → `n-card`；表格 → `n-data-table` |

### 6. 通用组件

| 文件 | 改动 |
|---|---|
| `AppStatusTag.vue` | 返回 `n-tag`，type 根据 status 映射（success/warning/error/default） |
| `RuntimeStatusTag.vue` | 同上 |
| `ConfirmActionModal.vue` | 替换为 `n-modal`（不用 n-dialog，因为需要自定义 verify 输入框），内部用 `n-input`（verify 输入）+ `n-button` |
| `DataTableToolbar.vue` | 标题结构保持，按钮替换为 `n-button` |
| `JobProgressPanel.vue` | 进度展示改用 `n-progress` |
| `AuthChallengeRenderer.vue` | 按钮改 `n-button` |

### 7. 登录页 `LoginPage.vue`

- `auth-shell` / `login-form` 背景改深色
- `<input>` → `n-input`；`<button>` → `n-button type=primary`

---

## 主题色规格

| 变量 | 值 | 用途 |
|---|---|---|
| Primary | `#00F0FF` | 主按钮、激活状态、边框高亮 |
| Primary Hover | `#33F5FF` | 悬停 |
| Accent Purple | `#7B2EDA` | 渐变搭档色 |
| Success | `#00FF88` | 运行中状态 |
| Warning | `#FFB800` | 待配置状态 |
| Error | `#FF3B5C` | 异常状态 |
| Body BG | `#0A0E27` | 全局背景 |
| Card BG | `rgba(20,28,58,0.8)` | 卡片背景 |
| Border | `rgba(0,240,255,0.2)` | 边框 |
| Text Main | `#FFFFFF` | 主文字 |
| Text Sub | `#8A94C6` | 次要文字、标签 |

---

## 不改动范围

- 所有业务逻辑、API 调用、状态管理（Pinia store、TanStack Query hooks）
- 路由配置
- Go 后端代码
- `ConfirmActionModal` 的验证逻辑（只换 UI 组件）
- 测试文件（视觉变更不影响已有逻辑测试）

---

## 实施顺序

1. `App.vue`：启用 `darkTheme` + `themeOverrides`
2. `base.css`：重写全局深色变量
3. `DashboardLayout.vue`：换 `n-layout` + `n-menu`
4. `DashboardHome.vue`：全面重构首页
5. 通用组件：`AppStatusTag`、`RuntimeStatusTag`、`ConfirmActionModal`、`JobProgressPanel`
6. 列表页逐一替换（AppsPage → MembersPage → Organizations → 其余）
7. `AppDetailPage.vue` tab 换 `n-tabs`
8. 登录页深色适配
9. 端到端视觉验收
