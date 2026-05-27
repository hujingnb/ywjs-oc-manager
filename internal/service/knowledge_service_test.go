// Package service 的 knowledge_service_test 覆盖 RAGFlow-backed 知识库的 manager 权限边界。
package service

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/ragflow"
	"oc-manager/internal/store/sqlc"
)

const (
	testKnowledgeOrg       = "00000000-0000-0000-0000-000000000e01"
	testKnowledgeOrg2      = "00000000-0000-0000-0000-000000000e02"
	testKnowledgeApp       = "00000000-0000-0000-0000-000000000e03"
	testKnowledgeOwner     = "00000000-0000-0000-0000-000000000e04"
	testKnowledgeDocument  = "00000000-0000-0000-0000-000000000e05"
	testRuntimeToken       = "runtime-token"
	testRuntimeTokenHash   = "af4a4b5c"
	testRemoteOrgDatasetID = "org-ds"
	testRemoteAppDatasetID = "app-ds"
)

// TestRAGFlowKnowledgeListOrgUsesManagerPermission 验证组织知识库读取只由 manager principal 判权，RAGFlow 不参与授权。
func TestRAGFlowKnowledgeListOrgUsesManagerPermission(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.docs[testKnowledgeDocument] = testDocument(t, "org", "policy.md", store.orgDataset.ID)

	result, err := svc.ListOrg(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg}, testKnowledgeOrg, 1, 50, "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.Total)
	assert.Equal(t, "policy.md", result.Items[0].Name)

	_, err = svc.ListOrg(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg2}, testKnowledgeOrg, 1, 50, "", "")
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
	assert.Empty(t, rf.retrieveDatasetIDs)
	assert.Empty(t, rf.deleteCalls)
}

// TestRAGFlowKnowledgeUploadOrgTriggersParse 验证组织管理员上传文件后写入 document 映射并触发 RAGFlow 解析。
func TestRAGFlowKnowledgeUploadOrgTriggersParse(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)

	doc, err := svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "report.md", strings.NewReader("# report"), 8)
	require.NoError(t, err)

	assert.Equal(t, "report.md", doc.Name)
	assert.Equal(t, "queued", doc.ParseStatus)
	require.Len(t, rf.uploadCalls, 1)
	assert.Equal(t, testRemoteOrgDatasetID, rf.uploadCalls[0].datasetID)
	assert.Equal(t, "report.md", rf.uploadCalls[0].filename)
	require.Len(t, rf.parseCalls, 1)
	assert.Equal(t, testRemoteOrgDatasetID, rf.parseCalls[0].datasetID)
	assert.Equal(t, []string{"remote-doc-1"}, rf.parseCalls[0].documentIDs)
	require.Len(t, store.createdDocs, 1)
	assert.Equal(t, "remote-doc-1", store.createdDocs[0].RagflowDocumentID)
}

// TestRAGFlowKnowledgeDeleteAppRejectsOtherOwner 验证实例知识库删除先按 manager app owner 判权，禁止路径不会调用 RAGFlow。
func TestRAGFlowKnowledgeDeleteAppRejectsOtherOwner(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.docs[testKnowledgeDocument] = testDocument(t, "app", "app.md", store.appDataset.ID)

	err := svc.DeleteAppFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "other"}, testKnowledgeApp, testKnowledgeDocument)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
	assert.Empty(t, rf.deleteCalls)

	err = svc.DeleteAppFile(context.Background(), appOwnerPrincipal(), testKnowledgeApp, testKnowledgeDocument)
	require.NoError(t, err)
	require.Len(t, rf.deleteCalls, 1)
	assert.Equal(t, testRemoteAppDatasetID, rf.deleteCalls[0].datasetID)
	assert.Equal(t, []string{"remote-doc-1"}, rf.deleteCalls[0].documentIDs)
}

// TestRuntimeSearchUsesOnlyCurrentAppAndOrgDatasets 验证 runtime 检索只使用 token 解析出的当前实例和所属组织 dataset。
func TestRuntimeSearchUsesOnlyCurrentAppAndOrgDatasets(t *testing.T) {
	svc, _, rf := newRAGFlowKnowledgeTestService(t)

	_, err := svc.RuntimeSearch(context.Background(), testRuntimeToken, "退款政策", 8)
	require.NoError(t, err)

	require.Len(t, rf.retrieveCalls, 2)
	assert.Equal(t, []string{testRemoteAppDatasetID}, rf.retrieveCalls[0].datasetIDs)
	assert.Equal(t, []string{testRemoteOrgDatasetID}, rf.retrieveCalls[1].datasetIDs)
	assert.Equal(t, "退款政策", rf.retrieveCalls[0].question)
	assert.Equal(t, int32(8), rf.retrieveCalls[0].topK)
}

