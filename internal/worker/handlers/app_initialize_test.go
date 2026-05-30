package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/audit"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/k8sorch"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

const (
	testAppID = "00000000-0000-0000-0000-000000000a01"
	testOrgID = "00000000-0000-0000-0000-000000000b01"
	testUsrID = "00000000-0000-0000-0000-000000000c01"
	// testVersionID 是测试中默认绑定的助手版本 ID。
	testVersionID = "00000000-0000-0000-0000-000000000d01"
)

// fakeOrchestrator 实现 k8sorch.Orchestrator，记录 EnsureApp / WaitReady 调用。
// 用于断言 k8s 编排器被正确调用。
type fakeOrchestrator struct {
	// ensureAppCalls 记录每次 EnsureApp 调用的 AppSpec，供断言使用。
	ensureAppCalls []k8sorch.AppSpec
	// ensureAppErr 非 nil 时 EnsureApp 返回该错误（模拟 k8s apply 失败）。
	ensureAppErr error
	// waitReadyCalls 记录每次 WaitReady 调用的 appID。
	waitReadyCalls []string
	// waitReadyErr 非 nil 时 WaitReady 返回该错误（模拟 pod 就绪超时）。
	waitReadyErr error
}

func (f *fakeOrchestrator) EnsureApp(_ context.Context, spec k8sorch.AppSpec) error {
	f.ensureAppCalls = append(f.ensureAppCalls, spec)
	return f.ensureAppErr
}

func (f *fakeOrchestrator) WaitReady(_ context.Context, appID string, _ time.Duration) error {
	f.waitReadyCalls = append(f.waitReadyCalls, appID)
	return f.waitReadyErr
}

func (f *fakeOrchestrator) Scale(_ context.Context, _ string, _ int32) error {
	return nil
}

func (f *fakeOrchestrator) UpdateImage(_ context.Context, _, _ string) error {
	return nil
}

func (f *fakeOrchestrator) Delete(_ context.Context, _ string) error {
	return nil
}

func (f *fakeOrchestrator) Status(_ context.Context, _ string) (k8sorch.AppStatus, error) {
	return k8sorch.AppStatus{}, nil
}

// TestAppInitializeHandlesHappyPath 验证 k8s 路径应用初始化成功：
// version 校验 + ensureAPIKey + EnsureAppRuntimeToken → EnsureApp（AppSpec 字段正确）
// → WaitReady → binding_waiting。
func TestAppInitializeHandlesHappyPath(t *testing.T) {
	store := newAppInitStub(t)
	client := &fakeNewAPI{result: newapi.APIKey{ID: 99, Key: "sk-test"}}

	cipher, err := auth.NewCipher(make([]byte, 32))
	require.NoError(t, err)

	// bootstrapURL = trimRight(BootstrapBaseURL, "/") + "/internal/apps/" + appID + "/bootstrap"
	bootstrapBase := "http://manager.svc"
	cfg := AppInitializeConfig{
		Cipher:              cipher,
		ResolveRuntimeImage: testResolveRuntimeImage,
	}
	handler := NewAppInitializeHandler(store, client, cfg)

	// 注入 fake orchestrator 与 k8s 配置。
	orch := &fakeOrchestrator{}
	handler.SetOrchestrator(orch, AppInitializeK8sConfig{
		OpsImage:         "ops:latest",
		BootstrapBaseURL: bootstrapBase,
		ImagePullSecret:  "acr-pull",
		Resources: AppInitializeK8sResources{
			Requests: AppInitializeK8sResourceSpec{CPU: "100m", Memory: "128Mi"},
			Limits:   AppInitializeK8sResourceSpec{CPU: "500m", Memory: "512Mi"},
		},
	})

	err = handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	require.NoError(t, err)

	// api_key 必须被持久化。
	require.True(t, store.apiKeySet, "ensureAPIKey 应被调用并写库")

	// ciphertext 必须可被同一 cipher 解回 sk-test。
	plain, err := cipher.Decrypt(store.app.NewapiKeyCiphertext.String)
	require.NoError(t, err)
	require.Equal(t, "sk-test", string(plain))

	// EnsureApp 必须被调用一次，AppSpec 字段应正确。
	require.Len(t, orch.ensureAppCalls, 1, "EnsureApp 应被调用 1 次")
	spec := orch.ensureAppCalls[0]
	assert.Equal(t, testAppID, spec.AppID, "AppSpec.AppID 应等于 app.ID")
	assert.Equal(t, testRuntimeImageRef, spec.HermesImage, "AppSpec.HermesImage 应为 ResolveRuntimeImage 解析出的 ref")
	assert.Equal(t, "ops:latest", spec.OpsImage, "AppSpec.OpsImage 应来自 k8sCfg.OpsImage")
	assert.Equal(t, "acr-pull", spec.ImagePullSecret, "AppSpec.ImagePullSecret 应来自 k8sCfg.ImagePullSecret")
	// BootstrapURL = trimRight(base) + "/internal/apps/" + appID + "/bootstrap"
	assert.Equal(t, bootstrapBase+"/internal/apps/"+testAppID+"/bootstrap", spec.BootstrapURL,
		"AppSpec.BootstrapURL 应正确拼接")
	// ControlToken 必须非空（由 EnsureAppRuntimeToken 生成）。
	assert.NotEmpty(t, spec.ControlToken, "AppSpec.ControlToken 应为 EnsureAppRuntimeToken 生成的明文 token")
	// Resources 应对应 k8sCfg。
	assert.Equal(t, "100m", spec.Resources.RequestsCPU)
	assert.Equal(t, "128Mi", spec.Resources.RequestsMemory)
	assert.Equal(t, "500m", spec.Resources.LimitsCPU)
	assert.Equal(t, "512Mi", spec.Resources.LimitsMemory)

	// WaitReady 必须被调用一次。
	require.Len(t, orch.waitReadyCalls, 1, "WaitReady 应被调用 1 次")
	assert.Equal(t, testAppID, orch.waitReadyCalls[0], "WaitReady 应传入 app.ID")

	// 终态应为 binding_waiting。
	assert.Equal(t, domain.AppStatusBindingWaiting, store.app.Status)

	// 审计日志必须被写入。
	require.Len(t, store.auditLogs, 1)
	assert.Equal(t, "app", store.auditLogs[0].TargetType)
	assert.Equal(t, testAppID, store.auditLogs[0].TargetID)
	assert.Equal(t, "initialize", store.auditLogs[0].Action)
	assert.Equal(t, "succeeded", store.auditLogs[0].Result)
}

