# ocops/conversation_files.py —— 把对话消息里的 input_file part 落到 agent 共享盘并改写为文字注记。
#
# manager 发来的消息含 input_file part（带预签名 file_url）。oc-ops 与 hermes 同 pod 共享
# /opt/data；这里下载文件、用引擎自带 cache_media_bytes 落到 agent 可见缓存路径，再把 part
# 改写成「文字注记 + <oc-file:file_id> 标记」拼进文字内容。注记里的路径让 agent 用文件工具/
# vision_analyze 读取；<oc-file:id> 标记供 manager 前端解析渲染历史文件卡片。
#
# 设计要点：
# - cache_media_bytes 为引擎模块函数，**延迟 import**（在 _cache_media_bytes 内），便于单测 mock，
#   且避免在不含 hermes-agent 的环境 import 失败。
# - 任一文件下载/落盘失败只降级为「不可用」注记，不让整轮 chat 失败。
import urllib.request

# 下载单文件的超时与大小上限（与 manager 端 100MB 上限呼应，留余量）。
_DOWNLOAD_TIMEOUT_SEC = 120
_MAX_BYTES = 120 * 1024 * 1024


def _download(url: str) -> bytes:
    """HTTP GET 预签名 URL 下载字节；超时或超限抛异常。"""
    req = urllib.request.Request(url, method="GET")
    with urllib.request.urlopen(req, timeout=_DOWNLOAD_TIMEOUT_SEC) as resp:
        data = resp.read(_MAX_BYTES + 1)
    if len(data) > _MAX_BYTES:
        raise RuntimeError("conversation file exceeds size limit")
    return data


def _cache_media_bytes(data: bytes, filename: str):
    """延迟 import 引擎 cache_media_bytes，落盘并返回 CachedMedia（path/kind/display_name）。"""
    import sys
    if "/usr/local/lib/hermes-agent" not in sys.path:
        sys.path.insert(0, "/usr/local/lib/hermes-agent")
    from gateway.platforms.base import cache_media_bytes
    return cache_media_bytes(data, filename=filename)


def _note_for(part: dict) -> str:
    """把一个 input_file part 处理成一段文字注记（含 <oc-file:id> 标记）。"""
    file_id = str(part.get("file_id") or "")
    filename = str(part.get("filename") or "file")
    url = str(part.get("file_url") or "")
    marker = f"<oc-file:{file_id}>"
    if not url:
        return f"[The user attached '{filename}', but it could not be loaded.] {marker}"
    try:
        data = _download(url)
        cached = _cache_media_bytes(data, filename)
    except Exception:
        return f"[The user attached '{filename}', but it could not be loaded.] {marker}"
    if cached is None:
        return f"[The user attached '{filename}', but its type is not supported.] {marker}"
    kind = getattr(cached, "kind", "file")
    path = getattr(cached, "path", "")
    return (
        f"[The user sent a {kind}: '{filename}'. The file is saved at: {path}. "
        f"Ask the user what they'd like you to do with it.] {marker}"
    )


def materialize_files(message):
    """把消息里的 input_file part 落盘并改写为文字。

    message 为字符串时原样返回；为 parts 数组时：text part 取文字、input_file part 转注记，
    注记置于文字之前，整体返回为单个字符串（api_server 接受字符串 message）。
    其它形态原样返回。
    """
    if isinstance(message, str):
        return message
    if not isinstance(message, list):
        return message
    text_segments = []
    note_segments = []
    for raw in message:
        if not isinstance(raw, dict):
            continue
        ptype = raw.get("type")
        if ptype == "text":
            t = str(raw.get("text") or "")
            if t:
                text_segments.append(t)
        elif ptype == "input_file":
            note_segments.append(_note_for(raw))
    base_text = "\n".join(text_segments)
    notes = "\n".join(note_segments)
    if notes and base_text:
        return f"{notes}\n\n{base_text}"
    return notes or base_text
