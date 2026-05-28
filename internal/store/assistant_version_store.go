package store

import (
	"context"

	"github.com/google/uuid"
	null "github.com/guregu/null/v5"

	"oc-manager/internal/store/sqlc"
)

// AssistantVersionStore 提供 assistant_versions 表的查询能力，代理 sqlc 生成的 Queries 方法。
type AssistantVersionStore struct {
	// q 是 sqlc 生成的类型安全查询入口，由 Store 统一管理生命周期。
	q *sqlc.Queries
}

// NewAssistantVersionStore 创建 AssistantVersionStore，从 Store 获取 sqlc Queries。
func NewAssistantVersionStore(s *Store) *AssistantVersionStore {
	return &AssistantVersionStore{q: s.Queries}
}

// GetAssistantVersion 按主键 id 查询单个版本；未找到时原样返回 sql.ErrNoRows。
func (s *AssistantVersionStore) GetAssistantVersion(ctx context.Context, id string) (sqlc.AssistantVersion, error) {
	return s.q.GetAssistantVersion(ctx, id)
}

// GetAssistantVersionByName 按名称查询单个版本；未找到时原样返回 sql.ErrNoRows。
func (s *AssistantVersionStore) GetAssistantVersionByName(ctx context.Context, name string) (sqlc.AssistantVersion, error) {
	return s.q.GetAssistantVersionByName(ctx, name)
}

// ListAssistantVersions 返回所有未软删除版本，按创建时间倒序。
func (s *AssistantVersionStore) ListAssistantVersions(ctx context.Context) ([]sqlc.AssistantVersion, error) {
	return s.q.ListAssistantVersions(ctx)
}

// CreateAssistantVersion 写入新版本并返回完整记录。
// 迁移至 MySQL 后 sqlc 生成的 CreateAssistantVersion 为 :exec 模式，不再返回行；
// 此处先生成 UUID 并写入 ID 字段，执行 INSERT 后通过 GetAssistantVersion 读回完整记录。
func (s *AssistantVersionStore) CreateAssistantVersion(ctx context.Context, arg sqlc.CreateAssistantVersionParams) (sqlc.AssistantVersion, error) {
	// 若调用方已设置 ID 则沿用；否则生成新 UUID 保证幂等。
	if arg.ID == "" {
		arg.ID = uuid.NewString()
	}
	if err := s.q.CreateAssistantVersion(ctx, arg); err != nil {
		return sqlc.AssistantVersion{}, err
	}
	return s.q.GetAssistantVersion(ctx, arg.ID)
}

// UpdateAssistantVersion 更新版本字段并返回最新记录。
// 迁移至 MySQL 后 sqlc 生成的 UpdateAssistantVersion 为 :exec 模式；执行后读回最新行。
func (s *AssistantVersionStore) UpdateAssistantVersion(ctx context.Context, arg sqlc.UpdateAssistantVersionParams) (sqlc.AssistantVersion, error) {
	if err := s.q.UpdateAssistantVersion(ctx, arg); err != nil {
		return sqlc.AssistantVersion{}, err
	}
	return s.q.GetAssistantVersion(ctx, arg.ID)
}

// UpdateAssistantVersionSkills 更新版本的 skills_json 与 revision 并返回最新记录。
// 迁移至 MySQL 后 sqlc 生成的 UpdateAssistantVersionSkills 为 :exec 模式；执行后读回最新行。
func (s *AssistantVersionStore) UpdateAssistantVersionSkills(ctx context.Context, arg sqlc.UpdateAssistantVersionSkillsParams) (sqlc.AssistantVersion, error) {
	if err := s.q.UpdateAssistantVersionSkills(ctx, arg); err != nil {
		return sqlc.AssistantVersion{}, err
	}
	return s.q.GetAssistantVersion(ctx, arg.ID)
}

// SoftDeleteAssistantVersion 软删除版本并返回已删除记录。
// 迁移至 MySQL 后 sqlc 生成的 SoftDeleteAssistantVersion 为 :exec 模式；
// 软删除后 deleted_at 非空，GetAssistantVersion 过滤 deleted_at IS NULL 将找不到行；
// 因此改为先读后删：先取快照，再执行软删除，返回快照。
func (s *AssistantVersionStore) SoftDeleteAssistantVersion(ctx context.Context, id string) (sqlc.AssistantVersion, error) {
	// 先读取当前记录，以便软删除后仍能返回已删除的数据快照。
	row, err := s.q.GetAssistantVersion(ctx, id)
	if err != nil {
		return sqlc.AssistantVersion{}, err
	}
	if err := s.q.SoftDeleteAssistantVersion(ctx, id); err != nil {
		return sqlc.AssistantVersion{}, err
	}
	return row, nil
}

// CountAppsUsingVersion 统计引用该版本的未删除实例数量，供删除前保护性检查使用。
// sqlc 生成的参数类型为 null.String；版本 ID 必然有值，使用 null.StringFrom 包装。
func (s *AssistantVersionStore) CountAppsUsingVersion(ctx context.Context, id string) (int64, error) {
	return s.q.CountAppsUsingVersion(ctx, null.StringFrom(id))
}

// CountOrgsUsingVersion 统计 assistant_version_ids jsonb 中包含该版本 id 的组织数量。
// 参数 id 是版本 UUID 的字符串形式，与 JSON_CONTAINS 的参数语义一致。
func (s *AssistantVersionStore) CountOrgsUsingVersion(ctx context.Context, id string) (int64, error) {
	return s.q.CountOrgsUsingVersion(ctx, id)
}
