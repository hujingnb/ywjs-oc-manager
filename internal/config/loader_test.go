// Package config 的测试覆盖 YAML 解析、必需项校验和安全密钥格式。
package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validBase64MasterKey 提供测试用的 32 字节 base64 master_key。
const validBase64MasterKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// fullValidYAML 返回一份带新字段的合法配置文本，便于多个用例共用。
// 任何 security/hermes/agent 校验路径都应基于此文本派生最小修改。
func fullValidYAML() string {
	return `
app:
  env: dev
  http_addr: ":8080"
  public_base_url: "http://localhost:8080"
  data_root: "./data/manager"
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
		"app.http_addr", "app.data_root", "database.url", "redis.addr",
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

// TestLoad_AllowsMissingRAGFlowConfig 验证本地未启用 RAGFlow 时 manager 仍可启动，知识库请求由 service 层返回未配置。
func TestLoad_AllowsMissingRAGFlowConfig(t *testing.T) {
	cfg := loadConfigFromString(t, fullValidYAML())
	assert.Empty(t, cfg.RAGFlow.BaseURL)
	assert.Empty(t, cfg.RAGFlow.APIKey)
}

// TestLoad_RejectsInvalidRAGFlowBaseURL 验证 RAGFlow 地址配置错误时启动阶段直接失败。
func TestLoad_RejectsInvalidRAGFlowBaseURL(t *testing.T) {
	yaml := fullValidYAML() + "\nragflow:\n  base_url: \"://bad\"\n  api_key: \"secret\"\n"
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ragflow.base_url")
}

// TestLoad_RejectsUnsupportedRAGFlowBaseURLScheme 验证 RAGFlow 仅允许 http/https 地址，避免误把 ftp 等非 HTTP 协议交给运行期客户端。
func TestLoad_RejectsUnsupportedRAGFlowBaseURLScheme(t *testing.T) {
	yaml := fullValidYAML() + "\nragflow:\n  base_url: \"ftp://ragflow:9380\"\n  api_key: \"secret\"\n"
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ragflow.base_url")
}

// TestLoad_RejectsIncompleteRAGFlowConfig 验证 RAGFlow 地址和 API key 必须成对配置。
func TestLoad_RejectsIncompleteRAGFlowConfig(t *testing.T) {
	for name, yaml := range map[string]string{
		// 只配置 base_url 会让运行期鉴权失败，启动阶段应直接拒绝。
		"missing_api_key": fullValidYAML() + "\nragflow:\n  base_url: \"http://ragflow:9380\"\n",
		// 只配置 api_key 无法确定上游地址，启动阶段应直接拒绝。
		"missing_base_url": fullValidYAML() + "\nragflow:\n  api_key: \"secret\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadConfigFromStringErr(t, yaml)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "ragflow.")
		})
	}
}

// TestLoad_DefaultsHermesRuntimeBaseURL 验证 Hermes 容器访问 manager 的内部地址有稳定默认值。
func TestLoad_DefaultsHermesRuntimeBaseURL(t *testing.T) {
	cfg := loadConfigFromString(t, fullValidYAML())
	assert.Equal(t, "http://manager-api:8080", cfg.Hermes.ManagerRuntimeBaseURL)
}

// TestLoad_DefaultsRAGFlowOptions 验证 RAGFlow 启用后未显式配置的请求选项会使用保守默认值。
func TestLoad_DefaultsRAGFlowOptions(t *testing.T) {
	cfg := loadConfigFromString(t, fullValidYAML()+`
ragflow:
  base_url: "http://ragflow:9380"
  api_key: "secret"
`)
	assert.Equal(t, "30s", cfg.RAGFlow.RequestTimeout.Duration.String())
	assert.Equal(t, "naive", cfg.RAGFlow.ChunkMethod)
}

// TestStorageS3ValidationRequiresFields 验证启用 S3 但字段不全时加载报错（fail-fast）。
func TestStorageS3ValidationRequiresFields(t *testing.T) {
	// 启用 S3 却缺 endpoint/bucket/access_key_id/secret_access_key 等，Validate 必须 fail-fast。
	// 使用完整合法的基础配置拼接 storage.s3.enabled=true 触发 S3 专属校验路径。
	yaml := fullValidYAML() + "\nstorage:\n  s3:\n    enabled: true\n"
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage.s3")
}

// TestStorageS3ValidationRequiresSTSRoleARN 验证启用 S3 且主字段齐全但缺 sts_role_arn 时报错。
func TestStorageS3ValidationRequiresSTSRoleARN(t *testing.T) {
	// S3 已启用且 endpoint/bucket/凭证均完整，但缺少 sts_role_arn 时仍应 fail-fast。
	yaml := fullValidYAML() + `
storage:
  s3:
    enabled: true
    endpoint: "http://minio:9000"
    region: "us-east-1"
    bucket: "oc-manager"
    access_key_id: "minioadmin"
    secret_access_key: "minioadmin"
    use_path_style: true
`
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage.s3")
}

// TestStorageS3DefaultsApplied 验证 S3 启用且未配置 region/presign_ttl 时默认值被正确填充。
func TestStorageS3DefaultsApplied(t *testing.T) {
	// S3 启用且字段完整但未指定 region/presign_ttl，applyDefaults 应填充稳定默认值。
	yaml := fullValidYAML() + `
storage:
  s3:
    enabled: true
    endpoint: "http://minio:9000"
    bucket: "oc-manager"
    access_key_id: "minioadmin"
    secret_access_key: "minioadmin"
    use_path_style: true
    sts_role_arn: "arn:aws:iam::000000000000:role/manager"
`
	cfg := loadConfigFromString(t, yaml)
	// region 未配置时应填入默认值 us-east-1。
	assert.Equal(t, "us-east-1", cfg.Storage.S3.Region)
	// presign_ttl 未配置时应填入默认值 15m。
	assert.Equal(t, "15m0s", cfg.Storage.S3.PresignTTL.Duration.String())
}

// TestStorageS3DisabledByDefaultAllowsMissingFields 验证 S3 未启用时缺少字段不会报错。
func TestStorageS3DisabledByDefaultAllowsMissingFields(t *testing.T) {
	// storage 段完全不配置时，Validate 不应因 S3 字段缺失而失败。
	cfg := loadConfigFromString(t, fullValidYAML())
	assert.False(t, cfg.Storage.S3.Enabled)
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(content), 0o600)
	require.NoError(t, err)
	return path
}

func loadConfigFromString(t *testing.T, content string) Config {
	t.Helper()
	cfg, err := loadConfigFromStringErr(t, content)
	require.NoError(t, err)
	return cfg
}

func loadConfigFromStringErr(t *testing.T, content string) (Config, error) {
	t.Helper()
	return LoadFile(writeTempConfig(t, content))
}
