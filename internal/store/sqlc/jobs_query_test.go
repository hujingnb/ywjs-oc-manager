package sqlc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGetLatestAppInitJobUnquotesJSONAppID 验证初始化任务按 app_id 查询时，
// 会把调用方传入的 JSON 字符串字面量还原为文本，避免历史任务存在却被误判为无任务。
func TestGetLatestAppInitJobUnquotesJSONAppID(t *testing.T) {
	query := normalizedSQL(getLatestAppInitJob)
	assert.Contains(t, query, "payload_json->>'$.app_id' = json_unquote(?)")
}
