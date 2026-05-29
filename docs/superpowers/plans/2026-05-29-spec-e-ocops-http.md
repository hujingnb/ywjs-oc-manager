# spec-E：oc-* 收敛为 oc-ops HTTP 服务 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 hermes 镜像里的 `oc-*` 运维脚本收敛为 app pod 内一个 oc-ops HTTP 服务（与 hermes 复用同一镜像、覆盖 CMD 起 uvicorn），manager 改用类型化 HTTP + per-app token 调用，彻底取消 docker/k8s exec。

**Architecture:** pod 侧把每个 `oc-*.py` 的核心逻辑下沉为可 import 的 `ocops/` 包模块，Starlette ASGI server 暴露类型化 REST + 两个 SSE 端点（cron/kanban/channel），Bearer `OC_OPS_TOKEN` 鉴权。manager 侧新增 `internal/integrations/ocops` 类型化 HTTP 客户端，cron/kanban/channel service 改依赖 `OcOps` 接口；删除现有 exec 拼 argv + 信封解析通道。本 spec 仅做单元测试，浏览器/集成验证随 spec-A/B/D 合并后统一做（见设计 §6/E4）。

**Tech Stack:** Python 3.13 + Starlette + uvicorn（跑 hermes venv `/usr/local/lib/hermes-agent/venv`）、pytest（Starlette TestClient / httpx）；Go + `net/http` + `httptest`、testify。

**设计依据：** `docs/superpowers/specs/2026-05-29-spec-e-ocops-http-design.md`（父设计 `docs/superpowers/specs/2026-05-29-k8s-migration-design.md` §4.3 D9 / §10）。

---

## 阅读前必读：现有代码与约定

执行本计划前，先通读这些文件，理解既有结构与命名（计划中的重构以它们为基准）：

- pod 侧脚本（重构对象，**逻辑与目录结构允许全量调整**）：
  `runtime/hermes/hermes-v2026.5.16/` 下 `oc-info.py` / `oc-doctor.py` /
  `oc-channel-{status,login,unbind}.py` / `oc-cron.py`（~740 行）/ `oc-kanban.py`（~548 行）；
  共享 `lib/`（`state.py`/`atomic.py`/`logging.py`/`manifest.py`）；
  构建期自检测试在 `tests/`（pytest）。
- 镜像构建：`runtime/hermes/hermes-v2026.5.16/Dockerfile`（venv 在
  `/usr/local/lib/hermes-agent/venv/bin/python`；oc-* COPY 到 `/usr/local/bin/`；
  末尾 `python -m pytest /usr/local/lib/oc-entrypoint/tests/` 构建期自检）；
  `Makefile` 的 `hermes-inject-contract` / `build-hermes-image`。
- manager 侧调用点（HTTP 化对象）：
  `internal/integrations/hermes/commands.go`（info/doctor/channel，`ContainerExecer`）、
  `internal/integrations/hermes/wechat_runner.go`（login 流，`ContainerExecutor.ExecAttach`）、
  `internal/service/hermes_cron.go` + `hermes_cron_types.go`（`cronExecer.ContainerExecJSON`）、
  `internal/service/hermes_kanban.go` + `hermes_kanban_types.go`
  （`kanbanExecer.{ContainerExecJSON,ContainerExecStream}`）、
  `internal/integrations/runtime/adapter.go`（`Adapter` 接口、`ExecJSONResult`/`ExecStreamHandle`）、
  `cmd/server/main.go`（约 196/386-418 行装配 `HermesKanbanService`/`HermesCronService`）。

**契约 / 错误码基线（pod ↔ manager 共用语义，HTTP 化后必须等价）：**

| 内部错误码 | HTTP 状态 | manager 哨兵错误（cron / kanban） |
|---|---|---|
| `BAD_REQUEST` | 400 | `ErrCronBadRequest` / `ErrKanbanBadRequest` |
| `NOT_FOUND` | 404 | `ErrNotFound` |
| `UNSUPPORTED` | 409 | `ErrCronNotSupported` / `ErrKanbanNotSupported` |
| `INTERNAL` | 500 | `ErrCronOutputInvalid` / `ErrKanbanOutputInvalid`（输出非法）|
| `HERMES_CLI_FAILED`（及未知码）| 502 | `ErrCronCLI` / `ErrKanbanCLI` |
| 鉴权失败 | 401 | `ErrOcOpsUnauthorized`（新增）|

成功响应：HTTP 200（DELETE 用 204），body 直接是类型化对象（**不再包 `{ok,data}` 信封**）；
失败响应：上表状态码 + body `{"code":"...","message":"..."}`。

**全局约定（务必遵守，见 `CLAUDE.md` / `AGENTS.md`）：**

- 直接在 `master` 分支提交；不切 worktree。
- Conventional Commits，summary 用中文且具体；正文补背景/实现/影响；
  commit trailer 固定一行：`Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`。
- 每个 commit 只 `git add` 本任务涉及的具体文件，**不要 `git add -A`**，**不要提交未跟踪的 `docs/reports/`**。
- 每个测试方法/子测试/表驱动用例都要相邻中文注释说明覆盖的场景/边界。
- 新增/重构代码补充中文注释（业务意图、边界、非显然约束）。
- Go 测试断言用 testify `require`/`assert`（expected 在前）。
- 本 spec **不做**浏览器验证、不构建运行 oc-ops 容器做集成验证（E4）；
  每个任务的「验证」指单元测试与 `make` 静态检查。

---

## 文件结构总览

**pod 侧（`runtime/hermes/hermes-v2026.5.16/`）新增：**

```
ocops/
  __init__.py        # 空包标记
  errors.py          # OpsError（携带 code），CODE_TO_HTTP 映射
  info.py            # collect_info() -> dict（从 oc-info.py 抽取）
  doctor.py          # collect_doctor() -> dict（从 oc-doctor.py 抽取）
  channel.py         # channel_status / channel_unbind（同步）+ channel_login（async generator）
  cron.py            # oc-cron 全部逻辑搬入；cmd_* → 返回 data / raise CronError
  kanban.py          # oc-kanban 全部逻辑搬入；verb_* → 返回 data / raise KanbanError；watch → generator
  auth.py            # Bearer OC_OPS_TOKEN 常量时间校验
  server.py          # Starlette app：路由 + 鉴权中间件 + 错误→HTTP + SSE
tests/                # 复用现有 pytest 目录，新增 test_ocops_*.py
```

各 `oc-*.py` CLI 保留为薄 shim：`import ocops.X` 后调用核心函数、用原 `emit_ok/emit_err` 输出（不改对外命令契约，构建期自检与本地调试不受影响）。

**manager 侧（Go）新增 / 改动：**

```
internal/integrations/ocops/
  types.go           # 从 service 包迁入的契约 DTO（CronJob/KanbanTaskDetail/... 等）
  errors.go          # ocops 哨兵错误 + HTTP 状态码 → 哨兵错误映射
  client.go          # Client：http.Client 封装，doJSON / doSSE
  client_cron.go     # cron 11 个方法
  client_kanban.go   # kanban 方法（含 WatchEvents SSE）
  client_channel.go  # info/doctor/channel-status/unbind/login(SSE)
internal/service/
  ocops.go           # OcOps 接口（消费方定义）+ OcOpsResolver + OcOpsAppLocation + 从 store 的最小 resolver
  hermes_cron.go     # 改依赖 OcOps（删 cronExecer/argv/信封）
  hermes_kanban.go   # 改依赖 OcOps（删 kanbanExecer/argv/信封）
internal/integrations/hermes/
  commands.go        # info/doctor/channel → OcOps（删 ContainerExecer）
  wechat_runner.go   # login 改消费 OcOps 的 SSE 事件 channel（删 ExecAttach 依赖）
cmd/server/main.go   # 装配 ocops.Client + resolver，注入 service；摘除 oc-* 的 exec 装配
```

> **DTO 迁移的循环依赖约束**：契约 DTO 现在定义在 `service` 包。若 `ocops` 客户端返回这些
> DTO 且 `service` 又 import `ocops`，会形成循环。解决：把 DTO 迁到 `ocops` 包（契约属主），
> `service`/handlers 改引用 `ocops.CronJob` 等；`OcOps` 接口由消费方 `service` 定义、引用
> `ocops` 类型。Task 13 专门做这次迁移。

---

## Phase 0 — Python ocops 包脚手架

### Task 1：ocops 包与错误模型

**Files:**
- Create: `runtime/hermes/hermes-v2026.5.16/ocops/__init__.py`
- Create: `runtime/hermes/hermes-v2026.5.16/ocops/errors.py`
- Test: `runtime/hermes/hermes-v2026.5.16/tests/test_ocops_errors.py`

- [ ] **Step 1: 写失败测试**

```python
# tests/test_ocops_errors.py
"""覆盖 ocops 统一错误模型：错误码 → HTTP 状态码映射，及默认兜底。"""
from ocops.errors import OpsError, code_to_http


def test_code_to_http_known_codes():
    # 覆盖契约里全部已知错误码 → HTTP 状态码的精确映射
    assert code_to_http("BAD_REQUEST") == 400
    assert code_to_http("NOT_FOUND") == 404
    assert code_to_http("UNSUPPORTED") == 409
    assert code_to_http("INTERNAL") == 500
    assert code_to_http("HERMES_CLI_FAILED") == 502


def test_code_to_http_unknown_defaults_502():
    # 未知错误码兜底为 502（与 manager 端 default→ErrCronCLI 语义一致）
    assert code_to_http("SOMETHING_ELSE") == 502


def test_opserror_carries_code_and_message():
    # OpsError 须同时携带契约 code 与人读 message
    err = OpsError("NOT_FOUND", "任务不存在")
    assert err.code == "NOT_FOUND"
    assert str(err) == "任务不存在"
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd runtime/hermes/hermes-v2026.5.16 && python -m pytest tests/test_ocops_errors.py -v`
Expected: FAIL（`ModuleNotFoundError: No module named 'ocops'`）

- [ ] **Step 3: 实现**

```python
# ocops/__init__.py
"""oc-ops：把 hermes 镜像内 oc-* 运维脚本逻辑下沉为可 import 的核心模块，
供 server.py 的 HTTP handler 与各 oc-* CLI shim 共用。"""
```

```python
# ocops/errors.py
"""ocops 统一错误模型。

各核心模块（cron/kanban/channel）业务失败统一抛 OpsError 的子类或 OpsError 本身，
携带契约错误码；server 层据 code_to_http 映射成 HTTP 状态码 + {code,message} body。
CLI shim 则据 code 输出 {ok:false,error} 信封，保持对外命令契约不变。"""

from __future__ import annotations

# 契约错误码 → HTTP 状态码。未列出的码兜底 502（与 manager default→CLI 失败一致）。
_CODE_TO_HTTP = {
    "BAD_REQUEST": 400,
    "NOT_FOUND": 404,
    "UNSUPPORTED": 409,
    "INTERNAL": 500,
    "HERMES_CLI_FAILED": 502,
}


def code_to_http(code: str) -> int:
    """把契约错误码映射成 HTTP 状态码；未知码按 502（上游/CLI 失败）处理。"""
    return _CODE_TO_HTTP.get(code, 502)


class OpsError(Exception):
    """携带契约错误码的业务异常基类。"""

    def __init__(self, code: str, message: str):
        super().__init__(message)
        self.code = code
        self.message = message
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd runtime/hermes/hermes-v2026.5.16 && python -m pytest tests/test_ocops_errors.py -v`
Expected: PASS（3 passed）

- [ ] **Step 5: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/ocops/__init__.py \
        runtime/hermes/hermes-v2026.5.16/ocops/errors.py \
        runtime/hermes/hermes-v2026.5.16/tests/test_ocops_errors.py
git commit -m "feat(ocops): 新增 ocops 包与统一错误模型

spec-E pod 侧基础：OpsError 携带契约错误码，code_to_http 把
BAD_REQUEST/NOT_FOUND/UNSUPPORTED/INTERNAL/HERMES_CLI_FAILED 映射成
400/404/409/500/502，未知码兜底 502，与 manager 端错误码语义对齐。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Phase 1 — Python：纯文件读模块（info / doctor / channel status·unbind）

### Task 2：ocops.info 与 ocops.doctor

**Files:**
- Create: `runtime/hermes/hermes-v2026.5.16/ocops/info.py`
- Create: `runtime/hermes/hermes-v2026.5.16/ocops/doctor.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/oc-info.py`（改为调用 `ocops.info`）
- Modify: `runtime/hermes/hermes-v2026.5.16/oc-doctor.py`（改为调用 `ocops.doctor`）
- Test: `runtime/hermes/hermes-v2026.5.16/tests/test_ocops_info_doctor.py`

