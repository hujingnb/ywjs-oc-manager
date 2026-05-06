package config

import (
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
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置文件失败: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
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
	if strings.TrimSpace(c.OpenClaw.SystemPromptTemplate) == "" {
		missing = append(missing, "openclaw.system_prompt_template")
	}
	if len(missing) > 0 {
		return fmt.Errorf("缺少必需配置: %s", strings.Join(missing, ", "))
	}
	if err := validateMasterKey(c.Security.MasterKey); err != nil {
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
