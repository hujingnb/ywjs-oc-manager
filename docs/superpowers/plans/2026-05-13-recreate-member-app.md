# Recreate Member App Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let platform administrators create a new instance for an existing member after that member's previous instance has been deleted.

**Architecture:** Add a dedicated existing-member app creation path rather than overloading member onboarding. The new path reuses onboarding defaults and transaction boundaries, adds a centralized authorizer predicate, exposes one members-scoped HTTP route, and adds a platform-only row action on the members page.

**Tech Stack:** Go, Gin, pgx/sqlc, testify, Vue 3, TanStack Query, Naive UI, Vitest, swag/OpenAPI.

---

## File Map

- Modify `internal/auth/authorizer.go`: add `CanCreateAppForMember`.
- Modify `internal/auth/authorizer_test.go`: cover the new permission matrix.
- Modify `internal/service/onboarding_service.go`: extend `OnboardingStore`, add `CreateAppForMemberInput`, `CreateAppForMemberResult`, and `CreateAppForMember`.
- Modify `internal/service/onboarding_service_test.go`: cover success, active-app conflict, cross-org user, disabled user, and no-node cases.
- Modify `internal/api/handlers/dto.go`: add `CreateMemberAppRequest`.
- Modify `internal/api/handlers/members.go`: extend onboarding interface, register `POST /organizations/:orgId/members/:userId/apps`, add handler method and swagger annotations.
- Modify `internal/api/handlers/members_test.go`: add route forwarding and error mapping tests.
- Modify `web/src/api/hooks/useMembers.ts`: add payload/result types and `useCreateMemberApp`.
- Modify `web/src/pages/org/MembersPage.vue`: add platform-only create-instance modal and action.
- Modify `web/src/pages/org/MembersPage.spec.ts`: cover platform-only visibility and successful result feedback.
- Regenerate `openapi/openapi.yaml` and `web/src/api/generated.ts`.

## Task 1: Permission Predicate

**Files:**
- Modify: `internal/auth/authorizer_test.go`
- Modify: `internal/auth/authorizer.go`

- [ ] **Step 1: Write the failing authorizer test**

Add this test near `TestCanCreateAppForOrg` if present, otherwise after app-management tests:

```go
// TestCanCreateAppForMember 验证为已有成员创建实例的权限边界。
func TestCanCreateAppForMember(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 可为任意组织成员创建实例", domain.UserRolePlatformAdmin, orgA, orgB, true}, // 场景：平台管理员跨组织为已有成员重建实例。
		{"org_admin 可为本组织成员创建实例", domain.UserRoleOrgAdmin, orgA, orgA, true},           // 场景：组织管理员仍保留本组织创建实例能力。
		{"org_admin 不可为其他组织成员创建实例", domain.UserRoleOrgAdmin, orgA, orgB, false},       // 场景：组织管理员不能越过组织边界。
		{"org_member 不可创建实例", domain.UserRoleOrgMember, orgA, orgA, false},                // 场景：普通成员没有实例创建权限。
		{"未知角色不可创建实例", "unknown", orgA, orgA, false},                                  // 场景：未知角色降级为无权限。
	}
	runOrgCases(t, CanCreateAppForMember, cases)
}
```

- [ ] **Step 2: Run the failing authorizer test**

Run:

```bash
rtk go test ./internal/auth -run TestCanCreateAppForMember -count=1
```

Expected: FAIL because `CanCreateAppForMember` is undefined.

- [ ] **Step 3: Implement the predicate**

Add this function in `internal/auth/authorizer.go` near `CanCreateAppForOrg`:

```go
// CanCreateAppForMember 判断主体是否可为指定组织内的已有成员创建实例。
// 平台管理员负责跨组织运维复建；组织管理员只允许在本组织内创建；
// 普通成员不能自行创建或复建实例。
func CanCreateAppForMember(p Principal, orgID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == orgID
	default:
		return false
	}
}
```

- [ ] **Step 4: Verify the authorizer test passes**

