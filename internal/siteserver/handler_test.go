package siteserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeReader 是内存 ObjectReader：key→内容；未命中返回 ErrObjectNotFound。
type fakeReader struct{ objs map[string]string }

func (f *fakeReader) GetObject(_ context.Context, key string) (io.ReadCloser, int64, error) {
	v, ok := f.objs[key]
	if !ok {
		return nil, 0, ErrObjectNotFound
	}
	return io.NopCloser(strings.NewReader(v)), int64(len(v)), nil
}

func newTestHandler(objs map[string]string, entries map[string]Entry) *Handler {
	reg := NewRegistry()
	reg.Replace(entries)
	return NewHandler(reg, &fakeReader{objs: objs})
}

// TestServeFile 覆盖：命中 host + 存在文件 → 200、正确 content-type、原样内容。
func TestServeFile(t *testing.T) {
	h := newTestHandler(
		map[string]string{"published-sites/s1/v1/style.css": "body{}"},
		map[string]Entry{"blog.example.com": {SiteID: "s1", S3Prefix: "published-sites/s1/v1/", Status: "active"}},
	)
	req := httptest.NewRequest(http.MethodGet, "http://blog.example.com/style.css", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/css")
	assert.Equal(t, "body{}", w.Body.String())
}

// TestRootFallsBackToIndex 覆盖：根路径 "/" 回退 index.html。
func TestRootFallsBackToIndex(t *testing.T) {
	h := newTestHandler(
		map[string]string{"published-sites/s1/v1/index.html": "<h1>home</h1>"},
		map[string]Entry{"blog.example.com": {SiteID: "s1", S3Prefix: "published-sites/s1/v1/", Status: "active"}},
	)
	req := httptest.NewRequest(http.MethodGet, "http://blog.example.com/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "<h1>home</h1>", w.Body.String())
}

// TestDirFallsBackToIndex 覆盖：以 "/" 结尾的目录路径回退该目录下 index.html。
func TestDirFallsBackToIndex(t *testing.T) {
	h := newTestHandler(
		map[string]string{"published-sites/s1/v1/docs/index.html": "docs"},
		map[string]Entry{"blog.example.com": {SiteID: "s1", S3Prefix: "published-sites/s1/v1/", Status: "active"}},
	)
	req := httptest.NewRequest(http.MethodGet, "http://blog.example.com/docs/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "docs", w.Body.String())
}

// TestUnknownHost404 覆盖：未注册 host → 404，不触对象存储。
func TestUnknownHost404(t *testing.T) {
	h := newTestHandler(map[string]string{}, map[string]Entry{})
	req := httptest.NewRequest(http.MethodGet, "http://nope.example.com/x", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestMissingFile404 覆盖：host 命中但文件不存在 → 404。
func TestMissingFile404(t *testing.T) {
	h := newTestHandler(
		map[string]string{},
		map[string]Entry{"blog.example.com": {SiteID: "s1", S3Prefix: "published-sites/s1/v1/", Status: "active"}},
	)
	req := httptest.NewRequest(http.MethodGet, "http://blog.example.com/missing.js", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestPathTraversalBlocked 覆盖：含 ../ 的路径被归一化，不能越出站点前缀读别处对象。
func TestPathTraversalBlocked(t *testing.T) {
	h := newTestHandler(
		map[string]string{"published-sites/secret/v1/passwd": "TOPSECRET"},
		map[string]Entry{"blog.example.com": {SiteID: "s1", S3Prefix: "published-sites/s1/v1/", Status: "active"}},
	)
	req := httptest.NewRequest(http.MethodGet, "http://blog.example.com/../../secret/v1/passwd", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.NotContains(t, w.Body.String(), "TOPSECRET")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestNonGetMethod405 覆盖：静态站点只读，非 GET/HEAD 返回 405。
func TestNonGetMethod405(t *testing.T) {
	h := newTestHandler(map[string]string{}, map[string]Entry{
		"blog.example.com": {SiteID: "s1", S3Prefix: "published-sites/s1/v1/", Status: "active"}})
	req := httptest.NewRequest(http.MethodPost, "http://blog.example.com/x", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	require.NotNil(t, w.Body)
}
