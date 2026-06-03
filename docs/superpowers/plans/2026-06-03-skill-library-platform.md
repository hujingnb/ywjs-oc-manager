# Skill 库数据模型与平台库 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立 skill 库的两张数据表（`platform_skills`、`app_skills`），并实现「平台库」的完整后端 CRUD（平台管理员上传/列出/删除 skill tar，内容存对象存储共享前缀 `library/`）。

**Architecture:** 沿用本仓库既有「migration → sqlc → service（自带 store 接口）→ handler → router」分层。新增一个 `LibraryBlobStore`（对象存储抽象，FS + S3 两实现，key 前缀 `library/<source>/<source_ref>/<version>.<ext>`），平台库 source 固定为 `platform`、source_ref 为 skill name。本 plan 只建 `app_skills` 表（其业务逻辑属于后续 plan），平台库 CRUD 全栈完成并可独立测试交付。

**Tech Stack:** Go 1.25 / Gin / sqlc (v1.30.0) / golang-migrate / MySQL 8 / 对象存储（aws-sdk-go-v2 标准 S3）/ testify。

这是「Hermes Skill 市场与实例级 skill 管理」设计（`docs/superpowers/specs/2026-06-03-skill市场-design.md`）的 Plan 1（共 6 个 plan）。

---

## File Structure

新建：
- `internal/migrations/000006_skill_library.up.sql` / `.down.sql` — 两张表的建/删
- `internal/store/queries/platform_skills.sql` — platform_skills 的 sqlc CRUD 查询
- `internal/service/library_blob_store.go` — `LibraryBlobStore` 接口 + FS 实现
- `internal/service/s3_library_blob_store.go` — S3 实现
- `internal/service/library_blob_store_test.go` — FS blob store 测试
- `internal/service/platform_skill_service.go` — `PlatformSkillService` + `PlatformSkillResult` + store 接口
- `internal/service/platform_skill_service_test.go` — service 测试
- `internal/api/handlers/platform_skills.go` — handler + `RegisterPlatformSkillRoutes`
- `internal/api/handlers/platform_skills_test.go` — handler 测试

修改：
- `sqlc.yaml` — `schema:` 列表追加新 up 文件
- `internal/integrations/storage/keys.go` — 加 `LibrarySkillKey`
- `internal/service/errors.go` — 加平台库哨兵错误
- `internal/auth/authorizer.go` — 加 `CanManagePlatformSkill`
- `internal/api/handlers/dto.go` — 加平台库上传 DTO（multipart，无需 JSON struct，仅注释占位）
- `internal/api/router.go` — `Dependencies` 加字段 + 条件注册
- `cmd/server/main.go` — 构造 `PlatformSkillService` 填入 `Dependencies`
- `openapi/openapi.yaml` / `web/src/api/generated.ts` — 生成产物

---

## Task 1: 数据库迁移（platform_skills + app_skills 两张表）

**Files:**
- Create: `internal/migrations/000006_skill_library.up.sql`
- Create: `internal/migrations/000006_skill_library.down.sql`
- Modify: `sqlc.yaml`（`schema:` 列表追加）

- [ ] **Step 1: 写 up 迁移**

Create `internal/migrations/000006_skill_library.up.sql`：

```sql
CREATE TABLE platform_skills (
    id            CHAR(36)     NOT NULL                 COMMENT '主键 UUID',
    name          VARCHAR(128) NOT NULL                 COMMENT 'skill 名，等于容器内解压目录名',
    description   TEXT         NOT NULL                 COMMENT 'skill 描述，市场展示用',
    version       VARCHAR(64)  NOT NULL                 COMMENT '语义版本号',
    tar_path      VARCHAR(512) NOT NULL                 COMMENT '对象存储相对路径 library/platform/<name>/<version>.tar',
    file_size     BIGINT       NOT NULL                 COMMENT 'tar 字节大小',
    file_sha256   CHAR(64)     NOT NULL                 COMMENT 'tar 内容 SHA256，完整性校验',
    metadata_json JSON         NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '附加元数据（作者、标签等）',
    uploaded_by   CHAR(36)     NULL                     COMMENT '上传者 user id（平台管理员）',
    created_at    DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    PRIMARY KEY (id),
    CONSTRAINT fk_platform_skills_uploaded_by FOREIGN KEY (uploaded_by) REFERENCES users(id),
    UNIQUE KEY uk_platform_skills_name_version (name, version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='平台库 skill，平台管理员维护，多版本共存';

CREATE TABLE app_skills (
    id              CHAR(36)     NOT NULL                 COMMENT '主键 UUID',
    app_id          CHAR(36)     NOT NULL                 COMMENT '所属实例 app id',
    name            VARCHAR(128) NOT NULL                 COMMENT '解压目录名，实例内唯一，去重键',
    source          VARCHAR(32)  NOT NULL                 COMMENT '来源：platform | clawhub',
    source_ref      VARCHAR(256) NOT NULL                 COMMENT '来源内精准标识：platform=name、clawhub=slug，回源查更新用',
    version         VARCHAR(64)  NOT NULL                 COMMENT '锁定的当前安装版本',
    latest_version  VARCHAR(64)  NULL                     COMMENT '定时任务回源所得最高版本，大于 version 即有更新',
    cached_tar_path VARCHAR(512) NOT NULL                 COMMENT '对象存储缓存路径，恢复走它（确定性 + 抗下架）',
    source_metadata JSON         NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '安装时来源完整元数据快照，后台展示用（抗下架）',
    file_size       BIGINT       NOT NULL                 COMMENT '归档字节大小',
    file_sha256     CHAR(64)     NOT NULL                 COMMENT '归档内容 SHA256',
    installed_by    CHAR(36)     NULL                     COMMENT '安装者 user id',
    installed_at    DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '安装时间',
    last_checked_at DATETIME(6)  NULL                     COMMENT '定时任务上次回源检查时间',
    PRIMARY KEY (id),
    CONSTRAINT fk_app_skills_app_id FOREIGN KEY (app_id) REFERENCES apps(id),
    UNIQUE KEY uk_app_skills_app_name (app_id, name),
    KEY idx_app_skills_app (app_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='实例级 skill 安装清单，自包含快照，运行时唯一来源';
```

