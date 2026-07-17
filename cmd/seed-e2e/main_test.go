// Package main 的 e2e 种子测试只校验危险命令守门，不连接数据库。
package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/config"
	"oc-manager/internal/integrations/newapi"
)

// newAPIUsernameLookupStub 记录用户名占用检查，DeleteUser 仅用于证明安全路径不会调用删除。
type newAPIUsernameLookupStub struct {
	// user 是模拟查询命中的未知上游用户。
	user newapi.User
	// err 是模拟查询结果错误。
	err error
	// deleteCalls 记录是否错误地按用户名授权删除。
	deleteCalls int
}

// FindUserByUsername 返回预置用户或错误，模拟真实 new-api 精确用户名查询。
func (stub *newAPIUsernameLookupStub) FindUserByUsername(context.Context, string) (newapi.User, error) {
	return stub.user, stub.err
}

// DeleteUser 记录危险删除调用；被测占用检查绝不应调用本方法。
func (stub *newAPIUsernameLookupStub) DeleteUser(context.Context, int64) error {
	stub.deleteCalls++
	return nil
}

// 验证默认参数兼容人工直接 seed，并保持 regression 单 worker 行为。
func TestLoadRunOptionsUsesSafeDefaults(t *testing.T) {
	t.Setenv("OCM_E2E_RUN_ID", "")
	t.Setenv("OCM_E2E_SUITE", "")
	t.Setenv("OCM_E2E_WORKERS", "")
	t.Setenv("OCM_E2E_ACTION", "")

	opts, err := loadRunOptions()

	require.NoError(t, err)
	assert.Equal(t, runOptions{RunID: "manual", Suite: suiteRegression, Workers: 1, Action: actionSeed}, opts)
}

// 验证 run ID 会先归一化为小写安全片段，供数据库 fixture 命名复用。
func TestLoadRunOptionsSanitizesRunID(t *testing.T) {
	t.Setenv("OCM_E2E_RUN_ID", " Run_AB C!! ")
	t.Setenv("OCM_E2E_SUITE", "quick")
	t.Setenv("OCM_E2E_WORKERS", "2")
	t.Setenv("OCM_E2E_ACTION", "seed")

	opts, err := loadRunOptions()

	require.NoError(t, err)
	assert.Equal(t, runOptions{RunID: "run-ab-c", Suite: suiteQuick, Workers: 2, Action: actionSeed}, opts)
}

// 验证 destructive cleanup 不接受需要归一化的原始 run ID，避免直接调用 binary 时删除另一个合法 run。
func TestLoadRunOptionsRejectsCleanupRunIDNormalization(t *testing.T) {
	t.Setenv("OCM_E2E_RUN_ID", "Run_A")
	t.Setenv("OCM_E2E_SUITE", "regression")
	t.Setenv("OCM_E2E_WORKERS", "1")
	t.Setenv("OCM_E2E_ACTION", "cleanup")

	_, err := loadRunOptions()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cleanup")
}

// 验证三个 worker 的组织、账号与实例命名空间互不相同。
func TestFixtureIdentitiesAreUniquePerWorker(t *testing.T) {
	items, err := fixtureIdentities(runOptions{RunID: "run-abc123", Suite: suiteRegression, Workers: 3, Action: actionSeed})

	require.NoError(t, err)
	require.Len(t, items, 3)
	assert.NotEqual(t, items[0].PlatformAdminLogin, items[1].PlatformAdminLogin)
	assert.NotEqual(t, items[0].OrgCode, items[1].OrgCode)
	assert.NotEqual(t, items[1].OrgAdminLogin, items[2].OrgAdminLogin)
	assert.NotEqual(t, items[0].AppName, items[2].AppName)
	assert.Equal(t, fixtureIdentity{
		RunID:              "run-abc123",
		WorkerIndex:        0,
		OrgName:            "e2e-run-abc123-w0",
		OrgCode:            "e2e-run-abc123-w0",
		PlatformAdminLogin: "e2e-run-abc123-w0-platform",
		OrgAdminLogin:      "e2e-run-abc123-w0-admin",
		OrgMemberLogin:     "e2e-run-abc123-w0-member",
		AppName:            "e2e-run-abc123-w0-app",
	}, items[0])
}

