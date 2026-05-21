"""验证 migrator dispatch 的首启、同版本、历史重命名和未知来源路径。"""

from pathlib import Path

import pytest

from migrator import _migration_module_suffix, run as run_migration


def test_no_prev_skips(tmp_data: Path) -> None:
    # 首次启动 prev=None，应直接返回 None 不抛。
    result = run_migration(prev_variant=None, curr_variant="hermes-v2026.5.7", data_root=tmp_data)
    assert result is None


def test_same_variant_skips(tmp_data: Path) -> None:
    # prev == curr 表示同一个版本重启，跳过迁移。
    result = run_migration(
        prev_variant="hermes-v2026.5.7",
        curr_variant="hermes-v2026.5.7",
        data_root=tmp_data,
    )
    assert result is None


def test_legacy_hermes_main_noop_returns_summary(tmp_data: Path) -> None:
    # hermes-main 是本 variant 的历史目录名，只记录 no-op 摘要，不改数据文件。
    result = run_migration(
        prev_variant="hermes-main",
        curr_variant="hermes-v2026.5.7",
        data_root=tmp_data,
    )
    assert result == {
        "from": "hermes-main",
        "to": "hermes-v2026.5.7",
        "mode": "noop_rename",
    }


def test_unknown_prev_raises(tmp_data: Path) -> None:
    # 未实现迁移模块的来源版本必须 fail-fast，避免错误复用不兼容数据。
    with pytest.raises(NotImplementedError):
        run_migration(
            prev_variant="hermes-experimental",
            curr_variant="hermes-v2026.5.7",
            data_root=tmp_data,
        )


def test_migration_module_suffix_replaces_dash_and_dot() -> None:
    # 版本号包含 "." 时也要生成合法 Python module 名。
    assert _migration_module_suffix("hermes-v2026.5.7") == "hermes_v2026_5_7"
