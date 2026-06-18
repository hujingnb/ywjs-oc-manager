#!/usr/bin/env python3
# patches/patch_i18n_literals.py
"""构建期补丁：把 hermes 里**漏翻**的用户可见英文裸字符串改成中文。

背景：hermes-agent 自带 i18n（agent/i18n.py + locales/zh.yaml），凡走 `t("...")`
的文案都能随 display.language=zh 自动切中文。但 gateway 代码里仍有一批直接发给
聊天对话框的英文**裸字符串字面量**没有包进 t()（关闭/重启通知、会话过大、后台
任务、斜杠命令回复、错误提示、配对流程、技能提示等），language 开关对它们无效。
本补丁在镜像构建期把这些裸字符串的**内部文本**就地替换为中文。

覆盖范围：仅本产品实际启用的**微信**渠道路径——平台无关的核心 gateway/run.py
与所有适配器共用的 gateway/platforms/base.py。Telegram 话题、Discord 语音、
/platform 多平台管理等**其它平台专属**文案不在范围内（微信用户看不到，翻译只会
徒增构建期锚点、增加上游升级时失配的脆弱性）。

实现约定（与 patch_api_server_reload.py 同风格）：
- 只替换字符串字面量的**可读内部文本**，不动外层引号 / f 前缀 / `{占位符}` /
  `\\n` 转义 / 命令名（/compact、/reset 等）/ 代码标识。
- 每条 `old` 是上游源码里实际存在的英文片段；若 `old` 不存在且 `new` 也不存在，
  说明上游文案结构变了，抛错中断构建（避免静默漏翻）。
- `new` 已存在即视为已打过补丁，跳过（幂等）。
- str.replace 全量替换；**当某片段是另一片段的子串时，长串必须排在短串之前**，
  否则短串会先命中长串内部，切出中英混杂病句。
"""

import pathlib
import sys

RUN = pathlib.Path("/usr/local/lib/hermes-agent/gateway/run.py")
BASE = pathlib.Path("/usr/local/lib/hermes-agent/gateway/platforms/base.py")

