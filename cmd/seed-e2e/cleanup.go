// cleanup.go 实现按 E2E run 命名边界删除临时数据的安全清理流程。
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"oc-manager/internal/config"
	"oc-manager/internal/integrations/newapi"
)

// safeCleanupRunID 只接受 loadRunOptions 清洗后的 run ID，排除 SQL LIKE 通配符。
var safeCleanupRunID = regexp.MustCompile(`^[a-z0-9-]{1,16}$`)

// fixtureOrgCodePattern 从 fixture 或其派生组织 code 中提取 owning run。
var fixtureOrgCodePattern = regexp.MustCompile(`^e2e-([a-z0-9-]{1,16})-w[0-3](?:-c-[a-z0-9-]+)?$`)

// cleanupStatement 保存单条参数化删除语句及其参数，顺序即外键删除顺序。
type cleanupStatement struct {
	// query 必须含占位符，禁止将组织标识拼接进 SQL。
	query string
	// args 与 query 占位符逐一对应。
	args []any
}

// runOrgPattern 返回只覆盖指定 run worker 及其派生组织的 LIKE 模式。
func runOrgPattern(runID string) (string, error) {
	if !safeCleanupRunID.MatchString(runID) {
		return "", fmt.Errorf("run ID 必须为 1 到 16 个小写字母、数字或连字符")
	}
	return "e2e-" + runID + "-w%", nil
}

// runOrgRegexp 精确限定 worker 0..3 及可选派生组织，阻止 LIKE 前缀命中另一合法 run。
func runOrgRegexp(runID string) (string, error) {
	if _, err := runOrgPattern(runID); err != nil {
		return "", err
	}
	return "^e2e-" + regexp.QuoteMeta(runID) + `-w[0-3](-c-[a-z0-9-]+)?$`, nil
}

// runPlatformAdminPattern 返回指定 run 的隔离平台管理员 LIKE 模式。
func runPlatformAdminPattern(runID string) (string, error) {
	if _, err := runOrgPattern(runID); err != nil {
		return "", err
	}
	return "e2e-" + runID + "-w%-platform", nil
}

// runPlatformAdminRegexp 精确限定隔离平台管理员的 worker 与结尾边界。
func runPlatformAdminRegexp(runID string) (string, error) {
	if _, err := runOrgPattern(runID); err != nil {
		return "", err
	}
	return "^e2e-" + regexp.QuoteMeta(runID) + `-w[0-3]-platform$`, nil
}

// runAssistantVersionPattern 返回指定 run 的临时助手版本 LIKE 模式。
func runAssistantVersionPattern(runID string) (string, error) {
	if _, err := runOrgPattern(runID); err != nil {
		return "", err
	}
	return "e2e-" + runID + "-w%-version", nil
}

// runAssistantVersionRegexp 精确限定临时助手版本的 worker 与结尾边界。
func runAssistantVersionRegexp(runID string) (string, error) {
	if _, err := runOrgPattern(runID); err != nil {
		return "", err
	}
	return "^e2e-" + regexp.QuoteMeta(runID) + `-w[0-3]-version$`, nil
}

