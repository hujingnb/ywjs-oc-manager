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

// TestValidateModelIDsRejectsMissingModel 验证组织提交的模型必须来自实时模型列表。
func TestValidateModelIDsRejectsMissingModel(t *testing.T) {
	t.Parallel()
	svc := NewModelCatalogService(fakeModelCatalog{models: []newapi.Model{{ID: "qwen", Name: "qwen"}}})
	_, err := svc.ValidateModelIDs(context.Background(), []string{"missing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "模型 missing 不存在")
}

// TestModelCatalogServiceSurfacesUpstreamFailure 验证 new-api 不可用时阻止上层继续提交。
func TestModelCatalogServiceSurfacesUpstreamFailure(t *testing.T) {
	t.Parallel()
	svc := NewModelCatalogService(fakeModelCatalog{err: errors.New("upstream down")})
	_, err := svc.ValidateModelIDs(context.Background(), []string{"qwen"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "查询模型列表失败")
}
