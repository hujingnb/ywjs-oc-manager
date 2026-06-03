# Per-App Skill 装卸更新（热装 + reload + 实时对账）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现实例级 skill 的安装/卸载/更新（从市场来源取归档 → 缓存到对象存储 → 落 `app_skills` → 经 oc-ops 热装并触发 reload，不重启），以及「已安装列表」的实时对账（容器实际 skill × app_skills × 内置清单 → status）、当前版本 skill 删除保护、`latest_version` 定时检测。

**Architecture:** `AppSkillService` 注入 `PlatformSkillStore`+`LibraryBlobStore`（platform 来源取归档）、`ClawHubDownloader`（clawhub 来源下载）、`OcOpsResolver`+ocops client（热装/热删/reload/列）、`AppStore`+`AssistantVersionStore`（删除保护查当前版本 skills_json）。日常装卸**不重启**；pending 兜底用 `RuntimeOperationService.Trigger(restart)`。

**Tech Stack:** Go 1.25 / Gin / sqlc / go-redis（间接）/ testify。

「Hermes Skill 市场」功能 Plan 3（共 6 个）。**依赖 Plan 1（app_skills 表、LibraryBlobStore、PlatformSkillService）、Plan 2（市场聚合、ClawHub client）、Plan 5（ocops client SkillList/Install/Delete/Reload、skills-builtin.json）全部合入。**

> **执行时现场确认项**：模块路径 `oc-manager`；`OcOpsResolver`/`ocops.Endpoint`/`RuntimeOperationService.Trigger` 实际签名（`internal/service/ocops.go`、`runtime_operation_service.go`）；`decodeSkills`/`AssistantVersionSkill` 复用（`assistant_version_service.go`）；ocops client `SkillInstall` 的 multipart 签名（Plan 5）。

---

## File Structure

新建：
- `internal/store/queries/app_skills.sql` + 生成的 `internal/store/sqlc/app_skills.sql.go`
- `internal/service/app_skill_service.go` + `_test.go` — 核心装卸更新 + 对账
- `internal/service/skill_update_checker.go` + `_test.go` — 定时回源 latest_version
- `internal/api/handlers/app_skills.go` + `_test.go`
修改：
- `internal/service/errors.go`（app skill 哨兵）、`internal/auth/authorizer.go`（CanManageAppSkill）
- `internal/service/platform_skill_service.go`（加 `GetForInstall`）
- `internal/api/router.go`、`cmd/server/main.go`、`openapi/openapi.yaml`、`web/src/api/generated.ts`

---

## Task 1: app_skills sqlc 查询

**Files:** Create `internal/store/queries/app_skills.sql`；生成 `internal/store/sqlc/app_skills.sql.go`

- [ ] **Step 1: 写查询** — Create `internal/store/queries/app_skills.sql`（占位符 `?`，参照 `platform_skills.sql`）：

```sql
-- name: ListAppSkillsByApp :many
SELECT * FROM app_skills WHERE app_id = ? ORDER BY name ASC;

-- name: GetAppSkillByAppAndName :one
SELECT * FROM app_skills WHERE app_id = ? AND name = ?;

-- name: CreateAppSkill :exec
INSERT INTO app_skills (
    id, app_id, name, source, source_ref, version, latest_version,
    cached_tar_path, source_metadata, file_size, file_sha256, installed_by
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteAppSkillByAppAndName :exec
DELETE FROM app_skills WHERE app_id = ? AND name = ?;

-- name: UpdateAppSkillVersion :exec
UPDATE app_skills SET version = ?, cached_tar_path = ?, file_size = ?, file_sha256 = ?,
    source_metadata = ?, latest_version = NULL WHERE app_id = ? AND name = ?;

-- name: UpdateAppSkillLatest :exec
UPDATE app_skills SET latest_version = ?, last_checked_at = now() WHERE id = ?;

-- name: ListDistinctAppSkillSources :many
SELECT DISTINCT source, source_ref FROM app_skills;

-- name: ListAppSkillsBySourceRef :many
SELECT * FROM app_skills WHERE source = ? AND source_ref = ?;
```

