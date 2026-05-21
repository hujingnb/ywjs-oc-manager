#!/usr/bin/env python3
"""输出诊断快照。stdout 单行 JSON；spec §7。"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, "/usr/local/lib/oc-entrypoint")
if not Path("/usr/local/lib/oc-entrypoint").exists():
    sys.path.insert(0, str(Path(__file__).resolve().parent))

from lib.state import read_state


def main() -> int:
    data_root = Path(os.environ.get("OC_DATA_DIR", "/opt/data"))
    variant = os.environ.get("OC_IMAGE_VARIANT", "unknown")
    state = read_state(data_root)

    hermes_pid = None
    hermes_status = "unknown"
    try:
        out = subprocess.run(["pgrep", "-f", "hermes gateway"], capture_output=True, text=True, timeout=5)
        if out.returncode == 0 and out.stdout.strip():
            hermes_pid = int(out.stdout.splitlines()[0])
            hermes_status = "running"
        else:
            hermes_status = "stopped"
    except (FileNotFoundError, subprocess.TimeoutExpired):
        hermes_status = "unknown"

    snapshot = {
        "variant": variant,
        "last_render_at": state.last_render_at,
        "manifest_sha256": state.manifest_sha256,
        "hermes_pid": hermes_pid,
        "hermes_status": hermes_status,
        "issues": [],
    }
    sys.stdout.write(json.dumps(snapshot, ensure_ascii=False) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
