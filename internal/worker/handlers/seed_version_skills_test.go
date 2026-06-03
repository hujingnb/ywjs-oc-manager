package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/store/sqlc"
)

// fakeSeedStore 是 AppSkillSeedStore 的测试替身。
// appSkills 以 appID 为键、skill name 切片为值，模拟已有 app_skills 行。
// created 记录所有通过 CreateAppSkill 写入的条目，供断言使用。
type fakeSeedStore struct {
	// appSkills 存放各 app 已有的 skill name 集合（模拟已有行，get 时命中则不覆盖）。
	appSkills map[string][]string
	// created 按顺序记录 CreateAppSkill 被调用的参数，供断言新增行的字段值。
	created []sqlc.CreateAppSkillParams
	// getErr 非 nil 时 GetAppSkillByAppAndName 对所有查询返回该错误（用于错误路径测试）。
	getErr error
	// createErr 非 nil 时 CreateAppSkill 返回该错误（用于写入失败路径测试）。
	createErr error
}

// GetAppSkillByAppAndName 若 appSkills[appID] 包含目标 name，返回占位行；否则返回 sql.ErrNoRows。
func (f *fakeSeedStore) GetAppSkillByAppAndName(_ context.Context, arg sqlc.GetAppSkillByAppAndNameParams) (sqlc.AppSkill, error) {
	if f.getErr != nil {
		return sqlc.AppSkill{}, f.getErr
	}
	for _, n := range f.appSkills[arg.AppID] {
		if n == arg.Name {
			// 命中已有行，返回占位 AppSkill（字段值不重要，只要 err==nil 触发 skip）。
			return sqlc.AppSkill{Name: arg.Name, AppID: arg.AppID}, nil
		}
	}
	// 未找到，与真实 DB 行为一致。
	return sqlc.AppSkill{}, sql.ErrNoRows
}

// CreateAppSkill 记录调用参数；将 name 追加到 appSkills[appID] 模拟写库。
func (f *fakeSeedStore) CreateAppSkill(_ context.Context, arg sqlc.CreateAppSkillParams) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, arg)
	f.appSkills[arg.AppID] = append(f.appSkills[arg.AppID], arg.Name)
	return nil
}

// versionWithSkills 构造含给定 skill 列表的 AssistantVersion，用于测试辅助。
// 每个 skill 的快照字段填写可断言的占位值，供断言 CreateAppSkill 参数。
func versionWithSkills(names ...string) sqlc.AssistantVersion {
	type skillRow struct {
		Source    string `json:"source"`
		SourceRef string `json:"source_ref"`
		Name      string `json:"name"`
		Version   string `json:"version"`
		CachedPath string `json:"cached_path"`
		FileSize  int64  `json:"file_size"`
		FileSha256 string `json:"file_sha256"`
	}
	rows := make([]skillRow, 0, len(names))
	for _, n := range names {
		rows = append(rows, skillRow{
			Source:     "platform",
			SourceRef:  n,
			Name:       n,
			Version:    "1.0.0",
			CachedPath: "library/platform/" + n + "/1.0.0.tar",
			FileSize:   1024,
			FileSha256: "sha256-" + n,
		})
	}
	raw, _ := json.Marshal(rows)
	return sqlc.AssistantVersion{
		ID:         "ver-test-001",
		Name:       "v1",
		SkillsJson: raw,
	}
}

// TestSeedVersionSkills_Union 验证并集注入的核心语义：
// 实例已有 weather，版本含 weather + translate；
// 期望：translate 被注入，weather 不重复写入（已有不覆盖）。
func TestSeedVersionSkills_Union(t *testing.T) {
	// 实例已有 weather skill
	store := &fakeSeedStore{
		appSkills: map[string][]string{
			"app-1": {"weather"},
		},
	}
	// 版本含 weather + translate 两个 skill
	version := versionWithSkills("weather", "translate")

	err := seedVersionSkills(context.Background(), store, "app-1", version)
	require.NoError(t, err)

	// 仅 translate 被新写入（weather 已有，跳过）
	require.Len(t, store.created, 1, "只有 translate 应被注入，weather 已有应跳过")
	assert.Equal(t, "translate", store.created[0].Name, "新注入的 skill name 应为 translate")

	// 实例最终 skill 集合应为 weather + translate（并集，无重复）
	got := store.appSkills["app-1"]
	assert.ElementsMatch(t, []string{"weather", "translate"}, got, "实例 skill 应为版本 skill 并集，不重复")
}

// TestSeedVersionSkills_NoExisting 验证实例没有任何 skill 时，版本全部 skill 被注入。
func TestSeedVersionSkills_NoExisting(t *testing.T) {
	// 实例不存在任何 skill
	store := &fakeSeedStore{
		appSkills: map[string][]string{"app-2": {}},
	}
	// 版本含两个 skill
	version := versionWithSkills("search", "calc")

	err := seedVersionSkills(context.Background(), store, "app-2", version)
	require.NoError(t, err)

	// 两个 skill 都应被写入
	require.Len(t, store.created, 2, "实例无 skill 时，版本全部 skill 应被注入")
	names := []string{store.created[0].Name, store.created[1].Name}
	assert.ElementsMatch(t, []string{"search", "calc"}, names)
}

