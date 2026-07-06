#!/usr/bin/env python3
"""oc-publish：把本地静态目录发布为带域名的公网站点。

用法：oc-publish ./<dir>

把目录打成 tar.gz，经 manager runtime 发布端点（X-OC-App-Token 鉴权）上传，
manager 解包上传对象存储、分配随机 <slug>.<base_domain>、返回 {url, expires_at}。
每次发布都创建一个全新的随机站点（不支持指定名字或原地更新）；要改内容就再发布
一次，会得到一个新的随机地址。配置（OC_PUBLISH_RUNTIME_BASE_URL /
OC_PUBLISH_APP_TOKEN）由 oc-entrypoint 从 manifest.web_publish 注入进程环境。
"""

from __future__ import annotations

import io
import json
import os
import sys
import tarfile
import urllib.error
import urllib.request
import uuid


# 单次发布 tar.gz 大小上限（与 manager 服务端 MaxUploadSize 同量级）。
MAX_UPLOAD_BYTES = 200 * 1024 * 1024


def _config() -> tuple[str, str]:
    """读取 manager runtime API 连接配置；任一缺失立即失败而不是默默拼错请求。

    runtime_base_url 与 app_token 由 oc-entrypoint 从 manifest.web_publish 解析后
    注入进程环境（见 _configure_web_publish_env）。
    """
    base_url = os.environ.get("OC_PUBLISH_RUNTIME_BASE_URL", "").rstrip("/")
    token = os.environ.get("OC_PUBLISH_APP_TOKEN", "")
    if not base_url or not token:
        raise RuntimeError("oc-publish 未配置：缺 OC_PUBLISH_RUNTIME_BASE_URL 或 OC_PUBLISH_APP_TOKEN")
    return base_url, token


def _make_targz(src_dir: str) -> bytes:
    """把 src_dir 下内容打成 tar.gz（仅常规文件，路径相对 src_dir）。

    os.walk 递归遍历子目录，arcname 去掉 src_dir 前缀，确保解压后根目录即文件本身
    而不是多余的父路径层级；目录条目本身不入包，只写文件，避免权限元数据干扰。
    """
    if not os.path.isdir(src_dir):
        raise RuntimeError(f"目录不存在：{src_dir}")
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for root, _dirs, files in os.walk(src_dir):
            for name in files:
                full = os.path.join(root, name)
                rel = os.path.relpath(full, src_dir)
                tar.add(full, arcname=rel)
    data = buf.getvalue()
    if len(data) > MAX_UPLOAD_BYTES:
        raise RuntimeError(f"发布内容超过上限 {MAX_UPLOAD_BYTES} 字节")
    return data


def _publish(src_dir: str) -> dict:
    """上传 tar.gz 到 manager 发布端点并返回解析后的 JSON。

    手写 multipart 而不是用 requests：避免引入第三方依赖，与 oc-kb 保持一致；
    boundary 用 uuid4 hex，与正文不可能冲突。manager 端每次都分配随机站点名，
    无需也不接受调用方指定 slug。
    """
    base_url, token = _config()
    payload = _make_targz(src_dir)
    boundary = "----oc-publish-" + uuid.uuid4().hex
    parts: list[bytes] = [
        f"--{boundary}\r\n".encode(),
        b'Content-Disposition: form-data; name="file"; filename="site.tar.gz"\r\n',
        b"Content-Type: application/gzip\r\n\r\n",
        payload,
        b"\r\n",
        f"--{boundary}--\r\n".encode(),
    ]
    body = b"".join(parts)
    req = urllib.request.Request(
        base_url + "/api/v1/runtime/web-publish", data=body, method="POST"
    )
    req.add_header("Content-Type", f"multipart/form-data; boundary={boundary}")
    req.add_header("Accept", "application/json")
    req.add_header("X-OC-App-Token", token)
    return _open_json(req)


def _open_json(req: urllib.request.Request) -> dict:
    """统一执行 HTTP 请求并把响应解析为 JSON。

    错误处理：
    - HTTPError（4xx / 5xx）：把状态码和响应体拼成 RuntimeError，
      让上层 stderr 输出包含具体的 manager 业务错误码；
    - URLError（网络断连 / DNS）：把异常文案直传，便于排查 manager 是否在线；
    - 空响应：返回空 dict，避免下游 .get 出 NoneType 错误；
    - 非 JSON 响应：包成 {"raw": ...}，保留原始内容方便日志排查。

    timeout=60s 覆盖 manager 端解包 + 上传对象存储的最长正常耗时；
    超过此值视为下游异常，让调用方重新发起请求而不是长挂。
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


def main(argv: list[str]) -> int:
    """解析参数并执行发布，把结果用户可读地打到 stdout。

    退出码语义：
    - 0：发布成功，stdout 输出 url 与 expires_at；
    - 1：运行时错误（配置缺失 / 目录不存在 / 网络异常等），stderr 给原因；
    - 2：参数错误（缺少 dir 位置参数），stderr 给用法提示。
    """
    # 仅取第一个位置参数作为发布目录；不再支持任何命名参数（每次都是新随机站点）。
    positional = [a for a in argv[1:] if not a.startswith("--")]
    if not positional:
        print("用法：oc-publish ./<dir>", file=sys.stderr)
        return 2
    try:
        result = _publish(positional[0])
    except Exception as exc:  # noqa: BLE001 — CLI 顶层统一兜底，错误回显给 hermes
        print(f"发布失败：{exc}", file=sys.stderr)
        return 1
    url = result.get("url", "")
    expires = result.get("expires_at", "")
    print(f"已发布：{url}（到期：{expires}）")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