// TestRuntimeSearchNormalizesTopK 验证 runtime 检索在调用 RAGFlow 前应用 manager 默认值和上限，
// 避免 RAGFlow 使用 1024 的服务端默认值或被异常大请求放大。
func TestRuntimeSearchNormalizesTopK(t *testing.T) {
	svc, _, rf := newRAGFlowKnowledgeTestService(t)

	_, err := svc.RuntimeSearch(context.Background(), testRuntimeToken, "退款政策", 0)
	require.NoError(t, err)
	require.Len(t, rf.retrieveCalls, 2)
	assert.Equal(t, int32(8), rf.retrieveCalls[0].topK)

	rf.retrieveCalls = nil
	_, err = svc.RuntimeSearch(context.Background(), testRuntimeToken, "退款政策", 500)
	require.NoError(t, err)
	require.Len(t, rf.retrieveCalls, 2)
	assert.Equal(t, int32(50), rf.retrieveCalls[0].topK)
}

// TestRuntimeSearchAppResultsFirst 验证 runtime 检索结果先返回实例知识库命中，再返回组织知识库命中。
func TestRuntimeSearchAppResultsFirst(t *testing.T) {
	svc, _, rf := newRAGFlowKnowledgeTestService(t)
	rf.retrieveChunksByDataset = map[string][]ragflow.RetrievalChunk{
		testRemoteAppDatasetID: {
			{DocumentID: "app-low", DocumentName: "app-low.md", DatasetID: testRemoteAppDatasetID, Content: "app low", Similarity: 0.2},
			{DocumentID: "app-high", DocumentName: "app-high.md", DatasetID: testRemoteAppDatasetID, Content: "app high", Similarity: 0.8},
		},
		testRemoteOrgDatasetID: {
			{DocumentID: "org-doc", DocumentName: "org.md", DatasetID: testRemoteOrgDatasetID, Content: "org", Similarity: 0.99},
		},
	}

	result, err := svc.RuntimeSearch(context.Background(), testRuntimeToken, "退款政策", 8)
	require.NoError(t, err)

	require.Len(t, result.Results, 3)
	assert.Equal(t, []string{"app", "app", "org"}, []string{result.Results[0].Scope, result.Results[1].Scope, result.Results[2].Scope})
	assert.Equal(t, "app-high", result.Results[0].DocumentID)
	assert.Equal(t, "app-low", result.Results[1].DocumentID)
	assert.Equal(t, "org-doc", result.Results[2].DocumentID)
}

// TestRAGFlowKnowledgeListReturnsCachedStatus 验证列表请求只读取本地缓存，不向 RAGFlow 拉取最新解析状态。
// 解析状态推进交由独立的后台任务 RagflowParseStatusRefresher，列表此处只反映 DB 现状，
// 避免每次拉列表都打 RAGFlow，也避免 RAGFlow 临时不可用让列表 5xx。
func TestRAGFlowKnowledgeListReturnsCachedStatus(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	doc := testDocument(t, "org", "policy.md", store.orgDataset.ID)
	doc.ParseStatus = "running"
	doc.Progress = 0
	store.docs[testKnowledgeDocument] = doc
	// 即使远端已经完成，列表仍应返回本地 running 状态，由后台任务负责后续推进。
	rf.listDocuments = []ragflow.Document{{ID: "remote-doc-1", Name: "policy.md", Run: "DONE"}}
	rf.listDocumentsCalls = 0

	result, err := svc.ListOrg(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, 1, 50, "", "")
	require.NoError(t, err)

	require.Len(t, result.Items, 1)
	assert.Equal(t, "running", result.Items[0].ParseStatus)
	assert.Equal(t, int32(0), result.Items[0].Progress)
	assert.Equal(t, "running", store.docs[testKnowledgeDocument].ParseStatus)
	// 关键断言：列表流程不应触发 RAGFlow ListDocuments。
	assert.Zero(t, rf.listDocumentsCalls, "列表请求不应调 RAGFlow ListDocuments")
}

// TestNormalizeRAGFlowRunAcceptsNumericAndObservedValues 验证 RAGFlow 文档解析状态兼容官方数字枚举
// 以及实际接口可能返回的小写状态，避免失败文档被误判为运行中。
func TestNormalizeRAGFlowRunAcceptsNumericAndObservedValues(t *testing.T) {
	// 数字 0 表示尚未处理，应展示为排队状态。
	assert.Equal(t, "queued", normalizeRAGFlowRun("0"))
	// 数字 1 表示处理中，应继续触发页面轮询。
	assert.Equal(t, "running", normalizeRAGFlowRun("1"))
	// 数字 2 表示取消，应允许用户重新解析。
	assert.Equal(t, "stopped", normalizeRAGFlowRun("2"))
	// 数字 3 表示完成，应展示 100% 解析进度。
	assert.Equal(t, "completed", normalizeRAGFlowRun("3"))
	// 数字 4 表示失败，应展示重解析入口。
	assert.Equal(t, "failed", normalizeRAGFlowRun("4"))
	// failed 是线上接口曾出现的小写失败值，也必须归一到失败。
	assert.Equal(t, "failed", normalizeRAGFlowRun("failed"))
	// cancel 是线上接口曾出现的小写停止值，也必须归一到已停止。
	assert.Equal(t, "stopped", normalizeRAGFlowRun("cancel"))
}

