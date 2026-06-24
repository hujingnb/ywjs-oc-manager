# ocops/server.py
"""oc-ops HTTP 服务：把 ocops 各核心模块暴露为类型化 REST + SSE。

鉴权：除健康检查（/healthz）外，所有路由要求 Authorization: Bearer OC_OPS_TOKEN。
  Authorization 头缺失或 token 不匹配时返回 401 + {"code":"UNAUTHORIZED","message":"invalid token"}。
错误：业务 OpsError → code_to_http(code) + {code,message}；其它异常 → 500 INTERNAL。

当前实现包含基础端点（info/doctor/channel-status/unbind）与 cron 11 个端点；
kanban/login 端点在 Task 10/11 追加。"""
from __future__ import annotations

import json
import os
from pathlib import Path

from starlette.applications import Starlette
from starlette.middleware import Middleware
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.responses import JSONResponse, Response, StreamingResponse
from starlette.routing import Route

from ocops import channel, conversation, cron, doctor, info, kanban, skills
from ocops.auth import token_matches
from ocops.errors import OpsError, code_to_http
from ocops.kanban import KanbanError

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


async def get_config(request):
    """GET /oc/config：返回实例当前运行的 display.language，供 manager 实时查询。

    不依赖 DB 快照，直接读容器内 /opt/data/config.yaml（由 OC_DATA_DIR 指定根目录）
    的 display.language 字段。
    - 文件缺失或不含 display.language 键时，回落返回默认值 "en"，不报错。
    - 鉴权同其它 /oc/* 端点（Bearer OC_OPS_TOKEN），由 AuthMiddleware 统一校验。

    响应：{"display_language": "zh"|"en"（或任意 config 中配置的语言代码）}
    """
    import yaml  # PyYAML 在 hermes 镜像中已可用；延迟 import 与其它端点保持一致风格

    config_path = _data_root() / "config.yaml"
    language = "en"  # 默认回落值：文件缺失或无 display.language 键均返回 en
    try:
        text = config_path.read_text(encoding="utf-8")
        cfg = yaml.safe_load(text) or {}
        # 取 display.language，若 display 键缺失或不是 dict 则安全跳过，保持默认 en
        display = cfg.get("display") if isinstance(cfg, dict) else None
        if isinstance(display, dict):
            lang = display.get("language")
            if lang:
                language = str(lang)
    except FileNotFoundError:
        # config.yaml 不存在属正常边界（实例刚创建未写入），静默回落 en
        pass

    return _ok({"display_language": language})


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


# ---------------------------------------------------------------------------
# kanban 守卫辅助：stub 镜像抛 UNSUPPORTED（409）。
# ---------------------------------------------------------------------------

def _require_real_hermes() -> None:
    """检查是否存在真实 hermes CLI；stub 镜像（has_real_hermes()→False）时抛 KanbanError。

    每个需要守卫的 kanban handler 在执行业务逻辑前先调用此函数。
    KanbanError 是 OpsError 子类，会被 handler 的 except OpsError 统一捕获，
    code_to_http("UNSUPPORTED") → 409。
    """
    if not kanban.has_real_hermes():
        raise KanbanError("UNSUPPORTED", "此镜像不含真实 hermes，kanban 操作不可用")


# ---------------------------------------------------------------------------
# kanban 端点（14 个）：按契约表实现，统一 try/except OpsError。
# ---------------------------------------------------------------------------

async def kanban_capabilities(request):
    """GET /oc/kanban/capabilities：返回 kanban 能力自描述；不做 hermes 守卫，任何环境可用。"""
    try:
        return _ok(kanban.run_capabilities())
    except OpsError as e:
        return _err(e)


async def kanban_boards(request):
    """GET /oc/kanban/boards：列出所有 board；stub 镜像→ 409 UNSUPPORTED。"""
    try:
        _require_real_hermes()
        return _ok(kanban.run_boards())
    except OpsError as e:
        return _err(e)


async def kanban_list(request):
    """GET /oc/kanban/tasks：列出任务；query 支持 board（默认 default）/status/assignee 过滤。"""
    board = request.query_params.get("board", "default")
    status = request.query_params.get("status") or None
    assignee = request.query_params.get("assignee") or None
    try:
        _require_real_hermes()
        return _ok(kanban.run_list(board, status=status, assignee=assignee))
    except OpsError as e:
        return _err(e)


