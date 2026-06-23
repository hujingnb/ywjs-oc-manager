// Package service 的 recharge_service_test 覆盖组织充值服务对权限、余额同步和审计记录的处理。
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	null "github.com/guregu/null/v5"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
)

const (
	testRechargeOrgID = "00000000-0000-0000-0000-000000002001"
	testRechargeOpID  = "00000000-0000-0000-0000-000000002099"
)

// TestRecharge_HappyPath 验证充值成功路径的成功路径场景。
func TestRecharge_HappyPath(t *testing.T) {
	store := newRechargeStub(t, "1234")
	client := &fakeNewAPIRecharge{rechargeResult: newapi.RechargeResult{RefID: "ref-9", RemainQuota: 5000}}
	svc := NewRechargeService(store, client)

	result, err := svc.Recharge(context.Background(), platformAdmin(), testRechargeOrgID, 1000, "test")
	require.NoError(t, err)
	require.Equal(t, "succeeded", result.Status)
	require.Equal(t, "ref-9", result.NewAPIRefID)
	if !store.recordWritten || !store.auditWritten {
		t.Fatalf("record/audit 未写: %+v", store)
	}
	if client.lastInput.NewAPIUserID != 1234 || client.lastInput.CreditAmount != 1000 {
		t.Fatalf("client 调用 = %+v", client.lastInput)
	}
	// 审计不再写入冻结中文文案，改用 metadata 存储结构化参数：amount/remark。
	require.False(t, store.lastAuditCreate.DetailMessage.Valid, "新记录不应写入 detail_message")
	require.NotEmpty(t, store.lastAuditCreate.MetadataJson, "metadata 必须存储 amount/remark")
	// 验证 metadata 包含结构化充值参数：金额和备注。
	var meta map[string]any
	require.NoError(t, json.Unmarshal(store.lastAuditCreate.MetadataJson, &meta))
	require.Equal(t, float64(1000), meta["amount"], "metadata.amount 应为充值金额") // JSON 数字解析为 float64
	require.Equal(t, "test", meta["remark"], "metadata.remark 应为备注")
}

// TestRecharge_DeniesNonPlatformAdmin 验证充值Denies非平台管理员的预期行为场景。
func TestRecharge_DeniesNonPlatformAdmin(t *testing.T) {
	store := newRechargeStub(t, "1234")
	svc := NewRechargeService(store, &fakeNewAPIRecharge{})
	_, err := svc.Recharge(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin}, testRechargeOrgID, 100, "")
	require.ErrorIs(t, err, ErrRechargeDenied)
}

// TestRecharge_RejectsZeroAmount 验证充值拒绝零金额的异常或拒绝路径场景。
func TestRecharge_RejectsZeroAmount(t *testing.T) {
	store := newRechargeStub(t, "1234")
	svc := NewRechargeService(store, &fakeNewAPIRecharge{})
	_, err := svc.Recharge(context.Background(), platformAdmin(), testRechargeOrgID, 0, "")
	require.ErrorIs(t, err, ErrInvalidRechargeAmount)
}

// TestRecharge_RejectsMissingNewAPIUserID 验证充值拒绝缺失new-api用户ID的异常或拒绝路径场景。
func TestRecharge_RejectsMissingNewAPIUserID(t *testing.T) {
	store := newRechargeStub(t, "")
	svc := NewRechargeService(store, &fakeNewAPIRecharge{})
	_, err := svc.Recharge(context.Background(), platformAdmin(), testRechargeOrgID, 100, "")
	require.ErrorIs(t, err, ErrOrgMissingNewAPIUserID)
}

// TestRecharge_OrganizationLookupErrorUsesEnterpriseCopy 验证充值查询企业失败时返回企业文案。
func TestRecharge_OrganizationLookupErrorUsesEnterpriseCopy(t *testing.T) {
	store := newRechargeStub(t, "1234")
	store.getOrgErr = errors.New("database down")
	svc := NewRechargeService(store, &fakeNewAPIRecharge{})

	_, err := svc.Recharge(context.Background(), platformAdmin(), testRechargeOrgID, 100, "")
	require.ErrorContains(t, err, "查询企业失败")
}

