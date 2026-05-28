package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/ragflow"
	"oc-manager/internal/store/sqlc"
)

// fakeRefresherStore 是后台轮询任务测试用的最小 store 替身。
// 只实现 refresher 真实需要的两个查询，避免与 fakeKnowledgeStore 耦合。
type fakeRefresherStore struct {
	listRows []sqlc.ListRAGFlowDocumentsNeedingRefreshRow
	listErr  error
	listCnt  int
	updates  []sqlc.UpdateRAGFlowDocumentParseStatusParams
}

func (s *fakeRefresherStore) ListRAGFlowDocumentsNeedingRefresh(_ context.Context, limit int32) ([]sqlc.ListRAGFlowDocumentsNeedingRefreshRow, error) {
	s.listCnt++
	if s.listErr != nil {
		return nil, s.listErr
	}
	if int(limit) >= len(s.listRows) {
		return s.listRows, nil
	}
	return s.listRows[:limit], nil
}

// UpdateRAGFlowDocumentParseStatus 为 :exec；stub 记录参数供测试断言，不返回文档行。
func (s *fakeRefresherStore) UpdateRAGFlowDocumentParseStatus(_ context.Context, arg sqlc.UpdateRAGFlowDocumentParseStatusParams) error {
	s.updates = append(s.updates, arg)
	// 模拟 :exec 写入成功后返回 nil；服务层如需读回会调 GetRAGFlowDocument（此处不需要）。
	_ = sql.ErrNoRows // 仅用于表明已知语义，非真实返回路径
	return nil
}

// fakeRefresherRAGFlow 模拟 RAGFlow ListDocuments，可按 datasetID 返回不同响应或注入错误。
type fakeRefresherRAGFlow struct {
	responses     map[string][]ragflow.Document
	errs          map[string]error
	listCallOrder []string
}

func (f *fakeRefresherRAGFlow) ListDocuments(_ context.Context, datasetID string, _ int32, _ int32, _ string, _ string) ([]ragflow.Document, int32, error) {
	f.listCallOrder = append(f.listCallOrder, datasetID)
	if err, ok := f.errs[datasetID]; ok {
		return nil, 0, err
	}
	docs := f.responses[datasetID]
	return docs, int32(len(docs)), nil
}

// makeRefreshRow 构建测试用的 ListRAGFlowDocumentsNeedingRefreshRow。
// ID / DatasetID 为字符串（MySQL CHAR(36)）；RemoteDatasetID 为 null.String。
func makeRefreshRow(id, datasetID, remoteDatasetID, remoteDocID, status string, progress int32) sqlc.ListRAGFlowDocumentsNeedingRefreshRow {
	return sqlc.ListRAGFlowDocumentsNeedingRefreshRow{
		ID:                mustParseUUID(id),
		DatasetID:         mustParseUUID(datasetID),
		RagflowDocumentID: remoteDocID,
		ParseStatus:       status,
		Progress:          progress,
		RemoteDatasetID:   null.StringFrom(remoteDatasetID),
	}
}

func TestRagflowParseStatusRefresher_NoDocs(t *testing.T) {
	// 没有待刷新文档时不调 RAGFlow，也不写 DB。
	store := &fakeRefresherStore{}
	rf := &fakeRefresherRAGFlow{}
	refresher := NewRagflowParseStatusRefresher(store, rf)

	require.NoError(t, refresher.Tick(context.Background()))
	assert.Equal(t, 1, store.listCnt)
	assert.Empty(t, rf.listCallOrder)
	assert.Empty(t, store.updates)
}

func TestRagflowParseStatusRefresher_UpdatesQueuedToCompleted(t *testing.T) {
	// 远端文档解析完成后，应把本地状态从 queued 推进到 completed，progress=100，并清空 last_error。
	store := &fakeRefresherStore{listRows: []sqlc.ListRAGFlowDocumentsNeedingRefreshRow{
		makeRefreshRow("00000000-0000-0000-0000-000000000a01", "00000000-0000-0000-0000-000000000d01", "remote-ds-1", "remote-doc-1", "queued", 0),
	}}
	rf := &fakeRefresherRAGFlow{responses: map[string][]ragflow.Document{
		"remote-ds-1": {{ID: "remote-doc-1", Run: "DONE"}},
	}}
	refresher := NewRagflowParseStatusRefresher(store, rf)

	require.NoError(t, refresher.Tick(context.Background()))
	require.Len(t, store.updates, 1)
	assert.Equal(t, "completed", store.updates[0].ParseStatus)
	assert.Equal(t, int32(100), store.updates[0].Progress)
	assert.False(t, store.updates[0].LastError.Valid)
}

