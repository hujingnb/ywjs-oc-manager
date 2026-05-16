package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dockerclient "github.com/docker/docker/client"

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
)

// TestAppInitializeHandlesHappyPath 验证应用初始化处理成功路径的成功路径场景。
func TestAppInitializeHandlesHappyPath(t *testing.T) {
	store := newAppInitStub(t)
	dirs := &fakeDirs{}
	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID, Status: "created"}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 99, Key: "sk-test"}}
	rw := &fakeRuntimeFileWriter{}

	cipher, err := auth.NewCipher(make([]byte, 32))
	require.NoError(t, err)
	cfg := AppInitializeConfig{
		RuntimeImage:         "hermes:dev",
		SystemPromptTemplate: "你是 {org_name} 的助手",
		Cipher:               cipher,
	}
	handler := NewAppInitializeHandler(store, dirs, containers, containers, client, cfg)
	// 注入 fakeRuntimeFileWriter，验证 Hermes 配置文件通过 UploadAppRuntimeFile 上传。
	handler.SetRuntimeFileWriter(rw)

	err = handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	require.NoError(t, err)
	if !store.apiKeySet || !store.statusSet || !store.containerSet {
		t.Fatalf("api_key/status/container 应当都被持久化: %+v", store)
	}

	// container_id 写库为 docker mock 返回的 ID。
	require.Equal(t, "ctr-1", store.app.ContainerID.String)

	// ciphertext 必须可被同一 cipher 解回 sk-test，证明真的走了加密路径。
	plain, err := cipher.Decrypt(store.app.NewapiKeyCiphertext.String)
	require.NoError(t, err)
	require.Equal(t, "sk-test", string(plain))
	require.NotEqual(t, "sk-test", store.app.NewapiKeyCiphertext.String)

	// Hermes 容器规格断言：1 个挂载（.hermes bind mount 到 /opt/data）。
	// Hermes 时代不再需要 5 个独立目录挂载（workspace/state/logs/knowledge）。
	require.Equal(t, 1, len(containers.lastSpec.Volumes))
	require.Equal(t, "/opt/data", containers.lastSpec.Volumes[0].ContainerPath)

	// 容器名应以 hermes- 为前缀，替换旧的 ocm- 前缀。
	require.Equal(t, "hermes-"+testAppID, containers.lastSpec.Name)
	require.Equal(t, "hermes:dev", containers.lastSpec.Image)

	// Sprint 1：InitAppDirs 与 StartContainer 必须被调对参数
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

	// 验证 Hermes 配置文件已通过 UploadAppRuntimeFile 上传到目标节点，而非写入 manager 本机。
	// 三个必须存在的文件：config.yaml / .env（SOUL.md 在 prompt 非空时上传）。
	require.True(t, rw.hasUpload(testAppID, "config.yaml"), "config.yaml 应被上传")
	require.True(t, rw.hasUpload(testAppID, ".env"), ".env 应被上传")
	// 所有上传调用使用相同的 nodeID。
	for _, c := range rw.calls {
		require.Equal(t, "node-1", c.nodeID, "上传节点应为 node-1")
	}
}

// TestWriteHermesFiles_FailsWhenWriterNil 验证 writeHermesFiles 在 AppRuntimeFileWriter 未注入时
// 直接报错，而非静默跳过——确保多节点部署下不会因缺少配置文件导致容器启动后行为异常。
func TestWriteHermesFiles_FailsWhenWriterNil(t *testing.T) {
	store := newAppInitStub(t)
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	// 不注入 SetRuntimeFileWriter，runtimeFiles 保持 nil。
	handler := NewAppInitializeHandler(store, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, client, AppInitializeConfig{Cipher: testCipher(t)})

	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	// nodeID 非空时 writeHermesFiles 必须被调用；nil writer 应立即报错。
	require.Error(t, err)
	require.Contains(t, err.Error(), "AppRuntimeFileWriter 未注入")
}

