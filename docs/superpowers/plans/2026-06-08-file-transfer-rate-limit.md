# File Transfer Rate Limit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add configurable per-request upload and download rate limiting for manager-facing knowledge file transfers.

**Architecture:** Keep the limit at the HTTP handler boundary. Configuration stays in `config.Config`, router dependencies pass normalized values into handlers, upload bodies are wrapped before service reads them, and downloads are wrapped in the shared `writeKnowledgeDownload` path. Runtime internal upload remains unchanged.

**Tech Stack:** Go, Gin, `golang.org/x/time/rate`, project config loader, testify, existing knowledge handler tests.

---

## File Structure

- Modify: `internal/config/config.go`
  - Add `TransferLimitConfig` to the main config model.
- Modify: `internal/config/loader.go`
  - Validate that upload/download limits are non-negative. Do not apply defaults.
- Modify: `internal/config/loader_test.go`
  - Cover missing config, explicit `524288`, and negative config.
- Create: `internal/api/handlers/transfer_limit.go`
  - Provide handler-local rate-limited `io.ReadCloser` helpers.
- Create: `internal/api/handlers/transfer_limit_test.go`
  - Test no-limit passthrough, positive-limit wrapping, and data preservation without slow wall-clock assertions.
- Modify: `internal/api/handlers/knowledge.go`
  - Store transfer config in `KnowledgeHandler`; apply upload and download limits for org/app knowledge files.
- Modify: `internal/api/handlers/knowledge_test.go`
  - Cover upload wrapping and download response behavior with limit config.
- Modify: `internal/api/handlers/industry_knowledge.go`
  - Store transfer config in `IndustryKnowledgeHandler`; apply upload and download limits for platform and external industry knowledge files.
- Modify: `internal/api/handlers/industry_knowledge_test.go`
  - Cover platform upload, external upload, and download behavior with limit config.
- Modify: `internal/api/router.go`
  - Add a dependency field and pass it into knowledge handlers. Runtime knowledge handler receives no limit config.
- Modify: `cmd/server/main.go`
  - Convert `cfg.TransferLimit` into handler dependency config.
- Modify: `config/manager.example.yaml`
  - Add example `transfer_limit` with `524288` bytes/s.
- Modify: `deploy/k8s/prod/secret.example.yaml`
  - Add matching production Secret example.
- Modify: `docs/configuration.md`
  - Document units, zero-value behavior, negative validation, and per-request scope.
- Possibly Modify: `go.mod`
  - `golang.org/x/time` is already present as indirect. If `go mod tidy` makes it direct, commit that mechanical change.

---

### Task 1: Add Config Contract

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`
- Modify: `internal/config/loader_test.go`

- [ ] **Step 1: Write tests for default, explicit, and invalid transfer limits**

In `internal/config/loader_test.go`, add these tests near the other config validation tests:

```go
// TestTransferLimitDefaultsToUnlimited 验证未配置 transfer_limit 时保持历史行为，不启用上传或下载限速。
func TestTransferLimitDefaultsToUnlimited(t *testing.T) {
	cfg := loadConfigFromString(t, fullValidYAML())

	assert.Equal(t, int64(0), cfg.TransferLimit.UploadBytesPerSec)
	assert.Equal(t, int64(0), cfg.TransferLimit.DownloadBytesPerSec)
}

// TestTransferLimitLoadsExplicitBytesPerSecond 验证配置的字节每秒速率会原样加载，供 handler 层执行单请求限速。
func TestTransferLimitLoadsExplicitBytesPerSecond(t *testing.T) {
	yaml := fullValidYAML() + `
transfer_limit:
  upload_bytes_per_sec: 524288
  download_bytes_per_sec: 524288
`
	cfg := loadConfigFromString(t, yaml)

	assert.Equal(t, int64(524288), cfg.TransferLimit.UploadBytesPerSec)
	assert.Equal(t, int64(524288), cfg.TransferLimit.DownloadBytesPerSec)
}

