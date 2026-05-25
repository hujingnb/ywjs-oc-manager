# Knowledge Download Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add protected single-file download for both organization and app knowledge bases for every user who already has read access.

**Architecture:** Lists and downloads both read from the manager-side knowledge master copy. Backend download methods reuse existing knowledge read permissions and `KnowledgeMaster.Open`; frontend download helpers mirror the existing workspace Blob download pattern. The UI adds file-only download actions without changing upload, delete, directory navigation, or sync behavior.

**Tech Stack:** Go, Gin, testify, Vue 3, TanStack Query, Naive UI, Vitest, OpenAPI via swag and openapi-typescript.

---

## File Structure

- `internal/files/knowledge_master.go`
  - Owns direct file-system reads from the manager knowledge master copy.
  - Add a guard so `Open` rejects directories and only returns regular files.
- `internal/files/knowledge_master_test.go`
  - Covers the direct file-open boundary that download uses.
- `internal/service/knowledge_service.go`
  - Adds service-level download/open methods for org and app knowledge.
  - Keeps all role/resource authorization in `internal/auth/authorizer.go` by reusing existing predicates.
- `internal/service/knowledge_service_test.go`
  - Covers download permissions and path rejection at service level.
- `internal/api/handlers/knowledge.go`
  - Adds HTTP routes and streaming handlers.
  - Sets binary response headers and streams from `io.ReadCloser`.
- `internal/api/handlers/knowledge_test.go`
  - Covers HTTP contract for success and missing query parameters.
- `web/src/api/hooks/useKnowledge.ts`
  - Adds token-authenticated Blob download helpers.
- `web/src/api/hooks/useKnowledge.spec.ts`
  - Covers URL construction, Authorization, and browser download trigger.
- `web/src/pages/knowledge/OrgKnowledgePage.vue`
  - Adds file download actions for all readable org knowledge users.
  - Keeps delete actions restricted to `canManage`.
- `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`
  - Covers org member download visibility without delete visibility.
- `web/src/pages/apps/AppKnowledgeTab.vue`
  - Adds file download actions for app knowledge.
  - Keeps delete actions restricted to `canManage`.
- `web/src/pages/apps/AppKnowledgeTab.spec.ts`
  - Covers read-only users getting download without delete.
- `openapi/openapi.yaml`
  - Generated from swag annotations.
- `web/src/api/generated.ts`
  - Generated from `openapi/openapi.yaml`.
- `docs/user-manual.md`
  - Documents knowledge download behavior for organization and member views.
- `docs/product-design.md`
  - Updates permission wording from read-only to read/download where relevant.

---

### Task 1: Reject Directory Opens In Knowledge Master

**Files:**
- Modify: `internal/files/knowledge_master.go`
- Modify: `internal/files/knowledge_master_test.go`

- [ ] **Step 1: Write the failing directory-open test**

Add this test to `internal/files/knowledge_master_test.go` after `TestKnowledgeMasterListReturnsSortedEntries`:

```go
// TestKnowledgeMasterOpenRejectsDirectory 验证下载入口拒绝目录路径，避免把目录句柄当作文件流返回。
func TestKnowledgeMasterOpenRejectsDirectory(t *testing.T) {
	master := newMaster(t, 1024)
	err := os.MkdirAll(filepath.Join(master.root.Root, "docs"), 0o755)
	require.NoError(t, err)

	stream, size, err := master.Open("docs")
	require.ErrorIs(t, err, ErrPathNotRegular)
	require.Nil(t, stream)
	require.Equal(t, int64(0), size)
}
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run:

```bash
rtk go test ./internal/files -run TestKnowledgeMasterOpenRejectsDirectory -count=1
```

Expected: `FAIL`; the error is nil because `KnowledgeMaster.Open` currently accepts directories.

- [ ] **Step 3: Implement regular-file validation in `KnowledgeMaster.Open`**

Replace the current `Open` method in `internal/files/knowledge_master.go` with:

```go
// Open 打开主副本中的指定普通文件供读取。
// 关闭返回的 ReadCloser 由调用方负责；目录和非常规文件会被拒绝，避免下载接口返回非文件流。
func (m *KnowledgeMaster) Open(relative string) (io.ReadCloser, int64, error) {
	if relative == "" {
		return nil, 0, ErrKnowledgePathRequired
	}
	resolved, err := m.root.Resolve(relative)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(resolved)
	if err != nil {
		return nil, 0, fmt.Errorf("打开知识库文件失败: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, fmt.Errorf("查询知识库文件大小失败: %w", err)
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, 0, fmt.Errorf("%w: %s", ErrPathNotRegular, relative)
	}
	return f, info.Size(), nil
}
```

- [ ] **Step 4: Run files tests**

Run:

```bash
rtk go test ./internal/files -count=1
```

Expected: `ok  	oc-manager/internal/files`.

- [ ] **Step 5: Commit the file-boundary change**

```bash
rtk git add internal/files/knowledge_master.go internal/files/knowledge_master_test.go
rtk git commit -m "fix(files): 拒绝打开知识库目录" -m "下载接口只应返回普通文件流。" -m "在 KnowledgeMaster.Open 中拒绝目录路径，并补充目录打开拒绝用例。"
```

---

### Task 2: Add Service-Level Knowledge Open Methods

**Files:**
- Modify: `internal/service/knowledge_service.go`
- Modify: `internal/service/knowledge_service_test.go`

- [ ] **Step 1: Add service tests for download permissions and path validation**

In `internal/service/knowledge_service_test.go`, add `io` to the imports:

```go
import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/files"
)
```

Then add these tests before `TestKnowledgeServiceSaveOrgRequiresOrgManager`:

```go
// TestKnowledgeServiceOpenOrgAllowsOrgMember 验证组织成员可下载本组织组织知识库文件。
func TestKnowledgeServiceOpenOrgAllowsOrgMember(t *testing.T) {
	svc := newKnowledgeService(t)
	require.NoError(t, svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "docs/readme.md", strings.NewReader("hello"), 5))

	member := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "member-1"}
	stream, size, err := svc.OpenOrgFile(context.Background(), member, testKnowledgeOrg, "docs/readme.md")
	require.NoError(t, err)
	defer stream.Close()
	body, err := io.ReadAll(stream)
	require.NoError(t, err)

	assert.Equal(t, int64(5), size)
	assert.Equal(t, "hello", string(body))
}

