# AICC 客服专用运行时隔离 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 AICC 隐藏应用的 Hermes 镜像、构建物和升级流程从普通实例 Hermes 版本列表中隔离出来。

**Architecture:** 普通实例继续通过助手版本的 `image_id` 从 `hermes.runtime_images` 解析镜像。AICC 隐藏应用仍绑定助手版本以复用模型、技能和初始化配置，但 app 初始化时根据 `apps.aicc_hidden` 强制解析 `aicc.runtime_image`。独立协调器逐个找出镜像不一致的 AICC 隐藏应用并复用 `app_initialize` 任务完成滚动更新。

**Tech Stack:** Go、sqlc、MySQL、Kubernetes 编排、Docker、GNU Make、Playwright。

---

## 文件结构

| 文件 | 责任 | 操作 |
|---|---|---|
| `internal/config/config.go` | 声明 AICC 专用镜像配置 | 修改 |
| `internal/config/loader.go`、`internal/config/*_test.go` | 配置 fail-fast 校验 | 修改 / 新增测试 |
| `internal/config/runtime_images.go` | 复用镜像引用校验 helper | 修改 |
| `internal/service/app_service.go`、`internal/service/app_service_test.go` | AICC 隐藏应用创建时保留版本但不再将其作为镜像来源 | 修改 |
| `internal/worker/handlers/app_initialize.go`、测试 | 根据隐藏应用标记选择专用 AICC 镜像 | 修改 |
| `internal/service/aicc_runtime_upgrade_reconciler.go`、测试 | 逐个发现并入队待升级 AICC 运行时 | 新建 |
| `internal/store/queries/apps.sql`、`internal/store/sqlc/*` | 查询待升级 AICC 隐藏应用与初始化任务状态 | 修改 / 生成 |
| `cmd/server/main.go` | 装配 AICC 镜像解析器与升级协调器 | 修改 |
| `runtime/hermes/hermes-aicc/` | AICC 专用 Hermes 构建上下文 | 新建 |
| `Makefile` | 独立构建和生产发布目标 | 修改 |
| `deploy/k8s/local/secret.yaml`、`deploy/k8s/prod/secret.example.yaml`、`deploy/k8s/prod/secret.yaml` | 添加 AICC 镜像配置 | 修改 |
| `docs/configuration.md`、`docs/local-development.md` | 配置及本地构建说明 | 修改 |

### Task 1: 增加 AICC 专用配置及校验

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`
- Modify: `internal/config/runtime_images.go`
- Modify: `internal/config/runtime_images_test.go`
- Modify: `deploy/k8s/local/secret.yaml`
- Modify: `deploy/k8s/prod/secret.example.yaml`
- Modify: `deploy/k8s/prod/secret.yaml`

- [ ] **Step 1: 写失败的配置校验测试**

在 `internal/config/runtime_images_test.go` 新增相邻中文注释的测试：`aicc.runtime_image` 为空时报错文本包含该配置键；合法带 registry、repository、tag 的 ref 通过；包含空格或无 repository 的 ref 被拒绝。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./internal/config -run 'TestValidateAICCRuntimeImage' -count=1`

Expected: FAIL，因为 `ValidateAICCRuntimeImage` 与 `AICCConfig` 尚不存在。

- [ ] **Step 3: 实现最小配置模型与校验**

在 `config.go` 增加：

```go
// AICCConfig 描述在线智能客服独立运行时配置，不能与普通实例 Hermes 镜像列表混用。
type AICCConfig struct {
	// RuntimeImage 是 AICC 隐藏应用唯一允许使用的不可变 Hermes 镜像引用。
	RuntimeImage string `yaml:"runtime_image"`
}
```

在总配置结构增加 `AICC AICCConfig 'yaml:"aicc"'`。在 `Validate` 中于普通 `RuntimeImages` 校验后调用 `ValidateAICCRuntimeImage(c.AICC.RuntimeImage)`；该 helper 使用容器镜像引用解析器校验非空、无空白及可解析的 name/tag 或 digest，并返回包含 `aicc.runtime_image` 的错误。

- [ ] **Step 4: 补齐各环境配置**

在三份 manager YAML 中添加根级 `aicc.runtime_image`。本地引用 `k3d-ocm-registry.localhost:5000/oc-manager-hermes-aicc:dev`；生产示例使用明确的 `__FILL_AICC_RUNTIME_IMAGE_TAG__` 占位；实际生产 Secret 保留当前可回滚的不可变客服镜像 tag，不写入凭据。

