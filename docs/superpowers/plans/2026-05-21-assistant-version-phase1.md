# 助手版本 Phase 1 实施计划：版本目录后端

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现助手版本（assistant version）的平台级版本目录后端——平台管理员可通过 API 创建、编辑、删除版本，并上传/删除版本 skill tar 包。

**Architecture:** 新增 `assistant_versions` 表（仅本表 + 给 `organizations` / `apps` 加列，全部 additive，不删任何东西，保证构建始终通过）。版本 CRUD 走标准的 sqlc → service → handler → routes 分层；skill tar 存 manager 文件系统主副本，名称从 tar 内 `SKILL.md` frontmatter 推导；镜像选项来自配置文件 `hermes.runtime_images` 列表。

**Tech Stack:** Go 1.x、pgx/v5、sqlc、gin、testify、golang-migrate、Go stdlib `archive/tar` + `gopkg.in/yaml.v3`。

**关联文档：** 设计 spec `docs/superpowers/specs/2026-05-21-assistant-version-design.md`。

**范围说明：** 本计划只覆盖 spec §11 的 Phase 1 后端部分（DB + 配置 + 版本 CRUD API）。前端版本管理页、组织 allowlist、实例绑定版本、manifest v2 / oc-entrypoint、version_synced 检测、删除旧 persona/model 列与代码，均由后续 Phase 2–5 的独立计划承接。本计划交付后即可用 API 完整管理版本，且单元测试覆盖。

---

## 文件结构

| 文件 | 职责 | 动作 |
|---|---|---|
| `internal/migrations/000023_assistant_versions.up.sql` / `.down.sql` | 建 `assistant_versions` 表 + 给 `organizations` / `apps` 加列 | 新建 |
| `sqlc.yaml` | 把新迁移加入 schema 列表 | 修改 |
| `internal/store/queries/assistant_versions.sql` | 版本 sqlc 查询定义 | 新建 |
| `internal/store/sqlc/*` | sqlc 生成产物 | 重新生成 |
| `internal/config/config.go` | 新增 `RuntimeImageConfig` 类型与 `HermesConfig.RuntimeImages` 字段 | 修改 |
| `internal/config/runtime_images.go` | 镜像列表解析与查找 helper | 新建 |
| `internal/config/runtime_images_test.go` | helper 单测 | 新建 |
| `config/manager.yaml`、`deploy/manage/config/manager.example.yaml` | 增加 `runtime_images` 配置段 | 修改 |
| `internal/auth/authorizer.go` | 新增版本权限谓词 | 修改 |
| `internal/auth/authorizer_test.go` | 权限谓词单测 | 修改 |
| `internal/integrations/hermes/skill_archive.go` | skill tar 校验 + 从 `SKILL.md` 推导名称 | 新建 |
| `internal/integrations/hermes/skill_archive_test.go` | skill tar 解析单测 | 新建 |
| `internal/service/assistant_version_service.go` | 版本 CRUD + skill 管理业务逻辑 | 新建 |
| `internal/service/assistant_version_service_test.go` | service 单测 | 新建 |
| `internal/service/errors.go` | 新增版本相关哨兵错误 | 修改 |
| `internal/api/handlers/dto.go` | 版本请求体类型 | 修改 |
| `internal/api/handlers/assistant_versions.go` | 版本 HTTP handler + 路由 | 新建 |
| `internal/api/handlers/assistant_versions_test.go` | handler 单测 | 新建 |
| `internal/api/router.go` | `Dependencies` 增字段并装配路由 | 修改 |
| `cmd/server/main.go` | 构造 service 并注入 router deps | 修改 |
| `openapi/openapi.yaml`、`web/src/api/generated.ts` | OpenAPI 同步产物 | 重新生成 |

---

## Task 1：DB 迁移——新增 assistant_versions 表与关联列

**Files:**
- Create: `internal/migrations/000023_assistant_versions.up.sql`
- Create: `internal/migrations/000023_assistant_versions.down.sql`
- Modify: `sqlc.yaml`

- [ ] **Step 1：写 up 迁移**

创建 `internal/migrations/000023_assistant_versions.up.sql`：

```sql
-- 000023_assistant_versions.up.sql
-- 助手版本目录表，以及组织 allowlist / 实例绑定版本所需的关联列。
-- 本迁移全部为 additive：不删除任何已有表或列，保证构建与运行不被打断。

CREATE TABLE assistant_versions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name          text NOT NULL,
    description   text NOT NULL DEFAULT '',
    system_prompt text NOT NULL,
    image_id      text NOT NULL,
    main_model    text NOT NULL,
    routing_json  jsonb NOT NULL DEFAULT '{}'::jsonb,
    skills_json   jsonb NOT NULL DEFAULT '[]'::jsonb,
    revision      integer NOT NULL DEFAULT 1,
    created_by    uuid NULL REFERENCES users(id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz NULL,
    CONSTRAINT assistant_versions_revision_check CHECK (revision > 0)
);

COMMENT ON TABLE assistant_versions IS '助手版本目录：把智能路由、skill、内置提示词、镜像打包成可复用的命名版本。';
COMMENT ON COLUMN assistant_versions.system_prompt IS '版本内置提示词，渲染进容器 SOUL.md 的版本层；字段语义泛化，可填写人设、行为规则等。';
COMMENT ON COLUMN assistant_versions.image_id IS '引用配置文件 hermes.runtime_images[].id。';
COMMENT ON COLUMN assistant_versions.routing_json IS '智能路由：8 个 auxiliary 槽位到模型名的映射，空槽位省略。';
COMMENT ON COLUMN assistant_versions.skills_json IS 'skill 元信息数组，每项 {name,file_path,file_size,file_sha256}；tar 字节存文件系统主副本。';
COMMENT ON COLUMN assistant_versions.revision IS '内容修订号，影响容器的字段变更时 +1，供实例 version_synced 检测使用。';

-- 版本名在未删除集合内唯一。
CREATE UNIQUE INDEX assistant_versions_name_active_idx
    ON assistant_versions(name) WHERE deleted_at IS NULL;

-- 组织可用版本 allowlist：jsonb 字符串数组。
ALTER TABLE organizations
    ADD COLUMN assistant_version_ids jsonb NOT NULL DEFAULT '[]'::jsonb;
COMMENT ON COLUMN organizations.assistant_version_ids IS '该组织可用的助手版本 id 数组（allowlist）。';

-- 实例绑定的版本与变更检测字段。version_id 暂可空，Phase 3 创建流程改造后由 service 强制必填。
ALTER TABLE apps
    ADD COLUMN version_id uuid NULL REFERENCES assistant_versions(id),
    ADD COLUMN applied_version_revision integer NOT NULL DEFAULT 0,
    ADD COLUMN applied_image_ref text NOT NULL DEFAULT '';
COMMENT ON COLUMN apps.version_id IS '实例绑定的助手版本。';
COMMENT ON COLUMN apps.applied_version_revision IS '上次初始化/重启时使用的版本 revision；与版本当前 revision 比较得出 version_synced。';
COMMENT ON COLUMN apps.applied_image_ref IS '上次实际拉取的镜像 ref；与配置解析出的 ref 比较得出 version_synced。';

CREATE INDEX apps_version_id_idx ON apps(version_id);
```

- [ ] **Step 2：写 down 迁移**

创建 `internal/migrations/000023_assistant_versions.down.sql`：

```sql
-- 000023_assistant_versions.down.sql
DROP INDEX IF EXISTS apps_version_id_idx;
ALTER TABLE apps
    DROP COLUMN IF EXISTS applied_image_ref,
    DROP COLUMN IF EXISTS applied_version_revision,
    DROP COLUMN IF EXISTS version_id;
ALTER TABLE organizations DROP COLUMN IF EXISTS assistant_version_ids;
DROP TABLE IF EXISTS assistant_versions;
```

- [ ] **Step 3：把迁移加入 sqlc.yaml**

在 `sqlc.yaml` 的 `schema:` 列表末尾、`000022_org_single_model.up.sql` 之后追加一行：

```yaml
      - internal/migrations/000023_assistant_versions.up.sql
```

- [ ] **Step 4：跑迁移验证 up / down 可逆**

Run: `make migrate-up && make migrate-down && make migrate-up`
Expected: 三步均成功，无报错；`assistant_versions` 表创建后被 down 删除再重建。

- [ ] **Step 5：提交**

```bash
git add internal/migrations/000023_assistant_versions.up.sql internal/migrations/000023_assistant_versions.down.sql sqlc.yaml
git commit -m "feat(assistant-version): 新增 assistant_versions 表与关联列迁移"
```

---

## Task 2：sqlc 查询定义与代码生成

**Files:**
- Create: `internal/store/queries/assistant_versions.sql`
- Modify: `internal/store/sqlc/*`（生成产物）

- [ ] **Step 1：写 sqlc 查询**

创建 `internal/store/queries/assistant_versions.sql`：

```sql
-- name: CreateAssistantVersion :one
INSERT INTO assistant_versions (
    name, description, system_prompt, image_id, main_model,
    routing_json, skills_json, created_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: GetAssistantVersion :one
SELECT * FROM assistant_versions
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetAssistantVersionByName :one
SELECT * FROM assistant_versions
WHERE name = $1 AND deleted_at IS NULL;

-- name: ListAssistantVersions :many
SELECT * FROM assistant_versions
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC;

-- name: UpdateAssistantVersion :one
-- revision 由 service 计算后整体写入（仅容器相关字段变更才递增）。
UPDATE assistant_versions
SET name = $2,
    description = $3,
    system_prompt = $4,
    image_id = $5,
    main_model = $6,
    routing_json = $7,
    skills_json = $8,
    revision = $9,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateAssistantVersionSkills :one
-- skill 上传/删除单独走此查询：只改 skills_json 与 revision，避免覆盖其它字段。
UPDATE assistant_versions
SET skills_json = $2,
    revision = $3,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteAssistantVersion :one
UPDATE assistant_versions
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: CountAppsUsingVersion :one
-- 严格保护：版本被未删除实例引用时不可删除。
SELECT count(*) FROM apps
WHERE version_id = $1 AND deleted_at IS NULL;

-- name: CountOrgsUsingVersion :one
-- 严格保护：版本出现在任意未删除组织 allowlist 时不可删除。
SELECT count(*) FROM organizations
WHERE deleted_at IS NULL AND jsonb_exists(assistant_version_ids, $1);
```

- [ ] **Step 2：生成 sqlc 代码**

Run: `make sqlc-generate`
Expected: 成功；`internal/store/sqlc/` 下新增 `assistant_versions.sql.go`，`models.go` 含 `AssistantVersion` 结构体，`querier.go` 含新方法。

- [ ] **Step 3：验证编译**

Run: `make vet` 或 `go build ./...`
Expected: 编译通过（此时还无业务代码调用新查询，仅验证生成产物合法）。

- [ ] **Step 4：提交**

```bash
git add internal/store/queries/assistant_versions.sql internal/store/sqlc/
git commit -m "feat(assistant-version): 新增 assistant_versions sqlc 查询"
```

---

## Task 3：配置文件镜像列表

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/runtime_images.go`
- Create: `internal/config/runtime_images_test.go`
- Modify: `config/manager.yaml`
- Modify: `deploy/manage/config/manager.example.yaml`

- [ ] **Step 1：写 helper 失败测试**

创建 `internal/config/runtime_images_test.go`：

```go
package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveRuntimeImageFound 验证按 id 能解析出对应镜像 ref。
func TestResolveRuntimeImageFound(t *testing.T) {
	imgs := []RuntimeImageConfig{
		{ID: "v2026.5.16", Label: "当前", Ref: "repo/hermes:v2026.5.16-x"},
		{ID: "v2026.5.7", Label: "旧版", Ref: "repo/hermes:v2026.5.7-x"},
	}
	ref, ok := ResolveRuntimeImage(imgs, "v2026.5.7")
	require.True(t, ok)
	assert.Equal(t, "repo/hermes:v2026.5.7-x", ref)
}

// TestResolveRuntimeImageMissing 验证未知 id 返回 ok=false。
func TestResolveRuntimeImageMissing(t *testing.T) {
	_, ok := ResolveRuntimeImage(nil, "nope")
	assert.False(t, ok)
}

// TestValidateRuntimeImagesRejectsDuplicateID 验证 id 重复时报错。
func TestValidateRuntimeImagesRejectsDuplicateID(t *testing.T) {
	err := ValidateRuntimeImages([]RuntimeImageConfig{
		{ID: "a", Label: "A", Ref: "r1"},
		{ID: "a", Label: "A2", Ref: "r2"},
	})
	require.Error(t, err)
}

// TestValidateRuntimeImagesRejectsEmptyField 验证 id/ref 为空时报错。
func TestValidateRuntimeImagesRejectsEmptyField(t *testing.T) {
	err := ValidateRuntimeImages([]RuntimeImageConfig{{ID: "a", Label: "A", Ref: ""}})
	require.Error(t, err)
}