async def kanban_show(request):
    """GET /oc/kanban/tasks/{id}：返回单个任务详情；board 来自 query（默认 default）。"""
    board = request.query_params.get("board", "default")
    task_id = request.path_params["id"]
    try:
        _require_real_hermes()
        return _ok(kanban.run_show(board, task_id))
    except OpsError as e:
        return _err(e)


async def kanban_runs(request):
    """GET /oc/kanban/tasks/{id}/runs：返回任务历次执行记录；board 来自 query（默认 default）。"""
    board = request.query_params.get("board", "default")
    task_id = request.path_params["id"]
    try:
        _require_real_hermes()
        return _ok(kanban.run_runs(board, task_id))
    except OpsError as e:
        return _err(e)


async def kanban_stats(request):
    """GET /oc/kanban/stats：返回 board 统计；board 来自 query（默认 default）。"""
    board = request.query_params.get("board", "default")
    try:
        _require_real_hermes()
        return _ok(kanban.run_stats(board))
    except OpsError as e:
        return _err(e)


async def kanban_create(request):
    """POST /oc/kanban/tasks：创建任务；body 字段直接透传给 run_create（board/title/assignee 必填）。

    写字段（board/title/assignee/priority/body/skills/workspace/parent/max_retries）来自 JSON body；
    多余字段静默忽略，必填项缺失由 run_create 内部校验。
    """
    body = await request.json()
    try:
        _require_real_hermes()
        # 从 body 提取 run_create 认识的参数，防止未知键污染
        kwargs = {k: body[k] for k in (
            "board", "title", "assignee", "priority",
            "body", "skills", "workspace", "parent", "max_retries",
        ) if k in body}
        return _ok(kanban.run_create(**kwargs))
    except OpsError as e:
        return _err(e)


async def kanban_comment(request):
    """POST /oc/kanban/tasks/{id}/comment：给任务添加评论；body 字段 board/body 来自 JSON body。"""
    task_id = request.path_params["id"]
    body = await request.json()
    board = body.get("board", "default")
    body_text = body.get("body", "")
    try:
        _require_real_hermes()
        return _ok(kanban.run_comment(board, task_id, body_text))
    except OpsError as e:
        return _err(e)


async def kanban_complete(request):
    """POST /oc/kanban/tasks/{id}/complete：标记任务完成；board/result 来自 JSON body（result 可选）。"""
    task_id = request.path_params["id"]
    body = await request.json()
    board = body.get("board", "default")
    result = body.get("result") or None
    try:
        _require_real_hermes()
        return _ok(kanban.run_complete(board, task_id, result=result))
    except OpsError as e:
        return _err(e)


async def kanban_block(request):
    """POST /oc/kanban/tasks/{id}/block：阻塞任务；board/reason 来自 JSON body。"""
    task_id = request.path_params["id"]
    body = await request.json()
    board = body.get("board", "default")
    reason = body.get("reason", "")
    try:
        _require_real_hermes()
        return _ok(kanban.run_block(board, task_id, reason))
    except OpsError as e:
        return _err(e)


async def kanban_unblock(request):
    """POST /oc/kanban/tasks/{id}/unblock：解除阻塞；board 来自 JSON body（默认 default）。"""
    task_id = request.path_params["id"]
    body = await request.json()
    board = body.get("board", "default")
    try:
        _require_real_hermes()
        return _ok(kanban.run_unblock(board, task_id))
    except OpsError as e:
        return _err(e)


async def kanban_archive(request):
    """POST /oc/kanban/tasks/{id}/archive：归档任务；board 来自 JSON body（默认 default）。"""
    task_id = request.path_params["id"]
    body = await request.json()
    board = body.get("board", "default")
    try:
        _require_real_hermes()
        return _ok(kanban.run_archive(board, task_id))
    except OpsError as e:
        return _err(e)


async def kanban_reassign(request):
    """POST /oc/kanban/tasks/{id}/reassign：重新分配任务；board/to 来自 JSON body。"""
    task_id = request.path_params["id"]
    body = await request.json()
    board = body.get("board", "default")
    to = body.get("to", "")
    try:
        _require_real_hermes()
        return _ok(kanban.run_reassign(board, task_id, to))
    except OpsError as e:
        return _err(e)


