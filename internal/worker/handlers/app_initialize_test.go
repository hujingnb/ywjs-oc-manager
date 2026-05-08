package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/audit"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	runtimepkg "oc-manager/internal/integrations/runtime"
	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

const (
	testAppID = "00000000-0000-0000-0000-000000000a01"
	testOrgID = "00000000-0000-0000-0000-000000000b01"
	testUsrID = "00000000-0000-0000-0000-000000000c01"
)

func TestAppInitializeHandlesHappyPath(t *testing.T) {
	store := newAppInitStub(t)
	images := &fakeImages{}
	dirs := &fakeDirs{}
	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "ocm-" + testAppID, Status: "created"}}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 99, Key: "sk-test"}}

	cipher, err := auth.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	cfg := AppInitializeConfig{
		RuntimeImage:         "openclaw:dev",
		SystemPromptTemplate: "工作目录:{{workspace_dir}} 组织:{{knowledge_org_dir}} 应用:{{knowledge_app_dir}}",
		Cipher:               cipher,
	}
	handler := NewAppInitializeHandler(store, images, dirs, containers, containers, client, cfg)

	if err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if !store.apiKeySet || !store.statusSet || !store.containerSet {
		t.Fatalf("api_key/status/container 应当都被持久化: %+v", store)
	}
	if images.lastImage != "openclaw:dev" || images.lastNode != "node-1" {
		t.Fatalf("镜像分发 = %s/%s", images.lastNode, images.lastImage)
	}

	// container_id 写库为 docker mock 返回的 ID。
	if store.app.ContainerID.String != "ctr-1" {
		t.Fatalf("container_id = %q, want ctr-1", store.app.ContainerID.String)
	}

	// ciphertext 必须可被同一 cipher 解回 sk-test，证明真的走了加密路径。
	plain, err := cipher.Decrypt(store.app.NewapiKeyCiphertext.String)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(plain) != "sk-test" {
		t.Fatalf("decrypted = %q, want sk-test", plain)
	}
	if store.app.NewapiKeyCiphertext.String == "sk-test" {
		t.Fatal("ciphertext 等于明文，加密未生效")
	}

	// 容器规格断言：6 个挂载（5 个业务目录 + 1 个 weixin plugin token 目录）。
	// 早期版本曾有 models.json file-level mount 但因 EBUSY 问题已移除，
	// 改由 worker 在容器启动后通过 docker exec openclaw config patch 注入 catalog。
	if len(containers.lastSpec.Volumes) != 6 {
		t.Fatalf("Volumes 数量 = %d, want 6", len(containers.lastSpec.Volumes))
	}
	// 反向断言：models.json 不能再被 file-level bind mount。早期实现把渲染好的
	// catalog 用 RW 文件级 mount 覆盖容器内 models.json，但 OpenClaw embedded agent
	// 在响应消息时会 atomic rename .tmp -> models.json，撞 mount point 触发 EBUSY，
	// 导致 weixin 回 "Something went wrong"。改为容器启动后用 docker exec
	// `openclaw config patch` 把 models.providers 写到 openclaw.json 规避 mount 冲突。
	for _, vol := range containers.lastSpec.Volumes {
		if vol.ContainerPath == "/root/.openclaw/agents/main/agent/models.json" {
			t.Fatalf("models.json file-level mount 已废弃但仍出现在 ContainerSpec：host=%q", vol.HostPath)
		}
	}
	if containers.lastSpec.Image != "openclaw:dev" {
		t.Fatalf("Image = %q", containers.lastSpec.Image)
	}
	if containers.lastSpec.Name != "ocm-"+testAppID {
		t.Fatalf("Name = %q", containers.lastSpec.Name)
	}
	// Sprint 0 契约：上游 OpenClaw 内置 openai SDK 认 OPENAI_API_KEY，不是 OPENCLAW_API_KEY
	if containers.lastSpec.Env["OPENAI_API_KEY"] != "sk-test" {
		t.Fatalf("OPENAI_API_KEY env = %q, want sk-test", containers.lastSpec.Env["OPENAI_API_KEY"])
	}
	if containers.lastSpec.Env["OPENCLAW_WORKSPACE_DIR"] != "/workspace" {
		t.Fatalf("OPENCLAW_WORKSPACE_DIR env = %q", containers.lastSpec.Env["OPENCLAW_WORKSPACE_DIR"])
	}
	if containers.lastSpec.Env["OPENCLAW_DISABLE_BONJOUR"] != "1" {
		t.Fatalf("应注入 OPENCLAW_DISABLE_BONJOUR=1，got %q", containers.lastSpec.Env["OPENCLAW_DISABLE_BONJOUR"])
	}
	prompt := containers.lastSpec.Env["OPENCLAW_SYSTEM_PROMPT"]
	if !strings.Contains(prompt, "/workspace") || !strings.Contains(prompt, "/knowledge/org") || !strings.Contains(prompt, "/knowledge/app") {
		t.Fatalf("system prompt 未展开占位符: %q", prompt)
	}

	// Sprint 1：InitAppDirs 与 StartContainer 必须被调对参数
	if dirs.calls != 1 || dirs.lastNode != "node-1" || dirs.lastApp != testAppID {
		t.Fatalf("InitAppDirs 调用 = %+v", dirs)
	}
	if containers.startCalls != 1 || containers.lastStartNode != "node-1" || containers.lastStartID != "ctr-1" {
		t.Fatalf("StartContainer 调用 = calls=%d node=%s id=%s",
			containers.startCalls, containers.lastStartNode, containers.lastStartID)
	}
}

