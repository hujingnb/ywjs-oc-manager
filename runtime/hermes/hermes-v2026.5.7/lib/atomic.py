"""原子写工具：先写临时文件再 rename，保证读者永远看到完整内容。"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Union


def write_text(target: Union[str, Path], content: str) -> None:
    """将 content 写入 target；中间过程不暴露半文件。

    target: 目标路径，父目录必须已存在
    content: 要写入的完整文本
    """
    target_path = Path(target)
    tmp_path = target_path.with_suffix(target_path.suffix + ".tmp")
    with open(tmp_path, "w", encoding="utf-8") as f:
        f.write(content)
        f.flush()
        os.fsync(f.fileno())
    os.replace(tmp_path, target_path)
