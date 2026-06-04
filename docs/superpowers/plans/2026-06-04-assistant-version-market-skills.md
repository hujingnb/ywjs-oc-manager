# 助手版本从市场选 skill（平台库 + ClawHub）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让平台管理员在助手版本里像实例技能市场一样浏览/选择平台库 + ClawHub skill（锁定具体版本），作为该版本所有实例的种子。

**Architecture:** 后端解除 `AddSkillFromLibrary` 的 platform-only 限制，clawhub 分支复刻 `AppSkillService.Install` 的「下载 zip → 缓存对象存储 → 本地算 sha256」；下游种子/bootstrap/render_skills 链路已来源无关、格式无关，零改动。前端把 `SkillManager` 的市场浏览与详情抽屉抽成可复用的 `SkillMarketBrowser.vue` + `SkillDetailDrawer.vue`，实例页与助手版本页共用，仅注入 props 区分主操作（安装/添加）与是否可锁旧版。

**Tech Stack:** Go 1.25 / Gin / sqlc / MySQL；Vue 3 / Vite / Pinia / Naive UI / TanStack Query / vitest。

设计依据：`docs/superpowers/specs/2026-06-04-助手版本市场选skill-design.md`。

---

## File Structure

**后端：**
- `internal/service/assistant_version_service.go` — 改：AVS 结构体 + 构造函数加 `clawhub`/`blobs` 两依赖；`AddSkillFromLibrary` 拆出 `resolveLibrarySkill` 支持 platform/clawhub；`AddSkillFromLibraryInput` 加 `Name` 字段。
- `internal/service/assistant_version_service_test.go` — 改：更新 4 处构造调用；加 clawhub 用例与 fake。
- `cmd/server/main.go` — 改：构造 AVS 时注入 `clawhubClient`（nil 守卫）+ `libraryBlobs`。
- `internal/api/handlers/assistant_versions.go` — 改：`writeAVError` 加 `ErrAppSkillSourceUnknown` → 400 映射。
- `internal/api/handlers/dto.go` — 改：`AddSkillFromLibraryRequest` 加 `Name` 字段、放宽 source 注释。
- `internal/api/handlers/assistant_versions_test.go` — 改：透传 `Name`；加 source 错误用例（若该测试存在；否则在 service 测试覆盖）。

**前端：**
- `web/src/components/SkillDetailDrawer.vue` — 新增：presentational 详情抽屉（richDetail + 版本列表 + 可选「添加此版本」），实例已安装详情、市场详情共用。
- `web/src/components/SkillMarketBrowser.vue` — 新增：市场卡片 + 搜索 + 来源筛选 + 滚动加载，内嵌 `SkillDetailDrawer`，emit `action`。
- `web/src/components/SkillManager.vue` — 改：市场 tab 用 `<SkillMarketBrowser>`；已安装详情用 `<SkillDetailDrawer>`；删掉已迁出的市场/抽屉本地代码。
- `web/src/pages/platform/AssistantVersionsPage.vue` — 改：平台库下拉换成 `<SkillMarketBrowser allow-version-pick action-label="添加">`。
- `web/src/components/SkillDetailDrawer.spec.ts` / `SkillMarketBrowser.spec.ts` — 新增：承接原 SkillManager 市场/抽屉用例 + 锁旧版用例。
- `web/src/components/SkillManager.spec.ts` — 改：精简为已安装用例，stub 两个子组件。
- `web/src/pages/platform/AssistantVersionsPage.spec.ts` — 改/增：市场添加流程用例。
- `web/src/api/hooks/useAssistantVersions.ts` — 改：`AddVersionSkillInput` 加可选 `name`。

---

## 后端 Track

### Task 1: AVS 支持 clawhub 来源（service 层）

**Files:**
- Modify: `internal/service/assistant_version_service.go`（结构体 74-89、Input 400-408、AddSkillFromLibrary 418-456）
- Modify: `cmd/server/main.go:342-347`
- Test: `internal/service/assistant_version_service_test.go`

- [ ] **Step 1: 给 AVS 测试加 clawhub fake 与构造助手，写失败用例**

在 `assistant_version_service_test.go` 顶部（与其它 fake 同处）加 fake，并在文件已有的 import 补 `"context"`（若缺）：

```go
// fakeClawHub 是 ClawHubDownloader 的测试替身：按预置 archive/err 返回。
type fakeClawHub struct {
	archive []byte
	err     error
}

func (f fakeClawHub) Download(_ context.Context, _ /*slug*/, _ /*version*/ string) ([]byte, error) {
	return f.archive, f.err
}

// fakeLibBlob 是 LibraryBlobStore 的测试替身：PutLibrarySkill 记录入参并回固定相对路径。
type fakeLibBlob struct {
	putSource, putRef, putVersion, putExt string
	putData                               []byte
}

func (f *fakeLibBlob) PutLibrarySkill(source, ref, version, ext string, data []byte) (string, error) {
	f.putSource, f.putRef, f.putVersion, f.putExt, f.putData = source, ref, version, ext, data
	return "library/" + source + "/" + ref + "/" + version + "." + ext, nil
}
func (f *fakeLibBlob) DeleteLibrarySkill(string) error            { return nil }
func (f *fakeLibBlob) OpenLibrarySkill(string) (io.ReadCloser, error) { return nil, nil }
```

加新测试（放在已有 AddSkillFromLibrary 平台库用例附近）：

```go
// TestAddSkillFromLibrary_ClawHub 覆盖：source=clawhub 时下载 zip → 缓存对象存储 →
// 本地算 sha256 → 写入 skills_json 自包含快照（name 用入参 displayName，cached_path 为 .zip）。
func TestAddSkillFromLibrary_ClawHub(t *testing.T) {
	store := newFakeAVStore()
	store.seed(sampleVersion("v1")) // 已有 helper：建一个空 skills_json 的版本，ID "v1"
	blob := &fakeLibBlob{}
	svc := NewAssistantVersionService(
		store, fakeImageResolver{}, fakeModelValidator{}, newFakePlatformSkillLibrary(),
		fakeClawHub{archive: []byte("PK\x03\x04zip-bytes")}, blob,
	)
	out, err := svc.AddSkillFromLibrary(context.Background(), adminPrincipal(), "v1", AddSkillFromLibraryInput{
		Source: "clawhub", SourceRef: "skill-vetter", Name: "Skill Vetter", Version: "1.0.0",
	})
	require.NoError(t, err)
	require.Len(t, out.Skills, 1)
	got := out.Skills[0]
	assert.Equal(t, "clawhub", got.Source)              // 来源透传
	assert.Equal(t, "skill-vetter", got.SourceRef)      // slug
	assert.Equal(t, "Skill Vetter", got.Name)           // 目录名用 displayName（非 slug）
	assert.Equal(t, "1.0.0", got.Version)               // 锁定版本
	assert.Equal(t, "library/clawhub/skill-vetter/1.0.0.zip", got.CachedPath) // 缓存为 .zip
	assert.NotEmpty(t, got.FileSha256)                  // 本地计算 sha256
	assert.Equal(t, "zip", blob.putExt)                 // 以 zip 扩展名缓存
}
```

