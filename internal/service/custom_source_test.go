package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// fakeVisibleStore 内存实现 CustomSourceStore：按「org|admin|user」键返回预置可见行，
// 供 CustomSource 单测注入不同主体的可见性结果。
type fakeVisibleStore struct {
	visible map[string][]sqlc.ListVisibleCustomSkillsRow // key: org|admin|user
}

// ListVisibleCustomSkills 按主体键命中预置可见行；IsAdmin 为 interface{}，用 itoa 归一成 0/1 拼键。
func (f *fakeVisibleStore) ListVisibleCustomSkills(_ context.Context, a sqlc.ListVisibleCustomSkillsParams) ([]sqlc.ListVisibleCustomSkillsRow, error) {
	key := a.OrgID + "|" + itoa(a.IsAdmin) + "|" + a.UserID
	return f.visible[key], nil
}

// itoa 把 IsAdmin（sqlc 生成为 interface{}）断言成 int64 后归一为 "1"/"0"，用于拼可见性键。
func itoa(v interface{}) string {
	if n, ok := v.(int64); ok && n == 1 {
		return "1"
	}
	return "0"
}

// 成员可见 all_org 的定制技能，且带申请人展示名与命中范围徽章。
func TestCustomSource_Search_MemberSeesAllOrg(t *testing.T) {
	// 预置一条 all_org 可见的定制技能（ListVisibleCustomSkillsRow 为平铺结构，直接赋字段）。
	store := &fakeVisibleStore{visible: map[string][]sqlc.ListVisibleCustomSkillsRow{
		"org-1|0|u-mem": {{Name: "weekly-report", Description: "周报", Version: "20260610-153012", RequesterUsername: "张三", Audience: "all_org"}},
	}}
	src := NewCustomSource(store)
	// org_member 主体：is_admin=0，命中 all_org 行。
	p := auth.Principal{OrgID: "org-1", UserID: "u-mem", Role: domain.UserRoleOrgMember}
	page, err := src.Search(context.Background(), p, "", "")
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "custom", page.Entries[0].Source)
	assert.Equal(t, "weekly-report", page.Entries[0].Name)
	assert.Equal(t, "张三", page.Entries[0].RequesterName)
	assert.Equal(t, "all_org", page.Entries[0].Audience)
}

// 同名多版本只返回最新一条（查询已 created_at DESC，首条即最新）。
func TestCustomSource_Search_LatestPerName(t *testing.T) {
	// 同名两行：首条为新版本（20260610-153012），第二条为旧版本（20260601-100000）。
	store := &fakeVisibleStore{visible: map[string][]sqlc.ListVisibleCustomSkillsRow{
		"org-1|0|u-mem": {
			{Name: "wr", Version: "20260610-153012", Audience: "all_org"}, // 新（查询已 created_at DESC，首条）
			{Name: "wr", Version: "20260601-100000", Audience: "all_org"}, // 旧
		},
	}}
	src := NewCustomSource(store)
	page, err := src.Search(context.Background(), auth.Principal{OrgID: "org-1", UserID: "u-mem", Role: domain.UserRoleOrgMember}, "", "")
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "20260610-153012", page.Entries[0].Version)
}

// q 子串过滤 name/description（大小写不敏感）：只命中 invoice-ocr。
func TestCustomSource_Search_Filter(t *testing.T) {
	// 两条不同名技能，按 q="invoice" 子串只应保留 invoice-ocr。
	store := &fakeVisibleStore{visible: map[string][]sqlc.ListVisibleCustomSkillsRow{
		"org-1|0|u-mem": {
			{Name: "weekly-report", Description: "周报", Audience: "all_org"},
			{Name: "invoice-ocr", Description: "发票", Audience: "all_org"},
		},
	}}
	src := NewCustomSource(store)
	page, _ := src.Search(context.Background(), auth.Principal{OrgID: "org-1", UserID: "u-mem", Role: domain.UserRoleOrgMember}, "invoice", "")
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "invoice-ocr", page.Entries[0].Name)
}
