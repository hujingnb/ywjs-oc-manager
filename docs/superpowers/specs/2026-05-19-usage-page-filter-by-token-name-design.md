# 用量页面"切实例/成员/组织看到的不是自己的数据"修复设计

- 状态：Design
- 日期：2026-05-19
- 范围：DB schema（organizations / apps）+ manager 后端 usage service + newapi 集成层 + 前端 UsagePage

## 一、问题与现象

用量页面（`/usage`）的"实例"、"成员"、"组织"三个 tab 都存在数据隔离失效：

- 选中某个**实例**，summary（Token 总量 / 金额 / 使用总量）和明细行展示的是远超此实例真实用量的数据；不同实例之间切来切去看到的内容几乎一样。
- 选中某个**成员**，同样的现象。
- 选中某个**组织**，看似展示本组织数据，实际上混入了其他组织的数据（用户最初未察觉，本次顺带修复）。

平台 tab 不受影响。

## 二、根因（实测验证）

manager 是 new-api `/api/log/` 与 `/api/data/` 的薄代理。本地直连 new-api（`calciumion/new-api:latest`）admin 接口验证：

| 接口 | 透传的过滤参数 | 实际行为 |
|---|---|---|
| `/api/log/?token_id=18` | `token_id` | **被静默忽略**，返回 token_id ∈ {0, 13, 17, 18} 的混合日志 |
| `/api/log/?token_name=app-<uuid>` | `token_name` | ✅ 精确生效 |
| `/api/log/?username=m8-test` | `username` | ✅ 生效 |
| `/api/log/?user_id=14` | `user_id` | ✅ 生效 |
| `/api/data/users?id=14` | `id` | **被静默忽略**，返回所有用户的按日聚合 |
| `/api/data/users?username=m8-test` | `username` | **被静默忽略** |
| `/api/data/` | – | 平台维度聚合，正常工作 |

manager 与浏览器联合验证（`/api/v1/apps/<id>/usage?newapi_key_id=18` 响应包含 token_id ∈ {0, 13, 17, 18} 共 190 条混合记录），与直连结果一致。

**结论**：

- 实例 tab 走 `GetAppUsage` → `GetTokenLogs(LogsQuery{TokenID: keyID})` → `/api/log/?token_id=X` → 过滤失效。
- 成员 tab 走 `GetMemberUsage` → 同样的 `GetTokenLogs` 链路 → 过滤失效。
- 组织 tab 走 `GetOrgUsage` → `GetUserQuotaDates(userID)` → `/api/data/users?id=X` → 过滤失效。
- 平台 tab 走 `GetPlatformUsage` → `/api/data/` → 无过滤需求，正确。

附带发现一个前端竞态：切换组织时 `watch(effectiveOrgId, ...)` 把 `selectedMemberId` 重置为空字符串，但 vue-query 对 `memberOrgRef` 的响应可能先一步发出请求，导致以"新组织 ID + 旧成员 ID"组合的查询发到后端（浏览器 Network 面板实测到了 98fecafb 这个隶属于"进度验证组织"的 member ID 被带到 e2e-org 的查询里）。被根因掩盖了，但仍是隐患。

## 三、修复策略

不在前端硬性回避问题，也不写"自动判断 new-api 哪个过滤生效"的兼容层。直接把数据过滤改到 new-api 实测能稳定生效的口径：

- 实例 / 成员：用 `token_name` 取代 `token_id`。
- 组织：在 manager 一侧客户端按 `username` 过滤 `/api/data/users` 响应。
- 前端：用 computed 把"选中 ID 必须在当前列表内"作为硬约束，去掉跨组织残留。

**关键决策**：`username`、`token_name` 这些 new-api 端的稳定标识，manager 在创建链路里**显式落库**到 `organizations.newapi_username` 与 `apps.newapi_key_name`，而不是靠"约定派生"或"额外查 `/api/user/<id>`"：

- 显式落库，消除 manager 与 new-api 命名规则的隐式耦合；未来 new-api 端命名规则若变化，只需要在创建链路改一处。
- 避免每次用量查询额外多一次 new-api 调用。
- `username` 当前实际就是 `organizations.code`，`token_name` 当前实际就是 `"app-" + app.ID`——落一个跟 code/id 同值的字段看似冗余，但语义上"组织 / 实例在 new-api 那侧叫什么"是另一件事，分开存更清晰。

## 四、详细方案

### 4.1 数据库 schema

新 migration：`internal/migrations/000021_organizations_apps_newapi_names.up.sql` / `.down.sql`

