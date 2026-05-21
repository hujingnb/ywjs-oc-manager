# 助手版本 Phase 4 实施计划：版本数据接入 manifest 与变更检测

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让实例真正按其绑定的助手版本运行——把版本的智能路由、skill、内置提示词、镜像写进 manifest v2；初始化/重启时记录「已应用的版本修订与镜像」；实例列表据此显示「需重启更新」；并提供「切换版本」动作。

**Architecture:** Go 写入侧补齐 manifest 契约 v2（Phase 2 已让 oc-entrypoint 能消费它）。`app_initialize` 与 restart 刷新链路加载实例绑定的 `assistant_versions` 行，解析 `image_id`→镜像 ref、`routing`、`system_prompt`、`skills_json`，写入 manifest 并把版本 skill tar 主副本推送到节点；成功后回写 `apps.applied_version_revision` / `applied_image_ref`。`version_synced` 是计算值：`applied_version_revision == version.revision AND applied_image_ref == 配置解析(version.image_id)`，在实例列表查询时 join 版本计算。切换版本动作改 `apps.version_id`，使实例自然进入「需重启」态。

**Tech Stack:** Go、pgx/v5、sqlc、gin、testify；前端 Vue 3 + naive-ui。

**关联文档：** 设计 spec `docs/superpowers/specs/2026-05-21-assistant-version-design.md` §3.5 / §5 / §7.5 / §7.6 / §8.4 / §8.5；Phase 2 已交付的 manifest v2 oc-entrypoint 消费端。

**范围与边界：**
- 本阶段把版本数据真正接进运行链路。`apps.model_id` / `persona_mode` / `app_prompt` / `model_synced` 与 `organizations.model_id`、`organization_personas` 表的**删除**仍属 Phase 5。
- 本阶段是 additive + 行为替换：manifest 改为写 v2（routing/skills/仅平台层 rule）；`app.model_id` 不再决定 manifest 的 model（改由 version.main_model），但列暂留。
- `apps.applied_version_revision` / `applied_image_ref` / `version_id` 列已由 Phase 1 迁移建好。

---

## 关键设计决策

1. **manifest 的 model / routing / persona / skill 全部来自版本。** `app_initialize` 与 restart 刷新加载 `app.version_id` 对应的 `assistant_versions` 行：`manifest.app.model = version.main_model`，`manifest.routing = version.routing`，`resources/persona.md = version.system_prompt`，`resources.skills = 推送的版本 skill tar 列表`。平台层 rule 仍来自配置 `hermes.system_prompt_template`。组织层 / 应用层 rule 不再写（Phase 2 的 oc-entrypoint 解析器已视其为可选）。

2. **镜像来自版本。** `app_initialize` 把 `version.image_id` 用配置 `hermes.runtime_images` 解析成镜像 ref；`phasePullRuntimeImage` 拉该 ref。不再用全局 `cfg.Hermes.RuntimeImage`（该字段 Phase 5 清理）。

3. **applied 字段。** 初始化成功 / 重启成功后写 `apps.applied_version_revision = version.revision`、`apps.applied_image_ref = 解析后的镜像 ref`。

4. **version_synced 计算。** 不落库布尔列。实例列表查询 join `assistant_versions` 取 `revision` 与 `image_id`；service 用启动配置把 `image_id` 解析成 ref，`version_synced = (applied_version_revision == revision) AND (applied_image_ref == resolvedRef)`。`AppResult` 新增 `version_synced` 字段。`model_synced` 字段与列暂留（Phase 5 删），本阶段前端改用 `version_synced`。

5. **切换版本。** 新增 `POST /api/v1/apps/{id}/version` 动作：校验目标 `version_id` 在实例所属组织的 allowlist 内，写 `apps.version_id`。切换后 `applied_version_revision` / `applied_image_ref` 不变、与新版本不匹配 → 实例自动进入「需重启」。

6. **存量实例。** `app.version_id` 为空的存量实例：`app_initialize` / restart 遇到空 `version_id` 时报明确错误（「实例未绑定助手版本」）。符合「不考虑存量」——本地 dev 存量实例需重建。

