# AICC Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐 AICC 原需求文档中尚未完成的二维码、会话续接、地域、统计筛选和公开端安全治理能力。

**Architecture:** 采用运营配置中心化方案：新增 `aicc_agent_settings` 和 `aicc_blocked_visitors` 承载机器人级运营策略，公开端消息发送统一经过安全网关校验，管理端会话和统计接口在现有 AICC handler/service/sqlc 边界内扩展。前端继续使用现有 `web/src/pages/aicc` 页面体系，不新建独立子应用。

**Tech Stack:** Go + Gin + sqlc + MySQL migrations, Vue 3 + TypeScript + TanStack Query + Vite, Playwright E2E, OpenAPI swag generation.

---

## File Structure

- Create: `internal/migrations/000030_aicc_settings.up.sql`
  - 新增 AICC 运营配置表和封禁访客表。
- Create: `internal/migrations/000030_aicc_settings.down.sql`
  - 回滚新增表。
- Modify: `internal/migrations/migrations_test.go`
  - 断言新增表、约束、索引和外键存在。
- Modify: `internal/store/queries/aicc.sql`
  - 增加 settings、blocked visitors、会话筛选、统计聚合、消息计数查询。
- Regenerate: `internal/store/sqlc/aicc.sql.go`, `internal/store/sqlc/models.go`, `internal/store/sqlc/querier.go`
  - 由 sqlc 生成。
- Modify: `internal/service/aicc_types.go`
  - 增加 settings、analytics filters、trend/region results、session region/message count 字段。
- Modify: `internal/service/aicc_service.go`
  - 管理端 settings 读写、会话筛选、统计聚合。
- Modify: `internal/service/aicc_service_test.go`
  - 管理端 settings、筛选、统计单测。
- Modify: `internal/service/aicc_public_service.go`
  - 公开端恢复会话、安全校验、地域解析。
- Modify: `internal/service/aicc_public_service_test.go`
  - 公开端续接、敏感词、消息上限、封禁单测。
- Modify: `internal/api/handlers/dto.go`
  - 新增 settings request，扩展 session request。
- Modify: `internal/api/handlers/aicc.go`
  - 新增 settings 路由，扩展 analytics 和 sessions 查询参数。
- Modify: `internal/api/handlers/aicc_test.go`
  - handler 路由和参数绑定测试。
- Modify: `internal/api/handlers/public_aicc.go`
  - 创建会话接收 `session_token`，公开错误码映射。
- Modify: `internal/api/handlers/public_aicc_test.go`
  - 公开 handler 续接入参和错误码测试。
- Regenerate: `openapi/openapi.yaml`, `web/src/api/generated.ts`
  - 由 `make openapi-gen` 和 `make web-types-gen` 生成。
- Modify: `web/src/domain/aicc.ts`
  - 增加 settings、analytics filters、trend、region、session token restore 类型。
- Modify: `web/src/api/hooks/useAICC.ts`
  - 增加 settings hooks，扩展 sessions/analytics query，支持 public session restore。
- Modify: `web/src/pages/aicc/AICCManagerPage.vue`
  - 设置页增加运营配置区和二维码入口。
- Modify: `web/src/pages/aicc/AICCAnalyticsPage.vue`
  - 增加时间范围、日/周粒度、趋势、地域、未解决率展示。
- Modify: `web/src/pages/aicc/AICCSessionsPage.vue`
  - 增加时间、地域、解决状态筛选和列展示。
- Modify: `web/src/pages/aicc/PublicAICCChatPage.vue`
  - localStorage 保存/恢复 session token，展示安全拦截提示。
- Modify: `web/tests/e2e/aicc.spec.ts`
  - 使用 `ocm.localhost` 验证后台配置、二维码、筛选、公开端续接。

## Task 1: 数据模型与 sqlc 查询

**Files:**
- Create: `internal/migrations/000030_aicc_settings.up.sql`
- Create: `internal/migrations/000030_aicc_settings.down.sql`
- Modify: `internal/migrations/migrations_test.go`
- Modify: `internal/store/queries/aicc.sql`
- Regenerate: `internal/store/sqlc/aicc.sql.go`
- Regenerate: `internal/store/sqlc/models.go`
- Regenerate: `internal/store/sqlc/querier.go`

- [ ] **Step 1: 写 migration 失败测试**

在 `internal/migrations/migrations_test.go` 增加测试，使用相邻中文注释说明场景：

```go
// TestAICCSettingsMigrationContainsOperationalTables 覆盖 AICC 运营配置表：
// 新增表必须按 agent 维度保存安全与续接策略，并用访客哈希记录封禁，避免保存明文 IP。
func TestAICCSettingsMigrationContainsOperationalTables(t *testing.T) {
	upBytes, err := FS.ReadFile("000030_aicc_settings.up.sql")
	require.NoError(t, err)
	up := string(upBytes)

	assert.Contains(t, up, "CREATE TABLE aicc_agent_settings")
	assert.Contains(t, up, "agent_id CHAR(36) NOT NULL")
	assert.Contains(t, up, "message_limit_per_session INT NOT NULL DEFAULT 100")
	assert.Contains(t, up, "sensitive_words_json JSON NULL")
	assert.Contains(t, up, "session_resume_ttl_minutes INT NOT NULL DEFAULT 30")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_agent_settings_agent (agent_id)")
	assert.Contains(t, up, "CREATE TABLE aicc_blocked_visitors")
	assert.Contains(t, up, "visitor_hash VARCHAR(128) NOT NULL")
	assert.Contains(t, up, "expires_at DATETIME NOT NULL")
	assert.Contains(t, up, "KEY idx_aicc_blocked_visitors_lookup (agent_id, visitor_hash, expires_at)")
	assert.NotContains(t, up, "remote_ip")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run:

```bash
go test ./internal/migrations -run TestAICCSettingsMigrationContainsOperationalTables -count=1
```

Expected: FAIL，报 `000030_aicc_settings.up.sql` 文件不存在或断言缺失。

- [ ] **Step 3: 新增 migration**

`internal/migrations/000030_aicc_settings.up.sql` 写入：

```sql
CREATE TABLE aicc_agent_settings (
    agent_id CHAR(36) NOT NULL PRIMARY KEY,
    message_limit_per_session INT NOT NULL DEFAULT 100,
    sensitive_words_json JSON NULL,
    blocked_visitor_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    blocked_visitor_threshold_json JSON NULL,
    session_resume_ttl_minutes INT NOT NULL DEFAULT 30,
    analytics_config_json JSON NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT aicc_agent_settings_message_limit_check CHECK (message_limit_per_session BETWEEN 1 AND 1000),
    CONSTRAINT aicc_agent_settings_resume_ttl_check CHECK (session_resume_ttl_minutes BETWEEN 1 AND 1440),
    CONSTRAINT fk_aicc_agent_settings_agent FOREIGN KEY (agent_id) REFERENCES aicc_agents(id) ON DELETE CASCADE,
    UNIQUE KEY uk_aicc_agent_settings_agent (agent_id)
);

