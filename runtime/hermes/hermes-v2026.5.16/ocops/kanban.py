"""oc-kanban 核心逻辑：hermes kanban CLI 的稳定适配层（可 import）。

从 oc-kanban.py 下沉而来：常量、KanbanError、hermes subprocess 调用
（run_hermes/hermes_json/hermes_ok/has_real_hermes/read_image_info）、全部
normalize_*、parse_watch_line，以及全部 verb 的核心实现。每个核心函数
`run_<verb>(...)` 接收类型化形参、返回 data（dict / list）或抛 KanbanError；
watch 改为 `watch_events(board)` generator，逐条 yield 契约 Event。
CLI 输出层（emit_ok/emit_err、argparse、UNSUPPORTED 守卫）留在 oc-kanban.py 薄 shim。

命名：oc- 前缀取自项目名 oc-manager，标识注入 hermes runtime 镜像、供容器内调用的运维
适配层（区别于 hermes 上游自带命令）；kanban = 看板（hermes kanban 稳定适配层）。

输出协议（HTTP 化后由 server 复用同一逻辑）：核心函数对内返回类型化对象，
对外的 {ok,data} 信封与 watch 的 NDJSON 流由调用方（shim / server）负责。
"""

from __future__ import annotations

import ast
import json
import os
import re
import subprocess
import time
from collections.abc import Iterator
from pathlib import Path

from ocops.errors import OpsError

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


class KanbanError(OpsError):
    """oc-kanban 业务异常，继承 OpsError 以复用 code→HTTP 映射与失败信封。

    OpsError.__init__ 已是 (code, message)，与原 KanbanError 构造签名兼容，
    emit_err 用的 code/message 字段语义不变。
    """


# ———————————————————————————————————————————————
# hermes subprocess 调用
# ———————————————————————————————————————————————

