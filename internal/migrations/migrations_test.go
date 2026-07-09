// Package migrations 的默认测试主要校验 embed.FS 内容；
// 真实 MySQL 迁移执行测试通过环境变量显式开启，避免影响日常单元测试速度。
package migrations

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFS_ContainsUpAndDownPairs 验证FS包含Up并Down配对的预期行为场景。
func TestFS_ContainsUpAndDownPairs(t *testing.T) {
	entries, err := fs.ReadDir(FS, ".")
	require.NoError(t, err)
	// 每个 up 迁移都必须有同版本 down 文件，保证 cmd/migrate down 可回退一个版本。
	ups := make(map[string]struct{})
	downs := make(map[string]struct{})
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), ".up.sql"):
			ups[strings.TrimSuffix(e.Name(), ".up.sql")] = struct{}{}
		case strings.HasSuffix(e.Name(), ".down.sql"):
			downs[strings.TrimSuffix(e.Name(), ".down.sql")] = struct{}{}
		}
	}
	require.NotEqual(t, 0, len(ups))
	for version := range ups {
		if _, ok := downs[version]; !ok {
			t.Fatalf("迁移版本 %s 缺少 down 文件", version)
		}
	}
	for version := range downs {
		if _, ok := ups[version]; !ok {
			t.Fatalf("迁移版本 %s 缺少 up 文件", version)
		}
	}
}

// TestMigrationsIncludeIndustryKnowledge 验证行业知识库迁移已进入嵌入迁移集合，避免新增 SQL 文件遗漏到发布包。
func TestMigrationsIncludeIndustryKnowledge(t *testing.T) {
	src, err := iofs.New(FS, ".")
	require.NoError(t, err)
	defer src.Close()

	// 版本 7 是行业知识库迁移；First/Next 能防止迁移文件命名或嵌入路径缺失。
	first, err := src.First()
	require.NoError(t, err)
	last := first
	for {
		next, nextErr := src.Next(last)
		if errors.Is(nextErr, os.ErrNotExist) {
			break
		}
		require.NoError(t, nextErr)
		last = next
	}

	assert.Equal(t, uint(1), first)
	assert.GreaterOrEqual(t, last, uint(7))
}

// TestRAGFlowKnowledgeMigrationDeclaresIntegrityConstraints 验证 MySQL 基线 schema 中等价的完整性约束：
// - runtime token 唯一性：PG 部分唯一索引改为 STORED 生成列 + 普通唯一键，业务语义不变；
// - ragflow dataset/document 跨表 scope 一致性：复合唯一键 + 复合外键代替 PG 的声明方式。
func TestRAGFlowKnowledgeMigrationDeclaresIntegrityConstraints(t *testing.T) {
	upBytes, err := FS.ReadFile("000001_baseline.up.sql")
	require.NoError(t, err)
	up := string(upBytes)

	// runtime token hash 只允许未删除应用持有唯一的非空值，避免 Hermes token 解析到多个 app。
	// MySQL 不支持 WHERE 过滤的部分唯一索引，改用 VIRTUAL 生成列 runtime_token_active_key：
	// 有效（非 NULL）时值为 token hash，已删除时为 NULL，再对生成列建普通唯一键实现等价语义。
	assert.Contains(t, up, "runtime_token_active_key")
	assert.Contains(t, up, "uk_apps_runtime_token_hash_active")
	assert.Contains(t, up, "CONSTRAINT apps_runtime_token_pair_check CHECK")

	// document 冗余 scope 字段必须能通过复合外键回指到 dataset，防止跨组织或跨 app 写错映射。
	// ragflow_datasets 上建复合唯一键供 ragflow_documents 的复合外键引用：
	// 三列唯一键覆盖 org 范围，四列唯一键覆盖 app 范围；两条复合外键分别指向三列键与四列键，
	// 保证 org 范围与 app 范围下 document 的 scope/org/app 都与所属 dataset 强一致。
	assert.Contains(t, up, "uk_ragflow_datasets_scope_identity (id, scope_type, org_id)")
	assert.Contains(t, up, "uk_ragflow_datasets_app_identity (id, scope_type, org_id, app_id)")
	assert.Contains(t, up, "CONSTRAINT fk_ragflow_documents_dataset_scope FOREIGN KEY (dataset_id, scope_type, org_id)")
	assert.Contains(t, up, "CONSTRAINT fk_ragflow_documents_dataset_app_scope FOREIGN KEY (dataset_id, scope_type, org_id, app_id)")
}

