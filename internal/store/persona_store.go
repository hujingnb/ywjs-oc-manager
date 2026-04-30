package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// personaStore 提供 organization_personas 表的查询能力。
// 因 sqlc 生成目录不可写，这里手写直连 pgx 的 query；保持与 sqlc 风格一致便于后续迁移。
type personaStore struct {
	pool *pgxpool.Pool
}

// NewPersonaStore 创建 persona 存储。
func NewPersonaStore(s *Store) *personaStore {
	return &personaStore{pool: s.pool}
}

const getCurrentPersona = `
SELECT id, org_id, system_prompt, conversation_rules, forbidden_rules, reply_style,
       allow_member_override, version, created_by, created_at
FROM organization_personas
WHERE org_id = $1
ORDER BY version DESC
LIMIT 1
`

// GetCurrentOrganizationPersona 取组织当前生效的 persona（版本号最大那条）。
func (s *personaStore) GetCurrentOrganizationPersona(ctx context.Context, orgID pgtype.UUID) (sqlc.OrganizationPersona, error) {
	var persona sqlc.OrganizationPersona
	row := s.pool.QueryRow(ctx, getCurrentPersona, orgID)
	if err := row.Scan(
		&persona.ID,
		&persona.OrgID,
		&persona.SystemPrompt,
		&persona.ConversationRules,
		&persona.ForbiddenRules,
		&persona.ReplyStyle,
		&persona.AllowMemberOverride,
		&persona.Version,
		&persona.CreatedBy,
		&persona.CreatedAt,
	); err != nil {
		return sqlc.OrganizationPersona{}, err
	}
	return persona, nil
}

const createPersonaSQL = `
INSERT INTO organization_personas (
    org_id,
    system_prompt,
    conversation_rules,
    forbidden_rules,
    reply_style,
    allow_member_override,
    version,
    created_by
) VALUES (
    $1,
    $2,
    $3,
    $4,
    $5,
    $6,
    COALESCE((SELECT MAX(version) + 1 FROM organization_personas WHERE org_id = $1), 1),
    $7
)
RETURNING id, org_id, system_prompt, conversation_rules, forbidden_rules, reply_style,
          allow_member_override, version, created_by, created_at
`

// CreateOrganizationPersona 写入新版本 persona，返回完整记录。
// version 由 SQL 自动递增；并发写入时由数据库唯一约束 (org_id, version) 拦截。
func (s *personaStore) CreateOrganizationPersona(ctx context.Context, arg service.PersonaCreateInput) (sqlc.OrganizationPersona, error) {
	var persona sqlc.OrganizationPersona
	row := s.pool.QueryRow(ctx, createPersonaSQL,
		arg.OrgID,
		arg.SystemPrompt,
		arg.ConversationRules,
		arg.ForbiddenRules,
		arg.ReplyStyle,
		arg.AllowMemberOverride,
		arg.CreatedBy,
	)
	if err := row.Scan(
		&persona.ID,
		&persona.OrgID,
		&persona.SystemPrompt,
		&persona.ConversationRules,
		&persona.ForbiddenRules,
		&persona.ReplyStyle,
		&persona.AllowMemberOverride,
		&persona.Version,
		&persona.CreatedBy,
		&persona.CreatedAt,
	); err != nil {
		return sqlc.OrganizationPersona{}, err
	}
	return persona, nil
}
