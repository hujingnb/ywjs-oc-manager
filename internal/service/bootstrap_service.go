package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/hermes"
	"oc-manager/internal/integrations/storage"
	mlog "oc-manager/internal/log"
	"oc-manager/internal/store/sqlc"
)

// BootstrapResult 是 GET /internal/apps/{id}/bootstrap 的响应体（pod initContainer 消费）。
type BootstrapResult struct {
	// ManifestYAML 是渲染后的 manifest（YAML 字符串，含 api_key/persona 路径/skills/knowledge）。
	ManifestYAML string `json:"manifest_yaml"`
	// Persona 是 resources/persona.md 的文本内容，initContainer 写入 emptyDir。
	Persona string `json:"persona"`
	// PlatformRule 是 resources/platform-rules.md 的文本内容，initContainer 写入 emptyDir。
	PlatformRule string `json:"platform_rule"`
	// Skills 是各 skill tar 的预签名读 URL + 目标相对路径（与 manifest.resources.skills 对应）。
	Skills []BootstrapSkill `json:"skills"`
	// Restore 是会话/工作区恢复的预签名读 URL；首启时各字段为空。
	Restore BootstrapRestore `json:"restore"`
	// S3Write 是 sidecar mirror 写回 S3 用的凭证 + 寻址信息（长期凭证直发，见 BootstrapS3Write）。
	S3Write BootstrapS3Write `json:"s3_write"`
}

// BootstrapSkill 单个 skill 的下载信息。
type BootstrapSkill struct {
	// Name 是 skill 名。
	Name string `json:"name"`
	// RelPath 是 pod 内目标相对路径，如 resources/skills/weather.tar。
	RelPath string `json:"rel_path"`
	// URL 是预签名 GET URL。
	URL string `json:"url"`
}

// BootstrapRestore 会话/工作区恢复 URL；为空表示首启或该项无快照。
type BootstrapRestore struct {
	// WorkspaceURL 是工作区快照的预签名读 URL；首启时为空。
	WorkspaceURL string `json:"workspace_url,omitempty"`
	// StateDBURL 是状态数据库快照的预签名读 URL；首启时为空。
	StateDBURL string `json:"state_db_url,omitempty"`
	// SessionsURL 是会话快照的预签名读 URL；首启时为空。
	SessionsURL string `json:"sessions_url,omitempty"`
}

// BootstrapS3Write 是 sidecar 写回 S3 所需的凭证与寻址信息。
// 目标对象存储不支持标准 STS AssumeRole，故下发 manager 长期凭证（SessionToken 为空）。
type BootstrapS3Write struct {
	// Endpoint 是 S3 兼容存储的访问地址。
	Endpoint string `json:"endpoint"`
	// Region 是存储桶所在区域。
	Region string `json:"region"`
	// Bucket 是存储桶名称。
	Bucket string `json:"bucket"`
	// Prefix 是约定写入前缀，格式为 apps/<id>/；sidecar 只主动写该前缀（凭证本身不强制限定）。
	Prefix string `json:"prefix"`
	// AccessKeyID 是 manager 长期访问 Key ID。
	AccessKeyID string `json:"access_key_id"`
	// SecretAccessKey 是 manager 长期访问密钥。
	SecretAccessKey string `json:"secret_access_key"`
	// SessionToken 长期凭证下为空；保留字段以兼容 sidecar JSON 解析（为空时 sidecar 不写该项）。
	SessionToken string `json:"session_token"`
	// ExpiresAt 长期凭证下为远未来时间，使 sidecar 不会因临近过期反复回源续期。
	ExpiresAt time.Time `json:"expires_at"`
}