// TestValidateRuntimeImagesAcceptsValid 验证合法列表通过校验。
func TestValidateRuntimeImagesAcceptsValid(t *testing.T) {
	err := ValidateRuntimeImages([]RuntimeImageConfig{{ID: "a", Label: "A", Ref: "r"}})
	require.NoError(t, err)
}
```

- [ ] **Step 2：运行测试确认失败**

Run: `go test ./internal/config/ -run TestResolveRuntimeImage -v`
Expected: 编译失败——`RuntimeImageConfig`、`ResolveRuntimeImage`、`ValidateRuntimeImages` 未定义。

- [ ] **Step 3：在 config.go 增加类型与字段**

在 `internal/config/config.go` 的 `HermesConfig` 结构体内，`RuntimeImage` 字段下方新增：

```go
	// RuntimeImages 是平台可选的 Hermes 镜像列表，助手版本的 image_id 引用其中的 id。
	// 与单值 RuntimeImage 并存（后续 Phase 移除单值字段）。
	RuntimeImages []RuntimeImageConfig `yaml:"runtime_images"`
```

在 `HermesConfig` 类型定义之后新增类型：

```go
// RuntimeImageConfig 是单个可选 Hermes 镜像条目。
// id 是稳定槽位标识（助手版本存它），label 供前端展示，ref 是具体可拉取 tag。
type RuntimeImageConfig struct {
	ID    string `yaml:"id"`
	Label string `yaml:"label"`
	Ref   string `yaml:"ref"`
}
```

- [ ] **Step 4：写 helper 实现**

创建 `internal/config/runtime_images.go`：

```go
package config

import "fmt"

// ResolveRuntimeImage 按 id 在镜像列表中查找对应 ref。
// 找到返回 (ref, true)；未找到返回 ("", false)。
func ResolveRuntimeImage(images []RuntimeImageConfig, id string) (string, bool) {
	for _, img := range images {
		if img.ID == id {
			return img.Ref, true
		}
	}
	return "", false
}

// ValidateRuntimeImages 校验镜像列表：id 非空且唯一、ref 非空。
// 空列表视为合法（Phase 1 不强制配置该段）。
func ValidateRuntimeImages(images []RuntimeImageConfig) error {
	seen := make(map[string]struct{}, len(images))
	for i, img := range images {
		if img.ID == "" {
			return fmt.Errorf("hermes.runtime_images[%d].id 不能为空", i)
		}
		if img.Ref == "" {
			return fmt.Errorf("hermes.runtime_images[%d].ref 不能为空", i)
		}
		if _, dup := seen[img.ID]; dup {
			return fmt.Errorf("hermes.runtime_images 存在重复 id: %s", img.ID)
		}
		seen[img.ID] = struct{}{}
	}
	return nil
}
```

- [ ] **Step 5：在 Config.Validate 调用校验**

在 `internal/config/config.go` 的 `Validate` 方法内、`missing` 收集逻辑之后、`return` 之前，加入：

```go
	if err := ValidateRuntimeImages(c.Hermes.RuntimeImages); err != nil {
		return err
	}
```

（若 `Validate` 现有结构是先 `if len(missing) > 0 { return ... }` 再 return nil，则把上面这段放在 `if len(missing) > 0` 判断之后。）

- [ ] **Step 6：运行测试确认通过**

Run: `go test ./internal/config/ -run "TestResolveRuntimeImage|TestValidateRuntimeImages" -v`
Expected: 全部 PASS。

- [ ] **Step 7：更新配置文件**

在 `config/manager.yaml` 的 `hermes:` 段内、`runtime_image:` 一行之后新增：

```yaml
  # 助手版本可选的 Hermes 镜像列表。id 稳定、ref 为具体可拉取 tag。
  runtime_images:
    - id: "v2026.5.16"
      label: "Hermes v2026.5.16（当前）"
      ref: "crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes:v2026.5.16-2026-05-21-12-00-00"
    - id: "v2026.5.7"
      label: "Hermes v2026.5.7（旧版）"
      ref: "crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes:v2026.5.7-2026-05-21-12-00-00"
```

在 `deploy/manage/config/manager.example.yaml` 的 `hermes:` 段做同样追加（ref 用占位示例值即可）。

- [ ] **Step 8：验证配置可加载**

Run: `go test ./internal/config/ -v`
Expected: 全部 PASS（含既有 loader 测试）。

- [ ] **Step 9：提交**

```bash
git add internal/config/ config/manager.yaml deploy/manage/config/manager.example.yaml
git commit -m "feat(assistant-version): 配置文件新增 Hermes 镜像列表"
```

---

## Task 4：权限谓词

**Files:**
- Modify: `internal/auth/authorizer.go`
- Modify: `internal/auth/authorizer_test.go`

- [ ] **Step 1：写权限失败测试**

在 `internal/auth/authorizer_test.go` 末尾追加：

```go
// TestCanManageAssistantVersion 验证仅平台管理员可写助手版本。
func TestCanManageAssistantVersion(t *testing.T) {
	assert.True(t, CanManageAssistantVersion(Principal{Role: domain.UserRolePlatformAdmin}))
	assert.False(t, CanManageAssistantVersion(Principal{Role: domain.UserRoleOrgAdmin}))
	assert.False(t, CanManageAssistantVersion(Principal{Role: domain.UserRoleOrgMember}))
}

// TestCanViewAssistantVersion 验证平台管理员与组织管理员可读助手版本，普通成员不可。
func TestCanViewAssistantVersion(t *testing.T) {
	assert.True(t, CanViewAssistantVersion(Principal{Role: domain.UserRolePlatformAdmin}))
	assert.True(t, CanViewAssistantVersion(Principal{Role: domain.UserRoleOrgAdmin}))
	assert.False(t, CanViewAssistantVersion(Principal{Role: domain.UserRoleOrgMember}))
}
```

若 `authorizer_test.go` 尚未 import `assert` / `domain`，按文件现有 import 风格补齐。

- [ ] **Step 2：运行测试确认失败**

Run: `go test ./internal/auth/ -run "AssistantVersion" -v`
Expected: 编译失败——函数未定义。

- [ ] **Step 3：实现权限谓词**

在 `internal/auth/authorizer.go` 末尾追加：

```go
// 助手版本资源 ----------------------------------------------------

// CanManageAssistantVersion 判断主体能否创建/编辑/删除助手版本。
// 助手版本是平台级目录，仅平台管理员可写。
func CanManageAssistantVersion(p Principal) bool {
	return p.Role == domain.UserRolePlatformAdmin
}

// CanViewAssistantVersion 判断主体能否查看助手版本。
// 平台管理员维护目录，组织管理员需读取版本以便创建实例时选用；普通成员不可见。
func CanViewAssistantVersion(p Principal) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin, domain.UserRoleOrgAdmin:
		return true
	default:
		return false
	}
}
```

- [ ] **Step 4：运行测试确认通过**

Run: `go test ./internal/auth/ -run "AssistantVersion" -v`
Expected: 全部 PASS。

- [ ] **Step 5：提交**

```bash
git add internal/auth/authorizer.go internal/auth/authorizer_test.go
git commit -m "feat(assistant-version): 新增助手版本权限谓词"
```

---

## Task 5：skill tar 校验与名称推导

**Files:**
- Create: `internal/integrations/hermes/skill_archive.go`
- Create: `internal/integrations/hermes/skill_archive_test.go`

- [ ] **Step 1：写失败测试**

创建 `internal/integrations/hermes/skill_archive_test.go`：

```go
package hermes

import (
	"archive/tar"
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTar 构造一个内含指定文件的内存 tar，供测试使用。
func makeTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// TestInspectSkillArchiveExtractsName 验证从 SKILL.md frontmatter 推导出 name。
func TestInspectSkillArchiveExtractsName(t *testing.T) {
	skillMD := "---\nname: weather-lookup\ndescription: 查天气\n---\n# 天气\n正文"
	data := makeTar(t, map[string]string{"weather/SKILL.md": skillMD})
	info, err := InspectSkillArchive(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, "weather-lookup", info.Name)
}

// TestInspectSkillArchiveRejectsMissingSkillMD 验证缺少 SKILL.md 时报错。
func TestInspectSkillArchiveRejectsMissingSkillMD(t *testing.T) {
	data := makeTar(t, map[string]string{"weather/readme.txt": "x"})
	_, err := InspectSkillArchive(bytes.NewReader(data))
	require.ErrorIs(t, err, ErrSkillArchiveNoSkillMD)
}

// TestInspectSkillArchiveRejectsNoName 验证 SKILL.md frontmatter 缺 name 时报错。
func TestInspectSkillArchiveRejectsNoName(t *testing.T) {
	data := makeTar(t, map[string]string{"SKILL.md": "---\ndescription: x\n---\n正文"})
	_, err := InspectSkillArchive(bytes.NewReader(data))
	require.ErrorIs(t, err, ErrSkillArchiveNoName)
}

// TestInspectSkillArchiveRejectsBadTar 验证非法 tar 字节报错。
func TestInspectSkillArchiveRejectsBadTar(t *testing.T) {
	_, err := InspectSkillArchive(bytes.NewReader([]byte("not a tar at all")))
	require.Error(t, err)
}

// TestInspectSkillArchiveRejectsUnsafePath 验证 tar 内含越界路径时报错。
func TestInspectSkillArchiveRejectsUnsafePath(t *testing.T) {
	data := makeTar(t, map[string]string{"../evil/SKILL.md": "---\nname: x\n---\n"})
	_, err := InspectSkillArchive(bytes.NewReader(data))
	require.ErrorIs(t, err, ErrSkillArchiveUnsafePath)
}
```

- [ ] **Step 2：运行测试确认失败**

Run: `go test ./internal/integrations/hermes/ -run InspectSkillArchive -v`
Expected: 编译失败——`InspectSkillArchive` 等未定义。

- [ ] **Step 3：写实现**

创建 `internal/integrations/hermes/skill_archive.go`：

```go
package hermes

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// skill tar 校验失败的哨兵错误。
var (
	// ErrSkillArchiveNoSkillMD tar 内不含 SKILL.md。
	ErrSkillArchiveNoSkillMD = errors.New("skill tar 内未找到 SKILL.md")
	// ErrSkillArchiveNoName SKILL.md frontmatter 缺少 name 字段。
	ErrSkillArchiveNoName = errors.New("SKILL.md frontmatter 缺少 name")
	// ErrSkillArchiveUnsafePath tar 条目路径越界（含 .. 或绝对路径）。
	ErrSkillArchiveUnsafePath = errors.New("skill tar 含越界路径条目")
)

// SkillArchiveInfo 是 skill tar 校验后的元信息。
type SkillArchiveInfo struct {
	// Name 来自 tar 内 SKILL.md frontmatter 的 name 字段。
	Name string
}

// skillMDFrontmatter 仅取 SKILL.md frontmatter 需要的字段。
type skillMDFrontmatter struct {
	Name string `yaml:"name"`
}

// InspectSkillArchive 读取并校验一个 skill tar：
//   - 所有条目路径必须在 tar 内部、不得越界（防解压逃逸）；
//   - 必须含一个 SKILL.md（根目录或任意一级子目录均可）；
//   - SKILL.md 必须有 YAML frontmatter 且含非空 name。
//
// 校验通过返回 SkillArchiveInfo；调用方负责另行限制 tar 大小。
func InspectSkillArchive(r io.Reader) (SkillArchiveInfo, error) {
	tr := tar.NewReader(r)
	var skillMD string
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return SkillArchiveInfo{}, fmt.Errorf("读取 skill tar 失败: %w", err)
		}
		clean := path.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") || strings.Contains(clean, "/../") {
			return SkillArchiveInfo{}, fmt.Errorf("%w: %s", ErrSkillArchiveUnsafePath, hdr.Name)
		}
		if hdr.Typeflag == tar.TypeReg && path.Base(clean) == "SKILL.md" {
			body, err := io.ReadAll(tr)
			if err != nil {
				return SkillArchiveInfo{}, fmt.Errorf("读取 SKILL.md 失败: %w", err)
			}
			skillMD = string(body)
			found = true
		}
	}
	if !found {
		return SkillArchiveInfo{}, ErrSkillArchiveNoSkillMD
	}
	name, err := parseSkillMDName(skillMD)
	if err != nil {
		return SkillArchiveInfo{}, err
	}
	return SkillArchiveInfo{Name: name}, nil
}

