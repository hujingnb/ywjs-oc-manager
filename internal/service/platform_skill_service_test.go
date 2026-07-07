package service

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// makeFlatSkillTar 构造一个遵循「扁平契约」的最小 skill tar：根级 SKILL.md，frontmatter name=name。
// 供 Upload 相关用例提供能通过后端结构校验（hermes.InspectFlatSkillArchive）的合法归档。
func makeFlatSkillTar(t *testing.T, name string) []byte {
	t.Helper()
	md := "---\nname: " + name + "\ndescription: 测试 skill\n---\n# " + name + "\n正文"
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "SKILL.md", Mode: 0o644, Size: int64(len(md)), Typeflag: tar.TypeReg}))
	_, err := tw.Write([]byte(md))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

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

// fakeLibraryBlob 记录 Put/Delete 调用，Put 返回确定性相对路径，并按 relPath 保存字节以供 OpenLibrarySkill 返回。
type fakeLibraryBlob struct {
	deleted []string
	// stored 保存 PutLibrarySkill 写入的字节，key 为 relPath，供 OpenLibrarySkill 按路径返回。
	stored map[string][]byte
}

func (f *fakeLibraryBlob) PutLibrarySkill(source, ref, version, ext string, data []byte) (string, error) {
	rel := "library/" + source + "/" + ref + "/" + version + "." + ext
	if f.stored == nil {
		f.stored = map[string][]byte{}
	}
	f.stored[rel] = data
	return rel, nil
}
func (f *fakeLibraryBlob) DeleteLibrarySkill(rel string) error {
	f.deleted = append(f.deleted, rel)
	return nil
}

// OpenLibrarySkill 按 relPath 返回之前 Put 存入的字节；路径不存在则返回 os.ErrNotExist。
func (f *fakeLibraryBlob) OpenLibrarySkill(rel string) (io.ReadCloser, error) {
	if f.stored == nil {
		return nil, fmt.Errorf("blob not found: %s", rel)
	}
	data, ok := f.stored[rel]
	if !ok {
		return nil, fmt.Errorf("blob not found: %s", rel)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

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
	data := makeFlatSkillTar(t, "weather") // 合法扁平归档，frontmatter name=weather

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
	_, err := svc.Upload(context.Background(), psvcOrgMemberPrincipal(), PlatformSkillUploadInput{Name: "x", Version: "1.0", Data: []byte("a")})
	require.ErrorIs(t, err, ErrPlatformSkillDenied)
}

// name / version / data 任一为空 → Invalid。
func TestPlatformSkillService_Upload_Invalid(t *testing.T) {
	svc := NewPlatformSkillService(newFakePlatformSkillStore(), &fakeLibraryBlob{})
	_, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "", Version: "1", Data: []byte("a")})
	require.ErrorIs(t, err, ErrPlatformSkillInvalid)
}

// 版本号格式校验：合法的 x.x / x.x.x 通过，其余（缺段、多段、含前缀、非数字、含空格）一律 Invalid。
func TestPlatformSkillService_Upload_VersionFormat(t *testing.T) {
	// table-driven：覆盖两段/三段合法与各类非法输入组合。
	cases := []struct {
		name    string // 子测试场景名
		version string // 待校验版本号
		ok      bool   // 是否应通过格式校验
	}{
		{"two-segment", "1.0", true},        // 合法两段：x.x
		{"three-segment", "1.2.3", true},    // 合法三段：x.x.x
		{"multi-digit", "10.20.30", true},   // 合法：各段允许多位数字
		{"single-segment", "1", false},      // 非法：只有一段，不满足 x.x
		{"four-segment", "1.2.3.4", false},  // 非法：超过三段
		{"v-prefix", "v1.0", false},         // 非法：带 v 前缀
		{"non-numeric", "1.0-beta", false},  // 非法：含非数字后缀
		{"trailing-dot", "1.0.", false},     // 非法：结尾多余的点
		{"trimmed-space", " 1.0 ", true},    // 合法：service 先 TrimSpace，去空格后 1.0 满足格式
		{"letters", "abc", false},           // 非法：纯字母
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := NewPlatformSkillService(newFakePlatformSkillStore(), &fakeLibraryBlob{})
			// 用合法扁平归档，确保上传只可能因版本格式失败，隔离校验点。
			in := PlatformSkillUploadInput{Name: "weather", Version: c.version, Data: makeFlatSkillTar(t, "weather")}
			_, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), in)
			if c.ok {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrPlatformSkillInvalid)
			}
		})
	}
}

// 非 tar 字节（无法解析为归档）→ Invalid（后端扁平契约校验防线）。
func TestPlatformSkillService_Upload_RejectsNonTar(t *testing.T) {
	svc := NewPlatformSkillService(newFakePlatformSkillStore(), &fakeLibraryBlob{})
	_, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "x", Version: "1.0", Data: []byte("not a tar at all")})
	require.ErrorIs(t, err, ErrPlatformSkillInvalid)
}

