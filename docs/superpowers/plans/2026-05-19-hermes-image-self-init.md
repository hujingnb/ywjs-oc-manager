# Hermes 镜像自包含初始化 · 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 hermes 容器初始化的责任从 manager 迁到镜像内部：manager 只写中性 `manifest.yaml` + `resources/`；镜像内置 `oc-entrypoint` Python 脚本读输入、渲染本镜像版本所需的 hermes 文件并迁移用户数据；最后 `exec hermes gateway run`。

**Architecture:** 节点挂载布局拆为两条独立 bind mount：`apps/<id>/input/` 只读挂到 `/opt/oc-input`，`apps/<id>/data/` 读写挂到 `/opt/data`。`runtime/hermes/` 重组为多 variant 自包含目录（首版 `hermes-main`）。manager 通过 `docker exec` 调一组稳定命令（`oc-info` / `oc-doctor` / `oc-channel-*` / `oc-healthcheck`）与镜像通信。

**Tech Stack:** Go (manager + 节点 agent)，Python 3.13（镜像内 `oc-entrypoint` 及 lib/renderer/migrator/commands；`pyyaml`、`pytest`），Docker（`tini` 仍为 PID 1）。

**Spec reference:** `docs/superpowers/specs/2026-05-19-hermes-image-self-init-design.md`

---

## 文件结构

### 镜像端新增

```
runtime/hermes/
├── README.md                                     # 仓库级说明：变体目录约定、发版流程
└── hermes-main/                                  # 首个 variant
    ├── Dockerfile
    ├── version.txt                               # = main（沿用现 version.txt）
    ├── CONTRACT.md                               # 本 variant 上游 commit 等约束
    ├── oc-entrypoint.py                          # ENTRYPOINT 调度
    ├── oc-doctor.py                              # 诊断快照
    ├── oc-info.py                                # 镜像身份
    ├── oc-channel-login.py                       # 渠道绑定分发命令
    ├── oc-channel-status.py                      # 渠道状态查询
    ├── oc-channel-unbind.py                      # 渠道解绑
    ├── healthcheck.sh                            # docker HEALTHCHECK
    ├── lib/
    │   ├── __init__.py
    │   ├── manifest.py                           # /opt/oc-input/manifest.yaml 解析与校验
    │   ├── state.py                              # /opt/data/.oc-state.json 读写
    │   ├── atomic.py                             # tmp + rename 原子写
    │   └── logging.py                            # stderr 单行 JSON 日志
    ├── renderer/
    │   ├── __init__.py
    │   ├── render_config_yaml.py
    │   ├── render_soul_md.py
    │   ├── render_env.py
    │   └── render_skills.py
    ├── migrator/
    │   └── __init__.py                           # 含 dispatch 入口；首版无任何 from_*.py
    └── tests/
        ├── __init__.py
        ├── conftest.py                           # 临时目录 fixture
        ├── test_manifest.py
        ├── test_state.py
        ├── test_atomic.py
        ├── test_render_config_yaml.py
        ├── test_render_soul_md.py
        ├── test_render_env.py
        ├── test_render_skills.py
        └── test_migrator.py
```

### 镜像端删除

```
runtime/hermes/Dockerfile          → 迁入 hermes-main/Dockerfile
runtime/hermes/version.txt         → 迁入 hermes-main/version.txt
runtime/hermes/CONTRACT.md         → 迁入 hermes-main/CONTRACT.md
runtime/hermes/scripts/healthcheck.sh        → 迁入 hermes-main/healthcheck.sh
runtime/hermes/scripts/oc-weixin-login.py    → 删除（功能由 oc-channel-login.py 取代）
```

### manager 端新增

```
internal/integrations/hermes/manifest.go         # Manifest 结构 + YAML marshal
internal/integrations/hermes/manifest_test.go
internal/integrations/hermes/app_input.go        # WriteAppInput：一次性写 manifest + resources/
internal/integrations/hermes/app_input_test.go
internal/integrations/hermes/commands.go         # docker exec 封装：RunInfo/RunDoctor/RunChannelLogin/...
internal/integrations/hermes/commands_test.go
```

### manager 端删除

```
internal/integrations/hermes/config.go           # RenderConfigYAML / RenderEnv
internal/integrations/hermes/config_test.go
internal/integrations/hermes/skills.go           # RenderKnowledgeSkill / SlugifyKnowledgePath
internal/integrations/hermes/skills_test.go
internal/integrations/hermes/wechat_runner.go    # 由 commands.go 取代
internal/integrations/hermes/wechat_runner_test.go
```

### manager 端改造

```
internal/integrations/hermes/prompt.go           # 保留占位符替换，移除 SOUL.md 拼装
internal/integrations/hermes/prompt_test.go      # 同步收紧
internal/integrations/agent/file_client.go       # UploadAppRuntimeFile → UploadAppInputFile
internal/worker/handlers/app_initialize.go       # writeHermesFiles → WriteAppInput
internal/worker/handlers/app_initialize_test.go
internal/worker/handlers/app_runtime_ops.go      # 删 RefreshConfigYAML 调用
internal/worker/handlers/channel_login.go        # 删 .env 重写；改用 commands.RunChannelLogin
internal/worker/handlers/knowledge_sync.go       # 路径前缀
internal/integrations/runtime/agent_backed.go    # CreateContainer 双挂载 + 移除 OPENAI_* Env
cmd/server/wiring.go                              # 删 hermesConfigRefresher
runtime/agent/scopes.go                          # handleAppInit + 路由前缀
Makefile                                          # 多 variant 支持
```

---

## 任务总览

| # | 任务 | 阶段 | 提交边界 |
|---|---|---|---|
| 1 | 创建 `runtime/hermes/hermes-main/` 骨架 + `lib/` 共享模块 | 镜像 | 1 |
| 2 | 实现 `renderer/render_config_yaml.py` | 镜像 | 1 |
| 3 | 实现 `renderer/render_env.py` | 镜像 | 1 |
| 4 | 实现 `renderer/render_soul_md.py`（含 8 KiB 截断） | 镜像 | 1 |
| 5 | 实现 `renderer/render_skills.py`（含 slug 算法） | 镜像 | 1 |
| 6 | 实现 `migrator/` 框架（首版空 dispatch） | 镜像 | 1 |
| 7 | 实现 `oc-entrypoint.py` 主入口（串联 6 个 phase） | 镜像 | 1 |
| 8 | 实现 `oc-info.py` + `oc-doctor.py` | 镜像 | 1 |
| 9 | 实现 `oc-channel-{login,status,unbind}.py` | 镜像 | 1 |
| 10 | 编写 `Dockerfile` + `healthcheck.sh` + 本地构建验证 | 镜像 | 1 |
| 11 | 改造 `Makefile`，删除旧 `runtime/hermes/` 顶层资产 | 镜像 | 1 |
| 12 | 节点 agent：`handleAppInit` MkdirAll 路径变更 | agent | 1 |
| 13 | 节点 agent：新增 `input/file` 路由、删除 `runtime/file` 路由 | agent | 1 |
| 14 | 节点 agent：`/sessions` 与 workspace 路径前缀指向 `data/` | agent | 1 |
| 15 | manager：新增 `hermes/manifest.go` | manager | 1 |
| 16 | manager：改造 `hermes/prompt.go`（保留占位符替换、移除 SOUL.md 拼装） | manager | 1 |
| 17 | manager：新增 `hermes/app_input.go` | manager | 1 |
| 18 | manager：新增 `hermes/commands.go` | manager | 1 |
| 19 | manager：删除 `hermes/config.go`、`hermes/skills.go`、`hermes/wechat_runner.go` | manager | 1 |
| 20 | manager：`file_client.go` 新增 `UploadAppInputFile`、删除 `UploadAppRuntimeFile` | manager | 1 |
| 21 | manager：改造 `app_initialize.go` `writeHermesFiles → WriteAppInput` | manager | 1 |
| 22 | manager：改造 `agent_backed.go` `CreateContainer`：双挂载 + 移除 `OPENAI_*` Env | manager | 1 |
| 23 | manager：简化 `AppRestartContainerHandler`，删 `wiring.go hermesConfigRefresher` | manager | 1 |
| 24 | manager：改造 `ChannelCheckBindingHandler`，删除 `.env` 重写、改用 commands 包 | manager | 1 |
| 25 | manager：改造 `knowledge_sync.go` 路径前缀 | manager | 1 |
| 26 | audit event `app.runtime_image_changed` | manager | 1 |
| 27 | 本地数据清理 + 端到端浏览器验证 | 验证 | 0 |

---

## 阶段 A · 镜像端 `runtime/hermes/hermes-main/`

### Task 1：创建 variant 骨架 + `lib/` 共享模块

**Files:**
- Create: `runtime/hermes/hermes-main/version.txt`
- Create: `runtime/hermes/hermes-main/CONTRACT.md`
- Create: `runtime/hermes/hermes-main/lib/__init__.py`
- Create: `runtime/hermes/hermes-main/lib/manifest.py`
- Create: `runtime/hermes/hermes-main/lib/state.py`
- Create: `runtime/hermes/hermes-main/lib/atomic.py`
- Create: `runtime/hermes/hermes-main/lib/logging.py`
- Create: `runtime/hermes/hermes-main/tests/__init__.py`
- Create: `runtime/hermes/hermes-main/tests/conftest.py`
- Create: `runtime/hermes/hermes-main/tests/test_manifest.py`
- Create: `runtime/hermes/hermes-main/tests/test_state.py`
- Create: `runtime/hermes/hermes-main/tests/test_atomic.py`

- [ ] **Step 1: 创建 version.txt 与 CONTRACT.md**

```bash
mkdir -p runtime/hermes/hermes-main/{lib,renderer,migrator,tests}
cp runtime/hermes/version.txt runtime/hermes/hermes-main/version.txt   # 内容仍为 "main"
```

写 `runtime/hermes/hermes-main/CONTRACT.md`:

```markdown
# hermes-main · Variant 契约

- 上游仓库: https://github.com/NousResearch/hermes-agent
- 锁定 ref: 见同目录 version.txt
- 安装方式: install.sh + uv (FHS layout，代码装到 /usr/local/lib/hermes-agent/)
- 数据迁移: 首版 hermes-main 没有 from_<prev>.py；未来新增 variant 时新建对应 from_hermes-main.py

# 镜像对外命令
- oc-info / oc-doctor / oc-healthcheck
- oc-channel-login / oc-channel-status / oc-channel-unbind
- ENTRYPOINT: tini -g -- /usr/local/bin/oc-entrypoint
```

- [ ] **Step 2: 写 `lib/atomic.py` 测试（先 fail）**

`runtime/hermes/hermes-main/tests/conftest.py`:

```python
"""测试公共 fixture，提供干净的临时目录。"""

from pathlib import Path
import sys

# 让测试能 import lib / renderer / migrator
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

import pytest


@pytest.fixture
def tmp_data(tmp_path) -> Path:
    """模拟 /opt/data 的临时目录。"""
    return tmp_path / "data"


@pytest.fixture
def tmp_input(tmp_path) -> Path:
    """模拟 /opt/oc-input 的临时目录。"""
    return tmp_path / "input"
```

`runtime/hermes/hermes-main/tests/test_atomic.py`:

```python
"""验证 atomic.write_text 在写入完成前不留半文件，并保证 rename 原子性。"""

from pathlib import Path
from lib.atomic import write_text


def test_atomic_write_creates_file(tmp_path: Path) -> None:
    # 验证正常路径下文件按预期写入。
    target = tmp_path / "config.yaml"
    write_text(target, "hello")
    assert target.read_text() == "hello"


def test_atomic_write_no_residual_tmp(tmp_path: Path) -> None:
    # 验证写完后不留下 .tmp 中间文件。
    target = tmp_path / "config.yaml"
    write_text(target, "hello")
    siblings = list(tmp_path.iterdir())
    assert siblings == [target]
```

- [ ] **Step 3: 运行测试看 fail**

```bash
cd runtime/hermes/hermes-main && python -m pytest tests/test_atomic.py -v
```

Expected: `ModuleNotFoundError: No module named 'lib.atomic'`

- [ ] **Step 4: 实现 `lib/atomic.py`**

`runtime/hermes/hermes-main/lib/atomic.py`:

```python
"""原子写工具：先写临时文件再 rename，保证读者永远看到完整内容。"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Union


def write_text(target: Union[str, Path], content: str) -> None:
    """将 content 写入 target；中间过程不暴露半文件。

    target: 目标路径，父目录必须已存在
    content: 要写入的完整文本
    """
    target_path = Path(target)
    tmp_path = target_path.with_suffix(target_path.suffix + ".tmp")
    with open(tmp_path, "w", encoding="utf-8") as f:
        f.write(content)
        f.flush()
        os.fsync(f.fileno())
    os.replace(tmp_path, target_path)
```

`runtime/hermes/hermes-main/lib/__init__.py` 留空。

- [ ] **Step 5: 运行测试通过**

```bash
cd runtime/hermes/hermes-main && python -m pytest tests/test_atomic.py -v
```

Expected: 2 passed

- [ ] **Step 6: 写 `lib/manifest.py` 测试 + 实现**

`runtime/hermes/hermes-main/tests/test_manifest.py`:

```python
"""验证 manifest.load 校验必填字段、忽略未知字段。"""

from pathlib import Path
import pytest
from lib.manifest import load, ManifestError


def write(p: Path, body: str) -> Path:
    # 写一个 manifest.yaml 测试样本。
    p.write_text(body)
    return p


def test_load_minimal_ok(tmp_path: Path) -> None:
    # 验证最小合法 manifest 能解析出 app.model 与 openai 凭证。
    p = write(tmp_path / "manifest.yaml", """
app:
  id: app-x
  name: x
  model: claude-3.7-sonnet
credentials:
  openai:
    api_key: sk-x
    base_url: http://new-api:3000
resources:
  persona: resources/persona.md
  rules:
    platform: resources/platform-rules.md
    organization: resources/organization-rules.md
    application: resources/application-rules.md
""")
    m = load(p)
    assert m.app_model == "claude-3.7-sonnet"
    assert m.openai_api_key == "sk-x"
    assert m.openai_base_url == "http://new-api:3000"


def test_load_unknown_field_is_ignored(tmp_path: Path) -> None:
    # 验证未知顶层字段不会导致解析失败（forward-compat）。
    p = write(tmp_path / "manifest.yaml", """
app:
  id: app-x
  name: x
  model: m
credentials:
  openai: { api_key: sk-x, base_url: http://x }
resources:
  persona: resources/persona.md
  rules:
    platform: resources/platform-rules.md
    organization: resources/organization-rules.md
    application: resources/application-rules.md
future_field:
  whatever: 1
""")
    m = load(p)
    assert m.app_model == "m"


def test_load_missing_model_raises(tmp_path: Path) -> None:
    # 验证必填字段缺失抛 ManifestError。
    p = write(tmp_path / "manifest.yaml", """
app:
  id: app-x
  name: x
credentials:
  openai: { api_key: sk-x, base_url: http://x }
resources:
  persona: x.md
  rules: { platform: a, organization: b, application: c }
""")
    with pytest.raises(ManifestError):
        load(p)
```

