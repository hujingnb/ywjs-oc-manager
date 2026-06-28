# 对话功能支持文件消息 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 manager 对话功能支持「文件」消息——网页端可上传文档/图片随消息发给 AI，历史消息里的文件可渲染与下载。

**Architecture:** 文件由 manager 上传到 S3（durable）并记 `conversation_files`；发消息时 manager 把 `input_file` part（带 file_id）解析为预签名 URL 转给 oc-ops；oc-ops（与 hermes 同 pod 共享 `/opt/data`）下载文件、用引擎自带 `cache_media_bytes` 落到 agent 可见缓存路径、把 part 改写成「文字注记 + `<oc-file:id>` 标记」后转发 api_server；agent 按共享盘路径读文件。历史下载靠 manager 自有 `conversation_files` + 重新签名，与引擎 transcript 解耦。**不修改任何上游 hermes 代码。**

**Tech Stack:** Go（gin + sqlc + aws-sdk-go-v2）、Python（oc-ops，stdlib urllib + 引擎模块）、Vue 3 + TS（naive-ui + vitest）、MySQL 8、golang-migrate。

**设计依据：** [docs/superpowers/specs/2026-06-28-conversation-file-messages-design.md](../specs/2026-06-28-conversation-file-messages-design.md)

---

## 关键约定（所有任务共享）

- **对象键**：`apps/<appID>/conversations/<sessionID>/<fileID>/<filename>`（在 `AppPrefix` 下，sidecar 持久化覆盖）。
- **消息 part 形态**：文字 `{type:"text", text}`；文件 `{type:"input_file", file_id, filename, mime}`（前端发给 manager）。manager 转给 oc-ops 时把每个 `input_file` part 补上 `file_url`（预签名 GET）。
- **历史标记**：oc-ops 注入 `<oc-file:FILEID>`（紧跟注记尾部），manager 前端用正则 `/<oc-file:([^>]+)>/g` 解析。
- **支持类型**（前后端一致）：图片 `jpg jpeg png gif webp bmp`；文档对齐引擎 `pdf docx doc odt rtf txt md epub xlsx xls ods csv tsv json xml yaml yml pptx ppt odp key zip tar gz tgz bz2 xz 7z rar html htm`。
- **大小上限**：单文件默认 100MB（常量 `conversationFileMaxBytes`）。
- **预签名 TTL**：10 分钟（`conversationFilePresignTTL`，仅供 oc-ops 当轮下载）。
- **文件结构（新增/修改）**：
  - 新增 `internal/migrations/000018_conversation_files.{up,down}.sql`
  - 新增 `internal/store/queries/conversation_files.sql`（sqlc 生成 `internal/store/sqlc/conversation_files.sql.go`）
  - 修改 `internal/integrations/storage/keys.go`（加 `ConversationFileKey`）
  - 新增 `internal/service/conversation_files.go`（`ConversationFileService`）
  - 修改 `internal/service/hermes_conversation.go`（Chat/ChatStream 参数 string→any + 文件 part 富化）
  - 修改 `internal/api/handlers/dto.go`（`ConversationChatRequest.Message` string→any）
  - 新增 `internal/api/handlers/conversation_files.go`（上传/下载 handler + 路由）
  - 修改 `internal/api/handlers/hermes_conversation.go`（Chat/ChatStream 传 any）
  - 修改 `internal/api/router.go`（装配新 service/handler）
  - 修改 `runtime/hermes/hermes-v2026.6.5/ocops/conversation.py` + 新增 `ocops/conversation_files.py` + 测试；**同样改 `hermes-v2026.5.16/`**
  - 修改 `web/src/api/conversations.ts`、`web/src/pages/apps/AppConversationsTab.vue`、`web/src/pages/apps/ConversationMessageView.vue`

---

## Phase 1 — 数据库与存储键

### Task 1: conversation_files 迁移

**Files:**
- Create: `internal/migrations/000018_conversation_files.up.sql`
- Create: `internal/migrations/000018_conversation_files.down.sql`
- Modify: `sqlc.yaml`（schema 列表追加新 up.sql）

- [ ] **Step 1: 写 up 迁移**

`internal/migrations/000018_conversation_files.up.sql`：
```sql
-- conversation_files 记录 manager 自身的「对话文件上传」操作：文件本体存 S3，
-- manager 不持有对话消息本体，仅靠本表把 file_id 映射回 S3 对象以支持历史下载与重新签名。
CREATE TABLE conversation_files (
    id CHAR(36) PRIMARY KEY COMMENT '文件 ID（UUID），即消息 part 与 <oc-file:id> 标记里的 file_id',
    app_id CHAR(36) NOT NULL COMMENT '所属实例 ID',
    session_id VARCHAR(256) NOT NULL COMMENT '所属会话 ID（hermes session id，非 UUID）',
    s3_key VARCHAR(1024) NOT NULL COMMENT 'S3 对象键 apps/<appID>/conversations/<sid>/<fileID>/<filename>',
    filename VARCHAR(512) NOT NULL COMMENT '原始文件名（展示与下载用）',
    mime VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'MIME 类型',
    size BIGINT NOT NULL DEFAULT 0 COMMENT '文件字节数',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '上传时间',
    KEY idx_conversation_files_app_session (app_id, session_id),
    CONSTRAINT fk_conversation_files_app FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
```

- [ ] **Step 2: 写 down 迁移**

`internal/migrations/000018_conversation_files.down.sql`：
```sql
DROP TABLE IF EXISTS conversation_files;
```

- [ ] **Step 3: 把新 up.sql 加入 sqlc schema**

在 `sqlc.yaml` 的 `schema:` 列表末尾（`000014_apps_locale.up.sql` 之后）追加一行：
```yaml
      - internal/migrations/000018_conversation_files.up.sql
```
注意：确认 `000015~000017` 是否已在列表中；若缺失则一并按序补上，保证 sqlc 能解析到最新 `apps` 表结构（FK 依赖）。

- [ ] **Step 4: 校验迁移可加载**

Run: `go test ./internal/migrations/...`
Expected: PASS（`migrations_test.go` 校验 up/down 成对且可解析）。

- [ ] **Step 5: Commit**

```bash
git add internal/migrations/000018_conversation_files.up.sql internal/migrations/000018_conversation_files.down.sql sqlc.yaml
git commit -m "feat(conversation): 新增 conversation_files 表迁移

记录对话文件上传操作，把 file_id 映射回 S3 对象，支持历史下载与重新签名。"
```

---

### Task 2: sqlc 查询

**Files:**
- Create: `internal/store/queries/conversation_files.sql`
- Generated: `internal/store/sqlc/conversation_files.sql.go`（由 sqlc 生成，勿手写）

- [ ] **Step 1: 写查询**

`internal/store/queries/conversation_files.sql`：
```sql
-- name: CreateConversationFile :exec
INSERT INTO conversation_files (
    id, app_id, session_id, s3_key, filename, mime, size
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetConversationFile :one
SELECT * FROM conversation_files WHERE id = ?;
```

- [ ] **Step 2: 生成 sqlc 代码**

Run: `make sqlc-gen`（若无该 target，用 `sqlc generate`）
Expected: 生成 `internal/store/sqlc/conversation_files.sql.go`，含 `ConversationFile` 结构体、`CreateConversationFileParams`、`CreateConversationFile`、`GetConversationFile`。

- [ ] **Step 3: 编译确认**

Run: `go build ./internal/store/...`
Expected: 编译通过。

- [ ] **Step 4: Commit**

```bash
git add internal/store/queries/conversation_files.sql internal/store/sqlc/conversation_files.sql.go
git commit -m "feat(conversation): conversation_files 的 sqlc 查询

新增按 id 查询与插入操作，供对话文件 service 使用。"
```

---

### Task 3: 对象键约定

**Files:**
- Modify: `internal/integrations/storage/keys.go`
- Test: `internal/integrations/storage/keys_test.go`（若不存在则创建）

- [ ] **Step 1: 写失败测试**