// TestKnowledgeServiceOpenOrgAllowsPlatformAdmin 验证平台管理员沿用组织知识库读取权限下载任意组织文件。
func TestKnowledgeServiceOpenOrgAllowsPlatformAdmin(t *testing.T) {
	svc := newKnowledgeService(t)
	require.NoError(t, svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "policy.md", strings.NewReader("policy"), 6))

	stream, size, err := svc.OpenOrgFile(context.Background(), platformAdmin(), testKnowledgeOrg, "policy.md")
	require.NoError(t, err)
	defer stream.Close()
	body, err := io.ReadAll(stream)
	require.NoError(t, err)

	assert.Equal(t, int64(6), size)
	assert.Equal(t, "policy", string(body))
}

// TestKnowledgeServiceOpenAppAllowsPlatformAdmin 验证平台管理员沿用应用知识库读取权限下载任意实例文件。
func TestKnowledgeServiceOpenAppAllowsPlatformAdmin(t *testing.T) {
	svc := newKnowledgeService(t)
	owner := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}
	require.NoError(t, svc.SaveAppFile(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "app.md", strings.NewReader("app"), 3))

	stream, size, err := svc.OpenAppFile(context.Background(), platformAdmin(), testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "app.md")
	require.NoError(t, err)
	defer stream.Close()
	body, err := io.ReadAll(stream)
	require.NoError(t, err)

	assert.Equal(t, int64(3), size)
	assert.Equal(t, "app", string(body))
}

// TestKnowledgeServiceOpenAppRejectsOtherOwner 验证组织成员不能下载其他成员的实例知识库文件。
func TestKnowledgeServiceOpenAppRejectsOtherOwner(t *testing.T) {
	svc := newKnowledgeService(t)
	owner := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}
	require.NoError(t, svc.SaveAppFile(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "app.md", strings.NewReader("app"), 3))

	stranger := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "stranger"}
	stream, size, err := svc.OpenAppFile(context.Background(), stranger, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "app.md")

	require.ErrorIs(t, err, ErrKnowledgeForbidden)
	require.Nil(t, stream)
	assert.Equal(t, int64(0), size)
}

// TestKnowledgeServiceOpenOrgRejectsEscapingPath 验证下载路径仍受 SafeRoot 边界约束保护。
func TestKnowledgeServiceOpenOrgRejectsEscapingPath(t *testing.T) {
	svc := newKnowledgeService(t)

	stream, size, err := svc.OpenOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "../../secret.md")

	require.Error(t, err)
	require.Nil(t, stream)
	assert.Equal(t, int64(0), size)
}
```

- [ ] **Step 2: Run the focused service tests and verify they fail**

Run:

```bash
rtk go test ./internal/service -run 'TestKnowledgeServiceOpen' -count=1
```

Expected: `FAIL`; `OpenOrgFile` and `OpenAppFile` are undefined.

- [ ] **Step 3: Add service methods**

Add these methods to `internal/service/knowledge_service.go` after `DeleteAppFile` and before `ListOrg`:

```go
// OpenOrgFile 打开组织级知识库中的普通文件供下载。
// 下载属于读取能力，权限沿用 CanReadOrgKnowledge；写入和同步权限不参与判断。
func (s *KnowledgeService) OpenOrgFile(ctx context.Context, principal auth.Principal, orgID, relative string) (io.ReadCloser, int64, error) {
	if s.master == nil {
		return nil, 0, ErrKnowledgeMissing
	}
	if !auth.CanReadOrgKnowledge(principal, orgID) {
		return nil, 0, ErrKnowledgeForbidden
	}
	target := path.Join("org", orgID, "knowledge", relative)
	stream, size, err := s.master.Open(target)
	if err != nil {
		return nil, 0, fmt.Errorf("打开组织知识库文件失败: %w", err)
	}
	return stream, size, nil
}

