"""端到端：从一份完整 manifest + resources 出发，跑 oc-entrypoint 主流程到 phase 5。

oc-entrypoint phase 6 是 os.execvp 替换进程，不能在 pytest 中直接验证；
集成测试用 OC_TEST_NO_EXEC=1 让 oc-entrypoint 跳过 exec 直接退出 0。
"""

import json
import os
import shutil
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
capabilities: [knowledge.read, web.search, skills.read, vision.read]
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
        "OC_IMAGE_VARIANT": "hermes-v2026.7.1",
        "OC_BUILTIN_SKILLS_DIR": str(Path(__file__).resolve().parent.parent / "skills"),
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
    # AICC 的知识能力来自镜像只读层，entrypoint 会复制进 emptyDir 供 Hermes 实际扫描。
    assert not (data_root / "skills" / "oc-kb" / "SKILL.md").exists()
    names = sorted(path.name for path in (data_root / "skills").iterdir() if path.is_dir())
    assert names == ["aicc-customer-answer", "aicc-lead-analysis", "aicc-safe-web-research"]
    env_body = (data_root / ".env").read_text()
    assert "OC_KB_RUNTIME_BASE_URL=http://manager-api:8080" in env_body
    assert "OC_KB_APP_TOKEN=runtime-token" in env_body

    # phase 5 写下 .oc-state.json
    state = json.loads((data_root / ".oc-state.json").read_text())
    assert state["image_variant"] == "hermes-v2026.7.1"
    assert state["last_migrate_from"] is None

    # render 前生成内置 skill 共享基线，三个内置 Skill 都必须被记录。
    builtin_manifest = data_root / "skills" / ".bundled_manifest"
    assert builtin_manifest.exists()
    assert len(builtin_manifest.read_text(encoding="utf-8").splitlines()) == 3


def test_entrypoint_rejects_missing_aicc_capabilities(tmp_path: Path) -> None:
    # AICC manifest 缺失最小能力集合时必须在启动前失败关闭，不能回退为上游默认工具面。
    input_root = tmp_path / "input"
    data_root = tmp_path / "data"
    _setup_input(input_root)
    manifest_path = input_root / "manifest.yaml"
    manifest_path.write_text(manifest_path.read_text().replace(
        "capabilities: [knowledge.read, web.search, skills.read, vision.read]\n", "",
    ))

    env = {
        **os.environ,
        "OC_TEST_NO_EXEC": "1",
        "OC_INPUT_DIR": str(input_root),
        "OC_DATA_DIR": str(data_root),
        "OC_IMAGE_VARIANT": "hermes-aicc",
    }
    source_script = Path(__file__).resolve().parent.parent / "oc-entrypoint.py"
    result = subprocess.run([sys.executable, str(source_script)], env=env, capture_output=True, text=True)

    assert result.returncode == 1
    assert "AICC_MANIFEST_CAPABILITY_MISSING" in result.stderr


def test_entrypoint_rejects_builtin_skill_capability_outside_manifest(tmp_path: Path) -> None:
    # 即便恶意 Skill 已在镜像源目录，启动也必须在复制到 emptyDir 之前因越权能力失败关闭。
    input_root = tmp_path / "input"
    data_root = tmp_path / "data"
    source_root = tmp_path / "builtin-skills"
    _setup_input(input_root)
    shutil.copytree(Path(__file__).resolve().parent.parent / "skills", source_root)
    target = source_root / "aicc-customer-answer" / "SKILL.md"
    target.write_text(target.read_text(encoding="utf-8").replace(
        "- knowledge.read", "- terminal.execute",
    ), encoding="utf-8")

    env = {
        **os.environ,
        "OC_TEST_NO_EXEC": "1",
        "OC_INPUT_DIR": str(input_root),
        "OC_DATA_DIR": str(data_root),
        "OC_IMAGE_VARIANT": "hermes-aicc",
        "OC_BUILTIN_SKILLS_DIR": str(source_root),
    }
    source_script = Path(__file__).resolve().parent.parent / "oc-entrypoint.py"
    result = subprocess.run([sys.executable, str(source_script)], env=env, capture_output=True, text=True)

    assert result.returncode == 1
    assert "AICC_SKILL_CAPABILITY_FORBIDDEN: terminal.execute" in result.stderr
    assert not (data_root / "skills" / "aicc-customer-answer").exists()