- [ ] **Step 2: 生成 + 确认** — `make sqlc-generate`；确认 `internal/store/sqlc/app_skills.sql.go` 出现 8 个方法；报告 `CreateAppSkillParams` 字段（后续 service 用）。
- [ ] **Step 3: 编译** — `go build ./internal/store/...`，PASS。
- [ ] **Step 4: 提交**：`feat(skill): 生成 app_skills 的 sqlc 查询代码`

---

## Task 2: 权限谓词 + 哨兵错误 + 平台库取归档

**Files:** Modify `internal/auth/authorizer.go`、`internal/service/errors.go`、`internal/service/platform_skill_service.go`（+test）

- [ ] **Step 1: 加 CanManageAppSkill**（TDD，参照 CanWriteAppKnowledge）：
```go
// CanManageAppSkill 判断是否可管理某 app 的 skill：与应用写权限同款（owner 本人 / 本 org 的 org_admin）。
func CanManageAppSkill(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanManageApp(p, appOrgID, appOwnerUserID)
}
```
测试覆盖：owner 本人 true、同 org org_admin true、他人 member false、platform_admin false。

- [ ] **Step 2: 加哨兵错误** — `errors.go` 末尾：
```go
// ===== 实例 skill =====
var ErrAppSkillDenied = errors.New("无权管理该实例的 skill")
var ErrAppSkillNotFound = errors.New("实例 skill 不存在")
var ErrAppSkillNameConflict = errors.New("已有同名 skill")
var ErrAppSkillProtected = errors.New("当前助手版本必需的 skill 不可删除")
var ErrAppSkillSourceUnknown = errors.New("未知的 skill 来源")
var ErrAppSkillArchiveTooDangerous = errors.New("skill 归档解压校验失败")
```

- [ ] **Step 3: PlatformSkillService 加 GetForInstall**（TDD）：
```go
// GetForInstall 取平台库 skill 指定版本的归档字节与元数据，供安装到实例。
func (s *PlatformSkillService) GetForInstall(ctx context.Context, name, version string) (archive []byte, sha string, err error) {
	row, err := s.store.GetPlatformSkillByNameVersion(ctx, sqlc.GetPlatformSkillByNameVersionParams{Name: name, Version: version})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) { return nil, "", ErrPlatformSkillNotFound }
		return nil, "", fmt.Errorf("查询平台库 skill 失败: %w", err)
	}
	rc, err := s.blobs.OpenLibrarySkill(row.TarPath)
	if err != nil { return nil, "", err }
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil { return nil, "", fmt.Errorf("读取平台库归档失败: %w", err) }
	return data, row.FileSha256, nil
}
```
测试：上传后 GetForInstall 取回字节与 sha；不存在 → ErrPlatformSkillNotFound。

- [ ] **Step 4: 测试 + 提交** — `go test ./internal/auth/... ./internal/service/ -run 'CanManageAppSkill|GetForInstall'`，PASS。提交：`feat(skill): 增加实例 skill 权限/哨兵与平台库取归档`。

---

## Task 3: AppSkillService 安装（热装 + reload，TDD）

**Files:** Create `internal/service/app_skill_service.go`、`_test.go`

安装流程：权限 → name 去重（查 app_skills + 容器对账由 List 负责，这里按 app_skills 唯一约束）→ 按 source 取归档（platform: GetForInstall / clawhub: Download）→ 解压防炸弹 + zip-slip 预校验（解压后总字节/文件数上限）→ 缓存到 LibraryBlobStore（共享前缀）→ 落 app_skills → oc-ops 热装（SkillInstall）+ reload（SkillReload）→ 审计。oc-ops 失败 → 状态留 pending（app_skills 已落，对账显示 pending，可重试）。

- [ ] **Step 1: 写失败测试**（fake stores + fake downloader + fake ocops + fake blob）：

