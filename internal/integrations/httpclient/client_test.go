// Package httpclient 的 client_test 覆盖共享 HTTP 客户端的超时、状态码和响应读取边界。
package httpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoJSON_happy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "Bearer secret-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"alice"}`))
	}))
	defer server.Close()

	c := &BaseHTTPClient{BaseURL: server.URL, AuthToken: "secret-token"}
	var out struct {
		Name string `json:"name"`
	}
	err := c.DoJSON(context.Background(), http.MethodPost, "/users", nil, map[string]string{"x": "y"}, &out)
	require.NoError(t, err)
	assert.Equal(t, "alice", out.Name)
}

func TestDoJSON_404_returnsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	c := &BaseHTTPClient{BaseURL: server.URL}
	err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil, nil)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestDoJSON_401_returnsUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	c := &BaseHTTPClient{BaseURL: server.URL}
	err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil, nil)
	assert.True(t, errors.Is(err, ErrUnauthorized))
}

func TestDoJSON_500_returnsUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`upstream details`))
	}))
	defer server.Close()
	c := &BaseHTTPClient{BaseURL: server.URL}
	err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil, nil)
	assert.True(t, errors.Is(err, ErrUpstream))
	assert.Contains(t, err.Error(), "upstream details")
}

func TestDoJSON_409_returnsConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()
	c := &BaseHTTPClient{BaseURL: server.URL}
	err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil, nil)
	assert.True(t, errors.Is(err, ErrConflict))
}

func TestDoJSON_400_returnsPayloadInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	c := &BaseHTTPClient{BaseURL: server.URL}
	err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil, nil)
	assert.True(t, errors.Is(err, ErrPayloadInvalid))
}

func TestDoStream_happy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("binary content"))
	}))
	defer server.Close()
	c := &BaseHTTPClient{BaseURL: server.URL}
	var buf strings.Builder
	err := c.DoStream(context.Background(), http.MethodGet, "/x", nil, &buf)
	require.NoError(t, err)
	assert.Equal(t, "binary content", buf.String())
}

func TestDoStream_404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	c := &BaseHTTPClient{BaseURL: server.URL}
	var buf strings.Builder
	err := c.DoStream(context.Background(), http.MethodGet, "/x", nil, &buf)
	assert.True(t, errors.Is(err, ErrNotFound))
}
