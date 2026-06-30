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

// fakeClusterApplier 模拟 ClusterApplier 接口，记录调用情况与收到的资源名供断言。
type fakeClusterApplier struct {
	tlsApplied bool
	ingApplied bool
	tlsErr     error
	ingErr     error
	gotTLSName string                // ApplyTLSSecret 收到的 Secret 名
	gotIngress WildcardIngressParams // ApplyWildcardIngress 收到的参数
}

func (f *fakeClusterApplier) ApplyTLSSecret(_ context.Context, name string, _, _ []byte) error {
	f.tlsApplied = true
	f.gotTLSName = name
	return f.tlsErr
}

func (f *fakeClusterApplier) ApplyWildcardIngress(_ context.Context, params WildcardIngressParams) error {
	f.ingApplied = true
	f.gotIngress = params
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
	lastCert := st.certUpdates[len(st.certUpdates)-1]
	assert.Equal(t, domain.CertStatusIssued, lastCert.CertStatus)
	// 验证签发成功时写了 cert_last_issued_at（Plan 5 续期追踪依赖此契约）
	assert.True(t, lastCert.CertLastIssuedAt.Valid, "签发成功应记录 cert_last_issued_at")
}

// TestProvisionDerivesCertSecretName 覆盖：cfg.CertSecretName 为空时按 base_domain 确定性派生资源名，
// 避免空名被 k8s 拒绝导致 provisioning 永久失败；并把派生值写回 DB。
func TestProvisionDerivesCertSecretName(t *testing.T) {
	// 准备测试依赖：CertSecretName 留空，base_domain=apps.example.com
	cipher, _ := auth.NewCipher(make([]byte, 32))
	cfg := newCfg(cipher)
	cfg.CertSecretName = "" // 模拟首次 provisioning：列默认空串
	st := &fakeWPProvStore{cfg: cfg}
	prov := &fakeProvisioner{ret: acme.Certificate{CertPEM: []byte("C"), KeyPEM: []byte("K"), NotAfter: 1893456000}}
	cl := &fakeClusterApplier{}
	h := NewWebPublishProvisionHandler(st, prov, cl, cipher, WebPublishProvisionConfig{IngressPublicIP: "1.2.3.4"})

	// 执行 handler，期望无错误
	require.NoError(t, h.Handle(context.Background(), provJob()))

	// 期望资源名按 base_domain 点替连字符派生
	const want = "wildcard-apps-example-com"

	// 验证传给 cluster applier 的 TLS Secret 名与 Ingress 名/引用名都是派生值
	assert.Equal(t, want, cl.gotTLSName, "TLS Secret 名应按 base_domain 派生")
	assert.Equal(t, want, cl.gotIngress.Name, "Ingress 名应按 base_domain 派生")
	assert.Equal(t, want, cl.gotIngress.TLSSecretName, "Ingress 引用的 TLS Secret 名应一致")

	// 验证派生值写回 DB（最终 SetWebPublishProvisioning 的 CertSecretName）
	require.NotEmpty(t, st.provUpdates)
	assert.Equal(t, want, st.provUpdates[len(st.provUpdates)-1].CertSecretName, "派生的 secret 名应写回 DB")
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

	// 验证签证书失败时连 TLS Secret 都不该写
	assert.False(t, cl.tlsApplied, "签证书失败不应写 TLS Secret")
	// 验证签证书失败时不应建 Ingress
	assert.False(t, cl.ingApplied, "签证书失败不应建 Ingress")

	// 验证最终 provisioning 状态为 failed
	assert.Equal(t, domain.ProvisioningFailed, st.provUpdates[len(st.provUpdates)-1].ProvisioningStatus)

	// 验证最终 cert 状态为 failed
	assert.Equal(t, domain.CertStatusFailed, st.certUpdates[len(st.certUpdates)-1].CertStatus)
}

// TestProvisionTLSSecretFails 覆盖：签证书成功但写 TLS Secret 失败 → 返回错误、provisioning=failed、cert=failed，
// 且不应继续建 Ingress（写 Secret 失败时 Ingress 引用的证书还不存在）。
func TestProvisionTLSSecretFails(t *testing.T) {
	// 准备测试依赖：签发成功，但写 TLS Secret 注入失败
	cipher, _ := auth.NewCipher(make([]byte, 32))
	st := &fakeWPProvStore{cfg: newCfg(cipher)}
	prov := &fakeProvisioner{ret: acme.Certificate{CertPEM: []byte("C"), KeyPEM: []byte("K"), NotAfter: 1893456000}}
	cl := &fakeClusterApplier{tlsErr: assert.AnError}
	h := NewWebPublishProvisionHandler(st, prov, cl, cipher, WebPublishProvisionConfig{IngressPublicIP: "1.2.3.4"})

	// 执行 handler，期望返回错误（供 worker backoff 重试）
	err := h.Handle(context.Background(), provJob())
	require.Error(t, err)

	// 验证写 TLS Secret 失败后不应继续建 Ingress
	assert.False(t, cl.ingApplied, "写 TLS Secret 失败不应建 Ingress")

	// 验证最终 provisioning 状态为 failed
	assert.Equal(t, domain.ProvisioningFailed, st.provUpdates[len(st.provUpdates)-1].ProvisioningStatus)

	// 验证最终 cert 状态为 failed
	assert.Equal(t, domain.CertStatusFailed, st.certUpdates[len(st.certUpdates)-1].CertStatus)
}

