package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"oc-manager/internal/auth"
	"oc-manager/internal/store/sqlc"
)

// 消息类型常量:kind 作为前端判别联合的分派字段,body JSON 保存各类型负载。
const (
	MessageKindText  = "text"
	MessageKindImage = "image"
	MessageKindFile  = "file"
)

// textPayload 是 text 消息的 body JSON 结构。
type textPayload struct {
	Text string `json:"text"`
}

// filePayload 是 image/file 消息的 body JSON 结构;object_path 仅后端用于鉴权下载。
type filePayload struct {
	ObjectPath  string `json:"object_path"`
	FileName    string `json:"file_name"`
	FileSize    int64  `json:"file_size"`
	ContentType string `json:"content_type"`
}

// SkillTicketMessageStore 是消息流所需的最小数据能力。
type SkillTicketMessageStore interface {
	GetSkillTicket(ctx context.Context, id string) (sqlc.SkillTicket, error)
	UpdateSkillTicketStatus(ctx context.Context, arg sqlc.UpdateSkillTicketStatusParams) error
	TouchSkillTicket(ctx context.Context, id string) error
	CreateSkillTicketMessage(ctx context.Context, arg sqlc.CreateSkillTicketMessageParams) error
	ListSkillTicketMessages(ctx context.Context, ticketID string) ([]sqlc.SkillTicketMessage, error)
	GetSkillTicketMessage(ctx context.Context, id string) (sqlc.SkillTicketMessage, error)
}

// SkillTicketMessageService 管理工单统一消息流,取代旧 comments + attachments 两套模型。
type SkillTicketMessageService struct {
	store SkillTicketMessageStore
	blobs LibraryBlobStore
}

// NewSkillTicketMessageService 构造工单消息 service。
func NewSkillTicketMessageService(store SkillTicketMessageStore, blobs LibraryBlobStore) *SkillTicketMessageService {
	return &SkillTicketMessageService{store: store, blobs: blobs}
}

