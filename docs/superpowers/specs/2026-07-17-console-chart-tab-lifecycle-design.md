# 平台控制台图表 Tab 生命周期修复设计

## 背景

平台管理员访问 `/console` 后，可在「Token 趋势」「各企业用量」「实例状态」三个 Tab 之间切换。当前连续往返切换后，三个图表会依次变为空白，但顶部统计卡片仍有数据。

2026-07-17 已在本地 `http://ocm.localhost` 使用平台管理员账号和真实 Chromium 无头浏览器稳定复现：

1. 首次进入时 Token 趋势正常显示；
2. 依次切换至各企业用量、实例状态，再切回 Token 趋势；
3. 第一轮结束时 Token 趋势已经空白；
4. 第二轮切换后，三个 Tab 均不再包含 ECharts canvas；
5. API 数据与顶部统计卡片仍正常，浏览器没有相关 JavaScript 异常或请求失败。

## 根因

`ConsolePage.vue` 的 `n-tabs` 启用了 `animated`，三个 `n-tab-pane` 使用 Naive UI 2.44.1 的默认 `displayDirective="if"`。

切换 Tab 时，Naive UI 会销毁离开 Tab 的 pane DOM。ECharts canvas 随旧的 `.chart-container` 一同被移除，但页面脚本持有的 `tokenChart`、`orgChart`、`statusChart` 变量仍指向旧实例。

再次进入 Tab 时，Vue 创建新的 `.chart-container`，现有 `onTabChange` 因图表变量非空而只调用旧实例的 `resize()`，不会对新容器执行 `init()`。因此新容器没有 canvas，连续切换后三个图表全部空白。

## 目标

- 三个图表 Tab 连续往返切换后仍能正常显示；
- 保持现有 API、轮询频率、统计口径和图表配置不变；
- 保持图表按首次访问延迟创建，避免初次进入页面时同时初始化三个 ECharts；
- 使用最小改动修复 DOM 与 ECharts 实例生命周期不一致的问题。

## 不在范围内

- 修改控制台布局、图表样式或统计口径；
- 修改后端接口、OpenAPI 或生成的前端类型；
- 重构三个图表为通用图表组件；
- 增加时间范围筛选、手动刷新或其他控制台功能；
- 保证现有 Tab 滑动动画的表现完全不变。

## 方案对比

### 方案一：TabPane 使用 `show:lazy`（采用）

为三个 `n-tab-pane` 设置 `display-directive="show:lazy"`。每个 pane 在首次访问时创建，此后通过显示状态切换而不销毁 DOM。

优点：

- ECharts 实例始终绑定原容器，不会产生悬空实例；
- 保留按首次访问延迟初始化的行为；
- 改动只涉及三个 pane 的显示策略，风险与维护成本最低。

取舍：pane 首次访问并保留后，Naive UI 的切换动画表现可能弱化。稳定显示优先，动画不作为本次验收条件。

### 方案二：检测容器变化并重建 ECharts

每次进入 Tab 时比较图表实例的 `getDom()` 与当前 Vue ref；不一致时销毁旧实例并在新容器重新初始化。

该方案能保留默认 pane 销毁行为，但需为三个图表增加重复的实例迁移逻辑，还要处理动画期间新旧 pane 同时存在的时序，复杂度和回归风险更高，因此不采用。

### 方案三：拆分独立图表组件

将三个图表分别拆成组件，在各自的挂载和卸载钩子中管理 ECharts。

该方案生命周期边界最明确，但对本次局部缺陷改动过大，会引入无关结构调整，因此不采用。

## 前端设计

仅修改 `web/src/pages/platform/ConsolePage.vue`：

- Token 趋势 pane 设置 `display-directive="show:lazy"`；
- 各企业用量 pane 设置 `display-directive="show:lazy"`；
- 实例状态 pane 设置 `display-directive="show:lazy"`；
- 保留 `animated`、`activeTab`、`onTabChange`、数据 watch、窗口 resize 和卸载时 dispose 逻辑；
- 不改变查询 hook、错误态、空数据态或 ECharts option。

首次进入页面时只渲染 Token 趋势 pane。首次打开其他 pane 后，`onTabChange` 在 `nextTick` 中初始化对应图表；之后切换只隐藏或显示既有容器，并对既有实例调用 `resize()`。

## 异常与边界处理

- 查询加载中或失败时仍使用现有状态文案，不创建图表；
- 各企业用量为空时继续显示「暂无数据」，不创建空图表；
- 数据轮询更新时，只有当前活动 Tab 立即重绘；非活动 Tab 再次显示时由 `resize()` 恢复正确尺寸；
- 页面离开时仍统一移除 resize 监听并 dispose 三个已创建的实例。

## 测试与验收

### 自动化回归

增加平台控制台定向浏览器测试，使用确定性的三个接口响应，避免依赖本地 new-api 用量波动。测试步骤：

1. 以平台管理员登录并进入 `/console`；
2. 确认初始 Token 趋势包含 ECharts canvas；
3. 依次打开各企业用量与实例状态，确认对应活动图表包含 canvas；
4. 连续执行至少三轮「Token 趋势 → 各企业用量 → 实例状态」切换；
5. 每次切换后确认活动 pane 的图表容器仍包含 canvas，且容器宽高均大于零。

测试方法和每个切换场景补充相邻中文注释，断言使用项目既有 Playwright `expect` 模式。

### 本地真实浏览器验证

修复后使用 Chromium 无头模式访问 `http://ocm.localhost`：

- 按复现路径连续切换至少三轮；
- 确认三个图表均持续显示；
- 确认顶部统计卡片与加载、错误、空数据状态没有回归；
- 确认浏览器控制台无新增异常，相关 API 无新增失败请求。

### 定向回归

运行新增的控制台定向 spec。由于改动仅影响平台控制台 Tab 的 DOM 保留策略，不执行全量 E2E。

## 成功标准

- 本地原始复现步骤不再导致任一有数据图表变空；
- 三轮连续切换后，三个图表各自仍绑定有效 canvas；
- 相关定向自动化测试通过；
- 工作区不包含临时复现脚本、截图或无关改动。