7. **version skill tar 来源。** 版本 skill tar 主副本在 manager 文件系统 `<app.data_root>/versions/<version_id>/skills/<name>.tar`（Phase 1 的 `FSSkillBlobStore` 写入）。`app_initialize` / restart 刷新读取它们并经 `AppInputUploader` 推送到节点 `apps/<id>/input/resources/skills/<name>.tar`。

---

## 文件结构

| 文件 | 职责 | 动作 |
|---|---|---|
| `internal/store/queries/apps.sql` | `SetAppAppliedVersion`、`SetAppVersion`、`ListAppsByOrgWithVersion`、`GetAppWithVersion` | 修改 |
| `internal/store/queries/assistant_versions.sql` | （已有 `GetAssistantVersion`，复用） | — |
| `internal/store/sqlc/*` | 生成 | 重新生成 |
| `internal/integrations/hermes/manifest.go` | `Manifest` 加 `routing` / `resources.skills`，rules 仅 platform | 修改 |
| `internal/integrations/hermes/app_input.go` | `AppInputData` v2、`WriteAppInput`、`BuildAppInputData` | 修改 |
| `internal/worker/handlers/app_initialize.go` | 加载版本、解析镜像、推送 skill tar、写 applied_* | 修改 |
| `internal/worker/handlers/app_runtime_ops.go` | restart 成功后写 applied_* | 修改 |
| `cmd/server/main.go` | 装配：worker handler / refresher 注入版本加载与镜像解析、skill 主副本读取 | 修改 |
| `internal/service/app_service.go` | `version_synced` 计算、`AppResult.VersionSynced` | 修改 |
| `internal/service/runtime_operation_service.go` | 切换版本 service 方法 | 修改 |
| `internal/api/handlers/app_runtime.go`（或 apps.go） | 切换版本 handler | 修改 |
| `internal/api/handlers/dto.go` | 切换版本请求体 | 修改 |
| `openapi/openapi.yaml`、`web/src/api/generated.ts` | OpenAPI 同步 | 重新生成 |
| `web/src/api/hooks/useApps.ts` | 切换版本 hook、`version_synced` | 修改 |
| `web/src/pages/apps/AppsPage.vue` / 实例列表 / 详情 | 「需重启」提示、切换版本动作 | 修改 |

构建/测试：`make vet`、`go test ./internal/... ./cmd/...`、`make web-typecheck`、`make web-test`、`make openapi-gen` + `make web-types-gen`。

---

## Task 1：apps sqlc 查询——applied 字段、切换版本、版本联查

**Files:** `internal/store/queries/apps.sql`、`internal/store/sqlc/*`

- [ ] **Step 1：加查询** — 在 `apps.sql` 追加：

```sql
-- name: SetAppAppliedVersion :one
-- 初始化/重启成功后记录已应用的版本修订与镜像 ref，用于 version_synced 检测。
UPDATE apps
SET applied_version_revision = $2,
    applied_image_ref = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetAppVersion :one
-- 切换实例绑定的助手版本；切换后 applied_* 不变，实例自然进入需重启态。
UPDATE apps
SET version_id = $2,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: GetAppWithVersion :one
-- 取实例及其绑定版本的 revision / image_id，供 version_synced 计算。
SELECT sqlc.embed(apps), av.revision AS version_revision, av.image_id AS version_image_id
FROM apps
JOIN assistant_versions av ON av.id = apps.version_id
WHERE apps.id = $1;

-- name: ListAppsByOrgWithVersion :many
-- 组织实例列表联查版本 revision / image_id，供 version_synced 批量计算。
SELECT sqlc.embed(apps), av.revision AS version_revision, av.image_id AS version_image_id
FROM apps
JOIN assistant_versions av ON av.id = apps.version_id
WHERE apps.org_id = $1 AND apps.deleted_at IS NULL
ORDER BY apps.created_at DESC, apps.id DESC
LIMIT $2 OFFSET $3;
```

> 注：`apps.version_id` 是可空列；`JOIN` 会过滤掉 `version_id IS NULL` 的存量实例。这是可接受的——本阶段「不考虑存量」，存量未绑定版本的实例不在版本化列表内（如需仍展示，改 `LEFT JOIN` 并在 service 侧把空版本视作 version_synced=false；执行时按是否有存量需求决定，默认 `JOIN`）。若选 `LEFT JOIN`，`version_revision` / `version_image_id` 用可空类型。

