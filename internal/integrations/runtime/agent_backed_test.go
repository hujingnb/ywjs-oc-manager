package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/runtime/imagesync"
)

func TestAgentBackedAdapterEnsureImageDelegatesToImageSyncer(t *testing.T) {
	syncer := &fakeImageSyncer{result: imagesync.SyncResult{Image: "openclaw:dev", NodeID: "node-1", Transferred: true}}
	adapter := NewAgentBackedAdapter(nil, syncer)

	got, err := adapter.EnsureImage(context.Background(), "node-1", "openclaw:dev")
	if err != nil {
		t.Fatalf("EnsureImage() error = %v", err)
	}
	if !got.Transferred {
		t.Fatalf("expected Transferred=true, got %+v", got)
	}
	if syncer.lastImage != "openclaw:dev" || syncer.lastNode != "node-1" {
		t.Fatalf("syncer last = %s/%s", syncer.lastNode, syncer.lastImage)
	}
}

func TestAgentBackedAdapterEnsureImageReturnsUnimplementedWithoutSyncer(t *testing.T) {
	adapter := NewAgentBackedAdapter(nil, nil)
	if _, err := adapter.EnsureImage(context.Background(), "node-1", "openclaw:dev"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("EnsureImage() error = %v, want ErrUnimplemented", err)
	}
}

func TestAgentBackedAdapterContainerOpsReturnUnimplemented(t *testing.T) {
	adapter := NewAgentBackedAdapter(nil, nil)
	if _, err := adapter.CreateContainer(context.Background(), "n1", ContainerSpec{}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("CreateContainer() error = %v", err)
	}
	if err := adapter.StartContainer(context.Background(), "n1", "c1"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("StartContainer() error = %v", err)
	}
	if err := adapter.StopContainer(context.Background(), "n1", "c1"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("StopContainer() error = %v", err)
	}
	if err := adapter.RestartContainer(context.Background(), "n1", "c1"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("RestartContainer() error = %v", err)
	}
	if err := adapter.RemoveContainer(context.Background(), "n1", "c1"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("RemoveContainer() error = %v", err)
	}
	if _, err := adapter.InspectContainer(context.Background(), "n1", "c1"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("InspectContainer() error = %v", err)
	}
}

func TestAgentBackedAdapterFileOpsRouteThroughAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/files/list":
			_, _ = w.Write([]byte(`{"path":"/data","entries":[]}`))
		case "/v1/files/get":
			_, _ = w.Write([]byte("payload"))
		case "/v1/files/put":
			body, _ := io.ReadAll(r.Body)
			if string(body) != "hello" {
				t.Errorf("upload body = %q", string(body))
			}
			w.WriteHeader(http.StatusNoContent)
		case "/v1/files/delete":
			w.WriteHeader(http.StatusNoContent)
		case "/v1/files/archive":
			_, _ = w.Write([]byte("tar-bytes"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	resolver := fakeResolverFn(func(_ context.Context, nodeID string) (*agent.AgentFileClient, error) {
		if nodeID != "node-1" {
			t.Fatalf("nodeID = %q", nodeID)
		}
		return agent.NewFileClient(server.URL, ""), nil
	})
	adapter := NewAgentBackedAdapter(resolver, nil)

	if _, err := adapter.ListFiles(context.Background(), "node-1", "/data"); err != nil {
		t.Fatalf("ListFiles() error = %v", err)
	}
	if err := adapter.UploadFile(context.Background(), "node-1", "/data/x.txt", strings.NewReader("hello")); err != nil {
		t.Fatalf("UploadFile() error = %v", err)
	}
	stream, err := adapter.DownloadFile(context.Background(), "node-1", "/data/x.txt")
	if err != nil {
		t.Fatalf("DownloadFile() error = %v", err)
	}
	if _, err := io.Copy(io.Discard, stream); err != nil {
		t.Fatalf("read download: %v", err)
	}
	stream.Close()
	if err := adapter.DeletePath(context.Background(), "node-1", "/data/x.txt"); err != nil {
		t.Fatalf("DeletePath() error = %v", err)
	}
	tar, err := adapter.ArchiveDirectory(context.Background(), "node-1", "/data")
	if err != nil {
		t.Fatalf("ArchiveDirectory() error = %v", err)
	}
	tarBody, _ := io.ReadAll(tar)
	tar.Close()
	if string(tarBody) != "tar-bytes" {
		t.Fatalf("archive body = %q", string(tarBody))
	}
}

func TestAgentBackedAdapterFileOpsRequireResolver(t *testing.T) {
	adapter := NewAgentBackedAdapter(nil, nil)
	if _, err := adapter.ListFiles(context.Background(), "node-1", "/data"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("ListFiles() error = %v", err)
	}
}

type fakeImageSyncer struct {
	result    imagesync.SyncResult
	err       error
	lastImage string
	lastNode  string
}

func (f *fakeImageSyncer) SyncOpenClawImage(_ context.Context, nodeID, image string) (imagesync.SyncResult, error) {
	f.lastNode = nodeID
	f.lastImage = image
	return f.result, f.err
}

type fakeResolverFn func(ctx context.Context, nodeID string) (*agent.AgentFileClient, error)

func (f fakeResolverFn) FileClient(ctx context.Context, nodeID string) (*agent.AgentFileClient, error) {
	return f(ctx, nodeID)
}