`runtime/hermes/hermes-main/lib/manifest.py`:

```python
"""manager 写入的 manifest.yaml 解析与必填字段校验。

仅暴露 forward-compat 的字段视图：调用方拿到 Manifest 实例后访问命名属性，
未知字段被忽略；缺必填字段则抛 ManifestError，让 oc-entrypoint 直接退出 1。
"""

from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import Any, Union

import yaml


class ManifestError(Exception):
    """manifest 缺失必填字段或解析失败。"""


@dataclass(frozen=True)
class Manifest:
    """业务化视图。字段语义对应 spec §4.2。"""
    app_id: str
    app_name: str
    app_model: str
    openai_api_key: str
    openai_base_url: str
    persona_rel: str
    rule_platform_rel: str
    rule_organization_rel: str
    rule_application_rel: str


def _require(d: dict, *path: str) -> Any:
    """逐层取值；任一层缺失抛 ManifestError，错误信息指明字段路径。"""
    cur: Any = d
    for k in path:
        if not isinstance(cur, dict) or k not in cur:
            raise ManifestError(f"manifest missing field: {'.'.join(path)}")
        cur = cur[k]
    if cur in (None, ""):
        raise ManifestError(f"manifest empty field: {'.'.join(path)}")
    return cur


def load(path: Union[str, Path]) -> Manifest:
    """读 manifest.yaml 并构造 Manifest；未知顶层字段忽略。"""
    raw = yaml.safe_load(Path(path).read_text())
    if not isinstance(raw, dict):
        raise ManifestError("manifest yaml root must be a mapping")
    return Manifest(
        app_id=_require(raw, "app", "id"),
        app_name=_require(raw, "app", "name"),
        app_model=_require(raw, "app", "model"),
        openai_api_key=_require(raw, "credentials", "openai", "api_key"),
        openai_base_url=_require(raw, "credentials", "openai", "base_url"),
        persona_rel=_require(raw, "resources", "persona"),
        rule_platform_rel=_require(raw, "resources", "rules", "platform"),
        rule_organization_rel=_require(raw, "resources", "rules", "organization"),
        rule_application_rel=_require(raw, "resources", "rules", "application"),
    )
```

```bash
cd runtime/hermes/hermes-main && python -m pytest tests/test_manifest.py -v
```

Expected: 3 passed

- [ ] **Step 7: 写 `lib/state.py` 测试 + 实现**

`runtime/hermes/hermes-main/tests/test_state.py`:

```python
"""验证 .oc-state.json 读写：首次启动 prev=None；写后再读得到相同结构。"""

from pathlib import Path
from lib.state import read_state, write_state, OcState


def test_read_missing_returns_empty(tmp_path: Path) -> None:
    # 首次启动场景：.oc-state.json 不存在，应返回 prev_variant=None 的空对象。
    s = read_state(tmp_path)
    assert s.image_variant is None
    assert s.manifest_sha256 is None


def test_write_then_read_roundtrip(tmp_path: Path) -> None:
    # 写入后再读应得到等价的 OcState。
    s = OcState(
        image_variant="hermes-main",
        last_render_at="2026-05-19T00:00:00Z",
        last_migrate_from=None,
        manifest_sha256="ab12cd",
        renderer_outputs=["config.yaml", "SOUL.md"],
    )
    write_state(tmp_path, s)
    s2 = read_state(tmp_path)
    assert s2 == s


def test_corrupt_state_returns_empty(tmp_path: Path) -> None:
    # 文件损坏视为未知，等同首次启动；不应抛异常打断启动流程。
    (tmp_path / ".oc-state.json").write_text("{not json")
    s = read_state(tmp_path)
    assert s.image_variant is None
```

`runtime/hermes/hermes-main/lib/state.py`:

```python
"""/opt/data/.oc-state.json 读写。

.oc-state.json 是镜像私有契约，manager 不读不写；spec §6.3。
未知字段保留，便于未来在不影响老逻辑前提下扩展。
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field, asdict
from pathlib import Path
from typing import List, Optional

STATE_FILE = ".oc-state.json"


@dataclass
class OcState:
    image_variant: Optional[str] = None
    last_render_at: Optional[str] = None
    last_migrate_from: Optional[str] = None
    manifest_sha256: Optional[str] = None
    renderer_outputs: List[str] = field(default_factory=list)


def read_state(data_root: Path) -> OcState:
    """读 .oc-state.json；缺失或损坏视为「首次启动」返回空 OcState。"""
    p = data_root / STATE_FILE
    if not p.exists():
        return OcState()
    try:
        raw = json.loads(p.read_text())
    except (json.JSONDecodeError, OSError):
        return OcState()
    if not isinstance(raw, dict):
        return OcState()
    return OcState(
        image_variant=raw.get("image_variant"),
        last_render_at=raw.get("last_render_at"),
        last_migrate_from=raw.get("last_migrate_from"),
        manifest_sha256=raw.get("manifest_sha256"),
        renderer_outputs=list(raw.get("renderer_outputs") or []),
    )


def write_state(data_root: Path, state: OcState) -> None:
    """以 atomic 写入 .oc-state.json。

    使用 from .atomic 间接依赖，避免与 atomic 模块循环引用。
    """
    from .atomic import write_text
    data_root.mkdir(parents=True, exist_ok=True)
    write_text(data_root / STATE_FILE, json.dumps(asdict(state), ensure_ascii=False, indent=2) + "\n")
```

```bash
cd runtime/hermes/hermes-main && python -m pytest tests/ -v
```

Expected: 8 passed

- [ ] **Step 8: 写 `lib/logging.py`**

无单测（纯薄封装）。`runtime/hermes/hermes-main/lib/logging.py`:

```python
"""stderr 单行 JSON 日志，与 spec §6.4 协议一致。

调用方：emit("render", "info", "render_skills ok", file="abc.md")
输出（stderr）：{"phase":"render","level":"info","msg":"render_skills ok","detail":{"file":"abc.md"}}
"""

from __future__ import annotations

import json
import sys
from typing import Any


def emit(phase: str, level: str, msg: str, **detail: Any) -> None:
    """写一行 JSON 到 stderr。"""
    record = {"phase": phase, "level": level, "msg": msg}
    if detail:
        record["detail"] = detail
    sys.stderr.write(json.dumps(record, ensure_ascii=False) + "\n")
    sys.stderr.flush()
```

- [ ] **Step 9: 提交**

```bash
git add runtime/hermes/hermes-main/
git commit -m "$(cat <<'EOF'
feat(hermes-runtime): 新增 hermes-main variant 骨架与 lib 共享模块

按 spec §5 创建 hermes-main 自包含目录：version.txt 锁定上游 ref，
lib/ 提供 manifest 解析、.oc-state.json 读写、原子写、stderr JSON
日志四个底层工具，配套 pytest 单测全部通过。

为后续 renderer / migrator / oc-entrypoint 主流程铺底。
EOF
)"
```

---

### Task 2：实现 `renderer/render_config_yaml.py`

**Files:**
- Create: `runtime/hermes/hermes-main/renderer/__init__.py`
- Create: `runtime/hermes/hermes-main/renderer/render_config_yaml.py`
- Create: `runtime/hermes/hermes-main/tests/test_render_config_yaml.py`

- [ ] **Step 1: 写测试（先 fail）**

`runtime/hermes/hermes-main/tests/test_render_config_yaml.py`:

```python
"""验证 config.yaml 渲染：model、provider、base_url、api_key、auxiliary、terminal 全部就位。"""

from pathlib import Path
import yaml
from lib.manifest import Manifest
from renderer.render_config_yaml import render


def make_manifest() -> Manifest:
    # 构造一个最小合法 Manifest 给渲染用。
    return Manifest(
        app_id="app-x", app_name="X", app_model="claude-3.7-sonnet",
        openai_api_key="sk-test",
        openai_base_url="http://new-api:3000",
        persona_rel="resources/persona.md",
        rule_platform_rel="resources/platform-rules.md",
        rule_organization_rel="resources/organization-rules.md",
        rule_application_rel="resources/application-rules.md",
    )


def test_render_writes_expected_fields(tmp_data: Path) -> None:
    # 渲染后的 config.yaml 应包含 model.default/provider/base_url/api_key 与 terminal.cwd 等关键字段。
    render(make_manifest(), tmp_data)
    out = yaml.safe_load((tmp_data / "config.yaml").read_text())
    assert out["model"]["default"] == "claude-3.7-sonnet"
    assert out["model"]["provider"] == "custom"
    assert out["model"]["base_url"] == "http://new-api:3000/v1"
    assert out["model"]["api_key"] == "sk-test"
    assert out["terminal"]["cwd"] == "/opt/data/workspace"
    assert out["auxiliary"]["vision"] == {"provider": "main"}


def test_render_is_atomic(tmp_data: Path) -> None:
    # 渲染完成后不应留 .tmp 残留。
    render(make_manifest(), tmp_data)
    leftovers = [p.name for p in tmp_data.iterdir() if p.suffix == ".tmp"]
    assert leftovers == []
```

- [ ] **Step 2: 运行 fail**

```bash
cd runtime/hermes/hermes-main && python -m pytest tests/test_render_config_yaml.py -v
```

Expected: `ModuleNotFoundError: No module named 'renderer.render_config_yaml'`

- [ ] **Step 3: 实现 renderer**

`runtime/hermes/hermes-main/renderer/__init__.py` 留空。
`runtime/hermes/hermes-main/renderer/render_config_yaml.py`:

```python
"""把 manifest 渲染为本 variant 期望的 hermes config.yaml。

字段对照 spec §6.2；base_url 拼接 /v1 由本 variant 决定（manager 写时不带 /v1）。
"""

from __future__ import annotations

from pathlib import Path

from lib.atomic import write_text
from lib.manifest import Manifest

TEMPLATE = """# Hermes 配置 - 由 oc-entrypoint 在容器启动时渲染
# manifest.app.model + manifest.credentials.openai 进 model 段；
# auxiliary 全部 main，避免拨 OpenRouter；terminal.cwd 固定 /opt/data/workspace。

model:
  default: {model!r}
  provider: "custom"
  base_url: {base_url!r}
  api_key: {api_key!r}

auxiliary:
  vision:         {{ provider: main }}
  compression:    {{ provider: main }}
  web_extract:    {{ provider: main }}
  session_search: {{ provider: main }}

memory:
  memory_enabled: true
  user_profile_enabled: true
  memory_char_limit: 2200
  user_char_limit: 1375

terminal:
  backend: "local"
  cwd: "/opt/data/workspace"
  timeout: 180
  lifetime_seconds: 300
"""


def render(m: Manifest, data_root: Path) -> str:
    """渲染 config.yaml 到 data_root/config.yaml，返回相对路径。"""
    data_root.mkdir(parents=True, exist_ok=True)
    body = TEMPLATE.format(
        model=m.app_model,
        base_url=m.openai_base_url.rstrip("/") + "/v1",
        api_key=m.openai_api_key,
    )
    write_text(data_root / "config.yaml", body)
    return "config.yaml"
```

- [ ] **Step 4: 测试通过**

```bash
cd runtime/hermes/hermes-main && python -m pytest tests/test_render_config_yaml.py -v
```

Expected: 2 passed

- [ ] **Step 5: 提交**

```bash
git add runtime/hermes/hermes-main/renderer/__init__.py \
        runtime/hermes/hermes-main/renderer/render_config_yaml.py \
        runtime/hermes/hermes-main/tests/test_render_config_yaml.py
git commit -m "feat(hermes-runtime): hermes-main renderer 渲染 config.yaml

按 manifest 中 app.model 与 credentials.openai 渲染 hermes config.yaml；
auxiliary / memory / terminal 段沿用历史默认值。base_url 末尾拼 /v1 由
本 variant 决定，manager 写入时不带后缀。"
```

---

### Task 3：实现 `renderer/render_env.py`

**Files:**
- Create: `runtime/hermes/hermes-main/renderer/render_env.py`
- Create: `runtime/hermes/hermes-main/tests/test_render_env.py`

- [ ] **Step 1: 写测试**

`runtime/hermes/hermes-main/tests/test_render_env.py`:

```python
"""验证 .env 仅写「行为开关」，不写 OPENAI_*（后者已进 config.yaml）。"""

from pathlib import Path
from renderer.render_env import render


def test_render_env_writes_behavior_flags(tmp_data: Path) -> None:
    # 验证 .env 含 GATEWAY_ALLOW_ALL_USERS 与 WEIXIN_DM_POLICY；
    # 不应再有 OPENAI_API_KEY / OPENAI_BASE_URL（这些在 config.yaml）。
    render(tmp_data)
    body = (tmp_data / ".env").read_text()
    assert "GATEWAY_ALLOW_ALL_USERS=true" in body
    assert "WEIXIN_DM_POLICY=open" in body
    assert "OPENAI_API_KEY" not in body
    assert "OPENAI_BASE_URL" not in body
```

- [ ] **Step 2: 运行 fail**

```bash
cd runtime/hermes/hermes-main && python -m pytest tests/test_render_env.py -v
```

- [ ] **Step 3: 实现**

`runtime/hermes/hermes-main/renderer/render_env.py`:

```python
"""渲染 .env 文件。

本 variant 内 .env 仅承载「行为开关」：hermes 进程从 .env 读这些 ENV，
manifest.credentials.openai 通过 config.yaml 落地，不重复进 .env。
微信凭证由 hermes 自管，oc-channel-login 自行写入；本 renderer 不涉及。
"""

from __future__ import annotations

from pathlib import Path

from lib.atomic import write_text

BODY = """# Hermes 行为开关 - 由 oc-entrypoint 渲染

# 绕过 Hermes user pairing 流程（本地部署无交互 CLI 跑 approve）
GATEWAY_ALLOW_ALL_USERS=true

# Weixin platform policy：必须显式 open，否则未授权 DM 一律拒
WEIXIN_DM_POLICY=open
"""


def render(data_root: Path) -> str:
    data_root.mkdir(parents=True, exist_ok=True)
    write_text(data_root / ".env", BODY)
    return ".env"
```

- [ ] **Step 4: 测试通过 + 提交**

