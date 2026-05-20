# Hermes Cron Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an app detail "定时任务" tab that manages Hermes Cron jobs through a version-stable `oc-cron` runtime adapter.

**Architecture:** Each Hermes runtime variant owns its own `oc-cron` command and hides upstream Hermes version differences behind one JSON-envelope contract. Manager calls `oc-cron` with `ContainerExecJSON`, exposes app-scoped REST APIs, and the web app follows the existing Kanban tab layout: left list, right detail/history. Manager does not persist Cron snapshots.

**Tech Stack:** Python 3.13 runtime adapter + pytest/jsonschema, Go/Gin/sqlc/service tests, OpenAPI via swag, Vue 3 + TypeScript + TanStack Vue Query + Naive UI + Vitest, browser verification.

---

## Scope Check

This is one vertical feature because runtime, manager API, OpenAPI, and web UI must ship together to produce usable Cron management. The plan is split into task-sized commits so each layer becomes testable before the next layer consumes it.

## File Structure

- Create `runtime/hermes/cron-contract/SPEC.md`: stable `oc-cron` contract, versioning, command semantics.
- Create `runtime/hermes/cron-contract/schema/*.schema.json`: success/error envelope and core Cron data schemas.
- Create `runtime/hermes/hermes-main/oc-cron.py`: Hermes Cron version adapter for `hermes-main`.
- Modify `runtime/hermes/hermes-main/Dockerfile` and `Dockerfile.dev`: copy `oc-cron` and contract into the image, chmod executable.
- Modify `Makefile`: inject both `kanban-contract` and `cron-contract` into the selected variant before runtime builds.
- Create `runtime/hermes/hermes-main/tests/test_cron_contract.py`: runtime adapter contract tests.
- Modify `internal/auth/authorizer.go` and `internal/auth/authorizer_test.go`: Cron permission predicates.
- Modify `internal/service/errors.go`: Cron sentinel errors.
- Create `internal/service/hermes_cron_types.go`: Go DTOs for the `oc-cron` contract.
- Create `internal/service/hermes_cron.go`: app location, permission, validation, `oc-cron` execution, response parsing.
- Create `internal/service/hermes_cron_test.go`: service tests for argv, validation, errors, and parsing.
- Modify `internal/api/handlers/dto.go`: request DTOs for Cron create/update/actions.
- Create `internal/api/handlers/hermes_cron.go`: Gin handlers and routes.
- Create `internal/api/handlers/hermes_cron_test.go`: handler binding, field stripping, route behavior.
- Modify `internal/api/router.go`: dependency field and route registration.
- Modify `cmd/server/main.go`: construct and inject `HermesCronService`.
- Run generated outputs: `openapi/openapi.yaml`, `web/src/api/generated.ts`.
- Create `web/src/api/hooks/useCron.ts`: Vue Query hooks and types.
- Create `web/src/pages/apps/cron/CronJobList.vue`, `CronJobDetail.vue`, `CronJobFormModal.vue`, `CronRunHistory.vue`: focused UI components.
- Create `web/src/pages/apps/AppCronTab.vue`: top-level tab composed like `AppKanbanTab.vue`.
- Modify `web/src/app/router.ts` and `web/src/pages/apps/AppDetailPage.vue`: add route and tab.
- Add/update Vitest specs under `web/src/pages/apps/*.spec.ts` and `web/src/pages/apps/cron/*.spec.ts`.

---

### Task 1: Runtime Cron Contract And Image Wiring

**Files:**
- Create: `runtime/hermes/cron-contract/SPEC.md`
- Create: `runtime/hermes/cron-contract/schema/envelope.schema.json`
- Create: `runtime/hermes/cron-contract/schema/job.schema.json`
- Create: `runtime/hermes/cron-contract/schema/status.schema.json`
- Create: `runtime/hermes/cron-contract/schema/run-entry.schema.json`
- Create: `runtime/hermes/cron-contract/schema/run-output.schema.json`
- Modify: `Makefile`
- Modify: `runtime/hermes/hermes-main/Dockerfile`
- Modify: `runtime/hermes/hermes-main/Dockerfile.dev`
- Test: shell checks only in this task

- [ ] **Step 1: Add contract documentation**

Create `runtime/hermes/cron-contract/SPEC.md` with this structure:

```markdown
# oc-cron Contract

`oc-cron` is the version adapter between oc-manager and the Hermes Cron implementation bundled in each runtime variant.

## Versioning

- Contract version is `1.0`.
- Manager may consume any `1.x` adapter.
- Breaking changes require `2.0`; manager must reject unsupported major versions.
- Each runtime variant owns its adapter implementation and hides upstream Hermes CLI/API/file differences.

## Envelope

Successful non-streaming commands write exactly one JSON object to stdout:

```json
{"ok":true,"data":{}}
```

Failed commands write:

```json
{"ok":false,"error":{"code":"BAD_REQUEST","message":"schedule is required"}}
```

## Error Codes

| Code | Meaning |
|---|---|
| `BAD_REQUEST` | Invalid user input or unsafe path |
| `NOT_FOUND` | Job or output file not found |
| `UNSUPPORTED` | Runtime does not include real Hermes Cron |
| `HERMES_CLI_FAILED` | `hermes cron` failed |
| `INTERNAL` | Adapter could not parse or normalize runtime data |

## Verbs

- `capabilities`
- `status`
- `list --all`
- `show --id <job_id>`
- `create`
- `edit --id <job_id>`
- `pause --id <job_id>`
- `resume --id <job_id>`
- `run --id <job_id>`
- `remove --id <job_id>`
- `history --id <job_id>`
- `output --id <job_id> --file <file_name>`
```