async def kanban_reclaim(request):
    """POST /oc/kanban/tasks/{id}/reclaim：撤销任务认领；board 来自 JSON body（默认 default）。"""
    task_id = request.path_params["id"]
    body = await request.json()
    board = body.get("board", "default")
    try:
        _require_real_hermes()
        return _ok(kanban.run_reclaim(board, task_id))
    except OpsError as e:
        return _err(e)


# ---------------------------------------------------------------------------
# skills 端点（4 个）：list/install/delete/reload。
# install 使用 multipart form-data（name 字段 + archive 文件字段）。
# ---------------------------------------------------------------------------

async def skills_list(request):
    """GET /oc/skills：列出 SKILLS_DIR 下所有 skill，标注 managed/builtin。"""
    try:
        return _ok(skills.list_skills())
    except OpsError as e:
        return _err(e)


async def skills_install(request):
    """POST /oc/skills：接收 multipart form-data（name+archive），解压安装到 SKILLS_DIR/<name>/。

    name 字段：skill 名（非空、不含路径分隔符）。
    archive 字段：tar 或 zip 归档文件（格式由 install_skill/_safe_extract 按内容判定）。
    缺少 name 或 archive 时立即返回 400 BAD_REQUEST。
    """
    form = await request.form()
    name = form.get("name")
    upload = form.get("archive")
    # 必填字段缺失时提前拒绝，不继续处理上传数据
    if not name or upload is None:
        return _err(OpsError("BAD_REQUEST", "缺少 name 或 archive"))
    import os
    import tempfile
    # 归档格式由 _safe_extract 按文件内容（zip 魔数）判定，与临时文件后缀无关，
    # 故临时文件用中性后缀即可（上传方 multipart filename 不带扩展名，不可作为格式依据）。
    fd, tmp = tempfile.mkstemp(suffix=".archive")
    try:
        with os.fdopen(fd, "wb") as f:
            f.write(await upload.read())
        return _ok(skills.install_skill(name, tmp))
    except OpsError as e:
        return _err(e)
    finally:
        # 临时文件无论成功失败都删除，避免磁盘泄漏
        os.unlink(tmp)


async def skills_delete(request):
    """DELETE /oc/skills/{name}：热删 SKILLS_DIR/<name>/（幂等，目录不存在时正常返回）。"""
    try:
        return _ok(skills.delete_skill(request.path_params["name"]))
    except OpsError as e:
        return _err(e)


async def skills_reload(request):
    """POST /oc/skills/reload：触发 hermes api_server 重扫 skills/ 目录（不重启进程）。

    调用 hermes 127.0.0.1:8642/oc/skills/reload，透传其响应体（added/removed/total 等字段）。
    """
    try:
        return _ok(skills.reload_skills())
    except OpsError as e:
        return _err(e)


# ---------------------------------------------------------------------------
# SSE 端点（Task 11）：手写 `data: <json>\n\n` 帧，media_type=text/event-stream，
# 不引入 sse-starlette 等额外依赖。
# ---------------------------------------------------------------------------

# 线程池迭代同步 generator 的哨兵：anyio.to_thread.run_sync(next, it, _SENTINEL)
# 在迭代耗尽时返回此对象（而非抛 StopIteration 穿透线程边界），据此判断结束。
_SENTINEL = object()


