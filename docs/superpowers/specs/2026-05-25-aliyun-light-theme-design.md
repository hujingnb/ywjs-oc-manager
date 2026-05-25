# 设计文档：全站切换阿里云控制台浅色风格

**日期：** 2026-05-25
**状态：** 待用户审核

## 背景

当前前端整体是深蓝/青紫的暗色霓虹风格：`App.vue` 使用 Naive UI `darkTheme`，`web/src/styles/base.css` 中大量基础色值也围绕深色背景、青色描边和紫色渐变展开。用户希望“换一下页面的色系，风格参考阿里云”。

本次讨论已确认采用全站逐页打磨方案：登录页、后台外壳、控制台、业务页面、图表和通用组件统一切到阿里云控制台式浅色工作台，而不是只替换几个主题变量。

调研参考：

- 阿里云管理控制台公告显示，控制台暗色模式已于 2026-04-21 下线，当前参考应以浅色控制台为准。
- Alibaba Cloud Console Components 文档强调控制台视觉规范和体验一致性；本项目不引入其 React 组件，只参考视觉方向。

## 目标

- 全站从暗色霓虹风切换为阿里云控制台式浅色风格。
- 登录页、后台布局、菜单、表格、卡片、图表、弹窗、表单和空状态保持一致色系。
- 主操作和导航 active 状态使用橙色；信息链接和图表主线使用蓝色。
- 清理旧的青紫霓虹、深色卡片、深色表格和高发光阴影残留。
- 不改变业务逻辑、路由结构、权限判断、API 契约和数据请求行为。

## 非目标

- 不切换或引入阿里云 React 组件库。
- 不新增暗色/浅色主题切换。
- 不重排业务信息架构，不改变导航项的权限裁剪。
- 不借视觉改版做无关重构、页面功能调整或 API 变更。

## 视觉系统

整体风格采用“浅灰工作区 + 白色内容面 + 细边框 + 低阴影”的控制台视觉。

建议语义色：

| 用途 | 色值 | 说明 |
| --- | --- | --- |
| 主色 / 主按钮 / 当前导航 | `#ff6a00` | 参考阿里云橙，承载高优先级操作和选中态 |
| 信息 / 链接 / 图表主线 | `#1677ff` | 用于链接、信息图表和辅助强调 |
| 成功 | `#16a34a` | 用于正常、成功、运行中 |
| 警告 | `#f59e0b` | 用于需要注意但非失败的状态 |
| 危险 | `#d93026` | 用于失败、删除、异常错误 |
| 正文 | `#1f2329` | 主文字 |
| 次级文字 | `#6b7280` | 标签、说明、表头辅助信息 |
| 弱文字 | `#8a94a6` | 低优先级说明 |
| 边框 | `#e5e7eb` | 卡片、表格、布局分隔线 |
| 页面背景 | `#f5f7fa` | 工作区底色 |
| 内容背景 | `#ffffff` | 卡片、表格、弹窗、登录面板 |

设计约束：

- 页面主体不再使用深色线性渐变、青色发光描边和紫色渐变品牌块。
- 卡片圆角控制在现有系统附近，避免营销页式大圆角。
- 交互态使用浅橙背景、浅蓝背景或浅灰 hover，不使用强发光效果。
- 文本保持清楚对比度，尤其是表格表头、标签、空状态和图表坐标轴。

## 布局和导航

现有布局结构保持不变：

- `DashboardLayout.vue` 继续承载已登录后台外壳。
- `n-layout` / `n-layout-sider` / `n-layout-header` / `n-menu` 继续使用 Naive UI。
- `RouterView`、角色菜单裁剪、退出登录、刷新按钮等行为不变。

视觉调整：

- 侧栏改为白色或极浅灰底，右侧用浅边框分隔。
- 当前菜单项使用左侧橙色竖条和浅橙底突出。
- 顶栏改为白底细边框，环境标识、API 状态和刷新按钮使用浅色控件样式。
- 内容区使用浅灰背景，页面内面板、表格和统计卡使用白底。
- 登录页同步改为浅色品牌登录面板，主按钮使用橙色，背景不再使用青紫径向光斑。

响应式策略沿用现有断点，只验证浅色后的边框、菜单 active 状态和文字在窄屏仍清晰可读。

## 组件和页面范围

### 全局和布局层

