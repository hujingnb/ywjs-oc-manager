# 用量页面按 token_name 过滤修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让用量页面"实例 / 成员 / 组织" tab 切换时展示该对象自身真实用量；根因是 new-api admin 接口 `token_id` 与 `/api/data/users?id=` 过滤静默失效，方案是改用 `token_name` 与客户端按 `username` 过滤，并在 manager 数据库显式落库这两个 new-api 侧标识。

**Architecture:** 落库 `organizations.newapi_username` 与 `apps.newapi_key_name` 两列；创建链路同步写入；usage service 读字段后传给 newapi client；newapi client `LogsQuery` 用 `TokenName` 取代 `TokenID`，`GetUserQuotaDates` 增 `username` 参数做客户端过滤；前端 `UsagePage` 用 `effective` computed 把选中 ID 约束在当前列表内消除跨组织残留。

**Tech Stack:** Go 1.22+, pgx/v5, sqlc, golang-migrate, gin, testify, Vue 3 + naive-ui + @tanstack/vue-query + vitest。

**Spec:** `docs/superpowers/specs/2026-05-19-usage-page-filter-by-token-name-design.md`

---

## Task 1：数据库 schema 加两列 + sqlc 重生成

**Files:**
- Create: `internal/migrations/000021_organizations_apps_newapi_names.up.sql`
- Create: `internal/migrations/000021_organizations_apps_newapi_names.down.sql`
- Modify: `internal/store/queries/organizations.sql:24-31`（SetOrganizationNewAPIUser）
- Modify: `internal/store/queries/apps.sql`（SetAppNewAPIKey 段）
- Regenerate: `internal/store/sqlc/organizations.sql.go`、`internal/store/sqlc/apps.sql.go`、`internal/store/sqlc/models.go`、`internal/store/sqlc/querier.go`
- Test: `internal/migrations/migrations_test.go`（已有，自动覆盖 up/down）

- [ ] **Step 1：写 up migration**

```sql
-- internal/migrations/000021_organizations_apps_newapi_names.up.sql

-- organizations.newapi_username 存组织在 new-api 一侧的 user 名。
-- 当前实现里它与 organizations.code 同值，但语义不同：
-- code 是 manager 内部组织标识，username 是远端 new-api 的 user.username。
-- 拆开存避免未来 new-api 命名规则变化时隐式破坏过滤。
ALTER TABLE organizations ADD COLUMN newapi_username text NULL;
COMMENT ON COLUMN organizations.newapi_username IS 'new-api 侧的 user.username，用于按 username 过滤用量响应';

-- 回填：已创建 new-api user 的组织，username 等于 code（实测一致）。
UPDATE organizations
SET newapi_username = code
WHERE newapi_user_id IS NOT NULL AND newapi_user_id <> '';

-- apps.newapi_key_name 存实例在 new-api 一侧的 token name。
-- 当前实现 = "app-" + app.id，但同样分开存。
ALTER TABLE apps ADD COLUMN newapi_key_name text NULL;
COMMENT ON COLUMN apps.newapi_key_name IS 'new-api 侧的 token.name，用于按 token_name 过滤用量日志';

-- 回填：已绑定 new-api token 的实例。
UPDATE apps
SET newapi_key_name = 'app-' || id::text
WHERE newapi_key_id IS NOT NULL AND newapi_key_id <> '';
```

- [ ] **Step 2：写 down migration**

```sql
-- internal/migrations/000021_organizations_apps_newapi_names.down.sql

ALTER TABLE apps DROP COLUMN newapi_key_name;
ALTER TABLE organizations DROP COLUMN newapi_username;
```

- [ ] **Step 3：跑迁移并核对回填**

Run:
```bash
make migrate-up
PGPASSWORD=ocm psql -h localhost -p 15432 -U ocm -d ocm -At -c "SELECT name, newapi_username FROM organizations WHERE newapi_user_id IS NOT NULL ORDER BY created_at;"
PGPASSWORD=ocm psql -h localhost -p 15432 -U ocm -d ocm -At -c "SELECT name, newapi_key_name FROM apps WHERE newapi_key_id IS NOT NULL ORDER BY created_at LIMIT 5;"
```

Expected：organizations.newapi_username 与 organizations.code 同值；apps.newapi_key_name 为 `app-<uuid>`。

- [ ] **Step 4：改 queries.sql（organizations）**

Modify `internal/store/queries/organizations.sql`，定位到 `-- name: SetOrganizationNewAPIUser :one` 块，整段替换为：

```sql
-- name: SetOrganizationNewAPIUser :one
UPDATE organizations
SET
    newapi_user_id = $2,
    newapi_user_credentials_ciphertext = $3,
    newapi_username = $4,
    updated_at = now()
WHERE id = $1
RETURNING *;
```

- [ ] **Step 5：改 queries.sql（apps）**

Modify `internal/store/queries/apps.sql`，定位到 `-- name: SetAppNewAPIKey :one` 块，整段替换为：

```sql
-- name: SetAppNewAPIKey :one
UPDATE apps
SET
    newapi_key_id = $2,
    newapi_key_ciphertext = $3,
    api_key_status = $4,
    newapi_key_name = $5,
    updated_at = now()
WHERE id = $1
RETURNING *;
```

- [ ] **Step 6：跑 sqlc 重生成**

Run: `make sqlc-generate`
Expected: 退出码 0，`git status` 看到 `internal/store/sqlc/organizations.sql.go`、`apps.sql.go`、`models.go`、`querier.go` 被改。

确认 `SetOrganizationNewAPIUserParams` 多出 `NewapiUsername pgtype.Text`、`SetAppNewAPIKeyParams` 多出 `NewapiKeyName pgtype.Text`、`sqlc.Organization` 与 `sqlc.App` 结构体新增同名字段。

- [ ] **Step 7：跑迁移单测**

Run: `go test ./internal/migrations/... -count=1 -run TestMigrations`
Expected: PASS（自动覆盖 up/down 顺序）。

- [ ] **Step 8：编译全仓库**

Run: `go build ./...`
Expected: 编译会失败，提示 `internal/service/organization_service.go` 和 `internal/worker/handlers/app_initialize.go` 里调 `SetOrganizationNewAPIUser`/`SetAppNewAPIKey` 没传新增字段——这是预期的，后续 Task 修。

- [ ] **Step 9：提交（不修编译错误，先把 schema + sqlc 落盘）**

```bash
git add internal/migrations/000021_organizations_apps_newapi_names.up.sql \
        internal/migrations/000021_organizations_apps_newapi_names.down.sql \
        internal/store/queries/organizations.sql \
        internal/store/queries/apps.sql \
        internal/store/sqlc/
git commit -m "feat(db): organizations / apps 增加 newapi_username / newapi_key_name 列

新增 migration 000021：在 manager 数据库显式落 new-api 侧的 username
（组织）与 token name（实例）。SetOrganizationNewAPIUser / SetAppNewAPIKey
增加对应入参，sqlc 重生成；编译期错误由后续 commit 修复。

历史数据按既有约定回填：organizations.newapi_username = code，
apps.newapi_key_name = 'app-' || id。"
```

---

## Task 2：organization_service 在创建链路写入 newapi_username