func TestAppInitializeWaitsForOpenClawHealthyWhenSupported(t *testing.T) {
	// Sprint 2：starter 同时实现 OpenClawHealthChecker 时 handler 应等 /healthz 通过再推 binding_waiting。
	store := newAppInitStub(t)
	images := &fakeImages{}
	dirs := &fakeDirs{}
	base := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "ctr-1", Name: "ocm-" + testAppID, Status: "created"}}
	containers := &healthAwareContainers{fakeContainers: base}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 99, Key: "sk-test"}}

	cipher, err := auth.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	handler := NewAppInitializeHandler(store, images, dirs, base, containers, client, AppInitializeConfig{
		RuntimeImage: "openclaw:dev",
		Cipher:       cipher,
	})
	if err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if base.healthCalls != 1 {
		t.Fatalf("WaitForOpenClawHealthy 调用 = %d", base.healthCalls)
	}
}

func TestAppInitializePropagatesHealthCheckError(t *testing.T) {
	store := newAppInitStub(t)
	base := &fakeContainers{
		result:    runtimepkg.ContainerInfo{ID: "ctr-1", Name: "ocm-" + testAppID, Status: "created"},
		healthErr: errors.New("/healthz timeout"),
	}
	containers := &healthAwareContainers{fakeContainers: base}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	handler := NewAppInitializeHandler(store, &fakeImages{}, &fakeDirs{}, base, containers, client, AppInitializeConfig{Cipher: testCipher(t)})

	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	if err == nil || !strings.Contains(err.Error(), "等待 OpenClaw 健康失败") {
		t.Fatalf("err=%v", err)
	}
	if store.statusSet {
		t.Fatal("健康检查失败时不应推 status")
	}
}

func TestAppInitializeIsIdempotentForBindingWaiting(t *testing.T) {
	store := newAppInitStub(t)
	store.app.Status = domain.AppStatusBindingWaiting
	store.app.ApiKeyStatus = domain.APIKeyStatusActive
	images := &fakeImages{}
	containers := &fakeContainers{}
	client := &fakeNewAPI{}

	handler := NewAppInitializeHandler(store, images, &fakeDirs{}, containers, containers, client, AppInitializeConfig{})
	if err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if client.calls != 0 {
		t.Fatalf("已 binding_waiting 时 new-api 不应被调用，calls = %d", client.calls)
	}
	if images.lastImage != "" {
		t.Fatalf("镜像分发应当跳过，got %s", images.lastImage)
	}
	if containers.calls != 0 {
		t.Fatal("CreateContainer 不应被调用")
	}
	if store.statusSet {
		t.Fatal("status 不应再次写入")
	}
}

func TestAppInitializeSkipsAPIKeyWhenAlreadyActive(t *testing.T) {
	store := newAppInitStub(t)
	cipher := testCipher(t)
	encrypted, err := cipher.Encrypt([]byte("sk-old-cached"))
	if err != nil {
		t.Fatalf("加密 fixture 失败: %v", err)
	}
	store.app.ApiKeyStatus = domain.APIKeyStatusActive
	store.app.NewapiKeyCiphertext = pgtype.Text{String: encrypted, Valid: true}
	client := &fakeNewAPI{}
	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "c", Name: "n"}}

	handler := NewAppInitializeHandler(store, &fakeImages{}, &fakeDirs{}, containers, containers, client, AppInitializeConfig{Cipher: cipher})
	if err := handler.Handle(context.Background(), buildJob(t, testAppID, "")); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if client.calls != 0 {
		t.Fatal("api_key 已 active 时 new-api 不应被调用")
	}
	if !store.statusSet {
		t.Fatal("status 仍应推到 binding_waiting")
	}
}

