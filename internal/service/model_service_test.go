package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
)

type fakeModelCatalog struct {
	models []newapi.Model
	err    error
}

func (f fakeModelCatalog) ListModels(context.Context) ([]newapi.Model, error) {
	return f.models, f.err
}

// TestModelCatalogServiceListRequiresPlatformAdmin 验证模型列表管理接口只允许平台管理员读取。
func TestModelCatalogServiceListRequiresPlatformAdmin(t *testing.T) {
	t.Parallel()
	svc := NewModelCatalogService(fakeModelCatalog{models: []newapi.Model{{ID: "qwen", Name: "qwen"}}})
	_, err := svc.List(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin})
	require.ErrorIs(t, err, ErrForbidden)
}

// TestModelCatalogServiceListReturnsModels 验证平台管理员可以读取实时模型列表。
func TestModelCatalogServiceListReturnsModels(t *testing.T) {
	t.Parallel()
	svc := NewModelCatalogService(fakeModelCatalog{models: []newapi.Model{{ID: "qwen", Name: "qwen"}}})
	got, err := svc.List(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin})
	require.NoError(t, err)
	assert.Equal(t, []ModelResult{{ID: "qwen", Name: "qwen"}}, got)
}

// TestModelCatalogServiceListSurfacesUpstreamError 验证 new-api 上游查询失败时 List 冒泡错误。
func TestModelCatalogServiceListSurfacesUpstreamError(t *testing.T) {
	t.Parallel()
	svc := NewModelCatalogService(fakeModelCatalog{err: errors.New("upstream down")})
	_, err := svc.List(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin})
	require.ErrorContains(t, err, "查询模型列表失败")
}

// TestModelCatalogServiceHasModelInCatalogSurfacesUpstreamError 验证单模型校验保留 new-api 目录故障供调用方分类。
func TestModelCatalogServiceHasModelInCatalogSurfacesUpstreamError(t *testing.T) {
	t.Parallel()
	upstreamErr := errors.New("upstream down")
	svc := NewModelCatalogService(fakeModelCatalog{err: upstreamErr})

	exists, err := svc.HasModelInCatalog(context.Background(), "qwen")

	assert.False(t, exists)
	require.ErrorIs(t, err, upstreamErr)
}