// parseSkillMDName 从 SKILL.md 的 YAML frontmatter 提取 name。
// frontmatter 约定以 "---" 行开头、再以 "---" 行结束。
func parseSkillMDName(body string) (string, error) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if !strings.HasPrefix(body, "---\n") {
		return "", ErrSkillArchiveNoName
	}
	rest := body[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", ErrSkillArchiveNoName
	}
	var fm skillMDFrontmatter
	if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
		return "", fmt.Errorf("解析 SKILL.md frontmatter 失败: %w", err)
	}
	if strings.TrimSpace(fm.Name) == "" {
		return "", ErrSkillArchiveNoName
	}
	return strings.TrimSpace(fm.Name), nil
}
```

- [ ] **Step 4：运行测试确认通过**

Run: `go test ./internal/integrations/hermes/ -run InspectSkillArchive -v`
Expected: 全部 PASS。

- [ ] **Step 5：提交**

```bash
git add internal/integrations/hermes/skill_archive.go internal/integrations/hermes/skill_archive_test.go
git commit -m "feat(assistant-version): 新增 skill tar 校验与名称推导"
```

---

## Task 6：service 哨兵错误

**Files:**
- Modify: `internal/service/errors.go`

- [ ] **Step 1：新增哨兵错误**

在 `internal/service/errors.go` 中，按文件现有 `errors.New(...)` 风格新增（放在已有错误声明附近）：

```go
	// ErrAssistantVersionNotFound 助手版本不存在或已删除。
	ErrAssistantVersionNotFound = errors.New("助手版本不存在")
	// ErrAssistantVersionDenied 无权操作助手版本。
	ErrAssistantVersionDenied = errors.New("无权操作助手版本")
	// ErrAssistantVersionInvalid 助手版本入参非法（名称空、模型不存在、镜像 id 未知等）。
	ErrAssistantVersionInvalid = errors.New("助手版本入参非法")
	// ErrAssistantVersionNameTaken 助手版本名称已被占用。
	ErrAssistantVersionNameTaken = errors.New("助手版本名称已存在")
	// ErrAssistantVersionInUse 助手版本正被组织或实例引用，不可删除。
	ErrAssistantVersionInUse = errors.New("助手版本正被引用，不可删除")
	// ErrSkillTooLarge 上传的 skill tar 超过大小上限。
	ErrSkillTooLarge = errors.New("skill tar 超过大小上限")
)
```

注意：若现有错误是用单个 `var (...)` 块声明，把上面几行并入该块（去掉多余的结尾 `)`）。

- [ ] **Step 2：验证编译**

Run: `go build ./internal/service/`
Expected: 编译通过。

- [ ] **Step 3：提交**

```bash
git add internal/service/errors.go
git commit -m "feat(assistant-version): 新增助手版本相关哨兵错误"
```

---

## Task 7：版本 service——类型、Store 接口、List/Get

**Files:**
- Create: `internal/service/assistant_version_service.go`
- Create: `internal/service/assistant_version_service_test.go`

本任务建立 service 骨架与只读路径；Create/Update/Delete/skill 在后续任务补齐。

- [ ] **Step 1：写 List/Get 失败测试**

创建 `internal/service/assistant_version_service_test.go`：

```go
package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// fakeAVStore 是 AssistantVersionStore 的内存实现，按需在各测试里填充。
type fakeAVStore struct {
	versions   map[string]sqlc.AssistantVersion
	byName     map[string]sqlc.AssistantVersion
	appCount   int64
	orgCount   int64
	createErr  error
	updateErr  error
}

func newFakeAVStore() *fakeAVStore {
	return &fakeAVStore{versions: map[string]sqlc.AssistantVersion{}, byName: map[string]sqlc.AssistantVersion{}}
}

func (f *fakeAVStore) GetAssistantVersion(_ context.Context, id pgtype.UUID) (sqlc.AssistantVersion, error) {
	v, ok := f.versions[uuidToString(id)]
	if !ok {
		return sqlc.AssistantVersion{}, pgx.ErrNoRows
	}
	return v, nil
}

func (f *fakeAVStore) GetAssistantVersionByName(_ context.Context, name string) (sqlc.AssistantVersion, error) {
	v, ok := f.byName[name]
	if !ok {
		return sqlc.AssistantVersion{}, pgx.ErrNoRows
	}
	return v, nil
}

func (f *fakeAVStore) ListAssistantVersions(context.Context) ([]sqlc.AssistantVersion, error) {
	out := make([]sqlc.AssistantVersion, 0, len(f.versions))
	for _, v := range f.versions {
		out = append(out, v)
	}
	return out, nil
}

func (f *fakeAVStore) CreateAssistantVersion(_ context.Context, arg sqlc.CreateAssistantVersionParams) (sqlc.AssistantVersion, error) {
	if f.createErr != nil {
		return sqlc.AssistantVersion{}, f.createErr
	}
	v := sqlc.AssistantVersion{
		ID: mustUUID("00000000-0000-0000-0000-0000000000a1"), Name: arg.Name,
		Description: arg.Description, SystemPrompt: arg.SystemPrompt, ImageID: arg.ImageID,
		MainModel: arg.MainModel, RoutingJson: arg.RoutingJson, SkillsJson: arg.SkillsJson, Revision: 1,
	}
	f.versions[uuidToString(v.ID)] = v
	f.byName[v.Name] = v
	return v, nil
}

func (f *fakeAVStore) UpdateAssistantVersion(_ context.Context, arg sqlc.UpdateAssistantVersionParams) (sqlc.AssistantVersion, error) {
	if f.updateErr != nil {
		return sqlc.AssistantVersion{}, f.updateErr
	}
	v := f.versions[uuidToString(arg.ID)]
	v.Name, v.Description, v.SystemPrompt = arg.Name, arg.Description, arg.SystemPrompt
	v.ImageID, v.MainModel = arg.ImageID, arg.MainModel
	v.RoutingJson, v.SkillsJson, v.Revision = arg.RoutingJson, arg.SkillsJson, arg.Revision
	f.versions[uuidToString(v.ID)] = v
	return v, nil
}

func (f *fakeAVStore) UpdateAssistantVersionSkills(_ context.Context, arg sqlc.UpdateAssistantVersionSkillsParams) (sqlc.AssistantVersion, error) {
	v := f.versions[uuidToString(arg.ID)]
	v.SkillsJson, v.Revision = arg.SkillsJson, arg.Revision
	f.versions[uuidToString(v.ID)] = v
	return v, nil
}

func (f *fakeAVStore) SoftDeleteAssistantVersion(_ context.Context, id pgtype.UUID) (sqlc.AssistantVersion, error) {
	v, ok := f.versions[uuidToString(id)]
	if !ok {
		return sqlc.AssistantVersion{}, pgx.ErrNoRows
	}
	delete(f.versions, uuidToString(id))
	delete(f.byName, v.Name)
	return v, nil
}

func (f *fakeAVStore) CountAppsUsingVersion(context.Context, pgtype.UUID) (int64, error) {
	return f.appCount, nil
}

func (f *fakeAVStore) CountOrgsUsingVersion(context.Context, []byte) (int64, error) {
	return f.orgCount, nil
}

// fakeModelCatalog 是 AssistantVersionModelValidator 的内存实现。
type fakeModelCatalog struct{ models map[string]bool }

func (f fakeModelCatalog) hasModel(id string) bool { return f.models[id] }

// platformPrincipal / orgAdminPrincipal 是测试公用主体。
func platformPrincipal() auth.Principal { return auth.Principal{UserID: "00000000-0000-0000-0000-0000000000ff", Role: domain.UserRolePlatformAdmin} }
func orgAdminPrincipal() auth.Principal { return auth.Principal{Role: domain.UserRoleOrgAdmin} }

// TestAssistantVersionListDeniesMember 验证普通成员读版本列表被拒。
func TestAssistantVersionListDeniesMember(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	_, err := svc.List(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember})
	require.ErrorIs(t, err, ErrAssistantVersionDenied)
}

// TestAssistantVersionGetNotFound 验证查询不存在的版本返回 NotFound。
func TestAssistantVersionGetNotFound(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	_, err := svc.Get(context.Background(), platformPrincipal(), "00000000-0000-0000-0000-0000000000a1")
	require.ErrorIs(t, err, ErrAssistantVersionNotFound)
}
```

> 说明：`newTestAVService`、`mustUUID` 是测试 helper，在 Step 3 与 Task 8 内补全。`uuidToString` 已存在于 service 包。

- [ ] **Step 2：运行测试确认失败**

Run: `go test ./internal/service/ -run AssistantVersion -v`
Expected: 编译失败——service 类型与 helper 未定义。

- [ ] **Step 3：写 service 骨架与只读方法**

创建 `internal/service/assistant_version_service.go`：

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/store/sqlc"
)

// auxiliarySlots 是智能路由支持的 8 个 auxiliary 槽位，顺序固定，用于校验与渲染。
var auxiliarySlots = []string{
	"vision", "compression", "web_extract", "session_search",
	"title_generation", "approval", "skills_hub", "mcp",
}

// AssistantVersionStore 抽象版本 service 需要的数据访问能力。
type AssistantVersionStore interface {
	GetAssistantVersion(ctx context.Context, id pgtype.UUID) (sqlc.AssistantVersion, error)
	GetAssistantVersionByName(ctx context.Context, name string) (sqlc.AssistantVersion, error)
	ListAssistantVersions(ctx context.Context) ([]sqlc.AssistantVersion, error)
	CreateAssistantVersion(ctx context.Context, arg sqlc.CreateAssistantVersionParams) (sqlc.AssistantVersion, error)
	UpdateAssistantVersion(ctx context.Context, arg sqlc.UpdateAssistantVersionParams) (sqlc.AssistantVersion, error)
	UpdateAssistantVersionSkills(ctx context.Context, arg sqlc.UpdateAssistantVersionSkillsParams) (sqlc.AssistantVersion, error)
	SoftDeleteAssistantVersion(ctx context.Context, id pgtype.UUID) (sqlc.AssistantVersion, error)
	CountAppsUsingVersion(ctx context.Context, id pgtype.UUID) (int64, error)
	CountOrgsUsingVersion(ctx context.Context, idJSON []byte) (int64, error)
}

// AssistantVersionImageResolver 抽象「校验 image_id 是否存在于配置」的能力。
type AssistantVersionImageResolver interface {
	HasRuntimeImage(id string) bool
}

// AssistantVersionModelValidator 抽象「校验模型名是否存在」的能力。
type AssistantVersionModelValidator interface {
	HasModel(id string) bool
}

// SkillBlobStore 抽象 skill tar 文件系统主副本的读写能力。
type SkillBlobStore interface {
	// PutSkill 写入一个 skill tar，返回相对数据根目录的存储路径。
	PutSkill(versionID, skillName string, data []byte) (relPath string, err error)
	// DeleteSkill 删除一个 skill tar。
	DeleteSkill(relPath string) error
}

// AssistantVersionService 维护助手版本目录。
type AssistantVersionService struct {
	store  AssistantVersionStore
	images AssistantVersionImageResolver
	models AssistantVersionModelValidator
	blobs  SkillBlobStore
	// maxSkillBytes 是单个 skill tar 的大小上限。
	maxSkillBytes int64
}

// NewAssistantVersionService 创建版本 service。maxSkillBytes<=0 时取默认 10 MiB。
func NewAssistantVersionService(
	store AssistantVersionStore,
	images AssistantVersionImageResolver,
	models AssistantVersionModelValidator,
	blobs SkillBlobStore,
	maxSkillBytes int64,
) *AssistantVersionService {
	if maxSkillBytes <= 0 {
		maxSkillBytes = 10 << 20
	}
	return &AssistantVersionService{store: store, images: images, models: models, blobs: blobs, maxSkillBytes: maxSkillBytes}
}

// AssistantVersionSkill 是 skills_json 内单个 skill 的元信息。
type AssistantVersionSkill struct {
	Name       string `json:"name"`
	FilePath   string `json:"file_path"`
	FileSize   int64  `json:"file_size"`
	FileSha256 string `json:"file_sha256"`
}

// AssistantVersionResult 是面向 handler/前端的版本视图。
type AssistantVersionResult struct {
	ID           string                  `json:"id"`
	Name         string                  `json:"name"`
	Description  string                  `json:"description"`
	SystemPrompt string                  `json:"system_prompt"`
	ImageID      string                  `json:"image_id"`
	MainModel    string                  `json:"main_model"`
	Routing      map[string]string       `json:"routing"`
	Skills       []AssistantVersionSkill `json:"skills"`
	Revision     int32                   `json:"revision"`
}

// List 返回全部未删除版本，按创建时间倒序。
func (s *AssistantVersionService) List(ctx context.Context, principal auth.Principal) ([]AssistantVersionResult, error) {
	if !auth.CanViewAssistantVersion(principal) {
		return nil, ErrAssistantVersionDenied
	}
	rows, err := s.store.ListAssistantVersions(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询版本列表失败: %w", err)
	}
	out := make([]AssistantVersionResult, 0, len(rows))
	for _, row := range rows {
		r, err := toAssistantVersionResult(row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// Get 返回单个版本。
func (s *AssistantVersionService) Get(ctx context.Context, principal auth.Principal, id string) (AssistantVersionResult, error) {
	if !auth.CanViewAssistantVersion(principal) {
		return AssistantVersionResult{}, ErrAssistantVersionDenied
	}
	row, err := s.loadVersion(ctx, id)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	return toAssistantVersionResult(row)
}

// loadVersion 按 id 取版本，未找到统一映射为 ErrAssistantVersionNotFound。
func (s *AssistantVersionService) loadVersion(ctx context.Context, id string) (sqlc.AssistantVersion, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return sqlc.AssistantVersion{}, ErrAssistantVersionNotFound
	}
	row, err := s.store.GetAssistantVersion(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.AssistantVersion{}, ErrAssistantVersionNotFound
	}
	if err != nil {
		return sqlc.AssistantVersion{}, fmt.Errorf("查询版本失败: %w", err)
	}
	return row, nil
}

// toAssistantVersionResult 把 sqlc 行转成对外视图。
func toAssistantVersionResult(row sqlc.AssistantVersion) (AssistantVersionResult, error) {
	routing := map[string]string{}
	if len(row.RoutingJson) > 0 {
		if err := json.Unmarshal(row.RoutingJson, &routing); err != nil {
			return AssistantVersionResult{}, fmt.Errorf("解析 routing_json 失败: %w", err)
		}
	}
	skills := []AssistantVersionSkill{}
	if len(row.SkillsJson) > 0 {
		if err := json.Unmarshal(row.SkillsJson, &skills); err != nil {
			return AssistantVersionResult{}, fmt.Errorf("解析 skills_json 失败: %w", err)
		}
	}
	return AssistantVersionResult{
		ID:           uuidToString(row.ID),
		Name:         row.Name,
		Description:  row.Description,
		SystemPrompt: row.SystemPrompt,
		ImageID:      row.ImageID,
		MainModel:    row.MainModel,
		Routing:      routing,
		Skills:       skills,
		Revision:     row.Revision,
	}, nil
}

// decodeSkills 把 skills_json 解为切片，供 Update/skill 操作复用。
func decodeSkills(raw []byte) ([]AssistantVersionSkill, error) {
	skills := []AssistantVersionSkill{}
	if len(raw) == 0 {
		return skills, nil
	}
	if err := json.Unmarshal(raw, &skills); err != nil {
		return nil, fmt.Errorf("解析 skills_json 失败: %w", err)
	}
	return skills, nil
}

// trimSpace 是 strings.TrimSpace 的本地别名，保持调用点简洁。
func trimSpace(s string) string { return strings.TrimSpace(s) }
```