Run:

```bash
rtk go test ./internal/auth -run 'TestCanCreateAppFor(Member|Org)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit permission change**

```bash
rtk git add internal/auth/authorizer.go internal/auth/authorizer_test.go
rtk git commit -m "feat(auth): 允许平台管理员为成员创建实例" -m "新增为已有成员创建实例的权限谓词，并覆盖平台管理员、组织管理员、普通成员和未知角色的权限边界。"
```

## Task 2: Service Flow

**Files:**
- Modify: `internal/service/onboarding_service_test.go`
- Modify: `internal/service/onboarding_service.go`

- [ ] **Step 1: Write the success and validation tests**

Extend `onboardingStub` with:

```go
	user          sqlc.User
	activeApp     *sqlc.App
	lastAppOwnerID string
```

Initialize `user` in `newOnboardingStub`:

```go
user: sqlc.User{
	ID:          mustUUID(t, "00000000-0000-0000-0000-000000000a11"),
	OrgID:       mustUUID(t, testOrgID),
	Username:    "alice",
	DisplayName: "Alice",
	Role:        domain.UserRoleOrgMember,
	Status:      domain.StatusActive,
},
```

Add stub methods:

```go
func (s *onboardingStub) GetUser(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
	if id != s.user.ID {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return s.user, nil
}

func (s *onboardingStub) GetActiveAppByOwner(_ context.Context, ownerUserID pgtype.UUID) (sqlc.App, error) {
	if s.activeApp == nil || ownerUserID != s.activeApp.OwnerUserID {
		return sqlc.App{}, pgx.ErrNoRows
	}
	return *s.activeApp, nil
}
```

Update `CreateApp` to record owner:

```go
s.lastAppOwnerID = uuidToString(arg.OwnerUserID)
```

Add imports if missing:

```go
	"github.com/jackc/pgx/v5"
```

Add tests:

```go
// TestCreateAppForMember_PlatformAdminCreatesAfterDelete 验证平台管理员可为无活跃实例的已有成员创建新实例。
func TestCreateAppForMember_PlatformAdminCreatesAfterDelete(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	result, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, uuidToString(store.user.ID), CreateAppForMemberInput{
		AppName: "alice-new-bot",
	})

	require.NoError(t, err)
	require.True(t, tx.committed)
	assert.Equal(t, "alice-new-bot", result.App.Name)
	assert.Equal(t, uuidToString(store.user.ID), store.lastAppOwnerID)
	assert.NotEmpty(t, result.JobID)
	require.Len(t, store.auditLogs, 1)
	assert.Equal(t, "app", store.auditLogs[0].TargetType)
	assert.Equal(t, "create_for_existing_member", store.auditLogs[0].Action)
}

// TestCreateAppForMember_RejectsExistingActiveApp 验证成员已有未删除实例时拒绝创建新实例。
func TestCreateAppForMember_RejectsExistingActiveApp(t *testing.T) {
	store := newOnboardingStub(t)
	existing := sqlc.App{ID: mustUUID(t, "00000000-0000-0000-0000-000000000b99"), OwnerUserID: store.user.ID}
	store.activeApp = &existing
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, uuidToString(store.user.ID), CreateAppForMemberInput{
		AppName: "alice-new-bot",
	})

	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	require.False(t, tx.committed)
}

// TestCreateAppForMember_RejectsCrossOrgUser 验证路径组织与目标用户组织不一致时按不存在处理。
func TestCreateAppForMember_RejectsCrossOrgUser(t *testing.T) {
	store := newOnboardingStub(t)
	store.user.OrgID = mustUUID(t, testOrg2ID)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, uuidToString(store.user.ID), CreateAppForMemberInput{
		AppName: "alice-new-bot",
	})

	require.ErrorIs(t, err, ErrNotFound)
	require.False(t, tx.committed)
}