**Files:**
- Modify: `internal/service/organization_service.go:378-385`
- Test: `internal/service/organization_service_test.go`（新增用例）

- [ ] **Step 1：找到现有 organization_service_test.go 里 provisionNewAPIUser 的测试模式**

Run: `grep -n "provisionNewAPIUser\|SetOrganizationNewAPIUserParams\|fakeOrgStore\|type.*Store struct" internal/service/organization_service_test.go | head -20`
Expected：能定位到 fake store 名字与一两个已有用例，照抄断言风格。

- [ ] **Step 2：写失败用例（断言 NewapiUsername 落库）**

Modify `internal/service/organization_service_test.go`，在文件末尾追加：

```go
// TestProvisionNewAPIUserPersistsUsername 校验组织创建链路把 new-api 侧 username
// （即 org.Code）显式落到 organizations.newapi_username 字段，供 usage 查询直接读。
// 防止"username 与 code 同值"这一隐式约定回归。
func TestProvisionNewAPIUserPersistsUsername(t *testing.T) {
    store := newFakeOrgStore()
    provisioner := &fakeProvisioner{user: newapi.User{ID: 42, Username: "demo-org"}}
    cipher := mustCipher(t)
    svc := NewOrganizationService(store, provisioner, cipher, nil, nil)

    org, err := svc.CreateOrganization(context.Background(), platformAdmin(), CreateOrganizationInput{
        Name: "Demo Org",
        Code: "demo-org",
    })
    require.NoError(t, err)
    require.NotEmpty(t, store.lastSetNewAPIUserParams.NewapiUsername.String)
    assert.Equal(t, "demo-org", store.lastSetNewAPIUserParams.NewapiUsername.String)
    assert.True(t, store.lastSetNewAPIUserParams.NewapiUsername.Valid)
    assert.Equal(t, "demo-org", org.NewapiUsername.String)  // 返回的 org 也带上
}
```

> **注意**：上面的 helper 名（`newFakeOrgStore`、`fakeProvisioner`、`mustCipher`、`platformAdmin`、`CreateOrganizationInput` 字段）以现存测试文件里的具体名字为准，Step 1 里能看到。如果 fake store 没有 `lastSetNewAPIUserParams` 字段，先在 fake 上加一个，并在 fake 的 `SetOrganizationNewAPIUser` 方法里赋值。

- [ ] **Step 3：跑测试看它 fail**

Run: `go test ./internal/service/ -run TestProvisionNewAPIUserPersistsUsername -count=1 -v`
Expected: FAIL，Username 为空（因为 service 还没传）。

- [ ] **Step 4：改 service 写入 NewapiUsername**

Modify `internal/service/organization_service.go`：定位到 `provisionNewAPIUser` 函数里 `SetOrganizationNewAPIUser` 那一段，把：

```go
updated, err := s.store.SetOrganizationNewAPIUser(ctx, sqlc.SetOrganizationNewAPIUserParams{
    ID:                              org.ID,
    NewapiUserID:                    pgtype.Text{String: strconv.FormatInt(user.ID, 10), Valid: true},
    NewapiUserCredentialsCiphertext: pgtype.Text{String: ciphertext, Valid: true},
})
```

改为：

```go
updated, err := s.store.SetOrganizationNewAPIUser(ctx, sqlc.SetOrganizationNewAPIUserParams{
    ID:                              org.ID,
    NewapiUserID:                    pgtype.Text{String: strconv.FormatInt(user.ID, 10), Valid: true},
    NewapiUserCredentialsCiphertext: pgtype.Text{String: ciphertext, Valid: true},
    NewapiUsername:                  pgtype.Text{String: username, Valid: true},
})
```

`username` 局部变量在函数顶部已声明（`username := org.Code`）。

- [ ] **Step 5：跑新用例 + 已有用例**

Run: `go test ./internal/service/ -run TestProvisionNewAPIUserPersistsUsername -count=1 -v && go test ./internal/service/ -run TestOrganization -count=1 -v`
Expected: 新用例 PASS，已有用例继续 PASS。

- [ ] **Step 6：commit**

```bash
git add internal/service/organization_service.go internal/service/organization_service_test.go
git commit -m "feat(org): provisionNewAPIUser 同步落库 newapi_username

CreateOrganization 调 new-api 创建 user 后，把 username（当前等于
org.Code）一并写入 organizations.newapi_username，供 usage service
直接读取，避免下游再走"凭据密文解密"或"运行时查 new-api"的弯路。"
```

---

## Task 3：app_initialize 在创建链路写入 newapi_key_name

**Files:**
- Modify: `internal/worker/handlers/app_initialize.go:805-846`
- Test: `internal/worker/handlers/app_initialize_test.go`（新增用例）

- [ ] **Step 1：摸清 init 现有测试的 fake store 风格**

Run: `grep -n "SetAppNewAPIKey\|fakeAppStore\|fakeInitStore" internal/worker/handlers/app_initialize_test.go | head -10`
Expected：定位 fake store 类型名 + `lastSetAPIKeyParams` 之类字段（无则后续步骤里在 fake 上加一个）。

- [ ] **Step 2：写失败用例**

Modify `internal/worker/handlers/app_initialize_test.go`，在末尾追加：

```go
// TestProvisionAPIKeyPersistsKeyName 校验实例初始化链路把 new-api 侧 token name
// （当前实现 = "app-" + app.ID）显式落到 apps.newapi_key_name，供 usage 查询直接读。
func TestProvisionAPIKeyPersistsKeyName(t *testing.T) {
    appID := uuid.MustParse("0193ce63-4b8e-7000-a000-000000000001")
    store := newFakeInitStore()
    factory := newFakeNewAPIFactory(newapi.APIKey{ID: 77})
    h := newTestInitHandler(t, store, factory)

    _, err := h.provisionAPIKey(context.Background(), &sqlc.App{
        ID: pgtype.UUID{Bytes: appID, Valid: true},
        // 其他必要字段按现存 fake 的 happy path 填
    })
    require.NoError(t, err)

    expectedName := "app-" + appID.String()
    assert.Equal(t, expectedName, store.lastSetAPIKeyParams.NewapiKeyName.String)
    assert.True(t, store.lastSetAPIKeyParams.NewapiKeyName.Valid)
    // CreateAPIKey 的 Name 也应当与落库一致
    assert.Equal(t, expectedName, factory.lastCreateAPIKeyInput.Name)
}
```

> **注意**：`newFakeInitStore` / `newFakeNewAPIFactory` / `newTestInitHandler` 这些 helper 名字以现存文件为准。如果当前 fake 没有 `lastSetAPIKeyParams` 与 `lastCreateAPIKeyInput`，先在 fake 上补这两个字段并在对应方法里赋值。

- [ ] **Step 3：跑测试看 fail**

Run: `go test ./internal/worker/handlers/ -run TestProvisionAPIKeyPersistsKeyName -count=1 -v`
Expected: FAIL，`NewapiKeyName.String` 为空。

- [ ] **Step 4：改实现，把 keyName 抽变量并写入**

Modify `internal/worker/handlers/app_initialize.go`，定位到 line ~805 创建 token 那段：

```go
key, err := client.CreateAPIKey(ctx, newapi.CreateAPIKeyInput{
    Name:       fmt.Sprintf("app-%s", uuidToString(app.ID)),
    Models:     []string{},
    UnlimitedQ: true,
})
```

