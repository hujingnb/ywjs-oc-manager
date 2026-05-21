"""测试公共 fixture，提供干净的临时目录。"""

from pathlib import Path
import sys

# 让测试能 import lib / renderer / migrator
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

import pytest


@pytest.fixture
def tmp_data(tmp_path) -> Path:
    """模拟 /opt/data 的临时目录。"""
    return tmp_path / "data"


@pytest.fixture
def tmp_input(tmp_path) -> Path:
    """模拟 /opt/oc-input 的临时目录。"""
    return tmp_path / "input"
