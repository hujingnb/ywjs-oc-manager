# oc-kanban 版本适配层 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 hermes 镜像里增加 `oc-kanban` 命令作为稳定适配层，让 manager 与 hermes 版本解耦。

**Architecture:** `oc-kanban` 是镜像内的 Python 脚本，subprocess 调用 `hermes kanban` 并把输出规整成一套版本无关的契约（统一信封 + 15 个 verb + 错误码 + capabilities 自描述）。契约规范以 JSON Schema 工件形式单一存放、构建期注入镜像，由镜像内契约测试机械守护。manager 改为只调 `oc-kanban`，Go/TS 类型按新契约重写。

**Tech Stack:** Python 3.13（镜像内，标准库 + `jsonschema`）、Go 1.x（gin、pgx）、Vue 3 + TypeScript（TanStack Query、Naive UI、Vitest）、Docker、Make。

**设计依据:** `docs/superpowers/specs/2026-05-20-oc-kanban-version-adapter-design.md`（已确认）。本计划中凡引用「spec §N」均指该文档。

**执行模型说明:** `oc-kanban` 依赖镜像内真实 hermes 环境，其契约测试 `test_kanban_contract.py` 设计为「镜像内集成测试」（本地无 hermes 时自动 skip）。因此 Task 2–6 先完成脚本与测试代码，Task 8 在构建出的镜像内统一跑契约测试做红绿验证。`capabilities` verb 不依赖 hermes，可本地直接验证。

---

## 文件结构

### 新建文件

| 文件 | 职责 |
|---|---|
| `runtime/hermes/kanban-contract/SPEC.md` | 契约规范文档：verb、信封、错误码、版本号规则。 |
| `runtime/hermes/kanban-contract/schema/envelope.schema.json` | 成功/失败信封的 JSON Schema。 |
| `runtime/hermes/kanban-contract/schema/capabilities.schema.json` | `capabilities` data 的 schema。 |
| `runtime/hermes/kanban-contract/schema/board.schema.json` | `Board` 的 schema。 |
| `runtime/hermes/kanban-contract/schema/task.schema.json` | `Task` 的 schema。 |
| `runtime/hermes/kanban-contract/schema/task-detail.schema.json` | `TaskDetail` 的 schema（内联 comment/event）。 |
| `runtime/hermes/kanban-contract/schema/stats.schema.json` | `Stats` 的 schema。 |
| `runtime/hermes/kanban-contract/schema/run.schema.json` | `Run` 的 schema。 |
| `runtime/hermes/kanban-contract/schema/event.schema.json` | `Event` 的 schema。 |
| `runtime/hermes/hermes-main/oc-kanban.py` | hermes-main 变体的 `oc-kanban` 适配实现。 |

### 修改文件

| 文件 | 改动 |
|---|---|
| `Makefile` | `build-hermes-runtime` 在 `docker build` 前注入契约工件到变体目录。 |
| `.gitignore` | 忽略变体目录内构建期注入的 `kanban-contract/` 副本。 |
| `runtime/hermes/hermes-main/Dockerfile` | COPY `oc-kanban` + 契约工件；加 `jsonschema` 依赖。 |
| `runtime/hermes/hermes-main/Dockerfile.dev` | stub 镜像也装 `oc-kanban`。 |
| `runtime/hermes/hermes-main/CONTRACT.md` | 「镜像对外命令」一节补 `oc-kanban`。 |
| `runtime/hermes/hermes-main/tests/test_kanban_contract.py` | 升级为「调 `oc-kanban` + JSON Schema 校验」的契约测试。 |
| `internal/service/hermes_kanban.go` | `runCLI` 改为 `runOCKanban`：调 `oc-kanban`、解析信封、错误码映射；各 verb 方法改 flag 风格；写方法返回 `KanbanTaskDetail`；新增 `Capabilities`；`StreamEvents` 改 `oc-kanban watch`。复用现有 `ErrKanban*` 哨兵，无需改 `errors.go`。 |
| `internal/service/hermes_kanban_types.go` | 新增 `KanbanCapabilities` / `KanbanFeatures` 类型。 |
| `internal/service/hermes_kanban_test.go` | fake execer 输出改信封格式；补充错误码映射、capabilities、写返回 detail 的用例。 |
| `internal/api/handlers/hermes_kanban.go` | 写端点返回 `{task: detail}`；新增 `Capabilities` 端点；在 `RegisterHermesKanbanRoutes` 内注册 `capabilities` 路由（`router.go` 无需改）。 |
| `openapi/openapi.yaml` | `make openapi-gen` 生成产物。 |
| `web/src/api/generated.ts` | `make web-types-gen` 生成产物。 |
| `web/src/api/hooks/useKanban.ts` | 新增 `useKanbanCapabilitiesQuery`、`KanbanCapabilities` 类型；写 mutation 消费返回的 `TaskDetail`。 |
| `web/src/pages/apps/AppKanbanTab.vue` | 按 capabilities 降级（隐藏不支持的操作）。 |
| `web/src/pages/apps/kanban/KanbanTaskActions.vue` | 按 capabilities 隐藏单任务操作按钮。 |
| `web/src/pages/apps/AppKanbanTab.spec.ts` | 补 `useKanbanCapabilitiesQuery` mock 与降级用例。 |

---

## 阶段 0 · 契约工件

### Task 1: 契约规范与 JSON Schema

**Files:**
- Create: `runtime/hermes/kanban-contract/SPEC.md`
- Create: `runtime/hermes/kanban-contract/schema/envelope.schema.json`
- Create: `runtime/hermes/kanban-contract/schema/capabilities.schema.json`
- Create: `runtime/hermes/kanban-contract/schema/board.schema.json`
- Create: `runtime/hermes/kanban-contract/schema/task.schema.json`
- Create: `runtime/hermes/kanban-contract/schema/task-detail.schema.json`
- Create: `runtime/hermes/kanban-contract/schema/stats.schema.json`
- Create: `runtime/hermes/kanban-contract/schema/run.schema.json`
- Create: `runtime/hermes/kanban-contract/schema/event.schema.json`

- [ ] **Step 1: 写契约规范文档 `SPEC.md`**

内容是 spec 第 4 节的提炼，作为镜像内开发者参考。完整写出（不是占位）：标题「oc-kanban 契约规范 v1.0」；小节依次为 —— 命令形态与信封（含 `{ok,data}` / `{ok,error}` 两个示例 JSON、退出码 0/1/2 含义）、verb 全集表（15 行，复制 spec §4.2 表格）、错误码枚举表（复制 spec §4.5 五行）、`watch` NDJSON 流约定（复制 spec §4.6）、版本号规则（MAJOR/MINOR 含义，复制 spec §4.4 版本规则）。文末注明：「各结构的精确字段以同目录 `schema/*.json` 为准；schema 是契约的机器可校验形式。」

- [ ] **Step 2: 写 `envelope.schema.json`**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "oc-kanban envelope",
  "description": "oc-kanban 所有非 watch verb 的 stdout 单行 JSON 信封。",
  "oneOf": [
    {
      "type": "object",
      "required": ["ok", "data"],
      "properties": {
        "ok": { "const": true },
        "data": {}
      },
      "additionalProperties": false
    },
    {
      "type": "object",
      "required": ["ok", "error"],
      "properties": {
        "ok": { "const": false },
        "error": {
          "type": "object",
          "required": ["code", "message"],
          "properties": {
            "code": {
              "enum": ["BAD_REQUEST", "NOT_FOUND", "UNSUPPORTED",
                       "HERMES_CLI_FAILED", "INTERNAL"]
            },
            "message": { "type": "string" }
          },
          "additionalProperties": false
        }
      },
      "additionalProperties": false
    }
  ]
}
```

- [ ] **Step 3: 写 `task.schema.json`**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "oc-kanban Task",
  "type": "object",
  "required": ["id", "title", "assignee", "status", "priority", "created_at"],
  "properties": {
    "id": { "type": "string" },
    "title": { "type": "string" },
    "body": { "type": ["string", "null"] },
    "assignee": { "type": "string" },
    "status": {
      "enum": ["triage", "todo", "ready", "running", "blocked", "done", "archived"]
    },
    "priority": { "type": "integer", "minimum": 0, "maximum": 9 },
    "tenant": { "type": ["string", "null"] },
    "workspace_kind": { "type": ["string", "null"] },
    "workspace_path": { "type": ["string", "null"] },
    "created_by": { "type": ["string", "null"] },
    "created_at": { "type": "integer" },
    "started_at": { "type": ["integer", "null"] },
    "completed_at": { "type": ["integer", "null"] },
    "result": { "type": ["string", "null"] },
    "skills": { "type": "array", "items": { "type": "string" } },
    "max_retries": { "type": ["integer", "null"] }
  },
  "additionalProperties": false
}
```

- [ ] **Step 4: 写 `board.schema.json`**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "oc-kanban Board",
  "type": "object",
  "required": ["slug", "name"],
  "properties": {
    "slug": { "type": "string" },
    "name": { "type": "string" },
    "description": { "type": ["string", "null"] },
    "icon": { "type": ["string", "null"] },
    "color": { "type": ["string", "null"] },
    "archived": { "type": "boolean" },
    "is_current": { "type": "boolean" },
    "counts": {
      "type": "object",
      "additionalProperties": { "type": "integer" }
    },
    "total": { "type": "integer" }
  },
  "additionalProperties": false
}
```

- [ ] **Step 5: 写 `event.schema.json`**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "oc-kanban Event",
  "type": "object",
  "required": ["kind", "created_at"],
  "properties": {
    "kind": { "type": "string" },
    "payload": {},
    "created_at": { "type": "integer" },
    "run_id": {}
  },
  "additionalProperties": false
}
```

- [ ] **Step 6: 写 `stats.schema.json`**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "oc-kanban Stats",
  "type": "object",
  "required": ["by_status"],
  "properties": {
    "by_status": {
      "type": "object",
      "additionalProperties": { "type": "integer" }
    },
    "by_assignee": {
      "type": "object",
      "additionalProperties": {
        "type": "object",
        "additionalProperties": { "type": "integer" }
      }
    },
    "oldest_ready_age_seconds": { "type": "integer" },
    "now": { "type": "integer" }
  },
  "additionalProperties": false
}
```

- [ ] **Step 7: 写 `run.schema.json`**

注：`Run` 字段在 Task 5 实测 hermes 后可能微调；此处给初始 schema，实测发现差异时回到本文件改。

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "oc-kanban Run",
  "type": "object",
  "required": ["profile", "status", "started_at"],
  "properties": {
    "profile": { "type": "string" },
    "status": { "type": "string" },
    "worker_pid": { "type": ["integer", "null"] },
    "started_at": { "type": "integer" },
    "ended_at": { "type": ["integer", "null"] },
    "outcome": { "type": ["string", "null"] },
    "summary": { "type": ["string", "null"] },
    "error": { "type": ["string", "null"] }
  },
  "additionalProperties": false
}
```

- [ ] **Step 8: 写 `task-detail.schema.json`**