# ==========================================================================
# gateway/run.py 替换表
# ==========================================================================
REPLACEMENTS_RUN: list[tuple[str, str]] = [
    # --- 智能体超时诊断（无 emoji，拼进 final_response 发给对话）---
    # 整句 `Try again, or use /reset to start fresh.` 必须排在「会话过大」块的通用
    # 片段 `/reset to start fresh.` 之前，否则会被切成「use 用 /reset…」混杂病句。
    ("Last activity: {_last_desc} ({_secs_ago:.0f}s ago, ",
     "最近活动:{_last_desc}(距今 {_secs_ago:.0f} 秒, "),
    ("iteration {_iter_n}/{_iter_max}). ",
     "迭代 {_iter_n}/{_iter_max})。 "),
    ("The agent may have been waiting on an API response.",
     "智能体可能正在等待 API 响应。"),
    ("To increase the limit, set agent.gateway_timeout in config.yaml ",
     "如需提高上限,请在 config.yaml 中设置 agent.gateway_timeout "),
    ("(value in seconds, 0 = no limit) and restart the gateway.",
     "(单位为秒,0 = 不限制),然后重启网关。"),
    ("Try again, or use /reset to start fresh.",
     "请重试,或使用 /reset 开始新会话。"),

    # --- _gateway_provider_error_reply：模型服务商错误回包 ---
    ("⚠️ Provider authentication failed. Check the configured credentials; ",
     "⚠️ 模型服务商鉴权失败。请检查所配置的凭证;"),
    ("raw provider details are in the gateway logs.",
     "原始服务商错误详情见 gateway 日志。"),
    ("⚠️ The model provider rejected the request. I kept the raw provider ",
     "⚠️ 模型服务商拒绝了本次请求。我没有把原始服务商"),
    ("error out of chat; check gateway logs for details or try rephrasing.",
     "错误输出到对话中;详情见 gateway 日志,或换一种说法重试。"),
    ("⏱️ The model provider is rate-limiting requests. Please wait a moment and try again.",
     "⏱️ 模型服务商正在限流。请稍候片刻后重试。"),
    ("⚠️ The model provider failed after retries. I kept raw provider details ",
     "⚠️ 模型服务商在多次重试后仍失败。我没有把原始服务商详情"),
    ("out of chat; check gateway logs for diagnostics.",
     "输出到对话中;诊断信息见 gateway 日志。"),

    # --- 会话过大（多处重复，全量替换）---
    ("⚠️ Session too large for the model's context window.",
     "⚠️ 会话过大,超出了模型的上下文窗口。"),
    ("Use /compact to compress the conversation, or ",
     "请用 /compact 压缩对话,或"),
    ("/reset to start fresh.",
     "用 /reset 开始新会话。"),

    # --- 请求失败兜底 ---
    ("The request failed: ",
     "请求失败:"),
    ("Try again or use /reset to start a fresh session.",
     "请重试,或用 /reset 开始一个新会话。"),

    # --- 处理中止 / 无回复 ---
    ("⚠️ Processing stopped: ",
     "⚠️ 处理已中止:"),
    (". Try again.",
     "。请重试。"),
    ("⚠️ Processing completed but no response was generated. ",
     "⚠️ 处理已完成,但没有生成任何回复。"),
    ("This may be a transient error — try sending your message again.",
     "这可能是临时性错误 —— 请再发送一次你的消息。"),

    # --- 网关关闭 / 重启通知（用户最初报告的那条）---
    # `"restarting" if ... else "shutting down"` 同时是 action= 赋值与
    # _status_action_gerund() 的返回，去掉前缀以同时覆盖两处。
    ('"restarting" if self._restart_requested else "shutting down"',
     '"正在重启" if self._restart_requested else "正在关闭"'),
    ("Your current task will be interrupted. ",
     "你当前的任务将被中断。"),
    ("Send any message after restart and I'll try to resume where you left off.",
     "重启完成后发送任意消息,我会尝试从你上次离开的地方继续。"),
    ("Your current task will be interrupted.",
     "你当前的任务将被中断。"),
    ('f"⚠️ Gateway {action} — {hint}"',
     'f"⚠️ 网关{action} —— {hint}"'),

    # --- 工具结果后模型无回复 ---
    ("⚠️ The model returned no response after processing tool ",
     "⚠️ 模型在处理工具结果后没有返回任何回复。"),
    ("results. This can happen with some models — try again or ",
     "某些模型会出现这种情况 —— 请重试,或"),
    ("rephrase your question.",
     "换一种方式提问。"),

    # --- 会话自动重置 ---
    ("🔄 Session auto-reset — the conversation exceeded the ",
     "🔄 会话已自动重置 —— 对话超过了"),
    ("maximum context size and could not be compressed further. ",
     "最大上下文上限,且已无法继续压缩。"),
    ("Your next message will start a fresh session.",
     "你的下一条消息将开始一个新会话。"),

    # --- Steer（先放整句，再放片段，避免子串误伤）---
    ("⚠️ Steer failed: ",
     "⚠️ Steer 失败:"),
    ("⏩ Steer queued — arrives after the next tool call: ",
     "⏩ Steer 已排队 —— 将在下一次工具调用后送达:"),
    ("Usage: /steer <prompt>  (no agent is running; sending as a normal message)",
     "用法:/steer <prompt>(当前没有正在运行的智能体;将作为普通消息发送)"),
    ("Usage: /steer <prompt>",
     "用法:/steer <prompt>"),
    ("Agent still starting — /steer queued for the next turn.",
     "智能体仍在启动 —— /steer 已加入下一轮队列。"),
    ("Steer rejected (empty payload).",
     "Steer 被拒绝(内容为空)。"),
    ("No active agent — /steer queued for the next turn.",
     "没有活动的智能体 —— /steer 已加入下一轮队列。"),

    # --- /queue ---
    ("Usage: /queue <prompt>",
     "用法:/queue <prompt>"),
    ("Queued for the next turn.",
     "已加入队列,将在下一轮处理。"),
    (" queued)",
     " 条排队)"),

    # --- 智能体运行中、命令受限（整句先于片段）---
    ("Agent is running — wait or /stop first, then switch models.",
     "智能体正在运行 —— 请先等待或 /stop,再切换模型。"),
    ("Agent is running — wait or /stop first, then ",
     "智能体正在运行 —— 请先等待或 /stop,再"),
    ("change runtime.",
     "切换运行时。"),
    ("Agent is running — use /goal status / pause / clear mid-run, or /stop before setting a new goal.",
     "智能体正在运行 —— 运行中可用 /goal status / pause / clear,或先 /stop 再设置新目标。"),
    # ⏳ Agent is running — `/{name}` can't run mid-turn.（隐式拼接，按物理段译）
    ("⏳ Agent is running — `",
     "⏳ 智能体正在运行 —— `"),
    ("` can't run ",
     "` 现在不能运行"),
    ("mid-turn. Wait for the current response or `/stop` first.",
     "(回合进行中)。请等待当前回复结束,或先 `/stop`。"),
    ("⚡ Force-stopped. The agent was still starting — session unlocked.",
     "⚡ 已强制停止。智能体仍在启动中 —— 会话已解锁。"),

    # --- 网关 draining/重启时排队提示（gerund 已由上面表达式翻成中文）---
    # `⏳ Gateway is ` 必须排在 `⏳ Gateway ` 之前（前者含后者为子串）。
    ("⏳ Gateway is ",
     "⏳ 网关"),
    ("⏳ Gateway ",
     "⏳ 网关"),
    (" — queued for the next turn after it comes back.",
     " —— 已加入队列,待其恢复后于下一轮处理。"),
    (" and is not accepting another turn right now.",
     ",当前暂不接受新的回合。"),
    (" and is not accepting new work right now.",
     ",当前暂不接受新任务。"),
    ("Command `",
     "命令 `"),
    ("` was blocked by a hook.",
     "` 被某个 hook 拦截。"),

    # --- /new、/reset 销毁性确认 ---
    ("This starts a fresh session and discards the current ",
     "这将开始一个全新会话,并丢弃当前的"),
    ("conversation history.",
     "对话历史。"),
    (" cancelled. Conversation unchanged.",
     " 已取消。对话内容未改变。"),

    # --- 自定义快捷命令 ---
    ("Command returned no output.",
     "命令没有输出。"),
    ("Quick command timed out (30s).",
     "快捷命令超时(30 秒)。"),
    ("Quick command error: ",
     "快捷命令出错:"),
    # f"Quick command '/{command}' has no …" —— 前缀段也要译，否则中英混杂。
    ("Quick command '",
     "快捷命令 '"),
    ("' has no command defined.",
     "' 未定义命令。"),
    ("' has no target defined.",
     "' 未定义目标。"),
    ("' has unsupported type (supported: 'exec', 'alias').",
     "' 类型不受支持(支持:'exec'、'alias')。"),

    # --- 未知斜杠命令提示（隐式拼接，按物理段译）---
    ("Unknown command `",
     "未知命令 `"),
    ("Type /commands to see what's available, ",
     "输入 /commands 查看可用命令,"),
    ("or resend without the leading slash to send ",
     "或去掉开头的斜杠重新发送,"),
    ("as a regular message.",
     "即可作为普通消息处理。"),

    # --- 技能未安装/被禁用提示（f-string 多物理段；`\\n` 为源码字面转义）---
    # `The **` 是两条技能消息共有的前缀段，统一去掉「The」让其读作「技能 **名**」。
    ("The **",
     "技能 **"),
    ("** skill is installed but disabled.\\n",
     "** 已安装但被禁用。\\n"),
    ("Enable it with: `hermes skills config`",
     "用 `hermes skills config` 启用"),
    ("** skill is available but not installed.\\n",
     "** 可用但尚未安装。\\n"),
    ("Install it with: `hermes skills install ",
     "用 `hermes skills install "),
    # 第三条技能消息：f"The **{name}** skill is disabled for {platform}.\\n"
    ("** skill is disabled for ",
     "** 已在以下平台被禁用:"),

    # --- 配对流程（未识别用户首次私聊；按物理段译，`\\n` 为字面转义）---
    ("Hi~ I don't recognize you yet!\\n\\n",
     "你好~ 我还不认识你!\\n\\n"),
    ("Here's your pairing code: `",
     "这是你的配对码:`"),
    ("Ask the bot owner to run:\\n",
     "请让机器人管理员运行:\\n"),
    ("Too many pairing requests right now~ ",
     "当前配对请求过多~ "),
    ("Please try again later!",
     "请稍后再试!"),

    # --- 更新进程交互（/approve 转发给更新进程）---
    ("✗ Failed to send response to update process: ",
     "✗ 发送回复到更新进程失败:"),
    ("✓ Sent `",
     "✓ 已发送 `"),
    ("` to the update process.",
     "` 到更新进程。"),
    ("⚕ **Update needs your input:**\\n\\n",
     "⚕ **更新需要你的输入:**\\n\\n"),
    ("Reply `/approve` (yes) or `/deny` (no), ",
     "回复 `/approve`(同意)或 `/deny`(拒绝),"),
    ("or type your answer directly.",
     "或直接输入你的回答。"),

    # --- 上下文压缩中止 ---
    ("⚠️ Context compression aborted ",
     "⚠️ 上下文压缩已中止"),
    (". No messages were dropped — ",
     "。没有丢弃任何消息 —— "),
    ("conversation is unchanged. Run /compress ",
     "对话内容未改变。请运行 /compress "),
    ("to retry, /reset for a clean session, or ",
     "重试,或用 /reset 开始干净会话,或"),
    ("check your auxiliary.compression model ",
     "检查你的 auxiliary.compression 模型"),

    # --- 目标 / 子目标命令 ---
    ("No active goal. Set one with /goal <text>.",
     "当前没有活动目标。用 /goal <text> 设置一个。"),
    ("Usage: /subgoal remove <n>",
     "用法:/subgoal remove <n>"),
    ("/subgoal remove: <n> must be an integer (1-based index).",
     "/subgoal remove:<n> 必须是整数(从 1 开始计)。"),
    ("/subgoal remove: ",
     "/subgoal remove:"),
    ("✓ Removed subgoal ",
     "✓ 已移除子目标 "),
    ("/subgoal clear: ",
     "/subgoal clear:"),
    ("No subgoals to clear.",
     "没有可清除的子目标。"),
    ("✓ Added subgoal ",
     "✓ 已添加子目标 "),

    # --- 权限层级信息（/access 等；多物理段，`\\n` 为字面转义）---
    (" is admin-only here. ",
     " 在此仅管理员可用。"),
    ("**You** — ",
     "**你** —— "),
    ("User ID: `",
     "用户 ID:`"),
    ("Tier: unrestricted (no admin list configured for this scope)\\n",
     "权限层级:不受限(此范围未配置管理员名单)\\n"),
    ("Tier: **admin**\\n",
     "权限层级:**管理员**\\n"),
    ("Tier: user\\n",
     "权限层级:普通用户\\n"),
    ("Slash commands: all available",
     "可用斜杠命令:全部"),
    ("Slash commands you can run: ",
     "你可运行的斜杠命令:"),

    # --- 技能 bundles（多物理段）---
    ("Bundles subsystem unavailable: ",
     "Bundles 子系统不可用:"),
    ("No skill bundles installed.\\n",
     "尚未安装任何技能 bundle。\\n"),
    ("Create one on the host with:\\n",
     "请在宿主机用以下命令创建:\\n"),
    ("Directory: `",
     "目录:`"),

    # --- 后台任务 ---
    # `❌ Background task ` 带 ❌ 前缀，避免误伤 `✅ Background task complete`。
    ("❌ Background task ",
     "❌ 后台任务 "),
    (" failed: no provider credentials configured.",
     " 失败:未配置任何模型服务商凭证。"),
    (" failed: {e}",
     " 失败:{e}"),
    ("✅ Background task complete",
     "✅ 后台任务完成"),
    ("(No response generated)",
     "(未生成任何回复)"),

    # --- 危险命令审批（confirm 命令；本部署 approvals=off 通常不触发，仍翻译兜底）---
    ("⚠️ **Confirm /",
     "⚠️ **确认 /"),
    ("• **Approve Once** — proceed this time only",
     "• **本次批准** —— 仅本次放行"),
    ("• **Always Approve** — proceed and silence this prompt permanently",
     "• **始终批准** —— 放行并永久不再提示"),
    ("• **Cancel** — keep current conversation",
     "• **取消** —— 保持当前对话不变"),
    ("⚠️ **Dangerous command requires approval:**",
     "⚠️ **危险命令需要审批:**"),
    ("Reply `/approve` to execute, `/approve session` to approve this pattern ",
     "回复 `/approve` 执行,`/approve session` 对本会话内该模式放行,"),
    ("for the session, `/approve always` to approve permanently, or `/deny` to cancel.",
     "`/approve always` 永久放行,或 `/deny` 取消。"),

    # --- hermes 自更新通知 ---
    ("✅ Hermes update finished.",
     "✅ Hermes 更新已完成。"),
    ("❌ Hermes update failed (exit code {}).",
     "❌ Hermes 更新失败(退出码 {})。"),
    ("❌ Hermes update timed out after 30 minutes.",
     "❌ Hermes 更新超过 30 分钟超时。"),
    ("✅ Hermes update finished successfully.",
     "✅ Hermes 更新成功完成。"),
    ("❌ Hermes update failed. Check the gateway logs or run `hermes update` manually for details.",
     "❌ Hermes 更新失败。详情见 gateway 日志,或手动运行 `hermes update` 查看。"),
    ("♻ Gateway restarted successfully. Your session continues.",
     "♻ 网关已成功重启,你的会话将继续。"),

    # --- 代理模式错误 ---
    ("⚠️ Proxy mode requires aiohttp. Install with: pip install aiohttp",
     "⚠️ 代理模式需要 aiohttp。请用 pip install aiohttp 安装。"),
    ("⚠️ Proxy URL not configured (GATEWAY_PROXY_URL or gateway.proxy_url)",
     "⚠️ 未配置代理地址(GATEWAY_PROXY_URL 或 gateway.proxy_url)"),
    ("⚠️ Proxy error (",
     "⚠️ 代理错误("),
    ("⚠️ Proxy connection error: ",
     "⚠️ 代理连接错误:"),
    ("(No response from remote agent)",
     "(远端智能体无响应)"),

    # --- run_agent 鉴权失败 / 通用错误 ---
    ("⚠️ Provider authentication failed: ",
     "⚠️ 模型服务商鉴权失败:"),
    ("Sorry, I encountered an error (",
     "抱歉,我遇到了错误("),
    (" The request was rejected by the API.",
     " 该请求被 API 拒绝。"),
    ("❌ Could not load config: ",
     "❌ 无法加载配置:"),
    ("Context injection refused.",
     "上下文注入被拒绝。"),

    # --- 澄清提问送达失败 / 用户未响应 ---
    ("[clarify prompt could not be delivered]",
     "[澄清提问无法送达]"),
    ("[user did not respond within ",
     "[用户未在 "),

    # --- 无活动超时提醒（修复此前半翻造成的中英混杂；`be timed out in ` 排在
    #     `Agent is running … then ` 之类无关，但 ` min. ` 为本消息两段共用，
    #     统一译为「 分钟。」，靠构建期 diff 复核未误伤其它处）---
    ("⚠️ No activity for ",
     "⚠️ 已闲置 "),
    (" min. ",
     " 分钟。"),
    ("If the agent does not respond soon, it will ",
     "若仍无响应,"),
    ("be timed out in ",
     "超时倒计时 "),
    ("You can continue waiting or use /reset.",
     "你可以继续等待,或使用 /reset。"),

    # ======================================================================
    # 二次穷举审计补漏（2026-06-18，全量补齐，与另一 variant 同步）。
    # 以下均为经 adapter.send / 斜杠命令 return / final_response 发往对话框、
    # 但首轮未覆盖的用户可见英文串；多因 f-string 插值（{status_detail} 等）
    # 打断锚点、与已译串措辞相近、或无 emoji 的尾段。仅覆盖微信走的平台无关
    # 路径；其它平台专属文案仍不译。
    # ======================================================================

    # --- 忙碌状态明细 status_parts（拼进下方 steer/queue/interrupt 与心跳）---
    ("{elapsed_min} min elapsed", "已过 {elapsed_min} 分钟"),
    ("iteration {iteration}/{max_iter}", "迭代 {iteration}/{max_iter}"),
    ("running: {current_tool}", "运行中: {current_tool}"),
    ("iteration {_a['api_call_count']}/{_a['max_iterations']}",
     "迭代 {_a['api_call_count']}/{_a['max_iterations']}"),

    # --- 忙时 steer / queue / interrupt 确认 ---
    ("⏩ Steered into current run{status_detail}. ", "⏩ 已切入当前运行{status_detail}。"),
    ("Your message arrives after the next tool call.", "你的消息将在下一次工具调用后送达。"),
    ("⏳ Queued for the next turn{status_detail}. ", "⏳ 已加入下一轮队列{status_detail}。"),
    ("I'll respond once the current task finishes.", "当前任务完成后我会回复。"),
    ("⚡ Interrupting current task{status_detail}. ", "⚡ 正在中断当前任务{status_detail}。"),
    ("I'll respond to your message shortly.", "我会尽快回复你的消息。"),

    # --- Kanban 子任务通知（adapter.send，平台无关）---
    ("✔ {tag}Kanban {sub['task_id']} done", "✔ {tag}看板 {sub['task_id']} 已完成"),
    (" — {title}{handoff}", " —— {title}{handoff}"),
    ("⏸ {tag}Kanban {sub['task_id']} blocked{reason}", "⏸ {tag}看板 {sub['task_id']} 受阻{reason}"),
    ("✖ {tag}Kanban {sub['task_id']} gave up ", "✖ {tag}看板 {sub['task_id']} 已放弃"),
    ("after repeated spawn failures{err}", "(多次启动失败){err}"),
    ("✖ {tag}Kanban {sub['task_id']} worker crashed ", "✖ {tag}看板 {sub['task_id']} worker 崩溃 "),
    ("(pid gone); dispatcher will retry", "(进程已消失);调度器将重试"),
    ("⏱ {tag}Kanban {sub['task_id']} timed out ", "⏱ {tag}看板 {sub['task_id']} 超时 "),
    ("(max_runtime={limit}s); will retry", "(max_runtime={limit}s);将重试"),

    # --- 语音消息无法转写（STT 未配置）---
    ("🎤 I received your voice message but can't transcribe it — ",
     "🎤 收到了你的语音消息,但无法转写 —— "),
    ("no speech-to-text provider is configured.\\n\\n",
     "未配置语音转文字(STT)服务。\\n\\n"),
    ("To enable voice: install faster-whisper ", "启用语音功能:安装 faster-whisper "),
    ("and set `stt.enabled: true` in config.yaml, ",
     "并在 config.yaml 中设置 `stt.enabled: true`,"),
    ("then /restart the gateway.", "然后用 /restart 重启网关。"),
    ("\\n\\nFor full setup instructions, type: `/skill hermes-agent-setup`",
     "\\n\\n完整配置说明请输入:`/skill hermes-agent-setup`"),

    # --- 会话自动重置通知 ---
    ("previous session was stopped or interrupted", "上一会话已被停止或中断"),
    ("daily schedule at {policy.at_hour}:00", "每日定时 {policy.at_hour}:00"),
    ("inactive for {duration}", "已闲置 {duration}"),
    ("◐ Session automatically reset ({reason_text}). ",
     "◐ 会话已自动重置({reason_text})。"),
    ("Conversation history cleared.\\n", "对话历史已清空。\\n"),
    ("Use /resume to browse and restore a previous session.\\n",
     "使用 /resume 浏览并恢复此前的会话。\\n"),
    ("Adjust reset timing in config.yaml under session_reset.",
     "可在 config.yaml 的 session_reset 下调整重置时机。"),

    # --- 未设置 home channel 一次性提示 ---
    ("📬 No home channel is set for {platform_name.title()}. ",
     "📬 尚未为 {platform_name.title()} 设置主频道。"),
    ("A home channel is where Hermes delivers cron job results ",
     "主频道是 Hermes 投递定时任务结果"),
    ("and cross-platform messages.\\n\\n", "和跨平台消息的地方。\\n\\n"),
    ("Type {sethome_cmd} to make this chat your home channel, ",
     "输入 {sethome_cmd} 即可将本聊天设为主频道,"),
    ("or ignore to skip.", "或忽略以跳过。"),

    # --- 推理过程展示 ---
    ("({len(lines) - 15} more lines)_", "(还有 {len(lines) - 15} 行)_"),
    ("💭 **Reasoning:**", "💭 **推理过程:**"),

    # --- /model 会话信息（管理员诊断；ctx_source 的 config/detected 单词态
    #     因全局碰撞风险不替换，仅译可安全唯一匹配的句子）---
    ("default — set model.context_length in config to override",
     "默认 —— 在 config 中设置 model.context_length 可覆盖"),
    ("◆ Model: `{model}`", "◆ 模型: `{model}`"),
    ("◆ Provider: {provider or 'openrouter'}", "◆ 服务商: {provider or 'openrouter'}"),
    ("◆ Context: {ctx_display} tokens ({ctx_source})",
     "◆ 上下文: {ctx_display} tokens ({ctx_source})"),
    ("◆ Endpoint: {base_url}", "◆ 端点: {base_url}"),

    # --- 非管理员斜杠访问提示 ---
    ("You can run: ", "你可以使用: "),
    (". Use /whoami for the full list.", "。使用 /whoami 查看完整列表。"),
    ("No slash commands are enabled for non-admins on this ",
     "本平台未对非管理员启用任何斜杠命令。"),
    ("platform. Ask an admin to add you to allow_admin_from ",
     "请联系管理员把你加入 allow_admin_from,"),
    ("or to set user_allowed_commands.", "或设置 user_allowed_commands。"),

    # --- /platform 平台管理命令（list / pause / resume）---
    ("**Gateway platforms**", "**网关平台**"),
    ("Connected: (none)", "已连接: (无)"),
    ("Connected: ", "已连接: "),
    ("Failed/paused: (none)", "失败/暂停: (无)"),
    (" — PAUSED ({reason}). ", " —— 已暂停({reason})。"),
    ("Resume with `/platform resume {p.value}`.", "用 `/platform resume {p.value}` 恢复。"),
    (" — retrying (attempt {attempts})", " —— 重试中(第 {attempts} 次)"),
    ("Usage: /platform {action} <name>", "用法:/platform {action} <name>"),
    ("Unknown platform: {target}", "未知平台:{target}"),
    # 「不在重试队列中 — nothing to resume」整段（含尾破折号）必须排在仅含尾空格的
    # 同前缀 pause 变体之前，否则短串先命中长串内部切出病句。
    ("{platform.value} is not in the retry queue — ",
     "{platform.value} 不在重试队列中 —— "),
    ("nothing to resume.", "无可恢复项。"),
    ("{platform.value} is not in the retry queue ",
     "{platform.value} 不在重试队列中"),
    ("(it's either connected or not enabled).", "(它要么已连接,要么未启用)。"),
    ("{platform.value} is already paused.", "{platform.value} 已处于暂停状态。"),
    ("✓ {platform.value} paused. ", "✓ {platform.value} 已暂停。"),
    ("Resume with `/platform resume {platform.value}` or ",
     "用 `/platform resume {platform.value}` 或 "),
    ("`hermes gateway restart` to reset.", "`hermes gateway restart` 重置以恢复。"),
    ("{platform.value} is already retrying — ", "{platform.value} 已在重试中 —— "),
    ("no resume needed.", "无需恢复。"),
    ("✓ {platform.value} resumed — retrying on next watcher tick.",
     "✓ {platform.value} 已恢复 —— 将在下次巡检时重试。"),
    ("Usage: /platform <list|pause|resume> [name]\\n",
     "用法:/platform <list|pause|resume> [name]\\n"),
    ("  /platform list — show platform status\\n",
     "  /platform list —— 显示平台状态\\n"),
    ("  /platform pause <name> — stop retrying a failing platform\\n",
     "  /platform pause <name> —— 停止重试某个故障平台\\n"),
    ("  /platform resume <name> — re-queue a paused platform",
     "  /platform resume <name> —— 重新排队某个已暂停平台"),

    # --- /subgoal 错误前缀 ---
    ("/subgoal: {exc}", "/subgoal:{exc}"),

    # --- /undo 销毁性确认说明（单轮）---
    ("This removes the last user/assistant exchange from history.",
     "这将从历史中移除最近一轮用户/助手对话。"),

    # --- 危险命令确认提示框骨架（与已覆盖的 Approve/Cancel 选项配套）---
    ("Choose:\\n", "请选择:\\n"),
    ("_Text fallback: reply `/approve`, `/always`, or `/cancel`._",
     "_纯文本回退:回复 `/approve`、`/always` 或 `/cancel`。_"),

    # --- 后台任务结果错误前缀 ---
    ("Error: {result['error']}", "出错: {result['error']}"),

    # --- 危险命令审批：原因标签 ---
    ("Reason: {desc}\\n\\n", "原因: {desc}\\n\\n"),

    # --- 网关上线广播 ---
    ("♻️ Gateway online — Hermes is back and ready.",
     "♻️ 网关已上线 —— Hermes 已恢复就绪。"),

    # --- Hermes 自更新失败（带输出代码块）---
    ("❌ Hermes update failed.\\n\\n", "❌ Hermes 更新失败。\\n\\n"),

    # --- /subgoal clear 成功（中文无复数，丢弃 {'s' ...} 占位逻辑）---
    ("✓ Cleared ", "✓ 已清除 "),
    (" subgoal{'s' if prev != 1 else ''}.", " 个子目标。"),

    # --- 关闭销毁性确认后的一次性提示 ---
    ("\\n\\nℹ️ Future /clear, /new, /reset, and /undo will run ",
     "\\n\\nℹ️ 今后 /clear、/new、/reset 和 /undo 将直接执行,"),
    ("without confirmation. Re-enable via ", "无需确认。如需重新启用,请用 "),
    ("`approvals.destructive_slash_confirm: true` in config.yaml.",
     "config.yaml 中的 `approvals.destructive_slash_confirm: true`。"),

    # --- 闲置超时诊断：卡在工具分支 + 首行（else「Last activity」分支与尾段
    #     已由上方超时诊断段覆盖）。无尾空格的 `iteration {_iter_n}/{_iter_max}).`
    #     排在带尾空格同名锚点之后，靠列表顺序保证不误伤。---
    ("⏱️ Agent inactive for {_timeout_mins} min — no tool calls ",
     "⏱️ 智能体已闲置 {_timeout_mins} 分钟 —— 无工具调用"),
    ("or API responses.", "也无 API 响应。"),
    ("The agent appears stuck on tool ", "智能体疑似卡在工具 "),
    ("({_secs_ago:.0f}s since last activity, ", "(距上次活动 {_secs_ago:.0f} 秒, "),
    ("iteration {_iter_n}/{_iter_max}).", "迭代 {_iter_n}/{_iter_max})。"),

    # ====================== 6.5 专属（与 5.16 文案不同）======================
    # 长任务心跳（6.5 文案；5.16 为「⏳ Still working... (N min elapsed)」）
    ("⏳ Working — {_elapsed_mins} min", "⏳ 处理中 —— {_elapsed_mins} 分钟"),
    # 忙时 subagent 降级变体（6.5 独有：子智能体运行中时排队提示）
    ("⏳ Subagent working{status_detail} — your message is queued for ",
     "⏳ 子智能体运行中{status_detail} —— 你的消息已排队,"),
    ("when it finishes (use /stop to cancel everything).",
     "将在其完成后处理(用 /stop 取消全部)。"),
    # /undo 多轮变体（6.5 独有）
    ("This removes the last {_undo_n} user turns from history.",
     "这将从历史中移除最近 {_undo_n} 轮用户对话。"),
    # 语音 STT 安装命令（6.5 文案：uv pip，附 pip 备注）
    ("(`uv pip install faster-whisper` in the Hermes venv; ",
     "(在 Hermes venv 中执行 `uv pip install faster-whisper`;"),
    ("`pip install faster-whisper` also works if pip is on PATH) ",
     "若 pip 在 PATH 上,`pip install faster-whisper` 亦可) "),
    # /bundles 命令（6.5 独有；5.16 无此命令）
    ("**Skill Bundles** ({len(bundles)} installed):",
     "**技能 Bundle**(已安装 {len(bundles)} 个):"),
    ("Load {skill_count} skills", "加载 {skill_count} 个技能"),
    (" skills)_", " 个技能)_"),
    ("Invoke a bundle with `/<slug>` to load all its skills.",
     "用 `/<slug>` 调用某个 bundle 即可加载其全部技能。"),
    # 配置的辅助压缩模型失败、已回退主模型（6.5；5.16 已在压缩段覆盖）
    ("ℹ️ Configured compression model ", "ℹ️ 所配置的压缩模型 "),
    ("failed ({_aux_err}). Recovered using your main ",
     "失败({_aux_err})。已回退使用你的主"),
    ("model — context is intact — but you may want to ",
     "模型(上下文完好),但你可能需要"),
    ("check `auxiliary.compression.model` in config.yaml.",
     "检查 config.yaml 中的 `auxiliary.compression.model`。"),
    # aborted 压缩告警块末段「configuration.」（首轮补丁遗漏，补齐避免中英混排）
    ("configuration.", "配置。"),
]

