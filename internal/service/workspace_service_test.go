package service

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/storage"
	"oc-manager/internal/store/sqlc"
)

const (
	testWorkAppID = "00000000-0000-0000-0000-000000000f01"
	testWorkOrg   = "00000000-0000-0000-0000-000000000f02"
	testWorkOwner = "00000000-0000-0000-0000-000000000f03"
	// testWorkNode 仅保留常量以维持字段语义一致性，S3 版本不再检查 RuntimeNodeID
	testWorkNode = "00000000-0000-0000-0000-000000000f04"
)

// fakeWorkspaceObjectStore 实现 storage.ObjectStore 接口，用于 workspace 服务单元测试。
// 与 s3_skill_blob_store_test.go 中的 fakeObjectStore 同包但不同名，避免重名冲突。
type fakeWorkspaceObjectStore struct {
	// data 模拟 S3 bucket，key 为完整对象 key，value 为内容
	data         map[string][]byte
	presignError error // 模拟预签名失败
	listError    error // 模拟 ListObjects 失败
	lastPrefix   string // 最近一次 ListObjects 的 prefix
	lastPresign  string // 最近一次 PresignGet 的 key
}

func newFakeWorkspaceObjectStore() *fakeWorkspaceObjectStore {
	return &fakeWorkspaceObjectStore{data: make(map[string][]byte)}
}

// addObject 向 fake store 放入一个对象，content 为对象内容。
func (f *fakeWorkspaceObjectStore) addObject(key string, content []byte) {
	f.data[key] = content
}

func (f *fakeWorkspaceObjectStore) PutObject(_ context.Context, key string, r io.Reader, _ int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.data[key] = data
	return nil
}

func (f *fakeWorkspaceObjectStore) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	f.lastPresign = key
	if f.presignError != nil {
		return "", f.presignError
	}
	// 返回约定 fake URL，格式 "fake://key"；Download 测试中关注 key 是否正确
	return "fake://" + key, nil
}

func (f *fakeWorkspaceObjectStore) ObjectExists(_ context.Context, key string) (bool, error) {
	_, ok := f.data[key]
	return ok, nil
}

// ListObjects 遍历 data map，返回 prefix 下相对 key 与大小。
func (f *fakeWorkspaceObjectStore) ListObjects(_ context.Context, prefix string) ([]storage.ObjectInfo, error) {
	f.lastPrefix = prefix
	if f.listError != nil {
		return nil, f.listError
	}
	var items []storage.ObjectInfo
	for k, v := range f.data {
		if strings.HasPrefix(k, prefix) {
			relKey := k[len(prefix):]
			items = append(items, storage.ObjectInfo{Key: relKey, Size: int64(len(v))})
		}
	}
	return items, nil
}

func (f *fakeWorkspaceObjectStore) MovePrefix(_ context.Context, _, _ string) error { return nil }
func (f *fakeWorkspaceObjectStore) DeletePrefix(_ context.Context, _ string) error   { return nil }

// newWorkspaceStub 构造带有合法 App 记录的存储桩。
// S3 版本保留 RuntimeNodeID 字段（其 Valid 不影响 S3 分支的工作，仅供鉴权路径判断）。
func newWorkspaceStub(t *testing.T) *workspaceStub {
	app := sqlc.App{
		ID:            mustUUID(t, testWorkAppID),
		OrgID:         mustUUID(t, testWorkOrg),
		OwnerUserID:   mustUUID(t, testWorkOwner),
		RuntimeNodeID: null.StringFrom(mustUUID(t, testWorkNode)), // 保留以确保字段初始化完整
		Status:        domain.AppStatusRunning,
		ApiKeyStatus:  domain.APIKeyStatusActive,
	}
	return &workspaceStub{t: t, app: app}
}

type workspaceStub struct {
	t   *testing.T
	app sqlc.App
}

func (s *workspaceStub) GetApp(_ context.Context, id string) (sqlc.App, error) {
	if id != s.app.ID {
		return sqlc.App{}, sql.ErrNoRows
	}
	return s.app, nil
}

// TestWorkspaceServiceListReturnsEntries 验证正常路径：ListObjects 有结果时 List 正确映射为条目列表。
func TestWorkspaceServiceListReturnsEntries(t *testing.T) {
	store := newWorkspaceStub(t)
	obj := newFakeWorkspaceObjectStore()
	// 在 workspace/logs/ 下放一个文件
	prefix := "apps/" + testWorkAppID + "/workspace/logs/"
	obj.addObject(prefix+"alice.log", []byte("hello"))

	svc := NewWorkspaceService(store, obj, time.Minute)

	// 场景：ListObjects 返回一个文件对象时，List 应正确映射为 WorkspaceEntryResult。
	listing, err := svc.List(context.Background(), platformAdmin(), testWorkAppID, "logs")
	require.NoError(t, err)
	require.Len(t, listing.Entries, 1)
	assert.Equal(t, "alice.log", listing.Entries[0].Name)
	assert.False(t, listing.Entries[0].IsDir)
	assert.Equal(t, int64(5), listing.Entries[0].Size)
	// 确认 ListObjects 被调用时使用了正确的前缀
	assert.Equal(t, prefix, obj.lastPrefix)
}

