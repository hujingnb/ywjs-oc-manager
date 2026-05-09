package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"github.com/stretchr/testify/require"
)

// validBase64MasterKey 提供测试用的 32 字节 base64 master_key。
const validBase64MasterKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// validSystemPromptTemplate 包含 OpenClawConfig 校验要求的全部占位符。
const validSystemPromptTemplate = `你是 OpenClaw 智能助手。
工作目录：{{workspace_dir}}
组织知识库：{{knowledge_org_dir}}
应用知识库：{{knowledge_app_dir}}`

// fullValidYAML 返回一份带新字段的合法配置文本，便于多个用例共用。
// 任何 security/openclaw/agent 校验路径都应基于此文本派生最小修改。
func fullValidYAML() string {
	return `
app:
  env: dev
  http_addr: ":8080"
  public_base_url: "http://localhost:8080"
  data_root: "./data/manager"
  knowledge_root: "/var/lib/oc-manager/knowledge"
database:
  url: "postgres://ocm:ocm@manager-postgres:5432/ocm?sslmode=disable"
redis:
  addr: "redis:6379"
  password: ""
  db: 0
  key_prefix: "ocm:"
auth:
  cookie_domain: "localhost"
  access_token_ttl: "15m"
  refresh_token_ttl: "720h"
  jwt_access_secret: "access-secret"
  jwt_refresh_secret: "refresh-secret"
  csrf_secret: "csrf-secret"
security:
  master_key: "` + validBase64MasterKey + `"
openclaw:
  runtime_image: "openclaw-runtime:dev"
  system_prompt_template: |
    你是 OpenClaw 智能助手。
    工作目录：{{workspace_dir}}
    组织知识库：{{knowledge_org_dir}}
    应用知识库：{{knowledge_app_dir}}
  workspace:
    archive_retention_days: 14
agent:
  heartbeat_interval_seconds: 30
`
}

func TestLoad_DoesNotExpandEnvPlaceholders(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://should-not-be-substituted/db")

	yaml := strings.Replace(fullValidYAML(),
		`url: "postgres://ocm:ocm@manager-postgres:5432/ocm?sslmode=disable"`,
		`url: "${DATABASE_URL}"`, 1)
	path := writeTempConfig(t, yaml)

	cfg, err := LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, "${DATABASE_URL}", cfg.Database.URL)
	require.Equal(t, "15m0s", cfg.Auth.AccessTokenTTL.Duration.String())
	require.Equal(t, validBase64MasterKey, cfg.Security.MasterKey)
	require.Equal(t, "openclaw-runtime:dev", cfg.OpenClaw.RuntimeImage)
	require.Equal(t, 14, cfg.OpenClaw.Workspace.ArchiveRetentionDays)
	require.Equal(t, 30, cfg.Agent.HeartbeatIntervalSeconds)
}

// TestLoad_RejectsUnknownFields 校验 yaml 字段拼写错误会 fail-fast，避免可选配置因 typo 被静默忽略。
func TestLoad_RejectsUnknownFields(t *testing.T) {
	yaml := strings.Replace(fullValidYAML(),
		`key_prefix: "ocm:"`,
		`key_prefx: "ocm:"`, 1)
	path := writeTempConfig(t, yaml)
	_, err := LoadFile(path)
	require.Error(t, err)
}

func TestValidateReportsRequiredFields(t *testing.T) {
	err := (Config{}).Validate()
	require.Error(t, err)
	required := []string{
		"app.http_addr", "app.data_root", "app.knowledge_root", "database.url", "redis.addr",
		"auth.access_token_ttl", "auth.refresh_token_ttl",
		"auth.jwt_access_secret", "auth.jwt_refresh_secret", "auth.csrf_secret",
		"security.master_key", "openclaw.system_prompt_template",
	}
	for _, field := range required {
		require.True(t, strings.Contains(err.Error(), field))
	}
}

