# tests/test_ocops_server_kanban.py
"""覆盖 server kanban 非流式端点：capabilities/boards/tasks(CRUD/comment/complete/
block/unblock/archive/reassign/reclaim)/runs/stats 的 HTTP handler 契约验证。

涉及 hermes CLI 调用的 handler 通过 monkeypatch ocops.kanban.run_hermes /
hermes_json / has_real_hermes 打桩，避免真调 hermes 进程。
每条用例相邻中文注释说明覆盖场景。
"""
from __future__ import annotations

import json
import subprocess
from pathlib import Path

import pytest
from starlette.testclient import TestClient

from ocops.kanban import KanbanError


# ---------------------------------------------------------------------------
# 公共辅助
# ---------------------------------------------------------------------------

def _client(monkeypatch, tmp_path: Path) -> TestClient:
    """构造带固定 token 的测试 client。

    OC_OPS_TOKEN 固定为 t0ken，OC_INFO_FILE 指向 tmp_path 下假文件，
    避免依赖 /etc/oc-image.json。
    """
    monkeypatch.setenv("OC_OPS_TOKEN", "t0ken")
    info_file = tmp_path / "oc-image.json"
    info_file.write_text(json.dumps({
        "variant": "hermes-v0.18.2",
        "hermes_upstream_ref": "abc",
        "built_at": "2026-05-29",
    }))
    monkeypatch.setenv("OC_INFO_FILE", str(info_file))

    from ocops.server import app
    return TestClient(app)


def _auth() -> dict:
    """返回带正确 Bearer token 的请求头，供所有 kanban 端点测试使用。"""
    return {"Authorization": "Bearer t0ken"}


def _stub_real_hermes(monkeypatch) -> None:
    """把 has_real_hermes 打桩为 True，使守卫通过，允许执行真正的 handler 逻辑。"""
    import ocops.kanban as kanban_mod
    monkeypatch.setattr(kanban_mod, "has_real_hermes", lambda: True)


def _stub_no_hermes(monkeypatch) -> None:
    """把 has_real_hermes 打桩为 False，模拟 stub 镜像（无真实 hermes）。"""
    import ocops.kanban as kanban_mod
    monkeypatch.setattr(kanban_mod, "has_real_hermes", lambda: False)


def _make_task_detail(task_id: str = "t_abc", board: str = "default") -> dict:
    """构造最简 TaskDetail，供 hermes_json 打桩返回使用。"""
    return {
        "task": {
            "id": task_id,
            "title": "测试任务",
            "body": None,
            "assignee": "agent1",
            "status": "ready",
            "priority": 0,
            "tenant": None,
            "workspace_kind": None,
            "workspace_path": None,
            "created_by": None,
            "created_at": 1700000000,
            "started_at": None,
            "completed_at": None,
            "result": None,
            "skills": [],
            "max_retries": None,
        },
        "latest_summary": None,
        "parents": [],
        "children": [],
        "comments": [],
        "events": [],
    }


# ---------------------------------------------------------------------------
# GET /oc/kanban/capabilities（不做守卫，任何环境都应返回 200）
# ---------------------------------------------------------------------------