// bootstrapStore 是 bootstrap 组装所需的最小数据库能力（窄接口，便于单测注入假实现）。
type bootstrapStore interface {
	// GetApp 按应用 ID 查询应用记录。
	GetApp(ctx context.Context, id string) (sqlc.App, error)
	// GetAppByRuntimeTokenHash 按 runtime token hash 查询应用记录；hash 使用 null.String 与 sqlc 保持一致。
	GetAppByRuntimeTokenHash(ctx context.Context, runtimeTokenHash null.String) (sqlc.App, error)
	// GetOrganization 按组织 ID 查询组织记录。
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	// GetUser 按用户 ID 查询用户记录。
	GetUser(ctx context.Context, id string) (sqlc.User, error)
	// GetAssistantVersion 按版本 ID 查询助手版本记录。
	GetAssistantVersion(ctx context.Context, id string) (sqlc.AssistantVersion, error)
	// ListAppSkillsByApp 返回指定实例的全部 app_skills 行，是运行时 skill 下发的唯一来源。
	// 方法名与 sqlc.Queries.ListAppSkillsByApp 保持一致，生产传入 dbStore.Queries 可直接满足。
	ListAppSkillsByApp(ctx context.Context, appID string) ([]sqlc.AppSkill, error)
	// GetWebPublishConfig 查询企业 web_publish 开通配置；企业未开通时返回 sql.ErrNoRows。
	GetWebPublishConfig(ctx context.Context, orgID string) (sqlc.OrgWebPublishConfig, error)
	// SetAppWebPublishApplied 记录本次 bootstrap 是否注入了 web-publish 发布能力，用于「能力已开通需重启」检测。
	SetAppWebPublishApplied(ctx context.Context, arg sqlc.SetAppWebPublishAppliedParams) error
}

// bootstrapSkillSource 提供 skill 预签名 URL（由 S3SkillBlobStore 实现）。
type bootstrapSkillSource interface {
	// PresignSkill 为指定相对路径的 skill tar 生成预签名读 URL，ttl 控制有效期。
	PresignSkill(ctx context.Context, relPath string, ttl time.Duration) (string, error)
}

// bootstrapManifestRenderer 渲染 persona/platform 文本并序列化 manifest，便于测试替换。
type bootstrapManifestRenderer interface {
	// Render 接受应用输入数据，返回序列化后的 manifestYAML、persona 文本与 platform 规则文本。
	Render(in hermes.AppInputData) (manifestYAML, persona, platform string, err error)
}

// defaultManifestRenderer 用 hermes 包的渲染函数实现 bootstrapManifestRenderer。
type defaultManifestRenderer struct{}

// Render 渲染 persona/platform 文本并序列化 manifest YAML。
// 内部依次调用 hermes.RenderPersonaText、hermes.RenderRuleText、
// hermes.BuildManifest + hermes.MarshalManifestYAML，保持与 WriteAppInput 相同的渲染语义。
func (defaultManifestRenderer) Render(in hermes.AppInputData) (string, string, string, error) {
	vars := hermes.VariablesFromContext(in.OrgName, in.AppName, in.OwnerName)
	persona, err := hermes.RenderPersonaText(in.PersonaText, vars)
	if err != nil {
		return "", "", "", fmt.Errorf("render persona: %w", err)
	}
	platform, err := hermes.RenderRuleText(in.PlatformRule, vars)
	if err != nil {
		return "", "", "", fmt.Errorf("render platform rule: %w", err)
	}
	yamlBytes, err := hermes.MarshalManifestYAML(hermes.BuildManifest(in))
	if err != nil {
		return "", "", "", fmt.Errorf("marshal manifest: %w", err)
	}
	return string(yamlBytes), persona, platform, nil
}

// BootstrapConfig 是 bootstrap 组装的静态配置（来自 manager 配置文件）。
type BootstrapConfig struct {
	// Endpoint 是 S3 兼容存储的访问地址（透传给 pod 的 s3_write）。
	Endpoint string
	// Region 是存储桶所在区域。
	Region string
	// Bucket 是存储桶名称。
	Bucket string
	// AccessKeyID / SecretAccessKey 是 manager 的长期 S3 凭证。目标对象存储不支持标准
	// STS AssumeRole，故 sidecar 写回数据直接复用这对长期凭证（透传到 s3_write），
	// 不再签发 prefix 限定的临时凭证；隔离取舍见 Build 第 8 步注释与 secret 配置说明。
	AccessKeyID     string
	SecretAccessKey string
	// NewAPIBaseURL 是 manifest.credentials.openai.base_url，指向 new-api 代理地址。
	NewAPIBaseURL string
	// KnowledgeBaseURL 是 manifest.knowledge.runtime_base_url，pod 内访问 manager runtime 知识库 API 的地址。
	KnowledgeBaseURL string
	// PlatformPrompt 是平台层规则模板（platform-rules.md 内容），可含 {var} 占位符。
	PlatformPrompt string
	// PresignTTL 是预签名读 URL 的有效期。
	PresignTTL time.Duration
}

