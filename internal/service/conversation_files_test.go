package service

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
)

// fakeConvFileStore 记录插入参数并按 id 返回固定记录。
type fakeConvFileStore struct {
	created ConvFileRecord
	getByID map[string]ConvFileRecord
}

func (f *fakeConvFileStore) CreateConversationFile(ctx context.Context, r ConvFileRecord) error {
	f.created = r
	return nil
}
func (f *fakeConvFileStore) GetConversationFile(ctx context.Context, id string) (ConvFileRecord, error) {
	r, ok := f.getByID[id]
	if !ok {
		return ConvFileRecord{}, ErrConversationFileNotFound
	}
	return r, nil
}

// fakeBlob 记录 PutObject 并对任意 key 返回固定预签名 URL。
type fakeBlob struct{ putKey, putData string }

func (b *fakeBlob) PutObject(ctx context.Context, key string, r io.Reader, size int64) error {
	b.putKey = key
	data, _ := io.ReadAll(r)
	b.putData = string(data)
	return nil
}
func (b *fakeBlob) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return "https://s3.example/" + key, nil
}

// fakeConvResolver 返回固定 app 定位（owner/org），供权限判断。
type fakeConvResolver struct{}

func (fakeConvResolver) Resolve(ctx context.Context, appID string) (OcOpsAppLocation, error) {
	return OcOpsAppLocation{OrgID: "org1", OwnerUserID: "owner1"}, nil
}

func convFilePlatformAdmin() auth.Principal { return auth.Principal{Role: domain.UserRolePlatformAdmin} }

// TestUploadConversationFile 上传：校验类型/大小后 PutObject 并落库，返回 file_id 与元数据。
func TestUploadConversationFile(t *testing.T) {
	store := &fakeConvFileStore{}
	blob := &fakeBlob{}
	svc := NewConversationFileService(store, blob, fakeConvResolver{})

	res, err := svc.Upload(context.Background(), convFilePlatformAdmin(), "app1", "weixin:u1",
		"报告.pdf", strings.NewReader("PDFDATA"), int64(len("PDFDATA")))
	require.NoError(t, err)
	assert.NotEmpty(t, res.FileID)
	assert.Equal(t, "报告.pdf", res.Filename)
	assert.Equal(t, "application/pdf", res.Mime)
	assert.Equal(t, "PDFDATA", blob.putData)
	assert.Equal(t, store.created.S3Key, blob.putKey)
	assert.Contains(t, blob.putKey, "apps/app1/conversations/weixin:u1/")
}

// TestUploadConversationFileRejectsType 不支持的扩展名直接拒绝，不落库。
func TestUploadConversationFileRejectsType(t *testing.T) {
	svc := NewConversationFileService(&fakeConvFileStore{}, &fakeBlob{}, fakeConvResolver{})
	_, err := svc.Upload(context.Background(), convFilePlatformAdmin(), "app1", "s1",
		"evil.exe", strings.NewReader("x"), 1)
	require.ErrorIs(t, err, ErrConversationFileUnsupported)
}

// TestResolveFileURL 解析 file_id → 预签名 URL，并校验文件归属该 app+session。
func TestResolveFileURL(t *testing.T) {
	store := &fakeConvFileStore{getByID: map[string]ConvFileRecord{
		"f1": {ID: "f1", AppID: "app1", SessionID: "s1", S3Key: "apps/app1/conversations/s1/f1/a.pdf", Filename: "a.pdf", Mime: "application/pdf"},
	}}
	svc := NewConversationFileService(store, &fakeBlob{}, fakeConvResolver{})
	url, filename, mime, err := svc.ResolveFileURL(context.Background(), "app1", "s1", "f1")
	require.NoError(t, err)
	assert.Equal(t, "https://s3.example/apps/app1/conversations/s1/f1/a.pdf", url)
	assert.Equal(t, "a.pdf", filename)
	assert.Equal(t, "application/pdf", mime)
}

// TestResolveFileURLWrongOwnerRejected 文件不属于该 app/session 时拒绝（防越权引用他人文件）。
func TestResolveFileURLWrongOwnerRejected(t *testing.T) {
	store := &fakeConvFileStore{getByID: map[string]ConvFileRecord{
		"f1": {ID: "f1", AppID: "appX", SessionID: "sX", S3Key: "k", Filename: "a.pdf"},
	}}
	svc := NewConversationFileService(store, &fakeBlob{}, fakeConvResolver{})
	_, _, _, err := svc.ResolveFileURL(context.Background(), "app1", "s1", "f1")
	require.ErrorIs(t, err, ErrConversationFileNotFound)
}
