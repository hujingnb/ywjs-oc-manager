package siteserver

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// Handler 按 Host 路由到站点前缀并从对象存储流式返回静态文件。
type Handler struct {
	registry *Registry
	reader   ObjectReader
}

// NewHandler 构造 handler。
func NewHandler(registry *Registry, reader ObjectReader) *Handler {
	return &Handler{registry: registry, reader: reader}
}

// ServeHTTP 实现路由：解析 Host → 查注册表 → 计算对象 key（含 index 回退与 path 安全）→ 流式返回。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 静态站点只读：仅允许 GET/HEAD。
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	host := stripPort(r.Host)
	entry, ok := h.registry.Lookup(host)
	if !ok || entry.Status != "active" {
		http.NotFound(w, r)
		return
	}
	// path 安全：归一化消解 ..；目录/根回退 index.html。
	// path.Clean("/" + path) 保证结果始终以 / 开头，且 .. 不能越过根，
	// 因此拼接 S3Prefix 后不会逃出站点前缀范围。
	rel := path.Clean("/" + r.URL.Path) // 始终以 / 开头，.. 被消解，不会越过根
	if rel == "/" || strings.HasSuffix(r.URL.Path, "/") {
		rel = path.Join(rel, "index.html")
	}
	key := entry.S3Prefix + strings.TrimPrefix(rel, "/")

	rc, size, err := h.reader.GetObject(r.Context(), key)
	if errors.Is(err, ErrObjectNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	if ct := mime.TypeByExtension(path.Ext(rel)); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	// 合理缓存：公网静态资源缓存 5 分钟（与发布一致性窗口同量级，避免过期内容长留）。
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, rc)
}

// stripPort 去掉 Host 头里可能带的端口（如 blog.example.com:443 → blog.example.com）。
func stripPort(host string) string {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		return host[:i]
	}
	return host
}