// TestWorkspaceServiceListRootReturnsAllTopLevel 验证根目录列举：返回直接子条目，目录与文件都正确识别。
func TestWorkspaceServiceListRootReturnsAllTopLevel(t *testing.T) {
	store := newWorkspaceStub(t)
	obj := newFakeWorkspaceObjectStore()
	wsPrefix := "apps/" + testWorkAppID + "/workspace/"
	// 根目录下有一个文件和一个子目录（两个对象）
	obj.addObject(wsPrefix+"readme.txt", []byte("doc"))
	obj.addObject(wsPrefix+"logs/app.log", []byte("log"))

	svc := NewWorkspaceService(store, obj, time.Minute)

	// 场景：列举根目录时应看到 readme.txt（文件）和 logs（目录）。
	listing, err := svc.List(context.Background(), platformAdmin(), testWorkAppID, "")
	require.NoError(t, err)
	require.Len(t, listing.Entries, 2)

	// 整理为 name->entry 便于断言顺序无关
	byName := make(map[string]WorkspaceEntryResult)
	for _, e := range listing.Entries {
		byName[e.Name] = e
	}
	assert.False(t, byName["readme.txt"].IsDir)
	assert.Equal(t, int64(3), byName["readme.txt"].Size)
	assert.True(t, byName["logs"].IsDir)
	assert.Equal(t, int64(0), byName["logs"].Size) // 目录 size 归零
}

// TestWorkspaceServiceListDeduplicatesDirectories 验证目录去重：同一子目录下有多个对象时只出现一次。
func TestWorkspaceServiceListDeduplicatesDirectories(t *testing.T) {
	store := newWorkspaceStub(t)
	obj := newFakeWorkspaceObjectStore()
	wsPrefix := "apps/" + testWorkAppID + "/workspace/"
	// 同一 logs/ 目录下有多个文件
	obj.addObject(wsPrefix+"logs/a.log", []byte("a"))
	obj.addObject(wsPrefix+"logs/b.log", []byte("b"))
	obj.addObject(wsPrefix+"logs/sub/c.log", []byte("c"))

	svc := NewWorkspaceService(store, obj, time.Minute)

	// 场景：多个对象属于同一子目录前缀时，目录条目应去重，仅返回一次。
	listing, err := svc.List(context.Background(), platformAdmin(), testWorkAppID, "")
	require.NoError(t, err)
	require.Len(t, listing.Entries, 1)
	assert.Equal(t, "logs", listing.Entries[0].Name)
	assert.True(t, listing.Entries[0].IsDir)
}

// TestWorkspaceServiceListAllowsPlatformAdminRead 验证平台管理员可以读取任意应用的工作目录。
func TestWorkspaceServiceListAllowsPlatformAdminRead(t *testing.T) {
	store := newWorkspaceStub(t)
	obj := newFakeWorkspaceObjectStore()
	wsPrefix := "apps/" + testWorkAppID + "/workspace/"
	obj.addObject(wsPrefix+"session.log", []byte("session"))

	svc := NewWorkspaceService(store, obj, time.Minute)

	// 场景：平台管理员读取根目录时可看到文件条目。
	listing, err := svc.List(context.Background(), platformAdmin(), testWorkAppID, "")
	require.NoError(t, err)
	require.Len(t, listing.Entries, 1)
	assert.Equal(t, "session.log", listing.Entries[0].Name)
}

// TestWorkspaceServiceListRejectsForbidden 验证非应用成员无权访问工作目录，返回 ErrWorkspaceForbidden。
func TestWorkspaceServiceListRejectsForbidden(t *testing.T) {
	store := newWorkspaceStub(t)
	svc := NewWorkspaceService(store, newFakeWorkspaceObjectStore(), time.Minute)

	// 场景：OrgMember 角色但不是该应用的 owner，应被拒绝。
	_, err := svc.List(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testWorkOrg, UserID: "stranger"}, testWorkAppID, "logs")
	require.ErrorIs(t, err, ErrWorkspaceForbidden)
}

