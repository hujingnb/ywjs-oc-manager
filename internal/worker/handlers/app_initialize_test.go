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
	dockerclient "github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/audit"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	runtimepkg "oc-manager/internal/integrations/runtime"
	imagecoord "oc-manager/internal/runtime/imagecoord"
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

// TestAppInitializeHandlesHappyPath 验证应用初始化处理成功路径的成功路径场景:
// hermes-agent-pull 切换后, manifest.yaml + 4 份 resources/*.md 必须经
// AppInputUploader 写入 apps/<id>/input/ 目录。
func TestAppInitializeHandlesHappyPath(t *testing.T) {
	store := newAppInitStub(t)
	dirs := &fakeDirs{}
	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID, Status: "created"}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 99, Key: "sk-test"}}
	up := &fakeAppInputUploader{}

	cipher, err := auth.NewCipher(make([]byte, 32))
	require.NoError(t, err)
	cfg := AppInitializeConfig{
		PlatformPrompt:       "平台默认规则",
		SystemPromptTemplate: "你是 {org_name} 的助手",
		NewAPIBaseURL:        "http://new-api:3000",
		Cipher:               cipher,
		ResolveRuntimeImage:  testResolveRuntimeImage,
	}
	handler := NewAppInitializeHandler(store, dirs, containers, containers, client, cfg)
	// 注入 fakeAppInputUploader, 验证 hermes 输入资源经 UploadAppInputFile 上传到目标节点。
	handler.SetAppInputUploader(up)

	err = handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	require.NoError(t, err)
	if !store.apiKeySet || !store.statusSet || !store.containerSet {
		t.Fatalf("api_key/status/container 应当都被持久化: %+v", store)
	}

	// container_id 写库为 docker mock 返回的 ID。
	require.Equal(t, "ctr-1", store.app.ContainerID.String)

	// ciphertext 必须可被同一 cipher 解回 sk-test, 证明真的走了加密路径。
	plain, err := cipher.Decrypt(store.app.NewapiKeyCiphertext.String)
	require.NoError(t, err)
	require.Equal(t, "sk-test", string(plain))
	require.NotEqual(t, "sk-test", store.app.NewapiKeyCiphertext.String)

	// Hermes 容器规格断言: 两个挂载——input(ro) + data(rw)，分别承担
	// oc-entrypoint 输入清单和 hermes 运行期数据，两份不能合并避免容器内
	// 意外覆盖只读清单。
	require.Equal(t, 2, len(containers.lastSpec.Volumes))

	// mount[0]: apps/<id>/input → /opt/oc-input (ro)，承载 manifest.yaml 与
	// resources/*.md，oc-entrypoint 启动时只读消费。
	inputMount := containers.lastSpec.Volumes[0]
	require.Equal(t, "/opt/oc-input", inputMount.ContainerPath)
	require.True(t, inputMount.ReadOnly, "input 挂载必须为只读")
	require.Contains(t, inputMount.HostPath, "/apps/"+testAppID+"/input", "input HostPath 应指向 apps/<id>/input")

	// mount[1]: apps/<id>/data → /opt/data (rw)，承载 hermes workspace、
	// 渲染后的 config.yaml、sqlite、日志等可写数据。
	dataMount := containers.lastSpec.Volumes[1]
	require.Equal(t, "/opt/data", dataMount.ContainerPath)
	require.False(t, dataMount.ReadOnly, "data 挂载必须为读写")
	require.Contains(t, dataMount.HostPath, "/apps/"+testAppID+"/data", "data HostPath 应指向 apps/<id>/data")

	// docker Env 中不能再出现 OPENAI_API_KEY / OPENAI_BASE_URL：业务配置统一走
	// manifest.yaml → oc-entrypoint 渲染 config.yaml，避免双路注入语义漂移。
	_, hasAPIKey := containers.lastSpec.Env["OPENAI_API_KEY"]
	require.False(t, hasAPIKey, "OPENAI_API_KEY 不应通过 docker Env 注入")
	_, hasBaseURL := containers.lastSpec.Env["OPENAI_BASE_URL"]
	require.False(t, hasBaseURL, "OPENAI_BASE_URL 不应通过 docker Env 注入")

	// 容器名应以 hermes- 为前缀。
	require.Equal(t, "hermes-"+testAppID, containers.lastSpec.Name)
	// 容器镜像应为 ResolveRuntimeImage 桩按版本 image_id 解析出的 ref。
	require.Equal(t, testRuntimeImageRef, containers.lastSpec.Image)

	// InitAppDirs 与 StartContainer 必须被调对参数。
	if dirs.calls != 1 || dirs.lastNode != "node-1" || dirs.lastApp != testAppID {
		t.Fatalf("InitAppDirs 调用 = %+v", dirs)
	}
	if containers.startCalls != 1 || containers.lastStartNode != "node-1" || containers.lastStartID != "ctr-1" {
		t.Fatalf("StartContainer 调用 = calls=%d node=%s id=%s",
			containers.startCalls, containers.lastStartNode, containers.lastStartID)
	}
	require.Len(t, store.auditLogs, 1)
	require.Equal(t, "app", store.auditLogs[0].TargetType)
	require.Equal(t, testAppID, store.auditLogs[0].TargetID)
	require.Equal(t, "initialize", store.auditLogs[0].Action)
	require.Equal(t, "succeeded", store.auditLogs[0].Result)

	// manifest v2 必须上传 3 份文件: persona.md + platform-rules.md + manifest.yaml。
	// v2 不再写 organization-rules.md / application-rules.md（两字段已从 AppInputData 移除）。
	// 顺序断言: manifest.yaml 必须最后写, 避免 oc-entrypoint 读到 resources 文件还没就绪
	// 的中间态。
	relPaths := up.relPathsForApp(testAppID)
	require.ElementsMatch(t,
		[]string{
			"resources/persona.md",
			"resources/platform-rules.md",
			"manifest.yaml",
		},
		relPaths,
		"happy path 必须上传 persona.md + platform-rules.md + manifest.yaml; 实际: %v", relPaths,
	)
	require.Equal(t, "manifest.yaml", relPaths[len(relPaths)-1], "manifest.yaml 必须最后写")

	// 所有上传调用必须命中 node-1, 避免在多节点装配中误投递。
	for _, c := range up.calls {
		require.Equal(t, "node-1", c.nodeID, "上传节点应为 node-1")
	}
}

