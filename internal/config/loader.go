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
	if strings.TrimSpace(c.OpenClaw.SystemPromptTemplate) == "" {
		missing = append(missing, "openclaw.system_prompt_template")
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
	if err := c.Runtime.Probe.validate(); err != nil {
		return err
	}
	if err := validatePromptTemplate(c.OpenClaw.SystemPromptTemplate); err != nil {
		return err
	}
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

// validatePromptTemplate 强制 system prompt 模板包含三类目录占位符。
// 否则 prompt 拼接时会丢失对工作目录与知识库目录的引用，OpenClaw 容器无法正确读写文件。
func validatePromptTemplate(template string) error {
	for _, placeholder := range []string{"{{workspace_dir}}", "{{knowledge_org_dir}}", "{{knowledge_app_dir}}"} {
		if !strings.Contains(template, placeholder) {
			return fmt.Errorf("openclaw.system_prompt_template 必须包含占位符 %s", placeholder)
		}
	}
	return nil
}
