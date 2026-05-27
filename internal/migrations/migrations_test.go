// Package migrations 的测试只校验 embed.FS 内容，不连接真实数据库。
package migrations

import (
	"io/fs"
	"strings"
	"testing"

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

// TestRAGFlowKnowledgeMigrationDeclaresIntegrityConstraints 验证 RAGFlow 知识库迁移声明跨表一致性与 runtime token 唯一性约束。
func TestRAGFlowKnowledgeMigrationDeclaresIntegrityConstraints(t *testing.T) {
	upBytes, err := FS.ReadFile("000029_ragflow_knowledge.up.sql")
	require.NoError(t, err)
	up := string(upBytes)

	// runtime token hash 只允许未删除应用持有唯一的非空值，避免 Hermes token 解析到多个 app。
	assert.Contains(t, up, "CREATE UNIQUE INDEX apps_runtime_token_hash_active_unique")
	assert.Contains(t, up, "ON apps(runtime_token_hash)")
	assert.Contains(t, up, "WHERE runtime_token_hash IS NOT NULL AND deleted_at IS NULL")
	assert.Contains(t, up, "CONSTRAINT apps_runtime_token_pair_check CHECK")
	assert.Contains(t, up, "create_claim_token text NULL")

	// document 冗余 scope 字段必须能通过复合外键回指到 dataset，防止跨组织或跨 app 写错映射。
	assert.Contains(t, up, "CONSTRAINT ragflow_datasets_scope_identity_unique UNIQUE (id, scope_type, org_id)")
	assert.Contains(t, up, "CONSTRAINT ragflow_datasets_app_identity_unique UNIQUE (id, scope_type, org_id, app_id)")
	assert.Contains(t, up, "CONSTRAINT ragflow_documents_dataset_scope_fk FOREIGN KEY (dataset_id, scope_type, org_id)")
	assert.Contains(t, up, "CONSTRAINT ragflow_documents_dataset_app_scope_fk FOREIGN KEY (dataset_id, scope_type, org_id, app_id)")
}

// TestRAGFlowKnowledgeDownMigrationDropsRuntimeTokenIndexFirst 验证回滚时先移除依赖列的索引，再删除 runtime token 字段。
func TestRAGFlowKnowledgeDownMigrationDropsRuntimeTokenIndexFirst(t *testing.T) {
	downBytes, err := FS.ReadFile("000029_ragflow_knowledge.down.sql")
	require.NoError(t, err)
	down := string(downBytes)

	// 显式删除索引让回滚顺序清晰，即使 PostgreSQL 删除列时会级联清理依赖索引。
	indexDrop := strings.Index(down, "DROP INDEX IF EXISTS apps_runtime_token_hash_active_unique")
	hashColumnDrop := strings.Index(down, "ALTER TABLE apps DROP COLUMN IF EXISTS runtime_token_hash")
	require.NotEqual(t, -1, indexDrop)
	require.NotEqual(t, -1, hashColumnDrop)
	assert.Less(t, indexDrop, hashColumnDrop)
}