在 `internal/integrations/storage/keys_test.go` 追加（无文件则创建，包名 `storage`）：
```go
// 校验对话文件对象键拼装：位于 app 前缀下，按 session/fileID/filename 分层。
func TestConversationFileKey(t *testing.T) {
	got := ConversationFileKey("app1", "weixin:u1", "f9", "报告.pdf")
	assert.Equal(t, "apps/app1/conversations/weixin:u1/f9/报告.pdf", got)
}
```
（文件顶部需 `import ("testing"; "github.com/stretchr/testify/assert")`。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/integrations/storage/ -run TestConversationFileKey`
Expected: FAIL（`ConversationFileKey` 未定义）。

- [ ] **Step 3: 实现**

在 `internal/integrations/storage/keys.go` 末尾追加：
```go
// ConversationFileKey 返回对话文件对象键
// "apps/<appID>/conversations/<sessionID>/<fileID>/<filename>"。
// 位于 AppPrefix 下，随 app 数据被 sidecar 持久化。调用方保证各段为合法路径段。
func ConversationFileKey(appID, sessionID, fileID, filename string) string {
	return path.Join("apps", appID, "conversations", sessionID, fileID, filename)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/integrations/storage/ -run TestConversationFileKey`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/storage/keys.go internal/integrations/storage/keys_test.go
git commit -m "feat(conversation): 新增对话文件 S3 对象键约定"
```

---

## Phase 2 — manager 上传/下载 service 与端点

### Task 4: ConversationFileService（上传 + 查询 + 预签名）

**Files:**
- Create: `internal/service/conversation_files.go`
- Test: `internal/service/conversation_files_test.go`

- [ ] **Step 1: 写失败测试**

`internal/service/conversation_files_test.go`：
```go
package service

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
)

// fakeConvFileStore 记录插入参数并按 id 返回固定记录。
type fakeConvFileStore struct {
	created  convFileRecord
	getByID  map[string]convFileRecord
}

func (f *fakeConvFileStore) CreateConversationFile(ctx context.Context, r convFileRecord) error {
	f.created = r
	return nil
}
func (f *fakeConvFileStore) GetConversationFile(ctx context.Context, id string) (convFileRecord, error) {
	r, ok := f.getByID[id]
	if !ok {
		return convFileRecord{}, ErrConversationFileNotFound
	}
	return r, nil
}

// fakeBlob 记录 PutObject 并对任意 key 返回固定预签名 URL。
type fakeBlob struct{ putKey, putData string }

func (b *fakeBlob) PutObject(ctx context.Context, key string, r io.Reader, size int64) error {
	b.putKey = key
	data, _ := io.ReadAll(r)
	b.putData = string(data)
	return nil
}
func (b *fakeBlob) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return "https://s3.example/" + key, nil
}

// fakeResolver 返回固定 app 定位（owner/org），供权限判断。
type fakeConvResolver struct{}

func (fakeConvResolver) Resolve(ctx context.Context, appID string) (OcOpsAppLocation, error) {
	return OcOpsAppLocation{OrgID: "org1", OwnerUserID: "owner1"}, nil
}

func platformAdmin() auth.Principal { return auth.Principal{Role: domain.UserRolePlatformAdmin} }

// 上传：校验类型/大小后 PutObject 并落库，返回 file_id 与元数据。
func TestUploadConversationFile(t *testing.T) {
	store := &fakeConvFileStore{}
	blob := &fakeBlob{}
	svc := NewConversationFileService(store, blob, fakeConvResolver{})

	res, err := svc.Upload(context.Background(), platformAdmin(), "app1", "weixin:u1",
		"报告.pdf", strings.NewReader("PDFDATA"), int64(len("PDFDATA")))
	require.NoError(t, err)
	assert.NotEmpty(t, res.FileID)
	assert.Equal(t, "报告.pdf", res.Filename)
	assert.Equal(t, "application/pdf", res.Mime)
	assert.Equal(t, "PDFDATA", blob.putData)
	assert.Equal(t, store.created.S3Key, blob.putKey)
	assert.Contains(t, blob.putKey, "apps/app1/conversations/weixin:u1/")
}

// 不支持的扩展名直接拒绝，不落库。
func TestUploadConversationFileRejectsType(t *testing.T) {
	svc := NewConversationFileService(&fakeConvFileStore{}, &fakeBlob{}, fakeConvResolver{})
	_, err := svc.Upload(context.Background(), platformAdmin(), "app1", "s1",
		"evil.exe", strings.NewReader("x"), 1)
	require.ErrorIs(t, err, ErrConversationFileUnsupported)
}

// 解析 file_id → 预签名 URL，并校验文件归属该 app+session。
func TestResolveFileURL(t *testing.T) {
	store := &fakeConvFileStore{getByID: map[string]convFileRecord{
		"f1": {ID: "f1", AppID: "app1", SessionID: "s1", S3Key: "apps/app1/conversations/s1/f1/a.pdf", Filename: "a.pdf", Mime: "application/pdf"},
	}}
	svc := NewConversationFileService(store, &fakeBlob{}, fakeConvResolver{})
	url, filename, mime, err := svc.ResolveFileURL(context.Background(), "app1", "s1", "f1")
	require.NoError(t, err)
	assert.Equal(t, "https://s3.example/apps/app1/conversations/s1/f1/a.pdf", url)
	assert.Equal(t, "a.pdf", filename)
	assert.Equal(t, "application/pdf", mime)
}

// 文件不属于该 app/session 时拒绝（防越权引用他人文件）。
func TestResolveFileURLWrongOwnerRejected(t *testing.T) {
	store := &fakeConvFileStore{getByID: map[string]convFileRecord{
		"f1": {ID: "f1", AppID: "appX", SessionID: "sX", S3Key: "k", Filename: "a.pdf"},
	}}
	svc := NewConversationFileService(store, &fakeBlob{}, fakeConvResolver{})
	_, _, _, err := svc.ResolveFileURL(context.Background(), "app1", "s1", "f1")
	require.ErrorIs(t, err, ErrConversationFileNotFound)
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/service/ -run TestUploadConversationFile`
Expected: FAIL（类型未定义）。

- [ ] **Step 3: 实现 service**

`internal/service/conversation_files.go`：
```go
// Package service —— conversation_files.go 实现对话文件上传/下载。
// manager 把文件存 S3 并以 conversation_files 记录映射，支持历史渲染与下载；
// 文件本体不入 DB，权限沿用对话读写谓词。
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/storage"
)

// 对话文件相关哨兵错误。
var (
	ErrConversationFileNotFound    = errors.New("conversation file not found")
	ErrConversationFileForbidden   = errors.New("conversation file forbidden")
	ErrConversationFileUnsupported = errors.New("conversation file type unsupported")
	ErrConversationFileTooLarge    = errors.New("conversation file too large")
)

// conversationFileMaxBytes 单文件上限（100MB）。
const conversationFileMaxBytes int64 = 100 * 1024 * 1024

// conversationFilePresignTTL 预签名 GET 有效期（仅供 oc-ops 当轮下载）。
const conversationFilePresignTTL = 10 * time.Minute

// allowedConversationFileExts 允许的扩展名集合（图片 + 引擎 SUPPORTED_DOCUMENT_TYPES 对齐）。
var allowedConversationFileExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".bmp": true,
	".pdf": true, ".docx": true, ".doc": true, ".odt": true, ".rtf": true, ".txt": true,
	".md": true, ".epub": true, ".xlsx": true, ".xls": true, ".ods": true, ".csv": true,
	".tsv": true, ".json": true, ".xml": true, ".yaml": true, ".yml": true, ".pptx": true,
	".ppt": true, ".odp": true, ".key": true, ".zip": true, ".tar": true, ".gz": true,
	".tgz": true, ".bz2": true, ".xz": true, ".7z": true, ".rar": true, ".html": true, ".htm": true,
}

// convFileRecord 是 service 内部的对话文件记录（与 sqlc.ConversationFile 字段对齐，
// 由 store 适配层转换，避免 service 直接依赖 sqlc 类型）。
type convFileRecord struct {
	ID        string
	AppID     string
	SessionID string
	S3Key     string
	Filename  string
	Mime      string
	Size      int64
}

// ConversationFileStore 是对话文件持久化窄接口。
type ConversationFileStore interface {
	CreateConversationFile(ctx context.Context, r convFileRecord) error
	GetConversationFile(ctx context.Context, id string) (convFileRecord, error)
}

// conversationFileBlob 是对象存储窄接口（由 storage.S3ObjectStore 实现）。
type conversationFileBlob interface {
	PutObject(ctx context.Context, key string, r io.Reader, size int64) error
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// ConversationFileUploadResult 是上传成功后回给前端的元数据。
type ConversationFileUploadResult struct {
	FileID   string `json:"file_id"`
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
}

// ConversationFileService 提供对话文件上传/下载/预签名。
type ConversationFileService struct {
	store    ConversationFileStore
	blob     conversationFileBlob
	resolver OcOpsResolver
}

// NewConversationFileService 构造 service。
func NewConversationFileService(store ConversationFileStore, blob conversationFileBlob, resolver OcOpsResolver) *ConversationFileService {
	return &ConversationFileService{store: store, blob: blob, resolver: resolver}
}

// Upload 校验类型/大小、PutObject 到 S3、落 conversation_files，返回 file_id。
// 权限：实例写权限（CanManageAppConversations）。
func (s *ConversationFileService) Upload(ctx context.Context, p auth.Principal, appID, sid, filename string, body io.Reader, size int64) (ConversationFileUploadResult, error) {
	loc, err := s.resolver.Resolve(ctx, appID)
	if err != nil {
		return ConversationFileUploadResult{}, err
	}
	if !auth.CanManageAppConversations(p, loc.OrgID, loc.OwnerUserID) {
		return ConversationFileUploadResult{}, ErrConversationFileForbidden
	}
	if err := validateSessionID(sid); err != nil {
		return ConversationFileUploadResult{}, err
	}
	if size > conversationFileMaxBytes {
		return ConversationFileUploadResult{}, ErrConversationFileTooLarge
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if !allowedConversationFileExts[ext] {
		return ConversationFileUploadResult{}, fmt.Errorf("%w: %s", ErrConversationFileUnsupported, ext)
	}
	fileID := uuid.NewString()
	key := storage.ConversationFileKey(appID, sid, fileID, filename)
	if err := s.blob.PutObject(ctx, key, body, size); err != nil {
		return ConversationFileUploadResult{}, err
	}
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	rec := convFileRecord{ID: fileID, AppID: appID, SessionID: sid, S3Key: key, Filename: filename, Mime: mimeType, Size: size}
	if err := s.store.CreateConversationFile(ctx, rec); err != nil {
		return ConversationFileUploadResult{}, err
	}
	return ConversationFileUploadResult{FileID: fileID, Filename: filename, Mime: mimeType, Size: size}, nil
}

// ResolveFileURL 把 file_id 解析为预签名 GET URL（供 oc-ops 当轮下载）。
// 校验文件归属该 app+session，防越权引用。不做角色权限判断（调用方 Chat 已校验）。
func (s *ConversationFileService) ResolveFileURL(ctx context.Context, appID, sid, fileID string) (url, filename, mimeType string, err error) {
	rec, err := s.store.GetConversationFile(ctx, fileID)
	if err != nil {
		return "", "", "", ErrConversationFileNotFound
	}
	if rec.AppID != appID || rec.SessionID != sid {
		return "", "", "", ErrConversationFileNotFound
	}
	u, err := s.blob.PresignGet(ctx, rec.S3Key, conversationFilePresignTTL)
	if err != nil {
		return "", "", "", err
	}
	return u, rec.Filename, rec.Mime, nil
}

// Download 供历史下载端点：校验读权限 + 归属，返回预签名 URL（handler 302 跳转）。
func (s *ConversationFileService) Download(ctx context.Context, p auth.Principal, appID, sid, fileID string) (url, filename string, err error) {
	loc, err := s.resolver.Resolve(ctx, appID)
	if err != nil {
		return "", "", err
	}
	if !auth.CanViewAppConversations(p, loc.OrgID, loc.OwnerUserID) {
		return "", "", ErrConversationFileForbidden
	}
	u, fn, _, err := s.ResolveFileURL(ctx, appID, sid, fileID)
	return u, fn, err
}
```
说明：`OcOpsResolver` / `OcOpsAppLocation` 已在 service 包定义（见 `hermes_conversation.go`）。若 `github.com/google/uuid` 未引入，用仓库现有 UUID 生成方式（搜 `uuid.NewString()` 既有用法确认）。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/service/ -run TestUploadConversationFile -run TestResolveFileURL`
Expected: PASS（4 个用例全过）。

- [ ] **Step 5: Commit**

```bash
git add internal/service/conversation_files.go internal/service/conversation_files_test.go
git commit -m "feat(conversation): 对话文件上传/下载 service

校验类型大小后 PutObject 落 S3 与 conversation_files；ResolveFileURL 按
file_id 预签名并校验归属，防越权引用。"
```

---

### Task 5: store 适配层（sqlc ↔ convFileRecord）

**Files:**
- Create: `internal/store/conversation_file_store.go`
- Test: `internal/store/mysql_integration_test.go`（已存在，追加用例；无 DB 时跳过遵循现有约定）

- [ ] **Step 1: 实现适配层**

`internal/store/conversation_file_store.go`：
```go
// Package store —— conversation_file_store.go 把 sqlc 查询适配成
// service.ConversationFileStore 接口。
package store

import (
	"context"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// ConversationFileStore 把 *Store 适配为 service.ConversationFileStore。
type ConversationFileStore struct {
	store *Store
}

// NewConversationFileStore 构造适配器。
func NewConversationFileStore(s *Store) *ConversationFileStore {
	return &ConversationFileStore{store: s}
}

// CreateConversationFile 插入一条对话文件记录。
func (a *ConversationFileStore) CreateConversationFile(ctx context.Context, r service.ConvFileRecord) error {
	return a.store.Queries().CreateConversationFile(ctx, sqlc.CreateConversationFileParams{
		ID: r.ID, AppID: r.AppID, SessionID: r.SessionID,
		S3Key: r.S3Key, Filename: r.Filename, Mime: r.Mime, Size: r.Size,
	})
}

// GetConversationFile 按 id 读一条记录。
func (a *ConversationFileStore) GetConversationFile(ctx context.Context, id string) (service.ConvFileRecord, error) {
	row, err := a.store.Queries().GetConversationFile(ctx, id)
	if err != nil {
		return service.ConvFileRecord{}, err
	}
	return service.ConvFileRecord{
		ID: row.ID, AppID: row.AppID, SessionID: row.SessionID,
		S3Key: row.S3Key, Filename: row.Filename, Mime: row.Mime, Size: row.Size,
	}, nil
}
```
> **重要**：上面引用了导出的 `service.ConvFileRecord`，但 Task 4 把它定义为非导出 `convFileRecord`。请在 Task 4 的 `conversation_files.go` 里把 `convFileRecord` 改名为导出的 **`ConvFileRecord`**（含 `ConversationFileStore` 接口签名与所有用到处），以便跨包 `store` 引用。改完重跑 Task 4 测试确认仍 PASS。
> 同时确认 `*Store` 暴露 `Queries()` 方法返回 `*sqlc.Queries`；若现有命名不同（如直接字段），按既有 store 适配器（如 `app_runner.go`）的访问方式对齐。

- [ ] **Step 2: 编译确认**

Run: `go build ./internal/...`
Expected: 通过（若报 `convFileRecord` 未导出，按上面说明改名）。

- [ ] **Step 3: Commit**

```bash
git add internal/store/conversation_file_store.go internal/service/conversation_files.go internal/service/conversation_files_test.go
git commit -m "feat(conversation): conversation_files store 适配层

把 sqlc 查询适配为 service.ConversationFileStore，记录导出为 ConvFileRecord。"
```

---

### Task 6: 上传/下载 handler 与路由

**Files:**
- Create: `internal/api/handlers/conversation_files.go`
- Test: `internal/api/handlers/conversation_files_test.go`

- [ ] **Step 1: 写失败测试**

`internal/api/handlers/conversation_files_test.go`（参照 `hermes_conversation_test.go` 的 gin 测试风格，用 fake service）：
```go
package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/service"
)

// fakeConvFileSvc 实现 handler 依赖的窄接口。
type fakeConvFileSvc struct {
	uploadRes service.ConversationFileUploadResult
	dlURL     string
}

func (f *fakeConvFileSvc) Upload(ctx ginCtx, appID, sid, filename string, body ioReader, size int64) (service.ConversationFileUploadResult, error) {
	return f.uploadRes, nil
}

// 说明：实际 handler 直接调 *service.ConversationFileService。测试可改为构造真 service + fake store/blob，
// 或为 handler 抽一个窄接口。优先抽接口 conversationFileService（与 hermes_conversation handler 同模式）。

// 上传成功返回 200 + file_id。
func TestConversationFileUploadHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 构造真 service + fake 依赖（复用 service 包测试里的 fakeConvFileStore/fakeBlob 思路），
	// 或注入实现 conversationFileService 接口的 fake，返回固定 uploadRes。
	// 断言：POST /api/v1/apps/app1/hermes/conversations/s1/files?filename=a.pdf
	//   body=octet-stream，响应 200，JSON 含 file_id。
	_ = bytes.NewReader
	_ = httptest.NewRecorder
	_ = http.StatusOK
	require.True(t, true)
	assert.True(t, true)
}
```
> 落地时请按 `hermes_conversation.go` 的 handler 模式：定义窄接口
> `conversationFileService`（含 `Upload`/`Download` 方法签名与 `*service.ConversationFileService` 一致），
> handler 持该接口，测试注入 fake。上面是占位骨架，补全为真实断言。

- [ ] **Step 2: 实现 handler**

`internal/api/handlers/conversation_files.go`：
```go
// Package handlers —— conversation_files.go 暴露对话文件上传/下载端点。
package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// conversationFileService 是 handler 依赖的对话文件服务窄接口。
type conversationFileService interface {
	Upload(ctx ctxWithPrincipal, appID, sid, filename string, body httpBody, size int64) (service.ConversationFileUploadResult, error)
	Download(ctx ctxWithPrincipal, appID, sid, fileID string) (url, filename string, err error)
}
```
> 注意：上面用了占位类型 `ctxWithPrincipal`/`httpBody`，仅为示意。**实际签名必须与 Task 4 的
> `*service.ConversationFileService` 完全一致**：
> `Upload(ctx context.Context, p auth.Principal, appID, sid, filename string, body io.Reader, size int64) (service.ConversationFileUploadResult, error)`
> 和 `Download(ctx context.Context, p auth.Principal, appID, sid, fileID string) (url, filename string, err error)`。
> 请据此写接口与 handler，正文如下：

```go
// HermesConversationFileHandler 处理对话文件上传/下载。
type HermesConversationFileHandler struct {
	svc conversationFileService // 实际为 *service.ConversationFileService
}