func TestRagflowParseStatusRefresher_GroupsByDataset(t *testing.T) {
	// 同一 dataset 下多个待刷新文档应只调用一次 ListDocuments，避免重复请求 RAGFlow。
	store := &fakeRefresherStore{listRows: []sqlc.ListRAGFlowDocumentsNeedingRefreshRow{
		makeRefreshRow("00000000-0000-0000-0000-000000000a01", "00000000-0000-0000-0000-000000000d01", "remote-ds-1", "remote-doc-1", "queued", 0),
		makeRefreshRow("00000000-0000-0000-0000-000000000a02", "00000000-0000-0000-0000-000000000d01", "remote-ds-1", "remote-doc-2", "running", 0),
		makeRefreshRow("00000000-0000-0000-0000-000000000a03", "00000000-0000-0000-0000-000000000d02", "remote-ds-2", "remote-doc-3", "queued", 0),
	}}
	rf := &fakeRefresherRAGFlow{responses: map[string][]ragflow.Document{
		"remote-ds-1": {
			{ID: "remote-doc-1", Run: "DONE"},
			{ID: "remote-doc-2", Run: "DONE"},
		},
		"remote-ds-2": {
			{ID: "remote-doc-3", Run: "FAIL"},
		},
	}}
	refresher := NewRagflowParseStatusRefresher(store, rf)

	require.NoError(t, refresher.Tick(context.Background()))
	// 验证只调了两次 ListDocuments（一次 ds-1 一次 ds-2），不会按文档逐个调用。
	assert.Len(t, rf.listCallOrder, 2)
	assert.ElementsMatch(t, []string{"remote-ds-1", "remote-ds-2"}, rf.listCallOrder)
	require.Len(t, store.updates, 3)
}

func TestRagflowParseStatusRefresher_NoChangeNoUpdate(t *testing.T) {
	// 远端状态与本地一致时不应触发更新，避免无意义的 DB 写入和 updated_at 抖动。
	store := &fakeRefresherStore{listRows: []sqlc.ListRAGFlowDocumentsNeedingRefreshRow{
		makeRefreshRow("00000000-0000-0000-0000-000000000a01", "00000000-0000-0000-0000-000000000d01", "remote-ds-1", "remote-doc-1", "running", 0),
	}}
	rf := &fakeRefresherRAGFlow{responses: map[string][]ragflow.Document{
		"remote-ds-1": {{ID: "remote-doc-1", Run: "RUNNING"}},
	}}
	refresher := NewRagflowParseStatusRefresher(store, rf)

	require.NoError(t, refresher.Tick(context.Background()))
	assert.Empty(t, store.updates)
}

func TestRagflowParseStatusRefresher_RemoteMissingMarksFailed(t *testing.T) {
	// RAGFlow 返回列表中找不到对应 document 时（远端被外部删除），本地标记 failed 并写入提示。
	store := &fakeRefresherStore{listRows: []sqlc.ListRAGFlowDocumentsNeedingRefreshRow{
		makeRefreshRow("00000000-0000-0000-0000-000000000a01", "00000000-0000-0000-0000-000000000d01", "remote-ds-1", "remote-doc-1", "queued", 0),
	}}
	rf := &fakeRefresherRAGFlow{responses: map[string][]ragflow.Document{
		"remote-ds-1": {},
	}}
	refresher := NewRagflowParseStatusRefresher(store, rf)

	require.NoError(t, refresher.Tick(context.Background()))
	require.Len(t, store.updates, 1)
	assert.Equal(t, "failed", store.updates[0].ParseStatus)
	assert.True(t, store.updates[0].LastError.Valid)
}

func TestRagflowParseStatusRefresher_ListErrorPreservesStatusButWritesLastError(t *testing.T) {
	// 单个 dataset 的 RAGFlow 调用失败不应影响其他 dataset；
	// 失败组内的文档保留原 parse_status / progress，但 last_error 写入失败原因等待下一轮重试。
	store := &fakeRefresherStore{listRows: []sqlc.ListRAGFlowDocumentsNeedingRefreshRow{
		makeRefreshRow("00000000-0000-0000-0000-000000000a01", "00000000-0000-0000-0000-000000000d01", "remote-ds-1", "remote-doc-1", "queued", 0),
		makeRefreshRow("00000000-0000-0000-0000-000000000a02", "00000000-0000-0000-0000-000000000d02", "remote-ds-2", "remote-doc-2", "queued", 0),
	}}
	rf := &fakeRefresherRAGFlow{
		responses: map[string][]ragflow.Document{
			"remote-ds-2": {{ID: "remote-doc-2", Run: "DONE"}},
		},
		errs: map[string]error{
			"remote-ds-1": errors.New("ragflow 网络抖动"),
		},
	}
	refresher := NewRagflowParseStatusRefresher(store, rf)

	err := refresher.Tick(context.Background())
	// 失败被冒泡给 PeriodicReconciler 仅作日志输出；成功 dataset 不受影响。
	require.Error(t, err)
	require.Len(t, store.updates, 2)

	// ID 已是 string，直接作 map key，无需转换。
	updateByID := map[string]sqlc.UpdateRAGFlowDocumentParseStatusParams{}
	for _, u := range store.updates {
		updateByID[u.ID] = u
	}
	failed := updateByID["00000000-0000-0000-0000-000000000a01"]
	assert.Equal(t, "queued", failed.ParseStatus)
	assert.True(t, failed.LastError.Valid)
	assert.Contains(t, failed.LastError.String, "ragflow 网络抖动")

	completed := updateByID["00000000-0000-0000-0000-000000000a02"]
	assert.Equal(t, "completed", completed.ParseStatus)
	assert.Equal(t, int32(100), completed.Progress)
	assert.False(t, completed.LastError.Valid)
}

func TestRagflowParseStatusRefresher_StoreListErrorReturned(t *testing.T) {
	// 顶层扫描失败应直接冒泡，避免后续 nil rows 触发空操作伪装成功。
	store := &fakeRefresherStore{listErr: errors.New("db 连接断开")}
	rf := &fakeRefresherRAGFlow{}
	refresher := NewRagflowParseStatusRefresher(store, rf)

	require.Error(t, refresher.Tick(context.Background()))
	assert.Empty(t, rf.listCallOrder)
	assert.Empty(t, store.updates)
}