改为：

```go
keyName := fmt.Sprintf("app-%s", uuidToString(app.ID))
key, err := client.CreateAPIKey(ctx, newapi.CreateAPIKeyInput{
    Name:       keyName,
    Models:     []string{},
    UnlimitedQ: true,
})
```

再定位到 line ~840 的 `SetAppNewAPIKey` 调用：

```go
updated, err := h.store.SetAppNewAPIKey(ctx, sqlc.SetAppNewAPIKeyParams{
    ID:                  app.ID,
    NewapiKeyID:         pgtype.Text{String: fmt.Sprintf("%d", key.ID), Valid: true},
    NewapiKeyCiphertext: pgtype.Text{String: ciphertext, Valid: true},
    ApiKeyStatus:        domain.APIKeyStatusActive,
})
```

改为：

```go
updated, err := h.store.SetAppNewAPIKey(ctx, sqlc.SetAppNewAPIKeyParams{
    ID:                  app.ID,
    NewapiKeyID:         pgtype.Text{String: fmt.Sprintf("%d", key.ID), Valid: true},
    NewapiKeyCiphertext: pgtype.Text{String: ciphertext, Valid: true},
    ApiKeyStatus:        domain.APIKeyStatusActive,
    NewapiKeyName:       pgtype.Text{String: keyName, Valid: true},
})
```

- [ ] **Step 5：跑测试**

Run: `go test ./internal/worker/handlers/ -run TestProvisionAPIKeyPersistsKeyName -count=1 -v && go test ./internal/worker/handlers/ -count=1`
Expected: 全 PASS。

- [ ] **Step 6：commit**

```bash
git add internal/worker/handlers/app_initialize.go internal/worker/handlers/app_initialize_test.go
git commit -m "feat(app-init): provisionAPIKey 同步落库 newapi_key_name

把 CreateAPIKey 的 Name 抽局部变量后同时写入 apps.newapi_key_name，
保证 manager 数据库里记录的 token name 与 new-api 实际创建的一致，
让 usage 查询直接读字段而非再次拼 'app-<uuid>'。"
```

---

## Task 4：newapi LogsQuery 切到 TokenName，GetTokenLogs 不再发 token_id

**Files:**
- Modify: `internal/integrations/newapi/client.go:147-156`（LogsQuery struct）
- Modify: `internal/integrations/newapi/client.go:826-828`（GetTokenLogs token_id 透传）
- Test: `internal/integrations/newapi/client_test.go`（新增用例）

- [ ] **Step 1：写失败用例**

Modify `internal/integrations/newapi/client_test.go`，文件末尾追加：

```go
// TestGetTokenLogsSendsTokenName 校验 manager 透传给 new-api 的过滤参数
// 改为 token_name 而非 token_id。new-api admin 端实测 token_id 静默失效、
// token_name 才生效，本测试防止这一关键修复回归。
func TestGetTokenLogsSendsTokenName(t *testing.T) {
    var gotQuery url.Values
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/api/log/" {
            t.Fatalf("unexpected path: %s", r.URL.Path)
        }
        gotQuery = r.URL.Query()
        w.Header().Set("Content-Type", "application/json")
        _, _ = w.Write([]byte(`{"success":true,"data":{"total":0,"items":[]}}`))
    }))
    defer server.Close()

    client := NewClient(server.URL, "tok", 1)
    _, err := client.GetTokenLogs(context.Background(), LogsQuery{
        TokenName: "app-0193ce63-4b8e-7000-a000-000000000001",
        Page:      1,
        PageSize:  20,
    })
    require.NoError(t, err)
    assert.Equal(t, "app-0193ce63-4b8e-7000-a000-000000000001", gotQuery.Get("token_name"))
    assert.Empty(t, gotQuery.Get("token_id"), "token_id 不应再发送（new-api 静默忽略，留着误导）")
}
```

- [ ] **Step 2：跑测试看编译失败**

Run: `go test ./internal/integrations/newapi/ -run TestGetTokenLogsSendsTokenName -count=1 -v`
Expected: 编译报错 `unknown field TokenName in struct literal`。

- [ ] **Step 3：改 LogsQuery 结构与 GetTokenLogs 透传**

Modify `internal/integrations/newapi/client.go`，定位到 `LogsQuery` 结构（约 line 147-156），把：

```go
type LogsQuery struct {
    TokenID   int64
    UserID    int64
    Username  string
    ModelName string
    Since     int64
    Until     int64
    Page      int
    PageSize  int
}
```

改为：

```go
// LogsQuery 控制 GetTokenLogs 的过滤条件；零值字段表示不过滤。
//
// 时间范围 Since / Until 是 unix 秒；new-api 端字段名为 start_timestamp / end_timestamp。
// PageSize 缺省 20，对应 new-api `p=1&page_size=20`。
//
// TokenName 是 manager 唯一可靠的 token 维度过滤口径：new-api admin 端
// 实测 token_id query 被静默忽略，只有 token_name 与 username/user_id 生效。
type LogsQuery struct {
    UserID    int64
    Username  string
    TokenName string
    ModelName string
    Since     int64
    Until     int64
    Page      int
    PageSize  int
}
```

再定位 `GetTokenLogs`（约 line 826），把：

```go
if q.TokenID > 0 {
    values.Set("token_id", strconv.FormatInt(q.TokenID, 10))
}
```

改为：

```go
if q.TokenName != "" {
    values.Set("token_name", q.TokenName)
}
```

- [ ] **Step 4：跑新测试**

Run: `go test ./internal/integrations/newapi/ -run TestGetTokenLogsSendsTokenName -count=1 -v`
Expected: PASS。

- [ ] **Step 5：跑整包测试，预期 service 包仍编译失败（下个 task 修）**

Run: `go test ./internal/integrations/newapi/ -count=1`
Expected: newapi 包全 PASS。

Run: `go build ./...`
Expected: 编译错误集中在 `internal/service/usage_service.go` / `internal/service/usage_service_test.go` 引用 `TokenID` 处。

- [ ] **Step 6：commit**

```bash
git add internal/integrations/newapi/client.go internal/integrations/newapi/client_test.go
git commit -m "refactor(newapi): LogsQuery 用 TokenName 取代 TokenID

new-api admin GET /api/log/ 实测忽略 token_id query 参数，
留着会让调用方误以为按 token id 过滤可用。改为透传 token_name，
该参数实测精确生效。service 包同步切换由下个 commit 完成。"
```

---

## Task 5：GetUserQuotaDates 接收 username 参数并客户端过滤

**Files:**
- Modify: `internal/integrations/newapi/client.go:866-883`
- Modify: `internal/integrations/newapi/client_test.go`（已有用例 `TestGetUserQuotaDatesBackfillsModelNameFromLogs` 签名变更 + 新增过滤用例）

- [ ] **Step 1：写失败用例**

Modify `internal/integrations/newapi/client_test.go`，末尾追加：

