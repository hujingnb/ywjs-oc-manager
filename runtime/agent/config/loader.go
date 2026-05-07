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
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// applyDefaults 在校验前给可选字段设置默认值。
// 默认值集中在这里维护，避免 Validate 既校验又改写状态导致语义混乱。
func (c *Config) applyDefaults() {
	if c.Heartbeat.IntervalSeconds == 0 {
		c.Heartbeat.IntervalSeconds = 30
	}
	if c.Heartbeat.FailureLogThreshold == 0 {
		c.Heartbeat.FailureLogThreshold = 5
	}
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
	if c.Heartbeat.IntervalSeconds < 5 {
		return fmt.Errorf("heartbeat.interval_seconds 不得小于 5（当前 %d）", c.Heartbeat.IntervalSeconds)
	}
	// manager 三字段一致性：必须同时填齐或同时为空。允许空场景是因为
	// 节点首次 register 之前 yaml 还没有 node_id / agent_token，agent 进程不发心跳。
	mgrFilled := 0
	if strings.TrimSpace(c.Manager.Endpoint) != "" {
		mgrFilled++
	}
	if strings.TrimSpace(c.Manager.NodeID) != "" {
		mgrFilled++
	}
	if strings.TrimSpace(c.Manager.AgentToken) != "" {
		mgrFilled++
	}
	if mgrFilled != 0 && mgrFilled != 3 {
		return fmt.Errorf("manager 段必须三字段全填或全空（endpoint / node_id / agent_token），当前填了 %d 个", mgrFilled)
	}
	return nil
}
