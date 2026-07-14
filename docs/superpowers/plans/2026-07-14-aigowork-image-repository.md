# AiGoWork 镜像仓库更名 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将普通运行时与 AICC 运行时的镜像仓库从 `oc-manager-hermes*` 统一更名为 `oc-manager-aigowork*`，不改变 Hermes 的运行时技术标识。

**Architecture:** 仅替换镜像引用中的仓库段。Makefile 仍使用 `HERMES_*` 变量、同一构建目录和同一 tag 规则；本地、生产及示例配置跟随新仓库。测试夹具、发布脚本与说明文档引用同样更新，以免示例或自动化重新引入旧仓库名。

**Tech Stack:** GNU Make、Docker 镜像引用、Kubernetes YAML、Go/testify 测试、Bash。

---

## 文件结构

- 修改：`Makefile` — 普通/AICC 生产镜像仓库变量、本地 AICC 构建镜像名，以及生产发布时匹配普通运行时镜像的 sed 模式。
- 修改：`deploy/k8s/local/secret.yaml`、`deploy/k8s/prod/secret.example.yaml` — 入库的 manager 运行时镜像引用。
- 修改（本机/发布环境配置，不提交）：`deploy/k8s/prod/secret.yaml`、`config/manager.yaml` — gitignore 的 manager 运行时镜像引用。
- 修改：`cmd/migrate/main_test.go`、`internal/config/loader_test.go`、`internal/config/runtime_images_test.go`、`internal/service/aicc_runtime_upgrade_reconciler_test.go`、`internal/worker/handlers/app_initialize_test.go` — 与真实仓库名一致的 AICC 镜像测试夹具。
- 修改：`scripts/aicc-readiness/upgrade-rollback.sh` — 就绪性验证预置的本地普通运行时镜像引用。
- 修改：`README.md`、`docs/configuration.md`、`docs/hermes-container.md`、`runtime/hermes/README.md` — 构建、发布和配置示例中的镜像仓库名称。
- 不修改：`docs/testing/evidence/**` — 历史验证日志必须保留当时实际推送的旧镜像名。

### Task 1: 更新构建与发布镜像引用

**Files:**
- Modify: `Makefile:53-63,214-227,549-553`

- [ ] **Step 1: 写出镜像仓库变量与发布替换规则的预期值**

在执行任何构建前，运行以下只读检查，记录当前输出中仍包含的旧仓库名：

```bash
make -pn | rg '^(HERMES_IMAGE_REPO|AICC_RUNTIME_IMAGE_REPO|HERMES_IMAGE|AICC_RUNTIME_IMAGE)\s*[:?]?='
make -n local-build-aicc-runtime | rg 'oc-manager-hermes-aicc'
make -n .prod-deploy-hermes-one HERMES_VARIANT=hermes-v2026.7.1 | rg 'oc-manager-hermes'
```

Expected: 三个命令均能看到 `oc-manager-hermes` 或 `oc-manager-hermes-aicc`，证明待替换路径已被构建入口使用。

- [ ] **Step 2: 替换 Makefile 中的镜像仓库名**

将以下精确文本替换为新仓库名，保留变量名、容器变体和 tag 计算方式：

```make
HERMES_IMAGE_REPO    ?= $(PROD_REGISTRY)/$(PROD_APP_NS)/oc-manager-aigowork
AICC_RUNTIME_IMAGE_REPO ?= $(PROD_REGISTRY)/$(PROD_APP_NS)/oc-manager-aigowork-aicc
```

并在 `local-build-aicc-runtime` 的 build、push、save 三处将：

```make
$(K3D_REGISTRY_HOST)/oc-manager-hermes-aicc:dev
```

替换为：

```make
$(K3D_REGISTRY_HOST)/oc-manager-aigowork-aicc:dev
```

最后将 `.prod-deploy-hermes-one` 的精确匹配模式更新为：

```make
sed -i -E 's#ref: "[^"]*oc-manager-aigowork:$(HERMES_VERSION_RE)-[^"]*"#ref: "$(HERMES_IMAGE)"#' deploy/k8s/prod/secret.yaml
```

- [ ] **Step 3: 验证 Makefile 解析结果和 dry-run 发布命令**

Run:

