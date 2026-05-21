# 助手版本 Phase 2 实施计划：manifest 契约 v2 与 oc-entrypoint 改造

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Hermes 镜像的 oc-entrypoint 支持 manifest 契约 v2——智能路由（8 个 auxiliary 槽位）、版本 skill tar、仅平台层 prompt——并新建第二个 `hermes-v2026.5.7` variant 用于测试版本切换。

**Architecture:** 纯镜像侧（Python）改动，**不动 Go 代码**。oc-entrypoint 的 `lib/manifest.py` 与三个 renderer 改为消费 manifest v2，并对老 v1 manifest 保持向后兼容（Go 写入侧的改造在 Phase 4）。skill 安装引入 `.oc-managed` 隐藏标记文件机制：oc-entrypoint 安装的 skill 目录可被识别并在每次渲染前清理重建，镜像内置 skill 不受影响。第二个 variant 是当前 variant 的完整拷贝，仅 `version.txt` 与目录名不同，并互相补一个 no-op 迁移模块。

**Tech Stack:** Python 3.13、PyYAML、`archive/tar`（Python `tarfile`）、pytest。镜像构建 `make build-hermes-runtime HERMES_VARIANT=<variant>`。

**关联文档：** 设计 spec `docs/superpowers/specs/2026-05-21-assistant-version-design.md` §4.1 / §5 / §6；Phase 1 计划已交付配置 `hermes.runtime_images`。

**范围与边界：**
- 本计划只改 `runtime/hermes/` 下的镜像源码，**不改任何 Go 文件**。
- Go 侧 `internal/integrations/hermes/manifest.go`（manifest 写入结构体）与 `BuildAppInputData`（写入 routing/skills）属 Phase 4——届时 Go 才真正往 manifest 写版本数据。
- 因此 Phase 2 的 oc-entrypoint 必须**向后兼容**：既能消费 manifest v2（含 `routing` / `resources.skills`、只有 platform rule），也能容忍当前 Go 仍在写的 v1 manifest（含 `resources.rules.organization` / `application`、无 `routing` / `skills`）。
- 工作目录：所有改动在 `runtime/hermes/hermes-v2026.5.16/`；最后一个任务把它整体拷贝成 `hermes-v2026.5.7/`。
- 测试：每个 variant 自带 pytest 套件。运行方式（在 variant 目录内）：`python3 -m pytest tests/ -v`。conftest.py 已把 variant 目录加入 `sys.path`。

---

## manifest 契约 v2（目标形态）

```yaml
app:
  id: <app id>
  name: <app name>
  model: <主模型>
routing:                       # 新增，可选；缺省 = 全部走主模型
  vision: <model>
  compression: <model>
  ...（8 个槽位，空槽位可省略）
credentials:
  openai: { api_key: <sk-...>, base_url: <new-api base, 不带 /v1> }
resources:
  persona: resources/persona.md
  rules:
    platform: resources/platform-rules.md   # 必填
    # organization / application：v2 不再写；Phase 2 解析器视为可选
  skills:                      # 新增，可选；版本 skill tar 的相对路径列表
    - resources/skills/<name>.tar
```

8 个 auxiliary 槽位（顺序固定）：`vision`、`compression`、`web_extract`、`session_search`、`title_generation`、`approval`、`skills_hub`、`mcp`。

---

## 文件结构

| 文件 | 职责 | 动作 |
|---|---|---|
| `runtime/hermes/hermes-v2026.5.16/lib/manifest.py` | manifest v2 解析（routing / skills 可选，org/app rule 可选） | 修改 |
| `runtime/hermes/hermes-v2026.5.16/tests/test_manifest.py` | manifest 解析测试 | 修改 |
| `runtime/hermes/hermes-v2026.5.16/renderer/render_config_yaml.py` | 从 routing 渲染 auxiliary 8 槽位 | 修改 |
| `runtime/hermes/hermes-v2026.5.16/tests/test_render_config_yaml.py` | config 渲染测试 | 修改 |
| `runtime/hermes/hermes-v2026.5.16/renderer/render_soul_md.py` | SOUL.md 去掉组织层 / 应用层 | 修改 |
| `runtime/hermes/hermes-v2026.5.16/tests/test_render_soul_md.py` | SOUL.md 渲染测试 | 修改 |
| `runtime/hermes/hermes-v2026.5.16/renderer/render_skills.py` | `.oc-managed` 标记机制 + 版本 skill tar 解压 | 修改 |
| `runtime/hermes/hermes-v2026.5.16/tests/test_render_skills.py` | skill 渲染测试 | 修改 |
| `runtime/hermes/hermes-v2026.5.16/oc-entrypoint.py` | render_skills 调用传入 manifest | 修改 |
| `runtime/hermes/hermes-v2026.5.16/tests/test_entrypoint_integration.py` | 入口集成测试（如受影响） | 按需修改 |
| `runtime/hermes/hermes-v2026.5.16/migrator/from_hermes_v2026_5_7.py` | 5.7→5.16 no-op 迁移 | 新建 |
| `runtime/hermes/hermes-v2026.5.7/**` | 第二个 variant（5.16 的完整拷贝） | 新建 |

