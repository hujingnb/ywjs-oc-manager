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

	start := strings.Index(sqlText, "-- name: SetAppRuntimeToken :one")
	require.NotEqual(t, -1, start)
	end := strings.Index(sqlText[start:], "-- name: GetAppByRuntimeTokenHash :one")
	require.NotEqual(t, -1, end)
	query := sqlText[start : start+end]

	// 首次写入成功后，后续并发任务必须拿不到 RETURNING 行，并交给 service 重新读取既有 token。
	assert.Contains(t, query, "runtime_token_hash IS NULL")
	assert.Contains(t, query, "runtime_token_ciphertext IS NULL")
}

// TestCreateRAGFlowDatasetMappingQueriesUseInsertOnlyClaims 验证懒创建 dataset 映射时只让插入成功者拥有远端创建权。
func TestCreateRAGFlowDatasetMappingQueriesUseInsertOnlyClaims(t *testing.T) {
	sqlBytes, err := os.ReadFile("queries/ragflow_knowledge.sql")
	require.NoError(t, err)
	sqlText := string(sqlBytes)

	assert.NotContains(t, sqlText, "-- name: CreateRAGFlowDatasetMapping :one")
	assert.Contains(t, sqlText, "-- name: CreateRAGFlowOrgDatasetMapping :one")
	assert.Contains(t, sqlText, "ON CONFLICT (org_id) WHERE scope_type = 'org'")
	assert.Contains(t, sqlText, "-- name: CreateRAGFlowAppDatasetMapping :one")
	assert.Contains(t, sqlText, "ON CONFLICT (app_id) WHERE scope_type = 'app'")
	assert.Contains(t, sqlText, "DO NOTHING")
	assert.Contains(t, sqlText, "create_claim_token")
	assert.NotContains(t, sqlText, "SET updated_at = ragflow_datasets.updated_at")
}
