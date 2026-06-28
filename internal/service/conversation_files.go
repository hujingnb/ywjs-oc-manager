// Package service —— conversation_files.go 实现对话文件上传/下载。
// manager 把文件存 S3 并以 conversation_files 记录映射，支持历史渲染与下载；
// 文件本体不入 DB，权限沿用对话读写谓词。
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/storage"
)

var (
	// ErrConversationFileNotFound 文件记录不存在或归属校验失败。
	ErrConversationFileNotFound = errors.New("conversation file not found")
	// ErrConversationFileForbidden 当前主体无权操作该文件。
	ErrConversationFileForbidden = errors.New("conversation file forbidden")
	// ErrConversationFileUnsupported 文件扩展名不在白名单内。
	ErrConversationFileUnsupported = errors.New("conversation file type unsupported")
	// ErrConversationFileTooLarge 文件超出最大允许大小。
	ErrConversationFileTooLarge = errors.New("conversation file too large")
)

// conversationFileMaxBytes 单文件最大 100 MiB。
const conversationFileMaxBytes int64 = 100 * 1024 * 1024

// conversationFilePresignTTL 预签名 URL 有效期 10 分钟（供单轮下载）。
const conversationFilePresignTTL = 10 * time.Minute

// allowedConversationFileExts 文件扩展名白名单（小写）。
// 涵盖常见图片、文档、表格、演示、压缩与标记类型；禁止可执行或脚本扩展名。
var allowedConversationFileExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".bmp": true,
	".pdf": true, ".docx": true, ".doc": true, ".odt": true, ".rtf": true, ".txt": true,
	".md": true, ".epub": true, ".xlsx": true, ".xls": true, ".ods": true, ".csv": true,
	".tsv": true, ".json": true, ".xml": true, ".yaml": true, ".yml": true, ".pptx": true,
	".ppt": true, ".odp": true, ".key": true, ".zip": true, ".tar": true, ".gz": true,
	".tgz": true, ".bz2": true, ".xz": true, ".7z": true, ".rar": true, ".html": true, ".htm": true,
}

// ConvFileRecord 是 service 内部的对话文件记录（与 sqlc.ConversationFile 字段对齐，
// 由 store 适配层转换，避免 service 直接依赖 sqlc 类型）。
// 导出命名供 Task 5 的 store 包使用，无需跨包改名。
type ConvFileRecord struct {
	// ID 是文件的全局唯一标识（UUID）。
	ID string
	// AppID 是文件所属实例 ID，用于归属校验。
	AppID string
	// SessionID 是文件所属会话 ID，用于归属校验。
	SessionID string
	// S3Key 是文件在对象存储中的对象键。
	S3Key string
	// Filename 是上传时的原始文件名。
	Filename string
	// Mime 是按扩展名推导的 MIME 类型。
	Mime string
	// Size 是文件字节数。
	Size int64
}

// ConversationFileStore 是 conversation_files 表的最小持久化接口。
// 真实实现由 store 包提供，单测使用 fake 实现。
type ConversationFileStore interface {
	// CreateConversationFile 落库一条文件记录。
	CreateConversationFile(ctx context.Context, r ConvFileRecord) error
	// GetConversationFile 按文件 ID 查询记录；不存在时返回 ErrConversationFileNotFound。
	GetConversationFile(ctx context.Context, id string) (ConvFileRecord, error)
}

