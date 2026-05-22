# Hermes 任务看板数据获取调研报告

> 调研日期：2026-05-19
> 目标：调研如何从 Hermes 容器获取运行中的任务/会话数据，为构建任务看板提供技术方案
> 验证环境：本地 docker + new-api + hermes-runtime 镜像

---

## 一、数据源总览

Hermes 提供 **4 层数据获取方式**，从高到低：

| 层级 | 接口 | 适用场景 | 实时性 |
|---|---|---|---|
| REST API（API Server） | HTTP 端点 | 外部系统集成 | 实时 |
| REST API（Dashboard） | HTTP 端点 | Web UI | 实时 |
| CLI 命令 | `hermes sessions` / `hermes kanban` | 运维/脚本 | 按需 |
| SQLite 数据库直读 | `state.db` / `kanban.db` | 底层集成 | 实时 |

---

## 二、API Server（端口 8642）

### 2.1 启用方式

在 `.env` 中设置：
```
API_SERVER_ENABLED=true
API_SERVER_KEY=your-secret-key
```

Gateway 启动时自动开启 API server，监听 `127.0.0.1:8642`。

### 2.2 关键端点（已验证可用）

#### 健康检查与状态

```
GET /health
→ {"status": "ok", "platform": "hermes-agent"}

GET /health/detailed
→ {
    "status": "ok",
    "gateway_state": "running",
    "active_agents": 0,        ← 当前正在执行的 agent 数量
    "platforms": {
      "api_server": {"state": "connected", ...}
    },
    "pid": 7
  }
```

#### Runs API（任务执行与监控）

```
POST /v1/runs
Body: {"input": "...", "session_id": "..."}
→ {"run_id": "run_xxx", "status": "started"}

GET /v1/runs/{run_id}
→ {
    "object": "hermes.run",
    "run_id": "run_xxx",
    "status": "running",          ← started | running | completed | failed | cancelled
    "session_id": "test-session-1",
    "model": "hermes-agent",
    "last_event": "tool.completed", ← 最近的事件类型
    "output": "...",               ← 完成后的输出
    "usage": {"input_tokens": ..., "output_tokens": ..., "total_tokens": ...}
  }

GET /v1/runs/{run_id}/events     ← SSE 实时事件流
POST /v1/runs/{run_id}/stop      ← 中断运行中的任务
```

#### Capabilities 发现

```
GET /v1/capabilities
→ 返回所有支持的功能和端点列表
```

### 2.3 验证结果

- Run 创建后立即返回 `run_id`
- 轮询 `GET /v1/runs/{run_id}` 可实时获取状态变化
- `last_event` 字段反映最近的工具调用事件
- 完成后 `output` 字段包含最终回复
- `usage` 包含 token 消耗统计

---

## 三、Dashboard API（端口 9119）

Dashboard 需要 `fastapi` + `uvicorn`（当前 runtime 镜像未安装），但其 REST API 设计值得参考：

| 端点 | 功能 |
|---|---|
| `GET /api/status` | 版本、网关状态、活跃 session 数 |
| `GET /api/sessions` | 最近 20 个 session（含 metadata） |
| `GET /api/sessions/{id}` | 单个 session 详情 |
| `GET /api/sessions/{id}/messages` | 完整消息历史 |
| `GET /api/sessions/search?q=` | 全文搜索 |
| `GET /api/analytics/usage?days=30` | 用量分析 |
| `GET /api/cron/jobs` | 定时任务列表 |
| `GET /api/logs` | 日志查看 |
| `GET /api/skills` | 技能列表 |

**注意**：Dashboard API 需要额外安装 `pip install 'hermes-agent[web]'`，当前 oc-manager 的 runtime 镜像未包含。

---

## 四、Kanban 系统（已验证）

### 4.1 概述

Hermes Kanban 是一个**持久化任务板**，基于 SQLite（`kanban.db`），支持：
- 多 agent 协作（不同 profile 处理不同任务）
- 任务依赖关系（parent → child）
- 任务状态机：`triage → todo → ready → running → blocked → done → archived`
- Worker 心跳和自动回收
- 事件流（实时 tail）

### 4.2 数据库结构（kanban.db）

```sql
-- 核心任务表
tasks:
  id TEXT                    -- 任务 ID (t_xxxxxxxx)
  title TEXT                 -- 标题
  body TEXT                  -- 详细描述
  assignee TEXT              -- 分配给哪个 profile
  status TEXT                -- triage|todo|ready|running|blocked|done|archived
  priority INTEGER           -- 优先级
  created_by TEXT            -- 创建者
  created_at INTEGER         -- 创建时间戳
  started_at INTEGER         -- 开始时间
  completed_at INTEGER       -- 完成时间
  workspace_kind TEXT        -- scratch|dir:<path>|worktree
  workspace_path TEXT        -- 工作目录路径
  claim_lock TEXT            -- 原子锁
  claim_expires INTEGER      -- 锁过期时间
  tenant TEXT                -- 租户命名空间
  result TEXT                -- 完成结果
  worker_pid INTEGER         -- Worker 进程 PID
  last_heartbeat_at INTEGER  -- 最后心跳时间
  current_run_id INTEGER     -- 当前 run ID
  skills TEXT                -- 所需技能
  max_retries INTEGER        -- 最大重试次数

-- 任务依赖
task_links:
  parent_id TEXT
  child_id TEXT

-- 评论/协作
task_comments:
  id INTEGER
  task_id TEXT
  author TEXT
  body TEXT
  created_at INTEGER

-- 事件流
task_events:
  id INTEGER
  task_id TEXT
  run_id INTEGER
  kind TEXT                  -- created|claimed|heartbeat|completed|blocked|...
  payload TEXT               -- JSON
  created_at INTEGER

-- 执行历史
task_runs:
  id INTEGER
  task_id TEXT
  profile TEXT
  status TEXT
  worker_pid INTEGER
  started_at INTEGER
  ended_at INTEGER
  outcome TEXT
  summary TEXT
  error TEXT
```