- [ ] **Step 1: 写失败测试**

```python
# tests/test_ocops_info_doctor.py
"""覆盖 info/doctor 核心函数：从镜像身份文件与 state 读出结构化快照。"""
import json
from pathlib import Path

from ocops import info, doctor


def test_collect_info_reads_image_file(tmp_path, monkeypatch):
    # 正常路径：读 OC_INFO_FILE 指向的镜像身份 JSON，并补 oc_entrypoint_version
    f = tmp_path / "oc-image.json"
    f.write_text(json.dumps({"variant": "hermes-v2026.5.16",
                             "hermes_upstream_ref": "abc", "built_at": "2026-05-29"}))
    monkeypatch.setenv("OC_INFO_FILE", str(f))
    got = info.collect_info()
    assert got["variant"] == "hermes-v2026.5.16"
    assert got["oc_entrypoint_version"] == "1"


def test_collect_info_missing_file_raises(tmp_path, monkeypatch):
    # 边界：身份文件缺失/损坏时抛 OpsError(INTERNAL)，由 server 映射 500
    monkeypatch.setenv("OC_INFO_FILE", str(tmp_path / "missing.json"))
    from ocops.errors import OpsError
    try:
        info.collect_info()
        assert False, "应抛 OpsError"
    except OpsError as e:
        assert e.code == "INTERNAL"


def test_collect_doctor_reports_state_and_status(tmp_path, monkeypatch):
    # 正常路径：doctor 读 state 快照，hermes 进程不在时 hermes_status=stopped、issues 为空
    monkeypatch.setenv("OC_DATA_DIR", str(tmp_path))
    monkeypatch.setenv("OC_IMAGE_VARIANT", "hermes-v2026.5.16")
    got = doctor.collect_doctor()
    assert got["variant"] == "hermes-v2026.5.16"
    assert got["hermes_status"] in ("running", "stopped", "unknown")
    assert got["issues"] == []
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd runtime/hermes/hermes-v2026.5.16 && python -m pytest tests/test_ocops_info_doctor.py -v`
Expected: FAIL（`ImportError: cannot import name 'info'`）

- [ ] **Step 3: 实现 ocops.info / ocops.doctor**

把 `oc-info.py` `main()` 的读取逻辑抽成纯函数（抛 `OpsError` 取代写 stderr + return 1）：

```python
# ocops/info.py
"""镜像身份信息：读取构建期写入的 /etc/oc-image.json。从 oc-info.py 抽取核心逻辑。"""
from __future__ import annotations

import json
import os
from pathlib import Path

from ocops.errors import OpsError


def collect_info() -> dict:
    """读取镜像身份 JSON 并补 oc_entrypoint_version；文件缺失/损坏抛 OpsError(INTERNAL)。"""
    info_path = Path(os.environ.get("OC_INFO_FILE", "/etc/oc-image.json"))
    try:
        raw = json.loads(info_path.read_text())
    except (OSError, json.JSONDecodeError) as e:
        raise OpsError("INTERNAL", f"读取镜像身份失败: {e}") from e
    raw["oc_entrypoint_version"] = "1"
    return raw
```

```python
# ocops/doctor.py
"""运行时诊断快照：state 渲染信息 + hermes 进程状态。从 oc-doctor.py 抽取核心逻辑。"""
from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

# 复用 oc-entrypoint 的 lib.state（镜像内装在 /usr/local/lib/oc-entrypoint）。
sys.path.insert(0, "/usr/local/lib/oc-entrypoint")
if not Path("/usr/local/lib/oc-entrypoint").exists():
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from lib.state import read_state  # noqa: E402


def collect_doctor() -> dict:
    """读 state 快照并探测 hermes gateway 进程；返回诊断 dict（永不抛业务错误）。"""
    data_root = Path(os.environ.get("OC_DATA_DIR", "/opt/data"))
    variant = os.environ.get("OC_IMAGE_VARIANT", "unknown")
    state = read_state(data_root)

    hermes_pid = None
    hermes_status = "unknown"
    try:
        out = subprocess.run(["pgrep", "-f", "hermes gateway"],
                             capture_output=True, text=True, timeout=5)
        if out.returncode == 0 and out.stdout.strip():
            hermes_pid = int(out.stdout.splitlines()[0])
            hermes_status = "running"
        else:
            hermes_status = "stopped"
    except (FileNotFoundError, subprocess.TimeoutExpired):
        hermes_status = "unknown"

    return {
        "variant": variant,
        "last_render_at": state.last_render_at,
        "manifest_sha256": state.manifest_sha256,
        "hermes_pid": hermes_pid,
        "hermes_status": hermes_status,
        "issues": [],
    }
```

- [ ] **Step 4: 改 oc-info.py / oc-doctor.py 为薄 shim**

`oc-info.py` `main()` 改为：

```python
def main() -> int:
    # CLI shim：复用 ocops.info 核心逻辑，保留 stdout 单行 JSON 的对外命令契约。
    import json, sys
    sys.path.insert(0, "/usr/local/lib")  # 镜像内 ocops 装在 /usr/local/lib/ocops
    sys.path.insert(0, str(Path(__file__).resolve().parent))  # 本地自检 fallback
    from ocops.info import collect_info
    from ocops.errors import OpsError
    try:
        sys.stdout.write(json.dumps(collect_info(), ensure_ascii=False) + "\n")
        return 0
    except OpsError as e:
        sys.stderr.write(json.dumps({"phase": "oc-info", "level": "error", "msg": e.message}) + "\n")
        return 1
```

`oc-doctor.py` `main()` 改为调用 `ocops.doctor.collect_doctor()` 后 `json.dumps` 输出（同样的 shim 形态）。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd runtime/hermes/hermes-v2026.5.16 && PYTHONPATH=. python -m pytest tests/test_ocops_info_doctor.py -v`
Expected: PASS（3 passed）

- [ ] **Step 6: 跑全量构建期自检确认未回归**

Run: `cd runtime/hermes/hermes-v2026.5.16 && PYTHONPATH=. python -m pytest tests/ -q`
Expected: PASS（含原有 test_state/test_manifest 等）

- [ ] **Step 7: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/ocops/info.py \
        runtime/hermes/hermes-v2026.5.16/ocops/doctor.py \
        runtime/hermes/hermes-v2026.5.16/oc-info.py \
        runtime/hermes/hermes-v2026.5.16/oc-doctor.py \
        runtime/hermes/hermes-v2026.5.16/tests/test_ocops_info_doctor.py
git commit -m "refactor(ocops): info/doctor 逻辑下沉为可 import 核心函数

把 oc-info/oc-doctor 的读取逻辑抽成 ocops.info.collect_info /
ocops.doctor.collect_doctor，失败改抛 OpsError；oc-info.py/oc-doctor.py
退化为调用核心函数的薄 CLI shim，保留 stdout 单行 JSON 对外契约。
为后续 oc-ops HTTP server 复用同一逻辑做准备。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 3：ocops.channel 的 status / unbind（纯文件）

**Files:**
- Create: `runtime/hermes/hermes-v2026.5.16/ocops/channel.py`
- Modify: `oc-channel-status.py` / `oc-channel-unbind.py`（改调 `ocops.channel`）
- Test: `runtime/hermes/hermes-v2026.5.16/tests/test_ocops_channel_status.py`

- [ ] **Step 1: 写失败测试**

```python
# tests/test_ocops_channel_status.py
"""覆盖渠道 status/unbind 核心函数：基于 /opt/data/weixin/accounts 目录判定绑定态。"""
from ocops import channel
from ocops.errors import OpsError


def test_status_unbound_when_dir_absent(tmp_path):
    # 边界：账号目录不存在 → 未绑定
    got = channel.channel_status("weixin", tmp_path)
    assert got == {"channel": "weixin", "bound": False}


def test_status_bound_returns_account_id(tmp_path):
    # 正常路径：存在 <id>.json 账号文件 → 已绑定且回传 account_id
    accounts = tmp_path / "weixin" / "accounts"
    accounts.mkdir(parents=True)
    (accounts / "abc@im.bot.json").write_text("{}")
    got = channel.channel_status("weixin", tmp_path)
    assert got == {"channel": "weixin", "bound": True, "account_id": "abc@im.bot"}


def test_status_unknown_channel_raises(tmp_path):
    # 异常路径：未知 channel → OpsError(BAD_REQUEST)
    try:
        channel.channel_status("telegram", tmp_path)
        assert False
    except OpsError as e:
        assert e.code == "BAD_REQUEST"


def test_unbind_idempotent(tmp_path):
    # 幂等：目录不存在也返回 unbound
    assert channel.channel_unbind("weixin", tmp_path) == {"status": "unbound"}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd runtime/hermes/hermes-v2026.5.16 && PYTHONPATH=. python -m pytest tests/test_ocops_channel_status.py -v`
Expected: FAIL（`ImportError`）

- [ ] **Step 3: 实现 ocops.channel 的同步部分**

```python
# ocops/channel.py（先实现 status/unbind 同步部分；login 在 Task 6 追加）
"""渠道绑定运维：weixin 账号目录读写。从 oc-channel-status/unbind 抽取核心逻辑。

设计约束：manager 不解析凭证字段；status 仅判定是否存在账号文件，unbind 直接删目录
（幂等），凭证由 hermes 自管。未知 channel 一律 BAD_REQUEST（HTTP 400）。"""
from __future__ import annotations

import shutil
from pathlib import Path

from ocops.errors import OpsError


def channel_status(channel: str, data_root: Path) -> dict:
    """查询渠道绑定态；当前仅支持 weixin，未知 channel 抛 BAD_REQUEST。"""
    if channel != "weixin":
        raise OpsError("BAD_REQUEST", f"unknown channel: {channel}")
    accounts_dir = data_root / "weixin" / "accounts"
    if not accounts_dir.exists():
        return {"channel": "weixin", "bound": False}
    for entry in sorted(accounts_dir.iterdir()):
        if entry.is_file() and entry.suffix == ".json":
            return {"channel": "weixin", "bound": True,
                    "account_id": entry.name[: -len(".json")]}
    return {"channel": "weixin", "bound": False}


def channel_unbind(channel: str, data_root: Path) -> dict:
    """解绑渠道：删除账号目录（幂等）；未知 channel 抛 BAD_REQUEST。"""
    if channel != "weixin":
        raise OpsError("BAD_REQUEST", "unknown channel")
    accounts_dir = data_root / "weixin" / "accounts"
    if accounts_dir.exists():
        shutil.rmtree(accounts_dir)
    return {"status": "unbound"}
```

- [ ] **Step 4: 改 oc-channel-status.py / oc-channel-unbind.py 为薄 shim**

两脚本 `main()` 解析 `--channel` 后调用对应核心函数，`OpsError` → 原有 `{"status":"failed"...}` / 退出码 1 语义；成功 `json.dumps` 输出。data_root 仍取 `OC_DATA_DIR`。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd runtime/hermes/hermes-v2026.5.16 && PYTHONPATH=. python -m pytest tests/test_ocops_channel_status.py -v`
Expected: PASS（4 passed）

- [ ] **Step 6: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/ocops/channel.py \
        runtime/hermes/hermes-v2026.5.16/oc-channel-status.py \
        runtime/hermes/hermes-v2026.5.16/oc-channel-unbind.py \
        runtime/hermes/hermes-v2026.5.16/tests/test_ocops_channel_status.py
git commit -m "refactor(ocops): 渠道 status/unbind 下沉为 ocops.channel 核心函数

channel_status/channel_unbind 接收 data_root 参数、返回结构化 dict、
未知 channel 抛 OpsError(BAD_REQUEST)；oc-channel-status/unbind 退化为
薄 CLI shim，保留对外命令契约。login(async) 在后续任务追加。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Phase 2 — Python：ocops.cron（oc-cron 逻辑下沉）

### Task 4：把 oc-cron.py 重构为 ocops.cron

**Files:**
- Create: `runtime/hermes/hermes-v2026.5.16/ocops/cron.py`（承接 `oc-cron.py` 几乎全部逻辑）
- Modify: `runtime/hermes/hermes-v2026.5.16/oc-cron.py`（退化为薄 CLI shim）
- Test: `runtime/hermes/hermes-v2026.5.16/tests/test_cron_contract.py`（迁移 / 复用现有断言）

**重构规则（机械、保行为）：**

