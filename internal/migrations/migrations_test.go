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
	up29Bytes, err := FS.ReadFile("000029_aicc_lead_fields_soft_delete.up.sql")
	require.NoError(t, err)
	up29 := string(up29Bytes)

	assert.Contains(t, up, "ADD COLUMN aicc_enabled BOOLEAN NOT NULL DEFAULT FALSE")
	assert.Contains(t, up, "ADD COLUMN aicc_agent_limit INT NULL")
	assert.Contains(t, up, "CONSTRAINT organizations_aicc_agent_limit_check CHECK (aicc_agent_limit IS NULL OR aicc_agent_limit >= 0)")
	assert.Contains(t, up, "UNIQUE KEY uk_apps_id_org (id, org_id)")
	assert.Contains(t, up, "CASE WHEN deleted_at IS NULL AND aicc_hidden = FALSE THEN owner_user_id END")
	assert.Contains(t, up, "CREATE TABLE aicc_agents")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_agents_public_token")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_agents_widget_token")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_agents_app_org FOREIGN KEY (app_id, org_id) REFERENCES apps(id, org_id)")
	assert.Contains(t, up, "KEY idx_aicc_agents_app_org (app_id, org_id)")
	assert.Contains(t, up, "KEY idx_aicc_agents_org_deleted_created (org_id, deleted_at, created_at DESC, id DESC)")
	assert.Contains(t, up, "scope_identity_key CHAR(36) GENERATED ALWAYS AS")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_agent_knowledge_scope (agent_id, scope_type, scope_identity_key)")
	assert.Contains(t, up, "CONSTRAINT aicc_agent_knowledge_scope_target_check CHECK")
	assert.Contains(t, up, "agent_org_id CHAR(36) NOT NULL")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_agent_knowledge_agent_scope FOREIGN KEY (agent_id, agent_org_id)")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_agent_knowledge_app_org FOREIGN KEY (app_id, org_id)")
	assert.Contains(t, up, "REFERENCES apps(id, org_id)")
	assert.Contains(t, up, "org_id = agent_org_id")
	assert.Contains(t, up, "org_id CHAR(36) NULL")
	assert.Contains(t, up, "app_id CHAR(36) NULL")
	assert.Contains(t, up, "industry_knowledge_base_id CHAR(36) NULL")
	assert.Contains(t, up, "ragflow_document_id CHAR(36) NULL")
	assert.Contains(t, up, "ragflow_document_scope_type VARCHAR(50) GENERATED ALWAYS AS")
	assert.Contains(t, up, "CASE WHEN scope_type = 'app_document' THEN 'app' END\n    ) STORED")
	assert.Contains(t, up, "UNIQUE KEY uk_ragflow_documents_aicc_app_doc_identity (id, scope_type, org_id, app_id)")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_agent_knowledge_document_scope FOREIGN KEY")
	assert.Contains(t, up, "KEY idx_aicc_agent_knowledge_agent_scope (agent_id, agent_org_id)")
	assert.Contains(t, up, "KEY idx_aicc_agent_knowledge_app_org (app_id, org_id)")
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
	assert.Contains(t, up, "CREATE TABLE aicc_images")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_images_session_org FOREIGN KEY (session_id, org_id)")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_images_session_agent FOREIGN KEY (session_id, agent_id)")
	assert.Contains(t, up, "KEY idx_aicc_images_session (session_id, id)")
	assert.Contains(t, up, "CREATE TABLE aicc_leads")
	assert.Contains(t, up, "latest_session_org_id CHAR(36) GENERATED ALWAYS AS")
	assert.Contains(t, up, "CASE WHEN latest_session_id IS NULL THEN NULL ELSE org_id END")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_leads_latest_session FOREIGN KEY (latest_session_id, latest_session_org_id)")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_leads_identity (id, org_id)")
	assert.Contains(t, up, "KEY idx_aicc_leads_latest_session (latest_session_id, latest_session_org_id)")
	assert.Contains(t, up, "CREATE TABLE aicc_feedback")
	assert.Contains(t, up, "org_id CHAR(36) NOT NULL")
	assert.Contains(t, up, "lead_org_id CHAR(36) NULL")
	assert.Contains(t, up29, "ADD COLUMN deleted_at DATETIME NULL")
	assert.Contains(t, up29, "ADD KEY idx_aicc_lead_fields_agent_active (agent_id, deleted_at, sort_order, id)")
	assert.Contains(t, up29, "DROP FOREIGN KEY fk_aicc_lead_values_session_agent")
	assert.Contains(t, up29, "REFERENCES aicc_sessions(id, agent_id) ON DELETE CASCADE")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_lead_values_session_org FOREIGN KEY (session_id, org_id)")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_lead_values_session_agent FOREIGN KEY (session_id, agent_id)")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_lead_values_lead_org FOREIGN KEY (lead_id, lead_org_id)")
	// MySQL 不允许 CHECK 引用参与外键级联动作的列；跨租户约束由生成列 + 复合外键保证。
	assert.NotContains(t, up, "CONSTRAINT aicc_leads_latest_session_org_check CHECK")
	assert.NotContains(t, up, "CONSTRAINT aicc_lead_values_lead_org_check CHECK")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_lead_values_field_agent FOREIGN KEY (field_id, agent_id)")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_lead_values_session_field (session_id, field_id)")
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
	dropKnowledgeIndex := strings.Index(down, "DROP TABLE IF EXISTS aicc_agent_knowledge;")
	dropDocumentIndex := strings.Index(down, "ALTER TABLE ragflow_documents\n    DROP INDEX uk_ragflow_documents_aicc_app_doc_identity;")
	require.NotEqual(t, -1, dropKnowledgeIndex)
	require.NotEqual(t, -1, dropDocumentIndex)
	assert.Greater(t, dropDocumentIndex, dropKnowledgeIndex)
}

