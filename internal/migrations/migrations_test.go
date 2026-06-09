// Package migrations 的测试只校验 embed.FS 内容，不连接真实数据库。
package migrations

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

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
