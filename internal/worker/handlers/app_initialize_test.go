package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
)

const (
	testAppID = "00000000-0000-0000-0000-000000000a01"
	testOrgID = "00000000-0000-0000-0000-000000000b01"
	testUsrID = "00000000-0000-0000-0000-000000000c01"
)

func TestAppInitializeHandlesHappyPath(t *testing.T) {
	store := newAppInitStub(t)
	images := &fakeImages{}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 99, Key: "sk-test"}}

	handler := NewAppInitializeHandler(store, images, client, AppInitializeConfig{RuntimeImage: "openclaw:dev"})
	if err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !store.apiKeySet || !store.statusSet {
		t.Fatalf("expected api key and status to be persisted: %+v", store)
	}
	if images.lastImage != "openclaw:dev" || images.lastNode != "node-1" {
		t.Fatalf("image dispatch = %s/%s", images.lastNode, images.lastImage)
	}
}

func TestAppInitializeIsIdempotentForBindingWaiting(t *testing.T) {
	store := newAppInitStub(t)
	store.app.Status = domain.AppStatusBindingWaiting
	store.app.ApiKeyStatus = domain.APIKeyStatusActive
	images := &fakeImages{}
	client := &fakeNewAPI{}

	handler := NewAppInitializeHandler(store, images, client, AppInitializeConfig{})
	if err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if client.calls != 0 {
		t.Fatalf("new-api should be skipped, calls = %d", client.calls)
	}
	if images.lastImage != "" {
		t.Fatalf("image distribution should be skipped, got %s", images.lastImage)
	}
	if store.statusSet {
		t.Fatalf("status update should be skipped")
	}
}

func TestAppInitializeSkipsAPIKeyWhenAlreadyActive(t *testing.T) {
	store := newAppInitStub(t)
	store.app.ApiKeyStatus = domain.APIKeyStatusActive
	client := &fakeNewAPI{}

	handler := NewAppInitializeHandler(store, &fakeImages{}, client, AppInitializeConfig{})
	if err := handler.Handle(context.Background(), buildJob(t, testAppID, "")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if client.calls != 0 {
		t.Fatalf("new-api should not be called when key already active")
	}
	if !store.statusSet {
		t.Fatalf("status should still be promoted to binding_waiting")
	}
}

func TestAppInitializePropagatesNewAPIError(t *testing.T) {
	store := newAppInitStub(t)
	client := &fakeNewAPI{err: newapi.ErrUpstream}

	handler := NewAppInitializeHandler(store, &fakeImages{}, client, AppInitializeConfig{})
	err := handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	if !errors.Is(err, newapi.ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream", err)
	}
	if store.statusSet {
		t.Fatalf("status should not be updated on failure")
	}
}

func TestAppInitializeRejectsInvalidPayload(t *testing.T) {
	store := newAppInitStub(t)
	handler := NewAppInitializeHandler(store, &fakeImages{}, &fakeNewAPI{}, AppInitializeConfig{})

	job := sqlc.Job{Type: domain.JobTypeAppInitialize, PayloadJson: []byte(`{"runtime_node":"node-1"}`)}
	if err := handler.Handle(context.Background(), job); err == nil {
		t.Fatalf("expected error for missing app_id")
	}
}

func buildJob(t *testing.T, appID, nodeID string) sqlc.Job {
	t.Helper()
	payload := []byte(`{"app_id":"` + appID + `","runtime_node":"` + nodeID + `"}`)
	return sqlc.Job{Type: domain.JobTypeAppInitialize, PayloadJson: payload}
}

type appInitStub struct {
	t          *testing.T
	app        sqlc.App
	org        sqlc.Organization
	user       sqlc.User
	apiKeySet  bool
	statusSet  bool
}

func newAppInitStub(t *testing.T) *appInitStub {
	return &appInitStub{
		t: t,
		app: sqlc.App{
			ID:           mustUUIDForTest(t, testAppID),
			OrgID:        mustUUIDForTest(t, testOrgID),
			OwnerUserID:  mustUUIDForTest(t, testUsrID),
			Name:         "alice-bot",
			Status:       domain.AppStatusDraft,
			PersonaMode:  domain.PersonaModeOrgInherited,
			ApiKeyStatus: domain.APIKeyStatusPending,
			AppPrompt:    pgtype.Text{String: "{org_name} 应用 {app_name}", Valid: true},
		},
		org:  sqlc.Organization{Name: "测试组织", Status: domain.StatusActive},
		user: sqlc.User{DisplayName: "Alice"},
	}
}

func (s *appInitStub) GetApp(_ context.Context, _ pgtype.UUID) (sqlc.App, error) { return s.app, nil }
func (s *appInitStub) GetOrganization(_ context.Context, _ pgtype.UUID) (sqlc.Organization, error) {
	return s.org, nil
}
func (s *appInitStub) GetUser(_ context.Context, _ pgtype.UUID) (sqlc.User, error) { return s.user, nil }

func (s *appInitStub) SetAppNewAPIKey(_ context.Context, arg sqlc.SetAppNewAPIKeyParams) (sqlc.App, error) {
	s.apiKeySet = true
	s.app.ApiKeyStatus = arg.ApiKeyStatus
	s.app.NewapiKeyID = arg.NewapiKeyID
	s.app.NewapiKeyCiphertext = arg.NewapiKeyCiphertext
	return s.app, nil
}

func (s *appInitStub) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error) {
	s.statusSet = true
	s.app.Status = arg.Status
	return s.app, nil
}

type fakeImages struct {
	lastImage string
	lastNode  string
}

func (f *fakeImages) EnsureRuntimeImage(_ context.Context, nodeID, image string) (any, error) {
	f.lastNode = nodeID
	f.lastImage = image
	return nil, nil
}

type fakeNewAPI struct {
	result newapi.APIKey
	err    error
	calls  int
}

func (f *fakeNewAPI) CreateAPIKey(_ context.Context, _ newapi.CreateAPIKeyInput) (newapi.APIKey, error) {
	f.calls++
	if f.err != nil {
		return newapi.APIKey{}, f.err
	}
	return f.result, nil
}

func mustUUIDForTest(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return id
}
