// ocops_test.go —— OcOps 错误映射与 OcOpsResolverFromStore 解析的单元测试。
package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/ocops"
	"oc-manager/internal/store/sqlc"
)

// TestMapOcOpsCronErr 覆盖 mapOcOpsCronErr 的全部分支：
// nil、四个具名哨兵错误以及兜底分支，确保 ocops 错误被无损翻译成 cron service 哨兵错误。
func TestMapOcOpsCronErr(t *testing.T) {
	tests := []struct {
		name string
		in   error
		want error
	}{
		{name: "nil 透传 nil", in: nil, want: nil},                                                // 无错误时返回 nil
		{name: "BadRequest→ErrCronBadRequest", in: ocops.ErrBadRequest, want: ErrCronBadRequest},  // 400 参数非法
		{name: "NotFound→ErrNotFound", in: ocops.ErrNotFound, want: ErrNotFound},                  // 404 资源不存在
		{name: "Unsupported→ErrCronNotSupported", in: ocops.ErrUnsupported, want: ErrCronNotSupported}, // 409 不支持
		{name: "OutputInvalid→ErrCronOutputInvalid", in: ocops.ErrOutputInvalid, want: ErrCronOutputInvalid}, // 500 输出无效
		{name: "Unauthorized 走兜底→ErrCronCLI", in: ocops.ErrUnauthorized, want: ErrCronCLI},       // 401 未单独映射，兜底
		{name: "CLI 走兜底→ErrCronCLI", in: ocops.ErrCLI, want: ErrCronCLI},                          // 502 上游失败兜底
		{name: "未知错误走兜底→ErrCronCLI", in: errors.New("boom"), want: ErrCronCLI},                  // 非哨兵错误兜底
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapOcOpsCronErr(tt.in)
			if tt.want == nil {
				require.NoError(t, got)
				return
			}
			// 用 ErrorIs 校验语义等价（保留 wrap 链兼容）
			require.ErrorIs(t, got, tt.want)
		})
	}
}

// TestMapOcOpsKanbanErr 覆盖 mapOcOpsKanbanErr 的全部分支：
// nil、四个具名哨兵错误以及兜底分支，确保 ocops 错误被无损翻译成 kanban service 哨兵错误。
func TestMapOcOpsKanbanErr(t *testing.T) {
	tests := []struct {
		name string
		in   error
		want error
	}{
		{name: "nil 透传 nil", in: nil, want: nil},                                                      // 无错误时返回 nil
		{name: "BadRequest→ErrKanbanBadRequest", in: ocops.ErrBadRequest, want: ErrKanbanBadRequest},    // 400 参数非法
		{name: "NotFound→ErrNotFound", in: ocops.ErrNotFound, want: ErrNotFound},                        // 404 资源不存在
		{name: "Unsupported→ErrKanbanNotSupported", in: ocops.ErrUnsupported, want: ErrKanbanNotSupported}, // 409 不支持
		{name: "OutputInvalid→ErrKanbanOutputInvalid", in: ocops.ErrOutputInvalid, want: ErrKanbanOutputInvalid}, // 500 输出无效
		{name: "CLI 走兜底→ErrKanbanCLI", in: ocops.ErrCLI, want: ErrKanbanCLI},                            // 502 上游失败兜底
		{name: "未知错误走兜底→ErrKanbanCLI", in: errors.New("boom"), want: ErrKanbanCLI},                    // 非哨兵错误兜底
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapOcOpsKanbanErr(tt.in)
			if tt.want == nil {
				require.NoError(t, got)
				return
			}
			require.ErrorIs(t, got, tt.want)
		})
	}
}

// fakeOcOpsAppStore 是 OcOpsResolverFromStore 的最小假 store：
// 按 returnErr 优先返回错误，否则返回预置的 app。
type fakeOcOpsAppStore struct {
	app       sqlc.App // GetApp 成功时返回的 app
	returnErr error    // 非 nil 时 GetApp 直接返回该错误（模拟 sql.ErrNoRows 等）
}

func (f *fakeOcOpsAppStore) GetApp(_ context.Context, _ string) (sqlc.App, error) {
	if f.returnErr != nil {
		return sqlc.App{}, f.returnErr
	}
	return f.app, nil
}

