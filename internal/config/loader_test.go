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
	for _, field := range []string{"app.http_addr", "app.data_root", "database.url", "redis.addr"} {
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("error = %q, want field %s", err.Error(), field)
		}
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
