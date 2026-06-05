package service

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/ocops"
	"oc-manager/internal/store/sqlc"
)

// =========================================================
// Fake 实现
// =========================================================

// fakeAppSkillStore 是 AppSkillStore 的内存实现，供 AppSkillService 单测使用。
type fakeAppSkillStore struct {
	// rows 存储已安装的 skill，key 为 "appID|name"
	rows map[string]sqlc.AppSkill
}

func newFakeAppSkillStore() *fakeAppSkillStore {
	return &fakeAppSkillStore{rows: map[string]sqlc.AppSkill{}}
}

// key 生成唯一存储键。
func (f *fakeAppSkillStore) key(appID, name string) string { return appID + "|" + name }

// get 取出某 app 下某 skill 的行（供测试直接断言）。
func (f *fakeAppSkillStore) get(appID, name string) (sqlc.AppSkill, bool) {
	r, ok := f.rows[f.key(appID, name)]
	return r, ok
}

// put 预置一条 app_skills 行（供测试构造重复场景）。
func (f *fakeAppSkillStore) put(appID, name, source string) {
	// CachedTarPath 用确定性路径，与 fakeLibraryBlob.PutLibrarySkill 的命名规则一致，
	// 便于 Reinstall（读缓存归档）测试预置对应字节。
	f.rows[f.key(appID, name)] = sqlc.AppSkill{
		AppID: appID, Name: name, Source: source, SourceRef: name, Version: "1.0",
		CachedTarPath: "library/" + source + "/" + name + "/1.0.tar",
	}
}

// putWithLatest 预置一条带 latest_version 的 app_skills 行（供测试更新提示场景）。
func (f *fakeAppSkillStore) putWithLatest(appID, name, source, version, latestVersion string) {
	f.rows[f.key(appID, name)] = sqlc.AppSkill{
		AppID:         appID,
		Name:          name,
		Source:        source,
		SourceRef:     name,
		Version:       version,
		LatestVersion: null.StringFrom(latestVersion),
	}
}