// TestWriteHermesFiles_PropagatesUploadError 验证 UploadAppRuntimeFile 返回错误时
// writeHermesFiles 正确透传错误，handler 不继续创建容器。
func TestWriteHermesFiles_PropagatesUploadError(t *testing.T) {
	store := newAppInitStub(t)
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	// 模拟 agent 上传失败（网络不通 / 节点不可达）。
	rw := &fakeRuntimeFileWriter{err: errors.New("agent upload failed")}
	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "c", Name: "n"}}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{Cipher: testCipher(t)})
	handler.SetRuntimeFileWriter(rw)

	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	// 上传失败应冒泡，容器不应被创建。
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent upload failed")
	require.Equal(t, 0, containers.calls, "上传失败后不应创建容器")
}

// TestAppInitializeWaitsForHermesHealthyWhenSupported 验证应用初始化等待 Hermes 容器
// docker HEALTHCHECK 报 healthy 当 starter 实现 HermesHealthChecker 接口时的预期行为。
func TestAppInitializeWaitsForHermesHealthyWhenSupported(t *testing.T) {
	// Sprint 2（Hermes 版）：starter 同时实现 HermesHealthChecker 时
	// handler 应等 docker HEALTHCHECK 报 healthy 再推 binding_waiting。
	store := newAppInitStub(t)
	dirs := &fakeDirs{}
	base := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID, Status: "created"}}
	// healthAwareContainers 包装 fakeContainers，额外暴露 WaitContainerHealthy 方法。
	containers := &healthAwareContainers{fakeContainers: base}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 99, Key: "sk-test"}}

	cipher, err := auth.NewCipher(make([]byte, 32))
	require.NoError(t, err)
	handler := NewAppInitializeHandler(store, dirs, base, containers, client, AppInitializeConfig{
		RuntimeImage: "hermes:dev",
		Cipher:       cipher,
	})
	// 注入 fakeRuntimeFileWriter，确保 writeHermesFiles 可正常执行。
	handler.SetRuntimeFileWriter(&fakeRuntimeFileWriter{})
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
	handler := NewAppInitializeHandler(store, &fakeDirs{}, base, containers, client, AppInitializeConfig{Cipher: testCipher(t)})
	// 注入 fakeRuntimeFileWriter，使 writeHermesFiles 不报错，聚焦健康检查失败路径。
	handler.SetRuntimeFileWriter(&fakeRuntimeFileWriter{})

	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	// 错误信息应包含"等待 Hermes 容器健康失败"。
	if err == nil || !strings.Contains(err.Error(), "等待 Hermes 容器健康失败") {
		t.Fatalf("err=%v", err)
	}
	// 健康检查在 phaseStart 中失败:MarkAppFailed 被调用,last_error_status 记为 starting,
	// app.status 收敛到 error。
	require.True(t, store.failedSet, "健康检查失败应触发 MarkAppFailed")
	require.True(t, store.lastFailed.LastErrorStatus.Valid)
	assert.Equal(t, domain.AppStatusStarting, store.lastFailed.LastErrorStatus.String)
	assert.Equal(t, domain.AppStatusError, store.app.Status)
}

// TestAppInitializeIsIdempotentForBindingWaiting 验证应用初始化保持幂等针对绑定Waiting的特殊分支或幂等场景。
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

// TestAppInitializeSkipsAPIKeyWhenAlreadyActive 验证应用初始化跳过APIKey当已经启用的特殊分支或幂等场景。
func TestAppInitializeSkipsAPIKeyWhenAlreadyActive(t *testing.T) {
	store := newAppInitStub(t)
	cipher := testCipher(t)
	encrypted, err := cipher.Encrypt([]byte("sk-old-cached"))
	require.NoError(t, err)
	store.app.ApiKeyStatus = domain.APIKeyStatusActive
	store.app.NewapiKeyCiphertext = pgtype.Text{String: encrypted, Valid: true}
	client := &fakeNewAPI{}
	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "c", Name: "n"}}

	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{Cipher: cipher})
	err = handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	require.NoError(t, err)
	require.Equal(t, 0, client.calls)
	require.True(t, store.statusSet)
}

