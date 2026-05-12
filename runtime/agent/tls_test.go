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