- [ ] **Step 5: 运行配置测试**

Run: `go test ./internal/config -count=1`

Expected: PASS。

- [ ] **Step 6: 提交配置边界**

```bash
git add internal/config deploy/k8s/local/secret.yaml deploy/k8s/prod/secret.yaml deploy/k8s/prod/secret.example.yaml
git commit -m "feat(aicc): 增加客服专用运行时镜像配置" -m "新增 aicc.runtime_image 并在配置加载阶段严格校验。\n\n普通实例的 hermes.runtime_images 保持原有用途。"
```

### Task 2: 让 AICC 初始化强制解析专用镜像

**Files:**
- Modify: `internal/worker/handlers/app_initialize.go`
- Modify: `internal/worker/handlers/app_initialize_test.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: 写失败的 handler 测试**

在 `app_initialize_test.go` 增加三组带中文注释的测试：AICC 隐藏 app 使用注入的客服镜像，即使绑定版本的 `image_id` 指向另一普通镜像；普通 app 仍调用既有版本镜像解析；客服镜像 resolver 返回空时，初始化标记失败且不调用编排器。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./internal/worker/handlers -run 'TestAppInitialize.*AICC.*RuntimeImage' -count=1`

Expected: FAIL，因为 handler 尚无 AICC 专用 resolver。

- [ ] **Step 3: 扩展初始化配置并切换解析逻辑**

给 `handlers.AppInitializeConfig` 增加：

```go
// ResolveAICCRuntimeImage 返回客服隐藏应用唯一允许使用的镜像引用。
ResolveAICCRuntimeImage func() (ref string, ok bool)
```

在负责选择镜像的阶段先读取 `app.AiccHidden`：为 true 时只调用 `ResolveAICCRuntimeImage`；否则保留 `ResolveRuntimeImage(version.ImageID)`。错误消息须明确为“未配置 AICC 运行时镜像”，不得回退到版本镜像。

在 `cmd/server/main.go` 注入闭包，返回 `cfg.AICC.RuntimeImage`；普通 `ResolveRuntimeImage` 继续只使用 `cfg.Hermes.RuntimeImages`。

- [ ] **Step 4: 运行定向与完整 handler 测试**

Run: `go test ./internal/worker/handlers -count=1`

Expected: PASS。

- [ ] **Step 5: 提交初始化镜像隔离**

```bash
git add internal/worker/handlers/app_initialize.go internal/worker/handlers/app_initialize_test.go cmd/server/main.go
git commit -m "feat(aicc): 隔离隐藏应用运行时镜像解析" -m "AICC 初始化只读取 aicc.runtime_image。\n\n普通实例继续按助手版本从 hermes.runtime_images 解析镜像。"
```

### Task 3: 保留 AICC 助手版本但解除其镜像职责

**Files:**
- Modify: `internal/service/app_service.go`
- Modify: `internal/service/app_service_test.go`
- Modify: `internal/service/onboarding_service.go`

- [ ] **Step 1: 写失败的 AppService 回归测试**

将 `TestCreateHiddenAICCAppCreatesHiddenAppAndInitializeJob` 的相邻中文注释调整为“版本仅用于模型和技能初始化”；新增断言确保创建代码不要求普通版本镜像解析器。保留“企业没有可用助手版本”失败测试，明确该版本仍是 AICC 的模型和技能来源，而不是镜像来源。

- [ ] **Step 2: 运行测试并确认当前语义不完整**

Run: `go test ./internal/service -run 'TestCreateHiddenAICCApp' -count=1`

Expected: FAIL，测试应暴露旧注释/错误文案仍将版本描述为运行时镜像来源。

- [ ] **Step 3: 更新服务注释与错误边界**

保留 `firstAssistantVersionID`，但将函数及 `CreateHiddenAICCApp` 的注释改为“初始化模型、技能与行为配置来源”。不向 `AICCHiddenAppInput` 增加镜像字段，避免业务层复制配置；镜像仅由 worker 配置解析。删除任何“首个授权版本决定 AICC 镜像”的描述。

- [ ] **Step 4: 运行服务测试**

Run: `go test ./internal/service -run 'TestCreateHiddenAICCApp' -count=1`

Expected: PASS。

- [ ] **Step 5: 提交创建链路语义修正**

