package sqlc

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAppQueriesScopeByAppType 校验普通实例查询只查询 standard 类型，
// 避免 AICC 应用计入成员活跃实例或企业实例配额。
func TestAppQueriesScopeByAppType(t *testing.T) {
	// CreateApp 必须显式写入 app_type，使调用方能够创建 standard 或 aicc 应用。
	assert.Contains(t, normalizedSQL(createApp), "knowledge_quota_bytes, app_type")
	// GetActiveAppByOwner 用于判断成员是否已有普通实例，应仅匹配 standard 应用。
	assert.Contains(t, normalizedSQL(getActiveAppByOwner), "app_type = 'standard'")
	// ListAppsByOrg 是普通实例基础列表，应只返回 standard 应用。
	assert.Contains(t, normalizedSQL(listAppsByOrg), "app_type = 'standard'")
	// CountActiveAppsByOrg 用于 max_instance_count，AICC 使用独立上限，不应占用普通实例名额。
	assert.Contains(t, normalizedSQL(countActiveAppsByOrg), "app_type = 'standard'")
	// ListAppsByOrgWithVersion 是普通实例列表实际使用查询，应仅返回 standard 应用。
	assert.Contains(t, normalizedSQL(listAppsByOrgWithVersion), "app_type = 'standard'")
	// AICC 升级扫描只处理 aicc 类型应用，避免把普通应用切换为客服运行时镜像。
	assert.Contains(t, normalizedSQL(listStaleAICCRuntimeApps), "app_type = 'aicc'")
	// AICC 类型补标记必须显式写入 aicc，供后续所有查询按应用类型分流。
	assert.Contains(t, normalizedSQL(markAppAICCType), "set app_type = 'aicc'")
}

func normalizedSQL(query string) string {
	return strings.Join(strings.Fields(strings.ToLower(query)), " ")
}
