# User Change Password Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a logged-in user self-service password change flow that verifies the current password, writes a new manager password hash, clears the frontend session, and returns the user to login.

**Architecture:** The backend exposes `POST /api/v1/auth/password` under the existing authenticated auth routes. `AuthService` owns current-user password verification and update; the existing administrator reset endpoint under members remains unchanged. The frontend adds a compact password modal in `DashboardLayout.vue` and routes all API/session cleanup through `auth.changePassword`.

**Tech Stack:** Go, Gin, sqlc query interfaces, Argon2id password hashing, Vue 3, Pinia, Naive UI, Vitest, OpenAPI generation with swag and openapi-typescript.

---

## File Structure

- `internal/service/auth_service.go`
  - Add `ChangePasswordInput`.
  - Extend `AuthStore` with `UpdateUserPassword`.
  - Rename the existing password verifier field to `verifyPassword`.
  - Add an injectable `hashPassword` function for fast service tests.
  - Implement `ChangePassword`.

- `internal/service/auth_service_test.go`
  - Add service tests for success, wrong old password, invalid new password, disabled user, and disabled organization.
  - Extend `authStoreStub` with password update tracking.

- `internal/api/handlers/dto.go`
  - Add `ChangePasswordRequest`.
  - Register `OldPassword` and `NewPassword` in `jsonFieldNames`.

- `internal/api/handlers/auth.go`
  - Extend the handler-facing `AuthService` interface.
  - Register `POST /api/v1/auth/password` in the authenticated auth group.
  - Add handler method and Swagger annotations.
  - Map `ErrMemberCreateInvalid` to 400 for password validation failures.

- `internal/api/handlers/auth_test.go`
  - Add handler tests for success, missing body fields, and wrong current password.
  - Extend `authServiceStub` with `ChangePassword`.

- `web/src/stores/auth.ts`
  - Add `changePassword(oldPassword, newPassword)`.
  - On success, clear stored tokens and set `user` to null.

- `web/src/stores/auth.spec.ts`
  - Add tests for request shape and local session cleanup.

- `web/src/layouts/DashboardLayout.vue`
  - Add “修改密码” button in the sidebar footer.
  - Add modal form with current password, new password, confirmation, validation, and submit flow.

- `web/src/layouts/DashboardLayout.spec.ts`
  - Extend the auth mock with `changePassword`.
  - Add tests for opening the modal, validation, successful submit, and login redirect.

- Generated artifacts after implementation:
  - `openapi/openapi.yaml`
  - `web/src/api/generated.ts`

---

### Task 1: Auth Service Password Change

**Files:**
- Modify: `internal/service/auth_service.go`
- Modify: `internal/service/auth_service_test.go`

- [ ] **Step 1: Write service tests for password change**

Append these tests after `TestAuthServiceRefreshRejectsRotatedToken` in `internal/service/auth_service_test.go`:

```go
// TestAuthServiceChangePasswordUpdatesHash 验证已登录用户输入正确旧密码后能写入新密码 hash。
func TestAuthServiceChangePasswordUpdatesHash(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)
	svc.hashPassword = fakeAuthHash

	err := svc.ChangePassword(context.Background(),
		auth.Principal{UserID: authTestOrgMemberID, OrgID: authTestOrgID, Role: domain.UserRoleOrgMember},
		ChangePasswordInput{OldPassword: "correct-password", NewPassword: "new-password-123"},
	)

	require.NoError(t, err)
	require.Equal(t, 1, store.updatePasswordCalls)
	require.Equal(t, authTestOrgMemberID, store.lastPasswordUpdate.ID)
	require.Equal(t, "hashed:new-password-123", store.lastPasswordUpdate.PasswordHash)
	require.NotEqual(t, "new-password-123", store.usersByID[authTestOrgMemberID].PasswordHash)
}

// TestAuthServiceChangePasswordRejectsWrongOldPassword 验证旧密码错误时拒绝修改且不写库。
func TestAuthServiceChangePasswordRejectsWrongOldPassword(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)
	svc.hashPassword = fakeAuthHash

	err := svc.ChangePassword(context.Background(),
		auth.Principal{UserID: authTestOrgMemberID, OrgID: authTestOrgID, Role: domain.UserRoleOrgMember},
		ChangePasswordInput{OldPassword: "wrong-password", NewPassword: "new-password-123"},
	)

	require.ErrorIs(t, err, ErrInvalidCredentials)
	require.Equal(t, 0, store.updatePasswordCalls)
}

// TestAuthServiceChangePasswordRejectsInvalidNewPassword 覆盖新密码为空、过短、与旧密码相同的校验路径。
func TestAuthServiceChangePasswordRejectsInvalidNewPassword(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)
	svc.hashPassword = fakeAuthHash

	cases := []struct {
		name        string
		newPassword string
	}{
		{name: "空新密码", newPassword: ""},                  // 空新密码：不能生成 hash。
		{name: "不足八位", newPassword: "short"},           // 长度边界：少于 8 位直接拒绝。
		{name: "与旧密码相同", newPassword: "correct-password"}, // 防误操作：新旧密码不能完全一致。
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.ChangePassword(context.Background(),
				auth.Principal{UserID: authTestOrgMemberID, OrgID: authTestOrgID, Role: domain.UserRoleOrgMember},
				ChangePasswordInput{OldPassword: "correct-password", NewPassword: tc.newPassword},
			)

			require.ErrorIs(t, err, ErrMemberCreateInvalid)
		})
	}
	require.Equal(t, 0, store.updatePasswordCalls)
}

// TestAuthServiceChangePasswordRejectsDisabledUser 验证已停用用户不能继续修改密码。
func TestAuthServiceChangePasswordRejectsDisabledUser(t *testing.T) {
	store := newAuthStoreStub(t)
	user := store.usersByID[authTestOrgMemberID]
	user.Status = domain.StatusDisabled
	store.usersByID[authTestOrgMemberID] = user
	svc := newTestAuthService(t, store)

	err := svc.ChangePassword(context.Background(),
		auth.Principal{UserID: authTestOrgMemberID, OrgID: authTestOrgID, Role: domain.UserRoleOrgMember},
		ChangePasswordInput{OldPassword: "correct-password", NewPassword: "new-password-123"},
	)

	require.ErrorIs(t, err, ErrUserDisabled)
	require.Equal(t, 0, store.updatePasswordCalls)
}

// TestAuthServiceChangePasswordRejectsDisabledOrg 验证所属企业停用后企业用户不能修改密码。
func TestAuthServiceChangePasswordRejectsDisabledOrg(t *testing.T) {
	store := newAuthStoreStub(t)
	org := store.orgsByID[authTestOrgID]
	org.Status = domain.StatusDisabled
	store.orgsByID[authTestOrgID] = org
	store.orgsByCode[org.Code] = org
	svc := newTestAuthService(t, store)

	err := svc.ChangePassword(context.Background(),
		auth.Principal{UserID: authTestOrgMemberID, OrgID: authTestOrgID, Role: domain.UserRoleOrgMember},
		ChangePasswordInput{OldPassword: "correct-password", NewPassword: "new-password-123"},
	)

	require.ErrorIs(t, err, ErrOrgDisabled)
	require.Equal(t, 0, store.updatePasswordCalls)
}

// fakeAuthHash 用稳定前缀替代真实 Argon2id，避免改密单测受默认 hash 成本影响。
func fakeAuthHash(password string) (string, error) {
	return "hashed:" + password, nil
}
```

Update `authStoreStub` in the same file by adding these fields:

```go
	lastPasswordUpdate sqlc.UpdateUserPasswordParams
	updatePasswordCalls int
```

Add this method near the other `authStoreStub` methods:

```go
func (s *authStoreStub) UpdateUserPassword(_ context.Context, arg sqlc.UpdateUserPasswordParams) error {
	user, ok := s.usersByID[arg.ID]
	if !ok {
		return sql.ErrNoRows
	}
	s.lastPasswordUpdate = arg
	s.updatePasswordCalls++
	user.PasswordHash = arg.PasswordHash
	s.usersByID[arg.ID] = user
	if user.OrgID.Valid {
		s.orgUsersByKey[orgUserKey(user.OrgID.String, user.Username)] = user
	} else {
		s.platformByName[user.Username] = user
	}
	return nil
}
```

- [ ] **Step 2: Run service tests and verify they fail**

Run:

```bash
rtk go test ./internal/service -run 'TestAuthServiceChangePassword' -count=1
```

Expected: FAIL because `AuthService.ChangePassword`, `ChangePasswordInput`, and `AuthStore.UpdateUserPassword` are not implemented.

