# Hermes Versioned Variant Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename Hermes runtime from `hermes-main` to `hermes-v2026.5.16` and make every active Hermes image tag derive from `version.txt`.

**Architecture:** Keep the existing per-variant runtime layout, but make the variant identity versioned. `Makefile` remains the single build entrypoint; it reads `runtime/hermes/hermes-v2026.5.16/version.txt` by default, rejects floating refs, and uses that version for local and production Docker tags. Runtime data compatibility is handled inside the variant migrator with a one-time no-op path from the old `hermes-main` name.

**Tech Stack:** GNU Make, Docker, Python 3.13 runtime scripts, pytest, YAML config files, Markdown docs.

---

## Scope Check

This is one cohesive build/runtime naming change. It touches the Hermes runtime directory, image build rules, configuration defaults, local debug scripts, and docs that describe those same tags. It does not change API behavior or frontend code, so OpenAPI generation and browser UI verification are not part of this plan.

## File Structure

| File | Responsibility |
|---|---|
| `runtime/hermes/hermes-v2026.5.16/` | Versioned Hermes runtime variant, renamed from `hermes-main`. |
| `runtime/hermes/hermes-v2026.5.16/migrator/__init__.py` | Decides whether previous runtime data can migrate into this variant. Adds `hermes-main` no-op compatibility and safe module suffix generation for dotted versions. |
| `runtime/hermes/hermes-v2026.5.16/tests/test_migrator.py` | Covers first boot, same-variant restart, legacy-name no-op migration, and unknown previous variant failure. |
| `Makefile` | Reads `version.txt`, rejects floating Hermes refs, builds local and production Hermes images with versioned tags. |
| `config/manager.yaml` | Local dev runtime image default. |
| `config/manager.example.yaml` | Local/example runtime image default and tag rule comment. |
| `deploy/manage/config/manager.yaml` | Production config sample in the deployment package; uses versioned production tag shape. |
| `deploy/manage/config/manager.example.yaml` | Production example config; uses versioned production tag shape. |
| `internal/config/loader_test.go` | Keeps config parsing assertions aligned with the new local dev tag. |
| `scripts/verify-hermes-runtime.sh` | Defaults local verification to the versioned dev image. |
| `scripts/sync-hermes-runtime-image.sh` | Defaults local image sync to the versioned dev image. |
| `runtime/hermes/README.md` | Documents versioned variant naming and build commands. |
| `runtime/hermes/hermes-v2026.5.16/CONTRACT.md` | Documents the variant identity and `hermes-main` legacy no-op migration. |
| `docs/configuration.md` | Documents the new `hermes.runtime_image` default and no-floating-tag rule. |
| `docs/hermes-container.md` | Updates container build/version wording to the new layout. |
| `docs/local-development.md` | Updates local build output image name. |

## Task 1: Rename Runtime Variant and Current Identity Strings

**Files:**
- Move: `runtime/hermes/hermes-main/` to `runtime/hermes/hermes-v2026.5.16/`
- Modify: `runtime/hermes/hermes-v2026.5.16/Dockerfile`
- Modify: `runtime/hermes/hermes-v2026.5.16/oc-entrypoint.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/oc-kanban.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/oc-cron.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/tests/test_state.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/tests/test_entrypoint_integration.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/tests/test_kanban_contract.py`

- [ ] **Step 1: Rename the variant directory**

Run:

```bash
rtk git mv runtime/hermes/hermes-main runtime/hermes/hermes-v2026.5.16
```

Expected: `git status --short` shows a directory rename under `runtime/hermes/`.

- [ ] **Step 2: Replace current variant identity inside the renamed directory**

Run:

```bash
rtk rg -l "hermes-main" runtime/hermes/hermes-v2026.5.16 \
  | xargs perl -0pi -e 's/hermes-main/hermes-v2026.5.16/g'
```

Expected: current identity references now use `hermes-v2026.5.16`. Task 2 will reintroduce the old `hermes-main` string only where it is a deliberate legacy migration input.

- [ ] **Step 3: Fix contract wording for future migrator module names**

Edit `runtime/hermes/hermes-v2026.5.16/CONTRACT.md` so its content is:

```markdown
# hermes-v2026.5.16 · Variant 契约

- 上游仓库: https://github.com/NousResearch/hermes-agent
- 锁定 ref: 见同目录 version.txt
- 安装方式: install.sh + uv (FHS layout，代码装到 /usr/local/lib/hermes-agent/)
- 数据迁移: 本 variant 由历史 `hermes-main` 重命名而来，允许 `hermes-main` 到 `hermes-v2026.5.16` 的 no-op 迁移；未来新增 variant 时，迁移模块名使用安全后缀，例如从 `hermes-v2026.5.16` 迁移时模块为 `from_hermes_v2026_5_16.py`。

# 镜像对外命令
- oc-info / oc-doctor / oc-healthcheck
- oc-channel-login / oc-channel-status / oc-channel-unbind
- oc-kanban
- ENTRYPOINT: tini -g -- /usr/local/bin/oc-entrypoint
```

- [ ] **Step 4: Verify direct identity tests**

Run:

```bash
rtk bash -lc 'cd runtime/hermes/hermes-v2026.5.16 && python -m pytest tests/test_state.py tests/test_entrypoint_integration.py -v'
```

Expected: both files pass. `test_entrypoint_first_boot` writes `.oc-state.json` with `image_variant == "hermes-v2026.5.16"`.

- [ ] **Step 5: Commit the rename**

Run:

```bash
rtk git add runtime/hermes/hermes-v2026.5.16
rtk git add -u runtime/hermes/hermes-main
rtk git commit -m "refactor(hermes-runtime): 将 variant 重命名为版本号" -m "把 hermes-main 目录迁到 hermes-v2026.5.16，并同步镜像内当前 variant 身份。"
```

Expected: one commit containing only the directory rename and current identity string updates.

## Task 2: Add Legacy Variant No-Op Migration

**Files:**
- Modify: `runtime/hermes/hermes-v2026.5.16/migrator/__init__.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/tests/test_migrator.py`

- [ ] **Step 1: Replace migrator dispatch implementation**

Edit `runtime/hermes/hermes-v2026.5.16/migrator/__init__.py` to:

```python
"""跨 variant 数据迁移 dispatch。

当前 variant 是 hermes-v2026.5.16。它由历史 hermes-main 目录重命名而来，
因此允许 hermes-main → hermes-v2026.5.16 作为 no-op 迁移。
未来版本号可能包含 "."，迁移模块名必须先规整为 Python-safe 后缀。
"""

from __future__ import annotations

import importlib
from pathlib import Path
from typing import Optional

LEGACY_NOOP_PREV_VARIANTS = {"hermes-main"}


def run(prev_variant: Optional[str], curr_variant: str, data_root: Path) -> Optional[dict]:
    """根据 prev/curr variant 决定是否需要迁移。

    返回 None 表示跳过迁移；非 None 返回迁移摘要（写入 .oc-state.last_migrate_from）。
    迁移失败抛异常，调用方（oc-entrypoint）退出码 1，并保证 data_root 已被 migrator 原子处理。
    """
    if prev_variant is None or prev_variant == curr_variant:
        return None
    if curr_variant == "hermes-v2026.5.16" and prev_variant in LEGACY_NOOP_PREV_VARIANTS:
        return {"from": prev_variant, "to": curr_variant, "mode": "noop_rename"}
    module_name = f"migrator.from_{_migration_module_suffix(prev_variant)}"
    try:
        mod = importlib.import_module(module_name)
    except ModuleNotFoundError as e:
        raise NotImplementedError(
            f"no migrator path from {prev_variant} → {curr_variant}; "
            f"please ship a {module_name} module"
        ) from e
    return mod.run(data_root)


def _migration_module_suffix(variant: str) -> str:
    """把 variant 名转换为可用于 Python module 的后缀。"""
    return variant.replace("-", "_").replace(".", "_")
```

- [ ] **Step 2: Replace migrator tests**

Edit `runtime/hermes/hermes-v2026.5.16/tests/test_migrator.py` to:

```python
"""验证 migrator dispatch 的首启、同版本、历史重命名和未知来源路径。"""

from pathlib import Path

import pytest

from migrator import _migration_module_suffix, run as run_migration


def test_no_prev_skips(tmp_data: Path) -> None:
    # 首次启动 prev=None，应直接返回 None 不抛。
    result = run_migration(prev_variant=None, curr_variant="hermes-v2026.5.16", data_root=tmp_data)
    assert result is None


def test_same_variant_skips(tmp_data: Path) -> None:
    # prev == curr 表示同一个版本重启，跳过迁移。
    result = run_migration(
        prev_variant="hermes-v2026.5.16",
        curr_variant="hermes-v2026.5.16",
        data_root=tmp_data,
    )
    assert result is None


def test_legacy_hermes_main_noop_returns_summary(tmp_data: Path) -> None:
    # hermes-main 是本 variant 的历史目录名，只记录 no-op 摘要，不改数据文件。
    result = run_migration(
        prev_variant="hermes-main",
        curr_variant="hermes-v2026.5.16",
        data_root=tmp_data,
    )
    assert result == {
        "from": "hermes-main",
        "to": "hermes-v2026.5.16",
        "mode": "noop_rename",
    }


def test_unknown_prev_raises(tmp_data: Path) -> None:
    # 未实现迁移模块的来源版本必须 fail-fast，避免错误复用不兼容数据。
    with pytest.raises(NotImplementedError):
        run_migration(
            prev_variant="hermes-experimental",
            curr_variant="hermes-v2026.5.16",
            data_root=tmp_data,
        )


def test_migration_module_suffix_replaces_dash_and_dot() -> None:
    # 版本号包含 "." 时也要生成合法 Python module 名。
    assert _migration_module_suffix("hermes-v2026.5.16") == "hermes_v2026_5_16"
```

- [ ] **Step 3: Verify migrator behavior**

Run:

```bash
rtk bash -lc 'cd runtime/hermes/hermes-v2026.5.16 && python -m pytest tests/test_migrator.py -v'
```

Expected: 5 tests pass.

- [ ] **Step 4: Commit migrator compatibility**

Run:

```bash
rtk git add runtime/hermes/hermes-v2026.5.16/migrator/__init__.py runtime/hermes/hermes-v2026.5.16/tests/test_migrator.py
rtk git commit -m "fix(hermes-runtime): 兼容旧 variant 名迁移" -m "允许 hermes-main 到 hermes-v2026.5.16 的 no-op 迁移，并修正 dotted 版本号对应的 migrator 模块名生成。"
```

Expected: one commit containing only migrator code and tests.

## Task 3: Derive Hermes Image Tags From version.txt

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Replace Hermes variable block**

In `Makefile`, replace the Hermes variable block near the top with:

```makefile
# hermes runtime 生产镜像仓库，与上方三个服务保持一致命名风格。
# HERMES_VARIANT 选择 runtime/hermes/ 下的 versioned variant 子目录（自包含 Dockerfile + 资产）。
# 镜像 tag 从该 variant 的 version.txt 派生，禁止 main / master / latest 等浮动 ref。
HERMES_VARIANT       ?= hermes-v2026.5.16
HERMES_VARIANT_DIR   := runtime/hermes/$(HERMES_VARIANT)
HERMES_VERSION       := $(strip $(shell if [ -f "$(HERMES_VARIANT_DIR)/version.txt" ]; then cat "$(HERMES_VARIANT_DIR)/version.txt"; fi))
HERMES_IMAGE_REPO    ?= crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes
# hermes tag 形如 v2026.5.16-2026-05-21-12-00-00，便于从镜像引用直接看出上游版本。
HERMES_IMAGE         := $(HERMES_IMAGE_REPO):$(HERMES_VERSION)-$(IMAGE_TIMESTAMP)
```

- [ ] **Step 2: Add the Makefile guard target**

After the `help` target or before `##@ Hermes runtime 镜像`, add:

```makefile
.PHONY: .guard-hermes-version
.guard-hermes-version:
	@test -f "$(HERMES_VARIANT_DIR)/version.txt" || { echo "Hermes variant 缺少 version.txt: $(HERMES_VARIANT_DIR)/version.txt" >&2; exit 1; }
	@test -n "$(HERMES_VERSION)" || { echo "Hermes version 不能为空: $(HERMES_VARIANT_DIR)/version.txt" >&2; exit 1; }
	@case "$(HERMES_VERSION)" in \
		main|master|latest) echo "Hermes version 不能使用浮动 tag: $(HERMES_VERSION)" >&2; exit 1;; \
	esac
```

- [ ] **Step 3: Make Hermes injection depend on the guard**

Change the target header:

```makefile
hermes-inject-contract: ## 把 HERMES_VARIANT 指定变体的契约工件注入目录
```

to:

```makefile
hermes-inject-contract: .guard-hermes-version ## 把 HERMES_VARIANT 指定变体的契约工件注入目录
```

- [ ] **Step 4: Use HERMES_VERSION for local build tags and build args**

In `build-hermes-runtime`, replace the docker build tag and `HERMES_REF` line with:

```makefile
	  -t hermes-runtime:$(HERMES_VERSION)-dev \
	  --build-arg HERMES_REF=$(HERMES_VERSION) \
```

