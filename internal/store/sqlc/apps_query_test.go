package sqlc

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAppQueriesExcludeAICCHiddenFromNormalInstanceScope 校验普通实例查询不会把 AICC 隐藏 app
// 计入成员活跃实例或企业实例配额，避免 AICC 后台 app 阻断普通实例创建。
func TestAppQueriesExcludeAICCHiddenFromNormalInstanceScope(t *testing.T) {
	// GetActiveAppByOwner 用于判断成员是否已有普通实例，应排除 AICC 隐藏 app。
	assert.Contains(t, normalizedSQL(getActiveAppByOwner), "aicc_hidden = false")
	// CountActiveAppsByOrg 用于 max_instance_count，AICC 已有独立上限，不应占用普通实例名额。
	assert.Contains(t, normalizedSQL(countActiveAppsByOrg), "aicc_hidden = false")
}

func normalizedSQL(query string) string {
	return strings.Join(strings.Fields(strings.ToLower(query)), " ")
}