// TestCreateAppForMember_RejectsDisabledUser 验证已下线成员不能创建新的可运行实例。
func TestCreateAppForMember_RejectsDisabledUser(t *testing.T) {
	store := newOnboardingStub(t)
	store.user.Status = domain.StatusDisabled
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, uuidToString(store.user.ID), CreateAppForMemberInput{
		AppName: "alice-new-bot",
	})

	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	require.False(t, tx.committed)
}

// TestCreateAppForMember_NoActiveNode 验证自动选节点无容量时返回无可用节点。
func TestCreateAppForMember_NoActiveNode(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, &nodeSelectorStub{})

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, uuidToString(store.user.ID), CreateAppForMemberInput{
		AppName: "alice-new-bot",
	})

	require.ErrorIs(t, err, ErrNoNodeAvailable)
	require.False(t, tx.committed)
}
```

- [ ] **Step 2: Run the service tests and confirm RED**

Run:

```bash
rtk go test ./internal/service -run 'TestCreateAppForMember' -count=1
```

Expected: FAIL because `CreateAppForMemberInput` and `CreateAppForMember` are undefined, and the stub no longer satisfies `OnboardingStore` until the interface is extended.

- [ ] **Step 3: Implement the service API and transaction**

Update `OnboardingStore`:

```go
	GetUser(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	GetActiveAppByOwner(ctx context.Context, ownerUserID pgtype.UUID) (sqlc.App, error)
```

Add result/input types near onboarding types:

```go
// CreateAppForMemberInput 描述为已有成员重建实例时需要的应用初始化字段。
type CreateAppForMemberInput struct {
	AppName     string
	AppPrompt   string
	PersonaMode string
	ChannelType string
	NodeID      string
}

// CreateAppForMemberResult 是为已有成员创建新实例后的视图。
type CreateAppForMemberResult struct {
	App   AppResult `json:"app"`
	JobID string    `json:"job_id"`
}
```

Add method:

```go
// CreateAppForMember 为已有成员创建新的应用实例。
// 它只允许目标成员当前没有未删除应用；旧删除记录保留，新的应用重新创建初始化任务。
func (s *MemberOnboardingService) CreateAppForMember(ctx context.Context, principal auth.Principal, orgID, userID string, input CreateAppForMemberInput) (CreateAppForMemberResult, error) {
	if !auth.CanCreateAppForMember(principal, orgID) {
		return CreateAppForMemberResult{}, ErrForbidden
	}
	if input.AppName == "" {
		return CreateAppForMemberResult{}, fmt.Errorf("%w: 应用名不能为空", ErrMemberCreateInvalid)
	}
	channelType := input.ChannelType
	if channelType == "" {
		channelType = domain.ChannelTypeWeChat
	}
	personaMode := input.PersonaMode
	if personaMode == "" {
		personaMode = domain.PersonaModeOrgInherited
	}
	orgUUID, err := parseUUID(orgID)
	if err != nil {
		return CreateAppForMemberResult{}, ErrNotFound
	}
	userUUID, err := parseUUID(userID)
	if err != nil {
		return CreateAppForMemberResult{}, ErrNotFound
	}
	if input.NodeID == "" {
		chosen, err := s.selectNode(ctx)
		if err != nil {
			return CreateAppForMemberResult{}, err
		}
		input.NodeID = chosen
	}

	var result CreateAppForMemberResult
	txErr := s.tx.WithTx(ctx, func(store OnboardingStore) error {
		org, err := store.GetOrganization(ctx, orgUUID)
		if err != nil {
			return fmt.Errorf("查询组织失败: %w", err)
		}
		if org.Status != domain.StatusActive {
			return fmt.Errorf("%w: 组织已停用", ErrMemberCreateInvalid)
		}
		user, err := store.GetUser(ctx, userUUID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("查询成员失败: %w", err)
		}
		if user.OrgID != org.ID {
			return ErrNotFound
		}
		if user.Status == domain.StatusDisabled {
			return fmt.Errorf("%w: 成员已下线", ErrMemberCreateInvalid)
		}
		if _, err := store.GetActiveAppByOwner(ctx, user.ID); err == nil {
			return fmt.Errorf("%w: 成员已有未删除实例", ErrMemberCreateInvalid)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("查询成员应用失败: %w", err)
		}
		nodeUUID, err := optionalUUID(input.NodeID)
		if err != nil {
			return fmt.Errorf("非法 runtime node id: %w", err)
		}
		app, err := store.CreateApp(ctx, sqlc.CreateAppParams{
			OrgID:         org.ID,
			OwnerUserID:   user.ID,
			RuntimeNodeID: nodeUUID,
			Name:          input.AppName,
			Description:   pgtype.Text{},
			Status:        domain.AppStatusDraft,
			PersonaMode:   personaMode,
			AppPrompt:     pgtype.Text{String: input.AppPrompt, Valid: input.AppPrompt != ""},
			ApiKeyStatus:  domain.APIKeyStatusPending,
		})
		if err != nil {
			return fmt.Errorf("创建应用失败: %w", err)
		}
		if _, err := store.CreateChannelBinding(ctx, sqlc.CreateChannelBindingParams{
			AppID:       app.ID,
			ChannelType: channelType,
			Status:      domain.ChannelStatusUnbound,
		}); err != nil {
			return fmt.Errorf("创建渠道绑定失败: %w", err)
		}
		actorUUID, _ := optionalUUID(principal.UserID)
		metadata, err := json.Marshal(map[string]any{
			"owner_user_id":   uuidToString(user.ID),
			"channel_type":    channelType,
			"runtime_node_id": input.NodeID,
		})
		if err != nil {
			return fmt.Errorf("序列化应用创建审计元数据失败: %w", err)
		}
		if _, err := store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
			ActorID:      actorUUID,
			ActorRole:    principal.Role,
			OrgID:        org.ID,
			TargetType:   "app",
			TargetID:     uuidToString(app.ID),
			Action:       "create_for_existing_member",
			Result:       "succeeded",
			MetadataJson: metadata,
		}); err != nil {
			return fmt.Errorf("写入应用创建审计日志失败: %w", err)
		}
		payload, err := json.Marshal(map[string]any{
			"app_id":       uuidToString(app.ID),
			"runtime_node": input.NodeID,
		})
		if err != nil {
			return fmt.Errorf("序列化 job payload 失败: %w", err)
		}
		job, err := store.CreateJob(ctx, sqlc.CreateJobParams{
			Type:        domain.JobTypeAppInitialize,
			Priority:    100,
			RunAfter:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
			MaxAttempts: 5,
			PayloadJson: payload,
		})
		if err != nil {
			return fmt.Errorf("创建初始化任务失败: %w", err)
		}
		result = CreateAppForMemberResult{App: toAppResult(app), JobID: uuidToString(job.ID)}
		return nil
	})
	if txErr != nil {
		return CreateAppForMemberResult{}, txErr
	}
	return result, nil
}
```

Ensure `onboarding_service.go` imports `github.com/jackc/pgx/v5`.

- [ ] **Step 4: Verify service tests pass**

Run:

```bash
rtk go test ./internal/service -run 'Test(CreateAppForMember|OnboardMember)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit service change**