内联 task / comment / event 定义（自包含，不跨文件 `$ref`）：

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "oc-kanban TaskDetail",
  "type": "object",
  "required": ["task"],
  "properties": {
    "task": { "$ref": "#/$defs/task" },
    "latest_summary": { "type": ["string", "null"] },
    "parents": { "type": "array", "items": { "type": "string" } },
    "children": { "type": "array", "items": { "type": "string" } },
    "comments": { "type": "array", "items": { "$ref": "#/$defs/comment" } },
    "events": { "type": "array", "items": { "$ref": "#/$defs/event" } }
  },
  "additionalProperties": false,
  "$defs": {
    "task": {
      "type": "object",
      "required": ["id", "title", "assignee", "status", "priority", "created_at"],
      "properties": {
        "id": { "type": "string" },
        "title": { "type": "string" },
        "body": { "type": ["string", "null"] },
        "assignee": { "type": "string" },
        "status": {
          "enum": ["triage", "todo", "ready", "running", "blocked", "done", "archived"]
        },
        "priority": { "type": "integer", "minimum": 0, "maximum": 9 },
        "tenant": { "type": ["string", "null"] },
        "workspace_kind": { "type": ["string", "null"] },
        "workspace_path": { "type": ["string", "null"] },
        "created_by": { "type": ["string", "null"] },
        "created_at": { "type": "integer" },
        "started_at": { "type": ["integer", "null"] },
        "completed_at": { "type": ["integer", "null"] },
        "result": { "type": ["string", "null"] },
        "skills": { "type": "array", "items": { "type": "string" } },
        "max_retries": { "type": ["integer", "null"] }
      },
      "additionalProperties": false
    },
    "comment": {
      "type": "object",
      "required": ["author", "body", "created_at"],
      "properties": {
        "author": { "type": "string" },
        "body": { "type": "string" },
        "created_at": { "type": "integer" }
      },
      "additionalProperties": false
    },
    "event": {
      "type": "object",
      "required": ["kind", "created_at"],
      "properties": {
        "kind": { "type": "string" },
        "payload": {},
        "created_at": { "type": "integer" },
        "run_id": {}
      },
      "additionalProperties": false
    }
  }
}
```

- [ ] **Step 9: 写 `capabilities.schema.json`**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "oc-kanban Capabilities",
  "type": "object",
  "required": ["contract_version", "oc_kanban_version", "variant", "verbs", "features"],
  "properties": {
    "contract_version": { "type": "string", "pattern": "^[0-9]+\\.[0-9]+$" },
    "oc_kanban_version": { "type": "string" },
    "hermes_version": { "type": ["string", "null"] },
    "variant": { "type": "string" },
    "verbs": { "type": "array", "items": { "type": "string" } },
    "features": {
      "type": "object",
      "properties": {
        "write": { "type": "boolean" },
        "watch": { "type": "boolean" },
        "runs": { "type": "boolean" },
        "stats": { "type": "boolean" }
      },
      "required": ["write", "watch", "runs", "stats"],
      "additionalProperties": false
    }
  },
  "additionalProperties": false
}
```

- [ ] **Step 10: 校验所有 schema 是合法 JSON**

Run: `python3 -c "import json,glob; [json.load(open(f)) for f in glob.glob('runtime/hermes/kanban-contract/schema/*.json')]; print('all schema valid json')"`
Expected: 输出 `all schema valid json`，无异常。

- [ ] **Step 11: Commit**

