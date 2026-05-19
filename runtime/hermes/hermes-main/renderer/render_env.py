"""渲染 .env 文件。

本 variant 内 .env 仅承载「行为开关」：hermes 进程从 .env 读这些 ENV，
manifest.credentials.openai 通过 config.yaml 落地，不重复进 .env。
微信凭证由 hermes 自管，oc-channel-login 自行写入；本 renderer 不涉及。
"""

from __future__ import annotations

from pathlib import Path

from lib.atomic import write_text

BODY = """# Hermes 行为开关 - 由 oc-entrypoint 渲染

# 绕过 Hermes user pairing 流程（本地部署无交互 CLI 跑 approve）
GATEWAY_ALLOW_ALL_USERS=true

# Weixin platform policy：必须显式 open，否则未授权 DM 一律拒
WEIXIN_DM_POLICY=open
"""


def render(data_root: Path) -> str:
    data_root.mkdir(parents=True, exist_ok=True)
    write_text(data_root / ".env", BODY)
    return ".env"
