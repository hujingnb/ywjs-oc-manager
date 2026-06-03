"""oc-ops skill 端点业务逻辑：列出/热装/热删/触发 reload 容器内 skill。

skills/ 目录约定：
  - SKILLS_DIR 下每个子目录是一个 skill；
  - .oc-managed 标记文件存在表示该 skill 由 oc-ops 管理（可热装/热删）；
  - BUILTIN_FILE 记录内置 skill 名称列表（镜像构建期写入，运行时只读）。
"""
from __future__ import annotations

import json
import shutil
import tarfile
import urllib.request
import zipfile
from pathlib import Path

from ocops.errors import OpsError

# skills 目录：容器内 hermes skill 根目录。
SKILLS_DIR = Path("/opt/data/skills")
# 内置 skill 清单：镜像构建期写入，记录随镜像预装的 skill 名称列表。
BUILTIN_FILE = Path("/opt/skills-builtin.json")
# hermes api_server reload 端点：容器内 127.0.0.1:8642，调用后触发无重启扫描。
RELOAD_URL = "http://127.0.0.1:8642/oc/skills/reload"


def list_skills() -> dict:
    """列出 SKILLS_DIR 下所有 skill 目录，为每项标注 managed 与 builtin 属性。

    managed=True 表示该 skill 含 .oc-managed 标记文件（由 install_skill 写入）；
    builtin=True 表示 skill 名称出现在 BUILTIN_FILE 内置清单中（镜像预装）。
    SKILLS_DIR 不存在时返回空列表（容器首启尚未挂载 /opt/data）。
    """
    # 读内置清单；文件不存在或解析失败时静默视为空集
    builtin: set[str] = set()
    if BUILTIN_FILE.exists():
        try:
            builtin = set(json.loads(BUILTIN_FILE.read_text()).get("builtin", []))
        except Exception:
            # 清单损坏不阻断列表端点，仅 builtin 字段退化为 False
            builtin = set()

    out: list[dict] = []
    if SKILLS_DIR.exists():
        for d in sorted(SKILLS_DIR.iterdir()):
            if d.is_dir():
                out.append({
                    "name": d.name,
                    # .oc-managed 文件存在表示由 oc-ops 热装，否则为手动或镜像预置
                    "managed": (d / ".oc-managed").exists(),
                    "builtin": d.name in builtin,
                })
    return {"skills": out}


def install_skill(name: str, archive_path: str | Path) -> dict:
    """把归档（tar/zip）解压进 SKILLS_DIR/<name>/ 并写入 .oc-managed 标记。

    name 合法性：非空、不含 "/"、不为 "." 或 ".."（防路径穿越）。
    目录已存在时先删除再重建（幂等热装）。
    归档内容经 _safe_extract 校验越界路径，拒绝 zip-slip / tar 穿越。
    成功后返回 {"name": name}。
    """
    _validate_name(name)
    dest = SKILLS_DIR / name
    # 目标目录已存在时先删除旧版本（热装覆盖，保持幂等）
    if dest.exists():
        shutil.rmtree(dest)
    dest.mkdir(parents=True, exist_ok=True)
    _safe_extract(Path(archive_path), dest)
    # 写 .oc-managed 标记，记录来源为 app-install（便于运维识别）
    (dest / ".oc-managed").write_text('{"source":"app-install"}\n', encoding="utf-8")
    return {"name": name}


def delete_skill(name: str) -> dict:
    """热删 SKILLS_DIR/<name>/（幂等：目录不存在时正常返回）。

    name 合法性校验同 install_skill；不存在时静默成功。
    成功返回 {"name": name}。
    """
    _validate_name(name)
    dest = SKILLS_DIR / name
    # 目录不存在视为已删除（幂等），不报错
    if dest.exists():
        shutil.rmtree(dest)
    return {"name": name}


def reload_skills() -> dict:
    """触发 hermes api_server 重扫 skills/ 目录（POST 127.0.0.1:8642/oc/skills/reload）。

    调用成功后返回 api_server 原始响应体（含 added/removed/total 等字段）；
    连接失败、超时或非 2xx 响应时抛 OpsError("INTERNAL", ...)。
    此端点不重启 hermes 进程，仅触发内存级热加载。
    """
    req = urllib.request.Request(RELOAD_URL, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            # api_server 响应体为 JSON，直接解析后透传给调用方
            return json.loads(resp.read())
    except Exception as e:
        raise OpsError("INTERNAL", f"触发 skill reload 失败: {e}")


def _validate_name(name: str) -> None:
    """校验 skill 名合法性：非空、不含路径分隔符、不为特殊目录名。

    不合法时抛 OpsError("BAD_REQUEST", ...)（对应 HTTP 400）。
    """
    if not name or "/" in name or name in (".", ".."):
        raise OpsError("BAD_REQUEST", f"非法 skill 名: {repr(name)}")


def _safe_extract(archive: Path, dest: Path) -> None:
    """将 tar/zip 归档安全解压到 dest，拒绝含越界路径的条目（zip-slip / tar 穿越）。

    zip：检查所有成员名，拒绝以 "/" 开头或 parts 含 ".." 的路径；
    tar：优先使用 Python 3.12+ 的 filter="data"（剥离绝对路径与穿越）；
         低版本回退时同样逐条拒绝含越界路径的条目。
    归档格式以文件名后缀判断（.zip → ZipFile，其余按 tar 处理）。
    """
    if archive.name.lower().endswith(".zip"):
        # zip 归档：逐条校验成员名后整体解压
        with zipfile.ZipFile(archive) as zf:
            for member in zf.namelist():
                # 拒绝绝对路径（以 / 开头）或含 ".." 的路径段（路径穿越）
                if member.startswith("/") or ".." in Path(member).parts:
                    raise OpsError("BAD_REQUEST", f"归档含越界路径条目: {member}")
            zf.extractall(dest)
    else:
        # tar 归档（.tar / .tar.gz / .tgz 等）：优先用 filter="data" 防穿越
        with tarfile.open(archive) as tf:
            try:
                # Python 3.12+ filter="data" 自动剥离绝对路径与 .. 穿越
                tf.extractall(dest, filter="data")
            except TypeError:
                # 低版本 Python 不支持 filter 参数，逐条手动校验
                for member in tf.getmembers():
                    mpath = Path(member.name)
                    if member.name.startswith("/") or ".." in mpath.parts:
                        raise OpsError("BAD_REQUEST", f"归档含越界路径条目: {member.name}")
                tf.extractall(dest)
