# AICC 与助手版本业务隔离 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让智能客服只使用企业级 AICC 模型和智能体级人设/知识配置，彻底移除对普通助手版本的创建、启动、模型、路由、Skill 与行业知识库依赖。

**Architecture:** 新建 `organization_aicc_configs` 作为企业 AICC 唯一配置源，`aicc_agents` 保存人设和已应用修订，隐藏 app 仅保留运行时锚点且 `version_id=NULL`。模型变更通过持久化 `aicc_model_rollout` 任务逐台滚动重启运行中的客服，并用 Kubernetes Deployment rollout 完成事实确认后写回修订。

**Tech Stack:** Go 1.22+、Gin、sqlc/MySQL、golang-migrate、Kubernetes client-go、Vue 3 + TypeScript + Naive UI、TanStack Query、Vitest、Playwright。

---

## 文件结构

- `internal/migrations/000040_aicc_assistant_version_isolation.{up,down}.sql`：新配置表、智能体字段、旧配置迁移、版本绑定备份和 job 枚举迁移。
- `internal/store/queries/aicc_configs.sql`：企业 AICC 配置读取、写入、修订和 rollout 候选查询。
- `internal/store/queries/aicc.sql`：人设、知识选择和已应用修订写入。
- `internal/service/organization_aicc_config.go`：配置权限、模型校验、事务更新和 rollout 任务创建。
- `internal/store/organization_aicc_config_runner.go`：配置、行业授权与 job 的事务适配器。
- `internal/service/aicc_service.go`、`aicc_types.go`：智能体人设、创建/编辑知识范围和无版本隐藏 app 创建。
- `internal/worker/handlers/app_initialize.go`：普通应用与 AICC 在版本读取前分流。
- `internal/service/bootstrap_service.go`：AICC 从企业配置和智能体读取模型、人设；普通应用继续读取助手版本。
- `internal/worker/handlers/aicc_model_rollout.go`：企业模型逐台静默重启执行器。
- `internal/integrations/k8sorch/{orchestrator,adapter,routing}.go`：等待新 Deployment revision 完整可用。
- `internal/api/handlers/{organizations,dto}.go`：独立 GET/PUT AICC 配置契约和智能体字段契约。
- `web/src/pages/platform/OrganizationsPage.vue`：平台选择企业模型和确认静默重启。
- `web/src/pages/aicc/AICCManagerPage.vue`：客服人设、行业库选择和企业模型只读展示。
- `openapi/openapi.yaml`、`web/src/api/generated.ts`：只通过生成命令更新。

### Task 1: 建立隔离数据模型与迁移保护

**Files:**
- Create: `internal/migrations/000040_aicc_assistant_version_isolation.up.sql`
- Create: `internal/migrations/000040_aicc_assistant_version_isolation.down.sql`
- Modify: `sqlc.yaml`
- Modify: `internal/migrations/migrations_test.go`
- Create: `internal/store/queries/aicc_configs.sql`
- Modify: `internal/store/queries/aicc.sql`
- Modify: `internal/store/queries/organizations.sql`
- Modify: `internal/store/queries/jobs.sql`
- Regenerate: `internal/store/sqlc/aicc_configs.sql.go`
- Regenerate: `internal/store/sqlc/aicc.sql.go`
- Regenerate: `internal/store/sqlc/organizations.sql.go`
- Regenerate: `internal/store/sqlc/jobs.sql.go`
- Regenerate: `internal/store/sqlc/models.go`
- Regenerate: `internal/store/sqlc/querier.go`

- [ ] **Step 1: 先写迁移契约失败测试**

在 `internal/migrations/migrations_test.go` 增加 `TestAICCAssistantVersionIsolationMigration`，逐项断言：新表包含 `model/revision` 约束、备份表无助手版本外键、AICC app 清空 `version_id`、旧组织字段被删除、down 恢复字段与绑定、jobs CHECK 包含 `aicc_model_rollout`。每个断言前添加中文场景注释。

```go
// TestAICCAssistantVersionIsolationMigration 验证 AICC 配置迁出组织主表、隐藏应用解除版本绑定且可回滚。
func TestAICCAssistantVersionIsolationMigration(t *testing.T) {
	upBytes, err := FS.ReadFile("000040_aicc_assistant_version_isolation.up.sql")
	require.NoError(t, err)
	downBytes, err := FS.ReadFile("000040_aicc_assistant_version_isolation.down.sql")
	require.NoError(t, err)
	up, down := string(upBytes), string(downBytes)
	assert.Contains(t, up, "CREATE TABLE organization_aicc_configs")
	assert.Contains(t, up, "CONSTRAINT organization_aicc_configs_enabled_model_check")
	assert.Contains(t, up, "CREATE TABLE aicc_version_isolation_backups")
	assert.NotContains(t, up, "fk_aicc_version_isolation_backups_version")
	assert.Contains(t, up, "UPDATE apps SET version_id = NULL WHERE app_type = 'aicc'")
	assert.Contains(t, up, "DROP COLUMN aicc_enabled")
	assert.Contains(t, up, "'aicc_model_rollout'")
	assert.Contains(t, down, "ADD COLUMN aicc_enabled BOOLEAN NOT NULL DEFAULT FALSE")
	assert.Contains(t, down, "JOIN aicc_version_isolation_backups")
}
```

- [ ] **Step 2: 运行测试并确认缺少迁移文件**

Run: `go test ./internal/migrations -run TestAICCAssistantVersionIsolationMigration -count=1`

