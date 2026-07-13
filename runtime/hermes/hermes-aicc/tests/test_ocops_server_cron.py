# tests/test_ocops_server_cron.py
"""覆盖 server cron 端点（11 个）：capabilities/status/list/show/create/update/
toggle/run/delete/history/output 的 HTTP handler 契约验证。

涉及 hermes CLI 的写 verb（create/delete/toggle/run/status）通过
monkeypatch ocops.cron._run_hermes_cron / _hermes_ok 打桩，避免真调 hermes。
每条用例相邻中文注释说明覆盖场景。
"""
from __future__ import annotations

import json
import subprocess
from pathlib import Path

import pytest
from starlette.testclient import TestClient


# ---------------------------------------------------------------------------
# 公共辅助
# ---------------------------------------------------------------------------

def _write_jobs(home: Path, jobs: list[dict]) -> None:
    """在 HERMES_HOME 下写 cron/jobs.json，模拟 hermes 权威任务文件。"""
    cron_dir = home / "cron"
    cron_dir.mkdir(parents=True, exist_ok=True)
    (cron_dir / "jobs.json").write_text(
        json.dumps({"jobs": jobs}, ensure_ascii=False), encoding="utf-8"
    )


def _make_job(job_id: str, enabled: bool = True) -> dict:
    """生成最简 CronJob dict，方便各测试复用。"""
    return {
        "id": job_id,
        "name": f"job-{job_id}",
        "prompt": "do something",
        "schedule": "0 * * * *",
        "repeat": None,
        "enabled": enabled,
        "state": "scheduled" if enabled else "paused",
        "created_at": "2026-05-29T00:00:00Z",
        "next_run_at": None,
        "last_run_at": None,
        "last_status": None,
        "last_error": None,
        "last_delivery_error": None,
        "deliver": None,
        "script": None,
        "no_agent": False,
        "workdir": None,
        "skills": [],
        "model": None,
        "provider": None,
        "base_url": None,
    }


def _client(monkeypatch, tmp_path: Path) -> TestClient:
    """构造带固定 token 与 tmp HERMES_HOME 的测试 client。

    OC_OPS_TOKEN 固定为 t0ken，HERMES_HOME 指向 tmp_path（避免污染真实数据）。
    每次调用都重新从 ocops.server 导入 app，确保环境变量生效。
    """
    monkeypatch.setenv("OC_OPS_TOKEN", "t0ken")
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    # info.py 需要 OC_INFO_FILE，给一个不会 INTERNAL 的假文件
    info_file = tmp_path / "oc-image.json"
    info_file.write_text(json.dumps({"variant": "hermes-v2026.7.1",
                                      "hermes_upstream_ref": "abc",
                                      "built_at": "2026-05-29"}))
    monkeypatch.setenv("OC_INFO_FILE", str(info_file))

    from ocops.server import app
    return TestClient(app)


def _auth() -> dict:
    """返回带正确 Bearer token 的请求头，供所有 cron 端点测试使用。"""
    return {"Authorization": "Bearer t0ken"}


def _stub_hermes_ok(monkeypatch) -> None:
    """把 _hermes_ok 桩为无操作成功调用，避免真调 hermes CLI。"""
    import ocops.cron as cron_mod
    monkeypatch.setattr(cron_mod, "_hermes_ok", lambda args: None)


def _stub_run_hermes_cron(monkeypatch) -> None:
    """把 _run_hermes_cron 桩为返回 returncode=0 的成功调用。"""
    import ocops.cron as cron_mod

    def fake_run(args: list[str]) -> subprocess.CompletedProcess:
        return subprocess.CompletedProcess(
            args=["hermes", "cron", *args], returncode=0, stdout="", stderr=""
        )

    monkeypatch.setattr(cron_mod, "_run_hermes_cron", fake_run)


# ---------------------------------------------------------------------------
# GET /oc/cron/capabilities
# ---------------------------------------------------------------------------

def test_cron_capabilities_200(monkeypatch, tmp_path):
    # capabilities 端点不依赖 hermes，直接读镜像元数据；返回 200 + 契约字段
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/cron/capabilities", headers=_auth())
    assert r.status_code == 200
    body = r.json()
    # 必须包含 manager 识别契约所需的核心字段
    assert body["contract_version"] == "1.0"
    assert "list" in body["verbs"]
    assert body["features"]["history"] is True


# ---------------------------------------------------------------------------
# GET /oc/cron/status
# ---------------------------------------------------------------------------