# ==========================================================================
# gateway/platforms/base.py 替换表（所有适配器共用，含微信）
# ==========================================================================
REPLACEMENTS_BASE: list[tuple[str, str]] = [
    # 回复格式化失败时的纯文本兜底前缀
    ("(Response formatting failed, plain text:)\\n\\n",
     "(回复格式化失败,以纯文本显示:)\\n\\n"),
    # 通用错误兜底（与 run.py 同串，分文件各自替换）
    ("Sorry, I encountered an error (",
     "抱歉,我遇到了错误("),
    ("Try again or use /reset to start a fresh session.",
     "请重试,或用 /reset 开始一个新会话。"),

    # 二次审计补漏（与 run.py 同批）：base 适配器默认实现里的用户可见串。
    # 澄清提问（ask_clarify）数字选项回退提示
    ("Reply with the number, the option text, or your own answer.",
     "请回复序号、选项文字,或你自己的答案。"),
    # 原生媒体发送未被子类覆写时的纯文本回退标签（仅译英文词，路径原样保留）
    ("🔊 Audio: {audio_path}", "🔊 音频: {audio_path}"),
    ("🎬 Video: {video_path}", "🎬 视频: {video_path}"),
    ("📎 File: {file_path}", "📎 文件: {file_path}"),
    ("🖼️ Image: {image_path}", "🖼️ 图片: {image_path}"),
    # 多次重试后仍投递失败（⚠️/— 在源码里是 \\u26a0\\ufe0f / \\u2014 转义，
    # 故首段跳过 emoji 转义只译 ASCII 文本，第二段连同 \\u2014 整体匹配）
    ("Message delivery failed after multiple attempts. ",
     "多次尝试后仍未能送达消息。"),
    ("Please try again \\u2014 your request was processed but the response could not be sent.",
     "请重试,你的请求已处理,但回复未能发送。"),
]

