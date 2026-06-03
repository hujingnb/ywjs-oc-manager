package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// fakeAVStore 是 AssistantVersionStore 的内存实现，按需在各测试里填充。
// Create/Update/Delete 为 :exec，写入后通过 Get 读回（模拟 MySQL 写后读模式）。
type fakeAVStore struct {
	versions  map[string]sqlc.AssistantVersion
	byName    map[string]sqlc.AssistantVersion
	appCount  int64
	orgCount  int64
	createErr error
	updateErr error
}

func newFakeAVStore() *fakeAVStore {
	return &fakeAVStore{versions: map[string]sqlc.AssistantVersion{}, byName: map[string]sqlc.AssistantVersion{}}
}

func (f *fakeAVStore) GetAssistantVersion(_ context.Context, id string) (sqlc.AssistantVersion, error) {
	v, ok := f.versions[id]
	if !ok {
		return sqlc.AssistantVersion{}, sql.ErrNoRows
	}
	return v, nil
}

func (f *fakeAVStore) GetAssistantVersionByName(_ context.Context, name string) (sqlc.AssistantVersion, error) {
	v, ok := f.byName[name]
	if !ok {
		return sqlc.AssistantVersion{}, sql.ErrNoRows
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

// CreateAssistantVersion 为 :exec；service 传入自生成 ID，stub 将其存入 map 供后续 Get 读回。
func (f *fakeAVStore) CreateAssistantVersion(_ context.Context, arg sqlc.CreateAssistantVersionParams) error {
	if f.createErr != nil {
		return f.createErr
	}
	v := sqlc.AssistantVersion{
		ID:          arg.ID,
		Name:        arg.Name,
		Description: arg.Description,
		SystemPrompt: arg.SystemPrompt,
		ImageID:     arg.ImageID,
		MainModel:   arg.MainModel,
		RoutingJson: arg.RoutingJson,
		SkillsJson:  arg.SkillsJson,
		Revision:    1,
	}
	f.versions[v.ID] = v
	f.byName[v.Name] = v
	return nil
}

// UpdateAssistantVersion 为 :exec；stub 直接更新内存 map，供后续 Get 读回。
func (f *fakeAVStore) UpdateAssistantVersion(_ context.Context, arg sqlc.UpdateAssistantVersionParams) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	v, ok := f.versions[arg.ID]
	if !ok {
		return sql.ErrNoRows
	}
	oldName := v.Name
	v.Name, v.Description, v.SystemPrompt = arg.Name, arg.Description, arg.SystemPrompt
	v.ImageID, v.MainModel = arg.ImageID, arg.MainModel
	v.RoutingJson, v.SkillsJson, v.Revision = arg.RoutingJson, arg.SkillsJson, arg.Revision
	f.versions[v.ID] = v
	delete(f.byName, oldName)
	f.byName[v.Name] = v
	return nil
}

// UpdateAssistantVersionSkills 为 :exec；stub 更新 skill JSON 与 revision。
func (f *fakeAVStore) UpdateAssistantVersionSkills(_ context.Context, arg sqlc.UpdateAssistantVersionSkillsParams) error {
	v, ok := f.versions[arg.ID]
	if !ok {
		return sql.ErrNoRows
	}
	v.SkillsJson, v.Revision = arg.SkillsJson, arg.Revision
	f.versions[v.ID] = v
	f.byName[v.Name] = v
	return nil
}

// SoftDeleteAssistantVersion 为 :exec；stub 从 map 中删除，后续 Get 返回 sql.ErrNoRows。
func (f *fakeAVStore) SoftDeleteAssistantVersion(_ context.Context, id string) error {
	v, ok := f.versions[id]
	if !ok {
		return sql.ErrNoRows
	}
	delete(f.versions, id)
	delete(f.byName, v.Name)
	return nil
}

func (f *fakeAVStore) CountAppsUsingVersion(_ context.Context, _ null.String) (int64, error) {
	return f.appCount, nil
}

func (f *fakeAVStore) CountOrgsUsingVersion(_ context.Context, _ string) (int64, error) {
	return f.orgCount, nil
}

// platformPrincipal 是测试公用平台管理员主体。
func platformPrincipal() auth.Principal {
	return auth.Principal{UserID: "00000000-0000-0000-0000-0000000000ff", Role: domain.UserRolePlatformAdmin}
}

// TestAssistantVersionListAllowsMember 验证 CanViewAssistantVersion 扩展后 org_member 可正常读版本列表。
// org_member 需要在应用概览中查询版本名称，因此后端开放了该接口。
func TestAssistantVersionListAllowsMember(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	// org_member 调用 List 应无权限错误，返回空列表（存根无数据）。
	_, err := svc.List(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember})
	require.NoError(t, err, "CanViewAssistantVersion 已扩展至 org_member，List 应返回 nil 错误")
}

