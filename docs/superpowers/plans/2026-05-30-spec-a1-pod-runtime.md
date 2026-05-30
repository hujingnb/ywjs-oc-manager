# spec-A1：app pod 运行时侧（restore/sync 脚本 + ops 镜像）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付一个专用 ops 镜像与 oc-restore/oc-sync/oc-presync 脚本，让 app pod 在启动时从 manager bootstrap 端点拉配置并恢复数据、运行期把产物增量同步回 S3，并钉死 pod 内部契约供 spec-A2 渲染 pod spec。

**Architecture:** 新建 `runtime/ops/`：一个 alpine 基础的 ops 镜像（aws-cli + sqlite3 + jq + curl + bash），打包三个入口脚本（initContainer 的 oc-restore、sidecar 的 oc-sync、preStop 的 oc-presync）+ 共享库 oc-lib.sh。脚本对接 spec-B 的 bootstrap 契约（`docs/bootstrap-http-contract.md`）：skills 用预签名 URL（version 级跨前缀）、workspace/sessions/state.db 用 bootstrap 的 s3_write STS 凭证 `aws s3 sync`/`cp`（apps/<id>/ 前缀内）。验证用 shellcheck + 在 ops 容器内跑的库单测 + 对真实 MinIO 的 Go 集成测。

**Tech Stack:** Docker（alpine）、bash、aws-cli、sqlite3、jq、curl；Go（集成测，复用 `internal/integrations/storage`）；本地 k3d MinIO。

---

## 项目约定（实现者必读）

- **工作分支**：直接在 `master` 上完成，不切 worktree、不建分支。
- **提交规范**：Conventional Commits，第一行中文摘要，空行后中文正文；commit 末尾加 trailer（精确照抄）：
  ```
  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  ```
- **git add 精确文件**：每个 commit 只 `git add` 本任务涉及的文件，**禁止 `git add -A`**，**禁止提交未跟踪的 `docs/reports/`**。
- **shell 风格**：bash（`#!/usr/bin/env bash` + `set -euo pipefail`）；每个脚本/函数都要中文注释说明业务意图。
- **shellcheck**：本机无 shellcheck，统一用 docker 跑：
  `docker run --rm -v "$PWD/runtime/ops:/src" koalaman/shellcheck:stable /src/bin/<script>`（镜像走默认 docker.io；若拉取慢用已配的镜像源）。
- **测试断言**：Go 集成测用 `github.com/stretchr/testify`（`require`/`assert`，`assert.Equal` 顺序 expected 在前）；shell 单测用自定义 assert 函数。
- **本地 MinIO**：`make local-up` 起 k3d + MinIO；MinIO 在 ns `ocm` 的 svc/minio:9000，凭证在 secret `ocm-secrets`（键 `minio-root-user`/`minio-root-password`）；集成测经 `kubectl port-forward svc/minio 9000:9000` 暴露到本机。
- **k3d registry**：`k3d-ocm-registry.localhost:5000`（变量 `K3D_REGISTRY_HOST`）。
- **不破坏现有**：spec-A1 全是新增（`runtime/ops/` + Makefile target），不改 hermes/agent 镜像、不改 manager Go 代码。

## 已钉死的事实（实现按此，不要猜）

- HERMES_HOME=`/opt/data`；app 数据本地路径：`/opt/data/workspace/`、`/opt/data/sessions/`、`/opt/data/state.db`（+ `state.db-wal` / `state.db-shm`）。
- S3 key 约定（spec-B `internal/integrations/storage/keys.go`）：`apps/<id>/workspace`（前缀镜像）、`apps/<id>/sessions`（前缀镜像）、`apps/<id>/state.db`（单对象）、`versions/<vid>/skills/<name>.tar`（skills，version 级）。
- bootstrap 响应 schema（`docs/bootstrap-http-contract.md` §4）：顶层 `manifest_yaml`/`persona`/`platform_rule`/`skills[]{name,rel_path,url}`/`restore{...}`(A1 不用)/`s3_write{endpoint,region,bucket,prefix,access_key_id,secret_access_key,session_token,expires_at}`。
- **恢复机制分流**：skills 用预签名 `url`（STS 读不到 version 级）；workspace/sessions/state.db 用 `s3_write` STS 凭证（apps/<id>/ 前缀内）。
- pod env：`OC_CONTROL_TOKEN`（Bearer 调 bootstrap）、`OC_BOOTSTRAP_URL`（完整 URL）。S3 参数从 bootstrap 的 s3_write 解析，不走 env。

---

## 文件结构

