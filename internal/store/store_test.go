package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeDSNForcesUTCTimeZone 验证 normalizeDSN 把时区相关参数强制规整为 UTC：
// 这是「时区错位」类 bug 的源头根治——服务器 time_zone 为 +08:00 而 DSN loc=UTC 时，
// now() 写入的裸 datetime 会被当成 UTC 读回（凭空 +8h），导致 Go 时间与 now() 列比较错位。
func TestNormalizeDSNForcesUTCTimeZone(t *testing.T) {
	// 起始 DSN 故意不带 parseTime / loc / time_zone，模拟仅填了最小连接串的部署配置。
	out, err := normalizeDSN("oc_manager:pw@tcp(127.0.0.1:3306)/manager")
	require.NoError(t, err)

	// 用驱动自身解析回结构体逐项断言，避免依赖参数在 DSN 字符串里的拼接顺序。
	cfg, err := mysql.ParseDSN(out)
	require.NoError(t, err)
	// parseTime=true：时间列扫描为 time.Time 而非 []byte。
	assert.True(t, cfg.ParseTime, "应强制 parseTime=true")
	// loc=UTC：Go 侧按 UTC 解释/发送时间。
	assert.Equal(t, "UTC", cfg.Loc.String(), "应强制 loc=UTC")
	// 会话变量 time_zone='+00:00'：让 now()/CURRENT_TIMESTAMP 返回 UTC，与 loc=UTC 对齐。
	assert.Equal(t, "'+00:00'", cfg.Params["time_zone"], "应强制会话 time_zone='+00:00'")
}

// TestNormalizeDSNOverridesConflictingTimeZone 验证即使配置里写了相反的时区，也会被强制覆盖为 UTC：
// 根治不依赖每份部署配置手填正确——只要走 Open，时区一定落到 UTC。
func TestNormalizeDSNOverridesConflictingTimeZone(t *testing.T) {
	// 配置里故意写 loc=Local 与 time_zone='+08:00'，应被 normalizeDSN 覆盖。
	out, err := normalizeDSN("oc_manager:pw@tcp(127.0.0.1:3306)/manager?parseTime=true&loc=Local&time_zone=%27%2B08%3A00%27")
	require.NoError(t, err)
	cfg, err := mysql.ParseDSN(out)
	require.NoError(t, err)
	assert.Equal(t, "UTC", cfg.Loc.String(), "冲突的 loc=Local 应被覆盖为 UTC")
	assert.Equal(t, "'+00:00'", cfg.Params["time_zone"], "冲突的 time_zone=+08:00 应被覆盖为 +00:00")
}

// TestNormalizeDSNRejectsInvalid 非法 DSN 必须返回错误，让启动期 fail-fast。
func TestNormalizeDSNRejectsInvalid(t *testing.T) {
	_, err := normalizeDSN("not-a-valid-dsn-without-at-sign")
	require.Error(t, err)
}

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
