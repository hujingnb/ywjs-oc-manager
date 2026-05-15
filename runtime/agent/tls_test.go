package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestEnsureSelfSignedCert_GeneratesAndPersists 校验首次调用生成证书并写入 stateDir。
func TestEnsureSelfSignedCert_GeneratesAndPersists(t *testing.T) {
	stateDir := t.TempDir()
	bundle, err := EnsureSelfSignedCert(stateDir, "test-host")
	require.NoError(t, err)
	for _, name := range []string{"agent-tls.crt", "agent-tls.key", "agent-tls.ca.crt"} {
		path := filepath.Join(stateDir, name)
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.NotEqual(t, 0, info.Size())
		require.Equal(t, fs.FileMode(0), info.Mode().Perm()&0o077)
	}
	require.True(t, strings.Contains(string(bundle.CACertPEM), "-----BEGIN CERTIFICATE-----"))
	require.True(t, strings.Contains(string(bundle.CertPEM), "-----BEGIN CERTIFICATE-----"))
	require.True(t, strings.Contains(string(bundle.KeyPEM), "PRIVATE KEY"))

	leaf := mustParseLeafCert(t, bundle.CertPEM)
	require.True(t, leaf.NotAfter.After(time.Now().AddDate(0, 11, 0)))
	hasLoopback := false
	for _, ip := range leaf.IPAddresses {
		if ip.Equal(net.IPv4(127, 0, 0, 1)) {
			hasLoopback = true
		}
	}
	require.True(t, hasLoopback)
	hasHostname := false
	for _, dns := range leaf.DNSNames {
		if dns == "test-host" {
			hasHostname = true
		}
	}
	require.True(t, hasHostname)
}

// TestEnsureSelfSignedCert_ReusesExisting 校验已有合法证书时不重复生成。
func TestEnsureSelfSignedCert_ReusesExisting(t *testing.T) {
	stateDir := t.TempDir()
	first, err := EnsureSelfSignedCert(stateDir, "host")
	require.NoError(t, err)
	mtimeBefore := mustMTime(t, filepath.Join(stateDir, "agent-tls.crt"))
	time.Sleep(10 * time.Millisecond)
	second, err := EnsureSelfSignedCert(stateDir, "host")
	require.NoError(t, err)
	mtimeAfter := mustMTime(t, filepath.Join(stateDir, "agent-tls.crt"))
	require.True(t, mtimeBefore.Equal(mtimeAfter))
	require.Equal(t, string(second.CertPEM), string(first.CertPEM))
}

// TestEnsureSelfSignedCert_RegeneratesWhenHostnameChanges 验证确保自身SignedCert重新生成当Hostname变更s的特殊分支或幂等场景。
func TestEnsureSelfSignedCert_RegeneratesWhenHostnameChanges(t *testing.T) {
	stateDir := t.TempDir()
	first, err := EnsureSelfSignedCert(stateDir, "old-host")
	require.NoError(t, err)

	second, err := EnsureSelfSignedCert(stateDir, "new-host")
	require.NoError(t, err)

	require.NotEqual(t, string(first.CertPEM), string(second.CertPEM))
	leaf := mustParseLeafCert(t, second.CertPEM)
	require.Contains(t, leaf.DNSNames, "new-host")
}