**新建：**
- `runtime/ops/Dockerfile` — alpine + aws-cli/sqlite3/jq/curl/bash/coreutils/tar/ca-certificates；COPY bin/* 到 /usr/local/bin。
- `runtime/ops/bin/oc-lib.sh` — 共享库：日志、bootstrap 拉取、STS 凭证解析/写入、过期判断、aws s3 包装、workspace 同步、sqlite 备份。
- `runtime/ops/bin/oc-restore` — initContainer 入口。
- `runtime/ops/bin/oc-sync` — sidecar 同步循环入口。
- `runtime/ops/bin/oc-presync` — preStop 入口。
- `runtime/ops/README.md` — pod 内部契约（供 spec-A2 渲染）。
- `runtime/ops/test/unit_test.sh` — oc-lib.sh 纯函数 shell 单测（在 ops 容器内跑）。
- `runtime/ops/test/ops_integration_test.go` — 对真实 MinIO + mock bootstrap 的 Go 集成测（环境门控）。

**修改：**
- `Makefile` — 新增 `build-ops-runtime`（构建 + 推 registry）+ `local-build-ops`（推 k3d registry）。

---

## Phase 1：ops 镜像 + 共享库

### Task 1: ops 镜像骨架与 oc-lib.sh

**Files:**
- Create: `runtime/ops/Dockerfile`
- Create: `runtime/ops/bin/oc-lib.sh`
- Create: `runtime/ops/bin/oc-restore` / `oc-sync` / `oc-presync`（本任务先建占位，Task 3/4/5 替换为实体）

- [ ] **Step 0: 先建三个入口占位（使镜像可构建）**

Dockerfile 的 COPY 会引用四个脚本；本任务只实现 oc-lib.sh 实体，三个入口先建占位，避免 build 因缺文件失败。三个文件 `runtime/ops/bin/oc-restore`、`oc-sync`、`oc-presync` 内容均为：
```bash
#!/usr/bin/env bash
# 占位：实体见 Task 3/4/5。
set -euo pipefail
echo "oc-* 入口尚未实现" >&2
exit 1
```

- [ ] **Step 1: 写 oc-lib.sh（共享函数）**

```bash
#!/usr/bin/env bash
# oc-lib.sh — ops 脚本共享函数库。由 oc-restore / oc-sync / oc-presync 通过 source 引入。
# 职责：日志、bootstrap 拉取与重试、STS 凭证解析/写入与过期判断、aws s3 调用包装、
# workspace 同步与 sqlite 一致性备份。所有函数无副作用地依赖调用方已 export 的 env。
set -euo pipefail

# log 输出带 UTC 时间戳的日志到 stderr（不污染脚本 stdout）。
log() { printf '[%s] %s\n' "$(date -u +%FT%TZ)" "$*" >&2; }

# require_env 校验列出的环境变量均非空，缺失则报错返回 1。
require_env() {
  local v
  for v in "$@"; do
    [ -n "${!v:-}" ] || { log "缺少必需环境变量: $v"; return 1; }
  done
}

# fetch_bootstrap <out_file> 带 Bearer control token 调 bootstrap，指数退避重试有限次，
# 成功把响应 JSON 写入 out_file 并返回 0；最终失败返回 1（调用方据此决定是否非零退出）。
fetch_bootstrap() {
  local out="$1" attempt=0 max="${OC_BOOTSTRAP_RETRIES:-5}" delay=1
  while :; do
    if curl -fsS -H "Authorization: Bearer ${OC_CONTROL_TOKEN}" "${OC_BOOTSTRAP_URL}" -o "$out"; then
      return 0
    fi
    attempt=$((attempt + 1))
    if [ "$attempt" -ge "$max" ]; then
      log "bootstrap 拉取失败，已重试 ${max} 次"
      return 1
    fi
    log "bootstrap 拉取失败，第 ${attempt}/${max} 次，${delay}s 后重试"
    sleep "$delay"
    delay=$((delay * 2))
  done
}

# s3_field <json> <field> 输出 s3_write.<field>（endpoint/region/bucket/prefix 等）。
s3_field() { jq -r ".s3_write.$2" "$1"; }

# export_s3_env <json> 从 s3_write 导出 aws_s3 包装所需的 S3 参数到当前 shell 环境。
export_s3_env() {
  AWS_S3_ENDPOINT=$(s3_field "$1" endpoint)
  AWS_S3_REGION=$(s3_field "$1" region)
  AWS_S3_BUCKET=$(s3_field "$1" bucket)
  AWS_S3_PREFIX=$(s3_field "$1" prefix)
  export AWS_S3_ENDPOINT AWS_S3_REGION AWS_S3_BUCKET AWS_S3_PREFIX
}

# write_aws_credentials <json> 从 s3_write 把 STS 临时凭证写入 ~/.aws/credentials 的 ocsync profile
# （含 session token），供 aws_s3 用 --profile ocsync 调用。权限 0600。
write_aws_credentials() {
  local json="$1" ak sk st
  ak=$(jq -r '.s3_write.access_key_id' "$json")
  sk=$(jq -r '.s3_write.secret_access_key' "$json")
  st=$(jq -r '.s3_write.session_token' "$json")
  [ -n "$ak" ] && [ "$ak" != "null" ] || { log "s3_write.access_key_id 缺失"; return 1; }
  mkdir -p "$HOME/.aws"
  ( umask 077; cat > "$HOME/.aws/credentials" <<EOF
[ocsync]
aws_access_key_id = ${ak}
aws_secret_access_key = ${sk}
aws_session_token = ${st}
EOF
  )
}

# creds_expiry_epoch <json> 输出 s3_write.expires_at（RFC3339）对应的 epoch 秒（GNU date，由 coreutils 提供）。
creds_expiry_epoch() {
  local exp
  exp=$(jq -r '.s3_write.expires_at' "$1")
  date -u -d "$exp" +%s
}

# needs_refresh <expiry_epoch> <skew_seconds> 当凭证剩余有效期 < skew 时返回 0（需刷新），否则返回 1。
needs_refresh() {
  local now
  now=$(date -u +%s)
  [ $(( $1 - now )) -lt "$2" ]
}

# aws_s3 <args...> 用 ocsync profile + bootstrap 给的 endpoint/region 调 aws s3 子命令。
aws_s3() {
  aws --profile ocsync --endpoint-url "$AWS_S3_ENDPOINT" --region "$AWS_S3_REGION" s3 "$@"
}

# sync_workspace_up 把本地 workspace 增量同步到 S3（排除可重建大目录；不加 --delete 以免误删持久数据）。
sync_workspace_up() {
  local data_dir="$1"
  aws_s3 sync "$data_dir/workspace" "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}workspace/" \
    --exclude "node_modules/*" --exclude ".git/*" --exclude "*.tmp"
}

# backup_sqlite_up 用 sqlite .backup 出 live DB 的一致性快照并上传为 state.db（绝不分别传 -wal/-shm）。
# 本地无 state.db（首启未建库）时静默跳过。
backup_sqlite_up() {
  local data_dir="$1"
  [ -f "$data_dir/state.db" ] || return 0
  sqlite3 "$data_dir/state.db" ".backup /tmp/oc-snap.db"
  aws_s3 cp /tmp/oc-snap.db "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}state.db"
  rm -f /tmp/oc-snap.db
}
```

- [ ] **Step 2: 写 Dockerfile**

```dockerfile
# runtime/ops/Dockerfile
# app pod 运行时侧 ops 镜像：承载 initContainer(oc-restore) 与 sidecar(oc-sync/oc-presync)。
# 仅装标准 S3 客户端与搬运工具，与 hermes 主镜像解耦（agent 与运维搬运职责分离）。
ARG ALPINE_MIRROR=docker.io/library
FROM ${ALPINE_MIRROR}/alpine:3.20

# aws-cli：标准 S3 客户端（vendor-neutral，对接任意 S3 兼容端点）。
# sqlite：state.db .backup 一致性快照。jq：解析 bootstrap JSON。curl：调 bootstrap / 下载 skills。
# bash：脚本解释器。coreutils：GNU date（-d RFC3339 解析过期时间）。tar：兼容性。ca-certificates：HTTPS。
RUN apk add --no-cache aws-cli sqlite jq curl bash coreutils tar ca-certificates

COPY bin/oc-lib.sh bin/oc-restore bin/oc-sync bin/oc-presync /usr/local/bin/
RUN chmod +x /usr/local/bin/oc-restore /usr/local/bin/oc-sync /usr/local/bin/oc-presync

# 默认无 CMD：pod spec 按容器角色覆盖 command 为 oc-restore / oc-sync。
```

> **实现者注意**：若 `apk add aws-cli` 在所选 alpine 版本不可得，退路：`apk add --no-cache python3 py3-pip && pip install --no-cache-dir awscli`（aws-cli v1，s3 sync/cp 功能一致）。Task 1 的构建步骤会暴露此问题——以构建通过为准。

- [ ] **Step 3: 构建镜像验证（含 aws-cli 可用）**

Run:
```bash
docker build -t oc-manager-ops:dev runtime/ops/
docker run --rm oc-manager-ops:dev sh -c 'aws --version && sqlite3 --version && jq --version && bash --version | head -1 && date -u -d "2026-01-01T00:00:00Z" +%s'
```
Expected: 镜像构建成功；各工具版本正常打印；`date -d` 正确输出 epoch（验证 coreutils GNU date 生效）。

- [ ] **Step 4: shellcheck oc-lib.sh**

Run:
```bash
docker run --rm -v "$PWD/runtime/ops:/src" koalaman/shellcheck:stable --shell=bash /src/bin/oc-lib.sh
```
Expected: 无 error（warning 若有，评估后修；oc-lib.sh 作为被 source 的库，可在文件顶部加 `# shellcheck shell=bash`）。

- [ ] **Step 5: Commit**

```bash
git add runtime/ops/Dockerfile runtime/ops/bin/oc-lib.sh runtime/ops/bin/oc-restore runtime/ops/bin/oc-sync runtime/ops/bin/oc-presync
git commit -F - <<'EOF'
feat(ops): 新增 app pod 运行时侧 ops 镜像与共享库

为 k8s 迁移 spec-A1 引入 runtime/ops 专用镜像（alpine + aws-cli + sqlite3 + jq
+ curl + bash + coreutils），承载 initContainer/sidecar 的搬运脚本，与 hermes
主镜像解耦。oc-lib.sh 提供共享函数：bootstrap 拉取与退避重试、STS 凭证解析/写入
与过期判断、aws s3 调用包装、workspace 增量同步（不加 --delete）、sqlite .backup
一致性快照上传。同步走标准 aws s3 sync（vendor-neutral，最贴 B4）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 2: oc-lib.sh 纯函数 shell 单测

**Files:**
- Create: `runtime/ops/test/unit_test.sh`

- [ ] **Step 1: 写 unit_test.sh**

```bash
#!/usr/bin/env bash
# unit_test.sh — oc-lib.sh 纯函数单测，在 ops 镜像容器内跑（容器自带 bash/jq/coreutils）。
# 覆盖：s3_field 解析、creds 过期判断 needs_refresh 两个方向、write_aws_credentials 写出 session token。
set -uo pipefail
# shellcheck source=/usr/local/bin/oc-lib.sh
source /usr/local/bin/oc-lib.sh

fail=0
assert_eq() { # <actual> <expected> <msg>
  if [ "$1" != "$2" ]; then echo "FAIL: $3：期望 '$2' 实得 '$1'"; fail=1; fi
}

# 构造一份 canned bootstrap JSON（远期过期，含 s3_write）。
cat > /tmp/bs.json <<'EOF'
{"manifest_yaml":"m","persona":"p","platform_rule":"r",
 "skills":[{"name":"weather","rel_path":"resources/skills/weather.tar","url":"http://x/w"}],
 "s3_write":{"endpoint":"http://minio:9000","region":"us-east-1","bucket":"oc-apps",
   "prefix":"apps/a1/","access_key_id":"AK","secret_access_key":"SK","session_token":"ST",
   "expires_at":"2099-01-01T00:00:00Z"}}
EOF

# s3_field 解析 bucket / prefix
assert_eq "$(s3_field /tmp/bs.json bucket)" "oc-apps" "s3_field bucket"
assert_eq "$(s3_field /tmp/bs.json prefix)" "apps/a1/" "s3_field prefix"

# needs_refresh：远期过期 → 不需刷新（needs_refresh 返回非 0）
exp=$(creds_expiry_epoch /tmp/bs.json)
if needs_refresh "$exp" 300; then echo "FAIL: 远期凭证不应判定需刷新"; fail=1; fi
# 已过期（epoch 0）→ 需刷新（返回 0）
if ! needs_refresh 0 300; then echo "FAIL: 已过期凭证应判定需刷新"; fail=1; fi

# write_aws_credentials 写出 ocsync profile，含 session token
HOME=/tmp/ochome write_aws_credentials /tmp/bs.json
grep -q '^aws_session_token = ST$' /tmp/ochome/.aws/credentials || { echo "FAIL: 凭证文件缺 session token"; fail=1; }
grep -q '^\[ocsync\]$' /tmp/ochome/.aws/credentials || { echo "FAIL: 凭证文件缺 ocsync profile 头"; fail=1; }

if [ "$fail" -eq 0 ]; then echo "unit_test: ALL PASS"; fi
exit "$fail"
```

- [ ] **Step 2: 在 ops 容器内跑单测**

Run:
```bash
docker run --rm -v "$PWD/runtime/ops/test/unit_test.sh:/unit_test.sh:ro" oc-manager-ops:dev bash /unit_test.sh
```
Expected: `unit_test: ALL PASS`，退出码 0。

- [ ] **Step 3: shellcheck unit_test.sh**

Run:
```bash
docker run --rm -v "$PWD/runtime/ops:/src" koalaman/shellcheck:stable --shell=bash /src/test/unit_test.sh
```
Expected: 无 error。

- [ ] **Step 4: Commit**

```bash
git add runtime/ops/test/unit_test.sh
git commit -F - <<'EOF'
test(ops): oc-lib.sh 纯函数单测（容器内跑）

覆盖 s3_field 解析、needs_refresh 凭证过期判断两个方向、write_aws_credentials
写出含 session token 的 ocsync profile。在 ops 镜像容器内跑（容器自带 bash/jq/
coreutils），不依赖外部网络与 S3。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 2：oc-restore（initContainer）

### Task 3: oc-restore 脚本

**Files:**
- Modify（替换 Task 1 的占位）: `runtime/ops/bin/oc-restore`

- [ ] **Step 1: 写 oc-restore 实体（替换 Task 1 的占位）**

```bash
#!/usr/bin/env bash
# oc-restore — initContainer 入口：调 bootstrap 拉配置并恢复 app 数据。
# 机制：manifest/resources 直接落 emptyDir；skills（version 级、跨前缀）用预签名 URL 下载；
# workspace/sessions/state.db（apps/<id>/ 前缀内）用 s3_write STS 凭证 aws s3 sync/cp 下载。
# 首启时 apps/<id>/ 前缀为空 → sync 空操作、state.db 不存在则跳过。幂等：pod 重启重跑行为一致。
set -euo pipefail
# shellcheck source=/usr/local/bin/oc-lib.sh
source /usr/local/bin/oc-lib.sh

require_env OC_CONTROL_TOKEN OC_BOOTSTRAP_URL
INPUT_DIR=${OC_INPUT_DIR:-/opt/oc-input}
DATA_DIR=${OC_DATA_DIR:-/opt/data}

bs=$(mktemp)
trap 'rm -f "$bs"' EXIT
fetch_bootstrap "$bs"

# 1. manifest + resources 写入 emptyDir（api_key 随 manifest 落本地临时盘，不进 S3）。
mkdir -p "$INPUT_DIR/resources/skills" "$DATA_DIR/workspace" "$DATA_DIR/sessions"
jq -r '.manifest_yaml'  "$bs" > "$INPUT_DIR/manifest.yaml"
jq -r '.persona'        "$bs" > "$INPUT_DIR/resources/persona.md"
jq -r '.platform_rule'  "$bs" > "$INPUT_DIR/resources/platform-rules.md"
log "manifest/resources 已写入 $INPUT_DIR"

# 2. skills：预签名 URL 下载（version 级，STS 读不到，只能预签名）。
while IFS=$'\t' read -r rel url; do
  [ -n "$rel" ] || continue
  mkdir -p "$INPUT_DIR/$(dirname "$rel")"
  curl -fsS "$url" -o "$INPUT_DIR/$rel"
  log "skill 下载: $rel"
done < <(jq -r '.skills[]? | [.rel_path, .url] | @tsv' "$bs")

# 3. app 数据恢复：写 STS 凭证 + 解析 S3 参数，用 aws s3 sync/cp 从 apps/<id>/ 前缀拉取。
write_aws_credentials "$bs"
export_s3_env "$bs"

# workspace/sessions 前缀镜像同步下来；首启时前缀为空，aws s3 sync 返回 0 不报错。
aws_s3 sync "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}workspace/" "$DATA_DIR/workspace"
aws_s3 sync "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}sessions/"  "$DATA_DIR/sessions"
log "workspace/sessions 已恢复"

# state.db 单对象：存在才拉，并清 WAL 边车（绝不分别拉 -wal/-shm）保证干净重开。
if aws_s3 ls "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}state.db" >/dev/null 2>&1; then
  aws_s3 cp "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}state.db" "$DATA_DIR/state.db"
  rm -f "$DATA_DIR/state.db-wal" "$DATA_DIR/state.db-shm"
  log "state.db 已恢复并清理 WAL 边车"
else
  log "无 state.db 快照（首启），跳过"
fi
log "oc-restore 完成"
```

- [ ] **Step 2: 构建镜像 + shellcheck**

Run:
```bash
docker build -t oc-manager-ops:dev runtime/ops/
docker run --rm -v "$PWD/runtime/ops:/src" koalaman/shellcheck:stable --shell=bash /src/bin/oc-restore
```
Expected: 构建成功；shellcheck 无 error。

- [ ] **Step 3: Commit**

```bash
git add runtime/ops/bin/oc-restore
git commit -F - <<'EOF'
feat(ops): 实现 oc-restore（initContainer 恢复脚本）

调 bootstrap 拉 manifest/resources 写入 /opt/oc-input；skills 用预签名 URL 下载
（version 级跨前缀）；workspace/sessions/state.db 用 s3_write STS 凭证 aws s3
sync/cp 从 apps/<id>/ 前缀恢复，首启前缀为空时干净跳过；state.db 恢复后清理
-wal/-shm 保证干净重开（替换 Task 1 的占位）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 3：oc-sync + oc-presync（sidecar）

### Task 4: oc-sync 脚本

**Files:**
- Create（替换占位）: `runtime/ops/bin/oc-sync`

- [ ] **Step 1: 写 oc-sync 实体**

```bash
#!/usr/bin/env bash
# oc-sync — sidecar 入口：循环把 /opt/data 产物增量同步回 S3。
# 用 bootstrap 的 s3_write STS 凭证写 apps/<id>/ 前缀；凭证临近过期自调 bootstrap 续期。
# workspace 增量同步（无 --delete）每 OC_SYNC_INTERVAL 秒一次；sqlite 一致性快照每
# OC_SQLITE_INTERVAL 秒一次。OC_SYNC_ONCE=1 时只跑一轮（含一次 sqlite 备份）后退出，供测试。
set -euo pipefail
# shellcheck source=/usr/local/bin/oc-lib.sh
source /usr/local/bin/oc-lib.sh

require_env OC_CONTROL_TOKEN OC_BOOTSTRAP_URL
DATA_DIR=${OC_DATA_DIR:-/opt/data}
WS_INTERVAL=${OC_SYNC_INTERVAL:-8}
DB_INTERVAL=${OC_SQLITE_INTERVAL:-30}
SKEW=${OC_CRED_SKEW:-300}

bs=$(mktemp)
trap 'rm -f "$bs"' EXIT
expiry=0
last_db=0

# ensure_creds 首次或临近过期时拉 bootstrap、写 STS 凭证、解析 S3 参数、刷新过期时间。
ensure_creds() {
  if [ "$expiry" -eq 0 ] || needs_refresh "$expiry" "$SKEW"; then
    fetch_bootstrap "$bs"
    write_aws_credentials "$bs"
    export_s3_env "$bs"
    expiry=$(creds_expiry_epoch "$bs")
    log "STS 凭证已刷新，过期 $(date -u -d "@$expiry" +%FT%TZ)"
  fi
}

# run_once 跑一轮：确保凭证新鲜 → 同步 workspace → 到点则备份 sqlite。
run_once() {
  ensure_creds
  sync_workspace_up "$DATA_DIR"
  local now
  now=$(date -u +%s)
  if [ $((now - last_db)) -ge "$DB_INTERVAL" ]; then
    backup_sqlite_up "$DATA_DIR"
    last_db=$now
  fi
}

if [ "${OC_SYNC_ONCE:-0}" = "1" ]; then
  # 测试模式：跑一轮并强制做一次 sqlite 备份，便于断言。
  ensure_creds
  sync_workspace_up "$DATA_DIR"
  backup_sqlite_up "$DATA_DIR"
  log "oc-sync 单轮完成（OC_SYNC_ONCE）"
  exit 0
fi

log "oc-sync 启动：workspace 每 ${WS_INTERVAL}s，sqlite 每 ${DB_INTERVAL}s，凭证提前 ${SKEW}s 续期"
while :; do
  run_once
  sleep "$WS_INTERVAL"
done
```

- [ ] **Step 2: 构建 + shellcheck**

Run:
```bash
docker build -t oc-manager-ops:dev runtime/ops/
docker run --rm -v "$PWD/runtime/ops:/src" koalaman/shellcheck:stable --shell=bash /src/bin/oc-sync
```
Expected: 构建成功；shellcheck 无 error。

- [ ] **Step 3: Commit**

```bash
git add runtime/ops/bin/oc-sync
git commit -F - <<'EOF'
feat(ops): 实现 oc-sync（sidecar 增量同步循环）

循环把 /opt/data/workspace 增量同步回 apps/<id>/ 前缀（无 --delete、排除可重建大
目录）、定期出 sqlite 一致性快照上传 state.db。用 s3_write STS 凭证写入，凭证临近
过期（剩余 < OC_CRED_SKEW）自调 bootstrap 续期。OC_SYNC_ONCE=1 跑一轮后退出供测试。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 5: oc-presync 脚本（preStop）

**Files:**
- Create（替换占位）: `runtime/ops/bin/oc-presync`

- [ ] **Step 1: 写 oc-presync 实体**

```bash
#!/usr/bin/env bash
# oc-presync — pod preStop hook 入口：优雅终止前做一次全量同步 + sqlite 快照，零丢失（父设计 D17）。
# 复用 oc-lib.sh 的同步/备份函数，与 oc-sync 单轮等价但不循环。
set -euo pipefail
# shellcheck source=/usr/local/bin/oc-lib.sh
source /usr/local/bin/oc-lib.sh

require_env OC_CONTROL_TOKEN OC_BOOTSTRAP_URL
DATA_DIR=${OC_DATA_DIR:-/opt/data}

bs=$(mktemp)
trap 'rm -f "$bs"' EXIT
fetch_bootstrap "$bs"
write_aws_credentials "$bs"
export_s3_env "$bs"

log "preStop：全量同步 workspace + sqlite 快照"
sync_workspace_up "$DATA_DIR"
backup_sqlite_up "$DATA_DIR"
log "oc-presync 完成"
```

- [ ] **Step 2: 构建 + shellcheck（全部脚本）**

Run:
```bash
docker build -t oc-manager-ops:dev runtime/ops/
for s in oc-lib.sh oc-restore oc-sync oc-presync; do
  docker run --rm -v "$PWD/runtime/ops:/src" koalaman/shellcheck:stable --shell=bash "/src/bin/$s"
done
```
Expected: 构建成功；四个脚本 shellcheck 均无 error。

- [ ] **Step 3: 在容器内跑库单测回归**

Run:
```bash
docker run --rm -v "$PWD/runtime/ops/test/unit_test.sh:/unit_test.sh:ro" oc-manager-ops:dev bash /unit_test.sh
```
Expected: `unit_test: ALL PASS`。

- [ ] **Step 4: Commit**

```bash
git add runtime/ops/bin/oc-presync
git commit -F - <<'EOF'
feat(ops): 实现 oc-presync（preStop 全量同步）

pod 优雅终止前做一次全量 workspace 同步 + sqlite 一致性快照上传，保证零丢失
（父设计 D17）。复用 oc-lib.sh 的 sync_workspace_up/backup_sqlite_up，与 oc-sync
单轮等价但不循环。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 4：pod 内部契约文档

### Task 6: runtime/ops/README.md（供 spec-A2 渲染）

**Files:**
- Create: `runtime/ops/README.md`

- [ ] **Step 1: 写 README.md**

写 `runtime/ops/README.md`，内容覆盖（这是 spec-A2 渲染 pod spec 的权威契约）：
- **镜像**：ops 镜像用途（initContainer + sidecar 共用，覆盖 command）、构建方式（`make build-ops-runtime` / `make local-build-ops`）、镜像 ref 占位 `<OPS_IMAGE_REF>`。
- **共享卷**：`/opt/oc-input`（emptyDir，名 `oc-input`）、`/opt/data`（emptyDir，名 `data`）；各容器挂载关系（initContainer/hermes/oc-ops/s3-sync 谁读谁写）。
- **环境变量契约**：`OC_CONTROL_TOKEN`（来源 Secret `app-<id>-token` 键 `control-token`）、`OC_BOOTSTRAP_URL`（A2 渲染完整 URL）；可选调参 `OC_SYNC_INTERVAL`/`OC_SQLITE_INTERVAL`/`OC_CRED_SKEW`/`OC_BOOTSTRAP_RETRIES`/`OC_INPUT_DIR`/`OC_DATA_DIR` 及默认值。
- **容器角色与 command**：initContainer `restore` → `oc-restore`；sidecar `s3-sync` → `oc-sync`，preStop exec → `oc-presync`。
- **数据流/恢复机制**：skills 预签名 URL、app 数据 STS sync（明确 bootstrap `restore.*_url` 字段对 A1 废弃，A2 渲染时不依赖它）。
- **A2 待办对齐点**：把这两个 emptyDir + initContainer + s3-sync sidecar 加进 spec-D 的 app-pod 契约（spec-D 契约当前只有 hermes + oc-ops + `data` 卷）；注入 env；preStop hook。

- [ ] **Step 2: Commit**

```bash
git add runtime/ops/README.md
git commit -F - <<'EOF'
docs(ops): 新增 ops 镜像 pod 内部契约文档

固定 spec-A1 交付给 spec-A2 的 pod 内部契约：ops 镜像用途、共享卷（/opt/oc-input
与 /opt/data emptyDir）、环境变量契约（OC_CONTROL_TOKEN/OC_BOOTSTRAP_URL 及可选
调参）、容器角色与 command（restore/sync/presync）、恢复机制（skills 预签名 + app
数据 STS sync，bootstrap restore.*_url 对 A1 废弃）、A2 渲染待办对齐点。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 5：对真实 MinIO 的集成测

### Task 7: oc-restore 集成测（真实 MinIO + mock bootstrap）

**Files:**
- Create: `runtime/ops/test/ops_integration_test.go`

> 集成测复用 spec-B 的 `internal/integrations/storage`（真实 MinIO）与 STS 签发，httptest 起 mock
> bootstrap，`docker run --network host` 在 ops 容器内跑脚本。环境门控（缺变量即 Skip）：
> `OC_S3_TEST_ENDPOINT`/`OC_S3_TEST_BUCKET`/`OC_S3_TEST_AK`/`OC_S3_TEST_SK`/`OC_S3_TEST_STS_ROLE`
> + `OC_OPS_TEST_IMAGE`（ops 镜像 ref，默认 `oc-manager-ops:dev`）。

- [ ] **Step 1: 写集成测骨架 + oc-restore 用例**

```go
package ops_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/storage"
)

// opsTestEnv 从环境变量读 MinIO 接入参数与 ops 镜像 ref；缺失则跳过整组集成测。
type opsTestEnv struct {
	cfg   storage.S3Config
	image string
}

func loadOpsTestEnv(t *testing.T) opsTestEnv {
	t.Helper()
	ep := os.Getenv("OC_S3_TEST_ENDPOINT")
	if ep == "" {
		t.Skip("未设置 OC_S3_TEST_ENDPOINT，跳过 ops 集成测")
	}
	role := os.Getenv("OC_S3_TEST_STS_ROLE")
	if role == "" {
		role = "arn:aws:iam:::role/dev"
	}
	img := os.Getenv("OC_OPS_TEST_IMAGE")
	if img == "" {
		img = "oc-manager-ops:dev"
	}
	return opsTestEnv{
		cfg: storage.S3Config{
			Endpoint: ep, Region: "us-east-1", Bucket: os.Getenv("OC_S3_TEST_BUCKET"),
			AccessKeyID: os.Getenv("OC_S3_TEST_AK"), SecretAccessKey: os.Getenv("OC_S3_TEST_SK"),
			UsePathStyle: true, STSRoleARN: role,
		},
		image: img,
	}
}

// bootstrapJSON 构造 mock bootstrap 返回的 canned 响应（含 skills 预签名 + STS s3_write）。
func bootstrapJSON(t *testing.T, env opsTestEnv, appPrefix, skillURL string) []byte {
	t.Helper()
	issuer := storage.NewSTSCredentialIssuer(env.cfg)
	creds, err := issuer.AssumeAppRole(context.Background(), appPrefix, 15*time.Minute)
	require.NoError(t, err)
	resp := map[string]any{
		"manifest_yaml": "version: \"2\"\napp:\n  id: it\n",
		"persona":       "测试 persona",
		"platform_rule": "测试 platform rule",
		"skills": []map[string]string{
			{"name": "weather", "rel_path": "resources/skills/weather.tar", "url": skillURL},
		},
		"s3_write": map[string]any{
			"endpoint": env.cfg.Endpoint, "region": env.cfg.Region, "bucket": env.cfg.Bucket,
			"prefix": appPrefix, "access_key_id": creds.AccessKeyID,
			"secret_access_key": creds.SecretAccessKey, "session_token": creds.SessionToken,
			"expires_at": creds.ExpiresAt.UTC().Format(time.RFC3339),
		},
	}
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	return b
}

// runOpsContainer 用 --network host 在 ops 容器内跑指定 command，挂载 data/input 目录。
func runOpsContainer(t *testing.T, env opsTestEnv, command, bootstrapURL, dataDir, inputDir string, extraEnv ...string) (string, error) {
	t.Helper()
	args := []string{
		"run", "--rm", "--network", "host",
		"-e", "OC_CONTROL_TOKEN=test-token",
		"-e", "OC_BOOTSTRAP_URL=" + bootstrapURL,
		"-e", "OC_DATA_DIR=/data", "-e", "OC_INPUT_DIR=/input",
		"-e", "HOME=/tmp",
		"-v", dataDir + ":/data", "-v", inputDir + ":/input",
	}
	for _, e := range extraEnv {
		args = append(args, "-e", e)
	}
	args = append(args, env.image, command)
	cmd := exec.Command("docker", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// TestOcRestore 验证 oc-restore 写 manifest/skills、用 STS sync 恢复 workspace、恢复 state.db 并清 -wal/-shm。
func TestOcRestore(t *testing.T) {
	env := loadOpsTestEnv(t)
	store := storage.NewS3ObjectStore(env.cfg)
	ctx := context.Background()
	id := fmt.Sprintf("it-restore-%d", time.Now().UnixNano())
	appPrefix := storage.AppPrefix(id) // apps/<id>/
	t.Cleanup(func() { _ = store.DeletePrefix(context.Background(), appPrefix) })

	// 预置：version 级 skill 对象 + 预签名 URL；apps/<id>/ 下 workspace 对象 + state.db。
	skillKey := storage.SkillKey("itv", "weather")
	require.NoError(t, store.PutObject(ctx, skillKey, strings.NewReader("SKILL-TAR"), int64(len("SKILL-TAR"))))
	t.Cleanup(func() { _ = store.DeletePrefix(context.Background(), "versions/itv/") })
	skillURL, err := store.PresignGet(ctx, skillKey, 10*time.Minute)
	require.NoError(t, err)
	require.NoError(t, store.PutObject(ctx, appPrefix+"workspace/hello.txt", strings.NewReader("WS"), 2))
	require.NoError(t, store.PutObject(ctx, appPrefix+"state.db", strings.NewReader("SQLITEDATA"), 10))

	// mock bootstrap
	body := bootstrapJSON(t, env, appPrefix, skillURL)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	inputDir := t.TempDir()
	out, runErr := runOpsContainer(t, env, "oc-restore", srv.URL+"/internal/apps/"+id+"/bootstrap", dataDir, inputDir)
	require.NoError(t, runErr, "oc-restore 容器执行失败:\n%s", out)

	// 断言：manifest/persona/skills 落 input；workspace sync 下来；state.db 恢复且无 -wal/-shm。
	assertFileContains(t, filepath.Join(inputDir, "manifest.yaml"), "app:")
	assertFileContains(t, filepath.Join(inputDir, "resources/persona.md"), "测试 persona")
	assertFileContains(t, filepath.Join(inputDir, "resources/skills/weather.tar"), "SKILL-TAR")
	assertFileContains(t, filepath.Join(dataDir, "workspace/hello.txt"), "WS")
	assertFileContains(t, filepath.Join(dataDir, "state.db"), "SQLITEDATA")
	assert.NoFileExists(t, filepath.Join(dataDir, "state.db-wal"))
}

// TestOcRestoreFirstBoot 验证首启（apps/<id>/ 前缀为空）时 workspace sync 空操作、state.db 跳过、不报错。
func TestOcRestoreFirstBoot(t *testing.T) {
	env := loadOpsTestEnv(t)
	store := storage.NewS3ObjectStore(env.cfg)
	ctx := context.Background()
	id := fmt.Sprintf("it-firstboot-%d", time.Now().UnixNano())
	appPrefix := storage.AppPrefix(id)
	skillKey := storage.SkillKey("itv2", "weather")
	require.NoError(t, store.PutObject(ctx, skillKey, strings.NewReader("S"), 1))
	t.Cleanup(func() { _ = store.DeletePrefix(context.Background(), "versions/itv2/") })
	skillURL, err := store.PresignGet(ctx, skillKey, 10*time.Minute)
	require.NoError(t, err)

	body := bootstrapJSON(t, env, appPrefix, skillURL)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()

	dataDir := t.TempDir()
	inputDir := t.TempDir()
	out, runErr := runOpsContainer(t, env, "oc-restore", srv.URL+"/internal/apps/"+id+"/bootstrap", dataDir, inputDir)
	require.NoError(t, runErr, "首启 oc-restore 应成功:\n%s", out)
	assert.NoFileExists(t, filepath.Join(dataDir, "state.db"))
}

// assertFileContains 断言文件存在且包含子串。
func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err, "读文件 %s", path)
	assert.Contains(t, string(b), want)
}
```

- [ ] **Step 2: 无 MinIO 时跑（应跳过）**

Run: `go test ./runtime/ops/test/ -run TestOcRestore -v`
Expected: SKIP（未设 `OC_S3_TEST_ENDPOINT`）。

- [ ] **Step 3: 有 MinIO 时跑（交付前在本地 k3d 验证）**

先确保 ops 镜像已构建（`docker build -t oc-manager-ops:dev runtime/ops/`）、MinIO 端口转发到本机、bucket 存在。Run（值按本地调整）：
```bash
kubectl -n ocm port-forward svc/minio 9000:9000 >/tmp/pf.log 2>&1 &
PF=$!
OC_S3_TEST_ENDPOINT=http://localhost:9000 OC_S3_TEST_BUCKET=oc-apps \
OC_S3_TEST_AK=<minio-root-user> OC_S3_TEST_SK=<minio-root-password> \
OC_S3_TEST_STS_ROLE=arn:aws:iam:::role/dev OC_OPS_TEST_IMAGE=oc-manager-ops:dev \
go test ./runtime/ops/test/ -run TestOcRestore -v
kill $PF
```
Expected: `TestOcRestore` 与 `TestOcRestoreFirstBoot` PASS。**若 MinIO STS 未配 role 导致 AssumeRole 失败，记录实际结果与所需配置到交付说明，不可伪造通过**（spec-B Task 17 已验证本地 MinIO STS 可用，此处应能复用）。

- [ ] **Step 4: Commit**

```bash
git add runtime/ops/test/ops_integration_test.go
git commit -F - <<'EOF'
test(ops): oc-restore 对真实 MinIO + mock bootstrap 集成测

环境门控（OC_S3_TEST_* + OC_OPS_TEST_IMAGE，缺失即 Skip）。复用 storage 包对真实
MinIO 预置 version 级 skill（预签名）与 apps/<id>/ 前缀 workspace/state.db；httptest
起 mock bootstrap 返回含真实 STS 凭证的 canned 响应；docker run --network host 在
ops 容器内跑 oc-restore，断言 manifest/skills 落盘、workspace 经 STS sync 恢复、
state.db 恢复且无 -wal/-shm；首启用例断言空前缀干净跳过。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 8: oc-sync 集成测

**Files:**
- Modify: `runtime/ops/test/ops_integration_test.go`

- [ ] **Step 1: 追加 oc-sync 用例**

在 `ops_integration_test.go` 追加：
```go
// TestOcSyncOnce 验证 oc-sync（OC_SYNC_ONCE）把本地 workspace 与 sqlite 快照上传到 apps/<id>/ 前缀。
func TestOcSyncOnce(t *testing.T) {
	env := loadOpsTestEnv(t)
	store := storage.NewS3ObjectStore(env.cfg)
	ctx := context.Background()
	id := fmt.Sprintf("it-sync-%d", time.Now().UnixNano())
	appPrefix := storage.AppPrefix(id)
	t.Cleanup(func() { _ = store.DeletePrefix(context.Background(), appPrefix) })

	// mock bootstrap 不需要 skill 对象（sync 不下载 skills）；给一个占位 URL。
	body := bootstrapJSON(t, env, appPrefix, "http://unused.example/skill")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()

	// 预置本地 /data：workspace 文件 + 一个最小 sqlite DB。
	dataDir := t.TempDir()
	inputDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dataDir, "workspace"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "workspace/out.txt"), []byte("SYNCED"), 0o644))
	// 用 ops 容器内的 sqlite3 建一个最小 DB，确保 .backup 可用。
	mk := exec.Command("docker", "run", "--rm", "-v", dataDir+":/data", env.image,
		"sqlite3", "/data/state.db", "CREATE TABLE t(x); INSERT INTO t VALUES(1);")
	mkOut, mkErr := mk.CombinedOutput()
	require.NoError(t, mkErr, "建测试 sqlite 失败:\n%s", string(mkOut))

	out, runErr := runOpsContainer(t, env, "oc-sync", srv.URL+"/internal/apps/"+id+"/bootstrap",
		dataDir, inputDir, "OC_SYNC_ONCE=1")
	require.NoError(t, runErr, "oc-sync 容器执行失败:\n%s", out)

	// 断言：MinIO apps/<id>/ 前缀出现 workspace 对象与 state.db。
	exists, err := store.ObjectExists(ctx, appPrefix+"workspace/out.txt")
	require.NoError(t, err)
	assert.True(t, exists, "workspace 对象应已上传")
	dbExists, err := store.ObjectExists(ctx, appPrefix+"state.db")
	require.NoError(t, err)
	assert.True(t, dbExists, "state.db 快照应已上传")
}
```

- [ ] **Step 2: 跑（无 MinIO 跳过 / 有 MinIO 验证）**

Run（无 MinIO）: `go test ./runtime/ops/test/ -run TestOcSyncOnce -v` → SKIP。
Run（有 MinIO，同 Task 7 Step 3 的 env）: → `TestOcSyncOnce` PASS。

- [ ] **Step 3: Commit**

```bash
git add runtime/ops/test/ops_integration_test.go
git commit -F - <<'EOF'
test(ops): oc-sync 对真实 MinIO 集成测

预置本地 workspace 文件 + 用容器内 sqlite3 建最小 DB，OC_SYNC_ONCE=1 跑一轮，
断言 MinIO 的 apps/<id>/ 前缀出现 workspace 对象与 state.db 快照，证明 STS 凭证
解析、aws s3 sync 上传、sqlite .backup 路径真实可用。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 6：Makefile 构建 target 与收尾

### Task 9: Makefile build-ops-runtime target

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: 加构建 target**

先读 Makefile 现有 hermes/manager 镜像 target 与 registry 变量（`K3D_REGISTRY_HOST`、`HERMES_IMAGE_REPO` 风格、`.PHONY` 行、prod registry 前缀）。仿照加：
```makefile
# ops runtime 镜像仓库（pod initContainer/sidecar 搬运脚本）。
OPS_IMAGE_REPO    ?= crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-ops
OPS_IMAGE_TAG     ?= dev

build-ops-runtime: ## 构建 ops 运行时镜像并推生产 registry
	docker build -t $(OPS_IMAGE_REPO):$(OPS_IMAGE_TAG) runtime/ops/
	docker push $(OPS_IMAGE_REPO):$(OPS_IMAGE_TAG)

local-build-ops: ## 构建 ops 镜像推 k3d registry（本地联调用）
	docker build -t $(K3D_REGISTRY_HOST)/oc-manager-ops:dev runtime/ops/
	docker push $(K3D_REGISTRY_HOST)/oc-manager-ops:dev
```
并把 `build-ops-runtime local-build-ops` 加进文件首行的 `.PHONY` 列表。

- [ ] **Step 2: 验证 target 可解析**

Run: `make -n local-build-ops`
Expected: 打印 docker build/push 命令，无 Makefile 语法错误。

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -F - <<'EOF'
build(ops): 新增 ops 镜像构建 target

build-ops-runtime 构建并推生产 registry；local-build-ops 推 k3d registry 供本地
联调。仿照 hermes/manager 镜像 target 与 registry 变量风格。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 10: 收尾验证

**Files:** 无（校验任务）

- [ ] **Step 1: 构建 ops 镜像 + 四脚本 shellcheck**

Run:
```bash
docker build -t oc-manager-ops:dev runtime/ops/
for s in oc-lib.sh oc-restore oc-sync oc-presync; do
  echo "== shellcheck $s =="
  docker run --rm -v "$PWD/runtime/ops:/src" koalaman/shellcheck:stable --shell=bash "/src/bin/$s"
done
```
Expected: 构建成功；四脚本均无 shellcheck error。

- [ ] **Step 2: 容器内库单测**

Run: `docker run --rm -v "$PWD/runtime/ops/test/unit_test.sh:/unit_test.sh:ro" oc-manager-ops:dev bash /unit_test.sh`
Expected: `unit_test: ALL PASS`。

- [ ] **Step 3: Go 集成测无 MinIO 跳过 + 编译**

Run: `go build ./... && go vet ./runtime/... && go test ./runtime/ops/test/ -v`
Expected: 编译通过；集成测三个用例 SKIP（无 env）。

- [ ] **Step 4: 真实 MinIO 集成测（交付前在本地 k3d 必跑）**

Run（同 Task 7 Step 3 起 port-forward + env）：
```bash
go test ./runtime/ops/test/ -v
```
Expected: `TestOcRestore` / `TestOcRestoreFirstBoot` / `TestOcSyncOnce` PASS。**STS/恢复/同步是 pod 闭环的核心机制，须对真实 MinIO 证明；若环境受限无法跑，须在交付说明如实记录原因与风险，不可伪造通过。**

- [ ] **Step 5: 确认工作区无混入文件**

Run: `git status --short`
Expected: 仅本计划相关已提交改动；不含误加的未跟踪 `docs/reports/`。

---

## 验证范围说明（A1.4，写入交付）

spec-A1 是 pod 运行时侧产物：脚本逻辑用 shellcheck + 容器内库单测；restore/sync/sqlite 的外部协议交互对本地 k3d 真实 MinIO + mock bootstrap 集成测。**完整 pod 闭环**（initContainer/sidecar 真在 k8s pod 内编排运行、hermes 真读 restore 出的 manifest/state.db）与**三角色真实浏览器验证**在 spec-A2 渲染 pod 后，与 A/B/D/E 一起做。本 spec 不单独宣称「pod 闭环已验证可用」——对项目「真实环境验证」要求的一次显式、有界偏离（与 spec-B B6 / spec-E E4 同性质）。

---

## 待 spec-A2 衔接点（本计划不做，契约已钉）

- 渲染 pod spec：在 spec-D 的 app-pod 契约（hermes + oc-ops + `data` 卷）基础上加 `/opt/oc-input` emptyDir、initContainer `restore`（cmd oc-restore）、sidecar `s3-sync`（cmd oc-sync + preStop exec oc-presync），引用 `<OPS_IMAGE_REF>`。
- 注入 `OC_CONTROL_TOKEN`（Secret `app-<id>-token` 的 `control-token` 键）与 `OC_BOOTSTRAP_URL`（按 manager 在集群内/宿主的跑法渲染可达地址）。
- bootstrap 的 `restore.*_url` 字段对 A1 废弃——A2 渲染/wiring 时不依赖它（manager 侧可按需在后续清理该字段，非本序列必须）。
- KubernetesAdapter、docker→k8s 生命周期、节点概念删除、OcOpsResolver 真实寻址、创建流程 ensure。
- A/B/D/E 合并后的端到端 + 三角色真实浏览器验证（吸收 A1.4 推迟项）。