// NewHermesConversationFileHandler 构造 handler。
func NewHermesConversationFileHandler(svc conversationFileService) *HermesConversationFileHandler {
	return &HermesConversationFileHandler{svc: svc}
}

// RegisterHermesConversationFileRoutes 注册对话文件路由（挂在 conversations 组下）。
func RegisterHermesConversationFileRoutes(router gin.IRouter, h *HermesConversationFileHandler) {
	g := router.Group("/api/v1/apps/:appId/hermes/conversations")
	g.POST("/:sid/files", h.Upload)
	g.GET("/:sid/files/:fileId", h.Download)
}

// Upload godoc
// @Summary 上传对话文件
// @Tags hermes-conversations
// @Param appId path string true "实例 ID"
// @Param sid path string true "会话 ID"
// @Param filename query string true "文件名"
// @Accept octet-stream
// @Produce json
// @Success 200 {object} service.ConversationFileUploadResult
// @Router /api/v1/apps/{appId}/hermes/conversations/{sid}/files [post]
func (h *HermesConversationFileHandler) Upload(c *gin.Context) {
	p := principalFromCtx(c) // 与现有 handler 一致的鉴权主体提取
	filename := c.Query("filename")
	if filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filename required"})
		return
	}
	res, err := h.svc.Upload(c.Request.Context(), p, c.Param("appId"), c.Param("sid"),
		filename, c.Request.Body, c.Request.ContentLength)
	if err != nil {
		writeConversationFileError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// Download godoc
// @Summary 下载对话文件
// @Tags hermes-conversations
// @Param appId path string true "实例 ID"
// @Param sid path string true "会话 ID"
// @Param fileId path string true "文件 ID"
// @Success 302 {string} string "重定向到预签名 URL"
// @Router /api/v1/apps/{appId}/hermes/conversations/{sid}/files/{fileId} [get]
func (h *HermesConversationFileHandler) Download(c *gin.Context) {
	p := principalFromCtx(c)
	url, _, err := h.svc.Download(c.Request.Context(), p, c.Param("appId"), c.Param("sid"), c.Param("fileId"))
	if err != nil {
		writeConversationFileError(c, err)
		return
	}
	c.Redirect(http.StatusFound, url)
}

// writeConversationFileError 把 service 哨兵错误映射到 HTTP 状态码。
func writeConversationFileError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrConversationFileForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrConversationFileNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrConversationFileUnsupported):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrConversationFileTooLarge):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
```
> `principalFromCtx` 用法见 `hermes_conversation.go`；`auth` import 若未直接用可去掉。

- [ ] **Step 3: 运行确认通过**

Run: `go test ./internal/api/handlers/ -run TestConversationFileUploadHandler`
Expected: PASS。

- [ ] **Step 4: Commit**

```bash
git add internal/api/handlers/conversation_files.go internal/api/handlers/conversation_files_test.go
git commit -m "feat(conversation): 对话文件上传/下载 handler 与路由"
```

---

## Phase 3 — 发消息支持文件 part（DTO + service 富化）

### Task 7: ConversationChatRequest.Message string→any + service 富化文件 part

**Files:**
- Modify: `internal/api/handlers/dto.go:505-509`
- Modify: `internal/api/handlers/hermes_conversation.go`（Chat/ChatStream 传 message any）
- Modify: `internal/service/hermes_conversation.go`（Chat/ChatStream 参数 string→any + 富化）
- Modify: `internal/api/router.go`（给 HermesConversationService 注入文件解析器）
- Test: `internal/service/hermes_conversation_test.go`（追加富化用例）

- [ ] **Step 1: 写失败测试（service 富化）**

在 `internal/service/hermes_conversation_test.go` 追加。先在文件顶部确认已有 fake `ops`（记录 SessionChat 入参）与 fake resolver；若没有，参照现有测试构造。新增：
```go
// 发送含 input_file part 的消息：service 把 file_id 富化为 file_url（预签名）后转 oc-ops。
func TestChatEnrichesFileParts(t *testing.T) {
	ops := &fakeConversationOps{} // 现有/新建：记录最后一次 SessionChat 的 req
	svc := NewHermesConversationService(ops, fakeConvResolver{})
	// 注入文件解析器：file_id "f1" → url "https://s3/x"
	svc.SetFileResolver(fileResolverFunc(func(ctx context.Context, appID, sid, fileID string) (string, string, string, error) {
		assert.Equal(t, "f1", fileID)
		return "https://s3/x", "a.pdf", "application/pdf", nil
	}))

	msg := []any{
		map[string]any{"type": "text", "text": "看看这个"},
		map[string]any{"type": "input_file", "file_id": "f1"},
	}
	_, err := svc.Chat(context.Background(), platformAdmin(), "app1", "s1", msg)
	require.NoError(t, err)

	parts, ok := ops.lastReq.Message.([]any)
	require.True(t, ok)
	// 第二个 part 被补上 file_url/filename/mime
	fp := parts[1].(map[string]any)
	assert.Equal(t, "https://s3/x", fp["file_url"])
	assert.Equal(t, "a.pdf", fp["filename"])
}