- [ ] **Step 2：生成** — `make sqlc-generate`，确认 `SetAppAppliedVersionParams`、`SetAppVersionParams`、`GetAppWithVersionRow`、`ListAppsByOrgWithVersionRow` 生成。`sqlc.embed` 让 Row 内含完整 `App` 结构。

- [ ] **Step 3：编译** — `go build ./...`（service 还没用新查询，仅验证生成产物合法）。

- [ ] **Step 4：提交** — `git add internal/store/queries/apps.sql internal/store/sqlc/` ；commit `feat(assistant-version): apps sqlc 支持 applied 版本字段与版本联查`。提交信息：Conventional Commits、中文摘要，正文空一行后 `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`。

---

## Task 2：manifest.go 契约 v2（Go 写入侧）

**Files:** `internal/integrations/hermes/manifest.go`、`manifest_test.go`

- [ ] **Step 1：读现状** — `Manifest` 当前：`App`、`Credentials`、`Resources{Persona, Rules{Platform,Organization,Application}}`。

- [ ] **Step 2：改结构体** — `Manifest` 加 `Routing map[string]string \`yaml:"routing,omitempty"\``；`ManifestResources` 加 `Skills []string \`yaml:"skills,omitempty"\``；`ManifestRules` 删 `Organization` / `Application`，仅留 `Platform`。`MarshalManifestYAML` 不变（结构体驱动）。

- [ ] **Step 3：测试** — `manifest_test.go`：构造带 routing + skills 的 Manifest，marshal 后断言 yaml 含 `routing:` 与 `resources.skills`、不含 `organization`/`application`。空 routing/skills 时 `omitempty` 不输出。

- [ ] **Step 4：验证** — `go build ./internal/integrations/hermes/` 此时会因 `app_input.go` 仍引用 `ManifestRules.Organization` 失败——Task 3 一并修。本任务与 Task 3 视为连续一组，Task 3 末尾统一验证编译。

- [ ] **Step 5：提交**（与 Task 3 合并提交，或本任务先不提交，Task 3 一起 commit——执行者按 subagent-driven 流程；spec/quality 审查在 Task 3 后确认整体编译通过）。

---

## Task 3：app_input.go 契约 v2

**Files:** `internal/integrations/hermes/app_input.go`、`app_input_test.go`

- [ ] **Step 1：读现状** — `AppInputData`、`WriteAppInput`、`BuildAppInputData`、`AppInputBuildOptions`。

- [ ] **Step 2：改 `AppInputData`** — 加 `Routing map[string]string`、`SkillRelPaths []string`（manifest 用的 skill tar 相对路径，如 `resources/skills/weather.tar`）。`PersonaText` 语义改为「版本内置提示词」。删 `OrganizationRule`、`ApplicationRule`（保留 `PlatformRule`）。

- [ ] **Step 3：改 `WriteAppInput`** — 不再写 `organization-rules.md` / `application-rules.md`；`resources/persona.md` 写版本提示词；`resources/platform-rules.md` 写平台 rule；`Manifest` 加 `Routing` 与 `Resources.Skills: in.SkillRelPaths`，`Resources.Rules` 仅 `Platform`。占位符渲染（`RenderPersonaText` / `RenderRuleText`）保留。

- [ ] **Step 4：改 `BuildAppInputData`** — 入参增加版本数据。新增 `AppInputVersionData struct { MainModel string; Routing map[string]string; SystemPrompt string; SkillRelPaths []string }`，`BuildAppInputData` 签名加该参数。`Model` 用 `version.MainModel`（空则兜底 `opts.DefaultModel` / `"default"`）；`Routing`、`SkillRelPaths`、`PersonaText` 来自版本数据。`OrganizationRule` / `ApplicationRule` 删除赋值。

- [ ] **Step 5：测试** — `app_input_test.go`：更新构造，断言 manifest 含 routing/skills、persona 为版本提示词、不含 org/app rules。

- [ ] **Step 6：验证** — `go build ./...` 此时 `app_initialize.go` 调用 `BuildAppInputData` 签名不符——Task 4 修。Task 2+3+4 视为一组，Task 4 末尾验证整体编译。

- [ ] **Step 7：提交** — Task 2 + Task 3 合并一个 commit：`feat(assistant-version): manifest 写入侧升级到契约 v2`。

---