> 注：`newFakeAVStore` / `sampleVersion` / `adminPrincipal` / `newFakePlatformSkillLibrary` 是该测试文件已有 helper（若 helper 名不同，按文件实际名替换；保持断言不变）。

- [ ] **Step 2: 跑测试确认编译失败**

Run: `cd /home/hujing/dir/software/ywjs/oc-manager && go test ./internal/service/ -run TestAddSkillFromLibrary_ClawHub 2>&1 | head`
Expected: 编译失败——`NewAssistantVersionService` 实参过多 + `AddSkillFromLibraryInput` 无 `Name` 字段。

- [ ] **Step 3: 给 AVS 结构体与构造函数加两依赖**

`internal/service/assistant_version_service.go` 结构体（74-79）改为：

```go
// AssistantVersionService 维护助手版本目录。
type AssistantVersionService struct {
	store          AssistantVersionStore
	images         assistantVersionImages
	models         AssistantVersionModelValidator
	platformSkills PlatformSkillLibrary
	// clawhub 下载 ClawHub skill 归档；可为 nil（未配置 ClawHub BaseURL 时禁用 clawhub 来源）。
	clawhub ClawHubDownloader
	// blobs 把 clawhub 下载的归档缓存到对象存储（platform 来源不用，引用平台库已存档案）。
	blobs LibraryBlobStore
}
```

构造函数（82-89）改为：

```go
// NewAssistantVersionService 创建版本 service。
// clawhub 可为 nil（未配 ClawHub），此时 clawhub 来源的 AddSkillFromLibrary 返回 ErrAppSkillSourceUnknown。
func NewAssistantVersionService(
	store AssistantVersionStore,
	images assistantVersionImages,
	models AssistantVersionModelValidator,
	platformSkills PlatformSkillLibrary,
	clawhub ClawHubDownloader,
	blobs LibraryBlobStore,
) *AssistantVersionService {
	return &AssistantVersionService{
		store: store, images: images, models: models,
		platformSkills: platformSkills, clawhub: clawhub, blobs: blobs,
	}
}
```

- [ ] **Step 4: 给 Input 加 Name 字段，AddSkillFromLibrary 拆来源分支**

`AddSkillFromLibraryInput`（400-408）加 `Name`：

```go
// AddSkillFromLibraryInput 是从市场选 skill 配进版本的入参。
type AddSkillFromLibraryInput struct {
	// Source 是 skill 来源类型，接受 "platform" 或 "clawhub"。
	Source string
	// SourceRef 是来源内精准标识；platform=skill name，clawhub=slug。
	SourceRef string
	// Name 是 skill 在版本内的唯一目录名；clawhub 来源必填（用 displayName，与 per-app 安装一致），
	// platform 来源可空（以平台库 DB 的 name 为准）。
	Name string
	// Version 是目标版本号。
	Version string
}
```

`AddSkillFromLibrary`（418-456）改为先 `resolveLibrarySkill` 再做冲突检查；在文件 import 区补 `"crypto/sha256"` 与 `"encoding/hex"`：

```go
// AddSkillFromLibrary 从市场（平台库 / ClawHub）选一个 skill 配进版本快照：
//  1. 权限校验（CanManageAssistantVersion）
//  2. 查版本
//  3. 按来源解析为自包含快照（platform 引用平台库归档；clawhub 下载并缓存）
//  4. 同 name 冲突检查（ErrAssistantVersionSkillNameTaken）
//  5. 追加快照并 revision +1
func (s *AssistantVersionService) AddSkillFromLibrary(ctx context.Context, principal auth.Principal, id string, in AddSkillFromLibraryInput) (AssistantVersionResult, error) {
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
	snap, err := s.resolveLibrarySkill(ctx, in)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	// 同 name 冲突：同一版本内 skill 名称必须唯一。
	for _, sk := range skills {
		if sk.Name == snap.Name {
			return AssistantVersionResult{}, ErrAssistantVersionSkillNameTaken
		}
	}
	skills = append(skills, snap)
	return s.persistSkills(ctx, row, skills)
}

// resolveLibrarySkill 按来源把入参解析为一条自包含 AssistantVersionSkill 快照。
//   - platform：查平台库引用其已持久化的归档（library/platform/<name>/<ver>.tar），不下载。
//   - clawhub：下载 zip → 缓存到对象存储（library/clawhub/<slug>/<ver>.zip）→ 本地算 sha256。
func (s *AssistantVersionService) resolveLibrarySkill(ctx context.Context, in AddSkillFromLibraryInput) (AssistantVersionSkill, error) {
	switch in.Source {
	case "platform":
		ps, err := s.platformSkills.GetPlatformSkillByNameVersion(ctx, sqlc.GetPlatformSkillByNameVersionParams{
			Name: in.SourceRef, Version: in.Version,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return AssistantVersionSkill{}, ErrPlatformSkillNotFound
			}
			return AssistantVersionSkill{}, fmt.Errorf("查询平台库 skill 失败: %w", err)
		}
		return AssistantVersionSkill{
			Source: "platform", SourceRef: ps.Name, Name: ps.Name, Version: ps.Version,
			CachedPath: ps.TarPath, FileSize: ps.FileSize, FileSha256: ps.FileSha256,
		}, nil
	case "clawhub":
		// 未配 ClawHub（downloader/blobs 任一为 nil）时拒绝，前端市场此时也不会展示 clawhub 条目。
		if s.clawhub == nil || s.blobs == nil {
			return AssistantVersionSkill{}, ErrAppSkillSourceUnknown
		}
		if in.Name == "" {
			return AssistantVersionSkill{}, fmt.Errorf("%w: clawhub 来源缺少 name", ErrAssistantVersionInvalid)
		}
		archive, err := s.clawhub.Download(ctx, in.SourceRef, in.Version)
		if err != nil {
			return AssistantVersionSkill{}, fmt.Errorf("从 ClawHub 下载 skill 失败: %w", err)
		}
		relPath, err := s.blobs.PutLibrarySkill("clawhub", in.SourceRef, in.Version, "zip", archive)
		if err != nil {
			return AssistantVersionSkill{}, fmt.Errorf("缓存 ClawHub skill 归档失败: %w", err)
		}
		sum := sha256.Sum256(archive)
		return AssistantVersionSkill{
			Source: "clawhub", SourceRef: in.SourceRef, Name: in.Name, Version: in.Version,
			CachedPath: relPath, FileSize: int64(len(archive)), FileSha256: hex.EncodeToString(sum[:]),
		}, nil
	default:
		return AssistantVersionSkill{}, ErrAppSkillSourceUnknown
	}
}
```

同时更新 `AssistantVersionSkill` 结构体顶部注释（91-92）「首版仅 platform」一句为「支持 platform 与 clawhub」。

- [ ] **Step 5: 更新 main.go 注入两依赖（nil 守卫）**

`cmd/server/main.go:342-347` 改为：

```go
		assistantVersionService = service.NewAssistantVersionService(
			store.NewAssistantVersionStore(dbStore),
			runtimeImageAdapter{images: cfg.Hermes.RuntimeImages},
			modelValidatorAdapter{catalog: modelCatalogService},
			dbStore.Queries,
			nil, // clawhub：下方按 clawhubClient 非 nil 时回填，避免 nil *Client 包装成非 nil interface
			libraryBlobs,
		)
		// 仅当 clawhubClient 指针非 nil 时注入 clawhub 下载器（与 AppSkillService 同一守卫）。
		if clawhubClient != nil {
			assistantVersionService.SetClawHubDownloader(clawhubClient)
		}
```

