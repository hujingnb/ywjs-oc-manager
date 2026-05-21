package service

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
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
		ID: mustParseUUID("00000000-0000-0000-0000-0000000000a1"), Name: arg.Name,
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

func (f *fakeAVStore) CountOrgsUsingVersion(context.Context, string) (int64, error) {
	return f.orgCount, nil
}

// platformPrincipal 是测试公用平台管理员主体。
func platformPrincipal() auth.Principal {
	return auth.Principal{UserID: "00000000-0000-0000-0000-0000000000ff", Role: domain.UserRolePlatformAdmin}
}

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

// mustParseUUID 把字符串解析为 pgtype.UUID，失败即 panic（仅测试用）。
func mustParseUUID(s string) pgtype.UUID {
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

// fakeImageResolver 默认认为所有 image_id 都存在，并能列出一个镜像。
type fakeImageResolver struct{}

func (fakeImageResolver) HasRuntimeImage(string) bool { return true }
func (fakeImageResolver) ListRuntimeImages() []RuntimeImageOption {
	return []RuntimeImageOption{{ID: "v2026.5.16", Label: "当前"}}
}

// fakeModelValidator 默认认为所有模型名都存在。
type fakeModelValidator struct{}

func (fakeModelValidator) HasModel(string) bool { return true }

// fakeBlobStore 在内存里模拟 skill tar 存储。
type fakeBlobStore struct{}

func (fakeBlobStore) PutSkill(versionID, skillName string, _ []byte) (string, error) {
	return "versions/" + versionID + "/skills/" + skillName + ".tar", nil
}
func (fakeBlobStore) DeleteSkill(string) error { return nil }

// TestAssistantVersionListReturnsVersions 验证有权限时 List 返回库中的版本，并正确反序列化 routing/skills。
func TestAssistantVersionListReturnsVersions(t *testing.T) {
	store := newFakeAVStore()
	id := mustParseUUID("00000000-0000-0000-0000-0000000000d1")
	store.versions[uuidToString(id)] = sqlc.AssistantVersion{
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
	store.versions[uuidToString(id)] = sqlc.AssistantVersion{
		ID: id, Name: "高级版", SystemPrompt: "p", ImageID: "v2026.5.16",
		MainModel: "qwen", RoutingJson: []byte(`{}`), SkillsJson: []byte(`[]`), Revision: 1,
	}
	svc := newTestAVService(t, store)
	got, err := svc.Get(context.Background(), platformPrincipal(), uuidToString(id))
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

// seedVersion 在 fakeAVStore 内放一个已存在版本，返回其 id。
func seedVersion(store *fakeAVStore, name string, revision int32) string {
	id := mustParseUUID("00000000-0000-0000-0000-0000000000b1")
	v := sqlc.AssistantVersion{
		ID: id, Name: name, SystemPrompt: "p", ImageID: "v2026.5.16", MainModel: "qwen",
		RoutingJson: []byte("{}"), SkillsJson: []byte("[]"), Revision: revision,
	}
	store.versions[uuidToString(id)] = v
	store.byName[name] = v
	return uuidToString(id)
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

// TestAssistantVersionDeleteNotFound 验证删除不存在的版本返回 NotFound。
func TestAssistantVersionDeleteNotFound(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	err := svc.Delete(context.Background(), platformPrincipal(), "00000000-0000-0000-0000-0000000000e7")
	require.ErrorIs(t, err, ErrAssistantVersionNotFound)
}

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

// TestAssistantVersionUploadSkillDeniesOrgAdmin 验证组织管理员不能上传 skill。
func TestAssistantVersionUploadSkillDeniesOrgAdmin(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	svc := newTestAVService(t, store)
	_, err := svc.UploadSkill(context.Background(), orgAdminPrincipal(), id, buildSkillTar(t, "weather"))
	require.ErrorIs(t, err, ErrAssistantVersionDenied)
}

// TestAssistantVersionUploadSkillRejectsInvalidTar 验证非法 tar（无 SKILL.md）被拒。
func TestAssistantVersionUploadSkillRejectsInvalidTar(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	svc := newTestAVService(t, store)
	_, err := svc.UploadSkill(context.Background(), platformPrincipal(), id, []byte("not a tar"))
	require.ErrorIs(t, err, ErrAssistantVersionInvalid)
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

// TestAssistantVersionDeleteSkillNotFound 验证删除不存在的 skill 报 Invalid。
func TestAssistantVersionDeleteSkillNotFound(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	svc := newTestAVService(t, store)
	_, err := svc.DeleteSkill(context.Background(), platformPrincipal(), id, "nope")
	require.ErrorIs(t, err, ErrAssistantVersionInvalid)
}
