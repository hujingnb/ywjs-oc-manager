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