1. 把 `oc-cron.py` 第 21 行起到 `VERB_HANDLERS` 之前的**全部模块级代码**（常量、`CronError`、
   校验函数、`read_jobs`/`write_jobs`、`normalize_*`、`_run_hermes_cron`、`cmd_*` 等）原样搬入
   `ocops/cron.py`。
2. `CronError` 改为继承 `ocops.errors.OpsError`（`OpsError.__init__` 已是 `(code,message)`，
   语义一致）：`from ocops.errors import OpsError` 后 `class CronError(OpsError): pass`。
   `emit_err` 用的 code 字段不变。
3. 每个 `cmd_*(args)` 拆成两部分：
   - 纯函数 `run_<verb>(...) -> data`：接收**类型化形参**（不再依赖 argparse `args`），
     执行原逻辑，**`return data`**（原 `return emit_ok(data)` → `return data`），失败照旧
     `raise CronError(...)`。
   - 形参对齐现 argparse：如 `run_create(name, schedule, prompt=None, deliver=None,
     repeat=None, script=None, no_agent=False, workdir=None, skills=(), model=None,
     provider=None, base_url=None) -> dict`；`run_list(all_: bool) -> list`；
     `run_show(job_id) -> dict`；`run_toggle(job_id, enabled: bool) -> dict` 等。
     内部把这些形参组装成原 `_create_args` 期望的对象即可（可保留一个轻量
     `_Args` namedtuple/SimpleNamespace 适配，避免重写 argv 拼装逻辑）。
4. `emit_ok`/`emit_err` **不进** `ocops.cron`（属 CLI 输出层），留在 `oc-cron.py` shim。

**worked example（capabilities 与 create 两个代表）：**

```python
# ocops/cron.py（节选——其余 run_* 同此转换规则）
from ocops.errors import OpsError


class CronError(OpsError):
    """oc-cron 业务异常，继承 OpsError 以复用 code→HTTP 映射。"""


def run_capabilities() -> dict:
    """自描述能力；不依赖 hermes。等价原 cmd_capabilities 的 data。"""
    info = _read_image_info()
    return {
        "contract_version": CONTRACT_VERSION,
        "oc_cron_version": OC_CRON_VERSION,
        "hermes_version": (info.get("hermes_upstream_ref") or info.get("hermes_ref")
                           or info.get("hermes_version")),
        "variant": info.get("variant") or info.get("oc_image_variant") or "hermes-v2026.5.16",
        "verbs": FUNCTIONAL_VERBS,
        "features": {"status": True, "history": True, "output": True,
                     "write": True, "script": True, "advanced_fields": True},
    }


def run_create(name, schedule, prompt=None, deliver=None, repeat=None, script=None,
               no_agent=False, workdir=None, skills=(), model=None, provider=None,
               base_url=None) -> dict:
    """创建 Cron 任务并重读返回稳定对象。等价原 cmd_create，但返回 data 而非 emit_ok。"""
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
```

其中 `_CronArgs` 是 `ocops/cron.py` 内的 `SimpleNamespace` 包装（提供 `getattr(args, field)`
所需属性，默认值与 argparse 一致），让 `_create_args`/`_append_common_write_flags`
等既有内部函数零改动复用。

需转换的 `cmd_* → run_*` 全集（11 个）：`capabilities, status, list, show, history,
output, create, update, delete, toggle, run`。签名按现 argparse 对应展开。

- [ ] **Step 1: 写/迁移失败测试**

把现有 `tests/test_cron_contract.py` 里通过 `oc-cron.py main([...])` 解析 stdout 信封的断言，
改为直接调用 `ocops.cron.run_*` 并断言返回 dict / 抛 `CronError`。新增至少覆盖：
capabilities 字段、list 过滤 disabled、show NOT_FOUND、create 后重读、toggle true/false、
output 路径逃逸 BAD_REQUEST。每条用例相邻中文注释说明场景。示例（一条）：

```python
def test_run_show_not_found(tmp_path, monkeypatch):
    # 异常路径：show 不存在的任务 → CronError(NOT_FOUND)
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    from ocops.cron import run_show, CronError
    try:
        run_show("nope")
        assert False
    except CronError as e:
        assert e.code == "NOT_FOUND"
```

- [ ] **Step 2: 运行确认失败**

Run: `cd runtime/hermes/hermes-v2026.5.16 && PYTHONPATH=. python -m pytest tests/test_cron_contract.py -v`
Expected: FAIL（`ImportError` / 函数不存在）

- [ ] **Step 3: 实现 ocops/cron.py（按上方规则搬迁 + 转换）**

- [ ] **Step 4: 把 oc-cron.py 改为薄 shim**

```python
# oc-cron.py（shim）：保留 argparse 与 emit_ok/emit_err，仅把 verb 分发到 ocops.cron.run_*
import sys, json
sys.path.insert(0, "/usr/local/lib")
sys.path.insert(0, str(Path(__file__).resolve().parent))
from ocops import cron as _cron
from ocops.errors import OpsError

def main(argv=None) -> int:
    p = _build_parser()                      # 沿用原 build_parser
    args = p.parse_args(argv)
    try:
        data = _dispatch(args)               # 把 args 映射到 _cron.run_<verb>(...)
        sys.stdout.write(json.dumps({"ok": True, "data": data}, ensure_ascii=False) + "\n")
        return 0
    except OpsError as e:
        sys.stdout.write(json.dumps({"ok": False, "error": {"code": e.code, "message": e.message}},
                                    ensure_ascii=False) + "\n")
        return 1
```

> shim 的 `_build_parser` 与 `_dispatch` 可保留在 `oc-cron.py`；`_dispatch` 是
> `args.verb` → `_cron.run_*(...)` 的薄映射。argparse 用法错误经
> `CronArgumentParser.error` 抛 `CronError("BAD_REQUEST", ...)`，仍输出失败信封。

- [ ] **Step 5: 运行测试 + 全量自检确认通过**

Run: `cd runtime/hermes/hermes-v2026.5.16 && PYTHONPATH=. python -m pytest tests/ -q`
Expected: PASS（含迁移后的 cron 契约测试与其余自检）

- [ ] **Step 6: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/ocops/cron.py \
        runtime/hermes/hermes-v2026.5.16/oc-cron.py \
        runtime/hermes/hermes-v2026.5.16/tests/test_cron_contract.py
git commit -m "refactor(ocops): oc-cron 逻辑下沉为 ocops.cron 可 import 函数

把 oc-cron 的校验/jobs.json 读写/normalize/hermes CLI 调用与 11 个 verb
全部搬入 ocops.cron，cmd_* 改为接收类型化形参、返回 data 或抛 CronError
（继承 OpsError）；oc-cron.py 退化为 argparse + emit 信封的薄 shim。
行为等价，cron 契约测试改为直接断言核心函数。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Phase 3 — Python：ocops.kanban（oc-kanban 逻辑下沉，含 watch generator）

### Task 5：把 oc-kanban.py 重构为 ocops.kanban

**Files:**
- Create: `runtime/hermes/hermes-v2026.5.16/ocops/kanban.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/oc-kanban.py`（薄 shim）
- Test: `runtime/hermes/hermes-v2026.5.16/tests/test_kanban_contract.py`（迁移）

**重构规则：** 同 Task 4。要点：

1. 搬入常量、`KanbanError(OpsError)`、`run_hermes`/`hermes_json`/`hermes_ok`/`has_real_hermes`、
   全部 `normalize_*`、`parse_watch_line`、`verb_*`。
2. `verb_*(args)` → `run_<verb>(...) -> data`，形参对齐现 argparse（`run_list(board,
   status=None, assignee=None)`、`run_create(board, title, assignee, priority=0, body=None,
   skills=(), workspace=None, parent=None, max_retries=None)`、`run_show(board, id)` 等）。
3. **`verb_watch` → `watch_events(board) -> Iterator[dict]`**：把 `subprocess.Popen` 逐行
   `parse_watch_line` 的循环改成 **generator `yield ev`**（不再 `sys.stdout.write`）；
   启动失败（`returncode not in (0,None) and 未产出任何事件`）抛 `KanbanError`。
   server 的 SSE 端点消费此 generator（Task 11）。
4. `has_real_hermes()` 的 UNSUPPORTED 守卫逻辑移交调用方：核心函数不自带守卫，
   由 server / shim 在分发前检查（保持「stub 镜像→ UNSUPPORTED」语义）。

**worked example（list 与 watch）：**

```python
# ocops/kanban.py（节选）
from ocops.errors import OpsError


class KanbanError(OpsError):
    """oc-kanban 业务异常，继承 OpsError。"""


def run_list(board: str, status: str | None = None, assignee: str | None = None) -> list:
    """列出 board 任务（可按 status/assignee 过滤）。等价原 verb_list 的 data。"""
    cmd = ["--board", board, "list", "--json"]
    if status:
        cmd += ["--status", status]
    if assignee:
        cmd += ["--assignee", assignee]
    return [normalize_task(t) for t in (hermes_json(cmd) or [])]


def watch_events(board: str):
    """订阅 board 事件流：逐条 yield 契约 Event dict（取代原 verb_watch 的 NDJSON 写）。

    启动即失败且无任何事件产出时抛 KanbanError，让 server 在 SSE 建流前返回错误状态码。"""
    proc = subprocess.Popen(["hermes", "kanban", "--board", board, "watch"],
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    emitted = 0
    try:
        for line in proc.stdout:
            ev = parse_watch_line(line)
            if ev is None:
                continue
            emitted += 1
            yield ev
        proc.wait()
        stderr_text = proc.stderr.read() or ""
    finally:
        if proc.poll() is None:
            proc.terminate()
    if proc.returncode not in (0, None) and emitted == 0:
        raise KanbanError(classify_hermes_error(stderr_text),
                          (stderr_text or "hermes kanban watch 启动失败").strip()[:1024])
```

需转换的 `verb_* → run_*`（含 watch generator）全集：`capabilities, boards, list, show,
runs, stats, watch(→watch_events), create, comment, complete, block, unblock, archive,
reassign, reclaim`。

- [ ] **Step 1: 迁移失败测试**

把 `tests/test_kanban_contract.py` 中经 `main([...])` 解析信封的断言改为直接调用
`ocops.kanban.run_*`，并新增 `watch_events` 用 fake `hermes` 脚本（`tmp_path` 造一个
打印两行事件文本的可执行 `hermes`，`monkeypatch` PATH）断言 yield 两个 Event。每条用例中文注释。

- [ ] **Step 2: 运行确认失败** → FAIL（ImportError）
- [ ] **Step 3: 实现 ocops/kanban.py**
- [ ] **Step 4: oc-kanban.py 薄 shim**（argparse + `has_real_hermes` 守卫 + emit 信封；watch 分支消费 `watch_events` 逐行 NDJSON 写 stdout，保留对外命令契约）
- [ ] **Step 5: 全量自检**

Run: `cd runtime/hermes/hermes-v2026.5.16 && PYTHONPATH=. python -m pytest tests/ -q`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/ocops/kanban.py \
        runtime/hermes/hermes-v2026.5.16/oc-kanban.py \
        runtime/hermes/hermes-v2026.5.16/tests/test_kanban_contract.py
git commit -m "refactor(ocops): oc-kanban 逻辑下沉为 ocops.kanban 可 import 函数

verb_* 改为返回 data 的 run_*，watch 改为 watch_events generator（逐条
yield 契约 Event，启动失败抛 KanbanError）；oc-kanban.py 退化为薄 shim，
watch 分支消费 generator 写 NDJSON，保留对外命令契约。UNSUPPORTED 守卫
移交调用方。kanban 契约测试改为直接断言核心函数。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Phase 4 — Python：ocops.channel 的 login（async generator）

### Task 6：channel_login async 事件流

**Files:**
- Modify: `runtime/hermes/hermes-v2026.5.16/ocops/channel.py`（追加 `channel_login`）
- Modify: `runtime/hermes/hermes-v2026.5.16/oc-channel-login.py`（薄 shim，保留 venv re-exec）
- Test: `runtime/hermes/hermes-v2026.5.16/tests/test_ocops_channel_login.py`

**要点：** `oc-channel-login.py` 的 venv re-exec（第 32-34 行）保留在 **CLI shim**（HTTP server
本就跑在 venv 内，server 进程无需 re-exec）。核心函数 `channel_login` 是 **async generator**，
逐个 yield 事件 dict：`{"event":"qrcode","url":...}` → `{"event":"bound"}` /
`{"event":"failed","reason":...}` / `{"event":"timeout"}`。

