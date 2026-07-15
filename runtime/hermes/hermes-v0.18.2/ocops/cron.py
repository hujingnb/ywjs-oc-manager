"""oc-cron 核心逻辑：Hermes Cron 的稳定适配层（可 import）。

从 oc-cron.py 下沉而来：常量、校验、jobs.json 读写、normalize、hermes CLI 调用、
以及 11 个 verb 的核心实现。每个核心函数 `run_<verb>(...)` 接收类型化形参、
返回 data（dict / list）或抛 CronError；CLI 输出层（emit_ok/emit_err、argparse）
留在 oc-cron.py 薄 shim 中。

命名：oc- 前缀取自项目名 oc-manager，标识注入 hermes runtime 镜像、供容器内调用的运维
适配层（区别于 hermes 上游自带命令）；cron = 定时任务（Hermes Cron 稳定适配层）。
"""

from __future__ import annotations

import datetime as dt
import errno
import json
import os
import re
import subprocess
import stat
from pathlib import Path
from types import SimpleNamespace

from ocops.errors import OpsError

CONTRACT_VERSION = "1.0"
OC_CRON_VERSION = "1"
SYNTHETIC_RUN_FILE = "__scheduler_metadata__.md"
MAX_OUTPUT_BYTES = 1024 * 1024
JOB_ID_RE = re.compile(r"^[A-Za-z0-9_-]{1,64}$")
TEXT_LIMITS = {"name": 200, "schedule": 200, "prompt": 5000, "deliver": 512, "script": 512, "workdir": 512}

# manager 只依赖这些稳定 verb，不感知 hermes cron 的版本差异。
FUNCTIONAL_VERBS = [
    "status", "list", "show", "history", "output",
    "create", "update", "delete", "toggle", "run",
]

# CronJob 契约字段白名单：多余的上游字段在适配层内丢弃。
JOB_FIELDS = [
    "id", "name", "prompt", "schedule", "repeat", "enabled", "state",
    "created_at", "next_run_at", "last_run_at", "last_status", "last_error",
    "last_delivery_error", "deliver", "script", "no_agent", "workdir",
    "skills", "model", "provider", "base_url",
]


class CronError(OpsError):
    """oc-cron 业务异常，继承 OpsError 以复用 code→HTTP 映射与失败信封。

    OpsError.__init__ 已是 (code, message)，与原 CronError 构造签名兼容，
    emit_err 用的 code/message 字段语义不变。
    """


class _CronArgs(SimpleNamespace):
    """适配层：把 run_* 的类型化形参包装成原 argparse `args` 对象。

    既有内部函数（_create_args/_update_args/_append_common_write_flags/
    _advanced_job_updates）通过 getattr(args, field, default) 读取字段；
    本包装提供与 argparse 一致的默认值，让这些函数零改动复用。
    """

    def __init__(self, **kwargs):
        # 默认值对齐 build_parser：write 类 flag 默认 False，append 类默认空列表。
        defaults = {
            "name": None,
            "schedule": None,
            "prompt": None,
            "deliver": None,
            "repeat": None,
            "script": None,
            "no_agent": False,
            "agent": False,
            "clear_skills": False,
            "workdir": None,
            "skill": [],
            "model": None,
            "provider": None,
            "base_url": None,
            "id": None,
        }
        defaults.update(kwargs)
        super().__init__(**defaults)


def hermes_home() -> Path:
    """返回 Hermes 数据根目录；测试通过 HERMES_HOME 隔离 cron 状态。"""
    return Path(os.environ.get("HERMES_HOME") or "/opt/data")


def validate_job_id(job_id: str) -> str:
    """校验任务 ID，避免向文件路径和 hermes CLI 透传异常参数。"""
    if not JOB_ID_RE.match(job_id or ""):
        raise CronError("BAD_REQUEST", "job id 不合法")
    return job_id