// TestRAGFlowKnowledgeReparseOnlyFailedOrStopped 验证只有解析失败或已停止的文件允许重新解析，避免重复排队正常文档。
func TestRAGFlowKnowledgeReparseOnlyFailedOrStopped(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.docs[testKnowledgeDocument] = testDocument(t, "org", "policy.md", store.orgDataset.ID)

	_, err := svc.ReparseOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, testKnowledgeDocument)
	require.Error(t, err)
	assert.Empty(t, rf.parseCalls)

	doc := store.docs[testKnowledgeDocument]
	doc.ParseStatus = "failed"
	store.docs[testKnowledgeDocument] = doc
	_, err = svc.ReparseOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, testKnowledgeDocument)
	require.NoError(t, err)
	require.Len(t, rf.parseCalls, 1)
}

// TestRuntimeAddWritesOnlyCurrentAppDataset 验证 Hermes 写入报告只能落到当前实例 dataset，不会加载组织 dataset。
func TestRuntimeAddWritesOnlyCurrentAppDataset(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)

	doc, err := svc.RuntimeAddFile(context.Background(), testRuntimeToken, "research.md", strings.NewReader("report"), 6)
	require.NoError(t, err)

	assert.Equal(t, "research.md", doc.Name)
	require.Len(t, rf.uploadCalls, 1)
	assert.Equal(t, testRemoteAppDatasetID, rf.uploadCalls[0].datasetID)
	assert.Equal(t, 0, store.getOrgDatasetCalls)
	require.Len(t, store.createdDocs, 1)
	assert.Equal(t, "runtime:"+testKnowledgeApp, store.createdDocs[0].CreatedBy)
	assert.Equal(t, "app", store.createdDocs[0].ScopeType)
}

// TestDeleteAppDatasetRemovesRemoteAndLocalMapping 验证删除实例时会同步清理 RAGFlow app dataset，
// 防止软删应用后远端文件和索引长期成为不可见孤儿数据。
func TestDeleteAppDatasetRemovesRemoteAndLocalMapping(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)

	err := svc.DeleteAppDataset(context.Background(), mustParseUUID(testKnowledgeApp))
	require.NoError(t, err)

	require.Len(t, rf.deleteDatasetCalls, 1)
	assert.Equal(t, []string{testRemoteAppDatasetID}, rf.deleteDatasetCalls[0])
	assert.Equal(t, uuidToString(store.appDataset.ID), store.deletedDatasetID)
}

// TestEnsureOrgDatasetCreatesRemoteDatasetMapping 验证组织知识库没有映射时会自动创建 RAGFlow dataset 并回写远端 ID。
func TestEnsureOrgDatasetCreatesRemoteDatasetMapping(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	svc.SetDatasetChunkMethod("manual")
	store.missingOrgDataset = true
	rf.createDatasetResult = ragflow.Dataset{ID: "new-org-ds", Name: "remote-org-name"}

	dataset, err := svc.EnsureOrgDataset(context.Background(), sqlc.Organization{
		ID:   mustParseUUID(testKnowledgeOrg),
		Name: "测试组织",
		Code: "test-org",
	})
	require.NoError(t, err)

	assert.Equal(t, "new-org-ds", dataset.RagflowDatasetID.String)
	require.Len(t, rf.createDatasetCalls, 1)
	assert.Equal(t, "manual", rf.createDatasetCalls[0].chunkMethod)
	require.Len(t, store.createdDatasets, 1)
	assert.Equal(t, "creating", store.createdDatasets[0].Status)
	require.Len(t, store.activatedDatasets, 1)
	assert.Equal(t, "remote-org-name", store.activatedDatasets[0].Name)
}

// TestRuntimeAddRecreatesFailedAppDataset 验证实例 dataset 创建失败后，runtime 写入会自动重试创建再上传文件。
func TestRuntimeAddRecreatesFailedAppDataset(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.appDataset.Status = "failed"
	store.appDataset.RagflowDatasetID = pgtype.Text{}
	rf.createDatasetResult = ragflow.Dataset{ID: "new-app-ds", Name: "remote-app-name"}

	doc, err := svc.RuntimeAddFile(context.Background(), testRuntimeToken, "research.md", strings.NewReader("report"), 6)
	require.NoError(t, err)

	assert.Equal(t, "research.md", doc.Name)
	require.Len(t, rf.createDatasetCalls, 1)
	require.Len(t, store.activatedDatasets, 1)
	require.Len(t, rf.uploadCalls, 1)
	assert.Equal(t, "new-app-ds", rf.uploadCalls[0].datasetID)
}

// TestEnsureOrgDatasetCreateConflictDoesNotDuplicateRemoteCreate 验证并发首创 dataset 映射失败后只读取已有 creating 行，不重复创建远端 RAGFlow dataset。
func TestEnsureOrgDatasetCreateConflictDoesNotDuplicateRemoteCreate(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.missingOrgDatasetOnce = true
	store.createOrgDatasetErr = pgx.ErrNoRows
	store.orgDataset.Status = "creating"
	store.orgDataset.RagflowDatasetID = pgtype.Text{}

	_, err := svc.EnsureOrgDataset(context.Background(), store.org)
	require.ErrorIs(t, err, ErrKnowledgeDatasetCreating)

	assert.Equal(t, 0, len(rf.createDatasetCalls))
	assert.Equal(t, 2, store.getOrgDatasetCalls)
}

