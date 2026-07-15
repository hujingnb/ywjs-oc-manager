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
	"oc-manager/internal/config"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/ragflow"
	"oc-manager/internal/integrations/storage"
	"oc-manager/internal/store/sqlc"
)

const (
	// runtimeKnowledgeDefaultTopK 与 Hermes oc-kb 默认值保持一致，避免 RAGFlow 使用 1024 的服务端默认值。
	runtimeKnowledgeDefaultTopK int32 = 8
	// runtimeKnowledgeMaxTopK 是单个作用域的检索上限；runtime 检索会分别查询 app 与 org 两个作用域。
	runtimeKnowledgeMaxTopK int32 = 50
	// ragflowDatasetCreateClaimTimeout 是创建租约的保守超时，避免进程崩溃后 creating 状态永久卡住。
	ragflowDatasetCreateClaimTimeout = 15 * time.Minute

	// KnowledgeRAGFlowScopeOrg 表示企业级 RAGFlow dataset 运维目标。
	KnowledgeRAGFlowScopeOrg = "org"
	// KnowledgeRAGFlowScopeApp 表示实例级 RAGFlow dataset 运维目标。
	KnowledgeRAGFlowScopeApp = "app"
	// KnowledgeRAGFlowScopeIndustry 表示行业知识库 RAGFlow dataset 运维目标。
	KnowledgeRAGFlowScopeIndustry = "industry"
)

// RAGFlowKnowledgeClient 是 service 层依赖的 RAGFlow 最小能力集。
// 这里不暴露 RAGFlow 凭证、dataset 选择权或权限字段，调用方只能传 service 解析后的 dataset ID。
type RAGFlowKnowledgeClient interface {
	CreateDataset(ctx context.Context, req ragflow.CreateDatasetRequest) (ragflow.Dataset, error)
	GetDataset(ctx context.Context, datasetID string) (ragflow.Dataset, error)
	UpdateDatasetEmbeddingModel(ctx context.Context, datasetID, embeddingModel string) error
	RunDatasetEmbedding(ctx context.Context, datasetID string) error
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
	// GetRAGFlowOrgDataset 按组织 ID 查询 org 级 dataset 映射；schema 允许行业库 org_id 为 NULL，因此 sqlc 参数为 null.String。
	GetRAGFlowOrgDataset(ctx context.Context, orgID null.String) (sqlc.RagflowDataset, error)
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
	// CreateRAGFlowIndustryDocument 保存行业库 document 元数据，确保行业外键列被写入。
	CreateRAGFlowIndustryDocument(ctx context.Context, arg sqlc.CreateRAGFlowIndustryDocumentParams) error
	// ReplaceRAGFlowIndustryDocument 原地替换行业库同名文件的本地映射。
	ReplaceRAGFlowIndustryDocument(ctx context.Context, arg sqlc.ReplaceRAGFlowIndustryDocumentParams) error
	ListRAGFlowDocumentsByScope(ctx context.Context, arg sqlc.ListRAGFlowDocumentsByScopeParams) ([]sqlc.RagflowDocument, error)
	CountRAGFlowDocumentsByScope(ctx context.Context, arg sqlc.CountRAGFlowDocumentsByScopeParams) (int64, error)
	// ListAllRAGFlowDocumentsByScope 列出企业或实例知识库全部文件，供整库清空操作使用。
	ListAllRAGFlowDocumentsByScope(ctx context.Context, arg sqlc.ListAllRAGFlowDocumentsByScopeParams) ([]sqlc.RagflowDocument, error)
	// SumRAGFlowDocumentsSizeByScope 统计当前知识库累计占用，包含所有解析状态。
	SumRAGFlowDocumentsSizeByScope(ctx context.Context, arg sqlc.SumRAGFlowDocumentsSizeByScopeParams) (int64, error)
	GetRAGFlowDocument(ctx context.Context, id string) (sqlc.RagflowDocument, error)
	// UpdateRAGFlowDocumentParseStatus 更新解析状态（:exec），写入后通过 GetRAGFlowDocument 读回。
	UpdateRAGFlowDocumentParseStatus(ctx context.Context, arg sqlc.UpdateRAGFlowDocumentParseStatusParams) error
	// ResetRAGFlowDocumentsParseStatusByDataset 在整库重新解析被 RAGFlow 接受后批量重置本地解析状态。
	ResetRAGFlowDocumentsParseStatusByDataset(ctx context.Context, datasetID string) error
	// MarkRAGFlowDocumentManualReparseQueued 人工重解析入队，把文档重置为 queued（:exec），写入后通过 GetRAGFlowDocument 读回。
	MarkRAGFlowDocumentManualReparseQueued(ctx context.Context, id string) error
	DeleteRAGFlowDocumentMapping(ctx context.Context, id string) error
	DeleteRAGFlowDatasetMapping(ctx context.Context, id string) error
	// CreateIndustryKnowledgeBase 创建平台级行业知识库。
	CreateIndustryKnowledgeBase(ctx context.Context, arg sqlc.CreateIndustryKnowledgeBaseParams) error
	// GetIndustryKnowledgeBase 按 ID 读取未删除行业知识库。
	GetIndustryKnowledgeBase(ctx context.Context, id string) (sqlc.IndustryKnowledgeBasis, error)
	// GetIndustryKnowledgeBaseForUpdate 在事务中锁定未删除行业库。
	GetIndustryKnowledgeBaseForUpdate(ctx context.Context, id string) (sqlc.IndustryKnowledgeBasis, error)
	// GetIndustryKnowledgeBaseByName 按名称读取未删除行业知识库。
	GetIndustryKnowledgeBaseByName(ctx context.Context, name string) (sqlc.IndustryKnowledgeBasis, error)
	// ListIndustryKnowledgeBases 分页列出行业知识库并带文件数。
	ListIndustryKnowledgeBases(ctx context.Context, arg sqlc.ListIndustryKnowledgeBasesParams) ([]sqlc.ListIndustryKnowledgeBasesRow, error)
	// CountIndustryKnowledgeBases 统计行业知识库分页总数。
	CountIndustryKnowledgeBases(ctx context.Context, arg sqlc.CountIndustryKnowledgeBasesParams) (int64, error)
	// RenameIndustryKnowledgeBase 重命名未删除行业知识库。
	RenameIndustryKnowledgeBase(ctx context.Context, arg sqlc.RenameIndustryKnowledgeBaseParams) error
	// SoftDeleteIndustryKnowledgeBase 软删除行业知识库。
	SoftDeleteIndustryKnowledgeBase(ctx context.Context, id string) (int64, error)
	// CountAssistantVersionsUsingIndustryKnowledgeBase 统计仍引用行业库的未删除助手版本。
	CountAssistantVersionsUsingIndustryKnowledgeBase(ctx context.Context, industryKnowledgeBaseID string) (int64, error)
	// CountOrganizationsUsingIndustryKnowledgeBase 统计仍被平台授权给企业的行业库。
	CountOrganizationsUsingIndustryKnowledgeBase(ctx context.Context, industryKnowledgeBaseID string) (int64, error)
	// CreateRAGFlowIndustryDatasetMapping 创建行业库 dataset 映射。
	CreateRAGFlowIndustryDatasetMapping(ctx context.Context, arg sqlc.CreateRAGFlowIndustryDatasetMappingParams) error
	// GetRAGFlowIndustryDataset 按行业库 ID 读取 dataset 映射。
	GetRAGFlowIndustryDataset(ctx context.Context, industryKnowledgeBaseID null.String) (sqlc.RagflowDataset, error)
	// ListRAGFlowIndustryDocuments 分页列出行业库文件。
	ListRAGFlowIndustryDocuments(ctx context.Context, arg sqlc.ListRAGFlowIndustryDocumentsParams) ([]sqlc.RagflowDocument, error)
	// CountRAGFlowIndustryDocuments 统计行业库文件总数。
	CountRAGFlowIndustryDocuments(ctx context.Context, arg sqlc.CountRAGFlowIndustryDocumentsParams) (int64, error)
	// ListAllRAGFlowIndustryDocuments 列出行业库全部文件，供整库清空操作使用。
	ListAllRAGFlowIndustryDocuments(ctx context.Context, industryKnowledgeBaseID null.String) ([]sqlc.RagflowDocument, error)
	// GetRAGFlowIndustryDocumentByName 按行业库和文件名查找本地 document 映射。
	GetRAGFlowIndustryDocumentByName(ctx context.Context, arg sqlc.GetRAGFlowIndustryDocumentByNameParams) (sqlc.RagflowDocument, error)
	// ListIndustryKnowledgeBasesByAssistantVersion 列出指定助手版本关联的行业库。
	ListIndustryKnowledgeBasesByAssistantVersion(ctx context.Context, versionID string) ([]sqlc.IndustryKnowledgeBasis, error)
	// GetAICCAgentByAppID 识别 AICC 隐藏 app，并读取该智能体绑定的知识范围。
	GetAICCAgentByAppID(ctx context.Context, appID string) (sqlc.AiccAgent, error)
	// ListAICCAgentKnowledge 列出 AICC 智能体配置的可检索知识范围。
	ListAICCAgentKnowledge(ctx context.Context, agentID string) ([]sqlc.AiccAgentKnowledge, error)
}