// TestTransferLimitRejectsNegativeValues 验证负数限速会在启动阶段 fail-fast，避免运行期出现无意义的 limiter 参数。
func TestTransferLimitRejectsNegativeValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		// 上传限速为负数没有业务含义，应拒绝启动。
		{name: "negative_upload", body: "upload_bytes_per_sec: -1\n  download_bytes_per_sec: 0", want: "transfer_limit.upload_bytes_per_sec"},
		// 下载限速为负数没有业务含义，应拒绝启动。
		{name: "negative_download", body: "upload_bytes_per_sec: 0\n  download_bytes_per_sec: -1", want: "transfer_limit.download_bytes_per_sec"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			yaml := fullValidYAML() + `
transfer_limit:
  ` + tc.body + `
`
			_, err := loadConfigFromStringErr(t, yaml)
			require.Error(t, err)
			require.ErrorContains(t, err, tc.want)
		})
	}
}
```

- [ ] **Step 2: Run config tests and verify they fail**

Run: `rtk go test ./internal/config -run 'TestTransferLimit' -v`

Expected: FAIL because `Config.TransferLimit` does not exist yet.

- [ ] **Step 3: Add config fields**

In `internal/config/config.go`, add this field to `Config` after `IndustryKnowledge`:

```go
	// TransferLimit 描述 manager 面向浏览器和外部系统的文件传输单请求限速配置。
	TransferLimit TransferLimitConfig `yaml:"transfer_limit"`
```

In the same file, add this type near `IndustryKnowledgeConfig`:

```go
// TransferLimitConfig 描述 manager HTTP 文件传输的单请求限速配置。
type TransferLimitConfig struct {
	// UploadBytesPerSec 是单个上传请求从客户端读入 manager 的最大字节每秒；0 表示不限速。
	UploadBytesPerSec int64 `yaml:"upload_bytes_per_sec"`
	// DownloadBytesPerSec 是单个下载请求从 manager 写给客户端的最大字节每秒；0 表示不限速。
	DownloadBytesPerSec int64 `yaml:"download_bytes_per_sec"`
}
```

- [ ] **Step 4: Add validation without defaults**

In `internal/config/loader.go`, inside `Validate()` after industry knowledge validation, add:

```go
	if err := c.TransferLimit.validate(); err != nil {
		return err
	}
```

In the same file, add this method near `IndustryKnowledgeConfig.validate()`:

```go
// validate 校验文件传输限速配置；0 表示不限速，负数没有业务含义。
func (c TransferLimitConfig) validate() error {
	if c.UploadBytesPerSec < 0 {
		return fmt.Errorf("transfer_limit.upload_bytes_per_sec 不能小于 0")
	}
	if c.DownloadBytesPerSec < 0 {
		return fmt.Errorf("transfer_limit.download_bytes_per_sec 不能小于 0")
	}
	return nil
}
```

Do not add any defaulting in `applyDefaults()`.

- [ ] **Step 5: Run config tests and verify they pass**

Run: `rtk go test ./internal/config -run 'TestTransferLimit|TestLoad_AcceptsValidConfig|TestLoad_RejectsUnknownFields' -v`

Expected: PASS.

- [ ] **Step 6: Commit config contract**

```bash
rtk git add internal/config/config.go internal/config/loader.go internal/config/loader_test.go
rtk git commit -m "feat(config): 增加文件传输限速配置" -m "新增 transfer_limit 配置段，支持上传和下载单请求字节每秒速率。\n\n未配置或配置 0 时保持不限速，负数配置在启动校验阶段拒绝。"
```

---

### Task 2: Build Transfer Limit Helper

**Files:**
- Create: `internal/api/handlers/transfer_limit.go`
- Create: `internal/api/handlers/transfer_limit_test.go`
- Possibly Modify: `go.mod`

- [ ] **Step 1: Write helper tests**

Create `internal/api/handlers/transfer_limit_test.go`:

```go
package handlers

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTransferLimitLeavesUploadBodyUnchangedWhenDisabled 验证限速为 0 时不包装请求体，保持历史上传路径无额外行为。
func TestTransferLimitLeavesUploadBodyUnchangedWhenDisabled(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := io.NopCloser(bytes.NewBufferString("content"))
	c.Request = httptest.NewRequest(http.MethodPost, "/upload", body)

	TransferLimitConfig{}.limitUploadBody(c)

	assert.Same(t, body, c.Request.Body)
}