async def kanban_watch(request):
    """GET /oc/kanban/watch?board=：把 watch_events 同步 generator 转 SSE data 帧。

    watch_events 内部用 subprocess 阻塞读 hermes watch 输出，是同步 generator；
    若直接在 asyncio 协程里迭代会堵住事件循环，故用 anyio.to_thread.run_sync
    把「取迭代器」和「取下一条」都放线程池执行，逐条 yield 成 SSE data 帧。

    OpsError（含启动失败的 KanbanError）→ 发 `event: error` 帧携带 {code,message}，
    让前端能读到结构化错误体而非看到连接被静默切断。
    """
    board = request.query_params.get("board", "default")

    async def gen():
        # 延迟 import anyio：仅 SSE 路径需要，避免无谓拉高模块导入成本。
        import anyio

        try:
            # watch_events 是同步 generator（subprocess 阻塞读）；先在线程池取迭代器，
            # 再逐条 next。run_sync(next, it, _SENTINEL) 把 StopIteration 转成哨兵返回，
            # 不让 StopIteration 穿透线程边界。
            it = await anyio.to_thread.run_sync(lambda: iter(kanban.watch_events(board)))
            while True:
                ev = await anyio.to_thread.run_sync(next, it, _SENTINEL)
                if ev is _SENTINEL:
                    break
                yield f"data: {json.dumps(ev, ensure_ascii=False)}\n\n"
        except OpsError as e:
            # 业务错误（含 KanbanError 启动失败）转 SSE error 帧，不向上抛中断流。
            yield f"event: error\ndata: {json.dumps({'code': e.code, 'message': e.message}, ensure_ascii=False)}\n\n"

    return StreamingResponse(gen(), media_type="text/event-stream")


async def channel_login(request):
    """POST /oc/channels/{channel}/login：把 channel_login async generator 转 SSE。

    channel_login 已是 async generator（内部并发处理 qr_login 与二维码事件、
    所有失败降级为 failed 事件不抛异常），直接 async for 逐条转 SSE data 帧。
    """
    ch = request.path_params["channel"]

    async def gen():
        # channel_login 自身保证优雅结束（终态为 bound/timeout/failed），逐条转帧即可。
        async for ev in channel.channel_login(ch):
            yield f"data: {json.dumps(ev, ensure_ascii=False)}\n\n"

    return StreamingResponse(gen(), media_type="text/event-stream")


# ---------------------------------------------------------------------------
# 会话（conversation）端点：转发到同 pod hermes api_server /api/sessions/*。
# manager 仅做带 per-app token 的透传，会话数据不在 oc-ops 落地。
# ---------------------------------------------------------------------------

async def conversation_list(request):
    """GET /oc/conversations?source=&limit=&offset= —— 列实例下会话。"""
    try:
        q = request.query_params
        data = conversation.list_sessions(
            source=q.get("source", ""),
            limit=int(q.get("limit", "50") or "50"),
            offset=int(q.get("offset", "0") or "0"),
        )
        return _ok(data)
    except OpsError as e:
        return _err(e)


async def conversation_messages(request):
    """GET /oc/conversations/{sid}/messages —— 读会话历史。"""
    try:
        return _ok(conversation.session_messages(request.path_params["sid"]))
    except OpsError as e:
        return _err(e)


async def conversation_create(request):
    """POST /oc/conversations —— 新建会话，body 透传（source/title）。"""
    try:
        body = await request.json()
    except Exception:
        body = {}
    try:
        return _ok(conversation.create_session(body), status=201)
    except OpsError as e:
        return _err(e)


async def conversation_delete(request):
    """DELETE /oc/conversations/{sid} —— 删除会话。"""
    try:
        conversation.delete_session(request.path_params["sid"])
        return Response(status_code=204)
    except OpsError as e:
        return _err(e)


async def conversation_rename(request):
    """PATCH /oc/conversations/{sid} —— 重命名会话，body {"title"}。"""
    try:
        body = await request.json()
    except Exception:
        body = {}
    try:
        return _ok(conversation.update_title(request.path_params["sid"], str(body.get("title", ""))))
    except OpsError as e:
        return _err(e)


async def conversation_chat(request):
    """POST /oc/conversations/{sid}/chat —— 单轮续聊，body 含 message。"""
    try:
        body = await request.json()
    except Exception:
        body = {}
    try:
        return _ok(conversation.chat(request.path_params["sid"], body))
    except OpsError as e:
        return _err(e)