func TestAppInitializePropagatesNewAPIError(t *testing.T) {
	store := newAppInitStub(t)
	client := &fakeNewAPI{err: newapi.ErrUpstream}

	handler := NewAppInitializeHandler(store, &fakeImages{}, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, client, AppInitializeConfig{Cipher: testCipher(t)})
	err := handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	if !errors.Is(err, newapi.ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream", err)
	}
	if store.statusSet {
		t.Fatal("失败时不应推 status")
	}
}

func TestAppInitializePropagatesContainerError(t *testing.T) {
	store := newAppInitStub(t)
	containers := &fakeContainers{err: errors.New("boom")}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}
	handler := NewAppInitializeHandler(store, &fakeImages{}, &fakeDirs{}, containers, containers, client, AppInitializeConfig{Cipher: testCipher(t)})
	err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1"))
	if err == nil || !strings.Contains(err.Error(), "创建容器失败") {
		t.Fatalf("error = %v, want 创建容器失败", err)
	}
	if store.statusSet {
		t.Fatal("失败时不应推 status")
	}
}

func TestAppInitializeRejectsInvalidPayload(t *testing.T) {
	store := newAppInitStub(t)
	handler := NewAppInitializeHandler(store, &fakeImages{}, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, &fakeNewAPI{}, AppInitializeConfig{})

	job := sqlc.Job{Type: domain.JobTypeAppInitialize, PayloadJson: []byte(`{"runtime_node":"node-1"}`)}
	if err := handler.Handle(context.Background(), job); err == nil {
		t.Fatalf("缺 app_id 应当报错")
	}
}

func TestAppInitializeContainerStepSkippedWhenContainerExists(t *testing.T) {
	store := newAppInitStub(t)
	store.app.ContainerID = pgtype.Text{String: "already-there", Valid: true}
	containers := &fakeContainers{}
	client := &fakeNewAPI{result: newapi.APIKey{ID: 1, Key: "k"}}

	handler := NewAppInitializeHandler(store, &fakeImages{}, &fakeDirs{}, containers, containers, client, AppInitializeConfig{Cipher: testCipher(t)})
	if err := handler.Handle(context.Background(), buildJob(t, testAppID, "node-1")); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if containers.calls != 0 {
		t.Fatal("已有 container_id 时不应再次创建容器")
	}
	if store.containerSet {
		t.Fatal("container_id 不应被重写")
	}
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
	return s.org, nil
}
func (s *appInitStub) GetUser(_ context.Context, _ pgtype.UUID) (sqlc.User, error) { return s.user, nil }

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
	s.app.Status = arg.Status
	return s.app, nil
}

type fakeImages struct {
	lastImage string
	lastNode  string
}

func (f *fakeImages) EnsureRuntimeImage(_ context.Context, nodeID, image string) (any, error) {
	f.lastNode = nodeID
	f.lastImage = image
	return nil, nil
}

// fakeContainers 同时实现 ContainerCreator 与 ContainerLifecycle，
// 便于测试断言 Sprint 1 新增的 InitAppDirs / StartContainer 调用次序。
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
	// Sprint 2：可选实现 OpenClawHealthChecker。enableHealthCheck=true 时 fakeContainers
	// 暴露 WaitForOpenClawHealthy 方法（通过类型断言被 handler 探测）。
	enableHealthCheck bool
	healthCalls       int
	healthErr         error
}

// WaitForOpenClawHealthy 仅当 enableHealthCheck 为 true 时通过类型断言可见。
// 由于 Go 的接口断言看的是方法集（不论 enable flag），这里的 enable 通过 wrapper 实现。
// 所以测试用 healthAwareContainers 包装 fakeContainers 暴露此方法。
type healthAwareContainers struct {
	*fakeContainers
}