// TestWriteAppInput_FailsWhenUploaderNil 验证 writeAppInput 在 AppInputUploader 未注入
// 时直接报错, 而非静默跳过——hermes 容器没有 input/ 文件无法 bootstrap。
func TestWriteAppInput_FailsWhenUploaderNil(t *testing.T) {
	store := newAppInitStub(t)
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	// 不调 SetAppInputUploader, inputFiles 保持 nil。
	handler := NewAppInitializeHandler(store, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, client, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})

	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	// nodeID 非空时 writeAppInput 必须被调用; nil uploader 应立即报错。
	require.Error(t, err)
	require.Contains(t, err.Error(), "AppInputUploader 未注入")
}

// TestWriteAppInput_PropagatesUploadError 验证 UploadAppInputFile 返回错误时
// writeAppInput 正确透传错误, handler 不继续创建容器。
func TestWriteAppInput_PropagatesUploadError(t *testing.T) {
	store := newAppInitStub(t)
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	// 模拟 agent 上传失败 (网络不通 / 节点不可达)。
	up := &fakeAppInputUploader{err: errors.New("agent upload failed")}
	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "c", Name: "n"}}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})
	handler.SetAppInputUploader(up)

	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	// 上传失败应冒泡, 容器不应被创建。
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent upload failed")
	require.Equal(t, 0, containers.calls, "上传失败后不应创建容器")
}

// TestAppInitializeWaitsForHermesHealthyWhenSupported 验证应用初始化等待 Hermes 容器
// docker HEALTHCHECK 报 healthy 当 starter 实现 HermesHealthChecker 接口时的预期行为。
func TestAppInitializeWaitsForHermesHealthyWhenSupported(t *testing.T) {
	// starter 同时实现 HermesHealthChecker 时
	// handler 应等 docker HEALTHCHECK 报 healthy 再推 binding_waiting。
	store := newAppInitStub(t)
	dirs := &fakeDirs{}
	base := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID, Status: "created"}}
	// healthAwareContainers 包装 fakeContainers, 额外暴露 WaitContainerHealthy 方法。
	containers := &healthAwareContainers{fakeContainers: base}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 99, Key: "sk-test"}}

	cipher, err := auth.NewCipher(make([]byte, 32))
	require.NoError(t, err)
	handler := NewAppInitializeHandler(store, dirs, base, containers, client, AppInitializeConfig{
		Cipher:              cipher,
		ResolveRuntimeImage: testResolveRuntimeImage,
	})
	// 注入 fakeAppInputUploader, 确保 writeAppInput 可正常执行。
	handler.SetAppInputUploader(&fakeAppInputUploader{})
	err = handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	require.NoError(t, err)
	// 断言 WaitContainerHealthy 被调用了 1 次。
	require.Equal(t, 1, base.healthCalls)
}

// TestAppInitializePropagatesHealthCheckError 验证 WaitContainerHealthy 失败时
// handler 透传错误并不推进 binding_waiting 状态的错误传播场景。
func TestAppInitializePropagatesHealthCheckError(t *testing.T) {
	store := newAppInitStub(t)
	base := &fakeContainers{
		result:    runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID, Status: "created"},
		healthErr: errors.New("docker healthcheck timeout"),
	}
	containers := &healthAwareContainers{fakeContainers: base}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, base, containers, client, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})
	// 注入 fakeAppInputUploader, 使 writeAppInput 不报错, 聚焦健康检查失败路径。
	handler.SetAppInputUploader(&fakeAppInputUploader{})

	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	// 错误信息应包含"等待 Hermes 容器健康失败"。
	if err == nil || !strings.Contains(err.Error(), "等待 Hermes 容器健康失败") {
		t.Fatalf("err=%v", err)
	}
	// 健康检查在 phaseStart 中失败:MarkAppFailed 被调用, last_error_status 记为 starting,
	// app.status 收敛到 error。
	require.True(t, store.failedSet, "健康检查失败应触发 MarkAppFailed")
	require.True(t, store.lastFailed.LastErrorStatus.Valid)
	assert.Equal(t, domain.AppStatusStarting, store.lastFailed.LastErrorStatus.String)
	assert.Equal(t, domain.AppStatusError, store.app.Status)
}

