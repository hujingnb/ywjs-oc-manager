package service

import (
	"context"
	"errors"
	"testing"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
)

func TestUsageServiceForbidsCrossOrg(t *testing.T) {
	svc := NewUsageService(&fakeUsageProvider{})
	_, err := svc.GetAppUsage(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "other"}, "app-1", "owner-org", "owner-user", 1)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

func TestUsageServiceReturnsSnapshot(t *testing.T) {
	provider := &fakeUsageProvider{key: newapi.APIKey{ID: 42, RemainQuota: 100, Status: 1}}
	svc := NewUsageService(provider)
	got, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app-1", "owner-org", "owner-user", 42)
	if err != nil {
		t.Fatalf("GetAppUsage() error = %v", err)
	}
	if got.RemainQuota != 100 || got.Status != 1 {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestUsageServiceMapsNewAPIErrors(t *testing.T) {
	svc := NewUsageService(&fakeUsageProvider{err: newapi.ErrNotFound})
	if _, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app-1", "owner-org", "owner-user", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}

	svc = NewUsageService(&fakeUsageProvider{err: newapi.ErrUnauthorized})
	if _, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app-1", "owner-org", "owner-user", 1); !errors.Is(err, ErrUsageUnavailable) {
		t.Fatalf("error = %v, want ErrUsageUnavailable", err)
	}
}

func TestUsageServiceReturnsZeroForUnboundKey(t *testing.T) {
	svc := NewUsageService(&fakeUsageProvider{})
	got, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app-1", "owner-org", "owner-user", 0)
	if err != nil {
		t.Fatalf("GetAppUsage() error = %v", err)
	}
	if got.NewapiKeyID != 0 || got.RemainQuota != 0 {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestUsageServiceMissingProvider(t *testing.T) {
	svc := NewUsageService(nil)
	if _, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app", "owner-org", "owner-user", 1); !errors.Is(err, ErrUsageUnavailable) {
		t.Fatalf("error = %v, want ErrUsageUnavailable", err)
	}
}

type fakeUsageProvider struct {
	key newapi.APIKey
	err error
}

func (f *fakeUsageProvider) GetAPIKey(_ context.Context, _ int64) (newapi.APIKey, error) {
	if f.err != nil {
		return newapi.APIKey{}, f.err
	}
	return f.key, nil
}
