#!/usr/bin/env python3
# patches/merge_oc_locales.py
"""构建期：把 locales/oc_overlay.yaml 的 oc.* 文案分语言深合并进 upstream
en.yaml / zh.yaml 的 oc 顶层块。

overlay 结构：oc.<path>.<key> = {en: "...", zh: "..."}。合并时按目标语言取
对应叶子，写成 upstream catalog 的同构嵌套（t() 会拍平成点分键）。

约束：
- 幂等：目标已含同 key 同值 → 跳过。
- 冲突 fail-loud：目标已含同 key 但值不同 → 抛 MergeConflict（防止覆盖 upstream）。
"""
import pathlib
import sys

import yaml

OVERLAY = pathlib.Path(__file__).resolve().parent.parent / "locales" / "oc_overlay.yaml"
UPSTREAM_LOCALES = pathlib.Path("/usr/local/lib/hermes-agent/locales")
LANGS = ("en", "zh")


class MergeConflict(RuntimeError):
    """upstream 已存在同名 oc.* key 且值不同时抛出。"""


def _project_lang(node, lang):
    """把 overlay 的 {en,zh} 叶子投影成单语言嵌套 dict。"""
    # 叶子：形如 {"en": "...", "zh": "..."}
    if isinstance(node, dict) and set(node.keys()) <= {"en", "zh"} and node:
        if lang not in node:
            raise MergeConflict(f"overlay 叶子缺少语言 {lang}: {node!r}")
        return node[lang]
    if isinstance(node, dict):
        return {k: _project_lang(v, lang) for k, v in node.items()}
    raise MergeConflict(f"overlay 非法节点(非 dict/叶子): {node!r}")


def _deep_merge(dst, src, path=""):
    """把 src 深合并进 dst；同标量 key 值不同 → 冲突。返回 dst。"""
    for key, sval in src.items():
        cur = f"{path}.{key}" if path else key
        if key not in dst:
            dst[key] = sval
        elif isinstance(dst[key], dict) and isinstance(sval, dict):
            _deep_merge(dst[key], sval, cur)
        elif dst[key] == sval:
            continue  # 幂等
        else:
            raise MergeConflict(f"键 {cur} 冲突：upstream={dst[key]!r} overlay={sval!r}")
    return dst


def merge_lang(upstream: dict, overlay: dict, lang: str) -> dict:
    """把 overlay 投影到 lang 后深合并进 upstream（拷贝语义由调用方保证）。"""
    projected = _project_lang(overlay, lang)  # {"oc": {...}}
    return _deep_merge(upstream, projected)


def main() -> int:
    # overlay 文件为空 / 内容为 null 时 safe_load 返回 None，统一兜底成空 dict，
    # 与下方 upstream 的 `or {}` 保持一致，避免落进 _project_lang 抛出晦涩报错。
    overlay = yaml.safe_load(OVERLAY.read_text(encoding="utf-8")) or {}
    for lang in LANGS:
        target = UPSTREAM_LOCALES / f"{lang}.yaml"
        if not target.exists():
            print(f"[merge_oc_locales] 目标 catalog 不存在: {target}", file=sys.stderr)
            return 1
        upstream = yaml.safe_load(target.read_text(encoding="utf-8")) or {}
        merged = merge_lang(upstream, overlay, lang)
        target.write_text(
            yaml.safe_dump(merged, allow_unicode=True, sort_keys=False),
            encoding="utf-8",
        )
        print(f"[merge_oc_locales] 已合并 oc.* → {target}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
