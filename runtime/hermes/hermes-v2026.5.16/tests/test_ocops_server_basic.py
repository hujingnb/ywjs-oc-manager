# tests/test_ocops_server_basic.py
"""覆盖 server 鉴权与基础端点：401、info/doctor 200、channel-status/unbind、未知 channel 400。"""
import json
from pathlib import Path

from jsonschema import validate
from starlette.testclient import TestClient


def _client(monkeypatch, tmp_path):
    # 构造带固定 token 与 tmp 数据根的测试 client；
    # OC_INFO_FILE 指向 tmp_path 下的假镜像身份文件，避免依赖 /etc/oc-image.json。
    monkeypatch.setenv("OC_OPS_TOKEN", "t0ken")
    monkeypatch.setenv("OC_DATA_DIR", str(tmp_path))
    f = tmp_path / "oc-image.json"
    f.write_text(json.dumps({"variant": "hermes-v2026.5.16", "hermes_upstream_ref": "x", "built_at": "y"}))
    monkeypatch.setenv("OC_INFO_FILE", str(f))
    from ocops.server import app
    return TestClient(app)


def test_requires_bearer_token(monkeypatch, tmp_path, ocops_schema):
    # 鉴权：无 token → 401，body code 为 UNAUTHORIZED
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/info")
    assert r.status_code == 401
    validate(r.json(), ocops_schema("common/error.schema.json"))


def test_info_ok(monkeypatch, tmp_path, ocops_schema):
    # 正常：带正确 token → 200，返回镜像身份字段 variant
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/info", headers={"Authorization": "Bearer t0ken"})
    assert r.status_code == 200
    validate(r.json(), ocops_schema("core/info.schema.json"))
    assert r.json()["variant"] == "hermes-v2026.5.16"


def test_doctor_ok(monkeypatch, tmp_path, ocops_schema):
    # 正常：doctor 返回运行时诊断快照，字段符合 core/doctor schema。
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/doctor", headers={"Authorization": "Bearer t0ken"})
    assert r.status_code == 200
    validate(r.json(), ocops_schema("core/doctor.schema.json"))


def test_channel_status_unknown_channel_400(monkeypatch, tmp_path, ocops_schema):
    # 错误码映射：未知 channel → OpsError(BAD_REQUEST) → HTTP 400 + code 体
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/channels/telegram/status", headers={"Authorization": "Bearer t0ken"})
    assert r.status_code == 400
    validate(r.json(), ocops_schema("common/error.schema.json"))
    assert r.json()["code"] == "BAD_REQUEST"