// OpenAppFile 打开应用级知识库中的普通文件供下载。
// 下载属于读取能力，权限沿用 CanReadAppKnowledge；平台管理员保留跨组织观察和下载能力。
func (s *KnowledgeService) OpenAppFile(ctx context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string) (io.ReadCloser, int64, error) {
	if s.master == nil {
		return nil, 0, ErrKnowledgeMissing
	}
	if !auth.CanReadAppKnowledge(principal, orgID, ownerUserID) {
		return nil, 0, ErrKnowledgeForbidden
	}
	target := path.Join("org", orgID, "app", appID, "knowledge", relative)
	stream, size, err := s.master.Open(target)
	if err != nil {
		return nil, 0, fmt.Errorf("打开应用知识库文件失败: %w", err)
	}
	return stream, size, nil
}
```

- [ ] **Step 4: Run service tests**

Run:

```bash
rtk go test ./internal/service -run 'TestKnowledgeService' -count=1
```

Expected: `ok  	oc-manager/internal/service`.

- [ ] **Step 5: Commit the service change**

```bash
rtk git add internal/service/knowledge_service.go internal/service/knowledge_service_test.go
rtk git commit -m "feat(knowledge): 增加知识库文件读取服务" -m "为组织级和实例级知识库增加只读文件打开方法。" -m "下载权限复用现有读取谓词，并覆盖成员、平台管理员和越界路径场景。"
```

---

### Task 3: Add Knowledge Download HTTP Routes

**Files:**
- Modify: `internal/api/handlers/knowledge.go`
- Modify: `internal/api/handlers/knowledge_test.go`

- [ ] **Step 1: Extend the handler stub and add failing HTTP tests**

In `internal/api/handlers/knowledge_test.go`, extend `knowledgeServiceStub` with download fields:

```go
	openOrgContent string
	openOrgSize    int64
	openOrgErr     error
	openAppContent string
	openAppSize    int64
	openAppErr     error
```

Add these methods below `DeleteOrgFile`:

```go
func (s *knowledgeServiceStub) OpenOrgFile(_ context.Context, _ auth.Principal, _, _ string) (io.ReadCloser, int64, error) {
	if s.openOrgErr != nil {
		return nil, 0, s.openOrgErr
	}
	return io.NopCloser(bytes.NewBufferString(s.openOrgContent)), s.openOrgSize, nil
}
```

Add this method below `DeleteAppFile`:

```go
func (s *knowledgeServiceStub) OpenAppFile(_ context.Context, _ auth.Principal, _, _, _, _ string) (io.ReadCloser, int64, error) {
	if s.openAppErr != nil {
		return nil, 0, s.openAppErr
	}
	return io.NopCloser(bytes.NewBufferString(s.openAppContent)), s.openAppSize, nil
}
```

Add these tests after `TestKnowledgeListOrgHappy`:

```go
// TestKnowledgeDownloadOrgHappy 验证组织知识库下载成功返回二进制内容。
func TestKnowledgeDownloadOrgHappy(t *testing.T) {
	stub := &knowledgeServiceStub{openOrgContent: "hello", openOrgSize: 5}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/file?path=docs/readme.md", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Header().Get("Content-Disposition"), "readme.md")
	assert.Equal(t, "5", w.Header().Get("Content-Length"))
	assert.Equal(t, "hello", w.Body.String())
}

