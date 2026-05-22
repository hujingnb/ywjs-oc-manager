# Hermes 定时任务（Cron）管理调研报告

> 调研日期：2026-05-19
> 目标：调研 Hermes 定时任务的完整管理机制，为 oc-manager 集成提供技术方案
> 验证环境：本地 docker + new-api + hermes-runtime 镜像

---

## 一、功能概述

Hermes Cron 是一个**内置的定时任务调度系统**，支持：
- 4 种调度格式（cron 表达式、间隔、延迟、ISO 时间戳）
- 任务与 Python 脚本结合（脚本输出作为 agent 上下文）
- 多平台投递（Telegram、Discord、微信、本地文件等）
- 完整的 CRUD 管理（CLI + REST API）
- Gateway 内嵌调度器（60s tick，无需独立进程）

---

## 二、调度格式

| 格式 | 示例 | 行为 |
|---|---|---|
| Cron 表达式 | `0 9 * * *` | 标准 5 字段 cron（分 时 日 月 周） |
| 间隔 | `every 2h`、`every 30m` | 周期性执行 |
| 相对延迟 | `30m`、`2h`、`1d` | 一次性，从现在起延迟执行 |
| ISO 时间戳 | `2026-01-15T09:00:00` | 一次性，精确时间点 |

**注意**：不支持自然语言（如 "daily at 9am"），必须用 `0 9 * * *`。

---

## 三、任务数据结构（jobs.json）

任务存储在 `~/.hermes/cron/jobs.json`（容器内 `/opt/data/cron/jobs.json`），原子写入（write-then-rename）。

```json
{
  "id": "16c7725783aa",
  "name": "每分钟鼓励",
  "prompt": "请回复当前时间和一句鼓励的话",
  "skills": [],
  "model": null,
  "provider": null,
  "base_url": null,
  "script": null,
  "no_agent": false,
  "schedule": {
    "kind": "cron",
    "expr": "*/1 * * * *",
    "display": "*/1 * * * *"
  },
  "repeat": {
    "times": null,
    "completed": 18
  },
  "enabled": true,
  "state": "scheduled",
  "paused_at": null,
  "paused_reason": null,
  "created_at": "2026-05-19T08:16:26.844316+00:00",
  "next_run_at": "2026-05-19T08:42:58.544858+00:00",
  "last_run_at": "2026-05-19T08:37:06.442422+00:00",
  "last_status": "ok",
  "last_error": null,
  "last_delivery_error": null,
  "deliver": "local",
  "origin": null,
  "enabled_toolsets": null,
  "workdir": null
}
```

### 关键字段说明

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 12 位 hex，任务唯一标识 |
| `state` | string | `scheduled` \| `paused` \| `running` \| `completed` |
| `schedule.kind` | string | `cron` \| `interval` \| `once` |
| `repeat.times` | int\|null | null = 无限循环；数字 = 最大执行次数 |
| `repeat.completed` | int | 已执行次数 |
| `last_status` | string | `ok` \| `error` |
| `last_error` | string\|null | 最近一次错误信息 |
| `script` | string\|null | 脚本文件名（相对于 `~/.hermes/scripts/`） |
| `no_agent` | bool | true = 纯脚本模式，不调用 LLM |
| `deliver` | string | 投递目标（见下文） |

---

## 四、任务生命周期

```
scheduled → running → scheduled（循环）
scheduled → running → completed（repeat.times 耗尽）
scheduled → paused（手动暂停）
paused → scheduled（手动恢复）
```

调度器每 60 秒 tick 一次，检查 `next_run_at <= now AND state == "scheduled"` 的任务并执行。

---

## 五、投递目标（deliver 字段）

| 值 | 说明 |
|---|---|
| `local` | 保存到 `~/.hermes/cron/output/<job_id>/YYYY-MM-DD_HH-MM-SS.md` |
| `origin` | 投递到创建该任务的聊天（需 gateway 运行） |
| `telegram` | Telegram home channel |
| `telegram:<chat_id>` | 指定 Telegram 群组 |
| `telegram:<chat_id>:<thread_id>` | 指定 Telegram 话题 |
| `discord` | Discord home channel |
| `discord:#channel` | 指定 Discord 频道 |
| `slack` | Slack home channel |
| `weixin` | 微信（WeChat） |
| `email` | 邮件 |

**输出文件格式**（`local` 模式）：
```markdown
# Cron Job: 任务名称
**Job ID:** xxx
**Run Time:** 2026-05-19 08:27:18
**Schedule:** */1 * * * *

## Prompt
[系统注入的 cron 说明 + 用户 prompt]

## Response
[Agent 的回复内容]
```

**[SILENT] 机制**：Agent 回复以 `[SILENT]` 开头时，投递被抑制（不写文件、不发消息）。适合监控类任务（无变化时静默）。

---

## 六、脚本功能（script 字段）

脚本在每次 agent 执行前运行，stdout 作为上下文注入 prompt：

```python
# ~/.hermes/scripts/check_status.py
import datetime
print(f"当前时间: {datetime.datetime.now().isoformat()}")
print("STATUS=OK")
```

创建时使用相对文件名：
```bash
hermes cron create "*/5 * * * *" "分析脚本输出" --script check_status.py
```

**注意**：脚本路径必须是相对于 `~/.hermes/scripts/` 的文件名，不能用绝对路径。

**no_agent 模式**：`--no-agent` 标志让脚本直接投递 stdout，不经过 LLM，零 token 消耗。

---

## 七、CLI 命令（已验证）