```go
// 从 platform 来源安装：落 app_skills + 缓存 + 调 oc-ops 热装与 reload。
func TestAppSkillService_Install_Platform(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	deps.platform.put("weather", "1.0", []byte("PK\x03\x04tar")) // 预置平台库 skill
	svc := deps.service()

	res, err := svc.Install(context.Background(), deps.ownerPrincipal(), "app-1", InstallSkillInput{
		Source: "platform", SourceRef: "weather", Name: "weather", Version: "1.0",
	})
	require.NoError(t, err)
	assert.Equal(t, "weather", res.Name)
	assert.Equal(t, "active", res.Status)               // 热装+reload 成功 → active
	assert.True(t, deps.ocops.installed["weather"])      // 调了 oc-ops 热装
	assert.True(t, deps.ocops.reloaded)                  // 调了 reload
	row, _ := deps.appSkills.get("app-1", "weather")
	assert.Equal(t, "platform", row.Source)
}

// 非 owner/管理员安装被拒。
func TestAppSkillService_Install_Denied(t *testing.T) { ... require.ErrorIs(t, err, ErrAppSkillDenied) }

// 同名已装 → NameConflict。
func TestAppSkillService_Install_Duplicate(t *testing.T) { ... require.ErrorIs(t, err, ErrAppSkillNameConflict) }

// oc-ops 热装失败 → app_skills 已落、状态 pending（不报错，可重试）。
func TestAppSkillService_Install_OcOpsFail_Pending(t *testing.T) {
	deps := newAppSkillTestDeps(t); deps.ocops.installErr = errors.New("pod not ready")
	deps.platform.put("weather", "1.0", []byte("x"))
	res, err := deps.service().Install(context.Background(), deps.ownerPrincipal(), "app-1", InstallSkillInput{Source: "platform", SourceRef: "weather", Name: "weather", Version: "1.0"})
	require.NoError(t, err)
	assert.Equal(t, "pending", res.Status)
}
```

测试需要的 fakes（在 `_test.go` 内实现）：`fakeAppSkillStore`（app_skills CRUD）、`fakeAppLoader`（GetApp 返回 org/owner/version）、`fakePlatformInstall`（GetForInstall）、`fakeClawHubDownloader`（Download）、`fakeLibraryBlob`（复用 Plan 1）、`fakeOcOps`（SkillInstall/SkillDelete/SkillReload/SkillList + Resolve）。

- [ ] **Step 2: 运行确认失败** — `go test ./internal/service/ -run TestAppSkillService_Install -v`，FAIL。

- [ ] **Step 3: 实现 Install** — Create `internal/service/app_skill_service.go`：