// TestAppInitializeK8s_OrchestratorNilSkipsCreateAndWait 验证 orch 未注入时
// phaseCreate / phaseStart 直接跳过，Handle 能正常完成（测试装配兼容）。
func TestAppInitializeK8s_OrchestratorNilSkipsCreateAndWait(t *testing.T) {
	// orch 不注入，Handle 仍应走完，不崩溃。
	store := newAppInitStub(t)
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "sk-x"}}
	handler := NewAppInitializeHandler(store, client, AppInitializeConfig{
		Cipher:              testCipher(t),
		ResolveRuntimeImage: testResolveRuntimeImage,
	})
	// 不调 SetOrchestrator，orch 保持 nil。
	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "")))
	// 状态最终应到达 binding_waiting。
	assert.Equal(t, domain.AppStatusBindingWaiting, store.app.Status)
}

// TestAppInitializeK8s_EnsureAppError 验证 EnsureApp 返回错误时
// handler 透传错误并 markFailed（last_error_status=creating_container）。
func TestAppInitializeK8s_EnsureAppError(t *testing.T) {
	// EnsureApp 失败应触发 markFailed，last_error_status 记为 creating_container。
	store := newAppInitStub(t)
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	handler := NewAppInitializeHandler(store, client, AppInitializeConfig{
		Cipher:              testCipher(t),
		ResolveRuntimeImage: testResolveRuntimeImage,
	})
	orch := &fakeOrchestrator{ensureAppErr: errors.New("k8s apply failed")}
	handler.SetOrchestrator(orch, AppInitializeK8sConfig{})

	err := handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	require.Error(t, err, "EnsureApp 失败应返回 error")
	require.Contains(t, err.Error(), "k8s EnsureApp 失败")
	require.True(t, store.failedSet, "EnsureApp 失败应触发 MarkAppFailed")
	require.True(t, store.lastFailed.LastErrorStatus.Valid)
	assert.Equal(t, domain.AppStatusCreatingContainer, store.lastFailed.LastErrorStatus.String,
		"EnsureApp 失败的 last_error_status 应为 creating_container")
	assert.Equal(t, domain.AppStatusError, store.app.Status)
}

// TestAppInitializeK8s_WaitReadyError 验证 WaitReady 返回错误时
// handler 透传错误并 markFailed（last_error_status=starting）。
func TestAppInitializeK8s_WaitReadyError(t *testing.T) {
	// WaitReady 超时/失败应触发 markFailed，last_error_status 记为 starting。
	store := newAppInitStub(t)
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	handler := NewAppInitializeHandler(store, client, AppInitializeConfig{
		Cipher:              testCipher(t),
		ResolveRuntimeImage: testResolveRuntimeImage,
	})
	orch := &fakeOrchestrator{waitReadyErr: errors.New("pod not ready: timeout")}
	handler.SetOrchestrator(orch, AppInitializeK8sConfig{})

	err := handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	require.Error(t, err, "WaitReady 失败应返回 error")
	require.Contains(t, err.Error(), "等待 k8s pod Ready 失败")
	require.True(t, store.failedSet, "WaitReady 失败应触发 MarkAppFailed")
	require.True(t, store.lastFailed.LastErrorStatus.Valid)
	assert.Equal(t, domain.AppStatusStarting, store.lastFailed.LastErrorStatus.String,
		"WaitReady 失败的 last_error_status 应为 starting")
	assert.Equal(t, domain.AppStatusError, store.app.Status)
}

// TestAppInitializeK8s_BootstrapURLTrailingSlash 验证 BootstrapBaseURL 末尾有斜线时
// buildAppSpec 正确去重，不产生双斜线路径。
func TestAppInitializeK8s_BootstrapURLTrailingSlash(t *testing.T) {
	// BootstrapBaseURL 末尾带 "/" → trimRight 后拼接，不应出现双斜线。
	store := newAppInitStub(t)
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	handler := NewAppInitializeHandler(store, client, AppInitializeConfig{
		Cipher:              testCipher(t),
		ResolveRuntimeImage: testResolveRuntimeImage,
	})
	orch := &fakeOrchestrator{}
	// BootstrapBaseURL 含尾部斜线
	handler.SetOrchestrator(orch, AppInitializeK8sConfig{
		BootstrapBaseURL: "http://manager.svc/",
	})

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "")))
	require.Len(t, orch.ensureAppCalls, 1)
	// 不应出现 "//"
	assert.NotContains(t, orch.ensureAppCalls[0].BootstrapURL, "//internal",
		"去掉尾部 / 后路径不应有双斜线")
	assert.Equal(t,
		"http://manager.svc/internal/apps/"+testAppID+"/bootstrap",
		orch.ensureAppCalls[0].BootstrapURL,
		"BootstrapURL 应正确拼接")
}

// TestAppInitializeIsIdempotentForBindingWaiting 验证应用初始化保持幂等针对绑定 Waiting 的特殊分支或幂等场景。
func TestAppInitializeIsIdempotentForBindingWaiting(t *testing.T) {
	store := newAppInitStub(t)
	store.app.Status = domain.AppStatusBindingWaiting
	store.app.ApiKeyStatus = domain.APIKeyStatusActive
	client := &fakeNewAPI{}

	handler := NewAppInitializeHandler(store, client, AppInitializeConfig{})
	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	require.NoError(t, err)
	require.Equal(t, 0, client.calls)
	require.False(t, store.statusSet)
}

// TestAppInitializeSkipsAPIKeyWhenAlreadyActive 验证应用初始化跳过 APIKey 当已经启用的特殊分支或幂等场景。
func TestAppInitializeSkipsAPIKeyWhenAlreadyActive(t *testing.T) {
	store := newAppInitStub(t)
	cipher := testCipher(t)
	encrypted, err := cipher.Encrypt([]byte("sk-old-cached"))
	require.NoError(t, err)
	store.app.ApiKeyStatus = domain.APIKeyStatusActive
	// NewapiKeyCiphertext 迁移为 null.String。
	store.app.NewapiKeyCiphertext = null.StringFrom(encrypted)
	client := &fakeNewAPI{}

	handler := NewAppInitializeHandler(store, client, AppInitializeConfig{Cipher: cipher, ResolveRuntimeImage: testResolveRuntimeImage})
	err = handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	require.NoError(t, err)
	require.Equal(t, 0, client.calls)
	require.True(t, store.statusSet)
}

// TestAppInitializePropagatesNewAPIError 验证应用初始化透传 new-api 错误的错误映射或错误记录场景。
func TestAppInitializePropagatesNewAPIError(t *testing.T) {
	store := newAppInitStub(t)
	client := &fakeNewAPI{err: newapi.ErrUpstream}

	handler := NewAppInitializeHandler(store, client, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})
	err := handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	require.ErrorIs(t, err, newapi.ErrUpstream)
	// new-api 调用在 phasePrepare 内 ensureAPIKey 阶段失败：MarkAppFailed 被调用，
	// last_error_status 记为 preparing_runtime，app.status 收敛到 error。
	require.True(t, store.failedSet, "new-api 失败应触发 MarkAppFailed")
	require.True(t, store.lastFailed.LastErrorStatus.Valid)
	assert.Equal(t, domain.AppStatusPreparingRuntime, store.lastFailed.LastErrorStatus.String)
	assert.Equal(t, domain.AppStatusError, store.app.Status)
}