// TestAppInitializeIsIdempotentForBindingWaiting 验证应用初始化保持幂等针对绑定 Waiting 的特殊分支或幂等场景。
func TestAppInitializeIsIdempotentForBindingWaiting(t *testing.T) {
	store := newAppInitStub(t)
	store.app.Status = domain.AppStatusBindingWaiting
	store.app.ApiKeyStatus = domain.APIKeyStatusActive
	containers := &fakeContainers{}
	client := &fakeNewAPI{}

	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{})
	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	require.NoError(t, err)
	require.Equal(t, 0, client.calls)
	require.Equal(t, 0, containers.calls)
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
	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "c", Name: "n"}}

	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{Cipher: cipher, ResolveRuntimeImage: testResolveRuntimeImage})
	err = handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	require.NoError(t, err)
	require.Equal(t, 0, client.calls)
	require.True(t, store.statusSet)
}

// TestAppInitializePropagatesNewAPIError 验证应用初始化透传 new-api 错误的错误映射或错误记录场景。
func TestAppInitializePropagatesNewAPIError(t *testing.T) {
	store := newAppInitStub(t)
	client := &fakeNewAPI{err: newapi.ErrUpstream}

	handler := NewAppInitializeHandler(store, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, client, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})
	err := handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	require.ErrorIs(t, err, newapi.ErrUpstream)
	// new-api 调用在 phasePrepare 内 ensureAPIKey 阶段失败:MarkAppFailed 被调用,
	// last_error_status 记为 preparing_runtime, app.status 收敛到 error。
	require.True(t, store.failedSet, "new-api 失败应触发 MarkAppFailed")
	require.True(t, store.lastFailed.LastErrorStatus.Valid)
	assert.Equal(t, domain.AppStatusPreparingRuntime, store.lastFailed.LastErrorStatus.String)
	assert.Equal(t, domain.AppStatusError, store.app.Status)
}

// TestAppInitializePropagatesContainerError 验证应用初始化透传容器错误的错误映射或错误记录场景。
func TestAppInitializePropagatesContainerError(t *testing.T) {
	store := newAppInitStub(t)
	containers := &fakeContainers{err: errors.New("boom")}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})
	// 注入 fakeAppInputUploader, 使 writeAppInput 不报错, 聚焦容器创建失败路径。
	handler.SetAppInputUploader(&fakeAppInputUploader{})
	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	if err == nil || !strings.Contains(err.Error(), "创建容器失败") {
		t.Fatalf("error = %v, want 创建容器失败", err)
	}
	// 容器创建在 phaseCreate 中失败:MarkAppFailed 被调用, last_error_status 记为
	// creating_container, app.status 收敛到 error。
	require.True(t, store.failedSet, "容器创建失败应触发 MarkAppFailed")
	require.True(t, store.lastFailed.LastErrorStatus.Valid)
	assert.Equal(t, domain.AppStatusCreatingContainer, store.lastFailed.LastErrorStatus.String)
	assert.Equal(t, domain.AppStatusError, store.app.Status)
}

// TestAppInitializeRejectsInvalidPayload 验证应用初始化拒绝非法载荷的异常或拒绝路径场景。
func TestAppInitializeRejectsInvalidPayload(t *testing.T) {
	store := newAppInitStub(t)
	handler := NewAppInitializeHandler(store, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, &fakeNewAPI{}, AppInitializeConfig{})

	job := sqlc.Job{Type: domain.JobTypeAppInitialize, PayloadJson: []byte(`{"runtime_node":"node-1"}`)}
	err := handler.Handle(context.Background(), job)
	require.Error(t, err)
}

// TestAppInitializeContainerStepSkippedWhenContainerExists 验证应用初始化容器步骤跳过当容器存在的预期行为场景。
func TestAppInitializeContainerStepSkippedWhenContainerExists(t *testing.T) {
	store := newAppInitStub(t)
	// ContainerID 迁移为 null.String。
	store.app.ContainerID = null.StringFrom("already-there")
	containers := &fakeContainers{}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}

	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})
	// 注入 fakeAppInputUploader, 使 writeAppInput 不报错 (即使容器已存在也需要上传 input 文件)。
	handler.SetAppInputUploader(&fakeAppInputUploader{})
	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	require.NoError(t, err)
	require.Equal(t, 0, containers.calls)
	require.False(t, store.containerSet)
}

