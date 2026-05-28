# 任务看板状态汉化设计

## 背景

任务看板页面当前在部分位置直接展示 Hermes Kanban 状态原值，例如左侧分组标题
`Running`、详情状态条 `RUNNING`、执行历史里的 `run.status`。这些值适合作为前后端
契约字段，但不适合作为中文后台页面的最终展示文案。

本次只处理任务看板页面的状态展示，不改变后端 API、状态流转、操作按钮能力判断或
任务数据结构。

## 目标

- 任务看板状态统一展示为中文。
- 状态值映射集中维护，避免多个组件各自写本地字典。
- Hermes 新增未知状态时页面仍显示可诊断信息，避免空白或误导性文案。
- 补充状态映射测试，锁定展示契约。

## 状态映射

| 原始状态 | 中文文案 | 说明 |
|---|---|---|
| `running` | 运行中 | 任务正在执行。 |
| `ready` | 就绪 | 任务已准备执行。 |
| `todo` | 待办 | 任务待处理。 |
| `blocked` | 阻塞 | 任务被阻塞。 |
| `triage` | 待分诊 | 任务等待分类或确认。 |
| `done` | 已完成 | 任务已结束且成功完成。 |
| `archived` | 已归档 | 任务已归档，不再作为活跃任务处理。 |

未知状态显示为 `未知状态：<原始状态>`，视觉语义使用 warning，便于灰度或升级期间
发现前端尚未同步的新状态。

## 方案

在 `web/src/domain/status.ts` 新增 `formatKanbanStatus(status)`，沿用现有
`formatAppStatus`、`formatOrgStatus` 的结构，返回 `StatusView`：

- `label`：中文展示文案。
- `tone`：状态视觉语义，供后续需要 badge 颜色时复用。

任务看板组件只消费格式化函数，不直接维护中文状态字典：

- `KanbanTaskList.vue` 的状态分组定义保留状态顺序，但分组标题使用
  `formatKanbanStatus(status).label`。
- `KanbanTaskDetail.vue` 的顶部状态条使用中文标签。
- `KanbanTaskDetail.vue` 的历次执行状态列使用同一函数；缺失状态仍显示 `—`。

## 测试

在 `web/src/domain/status.test.ts` 中补充 `formatKanbanStatus` 单元测试：

- 覆盖全部已知 Kanban 状态的中文映射。
- 覆盖未知状态的降级展示。

组件层不新增复杂交互测试；本次改动是纯展示映射，单元测试能覆盖核心契约。

## 非目标

- 不修改 OpenAPI 或后端接口。
- 不调整任务状态流转、操作按钮、能力降级逻辑。
- 不把 assignee、priority、workspace 等字段一并汉化。
- 不修改构建产物或现有无关文档。
