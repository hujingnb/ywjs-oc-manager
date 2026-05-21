"""渲染 .env 文件。

.env 由两部分组成：

1. 固定「行为开关」：hermes 进程从 .env 读这些 ENV。
   manifest.credentials.openai 通过 config.yaml 落地，不重复进 .env。

2. 微信渠道凭证转译：微信扫码由容器内 oc-channel-login 完成，hermes 上游
   qr_login 把账号凭证落盘到 /opt/data/weixin/accounts/<account_id>.json。
   但 hermes gateway 启动时**是否启用 weixin 平台**取决于环境变量
   WEIXIN_TOKEN / WEIXIN_ACCOUNT_ID（见上游 gateway/config.py），不是
   扫描 accounts 目录。因此 oc-entrypoint 每次启动都把 accounts 里 hermes
   自管的凭证转译成 WEIXIN_* 写进 .env —— 这正是「hermes 自管数据 +
   启动脚本转成 hermes 需要的格式」职责的体现。

   未扫码（accounts 为空）时只写行为开关，hermes 启动为「无 messaging
   platform」的纯 cron 模式，符合预期。
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Optional

from lib import logging as oclog
from lib.atomic import write_text

# 固定行为开关：与微信凭证无关，任何启动都写。
BEHAVIOR_FLAGS = """# Hermes 行为开关 - 由 oc-entrypoint 渲染

# 绕过 Hermes user pairing 流程（本地部署无交互 CLI 跑 approve）
GATEWAY_ALLOW_ALL_USERS=true

# Weixin platform policy：必须显式 open，否则未授权 DM 一律拒
WEIXIN_DM_POLICY=open
"""


def render(data_root: Path) -> str:
    """渲染 .env 到 data_root/.env，返回相对路径。"""
    data_root.mkdir(parents=True, exist_ok=True)
    body = BEHAVIOR_FLAGS
    weixin = _load_weixin_account(data_root)
    if weixin:
        body += _render_weixin_env(weixin)
    write_text(data_root / ".env", body)
    return ".env"


def _load_weixin_account(data_root: Path) -> Optional[dict]:
    """读 hermes 自管的第一个微信账号凭证。

    accounts/<account_id>.json 由 hermes 上游 qr_login 在扫码成功后写入，
    字段含 token / base_url / user_id。account_id 取文件名去 .json 后缀。
    返回 None 表示尚未扫码绑定。
    """
    accounts_dir = data_root / "weixin" / "accounts"
    if not accounts_dir.exists():
        return None
    for entry in sorted(accounts_dir.iterdir()):
        if not (entry.is_file() and entry.suffix == ".json"):
            continue
        try:
            data = json.loads(entry.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as e:
            # 单个账号文件损坏不阻断启动，记一行 stderr 后跳过。
            oclog.emit("render", "warn", f"weixin account file unreadable: {e}",
                       file=entry.name)
            continue
        if not isinstance(data, dict) or not data.get("token"):
            continue
        return {
            "account_id": entry.name[: -len(".json")],
            "token": data["token"],
            "base_url": (data.get("base_url") or "").strip(),
        }
    return None


def _render_weixin_env(weixin: dict) -> str:
    """把微信账号凭证渲染成 hermes gateway 启动需要的 WEIXIN_* env 段。"""
    lines = [
        "",
        "# Weixin 渠道凭证 - 由 oc-entrypoint 从 hermes 自管的",
        "# weixin/accounts/<id>.json 转译；hermes gateway 据此启用 weixin 平台。",
        f"WEIXIN_ACCOUNT_ID={weixin['account_id']}",
        f"WEIXIN_TOKEN={weixin['token']}",
    ]
    if weixin["base_url"]:
        lines.append(f"WEIXIN_BASE_URL={weixin['base_url']}")
    return "\n".join(lines) + "\n"
