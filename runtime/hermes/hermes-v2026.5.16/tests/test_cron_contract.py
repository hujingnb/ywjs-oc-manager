"""ocops.cron 契约一致性测试。

直接调用核心函数 ocops.cron.run_*，断言返回的 data（dict/list）或抛出的
CronError，而非经由 oc-cron.py CLI 解析 stdout 信封。每个测试使用独立
HERMES_HOME，避免 cron/jobs.json 与输出文件互相污染；涉及 hermes CLI 的
写 verb 通过 monkeypatch _run_hermes_cron 桩，避免真调本机不存在的 hermes。
"""

from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path

import pytest

from ocops import cron
from ocops.cron import CronError


def write_jobs_file(home: Path, jobs: list[dict]) -> None:
    """在 HERMES_HOME 下写入 cron/jobs.json，模拟 hermes 的权威任务文件。"""
    cron_dir = home / "cron"
    cron_dir.mkdir(parents=True, exist_ok=True)
    (cron_dir / "jobs.json").write_text(json.dumps({"jobs": jobs}, ensure_ascii=False), encoding="utf-8")


def write_output(home: Path, job_id: str, file_name: str, content: str) -> Path:
    """在 cron/output/<job_id>/ 下写入一个输出文件并返回其路径。"""
    output_dir = home / "cron" / "output" / job_id
    output_dir.mkdir(parents=True, exist_ok=True)
    path = output_dir / file_name
    path.write_text(content, encoding="utf-8")
    return path


def stub_hermes(monkeypatch, home: Path, *, on_call=None):
    """把 _run_hermes_cron 桩为记录 argv 的成功调用，避免真调 hermes。

    每次调用把 ["cron", *args] 追加到 home/hermes-args.log；可选 on_call
    回调用于模拟 hermes 写 jobs.json 等副作用。返回成功的 CompletedProcess。
    """
    log_file = home / "hermes-args.log"

    def fake_run(args: list[str]) -> subprocess.CompletedProcess:
        with log_file.open("a", encoding="utf-8") as fh:
            fh.write(" ".join(["cron", *args]) + "\n")
        if on_call is not None:
            on_call(args)
        return subprocess.CompletedProcess(args=["hermes", "cron", *args], returncode=0, stdout="", stderr="")

    monkeypatch.setattr(cron, "_run_hermes_cron", fake_run)
    return log_file


def read_hermes_args(home: Path) -> list[str]:
    """读取被 stub 记录的 hermes argv 行。"""
    return (home / "hermes-args.log").read_text(encoding="utf-8").splitlines()


