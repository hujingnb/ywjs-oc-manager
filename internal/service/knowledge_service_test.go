// Package service 的 knowledge_service_test 覆盖 RAGFlow-backed 知识库的 manager 权限边界。
package service

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	null "github.com/guregu/null/v5"
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

// TestRAGFlowKnowledgeListOrgUsesManagerPermission 验证企业知识库读取只由 manager principal 判权，RAGFlow 不参与授权。
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

// TestRuntimeSearchNormalizesTopK 验证 runtime 检索在调用 RAGFlow 前应用 manager 默认值和上限。
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

// TestRuntimeSearchAppResultsFirst 验证 runtime 检索结果先返回实例知识库命中，再返回企业知识库命中。
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

// TestRuntimeSearchOrgRetrieveErrorUsesEnterpriseCopy 验证 runtime 检索企业知识库失败时返回企业文案。
func TestRuntimeSearchOrgRetrieveErrorUsesEnterpriseCopy(t *testing.T) {
	svc, _, rf := newRAGFlowKnowledgeTestService(t)
	rf.retrieveErrorsByDataset = map[string]error{
		testRemoteOrgDatasetID: errors.New("ragflow unavailable"),
	}

	_, err := svc.RuntimeSearch(context.Background(), testRuntimeToken, "退款政策", 8)
	require.ErrorContains(t, err, "RAGFlow 检索企业知识库失败")
}

// TestRAGFlowKnowledgeListReturnsCachedStatus 验证列表请求只读取本地缓存，不向 RAGFlow 拉取最新解析状态。
func TestRAGFlowKnowledgeListReturnsCachedStatus(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	doc := testDocument(t, "org", "policy.md", store.orgDataset.ID)
	doc.ParseStatus = "running"
	doc.Progress = 0
	store.docs[testKnowledgeDocument] = doc
	rf.listDocuments = []ragflow.Document{{ID: "remote-doc-1", Name: "policy.md", Run: "DONE"}}
	rf.listDocumentsCalls = 0

	result, err := svc.ListOrg(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, 1, 50, "", "")
	require.NoError(t, err)

	require.Len(t, result.Items, 1)
	assert.Equal(t, "running", result.Items[0].ParseStatus)
	assert.Equal(t, int32(0), result.Items[0].Progress)
	assert.Equal(t, "running", store.docs[testKnowledgeDocument].ParseStatus)
	assert.Zero(t, rf.listDocumentsCalls, "列表请求不应调 RAGFlow ListDocuments")
}

// TestNormalizeRAGFlowRunAcceptsNumericAndObservedValues 验证 RAGFlow 文档解析状态兼容官方数字枚举和实际接口可能返回的小写状态。
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

// TestRAGFlowKnowledgeReparseOnlyFailedOrStopped 验证只有解析失败或已停止的文件允许重新解析。
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

// TestDeleteAppDatasetRemovesRemoteAndLocalMapping 验证删除实例时会同步清理 RAGFlow app dataset。
func TestDeleteAppDatasetRemovesRemoteAndLocalMapping(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)

	// mustParseUUID 现在返回 string，与 DeleteAppDataset(string) 签名一致。
	err := svc.DeleteAppDataset(context.Background(), mustParseUUID(testKnowledgeApp))
	require.NoError(t, err)

	require.Len(t, rf.deleteDatasetCalls, 1)
	assert.Equal(t, []string{testRemoteAppDatasetID}, rf.deleteDatasetCalls[0])
	assert.Equal(t, store.appDataset.ID, store.deletedDatasetID)
}

// TestEnsureOrgDatasetCreatesRemoteDatasetMapping 验证企业知识库没有映射时会自动创建 RAGFlow dataset 并回写远端 ID。
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
	store.appDataset.RagflowDatasetID = null.String{}
	rf.createDatasetResult = ragflow.Dataset{ID: "new-app-ds", Name: "remote-app-name"}

	doc, err := svc.RuntimeAddFile(context.Background(), testRuntimeToken, "research.md", strings.NewReader("report"), 6)
	require.NoError(t, err)

	assert.Equal(t, "research.md", doc.Name)
	require.Len(t, rf.createDatasetCalls, 1)
	require.Len(t, store.activatedDatasets, 1)
	require.Len(t, rf.uploadCalls, 1)
	assert.Equal(t, "new-app-ds", rf.uploadCalls[0].datasetID)
}

