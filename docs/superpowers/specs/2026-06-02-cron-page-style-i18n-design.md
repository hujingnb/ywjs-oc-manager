# 定时任务页面样式与文案优化 — 设计文档

- 日期：2026-06-02
- 范围：纯前端（`web/src`），不改动后端、OpenAPI 契约或数据库

## 背景与问题

实例 Hermes Cron 管理页（`web/src/pages/apps/AppCronTab.vue` 及 `cron/` 子组件）存在三个体验问题：

1. **字段展示不完整**：左侧任务列表固定 `420px` 宽，却用 `<table>` 塞了 5 列（名称 / 调度 / 状态 / 投递 / 下次执行）。每个单元格都是 `table-layout: fixed` + `white-space: nowrap` + `text-overflow: ellipsis`，导致「下次执行」「调度」「状态」几乎必然被截断，用户读不全。
2. **调度展示不可读**：调度列优先展示 `schedule.display`（面向用户的可读说明），但 `display` 由 Hermes 上游（oc-cron）生成，manager 后端只透传、不翻译。当上游未返回 `display` 时，前端回退展示原始 `expr`（如 `0 9 * * *` / `every 10m`），用户无法理解。
3. **状态 / 投递为英文**：状态标签直接显示 `scheduled` / `paused` 等英文原文，投递显示 `wechat` / `email`，工具栏筛选下拉的 label 也是英文，缺乏中文可读性。

## 目标

- 左侧列表字段完整展示，不再被截断。
- 调度在 `display` 缺失时由前端兜底翻译成中文可读文案。
- 状态、投递在用户可见处统一中文化，整页文案一致。

## 非目标

- 不在 manager 后端生成 `schedule.display` 或做任何后端翻译（契约保持透传语义）。
- 不改动详情面板「基础字段」技术区的英文原始 key（该区是面向管理员的原始字段视图，刻意保留英文）。
- 不改动 `statusSummary` 顶部摘要（按现有注释，产品要求保留英文 `Gateway cron running …`）。

## 现状关键事实（调查结论）

- 左侧列表组件：`web/src/pages/apps/cron/CronJobList.vue`，5 列固定宽 + nowrap ellipsis。
- 详情面板：`web/src/pages/apps/cron/CronJobDetail.vue`，顶部状态行 `● {{ job.state }}` 为英文，`scheduleText = display || expr || '—'`。
- 容器与筛选：`web/src/pages/apps/AppCronTab.vue`，`statusOptions` label 为英文，左右分屏 `420px + 1fr`。
- 数据类型：`web/src/api/hooks/useCron.ts` 的 `CronSchedule { kind?, expr?, display? }`、`CronJob { state?, deliver?, ... }`。
- `schedule.display` 来源：oc-cron 上游生成，manager 透传，可能为空。
- `expr` 格式：标准 5 段 cron（`0 9 * * *`）、`every Xm/Xh`、`at ...`；`kind` 取值 `cron` / `every` / `at`，旧数据可能为空。
- 前端无集中式 i18n 框架，文案均硬编码中文，翻译就近放在组件 / 共享工具函数。

## 设计

### 1. 新增共享展示工具 `web/src/pages/apps/cron/cronDisplay.ts`

集中纯函数，供列表、详情、筛选下拉复用，避免文案散落多处：

- `translateState(state?: string): string`
  - 映射：`scheduled→已调度`、`paused→已暂停`、`running→运行中`、`disabled→已禁用`、`error→错误`、`removed→已移除`。
  - 未知 / 空值：原样返回（空值返回 `unknown` 或 `—`，与现状对齐——现状列表用 `'unknown'`，保留该兜底）。
- `translateDeliver(deliver?: string): string`
  - 映射：`wechat→微信`、`email→邮件`、`none→不投递`。
  - 缺省 / 空值：返回 `—`。
- `translateCronExpr(kind?: string, expr?: string): string`
  - 覆盖标准 5 段 cron 常见模式：
    - `m h * * *` → `每天 HH:MM`
    - `m h * * D`（D 为 0-7 单值）→ `每周X HH:MM`
    - `m h D * *`（D 为日单值）→ `每月D日 HH:MM`
    - `0 * * * *` → `每小时`
    - `*/N * * * *` → `每 N 分钟`
  - `every` 格式（`every 10m` / `10m` / `every 1h`）→ `每 N 分钟` / `每 N 小时`。
  - `at` 格式 → `指定时间 <原值>`（无法进一步解析时保留原始时间串）。
  - **不可识别的复杂表达式：直接返回原始 `expr`，绝不抛错。**
