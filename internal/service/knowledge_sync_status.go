package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/store/sqlc"
)

// KnowledgeSyncStatusStore 是 KnowledgeSyncStatusService 需要的 sqlc 子集。
// 独立小接口便于 fake 测试，不直接依赖整个 *sqlc.Queries。
type KnowledgeSyncStatusStore interface {
	UpsertKnowledgeSyncStatus(ctx context.Context, arg sqlc.UpsertKnowledgeSyncStatusParams) (sqlc.KnowledgeSyncStatus, error)
	ListKnowledgeSyncStatusByOrg(ctx context.Context, orgID pgtype.UUID) ([]sqlc.KnowledgeSyncStatus, error)
}

// KnowledgeSyncStatusService 维护组织级知识库每节点的最近同步状态。
//
// 提供两类操作：
//   - 状态写入：dispatcher 入队时写 pending、worker handler 完成时写 success/failed；
//     所有 (org, node) 状态翻转都走 UpsertKnowledgeSyncStatus 单条 SQL。
//   - 状态读取：API 层调 ListByOrg 拉某组织所有节点状态；前端展示徽章 + 失败原因 +
//     最近成功时间 + 触发"重试同步"。
type KnowledgeSyncStatusService struct {
	store KnowledgeSyncStatusStore
}

// NewKnowledgeSyncStatusService 创建服务。
func NewKnowledgeSyncStatusService(store KnowledgeSyncStatusStore) *KnowledgeSyncStatusService {
	return &KnowledgeSyncStatusService{store: store}
}

// MarkOrgNodePending 在 dispatcher 入队时写 (org, node) = pending；
// 不覆盖已有 last_success_at（COALESCE 由 SQL 处理）。
func (s *KnowledgeSyncStatusService) MarkOrgNodePending(ctx context.Context, orgID, nodeID string) error {
	return s.upsert(ctx, orgID, nodeID, "pending", time.Time{}, "")
}

// MarkOrgNodeSynced 在 worker handler 成功完成时写 (org, node) = synced；
// 同时把 last_success_at 推到 now()。
func (s *KnowledgeSyncStatusService) MarkOrgNodeSynced(ctx context.Context, orgID, nodeID string) error {
	return s.upsert(ctx, orgID, nodeID, "synced", time.Now(), "")
}

// MarkOrgNodeFailed 在 worker handler 失败时写 (org, node) = failed；
// 不更新 last_success_at（保留之前的成功时间用于排查）。
func (s *KnowledgeSyncStatusService) MarkOrgNodeFailed(ctx context.Context, orgID, nodeID, errMsg string) error {
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	return s.upsert(ctx, orgID, nodeID, "failed", time.Time{}, errMsg)
}

// SyncStatusResult 是对外的状态视图。
type SyncStatusResult struct {
	OrgID         string     `json:"org_id"`
	NodeID        string     `json:"node_id"`
	Status        string     `json:"status"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// ListByOrg 列出指定组织在所有节点上的最近同步状态。
// 调用方负责权限校验（仅组织管理员 / 平台管理员）。
func (s *KnowledgeSyncStatusService) ListByOrg(ctx context.Context, orgID string) ([]SyncStatusResult, error) {
	id, err := parseUUID(orgID)
	if err != nil {
		return nil, fmt.Errorf("非法 org_id: %w", err)
	}
	rows, err := s.store.ListKnowledgeSyncStatusByOrg(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("查询组织同步状态失败: %w", err)
	}
	out := make([]SyncStatusResult, 0, len(rows))
	for _, r := range rows {
		out = append(out, toSyncStatusResult(r))
	}
	return out, nil
}

func (s *KnowledgeSyncStatusService) upsert(ctx context.Context, orgID, nodeID, status string, successAt time.Time, errMsg string) error {
	if s == nil || s.store == nil {
		return nil
	}
	orgUUID, err := parseUUID(orgID)
	if err != nil {
		return fmt.Errorf("非法 org_id: %w", err)
	}
	nodeUUID, err := parseUUID(nodeID)
	if err != nil {
		return fmt.Errorf("非法 node_id: %w", err)
	}
	var lastSuccess pgtype.Timestamptz
	if !successAt.IsZero() {
		lastSuccess = pgtype.Timestamptz{Time: successAt, Valid: true}
	}
	var lastError pgtype.Text
	if errMsg != "" {
		lastError = pgtype.Text{String: errMsg, Valid: true}
	}
	_, err = s.store.UpsertKnowledgeSyncStatus(ctx, sqlc.UpsertKnowledgeSyncStatusParams{
		OrgID:         orgUUID,
		NodeID:        nodeUUID,
		Status:        status,
		LastSuccessAt: lastSuccess,
		LastError:     lastError,
	})
	return err
}

func toSyncStatusResult(r sqlc.KnowledgeSyncStatus) SyncStatusResult {
	out := SyncStatusResult{
		OrgID:     uuidToString(r.OrgID),
		NodeID:    uuidToString(r.NodeID),
		Status:    r.Status,
		UpdatedAt: r.UpdatedAt.Time,
	}
	if r.LastSuccessAt.Valid {
		t := r.LastSuccessAt.Time
		out.LastSuccessAt = &t
	}
	if r.LastError.Valid {
		out.LastError = r.LastError.String
	}
	return out
}