// KnowledgeDatasetProvisioner 抽象组织 / 实例生命周期中预创建 RAGFlow dataset 的能力。
type KnowledgeDatasetProvisioner interface {
	EnsureOrgDataset(ctx context.Context, org sqlc.Organization) (sqlc.RagflowDataset, error)
	EnsureAppDataset(ctx context.Context, app sqlc.App) (sqlc.RagflowDataset, error)
}

// KnowledgeTxRunner 抽象知识库本地写操作事务，用于序列化行业库删除和版本关联写入。
type KnowledgeTxRunner interface {
	WithKnowledgeTx(ctx context.Context, fn func(KnowledgeStore) error) error
}

// knowledgeBlobStore 是分片上传依赖的对象存储能力子集（由 storage.S3ObjectStore 实现）。
// 只在 S3 启用时注入；未注入时分片上传不可用，service 返回 ErrKnowledgeMultipartUnavailable。
type knowledgeBlobStore interface {
	CreateMultipartUpload(ctx context.Context, key string) (string, error)
	UploadPart(ctx context.Context, key, uploadID string, partNumber int32, r io.Reader, size int64) (string, error)
	CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []storage.MultipartPart) error
	AbortMultipartUpload(ctx context.Context, key, uploadID string) error
	OpenObject(ctx context.Context, key string) (io.ReadCloser, int64, error)
	DeleteObject(ctx context.Context, key string) error
}

// KnowledgeService 以 RAGFlow 作为唯一文件主库，对外提供 manager 权限控制后的知识库能力。
type KnowledgeService struct {
	store              KnowledgeStore
	ragflow            RAGFlowKnowledgeClient
	datasetChunkMethod string
	// defaultEmbeddingModel 保存配置中的 RAGFlow 控制台可见模型名，后续创建 dataset 时再解析为远端内部 ID。
	defaultEmbeddingModel string
	// embeddingModelFallbacks 保存 RAGFlow 模型列表不可用时的兜底候选，setter 会复制切片避免外部修改污染服务状态。
	embeddingModelFallbacks []config.RAGFlowEmbeddingModelConfig
	tx                      KnowledgeTxRunner
	// blobStore / uploadSessions / uploadPartSize 为分片上传所需依赖，仅 S3 启用时注入。
	blobStore      knowledgeBlobStore
	uploadSessions knowledgeUploadSessions
	uploadPartSize int64
}

// NewKnowledgeService 创建 RAGFlow-backed 知识库服务。
func NewKnowledgeService(store KnowledgeStore, client RAGFlowKnowledgeClient) *KnowledgeService {
	return &KnowledgeService{store: store, ragflow: client, datasetChunkMethod: "naive"}
}

// SetTxRunner 注入知识库本地事务 runner。
func (s *KnowledgeService) SetTxRunner(tx KnowledgeTxRunner) { s.tx = tx }

// SetMultipartUploader 注入分片上传依赖（对象存储 + 会话存储 + 分片大小）。
// partSize<=0 时回退默认 knowledgeUploadDefaultPartSize。仅在 S3 启用时调用，否则分片上传不可用。
func (s *KnowledgeService) SetMultipartUploader(blob knowledgeBlobStore, sessions knowledgeUploadSessions, partSize int64) {
	s.blobStore = blob
	s.uploadSessions = sessions
	if partSize <= 0 {
		partSize = knowledgeUploadDefaultPartSize
	}
	s.uploadPartSize = partSize
}

// withKnowledgeTx 在事务中执行本地知识库写操作；测试未注入 runner 时退化为直接使用 store。
func (s *KnowledgeService) withKnowledgeTx(ctx context.Context, fn func(KnowledgeStore) error) error {
	if s.tx != nil {
		return s.tx.WithKnowledgeTx(ctx, fn)
	}
	return fn(s.store)
}

// SetDatasetChunkMethod 设置自动创建 RAGFlow dataset 时使用的分块方法。
func (s *KnowledgeService) SetDatasetChunkMethod(method string) {
	method = strings.TrimSpace(method)
	if method == "" {
		method = "naive"
	}
	s.datasetChunkMethod = method
}

// SetDefaultEmbeddingModel 设置创建 RAGFlow dataset 时优先使用的 embedding 模型名。
func (s *KnowledgeService) SetDefaultEmbeddingModel(model string) {
	s.defaultEmbeddingModel = strings.TrimSpace(model)
}

// SetEmbeddingModelFallbacks 设置 RAGFlow embedding 模型兜底清单。
func (s *KnowledgeService) SetEmbeddingModelFallbacks(models []config.RAGFlowEmbeddingModelConfig) {
	s.embeddingModelFallbacks = append([]config.RAGFlowEmbeddingModelConfig(nil), models...)
}

// KnowledgeListResult 是扁平文档列表接口的返回。
type KnowledgeListResult struct {
	Items []KnowledgeDocumentResult `json:"items"`
	Total int64                     `json:"total"`
	// UsedBytes 是当前知识库本地记录的累计文件大小，包含失败/停止解析文件。
	UsedBytes int64 `json:"used_bytes"`
	// QuotaBytes 是当前知识库累计容量上限，单位字节。
	QuotaBytes int64 `json:"quota_bytes"`
	// RemainingBytes 是展示用剩余空间，超用时按 0 返回。
	RemainingBytes int64 `json:"remaining_bytes"`
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

// KnowledgeEmbeddingModelResult 是平台运维界面展示的 RAGFlow embedding 模型候选。
type KnowledgeEmbeddingModelResult struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Provider  string `json:"provider"`
	Available bool   `json:"available"`
}

// KnowledgeEmbeddingModelInput 是平台管理员提交的模型切换请求。
// Name/Provider 均使用 RAGFlow 控制台可见的人类可读文本，不暴露远端内部 ID。
type KnowledgeEmbeddingModelInput struct {
	Name     string
	Provider string
}