// TestRecharge_NewAPIErrorStillWritesFailedRecord 验证充值new-api错误仍然写入失败记录的成功路径场景。
func TestRecharge_NewAPIErrorStillWritesFailedRecord(t *testing.T) {
	store := newRechargeStub(t, "1234")
	client := &fakeNewAPIRecharge{rechargeErr: errors.New("upstream blow")}
	svc := NewRechargeService(store, client)
	_, err := svc.Recharge(context.Background(), platformAdmin(), testRechargeOrgID, 1000, "")
	require.Error(t, err)
	require.True(t, store.recordWritten)
	require.Equal(t, "failed", store.lastRecordStatus)
	require.True(t, store.auditWritten)
	// 审计不再写入冻结中文文案，改用 metadata 存储结构化参数。
	// 场景：失败路径下 metadata 仍包含 amount，remark 为空字符串。
	require.False(t, store.lastAuditCreate.DetailMessage.Valid, "失败路径新记录也不应写入 detail_message")
	var meta map[string]any
	require.NoError(t, json.Unmarshal(store.lastAuditCreate.MetadataJson, &meta))
	require.Equal(t, float64(1000), meta["amount"], "metadata.amount 应为充值金额")
	require.Equal(t, "", meta["remark"], "空备注下 metadata.remark 应为空字符串")
}

// TestListRecharges_DeniesOrgMember 验证普通成员无权查看充值记录。
func TestListRecharges_DeniesOrgMember(t *testing.T) {
	store := newRechargeStub(t, "1234")
	svc := NewRechargeService(store, &fakeNewAPIRecharge{})
	_, err := svc.ListRecharges(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testRechargeOrgID}, testRechargeOrgID, 0, 0)
	require.ErrorIs(t, err, ErrForbidden)
}

// TestListRecharges_HappyPath 验证列表充值记录成功路径的成功路径场景。
func TestListRecharges_HappyPath(t *testing.T) {
	store := newRechargeStub(t, "1234")
	store.records = []sqlc.RechargeRecord{
		{ID: mustUUID(t, "00000000-0000-0000-0000-000000002201"), OrgID: mustUUID(t, testRechargeOrgID), CreditAmount: 100, Status: "succeeded"}, // 场景：成功充值记录应出现在列表结果中。
		{ID: mustUUID(t, "00000000-0000-0000-0000-000000002202"), OrgID: mustUUID(t, testRechargeOrgID), CreditAmount: 200, Status: "failed"},    // 场景：失败充值记录也应按存储返回参与列表展示。
	}
	svc := NewRechargeService(store, &fakeNewAPIRecharge{})
	results, err := svc.ListRecharges(context.Background(), platformAdmin(), testRechargeOrgID, 50, 0)
	require.NoError(t, err)
	require.Len(t, results, 2)
}

// TestListRecharges_OrgAdminCanViewOwnOrg 验证 org_admin 可以查看自己组织的充值记录。
func TestListRecharges_OrgAdminCanViewOwnOrg(t *testing.T) {
	store := newRechargeStub(t, "1234")
	store.records = []sqlc.RechargeRecord{
		{ID: mustUUID(t, "00000000-0000-0000-0000-000000002201"), OrgID: mustUUID(t, testRechargeOrgID), CreditAmount: 100, Status: "succeeded"}, // 场景：org_admin 查询自己组织，应正常返回记录。
	}
	svc := NewRechargeService(store, &fakeNewAPIRecharge{})
	results, err := svc.ListRecharges(context.Background(),
		auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testRechargeOrgID}, testRechargeOrgID, 50, 0)
	require.NoError(t, err)
	require.Len(t, results, 1)
}

// TestListRecharges_OrgAdminCannotViewOtherOrg 验证 org_admin 无权查看其他组织的充值记录。
func TestListRecharges_OrgAdminCannotViewOtherOrg(t *testing.T) {
	store := newRechargeStub(t, "1234")
	svc := NewRechargeService(store, &fakeNewAPIRecharge{})
	_, err := svc.ListRecharges(context.Background(),
		auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "other-org-id"}, testRechargeOrgID, 50, 0)
	require.ErrorIs(t, err, ErrForbidden)
}

