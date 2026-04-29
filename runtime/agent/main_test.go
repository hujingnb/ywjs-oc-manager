package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	newHandlerWithDocker("/tmp/agent", &fakeDockerClient{}, "").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	var body HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "ok" || body.Role != "runtime-agent" || body.DataRoot != "/tmp/agent" {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestInspectImage(t *testing.T) {
	docker := &fakeDockerClient{
		images: map[string]DockerImageInfo{
			"openclaw-runtime:dev": {ID: "sha256:local", RepoTags: []string{"openclaw-runtime:dev"}},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/images/inspect?image=openclaw-runtime:dev", nil)
	rec := httptest.NewRecorder()

	newHandlerWithDocker("/tmp/agent", docker, "").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Exists bool            `json:"exists"`
		Info   DockerImageInfo `json:"info"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Exists || body.Info.ID != "sha256:local" {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestInspectImageNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/images/inspect?image=missing:dev", nil)
	rec := httptest.NewRecorder()

	newHandlerWithDocker("/tmp/agent", &fakeDockerClient{}, "").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	var body struct {
		Exists bool `json:"exists"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Exists {
		t.Fatalf("expected image to be missing")
	}
}

func TestLoadImageRequiresTokenWhenConfigured(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/images/load?image=openclaw-runtime:dev", bytes.NewBufferString("tar"))
	rec := httptest.NewRecorder()

	newHandlerWithDocker("/tmp/agent", &fakeDockerClient{}, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}
}

func TestLoadImage(t *testing.T) {
	docker := &fakeDockerClient{images: map[string]DockerImageInfo{}}
	req := httptest.NewRequest(http.MethodPost, "/v1/images/load?image=openclaw-runtime:dev", bytes.NewBufferString("tar"))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()

	newHandlerWithDocker("/tmp/agent", docker, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if docker.loadedBytes != "tar" {
		t.Fatalf("unexpected loaded bytes: %q", docker.loadedBytes)
	}
}

type fakeDockerClient struct {
	images      map[string]DockerImageInfo
	loadedBytes string
}

func (f *fakeDockerClient) InspectImage(_ context.Context, image string) (DockerImageInfo, error) {
	if f.images == nil {
		return DockerImageInfo{}, ErrImageNotFound
	}
	info, ok := f.images[image]
	if !ok {
		return DockerImageInfo{}, ErrImageNotFound
	}
	return info, nil
}

func (f *fakeDockerClient) LoadImage(_ context.Context, archive io.Reader) error {
	body, err := io.ReadAll(archive)
	if err != nil {
		return err
	}
	f.loadedBytes = string(body)
	if f.images == nil {
		f.images = map[string]DockerImageInfo{}
	}
	f.images["openclaw-runtime:dev"] = DockerImageInfo{ID: "sha256:loaded", RepoTags: []string{"openclaw-runtime:dev"}}
	return nil
}