Expected: FAIL，错误包含 `000040_aicc_assistant_version_isolation.up.sql: file does not exist`。

- [ ] **Step 3: 编写 up/down 迁移**

up 的核心定义必须使用以下字段和约束；先插入备份与新配置，再清空版本和删除旧列：

```sql
CREATE TABLE organization_aicc_configs (
    org_id CHAR(36) PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    model VARCHAR(191) NULL,
    agent_limit INT NULL,
    revision INT NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_organization_aicc_configs_org FOREIGN KEY (org_id) REFERENCES organizations(id) ON DELETE CASCADE,
    CONSTRAINT organization_aicc_configs_limit_check CHECK (agent_limit IS NULL OR agent_limit >= 0),
    CONSTRAINT organization_aicc_configs_enabled_model_check CHECK (enabled = FALSE OR (model IS NOT NULL AND LENGTH(TRIM(model)) > 0))
);

CREATE TABLE aicc_version_isolation_backups (
    app_id CHAR(36) PRIMARY KEY,
    version_id CHAR(36) NULL
);

ALTER TABLE aicc_agents
    ADD COLUMN persona TEXT NULL AFTER name,
    ADD COLUMN applied_config_revision INT NOT NULL DEFAULT 0 AFTER persona;

INSERT INTO aicc_version_isolation_backups (app_id, version_id)
SELECT id, version_id FROM apps WHERE app_type = 'aicc';

INSERT INTO organization_aicc_configs (org_id, enabled, model, agent_limit, revision)
SELECT o.id, o.aicc_enabled, av.main_model, o.aicc_agent_limit, 1
FROM organizations o
LEFT JOIN assistant_versions av
  ON av.id = JSON_UNQUOTE(JSON_EXTRACT(o.assistant_version_ids, '$[0]'))
 AND av.deleted_at IS NULL;

UPDATE apps SET version_id = NULL WHERE app_type = 'aicc';
```

在删除旧列前加入启用企业模型完整性约束，让缺失首版本的企业使迁移失败；上线前使用同一 join 的 `WHERE o.aicc_enabled=TRUE AND av.id IS NULL` 预检并输出企业 ID。down 先恢复组织字段和值，再从备份表恢复 app 绑定，最后删除新表和字段。同步重建 jobs type CHECK，加入 `aicc_model_rollout`。

- [ ] **Step 4: 添加 sqlc 查询并生成代码**

`internal/store/queries/aicc_configs.sql` 至少定义：

```sql
-- name: GetOrganizationAICCConfig :one
SELECT * FROM organization_aicc_configs WHERE org_id = ?;

-- name: ListOrganizationAICCConfigs :many
SELECT * FROM organization_aicc_configs ORDER BY org_id;

-- name: UpdateOrganizationAICCConfig :exec
UPDATE organization_aicc_configs
SET enabled = ?, model = ?, agent_limit = ?, revision = ?, updated_at = NOW()
WHERE org_id = ?;

-- name: ListPendingAICCModelRolloutAgents :many
SELECT aa.* FROM aicc_agents aa
JOIN apps a ON a.id = aa.app_id AND a.deleted_at IS NULL
WHERE aa.org_id = ? AND aa.deleted_at IS NULL AND aa.status = 'active'
  AND aa.applied_config_revision < ?
ORDER BY aa.id
LIMIT ?;

-- name: SetAICCAgentAppliedConfigRevision :exec
UPDATE aicc_agents SET applied_config_revision = ?, updated_at = NOW()
WHERE id = ? AND applied_config_revision < ?;
```

修改 `CreateAICCAgent`、`UpdateAICCAgentProfile` 查询读写 `persona`；从旧组织 update query 删除 AICC 列。将 000040 up 文件加入 `sqlc.yaml` 后运行：

Run: `make sqlc-generate`

Expected: `internal/store/sqlc/models.go` 出现 `OrganizationAiccConfig`，`AiccAgent` 出现 `Persona` 与 `AppliedConfigRevision`，无生成错误。

- [ ] **Step 5: 运行迁移与查询层验证**

Run: `go test ./internal/migrations ./internal/store/sqlc -count=1`

Expected: PASS。

- [ ] **Step 6: 提交数据模型**

```bash
git add internal/migrations internal/store/queries internal/store/sqlc sqlc.yaml
git commit -m "feat(aicc): 建立独立企业配置数据模型" -m "迁移企业 AICC 开关、模型和数量上限，并解除隐藏应用的助手版本绑定。\n\n保留可回滚的原版本映射并增加模型 rollout 查询。"
```

### Task 2: 实现企业 AICC 配置服务与独立 API

**Files:**
- Create: `internal/service/organization_aicc_config.go`
- Create: `internal/service/organization_aicc_config_test.go`
- Create: `internal/store/organization_aicc_config_runner.go`
- Modify: `internal/service/organization_service.go`
- Modify: `internal/service/organization_service_test.go`
- Modify: `internal/api/handlers/organizations.go`
- Modify: `internal/api/handlers/organizations_test.go`
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/auth/authorizer.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: 写配置校验与原子任务失败测试**

增加以下测试并为每个测试写相邻中文注释：

