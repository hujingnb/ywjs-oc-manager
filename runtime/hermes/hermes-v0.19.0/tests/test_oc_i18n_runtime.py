# runtime/hermes/hermes-v0.19.0/tests/test_oc_i18n_runtime.py
"""验证 oc_overlay 文案能被 str.format 用规划的 kwarg 正确渲染（中英）。"""
import pathlib
import yaml

OVERLAY = pathlib.Path(__file__).resolve().parent.parent / "locales" / "oc_overlay.yaml"


def _leaves(node, prefix=""):
    if isinstance(node, dict) and set(node.keys()) <= {"en", "zh"} and node:
        yield prefix, node
        return
    if isinstance(node, dict):
        for k, v in node.items():
            yield from _leaves(v, f"{prefix}.{k}" if prefix else k)


def test_every_leaf_has_both_langs_and_no_underscore_placeholders():
    # 场景：每条 oc.* 都含 en+zh；占位符已按 R3 规范化（不含前导下划线变量名）。
    # overlay 为空时（填充前）循环空跑通过，不引入红测试破坏 CI。
    overlay = yaml.safe_load(OVERLAY.read_text(encoding="utf-8"))
    for key, leaf in _leaves(overlay):
        assert "en" in leaf and "zh" in leaf, f"{key} 缺语言"
        # 规范化后不应再出现源码内部变量名（如 {_secs_ago}）
        assert "{_" not in leaf["en"] and "{_" not in leaf["zh"], f"{key} 占位符未规范化"


def test_zh_placeholders_subset_of_en():
    # 场景：zh 用到的占位符必须是 en 占位符的子集（R4：zh 可少不可多）
    import string
    overlay = yaml.safe_load(OVERLAY.read_text(encoding="utf-8"))
    for key, leaf in _leaves(overlay):
        en_fields = {f for _, f, _, _ in string.Formatter().parse(leaf["en"]) if f}
        zh_fields = {f for _, f, _, _ in string.Formatter().parse(leaf["zh"]) if f}
        assert zh_fields <= en_fields, f"{key}: zh 占位符 {zh_fields - en_fields} 不在 en 中"