// TestAppsAppTypeMigrationGuardrails 验证应用类型迁移会保留 AICC 标记语义，
// 并将普通应用的 owner 唯一约束限定在未删除的 standard 应用。
func TestAppsAppTypeMigrationGuardrails(t *testing.T) {
	upBytes, err := FS.ReadFile("000033_apps_app_type.up.sql")
	require.NoError(t, err)
	up := string(upBytes)

	// 新列默认 standard，存量 aicc_hidden=true 必须明确回填为 aicc，不能丢失客服应用身份。
	assert.Contains(t, up, "ADD COLUMN app_type VARCHAR(32) NOT NULL DEFAULT 'standard'")
	assert.Contains(t, up, "UPDATE apps SET app_type = CASE WHEN aicc_hidden THEN 'aicc' ELSE 'standard' END")
	assert.Contains(t, up, "CONSTRAINT apps_app_type_check CHECK (app_type IN ('standard', 'aicc'))")
	// MySQL 生成列模拟部分唯一索引，仅未删除的普通应用占用 owner 唯一名额。
	assert.Contains(t, up, "CASE WHEN deleted_at IS NULL AND app_type = 'standard' THEN owner_user_id END")
	assert.Contains(t, up, "DROP COLUMN aicc_hidden")

	downBytes, err := FS.ReadFile("000033_apps_app_type.down.sql")
	require.NoError(t, err)
	down := string(downBytes)
	// 回滚必须恢复旧标记列，供退回旧代码版本后继续识别 AICC 应用。
	assert.Contains(t, down, "ADD COLUMN aicc_hidden BOOLEAN NOT NULL DEFAULT FALSE")
	// AICC 类型回滚时恢复隐藏标记，保证旧代码仍会将客服应用排除在普通列表外。
	assert.Contains(t, down, "WHEN app_type = 'aicc' THEN TRUE")
	// 普通类型回滚时明确写入 FALSE，恢复旧 owner 唯一约束的参与条件。
	assert.Contains(t, down, "WHEN app_type = 'standard' THEN FALSE")
	// 未知类型不能被静默降级为普通应用；NULL 写入 NOT NULL 列会中止回滚。
	assert.Contains(t, down, "ELSE NULL")
	// 回滚时恢复旧生成列表达式和唯一索引，保持旧版本的普通应用 owner 唯一语义。
	assert.Contains(t, down, "CASE WHEN deleted_at IS NULL AND aicc_hidden = FALSE THEN owner_user_id END")
	assert.Contains(t, down, "ADD UNIQUE KEY uk_apps_owner_active (owner_active_key)")
	// app_type 检查约束在回填后才移除，约束与 ELSE NULL 共同阻止未知类型被静默回滚。
	dropCheckIndex := strings.Index(down, "DROP CHECK apps_app_type_check")
	updateIndex := strings.Index(down, "UPDATE apps")
	require.NotEqual(t, -1, dropCheckIndex)
	require.NotEqual(t, -1, updateIndex)
	assert.Greater(t, dropCheckIndex, updateIndex)
}