# 覆盖：capabilities 不依赖 jobs.json，必须返回 manager 识别契约所需的元数据。
def test_run_capabilities_returns_contract_metadata(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    data = cron.run_capabilities()
    assert data["contract_version"] == "1.0"
    assert "list" in data["verbs"]
    assert data["features"]["history"] is True


# 覆盖：capabilities 只暴露 manager 面向的稳定 verb，不泄露 runtime 内部命令名。
def test_run_capabilities_exposes_manager_contract_verbs(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    data = cron.run_capabilities()
    assert data["verbs"] == [
        "status", "list", "show", "history", "output",
        "create", "update", "delete", "toggle", "run",
    ]


# 覆盖：capabilities 优先读取 Dockerfile 写入的 hermes_upstream_ref 元信息。
def test_run_capabilities_reads_hermes_upstream_ref(tmp_path: Path, monkeypatch) -> None:
    info_file = tmp_path / "oc-image.json"
    info_file.write_text(json.dumps({"hermes_upstream_ref": "v1.2.3"}), encoding="utf-8")
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    monkeypatch.setenv("OC_INFO_FILE", str(info_file))
    assert cron.run_capabilities()["hermes_version"] == "v1.2.3"


# 覆盖：status 从 jobs.json 计算 active_jobs 和下一次执行摘要，hermes CLI 不可用时也返回结构。
def test_run_status_summarizes_jobs_json(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    # hermes 不可用时 status 仍要靠 jobs.json 产出结构化摘要。
    monkeypatch.setattr(cron, "_run_hermes_cron",
                        lambda args: (_ for _ in ()).throw(CronError("UNSUPPORTED", "无 hermes")))
    write_jobs_file(tmp_path, [{
        "id": "abc123",
        "name": "日报",
        "schedule": {"display": "0 9 * * *"},
        "enabled": True,
        "state": "scheduled",
        "next_run_at": "2026-05-21T09:00:00+00:00",
    }])
    data = cron.run_status()
    assert data["active_jobs"] == 1
    assert data["next_job_id"] == "abc123"
    assert data["gateway_running"] is False


# 覆盖：list 从 jobs.json 读取任务，并保留 schedule.display 等稳定展示字段。
def test_run_list_normalizes_jobs_json(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{
        "id": "abc123",
        "name": "日报",
        "prompt": "生成摘要",
        "schedule": {"kind": "cron", "expr": "0 9 * * *", "display": "0 9 * * *"},
        "repeat": {"times": None, "completed": 2},
        "enabled": True,
        "state": "scheduled",
        "created_at": "2026-05-20T00:00:00+00:00",
        "next_run_at": "2026-05-21T09:00:00+00:00",
        "deliver": "local",
    }])
    jobs = cron.run_list(True)
    assert jobs[0]["id"] == "abc123"
    assert jobs[0]["schedule"]["display"] == "0 9 * * *"


# 覆盖：list 不传 all_ 时默认过滤 disabled/removed 任务，只返回活跃任务。
def test_run_list_filters_disabled_when_not_all(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [
        {"id": "live", "name": "活跃", "schedule": {"display": "0 9 * * *"},
         "enabled": True, "state": "scheduled"},  # 活跃任务：默认应保留
        {"id": "dead", "name": "停用", "schedule": {"display": "0 9 * * *"},
         "enabled": False, "state": "disabled"},   # disabled 任务：默认应过滤
    ])
    ids = [job["id"] for job in cron.run_list(False)]
    assert ids == ["live"]
    # all_=True 时不过滤，两条都返回。
    assert {job["id"] for job in cron.run_list(True)} == {"live", "dead"}


# 覆盖：show 按 ID 返回单个规整后的 CronJob。
def test_run_show_returns_normalized_job(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{
        "id": "abc123",
        "name": "日报",
        "schedule": {"kind": "cron", "display": "0 9 * * *"},
        "enabled": True,
    }])
    job = cron.run_show("abc123")
    assert job["id"] == "abc123"
    assert job["schedule"]["display"] == "0 9 * * *"


# 异常路径：show 不存在的任务 → CronError(NOT_FOUND)。
def test_run_show_not_found(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    with pytest.raises(CronError) as exc:
        cron.run_show("nope")
    assert exc.value.code == "NOT_FOUND"


# 覆盖：调度器有 last_run_at 但无输出文件时，history 补充 synthetic 运行记录。
def test_run_history_adds_synthetic_entry_when_no_output(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{
        "id": "abc123",
        "name": "巡检",
        "schedule": {"kind": "interval", "display": "every 30m"},
        "enabled": True,
        "state": "scheduled",
        "last_run_at": "2026-05-20T08:00:00+00:00",
        "last_status": "ok",
        "script": "check.py",
        "no_agent": True,
    }])
    entries = cron.run_history("abc123")
    assert entries[0]["file_name"] == "__scheduler_metadata__.md"
    assert entries[0]["synthetic"] is True


# 覆盖：output 成功读取普通 markdown 文件，并返回内容与文件名。
def test_run_output_reads_markdown_file(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    write_output(tmp_path, "abc123", "run.md", "# ok\n")
    data = cron.run_output("abc123", "run.md")
    assert data["file_name"] == "run.md"
    assert data["content"] == "# ok\n"


# 覆盖：history 不列出 symlink 输出，避免通过链接暴露输出目录外的文件。
def test_run_history_rejects_symlink_output_entries(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{
        "id": "abc123",
        "name": "巡检",
        "schedule": {"kind": "interval", "display": "every 30m"},
        "enabled": True,
        "state": "scheduled",
        "last_run_at": "2026-05-20T08:00:00+00:00",
    }])
    outside = tmp_path / "secret.md"
    outside.write_text("secret", encoding="utf-8")
    output_dir = tmp_path / "cron" / "output" / "abc123"
    output_dir.mkdir(parents=True, exist_ok=True)
    (output_dir / "linked.md").symlink_to(outside)
    entries = cron.run_history("abc123")
    assert all(entry["file_name"] != "linked.md" for entry in entries)


# 异常路径：output 直接读取 symlink 文件必须拒绝，不跟随链接读取目录外内容。
def test_run_output_rejects_symlink_file(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    outside = tmp_path / "secret.md"
    outside.write_text("secret", encoding="utf-8")
    output_dir = tmp_path / "cron" / "output" / "abc123"
    output_dir.mkdir(parents=True, exist_ok=True)
    (output_dir / "linked.md").symlink_to(outside)
    with pytest.raises(CronError) as exc:
        cron.run_output("abc123", "linked.md")
    assert exc.value.code == "BAD_REQUEST"


# 覆盖：history 遇到任务输出目录本身是 symlink 时，不能列出目录外 markdown。
def test_run_history_rejects_symlink_output_directory(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{
        "id": "abc123",
        "name": "巡检",
        "schedule": {"kind": "interval", "display": "every 30m"},
        "enabled": True,
        "state": "scheduled",
    }])
    outside_dir = tmp_path / "external-output"
    outside_dir.mkdir()
    (outside_dir / "secret.md").write_text("secret", encoding="utf-8")
    output_root = tmp_path / "cron" / "output"
    output_root.mkdir(parents=True, exist_ok=True)
    (output_root / "abc123").symlink_to(outside_dir, target_is_directory=True)
    assert cron.run_history("abc123") == []


# 覆盖：history 遇到 cron/output 本身是 symlink 时，不能把外部目录当作输出根。
def test_run_history_rejects_symlink_output_root(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    outside_dir = tmp_path / "external-output" / "abc123"
    outside_dir.mkdir(parents=True)
    (outside_dir / "secret.md").write_text("secret", encoding="utf-8")
    (tmp_path / "cron" / "output").symlink_to(tmp_path / "external-output", target_is_directory=True)
    assert cron.run_history("abc123") == []


# 异常路径：output 遇到任务输出目录本身是 symlink 时，必须拒绝读取外部文件。
def test_run_output_rejects_symlink_output_directory(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    outside_dir = tmp_path / "external-output"
    outside_dir.mkdir()
    (outside_dir / "secret.md").write_text("secret", encoding="utf-8")
    output_root = tmp_path / "cron" / "output"
    output_root.mkdir(parents=True, exist_ok=True)
    (output_root / "abc123").symlink_to(outside_dir, target_is_directory=True)
    with pytest.raises(CronError) as exc:
        cron.run_output("abc123", "secret.md")
    assert exc.value.code == "BAD_REQUEST"


# 异常路径：output 遇到 cron/output 本身是 symlink 时，必须拒绝读取外部文件。
def test_run_output_rejects_symlink_output_root(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    outside_dir = tmp_path / "external-output" / "abc123"
    outside_dir.mkdir(parents=True)
    (outside_dir / "secret.md").write_text("secret", encoding="utf-8")
    (tmp_path / "cron" / "output").symlink_to(tmp_path / "external-output", target_is_directory=True)
    with pytest.raises(CronError) as exc:
        cron.run_output("abc123", "secret.md")
    assert exc.value.code == "BAD_REQUEST"


# 异常路径：output 对过大 markdown 设置上限，避免一次性读取超大文件。
def test_run_output_rejects_large_file(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    path = write_output(tmp_path, "abc123", "large.md", "")
    with path.open("wb") as fh:
        fh.truncate(1024 * 1024 + 1)
    with pytest.raises(CronError) as exc:
        cron.run_output("abc123", "large.md")
    assert exc.value.code == "BAD_REQUEST"


# 异常路径：output 打开后必须用 fstat 拒绝非普通文件，避免 check/read 之间的文件替换风险。
def test_run_output_rejects_non_regular_file(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    output_dir = tmp_path / "cron" / "output" / "abc123"
    output_dir.mkdir(parents=True, exist_ok=True)
    os.mkfifo(output_dir / "pipe.md")
    with pytest.raises(CronError) as exc:
        cron.run_output("abc123", "pipe.md")
    assert exc.value.code == "BAD_REQUEST"


# 异常路径：output 拒绝目录逃逸文件名，即使任务本身不存在也先返回参数错误。
def test_run_output_rejects_unsafe_file_name(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    with pytest.raises(CronError) as exc:
        cron.run_output("abc123", "../secret.md")
    assert exc.value.code == "BAD_REQUEST"


# 覆盖：create 写后通过新旧 ID 差集选择新增任务，不依赖 jobs.json 追加顺序。
def test_run_create_returns_new_job_when_hermes_writes_out_of_order(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{
        "id": "old_job",
        "name": "旧任务",
        "schedule": {"display": "0 8 * * *"},
        "created_at": "2026-05-20T00:00:00+00:00",
    }])

    def on_call(_args):
        # 模拟 hermes 以乱序写回 jobs.json（新任务排在前面）。
        write_jobs_file(tmp_path, [
            {"id": "new_job", "name": "新任务", "schedule": {"display": "0 9 * * *"},
             "created_at": "2026-05-21T00:00:00+00:00"},
            {"id": "old_job", "name": "旧任务", "schedule": {"display": "0 8 * * *"},
             "created_at": "2026-05-20T00:00:00+00:00"},
        ])

    log = stub_hermes(monkeypatch, tmp_path, on_call=on_call)
    data = cron.run_create(name="新任务", schedule="0 9 * * *")
    assert data["id"] == "new_job"
    assert log.read_text(encoding="utf-8").splitlines() == ["cron create --name 新任务 0 9 * * *"]


# 异常路径：create 的 no_agent 模式必须提供 script，避免把无可执行内容的任务交给上游。
def test_run_create_no_agent_requires_script(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    stub_hermes(monkeypatch, tmp_path)
    with pytest.raises(CronError) as exc:
        cron.run_create(name="巡检", schedule="*/5 * * * *", no_agent=True)
    assert exc.value.code == "BAD_REQUEST"


# 覆盖：update 是 manager 稳定 verb，内部翻译为当前 runtime 的 hermes cron edit 位置参数。
def test_run_update_translates_to_hermes_edit(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{"id": "abc123", "name": "旧任务", "schedule": {"display": "0 8 * * *"}}])
    log = stub_hermes(monkeypatch, tmp_path)
    cron.run_update(job_id="abc123", name="新任务")
    assert log.read_text(encoding="utf-8").splitlines() == ["cron edit abc123 --name 新任务"]


# 覆盖：Hermes CLI 未暴露 model/provider/base_url 时，update 在 jobs.json 补齐高级字段。
def test_run_update_patches_advanced_fields_after_hermes_edit(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{"id": "abc123", "name": "旧任务", "schedule": {"display": "0 8 * * *"}}])
    log = stub_hermes(monkeypatch, tmp_path)
    data = cron.run_update(job_id="abc123", model="gpt-5", provider="openai",
                           base_url="https://api.example/v1/")
    assert data["model"] == "gpt-5"
    assert data["provider"] == "openai"
    # base_url 末尾斜杠被规整去除。
    assert data["base_url"] == "https://api.example/v1"
    assert log.read_text(encoding="utf-8").splitlines() == ["cron edit abc123"]


# 覆盖：delete 是 manager 稳定 verb，内部翻译为当前 runtime 的 hermes cron remove 位置参数。
def test_run_delete_translates_to_hermes_remove(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    log = stub_hermes(monkeypatch, tmp_path)
    assert cron.run_delete("abc123") == {"ok": True}
    assert log.read_text(encoding="utf-8").splitlines() == ["cron remove abc123"]


# 覆盖：toggle False 内部翻译为 pause 位置参数，并返回任务对象。
def test_run_toggle_false_translates_to_hermes_pause(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    log = stub_hermes(monkeypatch, tmp_path)
    job = cron.run_toggle("abc123", False)
    assert job["id"] == "abc123"
    assert log.read_text(encoding="utf-8").splitlines() == ["cron pause abc123"]


# 覆盖：toggle True 内部翻译为 resume 位置参数，并返回任务对象。
def test_run_toggle_true_translates_to_hermes_resume(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    log = stub_hermes(monkeypatch, tmp_path)
    job = cron.run_toggle("abc123", True)
    assert job["id"] == "abc123"
    assert log.read_text(encoding="utf-8").splitlines() == ["cron resume abc123"]


# 覆盖：run 保持 manager 稳定 verb，并调用 hermes cron run 位置参数。
def test_run_run_translates_to_hermes_run(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    write_jobs_file(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    log = stub_hermes(monkeypatch, tmp_path)
    job = cron.run_run("abc123")
    assert job["id"] == "abc123"
    assert log.read_text(encoding="utf-8").splitlines() == ["cron run abc123"]