// 纯文件、无文字也允许（放宽空消息校验）。
func TestChatAllowsFileOnly(t *testing.T) {
	ops := &fakeConversationOps{}
	svc := NewHermesConversationService(ops, fakeConvResolver{})
	svc.SetFileResolver(fileResolverFunc(func(ctx context.Context, a, s, f string) (string, string, string, error) {
		return "https://s3/x", "a.pdf", "application/pdf", nil
	}))
	msg := []any{map[string]any{"type": "input_file", "file_id": "f1"}}
	_, err := svc.Chat(context.Background(), platformAdmin(), "app1", "s1", msg)
	require.NoError(t, err)
}
```
辅助类型（放测试文件内）：
```go
// fileResolverFunc 把函数适配成 ConversationFileResolver 接口。
type fileResolverFunc func(ctx context.Context, appID, sid, fileID string) (string, string, string, error)

func (f fileResolverFunc) ResolveFileURL(ctx context.Context, appID, sid, fileID string) (string, string, string, error) {
	return f(ctx, appID, sid, fileID)
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/service/ -run TestChatEnrichesFileParts`
Expected: FAIL（`SetFileResolver` / message any 未定义）。

- [ ] **Step 3: 改 DTO**

`internal/api/handlers/dto.go:505-509`：
```go
type ConversationChatRequest struct {
	// Message 是消息内容：文字字符串，或多模态 parts 数组
	// [{type:"text",text} | {type:"input_file",file_id,filename,mime}]。
	// 文件 part 由 service 富化为带 file_url 的预签名引用后转发 oc-ops。
	Message any `json:"message" binding:"required"`
}
```

- [ ] **Step 4: 改 service**

在 `internal/service/hermes_conversation.go`：
1) `HermesConversationService` 加字段与接口：
```go
// ConversationFileResolver 把消息里的 file_id 解析为预签名 URL 与元数据。
type ConversationFileResolver interface {
	ResolveFileURL(ctx context.Context, appID, sid, fileID string) (url, filename, mime string, err error)
}
```
结构体追加 `fileResolver ConversationFileResolver`，并加 setter：
```go
// SetFileResolver 注入文件 part 解析器（对话文件 service 实现）。
func (s *HermesConversationService) SetFileResolver(r ConversationFileResolver) { s.fileResolver = r }
```
2) Chat/ChatStream 签名 `message string` → `message any`，把空校验改为「无可见内容才拒绝」，并在转发前富化：
```go
func (s *HermesConversationService) Chat(ctx context.Context, p auth.Principal, appID, sid string, message any) (ocops.ConversationChatResult, error) {
	loc, err := s.resolveManage(ctx, p, appID)
	if err != nil {
		return ocops.ConversationChatResult{}, err
	}
	if err := validateSessionID(sid); err != nil {
		return ocops.ConversationChatResult{}, err
	}
	enriched, err := s.enrichMessage(ctx, appID, sid, message)
	if err != nil {
		return ocops.ConversationChatResult{}, err
	}
	if !messageHasContent(enriched) {
		return ocops.ConversationChatResult{}, fmt.Errorf("%w: 消息内容不能为空", ErrConversationBadRequest)
	}
	out, err := s.ops.SessionChat(ctx, loc.Endpoint, sid, ocops.ConversationChatReq{Message: enriched})
	if err != nil {
		return ocops.ConversationChatResult{}, mapOcOpsConversationErr(err)
	}
	return out, nil
}
```
ChatStream 同样改造（enrich + messageHasContent + 传 enriched）。
3) 新增富化与校验辅助：
```go
// enrichMessage 把多模态消息里的 input_file part 富化为带 file_url 的引用。
// message 为字符串时原样返回；为 parts 数组时逐个处理，input_file 需 file_id。
func (s *HermesConversationService) enrichMessage(ctx context.Context, appID, sid string, message any) (any, error) {
	parts, ok := message.([]any)
	if !ok {
		return message, nil // 字符串或其它形态：透传
	}
	out := make([]any, 0, len(parts))
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		if part["type"] == "input_file" {
			fileID, _ := part["file_id"].(string)
			if fileID == "" {
				return nil, fmt.Errorf("%w: input_file 缺少 file_id", ErrConversationBadRequest)
			}
			if s.fileResolver == nil {
				return nil, fmt.Errorf("%w: 文件解析器未配置", ErrConversationBadRequest)
			}
			url, filename, mime, err := s.fileResolver.ResolveFileURL(ctx, appID, sid, fileID)
			if err != nil {
				return nil, ErrConversationBadRequest // file_id 非法/越权
			}
			part["file_url"] = url
			part["filename"] = filename
			part["mime"] = mime
		}
		out = append(out, part)
	}
	return out, nil
}