```bash
git add runtime/hermes/kanban-contract/
git commit -m "feat(hermes-runtime): 增加 oc-kanban 契约规范与 JSON Schema 工件

定义 oc-kanban 版本无关契约的 single source of truth：SPEC.md 规范文档
+ schema/ 下 8 个 JSON Schema（envelope/capabilities/board/task/
task-detail/stats/run/event），供镜像内契约测试机械校验。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## 阶段 1 · oc-kanban 适配实现

### Task 2: hermes-main 变体的 oc-kanban.py

**Files:**
- Create: `runtime/hermes/hermes-main/oc-kanban.py`

`oc-kanban` 是 hermes kanban CLI 的稳定适配层：subprocess 调 `hermes kanban`、把输出规整成契约结构、统一信封输出。这是一个连贯单元（所有 verb 都依赖镜像内真实 hermes），整体实现后由 Task 5 在镜像内做契约测试验证。

- [ ] **Step 1: 写完整 `oc-kanban.py`**

```python
#!/usr/bin/env python3
"""oc-kanban —— hermes kanban CLI 的稳定适配层。

对 manager 暴露版本无关契约（见 /usr/local/lib/oc-kanban/contract/SPEC.md），
对内 subprocess 调用本镜像的 hermes kanban 命令，把输出规整成契约结构。

输出协议：
- 非 watch verb：stdout 单行 JSON 信封 {"ok":true,"data":...} 或
  {"ok":false,"error":{"code","message"}}。
- watch verb：stdout NDJSON 事件流，每行一个 Event。
退出码：0=成功；1=业务错误（错误信封已在 stdout）；2=用法错误（argparse）。
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path

# 契约版本号（MAJOR.MINOR，规则见 SPEC.md）与 oc-kanban 实现版本。
CONTRACT_VERSION = "1.0"
OC_KANBAN_VERSION = "1"
# 单条 hermes kanban 命令的超时秒数（kanban 操作均为本地 SQLite 读写，30s 足够）。
HERMES_TIMEOUT = 30

# 功能 verb 全集（不含 capabilities 自身——它是恒定存在的能力发现入口）。
FUNCTIONAL_VERBS = [
    "boards", "list", "show", "runs", "stats", "watch",
    "create", "comment", "complete", "block", "unblock",
    "archive", "reassign", "reclaim",
]

# 契约 Task 对象的字段白名单：规整时只挑这些字段，丢弃 hermes 多余字段。
TASK_FIELDS = [
    "id", "title", "body", "assignee", "status", "priority", "tenant",
    "workspace_kind", "workspace_path", "created_by", "created_at",
    "started_at", "completed_at", "result", "skills", "max_retries",
]
# 契约 Board / Run 对象字段白名单。
BOARD_FIELDS = ["slug", "name", "description", "icon", "color",
                "archived", "is_current", "counts", "total"]
RUN_FIELDS = ["profile", "status", "worker_pid", "started_at",
              "ended_at", "outcome", "summary", "error"]


class KanbanError(Exception):
    """承载契约错误码的内部异常，main 捕获后转成失败信封。"""

    def __init__(self, code: str, message: str):
        super().__init__(message)
        self.code = code
        self.message = message


# ———————————————————————————————————————————————
# 信封输出
# ———————————————————————————————————————————————

def emit_ok(data) -> int:
    """输出成功信封并返回退出码 0。"""
    sys.stdout.write(json.dumps({"ok": True, "data": data}, ensure_ascii=False) + "\n")
    sys.stdout.flush()
    return 0


def emit_err(code: str, message: str) -> int:
    """输出失败信封并返回退出码 1。"""
    sys.stdout.write(json.dumps(
        {"ok": False, "error": {"code": code, "message": str(message)}},
        ensure_ascii=False) + "\n")
    sys.stdout.flush()
    return 1


# ———————————————————————————————————————————————
# hermes subprocess 调用
# ———————————————————————————————————————————————

def classify_hermes_error(stderr: str) -> str:
    """按 hermes stderr 文本把失败归类成契约错误码。

    文本匹配跨版本脆弱，但这正是 oc-kanban 该承担、关在适配层内的脏活；
    每个 hermes 版本的 oc-kanban 用自己版本的模式（见 spec §5.4）。
    """
    low = (stderr or "").lower()
    if "not found" in low or "no such" in low or "unknown board" in low:
        return "NOT_FOUND"
    return "HERMES_CLI_FAILED"


def run_hermes(args: list[str], timeout: int = HERMES_TIMEOUT) -> subprocess.CompletedProcess:
    """执行 `hermes kanban <args>`，返回 CompletedProcess。"""
    try:
        return subprocess.run(["hermes", "kanban", *args],
                              capture_output=True, text=True, timeout=timeout)
    except FileNotFoundError:
        raise KanbanError("UNSUPPORTED", "镜像内未安装 hermes")
    except subprocess.TimeoutExpired:
        raise KanbanError("HERMES_CLI_FAILED", "hermes kanban 命令超时")


def hermes_json(args: list[str]):
    """执行 hermes 读命令并解析 --json 输出，失败抛 KanbanError。"""
    proc = run_hermes(args)
    if proc.returncode != 0:
        raise KanbanError(classify_hermes_error(proc.stderr),
                          (proc.stderr or "hermes kanban 执行失败").strip()[:1024])
    try:
        return json.loads(proc.stdout)
    except json.JSONDecodeError as e:
        raise KanbanError("INTERNAL", f"hermes 输出非合法 JSON: {e}")


def hermes_ok(args: list[str]) -> None:
    """执行 hermes 写命令，仅校验成功，不解析输出。"""
    proc = run_hermes(args)
    if proc.returncode != 0:
        raise KanbanError(classify_hermes_error(proc.stderr),
                          (proc.stderr or "hermes kanban 执行失败").strip()[:1024])


def has_real_hermes() -> bool:
    """探测镜像是否带真实 hermes kanban CLI（stub 镜像的 hermes 是 shell 脚本）。"""
    try:
        proc = subprocess.run(["hermes", "kanban", "--help"],
                              capture_output=True, text=True, timeout=10)
        return proc.returncode == 0
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return False


def read_image_info() -> dict:
    """读取构建期写入的 /etc/oc-image.json（与 oc-info 同源）。

    字段名以 Dockerfile 第 61-66 行写入 /etc/oc-image.json 的实际 key 为准；
    实现时先 `cat runtime/hermes/hermes-main/Dockerfile` 确认 key，再在下方
    capabilities 的 .get() 候选里对齐。
    """
    try:
        return json.loads(Path(
            os.environ.get("OC_INFO_FILE", "/etc/oc-image.json")).read_text())
    except (OSError, json.JSONDecodeError):
        return {}


# ———————————————————————————————————————————————
# 输出规整：把 hermes --json 输出重映射成契约结构
# ———————————————————————————————————————————————

def normalize_task(raw: dict) -> dict:
    """把 hermes 任务对象规整成契约 Task：挑字段 + 必填项缺省兜底。"""
    t = {k: raw.get(k) for k in TASK_FIELDS}
    t["skills"] = raw.get("skills") or []
    t["priority"] = raw.get("priority") if isinstance(raw.get("priority"), int) else 0
    t["created_at"] = raw.get("created_at") if isinstance(raw.get("created_at"), int) else 0
    return t


def normalize_board(raw: dict) -> dict:
    """把 hermes board 对象规整成契约 Board。"""
    b = {k: raw.get(k) for k in BOARD_FIELDS}
    b["archived"] = bool(raw.get("archived"))
    b["is_current"] = bool(raw.get("is_current"))
    b["counts"] = raw.get("counts") or {}
    b["total"] = raw.get("total") if isinstance(raw.get("total"), int) else 0
    return b


def normalize_comment(raw: dict) -> dict:
    """把 hermes 评论对象规整成契约 Comment。"""
    return {
        "author": raw.get("author") or "",
        "body": raw.get("body") or "",
        "created_at": raw.get("created_at") if isinstance(raw.get("created_at"), int) else 0,
    }


def normalize_event(raw: dict) -> dict:
    """把 hermes 事件对象规整成契约 Event（watch 流与 detail.events 共用）。"""
    return {
        "kind": raw.get("kind") or "",
        "payload": raw.get("payload"),
        "created_at": raw.get("created_at") if isinstance(raw.get("created_at"), int) else 0,
        "run_id": raw.get("run_id"),
    }


def normalize_run(raw: dict) -> dict:
    """把 hermes run 对象规整成契约 Run。"""
    r = {k: raw.get(k) for k in RUN_FIELDS}
    r["profile"] = raw.get("profile") or ""
    r["status"] = raw.get("status") or ""
    r["started_at"] = raw.get("started_at") if isinstance(raw.get("started_at"), int) else 0
    return r


def normalize_stats(raw: dict) -> dict:
    """把 hermes stats 对象规整成契约 Stats。"""
    return {
        "by_status": raw.get("by_status") or {},
        "by_assignee": raw.get("by_assignee") or {},
        "oldest_ready_age_seconds": raw.get("oldest_ready_age_seconds")
        if isinstance(raw.get("oldest_ready_age_seconds"), int) else 0,
        "now": raw.get("now") if isinstance(raw.get("now"), int) else 0,
    }


def normalize_detail(raw: dict) -> dict:
    """把 hermes show 输出规整成契约 TaskDetail。"""
    return {
        "task": normalize_task(raw.get("task") or {}),
        "latest_summary": raw.get("latest_summary"),
        "parents": raw.get("parents") or [],
        "children": raw.get("children") or [],
        "comments": [normalize_comment(c) for c in (raw.get("comments") or [])],
        "events": [normalize_event(e) for e in (raw.get("events") or [])],
    }


def _show_detail(board: str, task_id: str) -> dict:
    """调 hermes show 并规整成 TaskDetail，供 create / 写 verb 复用。"""
    return normalize_detail(hermes_json(["--board", board, "show", task_id, "--json"]))


# ———————————————————————————————————————————————
# verb 实现
# ———————————————————————————————————————————————

def verb_capabilities(args) -> int:
    """自描述能力：契约版本、支持的 verb、feature 开关。不调用 hermes。"""
    info = read_image_info()
    real = has_real_hermes()
    return emit_ok({
        "contract_version": CONTRACT_VERSION,
        "oc_kanban_version": OC_KANBAN_VERSION,
        "hermes_version": info.get("hermes_ref") or info.get("hermes_version"),
        "variant": info.get("variant") or info.get("oc_image_variant") or "hermes-main",
        "verbs": FUNCTIONAL_VERBS if real else [],
        "features": {"write": real, "watch": real, "runs": real, "stats": real},
    })


def verb_boards(args) -> int:
    """列出所有 board。"""
    raw = hermes_json(["boards", "list", "--all", "--json"])
    return emit_ok([normalize_board(b) for b in (raw or [])])


def verb_list(args) -> int:
    """列出某 board 的任务（可按 status / assignee 过滤）。"""
    cmd = ["--board", args.board, "list", "--json"]
    if args.status:
        cmd += ["--status", args.status]
    if args.assignee:
        cmd += ["--assignee", args.assignee]
    raw = hermes_json(cmd)
    return emit_ok([normalize_task(t) for t in (raw or [])])


def verb_show(args) -> int:
    """查询单个任务详情。"""
    return emit_ok(_show_detail(args.board, args.id))


def verb_runs(args) -> int:
    """查询任务历次执行记录。"""
    raw = hermes_json(["--board", args.board, "runs", args.id, "--json"])
    return emit_ok([normalize_run(r) for r in (raw or [])])


def verb_stats(args) -> int:
    """查询某 board 的统计。"""
    return emit_ok(normalize_stats(hermes_json(["--board", args.board, "stats", "--json"])))


def verb_create(args) -> int:
    """创建任务后再 show 一次，返回完整 TaskDetail。"""
    cmd = ["--board", args.board, "create", args.title,
           "--assignee", args.assignee, "--priority", str(args.priority), "--json"]
    if args.body:
        cmd += ["--body", args.body]
    for sk in args.skill:
        cmd += ["--skill", sk]
    if args.workspace:
        cmd += ["--workspace", args.workspace]
    if args.parent:
        cmd += ["--parent", args.parent]
    if args.max_retries is not None:
        cmd += ["--max-retries", str(args.max_retries)]
    created = hermes_json(cmd)
    task_id = created.get("id") if isinstance(created, dict) else None
    if not task_id:
        raise KanbanError("INTERNAL", "hermes create 未返回 task id")
    return emit_ok(_show_detail(args.board, task_id))


def verb_comment(args) -> int:
    """给任务加评论，返回更新后的 TaskDetail。"""
    hermes_ok(["--board", args.board, "comment", args.id, args.body])
    return emit_ok(_show_detail(args.board, args.id))


def verb_complete(args) -> int:
    """标记任务完成，返回更新后的 TaskDetail。"""
    cmd = ["--board", args.board, "complete", args.id]
    if args.result:
        cmd += ["--result", args.result]
    hermes_ok(cmd)
    return emit_ok(_show_detail(args.board, args.id))


def verb_block(args) -> int:
    """阻塞任务，返回更新后的 TaskDetail。"""
    hermes_ok(["--board", args.board, "block", args.id, args.reason])
    return emit_ok(_show_detail(args.board, args.id))


def verb_unblock(args) -> int:
    """解除阻塞，返回更新后的 TaskDetail。"""
    hermes_ok(["--board", args.board, "unblock", args.id])
    return emit_ok(_show_detail(args.board, args.id))


def verb_archive(args) -> int:
    """归档任务，返回更新后的 TaskDetail。"""
    hermes_ok(["--board", args.board, "archive", args.id])
    return emit_ok(_show_detail(args.board, args.id))


def verb_reassign(args) -> int:
    """重新分配任务，返回更新后的 TaskDetail。"""
    hermes_ok(["--board", args.board, "reassign", args.id, args.to])
    return emit_ok(_show_detail(args.board, args.id))


def verb_reclaim(args) -> int:
    """撤销任务认领，返回更新后的 TaskDetail。"""
    hermes_ok(["--board", args.board, "reclaim", args.id])
    return emit_ok(_show_detail(args.board, args.id))


def verb_watch(args) -> int:
    """订阅 board 事件流：把 hermes watch 的每行规整成契约 Event 后 NDJSON 输出。"""
    proc = subprocess.Popen(
        ["hermes", "kanban", "--board", args.board, "watch"],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    emitted = 0
    try:
        for line in proc.stdout:
            line = line.strip()
            if not line:
                continue
            try:
                raw = json.loads(line)
            except json.JSONDecodeError:
                # 跳过非 JSON 行（hermes 可能混入人读日志），不污染契约流。
                continue
            sys.stdout.write(json.dumps(normalize_event(raw), ensure_ascii=False) + "\n")
            sys.stdout.flush()
            emitted += 1
        proc.wait()
    finally:
        if proc.poll() is None:
            proc.terminate()
    # 一条事件都没输出且进程异常退出——视为启动失败，输出错误信封。
    if proc.returncode not in (0, None) and emitted == 0:
        return emit_err(classify_hermes_error(proc.stderr or ""),
                        (proc.stderr or "hermes kanban watch 启动失败").strip()[:1024])
    return 0 if proc.returncode in (0, None) else 1


# verb 名 → handler 函数。
VERB_HANDLERS = {
    "boards": verb_boards, "list": verb_list, "show": verb_show,
    "runs": verb_runs, "stats": verb_stats, "watch": verb_watch,
    "create": verb_create, "comment": verb_comment, "complete": verb_complete,
    "block": verb_block, "unblock": verb_unblock, "archive": verb_archive,
    "reassign": verb_reassign, "reclaim": verb_reclaim,
}


def build_parser() -> argparse.ArgumentParser:
    """构造 argparse 解析器：每个 verb 一个 subparser，全部用 flag、无 positional。"""
    p = argparse.ArgumentParser(prog="oc-kanban")
    sub = p.add_subparsers(dest="verb", required=True)

    sub.add_parser("capabilities")
    sub.add_parser("boards")

    sp = sub.add_parser("list")
    sp.add_argument("--board", default="default")
    sp.add_argument("--status")
    sp.add_argument("--assignee")

    # show / runs / unblock / archive / reclaim：仅 --board + --id。
    for v in ("show", "runs", "unblock", "archive", "reclaim"):
        sp = sub.add_parser(v)
        sp.add_argument("--board", default="default")
        sp.add_argument("--id", required=True)

    # stats / watch：仅 --board。
    for v in ("stats", "watch"):
        sp = sub.add_parser(v)
        sp.add_argument("--board", default="default")

    sp = sub.add_parser("create")
    sp.add_argument("--board", default="default")
    sp.add_argument("--title", required=True)
    sp.add_argument("--assignee", required=True)
    sp.add_argument("--priority", type=int, default=0)
    sp.add_argument("--body")
    sp.add_argument("--skill", action="append", default=[])
    sp.add_argument("--workspace")
    sp.add_argument("--parent")
    sp.add_argument("--max-retries", type=int)

    sp = sub.add_parser("comment")
    sp.add_argument("--board", default="default")
    sp.add_argument("--id", required=True)
    sp.add_argument("--body", required=True)

    sp = sub.add_parser("complete")
    sp.add_argument("--board", default="default")
    sp.add_argument("--id", required=True)
    sp.add_argument("--result")

    sp = sub.add_parser("block")
    sp.add_argument("--board", default="default")
    sp.add_argument("--id", required=True)
    sp.add_argument("--reason", required=True)

    sp = sub.add_parser("reassign")
    sp.add_argument("--board", default="default")
    sp.add_argument("--id", required=True)
    sp.add_argument("--to", required=True)

    return p


def main(argv=None) -> int:
    """入口：解析参数 → 分发 verb → 统一异常兜底成失败信封。"""
    args = build_parser().parse_args(argv)  # 用法错误时 argparse 自行 exit 2
    try:
        if args.verb == "capabilities":
            return verb_capabilities(args)
        # 非 capabilities 的功能 verb 需要真实 hermes；stub 镜像直接返回 UNSUPPORTED。
        if not has_real_hermes():
            return emit_err("UNSUPPORTED", "该镜像不支持任务看板")
        return VERB_HANDLERS[args.verb](args)
    except KanbanError as e:
        return emit_err(e.code, e.message)
    except Exception as e:  # 任何未预期异常兜底成 INTERNAL，保证永远输出信封
        return emit_err("INTERNAL", f"oc-kanban 内部错误: {e}")


if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 2: 语法检查**

Run: `python3 -m py_compile runtime/hermes/hermes-main/oc-kanban.py && echo "compile ok"`
Expected: 输出 `compile ok`，无 SyntaxError。

- [ ] **Step 3: 本地验证 capabilities（不依赖 hermes）**

`capabilities` 不调 hermes，本地可直接跑。本地无真实 hermes，预期 `verbs` 为空、`features` 全 `false`。

Run: `python3 runtime/hermes/hermes-main/oc-kanban.py capabilities`
Expected: 一行 JSON，形如
`{"ok": true, "data": {"contract_version": "1.0", "oc_kanban_version": "1", "hermes_version": null, "variant": "hermes-main", "verbs": [], "features": {"write": false, "watch": false, "runs": false, "stats": false}}}`

- [ ] **Step 4: 本地验证用法错误退出码**

Run: `python3 runtime/hermes/hermes-main/oc-kanban.py ; echo "exit=$?"`
Expected: argparse 在 stderr 报缺少 verb，`exit=2`。

- [ ] **Step 5: 本地验证 stub 降级（无真实 hermes 时功能 verb 返回 UNSUPPORTED）**

Run: `python3 runtime/hermes/hermes-main/oc-kanban.py list --board default ; echo "exit=$?"`
Expected: `{"ok": false, "error": {"code": "UNSUPPORTED", "message": "该镜像不支持任务看板"}}` 且 `exit=1`。

- [ ] **Step 6: Commit**

```bash
git add runtime/hermes/hermes-main/oc-kanban.py
git commit -m "feat(hermes-runtime): 增加 hermes-main 变体的 oc-kanban 适配实现

oc-kanban 作为 hermes kanban CLI 的稳定适配层：subprocess 调 hermes
kanban、把输出规整成契约结构、统一 {ok,data/error} 信封输出。覆盖
15 个 verb（capabilities + 5 读 + watch + 8 写），写操作统一返回
TaskDetail，capabilities 自描述契约版本与能力。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## 阶段 2 · 契约测试与构建集成

### Task 3: 升级 test_kanban_contract.py 为契约一致性测试

**Files:**
- Modify: `runtime/hermes/hermes-main/tests/test_kanban_contract.py`（整体重写）

现有该文件直接调 `hermes kanban` 验证字段名。升级为：调 `oc-kanban` 各 verb，用 `kanban-contract/schema/` 的 JSON Schema 逐一校验输出符合契约。

- [ ] **Step 1: 整体重写 `test_kanban_contract.py`**

```python
"""oc-kanban 契约一致性测试。

