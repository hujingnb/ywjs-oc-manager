# ocops/conversation.py
"""会话转发层：把 manager 经 oc-ops 发来的会话请求转发到同 pod 内的 hermes
api_server（127.0.0.1:8642 /api/sessions/*），注入 Bearer 鉴权并把非 2xx 响应
映射为 OpsError。manager 不持有会话数据，oc-ops 仅做带 token 的透传 + 字段裁剪。

鉴权来源见 _api_server_key：优先 env API_SERVER_KEY，回退共享卷 config.yaml 的
api_server.key（生产环境通常两者皆空 → api_server 不鉴权，不发 Authorization 头）。"""
from __future__ import annotations

import json
import os
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

from ocops.errors import OpsError

# api_server 容器内回环地址；与 skills.py RELOAD_URL 同一进程。
_API_BASE = "http://127.0.0.1:8642"
# 单次转发超时（秒）。续聊可能触发一次 agent 回合，给足时间但不无限阻塞。
_TIMEOUT = 120


def _api_server_key() -> str:
    """解析 api_server 的 Bearer key。

    顺序（命中即返回）：环境变量 API_SERVER_KEY → /opt/data/config.yaml 的
    api_server.key。两者都没有时返回空串（api_server 无 key 时 _check_auth 放行）。
    """
    env = os.environ.get("API_SERVER_KEY", "").strip()
    if env:
        return env
    cfg = Path(os.environ.get("OC_DATA_DIR", "/opt/data")) / "config.yaml"
    try:
        import yaml  # 镜像内随 hermes venv 提供
        data = yaml.safe_load(cfg.read_text(encoding="utf-8")) or {}
        return str((data.get("api_server") or {}).get("key", "") or "")
    except Exception:
        return ""


def _request(method: str, path: str, body: dict | None = None) -> bytes:
    """对 api_server 发一次请求，返回原始响应体；非 2xx / 网络错误映射为 OpsError。"""
    url = _API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    key = _api_server_key()
    if key:
        req.add_header("Authorization", "Bearer " + key)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=_TIMEOUT) as resp:
            return resp.read()
    except urllib.error.HTTPError as e:
        # 把 api_server 的 HTTP 状态码翻译成 oc-ops 契约错误码
        code = {400: "BAD_REQUEST", 401: "INTERNAL", 404: "NOT_FOUND"}.get(e.code, "INTERNAL")
        raise OpsError(code, f"api_server {e.code}: {e.reason}")
    except Exception as e:
        raise OpsError("INTERNAL", f"调用 api_server 失败: {e}")


def _json(method: str, path: str, body: dict | None = None) -> dict:
    """同 _request，但把响应体解析为 dict。"""
    raw = _request(method, path, body)
    try:
        return json.loads(raw)
    except Exception as e:
        raise OpsError("INTERNAL", f"api_server 响应非 JSON: {e}")


def list_sessions(source: str = "", limit: int = 50, offset: int = 0) -> list:
    """列出会话；source 非空时按渠道来源过滤。返回 api_server 的 data 数组。"""
    q = {"limit": str(limit), "offset": str(offset)}
    if source:
        q["source"] = source
    out = _json("GET", "/api/sessions?" + urllib.parse.urlencode(q))
    return out.get("data", [])


def session_messages(session_id: str) -> list:
    """读某会话的历史消息数组。"""
    sid = urllib.parse.quote(session_id, safe="")
    out = _json("GET", f"/api/sessions/{sid}/messages")
    return out.get("data", out) if isinstance(out, dict) else out


def create_session(body: dict) -> dict:
    """新建会话；body 透传（source/title 等），返回新建会话对象。"""
    return _json("POST", "/api/sessions", body)


def delete_session(session_id: str) -> None:
    """删除会话。"""
    sid = urllib.parse.quote(session_id, safe="")
    _request("DELETE", f"/api/sessions/{sid}")


def chat(session_id: str, body: dict) -> dict:
    """单轮续聊（非流式），body 含 message（文字/图片 parts）。返回 assistant 回复对象。"""
    sid = urllib.parse.quote(session_id, safe="")
    return _json("POST", f"/api/sessions/{sid}/chat", body)
