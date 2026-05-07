package config

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadFile 从 YAML 文件读取 runtime agent 配置，并执行启动前校验。
// 配置错误会在启动阶段直接返回，防止 agent 以不完整配置进入运行态。
func LoadFile(path string) (Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("读取 agent 配置文件失败: %w", err)
	}

	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("解析 agent 配置文件失败: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate 校验 runtime agent 启动必需配置。
// token 与 trusted_cidr 是可选安全收紧项，允许为空以支持本地调试和分阶段启用。
func (c Config) Validate() error {
	var missing []string
	if strings.TrimSpace(c.Agent.DataRoot) == "" {
		missing = append(missing, "agent.data_root")
	}
	if strings.TrimSpace(c.Agent.StateDir) == "" {
		missing = append(missing, "agent.state_dir")
	}
	if strings.TrimSpace(c.Agent.DockerSocket) == "" {
		missing = append(missing, "agent.docker_socket")
	}
	if strings.TrimSpace(c.Agent.DockerAddr) == "" {
		missing = append(missing, "agent.docker_addr")
	}
	if strings.TrimSpace(c.Agent.FileAddr) == "" {
		missing = append(missing, "agent.file_addr")
	}
	if len(missing) > 0 {
		return fmt.Errorf("缺少 agent 必需配置: %s", strings.Join(missing, ", "))
	}
	if trustedCIDR := strings.TrimSpace(c.Agent.TrustedCIDR); trustedCIDR != "" {
		if _, _, err := net.ParseCIDR(trustedCIDR); err != nil {
			return fmt.Errorf("agent.trusted_cidr 格式无效: %w", err)
		}
	}
	return nil
}
