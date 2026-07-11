// Package main 的 e2e 种子测试只校验危险命令守门，不连接数据库。
package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/config"
)

// 验证 OCM_E2E 守门：缺这个环境变量时命令必须非零退出，避免误在生产 truncate。
func TestSeedE2E_RejectsMissingOCME2EFlag(t *testing.T) {
	t.Setenv("OCM_E2E", "")

	err := requireE2EGuard()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "OCM_E2E=1")
}

// 验证 E2E fixture 使用 manager 已配置的首个 Hermes 镜像 ID，避免隐藏 app 初始化引用不存在的硬编码镜像。
func TestE2ERuntimeImageIDUsesConfiguredImage(t *testing.T) {
	cfg := config.Config{Hermes: config.HermesConfig{RuntimeImages: []config.RuntimeImageConfig{
		{ID: "v2026.7.1", Ref: "registry.local/hermes:v2026.7.1"},
	}}}

	imageID, err := e2eRuntimeImageID(cfg)

	require.NoError(t, err)
	assert.Equal(t, "v2026.7.1", imageID)
}

// 验证未配置 Hermes runtime image 时种子命令快速失败，避免生成永远无法启动的 AICC 隐藏 app。
func TestE2ERuntimeImageIDRejectsEmptyConfig(t *testing.T) {
	_, err := e2eRuntimeImageID(config.Config{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "runtime image")
}

// 验证 E2E new-api 用户使用可精确清理的固定名称，并满足上游 12 字符长度限制。
func TestE2ENewAPIUsernameIsStableAndValid(t *testing.T) {
	username := e2eNewAPIUsername()

	assert.Equal(t, "e2eaicc", username)
	assert.LessOrEqual(t, len(username), 12)
}