- [ ] **Step 4：补测试 helper**

在 `internal/service/assistant_version_service_test.go` 末尾追加：

```go
// mustUUID 把字符串解析为 pgtype.UUID，失败即 panic（仅测试用）。
func mustUUID(s string) pgtype.UUID {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		panic(err)
	}
	return u
}

// newTestAVService 用内存桩构造版本 service，默认模型与镜像校验全部通过。
func newTestAVService(t *testing.T, store *fakeAVStore) *AssistantVersionService {
	t.Helper()
	return NewAssistantVersionService(store, fakeImageResolver{}, fakeModelValidator{}, fakeBlobStore{}, 0)
}

// fakeImageResolver 默认认为所有 image_id 都存在。
type fakeImageResolver struct{}

func (fakeImageResolver) HasRuntimeImage(string) bool { return true }

// fakeModelValidator 默认认为所有模型名都存在。
type fakeModelValidator struct{}

func (fakeModelValidator) HasModel(string) bool { return true }

// fakeBlobStore 在内存里模拟 skill tar 存储。
type fakeBlobStore struct{}

func (fakeBlobStore) PutSkill(versionID, skillName string, _ []byte) (string, error) {
	return "versions/" + versionID + "/skills/" + skillName + ".tar", nil
}

func (fakeBlobStore) DeleteSkill(string) error { return nil }
```

- [ ] **Step 5：运行测试确认通过**

Run: `go test ./internal/service/ -run AssistantVersion -v`
Expected: `TestAssistantVersionListDeniesMember`、`TestAssistantVersionGetNotFound` PASS。

- [ ] **Step 6：提交**

```bash
git add internal/service/assistant_version_service.go internal/service/assistant_version_service_test.go
git commit -m "feat(assistant-version): 版本 service 骨架与只读路径"
```

---

## Task 8：版本 service——Create

**Files:**
- Modify: `internal/service/assistant_version_service.go`
- Modify: `internal/service/assistant_version_service_test.go`

- [ ] **Step 1：写 Create 失败测试**

在 `internal/service/assistant_version_service_test.go` 末尾追加：

```go
// validCreateInput 返回一组合法的版本创建入参。
func validCreateInput() AssistantVersionInput {
	return AssistantVersionInput{
		Name: "标准版", Description: "默认版本", SystemPrompt: "你是助手",
		ImageID: "v2026.5.16", MainModel: "qwen", Routing: map[string]string{"vision": "gpt"},
	}
}

// TestAssistantVersionCreateOK 验证合法入参创建成功且 revision 为 1。
func TestAssistantVersionCreateOK(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	got, err := svc.Create(context.Background(), platformPrincipal(), validCreateInput())
	require.NoError(t, err)
	assert.Equal(t, "标准版", got.Name)
	assert.EqualValues(t, 1, got.Revision)
	assert.Equal(t, "gpt", got.Routing["vision"])
}

// TestAssistantVersionCreateDeniesOrgAdmin 验证组织管理员不能创建版本。
func TestAssistantVersionCreateDeniesOrgAdmin(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	_, err := svc.Create(context.Background(), orgAdminPrincipal(), validCreateInput())
	require.ErrorIs(t, err, ErrAssistantVersionDenied)
}

// TestAssistantVersionCreateRejectsEmptyName 验证名称为空时报 Invalid。
func TestAssistantVersionCreateRejectsEmptyName(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	in := validCreateInput()
	in.Name = "  "
	_, err := svc.Create(context.Background(), platformPrincipal(), in)
	require.ErrorIs(t, err, ErrAssistantVersionInvalid)
}

// TestAssistantVersionCreateRejectsDuplicateName 验证名称已存在时报 NameTaken。
func TestAssistantVersionCreateRejectsDuplicateName(t *testing.T) {
	store := newFakeAVStore()
	store.byName["标准版"] = sqlc.AssistantVersion{Name: "标准版"}
	svc := newTestAVService(t, store)
	_, err := svc.Create(context.Background(), platformPrincipal(), validCreateInput())
	require.ErrorIs(t, err, ErrAssistantVersionNameTaken)
}

// TestAssistantVersionCreateRejectsUnknownImage 验证 image_id 不在配置内时报 Invalid。
func TestAssistantVersionCreateRejectsUnknownImage(t *testing.T) {
	svc := NewAssistantVersionService(newFakeAVStore(), rejectingImageResolver{}, fakeModelValidator{}, fakeBlobStore{}, 0)
	_, err := svc.Create(context.Background(), platformPrincipal(), validCreateInput())
	require.ErrorIs(t, err, ErrAssistantVersionInvalid)
}

// TestAssistantVersionCreateRejectsUnknownModel 验证主模型不存在时报 Invalid。
func TestAssistantVersionCreateRejectsUnknownModel(t *testing.T) {
	svc := NewAssistantVersionService(newFakeAVStore(), fakeImageResolver{}, rejectingModelValidator{}, fakeBlobStore{}, 0)
	_, err := svc.Create(context.Background(), platformPrincipal(), validCreateInput())
	require.ErrorIs(t, err, ErrAssistantVersionInvalid)
}

// TestAssistantVersionCreateRejectsUnknownRoutingSlot 验证 routing 含非法槽位名时报 Invalid。
func TestAssistantVersionCreateRejectsUnknownRoutingSlot(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	in := validCreateInput()
	in.Routing = map[string]string{"not_a_slot": "qwen"}
	_, err := svc.Create(context.Background(), platformPrincipal(), in)
	require.ErrorIs(t, err, ErrAssistantVersionInvalid)
}

// rejectingImageResolver 认为所有 image_id 都不存在。
type rejectingImageResolver struct{}

func (rejectingImageResolver) HasRuntimeImage(string) bool { return false }

// rejectingModelValidator 认为所有模型都不存在。
type rejectingModelValidator struct{}

func (rejectingModelValidator) HasModel(string) bool { return false }
```

- [ ] **Step 2：运行测试确认失败**

Run: `go test ./internal/service/ -run "AssistantVersionCreate" -v`
Expected: 编译失败——`AssistantVersionInput`、`Create` 未定义。

- [ ] **Step 3：实现 Create 与共用校验**

在 `internal/service/assistant_version_service.go` 末尾追加：

```go
// AssistantVersionInput 是创建/更新版本的入参。
type AssistantVersionInput struct {
	Name         string
	Description  string
	SystemPrompt string
	ImageID      string
	MainModel    string
	// Routing 是 auxiliary 槽位到模型名的映射；key 必须是 auxiliarySlots 之一。
	Routing map[string]string
}

// validateInput 校验版本入参的业务规则（不含名称唯一性，由调用方单独查）。
func (s *AssistantVersionService) validateInput(in AssistantVersionInput) error {
	if trimSpace(in.Name) == "" {
		return fmt.Errorf("%w: 名称不能为空", ErrAssistantVersionInvalid)
	}
	if trimSpace(in.SystemPrompt) == "" {
		return fmt.Errorf("%w: 内置提示词不能为空", ErrAssistantVersionInvalid)
	}
	if !s.images.HasRuntimeImage(in.ImageID) {
		return fmt.Errorf("%w: 镜像 %s 不存在于配置", ErrAssistantVersionInvalid, in.ImageID)
	}
	if trimSpace(in.MainModel) == "" || !s.models.HasModel(in.MainModel) {
		return fmt.Errorf("%w: 主模型 %s 不可用", ErrAssistantVersionInvalid, in.MainModel)
	}
	valid := make(map[string]struct{}, len(auxiliarySlots))
	for _, slot := range auxiliarySlots {
		valid[slot] = struct{}{}
	}
	for slot, model := range in.Routing {
		if _, ok := valid[slot]; !ok {
			return fmt.Errorf("%w: 未知路由槽位 %s", ErrAssistantVersionInvalid, slot)
		}
		if trimSpace(model) == "" {
			continue
		}
		if !s.models.HasModel(model) {
			return fmt.Errorf("%w: 路由槽位 %s 的模型 %s 不可用", ErrAssistantVersionInvalid, slot, model)
		}
	}
	return nil
}

// normalizeRouting 丢弃空值槽位，返回紧凑的 routing map。
func normalizeRouting(in map[string]string) map[string]string {
	out := map[string]string{}
	for slot, model := range in {
		if trimSpace(model) != "" {
			out[slot] = trimSpace(model)
		}
	}
	return out
}

// Create 创建一个新版本，revision 初始为 1。
func (s *AssistantVersionService) Create(ctx context.Context, principal auth.Principal, in AssistantVersionInput) (AssistantVersionResult, error) {
	if !auth.CanManageAssistantVersion(principal) {
		return AssistantVersionResult{}, ErrAssistantVersionDenied
	}
	if err := s.validateInput(in); err != nil {
		return AssistantVersionResult{}, err
	}
	if _, err := s.store.GetAssistantVersionByName(ctx, trimSpace(in.Name)); err == nil {
		return AssistantVersionResult{}, ErrAssistantVersionNameTaken
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return AssistantVersionResult{}, fmt.Errorf("查询版本名称失败: %w", err)
	}
	routingJSON, err := json.Marshal(normalizeRouting(in.Routing))
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("序列化 routing 失败: %w", err)
	}
	creator, _ := optionalUUID(principal.UserID)
	row, err := s.store.CreateAssistantVersion(ctx, sqlc.CreateAssistantVersionParams{
		Name:         trimSpace(in.Name),
		Description:  trimSpace(in.Description),
		SystemPrompt: in.SystemPrompt,
		ImageID:      in.ImageID,
		MainModel:    trimSpace(in.MainModel),
		RoutingJson:  routingJSON,
		SkillsJson:   []byte("[]"),
		CreatedBy:    creator,
	})
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("写入版本失败: %w", err)
	}
	return toAssistantVersionResult(row)
}
```

> `optionalUUID` 已存在于 service 包（persona_service.go 使用过）。若签名不符，按其实际签名调整。

- [ ] **Step 4：运行测试确认通过**

Run: `go test ./internal/service/ -run "AssistantVersionCreate" -v`
Expected: 全部 PASS。

- [ ] **Step 5：提交**

```bash
git add internal/service/assistant_version_service.go internal/service/assistant_version_service_test.go
git commit -m "feat(assistant-version): 版本 service 创建逻辑"
```

---

## Task 9：版本 service——Update 与 revision bump

**Files:**
- Modify: `internal/service/assistant_version_service.go`
- Modify: `internal/service/assistant_version_service_test.go`

- [ ] **Step 1：写 Update 失败测试**

在测试文件末尾追加：

```go
// seedVersion 在 fakeAVStore 内放一个已存在版本，返回其 id。
func seedVersion(store *fakeAVStore, name string, revision int32) string {
	id := mustUUID("00000000-0000-0000-0000-0000000000b1")
	v := sqlc.AssistantVersion{
		ID: id, Name: name, SystemPrompt: "p", ImageID: "v2026.5.16", MainModel: "qwen",
		RoutingJson: []byte("{}"), SkillsJson: []byte("[]"), Revision: revision,
	}
	store.versions[uuidToString(id)] = v
	store.byName[name] = v
	return uuidToString(id)
}

// TestAssistantVersionUpdateBumpsRevisionOnPromptChange 验证改提示词会 revision +1。
func TestAssistantVersionUpdateBumpsRevisionOnPromptChange(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 3)
	svc := newTestAVService(t, store)
	in := validCreateInput()
	in.Name = "标准版"
	in.SystemPrompt = "新的提示词"
	got, err := svc.Update(context.Background(), platformPrincipal(), id, in)
	require.NoError(t, err)
	assert.EqualValues(t, 4, got.Revision)
}

// TestAssistantVersionUpdateKeepsRevisionOnDescriptionOnly 验证只改描述不 bump revision。
func TestAssistantVersionUpdateKeepsRevisionOnDescriptionOnly(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 3)
	svc := newTestAVService(t, store)
	in := AssistantVersionInput{
		Name: "标准版", Description: "只改描述", SystemPrompt: "p",
		ImageID: "v2026.5.16", MainModel: "qwen", Routing: map[string]string{},
	}
	got, err := svc.Update(context.Background(), platformPrincipal(), id, in)
	require.NoError(t, err)
	assert.EqualValues(t, 3, got.Revision)
}

// TestAssistantVersionUpdateRejectsNameTakenByOther 验证改名撞到他人名称时报 NameTaken。
func TestAssistantVersionUpdateRejectsNameTakenByOther(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	store.byName["高级版"] = sqlc.AssistantVersion{ID: mustUUID("00000000-0000-0000-0000-0000000000c9"), Name: "高级版"}
	svc := newTestAVService(t, store)
	in := validCreateInput()
	in.Name = "高级版"
	_, err := svc.Update(context.Background(), platformPrincipal(), id, in)
	require.ErrorIs(t, err, ErrAssistantVersionNameTaken)
}
```

