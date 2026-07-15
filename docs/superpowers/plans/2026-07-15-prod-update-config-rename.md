# Production Update Config Command Rename Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将生产配置更新命令从 `make update-config` 硬重命名为 `make prod-update-config`，同步所有有效调用方和操作文档，并彻底移除旧目标。

**Architecture:** 只调整 Makefile 的生产运维入口及其直接调用方，不改变 kubectl 命令、部署流程或配置格式。验证仅解析 Makefile 和执行安全的单目标 dry-run，不运行任何生产写操作，也不 dry-run 可能触发递归 make 的高层生产目标。

**Tech Stack:** GNU Make、Bash、Markdown

---

## Task 1: 硬重命名生产配置更新目标及调用方

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: 确认新目标尚不存在**

Run: `make -n prod-update-config`

Expected: 命令失败，并提示没有 `prod-update-config` 规则。

- [ ] **Step 2: 重命名目标并同步直接调用方**

在 `Makefile` 中完成以下聚焦修改：

- 将 `.PHONY: update-config` 和 `update-config:` 改为 `prod-update-config`。
- 保持目标中的显式生产 kubeconfig、`apply`、`rollout restart` 和 `rollout status` 命令不变。
- 将 `prod-deploy-hermes`、`prod-deploy-hermes-all`、`prod-deploy-aicc-runtime`、`prod-deploy-ops` 的递归调用改为 `$(MAKE) prod-update-config`。
- 同步 Makefile 内的帮助文本、注释和示例，统一使用新命令名。
- 不保留 `update-config` 兼容别名，确保误用旧命令时立即失败。

- [ ] **Step 3: 验证新旧目标边界**

Run: `make -n prod-update-config`

Expected: 仅打印使用显式生产 kubeconfig 的 secret apply、manager-api 滚动重启和状态等待命令，不实际执行。

Run: `make -n update-config`

Expected: 命令失败，并提示没有 `update-config` 规则。

Run: `rg -n '\$\(MAKE\) (prod-)?update-config' Makefile`

Expected: 四个生产部署调用方均调用 `$(MAKE) prod-update-config`，不存在旧调用。

Run: `rg -n --pcre2 '(?<!prod-)update-config' Makefile`

Expected: 无输出并返回退出码 1，表示 Makefile 中没有残留旧名称。

> 安全约束：不要对上述高层生产部署目标执行 `make -n`。GNU Make 可能执行包含 `$(MAKE)` 的递归 recipe，dry-run 不能作为这些目标绝对不会触发构建、推送或部署的保证。

## Task 2: 同步当前有效文档和本地生产操作指引

**Files:**
- Modify: `docs/logging-conventions.md`
- Modify locally only: `.agents/skills/prod-cluster-ops/SKILL.md`（该文件被 git 忽略，禁止强制加入提交）

- [ ] **Step 1: 记录修改前的有效旧引用**

Run: `rg -n --pcre2 '(?<!prod-)update-config' Makefile docs/logging-conventions.md .agents/skills/prod-cluster-ops/SKILL.md`

Expected: 当前有效文档或本地生产操作指引中仍能找到旧命令引用。

- [ ] **Step 2: 将有效操作说明统一为新名称**

将 `docs/logging-conventions.md` 和 `.agents/skills/prod-cluster-ops/SKILL.md` 中用于当前操作的 `make update-config` 全部改为 `make prod-update-config`。

不要修改 `docs/superpowers/specs/` 和 `docs/superpowers/plans/` 中记录历史讨论或决策过程的旧引用；这些文件不是当前命令入口。

- [ ] **Step 3: 验证有效入口不存在旧引用**

Run: `rg -n --pcre2 '(?<!prod-)update-config' Makefile docs/logging-conventions.md .agents/skills/prod-cluster-ops/SKILL.md`

Expected: 无输出并返回退出码 1。

Run: `git diff --check`

Expected: 无输出并返回退出码 0。

- [ ] **Step 4: 提交实现改动**

Run: `git add Makefile docs/logging-conventions.md && git commit -m "refactor(ops): 重命名生产配置更新命令" -m "将生产配置更新入口硬重命名为 prod-update-config，并同步生产部署调用方与当前操作文档。\n\n旧 update-config 目标不保留兼容别名；验证仅使用安全 dry-run，未执行任何生产操作。"`

Expected: 仅提交 Makefile 和受版本控制的有效文档；不得使用 `git add -f`，不得提交 `.agents/skills/prod-cluster-ops/SKILL.md` 或生产 secret。

## Task 3: 最终安全验证

- [ ] **Step 1: 复核新目标展开结果**

Run: `make -n prod-update-config`

Expected: 打印预期的三类生产操作命令，且进程不连接集群、不修改任何线上资源。

- [ ] **Step 2: 复核旧目标已移除**

Run: `if make -n update-config >/tmp/ocm-update-config.out 2>&1; then cat /tmp/ocm-update-config.out; exit 1; else cat /tmp/ocm-update-config.out; fi`

Expected: 输出没有旧目标规则的错误信息，整体检查返回退出码 0。

- [ ] **Step 3: 复核引用和工作区范围**

Run: `rg -n --pcre2 '(?<!prod-)update-config' Makefile docs/logging-conventions.md .agents/skills/prod-cluster-ops/SKILL.md`

Expected: 无输出并返回退出码 1。

Run: `git diff --check && git status --short`

Expected: 没有格式错误；工作区不包含本任务产生的未提交受版本控制改动。既有无关未跟踪文件保持原状。

> 本任务只重命名 CLI 运维入口，不涉及前端或 API 行为，因此不需要浏览器验证或 OpenAPI 生成。整个执行过程禁止运行 `make prod-update-config`、`kubectl apply`、`kubectl rollout restart` 等生产写操作。
