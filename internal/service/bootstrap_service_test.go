package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/config"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/hermes"
	"oc-manager/internal/store/sqlc"
)

// fakeBootstrapStore 实现 bootstrapStore，返回预置的 app/org/owner/version/appSkills/webPublishConfig。
type fakeBootstrapStore struct {
	app       sqlc.App
	org       sqlc.Organization
	owner     sqlc.User
	version   sqlc.AssistantVersion
	appSkills []sqlc.AppSkill
	// webPublishConfig 按 orgID 返回的 OrgWebPublishConfig；零值 Enabled=false 表示未开通。
	// webPublishErr 为非 nil 时 GetWebPublishConfig 返回该错误（模拟 sql.ErrNoRows 等）。
	webPublishConfig sqlc.OrgWebPublishConfig
	webPublishErr    error
	// webPublishApplied / webPublishAppliedSet 记录 SetAppWebPublishApplied 最近写入值，供断言。
	webPublishApplied    bool
	webPublishAppliedSet bool
	// 捕获 stamp 的平台 prompt hash，供断言 Build 是否用当前常量 hash 写入。
	capturedPlatformPromptHash string
}

// GetApp 按 ID 返回预置 app。
func (f *fakeBootstrapStore) GetApp(_ context.Context, _ string) (sqlc.App, error) {
	return f.app, nil
}

// GetAppByRuntimeTokenHash 按 token hash 返回预置 app；参数类型与接口保持一致（null.String）。
func (f *fakeBootstrapStore) GetAppByRuntimeTokenHash(_ context.Context, _ null.String) (sqlc.App, error) {
	return f.app, nil
}

// GetOrganization 返回预置组织。
func (f *fakeBootstrapStore) GetOrganization(_ context.Context, _ string) (sqlc.Organization, error) {
	return f.org, nil
}

// GetUser 返回预置用户。
func (f *fakeBootstrapStore) GetUser(_ context.Context, _ string) (sqlc.User, error) {
	return f.owner, nil
}

// GetAssistantVersion 返回预置版本。
func (f *fakeBootstrapStore) GetAssistantVersion(_ context.Context, _ string) (sqlc.AssistantVersion, error) {
	return f.version, nil
}

// ListAppSkillsByApp 返回预置的实例 skill 列表（来源切换后的运行时来源）。
// 方法名与 bootstrapStore 接口保持一致（对应 sqlc.Queries.ListAppSkillsByApp）。
func (f *fakeBootstrapStore) ListAppSkillsByApp(_ context.Context, _ string) ([]sqlc.AppSkill, error) {
	return f.appSkills, nil
}

// GetWebPublishConfig 返回预置的 web_publish 配置；webPublishErr 非 nil 时优先返回错误。
func (f *fakeBootstrapStore) GetWebPublishConfig(_ context.Context, _ string) (sqlc.OrgWebPublishConfig, error) {
	if f.webPublishErr != nil {
		return sqlc.OrgWebPublishConfig{}, f.webPublishErr
	}
	return f.webPublishConfig, nil
}

// SetAppWebPublishApplied 记录 bootstrap 写入的 web_publish_applied 值。
func (f *fakeBootstrapStore) SetAppWebPublishApplied(_ context.Context, arg sqlc.SetAppWebPublishAppliedParams) error {
	f.webPublishApplied = arg.WebPublishApplied
	f.webPublishAppliedSet = true
	return nil
}

// SetAppAppliedPlatformPromptHash 记录 bootstrap 写入的平台 prompt hash。
func (f *fakeBootstrapStore) SetAppAppliedPlatformPromptHash(_ context.Context, arg sqlc.SetAppAppliedPlatformPromptHashParams) error {
	f.capturedPlatformPromptHash = arg.AppliedPlatformPromptHash
	return nil
}

// fakeSkills 实现 bootstrapSkillSource，为任意 relPath 返回固定格式的预签名 URL。
type fakeSkills struct{}

