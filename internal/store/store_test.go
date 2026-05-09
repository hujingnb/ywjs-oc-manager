package store

import (
	"context"
	"strings"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestOpenRejectsInvalidDatabaseURL(t *testing.T) {
	_, err := Open(context.Background(), "://bad-url")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "解析数据库连接配置失败"))
}

func TestCloseNilStoreIsSafe(t *testing.T) {
	var s *Store
	s.Close()
}