// TestEnsureAPIKeyKeepsNewAPITokenModelsUnrestricted 验证 new-api token 创建不限制模型。
func TestEnsureAPIKeyKeepsNewAPITokenModelsUnrestricted(t *testing.T) {
	store := newAppInitStub(t)
	api := &fakeNewAPI{result: newapi.APIKey{ID: 99, Key: "sk-test"}}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, api, AppInitializeConfig{
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
	handler := NewAppInitializeHandler(store, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, api, AppInitializeConfig{
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

// TestHermesHealthCheckerInterfaceUsed 验证 HermesHealthChecker 类型断言的调用与跳过行为。
// 场景: starter 不实现 HermesHealthChecker 时, handle 正常完成但不调用 WaitContainerHealthy。
func TestHermesHealthCheckerInterfaceUsed(t *testing.T) {
	store := newAppInitStub(t)
	// 普通 fakeContainers 不实现 HermesHealthChecker (无 WaitContainerHealthy 方法)。
	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "c", Name: "n"}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "sk"}}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{
		Cipher:              testCipher(t),
		ResolveRuntimeImage: testResolveRuntimeImage,
	})
	// 注入 fakeAppInputUploader, 使 writeAppInput 不报错, 聚焦健康检查接口探测路径。
	handler.SetAppInputUploader(&fakeAppInputUploader{})
	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	require.NoError(t, err)
	// healthCalls 应为 0:普通 starter 没实现 HermesHealthChecker, handler 跳过。
	require.Equal(t, 0, containers.healthCalls)
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
	// getOrganizationErr 让 GetOrganization 返回指定错误, 用于触发 phasePrepare 失败路径。
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

// 以下 3 个 stub 覆盖 AppInitializeStore 中的进度与失败语义:
//   - SetAppProgress / ClearAppProgress:阶段切换 / Receive 触发的进度落库;
//     测试不关心字段值, 仅需让 transitionTo → FlushReset 不报错。
//   - MarkAppFailed:阶段失败时被调用, 通过 failedSet / lastFailed 让用例
//     断言"是否进入失败路径"以及 last_error_status 写入值。
func (s *appInitStub) SetAppProgress(_ context.Context, _ sqlc.SetAppProgressParams) error {
	return nil
}
func (s *appInitStub) ClearAppProgress(_ context.Context, _ string) error {
	return nil
}
func (s *appInitStub) MarkAppFailed(_ context.Context, p sqlc.MarkAppFailedParams) error {
	// 模拟真实 SQL:status 推到 error, last_error_status 记录来源 phase;
	// 同时记录 failedSet / lastFailed, 供失败路径断言使用。
	s.failedSet = true
	s.lastFailed = p
	s.app.Status = domain.AppStatusError
	s.app.LastErrorStatus = p.LastErrorStatus
	return nil
}

// UpdateAppRuntimeImage 更新 app 的镜像引用与 sha256, 模拟 phasePullRuntimeImage 写库。
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

// fakeContainers 同时实现 ContainerCreator 与 ContainerStarter,
// 便于测试断言容器创建与启动的调用次序。
// healthCalls 计数 WaitContainerHealthy 被调用次数, 仅由 healthAwareContainers 递增。
type fakeContainers struct {
	result        runtimepkg.ContainerInfo
	err           error
	calls         int
	lastNode      string
	lastSpec      runtimepkg.ContainerSpec
	startCalls    int
	lastStartNode string
	lastStartID   string
	startErr      error
	// healthCalls 记录 WaitContainerHealthy 调用次数 (由 healthAwareContainers 包装暴露)。
	healthCalls int
	healthErr   error
}

// healthAwareContainers 包装 fakeContainers, 同时实现 ContainerStarter 与 HermesHealthChecker。
type healthAwareContainers struct {
	*fakeContainers
}

// WaitContainerHealthy 实现 HermesHealthChecker, 记录调用并返回预设错误 (nil 表示成功)。
func (h *healthAwareContainers) WaitContainerHealthy(_ context.Context, _, _ string, _ time.Duration) error {
	h.healthCalls++
	return h.healthErr
}

func (f *fakeContainers) CreateContainer(_ context.Context, nodeID string, spec runtimepkg.ContainerSpec) (runtimepkg.ContainerInfo, error) {
	f.calls++
	f.lastNode = nodeID
	f.lastSpec = spec
	if f.err != nil {
		return runtimepkg.ContainerInfo{}, f.err
	}
	return f.result, nil
}

// StartContainer 让 fakeContainers 同时实现 ContainerStarter 接口,
// 便于测试断言 StartContainer 被正确调用。
func (f *fakeContainers) StartContainer(_ context.Context, nodeID, containerID string) error {
	f.startCalls++
	f.lastStartNode = nodeID
	f.lastStartID = containerID
	if f.startErr != nil {
		return f.startErr
	}
	return nil
}

// fakeDirs 实现 AgentDirInitializer, 用来断言 InitAppDirs 被正确调用。
type fakeDirs struct {
	calls    int
	lastNode string
	lastApp  string
	err      error
}

func (f *fakeDirs) InitAppDirs(_ context.Context, nodeID, appID string) error {
	f.calls++
	f.lastNode = nodeID
	f.lastApp = appID
	return f.err
}

// fakeNewAPI 同时充当 NewAPIClientFactory 与 APIKeyClient: UserScopedFor 直接返回自身,
// 让现有用例 (构造一次 fakeNewAPI 给 handler) 继续通过; result 在 CreateAPIKey 与
// GetTokenFullKey 之间共用, 模拟 new-api 创 token + 拉完整 key 这条新链路。
type fakeNewAPI struct {
	result          newapi.APIKey
	err             error // UserScopedFor / CreateAPIKey / SetAPIKeyStatus 公用错误
	createKeyErr    error // 仅让 CreateAPIKey 失败, UserScopedFor 仍成功
	getKeyErr       error // 仅让 GetTokenFullKey 失败, CreateAPIKey 仍成功
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

// GetTokenFullKey 把 result.Key 作为完整 sk- 返回; 测试里通过设置 result.Key 控制注入容器的值。
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

// SetAPIKeyStatus 在 newapi_key_status / app_runtime_ops 测试中被调用;
// 不真做事, 仅通过 calls 计数让上层断言"发生了一次状态切换"。
func (f *fakeNewAPI) SetAPIKeyStatus(_ context.Context, _ int64, _ int) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	return nil
}

// testCipher 返回一个固定 key 的 cipher, 所有 app_initialize 测试共用。
// 32 字节填零 key 仅做单测加解密一致性, 不放入生产环境。
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

// fakeAppInputUploader 实现 AppInputUploader, 记录每次 UploadAppInputFile 调用。
// 用于断言 writeAppInput 通过 agent 上传文件 (而非写入 manager 本机)。
type fakeAppInputUploader struct {
	// mu 保护 calls 切片;handler 单 goroutine 跑, 但桩做并发安全更稳妥, 避免未来
	// handler 内部 goroutine 化时引入竞态。
	mu sync.Mutex
	// calls 按调用顺序记录每次上传的参数 (nodeID / appID / relPath)。
	calls []fakeAppInputUploadCall
	// err 非 nil 时所有调用返回该错误 (模拟 agent 上传失败场景)。
	err error
}

// fakeAppInputUploadCall 记录单次 UploadAppInputFile 调用的参数。
type fakeAppInputUploadCall struct {
	nodeID  string
	appID   string
	relPath string
}

func (f *fakeAppInputUploader) UploadAppInputFile(_ context.Context, nodeID, appID, relPath string, content io.Reader) error {
	// 消耗 content, 避免调用方 strings.NewReader 被留在半读状态。
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

// relPathsForApp 返回给定 appID 下所有上传的 relPath, 顺序与调用顺序一致;
// 用于断言"manifest.yaml 必须最后写"这类顺序契约。
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

// TestAppInitialize_DoesNotUploadKnowledgeFiles 验证 app_initialize 只上传 manifest/resources，
// 知识库由 oc-kb 通过 manager runtime API 访问，不再复制本地主副本文件。
func TestAppInitialize_DoesNotUploadKnowledgeFiles(t *testing.T) {
	store := newAppInitStub(t)
	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "n"}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	up := &fakeAppInputUploader{}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})
	handler.SetAppInputUploader(up)

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")))

	// 不应出现任何 resources/knowledge/* 上传记录。
	for _, c := range up.calls {
		require.False(t, strings.HasPrefix(c.relPath, "resources/knowledge/"),
			"app_initialize 不应上传本地知识库文件: %s", c.relPath)
	}
}