// TestAppInitializePropagatesNewAPIError 验证应用初始化透传new-api错误的错误映射或错误记录场景。
func TestAppInitializePropagatesNewAPIError(t *testing.T) {
	store := newAppInitStub(t)
	client := &fakeNewAPI{err: newapi.ErrUpstream}

	handler := NewAppInitializeHandler(store, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, client, AppInitializeConfig{Cipher: testCipher(t)})
	err := handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	require.ErrorIs(t, err, newapi.ErrUpstream)
	// new-api 调用在 phasePrepare 内 ensureAPIKey 阶段失败:MarkAppFailed 被调用,
	// last_error_status 记为 preparing_runtime,app.status 收敛到 error。
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
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{Cipher: testCipher(t)})
	// 注入 fakeRuntimeFileWriter，使 writeHermesFiles 不报错，聚焦容器创建失败路径。
	handler.SetRuntimeFileWriter(&fakeRuntimeFileWriter{})
	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	if err == nil || !strings.Contains(err.Error(), "创建容器失败") {
		t.Fatalf("error = %v, want 创建容器失败", err)
	}
	// 容器创建在 phaseCreate 中失败:MarkAppFailed 被调用,last_error_status 记为
	// creating_container,app.status 收敛到 error。
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
	store.app.ContainerID = pgtype.Text{String: "already-there", Valid: true}
	containers := &fakeContainers{}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}

	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{Cipher: testCipher(t)})
	// 注入 fakeRuntimeFileWriter，使 writeHermesFiles 不报错（即使容器已存在也需要上传配置文件）。
	handler.SetRuntimeFileWriter(&fakeRuntimeFileWriter{})
	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	require.NoError(t, err)
	require.Equal(t, 0, containers.calls)
	require.False(t, store.containerSet)
}

// TestEnsureAPIKeyKeepsNewAPITokenModelsUnrestricted 验证 new-api token 创建仍不限制模型。
func TestEnsureAPIKeyKeepsNewAPITokenModelsUnrestricted(t *testing.T) {
	store := newAppInitStub(t)
	store.app.ModelID = "deepseek-r1:14b"
	api := &fakeNewAPI{result: newapi.APIKey{ID: 99, Key: "sk-test"}}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, api, AppInitializeConfig{
		Cipher: testCipher(t),
	})

	_, err := handler.ensureAPIKey(context.Background(), &store.app)

	require.NoError(t, err)
	assert.Empty(t, api.lastCreateInput.Models)
}

// TestHermesHealthCheckerInterfaceUsed 验证 HermesHealthChecker 类型断言的调用与跳过行为。
// 场景：starter 不实现 HermesHealthChecker 时，handle 正常完成但不调用 WaitContainerHealthy。
func TestHermesHealthCheckerInterfaceUsed(t *testing.T) {
	store := newAppInitStub(t)
	// 普通 fakeContainers 不实现 HermesHealthChecker（无 WaitContainerHealthy 方法）。
	// handler 的类型断言应为 false，跳过健康等待。
	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "c", Name: "n"}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "sk"}}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{
		Cipher: testCipher(t),
	})
	// 注入 fakeRuntimeFileWriter，使 writeHermesFiles 不报错，聚焦健康检查接口探测路径。
	handler.SetRuntimeFileWriter(&fakeRuntimeFileWriter{})
	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	require.NoError(t, err)
	// healthCalls 应为 0：普通 starter 没实现 HermesHealthChecker，handler 跳过。
	require.Equal(t, 0, containers.healthCalls)
}

func buildJob(t *testing.T, appID, nodeID string) sqlc.Job {
	t.Helper()
	payload := []byte(`{"app_id":"` + appID + `","runtime_node":"` + nodeID + `"}`)
	return sqlc.Job{Type: domain.JobTypeAppInitialize, PayloadJson: payload}
}

