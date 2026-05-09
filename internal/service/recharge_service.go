package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
)

// RechargeStore 抽象 service 需要的存储能力。
type RechargeStore interface {
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	CreateRechargeRecord(ctx context.Context, arg sqlc.CreateRechargeRecordParams) (sqlc.RechargeRecord, error)
	ListRechargeRecordsByOrg(ctx context.Context, arg sqlc.ListRechargeRecordsByOrgParams) ([]sqlc.RechargeRecord, error)
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
}

// NewAPIRechargeClient 是 service 与 new-api 充值相关的最小接口形态。
type NewAPIRechargeClient interface {
	RechargeUser(ctx context.Context, input newapi.RechargeInput) (newapi.RechargeResult, error)
	GetUserBalance(ctx context.Context, newapiUserID int64) (newapi.BalanceResult, error)
}

// RechargeRecordResult 是面向 handler/前端的充值记录视图。
// 不直接复用 sqlc.RechargeRecord 是为了把 pgtype 字段转成易序列化的标量。
type RechargeRecordResult struct {
	ID           string `json:"id"`
	OrgID        string `json:"org_id"`
	OperatorID   string `json:"operator_id,omitempty"`
	CreditAmount int64  `json:"credit_amount"`
	Remark       string `json:"remark,omitempty"`
	NewAPIRefID  string `json:"newapi_ref_id,omitempty"`
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message,omitempty"`
	CreatedAt    string `json:"created_at"`
}

// BalanceView 是 GET /organizations/:id/balance 接口的响应。
type BalanceView struct {
	NewAPIUserID int64 `json:"newapi_user_id"`
	RemainQuota  int64 `json:"remain_quota"`
	UsedQuota    int64 `json:"used_quota"`
}

// RechargeService 串起 new-api 充值与本地审计/记录写入。
//
// 设计要点：
//   - 仅平台管理员可触发；其它角色一律返回 ErrRechargeDenied；
//   - new-api 调用成功后才写 recharge_records.status='succeeded'，失败写 'failed'；
//   - 不论成功失败都写一条审计日志，便于追溯触发人；
//   - 余额查询直接透传 new-api，不在 manager 端缓存，避免对账问题。
type RechargeService struct {
	store  RechargeStore
	client NewAPIRechargeClient
}

// NewRechargeService 创建 recharge 服务。
func NewRechargeService(store RechargeStore, client NewAPIRechargeClient) *RechargeService {
	return &RechargeService{store: store, client: client}
}

// Recharge 给指定组织增加点数。
func (s *RechargeService) Recharge(ctx context.Context, principal auth.Principal, orgID string, amount int64, remark string) (RechargeRecordResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return RechargeRecordResult{}, ErrRechargeDenied
	}
	if amount <= 0 {
		return RechargeRecordResult{}, ErrInvalidRechargeAmount
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return RechargeRecordResult{}, ErrNotFound
	}
	org, err := s.store.GetOrganization(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return RechargeRecordResult{}, ErrNotFound
	}
	if err != nil {
		return RechargeRecordResult{}, fmt.Errorf("查询组织失败: %w", err)
	}
	if !org.NewapiUserID.Valid || org.NewapiUserID.String == "" {
		return RechargeRecordResult{}, ErrOrgMissingNewAPIUserID
	}
	newapiUserID, err := parseInt64(org.NewapiUserID.String)
	if err != nil {
		return RechargeRecordResult{}, fmt.Errorf("非法 newapi_user_id: %w", err)
	}

	operatorUUID, _ := optionalUUID(principal.UserID)
	result, callErr := s.client.RechargeUser(ctx, newapi.RechargeInput{
		NewAPIUserID: newapiUserID,
		CreditAmount: amount,
		Remark:       remark,
	})
	status := "succeeded"
	errMsg := pgtype.Text{}
	refID := pgtype.Text{}
	if callErr != nil {
		status = "failed"
		errMsg = pgtype.Text{String: callErr.Error(), Valid: true}
	} else if result.RefID != "" {
		refID = pgtype.Text{String: result.RefID, Valid: true}
	}
	record, err := s.store.CreateRechargeRecord(ctx, sqlc.CreateRechargeRecordParams{
		OrgID:        id,
		OperatorID:   operatorUUID,
		CreditAmount: amount,
		Remark:       pgtype.Text{String: remark, Valid: remark != ""},
		NewapiRefID:  refID,
		Status:       status,
		ErrorMessage: errMsg,
	})
	if err != nil {
		return RechargeRecordResult{}, fmt.Errorf("写入充值记录失败: %w", err)
	}
	if _, err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ActorID:    operatorUUID,
		ActorRole:  principal.Role,
		OrgID:      id,
		TargetType: "organization",
		TargetID:   uuidToString(id),
		Action:     "recharge",
		Result:     status,
	}); err != nil {
		return RechargeRecordResult{}, fmt.Errorf("写入审计日志失败: %w", err)
	}
	if callErr != nil {
		return toRechargeResult(record), fmt.Errorf("充值失败: %w", callErr)
	}
	return toRechargeResult(record), nil
}

