package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// memoryKnowledgeSource 是 KnowledgeFileSource 的内存实现,
// 通过预填的 master path → bytes 映射模拟主副本读取,
// 命中即返回 ReadCloser,缺失即抛 not found。
type memoryKnowledgeSource struct {
	files map[string][]byte
}

func (m *memoryKnowledgeSource) Open(p string) (io.ReadCloser, int64, error) {
	data, ok := m.files[p]
	if !ok {
		return nil, 0, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

// memoryKnowledgeSink 是 KnowledgeFileSink 的内存实现,用于断言 worker 调用 sink
// 时传入的 nodeID / appID / relPath / body, 验证 input 路径前缀拼接是否正确。
type memoryKnowledgeSink struct {
	uploads   []sinkCall
	deletes   []sinkCall
	uploadErr error
	deleteErr error
}

// sinkCall 记录一次 sink 调用的全部参数,便于按字段断言。
type sinkCall struct {
	node, app, relPath, body string
}

func (s *memoryKnowledgeSink) UploadAppInputFile(_ context.Context, nodeID, appID, relPath string, content io.Reader) error {
	if s.uploadErr != nil {
		return s.uploadErr
	}
	body, _ := io.ReadAll(content)
	s.uploads = append(s.uploads, sinkCall{node: nodeID, app: appID, relPath: relPath, body: string(body)})
	return nil
}

func (s *memoryKnowledgeSink) DeleteAppInputFile(_ context.Context, nodeID, appID, relPath string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deletes = append(s.deletes, sinkCall{node: nodeID, app: appID, relPath: relPath})
	return nil
}

// memoryAppLister 模拟 ListAppsByOrg,按预填 appsByOrg 返回 sqlc.App 列表;
// org scope 测试用它构造「该 org 在不同节点上拥有 N 个 app」的场景。
type memoryAppLister struct {
	appsByOrg map[string][]sqlc.App
	err       error
}

func (m *memoryAppLister) ListAppsByOrg(_ context.Context, arg sqlc.ListAppsByOrgParams) ([]sqlc.App, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.appsByOrg[uuidStringForKnowledgeSync(arg.OrgID)], nil
}

// hexUUIDForTest 把 8-4-4-4-12 字面量字符串转成 pgtype.UUID,
// 便于在 table-driven 与单测里构造 app / org / node 标识。
func hexUUIDForTest(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	require.NoError(t, u.Scan(s))
	return u
}

func buildKnowledgeJob(t *testing.T, payload []byte) sqlc.Job {
	t.Helper()
	return sqlc.Job{Type: domain.JobTypeKnowledgeSyncNode, PayloadJson: payload}
}

// TestKnowledgeSyncHandler_UploadOrgFile_FansOutToAppsOnNode 验证 org scope upload
// 事件被 handler 扇出到「该 org 在目标节点上的每个 app」: relPath 前缀必须为
// resources/knowledge/org/, body 与主副本一致, 与镜像 oc-entrypoint 读取路径对齐。
func TestKnowledgeSyncHandler_UploadOrgFile_FansOutToAppsOnNode(t *testing.T) {
	// 场景:org "o1" 在节点 n1 上有两个 app(a1/a2),还有一个落在 n2 的 a3——
	// 不应被本节点 job 触达;handler 必须只对 a1/a2 发上传。
	orgUUID := hexUUIDForTest(t, "11111111-1111-1111-1111-111111111111")
	node1 := hexUUIDForTest(t, "22222222-2222-2222-2222-222222222222")
	node2 := hexUUIDForTest(t, "33333333-3333-3333-3333-333333333333")
	appA1 := hexUUIDForTest(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1")
	appA2 := hexUUIDForTest(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa2")
	appA3 := hexUUIDForTest(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa3")

	source := &memoryKnowledgeSource{files: map[string][]byte{
		"org/o1/knowledge/notes/a.md": []byte("# hello"),
	}}
	sink := &memoryKnowledgeSink{}
	lister := &memoryAppLister{appsByOrg: map[string][]sqlc.App{
		uuidStringForKnowledgeSync(orgUUID): {
			{ID: appA1, OrgID: orgUUID, RuntimeNodeID: node1}, // 命中:与 payload.NodeID 同节点
			{ID: appA2, OrgID: orgUUID, RuntimeNodeID: node1}, // 命中:同节点另一个 app
			{ID: appA3, OrgID: orgUUID, RuntimeNodeID: node2}, // 跳过:不同节点
		},
	}}
	handler := NewKnowledgeSyncHandler(source, sink)
	handler.SetAppLister(lister)

	nodeIDStr := uuidStringForKnowledgeSync(node1)
	orgIDStr := uuidStringForKnowledgeSync(orgUUID)
	payload := []byte(`{"scope":"org","org_id":"` + orgIDStr + `","node_id":"` + nodeIDStr + `","change_type":"upload_file","rel_path":"notes/a.md","master_path":"org/o1/knowledge/notes/a.md"}`)

	require.NoError(t, handler.Handle(context.Background(), buildKnowledgeJob(t, payload)))

	require.Len(t, sink.uploads, 2) // 仅 n1 上的两个 app 被触达
	got := map[string]sinkCall{}
	for _, c := range sink.uploads {
		got[c.app] = c
	}
	a1Key := uuidStringForKnowledgeSync(appA1)
	a2Key := uuidStringForKnowledgeSync(appA2)
	require.Contains(t, got, a1Key)
	require.Contains(t, got, a2Key)
	// 关键断言:relPath 必须落到 input/resources/knowledge/org/<rel> 之下。
	assert.Equal(t, "resources/knowledge/org/notes/a.md", got[a1Key].relPath)
	assert.Equal(t, "resources/knowledge/org/notes/a.md", got[a2Key].relPath)
	assert.Equal(t, "# hello", got[a1Key].body) // 同一主副本扇出多次,内容应一致
	assert.Equal(t, "# hello", got[a2Key].body)
	assert.Equal(t, nodeIDStr, got[a1Key].node)
}

// TestKnowledgeSyncHandler_UploadAppFile_WritesAppInputPath 验证 app scope upload
// 事件:relPath 前缀必须为 resources/knowledge/app/, 不再走 legacy
// apps/<id>/knowledge/ 沙箱(agent T13 已下线)。
func TestKnowledgeSyncHandler_UploadAppFile_WritesAppInputPath(t *testing.T) {
	// 场景:某 app 的应用级知识库新增 docs/x.md,
	// 期望 handler 直接对该 app 调 UploadAppInputFile,prefix 走 app 子目录。
	source := &memoryKnowledgeSource{files: map[string][]byte{
		"org/o1/app/a1/knowledge/docs/x.md": []byte("body"),
	}}
	sink := &memoryKnowledgeSink{}
	handler := NewKnowledgeSyncHandler(source, sink)

	payload := []byte(`{"scope":"app","org_id":"o1","app_id":"a1","node_id":"n1","change_type":"upload_file","rel_path":"docs/x.md","master_path":"org/o1/app/a1/knowledge/docs/x.md"}`)
	require.NoError(t, handler.Handle(context.Background(), buildKnowledgeJob(t, payload)))

	require.Len(t, sink.uploads, 1)
	got := sink.uploads[0]
	assert.Equal(t, "n1", got.node)
	assert.Equal(t, "a1", got.app)
	// 关键断言:app scope 使用 resources/knowledge/app/ 前缀。
	assert.Equal(t, "resources/knowledge/app/docs/x.md", got.relPath)
	assert.Equal(t, "body", got.body)
}

// TestKnowledgeSyncHandler_DeleteAppFile_DeletesFromAppInputPath 验证 app scope
// delete 事件走 DeleteAppInputFile,relPath 同样带 resources/knowledge/app/ 前缀。
func TestKnowledgeSyncHandler_DeleteAppFile_DeletesFromAppInputPath(t *testing.T) {
	// 场景:某 app 的知识库文件被删除,handler 仅触达该 app 的 input 目录,不读主副本。
	sink := &memoryKnowledgeSink{}
	handler := NewKnowledgeSyncHandler(nil, sink)
	payload := []byte(`{"scope":"app","app_id":"a1","node_id":"n2","change_type":"delete_file","rel_path":"docs/x.md"}`)
	require.NoError(t, handler.Handle(context.Background(), buildKnowledgeJob(t, payload)))

	require.Len(t, sink.deletes, 1)
	got := sink.deletes[0]
	assert.Equal(t, "n2", got.node)
	assert.Equal(t, "a1", got.app)
	assert.Equal(t, "resources/knowledge/app/docs/x.md", got.relPath)
}

// TestKnowledgeSyncHandler_DeleteOrgFile_FansOutToAppsOnNode 验证 org scope
// delete 事件被 handler 扇出到该 org 在节点上的每个 app, 每个 app 各执行一次
// DeleteAppInputFile, 防止 oc-entrypoint 下次启动时仍读到已废弃 skill。
func TestKnowledgeSyncHandler_DeleteOrgFile_FansOutToAppsOnNode(t *testing.T) {
	// 场景:org 级 policy.md 被删,handler 应该对节点上属于该 org 的所有 app 都发删除,
	// 且 relPath 必须带 resources/knowledge/org/ 前缀,匹配 upload 路径。
	orgUUID := hexUUIDForTest(t, "11111111-1111-1111-1111-111111111111")
	node1 := hexUUIDForTest(t, "22222222-2222-2222-2222-222222222222")
	appA1 := hexUUIDForTest(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1")
	appA2 := hexUUIDForTest(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa2")

	sink := &memoryKnowledgeSink{}
	lister := &memoryAppLister{appsByOrg: map[string][]sqlc.App{
		uuidStringForKnowledgeSync(orgUUID): {
			{ID: appA1, OrgID: orgUUID, RuntimeNodeID: node1},
			{ID: appA2, OrgID: orgUUID, RuntimeNodeID: node1},
		},
	}}
	handler := NewKnowledgeSyncHandler(nil, sink)
	handler.SetAppLister(lister)

	nodeIDStr := uuidStringForKnowledgeSync(node1)
	orgIDStr := uuidStringForKnowledgeSync(orgUUID)
	payload := []byte(`{"scope":"org","org_id":"` + orgIDStr + `","node_id":"` + nodeIDStr + `","change_type":"delete_file","rel_path":"policy.md"}`)
	require.NoError(t, handler.Handle(context.Background(), buildKnowledgeJob(t, payload)))

	require.Len(t, sink.deletes, 2)
	for _, c := range sink.deletes {
		assert.Equal(t, "resources/knowledge/org/policy.md", c.relPath) // 与 upload 前缀严格对齐
	}
}

// TestKnowledgeSyncHandler_OrgScope_NoAppsOnNodeIsNoop 验证 org 在该节点上没有任何
// app 时, handler 既不读主副本也不写文件; 仅依赖 status_writer 把状态翻 synced
// (主副本变更与该节点无关, 不能让 UI 永远 pending)。
func TestKnowledgeSyncHandler_OrgScope_NoAppsOnNodeIsNoop(t *testing.T) {
	// 场景:org 在节点上有 app 但都绑在其它节点,本节点扇出列表为空,
	// handler 应直接返回 nil,不调用 sink。
	orgUUID := hexUUIDForTest(t, "11111111-1111-1111-1111-111111111111")
	thisNode := hexUUIDForTest(t, "22222222-2222-2222-2222-222222222222")
	otherNode := hexUUIDForTest(t, "33333333-3333-3333-3333-333333333333")
	app := hexUUIDForTest(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1")

	source := &memoryKnowledgeSource{files: map[string][]byte{
		"org/o1/knowledge/note.md": []byte("body"),
	}}
	sink := &memoryKnowledgeSink{}
	lister := &memoryAppLister{appsByOrg: map[string][]sqlc.App{
		uuidStringForKnowledgeSync(orgUUID): {
			{ID: app, OrgID: orgUUID, RuntimeNodeID: otherNode}, // 跑在另一个节点
		},
	}}
	handler := NewKnowledgeSyncHandler(source, sink)
	handler.SetAppLister(lister)

	payload := []byte(`{"scope":"org","org_id":"` + uuidStringForKnowledgeSync(orgUUID) + `","node_id":"` + uuidStringForKnowledgeSync(thisNode) + `","change_type":"upload_file","rel_path":"note.md","master_path":"org/o1/knowledge/note.md"}`)
	require.NoError(t, handler.Handle(context.Background(), buildKnowledgeJob(t, payload)))
	assert.Empty(t, sink.uploads) // 没有命中节点的 app
}

// TestKnowledgeSyncHandler_RejectsUnknownChangeType 验证 handler 拒绝未在白名单
// 内的 change_type, 防止脏 payload 触达 sink。
func TestKnowledgeSyncHandler_RejectsUnknownChangeType(t *testing.T) {
	handler := NewKnowledgeSyncHandler(nil, &memoryKnowledgeSink{})
	payload := []byte(`{"scope":"org","org_id":"o","node_id":"n","change_type":"boom","rel_path":"x"}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	require.Error(t, err)
	require.Contains(t, err.Error(), "未知 change_type")
}

// TestKnowledgeSyncHandler_RejectsMissingRelPath 验证 upload/delete 必须携带
// rel_path; 仅 noop 允许 rel_path 缺失。
func TestKnowledgeSyncHandler_RejectsMissingRelPath(t *testing.T) {
	handler := NewKnowledgeSyncHandler(nil, &memoryKnowledgeSink{})
	payload := []byte(`{"scope":"org","org_id":"o","node_id":"n","change_type":"upload_file","rel_path":""}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	require.Error(t, err)
	require.Contains(t, err.Error(), "缺少 rel_path")
}

// TestKnowledgeSyncHandler_PropagatesUploadError 验证 sink 上传错误能透传出来,
// handler 不吞错; 这是 worker 决定是否重试整条 job 的关键依据。
func TestKnowledgeSyncHandler_PropagatesUploadError(t *testing.T) {
	source := &memoryKnowledgeSource{files: map[string][]byte{"x": []byte("ok")}}
	sink := &memoryKnowledgeSink{uploadErr: errors.New("agent unreachable")}
	handler := NewKnowledgeSyncHandler(source, sink)
	// 用 app scope 简化路径:不依赖 lister 即可触发 sink 上传错误。
	payload := []byte(`{"scope":"app","org_id":"o","app_id":"a","node_id":"n","change_type":"upload_file","rel_path":"y","master_path":"x"}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	require.Error(t, err)
	require.Contains(t, err.Error(), "上传到节点失败")
}

// TestKnowledgeSyncHandler_RejectsMissingNodeID 验证缺少 node_id 时立即拒绝,
// 防止 sink 收到空 nodeID 后向 manager 默认 baseURL 误发请求。
func TestKnowledgeSyncHandler_RejectsMissingNodeID(t *testing.T) {
	handler := NewKnowledgeSyncHandler(nil, &memoryKnowledgeSink{})
	payload := []byte(`{"scope":"org","org_id":"o","change_type":"delete_file","rel_path":"x"}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	require.Error(t, err)
}

// TestKnowledgeSyncHandler_AppScopeSuccessWritesAudit 验证 app scope 同步成功时
// 通过注入的 auditor 写一条 result=succeeded 的 audit_logs 事件,target_type
// 必须为 app_knowledge_sync,target_id 取 app_id,让审计页面可按应用筛选。
func TestKnowledgeSyncHandler_AppScopeSuccessWritesAudit(t *testing.T) {
	source := &memoryKnowledgeSource{files: map[string][]byte{"org/o1/app/a1/knowledge/x.md": []byte("body")}}
	sink := &memoryKnowledgeSink{}
	auditor := &fakeWorkerAuditor{}
	handler := NewKnowledgeSyncHandler(source, sink)
	handler.SetAuditor(auditor)

	payload := []byte(`{"scope":"app","org_id":"o1","app_id":"a1","node_id":"n1","change_type":"upload_file","rel_path":"x.md","master_path":"org/o1/app/a1/knowledge/x.md"}`)
	require.NoError(t, handler.Handle(context.Background(), buildKnowledgeJob(t, payload)))

	require.Len(t, auditor.events, 1)
	ev := auditor.events[0]
	assert.Equal(t, "app_knowledge_sync", ev.TargetType)
	assert.Equal(t, "a1", ev.TargetID)
	assert.Equal(t, "upload_file", ev.Action)
	assert.Equal(t, "succeeded", ev.Result)
	assert.Equal(t, "n1", ev.Metadata["node_id"])
	assert.Equal(t, "x.md", ev.Metadata["rel_path"])
	// 详情字段应展示「文件 <relPath>」，便于审计列表识别同步对象。
	assert.Equal(t, "文件 x.md", ev.DetailMessage)
}

// TestKnowledgeSyncHandler_AppScopeFailureWritesAudit 验证失败时 result=failed
// 且 error_message 非空,handler 不吞错(仍返回原 sink 错误)。
func TestKnowledgeSyncHandler_AppScopeFailureWritesAudit(t *testing.T) {
	source := &memoryKnowledgeSource{files: map[string][]byte{"m": []byte("x")}}
	sink := &memoryKnowledgeSink{uploadErr: errors.New("agent unreachable")}
	auditor := &fakeWorkerAuditor{}
	handler := NewKnowledgeSyncHandler(source, sink)
	handler.SetAuditor(auditor)

	payload := []byte(`{"scope":"app","org_id":"o","app_id":"a","node_id":"n","change_type":"upload_file","rel_path":"r","master_path":"m"}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	require.Error(t, err)
	require.Len(t, auditor.events, 1)
	assert.Equal(t, "failed", auditor.events[0].Result)
	assert.Contains(t, auditor.events[0].ErrorMessage, "agent unreachable")
	// 失败路径详情同样附带文件名。
	assert.Equal(t, "文件 r", auditor.events[0].DetailMessage)
}

// TestKnowledgeSyncHandler_OrgScopeDoesNotWriteAudit 验证 org scope 不走 audit_logs
// (已经有 knowledge_sync_status 表),避免双写造成事件冗余。
func TestKnowledgeSyncHandler_OrgScopeDoesNotWriteAudit(t *testing.T) {
	// 场景:org scope 同步即便成功也只翻 status,不写 audit_logs;
	// 这里不需要 app fanout,因此 lister 留空,handler 退化为 no-op 写文件流程,
	// 但仍按 org scope 走 status 写入分支。
	source := &memoryKnowledgeSource{files: map[string][]byte{"m": []byte("x")}}
	sink := &memoryKnowledgeSink{}
	auditor := &fakeWorkerAuditor{}
	handler := NewKnowledgeSyncHandler(source, sink)
	handler.SetAuditor(auditor)

	payload := []byte(`{"scope":"org","org_id":"o1","node_id":"n1","change_type":"upload_file","rel_path":"r","master_path":"m"}`)
	require.NoError(t, handler.Handle(context.Background(), buildKnowledgeJob(t, payload)))
	require.Empty(t, auditor.events) // org scope 必须由 knowledge_sync_status 表承担,不进 audit_logs。
}

// fakeWorkerAuditor 收集 worker handler 提交的 AuditEvent,供断言。
type fakeWorkerAuditor struct {
	events []service.AuditEvent
}

func (f *fakeWorkerAuditor) Record(_ context.Context, event service.AuditEvent) (service.AuditResult, error) {
	f.events = append(f.events, event)
	return service.AuditResult{}, nil
}
