package newapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateAPIKeyHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/token/" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer admin-token" {
			t.Fatalf("auth = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"user_id":7,"name":"alice","key":"sk-test","remain_quota":1000}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin-token")
	got, err := client.CreateAPIKey(context.Background(), CreateAPIKeyInput{UserID: 7, Name: "alice", Quota: 1000})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if got.ID != 42 || got.Key != "sk-test" {
		t.Fatalf("api key = %+v", got)
	}
}

func TestCreateAPIKeyMapsUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	_, err := client.CreateAPIKey(context.Background(), CreateAPIKeyInput{Name: "alice"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
}

func TestCreateAPIKeyMapsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	_, err := client.CreateAPIKey(context.Background(), CreateAPIKeyInput{Name: "alice"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestCreateAPIKeyMapsUpstream5xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	_, err := client.CreateAPIKey(context.Background(), CreateAPIKeyInput{Name: "alice"})
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream", err)
	}
}

func TestCreateAPIKeySurfacesUpstreamSuccessFalse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"message":"quota exhausted"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	_, err := client.CreateAPIKey(context.Background(), CreateAPIKeyInput{Name: "alice"})
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream", err)
	}
	if !strings.Contains(err.Error(), "quota exhausted") {
		t.Fatalf("error message lost upstream context: %v", err)
	}
}

func TestSetAPIKeyStatusPropagatesErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	if err := client.SetAPIKeyStatus(context.Background(), 1, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
}

func TestGetAPIKeyDecodesPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":42}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	got, err := client.GetAPIKey(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got.ID != 42 {
		t.Fatalf("id = %d", got.ID)
	}
}