- [ ] **Step 2：运行测试确认失败**

Run: `go test ./internal/service/ -run "AssistantVersionUpdate" -v`
Expected: 编译失败——`Update` 未定义。

- [ ] **Step 3：实现 Update**

在 `internal/service/assistant_version_service.go` 末尾追加：

```go
// Update 编辑版本。仅当「影响容器」的字段变更时才把 revision +1：
// system_prompt / image_id / main_model / routing。name / description 变更不 bump。
func (s *AssistantVersionService) Update(ctx context.Context, principal auth.Principal, id string, in AssistantVersionInput) (AssistantVersionResult, error) {
	if !auth.CanManageAssistantVersion(principal) {
		return AssistantVersionResult{}, ErrAssistantVersionDenied
	}
	if err := s.validateInput(in); err != nil {
		return AssistantVersionResult{}, err
	}
	current, err := s.loadVersion(ctx, id)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	// 改名时确认新名称未被「其它」版本占用。
	newName := trimSpace(in.Name)
	if newName != current.Name {
		if existing, err := s.store.GetAssistantVersionByName(ctx, newName); err == nil {
			if uuidToString(existing.ID) != uuidToString(current.ID) {
				return AssistantVersionResult{}, ErrAssistantVersionNameTaken
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return AssistantVersionResult{}, fmt.Errorf("查询版本名称失败: %w", err)
		}
	}
	routingJSON, err := json.Marshal(normalizeRouting(in.Routing))
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("序列化 routing 失败: %w", err)
	}
	revision := current.Revision
	if containerAffectingChanged(current, in, routingJSON) {
		revision++
	}
	row, err := s.store.UpdateAssistantVersion(ctx, sqlc.UpdateAssistantVersionParams{
		ID:           current.ID,
		Name:         newName,
		Description:  trimSpace(in.Description),
		SystemPrompt: in.SystemPrompt,
		ImageID:      in.ImageID,
		MainModel:    trimSpace(in.MainModel),
		RoutingJson:  routingJSON,
		SkillsJson:   current.SkillsJson,
		Revision:     revision,
	})
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("更新版本失败: %w", err)
	}
	return toAssistantVersionResult(row)
}

// containerAffectingChanged 判断本次更新是否动了「影响容器」的字段。
// routingJSON 是已归一化序列化后的新 routing，与库中现值做字节比较。
func containerAffectingChanged(current sqlc.AssistantVersion, in AssistantVersionInput, routingJSON []byte) bool {
	if current.SystemPrompt != in.SystemPrompt {
		return true
	}
	if current.ImageID != in.ImageID {
		return true
	}
	if current.MainModel != trimSpace(in.MainModel) {
		return true
	}
	return !jsonEqual(current.RoutingJson, routingJSON)
}

// jsonEqual 比较两段 jsonb 字节在语义上是否相等（忽略 key 顺序与空白）。
func jsonEqual(a, b []byte) bool {
	var ma, mb map[string]string
	if err := json.Unmarshal(normalizeEmptyJSON(a), &ma); err != nil {
		return false
	}
	if err := json.Unmarshal(normalizeEmptyJSON(b), &mb); err != nil {
		return false
	}
	if len(ma) != len(mb) {
		return false
	}
	for k, v := range ma {
		if mb[k] != v {
			return false
		}
	}
	return true
}

// normalizeEmptyJSON 把空字节视作空对象，避免 json.Unmarshal 报错。
func normalizeEmptyJSON(b []byte) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}
```

- [ ] **Step 4：运行测试确认通过**

Run: `go test ./internal/service/ -run "AssistantVersionUpdate" -v`
Expected: 全部 PASS。

- [ ] **Step 5：提交**

```bash
git add internal/service/assistant_version_service.go internal/service/assistant_version_service_test.go
git commit -m "feat(assistant-version): 版本 service 更新与 revision bump"
```

---

## Task 10：版本 service——Delete 严格保护

**Files:**
- Modify: `internal/service/assistant_version_service.go`
- Modify: `internal/service/assistant_version_service_test.go`

- [ ] **Step 1：写 Delete 失败测试**

在测试文件末尾追加：

```go
// TestAssistantVersionDeleteOK 验证未被引用的版本可删除。
func TestAssistantVersionDeleteOK(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	svc := newTestAVService(t, store)
	err := svc.Delete(context.Background(), platformPrincipal(), id)
	require.NoError(t, err)
}

// TestAssistantVersionDeleteRejectsAppInUse 验证被实例引用时拒绝删除。
func TestAssistantVersionDeleteRejectsAppInUse(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	store.appCount = 1
	svc := newTestAVService(t, store)
	err := svc.Delete(context.Background(), platformPrincipal(), id)
	require.ErrorIs(t, err, ErrAssistantVersionInUse)
}

// TestAssistantVersionDeleteRejectsOrgInUse 验证出现在组织 allowlist 时拒绝删除。
func TestAssistantVersionDeleteRejectsOrgInUse(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	store.orgCount = 1
	svc := newTestAVService(t, store)
	err := svc.Delete(context.Background(), platformPrincipal(), id)
	require.ErrorIs(t, err, ErrAssistantVersionInUse)
}

// TestAssistantVersionDeleteDeniesOrgAdmin 验证组织管理员不能删除版本。
func TestAssistantVersionDeleteDeniesOrgAdmin(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	svc := newTestAVService(t, store)
	err := svc.Delete(context.Background(), orgAdminPrincipal(), id)
	require.ErrorIs(t, err, ErrAssistantVersionDenied)
}
```

- [ ] **Step 2：运行测试确认失败**

Run: `go test ./internal/service/ -run "AssistantVersionDelete" -v`
Expected: 编译失败——`Delete` 未定义。

- [ ] **Step 3：实现 Delete**

在 `internal/service/assistant_version_service.go` 末尾追加：

```go
// Delete 软删除一个版本。严格保护：被任何未删除组织 allowlist 或未删除实例
// 引用时拒绝删除，调用方需先迁移/删除引用方。
func (s *AssistantVersionService) Delete(ctx context.Context, principal auth.Principal, id string) error {
	if !auth.CanManageAssistantVersion(principal) {
		return ErrAssistantVersionDenied
	}
	row, err := s.loadVersion(ctx, id)
	if err != nil {
		return err
	}
	appCount, err := s.store.CountAppsUsingVersion(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("统计引用实例失败: %w", err)
	}
	if appCount > 0 {
		return fmt.Errorf("%w: 仍有 %d 个实例使用", ErrAssistantVersionInUse, appCount)
	}
	// jsonb_exists 的参数是裸字符串 id，不是 JSON 数组。
	orgCount, err := s.store.CountOrgsUsingVersion(ctx, []byte(uuidToString(row.ID)))
	if err != nil {
		return fmt.Errorf("统计引用组织失败: %w", err)
	}
	if orgCount > 0 {
		return fmt.Errorf("%w: 仍有 %d 个组织 allowlist 包含", ErrAssistantVersionInUse, orgCount)
	}
	if _, err := s.store.SoftDeleteAssistantVersion(ctx, row.ID); err != nil {
		return fmt.Errorf("删除版本失败: %w", err)
	}
	return nil
}
```

> 注意：`CountOrgsUsingVersion` 的 sqlc 参数类型——`jsonb_exists(jsonb, text)` 第二参为 `text`，sqlc 会生成 `string` 入参，而非 `[]byte`。Step 4 据实际生成签名校正。

- [ ] **Step 4：核对 sqlc 生成的 CountOrgsUsingVersion 参数类型**

打开 `internal/store/sqlc/assistant_versions.sql.go`，查看 `CountOrgsUsingVersion` 的参数类型：
- 若为 `string`：把 `AssistantVersionStore` 接口里 `CountOrgsUsingVersion` 第二参改为 `string`，`fakeAVStore` 同步改，service 内调用改为 `s.store.CountOrgsUsingVersion(ctx, uuidToString(row.ID))`。
- 若为 `[]byte`：保持不变。

改完后两边类型必须一致。

- [ ] **Step 5：运行测试确认通过**

Run: `go test ./internal/service/ -run "AssistantVersion" -v`
Expected: 全部 PASS。

- [ ] **Step 6：提交**

```bash
git add internal/service/assistant_version_service.go internal/service/assistant_version_service_test.go
git commit -m "feat(assistant-version): 版本 service 删除与严格保护"
```

---

## Task 11：版本 service——skill 上传与删除

**Files:**
- Modify: `internal/service/assistant_version_service.go`
- Modify: `internal/service/assistant_version_service_test.go`

- [ ] **Step 1：写 skill 失败测试**

在测试文件末尾追加：

```go
import_archive_helpers_note := "" // 占位，见下方说明
_ = import_archive_helpers_note
```

> 上面占位行不要写入文件；它只是提醒：本测试需要构造合法 skill tar。请在测试文件顶部 import 块补 `archive/tar`、`bytes`，并把下面的 `buildSkillTar` helper 加到文件末尾。

实际追加到测试文件末尾：

```go
// buildSkillTar 构造一个含合法 SKILL.md 的内存 tar。
func buildSkillTar(t *testing.T, skillName string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := "---\nname: " + skillName + "\ndescription: d\n---\n# t\n正文"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: skillName + "/SKILL.md", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
	}))
	_, err := tw.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// TestAssistantVersionUploadSkillOK 验证上传合法 skill tar 后 skills 增加且 revision +1。
func TestAssistantVersionUploadSkillOK(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 2)
	svc := newTestAVService(t, store)
	got, err := svc.UploadSkill(context.Background(), platformPrincipal(), id, buildSkillTar(t, "weather"))
	require.NoError(t, err)
	require.Len(t, got.Skills, 1)
	assert.Equal(t, "weather", got.Skills[0].Name)
	assert.EqualValues(t, 3, got.Revision)
}

// TestAssistantVersionUploadSkillRejectsDuplicateName 验证同版本内 skill 重名被拒。
func TestAssistantVersionUploadSkillRejectsDuplicateName(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	svc := newTestAVService(t, store)
	_, err := svc.UploadSkill(context.Background(), platformPrincipal(), id, buildSkillTar(t, "weather"))
	require.NoError(t, err)
	_, err = svc.UploadSkill(context.Background(), platformPrincipal(), id, buildSkillTar(t, "weather"))
	require.ErrorIs(t, err, ErrAssistantVersionInvalid)
}

// TestAssistantVersionUploadSkillRejectsTooLarge 验证超过大小上限被拒。
func TestAssistantVersionUploadSkillRejectsTooLarge(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	svc := NewAssistantVersionService(store, fakeImageResolver{}, fakeModelValidator{}, fakeBlobStore{}, 8)
	_, err := svc.UploadSkill(context.Background(), platformPrincipal(), id, buildSkillTar(t, "weather"))
	require.ErrorIs(t, err, ErrSkillTooLarge)
}

// TestAssistantVersionDeleteSkillOK 验证删除已存在 skill 后 skills 清空且 revision +1。
func TestAssistantVersionDeleteSkillOK(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	svc := newTestAVService(t, store)
	_, err := svc.UploadSkill(context.Background(), platformPrincipal(), id, buildSkillTar(t, "weather"))
	require.NoError(t, err)
	got, err := svc.DeleteSkill(context.Background(), platformPrincipal(), id, "weather")
	require.NoError(t, err)
	assert.Empty(t, got.Skills)
}
```

- [ ] **Step 2：运行测试确认失败**

Run: `go test ./internal/service/ -run "AssistantVersionUploadSkill|AssistantVersionDeleteSkill" -v`
Expected: 编译失败——`UploadSkill` / `DeleteSkill` 未定义。

- [ ] **Step 3：实现 UploadSkill / DeleteSkill**

在 `internal/service/assistant_version_service.go` 的 import 块补 `crypto/sha256`、`encoding/hex`，并补 import `"oc-manager/internal/integrations/hermes"`。在文件末尾追加：

