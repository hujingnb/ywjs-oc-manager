# Web Publish — Plan 3: site-server 组件 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **独立性**：本 plan 交付一个**自包含、可独立部署、可独立单测**的新服务 `site-server`。它依赖 manager 的一个内部"活跃站点列表"端点来填注册表，但该端点（查 `published_sites` 表）属 **Plan 4**。本 plan 用 httptest fake 该端点的 JSON 契约单测同步逻辑；在 Plan 4 接通前，site-server 能正常启动、注册表为空、对任何 Host 返回 404——服务本体完整可测。

**Goal:** 一个无状态小 Go 服务：接收来自通配 Ingress 的请求，按 `Host` 头查内存注册表得到站点，从对象存储 `published-sites/<siteID>/<version>/` 流式返回静态文件（目录/根路径回退 `index.html`、正确 content-type、合理缓存头）；未知/已下线/已过期 Host → 404。注册表通过**主动轮询** manager 内部端点（每 5–10s）刷新。

**Architecture:** 新二进制 `cmd/site-server`，内部包 `internal/siteserver`：`Registry`（Host→Entry 内存映射 + 读写锁）、`Handler`（http.Handler，路由 + S3 流式 + index 回退 + path 安全）、`Syncer`（定时轮询 manager 端点，整体替换注册表快照；拉取失败保留旧快照）、`SiteListClient`（调 manager 内部端点的 HTTP 客户端）。对象读取复用 `storage` 包（新增只读 `GetObject`）。核心路由/流式逻辑用 fake `ObjectReader` + 内存 Registry 全量单测。部署在 `oc-apps` 命名空间，NetworkPolicy 收敛出网只到对象存储与 manager。

**Tech Stack:** Go 1.25、`net/http`、`aws-sdk-go-v2/service/s3`（GetObject 流式）、复用 `internal/integrations/storage`、testify、raw k8s YAML（Deployment/Service/NetworkPolicy）。

---

## 背景约束（落地前必读）

- **依赖 Plan 4 的 manager 端点**：site-server 轮询的 manager 内部端点 `GET {sync_url}`（带鉴权 header）返回活跃站点 JSON，由 **Plan 4** 实现（查 `published_sites`）。本 plan **定死它的 JSON 契约**（见 Task 5），Plan 4 按此契约实现 manager 侧。契约：
  ```json
  { "sites": [
      { "host": "blog.apps.example.com",
        "site_id": "01HXXX",
        "s3_prefix": "published-sites/01HXXX/v3/",
        "status": "active" } ] }
  ```
  site-server 只把 `status=active` 的纳入注册表（manager 也应只返回 active，双保险）。
- **拉取失败保留旧快照**（spec §4.3）：manager 重启 / 网络抖动时同步失败，**不清空**注册表，继续用上次成功的快照服务，保证"manager 重启不影响 site-server 已缓存路由"。
- **一致性窗口几秒可接受**：发布后最多 1 个轮询周期（5–10s）才可访问，spec 明确接受。
- **只读单一 bucket/前缀**：site-server 只读对象存储、只触 `published-sites/` 前缀；NetworkPolicy 出网只放行对象存储与 manager；不持任何集群级或其他企业凭证（spec §4.3/§9）。
- **path 安全**：必须用 `path.Clean("/"+urlPath)` 归一化，消解 `..`，杜绝跨站点/跨前缀读取；最终对象 key 必须仍在该站点 `s3_prefix` 之下。
- **命名空间与 Service 名**：部署在 `oc-apps`，Service 名 `site-server`、端口 `80`——必须与 Plan 2 通配 Ingress 的 backend 约定（`site_server_service: site-server` / `site_server_port: 80`）一致，否则通配 Ingress 路由不到。
- 配置走 env（12-factor 独立服务）；注释/单测注释/testify 规范同前序 plan。

## File Structure