// PresignSkill 构造形如 https://presigned/<relPath> 的 URL，供断言 skill URL 格式。
func (fakeSkills) PresignSkill(_ context.Context, relPath string, _ time.Duration) (string, error) {
	return "https://presigned/" + relPath, nil
}

// captureBootstrapRenderer 实现 bootstrapManifestRenderer，记录 Build 传入的渲染参数，
// 用于校验不同类型应用在进入 Hermes 前已选择正确的平台规则。
type captureBootstrapRenderer struct {
	input hermes.AppInputData
}

// Render 只保存输入并返回空渲染结果，避免测试依赖模板序列化细节。
func (r *captureBootstrapRenderer) Render(in hermes.AppInputData) (string, string, string, error) {
	r.input = in
	return "", "", "", nil
}

// newBootstrapApp 构造一个带 NewapiKeyCiphertext、RuntimeTokenCiphertext 与 VersionID 的完整 app。
// 密文由传入 cipher 生成，确保 Build 能正常解密。
func newBootstrapApp(t *testing.T, cipher *auth.Cipher) sqlc.App {
	t.Helper()
	// 加密 new-api api_key 明文 "sk-test"
	keyCt, err := cipher.Encrypt([]byte("sk-test"))
	require.NoError(t, err)
	// 加密 control token 明文 "control-tok"
	tokCt, err := cipher.Encrypt([]byte("control-tok"))
	require.NoError(t, err)
	return sqlc.App{
		ID:                     "a1",
		OrgID:                  "o1",
		OwnerUserID:            "u1",
		Name:                   "demo",
		NewapiKeyCiphertext:    null.StringFrom(keyCt),
		RuntimeTokenCiphertext: null.StringFrom(tokCt),
		RuntimeTokenHash:       null.StringFrom(HashAppRuntimeToken("control-tok")),
		VersionID:              null.StringFrom("v1"),
		// 默认 bootstrap 场景是普通应用，提示词与 hash 应明确按 standard 选择。
		AppType: string(domain.AppTypeStandard),
	}
}

// TestBootstrapBuildHappyPath 验证正常组装路径：
//   - manifest YAML 含解密后的 api_key 明文（"sk-test"）
//   - s3_write 下发 manager 长期凭证、Prefix 限定到 "apps/a1/"、SessionToken 为空、过期时间在未来
//   - 首启场景（fakeObjectStore 无任何对象）restore 字段全部为空
func TestBootstrapBuildHappyPath(t *testing.T) {
	// 复用 app_runtime_token_test.go 的 helper，构造全零密钥 cipher
	cipher := newRuntimeTokenTestCipher(t)
	app := newBootstrapApp(t, cipher)
	store := &fakeBootstrapStore{
		app: app,
		org: sqlc.Organization{ID: "o1", Name: "Org"},
		// owner DisplayName 留空，不影响 manifest 组装
		owner:   sqlc.User{ID: "u1", DisplayName: "Owner"},
		version: sqlc.AssistantVersion{ID: "v1", MainModel: "gpt-x", SystemPrompt: "you are bot"},
	}
	// 复用 s3_skill_blob_store_test.go 的 fakeObjectStore，默认无对象（ObjectExists 全返回 false）
	svc := NewBootstrapService(store, cipher, newFakeObjectStore(), fakeSkills{}, BootstrapConfig{
		Endpoint:         "http://minio:9000",
		Region:           "us-east-1",
		Bucket:           "oc-apps",
		AccessKeyID:      "manager-ak",
		SecretAccessKey:  "manager-sk",
		NewAPIBaseURL:    "http://new-api:3000",
		KnowledgeBaseURL: "http://manager/runtime",
		PresignTTL:       time.Minute,
	})

	res, err := svc.Build(context.Background(), app)
	require.NoError(t, err)

	// manifest YAML 必须包含解密后的 api_key 明文，证明密文解密正确并写入渲染结果
	assert.Contains(t, res.ManifestYAML, "sk-test")
	// manifest 还须包含解密后的 control token（写入 knowledge.app_token），验证 token 统一链路透传
	assert.Contains(t, res.ManifestYAML, "control-tok")
	// 约定写入前缀必须为 apps/<appID>/，sidecar 据此只写自身前缀
	require.NotNil(t, res.S3Write)
	assert.Equal(t, "apps/a1/", res.S3Write.Prefix)
	// 长期凭证须原样透传到响应，缺任一都会让 sidecar 无法写 S3
	assert.Equal(t, "manager-ak", res.S3Write.AccessKeyID)
	assert.Equal(t, "manager-sk", res.S3Write.SecretAccessKey)
	// 长期凭证下 SessionToken 必须为空（非临时凭证），sidecar 据此不写 aws_session_token
	assert.Empty(t, res.S3Write.SessionToken)
	// 过期时间须为远未来，避免 sidecar 因「临近过期」反复回源续期
	assert.True(t, res.S3Write.ExpiresAt.After(time.Now()))
	// 首启：fakeObjectStore 无任何对象，三个 restore 字段全部为空
	require.NotNil(t, res.Restore)
	assert.Empty(t, res.Restore.WorkspaceURL)
	assert.Empty(t, res.Restore.StateDBURL)
	assert.Empty(t, res.Restore.SessionsURL)
	// bootstrap 应把当前平台 prompt 常量 hash stamp 进 apps，供概览需重启检测。
	assert.Equal(t, config.PlatformPromptHash(domain.AppTypeStandard), store.capturedPlatformPromptHash)
}