```go
// TestGetUserQuotaDatesFiltersByUsername 校验 GetUserQuotaDates 在客户端
// 按传入的 username 过滤 /api/data/users 响应。new-api admin 端实测
// id / username 这两个 query 都被静默忽略，会返回全平台所有用户的混合
// 按日聚合，因此 manager 必须自己再过一遍。
func TestGetUserQuotaDatesFiltersByUsername(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        switch {
        case r.Method == http.MethodGet && r.URL.Path == "/api/data/users":
            // 模拟 new-api 真实行为：忽略 id，返回 3 个用户混合
            _, _ = w.Write([]byte(`{"success":true,"data":[
                {"username":"target","created_at":1778569200,"model_name":"qwen","count":1,"quota":5,"token_used":10},
                {"username":"other-a","created_at":1778569200,"model_name":"qwen","count":2,"quota":7,"token_used":12},
                {"username":"target","created_at":1778572800,"model_name":"deepseek","count":3,"quota":8,"token_used":20},
                {"username":"other-b","created_at":1778572800,"model_name":"deepseek","count":4,"quota":11,"token_used":33}
            ]}`))
        case r.Method == http.MethodGet && r.URL.Path == "/api/log/":
            // enrichQuotaDatesWithLogModels 仍会走这条；用例里 model_name 都已填，
            // 不需要回填，但端点必须能响应。
            _, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0}}`))
        default:
            t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
        }
    }))
    defer server.Close()

    client := NewClient(server.URL, "tok", 1)
    items, err := client.GetUserQuotaDates(context.Background(), 42, "target", 1778486400, 1778572799)
    require.NoError(t, err)
    require.Len(t, items, 2)
    for _, it := range items {
        assert.Equal(t, "target", it.Username)
    }
}
```

- [ ] **Step 2：跑测试看 fail（编译错误：参数数量不对）**

Run: `go test ./internal/integrations/newapi/ -run TestGetUserQuotaDatesFiltersByUsername -count=1 -v`
Expected: 编译报错 `too many arguments in call to client.GetUserQuotaDates`。

- [ ] **Step 3：改 GetUserQuotaDates 签名 + 客户端过滤**

Modify `internal/integrations/newapi/client.go`（约 line 866-883），整段 `GetUserQuotaDates` 函数替换为：

```go
// GetUserQuotaDates 调 admin GET /api/data/users 拿指定 user 在时间窗内的按天 quota 汇总。
//
// new-api 端实测 id / username 两个 query 参数都被静默忽略——响应里固定包含全平台
// 所有用户的聚合，因此调用方必须传入目标 username，client 在响应上做精确过滤。
// username 由调用方从 organizations.newapi_username 读出。
func (c *Client) GetUserQuotaDates(ctx context.Context, userID int64, username string, since, until int64) ([]QuotaDate, error) {
    if userID == 0 {
        return nil, fmt.Errorf("GetUserQuotaDates: userID 不能为 0")
    }
    if username == "" {
        return nil, fmt.Errorf("GetUserQuotaDates: username 不能为空（new-api 端 id 过滤静默失效，必须客户端按 username 过滤）")
    }
    values := url.Values{}
    values.Set("id", strconv.FormatInt(userID, 10))
    if since > 0 {
        values.Set("start_timestamp", strconv.FormatInt(since, 10))
    }
    if until > 0 {
        values.Set("end_timestamp", strconv.FormatInt(until, 10))
    }
    raw, err := c.fetchQuotaDates(ctx, "/api/data/users?"+values.Encode())
    if err != nil {
        return nil, err
    }
    filtered := raw[:0]
    for _, it := range raw {
        if it.Username == username {
            filtered = append(filtered, it)
        }
    }
    return c.enrichQuotaDatesWithLogModels(ctx, filtered, since, until)
}
```

- [ ] **Step 4：修旧用例 `TestGetUserQuotaDatesBackfillsModelNameFromLogs` 的签名**

定位 `internal/integrations/newapi/client_test.go` 里 `TestGetUserQuotaDatesBackfillsModelNameFromLogs`，把：

```go
items, err := client.GetUserQuotaDates(context.Background(), 8, 1778486400, 1778572799)
```

改为：

```go
items, err := client.GetUserQuotaDates(context.Background(), 8, "org-demo", 1778486400, 1778572799)
```

（mock server 返回的 username 就是 `org-demo`，老用例只有一条数据，过滤后仍剩这一条。）

- [ ] **Step 5：跑 newapi 包全部测试**

Run: `go test ./internal/integrations/newapi/ -count=1`
Expected: 全 PASS。

- [ ] **Step 6：跑 build，预期 service 包还编译错（UsageNewAPIClient 接口签名对不上）**

Run: `go build ./...`
Expected: 错误集中在 `internal/service/usage_service.go` 接口与实现里 `GetUserQuotaDates` 签名不一致——下个 task 修。

- [ ] **Step 7：commit**

```bash
git add internal/integrations/newapi/client.go internal/integrations/newapi/client_test.go
git commit -m "refactor(newapi): GetUserQuotaDates 加 username 入参做客户端过滤