def validate_script(script: str | None) -> str | None:
    """校验 script 是仓库内相对路径，不允许绝对路径或目录逃逸。"""
    if not script:
        return script
    path = Path(script)
    if path.is_absolute() or ".." in path.parts:
        raise CronError("BAD_REQUEST", "script 不允许使用绝对路径或 ..")
    return script


def validate_output_file(file_name: str) -> str:
    """校验输出文件名只能是 cron/output/<job_id>/ 下的单个 markdown 文件。"""
    if (
        not file_name
        or "/" in file_name
        or "\\" in file_name
        or file_name in (".", "..")
        or ".." in Path(file_name).parts
    ):
        raise CronError("BAD_REQUEST", "输出文件名不合法")
    if file_name != SYNTHETIC_RUN_FILE and not file_name.endswith(".md"):
        raise CronError("BAD_REQUEST", "输出文件必须是 markdown")
    return file_name


def jobs_file() -> Path:
    """Hermes Cron 的权威任务文件位置。"""
    return hermes_home() / "cron" / "jobs.json"


def read_jobs() -> list[dict]:
    """读取 jobs.json；文件不存在表示当前没有任务。"""
    path = jobs_file()
    if not path.exists():
        return []
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise CronError("INTERNAL", f"jobs.json 读取失败: {exc}") from exc
    jobs = payload.get("jobs") if isinstance(payload, dict) else payload
    if jobs is None:
        return []
    if not isinstance(jobs, list):
        raise CronError("INTERNAL", "jobs.json 中 jobs 不是数组")
    return [job for job in jobs if isinstance(job, dict)]


def write_jobs(jobs: list[dict]) -> None:
    """写回 jobs.json；用于补齐当前 Hermes CLI 尚未暴露的字段。"""
    path = jobs_file()
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        tmp = path.with_suffix(".json.tmp")
        tmp.write_text(json.dumps({"jobs": jobs}, ensure_ascii=False, indent=2), encoding="utf-8")
        tmp.replace(path)
    except OSError as exc:
        raise CronError("INTERNAL", f"jobs.json 写入失败: {exc}") from exc


def _read_image_info() -> dict:
    """读取构建期镜像元信息；缺失时 capabilities 仍可稳定返回。"""
    try:
        return json.loads(Path(os.environ.get("OC_INFO_FILE", "/etc/oc-image.json")).read_text())
    except (OSError, json.JSONDecodeError):
        return {}


def _text(value, field: str):
    """按契约限制自由文本长度，避免 manager 收到过大的字段。"""
    if value is None:
        return None
    if not isinstance(value, str):
        value = str(value)
    limit = TEXT_LIMITS.get(field)
    return value[:limit] if limit else value


def _normalize_schedule(raw) -> dict:
    """规整 schedule，兼容 dict 与旧版字符串表达。"""
    if isinstance(raw, dict):
        display = raw.get("display") or raw.get("expr") or raw.get("value") or ""
        return {
            "kind": raw.get("kind") or "",
            "expr": raw.get("expr"),
            "display": _text(display, "schedule") or "",
        }
    if raw is None:
        return {"kind": "", "expr": None, "display": ""}
    return {"kind": "", "expr": None, "display": _text(raw, "schedule") or ""}


def _normalize_repeat(raw) -> dict:
    """规整 repeat；times 为 None 表示无限重复。"""
    if isinstance(raw, dict):
        times = raw.get("times")
        completed = raw.get("completed")
    else:
        times = raw
        completed = 0
    return {
        "times": times if isinstance(times, int) and times > 0 else None,
        "completed": completed if isinstance(completed, int) and completed >= 0 else 0,
    }