// TestEnsureOrgDatasetFailedClaimLostDoesNotCreateRemote 验证 failed 映射被其它 worker 抢占后，当前调用不重复创建远端 dataset。
func TestEnsureOrgDatasetFailedClaimLostDoesNotCreateRemote(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.orgDataset.Status = "failed"
	store.orgDataset.RagflowDatasetID = pgtype.Text{}
	store.claimDatasetErr = pgx.ErrNoRows

	_, err := svc.EnsureOrgDataset(context.Background(), store.org)
	require.ErrorIs(t, err, ErrKnowledgeDatasetCreating)

	assert.Equal(t, 1, len(store.claimedDatasets))
	assert.Equal(t, 0, len(rf.createDatasetCalls))
}

// TestEnsureOrgDatasetFailedClaimWinnerCreatesRemote 验证 failed 映射只有抢占租约成功者会创建远端 dataset 并携带同一 token 回写。
func TestEnsureOrgDatasetFailedClaimWinnerCreatesRemote(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.orgDataset.Status = "failed"
	store.orgDataset.RagflowDatasetID = pgtype.Text{}
	rf.createDatasetResult = ragflow.Dataset{ID: "retry-org-ds", Name: "retry-org"}

	dataset, err := svc.EnsureOrgDataset(context.Background(), store.org)
	require.NoError(t, err)

	assert.Equal(t, "retry-org-ds", dataset.RagflowDatasetID.String)
	require.Len(t, store.claimedDatasets, 1)
	require.Len(t, store.activatedDatasets, 1)
	assert.Equal(t, store.claimedDatasets[0].CreateClaimToken, store.activatedDatasets[0].CreateClaimToken.String)
	assert.Equal(t, 1, len(rf.createDatasetCalls))
}

// TestEnsureOrgDatasetReclaimsStaleCreating 验证进程崩溃留下的过期 creating 映射可以被重新抢占并继续创建。
func TestEnsureOrgDatasetReclaimsStaleCreating(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.orgDataset.Status = "creating"
	store.orgDataset.RagflowDatasetID = pgtype.Text{}
	store.orgDataset.UpdatedAt = pgtype.Timestamptz{Time: time.Now().Add(-ragflowDatasetCreateClaimTimeout - time.Minute), Valid: true}
	rf.createDatasetResult = ragflow.Dataset{ID: "reclaimed-org-ds", Name: "reclaimed-org"}

	dataset, err := svc.EnsureOrgDataset(context.Background(), store.org)
	require.NoError(t, err)

	assert.Equal(t, "reclaimed-org-ds", dataset.RagflowDatasetID.String)
	assert.Equal(t, 1, len(store.claimedDatasets))
	assert.Equal(t, 1, len(rf.createDatasetCalls))
}

// TestEnsureOrgDatasetLostClaimDoesNotOverwriteWinner 验证远端创建返回后若本地租约已丢失，不覆盖获胜者状态并清理本次创建出的远端 dataset。
func TestEnsureOrgDatasetLostClaimDoesNotOverwriteWinner(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.missingOrgDataset = true
	store.setActiveErr = pgx.ErrNoRows
	rf.createDatasetResult = ragflow.Dataset{ID: "loser-org-ds", Name: "loser-org"}

	_, err := svc.EnsureOrgDataset(context.Background(), store.org)
	require.ErrorIs(t, err, ErrKnowledgeDatasetCreating)

	assert.Equal(t, 1, len(rf.createDatasetCalls))
	require.Len(t, rf.deleteDatasetCalls, 1)
	assert.Equal(t, []string{"loser-org-ds"}, rf.deleteDatasetCalls[0])
}

func newRAGFlowKnowledgeTestService(t *testing.T) (*KnowledgeService, *fakeKnowledgeStore, *fakeRAGFlowKnowledgeClient) {
	t.Helper()
	store := newFakeKnowledgeStore(t)
	rf := &fakeRAGFlowKnowledgeClient{
		uploadDocument: ragflow.Document{ID: "remote-doc-1", Name: "report.md", Size: 8, Run: "UNSTART"},
		createDatasetResult: ragflow.Dataset{
			ID:   "created-ds",
			Name: "created-dataset",
		},
	}
	return NewKnowledgeService(store, rf), store, rf
}

