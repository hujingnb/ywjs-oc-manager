package service

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// fakeSkillTicketStore 是 SkillTicketStore 的内存实现,供 service 单测使用。
type fakeSkillTicketStore struct {
	tickets  map[string]sqlc.SkillTicket          // id -> 工单
	messages map[string][]sqlc.SkillTicketMessage // ticketID -> 消息
	targets  map[string][]sqlc.CustomSkillTarget  // customSkillName -> 可见范围
	users    map[string]sqlc.User                 // id -> 用户展示信息
	orgs     map[string]sqlc.Organization         // id -> 企业展示信息
}

type fakeSkillTicketTxRunner struct {
	called bool
	store  SkillTicketStore
}

// WithSkillTicketTx 记录事务入口调用,并把测试 fake store 暴露给业务闭包。
func (r *fakeSkillTicketTxRunner) WithSkillTicketTx(ctx context.Context, fn func(SkillTicketStore) error) error {
	r.called = true
	return fn(r.store)
}

func newFakeSkillTicketStore() *fakeSkillTicketStore {
	return &fakeSkillTicketStore{
		tickets:  map[string]sqlc.SkillTicket{},
		messages: map[string][]sqlc.SkillTicketMessage{},
		targets:  map[string][]sqlc.CustomSkillTarget{},
		users:    map[string]sqlc.User{},
		orgs:     map[string]sqlc.Organization{},
	}
}

func (f *fakeSkillTicketStore) CreateSkillTicket(_ context.Context, a sqlc.CreateSkillTicketParams) error {
	f.tickets[a.ID] = sqlc.SkillTicket{
		ID: a.ID, OrgID: a.OrgID, RequesterUserID: a.RequesterUserID, RequesterRole: a.RequesterRole,
		Title: a.Title, Status: a.Status,
	}
	return nil
}
func (f *fakeSkillTicketStore) GetSkillTicket(_ context.Context, id string) (sqlc.SkillTicket, error) {
	t, ok := f.tickets[id]
	if !ok {
		return sqlc.SkillTicket{}, sql.ErrNoRows
	}
	return t, nil
}
func (f *fakeSkillTicketStore) ListSkillTicketsByRequester(_ context.Context, uid string) ([]sqlc.SkillTicket, error) {
	var out []sqlc.SkillTicket
	for _, t := range f.tickets {
		if t.RequesterUserID == uid {
			out = append(out, t)
		}
	}
	return out, nil
}
func (f *fakeSkillTicketStore) ListAllSkillTickets(_ context.Context) ([]sqlc.SkillTicket, error) {
	var out []sqlc.SkillTicket
	for _, t := range f.tickets {
		out = append(out, t)
	}
	return out, nil
}
func (f *fakeSkillTicketStore) UpdateSkillTicketStatus(_ context.Context, a sqlc.UpdateSkillTicketStatusParams) error {
	t := f.tickets[a.ID]
	t.Status = a.Status
	f.tickets[a.ID] = t
	return nil
}
func (f *fakeSkillTicketStore) SetSkillTicketQuote(_ context.Context, a sqlc.SetSkillTicketQuoteParams) error {
	t := f.tickets[a.ID]
	t.QuoteAmountCents = a.QuoteAmountCents
	f.tickets[a.ID] = t
	return nil
}
func (f *fakeSkillTicketStore) RejectSkillTicket(_ context.Context, a sqlc.RejectSkillTicketParams) error {
	t := f.tickets[a.ID]
	t.Status = "rejected"
	t.RejectReason = a.RejectReason
	f.tickets[a.ID] = t
	return nil
}
func (f *fakeSkillTicketStore) TouchSkillTicket(_ context.Context, _ string) error { return nil }
func (f *fakeSkillTicketStore) CountPendingSkillTickets(_ context.Context) (int64, error) {
	var n int64
	for _, t := range f.tickets {
		if t.Status == "pending" {
			n++
		}
	}
	return n, nil
}
func (f *fakeSkillTicketStore) ListSkillTicketMessages(_ context.Context, ticketID string) ([]sqlc.SkillTicketMessage, error) {
	return f.messages[ticketID], nil
}
func (f *fakeSkillTicketStore) CreateSkillTicketMessage(_ context.Context, a sqlc.CreateSkillTicketMessageParams) error {
	f.messages[a.TicketID] = append(f.messages[a.TicketID], sqlc.SkillTicketMessage{
		ID: a.ID, TicketID: a.TicketID, AuthorUserID: a.AuthorUserID, Kind: a.Kind, Body: a.Body,
	})
	return nil
}
func (f *fakeSkillTicketStore) ListCustomSkillTargetsByName(_ context.Context, name string) ([]sqlc.CustomSkillTarget, error) {
	return f.targets[name], nil
}
func (f *fakeSkillTicketStore) GetUser(_ context.Context, id string) (sqlc.User, error) {
	user, ok := f.users[id]
	if !ok {
		return sqlc.User{}, sql.ErrNoRows
	}
	return user, nil
}
func (f *fakeSkillTicketStore) GetOrganization(_ context.Context, id string) (sqlc.Organization, error) {
	org, ok := f.orgs[id]
	if !ok {
		return sqlc.Organization{}, sql.ErrNoRows
	}
	return org, nil
}