**`organizations`**

```sql
ALTER TABLE organizations
    ADD COLUMN newapi_username TEXT;

-- 回填：已经在 new-api 创建过 user 的组织，username = code（实测一致）。
UPDATE organizations
SET newapi_username = code
WHERE newapi_user_id IS NOT NULL AND newapi_user_id != '';
```

注释：`newapi_username` 与 `code` 在当前创建链路上同值，但语义不同——`code` 是组织在 manager 内部的标识，`newapi_username` 是该组织在 new-api 侧的 user 名。允许将来解耦。

**`apps`**

```sql
ALTER TABLE apps
    ADD COLUMN newapi_key_name TEXT;

-- 回填：已经绑定 new-api token 的实例，token name = "app-" + id（实测一致）。
UPDATE apps
SET newapi_key_name = 'app-' || id::text
WHERE newapi_key_id IS NOT NULL AND newapi_key_id != '';
```

down migration 对称 `DROP COLUMN`。

### 4.2 sqlc queries

文件：`internal/store/queries/organizations.sql` 与 `internal/store/queries/apps.sql`

**`SetOrganizationNewAPIUser`** 增加 `newapi_username` 参数：

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

**`SetAppNewAPIKey`** 增加 `newapi_key_name` 参数：

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

`make sqlc-generate` 重新生成；handler / service 调用点对应更新（4.3 / 4.4）。

### 4.3 创建链路写入

**`internal/service/organization_service.go`** — `provisionNewAPIUser`：

```go
updated, err := s.store.SetOrganizationNewAPIUser(ctx, sqlc.SetOrganizationNewAPIUserParams{
    ID:                              org.ID,
    NewapiUserID:                    pgtype.Text{String: strconv.FormatInt(user.ID, 10), Valid: true},
    NewapiUserCredentialsCiphertext: pgtype.Text{String: ciphertext, Valid: true},
    NewapiUsername:                  pgtype.Text{String: username, Valid: true}, // 即 org.Code
})
```

`username` 这个局部变量已经在前面构造好（`username := org.Code`），直接复用。

**`internal/worker/handlers/app_initialize.go`** — `provisionAPIKey`（或相邻位置）：

```go
keyName := fmt.Sprintf("app-%s", uuidToString(app.ID))
key, err := client.CreateAPIKey(ctx, newapi.CreateAPIKeyInput{
    Name: keyName,
    // ...
})
// ...
updated, err := h.store.SetAppNewAPIKey(ctx, sqlc.SetAppNewAPIKeyParams{
    ID:                  app.ID,
    NewapiKeyID:         pgtype.Text{String: fmt.Sprintf("%d", key.ID), Valid: true},
    NewapiKeyCiphertext: pgtype.Text{String: ciphertext, Valid: true},
    ApiKeyStatus:        domain.APIKeyStatusActive,
    NewapiKeyName:       pgtype.Text{String: keyName, Valid: true},
})
```

`keyName` 由原来的内联字符串提到局部变量，CreateAPIKey 入参和 SetAppNewAPIKey 入参共用一份，确保数据库写的就是 new-api 里实际的 name。

### 4.4 newapi 集成层

文件：`internal/integrations/newapi/client.go`、`internal/integrations/newapi/client_test.go`

**`LogsQuery` 调整**

```go
type LogsQuery struct {
    UserID    int64
    Username  string
    TokenName string  // 新增：唯一可靠的 token 维度过滤口径
    ModelName string
    Since     int64
    Until     int64
    Page      int
    PageSize  int
}
```

`TokenID` 字段删除（实测被 new-api 静默忽略；留着误导调用方）。`GetTokenLogs` 中 `values.Set("token_id", ...)` 删除，改为：

```go
if q.TokenName != "" {
    values.Set("token_name", q.TokenName)
}
```

**`GetUserQuotaDates` 客户端过滤**

调整签名，把 username 提到入参（由 service 层从 `org.NewapiUsername` 直接读，不再额外查 new-api）：

```go
// GetUserQuotaDates 拿指定 user 在时间窗内的按天 quota 汇总。
// new-api admin /api/data/users 的 id 过滤实测失效，因此 client 拿到完整响应后
// 按 username 做客户端过滤；username 由 caller 从本地组织记录读出（不再额外查 /api/user/<id>）。
func (c *Client) GetUserQuotaDates(ctx context.Context, userID int64, username string, since, until int64) ([]QuotaDate, error) {
    // ... 原 fetchQuotaDates 调用不变（保留 id 参数：上游若修复，少传数据；目前实测无差异） ...
    filtered := items[:0]
    for _, it := range items {
        if it.Username == username {
            filtered = append(filtered, it)
        }
    }
    return c.enrichQuotaDatesWithLogModels(ctx, filtered, since, until)
}
```

