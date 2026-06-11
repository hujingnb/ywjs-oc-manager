package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/store/sqlc"
)

// 工单状态常量(事件驱动状态机:pending→processing→delivered/rejected,可被重开回 pending)。
const (
	SkillTicketStatusPending    = "pending"    // 待处理:新提交 / 反馈重开,计入后台角标
	SkillTicketStatusProcessing = "processing" // 制作中:管理员接单后
	SkillTicketStatusDelivered  = "delivered"  // 已交付(关闭):Plan 2 交付置位
	SkillTicketStatusRejected   = "rejected"   // 已拒绝:带 reject_reason
)

// SkillTicketStore 是 SkillTicketService 所需的最小数据访问能力。
type SkillTicketStore interface {
	CreateSkillTicket(ctx context.Context, arg sqlc.CreateSkillTicketParams) error
	GetSkillTicket(ctx context.Context, id string) (sqlc.SkillTicket, error)
	ListSkillTicketsByRequester(ctx context.Context, requesterUserID string) ([]sqlc.SkillTicket, error)
	ListAllSkillTickets(ctx context.Context) ([]sqlc.SkillTicket, error)
	UpdateSkillTicketStatus(ctx context.Context, arg sqlc.UpdateSkillTicketStatusParams) error
	SetSkillTicketQuote(ctx context.Context, arg sqlc.SetSkillTicketQuoteParams) error
	RejectSkillTicket(ctx context.Context, arg sqlc.RejectSkillTicketParams) error
	TouchSkillTicket(ctx context.Context, id string) error
	CountPendingSkillTickets(ctx context.Context) (int64, error)
	ListSkillTicketMessages(ctx context.Context, ticketID string) ([]sqlc.SkillTicketMessage, error)
	ListCustomSkillTargetsByName(ctx context.Context, name string) ([]sqlc.CustomSkillTarget, error)
	GetUser(ctx context.Context, id string) (sqlc.User, error)
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
}

// SkillTicketService 管理定制技能需求工单的人工处理生命周期。
type SkillTicketService struct {
	store SkillTicketStore
}

// NewSkillTicketService 构造工单 service。
func NewSkillTicketService(store SkillTicketStore) *SkillTicketService {
	return &SkillTicketService{store: store}
}

// SubmitSkillTicketInput 是提交工单的入参。
type SubmitSkillTicketInput struct {
	Title       string
	Description string
}

