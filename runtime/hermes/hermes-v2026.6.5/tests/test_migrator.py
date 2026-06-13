"""验证基于文件检测的 migrator dispatch：首启、同版本、跨版本迁移与幂等。"""

from pathlib import Path

from migrator import run as run_migration


def test_no_prev_skips(tmp_data: Path) -> None:
    # 首次启动：目录无版本标记（prev=None），无数据可迁移，返回 None 跳过。
    result = run_migration(prev_variant=None, curr_variant="hermes-v2026.6.5", data_root=tmp_data)
    assert result is None


def test_same_variant_skips(tmp_data: Path) -> None:
    # 目录记录版本与运行镜像版本一致（同版本重启），无需迁移，跳过。
    result = run_migration(
        prev_variant="hermes-v2026.6.5",
        curr_variant="hermes-v2026.6.5",
        data_root=tmp_data,
    )
    assert result is None


def test_different_variant_returns_summary(tmp_data: Path) -> None:
    # 目录记录版本与运行镜像版本不同（升级场景）：做文件检测，当前无不兼容结构，
    # 返回 no-op 摘要并回显实际 prev/curr，不硬编码版本、不依赖来源版本模块。
    result = run_migration(
        prev_variant="hermes-v2026.5.7",
        curr_variant="hermes-v2026.6.5",
        data_root=tmp_data,
    )
    assert result == {
        "from": "hermes-v2026.5.7",
        "to": "hermes-v2026.6.5",
        "mode": "noop",
        "migrated": [],
    }


def test_downgrade_returns_summary(tmp_data: Path) -> None:
    # 降级场景（curr 比 prev 旧）走同一套文件检测逻辑，不区分方向，同样返回摘要。
    result = run_migration(
        prev_variant="hermes-v2026.6.5",
        curr_variant="hermes-v2026.5.7",
        data_root=tmp_data,
    )
    assert result == {
        "from": "hermes-v2026.6.5",
        "to": "hermes-v2026.5.7",
        "mode": "noop",
        "migrated": [],
    }


def test_arbitrary_unknown_variant_does_not_raise(tmp_data: Path) -> None:
    # 任意未知来源版本不再 fail-fast（已无按版本名 dispatch 的模块查找）：
    # 文件检测无命中即视为已兼容，返回 no-op 摘要而非抛 NotImplementedError。
    result = run_migration(
        prev_variant="hermes-experimental",
        curr_variant="hermes-v2026.6.5",
        data_root=tmp_data,
    )
    assert result is not None
    assert result["mode"] == "noop"
