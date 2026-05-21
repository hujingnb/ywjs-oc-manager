"""oc-cron 契约一致性测试。

每个测试使用独立 HERMES_HOME，避免 cron/jobs.json 与输出文件互相污染。
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path


def run_oc_cron(*args: str, home: Path, extra_env: dict[str, str] | None = None) -> tuple[dict, int]:
    env = {**os.environ, "HERMES_HOME": str(home), **(extra_env or {})}
    source_script = Path(__file__).resolve().parents[1] / "oc-cron.py"
    # 测试既支持源码目录布局，也支持 Docker 镜像内的 /usr/local/bin 安装布局。
    cmd = [sys.executable, str(source_script)] if source_script.exists() else ["oc-cron"]
    proc = subprocess.run([*cmd, *args], cwd=Path(__file__).parents[1],
                          env=env, capture_output=True, text=True, timeout=10)
    try:
        payload = json.loads(proc.stdout)
    except json.JSONDecodeError as exc:
        raise AssertionError(f"stdout 非 JSON: {proc.stdout!r}\nstderr: {proc.stderr}") from exc
    return payload, proc.returncode


def write_jobs(home: Path, jobs: list[dict]) -> None:
    cron_dir = home / "cron"
    cron_dir.mkdir(parents=True, exist_ok=True)
    (cron_dir / "jobs.json").write_text(json.dumps({"jobs": jobs}, ensure_ascii=False), encoding="utf-8")


def write_output(home: Path, job_id: str, file_name: str, content: str) -> Path:
    output_dir = home / "cron" / "output" / job_id
    output_dir.mkdir(parents=True, exist_ok=True)
    path = output_dir / file_name
    path.write_text(content, encoding="utf-8")
    return path


def install_fake_hermes(home: Path) -> dict[str, str]:
    bin_dir = home / "bin"
    bin_dir.mkdir()
    fake_hermes = bin_dir / "hermes"
    fake_hermes.write_text("""#!/bin/sh