在 `assistant_version_service.go` 加 setter（紧随构造函数）：

```go
// SetClawHubDownloader 注入 ClawHub 下载器。用 setter 而非构造参数回填，规避 nil *Client
// 直接传接口参数产生「非 nil interface 包装 nil 指针」的陷阱（与 AppSkillService 同处理）。
func (s *AssistantVersionService) SetClawHubDownloader(d ClawHubDownloader) { s.clawhub = d }
```

> 构造时 clawhub 传字面量 `nil`（接口零值，安全），随后仅在指针非 nil 时经 setter 赋值。

- [ ] **Step 6: 更新 4 处既有测试构造调用**

`assistant_version_service_test.go` 行 183/189/290/297 的 `NewAssistantVersionService(...)` 各在末尾补两个实参 `nil, nil`（platform-only 用例不需要 clawhub/blobs）。例如 183：

```go
	return NewAssistantVersionService(store, fakeImageResolver{}, fakeModelValidator{}, newFakePlatformSkillLibrary(), nil, nil)
```

- [ ] **Step 7: 跑 service 测试确认通过**

Run: `go test ./internal/service/ -run 'AssistantVersion' -v 2>&1 | tail -30`
Expected: 含 `TestAddSkillFromLibrary_ClawHub` 在内全部 PASS。

- [ ] **Step 8: 全包编译 + vet**

Run: `go build ./... && go vet ./internal/service/... ./cmd/...`
Expected: 无输出（成功）。

- [ ] **Step 9: Commit**

```bash
git add internal/service/assistant_version_service.go internal/service/assistant_version_service_test.go cmd/server/main.go
git commit -m "feat(skill): 助手版本支持从 ClawHub 选 skill

AddSkillFromLibrary 解除 platform-only：clawhub 分支下载 zip→缓存对象
存储→本地算 sha256，写入版本 skills_json 自包含快照（name 用 displayName）。
AVS 新增 clawhub/blobs 依赖，main.go 按 clawhubClient 非 nil 守卫注入。"
```

---

### Task 2: handler/DTO 放宽 source + 错误映射

**Files:**
- Modify: `internal/api/handlers/dto.go:355-362`
- Modify: `internal/api/handlers/assistant_versions.go:50-69`（writeAVError）+ 219-221（透传 Name）
- Test: `internal/api/handlers/assistant_versions_test.go`（若存在；否则跳过 handler 测试，由 service 测试覆盖）

- [ ] **Step 1: DTO 加 Name 字段、放宽注释**

`dto.go` 的 `AddSkillFromLibraryRequest`（355-362）改为：

```go
type AddSkillFromLibraryRequest struct {
	// Source 是 skill 来源类型，接受 "platform" 或 "clawhub"。
	Source string `json:"source" binding:"required"`
	// SourceRef 是来源内精准标识；platform=skill name，clawhub=slug。
	SourceRef string `json:"source_ref" binding:"required"`
	// Name 是 skill 在版本内的目录名；clawhub 必填（displayName），platform 可空（以 DB 为准）。
	Name string `json:"name"`
	// Version 是要配进版本的 skill 版本号。
	Version string `json:"version" binding:"required"`
}
```

- [ ] **Step 2: handler 透传 Name**

`assistant_versions.go:219-221` 改为：

```go
	out, err := h.service.AddSkillFromLibrary(c.Request.Context(), principalFromCtx(c), c.Param("id"), service.AddSkillFromLibraryInput{
		Source: req.Source, SourceRef: req.SourceRef, Name: req.Name, Version: req.Version,
	})
```

- [ ] **Step 3: writeAVError 加未知来源 → 400**

在 `writeAVError`（50-69）的 `ErrAssistantVersionInvalid` case 之前插入：

```go
	case errors.Is(err, service.ErrAppSkillSourceUnknown):
		c.JSON(http.StatusBadRequest, apierror.New("APP_SKILL_SOURCE_UNKNOWN", "未知的 skill 来源"))
```

> clawhub 下载失败（service 返回包装错误，非 sentinel）落 default → 500，与 per-app 安装 clawhub 下载失败一致，不另设状态码。

- [ ] **Step 4: 编译 + 相关测试**

Run: `go build ./... && go test ./internal/api/handlers/ -run 'AssistantVersion' 2>&1 | tail -20`
Expected: PASS（无 av handler 测试时输出 `no tests to run`，仍须编译通过）。

- [ ] **Step 5: 重新生成 OpenAPI + 前端类型并校验**

Run: `make openapi-gen && make web-types-gen && make openapi-check`
Expected: `openapi-check` 后 git 工作区干净（DTO 仅新增可选 `name` 字段，生成物相应更新）。把生成物一并加入提交。

- [ ] **Step 6: Commit**

```bash
git add internal/api/handlers/dto.go internal/api/handlers/assistant_versions.go openapi/openapi.yaml web/src/api/generated.ts
git commit -m "feat(skill): 助手版本选 skill 接口放宽 source 至 clawhub

AddSkillFromLibraryRequest 加 name 字段、source 接受 clawhub；writeAVError
对未知来源返回 400。重新生成 openapi 与前端类型。"
```

---

## 前端 Track

### Task 3: 抽出 SkillDetailDrawer.vue（详情抽屉）

把 `SkillManager.vue` 的详情抽屉（模板约 110-165、脚本 `SkillDetail` 接口/`detailHasUpstream`/`detailParams`/`skillDetailQuery`/`richDetail`/`detailVersions`/`effectiveDescription`/`detailStatusLabel`/`fmtDate`/`formatCount`/`sourceLabel`、相关 CSS）抽成独立 presentational 组件。

**Files:**
- Create: `web/src/components/SkillDetailDrawer.vue`

- [ ] **Step 1: 新建 SkillDetailDrawer.vue**