// TestBootstrapBuildAICCIsStateless 验证客服应用启动时不依赖对象存储：
// 即使 S3 与 skill 来源均未装配，仍应只下发 manifest、persona 和平台规则，
// 不向外部访客运行时泄露 skill、恢复快照或 S3 长期写凭证。
func TestBootstrapBuildAICCIsStateless(t *testing.T) {
	cipher := newRuntimeTokenTestCipher(t)
	app := newBootstrapApp(t, cipher)
	app.AppType = string(domain.AppTypeAICC)
	store := &fakeBootstrapStore{
		app:     app,
		org:     sqlc.Organization{ID: "o1", Name: "Org"},
		owner:   sqlc.User{ID: "u1", DisplayName: "Owner"},
		version: sqlc.AssistantVersion{ID: "v1", MainModel: "gpt-x", SystemPrompt: "客服助手"},
	}
	// AICC 不应触碰对象存储或 skill 来源，故两项依赖均显式传 nil。
	svc := NewBootstrapService(store, cipher, nil, nil, BootstrapConfig{
		NewAPIBaseURL:    "http://new-api:3000",
		KnowledgeBaseURL: "http://manager/runtime",
	})

	res, err := svc.Build(context.Background(), app)

	require.NoError(t, err)
	// 客服运行时不下载任何 skill，也不在 manifest 内写入 skill 相对路径。
	assert.Nil(t, res.Skills)
	assert.NotContains(t, res.ManifestYAML, "resources/skills/")
	// 客服运行时不读取恢复快照，也不获得 S3 写回凭证。
	assert.Nil(t, res.Restore)
	assert.Nil(t, res.S3Write)
}

// TestBootstrapBuildStandardRequiresObjectStorage 验证普通应用仍依赖 S3：
// 禁用对象存储后不能静默下发不完整配置，必须返回可识别错误供部署排查。
func TestBootstrapBuildStandardRequiresObjectStorage(t *testing.T) {
	cipher := newRuntimeTokenTestCipher(t)
	app := newBootstrapApp(t, cipher)

	// 对象存储缺失时，普通应用无法生成恢复快照的预签名 URL。
	t.Run("缺少对象存储", func(t *testing.T) {
		svc := NewBootstrapService(&fakeBootstrapStore{app: app}, cipher, nil, fakeSkills{}, BootstrapConfig{})

		_, err := svc.Build(context.Background(), app)

		require.ErrorIs(t, err, ErrStandardAppBootstrapRequiresObjectStorage)
	})

	// skill 来源缺失时，普通应用无法为已安装 skill 生成下载 URL。
	t.Run("缺少 skill 来源", func(t *testing.T) {
		svc := NewBootstrapService(&fakeBootstrapStore{app: app}, cipher, newFakeObjectStore(), nil, BootstrapConfig{})

		_, err := svc.Build(context.Background(), app)

		require.ErrorIs(t, err, ErrStandardAppBootstrapRequiresObjectStorage)
	})
}