// 验证所有显式非法运行参数都会快速失败，避免生成不完整或越界的 fixture 池。
func TestLoadRunOptionsRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		runID   string
		suite   string
		workers string
		action  string
		message string
	}{
		// 全部由不安全字符组成的 run ID 清洗后为空，必须拒绝。
		{name: "run ID 清洗后为空", runID: "!!!", suite: "regression", workers: "1", action: "seed", message: "1 到 16"},
		// 超过 16 字符的 run ID 会导致数据库对象命名失控，必须拒绝。
		{name: "run ID 过长", runID: "12345678901234567", suite: "regression", workers: "1", action: "seed", message: "1 到 16"},
		// suite 仅允许 quick、regression、slow 三级枚举。
		{name: "suite 非法", runID: "run-a", suite: "nightly", workers: "1", action: "seed", message: "未知 OCM_E2E_SUITE"},
		// action 仅解析 seed 与后续清理契约的两个枚举。
		{name: "action 非法", runID: "run-a", suite: "regression", workers: "1", action: "drop", message: "未知 OCM_E2E_ACTION"},
		// worker 必须是整数，拒绝隐式解析为默认值。
		{name: "worker 非整数", runID: "run-a", suite: "regression", workers: "many", action: "seed", message: "1 到 4"},
		// worker 下界为 1，空池不能进入 seed 流程。
		{name: "worker 为零", runID: "run-a", suite: "regression", workers: "0", action: "seed", message: "1 到 4"},
		// worker 上界为 4，避免本地依赖被过量并发压垮。
		{name: "worker 超上限", runID: "run-a", suite: "regression", workers: "5", action: "seed", message: "1 到 4"},
		// slow 虽最终固定单 worker，也不能绕过显式 worker 值的合法性校验。
		{name: "slow 显式 worker 非法", runID: "run-a", suite: "slow", workers: "5", action: "seed", message: "1 到 4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 每个子测试独立注入完整环境，避免开发机变量影响参数解析结果。
			t.Setenv("OCM_E2E_RUN_ID", tt.runID)
			t.Setenv("OCM_E2E_SUITE", tt.suite)
			t.Setenv("OCM_E2E_WORKERS", tt.workers)
			t.Setenv("OCM_E2E_ACTION", tt.action)

			_, err := loadRunOptions()

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}
}

// 验证 slow 套件在合法显式并发值下仍固定为单 worker，隔离真实外部依赖。
func TestLoadRunOptionsForcesSlowToOneWorker(t *testing.T) {
	t.Setenv("OCM_E2E_RUN_ID", "run-a")
	t.Setenv("OCM_E2E_SUITE", "slow")
	t.Setenv("OCM_E2E_WORKERS", "4")
	t.Setenv("OCM_E2E_ACTION", "cleanup-expired")

	opts, err := loadRunOptions()

	require.NoError(t, err)
	assert.Equal(t, runOptions{RunID: "run-a", Suite: suiteSlow, Workers: 1, Action: actionCleanupExpired}, opts)
}

// 验证 OCM_E2E 守门：缺这个环境变量时命令必须非零退出，避免误操作隔离 E2E 数据。
func TestSeedE2E_RejectsMissingOCME2EFlag(t *testing.T) {
	t.Setenv("OCM_E2E", "")

	err := requireE2EGuard()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "OCM_E2E=1")
}

// 验证 E2E fixture 使用 manager 已配置的首个 Hermes 镜像 ID，避免隐藏 app 初始化引用不存在的硬编码镜像。
func TestE2ERuntimeImageIDUsesConfiguredImage(t *testing.T) {
	cfg := config.Config{Hermes: config.HermesConfig{RuntimeImages: []config.RuntimeImageConfig{
		{ID: "v2026.7.1", Ref: "registry.local/hermes:v2026.7.1"},
	}}}

	imageID, err := e2eRuntimeImageID(cfg)

	require.NoError(t, err)
	assert.Equal(t, "v2026.7.1", imageID)
}