`enrichQuotaDatesWithLogModels` 不变（它走 `/api/log/?username=X`，username 过滤本就生效）。

**不新增** `GetUser` helper（前一版设计的 `GET /api/user/<id>` 解析路线作废，username 直接从 manager 数据库读）。

### 4.5 usage service

文件：`internal/service/usage_service.go`、`internal/service/usage_service_test.go`

**`UsageStore` 接口的 `GetApp` / `GetOrganization` 返回值已经是完整的 `sqlc.App` / `sqlc.Organization`**，新字段自动包含进来，接口本身不需要扩展。

**`GetAppUsage`**

- handler 入口签名保留 `newapiKeyID int64`（前端已传，OpenAPI 已发布，避免重新生成）。
- service 内部：
  - 仍保留 `newapiKeyID == 0` 短路（未绑定 new-api，返回空 items）。
  - 通过 `s.store.GetApp(ctx, ...)` 读 `app.NewapiKeyName`；若为空回退到 `"app-" + appID` 派生（保护未回填的边界情况）。
  - 调用：

```go
page, err := s.client.GetTokenLogs(ctx, newapi.LogsQuery{
    TokenName: keyName,  // 从 app.NewapiKeyName 读，或回退派生
    Since:     opts.Since,
    Until:     opts.Until,
    Page:      opts.Page,
    PageSize:  opts.PageSize,
    ModelName: opts.ModelName,
})
```

**`GetMemberUsage`**

- `GetActiveAppByOwner` 拿到 app 后，沿用 `app.NewapiKeyID.String == ""` 短路。
- 否则从 `app.NewapiKeyName` 读 token_name，构造 LogsQuery 同上。

**`GetOrgUsage`**

- 现有逻辑已经从 `s.store.GetOrganization(...)` 拿 `org`。
- 新增：从 `org.NewapiUsername.String` 读 username；如为空（未回填的边界）则返回空 series 短路（不调 `/api/data/users`，避免污染）。
- 调用：

```go
items, err := s.client.GetUserQuotaDates(ctx, userID, org.NewapiUsername.String, since, until)
```

### 4.6 前端

文件：`web/src/pages/usage/UsagePage.vue`、`web/src/pages/usage/__tests__/UsagePage.spec.ts`

把"选中 ID 必须落在当前列表内"作为硬约束放在 computed 里：

```ts
const selectedMemberId = ref<string>('')
const effectiveMemberId = computed(() => {
  const id = selectedMemberId.value
  if (!id) return undefined
  // 列表还没拉到 → 暂不发查询（避免跨组织残留）
  if (!members.value) return undefined
  return members.value.some((m) => m.id === id) ? id : undefined
})

const selectedAppId = ref<string | undefined>()
const effectiveAppId = computed(() => {
  const id = selectedAppId.value
  if (!id) return undefined
  if (!apps.value) return undefined
  return apps.value.some((a) => a.id === id) ? id : undefined
})
```

- `memberRef` / `useAppUsageQuery` 的第一个参数改为 `effectiveMemberId` / `effectiveAppId`。
- 原 `watch(effectiveOrgId, ...)` 里 reset 那两行删掉（reset 由 computed 自动完成）。
- `watch(members, ...)` / `watch(apps, ...)` 里的 auto-select 逻辑保留，条件用 `effectiveMemberId.value === undefined` / `effectiveAppId.value === undefined` 替代 `!selectedMemberId.value`，确保跨组织时也能正确 auto-select。

## 五、测试

### 5.1 单元测试

**`internal/migrations/migrations_test.go`** 自动覆盖 up/down 跑通。

**`internal/integrations/newapi/client_test.go`** 新增 / 调整：

- `TestGetTokenLogsSendsTokenName`：mock 收到的 query 包含 `token_name=app-<uuid>`，**不**包含 `token_id`。
- `TestGetUserQuotaDatesFiltersByUsername`：mock `/api/data/users` 返回 5 个 username 共 58 条 item，传入目标 username，结果只保留该 username 子集；model_name 回填仍能跑通。

**`internal/service/organization_service_test.go`** 新增：

- `TestProvisionNewAPIUserPersistsUsername`：fake store 断言 `SetOrganizationNewAPIUserParams.NewapiUsername.String == org.Code`。

