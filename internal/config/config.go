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