// SkillTicketResult 是工单的对外视图。
type SkillTicketResult struct {
	ID               string    `json:"id"`
	OrgID            string    `json:"org_id"`
	RequesterUserID  string    `json:"requester_user_id"`
	RequesterRole    string    `json:"requester_role"`
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	Status           string    `json:"status"`
	QuoteAmountCents *int64    `json:"quote_amount_cents,omitempty"`
	CustomSkillName  string    `json:"custom_skill_name,omitempty"`
	RejectReason     string    `json:"reject_reason,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// SkillTicketDetailResult 是工单详情(含统一消息流)。
type SkillTicketDetailResult struct {
	SkillTicketResult
	RequesterName string                     `json:"requester_name,omitempty"`
	OrgName       string                     `json:"org_name,omitempty"`
	Messages      []SkillTicketMessageResult `json:"messages"`
	Targets       []CustomSkillTargetResult  `json:"targets,omitempty"`
}

// CustomSkillTargetResult 是已交付定制技能的可见范围视图,供详情页展示与编辑回填。
type CustomSkillTargetResult struct {
	OrgID    string `json:"org_id"`
	Audience string `json:"audience"`
}

func toSkillTicketResult(r sqlc.SkillTicket) SkillTicketResult {
	out := SkillTicketResult{
		ID: r.ID, OrgID: r.OrgID, RequesterUserID: r.RequesterUserID, RequesterRole: r.RequesterRole,
		Title: r.Title, Description: r.Description, Status: r.Status,
		CustomSkillName: r.CustomSkillName.String, RejectReason: r.RejectReason.String,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.QuoteAmountCents.Valid {
		v := r.QuoteAmountCents.Int64
		out.QuoteAmountCents = &v
	}
	return out
}

// Submit 提交一条定制需求工单(status=pending,快照提交者 org 与角色)。
func (s *SkillTicketService) Submit(ctx context.Context, p auth.Principal, in SubmitSkillTicketInput) (SkillTicketResult, error) {
	if !auth.CanSubmitSkillTicket(p) {
		return SkillTicketResult{}, ErrSkillTicketDenied
	}
	title := strings.TrimSpace(in.Title)
	desc := strings.TrimSpace(in.Description)
	if title == "" || desc == "" {
		return SkillTicketResult{}, fmt.Errorf("%w: 标题与描述不能为空", ErrSkillTicketInvalid)
	}
	id := newUUID()
	if err := s.store.CreateSkillTicket(ctx, sqlc.CreateSkillTicketParams{
		ID: id, OrgID: p.OrgID, RequesterUserID: p.UserID, RequesterRole: p.Role,
		Title: title, Description: desc, Status: SkillTicketStatusPending,
	}); err != nil {
		return SkillTicketResult{}, fmt.Errorf("创建工单失败: %w", err)
	}
	row, err := s.store.GetSkillTicket(ctx, id)
	if err != nil {
		return SkillTicketResult{}, fmt.Errorf("读回工单失败: %w", err)
	}
	return toSkillTicketResult(row), nil
}

// ListMine 返回当前用户提交的工单(企业用户自助查看)。
func (s *SkillTicketService) ListMine(ctx context.Context, p auth.Principal) ([]SkillTicketResult, error) {
	if !auth.CanSubmitSkillTicket(p) {
		return nil, ErrSkillTicketDenied
	}
	rows, err := s.store.ListSkillTicketsByRequester(ctx, p.UserID)
	if err != nil {
		return nil, fmt.Errorf("查询我的工单失败: %w", err)
	}
	return mapSkillTickets(rows), nil
}

// ListAll 返回全部工单(平台管理员队列,pending 优先)。
func (s *SkillTicketService) ListAll(ctx context.Context, p auth.Principal) ([]SkillTicketResult, error) {
	if !auth.CanManageSkillTicket(p) {
		return nil, ErrSkillTicketDenied
	}
	rows, err := s.store.ListAllSkillTickets(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询工单队列失败: %w", err)
	}
	return mapSkillTickets(rows), nil
}

func mapSkillTickets(rows []sqlc.SkillTicket) []SkillTicketResult {
	out := make([]SkillTicketResult, 0, len(rows))
	for _, r := range rows {
		out = append(out, toSkillTicketResult(r))
	}
	return out
}

// Get 返回工单详情(含消息流)。提交者本人或平台管理员可见。
func (s *SkillTicketService) Get(ctx context.Context, p auth.Principal, id string) (SkillTicketDetailResult, error) {
	row, err := s.loadTicket(ctx, id)
	if err != nil {
		return SkillTicketDetailResult{}, err
	}
	if !auth.CanViewSkillTicket(p, row.RequesterUserID) {
		return SkillTicketDetailResult{}, ErrSkillTicketDenied
	}
	messages, err := s.store.ListSkillTicketMessages(ctx, id)
	if err != nil {
		return SkillTicketDetailResult{}, fmt.Errorf("查询工单消息失败: %w", err)
	}
	ms := make([]SkillTicketMessageResult, 0, len(messages))
	for _, message := range messages {
		ms = append(ms, toMessageResult(message))
	}
	targets := make([]CustomSkillTargetResult, 0)
	if row.CustomSkillName.Valid {
		rows, err := s.store.ListCustomSkillTargetsByName(ctx, row.CustomSkillName.String)
		if err != nil {
			return SkillTicketDetailResult{}, fmt.Errorf("查询可见范围失败: %w", err)
		}
		for _, target := range rows {
			targets = append(targets, CustomSkillTargetResult{OrgID: target.OrgID, Audience: target.Audience})
		}
	}
	return SkillTicketDetailResult{
		SkillTicketResult: toSkillTicketResult(row),
		RequesterName:     s.requesterName(ctx, row.RequesterUserID),
		OrgName:           s.organizationName(ctx, row.OrgID),
		Messages:          ms,
		Targets:           targets,
	}, nil
}

// requesterName 返回平台管理员详情页展示用的提交者名称；查询失败时降级为 user id，避免辅助信息阻断详情读取。
func (s *SkillTicketService) requesterName(ctx context.Context, userID string) string {
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return userID
	}
	if name := strings.TrimSpace(user.DisplayName); name != "" {
		return name
	}
	if username := strings.TrimSpace(user.Username); username != "" {
		return username
	}
	return userID
}

// organizationName 返回平台管理员详情页展示用的企业名称；查询失败时降级为 org id。
func (s *SkillTicketService) organizationName(ctx context.Context, orgID string) string {
	org, err := s.store.GetOrganization(ctx, orgID)
	if err != nil {
		return orgID
	}
	if name := strings.TrimSpace(org.Name); name != "" {
		return name
	}
	if code := strings.TrimSpace(org.Code); code != "" {
		return code
	}
	return orgID
}

// loadTicket 按 id 取工单,未找到映射成 ErrSkillTicketNotFound。
func (s *SkillTicketService) loadTicket(ctx context.Context, id string) (sqlc.SkillTicket, error) {
	row, err := s.store.GetSkillTicket(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sqlc.SkillTicket{}, ErrSkillTicketNotFound
		}
		return sqlc.SkillTicket{}, fmt.Errorf("查询工单失败: %w", err)
	}
	return row, nil
}

// transition 执行显式状态动作,先校验当前状态在 allowed 集合内,再落目标状态。
func (s *SkillTicketService) transition(ctx context.Context, p auth.Principal, id, next string, allowed ...string) error {
	if !auth.CanManageSkillTicket(p) {
		return ErrSkillTicketDenied
	}
	row, err := s.loadTicket(ctx, id)
	if err != nil {
		return err
	}
	ok := false
	for _, candidate := range allowed {
		if row.Status == candidate {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("%w: 当前状态 %q 不允许此操作", ErrSkillTicketInvalid, row.Status)
	}
	if err := s.store.UpdateSkillTicketStatus(ctx, sqlc.UpdateSkillTicketStatusParams{Status: next, ID: id}); err != nil {
		return fmt.Errorf("更新工单状态失败: %w", err)
	}
	return nil
}

// StartProcessing 开始制作:pending → processing,取代旧的手选状态。
func (s *SkillTicketService) StartProcessing(ctx context.Context, p auth.Principal, id string) error {
	return s.transition(ctx, p, id, SkillTicketStatusProcessing, SkillTicketStatusPending)
}

// ReopenRejected 重新受理已拒绝工单:rejected → processing,用于管理员翻案。
func (s *SkillTicketService) ReopenRejected(ctx context.Context, p auth.Principal, id string) error {
	return s.transition(ctx, p, id, SkillTicketStatusProcessing, SkillTicketStatusRejected)
}

// SetQuote 管理员设置报价(单位:分);仅未交付阶段允许调整。
func (s *SkillTicketService) SetQuote(ctx context.Context, p auth.Principal, id string, cents int64) error {
	if !auth.CanManageSkillTicket(p) {
		return ErrSkillTicketDenied
	}
	if cents < 0 {
		return fmt.Errorf("%w: 报价不能为负", ErrSkillTicketInvalid)
	}
	row, err := s.loadTicket(ctx, id)
	if err != nil {
		return err
	}
	if row.Status != SkillTicketStatusPending && row.Status != SkillTicketStatusProcessing {
		return fmt.Errorf("%w: 当前状态 %q 不允许设置报价", ErrSkillTicketInvalid, row.Status)
	}
	if err := s.store.SetSkillTicketQuote(ctx, sqlc.SetSkillTicketQuoteParams{QuoteAmountCents: null.IntFrom(cents), ID: id}); err != nil {
		return fmt.Errorf("设置报价失败: %w", err)
	}
	return nil
}

// Reject 管理员拒绝未交付工单(status=rejected + 原因);关闭后由需求方消息触发重开。
func (s *SkillTicketService) Reject(ctx context.Context, p auth.Principal, id, reason string) error {
	if !auth.CanManageSkillTicket(p) {
		return ErrSkillTicketDenied
	}
	row, err := s.loadTicket(ctx, id)
	if err != nil {
		return err
	}
	if row.Status != SkillTicketStatusPending && row.Status != SkillTicketStatusProcessing {
		return fmt.Errorf("%w: 当前状态 %q 不允许拒绝", ErrSkillTicketInvalid, row.Status)
	}
	if err := s.store.RejectSkillTicket(ctx, sqlc.RejectSkillTicketParams{RejectReason: null.StringFrom(strings.TrimSpace(reason)), ID: id}); err != nil {
		return fmt.Errorf("拒绝工单失败: %w", err)
	}
	return nil
}

// PendingBadgeCount 返回待处理(pending)工单数,供后台菜单角标。
func (s *SkillTicketService) PendingBadgeCount(ctx context.Context, p auth.Principal) (int64, error) {
	if !auth.CanManageSkillTicket(p) {
		return 0, ErrSkillTicketDenied
	}
	n, err := s.store.CountPendingSkillTickets(ctx)
	if err != nil {
		return 0, fmt.Errorf("查询待处理工单数失败: %w", err)
	}
	return n, nil
}
