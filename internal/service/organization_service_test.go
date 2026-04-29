package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

func TestOrganizationServiceCreateRequiresPlatformAdmin(t *testing.T) {
	svc := NewOrganizationService(&organizationStoreStub{})

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin}, OrganizationInput{Name: "测试组织"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateOrganization() error = %v, want ErrForbidden", err)
	}
}

func TestOrganizationServiceCreateOrganization(t *testing.T) {
	store := &organizationStoreStub{}
	svc := NewOrganizationService(store)
	threshold := int32(20)

	result, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:                   "测试组织",
		ContactName:            "张三",
		CreditWarningThreshold: &threshold,
	})
	if err != nil {
		t.Fatalf("CreateOrganization() error = %v", err)
	}
	if result.Name != "测试组织" || result.CreditWarningThreshold == nil || *result.CreditWarningThreshold != 20 {
		t.Fatalf("organization = %+v, want created values", result)
	}
	if store.created.Name != "测试组织" {
		t.Fatalf("created params = %+v, want name", store.created)
	}
}

func TestOrganizationServiceGetRestrictsOrgScope(t *testing.T) {
	store := &organizationStoreStub{org: sqlc.Organization{ID: mustUUID(t, "00000000-0000-0000-0000-000000000101"), Name: "测试组织", Status: domain.StatusActive}}
	svc := NewOrganizationService(store)

	_, err := svc.GetOrganization(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "00000000-0000-0000-0000-000000000999"}, "00000000-0000-0000-0000-000000000101")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("GetOrganization() error = %v, want ErrForbidden", err)
	}
}

func TestOrganizationServiceSetStatus(t *testing.T) {
	store := &organizationStoreStub{org: sqlc.Organization{ID: mustUUID(t, "00000000-0000-0000-0000-000000000101"), Name: "测试组织", Status: domain.StatusActive}}
	svc := NewOrganizationService(store)

	result, err := svc.SetOrganizationStatus(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "00000000-0000-0000-0000-000000000101", domain.StatusDisabled)
	if err != nil {
		t.Fatalf("SetOrganizationStatus() error = %v", err)
	}
	if result.Status != domain.StatusDisabled {
		t.Fatalf("status = %q, want disabled", result.Status)
	}
}

type organizationStoreStub struct {
	org     sqlc.Organization
	created sqlc.CreateOrganizationParams
}

func (s *organizationStoreStub) CreateOrganization(_ context.Context, arg sqlc.CreateOrganizationParams) (sqlc.Organization, error) {
	s.created = arg
	id, _ := parseUUID("00000000-0000-0000-0000-000000000101")
	return sqlc.Organization{
		ID:                     id,
		Name:                   arg.Name,
		Status:                 arg.Status,
		ContactName:            arg.ContactName,
		CreditWarningThreshold: arg.CreditWarningThreshold,
	}, nil
}

func (s *organizationStoreStub) GetOrganization(_ context.Context, id pgtype.UUID) (sqlc.Organization, error) {
	if !s.org.ID.Valid || s.org.ID != id {
		return sqlc.Organization{}, pgx.ErrNoRows
	}
	return s.org, nil
}

func (s *organizationStoreStub) ListOrganizations(_ context.Context, _ sqlc.ListOrganizationsParams) ([]sqlc.Organization, error) {
	return []sqlc.Organization{s.org}, nil
}

func (s *organizationStoreStub) UpdateOrganizationProfile(_ context.Context, arg sqlc.UpdateOrganizationProfileParams) (sqlc.Organization, error) {
	s.org.Name = arg.Name
	s.org.ContactName = arg.ContactName
	return s.org, nil
}

func (s *organizationStoreStub) SetOrganizationStatus(_ context.Context, arg sqlc.SetOrganizationStatusParams) (sqlc.Organization, error) {
	s.org.Status = arg.Status
	return s.org, nil
}
