"""把 manifest 渲染为本 variant 期望的 hermes config.yaml。

字段对照 spec §6.2；base_url 拼接 /v1 由本 variant 决定（manager 写时不带 /v1）。
"""

from __future__ import annotations

from pathlib import Path

from lib.atomic import write_text
from lib.manifest import Manifest

TEMPLATE = """# Hermes 配置 - 由 oc-entrypoint 在容器启动时渲染
# manifest.app.model + manifest.credentials.openai 进 model 段；
# auxiliary 全部 main，避免拨 OpenRouter；terminal.cwd 固定 /opt/data/workspace。

model:
  default: {model!r}
  provider: "custom"
  base_url: {base_url!r}
  api_key: {api_key!r}

auxiliary:
  vision:         {{ provider: main }}
  compression:    {{ provider: main }}
  web_extract:    {{ provider: main }}
  session_search: {{ provider: main }}

memory:
  memory_enabled: true
  user_profile_enabled: true
  memory_char_limit: 2200
  user_char_limit: 1375

terminal:
  backend: "local"
  cwd: "/opt/data/workspace"
  timeout: 180
  lifetime_seconds: 300
"""


def render(m: Manifest, data_root: Path) -> str:
    """渲染 config.yaml 到 data_root/config.yaml，返回相对路径。"""
    data_root.mkdir(parents=True, exist_ok=True)
    body = TEMPLATE.format(
        model=m.app_model,
        base_url=m.openai_base_url.rstrip("/") + "/v1",
        api_key=m.openai_api_key,
    )
    write_text(data_root / "config.yaml", body)
    return "config.yaml"