// TestBootstrapBuildRejectsUnknownAppType 验证未知应用类型必须拒绝启动：
// bootstrap 不能猜测其对象存储权限，避免未来新增类型意外继承 standard 的数据下发能力。
func TestBootstrapBuildRejectsUnknownAppType(t *testing.T) {
	cipher := newRuntimeTokenTestCipher(t)
	app := newBootstrapApp(t, cipher)
	app.AppType = "future-type"
	svc := NewBootstrapService(&fakeBootstrapStore{app: app}, cipher, nil, nil, BootstrapConfig{})

	_, err := svc.Build(context.Background(), app)

	require.ErrorIs(t, err, ErrUnsupportedBootstrapAppType)
}

// TestBootstrapBuildSelectsPlatformPromptByAICCHidden 验证 bootstrap 按应用类型选择平台规则，
// 并将相同类型的平台规则 hash 记录为已应用版本，供概览页判定是否需要重启。
func TestBootstrapBuildSelectsPlatformPromptByAICCHidden(t *testing.T) {
	cipher := newRuntimeTokenTestCipher(t)

	// 普通实例必须使用普通实例平台规则及其 hash，不能误用 AICC 客服规则。
	t.Run("普通实例使用普通平台规则和 hash", func(t *testing.T) {
		app := newBootstrapApp(t, cipher)
		store := &fakeBootstrapStore{
			app:     app,
			org:     sqlc.Organization{ID: "o1", Name: "Org"},
			owner:   sqlc.User{ID: "u1", DisplayName: "Owner"},
			version: sqlc.AssistantVersion{ID: "v1", MainModel: "gpt-x"},
		}
		renderer := &captureBootstrapRenderer{}
		svc := NewBootstrapService(store, cipher, newFakeObjectStore(), fakeSkills{}, BootstrapConfig{PresignTTL: time.Minute})
		svc.renderer = renderer

		_, err := svc.Build(context.Background(), app)

		require.NoError(t, err)
		assert.Equal(t, config.DefaultInstanceSystemPromptTemplate, renderer.input.PlatformRule)
		assert.Equal(t, config.PlatformPromptHash(domain.AppTypeStandard), store.capturedPlatformPromptHash)
	})

	// AICC 隐藏实例必须使用客服平台规则及其 hash，避免将普通实例工作目录约束下发给外部访客。
	t.Run("AICC 隐藏实例使用客服平台规则和 hash", func(t *testing.T) {
		app := newBootstrapApp(t, cipher)
		app.AppType = string(domain.AppTypeAICC)
		store := &fakeBootstrapStore{
			app:     app,
			org:     sqlc.Organization{ID: "o1", Name: "Org"},
			owner:   sqlc.User{ID: "u1", DisplayName: "Owner"},
			version: sqlc.AssistantVersion{ID: "v1", MainModel: "gpt-x"},
		}
		renderer := &captureBootstrapRenderer{}
		svc := NewBootstrapService(store, cipher, newFakeObjectStore(), fakeSkills{}, BootstrapConfig{PresignTTL: time.Minute})
		svc.renderer = renderer

		_, err := svc.Build(context.Background(), app)

		require.NoError(t, err)
		assert.Equal(t, config.DefaultAICCSystemPromptTemplate, renderer.input.PlatformRule)
		assert.Equal(t, config.PlatformPromptHash(domain.AppTypeAICC), store.capturedPlatformPromptHash)
	})
}

