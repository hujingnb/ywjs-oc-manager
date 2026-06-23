package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/guregu/null/v5"
	"golang.org/x/sync/errgroup"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
)

// RechargeStore 抽象 service 需要的存储能力。
type RechargeStore interface {
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	// CreateRechargeRecord 创建充值记录（:exec），写入后通过 GetRechargeRecord 读回。
	CreateRechargeRecord(ctx context.Context, arg sqlc.CreateRechargeRecordParams) error
	GetRechargeRecord(ctx context.Context, id string) (sqlc.RechargeRecord, error)
	ListRechargeRecordsByOrg(ctx context.Context, arg sqlc.ListRechargeRecordsByOrgParams) ([]sqlc.RechargeRecord, error)
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) error
	// SumRechargeAmountByOrg 聚合指定组织 succeeded 充值记录总额；无记录时返回 0。
	SumRechargeAmountByOrg(ctx context.Context, orgID string) (int64, error)
}

// NewAPIRechargeClient 是 service 与 new-api 充值相关的最小接口形态。
type NewAPIRechargeClient interface {
	RechargeUser(ctx context.Context, input newapi.RechargeInput) (newapi.RechargeResult, error)
	GetUserBalance(ctx context.Context, newapiUserID int64) (newapi.BalanceResult, error)
	GetStatusView(ctx context.Context) (newapi.StatusView, error)
}

// RechargeRecordResult 是面向 handler/前端的充值记录视图。
// 不直接复用 sqlc.RechargeRecord 是为了把 null.* 字段转成易序列化的标量。
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
	// TotalRecharged 是该企业历史累计充值额度之和（仅计 succeeded 记录）。
	// 数据来源于 manager 自身 recharge_records 表，不从 new-api 获取。
	TotalRecharged int64 `json:"total_recharged"`
}

// BillingStatusView 是 manager 前端格式化余额 / 用量所需的 new-api 展示配置。
//
// manager 不维护 token 单价；这些字段均来自 new-api /api/status。
type BillingStatusView struct {
	// QuotaPerUnit 是 new-api 展示额度与内部 quota 的换算比例。
	QuotaPerUnit int64 `json:"quota_per_unit"`
	// QuotaDisplayType 是 new-api 当前配置的额度显示类型，例如 USD。
	QuotaDisplayType string `json:"quota_display_type"`
	// DisplayInCurrency 表示 new-api 是否按货币口径展示额度。
	DisplayInCurrency bool `json:"display_in_currency"`
	// CustomCurrencySymbol 是自定义货币符号。
	CustomCurrencySymbol string `json:"custom_currency_symbol"`
	// CustomCurrencyExchangeRate 是自定义货币汇率。
	CustomCurrencyExchangeRate float64 `json:"custom_currency_exchange_rate"`
	// USDExchangeRate 是 new-api 配置的美元汇率。
	USDExchangeRate float64 `json:"usd_exchange_rate"`
	// Price 是 new-api 配置的计价参数，manager 只透传给前端展示层。
	Price float64 `json:"price"`
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
	org, err := s.store.GetOrganization(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return RechargeRecordResult{}, ErrNotFound
	}
	if err != nil {
		return RechargeRecordResult{}, fmt.Errorf("查询企业失败: %w", err)
	}
	if !org.NewapiUserID.Valid || org.NewapiUserID.String == "" {
		return RechargeRecordResult{}, ErrOrgMissingNewAPIUserID
	}
	newapiUserID, err := parseInt64(org.NewapiUserID.String)
	if err != nil {
		return RechargeRecordResult{}, fmt.Errorf("非法 newapi_user_id: %w", err)
	}

	result, callErr := s.client.RechargeUser(ctx, newapi.RechargeInput{
		NewAPIUserID: newapiUserID,
		CreditAmount: amount,
		Remark:       remark,
	})
	status := "succeeded"
	var errMsg null.String
	var refID null.String
	if callErr != nil {
		status = "failed"
		errMsg = null.StringFrom(callErr.Error())
	} else if result.RefID != "" {
		refID = null.StringFrom(result.RefID)
	}
	// CreateRechargeRecord 为 :exec；预先生成 ID，写入后通过 GetRechargeRecord 读回。
	recordID := newUUID()
	// OperatorID 为 string（非空，对应平台管理员 UserID）。
	if err := s.store.CreateRechargeRecord(ctx, sqlc.CreateRechargeRecordParams{
		ID:           recordID,
		OrgID:        orgID,
		OperatorID:   principal.UserID,
		CreditAmount: amount,
		Remark:       nullStr(remark),
		NewapiRefID:  refID,
		Status:       status,
		ErrorMessage: errMsg,
	}); err != nil {
		return RechargeRecordResult{}, fmt.Errorf("写入充值记录失败: %w", err)
	}
	record, err := s.store.GetRechargeRecord(ctx, recordID)
	if err != nil {
		return RechargeRecordResult{}, fmt.Errorf("读取充值记录失败: %w", err)
	}
	// metadata 存储结构化参数：amount/remark，供前端按语言渲染充值详情。
	// 金额单位与 recharge_records.credit_amount 一致（点，整数避免浮点误差）。
	rechargeMeta, _ := json.Marshal(map[string]any{
		"amount": amount,
		"remark": remark,
	})
	if err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ID:           newUUID(),
		ActorID:      null.StringFrom(principal.UserID),
		ActorRole:    principal.Role,
		OrgID:        null.StringFrom(orgID),
		TargetType:   "organization",
		TargetID:     orgID,
		Action:       "recharge",
		Result:       status,
		MetadataJson: rechargeMeta,
	}); err != nil {
		return RechargeRecordResult{}, fmt.Errorf("写入审计日志失败: %w", err)
	}
	if callErr != nil {
		return toRechargeResult(record), fmt.Errorf("充值失败: %w", callErr)
	}
	return toRechargeResult(record), nil
}