```go
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/ocops"
)

// 依赖接口（均最小化，便于测试）
type AppSkillStore interface {
	ListAppSkillsByApp(ctx context.Context, appID string) ([]sqlc.AppSkill, error)
	GetAppSkillByAppAndName(ctx context.Context, arg sqlc.GetAppSkillByAppAndNameParams) (sqlc.AppSkill, error)
	CreateAppSkill(ctx context.Context, arg sqlc.CreateAppSkillParams) error
	DeleteAppSkillByAppAndName(ctx context.Context, arg sqlc.DeleteAppSkillByAppAndNameParams) error
	UpdateAppSkillVersion(ctx context.Context, arg sqlc.UpdateAppSkillVersionParams) error
}
type AppLocator interface { // 由 OcOpsResolver 适配：返回 org/owner/version + Endpoint
	LocateApp(ctx context.Context, appID string) (AppSkillLocation, error)
}
type AppSkillLocation struct {
	OrgID, OwnerUserID, VersionID string
	Endpoint                      ocops.Endpoint
	Supported                     bool
}
type PlatformInstaller interface { GetForInstall(ctx context.Context, name, version string) ([]byte, string, error) }
type ClawHubDownloader interface { Download(ctx context.Context, slug, version string) ([]byte, error) }
type OcOpsSkillClient interface {
	SkillInstall(ctx context.Context, ep ocops.Endpoint, name string, archive []byte) error
	SkillDelete(ctx context.Context, ep ocops.Endpoint, name string) error
	SkillReload(ctx context.Context, ep ocops.Endpoint) error
	SkillList(ctx context.Context, ep ocops.Endpoint) ([]ocops.SkillInfo, error)
}

type AppSkillService struct {
	store    AppSkillStore
	apps     AppLocator
	versions AssistantVersionLoader // 删除保护：按 versionID 取 skills_json name 集
	platform PlatformInstaller
	clawhub  ClawHubDownloader      // 可 nil
	blobs    LibraryBlobStore
	ocops    OcOpsSkillClient
	builtin  func(versionImageID string) []string // 内置清单查询（来自 config.RuntimeImageConfig.BuiltinSkills），可注入
	audit    AuditRecorder          // 复用现有审计
}

// InstallSkillInput 是安装一个 skill 的入参。
type InstallSkillInput struct{ Source, SourceRef, Name, Version string }

// AppSkillResult 是已安装列表/操作返回的单条（含对账 status）。
type AppSkillResult struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	SourceRef string `json:"source_ref"`
	Version   string `json:"version"`
	Latest    string `json:"latest_version,omitempty"`
	Status    string `json:"status"` // active|pending|builtin|self_created
	Category  string `json:"category"`
	Protected bool   `json:"protected"` // 当前版本必需，禁删
}

func (s *AppSkillService) Install(ctx context.Context, principal auth.Principal, appID string, in InstallSkillInput) (AppSkillResult, error) {
	loc, err := s.apps.LocateApp(ctx, appID)
	if err != nil { return AppSkillResult{}, err }
	if !auth.CanManageAppSkill(principal, loc.OrgID, loc.OwnerUserID) {
		return AppSkillResult{}, ErrAppSkillDenied
	}
	// name 去重（app_skills 物理唯一）
	if _, err := s.store.GetAppSkillByAppAndName(ctx, sqlc.GetAppSkillByAppAndNameParams{AppID: appID, Name: in.Name}); err == nil {
		return AppSkillResult{}, ErrAppSkillNameConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return AppSkillResult{}, fmt.Errorf("查询实例 skill 失败: %w", err)
	}
	// 取归档 + 元数据
	archive, sha, meta, ext, err := s.fetchArchive(ctx, in)
	if err != nil { return AppSkillResult{}, err }
	// 解压防炸弹 + zip-slip 预校验（解压后总字节/文件数上限）
	if err := validateArchiveSafety(archive, ext); err != nil { return AppSkillResult{}, ErrAppSkillArchiveTooDangerous }
	if sha == "" { sum := sha256.Sum256(archive); sha = hex.EncodeToString(sum[:]) }
	// 缓存到共享前缀
	rel, err := s.blobs.PutLibrarySkill(in.Source, in.SourceRef, in.Version, ext, archive)
	if err != nil { return AppSkillResult{}, err }
	// 落 app_skills
	metaJSON, _ := json.Marshal(meta)
	if err := s.store.CreateAppSkill(ctx, sqlc.CreateAppSkillParams{
		ID: newUUID(), AppID: appID, Name: in.Name, Source: in.Source, SourceRef: in.SourceRef,
		Version: in.Version, CachedTarPath: rel, SourceMetadata: metaJSON,
		FileSize: int64(len(archive)), FileSha256: sha, InstalledBy: null.StringFrom(principal.UserID),
	}); err != nil { return AppSkillResult{}, fmt.Errorf("写入实例 skill 失败: %w", err) }
	s.audit.Record(ctx, principal, "skill.install", appID, in.Name) // 审计
	// 热装 + reload（失败 → pending，不回滚 app_skills，可重试）
	status := "active"
	if loc.Supported {
		if err := s.ocops.SkillInstall(ctx, loc.Endpoint, in.Name, archive); err != nil {
			status = "pending"
		} else if err := s.ocops.SkillReload(ctx, loc.Endpoint); err != nil {
			status = "pending"
		}
	} else {
		status = "pending" // 容器未运行，等下次启动 oc-restore 恢复
	}
	return AppSkillResult{Name: in.Name, Source: in.Source, SourceRef: in.SourceRef, Version: in.Version, Status: status, Category: "manager"}, nil
}

// fetchArchive 按来源取归档字节、sha、原始元数据、扩展名。
func (s *AppSkillService) fetchArchive(ctx context.Context, in InstallSkillInput) (data []byte, sha string, meta map[string]any, ext string, err error) {
	switch in.Source {
	case "platform":
		d, sh, e := s.platform.GetForInstall(ctx, in.SourceRef, in.Version)
		return d, sh, map[string]any{}, "tar", e
	case "clawhub":
		if s.clawhub == nil { return nil, "", nil, "", ErrAppSkillSourceUnknown }
		d, e := s.clawhub.Download(ctx, in.SourceRef, in.Version)
		return d, "", map[string]any{"source": "clawhub"}, "zip", e
	default:
		return nil, "", nil, "", ErrAppSkillSourceUnknown
	}
}
```

