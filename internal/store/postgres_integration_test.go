//go:build integration

package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestStoreOpen_PostgreSQLLiveConnection 通过 INTEGRATION_DATABASE_URL 真实连一次数据库。
// 主要校验连接池能拿到连接并 Ping 成功；表结构由 migration 工具单独管理。
func TestStoreOpen_PostgreSQLLiveConnection(t *testing.T) {
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
	err := store.Pool().Ping(ctx)
	require.NoError(t, err)
}