```vue
<template>
  <!-- SkillDetailDrawer：skill 详情抽屉，展示富信息 + 版本列表，供已安装列表与市场共用。 -->
  <n-drawer :show="show" :width="420" placement="right" @update:show="$emit('update:show', $event)">
    <n-drawer-content :title="skill?.name ?? '技能详情'" closable>
      <div v-if="skill" class="skill-detail">
        <!-- 作者（clawhub 才有） -->
        <div v-if="richDetail?.author_name" class="skill-detail-author">
          <img v-if="richDetail.author_avatar" :src="richDetail.author_avatar" class="skill-detail-avatar" alt="" referrerpolicy="no-referrer" />
          <span class="skill-detail-author-name">{{ richDetail.author_name }}</span>
          <span v-if="richDetail.author_handle" class="skill-detail-handle">@{{ richDetail.author_handle }}</span>
        </div>

        <!-- 基础信息行 -->
        <p class="skill-detail-row"><span class="skill-detail-label">来源</span>{{ sourceLabel(skill.source) }}</p>
        <p v-if="skill.version" class="skill-detail-row"><span class="skill-detail-label">版本</span>v{{ skill.version }}</p>
        <p v-if="skill.status" class="skill-detail-row"><span class="skill-detail-label">状态</span>{{ statusLabel(skill.status) }}</p>
        <p v-if="richDetail?.license" class="skill-detail-row"><span class="skill-detail-label">许可</span>{{ richDetail.license }}</p>
        <p v-if="fmtDate(richDetail?.created_at)" class="skill-detail-row"><span class="skill-detail-label">创建</span>{{ fmtDate(richDetail?.created_at) }}</p>
        <p v-if="fmtDate(richDetail?.updated_at)" class="skill-detail-row"><span class="skill-detail-label">更新</span>{{ fmtDate(richDetail?.updated_at) }}</p>

        <!-- 统计（clawhub）：下载/星标/安装，带单位显示。 -->
        <div v-if="richDetail && (richDetail.downloads || richDetail.stars || richDetail.installs)" class="skill-detail-stats">
          <span v-if="richDetail.downloads">↓ {{ formatCount(richDetail.downloads) }} 下载</span>
          <span v-if="richDetail.stars">★ {{ formatCount(richDetail.stars) }} 星标</span>
          <span v-if="richDetail.installs">⤓ {{ formatCount(richDetail.installs) }} 安装</span>
        </div>

        <!-- 关键词 -->
        <div v-if="richDetail?.keywords?.length" class="skill-detail-keywords">
          <n-tag v-for="kw in richDetail.keywords" :key="kw" size="tiny" :bordered="false">{{ kw }}</n-tag>
        </div>

        <!-- 完整描述（富详情优先，回退点击带入的描述） -->
        <p v-if="effectiveDescription" class="skill-detail-desc">{{ effectiveDescription }}</p>

        <!-- 版本列表：platform/clawhub 来源才有；builtin/self_created 无来源版本。 -->
        <div class="skill-detail-versions">
          <strong>版本列表</strong>
          <div v-if="!hasUpstream" class="state-text">该来源无版本信息</div>
          <div v-else-if="detailQuery.isLoading.value" class="state-text">加载中…</div>
          <p v-else-if="detailQuery.error.value" class="state-text danger">详情查询失败</p>
          <ul v-else-if="versions.length" class="skill-detail-version-list">
            <li v-for="(v, i) in versions" :key="v.version" class="skill-detail-version-item">
              <div class="skill-detail-version-head">
                <span class="skill-detail-version-num">v{{ v.version }}</span>
                <n-tag v-if="i === 0" size="tiny" type="success" :bordered="false">最新</n-tag>
                <n-tag v-if="v.version === skill.version" size="tiny" type="info" :bordered="false">当前</n-tag>
                <span v-if="fmtDate(v.published_at)" class="skill-detail-version-date">{{ fmtDate(v.published_at) }}</span>
                <!-- 版本场景：每个版本可锁定加入助手版本。 -->
                <n-button
                  v-if="allowVersionPick"
                  size="tiny"
                  type="primary"
                  :loading="actionPending"
                  :disabled="existingNames.has(skill.name)"
                  @click="$emit('pick-version', v.version)"
                >
                  {{ existingNames.has(skill.name) ? '已添加' : '添加此版本' }}
                </n-button>
              </div>
              <div v-if="v.changelog" class="skill-detail-version-log">{{ v.changelog }}</div>
            </li>
          </ul>
          <div v-else class="state-text">暂无版本</div>
        </div>
      </div>
    </n-drawer-content>
  </n-drawer>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NButton, NDrawer, NDrawerContent, NTag } from 'naive-ui'
import { useSkillDetailQuery } from '@/api/hooks/useSkills'

// SkillDetail 是抽屉展示的数据，已安装行与市场卡片各取所需字段填充。
export interface SkillDetail {
  name: string
  source?: string
  source_ref?: string
  version?: string
  description?: string
  downloads?: number
  status?: string // 仅已安装列表有
}

const props = withDefaults(
  defineProps<{
    show: boolean
    skill: SkillDetail | null
    allowVersionPick?: boolean // 版本场景=true，版本行显示「添加此版本」
    actionPending?: boolean
    existingNames?: Set<string> // 已配置/已安装名集合，命中则禁用添加
  }>(),
  { allowVersionPick: false, actionPending: false, existingNames: () => new Set<string>() },
)
defineEmits<{ 'update:show': [boolean]; 'pick-version': [string] }>()

// hasUpstream：仅 platform/clawhub 来源有上游富详情/版本（builtin/self_created 无来源标识）。
const hasUpstream = computed(() => {
  const d = props.skill
  return Boolean(d?.source_ref && (d.source === 'platform' || d.source === 'clawhub'))
})
const detailParams = computed(() => ({
  source: hasUpstream.value ? props.skill?.source : undefined,
  ref: hasUpstream.value ? props.skill?.source_ref : undefined,
}))
const detailQuery = useSkillDetailQuery(detailParams)
const richDetail = computed(() => detailQuery.data.value?.detail ?? null)
const versions = computed(() => detailQuery.data.value?.versions ?? [])
const effectiveDescription = computed(
  () => richDetail.value?.description || props.skill?.description || '',
)

// sourceLabel：空来源（内置/自创）显示「内置」，避免「来源」一栏为空。
function sourceLabel(source?: string): string {
  if (source === 'platform') return '平台库'
  if (source === 'clawhub') return 'ClawHub'
  return source || '内置'
}
function statusLabel(status: string): string {
  const labels: Record<string, string> = { active: '已生效', pending: '待生效', builtin: '内置', self_created: '自创' }
  return labels[status] ?? status
}
function fmtDate(v?: string | number): string {
  if (!v) return ''
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString().slice(0, 10)
}
function formatCount(n?: number): string {
  if (!n || n < 10000) return String(n ?? 0)
  const fmt = (val: number, unit: string) => `${val.toFixed(1).replace(/\.0$/, '')}${unit}`
  if (n >= 1_000_000) return fmt(n / 1_000_000, '百万')
  return fmt(n / 10_000, '万')
}
</script>

<style scoped>
/* 详情抽屉样式：从 SkillManager.vue 原样迁入（行/标签/描述/版本列表/作者/统计/关键词）。 */
.skill-detail-row { margin: 4px 0; font-size: 13px; }
.skill-detail-label { display: inline-block; width: 56px; color: var(--text-muted, #888); }
.skill-detail-desc { margin: 12px 0; font-size: 13px; line-height: 1.6; white-space: pre-wrap; }
.skill-detail-versions { margin-top: 16px; }
.skill-detail-version-list { list-style: none; padding: 0; margin: 8px 0 0; }
.skill-detail-version-list li { font-size: 13px; }
.skill-detail-version-num { font-family: var(--font-mono, monospace); }
.skill-detail-author { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; }
.skill-detail-avatar { width: 28px; height: 28px; border-radius: 50%; object-fit: cover; }
.skill-detail-author-name { font-weight: 600; font-size: 13px; }
.skill-detail-handle { color: var(--text-muted, #888); font-size: 12px; }
.skill-detail-stats { display: flex; flex-wrap: wrap; gap: 12px; margin: 10px 0; font-size: 12px; color: var(--text-muted, #888); }
.skill-detail-keywords { display: flex; flex-wrap: wrap; gap: 6px; margin: 8px 0; }
.skill-detail-version-item { display: block; padding: 8px 0; border-bottom: 1px solid var(--border-color, #eee); }
.skill-detail-version-head { display: flex; align-items: center; gap: 8px; }
.skill-detail-version-date { margin-left: auto; color: var(--text-muted, #999); font-size: 12px; }
.skill-detail-version-log { margin-top: 4px; font-size: 12px; color: var(--text-muted, #666); line-height: 1.5; }
</style>
```