---

## Task 1：manifest v2 解析

**Files:**
- Modify: `runtime/hermes/hermes-v2026.5.16/lib/manifest.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/tests/test_manifest.py`

`Manifest` 当前是 frozen dataclass，字段：`app_id/app_name/app_model/openai_api_key/openai_base_url/persona_rel/rule_platform_rel/rule_organization_rel/rule_application_rel`。`load()` 对全部字段用 `_require`。

- [ ] **Step 1：写失败测试** — 在 `tests/test_manifest.py` 末尾追加：

```python
def test_load_v2_parses_routing_and_skills(tmp_path: Path) -> None:
    # 验证 manifest v2 的 routing 与 resources.skills 被解析；org/app rule 缺省也合法。
    p = write(tmp_path / "manifest.yaml", """
app:
  id: app-x
  name: x
  model: m
routing:
  vision: gpt-5.4
  compression: deepseek-flash
credentials:
  openai: { api_key: sk-x, base_url: http://x }
resources:
  persona: resources/persona.md
  rules:
    platform: resources/platform-rules.md
  skills:
    - resources/skills/weather.tar
    - resources/skills/calc.tar
""")
    m = load(p)
    assert m.routing == {"vision": "gpt-5.4", "compression": "deepseek-flash"}
    assert m.skills == ["resources/skills/weather.tar", "resources/skills/calc.tar"]
    assert m.rule_organization_rel == ""
    assert m.rule_application_rel == ""


def test_load_v1_without_routing_skills_defaults_empty(tmp_path: Path) -> None:
    # 验证老 v1 manifest（无 routing / skills）解析后 routing={}、skills=[]，保持向后兼容。
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
""")
    m = load(p)
    assert m.routing == {}
    assert m.skills == []
    assert m.rule_platform_rel == "resources/platform-rules.md"
```

- [ ] **Step 2：运行测试确认失败** — Run（在 `runtime/hermes/hermes-v2026.5.16/` 目录）：`python3 -m pytest tests/test_manifest.py -v` — Expected: 两个新用例失败（`Manifest` 无 `routing` / `skills` 字段）。

- [ ] **Step 3：改 `lib/manifest.py`**

把 `Manifest` dataclass 改为（新增 `routing` / `skills`，`rule_organization_rel` / `rule_application_rel` 加默认值）：

```python
@dataclass(frozen=True)
class Manifest:
    """业务化视图。字段语义对应 spec §4.2 / §5（manifest v2）。"""
    app_id: str
    app_name: str
    app_model: str
    openai_api_key: str
    openai_base_url: str
    persona_rel: str
    rule_platform_rel: str
    # 组织层 / 应用层 rule：manifest v2 不再写；解析为可选，缺省空串。
    rule_organization_rel: str = ""
    rule_application_rel: str = ""
    # routing：8 个 auxiliary 槽位到模型名的映射；缺省空 dict（全部走主模型）。
    routing: dict = field(default_factory=dict)
    # skills：版本 skill tar 的相对路径列表（相对 input_root）；缺省空 list。
    skills: list = field(default_factory=list)
```

在文件顶部 import 加 `from dataclasses import dataclass, field`（当前只 import 了 `dataclass`）。

把 `load()` 改为：

```python
def load(path: Union[str, Path]) -> Manifest:
    """读 manifest.yaml 并构造 Manifest；未知顶层字段忽略；routing / skills / 组织层 / 应用层 rule 可选。"""
    raw = yaml.safe_load(Path(path).read_text())
    if not isinstance(raw, dict):
        raise ManifestError("manifest yaml root must be a mapping")
    resources = raw.get("resources")
    rules = resources.get("rules") if isinstance(resources, dict) else None
    rules = rules if isinstance(rules, dict) else {}
    routing = raw.get("routing")
    skills = resources.get("skills") if isinstance(resources, dict) else None
    return Manifest(
        app_id=_require(raw, "app", "id"),
        app_name=_require(raw, "app", "name"),
        app_model=_require(raw, "app", "model"),
        openai_api_key=_require(raw, "credentials", "openai", "api_key"),
        openai_base_url=_require(raw, "credentials", "openai", "base_url"),
        persona_rel=_require(raw, "resources", "persona"),
        rule_platform_rel=_require(raw, "resources", "rules", "platform"),
        rule_organization_rel=str(rules.get("organization") or ""),
        rule_application_rel=str(rules.get("application") or ""),
        routing={str(k): str(v) for k, v in routing.items()} if isinstance(routing, dict) else {},
        skills=[str(s) for s in skills] if isinstance(skills, list) else [],
    )
```

