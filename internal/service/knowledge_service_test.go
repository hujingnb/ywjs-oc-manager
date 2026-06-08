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
	"oc-manager/internal/config"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/ragflow"
	"oc-manager/internal/store/sqlc"
)

const (
	testKnowledgeOrg            = "00000000-0000-0000-0000-000000000e01"
	testKnowledgeOrg2           = "00000000-0000-0000-0000-000000000e02"
	testKnowledgeApp            = "00000000-0000-0000-0000-000000000e03"
	testKnowledgeOwner          = "00000000-0000-0000-0000-000000000e04"
	testKnowledgeDocument       = "00000000-0000-0000-0000-000000000e05"
	testRuntimeToken            = "runtime-token"
	testRuntimeTokenHash        = "af4a4b5c"
	testRemoteOrgDatasetID      = "org-ds"
	testRemoteAppDatasetID      = "app-ds"
	testIndustryKnowledgeBaseID = "00000000-0000-0000-0000-000000000f01"
	testRemoteIndustryDatasetID = "industry-ds"
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

// TestRAGFlowKnowledgeListOrgIncludesQuota 验证企业知识库列表返回已用、上限和剩余空间。
func TestRAGFlowKnowledgeListOrgIncludesQuota(t *testing.T) {
	svc, store, _ := newRAGFlowKnowledgeTestService(t)
	store.org.KnowledgeQuotaBytes = 100
	store.docs["doc-a"] = testDocument(t, "org", "a.md", store.orgDataset.ID)
	doc := store.docs["doc-a"]
	doc.SizeBytes = 40
	store.docs["doc-a"] = doc

	result, err := svc.ListOrg(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, 1, 50, "", "")
	require.NoError(t, err)

	assert.Equal(t, int64(40), result.UsedBytes)
	assert.Equal(t, int64(100), result.QuotaBytes)
	assert.Equal(t, int64(60), result.RemainingBytes)
}

// TestRAGFlowKnowledgeUploadOrgRejectsQuotaExceeded 验证企业知识库累计空间不足时不调用 RAGFlow 上传。
func TestRAGFlowKnowledgeUploadOrgRejectsQuotaExceeded(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.org.KnowledgeQuotaBytes = 10
	store.docs["doc-a"] = testDocument(t, "org", "a.md", store.orgDataset.ID)
	doc := store.docs["doc-a"]
	doc.SizeBytes = 8
	store.docs["doc-a"] = doc

	_, err := svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "b.md", strings.NewReader("bbb"), 3)

	require.ErrorIs(t, err, ErrKnowledgeQuotaExceeded)
	assert.Empty(t, rf.uploadCalls)
}

// TestRAGFlowKnowledgeUploadAppAllowsExactQuota 验证实例知识库刚好达到容量上限时允许上传。
func TestRAGFlowKnowledgeUploadAppAllowsExactQuota(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	app := store.apps[testKnowledgeApp]
	app.KnowledgeQuotaBytes = 10
	store.apps[testKnowledgeApp] = app
	store.docs["doc-a"] = testDocument(t, "app", "a.md", store.appDataset.ID)
	doc := store.docs["doc-a"]
	doc.AppID = null.StringFrom(testKnowledgeApp)
	doc.SizeBytes = 8
	store.docs["doc-a"] = doc

	_, err := svc.SaveAppFile(context.Background(), appOwnerPrincipal(), testKnowledgeApp, "b.md", strings.NewReader("bb"), 2)

	require.NoError(t, err)
	require.Len(t, rf.uploadCalls, 1)
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

// TestClearOrgKnowledgeFilesDeletesAllDocuments 验证清空企业知识库会删除该企业 dataset 下的全部文件，而不是只处理当前分页。
func TestClearOrgKnowledgeFilesDeletesAllDocuments(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	first := testDocument(t, "org", "policy-a.md", store.orgDataset.ID)
	second := testDocument(t, "org", "policy-b.md", store.orgDataset.ID)
	second.ID = mustParseUUID("00000000-0000-0000-0000-000000000a11")
	second.RagflowDocumentID = "remote-doc-2"
	store.docs[first.ID] = first
	store.docs[second.ID] = second

	err := svc.ClearOrgFiles(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg)
	require.NoError(t, err)

	require.Len(t, rf.deleteCalls, 1)
	assert.Equal(t, testRemoteOrgDatasetID, rf.deleteCalls[0].datasetID)
	assert.ElementsMatch(t, []string{"remote-doc-1", "remote-doc-2"}, rf.deleteCalls[0].documentIDs)
	assert.Empty(t, store.docs)
}

// TestClearIndustryKnowledgeFilesDeletesAllDocuments 验证清空行业知识库只删除当前行业库文件，不影响其他行业库文件。
func TestClearIndustryKnowledgeFilesDeletesAllDocuments(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	first := industryTestDocument(t, "policy-a.pdf", store.industryDataset.ID, testIndustryKnowledgeBaseID)
	second := industryTestDocument(t, "policy-b.pdf", store.industryDataset.ID, testIndustryKnowledgeBaseID)
	second.ID = mustParseUUID("00000000-0000-0000-0000-000000000a12")
	second.RagflowDocumentID = "remote-doc-2"
	other := industryTestDocument(t, "other.pdf", "other-dataset", "industry-other")
	other.ID = mustParseUUID("00000000-0000-0000-0000-000000000a13")
	other.RagflowDocumentID = "remote-doc-other"
	store.docs[first.ID] = first
	store.docs[second.ID] = second
	store.docs[other.ID] = other

	err := svc.ClearIndustryFiles(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, testIndustryKnowledgeBaseID)
	require.NoError(t, err)

	require.Len(t, rf.deleteCalls, 1)
	assert.Equal(t, testRemoteIndustryDatasetID, rf.deleteCalls[0].datasetID)
	assert.ElementsMatch(t, []string{"remote-doc-1", "remote-doc-2"}, rf.deleteCalls[0].documentIDs)
	require.Len(t, store.docs, 1)
	assert.Equal(t, "other.pdf", store.docs[other.ID].Name)
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

// TestCreateIndustryKnowledgeBasePlatformOnly 验证行业库创建只允许平台管理员，且名称会去除首尾空白。
func TestCreateIndustryKnowledgeBasePlatformOnly(t *testing.T) {
	svc, store, _ := newRAGFlowKnowledgeTestService(t)

	created, err := svc.CreateIndustryKnowledgeBase(context.Background(), auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin}, "  保险  ")
	require.NoError(t, err)
	assert.Equal(t, "保险", created.Name)
	assert.Contains(t, store.industryBases, created.ID)

	_, err = svc.CreateIndustryKnowledgeBase(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testKnowledgeOrg}, "金融")
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestListIndustryFilesFiltersByFilenameStatusAndCreatedRange 验证行业知识库文件列表同时按文件名、解析状态和创建时间区间过滤；
// createdBefore 是开区间上界，handler 会把用户选择的结束日期转换成下一日零点。
func TestListIndustryFilesFiltersByFilenameStatusAndCreatedRange(t *testing.T) {
	svc, store, _ := newRAGFlowKnowledgeTestService(t)
	included := industryTestDocument(t, "policy-2026.pdf", store.industryDataset.ID, testIndustryKnowledgeBaseID)
	included.ParseStatus = "completed"
	included.CreatedAt = time.Date(2026, 6, 5, 23, 59, 0, 0, time.UTC)
	nameMismatch := industryTestDocument(t, "manual-2026.pdf", store.industryDataset.ID, testIndustryKnowledgeBaseID)
	nameMismatch.ID = mustParseUUID("00000000-0000-0000-0000-000000000a02")
	nameMismatch.ParseStatus = "completed"
	nameMismatch.CreatedAt = time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	statusMismatch := industryTestDocument(t, "policy-failed.pdf", store.industryDataset.ID, testIndustryKnowledgeBaseID)
	statusMismatch.ID = mustParseUUID("00000000-0000-0000-0000-000000000a03")
	statusMismatch.ParseStatus = "failed"
	statusMismatch.CreatedAt = time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	dateMismatch := industryTestDocument(t, "policy-next-day.pdf", store.industryDataset.ID, testIndustryKnowledgeBaseID)
	dateMismatch.ID = mustParseUUID("00000000-0000-0000-0000-000000000a04")
	dateMismatch.ParseStatus = "completed"
	dateMismatch.CreatedAt = time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)
	store.docs[included.ID] = included
	store.docs[nameMismatch.ID] = nameMismatch
	store.docs[statusMismatch.ID] = statusMismatch
	store.docs[dateMismatch.ID] = dateMismatch

	result, err := svc.ListIndustryFiles(
		context.Background(),
		auth.Principal{Role: domain.UserRolePlatformAdmin},
		testIndustryKnowledgeBaseID,
		1,
		50,
		"policy",
		"completed",
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)

	require.Len(t, result.Items, 1)
	assert.Equal(t, "policy-2026.pdf", result.Items[0].Name)
	assert.Equal(t, int64(1), result.Total)
}

