//go:build integration

package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestStoreOpen_MySQLLiveConnection 通过 INTEGRATION_DATABASE_URL 真实连一次 MySQL。
// 主要校验连接池能拿到连接并 Ping 成功；表结构由 migration 工具单独管理。
func TestStoreOpen_MySQLLiveConnection(t *testing.T) {
	dsn := os.Getenv("INTEGRATION_DATABASE_URL")
	if dsn == "" {
		t.Skip("缺 INTEGRATION_DATABASE_URL")
	}
	// 使用短超时避免数据库不可达时阻塞整套集成测试。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, err := Open(ctx, dsn)
	require.NoError(t, err)
	defer store.Close()
	// sql.Open 是惰性的，用 Ping 强制建立真实连接以确认 MySQL 可达。
	require.NoError(t, store.Ping(ctx))
}