`validateArchiveSafety(archive, ext)`、`AssistantVersionLoader`、`AuditRecorder` 在本文件或复用现有定义；`validateArchiveSafety` 解 tar/zip 统计解压后总字节与文件数，超阈值（如 200MiB / 5000 文件）返回错误（防 zip bomb，spec 决策）。

- [ ] **Step 4: 运行确认通过** — `go test ./internal/service/ -run TestAppSkillService_Install -v`，PASS（4 用例）。
- [ ] **Step 5: 提交**：`feat(skill): AppSkillService 安装（取归档→缓存→落库→oc-ops 热装+reload）`

---

## Task 4: 卸载 + 删除保护（TDD）

**Files:** Modify `internal/service/app_skill_service.go`、`_test.go`

卸载流程：权限 → 取 app_skills 行（不存在→NotFound）→ **删除保护**（查 app 当前 version 的 skills_json name 集，若 name 在其中 → ErrAppSkillProtected）→ 删 app_skills → 自创类删 S3 备份（本 plan 管理类即可，自创卸载经对账识别后同样删 app_skills 不适用——自创不在 app_skills，由对账层处理，见 Task 5）→ oc-ops 热删 + reload → 审计。

- [ ] **Step 1: 写失败测试**：
```go
// 卸载 manager 管理的 skill：删 app_skills + oc-ops 热删 + reload。
func TestAppSkillService_Uninstall_OK(t *testing.T) { ... assert.True(t, deps.ocops.deleted["mytool"]); assert.True(t, deps.ocops.reloaded) }
// 当前版本必需的 skill 禁删 → Protected。
func TestAppSkillService_Uninstall_Protected(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	deps.versions.setSkills("v1", []string{"weather"}) // 当前版本含 weather
	deps.apps.setVersion("app-1", "v1")
	deps.appSkills.put("app-1", "weather", "platform")  // 实例已装 weather
	err := deps.service().Uninstall(context.Background(), deps.ownerPrincipal(), "app-1", "weather")
	require.ErrorIs(t, err, ErrAppSkillProtected)
}
// 卸载不存在 → NotFound。
func TestAppSkillService_Uninstall_NotFound(t *testing.T) { ... }
```

- [ ] **Step 2-4: 实现 + 测试 + 提交** — 实现 `Uninstall`：

