package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenRejectsInvalidDatabaseURL 覆盖启动阶段数据库 DSN 解析失败的错误包装。
func TestOpenRejectsInvalidDatabaseURL(t *testing.T) {
	_, err := Open(context.Background(), "://bad-url")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "解析数据库连接配置失败"))
}

// TestCloseNilStoreIsSafe 确认关闭逻辑允许延迟清理空 Store，便于启动失败路径统一 defer。
func TestCloseNilStoreIsSafe(t *testing.T) {
	var s *Store
	s.Close()
}

// TestSetAppRuntimeTokenQueryUsesFirstCreationCAS 验证 runtime token 写入只允许首次创建，避免并发初始化覆盖已渲染 manifest 的 token。
func TestSetAppRuntimeTokenQueryUsesFirstCreationCAS(t *testing.T) {
	sqlBytes, err := os.ReadFile("queries/apps.sql")
	require.NoError(t, err)
	sqlText := string(sqlBytes)

	start := strings.Index(sqlText, "-- name: SetAppRuntimeToken :exec")
	require.NotEqual(t, -1, start)
	end := strings.Index(sqlText[start:], "-- name: GetAppByRuntimeTokenHash :one")
	require.NotEqual(t, -1, end)
	query := sqlText[start : start+end]

	// 条件 UPDATE 仅命中 runtime_token 仍为空的行；首次写入后并发任务命中 0 行（:exec 不报错），
	// 由 service 读回既有 token，从而保证首次创建的 token 不被覆盖。
	assert.Contains(t, query, "runtime_token_hash IS NULL")
	assert.Contains(t, query, "runtime_token_ciphertext IS NULL")
}

// TestCreateRAGFlowDatasetMappingQueriesUseInsertOnlyClaims 验证懒创建 dataset 映射时只让插入成功者拥有远端创建权。
func TestCreateRAGFlowDatasetMappingQueriesUseInsertOnlyClaims(t *testing.T) {
	sqlBytes, err := os.ReadFile("queries/ragflow_knowledge.sql")
	require.NoError(t, err)
	sqlText := string(sqlBytes)

	assert.NotContains(t, sqlText, "-- name: CreateRAGFlowDatasetMapping :one")
	assert.Contains(t, sqlText, "-- name: CreateRAGFlowOrgDatasetMapping :exec")
	assert.Contains(t, sqlText, "-- name: CreateRAGFlowAppDatasetMapping :exec")
	// MySQL 无 ON CONFLICT；用 INSERT IGNORE 命中唯一索引（uk_ragflow_datasets_org/app_unique）时静默跳过，
	// 等价于「仅插入成功者获得远端创建权」的 DO NOTHING 语义。
	assert.Contains(t, sqlText, "INSERT IGNORE INTO ragflow_datasets")
	assert.Contains(t, sqlText, "create_claim_token")
	// 不得退化为 upsert 覆盖既有行——那会让并发败者覆盖获胜者的 claim token / 状态。
	assert.NotContains(t, sqlText, "ON DUPLICATE KEY UPDATE")
}
