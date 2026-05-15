// Package main 的 knowledge_dispatcher 测试覆盖 knowledge 同步调度器与
// reload 协调器的核心路径,包括:
//   - RetryOrgNode 真正全量重推(扫主副本所有文件)而非旧 noop 行为
//   - DispatchOrgChange / DispatchAppChange 入 sync job 后联动 reloader 入 restart job
//   - knowledgeReloadCoordinator 的状态过滤、in-memory debounce、按 org 列 app 行为
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// fakeKnowledgeJobsQueries 是 knowledgeJobsQueries 的内存实现,
// 不依赖真实 Postgres,让 dispatcher 路径在单测中可被穷举。
type fakeKnowledgeJobsQueries struct {
	mu         sync.Mutex
	createdJob []sqlc.CreateJobParams
	apps       map[string]sqlc.App // app_id → app
	appsByOrg  map[string][]sqlc.App
	nodes      []sqlc.RuntimeNode
	createErr  error
}

func (f *fakeKnowledgeJobsQueries) ListRuntimeNodes(_ context.Context, _ sqlc.ListRuntimeNodesParams) ([]sqlc.RuntimeNode, error) {
	return f.nodes, nil
}

func (f *fakeKnowledgeJobsQueries) GetApp(_ context.Context, id pgtype.UUID) (sqlc.App, error) {
	app, ok := f.apps[uuidToStringWiring(id)]
	if !ok {
		return sqlc.App{}, errors.New("not found")
	}
	return app, nil
}

func (f *fakeKnowledgeJobsQueries) ListAppsByOrg(_ context.Context, arg sqlc.ListAppsByOrgParams) ([]sqlc.App, error) {
	return f.appsByOrg[uuidToStringWiring(arg.OrgID)], nil
}

func (f *fakeKnowledgeJobsQueries) CreateJob(_ context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error) {
	if f.createErr != nil {
		return sqlc.Job{}, f.createErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createdJob = append(f.createdJob, arg)
	return sqlc.Job{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}}, nil
}

// fakeKnowledgeReader 是 workerhandlers.KnowledgeReader 的内存实现,
// 用于断言 RetryOrgNode 真扫了主副本 + 入了对应 upload_file job。
type fakeKnowledgeReader struct {
	files map[string][]byte // master path → 内容
}