// TestAssistantVersionGetNotFound 验证查询不存在的版本返回 NotFound。
func TestAssistantVersionGetNotFound(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	_, err := svc.Get(context.Background(), platformPrincipal(), "00000000-0000-0000-0000-0000000000a1")
	require.ErrorIs(t, err, ErrAssistantVersionNotFound)
}

// mustParseUUID 直接返回字符串 UUID（MySQL 侧 CHAR(36)，无需解析）；保留名称供调用方不变。
func mustParseUUID(s string) string {
	return s
}

// fakePlatformSkillLibrary 是 PlatformSkillLibrary 的内存实现，按 name+version 索引。
type fakePlatformSkillLibrary struct {
	// skills 是按 "name@version" 为键的平台库 skill 集合。
	skills map[string]sqlc.PlatformSkill
}

func newFakePlatformSkillLibrary() *fakePlatformSkillLibrary {
	return &fakePlatformSkillLibrary{skills: map[string]sqlc.PlatformSkill{}}
}

// addSkill 向桩库插入一条平台库 skill 记录，便于测试预置数据。
func (f *fakePlatformSkillLibrary) addSkill(name, version, tarPath string, size int64, sha string) {
	f.skills[name+"@"+version] = sqlc.PlatformSkill{
		ID: "ps-" + name + "-" + version, Name: name, Version: version,
		TarPath: tarPath, FileSize: size, FileSha256: sha,
	}
}

func (f *fakePlatformSkillLibrary) GetPlatformSkillByNameVersion(_ context.Context, arg sqlc.GetPlatformSkillByNameVersionParams) (sqlc.PlatformSkill, error) {
	ps, ok := f.skills[arg.Name+"@"+arg.Version]
	if !ok {
		return sqlc.PlatformSkill{}, sql.ErrNoRows
	}
	return ps, nil
}

// newTestAVService 用内存桩构造版本 service，默认模型与镜像校验全部通过，平台库为空。
func newTestAVService(t *testing.T, store *fakeAVStore) *AssistantVersionService {
	t.Helper()
	return NewAssistantVersionService(store, fakeImageResolver{}, fakeModelValidator{}, newFakePlatformSkillLibrary())
}

// newTestAVServiceWithLibrary 构造版本 service 并注入自定义平台库桩。
func newTestAVServiceWithLibrary(t *testing.T, store *fakeAVStore, lib *fakePlatformSkillLibrary) *AssistantVersionService {
	t.Helper()
	return NewAssistantVersionService(store, fakeImageResolver{}, fakeModelValidator{}, lib)
}

// fakeImageResolver 默认认为所有 image_id 都存在，并能列出一个镜像。
type fakeImageResolver struct{}

func (fakeImageResolver) HasRuntimeImage(string) bool { return true }
func (fakeImageResolver) ListRuntimeImages() []RuntimeImageOption {
	return []RuntimeImageOption{{ID: "v2026.5.16", Label: "当前"}}
}

// fakeModelValidator 默认认为所有模型名都存在。
type fakeModelValidator struct{}

func (fakeModelValidator) HasModel(string) bool { return true }

// TestAssistantVersionListReturnsVersions 验证有权限时 List 返回库中的版本，并正确反序列化 routing/skills。
func TestAssistantVersionListReturnsVersions(t *testing.T) {
	store := newFakeAVStore()
	id := mustParseUUID("00000000-0000-0000-0000-0000000000d1")
	store.versions[id] = sqlc.AssistantVersion{
		ID: id, Name: "标准版", Description: "默认", SystemPrompt: "p",
		ImageID: "v2026.5.16", MainModel: "qwen",
		RoutingJson: []byte(`{"vision":"gpt"}`), SkillsJson: []byte(`[]`), Revision: 2,
	}
	svc := newTestAVService(t, store)
	out, err := svc.List(context.Background(), platformPrincipal())
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "标准版", out[0].Name)
	assert.Equal(t, "gpt", out[0].Routing["vision"])
	assert.EqualValues(t, 2, out[0].Revision)
}

