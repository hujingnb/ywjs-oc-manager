"""测试公共 fixture，提供干净的临时目录与契约 schema 加载器。"""

import json
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


@pytest.fixture
def ocops_schema():
    """加载 ocops-contract 下的 JSON schema，优先使用镜像内安装路径。"""
    image_root = Path("/usr/local/lib/ocops/contract/schema")
    source_root = HERE.parent.parent / "ocops-contract" / "schema"
    root = image_root if image_root.exists() else source_root

    def load(name: str) -> dict:
        return json.loads((root / name).read_text(encoding="utf-8"))

    return load