// BootstrapService 组装 pod 启动回调所需的 manifest + 预签名读 URL + S3 写凭证。
// 只读：不创建 new-api key、不生成 token；缺失视为 app 未就绪（ErrAppNotReady）。
// api_key 与 control token 由 spec-A 创建流程预先 ensure，bootstrap 只负责解密复用。
type BootstrapService struct {
	store    bootstrapStore
	cipher   *auth.Cipher
	objects  storage.ObjectStore
	skills   bootstrapSkillSource
	renderer bootstrapManifestRenderer
	cfg      BootstrapConfig
}

// NewBootstrapService 构造 bootstrap 服务。
// renderer 固定为 defaultManifestRenderer；如需单测替换，直接对 BootstrapService 字段赋值。
func NewBootstrapService(
	store bootstrapStore,
	cipher *auth.Cipher,
	objects storage.ObjectStore,
	skills bootstrapSkillSource,
	cfg BootstrapConfig,
) *BootstrapService {
	return &BootstrapService{
		store:    store,
		cipher:   cipher,
		objects:  objects,
		skills:   skills,
		renderer: defaultManifestRenderer{},
		cfg:      cfg,
	}
}

// ErrAppNotReady 表示 app 缺少 api_key/control token 或尚无发布版本，
// 尚不能 bootstrap（应由创建流程先 ensure）。
var ErrAppNotReady = errors.New("app 未就绪：缺少 api_key、control token 或发布版本")

// ResolveByControlToken 用 control token hash 反查 app（鉴权即定位）。
// tokenHash 是调用方已对 plain token 做过 HashAppRuntimeToken 后的 hex 字符串；
// 内部转换为 null.String 以匹配 sqlc 生成的查询参数类型。
func (s *BootstrapService) ResolveByControlToken(ctx context.Context, tokenHash string) (sqlc.App, error) {
	return s.store.GetAppByRuntimeTokenHash(ctx, null.StringFrom(tokenHash))
}

