package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"

	"oc-manager/internal/migrations"
)

// TestIofsSourceLoads 校验 iofs.New 能从 internal/migrations.FS 构造 source instance；
// 不连数据库，仅验证 embed FS 与 iofs 兼容、不会因路径错位变空 source。
func TestIofsSourceLoads(t *testing.T) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("iofs.New error = %v", err)
	}
	defer src.Close()
	first, err := src.First()
	if err != nil {
		t.Fatalf("iofs.First error = %v (embed FS 可能为空或路径错位)", err)
	}
	if first == 0 {
		t.Fatal("iofs.First() 返回 version=0，未发现迁移文件")
	}
}

func TestLoadDatabaseURLReadsOnlyConfigFile(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://env:env@localhost/env?sslmode=disable")

	want := "postgres://config:config@localhost/config?sslmode=disable"
	configPath := filepath.Join(t.TempDir(), "manager.yaml")
	if err := os.WriteFile(configPath, []byte(`
app:
  http_addr: ":8080"
  data_root: "./data/manager"
  knowledge_root: "/var/lib/oc-manager/knowledge"
database:
  url: "`+want+`"
redis:
  addr: "redis:6379"
auth:
  access_token_ttl: "15m"
  refresh_token_ttl: "720h"
  jwt_access_secret: "access-secret"
  jwt_refresh_secret: "refresh-secret"
  csrf_secret: "csrf-secret"
security:
  master_key: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
openclaw:
  system_prompt_template: |
    {{workspace_dir}} {{knowledge_org_dir}} {{knowledge_app_dir}}
`), 0o600); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	t.Setenv("OCM_CONFIG", configPath)

	got, err := loadDatabaseURL()
	if err != nil {
		t.Fatalf("loadDatabaseURL() error = %v", err)
	}
	if got != want {
		t.Fatalf("loadDatabaseURL() = %q, want %q", got, want)
	}
}
