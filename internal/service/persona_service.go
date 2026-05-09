package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/store/sqlc"
)

// 与人设服务相关的错误。
var (
	ErrPersonaNotFound = errors.New("组织尚未配置人设")
	ErrPersonaDenied   = errors.New("无权访问该组织人设")
)

// PersonaStore 抽象 service 需要的存储能力。
type PersonaStore interface {
	GetCurrentOrganizationPersona(ctx context.Context, orgID pgtype.UUID) (sqlc.OrganizationPersona, error)
	CreateOrganizationPersona(ctx context.Context, arg PersonaCreateInput) (sqlc.OrganizationPersona, error)
}

// PersonaCreateInput 是 service 层向 store 传递的写入参数，与 store 包内部结构解耦。
type PersonaCreateInput struct {
	OrgID               pgtype.UUID
	SystemPrompt        string
	ConversationRules   pgtype.Text
	ForbiddenRules      pgtype.Text
	ReplyStyle          pgtype.Text
	AllowMemberOverride bool
	CreatedBy           pgtype.UUID
}

// PersonaService 维护组织 AI 人设的读写。
//
// 设计：
//   - 写入仅创建新版本，不更新旧版本，便于审计与回放；
//   - 读取始终取最大 version；
//   - 平台管理员读 + 写所有组织；组织管理员只能读写本组织；普通成员只读。
type PersonaService struct {
	store PersonaStore
}

// NewPersonaService 创建人设服务。
func NewPersonaService(store PersonaStore) *PersonaService {
	return &PersonaService{store: store}
}

// PersonaResult 是面向 handler/前端的人设视图。
type PersonaResult struct {
	OrgID               string `json:"org_id"`
	SystemPrompt        string `json:"system_prompt"`
	ConversationRules   string `json:"conversation_rules,omitempty"`
	ForbiddenRules      string `json:"forbidden_rules,omitempty"`
	ReplyStyle          string `json:"reply_style,omitempty"`
	AllowMemberOverride bool   `json:"allow_member_override"`
	Version             int32  `json:"version"`
}

// PersonaInput 是 PUT /persona 接口的入参。
type PersonaInput struct {
	SystemPrompt        string
	ConversationRules   string
	ForbiddenRules      string
	ReplyStyle          string
	AllowMemberOverride bool
}

// GetCurrent 返回组织当前生效的人设。
func (s *PersonaService) GetCurrent(ctx context.Context, principal auth.Principal, orgID string) (PersonaResult, error) {
	if !auth.CanViewOrgPersona(principal, orgID) {
		return PersonaResult{}, ErrPersonaDenied
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return PersonaResult{}, ErrNotFound
	}
	persona, err := s.store.GetCurrentOrganizationPersona(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonaResult{}, ErrPersonaNotFound
	}
	if err != nil {
		return PersonaResult{}, fmt.Errorf("查询人设失败: %w", err)
	}
	return toPersonaResult(persona), nil
}

// Replace 写入一条新版本的 persona（旧版本保留）。
func (s *PersonaService) Replace(ctx context.Context, principal auth.Principal, orgID string, input PersonaInput) (PersonaResult, error) {
	if !auth.CanManageOrgPersona(principal, orgID) {
		return PersonaResult{}, ErrPersonaDenied
	}
	if input.SystemPrompt == "" {
		return PersonaResult{}, fmt.Errorf("system_prompt 不能为空")
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return PersonaResult{}, ErrNotFound
	}
	creator, _ := optionalUUID(principal.UserID)
	persona, err := s.store.CreateOrganizationPersona(ctx, PersonaCreateInput{
		OrgID:               id,
		SystemPrompt:        input.SystemPrompt,
		ConversationRules:   pgtype.Text{String: input.ConversationRules, Valid: input.ConversationRules != ""},
		ForbiddenRules:      pgtype.Text{String: input.ForbiddenRules, Valid: input.ForbiddenRules != ""},
		ReplyStyle:          pgtype.Text{String: input.ReplyStyle, Valid: input.ReplyStyle != ""},
		AllowMemberOverride: input.AllowMemberOverride,
		CreatedBy:           creator,
	})
	if err != nil {
		return PersonaResult{}, fmt.Errorf("写入人设失败: %w", err)
	}
	return toPersonaResult(persona), nil
}

func toPersonaResult(p sqlc.OrganizationPersona) PersonaResult {
	r := PersonaResult{
		OrgID:               uuidToString(p.OrgID),
		SystemPrompt:        p.SystemPrompt,
		AllowMemberOverride: p.AllowMemberOverride,
		Version:             p.Version,
	}
	if p.ConversationRules.Valid {
		r.ConversationRules = p.ConversationRules.String
	}
	if p.ForbiddenRules.Valid {
		r.ForbiddenRules = p.ForbiddenRules.String
	}
	if p.ReplyStyle.Valid {
		r.ReplyStyle = p.ReplyStyle.String
	}
	return r
}
