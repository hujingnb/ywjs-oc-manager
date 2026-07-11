package service

// 知识库分片上传 service 层：init（发起）/ uploadPart（逐片）/ complete（合并并推 RAGFlow）/
// abort（中止）四步，org 与 app 两套作用域。
//
// 背景：大文件单请求一次传完会撞上公网入口的固定超时（线上限速 512KB/s 时 77MB 需 ~155s）。
// 改为前端顺序逐片上传，每片是短请求，manager 用对象存储 multipart 暂存并服务端合并，complete
// 时再流式推给 RAGFlow。complete 复用原 uploadToDataset 的「建文档 + 触发解析」逻辑。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/storage"
)

const (
	// knowledgeUploadDefaultPartSize 是分片默认大小 8MB：既满足 S3 非末片 ≥5MB 的约束，
	// 又保证单片在 512KB/s 限速下约 16s 传完，远小于公网入口 ~48s 超时，留足余量。
	knowledgeUploadDefaultPartSize int64 = 8 * 1024 * 1024
	// maxKnowledgeFileBytes 是单文件硬上限 1GB，与 handler 直传路径 maxKnowledgeUploadBytes 对齐。
	maxKnowledgeFileBytes int64 = 1024 * 1024 * 1024
)

// ErrKnowledgeMultipartUnavailable 表示当前环境未启用对象存储，分片上传不可用（前端应回退直传）。
var ErrKnowledgeMultipartUnavailable = errors.New("knowledge multipart upload unavailable")

// KnowledgeUploadInitResult 是发起分片上传的返回：uploadID 用于后续分片/合并，partSize 指导前端切片。
type KnowledgeUploadInitResult struct {
	UploadID string `json:"upload_id"`
	PartSize int64  `json:"part_size"`
}

// partSize 返回当前分片大小，未配置时回退默认值。
func (s *KnowledgeService) partSize() int64 {
	if s.uploadPartSize > 0 {
		return s.uploadPartSize
	}
	return knowledgeUploadDefaultPartSize
}

// ensureMultipartEnabled 校验分片依赖已注入；未启用 S3 时返回 ErrKnowledgeMultipartUnavailable。
func (s *KnowledgeService) ensureMultipartEnabled() error {
	if s.blobStore == nil || s.uploadSessions == nil {
		return ErrKnowledgeMultipartUnavailable
	}
	return nil
}

// ---------- org 作用域 ----------

// InitOrgUpload 发起企业知识库分片上传：鉴权 + 配额预校验 + dataset 预检后建会话。
func (s *KnowledgeService) InitOrgUpload(ctx context.Context, principal auth.Principal, orgID, filename string, size int64) (KnowledgeUploadInitResult, error) {
	if err := s.ensureMultipartEnabled(); err != nil {
		return KnowledgeUploadInitResult{}, err
	}
	if !auth.CanWriteOrgKnowledge(principal, orgID) {
		return KnowledgeUploadInitResult{}, ErrKnowledgeForbidden
	}
	org, err := s.getOrg(ctx, orgID)
	if err != nil {
		return KnowledgeUploadInitResult{}, err
	}
	if err := s.ensureKnowledgeQuotaAvailable(ctx, "org", org.ID, "", org.KnowledgeQuotaBytes, size); err != nil {
		return KnowledgeUploadInitResult{}, err
	}
	// 提前触发 dataset 解析，dataset 异常时尽早失败，避免传完分片才发现没有可写目标。
	if _, err := s.getOrgDataset(ctx, org.ID); err != nil {
		return KnowledgeUploadInitResult{}, err
	}
	return s.startMultipart(ctx, knowledgeUploadSession{Scope: "org", OrgID: org.ID, Filename: filename, Size: size})
}

// UploadOrgPart 上传企业知识库分片：校验会话归属当前组织后写分片。
func (s *KnowledgeService) UploadOrgPart(ctx context.Context, principal auth.Principal, orgID, uploadID string, partNumber int32, body io.Reader, size int64) error {
	if err := s.ensureMultipartEnabled(); err != nil {
		return err
	}
	if !auth.CanWriteOrgKnowledge(principal, orgID) {
		return ErrKnowledgeForbidden
	}
	sess, err := s.loadScopedSession(ctx, uploadID, "org", orgID, "")
	if err != nil {
		return err
	}
	return s.uploadPart(ctx, sess, uploadID, partNumber, body, size)
}

