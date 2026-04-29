package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// OrganizationStore 抽象组织管理所需的数据访问能力。
type OrganizationStore interface {
	CreateOrganization(ctx context.Context, arg sqlc.CreateOrganizationParams) (sqlc.Organization, error)
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	ListOrganizations(ctx context.Context, arg sqlc.ListOrganizationsParams) ([]sqlc.Organization, error)
	UpdateOrganizationProfile(ctx context.Context, arg sqlc.UpdateOrganizationProfileParams) (sqlc.Organization, error)
	SetOrganizationStatus(ctx context.Context, arg sqlc.SetOrganizationStatusParams) (sqlc.Organization, error)
}

type OrganizationService struct {
	store OrganizationStore
}

func NewOrganizationService(store OrganizationStore) *OrganizationService {
	return &OrganizationService{store: store}
}

type OrganizationInput struct {
	Name                   string
	ContactName            string
	ContactPhone           string
	Remark                 string
	NewAPIUserID           string
	CreditWarningThreshold *int32
}

type OrganizationResult struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	Status                 string `json:"status"`
	ContactName            string `json:"contact_name,omitempty"`
	ContactPhone           string `json:"contact_phone,omitempty"`
	Remark                 string `json:"remark,omitempty"`
	NewAPIUserID           string `json:"newapi_user_id,omitempty"`
	CreditWarningThreshold *int32 `json:"credit_warning_threshold,omitempty"`
}

// CreateOrganization 创建组织；第一版仅平台管理员可执行。
func (s *OrganizationService) CreateOrganization(ctx context.Context, principal auth.Principal, input OrganizationInput) (OrganizationResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return OrganizationResult{}, ErrForbidden
	}
	org, err := s.store.CreateOrganization(ctx, sqlc.CreateOrganizationParams{
		Name:                   input.Name,
		Status:                 domain.StatusActive,
		ContactName:            textValue(input.ContactName),
		ContactPhone:           textValue(input.ContactPhone),
		Remark:                 textValue(input.Remark),
		NewapiUserID:           textValue(input.NewAPIUserID),
		CreditWarningThreshold: int4Ptr(input.CreditWarningThreshold),
	})
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("创建组织失败: %w", err)
	}
	return toOrganizationResult(org), nil
}

// ListOrganizations 列出未删除组织；第一版仅平台管理员可访问全量组织。
func (s *OrganizationService) ListOrganizations(ctx context.Context, principal auth.Principal, limit, offset int32) ([]OrganizationResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return nil, ErrForbidden
	}
	orgs, err := s.store.ListOrganizations(ctx, sqlc.ListOrganizationsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("查询组织列表失败: %w", err)
	}
	return toOrganizationResults(orgs), nil
}

// GetOrganization 根据角色限制组织访问范围。
func (s *OrganizationService) GetOrganization(ctx context.Context, principal auth.Principal, orgID string) (OrganizationResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin && principal.OrgID != orgID {
		return OrganizationResult{}, ErrForbidden
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return OrganizationResult{}, ErrNotFound
	}
	org, err := s.store.GetOrganization(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationResult{}, ErrNotFound
	}
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("查询组织失败: %w", err)
	}
	return toOrganizationResult(org), nil
}

// UpdateOrganization 更新组织基础资料；生命周期状态必须走 enable/disable。
func (s *OrganizationService) UpdateOrganization(ctx context.Context, principal auth.Principal, orgID string, input OrganizationInput) (OrganizationResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return OrganizationResult{}, ErrForbidden
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return OrganizationResult{}, ErrNotFound
	}
	org, err := s.store.UpdateOrganizationProfile(ctx, sqlc.UpdateOrganizationProfileParams{
		ID:                     id,
		Name:                   input.Name,
		ContactName:            textValue(input.ContactName),
		ContactPhone:           textValue(input.ContactPhone),
		Remark:                 textValue(input.Remark),
		CreditWarningThreshold: int4Ptr(input.CreditWarningThreshold),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationResult{}, ErrNotFound
	}
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("更新组织失败: %w", err)
	}
	return toOrganizationResult(org), nil
}

// SetOrganizationStatus 启用或禁用组织；软删除后续由删除流程单独处理。
func (s *OrganizationService) SetOrganizationStatus(ctx context.Context, principal auth.Principal, orgID, status string) (OrganizationResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return OrganizationResult{}, ErrForbidden
	}
	if status != domain.StatusActive && status != domain.StatusDisabled {
		return OrganizationResult{}, fmt.Errorf("非法组织状态: %s", status)
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return OrganizationResult{}, ErrNotFound
	}
	org, err := s.store.SetOrganizationStatus(ctx, sqlc.SetOrganizationStatusParams{ID: id, Status: status})
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationResult{}, ErrNotFound
	}
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("更新组织状态失败: %w", err)
	}
	return toOrganizationResult(org), nil
}

func toOrganizationResults(orgs []sqlc.Organization) []OrganizationResult {
	results := make([]OrganizationResult, 0, len(orgs))
	for _, org := range orgs {
		results = append(results, toOrganizationResult(org))
	}
	return results
}

func toOrganizationResult(org sqlc.Organization) OrganizationResult {
	return OrganizationResult{
		ID:                     uuidToString(org.ID),
		Name:                   org.Name,
		Status:                 org.Status,
		ContactName:            textString(org.ContactName),
		ContactPhone:           textString(org.ContactPhone),
		Remark:                 textString(org.Remark),
		NewAPIUserID:           textString(org.NewapiUserID),
		CreditWarningThreshold: int4Pointer(org.CreditWarningThreshold),
	}
}

func textValue(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

func textString(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func int4Ptr(value *int32) pgtype.Int4 {
	if value == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *value, Valid: true}
}

func int4Pointer(value pgtype.Int4) *int32 {
	if !value.Valid {
		return nil
	}
	result := value.Int32
	return &result
}