```go
func TestOrganizationServiceUpdateAICCConfigRequiresAvailableModelWhenEnabled(t *testing.T)
func TestOrganizationServiceUpdateAICCConfigCreatesRolloutOnModelChange(t *testing.T)
func TestOrganizationServiceUpdateAICCConfigDoesNotRolloutWhenModelUnchanged(t *testing.T)
func TestOrganizationServiceUpdateAICCConfigRollsBackWhenJobInsertFails(t *testing.T)
func TestOrganizationServiceGetAICCConfigAllowsOwnOrgAdminRead(t *testing.T)
func TestOrganizationServiceGetAICCConfigRejectsOtherOrg(t *testing.T)
```

关键断言使用：

```go
require.ErrorIs(t, err, ErrInvalidArgument)
assert.Equal(t, int32(8), txStore.config.Revision)
require.Len(t, txStore.jobs, 1)
assert.Equal(t, domain.JobTypeAICCModelRollout, txStore.jobs[0].Type)
assert.JSONEq(t, `{"org_id":"org-1","target_revision":8}`, string(txStore.jobs[0].PayloadJson))
```

- [ ] **Step 2: 运行 service 测试确认接口尚不存在**

Run: `go test ./internal/service -run 'TestOrganizationService(Get|Update)AICCConfig' -count=1`

Expected: FAIL，缺少新结果类型、模型字段或事务 runner 方法。

- [ ] **Step 3: 实现窄接口、模型验证和事务更新**

在新文件定义明确类型：

```go
type AICCModelValidator interface {
	HasModelInCatalog(ctx context.Context, id string) bool
}

type OrganizationAICCConfigResult struct {
	OrgID                    string                     `json:"org_id"`
	Enabled                  bool                       `json:"enabled"`
	Model                    string                     `json:"model,omitempty"`
	AgentLimit               *int32                     `json:"agent_limit,omitempty"`
	Revision                 int32                      `json:"revision"`
	IndustryKnowledgeBases   []IndustryKnowledgeBaseRef `json:"industry_knowledge_bases"`
}

type OrganizationAICCConfigTxRunner interface {
	WithOrganizationAICCConfigTx(ctx context.Context, fn func(OrganizationAICCConfigStore) error) error
}
```

`UpdateAICCConfig` 必须 trim 模型、启用时实时校验、锁定当前配置、仅模型变化递增 revision，并在同一事务整组替换行业授权、清理智能体失效关联及创建 `aicc_model_rollout` job。job 使用 UUID、priority 100、`MaxAttempts: 20`。事务成功后 best-effort 通知现有 `JobNotifier`。

- [ ] **Step 4: 聚合组织列表中的兼容字段**

保留 `OrganizationResult.aicc_enabled/aicc_agent_limit` 供现有导航判断，并新增只读 `aicc_model`。`toOrganizationResultWithAdminUsername` 和列表转换从新配置表填充，不再读取 `sqlc.Organization` 的旧列：

```go
func applyAICCConfig(result *OrganizationResult, cfg sqlc.OrganizationAiccConfig) {
	result.AICCEnabled = cfg.Enabled
	result.AICCAgentLimit = int32PtrFromNullInt(cfg.AgentLimit)
	result.AICCModel = strOrEmpty(cfg.Model)
}
```

- [ ] **Step 5: 将路由改成 GET/PUT 并验证权限**

路由固定为：

```go
group.GET("/:orgId/aicc-config", handler.GetAICCConfig)
group.PUT("/:orgId/aicc-config", handler.UpdateAICCConfig)
```

DTO 增加 `Model string json:"model"`。GET 和 PUT 都返回键名为 `config`、值类型为
`service.OrganizationAICCConfigResult` 的 JSON envelope。平台管理员可写；企业管理员只能 GET
自己企业。更新 handler tests 断言 PUT，不保留旧 PATCH 写入口。

- [ ] **Step 6: 装配模型目录、事务 runner 和 notifier**

在 `cmd/server/main.go` 为 `OrganizationService` 注入 `ModelCatalogService`、`store.NewOrganizationAICCConfigRunner(dbStore)` 与现有 job notifier。不要把助手版本 service 作为 AICC 模型依赖。

- [ ] **Step 7: 运行定向测试并提交**

Run: `go test ./internal/service ./internal/api/handlers -run 'AICCConfig|Organization' -count=1`

Expected: PASS。

```bash
git add internal/service internal/store/organization_aicc_config_runner.go internal/api/handlers internal/auth/authorizer.go cmd/server/main.go
git commit -m "feat(aicc): 独立企业客服模型配置" -m "通过独立 GET/PUT 接口维护企业 AICC 模型、开关、限额和行业库授权。\n\n模型变化与逐台重启任务在同一事务中持久化。"
```

### Task 3: 让智能体创建与编辑脱离助手版本

