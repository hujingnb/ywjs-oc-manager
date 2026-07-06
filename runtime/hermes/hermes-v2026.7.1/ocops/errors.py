"""ocops 统一错误模型。

各核心模块（cron/kanban/channel）业务失败统一抛 OpsError 的子类或 OpsError 本身，
携带契约错误码；server 层据 code_to_http 映射成 HTTP 状态码 + {code,message} body。
CLI shim 则据 code 输出 {ok:false,error} 信封，保持对外命令契约不变。"""

from __future__ import annotations

# 契约错误码 → HTTP 状态码。未列出的码兜底 502（与 manager default→CLI 失败一致）。
_CODE_TO_HTTP = {
    "BAD_REQUEST": 400,
    "NOT_FOUND": 404,
    "UNSUPPORTED": 409,
    "INTERNAL": 500,
    "HERMES_CLI_FAILED": 502,
}


def code_to_http(code: str) -> int:
    """把契约错误码映射成 HTTP 状态码；未知码按 502（上游/CLI 失败）处理。"""
    return _CODE_TO_HTTP.get(code, 502)


class OpsError(Exception):
    """携带契约错误码的业务异常基类。"""

    def __init__(self, code: str, message: str):
        super().__init__(message)
        self.code = code
        self.message = message