```bash
rtk git add internal/service/onboarding_service.go internal/service/onboarding_service_test.go
rtk git commit -m "feat(service): 支持为已有成员创建实例" -m "在 onboarding service 中新增复建成员实例流程，校验用户归属、账号状态和活跃实例冲突，并复用应用、渠道、审计和初始化任务创建事务。"
```

## Task 3: HTTP Route And DTO

**Files:**
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/members.go`
- Modify: `internal/api/handlers/members_test.go`

- [ ] **Step 1: Write failing handler tests**

Extend `onboardingServiceStub`:

```go
	createAppResult service.CreateAppForMemberResult
	lastOrgID        string
	lastUserID       string
	lastCreateInput  service.CreateAppForMemberInput
```

Add method:

```go
func (s *onboardingServiceStub) CreateAppForMember(_ context.Context, _ auth.Principal, orgID, userID string, input service.CreateAppForMemberInput) (service.CreateAppForMemberResult, error) {
	s.lastOrgID = orgID
	s.lastUserID = userID
	s.lastCreateInput = input
	if s.err != nil {
		return service.CreateAppForMemberResult{}, s.err
	}
	return s.createAppResult, nil
}
```

Add tests:

```go
// TestMembersCreateAppForMemberForwardsRequest 验证已有成员创建实例路由转发组织、成员和应用字段。
func TestMembersCreateAppForMemberForwardsRequest(t *testing.T) {
	onboarding := &onboardingServiceStub{
		createAppResult: service.CreateAppForMemberResult{
			App: service.AppResult{ID: "app-1", Name: "alice-new-bot", Status: domain.AppStatusDraft},
			JobID: "job-1",
		},
	}
	router, tokens := newMembersTestRouterWithOnboarding(t, &memberServiceStub{}, onboarding)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "p1", Role: domain.UserRolePlatformAdmin})

	recorder := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"app_name":"alice-new-bot","persona_mode":"app_override","app_prompt":"hello","channel_type":"wechat","runtime_node_id":"node-1"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/members/user-1/apps", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, "org-1", onboarding.lastOrgID)
	require.Equal(t, "user-1", onboarding.lastUserID)
	require.Equal(t, "alice-new-bot", onboarding.lastCreateInput.AppName)
	require.Contains(t, recorder.Body.String(), `"job_id":"job-1"`)
}