// TestAppInitializeRejectsInvalidPayload 验证应用初始化拒绝非法载荷的异常或拒绝路径场景。
func TestAppInitializeRejectsInvalidPayload(t *testing.T) {
	store := newAppInitStub(t)
	handler := NewAppInitializeHandler(store, &fakeNewAPI{}, AppInitializeConfig{})

	job := sqlc.Job{Type: domain.JobTypeAppInitialize, PayloadJson: []byte(`{"runtime_node":"node-1"}`)}
	err := handler.Handle(context.Background(), job)
	require.Error(t, err)
}

// TestAppInitializeContainerStepSkippedWhenContainerExists 验证 ContainerID 已存在时
// 旧字段保留不报错（k8s 路径不再使用 ContainerID，但字段保留兼容性）。
func TestAppInitializeContainerStepSkippedWhenContainerExists(t *testing.T) {
	store := newAppInitStub(t)
	// ContainerID 迁移为 null.String；k8s 路径忽略此字段。
	store.app.ContainerID = null.StringFrom("already-there")
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}

	handler := NewAppInitializeHandler(store, client, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})
	// k8s 路径不需要 AppInputUploader，直接调用。
	err := handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	require.NoError(t, err)
	// ContainerID 字段本身不影响 k8s 路径，不应触发写 container 操作。
	require.False(t, store.containerSet, "k8s 路径不应写 container_id")
}

// TestEnsureAPIKeyKeepsNewAPITokenModelsUnrestricted 验证 new-api token 创建不限制模型。
func TestEnsureAPIKeyKeepsNewAPITokenModelsUnrestricted(t *testing.T) {
	store := newAppInitStub(t)
	api := &fakeNewAPI{result: newapi.APIKey{ID: 99, Key: "sk-test"}}
	handler := NewAppInitializeHandler(store, api, AppInitializeConfig{
		Cipher: testCipher(t),
	})

	_, err := handler.ensureAPIKey(context.Background(), &store.app)

	require.NoError(t, err)
	assert.Empty(t, api.lastCreateInput.Models)
}

// TestProvisionAPIKeyPersistsKeyName 校验实例初始化链路把 new-api 侧 token name
// (当前实现 = "app-" + app.ID) 显式落到 apps.newapi_key_name, 供 usage 查询直接读。
func TestProvisionAPIKeyPersistsKeyName(t *testing.T) {
	store := newAppInitStub(t)
	api := &fakeNewAPI{result: newapi.APIKey{ID: 42, Key: "sk-test"}}
	handler := NewAppInitializeHandler(store, api, AppInitializeConfig{
		Cipher: testCipher(t),
	})

	// 走完 ensureAPIKey 完整流程: CreateAPIKey + GetTokenFullKey + 加密 + SetAppNewAPIKey。
	_, err := handler.ensureAPIKey(context.Background(), &store.app)
	require.NoError(t, err)

	expectedName := "app-" + testAppID
	require.True(t, store.apiKeySet, "SetAppNewAPIKey 应被调用")
	assert.True(t, store.lastSetAPIKey.NewapiKeyName.Valid, "newapi_key_name 应被显式落库为 Valid")
	assert.Equal(t, expectedName, store.lastSetAPIKey.NewapiKeyName.String, "newapi_key_name 应等于 CreateAPIKey 的 Name")
	assert.Equal(t, expectedName, api.lastCreateInput.Name, "new-api 侧 token name 也应使用同一字符串, 保持双向一致")
}

func buildJob(t *testing.T, appID, nodeID string) sqlc.Job {
	t.Helper()
	payload := []byte(`{"app_id":"` + appID + `","runtime_node":"` + nodeID + `"}`)
	return sqlc.Job{Type: domain.JobTypeAppInitialize, PayloadJson: payload}
}

// appInitStub 实现 AppInitializeStore 接口；迁移后 ID 字段均为 string。
type appInitStub struct {
	t    *testing.T
	app  sqlc.App
	org  sqlc.Organization
	user sqlc.User
	node sqlc.RuntimeNode
	// versions 按 string UUID 存放助手版本；GetAssistantVersion 从此 map 查找。
	versions     map[string]sqlc.AssistantVersion
	apiKeySet    bool
	statusSet    bool
	containerSet bool
	// lastSetAPIKey 记录最近一次 SetAppNewAPIKey 调用的入参, 用于断言落库字段
	// (特别是 newapi_key_name 是否与 new-api CreateAPIKey 用的 token name 一致)。
	lastSetAPIKey sqlc.SetAppNewAPIKeyParams
	auditLogs     []sqlc.CreateAuditLogParams
	// statusCalls 按顺序记录每次 SetAppStatus 调用参数, 用于断言 4 阶段推进序列
	// (draft → pulling_runtime_image → ... → binding_waiting)。
	statusCalls []sqlc.SetAppStatusParams
	// failedSet 标记 MarkAppFailed 是否被调用, 用于失败路径精确断言。
	failedSet bool
	// lastFailed 记录最近一次 MarkAppFailed 参数, 用于断言 last_error_status 写入值。
	lastFailed sqlc.MarkAppFailedParams
	// getOrganizationErr 让 GetOrganization 返回指定错误（保留供其他测试使用）。
	getOrganizationErr error
	// appliedVersionSet 标记 SetAppAppliedVersion 是否被调用。
	appliedVersionSet bool
	// lastAppliedVersion 记录最近一次 SetAppAppliedVersion 的入参，供断言使用。
	lastAppliedVersion sqlc.SetAppAppliedVersionParams
	// channelBound 让 AppHasBoundChannelBinding 返回的 bool 值受测试控制；
	// 默认 false 保持「init 进入 binding_waiting 不再续推」的原行为。
	channelBound bool
	// hasBoundCalls 记录 AppHasBoundChannelBinding 被调用次数，
	// 供「init 完成 / 幂等分支 都应触发自愈探测」的用例断言。
	hasBoundCalls int
}