new-api admin GET /api/data/users 实测忽略 id / username query 参数，
固定返回全平台用户的混合按日聚合。改为由调用方从本地组织记录读出
username（organizations.newapi_username），client 收到响应后做精确
过滤。userID 仍透传是为了将来上游修复时少传数据，现状无实际差异。"
```

---

## Task 6：usage service 切到 TokenName + Username，单测全面更新

**Files:**
- Modify: `internal/service/usage_service.go:23-27`（UsageNewAPIClient 接口）
- Modify: `internal/service/usage_service.go:99-130`（GetAppUsage）
- Modify: `internal/service/usage_service.go:133-176`（GetMemberUsage）
- Modify: `internal/service/usage_service.go:179-218`（GetOrgUsage）
- Modify: `internal/service/usage_service_test.go`（fake 改、旧用例改、新用例加）

- [ ] **Step 1：先改 fakeUsageClient 适配新签名**

Modify `internal/service/usage_service_test.go`，定位 `fakeUsageClient` 结构与 `GetUserQuotaDates` 方法：

把：

```go
userQuota           []newapi.QuotaDate
userQuotaError      error
lastUserQuotaUserID int64
```

改为：

```go
userQuota             []newapi.QuotaDate
userQuotaError        error
lastUserQuotaUserID   int64
lastUserQuotaUsername string
```

并把：

```go
func (c *fakeUsageClient) GetUserQuotaDates(_ context.Context, userID, _, _ int64) ([]newapi.QuotaDate, error) {
    c.lastUserQuotaUserID = userID
    ...
}
```

改为：

```go
func (c *fakeUsageClient) GetUserQuotaDates(_ context.Context, userID int64, username string, _, _ int64) ([]newapi.QuotaDate, error) {
    c.lastUserQuotaUserID = userID
    c.lastUserQuotaUsername = username
    if c.userQuotaError != nil {
        return nil, c.userQuotaError
    }
    return c.userQuota, nil
}
```

- [ ] **Step 2：写失败用例（app）**

继续 modify `internal/service/usage_service_test.go`，末尾追加：

```go
// TestGetAppUsageUsesAppNewapiKeyName 校验 service 读 app.NewapiKeyName 后
// 通过 TokenName 而非 TokenID 调 newapi client。这是用量页修复的核心断言。
func TestGetAppUsageUsesAppNewapiKeyName(t *testing.T) {
    appID := pgtype.UUID{Bytes: uuid.MustParse("0193ce63-4b8e-7000-a000-000000000001"), Valid: true}
    store := &fakeUsageStore{
        // GetApp 当前总是 ErrNoRows，service 内部用不到；保留 activeApp 字段供 member 用例
    }
    _ = appID
    client := &fakeUsageClient{tokenLogs: newapi.LogsPage{Items: []newapi.LogEntry{{ID: 1}}, Total: 1}}
    svc := NewUsageService(store, client, nil)

    // GetAppUsage 不调 store.GetApp（handler 已把 newapi_key_id 与 keyName 信息通过参数传入）
    // service 内部把 appID 拼出 keyName="app-"+appID 作为兜底；当 store 提供 GetApp 后改读 NewapiKeyName。
    _, err := svc.GetAppUsage(context.Background(), platformAdmin(),
        "0193ce63-4b8e-7000-a000-000000000001",
        "owner-org", "owner-user",
        42,
        LogsQueryOptions{Page: 1, PageSize: 50})
    require.NoError(t, err)
    assert.Equal(t, "app-0193ce63-4b8e-7000-a000-000000000001", client.lastTokenLogsQuery.TokenName)
    assert.Equal(t, 50, client.lastTokenLogsQuery.PageSize)
}
```

> **说明**：当前 service.UsageStore 接口里 `GetApp` 已存在，但 fake 实现固定返回 `pgx.ErrNoRows`，且 GetAppUsage 的现有实现里**不调** `s.store.GetApp`。本 task 的 service 改造里**新增** 一段 `GetApp` 调用：成功取到则用 `app.NewapiKeyName`，ErrNoRows 时回退到 `"app-" + appID`。后续 Step 5 会落实。

- [ ] **Step 3：写失败用例（member）**

继续追加：

```go
// TestGetMemberUsageUsesAppNewapiKeyName 校验 member 维度走 GetActiveAppByOwner
// 拿到 app 后用 app.NewapiKeyName 作 TokenName；空字段回退到 "app-"+app.ID。
func TestGetMemberUsageUsesAppNewapiKeyName(t *testing.T) {
    appID := pgtype.UUID{Bytes: uuid.MustParse("0193ce63-4b8e-7000-a000-000000000002"), Valid: true}
    store := &fakeUsageStore{
        activeApp: sqlc.App{
            ID:             appID,
            NewapiKeyID:    pgtype.Text{String: "77", Valid: true},
            NewapiKeyName:  pgtype.Text{String: "app-0193ce63-4b8e-7000-a000-000000000002", Valid: true},
        },
    }
    client := &fakeUsageClient{tokenLogs: newapi.LogsPage{Items: []newapi.LogEntry{{ID: 9}}, Total: 1}}
    svc := NewUsageService(store, client, nil)

    _, err := svc.GetMemberUsage(context.Background(), platformAdmin(),
        "owner-org", uuid.NewString(), LogsQueryOptions{Page: 1, PageSize: 20})
    require.NoError(t, err)
    assert.Equal(t, "app-0193ce63-4b8e-7000-a000-000000000002", client.lastTokenLogsQuery.TokenName)
}

// TestGetMemberUsageFallsBackWhenKeyNameEmpty 校验回填前的边界：app.NewapiKeyName
// 字段尚未写入（老数据迁移后又有新建实例未跑修复路径等极端场景）时，
// service 自行拼 "app-"+app.ID。
func TestGetMemberUsageFallsBackWhenKeyNameEmpty(t *testing.T) {
    appID := pgtype.UUID{Bytes: uuid.MustParse("0193ce63-4b8e-7000-a000-000000000003"), Valid: true}
    store := &fakeUsageStore{
        activeApp: sqlc.App{
            ID:          appID,
            NewapiKeyID: pgtype.Text{String: "78", Valid: true},
            // NewapiKeyName 故意留空
        },
    }
    client := &fakeUsageClient{tokenLogs: newapi.LogsPage{}}
    svc := NewUsageService(store, client, nil)

    _, err := svc.GetMemberUsage(context.Background(), platformAdmin(),
        "owner-org", uuid.NewString(), LogsQueryOptions{})
    require.NoError(t, err)
    assert.Equal(t, "app-0193ce63-4b8e-7000-a000-000000000003", client.lastTokenLogsQuery.TokenName)
}
```

- [ ] **Step 4：写失败用例（org）**

继续追加：

```go
// TestGetOrgUsagePassesUsernameToClient 校验 GetOrgUsage 把
// organizations.newapi_username 透传给 client 做客户端过滤。
func TestGetOrgUsagePassesUsernameToClient(t *testing.T) {
    orgID := pgtype.UUID{Bytes: uuid.MustParse("0193ce63-4b8e-7000-a000-0000000000aa"), Valid: true}
    store := &fakeUsageStore{
        org: sqlc.Organization{
            ID:              orgID,
            NewapiUserID:    pgtype.Text{String: "14", Valid: true},
            NewapiUsername:  pgtype.Text{String: "m8-test", Valid: true},
        },
    }
    client := &fakeUsageClient{userQuota: []newapi.QuotaDate{{Date: "2026-05-19", Quota: 7}}}
    svc := NewUsageService(store, client, nil)

    _, err := svc.GetOrgUsage(context.Background(), platformAdmin(), uuidToString(orgID), 0, 0)
    require.NoError(t, err)
    assert.Equal(t, int64(14), client.lastUserQuotaUserID)
    assert.Equal(t, "m8-test", client.lastUserQuotaUsername)
}

