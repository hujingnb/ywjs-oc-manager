#!/usr/bin/env python3
"""oc-kanban —— hermes kanban CLI 稳定适配层的 CLI 薄 shim。

命名：oc- 前缀取自项目名 oc-manager，标识注入 hermes runtime 镜像、供容器内调用的运维 CLI
（区别于 hermes 上游自带命令）；后缀 kanban = 看板 CLI 稳定适配层。

核心逻辑（hermes 调用 / normalize / parse_watch_line / 全部 verb）已下沉至
ocops.kanban，本文件仅负责 argparse 输入契约、UNSUPPORTED 守卫、verb→run_*
分发，以及对外输出协议：
- 非 watch verb：stdout 单行 JSON 信封 {"ok":true,"data":...} 或
  {"ok":false,"error":{"code","message"}}。
- watch verb：消费 ocops.kanban.watch_events generator，逐行 NDJSON 写 stdout。
退出码：0=成功；1=业务错误（错误信封已在 stdout）；2=用法错误（argparse）。
行为与下沉前等价。
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

sys.path.insert(0, "/usr/local/lib")  # 镜像内 ocops 装在 /usr/local/lib/ocops
sys.path.insert(0, str(Path(__file__).resolve().parent))  # 本地自检 fallback

from ocops import kanban as _kanban
from ocops.kanban import KanbanError
from ocops.errors import OpsError


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


def _dispatch(args) -> object:
    """把解析后的 args.verb 映射到 ocops.kanban.run_*，返回 data。

    capabilities 在 main 里先于守卫单独处理（恒定可用）；本函数只处理
    需真实 hermes 的功能 verb（watch 由 main 单独走流式分支）。
    """
    verb = args.verb
    if verb == "boards":
        return _kanban.run_boards()
    if verb == "list":
        return _kanban.run_list(args.board, status=args.status, assignee=args.assignee)
    if verb == "show":
        return _kanban.run_show(args.board, args.id)
    if verb == "runs":
        return _kanban.run_runs(args.board, args.id)
    if verb == "stats":
        return _kanban.run_stats(args.board)
    if verb == "create":
        return _kanban.run_create(
            args.board, args.title, args.assignee, priority=args.priority,
            body=args.body, skills=args.skill, workspace=args.workspace,
            parent=args.parent, max_retries=args.max_retries,
        )
    if verb == "comment":
        return _kanban.run_comment(args.board, args.id, args.body)
    if verb == "complete":
        return _kanban.run_complete(args.board, args.id, result=args.result)
    if verb == "block":
        return _kanban.run_block(args.board, args.id, args.reason)
    if verb == "unblock":
        return _kanban.run_unblock(args.board, args.id)
    if verb == "archive":
        return _kanban.run_archive(args.board, args.id)
    if verb == "reassign":
        return _kanban.run_reassign(args.board, args.id, args.to)
    if verb == "reclaim":
        return _kanban.run_reclaim(args.board, args.id)
    raise KanbanError("BAD_REQUEST", f"未知 verb: {verb}")


def _stream_watch(board: str) -> int:
    """消费 watch_events generator，逐行 NDJSON 写 stdout，保留对外流契约。

    上游关闭管道或收到中断时正常终止；启动失败（generator 抛 KanbanError）
    时回退为单行错误信封，退出码 1（与下沉前 verb_watch 行为一致）。
    """
    try:
        for ev in _kanban.watch_events(board):
            sys.stdout.write(json.dumps(ev, ensure_ascii=False) + "\n")
            sys.stdout.flush()
        return 0
    except (KeyboardInterrupt, BrokenPipeError):
        return 0
    except KanbanError as e:
        return emit_err(e.code, e.message)


def main(argv=None) -> int:
    """入口：解析参数 → 守卫 → 分发 verb → 统一异常兜底成失败信封。"""
    args = build_parser().parse_args(argv)  # 用法错误时 argparse 自行 exit 2
    try:
        if args.verb == "capabilities":
            return emit_ok(_kanban.run_capabilities())
        # 非 capabilities 的功能 verb 需要真实 hermes；stub 镜像直接返回 UNSUPPORTED。
        # 守卫逻辑从核心模块移交到调用方（shim / server）。
        if not _kanban.has_real_hermes():
            return emit_err("UNSUPPORTED", "该镜像不支持任务看板")
        if args.verb == "watch":
            return _stream_watch(args.board)
        return emit_ok(_dispatch(args))
    except OpsError as e:
        return emit_err(e.code, e.message)
    except Exception as e:  # 任何未预期异常兜底成 INTERNAL，保证永远输出信封
        return emit_err("INTERNAL", f"oc-kanban 内部错误: {e}")


if __name__ == "__main__":
    sys.exit(main())