// TestAICCMessageTasksMigrationDeclaresDurableDispatchState 验证客服消息任务迁移持久化调度状态、
// 幂等锚点与租约扫描索引，避免 Redis 信号丢失后无法从 MySQL 恢复投递。
func TestAICCMessageTasksMigrationDeclaresDurableDispatchState(t *testing.T) {
	upBytes, err := FS.ReadFile("000034_aicc_message_tasks.up.sql")
	require.NoError(t, err)
	up := string(upBytes)

	// 每条消息仅能创建一个任务，消息删除后任务也必须随会话历史一并清理。
	assert.Contains(t, up, "CREATE TABLE aicc_message_tasks")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_message_tasks_message (message_id)")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_message_tasks_message FOREIGN KEY (message_id) REFERENCES aicc_messages(id) ON DELETE CASCADE")

	// 状态枚举覆盖排队、执行、重试、完成和最终失败，阻止未知状态绕过调度机。
	assert.Contains(t, up, "CONSTRAINT aicc_message_tasks_status_check CHECK (status IN ('queued','processing','retry_wait','completed','failed'))")

	// dispatcher 按到期时间扫描，reaper 按过期租约扫描；索引末尾 id 保证稳定的批量顺序。
	assert.Contains(t, up, "KEY idx_aicc_message_tasks_ready (status, run_after, id)")
	assert.Contains(t, up, "KEY idx_aicc_message_tasks_lease (status, lease_expires_at, id)")
}

