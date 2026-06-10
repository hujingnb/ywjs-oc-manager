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
	comments map[string][]sqlc.SkillTicketComment // ticketID -> 评论
}

func newFakeSkillTicketStore() *fakeSkillTicketStore {
	return &fakeSkillTicketStore{tickets: map[string]sqlc.SkillTicket{}, comments: map[string][]sqlc.SkillTicketComment{}}
}

func (f *fakeSkillTicketStore) CreateSkillTicket(_ context.Context, a sqlc.CreateSkillTicketParams) error {
	f.tickets[a.ID] = sqlc.SkillTicket{
		ID: a.ID, OrgID: a.OrgID, RequesterUserID: a.RequesterUserID, RequesterRole: a.RequesterRole,
		Title: a.Title, Description: a.Description, Status: a.Status,
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
func (f *fakeSkillTicketStore) CreateSkillTicketComment(_ context.Context, a sqlc.CreateSkillTicketCommentParams) error {
	f.comments[a.TicketID] = append(f.comments[a.TicketID], sqlc.SkillTicketComment{
		ID: a.ID, TicketID: a.TicketID, AuthorUserID: a.AuthorUserID, Body: a.Body,
	})
	return nil
}
func (f *fakeSkillTicketStore) ListSkillTicketComments(_ context.Context, ticketID string) ([]sqlc.SkillTicketComment, error) {
	return f.comments[ticketID], nil
}

func memberP() auth.Principal { return auth.Principal{UserID: "u-mem", OrgID: "org-1", Role: domain.UserRoleOrgMember} }
func adminP() auth.Principal  { return auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin} }

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

// 详情:提交者本人可看,含评论;企业内他人看不到(Denied)。
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

// 提交者在 rejected 工单上发评论 → 自动回 pending(重开)。
func TestSkillTicketService_AddComment_ReopensRejected(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	res, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "t", Description: "d"})
	require.NoError(t, svc.Reject(context.Background(), adminP(), res.ID, "不清晰"))

	_, err := svc.AddComment(context.Background(), memberP(), res.ID, "补充:要支持飞书")
	require.NoError(t, err)
	got, _ := store.GetSkillTicket(context.Background(), res.ID)
	assert.Equal(t, SkillTicketStatusPending, got.Status) // 重开
}

// 管理员在 pending 工单上发评论 → 不重开(仍 pending,不改状态)。
func TestSkillTicketService_AddComment_AdminNoReopen(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	res, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "t", Description: "d"})
	require.NoError(t, svc.UpdateStatus(context.Background(), adminP(), res.ID, SkillTicketStatusProcessing))

	_, err := svc.AddComment(context.Background(), adminP(), res.ID, "已接单")
	require.NoError(t, err)
	got, _ := store.GetSkillTicket(context.Background(), res.ID)
	assert.Equal(t, SkillTicketStatusProcessing, got.Status) // 管理员评论不重开
}

// 管理员接单:pending → processing。
func TestSkillTicketService_UpdateStatus_OK(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	res, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "t", Description: "d"})
	require.NoError(t, svc.UpdateStatus(context.Background(), adminP(), res.ID, SkillTicketStatusProcessing))
	got, _ := store.GetSkillTicket(context.Background(), res.ID)
	assert.Equal(t, SkillTicketStatusProcessing, got.Status)
}

// 改状态只允许 pending/processing;传 delivered 由交付链路(Plan 2)负责 → Invalid。
func TestSkillTicketService_UpdateStatus_RejectDelivered(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	res, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "t", Description: "d"})
	err := svc.UpdateStatus(context.Background(), adminP(), res.ID, SkillTicketStatusDelivered)
	require.ErrorIs(t, err, ErrSkillTicketInvalid)
}

// 非平台管理员改状态被拒。
func TestSkillTicketService_UpdateStatus_Denied(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	res, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "t", Description: "d"})
	err := svc.UpdateStatus(context.Background(), memberP(), res.ID, SkillTicketStatusProcessing)
	require.ErrorIs(t, err, ErrSkillTicketDenied)
}

// 管理员设报价(分);非管理员被拒。
func TestSkillTicketService_SetQuote(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	res, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "t", Description: "d"})
	require.NoError(t, svc.SetQuote(context.Background(), adminP(), res.ID, 80000))
	got, _ := store.GetSkillTicket(context.Background(), res.ID)
	assert.EqualValues(t, 80000, got.QuoteAmountCents.Int64)

	require.ErrorIs(t, svc.SetQuote(context.Background(), memberP(), res.ID, 1), ErrSkillTicketDenied)
}

// 管理员拒绝:status=rejected + reason 落库。
func TestSkillTicketService_Reject(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	res, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "t", Description: "d"})
	require.NoError(t, svc.Reject(context.Background(), adminP(), res.ID, "需求不清晰"))
	got, _ := store.GetSkillTicket(context.Background(), res.ID)
	assert.Equal(t, SkillTicketStatusRejected, got.Status)
	assert.Equal(t, "需求不清晰", got.RejectReason.String)
}

// 待处理角标:仅 pending 计数。
func TestSkillTicketService_PendingBadgeCount(t *testing.T) {
	store := newFakeSkillTicketStore()
	svc := NewSkillTicketService(store)
	a, _ := svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "a", Description: "d"})
	_, _ = svc.Submit(context.Background(), memberP(), SubmitSkillTicketInput{Title: "b", Description: "d"})
	require.NoError(t, svc.UpdateStatus(context.Background(), adminP(), a.ID, SkillTicketStatusProcessing)) // 移出角标
	n, err := svc.PendingBadgeCount(context.Background(), adminP())
	require.NoError(t, err)
	assert.EqualValues(t, 1, n) // 仅剩 b 是 pending
}
