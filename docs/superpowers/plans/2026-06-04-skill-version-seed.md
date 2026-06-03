# 版本 Skill 种子注入与助手版本改造 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把助手版本的 skill 配置从「上传 tar」改为「从 skill 库选」（自包含快照），并实现 `seedVersionSkills` 种子注入——实例创建 / 换版本重启 / 重启三时机，把当前绑定版本里实例还没有的 skill 并集注入成 `app_skills`（按 name 去重、已有不覆盖、残留不删）；运行时 skill 来源从 `version.skills_json` 切换为 `app_skills`（bootstrap 改造）。

**Architecture:** `AssistantVersionSkill` 增 `Source/SourceRef/Version` 快照字段，`AddSkillFromLibrary` 替代 `UploadSkill`（从 platform_skills 取元数据写快照）。`seedVersionSkills` 是 worker 内部函数（无 principal、不调 oc-ops，最大努力失败只 warn），挂在 `app_initialize.Handle`（覆盖创建 + 镜像变更重启）与 `AppRestartContainerHandler`（镜像不变分支 Scale(1) 后）。`bootstrap_service` 的 skill 来源改为 `app_skills`，oc-restore 据此拉取。

**Tech Stack:** Go 1.25 / sqlc / testify。

「Hermes Skill 市场」功能 Plan 4（共 6 个）。**依赖 Plan 1（platform_skills、app_skills 表）、Plan 3（app_skills sqlc query）合入。** 复用 Plan 3 的 app_skills store 与缓存路径（种子注入直接复用 platform_skills 的 `cached_tar_path`，不二次拷贝）。

> **现场确认项**：`AssistantVersionSkill`/`decodeSkills`/`UploadSkill` 实际签名；`app_initialize.Handle` 的 phase 结构与 version 加载点；`AppRestartContainerHandler` 镜像不变分支位置；`bootstrap_service.Build` 的 `presignSkills`；模块路径 `oc-manager`。

---

## Task 1: 助手版本 skill 改为「从库选」

**Files:** Modify `internal/service/assistant_version_service.go`（+test）、`internal/api/handlers/assistant_versions.go`、`dto.go`

- [ ] **Step 1: 扩展快照结构** — `AssistantVersionSkill` 加字段（保留 Name/FileSize/FileSha256，FilePath 改为 cached 路径，新增 source/source_ref/version）：
```go
type AssistantVersionSkill struct {
	Source     string `json:"source"`      // platform（首版仅平台库可配进版本）
	SourceRef  string `json:"source_ref"`  // platform=name
	Name       string `json:"name"`
	Version    string `json:"version"`
	CachedPath string `json:"cached_path"` // 对象存储 library/<source>/<ref>/<version>.tar
	FileSize   int64  `json:"file_size"`
	FileSha256 string `json:"file_sha256"`
}
```

- [ ] **Step 2: 写 AddSkillFromLibrary 失败测试** — 平台管理员从库选一个 platform skill 加入版本：service 查 platform_skills 取元数据 + tar_path，写快照进 skills_json，revision+1。覆盖：OK、版本不存在、库 skill 不存在、同 name 已在版本中（冲突）。

- [ ] **Step 3: 实现** — 用 `AddSkillFromLibrary(ctx, principal, versionID string, in AddSkillFromLibraryInput) (AssistantVersionResult, error)` 替代 `UploadSkill`（保留 DeleteSkill）：
```go
type AddSkillFromLibraryInput struct{ Source, SourceRef, Version string }

func (s *AssistantVersionService) AddSkillFromLibrary(ctx context.Context, principal auth.Principal, versionID string, in AddSkillFromLibraryInput) (AssistantVersionResult, error) {
	if !auth.CanManageAssistantVersion(principal) { return AssistantVersionResult{}, ErrAssistantVersionDenied }
	row, err := s.loadVersion(ctx, versionID)
	if err != nil { return AssistantVersionResult{}, err }
	skills, _ := decodeSkills(row.SkillsJson)
	// 取平台库 skill 元数据（首版仅 platform；clawhub 进版本留后续）
	ps, err := s.platformSkills.GetByNameVersion(ctx, in.SourceRef, in.Version) // 注入 PlatformSkillStore
	if err != nil { ... ErrPlatformSkillNotFound }
	for _, k := range skills { if k.Name == ps.Name { return AssistantVersionResult{}, ErrAssistantVersionSkillNameTaken } }
	skills = append(skills, AssistantVersionSkill{
		Source: "platform", SourceRef: ps.Name, Name: ps.Name, Version: ps.Version,
		CachedPath: ps.TarPath, FileSize: ps.FileSize, FileSha256: ps.FileSha256,
	})
	return s.persistSkills(ctx, row, skills)
}
```
（`platformSkills` 注入 `PlatformSkillStore`；新增哨兵 `ErrAssistantVersionSkillNameTaken`。`SkillBlobStore` 不再被 version 直接使用，移除其对 version 上传的依赖。）

