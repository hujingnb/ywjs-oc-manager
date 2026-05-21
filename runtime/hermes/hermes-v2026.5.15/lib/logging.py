"""stderr 单行 JSON 日志，与 spec §6.4 协议一致。

调用方：emit("render", "info", "render_skills ok", file="abc.md")
输出（stderr）：{"phase":"render","level":"info","msg":"render_skills ok","detail":{"file":"abc.md"}}
"""

from __future__ import annotations

import json
import sys
from typing import Any


def emit(phase: str, level: str, msg: str, **detail: Any) -> None:
    """写一行 JSON 到 stderr。"""
    record = {"phase": phase, "level": level, "msg": msg}
    if detail:
        record["detail"] = detail
    sys.stderr.write(json.dumps(record, ensure_ascii=False) + "\n")
    sys.stderr.flush()
