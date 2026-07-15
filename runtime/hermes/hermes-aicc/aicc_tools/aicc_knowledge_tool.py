"""AICC 专用企业知识检索工具。

该工具不执行 shell 命令，也不接收数据集、URL 或写入参数；它只能使用当前 app 的
runtime token 调 manager 受控知识检索 API。
"""

from __future__ import annotations

import json
import os
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


RUNTIME_SEARCH_PATH = "/api/v1/runtime/knowledge/search"
FIXED_TOP_K = 8


def _runtime_configuration() -> tuple[str, str]:
    """读取 app-scoped runtime 配置，缺失时拒绝运行以避免错误回退到匿名请求。"""
    base_url = os.environ.get("OC_KB_RUNTIME_BASE_URL", "").rstrip("/")
    app_token = os.environ.get("OC_KB_APP_TOKEN", "")
    if not base_url or not app_token:
        raise RuntimeError("AICC_KNOWLEDGE_SEARCH_UNCONFIGURED")
    return base_url, app_token


def search_knowledge(question: str) -> dict[str, Any]:
    """检索当前客服所属企业的知识库，并返回 manager 原样 JSON 结果。"""
    if not isinstance(question, str) or not question.strip():
        raise ValueError("AICC_KNOWLEDGE_SEARCH_INVALID_QUESTION")
    base_url, app_token = _runtime_configuration()
    payload = json.dumps({"question": question.strip(), "top_k": FIXED_TOP_K}).encode("utf-8")
    request = Request(
        f"{base_url}{RUNTIME_SEARCH_PATH}",
        data=payload,
        headers={
            "Authorization": f"Bearer {app_token}",
            "Content-Type": "application/json",
            "Accept": "application/json",
        },
        method="POST",
    )
    try:
        with urlopen(request, timeout=15) as response:  # noqa: S310 -- URL 只来自 manager bootstrap manifest。
            body = response.read()
    except (HTTPError, URLError, TimeoutError, OSError) as exc:
        raise RuntimeError("AICC_KNOWLEDGE_SEARCH_FAILED") from exc
    try:
        parsed = json.loads(body.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise RuntimeError("AICC_KNOWLEDGE_SEARCH_INVALID_RESPONSE") from exc
    if not isinstance(parsed, dict):
        raise RuntimeError("AICC_KNOWLEDGE_SEARCH_INVALID_RESPONSE")
    return parsed


def register_with_hermes_registry(registry: Any) -> None:
    """向 Hermes 注册 AICC 专用工具集，避免落入通用 terminal toolset。"""
    registry.register(
        name="aicc_knowledge_search",
        toolset="aicc",
        schema={
            "name": "aicc_knowledge_search",
            "description": "检索当前企业已授权知识库；仅接受访客问题文本。",
            "parameters": {
                "type": "object",
                "properties": {
                    "question": {"type": "string", "description": "访客需要查询的问题。"},
                },
                "required": ["question"],
                "additionalProperties": False,
            },
        },
        handler=search_knowledge,
        description="检索当前企业已授权知识库；仅接受访客问题文本。",
    )