// parseE2ENewAPIUserIDs 将数据库字符串 ID 转为 new-api 客户端需要的正整数。
func parseE2ENewAPIUserIDs(values []string) ([]int64, error) {
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		id, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("非法 new-api 用户 ID %q", value)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// parseFixtureOrgRunID 安全识别 fixture 组织及其 -c- 派生组织的 owning run。
func parseFixtureOrgRunID(code string) (string, bool) {
	matches := fixtureOrgCodePattern.FindStringSubmatch(code)
	if len(matches) != 2 || !safeCleanupRunID.MatchString(matches[1]) {
		return "", false
	}
	return matches[1], true
}

// cleanupNewAPIUsers 先删除指定 run 组织关联的上游用户；失败时不改 manager 数据。
func cleanupNewAPIUsers(ctx context.Context, db *sql.DB, cfg config.Config, runID string) error {
	pattern, err := runOrgPattern(runID)
	if err != nil {
		return err
	}
	boundary, err := runOrgRegexp(runID)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT COALESCE(newapi_user_id, '')
		FROM organizations
		WHERE code LIKE ? AND REGEXP_LIKE(code, ?, 'c')`, pattern, boundary)
	if err != nil {
		return fmt.Errorf("查询 run %s 的 new-api 用户: %w", runID, err)
	}
	values, err := scanStringRows(rows)
	if err != nil {
		return fmt.Errorf("读取 run %s 的 new-api 用户: %w", runID, err)
	}
	ids, err := parseE2ENewAPIUserIDs(values)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if strings.TrimSpace(cfg.NewAPI.BaseURL) == "" || strings.TrimSpace(cfg.NewAPI.AdminToken) == "" || cfg.NewAPI.AdminUserID == 0 {
		return errors.New("new-api 管理配置不完整")
	}
	client := newapi.NewClient(cfg.NewAPI.BaseURL, cfg.NewAPI.AdminToken, cfg.NewAPI.AdminUserID)
	for _, id := range ids {
		if err := client.DeleteUser(ctx, id); err != nil && !errors.Is(err, newapi.ErrNotFound) {
			return fmt.Errorf("删除 new-api E2E 用户 %d: %w", id, err)
		}
	}
	return nil
}

// scanStringRows 读取单列字符串结果，并在所有返回路径严谨关闭游标。
func scanStringRows(rows *sql.Rows) ([]string, error) {
	values := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			_ = rows.Close()
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return values, nil
}

// cleanupRun 删除指定 run 的所有组织，再删除共享的版本和隔离平台管理员。
func cleanupRun(ctx context.Context, db *sql.DB, runID string) error {
	pattern, err := runOrgPattern(runID)
	if err != nil {
		return err
	}
	boundary, err := runOrgRegexp(runID)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id FROM organizations
		WHERE code LIKE ? AND REGEXP_LIKE(code, ?, 'c')
		ORDER BY id`, pattern, boundary)
	if err != nil {
		return fmt.Errorf("查询 run %s 的组织: %w", runID, err)
	}
	orgIDs, err := scanStringRows(rows)
	if err != nil {
		return fmt.Errorf("读取 run %s 的组织: %w", runID, err)
	}
	for _, orgID := range orgIDs {
		if err := cleanupOrganization(ctx, db, orgID); err != nil {
			return fmt.Errorf("清理组织 %s: %w", orgID, err)
		}
	}
	// 版本可能被多个本 run 组织 app 引用，因此必须等所有组织 app 删除后统一清理。
	if err := cleanupAssistantVersions(ctx, db, runID); err != nil {
		return err
	}
	return cleanupPlatformAdmins(ctx, db, runID)
}

// cleanupOrganization 在单事务内按 child-before-parent 顺序删除一个 fixture 组织。
func cleanupOrganization(ctx context.Context, db *sql.DB, orgID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启组织清理事务: %w", err)
	}
	for _, statement := range cleanupStatements(orgID) {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("执行 scoped cleanup query %q: %w", compactQuery(statement.query), err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交组织清理事务: %w", err)
	}
	return nil
}

// compactQuery 压缩 SQL 空白，避免错误上下文被多行格式干扰。
func compactQuery(query string) string {
	return strings.Join(strings.Fields(query), " ")
}