// TestExternalUploadCreatesIndustryWhenMissing 验证外部上传在行业库不存在时会自动创建行业库和 dataset。
func TestExternalUploadCreatesIndustryWhenMissing(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.industryBases = map[string]sqlc.IndustryKnowledgeBasis{}
	store.missingIndustryDataset = true
	rf.createDatasetResult = ragflow.Dataset{ID: testRemoteIndustryDatasetID, Name: "保险"}

	doc, err := svc.ExternalUploadIndustryFile(context.Background(), "保险", "policy.pdf", strings.NewReader("new"), 3)
	require.NoError(t, err)

	assert.Equal(t, "policy.pdf", doc.Name)
	assert.NotEmpty(t, store.industryBases)
	assert.Empty(t, rf.deleteCalls)
	require.Len(t, rf.uploadCalls, 1)
	assert.Equal(t, testRemoteIndustryDatasetID, rf.uploadCalls[0].datasetID)
}

// TestExternalUploadIndustryCreateRaceRereadsWinner 验证外部按名称创建行业库遇到并发同名创建时会重新读取获胜记录。
func TestExternalUploadIndustryCreateRaceRereadsWinner(t *testing.T) {
	svc, store, _ := newRAGFlowKnowledgeTestService(t)
	store.industryBases = map[string]sqlc.IndustryKnowledgeBasis{}
	store.createIndustryDuplicateOnce = true

	base, err := svc.getOrCreateIndustryKnowledgeBaseByName(context.Background(), "保险", externalIndustryKnowledgeCreatedBy)
	require.NoError(t, err)

	assert.Equal(t, "winner-industry", base.ID)
	assert.Equal(t, "保险", base.Name)
}

// TestExternalUploadOverwritesSameNameAfterNewMetadataSaved 验证同名覆盖会先保存新 metadata，再删除旧远端文件。
func TestExternalUploadOverwritesSameNameAfterNewMetadataSaved(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	rf.uploadDocument = ragflow.Document{ID: "remote-doc-new", Name: "policy.pdf", Size: 3, Run: "UNSTART"}
	oldDoc := industryTestDocument(t, "policy.pdf", store.industryDataset.ID, testIndustryKnowledgeBaseID)
	store.docs[oldDoc.ID] = oldDoc

	doc, err := svc.ExternalUploadIndustryFile(context.Background(), "保险", "policy.pdf", strings.NewReader("new"), 3)
	require.NoError(t, err)

	assert.Equal(t, oldDoc.ID, doc.ID)
	assert.Equal(t, "remote-doc-new", store.docs[oldDoc.ID].RagflowDocumentID)
	require.Len(t, rf.deleteCalls, 1)
	assert.Equal(t, []string{"remote-doc-1"}, rf.deleteCalls[0].documentIDs)
	assert.Equal(t, []string{
		"upload:remote-doc-new",
		"parse:remote-doc-new",
		"replace-industry-doc:remote-doc-new",
		"delete-docs:remote-doc-1",
	}, store.recordedEvents())
}

// TestExternalUploadIndustryOverwriteUploadFailureKeepsOldDocument 验证同名覆盖上传新文件失败时不会删除旧文件映射或旧远端文件。
func TestExternalUploadIndustryOverwriteUploadFailureKeepsOldDocument(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	oldDoc := industryTestDocument(t, "policy.pdf", store.industryDataset.ID, testIndustryKnowledgeBaseID)
	store.docs[oldDoc.ID] = oldDoc
	rf.uploadErr = errors.New("ragflow upload failed")

	_, err := svc.ExternalUploadIndustryFile(context.Background(), "保险", "policy.pdf", strings.NewReader("new"), 3)
	require.ErrorContains(t, err, "上传 RAGFlow document 失败")

	assert.Empty(t, rf.deleteCalls)
	assert.Equal(t, oldDoc, store.docs[oldDoc.ID])
}

// TestExternalUploadIndustryOverwriteParseFailureKeepsOldDocument 验证同名覆盖的新远端文件解析失败时会清理新文件并保留旧映射。
func TestExternalUploadIndustryOverwriteParseFailureKeepsOldDocument(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	oldDoc := industryTestDocument(t, "policy.pdf", store.industryDataset.ID, testIndustryKnowledgeBaseID)
	store.docs[oldDoc.ID] = oldDoc
	rf.uploadDocument = ragflow.Document{ID: "remote-doc-new", Name: "policy.pdf", Size: 3, Run: "UNSTART"}
	rf.parseErr = errors.New("ragflow parse failed")

	_, err := svc.ExternalUploadIndustryFile(context.Background(), "保险", "policy.pdf", strings.NewReader("new"), 3)
	require.ErrorContains(t, err, "触发 RAGFlow 解析失败")

	require.Len(t, rf.deleteCalls, 1)
	assert.Equal(t, []string{"remote-doc-new"}, rf.deleteCalls[0].documentIDs)
	assert.Equal(t, oldDoc, store.docs[oldDoc.ID])
}

// TestExternalUploadIndustryOverwriteMetadataFailureCleansNewRemote 验证同名覆盖本地替换失败时清理新远端文件并保留旧映射。
func TestExternalUploadIndustryOverwriteMetadataFailureCleansNewRemote(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	oldDoc := industryTestDocument(t, "policy.pdf", store.industryDataset.ID, testIndustryKnowledgeBaseID)
	store.docs[oldDoc.ID] = oldDoc
	rf.uploadDocument = ragflow.Document{ID: "remote-doc-new", Name: "policy.pdf", Size: 3, Run: "UNSTART"}
	store.replaceIndustryDocumentErr = errors.New("database down")

	_, err := svc.ExternalUploadIndustryFile(context.Background(), "保险", "policy.pdf", strings.NewReader("new"), 3)
	require.ErrorContains(t, err, "替换行业知识库文件元数据失败")

	require.Len(t, rf.deleteCalls, 1)
	assert.Equal(t, []string{"remote-doc-new"}, rf.deleteCalls[0].documentIDs)
	assert.Equal(t, oldDoc, store.docs[oldDoc.ID])
}

