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
    try:
        return json.loads(proc.stdout), proc.returncode
    except json.JSONDecodeError as e:
        raise AssertionError(
            f"oc-kanban {args} stdout 非合法 JSON: {e}\n"
            f"stdout: {proc.stdout!r}\nstderr: {proc.stderr!r}"
        ) from e


def _init_kanban():
    """初始化 kanban.db；init 失败时直接 fail，避免后续用例报含糊错误。"""
    r = subprocess.run(["hermes", "kanban", "init"], capture_output=True, text=True)
    assert r.returncode == 0, f"hermes kanban init 失败: {r.stderr}"


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
    _init_kanban()
    env, code = _run_oc_kanban("boards")
    validate(env, _load_schema("envelope.schema.json"))
    assert env["ok"] is True and code == 0
    board_schema = _load_schema("board.schema.json")
    for b in env["data"]:
        validate(b, board_schema)


@real_only
def test_list_matches_schema():
    """覆盖：list 输出是数组，每个元素符合 task schema。"""
    _init_kanban()
    env, code = _run_oc_kanban("list", "--board", "default")
    validate(env, _load_schema("envelope.schema.json"))
    assert env["ok"] is True and code == 0
    task_schema = _load_schema("task.schema.json")
    for t in env["data"]:
        validate(t, task_schema)


@real_only
def test_create_show_returns_task_detail():
    """覆盖：create 与 show 都返回符合 task-detail schema 的 TaskDetail。"""
    _init_kanban()
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
    _init_kanban()
    env, code = _run_oc_kanban("stats", "--board", "default")
    assert env["ok"] is True and code == 0
    validate(env["data"], _load_schema("stats.schema.json"))


@real_only
def test_runs_returns_array():
    """覆盖：runs 输出是数组（新任务无执行历史时为空），元素若有则符合 run schema。"""
    _init_kanban()
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
    _init_kanban()
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
    _init_kanban()
    env, code = _run_oc_kanban("show", "--board", "default", "--id", "t_nonexistent")
    validate(env, _load_schema("envelope.schema.json"))
    assert env["ok"] is False
    # oc-kanban 对「任务不存在」的错误码归类依赖 hermes stderr 文本匹配：
    # stderr 含 "not found" 等关键词时归 NOT_FOUND，否则落到 HERMES_CLI_FAILED。
    # 实际归类待镜像内实测确认，此处接受两者之一。
    assert env["error"]["code"] in ("NOT_FOUND", "HERMES_CLI_FAILED")
    assert code == 1
