# 覆盖 ocops.conversation_files.materialize_files：
# input_file part → 下载 → cache_media_bytes → 文字注记 + <oc-file:id:enc_filename> 标记；字符串透传。
import json
import urllib.parse
from unittest import mock

from ocops import conversation_files as cf


# 字符串消息原样返回，不触发下载。
def test_string_passthrough():
    assert cf.materialize_files("hello") == "hello"


# input_file part：下载字节、调 cache_media_bytes、生成含路径与标记的注记。
# 文件名无特殊字符时 URL 编码后不变（a.pdf → a.pdf）。
def test_input_file_becomes_note_with_marker():
    fake_cached = mock.Mock(kind="document", display_name="a.pdf", path="/opt/data/cache/documents/a.pdf")
    with mock.patch.object(cf, "_download", return_value=b"PDFDATA") as dl, \
         mock.patch.object(cf, "_cache_media_bytes", return_value=fake_cached) as cm:
        out = cf.materialize_files([
            {"type": "text", "text": "看看这个"},
            {"type": "input_file", "file_id": "f1", "file_url": "https://s3/x", "filename": "a.pdf"},
        ])
    dl.assert_called_once_with("https://s3/x")
    cm.assert_called_once()
    assert "看看这个" in out
    assert "/opt/data/cache/documents/a.pdf" in out
    # marker 格式为 <oc-file:id:enc_filename>，前端凭此剥离注记并渲染文件卡片。
    assert "<oc-file:f1:a.pdf>" in out
    assert "a.pdf" in out


# 下载失败：该文件降级为「不可用」注记并带新格式标记，不抛异常，文字仍保留。
def test_download_failure_degrades():
    with mock.patch.object(cf, "_download", side_effect=RuntimeError("boom")):
        out = cf.materialize_files([
            {"type": "text", "text": "hi"},
            {"type": "input_file", "file_id": "f2", "file_url": "https://s3/y", "filename": "b.pdf"},
        ])
    assert "hi" in out
    # 降级路径同样使用新格式 marker，保持前端解析一致。
    assert "<oc-file:f2:b.pdf>" in out
    assert "b.pdf" in out


# 文件名含空格/中文时 marker 用 URL 编码，前端可 decodeURIComponent 还原。
def test_marker_encodes_filename():
    fake_cached = mock.Mock(kind="document", display_name="x", path="/opt/data/cache/documents/x")
    with mock.patch.object(cf, "_download", return_value=b"D"), \
         mock.patch.object(cf, "_cache_media_bytes", return_value=fake_cached):
        out = cf.materialize_files([{"type": "input_file", "file_id": "f9", "file_url": "https://s3/z", "filename": "我的 报告.pdf"}])
    assert f"<oc-file:f9:{urllib.parse.quote('我的 报告.pdf', safe='')}>" in out
