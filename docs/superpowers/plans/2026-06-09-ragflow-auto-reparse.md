# RAGFlow Auto Reparse Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Automatically reparse RAGFlow documents that failed due to transient embedding model overload errors, including matching historical failures.

**Architecture:** Add retry metadata to `ragflow_documents`, generate sqlc accessors, then extend the existing `ragflow_parse_status_refresh` background task with a second phase that reparses due failed documents. Retry eligibility is limited to the model overload whitelist and capped at three successful automatic submissions with persisted cooldown timestamps.

**Tech Stack:** Go, MySQL migrations, sqlc, RAGFlow HTTP client, testify, Makefile.

---

## File Map

| Path | Change |
|---|---|
| `internal/migrations/000008_ragflow_auto_reparse.up.sql` | Add retry columns, index, and historical failure backfill. |
| `internal/migrations/000008_ragflow_auto_reparse.down.sql` | Drop index and retry columns. |
| `internal/migrations/migrations_test.go` | Assert migration 8 includes retry columns, backfill, and down rollback. |
| `sqlc.yaml` | Add migration 8 to sqlc schema list. |
| `internal/store/queries/ragflow_knowledge.sql` | Add auto-reparse queries and reset retry metadata in existing reparse-related updates. |
| `internal/store/sqlc/*` | Regenerated sqlc output. |
| `internal/service/ragflow_parse_status_refresher.go` | Add retry classification, cooldown calculation, and automatic reparse phase. |
| `internal/service/ragflow_parse_status_refresher_test.go` | Cover automatic retry behavior and non-retry cases. |
| `internal/service/knowledge_service.go` | Use a reset query for manual reparse so manual action clears retry metadata. |
| `internal/service/knowledge_service_test.go` | Update fake store for new interface method and assert manual reparse resets retry metadata. |

No handler route, OpenAPI, or frontend files change.

---

### Task 1: Migration And Sqlc Queries

**Files:**
- Create: `internal/migrations/000008_ragflow_auto_reparse.up.sql`
- Create: `internal/migrations/000008_ragflow_auto_reparse.down.sql`
- Modify: `internal/migrations/migrations_test.go`
- Modify: `sqlc.yaml`
- Modify: `internal/store/queries/ragflow_knowledge.sql`
- Regenerate: `internal/store/sqlc/models.go`
- Regenerate: `internal/store/sqlc/querier.go`
- Regenerate: `internal/store/sqlc/ragflow_knowledge.sql.go`

- [ ] **Step 1: Write the failing migration test**

Add this test to `internal/migrations/migrations_test.go`:

```go
// TestRAGFlowAutoReparseMigrationDeclaresRetryState 验证自动重解析迁移声明重试状态、索引、存量回填和回滚语句。
func TestRAGFlowAutoReparseMigrationDeclaresRetryState(t *testing.T) {
	upBytes, err := FS.ReadFile("000008_ragflow_auto_reparse.up.sql")
	require.NoError(t, err)
	up := string(upBytes)

	// 自动重解析需要持久化次数和下次可重试时间，避免服务重启后丢失冷却状态。
	assert.Contains(t, up, "auto_reparse_attempts INT NOT NULL DEFAULT 0")
	assert.Contains(t, up, "auto_reparse_next_at DATETIME(6) NULL")
	assert.Contains(t, up, "idx_ragflow_documents_auto_reparse")

	// 存量模型过载失败必须被回填为立即可重试，但迁移本身不直接调用 RAGFlow。
	assert.Contains(t, up, "SET auto_reparse_next_at = NOW(6)")
	assert.Contains(t, up, "LOWER(last_error) LIKE '%model service overloaded%'")
	assert.Contains(t, up, "LOWER(last_error) LIKE '%error code: 503%'")
	assert.Contains(t, up, "LOWER(last_error) LIKE '%code: 50505%'")

	downBytes, err := FS.ReadFile("000008_ragflow_auto_reparse.down.sql")
	require.NoError(t, err)
	down := string(downBytes)

	// down 迁移必须先删索引再删列，保证本地回滚最近一次迁移可用。
	assert.Contains(t, down, "DROP INDEX idx_ragflow_documents_auto_reparse")
	assert.Contains(t, down, "DROP COLUMN auto_reparse_next_at")
	assert.Contains(t, down, "DROP COLUMN auto_reparse_attempts")
}
```