// TestGetBalance_IncludesTotalRecharged 验证 GetBalance 正确聚合并返回累计充值金额。
func TestGetBalance_IncludesTotalRecharged(t *testing.T) {
	store := newRechargeStub(t, "1234")
	store.totalRecharged = 3000
	client := &fakeNewAPIRecharge{balanceResult: newapi.BalanceResult{NewAPIUserID: 1234, RemainQuota: 2000}}
	svc := NewRechargeService(store, client)
	view, err := svc.GetBalance(context.Background(), platformAdmin(), testRechargeOrgID)
	require.NoError(t, err)
	require.Equal(t, int64(3000), view.TotalRecharged)
	require.Equal(t, int64(2000), view.RemainQuota)
}

// TestGetBalance_PlatformAdminAllowed 验证获取余额平台管理员Allowed的预期行为场景。
func TestGetBalance_PlatformAdminAllowed(t *testing.T) {
	store := newRechargeStub(t, "1234")
	client := &fakeNewAPIRecharge{balanceResult: newapi.BalanceResult{NewAPIUserID: 1234, RemainQuota: 8000}}
	svc := NewRechargeService(store, client)
	view, err := svc.GetBalance(context.Background(), platformAdmin(), testRechargeOrgID)
	require.NoError(t, err)
	require.Equal(t, int64(8000), view.RemainQuota)
}

// TestGetBalance_OrgAdminMustMatchOrg 验证获取余额组织管理员必须匹配组织的预期行为场景。
func TestGetBalance_OrgAdminMustMatchOrg(t *testing.T) {
	store := newRechargeStub(t, "1234")
	client := &fakeNewAPIRecharge{balanceResult: newapi.BalanceResult{NewAPIUserID: 1234, RemainQuota: 0}}
	svc := NewRechargeService(store, client)
	_, err := svc.GetBalance(context.Background(),
		auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "another"}, testRechargeOrgID)
	require.ErrorIs(t, err, ErrForbidden)

	if _, err := svc.GetBalance(context.Background(),
		auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testRechargeOrgID}, testRechargeOrgID); err != nil {
		t.Fatalf("同 org 应当允许: %v", err)
	}
}

// TestGetBalance_OrganizationLookupErrorUsesEnterpriseCopy 验证余额查询企业失败时返回企业文案。
func TestGetBalance_OrganizationLookupErrorUsesEnterpriseCopy(t *testing.T) {
	store := newRechargeStub(t, "1234")
	store.getOrgErr = errors.New("database down")
	svc := NewRechargeService(store, &fakeNewAPIRecharge{})

	_, err := svc.GetBalance(context.Background(), platformAdmin(), testRechargeOrgID)
	require.ErrorContains(t, err, "查询企业失败")
}

// TestRechargeServiceGetBillingStatusProxiesNewAPIStatus 验证 billing status 直接透传 new-api 展示配置。
func TestRechargeServiceGetBillingStatusProxiesNewAPIStatus(t *testing.T) {
	client := &fakeNewAPIRecharge{statusResult: newapi.StatusView{
		QuotaPerUnit:               500000,
		QuotaDisplayType:           "USD",
		DisplayInCurrency:          true,
		CustomCurrencySymbol:       "¤",
		CustomCurrencyExchangeRate: 1,
		USDExchangeRate:            7.3,
		Price:                      7.3,
	}}
	svc := NewRechargeService(newRechargeStub(t, "4"), client)

	view, err := svc.GetBillingStatus(context.Background(), platformAdmin())

	require.NoError(t, err)
	require.Equal(t, int64(500000), view.QuotaPerUnit)
	require.Equal(t, "USD", view.QuotaDisplayType)
	require.True(t, view.DisplayInCurrency)
}

