package imagesync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentHTTPClientInspectImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/inspect" || r.URL.Query().Get("image") != "openclaw-runtime:dev" {
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("missing auth header")
		}
		_, _ = w.Write([]byte(`{"exists":true,"info":{"id":"sha256:remote"}}`))
	}))
	defer server.Close()

	info, err := AgentHTTPClient{BaseURL: server.URL, Token: "token"}.InspectImage(context.Background(), "node-1", "openclaw-runtime:dev")
	if err != nil {
		t.Fatalf("inspect image: %v", err)
	}
	if !info.Exists || info.ID != "sha256:remote" {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestAgentHTTPClientLoadImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/load" || r.URL.Query().Get("image") != "openclaw-runtime:dev" {
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
		if r.Header.Get("Content-Type") != "application/x-tar" {
			t.Fatalf("unexpected content type: %s", r.Header.Get("Content-Type"))
		}
		_, _ = w.Write([]byte(`{"loaded":true,"info":{"id":"sha256:loaded"}}`))
	}))
	defer server.Close()

	info, err := AgentHTTPClient{BaseURL: server.URL}.LoadImage(context.Background(), "node-1", "openclaw-runtime:dev", strings.NewReader("tar"))
	if err != nil {
		t.Fatalf("load image: %v", err)
	}
	if !info.Exists || info.ID != "sha256:loaded" {
		t.Fatalf("unexpected info: %+v", info)
	}
}