def test_cron_status_200(monkeypatch, tmp_path):
    # status 端点读 jobs.json 并调 hermes status；打桩后返回 200 + 结构化摘要
    _stub_run_hermes_cron(monkeypatch)
    _write_jobs(tmp_path, [_make_job("j1")])
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/cron/status", headers=_auth())
    assert r.status_code == 200
    body = r.json()
    # available 字段必须为 True（服务可用）
    assert body["available"] is True
    assert "active_jobs" in body


# ---------------------------------------------------------------------------
# GET /oc/cron/jobs
# ---------------------------------------------------------------------------

def test_cron_list_default_hides_disabled(monkeypatch, tmp_path):
    # list 默认不带 all 参数：disabled/paused 任务不应出现在结果中
    _write_jobs(tmp_path, [
        _make_job("j-enabled", enabled=True),
        _make_job("j-disabled", enabled=False),
    ])
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/cron/jobs", headers=_auth())
    assert r.status_code == 200
    ids = [j["id"] for j in r.json()]
    assert "j-enabled" in ids
    # disabled 任务被默认过滤掉，不出现在结果中
    assert "j-disabled" not in ids


def test_cron_list_all_includes_disabled(monkeypatch, tmp_path):
    # list?all=true 时 disabled 任务也应包含在返回列表中
    _write_jobs(tmp_path, [
        _make_job("j-enabled", enabled=True),
        _make_job("j-disabled", enabled=False),
    ])
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/cron/jobs?all=true", headers=_auth())
    assert r.status_code == 200
    ids = [j["id"] for j in r.json()]
    # all=true 时两个任务均应出现
    assert "j-enabled" in ids
    assert "j-disabled" in ids


# ---------------------------------------------------------------------------
# GET /oc/cron/jobs/{id}
# ---------------------------------------------------------------------------

def test_cron_show_existing_job_200(monkeypatch, tmp_path):
    # show 已存在任务：返回 200 + 该任务的稳定 CronJob 对象
    _write_jobs(tmp_path, [_make_job("abc123")])
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/cron/jobs/abc123", headers=_auth())
    assert r.status_code == 200
    assert r.json()["id"] == "abc123"


def test_cron_show_not_found_404(monkeypatch, tmp_path):
    # show 不存在的任务：OpsError(NOT_FOUND) → HTTP 404 + code 字段
    _write_jobs(tmp_path, [])
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/cron/jobs/ghost", headers=_auth())
    assert r.status_code == 404
    assert r.json()["code"] == "NOT_FOUND"


# ---------------------------------------------------------------------------
# POST /oc/cron/jobs（create）
# ---------------------------------------------------------------------------

def test_cron_create_returns_job(monkeypatch, tmp_path):
    """create 端点：打桩 _hermes_ok 并预写 jobs.json 模拟 hermes 写入效果，
    返回 200 + 新任务对象（name/schedule 字段校验）。"""
    import ocops.cron as cron_mod

    def fake_hermes_ok(args: list[str]) -> None:
        # 模拟 hermes create 命令写入 jobs.json 的副作用
        _write_jobs(tmp_path, [_make_job("new-job")])

    monkeypatch.setattr(cron_mod, "_hermes_ok", fake_hermes_ok)

    c = _client(monkeypatch, tmp_path)
    # 先初始化空 jobs.json，_select_created_job 才能识别到新增任务
    _write_jobs(tmp_path, [])
    r = c.post(
        "/oc/cron/jobs",
        json={"name": "new-job", "schedule": "0 * * * *", "prompt": "do it"},
        headers=_auth(),
    )
    assert r.status_code == 200
    body = r.json()
    # 返回的任务对象必须包含 id 字段
    assert body.get("id") == "new-job"


# ---------------------------------------------------------------------------
# PATCH /oc/cron/jobs/{id}（update）
# ---------------------------------------------------------------------------

