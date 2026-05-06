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
	key   newapi.APIKey
	keys  map[int64]newapi.APIKey
	err   error
	calls int
}

func (f *fakeUsageProvider) GetAPIKey(_ context.Context, id int64) (newapi.APIKey, error) {
	f.calls++
	if f.err != nil {
		return newapi.APIKey{}, f.err
	}
	if f.keys != nil {
		if k, ok := f.keys[id]; ok {
			return k, nil
		}
	}
	return f.key, nil
}

type fakeAppLister struct {
	apps map[string][]AppResult
}

func (f *fakeAppLister) ListByOrg(_ context.Context, _ auth.Principal, orgID string, _, _ int32) ([]AppResult, error) {
	return f.apps[orgID], nil
}

type fakeOrgLister struct {
	orgs []OrganizationResult
}

func (f *fakeOrgLister) ListOrganizations(_ context.Context, _ auth.Principal, _, _ int32) ([]OrganizationResult, error) {
	return f.orgs, nil
}

func TestUsageServiceCachesAPIKey(t *testing.T) {
	provider := &fakeUsageProvider{key: newapi.APIKey{ID: 7, RemainQuota: 50, Status: 1}}
	svc := NewUsageService(provider)
	for i := 0; i < 3; i++ {
		if _, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app-1", "owner-org", "owner-user", 7); err != nil {
			t.Fatalf("GetAppUsage iter %d: %v", i, err)
		}
	}
	if provider.calls != 1 {
		t.Fatalf("provider.calls = %d, want 1 (5s cache)", provider.calls)
	}
}

func TestUsageServiceGetPlatformAggregates(t *testing.T) {
	provider := &fakeUsageProvider{keys: map[int64]newapi.APIKey{
		11: {ID: 11, RemainQuota: 100, Status: 1},
		22: {ID: 22, RemainQuota: 200, Status: 1},
	}}
	lister := &fakeAppLister{apps: map[string][]AppResult{
		"org-a": {{ID: "app-a", OrgID: "org-a", NewapiKeyID: 11}},
		"org-b": {{ID: "app-b", OrgID: "org-b", NewapiKeyID: 22}},
	}}
	orgs := &fakeOrgLister{orgs: []OrganizationResult{{ID: "org-a"}, {ID: "org-b"}}}
	svc := NewUsageService(provider)
	svc.SetAppLister(lister)
	svc.SetOrgLister(orgs)

	view, err := svc.GetPlatformUsage(context.Background(), platformAdmin())
	if err != nil {
		t.Fatalf("GetPlatformUsage: %v", err)
	}
	if view.Scope != "platform" || view.TotalRemainQuota != 300 || len(view.Apps) != 2 {
		t.Fatalf("view = %+v", view)
	}
}

func TestUsageServiceGetPlatformForbiddenForNonAdmin(t *testing.T) {
	svc := NewUsageService(&fakeUsageProvider{})
	svc.SetAppLister(&fakeAppLister{})
	svc.SetOrgLister(&fakeOrgLister{})
	_, err := svc.GetPlatformUsage(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestUsageServiceGetOrgUsesNewapiKeyID(t *testing.T) {
	provider := &fakeUsageProvider{keys: map[int64]newapi.APIKey{
		33: {ID: 33, RemainQuota: 80, Status: 1},
	}}
	lister := &fakeAppLister{apps: map[string][]AppResult{
		"org-x": {{ID: "app-x", OrgID: "org-x", NewapiKeyID: 33}},
	}}
	svc := NewUsageService(provider)
	svc.SetAppLister(lister)
	view, err := svc.GetOrgUsage(context.Background(), platformAdmin(), "org-x")
	if err != nil {
		t.Fatalf("GetOrgUsage: %v", err)
	}
	if view.TotalRemainQuota != 80 || len(view.Apps) != 1 || view.Apps[0].RemainQuota != 80 {
		t.Fatalf("view = %+v", view)
	}
}
