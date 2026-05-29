package service

import (
	"context"
	"testing"
	"time"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/storage"
	"oc-manager/internal/store/sqlc"
)

// fakeBootstrapStore 实现 bootstrapStore，返回预置的 app/org/owner/version。
type fakeBootstrapStore struct {
	app     sqlc.App
	org     sqlc.Organization
	owner   sqlc.User
	version sqlc.AssistantVersion
}

// GetApp 按 ID 返回预置 app。
func (f *fakeBootstrapStore) GetApp(_ context.Context, _ string) (sqlc.App, error) {
	return f.app, nil
}

// GetAppByRuntimeTokenHash 按 token hash 返回预置 app；参数类型与接口保持一致（null.String）。
func (f *fakeBootstrapStore) GetAppByRuntimeTokenHash(_ context.Context, _ null.String) (sqlc.App, error) {
	return f.app, nil
}

// GetOrganization 返回预置组织。
func (f *fakeBootstrapStore) GetOrganization(_ context.Context, _ string) (sqlc.Organization, error) {
	return f.org, nil
}

// GetUser 返回预置用户。
func (f *fakeBootstrapStore) GetUser(_ context.Context, _ string) (sqlc.User, error) {
	return f.owner, nil
}

// GetAssistantVersion 返回预置版本。
func (f *fakeBootstrapStore) GetAssistantVersion(_ context.Context, _ string) (sqlc.AssistantVersion, error) {
	return f.version, nil
}

// fakeSTS 实现 storage.STSIssuer，返回固定临时凭证，并记录被限定的 prefix 以便断言。
type fakeSTS struct {
	// gotPrefix 记录最近一次 AssumeAppRole 传入的 prefix，用于验证前缀限定正确。
	gotPrefix string
}

// AssumeAppRole 记录 prefix 并返回固定 STS 临时凭证。
func (f *fakeSTS) AssumeAppRole(_ context.Context, prefix string, _ time.Duration) (storage.TempCredentials, error) {
	f.gotPrefix = prefix
	return storage.TempCredentials{
		AccessKeyID:     "AK",
		SecretAccessKey: "SK",
		SessionToken:    "ST",
		ExpiresAt:       time.Unix(1000, 0),
	}, nil
}

// fakeSkills 实现 bootstrapSkillSource，为任意 relPath 返回固定格式的预签名 URL。
type fakeSkills struct{}

// PresignSkill 构造形如 https://presigned/<relPath> 的 URL，供断言 skill URL 格式。
func (fakeSkills) PresignSkill(_ context.Context, relPath string, _ time.Duration) (string, error) {
	return "https://presigned/" + relPath, nil
}

// newBootstrapApp 构造一个带 NewapiKeyCiphertext、RuntimeTokenCiphertext 与 VersionID 的完整 app。
// 密文由传入 cipher 生成，确保 Build 能正常解密。
func newBootstrapApp(t *testing.T, cipher *auth.Cipher) sqlc.App {
	t.Helper()
	// 加密 new-api api_key 明文 "sk-test"
	keyCt, err := cipher.Encrypt([]byte("sk-test"))
	require.NoError(t, err)
	// 加密 control token 明文 "control-tok"
	tokCt, err := cipher.Encrypt([]byte("control-tok"))
	require.NoError(t, err)
	return sqlc.App{
		ID:                     "a1",
		OrgID:                  "o1",
		OwnerUserID:            "u1",
		Name:                   "demo",
		NewapiKeyCiphertext:    null.StringFrom(keyCt),
		RuntimeTokenCiphertext: null.StringFrom(tokCt),
		RuntimeTokenHash:       null.StringFrom(HashAppRuntimeToken("control-tok")),
		VersionID:              null.StringFrom("v1"),
	}
}

// TestBootstrapBuildHappyPath 验证正常组装路径：
//   - manifest YAML 含解密后的 api_key 明文（"sk-test"）
//   - STS 调用时 prefix 限定到 "apps/a1/"
//   - 首启场景（fakeObjectStore 无任何对象）restore 字段全部为空
func TestBootstrapBuildHappyPath(t *testing.T) {
	// 复用 app_runtime_token_test.go 的 helper，构造全零密钥 cipher
	cipher := newRuntimeTokenTestCipher(t)
	app := newBootstrapApp(t, cipher)
	store := &fakeBootstrapStore{
		app: app,
		org: sqlc.Organization{ID: "o1", Name: "Org"},
		// owner DisplayName 留空，不影响 manifest 组装
		owner:   sqlc.User{ID: "u1", DisplayName: "Owner"},
		version: sqlc.AssistantVersion{ID: "v1", MainModel: "gpt-x", SystemPrompt: "you are bot"},
	}
	sts := &fakeSTS{}
	// 复用 s3_skill_blob_store_test.go 的 fakeObjectStore，默认无对象（ObjectExists 全返回 false）
	svc := NewBootstrapService(store, cipher, newFakeObjectStore(), sts, fakeSkills{}, BootstrapConfig{
		Endpoint:         "http://minio:9000",
		Region:           "us-east-1",
		Bucket:           "oc-apps",
		NewAPIBaseURL:    "http://new-api:3000",
		KnowledgeBaseURL: "http://manager/runtime",
		PlatformPrompt:   "platform rule",
		PresignTTL:       time.Minute,
	})

	res, err := svc.Build(context.Background(), app)
	require.NoError(t, err)

	// manifest YAML 必须包含解密后的 api_key 明文，证明密文解密正确并写入渲染结果
	assert.Contains(t, res.ManifestYAML, "sk-test")
	// manifest 还须包含解密后的 control token（写入 knowledge.app_token），验证 token 统一链路透传
	assert.Contains(t, res.ManifestYAML, "control-tok")
	// STS 限定前缀必须为 apps/<appID>/，sidecar 不得越界写入
	assert.Equal(t, "apps/a1/", sts.gotPrefix)
	assert.Equal(t, "apps/a1/", res.S3Write.Prefix)
	// STS 凭证三字段必须完整透传到响应，缺任一都会让 sidecar 无法写 S3
	assert.Equal(t, "AK", res.S3Write.AccessKeyID)
	assert.Equal(t, "SK", res.S3Write.SecretAccessKey)
	assert.Equal(t, "ST", res.S3Write.SessionToken)
	// 首启：fakeObjectStore 无任何对象，三个 restore 字段全部为空
	assert.Empty(t, res.Restore.WorkspaceURL)
	assert.Empty(t, res.Restore.StateDBURL)
	assert.Empty(t, res.Restore.SessionsURL)
}

// TestBootstrapBuildAppNotReady 验证未就绪边界：
//   - app 缺少 NewapiKeyCiphertext 时，Build 必须返回 ErrAppNotReady
func TestBootstrapBuildAppNotReady(t *testing.T) {
	cipher := newRuntimeTokenTestCipher(t)
	// 构造一个没有任何密文字段的最简 app，模拟创建流程尚未完成的状态
	app := sqlc.App{ID: "a1"}
	svc := NewBootstrapService(
		&fakeBootstrapStore{app: app},
		cipher,
		newFakeObjectStore(),
		&fakeSTS{},
		fakeSkills{},
		BootstrapConfig{PresignTTL: time.Minute},
	)
	_, err := svc.Build(context.Background(), app)
	// 缺少 api_key 密文必须返回 ErrAppNotReady，而非其他错误
	require.ErrorIs(t, err, ErrAppNotReady)
}