**Files:**
- Modify: `internal/service/aicc_types.go`
- Modify: `internal/service/aicc_service.go`
- Modify: `internal/service/aicc_service_test.go`
- Modify: `internal/service/app_service.go`
- Modify: `internal/service/app_service_test.go`
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/aicc.go`
- Modify: `internal/api/handlers/aicc_test.go`

- [ ] **Step 1: 写无版本创建、人设和知识授权失败测试**

```go
func TestCreateAgentUsesAICCConfigWithoutAssistantVersion(t *testing.T)
func TestCreateAgentPersistsPersonaAndAuthorizedIndustryKnowledge(t *testing.T)
func TestCreateAgentRejectsDisabledAICCConfig(t *testing.T)
func TestCreateAgentRejectsUnauthorizedIndustryKnowledge(t *testing.T)
func TestUpdateAgentPersistsPersonaAndIndustryKnowledgeAtomically(t *testing.T)
func TestUpdateAgentRestartsRunningAgentWhenPromptChanges(t *testing.T)
func TestUpdateAgentDoesNotRestartWhenOnlyGreetingChanges(t *testing.T)
```

正常路径断言隐藏 app input 不包含版本，存储记录包含 `Persona`，知识行只包含提交的授权行业库；异常路径断言未创建 app 或 profile 未部分更新。

- [ ] **Step 2: 运行测试确认当前仍读取首个助手版本**

Run: `go test ./internal/service -run 'Test(Create|Update)Agent|TestCreateHiddenAICCApp' -count=1`

Expected: FAIL，旧 `CreateHiddenAICCApp` 仍调用 `firstAssistantVersionID`，新 persona/industry 字段不存在。

- [ ] **Step 3: 扩展智能体契约并复用知识保存 helper**

`AICCAgentInput` 增加无 JSON tag 的 `Persona string` 和
`IndustryKnowledgeBaseIDs []string`；`AICCAgentResult` 增加以下响应字段：

```go
Persona                  string   `json:"persona,omitempty"`
IndustryKnowledgeBaseIDs []string `json:"industry_knowledge_base_ids"`
```

把 `ReplaceAgentKnowledge` 中的授权校验和整组写入拆成同文件私有 helper：

```go
func (s *AICCService) validateIndustryKnowledge(ctx context.Context, orgID string, ids []string) ([]string, error)
func (s *AICCService) replaceAgentIndustryKnowledge(ctx context.Context, store AICCStore, agent sqlc.AiccAgent, ids []string) error
```

创建和编辑在 AICC transaction 中写 profile 与行业库。创建前读取 `GetOrganizationAICCConfig`，要求 `enabled=true` 且 model 非空。现有企业知识库勾选语义保持不变；创建时默认启用企业知识库。编辑运行中智能体时，只有 `persona`、`scenario` 或 `answer_boundary` 实际变化才在同一事务入队一个 `app_restart_container`；名称、欢迎语、隐私或知识范围变化不重启。事务提交后通过现有 notifier 唤醒该 job。

- [ ] **Step 4: 删除隐藏应用首版本选择**

`CreateHiddenAICCApp` 不再调用 `firstAssistantVersionID`，创建参数固定：

```go
VersionID: null.String{},
AppType:   string(domain.AppTypeAICC),
```

删除 `TestCreateHiddenAICCAppRejectsMissingVersionAllowlist`，替换为 `TestCreateHiddenAICCAppLeavesVersionUnbound`，断言企业 allowlist 为空也能创建且 `VersionID.Valid == false`。仅在没有其他调用时删除 `firstAssistantVersionID`。

- [ ] **Step 5: 更新 HTTP DTO 与映射**

创建/更新请求增加 `persona` 与 `industry_knowledge_base_ids`，`toAICCAgentInput` 原样透传。为 persona 设置 service 侧 trim、最大 8000 rune 校验，超限返回 `ErrInvalidArgument`。

- [ ] **Step 6: 运行定向测试并提交**

Run: `go test ./internal/service ./internal/api/handlers -run 'AICC|HiddenAICC' -count=1`

Expected: PASS。

```bash
git add internal/service internal/api/handlers
git commit -m "feat(aicc): 使用独立人设和知识配置创建客服" -m "智能体创建与编辑保存客服人设和授权行业库。\n\n隐藏应用不再选择或绑定企业助手版本。"
```

### Task 4: 隔离初始化、bootstrap 与提示词

**Files:**
- Modify: `internal/worker/handlers/app_initialize.go`
- Modify: `internal/worker/handlers/app_initialize_test.go`
- Modify: `internal/worker/handlers/app_runtime_ops.go`
- Modify: `internal/worker/handlers/app_runtime_ops_test.go`
- Modify: `internal/service/bootstrap_service.go`
- Modify: `internal/service/bootstrap_service_test.go`
- Modify: `internal/integrations/hermes/app_input.go`
- Modify: `internal/integrations/hermes/app_input_test.go`
- Modify: `internal/service/aicc_public_service.go`
- Modify: `internal/service/aicc_public_service_test.go`

- [ ] **Step 1: 写 AICC 不访问助手版本的失败测试**

为 fake store 的 `GetAssistantVersion` 增加调用计数，并新增：

```go
func TestAppInitializeAICCUsesOrganizationConfigWithoutVersion(t *testing.T)
func TestBootstrapAICCUsesOrganizationModelAndAgentPersona(t *testing.T)
func TestBootstrapAICCOmitsRoutingAndVersionSkills(t *testing.T)
func TestBootstrapStandardAppStillRequiresAssistantVersion(t *testing.T)
func TestBuildAICCRuntimePromptDoesNotDuplicateBootstrapPersona(t *testing.T)
```

断言 AICC 路径 `assistantVersionCalls == 0`，manifest model 等于企业配置模型，persona 文本按平台规则之外的 `persona → scenario → answer_boundary` 顺序出现，routing/skill paths 为空。

- [ ] **Step 2: 运行最小测试确认失败**

Run: `go test ./internal/worker/handlers ./internal/service -run 'AICC.*(Version|Config|Persona)|BootstrapStandard' -count=1`

Expected: FAIL，初始化或 bootstrap 报“实例未绑定助手版本”。

- [ ] **Step 3: 在版本读取前按 app_type 分流**

为 handler store 增加 `GetAICCAgentByAppID`、`GetOrganizationAICCConfig`、`SetAICCAgentAppliedConfigRevision`。初始化上下文使用显式结构，避免 AICC 构造伪助手版本：

```go
type resolvedInitializeConfig struct {
	imageRef      string
	version       *sqlc.AssistantVersion
	aiccAgent     *sqlc.AiccAgent
	aiccRevision  int32
}
```

普通 app 执行现有版本、镜像和 skill seed 逻辑；AICC 校验配置启用、使用专用镜像、跳过版本与 skill seed。WaitReady 成功后，AICC 写 `applied_config_revision`；普通 app 继续写 applied version。

- [ ] **Step 4: 让 bootstrap 使用独立配置**

bootstrap AICC 分支查询 agent/config，构造：

```go
in.Model = cfg.Model.String
in.PersonaText = renderAICCAgentPersona(agent)
in.Routing = nil
in.SkillRelPaths = nil
```

`renderAICCAgentPersona` 只拼非空段落，并使用固定中文标题：`客服人设`、`业务场景`、`回答边界`。平台 AICC 安全规则仍通过 `PlatformPromptForApp(AppTypeAICC)` 单独渲染，企业文本不能进入 platform rule。

- [ ] **Step 5: 清理重启和逐轮提示中的版本假设**

`AppRestartContainerHandler` 对 AICC 跳过 `inputRefresher` 的助手版本刷新、对象存储会话清理、版本 skill seed 与 `SetAppAppliedVersion`。`buildAICCRuntimePrompt` 不再重复 persona/scenario/boundary；只保留会话级动态数据边界，确保 bootstrap 是智能体提示词唯一来源。

- [ ] **Step 6: 运行定向测试并提交**

Run: `go test ./internal/worker/handlers ./internal/service ./internal/integrations/hermes -run 'AICC|Bootstrap|AppInitialize|AppRestart' -count=1`

Expected: PASS。

```bash
git add internal/worker/handlers internal/service/bootstrap_service* internal/service/aicc_public_service* internal/integrations/hermes
git commit -m "refactor(aicc): 从独立配置渲染客服运行时" -m "AICC 初始化在版本加载前分流，并从企业模型和智能体人设生成 bootstrap。\n\n普通实例继续沿用助手版本初始化链路。"
```

### Task 5: 实现可核验的逐台模型 rollout

**Files:**
- Modify: `internal/domain/enums.go`
- Modify: `internal/domain/enums_test.go`
- Modify: `internal/integrations/k8sorch/orchestrator.go`
- Modify: `internal/integrations/k8sorch/adapter.go`
- Modify: `internal/integrations/k8sorch/adapter_test.go`
- Modify: `internal/integrations/k8sorch/routing.go`
- Modify: `internal/integrations/k8sorch/routing_test.go`
- Create: `internal/worker/handlers/aicc_model_rollout.go`
- Create: `internal/worker/handlers/aicc_model_rollout_test.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: 写 rollout 顺序、幂等和暂停过滤测试**