type appInitStub struct {
	t            *testing.T
	app          sqlc.App
	org          sqlc.Organization
	user         sqlc.User
	node         sqlc.RuntimeNode
	apiKeySet    bool
	statusSet    bool
	containerSet bool
	auditLogs    []sqlc.CreateAuditLogParams
	// statusCalls 按顺序记录每次 SetAppStatus 调用参数,用于断言 5 阶段推进序列
	// (draft → pulling_image → ... → binding_waiting)。
	statusCalls []sqlc.SetAppStatusParams
	// failedSet 标记 MarkAppFailed 是否被调用,用于失败路径精确断言。
	failedSet bool
	// lastFailed 记录最近一次 MarkAppFailed 参数,用于断言 last_error_status 写入值。
	lastFailed sqlc.MarkAppFailedParams
	// getOrganizationErr 让 GetOrganization 返回指定错误,用于触发 phasePrepare 失败路径。
	getOrganizationErr error
}

func newAppInitStub(t *testing.T) *appInitStub {
	return &appInitStub{
		t: t,
		app: sqlc.App{
			ID:           mustUUIDForTest(t, testAppID),
			OrgID:        mustUUIDForTest(t, testOrgID),
			OwnerUserID:  mustUUIDForTest(t, testUsrID),
			Name:         "alice-bot",
			Status:       domain.AppStatusDraft,
			PersonaMode:  domain.PersonaModeOrgInherited,
			ApiKeyStatus: domain.APIKeyStatusPending,
			AppPrompt:    pgtype.Text{String: "{org_name} 应用 {app_name}", Valid: true},
		},
		org:  sqlc.Organization{Name: "测试组织", Status: domain.StatusActive},
		user: sqlc.User{DisplayName: "Alice"},
		node: sqlc.RuntimeNode{NodeDataRoot: pgtype.Text{String: "/var/lib/oc-agent", Valid: true}},
	}
}

func (s *appInitStub) GetApp(_ context.Context, _ pgtype.UUID) (sqlc.App, error) { return s.app, nil }
func (s *appInitStub) GetOrganization(_ context.Context, _ pgtype.UUID) (sqlc.Organization, error) {
	if s.getOrganizationErr != nil {
		return sqlc.Organization{}, s.getOrganizationErr
	}
	return s.org, nil
}
func (s *appInitStub) GetUser(_ context.Context, _ pgtype.UUID) (sqlc.User, error) {
	return s.user, nil
}

func (s *appInitStub) GetRuntimeNode(_ context.Context, _ pgtype.UUID) (sqlc.RuntimeNode, error) {
	return s.node, nil
}

func (s *appInitStub) SetAppNewAPIKey(_ context.Context, arg sqlc.SetAppNewAPIKeyParams) (sqlc.App, error) {
	s.apiKeySet = true
	s.app.ApiKeyStatus = arg.ApiKeyStatus
	s.app.NewapiKeyID = arg.NewapiKeyID
	s.app.NewapiKeyCiphertext = arg.NewapiKeyCiphertext
	return s.app, nil
}

func (s *appInitStub) SetAppContainer(_ context.Context, arg sqlc.SetAppContainerParams) (sqlc.App, error) {
	s.containerSet = true
	s.app.ContainerID = arg.ContainerID
	s.app.ContainerName = arg.ContainerName
	return s.app, nil
}

func (s *appInitStub) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error) {
	s.statusSet = true
	// 按调用顺序记录每次状态切换,便于断言 5 阶段推进序列。
	s.statusCalls = append(s.statusCalls, arg)
	s.app.Status = arg.Status
	return s.app, nil
}

func (s *appInitStub) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error) {
	s.auditLogs = append(s.auditLogs, arg)
	return sqlc.AuditLog{TargetType: arg.TargetType, TargetID: arg.TargetID, Action: arg.Action, Result: arg.Result}, nil
}

