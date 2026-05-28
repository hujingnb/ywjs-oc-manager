package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/ragflow"
	"oc-manager/internal/store/sqlc"
)

const (
	// runtimeKnowledgeDefaultTopK 与 Hermes oc-kb 默认值保持一致，避免 RAGFlow 使用 1024 的服务端默认值。
	runtimeKnowledgeDefaultTopK int32 = 8
	// runtimeKnowledgeMaxTopK 是单个作用域的检索上限；runtime 检索会分别查询 app 与 org 两个作用域。
	runtimeKnowledgeMaxTopK int32 = 50
	// ragflowDatasetCreateClaimTimeout 是创建租约的保守超时，避免进程崩溃后 creating 状态永久卡住。
	ragflowDatasetCreateClaimTimeout = 15 * time.Minute
)

// RAGFlowKnowledgeClient 是 service 层依赖的 RAGFlow 最小能力集。
// 这里不暴露 RAGFlow 凭证、dataset 选择权或权限字段，调用方只能传 service 解析后的 dataset ID。
type RAGFlowKnowledgeClient interface {
	CreateDataset(ctx context.Context, name, chunkMethod string) (ragflow.Dataset, error)
	DeleteDatasets(ctx context.Context, ids []string) error
	UploadDocument(ctx context.Context, datasetID, filename string, body io.Reader) (ragflow.Document, error)
	DownloadDocument(ctx context.Context, datasetID, documentID string) (io.ReadCloser, int64, error)
	DeleteDocuments(ctx context.Context, datasetID string, documentIDs []string) error
	ParseDocuments(ctx context.Context, datasetID string, documentIDs []string) error
	ListDocuments(ctx context.Context, datasetID string, page, pageSize int32, keywords, run string) ([]ragflow.Document, int32, error)
	Retrieve(ctx context.Context, datasetIDs []string, question string, topK int32) ([]ragflow.RetrievalChunk, error)
}

// KnowledgeStore 是知识库 service 所需的数据库查询子集。
// 所有 org/app/runtime 权限和 dataset 解析都在 manager 内完成，RAGFlow 不参与业务鉴权。
type KnowledgeStore interface {
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	GetApp(ctx context.Context, id string) (sqlc.App, error)
	GetAppByRuntimeTokenHash(ctx context.Context, runtimeTokenHash null.String) (sqlc.App, error)
	// GetRAGFlowOrgDataset 按组织 ID（string）查询 org 级 dataset 映射。
	GetRAGFlowOrgDataset(ctx context.Context, orgID string) (sqlc.RagflowDataset, error)
	// GetRAGFlowAppDataset 按应用 ID（null.String，允许空值）查询 app 级 dataset 映射。
	GetRAGFlowAppDataset(ctx context.Context, appID null.String) (sqlc.RagflowDataset, error)
	// CreateRAGFlowOrgDatasetMapping 创建 org dataset 映射（:exec），写入后通过 GetRAGFlowOrgDataset 读回。
	CreateRAGFlowOrgDatasetMapping(ctx context.Context, arg sqlc.CreateRAGFlowOrgDatasetMappingParams) error
	// CreateRAGFlowAppDatasetMapping 创建 app dataset 映射（:exec），写入后通过 GetRAGFlowAppDataset 读回。
	CreateRAGFlowAppDatasetMapping(ctx context.Context, arg sqlc.CreateRAGFlowAppDatasetMappingParams) error
	// ClaimRAGFlowDatasetCreation 抢占创建租约（:exec），写入后通过 GetRAGFlowDataset 读回。
	ClaimRAGFlowDatasetCreation(ctx context.Context, arg sqlc.ClaimRAGFlowDatasetCreationParams) error
	GetRAGFlowDataset(ctx context.Context, id string) (sqlc.RagflowDataset, error)
	// SetRAGFlowDatasetActive 设置 dataset 为 active（:exec），写入后通过 GetRAGFlowDataset 读回。
	SetRAGFlowDatasetActive(ctx context.Context, arg sqlc.SetRAGFlowDatasetActiveParams) error
	// MarkRAGFlowDatasetFailed 标记 dataset 失败（:exec）。
	MarkRAGFlowDatasetFailed(ctx context.Context, arg sqlc.MarkRAGFlowDatasetFailedParams) error
	// CreateRAGFlowDocument 保存 document 元数据（:exec），写入后通过 GetRAGFlowDocument 读回。
	CreateRAGFlowDocument(ctx context.Context, arg sqlc.CreateRAGFlowDocumentParams) error
	ListRAGFlowDocumentsByScope(ctx context.Context, arg sqlc.ListRAGFlowDocumentsByScopeParams) ([]sqlc.RagflowDocument, error)
	CountRAGFlowDocumentsByScope(ctx context.Context, arg sqlc.CountRAGFlowDocumentsByScopeParams) (int64, error)
	GetRAGFlowDocument(ctx context.Context, id string) (sqlc.RagflowDocument, error)
	// UpdateRAGFlowDocumentParseStatus 更新解析状态（:exec），写入后通过 GetRAGFlowDocument 读回。
	UpdateRAGFlowDocumentParseStatus(ctx context.Context, arg sqlc.UpdateRAGFlowDocumentParseStatusParams) error
	DeleteRAGFlowDocumentMapping(ctx context.Context, id string) error
	DeleteRAGFlowDatasetMapping(ctx context.Context, id string) error
}

// KnowledgeDatasetProvisioner 抽象组织 / 实例生命周期中预创建 RAGFlow dataset 的能力。
type KnowledgeDatasetProvisioner interface {
	EnsureOrgDataset(ctx context.Context, org sqlc.Organization) (sqlc.RagflowDataset, error)
	EnsureAppDataset(ctx context.Context, app sqlc.App) (sqlc.RagflowDataset, error)
}

// KnowledgeService 以 RAGFlow 作为唯一文件主库，对外提供 manager 权限控制后的知识库能力。
type KnowledgeService struct {
	store              KnowledgeStore
	ragflow            RAGFlowKnowledgeClient
	datasetChunkMethod string
}

