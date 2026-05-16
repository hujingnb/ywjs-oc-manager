// Package domain 的应用状态机测试覆盖 5 阶段 init 子状态、binding 段、运行段以及 error 重试和软删除的状态机约束。
package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsAppTransitionAllowed_LegalTransitions 验证 21 条合法转移每条都能通过校验。
// 子测试 name 即转移本身，失败时定位精确到具体一行；任何一条新增/删除转移都必须在此处同步登记。
func TestIsAppTransitionAllowed_LegalTransitions(t *testing.T) {
	cases := []struct {
		from string
		to   string
	}{
		// 5 个 init 子状态串行：worker 按顺序推进，每一步对应一个明确的副作用阶段。
		{AppStatusDraft, AppStatusPullingImage},                 // onboarding 拾取后从 draft 进入 worker 第一阶段
		{AppStatusPullingImage, AppStatusSyncingImage},          // 镜像 pull 完成后进入跨节点 sync
		{AppStatusSyncingImage, AppStatusPreparingRuntime},      // 镜像 sync 完成后写运行时配置
		{AppStatusPreparingRuntime, AppStatusCreatingContainer}, // 运行时准备完成后创建容器
		{AppStatusCreatingContainer, AppStatusStarting},         // 容器创建完成后进入启动
		{AppStatusStarting, AppStatusBindingWaiting},            // 容器健康检查通过后等渠道扫码

		// binding / running 段：渠道绑定与容器运行状态切换。
		{AppStatusBindingWaiting, AppStatusRunning},       // 渠道扫码成功后进入运行态
		{AppStatusBindingWaiting, AppStatusBindingFailed}, // 扫码超时 / token 过期落到 binding_failed
		{AppStatusBindingFailed, AppStatusBindingWaiting}, // 用户手动重启绑定
		{AppStatusBindingFailed, AppStatusError},          // 多次失败后用户放弃或自动收敛
		{AppStatusRunning, AppStatusStopped},              // 用户主动停止
		{AppStatusRunning, AppStatusError},                // 运行时容器异常退出
		{AppStatusStopped, AppStatusRunning},              // 用户重启
		{AppStatusStopped, AppStatusError},                // 停止状态下底层异常（例如镜像被清理）

		// 5 个 init 子状态各自失败：全部收敛到 error，由 last_error_status 记录来源阶段。
		{AppStatusPullingImage, AppStatusError},      // pull 失败
		{AppStatusSyncingImage, AppStatusError},      // sync 失败
		{AppStatusPreparingRuntime, AppStatusError},  // 写运行时配置失败
		{AppStatusCreatingContainer, AppStatusError}, // 创建容器失败
		{AppStatusStarting, AppStatusError},          // 启动 / 健康检查失败

		// error 重试 / 软删除：error 是吸入态，离开必须显式触发。
		{AppStatusError, AppStatusPullingImage}, // RequestInitialize 重试入口，回到 worker 第一阶段
		{AppStatusError, AppStatusDeleted},      // SoftDeleteApp 终态由 IsAppTransitionAllowed 特殊分支兜底
	}
	for _, c := range cases {
		// 子测试名直接用转移本身，便于定位；失败时一眼看出哪一条未通过校验。
		t.Run(c.from+"->"+c.to, func(t *testing.T) {
			assert.True(t, IsAppTransitionAllowed(c.from, c.to), "合法转移被拒绝")
		})
	}
}

// TestIsAppTransitionAllowed_IllegalTransitions 覆盖关键非法转移。
// 不穷举，只挑能体现"状态机不会被绕过"的代表性 case，确保 worker / handler 不能跳阶段或回退。
func TestIsAppTransitionAllowed_IllegalTransitions(t *testing.T) {
	cases := []struct {
		name string
		from string
		to   string
	}{
		// 跳阶段：worker 不能从 pulling 直接跳过 sync 进入 preparing。
		{"跳过 sync 阶段", AppStatusPullingImage, AppStatusPreparingRuntime},
		// 不能从 running 回退到 init 子状态，避免运行中被误置为初始化阶段。
		{"running 不能回退到 init", AppStatusRunning, AppStatusPullingImage},
		// 同状态原地转移视为非法，避免 worker 重复触发副作用。
		{"同状态原地转移", AppStatusPullingImage, AppStatusPullingImage},
		// 进入 deleted 必须从 error 出发，确保走 SoftDeleteApp 流程。
		{"running 不能直接 → deleted", AppStatusRunning, AppStatusDeleted},
		// draft 只能进 pulling_image，不能直接跨阶段进 binding_waiting。
		{"draft 不能直接 → binding_waiting", AppStatusDraft, AppStatusBindingWaiting},
	}
	for _, c := range cases {
		// 子测试 name 描述场景含义，失败时直接看出哪条约束被打破。
		t.Run(c.name, func(t *testing.T) {
			assert.False(t, IsAppTransitionAllowed(c.from, c.to), "非法转移被放行")
		})
	}
}