def normalize_job(raw: dict) -> dict:
    """把 Hermes jobs.json 的任务对象规整成稳定 CronJob。"""
    job = {k: raw.get(k) for k in JOB_FIELDS}
    job["id"] = raw.get("id") or ""
    job["name"] = _text(raw.get("name") or "", "name") or ""
    job["prompt"] = _text(raw.get("prompt") or "", "prompt") or ""
    job["schedule"] = _normalize_schedule(raw.get("schedule"))
    job["repeat"] = _normalize_repeat(raw.get("repeat"))
    job["enabled"] = bool(raw.get("enabled", True))
    job["state"] = raw.get("state") or ("scheduled" if job["enabled"] else "paused")
    job["deliver"] = _text(raw.get("deliver"), "deliver")
    job["script"] = _text(raw.get("script"), "script")
    job["no_agent"] = bool(raw.get("no_agent", False))
    job["workdir"] = _text(raw.get("workdir"), "workdir")
    job["skills"] = raw.get("skills") if isinstance(raw.get("skills"), list) else []
    return job


def _job_by_id(job_id: str) -> dict:
    """按 ID 查询任务，不存在时转换为契约 NOT_FOUND。"""
    validate_job_id(job_id)
    for job in read_jobs():
        if job.get("id") == job_id:
            return job
    raise CronError("NOT_FOUND", "Cron 任务不存在")


def _dir_open_flags() -> int:
    """返回目录 no-follow 打开参数；缺少关键能力时拒绝执行敏感读取。"""
    if (
        not hasattr(os, "O_DIRECTORY")
        or not hasattr(os, "O_NOFOLLOW")
        or os.open not in os.supports_dir_fd
        or os.listdir not in os.supports_fd
        or os.stat not in os.supports_dir_fd
        or os.stat not in os.supports_follow_symlinks
    ):
        raise CronError("BAD_REQUEST", "当前平台不支持安全输出目录读取")
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    if hasattr(os, "O_CLOEXEC"):
        flags |= os.O_CLOEXEC
    return flags


def _open_dir_at(parent_fd: int | None, path, message: str) -> int:
    """以 no-follow 方式打开目录；parent_fd 为 None 时打开绝对路径。"""
    kwargs = {} if parent_fd is None else {"dir_fd": parent_fd}
    try:
        return os.open(path, _dir_open_flags(), **kwargs)
    except (NotImplementedError, TypeError) as exc:
        raise CronError("BAD_REQUEST", "当前平台不支持安全输出目录读取") from exc
    except FileNotFoundError as exc:
        raise CronError("NOT_FOUND", message) from exc
    except OSError as exc:
        if exc.errno in (errno.ELOOP, errno.ENOTDIR):
            raise CronError("BAD_REQUEST", message) from exc
        raise CronError("INTERNAL", f"{message}: {exc}") from exc


def _open_output_job_dir(job_id: str) -> int:
    """用 fd-relative no-follow 语义打开 cron/output/<job_id> 目录链。"""
    validate_job_id(job_id)
    home_fd = cron_fd = output_fd = None
    try:
        home_fd = _open_dir_at(None, hermes_home(), "Hermes home 目录不可用")
        cron_fd = _open_dir_at(home_fd, "cron", "cron 目录不可用")
        output_fd = _open_dir_at(cron_fd, "output", "输出根目录不安全或不存在")
        return _open_dir_at(output_fd, job_id, "任务输出目录不安全或不存在")
    finally:
        for fd in (output_fd, cron_fd, home_fd):
            if fd is not None:
                os.close(fd)


def _file_open_flags() -> int:
    """返回输出文件 no-follow 打开参数。"""
    if not hasattr(os, "O_NOFOLLOW") or os.open not in os.supports_dir_fd:
        raise CronError("BAD_REQUEST", "当前平台不支持安全输出文件读取")
    flags = os.O_RDONLY
    if hasattr(os, "O_CLOEXEC"):
        flags |= os.O_CLOEXEC
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    # 对 FIFO 等非普通文件使用非阻塞打开，确保能先 fstat 后拒绝而不会卡住。
    if hasattr(os, "O_NONBLOCK"):
        flags |= os.O_NONBLOCK
    return flags