func (f *fakeKnowledgeReader) WalkFiles(prefix string, fn func(relPath string, size int64) error) error {
	for p, body := range f.files {
		if len(p) <= len(prefix)+1 || p[:len(prefix)+1] != prefix+"/" {
			continue
		}
		rel := p[len(prefix)+1:]
		if err := fn(rel, int64(len(body))); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeKnowledgeReader) Open(p string) (io.ReadCloser, int64, error) {
	body, ok := f.files[p]
	if !ok {
		return nil, 0, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(body)), int64(len(body)), nil
}

// fakeReloader 替换 dispatcher 注入的 reloader,断言 DispatchOrg/App 是否触发了 reload。
type fakeReloader struct {
	appCalls []string
	orgCalls []string
}

func (f *fakeReloader) EnqueueAppReload(_ context.Context, appID string) error {
	f.appCalls = append(f.appCalls, appID)
	return nil
}

func (f *fakeReloader) EnqueueOrgReload(_ context.Context, orgID string) error {
	f.orgCalls = append(f.orgCalls, orgID)
	return nil
}

// uuidFromHex 是测试辅助:把 16 hex 字节字符串转 pgtype.UUID。
// 测试构造 app/org 时只需写"00000000-0000-0000-0000-000000000001"这种字面量。
func uuidFromHex(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	require.NoError(t, u.Scan(s))
	return u
}

// TestRetryOrgNode_FullResync 验证 RetryOrgNode 在注入了 KnowledgeReader
// 之后扫主副本所有文件,逐个入一条 upload_file sync job;不再是空翻状态的 noop。
func TestRetryOrgNode_FullResync(t *testing.T) {
	queries := &fakeKnowledgeJobsQueries{}
	dispatcher := newKnowledgeSyncDispatcher(queries, nil)
	dispatcher.SetKnowledgeReader(&fakeKnowledgeReader{
		files: map[string][]byte{
			"org/o1/knowledge/a.md":         []byte("A"), // 普通文件
			"org/o1/knowledge/sub/b.md":     []byte("B"), // 子目录:验证 WalkFiles 递归被透传
			"org/o2/knowledge/other.md":     []byte("Z"), // 其它 org:不应被本次重推覆盖
		},
	})

	require.NoError(t, dispatcher.RetryOrgNode(context.Background(), "o1", "n1"))
	require.Equal(t, 2, len(queries.createdJob)) // 仅 o1 的两个文件入队,o2 被前缀过滤掉

	// 解析两个 job 的 payload,确保 scope/org_id/node_id/change_type 正确。
	for _, j := range queries.createdJob {
		assert.Equal(t, "knowledge_sync_node", j.Type)
		var p map[string]any
		require.NoError(t, json.Unmarshal(j.PayloadJson, &p))
		assert.Equal(t, "org", p["scope"])
		assert.Equal(t, "o1", p["org_id"])
		assert.Equal(t, "n1", p["node_id"])
		assert.Equal(t, "upload_file", p["change_type"])
		assert.NotEmpty(t, p["rel_path"])
		assert.NotEmpty(t, p["master_path"])
	}
}

// TestRetryOrgNode_EmptyMasterFallsBackToNoop 验证主副本为空时退化为 noop,
// 让 worker 仍能把状态翻到 synced,避免 UI 永远 pending。
func TestRetryOrgNode_EmptyMasterFallsBackToNoop(t *testing.T) {
	queries := &fakeKnowledgeJobsQueries{}
	dispatcher := newKnowledgeSyncDispatcher(queries, nil)
	dispatcher.SetKnowledgeReader(&fakeKnowledgeReader{files: map[string][]byte{}})

	require.NoError(t, dispatcher.RetryOrgNode(context.Background(), "o1", "n1"))
	require.Equal(t, 1, len(queries.createdJob))
	var p map[string]any
	require.NoError(t, json.Unmarshal(queries.createdJob[0].PayloadJson, &p))
	assert.Equal(t, "noop", p["change_type"])
}

// TestRetryOrgNode_NoReaderFallsBackToNoop 验证未注入 KnowledgeReader(测试装配)时
// 退化为旧 noop 行为,保持向后兼容。
func TestRetryOrgNode_NoReaderFallsBackToNoop(t *testing.T) {
	queries := &fakeKnowledgeJobsQueries{}
	dispatcher := newKnowledgeSyncDispatcher(queries, nil)
	require.NoError(t, dispatcher.RetryOrgNode(context.Background(), "o1", "n1"))
	require.Equal(t, 1, len(queries.createdJob))
	var p map[string]any
	require.NoError(t, json.Unmarshal(queries.createdJob[0].PayloadJson, &p))
	assert.Equal(t, "noop", p["change_type"])
}

// TestDispatchOrgChange_TriggersReload 验证 org 改动入完 sync job 后,
// 还会让 reloader 给整个 org 入 reload(让运行中 hermes 容器读到新内容)。
func TestDispatchOrgChange_TriggersReload(t *testing.T) {
	queries := &fakeKnowledgeJobsQueries{
		nodes: []sqlc.RuntimeNode{
			{ID: uuidFromHex(t, "00000000-0000-0000-0000-000000000001"), Status: "active"},   // 应入队
			{ID: uuidFromHex(t, "00000000-0000-0000-0000-000000000002"), Status: "draining"}, // 非 active 跳过
		},
	}
	reloader := &fakeReloader{}
	dispatcher := newKnowledgeSyncDispatcher(queries, nil)
	dispatcher.SetReloader(reloader)

	require.NoError(t, dispatcher.DispatchOrgChange(context.Background(), "org-1", "doc.md", "upload_file", "org/org-1/knowledge/doc.md"))
	require.Equal(t, 1, len(queries.createdJob)) // 仅 active 节点入 sync job
	require.Equal(t, []string{"org-1"}, reloader.orgCalls)
}

// TestDispatchAppChange_TriggersReload 验证 app 改动入完 sync job 后,
// 会让 reloader 给该 app 入 reload。
func TestDispatchAppChange_TriggersReload(t *testing.T) {
	appUUID := uuidFromHex(t, "00000000-0000-0000-0000-000000000a01")
	queries := &fakeKnowledgeJobsQueries{
		apps: map[string]sqlc.App{
			uuidToStringWiring(appUUID): {
				ID:            appUUID,
				Status:        domain.AppStatusRunning,
				RuntimeNodeID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
			},
		},
	}
	reloader := &fakeReloader{}
	dispatcher := newKnowledgeSyncDispatcher(queries, nil)
	dispatcher.SetReloader(reloader)

	require.NoError(t, dispatcher.DispatchAppChange(context.Background(), "org-1", uuidToStringWiring(appUUID), "doc.md", "upload_file", "org/org-1/app/.../knowledge/doc.md"))
	require.Equal(t, 1, len(queries.createdJob))
	require.Equal(t, []string{uuidToStringWiring(appUUID)}, reloader.appCalls)
}

// TestReloadCoordinator_DebouncesSecondCall 验证 in-memory debounce:
// 同一 app 在 window 内第二次调用 EnqueueAppReload 时不再入队,
// 避免 N 次连续上传 = N 次容器重启。
func TestReloadCoordinator_DebouncesSecondCall(t *testing.T) {
	appUUID := uuidFromHex(t, "00000000-0000-0000-0000-000000000a01")
	queries := &fakeKnowledgeJobsQueries{
		apps: map[string]sqlc.App{
			uuidToStringWiring(appUUID): {
				ID:            appUUID,
				Status:        domain.AppStatusRunning,
				RuntimeNodeID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
			},
		},
	}
	c := newKnowledgeReloadCoordinator(queries, nil)
	c.window = time.Hour // 测试里把 window 拉到极大,保证第二次 100% 被抑制
	c.delay = time.Millisecond

	require.NoError(t, c.EnqueueAppReload(context.Background(), uuidToStringWiring(appUUID)))
	require.NoError(t, c.EnqueueAppReload(context.Background(), uuidToStringWiring(appUUID)))
	require.Equal(t, 1, len(queries.createdJob)) // 第二次被 debounce 抑制
}

// TestReloadCoordinator_SkipsNonRunningApp 验证非 running/binding_waiting 状态
// 的 app 不入 reload job(init/stopped/deleted 重启没有业务意义)。
func TestReloadCoordinator_SkipsNonRunningApp(t *testing.T) {
	appUUID := uuidFromHex(t, "00000000-0000-0000-0000-000000000a01")
	queries := &fakeKnowledgeJobsQueries{
		apps: map[string]sqlc.App{
			uuidToStringWiring(appUUID): {
				ID:     appUUID,
				Status: "stopped", // 关键:非 running
			},
		},
	}
	c := newKnowledgeReloadCoordinator(queries, nil)
	require.NoError(t, c.EnqueueAppReload(context.Background(), uuidToStringWiring(appUUID)))
	require.Empty(t, queries.createdJob)
}

// TestReloadCoordinator_OrgReloadFiltersAndDispatches 验证 EnqueueOrgReload 列
// 整个 org 的 app,过滤掉非 running 的,逐个走 EnqueueAppReload 的入队路径。
func TestReloadCoordinator_OrgReloadFiltersAndDispatches(t *testing.T) {
	orgUUID := uuidFromHex(t, "00000000-0000-0000-0000-0000000000a1")
	runningApp := sqlc.App{
		ID:            uuidFromHex(t, "00000000-0000-0000-0000-0000000000b1"),
		Status:        domain.AppStatusRunning,
		RuntimeNodeID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
	}
	bindingApp := sqlc.App{
		ID:            uuidFromHex(t, "00000000-0000-0000-0000-0000000000b2"),
		Status:        domain.AppStatusBindingWaiting, // 也应入队
		RuntimeNodeID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
	}
	stoppedApp := sqlc.App{
		ID:     uuidFromHex(t, "00000000-0000-0000-0000-0000000000b3"),
		Status: "stopped", // 必须被跳过
	}
	queries := &fakeKnowledgeJobsQueries{
		appsByOrg: map[string][]sqlc.App{
			uuidToStringWiring(orgUUID): {runningApp, bindingApp, stoppedApp},
		},
	}
	c := newKnowledgeReloadCoordinator(queries, nil)
	require.NoError(t, c.EnqueueOrgReload(context.Background(), uuidToStringWiring(orgUUID)))
	require.Equal(t, 2, len(queries.createdJob)) // running + binding_waiting,stopped 被跳过
	for _, j := range queries.createdJob {
		assert.Equal(t, domain.JobTypeAppRestartContainer, j.Type)
	}
}
