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
	// detail 是 Detail() 的预设返回值。
	detail SkillDetailResult
	// versions 是 Versions() 的预设返回值。
	versions []SkillVersionResult
	// downloadData 是 Download() 的预设归档字节。
	downloadData []byte
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

// Detail 实现 SkillSource，返回预设的详情或错误。
func (s *stubSource) Detail(_ context.Context, _ auth.Principal, _ string) (SkillDetailResult, error) {
	if s.err != nil {
		return SkillDetailResult{}, s.err
	}
	return s.detail, nil
}

// Versions 实现 SkillSource，返回预设的版本列表或错误。
func (s *stubSource) Versions(_ context.Context, _ auth.Principal, _ string) ([]SkillVersionResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.versions, nil
}

// Download 实现 SkillSource，返回预设的归档字节或错误（ext 取自 kind：platform=tar，其余=zip）。
func (s *stubSource) Download(_ context.Context, _, _ string) ([]byte, string, error) {
	if s.err != nil {
		return nil, "", s.err
	}
	ext := "zip"
	if s.kind == "platform" {
		ext = "tar"
	}
	return s.downloadData, ext, nil
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

// TestSkillLibraryService_Detail 覆盖 Detail 的来源路由（返回详情 + 版本列表）：
//   - "platform"/"clawhub" 各走对应来源。
//   - clawhub 未配置（nil）时返回空详情/空版本、不报错。
//   - 未知来源返回 ErrSkillMarketSourceUnknown。
func TestSkillLibraryService_Detail(t *testing.T) {
	plat := &stubSource{kind: "platform", detail: SkillDetailResult{Name: "weather", Source: "platform"}, versions: []SkillVersionResult{{Version: "2.0"}, {Version: "1.0"}}}
	claw := &stubSource{kind: "clawhub", detail: SkillDetailResult{Name: "Self-Improving Agent", Source: "clawhub", Stars: 3735}, versions: []SkillVersionResult{{Version: "3.0.21", Changelog: "re-upload"}}}
	svc := NewSkillLibraryService(plat, claw)

	// platform：详情 + 版本。
	pd, pv, err := svc.Detail(context.Background(), auth.Principal{}, "platform", "weather")
	require.NoError(t, err)
	assert.Equal(t, "weather", pd.Name)
	assert.Len(t, pv, 2)

	// clawhub：详情含统计、版本含 changelog。
	cd, cv, err := svc.Detail(context.Background(), auth.Principal{}, "clawhub", "self-improving-agent")
	require.NoError(t, err)
	assert.EqualValues(t, 3735, cd.Stars)
	require.Len(t, cv, 1)
	assert.Equal(t, "re-upload", cv[0].Changelog)

	// 未知来源 → 哨兵错误。
	_, _, err = svc.Detail(context.Background(), auth.Principal{}, "github", "x")
	require.ErrorIs(t, err, ErrSkillMarketSourceUnknown)

	// clawhub 未配置（nil）→ 空详情/空版本、不报错。
	svcNoClaw := NewSkillLibraryService(plat, nil)
	_, empty, err := svcNoClaw.Detail(context.Background(), auth.Principal{}, "clawhub", "x")
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// TestSkillLibraryService_Download 覆盖下载的来源路由与归档/扩展名返回：
//   - platform：走平台来源，ext=tar，字节透传。
//   - clawhub：走公共来源，ext=zip，字节透传。
//   - 未知来源：ErrSkillMarketSourceUnknown。
func TestSkillLibraryService_Download(t *testing.T) {
	plat := &stubSource{kind: "platform", downloadData: []byte("TAR-BYTES")} // 平台来源预设 tar 字节
	claw := &stubSource{kind: "clawhub", downloadData: []byte("ZIP-BYTES")}  // 公共来源预设 zip 字节
	svc := NewSkillLibraryService(plat, claw)

	// platform 来源：返回 tar 字节与 ext=tar。
	data, ext, err := svc.Download(context.Background(), psvcPlatformPrincipal(), "platform", "weather", "1.0")
	require.NoError(t, err)
	assert.Equal(t, []byte("TAR-BYTES"), data)
	assert.Equal(t, "tar", ext)

	// clawhub 来源：返回 zip 字节与 ext=zip。
	data, ext, err = svc.Download(context.Background(), psvcPlatformPrincipal(), "clawhub", "self-improving", "2.0")
	require.NoError(t, err)
	assert.Equal(t, []byte("ZIP-BYTES"), data)
	assert.Equal(t, "zip", ext)

	// 未知来源：返回 ErrSkillMarketSourceUnknown。
	_, _, err = svc.Download(context.Background(), psvcPlatformPrincipal(), "github", "x", "1.0")
	require.ErrorIs(t, err, ErrSkillMarketSourceUnknown)
}

// TestSkillLibraryService_Download_Denied 验证非平台管理员下载被拒（ErrSkillMarketDenied）。
func TestSkillLibraryService_Download_Denied(t *testing.T) {
	svc := NewSkillLibraryService(&stubSource{kind: "platform"}, &stubSource{kind: "clawhub"})
	// 空角色（非平台管理员）下载平台技能归档应被拒。
	_, _, err := svc.Download(context.Background(), auth.Principal{}, "platform", "weather", "1.0")
	require.ErrorIs(t, err, ErrSkillMarketDenied)
}

// TestSkillLibraryService_Download_Invalid 验证缺 ref 或 version 时返回 ErrSkillMarketInvalid。
func TestSkillLibraryService_Download_Invalid(t *testing.T) {
	svc := NewSkillLibraryService(&stubSource{kind: "platform"}, nil)
	// 缺版本号。
	_, _, err := svc.Download(context.Background(), psvcPlatformPrincipal(), "platform", "weather", "")
	require.ErrorIs(t, err, ErrSkillMarketInvalid)
	// 缺 ref（name/slug）。
	_, _, err = svc.Download(context.Background(), psvcPlatformPrincipal(), "platform", "", "1.0")
	require.ErrorIs(t, err, ErrSkillMarketInvalid)
}

// TestSkillLibraryService_Download_ClawHubNil 验证未配置公共库时下载 clawhub 来源返回 SourceUnknown。
func TestSkillLibraryService_Download_ClawHubNil(t *testing.T) {
	svc := NewSkillLibraryService(&stubSource{kind: "platform"}, nil) // clawhub 未配置
	_, _, err := svc.Download(context.Background(), psvcPlatformPrincipal(), "clawhub", "x", "1.0")
	require.ErrorIs(t, err, ErrSkillMarketSourceUnknown)
}