// newAppInitStub 构造 appInitStub；ID 字段迁移为 string（MySQL uuid）。
func newAppInitStub(t *testing.T) *appInitStub {
	t.Helper()
	// 默认助手版本：主模型 gpt-4o，含路由与 persona，供 happy path 测试使用。
	defaultVersion := sqlc.AssistantVersion{
		ID:           testVersionID,
		Name:         "v1",
		MainModel:    "gpt-4o",
		SystemPrompt: "你是 {org_name} 的专属助手",
		ImageID:      "hermes-v1",
		Revision:     1,
		RoutingJson:  []byte(`{"aux1":"gpt-3.5-turbo"}`),
		SkillsJson:   []byte(`[]`),
	}
	return &appInitStub{
		t: t,
		app: sqlc.App{
			ID:          testAppID,
			OrgID:       testOrgID,
			OwnerUserID: testUsrID,
			// Phase 4：实例必须绑定助手版本，否则 Handle 直接标记失败。
			// VersionID 迁移为 null.String；StringFrom 表示 Valid=true。
			VersionID:    null.StringFrom(testVersionID),
			Name:         "alice-bot",
			Status:       domain.AppStatusDraft,
			ApiKeyStatus: domain.APIKeyStatusPending,
		},
		org:  sqlc.Organization{Name: "测试组织", Status: domain.StatusActive},
		user: sqlc.User{DisplayName: "Alice"},
		node: sqlc.RuntimeNode{NodeDataRoot: null.StringFrom("/var/lib/oc-agent")},
		versions: map[string]sqlc.AssistantVersion{
			testVersionID: defaultVersion,
		},
	}
}

func (s *appInitStub) GetApp(_ context.Context, _ string) (sqlc.App, error) { return s.app, nil }
func (s *appInitStub) GetOrganization(_ context.Context, _ string) (sqlc.Organization, error) {
	if s.getOrganizationErr != nil {
		return sqlc.Organization{}, s.getOrganizationErr
	}
	return s.org, nil
}
func (s *appInitStub) GetUser(_ context.Context, _ string) (sqlc.User, error) {
	return s.user, nil
}

func (s *appInitStub) GetRuntimeNode(_ context.Context, _ string) (sqlc.RuntimeNode, error) {
	return s.node, nil
}

// SetAppNewAPIKey :exec 语义仅返回 error；留存入参供断言 newapi_key_name 等字段。
func (s *appInitStub) SetAppNewAPIKey(_ context.Context, arg sqlc.SetAppNewAPIKeyParams) error {
	s.apiKeySet = true
	s.lastSetAPIKey = arg
	s.app.ApiKeyStatus = arg.ApiKeyStatus
	s.app.NewapiKeyID = arg.NewapiKeyID
	s.app.NewapiKeyCiphertext = arg.NewapiKeyCiphertext
	s.app.NewapiKeyName = arg.NewapiKeyName
	return nil
}

// SetAppContainer :exec 语义仅返回 error；记录 container_id / container_name。
// k8s 路径不再写 container_id，保留供接口兼容。
func (s *appInitStub) SetAppContainer(_ context.Context, arg sqlc.SetAppContainerParams) error {
	s.containerSet = true
	s.app.ContainerID = arg.ContainerID
	s.app.ContainerName = arg.ContainerName
	return nil
}

// SetAppStatus :exec 语义仅返回 error；按调用顺序记录状态切换，便于断言阶段推进序列。
func (s *appInitStub) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) error {
	s.statusSet = true
	s.statusCalls = append(s.statusCalls, arg)
	s.app.Status = arg.Status
	return nil
}

// CreateAuditLog :exec 语义仅返回 error；存档入参供断言。
func (s *appInitStub) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) error {
	s.auditLogs = append(s.auditLogs, arg)
	return nil
}

// 以下 3 个 stub 覆盖 AppInitializeStore 中的进度与失败语义：
//   - SetAppProgress / ClearAppProgress：阶段切换 / Receive 触发的进度落库；
//     测试不关心字段值，仅需让 transitionTo → FlushReset 不报错。
//   - MarkAppFailed：阶段失败时被调用，通过 failedSet / lastFailed 让用例
//     断言"是否进入失败路径"以及 last_error_status 写入值。
func (s *appInitStub) SetAppProgress(_ context.Context, _ sqlc.SetAppProgressParams) error {
	return nil
}
func (s *appInitStub) ClearAppProgress(_ context.Context, _ string) error {
	return nil
}
func (s *appInitStub) MarkAppFailed(_ context.Context, p sqlc.MarkAppFailedParams) error {
	// 模拟真实 SQL：status 推到 error，last_error_status 记录来源 phase；
	// 同时记录 failedSet / lastFailed，供失败路径断言使用。
	s.failedSet = true
	s.lastFailed = p
	s.app.Status = domain.AppStatusError
	s.app.LastErrorStatus = p.LastErrorStatus
	return nil
}

// UpdateAppRuntimeImage 更新 app 的镜像引用与 sha256（k8s 路径不调用，保留接口兼容）。
func (s *appInitStub) UpdateAppRuntimeImage(_ context.Context, arg sqlc.UpdateAppRuntimeImageParams) error {
	s.app.RuntimeImageRef = arg.RuntimeImageRef
	s.app.RuntimeImageSha256 = arg.RuntimeImageSha256
	return nil
}

// GetAssistantVersion 从内存 versions map 返回版本，模拟 DB 查询。
// ID 迁移为 string；版本不存在时返回 sql.ErrNoRows（与真实 DB 行为一致）。
func (s *appInitStub) GetAssistantVersion(_ context.Context, id string) (sqlc.AssistantVersion, error) {
	if v, ok := s.versions[id]; ok {
		return v, nil
	}
	return sqlc.AssistantVersion{}, sql.ErrNoRows
}

// SetAppAppliedVersion :exec 语义仅返回 error；记录 applied 版本，供断言使用。
func (s *appInitStub) SetAppAppliedVersion(_ context.Context, arg sqlc.SetAppAppliedVersionParams) error {
	s.appliedVersionSet = true
	s.lastAppliedVersion = arg
	s.app.AppliedVersionRevision = arg.AppliedVersionRevision
	s.app.AppliedImageRef = arg.AppliedImageRef
	return nil
}

// SetAppRuntimeToken :exec 语义仅返回 error；记录 runtime API token 字段。
func (s *appInitStub) SetAppRuntimeToken(_ context.Context, arg sqlc.SetAppRuntimeTokenParams) error {
	s.app.RuntimeTokenHash = arg.RuntimeTokenHash
	s.app.RuntimeTokenCiphertext = arg.RuntimeTokenCiphertext
	return nil
}

// AppHasBoundChannelBinding 返回 channelBound 字段值；ID 迁移为 string（MySQL uuid）。
// hasBoundCalls 计数供断言「init 完成 / 幂等分支 都正确触发自愈探测」。
func (s *appInitStub) AppHasBoundChannelBinding(_ context.Context, _ string) (bool, error) {
	s.hasBoundCalls++
	return s.channelBound, nil
}

// fakeNewAPI 同时充当 NewAPIClientFactory 与 APIKeyClient：UserScopedFor 直接返回自身，
// 让现有用例（构造一次 fakeNewAPI 给 handler）继续通过；result 在 CreateAPIKey 与
// GetTokenFullKey 之间共用，模拟 new-api 创 token + 拉完整 key 这条新链路。
type fakeNewAPI struct {
	result          newapi.APIKey
	err             error // UserScopedFor / CreateAPIKey / SetAPIKeyStatus 公用错误
	createKeyErr    error // 仅让 CreateAPIKey 失败，UserScopedFor 仍成功
	getKeyErr       error // 仅让 GetTokenFullKey 失败，CreateAPIKey 仍成功
	calls           int
	lastCreateInput newapi.CreateAPIKeyInput
}