CREATE TABLE aicc_blocked_visitors (
    id CHAR(36) PRIMARY KEY,
    agent_id CHAR(36) NOT NULL,
    org_id CHAR(36) NOT NULL,
    visitor_hash VARCHAR(128) NOT NULL,
    reason VARCHAR(255) NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_aicc_blocked_visitors_agent_org FOREIGN KEY (agent_id, org_id) REFERENCES aicc_agents(id, org_id) ON DELETE CASCADE,
    CONSTRAINT fk_aicc_blocked_visitors_org FOREIGN KEY (org_id) REFERENCES organizations(id),
    KEY idx_aicc_blocked_visitors_lookup (agent_id, visitor_hash, expires_at),
    KEY idx_aicc_blocked_visitors_agent_created (agent_id, created_at DESC, id DESC)
);
```

`internal/migrations/000030_aicc_settings.down.sql` 写入：

```sql
DROP TABLE IF EXISTS aicc_blocked_visitors;
DROP TABLE IF EXISTS aicc_agent_settings;
```

- [ ] **Step 4: 增加 sqlc 查询**

在 `internal/store/queries/aicc.sql` 追加：

```sql
-- name: GetAICCAgentSettings :one
SELECT *
FROM aicc_agent_settings
WHERE agent_id = ?;

