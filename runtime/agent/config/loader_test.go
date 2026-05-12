package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validAgentYAML() string {
	return `
agent:
  name: "node-1"
  advertise_host: "node-1.example"
  max_apps: 3
  data_root: "/var/lib/oc-agent"
  state_dir: "/var/lib/oc-agent/state"
  docker_socket: "/var/run/docker.sock"
  trusted_cidr: "10.0.0.0/8"
  docker_addr: ":7001"
  file_addr: ":7002"
manager:
  endpoint: "https://manager.example/api/v1"
  enrollment_secret: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
`
}

// TestLoadFile_AcceptsValidConfig 验证加载文件接受合法配置的预期行为场景。
func TestLoadFile_AcceptsValidConfig(t *testing.T) {
	path := writeTempConfig(t, validAgentYAML())

	cfg, err := LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, "/var/lib/oc-agent", cfg.Agent.DataRoot)
	require.Equal(t, "/var/lib/oc-agent/state", cfg.Agent.StateDir)
	require.Equal(t, "/var/run/docker.sock", cfg.Agent.DockerSocket)
	require.Equal(t, "node-1", cfg.Agent.Name)
	require.Equal(t, "node-1.example", cfg.Agent.AdvertiseHost)
	require.NotNil(t, cfg.Agent.MaxApps)
	require.Equal(t, int32(3), *cfg.Agent.MaxApps)
	require.Equal(t, "10.0.0.0/8", cfg.Agent.TrustedCIDR)
	require.Equal(t, ":7001", cfg.Agent.DockerAddr)
	require.Equal(t, ":7002", cfg.Agent.FileAddr)
	require.Equal(t, "https://manager.example/api/v1", cfg.Manager.Endpoint)
}

// TestLoadFile_RejectsNegativeMaxApps 验证加载文件拒绝负数最大应用的异常或拒绝路径场景。
func TestLoadFile_RejectsNegativeMaxApps(t *testing.T) {
	yaml := strings.Replace(validAgentYAML(), `  max_apps: 3`, `  max_apps: -1`, 1)
	path := writeTempConfig(t, yaml)

	_, err := LoadFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent.max_apps")
}

// TestLoadFile_AllowsUnsetMaxApps 验证加载文件允许未设置最大应用的边界条件场景。
func TestLoadFile_AllowsUnsetMaxApps(t *testing.T) {
	yaml := strings.Replace(validAgentYAML(), "  max_apps: 3\n", "", 1)
	path := writeTempConfig(t, yaml)

	cfg, err := LoadFile(path)
	require.NoError(t, err)
	require.Nil(t, cfg.Agent.MaxApps)
}

// TestLoadFile_RejectsUnknownFields 验证加载文件拒绝未知字段的异常或拒绝路径场景。
func TestLoadFile_RejectsUnknownFields(t *testing.T) {
	for name, replacement := range map[string]string{
		"agent typo":   `agnet: {}`,
		"manager typo": `managr: {}`,
	} {
		// 当前子测试覆盖表格用例中该名称对应的输入组合、边界条件和期望结果。
		t.Run(name, func(t *testing.T) {
			path := writeTempConfig(t, strings.Replace(validAgentYAML(), `agent:`, replacement, 1))
			_, err := LoadFile(path)
			require.Error(t, err)
		})
	}
}

// TestLoadFile_RejectsMalformedTrustedCIDR 验证加载文件拒绝格式错误可信CIDR的异常或拒绝路径场景。
func TestLoadFile_RejectsMalformedTrustedCIDR(t *testing.T) {
	yaml := strings.Replace(validAgentYAML(), `trusted_cidr: "10.0.0.0/8"`, `trusted_cidr: "10.0.0.0/not-a-mask"`, 1)
	path := writeTempConfig(t, yaml)

	_, err := LoadFile(path)
	require.Error(t, err)
}

// TestLoadFile_RejectsMissingRequiredFields 验证加载文件拒绝缺失必填字段的异常或拒绝路径场景。
func TestLoadFile_RejectsMissingRequiredFields(t *testing.T) {
	for name, field := range map[string]string{
		"data_root":   "agent.data_root",
		"state_dir":   "agent.state_dir",
		"docker_addr": "agent.docker_addr",
		"manager":     "manager.endpoint",
	} {
		// 当前子测试覆盖表格用例中该名称对应的输入组合、边界条件和期望结果。
		t.Run(name, func(t *testing.T) {
			yaml := validAgentYAML()
			switch field {
			case "agent.data_root":
				yaml = strings.Replace(yaml, `  data_root: "/var/lib/oc-agent"`+"\n", "", 1)
			case "agent.state_dir":
				yaml = strings.Replace(yaml, `  state_dir: "/var/lib/oc-agent/state"`+"\n", "", 1)
			case "agent.docker_addr":
				yaml = strings.Replace(yaml, `  docker_addr: ":7001"`+"\n", "", 1)
			case "manager.endpoint":
				yaml = strings.Replace(yaml, `manager:
  endpoint: "https://manager.example/api/v1"
  enrollment_secret: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
`, `manager:
  enrollment_secret: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
`, 1)
			}
			path := writeTempConfig(t, yaml)
			_, err := LoadFile(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), field)
		})
	}
}

// TestLoadFile_Defaults 验证加载文件默认值的边界条件场景。
func TestLoadFile_Defaults(t *testing.T) {
	path := writeTempConfig(t, validAgentYAML())
	cfg, err := LoadFile(path)
	require.NoError(t, err)
	assert.Equal(t, 30, cfg.Heartbeat.IntervalSeconds)
	assert.Equal(t, 5, cfg.Heartbeat.FailureLogThreshold)
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.yaml")
	err := os.WriteFile(path, []byte(content), 0o600)
	require.NoError(t, err)
	return path
}
