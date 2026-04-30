package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// dockerStubServer 启动一个 TLS http server 模拟 agent 的 docker 代理端点，
// 返回 server、self-signed CA PEM 和共享指针让用例断言收到的鉴权头与路径。
type dockerSeen struct {
	mu    sync.Mutex
	auth  string
	paths []string
}

func (s *dockerSeen) record(auth, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auth = auth
	s.paths = append(s.paths, path)
}

func (s *dockerSeen) snapshot() (string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := append([]string(nil), s.paths...)
	return s.auth, clone
}

func startDockerStubServer(t *testing.T) (*httptest.Server, []byte, *dockerSeen) {
	t.Helper()
	seen := &dockerSeen{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		seen.record(r.Header.Get("Authorization"), r.URL.Path)
		w.Header().Set("Api-Version", "1.41")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Components": []any{},
			"ApiVersion": "1.41",
			"Version":    "stub",
			"OSType":     "linux",
			"Os":         "linux",
			"Arch":       "amd64",
		})
	})
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	caPEM := encodeCertPEM(server.Certificate().Raw)
	return server, caPEM, seen
}

func TestNewDockerClientForNode_PingPropagatesBearerOverTLS(t *testing.T) {
	server, caPEM, seen := startDockerStubServer(t)

	cli, err := NewDockerClientForNode(server.URL, "agent-secret", string(caPEM))
	if err != nil {
		t.Fatalf("NewDockerClientForNode err = %v", err)
	}
	defer cli.Close()

	if _, err := cli.Ping(context.Background()); err != nil {
		t.Fatalf("Ping err = %v", err)
	}
	auth, paths := seen.snapshot()
	if auth != "Bearer agent-secret" {
		t.Fatalf("agent 收到的 Authorization = %q, want Bearer agent-secret", auth)
	}
	if len(paths) == 0 {
		t.Fatal("没有捕获到任何请求 path")
	}
}

func TestNewDockerClientForNode_RejectsUntrustedTLS(t *testing.T) {
	server, _, seen := startDockerStubServer(t)
	otherPEM := makeUnrelatedCAPEM(t)
	cli, err := NewDockerClientForNode(server.URL, "secret", otherPEM)
	if err != nil {
		t.Fatalf("NewDockerClientForNode err = %v", err)
	}
	defer cli.Close()

	if _, err := cli.Ping(context.Background()); err == nil {
		t.Fatal("Ping 应失败但成功，TLS 校验未生效")
	}
	if auth, _ := seen.snapshot(); auth != "" {
		t.Fatalf("不应到达 server: auth=%q", auth)
	}
}

func TestNewDockerClientForNode_RejectsBadCAPEM(t *testing.T) {
	if _, err := NewDockerClientForNode("https://1.2.3.4:7001", "secret", "not-a-pem"); err == nil {
		t.Fatal("非法 CA PEM 应直接返回构造错误")
	}
}

func TestNewDockerClientForNode_PrefixesDockerPath(t *testing.T) {
	server, caPEM, seen := startDockerStubServer(t)

	cli, err := NewDockerClientForNode(server.URL, "secret", string(caPEM))
	if err != nil {
		t.Fatalf("NewDockerClientForNode err = %v", err)
	}
	defer cli.Close()

	if _, err := cli.Ping(context.Background()); err != nil {
		t.Fatalf("Ping err = %v", err)
	}
	_, paths := seen.snapshot()
	for _, p := range paths {
		if !strings.HasPrefix(p, "/v1/docker/") {
			t.Fatalf("agent 收到的 path = %q, 期望 /v1/docker/ 前缀", p)
		}
	}
}

func TestNewDockerClientForNode_AcceptsCertPool(t *testing.T) {
	server, caPEM, _ := startDockerStubServer(t)
	if pool := x509.NewCertPool(); !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("AppendCertsFromPEM 失败")
	}
	if _, err := NewDockerClientForNode(server.URL, "x", string(caPEM)); err != nil {
		t.Fatalf("构造失败: %v", err)
	}
}

// makeUnrelatedCAPEM 生成一段全新的 self-signed CA PEM，用于反向验证 TLS 校验生效。
// 不能复用 httptest.NewTLSServer，因为后者所有实例共享 httptest.LocalhostCert，
// 测试里不同实例之间互相能验证通过，无法证明信任链生效。
func makeUnrelatedCAPEM(t *testing.T) string {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成 key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "unrelated"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("生成证书: %v", err)
	}
	return string(encodeCertPEM(der))
}

// encodeCertPEM 把 DER 证书包成 PEM 文本，避免依赖 encoding/pem 里的 EncodeToMemory。
func encodeCertPEM(der []byte) []byte {
	const begin = "-----BEGIN CERTIFICATE-----\n"
	const end = "-----END CERTIFICATE-----\n"
	enc := base64.StdEncoding.EncodeToString(der)
	var sb strings.Builder
	sb.WriteString(begin)
	for i := 0; i < len(enc); i += 64 {
		stop := i + 64
		if stop > len(enc) {
			stop = len(enc)
		}
		sb.WriteString(enc[i:stop])
		sb.WriteByte('\n')
	}
	sb.WriteString(end)
	return []byte(sb.String())
}