func newFakeKnowledgeStore(t *testing.T) *fakeKnowledgeStore {
	t.Helper()
	orgID := mustParseUUID(testKnowledgeOrg)
	appID := mustParseUUID(testKnowledgeApp)
	ownerID := mustParseUUID(testKnowledgeOwner)
	app := sqlc.App{
		ID:          appID,
		OrgID:       orgID,
		OwnerUserID: ownerID,
		Name:        "实例",
		Status:      domain.AppStatusRunning,
		RuntimeTokenHash: pgtype.Text{
			String: HashAppRuntimeToken(testRuntimeToken),
			Valid:  true,
		},
	}
	org := sqlc.Organization{
		ID:     orgID,
		Name:   "测试组织",
		Code:   "test-org",
		Status: domain.StatusActive,
	}
	orgDataset := sqlc.RagflowDataset{
		ID:               mustParseUUID("00000000-0000-0000-0000-000000000d01"),
		ScopeType:        "org",
		OrgID:            orgID,
		RagflowDatasetID: pgtype.Text{String: testRemoteOrgDatasetID, Valid: true},
		Name:             "oc-org",
		Status:           "active",
	}
	appDataset := sqlc.RagflowDataset{
		ID:               mustParseUUID("00000000-0000-0000-0000-000000000d02"),
		ScopeType:        "app",
		OrgID:            orgID,
		AppID:            appID,
		RagflowDatasetID: pgtype.Text{String: testRemoteAppDatasetID, Valid: true},
		Name:             "oc-app",
		Status:           "active",
	}
	return &fakeKnowledgeStore{
		apps:         map[string]sqlc.App{testKnowledgeApp: app},
		appsByToken:  map[string]sqlc.App{HashAppRuntimeToken(testRuntimeToken): app, testRuntimeTokenHash: app},
		org:          org,
		orgDataset:   orgDataset,
		appDataset:   appDataset,
		docs:         map[string]sqlc.RagflowDocument{},
		nextDocument: "00000000-0000-0000-0000-000000000e06",
		now:          time.Date(2026, 5, 26, 8, 0, 0, 0, time.UTC),
	}
}

type fakeKnowledgeStore struct {
	apps                  map[string]sqlc.App
	appsByToken           map[string]sqlc.App
	org                   sqlc.Organization
	orgDataset            sqlc.RagflowDataset
	appDataset            sqlc.RagflowDataset
	missingOrgDataset     bool
	missingAppDataset     bool
	missingOrgDatasetOnce bool
	missingAppDatasetOnce bool
	createOrgDatasetErr   error
	createAppDatasetErr   error
	claimDatasetErr       error
	setActiveErr          error
	docs                  map[string]sqlc.RagflowDocument
	createdDatasets       []createdDatasetCall
	claimedDatasets       []sqlc.ClaimRAGFlowDatasetCreationParams
	activatedDatasets     []sqlc.SetRAGFlowDatasetActiveParams
	failedDatasets        []sqlc.MarkRAGFlowDatasetFailedParams
	createdDocs           []sqlc.CreateRAGFlowDocumentParams
	deletedDatasetID      string
	getOrgDatasetCalls    int
	nextDocument          string
	now                   time.Time
}

type createdDatasetCall struct {
	ScopeType        string
	OrgID            pgtype.UUID
	AppID            pgtype.UUID
	RagflowDatasetID pgtype.Text
	Name             string
	Status           string
	LastError        pgtype.Text
	CreateClaimToken string
}

func (s *fakeKnowledgeStore) GetApp(_ context.Context, id pgtype.UUID) (sqlc.App, error) {
	app, ok := s.apps[uuidToString(id)]
	if !ok {
		return sqlc.App{}, pgx.ErrNoRows
	}
	return app, nil
}

func (s *fakeKnowledgeStore) GetOrganization(_ context.Context, id pgtype.UUID) (sqlc.Organization, error) {
	if uuidToString(id) != testKnowledgeOrg {
		return sqlc.Organization{}, pgx.ErrNoRows
	}
	return s.org, nil
}

func (s *fakeKnowledgeStore) GetAppByRuntimeTokenHash(_ context.Context, runtimeTokenHash pgtype.Text) (sqlc.App, error) {
	app, ok := s.appsByToken[runtimeTokenHash.String]
	if !ok {
		return sqlc.App{}, pgx.ErrNoRows
	}
	return app, nil
}

func (s *fakeKnowledgeStore) GetRAGFlowOrgDataset(_ context.Context, orgID pgtype.UUID) (sqlc.RagflowDataset, error) {
	s.getOrgDatasetCalls++
	if s.missingOrgDatasetOnce && s.getOrgDatasetCalls == 1 {
		return sqlc.RagflowDataset{}, pgx.ErrNoRows
	}
	if s.missingOrgDataset {
		return sqlc.RagflowDataset{}, pgx.ErrNoRows
	}
	if uuidToString(orgID) != testKnowledgeOrg {
		return sqlc.RagflowDataset{}, pgx.ErrNoRows
	}
	return s.orgDataset, nil
}

func (s *fakeKnowledgeStore) GetRAGFlowAppDataset(_ context.Context, appID pgtype.UUID) (sqlc.RagflowDataset, error) {
	if s.missingAppDatasetOnce {
		s.missingAppDatasetOnce = false
		return sqlc.RagflowDataset{}, pgx.ErrNoRows
	}
	if s.missingAppDataset {
		return sqlc.RagflowDataset{}, pgx.ErrNoRows
	}
	if uuidToString(appID) != testKnowledgeApp {
		return sqlc.RagflowDataset{}, pgx.ErrNoRows
	}
	return s.appDataset, nil
}

