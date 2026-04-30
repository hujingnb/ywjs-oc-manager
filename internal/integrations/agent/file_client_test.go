package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFileClientList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1/files/list" {
			t.Fatalf("path = %q", got)
		}
		if got := r.URL.Query().Get("path"); got != "/data/foo" {
			t.Fatalf("path query = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer agent-1" {
			t.Fatalf("auth = %q", got)
		}
		_, _ = w.Write([]byte(`{"path":"/data/foo","entries":[{"path":"/data/foo/a.txt","name":"a.txt","is_dir":false,"size":3,"mode":"-rw-r--r--"}]}`))
	}))
	defer server.Close()

	client := NewFileClient(server.URL, "agent-1")
	listing, err := client.List(context.Background(), "/data/foo")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
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
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "path is outside data root") {
		t.Fatalf("error = %v, want body propagated", err)
	}
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
	if err := client.Upload(context.Background(), "/data/x.txt", strings.NewReader("hello")); err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if received != "hello" {
		t.Fatalf("body = %q, want hello", received)
	}
}

func TestFileClientDownloadReturnsStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload"))
	}))
	defer server.Close()

	client := NewFileClient(server.URL, "")
	stream, err := client.Download(context.Background(), "/data/x.txt")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	defer stream.Close()
	body, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(body) != "payload" {
		t.Fatalf("body = %q", string(body))
	}
}

func TestFileClientArchiveSurfacesError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream error"))
	}))
	defer server.Close()

	client := NewFileClient(server.URL, "")
	_, err := client.Archive(context.Background(), "/data/foo")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "upstream error") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveRemotePath(t *testing.T) {
	got := ResolveRemotePath("/data", "org-1", "app-2", "knowledge")
	want := "/data/org-1/app-2/knowledge"
	if got != want {
		t.Fatalf("ResolveRemotePath = %q, want %q", got, want)
	}
}
