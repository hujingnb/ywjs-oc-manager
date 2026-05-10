package config

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadFile 从 YAML 文件读取 runtime agent 配置，并执行启动前校验。
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
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// applyDefaults 为可选字段填默认值。
func (c *Config) applyDefaults() {
	if c.Heartbeat.IntervalSeconds == 0 {
		c.Heartbeat.IntervalSeconds = 30
	}
	if c.Heartbeat.FailureLogThreshold == 0 {
		c.Heartbeat.FailureLogThreshold = 5
	}
}

// Validate 校验 runtime agent 启动必需配置。
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
	if strings.TrimSpace(c.Manager.Endpoint) == "" {
		missing = append(missing, "manager.endpoint")
	}
	if strings.TrimSpace(c.Manager.EnrollmentSecret) == "" {
		missing = append(missing, "manager.enrollment_secret")
	}
	if len(missing) > 0 {
		return fmt.Errorf("缺少 agent 必需配置: %s", strings.Join(missing, ", "))
	}
	if trustedCIDR := strings.TrimSpace(c.Agent.TrustedCIDR); trustedCIDR != "" {
		if _, _, err := net.ParseCIDR(trustedCIDR); err != nil {
			return fmt.Errorf("agent.trusted_cidr 格式无效: %w", err)
		}
	}
	if c.Heartbeat.IntervalSeconds < 5 {
		return fmt.Errorf("heartbeat.interval_seconds 不得小于 5（当前 %d）", c.Heartbeat.IntervalSeconds)
	}
	if err := validateEnrollmentSecret(c.Manager.EnrollmentSecret); err != nil {
		return err
	}
	if c.Agent.MaxApps != nil && *c.Agent.MaxApps < 0 {
		return fmt.Errorf("agent.max_apps 不能为负（当前 %d）", *c.Agent.MaxApps)
	}
	return nil
}

// validateEnrollmentSecret 校验共享 enrollment secret 的长度与编码格式。
func validateEnrollmentSecret(value string) error {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return fmt.Errorf("manager.enrollment_secret 必须是合法 base64: %w", err)
	}
	if len(raw) != 32 {
		return fmt.Errorf("manager.enrollment_secret 解码后必须是 32 字节，实际 %d", len(raw))
	}
	return nil
}
