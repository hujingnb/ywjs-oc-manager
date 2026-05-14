// Package config 的测试覆盖 YAML 解析、必需项校验和安全密钥格式。
package config

import (
	"encoding/base64"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validBase64MasterKey 提供测试用的 32 字节 base64 master_key。
const validBase64MasterKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// validSystemPromptTemplate 包含 HermesConfig 校验要求的最小模板（非空即合法）。
const validSystemPromptTemplate = `你是 Hermes 智能助手。`

// fullValidYAML 返回一份带新字段的合法配置文本，便于多个用例共用。
// 任何 security/hermes/agent 校验路径都应基于此文本派生最小修改。
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
hermes:
  runtime_image: "hermes-runtime:dev"
  system_prompt_template: |
    你是 Hermes 智能助手。
  workspace:
    archive_retention_days: 14
agent:
  heartbeat_interval_seconds: 30
runtime:
  enrollment_secret: "` + validBase64MasterKey + `"
`
}

// TestLoad_DoesNotExpandEnvPlaceholders 验证加载Does未ExpandEnvPlaceholders的预期行为场景。
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
	require.Equal(t, "hermes-runtime:dev", cfg.Hermes.RuntimeImage)
	require.Equal(t, 14, cfg.Hermes.Workspace.ArchiveRetentionDays)
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

// TestValidateReportsRequiredFields 验证校验配置必填项必填字段的预期行为场景。
func TestValidateReportsRequiredFields(t *testing.T) {
	err := (Config{}).Validate()
	require.Error(t, err)
	required := []string{
		"app.http_addr", "app.data_root", "app.knowledge_root", "database.url", "redis.addr",
		"auth.access_token_ttl", "auth.refresh_token_ttl",
		"auth.jwt_access_secret", "auth.jwt_refresh_secret", "auth.csrf_secret",
		"security.master_key", "runtime.enrollment_secret", "hermes.system_prompt_template",
	}
	for _, field := range required {
		require.True(t, strings.Contains(err.Error(), field))
	}
}

// TestLoadFileFailsWhenDurationInvalid 验证加载文件失败当时长非法的异常或拒绝路径场景。
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
hermes:
  system_prompt_template: |
    你是 Hermes 智能助手。
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
		// 当前子测试覆盖表格用例中该名称对应的输入组合、边界条件和期望结果。
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

// TestLoad_RejectsEmptySystemPromptTemplate 校验 hermes.system_prompt_template 为空时 fail-fast。
// Hermes 时代不再要求 {{workspace_dir}} 等占位符，但模板本身不能为空。
func TestLoad_RejectsEmptySystemPromptTemplate(t *testing.T) {
	// 用例：system_prompt_template 仅含空白时应被拒绝。
	yaml := strings.Replace(fullValidYAML(),
		"system_prompt_template: |\n    你是 Hermes 智能助手。",
		"system_prompt_template: \"\"", 1)
	path := writeTempConfig(t, yaml)
	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "hermes.system_prompt_template") {
		t.Fatalf("LoadFile() err = %v, 期望含 hermes.system_prompt_template 错误", err)
	}
}

// TestLoad_AcceptsValidConfig 校验完整合法配置可被加载。
func TestLoad_AcceptsValidConfig(t *testing.T) {
	path := writeTempConfig(t, fullValidYAML())
	cfg, err := LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, validBase64MasterKey, cfg.Security.MasterKey)
	// Hermes 时代模板不要求占位符，只要非空即可。
	require.NotEmpty(t, cfg.Hermes.SystemPromptTemplate)
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(content), 0o600)
	require.NoError(t, err)
	return path
}