```bash
make -pn | rg '^(HERMES_IMAGE_REPO|AICC_RUNTIME_IMAGE_REPO|HERMES_IMAGE|AICC_RUNTIME_IMAGE)\s*[:?]?='
make -n local-build-aicc-runtime | rg 'oc-manager-aigowork-aicc:dev'
make -n .prod-deploy-hermes-one HERMES_VARIANT=hermes-v2026.7.1 | rg 'oc-manager-aigowork'
```

Expected: 输出仅使用 `oc-manager-aigowork` 或 `oc-manager-aigowork-aicc`；不会执行 Docker 构建、推送或 Kubernetes 更新。

- [ ] **Step 4: 提交构建入口修改**

```bash
git add Makefile
git commit -m "chore(runtime): 更名 AiGoWork 运行时镜像仓库" -m "普通运行时和 AICC 运行时构建、推送及生产发布回填统一使用 oc-manager-aigowork 镜像仓库。"
```

### Task 2: 更新部署与运行配置中的镜像引用

**Files:**
- Modify: `deploy/k8s/local/secret.yaml:115-129`
- Modify (gitignored): `deploy/k8s/prod/secret.yaml:94-111`
- Modify: `deploy/k8s/prod/secret.example.yaml:102-109`
- Modify (gitignored): `config/manager.yaml:41-47`

- [ ] **Step 1: 更新普通运行时镜像引用**

在四个配置文件中，将所有镜像引用的仓库段：

```text
oc-manager-hermes:
```

替换为：

```text
oc-manager-aigowork:
```

保留 registry、namespace、版本 ID、label 和 tag 不变；注释中描述旧仓库前缀的地方也同步写成 `oc-manager-aigowork`。

- [ ] **Step 2: 更新 AICC 镜像引用**

在两个 Kubernetes 配置中，将：

```text
oc-manager-hermes-aicc:
```

替换为：

```text
oc-manager-aigowork-aicc:
```

不得修改 `aicc.runtime_image` 配置键、`hermes.runtime_images` 配置键或任何 `Hermes` label。

- [ ] **Step 3: 运行配置解析测试**

Run:

```bash
go test ./internal/config
```

Expected: PASS。该包覆盖 manager 配置加载与 AICC 不可变镜像引用校验，确保仓库名改变不影响镜像引用语法。

- [ ] **Step 4: 检查配置引用范围**

Run:

```bash
rg -n 'oc-manager-hermes(-aicc)?' deploy/k8s/local/secret.yaml deploy/k8s/prod/secret.yaml deploy/k8s/prod/secret.example.yaml config/manager.yaml
rg -n 'oc-manager-aigowork(-aicc)?' deploy/k8s/local/secret.yaml deploy/k8s/prod/secret.yaml deploy/k8s/prod/secret.example.yaml config/manager.yaml
```

Expected: 第一条无输出且退出码为 1；第二条列出普通和 AICC 的新镜像引用。

- [ ] **Step 5: 提交入库配置并记录 gitignore 配置已同步**

```bash
git add deploy/k8s/local/secret.yaml deploy/k8s/prod/secret.example.yaml
git commit -m "chore(config): 切换 AiGoWork 运行时镜像引用" -m "本地、生产和示例配置改用 AiGoWork 镜像仓库，保留 Hermes 运行时配置结构与版本标签。"
```

Expected: `deploy/k8s/prod/secret.yaml` 和 `config/manager.yaml` 已在当前环境同步修改，但因它们包含环境专属敏感配置且被 gitignore，不得以 `git add -f` 提交。

### Task 3: 同步测试夹具与本地验证脚本

**Files:**
- Modify: `cmd/migrate/main_test.go:55`
- Modify: `internal/config/loader_test.go:48,162,538`
- Modify: `internal/config/runtime_images_test.go:65,67,80`
- Modify: `internal/service/aicc_runtime_upgrade_reconciler_test.go:20,28,35`
- Modify: `internal/worker/handlers/app_initialize_test.go:1028`
- Modify: `scripts/aicc-readiness/upgrade-rollback.sh:18`

- [ ] **Step 1: 把 AICC 测试夹具改为新仓库名**

在全部五个 Go 测试文件中，把示例镜像：

```text
registry.example.com/app/oc-manager-hermes-aicc:
```

替换为：

```text
registry.example.com/app/oc-manager-aigowork-aicc:
```

只替换夹具和断言字符串，不改变测试场景、断言方法或镜像合法性规则。

- [ ] **Step 2: 更新 AICC 就绪性脚本的本地普通镜像引用**

将：