// TestExternalUploadIndustryOverwriteConcurrentReplaceCleansNewRemote 验证并发同名覆盖导致读回不一致时会清理新远端文件并要求调用方重试。
func TestExternalUploadIndustryOverwriteConcurrentReplaceCleansNewRemote(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	oldDoc := industryTestDocument(t, "policy.pdf", store.industryDataset.ID, testIndustryKnowledgeBaseID)
	store.docs[oldDoc.ID] = oldDoc
	rf.uploadDocument = ragflow.Document{ID: "remote-doc-new", Name: "policy.pdf", Size: 3, Run: "UNSTART"}
	store.replaceConcurrentRemoteID = "remote-doc-concurrent"

	_, err := svc.ExternalUploadIndustryFile(context.Background(), "保险", "policy.pdf", strings.NewReader("new"), 3)
	require.ErrorIs(t, err, ErrConflict)

	require.Len(t, rf.deleteCalls, 1)
	assert.Equal(t, []string{"remote-doc-new"}, rf.deleteCalls[0].documentIDs)
	assert.Equal(t, "remote-doc-concurrent", store.docs[oldDoc.ID].RagflowDocumentID)
}

// TestDeleteIndustryKnowledgeBaseLocalCleanupFailureDoesNotDeleteRemote 验证行业库本地 dataset 清理失败时不会先删除远端 dataset。
func TestDeleteIndustryKnowledgeBaseLocalCleanupFailureDoesNotDeleteRemote(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.deleteDatasetMappingErr = errors.New("database down")

	err := svc.DeleteIndustryKnowledgeBase(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, testIndustryKnowledgeBaseID)
	require.ErrorContains(t, err, "删除行业知识库 dataset 映射失败")

	assert.Empty(t, rf.deleteDatasetCalls)
}

// TestDeleteIndustryKnowledgeBaseMissingDatasetStillDeletesBase 验证行业库没有 dataset 映射时仍可软删除本地行业库。
func TestDeleteIndustryKnowledgeBaseMissingDatasetStillDeletesBase(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.missingIndustryDataset = true

	err := svc.DeleteIndustryKnowledgeBase(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, testIndustryKnowledgeBaseID)
	require.NoError(t, err)

	assert.Equal(t, testIndustryKnowledgeBaseID, store.deletedIndustryBaseID)
	assert.Empty(t, rf.deleteDatasetCalls)
}

// TestDeleteIndustryKnowledgeBaseDetectsConcurrentVersionReference 验证删除期间若行业库被版本重新引用，会返回占用错误且不删除远端 dataset。
func TestDeleteIndustryKnowledgeBaseDetectsConcurrentVersionReference(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	// 软删命中 0 行代表 SQL 的 NOT EXISTS 保护发现并发新增的版本关联。
	store.softDeleteIndustryAffected = 0

	err := svc.DeleteIndustryKnowledgeBase(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, testIndustryKnowledgeBaseID)
	require.ErrorIs(t, err, ErrIndustryKnowledgeInUse)

	assert.Empty(t, store.deletedIndustryBaseID)
	assert.Empty(t, rf.deleteDatasetCalls)
}

// TestUploadToDatasetRejectsScopeMismatchBeforeRemoteUpload 验证上传目标作用域与 dataset 不匹配时会在远端上传前失败。
func TestUploadToDatasetRejectsScopeMismatchBeforeRemoteUpload(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)

	_, err := svc.uploadToDataset(context.Background(), knowledgeUploadTarget{
		Dataset:                 store.orgDataset,
		IndustryKnowledgeBaseID: testIndustryKnowledgeBaseID,
		CreatedBy:               "u-platform",
	}, "policy.pdf", strings.NewReader("new"), 3)
	require.ErrorContains(t, err, "知识库上传目标")

	assert.Empty(t, rf.uploadCalls)
}

