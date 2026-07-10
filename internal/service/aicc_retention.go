package service

import (
	"context"
	"fmt"

	null "github.com/guregu/null/v5"

	"oc-manager/internal/store/sqlc"
)

// AICCRetentionStore 是 AICC 保留期清理依赖的数据访问接口。
type AICCRetentionStore interface {
	// ListExpiredAICCSessions 按过期时间读取待清理会话，调用方限制单批数量。
	ListExpiredAICCSessions(ctx context.Context, limit int32) ([]sqlc.AiccSession, error)
	// ListAICCImageObjectKeysBySession 读取会话内访客图片对象 key，用于删库前清理对象存储。
	ListAICCImageObjectKeysBySession(ctx context.Context, sessionID string) ([]string, error)
	// ClearAICCLeadLatestSession 清空引用待删除会话的线索最近会话，避免删除 session 时被外键阻塞。
	ClearAICCLeadLatestSession(ctx context.Context, latestSessionID null.String) error
	// DeleteAICCSession 删除会话；消息、图片和留资值由外键级联或置空处理。
	DeleteAICCSession(ctx context.Context, id string) error
}

// AICCObjectCleaner 抽象对象存储删除能力，避免保留期清理服务依赖具体 S3 实现。
type AICCObjectCleaner interface {
	// DeleteObject 删除对象存储中的单个对象 key。
	DeleteObject(ctx context.Context, key string) error
}

// AICCRetentionService 负责按会话过期时间清理 AICC 访客数据。
type AICCRetentionService struct {
	store AICCRetentionStore
	blob  AICCObjectCleaner
}

// NewAICCRetentionService 创建 AICC 保留期清理服务。
func NewAICCRetentionService(store AICCRetentionStore, blob AICCObjectCleaner) *AICCRetentionService {
	return &AICCRetentionService{store: store, blob: blob}
}

// CleanupExpiredSessions 删除超过保留期的 AICC 会话及其图片对象。
func (s *AICCRetentionService) CleanupExpiredSessions(ctx context.Context, limit int32) (int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	sessions, err := s.store.ListExpiredAICCSessions(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("查询过期 AICC 会话失败: %w", err)
	}
	var deleted int64
	for _, session := range sessions {
		keys, err := s.store.ListAICCImageObjectKeysBySession(ctx, session.ID)
		if err != nil {
			return deleted, fmt.Errorf("查询 AICC 会话图片对象失败: %w", err)
		}
		for _, key := range keys {
			if s.blob == nil {
				continue
			}
			if err := s.blob.DeleteObject(ctx, key); err != nil {
				return deleted, fmt.Errorf("删除 AICC 图片对象失败: %w", err)
			}
		}
		if err := s.store.ClearAICCLeadLatestSession(ctx, null.StringFrom(session.ID)); err != nil {
			return deleted, fmt.Errorf("清空 AICC 线索最近会话失败: %w", err)
		}
		if err := s.store.DeleteAICCSession(ctx, session.ID); err != nil {
			return deleted, fmt.Errorf("删除 AICC 会话失败: %w", err)
		}
		deleted++
	}
	return deleted, nil
}
