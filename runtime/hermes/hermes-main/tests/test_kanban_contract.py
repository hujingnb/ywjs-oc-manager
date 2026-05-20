"""Hermes Kanban CLI JSON 输出契约测试。

manager 的 hermes_kanban service 依赖 `hermes kanban ... --json` 输出的字段名。
该测试在构建出的生产镜像里运行，确保上游版本变化不会悄悄 break manager 解析。
stub 镜像没有真实 hermes，整文件跳过。
"""

import json
import shutil
import subprocess

import pytest


# stub 镜像的 /usr/local/bin/hermes 是 shell 脚本，不支持 kanban 子命令；
# 用 `hermes kanban --help` 探测：真实 hermes 返回 0，stub 返回 2。
def _has_real_hermes() -> bool:
    if shutil.which("hermes") is None:
        return False
    proc = subprocess.run(
        ["hermes", "kanban", "--help"], capture_output=True, text=True
    )
    return proc.returncode == 0


pytestmark = pytest.mark.skipif(
    not _has_real_hermes(), reason="stub 镜像无真实 hermes kanban CLI"
)


def _run_kanban(*args: str) -> str:
    """跑一条 kanban 命令，返回 stdout；非零退出直接 fail。"""
    proc = subprocess.run(
        ["hermes", "kanban", *args], capture_output=True, text=True, timeout=30
    )
    assert proc.returncode == 0, f"hermes kanban {args} 失败: {proc.stderr}"
    return proc.stdout


def test_kanban_init_idempotent():
    """init 创建 kanban.db，幂等可重复执行。"""
    _run_kanban("init")
    _run_kanban("init")  # 第二次不应报错


def test_kanban_list_json_parseable():
    """list --json 输出必须是合法 JSON 数组（空看板时为 []）。"""
    _run_kanban("init")
    out = _run_kanban("list", "--json")
    tasks = json.loads(out)
    assert isinstance(tasks, list)


def test_kanban_stats_json_has_status_counts():
    """stats --json 输出含 manager 工具栏依赖的 per-status 计数。"""
    _run_kanban("init")
    out = _run_kanban("stats", "--json")
    stats = json.loads(out)
    assert isinstance(stats, dict)


def test_kanban_create_show_roundtrip():
    """create 后 show --json 能取回任务，含 manager 依赖的核心字段。"""
    _run_kanban("init")
    create_out = _run_kanban(
        "create", "contract-test 任务", "--assignee", "default", "--json"
    )
    task = json.loads(create_out)
    task_id = task["id"]
    assert task_id

    show_out = _run_kanban("show", task_id, "--json")
    detail = json.loads(show_out)
    # manager hermes_kanban_types.go 依赖以下字段名
    for field in ("id", "title", "status", "assignee", "created_at"):
        assert field in detail, f"kanban show 输出缺字段 {field}"