// TestAssistantVersionGetReturnsVersion 验证有权限时 Get 按 id 返回正确组装的版本视图。
func TestAssistantVersionGetReturnsVersion(t *testing.T) {
	store := newFakeAVStore()
	id := mustParseUUID("00000000-0000-0000-0000-0000000000d2")
	store.versions[id] = sqlc.AssistantVersion{
		ID: id, Name: "高级版", SystemPrompt: "p", ImageID: "v2026.5.16",
		MainModel: "qwen", RoutingJson: []byte(`{}`), SkillsJson: []byte(`[]`), Revision: 1,
	}
	svc := newTestAVService(t, store)
	got, err := svc.Get(context.Background(), platformPrincipal(), id)
	require.NoError(t, err)
	assert.Equal(t, "高级版", got.Name)
	assert.Equal(t, "qwen", got.MainModel)
}

// TestAssistantVersionGetRejectsInvalidUUID 验证传入非法 UUID 字符串时返回 NotFound。
func TestAssistantVersionGetRejectsInvalidUUID(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	_, err := svc.Get(context.Background(), platformPrincipal(), "not-a-uuid")
	require.ErrorIs(t, err, ErrAssistantVersionNotFound)
}

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
	svc := NewAssistantVersionService(newFakeAVStore(), rejectingImageResolver{}, fakeModelValidator{}, newFakePlatformSkillLibrary())
	_, err := svc.Create(context.Background(), platformPrincipal(), validCreateInput())
	require.ErrorIs(t, err, ErrAssistantVersionInvalid)
}