// TestEnsureSelfSignedCert_AdvertiseHostIPv4InSAN 校验当 advertise_host 是 IPv4 字面量时
// 该 IP 会进入证书的 IPAddresses SAN 而非 DNSNames;
// 这是修复 manager 主动探测 https://<IP>:7001 出现 "x509 certificate is valid for ... not <IP>" 的关键。
func TestEnsureSelfSignedCert_AdvertiseHostIPv4InSAN(t *testing.T) {
	stateDir := t.TempDir()
	bundle, err := EnsureSelfSignedCert(stateDir, "192.168.0.43")
	require.NoError(t, err)

	leaf := mustParseLeafCert(t, bundle.CertPEM)
	hasAdvertiseIP := false
	for _, ip := range leaf.IPAddresses {
		if ip.Equal(net.ParseIP("192.168.0.43")) {
			hasAdvertiseIP = true
		}
	}
	require.True(t, hasAdvertiseIP, "advertise IP 必须出现在 IPAddresses SAN")
	// IP 字面量不应被错误地塞进 DNSNames,否则 Go TLS 校验会按 DNS 规则匹配失败。
	for _, dns := range leaf.DNSNames {
		require.NotEqual(t, "192.168.0.43", dns)
	}

	// 端到端校验:用真实 TLS 握手验证 manager 端按 IP 拨号能通过 SAN 校验。
	cert, err := tls.X509KeyPair(bundle.CertPEM, bundle.KeyPEM)
	require.NoError(t, err)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	require.NoError(t, err)
	defer listener.Close()
	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		if tlsConn, ok := conn.(*tls.Conn); ok {
			_ = tlsConn.Handshake()
		}
		conn.Close()
	}()
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(bundle.CACertPEM))
	// ServerName 模拟 manager 端在 https URL 里写 IP 字面量的场景。
	dialer := &tls.Dialer{Config: &tls.Config{RootCAs: pool, ServerName: "192.168.0.43", MinVersion: tls.VersionTLS12}}
	conn, err := dialer.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	conn.Close()
}

// TestEnsureSelfSignedCert_AdvertiseHostIPv6InSAN 校验 IPv6 字面量同样会进 IPAddresses。
// 业务上 advertise_host 配 IPv6 (例如 `::1` 之外的全局地址) 也是合法部署。
func TestEnsureSelfSignedCert_AdvertiseHostIPv6InSAN(t *testing.T) {
	stateDir := t.TempDir()
	bundle, err := EnsureSelfSignedCert(stateDir, "fd00::1")
	require.NoError(t, err)

	leaf := mustParseLeafCert(t, bundle.CertPEM)
	hasAdvertiseIP := false
	for _, ip := range leaf.IPAddresses {
		if ip.Equal(net.ParseIP("fd00::1")) {
			hasAdvertiseIP = true
		}
	}
	require.True(t, hasAdvertiseIP, "IPv6 advertise host 必须出现在 IPAddresses SAN")
	for _, dns := range leaf.DNSNames {
		require.NotEqual(t, "fd00::1", dns)
	}
}

// TestEnsureSelfSignedCert_AdvertiseHostLoopbackNotDuplicated 校验当 advertise_host
// 显式给 "127.0.0.1" 时不会在 IPAddresses 里产生重复条目;
// 重复 SAN 不影响校验,但会让证书内容含噪,且影响后续按 SAN 数量做的回归断言。
func TestEnsureSelfSignedCert_AdvertiseHostLoopbackNotDuplicated(t *testing.T) {
	stateDir := t.TempDir()
	bundle, err := EnsureSelfSignedCert(stateDir, "127.0.0.1")
	require.NoError(t, err)

	leaf := mustParseLeafCert(t, bundle.CertPEM)
	loopbackCount := 0
	for _, ip := range leaf.IPAddresses {
		if ip.Equal(net.IPv4(127, 0, 0, 1)) {
			loopbackCount++
		}
	}
	require.Equal(t, 1, loopbackCount, "loopback IP 不应被重复写入 SAN")
}