// 以下 3 个 stub 覆盖 AppInitializeStore 中的进度与失败语义:
//   - SetAppProgress / ClearAppProgress:阶段切换 / Receive 触发的进度落库;
//     测试不关心字段值,仅需让 transitionTo → FlushReset 不报错。
//   - MarkAppFailed:阶段失败时被调用,通过 failedSet / lastFailed 让用例
//     断言"是否进入失败路径"以及 last_error_status 写入值。
func (s *appInitStub) SetAppProgress(_ context.Context, _ sqlc.SetAppProgressParams) (sqlc.App, error) {
	return sqlc.App{}, nil
}
func (s *appInitStub) ClearAppProgress(_ context.Context, _ pgtype.UUID) (sqlc.App, error) {
	return sqlc.App{}, nil
}
func (s *appInitStub) MarkAppFailed(_ context.Context, p sqlc.MarkAppFailedParams) (sqlc.App, error) {
	// 模拟真实 SQL:status 推到 error,last_error_status 记录来源 phase;
	// 同时记录 failedSet / lastFailed,供失败路径断言使用。
	s.failedSet = true
	s.lastFailed = p
	s.app.Status = domain.AppStatusError
	s.app.LastErrorStatus = p.LastErrorStatus
	return s.app, nil
}

// UpdateAppRuntimeImage 更新 app 的镜像引用与 sha256，模拟 phasePullRuntimeImage 写库。
func (s *appInitStub) UpdateAppRuntimeImage(_ context.Context, arg sqlc.UpdateAppRuntimeImageParams) (sqlc.App, error) {
	s.app.RuntimeImageRef = arg.RuntimeImageRef
	s.app.RuntimeImageSha256 = arg.RuntimeImageSha256
	return s.app, nil
}

// fakeContainers 同时实现 ContainerCreator 与 ContainerStarter，
// 便于测试断言容器创建与启动的调用次序。
// healthCalls 计数 WaitContainerHealthy 被调用次数，仅由 healthAwareContainers 递增。
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
	// healthCalls 记录 WaitContainerHealthy 调用次数（由 healthAwareContainers 包装暴露）。
	healthCalls int
	healthErr   error
}

// healthAwareContainers 包装 fakeContainers，同时实现 ContainerStarter 与 HermesHealthChecker。
// 用于测试 handler 对 HermesHealthChecker 类型断言的探测与调用路径。
type healthAwareContainers struct {
	*fakeContainers
}

// WaitContainerHealthy 实现 HermesHealthChecker，记录调用并返回预设错误（nil 表示成功）。
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

// StartContainer 让 fakeContainers 同时实现 ContainerStarter 接口，
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

// fakeDirs 实现 AgentDirInitializer，用来断言 InitAppDirs 被正确调用。
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
// getKeyErr 不为 nil 时优先返回该错误（用于独立测试 GetTokenFullKey 失败路径）。
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

func mustUUIDForTest(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	err := id.Scan(value)
	require.NoError(t, err)
	return id
}

// fakeRuntimeFileWriter 实现 AppRuntimeFileWriter，记录每次 UploadAppRuntimeFile 调用。
// 用于断言 writeHermesFiles 通过 agent 上传文件而非写入 manager 本机文件系统。
type fakeRuntimeFileWriter struct {
	// calls 按调用顺序记录每次上传的参数（nodeID / appID / relPath / 内容字节数）。
	calls []fakeRuntimeUploadCall
	// err 非 nil 时所有调用返回该错误（模拟 agent 上传失败场景）。
	err error
}

// fakeRuntimeUploadCall 记录单次 UploadAppRuntimeFile 调用的参数。
type fakeRuntimeUploadCall struct {
	nodeID  string
	appID   string
	relPath string
}