// TestBootstrapSkillsFromAppSkills 验证 skill 来源已切换为 app_skills（运行时只看实例 skill）：
//   - 预置 app_skills 行时，bootstrap 输出含对应 skill 的预签名 URL 与正确 RelPath
//   - version.skills_json 有内容但 app_skills 为空时，bootstrap skills 列表应为空（来源切换证明）
func TestBootstrapSkillsFromAppSkills(t *testing.T) {
	cipher := newRuntimeTokenTestCipher(t)
	app := newBootstrapApp(t, cipher)

	// 子测试 1：app_skills 有两个 skill（tar + zip），bootstrap 输出应含两条预签名 URL
	t.Run("app_skills 有 skill 时正确生成预签名 URL 与 RelPath", func(t *testing.T) {
		// 预置两个 app_skills 行：一个 tar、一个 zip
		appSkills := []sqlc.AppSkill{
			{
				ID:            "s1",
				AppID:         "a1",
				Name:          "weather",
				CachedTarPath: "library/platform/weather/1.0.0.tar",
			},
			{
				ID:            "s2",
				AppID:         "a1",
				Name:          "search",
				CachedTarPath: "library/platform/search/2.0.0.zip",
			},
		}
		// version.skills_json 为空，确保 skill 来自 app_skills 而非 version
		store := &fakeBootstrapStore{
			app:       app,
			org:       sqlc.Organization{ID: "o1", Name: "Org"},
			owner:     sqlc.User{ID: "u1", DisplayName: "Owner"},
			version:   sqlc.AssistantVersion{ID: "v1", MainModel: "gpt-x"},
			appSkills: appSkills,
		}
		svc := NewBootstrapService(store, cipher, newFakeObjectStore(), fakeSkills{}, BootstrapConfig{
			Endpoint: "http://minio:9000", Region: "us-east-1", Bucket: "oc-apps",
			AccessKeyID: "ak", SecretAccessKey: "sk", PresignTTL: time.Minute,
		})

		res, err := svc.Build(context.Background(), app)
		require.NoError(t, err)

		// 应有两个 skill 条目
		require.NotNil(t, res.Skills)
		require.Len(t, *res.Skills, 2)

		// 找到 weather（.tar）：RelPath 应为 resources/skills/weather.tar
		var weatherSkill, searchSkill *BootstrapSkill
		for i := range *res.Skills {
			switch (*res.Skills)[i].Name {
			case "weather":
				weatherSkill = &(*res.Skills)[i]
			case "search":
				searchSkill = &(*res.Skills)[i]
			}
		}
		require.NotNil(t, weatherSkill, "缺少 weather skill 条目")
		// RelPath 扩展名随 cached_tar_path，weather 是 .tar
		assert.Equal(t, "resources/skills/weather.tar", weatherSkill.RelPath)
		// URL 由 fakeSkills.PresignSkill 按 cached_tar_path 构造
		assert.Equal(t, "https://presigned/library/platform/weather/1.0.0.tar", weatherSkill.URL)

		require.NotNil(t, searchSkill, "缺少 search skill 条目")
		// RelPath 扩展名随 cached_tar_path，search 是 .zip
		assert.Equal(t, "resources/skills/search.zip", searchSkill.RelPath)
		assert.Equal(t, "https://presigned/library/platform/search/2.0.0.zip", searchSkill.URL)

		// manifest YAML 应包含两个 RelPath（来源切换到 app_skills 后 manifest 也跟随）
		assert.Contains(t, res.ManifestYAML, "weather.tar")
		assert.Contains(t, res.ManifestYAML, "search.zip")
	})

	// 子测试 2：来源切换证明——version.skills_json 有 skill 但 app_skills 为空时，bootstrap skills 为空
	t.Run("version.skills_json 有内容但 app_skills 为空时 bootstrap skills 列表为空", func(t *testing.T) {
		// version.skills_json 含一个 skill，但 app_skills 为空（未种子注入）
		versionSkillsJSON := []byte(`[{"name":"weather","cached_path":"library/platform/weather/1.0.0.tar"}]`)
		store := &fakeBootstrapStore{
			app:   app,
			org:   sqlc.Organization{ID: "o1", Name: "Org"},
			owner: sqlc.User{ID: "u1", DisplayName: "Owner"},
			version: sqlc.AssistantVersion{
				ID:         "v1",
				MainModel:  "gpt-x",
				SkillsJson: versionSkillsJSON,
			},
			appSkills: nil, // app_skills 为空：来源切换后 bootstrap skills 必须为空
		}
		svc := NewBootstrapService(store, cipher, newFakeObjectStore(), fakeSkills{}, BootstrapConfig{
			Endpoint: "http://minio:9000", Region: "us-east-1", Bucket: "oc-apps",
			AccessKeyID: "ak", SecretAccessKey: "sk", PresignTTL: time.Minute,
		})

		res, err := svc.Build(context.Background(), app)
		require.NoError(t, err)

		// 来源切换后，version.skills_json 中的 skill 不再下发；app_skills 为空则 skills 列表为空
		require.NotNil(t, res.Skills)
		assert.Empty(t, *res.Skills, "来源切换证明：version.skills_json 有内容但 app_skills 空时 bootstrap skills 应为空")
	})
}

