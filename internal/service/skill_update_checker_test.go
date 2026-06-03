// skill_update_checker_test.go — SkillUpdateChecker 单测（TDD）。
//
// 覆盖场景：
//  1. platform 来源：平台库存在更高版本时，回写 latest_version（非 NULL）；
//     版本相同时回写 NULL（无更新）。
//  2. clawhub 来源：fake ListVersions 返回更高版本时，回写 latest_version。
//  3. 单条 UpdateAppSkillLatest 失败不中断其他行的处理。
//  4. clawhub 为 nil 时跳过所有 clawhub 来源。
//  5. platform ListPlatformSkills 失败时跳过所有 platform 来源，不影响 clawhub 来源。
package service

import (
	"context"
	"errors"
	"testing"

	"github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/store/sqlc"
)

// =========================================================
// Fake 实现
// =========================================================

// fakeCheckerAppSkillStore 是 SkillUpdateCheckerAppSkillStore 的内存实现，
// 支持预置 distinct sources、app_skills 行，以及追踪 UpdateAppSkillLatest 调用。
type fakeCheckerAppSkillStore struct {
	// sources 预置的 distinct (source, source_ref) 列表
	sources []sqlc.ListDistinctAppSkillSourcesRow
	// rows 预置的 app_skills 行，key 为 "source|source_ref"
	rows map[string][]sqlc.AppSkill
	// updates 记录每次 UpdateAppSkillLatest 调用的参数（按顺序）
	updates []sqlc.UpdateAppSkillLatestParams
	// updateErrForID 预置指定 id 的 UpdateAppSkillLatest 返回错误
	updateErrForID map[string]error
}

func newFakeCheckerAppSkillStore() *fakeCheckerAppSkillStore {
	return &fakeCheckerAppSkillStore{
		rows:           map[string][]sqlc.AppSkill{},
		updateErrForID: map[string]error{},
	}
}

// addSource 添加一条 distinct (source, source_ref)。
func (f *fakeCheckerAppSkillStore) addSource(source, sourceRef string) {
	f.sources = append(f.sources, sqlc.ListDistinctAppSkillSourcesRow{
		Source:    source,
		SourceRef: sourceRef,
	})
}

// addSkill 向指定 (source, source_ref) 预置一条 app_skills 行。
func (f *fakeCheckerAppSkillStore) addSkill(source, sourceRef, id, version string) {
	key := source + "|" + sourceRef
	f.rows[key] = append(f.rows[key], sqlc.AppSkill{
		ID:        id,
		Source:    source,
		SourceRef: sourceRef,
		Version:   version,
	})
}

// setUpdateErr 预置指定 app_skills id 的 UpdateAppSkillLatest 错误。
func (f *fakeCheckerAppSkillStore) setUpdateErr(id string, err error) {
	f.updateErrForID[id] = err
}

func (f *fakeCheckerAppSkillStore) ListDistinctAppSkillSources(_ context.Context) ([]sqlc.ListDistinctAppSkillSourcesRow, error) {
	return f.sources, nil
}

func (f *fakeCheckerAppSkillStore) ListAppSkillsBySourceRef(_ context.Context, arg sqlc.ListAppSkillsBySourceRefParams) ([]sqlc.AppSkill, error) {
	key := arg.Source + "|" + arg.SourceRef
	return f.rows[key], nil
}

func (f *fakeCheckerAppSkillStore) UpdateAppSkillLatest(_ context.Context, arg sqlc.UpdateAppSkillLatestParams) error {
	f.updates = append(f.updates, arg)
	if err, ok := f.updateErrForID[arg.ID]; ok {
		return err
	}
	return nil
}

// fakeCheckerPlatformStore 是 SkillUpdateCheckerPlatformStore 的内存实现。
type fakeCheckerPlatformStore struct {
	// skills 预置的 platform_skills 行（调用方应按 name ASC, created_at DESC 排序预置）
	skills []sqlc.PlatformSkill
	// err 预置 ListPlatformSkills 的错误
	err error
}