- [ ] **Step 2: 写 down 迁移**

Create `internal/migrations/000006_skill_library.down.sql`（与 up 逆序，先删有外键依赖的）：

```sql
DROP TABLE IF EXISTS app_skills;
DROP TABLE IF EXISTS platform_skills;
```

- [ ] **Step 3: sqlc schema 列表追加新 up 文件**

在 `sqlc.yaml` 的 `schema:` 数组末尾（最后一个现有 up 之后）追加一行：

```yaml
    - internal/migrations/000006_skill_library.up.sql
```

- [ ] **Step 4: 验证迁移文件配对测试通过**

Run: `go test ./internal/migrations/...`
Expected: PASS（`TestFS_ContainsUpAndDownPairs` 确认 000006 的 up/down 成对）。

- [ ] **Step 5: 提交**

```bash
git add internal/migrations/000006_skill_library.up.sql internal/migrations/000006_skill_library.down.sql sqlc.yaml
git commit -m "feat(skill): 增加 platform_skills 与 app_skills 数据表

新增 skill 库的两张表：platform_skills（平台库 skill，多版本共存）与
app_skills（实例级安装清单，自包含快照）。所有字段带 SQL COMMENT。"
```

---

## Task 2: 生成 sqlc 代码（platform_skills 查询）

**Files:**
- Create: `internal/store/queries/platform_skills.sql`
- Generated: `internal/store/sqlc/platform_skills.sql.go`、`models.go`、`querier.go`（由 sqlc 生成，勿手编）

- [ ] **Step 1: 写 platform_skills 查询**

Create `internal/store/queries/platform_skills.sql`：

```sql
-- name: CreatePlatformSkill :exec
INSERT INTO platform_skills (
    id, name, description, version, tar_path, file_size, file_sha256, metadata_json, uploaded_by
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetPlatformSkill :one
SELECT * FROM platform_skills WHERE id = ?;

-- name: GetPlatformSkillByNameVersion :one
SELECT * FROM platform_skills WHERE name = ? AND version = ?;

-- name: ListPlatformSkills :many
SELECT * FROM platform_skills ORDER BY name ASC, created_at DESC, id DESC;

-- name: DeletePlatformSkill :exec
DELETE FROM platform_skills WHERE id = ?;
```

- [ ] **Step 2: 生成代码**

Run: `make sqlc-generate`
Expected: 无错误；新增 `internal/store/sqlc/platform_skills.sql.go`，`internal/store/sqlc/models.go` 出现 `type PlatformSkill struct` 和 `type AppSkill struct`，`querier.go` 的 `Querier` 接口新增 5 个方法。

- [ ] **Step 3: 确认生成的方法签名**

打开 `internal/store/sqlc/platform_skills.sql.go` 确认存在：

```go
func (q *Queries) CreatePlatformSkill(ctx context.Context, arg CreatePlatformSkillParams) error
func (q *Queries) GetPlatformSkill(ctx context.Context, id string) (PlatformSkill, error)
func (q *Queries) GetPlatformSkillByNameVersion(ctx context.Context, arg GetPlatformSkillByNameVersionParams) (PlatformSkill, error)
func (q *Queries) ListPlatformSkills(ctx context.Context) ([]PlatformSkill, error)
func (q *Queries) DeletePlatformSkill(ctx context.Context, id string) error
```

`CreatePlatformSkillParams.MetadataJson` 类型为 `json.RawMessage`，`UploadedBy` 为 `null.String`（可空外键）。

- [ ] **Step 4: 编译确认**

Run: `go build ./internal/store/...`
Expected: 成功。

- [ ] **Step 5: 提交**

```bash
git add internal/store/queries/platform_skills.sql internal/store/sqlc/
git commit -m "feat(skill): 生成 platform_skills 的 sqlc 查询代码

新增 platform_skills 的 Create/Get/GetByNameVersion/List/Delete 查询，
同步生成 PlatformSkill 与 AppSkill 的 sqlc model。"
```

---

## Task 3: 对象存储 key + LibraryBlobStore（FS 实现）

**Files:**
- Modify: `internal/integrations/storage/keys.go`
- Create: `internal/service/library_blob_store.go`
- Test: `internal/service/library_blob_store_test.go`

- [ ] **Step 1: 加 storage key 函数**

在 `internal/integrations/storage/keys.go` 末尾追加：

```go
// LibrarySkillKey 返回 skill 库共享缓存对象 key：
// library/<source>/<sourceRef>/<version>.<ext>（如 library/platform/weather/1.0.tar）。
// 同一 skill 同版本被多个 app 安装时只存一份。调用方保证各段不含路径分隔符。
func LibrarySkillKey(source, sourceRef, version, ext string) string {
	return path.Join("library", source, sourceRef, version+"."+ext)
}
```

- [ ] **Step 2: 写 LibraryBlobStore 接口 + FS 实现的失败测试**

Create `internal/service/library_blob_store_test.go`：

```go
package service

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FS 实现的存取删往返：Put 后 Open 能读回原字节，Delete 后 Open 报错。
func TestFSLibraryBlobStore_RoundTrip(t *testing.T) {
	store := NewFSLibraryBlobStore(t.TempDir())
	data := []byte("hello-skill-tar")

	rel, err := store.PutLibrarySkill("platform", "weather", "1.0", "tar", data)
	require.NoError(t, err)
	assert.Equal(t, "library/platform/weather/1.0.tar", rel) // 相对路径以 / 分隔

	rc, err := store.OpenLibrarySkill(rel)
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, data, got)

	require.NoError(t, store.DeleteLibrarySkill(rel))
	_, err = store.OpenLibrarySkill(rel)
	require.Error(t, err) // 删后不可读
}

// 非法路径段（含分隔符 / 上跳）被拒绝，防止写出根目录。
func TestFSLibraryBlobStore_RejectsUnsafeSegment(t *testing.T) {
	store := NewFSLibraryBlobStore(t.TempDir())
	_, err := store.PutLibrarySkill("platform", "../escape", "1.0", "tar", []byte("x"))
	require.Error(t, err)
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/service/ -run TestFSLibraryBlobStore -v`
Expected: FAIL（`NewFSLibraryBlobStore` undefined）。