// Build 按 appID 组装 bootstrap 响应。
// 调用方已通过 control token 鉴权并确认 token 属于该 app，此处不再重复鉴权。
// 整体只读：不写库、不创建 key，所有 new-api key 与 control token 已由创建流程 ensure。
func (s *BootstrapService) Build(ctx context.Context, app sqlc.App) (BootstrapResult, error) {
	// 1. 解密 new-api api_key；缺失代表创建流程未完成，视为未就绪。
	if !app.NewapiKeyCiphertext.Valid {
		return BootstrapResult{}, ErrAppNotReady
	}
	apiKeyPlain, err := s.cipher.Decrypt(app.NewapiKeyCiphertext.String)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("解密 api_key 失败: %w", err)
	}

	// 2. 解密 control token；缺失同样视为未就绪。
	// control token 用于 manifest.knowledge.app_token，供 pod 访问 manager runtime 知识库 API。
	if !app.RuntimeTokenCiphertext.Valid {
		return BootstrapResult{}, ErrAppNotReady
	}
	controlToken, err := s.cipher.Decrypt(app.RuntimeTokenCiphertext.String)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("解密 control token 失败: %w", err)
	}

	// 3. 查询组织、所有者与版本信息，用于模板渲染与 manifest 组装。
	org, err := s.store.GetOrganization(ctx, app.OrgID)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("查询组织失败: %w", err)
	}
	owner, err := s.store.GetUser(ctx, app.OwnerUserID)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("查询所有者失败: %w", err)
	}
	// 版本 ID 未设置代表 app 尚未发布任何版本，无法 bootstrap。
	if !app.VersionID.Valid {
		return BootstrapResult{}, ErrAppNotReady
	}
	version, err := s.store.GetAssistantVersion(ctx, app.VersionID.String)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("查询版本失败: %w", err)
	}

	// 4. 解析 routing 映射；非法 JSON 容错退化为空 map（不影响整体 bootstrap 流程）。
	routing := map[string]string{}
	if len(version.RoutingJson) > 0 {
		_ = json.Unmarshal(version.RoutingJson, &routing)
	}

	// 5. 从 app_skills 取实例 skill 并预签名，获得 pod 下载 URL 与 manifest 内相对路径。
	// 来源已从 version.SkillsJson 切换为 app_skills，运行时只下发实例实际安装的 skill。
	skills, skillRelPaths, err := s.presignSkills(ctx, app.ID)
	if err != nil {
		return BootstrapResult{}, err
	}

	// 6. 组装 AppInputData 并通过 renderer 渲染 manifest YAML、persona 与 platform 文本。
	// Language：优先取实例 locale（已在创建/手动更新时快照），空时留空由 renderer 回退平台默认。
	appLanguage := ""
	if app.Locale.Valid {
		appLanguage = app.Locale.String
	}
	in := hermes.AppInputData{
		AppID:                   app.ID,
		AppName:                 app.Name,
		Model:                   version.MainModel,
		OpenAIAPIKey:            string(apiKeyPlain),
		OpenAIBaseURL:           s.cfg.NewAPIBaseURL,
		KnowledgeRuntimeBaseURL: s.cfg.KnowledgeBaseURL,
		KnowledgeAppToken:       string(controlToken),
		PersonaText:             version.SystemPrompt,
		PlatformRule:            s.cfg.PlatformPrompt,
		Routing:                 routing,
		SkillRelPaths:           skillRelPaths,
		OrgName:                 org.Name,
		OwnerName:               owner.DisplayName,
		Language:                appLanguage,
	}
	// web_publish：企业开通且 provisioning ready 时注入，触发 oc-publish skill 条件渲染。
	// app_token 复用 per-app controlToken（与 knowledge 同），runtime base 同 knowledge base。
	// 企业未开通（sql.ErrNoRows）或未就绪时三字段留空，hermes 不渲染 web_publish 段。
	webPublishInjected := false
	if wp, werr := s.store.GetWebPublishConfig(ctx, app.OrgID); werr == nil &&
		wp.Enabled && wp.ProvisioningStatus == domain.ProvisioningReady {
		in.WebPublishRuntimeBaseURL = s.cfg.KnowledgeBaseURL
		in.WebPublishAppToken = string(controlToken)
		in.WebPublishBaseDomain = wp.BaseDomain
		webPublishInjected = true
	}
	// 记录本次 bootstrap 是否注入发布能力：与企业开通态比对即可判定运行中实例是否「需重启使能力生效」。
	// best-effort：写失败不阻断 bootstrap（仅影响 needs-restart 提示，不影响实例正常启动），仅记 warning。
	if err := s.store.SetAppWebPublishApplied(ctx, sqlc.SetAppWebPublishAppliedParams{
		WebPublishApplied: webPublishInjected,
		ID:                app.ID,
	}); err != nil {
		slog.WarnContext(ctx, "记录 web_publish_applied 失败", "app_id", app.ID, mlog.Err(err))
	}
	manifestYAML, persona, platform, err := s.renderer.Render(in)
	if err != nil {
		return BootstrapResult{}, err
	}

	// 7. restore 预签名：对存在的 workspace/state.db/sessions 对象生成读 URL；首启时留空。
	restore, err := s.presignRestore(ctx, app.ID)
	if err != nil {
		return BootstrapResult{}, err
	}

	// 8. 下发 S3 写凭证。目标对象存储不支持标准 STS AssumeRole，无法签发 prefix 限定的
	//    临时凭证，故直接下发 manager 的长期凭证（取舍：sidecar 凭证拥有整个 bucket 的
	//    读写权限，per-app 前缀隔离退化为 sidecar 只主动写自身 Prefix 的「善意行为」，不再
	//    由凭证策略强制；详见 secret 配置 storage.s3 注释）。SessionToken 留空表示这是长期
	//    凭证而非临时凭证；ExpiresAt 给远未来，使 sidecar 不会因「临近过期」反复回源续期。
	prefix := storage.AppPrefix(app.ID)
	return BootstrapResult{
		ManifestYAML: manifestYAML,
		Persona:      persona,
		PlatformRule: platform,
		Skills:       skills,
		Restore:      restore,
		S3Write: BootstrapS3Write{
			Endpoint:        s.cfg.Endpoint,
			Region:          s.cfg.Region,
			Bucket:          s.cfg.Bucket,
			Prefix:          prefix,
			AccessKeyID:     s.cfg.AccessKeyID,
			SecretAccessKey: s.cfg.SecretAccessKey,
			SessionToken:    "",
			ExpiresAt:       time.Now().AddDate(10, 0, 0),
		},
	}, nil
}