// ListRecharges 列出组织充值历史。仅平台管理员可访问。
func (s *RechargeService) ListRecharges(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]RechargeRecordResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return nil, ErrRechargeDenied
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return nil, ErrNotFound
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	records, err := s.store.ListRechargeRecordsByOrg(ctx, sqlc.ListRechargeRecordsByOrgParams{
		OrgID: id, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("查询充值记录失败: %w", err)
	}
	results := make([]RechargeRecordResult, 0, len(records))
	for _, record := range records {
		results = append(results, toRechargeResult(record))
	}
	return results, nil
}

// GetBalance 查询组织当前余额（透传 new-api）。
func (s *RechargeService) GetBalance(ctx context.Context, principal auth.Principal, orgID string) (BalanceView, error) {
	if principal.Role != domain.UserRolePlatformAdmin && principal.Role != domain.UserRoleOrgAdmin {
		return BalanceView{}, ErrForbidden
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return BalanceView{}, ErrNotFound
	}
	org, err := s.store.GetOrganization(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return BalanceView{}, ErrNotFound
	}
	if err != nil {
		return BalanceView{}, fmt.Errorf("查询组织失败: %w", err)
	}
	if principal.Role == domain.UserRoleOrgAdmin && principal.OrgID != uuidToString(org.ID) {
		return BalanceView{}, ErrForbidden
	}
	if !org.NewapiUserID.Valid || org.NewapiUserID.String == "" {
		return BalanceView{}, ErrOrgMissingNewAPIUserID
	}
	newapiUserID, err := parseInt64(org.NewapiUserID.String)
	if err != nil {
		return BalanceView{}, fmt.Errorf("非法 newapi_user_id: %w", err)
	}
	balance, err := s.client.GetUserBalance(ctx, newapiUserID)
	if err != nil {
		return BalanceView{}, fmt.Errorf("查询余额失败: %w", err)
	}
	return BalanceView{
		NewAPIUserID: balance.NewAPIUserID,
		RemainQuota:  balance.RemainQuota,
		UsedQuota:    balance.UsedQuota,
	}, nil
}

func toRechargeResult(r sqlc.RechargeRecord) RechargeRecordResult {
	out := RechargeRecordResult{
		ID:           uuidToString(r.ID),
		OrgID:        uuidToString(r.OrgID),
		OperatorID:   uuidToOptionalString(r.OperatorID),
		CreditAmount: r.CreditAmount,
		Status:       r.Status,
	}
	if r.Remark.Valid {
		out.Remark = r.Remark.String
	}
	if r.NewapiRefID.Valid {
		out.NewAPIRefID = r.NewapiRefID.String
	}
	if r.ErrorMessage.Valid {
		out.ErrorMessage = r.ErrorMessage.String
	}
	if r.CreatedAt.Valid {
		out.CreatedAt = r.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	return out
}

// parseInt64 把字符串解析为 int64，主要用于 newapi_user_id 这类外部数字 ID。
func parseInt64(value string) (int64, error) {
	var result int64
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("非法数字字符串: %q", value)
		}
		result = result*10 + int64(ch-'0')
	}
	return result, nil
}