```go
func TestAICCModelRolloutRestartsOneAgentAfterAnother(t *testing.T)
func TestAICCModelRolloutStopsWhenCurrentAgentFails(t *testing.T)
func TestAICCModelRolloutSkipsAppliedAgents(t *testing.T)
func TestAICCModelRolloutCoalescesToLatestRevision(t *testing.T)
func TestWaitRolloutReadyRequiresObservedDeploymentGeneration(t *testing.T)
```

fake orchestrator 记录事件，成功顺序必须严格等于：

```go
assert.Equal(t, []string{
	"restart:app-1", "wait:app-1", "stamp:agent-1:8",
	"restart:app-2", "wait:app-2", "stamp:agent-2:8",
}, events)
```

失败路径只包含 app-1，不出现 app-2；候选 SQL 已按 `status='active'` 排除暂停智能体。

- [ ] **Step 2: 运行测试确认 handler 和等待接口缺失**

Run: `go test ./internal/worker/handlers ./internal/integrations/k8sorch -run 'AICCModelRollout|WaitRolloutReady' -count=1`

Expected: FAIL，缺少 `AICCModelRolloutHandler` 与 `WaitRolloutReady`。

- [ ] **Step 3: 扩展 Kubernetes rollout 完成事实接口**

在 `Orchestrator` 增加：

```go
WaitRolloutReady(ctx context.Context, appID string, timeout time.Duration, onPoll func(AppStatus)) error
```

adapter 每轮读取 Deployment，只有 `status.observedGeneration >= metadata.generation`、
`updatedReplicas == spec.replicas`、`availableReplicas == spec.replicas` 且
`unavailableReplicas == 0` 才成功。该判断避免 `RolloutRestart` 后旧 pod 仍 Ready 时提前处理下一台。

- [ ] **Step 4: 实现 rollout handler**

handler payload 与入口固定为：

```go
type aiccModelRolloutPayload struct {
	OrgID          string `json:"org_id"`
	TargetRevision int32  `json:"target_revision"`
}

func (h *AICCModelRolloutHandler) Handle(ctx context.Context, job sqlc.Job) error
```

每轮重新读取企业最新配置，将过期 payload 合并到最新 revision；每次查询一个 pending agent，先写 app runtime phase `restarting`，调用 `RolloutRestart` 和 `WaitRolloutReady`，成功后写 agent applied revision 与 runtime phase `ready`。任何错误立即返回，由 job 重试；查询无候选时成功结束。

