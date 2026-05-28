#!/usr/bin/env python3
"""hermes-v2026.5.16 ENTRYPOINT。

命名：oc- 前缀取自项目名 oc-manager，标识注入 hermes runtime 镜像、供容器内调用的运维 CLI
（区别于 hermes 上游自带命令）；后缀 entrypoint = 容器入口（init 编排）。

phase 1 load manifest → 2 load state → 3 migrate → 4 render → 5 commit state → 6 exec hermes。
任何 phase 失败统一退出 1；详细错误通过 lib.logging.emit 写 stderr JSON。

测试模式：OC_TEST_NO_EXEC=1 时 phase 6 跳过 execvp 直接退出 0。
"""

from __future__ import annotations

import datetime as _dt
import hashlib
import os
import sys
from pathlib import Path

# 让 import lib / renderer / migrator 走包内路径。
sys.path.insert(0, "/usr/local/lib/oc-entrypoint")
# 测试模式：脚本目录而非镜像安装目录。
if not Path("/usr/local/lib/oc-entrypoint").exists():
    sys.path.insert(0, str(Path(__file__).resolve().parent))

from lib import logging as oclog
from lib.atomic import write_text  # noqa: F401  (atomic 在 renderer 内部使用)
from lib.manifest import load as load_manifest, ManifestError
from lib.state import OcState, read_state, write_state
from renderer import render_config_yaml, render_env, render_skills, render_soul_md
from migrator import run as run_migration


def main() -> int:
    input_root = Path(os.environ.get("OC_INPUT_DIR", "/opt/oc-input"))
    data_root = Path(os.environ.get("OC_DATA_DIR", "/opt/data"))
    curr_variant = os.environ.get("OC_IMAGE_VARIANT", "unknown")

    # phase 1 load manifest
    try:
        manifest = load_manifest(input_root / "manifest.yaml")
    except (ManifestError, FileNotFoundError, OSError) as e:
        oclog.emit("load_manifest", "error", str(e))
        return 1
    _configure_knowledge_env(manifest)

    # phase 2 load state
    state = read_state(data_root)
    prev_variant = state.image_variant

    # phase 3 migrate
    migrate_from = None
    try:
        if run_migration(prev_variant, curr_variant, data_root) is not None:
            migrate_from = prev_variant
    except Exception as e:  # noqa: BLE001
        oclog.emit("migrate", "error", str(e), prev_variant=prev_variant, curr_variant=curr_variant)
        return 1

    # phase 4 render（每次都跑、幂等）
    outputs: list[str] = []
    try:
        outputs.append(render_config_yaml.render(manifest, data_root))
        outputs.append(render_env.render(data_root))
        outputs.append(render_soul_md.render(manifest, input_root, data_root))
        outputs.extend(render_skills.render(manifest, input_root, data_root))
    except Exception as e:  # noqa: BLE001
        oclog.emit("render", "error", str(e))
        return 1

    # phase 5 commit state
    state_to_write = OcState(
        image_variant=curr_variant,
        last_render_at=_dt.datetime.utcnow().replace(microsecond=0).isoformat() + "Z",
        last_migrate_from=migrate_from,
        manifest_sha256=_sha256((input_root / "manifest.yaml").read_bytes()),
        renderer_outputs=outputs,
    )
    try:
        write_state(data_root, state_to_write)
    except OSError as e:
        # state 写失败不阻断；下次启动按首次处理。
        oclog.emit("commit_state", "warn", str(e))

    # phase 6 exec hermes
    if os.environ.get("OC_TEST_NO_EXEC") == "1":
        return 0
    os.execvp("hermes", ["hermes", "gateway", "run"])
    return 1  # pragma: no cover


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _configure_knowledge_env(manifest) -> None:
    """把 manifest knowledge 配置注入 Hermes 进程环境，供 oc-kb 子命令使用。"""
    if manifest.knowledge_runtime_base_url and manifest.knowledge_app_token:
        os.environ["OC_KB_RUNTIME_BASE_URL"] = manifest.knowledge_runtime_base_url
        os.environ["OC_KB_APP_TOKEN"] = manifest.knowledge_app_token
        return
    os.environ.pop("OC_KB_RUNTIME_BASE_URL", None)
    os.environ.pop("OC_KB_APP_TOKEN", None)


if __name__ == "__main__":
    sys.exit(main())