// messageHasContent 判断富化后的消息是否有可发送内容（非空文字 或 至少一个文件）。
func messageHasContent(message any) bool {
	switch m := message.(type) {
	case string:
		return strings.TrimSpace(m) != ""
	case []any:
		for _, raw := range m {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if part["type"] == "input_file" {
				return true
			}
			if part["type"] == "text" {
				if t, _ := part["text"].(string); strings.TrimSpace(t) != "" {
					return true
				}
			}
		}
		return false
	default:
		return false
	}
}
```

- [ ] **Step 5: 改 handler 传 any**

`internal/api/handlers/hermes_conversation.go` 的 `Chat`/`ChatStream`：把传给 service 的 `req.Message`（原 string）直接传 `any`：
```go
out, err := h.svc.Chat(c.Request.Context(), p, appID, sid, req.Message)
```
ChatStream 同理。同步把 handler 持有的 service 接口（若有窄接口定义）方法签名 `message string`→`message any`。

- [ ] **Step 6: 装配注入**

`internal/api/router.go` 在构造 `HermesConversationService` 处（约 line 229 上游 dep 装配）：
- 构造 `ConversationFileService`（store 适配器 + S3 ObjectStore + resolver）。
- `dep.HermesConversationService.SetFileResolver(convFileService)`。
- 注册文件路由：`handlers.RegisterHermesConversationFileRoutes(user, handlers.NewHermesConversationFileHandler(convFileService))`。
> S3 ObjectStore 仅在启用对象存储时存在；参照 knowledge service 的注入条件（`SetMultipartUploader` 仅 S3 启用时调用）。未启用 S3 时对话文件功能不可用，前端上传会得到错误。

- [ ] **Step 7: 运行测试 + 编译**

Run: `go test ./internal/service/ -run TestChat && go build ./...`
Expected: 富化用例 PASS，全量编译通过（修复因签名变更而报错的既有调用/测试：原 `Chat(..., message string)` 调用方改传 string 字面量仍兼容 `any`，但既有测试若断言 `ConversationChatReq{Message: message}` 需相应更新）。

- [ ] **Step 8: 重新生成 OpenAPI 与前端类型**

Run: `make openapi-gen && make web-types-gen`
然后 `make openapi-check`（工作区应干净）。
Expected: `openapi/openapi.yaml` 与 `web/src/api/generated.ts` 更新且 check 通过。

- [ ] **Step 9: Commit**

```bash
git add internal/api/handlers/dto.go internal/api/handlers/hermes_conversation.go internal/service/hermes_conversation.go internal/service/hermes_conversation_test.go internal/api/router.go openapi/openapi.yaml web/src/api/generated.ts
git commit -m "feat(conversation): 发消息支持文件 part 并由 service 富化预签名

ConversationChatRequest.Message 改 any 承载多模态 parts；service 把
input_file part 的 file_id 解析为预签名 file_url 后转 oc-ops，放宽纯文件消息的空校验。"
```

---

## Phase 4 — oc-ops 落盘改写（Python，两 variant）

### Task 8: ocops materialize_files（下载 + cache_media_bytes + 注记）

**Files:**
- Create: `runtime/hermes/hermes-v2026.6.5/ocops/conversation_files.py`
- Test: `runtime/hermes/hermes-v2026.6.5/tests/test_conversation_files.py`

- [ ] **Step 1: 写失败测试**

`runtime/hermes/hermes-v2026.6.5/tests/test_conversation_files.py`：
```python
# 覆盖 ocops.conversation_files.materialize_files：
# input_file part → 下载 → cache_media_bytes → 文字注记 + <oc-file:id> 标记；字符串透传。
import json
from unittest import mock

from ocops import conversation_files as cf


# 字符串消息原样返回，不触发下载。
def test_string_passthrough():
    assert cf.materialize_files("hello") == "hello"


# input_file part：下载字节、调 cache_media_bytes、生成含路径与标记的注记。
def test_input_file_becomes_note_with_marker():
    fake_cached = mock.Mock(kind="document", display_name="a.pdf", path="/opt/data/cache/documents/a.pdf")
    with mock.patch.object(cf, "_download", return_value=b"PDFDATA") as dl, \
         mock.patch.object(cf, "_cache_media_bytes", return_value=fake_cached) as cm:
        out = cf.materialize_files([
            {"type": "text", "text": "看看这个"},
            {"type": "input_file", "file_id": "f1", "file_url": "https://s3/x", "filename": "a.pdf"},
        ])
    dl.assert_called_once_with("https://s3/x")
    cm.assert_called_once()
    assert "看看这个" in out
    assert "/opt/data/cache/documents/a.pdf" in out
    assert "<oc-file:f1>" in out
    assert "a.pdf" in out


# 下载失败：该文件降级为「不可用」注记并带标记，不抛异常，文字仍保留。
def test_download_failure_degrades():
    with mock.patch.object(cf, "_download", side_effect=RuntimeError("boom")):
        out = cf.materialize_files([
            {"type": "text", "text": "hi"},
            {"type": "input_file", "file_id": "f2", "file_url": "https://s3/y", "filename": "b.pdf"},
        ])
    assert "hi" in out
    assert "<oc-file:f2>" in out
    assert "b.pdf" in out