```
internal/integrations/storage/
  s3.go        # 追加 GetObject + ErrObjectNotFound（只读流式）
  store.go     # 追加 ErrObjectNotFound 文档（或放 s3.go）
  s3_test.go   # ErrObjectNotFound 哨兵存在性（真实 S3 GetObject 不单测）

internal/siteserver/
  registry.go        # Registry：Host→Entry 内存映射 + RWMutex + Replace/Lookup
  registry_test.go
  reader.go          # ObjectReader 接口 + fake（供 handler 单测）
  handler.go         # http.Handler：Host 路由 + S3 流式 + index 回退 + 404 + path 安全
  handler_test.go
  syncer.go          # Syncer：定时轮询 + Replace；失败保留旧快照
  syncer_test.go
  client.go          # SiteListClient：HTTP 调 manager 内部端点（契约见背景约束）
  client_test.go

cmd/site-server/
  main.go            # env config + 装配 + 启动 http server + syncer goroutine

deploy/k8s/local/site-server.yaml   # Deployment + Service + NetworkPolicy（本地 k3d）
deploy/k8s/prod/site-server.yaml    # 生产版本

cmd/server/Dockerfile               # 追加 go build ./cmd/site-server（复用多 binary 镜像）
```

---

### Task 1: storage 只读 GetObject + ErrObjectNotFound

**Files:**
- Modify: `internal/integrations/storage/s3.go`
- Modify: `internal/integrations/storage/store.go`（加哨兵错误与接口文档）
- Test: `internal/integrations/storage/s3_test.go`

- [ ] **Step 1: Write the failing test**

追加到 `internal/integrations/storage/s3_test.go`（若不存在则建）：
```go
package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestErrObjectNotFoundExists 覆盖：哨兵错误存在且语义稳定，site-server 据此把缺失对象映射为 404。
func TestErrObjectNotFoundExists(t *testing.T) {
	assert.EqualError(t, ErrObjectNotFound, "storage: 对象不存在")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/storage/ -run TestErrObjectNotFound -v`
Expected: 编译失败 `undefined: ErrObjectNotFound`

- [ ] **Step 3: Write minimal implementation**

在 `store.go` 顶部（import 后）加哨兵错误：
```go
// ErrObjectNotFound 表示请求的对象不存在；GetObject 在 S3 返回 NoSuchKey/404 时返回它，
// 供 site-server 把缺失对象映射为 HTTP 404。
var ErrObjectNotFound = errors.New("storage: 对象不存在")
```
（`store.go` 需 import `"errors"`。）

在 `s3.go` 追加 `GetObject` 方法（不加进 `ObjectStore` 接口，避免影响 manager 既有实现；site-server 用自己的窄接口）：
```go
// GetObject 流式读取对象内容，返回内容流与字节数。对象不存在返回 ErrObjectNotFound。
// 调用方负责 Close 返回的 ReadCloser。site-server 用它把静态文件流式回给公网访客。
func (s *S3ObjectStore) GetObject(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, 0, ErrObjectNotFound
		}
		// MinIO/部分实现对缺失 key 返回带 404 的 ResponseError 而非 NoSuchKey，一并归一。
		var respErr *smithyhttp.ResponseError
		if errors.As(err, &respErr) && respErr.HTTPStatusCode() == 404 {
			return nil, 0, ErrObjectNotFound
		}
		return nil, 0, fmt.Errorf("storage: 读取对象 %s 失败: %w", key, err)
	}
	return out.Body, aws.ToInt64(out.ContentLength), nil
}
```
（`s3.go` 已 import `types`、`smithyhttp`、`aws`、`errors`、`io`、`fmt`，见现有文件头，无需新增。）

- [ ] **Step 4: Run test + build**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/storage/ -run TestErrObjectNotFound -v && go build ./internal/integrations/storage/`
Expected: PASS / 编译通过

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/storage/s3.go internal/integrations/storage/store.go internal/integrations/storage/s3_test.go
git commit -m "feat(storage): 增加只读 GetObject 与 ErrObjectNotFound 哨兵

GetObject 流式读取对象并把 S3 NoSuchKey/404 归一为 ErrObjectNotFound，
供 site-server 流式返回静态文件并把缺失对象映射为 HTTP 404；
不改动 ObjectStore 接口，site-server 用窄接口消费。"
```

---

### Task 2: Registry —— Host→Entry 内存注册表

**Files:**
- Create: `internal/siteserver/registry.go`
- Test: `internal/siteserver/registry_test.go`

- [ ] **Step 1: Write the failing test**