- [ ] **Step 4: 实现接口 + FS**

Create `internal/service/library_blob_store.go`：

```go
package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hujing/oc-manager/internal/integrations/storage"
)

// LibraryBlobStore 是 skill 库内容（tar/zip 归档）的对象存储抽象。
// 平台库与已缓存的公共库 skill 都经它读写，key 走 library/ 共享前缀。
type LibraryBlobStore interface {
	// PutLibrarySkill 写入归档，返回以 / 分隔的相对 key。
	PutLibrarySkill(source, sourceRef, version, ext string, data []byte) (relPath string, err error)
	// DeleteLibrarySkill 按相对 key 删除归档。
	DeleteLibrarySkill(relPath string) error
	// OpenLibrarySkill 按相对 key 打开归档供读取。
	OpenLibrarySkill(relPath string) (io.ReadCloser, error)
}

// FSLibraryBlobStore 把归档落本地文件系统，供本地开发使用。
type FSLibraryBlobStore struct{ root string }

// NewFSLibraryBlobStore 以 root 为根目录构造 FS 实现。
func NewFSLibraryBlobStore(root string) *FSLibraryBlobStore { return &FSLibraryBlobStore{root: root} }

// librarySafeSegment 校验单个路径段合法：非空、非 . / ..、不含分隔符。
func librarySafeSegment(seg string) error {
	if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, `/\`) {
		return fmt.Errorf("%w: 非法路径段 %q", ErrPlatformSkillInvalid, seg)
	}
	return nil
}

func (s *FSLibraryBlobStore) PutLibrarySkill(source, sourceRef, version, ext string, data []byte) (string, error) {
	for _, seg := range []string{source, sourceRef, version, ext} {
		if err := librarySafeSegment(seg); err != nil {
			return "", err
		}
	}
	rel := storage.LibrarySkillKey(source, sourceRef, version, ext)
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("创建 skill 库目录失败: %w", err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return "", fmt.Errorf("写入 skill 归档失败: %w", err)
	}
	return rel, nil
}

func (s *FSLibraryBlobStore) DeleteLibrarySkill(relPath string) error {
	abs := filepath.Join(s.root, filepath.FromSlash(relPath))
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除 skill 归档失败: %w", err)
	}
	return nil
}

func (s *FSLibraryBlobStore) OpenLibrarySkill(relPath string) (io.ReadCloser, error) {
	abs := filepath.Join(s.root, filepath.FromSlash(relPath))
	f, err := os.Open(abs)
	if err != nil {
		return nil, fmt.Errorf("打开 skill 归档失败: %w", err)
	}
	return f, nil
}

var _ LibraryBlobStore = (*FSLibraryBlobStore)(nil)
```

> 注：`ErrPlatformSkillInvalid` 在 Task 4 定义；本 task 与 Task 4 一起编译。若 TDD 严格按序，先在 `errors.go` 加 `ErrPlatformSkillInvalid`（见 Task 4 Step 1）再跑本测试。模块路径 `github.com/hujing/oc-manager` 以 `go.mod` 第一行为准——实现前先 `head -1 go.mod` 确认并替换 import 前缀。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/service/ -run TestFSLibraryBlobStore -v`
Expected: PASS（两个用例）。

- [ ] **Step 6: 提交**

```bash
git add internal/integrations/storage/keys.go internal/service/library_blob_store.go internal/service/library_blob_store_test.go
git commit -m "feat(skill): 增加 skill 库对象存储抽象 LibraryBlobStore

新增 library/<source>/<source_ref>/<version>.<ext> 共享前缀 key 与 FS
实现，含路径段安全校验；供平台库与公共库缓存共用。"
```

---

## Task 4: 哨兵错误 + 权限谓词

**Files:**
- Modify: `internal/service/errors.go`
- Modify: `internal/auth/authorizer.go`
- Test: `internal/auth/authorizer_test.go`（若存在则追加，否则在 service 测试覆盖）

- [ ] **Step 1: 加平台库哨兵错误**

在 `internal/service/errors.go` 末尾按现有分段风格追加：

```go
// ===== 平台库 skill =====

// ErrPlatformSkillNotFound 表示按 id 找不到平台库 skill。
var ErrPlatformSkillNotFound = errors.New("平台库 skill 不存在")

// ErrPlatformSkillDenied 表示当前主体无权管理平台库 skill。
var ErrPlatformSkillDenied = errors.New("无权管理平台库 skill")

// ErrPlatformSkillInvalid 表示上传入参非法（name/version/内容为空或路径段非法）。
var ErrPlatformSkillInvalid = errors.New("平台库 skill 入参非法")

