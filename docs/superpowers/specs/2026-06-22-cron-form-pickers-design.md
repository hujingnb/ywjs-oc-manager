# 定时任务表单点选化改造 — 设计文档

- 日期：2026-06-22
- 范围：纯前端（`web/`），不改后端 / OpenAPI 契约 / oc-ops 透传契约
- 触发：用户反馈「新建定时任务」表单全是手填裸字段（cron 表达式、渠道名、脚本文件名、目录），要求改成点选式、降低使用门槛

## 背景与现状

「新建定时任务」表单组件：`web/src/pages/apps/cron/CronJobFormModal.vue`。
当前所有字段都是纯文本/数字输入，无任何枚举或选择器：

| 字段 | 现状 | 问题 |
|---|---|---|
| schedule* | 文本框「cron 或 every 表达式」 | 要手写 `cron 0 9 * * *`，普通用户不会写 |
| deliver | 文本框「wechat / email / none」 | 要手记渠道名，且不知道自己绑了哪些 |
| repeat | 数字框 | 与 schedule 含义割裂，用户以为该合并 |
| script | 文本框「仓库内脚本文件名」 | 要手记文件名 |
| no_agent | 复选框「跳过 agent 执行路径」 | 文案看不懂 |
| workdir | 文本框「任务运行目录」 | 普通用户用不到、易混淆 |

后端 / 数据源约束（已核实）：

- 创建 cron：`POST /api/v1/apps/{appId}/hermes/cron/jobs`，body 为 `CreateCronJobRequest`。
  `schedule` 是单个字符串，接受 `cron <expr>` / `every <dur>` / `at <iso>`，manager 原样透传 hermes，不解析内容。
- 渠道：**无「一次性列出已绑定渠道」接口**。前端在 `AppChannelsTab.vue` / `ChannelLogo.vue` 硬编码渠道类型清单，目前仅 `wechat` 为 `supported`；绑定状态靠 `useChannelProgressQuery`（`GET .../channels/{type}/auth`，`status==='bound'` 表示已绑定）逐渠道查。
- 工作目录：`GET /api/v1/apps/{appId}/workspace?path=` 返回条目带 `is_dir` 区分文件/目录。**只读架构**，无 mkdir / 上传接口（写操作走 worker，避免 manager 直写运行中容器）。

## 设计目标

把表单从「裸字段手填」改为「点选为主、手填兜底」，且**完全不改后端**。

## 表单重组

整体分 4 个区块（替换现有平铺布局）：

```
① 基础   name*  ·  prompt
② 调度   schedule 可视化点选器*  ·  运行次数上限(原 repeat)
③ 投递   deliver 渠道下拉
④ 执行   script 文件点选  ·  no_agent(改文案)
   [平台管理员·高级]  skills / model / provider / base_url / workdir
```

- workdir 从普通字段移除，移入「平台管理员·高级」折叠区（后端本就归类 advanced）；普通用户不可见，任务跑默认工作目录。
- `payload` 构建逻辑（`buildPayload`）保持现有「创建省略空值 / 编辑空字符串表示清空」语义不变，仅字段来源从点选控件取值。

## ① 调度 schedule（核心）

新增子组件 `ScheduleField.vue`，对外 `v-model:value` 一个 schedule 字符串（与后端契约一致），内部三模式 Tab 切换：

### 模式 A「按天/周 + 时间点」（默认）

- 频率：单选 `每天` / `每周`；选「每周」时展开周一至周日多选。
- 时间点：可添加多个 `HH:MM`，可逐个删除，至少一个。
- 生成规则，输出 `cron <minute> <hour> * * <dow>`：
  - `minute` = 各时间点分钟去重升序，逗号连接
  - `hour` = 各时间点小时去重升序，逗号连接
  - `dow` = `每天` → `*`；`每周` → 勾选项（1=周一…7/0=周日，按 hermes/标准 cron 约定，实现时核对）逗号连接
  - 示例：每周一~五 09:00、18:00 → `cron 0 9,18 * * 1-5`（或 `1,2,3,4,5`）
- **cron 单表达式笛卡尔积限制**：当多个时间点分钟不一致（如 09:15、18:45），`minute×hour` 会多触发（09:45、18:15）。
  对策：组件下方常驻「实际运行」实时人类可读预览（复用 `cronDisplay.ts` 的翻译能力，从生成的表达式反推），把真实触发点直接摊给用户看。整点 / 同分钟的常见场景精确无歧义；分钟不一致时用户能立刻看到多余触发点并调整。**不做静默兜底、不隐藏多触发**。