// TestOcOpsResolverFromStoreNotFound 验证 app 不存在（sql.ErrNoRows）时 Resolve 返回 ErrNotFound。
func TestOcOpsResolverFromStoreNotFound(t *testing.T) {
	// app 不存在：store 返回 sql.ErrNoRows，resolver 应翻译为 ErrNotFound
	store := &fakeOcOpsAppStore{returnErr: sql.ErrNoRows}
	r := NewOcOpsResolverFromStore(store, nil, "http://app-%s-ocops.oc-apps.svc:8080")

	_, err := r.Resolve(context.Background(), "app-1")
	require.ErrorIs(t, err, ErrNotFound)
}

// TestOcOpsResolverFromStoreSupported 验证非 -dev 镜像解析为 Supported=true，
// 且 BaseURL 按模板以 appID 拼装、归属信息正确透传。
func TestOcOpsResolverFromStoreSupported(t *testing.T) {
	// 正常镜像（无 -dev 后缀）：Supported=true，坐标按模板拼装
	store := &fakeOcOpsAppStore{app: sqlc.App{
		OrgID:           "org-1",
		OwnerUserID:     "user-1",
		RuntimeImageRef: "registry/hermes:v2026.5.16",
	}}
	r := NewOcOpsResolverFromStore(store, nil, "http://app-%s-ocops.oc-apps.svc:8080")

	loc, err := r.Resolve(context.Background(), "app-1")
	require.NoError(t, err)
	assert.Equal(t, "org-1", loc.OrgID)
	assert.Equal(t, "user-1", loc.OwnerUserID)
	assert.True(t, loc.Supported)
	assert.Equal(t, "http://app-app-1-ocops.oc-apps.svc:8080", loc.Endpoint.BaseURL)
	// cipher 为 nil，Token 应为空
	assert.Empty(t, loc.Endpoint.Token)
}

// TestOcOpsResolverFromStoreUnsupported 验证 -dev stub 镜像解析为 Supported=false。
func TestOcOpsResolverFromStoreUnsupported(t *testing.T) {
	// dev stub 镜像（-dev 后缀）：不含真实 hermes，Supported 应为 false
	store := &fakeOcOpsAppStore{app: sqlc.App{
		OrgID:           "org-1",
		OwnerUserID:     "user-1",
		RuntimeImageRef: "registry/hermes:v2026.5.16-dev",
	}}
	r := NewOcOpsResolverFromStore(store, nil, "http://app-%s-ocops.oc-apps.svc:8080")

	loc, err := r.Resolve(context.Background(), "app-2")
	require.NoError(t, err)
	assert.False(t, loc.Supported)
}

// TestOcOpsResolverInjectsToken 验证 Resolve 解密 control token 填入 Endpoint.Token。
// 覆盖场景：cipher 与有效密文均存在时，Token 应解密为原始明文；
// BaseURL 按模板拼装正确；非 -dev 镜像 Supported=true。
func TestOcOpsResolverInjectsToken(t *testing.T) {
	// 构造 cipher 并加密明文 token
	cipher, err := auth.NewCipher(make([]byte, 32))
	require.NoError(t, err)
	ct, err := cipher.Encrypt([]byte("control-tok"))
	require.NoError(t, err)

	// store 返回含密文 token 的 app
	store := &fakeOcOpsAppStore{app: sqlc.App{
		ID:                     "a1",
		OrgID:                  "o1",
		OwnerUserID:            "u1",
		RuntimeTokenCiphertext: null.StringFrom(ct), // 有效密文
		RuntimeImageRef:        "registry/hermes:v1", // 非 -dev，Supported=true
	}}
	r := NewOcOpsResolverFromStore(store, cipher, "http://app-%s-ocops.oc-apps.svc:8080")

	loc, err := r.Resolve(context.Background(), "a1")
	require.NoError(t, err)
	// Token 应解密为原始明文
	assert.Equal(t, "control-tok", loc.Endpoint.Token)
	// BaseURL 按模板以 appID 拼装
	assert.Equal(t, "http://app-a1-ocops.oc-apps.svc:8080", loc.Endpoint.BaseURL)
	// 非 -dev 镜像应标记为支持
	assert.True(t, loc.Supported)
}