- [ ] **Step 2: 类型检查**

Run: `cd web && npx vue-tsc --noEmit 2>&1 | head`
Expected: 无报错（新组件自洽；`SkillManager.vue` 暂仍保留自己的抽屉，不冲突）。

- [ ] **Step 3: Commit**

```bash
git add web/src/components/SkillDetailDrawer.vue
git commit -m "feat(web): 抽出 SkillDetailDrawer 详情抽屉组件

把 SkillManager 详情抽屉抽成 presentational 组件，支持 allowVersionPick
时每个版本行可「添加此版本」，供已安装列表与市场浏览器共用。"
```

---

### Task 4: 抽出 SkillMarketBrowser.vue（市场浏览器）

把 `SkillManager.vue` 市场 tab（工具栏来源筛选 + 搜索、卡片网格、滚动加载哨兵）与相关脚本（`sourceFilters`/`selectedSource`/`searchText`/`debouncedSearch`/`marketParams`/`skillMarketQuery`/`marketEntries`/`loadMoreSentinel`/`setupLoadMoreObserver`/`onSelectSource`/`sourceLabel`/`sourceTagType`/`formatCount`/`isInstalled` 等市场相关项、市场 CSS）抽成组件，内嵌 `SkillDetailDrawer`。

**Files:**
- Create: `web/src/components/SkillMarketBrowser.vue`

- [ ] **Step 1: 新建 SkillMarketBrowser.vue**

```vue
<template>
  <!-- SkillMarketBrowser：平台库 + ClawHub 聚合市场浏览，供实例安装与助手版本选 skill 共用。 -->
  <div class="skill-market-browser">
    <!-- 筛选工具栏：来源 tag 切换 + 关键词搜索。 -->
    <div class="market-toolbar">
      <div class="market-filters">
        <n-tag
          v-for="filter in sourceFilters"
          :key="filter.value"
          :type="selectedSource === filter.value ? 'primary' : 'default'"
          :bordered="false"
          checkable
          :checked="selectedSource === filter.value"
          class="filter-tag"
          @click="selectedSource = filter.value"
        >
          {{ filter.label }}
        </n-tag>
      </div>
      <n-input v-model:value="searchText" placeholder="搜索技能名称…" clearable size="small" class="market-search" />
    </div>

    <div v-if="skillMarketQuery.isLoading.value" class="state-text">加载中…</div>
    <p v-else-if="skillMarketQuery.error.value" class="state-text danger">
      市场查询失败：{{ skillMarketQuery.error.value?.message }}
    </p>
    <div v-else-if="!marketEntries.length" class="state-text">暂无技能</div>
    <div v-else class="market-grid">
      <n-card
        v-for="entry in marketEntries"
        :key="`${entry.source}-${entry.source_ref}`"
        size="small"
        class="market-card market-card-clickable"
        @click="openDetail(entry)"
      >
        <div class="market-card-header">
          <strong class="market-card-name">{{ entry.name }}</strong>
          <div class="market-card-meta">
            <n-tag :type="sourceTagType(entry.source)" size="small" :bordered="false">{{ sourceLabel(entry.source) }}</n-tag>
          </div>
        </div>
        <p v-if="entry.description" class="market-card-desc">{{ entry.description }}</p>
        <div class="market-card-footer">
          <span class="market-card-version">v{{ entry.version }}</span>
          <span v-if="entry.downloads" class="market-card-downloads">↓ {{ formatCount(entry.downloads) }}</span>
          <template v-if="existingNames.has(entry.name)">
            <n-tag size="small" type="success" :bordered="false">{{ existingLabel }}</n-tag>
          </template>
          <n-button
            v-else-if="canAction"
            size="small"
            type="primary"
            :loading="actionPending"
            @click.stop="emitAction(entry, entry.version ?? '')"
          >
            {{ actionLabel }}
          </n-button>
        </div>
      </n-card>
    </div>
    <div
      v-if="marketEntries.length && skillMarketQuery.hasNextPage.value"
      ref="loadMoreSentinel"
      class="market-load-more state-text"
    >
      {{ skillMarketQuery.isFetchingNextPage.value ? '加载中…' : '滚动加载更多' }}
    </div>

    <!-- 详情抽屉：点卡片打开，版本场景下可锁旧版（pick-version）。 -->
    <skill-detail-drawer
      v-model:show="detailOpen"
      :skill="detailSkill"
      :allow-version-pick="allowVersionPick"
      :action-pending="actionPending"
      :existing-names="existingNames"
      @pick-version="onPickVersion"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { NButton, NCard, NInput, NTag } from 'naive-ui'
import type { SkillEntry } from '@/api'
import { useSkillMarketQuery } from '@/api/hooks/useSkills'
import SkillDetailDrawer, { type SkillDetail } from './SkillDetailDrawer.vue'

const props = withDefaults(
  defineProps<{
    existingNames?: Set<string> // 已安装/已配置名集合，命中则不显示操作按钮
    actionLabel?: string // 主操作文案：安装 / 添加
    existingLabel?: string // 已存在标记文案：已安装 / 已添加
    actionPending?: boolean
    canAction?: boolean // 是否有权限展示操作
    allowVersionPick?: boolean // true：详情抽屉版本行可「添加此版本」
  }>(),
  {
    existingNames: () => new Set<string>(),
    actionLabel: '安装',
    existingLabel: '已安装',
    actionPending: false,
    canAction: true,
    allowVersionPick: false,
  },
)
// action 事件：携带来源/标识/名称/选定版本，由父级执行安装或加入版本。
const emit = defineEmits<{ action: [{ source: string; source_ref: string; name: string; version: string }] }>()

// 来源筛选项。
const sourceFilters = [
  { label: '全部', value: '' },
  { label: '平台库', value: 'platform' },
  { label: 'ClawHub', value: 'clawhub' },
] as const
const selectedSource = ref<string>('')

const searchText = ref('')
const debouncedSearch = ref('')
let debounceTimer: ReturnType<typeof setTimeout> | null = null
watch(searchText, (val) => {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => { debouncedSearch.value = val }, 300)
})

const marketParams = computed(() => ({
  source: selectedSource.value || undefined,
  q: debouncedSearch.value || undefined,
}))
const skillMarketQuery = useSkillMarketQuery(marketParams)

// marketEntries 展平所有页并按 source+source_ref 去重（聚合模式下 platform 每页重复返回）。
const marketEntries = computed<SkillEntry[]>(() => {
  const pages = skillMarketQuery.data.value?.pages ?? []
  const seen = new Set<string>()
  const out: SkillEntry[] = []
  for (const page of pages) {
    for (const entry of page.entries ?? []) {
      const key = `${entry.source}-${entry.source_ref}`
      if (seen.has(key)) continue
      seen.add(key)
      out.push(entry)
    }
  }
  return out
})

// 滚动加载哨兵（IntersectionObserver）。
const loadMoreSentinel = ref<HTMLElement | null>(null)
let loadMoreObserver: IntersectionObserver | null = null
function setupLoadMoreObserver(el: HTMLElement | null) {
  loadMoreObserver?.disconnect()
  loadMoreObserver = null
  if (!el) return
  loadMoreObserver = new IntersectionObserver(
    (entries) => {
      if (
        entries.some((e) => e.isIntersecting) &&
        skillMarketQuery.hasNextPage.value &&
        !skillMarketQuery.isFetchingNextPage.value
      ) {
        void skillMarketQuery.fetchNextPage()
      }
    },
    { rootMargin: '200px' },
  )
  loadMoreObserver.observe(el)
}
watch(loadMoreSentinel, (el) => setupLoadMoreObserver(el))
onBeforeUnmount(() => loadMoreObserver?.disconnect())

// 详情抽屉。
const detailOpen = ref(false)
const detailSkill = ref<SkillDetail | null>(null)
function openDetail(entry: SkillEntry) {
  detailSkill.value = {
    name: entry.name, source: entry.source, source_ref: entry.source_ref,
    version: entry.version, description: entry.description, downloads: entry.downloads,
  }
  detailOpen.value = true
}
// 详情抽屉锁定某个具体版本加入。
function onPickVersion(version: string) {
  const d = detailSkill.value
  if (!d) return
  emit('action', { source: d.source ?? '', source_ref: d.source_ref ?? '', name: d.name, version })
}
function emitAction(entry: SkillEntry, version: string) {
  emit('action', { source: entry.source, source_ref: entry.source_ref, name: entry.name, version })
}

function sourceLabel(source?: string): string {
  if (source === 'platform') return '平台库'
  if (source === 'clawhub') return 'ClawHub'
  return source || '内置'
}
function sourceTagType(source?: string): 'info' | 'warning' | 'default' {
  if (source === 'platform') return 'info'
  if (source === 'clawhub') return 'warning'
  return 'default'
}
function formatCount(n?: number): string {
  if (!n || n < 10000) return String(n ?? 0)
  const fmt = (val: number, unit: string) => `${val.toFixed(1).replace(/\.0$/, '')}${unit}`
  if (n >= 1_000_000) return fmt(n / 1_000_000, '百万')
  return fmt(n / 10_000, '万')
}
</script>

<style scoped>
/* 市场样式：从 SkillManager.vue 原样迁入。 */
.market-toolbar { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; }
.market-filters { display: flex; gap: 8px; flex-wrap: wrap; }
.filter-tag { cursor: pointer; }
.market-search { width: 200px; flex-shrink: 0; }
.market-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: 12px; }
.market-load-more { display: flex; justify-content: center; margin-top: 12px; }
.market-card-clickable { cursor: pointer; }
.market-card-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 8px; margin-bottom: 6px; }
.market-card-name { font-size: 14px; word-break: break-all; }
.market-card-meta { flex-shrink: 0; }
.market-card-desc { font-size: 12px; color: var(--color-text-secondary); margin: 0 0 8px; display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; }
.market-card-footer { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.market-card-version { font-size: 12px; color: var(--color-text-secondary); }
.market-card-downloads { font-size: 12px; color: var(--color-text-secondary); }
</style>
```