- [ ] **Step 4: handler 改请求体** — `POST /assistant-versions/:id/skills` 从 multipart 改为 JSON `{source, source_ref, version}`（DTO 进 dto.go），调 AddSkillFromLibrary。

- [ ] **Step 5: 测试 + 提交** — `go test ./internal/service/ -run AssistantVersion && go build ./...`。提交：`feat(skill): 助手版本 skill 改为从库选（自包含快照）`。

---

## Task 2: seedVersionSkills 种子注入（创建/换版本重启/重启）

**Files:** Create `internal/worker/handlers/seed_version_skills.go`（+test）；Modify `internal/worker/handlers/app_initialize.go`、`app_runtime_ops.go`

- [ ] **Step 1: 写 seedVersionSkills 失败测试** — 内部函数，并集注入：
```go
// 版本里实例没有的 skill 注入成 app_skills；已有的不覆盖；残留不删。
func TestSeedVersionSkills_Union(t *testing.T) {
	store := newFakeSeedStore()
	store.appSkills["app-1"] = []string{"weather"}        // 实例已有 weather
	version := versionWithSkills("weather", "translate")  // 版本含 weather + translate
	err := seedVersionSkills(context.Background(), store, "app-1", version)
	require.NoError(t, err)
	got := store.appSkills["app-1"]
	assert.ElementsMatch(t, []string{"weather", "translate"}, got) // translate 新增，weather 不重复
	// 验证 weather 未被覆盖（仍是原 version），translate 用版本快照写入
}
```

- [ ] **Step 2-4: 实现 + 测试 + 提交** — Create `seed_version_skills.go`：
```go
// AppSkillSeedStore 是种子注入所需的最小存取（无权限，系统内部用）。
type AppSkillSeedStore interface {
	GetAppSkillByAppAndName(ctx context.Context, arg sqlc.GetAppSkillByAppAndNameParams) (sqlc.AppSkill, error)
	CreateAppSkill(ctx context.Context, arg sqlc.CreateAppSkillParams) error
}

// seedVersionSkills 把 version.skills_json 里实例尚无的 skill 并集注入 app_skills。
// 无 principal、不调 oc-ops（pod 起来后 bootstrap/oc-restore 据 app_skills 拉取）；
// 最大努力：单条失败只 slog.Warn，不阻断初始化主流程。
func seedVersionSkills(ctx context.Context, store AppSkillSeedStore, appID string, version sqlc.AssistantVersion) error {
	skills, err := decodeSkills(version.SkillsJson) // 复用 service 包的解码（或在 worker 复制一份解码逻辑）
	if err != nil { return err }
	for _, k := range skills {
		if _, err := store.GetAppSkillByAppAndName(ctx, sqlc.GetAppSkillByAppAndNameParams{AppID: appID, Name: k.Name}); err == nil {
			continue // 已有，不覆盖
		} else if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("种子注入查询失败", "app", appID, "skill", k.Name, "err", err); continue
		}
		meta, _ := json.Marshal(map[string]any{"seeded_from_version": version.ID})
		if err := store.CreateAppSkill(ctx, sqlc.CreateAppSkillParams{
			ID: uuid.NewString(), AppID: appID, Name: k.Name, Source: k.Source, SourceRef: k.SourceRef,
			Version: k.Version, CachedTarPath: k.CachedPath, SourceMetadata: meta,
			FileSize: k.FileSize, FileSha256: k.FileSha256,
		}); err != nil {
			slog.Warn("种子注入写入失败", "app", appID, "skill", k.Name, "err", err)
		}
	}
	return nil
}
```
> `decodeSkills`/`AssistantVersionSkill` 在 service 包；worker 包可导入 service 包或将解码逻辑提取到共享位置（实现时按现有依赖方向决定，避免 import 环）。提交：`feat(skill): 增加 seedVersionSkills 种子并集注入`。