func memberP() auth.Principal {
	return auth.Principal{UserID: "u-mem", OrgID: "org-1", Role: domain.UserRoleOrgMember}
}
func adminP() auth.Principal {
	return auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin}
}

// 成员提交工单:落库为 pending,org/角色/标题正确回填。
func TestSkillTicketService_Submit_OK(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	res, err := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "周报技能", Description: "每周汇总"})
	require.NoError(t, err)
	assert.Equal(t, SkillTicketStatusPending, res.Status)
	assert.Equal(t, "org-1", res.OrgID)
	assert.Equal(t, domain.UserRoleOrgMember, res.RequesterRole)
	assert.Equal(t, "周报技能", res.Title)
}

// 成员提交工单时,描述不再写入工单主表,而是作为提交者发送的第一条 text 消息。
func TestSkillTicketService_Submit_CreatesDescriptionMessage(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)

	res, err := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "周报技能", Description: "每周汇总"})
	require.NoError(t, err)
	detail, err := svc.Get(context.Background(), memberP(), res.ID)
	require.NoError(t, err)
	require.Len(t, detail.Messages, 1)
	assert.Equal(t, MessageKindText, detail.Messages[0].Kind)
	assert.Equal(t, "u-mem", detail.Messages[0].AuthorUserID)
	assert.Equal(t, "每周汇总", detail.Messages[0].Text)
}

// 成员提交工单在生产装配了事务 runner 时,主表与首条需求消息应通过同一个事务入口执行。
func TestSkillTicketService_Submit_UsesTransactionRunner(t *testing.T) {
	store := newFakeSkillTicketStore()
	runner := &fakeSkillTicketTxRunner{store: store}
	svc := NewSkillTicketServiceWithTx(store, runner)

	_, err := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "周报技能", Description: "每周汇总"})
	require.NoError(t, err)
	assert.True(t, runner.called)
}

// 平台管理员不能提交工单(不提需求)。
func TestSkillTicketService_Submit_Denied(t *testing.T) {
	svc := NewSkillTicketService(newFakeSkillTicketStore())
	_, err := svc.Submit(context.Background(), adminP(), SubmitSkillTicketInput{Title: "x", Description: "y"})
	require.ErrorIs(t, err, ErrSkillTicketDenied)
}

// 标题为空 → Invalid。
func TestSkillTicketService_Submit_Invalid(t *testing.T) {
	svc := NewSkillTicketService(newFakeSkillTicketStore())
	_, err := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "  ", Description: "y"})
	require.ErrorIs(t, err, ErrSkillTicketInvalid)
}

// 详情:提交者本人可看,含消息;企业内他人看不到(Denied)。
func TestSkillTicketService_Get_Visibility(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	res, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "t", Description: "d"})

	detail, err := svc.Get(context.Background(), memberP(), res.ID)
	require.NoError(t, err)
	assert.Equal(t, res.ID, detail.ID)

	other := auth.Principal{UserID: "u-other", OrgID: "org-1", Role: domain.UserRoleOrgMember}
	_, err = svc.Get(context.Background(), other, res.ID)
	require.ErrorIs(t, err, ErrSkillTicketDenied)
}

// 详情:已交付工单应带出当前 custom_skill_targets,供前端编辑可见范围时回填。
func TestSkillTicketService_Get_IncludesTargets(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	res, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "t", Description: "d"})
	row := store.tickets[res.ID]
	row.Status = SkillTicketStatusDelivered
	row.CustomSkillName = nullStr("weekly")
	store.tickets[res.ID] = row
	store.targets["weekly"] = []sqlc.CustomSkillTarget{
		{ID: "target-1", CustomSkillName: "weekly", OrgID: "org-1", Audience: "org_admins"},
	}

	detail, err := svc.Get(context.Background(), adminP(), res.ID)
	require.NoError(t, err)
	require.Len(t, detail.Targets, 1)
	assert.Equal(t, "org-1", detail.Targets[0].OrgID)
	assert.Equal(t, "org_admins", detail.Targets[0].Audience)
}

// 平台管理员查看详情时需要看到提交者与所属企业的可读名称,便于判断工单来源。
func TestSkillTicketService_Get_IncludesRequesterAndOrganizationNames(t *testing.T) {
	store := newFakeSkillTicketStore()
	store.users["u-mem"] = sqlc.User{ID: "u-mem", Username: "member-a", DisplayName: "张三"}
	store.orgs["org-1"] = sqlc.Organization{ID: "org-1", Name: "甲公司", Code: "test-org"}
	svc := NewSkillTicketService(store)
	res, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "t", Description: "d"})

	detail, err := svc.Get(context.Background(), adminP(), res.ID)
	require.NoError(t, err)
	assert.Equal(t, "张三", detail.RequesterName)
	assert.Equal(t, "甲公司", detail.OrgName)
}

