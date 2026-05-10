package config

import (
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是 manager 进程启动所需的完整配置。
// 这里仅放入工程基线阶段需要校验的字段；后续业务模块会在保持兼容的前提下继续扩展。
type Config struct {
	App      AppConfig      `yaml:"app"`
	Database DatabaseConfig `yaml:"database"`
	Redis    RedisConfig    `yaml:"redis"`
	Auth     AuthConfig     `yaml:"auth"`
	Security SecurityConfig `yaml:"security"`
	OpenClaw OpenClawConfig `yaml:"openclaw"`
	Agent    AgentConfig    `yaml:"agent"`
	Runtime  RuntimeConfig  `yaml:"runtime"`
	NewAPI   NewAPIConfig   `yaml:"newapi"`
}

// AppConfig 描述 manager API 进程自身的运行参数。
// HTTPAddr 必须显式配置，避免容器内外端口不一致时静默监听错误地址。
type AppConfig struct {
	Env           string `yaml:"env"`
	HTTPAddr      string `yaml:"http_addr"`
	PublicBaseURL string `yaml:"public_base_url"`
	DataRoot      string `yaml:"data_root"`
	// KnowledgeRoot 是知识库主副本根目录（manager 端"主拷贝"，由 worker 同步到各 runtime node）。
	// 此前由 OCM_KNOWLEDGE_ROOT 环境变量提供，现统一收口到 yaml；为空启动时 fail-fast。
	// 路径下结构：orgs/<org_id>/...、apps/<app_id>/...，由 files.SafeRoot 沙箱化。
	KnowledgeRoot  string        `yaml:"knowledge_root"`
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
	Addr      string `yaml:"addr"`
	Password  string `yaml:"password"`
	DB        int    `yaml:"db"`
	KeyPrefix string `yaml:"key_prefix"`
}

// AuthConfig 描述后台登录和令牌签发配置。
// access token 用于短期 API 认证，refresh token 只保存 hash 并用于续期。
type AuthConfig struct {
	CookieDomain     string   `yaml:"cookie_domain"`
	AccessTokenTTL   Duration `yaml:"access_token_ttl"`
	RefreshTokenTTL  Duration `yaml:"refresh_token_ttl"`
	JWTAccessSecret  string   `yaml:"jwt_access_secret"`
	JWTRefreshSecret string   `yaml:"jwt_refresh_secret"`
	CSRFSecret       string   `yaml:"csrf_secret"`
}

// SecurityConfig 描述敏感字段加解密所需的根密钥。
// MasterKey 必须是 base64 编码的 32 字节随机数，缺失或长度不符时启动 fail-fast。
type SecurityConfig struct {
	MasterKey string `yaml:"master_key"`
}

// OpenClawConfig 描述 OpenClaw runtime 容器及人设模板相关配置。
// SystemPromptTemplate 必须包含 {{workspace_dir}} / {{knowledge_org_dir}} / {{knowledge_app_dir}}
// 三个占位符，避免第二/三层人设拼接时丢失目录上下文。
type OpenClawConfig struct {
	RuntimeImage         string            `yaml:"runtime_image"`
	SystemPromptTemplate string            `yaml:"system_prompt_template"`
	Workspace            WorkspaceConfig   `yaml:"workspace"`
	LLM                  OpenClawLLMConfig `yaml:"llm"`
	// ContainerNetworks 控制 manager 创建 OpenClaw 容器时连接哪些 docker network。
	// 必须包含 new-api 所在的 network（默认 docker compose project name 派生的
	// "<project>_default"，如 oc-manager_default），否则 OpenClaw 容器无法解析
	// "new-api" hostname → chat completions Connection error。
	// 留空时 docker 默认 bridge network，与 compose 起的 new-api 不互通。
	ContainerNetworks []string `yaml:"container_networks"`
}

// OpenClawLLMConfig 描述 OpenClaw 容器内嵌 pi-coding-agent 调模型用的配置。
//
//   - BaseURL：OpenAI 兼容 endpoint，OpenClaw 容器从 docker network 看到的 new-api 地址，
//     必须含 /v1 路径后缀；注入为 OPENAI_BASE_URL 环境变量。
//   - DefaultProvider / DefaultModel：写入容器内 /root/.pi/agent/settings.json，
//     pi-coding-agent 用作默认 provider/model；缺失时 OpenClaw 默认 openai/gpt-5.5
//     无法路由到本地 ollama。
//
// 容器实际用的 OPENAI_API_KEY 来自 manager 替每个应用通过 new-api `POST /api/token/:id/key`
// 拉到的完整 sk-，加密落 apps.newapi_key_ciphertext 后注入；不再有"全局共享 sk-"的配置项。
type OpenClawLLMConfig struct {
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
	EnrollmentSecret string             `yaml:"enrollment_secret"`
	Probe            RuntimeProbeConfig `yaml:"probe"`
}

// RuntimeProbeConfig 控制 manager 主动探测 agent 双端口的节奏和状态阈值。
type RuntimeProbeConfig struct {
	IntervalSeconds  int `yaml:"interval_seconds"`
	TimeoutSeconds   int `yaml:"timeout_seconds"`
	FailureThreshold int `yaml:"failure_threshold"`
	RecoveryThreshold int `yaml:"recovery_threshold"`
}

// NewAPIConfig 描述 manager 与 new-api 网关的连接参数。
// BaseURL 为空时 cmd/server 装配阶段不会构造 newapi client，
// app_initialize handler 在调用 CreateAPIKey 时会直接报错；本地调试可不配。
//
// AdminToken 必须是 new-api「个人设置 → 安全设置 → 系统访问令牌」生成的 access_token；
// 不是「令牌」页的 sk- 形式 API token，那个只能调模型推理，不能调 admin API。
//
// AdminUserID 对应 new-api admin API 要求的 New-Api-User header（且必须与 access_token 持有者匹配）；
// 详见 https://www.newapi.ai/zh/docs/api/management/auth。
// 缺失时 client 调用会被 new-api 拒绝并返回 "Unauthorized, New-Api-User header not provided"。
type NewAPIConfig struct {
	BaseURL     string `yaml:"base_url"`
	AdminToken  string `yaml:"admin_token"`
	AdminUserID int64  `yaml:"admin_user_id"`
}

// Duration 让 YAML 中的 "15m"、"720h" 这类字符串显式解析为 time.Duration。
type Duration struct {
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
