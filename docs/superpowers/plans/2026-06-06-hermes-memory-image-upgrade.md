# Hermes Memory Image Upgrade Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve Hermes long-term memory across image updates and document the required capabilities for future Hermes runtime images.

**Architecture:** Long-term memory is app runtime data, not image data. The ops sidecar persists and restores `/opt/data/memories/`, `/opt/data/MEMORY.md`, and `/opt/data/USER.md` under the existing `apps/<appID>/` S3 prefix, while restart cleanup remains scoped to session snapshots (`sessions/`, `state.db*`). `runtime/hermes/AGENTS.md` records the stable image contract for future variants.

**Tech Stack:** Go tests with `testify`, Bash ops scripts, Docker ops image, S3-compatible object storage, Kubernetes `Recreate` app pods, Hermes runtime Python entrypoint.

---

## File Structure

- Create `runtime/hermes/AGENTS.md`: tree-local instructions for every future Hermes variant under `runtime/hermes/**`.
- Modify `runtime/ops/bin/oc-lib.sh`: add reusable long-term memory sync and restore helpers.
- Modify `runtime/ops/bin/oc-restore`: restore long-term memory before Hermes starts.
- Modify `runtime/ops/bin/oc-sync`: upload long-term memory during normal sidecar sync and one-shot test sync.
- Modify `runtime/ops/bin/oc-presync`: upload long-term memory during pod termination.
- Modify `runtime/ops/README.md`: document long-term memory as a persisted app data class and keep the ops image boundary explicit.
- Modify `runtime/ops/test/ops_integration_test.go`: prove restore and sync include long-term memory.
- Modify `internal/worker/handlers/app_runtime_ops_test.go`: protect the cleanup boundary so restart cleanup does not target long-term memory.

## Task 1: Add Hermes Runtime AGENTS Contract

**Files:**
- Create: `runtime/hermes/AGENTS.md`
- Verify: `runtime/hermes/README.md`

- [ ] **Step 1: Create the Hermes tree contract**

Create `runtime/hermes/AGENTS.md` with this exact content:

```markdown
# AGENTS.md

本文件约束 `runtime/hermes/**` 下所有 Hermes runtime variant。更深层目录如果新增
`AGENTS.md`，以更近层级文件为准。

## 基本原则

- 每个 Hermes variant 必须自包含：镜像构建、入口脚本、renderer、migrator、ocops 能力、测试和版本契约都放在自己的 variant 目录内。
- 不复用 `main`、`master`、`latest`、`dev` 等浮动上游 ref 作为生产镜像来源；生产镜像必须能追溯到固定 Hermes ref、variant 版本和 manager 源码 commit。
- 不把 app 运行时数据写进镜像层；镜像只提供代码、工具、默认模板和内置能力。
- 不在 renderer 中覆盖 Hermes 长期记忆；长期记忆由 Hermes 进程或其 memory/user_profile 能力维护。
- 新增或修改 variant 时，优先复用当前 variant 的目录结构和测试习惯，不做无关重排。

## 目录与版本约定

- variant 目录命名形如 `hermes-v2026.5.16`。
- 目录名中的版本必须与同目录 `version.txt` 的版本一致，但 `version.txt` 只写 `v2026.5.16` 这类裸版本。
- 每个 variant 必须包含：
  - `Dockerfile`
  - `version.txt`
  - `CONTRACT.md`
  - `oc-entrypoint.py`
  - `renderer/`
  - `migrator/`
  - `ocops/`
  - `tests/`
- `CONTRACT.md` 记录该 variant 的上游 ref、安装方式、对外命令、迁移说明和版本特有差异。
- 顶层 `runtime/hermes/README.md` 只说明通用目录和构建入口；variant 细节放进对应 `CONTRACT.md`。

## `/opt/data` 持久化边界

Hermes app 的运行时数据根目录是 `/opt/data`。镜像更新时必须区分以下数据类别。

### 必须保留的长期记忆

以下路径属于用户长期偏好、稳定事实和用户画像，必须通过 app 级持久化机制保存到 S3，并在新 pod 启动前恢复：

- `/opt/data/memories/`
- `/opt/data/MEMORY.md`
- `/opt/data/USER.md`

这些路径不存在时视为首启或尚未产生长期记忆，不应阻塞启动。

### 可以清理的会话快照

以下路径属于会话级状态，可能冻结旧 `SOUL.md`、旧模型配置或旧平台规则。配置变更或镜像升级需要新 session 时，可以清理：

- `/opt/data/sessions/`
- `/opt/data/state.db`
- `/opt/data/state.db-shm`
- `/opt/data/state.db-wal`

清理逻辑必须只命中会话快照，不得扩大到长期记忆、workspace、kanban 或渠道凭证。

### 启动时重渲染的文件

以下路径由 `oc-entrypoint` 从 `/opt/oc-input` 和 Hermes 自管数据生成，新镜像启动时应由当前镜像重渲染：

- `/opt/data/config.yaml`
- `/opt/data/SOUL.md`
- `/opt/data/.env`
- `/opt/data/skills/oc-kb/`

renderer 只拥有这些由它生成的输出；不得把 `MEMORY.md`、`USER.md` 或 `memories/` 当作 renderer 输出。

### 敏感数据

以下敏感数据不得写入 S3 或镜像层：

- new-api `api_key`
- app control token
- RAGFlow API key
- manager 内部管理凭证

这些值由 manager bootstrap 通过认证通道下发，DB 加密字段是持久真相源。

## S3 保存与恢复

app 级运行时数据使用 `apps/<appID>/` S3 前缀。

- `runtime/ops` 镜像负责通用恢复和同步命令：`oc-restore`、`oc-sync`、`oc-presync`。
- `oc-restore` 在 initContainer 中运行，负责调用 bootstrap、写入 `/opt/oc-input`，并把 S3 中的 app 数据恢复到 `/opt/data`。
- `oc-sync` 在 sidecar 中运行，负责周期性同步 `/opt/data` 中需要保留的非敏感数据。
- `oc-presync` 在 preStop hook 中运行，负责旧 pod 终止前做最后一次同步。
- 长期记忆必须纳入 `oc-sync` 与 `oc-presync` 的上传范围，并纳入 `oc-restore` 的恢复范围。
- S3 恢复失败时应让 initContainer 失败，不得静默启动一个空记忆实例。
- `sessions/` 与 `state.db*` 可以同步为会话快照，但不能被描述或处理为长期记忆。

## 对外能力

每个 Hermes variant 镜像必须提供以下能力：

- 主容器入口：`ENTRYPOINT` 执行 `oc-entrypoint`，最终启动 `hermes gateway run`。
- 健康检查：`oc-healthcheck` 或等价命令，能判断 Hermes gateway 是否可用。
- 知识库 CLI：`oc-kb`，只调用 manager runtime API，不直接持有 RAGFlow API key。
- `ocops.server`：随 Hermes variant 版本走的 HTTP 控制面，使用 Bearer `OC_OPS_TOKEN` 鉴权。
- `ocops.server` 至少覆盖当前 manager 依赖的 info、doctor、cron、kanban、channel、skills 能力。

`runtime/ops` 镜像不跟 Hermes variant 版本走。它是平台基础设施镜像，跟 manager bootstrap、S3 key 约定和 k8s 编排契约保持兼容。

`ocops.server` 当前必须保留在 Hermes 镜像内，因为它直接操作该 variant 的 Python 包、Hermes 内部命令、`/opt/data` 布局、cron、kanban、channel 和 skill 热加载能力。若未来拆到独立 ops 镜像，需要先定义跨镜像读写 `/opt/data` 的稳定 ABI。

## 启动流程

Hermes 主容器启动顺序必须保持：

1. 读取 `/opt/oc-input/manifest.yaml`。
2. 读取 `/opt/data/.oc-state.json`。
3. 若本地记录的 variant 与当前镜像 variant 不一致，执行 `migrator`。
4. 渲染 `/opt/data/config.yaml`、`/opt/data/.env`、`/opt/data/SOUL.md`、`/opt/data/skills/oc-kb/`。
5. 写回 `.oc-state.json`。
6. exec `hermes gateway run`。

如果 migrator 失败，必须阻止 Hermes 启动并保留原始 `/opt/data`。

## 升级兼容

- 新 variant 必须能读取上一个已发布 variant 的 `/opt/data`。
- 持久化格式不兼容时，必须在新 variant 的 `migrator/` 中做原地迁移。
- migrator 必须幂等，重复启动不能重复破坏数据。
- migrator 不得删除长期记忆；需要改格式时必须保留原始语义。
- `.oc-state.json` 是判断本地数据 variant 的状态锚点，不得随意改字段语义。

## 测试要求

新增或修改 Hermes variant 时至少确认以下测试：

- renderer 不覆盖 `/opt/data/memories/`、`/opt/data/MEMORY.md`、`/opt/data/USER.md`。
- migrator 覆盖首启、同版本启动、跨版本迁移、重复运行。
- `ocops.server` 覆盖 manager 当前调用的 HTTP 端点。
- ops 恢复/同步覆盖长期记忆、workspace、weixin 凭证、自创 skill、会话快照和 sqlite 快照。
- 镜像构建阶段必须运行 variant 自检，失败不得产出生产镜像。
```

- [ ] **Step 2: Verify the new document has the expected sections**

Run:

```bash
rg -n "长期记忆|runtime/ops|ocops.server|S3 保存与恢复|升级兼容" runtime/hermes/AGENTS.md
```

Expected: each pattern appears at least once in `runtime/hermes/AGENTS.md`.

- [ ] **Step 3: Commit the contract document**

Run:

```bash
git add runtime/hermes/AGENTS.md
git commit -m "docs(hermes): 增加运行时镜像能力契约" -m "在 runtime/hermes 下新增 AGENTS.md，明确未来 Hermes variant 的目录、持久化、S3、接口、恢复和迁移要求。"
```

Expected: commit succeeds and includes only `runtime/hermes/AGENTS.md`.

## Task 2: Persist Long-Term Memory Through Ops Sync and Restore

**Files:**
- Modify: `runtime/ops/test/ops_integration_test.go`
- Modify: `runtime/ops/bin/oc-lib.sh`
- Modify: `runtime/ops/bin/oc-restore`
- Modify: `runtime/ops/bin/oc-sync`
- Modify: `runtime/ops/bin/oc-presync`
- Modify: `runtime/ops/README.md`

- [ ] **Step 1: Extend restore integration test with long-term memory fixtures**

In `runtime/ops/test/ops_integration_test.go`, inside `TestOcRestore`, after the existing `state.db` object setup:

```go
	// 预置长期记忆：目录型 memories/ 与根级 MEMORY.md / USER.md 都必须随镜像更新恢复。
	require.NoError(t, store.PutObject(ctx, appPrefix+"memories/profile.json", strings.NewReader("MEMORY-DIR"), int64(len("MEMORY-DIR"))))
	require.NoError(t, store.PutObject(ctx, appPrefix+"MEMORY.md", strings.NewReader("ROOT-MEMORY"), int64(len("ROOT-MEMORY"))))
	require.NoError(t, store.PutObject(ctx, appPrefix+"USER.md", strings.NewReader("ROOT-USER"), int64(len("ROOT-USER"))))
```

After the existing workspace restore assertion:

```go
	// 断言：长期记忆完整恢复到 /opt/data，避免镜像更新后丢失稳定偏好与用户画像。
	assertFileContains(t, filepath.Join(dataDir, "memories/profile.json"), "MEMORY-DIR")
	assertFileContains(t, filepath.Join(dataDir, "MEMORY.md"), "ROOT-MEMORY")
	assertFileContains(t, filepath.Join(dataDir, "USER.md"), "ROOT-USER")
```

- [ ] **Step 2: Extend sync integration test with long-term memory fixtures**

In `TestOcSyncOnce`, after the existing `sessions/req.json` setup:

```go
	// 预置长期记忆：目录型 memories/ 与根级 MEMORY.md / USER.md 都应上传到 app S3 前缀。
	require.NoError(t, os.MkdirAll(filepath.Join(dataDir, "memories"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "memories/profile.json"), []byte("MEMORY-DIR"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "MEMORY.md"), []byte("ROOT-MEMORY"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "USER.md"), []byte("ROOT-USER"), 0o644))
```

After the existing sessions upload assertion:

```go
	// 断言长期记忆上行：目录与根级文件都必须存在于 apps/<id>/ 前缀。
	memDirExists, err := store.ObjectExists(ctx, appPrefix+"memories/profile.json")
	require.NoError(t, err)
	assert.True(t, memDirExists, "memories/profile.json 应已上传")
	memFileExists, err := store.ObjectExists(ctx, appPrefix+"MEMORY.md")
	require.NoError(t, err)
	assert.True(t, memFileExists, "MEMORY.md 应已上传")
	userFileExists, err := store.ObjectExists(ctx, appPrefix+"USER.md")
	require.NoError(t, err)
	assert.True(t, userFileExists, "USER.md 应已上传")
```

- [ ] **Step 3: Run the focused ops integration tests and observe failure or skip**

Run:

```bash
go test ./runtime/ops/test -run 'TestOcRestore|TestOcSyncOnce' -v
```

Expected before implementation:

- If `OC_S3_TEST_ENDPOINT` is not set: tests skip with `未设置 OC_S3_TEST_ENDPOINT`.
- If S3 test env is configured: `TestOcRestore` fails because long-term memory files are not restored, and `TestOcSyncOnce` fails because long-term memory objects are not uploaded.

- [ ] **Step 4: Add long-term memory helper functions to `oc-lib.sh`**

In `runtime/ops/bin/oc-lib.sh`, after `sync_weixin_up`, add:

```bash
# sync_longterm_memory_up 把 Hermes 长期记忆上传到 app S3 前缀。
# memories/ 是目录型长期记忆；MEMORY.md / USER.md 是根级长期记忆与用户画像文件。
# 不使用 --delete，保持与 workspace/sessions 的保守同步策略一致，避免误删稳定记忆。
sync_longterm_memory_up() {
  local data_dir="$1"
  if [ -d "$data_dir/memories" ]; then
    aws_s3 sync "$data_dir/memories" "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}memories/"
  fi
  local file
  for file in MEMORY.md USER.md; do
    [ -f "$data_dir/$file" ] || continue
    aws_s3 cp "$data_dir/$file" "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}${file}"
  done
}

# restore_longterm_memory_down 从 app S3 前缀恢复 Hermes 长期记忆。
# 根级 MEMORY.md / USER.md 仅在对象存在时下载；不存在按首启或未生成记忆处理。
restore_longterm_memory_down() {
  local data_dir="$1"
  mkdir -p "$data_dir/memories"
  aws_s3 sync "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}memories/" "$data_dir/memories"
  local file
  for file in MEMORY.md USER.md; do
    if aws_s3 ls "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}${file}" >/dev/null 2>&1; then
      aws_s3 cp "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}${file}" "$data_dir/$file"
    fi
  done
}
```

- [ ] **Step 5: Restore long-term memory in `oc-restore`**

In `runtime/ops/bin/oc-restore`, replace:

```bash
mkdir -p "$INPUT_DIR/resources/skills" "$DATA_DIR/workspace" "$DATA_DIR/sessions" "$DATA_DIR/weixin"
```

with:

```bash
mkdir -p "$INPUT_DIR/resources/skills" "$DATA_DIR/workspace" "$DATA_DIR/sessions" "$DATA_DIR/weixin" "$DATA_DIR/memories"
```

Replace:

```bash
restore_user_skills "$DATA_DIR"
log "workspace/sessions/weixin/skills 已恢复"
```

with:

```bash
restore_longterm_memory_down "$DATA_DIR"
restore_user_skills "$DATA_DIR"
log "workspace/sessions/weixin/长期记忆/skills 已恢复"
```

- [ ] **Step 6: Upload long-term memory in `oc-sync`**

In `runtime/ops/bin/oc-sync`, update the top comment so the persistence list includes long-term memory:

```bash
# workspace、sessions、weixin、长期记忆与自创 skill 增量同步（无 --delete）每 OC_SYNC_INTERVAL 秒一次；
# sqlite 一致性快照每 OC_SQLITE_INTERVAL 秒一次。OC_SYNC_ONCE=1 时只跑一轮后退出，供测试。
```

In `run_once`, after `sync_weixin_up "$DATA_DIR"`:

```bash
  sync_longterm_memory_up "$DATA_DIR"
```

In the `OC_SYNC_ONCE=1` block, after `sync_weixin_up "$DATA_DIR"`:

```bash
  sync_longterm_memory_up "$DATA_DIR"
```

- [ ] **Step 7: Upload long-term memory in `oc-presync`**

In `runtime/ops/bin/oc-presync`, replace the first comment line:

```bash
# oc-presync — pod preStop hook 入口：优雅终止前做一次全量同步（workspace + sessions）+ sqlite 快照，零丢失（父设计 D17）。
```

with:

```bash
# oc-presync — pod preStop hook 入口：优雅终止前做一次全量同步（workspace + sessions + 长期记忆）+ sqlite 快照，零丢失（父设计 D17）。
```

Replace:

```bash
log "preStop：全量同步 workspace + sessions + weixin 凭证 + sqlite 快照"
sync_workspace_up "$DATA_DIR"
sync_sessions_up "$DATA_DIR"
sync_weixin_up "$DATA_DIR"
backup_sqlite_up "$DATA_DIR"
```

with:

```bash
log "preStop：全量同步 workspace + sessions + weixin 凭证 + 长期记忆 + sqlite 快照"
sync_workspace_up "$DATA_DIR"
sync_sessions_up "$DATA_DIR"
sync_weixin_up "$DATA_DIR"
sync_longterm_memory_up "$DATA_DIR"
backup_sqlite_up "$DATA_DIR"
```

- [ ] **Step 8: Update the ops README data contract**

In `runtime/ops/README.md`, update the `data` volume row to include long-term memory:

```markdown
| `data` | `/opt/data` | `workspace/`（hermes 工作区）；`memories/` / `MEMORY.md` / `USER.md`（长期记忆）；`sessions/`（会话存档）；`state.db`（sqlite 状态库）；`weixin/`（渠道凭证）；`skills/`（自创 skill） | initContainer **写**（恢复数据）；hermes 主容器**读写**（正常运行）；sidecar `s3-sync` **读**（增量上传）；sidecar `oc-ops` **读写**（spec-E，沿用 spec-D 契约） |
```

In the `oc-restore` behavior list, replace the line that only names workspace and sessions with:

```markdown
6. `aws s3 sync` 恢复 `apps/<id>/workspace/`、`apps/<id>/sessions/`、`apps/<id>/weixin/`、`apps/<id>/memories/` 到 `/opt/data`，并按对象存在性恢复 `MEMORY.md` / `USER.md`（见 §6.3）。
```

In the `oc-sync` behavior paragraph, replace the persistence sentence with:

```markdown
- 每 `OC_SYNC_INTERVAL`（默认 8s）循环：先 `ensure_creds`（凭证临近过期时自动续期），然后同步 workspace、sessions、weixin 凭证、长期记忆和自创 skill；每 `OC_SQLITE_INTERVAL`（默认 30s）触发一次 `backup_sqlite_up`（sqlite 一致性快照上传）。
```

In the S3 mapping table, add these rows:

```markdown
| 长期记忆目录 | `apps/<id>/memories/` | `/opt/data/memories/` | `aws s3 sync`（增量下载） |
| 长期记忆文件 | `apps/<id>/MEMORY.md` / `apps/<id>/USER.md` | `/opt/data/MEMORY.md` / `/opt/data/USER.md` | 对象存在时 `aws s3 cp` |
```

In the sync table, add these rows:

```markdown
| 长期记忆目录 | `apps/<id>/memories/` | `/opt/data/memories/` | `aws s3 sync`，无 `--delete` |
| 长期记忆文件 | `apps/<id>/MEMORY.md` / `apps/<id>/USER.md` | `/opt/data/MEMORY.md` / `/opt/data/USER.md` | 文件存在时 `aws s3 cp` |
```

- [ ] **Step 9: Run focused verification**

Run:

```bash
go test ./runtime/ops/test -run 'TestOcRestore|TestOcSyncOnce' -v
```

Expected:

- If S3 env is configured: both tests pass.
- If S3 env is not configured: tests skip with `未设置 OC_S3_TEST_ENDPOINT`.

Run the shell syntax checks:

```bash
bash -n runtime/ops/bin/oc-lib.sh
bash -n runtime/ops/bin/oc-restore
bash -n runtime/ops/bin/oc-sync
bash -n runtime/ops/bin/oc-presync
```

Expected: no output and exit code 0 for each command.

- [ ] **Step 10: Commit ops memory persistence**

Run:

```bash
git add runtime/ops/bin/oc-lib.sh runtime/ops/bin/oc-restore runtime/ops/bin/oc-sync runtime/ops/bin/oc-presync runtime/ops/README.md runtime/ops/test/ops_integration_test.go
git commit -m "fix(ops): 同步恢复 Hermes 长期记忆" -m "将 /opt/data/memories、MEMORY.md 和 USER.md 纳入 oc-restore、oc-sync 与 oc-presync 的持久化范围。\n\n补充 ops 集成测试和文档，确保镜像更新后长期记忆随 app 级 S3 数据恢复。"
```

Expected: commit succeeds and includes only the listed ops files.

## Task 3: Guard Restart Cleanup Scope

**Files:**
- Modify: `internal/worker/handlers/app_runtime_ops_test.go`

- [ ] **Step 1: Add assertions that restart cleanup does not target long-term memory**

In `TestAppRestartContainerHandler_ImageUnchanged_DeletesSessionsThenScales`, after:

```go
	require.True(t, objects.deletedStateDB, "重启时必须清除 S3 state.db")
```

add:

```go
	// 长期记忆不属于会话快照清理范围，避免重启或镜像更新误删稳定偏好与用户画像。
	assert.NotContains(t, objects.deletedPrefixes, storage.AppPrefix(testAppID)+"memories/")
	assert.NotContains(t, objects.deletedPrefixes, storage.AppPrefix(testAppID)+"MEMORY.md")
	assert.NotContains(t, objects.deletedPrefixes, storage.AppPrefix(testAppID)+"USER.md")
```

- [ ] **Step 2: Run the focused restart test**

Run:

```bash
go test ./internal/worker/handlers -run TestAppRestartContainerHandler_ImageUnchanged_DeletesSessionsThenScales -v
```

Expected: PASS.

- [ ] **Step 3: Commit the cleanup guard**

Run:

```bash
git add internal/worker/handlers/app_runtime_ops_test.go
git commit -m "test(hermes): 保护长期记忆不被重启清理" -m "在应用重启清理测试中断言 sessions 与 state.db 清理不会扩大到 memories、MEMORY.md 或 USER.md。\n\n该测试固定长期记忆与会话快照的边界，防止后续修改误删用户稳定记忆。"
```

Expected: commit succeeds and includes only `internal/worker/handlers/app_runtime_ops_test.go`.

## Task 4: Final Verification

**Files:**
- Verify: `runtime/hermes/AGENTS.md`
- Verify: `runtime/ops/bin/oc-lib.sh`
- Verify: `runtime/ops/bin/oc-restore`
- Verify: `runtime/ops/bin/oc-sync`
- Verify: `runtime/ops/bin/oc-presync`
- Verify: `runtime/ops/README.md`
- Verify: `runtime/ops/test/ops_integration_test.go`
- Verify: `internal/worker/handlers/app_runtime_ops_test.go`

- [ ] **Step 1: Check the working tree only has intended changes**

Run:

```bash
git status --short
```

Expected after the task commits: no output. If output appears, every path must be one of the files listed in this plan and should be committed or intentionally left for review.

- [ ] **Step 2: Run focused Go tests**

Run:

```bash
go test ./internal/worker/handlers -run TestAppRestartContainerHandler -v
```

Expected: PASS.

Run:

```bash
go test ./runtime/ops/test -run 'TestOcRestore|TestOcSyncOnce' -v
```

Expected: PASS when S3 env is configured, or SKIP with `未设置 OC_S3_TEST_ENDPOINT` when S3 env is absent.

- [ ] **Step 3: Run script syntax checks**

Run:

```bash
bash -n runtime/ops/bin/oc-lib.sh
bash -n runtime/ops/bin/oc-restore
bash -n runtime/ops/bin/oc-sync
bash -n runtime/ops/bin/oc-presync
```

Expected: no output and exit code 0.

- [ ] **Step 4: Review final diff**

Run:

```bash
git show --stat --oneline HEAD
git log --oneline -4
```

Expected: the recent commits correspond to:

- Hermes AGENTS contract document
- ops long-term memory persistence
- restart cleanup boundary test

No unrelated frontend, OpenAPI, generated API, or deployment secret files should appear.
