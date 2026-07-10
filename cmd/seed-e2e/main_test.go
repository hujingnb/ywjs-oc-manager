// Package main 的 e2e 种子测试只校验危险命令守门，不连接数据库。
package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 验证 OCM_E2E 守门：缺这个环境变量时命令必须非零退出，避免误在生产 truncate。
func TestSeedE2E_RejectsMissingOCME2EFlag(t *testing.T) {
	t.Setenv("OCM_E2E", "")

	err := requireE2EGuard()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "OCM_E2E=1")
}