```bash
git add internal/service/app_service.go internal/service/app_service_test.go internal/service/onboarding_service.go
git commit -m "refactor(aicc): 保留隐藏应用版本配置职责" -m "AICC 助手版本继续提供模型和技能初始化数据。\n\n运行时镜像选择统一下沉到客服专用配置。"
```

### Task 4: 实现逐个客服运行时升级协调器

**Files:**
- Modify: `internal/store/queries/apps.sql`
- Regenerate: `internal/store/sqlc/apps.sql.go`, `internal/store/sqlc/querier.go`
- Create: `internal/service/aicc_runtime_upgrade_reconciler.go`
- Create: `internal/service/aicc_runtime_upgrade_reconciler_test.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: 写升级协调器失败测试**

新建 `aicc_runtime_upgrade_reconciler_test.go`，每个测试及子场景均写中文注释，覆盖：

```go
// 镜像不一致时每次 Tick 只为一个 AICC 隐藏 app 创建 app_initialize job。
func TestAICCRuntimeUpgradeReconcilerQueuesOneStaleApp(t *testing.T) { /* ... */ }

// 已使用目标镜像、删除 app、初始化中的 app 不得重复入队。
func TestAICCRuntimeUpgradeReconcilerSkipsConvergedAndBusyApps(t *testing.T) { /* ... */ }

// 单个入队失败仅返回带 app ID 的错误，不修改其它候选项。
func TestAICCRuntimeUpgradeReconcilerReportsPerAppQueueFailure(t *testing.T) { /* ... */ }
```

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./internal/service -run 'TestAICCRuntimeUpgradeReconciler' -count=1`

Expected: FAIL，因为协调器及查询尚不存在。

- [ ] **Step 3: 增加最小 sqlc 查询并生成代码**

在 `apps.sql` 增加按 `aicc_hidden = TRUE`、未删除、`runtime_image_ref` 与目标 ref 不同、且没有未完成 `app_initialize` job 的查询；按 `updated_at, id` 排序并 `LIMIT 1`。执行：

```bash
make sqlc-generate
```

不得手工修改生成的 `internal/store/sqlc/*.go`。

- [ ] **Step 4: 实现协调器**

实现 `AICCRuntimeUpgradeReconciler.Tick(ctx)`：目标镜像配置为空时返回配置错误；查询单个待升级 app；使用 `jobutil.EnsureInitJob` 创建或复用 `app_initialize` job；成功后调用 `JobNotifier.Enqueue`。每次 Tick 只处理一项，使新 Pod 就绪和下一项入队之间至少经过下一轮协调，避免全量客服同时被替换。

镜像实际替换由 Task 2 的 `app_initialize` 选择新 `aicc.runtime_image` 实现；初始化失败由既有 job 重试与 app 状态错误处理记录，旧的 `runtime_image_ref` 仅在新镜像拉取成功后更新，供下一轮继续识别失败项。

- [ ] **Step 5: 在启动入口装配周期任务**

在 `cmd/server/main.go` 中，仅当 Kubernetes 编排和 job notifier 已就绪时构造协调器，并通过既有 `onlyLeader` 与 `PeriodicReconciler` 以 15 秒间隔装配。任务名使用 `aicc_runtime_upgrade_reconcile`；未配置客服镜像时启动期已 fail-fast，因此不允许静默跳过。

- [ ] **Step 6: 运行服务与 sqlc 测试**

Run: `go test ./internal/service ./internal/store/sqlc -count=1`

Expected: PASS。

- [ ] **Step 7: 提交受控升级流程**

```bash
git add internal/store/queries/apps.sql internal/store/sqlc internal/service/aicc_runtime_upgrade_reconciler.go internal/service/aicc_runtime_upgrade_reconciler_test.go cmd/server/main.go
git commit -m "feat(aicc): 逐个升级客服专用运行时" -m "检测与配置镜像不一致的客服隐藏应用并逐个入队重建。\n\n复用 app_initialize 的就绪、重试和镜像写回流程，避免批量同时中断。"
```

### Task 5: 增加客服专用构建物和发布命令

**Files:**
- Create: `runtime/hermes/hermes-aicc/`
- Modify: `Makefile`
- Modify: `docs/configuration.md`
- Modify: `docs/local-development.md`

- [ ] **Step 1: 写 Makefile 静态行为测试或检查脚本**

在现有 Makefile 测试惯例中新增检查：`make -n build-aicc-runtime` 的 build context 为 `runtime/hermes/hermes-aicc`、仓库为 `oc-manager-hermes-aicc`；`make -n prod-deploy-aicc-runtime` 只替换 `aicc.runtime_image`，不匹配 `hermes.runtime_images` 或 `ops_image`。

