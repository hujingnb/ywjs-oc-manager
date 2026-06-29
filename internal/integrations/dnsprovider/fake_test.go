package dnsprovider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFakeProviderWildcardA 覆盖：EnsureWildcardA 写入后可查到，重复写同值幂等，
// DeleteWildcardA 删除后查不到且对不存在记录不报错。
func TestFakeProviderWildcardA(t *testing.T) {
	p := NewFakeProvider()
	ctx := context.Background()

	require.NoError(t, p.EnsureWildcardA(ctx, "apps.example.com", "1.2.3.4"))
	assert.Equal(t, "1.2.3.4", p.ARecords["*.apps.example.com"])

	require.NoError(t, p.EnsureWildcardA(ctx, "apps.example.com", "1.2.3.4"))

	require.NoError(t, p.DeleteWildcardA(ctx, "apps.example.com"))
	_, ok := p.ARecords["*.apps.example.com"]
	assert.False(t, ok)

	require.NoError(t, p.DeleteWildcardA(ctx, "apps.example.com"))
}

// TestFakeProviderInjectedError 覆盖：注入错误后 EnsureWildcardA 返回该错误，
// 供上层（acme.Issuer / provisioning 状态机）单测失败路径。
func TestFakeProviderInjectedError(t *testing.T) {
	p := NewFakeProvider()
	p.EnsureErr = assert.AnError
	assert.ErrorIs(t, p.EnsureWildcardA(context.Background(), "apps.example.com", "1.2.3.4"), assert.AnError)
}