func (f *fakeCheckerPlatformStore) ListPlatformSkills(_ context.Context) ([]sqlc.PlatformSkill, error) {
	return f.skills, f.err
}

// fakeClawHubVersionLister 是 ClawHubVersionLister 的内存实现。
type fakeClawHubVersionLister struct {
	// versions 预置各 slug 的版本列表，key 为 slug
	versions map[string][]SkillVersion
	// err 预置 ListVersions 的错误
	err error
}

func newFakeClawHubVersionLister() *fakeClawHubVersionLister {
	return &fakeClawHubVersionLister{versions: map[string][]SkillVersion{}}
}

// setVersions 预置某 slug 的版本列表。
func (f *fakeClawHubVersionLister) setVersions(slug string, versions []SkillVersion) {
	f.versions[slug] = versions
}

func (f *fakeClawHubVersionLister) ListVersions(_ context.Context, slug string) ([]SkillVersion, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.versions[slug], nil
}

// =========================================================
// 测试用例
// =========================================================

// TestSkillUpdateChecker_Platform_HigherVersion 验证 platform 来源时，
// 平台库存在比已安装版本更高的版本，回写 latest_version 为最高版本（非 NULL）。
func TestSkillUpdateChecker_Platform_HigherVersion(t *testing.T) {
	ctx := context.Background()

	store := newFakeCheckerAppSkillStore()
	// 预置一个 platform 来源 skill，安装版本 1.0.0
	store.addSource("platform", "my-skill")
	store.addSkill("platform", "my-skill", "app-skill-id-1", "1.0.0")

	// 平台库中 my-skill 有两个版本：1.2.0（最新，排前）和 1.0.0（旧版）
	platform := &fakeCheckerPlatformStore{
		skills: []sqlc.PlatformSkill{
			{Name: "my-skill", Version: "1.2.0"}, // 首条即最新
			{Name: "my-skill", Version: "1.0.0"},
		},
	}
	checker := NewSkillUpdateChecker(store, platform, nil)

	err := checker.Tick(ctx)
	require.NoError(t, err, "Tick 不应返回错误")

	// 应有一次 UpdateAppSkillLatest 调用，latest_version 为 "1.2.0"
	require.Len(t, store.updates, 1, "应有且仅有一次 UpdateAppSkillLatest 调用")
	assert.Equal(t, "app-skill-id-1", store.updates[0].ID, "回写 ID 应为 app-skill-id-1")
	assert.True(t, store.updates[0].LatestVersion.Valid, "latest_version 应为有效字符串（非 NULL）")
	assert.Equal(t, "1.2.0", store.updates[0].LatestVersion.String, "latest_version 应为最高版本 1.2.0")
}

// TestSkillUpdateChecker_Platform_SameVersion 验证 platform 来源时，
// 平台库最高版本与已安装版本相同，回写 latest_version 为 NULL（无更新）。
func TestSkillUpdateChecker_Platform_SameVersion(t *testing.T) {
	ctx := context.Background()

	store := newFakeCheckerAppSkillStore()
	// 安装版本与平台库最新版本相同，均为 1.0.0
	store.addSource("platform", "my-skill")
	store.addSkill("platform", "my-skill", "app-skill-id-1", "1.0.0")

	platform := &fakeCheckerPlatformStore{
		skills: []sqlc.PlatformSkill{
			{Name: "my-skill", Version: "1.0.0"},
		},
	}
	checker := NewSkillUpdateChecker(store, platform, nil)

	err := checker.Tick(ctx)
	require.NoError(t, err)

	// 应有一次调用，但 latest_version 为 NULL（版本相同不展示更新提示）
	require.Len(t, store.updates, 1)
	assert.False(t, store.updates[0].LatestVersion.Valid, "版本相同时 latest_version 应为 NULL")
}