```

- [ ] **Step 2: 运行确认失败**

Run（用引擎 venv python，或 system python + sys.path）：
```bash
cd runtime/hermes/hermes-v2026.6.5
PYTHONPATH=. python -m pytest tests/test_conversation_files.py -v
```
Expected: FAIL（模块不存在）。

- [ ] **Step 3: 实现 materialize_files**

`runtime/hermes/hermes-v2026.6.5/ocops/conversation_files.py`：
```python
# ocops/conversation_files.py —— 把对话消息里的 input_file part 落到 agent 共享盘并改写为文字注记。
#
# manager 发来的消息含 input_file part（带预签名 file_url）。oc-ops 与 hermes 同 pod 共享
# /opt/data；这里下载文件、用引擎自带 cache_media_bytes 落到 agent 可见缓存路径，再把 part
# 改写成「文字注记 + <oc-file:file_id> 标记」拼进文字内容。注记里的路径让 agent 用文件工具/
# vision_analyze 读取；<oc-file:id> 标记供 manager 前端解析渲染历史文件卡片。
#
# 设计要点：
# - cache_media_bytes 为引擎模块函数，**延迟 import**（在函数内），便于单测 mock，且避免在不含
#   hermes-agent 的环境（如纯 ocops 单测）import 失败。
# - 任一文件下载/落盘失败只降级为「不可用」注记，不让整轮 chat 失败。
import json
import urllib.request

# 下载单文件的超时与大小上限（与 manager 端 100MB 上限呼应，留余量）。
_DOWNLOAD_TIMEOUT_SEC = 120
_MAX_BYTES = 120 * 1024 * 1024


def _download(url: str) -> bytes:
    """HTTP GET 预签名 URL 下载字节；超时或超限抛异常。"""
    req = urllib.request.Request(url, method="GET")
    with urllib.request.urlopen(req, timeout=_DOWNLOAD_TIMEOUT_SEC) as resp:
        data = resp.read(_MAX_BYTES + 1)
    if len(data) > _MAX_BYTES:
        raise RuntimeError("conversation file exceeds size limit")
    return data


def _cache_media_bytes(data: bytes, filename: str):
    """延迟 import 引擎 cache_media_bytes，落盘并返回 CachedMedia（path/kind/display_name）。"""
    import sys
    if "/usr/local/lib/hermes-agent" not in sys.path:
        sys.path.insert(0, "/usr/local/lib/hermes-agent")
    from gateway.platforms.base import cache_media_bytes
    return cache_media_bytes(data, filename=filename)


def _note_for(part: dict) -> str:
    """把一个 input_file part 处理成一段文字注记（含 <oc-file:id> 标记）。"""
    file_id = str(part.get("file_id") or "")
    filename = str(part.get("filename") or "file")
    url = str(part.get("file_url") or "")
    marker = f"<oc-file:{file_id}>"
    if not url:
        return f"[The user attached '{filename}', but it could not be loaded.] {marker}"
    try:
        data = _download(url)
        cached = _cache_media_bytes(data, filename)
    except Exception:
        return f"[The user attached '{filename}', but it could not be loaded.] {marker}"
    if cached is None:
        return f"[The user attached '{filename}', but its type is not supported.] {marker}"
    kind = getattr(cached, "kind", "file")
    path = getattr(cached, "path", "")
    return (
        f"[The user sent a {kind}: '{filename}'. The file is saved at: {path}. "
        f"Ask the user what they'd like you to do with it.] {marker}"
    )


def materialize_files(message):
    """把消息里的 input_file part 落盘并改写为文字。

    message 为字符串时原样返回；为 parts 数组时：text part 取文字、input_file part 转注记，
    注记置于文字之前，整体返回为单个字符串（api_server 接受字符串 message）。
    其它形态原样返回。
    """
    if isinstance(message, str):
        return message
    if not isinstance(message, list):
        return message
    text_segments = []
    note_segments = []
    for raw in message:
        if not isinstance(raw, dict):
            continue
        ptype = raw.get("type")
        if ptype == "text":
            t = str(raw.get("text") or "")
            if t:
                text_segments.append(t)
        elif ptype == "input_file":
            note_segments.append(_note_for(raw))
    base_text = "\n".join(text_segments)
    notes = "\n".join(note_segments)
    if notes and base_text:
        return f"{notes}\n\n{base_text}"
    return notes or base_text
```

- [ ] **Step 4: 运行确认通过**

Run:
```bash
cd runtime/hermes/hermes-v2026.6.5
PYTHONPATH=. python -m pytest tests/test_conversation_files.py -v
```
Expected: PASS（3 用例）。

- [ ] **Step 5: Commit**

```bash
git add runtime/hermes/hermes-v2026.6.5/ocops/conversation_files.py runtime/hermes/hermes-v2026.6.5/tests/test_conversation_files.py
git commit -m "feat(conversation): oc-ops 落盘改写对话文件 part

下载预签名 URL、用引擎 cache_media_bytes 落到 agent 共享盘缓存，把 input_file
part 改写为文字注记 + <oc-file:id> 标记；下载失败降级不阻断整轮。"
```

---

### Task 9: 接入 conversation.chat / chat_stream

**Files:**
- Modify: `runtime/hermes/hermes-v2026.6.5/ocops/conversation.py`（chat / chat_stream）
- Test: `runtime/hermes/hermes-v2026.6.5/tests/test_conversation.py`（追加用例）

- [ ] **Step 1: 写失败测试**

在 `tests/test_conversation.py` 追加：
```python
# chat 转发前对 body["message"] 调 materialize_files：parts 数组被改写成字符串。
def test_chat_materializes_file_parts():
    captured = {}
    def fake_json(method, path, body=None):
        captured["body"] = body
        return {"session_id": "s1", "message": {"role": "assistant", "content": "ok"}}
    with mock.patch.object(conversation, "_json", side_effect=fake_json), \
         mock.patch("ocops.conversation_files.materialize_files", return_value="材料化后的文字"):
        conversation.chat("s1", {"message": [{"type": "text", "text": "hi"}]})
    assert captured["body"]["message"] == "材料化后的文字"
```

- [ ] **Step 2: 运行确认失败**

Run: `cd runtime/hermes/hermes-v2026.6.5 && PYTHONPATH=. python -m pytest tests/test_conversation.py::test_chat_materializes_file_parts -v`
Expected: FAIL。

- [ ] **Step 3: 实现接入**

`ocops/conversation.py`：在文件顶部 import 区加：
```python
from ocops import conversation_files
```
改 `chat`：
```python
def chat(session_id: str, body: dict) -> dict:
    """单轮续聊（非流式），body 含 message（文字/文件 parts）。返回 assistant 回复对象。"""
    sid = urllib.parse.quote(session_id, safe="")
    body = dict(body)
    body["message"] = conversation_files.materialize_files(body.get("message"))
    return _json("POST", f"/api/sessions/{sid}/chat", body)
```
改 `chat_stream`：在构造请求 body 前同样改写：
```python
def chat_stream(session_id: str, body: dict):
    sid = urllib.parse.quote(session_id, safe="")
    body = dict(body)
    body["message"] = conversation_files.materialize_files(body.get("message"))
    url = _API_BASE + f"/api/sessions/{sid}/chat/stream"
    req = urllib.request.Request(url, data=json.dumps(body).encode(), method="POST")
    # ...（其余不变）
```

- [ ] **Step 4: 运行确认通过**

Run: `cd runtime/hermes/hermes-v2026.6.5 && PYTHONPATH=. python -m pytest tests/test_conversation.py -v`
Expected: PASS（含原有用例）。

- [ ] **Step 5: Commit**

```bash
git add runtime/hermes/hermes-v2026.6.5/ocops/conversation.py runtime/hermes/hermes-v2026.6.5/tests/test_conversation.py
git commit -m "feat(conversation): chat/chat_stream 转发前落盘改写文件 part"
```

---

### Task 10: 同步到 hermes-v2026.5.16 variant

**Files:**
- Create: `runtime/hermes/hermes-v2026.5.16/ocops/conversation_files.py`
- Create: `runtime/hermes/hermes-v2026.5.16/tests/test_conversation_files.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/ocops/conversation.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/tests/test_conversation.py`

- [ ] **Step 1: 复制文件**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
cp runtime/hermes/hermes-v2026.6.5/ocops/conversation_files.py runtime/hermes/hermes-v2026.5.16/ocops/conversation_files.py
cp runtime/hermes/hermes-v2026.6.5/tests/test_conversation_files.py runtime/hermes/hermes-v2026.5.16/tests/test_conversation_files.py
```

- [ ] **Step 2: 在 5.16 的 conversation.py 应用同样改动**

按 Task 9 Step 3 对 `runtime/hermes/hermes-v2026.5.16/ocops/conversation.py` 的 `chat`/`chat_stream` 做同样修改，并把 Task 9 Step 1 的测试用例追加到 `runtime/hermes/hermes-v2026.5.16/tests/test_conversation.py`。
> 注意：5.16 的 conversation.py 行为可能与 6.5 略有差异（见 [[project-conversation-516-gap]]）；以该 variant 现有 chat/chat_stream 实际代码为锚点插入 materialize，不要照搬行号。