def _read_output_file_at(dir_fd: int, file_name: str) -> tuple[str, str]:
    """相对已打开的任务输出目录读取文件，并基于同一个 fd 完成校验和读取。"""
    try:
        fd = os.open(file_name, _file_open_flags(), dir_fd=dir_fd)
    except (NotImplementedError, TypeError) as exc:
        raise CronError("BAD_REQUEST", "当前平台不支持安全输出文件读取") from exc
    except FileNotFoundError as exc:
        raise CronError("NOT_FOUND", "输出文件不存在") from exc
    except OSError as exc:
        if exc.errno == errno.ELOOP:
            raise CronError("BAD_REQUEST", "输出文件不允许是 symlink") from exc
        raise CronError("INTERNAL", f"输出文件打开失败: {exc}") from exc
    try:
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode):
            raise CronError("BAD_REQUEST", "输出文件必须是普通文件")
        if st.st_size > MAX_OUTPUT_BYTES:
            raise CronError("BAD_REQUEST", "输出文件超过 1 MiB 限制")
        chunks = []
        remaining = MAX_OUTPUT_BYTES + 1
        while remaining > 0:
            chunk = os.read(fd, remaining)
            if not chunk:
                break
            chunks.append(chunk)
            remaining -= len(chunk)
        data = b"".join(chunks)
        if len(data) > MAX_OUTPUT_BYTES:
            raise CronError("BAD_REQUEST", "输出文件超过 1 MiB 限制")
        try:
            content = data.decode("utf-8")
        except UnicodeDecodeError as exc:
            raise CronError("INTERNAL", "输出文件不是 UTF-8 文本") from exc
        return content, dt.datetime.fromtimestamp(st.st_mtime, tz=dt.timezone.utc).isoformat()
    finally:
        os.close(fd)


def _synthetic_entry(job: dict) -> dict:
    """生成无输出文件时的调度器元数据记录。"""
    return {
        "job_id": job.get("id") or "",
        "file_name": SYNTHETIC_RUN_FILE,
        "run_time": job.get("last_run_at"),
        "size": 0,
        "has_output": False,
        "synthetic": True,
        "status": job.get("last_status"),
        "error": job.get("last_error") or job.get("last_delivery_error"),
    }


def _synthetic_content(job: dict) -> str:
    """为 synthetic output 构造可读 markdown，保留调度状态和错误摘要。"""
    lines = [
        "# Scheduler metadata",
        "",
        f"- job_id: {job.get('id') or ''}",
        f"- run_time: {job.get('last_run_at') or ''}",
        f"- status: {job.get('last_status') or ''}",
    ]
    error = job.get("last_error") or job.get("last_delivery_error")
    if error:
        lines.append(f"- error: {error}")
    lines.append("")
    lines.append("Hermes 记录了本次调度运行，但没有生成 markdown 输出文件。")
    return "\n".join(lines) + "\n"


def _classify_hermes_error(stderr: str) -> str:
    """按 hermes stderr 文本把失败归类成契约错误码。"""
    low = (stderr or "").lower()
    if "not found" in low or "no such" in low or "unknown" in low:
        return "NOT_FOUND"
    if "invalid" in low or "required" in low or "bad" in low:
        return "BAD_REQUEST"
    return "HERMES_CLI_FAILED"


def _run_hermes_cron(args: list[str]) -> subprocess.CompletedProcess:
    """执行 `hermes cron <args>`；写 verb 只经 argv 传参，不拼 shell。"""
    try:
        return subprocess.run(["hermes", "cron", *args],
                              capture_output=True, text=True, timeout=60)
    except FileNotFoundError as exc:
        raise CronError("UNSUPPORTED", "镜像内未安装 hermes") from exc
    except subprocess.TimeoutExpired as exc:
        raise CronError("HERMES_CLI_FAILED", "hermes cron 命令超时") from exc


