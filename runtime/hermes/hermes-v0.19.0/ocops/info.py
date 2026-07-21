"""镜像身份信息：读取构建期写入的 /etc/oc-image.json。从 oc-info.py 抽取核心逻辑。"""
from __future__ import annotations

import json
import os
from pathlib import Path

from ocops.errors import OpsError


def collect_info() -> dict:
    """读取镜像身份 JSON 并补 oc_entrypoint_version；文件缺失/损坏抛 OpsError(INTERNAL)。"""
    info_path = Path(os.environ.get("OC_INFO_FILE", "/etc/oc-image.json"))
    try:
        raw = json.loads(info_path.read_text())
    except (OSError, json.JSONDecodeError) as e:
        raise OpsError("INTERNAL", f"读取镜像身份失败: {e}") from e
    raw["oc_entrypoint_version"] = "1"
    return raw
