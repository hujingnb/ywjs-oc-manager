package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	runtimepkg "oc-manager/internal/integrations/runtime"
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
	handler := NewAppInitializeHandler(store, images, containers, client, cfg)

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

	// 容器规格断言：5 个挂载、关键 env 项、镜像、容器名。
	if len(containers.lastSpec.Volumes) != 5 {
		t.Fatalf("Volumes 数量 = %d, want 5", len(containers.lastSpec.Volumes))
	}
	if containers.lastSpec.Image != "openclaw:dev" {
		t.Fatalf("Image = %q", containers.lastSpec.Image)
	}
	if containers.lastSpec.Name != "ocm-"+testAppID {
		t.Fatalf("Name = %q", containers.lastSpec.Name)
	}
	if containers.lastSpec.Env["OPENCLAW_API_KEY"] != "sk-test" {
		t.Fatalf("OPENCLAW_API_KEY env = %q, want sk-test（容器内必须是明文）", containers.lastSpec.Env["OPENCLAW_API_KEY"])
	}
	if containers.lastSpec.Env["OPENCLAW_WORKSPACE_DIR"] != "/workspace" {
		t.Fatalf("OPENCLAW_WORKSPACE_DIR env = %q", containers.lastSpec.Env["OPENCLAW_WORKSPACE_DIR"])
	}
	prompt := containers.lastSpec.Env["OPENCLAW_SYSTEM_PROMPT"]
	if !strings.Contains(prompt, "/workspace") || !strings.Contains(prompt, "/knowledge/org") || !strings.Contains(prompt, "/knowledge/app") {
		t.Fatalf("system prompt 未展开占位符: %q", prompt)
	}
}

func TestAppInitializeIsIdempotentForBindingWaiting(t *testing.T) {
	store := newAppInitStub(t)
	store.app.Status = domain.AppStatusBindingWaiting
	store.app.ApiKeyStatus = domain.APIKeyStatusActive
	images := &fakeImages{}
	containers := &fakeContainers{}
	client := &fakeNewAPI{}

	handler := NewAppInitializeHandler(store, images, containers, client, AppInitializeConfig{})
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
	store.app.ApiKeyStatus = domain.APIKeyStatusActive
	store.app.NewapiKeyCiphertext = pgtype.Text{String: "old-key", Valid: true}
	client := &fakeNewAPI{}
	containers := &fakeContainers{result: runtimepkg.ContainerInfo{ID: "c", Name: "n"}}

	handler := NewAppInitializeHandler(store, &fakeImages{}, containers, client, AppInitializeConfig{})
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

	handler := NewAppInitializeHandler(store, &fakeImages{}, &fakeContainers{}, client, AppInitializeConfig{})
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
	handler := NewAppInitializeHandler(store, &fakeImages{}, containers, client, AppInitializeConfig{})
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
	handler := NewAppInitializeHandler(store, &fakeImages{}, &fakeContainers{}, &fakeNewAPI{}, AppInitializeConfig{})

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

	handler := NewAppInitializeHandler(store, &fakeImages{}, containers, client, AppInitializeConfig{})
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

type fakeContainers struct {
	result   runtimepkg.ContainerInfo
	err      error
	calls    int
	lastNode string
	lastSpec runtimepkg.ContainerSpec
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

type fakeNewAPI struct {
	result newapi.APIKey
	err    error
	calls  int
}

func (f *fakeNewAPI) CreateAPIKey(_ context.Context, _ newapi.CreateAPIKeyInput) (newapi.APIKey, error) {
	f.calls++
	if f.err != nil {
		return newapi.APIKey{}, f.err
	}
	return f.result, nil
}

func mustUUIDForTest(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return id
}