def test_kanban_capabilities_stub_env_200(monkeypatch, tmp_path):
    # capabilities 端点不依赖 hermes 守卫，stub 镜像下也应返回 200 + 契约字段
    _stub_no_hermes(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/kanban/capabilities", headers=_auth())
    # 不管有无真实 hermes，capabilities 端点始终返回 200
    assert r.status_code == 200
    body = r.json()
    # 必须包含契约核心字段
    assert "contract_version" in body
    assert "verbs" in body
    assert "features" in body


def test_kanban_capabilities_real_hermes_200(monkeypatch, tmp_path):
    # capabilities 端点在真实 hermes 环境（打桩为 True）下返回 200 + verbs 列表非空
    _stub_real_hermes(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/kanban/capabilities", headers=_auth())
    assert r.status_code == 200
    body = r.json()
    # 有真实 hermes 时，verbs 应包含功能动词
    assert "boards" in body["verbs"]
    assert body["features"]["write"] is True


# ---------------------------------------------------------------------------
# GET /oc/kanban/boards（守卫保护，stub→ 409 UNSUPPORTED）
# ---------------------------------------------------------------------------

def test_kanban_boards_stub_409(monkeypatch, tmp_path):
    # stub 镜像下（has_real_hermes=False），boards 端点被守卫拦截，返回 409 UNSUPPORTED
    _stub_no_hermes(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/kanban/boards", headers=_auth())
    # 守卫抛 UNSUPPORTED → code_to_http → 409
    assert r.status_code == 409
    assert r.json()["code"] == "UNSUPPORTED"


def test_kanban_boards_real_200(monkeypatch, tmp_path):
    # 真实 hermes 环境（has_real_hermes=True），boards 端点正常返回 200 + 列表
    import ocops.kanban as kanban_mod
    _stub_real_hermes(monkeypatch)
    # hermes_json 打桩：返回空 board 列表（格式合法）
    monkeypatch.setattr(kanban_mod, "hermes_json", lambda args: [])
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/kanban/boards", headers=_auth())
    assert r.status_code == 200
    assert isinstance(r.json(), list)


# ---------------------------------------------------------------------------
# GET /oc/kanban/tasks（list，守卫保护）
# ---------------------------------------------------------------------------

def test_kanban_list_stub_409(monkeypatch, tmp_path):
    # stub 镜像下，list 端点被守卫拦截，返回 409
    _stub_no_hermes(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/kanban/tasks", headers=_auth())
    assert r.status_code == 409


def test_kanban_list_real_200(monkeypatch, tmp_path):
    # 真实环境下，list 端点返回 200 + 空任务列表（hermes_json 打桩）
    import ocops.kanban as kanban_mod
    _stub_real_hermes(monkeypatch)
    monkeypatch.setattr(kanban_mod, "hermes_json", lambda args: [])
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/kanban/tasks?board=default&status=ready", headers=_auth())
    assert r.status_code == 200
    assert isinstance(r.json(), list)


# ---------------------------------------------------------------------------
# GET /oc/kanban/tasks/{id}（show，守卫保护）
# ---------------------------------------------------------------------------

def test_kanban_show_stub_409(monkeypatch, tmp_path):
    # stub 镜像下，show 端点被守卫拦截，返回 409
    _stub_no_hermes(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/kanban/tasks/t_abc", headers=_auth())
    assert r.status_code == 409


def test_kanban_show_ok_200(monkeypatch, tmp_path):
    # 真实环境下，show 已存在的任务，hermes_json 打桩返回 TaskDetail，handler 返回 200
    import ocops.kanban as kanban_mod
    _stub_real_hermes(monkeypatch)
    fake_detail = _make_task_detail("t_abc")
    monkeypatch.setattr(kanban_mod, "hermes_json", lambda args: fake_detail)
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/kanban/tasks/t_abc?board=default", headers=_auth())
    assert r.status_code == 200
    assert r.json()["task"]["id"] == "t_abc"


def test_kanban_show_not_found_404(monkeypatch, tmp_path):
    # show 不存在的任务：hermes_json 打桩抛 KanbanError(NOT_FOUND) → 404
    import ocops.kanban as kanban_mod
    _stub_real_hermes(monkeypatch)

    def fake_hermes_json(args):
        raise KanbanError("NOT_FOUND", "no such task: ghost")

    monkeypatch.setattr(kanban_mod, "hermes_json", fake_hermes_json)
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/kanban/tasks/ghost?board=default", headers=_auth())
    # NOT_FOUND → code_to_http → 404
    assert r.status_code == 404
    assert r.json()["code"] == "NOT_FOUND"


# ---------------------------------------------------------------------------
# GET /oc/kanban/tasks/{id}/runs（守卫保护）
# ---------------------------------------------------------------------------

def test_kanban_runs_stub_409(monkeypatch, tmp_path):
    # stub 镜像下，runs 端点被守卫拦截，返回 409
    _stub_no_hermes(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/kanban/tasks/t_abc/runs", headers=_auth())
    assert r.status_code == 409


def test_kanban_runs_real_200(monkeypatch, tmp_path):
    # 真实环境下，runs 端点返回 200 + 执行记录列表（hermes_json 打桩）
    import ocops.kanban as kanban_mod
    _stub_real_hermes(monkeypatch)
    monkeypatch.setattr(kanban_mod, "hermes_json", lambda args: [])
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/kanban/tasks/t_abc/runs?board=default", headers=_auth())
    assert r.status_code == 200
    assert isinstance(r.json(), list)


# ---------------------------------------------------------------------------
# GET /oc/kanban/stats（守卫保护）
# ---------------------------------------------------------------------------

def test_kanban_stats_stub_409(monkeypatch, tmp_path):
    # stub 镜像下，stats 端点被守卫拦截，返回 409
    _stub_no_hermes(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/kanban/stats", headers=_auth())
    assert r.status_code == 409


def test_kanban_stats_real_200(monkeypatch, tmp_path):
    # 真实环境下，stats 端点返回 200 + 统计对象（hermes_json 打桩）
    import ocops.kanban as kanban_mod
    _stub_real_hermes(monkeypatch)
    fake_stats = {"by_status": {}, "by_assignee": {}, "oldest_ready_age_seconds": 0, "now": 0}
    monkeypatch.setattr(kanban_mod, "hermes_json", lambda args: fake_stats)
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/kanban/stats?board=default", headers=_auth())
    assert r.status_code == 200
    body = r.json()
    assert "by_status" in body


# ---------------------------------------------------------------------------
# POST /oc/kanban/tasks（create，守卫保护）
# ---------------------------------------------------------------------------

def test_kanban_create_stub_409(monkeypatch, tmp_path):
    # stub 镜像下，create 端点被守卫拦截，返回 409
    _stub_no_hermes(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/kanban/tasks", json={"board": "default", "title": "t1", "assignee": "a1"}, headers=_auth())
    assert r.status_code == 409


def test_kanban_create_real_200(monkeypatch, tmp_path):
    # 真实环境下，create 端点打桩 hermes_json 返回假 TaskDetail，返回 200
    import ocops.kanban as kanban_mod
    _stub_real_hermes(monkeypatch)
    fake_detail = _make_task_detail("t_new")
    # run_create 先调 hermes_json 创建（返回 {id}），再调 _show_detail（也调 hermes_json）
    # 打桩两次返回不同值：第一次返回 {id:t_new}，后续调用返回 TaskDetail
    call_count = [0]

    def fake_hermes_json(args):
        call_count[0] += 1
        if call_count[0] == 1:
            # 第一次调用：模拟 create 返回含 id 的任务对象
            return {"id": "t_new", "title": "测试任务", "assignee": "agent1", "status": "ready"}
        # 后续调用：模拟 show 返回 TaskDetail
        return fake_detail

    monkeypatch.setattr(kanban_mod, "hermes_json", fake_hermes_json)
    c = _client(monkeypatch, tmp_path)
    r = c.post(
        "/oc/kanban/tasks",
        json={"board": "default", "title": "测试任务", "assignee": "agent1"},
        headers=_auth(),
    )
    assert r.status_code == 200
    body = r.json()
    # create 返回完整 TaskDetail，包含 task 字段
    assert body["task"]["id"] == "t_new"


# ---------------------------------------------------------------------------
# POST /oc/kanban/tasks/{id}/comment（守卫保护）
# ---------------------------------------------------------------------------

def test_kanban_comment_stub_409(monkeypatch, tmp_path):
    # stub 镜像下，comment 端点被守卫拦截，返回 409
    _stub_no_hermes(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/kanban/tasks/t_abc/comment", json={"body": "hello"}, headers=_auth())
    assert r.status_code == 409


def test_kanban_comment_real_200(monkeypatch, tmp_path):
    # 真实环境下，comment 端点打桩 run_hermes + hermes_json，返回 200 + TaskDetail
    import ocops.kanban as kanban_mod
    _stub_real_hermes(monkeypatch)
    fake_detail = _make_task_detail("t_abc")
    # run_comment 先调 hermes_ok（run_hermes），再调 hermes_json（_show_detail）
    monkeypatch.setattr(kanban_mod, "run_hermes", lambda args, timeout=30: subprocess.CompletedProcess(
        args=args, returncode=0, stdout="", stderr=""))
    monkeypatch.setattr(kanban_mod, "hermes_json", lambda args: fake_detail)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/kanban/tasks/t_abc/comment", json={"board": "default", "body": "LGTM"}, headers=_auth())
    assert r.status_code == 200
    assert r.json()["task"]["id"] == "t_abc"


# ---------------------------------------------------------------------------
# POST /oc/kanban/tasks/{id}/complete（守卫保护）
# ---------------------------------------------------------------------------

def test_kanban_complete_stub_409(monkeypatch, tmp_path):
    # stub 镜像下，complete 端点被守卫拦截，返回 409
    _stub_no_hermes(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/kanban/tasks/t_abc/complete", json={}, headers=_auth())
    assert r.status_code == 409


def test_kanban_complete_real_200(monkeypatch, tmp_path):
    # 真实环境下，complete 端点打桩 run_hermes + hermes_json，返回 200 + TaskDetail
    import ocops.kanban as kanban_mod
    _stub_real_hermes(monkeypatch)
    fake_detail = _make_task_detail("t_abc")
    monkeypatch.setattr(kanban_mod, "run_hermes", lambda args, timeout=30: subprocess.CompletedProcess(
        args=args, returncode=0, stdout="", stderr=""))
    monkeypatch.setattr(kanban_mod, "hermes_json", lambda args: fake_detail)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/kanban/tasks/t_abc/complete", json={"board": "default", "result": "done"}, headers=_auth())
    assert r.status_code == 200
    assert r.json()["task"]["id"] == "t_abc"


# ---------------------------------------------------------------------------
# POST /oc/kanban/tasks/{id}/block（守卫保护）
# ---------------------------------------------------------------------------

def test_kanban_block_stub_409(monkeypatch, tmp_path):
    # stub 镜像下，block 端点被守卫拦截，返回 409
    _stub_no_hermes(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/kanban/tasks/t_abc/block", json={"reason": "waiting"}, headers=_auth())
    assert r.status_code == 409


def test_kanban_block_real_200(monkeypatch, tmp_path):
    # 真实环境下，block 端点打桩后返回 200 + TaskDetail
    import ocops.kanban as kanban_mod
    _stub_real_hermes(monkeypatch)
    fake_detail = _make_task_detail("t_abc")
    monkeypatch.setattr(kanban_mod, "run_hermes", lambda args, timeout=30: subprocess.CompletedProcess(
        args=args, returncode=0, stdout="", stderr=""))
    monkeypatch.setattr(kanban_mod, "hermes_json", lambda args: fake_detail)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/kanban/tasks/t_abc/block", json={"board": "default", "reason": "waiting dep"}, headers=_auth())
    assert r.status_code == 200
    assert r.json()["task"]["id"] == "t_abc"


# ---------------------------------------------------------------------------
# POST /oc/kanban/tasks/{id}/unblock（守卫保护）
# ---------------------------------------------------------------------------

def test_kanban_unblock_stub_409(monkeypatch, tmp_path):
    # stub 镜像下，unblock 端点被守卫拦截，返回 409
    _stub_no_hermes(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/kanban/tasks/t_abc/unblock", json={}, headers=_auth())
    assert r.status_code == 409


def test_kanban_unblock_real_200(monkeypatch, tmp_path):
    # 真实环境下，unblock 端点打桩后返回 200 + TaskDetail
    import ocops.kanban as kanban_mod
    _stub_real_hermes(monkeypatch)
    fake_detail = _make_task_detail("t_abc")
    monkeypatch.setattr(kanban_mod, "run_hermes", lambda args, timeout=30: subprocess.CompletedProcess(
        args=args, returncode=0, stdout="", stderr=""))
    monkeypatch.setattr(kanban_mod, "hermes_json", lambda args: fake_detail)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/kanban/tasks/t_abc/unblock", json={"board": "default"}, headers=_auth())
    assert r.status_code == 200


# ---------------------------------------------------------------------------
# POST /oc/kanban/tasks/{id}/archive（守卫保护）
# ---------------------------------------------------------------------------

def test_kanban_archive_stub_409(monkeypatch, tmp_path):
    # stub 镜像下，archive 端点被守卫拦截，返回 409
    _stub_no_hermes(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/kanban/tasks/t_abc/archive", json={}, headers=_auth())
    assert r.status_code == 409


def test_kanban_archive_real_200(monkeypatch, tmp_path):
    # 真实环境下，archive 端点打桩后返回 200 + TaskDetail
    import ocops.kanban as kanban_mod
    _stub_real_hermes(monkeypatch)
    fake_detail = _make_task_detail("t_abc")
    monkeypatch.setattr(kanban_mod, "run_hermes", lambda args, timeout=30: subprocess.CompletedProcess(
        args=args, returncode=0, stdout="", stderr=""))
    monkeypatch.setattr(kanban_mod, "hermes_json", lambda args: fake_detail)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/kanban/tasks/t_abc/archive", json={"board": "default"}, headers=_auth())
    assert r.status_code == 200


# ---------------------------------------------------------------------------
# POST /oc/kanban/tasks/{id}/reassign（守卫保护）
# ---------------------------------------------------------------------------

def test_kanban_reassign_stub_409(monkeypatch, tmp_path):
    # stub 镜像下，reassign 端点被守卫拦截，返回 409
    _stub_no_hermes(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/kanban/tasks/t_abc/reassign", json={"to": "agent2"}, headers=_auth())
    assert r.status_code == 409


def test_kanban_reassign_real_200(monkeypatch, tmp_path):
    # 真实环境下，reassign 端点打桩后返回 200 + TaskDetail
    import ocops.kanban as kanban_mod
    _stub_real_hermes(monkeypatch)
    fake_detail = _make_task_detail("t_abc")
    monkeypatch.setattr(kanban_mod, "run_hermes", lambda args, timeout=30: subprocess.CompletedProcess(
        args=args, returncode=0, stdout="", stderr=""))
    monkeypatch.setattr(kanban_mod, "hermes_json", lambda args: fake_detail)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/kanban/tasks/t_abc/reassign", json={"board": "default", "to": "agent2"}, headers=_auth())
    assert r.status_code == 200


# ---------------------------------------------------------------------------
# POST /oc/kanban/tasks/{id}/reclaim（守卫保护）
# ---------------------------------------------------------------------------

def test_kanban_reclaim_stub_409(monkeypatch, tmp_path):
    # stub 镜像下，reclaim 端点被守卫拦截，返回 409
    _stub_no_hermes(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/kanban/tasks/t_abc/reclaim", json={}, headers=_auth())
    assert r.status_code == 409


def test_kanban_reclaim_real_200(monkeypatch, tmp_path):
    # 真实环境下，reclaim 端点打桩后返回 200 + TaskDetail
    import ocops.kanban as kanban_mod
    _stub_real_hermes(monkeypatch)
    fake_detail = _make_task_detail("t_abc")
    monkeypatch.setattr(kanban_mod, "run_hermes", lambda args, timeout=30: subprocess.CompletedProcess(
        args=args, returncode=0, stdout="", stderr=""))
    monkeypatch.setattr(kanban_mod, "hermes_json", lambda args: fake_detail)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/kanban/tasks/t_abc/reclaim", json={"board": "default"}, headers=_auth())
    assert r.status_code == 200


# ---------------------------------------------------------------------------
# 鉴权检查（kanban 端点同样需要 Bearer token）
# ---------------------------------------------------------------------------

def test_kanban_endpoint_requires_auth(monkeypatch, tmp_path):
    # 不带 Authorization 头访问 kanban capabilities 端点 → 401 UNAUTHORIZED
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/kanban/capabilities")
    assert r.status_code == 401
    assert r.json()["code"] == "UNAUTHORIZED"