func (f *fakeRuntimeFileWriter) UploadAppRuntimeFile(_ context.Context, nodeID, appID, relPath string, content io.Reader) error {
	// 消耗 content，避免调用方 strings.NewReader 被留在半读状态。
	_, _ = io.ReadAll(content)
	f.calls = append(f.calls, fakeRuntimeUploadCall{nodeID: nodeID, appID: appID, relPath: relPath})
	return f.err
}

// hasUpload 检查是否存在针对给定 appID + relPath 的上传记录。
func (f *fakeRuntimeFileWriter) hasUpload(appID, relPath string) bool {
	for _, c := range f.calls {
		if c.appID == appID && c.relPath == relPath {
			return true
		}
	}
	return false
}

// fakeKnowledgeReader 实现 KnowledgeReader,用于测试 writeSkillsFromKnowledge
// 是否按主副本子树展开成 .hermes/skills/kb-*-<slug>/SKILL.md。
//
// files 用主副本绝对路径(含 prefix)做 key,模拟一个内存中的主副本目录。
type fakeKnowledgeReader struct {
	files map[string]string
}

func (f *fakeKnowledgeReader) WalkFiles(prefix string, fn func(relPath string, size int64) error) error {
	for full, content := range f.files {
		if !strings.HasPrefix(full, prefix+"/") {
			continue
		}
		rel := strings.TrimPrefix(full, prefix+"/")
		if err := fn(rel, int64(len(content))); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeKnowledgeReader) Open(masterPath string) (io.ReadCloser, int64, error) {
	c, ok := f.files[masterPath]
	if !ok {
		return nil, 0, fmt.Errorf("not found: %s", masterPath)
	}
	return io.NopCloser(strings.NewReader(c)), int64(len(c)), nil
}

// TestAppInitialize_WritesKnowledgeSkills 验证 app_initialize 在容器启动前
// 把组织 + 应用知识库文件渲染为 SKILL.md 上传到 .hermes/skills/。
//
// 覆盖场景:
//   - 组织级文件 billing-rules.md → skills/kb-org-billing-rules/SKILL.md
//   - 应用级文件 quickstart.md → skills/kb-app-quickstart/SKILL.md
//   - 子目录 policies/refund.md → skills/kb-org-policies-refund/SKILL.md
//   - 中文文件名走 fallback hash slug, 不破坏其他文件上传
func TestAppInitialize_WritesKnowledgeSkills(t *testing.T) {
	store := newAppInitStub(t)
	dirs := &fakeDirs{}
	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 99, Key: "sk-test"}}
	rw := &fakeRuntimeFileWriter{}
	orgPrefix := fmt.Sprintf("org/%s/knowledge", testOrgID)
	appPrefix := fmt.Sprintf("org/%s/app/%s/knowledge", testOrgID, testAppID)
	reader := &fakeKnowledgeReader{files: map[string]string{
		orgPrefix + "/billing-rules.md":    "# 计费\n月度结算。",
		orgPrefix + "/policies/refund.md":  "# 退款政策",
		appPrefix + "/quickstart.md":       "# 应用使用入门",
		appPrefix + "/中文文件.md":             "# 仅供 fallback 测试",
	}}

	cfg := AppInitializeConfig{
		RuntimeImage:         "hermes:dev",
		SystemPromptTemplate: "你是 {org_name} 的助手",
		Cipher:               testCipher(t),
	}
	handler := NewAppInitializeHandler(store, dirs, containers, containers, client, cfg)
	handler.SetRuntimeFileWriter(rw)
	handler.SetKnowledgeReader(reader)

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")))

	// 必须存在的 skill 文件(基础英文文件名 → 标准 slug)。
	require.True(t, rw.hasUpload(testAppID, "skills/kb-org-billing-rules/SKILL.md"),
		"组织级 billing-rules.md 应渲染为 kb-org-billing-rules")
	require.True(t, rw.hasUpload(testAppID, "skills/kb-org-policies-refund/SKILL.md"),
		"子目录 policies/refund.md 应展平成 kb-org-policies-refund")
	require.True(t, rw.hasUpload(testAppID, "skills/kb-app-quickstart/SKILL.md"),
		"应用级 quickstart.md 应渲染为 kb-app-quickstart")
	// 中文文件名走 fallback,不应阻塞其他文件上传——至少一条以 kb-app-kb- 开头的 fallback 记录。
	var hasFallback bool
	for _, c := range rw.calls {
		if strings.HasPrefix(c.relPath, "skills/kb-app-kb-") {
			hasFallback = true
			break
		}
	}
	require.True(t, hasFallback, "中文文件名应走 sha256 fallback,实际上传记录: %+v", rw.calls)
}

