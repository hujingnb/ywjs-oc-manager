# ocops/channel.py（先实现 status/unbind 同步部分；login 在 Task 6 追加）
"""渠道绑定运维：weixin 账号目录读写。从 oc-channel-status/unbind 抽取核心逻辑。

设计约束：manager 不解析凭证字段；status 仅判定是否存在账号文件，unbind 直接删目录
（幂等），凭证由 hermes 自管。未知 channel 一律 BAD_REQUEST（HTTP 400）。"""
from __future__ import annotations

import asyncio
import contextlib
import io
import shutil
from pathlib import Path

from ocops.conversation import _API_BASE, _api_server_key
from ocops.errors import OpsError

# 二维码 URL 前缀：hermes 上游 qr_login 把扫码 URL print 到 stdout，所有
# 微信扫码 URL 都以此前缀开头。与 manager 侧 wechat_runner.go 的识别逻辑
# (strings.HasPrefix(line, "https://liteapp.weixin.qq.com/")) 保持一致。
_WEIXIN_QR_PREFIX = "https://liteapp.weixin.qq.com/"

# hermes 在账号目录里除账号本体 <id>.json 外，还会为同一账号写 sidecar 文件
# （context-tokens 令牌缓存、sync 长轮询游标，见 hermes weixin.py：
# <id>.context-tokens.json / <id>.sync.json）。它们与账号同前缀但不是账号本体，
# 枚举账号时必须排除——否则 sorted(iterdir())[0] 会把 ".context-tokens" 误当
# account_id（账号 id 自身含点号，无法靠分割点号区分），导致绑定身份显示错误。
_WEIXIN_ACCOUNT_SIDECAR_SUFFIXES = (".context-tokens.json", ".sync.json")


def channel_status(channel: str, data_root: Path) -> dict:
    """查询渠道绑定态：weixin 走 accounts 文件态，feishu 走 api_server /health/detailed。

    weixin 返回结构：
      - 未绑定：{"channel": "weixin", "bound": False}
      - 已绑定：{"channel": "weixin", "bound": True, "account_id": "<id>"}
    account_id 取 accounts/<id>.json 文件名去掉 .json 后缀；
    同一目录下只取第一个（当前单账号绑定语义）。

    feishu 没有本地账号文件（凭证经环境变量注入引擎），其「是否绑定」只能由
    引擎实际连接结果判定，故委托 _feishu_status 读运行态。未知 channel 抛 BAD_REQUEST。
    """
    if channel == "feishu":
        # 飞书绑定态以引擎运行态为准，不查本地文件。
        return _feishu_status()
    if channel != "weixin":
        raise OpsError("BAD_REQUEST", f"unknown channel: {channel}")
    accounts_dir = data_root / "weixin" / "accounts"
    if not accounts_dir.exists():
        # 目录不存在：从未绑定或已解绑。
        return {"channel": "weixin", "bound": False}
    for entry in sorted(accounts_dir.iterdir()):
        if not (entry.is_file() and entry.suffix == ".json"):
            continue
        # 跳过账号 sidecar 文件（context-tokens / sync），只认账号本体 <id>.json。
        if entry.name.endswith(_WEIXIN_ACCOUNT_SIDECAR_SUFFIXES):
            continue
        # 当前只支持单账号绑定，遇到第一个真正的账号文件即返回。
        return {"channel": "weixin", "bound": True,
                "account_id": entry.name[: -len(".json")]}
    # 目录存在但无账号文件：视为未绑定。
    return {"channel": "weixin", "bound": False}