// cleanupStatements 返回真实 schema 对应的参数化外键安全删除顺序。
func cleanupStatements(orgID string) []cleanupStatement {
	appSubquery := `SELECT id FROM apps WHERE org_id = ?`
	userSubquery := `SELECT id FROM users WHERE org_id = ?`
	agentSubquery := `SELECT id FROM aicc_agents WHERE org_id = ?`
	sessionSubquery := `SELECT id FROM aicc_sessions WHERE org_id = ?`
	messageSubquery := `SELECT id FROM aicc_messages WHERE session_id IN (` + sessionSubquery + `)`
	ticketSubquery := `SELECT id FROM skill_tickets WHERE org_id = ?`
	return []cleanupStatement{
		{query: `DELETE FROM aicc_message_sources WHERE message_id IN (` + messageSubquery + `)`, args: []any{orgID}},
		{query: `DELETE FROM aicc_session_contexts WHERE session_id IN (` + sessionSubquery + `)`, args: []any{orgID}},
		{query: `DELETE FROM aicc_session_intents WHERE session_id IN (` + sessionSubquery + `)`, args: []any{orgID}},
		{query: `DELETE FROM aicc_intent_analysis_retries WHERE session_id IN (` + sessionSubquery + `)`, args: []any{orgID}},
		{query: `DELETE FROM aicc_feedback WHERE session_id IN (` + sessionSubquery + `)`, args: []any{orgID}},
		{query: `DELETE FROM aicc_message_tasks WHERE org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM aicc_lead_values WHERE org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM aicc_leads WHERE org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM aicc_images WHERE org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM aicc_messages WHERE session_id IN (` + sessionSubquery + `)`, args: []any{orgID}},
		{query: `DELETE FROM aicc_sessions WHERE org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM aicc_blocked_visitors WHERE org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM aicc_agent_settings WHERE agent_id IN (` + agentSubquery + `)`, args: []any{orgID}},
		{query: `DELETE FROM aicc_lead_fields WHERE agent_id IN (` + agentSubquery + `)`, args: []any{orgID}},
		{query: `DELETE FROM aicc_agent_knowledge WHERE agent_org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM aicc_agents WHERE org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM organization_industry_knowledge_bases WHERE org_id = ?`, args: []any{orgID}},
		// assistant_version_industry_knowledge_bases 是版本级全局绑定，只能由 run 精确版本事务统一处理。
		{query: `DELETE FROM published_sites WHERE org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM conversation_files WHERE app_id IN (` + appSubquery + `)`, args: []any{orgID}},
		{query: `DELETE FROM app_skills WHERE app_id IN (` + appSubquery + `)`, args: []any{orgID}},
		{query: `DELETE FROM channel_bindings WHERE app_id IN (` + appSubquery + `)`, args: []any{orgID}},
		{query: `DELETE FROM ragflow_documents WHERE org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM ragflow_datasets WHERE org_id = ?`, args: []any{orgID}},
		// target 只按 skill name 建模；仅当同名技能全部来自当前 run ticket 时删除跨组织授权，避免误删其他 ticket 同名技能的可见范围。
		{query: `DELETE FROM custom_skill_targets
			WHERE EXISTS (
				SELECT 1 FROM custom_skills current_skill
				WHERE current_skill.name = custom_skill_targets.custom_skill_name
					AND current_skill.ticket_id IN (` + ticketSubquery + `)
			)
			AND NOT EXISTS (
				SELECT 1 FROM custom_skills other
				WHERE other.name = custom_skill_targets.custom_skill_name
					AND other.ticket_id NOT IN (` + ticketSubquery + `)
			)`, args: []any{orgID, orgID}},
		{query: `DELETE FROM custom_skill_targets WHERE org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM custom_skills WHERE ticket_id IN (` + ticketSubquery + `)`, args: []any{orgID}},
		{query: `DELETE FROM skill_ticket_messages WHERE ticket_id IN (` + ticketSubquery + `)`, args: []any{orgID}},
		{query: `DELETE FROM skill_tickets WHERE org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM refresh_tokens WHERE user_id IN (` + userSubquery + `)`, args: []any{orgID}},
		{query: `DELETE FROM recharge_records WHERE org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM audit_logs WHERE org_id = ? OR actor_id IN (` + userSubquery + `)`, args: []any{orgID, orgID}},
		// jobs 的当前 schema 没有 org_id/app_id 列，只能从 payload_json 的调度归属字段精确识别。
		{query: `DELETE FROM jobs WHERE JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.org_id')) = ? OR JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.app_id')) IN (` + appSubquery + `)`, args: []any{orgID, orgID}},
		{query: `DELETE FROM apps WHERE org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM users WHERE org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM org_web_publish_config WHERE org_id = ?`, args: []any{orgID}},
		{query: `DELETE FROM organizations WHERE id = ?`, args: []any{orgID}},
	}
}

// cleanupAssistantVersions 删除指定 run 的版本行业绑定，再删除不再被 app 引用的版本。
func cleanupAssistantVersions(ctx context.Context, db *sql.DB, runID string) error {
	pattern, err := runAssistantVersionPattern(runID)
	if err != nil {
		return err
	}
	boundary, err := runAssistantVersionRegexp(runID)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启 run %s 助手版本清理事务: %w", runID, err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM assistant_version_industry_knowledge_bases
		WHERE version_id IN (
			SELECT id FROM assistant_versions WHERE name LIKE ? AND REGEXP_LIKE(name, ?, 'c')
		)`, pattern, boundary); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("清理 run %s 的助手版本知识库绑定: %w", runID, err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM assistant_versions
		WHERE name LIKE ? AND REGEXP_LIKE(name, ?, 'c')`, pattern, boundary); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("清理 run %s 的助手版本: %w", runID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 run %s 助手版本清理事务: %w", runID, err)
	}
	return nil
}