- [ ] **Step 2: Add JSON schemas**

Create schema files with minimal strict fields used by tests. `envelope.schema.json`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "oneOf": [
    {
      "required": ["ok", "data"],
      "properties": {
        "ok": { "const": true },
        "data": {}
      },
      "additionalProperties": false
    },
    {
      "required": ["ok", "error"],
      "properties": {
        "ok": { "const": false },
        "error": {
          "type": "object",
          "required": ["code", "message"],
          "properties": {
            "code": { "enum": ["BAD_REQUEST", "NOT_FOUND", "UNSUPPORTED", "HERMES_CLI_FAILED", "INTERNAL"] },
            "message": { "type": "string" }
          },
          "additionalProperties": false
        }
      },
      "additionalProperties": false
    }
  ]
}
```

Create the other schema files with `type: object`, `additionalProperties: true`, and required identifiers:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["id", "name", "schedule", "enabled", "state"],
  "additionalProperties": true
}
```

Use required keys `["available","message"]` for `status`, `["job_id","file_name","run_time","has_output"]` for `run-entry`, and `["job_id","file_name","run_time","content"]` for `run-output`.

- [ ] **Step 3: Wire contract injection in Makefile**

Modify `hermes-inject-contract` to copy both contracts:

```make
hermes-inject-contract: ## 把契约工件注入变体目录（避开 Dockerfile 跨目录 COPY 约束）
	rm -rf $(HERMES_VARIANT_DIR)/kanban-contract
	cp -r runtime/hermes/kanban-contract $(HERMES_VARIANT_DIR)/kanban-contract
	rm -rf $(HERMES_VARIANT_DIR)/cron-contract
	cp -r runtime/hermes/cron-contract $(HERMES_VARIANT_DIR)/cron-contract
```

- [ ] **Step 4: Wire Dockerfiles**

In both `runtime/hermes/hermes-main/Dockerfile` and `runtime/hermes/hermes-main/Dockerfile.dev`, add:

```dockerfile
COPY oc-cron.py           /usr/local/bin/oc-cron
COPY cron-contract/       /usr/local/lib/oc-cron/contract/
```

Add `oc-cron` to the existing `chmod +x` line:

```dockerfile
            /usr/local/bin/oc-kanban /usr/local/bin/oc-cron /usr/local/bin/oc-healthcheck
```

For `Dockerfile.dev`, include `/usr/local/bin/oc-cron` on the dev chmod line.

- [ ] **Step 5: Verify contract injection**

Run:

```bash
make hermes-inject-contract HERMES_VARIANT=hermes-main
test -f runtime/hermes/hermes-main/cron-contract/SPEC.md
test -f runtime/hermes/hermes-main/cron-contract/schema/envelope.schema.json
```

Expected: all commands exit 0.

- [ ] **Step 6: Commit Task 1**

```bash
git add Makefile runtime/hermes/cron-contract runtime/hermes/hermes-main/Dockerfile runtime/hermes/hermes-main/Dockerfile.dev runtime/hermes/hermes-main/cron-contract
git commit -m "feat(hermes-runtime): 增加 oc-cron 契约注入"
```

Commit body:

```text
新增 oc-cron 的稳定契约文档和 schema。

runtime 构建时把 cron-contract 注入 variant 目录，并在 hermes-main Dockerfile 中预留 oc-cron 安装路径。
```

---

### Task 2: Runtime `oc-cron` Adapter

**Files:**
- Create: `runtime/hermes/hermes-main/oc-cron.py`
- Create: `runtime/hermes/hermes-main/tests/test_cron_contract.py`
- Test: `runtime/hermes/hermes-main/tests/test_cron_contract.py`

- [ ] **Step 1: Write failing contract tests**

Create `runtime/hermes/hermes-main/tests/test_cron_contract.py` with these initial tests:

```python
"""oc-cron 契约一致性测试。

每个测试使用独立 HERMES_HOME，避免 cron/jobs.json 与输出文件互相污染。
"""

from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path

import pytest


def run_oc_cron(*args: str, home: Path) -> tuple[dict, int]:
    env = {**os.environ, "HERMES_HOME": str(home)}
    proc = subprocess.run(["python", "oc-cron.py", *args], cwd=Path(__file__).parents[1],
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


def test_capabilities_returns_contract_metadata(tmp_path: Path) -> None:
    env, code = run_oc_cron("capabilities", home=tmp_path)
    assert code == 0
    assert env["ok"] is True
    assert env["data"]["contract_version"] == "1.0"
    assert "list" in env["data"]["verbs"]
    assert env["data"]["features"]["history"] is True


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


def test_rejects_unsafe_output_file(tmp_path: Path) -> None:
    env, code = run_oc_cron("output", "--id", "abc123", "--file", "../secret.md", home=tmp_path)
    assert code == 1
    assert env["ok"] is False
    assert env["error"]["code"] == "BAD_REQUEST"
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
cd runtime/hermes/hermes-main && python -m pytest tests/test_cron_contract.py -v
```

