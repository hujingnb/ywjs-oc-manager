// Package config 负责加载 manager YAML 配置、解析持续时间并在进程启动前执行必需项校验。
package config

import (
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是 manager 进程启动所需的完整配置。
// 这里仅放入工程基线阶段需要校验的字段；后续业务模块会在保持兼容的前提下继续扩展。
type Config struct {
	// App 是 manager API 自身的监听、数据目录和公开访问配置。
	App AppConfig `yaml:"app"`
	// Database 是业务 PostgreSQL 连接配置，缺失时禁止启动。
	Database DatabaseConfig `yaml:"database"`
	// Redis 是异步 job queue 与跨进程通知所需的 Redis 配置。
	Redis RedisConfig `yaml:"redis"`
	// Auth 是登录 cookie、JWT 和 CSRF 相关配置。
	Auth AuthConfig `yaml:"auth"`
	// Security 持有加密敏感字段所需的根密钥配置。
	Security SecurityConfig `yaml:"security"`
	// Hermes 描述 Hermes runtime 镜像、LLM 和工作目录归档策略。
	Hermes HermesConfig `yaml:"hermes"`
	// Agent 描述 manager 与 runtime agent 之间的心跳协议参数。
	Agent AgentConfig `yaml:"agent"`
	// Runtime 描述节点自动注册密钥和主动探测阈值。
	Runtime RuntimeConfig `yaml:"runtime"`
	// NewAPI 描述 manager 调用 new-api 管理接口所需的凭据。
	NewAPI NewAPIConfig `yaml:"newapi"`
}

// AppConfig 描述 manager API 进程自身的运行参数。
// HTTPAddr 必须显式配置，避免容器内外端口不一致时静默监听错误地址。
type AppConfig struct {
	// Env 标识当前运行环境，供日志、调试和未来环境分支使用。
	Env string `yaml:"env"`
	// HTTPAddr 是 manager API 监听地址，必须显式配置以匹配容器端口暴露。
	HTTPAddr string `yaml:"http_addr"`
	// PublicBaseURL 是浏览器访问 manager 的公开地址，用作 CORS origin 和外部链接基准。
	PublicBaseURL string `yaml:"public_base_url"`
	// DataRoot 是 manager 本地数据根目录，承载工作区归档等非知识库文件。
	DataRoot string `yaml:"data_root"`
	// KnowledgeRoot 是知识库主副本根目录（manager 端"主拷贝"，由 worker 同步到各 runtime node）。
	// 此前由 OCM_KNOWLEDGE_ROOT 环境变量提供，现统一收口到 yaml；为空启动时 fail-fast。
	// 路径下结构：orgs/<org_id>/...、apps/<app_id>/...，由 files.SafeRoot 沙箱化。
	KnowledgeRoot string `yaml:"knowledge_root"`
	// ShutdownPeriod 是优雅退出等待时间的进程内派生配置，不从 YAML 读取。
	ShutdownPeriod time.Duration `yaml:"-"`
}

// DatabaseConfig 描述业务 PostgreSQL 连接。
// URL 属于启动必需项，缺失时进程应 fail-fast，避免运行到业务请求时才暴露配置错误。
type DatabaseConfig struct {
	URL string `yaml:"url"`
}

// RedisConfig 描述 Redis 连接和 key 命名前缀。
// KeyPrefix 用于隔离 manager 与 new-api 等共享 Redis 的键空间。
type RedisConfig struct {
	// Addr 是 Redis 服务地址，worker queue 和 job 通知都依赖它。
	Addr string `yaml:"addr"`
	// Password 是 Redis 认证密码；为空表示本地无密码 Redis，不应写入日志。
	Password string `yaml:"password"`
	// DB 是 Redis 逻辑库编号，用于隔离本地或测试环境。
	DB int `yaml:"db"`
	// KeyPrefix 是 Redis key 前缀，用于避免多个系统共享 Redis 时互相污染。
	KeyPrefix string `yaml:"key_prefix"`
}

// AuthConfig 描述后台登录和令牌签发配置。
// access token 用于短期 API 认证，refresh token 只保存 hash 并用于续期。
type AuthConfig struct {
	// CookieDomain 控制浏览器 cookie 作用域，本地开发可为空。
	CookieDomain string `yaml:"cookie_domain"`
	// AccessTokenTTL 控制 access token 生命周期，必须是可解析且大于 0 的持续时间。
	AccessTokenTTL Duration `yaml:"access_token_ttl"`
	// RefreshTokenTTL 控制 refresh token 生命周期，也用于 refresh_tokens 表过期时间。
	RefreshTokenTTL Duration `yaml:"refresh_token_ttl"`
	// JWTAccessSecret 只用于 access token HMAC 签名。
	JWTAccessSecret string `yaml:"jwt_access_secret"`
	// JWTRefreshSecret 只用于 refresh token HMAC 签名，必须与 access secret 分离。
	JWTRefreshSecret string `yaml:"jwt_refresh_secret"`
	// CSRFSecret 用于 CSRF token 保护，缺失时登录态接口不得启动。
	CSRFSecret string `yaml:"csrf_secret"`
}

// SecurityConfig 描述敏感字段加解密所需的根密钥。
// MasterKey 必须是 base64 编码的 32 字节随机数，缺失或长度不符时启动 fail-fast。
type SecurityConfig struct {
	MasterKey string `yaml:"master_key"`
}

// HermesConfig 是 Hermes runtime 镜像与 manager 集成的配置段。
// 对应应用 yaml 顶级 key `hermes:`。
type HermesConfig struct {
	// RuntimeImage 是 manager docker run 启动 hermes 容器用的镜像引用（name:tag）。
	RuntimeImage string `yaml:"runtime_image"`
	// RuntimeImages 是平台可选的 Hermes 镜像列表，助手版本的 image_id 引用其中的 id。
	// 与单值 RuntimeImage 并存（后续 Phase 移除单值字段）。
	RuntimeImages []RuntimeImageConfig `yaml:"runtime_images"`
	// SystemPromptTemplate 是平台级 prompt 模板，作为 input/resources/platform-rules.md
	// 的内容写入 manager 端 input 目录，由节点 oc-entrypoint 在容器启动时翻译进 SOUL.md。
	SystemPromptTemplate string `yaml:"system_prompt_template"`
	// Workspace 仅保留同名段（WorkspaceConfig 是通用类型，不绑 Hermes）。
	Workspace WorkspaceConfig `yaml:"workspace"`
	// LLM 是 manager 写入 manifest.app.model 时的默认值兜底，在 app 未指定模型时使用。
	LLM HermesLLMConfig `yaml:"llm"`
	// ContainerNetworks 是 hermes 容器接入的 docker network 清单。
	// 必须包含 new-api 所在的 network（默认 docker compose project name 派生的
	// "<project>_default"，如 oc-manager_default），否则 Hermes 容器无法解析
	// "new-api" hostname → chat completions Connection error。
	// 留空时 docker 默认 bridge network，与 compose 起的 new-api 不互通。
	ContainerNetworks []string `yaml:"container_networks"`
}

// RuntimeImageConfig 是单个可选 Hermes 镜像条目。
// id 是稳定槽位标识（助手版本存它），label 供前端展示，ref 是具体可拉取 tag。
type RuntimeImageConfig struct {
	ID    string `yaml:"id"`
	Label string `yaml:"label"`
	Ref   string `yaml:"ref"`
}

// HermesLLMConfig 仅保留兜底默认值字段（具体每 app 的模型由 apps.model_id 决定）。
//
//   - BaseURL：OpenAI 兼容 endpoint，Hermes 容器从 docker network 看到的 new-api 地址，
//     必须含 /v1 路径后缀；作为 manifest.credentials.openai.base_url 写入
//     input/manifest.yaml；镜像内 oc-entrypoint 渲染为 hermes config.yaml 的
//     model.base_url。
//   - DefaultProvider / DefaultModel：写入容器内 config.yaml，Hermes 用作默认 provider/model；
//     缺失时 Hermes 默认去拨上游，无法路由到本地 new-api。
//
// 容器实际用的 api_key 由 manager 替每个应用通过 new-api `POST /api/token/:id/key`
// 拉到完整 sk-，加密落 apps.newapi_key_ciphertext 后在 ensureAPIKey 阶段写入
// manifest.credentials.openai.api_key，镜像内 oc-entrypoint 渲染为 config.yaml
// 的 model.api_key；不再有"全局共享 sk-"的配置项。
type HermesLLMConfig struct {
	BaseURL         string `yaml:"base_url"`
	DefaultProvider string `yaml:"default_provider"`
	DefaultModel    string `yaml:"default_model"`
}

// WorkspaceConfig 描述应用工作目录归档相关参数。
// ArchiveRetentionDays 控制 agent 端归档目录保留天数，0 表示不清理（仅本地调试场景使用）。
type WorkspaceConfig struct {
	ArchiveRetentionDays int `yaml:"archive_retention_days"`
}

// AgentConfig 描述 manager 与 runtime agent 的协议参数。
// HeartbeatIntervalSeconds 是约定值，agent 注册成功后回写并按此频率上报心跳。
type AgentConfig struct {
	HeartbeatIntervalSeconds int `yaml:"heartbeat_interval_seconds"`
}

// RuntimeConfig 描述 runtime node 自动注册和 manager 主动探测参数。
type RuntimeConfig struct {
	// EnrollmentSecret 是 runtime agent 自动注册时使用的共享密钥，必须是 32 字节随机值。
	EnrollmentSecret string `yaml:"enrollment_secret"`
	// Probe 控制 manager 主动探测节点双端口的周期、超时和状态切换阈值。
	Probe RuntimeProbeConfig `yaml:"probe"`
}

// RuntimeProbeConfig 控制 manager 主动探测 agent 双端口的节奏和状态阈值。
type RuntimeProbeConfig struct {
	// IntervalSeconds 是探测循环间隔，0 会在 applyDefaults 中填入默认值。
	IntervalSeconds int `yaml:"interval_seconds"`
	// TimeoutSeconds 是单次 agent 探测超时；如需避免探测堆积，应按 IntervalSeconds 保守配置。
	TimeoutSeconds int `yaml:"timeout_seconds"`
	// FailureThreshold 是主动探测连续失败多少次后把节点标记为 degraded；unreachable 由心跳超时路径负责。
	FailureThreshold int `yaml:"failure_threshold"`
	// RecoveryThreshold 是连续成功多少次后把节点恢复为 active。
	RecoveryThreshold int `yaml:"recovery_threshold"`
}

// NewAPIConfig 描述 manager 与 new-api 网关的连接参数。
// BaseURL 为空时 cmd/server 装配阶段不会构造 newapi client，
// app_initialize handler 在调用 CreateAPIKey 时会直接报错；本地调试可不配。
//
// AdminToken 必须是 new-api「个人设置 → 安全设置 → 系统访问令牌」生成的 access_token；
// 不是「令牌」页的 sk- 形式 API token，那个只能调模型推理，不能调 admin API。
//
// ModelRelayToken 是可选的 sk- 形式 API token，仅用于 /api/models 不可用时降级查询
// OpenAI 兼容 /v1/models；不参与 user、充值、token 管理等 admin API。
//
// AdminUserID 对应 new-api admin API 要求的 New-Api-User header（且必须与 access_token 持有者匹配）；
// 详见 https://www.newapi.ai/zh/docs/api/management/auth。
// 缺失时 client 调用会被 new-api 拒绝并返回 "Unauthorized, New-Api-User header not provided"。
type NewAPIConfig struct {
	// BaseURL 是 new-api 管理接口地址，通常需要包含协议和 /v1 兼容路径。
	BaseURL string `yaml:"base_url"`
	// AdminToken 是 new-api 系统访问令牌，具备管理权限，禁止落入日志或前端响应。
	AdminToken string `yaml:"admin_token"`
	// ModelRelayToken 是 OpenAI 兼容 sk- token，仅用于模型列表 fallback。
	ModelRelayToken string `yaml:"model_relay_token"`
	// AdminUserID 是 new-api 要求的 New-Api-User header 值，必须与 AdminToken 持有人一致。
	AdminUserID int64 `yaml:"admin_user_id"`
}

// Duration 让 YAML 中的 "15m"、"720h" 这类字符串显式解析为 time.Duration。
type Duration struct {
	// Duration 保存 YAML 字符串解析后的标准库持续时间值。
	time.Duration
}

// UnmarshalYAML 解析持续时间字符串，配置写错时在启动阶段 fail-fast。
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	parsed, err := time.ParseDuration(value.Value)
	if err != nil {
		return err
	}
	d.Duration = parsed
	return nil
}