```go
// UploadSkill 上传一个 skill tar：校验大小、合法性、推导名称，写文件系统主副本，
// 把元信息追加进 skills_json 并把 revision +1。
func (s *AssistantVersionService) UploadSkill(ctx context.Context, principal auth.Principal, id string, data []byte) (AssistantVersionResult, error) {
	if !auth.CanManageAssistantVersion(principal) {
		return AssistantVersionResult{}, ErrAssistantVersionDenied
	}
	if int64(len(data)) > s.maxSkillBytes {
		return AssistantVersionResult{}, fmt.Errorf("%w: 上限 %d 字节", ErrSkillTooLarge, s.maxSkillBytes)
	}
	row, err := s.loadVersion(ctx, id)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	info, err := hermes.InspectSkillArchive(bytesReader(data))
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("%w: %v", ErrAssistantVersionInvalid, err)
	}
	skills, err := decodeSkills(row.SkillsJson)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	for _, sk := range skills {
		if sk.Name == info.Name {
			return AssistantVersionResult{}, fmt.Errorf("%w: skill %s 已存在", ErrAssistantVersionInvalid, info.Name)
		}
	}
	relPath, err := s.blobs.PutSkill(uuidToString(row.ID), info.Name, data)
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("写入 skill tar 失败: %w", err)
	}
	sum := sha256.Sum256(data)
	skills = append(skills, AssistantVersionSkill{
		Name: info.Name, FilePath: relPath, FileSize: int64(len(data)), FileSha256: hex.EncodeToString(sum[:]),
	})
	return s.persistSkills(ctx, row, skills)
}

// DeleteSkill 从版本中删除一个 skill：删文件系统主副本、从 skills_json 移除、revision +1。
func (s *AssistantVersionService) DeleteSkill(ctx context.Context, principal auth.Principal, id, skillName string) (AssistantVersionResult, error) {
	if !auth.CanManageAssistantVersion(principal) {
		return AssistantVersionResult{}, ErrAssistantVersionDenied
	}
	row, err := s.loadVersion(ctx, id)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	skills, err := decodeSkills(row.SkillsJson)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	kept := make([]AssistantVersionSkill, 0, len(skills))
	var removed *AssistantVersionSkill
	for i := range skills {
		if skills[i].Name == skillName {
			removed = &skills[i]
			continue
		}
		kept = append(kept, skills[i])
	}
	if removed == nil {
		return AssistantVersionResult{}, fmt.Errorf("%w: skill %s 不存在", ErrAssistantVersionInvalid, skillName)
	}
	if err := s.blobs.DeleteSkill(removed.FilePath); err != nil {
		return AssistantVersionResult{}, fmt.Errorf("删除 skill tar 失败: %w", err)
	}
	return s.persistSkills(ctx, row, kept)
}

// persistSkills 把更新后的 skill 列表写库并把 revision +1（skill 变更属于容器相关变更）。
func (s *AssistantVersionService) persistSkills(ctx context.Context, row sqlc.AssistantVersion, skills []AssistantVersionSkill) (AssistantVersionResult, error) {
	skillsJSON, err := json.Marshal(skills)
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("序列化 skills 失败: %w", err)
	}
	updated, err := s.store.UpdateAssistantVersionSkills(ctx, sqlc.UpdateAssistantVersionSkillsParams{
		ID:         row.ID,
		SkillsJson: skillsJSON,
		Revision:   row.Revision + 1,
	})
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("更新版本 skill 失败: %w", err)
	}
	return toAssistantVersionResult(updated)
}

// bytesReader 把字节切片包成 io.Reader，避免在文件头部额外 import bytes。
func bytesReader(b []byte) *bytesReaderT { return &bytesReaderT{b: b} }

// bytesReaderT 是极简只读 reader。
type bytesReaderT struct {
	b   []byte
	pos int
}

func (r *bytesReaderT) Read(p []byte) (int, error) {
	if r.pos >= len(r.b) {
		return 0, ioEOF
	}
	n := copy(p, r.b[r.pos:])
	r.pos += n
	return n, nil
}
```

并在文件顶部 import 块加入 `"io"`，把 `ioEOF` 替换为直接用 `io.EOF`（即 `bytesReaderT.Read` 返回 `io.EOF`，删除 `ioEOF` 占位）。或更简单：import `"bytes"`，把 `bytesReader(data)` 改成 `bytes.NewReader(data)` 并删除 `bytesReaderT` 整段——**推荐后者**，代码更短。

> 实施提示：直接 import `"bytes"`、用 `bytes.NewReader(data)`，删掉 `bytesReader` / `bytesReaderT` / `ioEOF`。

- [ ] **Step 4：运行测试确认通过**

Run: `go test ./internal/service/ -run "AssistantVersion" -v`
Expected: 全部 PASS。

- [ ] **Step 5：提交**

```bash
git add internal/service/assistant_version_service.go internal/service/assistant_version_service_test.go
git commit -m "feat(assistant-version): 版本 service skill 上传与删除"
```

---

## Task 12：skill 文件系统主副本存储实现

**Files:**
- Create: `internal/service/skill_blob_store.go`
- Create: `internal/service/skill_blob_store_test.go`

`SkillBlobStore` 接口在 Task 7 定义，本任务给出基于本地文件系统的生产实现。

- [ ] **Step 1：写失败测试**

创建 `internal/service/skill_blob_store_test.go`：

```go
package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFSSkillBlobStorePutAndDelete 验证写入后文件存在、删除后文件消失。
func TestFSSkillBlobStorePutAndDelete(t *testing.T) {
	root := t.TempDir()
	bs := NewFSSkillBlobStore(root)
	rel, err := bs.PutSkill("ver-1", "weather", []byte("tar-bytes"))
	require.NoError(t, err)
	assert.Equal(t, filepath.ToSlash("versions/ver-1/skills/weather.tar"), rel)
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	assert.Equal(t, "tar-bytes", string(content))
	require.NoError(t, bs.DeleteSkill(rel))
	_, err = os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	assert.True(t, os.IsNotExist(err))
}

// TestFSSkillBlobStoreRejectsUnsafeName 验证 skill 名含路径分隔符时被拒。
func TestFSSkillBlobStoreRejectsUnsafeName(t *testing.T) {
	bs := NewFSSkillBlobStore(t.TempDir())
	_, err := bs.PutSkill("ver-1", "../evil", []byte("x"))
	require.Error(t, err)
}
```

- [ ] **Step 2：运行测试确认失败**

Run: `go test ./internal/service/ -run FSSkillBlobStore -v`
Expected: 编译失败——`NewFSSkillBlobStore` 未定义。

- [ ] **Step 3：写实现**

创建 `internal/service/skill_blob_store.go`：

```go
package service

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// FSSkillBlobStore 把 skill tar 存到 manager 本地数据根目录的
// versions/<versionID>/skills/<name>.tar，作为 manager 端主副本。
type FSSkillBlobStore struct {
	// root 是 manager 数据根目录（cfg.App.DataRoot）。
	root string
}

// NewFSSkillBlobStore 创建基于文件系统的 skill 主副本存储。
func NewFSSkillBlobStore(root string) *FSSkillBlobStore {
	return &FSSkillBlobStore{root: root}
}

// safeSegment 校验单个路径段不含分隔符 / .. 等危险字符。
func safeSegment(s string) error {
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\") {
		return fmt.Errorf("非法路径段: %q", s)
	}
	return nil
}

// PutSkill 写入一个 skill tar，返回相对 root 的 '/' 分隔路径。
func (s *FSSkillBlobStore) PutSkill(versionID, skillName string, data []byte) (string, error) {
	if err := safeSegment(versionID); err != nil {
		return "", err
	}
	if err := safeSegment(skillName); err != nil {
		return "", err
	}
	rel := path.Join("versions", versionID, "skills", skillName+".tar")
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("创建 skill 目录失败: %w", err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return "", fmt.Errorf("写入 skill tar 失败: %w", err)
	}
	return rel, nil
}

// DeleteSkill 删除一个 skill tar；文件不存在视为成功。
func (s *FSSkillBlobStore) DeleteSkill(relPath string) error {
	abs := filepath.Join(s.root, filepath.FromSlash(relPath))
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除 skill tar 失败: %w", err)
	}
	return nil
}
```

- [ ] **Step 4：运行测试确认通过**

Run: `go test ./internal/service/ -run FSSkillBlobStore -v`
Expected: 全部 PASS。

- [ ] **Step 5：提交**

```bash
git add internal/service/skill_blob_store.go internal/service/skill_blob_store_test.go
git commit -m "feat(assistant-version): skill tar 文件系统主副本存储"
```

---

## Task 13：HTTP handler、DTO 与路由

**Files:**
- Modify: `internal/api/handlers/dto.go`
- Create: `internal/api/handlers/assistant_versions.go`
- Create: `internal/api/handlers/assistant_versions_test.go`

- [ ] **Step 1：新增 DTO**

在 `internal/api/handlers/dto.go` 末尾追加：

```go
// AssistantVersionRoutingDTO 是智能路由 8 槽位的请求结构；空字符串表示走主模型。
type AssistantVersionRoutingDTO struct {
	Vision          string `json:"vision"`
	Compression     string `json:"compression"`
	WebExtract      string `json:"web_extract"`
	SessionSearch   string `json:"session_search"`
	TitleGeneration string `json:"title_generation"`
	Approval        string `json:"approval"`
	SkillsHub       string `json:"skills_hub"`
	Mcp             string `json:"mcp"`
}

// CreateAssistantVersionRequest 是创建助手版本的请求体。
type CreateAssistantVersionRequest struct {
	Name         string                     `json:"name" binding:"required"`
	Description  string                     `json:"description"`
	SystemPrompt string                     `json:"system_prompt" binding:"required"`
	ImageID      string                     `json:"image_id" binding:"required"`
	MainModel    string                     `json:"main_model" binding:"required"`
	Routing      AssistantVersionRoutingDTO `json:"routing"`
}

// UpdateAssistantVersionRequest 是编辑助手版本的请求体，字段同创建。
type UpdateAssistantVersionRequest struct {
	Name         string                     `json:"name" binding:"required"`
	Description  string                     `json:"description"`
	SystemPrompt string                     `json:"system_prompt" binding:"required"`
	ImageID      string                     `json:"image_id" binding:"required"`
	MainModel    string                     `json:"main_model" binding:"required"`
	Routing      AssistantVersionRoutingDTO `json:"routing"`
}
```

- [ ] **Step 2：写 handler 失败测试**

创建 `internal/api/handlers/assistant_versions_test.go`：

```go
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// avServiceStub 是 assistantVersionService 接口的内存桩。
type avServiceStub struct {
	list       []service.AssistantVersionResult
	one        service.AssistantVersionResult
	err        error
	images     []service.RuntimeImageOption
}

func (s *avServiceStub) List(context.Context, auth.Principal) ([]service.AssistantVersionResult, error) {
	return s.list, s.err
}

func (s *avServiceStub) Get(context.Context, auth.Principal, string) (service.AssistantVersionResult, error) {
	return s.one, s.err
}

func (s *avServiceStub) Create(context.Context, auth.Principal, service.AssistantVersionInput) (service.AssistantVersionResult, error) {
	return s.one, s.err
}

func (s *avServiceStub) Update(context.Context, auth.Principal, string, service.AssistantVersionInput) (service.AssistantVersionResult, error) {
	return s.one, s.err
}

func (s *avServiceStub) Delete(context.Context, auth.Principal, string) error { return s.err }

func (s *avServiceStub) UploadSkill(context.Context, auth.Principal, string, []byte) (service.AssistantVersionResult, error) {
	return s.one, s.err
}

func (s *avServiceStub) DeleteSkill(context.Context, auth.Principal, string, string) (service.AssistantVersionResult, error) {
	return s.one, s.err
}

func (s *avServiceStub) ListRuntimeImages(context.Context, auth.Principal) ([]service.RuntimeImageOption, error) {
	return s.images, s.err
}

func newAVTestRouter(t *testing.T, svc assistantVersionService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterAssistantVersionRoutes(router, NewAssistantVersionsHandler(svc))
	return router
}

// TestAVListReturnsVersions 验证平台管理员可列出版本。
func TestAVListReturnsVersions(t *testing.T) {
	svc := &avServiceStub{list: []service.AssistantVersionResult{{ID: "v1", Name: "标准版"}}}
	router := newAVTestRouter(t, svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/assistant-versions", nil)
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), "标准版")
}

// TestAVCreateReturns201 验证创建版本返回 201。
func TestAVCreateReturns201(t *testing.T) {
	svc := &avServiceStub{one: service.AssistantVersionResult{ID: "v1", Name: "标准版"}}
	router := newAVTestRouter(t, svc)
	body, _ := json.Marshal(CreateAssistantVersionRequest{
		Name: "标准版", SystemPrompt: "p", ImageID: "v2026.5.16", MainModel: "qwen",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assistant-versions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusCreated, resp.Code)
}

// TestAVCreateMapsDenied 验证 service 返回 Denied 时映射 403。
func TestAVCreateMapsDenied(t *testing.T) {
	svc := &avServiceStub{err: service.ErrAssistantVersionDenied}
	router := newAVTestRouter(t, svc)
	body, _ := json.Marshal(CreateAssistantVersionRequest{
		Name: "标准版", SystemPrompt: "p", ImageID: "v2026.5.16", MainModel: "qwen",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assistant-versions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{Role: domain.UserRoleOrgAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusForbidden, resp.Code)
}

// TestAVDeleteMapsInUse 验证 service 返回 InUse 时映射 409。
func TestAVDeleteMapsInUse(t *testing.T) {
	svc := &avServiceStub{err: service.ErrAssistantVersionInUse}
	router := newAVTestRouter(t, svc)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/assistant-versions/v1", nil)
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusConflict, resp.Code)
}

// TestAVListRuntimeImages 验证镜像列表端点返回配置镜像。
func TestAVListRuntimeImages(t *testing.T) {
	svc := &avServiceStub{images: []service.RuntimeImageOption{{ID: "v2026.5.16", Label: "当前"}}}
	router := newAVTestRouter(t, svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtime-images", nil)
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), "v2026.5.16")
}
```

