# RAGFlow 异常自愈定时任务 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用一个 leader 副本独占运行的定时任务,自动重解析 RAGFlow 失败文件、并恢复卡死(孤儿)在 running 的文件;同时移除前端「重新解析失败文件」手动按钮与旧的「过载自动重试」逻辑。

**Architecture:** 引入「单 leader 跑所有定时任务」的调度模型(基于已有 `internal/redis.DistLocker` + 新增续租);新增 `RagflowAnomalyHealer` 周期任务,重试状态全部放 Redis(计数/冷却/给上),不存 DB、不读 RAGFlow 内部心跳;给 RAGFlow 客户端加 `StopParsing`;删除手动按钮链路、刷新任务的过载重试段,并 drop `auto_reparse_attempts`/`auto_reparse_next_at` 两列。

**Tech Stack:** Go, MySQL(golang-migrate + sqlc), go-redis v9, gin, testify;前端 Vue3 + naive-ui + vitest。

**关键既有事实(实现前必读):**
- 后台周期任务用 `service.PeriodicReconciler`(`internal/service/reconciler.go`),在 `cmd/server/main.go` 用 `eg.Go(func() error { return task.Run(gctx, logger) })` 启动;当前 `app_status_reconcile`/`ragflow_parse_status_refresh`/skill 检测**在所有副本都跑**(无 leader 协调)。
- Redis:`cmd/server/main.go` 已有 `imagecoordRedis := goredis.NewClient(...)`(`*goredis.Client`),`distLocker := redis.NewRedisDistLocker(imagecoordRedis)`;key 前缀 `cfg.Redis.KeyPrefix`(线上为 `ocm:`)。
- `internal/redis.DistLocker` 接口:`TryAcquire(ctx, key, token, ttl) (bool, error)`(SET NX PX)、`Release(ctx, key, token) error`(Lua 校验 token 后 DEL)。**没有续租方法,需新增。**
- `manager` DB 连接固定 UTC 会话;`updated_at` 由 SQL `now()` 写入 = UTC。运行期按 UTC 计算时间阈值(见 memory `project-migration-now-utc-gotcha`)。
- RAGFlow `StopParsing`:`DELETE /api/v1/datasets/{id}/chunks`,body `{"document_ids":[...]}`,只对 `run=RUNNING` 文档生效,把 run 置 2(cancelled)、清 chunk/索引;非 running 文档返回业务错误。
- `parse` = `POST /api/v1/datasets/{id}/chunks`,manager 已有 `Client.ParseDocuments`。
- busy 容忍:`isRAGFlowDocBusyError`(`internal/service/ragflow_parse_status_refresher.go`)匹配 `being processed`,**保留**。
- 迁移最新为 `000008`;本计划新增 `000009`,并在 `sqlc.yaml` schema 列表追加。

**参数默认(放 `RAGFlowConfig` 子结构,带兜底常量):**
- 任务间隔 10 分钟;running 卡死阈值 30 分钟;单文档自愈次数上限 3;退避 attempt#1→0、#2→10min、#3→30min;giveup TTL 7 天;attempts TTL 6 小时;leader 租约 30s、续租间隔 10s。

---

## File Map

| Path | Change |
|---|---|
| `internal/migrations/000009_drop_ragflow_auto_reparse.up.sql` / `.down.sql` | 新建:drop 两列+索引;down 重建。 |
| `internal/migrations/migrations_test.go` | 改:断言 000009 内容。 |
| `sqlc.yaml` | 改:追加 000009.up.sql。 |
| `internal/store/queries/ragflow_knowledge.sql` | 改:删 4 个 auto_reparse 相关 query;改 3 个引用列的 query;新增 2 个全局自愈查询。 |
| `internal/store/sqlc/*` | 重新生成。 |
| `internal/service/ragflow_parse_status_refresher.go` / `_test.go` | 改:删过载重试整段(phase2、store/client 接口方法、helper)。 |
| `internal/service/knowledge_service.go` / `_test.go` | 改:删 `ReparseFailedKnowledgeDataset` 及其 store 接口方法 `ListRAGFlowFailedOrStoppedDocumentsByDataset`(被全局版取代);保留 `MarkRAGFlowDocumentManualReparseQueued`。 |
| `internal/api/handlers/knowledge.go` / `_test.go` | 改:删 org/app 的 reparse-failed handler/路由/接口方法/共享 helper。 |
| `internal/api/handlers/industry_knowledge.go` / `_test.go` | 改:删 industry reparse-failed handler/路由/接口方法。 |
| `openapi/openapi.yaml`、`web/src/api/generated.ts` | 重新生成(端点消失)。 |
| `web/src/api/hooks/useKnowledge.ts` | 改:删 `useReparseFailedRAGFlowDataset`。 |
| `web/src/components/RAGFlowDatasetInfoDialog.vue` / `.spec.ts` | 改:删按钮/确认/状态;保留模型切换确认去输入框。 |
| `internal/redis/dist_locker.go` / `_test.go` | 改:加 `Refresh`(Lua check-own + PEXPIRE)。 |
| `internal/service/leader_elector.go` / `_test.go` | 新建:leader 选举(`IsLeader()`)。 |
| `internal/integrations/ragflow/client.go` / `_test.go` | 改:加 `StopParsing`。 |
| `internal/service/ragflow_heal_state.go` / `_test.go` | 新建:Redis 自愈状态(attempts/cooldown/giveup)。 |
| `internal/service/ragflow_anomaly_healer.go` / `_test.go` | 新建:自愈任务(Part A/B)。 |
| `internal/config/config.go`、`internal/config/loader.go` | 改:加自愈参数子结构+默认。 |
| `cmd/server/main.go` | 改:装配 leader 选举,把所有 PeriodicReconciler gate 到 leader;装配 healer。 |
| `deploy/k8s/local/secret.yaml`、`deploy/k8s/prod/secret.example.yaml` | 改(可选):补自愈参数样例(不填则用默认)。 |

---

## Phase 1 — 清理:删旧自动重试 + 手动按钮 + drop 列

### Task 1: 迁移 000009 + 查询调整 + sqlc

**Files:**
- Create: `internal/migrations/000009_drop_ragflow_auto_reparse.up.sql` / `.down.sql`
- Modify: `internal/migrations/migrations_test.go`、`sqlc.yaml`、`internal/store/queries/ragflow_knowledge.sql`
- Regenerate: `internal/store/sqlc/*`

- [ ] **Step 1: 写迁移测试(失败)**