async def conversation_chat_stream(request):
    """POST /oc/conversations/{sid}/chat/stream —— 流式续聊，转发 api_server SSE。"""
    try:
        body = await request.json()
    except Exception:
        body = {}
    sid = request.path_params["sid"]

    def gen():
        # 错误统一规整为「正常 data 帧」{event:error,payload:{code,message}}，而非
        # SSE 命名事件 `event: error`：下游 Go scanSSE 对 `event: error` 帧会直接终止、
        # 不投递 data，导致前端只看到「空的成功回复」而非错误（见 code review #1）。
        # 用 data 帧承载 error 事件，可经 scanSSE → Go client → handler → 前端
        # evt.event==='error' 全链路透出。OpsError 与上游中途抛出的其它异常都覆盖。
        try:
            yield from conversation.chat_stream(sid, body)
        except OpsError as e:
            frame = {"event": "error", "payload": {"code": e.code, "message": e.message}}
            yield ("data: " + json.dumps(frame) + "\n\n").encode()
        except Exception as e:  # 上游中途 socket/解析等非 OpsError 异常，同样规整为 error 帧
            frame = {"event": "error", "payload": {"code": "INTERNAL", "message": str(e)}}
            yield ("data: " + json.dumps(frame) + "\n\n").encode()

    return StreamingResponse(gen(), media_type="text/event-stream")


# 路由表：按 REST 语义定义 HTTP 方法，无方法限制的路由接受所有方法。
# kanban / login 路由在 Task 10/11 追加。
routes = [
    Route("/healthz", healthz),
    Route("/oc/info", get_info),
    Route("/oc/doctor", get_doctor),
    Route("/oc/config", get_config),
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
    # kanban 端点（Task 10）：非流式 REST；watch SSE 在 Task 11 追加。
    Route("/oc/kanban/capabilities", kanban_capabilities),
    Route("/oc/kanban/boards", kanban_boards),
    Route("/oc/kanban/tasks", kanban_list, methods=["GET"]),
    Route("/oc/kanban/tasks", kanban_create, methods=["POST"]),
    Route("/oc/kanban/tasks/{id}", kanban_show, methods=["GET"]),
    Route("/oc/kanban/tasks/{id}/runs", kanban_runs),
    Route("/oc/kanban/stats", kanban_stats),
    Route("/oc/kanban/tasks/{id}/comment", kanban_comment, methods=["POST"]),
    Route("/oc/kanban/tasks/{id}/complete", kanban_complete, methods=["POST"]),
    Route("/oc/kanban/tasks/{id}/block", kanban_block, methods=["POST"]),
    Route("/oc/kanban/tasks/{id}/unblock", kanban_unblock, methods=["POST"]),
    Route("/oc/kanban/tasks/{id}/archive", kanban_archive, methods=["POST"]),
    Route("/oc/kanban/tasks/{id}/reassign", kanban_reassign, methods=["POST"]),
    Route("/oc/kanban/tasks/{id}/reclaim", kanban_reclaim, methods=["POST"]),
    # SSE 端点（Task 11）：kanban 事件流（GET）与 channel 扫码登录流（POST）。
    Route("/oc/kanban/watch", kanban_watch),
    Route("/oc/channels/{channel}/login", channel_login, methods=["POST"]),
    # skills 端点：list/install/delete/reload。
    # reload（POST /oc/skills/reload）放在带路径参数的 delete（DELETE /oc/skills/{name}）之前，
    # 虽然方法不同不会产生歧义，但排列顺序更易读。
    Route("/oc/skills",        skills_list,    methods=["GET"]),
    Route("/oc/skills",        skills_install, methods=["POST"]),
    Route("/oc/skills/reload", skills_reload,  methods=["POST"]),
    Route("/oc/skills/{name}", skills_delete,  methods=["DELETE"]),
    # 会话端点（Task 12）：非流式 REST；/chat/stream SSE 在后续任务追加。
    Route("/oc/conversations", conversation_list, methods=["GET"]),
    Route("/oc/conversations", conversation_create, methods=["POST"]),
    Route("/oc/conversations/{sid}/messages", conversation_messages, methods=["GET"]),
    Route("/oc/conversations/{sid}/chat", conversation_chat, methods=["POST"]),
    Route("/oc/conversations/{sid}/chat/stream", conversation_chat_stream, methods=["POST"]),
    Route("/oc/conversations/{sid}", conversation_delete, methods=["DELETE"]),
    Route("/oc/conversations/{sid}", conversation_rename, methods=["PATCH"]),
]

# Starlette app：路由 + AuthMiddleware 中间件栈。
app = Starlette(routes=routes, middleware=[Middleware(AuthMiddleware)])