- [ ] **Step 2: Run the migration test and verify it fails**

Run:

```bash
rtk go test ./internal/migrations -run TestRAGFlowAutoReparseMigrationDeclaresRetryState -count=1
```

Expected: FAIL because migration 8 files do not exist yet.

- [ ] **Step 3: Add migration 8 up/down files**

Create `internal/migrations/000008_ragflow_auto_reparse.up.sql`:

```sql
ALTER TABLE ragflow_documents
    ADD COLUMN auto_reparse_attempts INT NOT NULL DEFAULT 0 AFTER last_error,
    ADD COLUMN auto_reparse_next_at DATETIME(6) NULL AFTER auto_reparse_attempts,
    ADD KEY idx_ragflow_documents_auto_reparse (
        parse_status,
        auto_reparse_next_at,
        auto_reparse_attempts,
        updated_at
    );

UPDATE ragflow_documents
SET auto_reparse_next_at = NOW(6)
WHERE parse_status = 'failed'
  AND auto_reparse_attempts = 0
  AND last_error IS NOT NULL
  AND (
      LOWER(last_error) LIKE '%model service overloaded%'
      OR LOWER(last_error) LIKE '%error code: 503%'
      OR LOWER(last_error) LIKE '%code: 50505%'
  );
```

Create `internal/migrations/000008_ragflow_auto_reparse.down.sql`:

```sql
ALTER TABLE ragflow_documents
    DROP INDEX idx_ragflow_documents_auto_reparse,
    DROP COLUMN auto_reparse_next_at,
    DROP COLUMN auto_reparse_attempts;
```

- [ ] **Step 4: Add migration 8 to sqlc schema**

In `sqlc.yaml`, append migration 8 after `000007_industry_knowledge.up.sql`:

```yaml
      - internal/migrations/000008_ragflow_auto_reparse.up.sql
```

- [ ] **Step 5: Update ragflow sqlc queries**

In `internal/store/queries/ragflow_knowledge.sql`, add these queries after `UpdateRAGFlowDocumentParseStatus`:

```sql
-- name: MarkRAGFlowDocumentFailedWithAutoReparse :exec
-- 写入解析失败状态，并在可自动重试时设置下一次允许重试的时间；next_at 为空表示不再自动重试。
UPDATE ragflow_documents
SET parse_status = 'failed',
    progress = ?,
    last_error = ?,
    auto_reparse_next_at = ?,
    updated_at = now()
WHERE id = ?;

-- name: MarkRAGFlowDocumentAutoReparseQueued :exec
-- 自动重解析提交成功后累计次数并清空冷却时间；次数只统计已成功提交给 RAGFlow 的重试。
UPDATE ragflow_documents
SET parse_status = 'queued',
    progress = 0,
    last_error = NULL,
    auto_reparse_attempts = auto_reparse_attempts + 1,
    auto_reparse_next_at = NULL,
    updated_at = now()
WHERE id = ?
  AND parse_status = 'failed'
  AND auto_reparse_attempts < 3;

-- name: MarkRAGFlowDocumentManualReparseQueued :exec
-- 人工重解析表示用户显式介入，应清空历史自动重试状态，避免旧次数影响新的解析周期。
UPDATE ragflow_documents
SET parse_status = 'queued',
    progress = 0,
    last_error = NULL,
    auto_reparse_attempts = 0,
    auto_reparse_next_at = NULL,
    updated_at = now()
WHERE id = ?;
```

