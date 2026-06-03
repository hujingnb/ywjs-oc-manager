package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
)

// stubSource 是 SkillSource 的可控替身，用于隔离 SkillLibraryService 的聚合逻辑测试。
// 通过字段控制 Kind()、Search() 的返回值，无需依赖真实数据库或外部 API。
type stubSource struct {
	// kind 是来源标识，由 Kind() 返回。
	kind string
	// page 是 Search() 的预设成功返回值。
	page SkillPage
	// err 是 Search() 的预设失败返回值（非 nil 时 Search 返回错误）。
	err error
}

// Kind 实现 SkillSource，返回预设的来源标识。
func (s *stubSource) Kind() string { return s.kind }

// Search 实现 SkillSource，返回预设的 SkillPage 或错误。
func (s *stubSource) Search(_ context.Context, _ auth.Principal, _, _ string) (SkillPage, error) {
	if s.err != nil {
		return SkillPage{}, s.err
	}
	return s.page, nil
}

// TestSkillLibraryService_List 覆盖四类 source 参数的路由分支：
//   - "platform"：只走平台来源，返回平台条目。
//   - "clawhub"：只走公共来源，返回公共条目。
//   - ""（空）：聚合两者，platform 条目在前，clawhub 条目追加。
//   - "github"（未知）：返回 ErrSkillMarketSourceUnknown。
func TestSkillLibraryService_List(t *testing.T) {
	// 构造平台库与公共库各自的 stub 来源，各含一个条目。
	plat := &stubSource{
		kind: "platform",
		page: SkillPage{Entries: []SkillEntry{{Source: "platform", Name: "p1"}}},
	}
	claw := &stubSource{
		kind: "clawhub",
		page: SkillPage{Entries: []SkillEntry{{Source: "clawhub", Name: "c1"}}},
	}
	svc := NewSkillLibraryService(plat, claw)

	// source=platform：只返回平台库条目，公共库不参与。
	page, err := svc.List(context.Background(), auth.Principal{}, "platform", "", "")
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "platform", page.Entries[0].Source)

	// source=clawhub：只返回公共库条目，平台库不参与。
	page, err = svc.List(context.Background(), auth.Principal{}, "clawhub", "", "")
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "clawhub", page.Entries[0].Source)

	// source=""（空字符串）：聚合两者，共 2 条，platform 在前。
	page, err = svc.List(context.Background(), auth.Principal{}, "", "", "")
	require.NoError(t, err)
	assert.Len(t, page.Entries, 2)
	assert.Equal(t, "platform", page.Entries[0].Source)
	assert.Equal(t, "clawhub", page.Entries[1].Source)

	// source="github"（未知来源）：返回 ErrSkillMarketSourceUnknown，handler 映射为 400。
	_, err = svc.List(context.Background(), auth.Principal{}, "github", "", "")
	require.ErrorIs(t, err, ErrSkillMarketSourceUnknown)
}

// TestSkillLibraryService_List_ClawHubNil 验证 clawhub 为 nil 时的降级行为：
// - source=clawhub 返回空列表，不报错。
// - source="" 只返回平台库条目，公共库缺失不阻断。
func TestSkillLibraryService_List_ClawHubNil(t *testing.T) {
	// 构造不含公共库来源的 service（ClawHub 未配置场景）。
	plat := &stubSource{
		kind: "platform",
		page: SkillPage{Entries: []SkillEntry{{Source: "platform", Name: "p1"}}},
	}
	svc := NewSkillLibraryService(plat, nil)

	// source=clawhub 且 clawhub 为 nil：返回空列表，不报错。
	page, err := svc.List(context.Background(), auth.Principal{}, "clawhub", "", "")
	require.NoError(t, err)
	assert.Empty(t, page.Entries)

	// source="" 且 clawhub 为 nil：仅返回平台库条目。
	page, err = svc.List(context.Background(), auth.Principal{}, "", "", "")
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "platform", page.Entries[0].Source)
}

// TestSkillLibraryService_List_ClawHubFailureDegrades 验证聚合模式的降级行为：
// 公共库 Search 返回错误时，平台库条目正常返回，公共库失败被静默忽略（spec 要求）。
func TestSkillLibraryService_List_ClawHubFailureDegrades(t *testing.T) {
	plat := &stubSource{
		kind: "platform",
		page: SkillPage{Entries: []SkillEntry{{Source: "platform", Name: "p1"}}},
	}
	// 模拟 ClawHub 调用失败（网络超时等）。
	claw := &stubSource{
		kind: "clawhub",
		err:  errors.New("clawhub timeout"),
	}
	svc := NewSkillLibraryService(plat, claw)

	// source="" 聚合时 clawhub 失败：仅返回平台库条目，不报错（降级）。
	page, err := svc.List(context.Background(), auth.Principal{}, "", "", "")
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "platform", page.Entries[0].Source)
}