### 4.3 CLI 命令

```bash
hermes kanban list              # 列出任务
hermes kanban show <id>         # 任务详情
hermes kanban stats             # 统计信息
hermes kanban watch             # 实时事件流
hermes kanban tail <id>         # 跟踪单个任务事件
hermes kanban runs <id>         # 执行历史
hermes kanban context <id>      # Worker 看到的完整上下文
hermes kanban create "title" --assignee <profile>
hermes kanban complete <id> --result "..."
hermes kanban block <id> "reason"
hermes kanban unblock <id>
```

### 4.4 Agent 工具调用（容器内 model 使用）

Worker 通过 tool-call 驱动看板：
- `kanban_show()` — 读取当前任务
- `kanban_list()` — 列出任务
- `kanban_complete(summary=..., result=...)` — 完成任务
- `kanban_block(reason=...)` — 阻塞任务
- `kanban_heartbeat(note=...)` — 心跳
- `kanban_comment(task_id, body)` — 添加评论
- `kanban_create(title, assignee, ...)` — 创建子任务

---

## 五、Sessions 数据（state.db）

### 5.1 数据库结构

```sql
sessions:
  id TEXT                    -- Session ID
  source TEXT                -- cli|api_server|telegram|discord|weixin|cron|...
  user_id TEXT               -- 用户标识
  model TEXT                 -- 使用的模型
  title TEXT                 -- 会话标题
  message_count INTEGER      -- 消息数
  tool_call_count INTEGER    -- 工具调用数
  input_tokens INTEGER       -- 输入 token
  output_tokens INTEGER      -- 输出 token
  started_at REAL            -- 开始时间
  ended_at REAL              -- 结束时间（NULL = 进行中）
  estimated_cost_usd REAL    -- 预估成本

messages:
  id INTEGER
  session_id TEXT
  role TEXT                  -- user|assistant|system|tool
  content TEXT               -- 消息内容
  tool_calls TEXT            -- JSON: [{function: {name, arguments}}]
  tool_name TEXT             -- 工具名称（role=tool 时）
  timestamp REAL
  token_count INTEGER
```

### 5.2 判断 session 是否活跃

- `ended_at IS NULL` → session 仍在进行中
- 结合 `gateway_state.json` 的 `active_agents` 字段判断是否有正在执行的 agent

---

## 六、其他数据文件

| 文件 | 内容 | 用途 |
|---|---|---|
| `gateway_state.json` | 网关运行状态、活跃 agent 数、平台连接状态 | 实时状态监控 |
| `sessions/session_<id>.json` | 完整会话 JSONL（含 system_prompt、所有消息） | 会话回放 |
| `logs/gateway.log` | 网关日志 | 调试 |
| `logs/agent.log` | Agent 日志 | 调试 |

---

## 七、oc-manager 集成方案建议

### 方案 A：通过 API Server 获取（推荐）

**优点**：标准 HTTP 接口，无需直接访问文件系统
**实现**：
1. 确保 Hermes 容器启动时 `API_SERVER_ENABLED=true`
2. 通过 runtime-agent 代理转发 HTTP 请求到容器内 8642 端口
3. 使用 Runs API 监控任务执行状态
4. 使用 `/health/detailed` 获取实时活跃状态

```
manager → agent → docker exec curl http://127.0.0.1:8642/v1/runs/{id}
```

或者通过 docker network 直接访问容器 IP:8642。

### 方案 B：直读 SQLite 数据库

**优点**：无需 API server，数据最全
**实现**：
1. 通过 agent file API 读取 `state.db` 和 `kanban.db`
2. 在 manager 端解析 SQLite
3. 定期轮询或通过 inotify 监听变化

```
manager → agent GET /v1/scopes/apps/<id>/runtime/file?path=state.db
```

**缺点**：SQLite WAL 模式下并发读可能有问题；文件较大时传输开销大。

### 方案 C：混合方案

- **实时状态**：读 `gateway_state.json`（小文件，JSON）
- **会话列表**：通过 API Server `/health/detailed` + 直读 `state.db`
- **任务看板**：直读 `kanban.db`（结构清晰，数据量小）
- **运行中任务**：通过 Runs API 轮询

---

## 八、关键发现总结

1. **API Server 已内置**：只需 `.env` 设置 `API_SERVER_ENABLED=true` 即可开启，无需额外依赖
2. **Runs API 是任务监控的核心**：支持创建、轮询状态、SSE 事件流、中断
3. **Kanban 是持久化任务板**：独立于 session，支持多 agent 协作，有完整的事件流
4. **Sessions 是会话级数据**：每个对话一个 session，包含消息历史和 token 统计
5. **Dashboard API 需要额外依赖**：`fastapi`/`uvicorn` 未在当前 runtime 镜像中安装
6. **数据全部在 `/opt/data/` 挂载点**：manager 通过 agent 可完全访问

---

## 九、参考文档

- [Web Dashboard](https://hermes-agent.nousresearch.com/docs/user-guide/features/web-dashboard)
- [Kanban (Multi-Agent Board)](https://hermes-agent.nousresearch.com/docs/zh-Hans/user-guide/features/kanban)
- [API Server](https://hermes-agent.nousresearch.com/docs/user-guide/features/api-server)
- [Sessions](https://hermes-agent.nousresearch.com/docs/user-guide/sessions/)
- [Kanban Tutorial](https://hermes-agent.nousresearch.com/docs/user-guide/features/kanban-tutorial)