## Task 4：app_initialize 加载版本、解析镜像、推送 skill、写 applied

**Files:** `internal/worker/handlers/app_initialize.go`、`app_initialize_test.go`

- [ ] **Step 1：读现状** — 5 阶段 handler、`AppInitializeStore`、`AppInitializeConfig`、`phasePullRuntimeImage`（用 `cfg.RuntimeImage`）、`phasePrepare` / `writeAppInput`（调 `BuildAppInputData` + `hermes.WriteAppInput` + `writeKnowledgeIntoInput`）。

- [ ] **Step 2：版本加载能力** — `AppInitializeStore` 加 `GetAssistantVersion(ctx, pgtype.UUID) (sqlc.AssistantVersion, error)` 与 `SetAppAppliedVersion(ctx, sqlc.SetAppAppliedVersionParams) (sqlc.App, error)`。`AppInitializeConfig` 加镜像解析能力：`RuntimeImages []config.RuntimeImageConfig`（或一个 `ResolveImage func(id string)(string,bool)` 注入，避免 worker 包依赖 config 包——参考 `AppInitializeLLMConfig` 的解耦做法，定义本包内 `RuntimeImageOption` 并由 cmd/server 适配注入）。加 skill 主副本读取能力 `SkillBlobReader interface { OpenSkill(relPath string) (io.ReadCloser, error) }`（读 `FSSkillBlobStore` 写入的 tar）。

- [ ] **Step 3：handler 起始加载版本** — `Handle` 开头：若 `app.VersionID` 无效 → `markFailed`「实例未绑定助手版本」。加载 `version := store.GetAssistantVersion(app.VersionID)`。解析 `version.ImageID` → 镜像 ref（未知 id → 失败）。把 ref、version 传给后续阶段。

- [ ] **Step 4：`phasePullRuntimeImage`** — 用版本解析出的镜像 ref 取代 `cfg.RuntimeImage`。

- [ ] **Step 5：`writeAppInput`** — 解析 `version.RoutingJson` → `map[string]string`、`version.SkillsJson` → skill 列表；把版本 skill tar 从 `SkillBlobReader` 读出、经 `inputFiles.UploadAppInputFile` 推送到 `resources/skills/<name>.tar`（新增 `writeSkillsIntoInput`，与 `writeKnowledgeIntoInput` 并列）；`BuildAppInputData` 传入 `AppInputVersionData`（main_model / routing / system_prompt / skill rel paths）。

- [ ] **Step 6：写 applied** — `Handle` 在推进到 `binding_waiting` 后（或 `writeInitAuditLog` 附近）调 `SetAppAppliedVersion(app.ID, version.Revision, resolvedRef)`。

- [ ] **Step 7：测试** — `app_initialize_test.go`：内存桩补 `GetAssistantVersion` / `SetAppAppliedVersion` / 镜像解析 / skill reader；断言 manifest 走版本数据、applied_* 被写、未绑定版本时失败。

- [ ] **Step 8：验证** — `go build ./...`、`go test ./internal/worker/...`、`make vet` 全绿。

- [ ] **Step 9：提交** — `feat(assistant-version): 实例初始化按版本写 manifest 并记录 applied 修订`。

---

## Task 5：restart 刷新写版本数据与 applied

**Files:** `internal/worker/handlers/app_runtime_ops.go`、其测试、`cmd/server/main.go`（refresher 装配）

- [ ] **Step 1：读现状** — `AppRestartContainerHandler`、`AppInputRefresher.RefreshAppInput`、cmd/server 的 `appInputRefresher` 装配；restart 末尾的 `SetAppModelSynced`。

- [ ] **Step 2：refresher 走版本数据** — cmd/server 装配的 `appInputRefresher.RefreshAppInput` 改为：加载实例版本 → 解析镜像 ref → 用 `BuildAppInputData`（版本数据）+ `hermes.WriteAppInput` 重写 input + 推送版本 skill tar。与 Task 4 的 `writeAppInput` 共用同一套版本数据装配逻辑（抽公共函数，避免 init 与 restart 漂移）。