In `build-hermes-image`, replace the `HERMES_REF` line with:

```makefile
	  --build-arg HERMES_REF=$(HERMES_VERSION) \
```

- [ ] **Step 5: Verify dry-run tags**

Run:

```bash
rtk make --dry-run build-hermes-runtime
```

Expected output includes:

```text
-t hermes-runtime:v2026.5.16-dev
--build-arg HERMES_REF=v2026.5.16
--build-arg OC_IMAGE_VARIANT=hermes-v2026.5.16
```

Run:

```bash
rtk make --dry-run build-hermes-image
```

Expected output includes:

```text
-t crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes:v2026.5.16-
--build-arg HERMES_REF=v2026.5.16
```

- [ ] **Step 6: Verify floating-ref guard**

Run:

```bash
rtk make .guard-hermes-version HERMES_VERSION=main
```

Expected: command exits non-zero and stderr contains:

```text
Hermes version 不能使用浮动 tag: main
```

- [ ] **Step 7: Commit Makefile changes**

Run:

```bash
rtk git add Makefile
rtk git commit -m "chore(make): 用 Hermes 版本号生成镜像 tag" -m "Makefile 从 variant version.txt 读取 Hermes 上游版本，并拒绝 main、master、latest 等浮动 tag。"
```

Expected: one commit containing only `Makefile`.

## Task 4: Update Runtime Image Configs, Scripts, and Config Tests

**Files:**
- Modify: `config/manager.yaml`
- Modify: `config/manager.example.yaml`
- Modify: `deploy/manage/config/manager.yaml`
- Modify: `deploy/manage/config/manager.example.yaml`
- Modify: `internal/config/loader_test.go`
- Modify: `scripts/verify-hermes-runtime.sh`
- Modify: `scripts/sync-hermes-runtime-image.sh`

- [ ] **Step 1: Update local config**

In `config/manager.yaml`, replace the Hermes image comment and value with:

```yaml
hermes:
  # 本地 dev：用 make build-hermes-runtime 构建 hermes-runtime:v2026.5.16-dev
  # 生产：使用 ACR 的 oc-manager-hermes:v2026.5.16-2026-05-21-12-00-00 这种构建时间戳 tag
  runtime_image: "hermes-runtime:v2026.5.16-dev"
```

- [ ] **Step 2: Update local example config**

In `config/manager.example.yaml`, replace the Hermes image comment and value with:

```yaml
hermes:
  # Hermes 容器的镜像引用（name:tag 或 digest）。runtime node 上必须存在该镜像；
  # imagesync 会用 docker save / load 把宿主镜像分发到节点。tag 必须固定到具体 Hermes 版本。
  runtime_image: "hermes-runtime:v2026.5.16-dev"
```

- [ ] **Step 3: Update production config samples**

In both `deploy/manage/config/manager.yaml` and `deploy/manage/config/manager.example.yaml`, replace the Hermes runtime image block with:

```yaml
hermes:
  # Hermes 容器的镜像引用（name:tag 或 name@sha256:digest）。runtime node 上必须存在该镜像；
  # imagesync 会用 docker save / load 把宿主镜像分发到节点。tag 必须固定到具体 Hermes 版本，禁用 main / latest。
  runtime_image: "crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes:v2026.5.16-2026-05-21-12-00-00"
```

This value is a concrete example tag that matches the new release format. During a real production release, replace it with the exact image printed by `make release-hermes-image`.

- [ ] **Step 4: Update config loader test fixture**

In `internal/config/loader_test.go`, change the sample runtime image and assertion:

```go
hermes:
  runtime_image: "hermes-runtime:v2026.5.16-dev"
```

and:

```go
require.Equal(t, "hermes-runtime:v2026.5.16-dev", cfg.Hermes.RuntimeImage)
```

- [ ] **Step 5: Update script defaults**

In `scripts/verify-hermes-runtime.sh`, change:

```bash
image="${HERMES_RUNTIME_IMAGE:-hermes-runtime:dev}"
```

to:

```bash
image="${HERMES_RUNTIME_IMAGE:-hermes-runtime:v2026.5.16-dev}"
```

In `scripts/sync-hermes-runtime-image.sh`, change the default comment and image line to:

```bash
# 默认假设 hermes-runtime:v2026.5.16-dev 已在本机 build(参见 Makefile 的 build-hermes-runtime)。

image="${HERMES_RUNTIME_IMAGE:-hermes-runtime:v2026.5.16-dev}"
```