// TestRuntimeSearchRetrievesEachIndustryWithTopK 验证 runtime 检索会按 app、org、每个行业库分别调用 RAGFlow，行业库各自使用 top_k。
func TestRuntimeSearchRetrievesEachIndustryWithTopK(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	app := store.apps[testKnowledgeApp]
	app.VersionID = null.StringFrom("ver-1")
	store.apps[testKnowledgeApp] = app
	store.appsByToken[HashAppRuntimeToken(testRuntimeToken)] = app
	store.versionIndustryBases["ver-1"] = []sqlc.IndustryKnowledgeBasis{
		{ID: "industry-a", Name: "保险"},
		{ID: "industry-b", Name: "银行"},
	}
	store.industryDatasets["industry-a"] = industryDataset("industry-a", "remote-a")
	store.industryDatasets["industry-b"] = industryDataset("industry-b", "remote-b")
	rf.retrieveChunksByDataset = map[string][]ragflow.RetrievalChunk{
		// 行业库 A 返回一个命中，用于断言来源行业库 ID/name 会写入结果。
		"remote-a": {{DocumentID: "doc-a", DocumentName: "a.md", DatasetID: "remote-a", Content: "理赔 A", Similarity: 0.8}},
		// 行业库 B 返回一个命中，用于断言每个行业库命中都保留各自来源。
		"remote-b": {{DocumentID: "doc-b", DocumentName: "b.md", DatasetID: "remote-b", Content: "理赔 B", Similarity: 0.7}},
	}

	result, err := svc.RuntimeSearch(context.Background(), testRuntimeToken, "理赔", 6)
	require.NoError(t, err)

	require.Len(t, rf.retrieveCalls, 4)
	assert.Equal(t, []string{testRemoteAppDatasetID}, rf.retrieveCalls[0].datasetIDs)
	assert.Equal(t, []string{testRemoteOrgDatasetID}, rf.retrieveCalls[1].datasetIDs)
	assert.Equal(t, []string{"remote-a"}, rf.retrieveCalls[2].datasetIDs)
	assert.Equal(t, []string{"remote-b"}, rf.retrieveCalls[3].datasetIDs)
	assert.Equal(t, int32(6), rf.retrieveCalls[2].topK)
	assert.Equal(t, int32(6), rf.retrieveCalls[3].topK)
	require.Len(t, result.Results, 2)
	assert.Equal(t, "industry", result.Results[0].Scope)
	assert.Equal(t, "industry-a", result.Results[0].IndustryKnowledgeBaseID)
	assert.Equal(t, "保险", result.Results[0].IndustryKnowledgeBaseName)
	assert.Equal(t, "industry-b", result.Results[1].IndustryKnowledgeBaseID)
	assert.Equal(t, "银行", result.Results[1].IndustryKnowledgeBaseName)
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

// TestListKnowledgeEmbeddingModelsOnlyPlatformAdmin 验证 RAGFlow embedding 模型候选只允许平台管理员读取。
func TestListKnowledgeEmbeddingModelsOnlyPlatformAdmin(t *testing.T) {
	svc, _, _ := newRAGFlowKnowledgeTestService(t)

	_, err := svc.ListKnowledgeEmbeddingModels(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testKnowledgeOrg})

	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestListKnowledgeEmbeddingModelsReturnsFallbacks 验证模型候选来自配置兜底清单，并补齐展示标签。
func TestListKnowledgeEmbeddingModelsReturnsFallbacks(t *testing.T) {
	svc, _, _ := newRAGFlowKnowledgeTestService(t)
	svc.SetEmbeddingModelFallbacks([]config.RAGFlowEmbeddingModelConfig{
		{Name: "BAAI/bge-m3", Label: "BGE M3", Provider: "OpenAI-API-Compatible"},
		{Name: "text-embedding-3-small", Provider: "OpenAI"},
	})

	result, err := svc.ListKnowledgeEmbeddingModels(context.Background(), platformKnowledgePrincipal())
	require.NoError(t, err)

	require.Len(t, result.Items, 2)
	assert.Equal(t, KnowledgeEmbeddingModelResult{Name: "BAAI/bge-m3", Label: "BGE M3", Provider: "OpenAI-API-Compatible", Available: true}, result.Items[0])
	assert.Equal(t, KnowledgeEmbeddingModelResult{Name: "text-embedding-3-small", Label: "text-embedding-3-small", Provider: "OpenAI", Available: true}, result.Items[1])
}

// TestKnowledgeRAGFlowDatasetInfoOnlyPlatformAdmin 验证 RAGFlow dataset 运维信息只允许平台管理员读取。
func TestKnowledgeRAGFlowDatasetInfoOnlyPlatformAdmin(t *testing.T) {
	svc, _, _ := newRAGFlowKnowledgeTestService(t)

	_, err := svc.GetKnowledgeRAGFlowDatasetInfo(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testKnowledgeOrg}, KnowledgeRAGFlowScopeOrg, testKnowledgeOrg)

	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestKnowledgeRAGFlowDatasetInfoReturnsNotCreated 验证查看 RAGFlow 信息不会懒创建 dataset。
func TestKnowledgeRAGFlowDatasetInfoReturnsNotCreated(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.missingOrgDataset = true

	result, err := svc.GetKnowledgeRAGFlowDatasetInfo(context.Background(), platformKnowledgePrincipal(), KnowledgeRAGFlowScopeOrg, testKnowledgeOrg)
	require.NoError(t, err)

	assert.Equal(t, "not_created", result.Status)
	assert.Equal(t, "测试组织", result.TargetName)
	assert.Empty(t, rf.createDatasetCalls)
}

// TestUpdateKnowledgeEmbeddingModelResetsLocalDocuments 验证模型修改和整库重解析成功后，本地文件全部回到 queued。
func TestUpdateKnowledgeEmbeddingModelResetsLocalDocuments(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	svc.SetEmbeddingModelFallbacks([]config.RAGFlowEmbeddingModelConfig{{Name: "BAAI/bge-m3", Label: "BAAI/bge-m3", Provider: "OpenAI-API-Compatible"}})
	store.docs["doc-a"] = makeKnowledgeDoc("doc-a", store.orgDataset.ID, "remote-a", "completed", 100)
	store.docs["doc-b"] = makeKnowledgeDoc("doc-b", store.orgDataset.ID, "remote-b", "failed", 42)
	rf.datasetDetail = ragflow.Dataset{ID: testRemoteOrgDatasetID, Name: "oc-org", EmbeddingModelID: "BAAI/bge-m3@OpenAI-API-Compatible", DocNum: 2, ChunkNum: 9}

	result, err := svc.UpdateKnowledgeEmbeddingModel(context.Background(), platformKnowledgePrincipal(), KnowledgeRAGFlowScopeOrg, testKnowledgeOrg, KnowledgeEmbeddingModelInput{Name: "BAAI/bge-m3", Provider: "OpenAI-API-Compatible"})
	require.NoError(t, err)

	assert.Equal(t, "ok", result.Status)
	assert.Equal(t, []string{testRemoteOrgDatasetID}, rf.runDatasetEmbeddingCalls)
	require.Len(t, rf.updateEmbeddingModelCalls, 1)
	assert.Equal(t, "BAAI/bge-m3@OpenAI-API-Compatible", rf.updateEmbeddingModelCalls[0].embeddingModel)
	assert.Equal(t, "queued", store.docs["doc-a"].ParseStatus)
	assert.Equal(t, int32(0), store.docs["doc-a"].Progress)
	assert.False(t, store.docs["doc-a"].LastError.Valid)
	assert.Equal(t, "queued", store.docs["doc-b"].ParseStatus)
}

// TestUpdateKnowledgeEmbeddingModelDoesNotResetWhenReparseFails 验证 RAGFlow 未接受整库重解析时不改本地状态，避免 UI 误报解析中。
func TestUpdateKnowledgeEmbeddingModelDoesNotResetWhenReparseFails(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	svc.SetEmbeddingModelFallbacks([]config.RAGFlowEmbeddingModelConfig{{Name: "BAAI/bge-m3", Provider: "OpenAI-API-Compatible"}})
	store.docs["doc-a"] = makeKnowledgeDoc("doc-a", store.orgDataset.ID, "remote-a", "completed", 100)
	rf.runDatasetEmbeddingErr = errors.New("ragflow busy")

	_, err := svc.UpdateKnowledgeEmbeddingModel(context.Background(), platformKnowledgePrincipal(), KnowledgeRAGFlowScopeOrg, testKnowledgeOrg, KnowledgeEmbeddingModelInput{Name: "BAAI/bge-m3", Provider: "OpenAI-API-Compatible"})
	require.Error(t, err)

	assert.Equal(t, "completed", store.docs["doc-a"].ParseStatus)
	assert.Equal(t, int32(100), store.docs["doc-a"].Progress)
}

// TestUpdateKnowledgeEmbeddingModelRejectsUnknownModel 验证未知模型在本地校验失败，不调用 RAGFlow 修改或重解析。
func TestUpdateKnowledgeEmbeddingModelRejectsUnknownModel(t *testing.T) {
	svc, _, rf := newRAGFlowKnowledgeTestService(t)
	svc.SetEmbeddingModelFallbacks([]config.RAGFlowEmbeddingModelConfig{{Name: "BAAI/bge-m3", Provider: "OpenAI-API-Compatible"}})

	_, err := svc.UpdateKnowledgeEmbeddingModel(context.Background(), platformKnowledgePrincipal(), KnowledgeRAGFlowScopeOrg, testKnowledgeOrg, KnowledgeEmbeddingModelInput{Name: "unknown-model", Provider: "OpenAI-API-Compatible"})

	require.Error(t, err)
	assert.Empty(t, rf.updateEmbeddingModelCalls)
	assert.Empty(t, rf.runDatasetEmbeddingCalls)
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

// TestRuntimeAddRejectsQuotaExceeded 验证 runtime token 写入实例知识库也不能绕过容量限制。
func TestRuntimeAddRejectsQuotaExceeded(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	app := store.apps[testKnowledgeApp]
	app.KnowledgeQuotaBytes = 5
	store.apps[testKnowledgeApp] = app
	store.appsByToken[HashAppRuntimeToken(testRuntimeToken)] = app

	_, err := svc.RuntimeAddFile(context.Background(), testRuntimeToken, "research.md", strings.NewReader("report"), 6)

	require.ErrorIs(t, err, ErrKnowledgeQuotaExceeded)
	assert.Empty(t, rf.uploadCalls)
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
	svc.SetDefaultEmbeddingModel("BAAI/bge-m3")
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
	assert.Equal(t, "BAAI/bge-m3", rf.createDatasetCalls[0].embeddingModel)
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
	events := []string{}
	store.events = &events
	rf := &fakeRAGFlowKnowledgeClient{
		uploadDocument: ragflow.Document{ID: "remote-doc-1", Name: "report.md", Size: 8, Run: "UNSTART"},
		createDatasetResult: ragflow.Dataset{
			ID:   "created-ds",
			Name: "created-dataset",
		},
		events: &events,
	}
	return NewKnowledgeService(store, rf), store, rf
}

func newFakeKnowledgeStore(t *testing.T) *fakeKnowledgeStore {
	t.Helper()
	orgID := mustParseUUID(testKnowledgeOrg)
	appID := mustParseUUID(testKnowledgeApp)
	ownerID := mustParseUUID(testKnowledgeOwner)
	app := sqlc.App{
		ID:                  appID,
		OrgID:               orgID,
		OwnerUserID:         ownerID,
		Name:                "实例",
		Status:              domain.AppStatusRunning,
		KnowledgeQuotaBytes: KnowledgeQuotaDefaultBytes,
		RuntimeTokenHash:    null.StringFrom(HashAppRuntimeToken(testRuntimeToken)),
	}
	org := sqlc.Organization{
		ID:                  orgID,
		Name:                "测试组织",
		Code:                "test-org",
		Status:              domain.StatusActive,
		KnowledgeQuotaBytes: KnowledgeQuotaDefaultBytes,
	}
	orgDataset := sqlc.RagflowDataset{
		ID:               mustParseUUID("00000000-0000-0000-0000-000000000d01"),
		ScopeType:        "org",
		OrgID:            null.StringFrom(orgID),
		RagflowDatasetID: null.StringFrom(testRemoteOrgDatasetID),
		Name:             "oc-org",
		Status:           "active",
		UpdatedAt:        time.Now(),
	}
	appDataset := sqlc.RagflowDataset{
		ID:               mustParseUUID("00000000-0000-0000-0000-000000000d02"),
		ScopeType:        "app",
		OrgID:            null.StringFrom(orgID),
		AppID:            null.StringFrom(appID),
		RagflowDatasetID: null.StringFrom(testRemoteAppDatasetID),
		Name:             "oc-app",
		Status:           "active",
		UpdatedAt:        time.Now(),
	}
	industryBase := sqlc.IndustryKnowledgeBasis{
		ID:        testIndustryKnowledgeBaseID,
		Name:      "保险",
		CreatedBy: "u-platform",
	}
	industryDataset := sqlc.RagflowDataset{
		ID:                      mustParseUUID("00000000-0000-0000-0000-000000000d03"),
		ScopeType:               "industry",
		IndustryKnowledgeBaseID: null.StringFrom(testIndustryKnowledgeBaseID),
		RagflowDatasetID:        null.StringFrom(testRemoteIndustryDatasetID),
		Name:                    "oc-industry",
		Status:                  "active",
		UpdatedAt:               time.Now(),
	}
	return &fakeKnowledgeStore{
		apps:                       map[string]sqlc.App{testKnowledgeApp: app},
		appsByToken:                map[string]sqlc.App{HashAppRuntimeToken(testRuntimeToken): app, testRuntimeTokenHash: app},
		org:                        org,
		orgDataset:                 orgDataset,
		appDataset:                 appDataset,
		industryBases:              map[string]sqlc.IndustryKnowledgeBasis{testIndustryKnowledgeBaseID: industryBase},
		industryDataset:            industryDataset,
		industryDatasets:           map[string]sqlc.RagflowDataset{testIndustryKnowledgeBaseID: industryDataset},
		versionIndustryBases:       map[string][]sqlc.IndustryKnowledgeBasis{},
		docs:                       map[string]sqlc.RagflowDocument{},
		nextDocument:               "00000000-0000-0000-0000-000000000e06",
		now:                        time.Date(2026, 5, 26, 8, 0, 0, 0, time.UTC),
		softDeleteIndustryAffected: 1,
	}
}

type fakeKnowledgeStore struct {
	apps                        map[string]sqlc.App
	appsByToken                 map[string]sqlc.App
	org                         sqlc.Organization
	orgDataset                  sqlc.RagflowDataset
	appDataset                  sqlc.RagflowDataset
	industryBases               map[string]sqlc.IndustryKnowledgeBasis
	industryDataset             sqlc.RagflowDataset
	industryDatasets            map[string]sqlc.RagflowDataset
	versionIndustryBases        map[string][]sqlc.IndustryKnowledgeBasis
	missingOrgDataset           bool
	missingAppDataset           bool
	missingIndustryDataset      bool
	missingOrgDatasetOnce       bool
	missingAppDatasetOnce       bool
	createIndustryDuplicateOnce bool
	getOrganizationErr          error
	getOrgDatasetErr            error
	createOrgDatasetErr         error
	createAppDatasetErr         error
	replaceIndustryDocumentErr  error
	replaceConcurrentRemoteID   string
	claimDatasetErr             error
	setActiveErr                error
	deleteDatasetMappingErr     error
	setActiveLosesClaim         bool
	docs                        map[string]sqlc.RagflowDocument
	createdDatasets             []createdDatasetCall
	claimedDatasets             []sqlc.ClaimRAGFlowDatasetCreationParams
	activatedDatasets           []sqlc.SetRAGFlowDatasetActiveParams
	failedDatasets              []sqlc.MarkRAGFlowDatasetFailedParams
	createdDocs                 []sqlc.CreateRAGFlowDocumentParams
	createdIndustryDocs         []sqlc.CreateRAGFlowIndustryDocumentParams
	deletedDatasetID            string
	deletedIndustryBaseID       string
	softDeleteIndustryAffected  int64
	industryInUseCount          int64
	getOrgDatasetCalls          int
	nextDocument                string
	now                         time.Time
	events                      *[]string
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

func (s *fakeKnowledgeStore) CreateIndustryKnowledgeBase(_ context.Context, arg sqlc.CreateIndustryKnowledgeBaseParams) error {
	if s.createIndustryDuplicateOnce {
		s.createIndustryDuplicateOnce = false
		winner := sqlc.IndustryKnowledgeBasis{
			ID:        "winner-industry",
			Name:      arg.Name,
			CreatedBy: externalIndustryKnowledgeCreatedBy,
			CreatedAt: s.now,
			UpdatedAt: s.now,
		}
		s.industryBases[winner.ID] = winner
		return errors.New("Duplicate entry '保险' for key 'uk_industry_knowledge_bases_name_active'")
	}
	row := sqlc.IndustryKnowledgeBasis{
		ID:        arg.ID,
		Name:      arg.Name,
		CreatedBy: arg.CreatedBy,
		CreatedAt: s.now,
		UpdatedAt: s.now,
	}
	s.industryBases[arg.ID] = row
	return nil
}

func (s *fakeKnowledgeStore) GetIndustryKnowledgeBase(_ context.Context, id string) (sqlc.IndustryKnowledgeBasis, error) {
	row, ok := s.industryBases[id]
	if !ok || row.DeletedAt.Valid {
		return sqlc.IndustryKnowledgeBasis{}, sql.ErrNoRows
	}
	return row, nil
}

func (s *fakeKnowledgeStore) GetIndustryKnowledgeBaseForUpdate(ctx context.Context, id string) (sqlc.IndustryKnowledgeBasis, error) {
	return s.GetIndustryKnowledgeBase(ctx, id)
}

func (s *fakeKnowledgeStore) GetIndustryKnowledgeBaseByName(_ context.Context, name string) (sqlc.IndustryKnowledgeBasis, error) {
	for _, row := range s.industryBases {
		if row.Name == name && !row.DeletedAt.Valid {
			return row, nil
		}
	}
	return sqlc.IndustryKnowledgeBasis{}, sql.ErrNoRows
}

func (s *fakeKnowledgeStore) ListIndustryKnowledgeBases(_ context.Context, arg sqlc.ListIndustryKnowledgeBasesParams) ([]sqlc.ListIndustryKnowledgeBasesRow, error) {
	items := make([]sqlc.ListIndustryKnowledgeBasesRow, 0, len(s.industryBases))
	seen := map[string]struct{}{}
	for _, base := range s.industryBases {
		if base.DeletedAt.Valid {
			continue
		}
		if _, ok := seen[base.ID]; ok {
			continue
		}
		seen[base.ID] = struct{}{}
		if keyword, ok := arg.Keyword.(string); ok && keyword != "" && !strings.Contains(base.Name, keyword) {
			continue
		}
		var documentCount int64
		for _, doc := range s.docs {
			if doc.ScopeType == "industry" && doc.IndustryKnowledgeBaseID.String == base.ID {
				documentCount++
			}
		}
		items = append(items, sqlc.ListIndustryKnowledgeBasesRow{
			ID:            base.ID,
			Name:          base.Name,
			CreatedBy:     base.CreatedBy,
			CreatedAt:     base.CreatedAt,
			UpdatedAt:     base.UpdatedAt,
			DeletedAt:     base.DeletedAt,
			DocumentCount: documentCount,
		})
	}
	return items, nil
}

func (s *fakeKnowledgeStore) CountIndustryKnowledgeBases(ctx context.Context, arg sqlc.CountIndustryKnowledgeBasesParams) (int64, error) {
	items, err := s.ListIndustryKnowledgeBases(ctx, sqlc.ListIndustryKnowledgeBasesParams{Keyword: arg.Keyword})
	return int64(len(items)), err
}

func (s *fakeKnowledgeStore) RenameIndustryKnowledgeBase(_ context.Context, arg sqlc.RenameIndustryKnowledgeBaseParams) error {
	row, ok := s.industryBases[arg.ID]
	if !ok || row.DeletedAt.Valid {
		return sql.ErrNoRows
	}
	row.Name = arg.Name
	row.UpdatedAt = s.now
	s.industryBases[arg.ID] = row
	return nil
}

func (s *fakeKnowledgeStore) SoftDeleteIndustryKnowledgeBase(_ context.Context, id string) (int64, error) {
	row, ok := s.industryBases[id]
	if !ok {
		return 0, sql.ErrNoRows
	}
	if s.softDeleteIndustryAffected == 0 {
		return 0, nil
	}
	row.DeletedAt = null.TimeFrom(s.now)
	row.UpdatedAt = s.now
	s.industryBases[id] = row
	s.deletedIndustryBaseID = id
	return s.softDeleteIndustryAffected, nil
}

func (s *fakeKnowledgeStore) CountAssistantVersionsUsingIndustryKnowledgeBase(_ context.Context, _ string) (int64, error) {
	return s.industryInUseCount, nil
}

func (s *fakeKnowledgeStore) ListIndustryKnowledgeBasesByAssistantVersion(_ context.Context, versionID string) ([]sqlc.IndustryKnowledgeBasis, error) {
	return append([]sqlc.IndustryKnowledgeBasis(nil), s.versionIndustryBases[versionID]...), nil
}

func (s *fakeKnowledgeStore) GetRAGFlowOrgDataset(_ context.Context, orgID null.String) (sqlc.RagflowDataset, error) {
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
	if orgID.String != testKnowledgeOrg {
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

func (s *fakeKnowledgeStore) GetRAGFlowIndustryDataset(_ context.Context, industryKnowledgeBaseID null.String) (sqlc.RagflowDataset, error) {
	if s.missingIndustryDataset {
		return sqlc.RagflowDataset{}, sql.ErrNoRows
	}
	if row, ok := s.industryDatasets[industryKnowledgeBaseID.String]; ok {
		return row, nil
	}
	if s.industryDataset.IndustryKnowledgeBaseID.String == industryKnowledgeBaseID.String {
		return s.industryDataset, nil
	}
	return sqlc.RagflowDataset{}, sql.ErrNoRows
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
		OrgID:            strOrEmpty(arg.OrgID),
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
		OrgID:            strOrEmpty(arg.OrgID),
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

func (s *fakeKnowledgeStore) CreateRAGFlowIndustryDatasetMapping(_ context.Context, arg sqlc.CreateRAGFlowIndustryDatasetMappingParams) error {
	call := createdDatasetCall{
		ScopeType:        "industry",
		Name:             arg.Name,
		Status:           "creating",
		CreateClaimToken: arg.CreateClaimToken.String,
		ID:               arg.ID,
	}
	s.createdDatasets = append(s.createdDatasets, call)
	row := sqlc.RagflowDataset{
		ID:                      arg.ID,
		ScopeType:               "industry",
		IndustryKnowledgeBaseID: arg.IndustryKnowledgeBaseID,
		Name:                    arg.Name,
		Status:                  "creating",
		CreateClaimToken:        arg.CreateClaimToken,
		UpdatedAt:               s.now,
	}
	s.industryDataset = row
	s.industryDatasets[arg.IndustryKnowledgeBaseID.String] = row
	s.missingIndustryDataset = false
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
	if industryID, industryDataset, ok := s.industryDatasetByID(arg.ID); ok {
		if industryDataset.Status == "failed" || (industryDataset.Status == "creating" && industryDataset.UpdatedAt.Before(staleBefore)) {
			industryDataset.Status = "creating"
			industryDataset.LastError = null.String{}
			industryDataset.CreateClaimToken = arg.CreateClaimToken
			industryDataset.UpdatedAt = s.now
			s.setIndustryDataset(industryID, industryDataset)
			return nil
		}
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
	if _, row, ok := s.industryDatasetByID(id); ok {
		return row, nil
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
		if industryID, industryDataset, ok := s.industryDatasetByID(arg.ID); ok {
			industryDataset.Status = "creating"
			industryDataset.CreateClaimToken = null.StringFrom("winner-claim-token")
			industryDataset.RagflowDatasetID = null.String{}
			industryDataset.UpdatedAt = time.Now()
			s.setIndustryDataset(industryID, industryDataset)
			return nil
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
	if industryID, industryDataset, ok := s.industryDatasetByID(arg.ID); ok {
		if industryDataset.CreateClaimToken.String != arg.CreateClaimToken.String {
			return sql.ErrNoRows
		}
		industryDataset.RagflowDatasetID = arg.RagflowDatasetID
		industryDataset.Name = arg.Name
		industryDataset.Status = "active"
		industryDataset.CreateClaimToken = null.String{}
		s.setIndustryDataset(industryID, industryDataset)
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
	if industryID, industryDataset, ok := s.industryDatasetByID(arg.ID); ok {
		if industryDataset.CreateClaimToken.String != arg.CreateClaimToken.String {
			return sql.ErrNoRows
		}
		industryDataset.Status = "failed"
		industryDataset.LastError = arg.LastError
		industryDataset.CreateClaimToken = null.String{}
		s.setIndustryDataset(industryID, industryDataset)
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

func (s *fakeKnowledgeStore) CreateRAGFlowIndustryDocument(_ context.Context, arg sqlc.CreateRAGFlowIndustryDocumentParams) error {
	s.createdIndustryDocs = append(s.createdIndustryDocs, arg)
	s.recordEvent("create-industry-doc:" + arg.RagflowDocumentID)
	id := arg.ID
	if arg.RagflowDocumentID == "remote-doc-1" {
		id = s.nextDocument
	}
	row := sqlc.RagflowDocument{
		ID:                      id,
		DatasetID:               arg.DatasetID,
		ScopeType:               "industry",
		OrgID:                   null.String{},
		AppID:                   null.String{},
		IndustryKnowledgeBaseID: arg.IndustryKnowledgeBaseID,
		RagflowDocumentID:       arg.RagflowDocumentID,
		Name:                    arg.Name,
		SizeBytes:               arg.SizeBytes,
		MimeType:                arg.MimeType,
		Suffix:                  arg.Suffix,
		ParseStatus:             arg.ParseStatus,
		Progress:                arg.Progress,
		LastError:               arg.LastError,
		CreatedBy:               arg.CreatedBy,
		CreatedAt:               s.now,
		UpdatedAt:               s.now,
	}
	s.docs[arg.ID] = row
	if id != arg.ID {
		s.docs[id] = row
	}
	return nil
}

func (s *fakeKnowledgeStore) ReplaceRAGFlowIndustryDocument(_ context.Context, arg sqlc.ReplaceRAGFlowIndustryDocumentParams) error {
	if s.replaceIndustryDocumentErr != nil {
		return s.replaceIndustryDocumentErr
	}
	doc, ok := s.docs[arg.ID]
	if !ok {
		return nil
	}
	if s.replaceConcurrentRemoteID != "" {
		doc.RagflowDocumentID = s.replaceConcurrentRemoteID
		doc.UpdatedAt = s.now
		s.docs[arg.ID] = doc
		s.recordEvent("replace-industry-doc-concurrent:" + s.replaceConcurrentRemoteID)
		return nil
	}
	if doc.RagflowDocumentID != arg.OldRagflowDocumentID {
		return nil
	}
	doc.DatasetID = arg.DatasetID
	doc.RagflowDocumentID = arg.RagflowDocumentID
	doc.Name = arg.Name
	doc.SizeBytes = arg.SizeBytes
	doc.MimeType = arg.MimeType
	doc.Suffix = arg.Suffix
	doc.ParseStatus = arg.ParseStatus
	doc.Progress = arg.Progress
	doc.LastError = arg.LastError
	doc.CreatedBy = arg.CreatedBy
	doc.UpdatedAt = s.now
	s.docs[arg.ID] = doc
	s.recordEvent("replace-industry-doc:" + arg.RagflowDocumentID)
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

// ListAllRAGFlowDocumentsByScope 模拟整库清空时不带分页和筛选条件读取全部企业/实例文件。
func (s *fakeKnowledgeStore) ListAllRAGFlowDocumentsByScope(ctx context.Context, arg sqlc.ListAllRAGFlowDocumentsByScopeParams) ([]sqlc.RagflowDocument, error) {
	return s.ListRAGFlowDocumentsByScope(ctx, sqlc.ListRAGFlowDocumentsByScopeParams{
		ScopeType: arg.ScopeType,
		OrgID:     arg.OrgID,
		AppID:     arg.AppID,
	})
}

func (s *fakeKnowledgeStore) ListRAGFlowIndustryDocuments(_ context.Context, arg sqlc.ListRAGFlowIndustryDocumentsParams) ([]sqlc.RagflowDocument, error) {
	items := make([]sqlc.RagflowDocument, 0, len(s.docs))
	for _, doc := range s.docs {
		if doc.ScopeType != "industry" || doc.IndustryKnowledgeBaseID.String != arg.IndustryKnowledgeBaseID.String {
			continue
		}
		if arg.ParseStatus.Valid && doc.ParseStatus != arg.ParseStatus.String {
			continue
		}
		if keyword, ok := arg.Keywords.(string); ok && keyword != "" && !strings.Contains(doc.Name, keyword) {
			continue
		}
		if arg.CreatedFrom.Valid && doc.CreatedAt.Before(arg.CreatedFrom.Time) {
			continue
		}
		if arg.CreatedBefore.Valid && !doc.CreatedAt.Before(arg.CreatedBefore.Time) {
			continue
		}
		items = append(items, doc)
	}
	return items, nil
}

func (s *fakeKnowledgeStore) CountRAGFlowIndustryDocuments(ctx context.Context, arg sqlc.CountRAGFlowIndustryDocumentsParams) (int64, error) {
	items, err := s.ListRAGFlowIndustryDocuments(ctx, sqlc.ListRAGFlowIndustryDocumentsParams{
		IndustryKnowledgeBaseID: arg.IndustryKnowledgeBaseID,
		ParseStatus:             arg.ParseStatus,
		Keywords:                arg.Keywords,
		CreatedFrom:             arg.CreatedFrom,
		CreatedBefore:           arg.CreatedBefore,
	})
	return int64(len(items)), err
}

// ListAllRAGFlowIndustryDocuments 模拟整库清空时不受分页、日期和解析状态筛选影响。
func (s *fakeKnowledgeStore) ListAllRAGFlowIndustryDocuments(ctx context.Context, industryKnowledgeBaseID null.String) ([]sqlc.RagflowDocument, error) {
	return s.ListRAGFlowIndustryDocuments(ctx, sqlc.ListRAGFlowIndustryDocumentsParams{
		IndustryKnowledgeBaseID: industryKnowledgeBaseID,
	})
}

func (s *fakeKnowledgeStore) GetRAGFlowIndustryDocumentByName(_ context.Context, arg sqlc.GetRAGFlowIndustryDocumentByNameParams) (sqlc.RagflowDocument, error) {
	for _, doc := range s.docs {
		if doc.ScopeType == "industry" && doc.IndustryKnowledgeBaseID.String == arg.IndustryKnowledgeBaseID.String && doc.Name == arg.Name {
			return doc, nil
		}
	}
	return sqlc.RagflowDocument{}, sql.ErrNoRows
}

// SumRAGFlowDocumentsSizeByScope 汇总本地 document 大小，模拟数据库按 scope/org/app 聚合容量。
func (s *fakeKnowledgeStore) SumRAGFlowDocumentsSizeByScope(_ context.Context, arg sqlc.SumRAGFlowDocumentsSizeByScopeParams) (int64, error) {
	var total int64
	for _, doc := range s.docs {
		if doc.ScopeType != arg.ScopeType || doc.OrgID != arg.OrgID {
			continue
		}
		if arg.AppID.Valid && doc.AppID.String != arg.AppID.String {
			continue
		}
		if !arg.AppID.Valid && doc.AppID.Valid {
			continue
		}
		total += doc.SizeBytes
	}
	return total, nil
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

// ResetRAGFlowDocumentsParseStatusByDataset 模拟整库模型切换后批量重置本地解析状态。
func (s *fakeKnowledgeStore) ResetRAGFlowDocumentsParseStatusByDataset(_ context.Context, datasetID string) error {
	for id, doc := range s.docs {
		if doc.DatasetID != datasetID {
			continue
		}
		doc.ParseStatus = "queued"
		doc.Progress = 0
		doc.LastError = null.String{}
		doc.UpdatedAt = s.now
		s.docs[id] = doc
	}
	return nil
}

func (s *fakeKnowledgeStore) DeleteRAGFlowDocumentMapping(_ context.Context, id string) error {
	delete(s.docs, id)
	for key, doc := range s.docs {
		if doc.ID == id {
			delete(s.docs, key)
		}
	}
	return nil
}

func (s *fakeKnowledgeStore) DeleteRAGFlowDatasetMapping(_ context.Context, id string) error {
	if s.deleteDatasetMappingErr != nil {
		return s.deleteDatasetMappingErr
	}
	s.deletedDatasetID = id
	if industryID, _, ok := s.industryDatasetByID(id); ok {
		delete(s.industryDatasets, industryID)
		if s.industryDataset.ID == id {
			s.industryDataset = sqlc.RagflowDataset{}
		}
	}
	return nil
}

func (s *fakeKnowledgeStore) industryDatasetByID(id string) (string, sqlc.RagflowDataset, bool) {
	for industryID, row := range s.industryDatasets {
		if row.ID == id {
			return industryID, row, true
		}
	}
	if s.industryDataset.ID == id && s.industryDataset.IndustryKnowledgeBaseID.Valid {
		return s.industryDataset.IndustryKnowledgeBaseID.String, s.industryDataset, true
	}
	return "", sqlc.RagflowDataset{}, false
}

func (s *fakeKnowledgeStore) setIndustryDataset(industryID string, row sqlc.RagflowDataset) {
	s.industryDatasets[industryID] = row
	if s.industryDataset.ID == row.ID || s.industryDataset.ID == "" || industryID == testIndustryKnowledgeBaseID {
		s.industryDataset = row
	}
}

func (s *fakeKnowledgeStore) recordEvent(event string) {
	if s.events != nil {
		*s.events = append(*s.events, event)
	}
}

func (s *fakeKnowledgeStore) recordedEvents() []string {
	if s.events == nil {
		return nil
	}
	return append([]string(nil), (*s.events)...)
}

type fakeRAGFlowKnowledgeClient struct {
	createDatasetResult       ragflow.Dataset
	createDatasetCalls        []ragflowCreateDatasetCall
	datasetDetail             ragflow.Dataset
	deleteDatasetCalls        [][]string
	updateEmbeddingModelErr   error
	updateEmbeddingModelCalls []ragflowUpdateEmbeddingModelCall
	runDatasetEmbeddingErr    error
	runDatasetEmbeddingCalls  []string
	uploadDocument            ragflow.Document
	uploadErr                 error
	uploadCalls               []ragflowUploadCall
	parseErr                  error
	parseCalls                []ragflowParseCall
	deleteCalls               []ragflowDeleteCall
	retrieveDatasetIDs        []string
	retrieveQuestion          string
	retrieveTopK              int32
	retrieveChunks            []ragflow.RetrievalChunk
	retrieveChunksByDataset   map[string][]ragflow.RetrievalChunk
	retrieveErrorsByDataset   map[string]error
	retrieveCalls             []ragflowRetrieveCall
	listDocuments             []ragflow.Document
	listDocumentsCalls        int
	events                    *[]string
}

type ragflowCreateDatasetCall struct {
	name           string
	chunkMethod    string
	embeddingModel string
}

type ragflowUpdateEmbeddingModelCall struct {
	datasetID      string
	embeddingModel string
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

func (f *fakeRAGFlowKnowledgeClient) CreateDataset(_ context.Context, req ragflow.CreateDatasetRequest) (ragflow.Dataset, error) {
	f.createDatasetCalls = append(f.createDatasetCalls, ragflowCreateDatasetCall{
		name:           req.Name,
		chunkMethod:    req.ChunkMethod,
		embeddingModel: req.EmbeddingModel,
	})
	if f.createDatasetResult.ID == "" {
		return ragflow.Dataset{ID: "created-ds", Name: req.Name}, nil
	}
	return f.createDatasetResult, nil
}

func (f *fakeRAGFlowKnowledgeClient) GetDataset(_ context.Context, datasetID string) (ragflow.Dataset, error) {
	if f.datasetDetail.ID == "" {
		return ragflow.Dataset{ID: datasetID}, nil
	}
	return f.datasetDetail, nil
}

func (f *fakeRAGFlowKnowledgeClient) UpdateDatasetEmbeddingModel(_ context.Context, datasetID, embeddingModel string) error {
	f.updateEmbeddingModelCalls = append(f.updateEmbeddingModelCalls, ragflowUpdateEmbeddingModelCall{datasetID: datasetID, embeddingModel: embeddingModel})
	if f.updateEmbeddingModelErr != nil {
		return f.updateEmbeddingModelErr
	}
	return nil
}

func (f *fakeRAGFlowKnowledgeClient) RunDatasetEmbedding(_ context.Context, datasetID string) error {
	f.runDatasetEmbeddingCalls = append(f.runDatasetEmbeddingCalls, datasetID)
	if f.runDatasetEmbeddingErr != nil {
		return f.runDatasetEmbeddingErr
	}
	return nil
}

func (f *fakeRAGFlowKnowledgeClient) DeleteDatasets(_ context.Context, ids []string) error {
	f.deleteDatasetCalls = append(f.deleteDatasetCalls, append([]string(nil), ids...))
	return nil
}

func (f *fakeRAGFlowKnowledgeClient) UploadDocument(_ context.Context, datasetID, filename string, body io.Reader) (ragflow.Document, error) {
	content, _ := io.ReadAll(body)
	f.uploadCalls = append(f.uploadCalls, ragflowUploadCall{datasetID: datasetID, filename: filename, body: string(content)})
	if f.uploadErr != nil {
		return ragflow.Document{}, f.uploadErr
	}
	doc := f.uploadDocument
	doc.Name = filename
	f.recordEvent("upload:" + doc.ID)
	return doc, nil
}

func (f *fakeRAGFlowKnowledgeClient) DownloadDocument(_ context.Context, _, _ string) (io.ReadCloser, int64, error) {
	return io.NopCloser(strings.NewReader("content")), 7, nil
}

func (f *fakeRAGFlowKnowledgeClient) DeleteDocuments(_ context.Context, datasetID string, documentIDs []string) error {
	f.deleteCalls = append(f.deleteCalls, ragflowDeleteCall{datasetID: datasetID, documentIDs: append([]string(nil), documentIDs...)})
	f.recordEvent("delete-docs:" + strings.Join(documentIDs, ","))
	return nil
}

func (f *fakeRAGFlowKnowledgeClient) ParseDocuments(_ context.Context, datasetID string, documentIDs []string) error {
	f.parseCalls = append(f.parseCalls, ragflowParseCall{datasetID: datasetID, documentIDs: append([]string(nil), documentIDs...)})
	f.recordEvent("parse:" + strings.Join(documentIDs, ","))
	if f.parseErr != nil {
		return f.parseErr
	}
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

func (f *fakeRAGFlowKnowledgeClient) recordEvent(event string) {
	if f.events != nil {
		*f.events = append(*f.events, event)
	}
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
		ID:        mustParseUUID(testKnowledgeDocument),
		DatasetID: datasetID,
		ScopeType: scope,
		OrgID:     null.StringFrom(orgID),
		AppID: null.StringFromPtr(func() *string {
			if appID == "" {
				return nil
			}
			return &appID
		}()),
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

// industryTestDocument 构建行业知识库旧文件记录，确保行业库 ID 与 dataset 归属一致。
func industryTestDocument(t *testing.T, name, datasetID, industryID string) sqlc.RagflowDocument {
	t.Helper()
	doc := testDocument(t, "industry", name, datasetID)
	doc.OrgID = null.String{}
	doc.AppID = null.String{}
	doc.IndustryKnowledgeBaseID = null.StringFrom(industryID)
	return doc
}

func orgKnowledgeAdmin() auth.Principal {
	return auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testKnowledgeOrg, UserID: "admin"}
}

func appOwnerPrincipal() auth.Principal {
	return auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}
}

func platformKnowledgePrincipal() auth.Principal {
	return auth.Principal{Role: domain.UserRolePlatformAdmin, UserID: "platform-admin"}
}

// makeKnowledgeDoc 构建指定 dataset 下的 document 行，便于批量重置测试覆盖不同解析状态。
func makeKnowledgeDoc(id, datasetID, remoteID, parseStatus string, progress int32) sqlc.RagflowDocument {
	return sqlc.RagflowDocument{
		ID:                id,
		DatasetID:         datasetID,
		ScopeType:         "org",
		OrgID:             null.StringFrom(testKnowledgeOrg),
		RagflowDocumentID: remoteID,
		Name:              id + ".md",
		SizeBytes:         12,
		ParseStatus:       parseStatus,
		Progress:          progress,
		LastError:         null.StringFrom("previous error"),
		CreatedBy:         testKnowledgeOwner,
		CreatedAt:         time.Date(2026, 5, 26, 8, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, 5, 26, 8, 0, 0, 0, time.UTC),
	}
}

// industryDataset 构造行业库 dataset 测试行，避免每个 runtime 检索用例重复拼 null 字段。
func industryDataset(industryID, remoteID string) sqlc.RagflowDataset {
	return sqlc.RagflowDataset{
		ID:                      newUUID(),
		ScopeType:               "industry",
		IndustryKnowledgeBaseID: null.StringFrom(industryID),
		RagflowDatasetID:        null.StringFrom(remoteID),
		Name:                    "industry-" + industryID,
		Status:                  "active",
		UpdatedAt:               time.Now(),
	}
}