- [ ] **Step 5: 注册 job 类型与生产依赖**

在 domain 的合法 job 集合加入：

```go
const JobTypeAICCModelRollout = "aicc_model_rollout"
```

`cmd/server/main.go` 使用 db queries、routing orchestrator 构造 handler并注册；未启用 Kubernetes 时 handler 返回可诊断错误，不能把任务标成功。

- [ ] **Step 6: 运行测试并提交**

Run: `go test ./internal/domain ./internal/integrations/k8sorch ./internal/worker/handlers -run 'JobType|Rollout|AICCModel' -count=1`

Expected: PASS。

```bash
git add internal/domain internal/integrations/k8sorch internal/worker/handlers cmd/server/main.go
git commit -m "feat(aicc): 逐台静默切换企业客服模型" -m "新增持久化模型 rollout handler，并等待 Deployment 新 revision 完整可用后再处理下一台。\n\n任务支持失败重试、修订合并和已应用智能体跳过。"
```

### Task 6: 更新平台企业管理界面

**Files:**
- Modify: `web/src/api/hooks/useOrganizations.ts`
- Modify: `web/src/pages/platform/OrganizationsPage.vue`
- Modify: `web/src/pages/platform/OrganizationsPage.spec.ts`
- Modify: `web/src/i18n/locales/zh/platform.ts`
- Modify: `web/src/i18n/locales/en/platform.ts`

- [ ] **Step 1: 写模型必选、PUT 和确认弹框测试**

在 `OrganizationsPage.spec.ts` 增加：启用但未选模型阻断提交；选择模型后 payload 含 model；修改已启用企业模型弹确认框；确认后调用 PUT mutation；企业模型加载失败时禁用保存。每个 `it` 前写中文场景注释。

```ts
expect(updateOrganizationAICCConfig).toHaveBeenCalledWith({
  id: 'org-1',
  payload: {
    enabled: true,
    model: 'qwen3.5:27b',
    agent_limit: 5,
    industry_knowledge_base_ids: ['industry-1'],
  },
})
```

- [ ] **Step 2: 运行 Vitest 确认失败**

Run: `cd web && npm test -- OrganizationsPage.spec.ts --run`

Expected: FAIL，表单没有客服模型字段且 mutation 仍发送 PATCH。

- [ ] **Step 3: 更新 hook 和独立配置查询**

定义：

```ts
export interface OrganizationAICCConfig {
  org_id: string
  enabled: boolean
  model?: string
  agent_limit?: number
  revision: number
  industry_knowledge_bases: Array<{ id: string; name: string }>
}
```

新增 `useOrganizationAICCConfigQuery(orgId)` GET hook，update mutation 改用 PUT 并失效组织列表和配置 query。payload 的 `model` 为必填字符串。

- [ ] **Step 4: 添加模型选择和变更确认**

复用 `useModelsQuery`，在 AICC 区域添加可搜索 `n-select`。启用时模型为空、模型目录错误或仍在加载都禁用保存。模型从已有值变为其他值时使用 `ConfirmActionModal`，文案明确“将逐个静默重启该企业正在运行的智能客服”。仅修改限额或行业授权不弹模型重启确认。

- [ ] **Step 5: 运行页面测试、类型检查并提交**

Run: `cd web && npm test -- OrganizationsPage.spec.ts --run && npm run typecheck`

Expected: PASS。

```bash
git add web/src/api/hooks/useOrganizations.ts web/src/pages/platform/OrganizationsPage.vue web/src/pages/platform/OrganizationsPage.spec.ts web/src/i18n/locales/zh/platform.ts web/src/i18n/locales/en/platform.ts
git commit -m "feat(web): 在企业管理配置客服模型" -m "开通 AICC 时强制选择可用模型，修改模型前提示逐台静默重启。\n\n配置读写切换到独立 GET/PUT 接口。"
```

### Task 7: 更新智能体人设、行业库和模型只读展示

**Files:**
- Modify: `web/src/domain/aicc.ts`
- Modify: `web/src/api/hooks/useAICC.ts`
- Modify: `web/src/pages/aicc/AICCManagerPage.vue`
- Modify: `web/src/pages/aicc/AICCManagerPage.spec.ts`
- Modify: `web/src/i18n/locales/zh/aicc.ts`
- Modify: `web/src/i18n/locales/en/aicc.ts`

- [ ] **Step 1: 写创建/编辑人设与行业库回显测试**

增加测试覆盖：新建表单显示只读企业模型；persona 可保存回显；创建 payload 带行业库 IDs；编辑切换智能体时回显各自人设和行业库；页面不存在助手版本、路由或模型编辑控件。

```ts
expect(createAgent).toHaveBeenCalledWith(expect.objectContaining({
  persona: '专业、克制的售前顾问',
  industry_knowledge_base_ids: ['industry-1'],
}))
```

- [ ] **Step 2: 运行 Vitest 确认字段缺失**

Run: `cd web && npm test -- AICCManagerPage.spec.ts --run`

Expected: FAIL，领域类型和表单没有 `persona`、`industry_knowledge_base_ids`。

- [ ] **Step 3: 实现表单和只读模型**

领域类型为 agent/result/payload 增加：

```ts
persona?: string
industry_knowledge_base_ids: string[]
```

