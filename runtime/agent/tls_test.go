package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEnsureSelfSignedCert_GeneratesAndPersists 校验首次调用生成证书并写入 stateDir。
func TestEnsureSelfSignedCert_GeneratesAndPersists(t *testing.T) {
	stateDir := t.TempDir()
	bundle, err := EnsureSelfSignedCert(stateDir, "test-host")
	if err != nil {
		t.Fatalf("EnsureSelfSignedCert err = %v", err)
	}
	for _, name := range []string{"agent-tls.crt", "agent-tls.key", "agent-tls.ca.crt"} {
		path := filepath.Join(stateDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("文件 %s 不存在: %v", name, err)
		}
		if info.Size() == 0 {
			t.Fatalf("文件 %s 为空", name)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("文件 %s 权限过宽: %v", name, info.Mode().Perm())
		}
	}
	if !strings.Contains(string(bundle.CACertPEM), "-----BEGIN CERTIFICATE-----") {
		t.Fatalf("CA PEM 内容不是 PEM: %q", bundle.CACertPEM)
	}
	if !strings.Contains(string(bundle.CertPEM), "-----BEGIN CERTIFICATE-----") {
		t.Fatalf("leaf cert 不是 PEM")
	}
	if !strings.Contains(string(bundle.KeyPEM), "PRIVATE KEY") {
		t.Fatalf("key 不是 PEM 私钥")
	}

	leaf := mustParseLeafCert(t, bundle.CertPEM)
	if !leaf.NotAfter.After(time.Now().AddDate(0, 11, 0)) {
		t.Fatalf("证书有效期 < 11 个月: %v", leaf.NotAfter)
	}
	hasLoopback := false
	for _, ip := range leaf.IPAddresses {
		if ip.Equal(net.IPv4(127, 0, 0, 1)) {
			hasLoopback = true
		}
	}
	if !hasLoopback {
		t.Fatalf("证书未包含 127.0.0.1 SAN: %+v", leaf.IPAddresses)
	}
	hasHostname := false
	for _, dns := range leaf.DNSNames {
		if dns == "test-host" {
			hasHostname = true
		}
	}
	if !hasHostname {
		t.Fatalf("证书未包含主机名 SAN: %+v", leaf.DNSNames)
	}
}

// TestEnsureSelfSignedCert_ReusesExisting 校验已有合法证书时不重复生成。
func TestEnsureSelfSignedCert_ReusesExisting(t *testing.T) {
	stateDir := t.TempDir()
	first, err := EnsureSelfSignedCert(stateDir, "host")
	if err != nil {
		t.Fatalf("first call err = %v", err)
	}
	mtimeBefore := mustMTime(t, filepath.Join(stateDir, "agent-tls.crt"))
	time.Sleep(10 * time.Millisecond)
	second, err := EnsureSelfSignedCert(stateDir, "host")
	if err != nil {
		t.Fatalf("second call err = %v", err)
	}
	mtimeAfter := mustMTime(t, filepath.Join(stateDir, "agent-tls.crt"))
	if !mtimeBefore.Equal(mtimeAfter) {
		t.Fatalf("证书文件被重写，首次=%v 二次=%v", mtimeBefore, mtimeAfter)
	}
	if string(first.CertPEM) != string(second.CertPEM) {
		t.Fatalf("两次返回的 leaf cert 不一致")
	}
}

// TestEnsureSelfSignedCert_RegeneratesWhenExpired 校验过期证书会被重新生成。
func TestEnsureSelfSignedCert_RegeneratesWhenExpired(t *testing.T) {
	stateDir := t.TempDir()
	if err := writeExpiredCertBundle(stateDir); err != nil {
		t.Fatalf("写入过期 bundle: %v", err)
	}
	expiredPEM, err := os.ReadFile(filepath.Join(stateDir, "agent-tls.crt"))
	if err != nil {
		t.Fatalf("读取 expired pem: %v", err)
	}
	bundle, err := EnsureSelfSignedCert(stateDir, "host")
	if err != nil {
		t.Fatalf("EnsureSelfSignedCert err = %v", err)
	}
	if string(bundle.CertPEM) == string(expiredPEM) {
		t.Fatal("过期证书未被替换")
	}
	leaf := mustParseLeafCert(t, bundle.CertPEM)
	if !leaf.NotAfter.After(time.Now()) {
		t.Fatalf("新证书仍处于过期状态: %v", leaf.NotAfter)
	}
}

// TestEnsureSelfSignedCert_TLSHandshake 校验生成的 cert/key 能完成 TLS 握手。
func TestEnsureSelfSignedCert_TLSHandshake(t *testing.T) {
	stateDir := t.TempDir()
	bundle, err := EnsureSelfSignedCert(stateDir, "localhost")
	if err != nil {
		t.Fatalf("EnsureSelfSignedCert err = %v", err)
	}
	cert, err := tls.X509KeyPair(bundle.CertPEM, bundle.KeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair err = %v", err)
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatalf("tls.Listen err = %v", err)
	}
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
	if !pool.AppendCertsFromPEM(bundle.CACertPEM) {
		t.Fatal("无法把 CA PEM 加入 pool")
	}
	dialer := &tls.Dialer{Config: &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}}
	conn, err := dialer.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial err = %v", err)
	}
	conn.Close()
}

func mustParseLeafCert(t *testing.T, pemBytes []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatal("找不到 CERTIFICATE PEM block")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate err = %v", err)
	}
	return leaf
}

func mustMTime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
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