// 嵌套布局（仅 <子目录>/SKILL.md，无根级 SKILL.md）→ Invalid：违反扁平契约，安装后对账永远 pending。
func TestPlatformSkillService_Upload_RejectsNestedLayout(t *testing.T) {
	// 构造 weather/SKILL.md 嵌套归档（根级无 SKILL.md），应被后端校验拦截。
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	md := "---\nname: weather\n---\n正文"
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "weather/SKILL.md", Mode: 0o644, Size: int64(len(md)), Typeflag: tar.TypeReg}))
	_, err := tw.Write([]byte(md))
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	svc := NewPlatformSkillService(newFakePlatformSkillStore(), &fakeLibraryBlob{})
	_, err = svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "weather", Version: "1.0", Data: buf.Bytes()})
	require.ErrorIs(t, err, ErrPlatformSkillInvalid)
}

// 归档缺少 SKILL.md → Invalid：扁平归档但没有技能主文件。
func TestPlatformSkillService_Upload_RejectsMissingSkillMD(t *testing.T) {
	// 构造仅含无关文件、无 SKILL.md 的扁平 tar。
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "readme.txt", Mode: 0o644, Size: 5, Typeflag: tar.TypeReg}))
	_, err := tw.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	svc := NewPlatformSkillService(newFakePlatformSkillStore(), &fakeLibraryBlob{})
	_, err = svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "x", Version: "1.0", Data: buf.Bytes()})
	require.ErrorIs(t, err, ErrPlatformSkillInvalid)
}

// 同名同版本已存在 → NameVersionTaken。
func TestPlatformSkillService_Upload_Duplicate(t *testing.T) {
	store := newFakePlatformSkillStore()
	svc := NewPlatformSkillService(store, &fakeLibraryBlob{})
	in := PlatformSkillUploadInput{Name: "weather", Version: "1.0", Data: makeFlatSkillTar(t, "weather")}
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
	res, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "w", Version: "1.0", Data: makeFlatSkillTar(t, "w")})
	require.NoError(t, err)

	require.NoError(t, svc.Delete(context.Background(), psvcPlatformPrincipal(), res.ID))
	_, ok := store.rows[res.ID]
	assert.False(t, ok)
	assert.Equal(t, []string{"library/platform/w/1.0.tar"}, blob.deleted)
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

	_, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "w", Version: "1.0", Data: makeFlatSkillTar(t, "w")})
	require.Error(t, err)
	assert.Equal(t, []string{"library/platform/w/1.0.tar"}, blob.deleted) // 回滚删除了刚写入的归档
}

// List 成功：平台管理员获取全部平台库 skill。
func TestPlatformSkillService_List_OK(t *testing.T) {
	store := newFakePlatformSkillStore()
	svc := NewPlatformSkillService(store, &fakeLibraryBlob{})
	_, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "a", Version: "1.0", Data: makeFlatSkillTar(t, "a")})
	require.NoError(t, err)
	_, err = svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "b", Version: "1.0", Data: makeFlatSkillTar(t, "b")})
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

// GetForInstall_OK 验证上传后可通过 name+version 取回完整归档字节与 sha256。
func TestPlatformSkillService_GetForInstall_OK(t *testing.T) {
	store := newFakePlatformSkillStore()
	blob := &fakeLibraryBlob{}
	svc := NewPlatformSkillService(store, blob)
	data := makeFlatSkillTar(t, "translate") // 合法扁平归档，供安装回取做字节比对

	// 先上传平台库 skill
	res, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{
		Name: "translate", Version: "2.0", Description: "翻译 skill", Data: data,
	})
	require.NoError(t, err)

	// 通过 GetForInstall 取回归档字节与 sha256
	archive, sha, err := svc.GetForInstall(context.Background(), "translate", "2.0")
	require.NoError(t, err)
	// 归档字节与原始上传内容一致
	assert.Equal(t, data, archive)
	// sha256 与 Upload 返回的摘要一致
	assert.Equal(t, res.FileSha256, sha)
}

// GetForInstall_NotFound 验证不存在的 name/version 返回 ErrPlatformSkillNotFound。
func TestPlatformSkillService_GetForInstall_NotFound(t *testing.T) {
	svc := NewPlatformSkillService(newFakePlatformSkillStore(), &fakeLibraryBlob{})
	// 查询从未上传的 skill，应返回 ErrPlatformSkillNotFound
	_, _, err := svc.GetForInstall(context.Background(), "nonexistent", "99.9")
	require.ErrorIs(t, err, ErrPlatformSkillNotFound)
}
