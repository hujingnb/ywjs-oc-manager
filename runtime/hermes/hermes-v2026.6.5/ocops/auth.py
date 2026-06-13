# ocops/auth.py
"""oc-ops 入站鉴权：校验 Authorization: Bearer <OC_OPS_TOKEN>，常量时间比较。"""
from __future__ import annotations

import hmac


def token_matches(authorization_header: str, expected: str) -> bool:
    """比较 Authorization 头里的 Bearer token 与期望值；未配置期望值时一律拒绝。

    使用 hmac.compare_digest 做常量时间比较，防止时序侧信道攻击。
    expected 为空字符串时直接拒绝，避免服务端未配置 token 时裸奔。"""
    if not expected:
        return False
    prefix = "Bearer "
    if not authorization_header.startswith(prefix):
        return False
    presented = authorization_header[len(prefix):]
    return hmac.compare_digest(presented, expected)
