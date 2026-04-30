package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestLoadFileExpandsEnvironmentVariables(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://ocm:ocm@manager-postgres:5432/ocm?sslmode=disable")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("JWT_ACCESS_SECRET", "access-secret")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret")
	t.Setenv("CSRF_SECRET", "csrf-secret")
	t.Setenv("MASTER_KEY", validBase64MasterKey)

	path := writeTempConfig(t, `
app:
  env: dev
  http_addr: ":8080"
  public_base_url: "http://localhost:8080"
  data_root: "./data/manager"
database:
  url: "${DATABASE_URL}"
redis:
  addr: "${REDIS_ADDR}"
  password: ""
  db: 0
  key_prefix: "ocm:"
auth:
  cookie_domain: "localhost"
  access_token_ttl: "15m"
  refresh_token_ttl: "720h"
  jwt_access_secret: "${JWT_ACCESS_SECRET}"
  jwt_refresh_secret: "${JWT_REFRESH_SECRET}"
  csrf_secret: "${CSRF_SECRET}"
security:
  master_key: "${MASTER_KEY}"
openclaw:
  runtime_image: "openclaw-runtime:dev"
  system_prompt_template: |
    工作目录：{{workspace_dir}}
    组织知识库：{{knowledge_org_dir}}
    应用知识库：{{knowledge_app_dir}}
  workspace:
    archive_retention_days: 14
agent:
  heartbeat_interval_seconds: 30
`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if cfg.Database.URL != os.Getenv("DATABASE_URL") {
		t.Fatalf("database url = %q, want expanded env", cfg.Database.URL)
	}
	if cfg.Redis.Addr != os.Getenv("REDIS_ADDR") {
		t.Fatalf("redis addr = %q, want expanded env", cfg.Redis.Addr)
	}
	if cfg.Auth.AccessTokenTTL.Duration.String() != "15m0s" {
		t.Fatalf("access token ttl = %s, want 15m", cfg.Auth.AccessTokenTTL.Duration)
	}
	if cfg.Security.MasterKey != validBase64MasterKey {
		t.Fatalf("security.master_key = %q, want expanded env", cfg.Security.MasterKey)
	}
	if cfg.OpenClaw.RuntimeImage != "openclaw-runtime:dev" {
		t.Fatalf("openclaw.runtime_image = %q, want openclaw-runtime:dev", cfg.OpenClaw.RuntimeImage)
	}
	if cfg.OpenClaw.Workspace.ArchiveRetentionDays != 14 {
		t.Fatalf("openclaw.workspace.archive_retention_days = %d, want 14", cfg.OpenClaw.Workspace.ArchiveRetentionDays)
	}
	if cfg.Agent.HeartbeatIntervalSeconds != 30 {
		t.Fatalf("agent.heartbeat_interval_seconds = %d, want 30", cfg.Agent.HeartbeatIntervalSeconds)
	}
}

func TestLoadFileFailsWhenEnvironmentVariableMissing(t *testing.T) {
	path := writeTempConfig(t, `
app:
  http_addr: ":8080"
  data_root: "./data/manager"
database:
  url: "${DATABASE_URL_MISSING}"
redis:
  addr: "redis:6379"
auth:
  access_token_ttl: "15m"
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
	if err == nil {
		t.Fatal("LoadFile() error = nil, want missing env error")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL_MISSING") {
		t.Fatalf("error = %q, want missing env name", err.Error())
	}
}

func TestValidateReportsRequiredFields(t *testing.T) {
	err := (Config{}).Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want required fields error")
	}
	required := []string{
		"app.http_addr", "app.data_root", "database.url", "redis.addr",
		"auth.access_token_ttl", "auth.refresh_token_ttl",
		"auth.jwt_access_secret", "auth.jwt_refresh_secret", "auth.csrf_secret",
		"security.master_key", "openclaw.system_prompt_template",
	}
	for _, field := range required {
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("error = %q, want field %s", err.Error(), field)
		}
	}
}

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
openclaw:
  system_prompt_template: |
    {{workspace_dir}} {{knowledge_org_dir}} {{knowledge_app_dir}}
`)

	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want duration parse error")
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
	if err != nil {
		t.Fatalf("LoadFile() err = %v, want nil", err)
	}
	if cfg.Security.MasterKey != validBase64MasterKey {
		t.Fatalf("master_key 解析 = %q, want %q", cfg.Security.MasterKey, validBase64MasterKey)
	}
	if !strings.Contains(cfg.OpenClaw.SystemPromptTemplate, "{{workspace_dir}}") {
		t.Fatalf("system_prompt_template 不含 {{workspace_dir}}: %q", cfg.OpenClaw.SystemPromptTemplate)
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
