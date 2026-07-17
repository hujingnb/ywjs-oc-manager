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

// 验证默认参数兼容人工直接 seed，并保持 regression 单 worker 行为。
func TestLoadRunOptionsUsesSafeDefaults(t *testing.T) {
	t.Setenv("OCM_E2E_RUN_ID", "")
	t.Setenv("OCM_E2E_SUITE", "")
	t.Setenv("OCM_E2E_WORKERS", "")
	t.Setenv("OCM_E2E_ACTION", "")

	opts, err := loadRunOptions()

	require.NoError(t, err)
	assert.Equal(t, runOptions{RunID: "manual", Suite: suiteRegression, Workers: 1, Action: actionSeed}, opts)
}

// 验证 run ID 会先归一化为小写安全片段，供数据库 fixture 命名复用。
func TestLoadRunOptionsSanitizesRunID(t *testing.T) {
	t.Setenv("OCM_E2E_RUN_ID", " Run_AB C!! ")
	t.Setenv("OCM_E2E_SUITE", "quick")
	t.Setenv("OCM_E2E_WORKERS", "2")
	t.Setenv("OCM_E2E_ACTION", "seed")

	opts, err := loadRunOptions()

	require.NoError(t, err)
	assert.Equal(t, runOptions{RunID: "run-ab-c", Suite: suiteQuick, Workers: 2, Action: actionSeed}, opts)
}

// 验证三个 worker 的组织、账号与实例命名空间互不相同。
func TestFixtureIdentitiesAreUniquePerWorker(t *testing.T) {
	items, err := fixtureIdentities(runOptions{RunID: "run-abc123", Suite: suiteRegression, Workers: 3, Action: actionSeed})

	require.NoError(t, err)
	require.Len(t, items, 3)
	assert.NotEqual(t, items[0].PlatformAdminLogin, items[1].PlatformAdminLogin)
	assert.NotEqual(t, items[0].OrgCode, items[1].OrgCode)
	assert.NotEqual(t, items[1].OrgAdminLogin, items[2].OrgAdminLogin)
	assert.NotEqual(t, items[0].AppName, items[2].AppName)
	assert.Equal(t, fixtureIdentity{
		RunID:              "run-abc123",
		WorkerIndex:        0,
		OrgName:            "e2e-run-abc123-w0",
		OrgCode:            "e2e-run-abc123-w0",
		PlatformAdminLogin: "e2e-run-abc123-w0-platform",
		OrgAdminLogin:      "e2e-run-abc123-w0-admin",
		OrgMemberLogin:     "e2e-run-abc123-w0-member",
		AppName:            "e2e-run-abc123-w0-app",
	}, items[0])
}

// 验证所有显式非法运行参数都会快速失败，避免生成不完整或越界的 fixture 池。
func TestLoadRunOptionsRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		runID   string
		suite   string
		workers string
		action  string
		message string
	}{
		// 全部由不安全字符组成的 run ID 清洗后为空，必须拒绝。
		{name: "run ID 清洗后为空", runID: "!!!", suite: "regression", workers: "1", action: "seed", message: "1 到 16"},
		// 超过 16 字符的 run ID 会导致数据库对象命名失控，必须拒绝。
		{name: "run ID 过长", runID: "12345678901234567", suite: "regression", workers: "1", action: "seed", message: "1 到 16"},
		// suite 仅允许 quick、regression、slow 三级枚举。
		{name: "suite 非法", runID: "run-a", suite: "nightly", workers: "1", action: "seed", message: "未知 OCM_E2E_SUITE"},
		// action 仅解析 seed 与后续清理契约的两个枚举。
		{name: "action 非法", runID: "run-a", suite: "regression", workers: "1", action: "drop", message: "未知 OCM_E2E_ACTION"},
		// worker 必须是整数，拒绝隐式解析为默认值。
		{name: "worker 非整数", runID: "run-a", suite: "regression", workers: "many", action: "seed", message: "1 到 4"},
		// worker 下界为 1，空池不能进入 seed 流程。
		{name: "worker 为零", runID: "run-a", suite: "regression", workers: "0", action: "seed", message: "1 到 4"},
		// worker 上界为 4，避免本地依赖被过量并发压垮。
		{name: "worker 超上限", runID: "run-a", suite: "regression", workers: "5", action: "seed", message: "1 到 4"},
		// slow 虽最终固定单 worker，也不能绕过显式 worker 值的合法性校验。
		{name: "slow 显式 worker 非法", runID: "run-a", suite: "slow", workers: "5", action: "seed", message: "1 到 4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 每个子测试独立注入完整环境，避免开发机变量影响参数解析结果。
			t.Setenv("OCM_E2E_RUN_ID", tt.runID)
			t.Setenv("OCM_E2E_SUITE", tt.suite)
			t.Setenv("OCM_E2E_WORKERS", tt.workers)
			t.Setenv("OCM_E2E_ACTION", tt.action)

			_, err := loadRunOptions()

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}
}

// 验证 slow 套件在合法显式并发值下仍固定为单 worker，隔离真实外部依赖。
func TestLoadRunOptionsForcesSlowToOneWorker(t *testing.T) {
	t.Setenv("OCM_E2E_RUN_ID", "run-a")
	t.Setenv("OCM_E2E_SUITE", "slow")
	t.Setenv("OCM_E2E_WORKERS", "4")
	t.Setenv("OCM_E2E_ACTION", "cleanup-expired")

	opts, err := loadRunOptions()

	require.NoError(t, err)
	assert.Equal(t, runOptions{RunID: "run-a", Suite: suiteSlow, Workers: 1, Action: actionCleanupExpired}, opts)
}

// 验证 seed action 可进入现有构建流程。
func TestRequireSeedActionAcceptsSeed(t *testing.T) {
	err := requireSeedAction(runOptions{Action: actionSeed})

	require.NoError(t, err)
}

// 验证 Task 4 接入前清理 action 会安全退出，不能误入 truncate 后重新 seed 的流程。
func TestRequireSeedActionRejectsCleanup(t *testing.T) {
	tests := []struct {
		name   string
		action e2eAction
	}{
		// 普通 scoped cleanup 当前只完成解析契约，尚未实现执行逻辑。
		{name: "cleanup", action: actionCleanup},
		// 过期资源清理当前只完成解析契约，尚未实现执行逻辑。
		{name: "cleanup-expired", action: actionCleanupExpired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 每个子测试都验证清理 action 在数据库操作之前被统一拒绝。
			err := requireSeedAction(runOptions{Action: tt.action})

			require.Error(t, err)
			assert.Contains(t, err.Error(), "尚未实现")
		})
	}
}

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