- [ ] **Step 5: 挂到 app_initialize** — 在 `app_initialize.Handle` 加载 version 之后、创建容器之前，调 `seedVersionSkills(ctx, seedStore, appID, version)`（覆盖创建 + 镜像变更重启两个走 init 的路径）。失败只 warn 不 markFailed。加集成测试或在 handler 测试补一条种子注入断言。提交：`feat(skill): app_initialize 注入版本种子 skill`。

- [ ] **Step 6: 挂到镜像不变重启分支** — 在 `AppRestartContainerHandler.Handle` 的镜像不变分支（`Scale(1)` 成功后、`SetAppAppliedVersion` 前），同样调 `seedVersionSkills`（补齐用户重启时版本新增的 skill）。提交：`feat(skill): 镜像不变重启时补齐版本新增 skill`。

---

## Task 3: bootstrap 改从 app_skills 取 skill

**Files:** Modify `internal/service/bootstrap_service.go`（+test）

- [ ] **Step 1: bootstrapStore 加 ListAppSkills** — 接口加 `ListAppSkills(ctx, appID) ([]sqlc.AppSkill, error)`（dbStore.Queries 已实现，来自 Plan 3 的 sqlc）。

- [ ] **Step 2: presignSkills 改来源** — `Build` 的 step 5 从「解析 version.SkillsJson」改为「`ListAppSkills(appID)`」，对每行 `PresignSkill(ctx, row.CachedTarPath, ttl)` 生成 URL，`BootstrapSkill{Name: row.Name, RelPath: ..., URL}`，`skillRelPaths` 用 app_skills 的归档相对路径（注意 tar/zip 扩展名随 cached_tar_path）。

- [ ] **Step 3: 测试 + 提交** — 更新 bootstrap_service 测试：预置 app_skills 行，断言 bootstrap 输出含这些 skill 的预签名 URL（不再依赖 version.skills_json）。`go test ./internal/service/ -run Bootstrap`。提交：`feat(skill): bootstrap skill 来源改为 app_skills（运行时只看实例 skill）`。

> 注意：本 task 让运行时 skill 完全来自 app_skills，与 Plan 3 的 oc-restore 改造（恢复自创 + 读 app_skills）配套。oc-restore 读 app_skills 清单的 bash 改造若未在 Plan 5 完成，在此或 Plan 5 补齐（oc-restore 从 bootstrap `.skills[]` 拉，bootstrap 既已改为 app_skills 来源，oc-restore 无需改字段名，自动生效）。

---

## Task 4: OpenAPI / 前端类型同步

- [ ] `make openapi-gen && make web-types-gen && make openapi-check && make test && make vet`，全过。提交：`chore(skill): 同步助手版本从库选接口契约与前端类型`。

---

## Self-Review 备注

- **Spec 覆盖**：助手版本从库选（自包含快照）;种子并集注入三时机（创建/换版本重启/重启）——换版本经 `SwitchAppVersion`（清 applied_*）+ 用户重启触发，重启走 seedVersionSkills 补齐新增、不删旧的、按 name 去重不覆盖;运行时 skill 来源改 app_skills（bootstrap）。
- **关键设计**：种子注入是 worker 内部无权限函数（创建时无 principal），与 AppSkillService.Install（带权限/oc-ops，用户操作）分离;最大努力失败只 warn，不阻断初始化（同 RAGFlow dataset 等级）。
- **种子重装是预期行为**：用户卸载的种子 skill，换到也含它的版本会被重新注入（spec 已确认，不维护卸载清单）。
- **未覆盖**：clawhub skill 配进助手版本（首版仅 platform 可配进版本，clawhub 仅 per-app 安装）;前端助手版本编辑页改「从库选」选择器（Plan 6）。
- **现场确认项**：decodeSkills 跨包复用避免 import 环;app_initialize phase 结构;SwitchAppVersion 清 applied_* 已有（无需改）;模块路径。