// TestGetOrgUsageReturnsEmptyWhenUsernameEmpty 校验老数据 / 未回填边界：
// organizations.newapi_username 为空时，service 直接返回空 series，
// 不调 newapi（避免拿一堆其他组织的数据来污染）。
func TestGetOrgUsageReturnsEmptyWhenUsernameEmpty(t *testing.T) {
    orgID := pgtype.UUID{Bytes: uuid.MustParse("0193ce63-4b8e-7000-a000-0000000000bb"), Valid: true}
    store := &fakeUsageStore{
        org: sqlc.Organization{
            ID:           orgID,
            NewapiUserID: pgtype.Text{String: "99", Valid: true},
            // NewapiUsername 故意留空
        },
    }
    client := &fakeUsageClient{}
    svc := NewUsageService(store, client, nil)

    view, err := svc.GetOrgUsage(context.Background(), platformAdmin(), uuidToString(orgID), 0, 0)
    require.NoError(t, err)
    assert.Empty(t, view.Items)
    assert.Equal(t, int64(0), client.lastUserQuotaUserID, "username 空时不应调 newapi")
}
```

- [ ] **Step 5：把旧用例里对 `lastTokenLogsQuery.TokenID` 的断言改成对 `TokenName`**

定位 `internal/service/usage_service_test.go` 里以下三个用例并改：

`TestUsageServiceAppProxiesTokenLogs`（约 line 18-40）：

```go
if client.lastTokenLogsQuery.TokenID != 42 || client.lastTokenLogsQuery.PageSize != 50 {
    t.Fatalf("token logs query = %+v", client.lastTokenLogsQuery)
}
```

改为：

```go
if client.lastTokenLogsQuery.TokenName != "app-app-1" || client.lastTokenLogsQuery.PageSize != 50 {
    t.Fatalf("token logs query = %+v", client.lastTokenLogsQuery)
}
```

（service 把传入的 appID `"app-1"` 拼成 keyName `"app-app-1"`；newapiKeyID 42 仍然是 service 用来判断"未绑定"短路的；测试值不变。）

`TestUsageServiceMemberUsesActiveAppByOwner`（约 line 80-100）：定位 `client.lastTokenLogsQuery.TokenID != 77`，改为 `client.lastTokenLogsQuery.TokenName != "app-" + uuidToString(store.activeApp.ID)`。需要把 activeApp 构造时给个稳定 ID，并在 fake 上把 `NewapiKeyName` 也填上同值，断言 service 优先读 KeyName。

`TestUsageServiceMemberMapsErrors`（约 line 110-118）：定位 `require.Equal(t, int64(77), client.lastTokenLogsQuery.TokenID)`，改为校验 TokenName 与上面一致。

> **建议**：先 `git grep -n "lastTokenLogsQuery.TokenID" internal/service/` 把所有引用列出来，逐一改完，确保没有漏。

- [ ] **Step 6：跑测试看新老用例都 fail（service 实现还没改）**

Run: `go test ./internal/service/ -run "TestGetAppUsageUsesAppNewapiKeyName|TestGetMemberUsage|TestGetOrgUsage|TestUsageServiceAppProxies|TestUsageServiceMember" -count=1 -v`
Expected: FAIL（TokenName 为空、Username 为空）。

- [ ] **Step 7：改 GetAppUsage**

Modify `internal/service/usage_service.go`，定位 `GetAppUsage`：

```go
func (s *UsageService) GetAppUsage(ctx context.Context, principal auth.Principal, appID, ownerOrgID, ownerUserID string, newapiKeyID int64, opts LogsQueryOptions) (LogsPage, error) {
    if !auth.CanReadAppKnowledge(principal, ownerOrgID, ownerUserID) {
        return LogsPage{}, ErrForbidden
    }
    if s.client == nil {
        return LogsPage{}, ErrUsageUnavailable
    }
    if newapiKeyID == 0 {
        return LogsPage{Scope: "app", ScopeID: appID, Items: []newapi.LogEntry{}, UpdatedAt: time.Now()}, nil
    }
    page, err := s.client.GetTokenLogs(ctx, newapi.LogsQuery{
        TokenID:   newapiKeyID,
        Since:     opts.Since,
        ...
    })
```

替换为：

```go
func (s *UsageService) GetAppUsage(ctx context.Context, principal auth.Principal, appID, ownerOrgID, ownerUserID string, newapiKeyID int64, opts LogsQueryOptions) (LogsPage, error) {
    if !auth.CanReadAppKnowledge(principal, ownerOrgID, ownerUserID) {
        return LogsPage{}, ErrForbidden
    }
    if s.client == nil {
        return LogsPage{}, ErrUsageUnavailable
    }
    if newapiKeyID == 0 {
        return LogsPage{Scope: "app", ScopeID: appID, Items: []newapi.LogEntry{}, UpdatedAt: time.Now()}, nil
    }
    // 优先读 app.NewapiKeyName；store.GetApp 失败或字段空时回退到约定拼装。
    keyName := "app-" + appID
    if s.store != nil {
        appUUID, parseErr := parseUUID(appID)
        if parseErr == nil {
            if app, getErr := s.store.GetApp(ctx, appUUID); getErr == nil && app.NewapiKeyName.Valid && app.NewapiKeyName.String != "" {
                keyName = app.NewapiKeyName.String
            }
        }
    }
    page, err := s.client.GetTokenLogs(ctx, newapi.LogsQuery{
        TokenName: keyName,
        Since:     opts.Since,
        Until:     opts.Until,
        Page:      opts.Page,
        PageSize:  opts.PageSize,
        ModelName: opts.ModelName,
    })
    if err != nil {
        ...保持现有 failAuditor + mapUsageError 不变...
    }
    return LogsPage{Scope: "app", ScopeID: appID, Items: page.Items, Total: page.Total, UpdatedAt: time.Now()}, nil
}
```

- [ ] **Step 8：改 GetMemberUsage**

Modify `internal/service/usage_service.go`，定位 `GetMemberUsage` 内部 `GetTokenLogs` 调用前后，把：

```go
keyID := parseInt64Default(app.NewapiKeyID.String, 0)
if keyID == 0 || s.client == nil {
    return LogsPage{Scope: "member", ScopeID: memberID, Items: []newapi.LogEntry{}, UpdatedAt: time.Now()}, nil
}
page, err := s.client.GetTokenLogs(ctx, newapi.LogsQuery{
    TokenID:   keyID,
    Since:     opts.Since,
    ...
})
```

改为：

```go
keyID := parseInt64Default(app.NewapiKeyID.String, 0)
if keyID == 0 || s.client == nil {
    return LogsPage{Scope: "member", ScopeID: memberID, Items: []newapi.LogEntry{}, UpdatedAt: time.Now()}, nil
}
keyName := app.NewapiKeyName.String
if keyName == "" {
    keyName = "app-" + uuidToString(app.ID)
}
page, err := s.client.GetTokenLogs(ctx, newapi.LogsQuery{
    TokenName: keyName,
    Since:     opts.Since,
    Until:     opts.Until,
    Page:      opts.Page,
    PageSize:  opts.PageSize,
    ModelName: opts.ModelName,
})
```

- [ ] **Step 9：改 GetOrgUsage**

Modify `internal/service/usage_service.go`，定位 `GetOrgUsage`：

```go
if !org.NewapiUserID.Valid || org.NewapiUserID.String == "" {
    return QuotaSeries{Scope: "organization", ScopeID: orgID, Items: []newapi.QuotaDate{}, UpdatedAt: time.Now()}, nil
}
userID := parseInt64Default(org.NewapiUserID.String, 0)
if userID == 0 {
    return QuotaSeries{Scope: "organization", ScopeID: orgID, Items: []newapi.QuotaDate{}, UpdatedAt: time.Now()}, nil
}
items, err := s.client.GetUserQuotaDates(ctx, userID, since, until)
```

替换为：

```go
if !org.NewapiUserID.Valid || org.NewapiUserID.String == "" {
    return QuotaSeries{Scope: "organization", ScopeID: orgID, Items: []newapi.QuotaDate{}, UpdatedAt: time.Now()}, nil
}
userID := parseInt64Default(org.NewapiUserID.String, 0)
if userID == 0 {
    return QuotaSeries{Scope: "organization", ScopeID: orgID, Items: []newapi.QuotaDate{}, UpdatedAt: time.Now()}, nil
}
// org.NewapiUsername 是 client 做客户端过滤 /api/data/users 响应的依据；
// 字段空意味着老数据未回填，此时不调 newapi，直接返回空 series 避免污染。
if !org.NewapiUsername.Valid || org.NewapiUsername.String == "" {
    return QuotaSeries{Scope: "organization", ScopeID: orgID, Items: []newapi.QuotaDate{}, UpdatedAt: time.Now()}, nil
}
items, err := s.client.GetUserQuotaDates(ctx, userID, org.NewapiUsername.String, since, until)
```

- [ ] **Step 10：改 UsageNewAPIClient 接口签名**

Modify `internal/service/usage_service.go`（约 line 23-27），把：

```go
type UsageNewAPIClient interface {
    GetTokenLogs(ctx context.Context, q newapi.LogsQuery) (newapi.LogsPage, error)
    GetUserQuotaDates(ctx context.Context, userID, since, until int64) ([]newapi.QuotaDate, error)
    GetAllQuotaDates(ctx context.Context, since, until int64) ([]newapi.QuotaDate, error)
}
```

改为：

```go
type UsageNewAPIClient interface {
    GetTokenLogs(ctx context.Context, q newapi.LogsQuery) (newapi.LogsPage, error)
    GetUserQuotaDates(ctx context.Context, userID int64, username string, since, until int64) ([]newapi.QuotaDate, error)
    GetAllQuotaDates(ctx context.Context, since, until int64) ([]newapi.QuotaDate, error)
}
```

- [ ] **Step 11：跑完整 service 测试**

Run: `go test ./internal/service/ -count=1`
Expected: 全 PASS。

- [ ] **Step 12：跑全仓库 build + 关联测试**

Run: `go build ./...`
Expected: 编译通过。

Run: `go test ./internal/integrations/newapi/ ./internal/service/ ./internal/worker/handlers/ ./internal/api/handlers/ -count=1`
Expected: 全 PASS。

- [ ] **Step 13：commit**

```bash
git add internal/service/usage_service.go internal/service/usage_service_test.go
git commit -m "fix(usage): 按 token_name / username 过滤，消除跨实例/组织数据混淆

GetAppUsage / GetMemberUsage 改读 apps.newapi_key_name 作 TokenName；
字段空时回退到 'app-'+app.id 兼容老数据。GetOrgUsage 改读
organizations.newapi_username 传给 client 做客户端过滤；空字段时
直接返回空 series 而非调上游污染。

修复用户报告的'切换实例/成员看到的不是该对象自身数据'，根因为
new-api admin /api/log/?token_id= 与 /api/data/users?id= 静默忽略
过滤参数。"
```

---

## Task 7：前端 effective ID 消除跨组织残留

**Files:**
- Modify: `web/src/pages/usage/UsagePage.vue:140-225`
- Modify: `web/src/pages/usage/__tests__/UsagePage.spec.ts`（新增用例）

- [ ] **Step 1：把 useMembersQuery mock 改成可控的 vi.fn()**

Modify `web/src/pages/usage/__tests__/UsagePage.spec.ts`：

把文件顶部的：

```ts
vi.mock('@/api/hooks/useMembers', () => ({
  useMembersQuery: () => ({ data: ref([]) }),
}))
```

改为：

```ts
vi.mock('@/api/hooks/useMembers', () => ({
  useMembersQuery: vi.fn(() => ({ data: ref([]) })),
}))
```

并确保文件顶部 import 区域包含：

```ts
import { useMembersQuery } from '@/api/hooks/useMembers'
```

（如果已经有就跳过。）

- [ ] **Step 2：在文件末尾追加可断言的失败用例**

```ts
describe('UsagePage effective ID 消除跨组织残留', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    usageRefs.orgRef = undefined
    usageRefs.memberOrgRef = undefined
    usageRefs.memberRef = undefined
  })

  // effectiveMemberId 把"成员 ID 必须落在当前 members 列表里"作为硬约束，
  // 避免切换组织瞬间 vue-query 还以旧 memberId + 新 orgId 发查询。
  // 验证三个阶段：初始 auto-select → 列表清空（切组织瞬间）回退 undefined →
  // 新列表到位后重新 auto-select。
  it('memberRef 在选中成员不在当前 members 列表时解析为 undefined', async () => {
    const auth = useAuthStore()
    auth.user = {
      id: 'admin-1', org_id: 'org-A',
      username: 'admin', display_name: '平台管理员',
      role: 'platform_admin', status: 'active',
    }

    // 用一个可变 ref 充当 useMembersQuery 的返回值
    const membersRef = ref<{ id: string; username: string; display_name: string }[]>([
      { id: 'mem-A', username: 'a', display_name: 'A' },
    ])
    vi.mocked(useMembersQuery).mockReturnValue({ data: membersRef } as any)

    shallowMount(UsagePage, {
      global: { stubs: { RouterLink: true, UsageSummary: true } },
    })

    // 阶段 1：初始 auto-select 之后 memberRef 等于 mem-A
    await new Promise((r) => setTimeout(r, 0))
    expect(usageRefs.memberRef?.value).toBe('mem-A')

    // 阶段 2：切换组织瞬间，旧 members 列表被替换为空数组
    // （vue-query refetch 返回空，或新 org 还没拉到 members）
    membersRef.value = []
    await new Promise((r) => setTimeout(r, 0))
    expect(usageRefs.memberRef?.value).toBeUndefined()

    // 阶段 3：新 org 的列表到位（[B]），watch(members) auto-select 接管
    membersRef.value = [{ id: 'mem-B', username: 'b', display_name: 'B' }]
    await new Promise((r) => setTimeout(r, 0))
    expect(usageRefs.memberRef?.value).toBe('mem-B')
  })
})
```

- [ ] **Step 3：跑测试看 fail**

Run: `cd web && pnpm test -- src/pages/usage/__tests__/UsagePage.spec.ts -t "effective ID"`
Expected: FAIL（当前实现切组织瞬间 memberRef 还是旧值）。

- [ ] **Step 4：改 UsagePage.vue 加 effective computed**

Modify `web/src/pages/usage/UsagePage.vue`，定位 `<script setup>` 区域里 `selectedMemberId` 与 `memberRef` 的定义（约 line 170-175）：

```ts
const selectedMemberId = ref(isOrgMember.value ? auth.user?.id ?? '' : '')
const memberRef = computed(() =>
  isOrgMember.value ? auth.user?.id : selectedMemberId.value || undefined,
)
```

替换为：

```ts
const selectedMemberId = ref(isOrgMember.value ? auth.user?.id ?? '' : '')

