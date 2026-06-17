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
        # display.language=zh：让 hermes 自带 i18n（agent/i18n.py + locales/zh.yaml）
        # 把所有走 t() 的用户可见文案（审批提示、/status、/agents、reset/restart 通知、
        # /goal、/resume、kanban 等）输出为简体中文。run.py 里少数未走 t() 的裸字符串
        # 由构建期补丁 patches/patch_run_i18n_literals.py 单独翻译，两者配合实现完整中文化。
        "display": {"language": "zh"},
        "model": {
            "default": m.app_model, "provider": "custom",
            "base_url": base_url, "api_key": m.openai_api_key,
        },
        "auxiliary": _build_auxiliary(m, base_url),
        "memory": {
            "memory_enabled": True, "user_profile_enabled": True,
            "memory_char_limit": 2200, "user_char_limit": 1375,
        },
        "terminal": {
            "backend": "local", "cwd": "/opt/data/workspace",
            "timeout": 180, "lifetime_seconds": 300,
        },
        # 关闭上游 hermes-agent 的 dangerous-command 审批：
        # - mode="off" 命中上游 _normalize_approval_mode 的 yolo 分支，跳过所有
        #   dangerous-command 提示（受控部署形态下，逐条 /approve 是噪声非收益）。
        # - cron_mode="approve" 是兜底——当前 mode=off 命中 yolo 后 cron 路径
        #   走不到，但留这一项保证将来若 mode 被改回 manual/smart，cron 任务遇
        #   危险命令仍放行而非被 deny。
        # 不可绕过的上游 hardline 命令（rm -rf /、mkfs、dd raw、shutdown、
        # fork bomb、kill -1 等）仍由 hermes-agent 硬拦，本配置不影响。
        # YAML 落地：PyYAML 对字符串 "off" 自动加单引号输出 `mode: 'off'`，
        # 不需要手写引号包装；回读后仍是字符串 "off"。
        "approvals": {
            "mode": "off",
            "cron_mode": "approve",
        },
    }
    header = "# Hermes 配置 - 由 oc-entrypoint 在容器启动时渲染（manifest v2）\n"
    body = header + yaml.safe_dump(config, allow_unicode=True, sort_keys=False)
    write_text(data_root / "config.yaml", body)
    return "config.yaml"