// TestAppInitialize_KnowledgeReaderNilSkipsSkills 验证 KnowledgeReader 未注入时
// writeHermesFiles 仅上传 SOUL/config/.env,不报错(向后兼容旧装配 / 测试)。
func TestAppInitialize_KnowledgeReaderNilSkipsSkills(t *testing.T) {
	store := newAppInitStub(t)
	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "n"}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	rw := &fakeRuntimeFileWriter{}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{Cipher: testCipher(t)})
	handler.SetRuntimeFileWriter(rw)
	// 不调 SetKnowledgeReader,h.knowledge 保持 nil。

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")))

	for _, c := range rw.calls {
		require.False(t, strings.HasPrefix(c.relPath, "skills/"),
			"KnowledgeReader 未注入时不应上传任何 skill: %s", c.relPath)
	}
}

// fakeAuditRecorder 实现 audit.AuditRecorder，用于断言审计事件被写入。
type fakeAuditRecorder struct {
	events []service.AuditEvent
}

func (f *fakeAuditRecorder) Record(_ context.Context, event service.AuditEvent) (service.AuditResult, error) {
	f.events = append(f.events, event)
	return service.AuditResult{}, nil
}

// TestEnsureAPIKey_CreateAPIKeyFailureRecordsAudit 验证确保APIKey创建APIKey失败记录审计的错误映射或错误记录场景。
func TestEnsureAPIKey_CreateAPIKeyFailureRecordsAudit(t *testing.T) {
	store := newAppInitStub(t)
	rec := &fakeAuditRecorder{}
	helper := audit.NewNewAPIAuditHelper(rec)
	// UserScopedFor 成功，CreateAPIKey 失败
	client := &fakeNewAPI{createKeyErr: newapi.ErrUpstream}

	cfg := AppInitializeConfig{
		Cipher:      testCipher(t),
		AuditHelper: helper,
	}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, client, cfg)

	err := handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	require.ErrorIs(t, err, newapi.ErrUpstream)
	require.Equal(t, 1, len(rec.events))
	require.Equal(t, "newapi_call", rec.events[0].TargetType)
	require.Equal(t, "failed", rec.events[0].Result)
}

// TestEnsureAPIKey_GetTokenFullKeyFailureRecordsAudit 验证确保APIKey获取令牌完整Key失败记录审计的错误映射或错误记录场景。
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
		Cipher:      testCipher(t),
		AuditHelper: helper,
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

// errNodeDockerProvider 实现 NodeDockerProvider，DockerClientForNode 总是返回预设错误。
// 用于测试 phasePullRuntimeImage 在获取 Docker 客户端失败时的错误路径。
type errNodeDockerProvider struct {
	err error
}

func (p *errNodeDockerProvider) DockerClientForNode(_ context.Context, _ string) (*dockerclient.Client, error) {
	return nil, p.err
}

