#!/usr/bin/env python3
# patches/patch_i18n_literals.py
"""构建期补丁：把 hermes 里漏翻的用户可见英文裸字符串接入原生 t() catalog。

把每条完整字符串表达式整体替换为 t("oc.<file>.<key>", kw=expr) 调用；中英译文在
locales/oc_overlay.yaml，由 merge_oc_locales.py 构建期合并进 upstream en/zh.yaml。
语言由 t() 自行从 config.yaml 的 display.language 解析。覆盖范围仅微信走的平台无关
路径（run.py + 所有适配器共用的 base.py），其它平台专属文案不在范围内。

约定：
- old = 未打补丁 upstream 里完整的字符串表达式源文本（含 f"/引号/跨行空白）。
- new = t(...) 调用字符串。
- old 不存在且 new 也不存在 → 上游结构变更，收集后一次性抛错。
- new 已存在即视为已打过补丁，跳过（幂等）。
- 当某 old 是另一 old 的子串时，长串排前。
"""
import pathlib
import re
import sys

RUN = pathlib.Path("/usr/local/lib/hermes-agent/gateway/run.py")
BASE = pathlib.Path("/usr/local/lib/hermes-agent/gateway/platforms/base.py")

I18N_IMPORT = "from agent.i18n import t"

# 替换表：(完整英文表达式源文本, t() 调用字符串)。由后续任务按组填充。
REPLACEMENTS_RUN: list[tuple[str, str]] = [
    # 闲置 / 网关超时诊断 _diag_lines 块（4 条逻辑消息，相邻字面量隐式拼接）。
    # old 整段逐字符复制自 upstream run.py，缩进与换行须与源文件一致才能命中。
    (
        'f"⏱️ Agent inactive for {_timeout_mins} min — no tool calls "\n'
        '                    f"or API responses."',
        't("oc.run.timeout_inactive", timeout_mins=_timeout_mins)',
    ),
    (
        'f"The agent appears stuck on tool `{_cur_tool}` "\n'
        '                        f"({_secs_ago:.0f}s since last activity, "\n'
        '                        f"iteration {_iter_n}/{_iter_max})."',
        't("oc.run.timeout_stuck_tool", cur_tool=_cur_tool, secs_ago=_secs_ago, '
        'iter_n=_iter_n, iter_max=_iter_max)',
    ),
    (
        'f"Last activity: {_last_desc} ({_secs_ago:.0f}s ago, "\n'
        '                        f"iteration {_iter_n}/{_iter_max}). "\n'
        '                        "The agent may have been waiting on an API response."',
        't("oc.run.timeout_last_activity", last_desc=_last_desc, secs_ago=_secs_ago, '
        'iter_n=_iter_n, iter_max=_iter_max)',
    ),
    (
        '"To increase the limit, set agent.gateway_timeout in config.yaml "\n'
        '                    "(value in seconds, 0 = no limit) and restart the gateway.\\n"\n'
        '                    "Try again, or use /reset to start fresh."',
        't("oc.run.timeout_increase_limit")',
    ),
]
REPLACEMENTS_BASE: list[tuple[str, str]] = []

TARGETS: list[tuple[pathlib.Path, list[tuple[str, str]], bool]] = [
    (RUN, REPLACEMENTS_RUN, False),   # run.py 已有 import
    (BASE, REPLACEMENTS_BASE, True),  # base.py 需注入 import
]


def patch(content: str, replacements: list[tuple[str, str]]) -> str:
    """逐条把 old 整体替换为 new；fail-loud + 幂等（语义同旧框架）。"""
    replaced, already, missing = [], [], []
    for old, new in replacements:
        if old in content:
            content = content.replace(old, new)
            replaced.append(old)
        elif new in content:
            already.append(old)
        else:
            missing.append(old)
    print(f"[patch_i18n_literals] 已替换 {len(replaced)} 条，"
          f"幂等跳过 {len(already)} 条，缺失 {len(missing)} 条。")
    if missing:
        detail = "\n".join(f"  - {m!r}" for m in missing)
        raise RuntimeError(
            "patch_i18n_literals: 以下英文锚点找不到——上游文案结构可能已变更，"
            f"请更新补丁脚本：\n{detail}"
        )
    return content


def ensure_i18n_import(content: str) -> str:
    """若文件未导入 t 则注入 import；幂等。插在最后一条顶层 import 之后。"""
    if I18N_IMPORT in content:
        return content
    lines = content.splitlines(keepends=True)
    last_import = -1
    for i, line in enumerate(lines):
        if re.match(r"^(import |from )", line):
            last_import = i
    insert_at = last_import + 1 if last_import >= 0 else 0
    lines.insert(insert_at, I18N_IMPORT + "\n")
    return "".join(lines)


def main() -> int:
    for target, repls, need_import in TARGETS:
        if not target.exists():
            print(f"[patch_i18n_literals] 目标文件不存在: {target}", file=sys.stderr)
            return 1
        content = target.read_text(encoding="utf-8")
        original = content
        if need_import:
            content = ensure_i18n_import(content)
        content = patch(content, repls)
        if content != original:
            target.write_text(content, encoding="utf-8")
            print(f"[patch_i18n_literals] 已写回 {target}")
        else:
            print(f"[patch_i18n_literals] {target} 内容未变化（全部幂等跳过）")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
