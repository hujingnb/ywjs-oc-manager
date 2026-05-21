package service

import (
	"context"
	"fmt"
	"strings"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
)

// ModelCatalog 抽象 new-api 实时模型列表，便于 service 单测注入。
type ModelCatalog interface {
	ListModels(ctx context.Context) ([]newapi.Model, error)
}

// ModelResult 是 manager API 返回给前端的模型视图。
type ModelResult struct {
	// ID 是后续组织 allowlist 和实例模型字段使用的模型标识。
	ID string `json:"id"`
	// Name 是前端展示名称；new-api 缺省时与 ID 一致。
	Name string `json:"name"`
}

// ModelCatalogService 负责读取和校验 new-api 实时模型列表。
type ModelCatalogService struct {
	// catalog 是唯一实时来源；manager 不在本地缓存或持久化全量模型目录。
	catalog ModelCatalog
}

// NewModelCatalogService 创建模型目录服务。
func NewModelCatalogService(catalog ModelCatalog) *ModelCatalogService {
	return &ModelCatalogService{catalog: catalog}
}

// List 返回当前 new-api 可用模型，仅平台管理员可读。
func (s *ModelCatalogService) List(ctx context.Context, principal auth.Principal) ([]ModelResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return nil, ErrForbidden
	}
	return s.list(ctx)
}

// ValidateModelIDs 校验组织模型 allowlist 非空、去重并且全部存在于实时模型列表。
func (s *ModelCatalogService) ValidateModelIDs(ctx context.Context, input []string) ([]string, error) {
	models, err := s.list(ctx)
	if err != nil {
		return nil, err
	}
	available := make(map[string]struct{}, len(models))
	for _, model := range models {
		available[model.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(input))
	out := make([]string, 0, len(input))
	for _, raw := range input {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := available[id]; !ok {
			return nil, fmt.Errorf("%w: 模型 %s 不存在", ErrMemberCreateInvalid, id)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: 至少选择一个可用模型", ErrMemberCreateInvalid)
	}
	return out, nil
}

// HasModelInCatalog 判断单个模型是否存在于 new-api 实时模型列表。
// 供助手版本 service 校验主模型与路由模型；查询失败时保守返回 false。
func (s *ModelCatalogService) HasModelInCatalog(ctx context.Context, id string) bool {
	models, err := s.list(ctx)
	if err != nil {
		return false
	}
	for _, m := range models {
		if m.ID == id {
			return true
		}
	}
	return false
}

func (s *ModelCatalogService) list(ctx context.Context) ([]ModelResult, error) {
	if s.catalog == nil {
		return nil, fmt.Errorf("模型目录未配置")
	}
	models, err := s.catalog.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询模型列表失败: %w", err)
	}
	out := make([]ModelResult, 0, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = id
		}
		out = append(out, ModelResult{ID: id, Name: name})
	}
	return out, nil
}
