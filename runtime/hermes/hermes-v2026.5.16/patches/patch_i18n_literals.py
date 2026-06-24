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
    # --- 闲置 / 网关超时诊断 _diag_lines 块（相邻字面量隐式拼接，old 逐字符复制自 upstream）---
    (
        'f"⏱️ Agent inactive for {_timeout_mins} min — no tool calls "\n                    f"or API responses."',
        't("oc.run.timeout_inactive", timeout_mins=_timeout_mins)',
    ),
    (
        'f"The agent appears stuck on tool `{_cur_tool}` "\n                        f"({_secs_ago:.0f}s since last activity, "\n                        f"iteration {_iter_n}/{_iter_max})."',
        't("oc.run.timeout_stuck_tool", cur_tool=_cur_tool, secs_ago=_secs_ago, iter_n=_iter_n, iter_max=_iter_max)',
    ),
    (
        'f"Last activity: {_last_desc} ({_secs_ago:.0f}s ago, "\n                        f"iteration {_iter_n}/{_iter_max}). "\n                        "The agent may have been waiting on an API response."',
        't("oc.run.timeout_last_activity", last_desc=_last_desc, secs_ago=_secs_ago, iter_n=_iter_n, iter_max=_iter_max)',
    ),
    (
        '"To increase the limit, set agent.gateway_timeout in config.yaml "\n                    "(value in seconds, 0 = no limit) and restart the gateway.\\n"\n                    "Try again, or use /reset to start fresh."',
        't("oc.run.timeout_increase_limit")',
    ),
    # --- run_agent 异常回包的 status_hint（5.16 专属：按 status_code 内联赋值后拼进
    #     encountered_error 消息；6.5 重构为 _gateway_provider_error_reply 无对应）。
    #     each old = RHS 字符串字面量；plan_resets 为 f-string，{_hours} 作 hours kwarg。---
    (
        '" Check your API key or run `claude /login` to refresh OAuth credentials."',
        't("oc.run.status_hint_auth")',
    ),
    (
        '" Your API balance or quota is exhausted. Check your provider dashboard."',
        't("oc.run.status_hint_quota")',
    ),
    (
        'f" Your plan\'s usage limit has been reached. It resets in ~{_hours}h."',
        't("oc.run.status_hint_plan_resets", hours=_hours)',
    ),
    (
        '" Your plan\'s usage limit has been reached. Please wait until it resets."',
        't("oc.run.status_hint_plan_wait")',
    ),
    (
        '" You are being rate-limited. Please wait a moment and try again."',
        't("oc.run.status_hint_rate_limited")',
    ),
    (
        '" The API is temporarily overloaded. Please try again shortly."',
        't("oc.run.status_hint_overloaded")',
    ),
    # --- 会话过大兜底：源码两处文案相同、缩进不同，两条 old 映射同一 key ---
    (
        '"⚠️ Session too large for the model\'s context window.\\n"\n                "Use /compact to compress the conversation, or "\n                "/reset to start fresh."',
        't("oc.run.session_too_large")',
    ),
    (
        '"⚠️ Session too large for the model\'s context window.\\n"\n                        "Use /compact to compress the conversation, or "\n                        "/reset to start fresh."',
        't("oc.run.session_too_large")',
    ),
    # --- 通用请求失败兜底（{detail} 在调用点传入）---
    (
        'f"The request failed: {str(error_detail)[:300]}\\n"\n            "Try again or use /reset to start a fresh session."',
        't("oc.run.request_failed", detail=str(error_detail)[:300])',
    ),
    # --- 部分处理后中止（单行 f-string，{err} 在调用点传入）---
    (
        'f"⚠️ Processing stopped: {str(err)[:200]}. Try again."',
        't("oc.run.processing_stopped", err=str(err)[:200])',
    ),
    # --- 处理完成但无回复（隐式拼接整条合一）---
    (
        '"⚠️ Processing completed but no response was generated. "\n            "This may be a transient error — try sending your message again."',
        't("oc.run.no_response_generated")',
    ),
    # --- 关闭/重启通知 action 三元：两短字面量分支各一 key；整段三元做唯一锚点 ---
    (
        '"restarting" if self._restart_requested else "shutting down"',
        '(t("oc.run.action_restarting") if self._restart_requested else t("oc.run.action_shutting_down"))',
    ),
    # --- hint 三元 restart 分支（两行隐式拼接整条）---
    (
        '"Your current task will be interrupted. "\n            "Send any message after restart and I\'ll try to resume where you left off."',
        't("oc.run.task_interrupted_resume")',
    ),
    # --- hint 三元 else 分支：真实上游无尾随空格；`else ` 前缀锚定避免与 resume 分支首段冲突 ---
    (
        'else "Your current task will be interrupted."',
        'else t("oc.run.task_interrupted")',
    ),
    # --- msg 拼装：{action}/{hint} 已是上面 t() 的结果，作 kwarg 传入 ---
    (
        'f"⚠️ Gateway {action} — {hint}"',
        't("oc.run.gateway_interrupt", action=action, hint=hint)',
    ),
    # --- 工具结果后模型无回复（三段相邻字面量隐式拼接，整段合一 key）---
    (
        '"⚠️ The model returned no response after processing tool "\n                    "results. This can happen with some models — try again or "\n                    "rephrase your question."',
        't("oc.run.no_response_after_tool")',
    ),
    # --- 会话自动重置（"\n\n" 前缀 + 三段拼接；源码 \n 为字面量转义）---
    (
        '"\\n\\n🔄 Session auto-reset — the conversation exceeded the "\n                    "maximum context size and could not be compressed further. "\n                    "Your next message will start a fresh session."',
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
    (
        'f"Queued for the next turn. ({depth} queued)"',
        't("oc.run.queue_queued_depth", depth=depth)',
    ),
    # --- /steer 命令文案（带说明版排在纯用法之前，否则纯用法是其子串会误伤）---
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
    (
        'f"⚠️ Steer failed: {exc}"',
        't("oc.run.steer_failed", exc=exc)',
    ),
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
    (
        '("Agent is running — wait or /stop first, then "\n                        "change runtime.")',
        '(t("oc.run.agent_running_change_runtime"))',
    ),
    (
        '"Agent is running — use /goal status / pause / clear mid-run, or /stop before setting a new goal."',
        't("oc.run.agent_running_goal")',
    ),
    (
        'f"⏳ Agent is running — `/{_cmd_def_inner.name}` can\'t run "\n                    f"mid-turn. Wait for the current response or `/stop` first."',
        't("oc.run.agent_running_midturn", name=_cmd_def_inner.name)',
    ),
    (
        '"⚡ Force-stopped. The agent was still starting — session unlocked."',
        't("oc.run.force_stopped")',
    ),
    # --- 网关 draining 排队/拒绝（{gerund} = 已翻译的 _status_action_gerund()）；
    #     真实上游 queued 句无 "is"（与 6.5 同），not_accepting 两句带 "is"。---
    (
        'f"⏳ Gateway {self._status_action_gerund()} — queued for the next turn after it comes back."',
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
    # --- /new 销毁性确认 detail（源码两段相邻字面量隐式拼接）---
    (
        '"This starts a fresh session and discards the current "\n                    "conversation history."',
        't("oc.run.destructive_confirm")',
    ),
    # --- 销毁性确认被取消：{command} 在调用点传入 ---
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
    (
        'f"Quick command error: {e}"',
        't("oc.run.quick_cmd_error", err=e)',
    ),
    (
        'f"Quick command \'/{command}\' has no command defined."',
        't("oc.run.quick_cmd_no_command", command=command)',
    ),
    (
        'f"Quick command \'/{command}\' has no target defined."',
        't("oc.run.quick_cmd_no_target", command=command)',
    ),
    (
        'f"Quick command \'/{command}\' has unsupported type (supported: \'exec\', \'alias\')."',
        't("oc.run.quick_cmd_unsupported_type", command=command)',
    ),
    # --- 未知斜杠命令提示（源码四段 f-string 隐式拼接整条合一）；{command} 在调用点传入 ---
    (
        'f"Unknown command `/{command}`. "\n                            f"Type /commands to see what\'s available, "\n                            f"or resend without the leading slash to send "\n                            f"as a regular message."',
        't("oc.run.unknown_command", command=command)',
    ),
    # --- 技能未安装/被禁用（源码两段 f-string 隐式拼接，\n 为字面转义）---
    (
        'f"The **{command_name}** skill is installed but disabled.\\n"\n                        f"Enable it with: `hermes skills config`"',
        't("oc.run.skill_disabled", name=command_name)',
    ),
    (
        'f"The **{command_name}** skill is available but not installed.\\n"\n                        f"Install it with: `hermes skills install {install_path}`"',
        't("oc.run.skill_not_installed", name=command_name, install_path=install_path)',
    ),
    (
        'f"The **{_skill_name}** skill is disabled for {_plat}.\\n"\n                                f"Enable it with: `hermes skills config`"',
        't("oc.run.skill_disabled_platform", name=_skill_name, platform=_plat)',
    ),
    # --- 配对流程（未识别用户首次私聊）---
    (
        'f"Hi~ I don\'t recognize you yet!\\n\\n"\n                            f"Here\'s your pairing code: `{code}`\\n\\n"\n                            f"Ask the bot owner to run:\\n"\n                            f"`hermes pairing approve {platform_name} {code}`"',
        't("oc.run.pairing_intro", code=code)\n                            + f"`hermes pairing approve {platform_name} {code}`"',
    ),
    (
        '"Too many pairing requests right now~ "\n                            "Please try again later!"',
        't("oc.run.pairing_too_many")',
    ),
    # ====================== 批次 2：run.py 后半 ======================
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
    # --- 上下文压缩摘要失败（5.16 专属：丢弃的历史轮次不可恢复；6.5 改写为
    #     compression_aborted「无消息丢弃」措辞，两者不同故 5.16 单独成 key）。
    #     源码六段相邻字面量隐式拼接整条合一；{_err}/{_dropped} 作 err/dropped kwarg。---
    (
        '"⚠️ Context compression summary failed "\n'
        '                                            f"({_err}). {_dropped} historical message(s) "\n'
        '                                            "were removed and replaced with a placeholder. "\n'
        '                                            "Earlier context is no longer recoverable. "\n'
        '                                            "Consider /reset for a clean session, or check "\n'
        '                                            "your auxiliary.compression model configuration."',
        't("oc.run.compression_summary_failed", err=_err, dropped=_dropped)',
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
    # 命令仅管理员可用的拒绝提示（单行 f-string，{suffix} 为动态尾段透传）。
    (
        'f"⛔ /{canonical_cmd} is admin-only here. {suffix}"',
        't("oc.run.access_admin_only", canonical_cmd=canonical_cmd, suffix=suffix)',
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
    # 注意：5.16 无 /bundles 命令（bundles 子系统为 6.5 新增），故跳过相关文案。
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
    # --- run_agent 鉴权失败 / 通用错误（final_response 回包）---
    # 鉴权失败 final_response（L17721 f-string）：前缀译入 catalog，{exc} 保留原文用 + 显式拼接。
    (
        'f"⚠️ Provider authentication failed: {exc}"',
        't("oc.run.provider_auth_failed_exc") + f"{exc}"',
    ),
    # 通用错误首段（L9920 f-string，多段隐式拼接的第一段）：前缀译入 catalog，
    # {error_type}).\n 动态尾段保留原文用 + 显式拼接（隐式拼接遇 t() 会断，故改 +）。
    (
        'f"Sorry, I encountered an error ({error_type}).\\n"',
        't("oc.run.encountered_error") + f"{error_type}).\\n"',
    ),
    # 同块末尾静态行（隐式拼接在 f"{status_hint}" 之后）：旧补丁全局 replace 曾覆盖此处，
    # 新方案按整段表达式锚定只命中了 request_failed 块的同句，此处需单独转 + t()。
    # 锚点含 f"{status_hint}" 前缀 + 16 空格缩进，与 request_failed 块(12 空格)不冲突。
    (
        'f"{status_hint}"\n'
        '                "Try again or use /reset to start a fresh session."',
        'f"{status_hint}"\n'
        '                + t("oc.run.try_again_fresh")',
    ),
    # 请求被 API 拒绝（L9918 status_hint 赋值，整条单行字面量，保留前导空格）。
    (
        '" The request was rejected by the API."',
        't("oc.run.request_rejected_api")',
    ),
    # 无法加载配置（L11431 f-string）：前缀译入 catalog，{exc} 保留原文用 + 显式拼接。
    (
        'f"❌ Could not load config: {exc}"',
        't("oc.run.config_load_failed") + f"{exc}"',
    ),
    # 上下文注入被拒绝（L8788 or 兜底单行字面量）。
    (
        '"Context injection refused."',
        't("oc.run.context_injection_refused")',
    ),
    # --- 澄清提问送达失败 / 用户未响应 ---
    # 澄清提问无法送达（L18042 单行字面量哨兵）。
    (
        '"[clarify prompt could not be delivered]"',
        't("oc.run.clarify_not_delivered")',
    ),
    # 用户未响应（L18048 f-string）：前缀译入 catalog，{int(timeout/60)}m] 保留原文用 + 显式拼接。
    (
        'f"[user did not respond within {int(timeout / 60)}m]"',
        't("oc.run.clarify_no_response") + f"{int(timeout / 60)}m]"',
    ),
    # --- 无活动超时提醒（L18802-18805 四段 f-string 隐式拼接，整条合一）---
    # {_elapsed_warn}/{_remaining_mins} 作 elapsed_warn/remaining_mins kwarg 传入。
    (
        'f"⚠️ No activity for {_elapsed_warn} min. "\n'
        '                                    f"If the agent does not respond soon, it will "\n'
        '                                    f"be timed out in {_remaining_mins} min. "\n'
        '                                    f"You can continue waiting or use /reset."',
        't("oc.run.inactivity_warning", elapsed_warn=_elapsed_warn, '
        'remaining_mins=_remaining_mins)',
    ),
    # --- 忙碌状态明细 status_parts（拼进 steer/queue/interrupt 与心跳）---
    # 已过 N 分钟（L3533 f-string）；{elapsed_min} 在调用点传入。
    (
        'f"{elapsed_min} min elapsed"',
        't("oc.run.status_elapsed_min", elapsed_min=elapsed_min)',
    ),
    # 迭代进度（L3535 f-string）；{iteration}/{max_iter} 在调用点传入。
    (
        'f"iteration {iteration}/{max_iter}"',
        't("oc.run.status_iteration", iteration=iteration, max_iter=max_iter)',
    ),
    # 正在运行的工具（L3537 f-string）；{current_tool} 在调用点传入。
    (
        'f"running: {current_tool}"',
        't("oc.run.status_running_tool", current_tool=current_tool)',
    ),
    # 迭代进度（L18680 心跳路径 f-string）；下标 _a['api_call_count']/_a['max_iterations']
    # 在调用点取值，作 api/max kwarg 传入。
    (
        'f"iteration {_a[\'api_call_count\']}/{_a[\'max_iterations\']}"',
        't("oc.run.status_iteration_api", api=_a[\'api_call_count\'], '
        'max=_a[\'max_iterations\'])',
    ),
    # --- 忙时 steer / queue / interrupt 确认（各两段 f-string 隐式拼接整条合一）---
    # {status_detail} 在调用点传入（为已拼好的忙碌明细尾段，可能为空）。
    (
        'f"⏩ Steered into current run{status_detail}. "\n'
        '                f"Your message arrives after the next tool call."',
        't("oc.run.busy_steered", status_detail=status_detail)',
    ),
    (
        'f"⏳ Queued for the next turn{status_detail}. "\n'
        '                f"I\'ll respond once the current task finishes."',
        't("oc.run.busy_queued", status_detail=status_detail)',
    ),
    (
        'f"⚡ Interrupting current task{status_detail}. "\n'
        '                f"I\'ll respond to your message shortly."',
        't("oc.run.busy_interrupting", status_detail=status_detail)',
    ),
    # --- Kanban 子任务通知（adapter.send，平台无关）---
    # {tag} 作 tag kwarg；下标 sub['task_id'] 在调用点取值作 task_id kwarg。
    # 完成（L5437-5438 两段 f-string 隐式拼接整条合一）；{title}/{handoff} 作 kwarg。
    (
        'f"✔ {tag}Kanban {sub[\'task_id\']} done"\n'
        '                                f" — {title}{handoff}"',
        't("oc.run.kanban_done", tag=tag, task_id=sub[\'task_id\'], '
        'title=title, handoff=handoff)',
    ),
    # 受阻（L5444 单行 f-string）；{reason} 作 kwarg。
    (
        'f"⏸ {tag}Kanban {sub[\'task_id\']} blocked{reason}"',
        't("oc.run.kanban_blocked", tag=tag, task_id=sub[\'task_id\'], reason=reason)',
    ),
    # 放弃（L5450-5451 两段 f-string 隐式拼接整条合一）；{err} 作 kwarg。
    (
        'f"✖ {tag}Kanban {sub[\'task_id\']} gave up "\n'
        '                                f"after repeated spawn failures{err}"',
        't("oc.run.kanban_gave_up", tag=tag, task_id=sub[\'task_id\'], err=err)',
    ),
    # worker 崩溃（L5455-5456 两段 f-string 隐式拼接整条合一）。
    (
        'f"✖ {tag}Kanban {sub[\'task_id\']} worker crashed "\n'
        '                                f"(pid gone); dispatcher will retry"',
        't("oc.run.kanban_crashed", tag=tag, task_id=sub[\'task_id\'])',
    ),
    # 超时（L5463-5464 两段 f-string 隐式拼接整条合一）；{limit} 作 kwarg。
    (
        'f"⏱ {tag}Kanban {sub[\'task_id\']} timed out "\n'
        '                                f"(max_runtime={limit}s); will retry"',
        't("oc.run.kanban_timed_out", tag=tag, task_id=sub[\'task_id\'], limit=limit)',
    ),
    # --- 语音消息无法转写（STT 未配置）---
    # _stt_msg 整块（5.16 五段字面量隐式拼接，32 空格缩进）：通用段复用 6.5 stt_* key，
    # 中间安装命令为 5.16 专属 pip-venv 单一变体（6.5 拆成 uv/pip 两段），单独成 key。
    (
        '"🎤 I received your voice message but can\'t transcribe it — "\n'
        '                                "no speech-to-text provider is configured.\\n\\n"\n'
        '                                "To enable voice: install faster-whisper "\n'
        '                                "(`pip install faster-whisper` in the Hermes venv) "\n'
        '                                "and set `stt.enabled: true` in config.yaml, "\n'
        '                                "then /restart the gateway."',
        't("oc.run.stt_received") + t("oc.run.stt_no_provider")\n'
        '                                + t("oc.run.stt_enable_install")\n'
        '                                + t("oc.run.stt_install_pip_venv")\n'
        '                                + t("oc.run.stt_set_enabled")\n'
        '                                + t("oc.run.stt_then_restart")',
    ),
    # 完整配置说明指引（L8678 独立 += 追加单行字面量）。
    (
        '"\\n\\nFor full setup instructions, type: `/skill hermes-agent-setup`"',
        't("oc.run.stt_setup_skill")',
    ),
    # --- 会话自动重置通知 ---
    # reason_text 三分支：suspended（普通字面量）/ daily / inactive（f-string）。
    (
        '"previous session was stopped or interrupted"',
        't("oc.run.reset_reason_suspended")',
    ),
    (
        'f"daily schedule at {policy.at_hour}:00"',
        't("oc.run.reset_reason_daily", at_hour=policy.at_hour)',
    ),
    (
        'f"inactive for {duration}"',
        't("oc.run.reset_reason_inactive", duration=duration)',
    ),
    # 整段通知（4 段 f-string 隐式拼接；{reason_text} 由上方原因变量传入）。
    (
        'f"◐ Session automatically reset ({reason_text}). "\n'
        '                            f"Conversation history cleared.\\n"\n'
        '                            f"Use /resume to browse and restore a previous session.\\n"\n'
        '                            f"Adjust reset timing in config.yaml under session_reset."',
        't("oc.run.session_reset_notice", reason_text=reason_text)',
    ),
    # --- 未设置 home channel 一次性提示（5 段隐式拼接合为一条）---
    # {platform_name} 由 platform_name.title() 传入；{sethome_cmd} 由调用点传入。
    (
        'f"📬 No home channel is set for {platform_name.title()}. "\n'
        '                    f"A home channel is where Hermes delivers cron job results "\n'
        '                    f"and cross-platform messages.\\n\\n"\n'
        '                    f"Type {sethome_cmd} to make this chat your home channel, "\n'
        '                    f"or ignore to skip."',
        't("oc.run.no_home_channel", platform_name=platform_name.title(), sethome_cmd=sethome_cmd)',
    ),
    # --- 推理过程展示 ---
    # 折叠省略行（{more} 由 len(lines) - 15 传入）。
    (
        'f"\\n_... ({len(lines) - 15} more lines)_"',
        '"\\n_... " + t("oc.run.reasoning_more_lines", more=len(lines) - 15)',
    ),
    # 推理标题前缀（整段 f-string 含 {display_reasoning}/{response} 动态段，显式 + 拼接保留）。
    (
        'f"💭 **Reasoning:**\\n```\\n{display_reasoning}\\n```\\n\\n{response}"',
        't("oc.run.reasoning_header") + f"\\n```\\n{display_reasoning}\\n```\\n\\n{response}"',
    ),
    # --- /model 会话信息（管理员诊断）---
    (
        '"default — set model.context_length in config to override"',
        't("oc.run.model_ctx_source_default")',
    ),
    (
        'f"◆ Model: `{model}`"',
        't("oc.run.model_info_model", model=model)',
    ),
    (
        'f"◆ Provider: {provider or \'openrouter\'}"',
        't("oc.run.model_info_provider", provider=provider or \'openrouter\')',
    ),
    (
        'f"◆ Context: {ctx_display} tokens ({ctx_source})"',
        't("oc.run.model_info_context", ctx_display=ctx_display, ctx_source=ctx_source)',
    ),
    (
        'f"◆ Endpoint: {base_url}"',
        't("oc.run.model_info_endpoint", base_url=base_url)',
    ),
    # --- 非管理员斜杠访问提示 ---
    # suffix 由显式 + 拼接：前缀 + 动态命令清单 + 省略号 + 后缀。前缀/后缀各自一 key。
    (
        '"You can run: "',
        't("oc.run.nonadmin_you_can_run")',
    ),
    (
        '". Use /whoami for the full list."',
        't("oc.run.nonadmin_whoami_full_list")',
    ),
    # 非管理员无任何斜杠命令可用（3 段隐式拼接合一）。
    (
        '"No slash commands are enabled for non-admins on this "\n'
        '                "platform. Ask an admin to add you to allow_admin_from "\n'
        '                "or to set user_allowed_commands."',
        't("oc.run.nonadmin_no_slash")',
    ),
    # --- /platform 平台管理命令（list / pause / resume）---
    # 子串顺序：长串排其子串之前，避免短串先命中长串内部切出病句。
    (
        '"**Gateway platforms**"',
        't("oc.run.platform_list_header")',
    ),
    # "Connected: (none)" 必须排在前缀 "Connected: " 之前。
    (
        '"Connected: (none)"',
        't("oc.run.platform_connected_none")',
    ),
    (
        '"Connected: "',
        't("oc.run.platform_connected_prefix")',
    ),
    (
        '"Failed/paused: (none)"',
        't("oc.run.platform_failed_none")',
    ),
    # 列表项 paused（2 段 f-string 隐式拼接；{p.value} 作 kwarg）。
    (
        'f"  · {p.value} — PAUSED ({reason}). "\n'
        '                            f"Resume with `/platform resume {p.value}`."',
        't("oc.run.platform_list_paused", platform=p.value, reason=reason)',
    ),
    # 列表项 retrying（{p.value}/{attempts} 作 kwarg）。
    (
        'f"  · {p.value} — retrying (attempt {attempts})"',
        't("oc.run.platform_list_retrying", platform=p.value, attempts=attempts)',
    ),
    (
        'f"Usage: /platform {action} <name>"',
        't("oc.run.platform_usage_action", action=action)',
    ),
    (
        'f"Unknown platform: {target}"',
        't("oc.run.platform_unknown", target=target)',
    ),
    # pause：不在重试队列（2 段 f-string 隐式拼接；{platform.value} 作 kwarg）。
    (
        'f"{platform.value} is not in the retry queue "\n'
        '                        f"(it\'s either connected or not enabled)."',
        't("oc.run.platform_pause_not_in_queue", platform=platform.value)',
    ),
    (
        'f"{platform.value} is already paused."',
        't("oc.run.platform_already_paused", platform=platform.value)',
    ),
    # pause：成功暂停（3 段 f-string 隐式拼接；{platform.value} 作 kwarg）。
    (
        'f"✓ {platform.value} paused. "\n'
        '                    f"Resume with `/platform resume {platform.value}` or "\n'
        '                    f"`hermes gateway restart` to reset."',
        't("oc.run.platform_paused_ok", platform=platform.value)',
    ),
    # resume：不在重试队列、无可恢复（2 段 f-string 隐式拼接；{platform.value} 作 kwarg）。
    (
        'f"{platform.value} is not in the retry queue — "\n'
        '                    f"nothing to resume."',
        't("oc.run.platform_resume_not_in_queue", platform=platform.value)',
    ),
    # resume：已在重试、无需恢复（2 段 f-string 隐式拼接；{platform.value} 作 kwarg）。
    (
        'f"{platform.value} is already retrying — "\n'
        '                    f"no resume needed."',
        't("oc.run.platform_already_retrying", platform=platform.value)',
    ),
    (
        'f"✓ {platform.value} resumed — retrying on next watcher tick."',
        't("oc.run.platform_resumed_ok", platform=platform.value)',
    ),
    # /platform 总用法帮助（4 段隐式拼接，含字面 \n）。
    (
        '"Usage: /platform <list|pause|resume> [name]\\n"\n'
        '            "  /platform list — show platform status\\n"\n'
        '            "  /platform pause <name> — stop retrying a failing platform\\n"\n'
        '            "  /platform resume <name> — re-queue a paused platform"',
        't("oc.run.platform_usage_help")',
    ),
    # ====================== 本批：杂项命令 / 确认 / 销毁性提示 ======================
    # /subgoal 添加失败错误前缀（L11728；{exc} 作 kwarg）。
    (
        'f"/subgoal: {exc}"',
        't("oc.run.subgoal_error", exc=exc)',
    ),
    # /subgoal clear 成功（L11722；{prev} 与复数尾缀 {plural_s} 作 kwarg，
    # zh 无复数概念故 catalog 中 zh 省略 {plural_s}，复数逻辑在调用点求值）。
    (
        'f"✓ Cleared {prev} subgoal{\'s\' if prev != 1 else \'\'}."',
        't("oc.run.subgoal_cleared", prev=prev, plural_s=(\'s\' if prev != 1 else \'\'))',
    ),
    # /undo 销毁性确认说明（单轮，L8249 单行字面量）。
    (
        '"This removes the last user/assistant exchange from history."',
        't("oc.run.undo_confirm")',
    ),
    # 注意：5.16 无多轮 /undo 确认变体（仅单轮），故跳过 undo_confirm_multi。
    # 销毁性 slash 确认提示框「请选择」行（前批已把整块改为 + 拼接、此字面量留待本批；
    # L14629 原文 "Choose:\n"，在改写后表达式中仍以同一字面量存在）。
    (
        '"Choose:\\n"',
        't("oc.run.confirm_choose")',
    ),
    # 销毁性 slash 确认提示框纯文本回退行（同上，L14633 字面量留待本批）。
    (
        '"_Text fallback: reply `/approve`, `/always`, or `/cancel`._"',
        't("oc.run.confirm_text_fallback")',
    ),
    # 危险命令审批原因标签（前批已把整块改为 + 拼接、此 f-string 留待本批；
    # L18164 原文 f"Reason: {desc}\n\n"，{desc} 作 kwarg）。
    (
        'f"Reason: {desc}\\n\\n"',
        't("oc.run.confirm_reason", desc=desc)',
    ),
    # 后台任务结果错误前缀（L12692；result['error'] 下标取值留调用点、作 error kwarg）。
    (
        'f"Error: {result[\'error\']}"',
        't("oc.run.bgtask_result_error", error=result[\'error\'])',
    ),
    # 网关上线广播（L15552 单行字面量）。
    (
        '"♻️ Gateway online — Hermes is back and ready."',
        't("oc.run.gateway_online")',
    ),
    # 关闭销毁性确认后的一次性提示（L14614-14616 三段隐式拼接整条合一，前导 \n\n 在 catalog）。
    (
        '"\\n\\nℹ️ Future /clear, /new, /reset, and /undo will run "\n'
        '                    "without confirmation. Re-enable via "\n'
        '                    "`approvals.destructive_slash_confirm: true` in config.yaml."',
        't("oc.run.destructive_disabled_notice")',
    ),
    # ====================== 5.16 专属文案（与 6.5 不同）======================
    # 注意：5.16 无 6.5 的 subagent 降级排队、/bundles 列表、uv pip 安装变体等文案，
    # 故跳过 subagent_working、bundles_*、stt_install_uv/pip。
    # 长任务心跳（5.16 文案「Still working...」；6.5 改为「Working — N min」措辞不同，
    # 故单独成 key）。{_elapsed_mins} 作 kwarg，{_status_detail}) 动态尾段（含闭合右括号）
    # 保留在调用点用 + 显式拼接。
    (
        'f"⏳ Still working... ({_elapsed_mins} min elapsed{_status_detail})"',
        't("oc.run.working_heartbeat_516", elapsed_mins=_elapsed_mins) + f"{_status_detail})"',
    ),
    # 心跳 _parts 的 running 段（5.16 心跳路径用下标 _a['current_tool']；与 status_parts
    # 的 status_running_tool（用 current_tool）措辞同但取值不同，分别锚定故单独成 key）。
    (
        'f"running: {_a[\'current_tool\']}"',
        't("oc.run.status_running_tool_heartbeat", current_tool=_a[\'current_tool\'])',
    ),
    # 配置的辅助压缩模型失败、已回退主模型（5.16 与 6.5 文案一致，复用 6.5 key；
    # 四段隐式拼接整条合一，{_aux_model}/{_aux_err} 作 kwarg，反引号代码标识保留原文）。
    (
        'f"ℹ️ Configured compression model `{_aux_model}` "\n'
        '                                            f"failed ({_aux_err}). Recovered using your main "\n'
        '                                            "model — context is intact — but you may want to "\n'
        '                                            "check `auxiliary.compression.model` in config.yaml."',
        't("oc.run.compression_fallback", aux_model=_aux_model, aux_err=_aux_err)',
    ),
]
REPLACEMENTS_BASE: list[tuple[str, str]] = [
    # 回复重试均失败后改发纯文本时的前缀（L3375 整条 f-string）：前缀译入 catalog，
    # {content[:3500]} 正文保留原文用 + 显式拼接。
    (
        'f"(Response formatting failed, plain text:)\\n\\n{content[:3500]}"',
        't("oc.base.format_failed_plaintext") + f"{content[:3500]}"',
    ),
    # 通用错误兜底（L4441-4443 三段相邻 f-string 隐式拼接，base 自有副本）：
    # 首段前缀与末段重试提示译入 catalog（用 oc.base.* 独立 key，不复用 oc.run.*），
    # 中间 {error_type}).\n{error_detail}\n 动态段保留原文用 + 显式拼接。
    (
        'f"Sorry, I encountered an error ({error_type}).\\n"\n'
        '                        f"{error_detail}\\n"\n'
        '                        "Try again or use /reset to start a fresh session."',
        't("oc.base.generic_error") + f"{error_type}).\\n"\n'
        '                        + f"{error_detail}\\n"\n'
        '                        + t("oc.base.try_again_fresh_base")',
    ),
    # 澄清提问数字选项回退提示（L2465 单行字面量）。
    (
        '"Reply with the number, the option text, or your own answer."',
        't("oc.base.clarify_number_hint")',
    ),
    # 原生媒体发送未被子类覆写时的纯文本回退标签（各一条单行 f-string，路径作 kwarg）。
    (
        'f"🔊 Audio: {audio_path}"',
        't("oc.base.media_audio", audio_path=audio_path)',
    ),
    (
        'f"🎬 Video: {video_path}"',
        't("oc.base.media_video", video_path=video_path)',
    ),
    (
        'f"📎 File: {file_path}"',
        't("oc.base.media_file", file_path=file_path)',
    ),
    (
        'f"🖼️ Image: {image_path}"',
        't("oc.base.media_image", image_path=image_path)',
    ),
    # 多次重试后仍投递失败（L3362-3363 两段相邻字面量隐式拼接，emoji/破折号为 \\u 转义）：
    # 两段各译入 catalog（en 还原为真实 ⚠️ / — 字符），整块隐式拼接改 + 显式拼接。
    (
        '"\\u26a0\\ufe0f Message delivery failed after multiple attempts. "\n'
        '                    "Please try again \\u2014 your request was processed but the response could not be sent."',
        't("oc.base.delivery_failed")\n'
        '                    + t("oc.base.delivery_failed_retry")',
    ),
]

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