// TestTransferLimitWrapsUploadBodyWhenEnabled 验证配置上传限速后 handler 会把请求体替换为限速 reader。
func TestTransferLimitWrapsUploadBodyWhenEnabled(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/upload", bytes.NewBufferString("content"))

	TransferLimitConfig{UploadBytesPerSec: 1 << 20}.limitUploadBody(c)

	_, ok := c.Request.Body.(*rateLimitedReadCloser)
	assert.True(t, ok)
}

// TestTransferLimitReadCloserPreservesData 验证限速 reader 不改变读取到的字节内容。
func TestTransferLimitReadCloserPreservesData(t *testing.T) {
	stream := newRateLimitedReadCloser(
		t.Context(),
		io.NopCloser(bytes.NewBufferString("abcdef")),
		1<<20,
	)

	got, err := io.ReadAll(stream)
	require.NoError(t, err)
	require.NoError(t, stream.Close())
	assert.Equal(t, "abcdef", string(got))
}

// TestTransferLimitLeavesDownloadStreamUnchangedWhenDisabled 验证下载限速为 0 时复用原始 stream。
func TestTransferLimitLeavesDownloadStreamUnchangedWhenDisabled(t *testing.T) {
	stream := io.NopCloser(bytes.NewBufferString("content"))

	got := (TransferLimitConfig{}).limitDownloadStream(t.Context(), stream)

	assert.Same(t, stream, got)
}

// TestTransferLimitWrapsDownloadStreamWhenEnabled 验证配置下载限速后统一下载路径会使用限速 reader。
func TestTransferLimitWrapsDownloadStreamWhenEnabled(t *testing.T) {
	stream := io.NopCloser(bytes.NewBufferString("content"))

	got := (TransferLimitConfig{DownloadBytesPerSec: 1 << 20}).limitDownloadStream(t.Context(), stream)

	_, ok := got.(*rateLimitedReadCloser)
	assert.True(t, ok)
}
```

- [ ] **Step 2: Run helper tests and verify they fail**

Run: `rtk go test ./internal/api/handlers -run 'TestTransferLimit' -v`

Expected: FAIL because `TransferLimitConfig` and `rateLimitedReadCloser` do not exist.

- [ ] **Step 3: Implement transfer limit helper**

Create `internal/api/handlers/transfer_limit.go`:

```go
package handlers