// TestEnsureAppTransitionWraps 验证 EnsureAppTransition 对合法/非法转移的返回值与错误包装。
func TestEnsureAppTransitionWraps(t *testing.T) {
	// 非法转移必须返回带上下文的错误，方便 service / handler 直接向上抛。
	err := EnsureAppTransition(AppStatusRunning, AppStatusPullingImage)
	require.Error(t, err)
	// 合法转移必须无错，保证业务侧能正常推进状态机。
	err = EnsureAppTransition(AppStatusDraft, AppStatusPullingImage)
	require.NoError(t, err)
}

// TestAppIsTerminalOnlyDeleted 验证只有 deleted 是终态，其他状态都可经状态机回到运行态。
func TestAppIsTerminalOnlyDeleted(t *testing.T) {
	// deleted 是唯一终态：deleted_at 字段非空即认为已删，状态机不再允许离开。
	require.True(t, AppIsTerminal(AppStatusDeleted))
	// 其他状态（含 error / 5 个 init 子状态 / running / stopped）都仍可经状态机回到运行态。
	for _, status := range []string{
		AppStatusError,
		AppStatusRunning,
		AppStatusStopped,
		AppStatusDraft,
		AppStatusPullingImage,
		AppStatusSyncingImage,
		AppStatusPreparingRuntime,
		AppStatusCreatingContainer,
		AppStatusStarting,
		AppStatusBindingWaiting,
		AppStatusBindingFailed,
	} {
		require.False(t, AppIsTerminal(status), "非 deleted 状态 %s 不应被判为终态", status)
	}
}

// TestIsAPIKeyTransitionAllowedHappyPath 验证 api_key 状态机所有合法转移可通过校验。
// api_key 状态与 app 状态独立，不与本次 init 子状态拆分耦合，保持原有覆盖即可。
func TestIsAPIKeyTransitionAllowedHappyPath(t *testing.T) {
	cases := [][2]string{
		{APIKeyStatusPending, APIKeyStatusActive},  // 场景：待创建 API key 成功后允许变为 active
		{APIKeyStatusPending, APIKeyStatusError},   // 场景：待创建 API key 失败后允许变为 error
		{APIKeyStatusActive, APIKeyStatusDisabled}, // 场景：active API key 允许被禁用
		{APIKeyStatusActive, APIKeyStatusError},    // 场景：active API key 遇到异常时允许进入 error
		{APIKeyStatusDisabled, APIKeyStatusActive}, // 场景：disabled API key 允许重新启用
		{APIKeyStatusError, APIKeyStatusPending},   // 场景：error API key 允许回到 pending 重试
	}
	for _, c := range cases {
		// 子测试名直接体现转移含义，失败时定位到具体 api_key 状态。
		t.Run(c[0]+"->"+c[1], func(t *testing.T) {
			assert.True(t, IsAPIKeyTransitionAllowed(c[0], c[1]), "合法 api_key 转移被拒绝")
		})
	}
}

// TestAPIKeyAndAppStateAreIndependent 验证 api_key 状态机与 app 状态机相互独立、不共享状态集合。
func TestAPIKeyAndAppStateAreIndependent(t *testing.T) {
	// app 状态机内部允许 running → stopped。
	require.True(t, IsAppTransitionAllowed(AppStatusRunning, AppStatusStopped))
	// api_key 状态名不属于 app 状态机：跨枚举混用应被拒绝，避免数据污染。
	require.False(t, IsAppTransitionAllowed(APIKeyStatusActive, AppStatusStopped))
	// api_key 状态机内部正常推进不受 app 状态机影响。
	require.True(t, IsAPIKeyTransitionAllowed(APIKeyStatusActive, APIKeyStatusDisabled))
}

// TestEnsureAPIKeyTransitionFailsForInvalid 验证 EnsureAPIKeyTransition 对合法/非法转移的返回值。
func TestEnsureAPIKeyTransitionFailsForInvalid(t *testing.T) {
	// disabled → error 不在 apiKeyTransitions 中，应返回错误。
	err := EnsureAPIKeyTransition(APIKeyStatusDisabled, APIKeyStatusError)
	require.Error(t, err)
	// pending → active 是常规创建成功路径，必须无错。
	err = EnsureAPIKeyTransition(APIKeyStatusPending, APIKeyStatusActive)
	require.NoError(t, err)
}