- [ ] **Step 2: 类型检查**

Run: `cd web && npx vue-tsc --noEmit 2>&1 | head`
Expected: 无报错。

- [ ] **Step 3: Commit**

```bash
git add web/src/components/SkillMarketBrowser.vue
git commit -m "feat(web): 抽出 SkillMarketBrowser 市场浏览器组件

平台库+ClawHub 聚合市场（卡片/搜索/来源筛选/滚动加载/详情抽屉），
通过 props 注入主操作与 allowVersionPick，emit action 由父级执行。"
```

---

### Task 5: SkillManager.vue 改用两个共享组件

**Files:**
- Modify: `web/src/components/SkillManager.vue`

- [ ] **Step 1: 市场 tab 替换为 SkillMarketBrowser**

把 `<n-tab-pane name="market">` 内的全部市场实现（工具栏 + 卡片网格 + 哨兵，约模板 27-107）替换为：

```vue
      <n-tab-pane name="market" tab="技能市场">
        <skill-market-browser
          :existing-names="installedNames"
          action-label="安装"
          existing-label="已安装"
          :action-pending="installMutation.isPending.value"
          :can-action="canManage"
          @action="onMarketAction"
        />
      </n-tab-pane>
```

把根模板里原详情抽屉（约 110-165）替换为已安装详情抽屉：

```vue
    <!-- 已安装 skill 详情抽屉：点已安装名称打开（无版本锁定动作）。 -->
    <skill-detail-drawer v-model:show="detailOpen" :skill="detailSkill" />
```

- [ ] **Step 2: 脚本清理 + 接线**

在 `<script setup>`：
- 删除已迁出的市场与抽屉局部项：`sourceFilters`/`selectedSource`/`searchText`/`debouncedSearch`/`debounceTimer`/`marketParams`/`skillMarketQuery`/`marketEntries`/`loadMoreSentinel`/`loadMoreObserver`/`setupLoadMoreObserver`/`onSelectSource`/`sourceTagType`/`openMarketDetail`、以及抽屉相关 `detailHasUpstream`/`detailParams`/`skillDetailQuery`/`richDetail`/`detailVersions`/`effectiveDescription`/`detailStatusLabel`、`SkillDetail` 接口、`fmtDate`、市场用 `formatCount`/`sourceLabel`（若已安装列其它地方仍用 `sourceLabel`/`formatCount`，保留；否则删）。
- 删除对应 import：`NCard`/`NInput`/`NDrawer`/`NDrawerContent`、`useSkillMarketQuery`/`useSkillDetailQuery`、`onBeforeUnmount`（若不再用）。
- 保留并继续使用：`appSkillsQuery`/`installedNames`/`isInstalled`/`installMutation`/已安装列表来源筛选（上一提交新增项）/`openInstalledDetail`/`detailOpen`/`detailSkill`。
- 新增 import：

```ts
import SkillMarketBrowser from './SkillMarketBrowser.vue'
import SkillDetailDrawer from './SkillDetailDrawer.vue'
```

- 把原 `onInstall(entry)` 改名/适配为接收 emit 负载的 `onMarketAction`：

```ts
// onMarketAction 处理市场浏览器的安装动作（payload 含 source/source_ref/name/version）。
async function onMarketAction(p: { source: string; source_ref: string; name: string; version: string }) {
  try {
    await installMutation.mutateAsync({ source: p.source, source_ref: p.source_ref, name: p.name, version: p.version })
    message.success(`已安装 ${p.name}`)
  } catch (err) {
    message.error(err instanceof Error ? err.message : '安装失败')
  }
}
```

- `openInstalledDetail` 继续填 `detailSkill`（已安装详情，无 source_ref 上游时抽屉显示「该来源无版本信息」）。

> 删除原市场/抽屉对应的 `<style>` 段（已迁入两个子组件）；保留已安装列表与来源筛选相关 CSS。

- [ ] **Step 3: 类型检查 + 构建**

Run: `cd web && npx vue-tsc --noEmit 2>&1 | head`
Expected: 无报错、无未使用变量/import。

