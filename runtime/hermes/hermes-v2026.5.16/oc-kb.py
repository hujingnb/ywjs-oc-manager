#!/usr/bin/env python3
"""oc-kb：Hermes 容器内调 manager runtime 知识库 API 的 CLI 客户端。

命名约定：所有 oc-* 脚本的 oc- 前缀取自项目名 oc-manager，统一表示
「由 oc-manager 注入 hermes runtime 镜像、供容器内调用的运维 CLI」，
以此与 hermes 上游自带命令区分；后缀 kb = knowledge base（知识库）。

设计取舍：
- Hermes 容器只通过 manager runtime API 访问知识库，不直接连接 RAGFlow，
  也不持有 RAGFlow 凭证；权限边界由 manager 通过 app runtime token 收敛。
- 入口脚本只依赖 Python 标准库（urllib + argparse），避免 Hermes runtime 镜像
  为单一 CLI 多装第三方依赖；同时也防止 sys.path 污染 hermes-agent 自身。
- 所有错误统一走 stderr + 退出码 != 0，方便 hermes-agent 把失败原因带回对话。
"""

from __future__ import annotations

import argparse
import json
import mimetypes
import os
from pathlib import Path
import sys
import urllib.error
import urllib.request
import uuid


# manager runtime API 单文件上传上限；与 manager 端的 maxKnowledgeUploadBytes
# 必须保持一致，避免 CLI 先放行后被服务端 413 截断造成可读性差的错误。
MAX_UPLOAD_BYTES = 100 * 1024 * 1024


class CLIError(RuntimeError):
    """带结构化错误码的 CLI 错误，便于 Hermes 读取失败原因。

    与普通 RuntimeError 的区别在于：
    - 失败结果会以 {"ok": false, "error": {...}} JSON 形式写到 stderr，
      让 hermes-agent 解析后再决定如何在对话里复述失败原因；
    - code 字段固定（INVALID_PATH / FILE_TOO_LARGE 等），便于上层做条件分支。
    """

    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


def main() -> int:
    """oc-kb CLI 入口。

    支持两个子命令：
    - search：检索当前 app 与所属 org 知识库；
    - add：把 workspace 内文件加入当前 app 知识库。

    退出码语义：
    - 0：子命令业务成功（stdout 是 JSON）；
    - 1：CLIError 或 RuntimeError（stderr 给原因）；
    - 2：argparse 未匹配到子命令（理论上 required=True 会先抛 SystemExit，
      保留这条兜底是为了让"未来扩展子命令但忘记 dispatch"的状况显式失败）。
    """
    parser = argparse.ArgumentParser(prog="oc-kb")
    sub = parser.add_subparsers(dest="command", required=True)

    # search 把多 token 的提问 join 成一句话，避免使用方需要额外引号转义。
    search = sub.add_parser("search", help="search org and app knowledge bases")
    search.add_argument("question", nargs="+")
    search.add_argument("--top-k", type=int, default=8)

    # add 强制只接受 workspace 相对路径；filename 可选，缺省用本地文件名。
    add = sub.add_parser("add", help="add a local file to the current app knowledge base")
    add.add_argument("path")
    add.add_argument("--filename", default="")

    args = parser.parse_args()
    try:
        if args.command == "search":
            return _search(" ".join(args.question), args.top_k)
        if args.command == "add":
            return _add(args.path, args.filename)
    except CLIError as exc:
        # 结构化错误：写 stderr 让 hermes-agent 把错误码 / 文案带回对话上下文。
        _print_json({"ok": False, "error": {"code": exc.code, "message": exc.message}}, stream=sys.stderr)
        return 1
    except RuntimeError as exc:
        # 非结构化错误（网络 / 服务端 5xx 等）：纯文本写 stderr，保留原始信息。
        print(str(exc), file=sys.stderr)
        return 1
    return 2


def _config() -> tuple[str, str]:
    """读取 manager runtime API 的连接配置。

    runtime_base_url 与 app_token 由 oc-entrypoint 从 manifest.knowledge 解析后
    注入进程环境（见 oc-entrypoint.py 的 _configure_knowledge_env）。
    任一缺失视为容器尚未拿到 runtime token，CLI 立即失败而不是默默拼出错误请求。
    """
    base_url = os.environ.get("OC_KB_RUNTIME_BASE_URL", "").rstrip("/")
    token = os.environ.get("OC_KB_APP_TOKEN", "")
    if not base_url or not token:
        raise RuntimeError("oc-kb is not configured: missing OC_KB_RUNTIME_BASE_URL or OC_KB_APP_TOKEN")
    return base_url, token


def _search(question: str, top_k: int) -> int:
    """跨当前 app 与所属 org 知识库做一次检索。

    入参只透传 question / top_k，dataset_id / org_id 等目标参数由 manager 端
    通过 app runtime token 自行解析，不接受外部覆盖（防 prompt 注入跨 scope）。
    """
    payload = {"question": question, "top_k": top_k}
    result = _json_request("POST", "/api/v1/runtime/knowledge/search", payload)
    _print_json({"ok": True, "data": result})
    return 0


