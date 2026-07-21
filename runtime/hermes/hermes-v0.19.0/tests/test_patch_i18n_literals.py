# runtime/hermes/hermes-v0.19.0/tests/test_patch_i18n_literals.py
"""patch_i18n_literals 新框架（表达式→t() 替换 + base.py import 注入）单元测试。"""
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent / "patches"))
from patch_i18n_literals import (
    REPLACEMENTS_RUN,
    ensure_i18n_import,
    patch,
    patch_run_group,
)


def test_patch_replaces_full_expression_with_t_call():
    # 场景：把完整字符串表达式整体换成 t() 调用
    src = 'return "Queued for the next turn."\n'
    out = patch(src, [('"Queued for the next turn."', 't("oc.run.queue_queued")')])
    assert out == 'return t("oc.run.queue_queued")\n'


def test_patch_idempotent_when_t_call_present():
    # 场景：t() 调用已存在（已打过补丁）→ 幂等跳过，不抛
    src = 'return t("oc.run.queue_queued")\n'
    out = patch(src, [('"Queued for the next turn."', 't("oc.run.queue_queued")')])
    assert out == src


def test_patch_fail_loud_when_anchor_and_new_both_absent():
    # 场景：英文锚点与 t() 调用都不在源码 → 上游结构变更，抛错列出缺失
    with pytest.raises(RuntimeError) as e:
        patch("无关内容\n", [('"missing anchor"', 't("oc.run.x")')])
    assert "missing anchor" in str(e.value)


def test_ensure_i18n_import_adds_when_absent():
    # 场景：base.py 无 i18n import → 注入 from agent.i18n import t
    src = "import os\n\n\nclass Base:\n    pass\n"
    out = ensure_i18n_import(src)
    assert "from agent.i18n import t" in out


def test_ensure_i18n_import_idempotent():
    # 场景：已有 import → 不重复注入
    src = "import os\nfrom agent.i18n import t\n"
    assert ensure_i18n_import(src) == src


def test_patch_run_group_handles_v019_dangerous_approval_helper():
    # 场景：v0.19.0 上游把危险命令审批文案抽到 helper，description 取代旧 desc。
    run_py = (
        'def _format_exec_approval_fallback(command, description, command_prefix):\n'
        '    cmd_preview = command[:200] + "..." if len(command) > 200 else command\n'
        '    heading = "⚠️ **Dangerous command requires approval:**"\n'
        '    return (\n'
        '        f"{heading}\\n```\\n{cmd_preview}\\n```\\nReason: {description}\\n\\n"\n'
        '        + ", ".join(choices[:-1]) + f", or {choices[-1]}."\n'
        '    )\n'
    )
    contents = {"run.py": run_py, "slash_commands.py": "", "kanban_watchers.py": ""}
    dangerous_replacements = [
        item
        for item in REPLACEMENTS_RUN
        if "Dangerous command requires approval" in item[0]
        or "Reason: {desc}" in item[0]
        or "Reason: {description}" in item[0]
    ]

    missing = patch_run_group(contents, dangerous_replacements)

    assert not missing
    assert 't("oc.run.confirm_dangerous_header")' in contents["run.py"]
    assert 't("oc.run.confirm_reason", desc=description)' in contents["run.py"]
