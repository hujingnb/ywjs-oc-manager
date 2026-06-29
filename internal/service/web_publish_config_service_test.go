package service

import (
	"context"
	"encoding/json"
	"testing"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWPStore 实现 WebPublishConfigStore 接口，供单元测试使用。
// 记录每次调用的参数，不做实际数据库操作。
type fakeWPStore struct {
	upserted   *sqlc.UpsertWebPublishConfigParams
	enabled    *sqlc.SetWebPublishEnabledParams
	createdJob *sqlc.CreateJobParams
}

// UpsertWebPublishConfig 记录 upsert 参数，返回 nil 模拟成功。
func (f *fakeWPStore) UpsertWebPublishConfig(_ context.Context, p sqlc.UpsertWebPublishConfigParams) error {
	f.upserted = &p
	return nil
}

// SetWebPublishEnabled 记录 enabled 参数，返回 nil 模拟成功。
func (f *fakeWPStore) SetWebPublishEnabled(_ context.Context, p sqlc.SetWebPublishEnabledParams) error {
	f.enabled = &p
	return nil
}

// GetWebPublishConfig 返回空配置，模拟未配置状态。
func (f *fakeWPStore) GetWebPublishConfig(_ context.Context, _ string) (sqlc.OrgWebPublishConfig, error) {
	return sqlc.OrgWebPublishConfig{}, nil
}

// CreateJob 记录任务参数，返回 nil 模拟成功。
func (f *fakeWPStore) CreateJob(_ context.Context, p sqlc.CreateJobParams) error {
	f.createdJob = &p
	return nil
}

// fakeWPNotifier 实现 JobNotifier 接口，记录所有 Enqueue 的 jobID。
// 与 runtime_operation_service_test.go 中的 fakeNotifier 功能相似但独立声明，
// 需要记录入队次数（enqueued 切片）而非单次 lastJobID，因此不复用现有 fakeNotifier。
type fakeWPNotifier struct{ enqueued []string }

// Enqueue 记录入队的 jobID，返回 nil 模拟成功。
func (f *fakeWPNotifier) Enqueue(_ context.Context, id string) error {
	f.enqueued = append(f.enqueued, id)
	return nil
}

// TestConfigureEncryptsCredentials 覆盖：配置时凭证被加密落库、明文不出现在 upsert 参数里；
// 且加密结果可被同一 cipher 解密回原始凭证 map。
func TestConfigureEncryptsCredentials(t *testing.T) {
	// 使用全零 32 字节 key 构造 cipher，仅用于测试。
	cipher, err := auth.NewCipher(make([]byte, 32))
	require.NoError(t, err)

	st := &fakeWPStore{}
	svc := NewWebPublishConfigService(st, &fakeWPNotifier{}, cipher)
	admin := auth.Principal{Role: domain.UserRolePlatformAdmin}

	// 调用 Configure 并传入含明文密钥的凭证 map。
	err = svc.Configure(context.Background(), admin, WebPublishConfigInput{
		OrgID:       "org-1",
		BaseDomain:  "apps.example.com",
		DNSProvider: "alidns",
		Credentials: map[string]string{"access_key_id": "AK", "access_key_secret": "SK"},
		SiteTTLDays: 7,
		MaxSites:    20,
	})
	require.NoError(t, err)

	// upsert 参数必须已被记录，且凭证字段有效（非 null）。
	require.NotNil(t, st.upserted)
	require.True(t, st.upserted.DnsCredentialsCiphertext.Valid, "凭证密文字段应有效")

	// 明文不应直接出现在密文字符串中。
	assert.NotContains(t, st.upserted.DnsCredentialsCiphertext.String, "SK",
		"密文中不应包含明文 access_key_secret")

	// 解密后的内容应与原始凭证一致。
	raw, derr := cipher.Decrypt(st.upserted.DnsCredentialsCiphertext.String)
	require.NoError(t, derr, "密文应可被同一 cipher 解密")

	var creds map[string]string
	require.NoError(t, json.Unmarshal(raw, &creds), "解密后内容应为合法 JSON")
	assert.Equal(t, "AK", creds["access_key_id"])
	assert.Equal(t, "SK", creds["access_key_secret"])
}

// TestEnableEnqueuesProvisioning 覆盖：开通操作置 enabled=true + provisioning 状态，
// 并入队类型为 web_publish_provision 的 job，且 notifier.Enqueue 被调用一次。
func TestEnableEnqueuesProvisioning(t *testing.T) {
	cipher, _ := auth.NewCipher(make([]byte, 32))
	st := &fakeWPStore{}
	nt := &fakeWPNotifier{}
	svc := NewWebPublishConfigService(st, nt, cipher)
	admin := auth.Principal{Role: domain.UserRolePlatformAdmin}

	// 调用 Enable，断言开通流程正常完成。
	require.NoError(t, svc.Enable(context.Background(), admin, "org-1"))

	// SetWebPublishEnabled 应以 enabled=true + provisioning 状态调用。
	require.NotNil(t, st.enabled, "应调用 SetWebPublishEnabled")
	assert.Equal(t, true, st.enabled.Enabled)
	assert.Equal(t, domain.ProvisioningInProgress, st.enabled.ProvisioningStatus,
		"开通状态应为 provisioning")
	assert.Equal(t, "org-1", st.enabled.OrgID)

	// 应创建类型为 web_publish_provision 的 job。
	require.NotNil(t, st.createdJob, "应创建 provisioning job")
	assert.Equal(t, domain.JobTypeWebPublishProvision, st.createdJob.Type)
	assert.NotEmpty(t, st.createdJob.ID, "job ID 应非空")

	// 校验 payload：worker 据 org_id 知道给哪个企业开通，必须正确携带。
	var payload map[string]string
	require.NoError(t, json.Unmarshal(st.createdJob.PayloadJson, &payload))
	assert.Equal(t, "org-1", payload["org_id"])

	// notifier 应收到一次入队通知。
	assert.Len(t, nt.enqueued, 1, "应向 notifier 入队一次")
}

// TestConfigureDeniedForNonPlatformAdmin 覆盖：非平台管理员（如 org_member）调用
// Configure 时应返回权限错误，且 store 不被调用。
func TestConfigureDeniedForNonPlatformAdmin(t *testing.T) {
	cipher, _ := auth.NewCipher(make([]byte, 32))
	st := &fakeWPStore{}
	svc := NewWebPublishConfigService(st, &fakeWPNotifier{}, cipher)

	// 以普通成员身份调用，期望返回错误。
	member := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "org-1"}
	err := svc.Configure(context.Background(), member, WebPublishConfigInput{
		OrgID:       "org-1",
		DNSProvider: "alidns",
	})
	require.ErrorIs(t, err, ErrForbidden, "非平台管理员不得配置 web-publish")

	// 确认 store 未被调用（权限拒绝应在数据库操作前返回）。
	assert.Nil(t, st.upserted, "权限拒绝后 store 不应被调用")
}
