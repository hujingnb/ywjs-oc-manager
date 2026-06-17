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