// TestAssistantVersionCreateRejectsUnknownModel 验证主模型不存在时报 Invalid。
func TestAssistantVersionCreateRejectsUnknownModel(t *testing.T) {
	svc := NewAssistantVersionService(newFakeAVStore(), fakeImageResolver{}, rejectingModelValidator{}, newFakePlatformSkillLibrary())
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

// rejectingImageResolver 认为所有 image_id 都不存在；ListRuntimeImages 返回空。
type rejectingImageResolver struct{}

func (rejectingImageResolver) HasRuntimeImage(string) bool             { return false }
func (rejectingImageResolver) ListRuntimeImages() []RuntimeImageOption { return nil }

// rejectingModelValidator 认为所有模型都不存在。
type rejectingModelValidator struct{}

func (rejectingModelValidator) HasModel(string) bool { return false }

// TestAssistantVersionCreateWrapsStoreError 验证底层 store 写入失败时 Create 返回包装后的错误。
func TestAssistantVersionCreateWrapsStoreError(t *testing.T) {
	store := newFakeAVStore()
	store.createErr = errors.New("db down")
	svc := newTestAVService(t, store)
	_, err := svc.Create(context.Background(), platformPrincipal(), validCreateInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

// seedVersion 在 fakeAVStore 内放一个已存在版本，返回其 id（string）。
func seedVersion(store *fakeAVStore, name string, revision int32) string {
	id := mustParseUUID("00000000-0000-0000-0000-0000000000b1")
	v := sqlc.AssistantVersion{
		ID: id, Name: name, SystemPrompt: "p", ImageID: "v2026.5.16", MainModel: "qwen",
		RoutingJson: []byte("{}"), SkillsJson: []byte("[]"), Revision: revision,
	}
	store.versions[id] = v
	store.byName[name] = v
	return id
}

// TestAssistantVersionUpdateBumpsRevisionOnPromptChange 验证仅改提示词（其它容器相关字段不变）会 revision +1。
func TestAssistantVersionUpdateBumpsRevisionOnPromptChange(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 3)
	svc := newTestAVService(t, store)
	in := AssistantVersionInput{
		Name: "标准版", Description: "默认版本", SystemPrompt: "新的提示词",
		ImageID: "v2026.5.16", MainModel: "qwen", Routing: map[string]string{},
	}
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
	store.byName["高级版"] = sqlc.AssistantVersion{ID: mustParseUUID("00000000-0000-0000-0000-0000000000c9"), Name: "高级版"}
	svc := newTestAVService(t, store)
	in := validCreateInput()
	in.Name = "高级版"
	_, err := svc.Update(context.Background(), platformPrincipal(), id, in)
	require.ErrorIs(t, err, ErrAssistantVersionNameTaken)
}

// TestAssistantVersionUpdateBumpsRevisionOnRoutingChange 验证仅改智能路由（其它字段不变）也会 revision +1。
func TestAssistantVersionUpdateBumpsRevisionOnRoutingChange(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 3)
	svc := newTestAVService(t, store)
	in := AssistantVersionInput{
		Name: "标准版", Description: "默认版本", SystemPrompt: "p",
		ImageID: "v2026.5.16", MainModel: "qwen", Routing: map[string]string{"vision": "gpt"},
	}
	got, err := svc.Update(context.Background(), platformPrincipal(), id, in)
	require.NoError(t, err)
	assert.EqualValues(t, 4, got.Revision)
}

// TestAssistantVersionUpdateNotFound 验证更新不存在的版本返回 NotFound。
func TestAssistantVersionUpdateNotFound(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	_, err := svc.Update(context.Background(), platformPrincipal(), "00000000-0000-0000-0000-0000000000e9", validCreateInput())
	require.ErrorIs(t, err, ErrAssistantVersionNotFound)
}

// TestAssistantVersionUpdateDeniesOrgAdmin 验证组织管理员不能更新版本。
func TestAssistantVersionUpdateDeniesOrgAdmin(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	svc := newTestAVService(t, store)
	_, err := svc.Update(context.Background(), orgAdminPrincipal(), id, validCreateInput())
	require.ErrorIs(t, err, ErrAssistantVersionDenied)
}

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

// TestAssistantVersionDeleteRejectsOrgInUse 验证出现在企业 allowlist 时拒绝删除。
func TestAssistantVersionDeleteRejectsOrgInUse(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	store.orgCount = 1
	svc := newTestAVService(t, store)
	err := svc.Delete(context.Background(), platformPrincipal(), id)
	require.ErrorIs(t, err, ErrAssistantVersionInUse)
	require.ErrorContains(t, err, "企业 allowlist")
}

// TestAssistantVersionDeleteDeniesOrgAdmin 验证组织管理员不能删除版本。
func TestAssistantVersionDeleteDeniesOrgAdmin(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	svc := newTestAVService(t, store)
	err := svc.Delete(context.Background(), orgAdminPrincipal(), id)
	require.ErrorIs(t, err, ErrAssistantVersionDenied)
}

// TestAssistantVersionDeleteNotFound 验证删除不存在的版本返回 NotFound。
func TestAssistantVersionDeleteNotFound(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	err := svc.Delete(context.Background(), platformPrincipal(), "00000000-0000-0000-0000-0000000000e7")
	require.ErrorIs(t, err, ErrAssistantVersionNotFound)
}

// weatherSkillInput 返回一个指向平台库 weather skill 的从库选入参。
func weatherSkillInput() AddSkillFromLibraryInput {
	return AddSkillFromLibraryInput{Source: "platform", SourceRef: "weather", Version: "1.0.0"}
}

// libWithWeather 构造一个含 weather v1.0.0 的平台库桩。
func libWithWeather() *fakePlatformSkillLibrary {
	lib := newFakePlatformSkillLibrary()
	lib.addSkill("weather", "1.0.0", "library/platform/weather/1.0.0.tar", 1024, "abc123sha")
	return lib
}

// TestAssistantVersionAddSkillFromLibraryOK 验证从平台库选 skill 后版本 skills 增加且 revision +1。
func TestAssistantVersionAddSkillFromLibraryOK(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 2)
	svc := newTestAVServiceWithLibrary(t, store, libWithWeather())
	// 平台管理员选 weather v1.0.0 配进版本，应成功返回含该 skill 的快照。
	got, err := svc.AddSkillFromLibrary(context.Background(), platformPrincipal(), id, weatherSkillInput())
	require.NoError(t, err)
	require.Len(t, got.Skills, 1)
	assert.Equal(t, "weather", got.Skills[0].Name)
	assert.Equal(t, "platform", got.Skills[0].Source)
	assert.Equal(t, "1.0.0", got.Skills[0].Version)
	assert.Equal(t, "library/platform/weather/1.0.0.tar", got.Skills[0].CachedPath)
	assert.EqualValues(t, 3, got.Revision)
}

// TestAssistantVersionAddSkillFromLibraryVersionNotFound 验证版本不存在时返回 NotFound。
func TestAssistantVersionAddSkillFromLibraryVersionNotFound(t *testing.T) {
	svc := newTestAVServiceWithLibrary(t, newFakeAVStore(), libWithWeather())
	// 版本不存在，应返回 ErrAssistantVersionNotFound。
	_, err := svc.AddSkillFromLibrary(context.Background(), platformPrincipal(), "00000000-0000-0000-0000-000000000099", weatherSkillInput())
	require.ErrorIs(t, err, ErrAssistantVersionNotFound)
}

// TestAssistantVersionAddSkillFromLibrarySkillNotFound 验证平台库 skill 不存在时返回 ErrPlatformSkillNotFound。
func TestAssistantVersionAddSkillFromLibrarySkillNotFound(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	// 空平台库：请求的 skill 不存在，应返回 ErrPlatformSkillNotFound。
	svc := newTestAVServiceWithLibrary(t, store, newFakePlatformSkillLibrary())
	_, err := svc.AddSkillFromLibrary(context.Background(), platformPrincipal(), id, weatherSkillInput())
	require.ErrorIs(t, err, ErrPlatformSkillNotFound)
}

