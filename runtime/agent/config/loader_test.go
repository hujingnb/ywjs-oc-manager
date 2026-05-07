package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if cfg.Agent.DataRoot != "/var/lib/oc-agent" {
		t.Fatalf("data_root = %q, want /var/lib/oc-agent", cfg.Agent.DataRoot)
	}
	if cfg.Agent.StateDir != "/var/lib/oc-agent/state" {
		t.Fatalf("state_dir = %q, want /var/lib/oc-agent/state", cfg.Agent.StateDir)
	}
	if cfg.Agent.DockerSocket != "/var/run/docker.sock" {
		t.Fatalf("docker_socket = %q, want /var/run/docker.sock", cfg.Agent.DockerSocket)
	}
	if cfg.Agent.Token != "secret" {
		t.Fatalf("token = %q, want secret", cfg.Agent.Token)
	}
	if cfg.Agent.TrustedCIDR != "10.0.0.0/8" {
		t.Fatalf("trusted_cidr = %q, want 10.0.0.0/8", cfg.Agent.TrustedCIDR)
	}
	if cfg.Agent.DockerAddr != ":7001" {
		t.Fatalf("docker_addr = %q, want :7001", cfg.Agent.DockerAddr)
	}
	if cfg.Agent.FileAddr != ":7002" {
		t.Fatalf("file_addr = %q, want :7002", cfg.Agent.FileAddr)
	}
}

func TestLoadFile_AllowsEmptyTokenAndTrustedCIDR(t *testing.T) {
	yaml := strings.ReplaceAll(validAgentYAML(), `token: "secret"`, `token: ""`)
	yaml = strings.ReplaceAll(yaml, `trusted_cidr: "10.0.0.0/8"`, `trusted_cidr: ""`)
	path := writeTempConfig(t, yaml)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if cfg.Agent.Token != "" {
		t.Fatalf("token = %q, want empty", cfg.Agent.Token)
	}
	if cfg.Agent.TrustedCIDR != "" {
		t.Fatalf("trusted_cidr = %q, want empty", cfg.Agent.TrustedCIDR)
	}
}

func TestLoadFile_RejectsUnknownFields(t *testing.T) {
	for name, replacement := range map[string]string{
		"token typo":        `tokne: "secret"`,
		"trusted_cidr typo": `trusted_cidrs: "10.0.0.0/8"`,
	} {
		t.Run(name, func(t *testing.T) {
			yaml := strings.Replace(validAgentYAML(), `token: "secret"`, replacement, 1)
			path := writeTempConfig(t, yaml)

			if _, err := LoadFile(path); err == nil {
				t.Fatal("LoadFile() error = nil, want unknown field error")
			}
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

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