func (f *fakeAppSkillStore) ListAppSkillsByApp(_ context.Context, appID string) ([]sqlc.AppSkill, error) {
	out := []sqlc.AppSkill{}
	for _, r := range f.rows {
		if r.AppID == appID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeAppSkillStore) GetAppSkillByAppAndName(_ context.Context, arg sqlc.GetAppSkillByAppAndNameParams) (sqlc.AppSkill, error) {
	r, ok := f.rows[f.key(arg.AppID, arg.Name)]
	if !ok {
		return sqlc.AppSkill{}, sql.ErrNoRows
	}
	return r, nil
}

func (f *fakeAppSkillStore) CreateAppSkill(_ context.Context, arg sqlc.CreateAppSkillParams) error {
	f.rows[f.key(arg.AppID, arg.Name)] = sqlc.AppSkill{
		ID:             arg.ID,
		AppID:          arg.AppID,
		Name:           arg.Name,
		Source:         arg.Source,
		SourceRef:      arg.SourceRef,
		Version:        arg.Version,
		CachedTarPath:  arg.CachedTarPath,
		SourceMetadata: arg.SourceMetadata,
		FileSize:       arg.FileSize,
		FileSha256:     arg.FileSha256,
		InstalledBy:    arg.InstalledBy,
	}
	return nil
}

func (f *fakeAppSkillStore) DeleteAppSkillByAppAndName(_ context.Context, arg sqlc.DeleteAppSkillByAppAndNameParams) error {
	delete(f.rows, f.key(arg.AppID, arg.Name))
	return nil
}

func (f *fakeAppSkillStore) UpdateAppSkillVersion(_ context.Context, arg sqlc.UpdateAppSkillVersionParams) error {
	k := f.key(arg.AppID, arg.Name)
	if r, ok := f.rows[k]; ok {
		r.Version = arg.Version
		r.CachedTarPath = arg.CachedTarPath
		r.FileSize = arg.FileSize
		r.FileSha256 = arg.FileSha256
		f.rows[k] = r
	}
	return nil
}

// fakeAppLocator 是 AppLocator 的内存实现，记录各 appID 对应的位置信息。
type fakeAppLocator struct {
	// locations 存储各 app 的定位信息，key 为 appID
	locations map[string]AppSkillLocation
}

func newFakeAppLocator() *fakeAppLocator {
	return &fakeAppLocator{locations: map[string]AppSkillLocation{}}
}

// setApp 预置一个 app 的位置信息。
func (f *fakeAppLocator) setApp(appID, orgID, ownerUserID string, supported bool) {
	f.locations[appID] = AppSkillLocation{
		OrgID:       orgID,
		OwnerUserID: ownerUserID,
		Endpoint:    ocops.Endpoint{BaseURL: "http://fake-ocops"},
		Supported:   supported,
	}
}

// setVersion 更新某 app 当前绑定的助手版本 ID（供删除保护测试使用）。
func (f *fakeAppLocator) setVersion(appID, versionID string) {
	loc := f.locations[appID]
	loc.VersionID = versionID
	f.locations[appID] = loc
}

func (f *fakeAppLocator) LocateApp(_ context.Context, appID string) (AppSkillLocation, error) {
	loc, ok := f.locations[appID]
	if !ok {
		return AppSkillLocation{}, ErrNotFound
	}
	return loc, nil
}

// fakePlatformInstaller 是 PlatformInstaller 的内存实现。
type fakePlatformInstaller struct {
	// archives 存储已预置的归档，key 为 "name|version"
	archives map[string][]byte
}

func newFakePlatformInstaller() *fakePlatformInstaller {
	return &fakePlatformInstaller{archives: map[string][]byte{}}
}

// put 预置一个平台库 skill 归档。
func (f *fakePlatformInstaller) put(name, version string, data []byte) {
	f.archives[name+"|"+version] = data
}

func (f *fakePlatformInstaller) GetForInstall(_ context.Context, name, version string) ([]byte, string, error) {
	data, ok := f.archives[name+"|"+version]
	if !ok {
		return nil, "", ErrPlatformSkillNotFound
	}
	return data, "fakeshaxxx", nil
}

// fakeClawHubDownloader 是 ClawHubDownloader 的内存实现。
type fakeClawHubDownloader struct {
	archives map[string][]byte
	err      error
}

func (f *fakeClawHubDownloader) Download(_ context.Context, slug, version string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	data, ok := f.archives[slug+"|"+version]
	if !ok {
		return nil, errors.New("clawhub: not found")
	}
	return data, nil
}

// fakeOcOpsSkillClient 是 OcOpsSkillClient 的内存实现，记录调用状态。
type fakeOcOpsSkillClient struct {
	// installed 记录已热装的 skill name
	installed map[string]bool
	// deleted 记录已热删的 skill name
	deleted map[string]bool
	// reloaded 记录是否调用了 reload
	reloaded bool
	// installErr 预置 SkillInstall 返回的错误（测试热装失败场景）
	installErr error
	// reloadErr 预置 SkillReload 返回的错误
	reloadErr error
	// listSkills 预置 SkillList 的返回值（测试对账场景）
	listSkills []ocops.SkillInfo
	// listErr 预置 SkillList 返回的错误（测试容器不可达场景）
	listErr error
}

func newFakeOcOpsSkillClient() *fakeOcOpsSkillClient {
	return &fakeOcOpsSkillClient{
		installed: map[string]bool{},
		deleted:   map[string]bool{},
	}
}

func (f *fakeOcOpsSkillClient) SkillInstall(_ context.Context, _ ocops.Endpoint, name string, _ []byte) error {
	if f.installErr != nil {
		return f.installErr
	}
	f.installed[name] = true
	return nil
}

func (f *fakeOcOpsSkillClient) SkillDelete(_ context.Context, _ ocops.Endpoint, name string) error {
	f.deleted[name] = true
	return nil
}

func (f *fakeOcOpsSkillClient) SkillReload(_ context.Context, _ ocops.Endpoint) error {
	if f.reloadErr != nil {
		return f.reloadErr
	}
	f.reloaded = true
	return nil
}

func (f *fakeOcOpsSkillClient) SkillList(_ context.Context, _ ocops.Endpoint) ([]ocops.SkillInfo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listSkills, nil
}

// fakeAssistantVersionLoader 是 AssistantVersionLoader 的内存实现。
type fakeAssistantVersionLoader struct {
	// skillNames 存储各 versionID 对应的 skill name 集，key 为 versionID
	skillNames map[string][]string
}

func newFakeAssistantVersionLoader() *fakeAssistantVersionLoader {
	return &fakeAssistantVersionLoader{skillNames: map[string][]string{}}
}

// setSkills 预置某版本的 skill names（供卸载保护测试使用）。
func (f *fakeAssistantVersionLoader) setSkills(versionID string, names []string) {
	f.skillNames[versionID] = names
}

func (f *fakeAssistantVersionLoader) SkillNames(_ context.Context, versionID string) ([]string, error) {
	names, ok := f.skillNames[versionID]
	if !ok {
		return nil, nil
	}
	return names, nil
}

// fakeAuditRecorder 是 AuditRecorder 的内存实现，记录审计事件。
type fakeAuditRecorder struct {
	events []AuditEvent
}

func (f *fakeAuditRecorder) Record(_ context.Context, event AuditEvent) (AuditResult, error) {
	f.events = append(f.events, event)
	return AuditResult{}, nil
}

// =========================================================
// 测试依赖容器
// =========================================================

// appSkillTestDeps 封装 AppSkillService 单测所需的全部依赖与 fake 对象。
type appSkillTestDeps struct {
	appSkills *fakeAppSkillStore
	apps      *fakeAppLocator
	platform  *fakePlatformInstaller
	clawhub   *fakeClawHubDownloader
	blobs     *fakeLibraryBlob
	ocops     *fakeOcOpsSkillClient
	versions  *fakeAssistantVersionLoader
	audit     *fakeAuditRecorder
}

// newAppSkillTestDeps 初始化测试依赖并预置默认的 app-1 位置（owner=u-owner，org=org-1，支持 oc-ops）。
func newAppSkillTestDeps(_ *testing.T) *appSkillTestDeps {
	apps := newFakeAppLocator()
	// 预置默认 app-1：归属 org-1，所有者 u-owner，oc-ops 支持
	apps.setApp("app-1", "org-1", "u-owner", true)
	return &appSkillTestDeps{
		appSkills: newFakeAppSkillStore(),
		apps:      apps,
		platform:  newFakePlatformInstaller(),
		clawhub:   &fakeClawHubDownloader{archives: map[string][]byte{}},
		blobs:     &fakeLibraryBlob{},
		ocops:     newFakeOcOpsSkillClient(),
		versions:  newFakeAssistantVersionLoader(),
		audit:     &fakeAuditRecorder{},
	}
}

// service 构造 AppSkillService 注入所有 fake 依赖。
func (d *appSkillTestDeps) service() *AppSkillService {
	return NewAppSkillService(AppSkillServiceDeps{
		Store:    d.appSkills,
		Apps:     d.apps,
		Versions: d.versions,
		Platform: d.platform,
		ClawHub:  d.clawhub,
		Blobs:    d.blobs,
		OcOps:    d.ocops,
		Audit:    d.audit,
	})
}

// ownerPrincipal 返回 app-1 的 owner principal（org-1 的普通成员，同时是 app 拥有者）。
func (d *appSkillTestDeps) ownerPrincipal() auth.Principal {
	return auth.Principal{UserID: "u-owner", Role: domain.UserRoleOrgMember, OrgID: "org-1"}
}

// otherMemberPrincipal 返回同 org 但非 owner 的普通成员（无权管理 app-1 的 skill）。
func (d *appSkillTestDeps) otherMemberPrincipal() auth.Principal {
	return auth.Principal{UserID: "u-other", Role: domain.UserRoleOrgMember, OrgID: "org-1"}
}

// =========================================================
// Install 测试
// =========================================================

// TestAppSkillService_Install_Platform 从平台来源安装 skill：
// 期望落 app_skills + 缓存归档 + 调用 oc-ops 热装与 reload，状态返回 active。
func TestAppSkillService_Install_Platform(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// 预置平台库 skill weather 1.0 的归档
	deps.platform.put("weather", "1.0", []byte("PK\x03\x04tar"))
	svc := deps.service()

	res, err := svc.Install(context.Background(), deps.ownerPrincipal(), "app-1", InstallSkillInput{
		Source:    "platform",
		SourceRef: "weather",
		Name:      "weather",
		Version:   "1.0",
	})
	require.NoError(t, err)
	// 返回字段校验：name 正确、状态 active（热装+reload 成功）
	assert.Equal(t, "weather", res.Name)
	assert.Equal(t, "active", res.Status)
	// oc-ops 热装与 reload 均被调用
	assert.True(t, deps.ocops.installed["weather"])
	assert.True(t, deps.ocops.reloaded)
	// app_skills 表已落库
	row, ok := deps.appSkills.get("app-1", "weather")
	require.True(t, ok)
	assert.Equal(t, "platform", row.Source)
}

// TestAppSkillService_Install_Denied 非 owner/管理员安装被拒，返回 ErrAppSkillDenied。
func TestAppSkillService_Install_Denied(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	deps.platform.put("weather", "1.0", []byte("data"))
	svc := deps.service()

	// 使用非 owner 的成员（无权）
	_, err := svc.Install(context.Background(), deps.otherMemberPrincipal(), "app-1", InstallSkillInput{
		Source:    "platform",
		SourceRef: "weather",
		Name:      "weather",
		Version:   "1.0",
	})
	require.ErrorIs(t, err, ErrAppSkillDenied)
}

// TestAppSkillService_Install_Duplicate 同名 skill 已安装，再次安装返回 ErrAppSkillNameConflict。
func TestAppSkillService_Install_Duplicate(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	deps.platform.put("weather", "1.0", []byte("data"))
	// 预置同名已安装的 skill
	deps.appSkills.put("app-1", "weather", "platform")
	svc := deps.service()

	_, err := svc.Install(context.Background(), deps.ownerPrincipal(), "app-1", InstallSkillInput{
		Source:    "platform",
		SourceRef: "weather",
		Name:      "weather",
		Version:   "1.0",
	})
	require.ErrorIs(t, err, ErrAppSkillNameConflict)
}

// TestAppSkillService_Install_OcOpsFail_Pending oc-ops 热装失败时：
// app_skills 已落库（不回滚），状态为 pending（可重试），不返回错误。
func TestAppSkillService_Install_OcOpsFail_Pending(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// 预置热装错误
	deps.ocops.installErr = errors.New("pod not ready")
	deps.platform.put("weather", "1.0", []byte("x"))
	svc := deps.service()

	res, err := svc.Install(context.Background(), deps.ownerPrincipal(), "app-1", InstallSkillInput{
		Source:    "platform",
		SourceRef: "weather",
		Name:      "weather",
		Version:   "1.0",
	})
	require.NoError(t, err)
	// 热装失败 → 状态 pending
	assert.Equal(t, "pending", res.Status)
	// app_skills 仍然落库（不因 oc-ops 失败回滚）
	_, ok := deps.appSkills.get("app-1", "weather")
	assert.True(t, ok)
}

// =========================================================
// Reinstall（pending 重试）单测
// =========================================================

// TestAppSkillService_Reinstall_Success 已记录 skill 重试：从缓存归档读取并 oc-ops 热装+reload 成功 → status=active。
func TestAppSkillService_Reinstall_Success(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// 预置 app_skills 记录（首次安装已落库，CachedTarPath 指向缓存归档）+ 缓存归档字节。
	deps.appSkills.put("app-1", "weather", "platform")
	deps.blobs.stored = map[string][]byte{"library/platform/weather/1.0.tar": []byte("cached-archive")}
	svc := deps.service()

	res, err := svc.Reinstall(context.Background(), deps.ownerPrincipal(), "app-1", "weather")
	require.NoError(t, err)
	// 热装+reload 成功 → active
	assert.Equal(t, "active", res.Status)
	assert.Equal(t, "weather", res.Name)
}

// TestAppSkillService_Reinstall_UsesCacheNotUpstream Reinstall 必须读缓存归档而非重新下载上游：
// 即使 platform/clawhub 上游均无该 skill（模拟上游下架/抖动），只要缓存在即可恢复 → active。
func TestAppSkillService_Reinstall_UsesCacheNotUpstream(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// clawhub 来源的 pending skill：上游不预置任何归档（fetchArchive 会失败），仅预置缓存字节。
	deps.appSkills.put("app-1", "gone-upstream", "clawhub")
	deps.blobs.stored = map[string][]byte{"library/clawhub/gone-upstream/1.0.tar": []byte("cached-only")}
	svc := deps.service()

	res, err := svc.Reinstall(context.Background(), deps.ownerPrincipal(), "app-1", "gone-upstream")
	// 不重新下载上游，纯靠缓存恢复 → 不报错且 active
	require.NoError(t, err)
	assert.Equal(t, "active", res.Status)
}

// TestAppSkillService_Reinstall_OcOpsFail_Pending 重试时 oc-ops 仍失败 → 保持 pending，不报错（可继续重试）。
func TestAppSkillService_Reinstall_OcOpsFail_Pending(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	deps.appSkills.put("app-1", "weather", "platform")
	deps.blobs.stored = map[string][]byte{"library/platform/weather/1.0.tar": []byte("cached-archive")}
	// 预置热装错误，模拟容器仍未就绪
	deps.ocops.installErr = errors.New("pod still not ready")
	svc := deps.service()

	res, err := svc.Reinstall(context.Background(), deps.ownerPrincipal(), "app-1", "weather")
	require.NoError(t, err)
	// oc-ops 仍失败 → 保持 pending
	assert.Equal(t, "pending", res.Status)
}

// TestAppSkillService_Reinstall_NotFound 对不存在的 app_skill 重试 → ErrAppSkillNotFound。
func TestAppSkillService_Reinstall_NotFound(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	svc := deps.service()

	_, err := svc.Reinstall(context.Background(), deps.ownerPrincipal(), "app-1", "nonexistent")
	require.ErrorIs(t, err, ErrAppSkillNotFound)
}

// =========================================================
// validateArchiveSafety 单测
// =========================================================

// TestValidateArchiveSafety_OK 正常 tar 归档（小文件数+小总字节）通过检验。
func TestValidateArchiveSafety_OK(t *testing.T) {
	// 构造一个最小合法 tar（空归档：只有 EOF 块）
	// tar 格式：两个 512 字节全零块代表 EOF
	emptyTar := make([]byte, 1024)
	err := validateArchiveSafety(emptyTar, "tar")
	require.NoError(t, err)
}

// TestValidateArchiveSafety_TooManyFiles tar 归档解压后文件数超过上限，返回错误。
func TestValidateArchiveSafety_TooManyFiles(t *testing.T) {
	// 构造包含超过 maxArchiveFiles 个文件的 tar
	data := buildTarWithNFiles(t, maxArchiveFiles+1, 1)
	err := validateArchiveSafety(data, "tar")
	require.Error(t, err)
}

// TestValidateArchiveSafety_TooLarge tar 归档解压后总字节超过上限，返回错误。
func TestValidateArchiveSafety_TooLarge(t *testing.T) {
	// 构造一个文件内容超过 maxArchiveBytes 的 tar
	data := buildTarWithNFiles(t, 1, maxArchiveBytes+1)
	err := validateArchiveSafety(data, "tar")
	require.Error(t, err)
}

// =========================================================
// 测试辅助函数
// =========================================================

// buildTarWithNFiles 构造一个包含 n 个文件的 tar 归档，每个文件 header 声明大小为 fileSize 字节。
// 实际 body 写入 min(fileSize, 1) 字节（避免分配超大内存），
// validateTarSafety 读的是 header.Size，不实际读取文件 body。
// 注意：tar.Writer.Close 会校验 body 写入量与 header.Size 是否一致，
// 因此对 TooLarge（fileSize 远超内存）场景用 buildTarHeaderOnly 手动构造 raw header。
func buildTarWithNFiles(t *testing.T, n int, fileSize int64) []byte {
	t.Helper()
	// 对超大 fileSize（TooLarge 场景），使用 raw header 构造避免分配大内存
	if fileSize > 1024 {
		return buildRawTarHeaders(t, n, fileSize)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i := 0; i < n; i++ {
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     fmt.Sprintf("file%d.txt", i),
			Size:     fileSize,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("写 tar header 失败: %v", err)
		}
		// 写入实际 body（fileSize <= 1024，安全）
		if fileSize > 0 {
			if _, err := tw.Write(make([]byte, fileSize)); err != nil {
				t.Fatalf("写 tar body 失败: %v", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("关闭 tar writer 失败: %v", err)
	}
	return buf.Bytes()
}

// =========================================================
// Uninstall 测试
// =========================================================

// TestAppSkillService_Uninstall_OK 卸载已安装的 skill：
// 期望 app_skills 行被删除 + oc-ops 热删 + reload 均被调用。
func TestAppSkillService_Uninstall_OK(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// 预置 app-1 已安装 mytool（非当前版本必需 skill）
	deps.appSkills.put("app-1", "mytool", "platform")
	// app-1 绑定版本 v1，v1 的 skills_json 不含 mytool（不受保护）
	deps.versions.setSkills("v1", []string{"weather"})
	deps.apps.setVersion("app-1", "v1")
	svc := deps.service()

	// 执行卸载
	err := svc.Uninstall(context.Background(), deps.ownerPrincipal(), "app-1", "mytool")
	require.NoError(t, err)
	// app_skills 行已删除
	_, ok := deps.appSkills.get("app-1", "mytool")
	assert.False(t, ok, "app_skills 行应已删除")
	// oc-ops 热删与 reload 均被调用
	assert.True(t, deps.ocops.deleted["mytool"], "SkillDelete 应被调用")
	assert.True(t, deps.ocops.reloaded, "SkillReload 应被调用")
}

// TestAppSkillService_Uninstall_Protected 卸载当前版本必需的 skill 被拒：
// 版本 v1 的 skills_json 含 weather，卸载 weather → ErrAppSkillProtected。
func TestAppSkillService_Uninstall_Protected(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// 当前版本含 weather（受保护）
	deps.versions.setSkills("v1", []string{"weather"})
	deps.apps.setVersion("app-1", "v1")
	// 实例已装 weather
	deps.appSkills.put("app-1", "weather", "platform")
	svc := deps.service()

	err := svc.Uninstall(context.Background(), deps.ownerPrincipal(), "app-1", "weather")
	require.ErrorIs(t, err, ErrAppSkillProtected)
	// app_skills 行不应被删除
	_, ok := deps.appSkills.get("app-1", "weather")
	assert.True(t, ok, "受保护的 skill 不应被删除")
}

// TestAppSkillService_Uninstall_NotFound 卸载不存在的 skill 返回 ErrAppSkillNotFound。
func TestAppSkillService_Uninstall_NotFound(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// app_skills 中没有 unknown-skill
	svc := deps.service()

	err := svc.Uninstall(context.Background(), deps.ownerPrincipal(), "app-1", "unknown-skill")
	require.ErrorIs(t, err, ErrAppSkillNotFound)
}

// =========================================================
// Update 测试
// =========================================================

// TestAppSkillService_Update_OK 更新已安装的 skill 到新版本：
// 期望 UpdateAppSkillVersion 更新版本记录 + oc-ops 热替换（SkillInstall 覆盖）+ reload。
func TestAppSkillService_Update_OK(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// 预置 app-1 已安装 weather 1.0（平台来源）
	deps.appSkills.put("app-1", "weather", "platform")
	// 预置平台库 weather 2.0 的新归档
	deps.platform.put("weather", "2.0", []byte("PK\x03\x04newtardata"))
	svc := deps.service()

	res, err := svc.Update(context.Background(), deps.ownerPrincipal(), "app-1", "weather", "2.0")
	require.NoError(t, err)
	// 返回字段：name 正确、版本已更新、状态 active
	assert.Equal(t, "weather", res.Name)
	assert.Equal(t, "2.0", res.Version)
	assert.Equal(t, "active", res.Status)
	// oc-ops 热替换（SkillInstall 覆盖）与 reload 均被调用
	assert.True(t, deps.ocops.installed["weather"], "SkillInstall 应被调用（覆盖安装）")
	assert.True(t, deps.ocops.reloaded, "SkillReload 应被调用")
	// app_skills 版本已更新
	row, ok := deps.appSkills.get("app-1", "weather")
	require.True(t, ok)
	assert.Equal(t, "2.0", row.Version)
}

// TestAppSkillService_Update_NotFound 更新不存在的 skill 返回 ErrAppSkillNotFound。
func TestAppSkillService_Update_NotFound(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// app_skills 中没有 nonexistent
	svc := deps.service()

	_, err := svc.Update(context.Background(), deps.ownerPrincipal(), "app-1", "nonexistent", "2.0")
	require.ErrorIs(t, err, ErrAppSkillNotFound)
}

// =========================================================
// List 实时对账测试
// =========================================================

// findSkill 在 List 结果中按 name 查找，供断言用。
func findSkill(results []AppSkillResult, name string) (AppSkillResult, bool) {
	for _, r := range results {
		if r.Name == name {
			return r, true
		}
	}
	return AppSkillResult{}, false
}

// TestAppSkillService_List_Active app_skills 有（期望）且容器实际也有 → status=active。
func TestAppSkillService_List_Active(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// 期望：app-1 已在 app_skills 中注册 weather（manager 管理）
	deps.appSkills.put("app-1", "weather", "platform")
	// 容器实际：容器正在运行 weather
	deps.ocops.listSkills = []ocops.SkillInfo{
		{Name: "weather", Managed: true, Builtin: false},
	}
	svc := deps.service()

	results, err := svc.List(context.Background(), deps.ownerPrincipal(), "app-1")
	require.NoError(t, err)
	// 找到 weather 且状态为 active（期望×实际 = 已激活）
	r, ok := findSkill(results, "weather")
	require.True(t, ok, "应找到 weather")
	assert.Equal(t, "active", r.Status)
}

// TestAppSkillService_List_Pending app_skills 有（期望）但容器实际没有，容器可达 → status=pending。
func TestAppSkillService_List_Pending(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// 期望：app-1 已注册 weather，但容器没有执行安装（可能热装失败）
	deps.appSkills.put("app-1", "weather", "platform")
	// 容器实际：空列表（容器可达但 weather 不在其中）
	deps.ocops.listSkills = []ocops.SkillInfo{}
	svc := deps.service()

	results, err := svc.List(context.Background(), deps.ownerPrincipal(), "app-1")
	require.NoError(t, err)
	// weather 应为 pending（期望存在但容器无，等待热装完成）
	r, ok := findSkill(results, "weather")
	require.True(t, ok, "应找到 weather")
	assert.Equal(t, "pending", r.Status)
}

// TestAppSkillService_List_Builtin 容器有但 app_skills 无，且 Builtin=true → status=builtin。
func TestAppSkillService_List_Builtin(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// app_skills 为空（manager 没有管理这个 skill）
	// 容器实际：内置 skill（随镜像预装）
	deps.ocops.listSkills = []ocops.SkillInfo{
		{Name: "hermes-core", Managed: false, Builtin: true},
	}
	svc := deps.service()

	results, err := svc.List(context.Background(), deps.ownerPrincipal(), "app-1")
	require.NoError(t, err)
	// hermes-core 为 builtin（容器自带，manager 未管理，Builtin=true）
	r, ok := findSkill(results, "hermes-core")
	require.True(t, ok, "应找到 hermes-core")
	assert.Equal(t, "builtin", r.Status)
}

// TestAppSkillService_List_SelfCreated 容器有但 app_skills 无，且 Builtin=false → status=self_created。
func TestAppSkillService_List_SelfCreated(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// app_skills 为空（manager 没有管理这个 skill）
	// 容器实际：用户自己创建的 skill（非 builtin，非 manager 管理）
	deps.ocops.listSkills = []ocops.SkillInfo{
		{Name: "my-custom-skill", Managed: false, Builtin: false},
	}
	svc := deps.service()

	results, err := svc.List(context.Background(), deps.ownerPrincipal(), "app-1")
	require.NoError(t, err)
	// my-custom-skill 为 self_created（容器有，app_skills 无，非 builtin，非 managed）
	r, ok := findSkill(results, "my-custom-skill")
	require.True(t, ok, "应找到 my-custom-skill")
	assert.Equal(t, "self_created", r.Status)
}

// TestAppSkillService_List_SystemManagedAsBuiltin 容器有但 app_skills 无、Builtin=false 但 Managed=true
// （如 oc-kb：manager 运行时强制 render 的系统 skill，有 .oc-managed 标记却未在市场安装）→ 归 builtin、不可卸载，
// 与用户在容器内手动自建的 self_created 区分。
func TestAppSkillService_List_SystemManagedAsBuiltin(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// app_skills 为空；容器实际有一个含 .oc-managed 标记（Managed=true）但非镜像内置（Builtin=false）的系统 skill。
	deps.ocops.listSkills = []ocops.SkillInfo{
		{Name: "oc-kb", Managed: true, Builtin: false},
	}
	svc := deps.service()

	results, err := svc.List(context.Background(), deps.ownerPrincipal(), "app-1")
	require.NoError(t, err)
	// oc-kb：Managed=true 但不在 app_skills → 视为内置（builtin），不可卸载，区别于 self_created。
	r, ok := findSkill(results, "oc-kb")
	require.True(t, ok, "应找到 oc-kb")
	assert.Equal(t, "builtin", r.Status)
}

// TestAppSkillService_List_UnknownOnUnreachable 容器不可达（SkillList 报错）时：
// app_skills 中的 skill 状态应降级为 unknown（fallback，不能确认安装状态）。
func TestAppSkillService_List_UnknownOnUnreachable(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// 期望：app-1 已注册 weather
	deps.appSkills.put("app-1", "weather", "platform")
	// 容器不可达：SkillList 返回错误
	deps.ocops.listErr = errors.New("connection refused")
	svc := deps.service()

	results, err := svc.List(context.Background(), deps.ownerPrincipal(), "app-1")
	require.NoError(t, err)
	// weather 应为 unknown（容器不可达，无法确认安装状态）
	r, ok := findSkill(results, "weather")
	require.True(t, ok, "应找到 weather")
	assert.Equal(t, "unknown", r.Status)
}

// TestAppSkillService_List_RuntimeUnsupported 运行的 hermes 版本过旧（oc-ops 无 /oc/skills 路由，
// SkillList 返回 ocops.ErrNotFound/404）时，List 直接返回 ErrAppSkillRuntimeUnsupported，
// 而非降级为 unknown——用于前端提示用户更新实例版本。
func TestAppSkillService_List_RuntimeUnsupported(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	deps.appSkills.put("app-1", "weather", "platform")
	// 老版本 hermes：GET /oc/skills 路由不存在 → 404 → ocops.ErrNotFound
	deps.ocops.listErr = ocops.ErrNotFound
	svc := deps.service()

	_, err := svc.List(context.Background(), deps.ownerPrincipal(), "app-1")
	// 必须区别于「不可达降级 unknown」：直接报 RuntimeUnsupported
	require.ErrorIs(t, err, ErrAppSkillRuntimeUnsupported)
}

// TestAppSkillService_List_Protected app_skills 中且属于当前版本必需的 skill，Protected=true。
func TestAppSkillService_List_Protected(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// 预置 app-1 已注册 weather，且当前版本 v1 的 skills_json 含 weather（受保护）
	deps.appSkills.put("app-1", "weather", "platform")
	deps.versions.setSkills("v1", []string{"weather"})
	deps.apps.setVersion("app-1", "v1")
	// 容器实际也有 weather（active）
	deps.ocops.listSkills = []ocops.SkillInfo{
		{Name: "weather", Managed: true, Builtin: false},
	}
	svc := deps.service()

	results, err := svc.List(context.Background(), deps.ownerPrincipal(), "app-1")
	require.NoError(t, err)
	// weather 应为 active 且 Protected=true
	r, ok := findSkill(results, "weather")
	require.True(t, ok, "应找到 weather")
	assert.Equal(t, "active", r.Status)
	assert.True(t, r.Protected, "当前版本必需的 skill 应标记为 Protected")
}

// TestAppSkillService_List_LatestVersion app_skills 中有 latest_version 时，结果应填入 Latest 字段。
func TestAppSkillService_List_LatestVersion(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// 预置 app-1 已注册 weather 1.0，定时任务已检测到最新版 2.0
	deps.appSkills.putWithLatest("app-1", "weather", "platform", "1.0", "2.0")
	// 容器实际也有 weather（active）
	deps.ocops.listSkills = []ocops.SkillInfo{
		{Name: "weather", Managed: true, Builtin: false},
	}
	svc := deps.service()

	results, err := svc.List(context.Background(), deps.ownerPrincipal(), "app-1")
	require.NoError(t, err)
	// weather 应携带 latest_version=2.0，提示前端可更新
	r, ok := findSkill(results, "weather")
	require.True(t, ok, "应找到 weather")
	assert.Equal(t, "1.0", r.Version)
	assert.Equal(t, "2.0", r.Latest)
}

// =========================================================
// Install ClawHub 缓存命中 + 上游失败测试
// =========================================================

// TestAppSkillService_Install_ClawHub_CacheHit clawhub 安装命中缓存：不回源下载即落库 active。
// 预置 clawhub 下载器为「一调用即报错」，命中缓存时不应触发它 → 证明走的是缓存。
func TestAppSkillService_Install_ClawHub_CacheHit(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// 缓存里已有该 skill 归档（首次安装时写入的效果）。
	// 故意用不合法 zip 字节：命中缓存跳过解压校验，验证走的是缓存而非校验。
	deps.blobs.stored = map[string][]byte{"library/clawhub/skill-vetter/1.0.zip": []byte("CACHED-ZIP")}
	// 上游下载器预置错误：命中缓存时不应被调用。
	deps.clawhub.err = errors.New("上游不该被调用")
	svc := deps.service()

	res, err := svc.Install(context.Background(), deps.ownerPrincipal(), "app-1", InstallSkillInput{
		Source: "clawhub", SourceRef: "skill-vetter", Name: "skill-vetter", Version: "1.0",
	})
	require.NoError(t, err)
	// 命中缓存即可落库并热装成功。
	assert.Equal(t, "active", res.Status)
	row, ok := deps.appSkills.get("app-1", "skill-vetter")
	require.True(t, ok)
	// CachedTarPath 指向缓存键。
	assert.Equal(t, "library/clawhub/skill-vetter/1.0.zip", row.CachedTarPath)
}

// TestAppSkillService_Install_ClawHub_UpstreamFail clawhub 安装未命中且上游下载失败：
// 返回 ErrSkillMarketUpstreamUnavailable（供 handler 映射 502），不落库。
func TestAppSkillService_Install_ClawHub_UpstreamFail(t *testing.T) {
	deps := newAppSkillTestDeps(t)
	// 缓存为空（未命中），上游下载器报错。
	deps.clawhub.err = errors.New("clawhub 下载返回非 2xx: 502")
	svc := deps.service()

	_, err := svc.Install(context.Background(), deps.ownerPrincipal(), "app-1", InstallSkillInput{
		Source: "clawhub", SourceRef: "self-improving", Name: "self-improving", Version: "1.2.16",
	})
	require.ErrorIs(t, err, ErrSkillMarketUpstreamUnavailable)
	// 上游失败不落库。
	_, ok := deps.appSkills.get("app-1", "self-improving")
	assert.False(t, ok)
}

// buildRawTarHeaders 手动构造 tar 归档 raw 字节：
// 仅写 n 个 header（每个 512 字节），body 全零（不足 header.Size 声明量）。
// validateTarSafety 读 header.Size 字段统计，不需要实际 body，因此可正确触发超限检测。
// tar.Reader.Next 只解析 header，不校验 body 完整性；实际读取 body 时才会出错（测试不读）。
func buildRawTarHeaders(t *testing.T, n int, fileSize int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i := 0; i < n; i++ {
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     fmt.Sprintf("file%d.txt", i),
			Size:     fileSize,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("写 raw tar header 失败: %v", err)
		}
		// 不写 body（body 写入量 = 0 != fileSize），tw.Close 会报错，
		// 但我们直接用底层 bytes.Buffer，不调 tw.Close
	}
	// 手动追加 tar EOF（两个全零 512 字节块）
	buf.Write(make([]byte, 1024))
	return buf.Bytes()
}