printf '%s\\n' "$*" >> "$HERMES_HOME/hermes-args.log"
exit 0
""", encoding="utf-8")
    fake_hermes.chmod(0o755)
    return {"PATH": f"{bin_dir}:{os.environ['PATH']}"}


def read_fake_hermes_args(home: Path) -> list[str]:
    log_file = home / "hermes-args.log"
    return log_file.read_text(encoding="utf-8").splitlines()


# 覆盖：capabilities 不依赖 jobs.json，必须返回 manager 识别契约所需的元数据。
def test_capabilities_returns_contract_metadata(tmp_path: Path) -> None:
    env, code = run_oc_cron("capabilities", home=tmp_path)
    assert code == 0
    assert env["ok"] is True
    assert env["data"]["contract_version"] == "1.0"
    assert "list" in env["data"]["verbs"]
    assert env["data"]["features"]["history"] is True


# 覆盖：capabilities 只暴露 manager 面向的稳定 verb，不泄露 runtime 内部命令名。
def test_capabilities_exposes_manager_contract_verbs(tmp_path: Path) -> None:
    env, code = run_oc_cron("capabilities", home=tmp_path)
    assert code == 0
    assert env["data"]["verbs"] == [
        "status", "list", "show", "history", "output",
        "create", "update", "delete", "toggle", "run",
    ]


# 覆盖：capabilities 优先读取 Dockerfile 写入的 hermes_upstream_ref 元信息。
def test_capabilities_reads_hermes_upstream_ref(tmp_path: Path) -> None:
    info_file = tmp_path / "oc-image.json"
    info_file.write_text(json.dumps({"hermes_upstream_ref": "v1.2.3"}), encoding="utf-8")
    env, code = run_oc_cron("capabilities", home=tmp_path, extra_env={"OC_INFO_FILE": str(info_file)})
    assert code == 0
    assert env["data"]["hermes_version"] == "v1.2.3"


# 覆盖：argparse 用法错误也必须返回 JSON 信封，便于 manager 统一解析错误。
def test_parser_errors_return_json_envelope(tmp_path: Path) -> None:
    env, code = run_oc_cron("show", home=tmp_path)
    assert code == 1
    assert env["ok"] is False
    assert env["error"]["code"] == "BAD_REQUEST"


# 覆盖：status 从 jobs.json 计算 active_jobs 和下一次执行摘要，hermes CLI 不可用时也返回信封。
def test_status_summarizes_jobs_json(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{
        "id": "abc123",
        "name": "日报",
        "schedule": {"display": "0 9 * * *"},
        "enabled": True,
        "state": "scheduled",
        "next_run_at": "2026-05-21T09:00:00+00:00",
    }])
    env, code = run_oc_cron("status", home=tmp_path)
    assert code == 0
    assert env["ok"] is True
    assert env["data"]["active_jobs"] == 1
    assert env["data"]["next_job_id"] == "abc123"


# 覆盖：list 从 jobs.json 读取任务，并保留 schedule.display 等稳定展示字段。
def test_list_normalizes_jobs_json(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{
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
    env, code = run_oc_cron("list", "--all", home=tmp_path)
    assert code == 0
    assert env["ok"] is True
    assert env["data"][0]["id"] == "abc123"
    assert env["data"][0]["schedule"]["display"] == "0 9 * * *"


# 覆盖：show 按 ID 返回单个规整后的 CronJob。
def test_show_returns_normalized_job(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{
        "id": "abc123",
        "name": "日报",
        "schedule": {"kind": "cron", "display": "0 9 * * *"},
        "enabled": True,
    }])
    env, code = run_oc_cron("show", "--id", "abc123", home=tmp_path)
    assert code == 0
    assert env["ok"] is True
    assert env["data"]["id"] == "abc123"
    assert env["data"]["schedule"]["display"] == "0 9 * * *"


# 覆盖：调度器有 last_run_at 但无输出文件时，history 补充 synthetic 运行记录。
def test_history_adds_synthetic_entry_when_no_output(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{
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
    env, code = run_oc_cron("history", "--id", "abc123", home=tmp_path)
    assert code == 0
    assert env["data"][0]["file_name"] == "__scheduler_metadata__.md"
    assert env["data"][0]["synthetic"] is True


# 覆盖：output 成功读取普通 markdown 文件，并返回内容与文件名。
def test_output_reads_markdown_file(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    write_output(tmp_path, "abc123", "run.md", "# ok\n")
    env, code = run_oc_cron("output", "--id", "abc123", "--file", "run.md", home=tmp_path)
    assert code == 0
    assert env["ok"] is True
    assert env["data"]["file_name"] == "run.md"
    assert env["data"]["content"] == "# ok\n"


# 覆盖：history 不列出 symlink 输出，避免通过链接暴露输出目录外的文件。
def test_history_rejects_symlink_output_entries(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{
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
    env, code = run_oc_cron("history", "--id", "abc123", home=tmp_path)
    assert code == 0
    assert all(entry["file_name"] != "linked.md" for entry in env["data"])


# 覆盖：output 直接读取 symlink 时必须拒绝，不能跟随链接读取目录外内容。
def test_output_rejects_symlink_file(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    outside = tmp_path / "secret.md"
    outside.write_text("secret", encoding="utf-8")
    output_dir = tmp_path / "cron" / "output" / "abc123"
    output_dir.mkdir(parents=True, exist_ok=True)
    (output_dir / "linked.md").symlink_to(outside)
    env, code = run_oc_cron("output", "--id", "abc123", "--file", "linked.md", home=tmp_path)
    assert code == 1
    assert env["error"]["code"] == "BAD_REQUEST"


# 覆盖：history 遇到任务输出目录本身是 symlink 时，不能列出目录外 markdown。
def test_history_rejects_symlink_output_directory(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{
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
    env, code = run_oc_cron("history", "--id", "abc123", home=tmp_path)
    assert code == 0
    assert env["ok"] is True
    assert env["data"] == []


# 覆盖：history 遇到 cron/output 本身是 symlink 时，不能把外部目录当作输出根。
def test_history_rejects_symlink_output_root(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    outside_dir = tmp_path / "external-output" / "abc123"
    outside_dir.mkdir(parents=True)
    (outside_dir / "secret.md").write_text("secret", encoding="utf-8")
    (tmp_path / "cron" / "output").symlink_to(tmp_path / "external-output", target_is_directory=True)
    env, code = run_oc_cron("history", "--id", "abc123", home=tmp_path)
    assert code == 0
    assert env["ok"] is True
    assert env["data"] == []


# 覆盖：output 遇到任务输出目录本身是 symlink 时，必须拒绝读取外部文件。
def test_output_rejects_symlink_output_directory(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    outside_dir = tmp_path / "external-output"
    outside_dir.mkdir()
    (outside_dir / "secret.md").write_text("secret", encoding="utf-8")
    output_root = tmp_path / "cron" / "output"
    output_root.mkdir(parents=True, exist_ok=True)
    (output_root / "abc123").symlink_to(outside_dir, target_is_directory=True)
    env, code = run_oc_cron("output", "--id", "abc123", "--file", "secret.md", home=tmp_path)
    assert code == 1
    assert env["ok"] is False
    assert env["error"]["code"] == "BAD_REQUEST"


# 覆盖：output 遇到 cron/output 本身是 symlink 时，必须拒绝读取外部文件。
def test_output_rejects_symlink_output_root(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    outside_dir = tmp_path / "external-output" / "abc123"
    outside_dir.mkdir(parents=True)
    (outside_dir / "secret.md").write_text("secret", encoding="utf-8")
    (tmp_path / "cron" / "output").symlink_to(tmp_path / "external-output", target_is_directory=True)
    env, code = run_oc_cron("output", "--id", "abc123", "--file", "secret.md", home=tmp_path)
    assert code == 1
    assert env["ok"] is False
    assert env["error"]["code"] == "BAD_REQUEST"


# 覆盖：output 对过大 markdown 设置上限，避免一次性读取超大文件。
def test_output_rejects_large_file(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    path = write_output(tmp_path, "abc123", "large.md", "")
    with path.open("wb") as fh:
        fh.truncate(1024 * 1024 + 1)
    env, code = run_oc_cron("output", "--id", "abc123", "--file", "large.md", home=tmp_path)
    assert code == 1
    assert env["error"]["code"] == "BAD_REQUEST"


# 覆盖：output 打开后必须用 fstat 拒绝非普通文件，避免 check/read 之间的文件替换风险。
def test_output_rejects_non_regular_file(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    output_dir = tmp_path / "cron" / "output" / "abc123"
    output_dir.mkdir(parents=True, exist_ok=True)
    os.mkfifo(output_dir / "pipe.md")
    env, code = run_oc_cron("output", "--id", "abc123", "--file", "pipe.md", home=tmp_path)
    assert code == 1
    assert env["error"]["code"] == "BAD_REQUEST"


# 覆盖：output 拒绝目录逃逸文件名，即使任务本身不存在也先返回参数错误。
def test_rejects_unsafe_output_file(tmp_path: Path) -> None:
    env, code = run_oc_cron("output", "--id", "abc123", "--file", "../secret.md", home=tmp_path)
    assert code == 1
    assert env["ok"] is False
    assert env["error"]["code"] == "BAD_REQUEST"


# 覆盖：create 写后通过新旧 ID 差集选择新增任务，不依赖 jobs.json 追加顺序。
def test_create_returns_new_job_when_hermes_writes_out_of_order(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{
        "id": "old_job",
        "name": "旧任务",
        "schedule": {"display": "0 8 * * *"},
        "created_at": "2026-05-20T00:00:00+00:00",
    }])
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    fake_hermes = bin_dir / "hermes"
    fake_hermes.write_text(f"""#!/bin/sh
