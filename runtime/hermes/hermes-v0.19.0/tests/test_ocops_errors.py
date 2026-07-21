"""覆盖 ocops 统一错误模型：错误码 → HTTP 状态码映射，及默认兜底。"""
from ocops.errors import OpsError, code_to_http


def test_code_to_http_known_codes():
    # 覆盖契约里全部已知错误码 → HTTP 状态码的精确映射
    assert code_to_http("BAD_REQUEST") == 400
    assert code_to_http("NOT_FOUND") == 404
    assert code_to_http("UNSUPPORTED") == 409
    assert code_to_http("INTERNAL") == 500
    assert code_to_http("HERMES_CLI_FAILED") == 502


def test_code_to_http_unknown_defaults_502():
    # 未知错误码兜底为 502（与 manager 端 default→ErrCronCLI 语义一致）
    assert code_to_http("SOMETHING_ELSE") == 502


def test_opserror_carries_code_and_message():
    # OpsError 须同时携带契约 code 与人读 message
    err = OpsError("NOT_FOUND", "任务不存在")
    assert err.code == "NOT_FOUND"
    assert str(err) == "任务不存在"
