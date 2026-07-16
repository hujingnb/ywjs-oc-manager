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


def search_knowledge(arguments: dict[str, Any], **_task_metadata: Any) -> str:
    """检索当前客服所属企业的知识库，并附带 manager 可审计的稳定来源引用。

    Hermes registry 将 schema 参数整体作为字典传给 handler，并额外注入 ``task_id``
    等只读追踪元数据；后者不是工具契约的一部分，必须忽略。不能把字典误当作问题
    字符串，否则模型会反复收到无效参数错误并退化到不必要的公网检索。
    """
    question = arguments.get("question") if isinstance(arguments, dict) else None
    if not isinstance(question, str) or not question.strip():
        raise ValueError("AICC_KNOWLEDGE_SEARCH_INVALID_QUESTION")
    base_url, app_token = _runtime_configuration()
    payload = json.dumps({"question": question.strip(), "top_k": FIXED_TOP_K}).encode("utf-8")
    request = Request(
        f"{base_url}{RUNTIME_SEARCH_PATH}",
        data=payload,
        headers={
            "Authorization": f"Bearer {app_token}",
            # manager runtime knowledge API 以 app-scoped header 做鉴权；Bearer 仅保留给
            # 同一 bootstrap token 约定的兼容链路，不能替代该头。
            "X-OC-App-Token": app_token,
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
    parsed["aicc_response_sources"] = _response_sources(parsed.get("results"))
    # Hermes 会把工具返回值写进 OpenAI-compatible tool message；该协议的 content 只能
    # 是字符串或 parts，直接返回 Python dict 会使后续模型调用 400 中断。
    return json.dumps(parsed, ensure_ascii=False)


def _response_sources(results: Any) -> list[dict[str, Any]]:
    """把 manager 检索命中转换为最终答复可引用的审计来源。

    ``reference_id`` 由本轮工具结果中的 scope、document_id 和稳定索引组成；模型只能
    回显该值，manager 会从 Hermes 的 tool transcript 重建相同白名单后才允许持久化。
    """
    if not isinstance(results, list):
        return []
    sources: list[dict[str, Any]] = []
    for index, item in enumerate(results):
        if not isinstance(item, dict):
            continue
        document_id = item.get("document_id")
        scope = item.get("scope")
        if not isinstance(document_id, str) or not document_id or not isinstance(scope, str) or not scope:
            continue
        title = item.get("document_name")
        if not isinstance(title, str) or not title.strip():
            title = document_id
        sources.append(
            {
                "type": "knowledge",
                "title": title.strip(),
                "scope": scope,
                "reference_id": f"knowledge:{scope}:{document_id}:{index}",
            }
        )
    return sources


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