- [ ] **Step 4：运行测试确认通过** — Run: `python3 -m pytest tests/test_manifest.py -v` — Expected: 全部 PASS（含老用例 `test_load_minimal_ok` 等——它们的 manifest 含 org/app rule，仍合法）。

- [ ] **Step 5：提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/lib/manifest.py runtime/hermes/hermes-v2026.5.16/tests/test_manifest.py
git commit -m "feat(hermes-runtime): manifest 解析支持 v2 routing 与 skills"
```

提交信息：Conventional Commits、中文摘要，正文空一行后以 `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>` 结尾。

---

## Task 2：render_config_yaml 渲染 8 个 auxiliary 槽位

**Files:**
- Modify: `runtime/hermes/hermes-v2026.5.16/renderer/render_config_yaml.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/tests/test_render_config_yaml.py`

当前 `render_config_yaml.py` 用字符串 TEMPLATE，auxiliary 硬编码 4 个槽位全 `provider: main`。改为从 `manifest.routing` 渲染 8 个槽位。

- [ ] **Step 1：改测试** — 把 `tests/test_render_config_yaml.py` 的 `make_manifest()` 改为支持传 routing，并新增 routing 用例。完整替换文件内容为：

```python
"""验证 config.yaml 渲染：model、provider、base_url、api_key、auxiliary 8 槽位、terminal 全部就位。"""

from pathlib import Path
import yaml
from lib.manifest import Manifest
from renderer.render_config_yaml import render


