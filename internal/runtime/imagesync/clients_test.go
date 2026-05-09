package imagesync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestAgentHTTPClientInspectImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/inspect" || r.URL.Query().Get("image") != "openclaw-runtime:dev" {
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
		require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"exists":true,"info":{"id":"sha256:remote"}}`))
	}))
	defer server.Close()

	info, err := AgentHTTPClient{BaseURL: server.URL, Token: "token"}.InspectImage(context.Background(), "node-1", "openclaw-runtime:dev")
	require.NoError(t, err)
	if !info.Exists || info.ID != "sha256:remote" {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestAgentHTTPClientLoadImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/load" || r.URL.Query().Get("image") != "openclaw-runtime:dev" {
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
		require.Equal(t, "application/x-tar", r.Header.Get("Content-Type"))
		_, _ = w.Write([]byte(`{"loaded":true,"info":{"id":"sha256:loaded"}}`))
	}))
	defer server.Close()

	info, err := AgentHTTPClient{BaseURL: server.URL}.LoadImage(context.Background(), "node-1", "openclaw-runtime:dev", strings.NewReader("tar"))
	require.NoError(t, err)
	if !info.Exists || info.ID != "sha256:loaded" {
		t.Fatalf("unexpected info: %+v", info)
	}
}