// TestAppInitializeHandler_Phases_Progress 验证 5 阶段每阶段都把 status 推进一格,
// 共 5 次 SetAppStatus(4 个 init 子状态 + binding_waiting)。
//
// imagePullCoord / nodeDockerProv 均未注入，phasePullRuntimeImage 直接跳过；
// 其它依赖用既有 fake stub 让 happy path 跑完。
func TestAppInitializeHandler_Phases_Progress(t *testing.T) {
	store := newAppInitStub(t)
	// 起始 status=draft,模拟新建 app
	store.app.Status = domain.AppStatusDraft

	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "sk-test"}}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{
		RuntimeImage: "hermes:dev",
		Cipher:       testCipher(t),
	})
	handler.SetRuntimeFileWriter(&fakeRuntimeFileWriter{})

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")))

	// 期望按顺序触发:pulling_runtime_image → preparing_runtime →
	// creating_container → starting → binding_waiting,共 5 次状态切换。
	// phasePullRuntimeImage 因未注入 coord/provider 直接跳过，但状态仍被推进。
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
		// expect 是该阶段名,期望写入 MarkAppFailed.LastErrorStatus
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
				h := NewAppInitializeHandler(s, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}, AppInitializeConfig{Cipher: testCipher(t)})
				h.SetRuntimeFileWriter(&fakeRuntimeFileWriter{})
				// 注入非 nil coordinator（不会被调用，因 nodeDockerProv 更早失败）。
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
				h := NewAppInitializeHandler(s, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}, AppInitializeConfig{Cipher: testCipher(t)})
				h.SetRuntimeFileWriter(&fakeRuntimeFileWriter{})
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
				h := NewAppInitializeHandler(s, &fakeDirs{}, containers, containers, &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}, AppInitializeConfig{Cipher: testCipher(t)})
				h.SetRuntimeFileWriter(&fakeRuntimeFileWriter{})
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
				h := NewAppInitializeHandler(s, &fakeDirs{}, containers, containers, &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}, AppInitializeConfig{Cipher: testCipher(t)})
				h.SetRuntimeFileWriter(&fakeRuntimeFileWriter{})
				return h, s
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, s := c.build(t)
			err := h.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
			require.Error(t, err, "失败 stub 应返回 error")
			// MarkAppFailed 必须被调用,LastErrorStatus 应等于 case 期望阶段。
			require.True(t, s.failedSet, "MarkAppFailed 应被调用")
			require.True(t, s.lastFailed.LastErrorStatus.Valid)
			assert.Equal(t, c.expect, s.lastFailed.LastErrorStatus.String)
			// 终态应收敛到 error。
			assert.Equal(t, domain.AppStatusError, s.app.Status)
		})
	}
}

// TestAppInitializeHandler_IdempotentReentry 模拟 manager 在前次 init 跑到容器创建之后
// 才崩溃 / 重启;reaper 把 status 重置回 draft 入口重跑(状态机仅允许 draft / error →
// pulling_image 这两条入口),但 container_id 已写入数据库。
// 此时 Handle 重入应:
//   - 把状态从 draft 逐阶段推到 binding_waiting,共 6 次 SetAppStatus;
//   - phaseCreate 看到 container_id 已存在,跳过 CreateContainer 不重复创建容器。
func TestAppInitializeHandler_IdempotentReentry(t *testing.T) {
	store := newAppInitStub(t)
	// app 起始 draft + container_id 已存在,模拟 reaper 重置 status 但保留 container_id。
	store.app.Status = domain.AppStatusDraft
	store.app.ContainerID = pgtype.Text{String: "cid-1", Valid: true}

	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "hermes-" + testAppID}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	handler := NewAppInitializeHandler(store, &fakeDirs{}, containers, containers, client, AppInitializeConfig{
		RuntimeImage: "hermes:dev",
		Cipher:       testCipher(t),
	})
	handler.SetRuntimeFileWriter(&fakeRuntimeFileWriter{})

	require.NoError(t, handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")))

	// container_id 已存在,phaseCreate 必须跳过 CreateContainer。
	assert.Equal(t, 0, containers.calls, "container_id 已存在不应再创建")
	// 终态应推进到 binding_waiting。
	assert.Equal(t, domain.AppStatusBindingWaiting, store.app.Status)
	// 不应触发失败。
	assert.False(t, store.failedSet)
}