-- name: UpsertAICCAgentSettings :exec
INSERT INTO aicc_agent_settings (
    agent_id, message_limit_per_session, sensitive_words_json,
    blocked_visitor_enabled, blocked_visitor_threshold_json,
    session_resume_ttl_minutes, analytics_config_json
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    message_limit_per_session = VALUES(message_limit_per_session),
    sensitive_words_json = VALUES(sensitive_words_json),
    blocked_visitor_enabled = VALUES(blocked_visitor_enabled),
    blocked_visitor_threshold_json = VALUES(blocked_visitor_threshold_json),
    session_resume_ttl_minutes = VALUES(session_resume_ttl_minutes),
    analytics_config_json = VALUES(analytics_config_json),
    updated_at = now();

-- name: ListAICCBlockedVisitorsByAgent :many
SELECT *
FROM aicc_blocked_visitors
WHERE agent_id = ?
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: GetActiveAICCBlockedVisitor :one
SELECT *
FROM aicc_blocked_visitors
WHERE agent_id = ? AND visitor_hash = ? AND expires_at > now()
ORDER BY expires_at DESC, id DESC
LIMIT 1;

-- name: UpsertAICCBlockedVisitor :exec
INSERT INTO aicc_blocked_visitors (
    id, agent_id, org_id, visitor_hash, reason, expires_at
) VALUES (?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    reason = VALUES(reason),
    expires_at = VALUES(expires_at),
    updated_at = now();

-- name: DeleteAICCBlockedVisitor :execrows
DELETE FROM aicc_blocked_visitors
WHERE id = ? AND agent_id = ?;

-- name: CountAICCVisitorMessagesBySession :one
SELECT COUNT(*)
FROM aicc_messages
WHERE session_id = ? AND direction = 'visitor';
```

扩展已有 `ListAICCSessionsByAgent` 查询，加入时间和地域过滤：

```sql
  AND (sqlc.narg(region) IS NULL OR region = sqlc.narg(region))
  AND (sqlc.narg(start_at) IS NULL OR created_at >= sqlc.narg(start_at))
  AND (sqlc.narg(end_at) IS NULL OR created_at < sqlc.narg(end_at))
```

- [ ] **Step 5: 生成 sqlc**

Run:

```bash
make sqlc-gen
```

Expected: `internal/store/sqlc/aicc.sql.go`、`internal/store/sqlc/models.go`、`internal/store/sqlc/querier.go` 更新。

- [ ] **Step 6: 运行 migration 测试**

Run:

```bash
go test ./internal/migrations -count=1
```

Expected: PASS。

- [ ] **Step 7: 提交数据模型阶段**

```bash
git add internal/migrations/000030_aicc_settings.up.sql internal/migrations/000030_aicc_settings.down.sql internal/migrations/migrations_test.go internal/store/queries/aicc.sql internal/store/sqlc/aicc.sql.go internal/store/sqlc/models.go internal/store/sqlc/querier.go
git commit -m "feat(aicc): 增加运营配置数据模型" -m "新增 AICC 机器人运营配置和封禁访客表，并补充 settings、封禁查询、会话筛选与消息计数 sqlc 查询。\n\n同步 migration 测试和 sqlc 生成产物。"
```

## Task 2: 管理端 settings API

**Files:**
- Modify: `internal/service/aicc_types.go`
- Modify: `internal/service/aicc_service.go`
- Modify: `internal/service/aicc_service_test.go`
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/aicc.go`
- Modify: `internal/api/handlers/aicc_test.go`
- Regenerate: `openapi/openapi.yaml`
- Regenerate: `web/src/api/generated.ts`

- [ ] **Step 1: 写 service settings 测试**

在 `internal/service/aicc_service_test.go` 增加：

```go
// TestAICCSettingsDefaults 覆盖旧机器人无 settings 行的兼容路径：
// 后端必须返回默认运营配置，避免历史机器人打开设置页时报错。
func TestAICCSettingsDefaults(t *testing.T) {
	store := &fakeAICCStore{
		agents: map[string]sqlc.AiccAgent{
			"agent-1": {ID: "agent-1", OrgID: "org-1"},
		},
		getSettingsErr: sql.ErrNoRows,
	}
	svc := NewAICCService(store, nil)

	result, err := svc.GetAgentSettings(context.Background(), aiccOrgAdmin(), "agent-1")
	require.NoError(t, err)

	assert.Equal(t, "agent-1", result.AgentID)
	assert.Equal(t, int32(100), result.MessageLimitPerSession)
	assert.Equal(t, int32(30), result.SessionResumeTTLMinutes)
	assert.True(t, result.BlockedVisitorEnabled)
	assert.Empty(t, result.SensitiveWords)
}

// TestAICCSettingsUpdateNormalizesInput 覆盖设置保存：
// 敏感词需要去空白、去空项、去重，消息上限和续接时间必须落在业务允许范围内。
func TestAICCSettingsUpdateNormalizesInput(t *testing.T) {
	store := &fakeAICCStore{
		agents: map[string]sqlc.AiccAgent{
			"agent-1": {ID: "agent-1", OrgID: "org-1"},
		},
	}
	svc := NewAICCService(store, nil)

	result, err := svc.UpdateAgentSettings(context.Background(), aiccOrgAdmin(), "agent-1", AICCAgentSettingsInput{
		MessageLimitPerSession: 50,
		SensitiveWords:         []string{"  违禁词  ", "", "违禁词"},
		BlockedVisitorEnabled: true,
		SessionResumeTTLMinutes: 60,
	})
	require.NoError(t, err)

	assert.Equal(t, int32(50), result.MessageLimitPerSession)
	assert.Equal(t, []string{"违禁词"}, result.SensitiveWords)
	assert.Equal(t, int32(60), result.SessionResumeTTLMinutes)
	require.NotNil(t, store.upsertSettings)
	assert.JSONEq(t, `["违禁词"]`, store.upsertSettings.SensitiveWordsJson.String)
}
```

- [ ] **Step 2: 运行 service 测试确认失败**

```bash
go test ./internal/service -run 'TestAICCSettings(Default|Update)' -count=1
```

Expected: FAIL，提示类型或方法未定义。

- [ ] **Step 3: 增加 settings 类型**

在 `internal/service/aicc_types.go` 增加：

```go
// AICCAgentSettingsInput 是企业管理员保存 AICC 运营配置的入参。
type AICCAgentSettingsInput struct {
	// MessageLimitPerSession 限制单个公开会话最多发送多少条访客消息。
	MessageLimitPerSession int32 `json:"message_limit_per_session"`
	// SensitiveWords 是公开端发送前拦截的敏感词列表，保存前会 trim、去空和去重。
	SensitiveWords []string `json:"sensitive_words"`
	// BlockedVisitorEnabled 控制是否启用访客封禁检查。
	BlockedVisitorEnabled bool `json:"blocked_visitor_enabled"`
	// BlockedVisitorThresholdJSON 保留异常访客自动封禁阈值配置。
	BlockedVisitorThresholdJSON []byte `json:"blocked_visitor_threshold_json,omitempty"`
	// SessionResumeTTLMinutes 控制公开端刷新续接有效期。
	SessionResumeTTLMinutes int32 `json:"session_resume_ttl_minutes"`
}

// AICCAgentSettingsResult 是管理端回显的 AICC 运营配置。
type AICCAgentSettingsResult struct {
	AgentID                  string   `json:"agent_id"`
	MessageLimitPerSession   int32    `json:"message_limit_per_session"`
	SensitiveWords           []string `json:"sensitive_words"`
	BlockedVisitorEnabled    bool     `json:"blocked_visitor_enabled"`
	SessionResumeTTLMinutes  int32    `json:"session_resume_ttl_minutes"`
	BlockedVisitorCount      int64    `json:"blocked_visitor_count"`
}
```

- [ ] **Step 4: 实现 service 方法**

在 `internal/service/aicc_service.go` 增加 `GetAgentSettings` 和 `UpdateAgentSettings`，要点：

```go
const (
	aiccDefaultMessageLimitPerSession int32 = 100
	aiccDefaultSessionResumeTTLMin    int32 = 30
	aiccMaxMessageLimitPerSession     int32 = 1000
	aiccMaxSessionResumeTTLMin        int32 = 1440
)

func normalizeAICCSensitiveWords(words []string) []string {
	seen := make(map[string]struct{}, len(words))
	normalized := make([]string, 0, len(words))
	for _, word := range words {
		value := strings.TrimSpace(word)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized
}
```

权限必须使用 `auth.CanManageAICCAgent(principal, agent.OrgID)`。无 settings 行时返回默认值，不创建数据库行。

- [ ] **Step 5: 写 handler 测试**

在 `internal/api/handlers/aicc_test.go` 增加路由用例：

```go
// TestAICCHandlerSettingsRoutes 覆盖 AICC 运营配置路由：
// handler 必须绑定 settings 请求体并把 agentId 透传给 service。
func TestAICCHandlerSettingsRoutes(t *testing.T) {
	svc := &aiccServiceStub{
		settingsResult: service.AICCAgentSettingsResult{
			AgentID: "agent-1", MessageLimitPerSession: 80, SensitiveWords: []string{"违禁词"},
			BlockedVisitorEnabled: true, SessionResumeTTLMinutes: 45,
		},
	}
	router := newAICCTestRouter(t, svc)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/aicc/agents/agent-1/settings", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	assert.Equal(t, http.StatusOK, getRec.Code)
	assert.Contains(t, getRec.Body.String(), `"message_limit_per_session":80`)

	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/aicc/agents/agent-1/settings", bytes.NewBufferString(`{"message_limit_per_session":80,"sensitive_words":["违禁词"],"blocked_visitor_enabled":true,"session_resume_ttl_minutes":45}`))
	putRec := httptest.NewRecorder()
	router.ServeHTTP(putRec, putReq)
	assert.Equal(t, http.StatusOK, putRec.Code)
	assert.Equal(t, int32(80), svc.lastSettings.MessageLimitPerSession)
	assert.Equal(t, []string{"违禁词"}, svc.lastSettings.SensitiveWords)
}
```

- [ ] **Step 6: 实现 handler 和 dto**

在 `internal/api/handlers/dto.go` 增加：

```go
// UpdateAICCAgentSettingsRequest 是 AICC 运营配置保存请求。
type UpdateAICCAgentSettingsRequest struct {
	MessageLimitPerSession  int32    `json:"message_limit_per_session" binding:"required,min=1,max=1000"`
	SensitiveWords          []string `json:"sensitive_words"`
	BlockedVisitorEnabled   bool     `json:"blocked_visitor_enabled"`
	SessionResumeTTLMinutes int32    `json:"session_resume_ttl_minutes" binding:"required,min=1,max=1440"`
}
```

在 `internal/api/handlers/aicc.go`：

- `aiccService` interface 增加 `GetAgentSettings`、`UpdateAgentSettings`。
- `RegisterAICCRoutes` 增加：

```go
group.GET("/agents/:agentId/settings", handler.GetAgentSettings)
group.PUT("/agents/:agentId/settings", handler.UpdateAgentSettings)
```

返回 JSON key 使用 `settings`。

- [ ] **Step 7: 生成 OpenAPI 和前端类型**

```bash
make openapi-gen
make web-types-gen
make openapi-check
```

Expected: OpenAPI 校验通过。

- [ ] **Step 8: 运行相关测试并提交**

```bash
go test ./internal/service ./internal/api/handlers -run 'AICC.*Settings|TestAICCHandlerSettingsRoutes' -count=1
git add internal/service/aicc_types.go internal/service/aicc_service.go internal/service/aicc_service_test.go internal/api/handlers/dto.go internal/api/handlers/aicc.go internal/api/handlers/aicc_test.go openapi/openapi.yaml web/src/api/generated.ts
git commit -m "feat(aicc): 增加运营配置接口" -m "为 AICC 智能体增加运营配置读写能力，覆盖消息上限、敏感词、封禁开关和会话续接时长。\n\n同步 handler 测试、service 测试、OpenAPI 和前端生成类型。"
```

## Task 3: 公开端安全校验与会话续接

**Files:**
- Modify: `internal/service/aicc_public_service.go`
- Modify: `internal/service/aicc_public_service_test.go`
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/public_aicc.go`
- Modify: `internal/api/handlers/public_aicc_test.go`
- Regenerate: `openapi/openapi.yaml`
- Regenerate: `web/src/api/generated.ts`

- [ ] **Step 1: 写公开 service 测试**

在 `internal/service/aicc_public_service_test.go` 增加：

```go
// TestAICCPublicCreateSessionRestoresExistingSession 覆盖刷新续接：
// 访客传入仍有效的 session token 时，服务端必须返回原会话，不创建新会话。
func TestAICCPublicCreateSessionRestoresExistingSession(t *testing.T) {
	store := &fakeAICCPublicStore{
		agent: sqlc.AiccAgent{ID: "agent-1", OrgID: "org-1", PublicToken: "pub", Status: domain.AICCAgentStatusActive},
		session: sqlc.AiccSession{ID: "session-1", AgentID: "agent-1", OrgID: "org-1", SessionToken: "tok", ExpiresAt: aiccPublicTestNow.Add(time.Hour), PrivacyNoticeShown: true},
	}
	svc := NewAICCPublicService(store, &fakeAICCHermesChat{})
	svc.now = func() time.Time { return aiccPublicTestNow }

	result, err := svc.CreateSession(context.Background(), "pub", AICCPublicSessionInput{SessionToken: "tok"})
	require.NoError(t, err)

	assert.Equal(t, "tok", result.SessionToken)
	assert.True(t, result.Restored)
	assert.Equal(t, 0, store.createdSessionCount)
}

// TestAICCPublicSendMessageRejectsSensitiveWord 覆盖敏感词拦截：
// 命中配置后不能调用 Hermes，避免违规内容继续消耗模型费用。
func TestAICCPublicSendMessageRejectsSensitiveWord(t *testing.T) {
	chat := &fakeAICCHermesChat{}
	store := &fakeAICCPublicStore{
		agent: sqlc.AiccAgent{ID: "agent-1", OrgID: "org-1", AppID: "app-1", Status: domain.AICCAgentStatusActive},
		session: sqlc.AiccSession{ID: "session-1", AgentID: "agent-1", OrgID: "org-1", SessionToken: "tok", ExpiresAt: aiccPublicTestNow.Add(time.Hour), PrivacyNoticeShown: true},
		settings: sqlc.AiccAgentSetting{AgentID: "agent-1", MessageLimitPerSession: 100, SensitiveWordsJson: null.StringFrom(`["违禁词"]`), BlockedVisitorEnabled: true, SessionResumeTtlMinutes: 30},
	}
	svc := NewAICCPublicService(store, chat)
	svc.now = func() time.Time { return aiccPublicTestNow }

	_, err := svc.SendMessage(context.Background(), AICCPublicMessageInput{SessionToken: "tok", Text: "包含违禁词"})
	require.ErrorIs(t, err, ErrAICCSensitiveWord)
	assert.Empty(t, chat.text)
}

// TestAICCPublicSendMessageRejectsMessageLimit 覆盖单会话消息数上限：
// 达到上限后拒绝继续发送，且不调用 Hermes。
func TestAICCPublicSendMessageRejectsMessageLimit(t *testing.T) {
	chat := &fakeAICCHermesChat{}
	store := &fakeAICCPublicStore{
		agent: sqlc.AiccAgent{ID: "agent-1", OrgID: "org-1", AppID: "app-1", Status: domain.AICCAgentStatusActive},
		session: sqlc.AiccSession{ID: "session-1", AgentID: "agent-1", OrgID: "org-1", SessionToken: "tok", ExpiresAt: aiccPublicTestNow.Add(time.Hour), PrivacyNoticeShown: true},
		settings: sqlc.AiccAgentSetting{AgentID: "agent-1", MessageLimitPerSession: 1, BlockedVisitorEnabled: true, SessionResumeTtlMinutes: 30},
		visitorMessageCount: 1,
	}
	svc := NewAICCPublicService(store, chat)
	svc.now = func() time.Time { return aiccPublicTestNow }

	_, err := svc.SendMessage(context.Background(), AICCPublicMessageInput{SessionToken: "tok", Text: "还能问吗"})
	require.ErrorIs(t, err, ErrAICCMessageLimitExceeded)
	assert.Empty(t, chat.text)
}
```

- [ ] **Step 2: 运行公开 service 测试确认失败**

```bash
go test ./internal/service -run 'TestAICCPublic(CreateSessionRestores|SendMessageRejects)' -count=1
```

Expected: FAIL，提示 `SessionToken`、`Restored`、settings store 方法或 sentinel error 未定义。

- [ ] **Step 3: 扩展公开类型和 store 接口**

在 `AICCPublicStore` 增加：

```go
GetAICCAgentSettings(ctx context.Context, agentID string) (sqlc.AiccAgentSetting, error)
CountAICCVisitorMessagesBySession(ctx context.Context, sessionID string) (int64, error)
GetActiveAICCBlockedVisitor(ctx context.Context, arg sqlc.GetActiveAICCBlockedVisitorParams) (sqlc.AiccBlockedVisitor, error)
```

在 `AICCPublicSessionInput` 增加：

```go
// SessionToken 是访客端刷新页面时带回的短期会话 token，用于恢复未过期会话。
SessionToken string
```

在 `AICCPublicSessionResult` 增加：

```go
Restored bool `json:"restored"`
```

新增 sentinel error：

```go
var (
	ErrAICCSensitiveWord         = errors.New("aicc sensitive word")
	ErrAICCMessageLimitExceeded  = errors.New("aicc message limit exceeded")
	ErrAICCVisitorBlocked        = errors.New("aicc visitor blocked")
)
```

- [ ] **Step 4: 实现恢复和安全校验**

在 `CreateSession` 开头加入恢复逻辑：

```go
if strings.TrimSpace(input.SessionToken) != "" {
	session, err := s.store.GetAICCSessionByToken(ctx, strings.TrimSpace(input.SessionToken))
	if err == nil && session.AgentID == agent.ID {
		return AICCPublicSessionResult{
			SessionToken: session.SessionToken,
			PrivacyMode: agent.PrivacyMode,
			PrivacyText: agent.PrivacyText.String,
			PrivacyNoticeShown: session.PrivacyNoticeShown,
			Restored: true,
		}, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AICCPublicSessionResult{}, fmt.Errorf("恢复 AICC 会话失败: %w", err)
	}
}
```

在 `SendMessage` 调用 Hermes 前加入：

```go
settings := defaultAICCPublicSettings()
storedSettings, err := s.store.GetAICCAgentSettings(ctx, session.AgentID)
if err == nil {
	settings = publicSettingsFromSQLC(storedSettings)
} else if !errors.Is(err, sql.ErrNoRows) {
	return AICCPublicMessageResult{}, fmt.Errorf("读取 AICC 运营配置失败: %w", err)
}
if settings.BlockedVisitorEnabled {
	if err := s.ensureVisitorNotBlocked(ctx, session); err != nil {
		return AICCPublicMessageResult{}, err
	}
}
if err := s.ensureMessageLimit(ctx, session.ID, settings.MessageLimitPerSession); err != nil {
	return AICCPublicMessageResult{}, err
}
if containsAICCSensitiveWord(input.Text, settings.SensitiveWords) {
	return AICCPublicMessageResult{}, ErrAICCSensitiveWord
}
```

地域解析先用本地保守实现：内网、环回和解析失败返回空字符串；公网 IP 返回空字符串并保留接口形态 `resolveAICCRegion(remoteIP string) string`，避免引入外部网络依赖。

- [ ] **Step 5: 写 handler 测试并实现 DTO 绑定**

`internal/api/handlers/public_aicc_test.go` 增加：

```go
// TestPublicAICCHandlerCreateSessionPassesSessionToken 覆盖刷新续接：
// 公开创建会话接口必须把访客端保存的 token 透传给 service。
func TestPublicAICCHandlerCreateSessionPassesSessionToken(t *testing.T) {
	svc := &publicAICCServiceStub{sessionResult: service.AICCPublicSessionResult{SessionToken: "tok", Restored: true}}
	router := newPublicAICCTestRouter(t, svc)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/agents/pub/sessions", bytes.NewBufferString(`{"channel":"web_link","session_token":"tok"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusCreated, recorder.Code)
	assert.Equal(t, "tok", svc.lastSessionInput.SessionToken)
	assert.Contains(t, recorder.Body.String(), `"restored":true`)
}
```

`CreateAICCSessionRequest` 增加：

```go
SessionToken string `json:"session_token,omitempty"`
```

`CreateSession` handler 透传 `SessionToken: req.SessionToken`。

- [ ] **Step 6: 映射公开错误码**

在 `writePublicAICCError` 增加：

```go
case errors.Is(err, service.ErrAICCSensitiveWord):
	apierror.JSON(c, http.StatusBadRequest, "AICC_SENSITIVE_WORD", "消息包含暂不支持发送的内容")
case errors.Is(err, service.ErrAICCMessageLimitExceeded):
	apierror.JSON(c, http.StatusTooManyRequests, "AICC_MESSAGE_LIMIT_EXCEEDED", "本次会话消息数量已达上限")
case errors.Is(err, service.ErrAICCVisitorBlocked):
	apierror.JSON(c, http.StatusForbidden, "AICC_VISITOR_BLOCKED", "当前访客暂不能继续咨询")
```

- [ ] **Step 7: 生成契约、测试并提交**

```bash
make openapi-gen
make web-types-gen
make openapi-check
go test ./internal/service ./internal/api/handlers -run 'AICCPublic|PublicAICC' -count=1
git add internal/service/aicc_public_service.go internal/service/aicc_public_service_test.go internal/api/handlers/dto.go internal/api/handlers/public_aicc.go internal/api/handlers/public_aicc_test.go openapi/openapi.yaml web/src/api/generated.ts
git commit -m "feat(aicc): 补齐公开会话安全与续接" -m "公开端创建会话支持传入 session token 恢复未过期会话。\n\n消息发送前增加敏感词、单会话消息上限和访客封禁校验，拦截失败不调用 Hermes，并同步公开错误码和 OpenAPI。"
```

## Task 4: 会话筛选与运营统计

**Files:**
- Modify: `internal/service/aicc_types.go`
- Modify: `internal/service/aicc_service.go`
- Modify: `internal/service/aicc_service_test.go`
- Modify: `internal/api/handlers/aicc.go`
- Modify: `internal/api/handlers/aicc_test.go`
- Modify: `internal/store/queries/aicc.sql`
- Regenerate: `internal/store/sqlc/aicc.sql.go`
- Regenerate: `openapi/openapi.yaml`
- Regenerate: `web/src/api/generated.ts`

- [ ] **Step 1: 写 handler 参数绑定测试**

在 `internal/api/handlers/aicc_test.go` 增加：

```go
// TestAICCHandlerSessionFiltersPassTimeAndRegion 覆盖会话列表筛选：
// handler 必须透传时间范围、地域和解决状态，供后台运营筛选使用。
func TestAICCHandlerSessionFiltersPassTimeAndRegion(t *testing.T) {
	svc := &aiccServiceStub{sessionsResult: []service.AICCSessionResult{{ID: "session-1", AgentID: "agent-1"}}}
	router := newAICCTestRouter(t, svc)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/aicc/agents/agent-1/sessions?start_at=2026-07-01T00:00:00Z&end_at=2026-07-08T00:00:00Z&region=上海&resolution_status=unresolved", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "上海", svc.lastSessions.Region)
	assert.Equal(t, "unresolved", svc.lastSessions.ResolutionStatus)
	assert.Equal(t, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), svc.lastSessions.StartAt)
	assert.Equal(t, time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC), svc.lastSessions.EndAt)
}
```

- [ ] **Step 2: 写 analytics service 测试**

```go
// TestAICCAnalyticsWithRangeAndBucket 覆盖统计看板：
// service 必须把时间范围和 day/week 粒度传给 store，并返回趋势、地域和未解决率。
func TestAICCAnalyticsWithRangeAndBucket(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	store := &fakeAICCStore{
		analyticsTrend: []AICCTrendBucket{{Bucket: "2026-07-01", Count: 3}},
		analyticsRegions: []AICCTopItemResult{{Label: "上海", Count: 2}},
		analyticsSummary: AICCAnalyticsSummary{Sessions: 5, Resolved: 2, Unresolved: 1, Unknown: 2},
	}
	svc := NewAICCService(store, nil)

	result, err := svc.Analytics(context.Background(), aiccOrgAdmin(), AICCAnalyticsOptions{
		OrgID: "org-1", StartAt: start, EndAt: end, Bucket: "day",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(5), result.TotalSessions)
	assert.Equal(t, float64(1)/float64(3), result.UnresolvedRate)
	assert.Equal(t, []AICCTrendBucket{{Bucket: "2026-07-01", Count: 3}}, result.SessionTrend)
	assert.Equal(t, "上海", result.Regions[0].Label)
}
```

- [ ] **Step 3: 扩展 service 类型**

在 `internal/service/aicc_types.go`：

```go
type AICCAnalyticsOptions struct {
	OrgID   string
	AgentID string
	StartAt time.Time
	EndAt   time.Time
	Bucket  string
}

type AICCTrendBucket struct {
	Bucket string `json:"bucket"`
	Count  int64  `json:"count"`
}
```

`AICCAnalyticsResult` 增加：

```go
TotalSessions      int64               `json:"total_sessions"`
UnknownSessions    int64               `json:"unknown_sessions"`
UnresolvedRate     float64             `json:"unresolved_rate"`
SessionTrend       []AICCTrendBucket   `json:"session_trend"`
Regions            []AICCTopItemResult `json:"regions"`
```

`AICCSessionListOptions` 增加 `Region string`、`StartAt time.Time`、`EndAt time.Time`。

`AICCSessionResult` 增加 `Region string`、`MessageCount int64`。

- [ ] **Step 4: 增加统计 sqlc 查询**

在 `internal/store/queries/aicc.sql` 追加：

```sql
-- name: CountAICCSessionsByStatusInRange :one
SELECT
    COUNT(*) AS total_sessions,
    SUM(CASE WHEN resolution_status = 'resolved' THEN 1 ELSE 0 END) AS resolved_sessions,
    SUM(CASE WHEN resolution_status = 'unresolved' THEN 1 ELSE 0 END) AS unresolved_sessions,
    SUM(CASE WHEN resolution_status = 'unknown' THEN 1 ELSE 0 END) AS unknown_sessions
FROM aicc_sessions
WHERE org_id = ?
  AND (sqlc.narg(agent_id) IS NULL OR agent_id = sqlc.narg(agent_id))
  AND created_at >= ?
  AND created_at < ?;

-- name: ListAICCSessionTrendByDay :many
SELECT DATE(created_at) AS bucket, COUNT(*) AS count
FROM aicc_sessions
WHERE org_id = ?
  AND (sqlc.narg(agent_id) IS NULL OR agent_id = sqlc.narg(agent_id))
  AND created_at >= ?
  AND created_at < ?
GROUP BY DATE(created_at)
ORDER BY bucket ASC;

-- name: ListAICCSessionTrendByWeek :many
SELECT DATE_FORMAT(created_at, '%x-W%v') AS bucket, COUNT(*) AS count
FROM aicc_sessions
WHERE org_id = ?
  AND (sqlc.narg(agent_id) IS NULL OR agent_id = sqlc.narg(agent_id))
  AND created_at >= ?
  AND created_at < ?
GROUP BY DATE_FORMAT(created_at, '%x-W%v')
ORDER BY bucket ASC;

-- name: ListAICCRegionsInRange :many
SELECT COALESCE(NULLIF(region, ''), '未知') AS label, COUNT(*) AS count
FROM aicc_sessions
WHERE org_id = ?
  AND (sqlc.narg(agent_id) IS NULL OR agent_id = sqlc.narg(agent_id))
  AND created_at >= ?
  AND created_at < ?
GROUP BY COALESCE(NULLIF(region, ''), '未知')
ORDER BY count DESC, label ASC
LIMIT ?;
```

- [ ] **Step 5: 生成 sqlc 并实现 service**

```bash
make sqlc-gen
```

在 `AICCService.Analytics` 中：

- 默认范围：`now - 7*24h` 到 `now`。
- 最大范围：180 天，超出返回 `ErrInvalidInput`。
- `bucket` 只允许 `day` 或 `week`，空值默认 `day`。
- 未解决率：`unresolved / (resolved + unresolved)`，分母为 0 时返回 0。

- [ ] **Step 6: 实现 handler 参数解析**

`Analytics` 路由改为兼容旧 `/api/v1/aicc/analytics`，同时支持 query：

```go
options := service.AICCAnalyticsOptions{
	OrgID: c.Query("org_id"),
	AgentID: c.Query("agent_id"),
	StartAt: queryTime(c, "start_at"),
	EndAt: queryTime(c, "end_at"),
	Bucket: c.DefaultQuery("bucket", "day"),
}
```

若当前 `queryTime` helper 不存在，在 handler 文件内新增私有函数，使用 `time.Parse(time.RFC3339, value)`；空值返回零时间；非法格式返回 400。

- [ ] **Step 7: 运行测试、生成契约并提交**

```bash
make openapi-gen
make web-types-gen
make openapi-check
go test ./internal/service ./internal/api/handlers -run 'AICC.*(Analytics|SessionFilters)' -count=1
git add internal/service/aicc_types.go internal/service/aicc_service.go internal/service/aicc_service_test.go internal/api/handlers/aicc.go internal/api/handlers/aicc_test.go internal/store/queries/aicc.sql internal/store/sqlc/aicc.sql.go openapi/openapi.yaml web/src/api/generated.ts
git commit -m "feat(aicc): 补齐会话筛选和运营统计" -m "会话列表支持时间、地域和解决状态筛选。\n\n统计看板支持时间范围、日周粒度趋势、未解决率、地域分布、来源页和高频问题聚合，并同步 OpenAPI 与 sqlc 生成产物。"
```

## Task 5: 前端设置页、二维码、统计和续接

**Files:**
- Modify: `web/src/domain/aicc.ts`
- Modify: `web/src/api/hooks/useAICC.ts`
- Modify: `web/src/pages/aicc/AICCManagerPage.vue`
- Modify: `web/src/pages/aicc/AICCAnalyticsPage.vue`
- Modify: `web/src/pages/aicc/AICCSessionsPage.vue`
- Modify: `web/src/pages/aicc/PublicAICCChatPage.vue`
- Modify: `web/src/pages/aicc/AICCWidgetScript.spec.ts`
- Modify: `web/tests/e2e/aicc.spec.ts`

- [ ] **Step 1: 写前端单元测试**

在 `web/src/pages/aicc/AICCWidgetScript.spec.ts` 或新增 AICC 前端 spec 中覆盖公开端 token 保存。测试代码形态：

```ts
// 场景：公开客服页刷新后会把本地保存的 session_token 带给创建会话接口，避免刷新生成新会话。
it('passes stored session token when creating a public session', async () => {
  localStorage.setItem('aicc:session:pub:web_link', 'session-token-1')

  const session = await createAICCPublicSession('pub', 'web_link')

  expect(session.session_token).toBeDefined()
  expect(fetchMock).toHaveBeenCalledWith(
    expect.stringContaining('/api/v1/public/aicc/agents/pub/sessions'),
    expect.objectContaining({
      method: 'POST',
      body: expect.stringContaining('"session_token":"session-token-1"'),
    }),
  )
})
```

如果现有测试工具不使用 `fetchMock`，按项目当前 `vitest` mock 风格实现同等断言。

- [ ] **Step 2: 扩展前端领域类型**

在 `web/src/domain/aicc.ts` 增加：

```ts
export interface AICCAgentSettings {
  agent_id: string
  message_limit_per_session: number
  sensitive_words: string[]
  blocked_visitor_enabled: boolean
  session_resume_ttl_minutes: number
  blocked_visitor_count?: number
}

export interface AICCAgentSettingsPayload {
  message_limit_per_session: number
  sensitive_words: string[]
  blocked_visitor_enabled: boolean
  session_resume_ttl_minutes: number
}

export interface AICCAnalyticsFilters {
  start_at?: string
  end_at?: string
  bucket?: 'day' | 'week'
  agent_id?: string
}
```

扩展 `AICCSessionFilters`：

```ts
region?: string
start_at?: string
end_at?: string
```

扩展 `AICCPublicSession`：

```ts
restored?: boolean
```

- [ ] **Step 3: 扩展 API hooks**

`web/src/api/hooks/useAICC.ts` 增加：

```ts
const aiccSettingsKey = (agentId?: string) => ['aicc', 'settings', agentId] as const
const aiccAnalyticsKey = (filters?: AICCAnalyticsFilters) => ['aicc', 'analytics', filters ?? {}] as const

export function useAICCSettingsQuery(agentId: Ref<string | undefined>) {
  return useQuery<AICCAgentSettings | null>({
    queryKey: computed(() => aiccSettingsKey(agentId.value)),
    enabled: () => Boolean(agentId.value),
    queryFn: async () => {
      if (!agentId.value) return null
      const response = await apiRequest<{ settings: AICCAgentSettings }>(`/api/v1/aicc/agents/${agentId.value}/settings`)
      return response.settings
    },
  })
}

export function useUpdateAICCSettings() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ agentId, payload }: { agentId: string; payload: AICCAgentSettingsPayload }) => {
      const response = await apiRequest<{ settings: AICCAgentSettings }>(`/api/v1/aicc/agents/${agentId}/settings`, {
        method: 'PUT',
        body: payload,
      })
      return response.settings
    },
    onSuccess: (_settings, vars) => {
      void client.invalidateQueries({ queryKey: aiccSettingsKey(vars.agentId) })
    },
  })
}
```

`createAICCPublicSession` 读取和写入 localStorage：

```ts
const sessionStorageKey = `aicc:session:${publicToken}:${channel}`
const storedToken = typeof localStorage === 'undefined' ? '' : localStorage.getItem(sessionStorageKey) || ''
const response = await apiRequest<{ session: AICCPublicSession }>(`/api/v1/public/aicc/agents/${publicToken}/sessions`, {
  method: 'POST',
  withAuth: false,
  body: {
    channel,
    session_token: storedToken || undefined,
    referrer: typeof document === 'undefined' ? '' : document.referrer,
    source_url: typeof window === 'undefined' ? '' : window.location.href,
  },
})
if (response.session.session_token && typeof localStorage !== 'undefined') {
  localStorage.setItem(sessionStorageKey, response.session.session_token)
}
```

- [ ] **Step 4: 实现设置页二维码和运营配置**

在 `AICCManagerPage.vue` 的选中 agent 设置区域增加：

- 独立链接文本和复制按钮。
- 二维码展示和下载 PNG。
- 消息上限输入。
- 敏感词 textarea，一行一个。
- 封禁开关。
- 会话续接分钟数输入。

二维码优先复用项目已有 `qrcode` 依赖。若需要导入：

```ts
import QRCode from 'qrcode'
```

生成 PNG：

```ts
const qrDataUrl = ref('')
watch(publicLink, async (value) => {
  qrDataUrl.value = value ? await QRCode.toDataURL(value, { width: 192, margin: 1 }) : ''
}, { immediate: true })
```

- [ ] **Step 5: 实现统计和会话筛选页面**

`AICCAnalyticsPage.vue`：

- 增加时间范围快捷按钮：今天、近 7 天、近 30 天。
- 增加 `day/week` 分段按钮。
- 渲染 `session_trend`、`regions`、`top_sources`、`top_questions`。
- 空数组时显示空状态。

`AICCSessionsPage.vue`：

- 筛选模型增加 `start_at`、`end_at`、`region`。
- 列表显示 `region` 和 `message_count`。
- 筛选条件写入 route query，页面刷新保留筛选。

- [ ] **Step 6: 实现公开页安全提示**

`PublicAICCChatPage.vue` 捕获 API 错误时按 message 展示统一中文提示：

```ts
const friendlyAICCError = (error: unknown): string => {
  const text = error instanceof Error ? error.message : String(error || '')
  if (text.includes('AICC_SENSITIVE_WORD')) return '这条消息包含暂不支持发送的内容，请调整后再试。'
  if (text.includes('AICC_MESSAGE_LIMIT_EXCEEDED')) return '本次会话消息数量已达上限，请稍后重新打开客服。'
  if (text.includes('AICC_VISITOR_BLOCKED')) return '当前访客暂不能继续咨询。'
  return text || '消息发送失败，请稍后重试。'
}
```

- [ ] **Step 7: 运行前端测试并提交**

```bash
cd web && npm run typecheck && npm run test -- --run
git add web/src/domain/aicc.ts web/src/api/hooks/useAICC.ts web/src/pages/aicc/AICCManagerPage.vue web/src/pages/aicc/AICCAnalyticsPage.vue web/src/pages/aicc/AICCSessionsPage.vue web/src/pages/aicc/PublicAICCChatPage.vue web/src/pages/aicc/AICCWidgetScript.spec.ts
git commit -m "feat(aicc): 增加二维码和运营配置页面" -m "后台 AICC 设置页增加二维码下载、会话续接、安全策略配置。\n\n统计页和会话页补齐时间、地域、趋势与筛选展示，公开页支持 session token 恢复和安全拦截提示。"
```

## Task 6: 端到端验证与最终收口

**Files:**
- Modify: `web/tests/e2e/aicc.spec.ts`
- Verify: local app at `http://ocm.localhost`