// TestSkillUpdateChecker_ClawHub_HigherVersion 验证 clawhub 来源时，
// fake ListVersions 返回更高版本，回写 latest_version 为最高版本。
func TestSkillUpdateChecker_ClawHub_HigherVersion(t *testing.T) {
	ctx := context.Background()

	store := newFakeCheckerAppSkillStore()
	// 预置 clawhub 来源 skill，slug=my-slug，安装版本 1.0.0
	store.addSource("clawhub", "my-slug")
	store.addSkill("clawhub", "my-slug", "ck-skill-id-1", "1.0.0")

	clawhub := newFakeClawHubVersionLister()
	// ClawHub 返回两个版本，最高为 2.0.0
	clawhub.setVersions("my-slug", []SkillVersion{
		{Version: "1.0.0"},
		{Version: "2.0.0"},
	})

	checker := NewSkillUpdateChecker(store, &fakeCheckerPlatformStore{}, clawhub)

	err := checker.Tick(ctx)
	require.NoError(t, err)

	require.Len(t, store.updates, 1)
	assert.True(t, store.updates[0].LatestVersion.Valid, "latest_version 应为有效字符串")
	assert.Equal(t, "2.0.0", store.updates[0].LatestVersion.String, "应取最高版本 2.0.0")
}

// TestSkillUpdateChecker_SingleFailDoesNotAbort 验证单条 UpdateAppSkillLatest 失败时，
// 其他行仍然正常处理，不中断整个 Tick。
func TestSkillUpdateChecker_SingleFailDoesNotAbort(t *testing.T) {
	ctx := context.Background()

	store := newFakeCheckerAppSkillStore()
	// 预置三条同来源 app_skills 行
	store.addSource("platform", "skill-a")
	store.addSkill("platform", "skill-a", "id-1", "1.0.0")
	store.addSkill("platform", "skill-a", "id-2", "1.0.0")
	store.addSkill("platform", "skill-a", "id-3", "1.0.0")

	// 预置 id-2 回写失败
	store.setUpdateErr("id-2", errors.New("db write error"))

	platform := &fakeCheckerPlatformStore{
		skills: []sqlc.PlatformSkill{
			{Name: "skill-a", Version: "1.5.0"},
		},
	}
	checker := NewSkillUpdateChecker(store, platform, nil)

	err := checker.Tick(ctx)
	// Tick 本身不应返回错误（单条失败 warn 后继续）
	require.NoError(t, err, "单条失败不应导致 Tick 返回错误")

	// 三条行都应尝试回写（id-2 失败后继续处理 id-3）
	assert.Len(t, store.updates, 3, "三条行均应尝试 UpdateAppSkillLatest")

	// 构建实际回写的 id 集合（顺序由 map 迭代决定，不做顺序假设）
	updatedIDs := make(map[string]bool, len(store.updates))
	for _, u := range store.updates {
		updatedIDs[u.ID] = true
	}
	// id-1 和 id-3 的回写应成功触发；id-2 虽然失败但调用已经发出
	assert.True(t, updatedIDs["id-1"], "id-1 应被尝试回写")
	assert.True(t, updatedIDs["id-2"], "id-2 应被尝试回写（失败后继续）")
	assert.True(t, updatedIDs["id-3"], "id-3 应被尝试回写")
}

// TestSkillUpdateChecker_NilClawHub_SkipsClawhubSource 验证 clawhub 为 nil 时，
// 所有 clawhub 来源均被跳过，不调用 UpdateAppSkillLatest。
func TestSkillUpdateChecker_NilClawHub_SkipsClawhubSource(t *testing.T) {
	ctx := context.Background()

	store := newFakeCheckerAppSkillStore()
	// 预置 clawhub 来源 skill
	store.addSource("clawhub", "some-slug")
	store.addSkill("clawhub", "some-slug", "ck-id-1", "1.0.0")

	// clawhub 传 nil：来源未启用
	checker := NewSkillUpdateChecker(store, &fakeCheckerPlatformStore{}, nil)

	err := checker.Tick(ctx)
	require.NoError(t, err)

	// 来源跳过，不应调用任何 UpdateAppSkillLatest
	assert.Empty(t, store.updates, "clawhub 为 nil 时不应尝试回写")
}