// TestBootstrapBuildAppNotReady 验证未就绪边界：
//   - app 缺少 NewapiKeyCiphertext 时，Build 必须返回 ErrAppNotReady
func TestBootstrapBuildAppNotReady(t *testing.T) {
	cipher := newRuntimeTokenTestCipher(t)
	// 构造一个没有任何密文字段的最简 app，模拟创建流程尚未完成的状态
	app := sqlc.App{ID: "a1"}
	svc := NewBootstrapService(
		&fakeBootstrapStore{app: app},
		cipher,
		newFakeObjectStore(),
		fakeSkills{},
		BootstrapConfig{PresignTTL: time.Minute},
	)
	_, err := svc.Build(context.Background(), app)
	// 缺少 api_key 密文必须返回 ErrAppNotReady，而非其他错误
	require.ErrorIs(t, err, ErrAppNotReady)
}

// TestBuildAppInputInjectsWebPublishWhenReady 验证企业已开通且 provisioning_status=ready 时，
// AppInputData 中三个 WebPublish* 字段被正确注入（base_domain、app_token=controlToken、
// runtime_base_url=KnowledgeBaseURL），manifest YAML 也应包含对应值。
func TestBuildAppInputInjectsWebPublishWhenReady(t *testing.T) {
	cipher := newRuntimeTokenTestCipher(t)
	app := newBootstrapApp(t, cipher)
	// stub：web_publish 已开通且 provisioning ready，base_domain 设为 apps.example.com
	store := &fakeBootstrapStore{
		app:   app,
		org:   sqlc.Organization{ID: "o1", Name: "Org"},
		owner: sqlc.User{ID: "u1", DisplayName: "Owner"},
		version: sqlc.AssistantVersion{
			ID:        "v1",
			MainModel: "gpt-x",
		},
		webPublishConfig: sqlc.OrgWebPublishConfig{
			OrgID:              "o1",
			Enabled:            true,
			ProvisioningStatus: domain.ProvisioningReady,
			BaseDomain:         "apps.example.com",
		},
	}
	svc := NewBootstrapService(store, cipher, newFakeObjectStore(), fakeSkills{}, BootstrapConfig{
		Endpoint:         "http://minio:9000",
		Region:           "us-east-1",
		Bucket:           "oc-apps",
		AccessKeyID:      "ak",
		SecretAccessKey:  "sk",
		NewAPIBaseURL:    "http://new-api:3000",
		KnowledgeBaseURL: "http://manager/runtime",
		PresignTTL:       time.Minute,
	})

	res, err := svc.Build(context.Background(), app)
	require.NoError(t, err)

	// manifest YAML 应包含 base_domain（hermes 条件渲染 web_publish 段的关键字段）
	assert.Contains(t, res.ManifestYAML, "apps.example.com",
		"web_publish 开通且 ready 时 manifest 应含 base_domain")
	// manifest 应包含 app_token（与 knowledge 复用同一 controlToken 明文）
	assert.Contains(t, res.ManifestYAML, "control-tok",
		"web_publish 开通且 ready 时 manifest 应含 app_token（controlToken）")
	// manifest 应包含 runtime_base_url（同 KnowledgeBaseURL）
	assert.Contains(t, res.ManifestYAML, "http://manager/runtime",
		"web_publish 开通且 ready 时 manifest 应含 runtime_base_url（=KnowledgeBaseURL）")
}