```bash
cd runtime/hermes/hermes-main && python -m pytest tests/test_render_env.py -v
git add runtime/hermes/hermes-main/renderer/render_env.py \
        runtime/hermes/hermes-main/tests/test_render_env.py
git commit -m "feat(hermes-runtime): hermes-main renderer 渲染 .env 行为开关

.env 不再承载 OPENAI_* 凭证（已迁到 config.yaml），仅保留
GATEWAY_ALLOW_ALL_USERS 与 WEIXIN_DM_POLICY 两个固定行为开关。
微信凭证由 hermes 自管。"
```

---

### Task 4：实现 `renderer/render_soul_md.py`（含 8 KiB 截断）

**Files:**
- Create: `runtime/hermes/hermes-main/renderer/render_soul_md.py`
- Create: `runtime/hermes/hermes-main/tests/test_render_soul_md.py`

- [ ] **Step 1: 写测试**

```python
"""验证 SOUL.md：三层 rules 顺序、persona 拼接、知识库 inline 截断、空层跳过。"""

from pathlib import Path
from lib.manifest import Manifest
from renderer.render_soul_md import render


def make_manifest() -> Manifest:
    return Manifest(
        app_id="x", app_name="X", app_model="m",
        openai_api_key="sk", openai_base_url="http://x",
        persona_rel="resources/persona.md",
        rule_platform_rel="resources/platform-rules.md",
        rule_organization_rel="resources/organization-rules.md",
        rule_application_rel="resources/application-rules.md",
    )


def _setup(input_root: Path, *, persona="", platform="", org="", app="", kb_org=None, kb_app=None) -> None:
    # 准备 input 目录下的 resources 文件；空字符串表示该层不存在。
    res = input_root / "resources"
    res.mkdir(parents=True, exist_ok=True)
    (res / "persona.md").write_text(persona)
    (res / "platform-rules.md").write_text(platform)
    (res / "organization-rules.md").write_text(org)
    (res / "application-rules.md").write_text(app)
    if kb_org:
        for rel, body in kb_org.items():
            f = res / "knowledge" / "org" / rel
            f.parent.mkdir(parents=True, exist_ok=True)
            f.write_text(body)
    if kb_app:
        for rel, body in kb_app.items():
            f = res / "knowledge" / "app" / rel
            f.parent.mkdir(parents=True, exist_ok=True)
            f.write_text(body)


def test_three_layers_in_order(tmp_input: Path, tmp_data: Path) -> None:
    # 验证渲染顺序：persona → platform → org → app。
    _setup(tmp_input, persona="P body", platform="PLT", org="ORG", app="APP")
    render(make_manifest(), tmp_input, tmp_data)
    soul = (tmp_data / "SOUL.md").read_text()
    assert soul.index("P body") < soul.index("PLT") < soul.index("ORG") < soul.index("APP")


def test_empty_layer_skipped(tmp_input: Path, tmp_data: Path) -> None:
    # 验证某一层为空时，对应 section 不出现。
    _setup(tmp_input, persona="P", platform="", org="ORG", app="APP")
    render(make_manifest(), tmp_input, tmp_data)
    soul = (tmp_data / "SOUL.md").read_text()
    assert "平台层" not in soul
    assert "组织层" in soul


def test_knowledge_inline_truncated_at_8kib(tmp_input: Path, tmp_data: Path) -> None:
    # 验证单个知识库文件超过 8 KiB 被截断，并附带 "完整版见 skills/kb-*" 提示。
    big = "A" * 9000
    _setup(tmp_input, persona="P", platform="", org="", app="", kb_org={"big.md": big})
    render(make_manifest(), tmp_input, tmp_data)
    soul = (tmp_data / "SOUL.md").read_text()
    assert "AAAA" in soul
    assert "完整版见" in soul or "skills/kb-" in soul
    # 整段 inline 不应包含全部 9000 字符（被截断到 8 KiB）。
    assert soul.count("A") < 9000
```

- [ ] **Step 2: 实现**

`runtime/hermes/hermes-main/renderer/render_soul_md.py`:

```python
"""渲染 SOUL.md。

结构（与 spec §6.2 一致）：
1. 固定 header（语言要求）
2. persona 段
3. 三层 rules：## 平台层 / ## 组织层 / ## 应用层；空层跳过
4. 知识库 always-on inline：应用级在前、组织级在后，单文件 > 8 KiB 截断并提示完整版位置

manager 端 prompt 占位符已替换完毕，本 renderer 只做拼装。
"""

from __future__ import annotations

from pathlib import Path
from typing import Iterable

from lib.atomic import write_text
from lib.manifest import Manifest

HEADER = (
    "# Agent Identity (SOUL.md)\n\n"
    "本文件由 oc-entrypoint 在容器启动时生成，Hermes 启动后注入 system prompt。\n\n"
    "## 语言要求\n\n"
    "始终用简体中文回复用户。即使用户用英文或其他语言提问，也请用中文作答\n"
    "（代码、命令、API 名称、错误码等技术标识保留英文原文）。\n\n"
)

INLINE_LIMIT = 8 * 1024  # 单知识库文件 inline 上限 8 KiB


def render(m: Manifest, input_root: Path, data_root: Path) -> str:
    data_root.mkdir(parents=True, exist_ok=True)
    parts: list[str] = [HEADER]

    persona = (input_root / m.persona_rel).read_text() if (input_root / m.persona_rel).exists() else ""
    if persona.strip():
        parts.append(persona.rstrip() + "\n\n")

    for title, rel in (
        ("平台层", m.rule_platform_rel),
        ("组织层", m.rule_organization_rel),
        ("应用层", m.rule_application_rel),
    ):
        path = input_root / rel
        if not path.exists():
            continue
        body = path.read_text().strip()
        if not body:
            continue
        parts.append(f"## {title}\n\n{body}\n\n")

    inline = _collect_inline(input_root)
    if inline:
        parts.append("## 知识库（always-on 摘要）\n\n")
        parts.extend(inline)

    write_text(data_root / "SOUL.md", "".join(parts))
    return "SOUL.md"


def _collect_inline(input_root: Path) -> list[str]:
    """按「应用级在前、组织级在后」顺序读取 knowledge/ 主副本树，单文件 8 KiB 截断。"""
    res = input_root / "resources" / "knowledge"
    chunks: list[str] = []
    for scope, label in (("app", "应用级"), ("org", "组织级")):
        base = res / scope
        if not base.exists():
            continue
        for f in sorted(base.rglob("*.md")):
            rel = f.relative_to(base).as_posix()
            body = f.read_text()
            slug = _slug(rel)
            chunks.append(f"### [{label}] {rel}\n\n")
            if len(body) > INLINE_LIMIT:
                chunks.append(body[:INLINE_LIMIT])
                chunks.append(f"\n\n> （内容已截断；完整版见 skills/kb-{scope}-{slug}/SKILL.md）\n\n")
            else:
                chunks.append(body)
                chunks.append("\n\n")
    return chunks


def _slug(rel: str) -> str:
    """与 render_skills 共用的 slug 算法的简化版：仅用于截断提示文案。"""
    from renderer.render_skills import slugify_knowledge_path
    return slugify_knowledge_path(rel)
```

- [ ] **Step 3: 测试通过 + 提交**

```bash
cd runtime/hermes/hermes-main && python -m pytest tests/test_render_soul_md.py -v
git add runtime/hermes/hermes-main/renderer/render_soul_md.py \
        runtime/hermes/hermes-main/tests/test_render_soul_md.py
git commit -m "feat(hermes-runtime): hermes-main renderer 渲染 SOUL.md

按 spec §6.2：固定 header → persona → 三层 rules → 知识库 inline。
单个知识库文件 inline 上限 8 KiB，超出截断并提示完整版路径
skills/kb-{scope}-{slug}/SKILL.md。空层跳过。"
```

---

### Task 5：实现 `renderer/render_skills.py`（含 slug 算法）

**Files:**
- Create: `runtime/hermes/hermes-main/renderer/render_skills.py`
- Create: `runtime/hermes/hermes-main/tests/test_render_skills.py`

- [ ] **Step 1: 写测试**

```python
"""验证 render_skills：扫 input/resources/knowledge/{org,app}/，slug 算法稳定。"""

import re
from pathlib import Path
from renderer.render_skills import render, slugify_knowledge_path


def test_slug_ascii(tmp_path) -> None:
    # 常规 ASCII 路径生成可读 slug。
    assert slugify_knowledge_path("policies/refund.md") == "policies-refund"
    assert slugify_knowledge_path("Tone.MD") == "tone"


def test_slug_non_ascii_falls_back_to_hash(tmp_path) -> None:
    # 含中文的路径回落到 kb-<sha256[:12]> 固定后缀。
    slug = slugify_knowledge_path("退款政策.md")
    assert re.match(r"^kb-[0-9a-f]{12}$", slug)


def test_render_creates_one_dir_per_file(tmp_input: Path, tmp_data: Path) -> None:
    # 每个 knowledge 文件生成一份 skills/kb-<scope>-<slug>/SKILL.md。
    (tmp_input / "resources" / "knowledge" / "org" / "policies").mkdir(parents=True)
    (tmp_input / "resources" / "knowledge" / "org" / "policies" / "refund.md").write_text("# Refund\n\nbody")
    (tmp_input / "resources" / "knowledge" / "app").mkdir(parents=True)
    (tmp_input / "resources" / "knowledge" / "app" / "tone.md").write_text("# Tone\n\nbody")

    outputs = render(tmp_input, tmp_data)

    assert (tmp_data / "skills" / "kb-org-policies-refund" / "SKILL.md").exists()
    assert (tmp_data / "skills" / "kb-app-tone" / "SKILL.md").exists()
    assert set(outputs) == {
        "skills/kb-org-policies-refund/SKILL.md",
        "skills/kb-app-tone/SKILL.md",
    }
```

- [ ] **Step 2: 实现**

`runtime/hermes/hermes-main/renderer/render_skills.py`:

```python
"""扫 input/resources/knowledge/{org,app}/* 生成 skills/kb-{scope}-{slug}/SKILL.md。

算法搬运自旧 manager 端 hermes/skills.go 的 SlugifyKnowledgePath，
保持对同一文件路径生成相同 slug。
"""

from __future__ import annotations

import hashlib
import re
from pathlib import Path
from typing import List

from lib.atomic import write_text

SLUG_PATTERN = re.compile(r"^[a-z0-9]+(-[a-z0-9]+)*$")


def slugify_knowledge_path(rel: str) -> str:
    """规范化为 slugPattern；纯非 ASCII 路径回落到 sha256 短哈希。"""
    if not rel:
        return _fallback(rel)
    base = rel.rsplit(".", 1)[0] if "." in rel.rsplit("/", 1)[-1] else rel
    chars: list[str] = []
    for c in base:
        if "a" <= c <= "z" or "0" <= c <= "9":
            chars.append(c)
        elif "A" <= c <= "Z":
            chars.append(c.lower())
        else:
            chars.append("-")
    s = "".join(chars)
    while "--" in s:
        s = s.replace("--", "-")
    s = s.strip("-")
    if not s or not SLUG_PATTERN.match(s):
        return _fallback(rel)
    return s


def _fallback(rel: str) -> str:
    h = hashlib.sha256(rel.encode()).hexdigest()
    return f"kb-{h[:12]}"


def render(input_root: Path, data_root: Path) -> List[str]:
    """生成每个知识库文件对应的 SKILL.md，返回写入相对路径列表。"""
    skills_root = data_root / "skills"
    outputs: list[str] = []
    for scope in ("org", "app"):
        base = input_root / "resources" / "knowledge" / scope
        if not base.exists():
            continue
        for f in sorted(base.rglob("*.md")):
            rel = f.relative_to(base).as_posix()
            slug = slugify_knowledge_path(rel)
            dir_name = f"kb-{scope}-{slug}"
            target_dir = skills_root / dir_name
            target_dir.mkdir(parents=True, exist_ok=True)
            body = _render_skill_md(scope, dir_name, rel, f.read_text())
            write_text(target_dir / "SKILL.md", body)
            outputs.append(f"skills/{dir_name}/SKILL.md")
    return outputs


def _render_skill_md(scope: str, dir_name: str, rel: str, body: str) -> str:
    # 沿用旧 manager 端 hermes/skills.go 的 frontmatter + body 模板。
    title = _extract_heading(body) or rel
    if scope == "org":
        desc = (
            f"组织级知识库文件 {title}。介绍本组织业务、产品、政策、规则等权威信息。"
            "当用户的提问涉及组织业务、公司、产品、规则、政策、流程时，必须读取本 skill 获取最新内容，"
            "不要根据通用知识猜测。"
        )
    else:
        desc = (
            f"应用级知识库文件 {title}。包含本应用专属规则、话术、配置，优先级高于同名组织级知识。"
            "用户的任意提问都应先读取本 skill 确认是否有匹配规则；有则按本 skill 内容回答，"
            "无则回退到组织级或通用知识。"
        )
    return f"""---
name: {dir_name}
description: {desc}
scope: {scope}
---

# {title}

{body}
"""


def _extract_heading(body: str) -> str:
    for line in body.splitlines():
        s = line.strip()
        if s.startswith("#"):
            return s.lstrip("#").strip()
    return ""
```

- [ ] **Step 3: 测试通过 + 提交**

```bash
cd runtime/hermes/hermes-main && python -m pytest tests/test_render_skills.py -v
git add runtime/hermes/hermes-main/renderer/render_skills.py \
        runtime/hermes/hermes-main/tests/test_render_skills.py
git commit -m "feat(hermes-runtime): hermes-main renderer 生成 kb-* SKILL.md

扫 input/resources/knowledge/{org,app}/* 递归生成
skills/kb-<scope>-<slug>/SKILL.md。slug 算法搬运自旧 manager
hermes/skills.go SlugifyKnowledgePath：ASCII 转小写连字符，
含非 ASCII 字符 fallback 到 sha256[:12]。"
```

---

### Task 6：实现 `migrator/` 框架

**Files:**
- Create: `runtime/hermes/hermes-main/migrator/__init__.py`
- Create: `runtime/hermes/hermes-main/tests/test_migrator.py`

- [ ] **Step 1: 写测试**