// ErrPlatformSkillNameVersionTaken 表示同名同版本的平台库 skill 已存在。
var ErrPlatformSkillNameVersionTaken = errors.New("同名同版本的平台库 skill 已存在")
```

- [ ] **Step 2: 写权限谓词失败测试**

在 `internal/auth/authorizer_test.go` 追加（若文件不存在则创建，`package auth`，复用现有 principal 构造方式）：

```go
// 仅平台管理员可管理平台库 skill；org_admin / org_member 一律拒绝。
func TestCanManagePlatformSkill(t *testing.T) {
	assert.True(t, CanManagePlatformSkill(Principal{Role: domain.UserRolePlatformAdmin}))
	assert.False(t, CanManagePlatformSkill(Principal{Role: domain.UserRoleOrgAdmin}))
	assert.False(t, CanManagePlatformSkill(Principal{Role: domain.UserRoleOrgMember}))
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/auth/ -run TestCanManagePlatformSkill -v`
Expected: FAIL（`CanManagePlatformSkill` undefined）。

- [ ] **Step 4: 实现权限谓词**

在 `internal/auth/authorizer.go` 按现有风格追加：

```go
// CanManagePlatformSkill 判断是否可上传/删除平台库 skill：仅平台管理员。
func CanManagePlatformSkill(p Principal) bool {
	return p.Role == domain.UserRolePlatformAdmin
}
```

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/auth/ -run TestCanManagePlatformSkill -v`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/service/errors.go internal/auth/authorizer.go internal/auth/authorizer_test.go
git commit -m "feat(skill): 增加平台库哨兵错误与 CanManagePlatformSkill 权限谓词"
```

---

## Task 5: PlatformSkillService（List / Upload / Delete）

**Files:**
- Create: `internal/service/platform_skill_service.go`
- Test: `internal/service/platform_skill_service_test.go`

- [ ] **Step 1: 写 service 失败测试**

Create `internal/service/platform_skill_service_test.go`：

```go
package service

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hujing/oc-manager/internal/auth"
	"github.com/hujing/oc-manager/internal/domain"
	"github.com/hujing/oc-manager/internal/store/sqlc"
)

// fakePlatformSkillStore 是 PlatformSkillStore 的内存实现，供 service 单测使用。
type fakePlatformSkillStore struct {
	rows      map[string]sqlc.PlatformSkill // id -> row
	byNameVer map[string]sqlc.PlatformSkill // name|version -> row
	createErr error
}

func newFakePlatformSkillStore() *fakePlatformSkillStore {
	return &fakePlatformSkillStore{rows: map[string]sqlc.PlatformSkill{}, byNameVer: map[string]sqlc.PlatformSkill{}}
}

func (f *fakePlatformSkillStore) CreatePlatformSkill(_ context.Context, arg sqlc.CreatePlatformSkillParams) error {
	if f.createErr != nil {
		return f.createErr
	}
	row := sqlc.PlatformSkill{
		ID: arg.ID, Name: arg.Name, Description: arg.Description, Version: arg.Version,
		TarPath: arg.TarPath, FileSize: arg.FileSize, FileSha256: arg.FileSha256,
		MetadataJson: arg.MetadataJson, UploadedBy: arg.UploadedBy,
	}
	f.rows[arg.ID] = row
	f.byNameVer[arg.Name+"|"+arg.Version] = row
	return nil
}

func (f *fakePlatformSkillStore) GetPlatformSkill(_ context.Context, id string) (sqlc.PlatformSkill, error) {
	r, ok := f.rows[id]
	if !ok {
		return sqlc.PlatformSkill{}, sql.ErrNoRows
	}
	return r, nil
}

func (f *fakePlatformSkillStore) GetPlatformSkillByNameVersion(_ context.Context, arg sqlc.GetPlatformSkillByNameVersionParams) (sqlc.PlatformSkill, error) {
	r, ok := f.byNameVer[arg.Name+"|"+arg.Version]
	if !ok {
		return sqlc.PlatformSkill{}, sql.ErrNoRows
	}
	return r, nil
}

func (f *fakePlatformSkillStore) ListPlatformSkills(_ context.Context) ([]sqlc.PlatformSkill, error) {
	out := make([]sqlc.PlatformSkill, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakePlatformSkillStore) DeletePlatformSkill(_ context.Context, id string) error {
	delete(f.rows, id)
	return nil
}

// fakeLibraryBlob 记录 Put/Delete 调用，Put 返回确定性相对路径。
type fakeLibraryBlob struct{ deleted []string }

func (f *fakeLibraryBlob) PutLibrarySkill(source, ref, version, ext string, _ []byte) (string, error) {
	return "library/" + source + "/" + ref + "/" + version + "." + ext, nil
}
func (f *fakeLibraryBlob) DeleteLibrarySkill(rel string) error { f.deleted = append(f.deleted, rel); return nil }
func (f *fakeLibraryBlob) OpenLibrarySkill(string) (io.ReadCloser, error) { return nil, nil }

func platformPrincipal() auth.Principal { return auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin} }
func orgMemberPrincipal() auth.Principal { return auth.Principal{UserID: "u-mem", Role: domain.UserRoleOrgMember} }

// 上传成功：落库 + 写 blob，返回 Result 含正确 name/version/size/sha256。
func TestPlatformSkillService_Upload_OK(t *testing.T) {
	store := newFakePlatformSkillStore()
	blob := &fakeLibraryBlob{}
	svc := NewPlatformSkillService(store, blob)
	data := []byte("skill-archive-bytes")

	res, err := svc.Upload(context.Background(), platformPrincipal(), PlatformSkillUploadInput{
		Name: "weather", Version: "1.0", Description: "天气", Data: data,
	})
	require.NoError(t, err)
	assert.Equal(t, "weather", res.Name)
	assert.Equal(t, "1.0", res.Version)
	assert.EqualValues(t, len(data), res.FileSize)
	assert.Len(t, res.FileSha256, 64) // hex 编码的 sha256
	assert.Equal(t, "library/platform/weather/1.0.tar", store.rows[res.ID].TarPath)
}

// 非平台管理员上传被拒。
func TestPlatformSkillService_Upload_Denied(t *testing.T) {
	svc := NewPlatformSkillService(newFakePlatformSkillStore(), &fakeLibraryBlob{})
	_, err := svc.Upload(context.Background(), orgMemberPrincipal(), PlatformSkillUploadInput{Name: "x", Version: "1", Data: []byte("a")})
	require.ErrorIs(t, err, ErrPlatformSkillDenied)
}

// name / version / data 任一为空 → Invalid。
func TestPlatformSkillService_Upload_Invalid(t *testing.T) {
	svc := NewPlatformSkillService(newFakePlatformSkillStore(), &fakeLibraryBlob{})
	_, err := svc.Upload(context.Background(), platformPrincipal(), PlatformSkillUploadInput{Name: "", Version: "1", Data: []byte("a")})
	require.ErrorIs(t, err, ErrPlatformSkillInvalid)
}

