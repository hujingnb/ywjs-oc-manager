package acme

import (
	"context"
	"testing"

	"oc-manager/internal/integrations/dnsprovider"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeObtainer 是 obtainer 的测试替身：记录被请求的域名，返回预置 PEM 或预置错误。
type fakeObtainer struct {
	gotDomains []string
	ret        Certificate
	err        error
}

func (f *fakeObtainer) Obtain(_ context.Context, domains []string) (Certificate, error) {
	f.gotDomains = domains
	if f.err != nil {
		return Certificate{}, f.err
	}
	return f.ret, nil
}

// TestIssuerIssueHappyPath 覆盖：正常签发先写通配 A 记录、再用通配域名请求证书、返回 PEM。
func TestIssuerIssueHappyPath(t *testing.T) {
	fp := dnsprovider.NewFakeProvider()
	ob := &fakeObtainer{ret: Certificate{CertPEM: []byte("CERT"), KeyPEM: []byte("KEY")}}
	iss := newIssuerWithObtainer(fp, ob)

	cert, err := iss.Issue(context.Background(), "apps.example.com", "1.2.3.4")
	require.NoError(t, err)

	assert.Equal(t, "1.2.3.4", fp.ARecords["*.apps.example.com"])
	assert.Equal(t, []string{"*.apps.example.com"}, ob.gotDomains)
	assert.Equal(t, []byte("CERT"), cert.CertPEM)
	assert.Equal(t, []byte("KEY"), cert.KeyPEM)
}

// TestIssuerIssueDNSFailsSkipsObtain 覆盖：写 A 记录失败直接返回、不请求证书（不浪费 ACME 配额）。
func TestIssuerIssueDNSFailsSkipsObtain(t *testing.T) {
	fp := dnsprovider.NewFakeProvider()
	fp.EnsureErr = assert.AnError
	ob := &fakeObtainer{}
	iss := newIssuerWithObtainer(fp, ob)

	_, err := iss.Issue(context.Background(), "apps.example.com", "1.2.3.4")
	require.Error(t, err)
	assert.Nil(t, ob.gotDomains, "DNS 失败时不应请求证书")
}

// TestIssuerIssueObtainFails 覆盖：签发失败时把错误透传给调用方。
func TestIssuerIssueObtainFails(t *testing.T) {
	fp := dnsprovider.NewFakeProvider()
	ob := &fakeObtainer{err: assert.AnError}
	iss := newIssuerWithObtainer(fp, ob)
	_, err := iss.Issue(context.Background(), "apps.example.com", "1.2.3.4")
	assert.ErrorIs(t, err, assert.AnError)
}