// fakeAuditRecorder 实现 audit.AuditRecorder, 用于断言审计事件被写入。
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
	// UserScopedFor 成功, CreateAPIKey 失败
	client := &fakeNewAPI{createKeyErr: newapi.ErrUpstream}

	cfg := AppInitializeConfig{
		Cipher:              testCipher(t),
		AuditHelper:         helper,
		ResolveRuntimeImage: testResolveRuntimeImage,
	}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, client, cfg)

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
	// CreateAPIKey 成功, GetTokenFullKey 失败
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
	handler := NewAppInitializeHandler(store, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, client, cfg)

	err := handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	if err == nil || !strings.Contains(err.Error(), "取完整 sk-") {
		t.Fatalf("err = %v", err)
	}
	require.Equal(t, 1, len(rec.events))
	require.Equal(t, "newapi_call", rec.events[0].TargetType)
	// Endpoint 应含 token ID
	require.True(t, strings.Contains(rec.events[0].TargetID, "42"))
}

// errNodeDockerProvider 实现 NodeDockerProvider, DockerClientForNode 总是返回预设错误。
// 用于测试 phasePullRuntimeImage 在获取 Docker 客户端失败时的错误路径。
type errNodeDockerProvider struct {
	err error
}

func (p *errNodeDockerProvider) DockerClientForNode(_ context.Context, _ string) (*dockerclient.Client, error) {
	return nil, p.err
}

// TestAppInitializeHandler_Phases_Progress 验证 5 阶段每阶段都把 status 推进一格,
// 共 5 次 SetAppStatus (4 个 init 子状态 + binding_waiting)。
//
// imagePullCoord / nodeDockerProv 均未注入, phasePullRuntimeImage 直接跳过;
// 其它依赖用既有 fake stub 让 happy path 跑完。
func TestAppInitializeHandler_Phases_Progress(t *testing.T) {
	store := newAppInitStub(t)
	// 起始 status=draft, 模拟新建 app
	store.app.Status = domain.AppStatusDraft

	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "sk-test"}}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{
		Cipher:              testCipher(t),
		ResolveRuntimeImage: testResolveRuntimeImage,
	})
	handler.SetAppInputUploader(&fakeAppInputUploader{})

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")))

	// 期望按顺序触发:pulling_runtime_image → preparing_runtime →
	// creating_container → starting → binding_waiting, 共 5 次状态切换。
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

