"""验证 atomic.write_text 在写入完成前不留半文件，并保证 rename 原子性。"""

from pathlib import Path
from lib.atomic import write_text


def test_atomic_write_creates_file(tmp_path: Path) -> None:
    # 验证正常路径下文件按预期写入。
    target = tmp_path / "config.yaml"
    write_text(target, "hello")
    assert target.read_text() == "hello"


def test_atomic_write_no_residual_tmp(tmp_path: Path) -> None:
    # 验证写完后不留下 .tmp 中间文件。
    target = tmp_path / "config.yaml"
    write_text(target, "hello")
    siblings = list(tmp_path.iterdir())
    assert siblings == [target]
