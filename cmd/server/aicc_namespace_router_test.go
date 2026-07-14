package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// TestAppKindResolverResolveAppType 验证路由解析器保留数据库应用类型，
// 并拒绝未知类型，避免编排器误路由到普通 namespace。
func TestAppKindResolverResolveAppType(t *testing.T) {
	testCases := []struct {
		name    string
		appType string
		want    domain.AppType
		wantErr bool
	}{
		// 普通应用必须解析为 standard，供正常 namespace 使用。
		{name: "普通应用", appType: string(domain.AppTypeStandard), want: domain.AppTypeStandard},
		// AICC 应用必须解析为 aicc，供客服专属 namespace 使用。
		{name: "客服应用", appType: string(domain.AppTypeAICC), want: domain.AppTypeAICC},
		// 未知持久化类型不能被默认为普通应用，必须显式失败。
		{name: "未知类型", appType: "unknown", wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			resolver := appKindResolver{store: appKindStoreStub{app: sqlc.App{AppType: testCase.appType}}}
			appType, err := resolver.ResolveAppType(context.Background(), "app-1")
			if testCase.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.want, appType)
		})
	}
}

// appKindStoreStub 提供 app 类型解析所需的最小 store 行为。
type appKindStoreStub struct {
	app sqlc.App
	err error
}

// GetApp 返回预置应用或查询错误，覆盖 resolver 的数据库读取边界。
func (s appKindStoreStub) GetApp(_ context.Context, _ string) (sqlc.App, error) {
	if s.err != nil {
		return sqlc.App{}, s.err
	}
	return s.app, nil
}

// TestAppKindResolverResolveAppTypePropagatesStoreError 验证数据库读取失败不降级为 standard。
func TestAppKindResolverResolveAppTypePropagatesStoreError(t *testing.T) {
	resolver := appKindResolver{store: appKindStoreStub{err: errors.New("store unavailable")}}

	// store 错误必须向上返回，由编排调用方终止操作，避免使用错误 namespace。
	_, err := resolver.ResolveAppType(context.Background(), "app-1")
	require.Error(t, err)
}