- [ ] **Step 3：restart 末尾写 applied** — `AppRestartContainerHandler.Handle` 把 `SetAppModelSynced` 替换/补充为 `SetAppAppliedVersion(app.ID, version.Revision, resolvedRef)`（`model_synced` 列 Phase 5 删，本阶段可同时保留 `SetAppModelSynced` 也可移除——优先：保留 restart 对 `SetAppModelSynced` 的调用不动、额外加 `SetAppAppliedVersion`，把列清理留给 Phase 5）。`AppRuntimeStore` 接口加 `GetAssistantVersion` / `SetAppAppliedVersion`。

- [ ] **Step 4：测试 + 验证** — restart handler 测试更新；`go build ./...`、`go test ./internal/worker/... ./cmd/...`、`make vet` 全绿。

- [ ] **Step 5：提交** — `feat(assistant-version): 实例重启刷新按版本写 manifest 并记录 applied`。

---

## Task 6：version_synced 计算

**Files:** `internal/service/app_service.go`、`app_service_test.go`

- [ ] **Step 1：读现状** — `AppResult`（含 `ModelSynced`）、`toAppResult`、`ListByOrg`、`Get`、`AppStore`。

- [ ] **Step 2：改 service** — `AppStore` 加 `ListAppsByOrgWithVersion` / `GetAppWithVersion`。`AppService` 加镜像解析依赖（`AppImageResolver interface { ResolveRuntimeImage(id string) (string, bool) }`，cmd/server 用配置适配注入）。`AppResult` 加 `VersionSynced bool \`json:"version_synced"\``、`VersionID string`。`ListByOrg` / `Get` 改用带版本的查询，计算 `version_synced = (app.AppliedVersionRevision == row.VersionRevision) AND (app.AppliedImageRef == resolvedRef)`。`ModelSynced` 字段暂留。

- [ ] **Step 3：测试** — `app_service_test.go`：覆盖 version_synced 在「修订一致+镜像一致」「修订不一致」「镜像 ref 不一致」三种情况。

- [ ] **Step 4：验证 + 提交** — `go test ./internal/service/`、`go build ./...`；commit `feat(assistant-version): 实例列表计算 version_synced 需重启状态`。

---

## Task 7：切换版本 service + handler + DTO

**Files:** `internal/service/runtime_operation_service.go`（或 app_service）、`internal/api/handlers/*`、`dto.go`

- [ ] **Step 1：读现状** — 找到实例运行时操作 service（restart 等动作所在）与对应 handler、路由。

- [ ] **Step 2：service** — 新增 `SwitchAppVersion(ctx, principal, appID, versionID string) (AppResult, error)`：校验调用者可管理该实例（`auth.CanManageApp` 或等价）；加载 app → 加载 org → 校验 `versionID ∈ org.assistant_version_ids`（复用 Phase 3 的 `versionInOrgAllowlist` 思路）；`SetAppVersion(app.ID, versionUUID)`；返回更新后的 `AppResult`（`version_synced` 此时为 false）。

- [ ] **Step 3：handler + DTO + 路由** — `dto.go` 加 `SwitchAppVersionRequest { VersionID string \`json:"version_id" binding:"required"\` }`；handler `POST /api/v1/apps/:id/version`；错误映射（allowlist 外 → 400/409，无权 → 403）。

- [ ] **Step 4：测试** — service + handler 测试：成功切换、目标版本不在 allowlist 被拒、无权被拒。

- [ ] **Step 5：验证 + 提交** — 全量后端测试；commit `feat(assistant-version): 新增实例切换助手版本动作`。

---

## Task 8：cmd/server 接线

**Files:** `cmd/server/main.go`

- [ ] **Step 1：装配** — 给 `app_initialize` handler、restart refresher、`AppService`、运行时操作 service 注入：版本加载（`store.GetAssistantVersion` 适配）、镜像解析（配置 `runtime_images` 适配器，复用 Phase 1 的 `RuntimeImageAdapter` 思路）、skill 主副本读取（`FSSkillBlobStore` 读能力 / 新增 `OpenSkill`）。

- [ ] **Step 2：验证 + 提交** — `go build ./...`、`make vet`、`go test ./internal/... ./cmd/...` 全绿；commit `feat(assistant-version): 装配版本数据接入运行链路`。

---

## Task 9：OpenAPI 与前端类型同步

**Files:** `openapi/openapi.yaml`、`web/src/api/generated.ts`