// TestKnowledgeDownloadOrgMissingPath 验证组织知识库下载缺少 path 时返回 400。
func TestKnowledgeDownloadOrgMissingPath(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/file", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
```

Add these tests after `TestKnowledgeListAppHappy`:

```go
// TestKnowledgeDownloadAppHappy 验证实例知识库下载成功返回二进制内容。
func TestKnowledgeDownloadAppHappy(t *testing.T) {
	stub := &knowledgeServiceStub{openAppContent: "app", openAppSize: 3}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/knowledge/file?org_id=org-1&owner_user_id=u1&path=app.md", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Header().Get("Content-Disposition"), "app.md")
	assert.Equal(t, "3", w.Header().Get("Content-Length"))
	assert.Equal(t, "app", w.Body.String())
}

// TestKnowledgeDownloadAppMissingParams 验证实例知识库下载缺少任一必填参数时返回 400。
func TestKnowledgeDownloadAppMissingParams(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{name: "缺少 org_id", url: "/api/v1/apps/app-1/knowledge/file?owner_user_id=u1&path=app.md"}, // 场景：缺少组织归属参数。
		{name: "缺少 owner_user_id", url: "/api/v1/apps/app-1/knowledge/file?org_id=org-1&path=app.md"}, // 场景：缺少应用所有者参数。
		{name: "缺少 path", url: "/api/v1/apps/app-1/knowledge/file?org_id=org-1&owner_user_id=u1"}, // 场景：缺少文件路径参数。
	}
	for _, tc := range cases {
		// 当前子测试覆盖 tc.name 描述的实例知识库下载参数校验场景。
		t.Run(tc.name, func(t *testing.T) {
			stub := &knowledgeServiceStub{}
			router := newKnowledgeTestRouter(t, stub)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}
```

- [ ] **Step 2: Run focused handler tests and verify they fail**

Run:

```bash
rtk go test ./internal/api/handlers -run 'TestKnowledgeDownload' -count=1
```

Expected: `FAIL`; the new `/knowledge/file` routes return 404 because routes and handlers do not exist.

- [ ] **Step 3: Extend the `knowledgeService` interface**

In `internal/api/handlers/knowledge.go`, add these methods to the `knowledgeService` interface:

```go
	OpenOrgFile(ctx context.Context, principal auth.Principal, orgID, relative string) (io.ReadCloser, int64, error)
	OpenAppFile(ctx context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string) (io.ReadCloser, int64, error)
```

- [ ] **Step 4: Add imports required by download headers**

Update the import block in `internal/api/handlers/knowledge.go` so it includes `mime` and `path`:

```go
import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	redactlog "oc-manager/internal/log"
	"oc-manager/internal/service"
)
```

- [ ] **Step 5: Register the download routes**

Update `RegisterKnowledgeRoutes` in `internal/api/handlers/knowledge.go`:

```go
func RegisterKnowledgeRoutes(router gin.IRouter, handler *KnowledgeHandler) {
	orgGroup := router.Group("/api/v1/organizations/:orgId/knowledge")
	orgGroup.GET("", handler.ListOrg)
	orgGroup.GET("/file", handler.DownloadOrg)
	orgGroup.POST("", handler.SaveOrg)
	orgGroup.DELETE("", handler.DeleteOrg)
	orgGroup.GET("/sync-status", handler.GetOrgSyncStatus)
	orgGroup.POST("/sync-status/retry", handler.RetryOrgSync)

	appGroup := router.Group("/api/v1/apps/:appId/knowledge")
	appGroup.GET("", handler.ListApp)
	appGroup.GET("/file", handler.DownloadApp)
	appGroup.POST("", handler.SaveApp)
	appGroup.DELETE("", handler.DeleteApp)
}
```

- [ ] **Step 6: Add download handlers and stream helper**

Add this code after `ListOrg` in `internal/api/handlers/knowledge.go`:

```go
// DownloadOrg 下载组织级知识库单个文件。
//
// @Summary      下载组织级知识库文件
// @Description  从组织知识库主副本下载指定路径的单个文件，返回二进制流
// @Tags         knowledge
// @Produce      application/octet-stream
// @Security     BearerAuth
// @Param        orgId  path      string  true  "组织 ID"
// @Param        path   query     string  true  "文件相对路径"
// @Success      200    {string}  binary  "二进制文件流"
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge/file [get]
func (h *KnowledgeHandler) DownloadOrg(c *gin.Context) {
	principal := principalFromCtx(c)
	relative := c.Query("path")
	if relative == "" {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 path 参数"))
		return
	}
	stream, size, err := h.service.OpenOrgFile(c.Request.Context(), principal, c.Param("orgId"), relative)
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	writeKnowledgeDownload(c, stream, size, relative)
}
```

Add this code after `ListApp`:

```go
// DownloadApp 下载应用级知识库单个文件。
//
// @Summary      下载应用级知识库文件
// @Description  从应用知识库主副本下载指定路径的单个文件；需同时提供 org_id、owner_user_id 和 path
// @Tags         knowledge
// @Produce      application/octet-stream
// @Security     BearerAuth
// @Param        appId         path      string  true  "应用 ID"
// @Param        org_id        query     string  true  "应用所属组织 ID"
// @Param        owner_user_id query     string  true  "应用所有者用户 ID"
// @Param        path          query     string  true  "文件相对路径"
// @Success      200           {string}  binary  "二进制文件流"
// @Failure      400           {object}  ErrorResponse
// @Failure      401           {object}  ErrorResponse
// @Failure      403           {object}  ErrorResponse
// @Failure      503           {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge/file [get]
func (h *KnowledgeHandler) DownloadApp(c *gin.Context) {
	principal := principalFromCtx(c)
	orgID := c.Query("org_id")
	owner := c.Query("owner_user_id")
	relative := c.Query("path")
	if orgID == "" || owner == "" || relative == "" {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 org_id/owner_user_id/path"))
		return
	}
	stream, size, err := h.service.OpenAppFile(c.Request.Context(), principal, orgID, c.Param("appId"), owner, relative)
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	writeKnowledgeDownload(c, stream, size, relative)
}
```

Add this helper before `writeKnowledgeError`:

```go
// writeKnowledgeDownload 统一知识库下载响应头和流式输出。
// Content-Disposition 只使用 basename，避免把知识库内部目录结构当作本地保存路径。
func writeKnowledgeDownload(c *gin.Context, stream io.ReadCloser, size int64, relative string) {
	defer stream.Close()
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", strconv.FormatInt(size, 10))
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		"filename": path.Base(relative),
	}))
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, stream); err != nil {
		return
	}
}
```

- [ ] **Step 7: Run handler tests**

Run:

```bash
rtk go test ./internal/api/handlers -run 'TestKnowledge' -count=1
```

Expected: `ok  	oc-manager/internal/api/handlers`.

- [ ] **Step 8: Run backend knowledge-related tests**

Run:

```bash
rtk go test ./internal/files ./internal/service ./internal/api/handlers -count=1
```

Expected: all three packages pass.

- [ ] **Step 9: Commit the backend HTTP change**

```bash
rtk git add internal/api/handlers/knowledge.go internal/api/handlers/knowledge_test.go
rtk git commit -m "feat(knowledge): 增加知识库下载接口" -m "为组织级和实例级知识库增加单文件下载路由。" -m "接口复用读取权限并以二进制流返回 manager 主副本文件。"
```

---

### Task 4: Regenerate OpenAPI And Frontend Types

**Files:**
- Modify: `openapi/openapi.yaml`
- Modify: `web/src/api/generated.ts`

- [ ] **Step 1: Generate OpenAPI**

Run:

```bash
rtk make openapi-gen
```

Expected: `openapi/openapi.yaml` is regenerated and contains:

```yaml
/organizations/{orgId}/knowledge/file:
```

and:

```yaml
/apps/{appId}/knowledge/file:
```

- [ ] **Step 2: Generate frontend OpenAPI types**

Run:

```bash
rtk make web-types-gen
```

Expected: `web/src/api/generated.ts` is regenerated and contains:

```ts
"/organizations/{orgId}/knowledge/file"
```

and:

```ts
"/apps/{appId}/knowledge/file"
```

- [ ] **Step 3: Check OpenAPI is synchronized**

Run:

```bash
rtk make openapi-check
```

Expected: the command prints `openapi.yaml 与代码同步`.

- [ ] **Step 4: Commit generated API artifacts**

```bash
rtk git add openapi/openapi.yaml web/src/api/generated.ts
rtk git commit -m "chore(openapi): 同步知识库下载接口" -m "根据新增知识库单文件下载路由重新生成 OpenAPI 契约。" -m "同步前端 generated.ts，保持 API 契约和类型一致。"
```

---

### Task 5: Add Frontend Knowledge Download Helpers

**Files:**
- Modify: `web/src/api/hooks/useKnowledge.ts`
- Modify: `web/src/api/hooks/useKnowledge.spec.ts`

- [ ] **Step 1: Add failing download helper tests**

In `web/src/api/hooks/useKnowledge.spec.ts`, replace the imports with:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { clearStoredTokens, setStoredTokens } from '@/api/client'
import {
  KNOWLEDGE_UPLOAD_MAX_BYTES,
  KNOWLEDGE_UPLOAD_MAX_LABEL,
  KNOWLEDGE_UPLOAD_MAX_MESSAGE,
  downloadAppKnowledgeFile,
  downloadOrgKnowledgeFile,
  isKnowledgeUploadTooLarge,
} from './useKnowledge'
```