type rechargeStub struct {
	t                *testing.T
	org              sqlc.Organization
	records          []sqlc.RechargeRecord
	recordWritten    bool
	lastRecordStatus string
	// lastRecord 记录最近一次 CreateRechargeRecord 写入的参数，供 GetRechargeRecord 读回。
	lastRecord      sqlc.CreateRechargeRecordParams
	auditWritten     bool
	getOrgErr        error
	// lastAuditCreate 记录最近一次 CreateAuditLog 入参，便于断言 detail 等字段。
	lastAuditCreate sqlc.CreateAuditLogParams
	totalRecharged  int64 // SumRechargeAmountByOrg 的桩返回值
}

func newRechargeStub(t *testing.T, newapiUserID string) *rechargeStub {
	return &rechargeStub{
		t: t,
		org: sqlc.Organization{
			ID:           mustUUID(t, testRechargeOrgID),
			Name:         "测试组织",
			Status:       domain.StatusActive,
			NewapiUserID: func() null.String {
				if newapiUserID == "" {
					return null.String{}
				}
				return null.StringFrom(newapiUserID)
			}(),
		},
	}
}

func (s *rechargeStub) GetOrganization(_ context.Context, _ string) (sqlc.Organization, error) {
	if s.getOrgErr != nil {
		return sqlc.Organization{}, s.getOrgErr
	}
	return s.org, nil
}

// CreateRechargeRecord 为 :exec；stub 记录参数供后续 GetRechargeRecord 读回。
func (s *rechargeStub) CreateRechargeRecord(_ context.Context, arg sqlc.CreateRechargeRecordParams) error {
	s.recordWritten = true
	s.lastRecordStatus = arg.Status
	s.lastRecord = arg
	return nil
}

// GetRechargeRecord 供 :exec 写入后读回记录使用。
func (s *rechargeStub) GetRechargeRecord(_ context.Context, id string) (sqlc.RechargeRecord, error) {
	if s.lastRecord.ID != id {
		return sqlc.RechargeRecord{}, sql.ErrNoRows
	}
	return sqlc.RechargeRecord{
		ID:           s.lastRecord.ID,
		OrgID:        s.lastRecord.OrgID,
		OperatorID:   s.lastRecord.OperatorID,
		CreditAmount: s.lastRecord.CreditAmount,
		Remark:       s.lastRecord.Remark,
		NewapiRefID:  s.lastRecord.NewapiRefID,
		Status:       s.lastRecord.Status,
		ErrorMessage: s.lastRecord.ErrorMessage,
	}, nil
}

func (s *rechargeStub) ListRechargeRecordsByOrg(_ context.Context, _ sqlc.ListRechargeRecordsByOrgParams) ([]sqlc.RechargeRecord, error) {
	return s.records, nil
}

// CreateAuditLog 为 :exec；stub 记录参数供测试断言。
func (s *rechargeStub) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) error {
	s.auditWritten = true
	s.lastAuditCreate = arg
	return nil
}

func (s *rechargeStub) SumRechargeAmountByOrg(_ context.Context, _ string) (int64, error) {
	return s.totalRecharged, nil
}

type fakeNewAPIRecharge struct {
	rechargeResult newapi.RechargeResult
	rechargeErr    error
	balanceResult  newapi.BalanceResult
	balanceErr     error
	statusResult   newapi.StatusView
	statusErr      error
	lastInput      newapi.RechargeInput
}

func (f *fakeNewAPIRecharge) RechargeUser(_ context.Context, input newapi.RechargeInput) (newapi.RechargeResult, error) {
	f.lastInput = input
	if f.rechargeErr != nil {
		return newapi.RechargeResult{}, f.rechargeErr
	}
	return f.rechargeResult, nil
}

func (f *fakeNewAPIRecharge) GetUserBalance(_ context.Context, _ int64) (newapi.BalanceResult, error) {
	return f.balanceResult, f.balanceErr
}

func (f *fakeNewAPIRecharge) GetStatusView(_ context.Context) (newapi.StatusView, error) {
	if f.statusErr != nil {
		return newapi.StatusView{}, f.statusErr
	}
	return f.statusResult, nil
}