| 文件 | 设计意图 |
| --- | --- |
| `web/src/App.vue` | 从 `darkTheme` 切换为浅色主题，重设 Naive UI theme overrides |
| `web/src/styles/base.css` | 重建全局基础色、按钮、表格、面板、登录页和状态 pill 色值 |
| `web/src/layouts/DashboardLayout.vue` | 调整品牌块、侧栏 footer、菜单周边和退出按钮颜色 |
| `web/src/layouts/AuthLayout.vue` / `web/src/pages/login/LoginPage.vue` | 登录流程保持不变，仅同步浅色登录视觉 |

### 通用组件

重点检查并调整：

- `DataTableList.vue`：标题、说明、链接色与表格周边色。
- `StatusBadge.vue` / `AppStatusTag.vue` / `RuntimeStatusTag.vue`：语义色与浅色背景可读性。
- `ConfirmActionModal.vue` / `UploadProgressModal.vue`：弹窗、危险操作和进度状态。
- `ResourceTrendChart.vue` / `JobProgressPanel.vue`：图表、说明文字和空状态。

### 业务页面

逐页打磨覆盖当前存在硬编码旧色、深色残留或图表配置的页面：

- 平台控制台：`web/src/pages/platform/ConsolePage.vue`
- 组织控制台：`web/src/pages/org/OrgConsolePage.vue`
- 用量页面和汇总：`web/src/pages/usage/UsagePage.vue`、`UsageSummary.vue`
- 运行节点列表/详情：`web/src/pages/runtime-nodes/RuntimeNodesPage.vue`、`RuntimeNodeDetailPage.vue`
- 知识库：`web/src/pages/knowledge/OrgKnowledgePage.vue`、`web/src/pages/apps/AppKnowledgeTab.vue`
- 审计日志：`web/src/pages/audit/AuditLogsPage.vue`、`web/src/pages/apps/AppAuditTab.vue`
- 成员、组织、实例和助手版本相关页面中所有显式旧色值。

实现时优先把颜色收敛到 CSS 变量或 Naive UI token；ECharts 配置保留在页面或图表组件内，但改成浅色坐标轴、浅网格线、蓝/橙系列色。

## 实施边界

允许改动：

- CSS 和 scoped style。
- Naive UI `GlobalThemeOverrides`。
- ECharts option 中的颜色、坐标轴、图例和网格线样式。
- 为承载样式而补充少量 class。

不允许改动：

- API client、接口契约、后端 handler/service/database。
- 路由权限、登录鉴权、菜单可见性逻辑。
- 表单字段、提交行为、查询条件和数据转换。
- 与视觉无关的组件拆分或业务重构。

## 风险和处理

| 风险 | 处理 |
| --- | --- |
| 深色或青紫旧色散落在页面内联样式中 | 用 `rg` 扫描十六进制色、`rgba(`、`linear-gradient`、`darkTheme`，逐页处理 |
| Naive UI 切浅色后默认变量变化 | 用 theme overrides 锁定关键语义色、文本色、边框色和表格色 |
| 图表在浅色背景下对比度不足 | 单独调整 ECharts 坐标轴、splitLine、legend 和 series 色值 |
| 移动宽度下菜单和内容边界不清楚 | 保留现有响应式结构，浏览器检查窄屏下可读性 |
| 视觉改版误触业务逻辑 | 只做样式与配色修改，测试和浏览器验证聚焦行为不变 |

## 验收标准

- 登录页、后台外壳、控制台、列表页、详情页、图表页都呈现统一浅色阿里云控制台风格。
- 页面无明显深色卡片、青紫霓虹描边、紫色渐变品牌块或旧发光阴影残留。
- 主按钮、当前菜单和关键操作使用橙色；链接和信息图表使用蓝色。
- 成功、警告、危险、禁用等语义状态在浅色背景下清楚可辨。
- 表格、表单、弹窗、空状态和图表坐标轴文字对比度足够。
- 桌面和移动宽度下没有文字溢出、控件重叠或布局错位。

## 验证计划

实现完成后至少运行：

```bash
cd web
npm run typecheck
npm run build
npm run test -- --run
```

真实浏览器验证至少覆盖：

- 登录页。
- 平台管理员控制台。
- 组织管理员控制台。
- 列表页：组织、成员、知识库、审计日志、运行节点。
- 详情页：实例详情、运行节点详情。
- 图表页：平台/组织控制台、用量汇总。
- 一个移动宽度视口。

如果某条验证无法运行，交付说明必须写明原因和剩余风险。