func TestLoadFileFailsWhenDurationInvalid(t *testing.T) {
	path := writeTempConfig(t, `
app:
  http_addr: ":8080"
  data_root: "./data/manager"
  knowledge_root: "/var/lib/oc-manager/knowledge"
database:
  url: "postgres://ocm:ocm@manager-postgres:5432/ocm?sslmode=disable"
redis:
  addr: "redis:6379"
auth:
  access_token_ttl: "not-a-duration"
  refresh_token_ttl: "720h"
  jwt_access_secret: "access-secret"
  jwt_refresh_secret: "refresh-secret"
  csrf_secret: "csrf-secret"
security:
  master_key: "`+validBase64MasterKey+`"
openclaw:
  system_prompt_template: |
    {{workspace_dir}} {{knowledge_org_dir}} {{knowledge_app_dir}}
`)

	_, err := LoadFile(path)
	require.Error(t, err)
}

// TestLoad_RejectsMissingKnowledgeRoot 校验 app.knowledge_root 缺失或为空时启动 fail-fast。
func TestLoad_RejectsMissingKnowledgeRoot(t *testing.T) {
	for name, yaml := range map[string]string{
		"missing": strings.Replace(fullValidYAML(),
			`  knowledge_root: "/var/lib/oc-manager/knowledge"`+"\n", "", 1),
		"empty": strings.Replace(fullValidYAML(),
			`knowledge_root: "/var/lib/oc-manager/knowledge"`, `knowledge_root: ""`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			path := writeTempConfig(t, yaml)
			_, err := LoadFile(path)
			if err == nil || !strings.Contains(err.Error(), "app.knowledge_root") {
				t.Fatalf("LoadFile() err = %v, want app.knowledge_root 错误", err)
			}
		})
	}
}

// TestLoad_RejectsMissingMasterKey 校验 security.master_key 缺失时启动 fail-fast。
func TestLoad_RejectsMissingMasterKey(t *testing.T) {
	yaml := strings.Replace(fullValidYAML(),
		"security:\n  master_key: \""+validBase64MasterKey+"\"",
		"security:\n  master_key: \"\"", 1)
	path := writeTempConfig(t, yaml)
	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "security.master_key") {
		t.Fatalf("LoadFile() err = %v, want security.master_key 错误", err)
	}
}

// TestLoad_RejectsShortMasterKey 校验非 32 字节解码后的 master_key 被拒绝。
func TestLoad_RejectsShortMasterKey(t *testing.T) {
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	yaml := strings.Replace(fullValidYAML(), validBase64MasterKey, short, 1)
	path := writeTempConfig(t, yaml)
	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "32 字节") {
		t.Fatalf("LoadFile() err = %v, want 长度错误", err)
	}
}

// TestLoad_RejectsBadBase64MasterKey 校验非法 base64 的 master_key 被拒绝。
func TestLoad_RejectsBadBase64MasterKey(t *testing.T) {
	yaml := strings.Replace(fullValidYAML(), validBase64MasterKey, "!!!not-base64!!!", 1)
	path := writeTempConfig(t, yaml)
	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("LoadFile() err = %v, want base64 错误", err)
	}
}

// TestLoad_RejectsPromptMissingPlaceholder 校验 system_prompt_template 缺占位符时 fail-fast。
func TestLoad_RejectsPromptMissingPlaceholder(t *testing.T) {
	for _, missing := range []string{"{{workspace_dir}}", "{{knowledge_org_dir}}", "{{knowledge_app_dir}}"} {
		yaml := strings.Replace(fullValidYAML(), missing, "(removed)", 1)
		path := writeTempConfig(t, yaml)
		_, err := LoadFile(path)
		if err == nil || !strings.Contains(err.Error(), missing) {
			t.Fatalf("缺 %s 时 err = %v, 期望错误信息中含该占位符", missing, err)
		}
	}
}

// TestLoad_AcceptsValidConfig 校验完整合法配置可被加载。
func TestLoad_AcceptsValidConfig(t *testing.T) {
	path := writeTempConfig(t, fullValidYAML())
	cfg, err := LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, validBase64MasterKey, cfg.Security.MasterKey)
	require.True(t, strings.Contains(cfg.OpenClaw.SystemPromptTemplate, "{{workspace_dir}}"))
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(content), 0o600)
	require.NoError(t, err)
	return path
}