// TestRAGFlowAutoReparseMigrationDeclaresRetryState 验证自动重解析迁移声明重试状态、索引、存量回填和回滚语句。
func TestRAGFlowAutoReparseMigrationDeclaresRetryState(t *testing.T) {
	upBytes, err := FS.ReadFile("000008_ragflow_auto_reparse.up.sql")
	require.NoError(t, err)
	up := string(upBytes)

	// 自动重解析需要持久化次数和下次可重试时间，避免服务重启后丢失冷却状态。
	assert.Contains(t, up, "auto_reparse_attempts INT NOT NULL DEFAULT 0")
	assert.Contains(t, up, "auto_reparse_next_at DATETIME(6) NULL")
	assert.Contains(t, up, "idx_ragflow_documents_auto_reparse")

	// 存量模型过载失败必须被回填为立即可重试，但迁移本身不直接调用 RAGFlow。
	// 回填必须用 UTC_TIMESTAMP(6) 而非 NOW(6)：app DB 连接固定 time_zone='+00:00'（loc=UTC），
	// 其读取查询的 NOW(6) 为 UTC；而迁移连接走服务器 SYSTEM 时区（移动云常为 +08:00），
	// 若用 NOW(6) 会写入北京墙钟裸值，比 app 的 UTC 基准早 8 小时，导致回填文档要等 8 小时才到期。
	assert.Contains(t, up, "SET auto_reparse_next_at = UTC_TIMESTAMP(6)")
	assert.Contains(t, up, "LOWER(last_error) LIKE '%model service overloaded%'")
	assert.Contains(t, up, "LOWER(last_error) LIKE '%error code: 503%'")
	assert.Contains(t, up, "LOWER(last_error) LIKE '%code: 50505%'")

	downBytes, err := FS.ReadFile("000008_ragflow_auto_reparse.down.sql")
	require.NoError(t, err)
	down := string(downBytes)

	// down 迁移必须先删索引再删列，保证本地回滚最近一次迁移可用。
	assert.Contains(t, down, "DROP INDEX idx_ragflow_documents_auto_reparse")
	assert.Contains(t, down, "DROP COLUMN auto_reparse_next_at")
	assert.Contains(t, down, "DROP COLUMN auto_reparse_attempts")
}

// TestDropRAGFlowAutoReparseMigration 验证 000009 删除自动重试两列与索引,且 down 能重建。
func TestDropRAGFlowAutoReparseMigration(t *testing.T) {
	up, err := FS.ReadFile("000009_drop_ragflow_auto_reparse.up.sql")
	require.NoError(t, err)
	upStr := string(up)
	assert.Contains(t, upStr, "DROP INDEX idx_ragflow_documents_auto_reparse")
	assert.Contains(t, upStr, "DROP COLUMN auto_reparse_next_at")
	assert.Contains(t, upStr, "DROP COLUMN auto_reparse_attempts")

	down, err := FS.ReadFile("000009_drop_ragflow_auto_reparse.down.sql")
	require.NoError(t, err)
	downStr := string(down)
	// down 必须重建列(便于本地回滚),与 000008 的定义保持一致。
	assert.Contains(t, downStr, "auto_reparse_attempts INT NOT NULL DEFAULT 0")
	assert.Contains(t, downStr, "auto_reparse_next_at DATETIME(6) NULL")
	assert.Contains(t, downStr, "idx_ragflow_documents_auto_reparse")
}