printf '%s\\n' "$*" >> "$HERMES_HOME/hermes-args.log"
mkdir -p "$HERMES_HOME/cron"
cat > "$HERMES_HOME/cron/jobs.json" <<'JSON'
{{"jobs":[
  {{"id":"new_job","name":"新任务","schedule":{{"display":"0 9 * * *"}},"created_at":"2026-05-21T00:00:00+00:00"}},
  {{"id":"old_job","name":"旧任务","schedule":{{"display":"0 8 * * *"}},"created_at":"2026-05-20T00:00:00+00:00"}}
]}}
JSON
exit 0
""", encoding="utf-8")
    fake_hermes.chmod(0o755)
    env, code = run_oc_cron("create", "--name", "新任务", "--schedule", "0 9 * * *",
                            home=tmp_path, extra_env={"PATH": f"{bin_dir}:{os.environ['PATH']}"})
    assert code == 0
    assert env["data"]["id"] == "new_job"
    assert read_fake_hermes_args(tmp_path) == ["cron create --name 新任务 0 9 * * *"]


# 覆盖：create 的 no_agent 模式必须提供 script，避免把无可执行内容的任务交给上游。
def test_create_no_agent_requires_script(tmp_path: Path) -> None:
    env, code = run_oc_cron("create", "--name", "巡检", "--schedule", "*/5 * * * *", "--no-agent",
                            home=tmp_path, extra_env=install_fake_hermes(tmp_path))
    assert code == 1
    assert env["ok"] is False
    assert env["error"]["code"] == "BAD_REQUEST"


# 覆盖：update 是 manager 稳定 verb，内部翻译为当前 runtime 的 hermes cron edit 位置参数。
def test_update_translates_to_hermes_edit(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{"id": "abc123", "name": "旧任务", "schedule": {"display": "0 8 * * *"}}])
    env, code = run_oc_cron("update", "--id", "abc123", "--name", "新任务",
                            home=tmp_path, extra_env=install_fake_hermes(tmp_path))
    assert code == 0
    assert env["ok"] is True
    assert read_fake_hermes_args(tmp_path) == ["cron edit abc123 --name 新任务"]


# 覆盖：Hermes CLI 未暴露 model/provider/base_url 时，oc-cron 在 jobs.json 补齐高级字段。
def test_update_patches_advanced_fields_after_hermes_edit(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{"id": "abc123", "name": "旧任务", "schedule": {"display": "0 8 * * *"}}])
    env, code = run_oc_cron("update", "--id", "abc123", "--model", "gpt-5", "--provider", "openai",
                            "--base-url", "https://api.example/v1/",
                            home=tmp_path, extra_env=install_fake_hermes(tmp_path))
    assert code == 0
    assert env["ok"] is True
    assert env["data"]["model"] == "gpt-5"
    assert env["data"]["provider"] == "openai"
    assert env["data"]["base_url"] == "https://api.example/v1"
    assert read_fake_hermes_args(tmp_path) == ["cron edit abc123"]


# 覆盖：delete 是 manager 稳定 verb，内部翻译为当前 runtime 的 hermes cron remove 位置参数。
def test_delete_translates_to_hermes_remove(tmp_path: Path) -> None:
    env, code = run_oc_cron("delete", "--id", "abc123", home=tmp_path,
                            extra_env=install_fake_hermes(tmp_path))
    assert code == 0
    assert env["ok"] is True
    assert read_fake_hermes_args(tmp_path) == ["cron remove abc123"]


# 覆盖：toggle --enabled false 内部翻译为 pause 位置参数，并返回任务信封。
def test_toggle_false_translates_to_hermes_pause(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    env, code = run_oc_cron("toggle", "--id", "abc123", "--enabled", "false",
                            home=tmp_path, extra_env=install_fake_hermes(tmp_path))
    assert code == 0
    assert env["ok"] is True
    assert read_fake_hermes_args(tmp_path) == ["cron pause abc123"]


# 覆盖：toggle --enabled true 内部翻译为 resume 位置参数，并返回任务信封。
def test_toggle_true_translates_to_hermes_resume(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    env, code = run_oc_cron("toggle", "--id", "abc123", "--enabled", "true",
                            home=tmp_path, extra_env=install_fake_hermes(tmp_path))
    assert code == 0
    assert env["ok"] is True
    assert read_fake_hermes_args(tmp_path) == ["cron resume abc123"]


# 覆盖：run 保持 manager 稳定 verb，并调用 hermes cron run 位置参数。
def test_run_translates_to_hermes_run(tmp_path: Path) -> None:
    write_jobs(tmp_path, [{"id": "abc123", "name": "巡检", "schedule": {"display": "every 30m"}}])
    env, code = run_oc_cron("run", "--id", "abc123", home=tmp_path,
                            extra_env=install_fake_hermes(tmp_path))
    assert code == 0
    assert env["ok"] is True
    assert read_fake_hermes_args(tmp_path) == ["cron run abc123"]
