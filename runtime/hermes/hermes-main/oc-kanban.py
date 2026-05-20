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
import ast
import json
import os
import re
import subprocess
import sys
import time
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

    hermes-main v0.14.0 已确认：某些业务错误（如 show 不存在的任务）以退出码 0 +
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
    业务错误（hermes-main v0.14.0 已确认某些错误如此返回）。当前所有写 verb 在
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

    hermes-main v0.14.0 已确认：kanban watch 子命令无 --json 选项，输出
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
    """订阅 board 事件流：解析 hermes watch 的人读文本，规整成契约 Event 后 NDJSON 输出。

    hermes-main v0.14.0 的 kanban watch 子命令无 --json，输出人读文本，
    所以这里用 parse_watch_line 把每行文本解析成契约 Event；
    横幅行、无法解析的行直接跳过，不污染契约 NDJSON 流。
    """
    proc = subprocess.Popen(
        ["hermes", "kanban", "--board", args.board, "watch"],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    emitted = 0
    stderr_text = ""
    try:
        for line in proc.stdout:
            ev = parse_watch_line(line)
            if ev is None:
                continue
            sys.stdout.write(json.dumps(ev, ensure_ascii=False) + "\n")
            sys.stdout.flush()
            emitted += 1
        proc.wait()
        # 子进程退出后再读 stderr：此时 pipe 内容有限，不会因未读而死锁。
        stderr_text = proc.stderr.read() or ""
    except (KeyboardInterrupt, BrokenPipeError):
        # 上游关闭管道或收到中断信号：正常终止，不向 NDJSON 流追加信封。
        return 0
    finally:
        if proc.poll() is None:
            proc.terminate()
    # 一条事件都没输出且进程异常退出——视为启动失败，输出错误信封。
    if proc.returncode not in (0, None) and emitted == 0:
        return emit_err(classify_hermes_error(stderr_text),
                        (stderr_text or "hermes kanban watch 启动失败").strip()[:1024])
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
