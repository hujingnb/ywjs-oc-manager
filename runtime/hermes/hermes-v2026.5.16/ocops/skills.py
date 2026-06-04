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
# 镜像内置 skill 基线清单：hermes 进程首次启动时写入 SKILLS_DIR/.bundled_manifest，
# 每行 "<skill-name>:<hash>"，记录随镜像预装的全部内置 skill（叶子目录名）。
# 与 SKILLS_DIR 同在 emptyDir，oc-ops 与 hermes 共享可读——故 oc-ops 用它判 builtin，
# 而非镜像层私有的 /opt/skills-builtin.json（hermes 容器写、oc-ops 容器读不到，跨容器失效）。
BUNDLED_MANIFEST = SKILLS_DIR / ".bundled_manifest"
# hermes api_server reload 端点：容器内 127.0.0.1:8642，调用后触发无重启扫描。
RELOAD_URL = "http://127.0.0.1:8642/oc/skills/reload"


def _load_builtin_names() -> set[str]:
    """读 SKILLS_DIR/.bundled_manifest（hermes 维护的镜像内置基线），返回内置 skill 名集合。

    每行格式 "<name>:<hash>"，取冒号前的 name。文件不存在或解析失败时返回空集
    （退化：非 managed 的 skill 一律按 self_created 显示，不阻断列表端点）。
    """
    names: set[str] = set()
    if BUNDLED_MANIFEST.exists():
        try:
            for line in BUNDLED_MANIFEST.read_text().splitlines():
                line = line.strip()
                if not line:
                    continue
                # 仅取冒号前的 skill 名；行内无冒号时整行即为名。
                names.add(line.split(":", 1)[0].strip())
        except Exception:
            # 清单损坏不阻断列表端点，仅 builtin 字段退化为 False。
            return set()
    names.discard("")
    return names


def _read_skill_meta(skill_md: Path, fallback: str) -> tuple[str, str]:
    """从 SKILL.md frontmatter 一次性读 name 与 description 两个字段。

    name：hermes 的 skill 规范名（与 .bundled_manifest 一致，用于 builtin 匹配）。镜像内置
    skill 的目录叶子名常与规范名不同（如目录 audiocraft 的 SKILL.md name=audiocraft-audio-generation），
    故 builtin 匹配必须用规范名而非目录名；解析失败/缺字段时回退到 fallback（目录叶子名）。

    description：skill 介绍，供详情页展示内置/自建 skill 的元数据。取 description: 行的值，
    去引号；若值用引号且跨行（引号未闭合），累积后续行直到闭合引号；缺失时返回空字符串。
    返回 (name, description)。
    """
    try:
        lines = skill_md.read_text(encoding="utf-8", errors="replace").splitlines()
    except Exception:
        return fallback, ""
    # frontmatter 必须以首行 "---" 开始；否则视为无元数据，回退到目录名、空描述。
    if not lines or lines[0].strip() != "---":
        return fallback, ""
    name = fallback
    description = ""
    i = 1
    while i < len(lines):
        s = lines[i].strip()
        if s == "---":
            break
        if s.startswith("name:"):
            val = s[len("name:"):].strip().strip('"').strip("'")
            if val:
                name = val
        elif s.startswith("description:"):
            raw = s[len("description:"):].strip()
            # 带引号的值可能跨行：累积后续行直到出现配对的闭合引号。
            if raw[:1] in ("'", '"'):
                quote = raw[0]
                body = raw[1:]
                if body.endswith(quote) and len(body) >= 1:
                    description = body[:-1]
                else:
                    parts = [body]
                    i += 1
                    while i < len(lines):
                        ln = lines[i]
                        if ln.rstrip().endswith(quote):
                            parts.append(ln.rstrip()[:-1])
                            break
                        parts.append(ln)
                        i += 1
                    description = " ".join(p.strip() for p in parts).strip()
            else:
                # 无引号：取本行值（多行无引号 YAML 不规范，仅取首行即可，足够展示）。
                description = raw
        i += 1
    return name, description


def list_skills() -> dict:
    """递归扫描 SKILLS_DIR 下所有含 SKILL.md 的目录，每个视为一个 skill，标注 managed 与 builtin。

    hermes 的 skill 目录既有扁平结构（<name>/SKILL.md：oc-ops 安装、oc-kb、版本种子），
    也有层级结构（<category>/<name>/SKILL.md：镜像内置）；统一以「含 SKILL.md 的目录」为一个
    skill，skill 名取该目录叶子名（与 .bundled_manifest 命名一致）。

    历史教训：旧实现只遍历 SKILLS_DIR 直接子目录，把内置的 category 目录（apple/github…）
    误当成 skill，且内置子目录下的真实 skill 反而漏列。

    managed=True：该 skill 目录含 .oc-managed 标记（由 install_skill / render_skills 写入）；
    builtin=True：skill 名出现在 .bundled_manifest 镜像内置基线中。
    SKILLS_DIR 不存在时返回空列表（容器首启尚未挂载 /opt/data）。
    """
    builtin = _load_builtin_names()
    out: list[dict] = []
    if SKILLS_DIR.exists():
        seen: set[str] = set()
        for skill_md in sorted(SKILLS_DIR.rglob("SKILL.md")):
            d = skill_md.parent
            name = d.name
            # 同名 skill（不同层级）只保留首个，避免重复条目。
            if name in seen:
                continue
            seen.add(name)
            # 对外 name 用目录叶子名（= manager 安装时的目录名，保证与 app_skills.name 对账一致）；
            # builtin 判断改用 SKILL.md frontmatter 的规范名匹配 .bundled_manifest（叶子名常与规范名不同）。
            # 同时读 description，供详情页展示内置/自建 skill 的介绍。
            manifest_name, description = _read_skill_meta(skill_md, name)
            out.append({
                "name": name,
                # .oc-managed 文件存在表示由 oc-ops 热装/版本渲染，否则为镜像内置或手动自建。
                "managed": (d / ".oc-managed").exists(),
                "builtin": manifest_name in builtin,
                "description": description,
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
    归档格式以**内容**判断（zipfile.is_zipfile 识别 zip 魔数/中央目录，其余按 tar 处理），
    不依赖文件名后缀：上传方（manager SkillInstall）的 multipart filename 用的是 skill 名、
    不带 .zip/.tar 扩展名，若按后缀判断会把 ClawHub 的 zip 误当 tar 解压而失败。
    """
    if zipfile.is_zipfile(archive):
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