def _hermes_ok(args: list[str]) -> None:
    """执行 hermes cron 写操作，并把失败规整成契约错误。"""
    proc = _run_hermes_cron(args)
    if proc.returncode != 0:
        msg = (proc.stderr or proc.stdout or "hermes cron 执行失败").strip()[:1024]
        raise CronError(_classify_hermes_error(msg), msg)


def _show_after_write(job_id: str) -> dict:
    """写操作完成后重读 jobs.json，返回 manager 需要的稳定对象。"""
    return normalize_job(_job_by_id(job_id))


def _select_created_job(before: list[dict], after: list[dict]) -> dict | None:
    """按新旧 ID 差集识别 create 新任务，避免依赖 jobs.json 追加顺序。"""
    before_ids = {job.get("id") for job in before if job.get("id")}
    new_jobs = [job for job in after if job.get("id") and job.get("id") not in before_ids]
    candidates = new_jobs or after
    if not candidates:
        return None
    # set 差集失败时退化为 created_at 最大的任务；字符串 ISO 时间可稳定排序。
    return max(candidates, key=lambda job: job.get("created_at") or "")


def _advanced_job_updates(args) -> dict:
    """提取 manager 支持但 Hermes CLI 未直接暴露的 per-job 字段。

    该适配继承自 v2026.7.1，并已确认当前上游 v2026.7.7.2 保持相同字段边界。
    """
    updates = {}
    for field in ("model", "provider", "base_url"):
        value = getattr(args, field, None)
        if value is None:
            continue
        text = _text(value, field)
        if field == "base_url" and text:
            text = text.rstrip("/")
        updates[field] = text or None
    return updates


def _patch_job_fields(job_id: str, updates: dict) -> None:
    """在 CLI 写操作后补写高级字段，保持 manager 的统一输入契约。"""
    if not updates:
        return
    jobs = read_jobs()
    for job in jobs:
        if job.get("id") == job_id:
            job.update(updates)
            write_jobs(jobs)
            return
    raise CronError("NOT_FOUND", "Cron 任务不存在")


def _append_common_write_flags(cmd: list[str], args) -> list[str]:
    """把 manager 稳定字段转换为当前 Hermes create/edit 支持的 argv。"""
    for field in ("name", "deliver", "workdir"):
        value = getattr(args, field, None)
        if value is not None:
            cmd += [f"--{field.replace('_', '-')}", _text(value, field) or ""]
    if getattr(args, "repeat", None) is not None:
        repeat = int(args.repeat)
        if repeat <= 0:
            raise CronError("BAD_REQUEST", "repeat 必须是正整数")
        cmd += ["--repeat", str(repeat)]
    if getattr(args, "script", None) is not None:
        cmd += ["--script", validate_script(args.script) or ""]
    if getattr(args, "no_agent", False):
        cmd.append("--no-agent")
    if getattr(args, "agent", False):
        cmd.append("--agent")
    if getattr(args, "clear_skills", False):
        cmd.append("--clear-skills")
    for skill in getattr(args, "skill", []) or []:
        cmd += ["--skill", skill]
    return cmd


def _create_args(args) -> list[str]:
    """适配 Hermes v2026.7.7.2：create 的 schedule/prompt 是位置参数。

    当前 CLI 形态继承自 v2026.7.1。
    """
    if getattr(args, "no_agent", False) and not getattr(args, "script", None):
        raise CronError("BAD_REQUEST", "no_agent 模式必须提供 script")
    cmd = _append_common_write_flags(["create"], args)
    cmd.append(_text(args.schedule, "schedule") or "")
    prompt = _text(getattr(args, "prompt", None), "prompt")
    if prompt is not None:
        cmd.append(prompt)
    return cmd