- [ ] **Step 3: 校验 5.16 的 cache_media_bytes 签名一致**

Run（若本地有运行中的 5.16 实例 pod，否则在评审中标注待构建期验证）：
```bash
POD=$(rtk proxy kubectl get pods -n oc-apps -o name | grep app- | head -1 | sed 's#pod/##')
rtk proxy kubectl exec -n oc-apps $POD -c oc-ops -- /usr/local/lib/hermes-agent/venv/bin/python -c \
 "import sys; sys.path.insert(0,'/usr/local/lib/hermes-agent'); from gateway.platforms.base import cache_media_bytes; import inspect; print(inspect.signature(cache_media_bytes))"
```
Expected: 打印含 `data` 与 `filename=` 关键字参数的签名；若不一致，在 `_cache_media_bytes` 内做兼容适配。

- [ ] **Step 4: 运行两 variant 测试**

Run:
```bash
cd runtime/hermes/hermes-v2026.5.16 && PYTHONPATH=. python -m pytest tests/test_conversation_files.py tests/test_conversation.py -v
```
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add runtime/hermes/hermes-v2026.5.16/
git commit -m "feat(conversation): 5.16 variant 同步对话文件落盘改写"
```

---

## Phase 5 — 前端

### Task 11: conversations.ts 类型与上传/下载 API

**Files:**
- Modify: `web/src/api/conversations.ts`
- Test: `web/src/api/conversations.spec.ts`（新建）

- [ ] **Step 1: 写失败测试**

`web/src/api/conversations.spec.ts`：
```ts
import { describe, expect, it, vi } from 'vitest'

vi.mock('@/api/xhrUpload', () => ({ xhrUpload: vi.fn() }))
import { xhrUpload } from '@/api/xhrUpload'
import { uploadConversationFile, conversationFileDownloadUrl } from './conversations'

// 上传：调 xhrUpload 到 files 端点（octet-stream，带 filename query），返回 file_id 元数据。
describe('uploadConversationFile', () => {
  it('上传到 files 端点并返回元数据', async () => {
    vi.mocked(xhrUpload).mockResolvedValue({ status: 200, body: { file_id: 'f1', filename: 'a.pdf', mime: 'application/pdf', size: 3 } })
    const res = await uploadConversationFile('app1', 's1', new File(['abc'], 'a.pdf'))
    expect(res.file_id).toBe('f1')
    const [url, opts] = vi.mocked(xhrUpload).mock.calls[0]
    expect(url).toContain('/api/v1/apps/app1/hermes/conversations/s1/files?filename=')
    expect(opts.method).toBe('POST')
  })
})

// 下载 URL 拼装正确（前端用 <a href> / <img src> 指向它）。
describe('conversationFileDownloadUrl', () => {
  it('拼出下载端点', () => {
    expect(conversationFileDownloadUrl('app1', 's1', 'f1'))
      .toBe('/api/v1/apps/app1/hermes/conversations/s1/files/f1')
  })
})
```

- [ ] **Step 2: 运行确认失败**

Run: `npm run test -- conversations.spec.ts`
Expected: FAIL（函数未定义）。

- [ ] **Step 3: 实现**

`web/src/api/conversations.ts`：
1) 顶部 import 加 `import { xhrUpload } from '@/api/xhrUpload'`。
2) 扩展消息 part 类型与上传/下载 API：
```ts
// ConversationFilePart 是用户发送的文件 part；file_id 来自上传返回，发送时随消息带上。
export interface ConversationFilePart {
  type: 'input_file'
  file_id: string
  filename: string
  mime?: string
}

// ConversationTextPart 文字 part。
export interface ConversationTextPart {
  type: 'text'
  text: string
}

export type ConversationPart = ConversationTextPart | ConversationFilePart

// ConversationFileMeta 是上传成功返回的文件元数据。
export interface ConversationFileMeta {
  file_id: string
  filename: string
  mime: string
  size: number
}

// uploadConversationFile 上传单个文件到会话，返回 file_id 等元数据。
export async function uploadConversationFile(
  appId: string,
  sid: string,
  file: File,
  onProgress?: (loaded: number, total: number) => void,
  signal?: AbortSignal,
): Promise<ConversationFileMeta> {
  const params = new URLSearchParams({ filename: file.name })
  const r = await xhrUpload(
    `${base(appId)}/${encodeURIComponent(sid)}/files?${params.toString()}`,
    { method: 'POST', headers: { 'Content-Type': 'application/octet-stream' }, body: file, onProgress, signal },
  )
  return r.body as ConversationFileMeta
}

// conversationFileDownloadUrl 返回历史文件的下载/预览 URL（manager 302 跳预签名）。
export function conversationFileDownloadUrl(appId: string, sid: string, fileId: string): string {
  return `${base(appId)}/${encodeURIComponent(sid)}/files/${encodeURIComponent(fileId)}`
}
```
3) 改 `chatStream` 的 `message` 参数类型 `string` → `string | ConversationPart[]`，body 不变（`JSON.stringify({ message })`）。

- [ ] **Step 4: 运行确认通过**

Run: `npm run test -- conversations.spec.ts`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add web/src/api/conversations.ts web/src/api/conversations.spec.ts
git commit -m "feat(conversation): 前端对话文件上传/下载 API 与 part 类型"
```

---

### Task 12: 输入框文件选择/拖拽与发送组装

**Files:**
- Modify: `web/src/pages/apps/AppConversationsTab.vue`

- [ ] **Step 1: 模板加文件选择按钮 + 已选文件列表 + 拖拽**

在 `.composer` 区（输入框上方）加（参照 `OrgKnowledgePage.vue` 的 input/drag 模式）：
```vue
<div class="composer-files" v-if="pendingFiles.length">
  <n-tag v-for="(f, i) in pendingFiles" :key="i" closable @close="removePendingFile(i)">
    {{ f.name }}
  </n-tag>
</div>
<label class="attach-button">
  <input class="hidden-input" type="file" multiple :disabled="!currentId || sending" @change="onPickFiles" />
  {{ t('apps.conversations.attach') }}
</label>
```

- [ ] **Step 2: 脚本加待发送文件状态与处理**

```ts
import { uploadConversationFile, type ConversationPart } from '@/api/conversations'

const pendingFiles = ref<File[]>([])

function onPickFiles(e: Event) {
  const input = e.target as HTMLInputElement
  if (input.files) pendingFiles.value.push(...Array.from(input.files))
  input.value = ''
}
function removePendingFile(i: number) {
  pendingFiles.value.splice(i, 1)
}
```

- [ ] **Step 3: 改 onSend 组装多模态 parts**

```ts
async function onSend() {
  const text = draft.value.trim()
  const files = pendingFiles.value
  if ((!text && files.length === 0) || !currentId.value || sending.value) return

  sending.value = true
  draft.value = ''
  pendingFiles.value = []

  try {
    // 先逐个上传文件，拿到 file_id。
    const fileParts: ConversationPart[] = []
    for (const f of files) {
      const meta = await uploadConversationFile(props.appId, currentId.value, f)
      fileParts.push({ type: 'input_file', file_id: meta.file_id, filename: meta.filename, mime: meta.mime })
    }
    // 组装消息：有文件则用 parts 数组，否则纯文字字符串（保持与旧行为一致）。
    const message: string | ConversationPart[] =
      fileParts.length > 0
        ? [...(text ? [{ type: 'text', text } as ConversationPart] : []), ...fileParts]
        : text

    // 乐观推入用户消息（展示文字 + 文件名）。
    messages.value.push({ role: 'user', content: message })
    const asst = reactive<api.ConversationMessage>({ role: 'assistant', content: '' })
    messages.value.push(asst)
    await scrollToBottom()

    await api.chatStream(props.appId, currentId.value, message, {
      onDelta: (d) => { asst.content = (asst.content as string) + d; void scrollToBottom() },
      onDone: () => {},
      onError: (m) => { message.error(m) },
    })
    await selectSession(currentId.value)
  } catch (e) {
    message.error(e instanceof Error ? e.message : String(e))
  } finally {
    sending.value = false
  }
}
```
> 发送按钮 `:disabled` 条件改为 `!currentId || sending || (!draft.trim() && pendingFiles.length === 0)`。
> 新增 i18n key `apps.conversations.attach`（中/英）放对应 locale 文件。

- [ ] **Step 4: 构建确认**