// SkillTicketMessageResult 是消息对外视图;按 kind 填充 text 或文件字段。
type SkillTicketMessageResult struct {
	ID           string    `json:"id"`
	AuthorUserID string    `json:"author_user_id"`
	Kind         string    `json:"kind"`
	Text         string    `json:"text,omitempty"`
	FileName     string    `json:"file_name,omitempty"`
	FileSize     int64     `json:"file_size,omitempty"`
	ContentType  string    `json:"content_type,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// loadTicketForMessage 取工单并做可见性校验:仅提交者本人或平台管理员可读写消息。
func (s *SkillTicketMessageService) loadTicketForMessage(ctx context.Context, p auth.Principal, ticketID string) (sqlc.SkillTicket, error) {
	row, err := s.store.GetSkillTicket(ctx, ticketID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sqlc.SkillTicket{}, ErrSkillTicketNotFound
		}
		return sqlc.SkillTicket{}, fmt.Errorf("查询工单失败: %w", err)
	}
	if !auth.CanViewSkillTicket(p, row.RequesterUserID) {
		return sqlc.SkillTicket{}, ErrSkillTicketDenied
	}
	return row, nil
}

// reopenIfRequesterOnClosed 实现“需求方在关闭态补充任意消息即重开”的业务规则。
func (s *SkillTicketMessageService) reopenIfRequesterOnClosed(ctx context.Context, p auth.Principal, t sqlc.SkillTicket) error {
	closed := t.Status == SkillTicketStatusDelivered || t.Status == SkillTicketStatusRejected
	if p.UserID == t.RequesterUserID && closed {
		return s.store.UpdateSkillTicketStatus(ctx, sqlc.UpdateSkillTicketStatusParams{Status: SkillTicketStatusPending, ID: t.ID})
	}
	_ = s.store.TouchSkillTicket(ctx, t.ID)
	return nil
}

// SendText 发送纯文本消息;空白文本按非法入参处理。
func (s *SkillTicketMessageService) SendText(ctx context.Context, p auth.Principal, ticketID, text string) (SkillTicketMessageResult, error) {
	ticket, err := s.loadTicketForMessage(ctx, p, ticketID)
	if err != nil {
		return SkillTicketMessageResult{}, err
	}
	body := strings.TrimSpace(text)
	if body == "" {
		return SkillTicketMessageResult{}, fmt.Errorf("%w: 消息不能为空", ErrSkillTicketInvalid)
	}
	raw, err := json.Marshal(textPayload{Text: body})
	if err != nil {
		return SkillTicketMessageResult{}, fmt.Errorf("序列化消息失败: %w", err)
	}
	id := newUUID()
	createdAt := time.Now().UTC()
	if err := s.store.CreateSkillTicketMessage(ctx, sqlc.CreateSkillTicketMessageParams{
		ID: id, TicketID: ticketID, AuthorUserID: p.UserID, Kind: MessageKindText, Body: raw,
	}); err != nil {
		return SkillTicketMessageResult{}, fmt.Errorf("发送消息失败: %w", err)
	}
	if err := s.reopenIfRequesterOnClosed(ctx, p, ticket); err != nil {
		return SkillTicketMessageResult{}, fmt.Errorf("重开工单失败: %w", err)
	}
	return SkillTicketMessageResult{ID: id, AuthorUserID: p.UserID, Kind: MessageKindText, Text: body, CreatedAt: createdAt}, nil
}

// SendFile 发送图片或普通文件消息;content_type 为 image/* 时前端按图片内嵌预览渲染。
func (s *SkillTicketMessageService) SendFile(ctx context.Context, p auth.Principal, ticketID, fileName, contentType string, data []byte) (SkillTicketMessageResult, error) {
	ticket, err := s.loadTicketForMessage(ctx, p, ticketID)
	if err != nil {
		return SkillTicketMessageResult{}, err
	}
	name := strings.TrimSpace(fileName)
	if name == "" || len(data) == 0 {
		return SkillTicketMessageResult{}, fmt.Errorf("%w: 文件名与内容不能为空", ErrSkillTicketInvalid)
	}
	ct := strings.TrimSpace(contentType)
	if ct == "" {
		ct = "application/octet-stream"
	}
	kind := MessageKindFile
	if strings.HasPrefix(ct, "image/") {
		kind = MessageKindImage
	}
	id := newUUID()
	relPath, err := s.blobs.PutLibrarySkill("ticket-message", ticketID, id, safeExt(name), data)
	if err != nil {
		return SkillTicketMessageResult{}, fmt.Errorf("写入消息文件失败: %w", err)
	}
	raw, err := json.Marshal(filePayload{ObjectPath: relPath, FileName: name, FileSize: int64(len(data)), ContentType: ct})
	if err != nil {
		_ = s.blobs.DeleteLibrarySkill(relPath)
		return SkillTicketMessageResult{}, fmt.Errorf("序列化文件消息失败: %w", err)
	}
	createdAt := time.Now().UTC()
	if err := s.store.CreateSkillTicketMessage(ctx, sqlc.CreateSkillTicketMessageParams{
		ID: id, TicketID: ticketID, AuthorUserID: p.UserID, Kind: kind, Body: raw,
	}); err != nil {
		_ = s.blobs.DeleteLibrarySkill(relPath)
		return SkillTicketMessageResult{}, fmt.Errorf("发送文件消息失败: %w", err)
	}
	if err := s.reopenIfRequesterOnClosed(ctx, p, ticket); err != nil {
		return SkillTicketMessageResult{}, fmt.Errorf("重开工单失败: %w", err)
	}
	return SkillTicketMessageResult{
		ID: id, AuthorUserID: p.UserID, Kind: kind, FileName: name,
		FileSize: int64(len(data)), ContentType: ct, CreatedAt: createdAt,
	}, nil
}

// ListMessages 列出工单消息并把 body JSON 解析为前端可直接消费的判别联合。
func (s *SkillTicketMessageService) ListMessages(ctx context.Context, p auth.Principal, ticketID string) ([]SkillTicketMessageResult, error) {
	if _, err := s.loadTicketForMessage(ctx, p, ticketID); err != nil {
		return nil, err
	}
	rows, err := s.store.ListSkillTicketMessages(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("查询消息失败: %w", err)
	}
	out := make([]SkillTicketMessageResult, 0, len(rows))
	for _, row := range rows {
		out = append(out, toMessageResult(row))
	}
	return out, nil
}

// DownloadFile 取回图片/文件消息的原始字节;text 消息没有可下载内容。
func (s *SkillTicketMessageService) DownloadFile(ctx context.Context, p auth.Principal, ticketID, messageID string) ([]byte, string, string, error) {
	if _, err := s.loadTicketForMessage(ctx, p, ticketID); err != nil {
		return nil, "", "", err
	}
	row, err := s.store.GetSkillTicketMessage(ctx, messageID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", "", ErrSkillTicketNotFound
		}
		return nil, "", "", fmt.Errorf("查询消息失败: %w", err)
	}
	if row.TicketID != ticketID || (row.Kind != MessageKindImage && row.Kind != MessageKindFile) {
		return nil, "", "", fmt.Errorf("%w: 非文件消息", ErrSkillTicketInvalid)
	}
	var payload filePayload
	if err := json.Unmarshal(row.Body, &payload); err != nil {
		return nil, "", "", fmt.Errorf("解析文件消息失败: %w", err)
	}
	rc, err := s.blobs.OpenLibrarySkill(payload.ObjectPath)
	if err != nil {
		return nil, "", "", fmt.Errorf("打开消息文件失败: %w", err)
	}
	defer func() { _ = rc.Close() }()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		return nil, "", "", fmt.Errorf("读取消息文件失败: %w", err)
	}
	return buf.Bytes(), payload.FileName, payload.ContentType, nil
}

// toMessageResult 将 sqlc 行按 kind 解析为对外视图;坏 JSON 不让列表崩溃,保持空字段暴露异常数据。
func toMessageResult(row sqlc.SkillTicketMessage) SkillTicketMessageResult {
	out := SkillTicketMessageResult{
		ID: row.ID, AuthorUserID: row.AuthorUserID, Kind: row.Kind, CreatedAt: row.CreatedAt,
	}
	switch row.Kind {
	case MessageKindText:
		var payload textPayload
		_ = json.Unmarshal(row.Body, &payload)
		out.Text = payload.Text
	case MessageKindImage, MessageKindFile:
		var payload filePayload
		_ = json.Unmarshal(row.Body, &payload)
		out.FileName = payload.FileName
		out.FileSize = payload.FileSize
		out.ContentType = payload.ContentType
	}
	return out
}

// safeExt 从文件名取扩展名(不含点);空扩展或可疑分隔符统一回退 bin,避免对象 key 路径异常。
func safeExt(fileName string) string {
	ext := strings.TrimPrefix(path.Ext(fileName), ".")
	if ext == "" || strings.ContainsAny(ext, `/\`) {
		return "bin"
	}
	return ext
}
