"""ocops.kanban 契约一致性测试。

直接调用核心函数 ocops.kanban.run_*（及 watch_events），断言返回的 data
（dict/list）或抛出的 KanbanError，而非经由 oc-kanban.py CLI 解析 stdout 信封。
hermes 调用通过 monkeypatch ocops.kanban.run_hermes / has_real_hermes 打桩，
避免依赖本机真实 hermes；watch_events 用 tmp_path 下的假 hermes 脚本 + PATH
注入验证逐条 yield 契约 Event。返回数据仍用 ocops-contract/schema/kanban 校验形状。
"""

from __future__ import annotations

import json
import os
import stat
import subprocess
from pathlib import Path

import pytest
from jsonschema import validate

from ocops import kanban
from ocops.kanban import KanbanError

# 契约 schema 既支持镜像内安装路径，也支持源码目录下的 canonical 路径。
IMAGE_SCHEMA_DIR = Path("/usr/local/lib/ocops/contract/schema/kanban")
SOURCE_SCHEMA_DIR = Path(__file__).resolve().parents[2] / "ocops-contract" / "schema" / "kanban"
SCHEMA_DIR = IMAGE_SCHEMA_DIR if IMAGE_SCHEMA_DIR.exists() else SOURCE_SCHEMA_DIR


def _load_schema(name):
    """加载并返回一个契约 schema。"""
    return json.loads((SCHEMA_DIR / name).read_text())


def stub_hermes(monkeypatch, *, stdout="", stderr="", returncode=0):
    """把 ocops.kanban.run_hermes 桩为返回固定 CompletedProcess，记录收到的 args。

    返回一个 list，每次调用把 args 追加进去，便于断言下沉后的 argv 拼装。
    """
    calls: list[list[str]] = []

    def fake_run(args, timeout=kanban.HERMES_TIMEOUT):
        calls.append(list(args))
        return subprocess.CompletedProcess(
            args=["hermes", "kanban", *args], returncode=returncode,
            stdout=stdout, stderr=stderr)

    monkeypatch.setattr(kanban, "run_hermes", fake_run)
    return calls


# 覆盖：capabilities 不依赖 hermes 真实可用——输出须符合 capabilities schema，
# 且无真实 hermes 时 verbs 为空、features 全 False。
def test_run_capabilities_matches_schema(monkeypatch):
    monkeypatch.setattr(kanban, "has_real_hermes", lambda: False)
    monkeypatch.setattr(kanban, "read_image_info", lambda: {})
    data = kanban.run_capabilities()
    validate(data, _load_schema("capabilities.schema.json"))
    assert data["contract_version"] == "1.0"
    assert data["verbs"] == []
    assert data["features"] == {"write": False, "watch": False, "runs": False, "stats": False}


# 覆盖：capabilities 在有真实 hermes 时暴露全部功能 verb 并打开 feature 开关。
def test_run_capabilities_exposes_functional_verbs_when_real(monkeypatch):
    monkeypatch.setattr(kanban, "has_real_hermes", lambda: True)
    monkeypatch.setattr(kanban, "read_image_info", lambda: {"variant": "hermes-v2026.7.1"})
    data = kanban.run_capabilities()
    assert "watch" in data["verbs"] and "create" in data["verbs"]
    assert data["features"]["write"] is True


# 覆盖：boards 把 hermes --json 输出每个元素规整成符合 board schema 的对象。
def test_run_boards_normalizes_to_board_schema(monkeypatch):
    stub_hermes(monkeypatch, stdout=json.dumps([
        {"slug": "default", "name": "默认看板", "total": 3, "counts": {"ready": 1}},
    ]))
    boards = kanban.run_boards()
    board_schema = _load_schema("board.schema.json")
    for b in boards:
        validate(b, board_schema)
    assert boards[0]["slug"] == "default"
    # 缺省布尔字段被兜底成 False。
    assert boards[0]["archived"] is False


