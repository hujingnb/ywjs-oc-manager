# 用量页面"切实例/成员/组织看到的不是自己的数据"修复设计

- 状态：Design
- 日期：2026-05-19
- 范围：manager 后端 usage service + newapi 集成层 + 前端 UsagePage

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
| `/api/log/?token_name=app-<uuid>` | `token_name` | ✅ 精确生效，只返回该 token 的日志 |
| `/api/log/?username=m8-test` | `username` | ✅ 生效 |
| `/api/log/?user_id=14` | `user_id` | ✅ 生效 |
| `/api/data/users?id=14` | `id` | **被静默忽略**，返回所有用户的按日聚合 |
| `/api/data/users?username=m8-test` | `username` | **被静默忽略** |
| `/api/data/` | – | 平台维度聚合，正常工作 |

经过 manager 与浏览器联合验证（`/api/v1/apps/<id>/usage?newapi_key_id=18` 响应包含 token_id ∈ {0, 13, 17, 18} 共 190 条混合记录），与直连结果一致。

**结论**：

- 实例 tab 走 `GetAppUsage` → `GetTokenLogs(LogsQuery{TokenID: keyID})` → `/api/log/?token_id=X` → 过滤失效。
- 成员 tab 走 `GetMemberUsage` → 同样的 `GetTokenLogs` 链路 → 过滤失效。
- 组织 tab 走 `GetOrgUsage` → `GetUserQuotaDates(userID)` → `/api/data/users?id=X` → 过滤失效。
- 平台 tab 走 `GetPlatformUsage` → `/api/data/` → 无过滤需求，正确。

附带发现一个前端竞态：切换组织时 `watch(effectiveOrgId, ...)` 把 `selectedMemberId` 重置为空字符串，但 vue-query 对 `memberOrgRef` 的响应可能先一步发出请求，导致以"新组织 ID + 旧成员 ID"组合的查询发到后端（浏览器 Network 面板里实测到了 98fecafb 这个隶属于"进度验证组织"的 member ID 被带到 e2e-org 的查询里）。被根因掩盖了，但仍是隐患。

## 三、修复策略

不在前端硬性回避问题，也不写"自动判断 new-api 哪个过滤生效"的兼容层。直接把数据过滤改到 new-api 实测能稳定生效的口径：

- 实例 / 成员：用 `token_name` 取代 `token_id`。
- 组织：在 manager 一侧客户端按 `username` 过滤 `/api/data/users` 响应；username 通过 `GET /api/user/<id>` 解析。
- 前端：用 computed 把"选中 ID 必须在当前列表内"作为硬约束，去掉跨组织残留。

## 四、详细方案

### 4.1 newapi 集成层

文件：`internal/integrations/newapi/client.go`、`internal/integrations/newapi/client_test.go`

**`LogsQuery` 结构调整**

```go
type LogsQuery struct {
    UserID    int64
    Username  string
    TokenName string  // 新增：唯一可靠的 token 维度过滤
    ModelName string
    Since     int64
    Until     int64
    Page      int
    PageSize  int
}
```

`TokenID` 字段删除。理由：

- 实测被 new-api 静默忽略，留着会让未来的调用方误以为"按 token id 过滤"可用。
- 当前唯一在用的入口是 usage service，本设计同步切到 `TokenName`。

**`GetTokenLogs` 透传调整**

`values.Set("token_id", ...)` 删除，新增：

```go
if q.TokenName != "" {
    values.Set("token_name", q.TokenName)
}
```

其他字段（`username`、`user_id`、`model_name`、`start_timestamp`、`end_timestamp`、`p`、`page_size`）保持不变。

**新增 `GetUser` helper**

```go
// GetUser 调 admin GET /api/user/<id> 获取 new-api 用户基础信息。
// 用途：org 用量查询前先解析 username，因为 /api/data/users 的 id/username
// 过滤静默失效，manager 必须客户端按 username 过滤响应。
func (c *Client) GetUser(ctx context.Context, id int64) (User, error)
```

返回的 `User` 结构至少包含 `ID`、`Username`、`DisplayName`。错误处理：404 映射到 `ErrNotFound`，401 到 `ErrUnauthorized`，其他保持原样。

**`GetUserQuotaDates` 客户端过滤**

```go
func (c *Client) GetUserQuotaDates(ctx context.Context, userID, since, until int64) ([]QuotaDate, error) {
    // 1. 先解析 username（new-api 的过滤参数无效，必须靠 username 做客户端过滤）
    u, err := c.GetUser(ctx, userID)
    if err != nil {
        return nil, err
    }
    // 2. 调原有 /api/data/users?id=X（保留 id 参数：如果上游修复，
    //    我们少传数据；当前实测无差异）
    items, err := c.fetchQuotaDates(ctx, "/api/data/users?"+values.Encode())
    if err != nil {
        return nil, err
    }
    // 3. 按 username 客户端过滤
    filtered := items[:0]
    for _, it := range items {
        if it.Username == u.Username {
            filtered = append(filtered, it)
        }
    }
    return c.enrichQuotaDatesWithLogModels(ctx, filtered, since, until)
}
```

`enrichQuotaDatesWithLogModels` 不变（它走的是 `/api/log/?username=X`，username 过滤本就生效）。

### 4.2 usage service

文件：`internal/service/usage_service.go`、`internal/service/usage_service_test.go`

**`GetAppUsage`**

