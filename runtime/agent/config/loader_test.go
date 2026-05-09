package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/assert"
)

func validAgentYAML() string {
	return `
agent:
  data_root: "/var/lib/oc-agent"
  state_dir: "/var/lib/oc-agent/state"
  docker_socket: "/var/run/docker.sock"
  token: "secret"
  trusted_cidr: "10.0.0.0/8"
  docker_addr: ":7001"
  file_addr: ":7002"
`
}

func TestLoadFile_AcceptsValidConfig(t *testing.T) {
	path := writeTempConfig(t, validAgentYAML())

	cfg, err := LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, "/var/lib/oc-agent", cfg.Agent.DataRoot)
	require.Equal(t, "/var/lib/oc-agent/state", cfg.Agent.StateDir)
	require.Equal(t, "/var/run/docker.sock", cfg.Agent.DockerSocket)
	require.Equal(t, "secret", cfg.Agent.Token)
	require.Equal(t, "10.0.0.0/8", cfg.Agent.TrustedCIDR)
	require.Equal(t, ":7001", cfg.Agent.DockerAddr)
	require.Equal(t, ":7002", cfg.Agent.FileAddr)
}

func TestLoadFile_AllowsEmptyTokenAndTrustedCIDR(t *testing.T) {
	yaml := strings.ReplaceAll(validAgentYAML(), `token: "secret"`, `token: ""`)
	yaml = strings.ReplaceAll(yaml, `trusted_cidr: "10.0.0.0/8"`, `trusted_cidr: ""`)
	path := writeTempConfig(t, yaml)

	cfg, err := LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, "", cfg.Agent.Token)
	require.Equal(t, "", cfg.Agent.TrustedCIDR)
}

func TestLoadFile_RejectsUnknownFields(t *testing.T) {
	for name, replacement := range map[string]string{
		"token typo":        `tokne: "secret"`,
		"trusted_cidr typo": `trusted_cidrs: "10.0.0.0/8"`,
	} {
		t.Run(name, func(t *testing.T) {
			yaml := strings.Replace(validAgentYAML(), `token: "secret"`, replacement, 1)
			path := writeTempConfig(t, yaml)

			_, err := LoadFile(path)
			require.Error(t, err)
		})
	}
}

func TestLoadFile_RejectsMalformedTrustedCIDR(t *testing.T) {
	yaml := strings.Replace(validAgentYAML(), `trusted_cidr: "10.0.0.0/8"`, `trusted_cidr: "10.0.0.0/not-a-mask"`, 1)
	path := writeTempConfig(t, yaml)

	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "agent.trusted_cidr") {
		t.Fatalf("LoadFile() error = %v, want agent.trusted_cidr", err)
	}
}

func TestLoadFile_RejectsMissingRequiredFields(t *testing.T) {
	for name, tc := range map[string]struct {
		line  string
		field string
	}{
		"data_root":     {line: `  data_root: "/var/lib/oc-agent"` + "\n", field: "agent.data_root"},
		"state_dir":     {line: `  state_dir: "/var/lib/oc-agent/state"` + "\n", field: "agent.state_dir"},
		"docker_socket": {line: `  docker_socket: "/var/run/docker.sock"` + "\n", field: "agent.docker_socket"},
		"docker_addr":   {line: `  docker_addr: ":7001"` + "\n", field: "agent.docker_addr"},
		"file_addr":     {line: `  file_addr: ":7002"` + "\n", field: "agent.file_addr"},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeTempConfig(t, strings.Replace(validAgentYAML(), tc.line, "", 1))

			_, err := LoadFile(path)
			if err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("LoadFile() error = %v, want %s", err, tc.field)
			}
		})
	}
}

func TestLoadFile_HeartbeatDefaults(t *testing.T) {
	// heartbeat 段未声明时应填默认值（30s 间隔、阈值 5）
	path := writeTempConfig(t, validAgentYAML())

	cfg, err := LoadFile(path)
	require.NoError(t, err)
	assert.Equal(t, 30, cfg.Heartbeat.IntervalSeconds)
	assert.Equal(t, 5, cfg.Heartbeat.FailureLogThreshold)
}

func TestLoadFile_HeartbeatBelowMinimum(t *testing.T) {
	// 显式给一个小于 5 的间隔应被拒绝，避免运行期被反复 burst 请求
	yaml := validAgentYAML() + `
heartbeat:
  interval_seconds: 1
`
	path := writeTempConfig(t, yaml)

	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "heartbeat.interval_seconds") {
		t.Fatalf("LoadFile() error = %v, want heartbeat.interval_seconds", err)
	}
}

func TestLoadFile_ManagerOptional(t *testing.T) {
	// manager 段完全不出现：允许通过，agent 进程后续不会启动心跳
	path := writeTempConfig(t, validAgentYAML())

	cfg, err := LoadFile(path)
	require.NoError(t, err)
	if cfg.Manager.Endpoint != "" || cfg.Manager.NodeID != "" || cfg.Manager.AgentToken != "" {
		t.Fatalf("manager 段应保持全空, got %+v", cfg.Manager)
	}
}

func TestLoadFile_ManagerPartialRejected(t *testing.T) {
	// manager 三字段必须全填或全空，避免 ops 漏填导致悄悄不发心跳
	yaml := validAgentYAML() + `
manager:
  endpoint: "https://manager.example/api/v1"
  agent_token: "tok"
`
	path := writeTempConfig(t, yaml)

	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "manager") {
		t.Fatalf("LoadFile() error = %v, want manager 三字段一致性错误", err)
	}
}

func TestLoadFile_ManagerComplete(t *testing.T) {
	// manager 三字段齐全：允许通过，agent 进程会启动心跳
	yaml := validAgentYAML() + `
manager:
  endpoint: "https://manager.example/api/v1"
  node_id: "00000000-0000-0000-0000-000000000001"
  agent_token: "tok"
`
	path := writeTempConfig(t, yaml)

	cfg, err := LoadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "00000000-0000-0000-0000-000000000001", cfg.Manager.NodeID)
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.yaml")
	err := os.WriteFile(path, []byte(content), 0o600)
	require.NoError(t, err)
	return path
}
