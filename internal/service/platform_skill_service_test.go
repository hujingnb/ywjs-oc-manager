package service

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// fakePlatformSkillStore 是 PlatformSkillStore 的内存实现，供 service 单测使用。
type fakePlatformSkillStore struct {
	rows      map[string]sqlc.PlatformSkill
	byNameVer map[string]sqlc.PlatformSkill
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
	if r, ok := f.rows[id]; ok {
		delete(f.byNameVer, r.Name+"|"+r.Version)
	}
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

func psvcPlatformPrincipal() auth.Principal {
	return auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin}
}
func psvcOrgMemberPrincipal() auth.Principal {
	return auth.Principal{UserID: "u-mem", Role: domain.UserRoleOrgMember}
}

// 上传成功：落库 + 写 blob，返回 Result 含正确 name/version/size/sha256。
func TestPlatformSkillService_Upload_OK(t *testing.T) {
	store := newFakePlatformSkillStore()
	blob := &fakeLibraryBlob{}
	svc := NewPlatformSkillService(store, blob)
	data := []byte("skill-archive-bytes")

	res, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{
		Name: "weather", Version: "1.0", Description: "天气", Data: data,
	})
	require.NoError(t, err)
	assert.Equal(t, "weather", res.Name)
	assert.Equal(t, "1.0", res.Version)
	assert.EqualValues(t, len(data), res.FileSize)
	assert.Len(t, res.FileSha256, 64)
	assert.Equal(t, "library/platform/weather/1.0.tar", store.rows[res.ID].TarPath)
}

// 非平台管理员上传被拒。
func TestPlatformSkillService_Upload_Denied(t *testing.T) {
	svc := NewPlatformSkillService(newFakePlatformSkillStore(), &fakeLibraryBlob{})
	_, err := svc.Upload(context.Background(), psvcOrgMemberPrincipal(), PlatformSkillUploadInput{Name: "x", Version: "1", Data: []byte("a")})
	require.ErrorIs(t, err, ErrPlatformSkillDenied)
}

// name / version / data 任一为空 → Invalid。
func TestPlatformSkillService_Upload_Invalid(t *testing.T) {
	svc := NewPlatformSkillService(newFakePlatformSkillStore(), &fakeLibraryBlob{})
	_, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "", Version: "1", Data: []byte("a")})
	require.ErrorIs(t, err, ErrPlatformSkillInvalid)
}

// 同名同版本已存在 → NameVersionTaken。
func TestPlatformSkillService_Upload_Duplicate(t *testing.T) {
	store := newFakePlatformSkillStore()
	svc := NewPlatformSkillService(store, &fakeLibraryBlob{})
	in := PlatformSkillUploadInput{Name: "weather", Version: "1.0", Data: []byte("a")}
	_, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), in)
	require.NoError(t, err)
	_, err = svc.Upload(context.Background(), psvcPlatformPrincipal(), in)
	require.ErrorIs(t, err, ErrPlatformSkillNameVersionTaken)
}

// 删除成功：移除行并删除对应 blob。
func TestPlatformSkillService_Delete_OK(t *testing.T) {
	store := newFakePlatformSkillStore()
	blob := &fakeLibraryBlob{}
	svc := NewPlatformSkillService(store, blob)
	res, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "w", Version: "1", Data: []byte("a")})
	require.NoError(t, err)

	require.NoError(t, svc.Delete(context.Background(), psvcPlatformPrincipal(), res.ID))
	_, ok := store.rows[res.ID]
	assert.False(t, ok)
	assert.Equal(t, []string{"library/platform/w/1.tar"}, blob.deleted)
}

// 删除不存在的 id → NotFound。
func TestPlatformSkillService_Delete_NotFound(t *testing.T) {
	svc := NewPlatformSkillService(newFakePlatformSkillStore(), &fakeLibraryBlob{})
	err := svc.Delete(context.Background(), psvcPlatformPrincipal(), "missing")
	require.ErrorIs(t, err, ErrPlatformSkillNotFound)
}

// 落库失败时回滚已写入的 blob（删除归档），避免对象存储里残留孤儿归档。
func TestPlatformSkillService_Upload_RollbackOnDBError(t *testing.T) {
	store := newFakePlatformSkillStore()
	store.createErr = errors.New("db write failed") // 模拟 CreatePlatformSkill 落库失败
	blob := &fakeLibraryBlob{}
	svc := NewPlatformSkillService(store, blob)

	_, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "w", Version: "1", Data: []byte("a")})
	require.Error(t, err)
	assert.Equal(t, []string{"library/platform/w/1.tar"}, blob.deleted) // 回滚删除了刚写入的归档
}

// List 成功：平台管理员获取全部平台库 skill。
func TestPlatformSkillService_List_OK(t *testing.T) {
	store := newFakePlatformSkillStore()
	svc := NewPlatformSkillService(store, &fakeLibraryBlob{})
	_, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "a", Version: "1", Data: []byte("x")})
	require.NoError(t, err)
	_, err = svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "b", Version: "1", Data: []byte("y")})
	require.NoError(t, err)

	out, err := svc.List(context.Background(), psvcPlatformPrincipal())
	require.NoError(t, err)
	assert.Len(t, out, 2)
}

// List 非平台管理员被拒。
func TestPlatformSkillService_List_Denied(t *testing.T) {
	svc := NewPlatformSkillService(newFakePlatformSkillStore(), &fakeLibraryBlob{})
	_, err := svc.List(context.Background(), psvcOrgMemberPrincipal())
	require.ErrorIs(t, err, ErrPlatformSkillDenied)
}