// TestEnsureSelfSignedCert_RegeneratesWhenAdvertiseIPMissing 验证从旧版 agent
// 升级上来时的自愈路径:旧证书 IPAddresses 只有 [127.0.0.1, ::1],新版以 advertise IP
// 启动后必须识别为 SAN 不匹配并重建证书,否则 manager 探测仍会失败。
func TestEnsureSelfSignedCert_RegeneratesWhenAdvertiseIPMissing(t *testing.T) {
	stateDir := t.TempDir()
	// 用老逻辑(IP 进 DNSNames、IPAddresses 只有 loopback)造一份"陈旧但未过期"的证书;
	// 模拟线上已经签发、但缺 advertise IP SAN 的场景。
	require.NoError(t, writeLegacyCertBundle(stateDir, "192.168.0.43"))
	legacyPEM, err := os.ReadFile(filepath.Join(stateDir, "agent-tls.crt"))
	require.NoError(t, err)

	bundle, err := EnsureSelfSignedCert(stateDir, "192.168.0.43")
	require.NoError(t, err)
	require.NotEqual(t, string(legacyPEM), string(bundle.CertPEM), "缺少 IP SAN 的旧证书必须被重建")

	leaf := mustParseLeafCert(t, bundle.CertPEM)
	hasIP := false
	for _, ip := range leaf.IPAddresses {
		if ip.Equal(net.ParseIP("192.168.0.43")) {
			hasIP = true
		}
	}
	require.True(t, hasIP)
}

// TestEnsureSelfSignedCert_RegeneratesWhenExpired 校验过期证书会被重新生成。
func TestEnsureSelfSignedCert_RegeneratesWhenExpired(t *testing.T) {
	stateDir := t.TempDir()
	err := writeExpiredCertBundle(stateDir)
	require.NoError(t, err)
	expiredPEM, err := os.ReadFile(filepath.Join(stateDir, "agent-tls.crt"))
	require.NoError(t, err)
	bundle, err := EnsureSelfSignedCert(stateDir, "host")
	require.NoError(t, err)
	require.NotEqual(t, string(expiredPEM), string(bundle.CertPEM))
	leaf := mustParseLeafCert(t, bundle.CertPEM)
	require.True(t, leaf.NotAfter.After(time.Now()))
}

// TestEnsureSelfSignedCert_TLSHandshake 校验生成的 cert/key 能完成 TLS 握手。
func TestEnsureSelfSignedCert_TLSHandshake(t *testing.T) {
	stateDir := t.TempDir()
	bundle, err := EnsureSelfSignedCert(stateDir, "localhost")
	require.NoError(t, err)
	cert, err := tls.X509KeyPair(bundle.CertPEM, bundle.KeyPEM)
	require.NoError(t, err)
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	require.NoError(t, err)
	defer listener.Close()
	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		// 必须显式 Handshake 才会触发 TLS 握手，Accept 本身只完成 TCP。
		if tlsConn, ok := conn.(*tls.Conn); ok {
			_ = tlsConn.Handshake()
		}
		conn.Close()
	}()
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(bundle.CACertPEM))
	dialer := &tls.Dialer{Config: &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}}
	conn, err := dialer.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	conn.Close()
}

func mustParseLeafCert(t *testing.T, pemBytes []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatal("找不到 CERTIFICATE PEM block")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return leaf
}

func mustMTime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info.ModTime()
}

// writeExpiredCertBundle 在 stateDir 写入一组已过期的自签证书，用于回归测试自动重建逻辑。
func writeExpiredCertBundle(stateDir string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "expired"},
		NotBefore:             time.Now().Add(-2 * 24 * time.Hour),
		NotAfter:              time.Now().Add(-1 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		DNSNames:              []string{"host"},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(filepath.Join(stateDir, "agent-tls.crt"), certPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stateDir, "agent-tls.key"), keyPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stateDir, "agent-tls.ca.crt"), certPEM, 0o600); err != nil {
		return err
	}
	return nil
}

// writeLegacyCertBundle 在 stateDir 写入一组"未过期但 SAN 不含 advertise IP"的证书,
// 用于回归测试线上从旧版 agent 升级时的自愈路径(loadCertBundle 应识别为不匹配并重建)。
func writeLegacyCertBundle(stateDir, hostname string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "legacy"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(30 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		// 故意把 IP 字面量放进 DNSNames 复现老版本逻辑,SAN 校验时仍然不通过。
		DNSNames:              []string{"localhost", hostname},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(filepath.Join(stateDir, "agent-tls.crt"), certPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stateDir, "agent-tls.key"), keyPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stateDir, "agent-tls.ca.crt"), certPEM, 0o600); err != nil {
		return err
	}
	return nil
}