- [ ] `make openapi-gen` + `make web-types-gen` + `make openapi-check`（须干净）；commit `chore(assistant-version): 同步 OpenAPI 与前端类型`。

---

## Task 10：前端——实例列表「需重启」提示

**Files:** `web/src/api/hooks/useApps.ts`、实例列表页（`AppsPage.vue` / `MembersPage.vue` 实例展示处）、其 `.spec.ts`

- [ ] **Step 1：读现状** — 找到现有 `model_synced` 的「需重启」提示 UI（实例列表/详情）。

- [ ] **Step 2：改造** — 前端展示改用 `version_synced`：`version_synced === false` 时显示「版本已更新，需重启」提示（复用现有 model_synced 提示样式与位置）。`useApps.ts` 的 App 类型用生成的 `version_synced`。

- [ ] **Step 3：测试 + 验证** — `.spec.ts` 更新；`make web-typecheck`、`make web-test` 全绿；commit `feat(assistant-version): 实例列表显示版本需重启提示`。

---

## Task 11：前端——切换版本动作

**Files:** `web/src/api/hooks/useApps.ts`、实例详情页、其 `.spec.ts`

- [ ] **Step 1：hook** — `useApps.ts` 加 `useSwitchAppVersion` mutation（`POST /api/v1/apps/{id}/version`，body `{version_id}`）。

- [ ] **Step 2：UI** — 实例详情页加「切换版本」动作：单选 select 从该实例所属组织 allowlist 内的版本选（取组织详情 + 版本列表交集，同 Phase 3 实例创建表单的做法）；提交后实例进入「需重启」态。

- [ ] **Step 3：测试 + 验证** — `.spec.ts`；`make web-typecheck`、`make web-test` 全绿；commit `feat(assistant-version): 实例详情支持切换助手版本`。

---

## Task 12：真实浏览器功能验证

**Files:** 无

- [ ] 用 `webapp-testing` 技能走完整流程：① 创建绑定版本 A 的实例并初始化成功；② 后台编辑版本 A 的提示词（revision +1）→ 实例列表出现「需重启」；③ 点重启 → 提示消失，容器按新提示词运行；④ 切换实例到版本 B → 出现「需重启」→ 重启 → 生效；⑤ 用 v2026.5.7 / v2026.5.16 两个镜像的版本测镜像切换。截图留证；发现问题先修再验。

---

## Self-Review

**Spec 覆盖：** §5 manifest v2 写入侧 → Task 2-3；§7.5 init/restart 写版本数据 → Task 4-5；§3.5 version_synced → Task 6；§7.6 切换版本 → Task 7 + 11；§8.4 需重启提示 → Task 10；§8.5 切换版本动作 → Task 11；镜像来自版本 → Task 4。

**不在本计划：** 删 `apps.model_id`/`persona_mode`/`app_prompt`/`model_synced`、`organizations.model_id`、`organization_personas` 表、`cfg.Hermes.RuntimeImage` 单值字段、persona service/handler/页面（全部 Phase 5）。

**编译排序：** Task 1（sqlc）additive；Task 2+3 一组（manifest + app_input 一起改才编译通过）；Task 4 接 `BuildAppInputData` 新签名后整体编译恢复。执行时 Task 2-4 连续推进、Task 4 末尾验证编译。

**init 与 restart 共用版本数据装配：** Task 5 Step 2 要求抽公共函数，避免「初始化写的 manifest」与「restart 重写的 manifest」字段漂移——这是 spec 反复强调的核心约束。

**类型一致性：** `routing` / `skills` 在 manifest.go yaml tag、oc-entrypoint 解析、版本表 `routing_json`/`skills_json` 一致；`version_synced` 在 `AppResult` json tag 与前端一致；`applied_version_revision`/`applied_image_ref` 在 sqlc 列、`SetAppAppliedVersionParams`、service 计算处一致。

---

## 后续

Phase 4 交付后，实例真正按绑定版本运行、版本变更可被检测并通过重启生效、可切换版本。最后：
- **Phase 5：** 删除 `organization_personas` 表、`organizations.model_id`、`apps.model_id`/`persona_mode`/`app_prompt`/`model_synced` 列、`cfg.Hermes.RuntimeImage` 单值字段，及相关 service / handler / 前端 persona 页与 dead code（`UpdateAppModelsByOrg`、`modelValidator` 等）。