在 `internal/migrations/migrations_test.go` 追加:
```go
// TestDropRAGFlowAutoReparseMigration 验证 000009 删除自动重试两列与索引,且 down 能重建。
func TestDropRAGFlowAutoReparseMigration(t *testing.T) {
	up, err := FS.ReadFile("000009_drop_ragflow_auto_reparse.up.sql")
	require.NoError(t, err)
	upStr := string(up)
	assert.Contains(t, upStr, "DROP INDEX idx_ragflow_documents_auto_reparse")
	assert.Contains(t, upStr, "DROP COLUMN auto_reparse_next_at")
	assert.Contains(t, upStr, "DROP COLUMN auto_reparse_attempts")

	down, err := FS.ReadFile("000009_drop_ragflow_auto_reparse.down.sql")
	require.NoError(t, err)
	downStr := string(down)
	// down 必须重建列(便于本地回滚),与 000008 的定义保持一致。
	assert.Contains(t, downStr, "auto_reparse_attempts INT NOT NULL DEFAULT 0")
	assert.Contains(t, downStr, "auto_reparse_next_at DATETIME(6) NULL")
	assert.Contains(t, downStr, "idx_ragflow_documents_auto_reparse")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `rtk go test ./internal/migrations -run TestDropRAGFlowAutoReparseMigration -count=1`
Expected: FAIL(文件不存在)。

- [ ] **Step 3: 写迁移文件**

`000009_drop_ragflow_auto_reparse.up.sql`:
```sql
ALTER TABLE ragflow_documents
    DROP INDEX idx_ragflow_documents_auto_reparse,
    DROP COLUMN auto_reparse_next_at,
    DROP COLUMN auto_reparse_attempts;
```
`000009_drop_ragflow_auto_reparse.down.sql`(回滚重建,与 000008 一致):
```sql
ALTER TABLE ragflow_documents
    ADD COLUMN auto_reparse_attempts INT NOT NULL DEFAULT 0 AFTER last_error,
    ADD COLUMN auto_reparse_next_at DATETIME(6) NULL AFTER auto_reparse_attempts,
    ADD KEY idx_ragflow_documents_auto_reparse (
        parse_status, auto_reparse_next_at, auto_reparse_attempts, updated_at
    );
```

- [ ] **Step 4: sqlc.yaml 追加 000009**

在 `sqlc.yaml` schema 列表末尾(000008 之后)加:
```yaml
      - internal/migrations/000009_drop_ragflow_auto_reparse.up.sql
```

- [ ] **Step 5: 改 queries——删 4 个、改 3 个、加 2 个**

在 `internal/store/queries/ragflow_knowledge.sql`:
1. **删除** 这 4 个 query 块:`MarkRAGFlowDocumentFailedWithAutoReparse`、`MarkRAGFlowDocumentAutoReparseQueued`、`MarkRAGFlowDocumentAutoReparseSubmitFailed`、`ListRAGFlowDocumentsDueForAutoReparse`。
2. **改** `MarkRAGFlowDocumentManualReparseQueued`、`ResetRAGFlowDocumentsParseStatusByDataset` 的 SET 子句,去掉 `auto_reparse_attempts = 0, auto_reparse_next_at = NULL,` 两行(其余不变)。
3. **改** `ReplaceRAGFlowIndustryDocument`:去掉它里面的 `auto_reparse_attempts = 0, auto_reparse_next_at = NULL,` 两行。
4. **删** 旧的 `ListRAGFlowFailedOrStoppedDocumentsByDataset`(per-dataset 版,本计划改用全局版)。
5. **新增**两个全局自愈查询(放文件末尾):
```sql
-- name: ListRAGFlowFailedDocumentsForHeal :many
-- 全库列出 failed/stopped 且远端 dataset 仍存在的文档,供自愈任务逐个重解析。带远端 dataset id 以直接调 RAGFlow。
SELECT d.*, ds.ragflow_dataset_id AS remote_dataset_id
FROM ragflow_documents d
JOIN ragflow_datasets ds ON ds.id = d.dataset_id
WHERE d.parse_status IN ('failed', 'stopped')
  AND ds.ragflow_dataset_id IS NOT NULL
ORDER BY d.updated_at ASC
LIMIT ?;

-- name: ListRAGFlowStuckRunningDocumentsForHeal :many
-- 全库列出 running 超过给定时刻仍未推进(updated_at 即「进入 running 时刻」,刷新任务状态不变时不写库)的文档,
-- 供自愈任务 stop_parsing→reparse。只取 running、不取 queued(排队是正常积压,不在此恢复)。
SELECT d.*, ds.ragflow_dataset_id AS remote_dataset_id
FROM ragflow_documents d
JOIN ragflow_datasets ds ON ds.id = d.dataset_id
WHERE d.parse_status = 'running'
  AND d.updated_at < sqlc.arg(stuck_before)
  AND ds.ragflow_dataset_id IS NOT NULL