Expected: FAIL because `oc-cron.py` does not exist.

- [ ] **Step 3: Implement `oc-cron.py`**

Create `runtime/hermes/hermes-main/oc-cron.py`. Required implementation shape:

```python
#!/usr/bin/env python3
"""oc-cron —— Hermes Cron 的稳定适配层。"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import re
import subprocess
import sys
from pathlib import Path

CONTRACT_VERSION = "1.0"
OC_CRON_VERSION = "1"
SYNTHETIC_RUN_FILE = "__scheduler_metadata__.md"
JOB_ID_RE = re.compile(r"^[A-Za-z0-9_-]{1,64}$")
TEXT_LIMITS = {"name": 200, "schedule": 200, "prompt": 5000, "deliver": 512, "script": 512, "workdir": 512}


class CronError(Exception):
    def __init__(self, code: str, message: str):
        super().__init__(message)
        self.code = code
        self.message = message


def hermes_home() -> Path:
    return Path(os.environ.get("HERMES_HOME", "/opt/data"))


def emit_ok(data) -> int:
    print(json.dumps({"ok": True, "data": data}, ensure_ascii=False))
    return 0


def emit_err(code: str, message: str) -> int:
    print(json.dumps({"ok": False, "error": {"code": code, "message": message}}, ensure_ascii=False))
    return 1
```

Then add helpers:

```python
def validate_job_id(value: str) -> str:
    if not JOB_ID_RE.match(value or ""):
        raise CronError("BAD_REQUEST", "非法 job id")
    return value


def validate_script(value: str | None) -> str | None:
    if not value:
        return None
    if value.startswith("/") or ".." in Path(value).parts or "/" in value or "\\" in value:
        raise CronError("BAD_REQUEST", "script 必须是相对文件名")
    return value


def validate_output_file(value: str) -> str:
    if value == SYNTHETIC_RUN_FILE:
        return value
    if "/" in value or "\\" in value or ".." in value or not value.endswith(".md"):
        raise CronError("BAD_REQUEST", "非法输出文件名")
    return value


def jobs_file() -> Path:
    return hermes_home() / "cron" / "jobs.json"


def read_jobs() -> list[dict]:
    path = jobs_file()
    if not path.exists():
        return []
    raw = json.loads(path.read_text(encoding="utf-8"))
    if isinstance(raw, list):
        return [j for j in raw if isinstance(j, dict)]
    if isinstance(raw, dict) and isinstance(raw.get("jobs"), list):
        return [j for j in raw["jobs"] if isinstance(j, dict)]
    raise CronError("INTERNAL", "jobs.json 格式不合法")
```

Implement `normalize_job`, `cmd_capabilities`, `cmd_list`, `cmd_show`, `cmd_history`, and `cmd_output` with the fields from the design spec. For write verbs, call `hermes cron ...` using `subprocess.run(["hermes", "cron", ...], capture_output=True, text=True, timeout=60)` and then re-read `jobs.json`.

- [ ] **Step 4: Add CLI parser**

Use explicit subcommands:

```python
def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="oc-cron")
    sub = p.add_subparsers(dest="verb", required=True)
    sub.add_parser("capabilities")
    sub.add_parser("status")
    list_p = sub.add_parser("list")
    list_p.add_argument("--all", action="store_true")
    show_p = sub.add_parser("show")
    show_p.add_argument("--id", required=True)
    history_p = sub.add_parser("history")
    history_p.add_argument("--id", required=True)
    output_p = sub.add_parser("output")
    output_p.add_argument("--id", required=True)
    output_p.add_argument("--file", required=True)
    create_p = sub.add_parser("create")
    create_p.add_argument("--name")
    create_p.add_argument("--schedule", required=True)
    create_p.add_argument("--prompt", default="")
    create_p.add_argument("--deliver")
    create_p.add_argument("--repeat", type=int)
    create_p.add_argument("--script")
    create_p.add_argument("--no-agent", action="store_true")
    create_p.add_argument("--workdir")
    create_p.add_argument("--skill", action="append", default=[])
    create_p.add_argument("--model")
    create_p.add_argument("--provider")
    create_p.add_argument("--base-url")
    edit_p = sub.add_parser("edit")
    edit_p.add_argument("--id", required=True)
    edit_p.add_argument("--name")
    edit_p.add_argument("--schedule")
    edit_p.add_argument("--prompt")
    edit_p.add_argument("--deliver")
    edit_p.add_argument("--repeat", type=int)
    edit_p.add_argument("--script")
    edit_p.add_argument("--no-agent", action="store_true")
    edit_p.add_argument("--agent", action="store_true")
    edit_p.add_argument("--workdir")
    edit_p.add_argument("--skill", action="append", default=[])
    edit_p.add_argument("--clear-skills", action="store_true")
    edit_p.add_argument("--model")
    edit_p.add_argument("--provider")
    edit_p.add_argument("--base-url")
    for verb in ("pause", "resume", "run", "remove"):
        vp = sub.add_parser(verb)
        vp.add_argument("--id", required=True)
    return p
```

- [ ] **Step 5: Run runtime tests**

Run:

```bash
cd runtime/hermes/hermes-main && python -m pytest tests/test_cron_contract.py -v
```

Expected: PASS.

- [ ] **Step 6: Commit Task 2**