- [ ] **Step 4: Commit**

```bash
git add web/src/components/SkillManager.vue
git commit -m "refactor(web): SkillManager 市场与详情改用共享子组件

市场 tab 用 SkillMarketBrowser、已安装详情用 SkillDetailDrawer，删除
迁出的本地市场/抽屉逻辑与样式；已安装列表与来源筛选保持不变。"
```

---

### Task 6: 迁移/拆分前端测试

**Files:**
- Create: `web/src/components/SkillDetailDrawer.spec.ts`
- Create: `web/src/components/SkillMarketBrowser.spec.ts`
- Modify: `web/src/components/SkillManager.spec.ts`

- [ ] **Step 1: SkillMarketBrowser.spec.ts —— 承接市场用例**

把 `SkillManager.spec.ts` 中市场相关用例迁来并改为挂载 `SkillMarketBrowser`：来源徽章（平台库/ClawHub）、安装按钮展示、`existingNames` 命中显示「已安装/已添加」无按钮、`canAction=false` 不显示按钮、滚动加载（哨兵进入视口触发 fetchNextPage / 正在拉取不重复 / 无下一页不 observe）、点卡片 emit `action` 带最新版。复用原 spec 的 `IntersectionObserver` mock 与 `useSkillMarketQuery` mock。新增：

```ts
it('点击卡片安装按钮 emit action 带最新版本', async () => {
  // 覆盖：卡片主操作 emit action，version 为卡片展示的最新版。
  marketState.data.value = { entries: [{ source: 'clawhub', source_ref: 'sv', name: 'Skill Vetter', version: '1.0.0', downloads: 0 }] }
  const wrapper = mount(SkillMarketBrowser, { props: { actionLabel: '添加', canAction: true } })
  await wrapper.find('.market-card button').trigger('click')
  expect(wrapper.emitted('action')?.[0][0]).toMatchObject({ source: 'clawhub', source_ref: 'sv', name: 'Skill Vetter', version: '1.0.0' })
})
```

- [ ] **Step 2: SkillDetailDrawer.spec.ts —— 抽屉 + 锁旧版**

迁来抽屉用例（版本列表渲染「最新/当前/changelog」、富详情描述优先、内置无版本信息），新增锁旧版：

```ts
it('allowVersionPick 时版本行点「添加此版本」emit pick-version 带该版本', async () => {
  // 覆盖：版本场景下每个版本行可锁定加入，emit 携带该行版本号。
  detailState.data.value = { detail: { description: 'd' }, versions: [{ version: '2.0.0' }, { version: '1.0.0' }] }
  const wrapper = mount(SkillDetailDrawer, {
    props: { show: true, allowVersionPick: true, existingNames: new Set<string>(),
      skill: { name: 'sv', source: 'clawhub', source_ref: 'sv', version: '2.0.0' } },
  })
  await nextTick()
  const btn = wrapper.findAll('button').find((b) => b.text().includes('添加此版本'))
  await btn!.trigger('click')
  expect(wrapper.emitted('pick-version')?.[0]).toEqual(['2.0.0'])
})
```

> 复用原 spec 的 `useSkillDetailQuery` mock（`detailState`）与 NaiveUI stub（NDrawer/NDrawerContent/NTag/NButton）。

- [ ] **Step 3: SkillManager.spec.ts —— 精简 + stub 子组件**

删除已迁走的市场/抽屉用例；保留已安装列表用例（状态徽章、protected、卸载、来源筛选+数量统计、更新按钮等）。在 `vi.mock('naive-ui')` 之外，stub 两个子组件，避免拉起其内部市场/详情查询：

```ts
vi.mock('./SkillMarketBrowser.vue', () => ({ default: { name: 'SkillMarketBrowser', template: '<div class="stub-market" />' } }))
vi.mock('./SkillDetailDrawer.vue', () => ({ default: { name: 'SkillDetailDrawer', template: '<div class="stub-drawer" />' } }))
```

并删除测试中对市场/抽屉的 mock（`useSkillMarketQuery`/`useSkillDetailQuery` 若仅市场用例需要可移除）。已安装点名称只需断言打开抽屉（stub 存在即可）。

- [ ] **Step 4: 跑前端全部单测**

Run: `cd web && npx vitest run src/components/SkillManager.spec.ts src/components/SkillMarketBrowser.spec.ts src/components/SkillDetailDrawer.spec.ts 2>&1 | tail -15`
Expected: 三个 spec 全 PASS。

- [ ] **Step 5: Commit**

```bash
git add web/src/components/SkillManager.spec.ts web/src/components/SkillMarketBrowser.spec.ts web/src/components/SkillDetailDrawer.spec.ts
git commit -m "test(web): 拆分 SkillManager 测试至市场/详情子组件

市场与详情抽屉用例迁至 SkillMarketBrowser/SkillDetailDrawer spec（含锁旧版），
SkillManager spec 精简为已安装用例并 stub 两个子组件。"
```

---

### Task 7: 助手版本页接入 SkillMarketBrowser

**Files:**
- Modify: `web/src/pages/platform/AssistantVersionsPage.vue`
- Modify: `web/src/api/hooks/useAssistantVersions.ts`（`AddVersionSkillInput` 加可选 `name`）

- [ ] **Step 1: AddVersionSkillInput 加可选 name**

`useAssistantVersions.ts` 的 `AddVersionSkillInput`（149 起）加字段：

```ts
export interface AddVersionSkillInput {
  // source 是 skill 来源类型，支持 "platform" 与 "clawhub"。
  source: string
  // source_ref 是来源内精准标识；platform=skill name，clawhub=slug。
  source_ref: string
  // name 是 skill 在版本内的目录名；clawhub 必填（displayName），platform 可省略。
  name?: string
  // version 是要配进版本的 skill 版本号。
  version: string
}
```

- [ ] **Step 2: 版本页模板替换平台库下拉为市场浏览器**

`AssistantVersionsPage.vue` 编辑态的「从平台库选」块（114-131）替换为：

```vue
                <!-- 编辑态才可从市场选 skill；新建态需先保存版本才有 ID -->
                <template v-if="editingId">
                  <skill-market-browser
                    action-label="添加"
                    existing-label="已添加"
                    :allow-version-pick="true"
                    :action-pending="skillAdding"
                    :existing-names="editingSkillNames"
                    @action="onAddFromMarket"
                  />
                </template>
                <p v-else class="state-text">保存版本后可配置 skill</p>
```

- [ ] **Step 3: 版本页脚本接线**

在 `<script setup>`：
- 删除平台库下拉相关：`selectedSkillKey`、`platformSkillsQuery`、`platformSkillOptions`、`onAddSkill`，以及 `usePlatformSkillsQuery` import 和 `NSelect`（若别处不再用）。
- 新增 import：

```ts
import SkillMarketBrowser from '@/components/SkillMarketBrowser.vue'
```

- 加已配名集合与添加处理：