- [ ] **Step 3: Implement `AuthService.ChangePassword`**

In `internal/service/auth_service.go`, update the `AuthStore` interface:

```go
	UpdateUserPassword(ctx context.Context, arg sqlc.UpdateUserPasswordParams) error
```

Rename the existing `passwordHash` field to `verifyPassword`, add `hashPassword`, and update `NewAuthService`:

```go
	// verifyPassword 在测试中可替换，生产使用 auth.VerifyPassword 校验登录和改密旧密码。
	verifyPassword func(string, string) bool
	// hashPassword 在自助改密时生成新密码 hash；测试中可替换为快路径。
	hashPassword PasswordHasher
```

```go
		verifyPassword: auth.VerifyPassword,
		hashPassword: func(password string) (string, error) {
			return auth.HashPassword(password, auth.DefaultPasswordParams)
		},
```

Update `Login` to call the renamed field:

```go
	if !s.verifyPassword(input.Password, user.PasswordHash) {
		return LoginResult{}, ErrInvalidCredentials
	}
```

Add `ChangePasswordInput` near `LoginInput`:

```go
// ChangePasswordInput 是已登录用户自助修改密码的 service 入参。
// OldPassword 用于校验当前密码，NewPassword 通过 hash 后写入 users.password_hash。
type ChangePasswordInput struct {
	OldPassword string
	NewPassword string
}
```

Add the service method after `Me`:

```go
// ChangePassword 校验当前用户旧密码后写入新密码 hash。
// 该流程只允许用户修改自己的 manager 登录密码；忘记旧密码仍走管理员重置密码。
func (s *AuthService) ChangePassword(ctx context.Context, principal auth.Principal, input ChangePasswordInput) error {
	user, err := s.store.GetUser(ctx, principal.UserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidToken
		}
		return fmt.Errorf("查询当前用户失败: %w", err)
	}
	if err := s.ensureUserEnabled(ctx, user); err != nil {
		return err
	}
	if !s.verifyPassword(input.OldPassword, user.PasswordHash) {
		return ErrInvalidCredentials
	}
	if input.NewPassword == "" {
		return fmt.Errorf("%w: 新密码不能为空", ErrMemberCreateInvalid)
	}
	if len(input.NewPassword) < 8 {
		return fmt.Errorf("%w: 新密码至少 8 位", ErrMemberCreateInvalid)
	}
	if input.NewPassword == input.OldPassword {
		return fmt.Errorf("%w: 新密码不能与当前密码相同", ErrMemberCreateInvalid)
	}
	hashed, err := s.hashPassword(input.NewPassword)
	if err != nil {
		return fmt.Errorf("生成密码 hash 失败: %w", err)
	}
	if err := s.store.UpdateUserPassword(ctx, sqlc.UpdateUserPasswordParams{ID: user.ID, PasswordHash: hashed}); err != nil {
		return fmt.Errorf("更新当前用户密码失败: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run service tests and verify they pass**

Run:

```bash
rtk go test ./internal/service -run 'TestAuthService(ChangePassword|Login)' -count=1
```

Expected: PASS. This confirms the renamed verifier did not break login tests and the new self-change tests pass.

- [ ] **Step 5: Commit Task 1**

Run:

```bash
rtk git add internal/service/auth_service.go internal/service/auth_service_test.go
rtk git commit -m "feat(auth): 支持用户自助修改密码" -m "在 AuthService 增加 ChangePassword 流程。"
```

Expected: commit succeeds with only the auth service files staged.

---

### Task 2: Auth Handler Route and OpenAPI Contract

**Files:**
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/auth.go`
- Modify: `internal/api/handlers/auth_test.go`
- Generated later in this task: `openapi/openapi.yaml`, `web/src/api/generated.ts`

- [ ] **Step 1: Write handler tests**

In `internal/api/handlers/auth_test.go`, append these tests after `TestAuthMeReturnsCurrentUser`:

```go
// TestAuthChangePasswordReturnsNoContent 验证已认证改密接口成功时返回 204。
func TestAuthChangePasswordReturnsNoContent(t *testing.T) {
	svc := &authServiceStub{}
	router := newAuthTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", bytes.NewBufferString(`{"old_password":"old-pass","new_password":"new-pass-123"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: "org_member", OrgID: "org-1"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.Equal(t, "user-1", svc.lastPrincipal.UserID)
	require.Equal(t, "old-pass", svc.lastChangePasswordInput.OldPassword)
	require.Equal(t, "new-pass-123", svc.lastChangePasswordInput.NewPassword)
}