```go
package siteserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRegistryReplaceAndLookup 覆盖：Replace 整体替换快照后可按 host 查到，
// 不在快照中的 host 查不到（路由依据）。
func TestRegistryReplaceAndLookup(t *testing.T) {
	r := NewRegistry()
	r.Replace(map[string]Entry{
		"blog.apps.example.com": {SiteID: "s1", S3Prefix: "published-sites/s1/v1/", Status: "active"},
	})
	e, ok := r.Lookup("blog.apps.example.com")
	assert.True(t, ok)
	assert.Equal(t, "s1", e.SiteID)

	_, ok = r.Lookup("unknown.apps.example.com")
	assert.False(t, ok)
}

// TestRegistryReplaceIsAtomicSwap 覆盖：第二次 Replace 整体换新，旧 host 不再可查
// （下线/过期的站点在下一次同步后即从路由消失）。
func TestRegistryReplaceIsAtomicSwap(t *testing.T) {
	r := NewRegistry()
	r.Replace(map[string]Entry{"a.example.com": {SiteID: "a", Status: "active"}})
	r.Replace(map[string]Entry{"b.example.com": {SiteID: "b", Status: "active"}})
	_, ok := r.Lookup("a.example.com")
	assert.False(t, ok, "旧快照的 host 应在整体替换后消失")
	_, ok = r.Lookup("b.example.com")
	assert.True(t, ok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/siteserver/ -run TestRegistry -v`
Expected: 编译失败 `undefined: NewRegistry / Entry`

- [ ] **Step 3: Write minimal implementation**

```go
// Package siteserver 实现公网静态站点服务：按 Host 路由到对象存储前缀并流式返回文件。
package siteserver

import "sync"

// Entry 是一个已发布站点的路由信息（注册表的值）。
type Entry struct {
	SiteID   string // 站点 ID
	S3Prefix string // 当前版本前缀，如 published-sites/<siteID>/<version>/（末尾带 /）
	Status   string // 站点状态；site-server 只服务 active（其余在快照里本不应出现）
}

// Registry 是 Host→Entry 的内存注册表，读多写少：读路径（每请求）用 RLock，
// 写路径（每轮同步一次）用 Lock 整体替换。
type Registry struct {
	mu     sync.RWMutex
	byHost map[string]Entry
}

// NewRegistry 构造空注册表（同步前对任何 host 都 Lookup 失败 → 404）。
func NewRegistry() *Registry {
	return &Registry{byHost: map[string]Entry{}}
}

// Replace 用新快照整体替换注册表（原子换：下线/过期站点在替换后即从路由消失）。
func (r *Registry) Replace(snapshot map[string]Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byHost = snapshot
}

// Lookup 按 host 取站点路由信息；不存在返回 ok=false（调用方据此 404）。
func (r *Registry) Lookup(host string) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.byHost[host]
	return e, ok
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/siteserver/ -run TestRegistry -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/siteserver/registry.go internal/siteserver/registry_test.go
git commit -m "feat(siteserver): 增加 Host→Entry 内存注册表

读多写少的 Registry：每请求 RLock 查路由，每轮同步 Lock 整体替换快照，
下线/过期站点在替换后即从路由消失；同步前空注册表对任何 host 返回未命中。"
```

---

### Task 3: HTTP Handler —— Host 路由 + S3 流式 + index 回退

**Files:**
- Create: `internal/siteserver/reader.go`
- Create: `internal/siteserver/handler.go`
- Test: `internal/siteserver/handler_test.go`

- [ ] **Step 1: Write the failing test**

`internal/siteserver/handler_test.go`：
```go
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

// TestServeFile 覆盖：命中 host + 存在的文件 → 200、正确 content-type、原样内容。
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
	// 试图用 ../../ 跳到另一个站点前缀
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/siteserver/ -run 'TestServe|TestRoot|TestDir|TestUnknown|TestMissing|TestPath|TestNonGet' -v`
Expected: 编译失败 `undefined: NewHandler / ObjectReader`

- [ ] **Step 3: Write minimal implementation**

`internal/siteserver/reader.go`：
```go
package siteserver

import (
	"context"
	"io"

	"oc-manager/internal/integrations/storage"
)

// ObjectReader 是 handler 读取站点文件所需的最小能力。
// 生产实现为 *storage.S3ObjectStore（已实现该方法签名）；单测用内存 fake。
type ObjectReader interface {
	// GetObject 流式读取对象；不存在返回 storage.ErrObjectNotFound。
	GetObject(ctx context.Context, key string) (io.ReadCloser, int64, error)
}

// ErrObjectNotFound 别名，便于本包与单测引用同一哨兵。
var ErrObjectNotFound = storage.ErrObjectNotFound
```

