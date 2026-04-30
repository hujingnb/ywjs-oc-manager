package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/runtime/imagesync"
	"oc-manager/internal/store/sqlc"
)

const (
	testWorkAppID = "00000000-0000-0000-0000-000000000f01"
	testWorkOrg   = "00000000-0000-0000-0000-000000000f02"
	testWorkOwner = "00000000-0000-0000-0000-000000000f03"
	testWorkNode  = "00000000-0000-0000-0000-000000000f04"
)

func TestWorkspaceServiceListReturnsEntries(t *testing.T) {
	store := newWorkspaceStub(t)
	adapter := &fakeWorkspaceAdapter{listing: runtime.FileListing{Path: "/data", Entries: []runtime.FileEntry{{Name: "alice.log", IsDir: false, Size: 12}}}}
	svc := NewWorkspaceService(store, adapter, "/data")

	listing, err := svc.List(context.Background(), platformAdmin(), testWorkAppID, "logs")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "alice.log" {
		t.Fatalf("listing = %+v", listing)
	}
	if !strings.HasPrefix(adapter.lastPath, "/data/org/") {
		t.Fatalf("expected path prefix in adapter, got %q", adapter.lastPath)
	}
}

func TestWorkspaceServiceListRejectsForbidden(t *testing.T) {
	store := newWorkspaceStub(t)
	svc := NewWorkspaceService(store, &fakeWorkspaceAdapter{}, "/data")

	_, err := svc.List(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testWorkOrg, UserID: "stranger"}, testWorkAppID, "logs")
	if !errors.Is(err, ErrWorkspaceForbidden) {
		t.Fatalf("error = %v, want ErrWorkspaceForbidden", err)
	}
}

func TestWorkspaceServiceArchiveFailsWithoutAdapter(t *testing.T) {
	store := newWorkspaceStub(t)
	svc := NewWorkspaceService(store, nil, "/data")

	_, err := svc.Archive(context.Background(), platformAdmin(), testWorkAppID, "")
	if !errors.Is(err, ErrWorkspaceMissing) {
		t.Fatalf("error = %v, want ErrWorkspaceMissing", err)
	}
}

func TestWorkspaceServiceDownloadDelegatesToAdapter(t *testing.T) {
	store := newWorkspaceStub(t)
	adapter := &fakeWorkspaceAdapter{stream: io.NopCloser(strings.NewReader("payload"))}
	svc := NewWorkspaceService(store, adapter, "/data")

	stream, err := svc.Download(context.Background(), platformAdmin(), testWorkAppID, "logs/x.log")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	defer stream.Close()
	body, _ := io.ReadAll(stream)
	if string(body) != "payload" {
		t.Fatalf("payload = %q", string(body))
	}
}

func TestWorkspaceServiceListMissingNodeReturnsError(t *testing.T) {
	store := newWorkspaceStub(t)
	store.app.RuntimeNodeID = pgtype.UUID{} // 不再设置 valid=true
	svc := NewWorkspaceService(store, &fakeWorkspaceAdapter{}, "/data")

	_, err := svc.List(context.Background(), platformAdmin(), testWorkAppID, "")
	if !errors.Is(err, ErrWorkspaceMissing) {
		t.Fatalf("error = %v, want ErrWorkspaceMissing", err)
	}
}

func newWorkspaceStub(t *testing.T) *workspaceStub {
	app := sqlc.App{
		ID:           mustUUID(t, testWorkAppID),
		OrgID:        mustUUID(t, testWorkOrg),
		OwnerUserID:  mustUUID(t, testWorkOwner),
		Status:       domain.AppStatusRunning,
		PersonaMode:  domain.PersonaModeOrgInherited,
		ApiKeyStatus: domain.APIKeyStatusActive,
	}
	app.RuntimeNodeID = mustUUID(t, testWorkNode)
	app.RuntimeNodeID.Valid = true
	return &workspaceStub{t: t, app: app}
}

type workspaceStub struct {
	t   *testing.T
	app sqlc.App
}

func (s *workspaceStub) GetApp(_ context.Context, id pgtype.UUID) (sqlc.App, error) {
	if id != s.app.ID {
		return sqlc.App{}, pgx.ErrNoRows
	}
	return s.app, nil
}

type fakeWorkspaceAdapter struct {
	listing  runtime.FileListing
	stream   io.ReadCloser
	lastPath string
}

func (a *fakeWorkspaceAdapter) EnsureImage(_ context.Context, _, _ string) (imagesync.SyncResult, error) {
	return imagesync.SyncResult{}, nil
}
func (a *fakeWorkspaceAdapter) CreateContainer(_ context.Context, _ string, _ runtime.ContainerSpec) (runtime.ContainerInfo, error) {
	return runtime.ContainerInfo{}, runtime.ErrUnimplemented
}
func (a *fakeWorkspaceAdapter) StartContainer(_ context.Context, _, _ string) error   { return runtime.ErrUnimplemented }
func (a *fakeWorkspaceAdapter) StopContainer(_ context.Context, _, _ string) error    { return runtime.ErrUnimplemented }
func (a *fakeWorkspaceAdapter) RestartContainer(_ context.Context, _, _ string) error { return runtime.ErrUnimplemented }
func (a *fakeWorkspaceAdapter) RemoveContainer(_ context.Context, _, _ string) error  { return runtime.ErrUnimplemented }
func (a *fakeWorkspaceAdapter) InspectContainer(_ context.Context, _, _ string) (runtime.ContainerInfo, error) {
	return runtime.ContainerInfo{}, runtime.ErrUnimplemented
}
func (a *fakeWorkspaceAdapter) ListFiles(_ context.Context, _ string, path string) (runtime.FileListing, error) {
	a.lastPath = path
	return a.listing, nil
}
func (a *fakeWorkspaceAdapter) UploadFile(_ context.Context, _ string, _ string, _ io.Reader) error {
	return runtime.ErrUnimplemented
}
func (a *fakeWorkspaceAdapter) DownloadFile(_ context.Context, _ string, _ string) (io.ReadCloser, error) {
	return a.stream, nil
}
func (a *fakeWorkspaceAdapter) ArchiveDirectory(_ context.Context, _ string, _ string) (io.ReadCloser, error) {
	return a.stream, nil
}
func (a *fakeWorkspaceAdapter) DeletePath(_ context.Context, _ string, _ string) error {
	return runtime.ErrUnimplemented
}