// TestWorkspaceServiceListMissingObjectsReturnsEmpty 验证 S3 无对象时 List 返回空条目（非错误）。
func TestWorkspaceServiceListMissingObjectsReturnsEmpty(t *testing.T) {
	store := newWorkspaceStub(t)
	svc := NewWorkspaceService(store, newFakeWorkspaceObjectStore(), time.Minute)

	// 场景：S3 中该 app workspace 下无任何对象，List 应返回空列表而非错误。
	listing, err := svc.List(context.Background(), platformAdmin(), testWorkAppID, "")
	require.NoError(t, err)
	assert.Empty(t, listing.Entries)
}

// TestWorkspaceServiceListObjectStoreNilReturnsMissing 验证 objects 为 nil 时返回 ErrWorkspaceMissing。
func TestWorkspaceServiceListObjectStoreNilReturnsMissing(t *testing.T) {
	store := newWorkspaceStub(t)
	svc := NewWorkspaceService(store, nil, time.Minute)

	// 场景：S3 object store 未配置（nil）时应返回 ErrWorkspaceMissing。
	_, err := svc.List(context.Background(), platformAdmin(), testWorkAppID, "")
	require.ErrorIs(t, err, ErrWorkspaceMissing)
}

// TestWorkspaceServiceArchiveFailsWithoutObjectStore 验证 objects 为 nil 时 Archive 返回 ErrWorkspaceMissing。
func TestWorkspaceServiceArchiveFailsWithoutObjectStore(t *testing.T) {
	store := newWorkspaceStub(t)
	svc := NewWorkspaceService(store, nil, time.Minute)

	var buf strings.Builder
	// 场景：S3 object store 未配置时 Archive 应返回 ErrWorkspaceMissing，与旧版 adapter nil 行为一致。
	err := svc.Archive(context.Background(), platformAdmin(), testWorkAppID, "", &buf)
	require.ErrorIs(t, err, ErrWorkspaceMissing)
}

// TestWorkspaceServiceDownloadFailsWithoutObjectStore 验证 objects 为 nil 时 Download 返回 ErrWorkspaceMissing。
func TestWorkspaceServiceDownloadFailsWithoutObjectStore(t *testing.T) {
	store := newWorkspaceStub(t)
	svc := NewWorkspaceService(store, nil, time.Minute)

	// 场景：S3 object store 未配置时 Download 应返回 ErrWorkspaceMissing。
	_, err := svc.Download(context.Background(), platformAdmin(), testWorkAppID, "logs/x.log")
	require.ErrorIs(t, err, ErrWorkspaceMissing)
}

// TestWorkspaceServiceRejectsUnsafePaths 验证路径安全校验：越界路径必须被拒绝。
func TestWorkspaceServiceRejectsUnsafePaths(t *testing.T) {
	store := newWorkspaceStub(t)
	svc := NewWorkspaceService(store, newFakeWorkspaceObjectStore(), time.Minute)

	for _, target := range []string{"..", "../secret.txt", "/abs.txt", ""} {
		// 场景：Download 传入越界或绝对路径时应返回 ErrWorkspaceBadPath。
		if _, err := svc.Download(context.Background(), platformAdmin(), testWorkAppID, target); !errors.Is(err, ErrWorkspaceBadPath) {
			t.Fatalf("Download(%q) error = %v, want ErrWorkspaceBadPath", target, err)
		}
	}

	// 场景：List 传入绝对路径时应返回 ErrWorkspaceBadPath。
	if _, err := svc.List(context.Background(), platformAdmin(), testWorkAppID, "/abs"); !errors.Is(err, ErrWorkspaceBadPath) {
		t.Fatalf("List absolute error = %v, want ErrWorkspaceBadPath", err)
	}
}

// TestWorkspaceServiceDownloadPresignsCorrectKey 验证 Download 向 S3 请求预签名时使用正确的对象 key。
func TestWorkspaceServiceDownloadPresignsCorrectKey(t *testing.T) {
	store := newWorkspaceStub(t)
	obj := newFakeWorkspaceObjectStore()
	svc := NewWorkspaceService(store, obj, time.Minute)

	// 场景：Download 时 PresignGet 应被调用，key 为 apps/<id>/workspace/<relPath>。
	// 因为 http.Get 无法真正请求 fake:// URL，只验证 presign key 是否正确。
	// 实际会 http.Get 失败，但 presign key 可从 obj.lastPresign 取到。
	_, _ = svc.Download(context.Background(), platformAdmin(), testWorkAppID, "logs/x.log")
	expectedKey := "apps/" + testWorkAppID + "/workspace/logs/x.log"
	assert.Equal(t, expectedKey, obj.lastPresign)
}