// ListRecharges 列出组织充值历史。平台管理员可查任意组织；org_admin 仅可查自己所属组织。
func (s *RechargeService) ListRecharges(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]RechargeRecordResult, error) {
	if !auth.CanViewRecharges(principal, orgID) {
		return nil, ErrForbidden
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
		OrgID: orgID, Limit: limit, Offset: offset,
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

// GetBalance 查询企业当前余额（透传 new-api）及累计充值金额（本地聚合）。
// 两个数据源并发查询：① new-api 取 RemainQuota/UsedQuota；② 本地 DB 聚合 TotalRecharged。
func (s *RechargeService) GetBalance(ctx context.Context, principal auth.Principal, orgID string) (BalanceView, error) {
	if principal.Role != domain.UserRolePlatformAdmin && principal.Role != domain.UserRoleOrgAdmin {
		return BalanceView{}, ErrForbidden
	}
	org, err := s.store.GetOrganization(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return BalanceView{}, ErrNotFound
	}
	if err != nil {
		return BalanceView{}, fmt.Errorf("查询企业失败: %w", err)
	}
	if principal.Role == domain.UserRoleOrgAdmin && principal.OrgID != org.ID {
		return BalanceView{}, ErrForbidden
	}
	if !org.NewapiUserID.Valid || org.NewapiUserID.String == "" {
		return BalanceView{}, ErrOrgMissingNewAPIUserID
	}
	newapiUserID, err := parseInt64(org.NewapiUserID.String)
	if err != nil {
		return BalanceView{}, fmt.Errorf("非法 newapi_user_id: %w", err)
	}

	// 并发执行：① new-api 余额查询（实时，不缓存）；② 本地 DB 累计充值聚合。
	var balance newapi.BalanceResult
	var totalRecharged int64
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var e error
		balance, e = s.client.GetUserBalance(gctx, newapiUserID)
		return e
	})
	g.Go(func() error {
		var e error
		totalRecharged, e = s.store.SumRechargeAmountByOrg(gctx, orgID)
		return e
	})
	if err := g.Wait(); err != nil {
		return BalanceView{}, fmt.Errorf("查询余额失败: %w", err)
	}
	return BalanceView{
		NewAPIUserID:   balance.NewAPIUserID,
		RemainQuota:    balance.RemainQuota,
		UsedQuota:      balance.UsedQuota,
		TotalRecharged: totalRecharged,
	}, nil
}

// GetBillingStatus 透传 new-api 展示配置；manager 不维护 token 单价。
func (s *RechargeService) GetBillingStatus(ctx context.Context, principal auth.Principal) (BillingStatusView, error) {
	if principal.Role != domain.UserRolePlatformAdmin &&
		principal.Role != domain.UserRoleOrgAdmin &&
		principal.Role != domain.UserRoleOrgMember {
		return BillingStatusView{}, ErrForbidden
	}
	status, err := s.client.GetStatusView(ctx)
	if err != nil {
		return BillingStatusView{}, fmt.Errorf("查询 new-api 展示配置失败: %w", err)
	}
	return BillingStatusView{
		QuotaPerUnit:               status.QuotaPerUnit,
		QuotaDisplayType:           status.QuotaDisplayType,
		DisplayInCurrency:          status.DisplayInCurrency,
		CustomCurrencySymbol:       status.CustomCurrencySymbol,
		CustomCurrencyExchangeRate: status.CustomCurrencyExchangeRate,
		USDExchangeRate:            status.USDExchangeRate,
		Price:                      status.Price,
	}, nil
}

func toRechargeResult(r sqlc.RechargeRecord) RechargeRecordResult {
	out := RechargeRecordResult{
		ID:           r.ID,
		OrgID:        r.OrgID,
		// OperatorID 现在是 string（非空），直接使用。
		OperatorID:   r.OperatorID,
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
	// CreatedAt 是 time.Time（MySQL DATETIME），直接格式化。
	out.CreatedAt = r.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
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