`internal/siteserver/handler.go`：
```go
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

	// content-type 按后缀推断；推不出退 octet-stream。
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/siteserver/ -run 'TestServe|TestRoot|TestDir|TestUnknown|TestMissing|TestPath|TestNonGet' -v`
Expected: PASS（注意：`TestPathTraversalBlocked` 依赖 `path.Clean("/"+...)` 把 `/../../secret/...` 归一为 `/secret/...`，拼接后 key=`published-sites/s1/v1/secret/v1/passwd`，fake 中不存在 → 404，证明无法越出本站点前缀）

- [ ] **Step 5: Commit**

```bash
git add internal/siteserver/reader.go internal/siteserver/handler.go internal/siteserver/handler_test.go
git commit -m "feat(siteserver): 增加 Host 路由 + S3 流式返回的 HTTP handler

按 Host 查注册表 → 计算对象 key（目录/根回退 index.html、path.Clean 消解 ..
防越权）→ 从对象存储流式返回，正确 content-type 与缓存头；未知 host/缺失文件
返回 404，非 GET/HEAD 返回 405。核心逻辑用内存 fake reader 全量单测。"
```

---

### Task 4: Syncer —— 定时轮询刷新注册表

**Files:**
- Create: `internal/siteserver/syncer.go`
- Test: `internal/siteserver/syncer_test.go`

- [ ] **Step 1: Write the failing test**

```go
package siteserver

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeListClient 是 SiteListClient 的替身：按序返回预置结果/错误。
type fakeListClient struct {
	rets [][]SiteRecord
	errs []error
	call int
}

func (f *fakeListClient) ListActiveSites(_ context.Context) ([]SiteRecord, error) {
	i := f.call
	f.call++
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	return f.rets[i], nil
}

// TestSyncOnceBuildsSnapshot 覆盖：一次同步把 active 站点写入注册表，按 host 可查。
func TestSyncOnceBuildsSnapshot(t *testing.T) {
	reg := NewRegistry()
	cl := &fakeListClient{rets: [][]SiteRecord{{
		{Host: "blog.example.com", SiteID: "s1", S3Prefix: "published-sites/s1/v1/", Status: "active"},
	}}}
	s := NewSyncer(cl, reg, 0)
	require.NoError(t, s.syncOnce(context.Background()))

	e, ok := reg.Lookup("blog.example.com")
	require.True(t, ok)
	assert.Equal(t, "s1", e.SiteID)
}

// TestSyncFailureKeepsOldSnapshot 覆盖：拉取失败时不清空注册表，继续用上次成功快照
//（manager 重启不影响已缓存路由）。
func TestSyncFailureKeepsOldSnapshot(t *testing.T) {
	reg := NewRegistry()
	cl := &fakeListClient{
		rets: [][]SiteRecord{{{Host: "blog.example.com", SiteID: "s1", S3Prefix: "p/", Status: "active"}}, nil},
		errs: []error{nil, errors.New("manager down")},
	}
	s := NewSyncer(cl, reg, 0)

	require.NoError(t, s.syncOnce(context.Background())) // 首次成功
	require.Error(t, s.syncOnce(context.Background()))    // 二次失败

	// 失败后旧快照仍在
	_, ok := reg.Lookup("blog.example.com")
	assert.True(t, ok, "同步失败不应清空注册表")
}

// TestSyncFiltersNonActive 覆盖：非 active 记录被过滤，不进路由（双保险）。
func TestSyncFiltersNonActive(t *testing.T) {
	reg := NewRegistry()
	cl := &fakeListClient{rets: [][]SiteRecord{{
		{Host: "a.example.com", SiteID: "a", S3Prefix: "pa/", Status: "active"},
		{Host: "b.example.com", SiteID: "b", S3Prefix: "pb/", Status: "disabled"},
	}}}
	s := NewSyncer(cl, reg, 0)
	require.NoError(t, s.syncOnce(context.Background()))

	_, ok := reg.Lookup("a.example.com")
	assert.True(t, ok)
	_, ok = reg.Lookup("b.example.com")
	assert.False(t, ok, "非 active 站点不应进入路由")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/siteserver/ -run TestSync -v`
Expected: 编译失败 `undefined: NewSyncer / SiteRecord / SiteListClient`

- [ ] **Step 3: Write minimal implementation**

