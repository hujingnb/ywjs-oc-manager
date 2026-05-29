package service

import (
	"context"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/integrations/hermes"
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