// TestAppInitializeHandler_Phases_FailureWritesLastError 表驱动覆盖各阶段失败时
// MarkAppFailed 把 last_error_status 写为该阶段名;同时验证 app.status 收敛到 error。
//
// 每条 case 通过不同 stub 让对应阶段失败:
//   - phasePullRuntimeImage 用 errNodeDockerProvider 模拟获取 Docker 客户端失败
//   - phasePrepare 用 store.getOrganizationErr 模拟 GetOrganization 失败
//   - phaseCreate 用 fakeContainers.err 模拟 CreateContainer 失败
//   - phaseStart 用 fakeContainers.startErr 模拟 StartContainer 失败
func TestAppInitializeHandler_Phases_FailureWritesLastError(t *testing.T) {
	cases := []struct {
		// name 说明该 case 触发哪一阶段失败
		name string
		// expect 是该阶段名, 期望写入 MarkAppFailed.LastErrorStatus
		expect string
		// build 根据 case 类型构造特定失败行为的 handler 与 store
		build func(t *testing.T) (*AppInitializeHandler, *appInitStub)
	}{
		{
			// phasePullRuntimeImage 失败:nodeDockerProv.DockerClientForNode 返回 error
			name:   "phasePullRuntimeImage 失败写入 pulling_runtime_image",
			expect: domain.AppStatusPullingRuntimeImage,
			build: func(t *testing.T) (*AppInitializeHandler, *appInitStub) {
				s := newAppInitStub(t)
				s.app.Status = domain.AppStatusDraft
				h := NewAppInitializeHandler(s, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})
				h.SetAppInputUploader(&fakeAppInputUploader{})
				// 注入非 nil coordinator (不会被调用, 因 nodeDockerProv 更早失败)。
				h.SetImagePullCoord(imagecoord.NewCoordinator(nil, nil, "test"))
				// DockerClientForNode 返回错误触发 phasePullRuntimeImage 失败。
				h.SetNodeDockerProvider(&errNodeDockerProvider{err: errors.New("docker client failed")})
				return h, s
			},
		},
		{
			// phasePrepare 失败:GetOrganization 返回 error
			name:   "phasePrepare 失败写入 preparing_runtime",
			expect: domain.AppStatusPreparingRuntime,
			build: func(t *testing.T) (*AppInitializeHandler, *appInitStub) {
				s := newAppInitStub(t)
				s.app.Status = domain.AppStatusDraft
				s.getOrganizationErr = errors.New("org lookup failed")
				h := NewAppInitializeHandler(s, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})
				h.SetAppInputUploader(&fakeAppInputUploader{})
				return h, s
			},
		},
		{
			// phaseCreate 失败:CreateContainer 返回 error
			name:   "phaseCreate 失败写入 creating_container",
			expect: domain.AppStatusCreatingContainer,
			build: func(t *testing.T) (*AppInitializeHandler, *appInitStub) {
				s := newAppInitStub(t)
				s.app.Status = domain.AppStatusDraft
				containers := &fakeContainers{err: errors.New("create failed")}
				h := NewAppInitializeHandler(s, &fakeDirs{}, containers, containers, &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})
				h.SetAppInputUploader(&fakeAppInputUploader{})
				return h, s
			},
		},
		{
			// phaseStart 失败:StartContainer 返回 error
			name:   "phaseStart 失败写入 starting",
			expect: domain.AppStatusStarting,
			build: func(t *testing.T) (*AppInitializeHandler, *appInitStub) {
				s := newAppInitStub(t)
				s.app.Status = domain.AppStatusDraft
				containers := &fakeContainers{
					result:   runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID},
					startErr: errors.New("start failed"),
				}
				h := NewAppInitializeHandler(s, &fakeDirs{}, containers, containers, &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})
				h.SetAppInputUploader(&fakeAppInputUploader{})
				return h, s
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, s := c.build(t)
			err := h.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
			require.Error(t, err, "失败 stub 应返回 error")
			// MarkAppFailed 必须被调用, LastErrorStatus 应等于 case 期望阶段。
			require.True(t, s.failedSet, "MarkAppFailed 应被调用")
			require.True(t, s.lastFailed.LastErrorStatus.Valid)
			assert.Equal(t, c.expect, s.lastFailed.LastErrorStatus.String)
			// 终态应收敛到 error。
			assert.Equal(t, domain.AppStatusError, s.app.Status)
		})
	}
}

// TestAppInitializeHandler_IdempotentReentry 模拟 manager 在前次 init 跑到容器创建之后
// 才崩溃 / 重启;reaper 把 status 重置回 pulling_runtime_image 重跑, 但 container_id
// 已写入数据库。此时 Handle 重入应:
//   - 把状态从 draft 逐阶段推到 binding_waiting, 共 5 次 SetAppStatus;
//   - phaseCreate 看到 container_id 已存在, 跳过 CreateContainer 不重复创建容器。
func TestAppInitializeHandler_IdempotentReentry(t *testing.T) {
	store := newAppInitStub(t)
	// app 起始 draft + container_id 已存在, 模拟 reaper 重置 status 但保留 container_id。
	store.app.Status = domain.AppStatusDraft
	// ContainerID 迁移为 null.String。
	store.app.ContainerID = null.StringFrom("cid-1")

	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{
		Cipher:              testCipher(t),
		ResolveRuntimeImage: testResolveRuntimeImage,
	})
	handler.SetAppInputUploader(&fakeAppInputUploader{})

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")))

	// container_id 已存在, phaseCreate 必须跳过 CreateContainer。
	assert.Equal(t, 0, containers.calls, "container_id 已存在不应再创建")
	// 终态应推进到 binding_waiting。
	assert.Equal(t, domain.AppStatusBindingWaiting, store.app.Status)
	// 不应触发失败。
	assert.False(t, store.failedSet)
}