- [ ] **Step 1: 扩展 E2E 测试**

在 `web/tests/e2e/aicc.spec.ts` 增加或扩展用例：

```ts
test('AICC operations settings, analytics filters and public resume work', async ({ page }) => {
  await page.goto('http://ocm.localhost')
  await page.getByLabel('用户名').fill('admin')
  await page.getByLabel('密码').fill('admin123')
  await page.getByRole('button', { name: '登录' }).click()

  await page.getByRole('button', { name: '在线客服' }).click()
  await page.getByRole('tab', { name: '设置' }).click()
  await expect(page.getByText('运营配置')).toBeVisible()
  await expect(page.getByRole('button', { name: '下载二维码' })).toBeVisible()

  await page.getByLabel('单会话消息上限').fill('5')
  await page.getByLabel('敏感词').fill('违禁词')
  await page.getByRole('button', { name: '保存运营配置' }).click()
  await expect(page.getByText('保存成功')).toBeVisible()

  await page.getByRole('tab', { name: '统计' }).click()
  await page.getByRole('button', { name: '近 7 天' }).click()
  await page.getByRole('button', { name: '周' }).click()
  await expect(page.getByText('访客地域')).toBeVisible()

  await page.getByRole('tab', { name: '会话' }).click()
  await page.getByLabel('地域').fill('上海')
  await expect(page).toHaveURL(/region=/)
})
```