func (f *fakeNewAPI) UserScopedFor(_ context.Context, _ sqlc.App) (APIKeyClient, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f, nil
}

func (f *fakeNewAPI) CreateAPIKey(_ context.Context, input newapi.CreateAPIKeyInput) (newapi.APIKey, error) {
	f.calls++
	f.lastCreateInput = input
	if f.createKeyErr != nil {
		return newapi.APIKey{}, f.createKeyErr
	}
	if f.err != nil {
		return newapi.APIKey{}, f.err
	}
	return f.result, nil
}

// GetTokenFullKey 把 result.Key 作为完整 sk- 返回；测试里通过设置 result.Key 控制注入容器的值。
func (f *fakeNewAPI) GetTokenFullKey(_ context.Context, _ int64) (string, error) {
	if f.getKeyErr != nil {
		return "", f.getKeyErr
	}
	if f.err != nil {
		return "", f.err
	}
	if f.result.Key == "" {
		return "", fmt.Errorf("fakeNewAPI: result.Key 未设置")
	}
	return f.result.Key, nil
}

// SetAPIKeyStatus 在 newapi_key_status / app_runtime_ops 测试中被调用；
// 不真做事，仅通过 calls 计数让上层断言"发生了一次状态切换"。
func (f *fakeNewAPI) SetAPIKeyStatus(_ context.Context, _ int64, _ int) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	return nil
}

// testCipher 返回一个固定 key 的 cipher，所有 app_initialize 测试共用。
// 32 字节填零 key 仅做单测加解密一致性，不放入生产环境。
func testCipher(t *testing.T) *auth.Cipher {
	t.Helper()
	c, err := auth.NewCipher(make([]byte, 32))
	require.NoError(t, err)
	return c
}

// testRuntimeImageRef 是测试装配中 ResolveRuntimeImage 桩为默认版本 image_id
// "hermes-v1" 解析出的运行时镜像引用，供需要走完整 Handle 的用例断言容器镜像。
const testRuntimeImageRef = "hermes-runtime:v2026.5.16-test"

// testResolveRuntimeImage 是 AppInitializeConfig.ResolveRuntimeImage 的测试桩。
// Phase 5 起 ResolveRuntimeImage 是 Handle 解析运行时镜像的唯一来源、必需依赖，
// 任何走完整 Handle 的用例都必须注入它；这里把默认版本 image_id "hermes-v1"
// 映射到 testRuntimeImageRef，其余 id 返回未命中。
func testResolveRuntimeImage(imageID string) (string, bool) {
	if imageID == "hermes-v1" {
		return testRuntimeImageRef, true
	}
	return "", false
}

// mustUUIDForTest 在迁移后直接返回字符串 UUID（原来返回 pgtype.UUID）。
// 保留函数签名便于全文搜索；调用方断言逻辑不需要改动。
func mustUUIDForTest(_ *testing.T, value string) string {
	return value
}

// fakeAppInputUploader 实现 AppInputUploader，记录每次 UploadAppInputFile 调用。
// 保留供 restart 链路（AssembleVersionInputData）测试使用；app_initialize k8s 路径不再调用。
type fakeAppInputUploader struct {
	// mu 保护 calls 切片；handler 单 goroutine 跑，但桩做并发安全更稳妥。
	mu sync.Mutex
	// calls 按调用顺序记录每次上传的参数（nodeID / appID / relPath）。
	calls []fakeAppInputUploadCall
	// err 非 nil 时所有调用返回该错误。
	err error
}

// fakeAppInputUploadCall 记录单次 UploadAppInputFile 调用的参数。
type fakeAppInputUploadCall struct {
	nodeID  string
	appID   string
	relPath string
}

func (f *fakeAppInputUploader) UploadAppInputFile(_ context.Context, nodeID, appID, relPath string, content io.Reader) error {
	// 消耗 content，避免调用方 strings.NewReader 被留在半读状态。
	_, _ = io.ReadAll(content)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeAppInputUploadCall{nodeID: nodeID, appID: appID, relPath: relPath})
	return f.err
}

// hasUpload 检查是否存在针对给定 appID + relPath 的上传记录。
func (f *fakeAppInputUploader) hasUpload(appID, relPath string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.appID == appID && c.relPath == relPath {
			return true
		}
	}
	return false
}

// relPathsForApp 返回给定 appID 下所有上传的 relPath，顺序与调用顺序一致。
func (f *fakeAppInputUploader) relPathsForApp(appID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		if c.appID == appID {
			out = append(out, c.relPath)
		}
	}
	return out
}

// fakeAuditRecorder 实现 audit.AuditRecorder，用于断言审计事件被写入。
type fakeAuditRecorder struct {
	events []service.AuditEvent
}

func (f *fakeAuditRecorder) Record(_ context.Context, event service.AuditEvent) (service.AuditResult, error) {
	f.events = append(f.events, event)
	return service.AuditResult{}, nil
}

// TestEnsureAPIKey_CreateAPIKeyFailureRecordsAudit 验证确保 APIKey 创建 APIKey 失败记录审计的错误映射或错误记录场景。
func TestEnsureAPIKey_CreateAPIKeyFailureRecordsAudit(t *testing.T) {
	store := newAppInitStub(t)
	rec := &fakeAuditRecorder{}
	helper := audit.NewNewAPIAuditHelper(rec)
	// UserScopedFor 成功，CreateAPIKey 失败
	client := &fakeNewAPI{createKeyErr: newapi.ErrUpstream}

	cfg := AppInitializeConfig{
		Cipher:              testCipher(t),
		AuditHelper:         helper,
		ResolveRuntimeImage: testResolveRuntimeImage,
	}
	handler := NewAppInitializeHandler(store, client, cfg)

	err := handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	require.ErrorIs(t, err, newapi.ErrUpstream)
	require.Equal(t, 1, len(rec.events))
	require.Equal(t, "newapi_call", rec.events[0].TargetType)
	require.Equal(t, "failed", rec.events[0].Result)
}