// conversationFileBlob 是对象存储的最小操作接口（PutObject + 预签名读）。
type conversationFileBlob interface {
	// PutObject 上传对象到指定键。
	PutObject(ctx context.Context, key string, r io.Reader, size int64) error
	// PresignGet 生成指定有效期的预签名 GET URL。
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// ConversationFileUploadResult 是上传成功后返回给调用方的元数据。
type ConversationFileUploadResult struct {
	FileID   string `json:"file_id"`
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
}

// ConversationFileService 提供对话文件的上传、预签名与下载能力。
type ConversationFileService struct {
	store    ConversationFileStore  // conversation_files 持久化
	blob     conversationFileBlob   // S3 对象存储操作
	resolver OcOpsResolver          // 把 appID 解析为 oc-ops 坐标（含 OrgID/OwnerUserID）
}

// NewConversationFileService 构造 ConversationFileService。
func NewConversationFileService(store ConversationFileStore, blob conversationFileBlob, resolver OcOpsResolver) *ConversationFileService {
	return &ConversationFileService{store: store, blob: blob, resolver: resolver}
}

// Upload 校验类型/大小，PutObject 到 S3，落 conversation_files 记录，返回 file_id 与元数据。
// 权限：调用方须满足 CanManageAppConversations（org_admin/owner/platform_admin）。
func (s *ConversationFileService) Upload(ctx context.Context, p auth.Principal, appID, sid, filename string, body io.Reader, size int64) (ConversationFileUploadResult, error) {
	// 解析 app 定位信息，用于权限判断。
	loc, err := s.resolver.Resolve(ctx, appID)
	if err != nil {
		return ConversationFileUploadResult{}, err
	}
	// 只有有权管理对话的主体才能上传文件。
	if !auth.CanManageAppConversations(p, loc.OrgID, loc.OwnerUserID) {
		return ConversationFileUploadResult{}, ErrConversationFileForbidden
	}
	// session id 格式校验，防路径注入。
	if err := validateSessionID(sid); err != nil {
		return ConversationFileUploadResult{}, err
	}
	// 大小校验：超出 100 MiB 拒绝。
	if size > conversationFileMaxBytes {
		return ConversationFileUploadResult{}, ErrConversationFileTooLarge
	}
	// 扩展名白名单校验：禁止可执行、脚本等危险类型。
	ext := strings.ToLower(filepath.Ext(filename))
	if !allowedConversationFileExts[ext] {
		return ConversationFileUploadResult{}, fmt.Errorf("%w: %s", ErrConversationFileUnsupported, ext)
	}
	// 生成全局唯一文件 ID，构造 S3 对象键。
	fileID := uuid.NewString()
	key := storage.ConversationFileKey(appID, sid, fileID, filename)
	// 上传文件本体到对象存储。
	if err := s.blob.PutObject(ctx, key, body, size); err != nil {
		return ConversationFileUploadResult{}, err
	}
	// 按扩展名推导 MIME；推导失败退回通用二进制类型。
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	// 落库文件记录，建立 file_id → S3 key 的映射。
	rec := ConvFileRecord{
		ID:        fileID,
		AppID:     appID,
		SessionID: sid,
		S3Key:     key,
		Filename:  filename,
		Mime:      mimeType,
		Size:      size,
	}
	if err := s.store.CreateConversationFile(ctx, rec); err != nil {
		return ConversationFileUploadResult{}, err
	}
	return ConversationFileUploadResult{FileID: fileID, Filename: filename, Mime: mimeType, Size: size}, nil
}

// ResolveFileURL 把 file_id 解析为预签名 GET URL（供 oc-ops 当轮下载使用）。
// 校验文件归属该 app+session，防越权引用他人文件。
// 不做角色权限判断：调用方 Chat/Download 已完成权限校验。
func (s *ConversationFileService) ResolveFileURL(ctx context.Context, appID, sid, fileID string) (url, filename, mimeType string, err error) {
	rec, err := s.store.GetConversationFile(ctx, fileID)
	if err != nil {
		// 统一返回 not found，不暴露 store 内部错误。
		return "", "", "", ErrConversationFileNotFound
	}
	// 校验文件必须归属请求的 app 与 session，防止跨实例/跨会话越权引用。
	if rec.AppID != appID || rec.SessionID != sid {
		return "", "", "", ErrConversationFileNotFound
	}
	u, err := s.blob.PresignGet(ctx, rec.S3Key, conversationFilePresignTTL)
	if err != nil {
		return "", "", "", err
	}
	return u, rec.Filename, rec.Mime, nil
}

// Download 供历史下载端点：校验读权限与归属，返回预签名 URL（handler 以 302 跳转）。
// 权限：调用方须满足 CanViewAppConversations（org_member 及以上/platform_admin）。
func (s *ConversationFileService) Download(ctx context.Context, p auth.Principal, appID, sid, fileID string) (url, filename string, err error) {
	loc, err := s.resolver.Resolve(ctx, appID)
	if err != nil {
		return "", "", err
	}
	// 只读权限即可下载历史文件。
	if !auth.CanViewAppConversations(p, loc.OrgID, loc.OwnerUserID) {
		return "", "", ErrConversationFileForbidden
	}
	u, fn, _, err := s.ResolveFileURL(ctx, appID, sid, fileID)
	return u, fn, err
}
