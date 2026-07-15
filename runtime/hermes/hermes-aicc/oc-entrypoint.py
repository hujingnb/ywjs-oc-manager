#!/usr/bin/env python3
"""hermes-v2026.7.1 ENTRYPOINT。

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
# entrypoint_helpers.py 辅助模块落点 /usr/local/lib/（见 Dockerfile COPY），与 oc-entrypoint
# 子目录不同级。运行时容器入口不带 PYTHONPATH（仅构建期自检设了 PYTHONPATH=/usr/local/lib），
# 故须在此显式把 /usr/local/lib 加入 sys.path，否则 `import entrypoint_helpers` 在容器启动即崩溃。
sys.path.insert(0, "/usr/local/lib")
# AICC 不可变工具策略位于 Hermes 安装目录；entrypoint 需在渲染前校验 manifest 能力。
sys.path.insert(0, "/usr/local/lib/hermes-agent")
# 测试模式：脚本目录而非镜像安装目录。
if not Path("/usr/local/lib/oc-entrypoint").exists():
    sys.path.insert(0, str(Path(__file__).resolve().parent))

from lib import logging as oclog
from lib.atomic import write_text  # noqa: F401  (atomic 在 renderer 内部使用)
from lib.manifest import load as load_manifest, ManifestError
from lib.state import OcState, read_state, write_state
from renderer import render_config_yaml, render_env, render_skills, render_soul_md
from migrator import run as run_migration
from entrypoint_helpers import ensure_builtin_manifest, sync_aicc_builtin_skills
from aicc_tools.policy import require_manifest_capabilities


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
    try:
        capabilities = require_manifest_capabilities(manifest.capabilities)
    except ValueError as e:
        oclog.emit("load_manifest", "error", str(e))
        return 1
    # model_tools.py 的定义过滤和 dispatcher 都读取该值；每次启动由 manifest 覆盖，
    # 不可由 Pod 的历史临时状态放宽。
    os.environ["OC_AICC_CAPABILITIES"] = ",".join(sorted(capabilities))
    _configure_knowledge_env(manifest)
    _configure_web_publish_env(manifest)

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

    # render 前：把镜像层的客服 Skill 同步到可能被 emptyDir 覆盖的共享卷，并校验其
    # frontmatter 能力不能超过本次 manifest 授权；之后再记录内置 Skill 基线。
    # 默认写入 /opt/data/skills/.bundled_manifest，供 hermes/oc-ops/ops sidecar 跨容器共享；
    # OC_BUILTIN_MANIFEST 仅用于测试或调试覆盖。
    builtin_manifest_override = os.environ.get("OC_BUILTIN_MANIFEST")
    builtin_manifest_path = Path(builtin_manifest_override) if builtin_manifest_override else None
    try:
        sync_aicc_builtin_skills(data_root, capabilities)
        ensure_builtin_manifest(data_root, builtin_manifest_path)
    except Exception as e:  # noqa: BLE001
        oclog.emit("sync_builtin_skills", "error", str(e))
        return 1

    # phase 4 render（每次都跑、幂等）
    outputs: list[str] = []
    try:
        outputs.append(render_config_yaml.render(manifest, data_root))
        outputs.append(render_env.render(data_root, _runtime_cli_env()))
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


def _runtime_cli_env() -> dict[str, str]:
    """返回需要写入 .env 的 runtime CLI 配置。

    这些值已经由 _configure_*_env 从 manifest 解析到进程环境；这里再取一遍，
    让 execute_code 等不继承 gateway 环境的子执行器也能通过 CLI 兜底读取。
    """
    keys = (
        "OC_KB_RUNTIME_BASE_URL",
        "OC_KB_APP_TOKEN",
        "OC_PUBLISH_RUNTIME_BASE_URL",
        "OC_PUBLISH_APP_TOKEN",
    )
    return {key: os.environ.get(key, "") for key in keys if os.environ.get(key, "")}


def _configure_web_publish_env(manifest) -> None:
    """把 manifest web_publish 配置注入 Hermes 进程环境，供 oc-publish 子命令使用。

    runtime_base_url 或 app_token 任一为空时视为企业未开通发布能力，清除环境变量。
    """
    if manifest.web_publish_runtime_base_url and manifest.web_publish_app_token:
        os.environ["OC_PUBLISH_RUNTIME_BASE_URL"] = manifest.web_publish_runtime_base_url
        os.environ["OC_PUBLISH_APP_TOKEN"] = manifest.web_publish_app_token
        return
    os.environ.pop("OC_PUBLISH_RUNTIME_BASE_URL", None)
    os.environ.pop("OC_PUBLISH_APP_TOKEN", None)


if __name__ == "__main__":
    sys.exit(main())