// TestEnsureAPIKey_GetTokenFullKeyFailureRecordsAudit 验证 GetTokenFullKey 失败时
// 审计事件被记录的错误映射或错误记录场景。
func TestEnsureAPIKey_GetTokenFullKeyFailureRecordsAudit(t *testing.T) {
	store := newAppInitStub(t)
	rec := &fakeAuditRecorder{}
	helper := audit.NewNewAPIAuditHelper(rec)
	// CreateAPIKey 成功，GetTokenFullKey 失败
	getKeyErr := errors.New("get-key-fail")
	client := &fakeNewAPI{
		result:    newapi.APIKey{ID: 42, Key: ""},
		getKeyErr: getKeyErr,
	}

	cfg := AppInitializeConfig{
		Cipher:              testCipher(t),
		AuditHelper:         helper,
		ResolveRuntimeImage: testResolveRuntimeImage,
	}
	handler := NewAppInitializeHandler(store, client, cfg)

	err := handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	if err == nil || !strings.Contains(err.Error(), "取完整 sk-") {
		t.Fatalf("err = %v", err)
	}
	require.Equal(t, 1, len(rec.events))
	require.Equal(t, "newapi_call", rec.events[0].TargetType)
	// Endpoint 应含 token ID
	require.True(t, strings.Contains(rec.events[0].TargetID, "42"))
}

// TestAppInitializeHandler_Phases_Progress 验证 k8s 路径 4 阶段每阶段都把 status 推进一格，
// 共 5 次 SetAppStatus（4 个 init 子状态 + binding_waiting）。
func TestAppInitializeHandler_Phases_Progress(t *testing.T) {
	store := newAppInitStub(t)
	// 起始 status=draft，模拟新建 app
	store.app.Status = domain.AppStatusDraft

	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "sk-test"}}
	handler := NewAppInitializeHandler(store, client, AppInitializeConfig{
		Cipher:              testCipher(t),
		ResolveRuntimeImage: testResolveRuntimeImage,
	})
	// orch 不注入，phaseCreate/phaseStart 直接跳过，但状态仍按顺序推进。

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "")))

	// 期望按顺序触发：pulling_runtime_image → preparing_runtime →
	// creating_container → starting → binding_waiting，共 5 次状态切换。
	wantStatuses := []string{
		domain.AppStatusPullingRuntimeImage,
		domain.AppStatusPreparingRuntime,
		domain.AppStatusCreatingContainer,
		domain.AppStatusStarting,
		domain.AppStatusBindingWaiting,
	}
	require.Len(t, store.statusCalls, len(wantStatuses), "应触发 5 次 SetAppStatus")
	for i, want := range wantStatuses {
		assert.Equal(t, want, store.statusCalls[i].Status, "第 %d 次状态切换应推到 %s", i+1, want)
	}
	// 终态应为 binding_waiting。
	assert.Equal(t, domain.AppStatusBindingWaiting, store.app.Status)
	// happy path 不应触发 MarkAppFailed。
	assert.False(t, store.failedSet, "成功路径不应调用 MarkAppFailed")
}

// TestAppInitializeHandler_Phases_FailureWritesLastError 表驱动覆盖 k8s 路径各阶段失败时
// MarkAppFailed 把 last_error_status 写为该阶段名；同时验证 app.status 收敛到 error。
//
// 各阶段失败方式：
//   - phasePrepare：new-api 调用失败（ensureAPIKey 内）
//   - phaseCreate：EnsureApp 返回错误
//   - phaseStart：WaitReady 返回错误
func TestAppInitializeHandler_Phases_FailureWritesLastError(t *testing.T) {
	cases := []struct {
		// name 说明该 case 触发哪一阶段失败
		name string
		// expect 是该阶段名，期望写入 MarkAppFailed.LastErrorStatus
		expect string
		// build 根据 case 类型构造特定失败行为的 handler 与 store
		build func(t *testing.T) (*AppInitializeHandler, *appInitStub)
	}{
		{
			// phasePrepare 失败：ensureAPIKey 内 new-api 调用返回 error
			name:   "phasePrepare 失败写入 preparing_runtime",
			expect: domain.AppStatusPreparingRuntime,
			build: func(t *testing.T) (*AppInitializeHandler, *appInitStub) {
				s := newAppInitStub(t)
				s.app.Status = domain.AppStatusDraft
				client := &fakeNewAPI{err: errors.New("new-api down")}
				h := NewAppInitializeHandler(s, client, AppInitializeConfig{
					Cipher:              testCipher(t),
					ResolveRuntimeImage: testResolveRuntimeImage,
				})
				return h, s
			},
		},
		{
			// phaseCreate 失败：EnsureApp 返回 error
			name:   "phaseCreate 失败写入 creating_container",
			expect: domain.AppStatusCreatingContainer,
			build: func(t *testing.T) (*AppInitializeHandler, *appInitStub) {
				s := newAppInitStub(t)
				s.app.Status = domain.AppStatusDraft
				client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
				h := NewAppInitializeHandler(s, client, AppInitializeConfig{
					Cipher:              testCipher(t),
					ResolveRuntimeImage: testResolveRuntimeImage,
				})
				orch := &fakeOrchestrator{ensureAppErr: errors.New("k8s apply failed")}
				h.SetOrchestrator(orch, AppInitializeK8sConfig{})
				return h, s
			},
		},
		{
			// phaseStart 失败：WaitReady 返回 error
			name:   "phaseStart 失败写入 starting",
			expect: domain.AppStatusStarting,
			build: func(t *testing.T) (*AppInitializeHandler, *appInitStub) {
				s := newAppInitStub(t)
				s.app.Status = domain.AppStatusDraft
				client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
				h := NewAppInitializeHandler(s, client, AppInitializeConfig{
					Cipher:              testCipher(t),
					ResolveRuntimeImage: testResolveRuntimeImage,
				})
				orch := &fakeOrchestrator{waitReadyErr: errors.New("pod timeout")}
				h.SetOrchestrator(orch, AppInitializeK8sConfig{})
				return h, s
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, s := c.build(t)
			err := h.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
			require.Error(t, err, "失败 stub 应返回 error")
			// MarkAppFailed 必须被调用，LastErrorStatus 应等于 case 期望阶段。
			require.True(t, s.failedSet, "MarkAppFailed 应被调用")
			require.True(t, s.lastFailed.LastErrorStatus.Valid)
			assert.Equal(t, c.expect, s.lastFailed.LastErrorStatus.String)
			// 终态应收敛到 error。
			assert.Equal(t, domain.AppStatusError, s.app.Status)
		})
	}
}

// TestAppInitializeHandler_IdempotentReentry 模拟 manager 在前次 init 跑到
// EnsureApp 之后才崩溃 / 重启；reaper 把 status 重置回 draft 重跑，
// 但 EnsureApp 本身幂等（k8s apply，已存在即更新），WaitReady 再等一次即可。
func TestAppInitializeHandler_IdempotentReentry(t *testing.T) {
	store := newAppInitStub(t)
	// app 起始 draft，模拟 reaper 重置 status 保留其余字段。
	store.app.Status = domain.AppStatusDraft

	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	handler := NewAppInitializeHandler(store, client, AppInitializeConfig{
		Cipher:              testCipher(t),
		ResolveRuntimeImage: testResolveRuntimeImage,
	})
	// orch 注入，验证 EnsureApp 被调用（幂等 apply）。
	orch := &fakeOrchestrator{}
	handler.SetOrchestrator(orch, AppInitializeK8sConfig{})

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "")))

	// EnsureApp 仍应被调用（幂等 apply），WaitReady 也应被调用。
	assert.Equal(t, 1, len(orch.ensureAppCalls), "重入时 EnsureApp 应被幂等调用")
	assert.Equal(t, 1, len(orch.waitReadyCalls), "重入时 WaitReady 应被调用")
	// 终态应推进到 binding_waiting。
	assert.Equal(t, domain.AppStatusBindingWaiting, store.app.Status)
	// 不应触发失败。
	assert.False(t, store.failedSet)
}