// TestSeedVersionSkills_AllExisting 验证实例已有版本中所有 skill 时，无新写入（幂等）。
func TestSeedVersionSkills_AllExisting(t *testing.T) {
	// 实例已有 weather + translate
	store := &fakeSeedStore{
		appSkills: map[string][]string{
			"app-3": {"weather", "translate"},
		},
	}
	// 版本也是 weather + translate
	version := versionWithSkills("weather", "translate")

	err := seedVersionSkills(context.Background(), store, "app-3", version)
	require.NoError(t, err)

	// 无新写入
	assert.Empty(t, store.created, "实例已有版本所有 skill 时，应无新写入（幂等）")
}

// TestSeedVersionSkills_EmptyVersion 验证版本 SkillsJson 为空时，函数静默返回 nil，无任何写入。
func TestSeedVersionSkills_EmptyVersion(t *testing.T) {
	// 实例和版本都无 skill
	store := &fakeSeedStore{
		appSkills: map[string][]string{"app-4": {}},
	}
	// 版本无 skill
	version := sqlc.AssistantVersion{
		ID:         "ver-empty",
		SkillsJson: []byte(`[]`),
	}

	err := seedVersionSkills(context.Background(), store, "app-4", version)
	require.NoError(t, err)

	// 无任何写入
	assert.Empty(t, store.created, "空版本 skill 时应无任何写入")
}

// TestSeedVersionSkills_GetQueryError 验证 GetAppSkillByAppAndName 返回非 ErrNoRows 错误时，
// 该条 skill 被跳过（warn），其他 skill 继续注入（最大努力语义）。
func TestSeedVersionSkills_GetQueryError(t *testing.T) {
	// GetAppSkillByAppAndName 直接返回错误（非 ErrNoRows），模拟 DB 故障。
	store := &fakeSeedStore{
		appSkills: map[string][]string{"app-5": {}},
		getErr:    sql.ErrConnDone, // 模拟连接故障
	}
	// 版本含两个 skill
	version := versionWithSkills("a", "b")

	// 函数不因单条查询错误而返回错误（最大努力）
	err := seedVersionSkills(context.Background(), store, "app-5", version)
	require.NoError(t, err)

	// 所有 skill 因查询失败而被跳过，无写入
	assert.Empty(t, store.created, "查询失败时 skill 应被跳过，无写入")
}

// TestSeedVersionSkills_CreateError 验证 CreateAppSkill 失败时，该条 skill 记录 warn，
// 其他 skill 继续注入（最大努力语义）。
func TestSeedVersionSkills_CreateError(t *testing.T) {
	// 实例无已有 skill；CreateAppSkill 总是返回错误
	store := &fakeSeedStore{
		appSkills: map[string][]string{"app-6": {}},
		createErr: sql.ErrConnDone,
	}
	// 版本含两个 skill
	version := versionWithSkills("x", "y")

	// 函数不因写入失败而返回错误（最大努力）
	err := seedVersionSkills(context.Background(), store, "app-6", version)
	require.NoError(t, err)

	// 写入均失败，created 为空，但函数仍正常返回
	assert.Empty(t, store.created, "写入失败时 created 应为空，但函数应正常返回")
}

// TestSeedVersionSkills_SnapshotFieldsPreserved 验证注入时快照字段（source/source_ref/version/
// cached_path/file_size/file_sha256）被完整写入 CreateAppSkillParams，不丢失。
func TestSeedVersionSkills_SnapshotFieldsPreserved(t *testing.T) {
	// 实例无已有 skill
	store := &fakeSeedStore{
		appSkills: map[string][]string{"app-7": {}},
	}
	// 版本含一个 skill，快照字段可精确断言
	version := versionWithSkills("weather")

	err := seedVersionSkills(context.Background(), store, "app-7", version)
	require.NoError(t, err)

	require.Len(t, store.created, 1, "应注入 1 条 skill")
	row := store.created[0]

	// 断言快照字段被完整写入
	assert.Equal(t, "app-7", row.AppID, "AppID 应与传入 appID 一致")
	assert.Equal(t, "weather", row.Name)
	assert.Equal(t, "platform", row.Source)
	assert.Equal(t, "weather", row.SourceRef)
	assert.Equal(t, "1.0.0", row.Version)
	assert.Equal(t, "library/platform/weather/1.0.0.tar", row.CachedTarPath)
	assert.Equal(t, int64(1024), row.FileSize)
	assert.Equal(t, "sha256-weather", row.FileSha256)

	// 断言 source_metadata 含 seeded_from_version
	var meta map[string]any
	require.NoError(t, json.Unmarshal(row.SourceMetadata, &meta))
	assert.Equal(t, "ver-test-001", meta["seeded_from_version"], "source_metadata 应含 seeded_from_version")

	// InstalledBy 应为空（系统行为，无操作用户）
	assert.False(t, row.InstalledBy.Valid, "InstalledBy 应为 null（系统种子注入无操作用户）")
}
