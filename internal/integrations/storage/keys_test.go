package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestKeyConventions 验证各类 S3 key/prefix 拼接符合 spec-B §4 约定。
func TestKeyConventions(t *testing.T) {
	// app 根前缀必须以 "/" 结尾，供 STS prefix 通配与 MovePrefix 使用
	assert.Equal(t, "apps/a1/", AppPrefix("a1"))
	// 归档前缀位于 app 前缀下的 archive/ 子目录
	assert.Equal(t, "apps/a1/archive/", AppArchivePrefix("a1"))
	// sqlite 快照 key 固定为 state.db
	assert.Equal(t, "apps/a1/state.db", StateDBKey("a1"))
	// workspace / sessions 为归档逻辑根（无扩展名）
	assert.Equal(t, "apps/a1/workspace", WorkspaceKey("a1"))
	assert.Equal(t, "apps/a1/sessions", SessionsKey("a1"))
	// skill 走 version 维度，带 .tar 扩展，布局与 FSSkillBlobStore 一致
	assert.Equal(t, "versions/v1/skills/weather.tar", SkillKey("v1", "weather"))
}