// CompleteOrgUpload 合并企业知识库分片并推送 RAGFlow，复用 uploadToDataset 建文档+触发解析。
func (s *KnowledgeService) CompleteOrgUpload(ctx context.Context, principal auth.Principal, orgID, uploadID string) (KnowledgeDocumentResult, error) {
	if err := s.ensureMultipartEnabled(); err != nil {
		return KnowledgeDocumentResult{}, err
	}
	if !auth.CanWriteOrgKnowledge(principal, orgID) {
		return KnowledgeDocumentResult{}, ErrKnowledgeForbidden
	}
	sess, err := s.loadScopedSession(ctx, uploadID, "org", orgID, "")
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	org, err := s.getOrg(ctx, orgID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	dataset, err := s.getOrgDataset(ctx, org.ID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return s.completeMultipart(ctx, sess, uploadID, knowledgeUploadTarget{Dataset: dataset, CreatedBy: principal.UserID})
}

// AbortOrgUpload 中止企业知识库分片上传并清理暂存。
func (s *KnowledgeService) AbortOrgUpload(ctx context.Context, principal auth.Principal, orgID, uploadID string) error {
	if err := s.ensureMultipartEnabled(); err != nil {
		return err
	}
	if !auth.CanWriteOrgKnowledge(principal, orgID) {
		return ErrKnowledgeForbidden
	}
	return s.abortScoped(ctx, uploadID, "org", orgID, "")
}

// ---------- app 作用域 ----------

// InitAppUpload 发起实例知识库分片上传；权限由 app 真实 owner/org 决定。
func (s *KnowledgeService) InitAppUpload(ctx context.Context, principal auth.Principal, appID, filename string, size int64) (KnowledgeUploadInitResult, error) {
	if err := s.ensureMultipartEnabled(); err != nil {
		return KnowledgeUploadInitResult{}, err
	}
	app, err := s.getApp(ctx, principal, appID)
	if err != nil {
		return KnowledgeUploadInitResult{}, err
	}
	if !auth.CanWriteAppKnowledge(principal, app.OrgID, app.OwnerUserID) {
		return KnowledgeUploadInitResult{}, ErrKnowledgeForbidden
	}
	if err := s.ensureKnowledgeQuotaAvailable(ctx, "app", app.OrgID, app.ID, app.KnowledgeQuotaBytes, size); err != nil {
		return KnowledgeUploadInitResult{}, err
	}
	if _, err := s.getAppDataset(ctx, app.ID); err != nil {
		return KnowledgeUploadInitResult{}, err
	}
	return s.startMultipart(ctx, knowledgeUploadSession{Scope: "app", AppID: app.ID, Filename: filename, Size: size})
}

// UploadAppPart 上传实例知识库分片：校验会话归属当前应用后写分片。
func (s *KnowledgeService) UploadAppPart(ctx context.Context, principal auth.Principal, appID, uploadID string, partNumber int32, body io.Reader, size int64) error {
	if err := s.ensureMultipartEnabled(); err != nil {
		return err
	}
	app, err := s.getApp(ctx, principal, appID)
	if err != nil {
		return err
	}
	if !auth.CanWriteAppKnowledge(principal, app.OrgID, app.OwnerUserID) {
		return ErrKnowledgeForbidden
	}
	sess, err := s.loadScopedSession(ctx, uploadID, "app", "", app.ID)
	if err != nil {
		return err
	}
	return s.uploadPart(ctx, sess, uploadID, partNumber, body, size)
}

// CompleteAppUpload 合并实例知识库分片并推送 RAGFlow。
func (s *KnowledgeService) CompleteAppUpload(ctx context.Context, principal auth.Principal, appID, uploadID string) (KnowledgeDocumentResult, error) {
	if err := s.ensureMultipartEnabled(); err != nil {
		return KnowledgeDocumentResult{}, err
	}
	app, err := s.getApp(ctx, principal, appID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	if !auth.CanWriteAppKnowledge(principal, app.OrgID, app.OwnerUserID) {
		return KnowledgeDocumentResult{}, ErrKnowledgeForbidden
	}
	sess, err := s.loadScopedSession(ctx, uploadID, "app", "", app.ID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	dataset, err := s.getAppDataset(ctx, app.ID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return s.completeMultipart(ctx, sess, uploadID, knowledgeUploadTarget{Dataset: dataset, AppID: app.ID, CreatedBy: principal.UserID})
}

// AbortAppUpload 中止实例知识库分片上传并清理暂存。
func (s *KnowledgeService) AbortAppUpload(ctx context.Context, principal auth.Principal, appID, uploadID string) error {
	if err := s.ensureMultipartEnabled(); err != nil {
		return err
	}
	app, err := s.getApp(ctx, principal, appID)
	if err != nil {
		return err
	}
	if !auth.CanWriteAppKnowledge(principal, app.OrgID, app.OwnerUserID) {
		return ErrKnowledgeForbidden
	}
	return s.abortScoped(ctx, uploadID, "app", "", app.ID)
}

// ---------- 通用核心 ----------

// startMultipart 校验文件名/大小，发起对象存储 multipart 并建会话；建会话失败时回滚 multipart。
func (s *KnowledgeService) startMultipart(ctx context.Context, sess knowledgeUploadSession) (KnowledgeUploadInitResult, error) {
	filename := strings.TrimSpace(path.Base(sess.Filename))
	if filename == "" || filename == "." || filename == "/" {
		return KnowledgeUploadInitResult{}, fmt.Errorf("文件名非法")
	}
	if sess.Size <= 0 {
		return KnowledgeUploadInitResult{}, fmt.Errorf("缺少有效的文件大小信息")
	}
	if sess.Size > maxKnowledgeFileBytes {
		return KnowledgeUploadInitResult{}, fmt.Errorf("单文件最大支持 %dMB", maxKnowledgeFileBytes/(1024*1024))
	}
	uploadID := newUUID()
	key := storage.KnowledgeUploadKey(uploadID, filename)
	s3UploadID, err := s.blobStore.CreateMultipartUpload(ctx, key)
	if err != nil {
		return KnowledgeUploadInitResult{}, err
	}
	sess.Filename = filename
	sess.Key = key
	sess.S3UploadID = s3UploadID
	if err := s.uploadSessions.Create(ctx, uploadID, sess); err != nil {
		// 会话落库失败，回滚已发起的 multipart，避免对象存储里留下孤儿上传。
		_ = s.blobStore.AbortMultipartUpload(context.Background(), key, s3UploadID)
		return KnowledgeUploadInitResult{}, err
	}
	return KnowledgeUploadInitResult{UploadID: uploadID, PartSize: s.partSize()}, nil
}

// uploadPart 把单个分片写入对象存储并记录 ETag。
func (s *KnowledgeService) uploadPart(ctx context.Context, sess knowledgeUploadSession, uploadID string, partNumber int32, body io.Reader, size int64) error {
	if partNumber < 1 {
		return fmt.Errorf("分片序号非法")
	}
	etag, err := s.blobStore.UploadPart(ctx, sess.Key, sess.S3UploadID, partNumber, body, size)
	if err != nil {
		return err
	}
	return s.uploadSessions.PutPart(ctx, uploadID, partNumber, etag)
}

// completeMultipart 合并分片为完整对象，流式推给 RAGFlow，再清理暂存对象与会话。
func (s *KnowledgeService) completeMultipart(ctx context.Context, sess knowledgeUploadSession, uploadID string, target knowledgeUploadTarget) (KnowledgeDocumentResult, error) {
	parts, err := s.uploadSessions.ListParts(ctx, uploadID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	if len(parts) == 0 {
		return KnowledgeDocumentResult{}, fmt.Errorf("没有已上传的分片")
	}
	if err := s.blobStore.CompleteMultipartUpload(ctx, sess.Key, sess.S3UploadID, parts); err != nil {
		return KnowledgeDocumentResult{}, err
	}
	reader, size, err := s.blobStore.OpenObject(ctx, sess.Key)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	defer reader.Close()
	result, uploadErr := s.uploadToDataset(ctx, target, sess.Filename, reader, size)
	// 无论成败都清理暂存对象与会话：知识库文件最终落在 RAGFlow，对象存储仅作分片中转。
	// 用 Background ctx，避免请求 ctx 已取消导致清理被跳过而留下孤儿对象。
	_ = s.blobStore.DeleteObject(context.Background(), sess.Key)
	_ = s.uploadSessions.Delete(context.Background(), uploadID)
	if uploadErr != nil {
		return KnowledgeDocumentResult{}, uploadErr
	}
	return result, nil
}

// abortScoped 校验会话归属后中止 multipart 并删会话；会话已不存在视为成功（幂等）。
func (s *KnowledgeService) abortScoped(ctx context.Context, uploadID, scope, orgID, appID string) error {
	sess, err := s.loadScopedSession(ctx, uploadID, scope, orgID, appID)
	if errors.Is(err, ErrKnowledgeUploadSessionNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_ = s.blobStore.AbortMultipartUpload(ctx, sess.Key, sess.S3UploadID)
	return s.uploadSessions.Delete(ctx, uploadID)
}

// loadScopedSession 读取会话并校验其作用域与归属，防止拿别的组织/应用的 uploadID 越权操作。
func (s *KnowledgeService) loadScopedSession(ctx context.Context, uploadID, scope, orgID, appID string) (knowledgeUploadSession, error) {
	sess, err := s.uploadSessions.Get(ctx, uploadID)
	if err != nil {
		return knowledgeUploadSession{}, err
	}
	if sess.Scope != scope || sess.OrgID != orgID || sess.AppID != appID {
		// 归属不符按「会话不存在」处理，不泄露他人会话存在性。
		return knowledgeUploadSession{}, ErrKnowledgeUploadSessionNotFound
	}
	return sess, nil
}
