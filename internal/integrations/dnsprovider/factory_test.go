package dnsprovider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewUnknownProvider 覆盖：未知 provider 类型返回 ErrUnsupportedProvider，挡住脏配置。
func TestNewUnknownProvider(t *testing.T) {
	_, err := New(context.Background(), ProviderType("aws"), Credentials{}, "apps.example.com")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedProvider)
}

// TestNewAlidnsMissingCreds 覆盖：阿里云缺必填凭证时报错（签发前发现配置问题）。
func TestNewAlidnsMissingCreds(t *testing.T) {
	// access_key_id 缺失
	_, err := New(context.Background(), ProviderAlidns, Credentials{}, "apps.example.com")
	require.Error(t, err)
}

// TestNewAlidnsValidCreds 覆盖：阿里云凭证齐全时成功构造（DNS-01 provider 装配）。
// 注：lego alidns 构造器不发网络请求，仅初始化 SDK 客户端，故可用假凭证做冒烟测试。
func TestNewAlidnsValidCreds(t *testing.T) {
	p, err := New(context.Background(), ProviderAlidns,
		Credentials{"access_key_id": "AK", "access_key_secret": "SK"}, "apps.example.com")
	require.NoError(t, err)
	assert.NotNil(t, p)
}

// TestNewHuaweicloudMissingCreds 覆盖：华为云缺必填凭证时报错。
func TestNewHuaweicloudMissingCreds(t *testing.T) {
	// 仅提供 access_key_id，secret_access_key 缺失
	_, err := New(context.Background(), ProviderHuaweicloud,
		Credentials{"access_key_id": "AK"}, "apps.example.com")
	require.Error(t, err)
}

// TestNewHuaweicloudMissingRegion 覆盖：华为云缺 region 时报错（lego 构造器强校验 Region 非空）。
func TestNewHuaweicloudMissingRegion(t *testing.T) {
	// region 缺失，lego NewDNSProviderConfig 会返回 "credentials missing" 错误
	_, err := New(context.Background(), ProviderHuaweicloud,
		Credentials{"access_key_id": "AK", "secret_access_key": "SK"}, "apps.example.com")
	require.Error(t, err)
}

// NOTE: huaweicloud 无法做 valid-creds 冒烟测试——lego 的 NewDNSProviderConfig 内部
// 调用 hwdns.DnsClientBuilder().WithRegion(region).SafeBuild()，此调用会向华为云 IAM
// 发起真实网络请求获取 project ID，在无真实凭证的 CI 环境中会返回 401，因此省略该用例。

// TestNewTencentcloudMissingCreds 覆盖：腾讯云缺必填凭证时报错。
func TestNewTencentcloudMissingCreds(t *testing.T) {
	// secret_id 与 secret_key 均缺失
	_, err := New(context.Background(), ProviderTencentcloud, Credentials{}, "apps.example.com")
	require.Error(t, err)
}

// TestNewTencentcloudValidCreds 覆盖：腾讯云凭证齐全时成功构造。
// 注：lego tencentcloud 构造器不发网络请求，仅初始化 SDK 客户端，故可用假凭证做冒烟测试。
func TestNewTencentcloudValidCreds(t *testing.T) {
	p, err := New(context.Background(), ProviderTencentcloud,
		Credentials{"secret_id": "ID", "secret_key": "SK"}, "apps.example.com")
	require.NoError(t, err)
	assert.NotNil(t, p)
}

// TestNewCmcccloudMissingCreds 覆盖：移动云缺必填凭证时报错。
func TestNewCmcccloudMissingCreds(t *testing.T) {
	// access_key 与 secret_key 均缺失
	_, err := New(context.Background(), ProviderCmcccloud, Credentials{}, "apps.example.com")
	require.Error(t, err)
}

// TestNewCmcccloudValidCreds 覆盖：移动云凭证齐全时返回占位 provider（非 nil）。
func TestNewCmcccloudValidCreds(t *testing.T) {
	p, err := New(context.Background(), ProviderCmcccloud,
		Credentials{"access_key": "AK", "secret_key": "SK"}, "apps.example.com")
	require.NoError(t, err)
	assert.NotNil(t, p)
}