```bash
git add runtime/hermes/hermes-main/oc-cron.py runtime/hermes/hermes-main/tests/test_cron_contract.py
git commit -m "feat(hermes-runtime): 增加 oc-cron 适配命令"
```

Commit body:

```text
新增 hermes-main variant 的 oc-cron 适配命令。

适配层负责规整 jobs.json、读取 cron 输出历史、封装 hermes cron 写操作，并以统一 JSON 信封对 manager 暴露能力。
```

---

### Task 3: Auth, Errors, Go Types, And Service

**Files:**
- Modify: `internal/auth/authorizer.go`
- Modify: `internal/auth/authorizer_test.go`
- Modify: `internal/service/errors.go`
- Create: `internal/service/hermes_cron_types.go`
- Create: `internal/service/hermes_cron.go`
- Create: `internal/service/hermes_cron_test.go`

- [ ] **Step 1: Add failing auth tests**

Append to `internal/auth/authorizer_test.go`:

```go
// TestCanViewAppCron 验证 Cron 读权限三层角色判断与实例详情一致。
func TestCanViewAppCron(t *testing.T) {
	cases := []appPermissionCase{
		{"platform_admin 跨组织可看 cron", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：平台管理员跨组织查看
		{"org_admin 同组织可看 cron", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},           // 场景：组织管理员查看本组织实例
		{"org_admin 跨组织不可看 cron", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},        // 场景：组织管理员越权
		{"org_member 拥有者可看 cron", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},        // 场景：实例拥有者查看
		{"org_member 非拥有者不可看 cron", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},   // 场景：普通成员查看他人实例
	}
	runAppCases(t, CanViewAppCron, cases)
}

// TestCanManageAppCron 验证 Cron 写权限与读权限一致。
func TestCanManageAppCron(t *testing.T) {
	cases := []appPermissionCase{
		{"platform_admin 可管理 cron", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：平台管理员跨组织管理
		{"org_admin 同组织可管理 cron", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},     // 场景：组织管理员管理本组织实例
		{"org_admin 跨组织不可管理 cron", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},  // 场景：组织管理员越权
		{"org_member 拥有者可管理 cron", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},  // 场景：实例拥有者管理
	}
	runAppCases(t, CanManageAppCron, cases)
}
```

Run:

```bash
go test ./internal/auth -run 'TestCan(View|Manage)AppCron' -v
```

Expected: FAIL with undefined `CanViewAppCron`.

- [ ] **Step 2: Implement auth predicates**

Add to `internal/auth/authorizer.go` near Kanban predicates:

```go
// Cron 定时任务 -----------------------------------------------------------

// CanViewAppCron 判断 principal 能否查看应用的 Hermes Cron 定时任务。
// 与查看应用详情同权限：平台管理员、本组织管理员、应用拥有者本人。
func CanViewAppCron(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanViewApp(p, appOrgID, appOwnerUserID)
}

// CanManageAppCron 判断 principal 能否管理应用的 Hermes Cron 定时任务。
// 首版与 Kanban 一致：所有能查看实例详情的角色都能读写 Cron。
func CanManageAppCron(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanViewApp(p, appOrgID, appOwnerUserID)
}
```

Run the auth test again. Expected: PASS.

- [ ] **Step 3: Add service errors and types**

Add to `internal/service/errors.go`:

```go
// Cron 定时任务 -----------------------------------------------------------

// ErrCronForbidden 表示当前 principal 无权访问该实例的 Cron。
var ErrCronForbidden = errors.New("无权访问该实例定时任务")

// ErrCronRuntimeUnavailable 表示实例容器未运行，无法执行 oc-cron。
var ErrCronRuntimeUnavailable = errors.New("实例容器未运行")

// ErrCronNotSupported 表示该实例镜像不支持 Cron 管理。
var ErrCronNotSupported = errors.New("该实例镜像不支持定时任务")

// ErrCronCLI 表示 oc-cron 或 hermes cron 执行失败。
var ErrCronCLI = errors.New("cron 命令执行失败")

// ErrCronOutputInvalid 表示 oc-cron 输出不是合法 JSON 信封或数据结构不匹配。
var ErrCronOutputInvalid = errors.New("cron 输出解析失败")

// ErrCronBadRequest 表示 Cron 请求参数非法。
var ErrCronBadRequest = errors.New("cron 请求参数非法")
```

Create `internal/service/hermes_cron_types.go`:

```go
package service

// CronSchedule 是 Hermes Cron 的调度表达式规整结果。
type CronSchedule struct {
	Kind    string `json:"kind,omitempty"`
	Expr    string `json:"expr,omitempty"`
	Display string `json:"display,omitempty"`
	RunAt   string `json:"run_at,omitempty"`
	Minutes int    `json:"minutes,omitempty"`
}

// CronRepeat 描述 Cron 任务重复次数与已完成次数。
type CronRepeat struct {
	Times     *int `json:"times"`
	Completed int  `json:"completed"`
}

// CronJob 是 oc-cron 返回的稳定任务结构。
type CronJob struct {
	ID                string       `json:"id"`
	Name              string       `json:"name"`
	Prompt            string       `json:"prompt,omitempty"`
	Schedule          CronSchedule `json:"schedule"`
	Repeat            CronRepeat   `json:"repeat"`
	Enabled           bool         `json:"enabled"`
	State             string       `json:"state"`
	PausedAt          string       `json:"paused_at,omitempty"`
	PausedReason      string       `json:"paused_reason,omitempty"`
	CreatedAt         string       `json:"created_at,omitempty"`
	NextRunAt         string       `json:"next_run_at,omitempty"`
	LastRunAt         string       `json:"last_run_at,omitempty"`
	LastStatus        string       `json:"last_status,omitempty"`
	LastError         string       `json:"last_error,omitempty"`
	LastDeliveryError string       `json:"last_delivery_error,omitempty"`
	Deliver           string       `json:"deliver,omitempty"`
	Script            string       `json:"script,omitempty"`
	NoAgent           bool         `json:"no_agent"`
	Workdir           string       `json:"workdir,omitempty"`
	Skills            []string     `json:"skills,omitempty"`
	Model             string       `json:"model,omitempty"`
	Provider          string       `json:"provider,omitempty"`
	BaseURL           string       `json:"base_url,omitempty"`
}

// CronStatus 是 oc-cron status 的稳定结构。
type CronStatus struct {
	Available      bool   `json:"available"`
	GatewayRunning bool   `json:"gateway_running"`
	ActiveJobs     int    `json:"active_jobs"`
	NextRunAt      string `json:"next_run_at,omitempty"`
	NextJobID      string `json:"next_job_id,omitempty"`
	TickSeconds    int    `json:"tick_seconds,omitempty"`
	PID            string `json:"pid,omitempty"`
	Message        string `json:"message"`
}

// CronRunEntry 是某个 Cron job 的一次输出历史记录。
type CronRunEntry struct {
	JobID     string `json:"job_id"`
	FileName  string `json:"file_name"`
	RunTime   string `json:"run_time"`
	Size      int64  `json:"size"`
	HasOutput bool   `json:"has_output"`
	Synthetic bool   `json:"synthetic,omitempty"`
	Status    string `json:"status,omitempty"`
	Error     string `json:"error,omitempty"`
}

// CronRunOutput 是 markdown 输出文件内容。
type CronRunOutput struct {
	JobID    string `json:"job_id"`
	FileName string `json:"file_name"`
	RunTime  string `json:"run_time"`
	Content  string `json:"content"`
}

// CronCapabilities 描述 oc-cron 的契约版本与能力。
type CronCapabilities struct {
	ContractVersion string            `json:"contract_version"`
	OCCronVersion   string            `json:"oc_cron_version"`
	HermesVersion   string            `json:"hermes_version,omitempty"`
	Variant         string            `json:"variant,omitempty"`
	Verbs           []string          `json:"verbs"`
	Features        map[string]bool   `json:"features"`
}
```

- [ ] **Step 4: Write failing service tests**

Create `internal/service/hermes_cron_test.go` modeled after `hermes_kanban_test.go`. Include tests:

```go
// TestCronListJobsHappy 验证：正常 app 上 ListJobs 解析 oc-cron 信封 JSON 输出。
func TestCronListJobsHappy(t *testing.T) {
	execer := &fakeCronExecer{result: runtime.ExecJSONResult{
		ExitCode: 0,
		Stdout: cronOKEnvelope(`[{"id":"j_1","name":"日报","schedule":{"kind":"cron","display":"0 9 * * *"},"repeat":{"times":null,"completed":0},"enabled":true,"state":"scheduled"}]`),
	}}
	svc := NewHermesCronService(execer, &fakeCronLocator{loc: healthyCronLoc()})

	jobs, err := svc.ListJobs(context.Background(), cronOrgAdmin(), "app-1", CronJobFilter{All: true})
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, "j_1", jobs[0].ID)
	assert.Equal(t, []string{"oc-cron", "list", "--all"}, execer.lastCmd)
}
```

Also add:

- `TestCronResolveForbidden`
- `TestCronResolveStubUnsupported`
- `TestCronResolveRuntimeUnavailable`
- `TestCronErrorCodeMapping`
- `TestCronRejectsBadJobID`
- `TestCronCreateBuildsArgv`
- `TestCronCapabilitiesParsesEnvelope`

Run:

```bash
go test ./internal/service -run 'TestCron' -v
```

Expected: FAIL with undefined `HermesCronService`.

- [ ] **Step 5: Implement `HermesCronService`**

Create `internal/service/hermes_cron.go` using the same structure as `HermesKanbanService`:

- `cronExecer` interface with `ContainerExecJSON`.
- `cronAppLocator` interface.
- `CronAppLocation` struct matching `KanbanAppLocation`.
- `CronAppLocatorFromStore` matching `KanbanAppLocatorFromStore`.
- `runOCCron(ctx, loc, args)` that prepends `oc-cron`, parses envelope, maps errors.
- public methods:
  - `Capabilities`
  - `Status`
  - `ListJobs`
  - `ShowJob`
  - `CreateJob`
  - `UpdateJob`
  - `PauseJob`
  - `ResumeJob`
  - `RunJob`
  - `DeleteJob`
  - `History`
  - `Output`

Validation constants:

```go
var cronJobIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var cronScriptRe = regexp.MustCompile(`^[^/\\][^/\\]*$`)
```

Keep free-text limits from the spec.

- [ ] **Step 6: Run service tests**

Run:

```bash
go test ./internal/auth ./internal/service -run 'Cron|Can(View|Manage)AppCron' -v
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3**

```bash
git add internal/auth/authorizer.go internal/auth/authorizer_test.go internal/service/errors.go internal/service/hermes_cron.go internal/service/hermes_cron_types.go internal/service/hermes_cron_test.go
git commit -m "feat(cron): 增加 Hermes Cron 服务层"
```

Commit body:

```text
新增 app-scoped Hermes Cron service。

后端通过 oc-cron 稳定信封访问容器内 Cron 能力，并补充权限谓词、错误映射、参数校验和 service 单元测试。
```

---

### Task 4: Manager HTTP API And OpenAPI

**Files:**
- Modify: `internal/api/handlers/dto.go`
- Create: `internal/api/handlers/hermes_cron.go`
- Create: `internal/api/handlers/hermes_cron_test.go`
- Modify: `internal/api/handlers/request_errors.go`
- Modify: `internal/api/router.go`
- Modify: `cmd/server/main.go`
- Generated: `openapi/openapi.yaml`
- Generated: `web/src/api/generated.ts`

- [ ] **Step 1: Add DTOs**

Add to `internal/api/handlers/dto.go`:

```go
// CreateCronJobRequest 是新建 Hermes Cron 任务的请求体。
type CreateCronJobRequest struct {
	Name     string   `json:"name"`
	Schedule string   `json:"schedule" binding:"required"`
	Prompt   string   `json:"prompt"`
	Deliver  string   `json:"deliver"`
	Repeat   *int     `json:"repeat"`
	Script   string   `json:"script"`
	NoAgent  bool     `json:"no_agent"`
	Workdir  string   `json:"workdir"`
	Skills   []string `json:"skills"`
	Model    string   `json:"model"`
	Provider string   `json:"provider"`
	BaseURL  string   `json:"base_url"`
}

// UpdateCronJobRequest 是编辑 Hermes Cron 任务的请求体；所有字段可选。
type UpdateCronJobRequest struct {
	Name        *string  `json:"name"`
	Schedule    *string  `json:"schedule"`
	Prompt      *string  `json:"prompt"`
	Deliver     *string  `json:"deliver"`
	Repeat      *int     `json:"repeat"`
	ClearRepeat  bool     `json:"clear_repeat"`
	Script      *string  `json:"script"`
	NoAgent     *bool    `json:"no_agent"`
	Workdir     *string  `json:"workdir"`
	Skills      []string `json:"skills"`
	ClearSkills bool     `json:"clear_skills"`
	Model       *string  `json:"model"`
	Provider    *string  `json:"provider"`
	BaseURL     *string  `json:"base_url"`
}
```

- [ ] **Step 2: Write failing handler tests**

Create `internal/api/handlers/hermes_cron_test.go` with a fake service that records inputs. Cover:

- non-platform admin create strips `Skills/Model/Provider/BaseURL`;
- platform admin create keeps advanced fields;
- service `ErrCronNotSupported` maps to `CRON_NOT_SUPPORTED_ON_STUB`;
- `GET /jobs/:jobId/history` returns `{runs:[...]}`.

Run:

```bash
go test ./internal/api/handlers -run 'TestHermesCron' -v
```

Expected: FAIL because handler does not exist.

- [ ] **Step 3: Implement handler**

Create `internal/api/handlers/hermes_cron.go`. Match Kanban handler style:

- `hermesCronService` interface lists service methods.
- `HermesCronHandler` struct.
- `RegisterHermesCronRoutes`.
- `writeCronError`.
- `stripCronAdvancedFields(principal, input)`.

Route group:

```go
g := router.Group("/api/v1/apps/:appId/hermes/cron")
g.GET("/capabilities", h.Capabilities)
g.GET("/status", h.Status)
g.GET("/jobs", h.ListJobs)
g.POST("/jobs", h.CreateJob)
g.GET("/jobs/:jobId", h.ShowJob)
g.PATCH("/jobs/:jobId", h.UpdateJob)
g.DELETE("/jobs/:jobId", h.DeleteJob)
g.POST("/jobs/:jobId/pause", h.PauseJob)
g.POST("/jobs/:jobId/resume", h.ResumeJob)
g.POST("/jobs/:jobId/run", h.RunJob)
g.GET("/jobs/:jobId/history", h.History)
g.GET("/jobs/:jobId/output/:fileName", h.Output)
```

- [ ] **Step 4: Register errors and routes**

In `internal/api/handlers/request_errors.go`, add Cron mappings from the design spec.

In `internal/api/router.go`, add dependency:

```go
HermesCronService *service.HermesCronService
```

Register after Kanban:

```go
if dep.HermesCronService != nil {
	handlers.RegisterHermesCronRoutes(user, handlers.NewHermesCronHandler(dep.HermesCronService))
}
```

- [ ] **Step 5: Wire server main**

In `cmd/server/main.go`, near Kanban setup:

```go
cronLocator := service.NewCronAppLocatorFromStore(store)
hermesCronService := service.NewHermesCronService(runtimeAdapter, cronLocator)
```

Inject into `api.Dependencies`:

```go
HermesCronService: hermesCronService,
```

- [ ] **Step 6: Run Go API tests**

Run:

```bash
go test ./internal/api/handlers ./internal/api ./cmd/server ./internal/service ./internal/auth -run 'Cron|Router|Server|Can(View|Manage)AppCron' -v
```

Expected: PASS.

- [ ] **Step 7: Generate OpenAPI and web types**

Run:

```bash
make openapi-gen
make web-types-gen
make openapi-check
```

Expected: generated files change after `openapi-gen`/`web-types-gen`; `openapi-check` exits 0 with no additional diff.

- [ ] **Step 8: Commit Task 4**

```bash
git add internal/api/handlers/dto.go internal/api/handlers/hermes_cron.go internal/api/handlers/hermes_cron_test.go internal/api/handlers/request_errors.go internal/api/router.go cmd/server/main.go openapi/openapi.yaml web/src/api/generated.ts
git commit -m "feat(cron): 暴露实例定时任务管理接口"
```

Commit body:

```text
新增 /apps/{appId}/hermes/cron 路由组。