- [ ] **Step 2: 运行检查并确认失败**

Run: `make -n build-aicc-runtime`

Expected: FAIL，目标尚不存在。

- [ ] **Step 3: 创建独立构建上下文**

复制当前生产 Hermes 变体中必要的 Dockerfile、入口脚本、补丁和 AICC 所需 ocops contract 到 `runtime/hermes/hermes-aicc`。移除与版本化通用 Hermes 变体绑定的目录名假设；保留独立 `version.txt`，其版本采用客服专用语义版本，如 `v1.0.0`。

- [ ] **Step 4: 增加构建与发布目标**

在 Makefile 定义 `AICC_RUNTIME_DIR`、`AICC_RUNTIME_VERSION`、`AICC_RUNTIME_IMAGE_REPO` 和不可变 `AICC_RUNTIME_IMAGE`，使用与普通镜像相同的时间戳与 commit tag 规则。新增：

```make
build-aicc-runtime
prod-deploy-aicc-runtime
```

`prod-deploy-aicc-runtime` 必须构建推送客服镜像，精确替换生产 Secret 中唯一的 `aicc.runtime_image`，再调用一次 `update-config`。协调器将从新配置逐个升级既有客服。该命令不得调用 `prod-deploy-hermes-all`。

- [ ] **Step 5: 验证 dry-run 与本地构建**

Run: `make -n prod-deploy-aicc-runtime && make build-aicc-runtime`

Expected: dry-run 仅修改 AICC 配置；本地构建成功并产出客服专用镜像。

- [ ] **Step 6: 更新文档并提交**

记录 `aicc.runtime_image`、独立构建命令、不可变 tag、逐个升级和回滚步骤。提交：

```bash
git add Makefile runtime/hermes/hermes-aicc docs/configuration.md docs/local-development.md
git commit -m "feat(aicc): 增加客服专用运行时发布流程" -m "客服镜像使用独立构建上下文和仓库。\n\n生产发布只更新 aicc.runtime_image，并由协调器逐个更新既有客服。"
```

### Task 6: 全量回归与真实浏览器验证

**Files:**
- Modify only if failures require targeted fixes.

- [ ] **Step 1: 执行后端与前端质量检查**

Run:

```bash
go test ./... -count=1
make openapi-check
make web-types-gen
npm --prefix web run typecheck
npm --prefix web run test:unit
npm --prefix web run build
```

Expected: 全部通过，生成产物无非预期 diff。

- [ ] **Step 2: 重建本地环境并验证镜像隔离**

使用本地构建的 `oc-manager-hermes-aicc:dev` 更新 `aicc.runtime_image`，启动本地环境；创建 AICC 智能体后检查对应隐藏 app 的 `runtime_image_ref` 为客服专用仓库。再创建普通实例，确认其镜像仍来自 `hermes.runtime_images`。

- [ ] **Step 3: 验证客服镜像滚动更新**

构建第二个不可变 AICC 镜像 tag，更新本地 `aicc.runtime_image` 并重启 manager-api。观察协调器只先入队一个 AICC 隐藏 app，待其就绪后再处理下一项；确认会话历史保留，公开页刷新后仍能继续对话。

- [ ] **Step 4: 用真实浏览器完成 AICC 验收**

Run:

```bash
PLAYWRIGHT_BASE_URL=http://ocm.localhost npm --prefix web run test:e2e -- aicc.spec.ts aicc-access-i18n.spec.ts aicc-knowledge.spec.ts
```

并在 Chromium 中人工验证：创建客服、公开链接/挂件发消息、刷新恢复会话、知识库回答、会话状态和线索查看；检查浏览器控制台无新增错误。

- [ ] **Step 5: 最终核验并提交修复**

Run:

```bash
git diff --check
git status --short
git log --oneline -8
```

仅提交本功能修复，使用 Conventional Commits 中文说明；不提交本地凭据、构建缓存或无关验证产物。

## 计划自检

- 配置、创建、初始化、升级协调、构建发布、回归验证均有对应任务。
- 普通实例 `hermes.runtime_images` 的保留路径在 Task 2、Task 3 和 Task 6 中明确回归验证。
- 没有依赖可变镜像 tag；每次更新和回滚都通过配置中的不可变 ref 触发逐个升级。
- 代码步骤均要求先写失败测试并执行，再写最小实现与通过验证。
