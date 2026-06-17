# tests/test_patch_i18n_literals.py
"""验证 patch_i18n_literals.py 把 gateway 漏翻裸字符串替换为中文的行为。

测试聚焦补丁**算法**（替换 / 保留占位符 / 幂等 / 缺失抛错 / 顺序防误伤）：
patch() 接受自定义 replacements 表，测试传入仅含 fake 内容相关锚点的小表，
避免被全局 REPLACEMENTS 里其它未出现在 fake 中的锚点误判为缺失。全局表对真实
run.py 的逐条命中由 Dockerfile 构建期执行本脚本兜底（缺锚点即构建失败）。

覆盖：
- 正常替换：英文片段被替换为对应中文，英文原文消失。
- 占位符 / 转义保留：f-string 的 {占位符}、源码里的 \\n（两字符）、命令名保留。
- 顺序防误伤：通用片段 `/reset to start fresh.` 与整句
  `Try again, or use /reset to start fresh.` 同表时，整句排前优先命中，
  不切出「Try again, or use 用 /reset ...」这类中英混杂病句。
- 幂等性：第二遍全部走「已是中文」分支，内容不变且不抛错。
- 锚点缺失：某 old 与其 new 都不存在 → RuntimeError，阻止上游改版后静默漏翻。
"""

import sys
from pathlib import Path

import pytest

# 将 patches/ 目录加入 sys.path，直接 import 补丁模块
_PATCHES_DIR = Path(__file__).parent.parent / "patches"
sys.path.insert(0, str(_PATCHES_DIR))

import patch_i18n_literals as _mod  # noqa: E402


# 最小仿真 run.py 片段：含用户报告的关闭通知、含占位符的 f-string、
# 以及「会话过大」块与超时诊断块共用 `/reset to start fresh.` 的碰撞场景。
_FAKE_RUN = (
    '            "Your current task will be interrupted. "\n'
    '            "Send any message after restart and I\'ll try to resume where you left off."\n'
    '        return f"⚠️ Steer failed: {exc}"\n'
    '            "⚠️ Session too large for the model\'s context window.\\n"\n'
    '            "Use /compact to compress the conversation, or "\n'
    '            "/reset to start fresh."\n'
    '            "Try again, or use /reset to start fresh."\n'
)

# 与 fake 内容对应的小替换表；`Try again, or use ...` 整句排在通用片段之前，
# 镜像真实表（REPLACEMENTS）也是这个顺序。
_REPL = [
    ("Your current task will be interrupted. ", "你当前的任务将被中断。"),
    ("Send any message after restart and I'll try to resume where you left off.",
     "重启完成后发送任意消息,我会尝试从你上次离开的地方继续。"),
    ("⚠️ Steer failed: ", "⚠️ Steer 失败:"),
    ("⚠️ Session too large for the model's context window.",
     "⚠️ 会话过大,超出了模型的上下文窗口。"),
    ("Use /compact to compress the conversation, or ", "请用 /compact 压缩对话,或"),
    ("Try again, or use /reset to start fresh.", "请重试,或使用 /reset 开始新会话。"),
    ("/reset to start fresh.", "用 /reset 开始新会话。"),
]


def test_patch_translates_reported_string():
    # 用户报告的关闭通知（裸字符串，未走 t()）应翻成中文，英文原文消失
    result = _mod.patch(_FAKE_RUN, _REPL)
    assert "你当前的任务将被中断。" in result
    assert "Your current task will be interrupted" not in result


def test_patch_preserves_placeholder_and_escape():
    # f-string 占位符 {exc}、\\n 转义（源码中为两字符）、命令名 /compact 必须原样保留
    result = _mod.patch(_FAKE_RUN, _REPL)
    assert "⚠️ Steer 失败:{exc}" in result
    assert "超出了模型的上下文窗口。\\n" in result
    assert "/compact" in result


def test_patch_no_mixed_language_on_reset_collision():
    # 顺序防误伤：超时诊断整句优先，不得出现「use 用 /reset」这种中英混杂病句
    result = _mod.patch(_FAKE_RUN, _REPL)
    assert "use 用" not in result
    assert "请重试,或使用 /reset 开始新会话。" in result   # 超时诊断整句
    assert "用 /reset 开始新会话。" in result              # 会话过大块的片段


def test_patch_idempotent():
    # 幂等：第二遍全部走「已是中文」分支，内容不再变化且不抛错
    once = _mod.patch(_FAKE_RUN, _REPL)
    twice = _mod.patch(once, _REPL)
    assert twice == once


def test_patch_raises_if_anchor_missing():
    # 锚点缺失（某 old 与其 new 都不存在）→ RuntimeError，阻止静默漏翻
    broken = _FAKE_RUN.replace(
        '"Your current task will be interrupted. "', '"<upstream changed>"'
    )
    with pytest.raises(RuntimeError, match="找不到"):
        _mod.patch(broken, _REPL)
