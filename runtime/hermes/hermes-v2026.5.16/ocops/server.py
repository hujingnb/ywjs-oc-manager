# ocops/server.py
"""oc-ops HTTP 服务：把 ocops 各核心模块暴露为类型化 REST + SSE。

鉴权：除健康检查（/healthz）外，所有路由要求 Authorization: Bearer OC_OPS_TOKEN。
  Authorization 头缺失或 token 不匹配时返回 401 + {"code":"UNAUTHORIZED","message":"invalid token"}。
错误：业务 OpsError → code_to_http(code) + {code,message}；其它异常 → 500 INTERNAL。

当前实现包含 4 个基础端点（info/doctor/channel-status/unbind）；
cron/kanban/login 端点在 Task 9/10/11 追加。"""
from __future__ import annotations

import os
from pathlib import Path

from starlette.applications import Starlette
from starlette.middleware import Middleware
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.responses import JSONResponse, Response
from starlette.routing import Route

from ocops import channel, doctor, info
from ocops.auth import token_matches
from ocops.errors import OpsError, code_to_http


def _data_root() -> Path:
    """读取 OC_DATA_DIR 环境变量，返回运行时数据根目录路径；默认 /opt/data。"""
    return Path(os.environ.get("OC_DATA_DIR", "/opt/data"))


def _ok(data, status=200):
    """包装业务数据为成功 JSON 响应。"""
    return JSONResponse(data, status_code=status)


def _err(e: OpsError):
    """将 OpsError 映射为 HTTP 错误响应：code→HTTP 状态码 + {code,message} body。"""
    return JSONResponse({"code": e.code, "message": e.message}, status_code=code_to_http(e.code))


class AuthMiddleware(BaseHTTPMiddleware):
    """对非 /healthz 路径校验 Authorization: Bearer OC_OPS_TOKEN。

    OC_OPS_TOKEN 由环境变量注入（pod 启动时由 manager 生成并挂载为 Secret）。
    token 缺失或不匹配时立即返回 401，不继续调用下游 handler。"""

    async def dispatch(self, request, call_next):
        # /healthz 为健康探针白名单路径，不需要鉴权，让 Kubernetes liveness probe 通过。
        if request.url.path == "/healthz":
            return await call_next(request)
        # 从请求头读 Authorization（headers 大小写不敏感，starlette 统一转小写键）。
        if not token_matches(request.headers.get("authorization", ""), os.environ.get("OC_OPS_TOKEN", "")):
            return JSONResponse({"code": "UNAUTHORIZED", "message": "invalid token"}, status_code=401)
        return await call_next(request)


async def healthz(request):
    """健康探针端点，返回纯文本 ok，不需要鉴权。"""
    return Response("ok")


async def get_info(request):
    """GET /oc/info：返回镜像身份 JSON（从 OC_INFO_FILE 读取）。
    文件缺失或损坏时 OpsError(INTERNAL) → 500。"""
    try:
        return _ok(info.collect_info())
    except OpsError as e:
        return _err(e)


async def get_doctor(request):
    """GET /oc/doctor：返回运行时诊断快照（state + hermes 进程状态）。
    collect_doctor 永不抛业务错误，始终返回 200。"""
    return _ok(doctor.collect_doctor())


async def channel_status(request):
    """GET /oc/channels/{channel}/status：查询渠道绑定态。
    未知 channel 或其他业务错误 → 对应 HTTP 状态码 + {code,message}。"""
    try:
        return _ok(channel.channel_status(request.path_params["channel"], _data_root()))
    except OpsError as e:
        return _err(e)


async def channel_unbind(request):
    """POST /oc/channels/{channel}/unbind：解绑渠道（幂等）。
    未知 channel → 400，其他 OpsError → 对应状态码。"""
    try:
        return _ok(channel.channel_unbind(request.path_params["channel"], _data_root()))
    except OpsError as e:
        return _err(e)


# 路由表：按 REST 语义定义 HTTP 方法，无方法限制的路由接受所有方法。
# cron / kanban / login 路由在 Task 9/10/11 追加。
routes = [
    Route("/healthz", healthz),
    Route("/oc/info", get_info),
    Route("/oc/doctor", get_doctor),
    Route("/oc/channels/{channel}/status", channel_status),
    Route("/oc/channels/{channel}/unbind", channel_unbind, methods=["POST"]),
]

# Starlette app：路由 + AuthMiddleware 中间件栈。
app = Starlette(routes=routes, middleware=[Middleware(AuthMiddleware)])
