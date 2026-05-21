package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// AssistantVersionStore 提供 assistant_versions 表的查询能力，代理 sqlc 生成的 Queries 方法。
type AssistantVersionStore struct {
	// q 是 sqlc 生成的类型安全查询入口，由 Store 统一管理生命周期。
	q *sqlc.Queries
}

// 编译期断言：AssistantVersionStore 必须实现 service.AssistantVersionStore。
var _ service.AssistantVersionStore = (*AssistantVersionStore)(nil)

// NewAssistantVersionStore 创建 AssistantVersionStore，从 Store 获取 sqlc Queries。
func NewAssistantVersionStore(s *Store) *AssistantVersionStore {
	return &AssistantVersionStore{q: s.Queries}
}

// GetAssistantVersion 按主键 id 查询单个版本；未找到时原样返回 pgx.ErrNoRows。
func (s *AssistantVersionStore) GetAssistantVersion(ctx context.Context, id pgtype.UUID) (sqlc.AssistantVersion, error) {
	return s.q.GetAssistantVersion(ctx, id)
}

// GetAssistantVersionByName 按名称查询单个版本；未找到时原样返回 pgx.ErrNoRows。
func (s *AssistantVersionStore) GetAssistantVersionByName(ctx context.Context, name string) (sqlc.AssistantVersion, error) {
	return s.q.GetAssistantVersionByName(ctx, name)
}

// ListAssistantVersions 返回所有未软删除版本，按创建时间倒序。
func (s *AssistantVersionStore) ListAssistantVersions(ctx context.Context) ([]sqlc.AssistantVersion, error) {
	return s.q.ListAssistantVersions(ctx)
}

// CreateAssistantVersion 写入新版本并返回完整记录。
func (s *AssistantVersionStore) CreateAssistantVersion(ctx context.Context, arg sqlc.CreateAssistantVersionParams) (sqlc.AssistantVersion, error) {
	return s.q.CreateAssistantVersion(ctx, arg)
}

// UpdateAssistantVersion 更新版本字段并返回最新记录。
func (s *AssistantVersionStore) UpdateAssistantVersion(ctx context.Context, arg sqlc.UpdateAssistantVersionParams) (sqlc.AssistantVersion, error) {
	return s.q.UpdateAssistantVersion(ctx, arg)
}

// UpdateAssistantVersionSkills 更新版本的 skills_json 与 revision 并返回最新记录。
func (s *AssistantVersionStore) UpdateAssistantVersionSkills(ctx context.Context, arg sqlc.UpdateAssistantVersionSkillsParams) (sqlc.AssistantVersion, error) {
	return s.q.UpdateAssistantVersionSkills(ctx, arg)
}

// SoftDeleteAssistantVersion 软删除版本并返回已删除记录。
func (s *AssistantVersionStore) SoftDeleteAssistantVersion(ctx context.Context, id pgtype.UUID) (sqlc.AssistantVersion, error) {
	return s.q.SoftDeleteAssistantVersion(ctx, id)
}

// CountAppsUsingVersion 统计引用该版本的未删除实例数量，供删除前保护性检查使用。
func (s *AssistantVersionStore) CountAppsUsingVersion(ctx context.Context, id pgtype.UUID) (int64, error) {
	return s.q.CountAppsUsingVersion(ctx, id)
}

// CountOrgsUsingVersion 统计 assistant_version_ids jsonb 中包含该版本 id 的组织数量。
// 参数 id 是版本 UUID 的字符串形式，与 jsonb_exists 的参数语义一致。
func (s *AssistantVersionStore) CountOrgsUsingVersion(ctx context.Context, id string) (int64, error) {
	return s.q.CountOrgsUsingVersion(ctx, id)
}
