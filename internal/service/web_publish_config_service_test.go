package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// fakeWPStore 实现 WebPublishConfigStore 接口，供单元测试使用。
// 记录每次调用的参数，不做实际数据库操作。
type fakeWPStore struct {
	// cfg 是 GetWebPublishConfig 预置的返回值；零值表示未配置状态。
	cfg        sqlc.OrgWebPublishConfig
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

// GetWebPublishConfig 返回预置配置（cfg 字段），模拟存储层查询。
func (f *fakeWPStore) GetWebPublishConfig(_ context.Context, _ string) (sqlc.OrgWebPublishConfig, error) {
	return f.cfg, nil
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

// TestGetReturnsCertStatusDesensitized 覆盖：Get 返回脱敏配置视图，
// 包含正确的通配域名、证书状态及时间戳字段映射，凭证密文不出现在结果中。
func TestGetReturnsCertStatusDesensitized(t *testing.T) {
	cipher, _ := auth.NewCipher(make([]byte, 32))

	// 构造预置配置，CertLastIssuedAt 有效，CertLastRenewedAt 无效（未续签）
	issuedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	notAfter := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	st := &fakeWPStore{
		cfg: sqlc.OrgWebPublishConfig{
			OrgID:                    "org-1",
			Enabled:                  true,
			BaseDomain:               "apps.example.com",
			DnsProvider:              "alidns",
			DnsCredentialsCiphertext: null.StringFrom("SHOULD_NOT_APPEAR"),
			SiteTtlDays:              7,
			MaxSites:                 20,
			ProvisioningStatus:       domain.ProvisioningReady,
			ProvisioningMessage:      null.String{},
			CertStatus:               domain.CertStatusIssued,
			CertNotAfter:             null.TimeFrom(notAfter),
			CertLastIssuedAt:         null.TimeFrom(issuedAt),
			CertLastRenewedAt:        null.Time{}, // 从未续签，应映射为 nil
			CertMessage:              null.String{},
		},
	}
	svc := NewWebPublishConfigService(st, &fakeWPNotifier{}, cipher)

	// 平台管理员可查看任意企业配置
	admin := auth.Principal{Role: domain.UserRolePlatformAdmin}
	result, err := svc.Get(context.Background(), admin, "org-1")
	require.NoError(t, err)

	// 验证通配域名格式正确（"*." + BaseDomain）
	assert.Equal(t, "*.apps.example.com", result.WildcardDomain, "通配域名格式应为 *.<base_domain>")

	// 验证基本字段映射正确
	assert.Equal(t, "org-1", result.OrgID)
	assert.Equal(t, true, result.Enabled)
	assert.Equal(t, "apps.example.com", result.BaseDomain)
	assert.Equal(t, domain.CertStatusIssued, result.CertStatus)
	assert.Equal(t, domain.ProvisioningReady, result.ProvisioningStatus)

	// 验证证书时间戳字段正确映射：Valid=true → 非 nil 指针，Valid=false → nil
	require.NotNil(t, result.CertNotAfter, "cert_not_after 应映射为非 nil 指针")
	assert.Equal(t, notAfter, *result.CertNotAfter)
	require.NotNil(t, result.CertLastIssuedAt, "cert_last_issued_at 应映射为非 nil 指针")
	assert.Equal(t, issuedAt, *result.CertLastIssuedAt)
	assert.Nil(t, result.CertLastRenewedAt, "未续签时 cert_last_renewed_at 应为 nil")

	// 验证凭证密文绝不出现在返回结果中（脱敏核心契约）
	assert.NotContains(t, result.DNSProvider+result.ProvisioningMessage+result.CertMessage,
		"SHOULD_NOT_APPEAR", "凭证密文不应出现在任何返回字段")
}

// TestRetryProvisionDeniedForNonAdmin 覆盖：非平台管理员调用 RetryProvision 应返回 ErrForbidden，
// 且 store 不应收到任何 CreateJob 调用。
func TestRetryProvisionDeniedForNonAdmin(t *testing.T) {
	cipher, _ := auth.NewCipher(make([]byte, 32))
	st := &fakeWPStore{}
	svc := NewWebPublishConfigService(st, &fakeWPNotifier{}, cipher)

	// 以企业管理员身份调用（非平台管理员），期望返回权限错误
	orgAdmin := auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1"}
	err := svc.RetryProvision(context.Background(), orgAdmin, "org-1")
	require.ErrorIs(t, err, ErrForbidden, "非平台管理员不得触发 RetryProvision")

	// 权限拒绝后 CreateJob 不应被调用
	assert.Nil(t, st.createdJob, "权限拒绝后不应创建 provisioning job")
}

// TestRetryProvisionEnqueues 覆盖：平台管理员调用 RetryProvision 应成功入队
// 一个 web_publish_provision job，payload 包含正确的 org_id。
func TestRetryProvisionEnqueues(t *testing.T) {
	cipher, _ := auth.NewCipher(make([]byte, 32))
	st := &fakeWPStore{}
	nt := &fakeWPNotifier{}
	svc := NewWebPublishConfigService(st, nt, cipher)

	admin := auth.Principal{Role: domain.UserRolePlatformAdmin}
	err := svc.RetryProvision(context.Background(), admin, "org-2")
	require.NoError(t, err, "平台管理员 RetryProvision 应成功")

	// 验证 CreateJob 被调用，类型和 payload 正确
	require.NotNil(t, st.createdJob, "应创建 provisioning job")
	assert.Equal(t, domain.JobTypeWebPublishProvision, st.createdJob.Type)
	var payload map[string]string
	require.NoError(t, json.Unmarshal(st.createdJob.PayloadJson, &payload))
	assert.Equal(t, "org-2", payload["org_id"], "payload 中的 org_id 应与请求一致")

	// notifier 应收到一次入队通知
	assert.Len(t, nt.enqueued, 1, "应向 notifier 入队一次")
}
