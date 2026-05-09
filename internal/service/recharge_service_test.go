package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
	"github.com/stretchr/testify/require"
)

const (
	testRechargeOrgID = "00000000-0000-0000-0000-000000002001"
	testRechargeOpID  = "00000000-0000-0000-0000-000000002099"
)

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
}

func TestRecharge_DeniesNonPlatformAdmin(t *testing.T) {
	store := newRechargeStub(t, "1234")
	svc := NewRechargeService(store, &fakeNewAPIRecharge{})
	_, err := svc.Recharge(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin}, testRechargeOrgID, 100, "")
	require.ErrorIs(t, err, ErrRechargeDenied)
}

func TestRecharge_RejectsZeroAmount(t *testing.T) {
	store := newRechargeStub(t, "1234")
	svc := NewRechargeService(store, &fakeNewAPIRecharge{})
	_, err := svc.Recharge(context.Background(), platformAdmin(), testRechargeOrgID, 0, "")
	require.ErrorIs(t, err, ErrInvalidRechargeAmount)
}

func TestRecharge_RejectsMissingNewAPIUserID(t *testing.T) {
	store := newRechargeStub(t, "")
	svc := NewRechargeService(store, &fakeNewAPIRecharge{})
	_, err := svc.Recharge(context.Background(), platformAdmin(), testRechargeOrgID, 100, "")
	require.ErrorIs(t, err, ErrOrgMissingNewAPIUserID)
}

func TestRecharge_NewAPIErrorStillWritesFailedRecord(t *testing.T) {
	store := newRechargeStub(t, "1234")
	client := &fakeNewAPIRecharge{rechargeErr: errors.New("upstream blow")}
	svc := NewRechargeService(store, client)
	_, err := svc.Recharge(context.Background(), platformAdmin(), testRechargeOrgID, 1000, "")
	require.Error(t, err)
	require.True(t, store.recordWritten)
	require.Equal(t, "failed", store.lastRecordStatus)
	require.True(t, store.auditWritten)
}

func TestListRecharges_DeniesNonPlatformAdmin(t *testing.T) {
	store := newRechargeStub(t, "1234")
	svc := NewRechargeService(store, &fakeNewAPIRecharge{})
	_, err := svc.ListRecharges(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin}, testRechargeOrgID, 0, 0)
	require.ErrorIs(t, err, ErrRechargeDenied)
}

func TestListRecharges_HappyPath(t *testing.T) {
	store := newRechargeStub(t, "1234")
	store.records = []sqlc.RechargeRecord{
		{ID: mustUUID(t, "00000000-0000-0000-0000-000000002201"), OrgID: mustUUID(t, testRechargeOrgID), CreditAmount: 100, Status: "succeeded"},
		{ID: mustUUID(t, "00000000-0000-0000-0000-000000002202"), OrgID: mustUUID(t, testRechargeOrgID), CreditAmount: 200, Status: "failed"},
	}
	svc := NewRechargeService(store, &fakeNewAPIRecharge{})
	results, err := svc.ListRecharges(context.Background(), platformAdmin(), testRechargeOrgID, 50, 0)
	require.NoError(t, err)
	require.Len(t, results, 2)
}

func TestGetBalance_PlatformAdminAllowed(t *testing.T) {
	store := newRechargeStub(t, "1234")
	client := &fakeNewAPIRecharge{balanceResult: newapi.BalanceResult{NewAPIUserID: 1234, RemainQuota: 8000}}
	svc := NewRechargeService(store, client)
	view, err := svc.GetBalance(context.Background(), platformAdmin(), testRechargeOrgID)
	require.NoError(t, err)
	require.Equal(t, int64(8000), view.RemainQuota)
}

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

type rechargeStub struct {
	t                *testing.T
	org              sqlc.Organization
	records          []sqlc.RechargeRecord
	recordWritten    bool
	lastRecordStatus string
	auditWritten     bool
}

func newRechargeStub(t *testing.T, newapiUserID string) *rechargeStub {
	return &rechargeStub{
		t: t,
		org: sqlc.Organization{
			ID:           mustUUID(t, testRechargeOrgID),
			Name:         "测试组织",
			Status:       domain.StatusActive,
			NewapiUserID: pgtype.Text{String: newapiUserID, Valid: newapiUserID != ""},
		},
	}
}

func (s *rechargeStub) GetOrganization(_ context.Context, _ pgtype.UUID) (sqlc.Organization, error) {
	return s.org, nil
}

func (s *rechargeStub) CreateRechargeRecord(_ context.Context, arg sqlc.CreateRechargeRecordParams) (sqlc.RechargeRecord, error) {
	s.recordWritten = true
	s.lastRecordStatus = arg.Status
	return sqlc.RechargeRecord{
		ID:           mustUUID(s.t, "00000000-0000-0000-0000-000000002301"),
		OrgID:        arg.OrgID,
		OperatorID:   arg.OperatorID,
		CreditAmount: arg.CreditAmount,
		Remark:       arg.Remark,
		NewapiRefID:  arg.NewapiRefID,
		Status:       arg.Status,
		ErrorMessage: arg.ErrorMessage,
	}, nil
}

func (s *rechargeStub) ListRechargeRecordsByOrg(_ context.Context, _ sqlc.ListRechargeRecordsByOrgParams) ([]sqlc.RechargeRecord, error) {
	return s.records, nil
}

func (s *rechargeStub) CreateAuditLog(_ context.Context, _ sqlc.CreateAuditLogParams) (sqlc.AuditLog, error) {
	s.auditWritten = true
	return sqlc.AuditLog{}, nil
}

type fakeNewAPIRecharge struct {
	rechargeResult newapi.RechargeResult
	rechargeErr    error
	balanceResult  newapi.BalanceResult
	balanceErr     error
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