如果现有页面 label 与片段不同，先用 `rg -n "在线客服|运营配置|下载二维码|单会话消息上限|敏感词|访客地域" web/src/pages/aicc` 找到实际文案，再把 locator 改成同一语义的可见文本。

- [ ] **Step 2: 运行全量后端测试**

```bash
go test ./...
```

Expected: PASS。

- [ ] **Step 3: 运行前端类型检查、构建和单测**

```bash
cd web && npm run typecheck && npm run build && npm run test -- --run
```

Expected: PASS。

- [ ] **Step 4: 运行 OpenAPI 校验**

```bash
make openapi-check
```

Expected: PASS，工作区不因生成 OpenAPI 出现差异。

- [ ] **Step 5: 使用 ocm.localhost 做真实浏览器验证**

启动或确认本地环境可访问后运行：

```bash
PLAYWRIGHT_BASE_URL=http://ocm.localhost npm run test:e2e -- aicc.spec.ts
```

Expected: PASS。

手工浏览器验证也必须覆盖：

- `http://ocm.localhost` 登录后台。
- AICC 设置页能复制独立链接、显示二维码、下载二维码。
- 设置页保存消息上限、敏感词、续接时长后刷新仍回显。
- 统计页切换近 7 天、近 30 天、日/周粒度后数据刷新。
- 会话页按时间、地域、解决状态筛选后 URL query 保留。
- 公开客服页刷新后继续同一会话。
- 输入敏感词后显示拦截提示，且不出现助手回复。