**`internal/worker/handlers/app_initialize_test.go`**（或现有 init 测试文件）新增：

- `TestProvisionAPIKeyPersistsKeyName`：fake store 断言 `SetAppNewAPIKeyParams.NewapiKeyName.String == "app-" + app.ID`。

**`internal/service/usage_service_test.go`** 新增 / 调整：

- `TestGetAppUsageUsesAppNewapiKeyName`：fake store 返回 app（含 `NewapiKeyName = "app-xxx"`），fake client 断言 `LogsQuery.TokenName == "app-xxx"`。
- `TestGetAppUsageFallsBackWhenKeyNameEmpty`：未回填的边界，断言回退到 `"app-" + appID`。
- `TestGetMemberUsageUsesAppNewapiKeyName`：同上但通过 active app。
- `TestGetOrgUsagePassesUsernameToClient`：fake store 返回 org（含 `NewapiUsername = "m8-test"`），fake client 断言收到 `username = "m8-test"`。
- `TestGetOrgUsageReturnsEmptyWhenUsernameEmpty`：未回填的边界，断言不调 client。
- 保留 `TestGetAppUsageReturnsEmptyWhenNoKey`、`TestGetMemberUsageReturnsEmptyWhenNoActiveApp`。

**`web/src/pages/usage/__tests__/UsagePage.spec.ts`** 新增：

- 切换 `effectiveOrgId` 后，若旧 `selectedMemberId` 不在新 org 的 members 列表里，传给 `useMemberUsageQuery` 的 memberRef 应为 `undefined`。
- apps 列表同理。

### 5.2 浏览器端到端验证（交付前必跑）

复用 spec 调研期间的本地数据（M8验证组织下 token 17 / 18 各有若干条 qwen3.5:27b 日志）：

1. **实例 tab**：选 "M8 用户 C 的实例" → Token 总量等于 token 17 真实合计；切到 "M8 用户 E 的实例" → 数据立刻切换为 token 18 的合计，不混入 C 的数据，也不混入 hermes-test / org-2a75a89f 的历史数据。
2. **成员 tab**：分别选 "M8 用户 C" / "M8 用户 E"，同上。
3. **组织 tab**：选 "M8验证组织" → summary 只包含 m8-test 这一个 username 的按日聚合；切到"进度验证组织"数据立刻变化。
4. **跨组织切换**：从"进度验证组织"切到 "M8验证组织"，监控 Network 面板，**不能**出现 `/usage/members/98fecafb-...?org_id=03be7d7e-...`（旧 member ID 配新 org ID）这种组合。
5. **平台 tab**：保持现状，summary 与修复前一致。
6. **未绑定 token 的实例**：仍展示"该实例尚未绑定 new-api key，暂无实例维度用量"。

### 5.3 回归点

- OpenAPI 契约：`/api/v1/apps/{appId}/usage` 的 query 参数 `newapi_key_id` 保留。`make openapi-check` 应当干净。
- 平台 tab 行为不变。
- 短路用例（无 key、无 active app、无 username）行为不变。
- 历史数据已回填，老组织 / 老实例不需要重建即可正确过滤。

## 六、影响 & 兼容

- 数据库 schema：新增两列 + 历史回填，无 NOT NULL 约束（兼容未回填）。
- handler 公共 API（OpenAPI）无变化。
- `LogsQuery.TokenID` 字段移除是 manager 内部 breaking change，仅 usage service 一处调用方，本次同步切换。
- 用量查询不再额外调 `/api/user/<id>`。
- 不影响 new-api 充值 / token 创建等其他链路。

## 七、关联

- `internal/migrations/000021_organizations_apps_newapi_names.{up,down}.sql`（新增）
- `internal/store/queries/organizations.sql` — `SetOrganizationNewAPIUser`
- `internal/store/queries/apps.sql` — `SetAppNewAPIKey`
- `internal/store/sqlc/*` — 由 `make sqlc-generate` 重生成
- `internal/service/organization_service.go` — `provisionNewAPIUser` 写入 username
- `internal/worker/handlers/app_initialize.go` — `provisionAPIKey` 写入 keyName
- `internal/integrations/newapi/client.go` — `LogsQuery` / `GetTokenLogs` / `GetUserQuotaDates`
- `internal/service/usage_service.go` — `GetAppUsage` / `GetMemberUsage` / `GetOrgUsage`
- `web/src/pages/usage/UsagePage.vue` — effective ID computed
- 不动 `GetPlatformUsage` 主体逻辑