ORDER BY d.updated_at ASC
LIMIT sqlc.arg(row_limit);
```

- [ ] **Step 6: 生成 sqlc**

Run: `rtk make sqlc-generate`
Expected: PASS。生成 `ListRAGFlowFailedDocumentsForHealRow` / `ListRAGFlowStuckRunningDocumentsForHealRow`(均含 `RemoteDatasetID null.String`);`RagflowDocument` 不再含 `AutoReparse*` 字段;旧 4 个方法消失。

- [ ] **Step 7: 跑迁移/store 测试**

Run: `rtk go test ./internal/migrations ./internal/store -count=1`
Expected: PASS(若 store 测试引用了被删字段/方法,本任务先不改 service,可能编译失败——那些在 Task 4/2 处理;若仅 store 包则应过)。

- [ ] **Step 8: 提交**

```bash
rtk git add internal/migrations/000009_drop_ragflow_auto_reparse.up.sql internal/migrations/000009_drop_ragflow_auto_reparse.down.sql internal/migrations/migrations_test.go sqlc.yaml internal/store/queries/ragflow_knowledge.sql internal/store/sqlc
rtk git commit -m "refactor(knowledge): drop 自动重试列并改用全局自愈查询" -m "删除 auto_reparse_attempts/auto_reparse_next_at 两列与相关 query,新增全库 failed/卡死 running 查询,为统一自愈任务做准备。"
```

> 注:本任务删了 service 仍在用的方法,Task 2/4 会把 service 侧一起删干净;建议 Task 1→4 连续执行,中间编译不过属预期。

### Task 2: 删手动按钮的后端端点

**Files:** `internal/api/handlers/knowledge.go`(+`_test.go`)、`internal/api/handlers/industry_knowledge.go`(+`_test.go`)、`internal/service/knowledge_service.go`(+`_test.go`)、`openapi/openapi.yaml`

- [ ] **Step 1: 删 service 方法**

在 `internal/service/knowledge_service.go` 删除整个 `ReparseFailedKnowledgeDataset` 方法;并从 `KnowledgeStore` 接口删除 `ListRAGFlowFailedOrStoppedDocumentsByDataset`(per-dataset 版已删 query)。保留 `MarkRAGFlowDocumentManualReparseQueued`(单文件人工重解析仍用)。

- [ ] **Step 2: 删 handler/路由/接口方法**

- `knowledge.go`:删 `ReparseFailedOrg`、`ReparseFailedApp`、共享 helper `reparseFailedRAGFlowDataset`、两条路由 `orgGroup.POST(".../reparse-failed", ...)` / `appGroup.POST(...)`,并从 `knowledgeService`、`knowledgeRAGFlowDatasetService` 接口删 `ReparseFailedKnowledgeDataset`。
- `industry_knowledge.go`:删 `ReparseFailedIndustry`、路由 `group.POST("/:industryId/ragflow-dataset/reparse-failed", ...)`,从其 service 接口删 `ReparseFailedKnowledgeDataset`。

- [ ] **Step 3: 删 handler/service 测试中对应用例与 stub 方法**

- `knowledge_test.go`:删 `TestKnowledgeReparseFailedOrgRoutesToService`、`TestKnowledgeReparseFailedAppRoutesToService`、stub 的 `ReparseFailedKnowledgeDataset` 与 `reparseFailed*` 字段。
- `industry_knowledge_test.go`:删 `TestIndustryKnowledgeReparseFailedRoutesToService`、stub 的 `ReparseFailedKnowledgeDataset` 与 `reparseFailed*` 字段。
- `knowledge_service_test.go`:删全部 `TestReparseFailedKnowledgeDataset*` 用例、fake 的 `ListRAGFlowFailedOrStoppedDocumentsByDataset`、`parseMultiDocErr` 字段(若仅这些用例用)。

- [ ] **Step 4: 跑后端测试**

Run: `rtk go test ./internal/api/... ./internal/service -count=1`
Expected: PASS(若刷新任务过载段未删会编译失败——见 Task 4;建议合并验证)。

- [ ] **Step 5: 生成 openapi**

Run: `rtk proxy make openapi-gen`
Expected: PASS;`openapi/openapi.yaml` 里 3 个 `reparse-failed` path 消失。

- [ ] **Step 6: 提交**(与 Task 3 前端一起提交亦可)
```bash
rtk git add internal/api/handlers internal/service/knowledge_service.go internal/service/knowledge_service_test.go openapi/openapi.yaml
rtk git commit -m "refactor(knowledge): 移除手动「重新解析失败文件」接口" -m "失败重解析改由后台自愈任务统一处理,删除 org/app/industry 的 reparse-failed 端点与 service 方法。"
```

### Task 3: 删前端按钮

**Files:** `web/src/api/hooks/useKnowledge.ts`、`web/src/components/RAGFlowDatasetInfoDialog.vue`(+`.spec.ts`)、`web/src/api/generated.ts`

- [ ] **Step 1: 删 hook**

`useKnowledge.ts` 删除整个 `useReparseFailedRAGFlowDataset` 导出。

- [ ] **Step 2: 删弹框按钮/确认/状态(保留模型切换去输入框)**

`RAGFlowDatasetInfoDialog.vue`:删「重新解析失败文件」`<n-button>`、第二个 `<ConfirmActionModal>`(reparse 确认)、`reparseConfirmOpen`/`reparseMutation`/`canReparse`/`openReparseConfirm`/`submitReparse`、import 里的 `useReparseFailedRAGFlowDataset`。**保留**模型切换 `<ConfirmActionModal>` 去掉 `verify-value`/`verify-hint` 的改动。

- [ ] **Step 3: 改弹框单测**

`RAGFlowDatasetInfoDialog.spec.ts`:删 mock 的 `useReparseFailedRAGFlowDataset`、`reparseMutateAsync` 及 reset、两条 reparse 相关用例(`打开确认后触发批量重解析并刷新`、`dataset 未创建时禁用重新解析失败文件按钮`)。其余用例保留。

- [ ] **Step 4: 生成前端类型 + 跑测试 + 构建**

Run:
```bash
rtk proxy make web-types-gen
cd web && npx vitest run src/components/RAGFlowDatasetInfoDialog.spec.ts && npx vue-tsc --noEmit && npm run build
```
Expected: 测试通过、typecheck 0 错、build 成功。

- [ ] **Step 5: 提交**
```bash
rtk git add web/src/api/hooks/useKnowledge.ts web/src/components/RAGFlowDatasetInfoDialog.vue web/src/components/RAGFlowDatasetInfoDialog.spec.ts web/src/api/generated.ts
rtk git commit -m "refactor(knowledge): 移除 RAGFlow 信息弹框的「重新解析失败文件」入口" -m "失败重解析改由后台自愈任务自动完成,前端不再保留手动入口;模型切换确认去输入框的简化保留。"
```

### Task 4: 删刷新任务的过载自动重试段

**Files:** `internal/service/ragflow_parse_status_refresher.go`(+`_test.go`)

- [ ] **Step 1: 删实现**

`ragflow_parse_status_refresher.go`:
- `Tick` 改回只调 `refreshQueuedAndRunningDocuments`(删 `autoReparseDueFailedDocuments` 调用)。
- 删方法 `autoReparseDueFailedDocuments`、函数 `isRAGFlowAutoReparseError`、`autoReparseNextAt`、常量 `ragflowAutoReparseMaxAttempts`。
- 从 `RagflowParseStatusRefresherStore` 接口删 `ListRAGFlowDocumentsDueForAutoReparse`、`MarkRAGFlowDocumentFailedWithAutoReparse`、`MarkRAGFlowDocumentAutoReparseQueued`、`MarkRAGFlowDocumentAutoReparseSubmitFailed`。
- 从 `RagflowParseStatusRefreshClient` 接口删 `ParseDocuments`(刷新任务不再触发解析;自愈任务自带客户端)。
- `applyRemoteStatus` 里失败分支恢复为普通 `UpdateRAGFlowDocumentParseStatus`(不再判 overload/写 next_at)。
- 删 `now func()`/`SetNowFunc`(若仅过载段用)。

- [ ] **Step 2: 删测试**

`ragflow_parse_status_refresher_test.go`:删 `TestRagflowParseStatusRefresher_AutoReparses*`、`TestRagflowParseStatusRefresher_RequeuesDueAutoReparse`、`TestRagflowParseStatusRefresher_NonOverloadFailure*`、`TestAutoReparseNextAtBackoff`、`TestRagflowParseStatusRefresher_AutoReparseParseError*` 等过载相关用例;fake store/client 删对应方法与字段。保留普通状态同步用例。

- [ ] **Step 3: 跑全后端测试 + 构建**

Run: `rtk go test ./internal/... ./cmd/... -count=1 && rtk go build ./...`
Expected: PASS / Success(此时手动按钮、过载段、列都已移除,整体编译通过)。

- [ ] **Step 4: 提交**
```bash
rtk git add internal/service/ragflow_parse_status_refresher.go internal/service/ragflow_parse_status_refresher_test.go
rtk git commit -m "refactor(knowledge): 刷新任务移除过载自动重试段" -m "自动重解析统一收口到新的异常自愈任务,刷新任务恢复为纯状态同步。"
```

---

## Phase 2 — 单 leader 跑所有定时任务

### Task 5: DistLocker 续租

**Files:** `internal/redis/dist_locker.go`(+`_test.go`)

- [ ] **Step 1: 写测试(失败)**

`dist_locker_test.go` 追加(用现有测试里的 miniredis/redis fixture,沿用同文件已有构造方式):
```go
// TestRedisDistLocker_Refresh 验证只有持有者(token 匹配)能续租,匹配则延长 TTL、不匹配返回 false。
func TestRedisDistLocker_Refresh(t *testing.T) {
	locker, _ := newTestLocker(t) // 与本文件已有用例相同的构造 helper
	ctx := context.Background()
	ok, err := locker.TryAcquire(ctx, "k", "tok-a", time.Second)
	require.NoError(t, err)
	require.True(t, ok)

	// 持有者续租成功
	ok, err = locker.Refresh(ctx, "k", "tok-a", 2*time.Second)
	require.NoError(t, err)
	assert.True(t, ok)

	// 非持有者续租失败,不影响持有者
	ok, err = locker.Refresh(ctx, "k", "tok-b", 2*time.Second)
	require.NoError(t, err)
	assert.False(t, ok)
}
```
（若本文件用真实 Redis 集成 tag,请放到对应 `_integration_test.go` 并沿用其构造方式。）

- [ ] **Step 2: 跑测试确认失败**

Run: `rtk go test ./internal/redis -run TestRedisDistLocker_Refresh -count=1`
Expected: FAIL（`Refresh` 未定义）。

- [ ] **Step 3: 实现 Refresh**

`dist_locker.go`:在接口加 `Refresh(ctx context.Context, key, token string, ttl time.Duration) (bool, error)`;实现用 Lua:
```go
// luaRefresh: KEYS[1]=lockKey, ARGV[1]=token, ARGV[2]=ttlMillis。
// 仅当持有者 token 匹配时 PEXPIRE 续租,返回 1;否则返回 0(已被别人持有或已过期)。
const luaRefresh = `
if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("pexpire", KEYS[1], ARGV[2])
else
  return 0
end`

