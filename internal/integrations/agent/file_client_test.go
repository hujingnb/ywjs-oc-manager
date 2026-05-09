package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestFileClientList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/files/list", r.URL.Path)
		require.Equal(t, "/data/foo", r.URL.Query().Get("path"))
		require.Equal(t, "Bearer agent-1", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"path":"/data/foo","entries":[{"path":"/data/foo/a.txt","name":"a.txt","is_dir":false,"size":3,"mode":"-rw-r--r--"}]}`))
	}))
	defer server.Close()

	client := NewFileClient(server.URL, "agent-1")
	listing, err := client.List(context.Background(), "/data/foo")
	require.NoError(t, err)
	if listing.Path != "/data/foo" || len(listing.Entries) != 1 || listing.Entries[0].Name != "a.txt" {
		t.Fatalf("listing = %+v", listing)
	}
}

func TestFileClientListPropagatesErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"path is outside data root"}`))
	}))
	defer server.Close()

	client := NewFileClient(server.URL, "agent-1")
	_, err := client.List(context.Background(), "/etc/passwd")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "path is outside data root"))
}

func TestFileClientUploadStreamsBody(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewFileClient(server.URL, "")
	err := client.Upload(context.Background(), "/data/x.txt", strings.NewReader("hello"))
	require.NoError(t, err)
	require.Equal(t, "hello", received)
}

func TestFileClientDownloadReturnsStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload"))
	}))
	defer server.Close()

	client := NewFileClient(server.URL, "")
	stream, err := client.Download(context.Background(), "/data/x.txt")
	require.NoError(t, err)
	defer stream.Close()
	body, err := io.ReadAll(stream)
	require.NoError(t, err)
	require.Equal(t, "payload", string(body))
}

func TestFileClientArchiveSurfacesError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream error"))
	}))
	defer server.Close()

	client := NewFileClient(server.URL, "")
	_, err := client.Archive(context.Background(), "/data/foo")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "upstream error"))
}

func TestResolveRemotePath(t *testing.T) {
	got := ResolveRemotePath("/data", "org-1", "app-2", "knowledge")
	want := "/data/org-1/app-2/knowledge"
	require.Equal(t, want, got)
}