// effectiveMemberId 把"成员 ID 必须落在当前 members 列表里"作为硬约束，
// 避免切换组织瞬间 vue-query 还以旧 memberId + 新 orgId 发查询。
// members 列表未加载时回退到 undefined，让 watch(members) auto-select 接管。
const effectiveMemberId = computed<string | undefined>(() => {
  if (isOrgMember.value) return auth.user?.id
  const id = selectedMemberId.value
  if (!id) return undefined
  if (!members.value) return undefined
  return members.value.some((m) => m.id === id) ? id : undefined
})

const memberRef = effectiveMemberId
```

再定位 `selectedAppId` / `useAppUsageQuery` 那段（约 line 184-201）：

```ts
const selectedAppId = ref<string | undefined>()
const { data: apps } = useAppsByOrgQuery(effectiveOrgId)
...
const selectedApp = computed(() => (apps.value ?? []).find((app) => app.id === selectedAppId.value))
...
const { data: appView, isLoading: appLoading, error: appError } = useAppUsageQuery(selectedAppId, appUsageContext)
```

把最后一行 `useAppUsageQuery(selectedAppId, ...)` 改为 `useAppUsageQuery(effectiveAppId, ...)`，并在 `selectedAppId` 声明下方插入：

```ts
// effectiveAppId 同 effectiveMemberId，消除跨组织残留。
const effectiveAppId = computed<string | undefined>(() => {
  const id = selectedAppId.value
  if (!id) return undefined
  if (!apps.value) return undefined
  return apps.value.some((a) => a.id === id) ? id : undefined
})
```

再定位 `watch(effectiveOrgId, ...)` 段（约 line 203-209），把：

```ts
watch(effectiveOrgId, () => {
  if (!isOrgMember.value) {
    selectedMemberId.value = ''
  }
  selectedAppId.value = undefined
})
```

整段删除（reset 由 effective computed 自动完成）。

最后定位 `watch(members, ...)` 与 `watch(apps, ...)`（约 line 211-221），把：

```ts
watch(members, (list) => {
  if (!isOrgMember.value && !selectedMemberId.value && list && list.length > 0) {
    selectedMemberId.value = list[0].id
  }
})