// Refresh 见接口注释。
func (l *RedisDistLocker) Refresh(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	res, err := l.client.Eval(ctx, luaRefresh, []string{key}, token, ttl.Milliseconds()).Int64()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}
```

- [ ] **Step 4: 跑测试**

Run: `rtk go test ./internal/redis -run TestRedisDistLocker_Refresh -count=1`
Expected: PASS。

- [ ] **Step 5: 提交**
```bash
rtk git add internal/redis/dist_locker.go internal/redis/dist_locker_test.go
rtk git commit -m "feat(redis): DistLocker 增加续租 Refresh" -m "为 leader 选举提供持有者续租能力:token 匹配才延长 TTL。"
```

### Task 6: LeaderElector

**Files:** Create `internal/service/leader_elector.go`(+`_test.go`)

- [ ] **Step 1: 写测试(失败)**

`leader_elector.go`:先定义最小依赖接口便于 mock:
```go
type leaderLocker interface {
	TryAcquire(ctx context.Context, key, token string, ttl time.Duration) (bool, error)
	Refresh(ctx context.Context, key, token string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, key, token string) error
}
```
`leader_elector_test.go`:
```go
// fakeLeaderLocker:可控的抢锁/续租结果,用于验证选举状态机。
type fakeLeaderLocker struct {
	mu        sync.Mutex
	holder    string // 当前持有者 token,空表示空闲
}
func (f *fakeLeaderLocker) TryAcquire(_ context.Context, _ , token string, _ time.Duration) (bool, error) {
	f.mu.Lock(); defer f.mu.Unlock()
	if f.holder == "" { f.holder = token; return true, nil }
	return false, nil
}
func (f *fakeLeaderLocker) Refresh(_ context.Context, _ , token string, _ time.Duration) (bool, error) {
	f.mu.Lock(); defer f.mu.Unlock()
	return f.holder == token, nil
}
func (f *fakeLeaderLocker) Release(_ context.Context, _ , token string) error {
	f.mu.Lock(); defer f.mu.Unlock()
	if f.holder == token { f.holder = "" }
	return nil
}