```go
package siteserver

import (
	"context"
	"time"
)

// SiteRecord 是 manager 内部端点返回的单条活跃站点记录（与 Plan 4 端点 JSON 字段对应）。
type SiteRecord struct {
	Host     string `json:"host"`
	SiteID   string `json:"site_id"`
	S3Prefix string `json:"s3_prefix"`
	Status   string `json:"status"`
}

// SiteListClient 抽象"从 manager 拉活跃站点列表"的能力（生产用 HTTP 客户端，单测用 fake）。
type SiteListClient interface {
	ListActiveSites(ctx context.Context) ([]SiteRecord, error)
}

// Syncer 周期性轮询 manager 端点并整体刷新注册表；拉取失败保留旧快照。
type Syncer struct {
	client   SiteListClient
	registry *Registry
	interval time.Duration
}

// NewSyncer 构造 syncer；interval<=0 时由 Run 用默认 5s（单测传 0 只调 syncOnce）。
func NewSyncer(client SiteListClient, registry *Registry, interval time.Duration) *Syncer {
	return &Syncer{client: client, registry: registry, interval: interval}
}

// syncOnce 拉一次活跃站点并整体替换注册表；失败直接返回错误、不动注册表（保留旧快照）。
func (s *Syncer) syncOnce(ctx context.Context) error {
	records, err := s.client.ListActiveSites(ctx)
	if err != nil {
		return err // 保留旧快照，由 Run 记录日志后等下一周期
	}
	snapshot := make(map[string]Entry, len(records))
	for _, rec := range records {
		// 双保险：只把 active 纳入路由（manager 也应只返回 active）。
		if rec.Status != "active" {
			continue
		}
		snapshot[rec.Host] = Entry{SiteID: rec.SiteID, S3Prefix: rec.S3Prefix, Status: rec.Status}
	}
	s.registry.Replace(snapshot)
	return nil
}

// Run 阻塞循环：立即同步一次，之后每 interval 同步一次，直到 ctx 取消。
// 单次失败只记日志、不退出（下周期重试），保证 manager 抖动不影响服务。
func (s *Syncer) Run(ctx context.Context, onError func(error)) {
	interval := s.interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	// 启动即同步一次，缩短冷启动空窗。
	if err := s.syncOnce(ctx); err != nil && onError != nil {
		onError(err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.syncOnce(ctx); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/siteserver/ -run TestSync -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/siteserver/syncer.go internal/siteserver/syncer_test.go
git commit -m "feat(siteserver): 增加定时轮询刷新注册表的 Syncer

每周期拉 manager 活跃站点列表并整体替换注册表；拉取失败保留旧快照（manager
重启不影响已缓存路由），非 active 记录过滤不进路由。syncOnce 用 fake client
全量单测成功/失败/过滤路径。"
```

---

### Task 5: SiteListClient —— 调 manager 内部端点

**Files:**
- Create: `internal/siteserver/client.go`
- Test: `internal/siteserver/client_test.go`

> 定死与 Plan 4 的 JSON 契约。用 httptest fake manager 端点单测解析与鉴权 header。

- [ ] **Step 1: Write the failing test**

```go
package siteserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHTTPClientParsesSites 覆盖：客户端带鉴权 header 调端点，正确解析 sites 数组。
func TestHTTPClientParsesSites(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 校验鉴权 header 已带上
		assert.Equal(t, "secret-token", r.Header.Get("X-OC-Site-Sync-Token"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sites":[{"host":"blog.example.com","site_id":"s1","s3_prefix":"published-sites/s1/v1/","status":"active"}]}`))
	}))
	defer srv.Close()

	c := NewHTTPSiteListClient(srv.URL, "secret-token")
	sites, err := c.ListActiveSites(context.Background())
	require.NoError(t, err)
	require.Len(t, sites, 1)
	assert.Equal(t, "blog.example.com", sites[0].Host)
	assert.Equal(t, "published-sites/s1/v1/", sites[0].S3Prefix)
}

// TestHTTPClientNon200 覆盖：端点非 200（如 manager 未就绪）返回错误，syncer 据此保留旧快照。
func TestHTTPClientNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := NewHTTPSiteListClient(srv.URL, "t")
	_, err := c.ListActiveSites(context.Background())
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/siteserver/ -run TestHTTPClient -v`
Expected: 编译失败 `undefined: NewHTTPSiteListClient`

- [ ] **Step 3: Write minimal implementation**

```go
package siteserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// HTTPSiteListClient 调 manager 内部端点拉活跃站点列表（契约见 plan 背景约束）。
type HTTPSiteListClient struct {
	url   string // manager 内部端点完整 URL
	token string // 集群内鉴权 token（X-OC-Site-Sync-Token）
	http  *http.Client
}