Add this setup above the first `describe`:

```ts
let clickSpy: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  setStoredTokens({ accessToken: 'access-1', refreshToken: 'refresh-1' })
  Object.defineProperty(URL, 'createObjectURL', {
    value: vi.fn(() => 'blob:knowledge'),
    configurable: true,
  })
  Object.defineProperty(URL, 'revokeObjectURL', {
    value: vi.fn(),
    configurable: true,
  })
  clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
})

afterEach(() => {
  clearStoredTokens()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})
```

Add this test block after the upload-size tests:

```ts
describe('知识库文件下载', () => {
  // 覆盖组织知识库下载工具：路径参数需要 URL 编码，且受保护接口必须携带 Bearer token。
  it('请求组织知识库下载接口并触发浏览器下载', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(new Blob(['hello']), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    await downloadOrgKnowledgeFile('org-1', 'docs/read me.md', 'read me.md')

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/organizations/org-1/knowledge/file?path=docs%2Fread+me.md', {
      headers: { Authorization: 'Bearer access-1' },
    })
    expect(clickSpy).toHaveBeenCalledTimes(1)
  })

  // 覆盖实例知识库下载工具：实例、组织、所有者和路径共同定位应用级知识库文件。
  it('请求实例知识库下载接口并触发浏览器下载', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(new Blob(['app']), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    await downloadAppKnowledgeFile('app-1', 'org-1', 'user-1', 'docs/app.md', 'app.md')

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/apps/app-1/knowledge/file?org_id=org-1&owner_user_id=user-1&path=docs%2Fapp.md',
      { headers: { Authorization: 'Bearer access-1' } },
    )
    expect(clickSpy).toHaveBeenCalledTimes(1)
  })
})
```

- [ ] **Step 2: Run the focused frontend tests and verify they fail**

Run:

```bash
rtk sh -c 'cd web && npm test -- --run src/api/hooks/useKnowledge.spec.ts'
```

Expected: `FAIL`; `downloadOrgKnowledgeFile` and `downloadAppKnowledgeFile` are not exported.

- [ ] **Step 3: Add authenticated Blob download helpers**

In `web/src/api/hooks/useKnowledge.ts`, update the client import:

```ts
import { apiRequest, getStoredAccessToken } from '@/api/client'
```

Add this code after `isKnowledgeUploadTooLarge`:

```ts
// downloadKnowledgeBlob 负责把受保护知识库下载接口返回的二进制内容转成浏览器下载。
// 下载接口是 GET，但仍需要 Authorization，不能用裸 a.href 直接访问。
async function downloadKnowledgeBlob(url: string, fileName: string): Promise<void> {
  const headers: Record<string, string> = {}
  const token = getStoredAccessToken()
  if (token) headers.Authorization = `Bearer ${token}`
  const response = await fetch(url, { headers })
  if (!response.ok) {
    const text = await response.text().catch(() => '')
    throw new Error(text || '下载失败')
  }
  const blob = await response.blob()
  const objectUrl = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = objectUrl
  link.download = fileName
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(objectUrl)
}

// downloadOrgKnowledgeFile 下载组织级知识库中的单个普通文件。
export function downloadOrgKnowledgeFile(orgId: string, targetPath: string, fileName: string): Promise<void> {
  const params = new URLSearchParams({ path: targetPath })
  return downloadKnowledgeBlob(`/api/v1/organizations/${orgId}/knowledge/file?${params.toString()}`, fileName)
}

// downloadAppKnowledgeFile 下载实例级知识库中的单个普通文件。
export function downloadAppKnowledgeFile(
  appId: string,
  orgId: string,
  ownerUserId: string,
  targetPath: string,
  fileName: string,
): Promise<void> {
  const params = new URLSearchParams({
    org_id: orgId,
    owner_user_id: ownerUserId,
    path: targetPath,
  })
  return downloadKnowledgeBlob(`/api/v1/apps/${appId}/knowledge/file?${params.toString()}`, fileName)
}
```

