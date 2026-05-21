# 镜像 Tag 增加 Commit ID Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为本仓库构建发布的 4 个镜像 tag 增加当前 git commit 前 8 位，并在 tracked 工作区 dirty 时拒绝发布构建。

**Architecture:** 在 `Makefile` 顶层集中生成 `GIT_COMMIT_SHORT` 和 `IMAGE_TAG`，所有本仓库发布镜像 target 只消费这个统一 tag。新增 `.guard-image-git-state` 作为发布构建入口的前置校验，保证 tag 中的 commit id 对应一个干净的 tracked 工作区。文档同步说明新 tag 形态和 dirty guard 约束。

**Tech Stack:** GNU Make、Git、Docker CLI、Markdown。

---

## Execution Precondition

当前仓库可能存在与本任务无关的 tracked 改动，例如 `web/src/api/hooks/useCron.ts` 和 `web/src/api/hooks/useCron.spec.ts`。执行本计划前不要回滚这些文件；如果它们仍然是 tracked dirty 状态，`.guard-image-git-state` 的失败属于预期行为。`make --dry-run` 只用于观察最终 tag 展开，不会执行 guard；真实发布构建会执行 guard 并拒绝 tracked dirty 状态。

## File Structure

- Modify: `Makefile`
  - 新增统一 tag 变量：`GIT_COMMIT_SHORT`、`IMAGE_TAG`。
  - 新增 git 状态 guard：`.guard-image-git-state`。
  - 普通服务镜像 target 改用 `IMAGE_TAG`。
  - Hermes 生产镜像 `HERMES_IMAGE` 改为 `$(HERMES_VERSION)-$(IMAGE_TAG)`。
- Modify: `README.md`
  - 更新生产镜像 tag 说明和 Hermes 示例输出。
- Modify: `deploy/operations.md`
  - 更新 tag 约定，从 SemVer-only 调整为本仓库发布镜像的 `timestamp-commit8` 规则，同时保留外部依赖镜像必须固定 tag 或 digest 的要求。
- Modify: `deploy/manage/README.md`
  - 更新 `OCM_MANAGER_IMAGE`、`OCM_WEB_IMAGE` 变量说明。
- Modify: `deploy/runtime-agent/README.md`
  - 更新 `OC_RUNTIME_AGENT_IMAGE` 变量说明。

---

### Task 1: Add Makefile Tag Variables And Git Guard

**Files:**
- Modify: `Makefile:10-35`
- Modify: `Makefile:49-70`

- [ ] **Step 1: Capture current Makefile behavior**

Run:

```bash
rtk make --dry-run build-api-image
```

Expected before implementation: output contains an image tag shaped like `YYYY-MM-DD-HH-MM-SS` without an 8-character git suffix.

- [ ] **Step 2: Add generated commit tag variables**

In `Makefile`, directly after `IMAGE_TIMESTAMP`, add:

```make
# 当前 HEAD 的 8 位短 commit id，用于把本仓库构建产物追溯到源码提交。
# 使用 override 防止命令行 GIT_COMMIT_SHORT=main 改写发布镜像 tag。
override GIT_COMMIT_SHORT := $(strip $(shell git rev-parse --short=8 HEAD 2>/dev/null))

# 本仓库发布镜像统一 tag：构建时间戳 + 源码 commit 前 8 位。
# 使用 override 防止命令行 IMAGE_TAG=latest 绕过 tag 规则。
override IMAGE_TAG := $(IMAGE_TIMESTAMP)-$(GIT_COMMIT_SHORT)
```

- [ ] **Step 3: Change Hermes full image tag composition**

Replace the existing `HERMES_IMAGE` definition:

```make
override HERMES_IMAGE := $(HERMES_IMAGE_REPO):$(HERMES_VERSION)-$(IMAGE_TIMESTAMP)
```

with:

```make
override HERMES_IMAGE := $(HERMES_IMAGE_REPO):$(HERMES_VERSION)-$(IMAGE_TAG)
```

Update the nearby comment to:

```make
# hermes tag 形如 v2026.5.16-2026-05-21-12-00-00-be70e40，便于从镜像引用直接看出上游版本和源码提交。
```

- [ ] **Step 4: Add the git state guard**

After `.guard-hermes-version`, add:

```make
.PHONY: .guard-image-git-state
.guard-image-git-state:
	@git rev-parse --is-inside-work-tree >/dev/null 2>&1 || { echo "发布镜像必须在 git worktree 内构建" >&2; exit 1; }
	@test -n "$(GIT_COMMIT_SHORT)" || { echo "无法读取当前 git commit id" >&2; exit 1; }
	@git diff --quiet || { echo "发布镜像前请先提交 tracked 工作区改动" >&2; exit 1; }
	@git diff --cached --quiet || { echo "发布镜像前请先提交 staged 改动" >&2; exit 1; }
```

- [ ] **Step 5: Wire the guard into build entrypoints**

Change target dependency lines to:

```make
build-api-image: .guard-image-git-state ## 本地构建 manager-api 生产镜像，tag 取当前时间戳和 git commit 前 8 位
build-agent-image: .guard-image-git-state ## 本地构建 runtime-agent 生产镜像，tag 取当前时间戳和 git commit 前 8 位
build-web-image: .guard-image-git-state ## 本地构建 manager-web 生产镜像，tag 取当前时间戳和 git commit 前 8 位
build-hermes-image: .guard-image-git-state hermes-inject-contract ## 本地构建 hermes runtime 生产镜像（需 HERMES_VARIANT 指定变体）
```

Do not add `.guard-image-git-state` to `build-hermes-runtime`; it is a local dev stub target and remains outside the release image scope.

- [ ] **Step 6: Run syntax and guard checks**

Run:

```bash
rtk make .guard-hermes-version
rtk git diff --check -- Makefile
```

Expected:

- `make .guard-hermes-version` exits 0.
- `git diff --check -- Makefile` exits 0.

- [ ] **Step 7: Commit Task 1**

Run:

```bash
rtk git add Makefile
rtk git commit -m "fix(make): 增加镜像 tag 提交号变量" -m "为本仓库发布镜像新增 GIT_COMMIT_SHORT 与 IMAGE_TAG，并让 Hermes 生产镜像 tag 追加当前提交前 8 位。" -m "新增发布镜像 git 状态 guard，tracked 或 staged 改动未提交时拒绝构建，确保镜像 tag 可追溯到明确源码提交。"
```

---

### Task 2: Apply IMAGE_TAG To Service Image Targets

**Files:**
- Modify: `Makefile:136-171`
- Modify: `Makefile:210-239`

- [ ] **Step 1: Replace manager-api image references**

In `Makefile`, replace all manager-api release image references:

```make
$(API_IMAGE_REPO):$(IMAGE_TIMESTAMP)
```

with:

```make
$(API_IMAGE_REPO):$(IMAGE_TAG)
```

This applies to:

- `build-api-image`
- `push-api-image`
- `release-api-image`
- `deploy-api` remote `.env` update

- [ ] **Step 2: Replace runtime-agent image references**

In `Makefile`, replace all runtime-agent release image references:

```make
$(AGENT_IMAGE_REPO):$(IMAGE_TIMESTAMP)
```

with:

```make
$(AGENT_IMAGE_REPO):$(IMAGE_TAG)
```

This applies to:

- `build-agent-image`
- `push-agent-image`
- `release-agent-image`
- `deploy-agent` remote `.env` update

- [ ] **Step 3: Replace manager-web image references**

In `Makefile`, replace all manager-web release image references:

```make
$(WEB_IMAGE_REPO):$(IMAGE_TIMESTAMP)
```

with:

```make
$(WEB_IMAGE_REPO):$(IMAGE_TAG)
```

This applies to:

- `build-web-image`
- `push-web-image`
- `release-web-image`
- `deploy-web` remote `.env` update

- [ ] **Step 4: Update timestamp-only comments**

Replace the comment:

```make
# 同一次 make 调用中四个服务共享 IMAGE_TIMESTAMP，保证同批镜像 tag 一致。
```

with:

```make
# 同一次 make 调用中四个服务共享 IMAGE_TAG，保证同批镜像 tag 一致且可追溯到同一源码提交。
```

If any target description still says only “tag 取当前时间戳”, update it to “tag 取当前时间戳和 git commit 前 8 位”.

- [ ] **Step 5: Verify dry-run output after Task 1 commit**

Run after Task 1 is committed. `make --dry-run` does not execute `.guard-image-git-state`; this step only verifies tag expansion:

```bash
rtk make --dry-run build-api-image
rtk make --dry-run release-agent-image
rtk make --dry-run build-web-image
```

Expected:

- `build-api-image` contains `oc-manager-api:YYYY-MM-DD-HH-MM-SS-aaaaaaaa`, where `aaaaaaaa` is 8 lowercase hex characters.
- `release-agent-image` build, push, and echo lines all use the same `oc-manager-agent:YYYY-MM-DD-HH-MM-SS-aaaaaaaa` tag.
- `build-web-image` contains `oc-manager-web:YYYY-MM-DD-HH-MM-SS-aaaaaaaa`.