// --- Phase 4 新增测试 ---

// TestAppInitialize_NullVersionIDFails 验证实例未绑定助手版本时
// Handle 直接标记失败，不进入任何阶段推进。
// 覆盖场景：app.VersionID.Valid == false → markFailed 被调用，错误含"未绑定助手版本"。
func TestAppInitialize_NullVersionIDFails(t *testing.T) {
	store := newAppInitStub(t)
	// 清空 VersionID，模拟未绑定版本的实例。VersionID 迁移为 null.String；零值表示 NULL。
	store.app.VersionID = null.String{}

	handler := NewAppInitializeHandler(store, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, &fakeNewAPI{}, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})
	handler.SetAppInputUploader(&fakeAppInputUploader{})

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
	// map 迁移为 map[string]sqlc.AssistantVersion。
	store.versions = map[string]sqlc.AssistantVersion{}

	handler := NewAppInitializeHandler(store, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, &fakeNewAPI{}, AppInitializeConfig{Cipher: testCipher(t), ResolveRuntimeImage: testResolveRuntimeImage})
	handler.SetAppInputUploader(&fakeAppInputUploader{})

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
	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "sk-test"}}

	// 注入 ResolveRuntimeImage：版本 image_id "hermes-v1" → "hermes:v2026-test"。
	resolvedRef := "hermes:v2026-test"
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{
		Cipher: testCipher(t),
		ResolveRuntimeImage: func(imageID string) (string, bool) {
			if imageID == "hermes-v1" {
				return resolvedRef, true
			}
			return "", false
		},
	})
	handler.SetAppInputUploader(&fakeAppInputUploader{})

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")))

	// 初始化成功后 SetAppAppliedVersion 必须被调用。
	require.True(t, store.appliedVersionSet, "初始化成功应调用 SetAppAppliedVersion")
	// revision 应与版本 stub 中的 Revision(=1) 一致。
	assert.Equal(t, int32(1), store.lastAppliedVersion.AppliedVersionRevision, "applied_version_revision 应等于版本 Revision")
	// applied_image_ref 应等于 ResolveRuntimeImage 解析出的 ref。
	assert.Equal(t, resolvedRef, store.lastAppliedVersion.AppliedImageRef, "applied_image_ref 应等于解析出的镜像 ref")
}

// TestAppInitialize_PromotesToRunningWhenChannelAlreadyBound 验证切换助手版本+重启
// 触发镜像重建后的「已绑定渠道」自愈：app_initialize 完整跑完 5 阶段进入
// binding_waiting 后，若 AppHasBoundChannelBinding 返回 true（凭证仍在 bind mount
// 目录里、容器重启后即可复用），则 handler 应继续把 status 推到 running，避免
// 概览页与渠道页状态不一致（渠道页 bound、概览页待绑定）。
func TestAppInitialize_PromotesToRunningWhenChannelAlreadyBound(t *testing.T) {
	store := newAppInitStub(t)
	store.app.Status = domain.AppStatusDraft
	// 关键前置：渠道已 bound——模拟镜像重建前的历史绑定行没有被重置。
	store.channelBound = true

	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "sk-test"}}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{
		Cipher:              testCipher(t),
		ResolveRuntimeImage: testResolveRuntimeImage,
	})
	handler.SetAppInputUploader(&fakeAppInputUploader{})

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")))

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
// 的默认场景下，init 走完仍应停在 binding_waiting，保持「等待用户扫码绑定」的原行为
// （只在自愈条件命中时才提前推到 running）。
func TestAppInitialize_StaysBindingWaitingWhenNoChannelBound(t *testing.T) {
	store := newAppInitStub(t)
	store.app.Status = domain.AppStatusDraft
	// 渠道未 bound：保持原行为，等渠道扫码完成由 finalizeChannelBound 推到 running。
	store.channelBound = false

	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "sk-test"}}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{
		Cipher:              testCipher(t),
		ResolveRuntimeImage: testResolveRuntimeImage,
	})
	handler.SetAppInputUploader(&fakeAppInputUploader{})

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")))

	// 终态应为 binding_waiting：不应被错误地推到 running。
	assert.Equal(t, domain.AppStatusBindingWaiting, store.app.Status)
	// 最后一次 SetAppStatus 应是 binding_waiting，证明没有额外续推。
	require.Greater(t, len(store.statusCalls), 0)
	assert.Equal(t, domain.AppStatusBindingWaiting, store.statusCalls[len(store.statusCalls)-1].Status)
	// 自愈探测仍应被调用一次（即便 channelBound=false，handler 也要查一次）。
	assert.Equal(t, 1, store.hasBoundCalls, "init 完成后应至少查一次渠道绑定快照")
}