```bash
# 查看所有任务（含暂停）
hermes cron list --all

# 创建任务
hermes cron create "0 9 * * *" "每日报告" --name "日报" --deliver local
hermes cron create "every 2h" "检查状态" --script check.py --deliver telegram
hermes cron create "30m" "一次性提醒" --name "提醒" --deliver local

# 立即触发（下次 tick 时执行）
hermes cron run <job_id>

# 编辑任务
hermes cron edit <job_id> --schedule "every 4h"
hermes cron edit <job_id> --prompt "新的 prompt"
hermes cron edit <job_id> --name "新名称"

# 暂停/恢复
hermes cron pause <job_id>
hermes cron resume <job_id>

# 删除
hermes cron remove <job_id>

# 查看调度器状态
hermes cron status
# → ✓ Gateway is running — cron jobs will fire automatically
#   PID: 7, 1
#   4 active job(s)
#   Next run: 2026-05-19T08:42:00+00:00

# 手动触发一次 tick（调试用）
hermes cron tick
```

---

## 八、REST API（已验证，端口 8642）

需要 `API_SERVER_ENABLED=true` 和 `API_SERVER_KEY=<key>`。

### 端点列表

| 方法 | 路径 | 功能 |
|---|---|---|
| `GET` | `/api/jobs` | 列出所有任务 |
| `POST` | `/api/jobs` | 创建任务 |
| `GET` | `/api/jobs/{id}` | 获取单个任务 |
| `PATCH` | `/api/jobs/{id}` | 更新任务（部分更新） |
| `DELETE` | `/api/jobs/{id}` | 删除任务 |
| `POST` | `/api/jobs/{id}/pause` | 暂停任务 |
| `POST` | `/api/jobs/{id}/resume` | 恢复任务 |
| `POST` | `/api/jobs/{id}/run` | 立即触发 |

### 响应格式

```json
// GET /api/jobs
{
  "jobs": [
    { "id": "...", "name": "...", "state": "scheduled", ... }
  ]
}

// POST /api/jobs → 返回 {"job": {...}}
// PATCH /api/jobs/{id} → 返回 {"job": {...}}
// POST /api/jobs/{id}/pause → 返回 {"job": {...}}
// DELETE /api/jobs/{id} → 返回 {"ok": true}
```

**注意**：`GET /api/jobs/{id}` 直接返回 job 对象（不嵌套），其他操作返回 `{"job": {...}}`。

### 创建任务的请求体

```json
{
  "prompt": "每日报告内容",
  "schedule": "0 9 * * *",
  "name": "日报",
  "deliver": "local",
  "skills": ["arxiv"],
  "model": "deepseek-v4-pro",
  "provider": "custom",
  "base_url": "http://new-api:3000/v1",
  "script": "check.py",
  "no_agent": false
}
```

---

## 九、调度器内部机制

- **运行位置**：Gateway 内嵌线程，无需独立进程
- **Tick 间隔**：默认 60 秒（`kanban.dispatch_interval_seconds` 配置）
- **锁机制**：文件锁（`fcntl.flock`），防止并发 tick 重复执行
- **会话隔离**：每次执行创建全新 agent session，无历史记忆
- **递归防护**：cron session 内禁用 `cronjob` 工具，防止任务创建新任务
- **Fallback 支持**：继承 config.yaml 的 fallback_providers 配置

---

## 十、与 oc-manager 集成方案

### 10.1 当前状态

oc-manager 目前没有 Cron 管理功能。Hermes 容器内的 cron 任务完全由 Hermes 自身管理，manager 不感知。

### 10.2 集成方案

**方案 A：通过 API Server 管理（推荐）**

前提：Hermes 容器启动时 `API_SERVER_ENABLED=true`。

```
manager → agent HTTP proxy → hermes:8642/api/jobs
```

支持完整 CRUD：列出、创建、编辑、暂停、恢复、立即触发、删除。

**方案 B：直接读写 jobs.json**

通过 agent file API 读写 `/opt/data/cron/jobs.json`。

- 读取：`GET /v1/scopes/apps/<id>/runtime/file?path=cron/jobs.json`
- 写入：`PUT /v1/scopes/apps/<id>/runtime/file?path=cron/jobs.json`

**缺点**：需要自己维护 JSON 格式，且写入后需等下次 tick 才生效（不能立即触发）。

**方案 C：通过 docker exec 调用 CLI**

通过 agent docker proxy 执行 `docker exec hermes-<appId> hermes cron <cmd>`。

**缺点**：需要 docker exec 权限，输出解析复杂。

### 10.3 推荐集成路径

1. 在 Hermes 容器启动配置中加入 `API_SERVER_ENABLED=true`（已在 `.env` 中）
2. 通过 runtime-agent 的 HTTP 代理转发请求到容器内 8642 端口
3. manager 实现 Cron CRUD API，底层调用 Hermes Jobs API
4. 读取 `cron/output/<job_id>/` 目录获取历史执行结果

### 10.4 输出文件读取

```
GET /v1/scopes/apps/<id>/runtime/file?path=cron/output/<job_id>/
→ 列出该任务的所有输出文件

GET /v1/scopes/apps/<id>/runtime/file?path=cron/output/<job_id>/2026-05-19_08-27-18.md
→ 读取具体执行结果
```

---

## 十一、参考文档

- [Cron Internals](https://hermes-agent.nousresearch.com/docs/zh-Hans/developer-guide/cron-internals)
- [Automate Anything with Cron](https://hermes-agent.nousresearch.com/docs/zh-Hans/guides/automate-with-cron)
- [Script-Only Cron Jobs](https://hermes-agent.nousresearch.com/docs/guides/cron-script-only)
- [Cron Troubleshooting](https://hermes-agent.nousresearch.com/docs/guides/cron-troubleshooting)
- [Web Dashboard - Cron section](https://hermes-agent.nousresearch.com/docs/user-guide/features/web-dashboard)