- [ ] **Step 6: Commit Task 2**

Run:

```bash
rtk git add Makefile
rtk git commit -m "fix(make): 服务镜像 tag 追加提交号" -m "manager-api、runtime-agent、manager-web 的构建、推送和部署引用统一使用 IMAGE_TAG。" -m "IMAGE_TAG 由时间戳和当前 git commit 前 8 位组成，保证同批发布镜像可追溯到同一源码提交。"
```

---

### Task 3: Verify Override Protection And Dirty Guard

**Files:**
- Modify: `Makefile:10-35`
- Modify: `Makefile:49-80`

- [ ] **Step 1: Verify command-line override cannot change generated tags**

Run when tracked worktree is clean:

```bash
rtk bash -lc 'out=$(make --dry-run build-hermes-image IMAGE_TAG=latest GIT_COMMIT_SHORT=main HERMES_IMAGE=example.com/hermes:latest HERMES_IMAGE_REPO=example.com/hermes); printf "%s\n" "$out"; tag_line=$(grep -E " -t \"example.com/hermes:" <<<"$out"); printf "tag_line=%s\n" "$tag_line"; if grep -Eq "example.com/hermes:(latest|.*-main)" <<<"$tag_line"; then echo "unexpected overridden tag" >&2; exit 1; fi; grep -Eq "example.com/hermes:v2026\\.5\\.16-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{2}-[0-9]{2}-[0-9]{2}-[0-9a-f]{8}" <<<"$tag_line"'
```

Expected: command exits 0 and prints a `tag_line` shaped like:

```text
-t "example.com/hermes:v2026.5.16-YYYY-MM-DD-HH-MM-SS-aaaaaaaa" \
```

- [ ] **Step 2: Verify tracked dirty state fails**

Run this controlled probe. It temporarily appends a comment to `Makefile`, runs the real build target, then restores the file from a temporary copy. The guard must fail before Docker build starts:

```bash
rtk bash -lc 'tmp=$(mktemp); cp Makefile "$tmp"; printf "\n# dirty guard probe\n" >> Makefile; set +e; out=$(make build-api-image 2>&1); code=$?; cp "$tmp" Makefile; rm "$tmp"; printf "%s\n" "$out"; test $code -ne 0; grep -Fq "发布镜像前请先提交 tracked 工作区改动" <<<"$out"; if grep -Fq "docker build" <<<"$out"; then echo "docker build should not start when tracked dirty" >&2; exit 1; fi'
```

Expected: command exits 0, and the captured Make output includes:

```text
发布镜像前请先提交 tracked 工作区改动
```

- [ ] **Step 3: Verify staged dirty state fails**

Run this controlled probe. It temporarily appends a comment to `Makefile`, stages it, runs the real build target, then restores the index and working tree using the saved copy. The guard must fail before Docker build starts:

```bash
rtk bash -lc 'tmp=$(mktemp); cp Makefile "$tmp"; printf "\n# staged guard probe\n" >> Makefile; git add Makefile; set +e; out=$(make build-api-image 2>&1); code=$?; git reset --quiet Makefile; cp "$tmp" Makefile; rm "$tmp"; printf "%s\n" "$out"; test $code -ne 0; grep -Fq "发布镜像前请先提交 staged 改动" <<<"$out"; if grep -Fq "docker build" <<<"$out"; then echo "docker build should not start when staged dirty" >&2; exit 1; fi'
```

Expected: command exits 0, and the captured Make output includes:

```text
发布镜像前请先提交 staged 改动
```

- [ ] **Step 4: Re-check Makefile after probes**

Run:

```bash
rtk git diff -- Makefile
rtk git diff --cached -- Makefile
```

Expected: both commands print no diff for `Makefile`.

- [ ] **Step 5: Commit Task 3 if Makefile needed corrections**

If Steps 1-4 revealed a guard or override bug and you changed `Makefile`, run:

```bash
rtk git add Makefile
rtk git commit -m "fix(make): 防止镜像 tag 覆盖绕过" -m "补齐 IMAGE_TAG、GIT_COMMIT_SHORT 与 HERMES_IMAGE 的 override 和 dirty guard 验证，避免命令行覆盖生成不可追溯镜像 tag。"
```

If no file changed, do not create an empty commit.

---

### Task 4: Update Documentation

