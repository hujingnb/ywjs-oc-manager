"""patch 使用的 oc.* key 与 overlay 定义的 key 双向一致性守卫测试。"""
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent / "patches"))
from check_oc_i18n_consistency import (
    extract_patch_keys, extract_overlay_keys, check_consistency, ConsistencyError,
    detect_duplicate_keys,
)


def test_detect_duplicate_keys_finds_collision():
    # 场景：同层两个同名叶子 key（yaml.safe_load 会静默后者覆盖前者）→ 必须被检出
    text = (
        "oc:\n  run:\n"
        "    foo:\n      en: \"A\"\n      zh: \"甲\"\n"
        "    foo:\n      en: \"B\"\n      zh: \"乙\"\n"
        "    bar:\n      en: \"C\"\n      zh: \"丙\"\n"
    )
    assert detect_duplicate_keys(text) == ["foo"]


def test_detect_duplicate_keys_none_when_unique():
    # 场景：无重复叶子 key → 返回空列表
    text = "oc:\n  run:\n    foo:\n      en: \"A\"\n      zh: \"甲\"\n"
    assert detect_duplicate_keys(text) == []


def test_extract_patch_keys_from_replacement_strings():
    # 场景：从补丁 new 串里抽出所有 t("oc.X") 的 key
    repls = [("old1", 't("oc.run.queue_queued")'),
             ("old2", 't("oc.run.kanban_done", tag=tag)')]
    assert extract_patch_keys(repls) == {"oc.run.queue_queued", "oc.run.kanban_done"}


def test_extract_overlay_keys_requires_both_langs():
    # 场景：overlay 叶子必须同时含 en 和 zh，缺一即非法
    overlay = {"oc": {"run": {"a": {"en": "A", "zh": "甲"}}}}
    assert extract_overlay_keys(overlay) == {"oc.run.a"}
    bad = {"oc": {"run": {"a": {"en": "A"}}}}  # 缺 zh
    with pytest.raises(ConsistencyError):
        extract_overlay_keys(bad)


def test_check_consistency_detects_missing_and_orphan():
    # 场景：patch 用了 overlay 没有的 key（missing），或 overlay 有 patch 没用的 key（orphan）→ 报错
    patch_keys = {"oc.run.a", "oc.run.b"}
    overlay_keys = {"oc.run.a", "oc.run.c"}
    with pytest.raises(ConsistencyError) as e:
        check_consistency(patch_keys, overlay_keys)
    assert "oc.run.b" in str(e.value)  # patch 用了但 overlay 缺
    assert "oc.run.c" in str(e.value)  # overlay 有但 patch 未用


def test_check_consistency_ok_when_equal():
    # 场景：两侧 key 集合相等 → 通过，不抛
    check_consistency({"oc.run.a"}, {"oc.run.a"})