- [ ] **Step 4: Run frontend helper tests**

Run:

```bash
rtk sh -c 'cd web && npm test -- --run src/api/hooks/useKnowledge.spec.ts'
```

Expected: the spec passes.

- [ ] **Step 5: Commit frontend download helpers**

```bash
rtk git add web/src/api/hooks/useKnowledge.ts web/src/api/hooks/useKnowledge.spec.ts
rtk git commit -m "feat(knowledge): 增加前端知识库下载工具" -m "为组织级和实例级知识库增加带鉴权的 Blob 下载方法。" -m "覆盖下载 URL、Authorization header 和浏览器下载触发行为。"
```

---

### Task 6: Add Organization Knowledge Download UI

**Files:**
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`

- [ ] **Step 1: Add failing page test for read-only download**

In `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`, update the Vue import:

```ts
import { h, ref } from 'vue'
```

Extend `mocks`:

```ts
const mocks = vi.hoisted(() => ({
  run: vi.fn(),
  warning: vi.fn(),
  mutateAsync: vi.fn(),
  canManage: vi.fn(() => true),
  downloadOrgKnowledgeFile: vi.fn(),
}))
```

Update the permissions mock:

```ts
vi.mock('@/domain/permissions', () => ({
  canManageOrgKnowledge: mocks.canManage,
}))
```

In the `useKnowledge` mock, change the listing to include one file and export the download helper:

```ts
    useOrgKnowledgeQuery: () => ({
      data: ref({ path: '', entries: [{ path: 'docs/readme.md', name: 'readme.md', size: 5, is_dir: false }] }),
      isLoading: ref(false),
      error: ref(null),
    }),
    downloadOrgKnowledgeFile: mocks.downloadOrgKnowledgeFile,
```

Replace the `NDataTable` stub in `mountPage`:

```ts
        NDataTable: {
          props: ['columns', 'data'],
          setup(props: { columns: Array<{ key: string; render?: (row: unknown) => unknown }>; data: unknown[] }) {
            return () => h('div', props.data.flatMap((row) => props.columns.map((column) => h('div', { class: `cell-${column.key}` }, [
              column.render ? column.render(row) : '',
            ]))))
          },
        },