表单添加客服人设 textarea（maxlength 8000）和已授权行业库多选。候选项来自企业 AICC config GET；编辑时以 agent 响应 IDs 回显。顶部设置区域用只读 tag 展示 `config.model`，不提供模型 select。保留现有知识面板管理企业知识开关和当前智能体文档，行业库勾选统一由主表单保存，移除知识面板中的重复行业多选。

- [ ] **Step 4: 运行定向前端测试并提交**

Run: `cd web && npm test -- AICCManagerPage.spec.ts AICCConsoleWorkspace.spec.ts --run && npm run typecheck`

Expected: PASS。

```bash
git add web/src/domain/aicc.ts web/src/api/hooks/useAICC.ts web/src/pages/aicc/AICCManagerPage.vue web/src/pages/aicc/AICCManagerPage.spec.ts web/src/i18n/locales/zh/aicc.ts web/src/i18n/locales/en/aicc.ts
git commit -m "feat(web): 配置智能客服人设和行业知识" -m "企业管理员可在智能体表单维护人设与授权行业库，并只读查看企业统一客服模型。\n\n页面不再暴露助手版本或智能路由概念。"
```

### Task 8: 同步 OpenAPI、生成类型并更新文档

**Files:**
- Regenerate: `openapi/openapi.yaml`
- Regenerate: `web/src/api/generated.ts`
- Modify: `docs/user-manual.md`
- Modify: `docs/architecture.md`
- Modify: `docs/product-design.md`
- Modify: `docs/local-development.md`

- [ ] **Step 1: 更新 swag 注解并生成契约**

确认 handler 注解分别声明 GET/PUT `/organizations/{orgId}/aicc-config`，智能体请求/响应包含 persona 与 industry IDs；运行：

Run: `make openapi-gen && make web-types-gen`

Expected: 两个生成文件发生对应变更，命令退出 0。

- [ ] **Step 2: 校验生成类型的关键字段**

Run: `rg -n 'persona|industry_knowledge_base_ids|aicc-config|model' openapi/openapi.yaml web/src/api/generated.ts`

Expected: AICC 配置 schema 有 `model`，agent schema 有 `persona` 与行业库 IDs，配置路径含 GET/PUT 且不含 PATCH。

- [ ] **Step 3: 更新用户与架构文档**

明确写出：平台开通 AICC 时选择企业统一模型；智能体配置人设与行业库；模型变更逐台静默重启；AICC 不绑定助手版本、不使用路由和版本 Skill；普通实例行为不变。本地 seed 创建 demo AICC 前必须设置企业 AICC 模型，不能再依赖助手版本 allowlist 顺序。

- [ ] **Step 4: 运行类型检查并提交生成物与文档**

Run: `cd web && npm run typecheck`

Expected: PASS。

```bash
git add openapi/openapi.yaml web/src/api/generated.ts docs/user-manual.md docs/architecture.md docs/product-design.md docs/local-development.md
git commit -m "docs(aicc): 同步独立客服配置契约" -m "更新 OpenAPI、前端生成类型及用户文档，说明企业统一模型、人设配置和静默切换流程。"
```

- [ ] **Step 5: 在提交后验证 OpenAPI 可重复生成**

Run: `make openapi-check`

Expected: PASS，重新生成 `openapi/openapi.yaml` 后工作区保持干净。

### Task 9: 定向浏览器验收与回归

**Files:**
- Modify: `web/tests/e2e/aicc.spec.ts`
- Modify: `web/tests/e2e/aicc/helpers.ts`
- Modify: `scripts/local_seed_demo/seeder.py`
- Modify: `scripts/tests/test_local_seed_demo_seeder.py`
- Test: `web/tests/e2e/aicc.spec.ts`

- [ ] **Step 1: 先写浏览器验收场景**

在现有 AICC spec 增加两个定向测试，所有管理和公开客服操作只通过真实页面。先把
`setAICCConfigForFixtureOrg` 扩展为接收模型 ID，并把配置请求方法改成 PUT；测试主体使用：

`setAICCConfigForFixtureOrg` 的最终签名为
`(page: Page, enabled: boolean, agentLimit: number, requestedModel?: string) => Promise<string>`：
登录平台管理员、打开 fixture 企业编辑抽屉、设置开关和限额、从“客服模型” select 选择
`requestedModel`（未传时选择第一个候选），等待 PUT `/aicc-config` 成功并返回选中模型 ID。

新增 `changeAICCModelToAnotherAvailableOption(page: Page): Promise<string>`：打开同一企业，读取
当前模型，从可见候选中选择不同值，保存并确认“逐台静默重启”弹框，等待 PUT 成功并返回新
模型 ID；候选不足两个时抛出 `AICC 模型切换 E2E 至少需要两个可用模型`。

把 `createAICCAgentAsOrgAdmin` 改成
`createAICCAgentAsOrgAdmin(page: Page, persona = '专业客服'): Promise<AICCAgentResponse['agent']`，
通过 `#aicc-persona` 填入参数，并断言页面文本不包含“助手版本”和“智能路由”。

