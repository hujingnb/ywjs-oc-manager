# Hermes 版本化 Variant 与镜像 Tag · 设计 Spec

> 日期：2026-05-21
> 范围：把 Hermes runtime 从 `hermes-main` 浮动命名迁到版本化 variant，
> 并让本地与生产镜像 tag 都从 Hermes 上游版本号派生，避免继续发布或配置
> `main` 语义的镜像 tag。

## 1 · 背景

当前 Hermes runtime 已通过 `runtime/hermes/hermes-main/version.txt` 锁定上游版本：

```text
v2026.5.16
```

但构建与配置里仍保留 `main` 语义：

- 默认 variant 目录是 `runtime/hermes/hermes-main/`。
- `Makefile` 默认 `HERMES_VARIANT=hermes-main`。
- 本地 dev 镜像 tag 形如 `hermes-runtime:hermes-main-dev`。
- 生产镜像 tag 形如 `oc-manager-hermes:hermes-main-<timestamp>`。
- 文档仍描述 `hermes-main` 对应 upstream main 分支。

这会让部署侧无法从镜像引用判断实际 Hermes 版本，也容易把浮动分支语义误认为可复现版本。

## 2 · 目标 / 非目标

### 2.1 目标

- Hermes variant 身份使用明确版本号：`hermes-v2026.5.16`。
- Docker 镜像 tag 不再包含 `main`：
  - 本地 dev：`hermes-runtime:v2026.5.16-dev`
  - 生产发布：`<HERMES_IMAGE_REPO>:v2026.5.16-<IMAGE_TIMESTAMP>`
- `Makefile` 从当前 variant 的 `version.txt` 派生镜像 tag，并拒绝空值、
  `main`、`master`、`latest` 这类浮动 tag。
- 配置示例、当前部署配置和 Hermes 文档同步使用版本化 variant / tag。
- 已有 `.oc-state.json` 中的 `image_variant=hermes-main` 能平滑迁到
  `hermes-v2026.5.16`。

### 2.2 非目标

- 不更改 Hermes 上游版本；仍使用当前 `version.txt` 的 `v2026.5.16`。
- 不改 manager API 契约，不重新生成 OpenAPI。
- 不新增 per-app 镜像选择 UI。
- 不批量改写历史 spec / plan 中的已完成记录；只更新当前有效文档、配置和代码。

## 3 · 命名规则

| 对象 | 新值 | 说明 |
|---|---|---|
| variant 目录 | `runtime/hermes/hermes-v2026.5.16/` | 目录名体现该 variant 绑定的 Hermes 上游版本 |
| `HERMES_VARIANT` 默认值 | `hermes-v2026.5.16` | 保持 variant 选择机制不变 |
| `version.txt` | `v2026.5.16` | 作为上游 install ref 和镜像 tag 版本来源 |
| 本地 dev 镜像 | `hermes-runtime:v2026.5.16-dev` | 不再用 variant 名拼 tag |
| 生产镜像 | `<repo>:v2026.5.16-<timestamp>` | 保留时间戳用于同版本多次构建区分 |
| 镜像内 `OC_IMAGE_VARIANT` | `hermes-v2026.5.16` | 写入 `/etc/oc-image.json` 和 `.oc-state.json` |

`version.txt` 的内容是镜像 tag 的权威版本来源。实现时不要从目录名反推版本，
避免目录名和上游 ref 双向解析带来的隐藏约束。

## 4 · 架构改动

### 4.1 目录迁移

把 `runtime/hermes/hermes-main/` 重命名为：

```text
runtime/hermes/hermes-v2026.5.16/
```

目录内 Dockerfile、entrypoint、renderer、contract tests 等文件随目录移动。内部注释、
测试断言和默认 fallback 中表示 variant 身份的 `hermes-main` 同步改为
`hermes-v2026.5.16`。

### 4.2 Makefile

`Makefile` 保持 `HERMES_VARIANT` 入口，但新增从 `version.txt` 读取的
`HERMES_VERSION`：