def classify_hermes_error(stderr: str) -> str:
    """按 hermes stderr 文本把失败归类成契约错误码。

    文本匹配跨版本脆弱，但这正是 oc-kanban 该承担、关在适配层内的脏活；
    每个 hermes 版本的 oc-kanban 用自己版本的模式（见设计文档 §5.4）。
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
    """执行 hermes 读命令并解析 --json 输出，失败抛 KanbanError。

    hermes-v2026.5.16 v0.14.0 已确认：某些业务错误（如 show 不存在的任务）以退出码 0 +
    空 stdout + stderr 错误文本的形式返回，而非非零退出码。对这两种形式都做统一处理：
    - returncode != 0：用 stderr 分类错误码；
    - returncode == 0 但 stdout 为空或非 JSON：同样用 stderr 分类（此时 stderr 含
      "no such task" 等业务错误文本），避免落到 INTERNAL。
    """
    proc = run_hermes(args)
    if proc.returncode != 0:
        raise KanbanError(classify_hermes_error(proc.stderr),
                          (proc.stderr or "hermes kanban 执行失败").strip()[:1024])
    try:
        return json.loads(proc.stdout)
    except json.JSONDecodeError as e:
        # stdout 非合法 JSON（含空字符串）：若 stderr 有内容，按 stderr 分类业务错误；
        # 否则才归 INTERNAL（真正的内部异常）。
        if proc.stderr and proc.stderr.strip():
            raise KanbanError(classify_hermes_error(proc.stderr),
                              proc.stderr.strip()[:1024])
        raise KanbanError("INTERNAL", f"hermes 输出非合法 JSON: {e}")


def hermes_ok(args: list[str]) -> None:
    """执行 hermes 写命令，仅校验成功，不解析输出。

    注意：本函数仅靠 returncode 判断成败，不防御「exit 0 + 空 stdout + stderr」式
    业务错误（hermes-v2026.5.16 v0.14.0 已确认某些错误如此返回）。当前所有写 verb 在
    hermes_ok 之后都会调用 _show_detail，不存在的 task 会在该 show 阶段经
    hermes_json 捕获为 NOT_FOUND——新增写 verb 时须保持这一「写后必 show」的兜底约定。
    """
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

    字段名以 Dockerfile 写入 /etc/oc-image.json 的实际 key 为准；
    capabilities 的 .get() 候选据此对齐。
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
    t["id"] = raw.get("id") or ""
    t["title"] = raw.get("title") or ""
    t["assignee"] = raw.get("assignee") or ""
    t["status"] = raw.get("status") or "triage"
    return t


def normalize_board(raw: dict) -> dict:
    """把 hermes board 对象规整成契约 Board。"""
    b = {k: raw.get(k) for k in BOARD_FIELDS}
    b["archived"] = bool(raw.get("archived"))
    b["is_current"] = bool(raw.get("is_current"))
    b["counts"] = raw.get("counts") or {}
    b["total"] = raw.get("total") if isinstance(raw.get("total"), int) else 0
    b["slug"] = raw.get("slug") or ""
    b["name"] = raw.get("name") or ""
    return b


def normalize_comment(raw: dict) -> dict:
    """把 hermes 评论对象规整成契约 Comment。"""
    return {
        "author": raw.get("author") or "",
        "body": raw.get("body") or "",
        "created_at": raw.get("created_at") if isinstance(raw.get("created_at"), int) else 0,
    }


def normalize_event(raw: dict) -> dict:
    """把 hermes 事件对象规整成契约 Event（watch 流与 detail.events 共用）。

    watch 流的事件带 task_id；TaskDetail.events 单任务上下文，hermes 原始
    对象可能不含 task_id，取 None 即可（前端只在 watch 流按 task_id 分组）。
    """
    return {
        "task_id": raw.get("task_id"),
        "kind": raw.get("kind") or "",
        "payload": raw.get("payload"),
        "created_at": raw.get("created_at") if isinstance(raw.get("created_at"), int) else 0,
        "run_id": raw.get("run_id"),
    }


# hermes v0.14.0 的 `kanban watch` 无 --json，输出人读文本行，形如：
#   [2026-05-20 15:19] t_0388cefe created (@default) {'status': 'ready', ...}
# 这条正则把它拆成 [时间, task_id, kind, assignee, payload(可选)]。
_WATCH_LINE_RE = re.compile(
    r"^\[([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2})\]\s+"
    r"(\S+)\s+(\S+)\s+\(@([^)]*)\)\s*(\{.*\})?\s*$")


def parse_watch_line(line: str):
    """把 hermes kanban watch 的一行人读文本解析成契约 Event。

    hermes-v2026.5.16 v0.14.0 已确认：kanban watch 子命令无 --json 选项，输出
    人读文本（横幅 "Watching kanban events..." + 事件行如上面正则所示）。
    无法解析的行返回 None，由调用方跳过。

    payload 是 Python dict repr（单引号 / None），用 ast.literal_eval
    安全解析；失败时退化为空 dict，不让单行解析异常拖垮整个流。

    created_at 用当前 Unix 秒近似：watch 是实时流、事件刚发生；hermes
    文本时间仅精确到分钟、时区不确定，用当前时间既满足契约 integer 又
    避免时区偏差。
    """
    m = _WATCH_LINE_RE.match(line.strip())
    if not m:
        return None
    task_id, kind, assignee, payload_str = m.group(2), m.group(3), m.group(4), m.group(5)
    payload = {}
    if payload_str:
        try:
            parsed = ast.literal_eval(payload_str)
            if isinstance(parsed, dict):
                payload = parsed
        except (ValueError, SyntaxError):
            payload = {}
    # 契约 Event 没有独立 assignee 字段，把它并入 payload 以保留信息。
    if assignee:
        payload.setdefault("assignee", assignee)
    return {
        "task_id": task_id,
        "kind": kind,
        "payload": payload,
        "created_at": int(time.time()),
        "run_id": None,
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
# verb 核心实现：run_<verb>(...) 返回 data 或抛 KanbanError
# ———————————————————————————————————————————————

def run_capabilities() -> dict:
    """自描述能力：契约版本、支持的 verb、feature 开关。不调用 hermes 写/读命令。

    verbs/features 仍按是否存在真实 hermes 自描述（这是能力发现语义，不是
    UNSUPPORTED 分发守卫；调用方对其它 verb 的 UNSUPPORTED 守卫另行处理）。
    等价原 verb_capabilities 的 data。
    """
    info = read_image_info()
    real = has_real_hermes()
    return {
        "contract_version": CONTRACT_VERSION,
        "oc_kanban_version": OC_KANBAN_VERSION,
        "hermes_version": info.get("hermes_ref") or info.get("hermes_version"),
        "variant": info.get("variant") or info.get("oc_image_variant") or "hermes-v2026.5.16",
        "verbs": FUNCTIONAL_VERBS if real else [],
        "features": {"write": real, "watch": real, "runs": real, "stats": real},
    }


def run_boards() -> list:
    """列出所有 board。等价原 verb_boards 的 data。"""
    raw = hermes_json(["boards", "list", "--all", "--json"])
    return [normalize_board(b) for b in (raw or [])]


def run_list(board: str, status: str | None = None, assignee: str | None = None) -> list:
    """列出某 board 的任务（可按 status / assignee 过滤）。等价原 verb_list 的 data。"""
    cmd = ["--board", board, "list", "--json"]
    if status:
        cmd += ["--status", status]
    if assignee:
        cmd += ["--assignee", assignee]
    raw = hermes_json(cmd)
    return [normalize_task(t) for t in (raw or [])]


def run_show(board: str, id: str) -> dict:
    """查询单个任务详情。等价原 verb_show 的 data。"""
    return _show_detail(board, id)


def run_runs(board: str, id: str) -> list:
    """查询任务历次执行记录。等价原 verb_runs 的 data。"""
    raw = hermes_json(["--board", board, "runs", id, "--json"])
    return [normalize_run(r) for r in (raw or [])]


def run_stats(board: str) -> dict:
    """查询某 board 的统计。等价原 verb_stats 的 data。"""
    return normalize_stats(hermes_json(["--board", board, "stats", "--json"]))


def run_create(board: str, title: str, assignee: str, priority: int = 0,
               body: str | None = None, skills=(), workspace: str | None = None,
               parent: str | None = None, max_retries: int | None = None) -> dict:
    """创建任务后再 show 一次，返回完整 TaskDetail。等价原 verb_create 的 data。"""
    cmd = ["--board", board, "create", title,
           "--assignee", assignee, "--priority", str(priority), "--json"]
    if body:
        cmd += ["--body", body]
    for sk in skills:
        cmd += ["--skill", sk]
    if workspace:
        cmd += ["--workspace", workspace]
    if parent:
        cmd += ["--parent", parent]
    if max_retries is not None:
        cmd += ["--max-retries", str(max_retries)]
    created = hermes_json(cmd)
    task_id = created.get("id") if isinstance(created, dict) else None
    if not task_id:
        raise KanbanError("INTERNAL", "hermes create 未返回 task id")
    return _show_detail(board, task_id)


def run_comment(board: str, id: str, body: str) -> dict:
    """给任务加评论，返回更新后的 TaskDetail。等价原 verb_comment 的 data。"""
    hermes_ok(["--board", board, "comment", id, body])
    return _show_detail(board, id)


def run_complete(board: str, id: str, result: str | None = None) -> dict:
    """标记任务完成，返回更新后的 TaskDetail。等价原 verb_complete 的 data。"""
    cmd = ["--board", board, "complete", id]
    if result:
        cmd += ["--result", result]
    hermes_ok(cmd)
    return _show_detail(board, id)


def run_block(board: str, id: str, reason: str) -> dict:
    """阻塞任务，返回更新后的 TaskDetail。等价原 verb_block 的 data。"""
    hermes_ok(["--board", board, "block", id, reason])
    return _show_detail(board, id)


def run_unblock(board: str, id: str) -> dict:
    """解除阻塞，返回更新后的 TaskDetail。等价原 verb_unblock 的 data。"""
    hermes_ok(["--board", board, "unblock", id])
    return _show_detail(board, id)


def run_archive(board: str, id: str) -> dict:
    """归档任务，返回更新后的 TaskDetail。等价原 verb_archive 的 data。"""
    hermes_ok(["--board", board, "archive", id])
    return _show_detail(board, id)


def run_reassign(board: str, id: str, to: str) -> dict:
    """重新分配任务，返回更新后的 TaskDetail。等价原 verb_reassign 的 data。"""
    hermes_ok(["--board", board, "reassign", id, to])
    return _show_detail(board, id)


def run_reclaim(board: str, id: str) -> dict:
    """撤销任务认领，返回更新后的 TaskDetail。等价原 verb_reclaim 的 data。"""
    hermes_ok(["--board", board, "reclaim", id])
    return _show_detail(board, id)


def watch_events(board: str) -> Iterator[dict]:
    """订阅 board 事件流：逐条 yield 契约 Event dict（取代原 verb_watch 的 NDJSON 写）。

    把 hermes watch 的人读文本逐行经 parse_watch_line 解析成契约 Event 后 yield；
    横幅行、无法解析的行直接跳过，不污染契约事件流。

    启动即失败（子进程非零退出）且整个流未产出任何事件时抛 KanbanError，
    让调用方（server SSE 在建流前 / shim）能映射成错误状态码或失败信封；
    若已产出过事件，则把异常退出当作流正常结束（不再抛），与原 verb_watch
    「emitted>0 时按 returncode 决定退出码、不补错误信封」的行为一致。

    资源约束：finally 中 terminate 仍存活的子进程，避免上游提前停止消费时
    遗留僵尸 hermes 进程；子进程退出后再读 stderr，避免 pipe 未读死锁。
    """
    proc = subprocess.Popen(
        ["hermes", "kanban", "--board", board, "watch"],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    emitted = 0
    stderr_text = ""
    try:
        for line in proc.stdout:
            ev = parse_watch_line(line)
            if ev is None:
                continue
            emitted += 1
            yield ev
        proc.wait()
        # 子进程退出后再读 stderr：此时 pipe 内容有限，不会因未读而死锁。
        stderr_text = proc.stderr.read() or ""
    finally:
        if proc.poll() is None:
            proc.terminate()
    # 一条事件都没产出且进程异常退出——视为启动失败，抛出供调用方处理。
    if proc.returncode not in (0, None) and emitted == 0:
        raise KanbanError(classify_hermes_error(stderr_text),
                          (stderr_text or "hermes kanban watch 启动失败").strip()[:1024])
