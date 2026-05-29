# ocops/server.py
"""oc-ops HTTP 服务：把 ocops 各核心模块暴露为类型化 REST + SSE。

鉴权：除健康检查（/healthz）外，所有路由要求 Authorization: Bearer OC_OPS_TOKEN。
  Authorization 头缺失或 token 不匹配时返回 401 + {"code":"UNAUTHORIZED","message":"invalid token"}。
错误：业务 OpsError → code_to_http(code) + {code,message}；其它异常 → 500 INTERNAL。

当前实现包含基础端点（info/doctor/channel-status/unbind）与 cron 11 个端点；
kanban/login 端点在 Task 10/11 追加。"""
from __future__ import annotations

import os
from pathlib import Path

from starlette.applications import Starlette
from starlette.middleware import Middleware
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.responses import JSONResponse, Response
from starlette.routing import Route

from ocops import channel, cron, doctor, info
from ocops.auth import token_matches
from ocops.errors import OpsError, code_to_http

# ---------------------------------------------------------------------------
# cron body 字段白名单：防止未知键透传给 run_create / run_update，
# 键名与 ocops.cron.run_create / run_update 形参保持完全一致。
# ---------------------------------------------------------------------------

# create 允许的 body 字段（对应 run_create 形参，job_id 来自 path 不在此列）
_CRON_CREATE_KEYS = (
    "name", "schedule", "prompt", "deliver", "repeat", "script",
    "no_agent", "workdir", "skills", "model", "provider", "base_url",
)

# update 允许的 body 字段（对应 run_update 形参，job_id 来自 path 不在此列）
_CRON_UPDATE_KEYS = (
    "name", "schedule", "prompt", "deliver", "repeat", "script",
    "no_agent", "agent", "workdir", "skills", "clear_skills",
    "model", "provider", "base_url",
)


def _data_root() -> Path:
    """读取 OC_DATA_DIR 环境变量，返回运行时数据根目录路径；默认 /opt/data。"""
    return Path(os.environ.get("OC_DATA_DIR", "/opt/data"))


def _pick(body: dict, keys: tuple) -> dict:
    """从 JSON body 中只保留白名单键，多余键静默忽略；避免未知字段透传 run_* 函数。"""
    return {k: body[k] for k in keys if k in body}


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


# ---------------------------------------------------------------------------
# cron 端点（11 个）：按契约表实现，统一 try/except OpsError。
# ---------------------------------------------------------------------------

async def cron_capabilities(request):
    """GET /oc/cron/capabilities：返回 cron 能力自描述，不依赖 hermes CLI。"""
    try:
        return _ok(cron.run_capabilities())
    except OpsError as e:
        return _err(e)


async def cron_status(request):
    """GET /oc/cron/status：读 jobs.json 并调 hermes status，返回调度器状态摘要。"""
    try:
        return _ok(cron.run_status())
    except OpsError as e:
        return _err(e)


async def cron_list(request):
    """GET /oc/cron/jobs：列出 Cron 任务；query 参数 all=true 时包含禁用任务。

    all 参数只要存在且非空（true/1/yes 等）即视为过滤关闭；
    未提供或空字符串时默认只返回活跃任务。
    """
    all_param = request.query_params.get("all", "")
    # 非空且非 "false"/"0" 的字符串均视为 true
    all_ = bool(all_param and all_param.lower() not in ("false", "0", "no"))
    try:
        return _ok(cron.run_list(all_=all_))
    except OpsError as e:
        return _err(e)


async def cron_show(request):
    """GET /oc/cron/jobs/{id}：返回指定任务详情；不存在 → 404 NOT_FOUND。"""
    try:
        return _ok(cron.run_show(request.path_params["id"]))
    except OpsError as e:
        return _err(e)


