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
	// RAGFlow 描述 manager 后端访问 RAGFlow HTTP API 所需配置。
	RAGFlow RAGFlowConfig `yaml:"ragflow"`
	// NewAPI 描述 manager 调用 new-api 管理接口所需的凭据。
	NewAPI NewAPIConfig `yaml:"newapi"`
	// Storage 是对象存储（S3）配置；整段可选，配置则要求关键字段齐全（见 loader 校验）。
	Storage StorageConfig `yaml:"storage"`
	// Kubernetes 是 app pod 编排（client-go）配置；整段可选，启用编排时要求关键字段齐全。
	Kubernetes KubernetesConfig `yaml:"k8s"`
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
	// RuntimeImages 是平台可选的 Hermes 镜像列表，助手版本的 image_id 引用其中的 id。
	// 实例初始化/重启的运行时镜像严格由绑定的助手版本经 ResolveRuntimeImage 从此列表解析。
	RuntimeImages []RuntimeImageConfig `yaml:"runtime_images"`
	// SystemPromptTemplate 是平台级 prompt 模板，作为 input/resources/platform-rules.md
	// 的内容写入 manager 端 input 目录，由节点 oc-entrypoint 在容器启动时翻译进 SOUL.md。
	SystemPromptTemplate string `yaml:"system_prompt_template"`
	// Workspace 仅保留同名段（WorkspaceConfig 是通用类型，不绑 Hermes）。
	Workspace WorkspaceConfig `yaml:"workspace"`
	// ContainerNetworks 是 hermes 容器接入的 docker network 清单。
	// 必须包含 new-api 所在的 network（默认 docker compose project name 派生的
	// "<project>_default"，如 oc-manager_default），否则 Hermes 容器无法解析
	// "new-api" hostname → chat completions Connection error。
	// 留空时 docker 默认 bridge network，与 compose 起的 new-api 不互通。
	ContainerNetworks []string `yaml:"container_networks"`
	// ManagerRuntimeBaseURL 是 Hermes 容器内访问 manager runtime API 的地址。
	// 默认使用 compose service name，避免把浏览器 public_base_url 写进容器内部。
	ManagerRuntimeBaseURL string `yaml:"manager_runtime_base_url"`
}

// RAGFlowConfig 描述 RAGFlow HTTP API 连接信息。
// 为空表示未启用 RAGFlow-backed 知识库，业务请求应返回明确的未配置错误。
type RAGFlowConfig struct {
	// BaseURL 是 RAGFlow 服务地址，例如 http://ragflow:9380。
	BaseURL string `yaml:"base_url"`
	// APIKey 是 manager 后端调用 RAGFlow HTTP API 使用的 Bearer token。
	APIKey string `yaml:"api_key"`
	// RequestTimeout 是单次 RAGFlow HTTP 请求超时，缺省 30 秒。
	RequestTimeout Duration `yaml:"request_timeout"`
	// ChunkMethod 是自动创建 dataset 时使用的默认分块方法，缺省 naive。
	ChunkMethod string `yaml:"chunk_method"`
}

// RuntimeImageConfig 是单个可选 Hermes 镜像条目。
// id 是稳定槽位标识（助手版本存它），label 供前端展示，ref 是具体可拉取 tag。
type RuntimeImageConfig struct {
	ID    string `yaml:"id"`
	Label string `yaml:"label"`
	Ref   string `yaml:"ref"`
}

// WorkspaceConfig 描述应用工作目录归档相关参数。
// ArchiveRetentionDays 控制 agent 端归档目录保留天数，0 表示不清理（仅本地调试场景使用）。
type WorkspaceConfig struct {
	ArchiveRetentionDays int `yaml:"archive_retention_days"`
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

// KubernetesConfig 是 app pod 编排所需的 k8s 接入参数。
type KubernetesConfig struct {
	// Enabled 为 true 时 manager 用 KubernetesAdapter 编排 app（生产/本地 k3d）。
	Enabled bool `yaml:"enabled"`
	// Namespace 是 app pod 所在命名空间。
	Namespace string `yaml:"namespace"`
	// Kubeconfig 为空时用 in-cluster config；非空时用该 kubeconfig（本地 go run 指向 k3d）。
	Kubeconfig string `yaml:"kubeconfig"`
	// ImagePullSecret 是拉取私有镜像的 Secret 名（如 acr-pull）。
	ImagePullSecret string `yaml:"image_pull_secret"`
	// OpsImage 是 spec-A1 ops 镜像 ref（initContainer/sidecar）。
	OpsImage string `yaml:"ops_image"`
	// BootstrapBaseURL 是 pod 调 bootstrap 的基址（拼 /internal/apps/<id>/bootstrap）。
	BootstrapBaseURL string `yaml:"bootstrap_base_url"`
	// Resources 是 app pod 的资源 requests/limits。
	Resources K8sResources `yaml:"resources"`
	// PodProxy 为 app pod 内需直连外网的进程（如 hermes 微信平台连
	// ilinkai.weixin.qq.com）注入 HTTP(S)_PROXY/NO_PROXY。本地 k3d 无外网出口、
	// 须经宿主代理；生产 pod 有正常出口则全部留空、不注入任何代理 env。
	PodProxy K8sPodProxy `yaml:"pod_proxy"`
}

// K8sPodProxy 是注入 app pod 容器的代理环境变量（留空则不注入对应项）。
type K8sPodProxy struct {
	HTTPProxy  string `yaml:"http_proxy"`
	HTTPSProxy string `yaml:"https_proxy"`
	NoProxy    string `yaml:"no_proxy"`
}

// K8sResources 描述 pod 资源请求/上限（CPU/内存的 k8s quantity 字符串）。
type K8sResources struct {
	Requests K8sResourceSpec `yaml:"requests"`
	Limits   K8sResourceSpec `yaml:"limits"`
}

// K8sResourceSpec 是单组 CPU/内存配额。
type K8sResourceSpec struct {
	CPU    string `yaml:"cpu"`
	Memory string `yaml:"memory"`
}

// StorageConfig 是对象存储配置容器；当前仅 S3。
type StorageConfig struct {
	S3 S3StorageConfig `yaml:"s3"`
}

// S3StorageConfig 是标准 S3 接入参数（本地指向 MinIO，生产指向云 OSS）。
// 仅用标准 S3 协议，不绑定 MinIO 私有扩展。
type S3StorageConfig struct {
	// Enabled 是否启用 S3（false 时 skill 仍走本地 FS，便于无 MinIO 的最小开发）。
	Enabled bool `yaml:"enabled"`
	// Endpoint 是 S3 端点 URL。
	Endpoint string `yaml:"endpoint"`
	// Region 是区域（MinIO 任意）。
	Region string `yaml:"region"`
	// Bucket 是 app 数据 bucket。
	Bucket string `yaml:"bucket"`
	// AccessKeyID 是 manager 长期凭证。
	AccessKeyID string `yaml:"access_key_id"`
	// SecretAccessKey 与 AccessKeyID 配对的长期密钥。
	SecretAccessKey string `yaml:"secret_access_key"`
	// UsePathStyle 是否使用 path-style 寻址（MinIO 必须 true）。
	UsePathStyle bool `yaml:"use_path_style"`
	// PresignTTL 是预签名读 URL 默认有效期；空时由 applyDefaults 填默认。
	PresignTTL Duration `yaml:"presign_ttl"`
}
