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

// GetIndustryKnowledgeBase 按 ID 查询未删除行业知识库，供助手版本关联校验使用。
func (s *AssistantVersionStore) GetIndustryKnowledgeBase(ctx context.Context, id string) (sqlc.IndustryKnowledgeBasis, error) {
	return s.q.GetIndustryKnowledgeBase(ctx, id)
}

// ReplaceAssistantVersionIndustryKnowledgeBases 清空版本旧行业库关联。
func (s *AssistantVersionStore) ReplaceAssistantVersionIndustryKnowledgeBases(ctx context.Context, versionID string) error {
	return s.q.ReplaceAssistantVersionIndustryKnowledgeBases(ctx, versionID)
}

// AddAssistantVersionIndustryKnowledgeBase 为版本追加单个行业知识库关联。
func (s *AssistantVersionStore) AddAssistantVersionIndustryKnowledgeBase(ctx context.Context, arg sqlc.AddAssistantVersionIndustryKnowledgeBaseParams) (int64, error) {
	return s.q.AddAssistantVersionIndustryKnowledgeBase(ctx, arg)
}

// ListIndustryKnowledgeBasesByAssistantVersion 返回版本关联的未删除行业知识库。
func (s *AssistantVersionStore) ListIndustryKnowledgeBasesByAssistantVersion(ctx context.Context, versionID string) ([]sqlc.IndustryKnowledgeBasis, error) {
	return s.q.ListIndustryKnowledgeBasesByAssistantVersion(ctx, versionID)
}

// CreateAssistantVersion 写入新版本（:exec 模式），若调用方未设置 ID 则生成新 UUID。
// 符合 service.AssistantVersionStore 接口：返回 error。
// service 层调用后自行通过 GetAssistantVersion 读回完整记录。
func (s *AssistantVersionStore) CreateAssistantVersion(ctx context.Context, arg sqlc.CreateAssistantVersionParams) error {
	// 若调用方已设置 ID 则沿用；否则生成新 UUID 保证幂等。
	if arg.ID == "" {
		arg.ID = uuid.NewString()
	}
	return s.q.CreateAssistantVersion(ctx, arg)
}

// UpdateAssistantVersion 更新版本字段（:exec 模式）。
// 符合 service.AssistantVersionStore 接口：返回 error。
func (s *AssistantVersionStore) UpdateAssistantVersion(ctx context.Context, arg sqlc.UpdateAssistantVersionParams) error {
	return s.q.UpdateAssistantVersion(ctx, arg)
}

// UpdateAssistantVersionSkills 更新版本的 skills_json 与 revision（:exec 模式）。
// 符合 service.AssistantVersionStore 接口：返回 error。
func (s *AssistantVersionStore) UpdateAssistantVersionSkills(ctx context.Context, arg sqlc.UpdateAssistantVersionSkillsParams) error {
	return s.q.UpdateAssistantVersionSkills(ctx, arg)
}

// SoftDeleteAssistantVersion 软删除版本（:exec 模式）。
// 符合 service.AssistantVersionStore 接口：返回 error。
func (s *AssistantVersionStore) SoftDeleteAssistantVersion(ctx context.Context, id string) error {
	return s.q.SoftDeleteAssistantVersion(ctx, id)
}

// CountAppsUsingVersion 统计引用该版本的未删除实例数量，供删除前保护性检查使用。
// 参数类型为 null.String（与 service.AssistantVersionStore 接口对齐）。
func (s *AssistantVersionStore) CountAppsUsingVersion(ctx context.Context, versionID null.String) (int64, error) {
	return s.q.CountAppsUsingVersion(ctx, versionID)
}

// CountOrgsUsingVersion 统计 assistant_version_ids jsonb 中包含该版本 id 的组织数量。
// 参数 id 是版本 UUID 的字符串形式，与 JSON_CONTAINS 的参数语义一致。
func (s *AssistantVersionStore) CountOrgsUsingVersion(ctx context.Context, id string) (int64, error) {
	return s.q.CountOrgsUsingVersion(ctx, id)
}
