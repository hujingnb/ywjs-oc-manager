#!/usr/bin/env python3
"""输出诊断快照。stdout 单行 JSON；spec §7。

命名：oc- 前缀取自项目名 oc-manager，标识注入 hermes runtime 镜像、供容器内调用的运维 CLI
（区别于 hermes 上游自带命令）；后缀 doctor = 运行时诊断快照。

薄 shim：读取逻辑已下沉至 ocops.doctor.collect_doctor，本文件仅负责 stdout 输出与退出码。
"""

from __future__ import annotations

import sys
from pathlib import Path


def main() -> int:
    # CLI shim：复用 ocops.doctor 核心逻辑，保留 stdout 单行 JSON 的对外命令契约。
    import json
    sys.path.insert(0, "/usr/local/lib")  # 镜像内 ocops 装在 /usr/local/lib/ocops
    sys.path.insert(0, str(Path(__file__).resolve().parent))  # 本地自检 fallback
    from ocops.doctor import collect_doctor
    snapshot = collect_doctor()
    sys.stdout.write(json.dumps(snapshot, ensure_ascii=False) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