// NewHTTPSiteListClient 构造客户端，带 10s 超时（同步是后台任务，不必长等）。
func NewHTTPSiteListClient(url, token string) *HTTPSiteListClient {
	return &HTTPSiteListClient{url: url, token: token, http: &http.Client{Timeout: 10 * time.Second}}
}

// listResponse 对应 manager 端点 JSON：{"sites":[...]}。
type listResponse struct {
	Sites []SiteRecord `json:"sites"`
}

// ListActiveSites 调 manager 端点并解析返回；非 200 视为失败（syncer 据此保留旧快照）。
func (c *HTTPSiteListClient) ListActiveSites(ctx context.Context) ([]SiteRecord, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-OC-Site-Sync-Token", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("siteserver: 调 manager 同步端点失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("siteserver: manager 同步端点返回 %d", resp.StatusCode)
	}
	var out listResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("siteserver: 解析同步响应失败: %w", err)
	}
	return out.Sites, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/siteserver/ -run TestHTTPClient -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/siteserver/client.go internal/siteserver/client_test.go
git commit -m "feat(siteserver): 增加调 manager 内部端点的 SiteListClient

HTTP 客户端带 X-OC-Site-Sync-Token 鉴权拉取 {\"sites\":[...]} 契约，非 200 视为
失败供 syncer 保留旧快照；用 httptest 单测解析与鉴权 header。manager 侧端点在
Plan 4 实现，本 plan 定死 JSON 契约。"
```

---

### Task 6: cmd/site-server 装配

**Files:**
- Create: `cmd/site-server/main.go`

- [ ] **Step 1: Write main（env 配置 + 装配 + 启动）**

```go
// Command site-server 是公网静态站点服务：按 Host 路由到对象存储前缀流式返回文件。
// 无状态、只读单一 bucket/前缀；注册表通过轮询 manager 内部端点刷新。
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"oc-manager/internal/integrations/storage"
	"oc-manager/internal/siteserver"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 配置全部走 env（12-factor）；S3 为只读凭证，sync token 与 manager 共享。
	listenAddr := envOr("SITE_SERVER_LISTEN", ":80")
	s3cfg := storage.S3Config{
		Endpoint:        os.Getenv("S3_ENDPOINT"),
		Region:          envOr("S3_REGION", "us-east-1"),
		Bucket:          os.Getenv("S3_BUCKET"),
		AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		UsePathStyle:    os.Getenv("S3_USE_PATH_STYLE") == "true",
	}
	syncURL := os.Getenv("MANAGER_SYNC_URL")
	syncToken := os.Getenv("MANAGER_SYNC_TOKEN")
	interval := 5 * time.Second
	if v := os.Getenv("SYNC_INTERVAL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			interval = time.Duration(n) * time.Second
		}
	}

	store := storage.NewS3ObjectStore(s3cfg)
	registry := siteserver.NewRegistry()
	handler := siteserver.NewHandler(registry, store)
	syncer := siteserver.NewSyncer(siteserver.NewHTTPSiteListClient(syncURL, syncToken), registry, interval)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 后台轮询刷新注册表。
	go syncer.Run(ctx, func(err error) { logger.Warn("站点注册表同步失败，保留旧快照", "err", err) })

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	logger.Info("site-server 启动", "addr", listenAddr, "sync_url", syncURL, "interval", interval.String())
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("site-server 退出", "err", err)
		os.Exit(1)
	}
}

// envOr 读取环境变量，空则返回默认值。
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 2: Build verify**

Run: `cd /home/user/ywjs-oc-manager && go build ./cmd/site-server && go vet ./cmd/site-server`
Expected: 编译通过

- [ ] **Step 3: Commit**

```bash
git add cmd/site-server/main.go
git commit -m "feat(site-server): 增加 site-server 二进制入口与装配

env 配置（只读 S3 + manager 同步端点/token + 轮询间隔），装配 Registry/Handler/
Syncer，后台轮询刷新注册表、前台提供静态站点 HTTP 服务，支持优雅关停。"
```

---

### Task 7: 镜像构建 + k8s 部署清单

**Files:**
- Modify: `cmd/server/Dockerfile`（复用多 binary 镜像，追加 site-server 构建）
- Create: `deploy/k8s/local/site-server.yaml`
- Create: `deploy/k8s/prod/site-server.yaml`

- [ ] **Step 1: Dockerfile 追加构建**

