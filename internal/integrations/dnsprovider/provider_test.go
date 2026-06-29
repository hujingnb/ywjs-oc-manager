package dnsprovider

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProviderTypeValid 覆盖：四个受支持的 provider 类型判定为合法，未知值判定为非法。
func TestProviderTypeValid(t *testing.T) {
	// 受支持的四家云厂商均应合法
	for _, pt := range []ProviderType{ProviderAlidns, ProviderHuaweicloud, ProviderTencentcloud, ProviderCmcccloud} {
		assert.Truef(t, pt.Valid(), "%s 应为合法 provider", pt)
	}
	// 空值与拼写错误应非法，避免脏数据进入签发流程
	assert.False(t, ProviderType("").Valid())
	assert.False(t, ProviderType("aws").Valid())
}

// TestCredentialsJSONRoundTrip 覆盖：凭证结构 JSON 编解码可逆，
// 这是落库前序列化（再交给 auth.Cipher 加密）的基础。
func TestCredentialsJSONRoundTrip(t *testing.T) {
	in := Credentials{"access_key_id": "AK", "access_key_secret": "SK"}
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	var out Credentials
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, in, out)
}