// KnowledgeEmbeddingModelListResult 是模型候选列表接口的返回。
type KnowledgeEmbeddingModelListResult struct {
	Items []KnowledgeEmbeddingModelResult `json:"items"`
}

// KnowledgeRAGFlowDatasetInfoResult 是平台运维界面查看单个业务目标 RAGFlow dataset 的实时信息。
type KnowledgeRAGFlowDatasetInfoResult struct {
	Scope              string                         `json:"scope"`
	TargetID           string                         `json:"target_id"`
	TargetName         string                         `json:"target_name"`
	Status             string                         `json:"status"`
	RAGFlowDatasetID   string                         `json:"ragflow_dataset_id,omitempty"`
	RAGFlowDatasetName string                         `json:"ragflow_dataset_name,omitempty"`
	EmbeddingModel     *KnowledgeEmbeddingModelResult `json:"embedding_model,omitempty"`
	ErrorMessage       string                         `json:"error_message,omitempty"`
	DocNum             int32                          `json:"doc_num,omitempty"`
	ChunkNum           int32                          `json:"chunk_num,omitempty"`
	UpdatedAt          string                         `json:"updated_at,omitempty"`
}

// KnowledgeSearchResult 是 Hermes runtime 检索 API 的返回。
type KnowledgeSearchResult struct {
	Results []KnowledgeSearchHit `json:"results"`
}

// KnowledgeSearchHit 是一次 retrieval 命中的文本块。
type KnowledgeSearchHit struct {
	Scope                     string  `json:"scope"`
	DocumentID                string  `json:"document_id"`
	DocumentName              string  `json:"document_name"`
	Content                   string  `json:"content"`
	Similarity                float64 `json:"similarity"`
	IndustryKnowledgeBaseID   string  `json:"industry_knowledge_base_id,omitempty"`
	IndustryKnowledgeBaseName string  `json:"industry_knowledge_base_name,omitempty"`
}

// ListOrg 列出企业知识库文件。企业成员只读，企业管理员可写。
func (s *KnowledgeService) ListOrg(ctx context.Context, principal auth.Principal, orgID string, page, pageSize int32, keyword, status string) (KnowledgeListResult, error) {
	if !auth.CanReadOrgKnowledge(principal, orgID) {
		return KnowledgeListResult{}, ErrKnowledgeForbidden
	}
	org, err := s.getOrg(ctx, orgID)
	if err != nil {
		return KnowledgeListResult{}, err
	}
	// orgID 直接作为字符串传入，不再解析为 UUID 类型。
	dataset, err := s.getOrgDataset(ctx, org.ID)
	if err != nil {
		return KnowledgeListResult{}, err
	}
	return s.listDocuments(ctx, dataset, "org", org.ID, "", page, pageSize, keyword, status, org.KnowledgeQuotaBytes)
}