// 同名同版本已存在 → NameVersionTaken。
func TestPlatformSkillService_Upload_Duplicate(t *testing.T) {
	store := newFakePlatformSkillStore()
	svc := NewPlatformSkillService(store, &fakeLibraryBlob{})
	in := PlatformSkillUploadInput{Name: "weather", Version: "1.0", Data: []byte("a")}
	_, err := svc.Upload(context.Background(), platformPrincipal(), in)
	require.NoError(t, err)
	_, err = svc.Upload(context.Background(), platformPrincipal(), in)
	require.ErrorIs(t, err, ErrPlatformSkillNameVersionTaken)
}

// 删除成功：移除行并删除对应 blob。
func TestPlatformSkillService_Delete_OK(t *testing.T) {
	store := newFakePlatformSkillStore()
	blob := &fakeLibraryBlob{}
	svc := NewPlatformSkillService(store, blob)
	res, err := svc.Upload(context.Background(), platformPrincipal(), PlatformSkillUploadInput{Name: "w", Version: "1", Data: []byte("a")})
	require.NoError(t, err)

	require.NoError(t, svc.Delete(context.Background(), platformPrincipal(), res.ID))
	_, ok := store.rows[res.ID]
	assert.False(t, ok)
	assert.Equal(t, []string{"library/platform/w/1.tar"}, blob.deleted)
}

// 删除不存在的 id → NotFound。
func TestPlatformSkillService_Delete_NotFound(t *testing.T) {
	svc := NewPlatformSkillService(newFakePlatformSkillStore(), &fakeLibraryBlob{})
	err := svc.Delete(context.Background(), platformPrincipal(), "missing")
	require.ErrorIs(t, err, ErrPlatformSkillNotFound)
}
```

（顶部 import 需加 `"io"`。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/service/ -run TestPlatformSkillService -v`
Expected: FAIL（`NewPlatformSkillService`/`PlatformSkillUploadInput` undefined）。

- [ ] **Step 3: 实现 service**

Create `internal/service/platform_skill_service.go`：

```go
package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/guregu/null/v5"

	"github.com/hujing/oc-manager/internal/auth"
	"github.com/hujing/oc-manager/internal/store/sqlc"
)

// PlatformSkillStore 是 PlatformSkillService 所需的最小数据访问能力。
type PlatformSkillStore interface {
	CreatePlatformSkill(ctx context.Context, arg sqlc.CreatePlatformSkillParams) error
	GetPlatformSkill(ctx context.Context, id string) (sqlc.PlatformSkill, error)
	GetPlatformSkillByNameVersion(ctx context.Context, arg sqlc.GetPlatformSkillByNameVersionParams) (sqlc.PlatformSkill, error)
	ListPlatformSkills(ctx context.Context) ([]sqlc.PlatformSkill, error)
	DeletePlatformSkill(ctx context.Context, id string) error
}

// PlatformSkillService 管理平台库 skill（平台管理员上传/列出/删除）。
type PlatformSkillService struct {
	store PlatformSkillStore
	blobs LibraryBlobStore
}

// NewPlatformSkillService 构造平台库 service。
func NewPlatformSkillService(store PlatformSkillStore, blobs LibraryBlobStore) *PlatformSkillService {
	return &PlatformSkillService{store: store, blobs: blobs}
}

// PlatformSkillUploadInput 是上传平台库 skill 的入参（归档原始字节）。
type PlatformSkillUploadInput struct {
	Name        string
	Version     string
	Description string
	Data        []byte
}

// PlatformSkillResult 是平台库 skill 的对外视图。
type PlatformSkillResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	FileSize    int64  `json:"file_size"`
	FileSha256  string `json:"file_sha256"`
}

func toPlatformSkillResult(row sqlc.PlatformSkill) PlatformSkillResult {
	return PlatformSkillResult{
		ID: row.ID, Name: row.Name, Description: row.Description, Version: row.Version,
		FileSize: row.FileSize, FileSha256: row.FileSha256,
	}
}

// List 返回全部平台库 skill（按 name、创建时间排序）。
func (s *PlatformSkillService) List(ctx context.Context, principal auth.Principal) ([]PlatformSkillResult, error) {
	if !auth.CanManagePlatformSkill(principal) {
		return nil, ErrPlatformSkillDenied
	}
	rows, err := s.store.ListPlatformSkills(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询平台库 skill 失败: %w", err)
	}
	out := make([]PlatformSkillResult, 0, len(rows))
	for _, r := range rows {
		out = append(out, toPlatformSkillResult(r))
	}
	return out, nil
}

// Upload 上传一个平台库 skill 归档：校验入参 → 查重 → 写对象存储 → 落库 → 读回。
func (s *PlatformSkillService) Upload(ctx context.Context, principal auth.Principal, in PlatformSkillUploadInput) (PlatformSkillResult, error) {
	if !auth.CanManagePlatformSkill(principal) {
		return PlatformSkillResult{}, ErrPlatformSkillDenied
	}
	name := strings.TrimSpace(in.Name)
	version := strings.TrimSpace(in.Version)
	if name == "" || version == "" || len(in.Data) == 0 {
		return PlatformSkillResult{}, fmt.Errorf("%w: name/version/内容不能为空", ErrPlatformSkillInvalid)
	}
	// 查重：同名同版本不可重复上传（与 UNIQUE(name, version) 一致）。
	if _, err := s.store.GetPlatformSkillByNameVersion(ctx, sqlc.GetPlatformSkillByNameVersionParams{Name: name, Version: version}); err == nil {
		return PlatformSkillResult{}, ErrPlatformSkillNameVersionTaken
	} else if !errors.Is(err, sql.ErrNoRows) {
		return PlatformSkillResult{}, fmt.Errorf("查询同名版本失败: %w", err)
	}
	sum := sha256.Sum256(in.Data)
	sha := hex.EncodeToString(sum[:])
	relPath, err := s.blobs.PutLibrarySkill("platform", name, version, "tar", in.Data)
	if err != nil {
		return PlatformSkillResult{}, err // librarySafeSegment 已包 ErrPlatformSkillInvalid
	}
	id := newUUID()
	if err := s.store.CreatePlatformSkill(ctx, sqlc.CreatePlatformSkillParams{
		ID: id, Name: name, Description: strings.TrimSpace(in.Description), Version: version,
		TarPath: relPath, FileSize: int64(len(in.Data)), FileSha256: sha,
		MetadataJson: json.RawMessage("{}"), UploadedBy: null.StringFrom(principal.UserID),
	}); err != nil {
		// 落库失败回滚已写归档，避免孤儿对象。
		_ = s.blobs.DeleteLibrarySkill(relPath)
		return PlatformSkillResult{}, fmt.Errorf("写入平台库 skill 失败: %w", err)
	}
	row, err := s.store.GetPlatformSkill(ctx, id)
	if err != nil {
		return PlatformSkillResult{}, fmt.Errorf("读回平台库 skill 失败: %w", err)
	}
	return toPlatformSkillResult(row), nil
}

// Delete 删除一个平台库 skill：先确认存在，再删行与对象。
func (s *PlatformSkillService) Delete(ctx context.Context, principal auth.Principal, id string) error {
	if !auth.CanManagePlatformSkill(principal) {
		return ErrPlatformSkillDenied
	}
	row, err := s.store.GetPlatformSkill(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPlatformSkillNotFound
		}
		return fmt.Errorf("查询平台库 skill 失败: %w", err)
	}
	if err := s.store.DeletePlatformSkill(ctx, id); err != nil {
		return fmt.Errorf("删除平台库 skill 失败: %w", err)
	}
	if err := s.blobs.DeleteLibrarySkill(row.TarPath); err != nil {
		return fmt.Errorf("删除平台库 skill 归档失败: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/service/ -run TestPlatformSkillService -v`