// 验证未配置 Hermes runtime image 时种子命令快速失败，避免生成永远无法启动的 AICC 隐藏 app。
func TestE2ERuntimeImageIDRejectsEmptyConfig(t *testing.T) {
	_, err := e2eRuntimeImageID(config.Config{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "runtime image")
}

// 验证 E2E new-api 用户名同时隔离 run 与 worker，并满足上游 12 字符长度限制。
func TestE2ENewAPIUsernameIsRunAndWorkerScoped(t *testing.T) {
	first := e2eNewAPIUsername("run-a", 0)
	otherWorker := e2eNewAPIUsername("run-a", 1)
	otherRun := e2eNewAPIUsername("run-b", 0)

	assert.Equal(t, "e6c9da38c00", first)
	assert.NotEqual(t, first, otherWorker)
	assert.NotEqual(t, first, otherRun)
	assert.LessOrEqual(t, len(first), 12)
}

// 验证确定性用户名发生碰撞或遗留占用时安全失败，绝不删除未知上游用户。
func TestRequireE2ENewAPIUsernameAvailableRejectsExistingUser(t *testing.T) {
	stub := &newAPIUsernameLookupStub{user: newapi.User{ID: 99, Username: "e6c9da38c00"}}

	err := requireE2ENewAPIUsernameAvailable(context.Background(), stub, "e6c9da38c00")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "已被占用")
	assert.Zero(t, stub.deleteCalls)
}

// 验证真实 FNV-1a 32-bit 碰撞只会触发占用失败，绝不把另一 run 的用户当作自身资源删除。
func TestRequireE2ENewAPIUsernameAvailableRejectsRealHashCollision(t *testing.T) {
	first := e2eNewAPIUsername("6pcfutoze5", 0)
	collision := e2eNewAPIUsername("43epkzyhsv", 0)
	require.Equal(t, first, collision)
	stub := &newAPIUsernameLookupStub{user: newapi.User{ID: 101, Username: first}}

	err := requireE2ENewAPIUsernameAvailable(context.Background(), stub, collision)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "已被占用")
	assert.Zero(t, stub.deleteCalls)
}

// 验证用户名确实不存在时允许创建流程继续，且不产生任何删除调用。
func TestRequireE2ENewAPIUsernameAvailableAcceptsNotFound(t *testing.T) {
	stub := &newAPIUsernameLookupStub{err: newapi.ErrNotFound}

	err := requireE2ENewAPIUsernameAvailable(context.Background(), stub, "e6c9da38c00")

	require.NoError(t, err)
	assert.Zero(t, stub.deleteCalls)
}