// SaveOrgFile 上传企业知识库文件并触发解析。
func (s *KnowledgeService) SaveOrgFile(ctx context.Context, principal auth.Principal, orgID, filename string, content io.Reader, size int64) (KnowledgeDocumentResult, error) {
	if !auth.CanWriteOrgKnowledge(principal, orgID) {
		return KnowledgeDocumentResult{}, ErrKnowledgeForbidden
	}
	org, err := s.getOrg(ctx, orgID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	if err := s.ensureKnowledgeQuotaAvailable(ctx, "org", org.ID, "", org.KnowledgeQuotaBytes, size); err != nil {
		return KnowledgeDocumentResult{}, err
	}
	dataset, err := s.getOrgDataset(ctx, org.ID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return s.uploadToDataset(ctx, knowledgeUploadTarget{Dataset: dataset, CreatedBy: principal.UserID}, filename, content, size)
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

// ClearOrgFiles 清空企业知识库下的全部文件内容，保留企业和 dataset 映射。
func (s *KnowledgeService) ClearOrgFiles(ctx context.Context, principal auth.Principal, orgID string) error {
	if !auth.CanWriteOrgKnowledge(principal, orgID) {
		return ErrKnowledgeForbidden
	}
	org, err := s.getOrg(ctx, orgID)
	if err != nil {
		return err
	}
	dataset, err := s.getOrgDataset(ctx, org.ID)
	if err != nil {
		return err
	}
	documents, err := s.store.ListAllRAGFlowDocumentsByScope(ctx, sqlc.ListAllRAGFlowDocumentsByScopeParams{
		ScopeType: "org",
		OrgID:     null.StringFrom(org.ID),
		AppID:     null.String{},
	})
	if err != nil {
		return fmt.Errorf("查询企业知识库全部文件失败: %w", err)
	}
	return s.clearDocuments(ctx, dataset, documents)
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
	app, err := s.getApp(ctx, principal, appID, false)
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
	return s.listDocuments(ctx, dataset, "app", app.OrgID, strOrEmpty(dataset.AppID), page, pageSize, keyword, status, app.KnowledgeQuotaBytes)
}

// SaveAppFile 上传实例知识库文件并触发解析。
func (s *KnowledgeService) SaveAppFile(ctx context.Context, principal auth.Principal, appID, filename string, content io.Reader, size int64) (KnowledgeDocumentResult, error) {
	app, err := s.getApp(ctx, principal, appID, true)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	if !auth.CanWriteAppKnowledge(principal, app.OrgID, app.OwnerUserID) {
		return KnowledgeDocumentResult{}, ErrKnowledgeForbidden
	}
	if err := s.ensureKnowledgeQuotaAvailable(ctx, "app", app.OrgID, app.ID, app.KnowledgeQuotaBytes, size); err != nil {
		return KnowledgeDocumentResult{}, err
	}
	dataset, err := s.getAppDataset(ctx, app.ID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return s.uploadToDataset(ctx, knowledgeUploadTarget{Dataset: dataset, AppID: app.ID, CreatedBy: principal.UserID}, filename, content, size)
}

// OpenAppFile 打开实例知识库文件流供下载。
func (s *KnowledgeService) OpenAppFile(ctx context.Context, principal auth.Principal, appID, documentID string) (io.ReadCloser, int64, string, error) {
	app, err := s.getApp(ctx, principal, appID, false)
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
	app, err := s.getApp(ctx, principal, appID, true)
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
	app, err := s.getApp(ctx, principal, appID, true)
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
	topK = normalizeRuntimeTopK(topK)
	if result, handled, err := s.runtimeSearchAICC(ctx, app, question, topK); handled || err != nil {
		return result, err
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
	if app.VersionID.Valid {
		industryBases, err := s.store.ListIndustryKnowledgeBasesByAssistantVersion(ctx, app.VersionID.String)
		if err != nil {
			return KnowledgeSearchResult{}, fmt.Errorf("查询助手版本行业知识库失败: %w", err)
		}
		for _, base := range industryBases {
			dataset, err := s.getIndustryDataset(ctx, base)
			if err != nil {
				return KnowledgeSearchResult{}, err
			}
			remoteID, err := requireRemoteDatasetID(dataset)
			if err != nil {
				return KnowledgeSearchResult{}, err
			}
			// 每个行业库独立检索一次，保留各自 top_k，避免多个行业库合并后互相挤占命中。
			chunks, err := s.ragflowClient().Retrieve(ctx, []string{remoteID}, question, topK)
			if err != nil {
				return KnowledgeSearchResult{}, fmt.Errorf("RAGFlow 检索行业知识库失败: %w", err)
			}
			hits = append(hits, searchHitsFromIndustryChunks(base, chunks)...)
		}
	}
	return KnowledgeSearchResult{Results: hits}, nil
}

func (s *KnowledgeService) runtimeSearchAICC(ctx context.Context, app sqlc.App, question string, topK int32) (KnowledgeSearchResult, bool, error) {
	agent, err := s.store.GetAICCAgentByAppID(ctx, app.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeSearchResult{}, false, nil
	}
	if err != nil {
		return KnowledgeSearchResult{}, true, fmt.Errorf("查询 AICC 智能体知识范围失败: %w", err)
	}
	rows, err := s.store.ListAICCAgentKnowledge(ctx, agent.ID)
	if err != nil {
		return KnowledgeSearchResult{}, true, fmt.Errorf("查询 AICC 知识范围失败: %w", err)
	}
	scope := normalizeRuntimeAICCKnowledgeScope(rows)
	hits := []KnowledgeSearchHit{}
	appDataset, err := s.getAppDataset(ctx, app.ID)
	if err != nil {
		return KnowledgeSearchResult{}, true, err
	}
	appRemoteID, err := requireRemoteDatasetID(appDataset)
	if err != nil {
		return KnowledgeSearchResult{}, true, err
	}
	chunks, err := s.ragflowClient().Retrieve(ctx, []string{appRemoteID}, question, topK)
	if err != nil {
		return KnowledgeSearchResult{}, true, fmt.Errorf("RAGFlow 检索 AICC 当前客服知识库失败: %w", err)
	}
	hits = append(hits, searchHitsFromChunks("app", chunks)...)
	if scope.UseOrgKnowledge {
		orgDataset, err := s.getOrgDataset(ctx, app.OrgID)
		if err != nil {
			return KnowledgeSearchResult{}, true, err
		}
		orgRemoteID, err := requireRemoteDatasetID(orgDataset)
		if err != nil {
			return KnowledgeSearchResult{}, true, err
		}
		chunks, err := s.ragflowClient().Retrieve(ctx, []string{orgRemoteID}, question, topK)
		if err != nil {
			return KnowledgeSearchResult{}, true, fmt.Errorf("RAGFlow 检索 AICC 企业知识库失败: %w", err)
		}
		hits = append(hits, searchHitsFromChunks("org", chunks)...)
	}
	for _, industryID := range scope.IndustryKnowledgeBaseIDs {
		base, err := s.store.GetIndustryKnowledgeBase(ctx, industryID)
		if err != nil {
			return KnowledgeSearchResult{}, true, fmt.Errorf("查询 AICC 行业知识库失败: %w", err)
		}
		dataset, err := s.getIndustryDataset(ctx, base)
		if err != nil {
			return KnowledgeSearchResult{}, true, err
		}
		remoteID, err := requireRemoteDatasetID(dataset)
		if err != nil {
			return KnowledgeSearchResult{}, true, err
		}
		chunks, err := s.ragflowClient().Retrieve(ctx, []string{remoteID}, question, topK)
		if err != nil {
			return KnowledgeSearchResult{}, true, fmt.Errorf("RAGFlow 检索 AICC 行业知识库失败: %w", err)
		}
		hits = append(hits, searchHitsFromIndustryChunks(base, chunks)...)
	}
	return KnowledgeSearchResult{Results: hits}, true, nil
}

type runtimeAICCKnowledgeScope struct {
	UseOrgKnowledge          bool
	IndustryKnowledgeBaseIDs []string
}

func normalizeRuntimeAICCKnowledgeScope(rows []sqlc.AiccAgentKnowledge) runtimeAICCKnowledgeScope {
	scope := runtimeAICCKnowledgeScope{}
	industrySeen := map[string]bool{}
	for _, row := range rows {
		switch row.ScopeType {
		case "org":
			scope.UseOrgKnowledge = true
		case "industry":
			id := strings.TrimSpace(row.IndustryKnowledgeBaseID.String)
			if id != "" && !industrySeen[id] {
				industrySeen[id] = true
				scope.IndustryKnowledgeBaseIDs = append(scope.IndustryKnowledgeBaseIDs, id)
			}
		}
	}
	return scope
}

// RuntimeAddFile 供 Hermes 把工作目录中的报告写入当前实例知识库。
// 写入目标固定为当前 app dataset，组织 dataset 在写路径完全不可达。
func (s *KnowledgeService) RuntimeAddFile(ctx context.Context, appToken, filename string, content io.Reader, size int64) (KnowledgeDocumentResult, error) {
	app, err := s.appByRuntimeToken(ctx, appToken)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	// AICC 容器不保存运行时工作目录，客服能力仅限只读知识问答；必须在访问配额、dataset
	// 与 RAGFlow 前阻断写入，避免伪造工具调用绕过镜像侧 capability broker。
	if domain.IsAICCAppType(domain.AppType(app.AppType)) {
		return KnowledgeDocumentResult{}, ErrAICCOperationForbidden
	}
	if err := s.ensureKnowledgeQuotaAvailable(ctx, "app", app.OrgID, app.ID, app.KnowledgeQuotaBytes, size); err != nil {
		return KnowledgeDocumentResult{}, err
	}
	dataset, err := s.getAppDataset(ctx, app.ID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return s.uploadToDataset(ctx, knowledgeUploadTarget{Dataset: dataset, AppID: app.ID, CreatedBy: "runtime:" + app.ID}, filename, content, size)
}

// EnsureOrgDataset 确保企业知识库存在可用的 RAGFlow dataset。
func (s *KnowledgeService) EnsureOrgDataset(ctx context.Context, org sqlc.Organization) (sqlc.RagflowDataset, error) {
	name := buildRAGFlowDatasetName("org", org.Code, org.Name, org.ID)
	return s.ensureDataset(ctx, "org", org.ID, "", "", name)
}

// EnsureAppDataset 确保实例知识库存在可用的 RAGFlow dataset。
func (s *KnowledgeService) EnsureAppDataset(ctx context.Context, app sqlc.App) (sqlc.RagflowDataset, error) {
	name := buildRAGFlowDatasetName("app", app.Name, "", app.ID)
	return s.ensureDataset(ctx, "app", app.OrgID, app.ID, "", name)
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

// ListKnowledgeEmbeddingModels 返回平台运维可选的 embedding 模型兜底清单。
func (s *KnowledgeService) ListKnowledgeEmbeddingModels(ctx context.Context, principal auth.Principal) (KnowledgeEmbeddingModelListResult, error) {
	if !auth.CanManageKnowledgeRAGFlowDataset(principal) {
		return KnowledgeEmbeddingModelListResult{}, ErrKnowledgeForbidden
	}
	return KnowledgeEmbeddingModelListResult{Items: s.embeddingModelResultsFromFallbacks()}, nil
}

// GetKnowledgeRAGFlowDatasetInfo 读取业务目标对应的 RAGFlow dataset 实时信息。
// 该方法只读本地映射和远端状态，缺少本地映射时返回 not_created，不触发懒创建。
func (s *KnowledgeService) GetKnowledgeRAGFlowDatasetInfo(ctx context.Context, principal auth.Principal, scope, targetID string) (KnowledgeRAGFlowDatasetInfoResult, error) {
	if !auth.CanManageKnowledgeRAGFlowDataset(principal) {
		return KnowledgeRAGFlowDatasetInfoResult{}, ErrKnowledgeForbidden
	}
	target, dataset, err := s.resolveKnowledgeRAGFlowTarget(ctx, principal, scope, targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeRAGFlowDatasetInfoResult{Scope: scope, TargetID: targetID, TargetName: target.Name, Status: "not_created"}, nil
	}
	if err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{}, err
	}
	remoteID, err := requireRemoteDatasetID(dataset)
	if err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{Scope: scope, TargetID: targetID, TargetName: target.Name, Status: "not_created"}, nil
	}
	remote, err := s.ragflowClient().GetDataset(ctx, remoteID)
	if err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{Scope: scope, TargetID: targetID, TargetName: target.Name, Status: "error", ErrorMessage: err.Error()}, nil
	}
	return s.toKnowledgeRAGFlowDatasetInfo(scope, targetID, target.Name, dataset, remote), nil
}

// UpdateKnowledgeEmbeddingModel 修改 RAGFlow dataset embedding 模型，并在远端接受整库重解析后重置本地解析状态。
func (s *KnowledgeService) UpdateKnowledgeEmbeddingModel(ctx context.Context, principal auth.Principal, scope, targetID string, input KnowledgeEmbeddingModelInput) (KnowledgeRAGFlowDatasetInfoResult, error) {
	if !auth.CanManageKnowledgeRAGFlowDataset(principal) {
		return KnowledgeRAGFlowDatasetInfoResult{}, ErrKnowledgeForbidden
	}
	target, dataset, err := s.resolveKnowledgeRAGFlowTarget(ctx, principal, scope, targetID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return KnowledgeRAGFlowDatasetInfoResult{}, ErrNotFound
		}
		return KnowledgeRAGFlowDatasetInfoResult{}, err
	}
	remoteID, err := requireRemoteDatasetID(dataset)
	if err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{}, err
	}
	model, err := s.resolveKnowledgeEmbeddingModel(input)
	if err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{}, err
	}
	if err := s.ragflowClient().UpdateDatasetEmbeddingModel(ctx, remoteID, ragflowEmbeddingSubmitValue(model)); err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{}, fmt.Errorf("更新 RAGFlow embedding 模型失败: %w", err)
	}
	if err := s.ragflowClient().RunDatasetEmbedding(ctx, remoteID); err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{}, fmt.Errorf("触发 RAGFlow 整库重新解析失败: %w", err)
	}
	if err := s.store.ResetRAGFlowDocumentsParseStatusByDataset(ctx, dataset.ID); err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{}, fmt.Errorf("重置知识库文件解析状态失败: %w", err)
	}
	remote, err := s.ragflowClient().GetDataset(ctx, remoteID)
	if err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{Scope: scope, TargetID: targetID, TargetName: target.Name, Status: "error", ErrorMessage: err.Error()}, nil
	}
	return s.toKnowledgeRAGFlowDatasetInfo(scope, targetID, target.Name, dataset, remote), nil
}

