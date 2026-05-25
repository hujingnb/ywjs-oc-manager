// Package main 的 server 测试覆盖启动前 fail-fast 路径，避免进入外部依赖连接。
package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/config"
	"oc-manager/internal/files"
)

// TestRunManager_RejectsBadMasterKey 校验 fail-fast：master_key 非合法 base64 时立刻报错，
// 不进入数据库连接阶段。
func TestRunManager_RejectsBadMasterKey(t *testing.T) {
	cfg := config.Config{}
	cfg.Security.MasterKey = "!!!not-base64!!!"
	err := runManager(context.Background(), cfg, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("err = %v, want base64 错误", err)
	}
}

// TestRunManager_RejectsShortMasterKey 校验 master_key 解码后不足 32 字节时 fail-fast。
func TestRunManager_RejectsShortMasterKey(t *testing.T) {
	cfg := config.Config{}
	cfg.Security.MasterKey = "AAAA" // base64 解出仅 3 字节
	err := runManager(context.Background(), cfg, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "cipher") {
		t.Fatalf("err = %v, want cipher 错误", err)
	}
}

// TestNewKnowledgeSafeRootUsesBusinessLimit 验证 server 装配知识库主副本时使用 100MB 业务上限。
func TestNewKnowledgeSafeRootUsesBusinessLimit(t *testing.T) {
	root, err := newKnowledgeSafeRoot(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, files.KnowledgeMaxFileSize, root.MaxFileSize)
}