Expected: PASS（全部 6 个用例）。

- [ ] **Step 5: 提交**

```bash
git add internal/service/platform_skill_service.go internal/service/platform_skill_service_test.go
git commit -m "feat(skill): 增加 PlatformSkillService 平台库增删查

上传计算 sha256、写对象存储共享前缀、查重落库、落库失败回滚归档；
删除先确认存在再删行与对象。带权限/非法入参/重复/未找到全场景单测。"
```

---

## Task 6: HTTP handler + 路由注册

**Files:**
- Modify: `internal/api/handlers/dto.go`（仅加分段注释，multipart 无 JSON struct）
- Create: `internal/api/handlers/platform_skills.go`
- Test: `internal/api/handlers/platform_skills_test.go`

- [ ] **Step 1: 写 handler 失败测试**

Create `internal/api/handlers/platform_skills_test.go`：

```go
package handlers

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hujing/oc-manager/internal/auth"
	"github.com/hujing/oc-manager/internal/domain"
	"github.com/hujing/oc-manager/internal/service"
)

// platformSkillServiceStub 实现 handler 依赖的接口，用于隔离 HTTP 层测试。
type platformSkillServiceStub struct {
	uploadRes service.PlatformSkillResult
	uploadErr error
	listRes   []service.PlatformSkillResult
	deleteErr error
	gotUpload service.PlatformSkillUploadInput
}

func (s *platformSkillServiceStub) List(context.Context, auth.Principal) ([]service.PlatformSkillResult, error) {
	return s.listRes, nil
}
func (s *platformSkillServiceStub) Upload(_ context.Context, _ auth.Principal, in service.PlatformSkillUploadInput) (service.PlatformSkillResult, error) {
	s.gotUpload = in
	return s.uploadRes, s.uploadErr
}
func (s *platformSkillServiceStub) Delete(context.Context, auth.Principal, string) error {
	return s.deleteErr
}

func platformAdminReq(req *http.Request) *http.Request {
	return withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
}

// POST multipart 上传：handler 解析 name/version/file 并回传 201 + service 结果。
func TestPlatformSkillsHandler_Upload(t *testing.T) {
	stub := &platformSkillServiceStub{uploadRes: service.PlatformSkillResult{ID: "ps1", Name: "weather", Version: "1.0"}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterPlatformSkillRoutes(r, NewPlatformSkillsHandler(stub))

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("name", "weather")
	_ = w.WriteField("version", "1.0")
	fw, _ := w.CreateFormFile("file", "weather.tar")
	_, _ = fw.Write([]byte("archive"))
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/platform-skills", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, platformAdminReq(req))

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "weather", stub.gotUpload.Name)
	assert.Equal(t, "1.0", stub.gotUpload.Version)
	assert.Equal(t, []byte("archive"), stub.gotUpload.Data)
}

// DELETE 不存在 → 404。
func TestPlatformSkillsHandler_Delete_NotFound(t *testing.T) {
	stub := &platformSkillServiceStub{deleteErr: service.ErrPlatformSkillNotFound}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterPlatformSkillRoutes(r, NewPlatformSkillsHandler(stub))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/platform-skills/ps1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, platformAdminReq(req))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
```

> `withPrincipal` 是 handlers 包内既有测试 helper（见 `assistant_versions_test.go`）。若签名不同，按其实际写法调用。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/handlers/ -run TestPlatformSkillsHandler -v`
Expected: FAIL（`NewPlatformSkillsHandler` undefined）。

- [ ] **Step 3: 加 dto.go 分段注释**

在 `internal/api/handlers/dto.go` 末尾加一行注释（平台库上传走 multipart，无 JSON 请求体）：

```go
// ===== 平台库 skill =====
// 上传走 multipart/form-data（字段 name/version/description + file），无 JSON 请求体 DTO。
```

- [ ] **Step 4: 实现 handler**

Create `internal/api/handlers/platform_skills.go`：

```go
package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/hujing/oc-manager/internal/api/apierror"
	"github.com/hujing/oc-manager/internal/service"
)

// platformSkillService 是 handler 依赖的平台库能力（包内接口，便于测试桩）。
type platformSkillService interface {
	List(ctx context.Context, principal auth.Principal) ([]service.PlatformSkillResult, error)
	Upload(ctx context.Context, principal auth.Principal, in service.PlatformSkillUploadInput) (service.PlatformSkillResult, error)
	Delete(ctx context.Context, principal auth.Principal, id string) error
}