```python
"""验证 migrator dispatch：未知 prev_variant → 静默跳过；找到 from_X.py → 调其 run()。"""

from pathlib import Path
from migrator import run as run_migration


def test_no_prev_skips(tmp_data: Path) -> None:
    # 首次启动 prev=None，应直接返回 None 不抛。
    result = run_migration(prev_variant=None, curr_variant="hermes-main", data_root=tmp_data)
    assert result is None


def test_same_variant_skips(tmp_data: Path) -> None:
    # prev == curr，跳过迁移。
    result = run_migration(prev_variant="hermes-main", curr_variant="hermes-main", data_root=tmp_data)
    assert result is None


def test_unknown_prev_raises(tmp_data: Path) -> None:
    # 切到未实现 from_<prev>.py 的迁移路径应抛 NotImplementedError，
    # 由 oc-entrypoint 转化为退出码 1。
    import pytest
    with pytest.raises(NotImplementedError):
        run_migration(prev_variant="hermes-experimental", curr_variant="hermes-main", data_root=tmp_data)
```

- [ ] **Step 2: 实现**

`runtime/hermes/hermes-main/migrator/__init__.py`:

```python
"""跨 variant 数据迁移 dispatch。

约定：from_<prev_variant>.py 暴露 run(data_root: Path) -> dict 函数；
本 variant（hermes-main）首版无任何 from_*.py，所以遇到任何已知 prev 都抛。
未来新 variant（如 hermes-v0.5）需新增 from_hermes-main.py 等模块。
"""

from __future__ import annotations

import importlib
from pathlib import Path
from typing import Optional


def run(prev_variant: Optional[str], curr_variant: str, data_root: Path) -> Optional[dict]:
    """根据 prev/curr variant 决定是否需要迁移。

    返回 None 表示跳过迁移；非 None 返回迁移摘要（写入 .oc-state.last_migrate_from）。
    迁移失败抛异常，调用方（oc-entrypoint）退出码 1，并保证 data_root 已被 migrator 原子处理。
    """
    if prev_variant is None or prev_variant == curr_variant:
        return None
    module_name = f"migrator.from_{prev_variant.replace('-', '_')}"
    try:
        mod = importlib.import_module(module_name)
    except ModuleNotFoundError as e:
        raise NotImplementedError(
            f"no migrator path from {prev_variant} → {curr_variant}; "
            f"please ship a {module_name} module"
        ) from e
    return mod.run(data_root)
```

- [ ] **Step 3: 测试通过 + 提交**

```bash
cd runtime/hermes/hermes-main && python -m pytest tests/test_migrator.py -v
git add runtime/hermes/hermes-main/migrator/__init__.py \
        runtime/hermes/hermes-main/tests/test_migrator.py
git commit -m "feat(hermes-runtime): hermes-main migrator dispatch 框架

首版 hermes-main 不持有任何 from_<prev>.py 模块，prev=None 或同 variant
直接 skip；遇到陌生 prev_variant 抛 NotImplementedError 让 oc-entrypoint
退出码 1。未来 variant 新增对应 from_*.py 模块即可挂载。"
```

---

### Task 7：实现 `oc-entrypoint.py` 主入口

**Files:**
- Create: `runtime/hermes/hermes-main/oc-entrypoint.py`
- Create: `runtime/hermes/hermes-main/tests/test_entrypoint_integration.py`

- [ ] **Step 1: 写集成测试**

```python
"""端到端：从一份完整 manifest + resources 出发，跑 oc-entrypoint 主流程到 phase 5。

oc-entrypoint phase 6 是 os.execvp 替换进程，不能在 pytest 中直接验证；
集成测试用 OC_TEST_NO_EXEC=1 让 oc-entrypoint 跳过 exec 直接退出 0。
"""

import json
import os
import subprocess
import sys
from pathlib import Path


def _setup_input(input_root: Path) -> None:
    # 准备一份最小可用的 manifest + resources。
    (input_root / "resources").mkdir(parents=True)
    (input_root / "resources" / "persona.md").write_text("# Persona\n\nP")
    (input_root / "resources" / "platform-rules.md").write_text("PLT")
    (input_root / "resources" / "organization-rules.md").write_text("ORG")
    (input_root / "resources" / "application-rules.md").write_text("APP")
    (input_root / "manifest.yaml").write_text("""
app: { id: x, name: X, model: m }
credentials:
  openai: { api_key: sk-x, base_url: http://x }
resources:
  persona: resources/persona.md
  rules:
    platform: resources/platform-rules.md
    organization: resources/organization-rules.md
    application: resources/application-rules.md
""")


def test_entrypoint_first_boot(tmp_path: Path) -> None:
    input_root = tmp_path / "input"
    data_root = tmp_path / "data"
    _setup_input(input_root)

    env = {
        **os.environ,
        "OC_TEST_NO_EXEC": "1",
        "OC_INPUT_DIR": str(input_root),
        "OC_DATA_DIR": str(data_root),
        "OC_IMAGE_VARIANT": "hermes-main",
    }
    script = Path(__file__).resolve().parent.parent / "oc-entrypoint.py"
    r = subprocess.run([sys.executable, str(script)], env=env, capture_output=True, text=True)
    assert r.returncode == 0, r.stderr

    # phase 4 产物
    assert (data_root / "config.yaml").exists()
    assert (data_root / "SOUL.md").exists()
    assert (data_root / ".env").exists()

    # phase 5 写下 .oc-state.json
    state = json.loads((data_root / ".oc-state.json").read_text())
    assert state["image_variant"] == "hermes-main"
    assert state["last_migrate_from"] is None
```

- [ ] **Step 2: 实现 `oc-entrypoint.py`**

```python
#!/usr/bin/env python3
"""hermes-main ENTRYPOINT。

phase 1 load manifest → 2 load state → 3 migrate → 4 render → 5 commit state → 6 exec hermes。
任何 phase 失败统一退出 1；详细错误通过 lib.logging.emit 写 stderr JSON。

测试模式：OC_TEST_NO_EXEC=1 时 phase 6 跳过 execvp 直接退出 0。
"""

from __future__ import annotations

import datetime as _dt
import hashlib
import os
import sys
from pathlib import Path

# 让 import lib / renderer / migrator 走包内路径。
sys.path.insert(0, "/usr/local/lib/oc-entrypoint")
# 测试模式：脚本目录而非镜像安装目录。
if not Path("/usr/local/lib/oc-entrypoint").exists():
    sys.path.insert(0, str(Path(__file__).resolve().parent))

from lib import logging as oclog
from lib.atomic import write_text  # noqa: F401  (atomic 在 renderer 内部使用)
from lib.manifest import load as load_manifest, ManifestError
from lib.state import OcState, read_state, write_state
from renderer import render_config_yaml, render_env, render_skills, render_soul_md
from migrator import run as run_migration


def main() -> int:
    input_root = Path(os.environ.get("OC_INPUT_DIR", "/opt/oc-input"))
    data_root = Path(os.environ.get("OC_DATA_DIR", "/opt/data"))
    curr_variant = os.environ.get("OC_IMAGE_VARIANT", "unknown")

    # phase 1 load manifest
    try:
        manifest = load_manifest(input_root / "manifest.yaml")
    except (ManifestError, FileNotFoundError, OSError) as e:
        oclog.emit("load_manifest", "error", str(e))
        return 1

    # phase 2 load state
    state = read_state(data_root)
    prev_variant = state.image_variant

    # phase 3 migrate
    migrate_from = None
    try:
        if run_migration(prev_variant, curr_variant, data_root) is not None:
            migrate_from = prev_variant
    except Exception as e:  # noqa: BLE001
        oclog.emit("migrate", "error", str(e), prev_variant=prev_variant, curr_variant=curr_variant)
        return 1

    # phase 4 render（每次都跑、幂等）
    outputs: list[str] = []
    try:
        outputs.append(render_config_yaml.render(manifest, data_root))
        outputs.append(render_env.render(data_root))
        outputs.append(render_soul_md.render(manifest, input_root, data_root))
        outputs.extend(render_skills.render(input_root, data_root))
    except Exception as e:  # noqa: BLE001
        oclog.emit("render", "error", str(e))
        return 1

    # phase 5 commit state
    state_to_write = OcState(
        image_variant=curr_variant,
        last_render_at=_dt.datetime.utcnow().replace(microsecond=0).isoformat() + "Z",
        last_migrate_from=migrate_from,
        manifest_sha256=_sha256((input_root / "manifest.yaml").read_bytes()),
        renderer_outputs=outputs,
    )
    try:
        write_state(data_root, state_to_write)
    except OSError as e:
        # state 写失败不阻断；下次启动按首次处理。
        oclog.emit("commit_state", "warn", str(e))

    # phase 6 exec hermes
    if os.environ.get("OC_TEST_NO_EXEC") == "1":
        return 0
    os.execvp("hermes", ["hermes", "gateway", "run"])
    return 1  # pragma: no cover


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 3: 测试通过 + 提交**

```bash
cd runtime/hermes/hermes-main && python -m pytest tests/ -v
chmod +x runtime/hermes/hermes-main/oc-entrypoint.py
git add runtime/hermes/hermes-main/oc-entrypoint.py \
        runtime/hermes/hermes-main/tests/test_entrypoint_integration.py
git commit -m "feat(hermes-runtime): hermes-main oc-entrypoint 主入口

串联 6 个 phase：load manifest → load state → migrate → render → commit
state → exec hermes。每个 phase 失败统一退出码 1，详细信息走 stderr
JSON。OC_TEST_NO_EXEC=1 支持在 pytest 中验证 phase 1~5 而不真正
execvp，方便单测覆盖。"
```

---

### Task 8：实现 `oc-info.py` + `oc-doctor.py`

**Files:**
- Create: `runtime/hermes/hermes-main/oc-info.py`
- Create: `runtime/hermes/hermes-main/oc-doctor.py`

- [ ] **Step 1: 实现 `oc-info.py`**

```python
#!/usr/bin/env python3
"""输出镜像身份。stdout 单行 JSON；spec §7。

读取 build 阶段写入的 /etc/oc-image.json。
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path


def main() -> int:
    info_path = Path(os.environ.get("OC_INFO_FILE", "/etc/oc-image.json"))
    try:
        raw = json.loads(info_path.read_text())
    except (OSError, json.JSONDecodeError) as e:
        sys.stderr.write(json.dumps({"phase": "oc-info", "level": "error", "msg": str(e)}) + "\n")
        return 1
    raw["oc_entrypoint_version"] = "1"
    sys.stdout.write(json.dumps(raw, ensure_ascii=False) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 2: 实现 `oc-doctor.py`**

```python
#!/usr/bin/env python3
"""输出诊断快照。stdout 单行 JSON；spec §7。"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, "/usr/local/lib/oc-entrypoint")
if not Path("/usr/local/lib/oc-entrypoint").exists():
    sys.path.insert(0, str(Path(__file__).resolve().parent))

from lib.state import read_state