// TestIndustryKnowledgeMigrationDeclaresDatasetScopeIntegrity 验证行业知识库迁移对 document 与 dataset 的行业归属做复合外键约束。
func TestIndustryKnowledgeMigrationDeclaresDatasetScopeIntegrity(t *testing.T) {
	upBytes, err := FS.ReadFile("000007_industry_knowledge.up.sql")
	require.NoError(t, err)
	up := string(upBytes)

	// 行业 scope 的 document 不能只校验行业库存在，还必须校验 dataset_id 属于同一个行业库。
	// MySQL 对包含 NULL 的 org/app 复合外键不检查 industry 行，因此行业库需要独立的非 NULL 复合身份键。
	assert.Contains(t, up, "uk_ragflow_datasets_industry_identity (id, scope_type, industry_knowledge_base_id)")
	assert.Contains(t, up, "CONSTRAINT fk_ragflow_documents_dataset_industry_scope")
	assert.Contains(t, up, "FOREIGN KEY (dataset_id, scope_type, industry_knowledge_base_id)")
	assert.Contains(t, up, "REFERENCES ragflow_datasets(id, scope_type, industry_knowledge_base_id) ON DELETE CASCADE")
}

// TestAICCMigrationGuardrails 验证 AICC 迁移包含核心租户开关、公开 token 唯一性、
// 会话归属外键和保留期查询索引，避免后续匿名访客入口缺少必要安全边界。
func TestAICCMigrationGuardrails(t *testing.T) {
	upBytes, err := FS.ReadFile("000028_aicc.up.sql")
	require.NoError(t, err)
	up := string(upBytes)

	assert.Contains(t, up, "ADD COLUMN aicc_enabled BOOLEAN NOT NULL DEFAULT FALSE")
	assert.Contains(t, up, "ADD COLUMN aicc_agent_limit INT NULL")
	assert.Contains(t, up, "CONSTRAINT organizations_aicc_agent_limit_check CHECK (aicc_agent_limit IS NULL OR aicc_agent_limit >= 0)")
	assert.Contains(t, up, "UNIQUE KEY uk_apps_id_org (id, org_id)")
	assert.Contains(t, up, "CREATE TABLE aicc_agents")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_agents_public_token")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_agents_widget_token")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_agents_app_org FOREIGN KEY (app_id, org_id) REFERENCES apps(id, org_id)")
	assert.Contains(t, up, "scope_identity_key CHAR(36) GENERATED ALWAYS AS")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_agent_knowledge_scope (agent_id, scope_type, scope_identity_key)")
	assert.Contains(t, up, "CONSTRAINT aicc_agent_knowledge_scope_target_check CHECK")
	assert.Contains(t, up, "agent_org_id CHAR(36) NOT NULL")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_agent_knowledge_agent_scope FOREIGN KEY (agent_id, agent_org_id)")
	assert.Contains(t, up, "org_id = agent_org_id")
	assert.Contains(t, up, "org_id CHAR(36) NULL")
	assert.Contains(t, up, "app_id CHAR(36) NULL")
	assert.Contains(t, up, "industry_knowledge_base_id CHAR(36) NULL")
	assert.Contains(t, up, "ragflow_document_id CHAR(36) NULL")
	assert.Contains(t, up, "ragflow_document_scope_type VARCHAR(50) GENERATED ALWAYS AS")
	assert.Contains(t, up, "UNIQUE KEY uk_ragflow_documents_aicc_app_doc_identity (id, scope_type, org_id, app_id)")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_agent_knowledge_document_scope FOREIGN KEY")
	assert.Contains(t, up, "KEY idx_aicc_agent_knowledge_agent_scope (agent_id, agent_org_id)")
	assert.Contains(t, up, "KEY idx_aicc_agent_knowledge_industry_scope (industry_knowledge_base_id)")
	assert.Contains(t, up, "KEY idx_aicc_agent_knowledge_document_scope (ragflow_document_id, ragflow_document_scope_type, org_id, app_id)")
	assert.Contains(t, up, "CREATE TABLE aicc_sessions")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_sessions_token")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_sessions_agent_org FOREIGN KEY (agent_id, org_id) REFERENCES aicc_agents(id, org_id)")
	assert.Contains(t, up, "KEY idx_aicc_sessions_agent_org (agent_id, org_id)")
	assert.Contains(t, up, "KEY idx_aicc_sessions_org (org_id)")
	assert.Contains(t, up, "KEY idx_aicc_sessions_retention (expires_at, id)")
	assert.Contains(t, up, "CREATE TABLE aicc_messages")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_messages_session_agent FOREIGN KEY (session_id, agent_id) REFERENCES aicc_sessions(id, agent_id) ON DELETE CASCADE")
	assert.Contains(t, up, "KEY idx_aicc_messages_session_agent (session_id, agent_id)")
	assert.Contains(t, up, "CREATE TABLE aicc_leads")
	assert.Contains(t, up, "latest_session_org_id CHAR(36) NULL")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_leads_latest_session FOREIGN KEY (latest_session_id, latest_session_org_id)")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_leads_identity (id, org_id)")
	assert.Contains(t, up, "KEY idx_aicc_leads_latest_session (latest_session_id, latest_session_org_id)")
	assert.Contains(t, up, "CREATE TABLE aicc_feedback")
	assert.Contains(t, up, "org_id CHAR(36) NOT NULL")
	assert.Contains(t, up, "lead_org_id CHAR(36) NULL")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_lead_values_session_org FOREIGN KEY (session_id, org_id)")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_lead_values_session_agent FOREIGN KEY (session_id, agent_id)")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_lead_values_lead_org FOREIGN KEY (lead_id, lead_org_id)")
	assert.Contains(t, up, "CONSTRAINT aicc_lead_values_lead_org_check CHECK")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_lead_values_field_agent FOREIGN KEY (field_id, agent_id)")
	assert.Contains(t, up, "KEY idx_aicc_lead_values_session_org (session_id, org_id)")
	assert.Contains(t, up, "KEY idx_aicc_lead_values_session_agent (session_id, agent_id)")
	assert.Contains(t, up, "KEY idx_aicc_lead_values_lead_org (lead_id, lead_org_id)")
	assert.Contains(t, up, "KEY idx_aicc_lead_values_field_agent (field_id, agent_id)")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_feedback_message_session FOREIGN KEY (message_id, session_id)")
	assert.Contains(t, up, "KEY idx_aicc_feedback_message_session (message_id, session_id)")

	downBytes, err := FS.ReadFile("000028_aicc.down.sql")
	require.NoError(t, err)
	down := string(downBytes)
	// down 必须先删除依赖 ragflow_documents 复合索引的 AICC 表，再删除 parent 索引，避免 MySQL 因 FK 依赖拒绝回滚。
	assert.Greater(t, strings.Index(down, "ALTER TABLE ragflow_documents\n    DROP INDEX uk_ragflow_documents_aicc_app_doc_identity;"), strings.Index(down, "DROP TABLE IF EXISTS aicc_agent_knowledge;"))
}