In the existing `ResetRAGFlowDocumentsParseStatusByDataset` query, replace its `SET` clause with:

```sql
SET parse_status = 'queued',
    progress = 0,
    last_error = NULL,
    auto_reparse_attempts = 0,
    auto_reparse_next_at = NULL,
    updated_at = now()
```

In the existing `ReplaceRAGFlowIndustryDocument` query, add these assignments before `updated_at = now()`:

```sql
    auto_reparse_attempts = 0,
    auto_reparse_next_at = NULL,
```

Add this query near `ListRAGFlowDocumentsNeedingRefresh`:

```sql
-- name: ListRAGFlowDocumentsDueForAutoReparse :many
-- 找出已到冷却时间的模型过载失败文档；远端 dataset 必须存在，否则无法调用 RAGFlow parse。
SELECT d.*, ds.ragflow_dataset_id AS remote_dataset_id
FROM ragflow_documents d
JOIN ragflow_datasets ds ON ds.id = d.dataset_id
WHERE d.parse_status = 'failed'
  AND d.auto_reparse_attempts < 3
  AND d.auto_reparse_next_at IS NOT NULL
  AND d.auto_reparse_next_at <= NOW(6)
  AND ds.ragflow_dataset_id IS NOT NULL
ORDER BY d.auto_reparse_next_at ASC, d.updated_at ASC
LIMIT ?;
```

- [ ] **Step 6: Regenerate sqlc**

Run:

```bash
rtk make sqlc-generate
```

Expected: PASS. Generated output includes:

- `sqlc.RagflowDocument.AutoReparseAttempts int32`
- `sqlc.RagflowDocument.AutoReparseNextAt null.Time`
- `ListRAGFlowDocumentsDueForAutoReparse`
- `MarkRAGFlowDocumentFailedWithAutoReparse`
- `MarkRAGFlowDocumentAutoReparseQueued`
- `MarkRAGFlowDocumentManualReparseQueued`

- [ ] **Step 7: Run migration/sqlc-focused verification**

Run:

```bash
rtk go test ./internal/migrations ./internal/store -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit migration and generated data access**

Run:

```bash
rtk git add sqlc.yaml internal/migrations/000008_ragflow_auto_reparse.up.sql internal/migrations/000008_ragflow_auto_reparse.down.sql internal/migrations/migrations_test.go internal/store/queries/ragflow_knowledge.sql internal/store/sqlc
rtk git commit -m "feat(knowledge): 增加 RAGFlow 自动重解析数据结构" -m "为知识库文档增加自动重解析次数和冷却时间字段。"
```

---

### Task 2: Refresher Auto-Reparse Tests

**Files:**
- Modify: `internal/service/ragflow_parse_status_refresher_test.go`

- [ ] **Step 1: Extend the refresher fakes for the new behavior**

In `internal/service/ragflow_parse_status_refresher_test.go`, extend `fakeRefresherStore` with these fields and methods:

```go
	autoRows        []sqlc.ListRAGFlowDocumentsDueForAutoReparseRow
	autoListCnt     int
	failedWithRetry []sqlc.MarkRAGFlowDocumentFailedWithAutoReparseParams
	autoQueuedIDs   []string

func (s *fakeRefresherStore) ListRAGFlowDocumentsDueForAutoReparse(_ context.Context, limit int32) ([]sqlc.ListRAGFlowDocumentsDueForAutoReparseRow, error) {
	s.autoListCnt++
	if int(limit) >= len(s.autoRows) {
		return s.autoRows, nil
	}
	return s.autoRows[:limit], nil
}

func (s *fakeRefresherStore) MarkRAGFlowDocumentFailedWithAutoReparse(_ context.Context, arg sqlc.MarkRAGFlowDocumentFailedWithAutoReparseParams) error {
	s.failedWithRetry = append(s.failedWithRetry, arg)
	return nil
}

