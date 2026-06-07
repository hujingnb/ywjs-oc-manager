"""覆盖 channel_login async 事件流：mock qr_login，断言 qrcode→bound / timeout / failed。"""
import asyncio
import sys
import types

import pytest
from jsonschema import validate


def test_login_unknown_channel_yields_failed(ocops_schema):
    # 异常路径：未知 channel 直接 yield failed 事件并结束
    from ocops.channel import channel_login

    async def collect():
        return [e async for e in channel_login("telegram")]

    events = asyncio.run(collect())
    for event in events:
        validate(event, ocops_schema("channel/login-event.schema.json"))
    assert events == [{"event": "failed", "reason": "unknown channel: telegram"}]


def test_login_sdk_unavailable_yields_failed(monkeypatch):
    # 边界：venv 无 hermes SDK（import 失败）→ failed 事件，reason 含原因
    from ocops import channel

    async def collect():
        return [e async for e in channel.channel_login("weixin")]

    events = asyncio.run(collect())
    assert events[-1]["event"] == "failed"
    assert "SDK not available" in events[-1]["reason"]


def _install_fake_weixin(monkeypatch, qr_login):
    """往 sys.modules 注入假的 gateway.platforms.weixin，挂上给定 qr_login。

    channel_login 内 `from gateway.platforms.weixin import qr_login` 会命中此桩，
    从而走正常登录路径（无需真实 hermes SDK）。
    """
    # 构造完整包链 gateway -> gateway.platforms -> gateway.platforms.weixin，
    # 确保 from ... import 能解析到模块属性。
    gateway = types.ModuleType("gateway")
    platforms = types.ModuleType("gateway.platforms")
    weixin = types.ModuleType("gateway.platforms.weixin")
    weixin.qr_login = qr_login
    monkeypatch.setitem(sys.modules, "gateway", gateway)
    monkeypatch.setitem(sys.modules, "gateway.platforms", platforms)
    monkeypatch.setitem(sys.modules, "gateway.platforms.weixin", weixin)


def test_login_qrcode_then_bound(monkeypatch, ocops_schema):
    # 正常路径：qr_login 在中途 print 二维码 URL 后返回 cred(truthy)
    # → 事件序列应为先 qrcode（url 与 print 行一致）后 bound
    from ocops import channel

    async def fake_qr_login(data_root, bot_type, timeout_seconds):
        # 模拟上游：先把二维码 URL print 到 stdout（被 redirect 捕获），
        # 让出事件循环让主循环消费 qrcode 事件，再返回凭证 dict。
        print("https://liteapp.weixin.qq.com/q/abc?qrcode=tok&bot_type=3")
        await asyncio.sleep(0)
        return {"ok": 1}

    _install_fake_weixin(monkeypatch, fake_qr_login)

    async def collect():
        return [e async for e in channel.channel_login("weixin")]

    events = asyncio.run(collect())
    for event in events:
        validate(event, ocops_schema("channel/login-event.schema.json"))
    assert events == [
        {"event": "qrcode", "url": "https://liteapp.weixin.qq.com/q/abc?qrcode=tok&bot_type=3"},
        {"event": "bound"},
    ]


def test_login_qrcode_then_timeout(monkeypatch):
    # 边界：qr_login print 二维码后返回 None（超时/失败）
    # → 事件序列应为先 qrcode 后 timeout
    from ocops import channel

    async def fake_qr_login(data_root, bot_type, timeout_seconds):
        # 模拟上游：print 二维码后超时返回 None。
        print("https://liteapp.weixin.qq.com/q/timeout")
        await asyncio.sleep(0)
        return None

    _install_fake_weixin(monkeypatch, fake_qr_login)

    async def collect():
        return [e async for e in channel.channel_login("weixin")]

    events = asyncio.run(collect())
    assert events == [
        {"event": "qrcode", "url": "https://liteapp.weixin.qq.com/q/timeout"},
        {"event": "timeout"},
    ]


def test_login_qr_login_exception_yields_failed(monkeypatch):
    # 异常路径：qr_login 内部抛异常 → 不向上抛，降级为 failed 事件并附带原因
    from ocops import channel

    async def fake_qr_login(data_root, bot_type, timeout_seconds):
        # 模拟上游执行中崩溃，验证异常被捕获为 failed 事件而非冒泡。
        raise RuntimeError("boom")

    _install_fake_weixin(monkeypatch, fake_qr_login)

    async def collect():
        return [e async for e in channel.channel_login("weixin")]

    events = asyncio.run(collect())
    assert events[-1]["event"] == "failed"
    assert "boom" in events[-1]["reason"]