def _feishu_status() -> dict:
    """读 hermes api_server /health/detailed 的 platforms.feishu 运行态，映射为渠道绑定态。

    引擎把各渠道运行态写入 runtime status 的 platforms.<name> 下，字段为
    state / error_code / error_message（见引擎 gateway/status.py write_runtime_status），
    api_server /health/detailed 原样透出该 platforms 字典（见引擎
    gateway/platforms/api_server.py _handle_health_detailed）。注意引擎里键名是
    state 而非 platform_state，本函数读 state、对外仍以 platform_state 暴露给 manager
    以稳定渠道契约。映射规则：
      - state == "connected" → bound=True（已成功连上飞书开放平台）
      - state == "fatal"     → bound=False，携带 error_code / error_message 供前端展示原因
      - 其他（connecting / retrying / disconnected / 字段缺失）→ bound=False，pending 态

    引擎 runtime status 不写 bot_open_id，故 bound 态下 bot_open_id 多为空串（best-effort）。
    api_server 不可达或响应非 JSON 时抛 INTERNAL，由上层映射为 HTTP 5xx。
    """
    import json as _json
    import urllib.request as _u

    # 复用 conversation 的 api_server 回环地址与 Bearer key 来源，避免硬编码与重复取 key 逻辑。
    req = _u.Request(_API_BASE + "/health/detailed", method="GET")
    key = _api_server_key()
    if key:
        # /health/detailed 本身不鉴权，但带上 key 与其它转发保持一致、无副作用。
        req.add_header("Authorization", "Bearer " + key)
    try:
        with _u.urlopen(req, timeout=10) as resp:
            data = _json.loads(resp.read().decode("utf-8"))
    except Exception as e:  # noqa: BLE001 - 网络/解析失败统一映射为 INTERNAL
        raise OpsError("INTERNAL", f"查询 /health/detailed 失败: {e}")
    fe = (data.get("platforms") or {}).get("feishu") or {}
    # 引擎字段为 state；缺失时视为尚未连接。
    state = fe.get("state", "")
    if state == "connected":
        return {"channel": "feishu", "bound": True, "platform_state": state,
                "bot_open_id": fe.get("bot_open_id", "")}
    if state == "fatal":
        return {"channel": "feishu", "bound": False, "platform_state": state,
                "error_code": fe.get("error_code", "") or "",
                "error_message": fe.get("error_message", "") or ""}
    # connecting / retrying / disconnected / 字段缺失：统一归为「连接中」待定态。
    return {"channel": "feishu", "bound": False, "platform_state": state or "connecting"}


def channel_unbind(channel: str, data_root: Path) -> dict:
    """解绑渠道：删除账号目录（幂等）；未知 channel 抛 BAD_REQUEST。

    直接删除 <data_root>/weixin/accounts/ 目录树，hermes 下次扫描
    platforms 配置时识别为未绑定状态。目录不存在时也视为已解绑（幂等语义）。
    """
    if channel != "weixin":
        raise OpsError("BAD_REQUEST", "unknown channel")
    accounts_dir = data_root / "weixin" / "accounts"
    if accounts_dir.exists():
        # 整体删除账号目录；hermes 下次扫描会判定为未绑定状态。
        shutil.rmtree(accounts_dir)
    return {"status": "unbound"}


class _QRLineWriter(io.StringIO):
    """捕获 qr_login print 输出的可写对象。

    qr_login 在同步阶段把二维码 URL print 到 stdout（经 redirect_stdout 接管），
    在 polling 阶段还可能 print 其它日志。这里在 write 时按行缓冲，识别以
    _WEIXIN_QR_PREFIX 开头的完整行后用 queue.put_nowait 投递给主循环，
    使二维码事件能在 qr_login 协程返回（bound/timeout）之前被 yield 出去。

    非 QR 行直接丢弃（不影响对外协议；CLI shim 侧只关心 QR 行与终态）。
    """

    def __init__(self, queue: asyncio.Queue):
        super().__init__()
        # 事件队列：识别到的二维码 URL 行投递到此处供主循环消费。
        self._queue = queue
        # 行缓冲：print 可能分多次 write（内容 + "\n"），需自行按 "\n" 切分。
        self._buffer = ""

    def write(self, s: str) -> int:
        # 累积到行缓冲，逐个完整行（以 "\n" 结尾）解析。
        self._buffer += s
        while "\n" in self._buffer:
            line, self._buffer = self._buffer.split("\n", 1)
            stripped = line.strip()
            if stripped.startswith(_WEIXIN_QR_PREFIX):
                # 命中二维码 URL 行：投递给主循环转成 qrcode 事件。
                # put_nowait 安全：队列无界（maxsize=0），不会阻塞 qr_login。
                self._queue.put_nowait(stripped)
        return len(s)


