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
	adapter := &fakeWorkspaceAdapter{
		workspaceListing: runtime.WorkspaceListing{
			Path: "/logs",
			Entries: []runtime.WorkspaceEntry{
				{Name: "alice.log", Type: "file", Size: 12, ModifiedAt: "2026-05-03T00:00:00Z"},
			},
		},
	}
	svc := NewWorkspaceService(store, adapter, "/data")

	listing, err := svc.List(context.Background(), platformAdmin(), testWorkAppID, "logs")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "alice.log" {
		t.Fatalf("listing = %+v", listing)
	}
	// Sprint 2 改用 scope-aware 端点：service 直接传 appID + relative，
	// 不再拼 /data/org/<id>/app/<id> 路径，校验 adapter 拿到的 appID/relPath。
	if adapter.lastAppID != testWorkAppID || adapter.lastRelPath != "logs" {
		t.Fatalf("adapter 收到 appID=%q relPath=%q", adapter.lastAppID, adapter.lastRelPath)
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

	var buf strings.Builder
	err := svc.Archive(context.Background(), platformAdmin(), testWorkAppID, "", &buf)
	if !errors.Is(err, ErrWorkspaceMissing) {
		t.Fatalf("error = %v, want ErrWorkspaceMissing", err)
	}
}

func TestWorkspaceServiceArchiveStreamsZip(t *testing.T) {
	store := newWorkspaceStub(t)
	adapter := &fakeWorkspaceAdapter{archiveBytes: []byte("zip-content")}
	svc := NewWorkspaceService(store, adapter, "/data")

	var buf strings.Builder
	if err := svc.Archive(context.Background(), platformAdmin(), testWorkAppID, "sub", &buf); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if buf.String() != "zip-content" {
		t.Fatalf("buf = %q", buf.String())
	}
	if adapter.lastAppID != testWorkAppID || adapter.lastRelPath != "sub" {
		t.Fatalf("adapter 收到 appID=%q relPath=%q", adapter.lastAppID, adapter.lastRelPath)
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

func TestWorkspaceServiceRejectsUnsafePaths(t *testing.T) {
	store := newWorkspaceStub(t)
	svc := NewWorkspaceService(store, &fakeWorkspaceAdapter{}, "/data")

	for _, target := range []string{"..", "../secret.txt", "/abs.txt", ""} {
		if _, err := svc.Download(context.Background(), platformAdmin(), testWorkAppID, target); !errors.Is(err, ErrWorkspaceBadPath) {
			t.Fatalf("Download(%q) error = %v, want ErrWorkspaceBadPath", target, err)
		}
	}

	if _, err := svc.List(context.Background(), platformAdmin(), testWorkAppID, "/abs"); !errors.Is(err, ErrWorkspaceBadPath) {
		t.Fatalf("List absolute error = %v, want ErrWorkspaceBadPath", err)
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
	listing          runtime.FileListing
	workspaceListing runtime.WorkspaceListing
	stream           io.ReadCloser
	archiveBytes     []byte
	lastPath         string
	lastAppID        string
	lastRelPath      string
}

func (a *fakeWorkspaceAdapter) EnsureImage(_ context.Context, _, _ string) (imagesync.SyncResult, error) {
	return imagesync.SyncResult{}, nil
}
func (a *fakeWorkspaceAdapter) CreateContainer(_ context.Context, _ string, _ runtime.ContainerSpec) (runtime.ContainerInfo, error) {
	return runtime.ContainerInfo{}, runtime.ErrUnimplemented
}
func (a *fakeWorkspaceAdapter) StartContainer(_ context.Context, _, _ string) error {
	return runtime.ErrUnimplemented
}
func (a *fakeWorkspaceAdapter) StopContainer(_ context.Context, _, _ string) error {
	return runtime.ErrUnimplemented
}
func (a *fakeWorkspaceAdapter) RestartContainer(_ context.Context, _, _ string) error {
	return runtime.ErrUnimplemented
}
func (a *fakeWorkspaceAdapter) RemoveContainer(_ context.Context, _, _ string) error {
	return runtime.ErrUnimplemented
}
func (a *fakeWorkspaceAdapter) InspectContainer(_ context.Context, _, _ string) (runtime.ContainerInfo, error) {
	return runtime.ContainerInfo{}, runtime.ErrUnimplemented
}
func (a *fakeWorkspaceAdapter) ContainerStats(_ context.Context, _, _ string) (runtime.ContainerStats, error) {
	return runtime.ContainerStats{}, runtime.ErrUnimplemented
}
func (a *fakeWorkspaceAdapter) ContainerExec(_ context.Context, _, _ string, _ []string) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, runtime.ErrUnimplemented
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

func (a *fakeWorkspaceAdapter) ListWorkspace(_ context.Context, _, appID, relPath string) (runtime.WorkspaceListing, error) {
	a.lastAppID = appID
	a.lastRelPath = relPath
	return a.workspaceListing, nil
}

func (a *fakeWorkspaceAdapter) DownloadWorkspaceFile(_ context.Context, _, appID, relPath string) (io.ReadCloser, error) {
	a.lastAppID = appID
	a.lastRelPath = relPath
	return a.stream, nil
}

func (a *fakeWorkspaceAdapter) StreamWorkspaceArchive(_ context.Context, _, appID, relPath string, w io.Writer) error {
	a.lastAppID = appID
	a.lastRelPath = relPath
	if a.archiveBytes != nil {
		_, err := w.Write(a.archiveBytes)
		return err
	}
	return nil
}

func (a *fakeWorkspaceAdapter) ArchiveApp(_ context.Context, _, _ string) error {
	return nil
}
