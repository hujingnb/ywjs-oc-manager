package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/acme"
	"oc-manager/internal/store/sqlc"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWPProvStore 模拟 WebPublishProvisionStore 接口，记录所有写入调用供断言。
type fakeWPProvStore struct {
	cfg         sqlc.OrgWebPublishConfig
	provUpdates []sqlc.SetWebPublishProvisioningParams
	certUpdates []sqlc.SetWebPublishCertStatusParams
}

func (f *fakeWPProvStore) GetWebPublishConfig(_ context.Context, _ string) (sqlc.OrgWebPublishConfig, error) {
	return f.cfg, nil
}

func (f *fakeWPProvStore) SetWebPublishProvisioning(_ context.Context, p sqlc.SetWebPublishProvisioningParams) error {
	f.provUpdates = append(f.provUpdates, p)
	return nil
}

func (f *fakeWPProvStore) SetWebPublishCertStatus(_ context.Context, p sqlc.SetWebPublishCertStatusParams) error {
	f.certUpdates = append(f.certUpdates, p)
	return nil
}

// fakeProvisioner 模拟 CertProvisioner 接口，记录传入参数供断言。
type fakeProvisioner struct {
	ret              acme.Certificate
	err              error
	gotBaseDomain    string
	gotIP            string
}

func (f *fakeProvisioner) Provision(_ context.Context, in CertProvisionInput) (acme.Certificate, error) {
	f.gotBaseDomain, f.gotIP = in.BaseDomain, in.IngressIP
	return f.ret, f.err
}

// fakeClusterApplier 模拟 ClusterApplier 接口，记录调用情况供断言。
type fakeClusterApplier struct {
	tlsApplied bool
	ingApplied bool
	tlsErr     error
	ingErr     error
}

func (f *fakeClusterApplier) ApplyTLSSecret(_ context.Context, _ string, _, _ []byte) error {
	f.tlsApplied = true
	return f.tlsErr
}

func (f *fakeClusterApplier) ApplyWildcardIngress(_ context.Context, _ WildcardIngressParams) error {
	f.ingApplied = true
	return f.ingErr
}

// newCfg 构造一个携带加密凭证的 OrgWebPublishConfig，用于测试。
func newCfg(cipher *auth.Cipher) sqlc.OrgWebPublishConfig {
	raw, _ := json.Marshal(map[string]string{"access_key_id": "AK", "access_key_secret": "SK"})
	enc, _ := cipher.Encrypt(raw)
	return sqlc.OrgWebPublishConfig{
		OrgID:                    "org-1",
		BaseDomain:               "apps.example.com",
		DnsProvider:              "alidns",
		DnsCredentialsCiphertext: null.StringFrom(enc),
		CertSecretName:           "wildcard-apps",
	}
}

// provJob 构造一个 web_publish_provision job，payload 携带 org_id。
func provJob() sqlc.Job {
	p, _ := json.Marshal(map[string]string{"org_id": "org-1"})
	return sqlc.Job{Type: domain.JobTypeWebPublishProvision, PayloadJson: p}
}

// TestProvisionHappyPath 覆盖：签证书成功 → 写 TLS Secret → 建通配 Ingress → provisioning=ready、cert=issued。
func TestProvisionHappyPath(t *testing.T) {
	// 准备测试依赖
	cipher, _ := auth.NewCipher(make([]byte, 32))
	st := &fakeWPProvStore{cfg: newCfg(cipher)}
	// 模拟签发成功，NotAfter 为 2030-01-01 00:00:00 UTC
	prov := &fakeProvisioner{ret: acme.Certificate{CertPEM: []byte("C"), KeyPEM: []byte("K"), NotAfter: 1893456000}}
	cl := &fakeClusterApplier{}
	h := NewWebPublishProvisionHandler(st, prov, cl, cipher, WebPublishProvisionConfig{IngressPublicIP: "1.2.3.4"})

	// 执行 handler，期望无错误
	require.NoError(t, h.Handle(context.Background(), provJob()))

	// 验证传给 Provisioner 的参数正确
	assert.Equal(t, "apps.example.com", prov.gotBaseDomain)
	assert.Equal(t, "1.2.3.4", prov.gotIP)

	// 验证集群副作用都被触发
	assert.True(t, cl.tlsApplied, "应写入 TLS Secret")
	assert.True(t, cl.ingApplied, "应建通配 Ingress")

	// 验证最终 provisioning 状态为 ready
	require.NotEmpty(t, st.provUpdates)
	assert.Equal(t, domain.ProvisioningReady, st.provUpdates[len(st.provUpdates)-1].ProvisioningStatus)

	// 验证最终 cert 状态为 issued
	require.NotEmpty(t, st.certUpdates)
	assert.Equal(t, domain.CertStatusIssued, st.certUpdates[len(st.certUpdates)-1].CertStatus)
}

// TestProvisionCertFails 覆盖：签证书失败 → 返回错误（worker 据此重试）、provisioning=failed、cert=failed，且不建 Ingress。
func TestProvisionCertFails(t *testing.T) {
	// 准备测试依赖
	cipher, _ := auth.NewCipher(make([]byte, 32))
	st := &fakeWPProvStore{cfg: newCfg(cipher)}
	// 模拟签发失败
	prov := &fakeProvisioner{err: assert.AnError}
	cl := &fakeClusterApplier{}
	h := NewWebPublishProvisionHandler(st, prov, cl, cipher, WebPublishProvisionConfig{IngressPublicIP: "1.2.3.4"})

	// 执行 handler，期望返回错误（供 worker backoff 重试）
	err := h.Handle(context.Background(), provJob())
	require.Error(t, err)

	// 验证签证书失败时不应建 Ingress
	assert.False(t, cl.ingApplied, "签证书失败不应建 Ingress")

	// 验证最终 provisioning 状态为 failed
	assert.Equal(t, domain.ProvisioningFailed, st.provUpdates[len(st.provUpdates)-1].ProvisioningStatus)

	// 验证最终 cert 状态为 failed
	assert.Equal(t, domain.CertStatusFailed, st.certUpdates[len(st.certUpdates)-1].CertStatus)
}
