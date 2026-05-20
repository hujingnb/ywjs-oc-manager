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


@pytest.fixture(autouse=True)
def isolated_hermes_home(tmp_path, monkeypatch):
    """每个测试用独立的 HERMES_HOME，kanban.db 落到临时目录，避免测试间状态串扰。"""
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))


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
    """stats --json 输出含 manager 工具栏依赖的 by_status 字段（hermes v0.14.0 真实契约）。"""
    _run_kanban("init")
    out = _run_kanban("stats", "--json")
    stats = json.loads(out)
    # stats 输出必须含 by_status 字段（而非旧的 status_counts），已按真实 CLI 输出校准。
    assert "by_status" in stats, f"stats 输出应含 by_status: {stats!r}"


def test_kanban_create_show_roundtrip():
    """create 后 show --json 能取回任务，show 输出的任务字段在 task 子对象内。

    hermes v0.14.0 真实契约：
    - create --json 返回扁平任务对象（与 list 元素相同格式）。
    - show --json 返回 {task: {...}, comments: [...], events: [...]} 结构，
      任务核心字段在顶层 task 子对象内，不平铺。
    manager hermes_kanban_types.go 的 KanbanTaskDetail.Task 依赖此结构。
    """
    _run_kanban("init")
    create_out = _run_kanban(
        "create", "contract-test 任务", "--assignee", "default", "--json"
    )
    task = json.loads(create_out)
    task_id = task["id"]
    assert task_id

    show_out = _run_kanban("show", task_id, "--json")
    detail = json.loads(show_out)
    # show 输出必须含顶层 task 子对象（而非直接平铺）
    assert "task" in detail, "kanban show 输出应含 task 字段"
    task_obj = detail["task"]
    # manager KanbanTask 依赖以下字段名
    for field in ("id", "title", "status", "assignee", "created_at"):
        assert field in task_obj, f"kanban show task 对象缺字段 {field}"
