// Package main 的 e2e 种子测试只校验危险命令守门，不连接数据库。
package main

import (
	"context"
	"errors"
	"testing"
	"time"

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

// 验证 E2E 助手版本使用 local-init-models 已配置的 DeepSeek 渠道模型，避免公开问答落到不存在的 gpt-4。
func TestE2EMainModelUsesLocalAvailableChannel(t *testing.T) {
	assert.Equal(t, "deepseek-chat", e2eMainModel())
}

// 验证临时 E2E 用户会获得正额度，避免真实 Hermes 问答在 new-api 余额校验阶段被拒绝。
func TestE2ENewAPICreditAmountIsPositive(t *testing.T) {
	assert.Positive(t, e2eNewAPICreditAmount())
}

// 验证 new-api 登录短暂限流时 seed 按序退避并最终返回 access token。
func TestRetryE2EAccessTokenRetriesRateLimit(t *testing.T) {
	attempts := 0
	var delays []time.Duration
	token, err := retryE2EAccessToken(context.Background(), func() (string, error) {
		attempts++
		if attempts < 3 {
			return "", errors.New("上游服务异常: status=429")
		}
		return "access-token", nil
	}, func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, "access-token", token)
	assert.Equal(t, 3, attempts)
	assert.Equal(t, []time.Duration{2 * time.Second, 4 * time.Second}, delays)
}

// 验证非限流错误不会重试，避免掩盖 new-api 配置、鉴权或协议故障。
func TestRetryE2EAccessTokenRejectsOtherErrors(t *testing.T) {
	attempts := 0
	expected := errors.New("上游鉴权失败: status=401")
	_, err := retryE2EAccessToken(context.Background(), func() (string, error) {
		attempts++
		return "", expected
	}, func(context.Context, time.Duration) error {
		t.Fatal("非限流错误不应进入等待")
		return nil
	})

	require.ErrorIs(t, err, expected)
	assert.Equal(t, 1, attempts)
}
