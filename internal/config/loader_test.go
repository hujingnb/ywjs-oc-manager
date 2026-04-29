package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFileExpandsEnvironmentVariables(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://ocm:ocm@manager-postgres:5432/ocm?sslmode=disable")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("JWT_ACCESS_SECRET", "access-secret")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret")
	t.Setenv("CSRF_SECRET", "csrf-secret")

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
	for _, field := range []string{"app.http_addr", "app.data_root", "database.url", "redis.addr", "auth.access_token_ttl", "auth.refresh_token_ttl", "auth.jwt_access_secret", "auth.jwt_refresh_secret", "auth.csrf_secret"} {
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
`)

	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want duration parse error")
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