func (s *fakeKnowledgeStore) CreateRAGFlowOrgDatasetMapping(_ context.Context, arg sqlc.CreateRAGFlowOrgDatasetMappingParams) (sqlc.RagflowDataset, error) {
	if s.createOrgDatasetErr != nil {
		return sqlc.RagflowDataset{}, s.createOrgDatasetErr
	}
	call := createdDatasetCall{
		ScopeType:        "org",
		OrgID:            arg.OrgID,
		Name:             arg.Name,
		Status:           "creating",
		CreateClaimToken: arg.CreateClaimToken,
	}
	s.createdDatasets = append(s.createdDatasets, call)
	row := sqlc.RagflowDataset{
		ID:               mustParseUUID("00000000-0000-0000-0000-000000000d03"),
		ScopeType:        call.ScopeType,
		OrgID:            call.OrgID,
		AppID:            call.AppID,
		RagflowDatasetID: call.RagflowDatasetID,
		Name:             call.Name,
		Status:           call.Status,
		LastError:        call.LastError,
		CreateClaimToken: pgtype.Text{String: call.CreateClaimToken, Valid: true},
	}
	s.orgDataset = row
	s.missingOrgDataset = false
	return row, nil
}

func (s *fakeKnowledgeStore) CreateRAGFlowAppDatasetMapping(_ context.Context, arg sqlc.CreateRAGFlowAppDatasetMappingParams) (sqlc.RagflowDataset, error) {
	if s.createAppDatasetErr != nil {
		return sqlc.RagflowDataset{}, s.createAppDatasetErr
	}
	call := createdDatasetCall{
		ScopeType:        "app",
		OrgID:            arg.OrgID,
		AppID:            arg.AppID,
		Name:             arg.Name,
		Status:           "creating",
		CreateClaimToken: arg.CreateClaimToken,
	}
	s.createdDatasets = append(s.createdDatasets, call)
	row := sqlc.RagflowDataset{
		ID:               mustParseUUID("00000000-0000-0000-0000-000000000d03"),
		ScopeType:        call.ScopeType,
		OrgID:            call.OrgID,
		AppID:            call.AppID,
		RagflowDatasetID: call.RagflowDatasetID,
		Name:             call.Name,
		Status:           call.Status,
		LastError:        call.LastError,
		CreateClaimToken: pgtype.Text{String: call.CreateClaimToken, Valid: true},
	}
	s.appDataset = row
	s.missingAppDataset = false
	return row, nil
}

func (s *fakeKnowledgeStore) ClaimRAGFlowDatasetCreation(_ context.Context, arg sqlc.ClaimRAGFlowDatasetCreationParams) (sqlc.RagflowDataset, error) {
	s.claimedDatasets = append(s.claimedDatasets, arg)
	if s.claimDatasetErr != nil {
		return sqlc.RagflowDataset{}, s.claimDatasetErr
	}
	staleBefore := arg.StaleBefore.Time
	if uuidToString(s.orgDataset.ID) == uuidToString(arg.ID) {
		if s.orgDataset.Status == "failed" || (s.orgDataset.Status == "creating" && s.orgDataset.UpdatedAt.Valid && s.orgDataset.UpdatedAt.Time.Before(staleBefore)) {
			s.orgDataset.Status = "creating"
			s.orgDataset.LastError = pgtype.Text{}
			s.orgDataset.CreateClaimToken = pgtype.Text{String: arg.CreateClaimToken, Valid: true}
			s.orgDataset.UpdatedAt = pgtype.Timestamptz{Time: s.now, Valid: true}
			return s.orgDataset, nil
		}
		return sqlc.RagflowDataset{}, pgx.ErrNoRows
	}
	if s.appDataset.Status == "failed" || (s.appDataset.Status == "creating" && s.appDataset.UpdatedAt.Valid && s.appDataset.UpdatedAt.Time.Before(staleBefore)) {
		s.appDataset.Status = "creating"
		s.appDataset.LastError = pgtype.Text{}
		s.appDataset.CreateClaimToken = pgtype.Text{String: arg.CreateClaimToken, Valid: true}
		s.appDataset.UpdatedAt = pgtype.Timestamptz{Time: s.now, Valid: true}
		return s.appDataset, nil
	}
	return sqlc.RagflowDataset{}, pgx.ErrNoRows
}

func (s *fakeKnowledgeStore) SetRAGFlowDatasetActive(_ context.Context, arg sqlc.SetRAGFlowDatasetActiveParams) (sqlc.RagflowDataset, error) {
	s.activatedDatasets = append(s.activatedDatasets, arg)
	if s.setActiveErr != nil {
		return sqlc.RagflowDataset{}, s.setActiveErr
	}
	if uuidToString(s.orgDataset.ID) == uuidToString(arg.ID) {
		if s.orgDataset.CreateClaimToken.String != arg.CreateClaimToken.String {
			return sqlc.RagflowDataset{}, pgx.ErrNoRows
		}
		s.orgDataset.RagflowDatasetID = arg.RagflowDatasetID
		s.orgDataset.Name = arg.Name
		s.orgDataset.Status = "active"
		s.orgDataset.CreateClaimToken = pgtype.Text{}
		return s.orgDataset, nil
	}
	if s.appDataset.CreateClaimToken.String != arg.CreateClaimToken.String {
		return sqlc.RagflowDataset{}, pgx.ErrNoRows
	}
	s.appDataset.RagflowDatasetID = arg.RagflowDatasetID
	s.appDataset.Name = arg.Name
	s.appDataset.Status = "active"
	s.appDataset.CreateClaimToken = pgtype.Text{}
	return s.appDataset, nil
}