```makefile
HERMES_VARIANT ?= hermes-v2026.5.16
HERMES_VARIANT_DIR := runtime/hermes/$(HERMES_VARIANT)
HERMES_VERSION := $(shell cat $(HERMES_VARIANT_DIR)/version.txt)
HERMES_IMAGE := $(HERMES_IMAGE_REPO):$(HERMES_VERSION)-$(IMAGE_TIMESTAMP)
```

本地 dev 构建改为：

```text
hermes-runtime:$(HERMES_VERSION)-dev
```

Hermes 镜像相关 target 在 docker build 前执行版本 guard：

- `HERMES_VERSION` 不能为空。
- `HERMES_VERSION` 不能是 `main`、`master`、`latest`。
- `HERMES_VARIANT_DIR/version.txt` 不存在时直接失败。

### 4.3 配置和文档

需要更新的当前有效文件包括：

- `config/manager.yaml`
- `config/manager.example.yaml`
- `deploy/manage/config/manager.yaml`
- `deploy/manage/config/manager.example.yaml`
- `docs/configuration.md`
- `docs/hermes-container.md`
- `runtime/hermes/README.md`
- `runtime/hermes/hermes-v2026.5.16/CONTRACT.md`

配置中的 `hermes.runtime_image` 使用版本化 tag。示例配置可以继续用
`CHANGE_ME_TAG` 表示生产占位，但说明文字必须强调不能填 `main` / `latest` 这类浮动 tag。

## 5 · 兼容迁移

这次采用方案 B，variant 身份会从 `hermes-main` 改成 `hermes-v2026.5.16`。
因此新目录的 migrator 必须显式允许一次 no-op 迁移：

```text
prev_variant=hermes-main
curr_variant=hermes-v2026.5.16
```

迁移不改 Hermes 数据文件，只把“历史上同一套镜像资产曾叫 `hermes-main`”这个事实
纳入兼容列表。这样老实例重启时不会因为 `.oc-state.json` 仍记录 `hermes-main`
而失败。

其他未知 `prev_variant` 仍按当前策略失败，避免误把不兼容数据当成可迁移数据。

## 6 · 测试与验证

实现完成后至少验证：

1. `cd runtime/hermes/hermes-v2026.5.16 && python -m pytest tests/ -v`
   通过，覆盖 renderer、migrator、oc-kanban、oc-cron 等镜像内契约测试。
2. 新增或更新 migrator 单测，覆盖：
   - `prev_variant=None` 首次启动；
   - `prev_variant=hermes-v2026.5.16` 同版本重启；
   - `prev_variant=hermes-main` 历史名称 no-op 迁移；
   - 未知旧 variant 继续失败。
3. `make build-hermes-runtime` 默认构建出的本地镜像 tag 为
   `hermes-runtime:v2026.5.16-dev`。
4. `make build-hermes-image` 使用的生产 tag 为
   `<HERMES_IMAGE_REPO>:v2026.5.16-<timestamp>`。
5. `rg "hermes-main|hermes-runtime:hermes-main|main 分支" Makefile config deploy docs runtime/hermes`
   不再命中当前有效配置、文档或代码中的浮动 tag 语义；历史 spec / plan 可保留。

如果 Docker 构建受网络或镜像源影响无法完成，交付说明必须记录失败命令、失败原因和
已完成的替代验证。

## 7 · 风险与取舍

- **风险：目录重命名影响较广。** 方案 B 会修改较多路径引用，需要用 `rg` 全量检查
  当前有效路径。接受该成本是为了让 variant 身份和镜像 tag 完全一致。
- **风险：老数据记录旧 variant。** 通过 `hermes-main → hermes-v2026.5.16` no-op
  migrator 处理，只放行这一种历史名称。
- **取舍：不改历史设计文档。** 历史 spec / plan 记录当时的设计和实施过程，批量改写会制造
  无关 diff；本次只改仍会被开发者或部署流程读取的文件。