```go
func (s *AppSkillService) Uninstall(ctx context.Context, principal auth.Principal, appID, name string) error {
	loc, err := s.apps.LocateApp(ctx, appID)
	if err != nil { return err }
	if !auth.CanManageAppSkill(principal, loc.OrgID, loc.OwnerUserID) { return ErrAppSkillDenied }
	if _, err := s.store.GetAppSkillByAppAndName(ctx, sqlc.GetAppSkillByAppAndNameParams{AppID: appID, Name: name}); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return ErrAppSkillNotFound }
		return fmt.Errorf("查询实例 skill 失败: %w", err)
	}
	// 删除保护：当前版本必需的不可删
	if protected, err := s.isCurrentVersionSkill(ctx, loc.VersionID, name); err != nil {
		return err
	} else if protected { return ErrAppSkillProtected }
	if err := s.store.DeleteAppSkillByAppAndName(ctx, sqlc.DeleteAppSkillByAppAndNameParams{AppID: appID, Name: name}); err != nil {
		return fmt.Errorf("删除实例 skill 失败: %w", err)
	}
	s.audit.Record(ctx, principal, "skill.uninstall", appID, name)
	if loc.Supported {
		_ = s.ocops.SkillDelete(ctx, loc.Endpoint, name)
		_ = s.ocops.SkillReload(ctx, loc.Endpoint)
	}
	return nil
}

// isCurrentVersionSkill 判断 name 是否属于 app 当前绑定版本的 skills_json（删除保护）。
func (s *AppSkillService) isCurrentVersionSkill(ctx context.Context, versionID, name string) (bool, error) {
	if versionID == "" { return false, nil }
	names, err := s.versions.SkillNames(ctx, versionID) // 内部 GetAssistantVersion + decodeSkills
	if err != nil { return false, err }
	for _, n := range names { if n == name { return true, nil } }
	return false, nil
}
```
`AssistantVersionLoader` 接口 `SkillNames(ctx, versionID) ([]string, error)` 由现有 `AssistantVersionStore.GetAssistantVersion` + `decodeSkills` 适配。提交：`feat(skill): AppSkillService 卸载与当前版本删除保护`。

---

## Task 5: 更新 + 实时对账 List（status，TDD）

**Files:** Modify `internal/service/app_skill_service.go`、`_test.go`

- [ ] **更新 `Update(ctx, principal, appID, name, targetVersion)`**：权限 → 取归档目标版本 → 缓存 → `UpdateAppSkillVersion` → oc-ops 热替换（SkillInstall 覆盖）+ reload → 审计。TDD 覆盖 OK + 不存在。

- [ ] **实时对账 `List(ctx, principal, appID)`**：核心——容器实际 skill（ocops.SkillList）× app_skills 表 × 内置清单 → 每条 status：

```go
func (s *AppSkillService) List(ctx context.Context, principal auth.Principal, appID string) ([]AppSkillResult, error) {
	loc, err := s.apps.LocateApp(ctx, appID)
	if err != nil { return nil, err }
	if !auth.CanManageAppSkill(principal, loc.OrgID, loc.OwnerUserID) { return nil, ErrAppSkillDenied }
	rows, err := s.store.ListAppSkillsByApp(ctx, appID) // 期望
	if err != nil { return nil, err }
	expected := map[string]sqlc.AppSkill{}
	for _, r := range rows { expected[r.Name] = r }
	protectedNames, _ := s.versions.SkillNames(ctx, loc.VersionID) // 当前版本必需集
	protectedSet := toSet(protectedNames)

	// 容器实际（不可达则 fallback 仅 app_skills + unknown）
	var actual []ocops.SkillInfo
	reachable := loc.Supported
	if reachable {
		actual, err = s.ocops.SkillList(ctx, loc.Endpoint)
		if err != nil { reachable = false }
	}
	out := []AppSkillResult{}
	seenContainer := map[string]bool{}
	for _, a := range actual {
		seenContainer[a.Name] = true
		if exp, ok := expected[a.Name]; ok {
			out = append(out, managerEntry(exp, "active", protectedSet[a.Name]))
		} else if a.Builtin {
			out = append(out, AppSkillResult{Name: a.Name, Status: "builtin", Category: "hermes-builtin"})
		} else {
			out = append(out, AppSkillResult{Name: a.Name, Status: "self_created", Category: "hermes-self-created"})
		}
	}
	// app_skills 有但容器无 → pending（或容器不可达 → unknown）
	for name, exp := range expected {
		if seenContainer[name] { continue }
		st := "pending"
		if !reachable { st = "unknown" }
		out = append(out, managerEntry(exp, st, protectedSet[name]))
	}
	sortByName(out)
	return out, nil
}
```
`managerEntry` 把 app_skills 行 + status + protected 组装成 `AppSkillResult`（version、latest_version、source）。`latest_version > version` 时前端显示更新提示。

- [ ] **测试**：对账各 status（active=有×有、pending=有×无、builtin=无×有+内置、self_created=无×有+非内置）、容器不可达 fallback（unknown）、protected 标记。提交：`feat(skill): AppSkillService 更新与实时对账（status/protected）`。