// PlatformSkillsHandler 暴露平台库 skill 的 HTTP 接口。
type PlatformSkillsHandler struct{ service platformSkillService }

// NewPlatformSkillsHandler 构造 handler。
func NewPlatformSkillsHandler(svc platformSkillService) *PlatformSkillsHandler {
	return &PlatformSkillsHandler{service: svc}
}

// RegisterPlatformSkillRoutes 注册平台库路由（仅平台管理员可用，权限在 service 层判定）。
func RegisterPlatformSkillRoutes(router gin.IRouter, h *PlatformSkillsHandler) {
	router.GET("/api/v1/platform-skills", h.List)
	router.POST("/api/v1/platform-skills", h.Upload)
	router.DELETE("/api/v1/platform-skills/:id", h.Delete)
}

// List 列出平台库 skill。
//
// @Summary  列出平台库 skill
// @Tags     platform-skills
// @Produce  json
// @Security BearerAuth
// @Success  200 {object} map[string][]service.PlatformSkillResult
// @Failure  403 {object} ErrorResponse
// @Router   /platform-skills [get]
func (h *PlatformSkillsHandler) List(c *gin.Context) {
	out, err := h.service.List(c.Request.Context(), principalFromCtx(c))
	if err != nil {
		writePlatformSkillError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"skills": out})
}

// Upload 上传平台库 skill（multipart：name/version/description + file）。
//
// @Summary  上传平台库 skill
// @Tags     platform-skills
// @Accept   multipart/form-data
// @Produce  json
// @Security BearerAuth
// @Param    name        formData string true  "skill 名"
// @Param    version     formData string true  "版本"
// @Param    description formData string false "描述"
// @Param    file        formData file   true  "skill tar 归档"
// @Success  201 {object} map[string]service.PlatformSkillResult
// @Failure  400 {object} ErrorResponse
// @Router   /platform-skills [post]
func (h *PlatformSkillsHandler) Upload(c *gin.Context) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "缺少 file 字段"))
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "读取上传文件失败"))
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "读取上传内容失败"))
		return
	}
	out, err := h.service.Upload(c.Request.Context(), principalFromCtx(c), service.PlatformSkillUploadInput{
		Name: c.PostForm("name"), Version: c.PostForm("version"), Description: c.PostForm("description"), Data: data,
	})
	if err != nil {
		writePlatformSkillError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"skill": out})
}

// Delete 删除平台库 skill。
//
// @Summary  删除平台库 skill
// @Tags     platform-skills
// @Produce  json
// @Security BearerAuth
// @Param    id path string true "skill id"
// @Success  204
// @Failure  404 {object} ErrorResponse
// @Router   /platform-skills/{id} [delete]
func (h *PlatformSkillsHandler) Delete(c *gin.Context) {
	if err := h.service.Delete(c.Request.Context(), principalFromCtx(c), c.Param("id")); err != nil {
		writePlatformSkillError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// writePlatformSkillError 把平台库哨兵错误映射为 HTTP 状态码 + 错误体。
func writePlatformSkillError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrPlatformSkillDenied):
		c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", err.Error()))
	case errors.Is(err, service.ErrPlatformSkillNotFound):
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", err.Error()))
	case errors.Is(err, service.ErrPlatformSkillNameVersionTaken):
		c.JSON(http.StatusConflict, apierror.New("CONFLICT", err.Error()))
	case errors.Is(err, service.ErrPlatformSkillInvalid):
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", err.Error()))
	default:
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "服务器内部错误"))
	}
}
```

> import 里的 `auth` 包路径与 handlers 包其它文件保持一致（确认 `assistant_versions.go` 的 auth import 写法后照抄）。`apierror.New` 的参数顺序与现有用法对齐（确认 `apierror` 包签名）。

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/api/handlers/ -run TestPlatformSkillsHandler -v`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/api/handlers/platform_skills.go internal/api/handlers/platform_skills_test.go internal/api/handlers/dto.go
git commit -m "feat(skill): 增加平台库 HTTP handler 与路由

GET/POST(multipart)/DELETE /api/v1/platform-skills，哨兵错误映射到
对应 HTTP 状态码；带上传与未找到的 handler 测试。"
```

---

## Task 7: 依赖接线（router + main）

**Files:**
- Modify: `internal/api/router.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: router 加字段 + 条件注册**

在 `internal/api/router.go` 的 `Dependencies` struct 加字段：

```go
	PlatformSkillService *service.PlatformSkillService
```

在 `NewRouter` 内 `user` 组注册区（与 `RegisterAssistantVersionRoutes` 相邻）加：

```go
	if dep.PlatformSkillService != nil {
		handlers.RegisterPlatformSkillRoutes(user, handlers.NewPlatformSkillsHandler(dep.PlatformSkillService))
	}
```

- [ ] **Step 2: main.go 构造 service 并填入**

在 `cmd/server/main.go` 构造其它 service 处（参照 `AssistantVersionService` 的构造），按当前部署用 FS 还是 S3 blob store 选择实现。读现有代码确认 skill blob store 是怎么按配置选 FS/S3 的，照同样分支构造 `LibraryBlobStore`：

```go
	// libraryBlobs 按配置选 FS（本地）或 S3（生产），与现有 SkillBlobStore 选择逻辑一致。
	var libraryBlobs service.LibraryBlobStore
	if cfg.Storage.S3Enabled() { // 占位条件：替换为现有 skill blob store 实际用的判定
		libraryBlobs = service.NewS3LibraryBlobStore(objectStore /* 与现有 S3SkillBlobStore 相同的 objects 依赖 */)
	} else {
		libraryBlobs = service.NewFSLibraryBlobStore(cfg.Storage.LocalRoot /* 与现有 FS skill 根一致 */)
	}
	platformSkillService := service.NewPlatformSkillService(dbStore.PlatformSkillStoreAdapter(), libraryBlobs)
```

