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
	// AICC 描述在线智能客服的专用运行时配置，与普通实例 Hermes 镜像列表隔离。
	AICC AICCConfig `yaml:"aicc"`
	// RAGFlow 描述 manager 后端访问 RAGFlow HTTP API 所需配置。
	RAGFlow RAGFlowConfig `yaml:"ragflow"`
	// IndustryKnowledge 描述外部商业知识库上传行业库文件的固定鉴权配置。
	IndustryKnowledge IndustryKnowledgeConfig `yaml:"industry_knowledge"`
	// TransferLimit 描述 manager 面向浏览器和外部系统的文件传输单请求限速配置。
	TransferLimit TransferLimitConfig `yaml:"transfer_limit"`
	// NewAPI 描述 manager 调用 new-api 管理接口所需的凭据。
	NewAPI NewAPIConfig `yaml:"newapi"`
	// Storage 是对象存储（S3）配置；整段可选，配置则要求关键字段齐全（见 loader 校验）。
	Storage StorageConfig `yaml:"storage"`
	// Kubernetes 是 app pod 编排（client-go）配置；整段可选，启用编排时要求关键字段齐全。
	Kubernetes KubernetesConfig `yaml:"k8s"`
	// ClawHub 描述 ClawHub skill 市场 API 接入配置；整段可选，BaseURL 为空时市场降级为仅平台库。
	ClawHub ClawHubConfig `yaml:"clawhub"`
	// Captcha 是登录页工作量证明验证码配置；enabled 为 false 时整段可缺省。
	Captcha CaptchaConfig `yaml:"captcha"`
	// Logging 控制结构化日志的级别、格式与 SQL 慢查询阈值；整段可缺省，由 loader 填默认。
	Logging LoggingConfig `yaml:"logging"`
	// I18n 控制平台默认界面语言；整段可缺省，applyDefaults 回填 en。
	I18n I18nConfig `yaml:"i18n"`
	// WebPublish 是企业网站发布能力的平台级配置（基础域名/provider/凭证为 per-org，入库）。
	WebPublish WebPublishConfig `yaml:"web_publish"`
}

// I18nConfig 描述平台国际化默认行为。
// DefaultLocale 是用户未显式选择语言时的回退（也下发给登录页）；缺省 en。
type I18nConfig struct {
	// DefaultLocale 是平台默认界面语言（en/zh）；空时由 applyDefaults 回填 en。
	DefaultLocale string `yaml:"default_locale"`
}