func (s *fakeRefresherStore) MarkRAGFlowDocumentAutoReparseQueued(_ context.Context, id string) error {
	s.autoQueuedIDs = append(s.autoQueuedIDs, id)
	return nil
}
```

Extend `fakeRefresherRAGFlow` with parse recording:

```go
	parseCalls []ragflowParseCall
	parseErrs  map[string]error

type ragflowParseCall struct {
	datasetID   string
	documentIDs []string
}

func (f *fakeRefresherRAGFlow) ParseDocuments(_ context.Context, datasetID string, documentIDs []string) error {
	f.parseCalls = append(f.parseCalls, ragflowParseCall{datasetID: datasetID, documentIDs: append([]string(nil), documentIDs...)})
	if f.parseErrs != nil {
		return f.parseErrs[datasetID]
	}
	return nil
}
```

Add this helper near `makeRefreshRow`:

```go
func makeAutoReparseRow(id, datasetID, remoteDatasetID, remoteDocID string, attempts int32) sqlc.ListRAGFlowDocumentsDueForAutoReparseRow {
	row := sqlc.ListRAGFlowDocumentsDueForAutoReparseRow{
		ID:                   mustParseUUID(id),
		DatasetID:            mustParseUUID(datasetID),
		RagflowDocumentID:    remoteDocID,
		ParseStatus:          "failed",
		Progress:             0,
		AutoReparseAttempts:  attempts,
		AutoReparseNextAt:    null.TimeFrom(time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)),
		RemoteDatasetID:      null.StringFrom(remoteDatasetID),
	}
	return row
}
```

Add `time` to the test imports.

- [ ] **Step 2: Add failing tests for automatic reparse**

Add these tests:

```go
func TestRagflowParseStatusRefresher_AutoReparsesModelOverloadFailure(t *testing.T) {
	// 模型服务过载是临时上游失败：首次同步为 failed 后应立即进入自动重解析队列。
	store := &fakeRefresherStore{listRows: []sqlc.ListRAGFlowDocumentsNeedingRefreshRow{
		makeRefreshRow("00000000-0000-0000-0000-000000000a01", "00000000-0000-0000-0000-000000000d01", "remote-ds-1", "remote-doc-1", "running", 0),
	}}
	rf := &fakeRefresherRAGFlow{responses: map[string][]ragflow.Document{
		"remote-ds-1": {{ID: "remote-doc-1", Run: "FAIL", ProgressMsg: "15:42:06 [ERROR][Exception]: Error code: 503 - {'code': 50505, 'message': 'Model service overloaded. Please try again later.'}"}},
	}}
	refresher := NewRagflowParseStatusRefresher(store, rf)
	refresher.SetNowFunc(func() time.Time { return time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC) })

	require.NoError(t, refresher.Tick(context.Background()))
	require.Len(t, store.failedWithRetry, 1)
	require.True(t, store.failedWithRetry[0].AutoReparseNextAt.Valid)
	assert.Equal(t, time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC), store.failedWithRetry[0].AutoReparseNextAt.Time)
}

func TestRagflowParseStatusRefresher_RequeuesDueAutoReparse(t *testing.T) {
	// 存量或冷却到期的模型过载失败文件应被重新提交给 RAGFlow，并累计自动重试次数。
	store := &fakeRefresherStore{autoRows: []sqlc.ListRAGFlowDocumentsDueForAutoReparseRow{
		makeAutoReparseRow("00000000-0000-0000-0000-000000000a01", "00000000-0000-0000-0000-000000000d01", "remote-ds-1", "remote-doc-1", 0),
		makeAutoReparseRow("00000000-0000-0000-0000-000000000a02", "00000000-0000-0000-0000-000000000d01", "remote-ds-1", "remote-doc-2", 1),
	}}
	rf := &fakeRefresherRAGFlow{responses: map[string][]ragflow.Document{}}
	refresher := NewRagflowParseStatusRefresher(store, rf)

	require.NoError(t, refresher.Tick(context.Background()))
	require.Len(t, rf.parseCalls, 1)
	assert.Equal(t, "remote-ds-1", rf.parseCalls[0].datasetID)
	assert.ElementsMatch(t, []string{"remote-doc-1", "remote-doc-2"}, rf.parseCalls[0].documentIDs)
	assert.ElementsMatch(t, []string{"00000000-0000-0000-0000-000000000a01", "00000000-0000-0000-0000-000000000a02"}, store.autoQueuedIDs)
}