- `scheduleDisplay(schedule?: CronSchedule): string`
  - 优先返回非空 `schedule.display`。
  - 否则调用 `translateCronExpr(kind, expr)`。
  - 翻译器若回退到原文，则结果即原始 `expr`；`expr` 也为空时返回 `—`。

> 设计取舍：翻译器以「尽力翻译 + 原文兜底」为原则（用户确认），保证任何上游格式都不会让页面报错或显示空白。

### 2. 左侧列表 `CronJobList.vue` 改为卡片式

将 `<table>` 五列结构替换为每个任务一个多行卡片（用户确认版式）：

```
┌──────────────────────────────────┐
│ 每日数据报表          [● 已调度] │
│ job-abc123                       │
│ 调度  每天 09:00                 │
│ 下次  2026-06-03 09:00 · 微信    │
└──────────────────────────────────┘
```

- 第一行：任务名称（加粗，溢出可省略）+ 右侧状态标签（`<n-tag>`，文案用 `translateState`，颜色沿用现有 `stateTagType`）。
- 第二行：`job_id`（次要灰色小字）。
- 第三行：`调度  <scheduleDisplay(job.schedule)>`。
- 第四行：`下次  <next_run_at 兜底 —> · <translateDeliver(job.deliver)>`。
- 卡片宽度撑满 420px，正文不再 `nowrap` 截断（必要处仅对单行任务名做 ellipsis）。
- 保留 hover 与 selected 选中态（左侧色条 + 背景），保留缺少 `id` 行不可选的防御逻辑。
- 空列表保留现有 `<n-empty>` 与最小高度，避免分屏高度跳动。

### 3. 右侧详情 `CronJobDetail.vue` 一致化

- 顶部状态行 `● {{ job.state }}` → 用 `translateState(job.state)`，即 `● 已调度`。
- `scheduleText` 由 `display || expr || '—'` 改为调用 `scheduleDisplay(job.schedule)`，使兜底翻译一致生效。
- 「基础字段」技术区**保持英文 key 与原始 value 不动**（管理员原始字段视图）。

### 4. 工具栏筛选下拉 `AppCronTab.vue`

- `statusOptions` 的 `label` 翻成中文（`scheduled→已调度` 等），`value` 不变（仍是后端识别的英文）。「全部状态」保留。
- `statusSummary` 顶部摘要保持英文，不动。

### 5. 文案微调

- 卡片化后列表表头「名称」概念消失，无需再改表头文案。
- 表单弹窗 `CronJobFormModal.vue` 的 name placeholder 已是「任务名称」，无需改动。

## 数据流

无新增数据流。所有翻译都是前端在渲染层对既有字段（`state` / `deliver` / `schedule`）做纯函数转换，不改变请求、响应或状态管理。

## 错误处理与边界

- 翻译器对未知 / 空 / 畸形输入一律安全回退（原文或 `—`），不抛异常。
- `display` 已存在时不触发翻译器，尊重上游文案。
- 列表行缺 `id` 时不可选，沿用现有逻辑。

## 测试

- 新增 `web/src/pages/apps/cron/cronDisplay.spec.ts`：
  - `translateState` / `translateDeliver` 各状态与投递映射、未知值回退。
  - `translateCronExpr`：每天 / 每周 / 每月 / 每小时 / 每 N 分钟、`every Xm/Xh`、`at`，以及不可识别表达式回退原文。
  - `scheduleDisplay`：`display` 优先、`display` 缺失走翻译、`expr` 缺失返回 `—`。
- 更新 `CronJobList` 相关测试（若断言旧表格结构）：断言卡片渲染、中文状态、翻译后调度文案。
- 复跑现有 `AppCronTab.spec.ts`、`CronJobFormModal.spec.ts`，确保未回归。
- 真实浏览器验证（本地 k3d）：列表字段完整、调度中文可读、状态 / 投递中文、筛选下拉中文、详情一致。

## 影响范围

- 修改：`CronJobList.vue`、`CronJobDetail.vue`、`AppCronTab.vue`。
- 新增：`cronDisplay.ts`、`cronDisplay.spec.ts`。
- 不涉及后端、OpenAPI、数据库。