func (h *healthAwareContainers) WaitForOpenClawHealthy(_ context.Context, _, _ string) error {
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

// StartContainer 让 fakeContainers 同时实现 ContainerLifecycle 接口，
// 便于测试一并断言 Sprint 1 新增的 start 步骤。
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
	result       newapi.APIKey
	err          error // UserScopedFor / CreateAPIKey / SetAPIKeyStatus 公用错误
	createKeyErr error // 仅让 CreateAPIKey 失败，UserScopedFor 仍成功
	getKeyErr    error // 仅让 GetTokenFullKey 失败，CreateAPIKey 仍成功
	calls        int
}

func (f *fakeNewAPI) UserScopedFor(_ context.Context, _ sqlc.App) (APIKeyClient, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f, nil
}

func (f *fakeNewAPI) CreateAPIKey(_ context.Context, _ newapi.CreateAPIKeyInput) (newapi.APIKey, error) {
	f.calls++
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
	if err != nil {
		t.Fatalf("初始化测试 cipher 失败: %v", err)
	}
	return c
}

func mustUUIDForTest(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return id
}

// fakeAuditRecorder 实现 audit.AuditRecorder，用于断言审计事件被写入。
type fakeAuditRecorder struct {
	events []service.AuditEvent
}

func (f *fakeAuditRecorder) Record(_ context.Context, event service.AuditEvent) (service.AuditResult, error) {
	f.events = append(f.events, event)
	return service.AuditResult{}, nil
}

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
	handler := NewAppInitializeHandler(store, &fakeImages{}, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, client, cfg)

	err := handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	if !errors.Is(err, newapi.ErrUpstream) {
		t.Fatalf("err = %v, want ErrUpstream", err)
	}
	if len(rec.events) != 1 {
		t.Fatalf("审计事件数 = %d, want 1", len(rec.events))
	}
	if rec.events[0].TargetType != "newapi_call" {
		t.Fatalf("TargetType = %q, want newapi_call", rec.events[0].TargetType)
	}
	if rec.events[0].Result != "failed" {
		t.Fatalf("Result = %q, want failed", rec.events[0].Result)
	}
}

func TestEnsureAPIKey_GetTokenFullKeyFailureRecordsAudit(t *testing.T) {
	store := newAppInitStub(t)
	rec := &fakeAuditRecorder{}
	helper := audit.NewNewAPIAuditHelper(rec)
	// CreateAPIKey 成功，GetTokenFullKey 失败
	getKeyErr := errors.New("get-key-fail")
	client := &fakeNewAPI{
		result:     newapi.APIKey{ID: 42, Key: ""},
		getKeyErr:  getKeyErr,
	}

	cfg := AppInitializeConfig{
		Cipher:      testCipher(t),
		AuditHelper: helper,
	}
	handler := NewAppInitializeHandler(store, &fakeImages{}, &fakeDirs{}, &fakeContainers{}, &fakeContainers{}, client, cfg)

	err := handler.Handle(context.Background(), buildJob(t, testAppID, ""))
	if err == nil || !strings.Contains(err.Error(), "取完整 sk-") {
		t.Fatalf("err = %v", err)
	}
	if len(rec.events) != 1 {
		t.Fatalf("审计事件数 = %d, want 1", len(rec.events))
	}
	if rec.events[0].TargetType != "newapi_call" {
		t.Fatalf("TargetType = %q, want newapi_call", rec.events[0].TargetType)
	}
	// Endpoint 应含 token ID
	if !strings.Contains(rec.events[0].TargetID, "42") {
		t.Fatalf("Endpoint/TargetID = %q, want 含 42", rec.events[0].TargetID)
	}
}

// renderOpenClawModels 已删除：早期版本通过 file-level bind mount 把渲染好的 catalog
// 覆盖容器内 models.json，但 OpenClaw embedded agent 在响应消息时 atomic rename
// .tmp -> models.json 撞 mount point 触发 EBUSY。改为容器启动后用
// configureOpenClawDefaultModel 通过 docker exec openclaw config patch 注入 catalog 到
// openclaw.json。对应单元测试在 TestConfigureOpenClawDefaultModel_PatchesAgentAndModels。

// fakeContainerExec 捕获 ContainerExec 调用的命令行，用于验证 patch 内容。
type fakeContainerExec struct {
	calls []fakeExecCall
	res   ExecResultStub
}

type fakeExecCall struct {
	nodeID, containerID string
	cmd                 []string
}

// ExecResultStub 模拟 runtime.ExecResult，与生产 runtime 包同形态便于赋值返回。
type ExecResultStub struct {
	ExitCode int
	Stdout   string
}

