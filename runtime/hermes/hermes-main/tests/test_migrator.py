"""验证 migrator dispatch：未知 prev_variant → 静默跳过；找到 from_X.py → 调其 run()。"""

from pathlib import Path
from migrator import run as run_migration


def test_no_prev_skips(tmp_data: Path) -> None:
    # 首次启动 prev=None，应直接返回 None 不抛。
    result = run_migration(prev_variant=None, curr_variant="hermes-main", data_root=tmp_data)
    assert result is None


def test_same_variant_skips(tmp_data: Path) -> None:
    # prev == curr，跳过迁移。
    result = run_migration(prev_variant="hermes-main", curr_variant="hermes-main", data_root=tmp_data)
    assert result is None


def test_unknown_prev_raises(tmp_data: Path) -> None:
    # 切到未实现 from_<prev>.py 的迁移路径应抛 NotImplementedError，
    # 由 oc-entrypoint 转化为退出码 1。
    import pytest
    with pytest.raises(NotImplementedError):
        run_migration(prev_variant="hermes-experimental", curr_variant="hermes-main", data_root=tmp_data)