---

## Task 6: SkillUpdateChecker 定时任务

**Files:** Create `internal/service/skill_update_checker.go`、`_test.go`

- [ ] `SkillUpdateChecker.Tick(ctx)`：`ListDistinctAppSkillSources` 拿去重的 (source, source_ref) → 各回源查最高版本（platform: PlatformSkillStore 查同 name 最高 version；clawhub: ClawHub ListVersions(slug) 取最高）→ 对每条匹配的 app_skills 行 `UpdateAppSkillLatest(latest, id)`。用 `NewPeriodicReconciler("skill_update_check", cfg.ClawHub.CacheTTL 或独立周期, checker.Tick)` 包装。TDD：fake stores，验证回写 latest_version。提交：`feat(skill): 增加 skill 更新检测定时任务`。

---

## Task 7: handler + 路由（TDD）

**Files:** Create `internal/api/handlers/app_skills.go`、`_test.go`

- [ ] handler（参照 knowledge app 子资源）：
```go
func RegisterAppSkillRoutes(router gin.IRouter, h *AppSkillsHandler) {
	g := router.Group("/api/v1/apps/:appId/skills")
	g.GET("", h.List)                      // 实时对账列表（带 status）
	g.POST("", h.Install)                  // body: source/source_ref/name/version
	g.DELETE("/:skillName", h.Uninstall)   // 删除保护在 service
	g.POST("/:skillName/update", h.Update) // body: version
}
```
错误映射：Denied→403、NotFound→404、NameConflict→409、Protected→403（或 409）、SourceUnknown→400、ArchiveTooDangerous→400。请求体 DTO 进 `dto.go`。测试用 stub service。提交：`feat(skill): 增加 per-app skill HTTP 接口`。

---

## Task 8: 接线 + OpenAPI 同步

**Files:** Modify `internal/api/router.go`、`cmd/server/main.go`；生成 `openapi.yaml`/`generated.ts`

- [ ] **main 接线**：构造 `AppSkillService`（注入 app_skills store=dbStore.Queries、AppLocator 适配 OcOpsResolver、AssistantVersionLoader 适配 assistant_version store、PlatformInstaller=platformSkillService、clawhub downloader=clawhubClient(可 nil)、blobs=libraryBlobs、ocops client、builtin 查询=config.RuntimeImageConfig.BuiltinSkills、audit）；`SkillUpdateChecker` + `PeriodicReconciler` 加进 errgroup；router 注册。
- [ ] **router**：Dependencies 加 `AppSkillService`，user 组注册。
- [ ] **生成 + 校验**：`make openapi-gen && make web-types-gen && make openapi-check && make test && make vet`，全过。
- [ ] 提交：`feat(skill): 接线 per-app skill service 与定时任务` + `chore(skill): 同步 per-app skill 接口契约与前端类型`。

---

## Self-Review 备注

- **Spec 覆盖**：安装/卸载/更新（热装+reload 不重启）、实时对账 status（active/pending/builtin/self_created）+ 容器不可达 fallback、当前版本删除保护、latest_version 定时检测、pending 兜底（loc.Supported=false 留 pending）、审计、共享缓存。
- **未覆盖（其它 plan）**：种子注入 syncVersionSeed（Plan 4，会复用本 plan 的 Install/缓存逻辑）、前端（Plan 6）。
- **自创 skill 卸载**：对账识别 self_created（无 app_skills 行），其卸载 = oc-ops 热删 + 删 S3 备份；本 plan handler 的 DELETE 对 self_created 走「无 app_skills 行 → 调 oc-ops SkillDelete + 删 S3」分支（实现时在 Uninstall 加 self_created 分支，或单独 handler 路径）。**实现时补充该分支并加测试**。
- **现场确认项**：OcOpsResolver→AppLocator 适配、AssistantVersionLoader.SkillNames 适配、AuditRecorder 现有签名、ocops SkillInstall multipart、RuntimeImageConfig.BuiltinSkills 注入、模块路径。
