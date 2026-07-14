package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsAICCAppType 验证仅 aicc 枚举会进入客服专属语义，普通和未知类型均不能误判。
func TestIsAICCAppType(t *testing.T) {
	testCases := []struct {
		name    string
		appType AppType
		want    bool
	}{
		// 普通应用必须继续走标准实例路径。
		{name: "普通应用", appType: AppTypeStandard, want: false},
		// AICC 应用必须走客服专属权限、提示词和运行时路径。
		{name: "客服应用", appType: AppTypeAICC, want: true},
		// 未知类型不能被当作客服应用，避免绕过普通资源隔离。
		{name: "未知类型", appType: AppType("unknown"), want: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// 每种枚举输入都锁定对应客服语义，防止未来扩展类型误入 AICC 分支。
			assert.Equal(t, testCase.want, IsAICCAppType(testCase.appType))
		})
	}
}