// 验证没有本地 new-api ID 时清理无需管理配置或网络调用即可安全返回。
func TestCleanupNewAPIUsersWithoutIDsDoesNotRequireConfig(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectQuery(`REGEXP_LIKE\(code, \?, 'c'\)`).
		WithArgs("e2e-a-w%", "^e2e-a-w[0-3](-c-[a-z0-9-]+)?$").
		WillReturnRows(sqlmock.NewRows([]string{"newapi_user_id"}))
	mock.ExpectClose()

	err = cleanupNewAPIUsers(context.Background(), db, config.Config{}, "a")

	require.NoError(t, err)
	require.NoError(t, db.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

// 验证 run 组织选择器包含 worker 边界，短 run 不会覆盖带相同前缀的长 run。
func TestRunOrgPatternUsesWorkerBoundary(t *testing.T) {
	shortPattern, err := runOrgPattern("a")
	require.NoError(t, err)
	longPattern, err := runOrgPattern("a-b")
	require.NoError(t, err)

	assert.Equal(t, "e2e-a-w%", shortPattern)
	assert.Equal(t, "e2e-a-b-w%", longPattern)
	assert.False(t, strings.HasPrefix("e2e-a-b-w0", strings.TrimSuffix(shortPattern, "%")))
}

// 验证 LIKE 预筛之后的正则边界会拒绝名称中继续携带 worker 片段的另一合法 run。
func TestRunOwnedRegexRejectsWorkerLikeRunSuffix(t *testing.T) {
	orgBoundary, err := runOrgRegexp("a")
	require.NoError(t, err)
	adminBoundary, err := runPlatformAdminRegexp("a")
	require.NoError(t, err)
	versionBoundary, err := runAssistantVersionRegexp("a")
	require.NoError(t, err)

	assert.Regexp(t, orgBoundary, "e2e-a-w0")
	assert.Regexp(t, orgBoundary, "e2e-a-w0-c-123456")
	assert.NotRegexp(t, orgBoundary, "e2e-a-w1-w0")
	assert.NotRegexp(t, adminBoundary, "e2e-a-w1-w0-platform")
	assert.NotRegexp(t, versionBoundary, "e2e-a-w1-w0-version")
}

// 验证清理选择器拒绝未经过运行参数清洗的不安全 run ID，避免 LIKE 通配符扩大删除范围。
func TestRunOrgPatternRejectsUnsafeRunID(t *testing.T) {
	tests := []struct {
		name  string
		runID string
	}{
		// 空 run ID 没有可证明的租户边界，必须拒绝。
		{name: "空值", runID: ""},
		// 百分号是 LIKE 多字符通配符，必须拒绝。
		{name: "百分号", runID: "run%"},
		// 下划线是 LIKE 单字符通配符，必须拒绝。
		{name: "下划线", runID: "run_a"},
		// 大写字符不是 loadRunOptions 清洗后的安全形式，必须拒绝。
		{name: "大写字符", runID: "Run-a"},
		// 超过 16 字符不符合 fixture 命名契约，必须拒绝。
		{name: "超长", runID: "12345678901234567"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 每个子测试验证一种可能扩大或模糊清理边界的输入。
			_, err := runOrgPattern(tt.runID)

			require.Error(t, err)
		})
	}
}

// 验证平台管理员与助手版本选择器都绑定 worker 边界，且永久本地 admin 永不匹配。
func TestRunOwnedNamePatternsAreScoped(t *testing.T) {
	adminPattern, err := runPlatformAdminPattern("a")
	require.NoError(t, err)
	versionPattern, err := runAssistantVersionPattern("a")
	require.NoError(t, err)

	assert.Equal(t, "e2e-a-w%-platform", adminPattern)
	assert.Equal(t, "e2e-a-w%-version", versionPattern)
	assert.NotEqual(t, "admin", adminPattern)
	assert.False(t, strings.HasPrefix("e2e-a-b-w0-platform", "e2e-a-w"))
}

// 验证组织 new-api 用户 ID 解析会忽略空值、保留合法数字并拒绝格式错误。
func TestParseE2ENewAPIUserIDs(t *testing.T) {
	ids, err := parseE2ENewAPIUserIDs([]string{"", "  ", "17", " 23 "})
	require.NoError(t, err)
	assert.Equal(t, []int64{17, 23}, ids)

	_, err = parseE2ENewAPIUserIDs([]string{"12x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "12x")
}

// 验证派生 fixture 组织能还原包含连字符的 owning run，非 fixture code 必须安全拒绝。
func TestParseFixtureOrgRunID(t *testing.T) {
	runID, ok := parseFixtureOrgRunID("e2e-run-abc-w0-c-123456")
	assert.True(t, ok)
	assert.Equal(t, "run-abc", runID)
	// run 本身含 worker 风格片段时，必须选择最后一个真实 worker 边界。
	workerLikeRunID, ok := parseFixtureOrgRunID("e2e-a-w1-w0")
	assert.True(t, ok)
	assert.Equal(t, "a-w1", workerLikeRunID)

	tests := []struct {
		name string
		code string
	}{
		// 永久本地组织不属于任何 E2E run。
		{name: "非 fixture", code: "local-org"},
		// 缺少 worker 数字的伪前缀不能被解释为 fixture。
		{name: "worker 缺失", code: "e2e-run-abc-w-c-123456"},
		// worker 后缀不符合 fixture 或派生组织契约时必须拒绝。
		{name: "未知后缀", code: "e2e-run-abc-w0-other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 每个子测试覆盖一种不得进入过期清理集合的非 fixture code。
			_, ok := parseFixtureOrgRunID(tt.code)

			assert.False(t, ok)
		})
	}
}

// 验证组织清理 SQL 全部参数化，并锁定关键 child-before-parent 外键顺序。
func TestCleanupStatementsAreParameterizedAndFKOrdered(t *testing.T) {
	statements := cleanupStatements("org-id")
	require.NotEmpty(t, statements)

	var queries strings.Builder
	for index, statement := range statements {
		// 每条清理语句都必须含占位符，禁止把组织 ID 拼接进 SQL。
		t.Run(fmt.Sprintf("语句-%02d", index), func(t *testing.T) {
			assert.Contains(t, statement.query, "?")
			assert.Equal(t, strings.Count(statement.query, "?"), len(statement.args))
		})
		queries.WriteString(statement.query)
		queries.WriteByte('\n')
	}

	all := queries.String()
	// 会话智能处理的子表必须完整覆盖。
	assert.Contains(t, all, "aicc_message_sources")
	assert.Contains(t, all, "aicc_session_contexts")
	assert.Contains(t, all, "aicc_session_intents")
	assert.Contains(t, all, "aicc_intent_analysis_retries")
	// AICC 业务数据必须完整覆盖，不能依赖关闭外键后清空父表。
	assert.Contains(t, all, "aicc_feedback")
	assert.Contains(t, all, "aicc_message_tasks")
	assert.Contains(t, all, "aicc_lead_values")
	assert.Contains(t, all, "aicc_leads")
	assert.Contains(t, all, "aicc_images")
	assert.Contains(t, all, "aicc_messages")
	assert.Contains(t, all, "aicc_sessions")
	assert.Contains(t, all, "aicc_blocked_visitors")
	assert.Contains(t, all, "aicc_agent_settings")
	assert.Contains(t, all, "aicc_lead_fields")
	assert.Contains(t, all, "aicc_agent_knowledge")
	assert.Contains(t, all, "aicc_agents")
	// 组织、app 及扩展能力表必须都由 scoped 条件删除。
	assert.Contains(t, all, "organization_industry_knowledge_bases")
	// 版本行业绑定是跨组织全局资源，只能由 run 精确版本事务统一删除。
	assert.NotContains(t, all, "assistant_version_industry_knowledge_bases")
	assert.Contains(t, all, "published_sites")
	assert.Contains(t, all, "conversation_files")
	assert.Contains(t, all, "app_skills")
	assert.Contains(t, all, "channel_bindings")
	assert.Contains(t, all, "ragflow_documents")
	assert.Contains(t, all, "ragflow_datasets")
	assert.Contains(t, all, "custom_skill_targets")
	assert.Contains(t, all, "custom_skills")
	assert.Contains(t, all, "skill_ticket_messages")
	assert.Contains(t, all, "skill_tickets")
	assert.Contains(t, all, "refresh_tokens")
	assert.Contains(t, all, "recharge_records")
	assert.Contains(t, all, "audit_logs")
	assert.Contains(t, all, "jobs")
	assert.Contains(t, all, "apps")
	assert.Contains(t, all, "users")
	assert.Contains(t, all, "org_web_publish_config")
	assert.Contains(t, all, "organizations")
	// current-run 工单派生技能的跨组织 target 必须在技能父记录前精确删除。
	assert.Equal(t, 2, strings.Count(all, "DELETE FROM custom_skill_targets"))
	assert.Contains(t, all, "current_skill.name = custom_skill_targets.custom_skill_name")
	assert.Contains(t, all, "NOT EXISTS")
	assert.Contains(t, all, "other.ticket_id NOT IN")
	assert.Less(t, strings.Index(all, "current_skill.name ="), strings.Index(all, "DELETE FROM custom_skills"))
	// 关键父表删除必须严格晚于所有直接子表删除。
	assert.Less(t, strings.Index(all, "DELETE FROM aicc_message_sources"), strings.Index(all, "DELETE FROM aicc_messages"))
	assert.Less(t, strings.Index(all, "DELETE FROM aicc_lead_values"), strings.Index(all, "DELETE FROM aicc_leads"))
	assert.Less(t, strings.Index(all, "DELETE FROM aicc_sessions"), strings.Index(all, "DELETE FROM aicc_agents"))
	assert.Less(t, strings.Index(all, "DELETE FROM apps WHERE"), strings.Index(all, "DELETE FROM organizations WHERE"))
	// 当前 run 组织产生的平台管理员用户引用必须在平台管理员事务开始前按组织边界清掉。
	assert.Less(t, strings.Index(all, "DELETE FROM recharge_records"), strings.Index(all, "DELETE FROM users WHERE"))
	assert.Less(t, strings.Index(all, "DELETE FROM skill_ticket_messages"), strings.Index(all, "DELETE FROM users WHERE"))
	assert.Less(t, strings.Index(all, "DELETE FROM skill_tickets"), strings.Index(all, "DELETE FROM users WHERE"))
	assert.Less(t, strings.Index(all, "DELETE FROM apps WHERE"), strings.Index(all, "DELETE FROM users WHERE"))
}

// 验证跨组织 custom skill target 仅在技能名完全归属当前 run ticket 时删除。
func TestCustomSkillTargetCleanupRequiresExclusiveTicketOwnership(t *testing.T) {
	statements := cleanupStatements("org-id")
	var targetCleanup cleanupStatement
	for _, statement := range statements {
		// 精确选择包含同名所有权判定的跨组织 target 清理语句。
		if strings.Contains(statement.query, "current_skill.name = custom_skill_targets.custom_skill_name") {
			targetCleanup = statement
			break
		}
	}
	require.NotEmpty(t, targetCleanup.query)

	// EXISTS 当前 ticket 技能证明唯一名称时会进入删除候选。
	assert.Contains(t, targetCleanup.query, "current_skill.ticket_id IN")
	// NOT EXISTS 非当前 ticket 同名技能证明同名多来源时会保留全部 name 级 target。
	assert.Contains(t, targetCleanup.query, "NOT EXISTS")
	assert.Contains(t, targetCleanup.query, "other.ticket_id NOT IN")
	assert.Equal(t, []any{"org-id", "org-id"}, targetCleanup.args)
}

// 验证平台管理员清理只处理 actor 专属依赖，版本资源仍由精确 run 事务负责。
func TestPlatformAdminCleanupStatementsStayActorScoped(t *testing.T) {
	statements, err := platformAdminCleanupStatements("a")
	require.NoError(t, err)

	var queries strings.Builder
	for index, statement := range statements {
		// 每个子测试验证平台管理员依赖语句仍完全参数化。
		t.Run(fmt.Sprintf("平台管理员语句-%02d", index), func(t *testing.T) {
			assert.Contains(t, statement.query, "?")
			assert.Equal(t, strings.Count(statement.query, "?"), len(statement.args))
		})
		queries.WriteString(statement.query)
		queries.WriteByte('\n')
	}

	all := queries.String()
	assert.NotContains(t, all, " REGEXP ?")
	assert.Contains(t, all, "REGEXP_LIKE(username, ?, 'c')")
	assert.Contains(t, all, "DELETE FROM platform_skills")
	// 版本及其行业绑定只能由 cleanupAssistantVersions 的精确命名事务处理。
	assert.NotContains(t, all, "assistant_version_industry_knowledge_bases")
	assert.NotContains(t, all, "assistant_versions")
	assert.Less(t, strings.Index(all, "DELETE FROM platform_skills"), strings.Index(all, "DELETE FROM users"))
}

// 验证隔离管理员仍被其他 run 数据引用时，父用户删除失败会回滚本 run actor 子资源清理。
func TestCleanupPlatformAdminsRollsBackOnCrossRunUserReference(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	statements, err := platformAdminCleanupStatements("a")
	require.NoError(t, err)
	require.Len(t, statements, 4)

	mock.ExpectBegin()
	for _, statement := range statements[:3] {
		// 所有明确属于当前 run actor 的 child 清理先成功，但仍保持在同一未提交事务中。
		mock.ExpectExec(regexp.QuoteMeta(statement.query)).
			WithArgs("e2e-a-w%-platform", "^e2e-a-w[0-3]-platform$").
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	// 任何未被当前 run 组织清理覆盖的跨 run FK 都必须阻止父用户删除。
	mock.ExpectExec(regexp.QuoteMeta(statements[3].query)).
		WithArgs("e2e-a-w%-platform", "^e2e-a-w[0-3]-platform$").
		WillReturnError(errors.New("foreign key constraint fails"))
	mock.ExpectRollback()
	mock.ExpectClose()

	err = cleanupPlatformAdmins(context.Background(), db, "a")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "foreign key")
	require.NoError(t, db.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

// 验证 run 命名版本仍被其他 app 引用时，行业绑定删除与父版本删除会整体回滚。
func TestCleanupAssistantVersionsRollsBackOnDependencyFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	pattern := "e2e-a-w%-version"
	boundary := "^e2e-a-w[0-3]-version$"

	mock.ExpectBegin()
	// 行业绑定 child 删除先成功，但必须保持在未提交事务中。
	mock.ExpectExec(`(?s)DELETE FROM assistant_version_industry_knowledge_bases.*REGEXP_LIKE\(name, \?, 'c'\)`).
		WithArgs(pattern, boundary).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// 版本仍被其他 app 引用时由真实 FK 拒绝父表删除。
	mock.ExpectExec(`(?s)DELETE FROM assistant_versions.*REGEXP_LIKE\(name, \?, 'c'\)`).
		WithArgs(pattern, boundary).
		WillReturnError(errors.New("foreign key constraint fails"))
	mock.ExpectRollback()
	mock.ExpectClose()

	err = cleanupAssistantVersions(context.Background(), db, "a")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "foreign key")
	require.NoError(t, db.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

// 验证 E2E 助手版本使用 local-init-models 已配置的 DeepSeek 渠道模型，避免公开问答落到不存在的 gpt-4。
func TestE2EMainModelUsesLocalAvailableChannel(t *testing.T) {
	assert.Equal(t, "deepseek-chat", e2eMainModel())
}

// 验证临时 E2E 用户会获得正额度，避免真实 Hermes 问答在 new-api 余额校验阶段被拒绝。
func TestE2ENewAPICreditAmountIsPositive(t *testing.T) {
	assert.Positive(t, e2eNewAPICreditAmount())
}

// 验证 new-api 登录短暂限流时 seed 按序退避并最终返回 access token。
func TestRetryE2EAccessTokenRetriesRateLimit(t *testing.T) {
	attempts := 0
	var delays []time.Duration
	token, err := retryE2EAccessToken(context.Background(), func() (string, error) {
		attempts++
		if attempts < 3 {
			return "", errors.New("上游服务异常: status=429")
		}
		return "access-token", nil
	}, func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, "access-token", token)
	assert.Equal(t, 3, attempts)
	assert.Equal(t, []time.Duration{2 * time.Second, 4 * time.Second}, delays)
}

// 验证非限流错误不会重试，避免掩盖 new-api 配置、鉴权或协议故障。
func TestRetryE2EAccessTokenRejectsOtherErrors(t *testing.T) {
	attempts := 0
	expected := errors.New("上游鉴权失败: status=401")
	_, err := retryE2EAccessToken(context.Background(), func() (string, error) {
		attempts++
		return "", expected
	}, func(context.Context, time.Duration) error {
		// 非限流错误进入等待代表重试分类错误，立即停止当前测试。
		require.FailNow(t, "非限流错误不应进入等待")
		return nil
	})

	require.ErrorIs(t, err, expected)
	assert.Equal(t, 1, attempts)
}