接口通过 HermesCronService 代理 oc-cron，支持任务 CRUD、调度器状态、执行历史和输出读取，并同步 OpenAPI 与前端类型。
```

---

### Task 5: Web API Hooks

**Files:**
- Create: `web/src/api/hooks/useCron.ts`
- Test: `web/src/api/hooks/useCron.spec.ts`

- [ ] **Step 1: Create hook tests**

Create `web/src/api/hooks/useCron.spec.ts` to verify URL and cache behavior. Mock `apiRequest` and use a query client wrapper like existing API hook tests.

Core expectations:

```ts
expect(apiRequest).toHaveBeenCalledWith('/api/v1/apps/app-1/hermes/cron/jobs', {
  query: { q: '', status: '' },
})
```

For create mutation:

```ts
await mutation.mutateAsync({ name: '日报', schedule: '0 9 * * *', prompt: '生成摘要' })
expect(apiRequest).toHaveBeenCalledWith('/api/v1/apps/app-1/hermes/cron/jobs', {
  method: 'POST',
  body: { name: '日报', schedule: '0 9 * * *', prompt: '生成摘要' },
})
```

Run:

```bash
cd web && npm test -- useCron.spec.ts --run
```

Expected: FAIL because `useCron.ts` does not exist.

- [ ] **Step 2: Implement hooks**

Create `web/src/api/hooks/useCron.ts` with:

- TypeScript interfaces matching `CronJob`, `CronStatus`, `CronRunEntry`, `CronRunOutput`, `CronCapabilities`.
- Query keys:
  - `cronJobsKey(appId, filters)`
  - `cronStatusKey(appId)`
  - `cronJobKey(appId, jobId)`
  - `cronHistoryKey(appId, jobId)`
  - `cronOutputKey(appId, jobId, fileName)`
- Query hooks from the spec.
- Mutations that invalidate jobs/status/history and set detail cache where possible.

- [ ] **Step 3: Run hook tests**

Run:

```bash
cd web && npm test -- useCron.spec.ts --run
```

Expected: PASS.

- [ ] **Step 4: Commit Task 5**

```bash
git add web/src/api/hooks/useCron.ts web/src/api/hooks/useCron.spec.ts
git commit -m "feat(web): 增加 Cron API hooks"
```

Commit body:

```text
新增实例定时任务的 Vue Query hooks。

