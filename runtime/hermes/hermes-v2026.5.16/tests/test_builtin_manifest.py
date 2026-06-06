"""验证 ensure_builtin_manifest：首次启动生成镜像内置 skill 基线。

规则：
- 首次启动时 skills/ 下无 .oc-managed 标记且含 SKILL.md 的目录视为镜像内置 skill；
- 基线写到 /opt/data/skills/.bundled_manifest，供 hermes 与 ops sidecar 共享；
- 基线已存在时不覆盖（后续启动 skills/ 已混入 managed/自创）。
"""

from pathlib import Path

# ensure_builtin_manifest 从 oc-entrypoint 同模块导入
from oc_entrypoint import ensure_builtin_manifest


def test_generates_bundled_manifest_on_first_boot(tmp_path: Path) -> None:
    """首次启动：含 SKILL.md 且无 .oc-managed 的 skill 写入共享 .bundled_manifest。"""
    data_root = tmp_path / "data"

    # 准备 skills/：a 与 nested/b 是镜像内置；c 带 .oc-managed，应跳过。
    skills_dir = data_root / "skills"
    (skills_dir / "a").mkdir(parents=True)
    (skills_dir / "a" / "SKILL.md").write_text("---\nname: alpha\n---\n", encoding="utf-8")
    (skills_dir / "nested" / "b").mkdir(parents=True)
    (skills_dir / "nested" / "b" / "SKILL.md").write_text("---\nname: beta\n---\n", encoding="utf-8")
    (skills_dir / "c").mkdir(parents=True)
    (skills_dir / "c" / "SKILL.md").write_text("---\nname: gamma\n---\n", encoding="utf-8")
    (skills_dir / "c" / ".oc-managed").write_text('{"source":"version-skill"}')

    ensure_builtin_manifest(data_root)

    manifest_path = skills_dir / ".bundled_manifest"
    assert manifest_path.exists()
    lines = manifest_path.read_text(encoding="utf-8").splitlines()
    assert len(lines) == 2
    assert lines[0].startswith("alpha:")
    assert lines[1].startswith("beta:")
    assert "gamma" not in manifest_path.read_text(encoding="utf-8")


def test_does_not_overwrite_existing_manifest(tmp_path: Path) -> None:
    """共享基线已存在时不覆盖，防止后续启动时把运行期 skill 混入内置基线。"""
    data_root = tmp_path / "data"
    manifest_path = data_root / "skills" / ".bundled_manifest"
    manifest_path.parent.mkdir(parents=True)

    # 预先写入已有清单
    original = "original-skill:deadbeef\n"
    manifest_path.write_text(original, encoding="utf-8")

    # skills/ 下放一个新目录
    (data_root / "skills" / "new-skill").mkdir()
    (data_root / "skills" / "new-skill" / "SKILL.md").write_text(
        "---\nname: new-skill\n---\n", encoding="utf-8")

    ensure_builtin_manifest(data_root)

    # 清单内容不变
    assert manifest_path.read_text(encoding="utf-8") == original


def test_generates_empty_manifest_when_skills_dir_absent(tmp_path: Path) -> None:
    """skills/ 目录不存在时创建共享空基线，不阻断首次启动。"""
    data_root = tmp_path / "data"

    # data_root 存在但无 skills/ 子目录
    data_root.mkdir(parents=True)

    ensure_builtin_manifest(data_root)

    manifest_path = data_root / "skills" / ".bundled_manifest"
    assert manifest_path.exists()
    assert manifest_path.read_text(encoding="utf-8") == ""