### 模式 B「按间隔」

- 控件：`每 [N] [分钟/小时/天]` → 生成 `every 10m` / `every 2h` / `every 1d`。

### 模式 C「高级表达式」

- 保留原文本框，直接手写 `cron ...` / `every ...` / `at ...`，作为所有用户可用的逃生口。

### 回填（编辑态）

- 按 `job.schedule.kind` 选模式：`every` → 模式 B 解析 `expr`；`cron` 且能解析成「纯天/周 + 时间点」→ 模式 A 回填；否则落模式 C 直接放 `expr`。
- 解析失败一律安全降级到模式 C，不丢原值。

### 运行次数上限（原 repeat）

- 紧跟调度区块下方，`n-input-number`，文案「运行次数上限」，help：「留空 = 一直按计划运行；填 N = 运行 N 次后停止」。
- 提交 / 清空语义沿用现有逻辑（`repeat>0` 才发送，编辑态清空暂不支持 `clear_repeat`，与现状一致）。

## ② 投递 deliver（下拉）

- 新增/内联一个渠道下拉：选项 = `不投递`（值空串）+ 前端已知 `supported` 渠道中 `status==='bound'` 的项，显示中文名（复用 `DELIVER_LABELS` / `ChannelLogo`）。
- 数据源：复用 `useChannelProgressQuery` 对 supported 渠道查状态（目前实际只有 `wechat`）。
- 空态：无任何已绑定渠道时，下拉仅「不投递」，并在下方提示「去『渠道』页绑定后可在此选择」。
- 编辑态：若 `job.deliver` 是个当前未在已绑定列表里的值，仍把它作为一项显示（避免回填丢值）。
- 纯前端，无后端改动。

## ③ script（文件点选）

- 字段旁加「选择文件」按钮，弹出选择器列 `GET .../workspace`（根目录）下 `is_dir===false` 的文件。
- 后端要求 script 为无路径单文件名，故只列根层文件、回填 `name`（basename）。
- 保留文本框手输兜底（文件尚未上传时）。

## ④ no_agent（仅文案）

- 复选框文案：「不使用 AI，仅运行脚本」。
- 旁置 `?` tooltip：「勾选后跳过 AI agent，直接执行 script 指定脚本（更快、不消耗 token），适合纯脚本任务；不勾选则由 AI 按 prompt 执行。」
- 字段语义、提交值不变（仍是 `no_agent` 布尔）。

## 平台管理员·高级

- 现有 `isPlatformAdmin` 分支保持；workdir 移入该折叠区，文案与现状一致（纯文本框，平台管理员自负责）。

## 组件拆分

- `CronJobFormModal.vue`：负责区块布局、payload 组装、提交（瘦身，主要改 template 与字段来源）。
- `ScheduleField.vue`（新）：schedule 三模式点选 + 实时预览，对外只暴露一个 schedule 字符串。
- `WorkspaceFilePicker.vue`（新，可选复用）：列 workspace 文件供 script 选择。
- deliver 下拉若简单可内联在 Modal 内，不强行拆组件。

边界原则：`ScheduleField` 对调用方只暴露「字符串进、字符串出」，cron 拼装/解析全封装在内部，Modal 不感知 cron 语法。

## 测试

沿用 `CronJobFormModal.spec.ts` 既有风格（vitest + testify 思路对应的前端断言）：

- `ScheduleField`：
  - 模式 A 每天单时间点 → `cron 0 9 * * *`
  - 模式 A 每周多日多时间点 → `cron 0 9,18 * * 1-5`
  - 模式 A 分钟不一致 → 生成笛卡尔积表达式 + 预览反映多触发（断言预览文本）
  - 模式 B → `every 10m`
  - 回填：`every` / 可解析 cron / 不可解析 cron 分别落到正确模式
- deliver 下拉：仅 bound 渠道入选项；空态仅「不投递」；编辑态未知值保留。
- script 选择器：只列文件不列目录；回填 basename。
- `buildPayload` 既有用例保持绿（payload 语义不回归）。

## 非目标（YAGNI）

- 不新增任何后端接口（不补 mkdir、不补「列出渠道」批接口、不补「列脚本」接口）。
- 不支持「同一任务多套 schedule」（后端单字符串契约不变）。
- 不做 workdir 子目录隔离的普通用户入口。
- 不改 OpenAPI / `generated.ts`。
