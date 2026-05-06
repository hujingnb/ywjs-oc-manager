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
	NewAPI   NewAPIConfig   `yaml:"newapi"`
}

// AppConfig 描述 manager API 进程自身的运行参数。
// HTTPAddr 必须显式配置，避免容器内外端口不一致时静默监听错误地址。
type AppConfig struct {
	Env            string        `yaml:"env"`
	HTTPAddr       string        `yaml:"http_addr"`
	PublicBaseURL  string        `yaml:"public_base_url"`
	DataRoot       string        `yaml:"data_root"`
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
// MasterKey 必须是 base64 编码的 32 字节随机数，缺失或长度不符时启动 fail-fast；
// 不在配置文件落明文，由部署侧通过 ${MASTER_KEY} 环境变量注入。
type SecurityConfig struct {
	MasterKey string `yaml:"master_key"`
}

// OpenClawConfig 描述 OpenClaw runtime 容器及人设模板相关配置。
// SystemPromptTemplate 必须包含 {{workspace_dir}} / {{knowledge_org_dir}} / {{knowledge_app_dir}}
// 三个占位符，避免第二/三层人设拼接时丢失目录上下文。
type OpenClawConfig struct {
	RuntimeImage         string          `yaml:"runtime_image"`
	SystemPromptTemplate string          `yaml:"system_prompt_template"`
	Workspace            WorkspaceConfig `yaml:"workspace"`
	LLM                  OpenClawLLMConfig `yaml:"llm"`
	// ContainerNetworks 控制 manager 创建 OpenClaw 容器时连接哪些 docker network。
	// 必须包含 new-api 所在的 network（默认 docker compose project name 派生的
	// "<project>_default"，如 oc-manager_default），否则 OpenClaw 容器无法解析
	// "new-api" hostname → chat completions Connection error。
	// 留空时 docker 默认 bridge network，与 compose 起的 new-api 不互通。
	ContainerNetworks    []string        `yaml:"container_networks"`
}

// OpenClawLLMConfig 描述 OpenClaw 容器内嵌 pi-coding-agent 调模型用的配置。
//
//   - BaseURL：OpenAI 兼容 endpoint，OpenClaw 容器从 docker network 看到的 new-api 地址，
//     必须含 /v1 路径后缀；注入为 OPENAI_BASE_URL 环境变量。
//   - DefaultProvider / DefaultModel：写入容器内 /root/.pi/agent/settings.json，
//     pi-coding-agent 用作默认 provider/model；缺失时 OpenClaw 默认 openai/gpt-5.5
//     无法路由到本地 ollama。
//
// 当三项任一为空时，buildContainerSpec 不会写入 settings.json，OpenClaw 走默认配置；
// 部署侧应保证三项都填上，否则模型调用会失败。
type OpenClawLLMConfig struct {
	BaseURL         string             `yaml:"base_url"`
	DefaultProvider string             `yaml:"default_provider"`
	DefaultModel    string             `yaml:"default_model"`
	OpenAICompat    OpenAICompatConfig `yaml:"openai_compat"`
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

// OpenAICompatConfig 描述 OpenClaw 容器调模型用的 OpenAI 兼容 API key。
//
// 设计动机：new-api v1 的 admin POST /api/token/ 不返回新建 token 的完整 key（只
// 返回 success），GET 也只返回 truncated 18 字符前缀。manager 通过 admin API 完全
// 拿不到真 sk- 形式 token，导致注入容器的 OPENAI_API_KEY 不可用，下游所有 chat
// completions 401。
//
// dev / 单机部署的解法：ops 在 new-api 后台手工创建一个 sk- token，配进本字段；
// 所有应用容器共用此 token 调 OpenAI 兼容 endpoint（spec §5.3 的"每应用一 api_key"
// 隔离暂时降级，等 new-api 提供"创建后短窗口可读完整 key"的 API 再恢复）。
//
// 仍然每应用独立调 admin POST /api/token/ 创建 token 记录（用于未来按 token id
// 拆分用量统计）；但容器实际用的 OPENAI_API_KEY 走本字段全局值。
type OpenAICompatConfig struct {
	APIKey string `yaml:"api_key"`
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