func (s *fakeKnowledgeStore) MarkRAGFlowDatasetFailed(_ context.Context, arg sqlc.MarkRAGFlowDatasetFailedParams) (sqlc.RagflowDataset, error) {
	s.failedDatasets = append(s.failedDatasets, arg)
	if uuidToString(s.orgDataset.ID) == uuidToString(arg.ID) {
		if s.orgDataset.CreateClaimToken.String != arg.CreateClaimToken.String {
			return sqlc.RagflowDataset{}, pgx.ErrNoRows
		}
		s.orgDataset.Status = "failed"
		s.orgDataset.LastError = arg.LastError
		s.orgDataset.CreateClaimToken = pgtype.Text{}
		return s.orgDataset, nil
	}
	if s.appDataset.CreateClaimToken.String != arg.CreateClaimToken.String {
		return sqlc.RagflowDataset{}, pgx.ErrNoRows
	}
	s.appDataset.Status = "failed"
	s.appDataset.LastError = arg.LastError
	s.appDataset.CreateClaimToken = pgtype.Text{}
	return s.appDataset, nil
}

func (s *fakeKnowledgeStore) CreateRAGFlowDocument(_ context.Context, arg sqlc.CreateRAGFlowDocumentParams) (sqlc.RagflowDocument, error) {
	s.createdDocs = append(s.createdDocs, arg)
	id := mustParseUUIDFromString(arg.RagflowDocumentID)
	if arg.RagflowDocumentID == "remote-doc-1" {
		id = mustParseUUIDFromString(s.nextDocument)
	}
	row := sqlc.RagflowDocument{
		ID:                id,
		DatasetID:         arg.DatasetID,
		ScopeType:         arg.ScopeType,
		OrgID:             arg.OrgID,
		AppID:             arg.AppID,
		RagflowDocumentID: arg.RagflowDocumentID,
		Name:              arg.Name,
		SizeBytes:         arg.SizeBytes,
		MimeType:          arg.MimeType,
		Suffix:            arg.Suffix,
		ParseStatus:       arg.ParseStatus,
		Progress:          arg.Progress,
		LastError:         arg.LastError,
		CreatedBy:         arg.CreatedBy,
		CreatedAt:         pgtype.Timestamptz{Time: s.now, Valid: true},
		UpdatedAt:         pgtype.Timestamptz{Time: s.now, Valid: true},
	}
	s.docs[uuidToString(row.ID)] = row
	return row, nil
}

func (s *fakeKnowledgeStore) ListRAGFlowDocumentsByScope(_ context.Context, arg sqlc.ListRAGFlowDocumentsByScopeParams) ([]sqlc.RagflowDocument, error) {
	items := make([]sqlc.RagflowDocument, 0, len(s.docs))
	for _, doc := range s.docs {
		if doc.ScopeType != arg.ScopeType || uuidToString(doc.OrgID) != uuidToString(arg.OrgID) {
			continue
		}
		if arg.AppID.Valid && uuidToString(doc.AppID) != uuidToString(arg.AppID) {
			continue
		}
		items = append(items, doc)
	}
	return items, nil
}

func (s *fakeKnowledgeStore) CountRAGFlowDocumentsByScope(ctx context.Context, arg sqlc.CountRAGFlowDocumentsByScopeParams) (int64, error) {
	items, err := s.ListRAGFlowDocumentsByScope(ctx, sqlc.ListRAGFlowDocumentsByScopeParams{
		ScopeType: arg.ScopeType,
		OrgID:     arg.OrgID,
		AppID:     arg.AppID,
	})
	return int64(len(items)), err
}

func (s *fakeKnowledgeStore) GetRAGFlowDocument(_ context.Context, id pgtype.UUID) (sqlc.RagflowDocument, error) {
	doc, ok := s.docs[uuidToString(id)]
	if !ok {
		return sqlc.RagflowDocument{}, pgx.ErrNoRows
	}
	return doc, nil
}

func (s *fakeKnowledgeStore) UpdateRAGFlowDocumentParseStatus(_ context.Context, arg sqlc.UpdateRAGFlowDocumentParseStatusParams) (sqlc.RagflowDocument, error) {
	doc, ok := s.docs[uuidToString(arg.ID)]
	if !ok {
		return sqlc.RagflowDocument{}, pgx.ErrNoRows
	}
	doc.ParseStatus = arg.ParseStatus
	doc.Progress = arg.Progress
	doc.LastError = arg.LastError
	s.docs[uuidToString(arg.ID)] = doc
	return doc, nil
}

func (s *fakeKnowledgeStore) DeleteRAGFlowDocumentMapping(_ context.Context, id pgtype.UUID) error {
	delete(s.docs, uuidToString(id))
	return nil
}

func (s *fakeKnowledgeStore) DeleteRAGFlowDatasetMapping(_ context.Context, id pgtype.UUID) error {
	s.deletedDatasetID = uuidToString(id)
	return nil
}

type fakeRAGFlowKnowledgeClient struct {
	createDatasetResult     ragflow.Dataset
	createDatasetCalls      []ragflowCreateDatasetCall
	deleteDatasetCalls      [][]string
	uploadDocument          ragflow.Document
	uploadCalls             []ragflowUploadCall
	parseCalls              []ragflowParseCall
	deleteCalls             []ragflowDeleteCall
	retrieveDatasetIDs      []string
	retrieveQuestion        string
	retrieveTopK            int32
	retrieveChunks          []ragflow.RetrievalChunk
	retrieveChunksByDataset map[string][]ragflow.RetrievalChunk
	retrieveCalls           []ragflowRetrieveCall
	listDocuments           []ragflow.Document
	listDocumentsCalls      int
}