// TestMembersCreateAppForMemberMapsNoNodeAvailable 验证已有成员创建实例无可用节点时映射为 503。
func TestMembersCreateAppForMemberMapsNoNodeAvailable(t *testing.T) {
	onboarding := &onboardingServiceStub{err: service.ErrNoNodeAvailable}
	router, tokens := newMembersTestRouterWithOnboarding(t, &memberServiceStub{}, onboarding)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "p1", Role: domain.UserRolePlatformAdmin})

	recorder := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"app_name":"alice-new-bot"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/members/user-1/apps", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "NO_NODE_AVAILABLE")
}
```

- [ ] **Step 2: Run handler tests and confirm RED**

Run:

```bash
rtk go test ./internal/api/handlers -run 'TestMembersCreateAppForMember' -count=1
```

Expected: FAIL because the route and request DTO do not exist.

- [ ] **Step 3: Implement DTO, interface, route and handler**

Add DTO:

```go
// CreateMemberAppRequest 为已有成员创建新实例的请求体。
type CreateMemberAppRequest struct {
	// AppName 是新实例名称，创建时必填。
	AppName string `json:"app_name" binding:"required"`
	// AppPrompt 是新实例提示词，可为空。
	AppPrompt string `json:"app_prompt"`
	// PersonaMode 控制新实例是否继承组织人设或使用独立人设。
	PersonaMode string `json:"persona_mode"`
	// ChannelType 是初始化渠道绑定的渠道标识。
	ChannelType string `json:"channel_type"`
	// NodeID 是指定 runtime 节点；为空时 service 自动选择可用节点。
	NodeID string `json:"runtime_node_id"`
}
```

Extend `onboardingService`:

```go
	CreateAppForMember(ctx context.Context, principal auth.Principal, orgID, userID string, input service.CreateAppForMemberInput) (service.CreateAppForMemberResult, error)
```

Register route:

```go
	orgGroup.POST("/:userId/apps", handler.CreateAppForMember)