async def cron_create(request):
    """POST /oc/cron/jobs：创建 Cron 任务；body 字段按 _CRON_CREATE_KEYS 白名单透传。

    body 多余键静默忽略；run_create 内部校验必填字段并调 hermes create。
    """
    body = await request.json()
    try:
        # _pick 保证只透传 run_create 认识的参数，防止意外关键字污染
        return _ok(cron.run_create(**_pick(body, _CRON_CREATE_KEYS)))
    except OpsError as e:
        return _err(e)


async def cron_update(request):
    """PATCH /oc/cron/jobs/{id}：编辑 Cron 任务；path 提供 job_id，body 提供更新字段。"""
    job_id = request.path_params["id"]
    body = await request.json()
    try:
        return _ok(cron.run_update(job_id, **_pick(body, _CRON_UPDATE_KEYS)))
    except OpsError as e:
        return _err(e)


async def cron_toggle(request):
    """POST /oc/cron/jobs/{id}/toggle：按 body.enabled 切换任务启停。

    enabled 为 bool：True→resume，False→pause（hermes 内部翻译）。
    """
    job_id = request.path_params["id"]
    body = await request.json()
    try:
        enabled = bool(body.get("enabled", True))
        return _ok(cron.run_toggle(job_id, enabled=enabled))
    except OpsError as e:
        return _err(e)


async def cron_run(request):
    """POST /oc/cron/jobs/{id}/run：立即触发任务执行，返回触发后的任务对象。"""
    try:
        return _ok(cron.run_run(request.path_params["id"]))
    except OpsError as e:
        return _err(e)


async def cron_delete(request):
    """DELETE /oc/cron/jobs/{id}：删除任务；成功返回 204 No Content（无 body）。"""
    try:
        cron.run_delete(request.path_params["id"])
        return Response(status_code=204)
    except OpsError as e:
        return _err(e)


async def cron_history(request):
    """GET /oc/cron/jobs/{id}/history：列出任务输出历史（markdown 文件列表）。"""
    try:
        return _ok(cron.run_history(request.path_params["id"]))
    except OpsError as e:
        return _err(e)


async def cron_output(request):
    """GET /oc/cron/jobs/{id}/output?file=<file_name>：读取指定输出文件内容。

    file 查询参数映射到 run_output 的 file_name 形参；缺省或含路径逃逸字符 → 400。
    """
    job_id = request.path_params["id"]
    # query 参数 file 对应 run_output 的 file_name 形参
    file_name = request.query_params.get("file", "")
    try:
        return _ok(cron.run_output(job_id, file_name))
    except OpsError as e:
        return _err(e)


# 路由表：按 REST 语义定义 HTTP 方法，无方法限制的路由接受所有方法。
# kanban / login 路由在 Task 10/11 追加。
routes = [
    Route("/healthz", healthz),
    Route("/oc/info", get_info),
    Route("/oc/doctor", get_doctor),
    Route("/oc/channels/{channel}/status", channel_status),
    Route("/oc/channels/{channel}/unbind", channel_unbind, methods=["POST"]),
    # cron 端点（Task 9）
    Route("/oc/cron/capabilities", cron_capabilities),
    Route("/oc/cron/status", cron_status),
    Route("/oc/cron/jobs", cron_list, methods=["GET"]),
    Route("/oc/cron/jobs", cron_create, methods=["POST"]),
    Route("/oc/cron/jobs/{id}", cron_show, methods=["GET"]),
    Route("/oc/cron/jobs/{id}", cron_update, methods=["PATCH"]),
    Route("/oc/cron/jobs/{id}/toggle", cron_toggle, methods=["POST"]),
    Route("/oc/cron/jobs/{id}/run", cron_run, methods=["POST"]),
    Route("/oc/cron/jobs/{id}", cron_delete, methods=["DELETE"]),
    Route("/oc/cron/jobs/{id}/history", cron_history),
    Route("/oc/cron/jobs/{id}/output", cron_output),
]

# Starlette app：路由 + AuthMiddleware 中间件栈。
app = Starlette(routes=routes, middleware=[Middleware(AuthMiddleware)])
