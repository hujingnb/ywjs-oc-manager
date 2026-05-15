package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPrincipalContextRoundTrip 覆盖正常路径：注入再取出，
// UserID / OrgID / Role 三个字段必须与原值完全一致，
// 任意字段错位都会导致 service 层权限判断失效。
func TestPrincipalContextRoundTrip(t *testing.T) {
	original := Principal{UserID: "user-1", OrgID: "org-9", Role: "org_admin"}
	ctx := WithPrincipal(context.Background(), original)

	got, ok := PrincipalFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, original, got)
}

// TestPrincipalFromContext_Empty 覆盖未注入主体的边界：
// 当请求未经过 RequireUserAuth 中间件（如 public / agent 路由组），
// 取出必须返回 (Principal{}, false)，不应触发 panic 或返回脏数据。
func TestPrincipalFromContext_Empty(t *testing.T) {
	got, ok := PrincipalFromContext(context.Background())
	assert.False(t, ok)
	assert.Equal(t, Principal{}, got)
}

// TestPrincipalFromContext_WrongType 覆盖类型擦除事故：
// 调用方用与 principalContextKey 不同的 key 写入字符串值，
// PrincipalFromContext 必须基于 key 严格匹配，不会被任意 ctx 值误命中。
func TestPrincipalFromContext_WrongType(t *testing.T) {
	type bogusKey struct{}
	ctx := context.WithValue(context.Background(), bogusKey{}, "not-a-principal")

	got, ok := PrincipalFromContext(ctx)
	assert.False(t, ok)
	assert.Equal(t, Principal{}, got)
}

// TestPrincipalFromContext_TypeAssertSafe 覆盖另一类擦除事故：
// 若有人误用相同 key 注入了非 Principal 类型，类型断言必须 ok=false 返回零值，
// 而不是 panic；这是防御性测试，保障运行期稳定。
func TestPrincipalFromContext_TypeAssertSafe(t *testing.T) {
	ctx := context.WithValue(context.Background(), principalContextKey{}, "string-value")

	got, ok := PrincipalFromContext(ctx)
	assert.False(t, ok)
	assert.Equal(t, Principal{}, got)
}