// TestBuildAppInputOmitsWebPublishWhenDisabled 验证企业未开通（Enabled=false 或 sql.ErrNoRows）时，
// AppInputData 的三个 WebPublish* 字段均为空，manifest 不含 web_publish 相关域名。
func TestBuildAppInputOmitsWebPublishWhenDisabled(t *testing.T) {
	cipher := newRuntimeTokenTestCipher(t)
	app := newBootstrapApp(t, cipher)

	// 子测试 1：store 返回 sql.ErrNoRows（企业无记录），三字段应为空
	t.Run("GetWebPublishConfig 返回 ErrNoRows 时三字段为空", func(t *testing.T) {
		// 模拟企业 org_web_publish_config 无记录（未开通过）
		store := &fakeBootstrapStore{
			app:           app,
			org:           sqlc.Organization{ID: "o1", Name: "Org"},
			owner:         sqlc.User{ID: "u1", DisplayName: "Owner"},
			version:       sqlc.AssistantVersion{ID: "v1", MainModel: "gpt-x"},
			webPublishErr: sql.ErrNoRows,
		}
		svc := NewBootstrapService(store, cipher, newFakeObjectStore(), fakeSkills{}, BootstrapConfig{
			Endpoint: "http://minio:9000", Region: "us-east-1", Bucket: "oc-apps",
			AccessKeyID: "ak", SecretAccessKey: "sk",
			KnowledgeBaseURL: "http://manager/runtime",
			PresignTTL:       time.Minute,
		})

		res, err := svc.Build(context.Background(), app)
		require.NoError(t, err)

		// manifest 不应含 apps.example.com，证明 web_publish 段未注入
		assert.NotContains(t, res.ManifestYAML, "apps.example.com",
			"未开通时 manifest 不应含 web_publish base_domain")
	})

	// 子测试 2：Enabled=false（企业被停用），三字段也应为空
	t.Run("Enabled=false 时三字段为空", func(t *testing.T) {
		// 模拟企业 web_publish 配置存在但 Enabled=false（被停用）
		store := &fakeBootstrapStore{
			app:   app,
			org:   sqlc.Organization{ID: "o1", Name: "Org"},
			owner: sqlc.User{ID: "u1", DisplayName: "Owner"},
			version: sqlc.AssistantVersion{
				ID:        "v1",
				MainModel: "gpt-x",
			},
			webPublishConfig: sqlc.OrgWebPublishConfig{
				OrgID:              "o1",
				Enabled:            false,
				ProvisioningStatus: domain.ProvisioningReady,
				BaseDomain:         "apps.example.com",
			},
		}
		svc := NewBootstrapService(store, cipher, newFakeObjectStore(), fakeSkills{}, BootstrapConfig{
			Endpoint: "http://minio:9000", Region: "us-east-1", Bucket: "oc-apps",
			AccessKeyID: "ak", SecretAccessKey: "sk",
			KnowledgeBaseURL: "http://manager/runtime",
			PresignTTL:       time.Minute,
		})

		res, err := svc.Build(context.Background(), app)
		require.NoError(t, err)

		// Enabled=false 时 manifest 不应含 web_publish base_domain
		assert.NotContains(t, res.ManifestYAML, "apps.example.com",
			"Enabled=false 时 manifest 不应含 web_publish base_domain")
	})
}