# 覆盖：list 是数组，按 status/assignee 过滤参数被拼进 argv，元素符合 task schema。
def test_run_list_filters_and_normalizes(monkeypatch):
    calls = stub_hermes(monkeypatch, stdout=json.dumps([
        {"id": "t_1", "title": "任务一", "assignee": "default", "status": "ready"},
    ]))
    tasks = kanban.run_list("default", status="ready", assignee="alice")
    task_schema = _load_schema("task.schema.json")
    for t in tasks:
        validate(t, task_schema)
    # status/assignee 过滤项必须拼进底层 hermes argv。
    assert calls[0] == ["--board", "default", "list", "--json",
                        "--status", "ready", "--assignee", "alice"]


# 覆盖：show 返回符合 task-detail schema 的 TaskDetail。
def test_run_show_returns_task_detail(monkeypatch):
    stub_hermes(monkeypatch, stdout=json.dumps({
        "task": {"id": "t_1", "title": "详情任务", "assignee": "default", "status": "ready"},
        "comments": [], "events": [],
    }))
    detail = kanban.run_show("default", "t_1")
    validate(detail, _load_schema("task-detail.schema.json"))
    assert detail["task"]["id"] == "t_1"


# 异常路径：show 不存在的任务时 hermes 以 stderr "no such task" 报错 → KanbanError(NOT_FOUND)。
def test_run_show_not_found(monkeypatch):
    stub_hermes(monkeypatch, returncode=1, stderr="error: no such task t_x")
    with pytest.raises(KanbanError) as exc:
        kanban.run_show("default", "t_x")
    assert exc.value.code == "NOT_FOUND"


# 异常路径：hermes 退出 0 但 stdout 为空、stderr 含业务错误文本时，也按 stderr 归类
# （避免误落 INTERNAL）——覆盖 hermes-v2026.7.1 v0.14.0 的 exit0+stderr 行为。
def test_run_show_exit0_with_stderr_classified(monkeypatch):
    stub_hermes(monkeypatch, returncode=0, stdout="", stderr="no such task")
    with pytest.raises(KanbanError) as exc:
        kanban.run_show("default", "t_x")
    assert exc.value.code == "NOT_FOUND"


# 覆盖：runs 返回数组，元素符合 run schema。
def test_run_runs_returns_run_array(monkeypatch):
    stub_hermes(monkeypatch, stdout=json.dumps([
        {"profile": "default", "status": "done", "started_at": 100},
    ]))
    runs = kanban.run_runs("default", "t_1")
    run_schema = _load_schema("run.schema.json")
    for r in runs:
        validate(r, run_schema)
    assert runs[0]["profile"] == "default"


# 覆盖：stats 输出符合 stats schema，缺省字段被兜底。
def test_run_stats_matches_schema(monkeypatch):
    stub_hermes(monkeypatch, stdout=json.dumps({"by_status": {"ready": 2}}))
    data = kanban.run_stats("default")
    validate(data, _load_schema("stats.schema.json"))
    assert data["by_assignee"] == {}


# 覆盖：create 先调 hermes create（返回 task id），再 show 一次返回 TaskDetail。
def test_run_create_returns_task_detail(monkeypatch):
    detail_schema = _load_schema("task-detail.schema.json")
    created_detail = {
        "task": {"id": "t_new", "title": "新任务", "assignee": "default", "status": "ready"},
        "comments": [], "events": [],
    }
    # 第一次调用是 create（返回带 id 的 json），第二次是 show（返回 detail）。
    outputs = [json.dumps({"id": "t_new"}), json.dumps(created_detail)]
    calls: list[list[str]] = []

    def fake_run(args, timeout=kanban.HERMES_TIMEOUT):
        calls.append(list(args))
        return subprocess.CompletedProcess(
            args=["hermes", "kanban", *args], returncode=0,
            stdout=outputs[len(calls) - 1], stderr="")

    monkeypatch.setattr(kanban, "run_hermes", fake_run)
    detail = kanban.run_create("default", "新任务", "default", priority=2, skills=["py"])
    validate(detail, detail_schema)
    assert detail["task"]["id"] == "t_new"
    # create argv 应带 priority 与 skill 透传。
    assert "--priority" in calls[0] and "2" in calls[0]
    assert calls[0][calls[0].index("--skill") + 1] == "py"