// TestSkillUpdateChecker_PlatformListFail_SkipsPlatformSourcesContinuedOtherwise
// 验证 ListPlatformSkills 失败时跳过所有 platform 来源，clawhub 来源正常继续。
func TestSkillUpdateChecker_PlatformListFail_SkipsPlatformSourcesContinuedOtherwise(t *testing.T) {
	ctx := context.Background()

	store := newFakeCheckerAppSkillStore()
	// 预置 platform 和 clawhub 来源各一条
	store.addSource("platform", "p-skill")
	store.addSkill("platform", "p-skill", "p-id-1", "1.0.0")
	store.addSource("clawhub", "c-slug")
	store.addSkill("clawhub", "c-slug", "c-id-1", "1.0.0")

	// platform ListPlatformSkills 失败
	platform := &fakeCheckerPlatformStore{err: errors.New("db timeout")}

	clawhub := newFakeClawHubVersionLister()
	// clawhub 返回更高版本
	clawhub.setVersions("c-slug", []SkillVersion{{Version: "2.0.0"}})

	checker := NewSkillUpdateChecker(store, platform, clawhub)

	err := checker.Tick(ctx)
	require.NoError(t, err, "platform 查询失败不应导致 Tick 返回错误")

	// 仅 clawhub 的 c-id-1 应被回写；platform 的 p-id-1 应被跳过
	require.Len(t, store.updates, 1, "仅 clawhub 来源应有一次回写")
	assert.Equal(t, "c-id-1", store.updates[0].ID, "回写应为 clawhub 来源的 c-id-1")
	assert.Equal(t, null.StringFrom("2.0.0"), store.updates[0].LatestVersion,
		"clawhub latest_version 应为 2.0.0")
}

// TestSkillUpdateChecker_MultipleAppsShareSource 验证同一 (source, source_ref) 被多个 app 安装时，
// 每个 app 的 app_skills 行均被回写。
func TestSkillUpdateChecker_MultipleAppsShareSource(t *testing.T) {
	ctx := context.Background()

	store := newFakeCheckerAppSkillStore()
	// 两个 app 安装了同名 platform skill
	store.addSource("platform", "shared-skill")
	store.addSkill("platform", "shared-skill", "id-app1", "1.0.0")
	store.addSkill("platform", "shared-skill", "id-app2", "1.0.0")

	platform := &fakeCheckerPlatformStore{
		skills: []sqlc.PlatformSkill{
			{Name: "shared-skill", Version: "1.1.0"},
		},
	}
	checker := NewSkillUpdateChecker(store, platform, nil)

	err := checker.Tick(ctx)
	require.NoError(t, err)

	// 两条行均应被回写
	assert.Len(t, store.updates, 2, "共享来源下两条 app_skills 行均应回写")
	for _, u := range store.updates {
		assert.True(t, u.LatestVersion.Valid, "latest_version 应为有效字符串")
		assert.Equal(t, "1.1.0", u.LatestVersion.String)
	}
}

// TestPickHighestVersion 验证 pickHighestVersion 在多个版本中正确取最大值。
func TestPickHighestVersion(t *testing.T) {
	// 空列表返回空字符串
	assert.Equal(t, "", pickHighestVersion(nil), "空列表应返回空字符串")
	assert.Equal(t, "", pickHighestVersion([]SkillVersion{}), "空列表应返回空字符串")

	// 单元素直接返回
	assert.Equal(t, "1.0.0", pickHighestVersion([]SkillVersion{{Version: "1.0.0"}}),
		"单元素应返回该版本")

	// 多元素取最大（字符串比较，同位数 semver 正确）
	versions := []SkillVersion{
		{Version: "1.0.0"},
		{Version: "2.0.0"},
		{Version: "1.9.9"},
	}
	assert.Equal(t, "2.0.0", pickHighestVersion(versions), "应取最大版本 2.0.0")
}