def _update_args(args) -> list[str]:
    """适配 Hermes v2026.7.7.2：edit 的 job_id 是位置参数，其余字段仍用 flag。

    当前 CLI 形态继承自 v2026.7.1。
    """
    cmd = ["edit", validate_job_id(args.id)]
    cmd = _append_common_write_flags(cmd, args)
    for field in ("schedule", "prompt"):
        value = getattr(args, field, None)
        if value is not None:
            cmd += [f"--{field}", _text(value, field) or ""]
    return cmd


def run_capabilities() -> dict:
    """自描述能力：契约版本、支持 verb、feature 开关。不依赖 hermes。

    等价原 cmd_capabilities 的 data。
    """
    info = _read_image_info()
    return {
        "contract_version": CONTRACT_VERSION,
        "oc_cron_version": OC_CRON_VERSION,
        "hermes_version": (
            info.get("hermes_upstream_ref")
            or info.get("hermes_ref")
            or info.get("hermes_version")
        ),
        "variant": info.get("variant") or info.get("oc_image_variant") or "hermes-v0.18.2",
        "verbs": FUNCTIONAL_VERBS,
        "features": {
            "status": True,
            "history": True,
            "output": True,
            "write": True,
            "script": True,
            "advanced_fields": True,
        },
    }


def run_status() -> dict:
    """查询调度器状态；上游文本只作为 message，结构化摘要来自 jobs.json。"""
    jobs = [normalize_job(job) for job in read_jobs()]
    active = [job for job in jobs if job["enabled"] and job["state"] not in ("paused", "disabled", "removed")]
    next_jobs = [job for job in active if job.get("next_run_at")]
    next_jobs.sort(key=lambda job: job.get("next_run_at") or "")
    errored = next((job for job in jobs if job.get("last_status") not in (None, "", "ok", "success")), None)
    message = ""
    try:
        proc = _run_hermes_cron(["status"])
        message = (proc.stdout or proc.stderr or "").strip()[:1024]
        gateway_running = proc.returncode == 0
    except CronError as exc:
        message = exc.message
        gateway_running = False
    return {
        "available": True,
        "gateway_running": gateway_running,
        "active_jobs": len(active),
        "next_run_at": next_jobs[0].get("next_run_at") if next_jobs else None,
        "next_job_id": next_jobs[0].get("id") if next_jobs else None,
        "tick_seconds": None,
        "pid": None,
        "message": message,
        "last_error": errored.get("last_error") if errored else None,
        "last_error_job_id": errored.get("id") if errored else None,
    }


def run_list(all_: bool) -> list:
    """列出 Cron 任务；all_ 为假时默认隐藏 disabled/removed 任务。"""
    jobs = [normalize_job(job) for job in read_jobs()]
    if not all_:
        jobs = [job for job in jobs if job["enabled"] and job["state"] not in ("disabled", "removed")]
    return jobs


def run_show(job_id: str) -> dict:
    """查询单个 Cron 任务详情。"""
    return normalize_job(_job_by_id(job_id))


def run_history(job_id: str) -> list:
    """列出任务输出历史；无 markdown 输出但有 last_run_at 时补 synthetic 记录。"""
    job = _job_by_id(job_id)
    entries = []
    job_fd = None
    try:
        job_fd = _open_output_job_dir(job_id)
    except CronError as exc:
        if exc.code in ("BAD_REQUEST", "NOT_FOUND"):
            if exc.code == "BAD_REQUEST":
                return []
            job_fd = None
        else:
            raise
    if job_fd is not None:
        try:
            names = os.listdir(job_fd)
            file_stats = []
            for name in names:
                if not name.endswith(".md"):
                    continue
                try:
                    st = os.stat(name, dir_fd=job_fd, follow_symlinks=False)
                except FileNotFoundError:
                    continue
                if stat.S_ISLNK(st.st_mode) or not stat.S_ISREG(st.st_mode):
                    continue
                file_stats.append((name, st))
            for name, st in sorted(file_stats, key=lambda item: item[1].st_mtime, reverse=True):
                entries.append({
                    "job_id": job_id,
                    "file_name": name,
                    "run_time": dt.datetime.fromtimestamp(st.st_mtime, tz=dt.timezone.utc).isoformat(),
                    "size": st.st_size,
                    "has_output": True,
                    "synthetic": False,
                    "status": None,
                    "error": None,
                })
        finally:
            os.close(job_fd)
    if not entries and job.get("last_run_at"):
        entries.append(_synthetic_entry(job))
    return entries