# 异常路径：create 时 hermes 未返回 task id → KanbanError(INTERNAL)。
def test_run_create_missing_id_raises_internal(monkeypatch):
    stub_hermes(monkeypatch, stdout=json.dumps({"no_id": True}))
    with pytest.raises(KanbanError) as exc:
        kanban.run_create("default", "新任务", "default")
    assert exc.value.code == "INTERNAL"


# 覆盖：写 verb（以 comment 为例）先写后 show，返回更新后的、符合 schema 的 TaskDetail。
def test_run_comment_writes_then_shows(monkeypatch):
    detail = {
        "task": {"id": "t_1", "title": "任务", "assignee": "default", "status": "ready"},
        "comments": [{"author": "default", "body": "一条评论", "created_at": 1}],
        "events": [],
    }
    outputs = ["", json.dumps(detail)]  # comment 写无输出；随后 show 返回 detail
    calls: list[list[str]] = []

    def fake_run(args, timeout=kanban.HERMES_TIMEOUT):
        calls.append(list(args))
        return subprocess.CompletedProcess(
            args=["hermes", "kanban", *args], returncode=0,
            stdout=outputs[len(calls) - 1], stderr="")

    monkeypatch.setattr(kanban, "run_hermes", fake_run)
    result = kanban.run_comment("default", "t_1", "一条评论")
    validate(result, _load_schema("task-detail.schema.json"))
    # 第一次调用是 comment 写、第二次是 show 读。
    assert calls[0][:4] == ["--board", "default", "comment", "t_1"]
    assert calls[1][:3] == ["--board", "default", "show"]
    assert any(c["body"] == "一条评论" for c in result["comments"])


def _make_fake_hermes(tmp_path: Path, body: str) -> Path:
    """在 tmp_path 下造一个可执行 hermes 脚本，并返回其所在目录（供加入 PATH）。"""
    bindir = tmp_path / "bin"
    bindir.mkdir(parents=True, exist_ok=True)
    script = bindir / "hermes"
    script.write_text(body)
    script.chmod(script.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)
    return bindir


# 覆盖：watch_events 把假 hermes 打印的两行人读事件文本逐条 yield 成契约 Event。
def test_watch_events_yields_contract_events(tmp_path, monkeypatch):
    event_schema = _load_schema("event.schema.json")
    # 假 hermes：打印一行横幅（应被跳过）+ 两行可解析事件文本，然后正常退出 0。
    bindir = _make_fake_hermes(tmp_path, (
        "#!/bin/sh\n"
        "echo 'Watching kanban events...'\n"
        "echo \"[2026-05-20 15:19] t_0388cefe created (@default) {'status': 'ready'}\"\n"
        "echo \"[2026-05-20 15:20] t_0388cefe completed (@default) {'status': 'done'}\"\n"
    ))
    monkeypatch.setenv("PATH", str(bindir) + os.pathsep + os.environ["PATH"])
    events = list(kanban.watch_events("default"))
    assert len(events) == 2
    for ev in events:
        validate(ev, event_schema)
    # 事件按行顺序产出，task_id/kind 与文本一致。
    assert events[0]["task_id"] == "t_0388cefe" and events[0]["kind"] == "created"
    assert events[1]["kind"] == "completed"
    # assignee 被并入 payload（契约 Event 无独立 assignee 字段）。
    assert events[0]["payload"]["assignee"] == "default"


# 异常路径：watch 启动即失败（非零退出且无任何事件）→ KanbanError，错误码按 stderr 归类。
def test_watch_events_startup_failure_raises(tmp_path, monkeypatch):
    # 假 hermes：不打印事件、向 stderr 写 "unknown board" 并以 1 退出。
    bindir = _make_fake_hermes(tmp_path, (
        "#!/bin/sh\n"
        "echo 'unknown board: ghost' 1>&2\n"
        "exit 1\n"
    ))
    monkeypatch.setenv("PATH", str(bindir) + os.pathsep + os.environ["PATH"])
    with pytest.raises(KanbanError) as exc:
        list(kanban.watch_events("ghost"))
    assert exc.value.code == "NOT_FOUND"