// TestAuthChangePasswordRejectsMissingFields 验证改密请求缺少必填字段时返回 400 和字段名。
func TestAuthChangePasswordRejectsMissingFields(t *testing.T) {
	router := newAuthTestRouter(t, &authServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: "org_member", OrgID: "org-1"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "old_password")
	require.Contains(t, recorder.Body.String(), "new_password")
}

// TestAuthChangePasswordMapsWrongPasswordToUnauthorized 验证旧密码错误时沿用认证失败响应。
func TestAuthChangePasswordMapsWrongPasswordToUnauthorized(t *testing.T) {
	router := newAuthTestRouter(t, &authServiceStub{changePasswordErr: service.ErrInvalidCredentials})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", bytes.NewBufferString(`{"old_password":"bad-pass","new_password":"new-pass-123"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: "org_member", OrgID: "org-1"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Contains(t, recorder.Body.String(), "用户名或密码错误")
}

// TestAuthChangePasswordMapsInvalidNewPasswordToBadRequest 验证新密码业务校验错误返回 400。
func TestAuthChangePasswordMapsInvalidNewPasswordToBadRequest(t *testing.T) {
	router := newAuthTestRouter(t, &authServiceStub{
		changePasswordErr: fmt.Errorf("%w: 新密码至少 8 位", service.ErrMemberCreateInvalid),
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", bytes.NewBufferString(`{"old_password":"old-pass","new_password":"short"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: "org_member", OrgID: "org-1"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "新密码至少 8 位")
}
```

Update the import block in `auth_test.go` to include `fmt`:

```go
	"fmt"
```

Extend `authServiceStub`:

```go
	changePasswordErr        error
	lastChangePasswordInput service.ChangePasswordInput
```

Add this method to `authServiceStub`:

```go
func (s *authServiceStub) ChangePassword(_ context.Context, principal auth.Principal, input service.ChangePasswordInput) error {
	s.lastPrincipal = principal
	s.lastChangePasswordInput = input
	return s.changePasswordErr
}
```

- [ ] **Step 2: Run handler tests and verify they fail**

Run:

```bash
rtk go test ./internal/api/handlers -run 'TestAuthChangePassword' -count=1
```

Expected: FAIL because `ChangePasswordRequest`, route registration, handler method, and handler interface method are missing.

- [ ] **Step 3: Add DTO and route handler**

In `internal/api/handlers/dto.go`, add these entries to `jsonFieldNames`:

```go
	"OldPassword":      "old_password",
	"NewPassword":      "new_password",
```

Add this DTO after `RefreshRequest`:

```go
// ChangePasswordRequest 是已登录用户修改自己密码的请求体。
type ChangePasswordRequest struct {
	// OldPassword 是当前登录密码，只用于本次校验，不写日志。
	OldPassword string `json:"old_password" binding:"required"`
	// NewPassword 是新登录密码，service 层会校验长度并写入 hash。
	NewPassword string `json:"new_password" binding:"required"`
}
```

In `internal/api/handlers/auth.go`, extend the `AuthService` interface:

```go
	ChangePassword(ctx context.Context, principal auth.Principal, input service.ChangePasswordInput) error
```

Update `RegisterAuthMeRoutes`:

```go
func RegisterAuthMeRoutes(router gin.IRouter, handler *AuthHandler) {
	group := router.Group("/api/v1/auth")
	group.GET("/me", handler.Me)
	group.POST("/password", handler.ChangePassword)
}
```

Add this handler method after `Me`:

```go
// ChangePassword 修改当前登录用户自己的密码。
//
// @Summary      修改当前用户密码
// @Description  已登录用户输入当前密码后修改自己的 manager 登录密码
// @Tags         auth
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  ChangePasswordRequest  true  "修改密码请求"
// @Success      204   "密码修改成功，无响应体"
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      403   {object}  ErrorResponse
// @Failure      500   {object}  ErrorResponse
// @Router       /auth/password [post]
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	principal := principalFromCtx(c)
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	if err := h.service.ChangePassword(c.Request.Context(), principal, service.ChangePasswordInput{
		OldPassword: req.OldPassword,
		NewPassword: req.NewPassword,
	}); err != nil {
		writeAuthError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
```

In `writeAuthError`, add the validation mapping before the default branch:

```go
	case errors.Is(err, service.ErrMemberCreateInvalid):
		c.JSON(http.StatusBadRequest, apierror.New("MEMBER_INVALID", validationServiceMessage(err, service.ErrMemberCreateInvalid)))
```

- [ ] **Step 4: Run handler tests and verify they pass**

Run:

```bash
rtk go test ./internal/api/handlers -run 'TestAuth(ChangePassword|Login|Me)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Regenerate OpenAPI and frontend types**

Run:

```bash
rtk make openapi-gen
rtk make web-types-gen
```

Expected:
- `openapi/openapi.yaml` includes `POST /auth/password`.
- `web/src/api/generated.ts` includes `handlers.ChangePasswordRequest`.

- [ ] **Step 6: Run OpenAPI check**

Run:

```bash
rtk make openapi-check
```

Expected: PASS with no message indicating OpenAPI is out of sync.

- [ ] **Step 7: Commit Task 2**

Run:

```bash
rtk git add internal/api/handlers/dto.go internal/api/handlers/auth.go internal/api/handlers/auth_test.go openapi/openapi.yaml web/src/api/generated.ts
rtk git commit -m "feat(auth): 增加当前用户修改密码接口" -m "新增 /api/v1/auth/password 已认证接口。"
```

Expected: commit succeeds with handler and generated contract files staged.

---

### Task 3: Auth Store Session Cleanup API

**Files:**
- Modify: `web/src/stores/auth.ts`
- Modify: `web/src/stores/auth.spec.ts`

- [ ] **Step 1: Write auth store tests**

Append these tests inside `describe('auth store', ...)` in `web/src/stores/auth.spec.ts`:

```ts
  it('修改密码提交当前密码和新密码', async () => {
    const auth = useAuthStore()

    await auth.changePassword('old-password', 'new-password-123')

    expect(clientMocks.apiRequest).toHaveBeenCalledWith('/api/v1/auth/password', {
      method: 'POST',
      body: { old_password: 'old-password', new_password: 'new-password-123' },
    })
  })

  it('修改密码成功后清理本地会话', async () => {
    const auth = useAuthStore()
    auth.user = {
      id: 'user-1',
      username: 'admin',
      display_name: '管理员',
      role: 'platform_admin',
      status: 'active',
    }

    await auth.changePassword('old-password', 'new-password-123')

    expect(clientMocks.clearStoredTokens).toHaveBeenCalledTimes(1)
    expect(auth.user).toBeNull()
  })
```

- [ ] **Step 2: Run auth store tests and verify they fail**

Run:

```bash
rtk bash -lc 'cd web && npm test -- --run src/stores/auth.spec.ts'
```

Expected: FAIL because `auth.changePassword` is not defined.

- [ ] **Step 3: Implement `changePassword` in auth store**

In `web/src/stores/auth.ts`, add this function after `fetchCurrentUser`:

```ts
  // changePassword 修改当前登录用户自己的密码；成功后立即清理本地会话，要求用户用新密码重新登录。
  async function changePassword(oldPassword: string, newPassword: string): Promise<void> {
    await apiRequest<void>('/api/v1/auth/password', {
      method: 'POST',
      body: { old_password: oldPassword, new_password: newPassword },
    })
    clearStoredTokens()
    user.value = null
  }
```

Add `changePassword` to the returned store object:

```ts
    changePassword,
```

- [ ] **Step 4: Run auth store tests and verify they pass**

Run:

```bash
rtk bash -lc 'cd web && npm test -- --run src/stores/auth.spec.ts'
```

Expected: PASS.

- [ ] **Step 5: Commit Task 3**

Run:

```bash
rtk git add web/src/stores/auth.ts web/src/stores/auth.spec.ts
rtk git commit -m "feat(auth): 前端支持提交自助改密" -m "在 auth store 增加 changePassword 并在成功后清理本地会话。"
```

Expected: commit succeeds with only auth store files staged.

---

### Task 4: Dashboard Password Modal

**Files:**
- Modify: `web/src/layouts/DashboardLayout.vue`
- Modify: `web/src/layouts/DashboardLayout.spec.ts`

- [ ] **Step 1: Write DashboardLayout modal tests**

In `web/src/layouts/DashboardLayout.spec.ts`, update the auth mock section:

```ts
const changePassword = vi.hoisted(() => vi.fn())
const authState = vi.hoisted(() => ({
  user: { id: 'admin-1', username: 'admin', display_name: 'admin', role: 'platform_admin', org_id: 'org-1' },
  isPlatformAdmin: true,
  isOrgAdmin: false,
  isOrgMember: false,
  logout,
  changePassword,
}))
```

Add `changePassword.mockClear()` in `beforeEach`:

```ts
    changePassword.mockClear()
    changePassword.mockResolvedValue(undefined)
```

Add these stubs inside `mountLayout()` under `global.stubs`:

```ts
        NModal: {
          props: ['show'],
          emits: ['update:show'],
          template: '<section v-if="show" data-test="password-modal"><slot /></section>',
        },
        NForm: { template: '<form @submit.prevent="$emit(`submit`, $event)"><slot /></form>' },
        NFormItem: { props: ['label'], template: '<label><span>{{ label }}</span><slot /></label>' },
        NInput: {
          props: ['value', 'placeholder', 'type'],
          emits: ['update:value'],
          template: '<input :type="type || `text`" :placeholder="placeholder" :value="value" @input="$emit(`update:value`, ($event.target as HTMLInputElement).value)" />',
        },
        NSpace: { template: '<div><slot /></div>' },
        NAlert: { template: '<p data-test="password-error"><slot /></p>' },
```

Append these tests inside the `describe('DashboardLayout', ...)` block:

```ts
  // 覆盖修改密码入口：侧边栏用户区应能打开自助改密弹窗。
  it('opens the password modal from the sidebar footer', async () => {
    const wrapper = mountLayout()
    const button = wrapper.findAll('button').find(item => item.text().trim() === '修改密码')

    expect(button).toBeTruthy()

    await button!.trigger('click')

    expect(wrapper.find('[data-test="password-modal"]').exists()).toBe(true)
  })

  // 覆盖前端校验：确认密码不一致时不调用后端。
  it('rejects mismatched confirmation before submitting password change', async () => {
    const wrapper = mountLayout()
    const button = wrapper.findAll('button').find(item => item.text().trim() === '修改密码')
    await button!.trigger('click')

    const inputs = wrapper.findAll('[data-test="password-modal"] input')
    await inputs[0].setValue('old-password')
    await inputs[1].setValue('new-password-123')
    await inputs[2].setValue('different-password')
    await wrapper.find('[data-test="password-modal"] form').trigger('submit')

    expect(changePassword).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="password-error"]').text()).toContain('两次输入的新密码不一致')
  })

  // 覆盖成功路径：提交改密后调用 auth store，并跳回登录页。
  it('submits password change and redirects to login on success', async () => {
    const wrapper = mountLayout()
    const button = wrapper.findAll('button').find(item => item.text().trim() === '修改密码')
    await button!.trigger('click')

    const inputs = wrapper.findAll('[data-test="password-modal"] input')
    await inputs[0].setValue('old-password')
    await inputs[1].setValue('new-password-123')
    await inputs[2].setValue('new-password-123')
    await wrapper.find('[data-test="password-modal"] form').trigger('submit')

    expect(changePassword).toHaveBeenCalledWith('old-password', 'new-password-123')
    expect(routerReplace).toHaveBeenCalledWith('/login')
  })
```

- [ ] **Step 2: Run DashboardLayout tests and verify they fail**

Run:

```bash
rtk bash -lc 'cd web && npm test -- --run src/layouts/DashboardLayout.spec.ts'
```

Expected: FAIL because the “修改密码” button and modal state do not exist.

- [ ] **Step 3: Add password modal UI and logic**

In `web/src/layouts/DashboardLayout.vue`, add this button before the existing logout button:

```vue
        <n-button
          v-if="auth.user"
          size="small"
          quaternary
          class="logout-button"
          style="width: 100%; justify-content: flex-start"
          @click="openPasswordModal"
        >
          <template #icon><KeyRound :size="15" /></template>
          修改密码
        </n-button>
```

Add this modal before `HelpDrawer`:

```vue
    <!-- 修改密码弹窗：只处理当前登录用户的自助改密，管理员重置成员密码仍在成员页。 -->
    <n-modal v-model:show="passwordModalOpen" preset="card" title="修改密码" style="width: 420px; max-width: calc(100vw - 32px)">
      <n-form label-placement="top" @submit.prevent="onChangePassword">
        <n-form-item label="当前密码">
          <n-input v-model:value="passwordForm.oldPassword" type="password" placeholder="输入当前密码" />
        </n-form-item>
        <n-form-item label="新密码">
          <n-input v-model:value="passwordForm.newPassword" type="password" placeholder="至少 8 位" />
        </n-form-item>
        <n-form-item label="确认新密码">
          <n-input v-model:value="passwordForm.confirmPassword" type="password" placeholder="再次输入新密码" />
        </n-form-item>
        <n-alert v-if="passwordError" type="error" :bordered="false" style="margin-bottom: 12px">
          {{ passwordError }}
        </n-alert>
        <n-space justify="end">
          <n-button :disabled="passwordChanging" @click="closePasswordModal">取消</n-button>
          <n-button type="primary" attr-type="submit" :loading="passwordChanging">确认修改</n-button>
        </n-space>
      </n-form>
    </n-modal>
```

Update the Naive UI imports:

```ts
  NAlert, NButton, NForm, NFormItem, NInput, NLayout, NLayoutContent, NLayoutHeader, NLayoutSider,
  NMenu, NModal, NSpace, NTag, type MenuOption,
```

Update the lucide imports:

```ts
  ShieldCheck, Users, Wallet, KeyRound,
```

Add modal state after `helpOpen`:

```ts
// passwordModalOpen 控制当前用户自助改密弹窗；密码字段只保存在内存中，关闭时立即清空。
const passwordModalOpen = ref(false)
const passwordChanging = ref(false)
const passwordError = ref('')
const passwordForm = ref({
  oldPassword: '',
  newPassword: '',
  confirmPassword: '',
})
```

Add these functions near `onLogout`:

```ts
// openPasswordModal 每次打开都重置表单，避免残留上次输入的密码。
function openPasswordModal() {
  passwordForm.value = { oldPassword: '', newPassword: '', confirmPassword: '' }
  passwordError.value = ''
  passwordModalOpen.value = true
}

// closePasswordModal 在提交中禁用关闭，防止请求状态和弹窗状态错位。
function closePasswordModal() {
  if (passwordChanging.value) return
  passwordModalOpen.value = false
  passwordForm.value = { oldPassword: '', newPassword: '', confirmPassword: '' }
  passwordError.value = ''
}

// validatePasswordForm 做前端基础校验；后端仍负责最终旧密码和账号状态校验。
function validatePasswordForm(): string | null {
  const form = passwordForm.value
  if (!form.oldPassword || !form.newPassword || !form.confirmPassword) return '请填写当前密码、新密码和确认新密码'
  if (form.newPassword.length < 8) return '新密码至少 8 位'
  if (form.newPassword === form.oldPassword) return '新密码不能与当前密码相同'
  if (form.newPassword !== form.confirmPassword) return '两次输入的新密码不一致'
  return null
}

// onChangePassword 提交当前用户改密；成功后 auth store 会清理 token，这里负责跳回登录页。
async function onChangePassword() {
  const validation = validatePasswordForm()
  if (validation) {
    passwordError.value = validation
    return
  }
  passwordChanging.value = true
  passwordError.value = ''
  try {
    await auth.changePassword(passwordForm.value.oldPassword, passwordForm.value.newPassword)
    passwordModalOpen.value = false
    passwordForm.value = { oldPassword: '', newPassword: '', confirmPassword: '' }
    await router.replace('/login')
  } catch (err) {
    passwordError.value = err instanceof Error ? err.message : '修改密码失败'
  } finally {
    passwordChanging.value = false
  }
}
```

- [ ] **Step 4: Run DashboardLayout tests and verify they pass**

Run:

```bash
rtk bash -lc 'cd web && npm test -- --run src/layouts/DashboardLayout.spec.ts'
```

Expected: PASS.

- [ ] **Step 5: Run focused frontend tests**

Run:

```bash
rtk bash -lc 'cd web && npm test -- --run src/stores/auth.spec.ts src/layouts/DashboardLayout.spec.ts'
```

Expected: PASS.

- [ ] **Step 6: Commit Task 4**

Run:

```bash
rtk git add web/src/layouts/DashboardLayout.vue web/src/layouts/DashboardLayout.spec.ts
rtk git commit -m "feat(auth): 增加前端修改密码弹窗" -m "在后台侧边栏用户区增加自助改密入口。"
```

Expected: commit succeeds with only DashboardLayout files staged.

---

### Task 5: Full Verification and Browser Check

**Files:**
- Verify tracked changes across backend, frontend, and generated artifacts.
- No new source file is created in this task unless a verification step exposes a bug.

- [ ] **Step 1: Run backend tests**

Run:

```bash
rtk go test ./internal/service ./internal/api/handlers ./internal/auth
```

Expected: PASS.

- [ ] **Step 2: Run frontend tests and typecheck**

Run:

```bash
rtk bash -lc 'cd web && npm test -- --run src/stores/auth.spec.ts src/layouts/DashboardLayout.spec.ts'
rtk make web-typecheck
```

Expected: PASS.

- [ ] **Step 3: Run OpenAPI sync check**

Run:

```bash
rtk make openapi-check
```

Expected: PASS with no OpenAPI drift.

- [ ] **Step 4: Run full project checks when time allows**

Run:

```bash
rtk make test
rtk make web-test
```

Expected: PASS. If `make web-test` spends time installing dependencies, let it complete before moving to browser verification.

- [ ] **Step 5: Start or refresh local app for browser verification**

If the local k3d stack is already running, rebuild the manager images:

```bash
rtk make local-build
```

Expected: manager API and web deployments roll out successfully.

If the local stack is not running, start it:

```bash
rtk make local-up
```

Expected: the command prints `manager 控制台 http://ocm.localhost`.

- [ ] **Step 6: Verify the feature in a real browser**

Use Chromium via the available browser tool and perform this flow:

1. Open `http://ocm.localhost/login`.
2. Log in with organization identifier blank, username `admin`, password `admin123`.
3. Click `修改密码` in the sidebar footer.
4. Submit current password `admin123`, new password `admin12345`, confirmation `admin12345`.
5. Confirm the app navigates to `/login`.
6. Log in with username `admin`, password `admin12345`, organization identifier blank.
7. Confirm the dashboard loads.
8. Log out.
9. Try username `admin`, old password `admin123`, organization identifier blank.
10. Confirm login fails.

Expected: new password works, old password fails, no console errors appear during the flow.

- [ ] **Step 7: Restore local admin password for developer convenience**

Use the UI flow again while logged in as `admin/admin12345`:

1. Click `修改密码`.
2. Submit current password `admin12345`, new password `admin123`, confirmation `admin123`.
3. Confirm redirect to login.
4. Log in with `admin/admin123`.

Expected: the local debug credential documented in `AGENTS.md` works again.

- [ ] **Step 8: Confirm working tree scope**

Run:

```bash
rtk git status --short
```

Expected: either a clean working tree or only files directly related to this feature. No unrelated files are present.

- [ ] **Step 9: Commit final verification fixes if needed**

If verification required code changes, commit only those changed feature files:

```bash
rtk git add internal/service/auth_service.go internal/service/auth_service_test.go internal/api/handlers/dto.go internal/api/handlers/auth.go internal/api/handlers/auth_test.go web/src/stores/auth.ts web/src/stores/auth.spec.ts web/src/layouts/DashboardLayout.vue web/src/layouts/DashboardLayout.spec.ts openapi/openapi.yaml web/src/api/generated.ts
rtk git commit -m "fix(auth): 修正修改密码验证问题" -m "根据测试和浏览器验证结果修正自助改密流程。"
```

Expected: commit succeeds. If verification found no bugs, skip this commit step and record the passing commands in the final delivery note.

---

## Plan Self-Review

- Spec coverage: Tasks 1 and 2 cover `POST /api/v1/auth/password`, old password verification, new password validation, active user/org checks, and OpenAPI sync. Tasks 3 and 4 cover the frontend store, modal entry, validation, token cleanup, and login redirect. Task 5 covers required real-browser validation and restores the documented local password.
- Scope control: The plan does not change administrator member reset, does not add password recovery, does not add an account settings page, and does not modify new-api passwords.
- Type consistency: Backend request fields use `old_password` and `new_password`; service uses `ChangePasswordInput{OldPassword, NewPassword}`; frontend store takes `changePassword(oldPassword, newPassword)` and sends the same JSON field names.