- [ ] **Step 6: Verify config tests**

Run:

```bash
rtk go test ./internal/config
```

Expected: all tests in `internal/config` pass.

- [ ] **Step 7: Commit config and script changes**

Run:

```bash
rtk git add config/manager.yaml config/manager.example.yaml deploy/manage/config/manager.yaml deploy/manage/config/manager.example.yaml internal/config/loader_test.go scripts/verify-hermes-runtime.sh scripts/sync-hermes-runtime-image.sh
rtk git commit -m "chore(config): 使用版本化 Hermes 镜像引用" -m "将本地、示例和部署配置的 Hermes runtime 镜像改为 v2026.5.16 版本化 tag，并同步本地调试脚本默认值。"
```

Expected: one commit containing only config, script, and config-test updates.

## Task 5: Update Active Documentation

**Files:**
- Modify: `runtime/hermes/README.md`
- Modify: `docs/configuration.md`
- Modify: `docs/hermes-container.md`
- Modify: `docs/local-development.md`

- [ ] **Step 1: Replace runtime README**

Edit `runtime/hermes/README.md` to:

```markdown
# Hermes runtime 镜像

## 目录约定

每个子目录是一个独立 variant，完全自包含：

- 命名形如 `hermes-v2026.5.16`，目录版本号与 version.txt 的 `v2026.5.16` 保持一致
- 内部布局见 spec docs/superpowers/specs/2026-05-19-hermes-image-self-init-design.md §5.1

## 新增 variant

整体复制上一个目录后改名并修改 `version.txt`；如需要从上一个 variant 迁数据，
新增迁移模块，例如从 `hermes-v2026.5.16` 迁移时使用
`from_hermes_v2026_5_16.py`。

## 构建

```bash
make build-hermes-runtime                         # 本地 dev，输出 hermes-runtime:v2026.5.16-dev
make build-hermes-image                           # 生产镜像，输出 oc-manager-hermes:v2026.5.16-2026-05-21-12-00-00 这种时间戳 tag
make release-hermes-image                         # 构建 + 推送
make build-hermes-runtime HERMES_VARIANT=hermes-v2026.5.16
```

Hermes runtime 的 pytest 自检已经写入 Dockerfile，构建过程中会自动执行。
```

- [ ] **Step 2: Update configuration reference**

In `docs/configuration.md`, update the `runtime_image` row to:

```markdown
| `runtime_image` | string | `hermes-runtime:v2026.5.16-dev` | Hermes 容器镜像引用（name:tag 或 digest）；tag 必须固定到具体 Hermes 版本，runtime 节点上必须存在该镜像，imagesync 用 `docker save / load` 分发 |
```

- [ ] **Step 3: Update Hermes container docs**

In `docs/hermes-container.md`, replace the old image sync paragraph:

```markdown
构建产物为 `hermes-runtime:dev`(默认 tag),由仓库内 `runtime/hermes/Dockerfile`
构建。`AppInitializeConfig.RuntimeImage` 在装配时如未设置则 fallback 到
`hermes-runtime:dev`(`app_initialize.go:191`)。镜像版本锁通过
`runtime/hermes/version.txt`(当前 `main`)与 Dockerfile `HERMES_REF` ARG 传入。
```

with:

```markdown
构建产物为 `hermes-runtime:v2026.5.16-dev`（本地 dev tag），由仓库内
`runtime/hermes/hermes-v2026.5.16/Dockerfile` 构建。生产发布 tag 形如
`oc-manager-hermes:v2026.5.16-2026-05-21-12-00-00` 这种时间戳 tag。镜像版本锁通过
`runtime/hermes/hermes-v2026.5.16/version.txt` 与 Dockerfile `HERMES_REF`
ARG 传入，Makefile 会拒绝 `main`、`master`、`latest` 等浮动 ref。
```

- [ ] **Step 4: Update local development docs**

In `docs/local-development.md`, replace the duplicate Hermes runtime rows under `### Hermes runtime 与调试` with a single row:

```markdown
| `make build-hermes-runtime` | 构建 `hermes-runtime:v2026.5.16-dev` 镜像，并在 Dockerfile 构建期自动运行 runtime pytest 自检 |
```

- [ ] **Step 5: Verify active docs no longer advertise floating Hermes tags**

Run:

```bash
rtk rg -n "hermes-runtime:dev|hermes-runtime:hermes-main|HERMES_VARIANT=hermes-main|main 分支" runtime/hermes/README.md docs/configuration.md docs/hermes-container.md docs/local-development.md
```