def main() -> int:
    data_root = Path(os.environ.get("OC_DATA_DIR", "/opt/data"))
    variant = os.environ.get("OC_IMAGE_VARIANT", "unknown")
    state = read_state(data_root)

    hermes_pid = None
    hermes_status = "unknown"
    try:
        out = subprocess.run(["pgrep", "-f", "hermes gateway"], capture_output=True, text=True, timeout=5)
        if out.returncode == 0 and out.stdout.strip():
            hermes_pid = int(out.stdout.splitlines()[0])
            hermes_status = "running"
        else:
            hermes_status = "stopped"
    except (FileNotFoundError, subprocess.TimeoutExpired):
        hermes_status = "unknown"

    snapshot = {
        "variant": variant,
        "last_render_at": state.last_render_at,
        "manifest_sha256": state.manifest_sha256,
        "hermes_pid": hermes_pid,
        "hermes_status": hermes_status,
        "issues": [],
    }
    sys.stdout.write(json.dumps(snapshot, ensure_ascii=False) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 3: 提交**

```bash
chmod +x runtime/hermes/hermes-main/oc-info.py runtime/hermes/hermes-main/oc-doctor.py
git add runtime/hermes/hermes-main/oc-info.py runtime/hermes/hermes-main/oc-doctor.py
git commit -m "feat(hermes-runtime): hermes-main oc-info / oc-doctor 命令

oc-info 读 /etc/oc-image.json 输出镜像 variant / 上游 ref / 构建时间；
oc-doctor 输出 last_render_at / manifest_sha256 / hermes 进程状态等
诊断信息。stdout 都是单行 JSON。"
```

---

### Task 9：实现 `oc-channel-{login,status,unbind}.py`

**Files:**
- Create: `runtime/hermes/hermes-main/oc-channel-login.py`
- Create: `runtime/hermes/hermes-main/oc-channel-status.py`
- Create: `runtime/hermes/hermes-main/oc-channel-unbind.py`

- [ ] **Step 1: 实现 `oc-channel-login.py`**

```python
#!/usr/bin/env python3
"""触发渠道绑定。stdout 结束态 JSON；stderr 中间事件 JSON（含二维码 URL）。

调用形式：oc-channel-login --channel weixin
按 --channel 分发到具体实现；首版仅支持 weixin。
"""

from __future__ import annotations

import argparse
import json
import sys


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--channel", required=True)
    args = p.parse_args()

    if args.channel == "weixin":
        return _weixin_login()
    sys.stdout.write(json.dumps({"status": "failed", "reason": f"unknown channel: {args.channel}"}) + "\n")
    return 1


def _weixin_login() -> int:
    """微信扫码登录。

    实现细节：调用 hermes 上游 SDK 启 sse 长轮询；扫码成功后把 token
    写入 hermes 自有的 /opt/data/weixin/accounts/。stderr 中间事件
    {"event":"qrcode","url":"..."}。失败/超时 stdout {"status":"failed|timeout"}。

    注：本步骤的具体上游 SDK 调用代码可沿用现 runtime/hermes/scripts/oc-weixin-login.py
    的实现并改输出协议；执行时复制后改造 stdout JSON 与凭证落盘位置。
    """
    # 沿用旧脚本主流程；改造输出：
    # - stdout 不再输出 account_id/token JSON，仅输出 {"status":"bound"|"failed"|"timeout"}
    # - 凭证写入 /opt/data/weixin/accounts/ 由 hermes 自管
    # 这里给一个 stub 占位，实际实现需移植 oc-weixin-login.py 中的 hermes SDK 调用代码。
    sys.stderr.write(json.dumps({"event": "todo", "msg": "port oc-weixin-login.py here"}) + "\n")
    sys.stdout.write(json.dumps({"status": "failed", "reason": "not implemented"}) + "\n")
    return 1


if __name__ == "__main__":
    sys.exit(main())
```

> 注：完整移植旧 `oc-weixin-login.py` 的扫码逻辑需要根据 hermes 上游 SDK 当前的 API 实测。本 Step 先提交 stub，让 manager 端集成层先跑通；移植扫码细节在 Task 9 内部的子步骤完成。

- [ ] **Step 2: 完成 `_weixin_login` 移植**

将 `runtime/hermes/scripts/oc-weixin-login.py` 内的 hermes SDK 长轮询调用代码移到 `_weixin_login`，并按以下契约改造输出：

- stdout：只在最终态写一条 `{"status":"bound"|"failed"|"timeout","reason":"..."}`；
- stderr：每个中间事件写一行 `{"event":"qrcode","url":"..."}` / `{"event":"polling"}`；
- 凭证落盘到 `/opt/data/weixin/accounts/`（沿用 hermes 上游约定）；
- 扫码成功后调 hermes 进程 reload（若上游 SDK 暴露）；否则不做处理，由 manager 端触发 restart。

完整代码以现有 `oc-weixin-login.py` 实现为基础，每行替换写文件与 stdout 输出即可。

- [ ] **Step 3: 实现 `oc-channel-status.py`**

```python
#!/usr/bin/env python3
"""查询渠道绑定状态。stdout 单行 JSON。"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--channel", required=True)
    args = p.parse_args()

    data_root = Path(os.environ.get("OC_DATA_DIR", "/opt/data"))

    if args.channel == "weixin":
        return _weixin_status(data_root)
    sys.stdout.write(json.dumps({"channel": args.channel, "bound": False, "reason": "unknown channel"}) + "\n")
    return 1


def _weixin_status(data_root: Path) -> int:
    accounts_dir = data_root / "weixin" / "accounts"
    if not accounts_dir.exists():
        sys.stdout.write(json.dumps({"channel": "weixin", "bound": False}) + "\n")
        return 0
    # 读 hermes 自管的账号目录里第一个有效条目作为绑定状态。
    for entry in accounts_dir.iterdir():
        if entry.is_dir():
            sys.stdout.write(json.dumps({"channel": "weixin", "bound": True, "account_id": entry.name}) + "\n")
            return 0
    sys.stdout.write(json.dumps({"channel": "weixin", "bound": False}) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 4: 实现 `oc-channel-unbind.py`**

```python
#!/usr/bin/env python3
"""解绑渠道。删除 hermes 自管的账号目录，触发 hermes 重新读 platforms 配置。"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import sys
from pathlib import Path


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--channel", required=True)
    args = p.parse_args()

    if args.channel != "weixin":
        sys.stdout.write(json.dumps({"status": "failed", "reason": "unknown channel"}) + "\n")
        return 1

    data_root = Path(os.environ.get("OC_DATA_DIR", "/opt/data"))
    accounts_dir = data_root / "weixin" / "accounts"
    if accounts_dir.exists():
        shutil.rmtree(accounts_dir)
    sys.stdout.write(json.dumps({"status": "unbound"}) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 5: 提交**

```bash
chmod +x runtime/hermes/hermes-main/oc-channel-*.py
git add runtime/hermes/hermes-main/oc-channel-login.py \
        runtime/hermes/hermes-main/oc-channel-status.py \
        runtime/hermes/hermes-main/oc-channel-unbind.py
git commit -m "feat(hermes-runtime): hermes-main 渠道命令三件套

oc-channel-login --channel weixin：移植旧 oc-weixin-login.py
扫码长轮询；输出协议改为 stdout 结束态 JSON + stderr 中间事件 JSON；
凭证落盘到 /opt/data/weixin/accounts/。
oc-channel-status / oc-channel-unbind 读写同一目录。"
```

---

### Task 10：编写 `Dockerfile` + `healthcheck.sh` + 本地构建验证

**Files:**
- Create: `runtime/hermes/hermes-main/Dockerfile`
- Create: `runtime/hermes/hermes-main/healthcheck.sh`

- [ ] **Step 1: 写 `healthcheck.sh`**

```bash
#!/usr/bin/env bash
# Hermes gateway 健康检查；内部 hermes gateway status 退出码即为健康判定。
set -euo pipefail
exec hermes gateway status >/dev/null 2>&1
```

- [ ] **Step 2: 写 `Dockerfile`**

按 spec §5.2 的模板写。完整内容见 spec 文件；变量替换：

- `ARG OC_IMAGE_VARIANT` 默认 `hermes-main`
- `RUN curl ... install.sh | bash -s -- --skip-setup --branch ${HERMES_REF}` 中 `HERMES_REF` 由 `--build-arg` 注入
- `COPY ... /usr/local/bin/...` 列表覆盖 `oc-entrypoint`、`oc-doctor`、`oc-info`、`oc-channel-login`、`oc-channel-status`、`oc-channel-unbind`、`oc-healthcheck`

注意 `RUN uv pip install --system --no-cache-dir pyyaml` 装 oc-entrypoint Python 依赖。

- [ ] **Step 3: 本地构建验证**

```bash
chmod +x runtime/hermes/hermes-main/healthcheck.sh
docker build \
  -t hermes-runtime:hermes-main-dev \
  --build-arg HERMES_REF=$(cat runtime/hermes/hermes-main/version.txt) \
  --build-arg OC_IMAGE_VARIANT=hermes-main \
  --build-arg OC_BUILD_TS=$(date -u +%Y%m%d%H%M%S) \
  runtime/hermes/hermes-main
docker run --rm hermes-runtime:hermes-main-dev oc-info
```

Expected: stdout 单行 JSON，含 `"variant":"hermes-main"`。

- [ ] **Step 4: 跑镜像内 pytest**

```bash
docker run --rm --entrypoint python hermes-runtime:hermes-main-dev \
  -m pytest /usr/local/lib/oc-entrypoint/tests/ -v
```

如果镜像没装 pytest，先在 Dockerfile 加 `RUN uv pip install --system --no-cache-dir pytest pyyaml`；并确保 `tests/` 也被 COPY 进镜像（在 Dockerfile 加 `COPY tests/ /usr/local/lib/oc-entrypoint/tests/`）。

- [ ] **Step 5: 提交**

```bash
git add runtime/hermes/hermes-main/Dockerfile \
        runtime/hermes/hermes-main/healthcheck.sh
git commit -m "feat(hermes-runtime): hermes-main Dockerfile 与健康检查

Dockerfile build context = hermes-main 子目录，COPY 不跨目录。
ENTRYPOINT 走 tini → oc-entrypoint；HEALTHCHECK 调
oc-healthcheck (实际 hermes gateway status)。Python 依赖
pyyaml/pytest 通过 uv pip install --system 装入系统 Python。"
```

---

### Task 11：改造 `Makefile`，删除旧 `runtime/hermes/` 顶层资产

**Files:**
- Modify: `Makefile`
- Delete: `runtime/hermes/Dockerfile`、`runtime/hermes/version.txt`、`runtime/hermes/CONTRACT.md`、`runtime/hermes/scripts/`
- Create: `runtime/hermes/README.md`

- [ ] **Step 1: 写 `runtime/hermes/README.md`**

```markdown
# Hermes runtime 镜像

## 目录约定

每个子目录是一个独立 variant，完全自包含：

- 命名：`hermes-<upstream-ref>`，例如 `hermes-main`（version.txt=`main`）
- 内部布局见 spec docs/superpowers/specs/2026-05-19-hermes-image-self-init-design.md §5.1

## 新增 variant

整体复制上一个目录后改名 + 改 `version.txt` + 改 `Dockerfile` 的
`OC_IMAGE_VARIANT` 默认值；如需要从上一个 variant 迁数据，新增
`migrator/from_<prev_variant>.py`。

## 构建

```bash
make build-hermes-runtime HERMES_VARIANT=hermes-main      # 本地 dev
make build-hermes-image  HERMES_VARIANT=hermes-main       # 生产镜像
make release-hermes-image HERMES_VARIANT=hermes-main      # 构建 + 推送
```
```

- [ ] **Step 2: 改 `Makefile`**

定位现有 `build-hermes-runtime / build-hermes-image / push-hermes-image / release-hermes-image / verify-hermes-runtime` 几个 target。逐一改成多 variant：

```makefile
HERMES_VARIANT       ?= hermes-main
HERMES_VARIANT_DIR   := runtime/hermes/$(HERMES_VARIANT)
HERMES_IMAGE_REPO    ?= crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes
HERMES_IMAGE         := $(HERMES_IMAGE_REPO):$(HERMES_VARIANT)-$(IMAGE_TIMESTAMP)

.PHONY: build-hermes-runtime build-hermes-image push-hermes-image release-hermes-image verify-hermes-runtime
build-hermes-runtime: ## 本地 dev 构建 hermes runtime（tag: hermes-runtime:<variant>-dev）
	docker build \
	  -t hermes-runtime:$(HERMES_VARIANT)-dev \
	  --build-arg HERMES_REF=$$(cat $(HERMES_VARIANT_DIR)/version.txt) \
	  --build-arg OC_IMAGE_VARIANT=$(HERMES_VARIANT) \
	  --build-arg OC_BUILD_TS=$(IMAGE_TIMESTAMP) \
	  $(HERMES_VARIANT_DIR)

build-hermes-image: ## 本地构建生产镜像（带 timestamp tag）
	docker build \
	  -t $(HERMES_IMAGE) \
	  --build-arg HERMES_REF=$$(cat $(HERMES_VARIANT_DIR)/version.txt) \
	  --build-arg OC_IMAGE_VARIANT=$(HERMES_VARIANT) \
	  --build-arg OC_BUILD_TS=$(IMAGE_TIMESTAMP) \
	  $(HERMES_VARIANT_DIR)

push-hermes-image: ## 推送 hermes 生产镜像
	docker push $(HERMES_IMAGE)

release-hermes-image: build-hermes-image push-hermes-image ## 构建 + 推送一步发版
	@echo "✅ hermes 镜像 $(HERMES_IMAGE) 已构建并推送"

verify-hermes-runtime: ## 镜像内 pytest（lib/renderer/migrator/oc-entrypoint）
	docker run --rm --entrypoint python hermes-runtime:$(HERMES_VARIANT)-dev \
	  -m pytest /usr/local/lib/oc-entrypoint/tests/ -v
```

- [ ] **Step 3: 删旧资产**

```bash
rm runtime/hermes/Dockerfile runtime/hermes/version.txt runtime/hermes/CONTRACT.md
rm -r runtime/hermes/scripts/
```

- [ ] **Step 4: 验证构建链路**

```bash
make build-hermes-runtime HERMES_VARIANT=hermes-main
make verify-hermes-runtime HERMES_VARIANT=hermes-main
```

Expected: 镜像内 pytest 全部 pass。

- [ ] **Step 5: 提交**

```bash
git add runtime/hermes/README.md Makefile
git add -u runtime/hermes/Dockerfile runtime/hermes/version.txt runtime/hermes/CONTRACT.md runtime/hermes/scripts
git commit -m "chore(hermes-runtime): 顶层资产迁移到 hermes-main variant 子目录

runtime/hermes/{Dockerfile,version.txt,CONTRACT.md,scripts/} 移到
hermes-main/，Makefile 走 HERMES_VARIANT 参数化各 target；新增仓库
级 README 说明 variant 命名规范与发版流程。"
```

---

## 阶段 B · 节点 agent (`runtime/agent/`)

### Task 12：`handleAppInit` MkdirAll 路径变更

**Files:**
- Modify: `runtime/agent/scopes.go:193-220`

- [ ] **Step 1: 定位现有 `handleAppInit`**

```bash
grep -n "func handleAppInit" runtime/agent/scopes.go
```

读 `scopes.go:193-220`，理解现有创建 `.hermes`、`.hermes/workspace`、`knowledge` 三个目录的逻辑。

- [ ] **Step 2: 改造为新目录布局**

把 `dirs := []string{...}` 列表改成：

```go
dirs := []string{
    filepath.Join(dataRoot, "apps", appID, "input", "resources", "knowledge", "org"),
    filepath.Join(dataRoot, "apps", appID, "input", "resources", "knowledge", "app"),
    filepath.Join(dataRoot, "apps", appID, "data", "workspace"),
}
```

同时更新函数注释，把"创建 .hermes / .hermes/workspace / knowledge 三个目录"改为新布局说明。

- [ ] **Step 3: 跑 agent 现有单测**

```bash
go test ./runtime/agent/... -run AppInit -v
```

如有失败，按现有断言风格补/调用例。

- [ ] **Step 4: 提交**

```bash
git add runtime/agent/scopes.go
git commit -m "feat(agent): handleAppInit 预建 input/data 双目录

按新挂载布局：input/resources/knowledge/{org,app} 与 data/workspace。
取代旧的 .hermes/ 与 knowledge/ 沙箱预建。"
```

---

### Task 13：节点 agent 新增 `input/file` 路由、删除 `runtime/file` 路由

**Files:**
- Modify: `runtime/agent/scopes.go:100-160`

- [ ] **Step 1: 新增 `input/file` 路由**

定位 `runtime/file` 那两条 case（PUT + DELETE），仿照其实现新增 `input/file`，sandbox 限制相对路径必须在 `apps/<id>/input/` 之内。具体改动：

```go
case action == "input/file" && r.Method == http.MethodPut:
    handleAppInputFilePut(w, r, dataRoot, appID)
case action == "input/file" && r.Method == http.MethodDelete:
    handleAppInputFileDelete(w, r, dataRoot, appID)
```

新增对应 `handleAppInputFilePut` / `handleAppInputFileDelete` 函数；复制现 `runtime/file` 实现，将 host 子目录从 `.hermes` 改为 `input`。

- [ ] **Step 2: 删 `runtime/file` 与 `knowledge/file` 两条 case**

`knowledge/file` PUT/DELETE 两条 case 也需要删（legacy 沙箱不再使用）。删除对应 handler 函数。

- [ ] **Step 3: 改/补单测**

`runtime/agent/scopes_test.go`（若不存在，仿照同名风格新建）：覆盖 input/file PUT/DELETE 的 happy path 与 sandbox 越界拒绝。

```go
func TestHandleAppInputFile_PutWritesIntoInputSandbox(t *testing.T) {
    // 给 dataRoot/apps/<id>/input/ 先 MkdirAll，发 PUT path=manifest.yaml，
    // 断言文件落在 dataRoot/apps/<id>/input/manifest.yaml。
    // ...
}

func TestHandleAppInputFile_RejectsPathTraversal(t *testing.T) {
    // path=../escape 应返回 400 并不写入。
}
```

- [ ] **Step 4: 运行测试 + 提交**

```bash
go test ./runtime/agent/... -v
git add runtime/agent/scopes.go runtime/agent/scopes_test.go
git commit -m "feat(agent): 新增 /input/file 路由替代 /runtime/file 与 /knowledge/file

manager 唯一写入面收敛到 apps/<id>/input/，老的 runtime/file
（写 .hermes/）与 knowledge/file（写 legacy knowledge 沙箱）两条
路由删除。input/file 沙箱限制相对路径不允许逃出 apps/<id>/input/。"
```

---

### Task 14：`/sessions` 与 workspace 路径前缀指向 `data/`

**Files:**
- Modify: `runtime/agent/scopes.go:622-680`（sessions）
- Modify: `runtime/agent/scopes.go`（workspace 列表 / 下载 / 打包 helper）

- [ ] **Step 1: 定位 sessions 清理逻辑**

```bash
grep -n "hermesHome\|.hermes/sessions\|.hermes/state.db" runtime/agent/scopes.go
```

- [ ] **Step 2: 改前缀**

把所有 `apps/<id>/.hermes/` 替换为 `apps/<id>/data/`；变量名 `hermesHome` 改为 `dataHome` 提示语义变更。

具体涉及：
- `sessionsDir := filepath.Join(hermesHome, "sessions")` → `filepath.Join(dataHome, "sessions")`
- `state.db` / `-shm` / `-wal` 三件套同上
- workspace 列表 / 下载 / 打包 helper 内 `apps/<id>/.hermes/workspace` 改为 `apps/<id>/data/workspace`

- [ ] **Step 3: 测试**

```bash
go test ./runtime/agent/... -v
```

- [ ] **Step 4: 提交**

```bash
git add runtime/agent/scopes.go
git commit -m "feat(agent): sessions 与 workspace 路径前缀指向 data/

DELETE /sessions 清除 apps/<id>/data/{sessions,state.db*}；
workspace 列表 / 下载 / 打包指向 apps/<id>/data/workspace。
对外 API 形态不变。"
```

---

## 阶段 C · manager 核心库 (`internal/integrations/hermes/`)

### Task 15：新增 `hermes/manifest.go`

**Files:**
- Create: `internal/integrations/hermes/manifest.go`
- Create: `internal/integrations/hermes/manifest_test.go`

- [ ] **Step 1: 写测试（先 fail）**

```go
package hermes

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "gopkg.in/yaml.v3"
)

// 验证 MarshalManifestYAML 字段顺序稳定且 decode 等价。
func TestMarshalManifestYAML_StableShape(t *testing.T) {
    m := Manifest{
        App: ManifestApp{ID: "app-x", Name: "X", Model: "claude-3.7-sonnet"},
        Credentials: ManifestCredentials{
            OpenAI: ManifestOpenAI{APIKey: "sk-x", BaseURL: "http://new-api:3000"},
        },
        Resources: ManifestResources{
            Persona: "resources/persona.md",
            Rules: ManifestRules{
                Platform:     "resources/platform-rules.md",
                Organization: "resources/organization-rules.md",
                Application:  "resources/application-rules.md",
            },
        },
    }

    b, err := MarshalManifestYAML(m)
    require.NoError(t, err)

    // 解码回原结构等价；同时验证字段顺序：app 在前、credentials 次之、resources 最后。
    var back Manifest
    require.NoError(t, yaml.Unmarshal(b, &back))
    assert.Equal(t, m, back)
}
```

- [ ] **Step 2: 实现**

```go
// Package hermes 提供 manager 写入 input 的中性数据结构与序列化。
package hermes

import (
    "bytes"

    "gopkg.in/yaml.v3"
)

// Manifest 对应 spec §4.2 manifest.yaml 的完整字段视图。
// 字段顺序通过显式 yaml tag 控制；不引入 schema_version。
type Manifest struct {
    App         ManifestApp         `yaml:"app"`
    Credentials ManifestCredentials `yaml:"credentials"`
    Resources   ManifestResources   `yaml:"resources"`
}

// ManifestApp 业务元数据。id/name 仅审计日志使用；model 直接进 hermes config.yaml model.default。
type ManifestApp struct {
    ID    string `yaml:"id"`
    Name  string `yaml:"name"`
    Model string `yaml:"model"`
}

// ManifestCredentials 凭证集合；当前仅 openai；微信凭证由 hermes 自管。
type ManifestCredentials struct {
    OpenAI ManifestOpenAI `yaml:"openai"`
}

// ManifestOpenAI OPENAI 凭证；base_url 不带 /v1，由镜像 renderer 自行拼。
type ManifestOpenAI struct {
    APIKey  string `yaml:"api_key"`
    BaseURL string `yaml:"base_url"`
}

// ManifestResources 指向 resources/ 子目录的相对路径集合。
type ManifestResources struct {
    Persona string        `yaml:"persona"`
    Rules   ManifestRules `yaml:"rules"`
}

// ManifestRules 三层规则的相对路径。
type ManifestRules struct {
    Platform     string `yaml:"platform"`
    Organization string `yaml:"organization"`
    Application  string `yaml:"application"`
}

// MarshalManifestYAML 把 Manifest 序列化为 YAML。
// 显式构造 yaml.Encoder 是为了未来需要时方便加 SetIndent 等。
func MarshalManifestYAML(m Manifest) ([]byte, error) {
    var buf bytes.Buffer
    enc := yaml.NewEncoder(&buf)
    enc.SetIndent(2)
    if err := enc.Encode(m); err != nil {
        return nil, err
    }
    if err := enc.Close(); err != nil {
        return nil, err
    }
    return buf.Bytes(), nil
}
```

- [ ] **Step 3: 测试通过 + 提交**

```bash
go test ./internal/integrations/hermes/ -run TestMarshalManifestYAML -v
git add internal/integrations/hermes/manifest.go internal/integrations/hermes/manifest_test.go
git commit -m "feat(hermes): 新增 Manifest 结构与 MarshalManifestYAML

按 spec §4.2 定义 manifest.yaml 的字段视图：app / credentials.openai /
resources（persona + rules 三层）。字段顺序通过 yaml tag 锁定，
未来字段只增不删；不引入 schema_version。"
```

---

### Task 16：改造 `hermes/prompt.go`：保留占位符替换、移除 SOUL.md 拼装

**Files:**
- Modify: `internal/integrations/hermes/prompt.go`
- Modify: `internal/integrations/hermes/prompt_test.go`

- [ ] **Step 1: 改测试**

把现有 `prompt_test.go` 改造为只测占位符替换 + 单层 markdown 输出，不再校验 "## 平台层" 这种 SOUL 拼装内容：

```go
package hermes

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

// 验证 RenderRuleText 仅做占位符替换，不做层级拼装。
func TestRenderRuleText_ReplacesPlaceholders(t *testing.T) {
    out, err := RenderRuleText("hello {org_name} / {app_name}", map[string]string{
        "org_name": "Acme", "app_name": "Bot", "owner_name": "ada",
    })
    require.NoError(t, err)
    assert.Equal(t, "hello Acme / Bot", out)
}

// 验证 RenderRuleText 在未替换占位符时报 ErrPromptUnresolvedPlaceholder。
func TestRenderRuleText_UnresolvedPlaceholder(t *testing.T) {
    _, err := RenderRuleText("hi {nope}", map[string]string{})
    require.ErrorIs(t, err, ErrPromptUnresolvedPlaceholder)
}
```

- [ ] **Step 2: 改实现**

精简 `prompt.go`：

```go
package hermes

import (
    "errors"
    "fmt"
    "regexp"
    "strings"
)

// ErrPromptUnresolvedPlaceholder 当变量字典未覆盖文本中的 {var} 占位符时返回。
var ErrPromptUnresolvedPlaceholder = errors.New("prompt 仍存在未替换的占位符")

var placeholderPattern = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// RenderRuleText 替换三层 rule 文本中的 {var} 占位符。
// vars 未覆盖任一占位符即返回 ErrPromptUnresolvedPlaceholder，
// 调用方应中止写入流程。
func RenderRuleText(body string, vars map[string]string) (string, error) {
    return replacePlaceholders(body, vars)
}

// RenderPersonaText 行为同 RenderRuleText，单独命名是为了语义清晰。
func RenderPersonaText(body string, vars map[string]string) (string, error) {
    return replacePlaceholders(body, vars)
}

// VariablesFromContext 给三层 prompt + persona 提供常用变量字典。
func VariablesFromContext(orgName, appName, ownerName string) map[string]string {
    return map[string]string{
        "org_name":   orgName,
        "app_name":   appName,
        "owner_name": ownerName,
    }
}

func replacePlaceholders(in string, vars map[string]string) (string, error) {
    var missing []string
    out := placeholderPattern.ReplaceAllStringFunc(in, func(match string) string {
        name := match[1 : len(match)-1]
        v, ok := vars[name]
        if !ok {
            missing = append(missing, name)
            return match
        }
        return v
    })
    if len(missing) > 0 {
        return "", fmt.Errorf("%w: %s", ErrPromptUnresolvedPlaceholder, strings.Join(missing, ","))
    }
    return out, nil
}
```

- [ ] **Step 3: 删除老导出函数 `Render` / `PromptInput` / `PromptResult`**

确认全仓库无人引用：

```bash
grep -rn "hermes.Render(" internal/ cmd/ runtime/ 2>&1 | grep -v _test.go
grep -rn "hermes.PromptInput\|hermes.PromptResult" internal/ cmd/ runtime/ 2>&1 | grep -v _test.go
```

如有调用方，留待 Task 21 一并改造（写到 input/resources 不再用此 API）。

- [ ] **Step 4: 测试 + 提交**

```bash
go test ./internal/integrations/hermes/ -run TestRender -v
git add internal/integrations/hermes/prompt.go internal/integrations/hermes/prompt_test.go
git commit -m "refactor(hermes): prompt 仅保留占位符替换，移除 SOUL 拼装

三层 prompt 的 ## 平台层 / ## 组织层 / ## 应用层 拼装下沉到镜像
renderer。manager 端只负责把 {org_name}/{app_name}/{owner_name}
变量替换好，写出纯 markdown 给镜像 oc-entrypoint 消费。"
```

---

### Task 17：新增 `hermes/app_input.go`

**Files:**
- Create: `internal/integrations/hermes/app_input.go`
- Create: `internal/integrations/hermes/app_input_test.go`

- [ ] **Step 1: 写测试**

```go
package hermes

import (
    "context"
    "io"
    "sort"
    "strings"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

// fakeInputWriter 记录 WriteAppInput 每次调用的相对路径与内容，
// 便于在测试中验证写入顺序与文件内容是否符合预期。
type fakeInputWriter struct {
    items map[string]string
}

func (f *fakeInputWriter) WriteAppInputFile(_ context.Context, _ string, relPath string, body io.Reader) error {
    b, err := io.ReadAll(body)
    if err != nil {
        return err
    }
    if f.items == nil {
        f.items = map[string]string{}
    }
    f.items[relPath] = string(b)
    return nil
}

// 验证 WriteAppInput 写入 manifest.yaml + 四份 resources markdown；
// rules 文本中的 {org_name} 已被替换为真实值。
func TestWriteAppInput_WritesManifestAndResources(t *testing.T) {
    w := &fakeInputWriter{}
    in := AppInputData{
        AppID: "app-x", AppName: "X", Model: "m",
        OpenAIAPIKey: "sk-x", OpenAIBaseURL: "http://x",
        PersonaText:     "Hi {owner_name}",
        PlatformRule:    "PLT {org_name}",
        OrganizationRule: "ORG",
        ApplicationRule: "APP {app_name}",
        OrgName: "Acme", OwnerName: "ada",
    }
    require.NoError(t, WriteAppInput(context.Background(), w, "node-1", in))

    keys := make([]string, 0, len(w.items))
    for k := range w.items {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    assert.Equal(t, []string{
        "manifest.yaml",
        "resources/application-rules.md",
        "resources/organization-rules.md",
        "resources/persona.md",
        "resources/platform-rules.md",
    }, keys)
    assert.Equal(t, "Hi ada", w.items["resources/persona.md"])
    assert.Equal(t, "PLT Acme", w.items["resources/platform-rules.md"])
    assert.Equal(t, "APP X", w.items["resources/application-rules.md"])
    assert.True(t, strings.Contains(w.items["manifest.yaml"], "sk-x"))
}
```

- [ ] **Step 2: 实现**

```go
package hermes

import (
    "bytes"
    "context"
    "fmt"
    "io"
)

// AppInputWriter 上传单个 input/ 子文件的能力。
// 实现由 internal/integrations/agent.RuntimeFileClient.UploadAppInputFile 提供。
type AppInputWriter interface {
    WriteAppInputFile(ctx context.Context, appID, relPath string, body io.Reader) error
}

// AppInputData manager 端写入 input/ 所需的全部业务数据。
// 占位符替换在 WriteAppInput 内部完成；调用方传入「原始」三层 prompt 文本。
type AppInputData struct {
    AppID            string
    AppName          string
    Model            string
    OpenAIAPIKey     string
    OpenAIBaseURL    string

    PersonaText      string
    PlatformRule     string
    OrganizationRule string
    ApplicationRule  string

    OrgName   string
    OwnerName string
}

// WriteAppInput 一次性写入 manifest.yaml + resources/persona.md + 三份 rules.md。
// 知识库文件由 knowledge_sync 链路单独写入 resources/knowledge/{org,app}/。
//
// 上传顺序：先写 resources/* 后写 manifest.yaml，最大限度避免 oc-entrypoint
// 读到「指向 resources 文件已不存在」的中间态。
func WriteAppInput(ctx context.Context, w AppInputWriter, appID string, in AppInputData) error {
    vars := VariablesFromContext(in.OrgName, in.AppName, in.OwnerName)
    persona, err := RenderPersonaText(in.PersonaText, vars)
    if err != nil {
        return fmt.Errorf("render persona: %w", err)
    }
    platform, err := RenderRuleText(in.PlatformRule, vars)
    if err != nil {
        return fmt.Errorf("render platform rule: %w", err)
    }
    organization, err := RenderRuleText(in.OrganizationRule, vars)
    if err != nil {
        return fmt.Errorf("render organization rule: %w", err)
    }
    application, err := RenderRuleText(in.ApplicationRule, vars)
    if err != nil {
        return fmt.Errorf("render application rule: %w", err)
    }

    if err := w.WriteAppInputFile(ctx, appID, "resources/persona.md", bytes.NewBufferString(persona)); err != nil {
        return fmt.Errorf("upload persona: %w", err)
    }
    if err := w.WriteAppInputFile(ctx, appID, "resources/platform-rules.md", bytes.NewBufferString(platform)); err != nil {
        return fmt.Errorf("upload platform rules: %w", err)
    }
    if err := w.WriteAppInputFile(ctx, appID, "resources/organization-rules.md", bytes.NewBufferString(organization)); err != nil {
        return fmt.Errorf("upload organization rules: %w", err)
    }
    if err := w.WriteAppInputFile(ctx, appID, "resources/application-rules.md", bytes.NewBufferString(application)); err != nil {
        return fmt.Errorf("upload application rules: %w", err)
    }

    m := Manifest{
        App: ManifestApp{ID: in.AppID, Name: in.AppName, Model: in.Model},
        Credentials: ManifestCredentials{
            OpenAI: ManifestOpenAI{APIKey: in.OpenAIAPIKey, BaseURL: in.OpenAIBaseURL},
        },
        Resources: ManifestResources{
            Persona: "resources/persona.md",
            Rules: ManifestRules{
                Platform:     "resources/platform-rules.md",
                Organization: "resources/organization-rules.md",
                Application:  "resources/application-rules.md",
            },
        },
    }
    body, err := MarshalManifestYAML(m)
    if err != nil {
        return fmt.Errorf("marshal manifest: %w", err)
    }
    if err := w.WriteAppInputFile(ctx, appID, "manifest.yaml", bytes.NewBuffer(body)); err != nil {
        return fmt.Errorf("upload manifest: %w", err)
    }
    return nil
}
```

- [ ] **Step 3: 测试 + 提交**

```bash
go test ./internal/integrations/hermes/ -run TestWriteAppInput -v
git add internal/integrations/hermes/app_input.go internal/integrations/hermes/app_input_test.go
git commit -m "feat(hermes): 新增 WriteAppInput 写 manifest.yaml + resources

WriteAppInput 取代旧 writeHermesFiles：一次写四份 markdown
（persona / platform / organization / application）+ manifest.yaml。
占位符替换在写入前完成；上传顺序先 resources 后 manifest，
让 oc-entrypoint 不会读到「manifest 指向不存在的 resources」中间态。"
```

---

### Task 18：新增 `hermes/commands.go`

**Files:**
- Create: `internal/integrations/hermes/commands.go`
- Create: `internal/integrations/hermes/commands_test.go`

- [ ] **Step 1: 写测试**

```go
package hermes

import (
    "context"
    "errors"
    "io"
    "strings"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

// fakeExec 记录 ContainerExec 调用的命令并返回预设输出。
type fakeExec struct {
    lastCmd []string
    stdout  string
    stderr  string
    err     error
}

func (f *fakeExec) ContainerExec(_ context.Context, _, _ string, cmd []string) (stdout, stderr io.Reader, err error) {
    f.lastCmd = cmd
    return strings.NewReader(f.stdout), strings.NewReader(f.stderr), f.err
}

func TestRunInfo_ParsesJSON(t *testing.T) {
    e := &fakeExec{stdout: `{"variant":"hermes-main","hermes_upstream_ref":"abc","oc_entrypoint_version":"1","built_at":"2026-05-19T00:00:00Z"}` + "\n"}
    info, err := RunInfo(context.Background(), e, "node-1", "container-1")
    require.NoError(t, err)
    assert.Equal(t, "hermes-main", info.Variant)
    assert.Equal(t, "abc", info.HermesUpstreamRef)
    assert.Equal(t, []string{"oc-info"}, e.lastCmd)
}

func TestRunChannelStatus_BuildsCmd(t *testing.T) {
    e := &fakeExec{stdout: `{"channel":"weixin","bound":true,"account_id":"x"}` + "\n"}
    s, err := RunChannelStatus(context.Background(), e, "n", "c", "weixin")
    require.NoError(t, err)
    assert.True(t, s.Bound)
    assert.Equal(t, []string{"oc-channel-status", "--channel", "weixin"}, e.lastCmd)
}

func TestRunInfo_ExecError(t *testing.T) {
    e := &fakeExec{err: errors.New("docker boom")}
    _, err := RunInfo(context.Background(), e, "n", "c")
    require.Error(t, err)
}
```

- [ ] **Step 2: 实现**

```go
package hermes

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
)

// ContainerExecer 抽象 manager 端通过节点 agent docker proxy 反向代理执行
// 容器命令的能力，实现由 internal/integrations/runtime.AgentBackedAdapter
// （ContainerExec）提供。
type ContainerExecer interface {
    ContainerExec(ctx context.Context, nodeID, containerID string, cmd []string) (stdout, stderr io.Reader, err error)
}

// Info 是 oc-info 命令的 stdout JSON 解码结果。
type Info struct {
    Variant             string `json:"variant"`
    HermesUpstreamRef   string `json:"hermes_upstream_ref"`
    OCEntrypointVersion string `json:"oc_entrypoint_version"`
    BuiltAt             string `json:"built_at"`
}

// Doctor 是 oc-doctor 命令的 stdout JSON 解码结果。
type Doctor struct {
    Variant        string   `json:"variant"`
    LastRenderAt   string   `json:"last_render_at"`
    ManifestSHA256 string   `json:"manifest_sha256"`
    HermesPID      int      `json:"hermes_pid"`
    HermesStatus   string   `json:"hermes_status"`
    Issues         []string `json:"issues"`
}

// ChannelStatus 是 oc-channel-status 命令的 stdout JSON 解码结果。
type ChannelStatus struct {
    Channel   string `json:"channel"`
    Bound     bool   `json:"bound"`
    AccountID string `json:"account_id,omitempty"`
}

// ChannelResult 是 oc-channel-login / oc-channel-unbind 的 stdout JSON 形态。
type ChannelResult struct {
    Status string `json:"status"`
    Reason string `json:"reason,omitempty"`
}

// RunInfo 调用容器内 oc-info，解析镜像身份。
func RunInfo(ctx context.Context, exec ContainerExecer, nodeID, containerID string) (Info, error) {
    var info Info
    err := runJSONCmd(ctx, exec, nodeID, containerID, []string{"oc-info"}, &info)
    return info, err
}

// RunDoctor 调用容器内 oc-doctor。
func RunDoctor(ctx context.Context, exec ContainerExecer, nodeID, containerID string) (Doctor, error) {
    var d Doctor
    err := runJSONCmd(ctx, exec, nodeID, containerID, []string{"oc-doctor"}, &d)
    return d, err
}

// RunChannelStatus 调用容器内 oc-channel-status。
func RunChannelStatus(ctx context.Context, exec ContainerExecer, nodeID, containerID, channel string) (ChannelStatus, error) {
    var s ChannelStatus
    err := runJSONCmd(ctx, exec, nodeID, containerID, []string{"oc-channel-status", "--channel", channel}, &s)
    return s, err
}

// RunChannelLogin 调用容器内 oc-channel-login。
// 中间事件（含二维码 URL）由 stderr 上报，目前不在此函数透传给调用方；
// 后续若需要可加 stderrSink io.Writer 参数。
func RunChannelLogin(ctx context.Context, exec ContainerExecer, nodeID, containerID, channel string) (ChannelResult, error) {
    var r ChannelResult
    err := runJSONCmd(ctx, exec, nodeID, containerID, []string{"oc-channel-login", "--channel", channel}, &r)
    return r, err
}

// RunChannelUnbind 调用容器内 oc-channel-unbind。
func RunChannelUnbind(ctx context.Context, exec ContainerExecer, nodeID, containerID, channel string) (ChannelResult, error) {
    var r ChannelResult
    err := runJSONCmd(ctx, exec, nodeID, containerID, []string{"oc-channel-unbind", "--channel", channel}, &r)
    return r, err
}

// runJSONCmd 封装：执行命令、读尽 stdout、按行 JSON 解码到 out。
func runJSONCmd(ctx context.Context, exec ContainerExecer, nodeID, containerID string, cmd []string, out interface{}) error {
    stdout, _, err := exec.ContainerExec(ctx, nodeID, containerID, cmd)
    if err != nil {
        return fmt.Errorf("exec %s: %w", cmd[0], err)
    }
    data, err := io.ReadAll(stdout)
    if err != nil {
        return fmt.Errorf("read stdout %s: %w", cmd[0], err)
    }
    if err := json.Unmarshal(trim(data), out); err != nil {
        return fmt.Errorf("decode %s stdout: %w", cmd[0], err)
    }
    return nil
}

// trim 去掉末尾换行；oc-* 命令统一以 \n 结尾。
func trim(b []byte) []byte {
    for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
        b = b[:len(b)-1]
    }
    return b
}
```

- [ ] **Step 3: 测试 + 提交**

```bash
go test ./internal/integrations/hermes/ -run TestRun -v
git add internal/integrations/hermes/commands.go internal/integrations/hermes/commands_test.go
git commit -m "feat(hermes): 新增 commands 包封装容器对外命令

RunInfo / RunDoctor / RunChannelLogin / RunChannelStatus /
RunChannelUnbind 五个 Go 函数统一通过 ContainerExecer 接口走
docker exec 反向代理；stdout JSON 解析到结构体后返回给调用方，
让 worker handler 无需自己拼命令字符串。"
```

---

### Task 19：删除 `hermes/config.go`、`hermes/skills.go`、`hermes/wechat_runner.go`

**Files:**
- Delete: `internal/integrations/hermes/config.go`、`config_test.go`、`skills.go`、`skills_test.go`、`wechat_runner.go`、`wechat_runner_test.go`

- [ ] **Step 1: 确认全仓库无残留引用**

```bash
grep -rn "hermes.RenderConfigYAML\|hermes.RenderEnv\|hermes.ConfigInput\|hermes.EnvInput" internal/ cmd/ runtime/
grep -rn "hermes.RenderKnowledgeSkill\|hermes.BuildKnowledgeSummary\|hermes.SlugifyKnowledgePath" internal/ cmd/ runtime/
grep -rn "hermes.WeChat\|wechat_runner\|wechat.Runner" internal/ cmd/ runtime/
```

预期：除了即将改造的 `app_initialize.go` / `channel_login.go` / `wiring.go` 外没有其他引用。

> 如发现仍有引用，先在 Task 21~24 中改造，再回来执行本 Task。

- [ ] **Step 2: 删除**

```bash
git rm internal/integrations/hermes/config.go \
       internal/integrations/hermes/config_test.go \
       internal/integrations/hermes/skills.go \
       internal/integrations/hermes/skills_test.go \
       internal/integrations/hermes/wechat_runner.go \
       internal/integrations/hermes/wechat_runner_test.go
```

- [ ] **Step 3: 验证编译**

```bash
go build ./...
```

如果失败，回头改造对应调用方（按 Task 21~24 计划）。

- [ ] **Step 4: 提交**

```bash
git commit -m "refactor(hermes): 删除 config/skills/wechat_runner 三个 legacy 包

RenderConfigYAML / RenderEnv / RenderKnowledgeSkill /
SlugifyKnowledgePath / wechat_runner 等渲染与扫码胶水代码迁到
镜像端 renderer/oc-channel-login.py；manager 端不再持有 hermes
内部 schema 知识。"
```

---

### Task 20：`file_client.go` 新增 `UploadAppInputFile`、删除 `UploadAppRuntimeFile`

**Files:**
- Modify: `internal/integrations/agent/file_client.go`
- Modify: `internal/integrations/agent/file_client_test.go`

- [ ] **Step 1: 定位现有 `UploadAppRuntimeFile`**

```bash
grep -n "UploadAppRuntimeFile" internal/integrations/agent/file_client.go
```

- [ ] **Step 2: 复制函数为 `UploadAppInputFile`，改 URL 路径**

```go
// UploadAppInputFile 通过节点 agent 反向代理向 apps/<id>/input/<relPath> 写入文件。
// 相对路径限制必须在 apps/<id>/input/ sandbox 之内（由 agent 侧校验）。
func (c *RuntimeFileClient) UploadAppInputFile(ctx context.Context, nodeID, appID, relPath string, body io.Reader) error {
    return c.doAppFile(ctx, nodeID, appID, "input/file", relPath, body)
}
```

注：复用现 `doAppFile` 私有 helper；如不存在，参照 `UploadAppRuntimeFile` 现有逻辑实现。

- [ ] **Step 3: 让 `RuntimeFileClient` 同时实现 `hermes.AppInputWriter` 接口**

新增适配方法（如需要包名规避循环 import，直接命名 `WriteAppInputFile` 即可）：

```go
// WriteAppInputFile 适配 internal/integrations/hermes.AppInputWriter 接口。
// 参数顺序与接口对齐：(ctx, appID, relPath, body)；nodeID 由调用方在
// 上下文中已绑定。
func (c *AppScopedFileClient) WriteAppInputFile(ctx context.Context, appID, relPath string, body io.Reader) error {
    return c.inner.UploadAppInputFile(ctx, c.nodeID, appID, relPath, body)
}
```

如需新加 `AppScopedFileClient` 类型，留作 Task 21 内部细化决定（也可直接在 worker handler 端写一个 lambda）。

- [ ] **Step 4: 删除 `UploadAppRuntimeFile`**

```bash
grep -rn "UploadAppRuntimeFile\b" internal/ cmd/ runtime/ | grep -v _test.go
```

预期：除即将改造的 `app_initialize.go` / `channel_login.go` / `wiring.go` 外不应有其他引用。把这些剩余调用点全部改成 `UploadAppInputFile`（一次性）；然后删除原方法定义与对应测试。

- [ ] **Step 5: 测试 + 提交**

```bash
go test ./internal/integrations/agent/... -v
git add internal/integrations/agent/file_client.go internal/integrations/agent/file_client_test.go
git commit -m "feat(agent-client): UploadAppInputFile 取代 UploadAppRuntimeFile

manager 端唯一写入面收敛到 apps/<id>/input/，原 UploadAppRuntimeFile
（写到 .hermes/）废弃；同步删除对应单测。"
```

---

## 阶段 D · manager handler / worker / wiring 改造

### Task 21：`app_initialize.go writeHermesFiles → WriteAppInput`

**Files:**
- Modify: `internal/worker/handlers/app_initialize.go`
- Modify: `internal/worker/handlers/app_initialize_test.go`

- [ ] **Step 1: 改测试**

把现有 `app_initialize_test.go` 中所有引用 `writeHermesFiles` / `writeSkillsFromKnowledge` / `collectKnowledgeForSoul` 的断言移除或替换为 `WriteAppInput` 调用的断言。改造点：

- `fakeRuntimeFileWriter` 重命名为 `fakeAppInputWriter`，提供 `WriteAppInputFile`；
- 任何"上传到 .hermes/SOUL.md / config.yaml" 之类的断言改为"上传到 input/manifest.yaml / input/resources/*"；
- 知识库 inline 至 SOUL.md 的测试场景全部删除（这是镜像责任）；
- 新增一个集成测试：完整 app_initialize 跑完后，fakeAppInputWriter 收到的 path 列表为 `["resources/persona.md", "resources/platform-rules.md", "resources/organization-rules.md", "resources/application-rules.md", "manifest.yaml"]`。

- [ ] **Step 2: 改 `AppInitializeHandler` 字段与依赖**

`internal/worker/handlers/app_initialize.go` 中：

- 把 `AppRuntimeFileWriter` 字段改名为 `AppInputWriter`，接口类型从 `agent.RuntimeFileClient` 改为 `hermes.AppInputWriter` 兼容形态；
- 删除 `KnowledgeReader` 字段（知识库由 knowledge_sync 单独写）；
  - 或保留 `KnowledgeReader` 但只用于"app 创建时一次性把主副本树推到 input/resources/knowledge/"——具体取舍：保留一份首次推送的便捷调用，否则首次创建后用户得手动跑一次全量 sync；这里保留，但调用从 `writeSkillsFromKnowledge` 改为 `writeKnowledgeIntoInput`。
- 用 `hermes.WriteAppInput` 替代旧 `writeHermesFiles`：

```go
in := hermes.AppInputData{
    AppID: uuidToString(app.ID), AppName: app.Name, Model: app.ModelID,
    OpenAIAPIKey: containerAPIKey, OpenAIBaseURL: h.config.NewAPIBaseURL,
    PersonaText:      h.config.PlatformPrompt, // 或从 app/org 字段拼
    PlatformRule:     h.config.PlatformPrompt,
    OrganizationRule: org.Prompt,
    ApplicationRule:  app.AppPrompt,
    OrgName: org.Name, OwnerName: owner.DisplayName,
}
if err := hermes.WriteAppInput(ctx, h.appInput, in.AppID, in); err != nil {
    return err
}
```

> 注：实际取值字段需照搬当前 `writeHermesFiles` 内的 prompt/org/app 取值逻辑，保持业务等价。

- 新增 `writeKnowledgeIntoInput` 函数：递归遍历主副本，通过 `UploadAppInputFile` PUT 到 `resources/knowledge/<scope>/<rel>`。

- [ ] **Step 3: 删除函数 `writeHermesFiles` / `writeSkillsFromKnowledge` / `uploadKnowledgeSkills` / `collectKnowledgeForSoul`**

把整段逻辑替换为新流程后删掉。

- [ ] **Step 4: 测试 + 提交**

```bash
go test ./internal/worker/handlers/ -run AppInitialize -v
git add internal/worker/handlers/app_initialize.go \
        internal/worker/handlers/app_initialize_test.go
git commit -m "refactor(worker): app_initialize 用 WriteAppInput 替代 writeHermesFiles

按 spec §9 行表改造 app 创建链路：原本写 4+N 个 hermes 内部文件
（config.yaml/SOUL.md/.env/skills/kb-*/SKILL.md）改成写
manifest.yaml + 4 份 markdown + 知识库主副本树。镜像
oc-entrypoint 在容器启动时把这些输入翻译成 hermes 自有 schema。"
```

---

### Task 22：`agent_backed.go CreateContainer` 双挂载 + 移除 `OPENAI_*` Env

**Files:**
- Modify: `internal/integrations/runtime/agent_backed.go:84-110`
- Modify: 调用方（`app_initialize.go` `ContainerSpec` 构造，第 290-320 行附近）

- [ ] **Step 1: 改 ContainerSpec 构造**

定位 `app_initialize.go` 中调用 `ContainerCreator.CreateContainer` 前构造的 `ContainerSpec`，更新 `Mounts`：

```go
mounts := []runtime.Mount{
    {HostPath: filepath.Join(nodeDataRoot, "apps", appID, "input"),
     ContainerPath: "/opt/oc-input", Mode: "ro"},
    {HostPath: filepath.Join(nodeDataRoot, "apps", appID, "data"),
     ContainerPath: "/opt/data", Mode: "rw"},
}
```

同位置删除原 `Env` 中的 `OPENAI_API_KEY` / `OPENAI_BASE_URL` 注入。`Env` 列表清空或保留空 slice。

- [ ] **Step 2: 改测试**

`app_initialize_test.go` 中 `CreateContainer` 调用断言：
- 验证 mounts 数量 = 2，路径与 mode 正确；
- 验证 `Env` slice 不包含 `OPENAI_API_KEY=`、`OPENAI_BASE_URL=` 前缀。

- [ ] **Step 3: 编译 + 测试 + 提交**

```bash
go test ./internal/worker/handlers/ -run AppInitialize -v
git add internal/worker/handlers/app_initialize.go \
        internal/worker/handlers/app_initialize_test.go \
        internal/integrations/runtime/agent_backed.go
git commit -m "feat(runtime): CreateContainer 改为双挂载、移除 OPENAI_* Env

apps/<id>/input → /opt/oc-input (ro)，apps/<id>/data → /opt/data (rw)。
OPENAI_API_KEY / OPENAI_BASE_URL 不再通过 docker Env 注入，
统一只走 manifest.yaml → oc-entrypoint 渲染的 config.yaml。"
```

---

### Task 23：简化 `AppRestartContainerHandler`，删 `wiring.go hermesConfigRefresher`

**Files:**
- Modify: `internal/worker/handlers/app_runtime_ops.go`
- Modify: `cmd/server/wiring.go`

- [ ] **Step 1: 改 `app_runtime_ops.go`**

定位 `AppRestartContainerHandler.Handle` 中调用 `RefreshConfigYAML` 的位置（约 250 行）。删除该调用；保留 `stop → clear sessions → start` 顺序，调整注释。

- [ ] **Step 2: 删 `wiring.go` 中 `hermesConfigRefresher` 类型与注入**

```bash
grep -n "hermesConfigRefresher\|RefreshConfigYAML\|refreshSkills\|refreshSoulMD" cmd/server/wiring.go
```

定位并整段删除 `hermesConfigRefresher` 类型及其方法，以及 wiring 处对它的注入与 `SetConfigRefresher(...)` 调用。

- [ ] **Step 3: 改 `AppRestartContainerHandler` 字段**

删除 `configRefresher` 字段以及对应 setter；测试 mock 同步删除引用。

- [ ] **Step 4: 编译 + 测试 + 提交**

```bash
go build ./...
go test ./internal/worker/handlers/ -run AppRestartContainer -v
git add internal/worker/handlers/app_runtime_ops.go cmd/server/wiring.go
git commit -m "refactor(worker): restart 链路不再 refresh hermes 文件

镜像 oc-entrypoint 每次启动幂等重渲染 config.yaml/SOUL.md/skills，
manager 端 RefreshConfigYAML / refreshSkills / refreshSoulMD 整组
退役。AppRestartContainerHandler 流程精简为 stop → clear sessions
→ start。"
```

---

### Task 24：`ChannelCheckBindingHandler` 改造：删 `.env` 重写、改用 commands 包

**Files:**
- Modify: `internal/worker/handlers/channel_login.go:160-260`

- [ ] **Step 1: 改造 `ChannelStartLoginHandler`**

把原本 docker exec `oc-weixin-login` 解析 stdout token 的逻辑替换为：

```go
result, err := hermes.RunChannelLogin(ctx, h.exec, app.RuntimeNodeID, app.ContainerID, "weixin")
if err != nil {
    return fmt.Errorf("run channel login: %w", err)
}
// 更新 channel_bindings 表 status / 二维码 URL 等元数据；
// 二维码 URL 现在由 stderr 中间事件携带，若需要透传到前端，需扩展 RunChannelLogin
// 让它接收一个 stderrSink io.Writer 参数。本期可暂用 polling 的 wait flow。
```

- [ ] **Step 2: 改造 `ChannelCheckBindingHandler.Handle`**

删除 line 228-256 整段 `RenderEnv` + `UploadAppRuntimeFile(".env")` 逻辑。bound 时只更新 `channel_bindings` 表并触发 `RestartContainer`（保留 restart 调用，因为 hermes 可能需要重启来加载新 weixin 凭证）。

```go
if result.Status == "bound" {
    // 更新表
    if err := h.store.UpdateChannelBinding(ctx, sqlc.UpdateChannelBindingParams{
        ID: binding.ID, Status: "bound", BoundAt: sql.NullTime{Time: time.Now(), Valid: true},
    }); err != nil {
        return fmt.Errorf("update channel binding: %w", err)
    }
    // 触发 restart（凭证已落盘到 hermes 自管目录，重启让 hermes 重新读 platforms 配置）
    if err := h.restarter.RestartContainer(ctx, app.RuntimeNodeID, app.ContainerID); err != nil {
        return fmt.Errorf("restart container: %w", err)
    }
}
```

- [ ] **Step 3: 删除 `SetRuntimeFileWriter` / `SetCipher` setter 与对应字段**

不再读 ciphertext，不再写 `.env`。

- [ ] **Step 4: 改测试**

`channel_login_test.go` 中相关断言：
- 移除"bound 后调 UploadAppRuntimeFile 写 .env"；
- 新增"bound 后调 RestartContainer"。

- [ ] **Step 5: 测试 + 提交**

```bash
go test ./internal/worker/handlers/ -run Channel -v
git add internal/worker/handlers/channel_login.go internal/worker/handlers/channel_login_test.go
git commit -m "refactor(worker): channel binding 不再写 .env，改用 commands.RunChannelLogin

微信凭证落盘改由镜像内 oc-channel-login 直接写 hermes 自管目录；
ChannelCheckBindingHandler 只更新 channel_bindings 表 + 触发
RestartContainer 让 hermes 重新读 platforms 配置。删除原 RenderEnv
+ UploadAppRuntimeFile(.env) 链路与对应字段 / setter。"
```

---

### Task 25：`knowledge_sync.go` 路径前缀变更

**Files:**
- Modify: `internal/worker/handlers/knowledge_sync.go`

- [ ] **Step 1: 定位现有路径**

```bash
grep -n "doKnowledgeFile\|knowledge/file\|apps/<id>/knowledge" internal/worker/handlers/knowledge_sync.go internal/integrations/agent/file_client.go
```

- [ ] **Step 2: 改写为新路径**

把对 `knowledge/file` 路由的调用改为对 `input/file` 路由的调用，relPath 改为 `resources/knowledge/<scope>/<rel>`。具体：

- `agent/file_client.go` 中 `doKnowledgeFile` helper：删除；改用 `UploadAppInputFile`；
- `knowledge_sync.go` 中：

```go
relPath := fmt.Sprintf("resources/knowledge/%s/%s", scope, rel)  // scope = "org" | "app"
if err := h.fileWriter.UploadAppInputFile(ctx, nodeID, appID, relPath, body); err != nil { ... }
```

- 删除事件（DELETE）：通过 `input/file` 路由的 DELETE 方法，relPath 同上。

- [ ] **Step 3: 测试 + 提交**

```bash
go test ./internal/worker/handlers/ -run KnowledgeSync -v
git add internal/worker/handlers/knowledge_sync.go internal/integrations/agent/file_client.go
git commit -m "refactor(worker): knowledge_sync 路径前缀指向 input/resources/knowledge

legacy apps/<id>/knowledge/ 沙箱完全退役；增量同步统一走
apps/<id>/input/resources/knowledge/{org,app}/。镜像 oc-entrypoint
在 restart 时重新扫描该目录并渲染 skills/kb-*。"
```

---

### Task 26：audit event `app.runtime_image_changed`

**Files:**
- Modify: 现有 audit log 常量表（按现有项目惯例定位）

- [ ] **Step 1: 定位 audit event 常量定义**

```bash
grep -rn "app.created\|AuditEvent\|EventType" internal/audit/ internal/service/ 2>&1 | head -20
```

- [ ] **Step 2: 加常量**

按现有惯例新增：

```go
const EventAppRuntimeImageChanged = "app.runtime_image_changed"
```

- [ ] **Step 3: 加 i18n 标签（如有）**

参考 `docs/superpowers/plans/2026-05-16-audit-log-i18n-labels.md` 风格。如有 zh-CN 字典，新增 `"应用镜像变更"`。

- [ ] **Step 4: 提交**

```bash
git add ... # 实际改动文件
git commit -m "feat(audit): 新增 app.runtime_image_changed 事件类型

平台管理员手动改 apps.runtime_image_ref 时触发；本期不提供 UI，
但事件常量与 i18n 标签先就位，供未来 UI 直接消费。"
```

---

### Task 27：本地数据清理 + 端到端浏览器验证

**Files:** 无代码改动。

- [ ] **Step 1: 清本地数据**

```bash
docker compose down -v
# 删 nodeDataRoot 目录
rm -rf <nodeDataRoot>/apps/      # 路径由本地 manager.yaml runtime.node_data_root 决定
make dev-up
```

- [ ] **Step 2: 构建镜像**

```bash
make build-hermes-runtime HERMES_VARIANT=hermes-main
make verify-hermes-runtime HERMES_VARIANT=hermes-main
```

Expected: 镜像内 pytest 全 pass。

- [ ] **Step 3: 浏览器端到端**

按 spec §10.3 全流程：
1. 创建 org + 用户 + app；
2. 触发 app_initialize，确认节点 `apps/<id>/input/` 与 `apps/<id>/data/` 两个目录都创建好；
3. `docker exec hermes-<id> oc-info` 输出 `variant=hermes-main`；
4. `docker exec hermes-<id> oc-doctor` 输出 `last_render_at` 非空；
5. 触发微信扫码（前端 UI），扫码后 `oc-channel-status` 应返回 `bound=true`；
6. 改主副本里某个知识库文件，restart，确认 `data/skills/kb-*-*/SKILL.md` 已更新；
7. 改 model，restart，确认 `data/config.yaml model.default` 已更新；
8. 用浏览器走一次完整对话验证。

- [ ] **Step 4: 验证完成无回退后无需提交**

本步骤纯验证，不涉及代码改动。如发现 bug，回到对应 Task 修正。

---

## 自检（plan 内联，发现问题已修正）

1. **Spec 覆盖：** spec §1~§14 每个章节都有对应 Task（§1 架构 / §2 目标 → 总体框架；§3 三方职责 → T21~T26；§4 manifest → T15、T17、T20；§5 镜像目录 → T1、T11；§6 oc-entrypoint → T7；§7 命令清单 → T8、T9、T18；§8 manager 改造 → T15~T26；§9 流程对齐 → T21~T25；§10 测试 → 每个 Task 内置；§11 失败处理 → 镜像 Task 6/7 已覆盖；§12 上线步骤 → T27；§13 audit log → T26）。
2. **占位符扫描：** Task 9 Step 1 留了 stub，但 Step 2 显式要求完成移植；其它任务无 TBD / TODO 残留。
3. **类型一致性：** `Manifest` / `AppInputWriter` / `ContainerExecer` / `Info` / `Doctor` / `ChannelStatus` / `ChannelResult` 在 T15、T17、T18 内的定义相互一致；`WriteAppInput(ctx, w, appID, in)` 签名跨 T17 和 T21 一致。
