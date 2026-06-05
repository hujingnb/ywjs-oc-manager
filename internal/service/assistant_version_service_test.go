package service

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"sort"
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
	versions               map[string]sqlc.AssistantVersion
	byName                 map[string]sqlc.AssistantVersion
	industryBases          map[string]sqlc.IndustryKnowledgeBasis
	versionIndustryBaseIDs map[string][]string
	appCount               int64
	orgCount               int64
	createErr              error
	updateErr              error
	addIndustryErrAfter    int
	addIndustryCalls       int
}

func newFakeAVStore() *fakeAVStore {
	return &fakeAVStore{
		versions:               map[string]sqlc.AssistantVersion{},
		byName:                 map[string]sqlc.AssistantVersion{},
		industryBases:          map[string]sqlc.IndustryKnowledgeBasis{},
		versionIndustryBaseIDs: map[string][]string{},
	}
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
		ID:           arg.ID,
		Name:         arg.Name,
		Description:  arg.Description,
		SystemPrompt: arg.SystemPrompt,
		ImageID:      arg.ImageID,
		MainModel:    arg.MainModel,
		RoutingJson:  arg.RoutingJson,
		SkillsJson:   arg.SkillsJson,
		Revision:     1,
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

// addIndustryKnowledgeBase 预置行业知识库元数据，供版本关联校验和返回名称使用。
func (f *fakeAVStore) addIndustryKnowledgeBase(id, name string) {
	f.industryBases[id] = sqlc.IndustryKnowledgeBasis{ID: id, Name: name}
}

// GetIndustryKnowledgeBase 按 ID 读取行业知识库；不存在时模拟 sqlc 的 sql.ErrNoRows。
func (f *fakeAVStore) GetIndustryKnowledgeBase(_ context.Context, id string) (sqlc.IndustryKnowledgeBasis, error) {
	base, ok := f.industryBases[id]
	if !ok {
		return sqlc.IndustryKnowledgeBasis{}, sql.ErrNoRows
	}
	return base, nil
}

// ReplaceAssistantVersionIndustryKnowledgeBases 清空版本旧行业库关联，等待 service 重新插入。
func (f *fakeAVStore) ReplaceAssistantVersionIndustryKnowledgeBases(_ context.Context, versionID string) error {
	f.versionIndustryBaseIDs[versionID] = nil
	return nil
}

// AddAssistantVersionIndustryKnowledgeBase 为版本追加一个行业库关联。
func (f *fakeAVStore) AddAssistantVersionIndustryKnowledgeBase(_ context.Context, arg sqlc.AddAssistantVersionIndustryKnowledgeBaseParams) (int64, error) {
	f.addIndustryCalls += 1
	if f.addIndustryErrAfter > 0 && f.addIndustryCalls >= f.addIndustryErrAfter {
		return 0, errors.New("insert industry association failed")
	}
	if _, ok := f.industryBases[arg.IndustryKnowledgeBaseID]; !ok {
		return 0, nil
	}
	f.versionIndustryBaseIDs[arg.VersionID] = append(f.versionIndustryBaseIDs[arg.VersionID], arg.IndustryKnowledgeBaseID)
	return 1, nil
}

// ListIndustryKnowledgeBasesByAssistantVersion 返回版本当前关联行业库，并按真实查询的名称/id 顺序排序。
func (f *fakeAVStore) ListIndustryKnowledgeBasesByAssistantVersion(_ context.Context, versionID string) ([]sqlc.IndustryKnowledgeBasis, error) {
	ids := f.versionIndustryBaseIDs[versionID]
	out := make([]sqlc.IndustryKnowledgeBasis, 0, len(ids))
	for _, id := range ids {
		if base, ok := f.industryBases[id]; ok {
			out = append(out, base)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// fakeAVTxRunner 用内存快照模拟数据库事务，失败时丢弃临时 store 的所有写入。
type fakeAVTxRunner struct {
	store *fakeAVStore
}

func (r fakeAVTxRunner) WithAssistantVersionTx(ctx context.Context, fn func(AssistantVersionStore) error) error {
	txStore := r.store.clone()
	if err := fn(txStore); err != nil {
		return err
	}
	r.store.copyFrom(txStore)
	return nil
}

// clone 复制 fake store 的可变 map/slice，保证事务内写入不会污染原始状态。
func (f *fakeAVStore) clone() *fakeAVStore {
	clone := newFakeAVStore()
	for id, row := range f.versions {
		clone.versions[id] = row
	}
	for name, row := range f.byName {
		clone.byName[name] = row
	}
	for id, row := range f.industryBases {
		clone.industryBases[id] = row
	}
	for versionID, ids := range f.versionIndustryBaseIDs {
		clone.versionIndustryBaseIDs[versionID] = append([]string(nil), ids...)
	}
	clone.appCount = f.appCount
	clone.orgCount = f.orgCount
	clone.createErr = f.createErr
	clone.updateErr = f.updateErr
	clone.addIndustryErrAfter = f.addIndustryErrAfter
	return clone
}

// copyFrom 提交事务快照，只在 fn 成功时覆盖原始 fake store。
func (f *fakeAVStore) copyFrom(src *fakeAVStore) {
	f.versions = src.versions
	f.byName = src.byName
	f.industryBases = src.industryBases
	f.versionIndustryBaseIDs = src.versionIndustryBaseIDs
	f.appCount = src.appCount
	f.orgCount = src.orgCount
	f.createErr = src.createErr
	f.updateErr = src.updateErr
	f.addIndustryErrAfter = src.addIndustryErrAfter
	f.addIndustryCalls = src.addIndustryCalls
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
func (f *fakeLibBlob) DeleteLibrarySkill(string) error                { return nil }
func (f *fakeLibBlob) OpenLibrarySkill(string) (io.ReadCloser, error) { return nil, nil }

// newTestAVService 用内存桩构造版本 service，默认模型与镜像校验全部通过，平台库为空。
// clawhub/blobs 均传 nil：platform-only 用例不需要 clawhub 能力。
func newTestAVService(t *testing.T, store *fakeAVStore) *AssistantVersionService {
	t.Helper()
	return NewAssistantVersionService(store, fakeImageResolver{}, fakeModelValidator{}, newFakePlatformSkillLibrary(), nil, nil)
}

// newTestAVServiceWithLibrary 构造版本 service 并注入自定义平台库桩。
// clawhub/blobs 均传 nil：platform-only 用例不需要 clawhub 能力。
func newTestAVServiceWithLibrary(t *testing.T, store *fakeAVStore, lib *fakePlatformSkillLibrary) *AssistantVersionService {
	t.Helper()
	return NewAssistantVersionService(store, fakeImageResolver{}, fakeModelValidator{}, lib, nil, nil)
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

// TestAssistantVersionCreateWithIndustryKnowledgeBases 验证创建版本时可同时保存行业知识库关联。
func TestAssistantVersionCreateWithIndustryKnowledgeBases(t *testing.T) {
	store := newFakeAVStore()
	// 预置一个可关联的行业库，创建后返回视图应包含其 id/name 引用。
	store.addIndustryKnowledgeBase("kb-risk", "金融风控")
	svc := newTestAVService(t, store)
	in := validCreateInput()
	in.IndustryKnowledgeBaseIDs = []string{" kb-risk ", "", "kb-risk"} // 覆盖 trim、空值过滤和去重。
	in.ReplaceIndustryKnowledgeBases = true
	got, err := svc.Create(context.Background(), platformPrincipal(), in)
	require.NoError(t, err)
	assert.EqualValues(t, 1, got.Revision)
	require.Len(t, got.IndustryKnowledgeBases, 1)
	assert.Equal(t, IndustryKnowledgeBaseRef{ID: "kb-risk", Name: "金融风控"}, got.IndustryKnowledgeBases[0])
	assert.Equal(t, []string{"kb-risk"}, store.versionIndustryBaseIDs[got.ID])
}

// TestAssistantVersionCreateRollsBackIndustryAssociationFailure 验证创建时关联写入失败不会留下同名孤儿版本。
func TestAssistantVersionCreateRollsBackIndustryAssociationFailure(t *testing.T) {
	store := newFakeAVStore()
	// 两个行业库都存在，失败只来自第二条关联插入，用于覆盖 create + association 的事务回滚。
	store.addIndustryKnowledgeBase("kb-risk", "金融风控")
	store.addIndustryKnowledgeBase("kb-law", "法律法规")
	store.addIndustryErrAfter = 2
	svc := newTestAVService(t, store)
	svc.SetTxRunner(fakeAVTxRunner{store: store})
	in := validCreateInput()
	in.IndustryKnowledgeBaseIDs = []string{"kb-risk", "kb-law"}
	in.ReplaceIndustryKnowledgeBases = true

	_, err := svc.Create(context.Background(), platformPrincipal(), in)
	require.ErrorContains(t, err, "保存版本行业知识库关联失败")

	assert.Empty(t, store.versions)
	assert.NotContains(t, store.byName, in.Name)
	assert.Empty(t, store.versionIndustryBaseIDs)
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
	svc := NewAssistantVersionService(newFakeAVStore(), rejectingImageResolver{}, fakeModelValidator{}, newFakePlatformSkillLibrary(), nil, nil)
	_, err := svc.Create(context.Background(), platformPrincipal(), validCreateInput())
	require.ErrorIs(t, err, ErrAssistantVersionInvalid)
}

// TestAssistantVersionCreateRejectsUnknownModel 验证主模型不存在时报 Invalid。
func TestAssistantVersionCreateRejectsUnknownModel(t *testing.T) {
	svc := NewAssistantVersionService(newFakeAVStore(), fakeImageResolver{}, rejectingModelValidator{}, newFakePlatformSkillLibrary(), nil, nil)
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

// TestAssistantVersionUpdateIndustryKnowledgeDoesNotBumpRevision 验证只替换行业库关联不递增版本 revision。
func TestAssistantVersionUpdateIndustryKnowledgeDoesNotBumpRevision(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 3)
	// 行业库名称来自行业库表，版本返回值只暴露运行时需要的 id/name 引用。
	store.addIndustryKnowledgeBase("kb-risk", "金融风控")
	svc := newTestAVService(t, store)
	in := AssistantVersionInput{
		Name: "标准版", Description: "默认版本", SystemPrompt: "p",
		ImageID: "v2026.5.16", MainModel: "qwen", Routing: map[string]string{},
		IndustryKnowledgeBaseIDs:      []string{" kb-risk "},
		ReplaceIndustryKnowledgeBases: true,
	}
	got, err := svc.Update(context.Background(), platformPrincipal(), id, in)
	require.NoError(t, err)
	assert.EqualValues(t, 3, got.Revision)
	require.Len(t, got.IndustryKnowledgeBases, 1)
	assert.Equal(t, IndustryKnowledgeBaseRef{ID: "kb-risk", Name: "金融风控"}, got.IndustryKnowledgeBases[0])
}

// TestAssistantVersionUpdateKeepsIndustryKnowledgeWhenOmitted 验证未显式提交行业库字段时保留旧关联，兼容旧客户端。
func TestAssistantVersionUpdateKeepsIndustryKnowledgeWhenOmitted(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 3)
	// 旧关联代表已有运行时检索范围；Update 入参省略行业库字段时不应被清空。
	store.addIndustryKnowledgeBase("kb-old", "已有行业库")
	store.versionIndustryBaseIDs[id] = []string{"kb-old"}
	svc := newTestAVService(t, store)

	got, err := svc.Update(context.Background(), platformPrincipal(), id, AssistantVersionInput{
		Name: "标准版", Description: "仅更新描述", SystemPrompt: "p",
		ImageID: "v2026.5.16", MainModel: "qwen", Routing: map[string]string{},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"kb-old"}, store.versionIndustryBaseIDs[id])
	require.Len(t, got.IndustryKnowledgeBases, 1)
	assert.Equal(t, "已有行业库", got.IndustryKnowledgeBases[0].Name)
}

// TestAssistantVersionUpdateClearsIndustryKnowledgeWhenExplicitEmpty 验证显式提交空列表时会清空行业库关联。
func TestAssistantVersionUpdateClearsIndustryKnowledgeWhenExplicitEmpty(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 3)
	// 显式空数组表示平台管理员主动清空运行时额外检索范围。
	store.addIndustryKnowledgeBase("kb-old", "已有行业库")
	store.versionIndustryBaseIDs[id] = []string{"kb-old"}
	svc := newTestAVService(t, store)

	got, err := svc.Update(context.Background(), platformPrincipal(), id, AssistantVersionInput{
		Name: "标准版", Description: "仅更新描述", SystemPrompt: "p",
		ImageID: "v2026.5.16", MainModel: "qwen", Routing: map[string]string{},
		IndustryKnowledgeBaseIDs:      []string{},
		ReplaceIndustryKnowledgeBases: true,
	})
	require.NoError(t, err)

	assert.Empty(t, store.versionIndustryBaseIDs[id])
	assert.Empty(t, got.IndustryKnowledgeBases)
}

// TestAssistantVersionUpdateRejectsUnknownIndustryKnowledgeWithoutClearingExisting 验证未知行业库不会清空旧关联。
func TestAssistantVersionUpdateRejectsUnknownIndustryKnowledgeWithoutClearingExisting(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 3)
	// 旧关联用于确认失败路径不会先 delete 再校验，避免丢失运行时配置。
	store.addIndustryKnowledgeBase("kb-old", "已有行业库")
	store.versionIndustryBaseIDs[id] = []string{"kb-old"}
	svc := newTestAVService(t, store)
	in := AssistantVersionInput{
		Name: "标准版", Description: "默认版本", SystemPrompt: "p",
		ImageID: "v2026.5.16", MainModel: "qwen", Routing: map[string]string{},
		IndustryKnowledgeBaseIDs:      []string{"kb-missing"},
		ReplaceIndustryKnowledgeBases: true,
	}
	_, err := svc.Update(context.Background(), platformPrincipal(), id, in)
	require.ErrorIs(t, err, ErrIndustryKnowledgeNotFound)
	assert.Equal(t, []string{"kb-old"}, store.versionIndustryBaseIDs[id])
}

// TestAssistantVersionUpdateRollsBackIndustryAssociationFailure 验证关联写入失败时版本字段和旧关联一起回滚。
func TestAssistantVersionUpdateRollsBackIndustryAssociationFailure(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 3)
	// 三个行业库都存在，失败只来自第二条关联插入，覆盖事务内部分写入失败的边界。
	store.addIndustryKnowledgeBase("kb-old", "旧行业库")
	store.addIndustryKnowledgeBase("kb-risk", "金融风控")
	store.addIndustryKnowledgeBase("kb-law", "法律法规")
	store.versionIndustryBaseIDs[id] = []string{"kb-old"}
	oldRow := store.versions[id]
	store.addIndustryErrAfter = 2
	svc := newTestAVService(t, store)
	svc.SetTxRunner(fakeAVTxRunner{store: store})

	_, err := svc.Update(context.Background(), platformPrincipal(), id, AssistantVersionInput{
		Name: "高级版", Description: "更新描述", SystemPrompt: "new prompt",
		ImageID: "v2026.5.16", MainModel: "qwen", Routing: map[string]string{},
		IndustryKnowledgeBaseIDs:      []string{"kb-risk", "kb-law"},
		ReplaceIndustryKnowledgeBases: true,
	})
	require.ErrorContains(t, err, "保存版本行业知识库关联失败")

	row := store.versions[id]
	assert.Equal(t, oldRow.Name, row.Name)
	assert.Equal(t, oldRow.Description, row.Description)
	assert.Equal(t, oldRow.SystemPrompt, row.SystemPrompt)
	assert.Equal(t, oldRow.Revision, row.Revision)
	assert.Equal(t, []string{"kb-old"}, store.versionIndustryBaseIDs[id])
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

// TestAddSkillFromLibrary_ClawHub 覆盖：source=clawhub 时下载 zip → 缓存对象存储 →
// 本地算 sha256 → 写入 skills_json 自包含快照（name 用入参 displayName，cached_path 为 .zip）。
func TestAddSkillFromLibrary_ClawHub(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	blob := &fakeLibBlob{}
	svc := NewAssistantVersionService(
		store, fakeImageResolver{}, fakeModelValidator{}, newFakePlatformSkillLibrary(),
		fakeClawHub{archive: []byte("PK\x03\x04zip-bytes")}, blob,
	)
	out, err := svc.AddSkillFromLibrary(context.Background(), platformPrincipal(), id, AddSkillFromLibraryInput{
		Source: "clawhub", SourceRef: "skill-vetter", Name: "Skill Vetter", Version: "1.0.0",
	})
	require.NoError(t, err)
	require.Len(t, out.Skills, 1)
	got := out.Skills[0]
	// 来源透传
	assert.Equal(t, "clawhub", got.Source)
	// slug 透传
	assert.Equal(t, "skill-vetter", got.SourceRef)
	// 目录名用 displayName（非 slug）
	assert.Equal(t, "Skill Vetter", got.Name)
	// 锁定版本
	assert.Equal(t, "1.0.0", got.Version)
	// 缓存为 .zip
	assert.Equal(t, "library/clawhub/skill-vetter/1.0.0.zip", got.CachedPath)
	// 本地计算 sha256，非空
	assert.NotEmpty(t, got.FileSha256)
	// PutLibrarySkill 以 zip 扩展名缓存
	assert.Equal(t, "zip", blob.putExt)
}

// TestAddSkillFromLibrary_ClawHub_NilDownloader 覆盖：clawhub 未配置（nil）时返回 ErrAppSkillSourceUnknown。
func TestAddSkillFromLibrary_ClawHub_NilDownloader(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	// clawhub/blobs 均传 nil，模拟未配置 ClawHub BaseURL 的情形。
	svc := NewAssistantVersionService(
		store, fakeImageResolver{}, fakeModelValidator{}, newFakePlatformSkillLibrary(), nil, nil,
	)
	_, err := svc.AddSkillFromLibrary(context.Background(), platformPrincipal(), id, AddSkillFromLibraryInput{
		Source: "clawhub", SourceRef: "skill-vetter", Name: "Skill Vetter", Version: "1.0.0",
	})
	// clawhub 未配置时应返回 ErrAppSkillSourceUnknown。
	require.ErrorIs(t, err, ErrAppSkillSourceUnknown)
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