**Files:**
- Modify: `README.md:150-213`
- Modify: `deploy/operations.md:140-180`
- Modify: `deploy/manage/README.md:25-33`
- Modify: `deploy/runtime-agent/README.md:22-28`

- [ ] **Step 1: Update README Hermes build example**

In `README.md`, update the Hermes Makefile output comment from:

```text
oc-manager-hermes:v2026.5.16-<timestamp>
```

to:

```text
oc-manager-hermes:v2026.5.16-<timestamp>-<commit8>
```

- [ ] **Step 2: Update README production tag guidance**

Replace the sentence that says:

```text
推送到镜像仓库后，写入对应运行包 `.env`：把 4 个私有镜像的 `:CHANGE_ME_TAG` 替换成具体版本 tag（如 `:v1.0.0`），更严格的环境可进一步固定到 `@sha256:` digest。**生产禁止使用 `:latest`、分支 tag 或版本族 tag**。
```

with:

```text
推送到镜像仓库后，写入对应运行包 `.env`：本仓库构建的 4 个私有镜像使用 `:<timestamp>-<commit8>` tag，Hermes runtime 使用 `:v2026.5.16-<timestamp>-<commit8>` tag；更严格的环境可进一步固定到 `@sha256:` digest。**生产禁止使用 `:latest`、分支 tag 或版本族 tag**。
```

- [ ] **Step 3: Update deploy operations tag convention**

In `deploy/operations.md`, replace the CI SemVer-only paragraph under `### 4.2 镜像 tag 约定` with:

```markdown
本仓库构建发布的镜像 tag 使用 `timestamp-commit8`：

- manager-api / manager-web / runtime-agent：`2026-05-21-12-00-00-be70e40`
- Hermes runtime：`v2026.5.16-2026-05-21-12-00-00-be70e40`

`commit8` 来自当前 git `HEAD` 的前 8 位。发布构建要求 tracked 工作区干净，否则 Makefile 会拒绝构建，避免镜像 tag 指向一个并不包含本地改动的 commit。

外部依赖镜像仍应使用上游具体版本 tag 或 `@sha256:<digest>`。
**生产禁止使用 `:latest`、分支 tag 或版本族 tag**（例如 `:2`、`:2.1`、`:stable`），
因为它们会随上游推送悄悄变更，破坏重启 / 扩容 / 回滚的可复现性。
```

Keep the following digest recommendation paragraph unchanged:

```markdown
进一步推荐把镜像引用固定到内容寻址的 `@sha256:<digest>`，
但前提是你的 CI 在发布每次新版本时同步记录 digest 并写入 `.env`。
```

- [ ] **Step 4: Update deploy/manage image variable descriptions**

In `deploy/manage/README.md`, update only these two table rows:

```markdown
| `OCM_MANAGER_IMAGE` | manager-api 生产镜像，aliyun ACR 私有仓库，使用 `timestamp-commit8` tag 或 `@sha256:` digest（禁用 `latest`） |
| `OCM_WEB_IMAGE` | manager-web 生产镜像，aliyun ACR 私有仓库，使用 `timestamp-commit8` tag 或 `@sha256:` digest（禁用 `latest`） |
```

Do not change PostgreSQL、Redis、nginx rows, because they are external dependency images.

- [ ] **Step 5: Update deploy/runtime-agent image variable description**

In `deploy/runtime-agent/README.md`, update the `OC_RUNTIME_AGENT_IMAGE` row to:

```markdown
| `OC_RUNTIME_AGENT_IMAGE` | runtime-agent 镜像，aliyun ACR 私有仓库，使用 `timestamp-commit8` tag 或 `@sha256:` digest（禁用 `latest`） |
```

- [ ] **Step 6: Verify documentation wording**

Run:

```bash
rtk rg -n "timestamp>|时间戳|SemVer|v1\\.0\\.0|CHANGE_ME_TAG|commit8|提交" README.md deploy/operations.md deploy/manage/README.md deploy/runtime-agent/README.md
rtk git diff --check -- README.md deploy/operations.md deploy/manage/README.md deploy/runtime-agent/README.md
```

Expected:

- No remaining documentation says the 4 repository-built production images use only timestamp or SemVer tag.
- External dependency rows still allow specific upstream tags or digest.
- `git diff --check` exits 0.

- [ ] **Step 7: Commit Task 4**

Run:

```bash
rtk git add README.md deploy/operations.md deploy/manage/README.md deploy/runtime-agent/README.md
rtk git commit -m "docs(image): 说明镜像 tag 提交号规则" -m "更新生产镜像构建与部署文档，说明本仓库构建的镜像使用时间戳加 git commit 前 8 位作为 tag。" -m "保留外部依赖镜像固定具体 tag 或 digest 的约束，并说明 dirty tracked 工作区会阻止发布构建。"
```

---

### Task 5: Final Verification And Handoff

**Files:**
- Verify: `Makefile`
- Verify: `README.md`
- Verify: `deploy/operations.md`
- Verify: `deploy/manage/README.md`
- Verify: `deploy/runtime-agent/README.md`

- [ ] **Step 1: Run final dry-run verification**

Run after the implementation and documentation commits. `make --dry-run` does not execute `.guard-image-git-state`; this step only verifies tag expansion:

```bash
rtk make --dry-run build-api-image
rtk make --dry-run release-agent-image
rtk make --dry-run build-web-image
rtk make --dry-run build-hermes-image
```

Expected:

- `build-api-image` uses `oc-manager-api:YYYY-MM-DD-HH-MM-SS-aaaaaaaa`.
- `release-agent-image` build, push, and echo use the same `oc-manager-agent:YYYY-MM-DD-HH-MM-SS-aaaaaaaa`.
- `build-web-image` uses `oc-manager-web:YYYY-MM-DD-HH-MM-SS-aaaaaaaa`.
- `build-hermes-image` uses `oc-manager-hermes:v2026.5.16-YYYY-MM-DD-HH-MM-SS-aaaaaaaa`.

- [ ] **Step 2: Run override verification**

Run:

```bash
rtk bash -lc 'out=$(make --dry-run build-hermes-image IMAGE_TAG=latest GIT_COMMIT_SHORT=main HERMES_IMAGE=example.com/hermes:latest HERMES_IMAGE_REPO=example.com/hermes); tag_line=$(grep -E " -t \"example.com/hermes:" <<<"$out"); printf "%s\n" "$tag_line"; if grep -Eq "example.com/hermes:(latest|.*-main)" <<<"$tag_line"; then echo "unexpected overridden tag" >&2; exit 1; fi; grep -Eq "example.com/hermes:v2026\\.5\\.16-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{2}-[0-9]{2}-[0-9]{2}-[0-9a-f]{8}" <<<"$tag_line"'
```

Expected: command exits 0 and prints a versioned Hermes tag with real `commit8`.

- [ ] **Step 3: Run dirty guard probes**

Run the two controlled probes from Task 3:

```bash
rtk bash -lc 'tmp=$(mktemp); cp Makefile "$tmp"; printf "\n# dirty guard probe\n" >> Makefile; set +e; out=$(make build-api-image 2>&1); code=$?; cp "$tmp" Makefile; rm "$tmp"; printf "%s\n" "$out"; test $code -ne 0; grep -Fq "发布镜像前请先提交 tracked 工作区改动" <<<"$out"; if grep -Fq "docker build" <<<"$out"; then echo "docker build should not start when tracked dirty" >&2; exit 1; fi'
rtk bash -lc 'tmp=$(mktemp); cp Makefile "$tmp"; printf "\n# staged guard probe\n" >> Makefile; git add Makefile; set +e; out=$(make build-api-image 2>&1); code=$?; git reset --quiet Makefile; cp "$tmp" Makefile; rm "$tmp"; printf "%s\n" "$out"; test $code -ne 0; grep -Fq "发布镜像前请先提交 staged 改动" <<<"$out"; if grep -Fq "docker build" <<<"$out"; then echo "docker build should not start when staged dirty" >&2; exit 1; fi'
```

Expected: both commands exit 0, and `Makefile` has no remaining diff afterward.

- [ ] **Step 4: Run final static checks**

Run:

```bash
rtk git diff --check
rtk git status --short --untracked-files=no
```

Expected:

- `git diff --check` exits 0.
- `git status --short --untracked-files=no` shows no tracked changes from this task after the final commit. If unrelated tracked changes remain, list them in the handoff and state that they pre-existed this implementation.

- [ ] **Step 5: Do not run Go tests unless Makefile/docs changes expanded**

This plan changes Makefile and Markdown only. Do not run `go test ./...` as a required gate unless implementation touches Go files. If Go files are accidentally modified, stop and inspect why before continuing.

- [ ] **Step 6: Final handoff**

Report:

- Commits created for Makefile and documentation.
- Dry-run examples showing `timestamp-commit8`.
- Dirty guard probe results.
- Any unrelated tracked or untracked files left in the worktree.