```ts
test('平台配置企业客服模型后企业可创建独立人设智能体', slowModel, async ({ page }) => {
  const model = await setAICCConfigForFixtureOrg(page, true, 100)
  await clearLoginState(page)
  const agent = await createAICCAgentAsOrgAdmin(page, '只称呼访客为贵宾')
  await startAICCAgent(page)
  await page.goto(`/aicc/${agent.public_token}`)
  const answer = await sendPublicAICCMessage(page, '请问你会如何称呼我？')
  expect(answer).toContain('贵宾')
  expect(model).not.toBe('')
})

test('平台修改企业客服模型后运行中智能体静默恢复', slowModel, async ({ page }) => {
  await setAICCConfigForFixtureOrg(page, true, 100)
  await clearLoginState(page)
  const agent = await createAICCAgentAsOrgAdmin(page, '保持简洁回答')
  await startAICCAgent(page)
  await clearLoginState(page)
  await changeAICCModelToAnotherAvailableOption(page)
  await waitForAICCRuntime(agent.app_id)
  await page.goto(`/aicc/${agent.public_token}`)
  await expect(sendPublicAICCMessage(page, '回复“切换成功”')).resolves.toContain('切换成功')
})
```

测试必须断言创建页面没有“助手版本”和“智能路由”，并通过 jobs 页面或管理状态确认 rollout 完成。

- [ ] **Step 2: 运行新增 spec 确认环境或行为失败**

Run: `cd web && npx playwright test tests/e2e/aicc.spec.ts --project=chromium --grep '客服模型|独立人设'`

Expected: 在实现或 seed 尚未同步时 FAIL，并明确落在模型配置/表单字段断言，而非选择器超时。

- [ ] **Step 3: 更新本地 seed 与 helper**

`Seeder._enable_required_aicc` 先 GET 独立配置，再用 PUT 显式提交本地智能客服模型；删除
`validate_aicc_version_order` 及其调用，不再借助助手版本列表顺序决定客服模型。对应 Python
测试断言 PUT body 包含 `model`，并断言现有企业首个助手版本顺序不会阻断 AICC seed。Playwright
helper 在创建表单填写 persona 和行业库，等待运行状态 Ready 后返回。

- [ ] **Step 4: 运行最小 Go、前端和浏览器回归**

Run: `go test ./internal/service ./internal/worker/handlers ./internal/integrations/k8sorch ./internal/api/handlers -count=1`

Expected: PASS。

Run: `cd web && npm test -- OrganizationsPage.spec.ts AICCManagerPage.spec.ts --run && npm run typecheck`

Expected: PASS。

Run: `cd web && npx playwright test tests/e2e/aicc.spec.ts --project=chromium --grep '平台开通 AICC|客服模型|独立人设'`

Expected: PASS；只运行匹配改动范围的 headless Chromium 场景。

- [ ] **Step 5: 验证普通助手业务未受影响**

Run: `go test ./internal/service -run 'AssistantVersion|OnboardMember|CreateHiddenAICCApp' -count=1`

Expected: PASS；普通 onboarding 仍要求显式助手版本，AICC 隐藏 app 不要求版本。

Run: `cd web && npm test -- AssistantVersionsPage.spec.ts CreateMemberPage.spec.ts AppOverviewTab.spec.ts --run`

Expected: PASS。

- [ ] **Step 6: 检查工作区并提交验收代码**

Run: `git status --short && git diff --check`

Expected: 只有本任务 E2E/helper/seed 相关文件，`git diff --check` 无输出。

```bash
git add web/tests/e2e/aicc.spec.ts web/tests/e2e/aicc/helpers.ts scripts/local_seed_demo/seeder.py scripts/tests/test_local_seed_demo_seeder.py
git commit -m "test(aicc): 覆盖独立模型与静默切换" -m "通过真实浏览器验证企业模型配置、智能体人设、助手版本隔离和模型 rollout 恢复。"
```

### Task 10: 交付前综合验证

**Files:**
- Verify only; do not modify unrelated files.

- [ ] **Step 1: 运行格式与生成物检查**

Run: `gofmt -w internal/service/organization_aicc_config.go internal/store/organization_aicc_config_runner.go internal/worker/handlers/aicc_model_rollout.go`

Expected: 命令退出 0。

Run: `make sqlc-generate && make openapi-gen && make web-types-gen && git diff --check`

Expected: 生成命令退出 0，`git diff --check` 无输出；若生成命令产生差异，将生成物加入对应业务提交，不单独手改。

- [ ] **Step 2: 运行改动范围完整回归**

Run: `go test ./internal/migrations ./internal/store/... ./internal/service ./internal/worker/handlers ./internal/integrations/k8sorch ./internal/api/handlers -count=1`

Expected: PASS。

Run: `cd web && npm test -- OrganizationsPage.spec.ts AICCManagerPage.spec.ts AssistantVersionsPage.spec.ts CreateMemberPage.spec.ts AppOverviewTab.spec.ts --run && npm run typecheck && npm run build`

Expected: PASS。

- [ ] **Step 3: 核对隔离完成标准**

Run: `rg -n 'GetAssistantVersion|AssistantVersionIds|ListIndustryKnowledgeBasesByAssistantVersion|seedVersionSkills' internal/service internal/worker/handlers | rg 'AICC|aicc'`

Expected: 只剩普通实例分支、测试防回归断言或说明“不得调用”的中文注释；不存在 AICC 执行路径调用。

Run: `rg -n 'VersionID:.*null.StringFrom|firstAssistantVersionID' internal/service/app_service.go internal/service/onboarding_service.go`

Expected: 普通 onboarding 可保留版本绑定；AICC 创建路径不存在 `null.StringFrom(versionID)` 或 `firstAssistantVersionID`。

- [ ] **Step 4: 最终状态检查**

Run: `git status --short && git log --oneline -10`

Expected: 工作区干净；提交按数据模型、后端配置、智能体隔离、运行时、rollout、两个前端任务、契约文档和 E2E 边界拆分。
