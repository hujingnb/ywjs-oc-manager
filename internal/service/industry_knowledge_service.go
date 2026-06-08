package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"strings"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/store/sqlc"
)

const (
	// externalIndustryKnowledgeCreatedBy 标记外部上传入口自动创建或写入的行业库记录。
	externalIndustryKnowledgeCreatedBy = "external:industry-knowledge"
)

// IndustryKnowledgeBaseResult 是平台管理面展示的行业知识库摘要。
type IndustryKnowledgeBaseResult struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DocumentCount int64  `json:"document_count"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// IndustryKnowledgeBaseListResult 是行业知识库分页列表返回。
type IndustryKnowledgeBaseListResult struct {
	Items []IndustryKnowledgeBaseResult `json:"items"`
	Total int64                         `json:"total"`
}

// IndustryKnowledgeUploadTokenResult 是平台管理员查看外部上传接口文档时需要的配置 token。
type IndustryKnowledgeUploadTokenResult struct {
	// UploadToken 是 industry_knowledge.upload_token 的当前配置值；为空表示外部上传入口禁用。
	UploadToken string `json:"upload_token"`
}

// IndustryKnowledgeBaseRef 是助手版本和检索命中的行业库来源引用。
type IndustryKnowledgeBaseRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CreateIndustryKnowledgeBase 创建平台级行业知识库；行业库不归属任何企业。
func (s *KnowledgeService) CreateIndustryKnowledgeBase(ctx context.Context, principal auth.Principal, name string) (IndustryKnowledgeBaseResult, error) {
	if !auth.CanManageIndustryKnowledge(principal) {
		return IndustryKnowledgeBaseResult{}, ErrKnowledgeForbidden
	}
	createdBy := strings.TrimSpace(principal.UserID)
	if createdBy == "" {
		createdBy = "platform:industry-knowledge"
	}
	row, err := s.createIndustryKnowledgeBase(ctx, name, createdBy)
	if err != nil {
		return IndustryKnowledgeBaseResult{}, err
	}
	return toIndustryKnowledgeBaseResult(row, 0), nil
}

// ListIndustryKnowledgeBases 分页列出行业知识库，供平台管理面检索和选择。
func (s *KnowledgeService) ListIndustryKnowledgeBases(ctx context.Context, principal auth.Principal, page, pageSize int32, keyword string) (IndustryKnowledgeBaseListResult, error) {
	if !auth.CanManageIndustryKnowledge(principal) {
		return IndustryKnowledgeBaseListResult{}, ErrKnowledgeForbidden
	}
	if s.store == nil {
		return IndustryKnowledgeBaseListResult{}, ErrKnowledgeMissing
	}
	page, pageSize = normalizePage(page, pageSize)
	var kw interface{}
	if k := strings.TrimSpace(keyword); k != "" {
		kw = k
	}
	rows, err := s.store.ListIndustryKnowledgeBases(ctx, sqlc.ListIndustryKnowledgeBasesParams{
		Keyword: kw,
		Limit:   pageSize,
		Offset:  (page - 1) * pageSize,
	})
	if err != nil {
		return IndustryKnowledgeBaseListResult{}, fmt.Errorf("查询行业知识库列表失败: %w", err)
	}
	total, err := s.store.CountIndustryKnowledgeBases(ctx, sqlc.CountIndustryKnowledgeBasesParams{Keyword: kw})
	if err != nil {
		return IndustryKnowledgeBaseListResult{}, fmt.Errorf("统计行业知识库失败: %w", err)
	}
	items := make([]IndustryKnowledgeBaseResult, 0, len(rows))
	for _, row := range rows {
		items = append(items, toIndustryKnowledgeBaseResultFromList(row))
	}
	return IndustryKnowledgeBaseListResult{Items: items, Total: total}, nil
}

// RenameIndustryKnowledgeBase 重命名平台级行业知识库，名称在未删除行业库中唯一。
func (s *KnowledgeService) RenameIndustryKnowledgeBase(ctx context.Context, principal auth.Principal, id, name string) (IndustryKnowledgeBaseResult, error) {
	if !auth.CanManageIndustryKnowledge(principal) {
		return IndustryKnowledgeBaseResult{}, ErrKnowledgeForbidden
	}
	base, err := s.getIndustryKnowledgeBase(ctx, id)
	if err != nil {
		return IndustryKnowledgeBaseResult{}, err
	}
	name, err = normalizeIndustryKnowledgeName(name)
	if err != nil {
		return IndustryKnowledgeBaseResult{}, err
	}
	if err := s.ensureIndustryKnowledgeNameAvailable(ctx, name, base.ID); err != nil {
		return IndustryKnowledgeBaseResult{}, err
	}
	if err := s.store.RenameIndustryKnowledgeBase(ctx, sqlc.RenameIndustryKnowledgeBaseParams{
		ID:   base.ID,
		Name: name,
	}); err != nil {
		if isIndustryKnowledgeNameDuplicate(err) {
			return IndustryKnowledgeBaseResult{}, ErrIndustryKnowledgeNameTaken
		}
		return IndustryKnowledgeBaseResult{}, fmt.Errorf("重命名行业知识库失败: %w", err)
	}
	renamed, err := s.getIndustryKnowledgeBase(ctx, base.ID)
	if err != nil {
		return IndustryKnowledgeBaseResult{}, err
	}
	return toIndustryKnowledgeBaseResult(renamed, 0), nil
}

// DeleteIndustryKnowledgeBase 删除行业知识库；仍被助手版本引用时禁止删除。
func (s *KnowledgeService) DeleteIndustryKnowledgeBase(ctx context.Context, principal auth.Principal, id string) error {
	if !auth.CanManageIndustryKnowledge(principal) {
		return ErrKnowledgeForbidden
	}
	var dataset sqlc.RagflowDataset
	var hasDataset bool
	if err := s.withKnowledgeTx(ctx, func(store KnowledgeStore) error {
		// 锁定行业库行，和版本关联写入的 SELECT ... FOR UPDATE 配合，避免删除与新增关联并发穿透。
		base, err := store.GetIndustryKnowledgeBaseForUpdate(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrIndustryKnowledgeNotFound
		}
		if err != nil {
			return fmt.Errorf("查询行业知识库失败: %w", err)
		}
		inUse, err := store.CountAssistantVersionsUsingIndustryKnowledgeBase(ctx, base.ID)
		if err != nil {
			return fmt.Errorf("检查行业知识库引用失败: %w", err)
		}
		if inUse > 0 {
			return ErrIndustryKnowledgeInUse
		}
		localDataset, err := store.GetRAGFlowIndustryDataset(ctx, null.StringFrom(base.ID))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("查询行业知识库 RAGFlow dataset 失败: %w", err)
		}
		if err == nil {
			dataset = localDataset
			hasDataset = true
		}
		affected, err := store.SoftDeleteIndustryKnowledgeBase(ctx, base.ID)
		if err != nil {
			return fmt.Errorf("删除行业知识库失败: %w", err)
		}
		if affected == 0 {
			return ErrIndustryKnowledgeInUse
		}
		if hasDataset {
			if err := store.DeleteRAGFlowDatasetMapping(ctx, dataset.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("删除行业知识库 dataset 映射失败: %w", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if hasDataset {
		if remoteDatasetID, remoteErr := requireRemoteDatasetID(dataset); remoteErr == nil {
			if err := s.ragflowClient().DeleteDatasets(ctx, []string{remoteDatasetID}); err != nil {
				return fmt.Errorf("删除 RAGFlow 行业 dataset 失败: %w", err)
			}
		}
	}
	return nil
}

// ExternalUploadIndustryFile 供后续外部上传 handler 使用固定 token 鉴权后写入行业库。
func (s *KnowledgeService) ExternalUploadIndustryFile(ctx context.Context, industryName, filename string, content io.Reader, size int64) (KnowledgeDocumentResult, error) {
	base, err := s.getOrCreateIndustryKnowledgeBaseByName(ctx, industryName, externalIndustryKnowledgeCreatedBy)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return s.saveIndustryFileForBase(ctx, base, filename, content, size, externalIndustryKnowledgeCreatedBy)
}

// SaveIndustryFile 上传行业知识库文件；平台侧管理不做累计容量限制。
func (s *KnowledgeService) SaveIndustryFile(ctx context.Context, principal auth.Principal, industryID, filename string, content io.Reader, size int64) (KnowledgeDocumentResult, error) {
	if !auth.CanManageIndustryKnowledge(principal) {
		return KnowledgeDocumentResult{}, ErrKnowledgeForbidden
	}
	base, err := s.getIndustryKnowledgeBase(ctx, industryID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	createdBy := strings.TrimSpace(principal.UserID)
	if createdBy == "" {
		createdBy = "platform:industry-knowledge"
	}
	return s.saveIndustryFileForBase(ctx, base, filename, content, size, createdBy)
}

// ListIndustryFiles 分页列出某个行业知识库文件，解析状态读取本地缓存。
// createdFrom 和 createdBefore 是已经由 handler 归一化后的 UTC 边界；零值表示不启用对应日期条件。
func (s *KnowledgeService) ListIndustryFiles(ctx context.Context, principal auth.Principal, industryID string, page, pageSize int32, keyword, status string, createdFrom, createdBefore time.Time) (KnowledgeListResult, error) {
	if !auth.CanManageIndustryKnowledge(principal) {
		return KnowledgeListResult{}, ErrKnowledgeForbidden
	}
	if _, err := s.getIndustryKnowledgeBase(ctx, industryID); err != nil {
		return KnowledgeListResult{}, err
	}
	if _, err := s.store.GetRAGFlowIndustryDataset(ctx, null.StringFrom(industryID)); errors.Is(err, sql.ErrNoRows) {
		return KnowledgeListResult{Items: []KnowledgeDocumentResult{}}, nil
	} else if err != nil {
		return KnowledgeListResult{}, fmt.Errorf("查询行业知识库 RAGFlow dataset 失败: %w", err)
	}
	page, pageSize = normalizePage(page, pageSize)
	var kw interface{}
	if k := strings.TrimSpace(keyword); k != "" {
		kw = k
	}
	params := sqlc.ListRAGFlowIndustryDocumentsParams{
		IndustryKnowledgeBaseID: null.StringFrom(industryID),
		ParseStatus:             nullStr(strings.TrimSpace(status)),
		Keywords:                kw,
		CreatedFrom:             nullTimeFromNonZero(createdFrom),
		CreatedBefore:           nullTimeFromNonZero(createdBefore),
		Limit:                   pageSize,
		Offset:                  (page - 1) * pageSize,
	}
	rows, err := s.store.ListRAGFlowIndustryDocuments(ctx, params)
	if err != nil {
		return KnowledgeListResult{}, fmt.Errorf("查询行业知识库文件列表失败: %w", err)
	}
	total, err := s.store.CountRAGFlowIndustryDocuments(ctx, sqlc.CountRAGFlowIndustryDocumentsParams{
		IndustryKnowledgeBaseID: params.IndustryKnowledgeBaseID,
		ParseStatus:             params.ParseStatus,
		Keywords:                params.Keywords,
		CreatedFrom:             params.CreatedFrom,
		CreatedBefore:           params.CreatedBefore,
	})
	if err != nil {
		return KnowledgeListResult{}, fmt.Errorf("统计行业知识库文件失败: %w", err)
	}
	items := make([]KnowledgeDocumentResult, 0, len(rows))
	for _, row := range rows {
		items = append(items, toKnowledgeDocumentResult(row))
	}
	return KnowledgeListResult{Items: items, Total: total}, nil
}

// nullTimeFromNonZero 把业务层「零值表示无筛选」转换为 sqlc 可空时间参数。
func nullTimeFromNonZero(value time.Time) null.Time {
	if value.IsZero() {
		return null.Time{}
	}
	return null.TimeFrom(value)
}

// OpenIndustryFile 打开行业知识库文件流供下载。
func (s *KnowledgeService) OpenIndustryFile(ctx context.Context, principal auth.Principal, industryID, documentID string) (io.ReadCloser, int64, string, error) {
	if !auth.CanManageIndustryKnowledge(principal) {
		return nil, 0, "", ErrKnowledgeForbidden
	}
	base, err := s.getIndustryKnowledgeBase(ctx, industryID)
	if err != nil {
		return nil, 0, "", err
	}
	document, dataset, err := s.getDocumentForScope(ctx, documentID, "industry", "", base.ID)
	if err != nil {
		return nil, 0, "", err
	}
	return s.openDocument(ctx, dataset, document)
}

// DeleteIndustryFile 删除行业知识库文件，并同步删除远端 RAGFlow document。
func (s *KnowledgeService) DeleteIndustryFile(ctx context.Context, principal auth.Principal, industryID, documentID string) error {
	if !auth.CanManageIndustryKnowledge(principal) {
		return ErrKnowledgeForbidden
	}
	base, err := s.getIndustryKnowledgeBase(ctx, industryID)
	if err != nil {
		return err
	}
	document, dataset, err := s.getDocumentForScope(ctx, documentID, "industry", "", base.ID)
	if err != nil {
		return err
	}
	return s.deleteDocument(ctx, dataset, document)
}

// ClearIndustryFiles 清空行业知识库下的全部文件内容，保留行业库记录和助手版本关联。
func (s *KnowledgeService) ClearIndustryFiles(ctx context.Context, principal auth.Principal, industryID string) error {
	if !auth.CanManageIndustryKnowledge(principal) {
		return ErrKnowledgeForbidden
	}
	base, err := s.getIndustryKnowledgeBase(ctx, industryID)
	if err != nil {
		return err
	}
	dataset, err := s.store.GetRAGFlowIndustryDataset(ctx, null.StringFrom(base.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("查询行业知识库 RAGFlow dataset 失败: %w", err)
	}
	documents, err := s.store.ListAllRAGFlowIndustryDocuments(ctx, null.StringFrom(base.ID))
	if err != nil {
		return fmt.Errorf("查询行业知识库全部文件失败: %w", err)
	}
	return s.clearDocuments(ctx, dataset, documents)
}

// ReparseIndustryFile 重新触发行业知识库文件解析。
func (s *KnowledgeService) ReparseIndustryFile(ctx context.Context, principal auth.Principal, industryID, documentID string) (KnowledgeDocumentResult, error) {
	if !auth.CanManageIndustryKnowledge(principal) {
		return KnowledgeDocumentResult{}, ErrKnowledgeForbidden
	}
	base, err := s.getIndustryKnowledgeBase(ctx, industryID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	document, dataset, err := s.getDocumentForScope(ctx, documentID, "industry", "", base.ID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return s.reparseDocument(ctx, dataset, document)
}

// EnsureIndustryDataset 确保行业知识库存在可用的 RAGFlow dataset。
func (s *KnowledgeService) EnsureIndustryDataset(ctx context.Context, base sqlc.IndustryKnowledgeBasis) (sqlc.RagflowDataset, error) {
	name := buildRAGFlowDatasetName("industry", base.Name, "", base.ID)
	return s.ensureDataset(ctx, "industry", "", "", base.ID, name)
}

// saveIndustryFileForBase 封装行业库上传流程：先按行业内归一化文件名覆盖旧文件，再写入新 document。
func (s *KnowledgeService) saveIndustryFileForBase(ctx context.Context, base sqlc.IndustryKnowledgeBasis, filename string, content io.Reader, size int64, createdBy string) (KnowledgeDocumentResult, error) {
	normalizedName, err := normalizeIndustryKnowledgeFilename(filename)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	dataset, err := s.getIndustryDataset(ctx, base)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	existing, err := s.store.GetRAGFlowIndustryDocumentByName(ctx, sqlc.GetRAGFlowIndustryDocumentByNameParams{
		IndustryKnowledgeBaseID: null.StringFrom(base.ID),
		Name:                    normalizedName,
	})
	if err == nil {
		return s.overwriteIndustryFile(ctx, dataset, existing, normalizedName, content, size, createdBy)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return KnowledgeDocumentResult{}, fmt.Errorf("查询同名行业知识库文件失败: %w", err)
	}
	return s.uploadToDataset(ctx, knowledgeUploadTarget{
		Dataset:                 dataset,
		IndustryKnowledgeBaseID: base.ID,
		CreatedBy:               createdBy,
	}, normalizedName, content, size)
}

// overwriteIndustryFile 先上传并触发新文件解析，再用旧远端 ID 做乐观替换，最后删除旧远端文件，避免并发覆盖留下不可管理的 RAGFlow document。
func (s *KnowledgeService) overwriteIndustryFile(ctx context.Context, dataset sqlc.RagflowDataset, existing sqlc.RagflowDocument, filename string, content io.Reader, size int64, createdBy string) (KnowledgeDocumentResult, error) {
	target := knowledgeUploadTarget{
		Dataset:                 dataset,
		IndustryKnowledgeBaseID: strOrEmpty(existing.IndustryKnowledgeBaseID),
		CreatedBy:               createdBy,
	}
	if err := validateKnowledgeUploadTarget(target); err != nil {
		return KnowledgeDocumentResult{}, err
	}
	remoteDatasetID, err := requireRemoteDatasetID(dataset)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	remote, err := s.ragflowClient().UploadDocument(ctx, remoteDatasetID, filename, content)
	if err != nil {
		return KnowledgeDocumentResult{}, fmt.Errorf("上传 RAGFlow document 失败: %w", err)
	}
	if remote.Size > 0 {
		size = remote.Size
	}
	if createdBy == "" {
		createdBy = "unknown"
	}
	parseStatus := normalizeRAGFlowRun(remote.Run)
	if parseStatus != "queued" && parseStatus != "running" {
		parseStatus = "queued"
	}
	if err := s.ragflowClient().ParseDocuments(ctx, remoteDatasetID, []string{remote.ID}); err != nil {
		_ = s.ragflowClient().DeleteDocuments(ctx, remoteDatasetID, []string{remote.ID})
		return KnowledgeDocumentResult{}, fmt.Errorf("触发 RAGFlow 解析失败: %w", err)
	}
	arg := sqlc.ReplaceRAGFlowIndustryDocumentParams{
		ID:                      existing.ID,
		DatasetID:               dataset.ID,
		IndustryKnowledgeBaseID: existing.IndustryKnowledgeBaseID,
		OldRagflowDocumentID:    existing.RagflowDocumentID,
		RagflowDocumentID:       remote.ID,
		Name:                    filename,
		SizeBytes:               size,
		MimeType:                nullStr(mime.TypeByExtension(path.Ext(filename))),
		Suffix:                  nullStr(strings.TrimPrefix(path.Ext(filename), ".")),
		ParseStatus:             parseStatus,
		Progress:                progressForStatus(parseStatus),
		LastError:               null.String{},
		CreatedBy:               createdBy,
	}
	if err := s.store.ReplaceRAGFlowIndustryDocument(ctx, arg); err != nil {
		_ = s.ragflowClient().DeleteDocuments(ctx, remoteDatasetID, []string{remote.ID})
		return KnowledgeDocumentResult{}, fmt.Errorf("替换行业知识库文件元数据失败: %w", err)
	}
	row, err := s.store.GetRAGFlowDocument(ctx, existing.ID)
	if err != nil {
		return KnowledgeDocumentResult{}, fmt.Errorf("读取覆盖后行业知识库文件失败: %w", err)
	}
	if row.RagflowDocumentID != remote.ID {
		_ = s.ragflowClient().DeleteDocuments(ctx, remoteDatasetID, []string{remote.ID})
		return KnowledgeDocumentResult{}, fmt.Errorf("%w: 行业知识库文件被并发覆盖，请重试", ErrConflict)
	}
	if existing.RagflowDocumentID != remote.ID {
		if err := s.ragflowClient().DeleteDocuments(ctx, remoteDatasetID, []string{existing.RagflowDocumentID}); err != nil {
			return KnowledgeDocumentResult{}, fmt.Errorf("删除旧 RAGFlow document 失败: %w", err)
		}
	}
	return toKnowledgeDocumentResult(row), nil
}

// getOrCreateIndustryKnowledgeBaseByName 供外部上传入口按行业名称自动定位行业库，不要求调用方预先知道 ID。
func (s *KnowledgeService) getOrCreateIndustryKnowledgeBaseByName(ctx context.Context, name, createdBy string) (sqlc.IndustryKnowledgeBasis, error) {
	name, err := normalizeIndustryKnowledgeName(name)
	if err != nil {
		return sqlc.IndustryKnowledgeBasis{}, err
	}
	if s.store == nil {
		return sqlc.IndustryKnowledgeBasis{}, ErrKnowledgeMissing
	}
	row, err := s.store.GetIndustryKnowledgeBaseByName(ctx, name)
	if err == nil && !row.DeletedAt.Valid {
		return row, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return sqlc.IndustryKnowledgeBasis{}, fmt.Errorf("查询行业知识库失败: %w", err)
	}
	created, err := s.createIndustryKnowledgeBase(ctx, name, createdBy)
	if errors.Is(err, ErrIndustryKnowledgeNameTaken) {
		row, readErr := s.store.GetIndustryKnowledgeBaseByName(ctx, name)
		if readErr == nil && !row.DeletedAt.Valid {
			return row, nil
		}
		if readErr != nil {
			return sqlc.IndustryKnowledgeBasis{}, fmt.Errorf("并发创建后读取行业知识库失败: %w", readErr)
		}
	}
	return created, err
}

// createIndustryKnowledgeBase 创建行业库并读回完整时间字段；同名并发由数据库唯一约束兜底。
func (s *KnowledgeService) createIndustryKnowledgeBase(ctx context.Context, name, createdBy string) (sqlc.IndustryKnowledgeBasis, error) {
	name, err := normalizeIndustryKnowledgeName(name)
	if err != nil {
		return sqlc.IndustryKnowledgeBasis{}, err
	}
	if s.store == nil {
		return sqlc.IndustryKnowledgeBasis{}, ErrKnowledgeMissing
	}
	id := newUUID()
	if err := s.store.CreateIndustryKnowledgeBase(ctx, sqlc.CreateIndustryKnowledgeBaseParams{
		ID:        id,
		Name:      name,
		CreatedBy: createdBy,
	}); err != nil {
		if isIndustryKnowledgeNameDuplicate(err) {
			return sqlc.IndustryKnowledgeBasis{}, ErrIndustryKnowledgeNameTaken
		}
		return sqlc.IndustryKnowledgeBasis{}, fmt.Errorf("创建行业知识库失败: %w", err)
	}
	row, err := s.getIndustryKnowledgeBase(ctx, id)
	if err != nil {
		return sqlc.IndustryKnowledgeBasis{}, err
	}
	return row, nil
}

// ensureIndustryKnowledgeNameAvailable 用于重命名前检查同名未删除行业库，排除当前记录自身。
func (s *KnowledgeService) ensureIndustryKnowledgeNameAvailable(ctx context.Context, name, excludeID string) error {
	existing, err := s.store.GetIndustryKnowledgeBaseByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("查询行业知识库名称失败: %w", err)
	}
	if existing.DeletedAt.Valid || existing.ID == excludeID {
		return nil
	}
	return ErrIndustryKnowledgeNameTaken
}

// getIndustryKnowledgeBase 统一把不存在和已删除行业库映射为行业库专属 NotFound。
func (s *KnowledgeService) getIndustryKnowledgeBase(ctx context.Context, id string) (sqlc.IndustryKnowledgeBasis, error) {
	if s.store == nil {
		return sqlc.IndustryKnowledgeBasis{}, ErrKnowledgeMissing
	}
	row, err := s.store.GetIndustryKnowledgeBase(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || row.DeletedAt.Valid {
		return sqlc.IndustryKnowledgeBasis{}, ErrIndustryKnowledgeNotFound
	}
	if err != nil {
		return sqlc.IndustryKnowledgeBasis{}, fmt.Errorf("查询行业知识库失败: %w", err)
	}
	return row, nil
}

// getIndustryDataset 读取或懒创建行业库 dataset；failed/creating 等非可用状态复用现有租约重试逻辑。
func (s *KnowledgeService) getIndustryDataset(ctx context.Context, base sqlc.IndustryKnowledgeBasis) (sqlc.RagflowDataset, error) {
	if s.store == nil {
		return sqlc.RagflowDataset{}, ErrKnowledgeMissing
	}
	dataset, err := s.store.GetRAGFlowIndustryDataset(ctx, null.StringFrom(base.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return s.EnsureIndustryDataset(ctx, base)
	}
	if err != nil {
		return sqlc.RagflowDataset{}, fmt.Errorf("查询行业知识库 RAGFlow dataset 失败: %w", err)
	}
	if dataset.Status != "active" || !dataset.RagflowDatasetID.Valid || strings.TrimSpace(dataset.RagflowDatasetID.String) == "" {
		return s.ensureExistingDataset(ctx, dataset)
	}
	return dataset, nil
}

// normalizeIndustryKnowledgeName 只做首尾空白裁剪，保留中文、空格和业务侧真实行业名称。
func normalizeIndustryKnowledgeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("行业知识库名称不能为空")
	}
	return name, nil
}

// normalizeIndustryKnowledgeFilename 将外部路径压成 RAGFlow 文件名，避免目录段参与同名覆盖判断。
func normalizeIndustryKnowledgeFilename(filename string) (string, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return "", fmt.Errorf("filename 不能为空")
	}
	name := path.Base(filename)
	if name == "." || name == "/" || strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("filename 不能为空")
	}
	return name, nil
}

// isIndustryKnowledgeNameDuplicate 识别行业库名称唯一约束冲突，仅匹配迁移中定义的精确唯一键名。
func isIndustryKnowledgeNameDuplicate(err error) bool {
	if err == nil {
		return false
	}
	return containsMySQLDuplicateKey(err.Error(), "uk_industry_knowledge_bases_name_active")
}

// toIndustryKnowledgeBaseResult 映射单行行业库记录；documentCount 由调用方按上下文传入。
func toIndustryKnowledgeBaseResult(row sqlc.IndustryKnowledgeBasis, documentCount int64) IndustryKnowledgeBaseResult {
	return IndustryKnowledgeBaseResult{
		ID:            row.ID,
		Name:          row.Name,
		DocumentCount: documentCount,
		CreatedAt:     row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     row.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// toIndustryKnowledgeBaseResultFromList 映射列表查询行，保留 SQL 聚合出的文件数。
func toIndustryKnowledgeBaseResultFromList(row sqlc.ListIndustryKnowledgeBasesRow) IndustryKnowledgeBaseResult {
	return IndustryKnowledgeBaseResult{
		ID:            row.ID,
		Name:          row.Name,
		DocumentCount: row.DocumentCount,
		CreatedAt:     row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     row.UpdatedAt.UTC().Format(time.RFC3339),
	}
}
