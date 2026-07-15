# tests/test_ocops_info_doctor.py
"""覆盖 info/doctor 核心函数：从镜像身份文件与 state 读出结构化快照。"""
import json
from pathlib import Path

from jsonschema import validate

from ocops import info, doctor


def test_collect_info_reads_image_file(tmp_path, monkeypatch, ocops_schema):
    # 正常路径：读 OC_INFO_FILE 指向的镜像身份 JSON，并补 oc_entrypoint_version
    f = tmp_path / "oc-image.json"
    f.write_text(json.dumps({"variant": "hermes-v0.18.2",
                             "hermes_upstream_ref": "abc", "built_at": "2026-05-29"}))
    monkeypatch.setenv("OC_INFO_FILE", str(f))
    got = info.collect_info()
    validate(got, ocops_schema("core/info.schema.json"))
    assert got["variant"] == "hermes-v0.18.2"
    assert got["oc_entrypoint_version"] == "1"


def test_collect_info_missing_file_raises(tmp_path, monkeypatch):
    # 边界：身份文件缺失/损坏时抛 OpsError(INTERNAL)，由 server 映射 500
    monkeypatch.setenv("OC_INFO_FILE", str(tmp_path / "missing.json"))
    from ocops.errors import OpsError
    try:
        info.collect_info()
        assert False, "应抛 OpsError"
    except OpsError as e:
        assert e.code == "INTERNAL"


def test_collect_doctor_reports_state_and_status(tmp_path, monkeypatch, ocops_schema):
    # 正常路径：doctor 读 state 快照，hermes 进程不在时 hermes_status=stopped、issues 为空
    monkeypatch.setenv("OC_DATA_DIR", str(tmp_path))
    monkeypatch.setenv("OC_IMAGE_VARIANT", "hermes-v0.18.2")
    got = doctor.collect_doctor()
    validate(got, ocops_schema("core/doctor.schema.json"))
    assert got["variant"] == "hermes-v0.18.2"
    assert got["hermes_status"] in ("running", "stopped", "unknown")
    assert got["issues"] == []