Run: `npm run build`（或 `npm run type-check`）
Expected: 通过，无类型错误。

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/apps/AppConversationsTab.vue web/src/locales/
git commit -m "feat(conversation): 对话输入框支持选/拖文件并随消息发送"
```

---

### Task 13: 历史消息渲染文件（input_file part + <oc-file:id> 标记）

**Files:**
- Modify: `web/src/pages/apps/ConversationMessageView.vue`
- Modify: `web/src/domain/conversation.ts`（`hasRenderableContent` 认 input_file + 文字里的标记）
- Test: `web/src/domain/conversation.spec.ts`（若存在则追加，否则新建）

- [ ] **Step 1: 写失败测试（domain）**

`web/src/domain/conversation.spec.ts` 追加：
```ts
import { describe, expect, it } from 'vitest'
import { hasRenderableContent } from './conversation'

// 含 input_file part 的消息可渲染。
it('input_file part 视为可渲染', () => {
  expect(hasRenderableContent([{ type: 'input_file', file_id: 'f1' }])).toBe(true)
})
```

- [ ] **Step 2: 运行确认失败**

Run: `npm run test -- conversation.spec.ts`
Expected: FAIL。

- [ ] **Step 3: 改 domain**

`web/src/domain/conversation.ts` 的 `hasRenderableContent` 在 array 分支加：
```ts
      if (part.type === 'input_file') return true
```

- [ ] **Step 4: 改渲染组件**

`web/src/pages/apps/ConversationMessageView.vue`：
1) props 加可选 `appId` / `sessionId`（渲染下载链接需要）：
```ts
const props = defineProps<{ message: ConversationMessage; appId?: string; sessionId?: string }>()
```
（调用方 `AppConversationsTab.vue` 的 `<ConversationMessageView>` 传 `:app-id="props.appId" :session-id="currentId"`。）
2) array 分支增加 `input_file` part 渲染（图片预览 / 文档卡片）：
```vue
      <template v-else-if="p.type === 'input_file'">
        <img
          v-if="isImageFile(p)"
          :src="fileUrl(p)"
          alt=""
          class="msg-image"
        />
        <a v-else class="file-card" :href="fileUrl(p)" target="_blank" rel="noopener">
          📎 {{ p.filename || 'file' }}
        </a>
      </template>
```
3) 文字内容里的 `<oc-file:id>` 标记：渲染文字时把标记替换为可点击的文件卡片。最简做法——在 `renderMarkdown` 前先把字符串内容里的标记抽出并单独渲染卡片。新增脚本：
```ts
import { conversationFileDownloadUrl } from '@/api/conversations'

// 从文字里解析所有 <oc-file:id> 标记，返回 fileId 列表与剥离标记后的纯文字。
function parseFileMarkers(text: string): { fileIds: string[]; clean: string } {
  const fileIds: string[] = []
  const clean = text.replace(/<oc-file:([^>]+)>/g, (_m, id) => { fileIds.push(id); return '' }).trim()
  return { fileIds, clean }
}

function isImageFile(p: { filename?: string; mime?: string }): boolean {
  const mime = p.mime ?? ''
  if (mime.startsWith('image/')) return true
  return /\.(jpe?g|png|gif|webp|bmp)$/i.test(p.filename ?? '')
}

function fileUrl(p: { file_id?: string }): string {
  if (!props.appId || !props.sessionId || !p.file_id) return ''
  return conversationFileDownloadUrl(props.appId, props.sessionId, p.file_id)
}

// 文字里标记对应的文件卡片 URL。
function markerUrl(fileId: string): string {
  if (!props.appId || !props.sessionId) return ''
  return conversationFileDownloadUrl(props.appId, props.sessionId, fileId)
}
```
4) 字符串内容渲染分支改为：先 `parseFileMarkers`，渲染 `clean` 文字（保持 assistant markdown / user 纯文本），再对每个 fileId 渲染一个文件卡片 `<a class="file-card" :href="markerUrl(id)">📎 {{ id }}</a>`。
> 历史里用户发的文件（经 oc-ops）以 `<oc-file:id>` 标记出现在 assistant/user 文字中；filename 在 transcript 注记文本里也有，但稳定渲染以标记为准、文件名经下载端点 302 后由 S3 决定。卡片显示文件名可后续增强（manager 下载端点可加 `Content-Disposition`）。v1 卡片显示「📎 文件」即可，点击下载。

- [ ] **Step 5: 运行测试 + 构建**

Run: `npm run test -- conversation.spec.ts && npm run build`
Expected: PASS + 构建通过。

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/apps/ConversationMessageView.vue web/src/domain/conversation.ts web/src/domain/conversation.spec.ts web/src/pages/apps/AppConversationsTab.vue
git commit -m "feat(conversation): 历史渲染文件 part 与 <oc-file:id> 标记为可下载卡片/图片"
```

---

## Phase 6 — 端到端验证

### Task 14: 全量测试与生成物校验

- [ ] **Step 1: 后端全量测试**

Run: `go test ./...`
Expected: PASS。

- [ ] **Step 2: 前端全量测试 + 构建**

Run: `npm run test && npm run build`
Expected: PASS。

- [ ] **Step 3: OpenAPI 同步校验**

Run: `make openapi-check`
Expected: 工作区干净（yaml 与代码同步）。

- [ ] **Step 4: Commit（如有生成物变更）**

```bash
git add -A && git commit -m "chore(conversation): 同步生成物与全量测试" || echo "无变更"
```

### Task 15: 真实浏览器全角色验证（交付前必做）

> 依据 [[feedback_verification-rigor]]：必须用真实浏览器、覆盖三角色、带证据。本地 k3d 环境见 [[local-k3d-env]]。

- [ ] **Step 1: 本地起环境并部署改动**

构建并部署 manager + 两 variant 引擎镜像到本地 k3d（按项目 runbook：`make local-up` / 重新构建镜像并 rollout）。确认 presigned URL 的 S3 endpoint 为引擎 pod 可达地址（**非 `*.localhost`/127.0.0.1**；见 [[project-local-k3d-host-internal-dns]]）。

- [ ] **Step 2: 平台管理员（admin / 组织留空）**

登录 http://ocm.localhost（admin/admin123）。进入某实例对话页：
- 发一个 **PDF/Word 文档** + 一句话 → 确认 AI 能读到内容并回应。
- 发一张 **图片** → 确认 AI 能描述图片。
- 刷新页面看历史：文件卡片/图片渲染、点击可下载。

- [ ] **Step 3: org_admin 与 org_member**

用本地 org 账号（见 [[feedback_verification-rigor]] 里的 org 账号）分别验证：
- org_admin 对本组织实例可上传/发送/下载；
- org_member 对自有实例可用，对非自有实例上传/下载返回 403。

- [ ] **Step 4: 异常路径**

- 上传不支持类型（如 .exe）→ 前端拦截 + 后端 400。
- 超大文件（>100MB）→ 413。
- 越权下载（改 URL 里的 fileId 为他人文件）→ 404。

- [ ] **Step 5: 记录验证矩阵**

整理逐项/逐角色验证结果（含截图或网络请求证据），写入交付说明。发现问题先修复再重验，直到全绿。

---

## Self-Review（计划作者已核对）

- **Spec 覆盖**：发送（Phase 3+4+5 Task 11/12）✓；历史渲染下载（Task 6 下载端点 + Task 13 渲染）✓；oc-ops 落盘不改引擎（Task 8/9/10）✓；conversation_files 表 + 解耦下载（Task 1/2/5/6）✓；两 variant（Task 10）✓；类型/大小约束（Task 4 常量 + Task 12/13 前端）✓；权限谓词复用（Task 4/6 用 CanView/CanManageAppConversations）✓；本地 k3d S3 可达 gotcha（Task 7 Step 6 + Task 15）✓；图片统一走共享盘路径（Task 8 cache_media_bytes 分类，前端 input_file 渲染）✓。
- **占位扫描**：Task 5 注明 `convFileRecord`→`ConvFileRecord` 导出改名（含交叉影响）；Task 6 handler 接口签名以「与 service 完全一致」明确替换占位类型；均给出真实正文。
- **类型一致**：`ConversationFileUploadResult` / `ConvFileRecord` / `ConversationFileResolver.ResolveFileURL` / `materialize_files` / `<oc-file:id>` 标记格式在前后端与 oc-ops 间一致；前端 `conversationFileDownloadUrl` 与后端路由 `/:sid/files/:fileId` 对齐。

> 已知需落地时确认项（非阻塞，已在对应步骤标注）：`*Store.Queries()` 访问方式、`uuid` 包用法、router.go 装配处 S3 ObjectStore 是否启用、5.16 `cache_media_bytes` 签名一致性。
