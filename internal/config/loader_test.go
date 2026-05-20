// Package config 的测试覆盖 YAML 解析、必需项校验和安全密钥格式。
package config

import (
	"encoding/base64"
	"github.com/stretchr/testify/assert"
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
  runtime_image: "hermes-runtime:v2026.5.16-dev"
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
	require.Equal(t, "hermes-runtime:v2026.5.16-dev", cfg.Hermes.RuntimeImage)
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
		"hermes.runtime_image",
	}
	for _, field := range required {
		require.True(t, strings.Contains(err.Error(), field))
	}
}

// TestLoad_RejectsMissingHermesRuntimeImage 校验 hermes.runtime_image 缺失或为空时启动 fail-fast。
func TestLoad_RejectsMissingHermesRuntimeImage(t *testing.T) {
	cases := []struct {
		// name 标识当前用例覆盖的缺失形态。
		name string
		// yaml 是由完整合法配置派生出的异常输入。
		yaml string
	}{
		{
			// 场景：字段整行缺失时应报必填项错误。
			name: "missing",
			yaml: strings.Replace(fullValidYAML(),
				`  runtime_image: "hermes-runtime:v2026.5.16-dev"`+"\n", "", 1),
		},
		{
			// 场景：字段存在但值为空字符串时也应报必填项错误。
			name: "empty",
			yaml: strings.Replace(fullValidYAML(),
				`runtime_image: "hermes-runtime:v2026.5.16-dev"`, `runtime_image: ""`, 1),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := writeTempConfig(t, c.yaml)
			_, err := LoadFile(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "hermes.runtime_image")
		})
	}
}

// TestLoad_RejectsUnsafeHermesRuntimeImage 校验 runtime 镜像引用必须固定到具体版本。
func TestLoad_RejectsUnsafeHermesRuntimeImage(t *testing.T) {
	cases := []struct {
		// name 标识当前非法镜像引用覆盖的边界。
		name string
		// image 是写入 hermes.runtime_image 的待校验镜像引用。
		image string
		// want 是期望错误中包含的业务说明片段。
		want string
	}{
		{
			// 场景：main 是分支语义 tag，应被拒绝。
			name:  "floating-main",
			image: "hermes-runtime:main",
			want:  "浮动版本",
		},
		{
			// 场景：latest 会随仓库更新漂移，应被拒绝。
			name:  "floating-latest",
			image: "hermes-runtime:latest",
			want:  "浮动版本",
		},
		{
			// 场景：dev 只代表开发态，不是可复现版本，应被拒绝。
			name:  "floating-dev",
			image: "hermes-runtime:dev",
			want:  "浮动版本",
		},
		{
			// 场景：旧 variant 名称 hermes-main 不能继续作为镜像 tag 使用。
			name:  "old-variant",
			image: "hermes-runtime:hermes-main-dev",
			want:  "旧 variant",
		},
		{
			// 场景：stable 是可漂移别名，不含完整 Hermes 版本号，应被拒绝。
			name:  "floating-stable",
			image: "hermes-runtime:stable",
			want:  "具体 Hermes 版本号",
		},
		{
			// 场景：prod 代表环境语义，不是可复现版本，应被拒绝。
			name:  "floating-prod",
			image: "hermes-runtime:prod",
			want:  "具体 Hermes 版本号",
		},
		{
			// 场景：2.1 是版本族 tag，后续补丁版本可能漂移，应被拒绝。
			name:  "version-family",
			image: "hermes-runtime:2.1",
			want:  "具体 Hermes 版本号",
		},
		{
			// 场景：v2026.5 缺少 patch 段，不是完整 Hermes 版本号，应被拒绝。
			name:  "partial-version",
			image: "hermes-runtime:v2026.5",
			want:  "具体 Hermes 版本号",
		},
		{
			// 场景：缺少 tag 或 digest 时无法定位具体镜像版本，应被拒绝。
			name:  "missing-tag",
			image: "registry.example.com/hermes-runtime",
			want:  "具体 tag",
		},
		{
			// 场景：tag 中带空白会被 Docker 解析为非法引用，应被拒绝。
			name:  "whitespace",
			image: "hermes-runtime:v2026.5.16 dev",
			want:  "空白字符",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			yaml := strings.Replace(fullValidYAML(),
				`runtime_image: "hermes-runtime:v2026.5.16-dev"`,
				`runtime_image: "`+c.image+`"`, 1)
			path := writeTempConfig(t, yaml)
			_, err := LoadFile(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

// TestLoad_AcceptsConcreteHermesRuntimeImageTags 校验完整 Hermes 版本号 tag 可通过启动校验。
func TestLoad_AcceptsConcreteHermesRuntimeImageTags(t *testing.T) {
	cases := []struct {
		// name 标识当前具体版本 tag 的形态。
		name string
		// image 是写入 hermes.runtime_image 的合法版本化引用。
		image string
	}{
		{
			// 场景：完整 Hermes 版本号本身是可复现版本 tag。
			name:  "plain-version",
			image: "hermes-runtime:v2026.5.16",
		},
		{
			// 场景：本地 dev stub 允许在完整版本号后追加 -dev。
			name:  "versioned-dev",
			image: "hermes-runtime:v2026.5.16-dev",
		},
		{
			// 场景：生产镜像 tag 在完整 Hermes 版本号后追加构建时间戳。
			name:  "versioned-release",
			image: "registry.example.com/oc/hermes-runtime:v2026.5.16-2026-05-21-03-40-00",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			yaml := strings.Replace(fullValidYAML(),
				`runtime_image: "hermes-runtime:v2026.5.16-dev"`,
				`runtime_image: "`+c.image+`"`, 1)
			path := writeTempConfig(t, yaml)
			cfg, err := LoadFile(path)
			require.NoError(t, err)
			assert.Equal(t, c.image, cfg.Hermes.RuntimeImage)
		})
	}
}

// TestLoad_AcceptsHermesRuntimeImageDigest 校验 sha256 digest 固定引用可通过启动校验。
func TestLoad_AcceptsHermesRuntimeImageDigest(t *testing.T) {
	digest := strings.Repeat("a", 64)
	image := "registry.example.com/oc/hermes-runtime@sha256:" + digest
	yaml := strings.Replace(fullValidYAML(),
		`runtime_image: "hermes-runtime:v2026.5.16-dev"`,
		`runtime_image: "`+image+`"`, 1)
	path := writeTempConfig(t, yaml)

	cfg, err := LoadFile(path)
	require.NoError(t, err)
	assert.Equal(t, image, cfg.Hermes.RuntimeImage)
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