```

Add handler method:

```go
// CreateAppForMember 为已有成员创建新的应用实例。
//
// @Summary      为已有成员创建实例
// @Description  平台管理员或本组织管理员为已有成员创建新的应用实例；目标成员必须没有未删除实例
// @Tags         members
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgId   path      string                  true  "组织 ID"
// @Param        userId  path      string                  true  "成员用户 ID"
// @Param        body    body      CreateMemberAppRequest  true  "创建实例请求"
// @Success      201     {object}  map[string]service.CreateAppForMemberResult
// @Failure      400     {object}  ErrorResponse
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Failure      503     {object}  ErrorResponse
// @Router       /organizations/{orgId}/members/{userId}/apps [post]
func (h *MembersHandler) CreateAppForMember(c *gin.Context) {
	if h.onboarding == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "成员实例创建流程暂未启用"})
		return
	}
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	var req CreateMemberAppRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.onboarding.CreateAppForMember(c.Request.Context(), principal, c.Param("orgId"), c.Param("userId"), service.CreateAppForMemberInput{
		AppName:     req.AppName,
		AppPrompt:   req.AppPrompt,
		PersonaMode: req.PersonaMode,
		ChannelType: req.ChannelType,
		NodeID:      req.NodeID,
	})
	if err != nil {
		writeMemberError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"member_app": result})
}
```

- [ ] **Step 4: Verify handler tests pass**

Run:

```bash
rtk go test ./internal/api/handlers -run 'TestMembers(CreateAppForMember|Onboard)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit handler change**

```bash
rtk git add internal/api/handlers/dto.go internal/api/handlers/members.go internal/api/handlers/members_test.go
rtk git commit -m "feat(api): 增加成员实例复建接口" -m "新增为已有成员创建实例的 members 路由、请求体、错误映射测试，并复用 onboarding service 的事务能力。"
```

## Task 4: Frontend Hook And Members Page

**Files:**
- Modify: `web/src/api/hooks/useMembers.ts`
- Modify: `web/src/pages/org/MembersPage.vue`
- Modify: `web/src/pages/org/MembersPage.spec.ts`

- [ ] **Step 1: Write failing page tests**

In `MembersPage.spec.ts`, hoist a mock create mutation:

```ts
const createMemberAppMock = vi.hoisted(() => ({
  mutateAsync: vi.fn(async () => ({
    app: { id: 'app-1', name: '新实例', status: 'draft', persona_mode: 'org_inherited', api_key_status: 'pending' },
    job_id: 'job-1',
  })),
}))
```

Add to `vi.mock('@/api/hooks/useMembers'...)`:

```ts
  useCreateMemberApp: () => ({ mutateAsync: createMemberAppMock.mutateAsync, isPending: ref(false) }),
```

Make `NInput` support `v-model:value` in the stubs:

```ts
        NInput: defineComponent({
          props: ['value'],
          emits: ['update:value'],
          setup(props, { emit }) {
            return () => h('input', {
              value: props.value,
              onInput: (event: Event) => emit('update:value', (event.target as HTMLInputElement).value),
            })
          },
        }),
```

Add tests:

```ts
  // 平台管理员可在成员行看到创建新实例入口，用于已删除实例后的复建。
  it('平台管理员可看到创建新实例入口', () => {
    authUser.current = { id: 'admin-1', role: 'platform_admin' }

    const wrapper = mountPage()

    expect(wrapper.findAll('button').some(button => button.text() === '创建新实例')).toBe(true)
  })

  // 组织管理员仍通过原开户入口创建成员，不显示平台复建实例入口。
  it('组织管理员看不到平台复建实例入口', () => {
    authUser.current = { id: 'admin-1', role: 'org_admin', org_id: 'org-1' }

    const wrapper = mountPage()

    expect(wrapper.findAll('button').some(button => button.text() === '创建新实例')).toBe(false)
  })

  // 平台管理员提交实例表单后展示新实例与初始化任务结果。
  it('平台管理员提交创建新实例后展示结果', async () => {
    authUser.current = { id: 'admin-1', role: 'platform_admin' }
    createMemberAppMock.mutateAsync.mockClear()
    const wrapper = mountPage()

    await wrapper.findAll('button').find(button => button.text() === '创建新实例')!.trigger('click')
    await wrapper.find('input').setValue('新实例')
    await wrapper.findAll('button').find(button => button.text() === '提交创建')!.trigger('click')

    expect(createMemberAppMock.mutateAsync).toHaveBeenCalledWith({ userId: 'member-1', payload: expect.objectContaining({ app_name: '新实例' }) })
    expect(wrapper.text()).toContain('已创建实例 新实例')
    expect(wrapper.text()).toContain('job-1')
  })
```