// TestEnsureOrgDatasetCreateConflictDoesNotDuplicateRemoteCreate 验证并发首创 dataset 映射失败后只读取已有 creating 行。
func TestEnsureOrgDatasetCreateConflictDoesNotDuplicateRemoteCreate(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.missingOrgDatasetOnce = true
	store.createOrgDatasetErr = sql.ErrNoRows
	store.orgDataset.Status = "creating"
	store.orgDataset.RagflowDatasetID = null.String{}

	_, err := svc.EnsureOrgDataset(context.Background(), store.org)
	require.ErrorIs(t, err, ErrKnowledgeDatasetCreating)

	assert.Equal(t, 0, len(rf.createDatasetCalls))
	assert.Equal(t, 2, store.getOrgDatasetCalls)
}

// TestGetOrgDatasetOrganizationLookupErrorUsesEnterpriseCopy 验证企业记录查询失败时返回企业文案。
func TestGetOrgDatasetOrganizationLookupErrorUsesEnterpriseCopy(t *testing.T) {
	svc, store, _ := newRAGFlowKnowledgeTestService(t)
	store.missingOrgDataset = true
	store.getOrganizationErr = errors.New("database down")

	_, err := svc.getOrgDataset(context.Background(), mustParseUUID(testKnowledgeOrg))
	require.ErrorContains(t, err, "查询企业失败")
}

// TestGetOrgDatasetMappingLookupErrorUsesEnterpriseCopy 验证企业 RAGFlow dataset 查询失败时返回企业文案。
func TestGetOrgDatasetMappingLookupErrorUsesEnterpriseCopy(t *testing.T) {
	svc, store, _ := newRAGFlowKnowledgeTestService(t)
	store.getOrgDatasetErr = errors.New("database down")

	_, err := svc.getOrgDataset(context.Background(), mustParseUUID(testKnowledgeOrg))
	require.ErrorContains(t, err, "查询企业 RAGFlow dataset 失败")
}

// TestEnsureOrgDatasetFailedClaimLostDoesNotCreateRemote 验证 failed 映射被其它 worker 抢占后，当前调用不重复创建远端 dataset。
func TestEnsureOrgDatasetFailedClaimLostDoesNotCreateRemote(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.orgDataset.Status = "failed"
	store.orgDataset.RagflowDatasetID = null.String{}
	store.claimDatasetErr = sql.ErrNoRows

	_, err := svc.EnsureOrgDataset(context.Background(), store.org)
	require.ErrorIs(t, err, ErrKnowledgeDatasetCreating)

	assert.Equal(t, 1, len(store.claimedDatasets))
	assert.Equal(t, 0, len(rf.createDatasetCalls))
}

// TestEnsureOrgDatasetFailedClaimWinnerCreatesRemote 验证 failed 映射只有抢占租约成功者会创建远端 dataset。
func TestEnsureOrgDatasetFailedClaimWinnerCreatesRemote(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.orgDataset.Status = "failed"
	store.orgDataset.RagflowDatasetID = null.String{}
	rf.createDatasetResult = ragflow.Dataset{ID: "retry-org-ds", Name: "retry-org"}

	dataset, err := svc.EnsureOrgDataset(context.Background(), store.org)
	require.NoError(t, err)

	assert.Equal(t, "retry-org-ds", dataset.RagflowDatasetID.String)
	require.Len(t, store.claimedDatasets, 1)
	require.Len(t, store.activatedDatasets, 1)
	assert.Equal(t, store.claimedDatasets[0].CreateClaimToken.String, store.activatedDatasets[0].CreateClaimToken.String)
	assert.Equal(t, 1, len(rf.createDatasetCalls))
}

// TestEnsureOrgDatasetReclaimsStaleCreating 验证进程崩溃留下的过期 creating 映射可以被重新抢占并继续创建。
func TestEnsureOrgDatasetReclaimsStaleCreating(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.orgDataset.Status = "creating"
	store.orgDataset.RagflowDatasetID = null.String{}
	// 过期时间：超过 ragflowDatasetCreateClaimTimeout 一分钟前。
	store.orgDataset.UpdatedAt = time.Now().Add(-ragflowDatasetCreateClaimTimeout - time.Minute)
	rf.createDatasetResult = ragflow.Dataset{ID: "reclaimed-org-ds", Name: "reclaimed-org"}

	dataset, err := svc.EnsureOrgDataset(context.Background(), store.org)
	require.NoError(t, err)

	assert.Equal(t, "reclaimed-org-ds", dataset.RagflowDatasetID.String)
	assert.Equal(t, 1, len(store.claimedDatasets))
	assert.Equal(t, 1, len(rf.createDatasetCalls))
}

