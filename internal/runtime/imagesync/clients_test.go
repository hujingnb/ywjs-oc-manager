package imagesync

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"github.com/stretchr/testify/require"
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

func TestAgentHTTPClientInspectImageUsesConfiguredTLSClient(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/images/inspect", r.URL.Path)
		_, _ = w.Write([]byte(`{"exists":true,"info":{"id":"sha256:remote"}}`))
	}))
	defer server.Close()

	_, err := AgentHTTPClient{BaseURL: server.URL}.InspectImage(context.Background(), "node-1", "openclaw-runtime:dev")
	require.Error(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	info, err := AgentHTTPClient{BaseURL: server.URL, HTTPClient: client}.InspectImage(context.Background(), "node-1", "openclaw-runtime:dev")
	require.NoError(t, err)
	require.True(t, info.Exists)
	require.Equal(t, "sha256:remote", info.ID)
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
