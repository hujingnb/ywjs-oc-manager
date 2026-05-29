package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/hermes"
	"oc-manager/internal/integrations/storage"
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
	// S3Write 是 prefix 限定的 STS 临时写凭证（sidecar mirror 用）。
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

// BootstrapS3Write 标准 STS 临时写凭证 + 寻址信息。
type BootstrapS3Write struct {
	// Endpoint 是 S3 兼容存储的访问地址。
	Endpoint string `json:"endpoint"`
	// Region 是存储桶所在区域。
	Region string `json:"region"`
	// Bucket 是存储桶名称。
	Bucket string `json:"bucket"`
	// Prefix 是限定前缀，格式为 apps/<id>/，sidecar 写入时不得越界。
	Prefix string `json:"prefix"`
	// AccessKeyID 是 STS 颁发的临时访问 Key ID。
	AccessKeyID string `json:"access_key_id"`
	// SecretAccessKey 是 STS 颁发的临时访问密钥。
	SecretAccessKey string `json:"secret_access_key"`
	// SessionToken 是 STS 颁发的会话令牌。
	SessionToken string `json:"session_token"`
	// ExpiresAt 是临时凭证的过期时间。
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
	// NewAPIBaseURL 是 manifest.credentials.openai.base_url，指向 new-api 代理地址。
	NewAPIBaseURL string
	// KnowledgeBaseURL 是 manifest.knowledge.runtime_base_url，pod 内访问 manager runtime 知识库 API 的地址。
	KnowledgeBaseURL string
	// PlatformPrompt 是平台层规则模板（platform-rules.md 内容），可含 {var} 占位符。
	PlatformPrompt string
	// PresignTTL 是预签名 URL 与 STS 临时凭证的有效期。
	PresignTTL time.Duration
}

// BootstrapService 组装 pod 启动回调所需的 manifest + 预签名 URL + STS 写凭证。
// 只读：不创建 new-api key、不生成 token；缺失视为 app 未就绪（ErrAppNotReady）。
// api_key 与 control token 由 spec-A 创建流程预先 ensure，bootstrap 只负责解密复用。
type BootstrapService struct {
	store    bootstrapStore
	cipher   *auth.Cipher
	objects  storage.ObjectStore
	sts      storage.STSIssuer
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
	sts storage.STSIssuer,
	skills bootstrapSkillSource,
	cfg BootstrapConfig,
) *BootstrapService {
	return &BootstrapService{
		store:    store,
		cipher:   cipher,
		objects:  objects,
		sts:      sts,
		skills:   skills,
		renderer: defaultManifestRenderer{},
		cfg:      cfg,
	}
}

// ErrAppNotReady 表示 app 缺少 api_key/control token 或尚无发布版本，
// 尚不能 bootstrap（应由创建流程先 ensure）。
var ErrAppNotReady = errors.New("app 未就绪：缺少 api_key、control token 或发布版本")

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

	// 5. 解析并预签名各 skill tar，获得 pod 下载 URL 与 manifest 内相对路径。
	skills, skillRelPaths, err := s.presignSkills(ctx, version)
	if err != nil {
		return BootstrapResult{}, err
	}

	// 6. 组装 AppInputData 并通过 renderer 渲染 manifest YAML、persona 与 platform 文本。
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

	// 8. 签发 STS 临时写凭证，限定到 apps/<id>/ 前缀，供 sidecar mirror 使用。
	prefix := storage.AppPrefix(app.ID)
	creds, err := s.sts.AssumeAppRole(ctx, prefix, s.cfg.PresignTTL)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("签发 STS 凭证失败: %w", err)
	}

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
			AccessKeyID:     creds.AccessKeyID,
			SecretAccessKey: creds.SecretAccessKey,
			SessionToken:    creds.SessionToken,
			ExpiresAt:       creds.ExpiresAt,
		},
	}, nil
}

// skillEntry 是 version.SkillsJson 中单条 skill 的最小视图。
// 仅解析 bootstrap 所需字段，与 skill 元数据其他字段解耦。
type skillEntry struct {
	// Name 是 skill 名，用于 manifest 路径与预签名 URL 日志。
	Name string `json:"name"`
	// FilePath 是 S3 对象 key（迁移后语义，与 storage.SkillKey 输出格式一致）。
	FilePath string `json:"file_path"`
}

// presignSkills 解析 version.SkillsJson，为每个 skill 生成预签名下载 URL 与 manifest 相对路径。
// 返回的 relPaths 与 BootstrapSkill 切片一一对应，透传到 AppInputData.SkillRelPaths。
func (s *BootstrapService) presignSkills(ctx context.Context, version sqlc.AssistantVersion) ([]BootstrapSkill, []string, error) {
	// 无 skill 配置属正常情况（基础 app），直接返回空。
	if len(version.SkillsJson) == 0 {
		return nil, nil, nil
	}
	var entries []skillEntry
	if err := json.Unmarshal(version.SkillsJson, &entries); err != nil {
		return nil, nil, fmt.Errorf("解析 skills_json 失败: %w", err)
	}
	var out []BootstrapSkill
	var relPaths []string
	for _, e := range entries {
		// FilePath 字段存储 S3 key，直接用于预签名；pod 按 RelPath 写入 emptyDir。
		url, err := s.skills.PresignSkill(ctx, e.FilePath, s.cfg.PresignTTL)
		if err != nil {
			return nil, nil, fmt.Errorf("预签名 skill %s 失败: %w", e.Name, err)
		}
		// 相对路径约定：resources/skills/<name>.tar，与 manifest.resources.skills 格式一致。
		rel := path.Join("resources", "skills", e.Name+".tar")
		out = append(out, BootstrapSkill{Name: e.Name, RelPath: rel, URL: url})
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

