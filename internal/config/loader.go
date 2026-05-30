// Package config 负责加载 manager YAML 配置、解析持续时间并在进程启动前执行必需项校验。
package config

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// LoadFile 从 YAML 文件读取配置，并执行启动前校验。
// 配置错误会在启动阶段直接返回，防止服务以不完整配置进入运行态。
func LoadFile(path string) (Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	// KnownFields 让拼错的 yaml key 在启动阶段报错，避免安全配置或外部依赖地址被静默忽略。
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置文件失败: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// applyDefaults 填充可选配置的默认值；所有必需配置仍由 Validate 统一校验。
func (c *Config) applyDefaults() {
	if strings.TrimSpace(c.Hermes.ManagerRuntimeBaseURL) == "" {
		c.Hermes.ManagerRuntimeBaseURL = "http://manager-api:8080"
	}
	if c.RAGFlow.RequestTimeout.Duration == 0 {
		c.RAGFlow.RequestTimeout.Duration = 30 * time.Second
	}
	if strings.TrimSpace(c.RAGFlow.ChunkMethod) == "" {
		c.RAGFlow.ChunkMethod = "naive"
	}
	// S3 启用时填预签名默认有效期 15m（pod 拉取 / 续期窗口足够，又不过长）。
	if c.Storage.S3.Enabled && c.Storage.S3.PresignTTL.Duration == 0 {
		c.Storage.S3.PresignTTL = Duration{Duration: 15 * time.Minute}
	}
	if c.Storage.S3.Enabled && strings.TrimSpace(c.Storage.S3.Region) == "" {
		c.Storage.S3.Region = "us-east-1"
	}
	// k8s 启用时填默认 namespace 与资源配额（与父设计/本地 k3d 一致）。
	if c.Kubernetes.Enabled {
		if strings.TrimSpace(c.Kubernetes.Namespace) == "" {
			c.Kubernetes.Namespace = "oc-apps"
		}
		if c.Kubernetes.Resources.Requests.CPU == "" {
			c.Kubernetes.Resources.Requests.CPU = "250m"
		}
		if c.Kubernetes.Resources.Requests.Memory == "" {
			c.Kubernetes.Resources.Requests.Memory = "512Mi"
		}
		if c.Kubernetes.Resources.Limits.CPU == "" {
			c.Kubernetes.Resources.Limits.CPU = "1"
		}
		if c.Kubernetes.Resources.Limits.Memory == "" {
			c.Kubernetes.Resources.Limits.Memory = "2Gi"
		}
	}
}

// Validate 校验启动必需配置。
// 这里只检查工程基线所需字段；后续模块新增外部依赖时必须同步扩展校验和测试。
func (c Config) Validate() error {
	var missing []string
	if strings.TrimSpace(c.App.HTTPAddr) == "" {
		missing = append(missing, "app.http_addr")
	}
	if strings.TrimSpace(c.App.DataRoot) == "" {
		missing = append(missing, "app.data_root")
	}
	if strings.TrimSpace(c.Database.URL) == "" {
		missing = append(missing, "database.url")
	}
	if strings.TrimSpace(c.Redis.Addr) == "" {
		missing = append(missing, "redis.addr")
	}
	if c.Auth.AccessTokenTTL.Duration <= 0 {
		missing = append(missing, "auth.access_token_ttl")
	}
	if c.Auth.RefreshTokenTTL.Duration <= 0 {
		missing = append(missing, "auth.refresh_token_ttl")
	}
	if strings.TrimSpace(c.Auth.JWTAccessSecret) == "" {
		missing = append(missing, "auth.jwt_access_secret")
	}
	if strings.TrimSpace(c.Auth.JWTRefreshSecret) == "" {
		missing = append(missing, "auth.jwt_refresh_secret")
	}
	if strings.TrimSpace(c.Auth.CSRFSecret) == "" {
		missing = append(missing, "auth.csrf_secret")
	}
	if strings.TrimSpace(c.Security.MasterKey) == "" {
		missing = append(missing, "security.master_key")
	}
	if strings.TrimSpace(c.Hermes.SystemPromptTemplate) == "" {
		missing = append(missing, "hermes.system_prompt_template")
	}
	if len(missing) > 0 {
		return fmt.Errorf("缺少必需配置: %s", strings.Join(missing, ", "))
	}
	if err := validateMasterKey(c.Security.MasterKey); err != nil {
		return err
	}
	if err := ValidateRuntimeImages(c.Hermes.RuntimeImages); err != nil {
		return err
	}
	if err := c.RAGFlow.validate(); err != nil {
		return err
	}
	// S3 启用时关键字段必须齐全，缺失 fail-fast（避免运行期才暴露配置缺漏）。
	if c.Storage.S3.Enabled {
		if strings.TrimSpace(c.Storage.S3.Endpoint) == "" || strings.TrimSpace(c.Storage.S3.Bucket) == "" ||
			strings.TrimSpace(c.Storage.S3.AccessKeyID) == "" || strings.TrimSpace(c.Storage.S3.SecretAccessKey) == "" {
			return fmt.Errorf("storage.s3 已启用但 endpoint/bucket/access_key_id/secret_access_key 不完整")
		}
		if strings.TrimSpace(c.Storage.S3.STSRoleARN) == "" {
			return fmt.Errorf("storage.s3 已启用但缺少 sts_role_arn")
		}
	}
	// k8s 启用时关键字段必须齐全，缺失 fail-fast。
	if c.Kubernetes.Enabled {
		if strings.TrimSpace(c.Kubernetes.OpsImage) == "" || strings.TrimSpace(c.Kubernetes.BootstrapBaseURL) == "" {
			return fmt.Errorf("k8s 已启用但 ops_image / bootstrap_base_url 不完整")
		}
	}
	// Hermes 时代模板不再需要 {{workspace_dir}} 等 legacy OpenClaw 专属占位符，
	// 仅需非空即可（上方 missing 检查已覆盖）。
	return nil
}

// validateMasterKey 校验 base64 解码后的根密钥长度是否为 32 字节。
// 32 字节对应 AES-256-GCM 的 key 长度；任何偏差都意味着部署侧密钥生成方式不正确，必须 fail-fast。
func validateMasterKey(value string) error {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return fmt.Errorf("security.master_key 必须是合法 base64: %w", err)
	}
	if len(raw) != 32 {
		return fmt.Errorf("security.master_key 解码后必须是 32 字节，实际 %d", len(raw))
	}
	return nil
}

// validate 校验 RAGFlow 外部依赖配置。
// RAGFlow 在本地开发可不启用；一旦配置任意连接字段，就必须同时提供地址和 API key。
func (r RAGFlowConfig) validate() error {
	baseURL := strings.TrimSpace(r.BaseURL)
	apiKey := strings.TrimSpace(r.APIKey)
	if baseURL == "" && apiKey == "" {
		return nil
	}
	if baseURL == "" {
		return fmt.Errorf("缺少必需配置: ragflow.base_url")
	}
	if apiKey == "" {
		return fmt.Errorf("缺少必需配置: ragflow.api_key")
	}
	parsed, err := url.ParseRequestURI(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("ragflow.base_url 必须是合法 URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("ragflow.base_url 必须使用 http 或 https 协议")
	}
	if r.RequestTimeout.Duration <= 0 {
		return fmt.Errorf("ragflow.request_timeout 必须为正持续时间")
	}
	return nil
}