```python
# ocops/channel.py（追加）
async def channel_login(channel: str):
    """微信扫码登录的 async 事件流：先 yield qrcode 事件，最后 yield bound/timeout/failed。

    复用 hermes 上游 qr_login（venv 内可 import）。qr_login 内部 print 二维码 URL，
    这里用 redirect 捕获并作为 qrcode 事件 yield；返回 cred→bound，None→timeout。
    未知 channel / SDK 不可用 → failed 事件（不抛异常，保证 SSE 流可优雅结束）。"""
    if channel != "weixin":
        yield {"event": "failed", "reason": f"unknown channel: {channel}"}
        return
    try:
        from gateway.platforms.weixin import qr_login
    except ImportError as e:
        yield {"event": "failed", "reason": f"hermes SDK not available: {e}"}
        return
    # qr_login 内部 print 二维码 URL 到 stdout；用一个可写对象捕获并转成 qrcode 事件。
    # 实现细节：用 contextlib.redirect_stdout 到自定义 StringIO 子类，在 write 时
    # 解析以 https://liteapp.weixin.qq.com/ 开头的行 → 经 asyncio.Queue 投递；
    # qr_login 协程与「从队列取事件 yield」协程用 asyncio.gather 协作。
    ...
    # 成功：yield {"event":"bound"}；返回 None：yield {"event":"timeout"}
```

> 实现提示：因为 `qr_login` 在登录全程才返回、而二维码 URL 在中途 print，需要并发：
> 用 `asyncio.Queue` + 一个把 redirect 捕获行推入队列的 writer，主循环 `await queue.get()`
> 逐条 yield，直到 `qr_login` 任务完成后 yield 终态。manager 端 QR URL 识别沿用现
> `wechat_runner.go` 的前缀 `https://liteapp.weixin.qq.com/`。

- [ ] **Step 1: 写失败测试**

```python
# tests/test_ocops_channel_login.py
"""覆盖 channel_login async 事件流：mock qr_login，断言 qrcode→bound / timeout / failed。"""
import asyncio
import pytest


def test_login_unknown_channel_yields_failed():
    # 异常路径：未知 channel 直接 yield failed 事件并结束
    from ocops.channel import channel_login

    async def collect():
        return [e async for e in channel_login("telegram")]

    events = asyncio.run(collect())
    assert events == [{"event": "failed", "reason": "unknown channel: telegram"}]


def test_login_sdk_unavailable_yields_failed(monkeypatch):
    # 边界：venv 无 hermes SDK（import 失败）→ failed 事件，reason 含原因
    from ocops import channel

    async def collect():
        return [e async for e in channel.channel_login("weixin")]

    events = asyncio.run(collect())
    assert events[-1]["event"] == "failed"
    assert "SDK not available" in events[-1]["reason"]
```

> bound/qrcode 路径用 monkeypatch 注入一个假的 `gateway.platforms.weixin.qr_login`
> （`sys.modules` 造桩，print 一行 `https://liteapp.weixin.qq.com/xxx` 后返回 `{"ok":1}`），
> 断言事件序列为 `qrcode` 然后 `bound`。补该用例并加中文注释。

- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现 channel_login（asyncio.Queue 并发方案）**
- [ ] **Step 4: oc-channel-login.py 改薄 shim**（保留 venv re-exec；`asyncio.run` 消费 generator：qrcode 事件 → stderr 行（沿用现协议），终态 → stdout 单行 JSON `{"status":...}`）
- [ ] **Step 5: 运行测试 + 全量自检** → PASS
- [ ] **Step 6: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/ocops/channel.py \
        runtime/hermes/hermes-v2026.5.16/oc-channel-login.py \
        runtime/hermes/hermes-v2026.5.16/tests/test_ocops_channel_login.py
git commit -m "refactor(ocops): 渠道 login 下沉为 channel_login async 事件流

channel_login 作为 async generator 逐条 yield qrcode/bound/timeout/failed
事件，复用 hermes 上游 qr_login；oc-channel-login.py 保留 venv re-exec、
退化为消费 generator 的薄 shim（qrcode→stderr、终态→stdout），对外协议不变。
供 oc-ops HTTP server 的 login SSE 端点复用。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Phase 5 — Python：Starlette oc-ops HTTP server

### Task 7：Bearer 鉴权

**Files:**
- Create: `runtime/hermes/hermes-v2026.5.16/ocops/auth.py`
- Test: `runtime/hermes/hermes-v2026.5.16/tests/test_ocops_auth.py`

- [ ] **Step 1: 写失败测试**

```python
# tests/test_ocops_auth.py
"""覆盖 Bearer token 校验：正确放行、缺失/错误拒绝、未配置 token 时拒绝一切。"""
from ocops.auth import token_matches


def test_token_matches_correct():
    # 正常：Authorization 头与期望 token 一致 → 放行
    assert token_matches("Bearer s3cret", "s3cret") is True


def test_token_matches_wrong_or_missing():
    # 异常：错误 token / 缺 Bearer 前缀 / 空头 → 拒绝
    assert token_matches("Bearer nope", "s3cret") is False
    assert token_matches("s3cret", "s3cret") is False
    assert token_matches("", "s3cret") is False


def test_token_matches_unset_expected_denies():
    # 边界：服务端未配置 token（空）→ 拒绝一切，避免裸奔
    assert token_matches("Bearer anything", "") is False
```

- [ ] **Step 2: 运行确认失败** → FAIL（ImportError）

- [ ] **Step 3: 实现**

```python
# ocops/auth.py
"""oc-ops 入站鉴权：校验 Authorization: Bearer <OC_OPS_TOKEN>，常量时间比较。"""
from __future__ import annotations

import hmac


def token_matches(authorization_header: str, expected: str) -> bool:
    """比较 Authorization 头里的 Bearer token 与期望值；未配置期望值时一律拒绝。"""
    if not expected:
        return False
    prefix = "Bearer "
    if not authorization_header.startswith(prefix):
        return False
    presented = authorization_header[len(prefix):]
    return hmac.compare_digest(presented, expected)
```

- [ ] **Step 4: 运行确认通过** → PASS（3 passed）

- [ ] **Step 5: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/ocops/auth.py \
        runtime/hermes/hermes-v2026.5.16/tests/test_ocops_auth.py
git commit -m "feat(ocops): 新增 Bearer OC_OPS_TOKEN 常量时间校验

token_matches 校验 Authorization: Bearer 头，hmac.compare_digest 防时序
侧信道；服务端未配置 token 时拒绝一切请求，避免裸奔。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 8：server 骨架 + info/doctor/channel-status/unbind 端点

**Files:**
- Create: `runtime/hermes/hermes-v2026.5.16/ocops/server.py`
- Test: `runtime/hermes/hermes-v2026.5.16/tests/test_ocops_server_basic.py`

server 用 Starlette；鉴权用一个 `Middleware`（读 `OC_OPS_TOKEN` env，非白名单路径校验
`token_matches`，失败返回 401 + `{"code":"UNAUTHORIZED","message":...}`）；
业务 handler 把 `OpsError` 统一捕获 → `code_to_http(code)` + `{"code","message"}`。

- [ ] **Step 1: 写失败测试**

```python
# tests/test_ocops_server_basic.py
"""覆盖 server 鉴权与基础端点：401、info/doctor 200、channel-status/unbind、未知 channel 400。"""
import json
from pathlib import Path

from starlette.testclient import TestClient


def _client(monkeypatch, tmp_path):
    # 构造带固定 token 与 tmp 数据根的测试 client
    monkeypatch.setenv("OC_OPS_TOKEN", "t0ken")
    monkeypatch.setenv("OC_DATA_DIR", str(tmp_path))
    f = tmp_path / "oc-image.json"
    f.write_text(json.dumps({"variant": "hermes-v2026.5.16", "hermes_upstream_ref": "x", "built_at": "y"}))
    monkeypatch.setenv("OC_INFO_FILE", str(f))
    from ocops.server import app
    return TestClient(app)


def test_requires_bearer_token(monkeypatch, tmp_path):
    # 鉴权：无 token → 401
    c = _client(monkeypatch, tmp_path)
    assert c.get("/oc/info").status_code == 401


def test_info_ok(monkeypatch, tmp_path):
    # 正常：带正确 token → 200，返回镜像身份
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/info", headers={"Authorization": "Bearer t0ken"})
    assert r.status_code == 200
    assert r.json()["variant"] == "hermes-v2026.5.16"


def test_channel_status_unknown_channel_400(monkeypatch, tmp_path):
    # 错误码映射：未知 channel → OpsError(BAD_REQUEST) → HTTP 400 + code 体
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/channels/telegram/status", headers={"Authorization": "Bearer t0ken"})
    assert r.status_code == 400
    assert r.json()["code"] == "BAD_REQUEST"
```

- [ ] **Step 2: 运行确认失败** → FAIL

- [ ] **Step 3: 实现 server 骨架 + 4 端点**

```python
# ocops/server.py（骨架 + info/doctor/channel）
"""oc-ops HTTP 服务：把 ocops 各核心模块暴露为类型化 REST + SSE。

鉴权：除健康检查外，所有路由要求 Authorization: Bearer OC_OPS_TOKEN。
错误：业务 OpsError → code_to_http(code) + {code,message}；其它异常 → 500 INTERNAL。"""
from __future__ import annotations

import os
from pathlib import Path

from starlette.applications import Starlette
from starlette.middleware import Middleware
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.responses import JSONResponse, Response
from starlette.routing import Route

from ocops import channel, doctor, info
from ocops.auth import token_matches
from ocops.errors import OpsError, code_to_http


def _data_root() -> Path:
    return Path(os.environ.get("OC_DATA_DIR", "/opt/data"))


def _ok(data, status=200):
    return JSONResponse(data, status_code=status)


def _err(e: OpsError):
    return JSONResponse({"code": e.code, "message": e.message}, status_code=code_to_http(e.code))


class AuthMiddleware(BaseHTTPMiddleware):
    """对非 /healthz 路径校验 Bearer OC_OPS_TOKEN。"""

    async def dispatch(self, request, call_next):
        if request.url.path == "/healthz":
            return await call_next(request)
        if not token_matches(request.headers.get("authorization", ""), os.environ.get("OC_OPS_TOKEN", "")):
            return JSONResponse({"code": "UNAUTHORIZED", "message": "invalid token"}, status_code=401)
        return await call_next(request)


async def healthz(request):
    return Response("ok")


async def get_info(request):
    try:
        return _ok(info.collect_info())
    except OpsError as e:
        return _err(e)


async def get_doctor(request):
    return _ok(doctor.collect_doctor())


async def channel_status(request):
    try:
        return _ok(channel.channel_status(request.path_params["channel"], _data_root()))
    except OpsError as e:
        return _err(e)


async def channel_unbind(request):
    try:
        return _ok(channel.channel_unbind(request.path_params["channel"], _data_root()))
    except OpsError as e:
        return _err(e)


routes = [
    Route("/healthz", healthz),
    Route("/oc/info", get_info),
    Route("/oc/doctor", get_doctor),
    Route("/oc/channels/{channel}/status", channel_status),
    Route("/oc/channels/{channel}/unbind", channel_unbind, methods=["POST"]),
    # cron / kanban / login 路由在 Task 9/10/11 追加
]

app = Starlette(routes=routes, middleware=[Middleware(AuthMiddleware)])
```

- [ ] **Step 4: 运行确认通过** → PASS（3 passed）
- [ ] **Step 5: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/ocops/server.py \
        runtime/hermes/hermes-v2026.5.16/tests/test_ocops_server_basic.py
git commit -m "feat(ocops): 新增 Starlette server 骨架与 info/doctor/channel 端点

oc-ops HTTP 服务骨架：AuthMiddleware 校验 Bearer OC_OPS_TOKEN（401）、
OpsError→code_to_http 错误映射、info/doctor/channel-status/unbind 四端点。
cron/kanban/login 端点后续追加。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 9：cron HTTP 端点（11 个）

**Files:**
- Modify: `runtime/hermes/hermes-v2026.5.16/ocops/server.py`（追加 cron 路由 + handlers）
- Test: `runtime/hermes/hermes-v2026.5.16/tests/test_ocops_server_cron.py`

**端点契约表（每行一个 handler，统一 `try ... except OpsError as e: return _err(e)`）：**