// cleanupPlatformAdmins 删除指定 run 的平台管理员依赖，再限定 org_id IS NULL 删除账号。
func cleanupPlatformAdmins(ctx context.Context, db *sql.DB, runID string) error {
	statements, err := platformAdminCleanupStatements(runID)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启 run %s 平台管理员清理事务: %w", runID, err)
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("清理 run %s 平台管理员 query %q: %w", runID, compactQuery(statement.query), err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 run %s 平台管理员清理事务: %w", runID, err)
	}
	return nil
}

// platformAdminCleanupStatements 返回平台管理员全部真实 FK 依赖与父用户的安全清理顺序。
func platformAdminCleanupStatements(runID string) ([]cleanupStatement, error) {
	pattern, err := runPlatformAdminPattern(runID)
	if err != nil {
		return nil, err
	}
	boundary, err := runPlatformAdminRegexp(runID)
	if err != nil {
		return nil, err
	}
	adminSubquery := `SELECT id FROM users WHERE org_id IS NULL AND username LIKE ? AND REGEXP_LIKE(username, ?, 'c')`
	args := func() []any { return []any{pattern, boundary} }
	// cleanupRun 会先逐组织清掉 apps、recharge_records、skill_tickets/messages 等当前 run 用户引用。
	// 这里不再按 actor 扩大删除这些业务表；若仍有其他 run 的 FK 引用，最终用户删除会失败并回滚本事务。
	return []cleanupStatement{
		{query: `DELETE FROM audit_logs WHERE actor_id IN (` + adminSubquery + `)`, args: args()},
		{query: `DELETE FROM refresh_tokens WHERE user_id IN (` + adminSubquery + `)`, args: args()},
		// 隔离管理员是 run 专属 actor，其上传的平台技能也归属该 run；只按 actor ID 删除，绝不匹配其他共享资源。
		{query: `DELETE FROM platform_skills WHERE uploaded_by IN (` + adminSubquery + `)`, args: args()},
		// 助手版本只由 cleanupAssistantVersions 按 run 精确名称处理；任何残留 created_by FK 会安全阻止用户删除。
		{query: `DELETE FROM users WHERE org_id IS NULL AND username LIKE ? AND REGEXP_LIKE(username, ?, 'c')`, args: args()},
	}, nil
}

// cleanupExpiredRuns 找出 cutoff 前 fixture 组织的 owning run，并逐 run 先删上游再删本地。
func cleanupExpiredRuns(ctx context.Context, db *sql.DB, cfg config.Config, cutoff time.Time) error {
	rows, err := db.QueryContext(ctx, `
		SELECT code
		FROM organizations
		WHERE created_at < ? AND code LIKE ?
		ORDER BY code`, cutoff, "e2e-%")
	if err != nil {
		return fmt.Errorf("查询过期 E2E 组织: %w", err)
	}
	codes, err := scanStringRows(rows)
	if err != nil {
		return fmt.Errorf("读取过期 E2E 组织: %w", err)
	}
	runs := make(map[string]struct{})
	for _, code := range codes {
		// 非 fixture code 只会被忽略，绝不从不可信后缀推断删除边界。
		if runID, ok := parseFixtureOrgRunID(code); ok {
			runs[runID] = struct{}{}
		}
	}
	for _, runID := range sortedRunIDs(runs) {
		if err := cleanupNewAPIUsers(ctx, db, cfg, runID); err != nil {
			return fmt.Errorf("清理过期 run %s 的 new-api 用户: %w", runID, err)
		}
		if err := cleanupRun(ctx, db, runID); err != nil {
			return fmt.Errorf("清理过期 run %s 的 manager 数据: %w", runID, err)
		}
	}
	return nil
}

// sortedRunIDs 将去重集合稳定排序，保证清理和错误定位可重复。
func sortedRunIDs(runs map[string]struct{}) []string {
	result := make([]string, 0, len(runs))
	for runID := range runs {
		result = append(result, runID)
	}
	sort.Strings(result)
	return result
}
