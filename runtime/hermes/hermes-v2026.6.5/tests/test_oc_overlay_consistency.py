"""patch 使用的 oc.* key 与 overlay 定义的 key 双向一致性守卫测试。"""
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent / "patches"))
from check_oc_i18n_consistency import (
    extract_patch_keys, extract_overlay_keys, check_consistency, ConsistencyError,
)


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