func (f *fakeContainerExec) ContainerExec(_ context.Context, nodeID, containerID string, cmd []string) (runtimepkg.ExecResult, error) {
	f.calls = append(f.calls, fakeExecCall{nodeID: nodeID, containerID: containerID, cmd: cmd})
	return runtimepkg.ExecResult{ExitCode: f.res.ExitCode, Stdout: f.res.Stdout}, nil
}

func TestConfigureOpenClawDefaultModel_PatchesAgentAndModels(t *testing.T) {
	exec := &fakeContainerExec{res: ExecResultStub{ExitCode: 0, Stdout: "Patched config\n"}}
	llm := AppInitializeLLMConfig{
		BaseURL:         "http://new-api:3000/v1",
		DefaultProvider: "openai",
		DefaultModel:    "qwen2.5:0.5b",
	}
	if err := configureOpenClawDefaultModel(context.Background(), exec, "node-1", "ctn-1", llm); err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(exec.calls) != 1 {
		t.Fatalf("ContainerExec 调用次数=%d，期望 1", len(exec.calls))
	}
	call := exec.calls[0]
	if call.cmd[0] != "sh" || call.cmd[1] != "-c" {
		t.Fatalf("cmd 应是 sh -c form，got %v", call.cmd)
	}
	shellLine := call.cmd[2]
	if !strings.Contains(shellLine, "openclaw config patch --stdin") {
		t.Fatalf("shell line 缺 patch 命令：%q", shellLine)
	}
	// 验证 patch JSON 含必要字段
	if !strings.Contains(shellLine, `"agents"`) || !strings.Contains(shellLine, `"defaults"`) {
		t.Errorf("patch 缺 agents.defaults: %q", shellLine)
	}
	if !strings.Contains(shellLine, `"qwen2.5:0.5b"`) {
		t.Errorf("patch 缺 model 值: %q", shellLine)
	}
	// schema 要求 models[].name 必填，未填会被 OpenClaw config validate 拒绝
	if !strings.Contains(shellLine, `"name":"qwen2.5:0.5b"`) {
		t.Errorf("patch 缺 model name 字段: %q", shellLine)
	}
	if !strings.Contains(shellLine, `"models"`) || !strings.Contains(shellLine, `"providers"`) {
		t.Errorf("patch 缺 models.providers: %q", shellLine)
	}
	if !strings.Contains(shellLine, `"mode":"replace"`) {
		t.Errorf("patch 应含 models.mode=replace 避免 EBUSY: %q", shellLine)
	}
	if !strings.Contains(shellLine, "${OPENAI_API_KEY}") {
		t.Errorf("apiKey 应为 env 占位符: %q", shellLine)
	}
}

func TestConfigureOpenClawDefaultModel_SkipsWhenLLMIncomplete(t *testing.T) {
	exec := &fakeContainerExec{res: ExecResultStub{ExitCode: 0, Stdout: "Patched\n"}}
	cases := []AppInitializeLLMConfig{
		{},
		{BaseURL: "x", DefaultProvider: "openai"},      // 缺 model
		{BaseURL: "x", DefaultModel: "qwen2.5:0.5b"},   // 缺 provider
		{DefaultProvider: "openai", DefaultModel: "x"}, // 缺 base
		{BaseURL: "  ", DefaultProvider: "openai", DefaultModel: "x"},
	}
	for i, c := range cases {
		exec.calls = nil
		if err := configureOpenClawDefaultModel(context.Background(), exec, "node-1", "ctn-1", c); err != nil {
			t.Errorf("[%d] err=%v", i, err)
		}
		if len(exec.calls) != 0 {
			t.Errorf("[%d] LLM 不全应跳过 exec，但调了 %d 次", i, len(exec.calls))
		}
	}
}

func TestConfigureOpenClawDefaultModel_PropagatesPatchFailure(t *testing.T) {
	exec := &fakeContainerExec{res: ExecResultStub{ExitCode: 2, Stdout: "schema validation failed\n"}}
	llm := AppInitializeLLMConfig{
		BaseURL: "http://x", DefaultProvider: "openai", DefaultModel: "m",
	}
	err := configureOpenClawDefaultModel(context.Background(), exec, "node-1", "ctn-1", llm)
	if err == nil {
		t.Fatal("非零 exit code 应返回错误")
	}
	if !strings.Contains(err.Error(), "exit=2") {
		t.Errorf("错误应含 exit code: %v", err)
	}
}
