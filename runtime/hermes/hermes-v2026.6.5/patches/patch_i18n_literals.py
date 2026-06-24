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
    # --- _gateway_provider_error_reply：模型服务商错误回包（四互斥分支）---
    # 每个分支是一组相邻字面量隐式拼接，整段逐字符复制自 upstream（缩进须一致）。
    (
        '"⚠️ Provider authentication failed. Check the configured credentials; "\n'
        '            "raw provider details are in the gateway logs."',
        't("oc.run.provider_auth_failed")',
    ),
    (
        '"⚠️ The model provider rejected the request. I kept the raw provider "\n'
        '            "error out of chat; check gateway logs for details or try rephrasing."',
        't("oc.run.provider_rejected")',
    ),
    (
        '"⏱️ The model provider is rate-limiting requests. Please wait a moment and try again."',
        't("oc.run.provider_rate_limited")',
    ),
    (
        '"⚠️ The model provider failed after retries. I kept raw provider details "\n'
        '        "out of chat; check gateway logs for diagnostics."',
        't("oc.run.provider_failed_retries")',
    ),
    # --- 会话过大兜底：源码两处文案相同、缩进不同，两条 old 映射同一 key ---
    (
        '"⚠️ Session too large for the model\'s context window.\\n"\n'
        '                "Use /compact to compress the conversation, or "\n'
        '                "/reset to start fresh."',
        't("oc.run.session_too_large")',
    ),
    (
        '"⚠️ Session too large for the model\'s context window.\\n"\n'
        '                        "Use /compact to compress the conversation, or "\n'
        '                        "/reset to start fresh."',
        't("oc.run.session_too_large")',
    ),
    # --- 通用请求失败兜底：f-string 占位符 {detail} 在调用点传入 ---
    (
        'f"The request failed: {str(error_detail)[:300]}\\n"\n'
        '            "Try again or use /reset to start a fresh session."',
        't("oc.run.request_failed", detail=str(error_detail)[:300])',
    ),
    # --- 部分处理后中止（单行 f-string，{err} 在调用点传入）---
    (
        'f"⚠️ Processing stopped: {str(err)[:200]}. Try again."',
        't("oc.run.processing_stopped", err=str(err)[:200])',
    ),
    # --- 处理完成但无回复（隐式拼接整条合为一 key）---
    (
        '"⚠️ Processing completed but no response was generated. "\n'
        '            "This may be a transient error — try sending your message again."',
        't("oc.run.no_response_generated")',
    ),
    # --- 关闭/重启通知 action 三元：两短字面量分支各一 key；整段三元做唯一锚点，
    #     同时覆盖 _status_action_gerund() return 与 _notify 的 action= 赋值两处。---
    (
        '"restarting" if self._restart_requested else "shutting down"',
        '(t("oc.run.action_restarting") if self._restart_requested '
        'else t("oc.run.action_shutting_down"))',
    ),
    # --- hint 三元 restart 分支（两行隐式拼接整条）---
    (
        '"Your current task will be interrupted. "\n'
        '            "Send any message after restart and I\'ll try to resume where you left off."',
        't("oc.run.task_interrupted_resume")',
    ),
    # --- hint 三元 else 分支：带 `else ` 前缀锚定，避免与 resume 分支首段冲突 ---
    (
        'else "Your current task will be interrupted. "',
        'else t("oc.run.task_interrupted")',
    ),
    # --- msg 拼装：{action}/{hint} 已是上面 t() 的结果，作 kwarg 传入 ---
    (
        'f"⚠️ Gateway {action} — {hint}"',
        't("oc.run.gateway_interrupt", action=action, hint=hint)',
    ),
    # --- 工具结果后模型无回复（三段相邻字面量隐式拼接，整段合一 key）---
    (
        '"⚠️ The model returned no response after processing tool "\n'
        '                    "results. This can happen with some models — try again or "\n'
        '                    "rephrase your question."',
        't("oc.run.no_response_after_tool")',
    ),
    # --- 会话自动重置（"\n\n" 前缀 + 三段拼接；源码 \n 为字面量转义）---
    (
        '"\\n\\n🔄 Session auto-reset — the conversation exceeded the "\n'
        '                    "maximum context size and could not be compressed further. "\n'
        '                    "Your next message will start a fresh session."',
        't("oc.run.session_auto_reset")',
    ),
    # --- /queue 命令文案 ---
    (
        '"Usage: /queue <prompt>"',
        't("oc.run.queue_usage")',
    ),
    (
        '"Queued for the next turn."',
        't("oc.run.queue_queued")',
    ),
    # 带计数版本：{depth} 在调用点传入；以整段 f-string 锚定避免与无计数版冲突。
    (
        'f"Queued for the next turn. ({depth} queued)"',
        't("oc.run.queue_queued_depth", depth=depth)',
    ),
    # --- /steer 命令文案 ---
    # 用法（带说明的版本排在纯用法之前，否则纯用法是其子串会误伤）。
    (
        '"Usage: /steer <prompt>  (no agent is running; sending as a normal message)"',
        't("oc.run.steer_usage_no_agent")',
    ),
    (
        '"Usage: /steer <prompt>"',
        't("oc.run.steer_usage")',
    ),
    (
        '"Agent still starting — /steer queued for the next turn."',
        't("oc.run.steer_starting")',
    ),
    # steer 失败：{exc} 在调用点传入。
    (
        'f"⚠️ Steer failed: {exc}"',
        't("oc.run.steer_failed", exc=exc)',
    ),
    # steer 已排队：{preview} 在调用点传入（单引号包裹保留在 catalog）。
    (
        'f"⏩ Steer queued — arrives after the next tool call: \'{preview}\'"',
        't("oc.run.steer_queued", preview=preview)',
    ),
    (
        '"Steer rejected (empty payload)."',
        't("oc.run.steer_rejected_empty")',
    ),
    (
        '"No active agent — /steer queued for the next turn."',
        't("oc.run.steer_no_agent")',
    ),
    # --- 智能体运行中、命令受限 ---
    (
        '"Agent is running — wait or /stop first, then switch models."',
        't("oc.run.agent_running_switch_model")',
    ),
    # /codex-runtime 受限：源码两段相邻字面量隐式拼接（24 空格续行缩进）。
    (
        '("Agent is running — wait or /stop first, then "\n'
        '                        "change runtime.")',
        '(t("oc.run.agent_running_change_runtime"))',
    ),
    (
        '"Agent is running — use /goal status / pause / clear mid-run, or /stop before setting a new goal."',
        't("oc.run.agent_running_goal")',
    ),
    # 回合进行中受限：两段 f-string 隐式拼接，{name} 在调用点传入。
    (
        'f"⏳ Agent is running — `/{_cmd_def_inner.name}` can\'t run "\n'
        '                    f"mid-turn. Wait for the current response or `/stop` first."',
        't("oc.run.agent_running_midturn", name=_cmd_def_inner.name)',
    ),
    (
        '"⚡ Force-stopped. The agent was still starting — session unlocked."',
        't("oc.run.force_stopped")',
    ),
    # --- 网关 draining 排队/拒绝：{gerund} = 已翻译的 _status_action_gerund() 结果 ---
    # 三条 f-string 各自唯一，replace 会覆盖文件内全部相同出现处。
    (
        'f"⏳ Gateway is {self._status_action_gerund()} — queued for the next turn after it comes back."',
        't("oc.run.gateway_draining_queued", gerund=self._status_action_gerund())',
    ),
    (
        'f"⏳ Gateway is {self._status_action_gerund()} and is not accepting another turn right now."',
        't("oc.run.gateway_not_accepting_turn", gerund=self._status_action_gerund())',
    ),
    (
        'f"⏳ Gateway is {self._status_action_gerund()} and is not accepting new work right now."',
        't("oc.run.gateway_not_accepting_work", gerund=self._status_action_gerund())',
    ),
    # --- 命令被 hook 拦截：{command} 在调用点传入 ---
    (
        'f"Command `/{command}` was blocked by a hook."',
        't("oc.run.command_blocked_hook", command=command)',
    ),
    # --- /new 销毁性确认 detail（源码两段相邻字面量隐式拼接，20 空格缩进）---
    (
        '"This starts a fresh session and discards the current "\n'
        '                    "conversation history."',
        't("oc.run.destructive_confirm")',
    ),
    # --- 销毁性确认被取消：{command} 在调用点传入（🟡 /{command} 前缀保留在 catalog）---
    (
        'f"🟡 /{command} cancelled. Conversation unchanged."',
        't("oc.run.destructive_cancelled", command=command)',
    ),
    # --- 自定义快捷命令 exec/alias 文案 ---
    (
        '"Command returned no output."',
        't("oc.run.quick_cmd_no_output")',
    ),
    (
        '"Quick command timed out (30s)."',
        't("oc.run.quick_cmd_timeout")',
    ),
    # exec 异常：{e} 在调用点传入。
    (
        'f"Quick command error: {e}"',
        't("oc.run.quick_cmd_error", err=e)',
    ),
    # 缺 command 字段：{command} 在调用点传入。
    (
        'f"Quick command \'/{command}\' has no command defined."',
        't("oc.run.quick_cmd_no_command", command=command)',
    ),
    # 缺 target 字段：{command} 在调用点传入。
    (
        'f"Quick command \'/{command}\' has no target defined."',
        't("oc.run.quick_cmd_no_target", command=command)',
    ),
    # type 不受支持：{command} 在调用点传入。
    (
        'f"Quick command \'/{command}\' has unsupported type (supported: \'exec\', \'alias\')."',
        't("oc.run.quick_cmd_unsupported_type", command=command)',
    ),
    # --- 未知斜杠命令提示（源码四段 f-string 隐式拼接整条合一）；{command} 在调用点传入 ---
    (
        'f"Unknown command `/{command}`. "\n'
        '                            f"Type /commands to see what\'s available, "\n'
        '                            f"or resend without the leading slash to send "\n'
        '                            f"as a regular message."',
        't("oc.run.unknown_command", command=command)',
    ),
    # --- 技能已安装但被禁用（源码两段 f-string 隐式拼接）；{command_name} 作 name kwarg ---
    (
        'f"The **{command_name}** skill is installed but disabled.\\n"\n'
        '                        f"Enable it with: `hermes skills config`"',
        't("oc.run.skill_disabled", name=command_name)',
    ),
    # --- 技能可用但尚未安装；{command_name}/{install_path} 在调用点传入 ---
    (
        'f"The **{command_name}** skill is available but not installed.\\n"\n'
        '                        f"Install it with: `hermes skills install {install_path}`"',
        't("oc.run.skill_not_installed", name=command_name, install_path=install_path)',
    ),
    # --- 技能在指定平台被禁用；{_skill_name}/{_plat} 作 name/platform kwarg ---
    (
        'f"The **{_skill_name}** skill is disabled for {_plat}.\\n"\n'
        '                                f"Enable it with: `hermes skills config`"',
        't("oc.run.skill_disabled_platform", name=_skill_name, platform=_plat)',
    ),
    # --- 配对引导（源码四段 f-string 隐式拼接）；{code} 在调用点传入。
    #     前三段译入 catalog，第四行 `hermes pairing approve ...` 命令保留原文，
    #     用 + 显式拼接到 t() 结果之后（隐式拼接遇函数调用会断，故改显式）---
    (
        'f"Hi~ I don\'t recognize you yet!\\n\\n"\n'
        '                            f"Here\'s your pairing code: `{code}`\\n\\n"\n'
        '                            f"Ask the bot owner to run:\\n"\n'
        '                            f"`hermes pairing approve {platform_name} {code}`"',
        't("oc.run.pairing_intro", code=code)\n'
        '                            + f"`hermes pairing approve {platform_name} {code}`"',
    ),
    # --- 配对请求过多被限流（源码两段相邻字面量隐式拼接）---
    (
        '"Too many pairing requests right now~ "\n'
        '                            "Please try again later!"',
        't("oc.run.pairing_too_many")',
    ),
    # --- 回复发送给更新进程失败：{e} 在调用点传入 ---
    (
        'f"✗ Failed to send response to update process: {e}"',
        't("oc.run.update_send_failed", err=e)',
    ),
    # --- 回复已发送给更新进程：{label} 在调用点传入 ---
    (
        'f"✓ Sent `{label}` to the update process."',
        't("oc.run.update_sent", label=label)',
    ),
    # --- 更新进程需要输入（源码四段 f-string 隐式拼接为一个 send 实参）---
    #     标题段与输入提示段各成 key，中间 {prompt_text}{default_hint} 动态行保留原文；
    #     整块用 + 显式拼接（t() 函数调用无法与字面量隐式拼接），故合为一条 old。
    (
        'f"⚕ **Update needs your input:**\\n\\n"\n'
        '                                f"{prompt_text}{default_hint}\\n\\n"\n'
        '                                f"Reply `/approve` (yes) or `/deny` (no), "\n'
        '                                f"or type your answer directly."',
        't("oc.run.update_needs_input")\n'
        '                                + f"{prompt_text}{default_hint}\\n\\n"\n'
        '                                + t("oc.run.update_reply_hint")',
    ),
    # --- 上下文压缩中止（源码六段相邻字面量隐式拼接整条合一）；{_err} 作 err kwarg ---
    (
        '"⚠️ Context compression aborted "\n'
        '                                            f"({_err}). No messages were dropped — "\n'
        '                                            "conversation is unchanged. Run /compress "\n'
        '                                            "to retry, /reset for a clean session, or "\n'
        '                                            "check your auxiliary.compression model "\n'
        '                                            "configuration."',
        't("oc.run.compression_aborted", err=_err)',
    ),
    # --- 目标 / 子目标命令（/goal、/subgoal）---
    # 没有活动目标（单行字面量）。
    (
        '"No active goal. Set one with /goal <text>."',
        't("oc.run.goal_none")',
    ),
    # /subgoal remove 用法（缺参数，单行字面量）。
    (
        '"Usage: /subgoal remove <n>"',
        't("oc.run.subgoal_usage_remove")',
    ),
    # /subgoal remove 序号非整数（单行字面量）。
    (
        '"/subgoal remove: <n> must be an integer (1-based index)."',
        't("oc.run.subgoal_remove_not_int")',
    ),
    # /subgoal remove 异常；{exc} 在调用点传入。
    (
        'f"/subgoal remove: {exc}"',
        't("oc.run.subgoal_remove_error", exc=exc)',
    ),
    # /subgoal remove 成功；{idx}/{removed} 在调用点传入。
    (
        'f"✓ Removed subgoal {idx}: {removed}"',
        't("oc.run.subgoal_removed", idx=idx, removed=removed)',
    ),
    # /subgoal clear 异常；{exc} 在调用点传入。
    (
        'f"/subgoal clear: {exc}"',
        't("oc.run.subgoal_clear_error", exc=exc)',
    ),
    # /subgoal clear 无可清除子目标（单行字面量）。
    (
        '"No subgoals to clear."',
        't("oc.run.subgoal_none_to_clear")',
    ),
    # /subgoal 添加成功；{idx}/{text} 在调用点传入。
    (
        'f"✓ Added subgoal {idx}: {text}"',
        't("oc.run.subgoal_added", idx=idx, text=text)',
    ),
    # --- /whoami 权限层级信息（三互斥分支，各为多段 f-string 隐式拼接）---
    # 整块逐字符复制自 upstream（16/12 空格缩进须一致）；隐式拼接改 + 显式拼接，
    # 每段译入 catalog，行尾 \n 在调用点用 + "\n" 保留，动态占位作 kwarg。
    # 分支一：unrestricted（无管理员名单）。
    (
        'f"**You** — {platform} ({scope})\\n"\n'
        '                f"User ID: `{user_id}`\\n"\n'
        '                f"Tier: unrestricted (no admin list configured for this scope)\\n"\n'
        '                f"Slash commands: all available"',
        't("oc.run.access_you", platform=platform, scope=scope) + "\\n"\n'
        '                + t("oc.run.access_user_id", user_id=user_id) + "\\n"\n'
        '                + t("oc.run.access_tier_unrestricted") + "\\n"\n'
        '                + t("oc.run.access_slash_all")',
    ),
    # 分支二：admin。
    (
        'f"**You** — {platform} ({scope})\\n"\n'
        '                f"User ID: `{user_id}`\\n"\n'
        '                f"Tier: **admin**\\n"\n'
        '                f"Slash commands: all available"',
        't("oc.run.access_you", platform=platform, scope=scope) + "\\n"\n'
        '                + t("oc.run.access_user_id", user_id=user_id) + "\\n"\n'
        '                + t("oc.run.access_tier_admin") + "\\n"\n'
        '                + t("oc.run.access_slash_all")',
    ),
    # 分支三：普通 user（12 空格缩进；{runnable_str} 在调用点传入）。
    (
        'f"**You** — {platform} ({scope})\\n"\n'
        '            f"User ID: `{user_id}`\\n"\n'
        '            f"Tier: user\\n"\n'
        '            f"Slash commands you can run: {runnable_str}"',
        't("oc.run.access_you", platform=platform, scope=scope) + "\\n"\n'
        '            + t("oc.run.access_user_id", user_id=user_id) + "\\n"\n'
        '            + t("oc.run.access_tier_user") + "\\n"\n'
        '            + t("oc.run.access_slash_you_can_run", runnable_str=runnable_str)',
    ),
    # --- 技能 bundles（/bundles）---
    # Bundles 子系统不可用；{exc} 在调用点传入。
    (
        'f"Bundles subsystem unavailable: {exc}"',
        't("oc.run.bundles_unavailable", exc=exc)',
    ),
    # 无 bundle 安装提示（源码四段相邻字面量隐式拼接，16 空格缩进）。
    # 首两段与目录前缀译入 catalog，中间命令行 / 目录值保留原文，用 + 显式拼接。
    (
        '"No skill bundles installed.\\n"\n'
        '                "Create one on the host with:\\n"\n'
        '                "  `hermes bundles create <name> --skill <s1> --skill <s2>`\\n"\n'
        '                f"Directory: `{_bundles_dir()}`"',
        't("oc.run.bundles_none") + t("oc.run.bundles_create_hint")\n'
        '                + "  `hermes bundles create <name> --skill <s1> --skill <s2>`\\n"\n'
        '                + t("oc.run.bundles_directory") + f"{_bundles_dir()}`"',
    ),
    # --- 后台任务回包 ---
    # 缺凭证失败；{task_id} 在调用点传入。
    (
        'f"❌ Background task {task_id} failed: no provider credentials configured."',
        't("oc.run.bgtask_failed_no_creds", task_id=task_id)',
    ),
    # 异常失败；{task_id}/{e} 在调用点传入。
    (
        'f"❌ Background task {task_id} failed: {e}"',
        't("oc.run.bgtask_failed_err", task_id=task_id, err=e)',
    ),
    # 完成标题 + 输出（L12770 整条 f-string）：标题与「无回复」译入 catalog，
    # 中间 Prompt 行保留原文，用 + 显式拼接；{preview} 在调用点传入。
    # 注意：本条（更长）须排在仅标题 header（其子串）之前。
    (
        'f\'✅ Background task complete\\nPrompt: "{preview}"\\n\\n(No response generated)\'',
        't("oc.run.bgtask_complete") + f\'\\nPrompt: "{preview}"\\n\\n\' '
        '+ t("oc.run.bgtask_no_response")',
    ),
    # 完成标题（L12702 header f-string，仅标题段）：标题译入 catalog，
    # Prompt 行保留原文用 + 显式拼接；{preview} 在调用点传入。
    (
        'f\'✅ Background task complete\\nPrompt: "{preview}"\\n\\n\'',
        't("oc.run.bgtask_complete") + f\'\\nPrompt: "{preview}"\\n\\n\'',
    ),
    # header 拼接「无回复」（L12713 header + 字面量）。
    (
        'header + "(No response generated)"',
        'header + t("oc.run.bgtask_no_response")',
    ),
    # --- 危险命令审批 ---
    # /confirm 提示整块（源码多段 f-string 隐式拼接，12 空格缩进）。
    # 标题前缀与三个审批选项译入 catalog；{command}**、{detail}、Choose:、
    # 文本兜底行保留原文，用 + 显式拼接，行尾 \n 在调用点保留。
    (
        'f"⚠️ **Confirm /{command}**\\n\\n"\n'
        '            f"{detail}\\n\\n"\n'
        '            "Choose:\\n"\n'
        '            "• **Approve Once** — proceed this time only\\n"\n'
        '            "• **Always Approve** — proceed and silence this prompt permanently\\n"\n'
        '            "• **Cancel** — keep current conversation\\n\\n"\n'
        '            "_Text fallback: reply `/approve`, `/always`, or `/cancel`._"',
        't("oc.run.confirm_header") + f"{command}**\\n\\n"\n'
        '            + f"{detail}\\n\\n"\n'
        '            + "Choose:\\n"\n'
        '            + t("oc.run.confirm_approve_once") + "\\n"\n'
        '            + t("oc.run.confirm_always") + "\\n"\n'
        '            + t("oc.run.confirm_cancel") + "\\n\\n"\n'
        '            + "_Text fallback: reply `/approve`, `/always`, or `/cancel`._"',
    ),
    # 危险命令文本审批整块（源码四段 f-string 隐式拼接，20 空格缩进）。
    # 标题与回复提示译入 catalog；命令预览代码块、Reason 行保留原文，用 + 显式拼接。
    (
        'f"⚠️ **Dangerous command requires approval:**\\n"\n'
        '                    f"```\\n{cmd_preview}\\n```\\n"\n'
        '                    f"Reason: {desc}\\n\\n"\n'
        '                    f"Reply `/approve` to execute, `/approve session` to approve this pattern "\n'
        '                    f"for the session, `/approve always` to approve permanently, or `/deny` to cancel."',
        't("oc.run.confirm_dangerous_header") + "\\n"\n'
        '                    + f"```\\n{cmd_preview}\\n```\\n"\n'
        '                    + f"Reason: {desc}\\n\\n"\n'
        '                    + t("oc.run.confirm_reply_hint")',
    ),
    # --- Hermes 自更新通知 ---
    # 更新完成（L15250 整条单行字面量）。
    (
        '"✅ Hermes update finished."',
        't("oc.run.update_finished")',
    ),
    # 更新失败带退出码（L15254：.format 定位占位 → 改 kwarg；{exit_code} 在调用点传入）。
    (
        '"❌ Hermes update failed (exit code {}).".format(exit_code)',
        't("oc.run.update_failed_exit", exit_code=exit_code)',
    ),
    # 更新超时（L15342 单行字面量）。
    (
        '"❌ Hermes update timed out after 30 minutes."',
        't("oc.run.update_timed_out")',
    ),
    # 更新完成带输出（L15442 f-string）：完成标题复用 update_finished，
    # 输出代码块保留原文用 + 显式拼接；{output} 在调用点传入。
    (
        'f"✅ Hermes update finished.\\n\\n```\\n{output}\\n```"',
        't("oc.run.update_finished") + f"\\n\\n```\\n{output}\\n```"',
    ),
    # 更新失败带输出（L15444 f-string）：失败前缀译入 catalog，
    # 输出代码块保留原文用 + 显式拼接；{output} 在调用点传入。
    (
        'f"❌ Hermes update failed.\\n\\n```\\n{output}\\n```"',
        't("oc.run.update_failed_output") + f"```\\n{output}\\n```"',
    ),
    # 更新成功完成（L15446 单行字面量）。
    (
        '"✅ Hermes update finished successfully."',
        't("oc.run.update_finished_ok")',
    ),
    # 更新失败、查日志（L15448 单行字面量）。
    (
        '"❌ Hermes update failed. Check the gateway logs or run `hermes update` manually for details."',
        't("oc.run.update_failed_logs")',
    ),
    # 网关重启成功（L15511 单行字面量）。
    (
        '"♻ Gateway restarted successfully. Your session continues."',
        't("oc.run.gateway_restarted")',
    ),
    # --- 代理模式错误 ---
    # 缺 aiohttp（L16684 单行字面量）。
    (
        '"⚠️ Proxy mode requires aiohttp. Install with: pip install aiohttp"',
        't("oc.run.proxy_needs_aiohttp")',
    ),
    # 未配置代理地址（L16693 单行字面量）。
    (
        '"⚠️ Proxy URL not configured (GATEWAY_PROXY_URL or gateway.proxy_url)"',
        't("oc.run.proxy_url_missing")',
    ),
    # 代理非 200 错误（L16835 f-string）：前缀译入 catalog，
    # {resp.status}): {error_text[:300]} 保留原文用 + 显式拼接。
    (
        'f"⚠️ Proxy error ({resp.status}): {error_text[:300]}"',
        't("oc.run.proxy_error") + f"{resp.status}): {error_text[:300]}"',
    ),
    # 代理连接错误（L16891 f-string）：前缀译入 catalog，{e} 保留原文用 + 显式拼接。
    (
        'f"⚠️ Proxy connection error: {e}"',
        't("oc.run.proxy_conn_error") + f"{e}"',
    ),
    # 远端智能体无响应（L16929 单行字面量，or 兜底）。
    (
        '"(No response from remote agent)"',
        't("oc.run.proxy_no_response")',
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