| HTTP | 路由 | 调 ocops.cron | 入参来源 |
|---|---|---|---|
| GET | `/oc/cron/capabilities` | `run_capabilities()` | — |
| GET | `/oc/cron/status` | `run_status()` | — |
| GET | `/oc/cron/jobs` | `run_list(all_=bool(query.all))` | query `all` |
| GET | `/oc/cron/jobs/{id}` | `run_show(id)` | path |
| POST | `/oc/cron/jobs` | `run_create(**body)` | JSON body |
| PATCH | `/oc/cron/jobs/{id}` | `run_update(id, **body)` | path + JSON body |
| POST | `/oc/cron/jobs/{id}/toggle` | `run_toggle(id, enabled=bool(body.enabled))` | path + body |
| POST | `/oc/cron/jobs/{id}/run` | `run_run(id)` | path |
| DELETE | `/oc/cron/jobs/{id}` | `run_delete(id)` → 204 | path |
| GET | `/oc/cron/jobs/{id}/history` | `run_history(id)` | path |
| GET | `/oc/cron/jobs/{id}/output` | `run_output(id, file=query.file)` | path + query `file` |

**worked example（create + delete）：**

```python
async def cron_create(request):
    body = await request.json()
    try:
        # body 字段名与 run_create 形参一致（name/schedule/prompt/...）；多余键忽略。
        return _ok(cron.run_create(**_pick(body, _CRON_CREATE_KEYS)))
    except OpsError as e:
        return _err(e)


async def cron_delete(request):
    try:
        cron.run_delete(request.path_params["id"])
        return Response(status_code=204)
    except OpsError as e:
        return _err(e)
```

`_pick(body, keys)` 是 `{k: body[k] for k in keys if k in body}` 的小工具；
`_CRON_CREATE_KEYS`/`_CRON_UPDATE_KEYS` 列出允许透传给 `run_create`/`run_update` 的键。

- [ ] **Step 1: 写失败测试**（覆盖：capabilities 200、create→201/200 后 show、list `all` 过滤、show NOT_FOUND→404、output 路径逃逸→400、delete→204；用 `HERMES_HOME=tmp` + 必要时 monkeypatch `ocops.cron._run_hermes_cron` 桩避免真调 hermes）。每条中文注释。
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现 11 个 handler + 路由（按契约表，每个 commit 前一次性补齐）**
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/ocops/server.py \
        runtime/hermes/hermes-v2026.5.16/tests/test_ocops_server_cron.py
git commit -m "feat(ocops): server 追加 cron 的 11 个类型化 REST 端点

按契约表暴露 cron capabilities/status/list/show/create/update/toggle/
run/delete/history/output；成功 200（delete 204），OpsError→HTTP 状态码
+ {code,message}，取代旧 stdout 信封。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 10：kanban HTTP 端点（非 watch verb）

**Files:**
- Modify: `runtime/hermes/hermes-v2026.5.16/ocops/server.py`
- Test: `runtime/hermes/hermes-v2026.5.16/tests/test_ocops_server_kanban.py`

**端点契约表（每个 handler 先做 `has_real_hermes()` 守卫，stub→ UNSUPPORTED 409）：**

| HTTP | 路由 | 调 ocops.kanban |
|---|---|---|
| GET | `/oc/kanban/capabilities` | `run_capabilities()`（不守卫）|
| GET | `/oc/kanban/boards` | `run_boards()` |
| GET | `/oc/kanban/tasks` | `run_list(board, status, assignee)`（query）|
| GET | `/oc/kanban/tasks/{id}` | `run_show(board, id)`（board=query，默认 default）|
| GET | `/oc/kanban/tasks/{id}/runs` | `run_runs(board, id)` |
| GET | `/oc/kanban/stats` | `run_stats(board)` |
| POST | `/oc/kanban/tasks` | `run_create(**body)` |
| POST | `/oc/kanban/tasks/{id}/comment` | `run_comment(board, id, body_text)` |
| POST | `/oc/kanban/tasks/{id}/complete` | `run_complete(board, id, result)` |
| POST | `/oc/kanban/tasks/{id}/block` | `run_block(board, id, reason)` |
| POST | `/oc/kanban/tasks/{id}/unblock` | `run_unblock(board, id)` |
| POST | `/oc/kanban/tasks/{id}/archive` | `run_archive(board, id)` |
| POST | `/oc/kanban/tasks/{id}/reassign` | `run_reassign(board, id, to)` |
| POST | `/oc/kanban/tasks/{id}/reclaim` | `run_reclaim(board, id)` |

> 守卫提取为一个 helper：`_require_real_hermes()`，stub 镜像抛 `KanbanError("UNSUPPORTED", ...)`。

- [ ] **Step 1: 写失败测试**（capabilities 200；stub（monkeypatch `has_real_hermes`→False）下 boards→409；create→200；show NOT_FOUND→404。monkeypatch `ocops.kanban.run_hermes`/`hermes_json` 桩）。中文注释。
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现 handler + 路由**
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/ocops/server.py \
        runtime/hermes/hermes-v2026.5.16/tests/test_ocops_server_kanban.py
git commit -m "feat(ocops): server 追加 kanban 非流式类型化 REST 端点

按契约表暴露 kanban capabilities/boards/tasks(CRUD/comment/complete/
block/unblock/archive/reassign/reclaim)/runs/stats；非 capabilities verb
先做 has_real_hermes 守卫（stub→UNSUPPORTED 409）。watch SSE 下个任务做。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 11：SSE 端点（kanban watch + channel login）

**Files:**
- Modify: `runtime/hermes/hermes-v2026.5.16/ocops/server.py`
- Test: `runtime/hermes/hermes-v2026.5.16/tests/test_ocops_server_sse.py`

用 Starlette 的流式响应（`EventSourceResponse` 来自 `sse-starlette`，**或**直接用
`StreamingResponse` 手写 `data: <json>\n\n`）。为少装一个依赖，**用 `StreamingResponse`
手写 SSE 帧**：

```python
from starlette.responses import StreamingResponse


async def kanban_watch(request):
    board = request.query_params.get("board", "default")

    async def gen():
        try:
            # watch_events 是同步 generator（subprocess 阻塞读）；用线程池迭代避免堵事件循环。
            import anyio
            it = await anyio.to_thread.run_sync(lambda: iter(kanban.watch_events(board)))
            while True:
                ev = await anyio.to_thread.run_sync(next, it, _SENTINEL)
                if ev is _SENTINEL:
                    break
                yield f"data: {json.dumps(ev, ensure_ascii=False)}\n\n"
        except OpsError as e:
            yield f"event: error\ndata: {json.dumps({'code': e.code, 'message': e.message})}\n\n"

    return StreamingResponse(gen(), media_type="text/event-stream")


async def channel_login(request):
    ch = request.path_params["channel"]

    async def gen():
        # channel_login 已是 async generator，直接逐条转 SSE 帧。
        async for ev in channel_mod.channel_login(ch):
            yield f"data: {json.dumps(ev, ensure_ascii=False)}\n\n"

    return StreamingResponse(gen(), media_type="text/event-stream")
```

路由追加：`Route("/oc/kanban/watch", kanban_watch)`、
`Route("/oc/channels/{channel}/login", channel_login, methods=["POST"])`。

- [ ] **Step 1: 写失败测试**

```python
# tests/test_ocops_server_sse.py
"""覆盖 SSE 端点：login 事件流（mock qr_login）逐帧、watch 事件流（fake hermes）逐帧。"""
# 用 TestClient(app).stream(...) 或 .get(... ) 读 text/event-stream，断言 data: 行包含
# qrcode/bound（login）或两条 Event（watch）。每条用例中文注释，monkeypatch 桩同前。
```

- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现两个 SSE handler + 路由**
- [ ] **Step 4: 运行确认通过 + 全量自检** → PASS
- [ ] **Step 5: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/ocops/server.py \
        runtime/hermes/hermes-v2026.5.16/tests/test_ocops_server_sse.py
git commit -m "feat(ocops): server 追加 kanban watch 与 channel login 的 SSE 端点

GET /oc/kanban/watch 把 watch_events generator（线程池迭代避免堵事件循环）
逐条转 SSE data 帧；POST /oc/channels/{channel}/login 把 channel_login
async generator 转 SSE。手写 SSE 帧、不引入额外依赖。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Phase 6 — 镜像与构建（同镜像方案）

### Task 12：把 ocops 打进 hermes 镜像

**Files:**
- Modify: `runtime/hermes/hermes-v2026.5.16/Dockerfile`
- Modify: `runtime/hermes/hermes-v2026.5.16/CONTRACT.md`（对外命令补 oc-ops 服务说明）

- [ ] **Step 1: 改 Dockerfile**

在装 pyyaml/pytest 的 `uv pip install` 之后，新增往 **hermes venv** 装 server 依赖；
并 COPY ocops 包。具体：

```dockerfile
# oc-ops HTTP 服务依赖：装进 hermes venv（oc-channel-login 需 venv 内的 weixin SDK，
# server 与之同进程环境）。starlette+uvicorn+anyio 提供 ASGI 与 SSE。
RUN uv pip install --python /usr/local/lib/hermes-agent/venv/bin/python --no-cache-dir \
      starlette uvicorn anyio

# oc-ops 核心包：与 oc-* CLI 共用同一份逻辑。装到 venv 的 site 可见路径。
COPY ocops/ /usr/local/lib/ocops/
```

并把 `oc-*` 的 `sys.path.insert(0, "/usr/local/lib")` 与镜像内 `ocops` 位置对齐
（确认 `/usr/local/lib` 在 venv `python` 的 import 搜索路径，否则改 COPY 到 venv 的
site-packages，或在 oc-* shim 里显式 `sys.path.insert`）。

构建期自检已有 `python -m pytest /usr/local/lib/oc-entrypoint/tests/`；**新增**对 ocops
测试的覆盖：把 `tests/test_ocops_*.py` 随 `tests/` 一起 COPY（Dockerfile 已 COPY `tests/`），
并确保自检命令能 import `ocops`（在该 pytest 调用前 `ENV PYTHONPATH=/usr/local/lib` 或在
命令里加 `PYTHONPATH`）。

- [ ] **Step 2: 本地构建验证**

Run: `make build-hermes-runtime HERMES_VARIANT=hermes-v2026.5.16`
Expected: 构建成功；构建期 pytest 自检（含 ocops 测试）全绿；末尾无 `cron-contract`/
`kanban-contract` 残留报错（`hermes-inject-contract` 仍正常）。

> 若本机网络导致 install.sh / pip 装不上（见 `docs/local-development.md` 代理说明），
> 在交付说明中记录「镜像构建未在本机完成」的原因与风险，不得伪造构建通过。

- [ ] **Step 3: 更新 CONTRACT.md**

在「# 镜像对外命令」段补一行说明 oc-ops 服务：

```markdown
# oc-ops HTTP 服务（spec-E）
- 同镜像第二用途：覆盖 CMD 启动 `python -m uvicorn ocops.server:app --host 0.0.0.0 --port 8080`
- Bearer OC_OPS_TOKEN 鉴权；端点见 docs（cron/kanban/channel 类型化 REST + SSE）
- oc-* CLI 保留为薄 shim，逻辑与 ocops 包共用
```

- [ ] **Step 4: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/Dockerfile \
        runtime/hermes/hermes-v2026.5.16/CONTRACT.md
git commit -m "build(hermes): 把 ocops 包与 starlette/uvicorn 打进 hermes 镜像

往 hermes venv 装 starlette/uvicorn/anyio，COPY ocops/ 到镜像；构建期
pytest 自检纳入 ocops 测试。oc-ops 容器复用本镜像、仅覆盖 CMD 起 uvicorn
（spec-E 同镜像方案，版本助手/更新只管一个标签）。CONTRACT.md 补 oc-ops
服务说明。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Phase 7 — Go：internal/integrations/ocops 类型化 HTTP 客户端

### Task 13：契约 DTO 迁移到 ocops 包（解循环依赖）

**Files:**
- Create: `internal/integrations/ocops/types.go`
- Modify: `internal/service/hermes_cron_types.go`、`internal/service/hermes_kanban_types.go`
  （删除被迁走的类型定义，保留 service 专属的 Input/Filter 类型）
- Modify: 所有引用这些 DTO 的文件（`hermes_cron.go`/`hermes_kanban.go`/handlers/测试）

**迁移清单（从 `service` 迁到 `ocops` 包，改包名 `ocops`、首字母仍大写）：**

- cron：`CronSchedule`、`CronRepeat`、`CronJob`、`CronStatus`、`CronRunEntry`、
  `CronRunOutput`、`CronFeatures`、`CronCapabilities`。