# 构建期处理的目标文件 → 替换表
TARGETS: list[tuple[pathlib.Path, list[tuple[str, str]]]] = [
    (RUN, REPLACEMENTS_RUN),
    (BASE, REPLACEMENTS_BASE),
]


def patch(content: str, replacements: list[tuple[str, str]]) -> str:
    """对单个文件文本逐条应用替换；返回新内容。

    任一 old 既找不到、对应 new 也不存在 → 上游结构变更，收集全部缺失项后一次性
    抛错（便于一次看清所有需更新的锚点）。new 已存在即视为已打过补丁（幂等跳过）。
    单元测试可传入仅含 fake 内容相关锚点的小表，以隔离测试算法本身。
    """
    replaced: list[str] = []
    already: list[str] = []
    missing: list[str] = []
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


def main() -> int:
    rc = 0
    for target, repls in TARGETS:
        if not target.exists():
            print(f"[patch_i18n_literals] 目标文件不存在: {target}", file=sys.stderr)
            return 1
        original = target.read_text(encoding="utf-8")
        patched = patch(original, repls)
        if patched != original:
            target.write_text(patched, encoding="utf-8")
            print(f"[patch_i18n_literals] 已写回 {target}")
        else:
            print(f"[patch_i18n_literals] {target} 内容未变化（全部幂等跳过）")
    return rc


if __name__ == "__main__":
    raise SystemExit(main())
