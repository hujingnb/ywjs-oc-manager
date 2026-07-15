"""把 manifest 渲染为本 variant 期望的 hermes config.yaml。

manifest v2：auxiliary 8 个槽位按 manifest.routing 渲染——指定模型的槽位走
custom + 该模型，未指定的走 { provider: main }。base_url 拼 /v1 由本 variant 决定。
"""

from __future__ import annotations

from pathlib import Path

import yaml

from lib.atomic import write_text
from lib.manifest import Manifest

# AUXILIARY_SLOTS 是智能路由的 8 个 auxiliary 槽位，顺序固定，与 manager 端约定一致。
AUXILIARY_SLOTS = [
    "vision", "compression", "web_extract", "session_search",
    "title_generation", "approval", "skills_hub", "mcp",
]


def _build_auxiliary(m: Manifest, base_url: str) -> dict:
    """按 manifest.routing 构造 auxiliary 段：指定模型走 custom，未指定走 main。"""
    aux: dict = {}
    routing = m.routing or {}
    for slot in AUXILIARY_SLOTS:
        model = str(routing.get(slot) or "").strip()
        if model:
            aux[slot] = {
                "provider": "custom", "model": model,
                "base_url": base_url, "api_key": m.openai_api_key,
            }
        else:
            aux[slot] = {"provider": "main"}
    return aux


def render(m: Manifest, data_root: Path) -> str:
    """渲染 config.yaml 到 data_root/config.yaml，返回相对路径。"""
    data_root.mkdir(parents=True, exist_ok=True)
    base_url = m.openai_base_url.rstrip("/") + "/v1"
    config = {
        # display.language：语言来自 manifest app.language（manager 快照应用所有者设置的 UI 语言），
        # 缺省回落 "en"。让 hermes 自带 i18n（agent/i18n.py + locales/）把所有走 t() 的
        # 用户可见文案（审批提示、/status、/agents、reset/restart 通知、/goal、/resume、
        # kanban 等）输出为对应语言。
        # run.py / base.py 里原本未走 t() 的裸字符串，已由构建期补丁
        # patches/patch_i18n_literals.py 接入 oc.* catalog（locales/oc_overlay.yaml，
        # 构建期合并进 upstream en/zh.yaml），随 display.language 输出对应中/英文，
        # 不再有中英混杂的已知局限。
        "display": {"language": (m.app_language or "en")},
        "model": {
            "default": m.app_model, "provider": "custom",
            "base_url": base_url, "api_key": m.openai_api_key,
        },
        "auxiliary": _build_auxiliary(m, base_url),
        # AICC 每轮上下文由 manager 重建，禁止 Hermes 保存跨会话记忆或访客画像。
        "memory": {"memory_enabled": False, "user_profile_enabled": False},
        # toolset 是上游 Hermes 的第一层可见工具集收敛；Task 2 的补丁还会按 manifest
        # capabilities 过滤具体函数并在 dispatch 时复核，不能以此列表替代授权判断。
        "platform_toolsets": {"api_server": ["aicc", "web", "skills", "vision"]},
        # 公开网络只允许只读检索；搜索用无需企业侧密钥的 DDGS，正文提取交给共享 Firecrawl。
        "web": {"search_backend": "ddgs", "extract_backend": "firecrawl"},
    }
    header = "# Hermes 配置 - 由 oc-entrypoint 在容器启动时渲染（manifest v2）\n"
    body = header + yaml.safe_dump(config, allow_unicode=True, sort_keys=False)
    write_text(data_root / "config.yaml", body)
    return "config.yaml"