```bash
readonly LOCAL_HERMES_IMAGE="${REGISTRY_HOST}/oc-manager-hermes:v2026.7.1-dev1"
```

替换为：

```bash
readonly LOCAL_HERMES_IMAGE="${REGISTRY_HOST}/oc-manager-aigowork:v2026.7.1-dev1"
```

变量名保留为 `LOCAL_HERMES_IMAGE`，因为脚本消费的是 Hermes runtime，而非镜像品牌名。

- [ ] **Step 3: 运行受影响的单元测试和脚本语法检查**

Run:

```bash
go test ./cmd/migrate ./internal/config ./internal/service ./internal/worker/handlers
bash -n scripts/aicc-readiness/upgrade-rollback.sh
```

Expected: 全部 PASS；脚本语法检查无输出。

- [ ] **Step 4: 提交测试和脚本同步**

```bash
git add cmd/migrate/main_test.go internal/config/loader_test.go internal/config/runtime_images_test.go internal/service/aicc_runtime_upgrade_reconciler_test.go internal/worker/handlers/app_initialize_test.go scripts/aicc-readiness/upgrade-rollback.sh
git commit -m "test(runtime): 同步 AiGoWork 镜像测试夹具" -m "测试配置和 AICC 就绪性验证脚本改用新的 AiGoWork 运行时镜像仓库。"
```

### Task 4: 更新开发与运维文档

**Files:**
- Modify: `README.md:168,210,224`
- Modify: `docs/configuration.md:141`
- Modify: `docs/hermes-container.md:59`
- Modify: `runtime/hermes/README.md:21`

- [ ] **Step 1: 更新文档中的仓库名称**

将所有现行镜像示例中的 `oc-manager-hermes` 改成 `oc-manager-aigowork`，并把 AICC 文档中的 `oc-manager-hermes-aicc` 改成 `oc-manager-aigowork-aicc`。保留 `Hermes` 在运行时、目录、Make target 和版本字段中的技术含义，不做目录或命令更名。

- [ ] **Step 2: 确认历史证据未被改写**

Run:

```bash
rg -n 'oc-manager-hermes' docs/testing/evidence | head -20
git diff -- docs/testing/evidence
```

Expected: 第一条可显示历史日志中的旧仓库名；第二条无输出，证明历史发布证据未被篡改。

- [ ] **Step 3: 检查现行引用不存在旧仓库名**

Run:

```bash
rg -n 'oc-manager-hermes(-aicc)?' --glob '!docs/testing/evidence/**' --glob '!docs/superpowers/**' .
```

Expected: 无输出且退出码为 1。`docs/testing/evidence/**` 中的历史日志和 `docs/superpowers/**` 中的既有设计/计划不属于现行配置或文档，不纳入此检查。

- [ ] **Step 4: 提交文档更新**

```bash
git add README.md docs/configuration.md docs/hermes-container.md runtime/hermes/README.md
git commit -m "docs(runtime): 更新 AiGoWork 镜像仓库说明" -m "构建、发布和 AICC 配置示例统一展示 AiGoWork 镜像仓库，避免继续复制旧路径。"
```

### Task 5: 完整性验证与交付

**Files:**
- Verify only: `Makefile`, 部署配置、测试夹具、脚本和文档

- [ ] **Step 1: 运行最终静态检查与受影响测试**

Run:

```bash
git diff --check
make -pn | rg '^(HERMES_IMAGE_REPO|AICC_RUNTIME_IMAGE_REPO|HERMES_IMAGE|AICC_RUNTIME_IMAGE)\s*[:?]?='
go test ./cmd/migrate ./internal/config ./internal/service ./internal/worker/handlers
bash -n scripts/aicc-readiness/upgrade-rollback.sh
rg -n 'oc-manager-hermes(-aicc)?' --glob '!docs/testing/evidence/**' --glob '!docs/superpowers/**' .
```

Expected: 格式检查、Go 测试和 Bash 语法检查通过；Make 变量输出均含 `aigowork`；最终检索无输出且退出码为 1。

- [ ] **Step 2: 检查提交边界和工作区**

Run:

```bash
git status --short
git log --oneline -4
```

Expected: Git 跟踪工作区干净；最近提交分别覆盖构建入口、入库部署配置、测试/脚本和文档，且不包含 `docs/testing/evidence/**` 的改动。`deploy/k8s/prod/secret.yaml` 与 `config/manager.yaml` 是预期存在的 gitignore 环境配置，不出现在该状态输出中。