验证本镜像的 oc-kanban 输出符合 kanban-contract/schema/ 定义的契约。
该测试在构建出的镜像里运行（make verify-hermes-runtime），任何 verb
输出违反契约即构建失败。capabilities 不依赖 hermes，stub 镜像也跑；
其余用例需真实 hermes，stub 镜像跳过。
"""

import json
import shutil
import subprocess
from pathlib import Path

import pytest
from jsonschema import validate

# 镜像内契约 schema 目录（Dockerfile 把 kanban-contract/ COPY 到此）。
SCHEMA_DIR = Path("/usr/local/lib/oc-kanban/contract/schema")


def _load_schema(name):
    """加载并返回一个契约 schema。"""
    return json.loads((SCHEMA_DIR / name).read_text())


def _has_real_hermes():
    """探测镜像是否带真实 hermes kanban CLI。"""
    if shutil.which("hermes") is None:
        return False
    proc = subprocess.run(["hermes", "kanban", "--help"], capture_output=True, text=True)
    return proc.returncode == 0


def _run_oc_kanban(*args, timeout=40):
    """跑一条 oc-kanban 命令，返回 (信封 dict, 退出码)。"""
    proc = subprocess.run(["oc-kanban", *args], capture_output=True, text=True, timeout=timeout)
    return json.loads(proc.stdout), proc.returncode


# 覆盖：capabilities 不依赖 hermes，stub 镜像也能跑——输出须符合信封 + capabilities schema。
def test_capabilities_matches_schema():
    """capabilities 输出符合 envelope 与 capabilities schema。"""
    env, code = _run_oc_kanban("capabilities")
    validate(env, _load_schema("envelope.schema.json"))
    assert env["ok"] is True
    validate(env["data"], _load_schema("capabilities.schema.json"))
    assert code == 0


# 以下用例需要真实 hermes，stub 镜像跳过。
real_only = pytest.mark.skipif(not _has_real_hermes(), reason="stub 镜像无真实 hermes")


@pytest.fixture(autouse=True)
def isolated_hermes_home(tmp_path, monkeypatch):
    """每个测试用独立 HERMES_HOME，kanban.db 落临时目录，避免测试间状态串扰。"""
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))


@real_only
def test_boards_matches_schema():
    """覆盖：boards 输出每个元素符合 board schema。"""
    subprocess.run(["hermes", "kanban", "init"], capture_output=True)
    env, code = _run_oc_kanban("boards")
    validate(env, _load_schema("envelope.schema.json"))
    assert env["ok"] is True and code == 0
    board_schema = _load_schema("board.schema.json")
    for b in env["data"]:
        validate(b, board_schema)


@real_only
def test_list_matches_schema():
    """覆盖：list 输出是数组，每个元素符合 task schema。"""
    subprocess.run(["hermes", "kanban", "init"], capture_output=True)
    env, code = _run_oc_kanban("list", "--board", "default")
    assert env["ok"] is True and code == 0
    task_schema = _load_schema("task.schema.json")
    for t in env["data"]:
        validate(t, task_schema)


@real_only
def test_create_show_returns_task_detail():
    """覆盖：create 与 show 都返回符合 task-detail schema 的 TaskDetail。"""
    subprocess.run(["hermes", "kanban", "init"], capture_output=True)
    detail_schema = _load_schema("task-detail.schema.json")
    env, code = _run_oc_kanban("create", "--board", "default",
                               "--title", "契约测试任务", "--assignee", "default")
    assert env["ok"] is True and code == 0
    validate(env["data"], detail_schema)
    task_id = env["data"]["task"]["id"]
    env2, _ = _run_oc_kanban("show", "--board", "default", "--id", task_id)
    assert env2["ok"] is True
    validate(env2["data"], detail_schema)


@real_only
def test_stats_matches_schema():
    """覆盖：stats 输出符合 stats schema。"""
    subprocess.run(["hermes", "kanban", "init"], capture_output=True)
    env, code = _run_oc_kanban("stats", "--board", "default")
    assert env["ok"] is True and code == 0
    validate(env["data"], _load_schema("stats.schema.json"))


@real_only
def test_runs_returns_array():
    """覆盖：runs 输出是数组（新任务无执行历史时为空），元素若有则符合 run schema。"""
    subprocess.run(["hermes", "kanban", "init"], capture_output=True)
    env, _ = _run_oc_kanban("create", "--board", "default",
                            "--title", "runs 测试", "--assignee", "default")
    task_id = env["data"]["task"]["id"]
    env2, code = _run_oc_kanban("runs", "--board", "default", "--id", task_id)
    assert env2["ok"] is True and code == 0 and isinstance(env2["data"], list)
    run_schema = _load_schema("run.schema.json")
    for r in env2["data"]:
        validate(r, run_schema)


@real_only
def test_write_verb_returns_task_detail():
    """覆盖：写操作（以 comment 为例）返回更新后的、符合 task-detail schema 的 TaskDetail。"""
    subprocess.run(["hermes", "kanban", "init"], capture_output=True)
    detail_schema = _load_schema("task-detail.schema.json")
    env, _ = _run_oc_kanban("create", "--board", "default",
                            "--title", "写操作测试", "--assignee", "default")
    task_id = env["data"]["task"]["id"]
    env2, code = _run_oc_kanban("comment", "--board", "default",
                                "--id", task_id, "--body", "一条评论")
    assert env2["ok"] is True and code == 0
    validate(env2["data"], detail_schema)
    # 写操作返回的 detail 应已包含刚加的评论。
    assert any(c["body"] == "一条评论" for c in env2["data"]["comments"])


@real_only
def test_not_found_returns_error_envelope():
    """覆盖：show 不存在的任务返回符合 envelope schema 的错误信封、退出码 1。"""
    subprocess.run(["hermes", "kanban", "init"], capture_output=True)
    env, code = _run_oc_kanban("show", "--board", "default", "--id", "t_nonexistent")
    validate(env, _load_schema("envelope.schema.json"))
    assert env["ok"] is False
    assert env["error"]["code"] in ("NOT_FOUND", "HERMES_CLI_FAILED")
    assert code == 1
```

- [ ] **Step 2: 语法检查**

Run: `python3 -m py_compile runtime/hermes/hermes-main/tests/test_kanban_contract.py && echo "compile ok"`
Expected: 输出 `compile ok`。

- [ ] **Step 3: Commit**

```bash
git add runtime/hermes/hermes-main/tests/test_kanban_contract.py
git commit -m "test(hermes-runtime): 升级 kanban 契约测试为 oc-kanban schema 校验

不再直接调 hermes kanban，改调 oc-kanban 各 verb，用 kanban-contract
的 JSON Schema 逐一校验输出符合契约。capabilities 用例不依赖 hermes、
stub 镜像也跑；其余用例需真实 hermes，stub 跳过。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

### Task 4: 构建集成

**Files:**
- Modify: `Makefile`（新增 `hermes-inject-contract` target，`build-hermes-runtime` 依赖它）
- Modify: `.gitignore`
- Modify: `runtime/hermes/hermes-main/Dockerfile`
- Modify: `runtime/hermes/hermes-main/Dockerfile.dev`
- Modify: `runtime/hermes/hermes-main/CONTRACT.md`

- [ ] **Step 1: 确认 Makefile 已有变量**

Run: `grep -n "HERMES_VARIANT_DIR\|HERMES_VARIANT " Makefile`
Expected: 看到 `HERMES_VARIANT ?= ...` 与 `HERMES_VARIANT_DIR := runtime/hermes/$(HERMES_VARIANT)`（或等价定义）。这两个变量本计划直接复用，不新增。

- [ ] **Step 2: Makefile —— 新增 `hermes-inject-contract` target 并让构建依赖它**

在 `.PHONY` 行追加 `hermes-inject-contract`。在 `build-hermes-runtime` target 之前插入：

```makefile
hermes-inject-contract: ## 把契约工件注入变体目录（避开 Dockerfile 跨目录 COPY 约束）
	rm -rf $(HERMES_VARIANT_DIR)/kanban-contract
	cp -r runtime/hermes/kanban-contract $(HERMES_VARIANT_DIR)/kanban-contract
```

把 `build-hermes-runtime:` 这一行改为依赖该 target：

```makefile
build-hermes-runtime: hermes-inject-contract ## 本地 dev 构建 hermes runtime（tag: hermes-runtime:<variant>-dev）
```

同时检查 Makefile 里是否还有其它 `docker build` hermes 变体的 target（如 stub 构建、生产发布）。每一个都改成依赖 `hermes-inject-contract`，保证任何途径构建的镜像都带契约工件。

- [ ] **Step 3: `.gitignore` —— 忽略变体目录内的契约副本**

在文件末尾追加：

```gitignore
# oc-kanban 契约工件构建期注入到变体目录的副本；canonical 在 runtime/hermes/kanban-contract/
runtime/hermes/*/kanban-contract/
```

（`*` 只匹配变体目录如 `hermes-main`，不会误伤 canonical 的 `runtime/hermes/kanban-contract/`。）

- [ ] **Step 4: `Dockerfile` —— COPY oc-kanban 与契约工件、加 jsonschema 依赖**

在第 41 行 `RUN uv pip install --system --no-cache-dir pyyaml pytest` 末尾追加 `jsonschema`：

```dockerfile
RUN uv pip install --system --no-cache-dir pyyaml pytest jsonschema
```

在现有 `COPY oc-channel-unbind.py ...` 之后、`COPY healthcheck.sh ...` 之前，新增两行：

```dockerfile
COPY oc-kanban.py         /usr/local/bin/oc-kanban
COPY kanban-contract/     /usr/local/lib/oc-kanban/contract/
```

在 `RUN chmod +x ...` 那一段的命令列表里加上 `/usr/local/bin/oc-kanban`。

- [ ] **Step 5: `Dockerfile.dev` —— stub 镜像同样安装 oc-kanban**

先 `cat runtime/hermes/hermes-main/Dockerfile.dev` 看现有 `COPY oc-*` 与 `chmod` 结构，照其风格新增同样的两行 COPY（`oc-kanban.py` → `/usr/local/bin/oc-kanban`、`kanban-contract/` → `/usr/local/lib/oc-kanban/contract/`）并把 `/usr/local/bin/oc-kanban` 加入 `chmod +x` 列表。stub 镜像无真实 hermes，`oc-kanban` 会自动对功能 verb 返回 `UNSUPPORTED`、`capabilities` 返回空 verbs，无需额外处理。

- [ ] **Step 6: `CONTRACT.md` —— 补充对外命令**

把「# 镜像对外命令」一节的命令列表改为：

```markdown
# 镜像对外命令
- oc-info / oc-doctor / oc-healthcheck
- oc-channel-login / oc-channel-status / oc-channel-unbind
- oc-kanban
- ENTRYPOINT: tini -g -- /usr/local/bin/oc-entrypoint
```

- [ ] **Step 7: 验证注入 target 可独立运行**

Run: `make hermes-inject-contract HERMES_VARIANT=hermes-main && ls runtime/hermes/hermes-main/kanban-contract/schema/`
Expected: 列出 8 个 `*.schema.json` 文件。

- [ ] **Step 8: 验证 git 不跟踪注入副本**

Run: `git status --porcelain runtime/hermes/hermes-main/kanban-contract/`
Expected: 无输出（注入副本被 `.gitignore` 忽略）。

- [ ] **Step 9: Commit**

```bash
git add Makefile .gitignore runtime/hermes/hermes-main/Dockerfile \
        runtime/hermes/hermes-main/Dockerfile.dev runtime/hermes/hermes-main/CONTRACT.md
git commit -m "build(hermes-runtime): 集成 oc-kanban 与契约工件到镜像构建

新增 hermes-inject-contract target，构建前把 canonical 契约工件拷进
变体目录（避开 Dockerfile 跨目录 COPY 约束），副本走 .gitignore。
Dockerfile/Dockerfile.dev COPY oc-kanban 与契约工件、补 jsonschema
依赖，CONTRACT.md 登记 oc-kanban 为镜像对外命令。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

### Task 5: 构建镜像、契约测试验证、runs 实测校准

本 task 不写新代码，是在真实镜像内验证 Task 1–4 的产物，并据实测结果校准 `run.schema.json`。

- [ ] **Step 1: 构建 hermes-main 镜像**

Run: `make build-hermes-runtime HERMES_VARIANT=hermes-main`
Expected: 构建成功，产出 `hermes-runtime:hermes-main-dev`。构建日志中可见 `COPY oc-kanban.py` 与 `COPY kanban-contract/` 步骤。

- [ ] **Step 2: 跑镜像内契约测试**

Run: `make verify-hermes-runtime HERMES_VARIANT=hermes-main`
Expected: pytest 输出包含 `test_kanban_contract.py` 的全部用例，`test_capabilities_matches_schema` 及 7 个 `real_only` 用例全部 PASS。

- [ ] **Step 3: 若契约测试失败 —— 修复**

按失败信息定位：schema 太严（hermes 实际输出含 schema 未声明的字段且 `additionalProperties:false`）→ 修对应 `schema/*.json`；规整逻辑漏字段 → 修 `oc-kanban.py` 的 `normalize_*` 函数。修完重跑 Step 1–2，直到全绿。修改 schema 后须重跑 `make hermes-inject-contract` 再构建。

- [ ] **Step 4: runs 实测校准**

在镜像内跑一个会真正执行的任务，观察 `oc-kanban runs` 的真实输出：

```bash
docker run --rm --entrypoint bash hermes-runtime:hermes-main-dev -lc '
  export HERMES_HOME=/tmp/h && hermes kanban init >/dev/null 2>&1
  cid=$(oc-kanban create --board default --title runs实测 --assignee default | python3 -c "import sys,json;print(json.load(sys.stdin)[\"data\"][\"task\"][\"id\"])")
  oc-kanban runs --board default --id "$cid"
'
```

把实际输出的 `data` 数组元素字段与 `run.schema.json` 比对：若有差异（字段名、类型、可空性），更新 `runtime/hermes/kanban-contract/schema/run.schema.json` 使之吻合，并在 `oc-kanban.py` 的 `RUN_FIELDS` / `normalize_run` 同步。若该 hermes 版本 runs 始终返回空数组、无法实测，在 `run.schema.json` 顶部加注释说明字段为 best-effort。改动后重跑 Step 1–2。

- [ ] **Step 5: Commit（若 Step 3/4 有改动）**

```bash
git add runtime/hermes/kanban-contract/ runtime/hermes/hermes-main/oc-kanban.py
git commit -m "fix(hermes-runtime): 按真实 hermes 输出校准 oc-kanban 契约

镜像内契约测试与 runs 实测发现的 schema/规整偏差修正。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

（若 Step 3/4 无改动则跳过本 commit。）

---

## 阶段 3 · manager 后端改造

### Task 6: manager 后端切换到 oc-kanban 契约

**Files:**
- Modify: `internal/service/hermes_kanban_types.go`
- Modify: `internal/service/hermes_kanban.go`
- Modify: `internal/service/hermes_kanban_test.go`
- Modify: `internal/api/handlers/hermes_kanban.go`

manager 后端从「调 `hermes kanban` + 解析裸 JSON」整体切换到「调 `oc-kanban` + 解析信封」。service、handler、测试紧耦合，必须一起改、结束时 `go build ./... && go test ./internal/...` 全绿。

- [ ] **Step 1: `hermes_kanban_types.go` —— 新增 capabilities 类型**

在文件末尾追加：

```go
// KanbanFeatures 描述 oc-kanban 的细粒度能力开关，对应 capabilities.features。
type KanbanFeatures struct {
	// Write 表示是否支持写操作（create/comment/...）。
	Write bool `json:"write"`
	// Watch 表示是否支持实时事件流。
	Watch bool `json:"watch"`
	// Runs 表示是否支持查询执行历史。
	Runs bool `json:"runs"`
	// Stats 表示是否支持统计。
	Stats bool `json:"stats"`
}

// KanbanCapabilities 对应 `oc-kanban capabilities` 的 data 段，
// 供 manager 探测实例 oc-kanban 的契约版本与可用能力、据此降级。
type KanbanCapabilities struct {
	// ContractVersion 是 oc-kanban 契约版本号（MAJOR.MINOR）。
	ContractVersion string `json:"contract_version"`
	// OCKanbanVersion 是 oc-kanban 实现版本。
	OCKanbanVersion string `json:"oc_kanban_version"`
	// HermesVersion 是底层 hermes 版本（信息性，可能为空）。
	HermesVersion string `json:"hermes_version,omitempty"`
	// Variant 是镜像变体标识。
	Variant string `json:"variant"`
	// Verbs 是本镜像实际支持的功能 verb 清单。
	Verbs []string `json:"verbs"`
	// Features 是细粒度能力开关。
	Features KanbanFeatures `json:"features"`
}
```

- [ ] **Step 2: `hermes_kanban.go` —— 用 runOCKanban 替换 runCLI**

把现有 `runCLI` 方法（注释「runCLI 在 hermes 容器内执行一条 kanban 命令」那一整个方法）整体替换为下面三段：

```go
// kanbanEnvelope 是 oc-kanban 输出的统一信封（契约 §4.1）。
type kanbanEnvelope struct {
	OK    bool                 `json:"ok"`
	Data  json.RawMessage      `json:"data"`
	Error *kanbanEnvelopeError `json:"error"`
}

// kanbanEnvelopeError 是失败信封里的错误对象。
type kanbanEnvelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// mapKanbanErrorCode 把 oc-kanban 契约错误码映射成 service 哨兵错误。
func mapKanbanErrorCode(e *kanbanEnvelopeError) error {
	if e == nil {
		return ErrKanbanCLI
	}
	switch e.Code {
	case "BAD_REQUEST":
		return fmt.Errorf("%w: %s", ErrKanbanBadRequest, e.Message)
	case "NOT_FOUND":
		return ErrNotFound
	case "UNSUPPORTED":
		return ErrKanbanNotSupported
	case "INTERNAL":
		return fmt.Errorf("%w: %s", ErrKanbanOutputInvalid, e.Message)
	default: // HERMES_CLI_FAILED 及未知码统一归为 CLI 失败
		return fmt.Errorf("%w: %s", ErrKanbanCLI, e.Message)
	}
}

// runOCKanban 在 hermes 容器内执行一条 oc-kanban 命令，解析统一信封：
// 成功返回 data 段；失败按契约错误码映射成 service 哨兵错误。
// args 是 oc-kanban 的 verb 及其 flag，不含 "oc-kanban" 前缀。
func (s *HermesKanbanService) runOCKanban(ctx context.Context, loc KanbanAppLocation, args []string) (json.RawMessage, error) {
	cmd := append([]string{"oc-kanban"}, args...)
	res, err := s.execer.ContainerExecJSON(ctx, loc.NodeID, loc.ContainerID, cmd)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKanbanCLI, err)
	}
	var env kanbanEnvelope
	if e := json.Unmarshal([]byte(res.Stdout), &env); e != nil {
		// stdout 非合法信封 JSON：老镜像无 oc-kanban、argparse 用法错误等。
		// 按 rune 截断 stderr，避免在多字节字符中间切断。
		msg := strings.TrimSpace(res.Stderr)
		if runes := []rune(msg); len(runes) > 1024 {
			msg = string(runes[:1024])
		}
		return nil, fmt.Errorf("%w: 信封解析失败: %v (stderr: %s)", ErrKanbanOutputInvalid, e, msg)
	}
	if !env.OK {
		return nil, mapKanbanErrorCode(env.Error)
	}
	return env.Data, nil
}

// runWriteVerb 执行一个写 verb 并把信封 data 解析为 KanbanTaskDetail。
// oc-kanban 的写操作统一返回更新后的完整任务详情（契约 §4.2）。
func (s *HermesKanbanService) runWriteVerb(ctx context.Context, loc KanbanAppLocation, args []string) (KanbanTaskDetail, error) {
	data, err := s.runOCKanban(ctx, loc, args)
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	var detail KanbanTaskDetail
	if err := json.Unmarshal(data, &detail); err != nil {
		return KanbanTaskDetail{}, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
	}
	return detail, nil
}
```

- [ ] **Step 3: `hermes_kanban.go` —— 改 5 个读 verb 方法**

每个读方法把 `runCLI(ctx, loc, board, verbArgs)` 调用替换为 `runOCKanban(ctx, loc, args)`，`args` 用下表的 oc-kanban flag 风格，解析目标不变。各方法原有的参数校验（`validateBoard`、`taskIDRe`、`kanbanStatuses`、`boardSlugRe` 等）全部保留。

| 方法 | 新 `args` |
|---|---|
| `ListBoards` | `[]string{"boards"}` |
| `ListTasks` | `[]string{"list", "--board", board}`，有 status 追加 `"--status", f.Status`，有 assignee 追加 `"--assignee", f.Assignee` |
| `ShowTask` | `[]string{"show", "--board", b, "--id", taskID}` |
| `TaskRuns` | `[]string{"runs", "--board", b, "--id", taskID}` |
| `Stats` | `[]string{"stats", "--board", b}` |

`ListTasks` 的完整改造后形态（其余读方法照此模式，仅 `args` 与解析目标类型不同）：

```go
func (s *HermesKanbanService) ListTasks(ctx context.Context, principal auth.Principal, appID string, f KanbanTaskFilter) ([]KanbanTask, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	board, err := validateBoard(f.Board)
	if err != nil {
		return nil, err
	}
	args := []string{"list", "--board", board}
	if f.Status != "" {
		if !kanbanStatuses[f.Status] {
			return nil, fmt.Errorf("%w: 非法 status", ErrKanbanBadRequest)
		}
		args = append(args, "--status", f.Status)
	}
	if f.Assignee != "" {
		if !boardSlugRe.MatchString(f.Assignee) {
			return nil, fmt.Errorf("%w: 非法 assignee", ErrKanbanBadRequest)
		}
		args = append(args, "--assignee", f.Assignee)
	}
	data, err := s.runOCKanban(ctx, loc, args)
	if err != nil {
		return nil, err
	}
	var tasks []KanbanTask
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
	}
	return tasks, nil
}
```

读方法解析目标：`ListBoards`→`[]KanbanBoard`、`ListTasks`→`[]KanbanTask`、`ShowTask`→`KanbanTaskDetail`、`TaskRuns`→`[]KanbanTaskRun`、`Stats`→`KanbanStats`。

- [ ] **Step 4: `hermes_kanban.go` —— 改 `CreateTask`**

`oc-kanban create` 直接返回 `TaskDetail`，删掉旧的「先解析扁平 `KanbanTask` 再手动包装」逻辑。完整新版：

```go
func (s *HermesKanbanService) CreateTask(ctx context.Context, principal auth.Principal, appID string, in CreateKanbanTaskInput) (KanbanTaskDetail, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	board, err := validateBoard(in.Board)
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return KanbanTaskDetail{}, fmt.Errorf("%w: 标题不能为空", ErrKanbanBadRequest)
	}
	if !boardSlugRe.MatchString(in.Assignee) {
		return KanbanTaskDetail{}, fmt.Errorf("%w: 非法 assignee", ErrKanbanBadRequest)
	}
	if in.Priority < 0 || in.Priority > 9 {
		return KanbanTaskDetail{}, fmt.Errorf("%w: priority 越界", ErrKanbanBadRequest)
	}
	args := []string{"create", "--board", board, "--title", in.Title,
		"--assignee", in.Assignee, "--priority", fmt.Sprintf("%d", in.Priority)}
	if in.Body != "" {
		args = append(args, "--body", in.Body)
	}
	for _, sk := range in.Skills {
		if !skillNameRe.MatchString(sk) {
			return KanbanTaskDetail{}, fmt.Errorf("%w: 非法 skill 名称: %s", ErrKanbanBadRequest, sk)
		}
		args = append(args, "--skill", sk)
	}
	if in.Workspace != "" {
		if !kanbanWorkspaceRe.MatchString(in.Workspace) {
			return KanbanTaskDetail{}, fmt.Errorf("%w: 非法 workspace 值", ErrKanbanBadRequest)
		}
		args = append(args, "--workspace", in.Workspace)
	}
	if in.ParentID != "" {
		if !taskIDRe.MatchString(in.ParentID) {
			return KanbanTaskDetail{}, fmt.Errorf("%w: 非法 parent id", ErrKanbanBadRequest)
		}
		args = append(args, "--parent", in.ParentID)
	}
	if in.MaxRetries > 0 {
		args = append(args, "--max-retries", fmt.Sprintf("%d", in.MaxRetries))
	}
	return s.runWriteVerb(ctx, loc, args)
}
```

- [ ] **Step 5: `hermes_kanban.go` —— 改 8 个状态写 verb 方法签名与实现**

8 个写方法（`Comment`/`Complete`/`Block`/`Unblock`/`Archive`/`Reassign`/`Reclaim`）返回值由 `error` 改为 `(KanbanTaskDetail, error)`，末尾改用 `runWriteVerb`。各方法原有的参数校验全部保留（校验失败时返回 `KanbanTaskDetail{}, err`）。`Reassign` 的 profile 改用 `--to` flag（不再 positional）。

`Comment` 的完整改造后形态（其余 7 个照此模式，仅校验与 `args` 不同）：

```go
func (s *HermesKanbanService) Comment(ctx context.Context, principal auth.Principal, appID, board, taskID, body string) (KanbanTaskDetail, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	if !taskIDRe.MatchString(taskID) {
		return KanbanTaskDetail{}, fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	if strings.TrimSpace(body) == "" {
		return KanbanTaskDetail{}, fmt.Errorf("%w: 评论内容不能为空", ErrKanbanBadRequest)
	}
	return s.runWriteVerb(ctx, loc, []string{"comment", "--board", b, "--id", taskID, "--body", body})
}
```

其余 7 个写方法的签名与 `args`（校验逻辑沿用现有代码，仅 `return err` 改为 `return KanbanTaskDetail{}, err`）：

| 方法 | 签名（返回值统一加 `KanbanTaskDetail`） | `runWriteVerb` 的 `args` |
|---|---|---|
| `Complete(ctx, p, appID, board, taskID, result string)` | `(KanbanTaskDetail, error)` | `[]string{"complete", "--board", b, "--id", taskID}`，`result != ""` 时追加 `"--result", result` |
| `Block(ctx, p, appID, board, taskID, reason string)` | `(KanbanTaskDetail, error)` | `[]string{"block", "--board", b, "--id", taskID, "--reason", reason}` |
| `Unblock(ctx, p, appID, board, taskID string)` | `(KanbanTaskDetail, error)` | `[]string{"unblock", "--board", b, "--id", taskID}` |
| `Archive(ctx, p, appID, board, taskID string)` | `(KanbanTaskDetail, error)` | `[]string{"archive", "--board", b, "--id", taskID}` |
| `Reassign(ctx, p, appID, board, taskID, profile string)` | `(KanbanTaskDetail, error)` | `[]string{"reassign", "--board", b, "--id", taskID, "--to", profile}` |
| `Reclaim(ctx, p, appID, board, taskID string)` | `(KanbanTaskDetail, error)` | `[]string{"reclaim", "--board", b, "--id", taskID}` |

- [ ] **Step 6: `hermes_kanban.go` —— 新增 `Capabilities` 方法**

在写 verb 方法之后追加：

```go
// Capabilities 探测实例 oc-kanban 的契约版本与可用能力。
// 仅需读权限，故用 resolve（与读 verb 一致）。stub 实例由 resolve
// 拦截返回 ErrKanbanNotSupported，前端按既有 stub 降级处理。
func (s *HermesKanbanService) Capabilities(ctx context.Context, principal auth.Principal, appID string) (KanbanCapabilities, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return KanbanCapabilities{}, err
	}
	data, err := s.runOCKanban(ctx, loc, []string{"capabilities"})
	if err != nil {
		return KanbanCapabilities{}, err
	}
	var caps KanbanCapabilities
	if err := json.Unmarshal(data, &caps); err != nil {
		return KanbanCapabilities{}, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
	}
	return caps, nil
}
```

- [ ] **Step 7: `hermes_kanban.go` —— 改 `StreamEvents` 的命令**

把 `StreamEvents` 里构造 `cmd` 的那一行：

```go
	cmd := []string{"hermes", "kanban", "--board", b, "watch"}
```

改为：

```go
	cmd := []string{"oc-kanban", "watch", "--board", b}
```

其余流处理逻辑不变。

- [ ] **Step 8: `handlers/hermes_kanban.go` —— 更新 service 接口与写端点**

(a) `hermesKanbanService` interface：8 个写方法返回值由 `error` 改为 `(service.KanbanTaskDetail, error)`；新增一行 `Capabilities(ctx context.Context, p auth.Principal, appID string) (service.KanbanCapabilities, error)`。`CreateTask` 已是 `(service.KanbanTaskDetail, error)`，不变。

(b) 8 个写端点 handler（`Comment`/`Complete`/`Block`/`Unblock`/`Archive`/`Reassign`/`Reclaim`）：把 `err := h.service.Xxx(...)` 改为 `detail, err := h.service.Xxx(...)`，把 `c.Status(http.StatusNoContent)` 改为 `c.JSON(http.StatusOK, gin.H{"task": detail})`。同时把每个写端点 swag 注释的 `@Success 204` 改为 `@Success 200 {object} map[string]service.KanbanTaskDetail`。

`Comment` handler 的完整改造后形态（其余 6 个照此模式）：

```go
func (h *HermesKanbanHandler) Comment(c *gin.Context) {
	var req KanbanCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	detail, err := h.service.Comment(c.Request.Context(), principalFromCtx(c), c.Param("appId"), req.Board, c.Param("taskId"), req.Body)
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": detail})
}
```

(c) 新增 `Capabilities` 端点，放在读端点区：

```go
// Capabilities GET /api/v1/apps/{appId}/hermes/kanban/capabilities
//
// @Summary      查询实例任务看板的 oc-kanban 能力
// @Description  返回 oc-kanban 契约版本、支持的 verb 与 feature 开关，供前端按能力降级。
// @Tags         hermes-kanban
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      200    {object}  map[string]service.KanbanCapabilities
// @Failure      403    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/capabilities [get]
func (h *HermesKanbanHandler) Capabilities(c *gin.Context) {
	caps, err := h.service.Capabilities(c.Request.Context(), principalFromCtx(c), c.Param("appId"))
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"capabilities": caps})
}
```

(d) 在 `RegisterHermesKanbanRoutes` 的「读端点」区加一行：

```go
	g.GET("/capabilities", h.Capabilities)
```

- [ ] **Step 9: `hermes_kanban_test.go` —— 加信封 helper**

在 `import` 块之后、`fakeKanbanExecer` 定义之前插入：

```go
// okEnvelope 把一段 data JSON 包成 oc-kanban 成功信封，供 fake execer 返回。
func okEnvelope(dataJSON string) string {
	return `{"ok":true,"data":` + dataJSON + `}`
}

// errEnvelope 把契约错误码包成 oc-kanban 失败信封。
func errEnvelope(code, message string) string {
	return `{"ok":false,"error":{"code":"` + code + `","message":"` + message + `"}}`
}
```

- [ ] **Step 10: `hermes_kanban_test.go` —— 改造受影响的测试**

下表逐个说明改动。未列出的测试（`TestListTasksRejectsBadBoard`、`TestListTasksRejectsBadStatus`、`TestResolveForbidden`、`TestResolveStubUnsupported`、`TestResolveRuntimeUnavailable`、`TestShowTaskRejectsBadTaskID`、`TestCreateTaskRejectsEmptyTitle`、`TestCreateTaskRejectsBadAssignee`、`TestCreateTaskRejectsBadWorkspace`、`TestStreamEventsCancelled`）逻辑不变 —— 但凡调用写方法（`Comment`/`Block`/`Reassign` 等）的，把 `err := svc.Xxx(...)` 改成 `_, err := svc.Xxx(...)` 以匹配新签名（涉及 `TestWriteVerbForbiddenForOutsider`、`TestCommentRejectsBadTaskID`、`TestBlockRejectsEmptyReason`、`TestReassignRejectsBadProfile`）。

| 测试 | 改动 |
|---|---|
| `TestListTasksHappy` | `result.Stdout` 用 `okEnvelope(`原裸 JSON 数组`)` 包裹；`lastCmd` 断言改为 `assert.Equal(t, []string{"oc-kanban", "list", "--board", "default"}, execer.lastCmd)`。 |
| `TestRunCLINonZeroExit` | 重命名为 `TestKanbanErrorCodeMapping`，改为 table-driven：`result.Stdout` 用 `errEnvelope(code,"msg")`，校验错误码→哨兵映射。见下方完整代码。 |
| `TestListTasksInvalidJSON` | 不改（`Stdout:"not json"` 仍非合法信封，仍返回 `ErrKanbanOutputInvalid`）。 |
| `TestShowTaskHappy` | `result.Stdout` 用 `okEnvelope(`原 show JSON`)` 包裹；删除 `assert.Contains(execer.lastCmd, "--json")` 这一行（oc-kanban 不带 `--json`），改为 `assert.Contains(t, execer.lastCmd, "--id")`。 |
| `TestListBoardsHappy` | `result.Stdout` 用 `okEnvelope(...)` 包裹；`lastCmd` 断言改为 `assert.Equal(t, []string{"oc-kanban", "boards"}, execer.lastCmd)`。 |
| `TestStatsHappy` | `result.Stdout` 用 `okEnvelope(...)` 包裹。 |
| `TestCreateTaskHappy` | `result.Stdout` 改为 `okEnvelope` 包裹一个 **TaskDetail** JSON（见下方完整代码——create 现在返回 detail）。 |
| `TestCompleteHappy` | `Complete` 现返回 `(detail, error)`，改 `detail, err := ...`；`result.Stdout` 用 `okEnvelope` 包 TaskDetail；保留 argv 含 `complete`/`t_1`/`已完成` 的断言。 |
| `TestBlockHappy` | `Block` 现返回 `(detail, error)`，改 `_, err := ...`；`result.Stdout` 用 `okEnvelope` 包 TaskDetail；删除「`--board` 在 `block` 之前」的 `boardIdx/blockIdx` 整段断言（oc-kanban 里 `--board` 是 verb 后的 flag），改为 `assert.Equal(t, "oc-kanban", execer.lastCmd[0])` + 保留 `Contains` 断言。 |
| `TestReassignHappy` | `Reassign` 现返回 `(detail, error)`，改 `_, err := ...`；`result.Stdout` 用 `okEnvelope` 包 TaskDetail；把「确认没有 `--to`」的循环断言**反转**为 `assert.Contains(t, execer.lastCmd, "--to")`（oc-kanban reassign 用 `--to` flag）。 |
| `TestArchiveHappy` | `Archive` 现返回 `(detail, error)`，改 `_, err := ...`；`result.Stdout` 用 `okEnvelope` 包 TaskDetail。 |
| `TestStreamEventsDeliversLines` | `lastCmd` 断言改为 `assert.Equal(t, []string{"oc-kanban", "watch", "--board", "default"}, execer.lastCmd)`。 |

`TestKanbanErrorCodeMapping`（替换 `TestRunCLINonZeroExit`）的完整代码：

```go
// TestKanbanErrorCodeMapping 验证：oc-kanban 失败信封的错误码被正确映射成 service 哨兵错误。
func TestKanbanErrorCodeMapping(t *testing.T) {
	cases := []struct {
		name    string // 测试场景
		code    string // oc-kanban 错误码
		wantErr error  // 期望映射到的哨兵错误
	}{
		{"参数非法映射为 BadRequest", "BAD_REQUEST", ErrKanbanBadRequest},   // BAD_REQUEST → ErrKanbanBadRequest
		{"资源不存在映射为 NotFound", "NOT_FOUND", ErrNotFound},             // NOT_FOUND → ErrNotFound
		{"能力不支持映射为 NotSupported", "UNSUPPORTED", ErrKanbanNotSupported}, // UNSUPPORTED → ErrKanbanNotSupported
		{"hermes 执行失败映射为 CLI 错误", "HERMES_CLI_FAILED", ErrKanbanCLI},  // HERMES_CLI_FAILED → ErrKanbanCLI
		{"内部错误映射为输出非法", "INTERNAL", ErrKanbanOutputInvalid},          // INTERNAL → ErrKanbanOutputInvalid
	}
	for _, c := range cases {
		// 每个子测试覆盖一种错误码到哨兵错误的映射路径。
		t.Run(c.name, func(t *testing.T) {
			execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
				ExitCode: 1, Stdout: errEnvelope(c.code, "失败详情"),
			}}
			svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})
			_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
			require.ErrorIs(t, err, c.wantErr)
		})
	}
}
```

`TestCreateTaskHappy` 的完整改造后形态（create 现在返回 TaskDetail）：

```go
// TestCreateTaskHappy 验证：CreateTask 拼出正确 argv 并解析 oc-kanban 返回的 TaskDetail。
func TestCreateTaskHappy(t *testing.T) {
	// oc-kanban create 返回完整 TaskDetail（task 子对象 + 关联数组）。
	detailJSON := `{"task":{"id":"t_new","title":"新任务","status":"ready","assignee":"devops",` +
		`"priority":2,"created_at":1779267436,"skills":[]},"latest_summary":null,` +
		`"parents":[],"children":[],"comments":[],"events":[]}`
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
		ExitCode: 0, Stdout: okEnvelope(detailJSON),
	}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	detail, err := svc.CreateTask(context.Background(), kanbanOrgAdmin(), "app-1", CreateKanbanTaskInput{
		Title: "新任务", Assignee: "devops", Priority: 2,
	})
	require.NoError(t, err)
	// 任务核心字段在 detail.Task 子对象内
	assert.Equal(t, "t_new", detail.Task.ID)
	assert.Equal(t, "ready", detail.Task.Status)
	// 自由文本 title 作为 --title 的独立 argv 值（不拼 shell），防注入
	assert.Contains(t, execer.lastCmd, "create")
	assert.Contains(t, execer.lastCmd, "新任务")
	assert.Equal(t, "oc-kanban", execer.lastCmd[0])
}
```

新增 `TestCapabilitiesHappy`（放文件末尾）：

```go
// TestCapabilitiesHappy 验证：Capabilities 解析 oc-kanban capabilities 信封并映射字段。
func TestCapabilitiesHappy(t *testing.T) {
	// oc-kanban capabilities 返回契约版本、verb 清单与 feature 开关。
	capsJSON := `{"contract_version":"1.0","oc_kanban_version":"1","hermes_version":"v0.14.0",` +
		`"variant":"hermes-main","verbs":["list","show","create"],` +
		`"features":{"write":true,"watch":true,"runs":true,"stats":true}}`
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
		ExitCode: 0, Stdout: okEnvelope(capsJSON),
	}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	caps, err := svc.Capabilities(context.Background(), kanbanOrgAdmin(), "app-1")
	require.NoError(t, err)
	assert.Equal(t, "1.0", caps.ContractVersion)
	assert.True(t, caps.Features.Write)
	assert.Contains(t, caps.Verbs, "create")
	// argv 为 oc-kanban capabilities
	assert.Equal(t, []string{"oc-kanban", "capabilities"}, execer.lastCmd)
}
```

- [ ] **Step 11: 编译与测试**

Run: `go build ./... && go test ./internal/service/... ./internal/api/...`
Expected: 编译通过；service 与 api 包测试全部 PASS。

- [ ] **Step 12: Commit**

```bash
git add internal/service/hermes_kanban.go internal/service/hermes_kanban_types.go \
        internal/service/hermes_kanban_test.go internal/api/handlers/hermes_kanban.go
git commit -m "feat(kanban): manager 后端切换到 oc-kanban 稳定契约

runCLI 改为 runOCKanban：调 oc-kanban、解析 {ok,data/error} 信封、
按契约错误码映射哨兵错误。读 verb 改 oc-kanban flag 风格；写 verb
统一返回 KanbanTaskDetail；新增 Capabilities 探测端点；StreamEvents
改走 oc-kanban watch。manager 不再直接依赖 hermes kanban CLI 约定。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

### Task 7: 重新生成 OpenAPI 与前端类型

**Files:**
- Modify: `openapi/openapi.yaml`（`make openapi-gen` 生成产物）
- Modify: `web/src/api/generated.ts`（`make web-types-gen` 生成产物）

Task 6 改了 handler 函数签名、写端点响应体、新增 capabilities 路由，必须按项目规范（CLAUDE.md「OpenAPI 同步」）重新生成契约文件。本 task 不手写这两个文件。

- [ ] **Step 1: 重新生成 OpenAPI**

Run: `make openapi-gen`
Expected: 命令成功，`openapi/openapi.yaml` 更新（新增 `/apps/{appId}/hermes/kanban/capabilities` 路径，写端点响应由 204 变为 200 + `KanbanTaskDetail`）。

- [ ] **Step 2: 重新生成前端类型**

Run: `make web-types-gen`
Expected: 命令成功，`web/src/api/generated.ts` 随之更新。

- [ ] **Step 3: 校验生成产物与代码一致**

Run: `make openapi-check`
Expected: 命令通过（再次跑 `openapi-gen` 后 git 工作区干净，说明 yaml 已与代码同步）。

- [ ] **Step 4: Commit**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(openapi): 同步 oc-kanban 契约改动到 OpenAPI 与前端类型

kanban 写端点响应改为返回 KanbanTaskDetail、新增 capabilities 端点，
按规范重新生成 openapi.yaml 与 generated.ts。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## 阶段 4 · 前端改造

### Task 8: 前端 useKanban —— capabilities 查询与写操作消费 TaskDetail

**Files:**
- Modify: `web/src/api/hooks/useKanban.ts`

- [ ] **Step 1: 新增 capabilities 类型与查询 hook**

在 `useKanban.ts` 中，`KanbanStats` 接口定义之后追加类型：

```ts
// KanbanFeatures 是 oc-kanban 的细粒度能力开关（对应 service.KanbanFeatures）。
export interface KanbanFeatures {
  write?: boolean
  watch?: boolean
  runs?: boolean
  stats?: boolean
}

// KanbanCapabilities 是 oc-kanban 的自描述能力（对应 service.KanbanCapabilities）。
// 前端据此降级：隐藏不支持的操作按钮、stats 徽标等。
export interface KanbanCapabilities {
  contract_version?: string
  oc_kanban_version?: string
  hermes_version?: string
  variant?: string
  // verbs 是本镜像实际支持的功能 verb 清单。
  verbs?: string[]
  features?: KanbanFeatures
}
```

在 `useKanbanStatsQuery` 之后追加 query hook：

```ts
// useKanbanCapabilitiesQuery 探测实例 oc-kanban 的契约版本与可用能力。
// capabilities 在实例生命周期内不变，故 staleTime 设为 Infinity、不轮询、不重试；
// stub 实例返回错误时查询失败，前端按既有 stub 降级路径处理。
export function useKanbanCapabilitiesQuery(appId: Ref<string | undefined>) {
  return useQuery<KanbanCapabilities | null>({
    queryKey: ['kanban', 'capabilities', appId],
    enabled: () => Boolean(appId.value),
    staleTime: Infinity,
    retry: false,
    queryFn: async () => {
      const res = await apiRequest<{ capabilities: KanbanCapabilities }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/capabilities`,
      )
      return res.capabilities ?? null
    },
  })
}
```

- [ ] **Step 2: 写操作 mutation 消费返回的 TaskDetail**

把 `useKanbanTaskAction` 整体替换为：

```ts
// useKanbanTaskAction 是统一的任务写操作 mutation（comment/complete/block/...）。
// 单 hook 覆盖所有非 create 写操作。oc-kanban 写操作返回更新后的完整 TaskDetail，
// 成功后直接写入详情缓存（详情面板即时刷新），并失效列表与统计缓存。
export function useKanbanTaskAction(appId: Ref<string | undefined>, board: Ref<string>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (action: KanbanWriteAction) => {
      // appId 为空时拒绝执行，避免向错误路径发起请求。
      if (!appId.value) throw new Error('缺少实例 ID')
      // 解构 verb 和 taskId 作为 URL 路径参数，剩余字段作为请求体追加 board。
      const { verb, taskId, ...rest } = action
      const res = await apiRequest<{ task: KanbanTaskDetail }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/tasks/${taskId}/${verb}`,
        { method: 'POST', body: { board: board.value, ...rest } },
      )
      return res.task
    },
    onSuccess: (detail, action) => {
      // 写操作返回权威 TaskDetail，直接写入详情缓存，无需再失效详情查询。
      if (detail) {
        client.setQueryData(taskKey(appId.value, board.value, action.taskId), detail)
      }
      // 状态/计数变化仍需失效任务列表与统计徽标缓存。
      void client.invalidateQueries({ queryKey: tasksKey(appId.value, board.value) })
      void client.invalidateQueries({
        queryKey: ['kanban', 'stats', appId.value, board.value],
      })
    },
  })
}
```

- [ ] **Step 3: create mutation 失效统计缓存**

`useCreateKanbanTask` 的 `onSuccess` 里，在已有的 `invalidateQueries({ queryKey: tasksKey(...) })` 之后追加一行（新建任务也改变状态计数）：

```ts
      void client.invalidateQueries({
        queryKey: ['kanban', 'stats', appId.value, board.value],
      })
```

- [ ] **Step 4: 类型检查**

Run: `cd web && npm run typecheck`
Expected: 无类型错误。

- [ ] **Step 5: Commit**

```bash
git add web/src/api/hooks/useKanban.ts
git commit -m "feat(web): 新增 kanban capabilities 查询、写操作消费 TaskDetail

新增 useKanbanCapabilitiesQuery 与 KanbanCapabilities/KanbanFeatures
类型，供前端按能力降级。写操作 mutation 改为消费 oc-kanban 返回的
TaskDetail 直接更新详情缓存，并失效统计徽标缓存。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

### Task 9: 前端按 capabilities 降级 UI

**Files:**
- Modify: `web/src/pages/apps/AppKanbanTab.vue`
- Modify: `web/src/pages/apps/kanban/KanbanTaskActions.vue`

按 `capabilities` 隐藏不支持的操作。**降级语义**：capabilities 未知（加载中 / 请求失败 / 老镜像）时默认**显示**所有操作，只有明确不支持时才隐藏 —— 避免误隐藏功能。

- [ ] **Step 1: `AppKanbanTab.vue` —— 接入 capabilities**

在 `<script setup>` 中，与现有 `useKanbanStatsQuery` 调用相邻处新增：

```ts
import { useKanbanCapabilitiesQuery } from '@/api/hooks/useKanban'
// ……
const capabilitiesQuery = useKanbanCapabilitiesQuery(appId)
// features 为 undefined 表示能力未知，按「默认显示」处理。
const kanbanFeatures = computed(() => capabilitiesQuery.data.value?.features)
```

把工具栏「+ 新建任务」按钮加上 `v-if="kanbanFeatures?.write !== false"`；把 stats 徽标元素（`<span class="stat-badge">`）加上 `v-if="kanbanFeatures?.stats !== false"`。

- [ ] **Step 2: `KanbanTaskActions.vue` —— 按 verbs 隐藏操作按钮**

先 `cat web/src/pages/apps/kanban/KanbanTaskActions.vue` 确认它如何拿到 `appId`（若无 appId prop，从父组件 `AppKanbanTab.vue` 透传一个 `appId` prop 进来）。在 `<script setup>` 中新增：

```ts
import { useKanbanCapabilitiesQuery } from '@/api/hooks/useKanban'
// ……
const capabilitiesQuery = useKanbanCapabilitiesQuery(appId)
// verbs 为 undefined 表示能力未知，按「默认显示」处理。
const supportedVerbs = computed(() => capabilitiesQuery.data.value?.verbs)
// verbSupported 判定某操作是否可用：能力未知时默认可用。
function verbSupported(verb: string): boolean {
  const verbs = supportedVerbs.value
  return !verbs || verbs.includes(verb)
}
```

给每个操作按钮（评论 / 完成 / 阻塞 / 解除阻塞 / 归档 / 重新分配 / 重置认领）加 `v-if="verbSupported('<verb>')"`，`<verb>` 取对应值：`comment` / `complete` / `block` / `unblock` / `archive` / `reassign` / `reclaim`。

- [ ] **Step 3: 类型检查**

Run: `cd web && npm run typecheck`
Expected: 无类型错误。

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/apps/AppKanbanTab.vue web/src/pages/apps/kanban/KanbanTaskActions.vue
git commit -m "feat(web): 任务看板按 oc-kanban capabilities 降级

AppKanbanTab 与 KanbanTaskActions 接入 useKanbanCapabilitiesQuery，
按 features/verbs 隐藏当前镜像不支持的操作；能力未知时默认全部显示，
避免误隐藏。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

### Task 10: 前端单元测试更新

**Files:**
- Modify: `web/src/pages/apps/AppKanbanTab.spec.ts`

- [ ] **Step 1: 给 useKanban mock 补 useKanbanCapabilitiesQuery**

`AppKanbanTab.spec.ts` 的 `vi.mock('@/api/hooks/useKanban', ...)` 工厂里，在 `useKanbanStatsQuery` mock 之后追加一项（默认返回「全部支持」，使现有用例不被降级影响）：

```ts
  // useKanbanCapabilitiesQuery：默认返回全部能力可用，不触发降级。
  useKanbanCapabilitiesQuery: () => ({
    data: ref({
      contract_version: '1.0',
      verbs: ['boards', 'list', 'show', 'runs', 'stats', 'watch',
              'create', 'comment', 'complete', 'block', 'unblock',
              'archive', 'reassign', 'reclaim'],
      features: { write: true, watch: true, runs: true, stats: true },
    }),
    isLoading: ref(false),
    error: ref(null),
  }),
```

- [ ] **Step 2: 新增降级用例**

在 `describe('AppKanbanTab', ...)` 中新增一个测试，验证 `features.write === false` 时「新建任务」按钮不渲染。由于该用例需要让 mock 返回不同的 capabilities，把上面 mock 中的 capabilities 数据抽成一个可变 `ref`（类比文件中已有的 `tasksError` / `boardsError` 模式），在用例内改写它后再挂载。具体：

```ts
// 在文件顶部、与 tasksError 相邻处新增可变 capabilities 引用。
const kanbanCapabilities = ref<unknown>({
  contract_version: '1.0',
  verbs: ['create', 'comment'],
  features: { write: true, watch: true, runs: true, stats: true },
})
```

把 Step 1 的 `useKanbanCapabilitiesQuery` mock 改成返回 `data: kanbanCapabilities`。在 `beforeEach` 里重置它为 `features.write: true`。新增用例：

```ts
  // 覆盖：capabilities 报告 write 不支持时，工具栏「新建任务」按钮被隐藏。
  it('能力降级：features.write 为 false 时隐藏新建任务按钮', () => {
    kanbanCapabilities.value = {
      contract_version: '1.0',
      verbs: ['list', 'show'],
      features: { write: false, watch: true, runs: true, stats: true },
    }
    const wrapper = mountKanbanTab()
    // NButton 被 stub，断言渲染输出里不含新建任务按钮的 stub 标记。
    // 实际断言方式以组件内「新建任务」按钮的可识别特征为准（见组件模板）。
    expect(wrapper.html()).not.toContain('新建任务')
  })
```

实现时若 `NButton` 被 stub 导致按钮文案不可见，改为给「新建任务」按钮一个 `data-testid="kanban-create-btn"`，断言 `wrapper.find('[data-testid=kanban-create-btn]').exists()` 为 `false`。

- [ ] **Step 3: 跑前端测试**

Run: `cd web && npm run test`
Expected: `AppKanbanTab.spec.ts` 全部用例 PASS，含新增降级用例。

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/apps/AppKanbanTab.spec.ts
git commit -m "test(web): 补 kanban capabilities mock 与能力降级用例

AppKanbanTab 单测加 useKanbanCapabilitiesQuery mock，新增 features
.write 为 false 时隐藏新建任务按钮的降级用例。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## 阶段 5 · 端到端验证

### Task 11: 重建镜像与实例、浏览器全量验证

**Files:** 无代码改动（验证 task）。

项目 CLAUDE.md 强制：所有新功能须用真实浏览器全量验证。本 task 验证 manager 经 `oc-kanban` 适配层完成全部 kanban 能力。

- [ ] **Step 1: 重建并部署 hermes 镜像**

确保 Task 5 构建的镜像 tag 与 `config/manager.yaml` 的 `hermes.runtime_image` 一致，且**不带 `-dev` 后缀**（manager `LocateApp` 会把 `-dev` 后缀镜像判为 stub）。

Run: `make build-hermes-runtime HERMES_VARIANT=hermes-main && docker tag hermes-runtime:hermes-main-dev hermes-runtime:hermes-main`
Expected: 产出 `hermes-runtime:hermes-main` 镜像。

- [ ] **Step 2: 重启 manager 栈并重建实例**

按本地开发流程重启 manager（使其加载 Task 6 的后端改动）并重新初始化一个 hermes 实例，使其用新镜像。验证实例容器内 `oc-kanban` 可用：

Run: `docker exec <hermes 容器> oc-kanban capabilities`
Expected: 返回成功信封，`data.verbs` 含全部 15 个功能 verb、`features` 全 `true`。

- [ ] **Step 3: 浏览器全量验证**

用真实浏览器登录 manager（平台管理员：组织标识留空 / `admin` / `admin123`），进入实例详情页「任务」tab，逐项验证：

1. 任务列表按状态分组渲染、board 切换。
2. 5 个读路径：列表、详情、执行历史、统计徽标、board 列表。
3. 8 个写操作：新建任务、评论、完成、阻塞、解除阻塞、归档、重新分配、重置认领 —— 每个操作后详情面板即时反映最新状态（验证写操作返回 TaskDetail 生效）。
4. SSE 实时事件流：watch 连接建立、事件实时到达。
5. capabilities 降级：确认正常镜像下所有按钮可见。
6. 平台管理员可填全部高级字段；组织管理员 / 组织成员仅必填字段。

遇到微信扫码 / 发送消息等需要人工配合的环节，通知用户配合。发现问题先修复再重新验证，直到全部功能正常。

- [ ] **Step 4: 跑全量自动化测试做回归**

Run: `go test ./... && cd web && npm run test`
Expected: 后端与前端测试全部 PASS。

- [ ] **Step 5: 最终提交（若 Step 3 验证中有修复）**

```bash
git add -A
git commit -m "fix(kanban): oc-kanban 适配层浏览器验证发现的问题修复

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

（若验证无问题则跳过本 commit。）

---

## 计划完成标准

- `runtime/hermes/kanban-contract/` 契约工件齐全，`oc-kanban` 在镜像内实现 15 个 verb。
- `make verify-hermes-runtime` 内的契约测试全绿。
- manager 不再出现 `hermes kanban` 字面量，全部经 `oc-kanban`；`go test ./...` 全绿。
- 前端按 capabilities 降级；`npm run test` 全绿。
- 浏览器全量验证通过。
- `make openapi-check` 通过。