// LoggingConfig 描述 manager 结构化日志的输出行为。
// 三个字段都可缺省，applyDefaults 会回填与历史默认一致的值（info / json / 200ms），
// 保证未配置 logging 段的旧部署行为不变。
type LoggingConfig struct {
	// Level 是日志级别：debug / info / warn / error，大小写不敏感；非法或空值回退 info。
	// 生产排故可临时调 debug 看 SQL 与外部调用的细粒度 Debug 日志。
	Level string `yaml:"level"`
	// Format 是输出格式：json（默认，容器 / ELK 可解析）或 text（本地调试人眼友好）；非法值回退 json。
	Format string `yaml:"format"`
	// SlowQueryMS 是 SQL 慢查询阈值（毫秒）：单条查询耗时超过它即从 Debug 抬到 Warn，便于定位慢查询。
	// 0 或负值视为未配置，回退默认 200ms。
	SlowQueryMS int `yaml:"slow_query_ms"`
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
	// 平台层 prompt 已固化为代码常量 DefaultSystemPromptTemplate，不再由配置提供；
	// 原 system_prompt_template 字段已移除。因 loader 用 KnownFields(true) 严格解码，
	// 任何仍残留该 key 的配置文件都必须同步删除，否则加载即报未知字段错误。
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

// AICCConfig 描述在线智能客服的运行时配置。
// RuntimeImage 是 AICC 隐藏应用唯一允许使用的镜像，不能回退到普通实例 Hermes 版本。
type AICCConfig struct {
	// RuntimeImage 必须是带 tag 或 digest 的完整客服专用镜像引用，发布时使用不可变引用。
	RuntimeImage string `yaml:"runtime_image"`
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
	// DefaultEmbeddingModel 是创建 dataset 时优先使用的 embedding 模型名。
	// 配置值必须填写 RAGFlow 控制台可见的人类模型名，而不是 RAGFlow 内部模型 ID。
	DefaultEmbeddingModel string `yaml:"default_embedding_model"`
	// EmbeddingModels 是 RAGFlow 模型列表不可用时供后端兜底展示和解析的模型清单。
	// name/provider 均使用 RAGFlow 控制台可见文本，运行期再解析成远端内部模型 ID。
	EmbeddingModels []RAGFlowEmbeddingModelConfig `yaml:"embedding_models"`
	// SelfHeal 是 RAGFlow 解析异常自愈任务参数;留空字段用内置默认。
	SelfHeal RAGFlowSelfHealConfig `yaml:"self_heal"`
}

// RAGFlowSelfHealConfig 配置 RAGFlow 解析异常自愈定时任务;所有字段可缺省,由 loader 填默认。
type RAGFlowSelfHealConfig struct {
	Interval       Duration `yaml:"interval"`        // 任务运行间隔,默认 10m
	StuckThreshold Duration `yaml:"stuck_threshold"` // running 超过此时长判卡死,默认 30m
	MaxAttempts    int      `yaml:"max_attempts"`    // 单文档自愈次数上限,默认 3
	BatchLimit     int      `yaml:"batch_limit"`     // 每轮每类处理上限,默认 100
}

// RAGFlowEmbeddingModelConfig 描述一个可选的 RAGFlow embedding 模型兜底配置。
// 这里保存的是控制台可见的人类模型名和 provider，不保存 RAGFlow 内部模型 ID。
type RAGFlowEmbeddingModelConfig struct {
	// Name 是 RAGFlow 控制台显示的 embedding 模型名，用于和远端模型列表匹配。
	Name string `yaml:"name"`
	// Label 是前端展示用标签；为空时调用方可回退显示 Name。
	Label string `yaml:"label"`
	// Provider 是 RAGFlow 控制台显示的模型供应方，用于区分同名模型来源。
	Provider string `yaml:"provider"`
}

// IndustryKnowledgeConfig 描述行业知识库外部上传入口配置。
type IndustryKnowledgeConfig struct {
	// UploadToken 是外部商业知识库上传接口要求的固定鉴权字符串；为空表示禁用外部上传入口。
	UploadToken string `yaml:"upload_token"`
}

// TransferLimitConfig 描述 manager HTTP 文件传输的单请求限速配置。
type TransferLimitConfig struct {
	// UploadBytesPerSec 是单个上传请求从客户端读入 manager 的最大字节每秒；0 表示不限速。
	UploadBytesPerSec int64 `yaml:"upload_bytes_per_sec"`
	// DownloadBytesPerSec 是单个下载请求从 manager 写给客户端的最大字节每秒；0 表示不限速。
	DownloadBytesPerSec int64 `yaml:"download_bytes_per_sec"`
}

// RuntimeImageConfig 是单个可选 Hermes 镜像条目。
// id 是稳定槽位标识（助手版本存它），label 供前端展示，ref 是具体可拉取 tag。
type RuntimeImageConfig struct {
	ID    string `yaml:"id"`
	Label string `yaml:"label"`
	Ref   string `yaml:"ref"`
	// BuiltinSkills 是该镜像内置 skill 名单，供「已安装列表」区分 builtin/self_created（对账兜底，可空）。
	BuiltinSkills []string `yaml:"builtin_skills"`
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

// WebPublishConfig 是 web-publish 能力的平台级配置。
// 企业级配置（基础域名 / DNS provider / 凭证）落 org_web_publish_config 表，不在此。
type WebPublishConfig struct {
	// IngressPublicIP 是平台 ingress 控制器的公网 IP；通配 A 记录 *.base_domain 指向它。
	IngressPublicIP string `yaml:"ingress_public_ip"`
	// IngressClassName 是通配 Ingress 的 ingressClassName，跟随环境（本地 traefik / 线上 controller）。
	IngressClassName string `yaml:"ingress_class_name"`
	// ACMEEmail 是 ACME 账户注册邮箱（证书到期通知等）。
	ACMEEmail string `yaml:"acme_email"`
	// ACMEDirectoryURL 是 ACME 目录 URL；缺省用 Let's Encrypt staging，生产需显式配生产目录。
	ACMEDirectoryURL string `yaml:"acme_directory_url"`
	// SiteServerService 是通配 Ingress 的 backend Service 名（Plan 3 部署），缺省 "site-server"。
	SiteServerService string `yaml:"site_server_service"`
	// SiteServerPort 是 backend Service 端口，缺省 80。
	SiteServerPort int32 `yaml:"site_server_port"`
	// S3Prefix 是已发布站点对象在对象存储中的顶层目录前缀（须以 / 结尾），缺省 "published-sites/"。
	// 与 app 数据共用同一 bucket 时用它按目录隔离 web-publish 数据。该前缀写入 published_sites.s3_prefix
	// 并下发 site-server，manager 删除/回收与 site-server 读取都据此，故只需在此一处配置。
	S3Prefix string `yaml:"s3_prefix"`
	// SiteSyncToken 是 site-server 轮询内部同步端点的鉴权 token（与 site-server MANAGER_SYNC_TOKEN 共享）。
	SiteSyncToken string `yaml:"site_sync_token"`
	// DevSelfSignedCert 仅供本地/dev 联调：为 true 时用自签通配证书 provisioner 取代真实
	// DNS+ACME 签发，使 web-publish 能在无公网域名 / 无真实 DNS 凭证的本地 k3d 跑通完整开通流程。
	// 默认 false——生产绝不能开启（自签证书浏览器不信任、且等于绕过真实签发链路）。
	// 仅本地 k3d 的 manager.yaml 显式置 true，启用时进程启动会打醒目 WARN 日志。
	DevSelfSignedCert bool `yaml:"dev_self_signed_cert"`
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

// ClawHubConfig 描述 ClawHub skill 市场 API 接入配置。
// BaseURL 为空时 ClawHubSource 降级（市场只显示平台库），不影响其它功能。
type ClawHubConfig struct {
	// BaseURL 是 ClawHub API 地址，例如 https://clawhubcn.com（国内站）。
	BaseURL string `yaml:"base_url"`
	// RequestTimeout 是单次请求超时，缺省 10s。
	RequestTimeout Duration `yaml:"request_timeout"`
	// CacheTTL 是搜索/列表结果的 Redis 缓存时长，缺省 5m。
	CacheTTL Duration `yaml:"cache_ttl"`
}

// CaptchaConfig 描述登录 PoW 验证码（Altcha）配置。
// enabled 为 false 时其余字段可缺省；启用时 hmac_secret 必填（见 loader 校验）。
type CaptchaConfig struct {
	// Enabled 是验证码总开关；false 时出题接口返回 204、登录跳过 PoW 校验。
	Enabled bool `yaml:"enabled"`
	// HMACSecret 是 Altcha 出题/验签的 HMAC 密钥，按密钥管理（不入 git）。
	HMACSecret string `yaml:"hmac_secret"`
	// Difficulty 是 Altcha maxNumber 难度上限；常驻取低值≈几百 ms，缺省 50000。
	Difficulty int64 `yaml:"difficulty"`
	// TTL 是挑战有效期，也是一次性消费 key 的最长 TTL，缺省 5m。
	TTL Duration `yaml:"ttl"`
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
