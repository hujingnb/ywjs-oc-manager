# tests/test_ocops_auth.py
"""覆盖 Bearer token 校验：正确放行、缺失/错误拒绝、未配置 token 时拒绝一切。"""
from ocops.auth import token_matches


def test_token_matches_correct():
    # 正常：Authorization 头与期望 token 一致 → 放行
    assert token_matches("Bearer s3cret", "s3cret") is True


def test_token_matches_wrong_or_missing():
    # 异常：错误 token / 缺 Bearer 前缀 / 空头 → 拒绝
    assert token_matches("Bearer nope", "s3cret") is False
    assert token_matches("s3cret", "s3cret") is False
    assert token_matches("", "s3cret") is False


def test_token_matches_unset_expected_denies():
    # 边界：服务端未配置 token（空）→ 拒绝一切，避免裸奔
    assert token_matches("Bearer anything", "") is False
