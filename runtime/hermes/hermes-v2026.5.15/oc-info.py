#!/usr/bin/env python3
"""输出镜像身份。stdout 单行 JSON；spec §7。

读取 build 阶段写入的 /etc/oc-image.json。
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path


def main() -> int:
    info_path = Path(os.environ.get("OC_INFO_FILE", "/etc/oc-image.json"))
    try:
        raw = json.loads(info_path.read_text())
    except (OSError, json.JSONDecodeError) as e:
        sys.stderr.write(json.dumps({"phase": "oc-info", "level": "error", "msg": str(e)}) + "\n")
        return 1
    raw["oc_entrypoint_version"] = "1"
    sys.stdout.write(json.dumps(raw, ensure_ascii=False) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