watch(apps, (list) => {
  if (!selectedAppId.value && list && list.length > 0) {
    selectedAppId.value = list[0].id
  }
})
```

改为：

```ts
// 列表加载后，如果 effective ID 还没解析出来（要么没选过、要么旧选中
// 不在新列表里），自动选第一项。条件用 effective.value 而非
// selectedXxx.value，确保跨组织时也能正确 auto-select。
watch(members, (list) => {
  if (!isOrgMember.value && effectiveMemberId.value === undefined && list && list.length > 0) {
    selectedMemberId.value = list[0].id
  }
})

watch(apps, (list) => {
  if (effectiveAppId.value === undefined && list && list.length > 0) {
    selectedAppId.value = list[0].id
  }
})
```

- [ ] **Step 5：跑前端测试**

Run: `cd web && pnpm test -- src/pages/usage/__tests__/UsagePage.spec.ts`
Expected: 全 PASS（新用例 + 旧 2 个用例）。

- [ ] **Step 6：跑前端类型检查**

Run: `make web-typecheck`
Expected: 退出码 0。

- [ ] **Step 7：commit**

```bash
git add web/src/pages/usage/UsagePage.vue web/src/pages/usage/__tests__/UsagePage.spec.ts
git commit -m "fix(web/usage): effective ID 消除跨组织残留

切换组织瞬间会出现"新 org_id + 旧 member/app_id"组合的混合查询，
原因是 watch(effectiveOrgId) 重置 selectedXxx 与 vue-query 重发请求
存在调度竞态。改为用 effective computed 把"选中 ID 必须落在当前
列表里"作为硬约束：列表未到 / 选中项不在列表 → 返回 undefined，
vue-query 自动停发；列表到位后 watch(members/apps) 接管 auto-select。

附带删除 watch(effectiveOrgId) 里的 reset 逻辑（由 computed 自动完成）。"
```

---

## Task 8：交付前浏览器端到端验证

**Files:** 无代码改动，仅执行 + 截图 / 记录。

- [ ] **Step 1：起本地服务（如未运行）**

Run: `make dev-up`
Expected: manager-api / manager-web / new-api 等容器全 up。

- [ ] **Step 2：造测试用量（如 spec 调研时数据已清空，重做一遍）**

Run:
```bash
# 复活 M8 组织下两个实例并打 token
PGPASSWORD=ocm psql -h localhost -p 15432 -U ocm -d ocm -c "UPDATE apps SET deleted_at=NULL, status='running' WHERE id IN ('f010c08f-e14b-49a5-9d64-49a6bf2bf79d','f25bc52b-93d6-4e5a-a97a-71da6d488fcc');"

# 用各自 sk- 调 new-api 产生混合日志
for i in 1 2 3; do
  curl -s -X POST http://localhost:3000/v1/chat/completions \
    -H "Authorization: Bearer sk-pTAe0efYE8P9tEsemopEjAVE1Uxsi7cOORhE9tsClCQzqHZ7" \
    -H "Content-Type: application/json" \
    -d '{"model":"qwen3.5:27b","messages":[{"role":"user","content":"user C call '$i'"}]}' > /dev/null
done
for i in 1 2; do
  curl -s -X POST http://localhost:3000/v1/chat/completions \
    -H "Authorization: Bearer sk-spPhgKgBQNo6JAYFxroNOyGCpyeO1nBEq7VSgk5cqSSMEYzN" \
    -H "Content-Type: application/json" \
    -d '{"model":"qwen3.5:27b","messages":[{"role":"user","content":"user E call '$i'"}]}' > /dev/null
done
```

Expected: 两条 token（17 / 18）分别产生 3 / 2 条 qwen3.5:27b 日志。

- [ ] **Step 3：用 chrome-devtools MCP 登录并验证**

Run（在工具调用流程里）：用 admin / admin123 登录 `http://localhost:5173`，切到 `/usage`。

- [ ] **Step 4：实例 tab 逐个核对**

进入实例 tab，选 M8验证组织 → 选 "M8 用户 C 的实例"：
- Token 总量 应 ≈ token 17 真实合计（约 ~750 token，且只来自 qwen3.5:27b）；不再混入 deepseek-v4-pro / hermes-test 等数据
- 切到 "M8 用户 E 的实例" → 数据立即变化到 token 18 的合计（更少）

- [ ] **Step 5：成员 tab 同上**

选 M8 用户 C / M8 用户 E，断言数据切换正确。

- [ ] **Step 6：组织 tab**

选 M8验证组织 → summary 只包含 m8-test 这一个 username 的数据；切到"进度验证组织"数据立即变化。

- [ ] **Step 7：跨组织切换 Network 验证**

开浏览器 DevTools Network，从"进度验证组织"切到"M8验证组织"。

Expected: 不应出现形如 `GET /api/v1/usage/members/98fecafb-...?org_id=03be7d7e-...` 的请求（旧 member ID 配新 org ID）。如果出现，回到 Task 7 看 effective computed 是否被 vue-query 正确订阅。

- [ ] **Step 8：平台 tab + 未绑定 token 的实例边界**

- 平台 tab：summary 与修复前一致
- 选 E2E测试实例（未绑定 token）：仍展示"该实例尚未绑定 new-api key，暂无实例维度用量。"

- [ ] **Step 9：跑 openapi-check 确认契约干净**

Run: `make openapi-check`
Expected: 退出码 0，git working tree 没有 `openapi.yaml` / `generated.ts` 的变更。

- [ ] **Step 10：跑全量测试**

Run: `make test && cd web && pnpm test`
Expected: 全 PASS。

- [ ] **Step 11：在本任务上不做 git commit；任意 fix-up 如有产生新代码，按对应 Task 的形态独立提交**

---

## 验收清单

- [ ] Task 1：migration up/down 干净，sqlc 重生成产物入库
- [ ] Task 2：组织创建落 newapi_username，单测覆盖
- [ ] Task 3：实例初始化落 newapi_key_name，单测覆盖
- [ ] Task 4：newapi LogsQuery 用 TokenName，单测断言不再发 token_id
- [ ] Task 5：GetUserQuotaDates 接收 username 做客户端过滤
- [ ] Task 6：usage service 三个维度全部切到新口径，旧测试断言更新
- [ ] Task 7：前端 effective computed 消除跨组织残留
- [ ] Task 8：浏览器全链路通过 + openapi-check 干净