async def channel_login(channel: str):
    """微信扫码登录的 async 事件流：先 yield qrcode 事件，最后 yield bound/timeout/failed。

    复用 hermes 上游 qr_login（venv 内可 import）。qr_login 在登录全程才返回、
    而二维码 URL 在中途被 print，因此需要并发：用 asyncio.Queue + 一个把
    redirect_stdout 捕获到的 QR 行推入队列的 writer；主循环在「等待队列新事件」
    与「等待 qr_login 任务完成」之间竞速，保证 qrcode 事件能在 bound/timeout
    之前 yield 出去。

    事件序列：
      - 未知 channel → 单条 {"event":"failed","reason":"unknown channel: <channel>"}
      - SDK 不可用 → 单条 {"event":"failed","reason":"hermes SDK not available: ..."}
      - 正常 → 零或多条 {"event":"qrcode","url":...}，末尾 {"event":"bound"}（cred truthy）
        或 {"event":"timeout"}（返回 None）
      - qr_login 抛异常 → 已 yield 的 qrcode 之后补一条
        {"event":"failed","reason":...}（不向上抛，保证 SSE 流优雅结束）

    刻意不抛异常：所有失败都降级为 failed 事件，让上层 SSE 端点能把终态写完
    再关闭流。
    """
    if channel != "weixin":
        # 异常路径：未知 channel 直接给出 failed 终态并结束。
        yield {"event": "failed", "reason": f"unknown channel: {channel}"}
        return
    try:
        # 上游 SDK 仅在 hermes 容器 venv 内安装；本地/CI 通常不可用。
        from gateway.platforms.weixin import qr_login
    except ImportError as e:
        # SDK 缺失：降级为 failed 事件（不抛），保证流能优雅结束。
        yield {"event": "failed", "reason": f"hermes SDK not available: {e}"}
        return

    # 无界队列：writer 在任意时刻 put_nowait 都不会阻塞 qr_login 协程。
    queue: asyncio.Queue = asyncio.Queue()
    writer = _QRLineWriter(queue)

    async def _run_login():
        # 在 qr_login 执行期间把 stdout 接管到 writer，使其 print 的二维码
        # URL 被识别并投递到队列。redirect_stdout 只包住这一个协程的执行，
        # 主循环（仅读队列、不写 stdout）不与之产生 stdout 竞争。
        with contextlib.redirect_stdout(writer):
            return await qr_login("/opt/data", bot_type="3", timeout_seconds=480)

    login_task = asyncio.create_task(_run_login())
    try:
        # 主循环：在「队列出现新 QR 行」与「登录任务完成」之间竞速。
        # 任一就绪即处理：QR 行 → yield qrcode；任务完成 → 退出循环走终态。
        while True:
            queue_get = asyncio.ensure_future(queue.get())
            done, _pending = await asyncio.wait(
                {queue_get, login_task},
                return_when=asyncio.FIRST_COMPLETED,
            )
            if queue_get in done:
                # 队列先就绪：拿到一条二维码 URL，立即 yield qrcode 事件。
                url = queue_get.result()
                yield {"event": "qrcode", "url": url}
            else:
                # 登录任务先完成：取消尚未就绪的 queue.get，跳出循环处理终态。
                queue_get.cancel()
                with contextlib.suppress(asyncio.CancelledError):
                    await queue_get
                break

        # 任务已完成，先把队列里残留的 QR 行（若在任务收尾瞬间入队）排空 yield，
        # 避免漏发已生成的二维码事件。
        while not queue.empty():
            yield {"event": "qrcode", "url": queue.get_nowait()}

        # 取登录结果；qr_login 内部异常在此 re-raise，转成 failed 事件。
        cred = login_task.result()
    except Exception as e:  # noqa: BLE001 - 任何登录异常都降级为 failed 事件，不向上抛
        yield {"event": "failed", "reason": str(e)}
        return

    if not cred:
        # 上游返回 None：失败或超时，统一标记为 timeout（与原 CLI 协议一致）。
        yield {"event": "timeout"}
        return
    # 凭证 truthy：登录成功，凭证已由 qr_login 落盘到 /opt/data/weixin/accounts/。
    yield {"event": "bound"}