// knowledgeRAGFlowTarget 保存运维目标的展示名称，避免缺少 dataset 映射时丢失目标上下文。
type knowledgeRAGFlowTarget struct {
	Name string
}

// resolveKnowledgeRAGFlowTarget 只解析已有 dataset 映射，不调用 getOrgDataset/getAppDataset 等懒创建路径。
func (s *KnowledgeService) resolveKnowledgeRAGFlowTarget(ctx context.Context, principal auth.Principal, scope, targetID string) (knowledgeRAGFlowTarget, sqlc.RagflowDataset, error) {
	if s.store == nil {
		return knowledgeRAGFlowTarget{}, sqlc.RagflowDataset{}, ErrKnowledgeMissing
	}
	switch scope {
	case KnowledgeRAGFlowScopeOrg:
		org, err := s.getOrg(ctx, targetID)
		if err != nil {
			return knowledgeRAGFlowTarget{}, sqlc.RagflowDataset{}, err
		}
		dataset, err := s.store.GetRAGFlowOrgDataset(ctx, null.StringFrom(org.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return knowledgeRAGFlowTarget{Name: org.Name}, sqlc.RagflowDataset{}, sql.ErrNoRows
		}
		if err != nil {
			return knowledgeRAGFlowTarget{}, sqlc.RagflowDataset{}, fmt.Errorf("查询企业 RAGFlow dataset 失败: %w", err)
		}
		return knowledgeRAGFlowTarget{Name: org.Name}, dataset, nil
	case KnowledgeRAGFlowScopeApp:
		app, err := s.getApp(ctx, principal, targetID, false)
		if err != nil {
			return knowledgeRAGFlowTarget{}, sqlc.RagflowDataset{}, err
		}
		dataset, err := s.store.GetRAGFlowAppDataset(ctx, null.StringFrom(app.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return knowledgeRAGFlowTarget{Name: app.Name}, sqlc.RagflowDataset{}, sql.ErrNoRows
		}
		if err != nil {
			return knowledgeRAGFlowTarget{}, sqlc.RagflowDataset{}, fmt.Errorf("查询实例 RAGFlow dataset 失败: %w", err)
		}
		return knowledgeRAGFlowTarget{Name: app.Name}, dataset, nil
	case KnowledgeRAGFlowScopeIndustry:
		base, err := s.getIndustryKnowledgeBase(ctx, targetID)
		if err != nil {
			return knowledgeRAGFlowTarget{}, sqlc.RagflowDataset{}, err
		}
		dataset, err := s.store.GetRAGFlowIndustryDataset(ctx, null.StringFrom(base.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return knowledgeRAGFlowTarget{Name: base.Name}, sqlc.RagflowDataset{}, sql.ErrNoRows
		}
		if err != nil {
			return knowledgeRAGFlowTarget{}, sqlc.RagflowDataset{}, fmt.Errorf("查询行业知识库 RAGFlow dataset 失败: %w", err)
		}
		return knowledgeRAGFlowTarget{Name: base.Name}, dataset, nil
	default:
		return knowledgeRAGFlowTarget{}, sqlc.RagflowDataset{}, ErrNotFound
	}
}

// embeddingModelResultsFromFallbacks 将配置兜底项转换为前端展示结构，忽略空模型名避免展示不可提交项。
func (s *KnowledgeService) embeddingModelResultsFromFallbacks() []KnowledgeEmbeddingModelResult {
	items := make([]KnowledgeEmbeddingModelResult, 0, len(s.embeddingModelFallbacks))
	for _, model := range s.embeddingModelFallbacks {
		name := strings.TrimSpace(model.Name)
		if name == "" {
			continue
		}
		label := strings.TrimSpace(model.Label)
		if label == "" {
			label = name
		}
		items = append(items, KnowledgeEmbeddingModelResult{
			Name:      name,
			Label:     label,
			Provider:  strings.TrimSpace(model.Provider),
			Available: true,
		})
	}
	return items
}

// resolveKnowledgeEmbeddingModel 把 UI 提交的人类可读 name/provider 解析为可提交给 RAGFlow 的模型值。
func (s *KnowledgeService) resolveKnowledgeEmbeddingModel(input KnowledgeEmbeddingModelInput) (KnowledgeEmbeddingModelResult, error) {
	name := strings.TrimSpace(input.Name)
	provider := strings.TrimSpace(input.Provider)
	if name == "" {
		return KnowledgeEmbeddingModelResult{}, fmt.Errorf("embedding 模型名称不能为空")
	}
	fallbacks := s.embeddingModelResultsFromFallbacks()
	if len(fallbacks) == 0 {
		return KnowledgeEmbeddingModelResult{}, fmt.Errorf("embedding 模型候选未配置")
	}
	if provider != "" {
		for _, model := range fallbacks {
			if model.Name == name && model.Provider == provider {
				return model, nil
			}
		}
		return KnowledgeEmbeddingModelResult{}, fmt.Errorf("embedding 模型不存在: %s@%s", name, provider)
	}
	var matches []KnowledgeEmbeddingModelResult
	for _, model := range fallbacks {
		if model.Name == name {
			matches = append(matches, model)
		}
	}
	switch len(matches) {
	case 0:
		return KnowledgeEmbeddingModelResult{}, fmt.Errorf("embedding 模型不存在: %s", name)
	case 1:
		return matches[0], nil
	default:
		return KnowledgeEmbeddingModelResult{}, fmt.Errorf("embedding 模型 provider 不能为空，模型 %s 存在多个 provider", name)
	}
}

// resolveDefaultEmbeddingSubmitValue 把配置默认 embedding 模型名解析为创建 RAGFlow dataset 需要的提交值。
// 模型名为空时返回空串以保留 RAGFlow 服务端默认行为；能在 fallback 白名单中按名唯一匹配时，
// 按官方 API 要求补全 provider 得到 name@provider；未配置白名单或匹配不到时回退提交原始名，
// 既保持旧的容错行为，又把格式校验交还 RAGFlow 服务端。
func (s *KnowledgeService) resolveDefaultEmbeddingSubmitValue() string {
	name := strings.TrimSpace(s.defaultEmbeddingModel)
	if name == "" {
		return ""
	}
	model, err := s.resolveKnowledgeEmbeddingModel(KnowledgeEmbeddingModelInput{Name: name})
	if err != nil {
		return name
	}
	return ragflowEmbeddingSubmitValue(model)
}

// ragflowEmbeddingSubmitValue 按官方 API 要求提交 model_name@model_factory；provider 为空时保留纯模型名。
func ragflowEmbeddingSubmitValue(model KnowledgeEmbeddingModelResult) string {
	name := strings.TrimSpace(model.Name)
	provider := strings.TrimSpace(model.Provider)
	if provider == "" {
		return name
	}
	if provider == "OpenAI-API-Compatible" && !strings.Contains(name, "___") {
		// RAGFlow 控制台展示 OpenAI compatible 模型的人类名，但 dataset API 需要内部模型名。
		name += "___OpenAI-API"
	}
	return name + "@" + provider
}

// toKnowledgeRAGFlowDatasetInfo 合并本地映射和远端实时信息，供平台运维界面展示。
func (s *KnowledgeService) toKnowledgeRAGFlowDatasetInfo(scope, targetID, targetName string, dataset sqlc.RagflowDataset, remote ragflow.Dataset) KnowledgeRAGFlowDatasetInfoResult {
	remoteID := strings.TrimSpace(remote.ID)
	if remoteID == "" {
		remoteID = strOrEmpty(dataset.RagflowDatasetID)
	}
	remoteName := strings.TrimSpace(remote.Name)
	if remoteName == "" {
		remoteName = dataset.Name
	}
	result := KnowledgeRAGFlowDatasetInfoResult{
		Scope:              scope,
		TargetID:           targetID,
		TargetName:         targetName,
		Status:             "ok",
		RAGFlowDatasetID:   remoteID,
		RAGFlowDatasetName: remoteName,
		DocNum:             remote.DocNum,
		ChunkNum:           remote.ChunkNum,
	}
	if !dataset.UpdatedAt.IsZero() {
		result.UpdatedAt = dataset.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if model := s.embeddingModelResultFromRemoteID(remote.EmbeddingModelID); model != nil {
		result.EmbeddingModel = model
	}
	return result
}

// embeddingModelResultFromRemoteID 将 RAGFlow 返回的 name@provider 或旧内部 ID 尽量映射回 UI 使用的人类可读模型。
func (s *KnowledgeService) embeddingModelResultFromRemoteID(remoteID string) *KnowledgeEmbeddingModelResult {
	remoteID = strings.TrimSpace(remoteID)
	if remoteID == "" {
		return nil
	}
	name, provider := splitRAGFlowEmbeddingRemoteID(remoteID)
	for _, model := range s.embeddingModelResultsFromFallbacks() {
		if model.Provider != provider && provider != "" {
			continue
		}
		if model.Name == name || strings.HasPrefix(name, model.Name+"___") {
			copy := model
			return &copy
		}
	}
	displayName := name
	if provider != "" {
		if idx := strings.Index(displayName, "___"); idx >= 0 {
			displayName = displayName[:idx]
		}
	}
	return &KnowledgeEmbeddingModelResult{Name: displayName, Label: displayName, Provider: provider, Available: true}
}

// splitRAGFlowEmbeddingRemoteID 拆分 RAGFlow 当前模型值；provider 本身不应包含 @，因此按最后一个 @ 处理。
func splitRAGFlowEmbeddingRemoteID(remoteID string) (string, string) {
	if at := strings.LastIndex(remoteID, "@"); at >= 0 {
		return strings.TrimSpace(remoteID[:at]), strings.TrimSpace(remoteID[at+1:])
	}
	return strings.TrimSpace(remoteID), ""
}

func (s *KnowledgeService) listDocuments(ctx context.Context, dataset sqlc.RagflowDataset, scope string, orgID, appID string, page, pageSize int32, keyword, status string, quotaBytes int64) (KnowledgeListResult, error) {
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
		OrgID:       null.StringFrom(orgID),
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
	usedBytes, err := s.knowledgeUsedBytes(ctx, scope, orgID, appID)
	if err != nil {
		return KnowledgeListResult{}, err
	}
	results := make([]KnowledgeDocumentResult, 0, len(items))
	for _, item := range items {
		results = append(results, toKnowledgeDocumentResult(item))
	}
	return KnowledgeListResult{
		Items:          results,
		Total:          total,
		UsedBytes:      usedBytes,
		QuotaBytes:     quotaBytes,
		RemainingBytes: knowledgeQuotaRemainingBytes(quotaBytes, usedBytes),
	}, nil
}

// knowledgeUsedBytes 汇总指定作用域的本地 document 大小，用于列表展示和上传前容量校验。
// 失败、停止或排队中的文件都仍占用 RAGFlow 原文件存储，因此不按解析状态过滤。
func (s *KnowledgeService) knowledgeUsedBytes(ctx context.Context, scope, orgID, appID string) (int64, error) {
	if s.store == nil {
		return 0, ErrKnowledgeMissing
	}
	appIDNull := null.String{}
	if appID != "" {
		appIDNull = null.StringFrom(appID)
	}
	used, err := s.store.SumRAGFlowDocumentsSizeByScope(ctx, sqlc.SumRAGFlowDocumentsSizeByScopeParams{
		ScopeType: scope,
		OrgID:     null.StringFrom(orgID),
		AppID:     appIDNull,
	})
	if err != nil {
		return 0, fmt.Errorf("统计知识库空间失败: %w", err)
	}
	return used, nil
}

// ensureKnowledgeQuotaAvailable 在上传到 RAGFlow 前检查累计容量，避免远端已写入后本地才发现超限。
// uploadBytes 允许等于剩余容量；只有剩余容量严格小于上传大小时才拒绝。
func (s *KnowledgeService) ensureKnowledgeQuotaAvailable(ctx context.Context, scope, orgID, appID string, quotaBytes, uploadBytes int64) error {
	if uploadBytes < 0 {
		return fmt.Errorf("知识库文件大小不能为负数")
	}
	used, err := s.knowledgeUsedBytes(ctx, scope, orgID, appID)
	if err != nil {
		return err
	}
	remaining := quotaBytes - used
	if remaining < uploadBytes {
		return fmt.Errorf("%w: 知识库空间不足，剩余 %s", ErrKnowledgeQuotaExceeded, formatKnowledgeBytes(knowledgeQuotaRemainingBytes(quotaBytes, used)))
	}
	return nil
}

// formatKnowledgeBytes 将字节数转成面向错误提示的短文案，优先展示整 GB，其次展示 MB。
// 不足 1MB 或无法整除 GB 的值保持字节/向下取整 MB，避免在错误信息中出现过长小数。
func formatKnowledgeBytes(value int64) string {
	const mb = 1024 * 1024
	const gb = 1024 * mb
	if value >= gb && value%gb == 0 {
		return fmt.Sprintf("%dGB", value/gb)
	}
	if value >= mb {
		return fmt.Sprintf("%dMB", value/mb)
	}
	return fmt.Sprintf("%dB", value)
}

// knowledgeUploadTarget 描述 document 写入的业务作用域，避免 org/app/industry 通过位置参数混用。
type knowledgeUploadTarget struct {
	Dataset                 sqlc.RagflowDataset
	AppID                   string
	IndustryKnowledgeBaseID string
	CreatedBy               string
}

func (s *KnowledgeService) uploadToDataset(ctx context.Context, target knowledgeUploadTarget, filename string, content io.Reader, size int64) (KnowledgeDocumentResult, error) {
	if filename = strings.TrimSpace(filename); filename == "" {
		return KnowledgeDocumentResult{}, fmt.Errorf("filename 不能为空")
	}
	dataset := target.Dataset
	if err := validateKnowledgeUploadTarget(target); err != nil {
		return KnowledgeDocumentResult{}, err
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
	if target.CreatedBy == "" {
		target.CreatedBy = "unknown"
	}
	suffix := strings.TrimPrefix(path.Ext(filename), ".")
	mimeType := mime.TypeByExtension(path.Ext(filename))
	// CreateRAGFlowDocument / CreateRAGFlowIndustryDocument 均为 :exec；预先生成 ID，写入后通过 GetRAGFlowDocument 读回。
	docID := newUUID()
	// AppID 为 null.String；仅在 appID 非空时填充（org 级别上传时留 NULL）。
	appIDNull := null.String{}
	if target.AppID != "" {
		appIDNull = null.StringFrom(target.AppID)
	}
	parseStatus := normalizeRAGFlowRun(remote.Run)
	if target.IndustryKnowledgeBaseID != "" {
		arg := sqlc.CreateRAGFlowIndustryDocumentParams{
			ID:                      docID,
			DatasetID:               dataset.ID,
			IndustryKnowledgeBaseID: null.StringFrom(target.IndustryKnowledgeBaseID),
			RagflowDocumentID:       remote.ID,
			Name:                    path.Base(filename),
			SizeBytes:               size,
			MimeType:                nullStr(mimeType),
			Suffix:                  nullStr(suffix),
			ParseStatus:             parseStatus,
			Progress:                progressForStatus(parseStatus),
			CreatedBy:               target.CreatedBy,
		}
		if err := s.store.CreateRAGFlowIndustryDocument(ctx, arg); err != nil {
			// 行业库文件名有唯一约束；本地落库失败时清理刚上传的远端文件，避免覆盖并发下留下孤儿 document。
			_ = s.ragflowClient().DeleteDocuments(ctx, remoteDatasetID, []string{remote.ID})
			return KnowledgeDocumentResult{}, fmt.Errorf("保存行业知识库文件元数据失败: %w", err)
		}
	} else {
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
			ParseStatus:       parseStatus,
			Progress:          progressForStatus(parseStatus),
			CreatedBy:         target.CreatedBy,
		}
		if err := s.store.CreateRAGFlowDocument(ctx, arg); err != nil {
			// 本地写入失败说明 DB 约束或连接异常，清理刚上传的远端 document，避免留下不可管理的文件。
			_ = s.ragflowClient().DeleteDocuments(ctx, remoteDatasetID, []string{remote.ID})
			return KnowledgeDocumentResult{}, fmt.Errorf("保存知识库文件元数据失败: %w", err)
		}
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

// validateKnowledgeUploadTarget 在远端上传前校验业务目标和 dataset scope，避免 DB 约束失败后才发现误用。
func validateKnowledgeUploadTarget(target knowledgeUploadTarget) error {
	dataset := target.Dataset
	switch dataset.ScopeType {
	case "org":
		if target.AppID != "" || target.IndustryKnowledgeBaseID != "" || !dataset.OrgID.Valid || dataset.AppID.Valid || dataset.IndustryKnowledgeBaseID.Valid {
			return fmt.Errorf("知识库上传目标与 dataset scope 不匹配")
		}
	case "app":
		if target.AppID == "" || target.IndustryKnowledgeBaseID != "" || !dataset.OrgID.Valid || !dataset.AppID.Valid || dataset.AppID.String != target.AppID || dataset.IndustryKnowledgeBaseID.Valid {
			return fmt.Errorf("知识库上传目标与 dataset scope 不匹配")
		}
	case "industry":
		if target.IndustryKnowledgeBaseID == "" || target.AppID != "" || dataset.OrgID.Valid || dataset.AppID.Valid || !dataset.IndustryKnowledgeBaseID.Valid || dataset.IndustryKnowledgeBaseID.String != target.IndustryKnowledgeBaseID {
			return fmt.Errorf("知识库上传目标与 dataset scope 不匹配")
		}
	default:
		return fmt.Errorf("知识库上传目标与 dataset scope 不匹配")
	}
	return nil
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

// clearDocuments 批量删除同一 dataset 下的文件；远端删除成功后再清理本地映射，避免本地先删导致文件不可管理。
func (s *KnowledgeService) clearDocuments(ctx context.Context, dataset sqlc.RagflowDataset, documents []sqlc.RagflowDocument) error {
	if len(documents) == 0 {
		return nil
	}
	remoteDatasetID, err := requireRemoteDatasetID(dataset)
	if err != nil {
		return err
	}
	remoteIDs := make([]string, 0, len(documents))
	for _, document := range documents {
		remoteIDs = append(remoteIDs, document.RagflowDocumentID)
	}
	if err := s.ragflowClient().DeleteDocuments(ctx, remoteDatasetID, remoteIDs); err != nil {
		return fmt.Errorf("删除 RAGFlow documents 失败: %w", err)
	}
	for _, document := range documents {
		if err := s.store.DeleteRAGFlowDocumentMapping(ctx, document.ID); err != nil {
			return fmt.Errorf("删除知识库文件元数据失败: %w", err)
		}
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
	// 人工重解析走专用 query MarkRAGFlowDocumentManualReparseQueued：把状态重置为 queued，
	// 交后台刷新任务继续推进。该 query 为 :exec；写入后通过 GetRAGFlowDocument 读回最新行。
	if err := s.store.MarkRAGFlowDocumentManualReparseQueued(ctx, document.ID); err != nil {
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

func searchHitsFromIndustryChunks(base sqlc.IndustryKnowledgeBasis, chunks []ragflow.RetrievalChunk) []KnowledgeSearchHit {
	hits := searchHitsFromChunks("industry", chunks)
	for i := range hits {
		hits[i].IndustryKnowledgeBaseID = base.ID
		hits[i].IndustryKnowledgeBaseName = base.Name
	}
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
	if document.ScopeType != scope {
		return sqlc.RagflowDocument{}, sqlc.RagflowDataset{}, ErrNotFound
	}
	if scope == "industry" {
		if strOrEmpty(document.IndustryKnowledgeBaseID) != appID {
			return sqlc.RagflowDocument{}, sqlc.RagflowDataset{}, ErrNotFound
		}
	} else if strOrEmpty(document.OrgID) != orgID {
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
		return s.getOrgDataset(ctx, strOrEmpty(document.OrgID))
	case "app":
		// AppID 是 null.String，取其字符串值传递给 getAppDataset。
		return s.getAppDataset(ctx, strOrEmpty(document.AppID))
	case "industry":
		base, err := s.getIndustryKnowledgeBase(ctx, strOrEmpty(document.IndustryKnowledgeBaseID))
		if err != nil {
			return sqlc.RagflowDataset{}, err
		}
		return s.getIndustryDataset(ctx, base)
	default:
		return sqlc.RagflowDataset{}, ErrNotFound
	}
}

func (s *KnowledgeService) getApp(ctx context.Context, principal auth.Principal, appID string, requireAICCManage bool) (sqlc.App, error) {
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
	if domain.IsAICCAppType(domain.AppType(app.AppType)) {
		if err := s.ensureAICCHiddenAppKnowledgeAccess(ctx, principal, app, requireAICCManage); err != nil {
			return sqlc.App{}, err
		}
	}
	return app, nil
}

// ensureAICCHiddenAppKnowledgeAccess 校验 AICC 隐藏实例知识库入口：
// 读取允许平台管理员只读排障和本企业管理员查看，写入仍只允许本企业管理员管理当前客服知识库。
// 未绑定 AICC 智能体的隐藏 app 继续按不存在处理，避免普通隐藏资源泄露。
func (s *KnowledgeService) ensureAICCHiddenAppKnowledgeAccess(ctx context.Context, principal auth.Principal, app sqlc.App, requireManage bool) error {
	agent, err := s.store.GetAICCAgentByAppID(ctx, app.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("查询 AICC 隐藏实例知识库归属失败: %w", err)
	}
	if agent.OrgID != app.OrgID {
		return ErrKnowledgeForbidden
	}
	if requireManage {
		if !auth.CanManageAICCAgent(principal, agent.OrgID) {
			return ErrKnowledgeForbidden
		}
		return nil
	}
	if !auth.CanViewAICC(principal, agent.OrgID) {
		return ErrKnowledgeForbidden
	}
	return nil
}

func (s *KnowledgeService) getOrg(ctx context.Context, orgID string) (sqlc.Organization, error) {
	if s.store == nil {
		return sqlc.Organization{}, ErrKnowledgeMissing
	}
	// orgID 直接按字符串查询；不存在或已软删除时对调用方统一表现为资源不存在。
	org, err := s.store.GetOrganization(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) || org.DeletedAt.Valid {
		return sqlc.Organization{}, ErrNotFound
	}
	if err != nil {
		return sqlc.Organization{}, fmt.Errorf("查询企业失败: %w", err)
	}
	return org, nil
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
	dataset, err := s.store.GetRAGFlowOrgDataset(ctx, null.StringFrom(orgID))
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

func (s *KnowledgeService) ensureDataset(ctx context.Context, scope string, orgID, appID, industryID string, name string) (sqlc.RagflowDataset, error) {
	if s.store == nil {
		return sqlc.RagflowDataset{}, ErrKnowledgeMissing
	}
	var (
		existing sqlc.RagflowDataset
		err      error
	)
	switch scope {
	case "org":
		existing, err = s.store.GetRAGFlowOrgDataset(ctx, null.StringFrom(orgID))
	case "app":
		existing, err = s.store.GetRAGFlowAppDataset(ctx, null.StringFrom(appID))
	case "industry":
		existing, err = s.store.GetRAGFlowIndustryDataset(ctx, null.StringFrom(industryID))
	default:
		return sqlc.RagflowDataset{}, ErrNotFound
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
	switch scope {
	case "org":
		err = s.store.CreateRAGFlowOrgDatasetMapping(ctx, sqlc.CreateRAGFlowOrgDatasetMappingParams{
			ID:               newID,
			OrgID:            null.StringFrom(orgID),
			Name:             name,
			CreateClaimToken: null.StringFrom(claimToken),
		})
	case "app":
		err = s.store.CreateRAGFlowAppDatasetMapping(ctx, sqlc.CreateRAGFlowAppDatasetMappingParams{
			ID:               newID,
			OrgID:            null.StringFrom(orgID),
			AppID:            null.StringFrom(appID),
			Name:             name,
			CreateClaimToken: null.StringFrom(claimToken),
		})
	case "industry":
		err = s.store.CreateRAGFlowIndustryDatasetMapping(ctx, sqlc.CreateRAGFlowIndustryDatasetMappingParams{
			ID:                      newID,
			IndustryKnowledgeBaseID: null.StringFrom(industryID),
			Name:                    name,
			CreateClaimToken:        null.StringFrom(claimToken),
		})
	}
	// 并发唯一索引冲突时 MySQL 忽略写入（INSERT IGNORE），读回已有行继续处理。
	if err != nil {
		return sqlc.RagflowDataset{}, fmt.Errorf("创建 RAGFlow dataset 映射失败: %w", err)
	}
	// 读回刚写入（或并发已有）的行。
	dataset, err := s.readDatasetAfterCreate(ctx, scope, orgID, appID, industryID, newID)
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
func (s *KnowledgeService) readDatasetAfterCreate(ctx context.Context, scope, orgID, appID, industryID, newID string) (sqlc.RagflowDataset, error) {
	if dataset, err := s.store.GetRAGFlowDataset(ctx, newID); err == nil {
		return dataset, nil
	}
	return s.datasetAfterCreateConflict(ctx, scope, orgID, appID, industryID)
}

func (s *KnowledgeService) datasetAfterCreateConflict(ctx context.Context, scope string, orgID, appID, industryID string) (sqlc.RagflowDataset, error) {
	var (
		existing sqlc.RagflowDataset
		err      error
	)
	switch scope {
	case "org":
		existing, err = s.store.GetRAGFlowOrgDataset(ctx, null.StringFrom(orgID))
	case "app":
		existing, err = s.store.GetRAGFlowAppDataset(ctx, null.StringFrom(appID))
	case "industry":
		existing, err = s.store.GetRAGFlowIndustryDataset(ctx, null.StringFrom(industryID))
	default:
		return sqlc.RagflowDataset{}, ErrNotFound
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
	// 创建远端 dataset 时把 manager 配置的默认模型一并提交；空值保持 RAGFlow 服务端默认行为。
	// RAGFlow 新版要求 embedding_model 为 name@provider 格式，必须经 fallback 白名单解析补全 provider，
	// 不能直接提交配置里的裸模型名（如 BAAI/bge-m3），否则远端按格式校验直接拒绝创建。
	remote, err := s.ragflowClient().CreateDataset(ctx, ragflow.CreateDatasetRequest{
		Name:           dataset.Name,
		ChunkMethod:    s.datasetChunkMethod,
		EmbeddingModel: s.resolveDefaultEmbeddingSubmitValue(),
	})
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
		return s.datasetAfterCreateConflict(ctx, dataset.ScopeType, strOrEmpty(dataset.OrgID), dataset.AppID.String, strOrEmpty(dataset.IndustryKnowledgeBaseID))
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

func (missingRAGFlowClient) CreateDataset(context.Context, ragflow.CreateDatasetRequest) (ragflow.Dataset, error) {
	return ragflow.Dataset{}, ErrKnowledgeMissing
}
func (missingRAGFlowClient) GetDataset(context.Context, string) (ragflow.Dataset, error) {
	return ragflow.Dataset{}, ErrKnowledgeMissing
}
func (missingRAGFlowClient) UpdateDatasetEmbeddingModel(context.Context, string, string) error {
	return ErrKnowledgeMissing
}
func (missingRAGFlowClient) RunDatasetEmbedding(context.Context, string) error {
	return ErrKnowledgeMissing
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
