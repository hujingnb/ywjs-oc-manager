# 用量页面体验优化设计

## 背景

当前“用量”页面已能按平台、组织和成员维度查询 new-api 用量，但体验偏原始：

- 成员和应用筛选依赖手工输入 ID。
- 只有动态表格，没有趋势图和汇总指标。
- `DATE` 列为空，`model_name` 为空时没有可读兜底。
- 充值弹框仍以“点数”作为主要文案，和 new-api 里可配置的金额 / 额度展示口径不一致。

本次设计明确：manager 不管理 token 单价，不做自己的金额记账；金额、余额和用量展示都以
new-api 配置与接口返回为事实来源。

## 目标

- 用量页支持可搜索的组织、成员、应用选择器，不再要求用户手填 UUID。
- 当前筛选条件变化时，Token 总量、金额 / 额度、调用次数、折线图和表格同步变化。
- 增加折线图，让按时间的 Token / 金额趋势更直观。
- 修复空 `DATE` 和空 `model_name` 的展示问题。
- 组织充值弹框按 new-api 口径展示余额、用量和充值输入，不在 manager 配置单价。
- 保持 manager 对 new-api 的薄代理原则，不新增本地用量缓存。

## 非目标

- 不在 manager 中配置或保存 token 单价。
- 不新增人民币订单、发票、支付或真实财务记账。
- 不把用量明细落库到 manager。
- 不重做整个组织管理页，只调整充值弹框与相关展示文案。

## 数据来源

### new-api 状态配置

manager 需要透传 new-api `/api/status` 中与显示相关的字段，供前端统一格式化金额 / 额度：

- `quota_per_unit`
- `quota_display_type`
- `display_in_currency`
- `custom_currency_symbol`
- `custom_currency_exchange_rate`
- `usd_exchange_rate`
- `price`

这些字段只用于展示折算。manager 不保存它们，也不允许用户在 manager 修改它们。

### 用量数据

现有 usage 接口继续作为用量数据来源：

- 平台 / 组织维度：`QuotaDate[]`，字段包括 `created_at`、`date`、`model_name`、`count`、
  `quota`、`token_used`。
- 成员 / 应用维度：`LogEntry[]`，字段包括 `created_at`、`model_name`、`quota`、
  `prompt_tokens`、`completion_tokens`、`token_id`。

`QuotaDate` 需要兼容 new-api 返回的 `created_at`。如果 `date` 为空，前端用 `created_at`
转换为本地日期展示和折线图横轴。

### 充值与余额

组织余额继续通过 `/api/v1/organizations/:orgId/balance` 直查 new-api 用户余额。充值提交仍使用
现有 `/api/v1/organizations/:orgId/recharge`，后端继续调用 new-api 充值接口。

充值记录仍保留现有 `credit_amount`，含义是传给 new-api 的展示额度值，不代表 manager 自定义金额。

## 页面设计

用量页采用“筛选栏 → 汇总卡片 → 折线图 → 明细表”的单页分析布局，保留角色权限裁剪：

- 平台管理员：可选择平台、组织、成员、应用视角；组织、成员、应用选择器均为可搜索。
- 组织管理员：可选择组织、成员、应用视角；组织固定为当前组织。
- 组织成员：只看“我的用量”和应用视角；成员固定为当前用户。

### 筛选栏

筛选栏包含：

- 视角选择：平台 / 组织 / 成员 / 应用，按角色显示可用选项。
- 组织选择：平台管理员可搜索选择组织。
- 成员选择：组织或平台视角下，根据当前组织加载成员列表；用 `display_name` + `username`
  作为 label，值为 user id。
- 应用选择：根据当前组织加载应用列表；用应用名作为 label，值为 app id。

成员和应用选择器替代原来的 ID 输入框。

### 汇总卡片

汇总指标完全基于当前筛选返回的 `items` 计算：

- Token 总量：平台 / 组织使用 `token_used`；成员 / 应用使用
  `prompt_tokens + completion_tokens`，缺失时回退到 0。
- 金额 / 额度：使用 `quota` 汇总后按 new-api 状态配置格式化。
- 调用次数：平台 / 组织使用 `count` 汇总；成员 / 应用使用日志条数或 `total`。
- 模型数：按非空 `model_name` 去重；空值归为“未知模型”。

筛选条件变化时，卡片不保留旧值；数据加载中展示 loading，成功后展示新值。

### 折线图

折线图按日期聚合当前筛选的 `items`：

- X 轴：日期。
- Y 值：Token 和 quota 两条线。Token 线展示 Token 用量；quota 线用于金额 / 额度趋势。
- 空数据时展示空态，不渲染误导性的 0 线。

项目当前没有图表库。实现优先使用轻量 SVG 折线图组件，避免为一个趋势图引入新依赖。组件应只接收
聚合后的点数据，不直接了解 usage API 结构。

### 明细表

明细表不再直接按首行动态取列，而是按当前视角使用稳定列：

- 平台 / 组织：日期、模型、调用次数、Token、金额 / 额度。
- 成员 / 应用：时间、模型、Token、金额 / 额度、token 名称、调用耗时。

空 `model_name` 展示“未知模型”。空 `date` 使用 `created_at` 转换；两者都缺失时展示“—”。

## 充值页设计

组织列表的充值弹框改为 new-api 口径：

- 当前余额展示为“剩余额度 / 已用额度”，按 new-api 状态配置格式化。
- 输入框文案从“充值点数”改为“充值额度”，旁边提示“按 new-api 配置折算，manager 不维护单价”。
- 成功反馈使用同一格式化函数展示充值额度。

后端接口不新增金额字段，不变更数据库 schema。

## 测试与验收

- 单元测试覆盖：
  - 用量格式化函数：空 `date`、空 `model_name`、Token 汇总、quota 格式化。
  - 用量页：成员 / 应用选择器使用当前组织数据，筛选变化后查询目标变化。
  - 充值弹框：文案和格式化结果符合 new-api 状态配置。
- 浏览器测试：
  - 平台管理员进入 `/usage`，检查平台、组织、成员、应用视角可切换。
  - 组织管理员进入 `/usage`，确认平台视角不可见，成员 / 应用可搜索。
  - 组织成员进入 `/usage`，确认只看到自己的成员用量和应用入口。
  - 检查 Token 总量、金额 / 额度、折线图、表格随筛选条件变化。
  - 检查 `DATE` 不再为空，空模型名展示“未知模型”。
  - 打开组织充值弹框，确认余额 / 已用量直接来自 new-api，充值文案不再暗示 manager 管理单价。
