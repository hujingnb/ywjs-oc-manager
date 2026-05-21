package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
