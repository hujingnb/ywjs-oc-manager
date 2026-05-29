#!/usr/bin/env python3
"""oc-cron —— Hermes Cron 稳定适配层的 CLI 薄 shim。

命名：oc- 前缀取自项目名 oc-manager，标识注入 hermes runtime 镜像、供容器内调用的运维 CLI
（区别于 hermes 上游自带命令）；后缀 cron = 定时任务（Hermes Cron 稳定适配层）。

核心逻辑（校验/jobs.json 读写/normalize/hermes CLI 调用/11 个 verb）已下沉至
ocops.cron，本文件仅负责 argparse 输入契约、verb→run_* 分发以及 stdout
{ok,data}/{ok:false,error} 信封输出，行为与下沉前等价。
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

sys.path.insert(0, "/usr/local/lib")  # 镜像内 ocops 装在 /usr/local/lib/ocops
sys.path.insert(0, str(Path(__file__).resolve().parent))  # 本地自检 fallback

from ocops import cron as _cron
from ocops.cron import CronError
from ocops.errors import OpsError


class CronArgumentParser(argparse.ArgumentParser):
    """把 argparse 用法错误转成契约错误，避免 stdout/stderr 混入人读文本。"""

    def error(self, message: str) -> None:
        raise CronError("BAD_REQUEST", message)


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
    """构造 CLI parser：verb 与 flag 固定，作为 manager 的稳定输入契约。"""
    parser = CronArgumentParser(prog="oc-cron")
    sub = parser.add_subparsers(dest="verb", required=True, parser_class=CronArgumentParser)

    sub.add_parser("capabilities")
    sub.add_parser("status")

    sp = sub.add_parser("list")
    sp.add_argument("--all", action="store_true")

    for verb in ("show", "history"):
        sp = sub.add_parser(verb)
        sp.add_argument("--id", required=True)

    sp = sub.add_parser("output")
    sp.add_argument("--id", required=True)
    sp.add_argument("--file", required=True)

    sp = sub.add_parser("create")
    sp.add_argument("--name", required=True)
    sp.add_argument("--schedule", required=True)
    sp.add_argument("--prompt")
    sp.add_argument("--deliver")
    sp.add_argument("--repeat", type=int)
    sp.add_argument("--script")
    sp.add_argument("--no-agent", action="store_true")
    sp.add_argument("--workdir")
    sp.add_argument("--skill", action="append", default=[])
    sp.add_argument("--model")
    sp.add_argument("--provider")
    sp.add_argument("--base-url")

    sp = sub.add_parser("update")
    sp.add_argument("--id", required=True)
    sp.add_argument("--name")
    sp.add_argument("--schedule")
    sp.add_argument("--prompt")
    sp.add_argument("--deliver")
    sp.add_argument("--repeat", type=int)
    sp.add_argument("--script")
    sp.add_argument("--no-agent", action="store_true")
    sp.add_argument("--agent", action="store_true")
    sp.add_argument("--workdir")
    sp.add_argument("--skill", action="append", default=[])
    sp.add_argument("--clear-skills", action="store_true")
    sp.add_argument("--model")
    sp.add_argument("--provider")
    sp.add_argument("--base-url")

    sp = sub.add_parser("toggle")
    sp.add_argument("--id", required=True)
    sp.add_argument("--enabled", required=True)

    for verb in ("delete", "run"):
        sp = sub.add_parser(verb)
        sp.add_argument("--id", required=True)

    return parser


def _dispatch(args) -> object:
    """把解析后的 args.verb 映射到 ocops.cron.run_*，返回 data。

    toggle 的 --enabled 字符串校验保留在 CLI 层（argparse 不限制取值），
    非 true/false 一律 BAD_REQUEST，以保持下沉前的对外行为。
    """
    verb = args.verb
    if verb == "capabilities":
        return _cron.run_capabilities()
    if verb == "status":
        return _cron.run_status()
    if verb == "list":
        return _cron.run_list(args.all)
    if verb == "show":
        return _cron.run_show(args.id)
    if verb == "history":
        return _cron.run_history(args.id)
    if verb == "output":
        return _cron.run_output(args.id, args.file)
    if verb == "create":
        return _cron.run_create(
            name=args.name, schedule=args.schedule, prompt=args.prompt,
            deliver=args.deliver, repeat=args.repeat, script=args.script,
            no_agent=args.no_agent, workdir=args.workdir, skills=args.skill,
            model=args.model, provider=args.provider, base_url=args.base_url,
        )
    if verb == "update":
        return _cron.run_update(
            job_id=args.id, name=args.name, schedule=args.schedule, prompt=args.prompt,
            deliver=args.deliver, repeat=args.repeat, script=args.script,
            no_agent=args.no_agent, agent=args.agent, workdir=args.workdir,
            skills=args.skill, clear_skills=args.clear_skills, model=args.model,
            provider=args.provider, base_url=args.base_url,
        )
    if verb == "delete":
        return _cron.run_delete(args.id)
    if verb == "toggle":
        desired = args.enabled.lower()
        if desired not in ("true", "false"):
            raise CronError("BAD_REQUEST", "enabled 必须是 true 或 false")
        return _cron.run_toggle(args.id, desired == "true")
    if verb == "run":
        return _cron.run_run(args.id)
    raise CronError("BAD_REQUEST", f"未知 verb: {verb}")


def main(argv=None) -> int:
    """入口：解析参数、分发 verb，并保证业务异常输出 JSON 信封。"""
    try:
        args = build_parser().parse_args(argv)
        return emit_ok(_dispatch(args))
    except OpsError as exc:
        return emit_err(exc.code, exc.message)
    except Exception as exc:
        return emit_err("INTERNAL", f"oc-cron 内部错误: {exc}")


if __name__ == "__main__":
    sys.exit(main())