- kanban：`KanbanBoard`、`KanbanTask`、`KanbanComment`、`KanbanEvent`、
  `KanbanTaskDetail`、`KanbanStats`、`KanbanTaskRun`、`KanbanFeatures`、
  `KanbanCapabilities`。

**保留在 `service` 包**（属业务输入/过滤，非线缆契约）：`CronJobFilter`、
`CreateCronJobInput`、`UpdateCronJobInput`、`KanbanTaskFilter`、`CreateKanbanTaskInput`。

> 迁移后 `service` 引用改为 `ocops.CronJob` 等（service import ocops，单向，无循环）。
> handlers 若直接引用这些类型同理改 `ocops.X`。JSON tag 原样保留（线缆字段名不变）。

- [ ] **Step 1: 建 ocops/types.go，粘贴迁移的类型（含原中文字段注释），包名 `ocops`**
- [ ] **Step 2: 从 service 两个 types 文件删除已迁类型**
- [ ] **Step 3: 全仓替换引用**

Run: `cd /home/hujing/dir/software/ywjs/oc-manager && grep -rln 'service\.\(CronJob\|KanbanTaskDetail\)' || true`
然后把 `service` 包内对这些类型的**裸引用**加 `ocops.` 前缀、import ocops；
其它包对 `service.CronJob` 的引用改 `ocops.CronJob`。

- [ ] **Step 4: 编译确认通过**

Run: `go build ./... && go vet ./internal/service/... ./internal/integrations/ocops/...`
Expected: 通过（无循环依赖、无未定义类型）

- [ ] **Step 5: 提交**

```bash
git add internal/integrations/ocops/types.go \
        internal/service/hermes_cron_types.go internal/service/hermes_kanban_types.go \
        # + 受影响的 .go 文件（按 git status 精确添加）
git commit -m "refactor(ocops): 契约 DTO 从 service 迁到 ocops 包

把 CronJob/CronCapabilities/KanbanTaskDetail 等线缆契约类型迁到新
internal/integrations/ocops 包（契约属主），service/handlers 改引用
ocops.X；service 专属的 Input/Filter 类型留在 service。为 ocops HTTP
客户端返回类型化对象、避免 service↔ocops 循环依赖做准备。JSON 字段不变。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 14：ocops.Client 核心与错误映射

**Files:**
- Create: `internal/integrations/ocops/errors.go`
- Create: `internal/integrations/ocops/client.go`
- Test: `internal/integrations/ocops/client_test.go`

- [ ] **Step 1: 写失败测试**

```go
// client_test.go
package ocops_test

// TestClientDoJSONMapsStatusToError 验证 HTTP 状态码→哨兵错误映射：
// 400→ErrBadRequest、404→ErrNotFound、409→ErrUnsupported、500→ErrOutputInvalid、
// 502→ErrCLI、401→ErrUnauthorized；2xx 正常解码 body。
func TestClientDoJSONMapsStatusToError(t *testing.T) {
	// table-driven：每行一个 (HTTP 状态, body) → 期望哨兵错误 / 解码结果
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(200); _, _ = w.Write([]byte(`{"id":"j1"}`))
		case "/nf":
			w.WriteHeader(404); _, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"x"}`))
		}
	}))
	defer srv.Close()
	c := ocops.NewClient(http.DefaultClient)
	ep := ocops.Endpoint{BaseURL: srv.URL, Token: "t"}

	var job ocops.CronJob
	require.NoError(t, c.DoJSON(context.Background(), ep, "GET", "/ok", nil, &job)) // 2xx 解码
	assert.Equal(t, "j1", job.ID)

	err := c.DoJSON(context.Background(), ep, "GET", "/nf", nil, nil) // 404→ErrNotFound
	require.ErrorIs(t, err, ocops.ErrNotFound)
}

// TestClientSendsBearer 验证请求带 Authorization: Bearer <token>。
func TestClientSendsBearer(t *testing.T) { /* 断言 server 收到的 header */ }
```

- [ ] **Step 2: 运行确认失败** → FAIL

- [ ] **Step 3: 实现 errors.go + client.go**

```go
// errors.go
package ocops

import "errors"

// 哨兵错误：与 oc-ops HTTP 状态码 / 契约错误码一一对应，供 service 映射成自身哨兵错误。
var (
	ErrBadRequest    = errors.New("ocops: bad request")     // 400
	ErrNotFound      = errors.New("ocops: not found")       // 404
	ErrUnsupported   = errors.New("ocops: unsupported")     // 409
	ErrOutputInvalid = errors.New("ocops: internal/invalid")// 500
	ErrCLI           = errors.New("ocops: hermes cli failed")// 502 及未知
	ErrUnauthorized  = errors.New("ocops: unauthorized")    // 401
)

// statusToErr 把 HTTP 状态码映射成哨兵错误；2xx 返回 nil。
func statusToErr(status int) error {
	switch status {
	case 400:
		return ErrBadRequest
	case 401:
		return ErrUnauthorized
	case 404:
		return ErrNotFound
	case 409:
		return ErrUnsupported
	case 500:
		return ErrOutputInvalid
	default:
		if status >= 200 && status < 300 {
			return nil
		}
		return ErrCLI
	}
}
```

```go
// client.go
package ocops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Endpoint 是单个 app 的 oc-ops 访问坐标：基址 + per-app 控制 token。
// 真实寻址（k8s Service DNS）与 token 来源由 spec-A 注入，spec-E 经 service.OcOpsResolver 解耦。
type Endpoint struct {
	BaseURL string
	Token   string
}

// Client 是 oc-ops 的类型化 HTTP 客户端。
type Client struct {
	httpClient *http.Client
}

// NewClient 构造客户端。
func NewClient(h *http.Client) *Client {
	if h == nil {
		h = http.DefaultClient
	}
	return &Client{httpClient: h}
}

// DoJSON 发一次 JSON 请求：reqBody 非 nil 时序列化为请求体；2xx 解码到 out（out 可为 nil）；
// 非 2xx 用 statusToErr 映射哨兵错误，并把 body 的 message 包进错误文本。
func (c *Client) DoJSON(ctx context.Context, ep Endpoint, method, path string, reqBody, out any) error {
	var body io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("ocops: marshal 请求体: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, ep.BaseURL+path, body)
	if err != nil {
		return fmt.Errorf("ocops: 构造请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCLI, err)
	}
	defer resp.Body.Close()
	if sentinel := statusToErr(resp.StatusCode); sentinel != nil {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("%w: %s", sentinel, e.Message)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("%w: 解码响应: %v", ErrOutputInvalid, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: 提交**

```bash
git add internal/integrations/ocops/errors.go internal/integrations/ocops/client.go \
        internal/integrations/ocops/client_test.go
git commit -m "feat(ocops): 新增 HTTP Client 核心与 HTTP 状态码→哨兵错误映射

Client.DoJSON 统一发请求、带 Bearer token、2xx 解码 body、非 2xx 按
statusToErr 映射 ErrBadRequest/NotFound/Unsupported/OutputInvalid/CLI/
Unauthorized 并携带 message。Endpoint 承载 per-app 基址+token。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 15：cron 客户端方法（11 个）

**Files:**
- Create: `internal/integrations/ocops/client_cron.go`
- Test: `internal/integrations/ocops/client_cron_test.go`

**方法契约表（全部 `func (c *Client) X(ctx, ep Endpoint, ...) (..., error)`，内部调 `DoJSON`）：**

| 方法 | HTTP | path | 返回 |
|---|---|---|---|
| `CronCapabilities(ctx,ep)` | GET | `/oc/cron/capabilities` | `CronCapabilities` |
| `CronStatus(ctx,ep)` | GET | `/oc/cron/status` | `CronStatus` |
| `CronList(ctx,ep,all bool)` | GET | `/oc/cron/jobs?all=` | `[]CronJob` |
| `CronShow(ctx,ep,id)` | GET | `/oc/cron/jobs/{id}` | `CronJob` |
| `CronCreate(ctx,ep,req CronCreateReq)` | POST | `/oc/cron/jobs` | `CronJob` |
| `CronUpdate(ctx,ep,id,req CronUpdateReq)` | PATCH | `/oc/cron/jobs/{id}` | `CronJob` |
| `CronToggle(ctx,ep,id,enabled bool)` | POST | `/oc/cron/jobs/{id}/toggle` | `CronJob` |
| `CronRun(ctx,ep,id)` | POST | `/oc/cron/jobs/{id}/run` | `CronJob` |
| `CronDelete(ctx,ep,id)` | DELETE | `/oc/cron/jobs/{id}` | `error` |
| `CronHistory(ctx,ep,id)` | GET | `/oc/cron/jobs/{id}/history` | `[]CronRunEntry` |
| `CronOutput(ctx,ep,id,file)` | GET | `/oc/cron/jobs/{id}/output?file=` | `CronRunOutput` |

`CronCreateReq`/`CronUpdateReq` 是 ocops 包内请求体结构，JSON 字段名与 Task 9 server 的
`_CRON_CREATE_KEYS` 对齐（name/schedule/prompt/deliver/repeat/script/no_agent/workdir/
skills/model/provider/base_url；update 用指针表达「未提交」）。path 参数用 `url.PathEscape`。

**worked example：**

```go
// client_cron.go（节选）
func (c *Client) CronShow(ctx context.Context, ep Endpoint, id string) (CronJob, error) {
	var job CronJob
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/cron/jobs/"+url.PathEscape(id), nil, &job)
	return job, err
}

func (c *Client) CronCreate(ctx context.Context, ep Endpoint, req CronCreateReq) (CronJob, error) {
	var job CronJob
	err := c.DoJSON(ctx, ep, http.MethodPost, "/oc/cron/jobs", req, &job)
	return job, err
}

func (c *Client) CronDelete(ctx context.Context, ep Endpoint, id string) error {
	return c.DoJSON(ctx, ep, http.MethodDelete, "/oc/cron/jobs/"+url.PathEscape(id), nil, nil)
}
```

- [ ] **Step 1: 写失败测试**（httptest 断言每个方法的 method/path/query/body + 解码；至少覆盖 Show、List(all)、Create、Delete、Output(file query)、以及一次 404→ErrNotFound）。表驱动，每用例中文注释。
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现 11 个方法（含 `CronCreateReq`/`CronUpdateReq`）**
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: 提交**

```bash
git add internal/integrations/ocops/client_cron.go internal/integrations/ocops/client_cron_test.go
git commit -m "feat(ocops): 新增 cron 的 11 个类型化客户端方法

按契约表实现 CronCapabilities/Status/List/Show/Create/Update/Toggle/Run/
Delete/History/Output，请求体 CronCreateReq/CronUpdateReq 字段与 server
端对齐，path 参数 url.PathEscape。httptest 覆盖方法/路径/query/body 与
错误映射。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 16：kanban 客户端方法（非流式）

**Files:**
- Create: `internal/integrations/ocops/client_kanban.go`
- Test: `internal/integrations/ocops/client_kanban_test.go`

**方法契约表**（对应 Task 10 路由；返回类型对齐 `HermesKanbanService` 现有方法）：

| 方法 | HTTP | path |
|---|---|---|
| `KanbanCapabilities(ctx,ep)` → `KanbanCapabilities` | GET | `/oc/kanban/capabilities` |
| `KanbanBoards(ctx,ep)` → `[]KanbanBoard` | GET | `/oc/kanban/boards` |
| `KanbanList(ctx,ep,board,status,assignee)` → `[]KanbanTask` | GET | `/oc/kanban/tasks?board=&status=&assignee=` |
| `KanbanShow(ctx,ep,board,id)` → `KanbanTaskDetail` | GET | `/oc/kanban/tasks/{id}?board=` |
| `KanbanRuns(ctx,ep,board,id)` → `[]KanbanTaskRun` | GET | `/oc/kanban/tasks/{id}/runs?board=` |
| `KanbanStats(ctx,ep,board)` → `KanbanStats` | GET | `/oc/kanban/stats?board=` |
| `KanbanCreate(ctx,ep,req KanbanCreateReq)` → `KanbanTaskDetail` | POST | `/oc/kanban/tasks` |
| `KanbanComment(ctx,ep,board,id,body)` → `KanbanTaskDetail` | POST | `/oc/kanban/tasks/{id}/comment` |
| `KanbanComplete(ctx,ep,board,id,result)` → `KanbanTaskDetail` | POST | `/oc/kanban/tasks/{id}/complete` |
| `KanbanBlock(ctx,ep,board,id,reason)` → `KanbanTaskDetail` | POST | `/oc/kanban/tasks/{id}/block` |
| `KanbanUnblock(ctx,ep,board,id)` → `KanbanTaskDetail` | POST | `/oc/kanban/tasks/{id}/unblock` |
| `KanbanArchive(ctx,ep,board,id)` → `KanbanTaskDetail` | POST | `/oc/kanban/tasks/{id}/archive` |
| `KanbanReassign(ctx,ep,board,id,to)` → `KanbanTaskDetail` | POST | `/oc/kanban/tasks/{id}/reassign` |
| `KanbanReclaim(ctx,ep,board,id)` → `KanbanTaskDetail` | POST | `/oc/kanban/tasks/{id}/reclaim` |

board 作为 query；写 verb 的 body/reason/result/to 走 JSON body（如
`{"body":"..."}`/`{"reason":"..."}`/`{"result":"..."}`/`{"to":"..."}`）。worked example
形如 Task 15。

- [ ] **Step 1: 写失败测试**（覆盖 Boards、List(query)、Show、Create、Comment(body)、一次 409→ErrUnsupported）。中文注释。
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现方法 + `KanbanCreateReq`**
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: 提交**

```bash
git add internal/integrations/ocops/client_kanban.go internal/integrations/ocops/client_kanban_test.go
git commit -m "feat(ocops): 新增 kanban 非流式类型化客户端方法