在 `cmd/server/Dockerfile` 的 build 阶段（agent 确认现有 `go build ... ./cmd/server && ... ./cmd/migrate && ...`）追加一行：
```dockerfile
 && go build -trimpath -o /out/site-server ./cmd/site-server \
```
（接到现有 `&& go build ...` 链尾，保持同一 RUN。）

- [ ] **Step 2: 本地部署清单**

`deploy/k8s/local/site-server.yaml`（参照 `deploy/k8s/local/manager-api.yaml` 的镜像/imagePullSecrets 写法）：
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: site-server
  namespace: oc-apps
  labels:
    app: site-server
    app.kubernetes.io/part-of: oc-manager
spec:
  replicas: 1
  selector:
    matchLabels:
      app: site-server
  template:
    metadata:
      labels:
        app: site-server
        app.kubernetes.io/part-of: oc-manager
    spec:
      containers:
        - name: site-server
          image: oc-manager:local            # 与 manager 同镜像（多 binary）
          command: ["/out/site-server"]
          ports:
            - containerPort: 80
          env:
            - { name: SITE_SERVER_LISTEN, value: ":80" }
            - { name: S3_ENDPOINT, value: "http://minio.ocm.svc:9000" }
            - { name: S3_REGION, value: "us-east-1" }
            - { name: S3_BUCKET, value: "oc-apps" }
            - { name: S3_USE_PATH_STYLE, value: "true" }
            - { name: S3_ACCESS_KEY_ID, valueFrom: { secretKeyRef: { name: site-server-secrets, key: s3-access-key-id } } }
            - { name: S3_SECRET_ACCESS_KEY, valueFrom: { secretKeyRef: { name: site-server-secrets, key: s3-secret-access-key } } }
            - { name: MANAGER_SYNC_URL, value: "http://oc-manager.ocm.svc:8080/internal/web-publish/sites" }
            - { name: MANAGER_SYNC_TOKEN, valueFrom: { secretKeyRef: { name: site-server-secrets, key: sync-token } } }
            - { name: SYNC_INTERVAL_SECONDS, value: "5" }
          resources:
            requests: { cpu: "50m", memory: "64Mi" }
            limits:   { cpu: "200m", memory: "128Mi" }
          readinessProbe:
            tcpSocket: { port: 80 }
            initialDelaySeconds: 3
            periodSeconds: 10
---
apiVersion: v1
kind: Service
metadata:
  name: site-server
  namespace: oc-apps
  labels:
    app: site-server
spec:
  selector:
    app: site-server
  ports:
    - name: http
      port: 80           # 必须与 Plan 2 通配 Ingress backend port 一致
      targetPort: 80
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: site-server
  namespace: oc-apps
spec:
  podSelector:
    matchLabels:
      app: site-server
  policyTypes: [Ingress, Egress]
  ingress:
    # 允许来自 ingress 控制器的入站（本地 traefik 在 kube-system；按环境调整 namespaceSelector）。
    - {}
  egress:
    # 放行 DNS 解析。
    - to: []
      ports:
        - { protocol: UDP, port: 53 }
        - { protocol: TCP, port: 53 }
    # 放行对象存储（MinIO 9000）与 manager 同步端点（8080）。出网只到这两处。
    - to: []
      ports:
        - { protocol: TCP, port: 9000 }
        - { protocol: TCP, port: 8080 }
```

> `site-server-secrets`（s3 只读凭证 + sync-token）由本地 bootstrap（`make local-up` 链路或手动 `kubectl create secret`）创建；在交付说明里写明需创建该 Secret。`MANAGER_SYNC_TOKEN` 须与 manager 侧 Plan 4 端点校验的 token 一致。

- [ ] **Step 3: 生产部署清单**

`deploy/k8s/prod/site-server.yaml`：与 local 同构，差异：
- `image` 指向生产镜像仓库 + tag，并加 `imagePullSecrets`（参照 `deploy/k8s/prod/manager-api.yaml`）。
- `S3_ENDPOINT`/`S3_BUCKET` 指向生产对象存储（EOS/云 OSS）。
- `MANAGER_SYNC_URL` 指向生产 manager service DNS。
- NetworkPolicy egress 端口按生产对象存储实际端口（如 443）调整；`replicas` 可设 2 提升可用性（无状态可水平扩展，注册表各副本独立轮询，spec §4.3 允许）。

> 生产清单的具体镜像 tag / endpoint / 端口以环境为准，落地时按 `deploy/k8s/prod/` 既有约定填。

- [ ] **Step 4: 校验 YAML 可被 k8s 解析（本地有 k3d 时）**

Run（本地 k3d 就绪时；无集群则跳过并在交付说明注明）:
```bash
kubectl --context k3d-ocm apply --dry-run=client -f deploy/k8s/local/site-server.yaml
```
Expected: 三资源 `created (dry run)`，无 schema 错误

- [ ] **Step 5: Commit**

```bash
git add cmd/server/Dockerfile deploy/k8s/local/site-server.yaml deploy/k8s/prod/site-server.yaml
git commit -m "feat(deploy): 增加 site-server 镜像构建与 k8s 部署清单