hooks 覆盖任务列表、详情、调度器状态、执行历史、输出读取和写操作缓存失效。
```

---

### Task 6: Cron Tab UI

**Files:**
- Create: `web/src/pages/apps/AppCronTab.vue`
- Create: `web/src/pages/apps/cron/CronJobList.vue`
- Create: `web/src/pages/apps/cron/CronJobDetail.vue`
- Create: `web/src/pages/apps/cron/CronJobFormModal.vue`
- Create: `web/src/pages/apps/cron/CronRunHistory.vue`
- Create: `web/src/pages/apps/AppCronTab.spec.ts`
- Create: `web/src/pages/apps/cron/CronJobFormModal.spec.ts`
- Modify: `web/src/pages/apps/AppDetailPage.vue`
- Modify: `web/src/app/router.ts`
- Modify: `web/src/pages/apps/AppDetailPage.spec.ts`

- [ ] **Step 1: Add failing tab tests**

Update `AppDetailPage.spec.ts`:

```ts
expect(wrapper.text()).toContain('定时任务')
```

Create `AppCronTab.spec.ts` with mocks similar to `AppKanbanTab.spec.ts`:

- jobs list renders `日报`;
- clicking list item writes `job` query and right detail receives selected detail;
- `CRON_NOT_SUPPORTED_ON_STUB` shows stub copy;
- status summary renders "Gateway cron running";
- features.write false hides create button.

Run:

```bash
cd web && npm test -- AppDetailPage.spec.ts AppCronTab.spec.ts --run
```

Expected: FAIL because route/tab/component do not exist.

- [ ] **Step 2: Add route and tab entry**

In `web/src/app/router.ts`, import `AppCronTab` and add child route:

```ts
import AppCronTab from '@/pages/apps/AppCronTab.vue'
...
{ path: 'cron', component: AppCronTab, props: true },
```

In `AppDetailPage.vue`, add:

```ts
{ path: 'cron', label: '定时任务' },
```

- [ ] **Step 3: Implement focused components**

Implement `CronJobList.vue`:

- props: `jobs`, `selectedId`;
- emits: `select`;
- table/list rows show `name`, schedule display, state, deliver, next run.

Implement `CronJobDetail.vue`:

- props: `job`, `history`, `output`, `isPlatformAdmin`;
- emits: `action`, `edit`, `select-output`;
- action buttons: run, pause/resume, edit, delete.

Implement `CronJobFormModal.vue`:

- props: `show`, `submitting`, `job`;
- emits: `update:show`, `submit`;
- base fields visible to all roles;
- advanced fields only if `auth.isPlatformAdmin`.

Implement `CronRunHistory.vue`:

- props: `runs`, `selectedFile`;
- emits: `select`;
- show synthetic entries with "无输出文件" copy.

- [ ] **Step 4: Implement `AppCronTab.vue`**

Structure it like `AppKanbanTab.vue`:

- toolbar card with status summary, search, filters, refresh, create;
- stub/runtime unavailable empty card;
- split layout with left list and right detail;
- URL query `job` and `file` synchronization;
- mutations for create/update/action;
- `window.confirm` before delete.

- [ ] **Step 5: Add form modal tests**

Create `CronJobFormModal.spec.ts`:

- org member sees `script/no_agent/workdir` but not `model/provider/base_url/skills`;
- platform admin sees all fields;
- submit trims fields and emits payload;
- non-platform payload does not include advanced fields.

Run:

```bash
cd web && npm test -- AppDetailPage.spec.ts AppCronTab.spec.ts CronJobFormModal.spec.ts --run
```

Expected: PASS.

- [ ] **Step 6: Run typecheck/build-related checks**

Run:

```bash
cd web && npm run typecheck
cd web && npm test -- --run
```

Expected: PASS.

- [ ] **Step 7: Commit Task 6**

```bash
git add web/src/app/router.ts web/src/pages/apps/AppDetailPage.vue web/src/pages/apps/AppDetailPage.spec.ts web/src/pages/apps/AppCronTab.vue web/src/pages/apps/AppCronTab.spec.ts web/src/pages/apps/cron
git commit -m "feat(web): 增加实例定时任务 tab"
```

Commit body:

```text
实例详情页新增定时任务 tab。

页面沿用任务看板的左侧列表、右侧详情布局，支持任务操作、执行历史、输出预览和按角色显隐高级字段。
```

---

### Task 7: Full Verification And Browser QA

**Files:**
- Review: all files changed by Tasks 1-6
- Commit only if verification uncovers small fixes

- [ ] **Step 1: Run backend tests**

Run:

```bash
go test ./internal/auth ./internal/service ./internal/api/handlers ./internal/api ./cmd/server -run 'Cron|Kanban|Router|Server' -v
```

Expected: PASS.

- [ ] **Step 2: Run runtime tests**

Run:

```bash
make build-hermes-runtime HERMES_VARIANT=hermes-main
make verify-hermes-runtime HERMES_VARIANT=hermes-main
```

Expected: image builds and pytest passes. If network blocks upstream Hermes install, document the exact failure and run `cd runtime/hermes/hermes-main && python -m pytest tests/test_cron_contract.py tests/test_kanban_contract.py -v` as the local fallback.

- [ ] **Step 3: Run OpenAPI check**

Run:

```bash
make openapi-check
```

Expected: exits 0 and leaves no generated diff.

- [ ] **Step 4: Run frontend tests**

Run:

```bash
cd web && npm run typecheck
cd web && npm test -- --run
cd web && npm run build
```

Expected: PASS.

- [ ] **Step 5: Start local app**

Check running containers:

```bash
docker compose ps manager-api manager-web oc-runtime-agent new-api manager-postgres manager-redis
```

If any required service is not running, start the local stack:

```bash
docker compose up -d manager-postgres manager-redis new-api-postgres new-api-redis new-api oc-runtime-agent manager-api manager-web
```

Expected: `manager-web` is reachable at `http://localhost:5173` and `manager-api` is reachable through the web app. Browser verification must use the UI at `http://localhost:5173`; curl/API calls do not replace this step.

- [ ] **Step 6: Browser verify platform admin flow**

In a real browser:

1. Login as platform admin with `admin` / `admin123`.
2. Open an app detail page.
3. Click `定时任务`.
4. Confirm status summary appears.
5. Create a job with schedule `*/5 * * * *`, prompt `测试定时任务`, deliver `local`.
6. Select the created job from the left list.
7. Confirm right detail shows prompt, schedule, actions, and history area.
8. Pause, resume, run now, edit name, and delete.
9. Confirm advanced fields are visible in create/edit modal.

- [ ] **Step 7: Browser verify org member field limits**

In a real browser:

1. Login as an org member that owns an instance.
2. Open that app's `定时任务` tab.
3. Open create modal.
4. Confirm `script`, `no_agent`, and `workdir` are visible.
5. Confirm `skills`, `model`, `provider`, and `base_url` are hidden.

- [ ] **Step 8: Final status check**

Run:

```bash
git status --short
```

Expected: only intended source/generated changes are present. Existing unrelated dirty files from other work must not be staged or reverted.

- [ ] **Step 9: Commit verification fixes if needed**

If verification required small fixes, commit them with:

```bash
git add <only-fixed-files>
git commit -m "fix(cron): 修正定时任务管理验证问题"
```

Commit body must list the failing verification and the fix.