def run_output(job_id: str, file_name: str) -> dict:
    """读取某次 markdown 输出，严格限制在 cron/output/<job_id>/ 内。"""
    file_name = validate_output_file(file_name)
    job = _job_by_id(job_id)
    if file_name == SYNTHETIC_RUN_FILE:
        if not job.get("last_run_at"):
            raise CronError("NOT_FOUND", "synthetic 输出不存在")
        return {
            "job_id": job_id,
            "file_name": file_name,
            "run_time": job.get("last_run_at"),
            "content": _synthetic_content(job),
        }
    job_fd = _open_output_job_dir(job_id)
    try:
        content, run_time = _read_output_file_at(job_fd, file_name)
    finally:
        os.close(job_fd)
    return {
        "job_id": job_id,
        "file_name": file_name,
        "run_time": run_time,
        "content": content,
    }


def run_create(name, schedule, prompt=None, deliver=None, repeat=None, script=None,
               no_agent=False, workdir=None, skills=(), model=None, provider=None,
               base_url=None) -> dict:
    """创建 Cron 任务并在写后重读 jobs.json 返回稳定任务对象。

    等价原 cmd_create，但返回 data 而非 emit_ok。
    """
    args = _CronArgs(name=name, schedule=schedule, prompt=prompt, deliver=deliver,
                     repeat=repeat, script=script, no_agent=no_agent, workdir=workdir,
                     skill=list(skills), model=model, provider=provider, base_url=base_url)
    before = read_jobs()
    _hermes_ok(_create_args(args))          # 复用原 argv 拼装与 hermes 调用
    created = _select_created_job(before, read_jobs())
    if created:
        job_id = validate_job_id(created["id"])
        _patch_job_fields(job_id, _advanced_job_updates(args))
        return _show_after_write(job_id)
    return {}


def run_update(job_id, name=None, schedule=None, prompt=None, deliver=None, repeat=None,
               script=None, no_agent=False, agent=False, workdir=None, skills=(),
               clear_skills=False, model=None, provider=None, base_url=None) -> dict:
    """编辑 Cron 任务，并返回更新后的任务对象。等价原 cmd_update。"""
    args = _CronArgs(id=job_id, name=name, schedule=schedule, prompt=prompt, deliver=deliver,
                     repeat=repeat, script=script, no_agent=no_agent, agent=agent,
                     workdir=workdir, skill=list(skills), clear_skills=clear_skills,
                     model=model, provider=provider, base_url=base_url)
    _hermes_ok(_update_args(args))
    _patch_job_fields(job_id, _advanced_job_updates(args))
    return _show_after_write(job_id)


def run_delete(job_id) -> dict:
    """删除 Cron 任务，内部适配当前 runtime 的 remove 命令。"""
    job_id = validate_job_id(job_id)
    _hermes_ok(["remove", job_id])
    return {"ok": True}


def run_toggle(job_id, enabled: bool) -> dict:
    """按 manager 期望状态切换启停，内部翻译为 pause/resume。

    enabled 已是 bool（argparse 字符串校验在 shim 层完成）：
    True→resume，False→pause。
    """
    job_id = validate_job_id(job_id)
    hermes_verb = "resume" if enabled else "pause"
    _hermes_ok([hermes_verb, job_id])
    return _show_after_write(job_id)


def run_run(job_id) -> dict:
    """立即触发 Cron 任务，返回触发后的任务对象。"""
    job_id = validate_job_id(job_id)
    _hermes_ok(["run", job_id])
    return _show_after_write(job_id)