def make_manifest(routing: dict | None = None) -> Manifest:
    # 构造一个最小合法 Manifest 给渲染用；routing 缺省空（全部走主模型）。
    return Manifest(
        app_id="app-x", app_name="X", app_model="claude-3.7-sonnet",
        openai_api_key="sk-test",
        openai_base_url="http://new-api:3000",
        persona_rel="resources/persona.md",
        rule_platform_rel="resources/platform-rules.md",
        routing=routing or {},
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


def test_render_auxiliary_all_main_when_routing_empty(tmp_data: Path) -> None:
    # routing 为空时，8 个 auxiliary 槽位全部 { provider: main }。
    render(make_manifest(), tmp_data)
    out = yaml.safe_load((tmp_data / "config.yaml").read_text())
    slots = ["vision", "compression", "web_extract", "session_search",
             "title_generation", "approval", "skills_hub", "mcp"]
    for slot in slots:
        assert out["auxiliary"][slot] == {"provider": "main"}, slot


def test_render_auxiliary_uses_routing_model(tmp_data: Path) -> None:
    # routing 指定了某槽位模型时，该槽位渲染为 custom + 该模型 + 凭证；其余仍走 main。
    render(make_manifest({"vision": "gpt-5.4", "title_generation": "qwen3.5:27b"}), tmp_data)
    out = yaml.safe_load((tmp_data / "config.yaml").read_text())
    assert out["auxiliary"]["vision"] == {
        "provider": "custom", "model": "gpt-5.4",
        "base_url": "http://new-api:3000/v1", "api_key": "sk-test",
    }
    assert out["auxiliary"]["title_generation"]["model"] == "qwen3.5:27b"
    assert out["auxiliary"]["compression"] == {"provider": "main"}


def test_render_is_atomic(tmp_data: Path) -> None:
    # 渲染完成后不应留 .tmp 残留。
    render(make_manifest(), tmp_data)
    leftovers = [p.name for p in tmp_data.iterdir() if p.suffix == ".tmp"]
    assert leftovers == []
```

- [ ] **Step 2：运行测试确认失败** — Run: `python3 -m pytest tests/test_render_config_yaml.py -v` — Expected: 新的 auxiliary 用例失败（当前只渲染 4 槽位）。

- [ ] **Step 3：改 `render_config_yaml.py`** — 完整替换文件内容为：

```python
"""把 manifest 渲染为本 variant 期望的 hermes config.yaml。

manifest v2：auxiliary 8 个槽位按 manifest.routing 渲染——指定模型的槽位走
custom + 该模型，未指定的走 { provider: main }。base_url 拼 /v1 由本 variant 决定。
"""

from __future__ import annotations

from pathlib import Path

import yaml

from lib.atomic import write_text
from lib.manifest import Manifest

# AUXILIARY_SLOTS 是智能路由的 8 个 auxiliary 槽位，顺序固定，与 manager 端约定一致。
AUXILIARY_SLOTS = [
    "vision", "compression", "web_extract", "session_search",
    "title_generation", "approval", "skills_hub", "mcp",
]


def _build_auxiliary(m: Manifest, base_url: str) -> dict:
    """按 manifest.routing 构造 auxiliary 段：指定模型走 custom，未指定走 main。"""
    aux: dict = {}
    routing = m.routing or {}
    for slot in AUXILIARY_SLOTS:
        model = str(routing.get(slot) or "").strip()
        if model:
            aux[slot] = {
                "provider": "custom", "model": model,
                "base_url": base_url, "api_key": m.openai_api_key,
            }
        else:
            aux[slot] = {"provider": "main"}
    return aux


def render(m: Manifest, data_root: Path) -> str:
    """渲染 config.yaml 到 data_root/config.yaml，返回相对路径。"""
    data_root.mkdir(parents=True, exist_ok=True)
    base_url = m.openai_base_url.rstrip("/") + "/v1"
    config = {
        "model": {
            "default": m.app_model, "provider": "custom",
            "base_url": base_url, "api_key": m.openai_api_key,
        },
        "auxiliary": _build_auxiliary(m, base_url),
        "memory": {
            "memory_enabled": True, "user_profile_enabled": True,
            "memory_char_limit": 2200, "user_char_limit": 1375,
        },
        "terminal": {
            "backend": "local", "cwd": "/opt/data/workspace",
            "timeout": 180, "lifetime_seconds": 300,
        },
    }
    header = "# Hermes 配置 - 由 oc-entrypoint 在容器启动时渲染（manifest v2）\n"
    body = header + yaml.safe_dump(config, allow_unicode=True, sort_keys=False)
    write_text(data_root / "config.yaml", body)
    return "config.yaml"
```

- [ ] **Step 4：运行测试确认通过** — Run: `python3 -m pytest tests/test_render_config_yaml.py -v` — Expected: 全部 PASS。

- [ ] **Step 5：提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/renderer/render_config_yaml.py runtime/hermes/hermes-v2026.5.16/tests/test_render_config_yaml.py
git commit -m "feat(hermes-runtime): config.yaml 按 routing 渲染 8 个 auxiliary 槽位"
```

提交信息规则同 Task 1。

---

## Task 3：render_soul_md 去掉组织层 / 应用层

**Files:**
- Modify: `runtime/hermes/hermes-v2026.5.16/renderer/render_soul_md.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/tests/test_render_soul_md.py`

当前 `render_soul_md.render` 拼装 header + persona + 三层 rules（平台/组织/应用）+ 知识库。spec §6.3：只保留平台层。

- [ ] **Step 1：先读现有测试** — 打开 `tests/test_render_soul_md.py` 了解它如何构造 `Manifest` 和断言（它会传 `rule_organization_rel` / `rule_application_rel`）。

- [ ] **Step 2：改 `render_soul_md.py`** — 把 `render` 函数里遍历三层 rules 的循环改为只处理平台层。把：

```python
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
```

替换为：

```python
    # manifest v2：只保留平台层 prompt；组织层 / 应用层已并入助手版本的 persona。
    platform_path = input_root / m.rule_platform_rel
    if m.rule_platform_rel and platform_path.exists():
        body = platform_path.read_text().strip()
        if body:
            parts.append(f"## 平台层\n\n{body}\n\n")
```

同步把文件顶部 docstring 里「三层 rules」的描述改为「平台层 rule」。

- [ ] **Step 3：改测试** — 在 `tests/test_render_soul_md.py`：
  - 现有用例若依赖组织层 / 应用层渲染，改成断言它们**不**出现。
  - 新增一个用例：构造一个带 `rule_organization_rel` / `rule_application_rel` 指向真实存在文件的 Manifest，渲染后断言 SOUL.md **不含** `## 组织层` 与 `## 应用层`，但**含** `## 平台层` 与 persona 内容。例如：

```python
def test_render_drops_org_and_app_layers(tmp_input: Path, tmp_data: Path) -> None:
    # manifest v2：即使 input 里仍有组织层 / 应用层 rule 文件，SOUL.md 也只渲染平台层。
    res = tmp_input / "resources"
    res.mkdir(parents=True)
    (res / "persona.md").write_text("我是版本人设")
    (res / "platform-rules.md").write_text("平台规则正文")
    (res / "organization-rules.md").write_text("组织规则正文")
    (res / "application-rules.md").write_text("应用规则正文")
    m = Manifest(
        app_id="a", app_name="A", app_model="m",
        openai_api_key="sk", openai_base_url="http://x",
        persona_rel="resources/persona.md",
        rule_platform_rel="resources/platform-rules.md",
        rule_organization_rel="resources/organization-rules.md",
        rule_application_rel="resources/application-rules.md",
    )
    render(m, tmp_input, tmp_data)
    soul = (tmp_data / "SOUL.md").read_text()
    assert "## 平台层" in soul
    assert "平台规则正文" in soul
    assert "我是版本人设" in soul
    assert "## 组织层" not in soul
    assert "## 应用层" not in soul
    assert "组织规则正文" not in soul
    assert "应用规则正文" not in soul
```

确认 `test_render_soul_md.py` 顶部已 import `Manifest`（`from lib.manifest import Manifest`）；缺则补。

- [ ] **Step 4：运行测试确认通过** — Run: `python3 -m pytest tests/test_render_soul_md.py -v` — Expected: 全部 PASS。

- [ ] **Step 5：提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/renderer/render_soul_md.py runtime/hermes/hermes-v2026.5.16/tests/test_render_soul_md.py
git commit -m "feat(hermes-runtime): SOUL.md 仅保留平台层 prompt"
```

提交信息规则同 Task 1。

---

## Task 4：render_skills 引入 .oc-managed 标记与版本 skill tar 解压

**Files:**
- Modify: `runtime/hermes/hermes-v2026.5.16/renderer/render_skills.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/tests/test_render_skills.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/oc-entrypoint.py`

新机制（spec §6.4）：
1. 每次渲染先扫 `data_root/skills/*/`，删掉所有含 `.oc-managed` 的目录。
2. 渲染知识库 `kb-*` skill（保留现有逻辑），每个目录补写 `.oc-managed`。
3. 解压 `manifest.skills` 列出的版本 skill tar 到 `data_root/skills/`，每个目录补写 `.oc-managed`。
4. 镜像内置 skill（无 `.oc-managed`）永不触碰。

`render` 签名从 `render(input_root, data_root)` 改为 `render(manifest, input_root, data_root)`，与 `render_config_yaml` / `render_soul_md` 一致。

- [ ] **Step 1：写失败测试** — 在 `tests/test_render_skills.py` 顶部 import 区加 `import io`、`import json`、`import tarfile`，`from lib.manifest import Manifest`。把现有调用 `render(tmp_input, tmp_data)` 的用例改为 `render(_manifest(), tmp_input, tmp_data)`，并加一个 `_manifest` helper 与新用例：

```python
def _manifest(skills: list[str] | None = None) -> Manifest:
    # 构造渲染 skill 所需的最小 Manifest；skills 缺省空。
    return Manifest(
        app_id="a", app_name="A", app_model="m",
        openai_api_key="sk", openai_base_url="http://x",
        persona_rel="resources/persona.md",
        rule_platform_rel="resources/platform-rules.md",
        skills=skills or [],
    )


def _make_skill_tar(path: Path, skill_dir: str, skill_name: str) -> None:
    # 在 path 写一个 skill tar：内含 <skill_dir>/SKILL.md（带 frontmatter）。
    path.parent.mkdir(parents=True, exist_ok=True)
    body = f"---\nname: {skill_name}\ndescription: d\n---\n# {skill_name}\n正文".encode()
    with tarfile.open(path, "w") as tw:
        info = tarfile.TarInfo(f"{skill_dir}/SKILL.md")
        info.size = len(body)
        tw.addfile(info, io.BytesIO(body))


def test_render_extracts_version_skill_tar(tmp_input: Path, tmp_data: Path) -> None:
    # manifest.skills 列出的 tar 被解压到 data_root/skills/ 下，且目录含 .oc-managed 标记。
    _make_skill_tar(tmp_input / "resources" / "skills" / "weather.tar", "weather", "weather")
    render(_manifest(["resources/skills/weather.tar"]), tmp_input, tmp_data)
    assert (tmp_data / "skills" / "weather" / "SKILL.md").exists()
    marker = tmp_data / "skills" / "weather" / ".oc-managed"
    assert marker.exists()
    assert json.loads(marker.read_text())["source"] == "version-skill"


def test_render_wipes_previously_managed_skill(tmp_input: Path, tmp_data: Path) -> None:
    # 上次安装的版本 skill（带 .oc-managed）在本次渲染前被清掉；不再出现在 manifest.skills 时即消失。
    stale = tmp_data / "skills" / "old-skill"
    stale.mkdir(parents=True)
    (stale / "SKILL.md").write_text("old")
    (stale / ".oc-managed").write_text('{"source":"version-skill"}')
    render(_manifest(), tmp_input, tmp_data)  # 本次 skills 为空
    assert not stale.exists()


def test_render_keeps_builtin_skill_without_marker(tmp_input: Path, tmp_data: Path) -> None:
    # 镜像内置 skill（无 .oc-managed）不被清理。
    builtin = tmp_data / "skills" / "builtin-skill"
    builtin.mkdir(parents=True)
    (builtin / "SKILL.md").write_text("builtin")
    render(_manifest(), tmp_input, tmp_data)
    assert (builtin / "SKILL.md").exists()


def test_render_rejects_unsafe_tar_path(tmp_input: Path, tmp_data: Path) -> None:
    # tar 含越界路径条目时抛错，不把文件写到 skills/ 之外。
    import pytest
    tar_path = tmp_input / "resources" / "skills" / "evil.tar"
    tar_path.parent.mkdir(parents=True, exist_ok=True)
    with tarfile.open(tar_path, "w") as tw:
        body = b"x"
        info = tarfile.TarInfo("../evil/SKILL.md")
        info.size = len(body)
        tw.addfile(info, io.BytesIO(body))
    with pytest.raises(Exception):
        render(_manifest(["resources/skills/evil.tar"]), tmp_input, tmp_data)
```

知识库相关的老用例（`test_render_creates_one_dir_per_file` 等）：把 `render(tmp_input, tmp_data)` 改成 `render(_manifest(), tmp_input, tmp_data)`，断言保持不变（知识库 `kb-*` 行为不变）。

- [ ] **Step 2：运行测试确认失败** — Run: `python3 -m pytest tests/test_render_skills.py -v` — Expected: 失败（`render` 签名不符 / 新功能未实现）。

- [ ] **Step 3：改 `render_skills.py`** — 在文件顶部 import 区加 `import datetime as _dt`、`import json`、`import shutil`、`import tarfile`、`from lib.manifest import Manifest`。把 `render` 函数与新增 helper 改为：

```python
# OC_SKILL_MARKER 是 oc-entrypoint 安装的 skill 目录里的隐藏标记文件名。
# 含该文件的目录由 oc-entrypoint 管理，每次渲染前清空重建；不含的视为镜像内置 skill，永不触碰。
OC_SKILL_MARKER = ".oc-managed"


def render(m: Manifest, input_root: Path, data_root: Path) -> List[str]:
    """渲染 skill：先清理上次 oc-entrypoint 安装的 skill，再渲染知识库 kb-* 与版本 skill tar。

    返回写入的相对路径列表。镜像内置 skill（无 .oc-managed 标记）不受影响。
    """
    skills_root = data_root / "skills"
    _wipe_managed_skills(skills_root)
    outputs: list[str] = []
    outputs.extend(_render_knowledge_skills(input_root, skills_root))
    outputs.extend(_extract_version_skills(m.skills or [], input_root, skills_root))
    return outputs


def _wipe_managed_skills(skills_root: Path) -> None:
    """删掉 skills_root 下所有含 .oc-managed 标记的目录（上次 oc-entrypoint 安装的 skill）。"""
    if not skills_root.exists():
        return
    for child in sorted(skills_root.iterdir()):
        if child.is_dir() and (child / OC_SKILL_MARKER).exists():
            shutil.rmtree(child)


def _write_marker(skill_dir: Path, source: str) -> None:
    """在一个 skill 目录里写 .oc-managed 标记，记录来源与安装时间。"""
    payload = {
        "source": source,
        "installed_at": _dt.datetime.utcnow().replace(microsecond=0).isoformat() + "Z",
    }
    write_text(skill_dir / OC_SKILL_MARKER, json.dumps(payload, ensure_ascii=False) + "\n")


def _render_knowledge_skills(input_root: Path, skills_root: Path) -> List[str]:
    """扫 input/resources/knowledge/{org,app}/* 生成 kb-* skill，每个目录补 .oc-managed 标记。"""
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
            _write_marker(target_dir, "knowledge")
            outputs.append(f"skills/{dir_name}/SKILL.md")
    return outputs


def _is_safe_member_path(name: str) -> bool:
    """校验 tar 条目路径在解压目标内、不越界。"""
    from pathlib import PurePosixPath
    p = PurePosixPath(name)
    if p.is_absolute():
        return False
    parts = p.parts
    return ".." not in parts and len(parts) > 0


def _extract_version_skills(skill_rels: List[str], input_root: Path, skills_root: Path) -> List[str]:
    """解压 manifest.skills 列出的版本 skill tar 到 skills_root，每个顶层目录补 .oc-managed 标记。"""
    outputs: list[str] = []
    skills_root.mkdir(parents=True, exist_ok=True)
    for rel in skill_rels:
        tar_path = input_root / rel
        if not tar_path.exists():
            raise FileNotFoundError(f"版本 skill tar 不存在: {rel}")
        top_dirs: set[str] = set()
        with tarfile.open(tar_path, "r") as tf:
            for member in tf.getmembers():
                if not _is_safe_member_path(member.name):
                    raise ValueError(f"skill tar 含越界路径条目: {member.name} ({rel})")
                if member.isreg() or member.isdir():
                    top = member.name.split("/", 1)[0]
                    if top:
                        top_dirs.add(top)
            tf.extractall(skills_root)
        for top in sorted(top_dirs):
            skill_dir = skills_root / top
            if skill_dir.is_dir():
                _write_marker(skill_dir, "version-skill")
                outputs.append(f"skills/{top}/")
    return outputs
```

> 注：`tf.extractall` 在已通过 `_is_safe_member_path` 逐条校验后调用是安全的。若运行的 Python ≥ 3.12，可选地额外传 `filter="data"`——但逐条校验已足够，保持上面写法即可。

- [ ] **Step 4：改 `oc-entrypoint.py`** — 把 phase 4 render 里的：

```python
        outputs.extend(render_skills.render(input_root, data_root))
```

改为：

```python
        outputs.extend(render_skills.render(manifest, input_root, data_root))
```

- [ ] **Step 5：运行测试确认通过** — Run: `python3 -m pytest tests/test_render_skills.py tests/test_entrypoint_integration.py -v` — Expected: 全部 PASS。若 `test_entrypoint_integration.py` 因 render_skills 签名变化失败，按其断言更新调用处（它走真实 `main()`，签名变更已在 Step 4 处理；若它直接调 `render_skills.render` 则同步改）。

- [ ] **Step 6：提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/renderer/render_skills.py runtime/hermes/hermes-v2026.5.16/tests/test_render_skills.py runtime/hermes/hermes-v2026.5.16/oc-entrypoint.py
git commit -m "feat(hermes-runtime): skill 渲染引入 .oc-managed 标记与版本 skill 解压"
```

提交信息规则同 Task 1。

---

## Task 5：全量自检 hermes-v2026.5.16

**Files:** 无（验证任务）

- [ ] **Step 1：跑完整 pytest** — Run（在 `runtime/hermes/hermes-v2026.5.16/`）：`python3 -m pytest tests/ -v` — Expected: 整个 variant 的测试套件全部 PASS。

- [ ] **Step 2：如有失败修复** — 若 `test_entrypoint_integration.py` 或其它用例因 manifest v2 / render 签名变化失败，定位并修复（更新测试装配或调用处），直到全绿。不得降低断言或跳过测试。

- [ ] **Step 3：（可选）构建镜像自检** — 若本地有 Docker：`make build-hermes-runtime HERMES_VARIANT=hermes-v2026.5.16`。Dockerfile 构建末尾会跑 `pytest`，构建成功即镜像内自检通过。无 Docker 时跳过，在交付说明里注明。

- [ ] **Step 4：提交（仅当 Step 2 有修复）**

```bash
git add runtime/hermes/hermes-v2026.5.16/
git commit -m "test(hermes-runtime): 修复 manifest v2 改造后的回归用例"
```

无修复则跳过提交。

---

## Task 6：新建 hermes-v2026.5.7 variant

**Files:**
- Create: `runtime/hermes/hermes-v2026.5.16/migrator/from_hermes_v2026_5_7.py`
- Create: `runtime/hermes/hermes-v2026.5.7/**`（整目录）

第二个 variant 是 `hermes-v2026.5.16` 完成 Phase 2 改造后的**完整拷贝**，仅 `version.txt` 与目录名不同，并互补一个 no-op 迁移模块，使两个 variant 之间可互相切换。

- [ ] **Step 1：给 5.16 variant 加 5.7→5.16 的 no-op 迁移**

创建 `runtime/hermes/hermes-v2026.5.16/migrator/from_hermes_v2026_5_7.py`：

```python
"""hermes-v2026.5.7 → hermes-v2026.5.16 的迁移。

两个 variant 渲染逻辑完全一致（5.7 是 5.16 的拷贝），数据无需迁移，no-op。
"""

from __future__ import annotations

from pathlib import Path


def run(data_root: Path) -> dict:
    """no-op 迁移：不改动 data_root，仅返回迁移摘要。"""
    return {"from": "hermes-v2026.5.7", "to": "hermes-v2026.5.16", "mode": "noop"}
```

- [ ] **Step 2：拷贝整个 variant 目录**

Run（仓库根目录）：

```bash
cp -r runtime/hermes/hermes-v2026.5.16 runtime/hermes/hermes-v2026.5.7
```

然后清理拷贝产生的非源码物：删除 `runtime/hermes/hermes-v2026.5.7/` 下的 `__pycache__`、`.pytest_cache`、注入的 `kanban-contract` / `cron-contract`（这些是构建期注入物，被 `.gitignore` 排除；用 `make hermes-inject-contract` 在构建时再生）。执行：

```bash
find runtime/hermes/hermes-v2026.5.7 -name __pycache__ -type d -exec rm -rf {} + 2>/dev/null; true
rm -rf runtime/hermes/hermes-v2026.5.7/.pytest_cache runtime/hermes/hermes-v2026.5.7/v
rm -rf runtime/hermes/hermes-v2026.5.7/kanban-contract runtime/hermes/hermes-v2026.5.7/cron-contract
```

（对照 `runtime/hermes/hermes-v2026.5.16/.gitignore` 确认哪些是忽略物——只提交源码文件。）

- [ ] **Step 3：改 5.7 variant 的 `version.txt`**

把 `runtime/hermes/hermes-v2026.5.7/version.txt` 内容从 `v2026.5.16` 改为 `v2026.5.7`（仅此一行，无换行差异）。

- [ ] **Step 4：调整 5.7 variant 的 migrator**

`runtime/hermes/hermes-v2026.5.7/migrator/__init__.py` 是从 5.16 拷来的，其中 legacy no-op 判断硬编码了 `curr_variant == "hermes-v2026.5.16"`。把该行改为 `curr_variant == "hermes-v2026.5.7"`，使 `hermes-main → hermes-v2026.5.7` 仍是 no-op。

删除拷贝来的 `runtime/hermes/hermes-v2026.5.7/migrator/from_hermes_v2026_5_7.py`（5.7 自己不需要「从 5.7 迁移」），改为创建 `runtime/hermes/hermes-v2026.5.7/migrator/from_hermes_v2026_5_16.py`：

```python
"""hermes-v2026.5.16 → hermes-v2026.5.7 的迁移。

两个 variant 渲染逻辑完全一致，数据无需迁移，no-op。
"""

from __future__ import annotations

from pathlib import Path


def run(data_root: Path) -> dict:
    """no-op 迁移：不改动 data_root，仅返回迁移摘要。"""
    return {"from": "hermes-v2026.5.16", "to": "hermes-v2026.5.7", "mode": "noop"}
```

- [ ] **Step 5：更新 5.7 variant 的 CONTRACT.md**

把 `runtime/hermes/hermes-v2026.5.7/CONTRACT.md` 里的 variant 名 `hermes-v2026.5.16` 改为 `hermes-v2026.5.7`，并把标题改为 `# hermes-v2026.5.7 · Variant 契约`。其余内容保持。

- [ ] **Step 6：自检 5.7 variant**

Run（在 `runtime/hermes/hermes-v2026.5.7/`）：`python3 -m pytest tests/ -v` — Expected: 全部 PASS（5.7 是 5.16 的拷贝，测试同样通过）。

- [ ] **Step 7：校验 Makefile variant 守卫**

Makefile 的 `.guard-hermes-version` 要求目录名 = `hermes-<version.txt 内容>`。确认 `runtime/hermes/hermes-v2026.5.7/version.txt` 是 `v2026.5.7`、目录名是 `hermes-v2026.5.7`、二者对齐。若本地有 Docker，可选：`make build-hermes-runtime HERMES_VARIANT=hermes-v2026.5.7` 验证构建（构建末尾跑 pytest）。无 Docker 跳过并注明。

- [ ] **Step 8：提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/migrator/from_hermes_v2026_5_7.py runtime/hermes/hermes-v2026.5.7/
git commit -m "feat(hermes-runtime): 新增 hermes-v2026.5.7 variant 与互通迁移"
```

提交信息：Conventional Commits、中文摘要，正文说明这是 5.16 的拷贝、仅 version.txt 不同、互补 no-op 迁移；空一行后以 `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>` 结尾。

---

## Self-Review

**Spec 覆盖（spec §4.1 / §5 / §6）：**
- §5 manifest 契约 v2（routing + skills，去 org/app rule）→ Task 1。
- §6.2 render_config_yaml 渲染 8 槽位 auxiliary → Task 2。
- §6.3 render_soul_md 只保留平台层 → Task 3。
- §6.4 render_skills 的 `.oc-managed` 标记机制 + 版本 skill tar 解压；§6.5 解压路径安全 → Task 4。
- §4.1 第二个 variant `hermes-v2026.5.7` → Task 6。

**不在本计划：** Go 侧 `manifest.go` 结构体扩展与 `BuildAppInputData` 写入 routing/skills（Phase 4，Go 写入侧）；组织 allowlist / 实例绑定（Phase 3）。

**向后兼容核对：** Task 1 的解析器把 routing/skills/org-app-rule 全设为可选——当前 Go 仍写 v1 manifest（含 org/app rule、无 routing/skills），解析为 `routing={}`、`skills=[]`、org/app rel 走可选；renderer 在 routing 空时全 `provider: main`、skills 空时不解压，行为与改造前一致。Phase 2 镜像既能跑 v1 也能跑 v2 manifest。

**Placeholder 扫描：** 无 TBD / TODO；每个步骤含完整代码与命令。

**类型一致性：** `render(manifest, input_root, data_root)` 三个 renderer 签名统一（config_yaml 是 `render(m, data_root)`，soul_md / skills 是 `render(m, input_root, data_root)`——与现状一致，仅 skills 从 2 参改 3 参）；`oc-entrypoint.py` Task 4 Step 4 同步改调用。`AUXILIARY_SLOTS` 的 8 个 key 与 Phase 1 后端 `auxiliarySlots`、Phase 1b 前端 `AUXILIARY_SLOTS` 完全一致。

---

## 后续

Phase 2 交付后，Hermes 镜像具备消费 manifest v2 的能力，且有两个可切换的 variant。后续：
- **Phase 3：** 组织 allowlist + 实例绑定版本 + 创建流程改造。
- **Phase 4：** Go 侧 `manifest.go` 扩展 + `BuildAppInputData` 写入 routing/skills/版本 persona + 实例初始化/重启写版本数据 + `version_synced` 检测 + 切换版本。
- **Phase 5：** 移除旧 persona / 组织 model 的列与代码。