> `withPrincipal` 是 handlers 测试包已有 helper（见 `models_test.go` 用法）。

- [ ] **Step 3：运行测试确认失败**

Run: `go test ./internal/api/handlers/ -run "TestAV" -v`
Expected: 编译失败——handler 类型与 service 类型未定义。

- [ ] **Step 4：在 service 包补 RuntimeImageOption 与 ListRuntimeImages**

在 `internal/service/assistant_version_service.go` 末尾追加：

```go
// RuntimeImageOption 是暴露给前端镜像 select 的单个选项。
type RuntimeImageOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// AssistantVersionImageLister 抽象「列出全部可选镜像」的能力。
type AssistantVersionImageLister interface {
	ListRuntimeImages() []RuntimeImageOption
}
```

把 `AssistantVersionService` 结构体的 `images` 字段类型从 `AssistantVersionImageResolver` 改为同时满足两个接口的组合——直接新增一个字段更简单。把结构体字段与构造函数改为：

```go
// 结构体内把 images 字段类型保持 AssistantVersionImageResolver 不变，
// 另加 imageLister 字段：
//	imageLister AssistantVersionImageLister
```

并在 `NewAssistantVersionService` 形参表把 `images AssistantVersionImageResolver` 替换为单一参数 `images assistantVersionImages`，其中：

```go
// assistantVersionImages 合并镜像校验与列举能力，由配置适配器统一实现。
type assistantVersionImages interface {
	AssistantVersionImageResolver
	AssistantVersionImageLister
}
```

构造函数签名改为 `images assistantVersionImages`，结构体 `images` 字段类型改为 `assistantVersionImages`，删除单独的 `AssistantVersionImageResolver` 字段引用处不变（接口兼容）。然后追加方法：

```go
// ListRuntimeImages 返回全部可选镜像，供前端版本编辑表单的镜像 select 使用。
func (s *AssistantVersionService) ListRuntimeImages(_ context.Context, principal auth.Principal) ([]RuntimeImageOption, error) {
	if !auth.CanViewAssistantVersion(principal) {
		return nil, ErrAssistantVersionDenied
	}
	return s.images.ListRuntimeImages(), nil
}
```

同步把 Task 7 测试里的 `fakeImageResolver` 改名/补充为同时实现 `ListRuntimeImages`：

```go
func (fakeImageResolver) ListRuntimeImages() []RuntimeImageOption {
	return []RuntimeImageOption{{ID: "v2026.5.16", Label: "当前"}}
}
```

`rejectingImageResolver` 同样补一个返回空切片的 `ListRuntimeImages`。

- [ ] **Step 5：写 handler 实现**

创建 `internal/api/handlers/assistant_versions.go`：

```go
package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// maxSkillUploadBytes 是 skill tar multipart 上传的硬上限（与 service 层 10 MiB 对齐，留少量余量）。
const maxSkillUploadBytes = 12 << 20

// assistantVersionService 是 handler 依赖的版本 service 能力集合。
type assistantVersionService interface {
	List(ctx context.Context, principal auth.Principal) ([]service.AssistantVersionResult, error)
	Get(ctx context.Context, principal auth.Principal, id string) (service.AssistantVersionResult, error)
	Create(ctx context.Context, principal auth.Principal, in service.AssistantVersionInput) (service.AssistantVersionResult, error)
	Update(ctx context.Context, principal auth.Principal, id string, in service.AssistantVersionInput) (service.AssistantVersionResult, error)
	Delete(ctx context.Context, principal auth.Principal, id string) error
	UploadSkill(ctx context.Context, principal auth.Principal, id string, data []byte) (service.AssistantVersionResult, error)
	DeleteSkill(ctx context.Context, principal auth.Principal, id, skillName string) (service.AssistantVersionResult, error)
	ListRuntimeImages(ctx context.Context, principal auth.Principal) ([]service.RuntimeImageOption, error)
}

// AssistantVersionsHandler 暴露助手版本目录的 HTTP 接口。
type AssistantVersionsHandler struct {
	service assistantVersionService
}

// NewAssistantVersionsHandler 创建版本 handler。
func NewAssistantVersionsHandler(svc assistantVersionService) *AssistantVersionsHandler {
	return &AssistantVersionsHandler{service: svc}
}

// RegisterAssistantVersionRoutes 注册助手版本与镜像列表路由。
func RegisterAssistantVersionRoutes(router gin.IRouter, h *AssistantVersionsHandler) {
	router.GET("/api/v1/assistant-versions", h.List)
	router.POST("/api/v1/assistant-versions", h.Create)
	router.GET("/api/v1/assistant-versions/:id", h.Get)
	router.PUT("/api/v1/assistant-versions/:id", h.Update)
	router.DELETE("/api/v1/assistant-versions/:id", h.Delete)
	router.POST("/api/v1/assistant-versions/:id/skills", h.UploadSkill)
	router.DELETE("/api/v1/assistant-versions/:id/skills/:skill", h.DeleteSkill)
	router.GET("/api/v1/runtime-images", h.ListRuntimeImages)
}

// writeAVError 把 service 哨兵错误映射成 HTTP 响应。
func writeAVError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrAssistantVersionDenied):
		c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", "无权操作助手版本"))
	case errors.Is(err, service.ErrAssistantVersionNotFound):
		c.JSON(http.StatusNotFound, apierror.New("ASSISTANT_VERSION_NOT_FOUND", "助手版本不存在"))
	case errors.Is(err, service.ErrAssistantVersionNameTaken):
		c.JSON(http.StatusConflict, apierror.New("ASSISTANT_VERSION_NAME_TAKEN", "助手版本名称已存在"))
	case errors.Is(err, service.ErrAssistantVersionInUse):
		c.JSON(http.StatusConflict, apierror.New("ASSISTANT_VERSION_IN_USE", "助手版本正被引用，不可删除"))
	case errors.Is(err, service.ErrSkillTooLarge):
		c.JSON(http.StatusRequestEntityTooLarge, apierror.New("SKILL_TOO_LARGE", "skill 包超过大小上限"))
	case errors.Is(err, service.ErrAssistantVersionInvalid):
		c.JSON(http.StatusBadRequest, apierror.New("ASSISTANT_VERSION_INVALID", err.Error()))
	default:
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "操作助手版本失败"))
	}
}

// routingToMap 把 8 槽位 DTO 转成 service 需要的 map（空值槽位也带上，service 内归一化）。
func routingToMap(d AssistantVersionRoutingDTO) map[string]string {
	return map[string]string{
		"vision": d.Vision, "compression": d.Compression, "web_extract": d.WebExtract,
		"session_search": d.SessionSearch, "title_generation": d.TitleGeneration,
		"approval": d.Approval, "skills_hub": d.SkillsHub, "mcp": d.Mcp,
	}
}

// List 返回全部助手版本。
//
// @Summary  助手版本列表
// @Tags     assistant-versions
// @Produce  json
// @Security BearerAuth
// @Success  200 {object} map[string][]service.AssistantVersionResult
// @Failure  403 {object} ErrorResponse
// @Router   /assistant-versions [get]
func (h *AssistantVersionsHandler) List(c *gin.Context) {
	out, err := h.service.List(c.Request.Context(), principalFromCtx(c))
	if err != nil {
		writeAVError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"versions": out})
}

// Get 返回单个助手版本。
//
// @Summary  助手版本详情
// @Tags     assistant-versions
// @Produce  json
// @Security BearerAuth
// @Param    id path string true "版本 ID"
// @Success  200 {object} map[string]service.AssistantVersionResult
// @Failure  404 {object} ErrorResponse
// @Router   /assistant-versions/{id} [get]
func (h *AssistantVersionsHandler) Get(c *gin.Context) {
	out, err := h.service.Get(c.Request.Context(), principalFromCtx(c), c.Param("id"))
	if err != nil {
		writeAVError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": out})
}

// Create 创建助手版本。
//
// @Summary  创建助手版本
// @Tags     assistant-versions
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    body body CreateAssistantVersionRequest true "版本"
// @Success  201 {object} map[string]service.AssistantVersionResult
// @Failure  400 {object} ErrorResponse
// @Failure  403 {object} ErrorResponse
// @Router   /assistant-versions [post]
func (h *AssistantVersionsHandler) Create(c *gin.Context) {
	var req CreateAssistantVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "请求体格式错误"))
		return
	}
	out, err := h.service.Create(c.Request.Context(), principalFromCtx(c), service.AssistantVersionInput{
		Name: req.Name, Description: req.Description, SystemPrompt: req.SystemPrompt,
		ImageID: req.ImageID, MainModel: req.MainModel, Routing: routingToMap(req.Routing),
	})
	if err != nil {
		writeAVError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"version": out})
}

// Update 编辑助手版本。
//
// @Summary  编辑助手版本
// @Tags     assistant-versions
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    id   path string                        true "版本 ID"
// @Param    body body UpdateAssistantVersionRequest true "版本"
// @Success  200 {object} map[string]service.AssistantVersionResult
// @Failure  400 {object} ErrorResponse
// @Router   /assistant-versions/{id} [put]
func (h *AssistantVersionsHandler) Update(c *gin.Context) {
	var req UpdateAssistantVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "请求体格式错误"))
		return
	}
	out, err := h.service.Update(c.Request.Context(), principalFromCtx(c), c.Param("id"), service.AssistantVersionInput{
		Name: req.Name, Description: req.Description, SystemPrompt: req.SystemPrompt,
		ImageID: req.ImageID, MainModel: req.MainModel, Routing: routingToMap(req.Routing),
	})
	if err != nil {
		writeAVError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": out})
}

// Delete 删除助手版本。
//
// @Summary  删除助手版本
// @Tags     assistant-versions
// @Produce  json
// @Security BearerAuth
// @Param    id path string true "版本 ID"
// @Success  204 "已删除"
// @Failure  409 {object} ErrorResponse
// @Router   /assistant-versions/{id} [delete]
func (h *AssistantVersionsHandler) Delete(c *gin.Context) {
	if err := h.service.Delete(c.Request.Context(), principalFromCtx(c), c.Param("id")); err != nil {
		writeAVError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// UploadSkill 上传一个 skill tar。
//
// @Summary  上传版本 skill
// @Tags     assistant-versions
// @Accept   multipart/form-data
// @Produce  json
// @Security BearerAuth
// @Param    id   path     string true "版本 ID"
// @Param    file formData file   true "skill tar 包"
// @Success  200 {object} map[string]service.AssistantVersionResult
// @Failure  400 {object} ErrorResponse
// @Failure  413 {object} ErrorResponse
// @Router   /assistant-versions/{id}/skills [post]
func (h *AssistantVersionsHandler) UploadSkill(c *gin.Context) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "缺少 file 表单字段"))
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "无法读取上传文件"))
		return
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxSkillUploadBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "读取上传文件失败"))
		return
	}
	out, err := h.service.UploadSkill(c.Request.Context(), principalFromCtx(c), c.Param("id"), data)
	if err != nil {
		writeAVError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": out})
}

// DeleteSkill 删除版本下的一个 skill。
//
// @Summary  删除版本 skill
// @Tags     assistant-versions
// @Produce  json
// @Security BearerAuth
// @Param    id    path string true "版本 ID"
// @Param    skill path string true "skill 名称"
// @Success  200 {object} map[string]service.AssistantVersionResult
// @Failure  400 {object} ErrorResponse
// @Router   /assistant-versions/{id}/skills/{skill} [delete]
func (h *AssistantVersionsHandler) DeleteSkill(c *gin.Context) {
	out, err := h.service.DeleteSkill(c.Request.Context(), principalFromCtx(c), c.Param("id"), c.Param("skill"))
	if err != nil {
		writeAVError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": out})
}

// ListRuntimeImages 返回配置文件中的可选 Hermes 镜像。
//
// @Summary  可选 Hermes 镜像列表
// @Tags     assistant-versions
// @Produce  json
// @Security BearerAuth
// @Success  200 {object} map[string][]service.RuntimeImageOption
// @Failure  403 {object} ErrorResponse
// @Router   /runtime-images [get]
func (h *AssistantVersionsHandler) ListRuntimeImages(c *gin.Context) {
	out, err := h.service.ListRuntimeImages(c.Request.Context(), principalFromCtx(c))
	if err != nil {
		writeAVError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"images": out})
}
```

> `principalFromCtx`、`apierror.New` 是 handlers 包已有原语（见 `models.go`）。

- [ ] **Step 6：运行测试确认通过**

Run: `go test ./internal/api/handlers/ -run "TestAV" -v` 再跑 `go test ./internal/service/ -run AssistantVersion -v`
Expected: 两组测试全部 PASS（service 测试因 Step 4 改了构造函数与桩，需一并通过）。

