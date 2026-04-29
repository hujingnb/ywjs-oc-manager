package store

import (
	"context"
	"strings"
	"testing"
)

func TestOpenRejectsInvalidDatabaseURL(t *testing.T) {
	_, err := Open(context.Background(), "://bad-url")
	if err == nil {
		t.Fatal("期望非法数据库连接字符串返回错误")
	}
	if !strings.Contains(err.Error(), "解析数据库连接配置失败") {
		t.Fatalf("错误信息应包含中文上下文，实际为: %v", err)
	}
}

func TestCloseNilStoreIsSafe(t *testing.T) {
	var s *Store
	s.Close()
}