// TestAICCMigrationExecutesOnMySQL 验证 AICC 迁移在真实 MySQL 8 上能建立约束、拒绝跨作用域脏数据并成功回滚。
func TestAICCMigrationExecutesOnMySQL(t *testing.T) {
	baseURL := os.Getenv("AICC_MIGRATION_TEST_DSN")
	if baseURL == "" {
		t.Skip("AICC_MIGRATION_TEST_DSN not set")
	}

	adminCfg := parseMigrationTestDSN(t, baseURL)
	adminDB, err := sql.Open("mysql", adminCfg.FormatDSN())
	require.NoError(t, err)
	defer adminDB.Close()
	require.NoError(t, adminDB.Ping())

	dbName := fmt.Sprintf("aicc_migration_test_%d", time.Now().UnixNano())
	mustExecMigrationSQL(t, adminDB, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName))
	mustExecMigrationSQL(t, adminDB, fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci", dbName))
	defer mustExecMigrationSQL(t, adminDB, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName))

	testCfg := *adminCfg
	testCfg.DBName = dbName
	testDB, err := sql.Open("mysql", testCfg.FormatDSN())
	require.NoError(t, err)
	defer testDB.Close()
	require.NoError(t, testDB.Ping())

	src, err := iofs.New(FS, ".")
	require.NoError(t, err)
	defer src.Close()

	migrator, err := migrate.NewWithSourceInstance("iofs", src, "mysql://"+testCfg.FormatDSN())
	require.NoError(t, err)
	defer func() {
		sourceErr, databaseErr := migrator.Close()
		require.NoError(t, sourceErr)
		require.NoError(t, databaseErr)
	}()

	err = migrator.Up()
	require.NoError(t, err)

	// 准备两个组织、两个 owner 与两个 app，构造跨组织/跨 app 文档作用域场景。
	mustExecMigrationSQL(t, testDB, "INSERT INTO organizations (id, name, code, status) VALUES (?, ?, ?, ?), (?, ?, ?, ?)",
		"org-a", "Org A", "orga", "active",
		"org-b", "Org B", "orgb", "active",
	)
	mustExecMigrationSQL(t, testDB, "INSERT INTO users (id, org_id, username, password_hash, display_name, role, status) VALUES (?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?)",
		"user-a", "org-a", "user-a", "hash", "User A", "org_admin", "active",
		"user-b", "org-b", "user-b", "hash", "User B", "org_admin", "active",
	)
	mustExecMigrationSQL(t, testDB, "INSERT INTO apps (id, org_id, owner_user_id, name, description, status, api_key_status, version_id, locale, knowledge_quota_bytes) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"app-a", "org-a", "user-a", "App A", nil, "draft", "pending", nil, nil, int64(1024),
		"app-b", "org-b", "user-b", "App B", nil, "draft", "pending", nil, nil, int64(1024),
	)
	mustExecMigrationSQL(t, testDB, "INSERT INTO aicc_agents (id, org_id, app_id, name, status, public_token, widget_token) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"agent-a", "org-a", "app-a", "Agent A", "draft", "public-a", "widget-a",
	)
	mustExecMigrationSQL(t, testDB, "INSERT INTO industry_knowledge_bases (id, name, created_by) VALUES (?, ?, ?)", "industry-1", "Industry 1", "system")
	mustExecMigrationSQL(t, testDB, "INSERT INTO ragflow_datasets (id, scope_type, org_id, app_id, ragflow_dataset_id, name, status) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"dataset-a", "app", "org-a", "app-a", "remote-dataset-a", "Dataset A", "active",
		"dataset-b", "app", "org-b", "app-b", "remote-dataset-b", "Dataset B", "active",
	)
	mustExecMigrationSQL(t, testDB, "INSERT INTO ragflow_documents (id, dataset_id, scope_type, org_id, app_id, ragflow_document_id, name, created_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?, ?)",
		"doc-a", "dataset-a", "app", "org-a", "app-a", "remote-doc-a", "Doc A", "system",
		"doc-b", "dataset-b", "app", "org-b", "app-b", "remote-doc-b", "Doc B", "system",
	)

	// 不存在的 agent 即使挂行业库 scope，也必须被 agent 所属 FK 拒绝。
	_, err = testDB.Exec("INSERT INTO aicc_agent_knowledge (id, agent_id, agent_org_id, scope_type, industry_knowledge_base_id) VALUES (?, ?, ?, ?, ?)",
		"knowledge-bad-agent", "missing-agent", "org-a", "industry", "industry-1",
	)
	require.Error(t, err)

	// 已存在 agent 的行业知识行应能成功写入，证明 ownership anchor 与 industry scope 可以共存。
	mustExecMigrationSQL(t, testDB, "INSERT INTO aicc_agent_knowledge (id, agent_id, agent_org_id, scope_type, industry_knowledge_base_id) VALUES (?, ?, ?, ?, ?)",
		"knowledge-industry-ok", "agent-a", "org-a", "industry", "industry-1",
	)

	// 同组织/同应用的文档可被 app_document scope 正常引用。
	mustExecMigrationSQL(t, testDB, `INSERT INTO aicc_agent_knowledge (
		id, agent_id, agent_org_id, scope_type, org_id, app_id, ragflow_document_id
	) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"knowledge-doc-ok", "agent-a", "org-a", "app_document", "org-a", "app-a", "doc-a",
	)

	// 目标 org/app 与 agent 保持一致时，若文档实际属于其他 org/app，必须由文档复合 FK 拒绝。
	_, err = testDB.Exec(`INSERT INTO aicc_agent_knowledge (
		id, agent_id, agent_org_id, scope_type, org_id, app_id, ragflow_document_id
	) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"knowledge-cross-doc", "agent-a", "org-a", "app_document", "org-a", "app-a", "doc-b",
	)
	require.Error(t, err)

	// 准备 lead_values 场景：会话、字段和两条不同组织的 lead。
	mustExecMigrationSQL(t, testDB, "INSERT INTO aicc_sessions (id, agent_id, org_id, session_token, expires_at) VALUES (?, ?, ?, ?, ?)",
		"session-a", "agent-a", "org-a", "session-token-a", time.Now().Add(24*time.Hour),
	)
	mustExecMigrationSQL(t, testDB, "INSERT INTO aicc_lead_fields (id, agent_id, field_key, label) VALUES (?, ?, ?, ?)",
		"field-a", "agent-a", "contact_phone", "联系电话",
	)
	mustExecMigrationSQL(t, testDB, "INSERT INTO aicc_leads (id, org_id, primary_contact_hash) VALUES (?, ?, ?), (?, ?, ?)",
		"lead-a", "org-a", "hash-a",
		"lead-b", "org-b", "hash-b",
	)

	// 跨组织 lead 绑定必须被复合外键拒绝。
	_, err = testDB.Exec(`INSERT INTO aicc_lead_values (
		id, session_id, agent_id, org_id, lead_id, lead_org_id, field_id, value_text
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"lead-value-cross-org", "session-a", "agent-a", "org-a", "lead-b", "org-b", "field-a", "13800138000",
	)
	require.Error(t, err)

	// 同组织 lead 绑定可成功写入。
	mustExecMigrationSQL(t, testDB, `INSERT INTO aicc_lead_values (
		id, session_id, agent_id, org_id, lead_id, lead_org_id, field_id, value_text
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"lead-value-ok", "session-a", "agent-a", "org-a", "lead-a", "org-a", "field-a", "13800138001",
	)

	// 删除 lead 后，复合外键应把 lead_id/lead_org_id 置空，而不是删除整条留资值。
	mustExecMigrationSQL(t, testDB, "DELETE FROM aicc_leads WHERE id = ? AND org_id = ?", "lead-a", "org-a")
	var leadID, leadOrgID sql.NullString
	require.NoError(t, testDB.QueryRow(
		"SELECT lead_id, lead_org_id FROM aicc_lead_values WHERE id = ?",
		"lead-value-ok",
	).Scan(&leadID, &leadOrgID))
	assert.False(t, leadID.Valid)
	assert.False(t, leadOrgID.Valid)

	// down 迁移必须能在真实 MySQL 上成功回滚 000028，避免 parent 索引因 FK 依赖删除失败。
	require.NoError(t, migrator.Steps(-1))
}

// parseMigrationTestDSN 把 mysql:// URL 规整为 go-sql-driver/mysql 可直接使用的 DSN。
func parseMigrationTestDSN(t *testing.T, databaseURL string) *mysql.Config {
	t.Helper()
	cfg, err := mysql.ParseDSN(strings.TrimPrefix(databaseURL, "mysql://"))
	require.NoError(t, err)
	return cfg
}

// mustExecMigrationSQL 在集成迁移测试里执行建数 SQL，失败即中止测试。
func mustExecMigrationSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	_, err := db.Exec(query, args...)
	require.NoError(t, err)
}
