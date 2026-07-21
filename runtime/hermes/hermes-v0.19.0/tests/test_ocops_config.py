# tests/test_ocops_config.py
"""覆盖 GET /oc/config 端点：返回实例当前运行的 display.language。

端点供 manager 实时查实例真正在用的语言，读 /opt/data/config.yaml 的
display.language 字段，缺失时回落 en。
"""
import json
from pathlib import Path

import pytest
import yaml
from starlette.testclient import TestClient


def _client(monkeypatch, tmp_path) -> TestClient:
    """构造带固定 token 与 tmp 数据根的测试 client。

    仿 test_ocops_server_basic.py 的 _client helper：
    - OC_OPS_TOKEN 固定为 t0ken，方便携带正确 Authorization 头。
    - OC_DATA_DIR 指向 tmp_path，使端点读临时目录下的 config.yaml。
    - 补 OC_INFO_FILE 指向假身份文件，避免其他端点初始化报错。
    """
    monkeypatch.setenv("OC_OPS_TOKEN", "t0ken")
    monkeypatch.setenv("OC_DATA_DIR", str(tmp_path))
    f = tmp_path / "oc-image.json"
    f.write_text(json.dumps({
        "variant": "hermes-v0.19.0",
        "hermes_upstream_ref": "x",
        "built_at": "y",
    }))
    monkeypatch.setenv("OC_INFO_FILE", str(f))
    # 重新导入 app，使 monkeypatch 设置的环境变量生效
    from ocops.server import app
    return TestClient(app)


# ---------------------------------------------------------------------------
# 场景 1：config.yaml 含 display.language: zh → 返回 200 + {"display_language":"zh"}
# ---------------------------------------------------------------------------

def test_config_returns_display_language(monkeypatch, tmp_path):
    """正常路径：config.yaml 存在且含 display.language=zh，返回 200 + 正确字段。"""
    # 在 tmp 数据根写 config.yaml，模拟容器内 /opt/data/config.yaml
    config_yaml = tmp_path / "config.yaml"
    config_yaml.write_text("display:\n  language: zh\n", encoding="utf-8")

    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/config", headers={"Authorization": "Bearer t0ken"})
    assert r.status_code == 200
    body = r.json()
    assert body["display_language"] == "zh"


# ---------------------------------------------------------------------------
# 场景 2：config.yaml 缺少 display.language → 返回默认 "en"
# 包含两个子边界：(a) 文件缺失；(b) 文件存在但无 display 键。
# ---------------------------------------------------------------------------

def test_config_missing_display_language_defaults_to_en(monkeypatch, tmp_path):
    """边界：config.yaml 存在但无 display.language 键，回落返回 en。"""
    # 写一个不含 display 块的最小 config
    config_yaml = tmp_path / "config.yaml"
    config_yaml.write_text("other_key: value\n", encoding="utf-8")

    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/config", headers={"Authorization": "Bearer t0ken"})
    assert r.status_code == 200
    assert r.json()["display_language"] == "en"


def test_config_missing_file_defaults_to_en(monkeypatch, tmp_path):
    """边界：config.yaml 文件完全不存在，回落返回 en（不应报 500）。"""
    # tmp_path 下不创建 config.yaml，模拟文件缺失场景
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/config", headers={"Authorization": "Bearer t0ken"})
    assert r.status_code == 200
    assert r.json()["display_language"] == "en"


# ---------------------------------------------------------------------------
# 场景 3：config.yaml 内容损坏（YAML 语法错误）→ 回落 "en"，不应返回 500
# ---------------------------------------------------------------------------

def test_config_corrupted_yaml_defaults_to_en(monkeypatch, tmp_path):
    """异常路径：config.yaml 存在但内容为非法 YAML（语法错误），
    应回落返回 200 + {"display_language":"en"}，不得返回 500。
    覆盖 yaml.YAMLError 被 OSError+yaml.YAMLError 捕获的边界场景。"""
    # 写入无法被 yaml.safe_load 解析的损坏内容（未闭合的 mapping）
    config_yaml = tmp_path / "config.yaml"
    config_yaml.write_text("display: {language: zh\n", encoding="utf-8")

    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/config", headers={"Authorization": "Bearer t0ken"})
    # 损坏 YAML 不应触发 500，应安全回落到默认语言 en
    assert r.status_code == 200
    assert r.json()["display_language"] == "en"


# ---------------------------------------------------------------------------
# 场景 4：无 token / 错误 token → 401（与其它 /oc/* 端点鉴权行为一致）
# ---------------------------------------------------------------------------

def test_config_no_token_returns_401(monkeypatch, tmp_path):
    """鉴权：不携带 Authorization 头 → 401 UNAUTHORIZED。"""
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/config")
    assert r.status_code == 401
    assert r.json()["code"] == "UNAUTHORIZED"


def test_config_wrong_token_returns_401(monkeypatch, tmp_path):
    """鉴权：携带错误 token → 401 UNAUTHORIZED，与 /oc/info 行为一致。"""
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/config", headers={"Authorization": "Bearer wrongtoken"})
    assert r.status_code == 401
    assert r.json()["code"] == "UNAUTHORIZED"