// TestLeaderElector_AcquiresAndHolds 验证空闲时能当选,且续租保持当选。
func TestLeaderElector_AcquiresAndHolds(t *testing.T) {
	lk := &fakeLeaderLocker{}
	e := NewLeaderElector(lk, "ocm:scheduler:leader", "tok-1", 60*time.Millisecond, 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.Run(ctx, slog.Default())
	require.Eventually(t, e.IsLeader, time.Second, 10*time.Millisecond)
	// 续租期内保持当选
	time.Sleep(80 * time.Millisecond)
	assert.True(t, e.IsLeader())
}

// TestLeaderElector_SecondInstanceNotLeader 验证已被占用时第二实例不当选。
func TestLeaderElector_SecondInstanceNotLeader(t *testing.T) {
	lk := &fakeLeaderLocker{}
	ctx, cancel := context.WithCancel(context.Background()); defer cancel()
	e1 := NewLeaderElector(lk, "k", "tok-1", 60*time.Millisecond, 20*time.Millisecond)
	go e1.Run(ctx, slog.Default())
	require.Eventually(t, e1.IsLeader, time.Second, 10*time.Millisecond)
	e2 := NewLeaderElector(lk, "k", "tok-2", 60*time.Millisecond, 20*time.Millisecond)
	go e2.Run(ctx, slog.Default())
	time.Sleep(60 * time.Millisecond)
	assert.False(t, e2.IsLeader())
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `rtk go test ./internal/service -run TestLeaderElector -count=1`
Expected: FAIL(未定义)。

- [ ] **Step 3: 实现 LeaderElector**

```go
// LeaderElector 基于 Redis 锁选出单一 leader 副本;非 leader 不运行定时任务。
// 通过 token 续租维持当选,leader 崩溃后租约到期(≤lease)其它副本接管。
type LeaderElector struct {
	locker   leaderLocker
	key      string
	token    string
	lease    time.Duration // 锁 TTL
	interval time.Duration // 续租/重试间隔(应 < lease,如 lease/3)
	isLeader atomic.Bool
}

func NewLeaderElector(locker leaderLocker, key, token string, lease, interval time.Duration) *LeaderElector {
	return &LeaderElector{locker: locker, key: key, token: token, lease: lease, interval: interval}
}

func (e *LeaderElector) IsLeader() bool { return e.isLeader.Load() }

// Run 阻塞运行选举循环,直到 ctx 取消;退出时若为 leader 主动释放,加速接管。
func (e *LeaderElector) Run(ctx context.Context, logger *slog.Logger) error {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	defer func() {
		if e.isLeader.Load() { _ = e.locker.Release(context.Background(), e.key, e.token) }
		e.isLeader.Store(false)
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if e.isLeader.Load() {
				ok, err := e.locker.Refresh(ctx, e.key, e.token, e.lease)
				if err != nil || !ok { e.isLeader.Store(false) } // 续租失败=丢失领导权
			} else {
				ok, err := e.locker.TryAcquire(ctx, e.key, e.token, e.lease)
				if err == nil && ok { e.isLeader.Store(true) }
			}
		}
	}
}
```

- [ ] **Step 4: 跑测试**

Run: `rtk go test ./internal/service -run TestLeaderElector -count=1`
Expected: PASS。

- [ ] **Step 5: 提交**
```bash
rtk git add internal/service/leader_elector.go internal/service/leader_elector_test.go
rtk git commit -m "feat(service): 增加 Redis leader 选举" -m "为「单副本运行所有定时任务」提供 leader 选举:token 续租维持当选,失租自动让位。"
```

### Task 7: 所有定时任务 gate 到 leader

**Files:** `cmd/server/main.go`

- [ ] **Step 1: 给 PeriodicReconciler 的 fn 包一层 leader gate**

在 `main.go` 装配处,先构造 elector(用已有 `distLocker` 与 `cfg.Redis.KeyPrefix`):
```go
// 所有定时任务只在 leader 副本运行,避免多副本重复轮询/重复自愈。
leaderElector := service.NewLeaderElector(distLocker, cfg.Redis.KeyPrefix+"scheduler:leader", uuid.NewString(), 30*time.Second, 10*time.Second)
eg.Go(func() error { return leaderElector.Run(gctx, logger) })

// onlyLeader 包装 reconciler 的 fn:非 leader 直接跳过本轮。
onlyLeader := func(fn func(ctx context.Context) error) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if !leaderElector.IsLeader() { return nil }
		return fn(ctx)
	}
}
```
把现有 `NewPeriodicReconciler(name, interval, X.Tick)` 改为 `NewPeriodicReconciler(name, interval, onlyLeader(X.Tick))`,覆盖:`app_status_reconcile`、`ragflow_parse_status_refresh`、skill 检测任务、以及 Task 12 的 healer。

> 注:`reaper` 已有自己的 `ocm:reaper:lock` 互斥,可保持不变(它本就单跑),不必强行并入 leader——但如希望统一,可后续将其 Start 也 gate 到 leader。本计划不动 reaper。

- [ ] **Step 2: 构建 + 冒烟**

Run: `rtk go build ./... && rtk go vet ./cmd/...`
Expected: Success。

- [ ] **Step 3: 提交**
```bash
rtk git add cmd/server/main.go
rtk git commit -m "feat(server): 定时任务统一由 leader 副本运行" -m "用 leader 选举把周期任务 gate 起来,非 leader 副本不再重复执行,避免重复轮询与并发自愈。"
```

---

## Phase 3 — RAGFlow StopParsing + Redis 自愈状态

### Task 8: RAGFlow 客户端 StopParsing

**Files:** `internal/integrations/ragflow/client.go`(+`_test.go`)

- [ ] **Step 1: 写测试(失败)**

`client_test.go`(沿用本文件已有的 httptest server 范式):
```go
// TestClient_StopParsing 验证 DELETE /datasets/{id}/chunks 携带 document_ids,且 200+code0 视为成功。
func TestClient_StopParsing(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method; gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body); gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, "k", 0); require.NoError(t, err)
	err = c.StopParsing(context.Background(), "ds-1", []string{"doc-a", "doc-b"})
	require.NoError(t, err)
	assert.Equal(t, http.MethodDelete, gotMethod)
	assert.Equal(t, "/api/v1/datasets/ds-1/chunks", gotPath)
	assert.Contains(t, gotBody, "doc-a"); assert.Contains(t, gotBody, "doc-b")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `rtk go test ./internal/integrations/ragflow -run TestClient_StopParsing -count=1`
Expected: FAIL(未定义)。

- [ ] **Step 3: 实现(紧邻 ParseDocuments)**
```go
// StopParsing 取消指定 dataset 下 running 文档的解析(DELETE /chunks):RAGFlow 会 cancel 任务、
// 把 run 置为 stopped、清空已生成 chunk/索引。仅对 run=RUNNING 文档有效,非 running 会返回业务错误。
func (c *Client) StopParsing(ctx context.Context, datasetID string, documentIDs []string) error {
	body := map[string][]string{"document_ids": documentIDs}
	return c.doJSON(ctx, http.MethodDelete, c.apiPath("/api/v1/datasets", datasetID, "chunks"), nil, body, nil)
}
```

- [ ] **Step 4: 跑测试**

Run: `rtk go test ./internal/integrations/ragflow -run TestClient_StopParsing -count=1`
Expected: PASS。

- [ ] **Step 5: 提交**
```bash
rtk git add internal/integrations/ragflow/client.go internal/integrations/ragflow/client_test.go
rtk git commit -m "feat(ragflow): 客户端增加 StopParsing" -m "封装 DELETE /datasets/{id}/chunks,用于自愈任务把卡死的 running 文档取消重置后再重解析。"
```

### Task 9: Redis 自愈状态 helper

**Files:** Create `internal/service/ragflow_heal_state.go`(+`_test.go`)

设计:用 go-redis `redis.Cmdable`(沿用 `pow.NewRedisReplayGuard` 的注入范式)。key 前缀 = `cfg.Redis.KeyPrefix`(`ocm:`)。

- [ ] **Step 1: 写测试(失败)**——用 miniredis(仓库测试已有用法,见 `internal/redis/*_test.go`)
```go
// TestHealState_AttemptsCooldownGiveup 覆盖:计数自增、冷却存在性、到上限置 giveup、giveup 拦截。
func TestHealState_AttemptsCooldownGiveup(t *testing.T) {
	rc := newMiniredisCmdable(t) // 与仓库其它 miniredis 测试一致的构造
	s := NewHealState(rc, "ocm:", HealStateTTL{Attempts: time.Hour, Giveup: 24 * time.Hour})
	ctx := context.Background()
	doc := "d1"

	gu, _ := s.GivenUp(ctx, doc); assert.False(t, gu)
	cd, _ := s.InCooldown(ctx, doc); assert.False(t, cd)

	n, _ := s.RecordAttempt(ctx, doc, 10*time.Minute); assert.Equal(t, 1, n) // 第1次,设10min冷却
	cd, _ = s.InCooldown(ctx, doc); assert.True(t, cd)

	_, _ = s.RecordAttempt(ctx, doc, time.Minute)
	n, _ = s.RecordAttempt(ctx, doc, time.Minute); assert.Equal(t, 3, n)
	require.NoError(t, s.MarkGivenUp(ctx, doc))
	gu, _ = s.GivenUp(ctx, doc); assert.True(t, gu)
}
```

- [ ] **Step 2: 跑测试确认失败** → FAIL。

- [ ] **Step 3: 实现**
```go
// HealStateTTL 控制各类自愈状态键的过期。
type HealStateTTL struct {
	Attempts time.Duration // 计数键存活(覆盖一轮 3 次尝试,默认 6h)
	Giveup   time.Duration // 给上标记存活(默认 7d)
}

// HealState 把自愈的重试簿记放 Redis(瞬时、自动过期),不落 DB。
type HealState struct {
	rc     redis.Cmdable
	prefix string
	ttl    HealStateTTL
}

func NewHealState(rc redis.Cmdable, keyPrefix string, ttl HealStateTTL) *HealState {
	return &HealState{rc: rc, prefix: keyPrefix, ttl: ttl}
}

func (s *HealState) kAttempts(doc string) string { return s.prefix + "heal:attempts:" + doc }
func (s *HealState) kCooldown(doc string) string { return s.prefix + "heal:cooldown:" + doc }
func (s *HealState) kGiveup(doc string) string   { return s.prefix + "heal:giveup:" + doc }

// RecordAttempt 自增次数并设冷却;返回累计次数。
func (s *HealState) RecordAttempt(ctx context.Context, doc string, backoff time.Duration) (int, error) {
	n, err := s.rc.Incr(ctx, s.kAttempts(doc)).Result()
	if err != nil { return 0, err }
	_ = s.rc.Expire(ctx, s.kAttempts(doc), s.ttl.Attempts).Err()
	if backoff > 0 { _ = s.rc.Set(ctx, s.kCooldown(doc), "1", backoff).Err() }
	return int(n), nil
}

// InCooldown 冷却中(本轮跳过)。
func (s *HealState) InCooldown(ctx context.Context, doc string) (bool, error) {
	n, err := s.rc.Exists(ctx, s.kCooldown(doc)).Result()
	return n > 0, err
}

// GivenUp 已放弃(达上限,停止自愈)。
func (s *HealState) GivenUp(ctx context.Context, doc string) (bool, error) {
	n, err := s.rc.Exists(ctx, s.kGiveup(doc)).Result()
	return n > 0, err
}

// MarkGivenUp 置给上标记(TTL=Giveup)。
func (s *HealState) MarkGivenUp(ctx context.Context, doc string) error {
	return s.rc.Set(ctx, s.kGiveup(doc), "1", s.ttl.Giveup).Err()
}
```

- [ ] **Step 4: 跑测试** → PASS。

- [ ] **Step 5: 提交**
```bash
rtk git add internal/service/ragflow_heal_state.go internal/service/ragflow_heal_state_test.go
rtk git commit -m "feat(knowledge): 自愈重试状态 Redis helper" -m "把重试次数/冷却/给上放 Redis(自动过期),为异常自愈任务提供封顶与退避簿记,不落库。"
```

---

## Phase 4 — 自愈任务

### Task 10: 自愈任务 Part A/B

**Files:** Create `internal/service/ragflow_anomaly_healer.go`(+`_test.go`)

依赖接口(便于 mock,沿用项目其它任务的接口式注入):
```go
type healStore interface {
	ListRAGFlowFailedDocumentsForHeal(ctx context.Context, limit int32) ([]sqlc.ListRAGFlowFailedDocumentsForHealRow, error)
	ListRAGFlowStuckRunningDocumentsForHeal(ctx context.Context, arg sqlc.ListRAGFlowStuckRunningDocumentsForHealParams) ([]sqlc.ListRAGFlowStuckRunningDocumentsForHealRow, error)
	MarkRAGFlowDocumentManualReparseQueued(ctx context.Context, id string) error
}
type healRAGFlow interface {
	ParseDocuments(ctx context.Context, datasetID string, documentIDs []string) error
	StopParsing(ctx context.Context, datasetID string, documentIDs []string) error
}
type healRetryState interface {
	GivenUp(ctx context.Context, doc string) (bool, error)
	InCooldown(ctx context.Context, doc string) (bool, error)
	RecordAttempt(ctx context.Context, doc string, backoff time.Duration) (int, error)
	MarkGivenUp(ctx context.Context, doc string) error
}
```

- [ ] **Step 1: 写测试(失败)**

`ragflow_anomaly_healer_test.go`(用 fake 三件依赖):
```go
// TestHealer_PartA_ReparsesFailedRespectingCapAndGiveup
// 失败文档逐个重解析:跳过 giveup/cooldown;到第3次后置 giveup。
func TestHealer_PartA_ReparsesFailedRespectingCapAndGiveup(t *testing.T) {
	store := &fakeHealStore{failed: []sqlc.ListRAGFlowFailedDocumentsForHealRow{
		healFailedRow("doc-a", "ds-remote-1", "rdoc-a"),
	}}
	rf := &fakeHealRAGFlow{}
	state := &fakeHealRetry{attempts: map[string]int{}}
	h := NewRagflowAnomalyHealer(store, rf, state, healerCfgForTest())
	require.NoError(t, h.Tick(context.Background()))
	// 提交了重解析 + 入队 + 计了一次
	require.Len(t, rf.parseCalls, 1)
	assert.Equal(t, []string{"doc-a"}, store.queued)
	assert.Equal(t, 1, state.attempts["doc-a"])
}

// TestHealer_PartA_SkipsGivenUpAndCooldown
func TestHealer_PartA_SkipsGivenUpAndCooldown(t *testing.T) {
	store := &fakeHealStore{failed: []sqlc.ListRAGFlowFailedDocumentsForHealRow{
		healFailedRow("g", "ds", "rg"), healFailedRow("c", "ds", "rc"),
	}}
	rf := &fakeHealRAGFlow{}
	state := &fakeHealRetry{attempts: map[string]int{}, given: map[string]bool{"g": true}, cool: map[string]bool{"c": true}}
	h := NewRagflowAnomalyHealer(store, rf, state, healerCfgForTest())
	require.NoError(t, h.Tick(context.Background()))
	assert.Empty(t, rf.parseCalls)
	assert.Empty(t, store.queued)
}

// TestHealer_PartA_CapReachedSetsGiveup
func TestHealer_PartA_CapReachedSetsGiveup(t *testing.T) {
	store := &fakeHealStore{failed: []sqlc.ListRAGFlowFailedDocumentsForHealRow{healFailedRow("d", "ds", "rd")}}
	rf := &fakeHealRAGFlow{}
	state := &fakeHealRetry{attempts: map[string]int{"d": 2}} // 本次将达到3
	h := NewRagflowAnomalyHealer(store, rf, state, healerCfgForTest())
	require.NoError(t, h.Tick(context.Background()))
	assert.True(t, state.given["d"])
}

// TestHealer_PartB_StopThenReparseStuckRunning
// 卡死 running:先 StopParsing 再 ParseDocuments;busy 错误容忍照常入队;非 busy 硬错误跳过。
func TestHealer_PartB_StopThenReparseStuckRunning(t *testing.T) {
	store := &fakeHealStore{stuck: []sqlc.ListRAGFlowStuckRunningDocumentsForHealRow{healStuckRow("doc-x", "ds", "rx")}}
	rf := &fakeHealRAGFlow{}
	state := &fakeHealRetry{attempts: map[string]int{}}
	h := NewRagflowAnomalyHealer(store, rf, state, healerCfgForTest())
	require.NoError(t, h.Tick(context.Background()))
	assert.Equal(t, []string{"rx"}, rf.stopCalls)   // 先 stop
	assert.Equal(t, []string{"rx"}, flatten(rf.parseCalls)) // 再 reparse
	assert.Equal(t, []string{"doc-x"}, store.queued)
}
```
（helper `healFailedRow`/`healStuckRow`/`healerCfgForTest`/fakes 在测试文件内实现;`healerCfgForTest` 返回 cap=3、退避 {0,10m,30m}、stuckThreshold=30m、batchLimit=100。)

- [ ] **Step 2: 跑测试确认失败** → FAIL(未定义)。

- [ ] **Step 3: 实现**
```go
// HealerConfig 自愈参数。
type HealerConfig struct {
	MaxAttempts    int             // 单文档自愈次数上限(默认 3)
	Backoffs       []time.Duration // 第 n 次尝试后的冷却;超出用最后一个
	StuckThreshold time.Duration   // running 超过此时长判卡死(默认 30m)
	BatchLimit     int32           // 每轮每类处理上限
	Now            func() time.Time
}

type RagflowAnomalyHealer struct {
	store healStore
	rf    healRAGFlow
	state healRetryState
	cfg   HealerConfig
	log   *slog.Logger
}

func NewRagflowAnomalyHealer(store healStore, rf healRAGFlow, state healRetryState, cfg HealerConfig) *RagflowAnomalyHealer {
	if cfg.Now == nil { cfg.Now = time.Now }
	if cfg.MaxAttempts <= 0 { cfg.MaxAttempts = 3 }
	return &RagflowAnomalyHealer{store: store, rf: rf, state: state, cfg: cfg, log: slog.Default()}
}
func (h *RagflowAnomalyHealer) SetLogger(l *slog.Logger) { if l != nil { h.log = l } }

// Tick 跑一轮:先恢复卡死 running,再重解析失败文档。Tick 由 leader 调度,已是单实例。
func (h *RagflowAnomalyHealer) Tick(ctx context.Context) error {
	firstErr := h.healStuckRunning(ctx)
	if err := h.healFailed(ctx); err != nil && firstErr == nil { firstErr = err }
	return firstErr
}

// backoffFor 第 attempts 次(1-based)后的冷却。
func (h *RagflowAnomalyHealer) backoffFor(attempts int) time.Duration {
	if len(h.cfg.Backoffs) == 0 { return 0 }
	if attempts-1 < len(h.cfg.Backoffs) { return h.cfg.Backoffs[attempts-1] }
	return h.cfg.Backoffs[len(h.cfg.Backoffs)-1]
}

// eligible 跳过已给上/冷却中的文档。
func (h *RagflowAnomalyHealer) eligible(ctx context.Context, doc string) bool {
	if gu, _ := h.state.GivenUp(ctx, doc); gu { return false }
	if cd, _ := h.state.InCooldown(ctx, doc); cd { return false }
	return true
}

// afterAttempt 记一次尝试,达上限则给上。
func (h *RagflowAnomalyHealer) afterAttempt(ctx context.Context, doc string) (reachedCap bool) {
	n, err := h.state.RecordAttempt(ctx, doc, 0) // backoff 在调用方按结果设;这里先取次数
	_ = err
	if n >= h.cfg.MaxAttempts { _ = h.state.MarkGivenUp(ctx, doc); return true }
	return false
}

func (h *RagflowAnomalyHealer) healFailed(ctx context.Context) error {
	rows, err := h.store.ListRAGFlowFailedDocumentsForHeal(ctx, h.cfg.BatchLimit)
	if err != nil { return fmt.Errorf("扫描待自愈失败文档: %w", err) }
	var firstErr error
	for _, row := range rows {
		doc := row.ID.String() // 注:sqlc 列为 uuid 类型时用 .String();若为 string 直接用
		remoteDS := row.RemoteDatasetID.String
		if !h.eligible(ctx, doc) { continue }
		// 记一次尝试 + 退避;读回累计次数决定是否给上
		n, _ := h.state.RecordAttempt(ctx, doc, 0)
		if err := h.rf.ParseDocuments(ctx, remoteDS, []string{row.RagflowDocumentID}); err != nil && !isRAGFlowDocBusyError(err) {
			if firstErr == nil { firstErr = err }
			h.maybeGiveup(ctx, doc, n)
			continue
		}
		if err := h.store.MarkRAGFlowDocumentManualReparseQueued(ctx, doc); err != nil && firstErr == nil {
			firstErr = err
		}
		_ = h.state.RecordAttempt // 退避用 SetCooldown:见下 maybeGiveup 同时设冷却
		h.setBackoffAndMaybeGiveup(ctx, doc, n)
	}
	return firstErr
}

func (h *RagflowAnomalyHealer) healStuckRunning(ctx context.Context) error {
	rows, err := h.store.ListRAGFlowStuckRunningDocumentsForHeal(ctx, sqlc.ListRAGFlowStuckRunningDocumentsForHealParams{
		StuckBefore: h.cfg.Now().UTC().Add(-h.cfg.StuckThreshold),
		RowLimit:    h.cfg.BatchLimit,
	})
	if err != nil { return fmt.Errorf("扫描卡死 running 文档: %w", err) }
	var firstErr error
	for _, row := range rows {
		doc := row.ID.String()
		remoteDS := row.RemoteDatasetID.String
		if !h.eligible(ctx, doc) { continue }
		n, _ := h.state.RecordAttempt(ctx, doc, 0)
		// 先取消(把 run=1 重置),再重解析
		_ = h.rf.StopParsing(ctx, remoteDS, []string{row.RagflowDocumentID})
		if err := h.rf.ParseDocuments(ctx, remoteDS, []string{row.RagflowDocumentID}); err != nil && !isRAGFlowDocBusyError(err) {
			if firstErr == nil { firstErr = err }
			h.setBackoffAndMaybeGiveupStuck(ctx, doc, n)
			continue
		}
		if err := h.store.MarkRAGFlowDocumentManualReparseQueued(ctx, doc); err != nil && firstErr == nil {
			firstErr = err
		}
		h.setBackoffAndMaybeGiveupStuck(ctx, doc, n)
	}
	return firstErr
}
```
> **实现备注(给执行者):** 上面把「记次数」「设退避冷却」「到上限给上」做成一个内聚 helper 更干净。重构为单一方法:
> ```go
> // applyAttempt: 设退避冷却;若累计达上限则置 giveup,卡死类同时打 error 日志。
> func (h *RagflowAnomalyHealer) applyAttempt(ctx context.Context, doc string, attempts int, stuck bool) {
>     _ = h.state.RecordAttempt(ctx, doc, h.backoffFor(attempts)) // 单次自增已在调用前完成则改为只设冷却
>     if attempts >= h.cfg.MaxAttempts {
>         _ = h.state.MarkGivenUp(ctx, doc)
>         if stuck { h.log.Error("[heal] 文档卡死自愈达上限仍未恢复,RAGFlow 可能需重启", "doc", doc) }
>     }
> }
> ```
> 注意 `RecordAttempt` 既自增又设冷却——**每个文档每轮只调一次** `RecordAttempt(ctx, doc, h.backoffFor(n))`,用其返回的 n 判断是否给上;不要重复自增。请据此把上面草稿收敛为「每文档:eligible→`n=RecordAttempt(backoff)`→Stop(仅B)→Parse→入队→`if n>=cap: MarkGivenUp(+B日志)`」单一直线流程。

- [ ] **Step 4: 跑测试**

Run: `rtk go test ./internal/service -run TestHealer -count=1`
Expected: PASS。

- [ ] **Step 5: 提交**
```bash
rtk git add internal/service/ragflow_anomaly_healer.go internal/service/ragflow_anomaly_healer_test.go
rtk git commit -m "feat(knowledge): 新增 RAGFlow 异常自愈任务" -m "Part A 重解析全库 failed/stopped;Part B 对卡死(running 超阈值)stop_parsing→reparse;均封顶3次、退避、给上,卡死达上限打 error 日志。"
```

### Task 11: 装配 healer + 配置 + 验证

**Files:** `internal/config/config.go`、`internal/config/loader.go`、`cmd/server/main.go`、secret 样例

- [ ] **Step 1: 配置加自愈参数(带默认)**

`config.go` 在 `RAGFlowConfig` 加:
```go
	// SelfHeal 是 RAGFlow 解析异常自愈任务参数;留空用内置默认。
	SelfHeal RAGFlowSelfHealConfig `yaml:"self_heal"`
```
并定义:
```go
type RAGFlowSelfHealConfig struct {
	Interval       Duration `yaml:"interval"`        // 任务间隔,默认 10m
	StuckThreshold Duration `yaml:"stuck_threshold"` // running 卡死阈值,默认 30m
	MaxAttempts    int      `yaml:"max_attempts"`    // 默认 3
	BatchLimit     int      `yaml:"batch_limit"`     // 每轮每类上限,默认 100
}
```
`loader.go` 里给零值填默认(参考已有默认填充处)。

- [ ] **Step 2: main.go 装配**

```go
// RAGFlow 解析异常自愈:与刷新任务并列,均由 leader 副本运行。
var ragflowHealTask *service.PeriodicReconciler
if ragflowClient != nil {
	healState := service.NewHealState(imagecoordRedis, cfg.Redis.KeyPrefix, service.HealStateTTL{Attempts: 6 * time.Hour, Giveup: 7 * 24 * time.Hour})
	healer := service.NewRagflowAnomalyHealer(dbStore.Queries, ragflowClient, healState, service.HealerConfig{
		MaxAttempts:    valOrDefault(cfg.RAGFlow.SelfHeal.MaxAttempts, 3),
		Backoffs:       []time.Duration{0, 10 * time.Minute, 30 * time.Minute},
		StuckThreshold: durOrDefault(cfg.RAGFlow.SelfHeal.StuckThreshold, 30*time.Minute),
		BatchLimit:     int32(valOrDefault(cfg.RAGFlow.SelfHeal.BatchLimit, 100)),
	})
	healer.SetLogger(logger)
	ragflowHealTask = service.NewPeriodicReconciler("ragflow_self_heal", durOrDefault(cfg.RAGFlow.SelfHeal.Interval, 10*time.Minute), onlyLeader(healer.Tick))
}
...
if ragflowHealTask != nil { eg.Go(func() error { return ragflowHealTask.Run(gctx, logger) }) }
```
（`dbStore.Queries` 需实现 `healStore` 的 3 个方法——`MarkRAGFlowDocumentManualReparseQueued` 已有,2 个新查询由 Task 1 的 sqlc 生成;`valOrDefault`/`durOrDefault` 为本地小 helper 或内联。）

- [ ] **Step 3: 全量后端验证**

Run:
```bash
rtk go test ./internal/... ./cmd/... -count=1
rtk go build ./...
rtk proxy make openapi-check
```
Expected: 测试全过;build 成功;openapi-check 干净(本阶段无 handler 变更,但 Phase1 改了——确保已 gen 并提交)。

- [ ] **Step 4: 提交**
```bash
rtk git add internal/config cmd/server/main.go deploy/k8s/local/secret.yaml deploy/k8s/prod/secret.example.yaml
rtk git commit -m "feat(knowledge): 装配 RAGFlow 自愈定时任务" -m "在 leader 调度下每 10 分钟运行自愈任务;参数可配,带内置默认。"
```

### Task 12: 真实环境验证(浏览器/数据)

**Files:** 无源码改动。

- [ ] **Step 1: 本地构造 + 部署**

`make local-build`;用本地 k3d MySQL 把某行业库一文档置 `failed`、另一文档置 `running` 且 `updated_at` 调早 40 分钟(模拟卡死)。

- [ ] **Step 2: 观察自愈**

等一轮(或临时把 interval 调小)后查 DB:
- failed 文档应转 `queued`/`running` 并最终 completed;Redis 出现 `ocm:heal:attempts:<doc>`。
- 卡死 running 文档应被 `stop_parsing`+reparse(RAGFlow document 表 run 由 1→2→重新解析);本地 ragflow 执行器健康时应能 completed。
- 模拟「坏文档」(embedding 必失败)验证 3 次后 `ocm:heal:giveup:<doc>` 出现且不再重试。
- 杀掉一个 manager 副本验证 leader 接管(另一副本开始运行任务)。

- [ ] **Step 3: 浏览器核对**

平台管理员在知识库页确认状态自动收敛(无需任何手动按钮)。AGENTS.md 要求真实浏览器验证新功能。

- [ ] **Step 4: 收尾**

确认 `rtk git status` 干净;若验证中发现 bug,先加复现测试再修。

---

## Self-Review

**Spec coverage:** 失败自愈(Task10 PartA)✓;卡死自愈(Task10 PartB)✓;封顶3+退避+给上7d(Task9/10)✓;Redis 存状态、drop 列(Task1/9)✓;单 leader 跑所有定时任务(Task5-7)✓;StopParsing(Task8)✓;删手动按钮链路(Task2/3)✓;删过载段(Task4)✓;只卡 running 不碰 queued(Task1 查询)✓;卡死达上限打 error(Task10)✓;模型切换/重传不需专门清(7d TTL,直接重解析绕过)——无需任务,设计已说明 ✓。

**Placeholder scan:** Task10 草稿里有「实现备注」要求执行者把次数/退避/给上收敛成单一 `RecordAttempt(backoff)+判 n` 直线流程(避免重复自增)——这是明确实现指令,非占位;其余步骤均有具体代码/命令。

**Type consistency:** `LeaderElector`(`IsLeader`/`Run`)、`HealState`(`RecordAttempt/InCooldown/GivenUp/MarkGivenUp`)、`HealerConfig`、`StopParsing`、查询名 `ListRAGFlowFailedDocumentsForHeal`/`ListRAGFlowStuckRunningDocumentsForHeal` 前后一致;`row.ID` 若 sqlc 生成为 uuid 类型用 `.String()`、为 string 直接用(执行者按生成结果对齐,单一约定)。

> ⚠️ 执行者注意:`ragflow_documents.id` 的 sqlc 类型决定 `row.ID` 用法;`MarkRAGFlowDocumentManualReparseQueued(ctx, id string)` 入参为 string,统一以 string 传递 doc id。