type ragflowCreateDatasetCall struct {
	name        string
	chunkMethod string
}

type ragflowUploadCall struct {
	datasetID string
	filename  string
	body      string
}

type ragflowParseCall struct {
	datasetID   string
	documentIDs []string
}

type ragflowDeleteCall struct {
	datasetID   string
	documentIDs []string
}

type ragflowRetrieveCall struct {
	datasetIDs []string
	question   string
	topK       int32
}

func (f *fakeRAGFlowKnowledgeClient) CreateDataset(_ context.Context, name, chunkMethod string) (ragflow.Dataset, error) {
	f.createDatasetCalls = append(f.createDatasetCalls, ragflowCreateDatasetCall{name: name, chunkMethod: chunkMethod})
	if f.createDatasetResult.ID == "" {
		return ragflow.Dataset{ID: "created-ds", Name: name}, nil
	}
	return f.createDatasetResult, nil
}

func (f *fakeRAGFlowKnowledgeClient) DeleteDatasets(_ context.Context, ids []string) error {
	f.deleteDatasetCalls = append(f.deleteDatasetCalls, append([]string(nil), ids...))
	return nil
}

func (f *fakeRAGFlowKnowledgeClient) UploadDocument(_ context.Context, datasetID, filename string, body io.Reader) (ragflow.Document, error) {
	content, _ := io.ReadAll(body)
	f.uploadCalls = append(f.uploadCalls, ragflowUploadCall{datasetID: datasetID, filename: filename, body: string(content)})
	doc := f.uploadDocument
	doc.Name = filename
	return doc, nil
}

func (f *fakeRAGFlowKnowledgeClient) DownloadDocument(_ context.Context, _, _ string) (io.ReadCloser, int64, error) {
	return io.NopCloser(strings.NewReader("content")), 7, nil
}

func (f *fakeRAGFlowKnowledgeClient) DeleteDocuments(_ context.Context, datasetID string, documentIDs []string) error {
	f.deleteCalls = append(f.deleteCalls, ragflowDeleteCall{datasetID: datasetID, documentIDs: append([]string(nil), documentIDs...)})
	return nil
}

func (f *fakeRAGFlowKnowledgeClient) ParseDocuments(_ context.Context, datasetID string, documentIDs []string) error {
	f.parseCalls = append(f.parseCalls, ragflowParseCall{datasetID: datasetID, documentIDs: append([]string(nil), documentIDs...)})
	return nil
}

func (f *fakeRAGFlowKnowledgeClient) ListDocuments(context.Context, string, int32, int32, string, string) ([]ragflow.Document, int32, error) {
	f.listDocumentsCalls++
	return f.listDocuments, int32(len(f.listDocuments)), nil
}

func (f *fakeRAGFlowKnowledgeClient) Retrieve(_ context.Context, datasetIDs []string, question string, topK int32) ([]ragflow.RetrievalChunk, error) {
	f.retrieveDatasetIDs = append([]string(nil), datasetIDs...)
	f.retrieveQuestion = question
	f.retrieveTopK = topK
	f.retrieveCalls = append(f.retrieveCalls, ragflowRetrieveCall{datasetIDs: append([]string(nil), datasetIDs...), question: question, topK: topK})
	if len(datasetIDs) == 1 && len(f.retrieveChunksByDataset) > 0 {
		return f.retrieveChunksByDataset[datasetIDs[0]], nil
	}
	return f.retrieveChunks, nil
}

func testDocument(t *testing.T, scope, name string, datasetID pgtype.UUID) sqlc.RagflowDocument {
	t.Helper()
	orgID := mustParseUUID(testKnowledgeOrg)
	appID := pgtype.UUID{}
	if scope == "app" {
		appID = mustParseUUID(testKnowledgeApp)
	}
	return sqlc.RagflowDocument{
		ID:                mustParseUUID(testKnowledgeDocument),
		DatasetID:         datasetID,
		ScopeType:         scope,
		OrgID:             orgID,
		AppID:             appID,
		RagflowDocumentID: "remote-doc-1",
		Name:              name,
		SizeBytes:         12,
		ParseStatus:       "completed",
		Progress:          100,
		CreatedBy:         testKnowledgeOwner,
		CreatedAt:         pgtype.Timestamptz{Time: time.Date(2026, 5, 26, 8, 0, 0, 0, time.UTC), Valid: true},
		UpdatedAt:         pgtype.Timestamptz{Time: time.Date(2026, 5, 26, 8, 0, 0, 0, time.UTC), Valid: true},
	}
}

func orgKnowledgeAdmin() auth.Principal {
	return auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testKnowledgeOrg, UserID: "admin"}
}

func appOwnerPrincipal() auth.Principal {
	return auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}
}

func mustParseUUIDFromString(value string) pgtype.UUID {
	id, _ := parseUUID(value)
	return id
}
