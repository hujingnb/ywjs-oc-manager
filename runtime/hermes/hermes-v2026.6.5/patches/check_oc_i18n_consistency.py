#!/usr/bin/env python3
# patches/check_oc_i18n_consistency.py
"""一致性守卫：补丁引入的每个 t("oc.X") key 必须在 oc_overlay.yaml 同时有 en+zh；
反之 overlay 每个 key 都应被补丁用到。任一不满足即 fail-loud，防止 key 与译文漂移。

可作脚本（构建期）运行，也被单元测试 import。"""
import pathlib
import re
import sys

import yaml

OVERLAY = pathlib.Path(__file__).resolve().parent.parent / "locales" / "oc_overlay.yaml"
_KEY_RE = re.compile(r't\(\s*"(oc\.[a-zA-Z0-9_.]+)"')


class ConsistencyError(RuntimeError):
    """patch 与 overlay 的 key 集合不一致时抛出。"""


def extract_patch_keys(replacements) -> set:
    """从 (old, new) 列表的 new 串里抽取所有 t("oc.X") 的 key。"""
    keys = set()
    for _old, new in replacements:
        keys.update(_KEY_RE.findall(new))
    return keys


def _flatten(node, prefix, out):
    """递归展开 overlay 嵌套字典为扁平 key 集合。

    叶子节点判断规则：字典的 key 集合是 {en, zh} 的子集且非空即视为叶子。
    叶子必须同时含 en 和 zh，否则抛 ConsistencyError。
    """
    # 叶子：{en,zh}
    if isinstance(node, dict) and set(node.keys()) <= {"en", "zh"} and node:
        if "en" not in node or "zh" not in node:
            raise ConsistencyError(f"overlay 叶子 {prefix} 缺 en 或 zh: {node!r}")
        out.add(prefix)
        return
    if isinstance(node, dict):
        for k, v in node.items():
            _flatten(v, f"{prefix}.{k}" if prefix else k, out)
        return
    raise ConsistencyError(f"overlay 非法节点 {prefix}: {node!r}")


def extract_overlay_keys(overlay: dict) -> set:
    """从 overlay 字典提取所有合法叶子 key（每叶子须含 en+zh）。"""
    out = set()
    _flatten(overlay, "", out)
    return out


def check_consistency(patch_keys: set, overlay_keys: set) -> None:
    """校验 patch_keys 与 overlay_keys 双向一致，不一致则抛 ConsistencyError。

    missing：patch 用了但 overlay 没有定义（缺译文）。
    orphan：overlay 定义了但 patch 未用到（多余 key，可能是漂移残留）。
    """
    missing = patch_keys - overlay_keys  # patch 用了但 overlay 没有
    orphan = overlay_keys - patch_keys   # overlay 有但 patch 没用
    if missing or orphan:
        raise ConsistencyError(
            f"oc i18n key 不一致：\n  patch 缺译文(missing): {sorted(missing)}\n"
            f"  overlay 多余(orphan): {sorted(orphan)}"
        )


def main() -> int:
    """构建期脚本入口：从 patch_i18n_literals 加载替换表并与 overlay 双向校验。

    作为脚本运行时本目录(patches/)在 sys.path[0]，扁平 import 同目录补丁模块。
    """
    from patch_i18n_literals import REPLACEMENTS_RUN, REPLACEMENTS_BASE
    patch_keys = extract_patch_keys(REPLACEMENTS_RUN + REPLACEMENTS_BASE)
    overlay = yaml.safe_load(OVERLAY.read_text(encoding="utf-8")) or {}
    overlay_keys = extract_overlay_keys(overlay)
    try:
        check_consistency(patch_keys, overlay_keys)
    except ConsistencyError as e:
        print(f"[check_oc_i18n_consistency] {e}", file=sys.stderr)
        return 1
    print(f"[check_oc_i18n_consistency] OK：{len(patch_keys)} 个 oc.* key 双侧一致。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
