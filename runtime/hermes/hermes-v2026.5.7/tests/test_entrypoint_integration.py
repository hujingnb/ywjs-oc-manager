"""端到端：从一份完整 manifest + resources 出发，跑 oc-entrypoint 主流程到 phase 5。

oc-entrypoint phase 6 是 os.execvp 替换进程，不能在 pytest 中直接验证；
集成测试用 OC_TEST_NO_EXEC=1 让 oc-entrypoint 跳过 exec 直接退出 0。
"""

import json
import os
import subprocess
import sys
from pathlib import Path


def _setup_input(input_root: Path) -> None:
    # 准备一份最小可用的 manifest + resources。
    (input_root / "resources").mkdir(parents=True)
    (input_root / "resources" / "persona.md").write_text("# Persona\n\nP")
    (input_root / "resources" / "platform-rules.md").write_text("PLT")
    (input_root / "resources" / "organization-rules.md").write_text("ORG")
    (input_root / "resources" / "application-rules.md").write_text("APP")
    (input_root / "manifest.yaml").write_text("""
app: { id: x, name: X, model: m }
knowledge:
  runtime_base_url: http://manager-api:8080
  app_token: runtime-token
credentials:
  openai: { api_key: sk-x, base_url: http://x }
resources:
  persona: resources/persona.md
  rules:
    platform: resources/platform-rules.md
    organization: resources/organization-rules.md
    application: resources/application-rules.md
""")


def test_entrypoint_first_boot(tmp_path: Path) -> None:
    input_root = tmp_path / "input"
    data_root = tmp_path / "data"
    _setup_input(input_root)

    env = {
        **os.environ,
        "OC_TEST_NO_EXEC": "1",
        "OC_INPUT_DIR": str(input_root),
        "OC_DATA_DIR": str(data_root),
        "OC_IMAGE_VARIANT": "hermes-v2026.5.7",
    }
    source_script = Path(__file__).resolve().parent.parent / "oc-entrypoint.py"
    # 测试既支持源码目录布局，也支持 Docker 镜像内的 /usr/local/bin 安装布局。
    script = source_script if source_script.exists() else Path("/usr/local/bin/oc-entrypoint")
    r = subprocess.run([sys.executable, str(script)], env=env, capture_output=True, text=True)
    assert r.returncode == 0, r.stderr

    # phase 4 产物
    assert (data_root / "config.yaml").exists()
    assert (data_root / "SOUL.md").exists()
    assert (data_root / ".env").exists()
    assert (data_root / "skills" / "oc-kb" / "SKILL.md").exists()

    # phase 5 写下 .oc-state.json
    state = json.loads((data_root / ".oc-state.json").read_text())
    assert state["image_variant"] == "hermes-v2026.5.7"
    assert state["last_migrate_from"] is None
