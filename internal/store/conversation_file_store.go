// Package store —— conversation_file_store.go 把 sqlc 查询适配成
// service.ConversationFileStore 接口（sqlc.ConversationFile ↔ service.ConvFileRecord）。
package store

import (
	"context"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// 编译期确认 *ConversationFileStore 实现 service.ConversationFileStore。
var _ service.ConversationFileStore = (*ConversationFileStore)(nil)

// ConversationFileStore 把 *sqlc.Queries 适配为 service.ConversationFileStore。
type ConversationFileStore struct {
	q *sqlc.Queries
}

// NewConversationFileStore 构造适配器。
func NewConversationFileStore(q *sqlc.Queries) *ConversationFileStore {
	return &ConversationFileStore{q: q}
}

// CreateConversationFile 插入一条对话文件记录。
func (a *ConversationFileStore) CreateConversationFile(ctx context.Context, r service.ConvFileRecord) error {
	return a.q.CreateConversationFile(ctx, sqlc.CreateConversationFileParams{
		ID:        r.ID,
		AppID:     r.AppID,
		SessionID: r.SessionID,
		S3Key:     r.S3Key,
		Filename:  r.Filename,
		Mime:      r.Mime,
		Size:      r.Size,
	})
}

// GetConversationFile 按 id 读一条记录并转换为 service.ConvFileRecord。
func (a *ConversationFileStore) GetConversationFile(ctx context.Context, id string) (service.ConvFileRecord, error) {
	row, err := a.q.GetConversationFile(ctx, id)
	if err != nil {
		return service.ConvFileRecord{}, err
	}
	return service.ConvFileRecord{
		ID:        row.ID,
		AppID:     row.AppID,
		SessionID: row.SessionID,
		S3Key:     row.S3Key,
		Filename:  row.Filename,
		Mime:      row.Mime,
		Size:      row.Size,
	}, nil
}