// --- Phase 4 测试 ---

// TestAppInitialize_NullVersionIDFails 验证实例未绑定助手版本时
// Handle 直接标记失败，不进入任何阶段推进。
// 覆盖场景：app.VersionID.Valid == false → markFailed 被调用，错误含"未绑定助手版本"。
func TestAppInitialize_NullVersionIDFails(t *testing.T) {
	store := newAppInitStub(t)
	// 清空 VersionID，模拟未绑定版本的实例。VersionID 迁移为 null.String；零值表示 NULL。
	store.app.VersionID = null.String{}

	handler := NewAppInitializeHandler(store, &fakeNewAPI{}, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})

	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	// 未绑定版本应立即失败，错误信息应含"未绑定助手版本"。
	require.Error(t, err)
	require.Contains(t, err.Error(), "未绑定助手版本")
	// MarkAppFailed 必须被调用。
	require.True(t, store.failedSet, "未绑定版本应触发 MarkAppFailed")
	assert.Equal(t, domain.AppStatusError, store.app.Status)
}

// TestAppInitialize_GetAssistantVersionErrorFails 验证 GetAssistantVersion 返回错误时
// Handle 直接标记失败，不进入任何阶段推进。
// 覆盖场景：app.VersionID.Valid == true 但版本加载失败 → markFailed 被调用，
// last_error_status 记为 pulling_runtime_image，错误信息含"加载助手版本失败"。
func TestAppInitialize_GetAssistantVersionErrorFails(t *testing.T) {
	store := newAppInitStub(t)
	// 清空 versions map，使 GetAssistantVersion 对有效 VersionID 返回 sql.ErrNoRows。
	store.versions = map[string]sqlc.AssistantVersion{}

	handler := NewAppInitializeHandler(store, &fakeNewAPI{}, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})

	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	// 版本加载失败应立即返回错误，错误信息应含"加载助手版本失败"。
	require.Error(t, err)
	require.Contains(t, err.Error(), "加载助手版本失败")
	// MarkAppFailed 必须被调用，app.status 收敛到 error。
	require.True(t, store.failedSet, "版本加载失败应触发 MarkAppFailed")
	assert.Equal(t, domain.AppStatusError, store.app.Status)
	// last_error_status 记为 pulling_runtime_image（版本加载属于初始化前置步骤）。
	require.True(t, store.lastFailed.LastErrorStatus.Valid)
	assert.Equal(t, domain.AppStatusPullingRuntimeImage, store.lastFailed.LastErrorStatus.String)
	// last_error_message 应反映版本加载失败原因。
	require.True(t, store.lastFailed.LastErrorMessage.Valid)
	assert.Contains(t, store.lastFailed.LastErrorMessage.String, "加载助手版本失败")
}

// TestAppInitialize_AppliedVersionRecorded 验证初始化成功后
// SetAppAppliedVersion 以正确的 revision 和 imageRef 被调用。
// 覆盖场景：happy path → appliedVersionSet=true + revision/imageRef 与版本数据一致。
func TestAppInitialize_AppliedVersionRecorded(t *testing.T) {
	store := newAppInitStub(t)
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "sk-test"}}

	// 注入 ResolveRuntimeImage：版本 image_id "hermes-v1" → "hermes:v2026-test"。
	resolvedRef := "hermes:v2026-test"
	handler := NewAppInitializeHandler(store, client, AppInitializeConfig{
		Cipher: testCipher(t),
		ResolveRuntimeImage: func(imageID string) (string, bool) {
			if imageID == "hermes-v1" {
				return resolvedRef, true
			}
			return "", false
		},
	})

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "")))

	// 初始化成功后 SetAppAppliedVersion 必须被调用。
	require.True(t, store.appliedVersionSet, "初始化成功应调用 SetAppAppliedVersion")
	// revision 应与版本 stub 中的 Revision(=1) 一致。
	assert.Equal(t, int32(1), store.lastAppliedVersion.AppliedVersionRevision, "applied_version_revision 应等于版本 Revision")
	// applied_image_ref 应等于 ResolveRuntimeImage 解析出的 ref。
	assert.Equal(t, resolvedRef, store.lastAppliedVersion.AppliedImageRef, "applied_image_ref 应等于解析出的镜像 ref")
}

// TestAppInitialize_PromotesToRunningWhenChannelAlreadyBound 验证切换助手版本+重启
// 触发镜像重建后的「已绑定渠道」自愈：app_initialize 完整跑完 4 阶段进入
// binding_waiting 后，若 AppHasBoundChannelBinding 返回 true，
// handler 应继续把 status 推到 running，避免概览页与渠道页状态不一致。
func TestAppInitialize_PromotesToRunningWhenChannelAlreadyBound(t *testing.T) {
	store := newAppInitStub(t)
	store.app.Status = domain.AppStatusDraft
	// 关键前置：渠道已 bound。
	store.channelBound = true

	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "sk-test"}}
	handler := NewAppInitializeHandler(store, client, AppInitializeConfig{
		Cipher:              testCipher(t),
		ResolveRuntimeImage: testResolveRuntimeImage,
	})

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "")))

	// 期望 6 次状态切换：4 阶段 init + binding_waiting + 自愈推到 running。
	wantStatuses := []string{
		domain.AppStatusPullingRuntimeImage,
		domain.AppStatusPreparingRuntime,
		domain.AppStatusCreatingContainer,
		domain.AppStatusStarting,
		domain.AppStatusBindingWaiting,
		domain.AppStatusRunning,
	}
	require.Len(t, store.statusCalls, len(wantStatuses), "渠道已 bound 应再触发一次 SetAppStatus(running)")
	for i, want := range wantStatuses {
		assert.Equal(t, want, store.statusCalls[i].Status, "第 %d 次状态切换应推到 %s", i+1, want)
	}
	// 终态应为 running。
	assert.Equal(t, domain.AppStatusRunning, store.app.Status)
	// 自愈探测应至少调用一次（init 完成后那次）。
	assert.GreaterOrEqual(t, store.hasBoundCalls, 1, "init 完成后应触发渠道绑定快照查询")
	assert.False(t, store.failedSet, "自愈路径不应触发 MarkAppFailed")
}