// TestEnsureOrgDatasetLostClaimDoesNotOverwriteWinner 验证远端创建返回后若本地租约已丢失，不覆盖获胜者状态。
func TestEnsureOrgDatasetLostClaimDoesNotOverwriteWinner(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	// 起始为 stale creating，使本进程先抢到租约并创建远端 dataset。
	store.orgDataset.Status = "creating"
	store.orgDataset.RagflowDatasetID = null.String{}
	store.orgDataset.UpdatedAt = time.Now().Add(-ragflowDatasetCreateClaimTimeout - time.Minute)
	// 激活时租约已被其他进程抢占（命中 0 行），本进程应为败者。
	store.setActiveLosesClaim = true
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
		RuntimeTokenHash: null.StringFrom(HashAppRuntimeToken(testRuntimeToken)),
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
		RagflowDatasetID: null.StringFrom(testRemoteOrgDatasetID),
		Name:             "oc-org",
		Status:           "active",
		UpdatedAt:        time.Now(),
	}
	appDataset := sqlc.RagflowDataset{
		ID:               mustParseUUID("00000000-0000-0000-0000-000000000d02"),
		ScopeType:        "app",
		OrgID:            orgID,
		AppID:            null.StringFrom(appID),
		RagflowDatasetID: null.StringFrom(testRemoteAppDatasetID),
		Name:             "oc-app",
		Status:           "active",
		UpdatedAt:        time.Now(),
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
	getOrganizationErr    error
	getOrgDatasetErr      error
	createOrgDatasetErr   error
	createAppDatasetErr   error
	claimDatasetErr       error
	setActiveErr          error
	setActiveLosesClaim   bool
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

// createdDatasetCall 记录 CreateRAGFlowOrgDatasetMapping / CreateRAGFlowAppDatasetMapping 调用参数。
type createdDatasetCall struct {
	ScopeType        string
	OrgID            string
	AppID            string
	RagflowDatasetID string
	Name             string
	Status           string
	LastError        string
	CreateClaimToken string
	ID               string
}

func (s *fakeKnowledgeStore) GetApp(_ context.Context, id string) (sqlc.App, error) {
	app, ok := s.apps[id]
	if !ok {
		return sqlc.App{}, sql.ErrNoRows
	}
	return app, nil
}

func (s *fakeKnowledgeStore) GetOrganization(_ context.Context, id string) (sqlc.Organization, error) {
	if s.getOrganizationErr != nil {
		return sqlc.Organization{}, s.getOrganizationErr
	}
	if id != testKnowledgeOrg {
		return sqlc.Organization{}, sql.ErrNoRows
	}
	return s.org, nil
}

func (s *fakeKnowledgeStore) GetAppByRuntimeTokenHash(_ context.Context, runtimeTokenHash null.String) (sqlc.App, error) {
	app, ok := s.appsByToken[runtimeTokenHash.String]
	if !ok {
		return sqlc.App{}, sql.ErrNoRows
	}
	return app, nil
}

func (s *fakeKnowledgeStore) GetRAGFlowOrgDataset(_ context.Context, orgID string) (sqlc.RagflowDataset, error) {
	s.getOrgDatasetCalls++
	if s.getOrgDatasetErr != nil {
		return sqlc.RagflowDataset{}, s.getOrgDatasetErr
	}
	if s.missingOrgDatasetOnce && s.getOrgDatasetCalls == 1 {
		return sqlc.RagflowDataset{}, sql.ErrNoRows
	}
	if s.missingOrgDataset {
		return sqlc.RagflowDataset{}, sql.ErrNoRows
	}
	if orgID != testKnowledgeOrg {
		return sqlc.RagflowDataset{}, sql.ErrNoRows
	}
	return s.orgDataset, nil
}

func (s *fakeKnowledgeStore) GetRAGFlowAppDataset(_ context.Context, appID null.String) (sqlc.RagflowDataset, error) {
	if s.missingAppDatasetOnce {
		s.missingAppDatasetOnce = false
		return sqlc.RagflowDataset{}, sql.ErrNoRows
	}
	if s.missingAppDataset {
		return sqlc.RagflowDataset{}, sql.ErrNoRows
	}
	if appID.String != testKnowledgeApp {
		return sqlc.RagflowDataset{}, sql.ErrNoRows
	}
	return s.appDataset, nil
}

// CreateRAGFlowOrgDatasetMapping 为 :exec（INSERT IGNORE）；
// createOrgDatasetErr = sql.ErrNoRows 模拟 INSERT IGNORE 0 行（并发冲突）：返回 nil，但不存储 dataset。
// 其他非 nil 错误直接返回给 service。
func (s *fakeKnowledgeStore) CreateRAGFlowOrgDatasetMapping(_ context.Context, arg sqlc.CreateRAGFlowOrgDatasetMappingParams) error {
	if s.createOrgDatasetErr != nil && s.createOrgDatasetErr != sql.ErrNoRows {
		return s.createOrgDatasetErr
	}
	// sql.ErrNoRows 表示 INSERT IGNORE 0 行：返回 nil 但不存储。
	if s.createOrgDatasetErr == sql.ErrNoRows {
		return nil
	}
	call := createdDatasetCall{
		ScopeType:        "org",
		OrgID:            arg.OrgID,
		Name:             arg.Name,
		Status:           "creating",
		CreateClaimToken: arg.CreateClaimToken.String,
		ID:               arg.ID,
	}
	s.createdDatasets = append(s.createdDatasets, call)
	row := sqlc.RagflowDataset{
		ID:               arg.ID,
		ScopeType:        "org",
		OrgID:            arg.OrgID,
		Name:             arg.Name,
		Status:           "creating",
		CreateClaimToken: arg.CreateClaimToken,
		UpdatedAt:        s.now,
	}
	s.orgDataset = row
	s.missingOrgDataset = false
	return nil
}

// CreateRAGFlowAppDatasetMapping 为 :exec（INSERT IGNORE）；
// createAppDatasetErr = sql.ErrNoRows 模拟 INSERT IGNORE 0 行（并发冲突）：返回 nil，但不存储。
func (s *fakeKnowledgeStore) CreateRAGFlowAppDatasetMapping(_ context.Context, arg sqlc.CreateRAGFlowAppDatasetMappingParams) error {
	if s.createAppDatasetErr != nil && s.createAppDatasetErr != sql.ErrNoRows {
		return s.createAppDatasetErr
	}
	if s.createAppDatasetErr == sql.ErrNoRows {
		return nil
	}
	call := createdDatasetCall{
		ScopeType:        "app",
		OrgID:            arg.OrgID,
		AppID:            arg.AppID.String,
		Name:             arg.Name,
		Status:           "creating",
		CreateClaimToken: arg.CreateClaimToken.String,
		ID:               arg.ID,
	}
	s.createdDatasets = append(s.createdDatasets, call)
	row := sqlc.RagflowDataset{
		ID:               arg.ID,
		ScopeType:        "app",
		OrgID:            arg.OrgID,
		AppID:            arg.AppID,
		Name:             arg.Name,
		Status:           "creating",
		CreateClaimToken: arg.CreateClaimToken,
		UpdatedAt:        s.now,
	}
	s.appDataset = row
	s.missingAppDataset = false
	return nil
}

// ClaimRAGFlowDatasetCreation 为 :exec；stub 根据条件更新 claim token。
// 成功时把 orgDataset/appDataset 的 status 设为 creating，claim token 更新为新值。
// claimDatasetErr = sql.ErrNoRows 模拟"0 行更新"（其他进程持有锁）：返回 nil（:exec 不报错）
// 但不更新 dataset，使后续 GetRAGFlowDataset 返回旧 claim token 导致 service 判断抢占失败。
func (s *fakeKnowledgeStore) ClaimRAGFlowDatasetCreation(_ context.Context, arg sqlc.ClaimRAGFlowDatasetCreationParams) error {
	s.claimedDatasets = append(s.claimedDatasets, arg)
	// 非 sql.ErrNoRows 类型错误：直接返回给 service（如 DB 连接错误）。
	if s.claimDatasetErr != nil && s.claimDatasetErr != sql.ErrNoRows {
		return s.claimDatasetErr
	}
	// sql.ErrNoRows 表示"0 行更新"，模拟抢占失败：:exec 返回 nil，但 dataset 不更新。
	if s.claimDatasetErr == sql.ErrNoRows {
		return nil
	}
	staleBefore := arg.StaleBefore
	if s.orgDataset.ID == arg.ID {
		if s.orgDataset.Status == "failed" || (s.orgDataset.Status == "creating" && s.orgDataset.UpdatedAt.Before(staleBefore)) {
			s.orgDataset.Status = "creating"
			s.orgDataset.LastError = null.String{}
			s.orgDataset.CreateClaimToken = arg.CreateClaimToken
			s.orgDataset.UpdatedAt = s.now
			return nil
		}
		// 条件不满足：0 行更新，:exec 返回 nil，dataset 不变。
		return nil
	}
	if s.appDataset.Status == "failed" || (s.appDataset.Status == "creating" && s.appDataset.UpdatedAt.Before(staleBefore)) {
		s.appDataset.Status = "creating"
		s.appDataset.LastError = null.String{}
		s.appDataset.CreateClaimToken = arg.CreateClaimToken
		s.appDataset.UpdatedAt = s.now
		return nil
	}
	// 条件不满足：0 行更新，:exec 返回 nil，dataset 不变。
	return nil
}

// GetRAGFlowDataset 按 ID 读取 dataset；支持 orgDataset 和 appDataset 两类。
func (s *fakeKnowledgeStore) GetRAGFlowDataset(_ context.Context, id string) (sqlc.RagflowDataset, error) {
	if s.orgDataset.ID == id {
		return s.orgDataset, nil
	}
	if s.appDataset.ID == id {
		return s.appDataset, nil
	}
	return sqlc.RagflowDataset{}, sql.ErrNoRows
}

// SetRAGFlowDatasetActive 为 :exec；stub 激活 dataset，写入远端 ID 和名称，清除 claim token。
func (s *fakeKnowledgeStore) SetRAGFlowDatasetActive(_ context.Context, arg sqlc.SetRAGFlowDatasetActiveParams) error {
	s.activatedDatasets = append(s.activatedDatasets, arg)
	if s.setActiveErr != nil {
		return s.setActiveErr
	}
	// setActiveLosesClaim 模拟「远端创建期间租约被其他进程抢占激活」：激活 UPDATE 命中 0 行
	// （MySQL :exec 对 0 行返回 nil），dataset 保持 creating、换成获胜者 claim token 且刷新时间
	// （非 stale），remote id 仍为空。service 读回时发现 ragflow_dataset_id 不是本次写入值，判定为败者。
	if s.setActiveLosesClaim {
		target := &s.orgDataset
		if s.orgDataset.ID != arg.ID {
			target = &s.appDataset
		}
		target.Status = "creating"
		target.CreateClaimToken = null.StringFrom("winner-claim-token")
		target.RagflowDatasetID = null.String{}
		target.UpdatedAt = time.Now()
		return nil
	}
	if s.orgDataset.ID == arg.ID {
		if s.orgDataset.CreateClaimToken.String != arg.CreateClaimToken.String {
			return sql.ErrNoRows
		}
		s.orgDataset.RagflowDatasetID = arg.RagflowDatasetID
		s.orgDataset.Name = arg.Name
		s.orgDataset.Status = "active"
		s.orgDataset.CreateClaimToken = null.String{}
		return nil
	}
	if s.appDataset.CreateClaimToken.String != arg.CreateClaimToken.String {
		return sql.ErrNoRows
	}
	s.appDataset.RagflowDatasetID = arg.RagflowDatasetID
	s.appDataset.Name = arg.Name
	s.appDataset.Status = "active"
	s.appDataset.CreateClaimToken = null.String{}
	return nil
}

// MarkRAGFlowDatasetFailed 为 :exec；stub 标记失败状态，保存错误信息。
func (s *fakeKnowledgeStore) MarkRAGFlowDatasetFailed(_ context.Context, arg sqlc.MarkRAGFlowDatasetFailedParams) error {
	s.failedDatasets = append(s.failedDatasets, arg)
	if s.orgDataset.ID == arg.ID {
		if s.orgDataset.CreateClaimToken.String != arg.CreateClaimToken.String {
			return sql.ErrNoRows
		}
		s.orgDataset.Status = "failed"
		s.orgDataset.LastError = arg.LastError
		s.orgDataset.CreateClaimToken = null.String{}
		return nil
	}
	if s.appDataset.CreateClaimToken.String != arg.CreateClaimToken.String {
		return sql.ErrNoRows
	}
	s.appDataset.Status = "failed"
	s.appDataset.LastError = arg.LastError
	s.appDataset.CreateClaimToken = null.String{}
	return nil
}

// CreateRAGFlowDocument 为 :exec；stub 存入 docs map 供后续 GetRAGFlowDocument 读回。
func (s *fakeKnowledgeStore) CreateRAGFlowDocument(_ context.Context, arg sqlc.CreateRAGFlowDocumentParams) error {
	s.createdDocs = append(s.createdDocs, arg)
	id := arg.ID
	if arg.RagflowDocumentID == "remote-doc-1" {
		id = s.nextDocument
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
		CreatedAt:         s.now,
		UpdatedAt:         s.now,
	}
	// 同时在 arg.ID 和 row.ID（可能不同时用于 remote-doc-1 场景）下存入 map。
	s.docs[arg.ID] = row
	if id != arg.ID {
		s.docs[id] = row
	}
	return nil
}

func (s *fakeKnowledgeStore) ListRAGFlowDocumentsByScope(_ context.Context, arg sqlc.ListRAGFlowDocumentsByScopeParams) ([]sqlc.RagflowDocument, error) {
	items := make([]sqlc.RagflowDocument, 0, len(s.docs))
	for _, doc := range s.docs {
		if doc.ScopeType != arg.ScopeType || doc.OrgID != arg.OrgID {
			continue
		}
		if arg.AppID.Valid && doc.AppID.String != arg.AppID.String {
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

func (s *fakeKnowledgeStore) GetRAGFlowDocument(_ context.Context, id string) (sqlc.RagflowDocument, error) {
	doc, ok := s.docs[id]
	if !ok {
		return sqlc.RagflowDocument{}, sql.ErrNoRows
	}
	return doc, nil
}

// UpdateRAGFlowDocumentParseStatus 为 :exec；stub 更新 docs map，服务之后调 GetRAGFlowDocument 读回。
func (s *fakeKnowledgeStore) UpdateRAGFlowDocumentParseStatus(_ context.Context, arg sqlc.UpdateRAGFlowDocumentParseStatusParams) error {
	doc, ok := s.docs[arg.ID]
	if !ok {
		return sql.ErrNoRows
	}
	doc.ParseStatus = arg.ParseStatus
	doc.Progress = arg.Progress
	doc.LastError = arg.LastError
	s.docs[arg.ID] = doc
	return nil
}

func (s *fakeKnowledgeStore) DeleteRAGFlowDocumentMapping(_ context.Context, id string) error {
	delete(s.docs, id)
	return nil
}

func (s *fakeKnowledgeStore) DeleteRAGFlowDatasetMapping(_ context.Context, id string) error {
	s.deletedDatasetID = id
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
	retrieveErrorsByDataset map[string]error
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
	if len(datasetIDs) == 1 && f.retrieveErrorsByDataset != nil {
		if err := f.retrieveErrorsByDataset[datasetIDs[0]]; err != nil {
			return nil, err
		}
	}
	if len(datasetIDs) == 1 && len(f.retrieveChunksByDataset) > 0 {
		return f.retrieveChunksByDataset[datasetIDs[0]], nil
	}
	return f.retrieveChunks, nil
}

// testDocument 构建测试用的 RagflowDocument 记录（string datasetID）。
func testDocument(t *testing.T, scope, name string, datasetID string) sqlc.RagflowDocument {
	t.Helper()
	orgID := mustParseUUID(testKnowledgeOrg)
	appID := ""
	if scope == "app" {
		appID = mustParseUUID(testKnowledgeApp)
	}
	return sqlc.RagflowDocument{
		ID:                mustParseUUID(testKnowledgeDocument),
		DatasetID:         datasetID,
		ScopeType:         scope,
		OrgID:             orgID,
		AppID:             null.StringFromPtr(func() *string { if appID == "" { return nil }; return &appID }()),
		RagflowDocumentID: "remote-doc-1",
		Name:              name,
		SizeBytes:         12,
		ParseStatus:       "completed",
		Progress:          100,
		CreatedBy:         testKnowledgeOwner,
		CreatedAt:         time.Date(2026, 5, 26, 8, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, 5, 26, 8, 0, 0, 0, time.UTC),
	}
}

func orgKnowledgeAdmin() auth.Principal {
	return auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testKnowledgeOrg, UserID: "admin"}
}

func appOwnerPrincipal() auth.Principal {
	return auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}
}
