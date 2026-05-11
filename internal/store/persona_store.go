package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// personaStore 提供 organization_personas 表的查询能力。
// 这里手写 create query，是为了在 SQL 内计算下一个 version；sqlc 生成的创建方法需要调用方传入 version。
type personaStore struct {
	// pool 承载 persona 专用手写查询，避免把版本递增规则散落到 service 层。
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
// 未配置 persona 时保持 pgx.ErrNoRows 原样返回，由 service 层映射为业务错误。
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
// arg 中的 prompt 与规则字段已在 service 层完成业务校验，这里只负责持久化。
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