// TestProvisionIngressFails 覆盖：签证书与写 TLS Secret 都成功，但建通配 Ingress 失败 →
// 返回错误、provisioning=failed、cert=failed（此时 tlsApplied=true、ingApplied=true 但返回了错误）。
func TestProvisionIngressFails(t *testing.T) {
	// 准备测试依赖：签发与写 Secret 成功，但建 Ingress 注入失败
	cipher, _ := auth.NewCipher(make([]byte, 32))
	st := &fakeWPProvStore{cfg: newCfg(cipher)}
	prov := &fakeProvisioner{ret: acme.Certificate{CertPEM: []byte("C"), KeyPEM: []byte("K"), NotAfter: 1893456000}}
	cl := &fakeClusterApplier{ingErr: assert.AnError}
	h := NewWebPublishProvisionHandler(st, prov, cl, cipher, WebPublishProvisionConfig{IngressPublicIP: "1.2.3.4"})

	// 执行 handler，期望返回错误（供 worker backoff 重试）
	err := h.Handle(context.Background(), provJob())
	require.Error(t, err)

	// 验证 TLS Secret 已写入、Ingress 也尝试创建（但失败）
	assert.True(t, cl.tlsApplied, "建 Ingress 前应已写入 TLS Secret")
	assert.True(t, cl.ingApplied, "应已尝试建通配 Ingress")

	// 验证最终 provisioning 状态为 failed
	assert.Equal(t, domain.ProvisioningFailed, st.provUpdates[len(st.provUpdates)-1].ProvisioningStatus)

	// 验证最终 cert 状态为 failed
	assert.Equal(t, domain.CertStatusFailed, st.certUpdates[len(st.certUpdates)-1].CertStatus)
}

// TestProvisionRenewalPath 覆盖续签场景：cfg.CertStatus=issued 时，handle 过程中
// cert 状态应先经过 renewing，最终置 issued，且成功时写 cert_last_renewed_at 而非 cert_last_issued_at。
func TestProvisionRenewalPath(t *testing.T) {
	// 准备依赖：cfg.CertStatus = issued，模拟已签发过的配置触发续签
	cipher, _ := auth.NewCipher(make([]byte, 32))
	cfg := newCfg(cipher)
	cfg.CertStatus = domain.CertStatusIssued // 已签发 → 本次为续签

	st := &fakeWPProvStore{cfg: cfg}
	// 模拟签发成功，NotAfter 为 2030-01-01 00:00:00 UTC
	prov := &fakeProvisioner{ret: acme.Certificate{CertPEM: []byte("C"), KeyPEM: []byte("K"), NotAfter: 1893456000}}
	cl := &fakeClusterApplier{}
	h := NewWebPublishProvisionHandler(st, prov, cl, cipher, WebPublishProvisionConfig{IngressPublicIP: "1.2.3.4"})

	// 执行 handler，期望无错误
	require.NoError(t, h.Handle(context.Background(), provJob()))

	// 验证中间状态：第一次 cert 更新应为 renewing（续签进行中），而非 issuing
	require.NotEmpty(t, st.certUpdates, "应至少有一次 cert 状态更新")
	assert.Equal(t, domain.CertStatusRenewing, st.certUpdates[0].CertStatus,
		"续签路径进行中状态应为 renewing")

	// 验证最终 cert 状态为 issued
	lastCert := st.certUpdates[len(st.certUpdates)-1]
	assert.Equal(t, domain.CertStatusIssued, lastCert.CertStatus, "续签完成后应置 issued")

	// 续签成功时应写 cert_last_renewed_at，不写 cert_last_issued_at
	assert.True(t, lastCert.CertLastRenewedAt.Valid, "续签成功应记录 cert_last_renewed_at")
	assert.False(t, lastCert.CertLastIssuedAt.Valid, "续签不应覆盖 cert_last_issued_at（COALESCE 保留原值）")

	// 验证最终 provisioning 状态为 ready
	require.NotEmpty(t, st.provUpdates)
	assert.Equal(t, domain.ProvisioningReady, st.provUpdates[len(st.provUpdates)-1].ProvisioningStatus)
}
