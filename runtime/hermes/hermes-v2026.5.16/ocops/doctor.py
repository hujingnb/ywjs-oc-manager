"""运行时诊断快照：state 渲染信息 + hermes 进程状态。从 oc-doctor.py 抽取核心逻辑。"""
from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

# 复用 oc-entrypoint 的 lib.state（镜像内装在 /usr/local/lib/oc-entrypoint）。
sys.path.insert(0, "/usr/local/lib/oc-entrypoint")
if not Path("/usr/local/lib/oc-entrypoint").exists():
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from lib.state import read_state  # noqa: E402


def collect_doctor() -> dict:
    """读 state 快照并探测 hermes gateway 进程；返回诊断 dict（永不抛业务错误）。"""
    data_root = Path(os.environ.get("OC_DATA_DIR", "/opt/data"))
    variant = os.environ.get("OC_IMAGE_VARIANT", "unknown")
    state = read_state(data_root)

    hermes_pid = None
    hermes_status = "unknown"
    try:
        out = subprocess.run(["pgrep", "-f", "hermes gateway"],
                             capture_output=True, text=True, timeout=5)
        if out.returncode == 0 and out.stdout.strip():
            hermes_pid = int(out.stdout.splitlines()[0])
            hermes_status = "running"
        else:
            hermes_status = "stopped"
    except (FileNotFoundError, subprocess.TimeoutExpired):
        hermes_status = "unknown"

    return {
        "variant": variant,
        "last_render_at": state.last_render_at,
        "manifest_sha256": state.manifest_sha256,
        "hermes_pid": hermes_pid,
        "hermes_status": hermes_status,
        "issues": [],
    }