- [ ] **Step 6: 最终提交测试和验证补齐**

```bash
git add web/tests/e2e/aicc.spec.ts
git commit -m "test(aicc): 覆盖客服补齐流程" -m "补充 AICC 运营配置、二维码、统计筛选、会话筛选和公开端续接的端到端验证。\n\n本地通过 go test、前端类型检查、构建、单测、OpenAPI 校验和 ocm.localhost 浏览器验证。"
```

如果 Step 6 没有新增测试文件改动，改为不提交，并在交付说明中列出已运行的验证命令。

## Final Verification Checklist

- [ ] `git status --short` 只显示当前任务预期文件。
- [ ] 每个阶段完成后都已 commit。
- [ ] `go test ./...` 通过。
- [ ] `cd web && npm run typecheck && npm run build && npm run test -- --run` 通过。
- [ ] `make openapi-check` 通过。
- [ ] `PLAYWRIGHT_BASE_URL=http://ocm.localhost npm run test:e2e -- aicc.spec.ts` 通过。
- [ ] 真实浏览器验证使用 `ocm.localhost`，覆盖后台与公开端。
- [ ] 文档中的 `AICC = AI Integrated Customer Care` 与代码注释、提示词中的旧称不冲突；若发现用户可见旧称，随对应阶段改为新含义。
