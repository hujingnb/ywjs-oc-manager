package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"oc-manager/internal/auth"
	"oc-manager/internal/store/sqlc"
)

// SkillTicketAttachmentStore 是附件存取所需的最小数据能力。
type SkillTicketAttachmentStore interface {
	CreateSkillTicketAttachment(ctx context.Context, arg sqlc.CreateSkillTicketAttachmentParams) error
	ListSkillTicketAttachments(ctx context.Context, ticketID string) ([]sqlc.SkillTicketAttachment, error)
	GetSkillTicketAttachment(ctx context.Context, id string) (sqlc.SkillTicketAttachment, error)
}

// SkillTicketAttachmentService 管理工单附件:对象存储用 LibraryBlobStore 复用,
// key 走 library/ticket-attachment/<ticketID>/<attachmentID>.<ext>。
type SkillTicketAttachmentService struct {
	store SkillTicketAttachmentStore
	blobs LibraryBlobStore
}

// NewSkillTicketAttachmentService 构造附件 service。
func NewSkillTicketAttachmentService(store SkillTicketAttachmentStore, blobs LibraryBlobStore) *SkillTicketAttachmentService {
	return &SkillTicketAttachmentService{store: store, blobs: blobs}
}

// SkillTicketAttachmentResult 是附件对外视图。
type SkillTicketAttachmentResult struct {
	ID       string `json:"id"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
}

// safeExt 从文件名取扩展名(去点),非法或空时回退 "bin",供对象 key 路径段使用。
func safeExt(fileName string) string {
	ext := strings.TrimPrefix(path.Ext(fileName), ".")
	if ext == "" || strings.ContainsAny(ext, `/\`) {
		return "bin"
	}
	return ext
}

// Add 上传一个工单附件:权限由调用方(handler 前置 CanViewSkillTicket)保证,此处只校验入参与落地。
func (s *SkillTicketAttachmentService) Add(ctx context.Context, p auth.Principal, ticketID, fileName string, data []byte) (SkillTicketAttachmentResult, error) {
	name := strings.TrimSpace(fileName)
	if name == "" || len(data) == 0 {
		return SkillTicketAttachmentResult{}, fmt.Errorf("%w: 文件名与内容不能为空", ErrSkillTicketAttachmentInvalid)
	}
	id := newUUID()
	relPath, err := s.blobs.PutLibrarySkill("ticket-attachment", ticketID, id, safeExt(name), data)
	if err != nil {
		return SkillTicketAttachmentResult{}, fmt.Errorf("写入附件失败: %w", err)
	}
	if err := s.store.CreateSkillTicketAttachment(ctx, sqlc.CreateSkillTicketAttachmentParams{
		ID: id, TicketID: ticketID, ObjectPath: relPath, FileName: name,
		FileSize: int64(len(data)), UploadedBy: p.UserID,
		// CommentID 留空(随工单/评论上传场景由调用方决定;首版统一不关联评论)
	}); err != nil {
		_ = s.blobs.DeleteLibrarySkill(relPath)
		return SkillTicketAttachmentResult{}, fmt.Errorf("附件落库失败: %w", err)
	}
	return SkillTicketAttachmentResult{ID: id, FileName: name, FileSize: int64(len(data))}, nil
}

// List 列出某工单的附件元数据。
func (s *SkillTicketAttachmentService) List(ctx context.Context, ticketID string) ([]SkillTicketAttachmentResult, error) {
	rows, err := s.store.ListSkillTicketAttachments(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("查询附件失败: %w", err)
	}
	out := make([]SkillTicketAttachmentResult, 0, len(rows))
	for _, r := range rows {
		out = append(out, SkillTicketAttachmentResult{ID: r.ID, FileName: r.FileName, FileSize: r.FileSize})
	}
	return out, nil
}

// Open 按工单 id + 附件 id 打开内容,返回 ReadCloser 与原始文件名(供下载 Content-Disposition)。
// ticketID 用于归属校验:若附件不属于该工单,返回 ErrSkillTicketAttachmentNotFound 而非泄露附件存在性,
// 以防 IDOR(Insecure Direct Object Reference)越权访问他人工单附件。
func (s *SkillTicketAttachmentService) Open(ctx context.Context, ticketID, id string) (io.ReadCloser, string, error) {
	row, err := s.store.GetSkillTicketAttachment(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", ErrSkillTicketAttachmentNotFound
		}
		return nil, "", fmt.Errorf("查询附件失败: %w", err)
	}
	// 归属校验:附件必须属于 path 中的工单,不一致视为不存在(不泄露其他工单附件的存在性)。
	if row.TicketID != ticketID {
		return nil, "", ErrSkillTicketAttachmentNotFound
	}
	rc, err := s.blobs.OpenLibrarySkill(row.ObjectPath)
	if err != nil {
		return nil, "", fmt.Errorf("打开附件失败: %w", err)
	}
	return rc, row.FileName, nil
}