// TestAppInitialize_StaysBindingWaitingWhenNoChannelBound 反向断言：在 channelBound=false
// 的默认场景下，init 走完仍应停在 binding_waiting，保持「等待用户扫码绑定」的原行为。
func TestAppInitialize_StaysBindingWaitingWhenNoChannelBound(t *testing.T) {
	store := newAppInitStub(t)
	store.app.Status = domain.AppStatusDraft
	// 渠道未 bound：保持原行为，等渠道扫码完成由 finalizeChannelBound 推到 running。
	store.channelBound = false

	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "sk-test"}}
	handler := NewAppInitializeHandler(store, client, AppInitializeConfig{
		Cipher:              testCipher(t),
		ResolveRuntimeImage: testResolveRuntimeImage,
	})

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "")))

	// 终态应为 binding_waiting：不应被错误地推到 running。
	assert.Equal(t, domain.AppStatusBindingWaiting, store.app.Status)
	// 最后一次 SetAppStatus 应是 binding_waiting。
	require.Greater(t, len(store.statusCalls), 0)
	assert.Equal(t, domain.AppStatusBindingWaiting, store.statusCalls[len(store.statusCalls)-1].Status)
	// 自愈探测仍应被调用一次。
	assert.Equal(t, 1, store.hasBoundCalls, "init 完成后应至少查一次渠道绑定快照")
}

// TestAppInitialize_IdempotentBindingWaitingPromotesWhenChannelBound 验证 Handle 入口
// 的 binding_waiting 幂等分支也带「自愈」能力：当 app 已经卡在 binding_waiting 但
// 渠道实际已 bound 时，worker 下一次重入 init job 时应当能把状态收敛到 running。
func TestAppInitialize_IdempotentBindingWaitingPromotesWhenChannelBound(t *testing.T) {
	store := newAppInitStub(t)
	store.app.Status = domain.AppStatusBindingWaiting
	store.app.ApiKeyStatus = domain.APIKeyStatusActive
	store.channelBound = true
	client := &fakeNewAPI{}

	handler := NewAppInitializeHandler(store, client, AppInitializeConfig{})
	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")))

	// 幂等分支不会走 k8s 编排，但应触发一次自愈推进。
	assert.Equal(t, 0, client.calls)
	require.Len(t, store.statusCalls, 1, "幂等分支命中自愈应仅触发一次 SetAppStatus")
	assert.Equal(t, domain.AppStatusRunning, store.statusCalls[0].Status)
	assert.Equal(t, domain.AppStatusRunning, store.app.Status)
}

// TestAppInitialize_VersionModelWrittenToManifest 验证版本 MainModel 通过 BuildAppInputData
// 正确写入 manifest，而非使用默认值。
// 覆盖场景：版本 MainModel="gpt-4o"，opts.DefaultModel="default-model"
// → BuildAppInputData 优先采用版本 MainModel。
func TestAppInitialize_VersionModelWrittenToManifest(t *testing.T) {
	// 仅测试 BuildAppInputData 纯函数，不走完整 Handle 流程。
	app := sqlc.App{
		ID:   testAppID,
		Name: "test-app",
	}
	org := sqlc.Organization{Name: "TestOrg"}
	owner := sqlc.User{DisplayName: "Bob"}

	// 版本 MainModel 非空时，model 字段必须使用版本值，不受默认值影响。
	in := BuildAppInputData(app, org, owner, "sk-x", AppInputVersionData{
		MainModel: "gpt-4o",
	}, AppInputBuildOptions{DefaultModel: "default-model"})
	assert.Equal(t, "gpt-4o", in.Model, "版本 MainModel 非空时应优先于 DefaultModel")

	// 版本 MainModel 为空时，退回 opts.DefaultModel。
	inFallback := BuildAppInputData(app, org, owner, "sk-x", AppInputVersionData{
		MainModel: "",
	}, AppInputBuildOptions{DefaultModel: "default-model"})
	assert.Equal(t, "default-model", inFallback.Model, "版本 MainModel 为空时应退回 DefaultModel")

	// 版本 MainModel 和 DefaultModel 都为空时，写 "default" 占位。
	inDouble := BuildAppInputData(app, org, owner, "sk-x", AppInputVersionData{}, AppInputBuildOptions{})
	assert.Equal(t, "default", inDouble.Model, "两者都为空时应写 default 占位")
}

// TestAppInitialize_VersionRoutingAndPersonaPassedThrough 验证版本 Routing 和 SystemPrompt
// 通过 BuildAppInputData 原样传递到 hermes.AppInputData。
func TestAppInitialize_VersionRoutingAndPersonaPassedThrough(t *testing.T) {
	// 仅测试 BuildAppInputData 纯函数。
	app := sqlc.App{ID: testAppID, Name: "r-app"}
	org := sqlc.Organization{Name: "O"}
	owner := sqlc.User{DisplayName: "U"}
	routing := map[string]string{"aux1": "claude-3-haiku", "aux2": "gpt-3.5-turbo"}
	persona := "你是 {org_name} 的路由助手，优先使用 aux1 做摘要。"

	in := BuildAppInputData(app, org, owner, "sk-y", AppInputVersionData{
		MainModel:     "gpt-4o",
		Routing:       routing,
		SystemPrompt:  persona,
		SkillRelPaths: []string{"resources/skills/search.tar"},
	}, AppInputBuildOptions{PlatformPrompt: "平台规则"})

	// Routing 原样透传到 AppInputData。
	assert.Equal(t, routing, in.Routing, "版本 Routing 应原样写入 AppInputData.Routing")
	// SystemPrompt 映射到 PersonaText。
	assert.Equal(t, persona, in.PersonaText, "版本 SystemPrompt 应映射到 AppInputData.PersonaText")
	// SkillRelPaths 原样透传。
	assert.Equal(t, []string{"resources/skills/search.tar"}, in.SkillRelPaths, "版本 SkillRelPaths 应原样写入 AppInputData.SkillRelPaths")
	// PlatformPrompt 来自 opts，原样透传。
	assert.Equal(t, "平台规则", in.PlatformRule, "opts.PlatformPrompt 应写入 AppInputData.PlatformRule")
}

// fakeSkillBlobReader 实现 SkillBlobReader，内存存储 skill tar 内容。
// 保留供 restart 链路（AssembleVersionInputData）测试使用。
type fakeSkillBlobReader struct {
	// blobs 以 relPath 为 key，存储伪 tar 内容。
	blobs map[string]string
	// errOnPath 若非空，当 relPath 等于此值时返回错误。
	errOnPath string
}

func (f *fakeSkillBlobReader) OpenSkill(relPath string) (io.ReadCloser, error) {
	if f.errOnPath != "" && relPath == f.errOnPath {
		return nil, fmt.Errorf("mock open skill error: %s", relPath)
	}
	content, ok := f.blobs[relPath]
	if !ok {
		return nil, fmt.Errorf("skill not found: %s", relPath)
	}
	return io.NopCloser(strings.NewReader(content)), nil
}
