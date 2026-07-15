"""为 AICC 只读网页工具追加可持久化的来源审计载荷。"""

from __future__ import annotations

import hashlib
import inspect
import json
import os
from typing import Any
from urllib.parse import urlparse


def _enterprise_domains() -> frozenset[str]:
    """读取平台注入的企业域名集合；未命中时一律按普通公开网络处理。"""
    return frozenset(value.strip().lower() for value in os.getenv("OC_AICC_ENTERPRISE_DOMAINS", "").split(",") if value.strip())


def _is_enterprise_network(url: str) -> bool:
    """仅精确域或子域命中企业配置时标为企业相关网络，且永远声明未确认。"""
    host = (urlparse(url).hostname or "").lower()
    return any(host == domain or host.endswith("." + domain) for domain in _enterprise_domains())


def _source(url: Any, title: Any, position: int) -> dict[str, Any] | None:
    """把一个受控网页工具结果转换为稳定、可回显的来源记录。"""
    if not isinstance(url, str) or not url.strip():
        return None
    parsed = urlparse(url.strip())
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        return None
    scope = "enterprise_network" if _is_enterprise_network(url) else "public_network"
    source_title = title.strip() if isinstance(title, str) and title.strip() else parsed.netloc
    digest = hashlib.sha256(url.strip().encode("utf-8")).hexdigest()[:16]
    return {
        "type": "web",
        "title": source_title,
        "url": url.strip(),
        "scope": scope,
        "reference_id": f"web:{scope}:{position}:{digest}",
        # 企业相关网页也只能作为未经企业确认的公开网络补充信息。
        "unconfirmed": scope == "enterprise_network",
    }


def append_web_audit(result: Any) -> Any:
    """保持 Hermes 原工具结果不变，并在成功 JSON 中附加来源审计白名单。"""
    if not isinstance(result, str):
        return result
    try:
        payload = json.loads(result)
    except json.JSONDecodeError:
        return result
    if not isinstance(payload, dict):
        return result
    candidates = payload.get("results")
    if not isinstance(candidates, list):
        candidates = payload.get("data", {}).get("web", []) if isinstance(payload.get("data"), dict) else []
    sources = []
    if isinstance(candidates, list):
        for index, candidate in enumerate(candidates):
            if not isinstance(candidate, dict) or candidate.get("error"):
                continue
            source = _source(candidate.get("url"), candidate.get("title"), index)
            if source is not None:
                sources.append(source)
    payload["aicc_response_sources"] = sources
    return json.dumps(payload, ensure_ascii=False)


def register_with_hermes_registry(registry: Any) -> None:
    """用同名 web toolset 条目包装上游 handler，维持 schema 与可用性检查不变。"""
    for name in ("web_search", "web_extract"):
        entry = registry.get_entry(name)
        if entry is None:
            raise RuntimeError(f"AICC_WEB_AUDIT_TOOL_MISSING: {name}")
        original = entry.handler

        if entry.is_async:
            async def audited_async(args: Any, _handler: Any = original, **kwargs: Any) -> Any:
                result = _handler(args, **kwargs)
                if inspect.isawaitable(result):
                    result = await result
                return append_web_audit(result)

            handler = audited_async
        else:
            def audited_sync(args: Any, _handler: Any = original, **kwargs: Any) -> Any:
                return append_web_audit(_handler(args, **kwargs))

            handler = audited_sync
        registry.register(
            name=name,
            toolset=entry.toolset,
            schema=entry.schema,
            handler=handler,
            check_fn=entry.check_fn,
            requires_env=entry.requires_env,
            is_async=entry.is_async,
            description=entry.description,
            emoji=entry.emoji,
            max_result_size_chars=entry.max_result_size_chars,
            dynamic_schema_overrides=entry.dynamic_schema_overrides,
        )