// TestAICCConversationIntelligenceMigration 验证无状态续聊、回复来源和匿名意向画像的
// 唯一事实均落在 manager 数据库，并由外键在会话或消息清理时同步删除。
func TestAICCConversationIntelligenceMigration(t *testing.T) {
	upBytes, err := FS.ReadFile("000037_aicc_conversation_intelligence.up.sql")
	require.NoError(t, err)
	up := string(upBytes)

	// 每个会话只保留一份可递进更新的摘要，避免运行时容器本地状态成为续聊依赖。
	assert.Contains(t, up, "CREATE TABLE aicc_session_contexts")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_session_contexts_session (session_id)")
	assert.Contains(t, up, "summarized_through_message_id CHAR(36) NULL")
	assert.Contains(t, up, "summary_version INT NOT NULL DEFAULT 1")
	assert.Contains(t, up, "CONSTRAINT aicc_session_contexts_version_check CHECK (summary_version >= 1)")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_session_contexts_session FOREIGN KEY (session_id) REFERENCES aicc_sessions(id) ON DELETE CASCADE")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_session_contexts_message FOREIGN KEY (summarized_through_message_id, session_id)\n        REFERENCES aicc_messages(id, session_id) ON DELETE CASCADE")

	// 每条助手消息可持久化多个知识或公开网络来源，企业公开网络结果必须另行标记未确认。
	assert.Contains(t, up, "CREATE TABLE aicc_message_sources")
	assert.Contains(t, up, "CONSTRAINT aicc_message_sources_type_check CHECK (source_type IN ('knowledge','web'))")
	assert.Contains(t, up, "unconfirmed BOOLEAN NOT NULL DEFAULT FALSE")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_message_sources_message FOREIGN KEY (message_id) REFERENCES aicc_messages(id) ON DELETE CASCADE")

	// 意向画像以 session 为唯一锚点，分析消息和会话删除时均不能留下孤儿候选。
	assert.Contains(t, up, "CREATE TABLE aicc_session_intents")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_session_intents_session (session_id)")
	assert.Contains(t, up, "CONSTRAINT aicc_session_intents_level_check CHECK (intent_level IN ('low','medium','high'))")
	assert.Contains(t, up, "CONSTRAINT aicc_session_intents_invite_check CHECK (invite_status IN ('not_invited','invited','declined','submitted'))")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_session_intents_session FOREIGN KEY (session_id) REFERENCES aicc_sessions(id) ON DELETE CASCADE")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_session_intents_message FOREIGN KEY (analyzed_message_id, session_id)\n        REFERENCES aicc_messages(id, session_id) ON DELETE CASCADE")

	// 复合子外键必须引用既有消息复合唯一键，才能在数据库层拒绝跨 session 的摘要水位与分析证据。
	messagesBytes, err := FS.ReadFile("000028_aicc.up.sql")
	require.NoError(t, err)
	assert.Contains(t, string(messagesBytes), "UNIQUE KEY uk_aicc_messages_session_identity (id, session_id)")

	downBytes, err := FS.ReadFile("000037_aicc_conversation_intelligence.down.sql")
	require.NoError(t, err)
	down := string(downBytes)
	// 回滚明确列出三张表，并按来源、意向、上下文的稳定顺序删除，避免遗漏新增事实表。
	dropSources := strings.Index(down, "DROP TABLE IF EXISTS aicc_message_sources;")
	dropIntents := strings.Index(down, "DROP TABLE IF EXISTS aicc_session_intents;")
	dropContexts := strings.Index(down, "DROP TABLE IF EXISTS aicc_session_contexts;")
	require.NotEqual(t, -1, dropSources)
	require.NotEqual(t, -1, dropIntents)
	require.NotEqual(t, -1, dropContexts)
	assert.Less(t, dropSources, dropIntents)
	assert.Less(t, dropIntents, dropContexts)
}

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
	assert.Contains(t, up, "blocked_visitor_enabled BOOLEAN NOT NULL DEFAULT TRUE")
	assert.Contains(t, up, "blocked_visitor_threshold_json JSON NULL")
	assert.Contains(t, up, "session_resume_ttl_minutes INT NOT NULL DEFAULT 30")
	assert.Contains(t, up, "analytics_config_json JSON NULL")
	assert.Contains(t, up, "CONSTRAINT aicc_agent_settings_message_limit_check CHECK (message_limit_per_session BETWEEN 1 AND 1000)")
	assert.Contains(t, up, "CONSTRAINT aicc_agent_settings_resume_ttl_check CHECK (session_resume_ttl_minutes BETWEEN 1 AND 1440)")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_agent_settings_agent FOREIGN KEY (agent_id) REFERENCES aicc_agents(id) ON DELETE CASCADE")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_agent_settings_agent (agent_id)")
	assert.Contains(t, up, "CREATE TABLE aicc_blocked_visitors")
	assert.Contains(t, up, "visitor_hash VARCHAR(128) NOT NULL")
	assert.Contains(t, up, "expires_at DATETIME NOT NULL")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_blocked_visitors_agent_org FOREIGN KEY (agent_id, org_id) REFERENCES aicc_agents(id, org_id) ON DELETE CASCADE")
	assert.Contains(t, up, "CONSTRAINT fk_aicc_blocked_visitors_org FOREIGN KEY (org_id) REFERENCES organizations(id)")
	assert.Contains(t, up, "UNIQUE KEY uk_aicc_blocked_visitors_agent_visitor (agent_id, visitor_hash)")
	assert.Contains(t, up, "KEY idx_aicc_blocked_visitors_lookup (agent_id, visitor_hash, expires_at)")
	assert.Contains(t, up, "KEY idx_aicc_blocked_visitors_agent_created (agent_id, created_at DESC, id DESC)")
	assert.NotContains(t, up, "remote_ip")

	downBytes, err := FS.ReadFile("000030_aicc_settings.down.sql")
	require.NoError(t, err)
	down := string(downBytes)

	// 000030 回滚必须先删除依赖 agent 的封禁表，再删除 agent settings 表。
	dropBlockedVisitorsIndex := strings.Index(down, "DROP TABLE IF EXISTS aicc_blocked_visitors;")
	dropAgentSettingsIndex := strings.Index(down, "DROP TABLE IF EXISTS aicc_agent_settings;")
	require.NotEqual(t, -1, dropBlockedVisitorsIndex)
	require.NotEqual(t, -1, dropAgentSettingsIndex)
	assert.Less(t, dropBlockedVisitorsIndex, dropAgentSettingsIndex)
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

	// 先迁移到 000030 准备既有 AICC 归属数据；任务表在末尾单独升级到 000034，便于覆盖其新增约束和 down。
	err = migrator.Migrate(30)
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
	mustExecMigrationSQL(t, testDB, "INSERT INTO aicc_agents (id, org_id, app_id, name, status, public_token, widget_token) VALUES (?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?)",
		"agent-a", "org-a", "app-a", "Agent A", "draft", "public-a", "widget-a",
		"agent-b", "org-b", "app-b", "Agent B", "draft", "public-b", "widget-b",
	)
	mustExecMigrationSQL(t, testDB, "INSERT INTO industry_knowledge_bases (id, name, created_by) VALUES (?, ?, ?)", "industry-1", "Industry 1", "system")
	mustExecMigrationSQL(t, testDB, "INSERT INTO ragflow_datasets (id, scope_type, org_id, app_id, ragflow_dataset_id, name, status) VALUES (?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?)",
		"dataset-a", "app", "org-a", "app-a", "remote-dataset-a", "Dataset A", "active",
		"dataset-b", "app", "org-b", "app-b", "remote-dataset-b", "Dataset B", "active",
	)
	forgedDatasetAccepted := true
	if _, err := testDB.Exec(
		"INSERT INTO ragflow_datasets (id, scope_type, org_id, app_id, ragflow_dataset_id, name, status) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"dataset-forged", "app", "org-a", "app-b", "remote-dataset-forged", "Dataset Forged", "active",
	); err != nil {
		forgedDatasetAccepted = false
		t.Logf("forged ragflow dataset already rejected by lower schema: %v", err)
	}
	mustExecMigrationSQL(t, testDB, "INSERT INTO ragflow_documents (id, dataset_id, scope_type, org_id, app_id, ragflow_document_id, name, created_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?, ?)",
		"doc-a", "dataset-a", "app", "org-a", "app-a", "remote-doc-a", "Doc A", "system",
		"doc-b", "dataset-b", "app", "org-b", "app-b", "remote-doc-b", "Doc B", "system",
	)
	forgedDocumentAccepted := false
	if forgedDatasetAccepted {
		if _, err := testDB.Exec(
			"INSERT INTO ragflow_documents (id, dataset_id, scope_type, org_id, app_id, ragflow_document_id, name, created_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			"doc-forged", "dataset-forged", "app", "org-a", "app-b", "remote-doc-forged", "Doc Forged", "system",
		); err != nil {
			t.Logf("forged ragflow document already rejected by lower schema: %v", err)
		} else {
			forgedDocumentAccepted = true
		}
	}

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

	// 即使下游 ragflow 表容纳了 forged 的 org/app 混搭文档，AICC 也必须用 app/org 复合 FK 拒绝该归属。
	if forgedDocumentAccepted {
		_, err = testDB.Exec(`INSERT INTO aicc_agent_knowledge (
			id, agent_id, agent_org_id, scope_type, org_id, app_id, ragflow_document_id
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			"knowledge-forged-app", "agent-a", "org-a", "app_document", "org-a", "app-b", "doc-forged",
		)
		require.Error(t, err)
	}

	// 准备 lead_values 场景：会话、字段和两条不同组织的 lead。
	mustExecMigrationSQL(t, testDB, "INSERT INTO aicc_sessions (id, agent_id, org_id, session_token, expires_at) VALUES (?, ?, ?, ?, ?)",
		"session-a", "agent-a", "org-a", "session-token-a", time.Now().Add(24*time.Hour),
	)
	mustExecMigrationSQL(t, testDB, "INSERT INTO aicc_lead_fields (id, agent_id, field_key, label) VALUES (?, ?, ?, ?)",
		"field-a", "agent-a", "contact_phone", "联系电话",
	)
	mustExecMigrationSQL(t, testDB, "INSERT INTO aicc_lead_fields (id, agent_id, field_key, label) VALUES (?, ?, ?, ?)",
		"field-b", "agent-b", "contact_phone", "联系电话",
	)
	mustExecMigrationSQL(t, testDB, "INSERT INTO aicc_leads (id, org_id, primary_contact_hash) VALUES (?, ?, ?), (?, ?, ?)",
		"lead-a", "org-a", "hash-a",
		"lead-b", "org-b", "hash-b",
	)

	// lead 指向 latest_session 时，数据库会阻止直接物理删除 session；保留任务必须先清空引用再删会话。
	mustExecMigrationSQL(t, testDB, "INSERT INTO aicc_sessions (id, agent_id, org_id, session_token, expires_at) VALUES (?, ?, ?, ?, ?)",
		"session-retention", "agent-a", "org-a", "session-token-retention", time.Now().Add(-24*time.Hour),
	)
	mustExecMigrationSQL(t, testDB, "INSERT INTO aicc_leads (id, org_id, primary_contact_hash, latest_session_id) VALUES (?, ?, ?, ?)",
		"lead-retention", "org-a", "hash-retention", "session-retention",
	)
	_, err = testDB.Exec("DELETE FROM aicc_sessions WHERE id = ? AND org_id = ?", "session-retention", "org-a")
	require.Error(t, err)
	mustExecMigrationSQL(t, testDB, "UPDATE aicc_leads SET latest_session_id = NULL WHERE id = ? AND org_id = ?", "lead-retention", "org-a")
	mustExecMigrationSQL(t, testDB, "DELETE FROM aicc_sessions WHERE id = ? AND org_id = ?", "session-retention", "org-a")

	// 不存在的 lead_id 与当前 org_id 组合必须由 lead 复合外键拒绝。
	_, err = testDB.Exec(`INSERT INTO aicc_lead_values (
		id, session_id, agent_id, org_id, lead_id, lead_org_id, field_id, value_text
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"lead-value-missing-lead-org", "session-a", "agent-a", "org-a", "missing-lead", "org-a", "field-a", "13800138002",
	)
	require.Error(t, err)

	// 即使 org_id 与 session 匹配、field_id 与 agent 匹配，也不能把 session-a 伪造成 agent-b 的留资值。
	_, err = testDB.Exec(`INSERT INTO aicc_lead_values (
		id, session_id, agent_id, org_id, lead_id, lead_org_id, field_id, value_text
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"lead-value-cross-agent", "session-a", "agent-b", "org-a", nil, nil, "field-b", "13800138003",
	)
	require.Error(t, err)

	// 同组织 lead 绑定可成功写入。
	mustExecMigrationSQL(t, testDB, `INSERT INTO aicc_lead_values (
		id, session_id, agent_id, org_id, lead_id, lead_org_id, field_id, value_text
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"lead-value-ok", "session-a", "agent-a", "org-a", "lead-a", "org-a", "field-a", "13800138001",
	)

	// 会话被物理清理时，已提交的留资字段值必须随 session 级联删除，避免保留期清理被 FK 阻塞。
	mustExecMigrationSQL(t, testDB, "INSERT INTO aicc_sessions (id, agent_id, org_id, session_token, expires_at) VALUES (?, ?, ?, ?, ?)",
		"session-lead-cascade", "agent-a", "org-a", "session-token-lead-cascade", time.Now().Add(-24*time.Hour),
	)
	mustExecMigrationSQL(t, testDB, `INSERT INTO aicc_lead_values (
		id, session_id, agent_id, org_id, field_id, value_text
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"lead-value-cascade", "session-lead-cascade", "agent-a", "org-a", "field-a", "13800138009",
	)
	mustExecMigrationSQL(t, testDB, "DELETE FROM aicc_sessions WHERE id = ? AND org_id = ?", "session-lead-cascade", "org-a")
	var leadValueCount int
	require.NoError(t, testDB.QueryRow("SELECT COUNT(*) FROM aicc_lead_values WHERE id = ?", "lead-value-cascade").Scan(&leadValueCount))
	assert.Equal(t, 0, leadValueCount)

	// 已有历史留资值引用字段后，管理端保存字段配置只能软停用/恢复字段，不能物理删除字段行。
	mustExecMigrationSQL(t, testDB, "UPDATE aicc_lead_fields SET deleted_at = NOW() WHERE agent_id = ? AND deleted_at IS NULL", "agent-a")
	mustExecMigrationSQL(t, testDB, `INSERT INTO aicc_lead_fields (
		id, agent_id, field_key, label, field_type, required, sort_order
	) VALUES (?, ?, ?, ?, ?, ?, ?)
	ON DUPLICATE KEY UPDATE
		label = VALUES(label),
		field_type = VALUES(field_type),
		required = VALUES(required),
		sort_order = VALUES(sort_order),
		deleted_at = NULL`,
		"field-restored", "agent-a", "contact_phone", "联系电话", "phone", true, 1,
	)
	var restoredDeletedAt sql.NullTime
	require.NoError(t, testDB.QueryRow("SELECT deleted_at FROM aicc_lead_fields WHERE agent_id = ? AND field_key = ?", "agent-a", "contact_phone").Scan(&restoredDeletedAt))
	assert.False(t, restoredDeletedAt.Valid)

	// 已被留资值引用的 lead 不允许被物理删除；需先清空引用，避免历史留资值跨租户失去归属锚点。
	_, err = testDB.Exec("DELETE FROM aicc_leads WHERE id = ? AND org_id = ?", "lead-a", "org-a")
	require.Error(t, err)
	mustExecMigrationSQL(t, testDB, "UPDATE aicc_lead_values SET lead_id = NULL, lead_org_id = NULL WHERE id = ?", "lead-value-ok")
	var leadID, leadOrgID sql.NullString
	require.NoError(t, testDB.QueryRow(
		"SELECT lead_id, lead_org_id FROM aicc_lead_values WHERE id = ?",
		"lead-value-ok",
	).Scan(&leadID, &leadOrgID))
	assert.False(t, leadID.Valid)
	assert.False(t, leadOrgID.Valid)
	mustExecMigrationSQL(t, testDB, "DELETE FROM aicc_leads WHERE id = ? AND org_id = ?", "lead-a", "org-a")

	// 升级任务表前会连续应用 000031 至 000034，真实 MySQL 必须接受生成列、复合外键与微秒时间字段。
	require.NoError(t, migrator.Migrate(34))
	mustExecMigrationSQL(t, testDB, `INSERT INTO aicc_messages (
		id, session_id, agent_id, direction, content_type, text_content
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"message-task-a", "session-a", "agent-a", "visitor", "text", "任务消息 A",
	)

	// 正常消息可创建任务；message_id 唯一键阻止同一消息被 Redis 重复通知时重复入队。
	mustExecMigrationSQL(t, testDB, `INSERT INTO aicc_message_tasks (
		id, message_id, session_id, agent_id, org_id, app_id
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"task-a", "message-task-a", "session-a", "agent-a", "org-a", "app-a",
	)
	_, err = testDB.Exec(`INSERT INTO aicc_message_tasks (
		id, message_id, session_id, agent_id, org_id, app_id
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"task-a-duplicate", "message-task-a", "session-a", "agent-a", "org-a", "app-a",
	)
	require.Error(t, err)

	// 同一会话最多存在一个 processing 任务，数据库唯一键为并发 dispatcher 提供最终串行化保障。
	mustExecMigrationSQL(t, testDB, `INSERT INTO aicc_messages (
		id, session_id, agent_id, direction, content_type, text_content
	) VALUES (?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?)`,
		"message-task-processing-a", "session-a", "agent-a", "visitor", "text", "任务消息 processing A",
		"message-task-processing-b", "session-a", "agent-a", "visitor", "text", "任务消息 processing B",
	)
	mustExecMigrationSQL(t, testDB, `INSERT INTO aicc_message_tasks (
		id, message_id, session_id, agent_id, org_id, app_id, status
	) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"task-processing-a", "message-task-processing-a", "session-a", "agent-a", "org-a", "app-a", "processing",
	)
	_, err = testDB.Exec(`INSERT INTO aicc_message_tasks (
		id, message_id, session_id, agent_id, org_id, app_id, status
	) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"task-processing-b", "message-task-processing-b", "session-a", "agent-a", "org-a", "app-a", "processing",
	)
	require.Error(t, err)

	// 升级到对话事实表后，复合消息外键必须将摘要水位和分析证据限制在同一会话。
	require.NoError(t, migrator.Migrate(37))
	mustExecMigrationSQL(t, testDB, "INSERT INTO aicc_sessions (id, agent_id, org_id, session_token, expires_at) VALUES (?, ?, ?, ?, ?), (?, ?, ?, ?, ?)",
		"session-b", "agent-b", "org-b", "session-token-b", time.Now().Add(24*time.Hour),
		"session-cross", "agent-a", "org-a", "session-token-cross", time.Now().Add(24*time.Hour),
	)
	mustExecMigrationSQL(t, testDB, `INSERT INTO aicc_messages (
		id, session_id, agent_id, direction, content_type, text_content
	) VALUES (?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?)`,
		"message-context-a", "session-a", "agent-a", "visitor", "text", "会话 A 证据",
		"message-context-b", "session-b", "agent-b", "visitor", "text", "会话 B 证据",
	)

	// 正常路径：摘要和意向引用本会话的消息可以写入。
	mustExecMigrationSQL(t, testDB, `INSERT INTO aicc_session_contexts (
		id, session_id, summary, summarized_through_message_id, summary_version
	) VALUES (?, ?, ?, ?, ?)`,
		"context-a", "session-a", "会话 A 摘要", "message-context-a", 1,
	)
	mustExecMigrationSQL(t, testDB, `INSERT INTO aicc_session_intents (
		id, session_id, intent_level, analyzer_version, analyzed_message_id, invite_status
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"intent-a", "session-a", "high", "test-v1", "message-context-a", "not_invited",
	)

	// 边界路径：即使消息真实存在，也不得把另一 session 的消息作为本会话摘要或分析证据。
	_, err = testDB.Exec(`INSERT INTO aicc_session_contexts (
		id, session_id, summary, summarized_through_message_id, summary_version
	) VALUES (?, ?, ?, ?, ?)`,
		"context-cross-session", "session-cross", "错误摘要", "message-context-b", 1,
	)
	require.Error(t, err)
	_, err = testDB.Exec(`INSERT INTO aicc_session_intents (
		id, session_id, intent_level, analyzer_version, analyzed_message_id, invite_status
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"intent-cross-session", "session-cross", "medium", "test-v1", "message-context-b", "not_invited",
	)
	require.Error(t, err)

	// 先回滚 000037，三张对话事实表必须随迁移一起移除。
	require.NoError(t, migrator.Steps(-1))
	var contextTable string
	err = testDB.QueryRow("SHOW TABLES LIKE 'aicc_session_contexts'").Scan(&contextTable)
	require.ErrorIs(t, err, sql.ErrNoRows)

	// 000036 与 000035 只引入队列锁和消息关联列，分别回滚后才回到任务表版本。
	require.NoError(t, migrator.Steps(-1))
	require.NoError(t, migrator.Steps(-1))
	// 单步回滚 000034 后任务表必须消失，证明回滚不残留依赖表。
	require.NoError(t, migrator.Steps(-1))
	var taskTable string
	err = testDB.QueryRow("SHOW TABLES LIKE 'aicc_message_tasks'").Scan(&taskTable)
	require.ErrorIs(t, err, sql.ErrNoRows)

	// 再从 000033 连续回滚到 000027，保留既有 settings 与 AICC parent 表的回滚依赖覆盖。
	require.NoError(t, migrator.Steps(-6))
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
