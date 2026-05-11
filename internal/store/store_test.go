package store

import (
	"context"
	"strings"
	"testing"

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
