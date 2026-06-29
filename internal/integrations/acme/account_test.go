package acme

import (
	"crypto"
	"testing"

	"github.com/go-acme/lego/v5/registration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewAccountImplementsUser 覆盖：account 满足 lego registration.User 接口，
// 且暴露的 email / 私钥与构造入参一致——lego.NewClient 需要一个 User。
//
// 注：lego v5 中 registration.User 的 GetPrivateKey 返回 crypto.Signer（而非 v4
// 的 crypto.PrivateKey），GetRegistration 返回 *acme.ExtendedAccount。
func TestNewAccountImplementsUser(t *testing.T) {
	acc, err := newAccount("ops@example.com")
	require.NoError(t, err)

	// 编译期+运行期确认实现 registration.User 接口
	var _ registration.User = acc
	assert.Equal(t, "ops@example.com", acc.GetEmail())
	assert.NotNil(t, acc.GetPrivateKey())

	// 同一账户的私钥应稳定（多次取用是同一把），保证 lego 注册一致
	var k crypto.Signer = acc.GetPrivateKey()
	assert.Equal(t, k, acc.GetPrivateKey())
}
