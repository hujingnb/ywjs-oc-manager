# runtime/hermes/hermes-v2026.6.5/tests/test_merge_oc_locales.py
"""merge_oc_locales 构建期 catalog 合并的单元测试。"""
import sys
from pathlib import Path

import pytest
import yaml

# 沿用现有测试约定：把 patches/ 加入 sys.path 后扁平 import
sys.path.insert(0, str(Path(__file__).parent.parent / "patches"))
from merge_oc_locales import merge_lang, MergeConflict


def _overlay():
    # 构造最小 overlay：两个 key，含 en/zh 两语言
    return {
        "oc": {
            "run": {
                "queue_queued": {"en": "Queued.", "zh": "已加入队列。"},
                "kanban_done": {"en": "{tag}done", "zh": "{tag}已完成"},
            }
        }
    }


def test_merge_lang_injects_oc_namespace_for_zh():
    # 场景：把 overlay 的 zh 文案合并进 upstream zh.yaml；upstream 原有键保持不变，新增 oc 顶层块
    upstream = {"gateway": {"draining": "排空中"}}
    merged = merge_lang(upstream, _overlay(), "zh")
    assert merged["gateway"]["draining"] == "排空中"  # upstream 原键不动
    assert merged["oc"]["run"]["queue_queued"] == "已加入队列。"  # 注入 zh 文案
    assert merged["oc"]["run"]["kanban_done"] == "{tag}已完成"


def test_merge_lang_picks_correct_language_leaf():
    # 场景：合并 en 时取 en 叶子，不混入 zh
    merged = merge_lang({}, _overlay(), "en")
    assert merged["oc"]["run"]["queue_queued"] == "Queued."


def test_merge_lang_idempotent():
    # 场景：对已合并结果再合并一次，结果不变（幂等）
    once = merge_lang({}, _overlay(), "zh")
    twice = merge_lang(once, _overlay(), "zh")
    assert once == twice


def test_merge_lang_conflict_with_existing_oc_key_raises():
    # 场景：upstream 已存在同名 oc.* key 且值不同 → 冲突 fail-loud，禁止静默覆盖
    upstream = {"oc": {"run": {"queue_queued": "别的值"}}}
    with pytest.raises(MergeConflict):
        merge_lang(upstream, _overlay(), "zh")


def test_merge_lang_leaf_missing_target_language_raises():
    # 场景：overlay 叶子缺少目标语言（只给 en 却要合并 zh）→ fail-loud，暴露漏译
    overlay = {"oc": {"run": {"only_en": {"en": "Only English"}}}}
    with pytest.raises(MergeConflict):
        merge_lang({}, overlay, "zh")