def _add(path: str, filename: str) -> int:
    """把 workspace 内的一个文件加入当前 app 知识库。

    本地预检覆盖两类问题，避免把大文件 / 越界文件白送上 manager 再被打回：
    - 路径越界 / 不是文件 → 由 _workspace_file 抛 CLIError；
    - 单文件超 100MB → 这里直接拒绝（与 manager 端上限保持一致）。
    """
    local = _workspace_file(path)
    if local.stat().st_size > MAX_UPLOAD_BYTES:
        raise CLIError("FILE_TOO_LARGE", "单文件最大支持 100MB")
    result = _multipart_request("/api/v1/runtime/knowledge/files", local, filename or local.name)
    _print_json({"ok": True, "data": result})
    return 0


def _workspace_file(path: str) -> Path:
    """把用户传入的相对路径解析为 workspace 内的真实文件路径。

    安全约束（按拒绝优先级排序）：
    1. 绝对路径：直接拒，避免攻击者用 /etc/passwd 之类的系统路径绕过 workspace；
    2. 路径穿越（..）：通过 Path.resolve() 再 relative_to(workspace) 双重校验，
       resolve() 把符号链接和 .. 全部展开后再判隶属，能挡住链接逃逸；
    3. 是目录或其它非常规文件：拒，防止 hermes-agent 误把整个目录当 file 上传；
    4. 文件不存在：报"file not found"（非 CLIError，因为这通常是 Hermes 在错处理上下文）。
    """
    requested = Path(path)
    if requested.is_absolute():
        raise CLIError("INVALID_PATH", "文件必须位于 workspace 内")
    workspace = Path(os.environ.get("OC_WORKSPACE_DIR", "/opt/data/workspace")).resolve()
    candidate = (workspace / requested).resolve()
    try:
        candidate.relative_to(workspace)
    except ValueError as exc:
        raise CLIError("INVALID_PATH", "文件必须位于 workspace 内") from exc
    if candidate.exists() and not candidate.is_file():
        raise CLIError("INVALID_PATH", "文件必须位于 workspace 内")
    if not candidate.is_file():
        raise RuntimeError(f"file not found: {path}")
    return candidate


def _json_request(method: str, api_path: str, payload: dict) -> object:
    """以 JSON 形式调 manager runtime API；统一加 app token 鉴权头。

    使用 X-OC-App-Token 而非 Authorization Bearer，是为了让 manager 端
    runtime API 与用户侧 Bearer JWT 流量在 router 层就能区分，
    避免 token 类型混淆。
    """
    base_url, token = _config()
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(base_url + api_path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    req.add_header("Accept", "application/json")
    req.add_header("X-OC-App-Token", token)
    return _open_json(req)


def _multipart_request(api_path: str, local: Path, filename: str) -> object:
    """以 multipart/form-data 上传单文件到 manager runtime API。

    手写 multipart 而不是用 requests.MultipartEncoder：
    - 避免引入第三方依赖；
    - 字段名固定为 file，与 manager 端 c.FormFile("file") 严格对齐；
    - boundary 用 uuid4 hex，与正文不可能冲突。

    Content-Type 由本地后缀猜测；猜不到时退回 octet-stream，
    服务端会按 mime_type 字段记录到 ragflow_documents 表。
    """
    base_url, token = _config()
    boundary = "----oc-kb-" + uuid.uuid4().hex
    ctype = mimetypes.guess_type(filename)[0] or "application/octet-stream"
    file_bytes = local.read_bytes()
    body = b"".join([
        f"--{boundary}\r\n".encode(),
        f'Content-Disposition: form-data; name="file"; filename="{filename}"\r\n'.encode(),
        f"Content-Type: {ctype}\r\n\r\n".encode(),
        file_bytes,
        b"\r\n",
        f"--{boundary}--\r\n".encode(),
    ])
    req = urllib.request.Request(base_url + api_path, data=body, method="POST")
    req.add_header("Content-Type", f"multipart/form-data; boundary={boundary}")
    req.add_header("Accept", "application/json")
    req.add_header("X-OC-App-Token", token)
    return _open_json(req)


def _open_json(req: urllib.request.Request) -> object:
    """统一执行 HTTP 请求并把响应解析为 JSON。

    错误处理：
    - HTTPError（4xx / 5xx）：把状态码和响应体拼成 RuntimeError，
      让上层 stderr 输出包含具体的 manager 业务错误码（如 INVALID_APP_TOKEN）；
    - URLError（网络断连 / DNS）：把异常文案直传，便于排查 manager 是否在线；
    - 空响应：返回空 dict，避免下游 .get 出 NoneType 错误；
    - 非 JSON 响应：包成 {"raw": ...}，保留原始内容方便日志排查。

    timeout=60s 是为了覆盖 RAGFlow retrieval 单次请求的最长正常耗时；
    超过此值视为下游异常，让 hermes-agent 重新发起请求而不是长挂。
    """
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:  # noqa: S310 - manager URL is injected by oc-manager.
            raw = resp.read()
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"manager runtime API returned HTTP {exc.code}: {body}") from exc
    except urllib.error.URLError as exc:
        raise RuntimeError(f"manager runtime API request failed: {exc}") from exc
    if not raw:
        return {}
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return {"raw": raw.decode("utf-8", errors="replace")}


def _print_json(value: object, stream=sys.stdout) -> None:
    """把结构化对象写到指定流；ensure_ascii=False 让中文错误文案保留可读形式。"""
    print(json.dumps(value, ensure_ascii=False, indent=2), file=stream)


if __name__ == "__main__":
    sys.exit(main())