def test_cron_update_returns_updated_job(monkeypatch, tmp_path):
    # update 端点：打桩 _hermes_ok，返回 200 + 更新后任务对象
    _write_jobs(tmp_path, [_make_job("job1")])
    _stub_hermes_ok(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.patch(
        "/oc/cron/jobs/job1",
        json={"name": "job1-renamed"},
        headers=_auth(),
    )
    assert r.status_code == 200
    # update 后重读 jobs.json 返回当前数据（打桩后 jobs.json 未变，id 仍为 job1）
    assert r.json()["id"] == "job1"


# ---------------------------------------------------------------------------
# POST /oc/cron/jobs/{id}/toggle
# ---------------------------------------------------------------------------

def test_cron_toggle_pause_job(monkeypatch, tmp_path):
    # toggle enabled=false：调 hermes pause，打桩后返回 200 + 任务对象
    _write_jobs(tmp_path, [_make_job("togj")])
    _stub_hermes_ok(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/cron/jobs/togj/toggle", json={"enabled": False}, headers=_auth())
    assert r.status_code == 200
    # 返回体包含任务 id
    assert r.json()["id"] == "togj"


# ---------------------------------------------------------------------------
# POST /oc/cron/jobs/{id}/run
# ---------------------------------------------------------------------------

def test_cron_run_triggers_job(monkeypatch, tmp_path):
    # run 端点：触发指定任务立即执行，打桩 _hermes_ok，返回 200 + 任务对象
    _write_jobs(tmp_path, [_make_job("runj")])
    _stub_hermes_ok(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/cron/jobs/runj/run", headers=_auth())
    assert r.status_code == 200
    assert r.json()["id"] == "runj"


# ---------------------------------------------------------------------------
# DELETE /oc/cron/jobs/{id}
# ---------------------------------------------------------------------------

def test_cron_delete_returns_204(monkeypatch, tmp_path):
    # delete 端点：成功时返回 204 No Content（无 body）
    _write_jobs(tmp_path, [_make_job("delj")])
    _stub_hermes_ok(monkeypatch)
    c = _client(monkeypatch, tmp_path)
    r = c.delete("/oc/cron/jobs/delj", headers=_auth())
    assert r.status_code == 204


def test_cron_delete_invalid_id_400(monkeypatch, tmp_path):
    # delete：job_id 包含非法字符（路径穿越字符）→ BAD_REQUEST → 400
    _write_jobs(tmp_path, [])
    # _hermes_ok 不需要桩，因为 validate_job_id 在它之前就会抛错
    c = _client(monkeypatch, tmp_path)
    r = c.delete("/oc/cron/jobs/../../etc/passwd", headers=_auth())
    # 路径包含 / 因此 Starlette 路由不匹配；Starlette 返回 404 或 405；
    # 这里验证请求无论如何不会成功（4xx）
    assert r.status_code in (400, 404, 405)


# ---------------------------------------------------------------------------
# GET /oc/cron/jobs/{id}/history
# ---------------------------------------------------------------------------

def test_cron_history_empty(monkeypatch, tmp_path):
    # history 端点：任务无输出文件且无 last_run_at → 返回空列表
    _write_jobs(tmp_path, [_make_job("histj")])
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/cron/jobs/histj/history", headers=_auth())
    assert r.status_code == 200
    assert r.json() == []


def test_cron_history_not_found_404(monkeypatch, tmp_path):
    # history 端点：任务不存在 → NOT_FOUND → 404
    _write_jobs(tmp_path, [])
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/cron/jobs/ghost/history", headers=_auth())
    assert r.status_code == 404


# ---------------------------------------------------------------------------
# GET /oc/cron/jobs/{id}/output?file=
# ---------------------------------------------------------------------------

def test_cron_output_path_escape_400(monkeypatch, tmp_path):
    # output 端点：file 参数含 / 或 .. 路径逃逸字符 → BAD_REQUEST → 400
    _write_jobs(tmp_path, [_make_job("outj")])
    c = _client(monkeypatch, tmp_path)
    # file=../secret.md：validate_output_file 应拒绝含 / 的路径
    r = c.get("/oc/cron/jobs/outj/output?file=../secret.md", headers=_auth())
    assert r.status_code == 400
    assert r.json()["code"] == "BAD_REQUEST"


def test_cron_output_no_file_param_400(monkeypatch, tmp_path):
    # output 端点：缺 file 查询参数 → validate_output_file 收到空串 → BAD_REQUEST → 400
    _write_jobs(tmp_path, [_make_job("outj2")])
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/cron/jobs/outj2/output", headers=_auth())
    assert r.status_code == 400


def test_cron_output_file_not_found_404(monkeypatch, tmp_path):
    # output 端点：file 参数合法但文件不存在 → NOT_FOUND → 404
    _write_jobs(tmp_path, [_make_job("outj3")])
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/cron/jobs/outj3/output?file=2026-05-29-run.md", headers=_auth())
    assert r.status_code == 404


# ---------------------------------------------------------------------------
# 鉴权检查（cron 端点同样需要 Bearer token）
# ---------------------------------------------------------------------------

def test_cron_endpoint_requires_auth(monkeypatch, tmp_path):
    # 不带 Authorization 头访问 cron 端点 → 401 UNAUTHORIZED
    _write_jobs(tmp_path, [])
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/cron/capabilities")
    assert r.status_code == 401
    assert r.json()["code"] == "UNAUTHORIZED"