```

Add `beforeEach` inside `describe('OrgKnowledgePage', () => {)`:

```ts
  beforeEach(() => {
    mocks.canManage.mockReturnValue(true)
    mocks.downloadOrgKnowledgeFile.mockReset()
    mocks.run.mockReset()
    mocks.warning.mockReset()
    mocks.mutateAsync.mockReset()
  })
```

Add this test:

```ts
  // 覆盖组织成员只读场景：可下载组织知识库文件，但不可看到删除入口。
  it('组织成员可下载组织知识库文件但不可删除', async () => {
    mocks.canManage.mockReturnValue(false)
    const wrapper = mountPage()

    expect(wrapper.text()).toContain('下载')
    expect(wrapper.text()).not.toContain('删除')

    await wrapper.find('button').trigger('click')

    expect(mocks.downloadOrgKnowledgeFile).toHaveBeenCalledWith('org-1', 'docs/readme.md', 'readme.md')
  })
```

- [ ] **Step 2: Run the page test and verify it fails**

Run:

```bash
rtk sh -c 'cd web && npm test -- --run src/pages/knowledge/OrgKnowledgePage.spec.ts'
```

Expected: `FAIL`; there is no download button in the organization knowledge table.

- [ ] **Step 3: Add download import and state**

In `web/src/pages/knowledge/OrgKnowledgePage.vue`, add `downloadOrgKnowledgeFile` to the `useKnowledge` import list:

```ts
  downloadOrgKnowledgeFile,
```

Add download state near the mutation declarations:

```ts
// downloading 标记当前页面正在触发浏览器下载，防止同一页面重复点击下载按钮。
const downloading = ref(false)
```

- [ ] **Step 4: Add organization download handler**

Add this function after `onDelete`:

```ts
// onDownload 下载组织知识库中的单个文件；目录行不调用此函数。
async function onDownload(entry: KnowledgeEntry) {
  if (!effectiveOrgId.value) return
  downloading.value = true
  try {
    await downloadOrgKnowledgeFile(effectiveOrgId.value, entry.path, entry.name)
  } catch (err) {
    message.error(err instanceof Error ? err.message : '下载失败')
  } finally {
    downloading.value = false
  }
}
```

- [ ] **Step 5: Replace the actions column**

Replace the current `actions` column in `fileColumns` with:

```ts
  {
    title: '操作', key: 'actions',
    render: (row) => {
      if (row.is_dir) return null
      const actions = [
        h(NButton, {
          size: 'small',
          disabled: downloading.value,
          onClick: () => onDownload(row),
        }, { default: () => downloading.value ? '下载中…' : '下载' }),
      ]
      if (canManage.value) {
        actions.push(h(NButton, { size: 'small', onClick: () => onDelete(row) }, { default: () => '删除' }))
      }
      return h('div', { style: 'display: flex; gap: 8px; flex-wrap: wrap' }, actions)
    },
  },
```

- [ ] **Step 6: Run organization page tests**

Run:

```bash
rtk sh -c 'cd web && npm test -- --run src/pages/knowledge/OrgKnowledgePage.spec.ts'
```

Expected: the spec passes.

- [ ] **Step 7: Commit organization UI**

```bash
rtk git add web/src/pages/knowledge/OrgKnowledgePage.vue web/src/pages/knowledge/OrgKnowledgePage.spec.ts
rtk git commit -m "feat(knowledge): 增加组织知识库下载入口" -m "组织知识库文件行对所有可读用户显示下载按钮。" -m "删除入口仍由组织知识库管理权限控制，并覆盖组织成员只读下载场景。"
```

---

### Task 7: Add App Knowledge Download UI

**Files:**
- Modify: `web/src/pages/apps/AppKnowledgeTab.vue`
- Modify: `web/src/pages/apps/AppKnowledgeTab.spec.ts`

- [ ] **Step 1: Add failing page test for app knowledge read-only download**

In `web/src/pages/apps/AppKnowledgeTab.spec.ts`, update the Vue import:

```ts
import { h, ref } from 'vue'
```

Extend `mocks`:

```ts
const mocks = vi.hoisted(() => ({
  run: vi.fn(),
  warning: vi.fn(),
  mutateAsync: vi.fn(),
  canManage: vi.fn(() => true),
  downloadAppKnowledgeFile: vi.fn(),
}))
```

Update the permissions mock:

```ts
vi.mock('@/domain/permissions', () => ({
  canManageApp: mocks.canManage,
}))
```

In the `useKnowledge` mock, change the listing and export the download helper:

```ts
    useAppKnowledgeQuery: () => ({
      data: ref({ path: '', entries: [{ path: 'docs/app.md', name: 'app.md', size: 3, is_dir: false }] }),
      isLoading: ref(false),
      error: ref(null),
    }),
    downloadAppKnowledgeFile: mocks.downloadAppKnowledgeFile,
```

Replace the `NDataTable` stub:

```ts
        NDataTable: {
          props: ['columns', 'data'],
          setup(props: { columns: Array<{ key: string; render?: (row: unknown) => unknown }>; data: unknown[] }) {
            return () => h('div', props.data.flatMap((row) => props.columns.map((column) => h('div', { class: `cell-${column.key}` }, [
              column.render ? column.render(row) : '',
            ]))))
          },
        },
```

Add `beforeEach` inside `describe('AppKnowledgeTab', () => {)`:

```ts
  beforeEach(() => {
    mocks.canManage.mockReturnValue(true)
    mocks.downloadAppKnowledgeFile.mockReset()
    mocks.run.mockReset()
    mocks.warning.mockReset()
    mocks.mutateAsync.mockReset()
  })
```

Add this test:

```ts
  // 覆盖只读访问者场景：可读实例知识库时仍可下载文件，但不可看到删除入口。
  it('可读用户可下载实例知识库文件但不可删除', async () => {
    mocks.canManage.mockReturnValue(false)
    const wrapper = mountTab()

    expect(wrapper.text()).toContain('下载')
    expect(wrapper.text()).not.toContain('删除')

    await wrapper.find('button').trigger('click')

    expect(mocks.downloadAppKnowledgeFile).toHaveBeenCalledWith('app-1', 'org-1', 'user-1', 'docs/app.md', 'app.md')
  })
```

- [ ] **Step 2: Run the app page test and verify it fails**

Run:

```bash
rtk sh -c 'cd web && npm test -- --run src/pages/apps/AppKnowledgeTab.spec.ts'
```

Expected: `FAIL`; there is no download button in the app knowledge table.

- [ ] **Step 3: Add download import and state**

In `web/src/pages/apps/AppKnowledgeTab.vue`, add `downloadAppKnowledgeFile` to the `useKnowledge` import list:

```ts
  downloadAppKnowledgeFile,
```

Add download state near `uploading` and `deleting`:

```ts
// downloading 标记当前页面正在触发浏览器下载，避免同一文件重复点击。
const downloading = ref(false)
```

- [ ] **Step 4: Add app download handler**

Add this function after `deleteEntry`:

```ts
// downloadEntry 下载实例知识库中的单个文件；目录行不调用此函数。
async function downloadEntry(entry: KnowledgeEntry) {
  errorMessage.value = ''
  if (!knowledgeContext.value) return
  downloading.value = true
  try {
    await downloadAppKnowledgeFile(
      props.appId,
      knowledgeContext.value.orgId,
      knowledgeContext.value.ownerUserId,
      entryRelativePath(entry.path),
      entry.name,
    )
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '下载失败'
  } finally {
    downloading.value = false
  }
}
```

- [ ] **Step 5: Replace the app actions column**

Replace the current `actions` column in `columns` with:

```ts
  {
    title: '操作', key: 'actions',
    render: (row) => {
      if (row.is_dir) return null
      const actions = [
        h(NButton, {
          size: 'small',
          disabled: downloading.value,
          onClick: () => downloadEntry(row),
        }, { default: () => downloading.value ? '下载中…' : '下载' }),
      ]
      if (canManage.value) {
        actions.push(h(NButton, {
          size: 'small',
          type: 'error',
          disabled: deleting.value,
          onClick: () => deleteEntry(entryRelativePath(row.path)),
        }, { default: () => '删除' }))
      }
      return h('div', { style: 'display: flex; gap: 8px; flex-wrap: wrap' }, actions)
    },
  },
```

- [ ] **Step 6: Run app page tests**

Run:

```bash
rtk sh -c 'cd web && npm test -- --run src/pages/apps/AppKnowledgeTab.spec.ts'
```

Expected: the spec passes.

- [ ] **Step 7: Commit app UI**

```bash
rtk git add web/src/pages/apps/AppKnowledgeTab.vue web/src/pages/apps/AppKnowledgeTab.spec.ts
rtk git commit -m "feat(knowledge): 增加实例知识库下载入口" -m "实例知识库文件行对所有可读用户显示下载按钮。" -m "删除入口仍由实例管理权限控制，并覆盖只读下载场景。"
```

---

### Task 8: Update Documentation And Run Final Verification

**Files:**
- Modify: `docs/user-manual.md`
- Modify: `docs/product-design.md`

- [ ] **Step 1: Update the user manual**

In `docs/user-manual.md`, under `### 2.5 组织级知识库`, add this line after the browse-directory paragraph:

```markdown
**下载文件**：点击普通文件行的「下载」按钮，浏览器自动下载该文件。
```

In the `### 3.2 实例详情` bullet list, replace the instance knowledge bullet with:

```markdown
- **实例知识库 tab**：上传 / 浏览 / 下载 / 删除实例私有知识库文件。
```

Under `### 3.5 知识库（只读浏览）`, replace the current description with:

```markdown
组织成员可浏览并下载组织共享知识库文件目录，但无法上传或删除文件，也不可见各节点同步状态卡片。
```

In the quick actions table near the end, add these rows next to the existing knowledge/workspace download rows:

```markdown
| 下载组织级知识库文件 | 有权用户 → `/knowledge` → 文件行「下载」 |
| 下载实例知识库文件 | 有权用户 → `/apps/:appId/knowledge` → 文件行「下载」 |
```

- [ ] **Step 2: Update product design permission wording**

In `docs/product-design.md`, update the knowledge permission rows so read predicates mention download:

```markdown
| `CanReadOrgKnowledge` | 读取 / 下载组织知识库 | 全部 | 本组织 | 本组织 |
| `CanReadAppKnowledge` | 读取 / 下载应用知识库 | 全部 | 本组织应用 | 自己应用 |
```

- [ ] **Step 3: Run backend tests**

Run:

```bash
rtk go test ./internal/files ./internal/service ./internal/api/handlers -count=1
```

Expected: all packages pass.

- [ ] **Step 4: Run focused frontend tests**

Run:

```bash
rtk sh -c 'cd web && npm test -- --run src/api/hooks/useKnowledge.spec.ts src/pages/knowledge/OrgKnowledgePage.spec.ts src/pages/apps/AppKnowledgeTab.spec.ts'
```

Expected: all three specs pass.

- [ ] **Step 5: Run frontend typecheck**

Run:

```bash
rtk sh -c 'cd web && npm run typecheck'
```

Expected: `vue-tsc --noEmit` exits 0.

- [ ] **Step 6: Re-check OpenAPI contract**

Run:

```bash
rtk make openapi-check
```

Expected: the command exits 0 and reports the OpenAPI file is synchronized.

- [ ] **Step 7: Check formatting-sensitive diff issues**

Run:

```bash
rtk git diff --check
```

Expected: no whitespace errors.

- [ ] **Step 8: Browser validation with real UI**

Start the local stack if it is not already running:

```bash
rtk make dev-up
```

Open `http://localhost:5173` in a real browser and validate:

1. Log in as manager platform admin with `admin` / `admin123`, open `/knowledge`, select an organization, and download a file from the organization knowledge list.
2. Log in as manager organization admin with organization identifier blank, username `admin`, password `admin123`, open `/knowledge`, and download a file from the organization knowledge list.
3. Log in as a member account that owns an app, open `/knowledge`, confirm file download works and delete is not visible.
4. With the same member account, open `/apps/:appId/knowledge`, download an instance knowledge file, and confirm the browser receives the expected file.

If the local fixture lacks knowledge files, upload a small `.md` file as organization admin first, then repeat the member download checks.

- [ ] **Step 9: Commit docs and final verified state**

```bash
rtk git add docs/user-manual.md docs/product-design.md
rtk git commit -m "docs(knowledge): 补充知识库下载说明" -m "更新用户手册和权限设计文档。" -m "说明组织级与实例级知识库文件下载入口及读取权限语义。"
```

- [ ] **Step 10: Confirm final worktree state**

Run:

```bash
rtk git status --short
```

Expected: only pre-existing unrelated files remain, such as `?? docs/reports/` if it still exists.

---

## Self-Review

- Spec coverage:
  - Organization knowledge single-file download is covered by Tasks 2, 3, 5, 6, and 8.
  - App knowledge single-file download is covered by Tasks 2, 3, 5, 7, and 8.
  - All identities download through existing read predicates in Task 2 and Task 3.
  - Manager master-copy download source is implemented in Task 2 through `KnowledgeMaster.Open`.
  - No directory archive work is included.
  - OpenAPI generation is covered by Task 4.
  - Browser validation is covered by Task 8.
- Placeholder scan:
  - No placeholder sections or deferred implementation steps are intentionally left in this plan.
- Type consistency:
  - Backend method names are `OpenOrgFile` and `OpenAppFile` in service, handler interface, stub, and tests.
  - Frontend helper names are `downloadOrgKnowledgeFile` and `downloadAppKnowledgeFile` in hook, page, and tests.
  - Existing fields `orgId`, `ownerUserId`, `path`, `name`, and `is_dir` are used consistently with current code.