Expected: no output.

- [ ] **Step 6: Commit docs**

Run:

```bash
rtk git add runtime/hermes/README.md docs/configuration.md docs/hermes-container.md docs/local-development.md
rtk git commit -m "docs(hermes): 更新版本化镜像构建说明" -m "同步 Hermes runtime 目录命名、镜像 tag 规则和本地开发文档，避免文档继续推荐 dev 或 main 语义 tag。"
```

Expected: one commit containing only docs.

## Task 6: Full Verification

**Files:**
- Read-only verification across the changed files.

- [ ] **Step 1: Run all runtime Python tests**

Run:

```bash
rtk bash -lc 'cd runtime/hermes/hermes-v2026.5.16 && python -m pytest tests/ -v'
```

Expected: all runtime tests pass.

- [ ] **Step 2: Run config package tests**

Run:

```bash
rtk go test ./internal/config
```

Expected: `ok` for `oc-manager/internal/config`.

- [ ] **Step 3: Verify Makefile default build command shape**

Run:

```bash
rtk make --dry-run build-hermes-runtime
```

Expected output includes:

```text
-t hermes-runtime:v2026.5.16-dev
--build-arg HERMES_REF=v2026.5.16
--build-arg OC_IMAGE_VARIANT=hermes-v2026.5.16
runtime/hermes/hermes-v2026.5.16
```

- [ ] **Step 4: Verify production image command shape**

Run:

```bash
rtk make --dry-run build-hermes-image
```

Expected output includes:

```text
-t crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes:v2026.5.16-
--build-arg HERMES_REF=v2026.5.16
--build-arg OC_IMAGE_VARIANT=hermes-v2026.5.16
runtime/hermes/hermes-v2026.5.16
```

- [ ] **Step 5: Verify guard rejection**

Run:

```bash
rtk make .guard-hermes-version HERMES_VERSION=latest
```

Expected: command exits non-zero and stderr contains:

```text
Hermes version 不能使用浮动 tag: latest
```

- [ ] **Step 6: Check active files for old floating tag wording**

Run:

```bash
rtk rg -n "hermes-runtime:dev|hermes-runtime:hermes-main|HERMES_VARIANT=hermes-main|main 分支" Makefile config deploy runtime/hermes docs/configuration.md docs/hermes-container.md docs/local-development.md scripts
```

Expected: no output.

- [ ] **Step 7: Check legacy name only appears as compatibility input**

Run:

```bash
rtk rg -n "hermes-main" runtime/hermes/hermes-v2026.5.16
```

Expected output is limited to:

```text
runtime/hermes/hermes-v2026.5.16/CONTRACT.md
runtime/hermes/hermes-v2026.5.16/migrator/__init__.py
runtime/hermes/hermes-v2026.5.16/tests/test_migrator.py
```

- [ ] **Step 8: Build the local runtime image**

Run:

```bash
rtk make build-hermes-runtime
```

Expected: Docker build succeeds and creates `hermes-runtime:v2026.5.16-dev`. The Dockerfile pytest self-check must pass during the build.

- [ ] **Step 9: Verify the built image with the existing script**

Run:

```bash
rtk scripts/verify-hermes-runtime.sh
```

Expected output:

```text
Hermes runtime 镜像验证通过:hermes-runtime:v2026.5.16-dev
```

- [ ] **Step 10: Inspect final git diff**

Run:

```bash
rtk git status --short
rtk git diff --stat HEAD
```

Expected: only files covered by this plan are modified. Existing unrelated untracked files may remain; do not add or delete them.

- [ ] **Step 11: Commit verification-only adjustments if needed**

If verification exposes a small correction in files already covered by this plan, commit it:

```bash
rtk git add Makefile config/manager.yaml config/manager.example.yaml deploy/manage/config/manager.yaml deploy/manage/config/manager.example.yaml internal/config/loader_test.go scripts/verify-hermes-runtime.sh scripts/sync-hermes-runtime-image.sh runtime/hermes/README.md docs/configuration.md docs/hermes-container.md docs/local-development.md runtime/hermes/hermes-v2026.5.16
rtk git commit -m "chore(hermes-runtime): 完成版本化镜像验证修正" -m "根据最终测试和构建验证修正文档、配置或 runtime 细节，保持 Hermes 镜像 tag 全部来自 v2026.5.16。"
```

Expected: commit only if Step 1-10 required changes after the earlier task commits.