按契约表实现 boards/tasks(CRUD/comment/complete/block/unblock/archive/
reassign/reclaim)/runs/stats/capabilities，board 走 query、写字段走 JSON
body。httptest 覆盖路径/query/body 与 UNSUPPORTED 错误映射。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 17：SSE 客户端（kanban watch + channel login）

**Files:**
- Create: `internal/integrations/ocops/client_sse.go`
- Test: `internal/integrations/ocops/client_sse_test.go`

实现 SSE 解析：发 GET/POST 后逐行读 `data: <json>`，解析成事件经 channel 投递；
连接结束/出错关闭 channel。两个方法：

```go
// client_sse.go（签名）
// WatchKanban 订阅 board 事件流，逐条投递 KanbanEvent；ctx 取消或流结束关闭 channel。
func (c *Client) WatchKanban(ctx context.Context, ep Endpoint, board string) (<-chan KanbanEvent, error)

// ChannelLogin 触发渠道登录 SSE，投递 ocops.ChannelLoginEvent（qrcode/bound/timeout/failed）。
func (c *Client) ChannelLogin(ctx context.Context, ep Endpoint, channel string) (<-chan ChannelLoginEvent, error)

// ChannelLoginEvent 是 login SSE 事件：Event ∈ {qrcode,bound,timeout,failed}，
// qrcode 用 URL，failed 用 Reason。字段对齐 server 端 channel_login 的 yield。
type ChannelLoginEvent struct {
	Event  string `json:"event"`
	URL    string `json:"url,omitempty"`
	Reason string `json:"reason,omitempty"`
}
```

> 解析用 `bufio.Scanner`，识别 `data: ` 前缀行 JSON、`event: error` 帧；
> 用 goroutine 读流、`defer close(ch)`、`defer resp.Body.Close()`，select ctx.Done。

- [ ] **Step 1: 写失败测试**（httptest 返回 `text/event-stream` 连续 data 帧；断言 WatchKanban 收到 2 个 Event、ChannelLogin 收到 qrcode→bound；ctx 取消能关闭 channel）。中文注释。
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: 提交**

```bash
git add internal/integrations/ocops/client_sse.go internal/integrations/ocops/client_sse_test.go
git commit -m "feat(ocops): 新增 kanban watch 与 channel login 的 SSE 客户端

WatchKanban/ChannelLogin 解析 text/event-stream 的 data 帧，逐条投递
KanbanEvent / ChannelLoginEvent；ctx 取消或流结束关闭 channel。httptest
覆盖多帧解析与取消关闭。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Phase 8 — Go：OcOps 接口 / resolver / service 改造 / 装配 / 删 exec

### Task 18：OcOps 接口、OcOpsResolver、错误映射

**Files:**
- Create: `internal/service/ocops.go`
- Test: `internal/service/ocops_test.go`

```go
// ocops.go
package service

import (
	"context"
	"errors"

	"oc-manager/internal/integrations/ocops"
)

// OcOps 抽象 oc-ops 客户端，便于 service 单测注入假实现；生产实现是 *ocops.Client。
// 方法签名镜像 ocops.Client（见 Task 15/16/17），统一首参 ctx、次参 ocops.Endpoint。
type OcOps interface {
	CronCapabilities(ctx context.Context, ep ocops.Endpoint) (ocops.CronCapabilities, error)
	CronStatus(ctx context.Context, ep ocops.Endpoint) (ocops.CronStatus, error)
	CronList(ctx context.Context, ep ocops.Endpoint, all bool) ([]ocops.CronJob, error)
	CronShow(ctx context.Context, ep ocops.Endpoint, id string) (ocops.CronJob, error)
	CronCreate(ctx context.Context, ep ocops.Endpoint, req ocops.CronCreateReq) (ocops.CronJob, error)
	CronUpdate(ctx context.Context, ep ocops.Endpoint, id string, req ocops.CronUpdateReq) (ocops.CronJob, error)
	CronToggle(ctx context.Context, ep ocops.Endpoint, id string, enabled bool) (ocops.CronJob, error)
	CronRun(ctx context.Context, ep ocops.Endpoint, id string) (ocops.CronJob, error)
	CronDelete(ctx context.Context, ep ocops.Endpoint, id string) error
	CronHistory(ctx context.Context, ep ocops.Endpoint, id string) ([]ocops.CronRunEntry, error)
	CronOutput(ctx context.Context, ep ocops.Endpoint, id, file string) (ocops.CronRunOutput, error)
	// kanban 方法（镜像 Task 16）…
	// channel + SSE 方法（镜像 Task 17 + info/doctor/channel-status/unbind）…
}

// OcOpsAppLocation 是执行 oc-ops 调用所需的全部 app 信息（取代旧 CronAppLocation/KanbanAppLocation）。
type OcOpsAppLocation struct {
	OrgID       string         // 归属组织，用于权限判断
	OwnerUserID string         // 拥有者，用于 org_member 权限判断
	Endpoint    ocops.Endpoint // oc-ops 基址 + per-app token
	Supported   bool           // false 表示 dev stub / 不支持 → UNSUPPORTED
}

// OcOpsResolver 把 appID 解析为 oc-ops 调用坐标。
// 注意：真实 k8s Service DNS 寻址与 per-app token 的生成/存储/注入是 spec-A；
// 本接口在 spec-E 仅由 store 最小实现（见下），单测用假实现。
type OcOpsResolver interface {
	Resolve(ctx context.Context, appID string) (OcOpsAppLocation, error)
}

// mapOcOpsCronErr 把 ocops 哨兵错误翻译成 cron service 既有哨兵错误，保留语义不变。
func mapOcOpsCronErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ocops.ErrBadRequest):
		return ErrCronBadRequest
	case errors.Is(err, ocops.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, ocops.ErrUnsupported):
		return ErrCronNotSupported
	case errors.Is(err, ocops.ErrOutputInvalid):
		return ErrCronOutputInvalid
	default:
		return ErrCronCLI
	}
}
// mapOcOpsKanbanErr 同理映射到 ErrKanban*。
```

最小 resolver（spec-E 实现，spec-A 替换为 k8s 寻址）：

```go
// OcOpsResolverFromStore 从 app store 解析 oc-ops 坐标。
// BaseURL 按 oc-apps Service 命名约定拼装、Token 读 per-app 来源；spec-A 会替换为
// client-go 真实寻址 + bootstrap token。Supported 由镜像 ref 是否 -dev 判定（沿用旧 Stub）。
type OcOpsResolverFromStore struct {
	store      cronAppStore // 复用现有最小 GetApp 接口
	baseURLTpl string       // 如 "http://app-%s-ocops.oc-apps.svc:8080"（spec-A 调整）
}

func (r *OcOpsResolverFromStore) Resolve(ctx context.Context, appID string) (OcOpsAppLocation, error) {
	app, err := r.store.GetApp(ctx, appID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OcOpsAppLocation{}, ErrNotFound
		}
		return OcOpsAppLocation{}, fmt.Errorf("查询 app 失败: %w", err)
	}
	return OcOpsAppLocation{
		OrgID:       app.OrgID,
		OwnerUserID: app.OwnerUserID,
		Endpoint:    ocops.Endpoint{BaseURL: fmt.Sprintf(r.baseURLTpl, appID) /* token: spec-A 注入 */},
		Supported:   !strings.HasSuffix(app.RuntimeImageRef, "-dev"),
	}, nil
}
```

> token 来源在 spec-E 是占位（空或从约定字段读）；这是 E↔A 边界，已在设计 §5.2/§8 记录。
> 单测覆盖 resolver 的 NOT_FOUND、Supported 判定、错误映射函数全分支。

- [ ] **Step 1: 写失败测试**（`mapOcOpsCronErr`/`mapOcOpsKanbanErr` 全分支 table-driven；resolver NOT_FOUND 与 Supported）。中文注释。
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现 ocops.go（接口全集 + 类型 + 映射 + resolver）**
- [ ] **Step 4: 运行确认通过 + `go build ./...`** → PASS
- [ ] **Step 5: 提交**

```bash
git add internal/service/ocops.go internal/service/ocops_test.go
git commit -m "feat(service): 新增 OcOps 接口、OcOpsResolver 与错误映射

OcOps 接口镜像 ocops.Client 供 service 注入假实现；OcOpsAppLocation 取代
CronAppLocation/KanbanAppLocation（含 Endpoint+Supported）；
OcOpsResolverFromStore 从 app store 解析坐标（k8s 真实寻址/token 注入留给
spec-A）；mapOcOpsCronErr/KanbanErr 把 ocops 哨兵错误翻译回 service 哨兵
错误，语义不变。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 19：HermesCronService 改用 OcOps

**Files:**
- Modify: `internal/service/hermes_cron.go`
- Modify: `internal/service/hermes_cron_test.go`

改造要点：

- `HermesCronService` 字段 `execer cronExecer` + `locator cronAppLocator` →
  `ops OcOps` + `resolver OcOpsResolver`；`NewHermesCronService(ops OcOps, resolver OcOpsResolver)`。
- `resolve`/`resolveManage` 用 `resolver.Resolve` 返回 `OcOpsAppLocation`，权限判断不变
  （`CanViewAppCron`/`CanManageAppCron`），`!Supported` → `ErrCronNotSupported`，
  Endpoint.BaseURL 为空视为运行时不可用 → `ErrCronRuntimeUnavailable`。
- 删除 `runOCCron`、`cronEnvelope`、`mapCronErrorCode`、`cronExitError` 等 exec/信封逻辑；
  各方法改调 `s.ops.CronX(ctx, loc.Endpoint, ...)`，错误经 `mapOcOpsCronErr` 翻译。
- **保留**全部输入校验（`validateCron*`、`appendCreateArgs` 的长度/正则校验逻辑），
  但把「拼 argv」改成「填 `ocops.CronCreateReq`/`CronUpdateReq`」。`cronAppLocator`/
  `CronAppLocation`/`CronAppLocatorFromStore` 删除（被 OcOpsResolver 取代）。

worked example（CreateJob）：

```go
func (s *HermesCronService) CreateJob(ctx context.Context, principal auth.Principal, appID string, in CreateCronJobInput) (ocops.CronJob, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return ocops.CronJob{}, err
	}
	req, err := buildCronCreateReq(in) // 复用原 appendCreateArgs 的校验，产出类型化请求体
	if err != nil {
		return ocops.CronJob{}, err
	}
	job, err := s.ops.CronCreate(ctx, loc.Endpoint, req)
	if err != nil {
		return ocops.CronJob{}, mapOcOpsCronErr(err)
	}
	if err := validateCronJobData(job); err != nil {
		return ocops.CronJob{}, err
	}
	return job, nil
}
```

`buildCronCreateReq`/`buildCronUpdateReq` 把原 `appendCreateArgs`/`appendUpdateArgs` 的
校验逻辑保留、产出 `ocops.CronCreateReq`/`CronUpdateReq`（而非 `[]string` argv）。

- [ ] **Step 1: 改测试**（`hermes_cron_test.go` 把假 `cronExecer` 换成假 `OcOps` + 假 `OcOpsResolver`，断言正常/权限拒绝/UNSUPPORTED/NOT_FOUND；保留校验类用例）。中文注释。
- [ ] **Step 2: 运行确认失败** → FAIL（编译错误/接口不符）
- [ ] **Step 3: 实现改造**
- [ ] **Step 4: 运行确认通过** → `go test ./internal/service/ -run Cron -v` PASS
- [ ] **Step 5: 提交**