- [ ] **Step 2: Run frontend tests and confirm RED**

Run:

```bash
rtk npm --prefix web test -- --run src/pages/org/MembersPage.spec.ts
```

Expected: FAIL because `useCreateMemberApp` and UI are missing.

- [ ] **Step 3: Implement hook**

Add to `web/src/api/hooks/useMembers.ts`:

```ts
import type { App } from '@/api'
```

Add types and hook:

```ts
// CreateMemberAppPayload 是平台管理员为已有成员创建新实例的表单提交体。
export interface CreateMemberAppPayload {
  // 新实例名称。
  app_name: string
  // 应用级提示词，persona_mode=app_override 时生效。
  app_prompt?: string
  // 人设继承模式，缺省时后端使用组织默认规则。
  persona_mode?: 'org_inherited' | 'app_override'
  // 首次绑定的渠道类型，目前仅支持 wechat。
  channel_type?: 'wechat'
  // 指定 runtime 节点；为空时后端按调度策略选择。
  runtime_node_id?: string
}

// CreateMemberAppResult 是已有成员实例创建结果。
export interface CreateMemberAppResult {
  // 新创建的应用实例。
  app: App
  // 初始化 job ID。
  job_id: string
}

// useCreateMemberApp 为已有成员创建新的实例。
// 成功后刷新成员列表；应用列表会在用户进入实例页时重新拉取。
export function useCreateMemberApp(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ userId, payload }: { userId: string; payload: CreateMemberAppPayload }) => {
      if (!orgId.value) throw new Error('缺少组织 ID')
      const response = await apiRequest<{ member_app: CreateMemberAppResult }>(
        `/api/v1/organizations/${orgId.value}/members/${userId}/apps`,
        { method: 'POST', body: payload },
      )
      return response.member_app
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: memberListKey(orgId.value) })
    },
  })
}
```

- [ ] **Step 4: Implement members page UI**

Import the hook and result type:

```ts
  useCreateMemberApp, useCreateMember, useDeleteMember, useMembersQuery, useResetMemberPassword,
  useSetMemberStatus, type CreateMemberAppPayload, type CreateMemberAppResult, type MemberFormPayload,
```

Add state:

```ts
const createAppMutation = useCreateMemberApp(effectiveOrgId)
const createAppTarget = ref<Member | null>(null)
const createAppResult = ref<CreateMemberAppResult | null>(null)
const createAppError = ref('')
const createAppForm = ref<CreateMemberAppPayload>({
  app_name: '',
  persona_mode: 'org_inherited',
  channel_type: 'wechat',
})
```

Add action:

```ts
    { label: '创建新实例', type: 'primary', hidden: () => auth.user?.role !== 'platform_admin', onClick: r => openCreateAppForm(r) },
```

Add functions:

```ts
// openCreateAppForm 打开平台管理员为已有成员复建实例的表单。
function openCreateAppForm(member: Member) {
  createAppTarget.value = member
  createAppResult.value = null
  createAppError.value = ''
  createAppForm.value = { app_name: '', persona_mode: 'org_inherited', channel_type: 'wechat' }
}

// onSubmitCreateApp 提交已有成员实例创建请求，并展示后端返回的新实例与 job。
async function onSubmitCreateApp() {
  if (!createAppTarget.value) return
  createAppError.value = ''
  try {
    createAppResult.value = await createAppMutation.mutateAsync({
      userId: createAppTarget.value.id,
      payload: { ...createAppForm.value },
    })
    createAppTarget.value = null
  } catch (err) {
    createAppError.value = err instanceof Error ? err.message : '创建实例失败'
  }
}
```

Add template below create member card:

```vue
    <n-card v-if="createAppTarget" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <h2 style="margin: 0">创建新实例</h2>
          <n-button quaternary circle @click="createAppTarget = null">✕</n-button>
        </div>
      </template>
      <n-form label-placement="top" @submit.prevent="onSubmitCreateApp">
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item label="实例名 *">
              <n-input v-model:value="createAppForm.app_name" placeholder="实例名称" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="人设模式">
              <n-select v-model:value="createAppForm.persona_mode" :options="personaModeOptions" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="实例 prompt（可选）">
              <n-input v-model:value="createAppForm.app_prompt" type="textarea" :rows="3" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-space justify="end">
              <n-button @click="createAppTarget = null">取消</n-button>
              <n-button type="primary" attr-type="submit" :loading="createAppMutation.isPending.value">提交创建</n-button>
            </n-space>
            <p v-if="createAppError" class="state-text danger">{{ createAppError }}</p>
          </n-grid-item>
        </n-grid>
      </n-form>
    </n-card>

    <p v-if="createAppResult" class="state-text">
      已创建实例 {{ createAppResult.app.name }}，Job ID：{{ createAppResult.job_id }}
    </p>
```

Add `personaModeOptions`:

```ts
const personaModeOptions: SelectOption[] = [
  { label: '沿用组织人设', value: 'org_inherited' },
  { label: '实例覆盖', value: 'app_override' },
]
```

- [ ] **Step 5: Verify frontend tests pass**

Run:

```bash
rtk npm --prefix web test -- --run src/pages/org/MembersPage.spec.ts
```

Expected: PASS.

- [ ] **Step 6: Commit frontend change**

```bash
rtk git add web/src/api/hooks/useMembers.ts web/src/pages/org/MembersPage.vue web/src/pages/org/MembersPage.spec.ts
rtk git commit -m "feat(web): 增加平台成员实例复建入口" -m "在成员列表中为平台管理员提供创建新实例操作，提交已有成员实例创建接口并展示新实例和初始化任务结果。"
```

## Task 5: OpenAPI Generation And Final Verification

**Files:**
- Modify: `openapi/openapi.yaml`
- Modify: `web/src/api/generated.ts`

- [ ] **Step 1: Regenerate OpenAPI and frontend types**

Run:

```bash
rtk make openapi-gen
rtk make web-types-gen
```

Expected: both commands complete successfully and update generated files.

- [ ] **Step 2: Run backend focused tests**

Run:

```bash
rtk go test ./internal/auth ./internal/service ./internal/api/handlers -count=1
```

Expected: PASS.

- [ ] **Step 3: Run frontend focused tests and typecheck**

Run:

```bash
rtk npm --prefix web test -- --run src/pages/org/MembersPage.spec.ts
rtk npm --prefix web run typecheck
```

Expected: PASS.

- [ ] **Step 4: Check generated OpenAPI consistency**

Run:

```bash
rtk make openapi-check
```

Expected: PASS or no diff beyond the generated files already staged from `openapi-gen`.

- [ ] **Step 5: Inspect final diff for unrelated changes**

Run:

```bash
rtk git status --short
rtk git diff --stat
```

Expected: only files listed in this plan are modified.

- [ ] **Step 6: Commit generated files and verification**

```bash
rtk git add openapi/openapi.yaml web/src/api/generated.ts
rtk git commit -m "chore(openapi): 同步成员实例复建接口类型" -m "根据新增的已有成员创建实例接口重新生成 OpenAPI 契约和前端 TypeScript 类型。"
```

## Self-Review

- Spec coverage: permissions, service validation, transaction creation, route, frontend entry, OpenAPI generation, and tests are covered by Tasks 1-5.
- Placeholder scan: no unresolved markers or vague implementation-only steps remain.
- Type consistency: request body uses `app_name`, `app_prompt`, `persona_mode`, `channel_type`, and `runtime_node_id`; service input uses `AppName`, `AppPrompt`, `PersonaMode`, `ChannelType`, and `NodeID`; API response key is `member_app` with `CreateAppForMemberResult`.