// 开始制作:pending → processing 成功,非 pending 状态拒绝。
func TestSkillTicketService_StartProcessing(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	res, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "t", Description: "d"})
	require.NoError(t, svc.StartProcessing(context.Background(), adminP(), res.ID))
	got, _ := store.GetSkillTicket(context.Background(), res.ID)
	assert.Equal(t, SkillTicketStatusProcessing, got.Status)

	err := svc.StartProcessing(context.Background(), adminP(), res.ID)
	require.ErrorIs(t, err, ErrSkillTicketInvalid)
}

// 拒绝:pending/processing 可拒绝,delivered 不可拒绝。
func TestSkillTicketService_RejectPrecondition(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	pending, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "p", Description: "d"})
	require.NoError(t, svc.Reject(context.Background(), adminP(), pending.ID, "不清晰"))
	assert.Equal(t, SkillTicketStatusRejected, store.tickets[pending.ID].Status)

	processing, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "pr", Description: "d"})
	require.NoError(t, svc.StartProcessing(context.Background(), adminP(), processing.ID))
	require.NoError(t, svc.Reject(context.Background(), adminP(), processing.ID, "不做"))
	assert.Equal(t, SkillTicketStatusRejected, store.tickets[processing.ID].Status)

	delivered, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "d", Description: "d"})
	row := store.tickets[delivered.ID]
	row.Status = SkillTicketStatusDelivered
	store.tickets[delivered.ID] = row
	err := svc.Reject(context.Background(), adminP(), delivered.ID, "不能拒")
	require.ErrorIs(t, err, ErrSkillTicketInvalid)
}

// 重新受理:仅 rejected → processing,非 rejected 状态拒绝。
func TestSkillTicketService_ReopenRejected(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	res, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "t", Description: "d"})
	require.NoError(t, svc.Reject(context.Background(), adminP(), res.ID, "不清晰"))

	require.NoError(t, svc.ReopenRejected(context.Background(), adminP(), res.ID))
	got, _ := store.GetSkillTicket(context.Background(), res.ID)
	assert.Equal(t, SkillTicketStatusProcessing, got.Status)

	err := svc.ReopenRejected(context.Background(), adminP(), res.ID)
	require.ErrorIs(t, err, ErrSkillTicketInvalid)
}

// 设报价:pending/processing 可改,delivered/rejected 禁止。
func TestSkillTicketService_SetQuotePrecondition(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	res, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "t", Description: "d"})
	require.NoError(t, svc.SetQuote(context.Background(), adminP(), res.ID, 80000))
	got, _ := store.GetSkillTicket(context.Background(), res.ID)
	assert.EqualValues(t, 80000, got.QuoteAmountCents.Int64)

	require.NoError(t, svc.StartProcessing(context.Background(), adminP(), res.ID))
	require.NoError(t, svc.SetQuote(context.Background(), adminP(), res.ID, 90000))
	got, _ = store.GetSkillTicket(context.Background(), res.ID)
	assert.EqualValues(t, 90000, got.QuoteAmountCents.Int64)

	delivered, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "delivered", Description: "d"})
	row := store.tickets[delivered.ID]
	row.Status = SkillTicketStatusDelivered
	store.tickets[delivered.ID] = row
	require.ErrorIs(t, svc.SetQuote(context.Background(), adminP(), delivered.ID, 1), ErrSkillTicketInvalid)

	rejected, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "rejected", Description: "d"})
	require.NoError(t, svc.Reject(context.Background(), adminP(), rejected.ID, "不清晰"))
	require.ErrorIs(t, svc.SetQuote(context.Background(), adminP(), rejected.ID, 1), ErrSkillTicketInvalid)
}

// 非平台管理员调用任意管理员状态动作都应被拒绝。
func TestSkillTicketService_ActionsRequireAdmin(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	res, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "t", Description: "d"})

	require.ErrorIs(t, svc.StartProcessing(context.Background(), memberP(), res.ID), ErrSkillTicketDenied)
	require.ErrorIs(t, svc.ReopenRejected(context.Background(), memberP(), res.ID), ErrSkillTicketDenied)
	require.ErrorIs(t, svc.SetQuote(context.Background(), memberP(), res.ID, 1), ErrSkillTicketDenied)
	require.ErrorIs(t, svc.Reject(context.Background(), memberP(), res.ID, "x"), ErrSkillTicketDenied)
}

// 待处理角标:仅 pending 计数。
func TestSkillTicketService_PendingBadgeCount(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	a, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "a", Description: "d"})
	_, _ = svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "b", Description: "d"})
	require.NoError(t, svc.StartProcessing(context.Background(), adminP(), a.ID)) // 移出角标
	n, err := svc.PendingBadgeCount(context.Background(), adminP())
	require.NoError(t, err)
	assert.EqualValues(t, 1, n) // 仅剩 b 是 pending
}
