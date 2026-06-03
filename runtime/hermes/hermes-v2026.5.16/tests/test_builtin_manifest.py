"""验证 ensure_builtin_manifest：首次启动生成镜像内置 skill 清单。

规则：
- 首次启动时 skills/ 下无 .oc-managed 标记的目录视为镜像内置基线，写入清单；
- /opt/skills-builtin.json 已存在时不覆盖（后续启动 skills/ 已混入 managed/自创）。
"""

import json
from pathlib import Path

import pytest

# ensure_builtin_manifest 从 oc-entrypoint 同模块导入
from oc_entrypoint import ensure_builtin_manifest


def test_generates_builtin_manifest_on_first_boot(tmp_path: Path) -> None:
    """首次启动：skills/ 下有内置目录 a、b（无 .oc-managed）+ 一个 managed 的 c，
    生成的清单只含 ['a', 'b']，不含 c。"""
    data_root = tmp_path / "data"
    manifest_path = tmp_path / "skills-builtin.json"

    # 准备 skills/：a、b 是镜像内置（无标记），c 是 managed（有标记）
    skills_dir = data_root / "skills"
    for name in ("a", "b", "c"):
        (skills_dir / name).mkdir(parents=True)
    # c 带 .oc-managed 标记，视为 manager 安装的 skill
    (skills_dir / "c" / ".oc-managed").write_text('{"source":"version-skill"}')

    ensure_builtin_manifest(data_root, manifest_path)

    assert manifest_path.exists()
    data = json.loads(manifest_path.read_text())
    # 只记录无 .oc-managed 的内置目录，按字母排序
    assert data == {"builtin": ["a", "b"]}


def test_does_not_overwrite_existing_manifest(tmp_path: Path) -> None:
    """清单已存在时不覆盖——保护首次启动的基线，防止后续启动（skills/ 已混入 managed）重写。"""
    data_root = tmp_path / "data"
    manifest_path = tmp_path / "skills-builtin.json"

    # 预先写入已有清单
    original = {"builtin": ["original-skill"]}
    manifest_path.write_text(json.dumps(original) + "\n")

    # skills/ 下放一个新目录
    (data_root / "skills" / "new-skill").mkdir(parents=True)

    ensure_builtin_manifest(data_root, manifest_path)

    # 清单内容不变
    assert json.loads(manifest_path.read_text()) == original


def test_generates_empty_manifest_when_skills_dir_absent(tmp_path: Path) -> None:
    """skills/ 目录不存在（纯空镜像）时生成空 builtin 列表，不报错。"""
    data_root = tmp_path / "data"
    manifest_path = tmp_path / "skills-builtin.json"

    # data_root 存在但无 skills/ 子目录
    data_root.mkdir(parents=True)

    ensure_builtin_manifest(data_root, manifest_path)

    assert manifest_path.exists()
    assert json.loads(manifest_path.read_text()) == {"builtin": []}