- handler 入口签名保留 `newapiKeyID int64`（前端已传，OpenAPI 已发布，避免重新生成）。
- service 内部不再把 `keyID` 传给 newapi client。
- `keyID == 0` 的短路逻辑保留——它代表"该实例尚未绑定 new-api"，仍然返回空 items；不调 new-api。
- 否则改为：

```go
page, err := s.client.GetTokenLogs(ctx, newapi.LogsQuery{
    TokenName: "app-" + appID,
    Since:     opts.Since,
    Until:     opts.Until,
    Page:      opts.Page,
    PageSize:  opts.PageSize,
    ModelName: opts.ModelName,
})
```

**`GetMemberUsage`**

- `GetActiveAppByOwner` 拿到 app 后，沿用 `app.NewapiKeyID.String == ""` 的短路条件（未绑定 token）。
- 否则：

```go
appID := uuidToString(app.ID)
page, err := s.client.GetTokenLogs(ctx, newapi.LogsQuery{
    TokenName: "app-" + appID,
    ...
})
```

**`GetOrgUsage`**

- 不改动 service 层调用：`s.client.GetUserQuotaDates(ctx, userID, since, until)`。
- 受益于 client 层的 username 过滤改造，返回结果只包含该 org 的 user。

### 4.3 前端

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
- `watch(members, ...)` / `watch(apps, ...)` 里的"列表加载后 auto-select 第一项"逻辑保留，但条件用 `effectiveMemberId.value === undefined` / `effectiveAppId.value === undefined` 替代 `!selectedMemberId.value`，确保跨组织时也能正确 auto-select。

## 五、测试

### 5.1 单元测试

**`internal/integrations/newapi/client_test.go`** 新增：

- `TestGetTokenLogsSendsTokenName`：mock 收到的请求 query 包含 `token_name=app-<uuid>`，**不**包含 `token_id`。
- `TestGetUser`：基础 200 / 404 / 401 解析。
- `TestGetUserQuotaDatesFiltersByUsername`：mock `/api/user/<id>` 返回目标 username，`/api/data/users` 返回混合 5 个 username 的 58 条 item，最终结果只保留目标 username 子集，且 model_name 回填仍能跑通。

**`internal/service/usage_service_test.go`** 新增 / 调整：

- `TestGetAppUsageBuildsTokenNameFromAppID`：fake client 断言收到 `TokenName == "app-" + appID`，不依赖 `newapiKeyID` 值。
- `TestGetMemberUsageBuildsTokenNameFromActiveApp`：fake store + fake client，断言 token_name 由 `app.ID` 构造。
- 保留 `TestGetAppUsageReturnsEmptyWhenNoKey`、`TestGetMemberUsageReturnsEmptyWhenNoActiveApp`（短路用例）。

**`web/src/pages/usage/__tests__/UsagePage.spec.ts`** 新增：

- 切换 `effectiveOrgId` 后，若旧 `selectedMemberId` 不在新 org 的 members 列表里，`useMemberUsageQuery` 拿到的 memberRef 应为 `undefined`（验证不会带旧 ID 发查询）。
- apps 列表同理。

### 5.2 浏览器端到端验证（交付前必跑）

复用 spec 调研期间造的本地数据（M8验证组织下 token 17 / 18 各有若干条 qwen3.5:27b 日志）：

1. **实例 tab**：选 "M8 用户 C 的实例" → Token 总量等于 token 17 真实合计；切到 "M8 用户 E 的实例" → 数据立刻切换为 token 18 的合计，不混入 C 的数据，也不混入 hermes-test / org-2a75a89f 的历史数据。
2. **成员 tab**：分别选 "M8 用户 C" / "M8 用户 E"，同上。
3. **组织 tab**：选 "M8验证组织" → summary 只包含 m8-test 这一个 username 的按日聚合；切到"进度验证组织"数据立刻变化。
4. **跨组织切换**：从"进度验证组织"切到 "M8验证组织"，监控 Network 面板，**不能**出现 `/usage/members/98fecafb-...?org_id=03be7d7e-...`（即旧 member ID 配新 org ID）这种组合。
5. **平台 tab**：保持现状，summary 与修复前一致。
6. **未绑定 token 的实例**：仍展示"该实例尚未绑定 new-api key，暂无实例维度用量"。

### 5.3 回归点

- OpenAPI 契约：`/api/v1/apps/{appId}/usage` 的 query 参数 `newapi_key_id` 保留。`make openapi-check` 应当干净。
- 平台 tab 行为不变。
- 短路用例（无 key、无 active app）行为不变。

## 六、影响 & 兼容

- 数据库 schema 无变化。
- handler 公共 API（OpenAPI）无变化。
- newapi client `LogsQuery.TokenID` 字段移除是 manager 内部 breaking change，仅 usage service 一处调用方，本次同步切换。
- 增加一次 `GET /api/user/<id>` 调用（仅 org / 平台 tab 周期 8s 一次轮询时叠加），属轻量请求，不缓存。
- 不影响 new-api 充值 / token 创建等其他链路。

## 七、关联

- `internal/integrations/newapi/client.go` 中 `LogsQuery` / `GetTokenLogs` / `GetUserQuotaDates` / 新增 `GetUser`
- `internal/service/usage_service.go` 中 `GetAppUsage` / `GetMemberUsage`
- `web/src/pages/usage/UsagePage.vue` 中选中 ID 的 effective 化
- 不动 `internal/service/usage_service.go` 的 `GetOrgUsage` / `GetPlatformUsage` 主体逻辑