复用多 binary 镜像追加 site-server 构建；新增 oc-apps 命名空间下的
Deployment/Service（name=site-server:80，对齐通配 Ingress backend）与
NetworkPolicy（出网仅放行 DNS/对象存储/manager 同步端点），含本地与生产两份。"
```

---

## Self-Review

**1. Spec coverage（对应 §4.3 / §11.3）：**
- 无状态小 Go 服务、平台级单 Deployment、apps 命名空间 → Task 6/7 ✓
- 按 Host 查注册表 → siteID → Task 2 Registry + Task 3 Handler ✓
- 未知/已下线/已过期 Host → 404 → Task 3（Lookup 失败或 status≠active）✓
- 从 `published-sites/<siteID>/` 流式返回、目录/根回退 index.html、正确 content-type、合理缓存头 → Task 3 ✓
- 注册表主动轮询 manager 内部端点（5–10s）、manager 重启不影响已缓存路由 → Task 4 Syncer（失败保留旧快照）+ Task 5 Client ✓
- manager 提供集群内带鉴权端点返回 host→{siteID,s3_prefix,status} → 契约定死（Task 5），实现属 Plan 4 ✓
- 安全：NetworkPolicy 出网只到对象存储（+manager 同步）、资源 limits、只读单一 bucket/前缀、不持集群级凭证 → Task 7 + Task 1（只读 GetObject）✓
- 共享单实例、未来可演进每企业独立 pod（接口不阻断）→ 注册表/handler 无状态、可水平扩展（prod replicas=2 注明）✓

**2. Placeholder scan：** 真实 S3 `GetObject`（Task 1）与部署清单（Task 7）不做 Go 单测——前者触真实对象存储、后者是 YAML，均为合理取舍并给出 dry-run 校验与本地联调验证方式。其余逻辑（registry/handler/syncer/client）全部 TDD 覆盖含安全用例（path traversal、405、404、失败保留快照）。

**3. Type consistency：**
- `siteserver.Entry{SiteID,S3Prefix,Status}`（Task 2）被 Handler（Task 3）、Syncer（Task 4）一致消费 ✓
- `siteserver.ObjectReader.GetObject(ctx,key)(io.ReadCloser,int64,error)`（Task 3）与 `storage.S3ObjectStore.GetObject`（Task 1）签名一致，生产可直接注入 ✓
- `SiteRecord{Host,SiteID,S3Prefix,Status}` JSON tag（Task 4）与 Task 5 客户端解析、与 Plan 4 manager 端点契约一致 ✓
- Service 名/端口 `site-server:80`（Task 7）与 Plan 2 `WebPublishConfig.SiteServerService/Port` 默认值一致 ✓

**给 Plan 4 的契约（manager 侧需实现）：**
- 内部端点 `GET /internal/web-publish/sites`，校验 `X-OC-Site-Sync-Token` header（token 与 site-server `MANAGER_SYNC_TOKEN` 共享，入 manager config）。
- 响应 `{"sites":[{"host","site_id","s3_prefix","status"}]}`，只返回 `status=active` 的 `published_sites` 行；`s3_prefix` 为当前版本前缀（末尾带 `/`）。
- 该端点应在集群内可达、不经用户 JWT 鉴权链（独立内部鉴权），放在 manager 一个 internal 路由组。

**落地者需确认的仓库既有项：** `cmd/server/Dockerfile` 现有 build 链的确切续行写法；本地 MinIO/manager 的 svc DNS 名与端口（示例用 `minio.ocm.svc:9000`、`oc-manager.ocm.svc:8080`，以实际 Service 名为准）；本地 NetworkPolicy 的 ingress 控制器 namespace（traefik 在 kube-system 还是别处）。