func TestRagflowParseStatusRefresher_NonOverloadFailureDoesNotScheduleAutoReparse(t *testing.T) {
	// 非白名单错误通常是文件或配置问题，不能自动重试。
	store := &fakeRefresherStore{listRows: []sqlc.ListRAGFlowDocumentsNeedingRefreshRow{
		makeRefreshRow("00000000-0000-0000-0000-000000000a01", "00000000-0000-0000-0000-000000000d01", "remote-ds-1", "remote-doc-1", "running", 0),
	}}
	rf := &fakeRefresherRAGFlow{responses: map[string][]ragflow.Document{
		"remote-ds-1": {{ID: "remote-doc-1", Run: "FAIL", ProgressMsg: "10:00:05 [ERROR] unsupported file type"}},
	}}
	refresher := NewRagflowParseStatusRefresher(store, rf)

	require.NoError(t, refresher.Tick(context.Background()))
	require.Len(t, store.updates, 1)
	assert.Empty(t, store.failedWithRetry)
	assert.Equal(t, "failed", store.updates[0].ParseStatus)
	assert.Contains(t, store.updates[0].LastError.String, "unsupported file type")
}

func TestAutoReparseNextAtBackoff(t *testing.T) {
	// 冷却时间按已成功提交的自动重试次数递增，避免过载未恢复时快速耗尽重试机会。
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)

	first := autoReparseNextAt(0, now)
	require.True(t, first.Valid)
	assert.Equal(t, now, first.Time)

	second := autoReparseNextAt(1, now)
	require.True(t, second.Valid)
	assert.Equal(t, now.Add(10*time.Minute), second.Time)

	third := autoReparseNextAt(2, now)
	require.True(t, third.Valid)
	assert.Equal(t, now.Add(30*time.Minute), third.Time)

	exhausted := autoReparseNextAt(3, now)
	assert.False(t, exhausted.Valid)
}

func TestRagflowParseStatusRefresher_AutoReparseParseErrorDoesNotIncrementAttempts(t *testing.T) {
	// RAGFlow parse 接口失败表示未成功提交重试，不能增加自动重试次数。
	store := &fakeRefresherStore{autoRows: []sqlc.ListRAGFlowDocumentsDueForAutoReparseRow{
		makeAutoReparseRow("00000000-0000-0000-0000-000000000a01", "00000000-0000-0000-0000-000000000d01", "remote-ds-1", "remote-doc-1", 0),
	}}
	rf := &fakeRefresherRAGFlow{
		responses: map[string][]ragflow.Document{},
		parseErrs: map[string]error{"remote-ds-1": errors.New("ragflow parse unavailable")},
	}
	refresher := NewRagflowParseStatusRefresher(store, rf)

	err := refresher.Tick(context.Background())
	require.Error(t, err)
	assert.Empty(t, store.autoQueuedIDs)
}
```

- [ ] **Step 3: Run tests and verify they fail**

Run:

```bash
rtk go test ./internal/service -run 'TestRagflowParseStatusRefresher|TestAutoReparseNextAtBackoff|TestExtractRAGFlowError' -count=1
```

Expected: FAIL with compile errors for missing generated sqlc types if Task 1 was not completed, or missing refresher methods/functions if Task 1 is complete.

Do not implement production code in this task.

---

### Task 3: Refresher Auto-Reparse Implementation

**Files:**
- Modify: `internal/service/ragflow_parse_status_refresher.go`
- Modify: `internal/service/ragflow_parse_status_refresher_test.go`

- [ ] **Step 1: Extend refresher interfaces and struct**

In `internal/service/ragflow_parse_status_refresher.go`, add `time` to imports.

Extend `RagflowParseStatusRefresherStore`:

```go
	// ListRAGFlowDocumentsDueForAutoReparse 找出已到冷却时间、可自动重解析的 failed 文档。
	ListRAGFlowDocumentsDueForAutoReparse(ctx context.Context, limit int32) ([]sqlc.ListRAGFlowDocumentsDueForAutoReparseRow, error)
	// MarkRAGFlowDocumentFailedWithAutoReparse 写入失败状态和下一次自动重试时间。
	MarkRAGFlowDocumentFailedWithAutoReparse(ctx context.Context, arg sqlc.MarkRAGFlowDocumentFailedWithAutoReparseParams) error
	// MarkRAGFlowDocumentAutoReparseQueued 在自动重解析提交成功后把文档重新置为 queued。
	MarkRAGFlowDocumentAutoReparseQueued(ctx context.Context, id string) error
