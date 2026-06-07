"""ocops-contract 与 ocops.server 路由表的一致性测试。"""

from __future__ import annotations

import json
import re
from pathlib import Path

from jsonschema.validators import validator_for


VARIANT_ROOT = Path(__file__).resolve().parents[1]
SOURCE_CONTRACT_ROOT = Path(__file__).resolve().parents[2] / "ocops-contract"
IMAGE_CONTRACT_ROOT = Path("/usr/local/lib/ocops/contract")
CONTRACT_ROOT = IMAGE_CONTRACT_ROOT if IMAGE_CONTRACT_ROOT.exists() else SOURCE_CONTRACT_ROOT
SPEC = CONTRACT_ROOT / "SPEC.md"
SOURCE_SERVER = VARIANT_ROOT / "ocops" / "server.py"
IMAGE_SERVER = Path("/usr/local/lib/ocops/server.py")
SERVER = IMAGE_SERVER if IMAGE_SERVER.exists() else SOURCE_SERVER


def _server_routes() -> list[tuple[str, str]]:
    """从 server.py 的 Route(...) 声明中提取 method/path 对。"""
    source = SERVER.read_text(encoding="utf-8")
    route_re = re.compile(r'Route\("([^"]+)"\s*,\s*[^,\n\)]+(?:,\s*methods=\[(.*?)\])?')
    out: list[tuple[str, str]] = []
    for path, methods_s in route_re.findall(source):
        methods = re.findall(r'"([A-Z]+)"', methods_s) if methods_s else ["GET"]
        for method in methods:
            out.append((method, path))
    return out


def test_all_ocops_routes_are_documented_in_contract() -> None:
    spec = SPEC.read_text(encoding="utf-8")
    missing = [
        f"{method} {path}"
        for method, path in _server_routes()
        if f"`{method}` | `{path}`" not in spec
    ]
    assert missing == []


def test_contract_schema_references_exist_and_are_valid() -> None:
    spec = SPEC.read_text(encoding="utf-8")
    refs = sorted(set(re.findall(r'`(schema/[^`*]+?\.schema\.json)`', spec)))
    missing = [ref for ref in refs if not (CONTRACT_ROOT / ref).exists()]
    assert missing == []

    for schema_path in (CONTRACT_ROOT / "schema").rglob("*.json"):
        schema = json.loads(schema_path.read_text(encoding="utf-8"))
        validator_for(schema).check_schema(schema)
