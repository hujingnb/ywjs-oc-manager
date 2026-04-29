package imagesync

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSyncOpenClawImageSkipsWhenRemoteMatches(t *testing.T) {
	local := &fakeLocalImage{imageID: "sha256:same"}
	agent := &fakeAgentImage{remote: RemoteImageInfo{Exists: true, ID: "sha256:same"}}

	result, err := New(local, agent).SyncOpenClawImage(context.Background(), "node-1", "openclaw-runtime:dev")
	if err != nil {
		t.Fatalf("sync image: %v", err)
	}
	if result.Transferred {
		t.Fatalf("expected transfer to be skipped: %+v", result)
	}
	if agent.loadCalls != 0 {
		t.Fatalf("expected no load calls, got %d", agent.loadCalls)
	}
}

func TestSyncOpenClawImageLoadsWhenRemoteMissing(t *testing.T) {
	local := &fakeLocalImage{imageID: "sha256:local", archive: "image-tar"}
	agent := &fakeAgentImage{remote: RemoteImageInfo{Exists: false}, loaded: RemoteImageInfo{Exists: true, ID: "sha256:local"}}

	result, err := New(local, agent).SyncOpenClawImage(context.Background(), "node-1", "openclaw-runtime:dev")
	if err != nil {
		t.Fatalf("sync image: %v", err)
	}
	if !result.Transferred {
		t.Fatalf("expected image to be transferred: %+v", result)
	}
	if agent.loadedArchive != "image-tar" {
		t.Fatalf("unexpected loaded archive: %q", agent.loadedArchive)
	}
}

func TestSyncOpenClawImageLoadsWhenRemoteDiffers(t *testing.T) {
	local := &fakeLocalImage{imageID: "sha256:new", archive: "image-tar"}
	agent := &fakeAgentImage{remote: RemoteImageInfo{Exists: true, ID: "sha256:old"}, loaded: RemoteImageInfo{Exists: true, ID: "sha256:new"}}

	result, err := New(local, agent).SyncOpenClawImage(context.Background(), "node-1", "openclaw-runtime:dev")
	if err != nil {
		t.Fatalf("sync image: %v", err)
	}
	if !result.Transferred || result.RemoteID != "sha256:new" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestSyncOpenClawImageRejectsMismatchedLoadedID(t *testing.T) {
	local := &fakeLocalImage{imageID: "sha256:local", archive: "image-tar"}
	agent := &fakeAgentImage{remote: RemoteImageInfo{Exists: false}, loaded: RemoteImageInfo{Exists: true, ID: "sha256:other"}}

	result, err := New(local, agent).SyncOpenClawImage(context.Background(), "node-1", "openclaw-runtime:dev")
	if err == nil {
		t.Fatalf("expected mismatch error")
	}
	if !result.Transferred {
		t.Fatalf("expected attempted transfer before mismatch: %+v", result)
	}
}

func TestSyncOpenClawImagePropagatesLocalInspectError(t *testing.T) {
	local := &fakeLocalImage{err: errors.New("boom")}
	_, err := New(local, &fakeAgentImage{}).SyncOpenClawImage(context.Background(), "node-1", "openclaw-runtime:dev")
	if err == nil {
		t.Fatalf("expected error")
	}
}

type fakeLocalImage struct {
	imageID string
	archive string
	err     error
}

func (f *fakeLocalImage) ImageID(context.Context, string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.imageID, nil
}

func (f *fakeLocalImage) Archive(context.Context, string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(strings.NewReader(f.archive)), nil
}

type fakeAgentImage struct {
	remote        RemoteImageInfo
	loaded        RemoteImageInfo
	loadCalls     int
	loadedArchive string
}

func (f *fakeAgentImage) InspectImage(context.Context, string, string) (RemoteImageInfo, error) {
	return f.remote, nil
}

func (f *fakeAgentImage) LoadImage(_ context.Context, _ string, _ string, archive io.Reader) (RemoteImageInfo, error) {
	f.loadCalls++
	body, err := io.ReadAll(archive)
	if err != nil {
		return RemoteImageInfo{}, err
	}
	f.loadedArchive = string(body)
	return f.loaded, nil
}