> `dbStore` 暴露 `*sqlc.Queries`，而 `PlatformSkillService` 需要 `PlatformSkillStore` 接口——`*sqlc.Queries` 已实现该接口的 5 个方法（方法名/签名完全一致），可直接传 `dbStore.Queries`。无需写 adapter，上面的 `PlatformSkillStoreAdapter()` 替换为 `dbStore.Queries`。

修正后：

```go
	platformSkillService := service.NewPlatformSkillService(dbStore.Queries, libraryBlobs)
```

填入 `Dependencies`：

```go
		PlatformSkillService: platformSkillService,
```

- [ ] **Step 3: 编译**

Run: `go build ./...`
Expected: 成功。若 `S3LibraryBlobStore` 未实现导致失败，先做 Step 4。

- [ ] **Step 4: 实现 S3LibraryBlobStore（对齐现有 S3SkillBlobStore）**

Create `internal/service/s3_library_blob_store.go`，参照 `internal/service/s3_skill_blob_store.go` 的依赖与写法：

```go
package service

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/hujing/oc-manager/internal/integrations/storage"
)

// S3LibraryBlobStore 把 skill 库归档存到 S3 兼容对象存储。
type S3LibraryBlobStore struct{ objects storage.ObjectStore }

// NewS3LibraryBlobStore 构造 S3 实现（objects 与现有 S3SkillBlobStore 同源）。
func NewS3LibraryBlobStore(objects storage.ObjectStore) *S3LibraryBlobStore {
	return &S3LibraryBlobStore{objects: objects}
}

func (s *S3LibraryBlobStore) PutLibrarySkill(source, sourceRef, version, ext string, data []byte) (string, error) {
	key := storage.LibrarySkillKey(source, sourceRef, version, ext)
	if err := s.objects.PutObject(context.Background(), key, bytes.NewReader(data), int64(len(data))); err != nil {
		return "", fmt.Errorf("上传 skill 归档失败: %w", err)
	}
	return key, nil
}

func (s *S3LibraryBlobStore) DeleteLibrarySkill(relPath string) error {
	if err := s.objects.DeleteObject(context.Background(), relPath); err != nil {
		return fmt.Errorf("删除 skill 归档失败: %w", err)
	}
	return nil
}

func (s *S3LibraryBlobStore) OpenLibrarySkill(relPath string) (io.ReadCloser, error) {
	rc, err := s.objects.GetObject(context.Background(), relPath)
	if err != nil {
		return nil, fmt.Errorf("打开 skill 归档失败: %w", err)
	}
	return rc, nil
}

var _ LibraryBlobStore = (*S3LibraryBlobStore)(nil)
```

> `storage.ObjectStore` 的方法名/签名以 `internal/integrations/storage/store.go` 实际定义为准（`PutObject`/`GetObject`/`DeleteObject` 参数可能不同，照抄 `s3_skill_blob_store.go` 的调用方式）。

- [ ] **Step 5: 编译 + 全量测试**

Run: `go build ./... && go test ./internal/... && go vet ./...`
Expected: 全部成功。

- [ ] **Step 6: 提交**

```bash
git add internal/api/router.go cmd/server/main.go internal/service/s3_library_blob_store.go
git commit -m "feat(skill): 接线平台库 service 到 router 与 server 启动

Dependencies 加 PlatformSkillService 字段并条件注册路由；main 按配置
选 FS/S3 构造 LibraryBlobStore；补 S3LibraryBlobStore 实现。"
```

---

## Task 8: OpenAPI / 前端类型同步 + 收尾校验

**Files:**
- Generated: `openapi/openapi.yaml`、`web/src/api/generated.ts`

- [ ] **Step 1: 生成 OpenAPI 与前端类型**

Run: `make openapi-gen && make web-types-gen`
Expected: `openapi/openapi.yaml` 出现 `/platform-skills` 三个路由与 `service.PlatformSkillResult` schema；`web/src/api/generated.ts` 同步更新。

- [ ] **Step 2: 校验生成产物与代码同步**

Run: `make openapi-check`
Expected: 退出码 0（git 工作区 openapi.yaml 干净，说明已跟随代码更新）。

- [ ] **Step 3: 全量测试 + 静态检查**

Run: `make test && make vet`
Expected: 全部 PASS。

- [ ] **Step 4: 本地迁移冒烟（k3d 在跑时）**

Run: `go run ./cmd/migrate up`（读 `config/manager.yaml` 的 database.url；或按本地实际用 `make migrate-up`）
Expected: 000006 应用成功，无报错。可在 DB 确认 `platform_skills`、`app_skills` 两表存在。

- [ ] **Step 5: 提交生成产物**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(skill): 同步平台库接口的 OpenAPI 契约与前端类型"
```

---

## Self-Review 备注（写计划时已核对）

- **Spec 覆盖**：本 plan 对应 spec「数据模型（platform_skills/app_skills）」「平台库 CRUD」「LibraryBlobStore 共享前缀」「CanManagePlatformSkill 权限」「建表全字段 COMMENT」；其余（市场聚合、per-app 装卸、种子注入、容器侧、前端）属 Plan 2–6。
- **类型一致**：`PlatformSkillUploadInput`/`PlatformSkillResult`/`PlatformSkillStore`/`LibraryBlobStore` 在 service、handler stub、main 接线中签名一致；sqlc 生成的 `CreatePlatformSkillParams` 字段名以 Task 2 Step 3 确认为准。
- **去业务大小上限**：按 spec 决策，平台库上传不设大小上限（仅算 size/sha256）；zip bomb 解压防护在安装/解压链路（Plan 3/5），平台库上传只存归档不解压，故此处无解压校验。
- **待实现现场确认项**（计划中已标注，执行时先核对再写）：① `go.mod` 模块路径前缀；② handlers 包 `auth` import 与 `withPrincipal`/`principalFromCtx`/`apierror.New` 实际签名；③ `storage.ObjectStore` 方法签名；④ main 里 FS/S3 blob store 的配置判定（照抄现有 SkillBlobStore 分支）。
