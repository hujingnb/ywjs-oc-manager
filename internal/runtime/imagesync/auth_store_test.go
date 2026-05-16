package imagesync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadRegistryAuthStore_StaticAuth 验证从 config.json 静态 auths 字段读取凭据。
// 一期只支持 base64(user:pass) 格式,不处理 credentials helper / keychain。
func TestLoadRegistryAuthStore_StaticAuth(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	// 模拟典型 docker login 后生成的 config.json:base64("alice:s3cret") = "YWxpY2U6czNjcmV0"
	content := `{
  "auths": {
    "registry.example.com": {"auth": "YWxpY2U6czNjcmV0"},
    "https://index.docker.io/v1/": {"auth": "Ym9iOmh1bnRlcjI="}
  }
}`
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0o600))

	store, err := LoadRegistryAuthStore(configPath)
	require.NoError(t, err)

	// 自定义 registry:命中 hostname 直接返回
	got := store.AuthFor("registry.example.com/team/app:dev")
	assert.Equal(t, "alice", got.Username)
	assert.Equal(t, "s3cret", got.Password)
	assert.Equal(t, "registry.example.com", got.ServerAddress)

	// docker.io 默认 hub:支持 "library/foo:tag" 简写
	hub := store.AuthFor("library/hermes-runtime:dev")
	assert.Equal(t, "bob", hub.Username)
	assert.Equal(t, "hunter2", hub.Password)
}

// TestLoadRegistryAuthStore_MissingFile 配置文件不存在视为"无凭据",不报错;
// 拉公共镜像不需要 auth,缺文件不应阻塞 manager 启动。
func TestLoadRegistryAuthStore_MissingFile(t *testing.T) {
	store, err := LoadRegistryAuthStore("/nonexistent/config.json")
	require.NoError(t, err)
	assert.Empty(t, store.AuthFor("library/anything:latest").Username)
}

// TestLoadRegistryAuthStore_NoMatch 已加载但目标 registry 不在 auths 里:
// 返回零值,调用方不传 X-Registry-Auth 头,docker daemon 仍可拉公共镜像。
func TestLoadRegistryAuthStore_NoMatch(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"auths":{"a.example.com":{"auth":"YWxpY2U6czNjcmV0"}}}`), 0o600))

	store, err := LoadRegistryAuthStore(configPath)
	require.NoError(t, err)
	assert.Empty(t, store.AuthFor("b.example.com/foo:bar").Username)
}