// presignSkills 从 app_skills 取实例安装的 skill 列表，为每条记录生成预签名下载 URL 与
// manifest 相对路径。返回的 relPaths 与 BootstrapSkill 切片一一对应，透传到
// AppInputData.SkillRelPaths。
//
// 来源已从 version.SkillsJson 切换为 app_skills（P4-T3）：运行时只下发实例实际安装的
// skill（seedVersionSkills 种子注入 + 用户手动安装的合集），版本配置不再直接进入 bootstrap。
// RelPath 格式：resources/skills/<name><ext>，扩展名取自 CachedTarPath（兼容 .tar 与 .zip）。
func (s *BootstrapService) presignSkills(ctx context.Context, appID string) ([]BootstrapSkill, []string, error) {
	rows, err := s.store.ListAppSkillsByApp(ctx, appID)
	if err != nil {
		return nil, nil, fmt.Errorf("查询 app_skills 失败: %w", err)
	}
	// 无已安装 skill 属正常情况（基础 app 或种子注入尚未完成），直接返回空。
	if len(rows) == 0 {
		return nil, nil, nil
	}
	var out []BootstrapSkill
	var relPaths []string
	for _, row := range rows {
		// CachedTarPath 存储 S3 对象 key，直接用于预签名；pod 按 RelPath 写入 emptyDir。
		url, err := s.skills.PresignSkill(ctx, row.CachedTarPath, s.cfg.PresignTTL)
		if err != nil {
			return nil, nil, fmt.Errorf("预签名 skill %s 失败: %w", row.Name, err)
		}
		// RelPath 格式：resources/skills/<name><ext>，扩展名取自 CachedTarPath（兼容 .tar/.zip）。
		// oc-restore 下载到 $INPUT_DIR/$rel；render_skills.py 按 suffix 决定解压方式。
		ext := path.Ext(row.CachedTarPath)
		if ext == "" {
			// CachedTarPath 不含扩展名时退化为 .tar（向后兼容旧数据）。
			ext = ".tar"
		}
		rel := path.Join("resources", "skills", row.Name+ext)
		out = append(out, BootstrapSkill{Name: row.Name, RelPath: rel, URL: url})
		relPaths = append(relPaths, rel)
	}
	return out, relPaths, nil
}

// presignRestore 对 apps/<id>/workspace、state.db、sessions 三个对象按需生成预签名读 URL。
// 对象不存在时跳过（首启场景），存在则签名，供 pod initContainer 恢复快照使用。
func (s *BootstrapService) presignRestore(ctx context.Context, appID string) (BootstrapRestore, error) {
	var r BootstrapRestore
	// 三个固定 restore 对象：workspace 目录归档、sqlite 状态库、会话归档。
	type item struct {
		key string
		dst *string
	}
	items := []item{
		{storage.WorkspaceKey(appID), &r.WorkspaceURL},
		{storage.StateDBKey(appID), &r.StateDBURL},
		{storage.SessionsKey(appID), &r.SessionsURL},
	}
	for _, it := range items {
		exists, err := s.objects.ObjectExists(ctx, it.key)
		if err != nil {
			return BootstrapRestore{}, fmt.Errorf("查询 restore 对象 %s 失败: %w", it.key, err)
		}
		if !exists {
			// 对象不存在：首启或该类型快照尚未生成，留空即可，pod 会跳过对应恢复步骤。
			continue
		}
		url, err := s.objects.PresignGet(ctx, it.key, s.cfg.PresignTTL)
		if err != nil {
			return BootstrapRestore{}, fmt.Errorf("预签名 restore %s 失败: %w", it.key, err)
		}
		*it.dst = url
	}
	return r, nil
}