- [ ] **Step 7：提交**

```bash
git add internal/api/handlers/dto.go internal/api/handlers/assistant_versions.go internal/api/handlers/assistant_versions_test.go internal/service/assistant_version_service.go internal/service/assistant_version_service_test.go
git commit -m "feat(assistant-version): 版本 HTTP handler 与路由"
```

---

## Task 14：路由装配与 cmd/server 接线

**Files:**
- Modify: `internal/api/router.go`
- Modify: `cmd/server/main.go`
- Create: `internal/config/assistant_version_adapter.go`

- [ ] **Step 1：在 router Dependencies 增字段**

在 `internal/api/router.go` 的 `Dependencies` 结构体内、`PersonaService` 字段附近新增：

```go
	// AssistantVersionService 提供助手版本目录管理路由。
	AssistantVersionService *service.AssistantVersionService
```

- [ ] **Step 2：在 router 装配路由**

在 `NewRouter` 内、`PersonaService` 装配块附近新增：

```go
	if dep.AssistantVersionService != nil {
		handlers.RegisterAssistantVersionRoutes(user, handlers.NewAssistantVersionsHandler(dep.AssistantVersionService))
	}
```

- [ ] **Step 3：写镜像/模型适配器**

`AssistantVersionService` 需要 `assistantVersionImages`（镜像校验+列举）与 `AssistantVersionModelValidator`（模型校验）。镜像来自配置，模型来自 `ModelCatalogService`。创建 `internal/config/assistant_version_adapter.go`：

```go
package config

import "oc-manager/internal/service"

// RuntimeImageAdapter 用配置文件的镜像列表实现 service 需要的镜像校验与列举能力。
type RuntimeImageAdapter struct {
	images []RuntimeImageConfig
}

// NewRuntimeImageAdapter 用 HermesConfig.RuntimeImages 构造适配器。
func NewRuntimeImageAdapter(images []RuntimeImageConfig) *RuntimeImageAdapter {
	return &RuntimeImageAdapter{images: images}
}

// HasRuntimeImage 判断 image_id 是否存在于配置。
func (a *RuntimeImageAdapter) HasRuntimeImage(id string) bool {
	_, ok := ResolveRuntimeImage(a.images, id)
	return ok
}

// ListRuntimeImages 返回全部镜像选项（仅 id + label）。
func (a *RuntimeImageAdapter) ListRuntimeImages() []service.RuntimeImageOption {
	out := make([]service.RuntimeImageOption, 0, len(a.images))
	for _, img := range a.images {
		out = append(out, service.RuntimeImageOption{ID: img.ID, Label: img.Label})
	}
	return out
}
```

> 若 `internal/config` 反向依赖 `internal/service` 会引入循环依赖，则改为把适配器放在 `cmd/server` 包内（main.go 同文件定义一个本地类型）。实施时先尝试本文件；`go build` 报循环依赖就移到 `cmd/server`。

- [ ] **Step 4：写模型校验适配器**

`ModelCatalogService` 有 `ValidateModelIDs`，但 service 需要单模型布尔校验 `HasModel`。在 `internal/service/model_service.go` 末尾新增方法：

```go
// HasModelInCatalog 判断单个模型是否存在于 new-api 实时模型列表。
// 供助手版本 service 校验主模型与路由模型；查询失败时保守返回 false。
func (s *ModelCatalogService) HasModelInCatalog(ctx context.Context, id string) bool {
	models, err := s.list(ctx)
	if err != nil {
		return false
	}
	for _, m := range models {
		if m.ID == id {
			return true
		}
	}
	return false
}
```

但 `AssistantVersionModelValidator.HasModel(id string) bool` 无 ctx。改为在 `cmd/server` 用一个轻量 wrapper 适配。在 `cmd/server/main.go` 内（或新建 `cmd/server/assistant_version_wiring.go`）定义：

```go
// modelValidatorAdapter 把 ModelCatalogService 适配成无 ctx 的 HasModel。
type modelValidatorAdapter struct {
	catalog *service.ModelCatalogService
}

func (a modelValidatorAdapter) HasModel(id string) bool {
	return a.catalog.HasModelInCatalog(context.Background(), id)
}
```

- [ ] **Step 5：在 cmd/server/main.go 构造 service 并注入**

在 `cmd/server/main.go` 构造 `modelCatalogService` 之后、装配 `Dependencies` 之前，新增：

```go
	// 助手版本 service：镜像来自配置、模型校验走 new-api 目录、skill tar 存数据根目录。
	var assistantVersionService *service.AssistantVersionService
	if modelCatalogService != nil {
		assistantVersionService = service.NewAssistantVersionService(
			store.NewAssistantVersionStore(dbStore),
			config.NewRuntimeImageAdapter(cfg.Hermes.RuntimeImages),
			modelValidatorAdapter{catalog: modelCatalogService},
			service.NewFSSkillBlobStore(cfg.App.DataRoot),
			0,
		)
	}
```

并在 `Dependencies{...}` 字面量里加一行：

```go
			AssistantVersionService: assistantVersionService,
```

- [ ] **Step 6：实现 store.NewAssistantVersionStore**

service 的 `AssistantVersionStore` 接口需要一个 store 适配器。参照 `internal/store/persona_store.go` 的写法，创建 `internal/store/assistant_version_store.go`：

```go
package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// AssistantVersionStore 用 sqlc.Queries 实现 service.AssistantVersionStore。
type AssistantVersionStore struct {
	q *sqlc.Queries
}

// NewAssistantVersionStore 创建助手版本 store 适配器。
func NewAssistantVersionStore(s *Store) *AssistantVersionStore {
	return &AssistantVersionStore{q: s.Queries}
}

func (s *AssistantVersionStore) GetAssistantVersion(ctx context.Context, id pgtype.UUID) (sqlc.AssistantVersion, error) {
	return s.q.GetAssistantVersion(ctx, id)
}

func (s *AssistantVersionStore) GetAssistantVersionByName(ctx context.Context, name string) (sqlc.AssistantVersion, error) {
	return s.q.GetAssistantVersionByName(ctx, name)
}

func (s *AssistantVersionStore) ListAssistantVersions(ctx context.Context) ([]sqlc.AssistantVersion, error) {
	return s.q.ListAssistantVersions(ctx)
}

func (s *AssistantVersionStore) CreateAssistantVersion(ctx context.Context, arg sqlc.CreateAssistantVersionParams) (sqlc.AssistantVersion, error) {
	return s.q.CreateAssistantVersion(ctx, arg)
}

func (s *AssistantVersionStore) UpdateAssistantVersion(ctx context.Context, arg sqlc.UpdateAssistantVersionParams) (sqlc.AssistantVersion, error) {
	return s.q.UpdateAssistantVersion(ctx, arg)
}

func (s *AssistantVersionStore) UpdateAssistantVersionSkills(ctx context.Context, arg sqlc.UpdateAssistantVersionSkillsParams) (sqlc.AssistantVersion, error) {
	return s.q.UpdateAssistantVersionSkills(ctx, arg)
}

func (s *AssistantVersionStore) SoftDeleteAssistantVersion(ctx context.Context, id pgtype.UUID) (sqlc.AssistantVersion, error) {
	return s.q.SoftDeleteAssistantVersion(ctx, id)
}

func (s *AssistantVersionStore) CountAppsUsingVersion(ctx context.Context, id pgtype.UUID) (int64, error) {
	return s.q.CountAppsUsingVersion(ctx, id)
}

// CountOrgsUsingVersion 第二参类型须与 sqlc 生成签名一致（见 Task 10 Step 4）。
func (s *AssistantVersionStore) CountOrgsUsingVersion(ctx context.Context, idJSON []byte) (int64, error) {
	return s.q.CountOrgsUsingVersion(ctx, idJSON)
}

var _ service.AssistantVersionStore = (*AssistantVersionStore)(nil)
```

> `CountOrgsUsingVersion` 的入参类型按 Task 10 Step 4 的核对结果对齐（`[]byte` 或 `string`）。`AssistantVersionStore` 接口、`AssistantVersionStore` 适配器、`fakeAVStore` 三处签名必须完全一致。

- [ ] **Step 7：构建并跑全量测试**

Run: `go build ./... && make vet`
Expected: 编译通过、vet 无报错。
Run: `go test ./internal/... ./cmd/...`
Expected: 全部 PASS。

- [ ] **Step 8：提交**

```bash
git add internal/api/router.go cmd/server/ internal/config/assistant_version_adapter.go internal/store/assistant_version_store.go internal/service/model_service.go
git commit -m "feat(assistant-version): 装配版本路由与服务接线"
```

---

## Task 15：OpenAPI 与前端类型同步

**Files:**
- Modify: `openapi/openapi.yaml`（生成产物）
- Modify: `web/src/api/generated.ts`（生成产物）

- [ ] **Step 1：生成 OpenAPI**

Run: `make openapi-gen`
Expected: `openapi/openapi.yaml` 更新，包含 `/assistant-versions`、`/runtime-images` 等路径。

- [ ] **Step 2：生成前端类型**

Run: `make web-types-gen`
Expected: `web/src/api/generated.ts` 更新，含助手版本相关类型。

- [ ] **Step 3：校验同步**

Run: `make openapi-check`
Expected: 跑完后 `git status` 工作区干净（无未提交的 yaml 漂移）。

- [ ] **Step 4：提交**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(assistant-version): 同步 OpenAPI 与前端类型"
```

---

## Task 16：手工冒烟验证

**Files:** 无（验证任务）

- [ ] **Step 1：启动本地环境**

Run: `make dev-up`（按仓库现有本地开发流程）
确认 manager、postgres、new-api 容器就绪。

- [ ] **Step 2：迁移到最新**

Run: `make migrate-up`
Expected: `000023` 迁移已应用。

- [ ] **Step 3：用平台管理员账号验证 API**

以平台管理员（组织标识留空，`admin` / `admin123`）登录拿 token，依次验证：
- `GET /api/v1/runtime-images` 返回配置中的两个镜像。
- `POST /api/v1/assistant-versions` 创建一个版本（用真实 new-api 模型名）→ 201。
- `GET /api/v1/assistant-versions` 列表含该版本，`revision=1`。
- `PUT` 改 `system_prompt` → `revision=2`；只改 `description` → `revision` 不变。
- `POST /api/v1/assistant-versions/:id/skills` 上传一个含合法 `SKILL.md` 的 tar → 返回版本含该 skill。
- `DELETE /api/v1/assistant-versions/:id` → 204（无引用时）。

- [ ] **Step 4：记录验证结果**

把验证命令与响应摘要写入交付说明。如发现问题，回到对应 Task 修复并重跑相关单测。

> 说明：本计划只交付后端 API，无前端页面，按 spec 要求的「真实浏览器验证」在前端版本管理页计划（后续）中执行。

---

## Self-Review

**Spec 覆盖（针对 spec §11 Phase 1 的后端范围）：**
- §3.1 `assistant_versions` 表 → Task 1。
- §3.2 / §3.3 关联列（additive）→ Task 1（drop 旧表/列属 Phase 5，本计划不含）。
- §3.5 `revision` bump 规则 → Task 9（容器相关字段）+ Task 11（skill 变更）。
- §4 配置镜像列表 + 只读 API → Task 3 + Task 13（`/runtime-images`）。
- §7.1 版本 service / handler、skill 上传校验、模型校验、image_id 校验、严格保护 → Task 7–14。
- §7.2 权限谓词 → Task 4。
- §9 OpenAPI 同步 → Task 15。
- §10 测试要点（CRUD、唯一名、模型校验、revision、严格保护、skill tar 校验）→ Task 5、8–13 的单测。

**不在本计划（后续 Phase）：** 前端版本管理页、组织 allowlist、实例绑定版本、manifest v2 / oc-entrypoint、第二个 variant、version_synced、删除旧 persona/model 列与代码。

**Placeholder 扫描：** Task 11 Step 3 提示用 `bytes.NewReader` 替代自写 reader——这是明确实施指令，非占位。其余步骤均含完整代码与命令。

**类型一致性：** `AssistantVersionStore` 接口、`store.AssistantVersionStore` 适配器、`fakeAVStore` 三处方法签名一致；`CountOrgsUsingVersion` 入参类型由 Task 10 Step 4 统一核对后对齐。`assistantVersionService` handler 接口与 `*AssistantVersionService` 方法集一致（含 `ListRuntimeImages`）。`AssistantVersionInput` / `AssistantVersionResult` / `RuntimeImageOption` 跨 service、handler、测试命名一致。

---

## 后续计划

本计划交付后，依次为以下 Phase 各写一份独立计划（每份在前一 Phase 落地后编写，以引用真实类型签名）：
- **Phase 1b：** 前端助手版本管理页（列表 / 编辑表单 / skill 上传组件 / 路由与导航）。
- **Phase 2：** manifest 契约 v2 + oc-entrypoint 改动 + 第二个 variant `hermes-v2026.5.7`。
- **Phase 3：** 组织 allowlist + 实例绑定版本 + 创建流程改造。
- **Phase 4：** 实例初始化/重启写入版本数据 + `version_synced` 检测 + 切换版本。
- **Phase 5：** 切换消费方至新列，再删除旧 persona/model 列与表、清理 dead code。