// TestAssistantVersionAddSkillFromLibraryNameTaken 验证同版本内 skill 同名冲突时返回 ErrAssistantVersionSkillNameTaken。
func TestAssistantVersionAddSkillFromLibraryNameTaken(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	lib := libWithWeather()
	svc := newTestAVServiceWithLibrary(t, store, lib)
	// 第一次添加 weather，应成功。
	_, err := svc.AddSkillFromLibrary(context.Background(), platformPrincipal(), id, weatherSkillInput())
	require.NoError(t, err)
	// 第二次添加同名 skill，应返回 ErrAssistantVersionSkillNameTaken。
	_, err = svc.AddSkillFromLibrary(context.Background(), platformPrincipal(), id, weatherSkillInput())
	require.ErrorIs(t, err, ErrAssistantVersionSkillNameTaken)
}

// TestAssistantVersionAddSkillFromLibraryDeniesOrgAdmin 验证组织管理员不能从库选 skill。
func TestAssistantVersionAddSkillFromLibraryDeniesOrgAdmin(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	svc := newTestAVServiceWithLibrary(t, store, libWithWeather())
	// 非平台管理员调用，应返回 ErrAssistantVersionDenied。
	_, err := svc.AddSkillFromLibrary(context.Background(), orgAdminPrincipal(), id, weatherSkillInput())
	require.ErrorIs(t, err, ErrAssistantVersionDenied)
}

// TestAssistantVersionDeleteSkillOK 验证删除已存在 skill 后 skills 清空且 revision 递增。
func TestAssistantVersionDeleteSkillOK(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	lib := libWithWeather()
	svc := newTestAVServiceWithLibrary(t, store, lib)
	// 先从库选 weather 配进版本，再删除，期望 skills 为空。
	_, err := svc.AddSkillFromLibrary(context.Background(), platformPrincipal(), id, weatherSkillInput())
	require.NoError(t, err)
	got, err := svc.DeleteSkill(context.Background(), platformPrincipal(), id, "weather")
	require.NoError(t, err)
	assert.Empty(t, got.Skills)
}

// TestAssistantVersionDeleteSkillNotFound 验证删除不存在的 skill 报 Invalid。
func TestAssistantVersionDeleteSkillNotFound(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	svc := newTestAVService(t, store)
	// 版本内无任何 skill，删除应返回 ErrAssistantVersionInvalid。
	_, err := svc.DeleteSkill(context.Background(), platformPrincipal(), id, "nope")
	require.ErrorIs(t, err, ErrAssistantVersionInvalid)
}

// TestAssistantVersionDeleteSkillDeniesOrgAdmin 验证组织管理员不能删除 skill。
func TestAssistantVersionDeleteSkillDeniesOrgAdmin(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	svc := newTestAVService(t, store)
	// 非平台管理员调用，应返回 ErrAssistantVersionDenied。
	_, err := svc.DeleteSkill(context.Background(), orgAdminPrincipal(), id, "weather")
	require.ErrorIs(t, err, ErrAssistantVersionDenied)
}

// TestAssistantVersionListRuntimeImagesDeniesMember 验证普通成员不能读取镜像列表。
func TestAssistantVersionListRuntimeImagesDeniesMember(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	_, err := svc.ListRuntimeImages(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember})
	require.ErrorIs(t, err, ErrAssistantVersionDenied)
}

// TestAssistantVersionValidateIDsOK 验证全部 id 存在时返回去重后的列表。
func TestAssistantVersionValidateIDsOK(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	svc := newTestAVService(t, store)
	out, err := svc.ValidateAssistantVersionIDs(context.Background(), []string{id, id})
	require.NoError(t, err)
	assert.Equal(t, []string{id}, out)
}

// TestAssistantVersionValidateIDsRejectsUnknown 验证含不存在 id 时报 Invalid。
func TestAssistantVersionValidateIDsRejectsUnknown(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	_, err := svc.ValidateAssistantVersionIDs(context.Background(), []string{"00000000-0000-0000-0000-0000000000e1"})
	require.ErrorIs(t, err, ErrAssistantVersionInvalid)
}

// TestAssistantVersionValidateIDsEmpty 验证空列表合法（组织可不配版本）。
func TestAssistantVersionValidateIDsEmpty(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	out, err := svc.ValidateAssistantVersionIDs(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, out)
}
