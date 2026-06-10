package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// fakeCustomStore 是 CustomSkillStore 的内存实现,并复用工单读取与置交付,供交付/取装单测。
type fakeCustomStore struct {
	skills    map[string]sqlc.CustomSkill // key: name|version
	targets   map[string][]sqlc.CustomSkillTarget
	tickets   map[string]sqlc.SkillTicket
	delivered map[string]string // ticketID -> custom_skill_name,记录置交付时锁定的技能名
}

func newFakeCustomStore() *fakeCustomStore {
	return &fakeCustomStore{
		skills:    map[string]sqlc.CustomSkill{},
		targets:   map[string][]sqlc.CustomSkillTarget{},
		tickets:   map[string]sqlc.SkillTicket{},
		delivered: map[string]string{},
	}
}

func (f *fakeCustomStore) CreateCustomSkill(_ context.Context, a sqlc.CreateCustomSkillParams) error {
	f.skills[a.Name+"|"+a.Version] = sqlc.CustomSkill{
		ID: a.ID, Name: a.Name, Description: a.Description, Version: a.Version,
		TarPath: a.TarPath, TicketID: a.TicketID, FileSize: a.FileSize, FileSha256: a.FileSha256,
	}
	return nil
}

func (f *fakeCustomStore) GetCustomSkillByNameVersion(_ context.Context, a sqlc.GetCustomSkillByNameVersionParams) (sqlc.CustomSkill, error) {
	r, ok := f.skills[a.Name+"|"+a.Version]
	if !ok {
		return sqlc.CustomSkill{}, sql.ErrNoRows
	}
	return r, nil
}

func (f *fakeCustomStore) GetSkillTicket(_ context.Context, id string) (sqlc.SkillTicket, error) {
	t, ok := f.tickets[id]
	if !ok {
		return sqlc.SkillTicket{}, sql.ErrNoRows
	}
	return t, nil
}

func (f *fakeCustomStore) CreateCustomSkillTarget(_ context.Context, a sqlc.CreateCustomSkillTargetParams) error {
	f.targets[a.CustomSkillName] = append(f.targets[a.CustomSkillName], sqlc.CustomSkillTarget{
		ID: a.ID, CustomSkillName: a.CustomSkillName, OrgID: a.OrgID, Audience: a.Audience,
	})
	return nil
}

func (f *fakeCustomStore) ListCustomSkillTargetsByName(_ context.Context, name string) ([]sqlc.CustomSkillTarget, error) {
	return f.targets[name], nil
}

func (f *fakeCustomStore) MarkSkillTicketDelivered(_ context.Context, a sqlc.MarkSkillTicketDeliveredParams) error {
	t := f.tickets[a.ID]
	t.Status = "delivered"
	t.CustomSkillName = a.CustomSkillName
	f.tickets[a.ID] = t
	f.delivered[a.ID] = a.CustomSkillName.String
	return nil
}

// flatTarWithName 复用平台库测试的 makeFlatSkillTar,生成一个能通过 hermes.InspectFlatSkillArchive
// 扁平契约校验的最小 skill tar(根级 SKILL.md,frontmatter name=name)。
func flatTarWithName(t *testing.T, name string) []byte { return makeFlatSkillTar(t, name) }

// adminPrincipalCS 返回平台管理员主体,可交付定制技能。
func adminPrincipalCS() auth.Principal {
	return auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin}
}

// fixedClock 返回固定时钟(2026-06-10 15:30:12 UTC),使版本号稳定为 20260610-153012,便于断言。
func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 10, 15, 30, 12, 0, time.UTC) }
}

// 首次交付:解析归档 name、自动生成版本=20260610-153012、写归档+目标范围+置工单 delivered。
func TestCustomSkillService_Deliver_First(t *testing.T) {
	store := newFakeCustomStore()
	store.tickets["tk-1"] = sqlc.SkillTicket{ID: "tk-1", OrgID: "org-1", RequesterUserID: "u-mem", RequesterRole: domain.UserRoleOrgMember, Status: "processing"}
	blobs := newFakeBlobs()
	svc := NewCustomSkillService(store, blobs)
	svc.now = fixedClock()

	res, err := svc.Deliver(context.Background(), adminPrincipalCS(), DeliverCustomSkillInput{
		TicketID: "tk-1", Description: "周报技能", Data: flatTarWithName(t, "weekly-report"),
		Targets: []CustomSkillTargetInput{{OrgID: "org-1", Audience: "all_org"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "weekly-report", res.Name)      // 归档 frontmatter 解析出的技能名
	assert.Equal(t, "20260610-153012", res.Version) // 按固定时钟生成的版本号
	assert.Equal(t, "delivered", store.tickets["tk-1"].Status)
	assert.Equal(t, "weekly-report", store.tickets["tk-1"].CustomSkillName.String) // 工单锁定技能名
	assert.Len(t, store.targets["weekly-report"], 1)                               // 首次交付写入一条目标范围
}

// 再次交付时归档 name 与工单已锁定 name 不一致 → NameMismatch。
func TestCustomSkillService_Deliver_NameMismatch(t *testing.T) {
	store := newFakeCustomStore()
	store.tickets["tk-1"] = sqlc.SkillTicket{ID: "tk-1", OrgID: "org-1", RequesterUserID: "u-mem", RequesterRole: domain.UserRoleOrgMember, Status: "delivered", CustomSkillName: nullStr("weekly-report")}
	svc := NewCustomSkillService(store, newFakeBlobs())
	svc.now = fixedClock()
	_, err := svc.Deliver(context.Background(), adminPrincipalCS(), DeliverCustomSkillInput{
		TicketID: "tk-1", Data: flatTarWithName(t, "different-name"),
		Targets: []CustomSkillTargetInput{{OrgID: "org-1", Audience: "all_org"}},
	})
	require.ErrorIs(t, err, ErrCustomSkillNameMismatch)
}

// 非平台管理员交付被拒。
func TestCustomSkillService_Deliver_Denied(t *testing.T) {
	store := newFakeCustomStore()
	store.tickets["tk-1"] = sqlc.SkillTicket{ID: "tk-1", OrgID: "org-1"}
	svc := NewCustomSkillService(store, newFakeBlobs())
	_, err := svc.Deliver(context.Background(), memberAP(), DeliverCustomSkillInput{TicketID: "tk-1", Data: flatTarWithName(t, "x"), Targets: []CustomSkillTargetInput{{OrgID: "org-1", Audience: "all_org"}}})
	require.ErrorIs(t, err, ErrCustomSkillDenied)
}

// 无目标范围 → Invalid。
func TestCustomSkillService_Deliver_NoTargets(t *testing.T) {
	store := newFakeCustomStore()
	store.tickets["tk-1"] = sqlc.SkillTicket{ID: "tk-1", OrgID: "org-1", RequesterRole: domain.UserRoleOrgMember}
	svc := NewCustomSkillService(store, newFakeBlobs())
	svc.now = fixedClock()
	_, err := svc.Deliver(context.Background(), adminPrincipalCS(), DeliverCustomSkillInput{TicketID: "tk-1", Data: flatTarWithName(t, "x"), Targets: nil})
	require.ErrorIs(t, err, ErrCustomSkillInvalid)
}
