# Hermes 任务看板设计

> 日期：2026-05-19
> 调研报告：`docs/superpowers/plans/2026-05-19-hermes-task-dashboard.md`
> 视觉参考：mockup v4（左侧 status 分组列表 + 右侧详情）；交互参照
> [EKKOLearnAI/hermes-web-ui](https://github.com/EKKOLearnAI/hermes-web-ui)

## 1. 背景

oc-manager 的实例详情页目前没有「正在执行的任务」可见。Hermes 内置 Kanban
系统提供了 triage/todo/ready/running/blocked/done/archived 七态任务板，
是 hermes-web-ui 等社区 UI 的核心视图。本期为实例详情页新增「任务」tab，
让组织成员能查看、管理本实例内的 Kanban 任务，覆盖看板浏览、详情查询、
实时执行流、写操作（评论/完成/阻塞/归档等）。

## 2. 目标 / 非目标

**目标**：
- 在实例详情页添加「任务」tab，对所有能看到实例详情的角色可见
- 完整覆盖 Hermes Kanban 的读 + 写能力（评论 / 完成 / 阻塞 / 解除阻塞
  / 归档 / 重新分配 / 释放 claim / 创建 / 父子链接）
- 实时反映任务事件流（claim / tool.started / heartbeat / completed
  等），不依赖手动刷新
- 支持多 board

**非目标（不在本期范围）**：
- Hermes Sessions、Runs、Analytics、Skills 等其他 dashboard 数据 ——
  本 tab 只覆盖 Kanban。如需另起 spec
- 多列拖拽换状态的 Kanban 视图 —— 使用左右分屏列表（详见 §5）
- 任务编辑（已完成任务的 `kanban edit` recovery 字段调整） ——
  低频运维操作，CLI 即可

## 3. 调研结论（驱动以下设计选择）

### 3.1 Hermes Kanban 的 HTTP 端点情况

实测 `hermes-agent==0.14.0` Dashboard 9119 的 OpenAPI 全集**不包含**
Kanban 端点：

```
$ curl http://127.0.0.1:9119/openapi.json | jq '.paths | keys[]' | grep -i kanban
（空）
```

可访问的 Dashboard 端点是 `/api/sessions`、`/api/cron/jobs`、
`/api/skills`、`/api/profiles`、`/api/analytics/*` 等；API Server 8642
则有 `/v1/runs/*` 和 `/api/jobs/*`，**两个端口都没有 Kanban**。

因此「启用 Dashboard 走 HTTP」对 Kanban 无效。

### 3.2 唯一可行的访问通路：CLI

阅读 hermes-web-ui 源码（`packages/server/src/services/hermes/hermes-kanban.ts`）
得知它**完全通过 `execFile` / `spawn` 调用 `hermes kanban` CLI**：

```ts
await execFile('hermes', ['kanban', 'boards', 'list', '--json'])
spawn('hermes', ['kanban', 'watch', '--board', slug])
await execFile('hermes', ['kanban', 'complete', taskId, '--result', ...])
```

CLI 全部 verb（实测）：
`init / boards / create / list / show / assign / reclaim / reassign
/ diagnostics / link / unlink / claim / comment / complete / edit
/ block / unblock / archive / tail / watch / stats / log / runs
/ heartbeat / assignees / context / specify / gc`

支持 `--json` 输出；`watch` / `tail` 输出 NDJSON 流。

本 spec 复用这条已被 hermes-web-ui 验证可行的通路：manager 通过
agent docker-exec 在 hermes 容器内运行 CLI。

### 3.3 hermes runtime 镜像有两份 Dockerfile

- `runtime/hermes/hermes-main/Dockerfile`：**生产用**，
  调 `install.sh` 装真实 hermes-agent；本 spec 落地后 Kanban CLI 可用。
- `runtime/hermes/hermes-main/Dockerfile.dev`：**本地 dev 用**，
  `/usr/local/bin/hermes` 是手写 shell stub，只响应
  `gateway run / gateway status`。本地容器 tag `hermes-runtime:hermes-main-dev`
  就是这份 Dockerfile build 出来的。

stub 模式下任何 `hermes kanban ...` 调用都会返回 exit 2
`unknown hermes subcommand`，所以**本 spec 在 dev stub 镜像上不可用**。
处理策略见 §10。

## 4. 用户故事

| 角色 | 主要场景 |
|---|---|
| 平台管理员 | 跨实例巡检；进入某实例的「任务」tab，看 running 列有没有卡死的、blocked 列有没有长期未处理 |
| 组织管理员 | 给 hermes 派新任务、跟进 blocked 任务并解除阻塞、归档作废任务 |
| 组织成员 | 浏览实例内当前任务进度；点 running 任务看实时执行流；给任务加评论 |

## 5. UI / UX

### 5.1 入口与权限

- 路由：`/apps/:appId/kanban`，由 `AppDetailPage.vue` 的 tab 导航
  驱动，插入位置在「概览」之后
- 可见性：与「概览」一致 —— 所有能看到实例详情的角色都能看
  （`isOrgMember` 或 `isPlatformAdmin`）
- 「运行时」tab 现行的「仅平台管理员可见」规则**不动**

### 5.2 整体布局

左右分屏：

```
+-------------------------- HEADER (实例名 + status) ---------------+
| 概览 | 任务 ← active | 渠道 | 实例知识库 | 工作目录 | 运行时 | 审计 |
+--------------------------- TOOLBAR -------------------------------+
| board: default ▾ | 优先级 ▾ | assignee ▾ | 搜索 | watch指示 |+ 新建任务 |
+-----------------------+------------------------------------------+
| LIST (400px)          | DETAIL (1fr)                              |
| ▼ ● Running (2)       |  RUNNING · 已执行 2分14秒                   |
|   - task A [selected] |  分析周二线上故障的根因                       |
|   - task B            |  task_id t_xxx · board default              |
| ▼ ◎ Ready (2)         |  ─────────────────────────────────────────  |
|   - ...               |  [✓ 完成] [⊘ 阻塞] [↺ 释放claim] [↳ 重新分配] [📦 归档] |
| ▼ ○ Todo (3)          |  ─────────────────────────────────────────  |
| ▼ ⊘ Blocked (1)       |  元信息（assignee / priority / skills ...）  |
| ▶ ✓ Done (14)         |  任务 body                                  |
| ▶ ◐ Triage (0)        |  实时执行流 ● LIVE                            |
|                       |  历次执行 (task_runs)                        |
|                       |  评论 + 输入框                               |
+-----------------------+------------------------------------------+
```

- 屏幕宽 < 1200px 时退化为单列（列表在上、详情在下）
- 列表 400px 宽度固定；详情 flex
- 选中任务通过 URL query 同步：`?board=default&task=t_xxx`，刷新/分享链接保留状态

### 5.3 左侧列表

- 按 `task.status` 分组，6 组（archived 默认隐藏，开关后追加为第 7 组）
- 默认展开：Running / Ready / Todo / Blocked
- 默认折叠：Done / Triage
- 折叠状态持久化到 localStorage（key 含 appID）
- 每行展示：标题（最多 2 行）+ assignee tag（蓝）+ priority tag
  （high 红 / medium 橙 / low 绿）+ skills tag（紫，仅当存在时）+
  相对时间
- Running 行额外显示「最新事件预览」绿色条（`tool.completed · web_extract · 2.3KB`）
- Blocked 行额外显示阻塞原因黄色条
- 新事件抵达（kanban watch 推送）时，对应行做一次轻微高亮动画

### 5.4 右侧详情（按当前 task.status 决定操作按钮）

| 当前状态 | 显示的操作按钮 |
|---|---|
| `todo` / `ready` | 评论 · 阻塞 · 归档 · 重新分配 · 链接父任务 |
| `running` | 评论 · 标记完成 · 阻塞 · 释放 claim · 归档 |
| `blocked` | 评论 · **解除阻塞** · 归档 · 重新分配 |
| `done` | 评论 · 归档 |
| `archived` | 评论（只读） |

详情区域分节滚动：
1. 元信息（assignee / priority / skills / workspace / worker / 心跳 / 创建时间 / 开始时间）
2. 任务 body
3. **实时执行流**（仅 running 时高亮显示 `● LIVE` 徽章）
4. 历次执行（`task_runs` 表，列：序号 / 状态 / worker / 时长 / 结果 或 错误）
5. 评论时间线 + 输入框（直接展开，不需要点「+评论」二次展开）

### 5.5 新建任务表单

- 用 `NModal` 模态框，提交后调 `POST /tasks`
- **字段按角色分两套**：
  - **平台管理员（platform_admin）**：title / body / assignee / priority
    / skills / workspace_kind / workspace_path / parent_id / max_retries
    （全字段）
  - **组织管理员 + 组织成员**：title / body / assignee / priority
    （必填字段集）
- 前端按角色显示对应表单；后端 handler 按 principal 角色 strip 不允许的字段
  （**前端是 UX、后端是权威**）

## 6. 数据通路

```
[Browser]
    ↓  /api/v1/apps/{appId}/hermes/kanban/...
[oc-manager Go]
    ↓  service.HermesKanban.runCLI(appID, ["kanban", verb, ..., "--json"])
    ↓  按 appID → runtime_node → agent endpoint + agent_token
    ↓  POST {agent}/v1/scopes/apps/{appID}/docker-exec
          body: { container: "hermes-<appID>", cmd: [...] }
[Runtime Agent]
    ↓  docker exec API（一次性 stdout & exit_code；或 chunked stdout 流）
[Hermes Container]
    ↓  /usr/local/bin/hermes kanban ... --json
    ↑  stdout = JSON 或 NDJSON
```

**关键约束**：
- 所有 CLI 参数走**严格白名单**：verb 枚举、status 枚举、priority 数值范围、
  assignee/board slug 正则 `^[a-z0-9][a-z0-9_-]{0,63}$`
- 任务 ID、body、result 等用户输入**作为单独 argv 元素传给 execFile**，
  绝不拼接到 shell 字符串里；agent 端的 docker-exec API 也必须按 argv
  数组接受、不走 shell

## 7. 后端分层

### 7.1 Agent HTTP 通路（与 cron-management 共建）

agent 端新增两个 endpoint（若已存在则复用）：

```
POST /v1/scopes/apps/{appID}/docker-exec
  body:    { container, cmd: []string, timeout_ms?, env? }
  resp:    { exit_code, stdout, stderr }

POST /v1/scopes/apps/{appID}/docker-exec-stream
  body:    { container, cmd: []string, timeout_ms? }
  resp:    chunked transfer，每个 chunk 是一行 stdout（NDJSON 友好）
           结尾 chunk 含 { __end: true, exit_code }
```

- 鉴权沿用 agent_token
- agent 必须按 argv 数组调 docker exec（不走 shell；不允许 `sh -c`）
- streaming endpoint 退出时 chunk 必须能携带 exit_code，便于 manager 端区分
  正常结束与异常断开

### 7.2 manager service 层

`internal/service/hermes_kanban.go`，每个 CLI verb 一个方法。参数严格白名单，
返回值是从 stdout JSON 反序列化的强类型结构：

| 方法 | CLI |
|---|---|
| `ListBoards` | `kanban boards list --json [--all]` |
| `ListTasks(filters)` | `kanban list --board <slug> [--status <s>] [--assignee <a>] --json` |
| `ShowTask(taskID)` | `kanban show <id> --board <slug> --json` |
| `TaskRuns(taskID)` | `kanban runs <id> --board <slug> --json` |
| `StreamEvents(taskID, w)` | `kanban watch --board <slug> [--task <id>]`（流） |
| `Stats` | `kanban stats --board <slug> --json` |
| `CreateTask(input)` | `kanban create "<title>" --board <slug> --assignee <a> --priority <n> [...]` |
| `Comment(taskID, body)` | `kanban comment <id> "<body>" --board <slug>` |
| `Complete(taskID, result)` | `kanban complete <id> --result "<text>" --board <slug>` |
| `Block(taskID, reason)` | `kanban block <id> "<reason>" --board <slug>` |
| `Unblock(taskID)` | `kanban unblock <id> --board <slug>` |
| `Archive(taskID)` | `kanban archive <id> --board <slug>` |
| `Reassign(taskID, profile)` | `kanban reassign <id> --to <profile> --board <slug>` |
| `Reclaim(taskID)` | `kanban reclaim <id> --board <slug>` |

每个方法内部模板：

```go
func (s *HermesKanban) Comment(ctx, appID, board, taskID, body string) error {
    args := []string{"kanban", "comment", taskID, body, "--board", board}
    out, err := s.runCLI(ctx, appID, args)
    if err != nil { return mapCLIError(err, out) }
    return nil
}
```

### 7.3 manager handler 层

`internal/api/handlers/hermes_kanban.go`，路由前缀 `/api/v1/apps/{appId}/hermes/kanban`：

```
GET    /boards
GET    /tasks?board=&status=&assignee=&priority=&q=
GET    /tasks/{taskId}?board=
GET    /tasks/{taskId}/runs?board=
GET    /tasks/{taskId}/events?board=        (SSE)
POST   /tasks
POST   /tasks/{taskId}/comment
POST   /tasks/{taskId}/complete
POST   /tasks/{taskId}/block
POST   /tasks/{taskId}/unblock
POST   /tasks/{taskId}/archive
POST   /tasks/{taskId}/reassign
POST   /tasks/{taskId}/reclaim
GET    /stats?board=
```

- 请求 DTO 放 `internal/api/handlers/dto.go`，遵循现有约定（大写导出名）
- 响应类型直接复用 service 返回的 `KanbanTask` / `KanbanTaskDetail` / 等
- `POST /tasks` 的字段按 principal 角色 strip：
  - 平台管理员：全字段通过
  - org admin / member：只保留 title / body / assignee / priority；其他字段
    被丢弃并返回 200（不报错，前端表单也不会显示这些字段）
- SSE handler 调 `service.StreamEvents`，agent 流式 chunk 转 SSE event

### 7.4 权限（`internal/auth/authorizer.go`）

新增：
- `CanViewAppKanban(principal, app) = CanViewApp(principal, app)`（所有读端点）
- `CanManageAppKanban(principal, app) = CanViewApp(principal, app)`（所有写端点）
- 新建任务时**字段级**权限由 handler 单独判断（见上）

不在 service / handler 内联写 `if principal.Role == "..."`。

## 8. 前端组件

- 路由：`AppDetailPage.vue` 的 `allTabs` 追加 `{ path: 'kanban', label: '任务' }`
  ，插入位置在「概览」之后；router 配置增加子路由
- 组件清单（全部新增，`web/src/pages/apps/`）：
  - `AppKanbanTab.vue`（顶层 wrapper，左右分屏布局；负责 board 选择 / 过滤 / 实时刷新协调）
  - `KanbanTaskList.vue`（左侧列表，按 status 分组）
  - `KanbanTaskRow.vue`（列表行）
  - `KanbanTaskDetail.vue`（右侧详情）
  - `KanbanTaskActions.vue`（按状态显示的操作按钮组）
  - `KanbanCreateModal.vue`（新建表单，按角色显示字段集）
- hooks（`web/src/api/hooks/useKanban.ts`，由 `make web-types-gen` 派生类型）：
  - `useKanbanBoards(appId)`
  - `useKanbanTasks(appId, board, filters)`（5s 轮询；watch 推送时 invalidate）
  - `useKanbanTask(appId, board, taskId)`
  - `useKanbanRuns(appId, board, taskId)`
  - `useKanbanEventsStream(appId, board, taskId)`（`EventSource`）
  - 写操作 mutation：`useCreateKanbanTask` / `useCommentKanbanTask` / `useCompleteKanbanTask`
    / `useBlockKanbanTask` / `useUnblockKanbanTask` / `useArchiveKanbanTask`
    / `useReassignKanbanTask` / `useReclaimKanbanTask`
- 写操作走二次确认：使用现有 `ConfirmActionModal.vue`；archive / reclaim
  / complete 等高风险操作必须二次确认；comment 不需要
- OpenAPI 同步：handler 加 swag 注解，提交时跑 `make openapi-gen` +
  `make web-types-gen`

## 9. 实时事件流

### 9.1 端到端通路

```
[hermes container]
  hermes kanban watch --board <slug> --json
  → stdout NDJSON: {"type":"tool.completed",...}\n{"type":"heartbeat",...}\n
[agent docker-exec-stream]
  chunked transfer，每行一个 chunk
[manager handler /events]
  转 SSE：每个 chunk wrap 成 `data: <json>\n\n`
[browser EventSource]
```

### 9.2 订阅范围

- 选择**整 board watch**（`kanban watch --board <slug>`，不带 `--task`），单
  EventSource 连接覆盖所有任务的事件
- 前端 store 接到事件后：(a) 更新对应任务行的「最新事件预览」；(b) 如选中
  任务的事件，追加到详情的实时执行流面板
- 切换 board / 离开 tab 时关闭 EventSource

### 9.3 容错

- watch 断流：前端 `onerror` 后等待 1s / 3s / 5s 三次重连；三次都失败显示
  「实时流已断开，<u>点此重连</u>」+ 切回 5s 轮询
- agent 5xx：同上

## 10. 镜像 / 运行时

### 10.1 生产 Dockerfile：无需改动

`runtime/hermes/hermes-main/Dockerfile` 已通过 `install.sh` 装真实
hermes-agent；本 spec 不需要：
- 安装 `hermes-agent[web,pty]`（dashboard 不用）
- 开 `API_SERVER_ENABLED`（API server 不用）
- 暴露任何容器外端口（CLI 完全在容器内执行）

### 10.2 dev stub 镜像（Dockerfile.dev）：检测 + 降级提示

`Dockerfile.dev` 的 stub 模式不支持 Kanban CLI。处理：

- 镜像在 `/etc/oc-image.json` 已经有 `"stub": true` 字段
- agent 心跳上报 / `app_runtime` 查询时附带 `image_info`（含 `stub`
  标志），manager 缓存到 `apps.image_info` 或类似列
- manager handler 在 stub 实例上调 Kanban 端点时直接返回 503
  `KANBAN_NOT_SUPPORTED_ON_STUB`
- 前端 tab 仍然可见，但主体显示静态提示卡片：「该实例运行的是本地 dev
  stub 镜像，Kanban 不可用；切到生产镜像后该功能自动启用」

**不**在 dev Dockerfile 里加 Kanban CLI stub —— 维护成本不值得，本地
开发若需要测 Kanban，直接 build 生产 Dockerfile。

### 10.3 pin hermes 上游版本

`runtime/hermes/hermes-main/version.txt` 当前是 hermes 上游 git ref。
需要 pin 到一个**已实测 `kanban --json` 输出格式稳定**的版本（≥ 0.14.0），
并在 `runtime/hermes/hermes-main/tests/` 加 contract 测试：构建生产镜像
后启动一个 hermes，跑 `hermes kanban init && hermes kanban list --json`、
`hermes kanban stats --json`，断言关键字段存在。

## 11. 错误处理

| 场景 | 后端响应 | 前端展示 |
|---|---|---|
| 实例容器未运行 | 503 `RUNTIME_NOT_AVAILABLE` | 「实例容器未运行，请先到运行时 tab 启动」+ 跳转链接 |
| agent 不可达 | 502 `AGENT_UNREACHABLE` | 「无法联系节点 agent」+ 5s 后自动重试一次 |
| CLI 非零退出 | 400 `KANBAN_CLI_ERROR`，body 含截断后 stderr | toast 显示 stderr 摘要 |
| stdout 非合法 JSON | 502 `KANBAN_OUTPUT_INVALID` | 「Hermes 版本可能不兼容，请联系平台管理员」 |
| watch 流断开 | SSE 关闭 | 重连 3 次后退回 5s 轮询 |
| 任务被并发改状态（race） | 409 `KANBAN_STATE_CONFLICT` | toast 「任务状态已变化，请刷新」 |
| 字段级权限不足（org admin/member 提交 strip 之外字段） | 字段被静默丢弃 | 前端表单也没有这些字段，不会触发 |

## 12. 测试

### 12.1 后端

- `internal/service/hermes_kanban_test.go`：
  - mock 一个 `dockerExecClient` 接口，table-driven 覆盖每个 verb
  - 每条用例中文注释（覆盖场景）
  - 关键断言：argv 参数白名单、JSON 解析正确性、错误码映射
  - 使用 `require.NoError` / `require.ErrorIs` / `assert.Equal`
- `internal/api/handlers/hermes_kanban_test.go`：
  - principal 角色矩阵：platform_admin / org_admin / org_member 各跑一遍
  - 关键断言：org admin/member 提交 skills/workspace_kind 等字段时被 strip
  - SSE handler：mock streaming，断言 chunk 正确翻译成 `data: <...>\n\n`
- `internal/auth/authorizer_test.go`：加 `CanViewAppKanban`
  / `CanManageAppKanban` 用例

### 12.2 前端

- `web/src/pages/apps/AppKanbanTab.spec.ts`：
  - 列表按 status 分组渲染、折叠/展开 localStorage 持久化
  - 选中任务高亮、URL `?task=` 同步
  - 操作按钮按状态显示（platform_admin / org_admin 矩阵）
  - 新建任务表单按角色显示字段集（platform_admin 看到 skills；org_admin
    看不到）
  - 写操作触发 mutation 后 invalidate query
  - SSE 断开后退回轮询

### 12.3 端到端

- 增加 `runtime/hermes/hermes-main/tests/test_kanban_json_contract.py`：
  在构建出的镜像里跑 `hermes kanban init && hermes kanban list --json`
  断言输出可解析
- 浏览器手测覆盖：
  - 创建任务（org member 角色 + platform_admin 角色）
  - 评论 / 完成 / 阻塞 / 解除阻塞 / 归档 / 重新分配 / 释放 claim
  - 实时事件流到达后列表行高亮
  - SSE 断开恢复

## 13. 前置依赖（plan 第一步必须先做）

| # | 依赖 | 验证方式 |
|---|---|---|
| 1 | agent 实现 `/docker-exec` 一次性 + `/docker-exec-stream` 流式 endpoint | curl agent 跑 `echo hi` 一次性、`tail -f /tmp/x` 流式 |
| 2 | 生产 Dockerfile build 一次确认 `hermes kanban --help` 可用 | CI 跑生产镜像 build + 容器内自检 |
| 3 | hermes 版本 pin（kanban --json 输出契约稳定） | contract test 通过 |
| 4 | agent 心跳上报 `image_info.stub` 标志，manager handler 据此返回 503 | unit test + 手测 dev 镜像访问 Kanban tab 看到降级提示 |
| 5 | OpenAPI / web-types-gen 工具链能跑通（已有，确认即可） | `make openapi-check` 干净 |

## 14. 风险

| 风险 | 缓解 |
|---|---|
| Hermes CLI 输出格式跨版本 break | (a) version pin；(b) contract test；(c) service 层解析容错（unknown field 跳过） |
| agent docker-exec 安全风险（任意命令执行） | (a) agent 端固定 cmd 数组首项必须是 `hermes`；(b) manager 端参数白名单；(c) 所有用户输入走 argv 不走 shell |
| watch 流过密导致前端卡顿 | manager 端可加节流（同任务事件 ≤ 200ms 合并一次推送） |
| 多 board 切换状态丢失 | URL query 同步 + localStorage |
| dev 镜像用户进 Kanban tab 体验差 | §10.2 降级提示卡片，明确说明"切到生产镜像可用" |

## 15. 不影响 / 不改动

- 「概览 / 渠道 / 实例知识库 / 工作目录 / 运行时 / 审计」六个现有 tab
- 平台管理员独占的「运行时」tab 仍仅平台管理员可见
- new-api / 余额查询 / cron-management 等其他模块
- `users.deleted_at` 语义 / 审计日志 schema

## 16. 后续可能演进（out of scope，预留方向）

- 父子任务依赖图可视化（`hermes kanban context`）
- 用 `hermes kanban diagnostics` 做"看板健康检查"提示
- 跨实例的"我的任务"聚合视图（platform admin 维度）
- Sessions / Analytics 视角的单独 tab