async def feishu_register(domain: str = "feishu"):
    """飞书扫码自动创建的 async 事件流：先 yield qrcode，最后 yield credentials/failed。

    驱动 hermes 引擎 gateway.platforms.feishu 的设备码注册函数：
      _begin_registration(domain) -> {device_code, qr_url, interval, expire_in}
      _poll_registration(device_code, interval, expire_in, domain) -> {app_id, app_secret, domain, open_id} | None

    事件序列：
      - 引擎 SDK 不可用 → {"event":"failed","reason":...}
      - 正常 → {"event":"qrcode","url":...} 然后
               {"event":"credentials","app_id":...,"app_secret":...,"domain":...,
                "bot_name":...,"bot_open_id":...}
      - 扫码超时/拒绝 → {"event":"failed","reason":"registration timeout or denied"}

    刻意不抛异常：所有失败降级为 failed 事件，让上层 SSE 端点优雅收尾。
    凭证（含 app_secret）经此 SSE 在 oc-ops↔manager 内网鉴权通道回传，由 manager 落库即加密。
    """
    try:
        from gateway.platforms.feishu import (
            _begin_registration,
            _poll_registration,
            probe_bot,
        )
    except ImportError as e:
        yield {"event": "failed", "reason": f"hermes feishu SDK not available: {e}"}
        return

    loop = asyncio.get_event_loop()
    try:
        begin = await loop.run_in_executor(None, _begin_registration, domain)
    except Exception as e:  # noqa: BLE001 - 注册启动失败降级为 failed
        yield {"event": "failed", "reason": f"begin registration failed: {e}"}
        return

    # 先把二维码 URL 发给前端展示。
    yield {"event": "qrcode", "url": begin.get("qr_url", "")}

    # 阻塞轮询（在线程池里跑，避免堵事件循环），直到扫码成功/超时。
    def _poll():
        return _poll_registration(
            device_code=begin["device_code"],
            interval=begin.get("interval", 5),
            expire_in=begin.get("expire_in", 600),
            domain=domain,
        )

    try:
        result = await loop.run_in_executor(None, _poll)
    except Exception as e:  # noqa: BLE001
        yield {"event": "failed", "reason": f"poll registration failed: {e}"}
        return

    if not result or not result.get("app_id") or not result.get("app_secret"):
        yield {"event": "failed", "reason": "registration timeout or denied"}
        return

    # best-effort 探测 bot 名/open_id（失败不影响凭证回传）。
    bot_name, bot_open_id = None, None
    try:
        info = await loop.run_in_executor(
            None, probe_bot, result["app_id"], result["app_secret"], result.get("domain", domain)
        )
        if info:
            bot_name = info.get("bot_name")
            bot_open_id = info.get("bot_open_id")
    except Exception:  # noqa: BLE001 - 探测失败忽略
        pass

    yield {
        "event": "credentials",
        "app_id": result["app_id"],
        "app_secret": result["app_secret"],
        "domain": result.get("domain", domain),
        "bot_name": bot_name,
        "bot_open_id": bot_open_id,
    }
