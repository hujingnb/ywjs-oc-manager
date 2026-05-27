#!/usr/bin/env python3
"""oc-kb: manager runtime knowledge API client for Hermes containers."""

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


MAX_UPLOAD_BYTES = 100 * 1024 * 1024


class CLIError(RuntimeError):
    """带结构化错误码的 CLI 错误，便于 Hermes 读取失败原因。"""

    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


def main() -> int:
    parser = argparse.ArgumentParser(prog="oc-kb")
    sub = parser.add_subparsers(dest="command", required=True)

    search = sub.add_parser("search", help="search org and app knowledge bases")
    search.add_argument("question", nargs="+")
    search.add_argument("--top-k", type=int, default=8)

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
        _print_json({"ok": False, "error": {"code": exc.code, "message": exc.message}}, stream=sys.stderr)
        return 1
    except RuntimeError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    return 2


def _config() -> tuple[str, str]:
    base_url = os.environ.get("OC_KB_RUNTIME_BASE_URL", "").rstrip("/")
    token = os.environ.get("OC_KB_APP_TOKEN", "")
    if not base_url or not token:
        raise RuntimeError("oc-kb is not configured: missing OC_KB_RUNTIME_BASE_URL or OC_KB_APP_TOKEN")
    return base_url, token


def _search(question: str, top_k: int) -> int:
    payload = {"question": question, "top_k": top_k}
    result = _json_request("POST", "/api/v1/runtime/knowledge/search", payload)
    _print_json({"ok": True, "data": result})
    return 0


def _add(path: str, filename: str) -> int:
    local = _workspace_file(path)
    if local.stat().st_size > MAX_UPLOAD_BYTES:
        raise CLIError("FILE_TOO_LARGE", "单文件最多支持 100MB")
    result = _multipart_request("/api/v1/runtime/knowledge/files", local, filename or local.name)
    _print_json({"ok": True, "data": result})
    return 0


def _workspace_file(path: str) -> Path:
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
    base_url, token = _config()
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(base_url + api_path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    req.add_header("Accept", "application/json")
    req.add_header("X-OC-App-Token", token)
    return _open_json(req)


def _multipart_request(api_path: str, local: Path, filename: str) -> object:
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
    print(json.dumps(value, ensure_ascii=False, indent=2), file=stream)


if __name__ == "__main__":
    sys.exit(main())