import (
	"context"
	"io"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

const transferLimitBurstBytes int64 = 32 * 1024

// TransferLimitConfig 是 handler 层使用的单请求传输限速配置；0 表示对应方向不限速。
type TransferLimitConfig struct {
	// UploadBytesPerSec 限制客户端上传到 manager 的单请求读取速率。
	UploadBytesPerSec int64
	// DownloadBytesPerSec 限制 manager 下载响应写给客户端的单请求读取速率。
	DownloadBytesPerSec int64
}

// limitUploadBody 在上传入口把请求体替换为限速 reader；未启用限速时不改变请求体。
func (c TransferLimitConfig) limitUploadBody(ctx *gin.Context) {
	if c.UploadBytesPerSec <= 0 || ctx.Request == nil || ctx.Request.Body == nil {
		return
	}
	ctx.Request.Body = newRateLimitedReadCloser(ctx.Request.Context(), ctx.Request.Body, c.UploadBytesPerSec)
}

// limitDownloadStream 在下载入口包装原始文件流；未启用限速时直接返回原始流。
func (c TransferLimitConfig) limitDownloadStream(ctx context.Context, stream io.ReadCloser) io.ReadCloser {
	if c.DownloadBytesPerSec <= 0 || stream == nil {
		return stream
	}
	return newRateLimitedReadCloser(ctx, stream, c.DownloadBytesPerSec)
}

// rateLimitedReadCloser 基于读取字节数等待 token，不缓存整文件，适合上传 body 和下载 stream 复用。
type rateLimitedReadCloser struct {
	ctx          context.Context
	stream       io.ReadCloser
	limiter      *rate.Limiter
	burstBytes   int
	bytesPerSec  int64
}

// newRateLimitedReadCloser 构造限速流。bytesPerSec 必须为正数；调用方负责在配置校验阶段拒绝负数。
func newRateLimitedReadCloser(ctx context.Context, stream io.ReadCloser, bytesPerSec int64) io.ReadCloser {
	burst := transferBurst(bytesPerSec)
	return &rateLimitedReadCloser{
		ctx:         ctx,
		stream:      stream,
		limiter:     rate.NewLimiter(rate.Limit(bytesPerSec), burst),
		burstBytes:  burst,
		bytesPerSec: bytesPerSec,
	}
}

func (r *rateLimitedReadCloser) Read(p []byte) (int, error) {
	n, err := r.stream.Read(p)
	if n > 0 {
		if waitErr := r.wait(n); waitErr != nil {
			return n, waitErr
		}
	}
	return n, err
}

func (r *rateLimitedReadCloser) Close() error {
	return r.stream.Close()
}

func (r *rateLimitedReadCloser) wait(n int) error {
	remaining := n
	for remaining > 0 {
		chunk := remaining
		if chunk > r.burstBytes {
			chunk = r.burstBytes
		}
		if err := r.limiter.WaitN(r.ctx, chunk); err != nil {
			return err
		}
		remaining -= chunk
	}
	return nil
}

func transferBurst(bytesPerSec int64) int {
	if bytesPerSec <= 1 {
		return 1
	}
	if bytesPerSec < transferLimitBurstBytes {
		return int(bytesPerSec)
	}
	return int(transferLimitBurstBytes)
}
```

- [ ] **Step 4: Run helper tests and go mod tidy**

Run: `rtk go test ./internal/api/handlers -run 'TestTransferLimit' -v`

Expected: PASS.

Run: `rtk go mod tidy`

Expected: completes. If `go.mod` changes `golang.org/x/time` from indirect to direct, keep that diff.

- [ ] **Step 5: Commit transfer helper**

```bash
rtk git add internal/api/handlers/transfer_limit.go internal/api/handlers/transfer_limit_test.go go.mod go.sum
rtk git commit -m "feat(api): 增加文件传输限速 reader" -m "新增 handler 层通用 TransferLimitConfig 与限速 ReadCloser。\n\n限速基于 golang.org/x/time/rate，按读取字节数等待 token，保持上传 body 和下载 stream 的流式处理。"
```

---

### Task 3: Wire Limits Into Knowledge Handlers

**Files:**
- Modify: `internal/api/handlers/knowledge.go`
- Modify: `internal/api/handlers/knowledge_test.go`
- Modify: `internal/api/handlers/industry_knowledge.go`
- Modify: `internal/api/handlers/industry_knowledge_test.go`
- Modify: `internal/api/router.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Write knowledge handler tests for upload and download limit wiring**

In `internal/api/handlers/knowledge_test.go`, extend `knowledgeServiceStub`:

```go
	saveOrgBodyType string
```

Replace `SaveOrgFile` with this version:

```go
func (s *knowledgeServiceStub) SaveOrgFile(_ context.Context, _ auth.Principal, _, _ string, content io.Reader, _ int64) (service.KnowledgeDocumentResult, error) {
	s.saveOrgCalls++
	s.saveOrgBodyType = fmt.Sprintf("%T", content)
	return s.saveOrgResult, s.saveOrgErr
}
```

Add this router helper below `newKnowledgeTestRouter`:

```go
func newKnowledgeTestRouterWithTransferLimit(t *testing.T, svc knowledgeService, limit TransferLimitConfig) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterKnowledgeRoutes(router, NewKnowledgeHandler(svc, limit))
	return router
}
```

Add these tests:

```go
// TestKnowledgeUploadOrgAppliesUploadRateLimit 验证企业知识库上传在进入 service 前会按配置包装请求体。
func TestKnowledgeUploadOrgAppliesUploadRateLimit(t *testing.T) {
	stub := &knowledgeServiceStub{saveOrgResult: service.KnowledgeDocumentResult{ID: "doc-1", Name: "report.md"}}
	router := newKnowledgeTestRouterWithTransferLimit(t, stub, TransferLimitConfig{UploadBytesPerSec: 1 << 20})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge?filename=report.md", bytes.NewBufferString("content"))
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, 1, stub.saveOrgCalls)
	assert.Contains(t, stub.saveOrgBodyType, "rateLimitedReadCloser")
}

// TestKnowledgeDownloadOrgKeepsResponseHeadersWithRateLimit 验证下载限速不改变文件名、长度和响应体契约。
func TestKnowledgeDownloadOrgKeepsResponseHeadersWithRateLimit(t *testing.T) {
	stub := &knowledgeServiceStub{openContent: "hello", openSize: 5, openName: "report.md"}
	router := newKnowledgeTestRouterWithTransferLimit(t, stub, TransferLimitConfig{DownloadBytesPerSec: 1 << 20})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/doc-1/file", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "attachment; filename=report.md", w.Header().Get("Content-Disposition"))
	assert.Equal(t, "5", w.Header().Get("Content-Length"))
	assert.Equal(t, "hello", w.Body.String())
	assert.Equal(t, 1, stub.openCloses)
}
```

- [ ] **Step 2: Write industry handler tests for platform and external upload limit wiring**

In `internal/api/handlers/industry_knowledge_test.go`, add fields to `industryKnowledgeServiceStub`:

```go
	saveBodyType           string
```

In `SaveIndustryFile`, after `s.saveSize = size`, add:

```go
	s.saveBodyType = fmt.Sprintf("%T", content)
```

Add `fmt` to the imports.

Change `newIndustryKnowledgeTestRouter` to accept optional limits:

```go
func newIndustryKnowledgeTestRouter(t *testing.T, svc industryKnowledgeService, uploadToken string, limits ...TransferLimitConfig) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	handler := NewIndustryKnowledgeHandler(svc, uploadToken, limits...)
	RegisterExternalIndustryKnowledgeRoutes(router, handler)
	RegisterIndustryKnowledgeRoutes(router, handler)
	return router
}
```

Add these tests:

```go
// TestIndustryKnowledgeUploadAppliesUploadRateLimit 验证平台行业库上传在进入 service 前使用上传限速 reader。
func TestIndustryKnowledgeUploadAppliesUploadRateLimit(t *testing.T) {
	stub := &industryKnowledgeServiceStub{saveResult: service.KnowledgeDocumentResult{ID: "doc-1", Name: "policy.pdf"}}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token", TransferLimitConfig{UploadBytesPerSec: 1 << 20})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/industry-knowledge-bases/industry-1/knowledge?filename=policy.pdf", bytes.NewBufferString("content"))
	req.Header.Set("Content-Type", "application/octet-stream")
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, 1, stub.saveCalls)
	assert.Contains(t, stub.saveBodyType, "rateLimitedReadCloser")
}

// TestExternalIndustryUploadParsesMultipartWithUploadRateLimit 验证外部行业库 multipart 上传在限速开启时仍能正常解析文件。
func TestExternalIndustryUploadParsesMultipartWithUploadRateLimit(t *testing.T) {
	stub := &industryKnowledgeServiceStub{saveResult: service.KnowledgeDocumentResult{ID: "doc-1", Name: "policy.pdf"}}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token", TransferLimitConfig{UploadBytesPerSec: 1 << 20})

	body, contentType := multipartIndustryUploadBody(t, "保险", "policy.pdf", "content")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/external/industry-knowledge/files", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-OC-Industry-Knowledge-Token", "secret-token")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, 1, stub.externalUploadCalls)
	assert.Equal(t, "保险", stub.externalIndustryName)
	assert.Equal(t, "policy.pdf", stub.externalFilename)
	assert.Equal(t, "content", stub.externalContent)
}
```

- [ ] **Step 3: Run handler tests and verify they fail**

Run: `rtk go test ./internal/api/handlers -run 'TestKnowledge.*RateLimit|TestIndustryKnowledge.*RateLimit|TestExternalIndustryUploadParsesMultipartWithUploadRateLimit' -v`

Expected: FAIL because handlers do not accept or use transfer limit config yet.

- [ ] **Step 4: Update `KnowledgeHandler`**

In `internal/api/handlers/knowledge.go`, update the struct:

```go
type KnowledgeHandler struct {
	service       knowledgeService
	transferLimit TransferLimitConfig
}
```

Replace constructor:

```go
func NewKnowledgeHandler(svc knowledgeService, limits ...TransferLimitConfig) *KnowledgeHandler {
	var limit TransferLimitConfig
	if len(limits) > 0 {
		limit = limits[0]
	}
	return &KnowledgeHandler{service: svc, transferLimit: limit}
}
```

In `SaveOrg`, after `prepareKnowledgeOctetStreamUpload` succeeds and before `SaveOrgFile`, add:

```go
	h.transferLimit.limitUploadBody(c)
```

In `SaveApp`, after `prepareKnowledgeOctetStreamUpload` succeeds and before `SaveAppFile`, add:

```go
	h.transferLimit.limitUploadBody(c)
```

In `DownloadOrg`, replace the write call with:

```go
	writeKnowledgeDownload(c, filename, reader, size, h.transferLimit)
```

In `DownloadApp`, replace the write call with:

```go
	writeKnowledgeDownload(c, filename, reader, size, h.transferLimit)
```

Replace `writeKnowledgeDownload` signature and body:

```go
// writeKnowledgeDownload 负责设置下载响应头、写出二进制流，并统一接管流关闭。
func writeKnowledgeDownload(c *gin.Context, filename string, stream io.ReadCloser, size int64, limits ...TransferLimitConfig) {
	var limit TransferLimitConfig
	if len(limits) > 0 {
		limit = limits[0]
	}
	stream = limit.limitDownloadStream(c.Request.Context(), stream)
	defer stream.Close()
	c.Header("Content-Type", "application/octet-stream")
	if size >= 0 {
		c.Header("Content-Length", strconv.FormatInt(size, 10))
	}
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(filename)}))
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, stream); err != nil {
		c.Error(err)
	}
}
```

- [ ] **Step 5: Update `IndustryKnowledgeHandler`**

In `internal/api/handlers/industry_knowledge.go`, update the struct:

```go
type IndustryKnowledgeHandler struct {
	service       industryKnowledgeService
	uploadToken   string
	transferLimit TransferLimitConfig
}
```

Replace constructor:

```go
func NewIndustryKnowledgeHandler(svc industryKnowledgeService, uploadToken string, limits ...TransferLimitConfig) *IndustryKnowledgeHandler {
	var limit TransferLimitConfig
	if len(limits) > 0 {
		limit = limits[0]
	}
	return &IndustryKnowledgeHandler{service: svc, uploadToken: uploadToken, transferLimit: limit}
}
```

In `ExternalUpload`, after `MaxBytesReader` and before `ParseMultipartForm`, add:

```go
	h.transferLimit.limitUploadBody(c)
```

In `SaveFile`, after `prepareKnowledgeOctetStreamUpload` succeeds and before `SaveIndustryFile`, add:

```go
	h.transferLimit.limitUploadBody(c)
```

In `DownloadFile`, replace the write call with:

```go
	writeKnowledgeDownload(c, filename, reader, size, h.transferLimit)
```

- [ ] **Step 6: Wire router dependency**

In `internal/api/router.go`, add this field to `Dependencies` after `IndustryKnowledgeUploadToken`:

```go
	// TransferLimit 是 manager 文件上传下载的单请求限速配置；零值表示不限速。
	TransferLimit handlers.TransferLimitConfig
```

Update the external industry handler construction:

```go
		industryHandler := handlers.NewIndustryKnowledgeHandler(dep.KnowledgeService, dep.IndustryKnowledgeUploadToken, dep.TransferLimit)
```

Update the user knowledge handlers:

```go
		knowledgeHandler := handlers.NewKnowledgeHandler(dep.KnowledgeService, dep.TransferLimit)
		industryHandler := handlers.NewIndustryKnowledgeHandler(dep.KnowledgeService, dep.IndustryKnowledgeUploadToken, dep.TransferLimit)
```

Do not pass `dep.TransferLimit` to `NewRuntimeKnowledgeHandler`.

- [ ] **Step 7: Wire server config**

In `cmd/server/main.go`, add an import alias because `handlers` is already used for worker handlers:

```go
	apihandlers "oc-manager/internal/api/handlers"
```

Before constructing `http.Server`, add:

```go
		transferLimit := apihandlers.TransferLimitConfig{
			UploadBytesPerSec:   cfg.TransferLimit.UploadBytesPerSec,
			DownloadBytesPerSec: cfg.TransferLimit.DownloadBytesPerSec,
		}
```

In `api.Dependencies{...}`, add:

```go
				TransferLimit:                transferLimit,
```

Place it near `IndustryKnowledgeUploadToken`.

- [ ] **Step 8: Run handler tests**

Run: `rtk go test ./internal/api/handlers -run 'TestTransferLimit|TestKnowledge.*RateLimit|TestKnowledgeDownloadOrgUsesServiceFilename|TestIndustryKnowledge.*RateLimit|TestExternalIndustryUpload' -v`

Expected: PASS.

- [ ] **Step 9: Run router/main package tests**

Run: `rtk go test ./internal/api ./cmd/server -v`

Expected: PASS.

- [ ] **Step 10: Commit handler wiring**

```bash
rtk git add internal/api/handlers/knowledge.go internal/api/handlers/knowledge_test.go internal/api/handlers/industry_knowledge.go internal/api/handlers/industry_knowledge_test.go internal/api/router.go cmd/server/main.go
rtk git commit -m "feat(api): 接入知识库文件单请求限速" -m "将 transfer_limit 注入知识库和行业知识库 handler。\n\n上传在进入 service 前包装请求体，下载在统一 writeKnowledgeDownload 路径包装文件流；runtime 内部知识库上传保持不受影响。"
```

---

### Task 4: Update Examples and Documentation

**Files:**
- Modify: `config/manager.example.yaml`
- Modify: `deploy/k8s/prod/secret.example.yaml`
- Modify: `docs/configuration.md`

- [ ] **Step 1: Add manager example config**

In `config/manager.example.yaml`, add this block after `industry_knowledge`:

```yaml
transfer_limit:
  # 单个文件上传请求从客户端读入 manager 的最大字节每秒；0 表示不限速。
  # 524288 = 512KB/s。该限速只保护浏览器/外部系统 -> manager-api 链路。
  upload_bytes_per_sec: 524288

  # 单个文件下载请求从 manager 写给客户端的最大字节每秒；0 表示不限速。
  # 524288 = 512KB/s。多个并发请求会各自按该速率限速。
  download_bytes_per_sec: 524288
```

- [ ] **Step 2: Add production Secret example config**

In `deploy/k8s/prod/secret.example.yaml`, add the same `transfer_limit` block inside the embedded `manager.yaml`, after `industry_knowledge` if present, or after `ragflow` if the file orders config that way:

```yaml
    transfer_limit:
      # 单请求上传限速，单位字节/秒；524288 = 512KB/s，0 表示不限速。
      upload_bytes_per_sec: 524288
      # 单请求下载限速，单位字节/秒；多个并发请求会分别限速。
      download_bytes_per_sec: 524288
```

- [ ] **Step 3: Document configuration**

In `docs/configuration.md`, add a subsection after the industry knowledge config section. Use `### 1.9 transfer_limit` and renumber the existing `### 1.9 hermes` section to `### 1.10 hermes`:

````markdown
### 1.9 `transfer_limit`

`transfer_limit` 控制 manager-api 面向浏览器和外部系统的文件传输单请求限速。它只限制客户端到 manager-api 的上传读取，以及 manager-api 到客户端的下载响应，不限制 manager-api 到 RAGFlow 的内部传输。

```yaml
transfer_limit:
  upload_bytes_per_sec: 524288
  download_bytes_per_sec: 524288
```

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---:|---|
| `upload_bytes_per_sec` | int64 | `0` | 单个上传请求的最大读取速率，单位字节/秒；`524288` 表示 512KB/s；`0` 表示不限速；负数启动 fail-fast |
| `download_bytes_per_sec` | int64 | `0` | 单个下载请求的最大响应速率，单位字节/秒；`524288` 表示 512KB/s；`0` 表示不限速；负数启动 fail-fast |

该配置是单请求限速，不是 manager-api 进程总带宽或集群总带宽。多个并发上传/下载请求会分别按该速率限制，总带宽会叠加。当前覆盖企业知识库、实例知识库、行业知识库的上传下载，以及外部行业库上传；不覆盖 runtime 内部知识库上传。
````

- [ ] **Step 4: Run documentation/config checks**

Run: `rtk go test ./internal/config -run 'TestTransferLimit' -v`

Expected: PASS.

Run: `rtk rg -n "transfer_limit|upload_bytes_per_sec|download_bytes_per_sec" config/manager.example.yaml deploy/k8s/prod/secret.example.yaml docs/configuration.md`

Expected: all three files contain the new keys and explanatory text.

- [ ] **Step 5: Commit docs and examples**

```bash
rtk git add config/manager.example.yaml deploy/k8s/prod/secret.example.yaml docs/configuration.md
rtk git commit -m "docs(config): 说明文件传输限速配置" -m "在 manager 示例配置、生产 Secret 示例和配置文档中加入 transfer_limit。\n\n示例值为 524288 字节/秒，文档明确 0 表示不限速、负数非法以及限速粒度为单请求。"
```

---

### Task 5: Final Verification

**Files:**
- No planned code changes. This task validates the full feature.

- [ ] **Step 1: Run focused Go tests**

Run:

```bash
rtk go test ./internal/config ./internal/api/handlers ./internal/api ./cmd/server -v
```

Expected: PASS.

- [ ] **Step 2: Run broader backend tests affected by imports and routing**

Run:

```bash
rtk go test ./internal/... ./cmd/server -v
```

Expected: PASS.

- [ ] **Step 3: Confirm OpenAPI and frontend generated types are untouched**

Run:

```bash
rtk git diff --name-only
```

Expected: no `openapi/openapi.yaml` or `web/src/api/generated.ts` unless an unrelated pre-existing user change exists.

- [ ] **Step 4: Optional local manual rate check**

Only run this if a local manager API with RAGFlow is already available. Use a test config with a very low limit such as `upload_bytes_per_sec: 1024` and upload a small file from the browser or with an authenticated request. Verify the request takes visibly longer than the same upload with `0`.

Expected: manual timing shows throttling. If the local RAGFlow stack is not running, skip this step and record that only automated tests were run.

- [ ] **Step 5: Final status check**

Run:

```bash
rtk git status --short
rtk git log --oneline -n 5
```

Expected: working tree is clean after commits, or only intentional uncommitted changes are present and listed in the handoff.

---

## Self-Review Checklist

- Spec coverage:
  - Config default `0` and negative validation: Task 1.
  - Single-request upload/download helper: Task 2.
  - Enterprise/app/industry upload/download plus external upload: Task 3.
  - Runtime internal upload excluded: Task 3 router/handler instructions and tests.
  - Example value `524288`: Task 4.
  - No OpenAPI/frontend changes: Task 5.
- Placeholder scan:
  - No placeholder markers or undefined future steps.
- Type consistency:
  - `TransferLimitConfig` in handlers has `UploadBytesPerSec` and `DownloadBytesPerSec`.
  - Config model uses the same field names with YAML keys `upload_bytes_per_sec` and `download_bytes_per_sec`.
  - Router dependency passes `handlers.TransferLimitConfig` into both knowledge handlers, but not runtime handler.