```bash
git add internal/service/hermes_cron.go internal/service/hermes_cron_test.go
git commit -m "refactor(service): HermesCronService 改走 OcOps HTTP 客户端

字段从 cronExecer+locator 换成 OcOps+OcOpsResolver；删除 runOCCron/信封
解析/exec 路径，各方法改调类型化 ocops 客户端，错误经 mapOcOpsCronErr
翻译；保留全部输入校验，argv 拼装改为构造 ocops.CronCreateReq/UpdateReq。
CronAppLocator 系列删除。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 20：HermesKanbanService 改用 OcOps

**Files:**
- Modify: `internal/service/hermes_kanban.go`
- Modify: `internal/service/hermes_kanban_test.go`

同 Task 19 的改造模式：

- 字段 → `ops OcOps` + `resolver OcOpsResolver`；删 `kanbanExecer`/`runOCKanban`/信封。
- 各方法改调 `s.ops.KanbanX(...)`，`StreamEvents(... onLine)` 改为 `WatchEvents` 消费
  `s.ops.WatchKanban(ctx, ep, board)` 的事件 channel（handler 侧把 channel 转 SSE，见 Task 22 wiring）。
- 保留 `validateBoard`/`boardSlugRe`/状态白名单等校验，产出 `ocops.KanbanCreateReq` 等。

- [ ] **Step 1: 改测试**（假 OcOps + resolver；覆盖 list/show/create/comment 正常 + 权限 + UNSUPPORTED + watch 事件转发）。中文注释。
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** → `go test ./internal/service/ -run Kanban -v` PASS
- [ ] **Step 5: 提交**

```bash
git add internal/service/hermes_kanban.go internal/service/hermes_kanban_test.go
git commit -m "refactor(service): HermesKanbanService 改走 OcOps HTTP 客户端

字段换成 OcOps+OcOpsResolver；删 kanbanExecer/runOCKanban/信封；各方法改
调类型化 ocops 客户端；StreamEvents 改消费 WatchKanban 事件 channel。保留
board/状态校验，argv 改为构造 ocops 请求体。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 21：commands.go（info/doctor/channel）与 wechat_runner 改用 OcOps

**Files:**
- Modify: `internal/integrations/hermes/commands.go`
- Modify: `internal/integrations/hermes/wechat_runner.go`（+ 对应 `_test.go`）

- `commands.go`：删 `ContainerExecer`、`runJSONCmd`；`RunInfo/RunDoctor/RunChannelStatus/
  RunChannelUnbind` 改为薄封装调 `OcOps`（或直接由调用方改调 `ocops.Client`）。
  `Info`/`Doctor`/`ChannelStatus`/`ChannelResult` 类型若仅此处用，迁到 ocops 包或保留并由
  client 复用（按 Task 13 一致性，优先用 ocops 包类型）。
- `wechat_runner.go`：`StreamWeChatLogin` 改为消费 `OcOps.ChannelLogin(ctx, ep, "weixin")`
  的 `<-chan ocops.ChannelLoginEvent`，翻译成现有 `WeixinEvent`（qrcode→QRCode、bound→Bound、
  timeout/failed→Failed）。删 `ContainerExecutor`/`ExecAttach`/stdcopy 逻辑。
  QR 识别不再靠 stderr 前缀（改由 server 端 qrcode 事件直接给 URL）。

- [ ] **Step 1: 改测试**（`wechat_runner_test.go` 用假 OcOps 投递 qrcode→bound / failed 事件序列，断言 WeixinEvent 序列）。中文注释。
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** → `go test ./internal/integrations/hermes/... -v` PASS
- [ ] **Step 5: 提交**

```bash
git add internal/integrations/hermes/commands.go \
        internal/integrations/hermes/wechat_runner.go \
        internal/integrations/hermes/wechat_runner_test.go
git commit -m "refactor(hermes): info/doctor/channel 与微信登录改走 OcOps HTTP

commands.go 删 ContainerExecer/runJSONCmd，info/doctor/channel-status/
unbind 改调 OcOps；wechat_runner 的 StreamWeChatLogin 改消费 ChannelLogin
SSE 事件 channel 并翻译成 WeixinEvent，删 ExecAttach/stdcopy 流解析。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 22：cmd/server 装配 ocops 客户端 + resolver

**Files:**
- Modify: `cmd/server/main.go`（约 196、386-418 行附近）
- Modify: 相关 handler 构造（kanban watch SSE handler 从 service 取事件 channel）

- 构造 `ocopsClient := ocops.NewClient(httpClientWithTimeout)`；构造
  `ocopsResolver := service.NewOcOpsResolverFromStore(store, baseURLTpl)`（baseURLTpl 从
  config 读，spec-E 给一个本地/约定默认值，spec-A 替换）。
- `service.NewHermesCronService(ocopsClient, ocopsResolver)`、
  `service.NewHermesKanbanService(ocopsClient, ocopsResolver)`。
- info/doctor/channel/wechat 的装配同样改用 `ocopsClient`（不再传 `runtimeAdapter`）。
- 移除仅为 oc-* 服务的 `runtimeAdapter` 传参；`runtimeAdapter` 本身保留（其余非 oc-* 能力
  仍在用，spec-A 再清理）。

- [ ] **Step 1: 改装配**
- [ ] **Step 2: `go build ./... && go vet ./...`** → 通过
- [ ] **Step 3: 全量后端测试** → `go test ./...` 全绿（修复因签名变更波及的 handler 测试）
- [ ] **Step 4: 提交**

```bash
git add cmd/server/main.go internal/api/handlers/  # 按 git status 精确添加受影响文件
git commit -m "feat(server): 装配 ocops HTTP 客户端与 resolver 替换 exec 通道

cmd/server 构造 ocops.Client + OcOpsResolverFromStore，注入 HermesCron/
HermesKanban 与 info/doctor/channel/微信登录；oc-* 不再经 runtimeAdapter
exec。baseURL 模板从 config 读（spec-A 替换为 k8s 真实寻址）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 23：清理失效的 exec 代码路径

**Files:**
- Modify: `internal/integrations/runtime/adapter.go` 及实现 `agent_backed.go`（按调用方核对）

- [ ] **Step 1: 核对调用方**

Run: `grep -rn 'ContainerExecJSON\|ContainerExecStream\|ExecAttach\|ContainerExec(' internal/ cmd/`
判断这些 Adapter 方法除 oc-*（已迁走）外是否还有调用方。

- [ ] **Step 2: 处置**
  - 若**无**其他调用方：从 `Adapter` 接口与 `AgentBackedAdapter` 删除对应方法及其测试，
    删除 `ExecResult`/`ExecJSONResult`/`ExecStreamHandle` 中已无引用者。
  - 若**仍有**其他调用方（如健康检查用 `ContainerExec`）：仅保留在用的，删掉确无引用的；
    在交付说明记录哪些因 spec-A 仍需而保留。
  - **不要**删除 `Adapter` 里 spec-A 仍需的容器生命周期/文件方法。

- [ ] **Step 3: `go build ./... && go test ./...`** → 全绿
- [ ] **Step 4: 提交**

```bash
git add internal/integrations/runtime/  # 精确添加
git commit -m "refactor(runtime): 摘除仅服务 oc-* 的 exec 适配方法

oc-* 全面改走 oc-ops HTTP 后，核对调用方并删除无其他引用的 ContainerExec*
/ExecAttach 及相关结果类型；仍被非 oc-* 能力使用的方法保留，待 spec-A 随
agent 节点概念一并清理（已在交付说明记录）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Phase 9 — 契约文档

### Task 24：oc-ops HTTP 契约文档 + spec-D 对齐说明

**Files:**
- Create: `docs/ocops-http-contract.md`（或放 `runtime/hermes/ocops-contract/SPEC.md`，与现有
  cron/kanban contract 目录风格一致——实现时确认现有契约文档落点后对齐）
- Modify: `docs/superpowers/specs/2026-05-29-spec-e-ocops-http-design.md`（在 §8 标注「契约文档已落地：见 …」）

文档内容：§4.3 的全部端点表（cron/kanban/channel + 2 个 SSE）、错误码→HTTP 状态码映射、
鉴权头、请求/响应类型字段（引用 `ocops` 包 Go 类型与 pod 侧 `ocops` 模块）、
**spec-A 对齐点**：oc-ops 容器 image ref = hermes image ref（`<OC_OPS_IMAGE_REF>` 退化）、
command 覆盖、`OC_OPS_TOKEN` 注入、`OcOpsResolver` 的真实寻址待 spec-A。

- [ ] **Step 1: 写契约文档**
- [ ] **Step 2: 在设计文档 §8 标注契约落点**
- [ ] **Step 3: 提交**

```bash
git add docs/ocops-http-contract.md docs/superpowers/specs/2026-05-29-spec-e-ocops-http-design.md
git commit -m "docs(ocops): 新增 oc-ops HTTP 契约文档并标注 spec-A 对齐点

固化 cron/kanban/channel 类型化 REST + 2 个 SSE 端点、错误码→HTTP 状态码
映射、Bearer 鉴权与请求/响应类型；记录 spec-A 对齐点（oc-ops 镜像 ref=
hermes、command 覆盖、OC_OPS_TOKEN 注入、OcOpsResolver 真实寻址）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## 收尾（全部任务完成后）

- [ ] 跑 pod 侧全量自检：`cd runtime/hermes/hermes-v2026.5.16 && PYTHONPATH=. python -m pytest tests/ -q`
- [ ] 跑后端全量：`go build ./... && go test ./...`
- [ ] 跑 `make openapi-check`（若 handler 签名/路由有变，先 `make openapi-gen && make web-types-gen` 同步并提交——见 AGENTS.md「OpenAPI 同步」）
- [ ] 派最终 code-reviewer 子代理做整体评审
- [ ] **不做**浏览器/集成验证（E4）：在交付说明显式记录「HTTP 通道单测通过，端到端与三角色浏览器验证随 spec-A/B/D 合并后统一执行」

---

## 自检（writing-plans 要求）

**1. spec 覆盖：**
- 设计 §2.1 四块交付 → pod 侧 oc-ops（Task 1-11）、oc-* 重构（Task 2-6）、同镜像打包（Task 12）、manager HTTP 客户端（Task 13-22）、契约文档（Task 24）✅
- §4.3 端点契约（cron 11 / kanban 全 verb / channel / 2 SSE）→ Task 9/10/11（server）+ Task 15/16/17（client）✅
- E1 同镜像 → Task 12；E2 删 exec → Task 19-23；E3 类型化 REST → 全程；E4 仅单测 → 每任务「验证」+ 收尾 ✅
- §5.2 OcOpsResolver 跨 spec 边界 → Task 18（含 spec-A 注释）✅
- §5.4 删除清单 → Task 23（核对调用方再删，不误删 spec-A 所需）✅

**2. 占位扫描：** 无 TBD/TODO。大脚本重构（cron/kanban）用「精确转换规则 + 代表性 worked
example + 待转换函数全集列表」表达，未复制 740/548 行原文——工程师据原文件 + 规则可无歧义完成；
重复性 REST/client 表面用「契约表 + worked example + 每行一个 commit」，每个签名/路径/字段均具体。

**3. 类型一致性：** `ocops.Endpoint`、`ocops.Cron*`/`Kanban*`/`ChannelLoginEvent`、
`service.OcOps`/`OcOpsResolver`/`OcOpsAppLocation`、`ocops.CronCreateReq`/`CronUpdateReq`/
`KanbanCreateReq` 在定义任务（13/14/15/16/17/18）与消费任务（19/20/21/22）间名称一致；
pod 侧 `OpsError`/`code_to_http`、`run_*`/`watch_events`/`channel_login` 在定义（1-6）与
server 消费（8-11）间一致。

**4. 依赖顺序：** Phase 0-6（Python，独立可测）→ Phase 7（Go client，依赖 Task 13 DTO 迁移先行）
→ Phase 8（service 改造，依赖 7）→ Phase 9（文档）。Task 13 必须在 Task 14+ 之前。

---

## 执行交接

Plan complete and saved to `docs/superpowers/plans/2026-05-29-spec-e-ocops-http.md`. Two execution options:

1. **Subagent-Driven (recommended)** - 每个任务派新 subagent，任务间两段评审（spec 合规 + 代码质量），快速迭代
2. **Inline Execution** - 本会话内用 executing-plans 批量执行 + 检查点

Which approach?
