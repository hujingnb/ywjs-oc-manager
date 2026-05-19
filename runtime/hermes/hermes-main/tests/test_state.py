"""验证 .oc-state.json 读写：首次启动 prev=None；写后再读得到相同结构。"""

from pathlib import Path
from lib.state import read_state, write_state, OcState


def test_read_missing_returns_empty(tmp_path: Path) -> None:
    # 首次启动场景：.oc-state.json 不存在，应返回 prev_variant=None 的空对象。
    s = read_state(tmp_path)
    assert s.image_variant is None
    assert s.manifest_sha256 is None


def test_write_then_read_roundtrip(tmp_path: Path) -> None:
    # 写入后再读应得到等价的 OcState。
    s = OcState(
        image_variant="hermes-main",
        last_render_at="2026-05-19T00:00:00Z",
        last_migrate_from=None,
        manifest_sha256="ab12cd",
        renderer_outputs=["config.yaml", "SOUL.md"],
    )
    write_state(tmp_path, s)
    s2 = read_state(tmp_path)
    assert s2 == s


def test_corrupt_state_returns_empty(tmp_path: Path) -> None:
    # 文件损坏视为未知，等同首次启动；不应抛异常打断启动流程。
    (tmp_path / ".oc-state.json").write_text("{not json")
    s = read_state(tmp_path)
    assert s.image_variant is None
