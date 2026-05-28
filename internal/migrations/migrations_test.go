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
