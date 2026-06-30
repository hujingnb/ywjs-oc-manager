// Package main 的 web-publish 装配测试：覆盖本地/dev 自签证书 provisioner 的产物正确性。
package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/worker/handlers"
)

// fakeAccountKeyStore 记录 GetOrCreateOpaqueSecretValue 调用次数，并模拟「已存在则返回同值」：
// 首次调用执行 gen 生成并缓存，后续返回缓存值（对齐真实 Secret 复用语义）。
type fakeAccountKeyStore struct {
	calls int
	value []byte
	err   error
}

func (f *fakeAccountKeyStore) GetOrCreateOpaqueSecretValue(_ context.Context, _, _ string, gen func() ([]byte, error)) ([]byte, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.value == nil {
		v, err := gen()
		if err != nil {
			return nil, err
		}
		f.value = v
	}
	return f.value, nil
}

// TestEnsureAccountKey_CachesAndReuses 验证账户私钥：从 store 读取/创建后进程内缓存，
// 多次 ensureAccountKey 只触达 store 一次且返回同一把私钥（复用同一 ACME 账户、避免新注册限流）。
func TestEnsureAccountKey_CachesAndReuses(t *testing.T) {
	store := &fakeAccountKeyStore{}
	c := &certProvisionerImpl{keyStore: store}

	k1, err := c.ensureAccountKey(context.Background())
	require.NoError(t, err)
	require.NotNil(t, k1)
	k2, err := c.ensureAccountKey(context.Background())
	require.NoError(t, err)
	// 缓存命中：store 只被调用一次，且两次返回同一把私钥。
	assert.Equal(t, 1, store.calls, "第二次应命中进程内缓存，不再访问 store")
	assert.Equal(t, k1, k2)
}

// TestEnsureAccountKey_NilStoreReturnsNil 验证未配置持久化（k8s 未启用）时返回 nil，
// 由 Issuer 退回为每次签发生成新账户（旧行为，不报错）。
func TestEnsureAccountKey_NilStoreReturnsNil(t *testing.T) {
	c := &certProvisionerImpl{keyStore: nil}
	k, err := c.ensureAccountKey(context.Background())
	require.NoError(t, err)
	assert.Nil(t, k)
}

// TestECAccountKeyPEM_RoundTrip 验证账户私钥 PEM 生成与解析可往返，得到可用的 crypto.Signer。
func TestECAccountKeyPEM_RoundTrip(t *testing.T) {
	pemBytes, err := generateECAccountKeyPEM()
	require.NoError(t, err)
	require.NotEmpty(t, pemBytes)
	key, err := parseECAccountKeyPEM(pemBytes)
	require.NoError(t, err)
	require.NotNil(t, key)
	require.NotNil(t, key.Public(), "解析出的私钥应能取出公钥")
}

// TestDevSelfSignedCertProvisioner_Provision 验证自签 provisioner 产出可解析的通配证书：
// 证书 PEM 可解析、SAN 同时覆盖 *.base_domain 与 base_domain、有效期约 90 天、
// 证书与返回的私钥匹配，且完全忽略传入的 DNS provider 类型与凭证（本地无真实凭证也能签）。
func TestDevSelfSignedCertProvisioner_Provision(t *testing.T) {
	const baseDomain = "sites.localhost"

	// 故意传入空 provider 类型与空凭证：自签路径不应依赖它们，仍须成功签发。
	cert, err := devSelfSignedCertProvisioner{}.Provision(context.Background(), handlers.CertProvisionInput{
		ProviderType: "",
		Credentials:  nil,
		BaseDomain:   baseDomain,
		IngressIP:    "",
	})
	require.NoError(t, err)
	require.NotEmpty(t, cert.CertPEM) // 证书 PEM 非空
	require.NotEmpty(t, cert.KeyPEM)  // 私钥 PEM 非空

	// 解析证书 PEM → x509 证书。
	block, _ := pem.Decode(cert.CertPEM)
	require.NotNil(t, block)                          // PEM 解码成功
	assert.Equal(t, "CERTIFICATE", block.Type)        // 块类型为 CERTIFICATE
	x509cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	// SAN 同时覆盖通配子域与基础域名本身：<slug>.base_domain 与 base_domain 都被覆盖。
	assert.ElementsMatch(t, []string{"*." + baseDomain, baseDomain}, x509cert.DNSNames)
	assert.Equal(t, "*."+baseDomain, x509cert.Subject.CommonName) // CN 为通配域

	// NotAfter 落库值与证书一致，且约为 90 天后（容忍 1 分钟时钟误差）。
	assert.Equal(t, x509cert.NotAfter.Unix(), cert.NotAfter)
	want := time.Now().UTC().Add(90 * 24 * time.Hour)
	assert.WithinDuration(t, want, time.Unix(cert.NotAfter, 0).UTC(), time.Minute)

	// 私钥 PEM 可解析为 RSA 私钥，且其公钥与证书内公钥一致（确认证书-私钥成对）。
	keyBlock, _ := pem.Decode(cert.KeyPEM)
	require.NotNil(t, keyBlock)
	priv, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	require.NoError(t, err)
	assert.True(t, priv.PublicKey.Equal(x509cert.PublicKey)) // 证书与私钥配对
}
