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
	CreateSkillTicketComment(ctx context.Context, arg sqlc.CreateSkillTicketCommentParams) error
	ListSkillTicketComments(ctx context.Context, ticketID string) ([]sqlc.SkillTicketComment, error)
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

// SkillTicketCommentResult 是工单评论的对外视图。
type SkillTicketCommentResult struct {
	ID           string    `json:"id"`
	AuthorUserID string    `json:"author_user_id"`
	Body         string    `json:"body"`
	CreatedAt    time.Time `json:"created_at"`
}

// SkillTicketDetailResult 是工单详情(含评论流)。
type SkillTicketDetailResult struct {
	SkillTicketResult
	Comments []SkillTicketCommentResult `json:"comments"`
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

// Get 返回工单详情(含评论流)。提交者本人或平台管理员可见。
func (s *SkillTicketService) Get(ctx context.Context, p auth.Principal, id string) (SkillTicketDetailResult, error) {
	row, err := s.loadTicket(ctx, id)
	if err != nil {
		return SkillTicketDetailResult{}, err
	}
	if !auth.CanViewSkillTicket(p, row.RequesterUserID) {
		return SkillTicketDetailResult{}, ErrSkillTicketDenied
	}
	comments, err := s.store.ListSkillTicketComments(ctx, id)
	if err != nil {
		return SkillTicketDetailResult{}, fmt.Errorf("查询工单评论失败: %w", err)
	}
	cs := make([]SkillTicketCommentResult, 0, len(comments))
	for _, c := range comments {
		cs = append(cs, SkillTicketCommentResult{ID: c.ID, AuthorUserID: c.AuthorUserID, Body: c.Body, CreatedAt: c.CreatedAt})
	}
	return SkillTicketDetailResult{SkillTicketResult: toSkillTicketResult(row), Comments: cs}, nil
}

// AddComment 追加一条评论。提交者或平台管理员可发;当提交者在 delivered/rejected 工单上发评论时,
// 自动重开工单回 pending(重新进入处理队列)。
func (s *SkillTicketService) AddComment(ctx context.Context, p auth.Principal, id, body string) (SkillTicketCommentResult, error) {
	row, err := s.loadTicket(ctx, id)
	if err != nil {
		return SkillTicketCommentResult{}, err
	}
	if !auth.CanViewSkillTicket(p, row.RequesterUserID) {
		return SkillTicketCommentResult{}, ErrSkillTicketDenied
	}
	text := strings.TrimSpace(body)
	if text == "" {
		return SkillTicketCommentResult{}, fmt.Errorf("%w: 评论不能为空", ErrSkillTicketInvalid)
	}
	cid := newUUID()
	if err := s.store.CreateSkillTicketComment(ctx, sqlc.CreateSkillTicketCommentParams{
		ID: cid, TicketID: id, AuthorUserID: p.UserID, Body: text,
	}); err != nil {
		return SkillTicketCommentResult{}, fmt.Errorf("创建评论失败: %w", err)
	}
	// 重开判定:提交者本人 + 工单处于关闭态(delivered/rejected)→ 回 pending;否则仅刷新 updated_at。
	reopened := p.UserID == row.RequesterUserID &&
		(row.Status == SkillTicketStatusDelivered || row.Status == SkillTicketStatusRejected)
	if reopened {
		if err := s.store.UpdateSkillTicketStatus(ctx, sqlc.UpdateSkillTicketStatusParams{Status: SkillTicketStatusPending, ID: id}); err != nil {
			return SkillTicketCommentResult{}, fmt.Errorf("重开工单失败: %w", err)
		}
	} else {
		_ = s.store.TouchSkillTicket(ctx, id)
	}
	return SkillTicketCommentResult{ID: cid, AuthorUserID: p.UserID, Body: text}, nil
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

// UpdateStatus 管理员调整工单状态。仅允许 pending/processing 两态互转;
// delivered 由交付链路(Plan 2)负责,rejected 走 Reject,传入其它值为 Invalid。
func (s *SkillTicketService) UpdateStatus(ctx context.Context, p auth.Principal, id, status string) error {
	if !auth.CanManageSkillTicket(p) {
		return ErrSkillTicketDenied
	}
	if status != SkillTicketStatusPending && status != SkillTicketStatusProcessing {
		return fmt.Errorf("%w: 不支持的状态 %q", ErrSkillTicketInvalid, status)
	}
	if _, err := s.loadTicket(ctx, id); err != nil {
		return err
	}
	if err := s.store.UpdateSkillTicketStatus(ctx, sqlc.UpdateSkillTicketStatusParams{Status: status, ID: id}); err != nil {
		return fmt.Errorf("更新工单状态失败: %w", err)
	}
	return nil
}

// SetQuote 管理员设置报价(单位:分)。
func (s *SkillTicketService) SetQuote(ctx context.Context, p auth.Principal, id string, cents int64) error {
	if !auth.CanManageSkillTicket(p) {
		return ErrSkillTicketDenied
	}
	if cents < 0 {
		return fmt.Errorf("%w: 报价不能为负", ErrSkillTicketInvalid)
	}
	if _, err := s.loadTicket(ctx, id); err != nil {
		return err
	}
	if err := s.store.SetSkillTicketQuote(ctx, sqlc.SetSkillTicketQuoteParams{QuoteAmountCents: null.IntFrom(cents), ID: id}); err != nil {
		return fmt.Errorf("设置报价失败: %w", err)
	}
	return nil
}

// Reject 管理员拒绝工单(status=rejected + 原因)。拒绝非终态:提交者补评论可重开。
func (s *SkillTicketService) Reject(ctx context.Context, p auth.Principal, id, reason string) error {
	if !auth.CanManageSkillTicket(p) {
		return ErrSkillTicketDenied
	}
	if _, err := s.loadTicket(ctx, id); err != nil {
		return err
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
