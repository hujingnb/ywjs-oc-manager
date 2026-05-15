package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CertBundle 是一组自签证书的内存视图。
//
// 三份 PEM 都需要：
//   - CertPEM：服务器 leaf 证书，agent 启动 TLS server 时使用；
//   - KeyPEM：对应私钥；
//   - CACertPEM：注册阶段上报给 manager，让 manager 验证 agent 自签 TLS。
//
// 第一版只用同一份证书既作 CA 又作 leaf（自签且 BasicConstraintsValid=true），
// 简化部署。多 agent 部署后续可以演进为单独的 CA 与按节点签发的 leaf。
type CertBundle struct {
	CertPEM   []byte
	KeyPEM    []byte
	CACertPEM []byte
}

// 证书文件名约定，便于运维直接定位。
const (
	certFileName   = "agent-tls.crt"
	keyFileName    = "agent-tls.key"
	caCertFileName = "agent-tls.ca.crt"
)

// certValidDuration 是新签发证书的有效期。
// 选 1 年是为了避免每次重启都重新生成；过期会自动重建，不影响可用性。
const certValidDuration = 365 * 24 * time.Hour

// EnsureSelfSignedCert 加载或生成一组自签 TLS 证书并写入 stateDir。
//
// 行为：
//   - stateDir 下三份文件齐备且 leaf 未过期 → 直接读盘并返回；
//   - 任何文件缺失、解析失败或证书已过期 → 用 ECDSA P-256 重新生成并覆盖写盘；
//   - 文件权限固定为 0o600，避免节点上其他进程读取私钥。
//
// hostname 会作为 DNS SAN 加入证书，便于 manager 用 hostname 验证 TLS。
func EnsureSelfSignedCert(stateDir, hostname string) (CertBundle, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return CertBundle{}, fmt.Errorf("创建 state 目录失败: %w", err)
	}
	if bundle, ok := loadCertBundle(stateDir, hostname); ok {
		return bundle, nil
	}
	bundle, err := generateCertBundle(hostname)
	if err != nil {
		return CertBundle{}, err
	}
	if err := writeCertBundle(stateDir, bundle); err != nil {
		return CertBundle{}, err
	}
	return bundle, nil
}

// loadCertBundle 尝试从 stateDir 读取并校验证书；任何步骤失败都视作需要重建。
func loadCertBundle(stateDir, hostname string) (CertBundle, bool) {
	certPEM, err := os.ReadFile(filepath.Join(stateDir, certFileName))
	if err != nil {
		return CertBundle{}, false
	}
	keyPEM, err := os.ReadFile(filepath.Join(stateDir, keyFileName))
	if err != nil {
		return CertBundle{}, false
	}
	caPEM, err := os.ReadFile(filepath.Join(stateDir, caCertFileName))
	if err != nil {
		return CertBundle{}, false
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return CertBundle{}, false
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return CertBundle{}, false
	}
	// 留 24h 余量：临近过期时主动重建，避免运行期突然失效。
	if time.Now().Add(24 * time.Hour).After(leaf.NotAfter) {
		return CertBundle{}, false
	}
	if !certificateMatchesHostname(leaf, hostname) {
		return CertBundle{}, false
	}
	return CertBundle{CertPEM: certPEM, KeyPEM: keyPEM, CACertPEM: caPEM}, true
}

// generateCertBundle 生成一组新的自签 ECDSA-P256 证书。
func generateCertBundle(hostname string) (CertBundle, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return CertBundle{}, fmt.Errorf("生成 ECDSA key 失败: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return CertBundle{}, fmt.Errorf("生成 serial 失败: %w", err)
	}
	ips, dnsNames := certSANs(hostname)
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "oc-runtime-agent"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(certValidDuration),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth,
		},
		IPAddresses:           ips,
		DNSNames:              dnsNames,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return CertBundle{}, fmt.Errorf("签发证书失败: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return CertBundle{}, fmt.Errorf("序列化 EC private key 失败: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return CertBundle{CertPEM: certPEM, KeyPEM: keyPEM, CACertPEM: certPEM}, nil
}

// certSANs 把 agent 启动期感知到的 hostname 分流到正确的 SAN 列表。
//
// x509 规范要求 IP 字面量(IPv4/IPv6)走 IPAddresses,DNS 名走 DNSNames;
// 把 IP 误塞进 DNSNames 时 Go 标准库 TLS 校验会失败(SAN 不匹配),线上 manager
// 探测 https://<IP>:7001 出现 "valid for 127.0.0.1, ::1, not <IP>" 即此场景。
//
// 127.0.0.1/::1 与 localhost 始终保留,确保容器内自检 / 本机调试可用。
func certSANs(hostname string) (ips []net.IP, dnsNames []string) {
	ips = []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	dnsNames = []string{"localhost"}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" || hostname == "localhost" {
		return ips, dnsNames
	}
	if ip := net.ParseIP(hostname); ip != nil {
		for _, existing := range ips {
			if existing.Equal(ip) {
				return ips, dnsNames
			}
		}
		ips = append(ips, ip)
		return ips, dnsNames
	}
	dnsNames = append(dnsNames, hostname)
	return ips, dnsNames
}

func certificateMatchesHostname(cert *x509.Certificate, hostname string) bool {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" || hostname == "localhost" {
		return true
	}
	if ip := net.ParseIP(hostname); ip != nil {
		for _, certIP := range cert.IPAddresses {
			if certIP.Equal(ip) {
				return true
			}
		}
		return false
	}
	for _, dns := range cert.DNSNames {
		if strings.EqualFold(dns, hostname) {
			return true
		}
	}
	return false
}

func writeCertBundle(stateDir string, bundle CertBundle) error {
	for _, item := range []struct {
		name    string
		content []byte
	}{
		{certFileName, bundle.CertPEM},
		{keyFileName, bundle.KeyPEM},
		{caCertFileName, bundle.CACertPEM},
	} {
		path := filepath.Join(stateDir, item.name)
		if err := os.WriteFile(path, item.content, 0o600); err != nil {
			return fmt.Errorf("写入 %s 失败: %w", item.name, err)
		}
	}
	return nil
}
