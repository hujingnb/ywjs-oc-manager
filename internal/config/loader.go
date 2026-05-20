// Package config 负责加载 manager YAML 配置、解析持续时间并在进程启动前执行必需项校验。
package config

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

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
	if c.Runtime.Probe.IntervalSeconds == 0 {
		c.Runtime.Probe.IntervalSeconds = 60
	}
	if c.Runtime.Probe.TimeoutSeconds == 0 {
		c.Runtime.Probe.TimeoutSeconds = 3
	}
	if c.Runtime.Probe.FailureThreshold == 0 {
		c.Runtime.Probe.FailureThreshold = 3
	}
	if c.Runtime.Probe.RecoveryThreshold == 0 {
		c.Runtime.Probe.RecoveryThreshold = 2
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
	if strings.TrimSpace(c.App.KnowledgeRoot) == "" {
		missing = append(missing, "app.knowledge_root")
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
	if strings.TrimSpace(c.Runtime.EnrollmentSecret) == "" {
		missing = append(missing, "runtime.enrollment_secret")
	}
	if strings.TrimSpace(c.Hermes.SystemPromptTemplate) == "" {
		missing = append(missing, "hermes.system_prompt_template")
	}
	if strings.TrimSpace(c.Hermes.RuntimeImage) == "" {
		missing = append(missing, "hermes.runtime_image")
	}
	if len(missing) > 0 {
		return fmt.Errorf("缺少必需配置: %s", strings.Join(missing, ", "))
	}
	if err := validateMasterKey(c.Security.MasterKey); err != nil {
		return err
	}
	if err := validateEnrollmentSecret(c.Runtime.EnrollmentSecret); err != nil {
		return err
	}
	if err := validateHermesRuntimeImage(c.Hermes.RuntimeImage); err != nil {
		return err
	}
	if err := c.Runtime.Probe.validate(); err != nil {
		return err
	}
	// Hermes 时代模板不再需要 {{workspace_dir}} 等 legacy OpenClaw 专属占位符，
	// 仅需非空即可（上方 missing 检查已覆盖）。
	return nil
}

// validateHermesRuntimeImage 校验 Hermes 镜像必须固定到可复现引用。
//
// 允许 name:v<完整版本号>[-suffix] 或 name@sha256:digest；
// 禁止 main/latest/stable/2.1 这类浮动或版本族 tag，避免 manager 运行时路径绕过
// Makefile 的版本化镜像约束。
func validateHermesRuntimeImage(value string) error {
	image := strings.TrimSpace(value)
	if strings.ContainsAny(image, " \t\r\n") {
		return fmt.Errorf("hermes.runtime_image 不能包含空白字符")
	}
	if strings.Contains(image, "@sha256:") {
		parts := strings.Split(image, "@sha256:")
		if len(parts) != 2 || parts[0] == "" || !isHexDigest(parts[1]) {
			return fmt.Errorf("hermes.runtime_image digest 必须是 name@sha256:<64位hex>")
		}
		return nil
	}

	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon <= lastSlash || lastColon == len(image)-1 {
		return fmt.Errorf("hermes.runtime_image 必须固定到具体 tag 或 sha256 digest")
	}
	return validateHermesRuntimeImageTag(image[lastColon+1:])
}

// validateHermesRuntimeImageTag 校验 Docker tag 语法并拒绝浮动 tag。
func validateHermesRuntimeImageTag(tag string) error {
	if len(tag) > 128 {
		return fmt.Errorf("hermes.runtime_image tag 不能超过 128 字符")
	}
	if strings.HasPrefix(tag, ".") || strings.HasPrefix(tag, "-") {
		return fmt.Errorf("hermes.runtime_image tag 不能以 . 或 - 开头")
	}
	for _, r := range tag {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-' {
			continue
		}
		return fmt.Errorf("hermes.runtime_image tag 包含非法字符: %q", r)
	}
	lower := strings.ToLower(tag)
	switch {
	case lower == "main", lower == "master", lower == "latest", lower == "dev":
		return fmt.Errorf("hermes.runtime_image tag 不能使用浮动版本: %s", tag)
	case strings.Contains(lower, "hermes-main"):
		return fmt.Errorf("hermes.runtime_image tag 不能继续使用旧 variant 名称: %s", tag)
	}
	if !hasConcreteHermesVersionPrefix(tag) {
		return fmt.Errorf("hermes.runtime_image tag 必须以具体 Hermes 版本号开头: %s", tag)
	}
	return nil
}

// hasConcreteHermesVersionPrefix 校验 tag 是否以 v<major>.<minor>.<patch> 开头。
//
// Hermes 当前 version.txt 使用 v2026.5.16 这类完整版本号；生产镜像会继续追加
// 构建时间戳，本地 dev stub 会追加 -dev。仅允许这种完整版本前缀，避免 stable、
// prod、nightly、2 或 2.1 等可漂移引用进入运行路径。
func hasConcreteHermesVersionPrefix(tag string) bool {
	if !strings.HasPrefix(tag, "v") {
		return false
	}
	rest := tag[1:]
	for i := 0; i < 3; i++ {
		digitCount := 0
		for len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
			digitCount++
			rest = rest[1:]
		}
		if digitCount == 0 {
			return false
		}
		if i < 2 {
			if len(rest) == 0 || rest[0] != '.' {
				return false
			}
			rest = rest[1:]
			continue
		}
	}
	if rest == "" {
		return true
	}
	if len(rest) == 1 {
		return false
	}
	return rest[0] == '-' || rest[0] == '_' || rest[0] == '.'
}

// isHexDigest 校验 sha256 digest 是否为 64 位十六进制字符串。
func isHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
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

// validateEnrollmentSecret 校验自动注册共享密钥，要求与 master_key 一样是 32 字节随机值。
func validateEnrollmentSecret(value string) error {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return fmt.Errorf("runtime.enrollment_secret 必须是合法 base64: %w", err)
	}
	if len(raw) != 32 {
		return fmt.Errorf("runtime.enrollment_secret 解码后必须是 32 字节，实际 %d", len(raw))
	}
	return nil
}

// validate 校验 probe 配置；0 表示使用默认值，负数或缺失阈值会 fail-fast。
func (p RuntimeProbeConfig) validate() error {
	if p.IntervalSeconds <= 0 || p.TimeoutSeconds <= 0 || p.FailureThreshold <= 0 || p.RecoveryThreshold <= 0 {
		return fmt.Errorf("runtime.probe.* 必须为正整数")
	}
	return nil
}