```ts
// editingSkillNames 是当前编辑版本已配 skill 名集合，传给市场浏览器做去重（已配则不可再加）。
const editingSkillNames = computed(() => new Set(editingSkills.value.map((s) => s.name)))

// onAddFromMarket 接收市场浏览器的添加动作，调后端 AddSkillFromLibrary，成功后刷新本地 skill 列表。
async function onAddFromMarket(p: { source: string; source_ref: string; name: string; version: string }) {
  if (!editingId.value) return
  skillFeedback.value = ''
  skillFeedbackError.value = false
  skillAdding.value = true
  try {
    const updated = await addSkillMutation.mutateAsync({
      id: editingId.value,
      input: { source: p.source, source_ref: p.source_ref, name: p.name, version: p.version },
    })
    editingSkills.value = updated.skills ?? []
    skillFeedback.value = `已添加 skill ${p.name} v${p.version}`
  } catch (err) {
    skillFeedbackError.value = true
    skillFeedback.value = err instanceof Error ? err.message : '添加失败'
  } finally {
    skillAdding.value = false
  }
}
```

> 保留 `editingSkills`/`addSkillMutation`/`onDeleteSkill`/`skillFeedback`/`skillAdding` 等不变。

- [ ] **Step 4: 类型检查**

Run: `cd web && npx vue-tsc --noEmit 2>&1 | head`
Expected: 无报错、无未使用项。

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/platform/AssistantVersionsPage.vue web/src/api/hooks/useAssistantVersions.ts
git commit -m "feat(web): 助手版本页用市场浏览器选 skill（平台库+ClawHub）

编辑态以 SkillMarketBrowser 替代平台库下拉，allowVersionPick 锁定具体
版本后调 AddSkillFromLibrary；AddVersionSkillInput 加可选 name。"
```

---

### Task 8: 助手版本页添加流程单测

**Files:**
- Modify/Create: `web/src/pages/platform/AssistantVersionsPage.spec.ts`

- [ ] **Step 1: 写市场添加流程用例**

stub `SkillMarketBrowser`，断言编辑态渲染浏览器、其 `action` 事件触发 `useAddVersionSkill` 调用并带正确 payload：

```ts
it('编辑态从市场添加 skill 调 AddSkillFromLibrary 并刷新列表', async () => {
  // 覆盖：版本编辑态渲染 SkillMarketBrowser，触发 action（clawhub + 指定版本）→ 调添加接口。
  addSkillMutateAsync.mockResolvedValue({ id: 'v1', skills: [{ source: 'clawhub', name: 'Skill Vetter', version: '1.0.0' }] })
  const wrapper = await mountInEditMode('v1') // helper：打开 v1 编辑态（按本 spec 既有模式实现）
  const browser = wrapper.findComponent({ name: 'SkillMarketBrowser' })
  expect(browser.exists()).toBe(true)
  browser.vm.$emit('action', { source: 'clawhub', source_ref: 'sv', name: 'Skill Vetter', version: '1.0.0' })
  await flushPromises()
  expect(addSkillMutateAsync).toHaveBeenCalledWith({
    id: 'v1',
    input: { source: 'clawhub', source_ref: 'sv', name: 'Skill Vetter', version: '1.0.0' },
  })
})
```

> 若该页尚无 spec，新建并按现有页面 spec（如 OrgSkillsPage/其它 platform 页）的挂载/mock 模式搭骨架：mock `useAssistantVersionsQuery`/`useAddVersionSkill` 等 hooks、stub `SkillMarketBrowser`。

- [ ] **Step 2: 跑用例**

Run: `cd web && npx vitest run src/pages/platform/AssistantVersionsPage.spec.ts 2>&1 | tail -15`
Expected: PASS。

- [ ] **Step 3: 跑前端全量单测 + 类型检查**

Run: `cd web && npx vitest run 2>&1 | tail -5 && npx vue-tsc --noEmit 2>&1 | head`
Expected: 全绿、无类型错误。

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/platform/AssistantVersionsPage.spec.ts
git commit -m "test(web): 覆盖助手版本页从市场添加 skill 流程"
```

---

### Task 9: 端到端真实浏览器验证（本地 k3d，三角色）

**Files:** 无（验证任务）

- [ ] **Step 1: 重建并部署 web + api 镜像**

Run:
```bash
cd /home/hujing/dir/software/ywjs/oc-manager
docker build -t k3d-ocm-registry.localhost:5000/oc-manager-web:dev -f web/Dockerfile ./web
docker build -t k3d-ocm-registry.localhost:5000/oc-manager-api:dev -f cmd/server/Dockerfile .
docker push k3d-ocm-registry.localhost:5000/oc-manager-web:dev
docker push k3d-ocm-registry.localhost:5000/oc-manager-api:dev
kubectl -n ocm rollout restart deploy/manager-web deploy/manager-api
kubectl -n ocm rollout status deploy/manager-web deploy/manager-api --timeout=120s
```
Expected: 两个 deploy 滚动完成。

- [ ] **Step 2: 平台管理员浏览器验证（chrome-devtools）**

用 admin/admin123 登录 `http://ocm.localhost`，进「助手版本」→ 编辑一个版本 → 技能区出现市场浏览器：
- 切「ClawHub」筛选、搜索，点一个 skill 卡片打开详情抽屉，在版本列表点「添加此版本」（锁旧版）；再用卡片「添加」加一个平台库 skill（最新版）。
- 断言：已配 skill 列表新增两条（含 ClawHub displayName + 锁定版本）；重复添加同名显示「已添加」不可再加。

- [ ] **Step 3: 种子继承验证**

新建或切换一个实例到该版本 → 进实例「技能」已安装 tab → 断言上述 ClawHub/平台库 skill 经种子注入出现、状态最终 `active`（必要时点「重新安装」促 reload）。三角色（platform_admin / org_admin vadmin / org_member vmember）各复核实例技能页展示正常。

- [ ] **Step 4: 回归既有市场功能**

在实例「技能市场」tab 复核：抽出的 `SkillMarketBrowser` 行为与改造前一致（搜索/来源筛选/滚动加载/详情/安装），已安装 tab 的来源筛选与数量统计不受影响。

- [ ] **Step 5（若发现问题）：修复并重验**

按 `systematic-debugging` 定位，修复后回到 Step 1 重新部署验证，直到全部正常。

---

## Self-Review 结论

- **Spec 覆盖**：3.1 组件边界 → Task 3/4/5；3.2 后端 → Task 1/2；3.3 版本锁定 → Task 3（pick-version）/4/7；3.4 数据流 → Task 9 端到端；4 测试 → Task 6/8/9；非目标（不动种子/bootstrap/render_skills/DB）→ 计划未触碰这些文件，符合。
- **新发现并补入**：详情抽屉被已安装与市场共用 → 抽 `SkillDetailDrawer`（Task 3）避免重复；clawhub displayName≠slug → Input/DTO/前端加 `name`（Task 1/2/7）。
- **类型一致性**：`action` 事件 payload `{source, source_ref, name, version}` 在 SkillMarketBrowser（emit）、SkillManager(`onMarketAction`)、AssistantVersionsPage(`onAddFromMarket`) 三处签名一致；`SkillDetail` 接口由 SkillDetailDrawer 导出、被 SkillMarketBrowser 复用；后端 `AddSkillFromLibraryInput.Name` 与 DTO `name`、前端 `AddVersionSkillInput.name` 对齐。
- **无占位符**：各步含完整代码或精确「迁移 + 改动」指令（抽取任务为既有代码原样迁移，已标注原位置与需改处）。