// NewKnowledgeService 创建 RAGFlow-backed 知识库服务。
func NewKnowledgeService(store KnowledgeStore, client RAGFlowKnowledgeClient) *KnowledgeService {
	return &KnowledgeService{store: store, ragflow: client, datasetChunkMethod: "naive"}
}

// SetDatasetChunkMethod 设置自动创建 RAGFlow dataset 时使用的分块方法。
func (s *KnowledgeService) SetDatasetChunkMethod(method string) {
	method = strings.TrimSpace(method)
	if method == "" {
		method = "naive"
	}
	s.datasetChunkMethod = method
}

// KnowledgeListResult 是扁平文档列表接口的返回。
type KnowledgeListResult struct {
	Items []KnowledgeDocumentResult `json:"items"`
	Total int64                     `json:"total"`
}

// KnowledgeDocumentResult 是 manager 对 RAGFlow document 元数据的用户侧视图。
type KnowledgeDocumentResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	MimeType    string `json:"mime_type,omitempty"`
	Suffix      string `json:"suffix,omitempty"`
	ParseStatus string `json:"parse_status"`
	Progress    int32  `json:"progress"`
	LastError   string `json:"last_error,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// KnowledgeSearchResult 是 Hermes runtime 检索 API 的返回。
type KnowledgeSearchResult struct {
	Results []KnowledgeSearchHit `json:"results"`
}

// KnowledgeSearchHit 是一次 retrieval 命中的文本块。
type KnowledgeSearchHit struct {
	Scope        string  `json:"scope"`
	DocumentID   string  `json:"document_id"`
	DocumentName string  `json:"document_name"`
	Content      string  `json:"content"`
	Similarity   float64 `json:"similarity"`
}

// ListOrg 列出企业知识库文件。企业成员只读，企业管理员可写。
func (s *KnowledgeService) ListOrg(ctx context.Context, principal auth.Principal, orgID string, page, pageSize int32, keyword, status string) (KnowledgeListResult, error) {
	if !auth.CanReadOrgKnowledge(principal, orgID) {
		return KnowledgeListResult{}, ErrKnowledgeForbidden
	}
	// orgID 直接作为字符串传入，不再解析为 UUID 类型。
	dataset, err := s.getOrgDataset(ctx, orgID)
	if err != nil {
		return KnowledgeListResult{}, err
	}
	return s.listDocuments(ctx, dataset, "org", dataset.OrgID, "", page, pageSize, keyword, status)
}

// SaveOrgFile 上传企业知识库文件并触发解析。
func (s *KnowledgeService) SaveOrgFile(ctx context.Context, principal auth.Principal, orgID, filename string, content io.Reader, size int64) (KnowledgeDocumentResult, error) {
	if !auth.CanWriteOrgKnowledge(principal, orgID) {
		return KnowledgeDocumentResult{}, ErrKnowledgeForbidden
	}
	dataset, err := s.getOrgDataset(ctx, orgID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return s.uploadToDataset(ctx, dataset, "", principal.UserID, filename, content, size)
}

// OpenOrgFile 打开企业知识库文件流供下载。
func (s *KnowledgeService) OpenOrgFile(ctx context.Context, principal auth.Principal, orgID, documentID string) (io.ReadCloser, int64, string, error) {
	if !auth.CanReadOrgKnowledge(principal, orgID) {
		return nil, 0, "", ErrKnowledgeForbidden
	}
	// getDocumentForScope 中 appID 为空字符串表示组织级别文件。
	document, dataset, err := s.getDocumentForScope(ctx, documentID, "org", orgID, "")
	if err != nil {
		return nil, 0, "", err
	}
	return s.openDocument(ctx, dataset, document)
}

// DeleteOrgFile 删除企业知识库文件。
func (s *KnowledgeService) DeleteOrgFile(ctx context.Context, principal auth.Principal, orgID, documentID string) error {
	if !auth.CanWriteOrgKnowledge(principal, orgID) {
		return ErrKnowledgeForbidden
	}
	document, dataset, err := s.getDocumentForScope(ctx, documentID, "org", orgID, "")
	if err != nil {
		return err
	}
	return s.deleteDocument(ctx, dataset, document)
}

// ReparseOrgFile 重新触发企业知识库文件解析。
func (s *KnowledgeService) ReparseOrgFile(ctx context.Context, principal auth.Principal, orgID, documentID string) (KnowledgeDocumentResult, error) {
	if !auth.CanWriteOrgKnowledge(principal, orgID) {
		return KnowledgeDocumentResult{}, ErrKnowledgeForbidden
	}
	document, dataset, err := s.getDocumentForScope(ctx, documentID, "org", orgID, "")
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return s.reparseDocument(ctx, dataset, document)
}

// ListApp 列出实例知识库文件。权限由 app 的真实 owner/org 决定，不信任请求方传入归属。
func (s *KnowledgeService) ListApp(ctx context.Context, principal auth.Principal, appID string, page, pageSize int32, keyword, status string) (KnowledgeListResult, error) {
	app, err := s.getApp(ctx, appID)
	if err != nil {
		return KnowledgeListResult{}, err
	}
	if !auth.CanReadAppKnowledge(principal, app.OrgID, app.OwnerUserID) {
		return KnowledgeListResult{}, ErrKnowledgeForbidden
	}
	dataset, err := s.getAppDataset(ctx, app.ID)
	if err != nil {
		return KnowledgeListResult{}, err
	}
	// dataset.AppID 是 null.String；取其字符串值（如 NULL 则传空字符串）。
	return s.listDocuments(ctx, dataset, "app", app.OrgID, strOrEmpty(dataset.AppID), page, pageSize, keyword, status)
}

// SaveAppFile 上传实例知识库文件并触发解析。
func (s *KnowledgeService) SaveAppFile(ctx context.Context, principal auth.Principal, appID, filename string, content io.Reader, size int64) (KnowledgeDocumentResult, error) {
	app, err := s.getApp(ctx, appID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	if !auth.CanWriteAppKnowledge(principal, app.OrgID, app.OwnerUserID) {
		return KnowledgeDocumentResult{}, ErrKnowledgeForbidden
	}
	dataset, err := s.getAppDataset(ctx, app.ID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return s.uploadToDataset(ctx, dataset, app.ID, principal.UserID, filename, content, size)
}

// OpenAppFile 打开实例知识库文件流供下载。
func (s *KnowledgeService) OpenAppFile(ctx context.Context, principal auth.Principal, appID, documentID string) (io.ReadCloser, int64, string, error) {
	app, err := s.getApp(ctx, appID)
	if err != nil {
		return nil, 0, "", err
	}
	if !auth.CanReadAppKnowledge(principal, app.OrgID, app.OwnerUserID) {
		return nil, 0, "", ErrKnowledgeForbidden
	}
	document, dataset, err := s.getDocumentForScope(ctx, documentID, "app", app.OrgID, app.ID)
	if err != nil {
		return nil, 0, "", err
	}
	return s.openDocument(ctx, dataset, document)
}

// DeleteAppFile 删除实例知识库文件。
func (s *KnowledgeService) DeleteAppFile(ctx context.Context, principal auth.Principal, appID, documentID string) error {
	app, err := s.getApp(ctx, appID)
	if err != nil {
		return err
	}
	if !auth.CanWriteAppKnowledge(principal, app.OrgID, app.OwnerUserID) {
		return ErrKnowledgeForbidden
	}
	document, dataset, err := s.getDocumentForScope(ctx, documentID, "app", app.OrgID, app.ID)
	if err != nil {
		return err
	}
	return s.deleteDocument(ctx, dataset, document)
}

// ReparseAppFile 重新触发实例知识库文件解析。
func (s *KnowledgeService) ReparseAppFile(ctx context.Context, principal auth.Principal, appID, documentID string) (KnowledgeDocumentResult, error) {
	app, err := s.getApp(ctx, appID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	if !auth.CanWriteAppKnowledge(principal, app.OrgID, app.OwnerUserID) {
		return KnowledgeDocumentResult{}, ErrKnowledgeForbidden
	}
	document, dataset, err := s.getDocumentForScope(ctx, documentID, "app", app.OrgID, app.ID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return s.reparseDocument(ctx, dataset, document)
}

// RuntimeSearch 供 Hermes 以 app runtime token 检索当前实例与所属企业知识库。
// 请求体中的任何 dataset / org / app 意图都不会传入本方法，dataset 只由 token 解析出的 app 决定。
func (s *KnowledgeService) RuntimeSearch(ctx context.Context, appToken, question string, topK int32) (KnowledgeSearchResult, error) {
	if strings.TrimSpace(question) == "" {
		return KnowledgeSearchResult{}, fmt.Errorf("question 不能为空")
	}
	app, err := s.appByRuntimeToken(ctx, appToken)
	if err != nil {
		return KnowledgeSearchResult{}, err
	}
	appDataset, err := s.getAppDataset(ctx, app.ID)
	if err != nil {
		return KnowledgeSearchResult{}, err
	}
	orgDataset, err := s.getOrgDataset(ctx, app.OrgID)
	if err != nil {
		return KnowledgeSearchResult{}, err
	}
	appRemoteID, err := requireRemoteDatasetID(appDataset)
	if err != nil {
		return KnowledgeSearchResult{}, err
	}
	orgRemoteID, err := requireRemoteDatasetID(orgDataset)
	if err != nil {
		return KnowledgeSearchResult{}, err
	}
	topK = normalizeRuntimeTopK(topK)
	// 两路检索避免 RAGFlow 在 top_k 截断时让企业知识库命中挤占实例知识库命中。
	appChunks, err := s.ragflowClient().Retrieve(ctx, []string{appRemoteID}, question, topK)
	if err != nil {
		return KnowledgeSearchResult{}, fmt.Errorf("RAGFlow 检索实例知识库失败: %w", err)
	}
	orgChunks, err := s.ragflowClient().Retrieve(ctx, []string{orgRemoteID}, question, topK)
	if err != nil {
		return KnowledgeSearchResult{}, fmt.Errorf("RAGFlow 检索企业知识库失败: %w", err)
	}
	hits := make([]KnowledgeSearchHit, 0, len(appChunks)+len(orgChunks))
	hits = append(hits, searchHitsFromChunks("app", appChunks)...)
	hits = append(hits, searchHitsFromChunks("org", orgChunks)...)
	return KnowledgeSearchResult{Results: hits}, nil
}

// RuntimeAddFile 供 Hermes 把工作目录中的报告写入当前实例知识库。
// 写入目标固定为当前 app dataset，组织 dataset 在写路径完全不可达。
func (s *KnowledgeService) RuntimeAddFile(ctx context.Context, appToken, filename string, content io.Reader, size int64) (KnowledgeDocumentResult, error) {
	app, err := s.appByRuntimeToken(ctx, appToken)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	dataset, err := s.getAppDataset(ctx, app.ID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return s.uploadToDataset(ctx, dataset, app.ID, "runtime:"+app.ID, filename, content, size)
}

// EnsureOrgDataset 确保企业知识库存在可用的 RAGFlow dataset。
func (s *KnowledgeService) EnsureOrgDataset(ctx context.Context, org sqlc.Organization) (sqlc.RagflowDataset, error) {
	name := buildRAGFlowDatasetName("org", org.Code, org.Name, org.ID)
	return s.ensureDataset(ctx, "org", org.ID, "", name)
}

// EnsureAppDataset 确保实例知识库存在可用的 RAGFlow dataset。
func (s *KnowledgeService) EnsureAppDataset(ctx context.Context, app sqlc.App) (sqlc.RagflowDataset, error) {
	name := buildRAGFlowDatasetName("app", app.Name, "", app.ID)
	return s.ensureDataset(ctx, "app", app.OrgID, app.ID, name)
}

// DeleteAppDataset 删除实例私有知识库的远端 RAGFlow dataset 和本地映射。
// app_delete 在软删 apps 行前调用本方法，避免应用不可见后留下无人管理的索引和原文件。
// appID 为普通字符串；store 接口 GetRAGFlowAppDataset 接收 null.String。
func (s *KnowledgeService) DeleteAppDataset(ctx context.Context, appID string) error {
	if s.store == nil {
		return nil
	}
	dataset, err := s.store.GetRAGFlowAppDataset(ctx, null.StringFrom(appID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("查询实例 RAGFlow dataset 失败: %w", err)
	}
	if remoteDatasetID, err := requireRemoteDatasetID(dataset); err == nil {
		if err := s.ragflowClient().DeleteDatasets(ctx, []string{remoteDatasetID}); err != nil {
			return fmt.Errorf("删除 RAGFlow app dataset 失败: %w", err)
		}
	}
	if err := s.store.DeleteRAGFlowDatasetMapping(ctx, dataset.ID); err != nil {
		return fmt.Errorf("删除实例 RAGFlow dataset 映射失败: %w", err)
	}
	return nil
}

func (s *KnowledgeService) listDocuments(ctx context.Context, dataset sqlc.RagflowDataset, scope string, orgID, appID string, page, pageSize int32, keyword, status string) (KnowledgeListResult, error) {
	page, pageSize = normalizePage(page, pageSize)
	// OrgID 为 string；AppID 为 null.String（空字符串表示组织级别查询，传 null.String{}）。
	appIDNull := null.String{}
	if appID != "" {
		appIDNull = null.StringFrom(appID)
	}
	statusNull := nullStr(strings.TrimSpace(status))
	// Keywords 在 sqlc 生成为 interface{}，传入 nil 表示不过滤；传字符串时 MySQL LIKE 生效。
	var kw interface{}
	if k := strings.TrimSpace(keyword); k != "" {
		kw = k
	}
	params := sqlc.ListRAGFlowDocumentsByScopeParams{
		ScopeType:   scope,
		OrgID:       orgID,
		AppID:       appIDNull,
		Limit:       pageSize,
		Offset:      (page - 1) * pageSize,
		ParseStatus: statusNull,
		Keywords:    kw,
	}
	items, err := s.store.ListRAGFlowDocumentsByScope(ctx, params)
	if err != nil {
		return KnowledgeListResult{}, fmt.Errorf("查询知识库文件列表失败: %w", err)
	}
	// 解析状态由后台任务 ragflow_parse_status_refresh 周期回写，
	// 列表请求只读本地缓存，避免每次拉列表都打 RAGFlow，且不会因 RAGFlow 临时不可用让列表 500。
	total, err := s.store.CountRAGFlowDocumentsByScope(ctx, sqlc.CountRAGFlowDocumentsByScopeParams{
		ScopeType:   params.ScopeType,
		OrgID:       params.OrgID,
		AppID:       params.AppID,
		ParseStatus: params.ParseStatus,
		Keywords:    params.Keywords,
	})
	if err != nil {
		return KnowledgeListResult{}, fmt.Errorf("统计知识库文件失败: %w", err)
	}
	results := make([]KnowledgeDocumentResult, 0, len(items))
	for _, item := range items {
		results = append(results, toKnowledgeDocumentResult(item))
	}
	return KnowledgeListResult{Items: results, Total: total}, nil
}

func (s *KnowledgeService) uploadToDataset(ctx context.Context, dataset sqlc.RagflowDataset, appID, createdBy, filename string, content io.Reader, size int64) (KnowledgeDocumentResult, error) {
	if filename = strings.TrimSpace(filename); filename == "" {
		return KnowledgeDocumentResult{}, fmt.Errorf("filename 不能为空")
	}
	remoteDatasetID, err := requireRemoteDatasetID(dataset)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	remote, err := s.ragflowClient().UploadDocument(ctx, remoteDatasetID, path.Base(filename), content)
	if err != nil {
		return KnowledgeDocumentResult{}, fmt.Errorf("上传 RAGFlow document 失败: %w", err)
	}
	if remote.Size > 0 {
		size = remote.Size
	}
	if createdBy == "" {
		createdBy = "unknown"
	}
	suffix := strings.TrimPrefix(path.Ext(filename), ".")
	mimeType := mime.TypeByExtension(path.Ext(filename))
	// CreateRAGFlowDocument 为 :exec；预先生成 ID，写入后通过 GetRAGFlowDocument 读回。
	docID := newUUID()
	// AppID 为 null.String；仅在 appID 非空时填充（org 级别上传时留 NULL）。
	appIDNull := null.String{}
	if appID != "" {
		appIDNull = null.StringFrom(appID)
	}
	arg := sqlc.CreateRAGFlowDocumentParams{
		ID:                docID,
		DatasetID:         dataset.ID,
		ScopeType:         dataset.ScopeType,
		OrgID:             dataset.OrgID,
		AppID:             appIDNull,
		RagflowDocumentID: remote.ID,
		Name:              path.Base(filename),
		SizeBytes:         size,
		MimeType:          nullStr(mimeType),
		Suffix:            nullStr(suffix),
		ParseStatus:       normalizeRAGFlowRun(remote.Run),
		Progress:          progressForStatus(normalizeRAGFlowRun(remote.Run)),
		CreatedBy:         createdBy,
	}
	if err := s.store.CreateRAGFlowDocument(ctx, arg); err != nil {
		return KnowledgeDocumentResult{}, fmt.Errorf("保存知识库文件元数据失败: %w", err)
	}
	row, err := s.store.GetRAGFlowDocument(ctx, docID)
	if err != nil {
		return KnowledgeDocumentResult{}, fmt.Errorf("读取新建知识库文件失败: %w", err)
	}
	if err := s.ragflowClient().ParseDocuments(ctx, remoteDatasetID, []string{remote.ID}); err != nil {
		return KnowledgeDocumentResult{}, fmt.Errorf("触发 RAGFlow 解析失败: %w", err)
	}
	if row.ParseStatus != "queued" && row.ParseStatus != "running" {
		// UpdateRAGFlowDocumentParseStatus 为 :exec；写入后重新读取。
		if err := s.store.UpdateRAGFlowDocumentParseStatus(ctx, sqlc.UpdateRAGFlowDocumentParseStatusParams{
			ID:          row.ID,
			ParseStatus: "queued",
			Progress:    0,
			LastError:   null.String{},
		}); err != nil {
			return KnowledgeDocumentResult{}, fmt.Errorf("更新知识库解析状态失败: %w", err)
		}
		row, err = s.store.GetRAGFlowDocument(ctx, row.ID)
		if err != nil {
			return KnowledgeDocumentResult{}, fmt.Errorf("读取更新后知识库文件失败: %w", err)
		}
	}
	return toKnowledgeDocumentResult(row), nil
}

func (s *KnowledgeService) openDocument(ctx context.Context, dataset sqlc.RagflowDataset, document sqlc.RagflowDocument) (io.ReadCloser, int64, string, error) {
	remoteDatasetID, err := requireRemoteDatasetID(dataset)
	if err != nil {
		return nil, 0, "", err
	}
	stream, size, err := s.ragflowClient().DownloadDocument(ctx, remoteDatasetID, document.RagflowDocumentID)
	if err != nil {
		return nil, 0, "", fmt.Errorf("下载 RAGFlow document 失败: %w", err)
	}
	return stream, size, document.Name, nil
}

func (s *KnowledgeService) deleteDocument(ctx context.Context, dataset sqlc.RagflowDataset, document sqlc.RagflowDocument) error {
	remoteDatasetID, err := requireRemoteDatasetID(dataset)
	if err != nil {
		return err
	}
	if err := s.ragflowClient().DeleteDocuments(ctx, remoteDatasetID, []string{document.RagflowDocumentID}); err != nil {
		return fmt.Errorf("删除 RAGFlow document 失败: %w", err)
	}
	if err := s.store.DeleteRAGFlowDocumentMapping(ctx, document.ID); err != nil {
		return fmt.Errorf("删除知识库文件元数据失败: %w", err)
	}
	return nil
}

func (s *KnowledgeService) reparseDocument(ctx context.Context, dataset sqlc.RagflowDataset, document sqlc.RagflowDocument) (KnowledgeDocumentResult, error) {
	if document.ParseStatus != "failed" && document.ParseStatus != "stopped" {
		return KnowledgeDocumentResult{}, fmt.Errorf("只有解析失败或已停止的文件可以重新解析")
	}
	remoteDatasetID, err := requireRemoteDatasetID(dataset)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	if err := s.ragflowClient().ParseDocuments(ctx, remoteDatasetID, []string{document.RagflowDocumentID}); err != nil {
		return KnowledgeDocumentResult{}, fmt.Errorf("触发 RAGFlow 重新解析失败: %w", err)
	}
	// UpdateRAGFlowDocumentParseStatus 为 :exec；写入后通过 GetRAGFlowDocument 读回。
	if err := s.store.UpdateRAGFlowDocumentParseStatus(ctx, sqlc.UpdateRAGFlowDocumentParseStatusParams{
		ID:          document.ID,
		ParseStatus: "queued",
		Progress:    0,
		LastError:   null.String{},
	}); err != nil {
		return KnowledgeDocumentResult{}, fmt.Errorf("更新知识库解析状态失败: %w", err)
	}
	row, err := s.store.GetRAGFlowDocument(ctx, document.ID)
	if err != nil {
		return KnowledgeDocumentResult{}, fmt.Errorf("读取重新解析后知识库文件失败: %w", err)
	}
	return toKnowledgeDocumentResult(row), nil
}

func searchHitsFromChunks(scope string, chunks []ragflow.RetrievalChunk) []KnowledgeSearchHit {
	hits := make([]KnowledgeSearchHit, 0, len(chunks))
	for _, chunk := range chunks {
		hits = append(hits, KnowledgeSearchHit{
			Scope:        scope,
			DocumentID:   chunk.DocumentID,
			DocumentName: chunk.DocumentName,
			Content:      chunk.Content,
			Similarity:   chunk.Similarity,
		})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		return hits[i].Similarity > hits[j].Similarity
	})
	return hits
}

func (s *KnowledgeService) getDocumentForScope(ctx context.Context, documentID, scope string, orgID, appID string) (sqlc.RagflowDocument, sqlc.RagflowDataset, error) {
	document, err := s.store.GetRAGFlowDocument(ctx, documentID)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlc.RagflowDocument{}, sqlc.RagflowDataset{}, ErrNotFound
	}
	if err != nil {
		return sqlc.RagflowDocument{}, sqlc.RagflowDataset{}, fmt.Errorf("查询知识库文件失败: %w", err)
	}
	if document.ScopeType != scope || document.OrgID != orgID {
		return sqlc.RagflowDocument{}, sqlc.RagflowDataset{}, ErrNotFound
	}
	if scope == "app" && strOrEmpty(document.AppID) != appID {
		return sqlc.RagflowDocument{}, sqlc.RagflowDataset{}, ErrNotFound
	}
	dataset, err := s.datasetByDocument(ctx, document)
	if err != nil {
		return sqlc.RagflowDocument{}, sqlc.RagflowDataset{}, err
	}
	return document, dataset, nil
}

func (s *KnowledgeService) datasetByDocument(ctx context.Context, document sqlc.RagflowDocument) (sqlc.RagflowDataset, error) {
	switch document.ScopeType {
	case "org":
		return s.getOrgDataset(ctx, document.OrgID)
	case "app":
		// AppID 是 null.String，取其字符串值传递给 getAppDataset。
		return s.getAppDataset(ctx, strOrEmpty(document.AppID))
	default:
		return sqlc.RagflowDataset{}, ErrNotFound
	}
}

func (s *KnowledgeService) getApp(ctx context.Context, appID string) (sqlc.App, error) {
	if s.store == nil {
		return sqlc.App{}, ErrKnowledgeMissing
	}
	// appID 直接作为字符串传入；不存在时 store 返回 sql.ErrNoRows。
	app, err := s.store.GetApp(ctx, appID)
	if errors.Is(err, sql.ErrNoRows) || app.DeletedAt.Valid {
		return sqlc.App{}, ErrNotFound
	}
	if err != nil {
		return sqlc.App{}, fmt.Errorf("查询应用失败: %w", err)
	}
	return app, nil
}

func (s *KnowledgeService) appByRuntimeToken(ctx context.Context, token string) (sqlc.App, error) {
	if strings.TrimSpace(token) == "" {
		return sqlc.App{}, ErrInvalidToken
	}
	hash := HashAppRuntimeToken(token)
	// GetAppByRuntimeTokenHash 接受 null.String。
	app, err := s.store.GetAppByRuntimeTokenHash(ctx, null.StringFrom(hash))
	if errors.Is(err, sql.ErrNoRows) || app.DeletedAt.Valid {
		return sqlc.App{}, ErrInvalidToken
	}
	if err != nil {
		return sqlc.App{}, fmt.Errorf("解析 runtime token 失败: %w", err)
	}
	return app, nil
}

func (s *KnowledgeService) getOrgDataset(ctx context.Context, orgID string) (sqlc.RagflowDataset, error) {
	if s.store == nil {
		return sqlc.RagflowDataset{}, ErrKnowledgeMissing
	}
	dataset, err := s.store.GetRAGFlowOrgDataset(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		org, orgErr := s.store.GetOrganization(ctx, orgID)
		if errors.Is(orgErr, sql.ErrNoRows) {
			return sqlc.RagflowDataset{}, ErrNotFound
		}
		if orgErr != nil {
			return sqlc.RagflowDataset{}, fmt.Errorf("查询企业失败: %w", orgErr)
		}
		return s.EnsureOrgDataset(ctx, org)
	}
	if err != nil {
		return sqlc.RagflowDataset{}, fmt.Errorf("查询企业 RAGFlow dataset 失败: %w", err)
	}
	if dataset.Status != "active" || !dataset.RagflowDatasetID.Valid || strings.TrimSpace(dataset.RagflowDatasetID.String) == "" {
		return s.ensureExistingDataset(ctx, dataset)
	}
	return dataset, nil
}

func (s *KnowledgeService) getAppDataset(ctx context.Context, appID string) (sqlc.RagflowDataset, error) {
	if s.store == nil {
		return sqlc.RagflowDataset{}, ErrKnowledgeMissing
	}
	// GetRAGFlowAppDataset 接受 null.String。
	dataset, err := s.store.GetRAGFlowAppDataset(ctx, null.StringFrom(appID))
	if errors.Is(err, sql.ErrNoRows) {
		app, appErr := s.store.GetApp(ctx, appID)
		if errors.Is(appErr, sql.ErrNoRows) {
			return sqlc.RagflowDataset{}, ErrNotFound
		}
		if appErr != nil {
			return sqlc.RagflowDataset{}, fmt.Errorf("查询实例失败: %w", appErr)
		}
		return s.EnsureAppDataset(ctx, app)
	}
	if err != nil {
		return sqlc.RagflowDataset{}, fmt.Errorf("查询实例 RAGFlow dataset 失败: %w", err)
	}
	if dataset.Status != "active" || !dataset.RagflowDatasetID.Valid || strings.TrimSpace(dataset.RagflowDatasetID.String) == "" {
		return s.ensureExistingDataset(ctx, dataset)
	}
	return dataset, nil
}

func (s *KnowledgeService) ensureDataset(ctx context.Context, scope string, orgID, appID string, name string) (sqlc.RagflowDataset, error) {
	if s.store == nil {
		return sqlc.RagflowDataset{}, ErrKnowledgeMissing
	}
	var (
		existing sqlc.RagflowDataset
		err      error
	)
	if scope == "org" {
		existing, err = s.store.GetRAGFlowOrgDataset(ctx, orgID)
	} else {
		existing, err = s.store.GetRAGFlowAppDataset(ctx, null.StringFrom(appID))
	}
	if err == nil {
		if existing.Status == "active" && existing.RagflowDatasetID.Valid && strings.TrimSpace(existing.RagflowDatasetID.String) != "" {
			return existing, nil
		}
		return s.ensureExistingDataset(ctx, existing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return sqlc.RagflowDataset{}, fmt.Errorf("查询 RAGFlow dataset 映射失败: %w", err)
	}
	claimToken, err := generateRAGFlowDatasetClaimToken()
	if err != nil {
		return sqlc.RagflowDataset{}, err
	}
	// CreateRAGFlowOrgDatasetMapping / CreateRAGFlowAppDatasetMapping 为 :exec；
	// 写入后按 orgID/appID 重新读回（并发冲突时也读回已有行）。
	newID := newUUID()
	if scope == "org" {
		err = s.store.CreateRAGFlowOrgDatasetMapping(ctx, sqlc.CreateRAGFlowOrgDatasetMappingParams{
			ID:               newID,
			OrgID:            orgID,
			Name:             name,
			CreateClaimToken: null.StringFrom(claimToken),
		})
	} else {
		err = s.store.CreateRAGFlowAppDatasetMapping(ctx, sqlc.CreateRAGFlowAppDatasetMappingParams{
			ID:               newID,
			OrgID:            orgID,
			AppID:            null.StringFrom(appID),
			Name:             name,
			CreateClaimToken: null.StringFrom(claimToken),
		})
	}
	// 并发唯一索引冲突时 MySQL 忽略写入（INSERT IGNORE），读回已有行继续处理。
	if err != nil {
		return sqlc.RagflowDataset{}, fmt.Errorf("创建 RAGFlow dataset 映射失败: %w", err)
	}
	// 读回刚写入（或并发已有）的行。
	dataset, err := s.readDatasetAfterCreate(ctx, scope, orgID, appID, newID)
	if err != nil {
		return sqlc.RagflowDataset{}, err
	}
	if dataset.Status == "active" && dataset.RagflowDatasetID.Valid && strings.TrimSpace(dataset.RagflowDatasetID.String) != "" {
		return dataset, nil
	}
	return s.createRemoteDataset(ctx, dataset, claimToken)
}

// readDatasetAfterCreate 在 INSERT IGNORE 后读回实际写入（或并发已有）的 dataset 行。
// 优先按 newID 读取本次写入；若并发写入导致 ID 不存在，则按 orgID/appID 读取已有行。
func (s *KnowledgeService) readDatasetAfterCreate(ctx context.Context, scope, orgID, appID, newID string) (sqlc.RagflowDataset, error) {
	if dataset, err := s.store.GetRAGFlowDataset(ctx, newID); err == nil {
		return dataset, nil
	}
	return s.datasetAfterCreateConflict(ctx, scope, orgID, appID)
}

func (s *KnowledgeService) datasetAfterCreateConflict(ctx context.Context, scope string, orgID, appID string) (sqlc.RagflowDataset, error) {
	var (
		existing sqlc.RagflowDataset
		err      error
	)
	if scope == "org" {
		existing, err = s.store.GetRAGFlowOrgDataset(ctx, orgID)
	} else {
		existing, err = s.store.GetRAGFlowAppDataset(ctx, null.StringFrom(appID))
	}
	if err != nil {
		return sqlc.RagflowDataset{}, fmt.Errorf("读取并发创建的 RAGFlow dataset 映射失败: %w", err)
	}
	if existing.Status == "active" && existing.RagflowDatasetID.Valid && strings.TrimSpace(existing.RagflowDatasetID.String) != "" {
		return existing, nil
	}
	return s.ensureExistingDataset(ctx, existing)
}

func (s *KnowledgeService) ensureExistingDataset(ctx context.Context, dataset sqlc.RagflowDataset) (sqlc.RagflowDataset, error) {
	if dataset.Status == "deleting" {
		return sqlc.RagflowDataset{}, ErrKnowledgeMissing
	}
	if dataset.RagflowDatasetID.Valid && strings.TrimSpace(dataset.RagflowDatasetID.String) != "" && dataset.Status == "active" {
		return dataset, nil
	}
	claimToken, err := generateRAGFlowDatasetClaimToken()
	if err != nil {
		return sqlc.RagflowDataset{}, err
	}
	// ClaimRAGFlowDatasetCreation 为 :exec；StaleBefore 是 time.Time（MySQL DATETIME）。
	// 成功更新 1 行说明本进程抢到租约；更新 0 行说明仍在有效租约内（由其他进程持有）。
	if err := s.store.ClaimRAGFlowDatasetCreation(ctx, sqlc.ClaimRAGFlowDatasetCreationParams{
		ID:               dataset.ID,
		CreateClaimToken: null.StringFrom(claimToken),
		StaleBefore:      time.Now().Add(-ragflowDatasetCreateClaimTimeout),
	}); err != nil {
		return sqlc.RagflowDataset{}, fmt.Errorf("抢占 RAGFlow dataset 创建租约失败: %w", err)
	}
	// 读回最新行确认是否成功更新（:exec 不返回受影响行数）。
	claimed, err := s.store.GetRAGFlowDataset(ctx, dataset.ID)
	if err != nil {
		return sqlc.RagflowDataset{}, fmt.Errorf("读取 RAGFlow dataset 创建租约失败: %w", err)
	}
	// 若 claim_token 不是本次写入的值，说明并发创建中其他进程持有租约，本进程应稍后重试。
	if !claimed.CreateClaimToken.Valid || claimed.CreateClaimToken.String != claimToken {
		return sqlc.RagflowDataset{}, ErrKnowledgeDatasetCreating
	}
	return s.createRemoteDataset(ctx, claimed, claimToken)
}

func (s *KnowledgeService) createRemoteDataset(ctx context.Context, dataset sqlc.RagflowDataset, claimToken string) (sqlc.RagflowDataset, error) {
	remote, err := s.ragflowClient().CreateDataset(ctx, dataset.Name, s.datasetChunkMethod)
	if err != nil {
		// MarkRAGFlowDatasetFailed 为 :exec；失败写入是 best-effort，忽略返回值。
		_ = s.store.MarkRAGFlowDatasetFailed(ctx, sqlc.MarkRAGFlowDatasetFailedParams{
			ID:               dataset.ID,
			LastError:        null.StringFrom(err.Error()),
			CreateClaimToken: null.StringFrom(claimToken),
		})
		return sqlc.RagflowDataset{}, fmt.Errorf("创建 RAGFlow dataset 失败: %w", err)
	}
	if strings.TrimSpace(remote.ID) == "" {
		emptyErr := fmt.Errorf("RAGFlow CreateDataset 返回空 dataset_id")
		_ = s.store.MarkRAGFlowDatasetFailed(ctx, sqlc.MarkRAGFlowDatasetFailedParams{
			ID:               dataset.ID,
			LastError:        null.StringFrom(emptyErr.Error()),
			CreateClaimToken: null.StringFrom(claimToken),
		})
		return sqlc.RagflowDataset{}, emptyErr
	}
	name := strings.TrimSpace(remote.Name)
	if name == "" {
		name = dataset.Name
	}
	// SetRAGFlowDatasetActive 为 :exec（条件 UPDATE：WHERE status='creating' AND create_claim_token=?）；
	// MySQL :exec 对 0 行更新返回 nil，故不能靠错误判断是否命中，写入后通过 GetRAGFlowDataset 读回比对。
	if err := s.store.SetRAGFlowDatasetActive(ctx, sqlc.SetRAGFlowDatasetActiveParams{
		ID:               dataset.ID,
		RagflowDatasetID: null.StringFrom(remote.ID),
		Name:             name,
		CreateClaimToken: null.StringFrom(claimToken),
	}); err != nil {
		return sqlc.RagflowDataset{}, fmt.Errorf("回写 RAGFlow dataset 映射失败: %w", err)
	}
	active, err := s.store.GetRAGFlowDataset(ctx, dataset.ID)
	if err != nil {
		return sqlc.RagflowDataset{}, fmt.Errorf("读取激活后 RAGFlow dataset 失败: %w", err)
	}
	// 读回的 ragflow_dataset_id 若不是本次创建的 remote.ID，说明激活 UPDATE 命中 0 行——
	// 租约在远端创建期间已被其他进程抢占并激活，本进程为败者：删除本次创建的孤儿远端 dataset，
	// 再读回获胜者映射（或返回"创建中"），避免用本进程的 remote.ID 覆盖获胜者状态。
	if !active.RagflowDatasetID.Valid || active.RagflowDatasetID.String != remote.ID {
		_ = s.ragflowClient().DeleteDatasets(ctx, []string{remote.ID})
		return s.datasetAfterCreateConflict(ctx, dataset.ScopeType, dataset.OrgID, dataset.AppID.String)
	}
	return active, nil
}

func generateRAGFlowDatasetClaimToken() (string, error) {
	token, err := generateAppRuntimeToken()
	if err != nil {
		return "", fmt.Errorf("生成 RAGFlow dataset 创建租约失败: %w", err)
	}
	return token, nil
}

func (s *KnowledgeService) ragflowClient() RAGFlowKnowledgeClient {
	if s.ragflow == nil {
		return missingRAGFlowClient{}
	}
	return s.ragflow
}

type missingRAGFlowClient struct{}

func (missingRAGFlowClient) CreateDataset(context.Context, string, string) (ragflow.Dataset, error) {
	return ragflow.Dataset{}, ErrKnowledgeMissing
}
func (missingRAGFlowClient) DeleteDatasets(context.Context, []string) error {
	return ErrKnowledgeMissing
}
func (missingRAGFlowClient) UploadDocument(context.Context, string, string, io.Reader) (ragflow.Document, error) {
	return ragflow.Document{}, ErrKnowledgeMissing
}
func (missingRAGFlowClient) DownloadDocument(context.Context, string, string) (io.ReadCloser, int64, error) {
	return nil, 0, ErrKnowledgeMissing
}
func (missingRAGFlowClient) DeleteDocuments(context.Context, string, []string) error {
	return ErrKnowledgeMissing
}
func (missingRAGFlowClient) ParseDocuments(context.Context, string, []string) error {
	return ErrKnowledgeMissing
}
func (missingRAGFlowClient) ListDocuments(context.Context, string, int32, int32, string, string) ([]ragflow.Document, int32, error) {
	return nil, 0, ErrKnowledgeMissing
}
func (missingRAGFlowClient) Retrieve(context.Context, []string, string, int32) ([]ragflow.RetrievalChunk, error) {
	return nil, ErrKnowledgeMissing
}

func requireRemoteDatasetID(dataset sqlc.RagflowDataset) (string, error) {
	if dataset.RagflowDatasetID.Valid && strings.TrimSpace(dataset.RagflowDatasetID.String) != "" {
		return dataset.RagflowDatasetID.String, nil
	}
	return "", ErrKnowledgeMissing
}

func buildRAGFlowDatasetName(scope, primary, fallback string, id string) string {
	// id 已是 string，直接截取前 8 位作为短 ID 用于数据集命名。
	shortID := id
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	label := sanitizeDatasetLabel(primary)
	if label == "" {
		label = sanitizeDatasetLabel(fallback)
	}
	if label == "" {
		return fmt.Sprintf("ocm-%s-%s", scope, shortID)
	}
	return fmt.Sprintf("ocm-%s-%s-%s", scope, label, shortID)
}

func sanitizeDatasetLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		keep := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if keep {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func normalizePage(page, pageSize int32) (int32, int32) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return page, pageSize
}

func normalizeRuntimeTopK(topK int32) int32 {
	if topK <= 0 {
		return runtimeKnowledgeDefaultTopK
	}
	if topK > runtimeKnowledgeMaxTopK {
		return runtimeKnowledgeMaxTopK
	}
	return topK
}

func normalizeRAGFlowRun(run string) string {
	switch strings.ToUpper(strings.TrimSpace(run)) {
	case "", "0", "UNSTART":
		return "queued"
	case "1", "RUNNING":
		return "running"
	case "3", "DONE", "SUCCESS", "SUCCEEDED", "COMPLETED":
		return "completed"
	case "4", "FAIL", "FAILED", "ERROR":
		return "failed"
	case "2", "CANCEL", "CANCELLED", "STOPPED":
		return "stopped"
	default:
		return "running"
	}
}

func progressForStatus(status string) int32 {
	if status == "completed" {
		return 100
	}
	return 0
}

func toKnowledgeDocumentResult(row sqlc.RagflowDocument) KnowledgeDocumentResult {
	result := KnowledgeDocumentResult{
		ID:          row.ID,
		Name:        row.Name,
		Size:        row.SizeBytes,
		ParseStatus: row.ParseStatus,
		Progress:    row.Progress,
		// CreatedAt 是 time.Time（MySQL DATETIME），直接格式化为 RFC3339。
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
	}
	if row.MimeType.Valid {
		result.MimeType = row.MimeType.String
	}
	if row.Suffix.Valid {
		result.Suffix = row.Suffix.String
	}
	if row.LastError.Valid {
		result.LastError = row.LastError.String
	}
	return result
}

// HashAppRuntimeToken 对 app runtime token 做不可逆 hash 后入库和鉴权。
func HashAppRuntimeToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