```

Extend `RagflowParseStatusRefreshClient`:

```go
	ParseDocuments(ctx context.Context, datasetID string, documentIDs []string) error
```

Add `now func() time.Time` to `RagflowParseStatusRefresher`, initialize it in the constructor:

```go
		now: time.Now,
```

Add this setter after `SetPageSize`:

```go
func (r *RagflowParseStatusRefresher) SetNowFunc(fn func() time.Time) {
	if fn != nil {
		r.now = fn
	}
}
```

- [ ] **Step 2: Split Tick into refresh and auto-reparse phases**

Replace `Tick` with:

```go
func (r *RagflowParseStatusRefresher) Tick(ctx context.Context) error {
	firstErr := r.refreshQueuedAndRunningDocuments(ctx)
	if err := r.autoReparseDueFailedDocuments(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
```

Move the existing body of `Tick` into a new method named `refreshQueuedAndRunningDocuments`:

```go
func (r *RagflowParseStatusRefresher) refreshQueuedAndRunningDocuments(ctx context.Context) error {
	rows, err := r.store.ListRAGFlowDocumentsNeedingRefresh(ctx, r.batchSize)
	// keep the rest of the existing Tick body unchanged
}
```

Keep the existing Chinese comments with the moved code and update only wording that refers to `Tick` directly.

- [ ] **Step 3: Add retry classification and backoff helpers**

Add these helpers near `extractRAGFlowError`:

```go
const ragflowAutoReparseMaxAttempts int32 = 3

func isRAGFlowAutoReparseError(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "model service overloaded") ||
		strings.Contains(lower, "error code: 503") ||
		strings.Contains(lower, "code: 50505")
}

func autoReparseNextAt(attempts int32, now time.Time) null.Time {
	switch attempts {
	case 0:
		return null.TimeFrom(now)
	case 1:
		return null.TimeFrom(now.Add(10 * time.Minute))
	case 2:
		return null.TimeFrom(now.Add(30 * time.Minute))
	default:
		return null.Time{}
	}
}
```

- [ ] **Step 4: Schedule retry metadata when remote status fails with overload**

In `applyRemoteStatus`, replace the failed-status last error block with:

```go
	lastErr := null.String{}
	if status == "failed" {
		lastErr = null.StringFrom(extractRAGFlowError(remote.ProgressMsg))
		if isRAGFlowAutoReparseError(lastErr.String) {
			_ = r.store.MarkRAGFlowDocumentFailedWithAutoReparse(ctx, sqlc.MarkRAGFlowDocumentFailedWithAutoReparseParams{
				Progress:            progress,
				LastError:           lastErr,
				AutoReparseNextAt:   autoReparseNextAt(row.AutoReparseAttempts, r.now()),
				ID:                  row.ID,
			})
			return
		}
	}
```

After sqlc generation, `row.AutoReparseAttempts` exists on `ListRAGFlowDocumentsNeedingRefreshRow` because the query selects `d.*`.

- [ ] **Step 5: Implement automatic reparse phase**

Add this method:

```go
func (r *RagflowParseStatusRefresher) autoReparseDueFailedDocuments(ctx context.Context) error {
	rows, err := r.store.ListRAGFlowDocumentsDueForAutoReparse(ctx, r.batchSize)
	if err != nil {
		return fmt.Errorf("扫描待自动重解析文档失败: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	type localDoc struct {
		id       string
		remoteID string
	}
	byDataset := make(map[string][]localDoc, len(rows))
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		remoteDatasetID := row.RemoteDatasetID.String
		if _, ok := byDataset[remoteDatasetID]; !ok {
			order = append(order, remoteDatasetID)
		}
		byDataset[remoteDatasetID] = append(byDataset[remoteDatasetID], localDoc{id: row.ID, remoteID: row.RagflowDocumentID})
	}

	var firstErr error
	for _, datasetID := range order {
		group := byDataset[datasetID]
		remoteIDs := make([]string, 0, len(group))
		for _, doc := range group {
			remoteIDs = append(remoteIDs, doc.remoteID)
		}
		if err := r.ragflow.ParseDocuments(ctx, datasetID, remoteIDs); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("自动重解析 dataset %s 失败: %w", datasetID, err)
			}
			continue
		}
		for _, doc := range group {
			_ = r.store.MarkRAGFlowDocumentAutoReparseQueued(ctx, doc.id)
		}
	}
	return firstErr
}
```

- [ ] **Step 6: Run refresher tests and make them pass**

Run:

```bash
rtk go test ./internal/service -run 'TestRagflowParseStatusRefresher|TestAutoReparseNextAtBackoff|TestExtractRAGFlowError' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit refresher implementation**

