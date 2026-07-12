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
    f.write_text(json.dumps({"variant": "hermes-v2026.7.1", "hermes_upstream_ref": "x", "built_at": "y"}))
    monkeypatch.setenv("OC_INFO_FILE", str(f))
    from ocops.server import app
    return TestClient(app)


def test_requires_bearer_token(monkeypatch, tmp_path, ocops_schema):
    # 鉴权：无 token → 401，body code 为 UNAUTHORIZED
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/info")
    assert r.status_code == 401
    validate(r.json(), ocops_schema("common/error.schema.json"))


def test_healthz_requires_api_server_ready(monkeypatch, tmp_path):
    # 就绪边界：oc-ops 已监听端口但 api_server 未恢复时，healthz 必须返回 503，
    # 防止 Kubernetes 过早将 pod 放入 Service endpoints。
    from unittest import mock

    with mock.patch("ocops.server.conversation.list_sessions", side_effect=Exception("api_server starting")):
        r = _client(monkeypatch, tmp_path).get("/healthz")
    assert r.status_code == 503


def test_healthz_accepts_ready_api_server(monkeypatch, tmp_path):
    # 正常路径：api_server 能读取轻量会话列表时，healthz 返回 200 允许 pod 接流量。
    from unittest import mock

    with mock.patch("ocops.server.conversation.list_sessions", return_value=[]):
        r = _client(monkeypatch, tmp_path).get("/healthz")
    assert r.status_code == 200


def test_info_ok(monkeypatch, tmp_path, ocops_schema):
    # 正常：带正确 token → 200，返回镜像身份字段 variant
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/info", headers={"Authorization": "Bearer t0ken"})
    assert r.status_code == 200
    validate(r.json(), ocops_schema("core/info.schema.json"))
    assert r.json()["variant"] == "hermes-v2026.7.1"


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