// TestAppInitialize_IdempotentBindingWaitingPromotesWhenChannelBound 验证 Handle 入口
// 的 binding_waiting 幂等分支也带「自愈」能力：当 app 已经卡在 binding_waiting 但
// 渠道实际已 bound 时（典型场景：上一次 init 跑完瞬间渠道刚 bound，但 worker 错过了
// 续推；或重启时进入了 binding_waiting 而渠道行未被重置），worker 下一次重入 init
// job 时应当能把状态收敛到 running，不再要求用户重新扫码。
func TestAppInitialize_IdempotentBindingWaitingPromotesWhenChannelBound(t *testing.T) {
	store := newAppInitStub(t)
	store.app.Status = domain.AppStatusBindingWaiting
	store.app.ApiKeyStatus = domain.APIKeyStatusActive
	store.channelBound = true
	containers := &fakeContainers{}
	client := &fakeNewAPI{}

	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{})
	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")))

	// 幂等分支不会走容器创建 / new-api 调用，但应触发一次自愈推进。
	assert.Equal(t, 0, containers.calls)
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
	// ID 迁移为 string。
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
// 覆盖场景：version.Routing 与 version.SystemPrompt 直接映射到输出字段。
func TestAppInitialize_VersionRoutingAndPersonaPassedThrough(t *testing.T) {
	// 仅测试 BuildAppInputData 纯函数。ID 迁移为 string。
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
// 用于验证 writeSkillsIntoInput 能正确读取并上传 skill tar。
type fakeSkillBlobReader struct {
	// blobs 以 relPath 为 key，存储伪 tar 内容。
	blobs map[string]string
	// errOnPath 若非空，当 relPath 等于此值时返回错误，测试错误路径。
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

// TestAppInitialize_SkillsUploadedToInput 验证版本 skills_json 中的 skill tar
// 被正确读取并推送到 input/resources/skills/<name>.tar。
// 覆盖场景：两个 skill → 上传两份文件 + manifest SkillRelPaths 包含对应路径。
func TestAppInitialize_SkillsUploadedToInput(t *testing.T) {
	store := newAppInitStub(t)
	// 更新版本 stub：添加两个 skill。版本 map 迁移为 map[string]sqlc.AssistantVersion。
	store.versions[testVersionID] = sqlc.AssistantVersion{
		ID:           testVersionID,
		Name:         "v1-with-skills",
		MainModel:    "gpt-4o",
		SystemPrompt: "你是助手",
		ImageID:      "hermes-v1",
		Revision:     2,
		RoutingJson:  []byte(`{}`),
		// skills_json 含两个 skill，FilePath 指向 manager 数据根的相对路径。
		SkillsJson: []byte(`[
			{"name":"search","file_path":"skills/search.tar","file_size":1024,"file_sha256":"abc"},
			{"name":"calc","file_path":"skills/calc.tar","file_size":512,"file_sha256":"def"}
		]`),
	}

	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "sk-test"}}
	up := &fakeAppInputUploader{}
	blobs := &fakeSkillBlobReader{blobs: map[string]string{
		"skills/search.tar": "fake-search-tar-content",
		"skills/calc.tar":   "fake-calc-tar-content",
	}}

	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{
		Cipher:              testCipher(t),
		SkillBlobs:          blobs,
		ResolveRuntimeImage: testResolveRuntimeImage,
	})
	handler.SetAppInputUploader(up)

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")))

	// 两个 skill tar 必须被上传到 resources/skills/。
	require.True(t, up.hasUpload(testAppID, "resources/skills/search.tar"),
		"search skill 应被上传到 resources/skills/search.tar")
	require.True(t, up.hasUpload(testAppID, "resources/skills/calc.tar"),
		"calc skill 应被上传到 resources/skills/calc.tar")
}

// TestAppInitialize_SkillBlobsNilSkipsSkills 验证 SkillBlobs 未注入时
// writeSkillsIntoInput 跳过推送，Handle 正常完成（向后兼容无 skill 的装配）。
func TestAppInitialize_SkillBlobsNilSkipsSkills(t *testing.T) {
	store := newAppInitStub(t)
	// 版本含 skill，但 SkillBlobs 未注入，应安全跳过。版本 map 迁移为 map[string]sqlc.AssistantVersion。
	store.versions[testVersionID] = sqlc.AssistantVersion{
		ID:          testVersionID,
		MainModel:   "gpt-4o",
		ImageID:     "hermes-v1",
		Revision:    1,
		RoutingJson: []byte(`{}`),
		SkillsJson:  []byte(`[{"name":"search","file_path":"skills/search.tar","file_size":1024,"file_sha256":"abc"}]`),
	}

	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "sk"}}
	up := &fakeAppInputUploader{}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{
		Cipher: testCipher(t),
		// SkillBlobs 未注入（nil），应安全跳过。
		ResolveRuntimeImage: testResolveRuntimeImage,
	})
	handler.SetAppInputUploader(up)

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")))

	// 不应出现任何 resources/skills/* 上传记录。
	for _, c := range up.calls {
		require.False(t, strings.HasPrefix(c.relPath, "resources/skills/"),
			"SkillBlobs 未注入时不应上传任何 skill: %s", c.relPath)
	}
}