Run:

```bash
rtk git add internal/service/ragflow_parse_status_refresher.go internal/service/ragflow_parse_status_refresher_test.go
rtk git commit -m "feat(knowledge): 自动重解析 RAGFlow 模型过载失败" -m "在解析状态刷新任务中识别模型服务过载错误，并对到期失败文档重新提交 RAGFlow 解析。"
```

---

### Task 4: Manual And Whole-Dataset Reset Paths

**Files:**
- Modify: `internal/service/knowledge_service.go`
- Modify: `internal/service/knowledge_service_test.go`
- Generated dependency from Task 1: `sqlc.MarkRAGFlowDocumentManualReparseQueued`

- [ ] **Step 1: Write a failing manual reparse reset test**

In `fakeKnowledgeStore`, add:

```go
	manualReparseQueuedIDs []string
```

Add this method:

```go
func (s *fakeKnowledgeStore) MarkRAGFlowDocumentManualReparseQueued(_ context.Context, id string) error {
	s.manualReparseQueuedIDs = append(s.manualReparseQueuedIDs, id)
	if doc, ok := s.docs[id]; ok {
		doc.ParseStatus = "queued"
		doc.Progress = 0
		doc.LastError = null.String{}
		doc.AutoReparseAttempts = 0
		doc.AutoReparseNextAt = null.Time{}
		s.docs[id] = doc
	}
	return nil
}
```

Add this test near `TestRAGFlowKnowledgeReparseOnlyFailedOrStopped`:

```go
func TestRAGFlowKnowledgeManualReparseResetsAutoRetryState(t *testing.T) {
	// 人工重解析代表用户显式处理失败文件，应清空自动重试次数和冷却时间。
	svc, store, fake := newKnowledgeTestService(t)
	doc := testDocument(t, "org", "failed.pdf", testKnowledgeDatasetID)
	doc.ParseStatus = "failed"
	doc.AutoReparseAttempts = 2
	doc.AutoReparseNextAt = null.TimeFrom(time.Date(2026, 6, 9, 10, 30, 0, 0, time.UTC))
	store.docs[doc.ID] = doc

	_, err := svc.ReparseOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, doc.ID)
	require.NoError(t, err)

	assert.Equal(t, []string{doc.ID}, store.manualReparseQueuedIDs)
	require.Len(t, fake.parseCalls, 1)
	got := store.docs[doc.ID]
	assert.Equal(t, "queued", got.ParseStatus)
	assert.Equal(t, int32(0), got.AutoReparseAttempts)
	assert.False(t, got.AutoReparseNextAt.Valid)
}
```

Add `time` to imports if it is not already present.

- [ ] **Step 2: Run the targeted test and verify it fails**

Run:

```bash
rtk go test ./internal/service -run TestRAGFlowKnowledgeManualReparseResetsAutoRetryState -count=1
```

Expected: FAIL because `KnowledgeStore` and `reparseDocument` still use `UpdateRAGFlowDocumentParseStatus`.

- [ ] **Step 3: Update KnowledgeStore and reparseDocument**

In `internal/service/knowledge_service.go`, extend `KnowledgeStore`:

```go
	// MarkRAGFlowDocumentManualReparseQueued 人工重解析入队并清空自动重试状态。
	MarkRAGFlowDocumentManualReparseQueued(ctx context.Context, id string) error
```

In `reparseDocument`, replace the `UpdateRAGFlowDocumentParseStatus` block with:

```go
	// MarkRAGFlowDocumentManualReparseQueued 为 :exec；写入后通过 GetRAGFlowDocument 读回。
	if err := s.store.MarkRAGFlowDocumentManualReparseQueued(ctx, document.ID); err != nil {
		return KnowledgeDocumentResult{}, fmt.Errorf("更新知识库解析状态失败: %w", err)
	}
```

- [ ] **Step 4: Update fakeKnowledgeStore compile fallout**

After sqlc generation, `sqlc.RagflowDocument` has two new fields. Any test helper constructing `sqlc.RagflowDocument` can rely on zero values unless it checks struct equality. If compile errors appear for missing interface methods, add the `MarkRAGFlowDocumentManualReparseQueued` fake method from Step 1.

- [ ] **Step 5: Run service tests**

Run:

```bash
rtk go test ./internal/service -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit manual reset path**

Run:

```bash
rtk git add internal/service/knowledge_service.go internal/service/knowledge_service_test.go
rtk git commit -m "feat(knowledge): 人工重解析清空自动重试状态" -m "手动重解析文件时重置自动重解析次数和冷却时间，避免历史自动重试状态影响人工操作。"
```

---

### Task 5: Full Verification

**Files:**
- No source edits expected.

- [ ] **Step 1: Run focused backend tests**

Run:

```bash
rtk go test ./internal/migrations ./internal/store ./internal/service -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader backend verification**

Run:

```bash
rtk go test ./internal/... ./cmd/... -count=1
```

Expected: PASS.

- [ ] **Step 3: Build all Go packages**

Run:

```bash
rtk go build ./...
```

Expected: PASS.

- [ ] **Step 4: Confirm no OpenAPI drift**

Run:

```bash
rtk make openapi-check
```

Expected: PASS with no generated OpenAPI diff. This feature does not change handlers, request bodies, response schemas, or routes.

- [ ] **Step 5: Inspect final diff**

Run:

```bash
rtk git status --short
rtk git diff --stat HEAD
```

Expected: only files listed in this plan are changed. No `openapi/openapi.yaml`, `web/src/api/generated.ts`, production secret files, or unrelated frontend files should appear.

- [ ] **Step 6: Finish without an empty commit**

If verification only confirms the commits from Tasks 1, 3, and 4, do not create an empty commit. If verification revealed a source fix, apply that fix with a new failing test first and commit it as a small task-specific fix before repeating Task 5.
