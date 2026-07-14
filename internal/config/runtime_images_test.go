package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveRuntimeImageFound 验证按 id 能解析出对应镜像 ref。
func TestResolveRuntimeImageFound(t *testing.T) {
	imgs := []RuntimeImageConfig{
		{ID: "v2026.5.16", Label: "当前", Ref: "repo/hermes:v2026.5.16-x"},
		{ID: "v2026.5.15", Label: "旧版", Ref: "repo/hermes:v2026.5.15-x"},
	}
	ref, ok := ResolveRuntimeImage(imgs, "v2026.5.15")
	require.True(t, ok)
	assert.Equal(t, "repo/hermes:v2026.5.15-x", ref)
}

// TestResolveRuntimeImageMissing 验证未知 id 返回 ok=false。
func TestResolveRuntimeImageMissing(t *testing.T) {
	_, ok := ResolveRuntimeImage(nil, "nope")
	assert.False(t, ok)
}

// TestValidateRuntimeImagesRejectsDuplicateID 验证 id 重复时报错。
func TestValidateRuntimeImagesRejectsDuplicateID(t *testing.T) {
	err := ValidateRuntimeImages([]RuntimeImageConfig{
		{ID: "a", Label: "A", Ref: "r1"},
		{ID: "a", Label: "A2", Ref: "r2"},
	})
	require.Error(t, err)
}

// TestValidateRuntimeImagesRejectsEmptyField 验证 id/ref 为空时报错。
func TestValidateRuntimeImagesRejectsEmptyField(t *testing.T) {
	err := ValidateRuntimeImages([]RuntimeImageConfig{{ID: "a", Label: "A", Ref: ""}})
	require.Error(t, err)
}

// TestValidateRuntimeImagesAcceptsValid 验证合法列表通过校验。
func TestValidateRuntimeImagesAcceptsValid(t *testing.T) {
	err := ValidateRuntimeImages([]RuntimeImageConfig{{ID: "a", Label: "A", Ref: "r"}})
	require.NoError(t, err)
}

// TestValidateAICCRuntimeImageRejectsMissing 验证客服专用运行时镜像缺失时启动前拒绝配置，
// 避免 AICC 隐藏应用在创建后错误回退到普通实例的镜像列表。
func TestValidateAICCRuntimeImageRejectsMissing(t *testing.T) {
	err := ValidateAICCRuntimeImage("")
	require.Error(t, err)
	assert.ErrorContains(t, err, "aicc.runtime_image")
}

// TestValidateAICCRuntimeImageRejectsInvalid 验证带空白或缺少仓库路径的镜像引用不会进入运行时。
func TestValidateAICCRuntimeImageRejectsInvalid(t *testing.T) {
	testCases := []struct {
		name string
		ref  string
	}{
		// 引用中含空白会导致 container runtime 无法解析。
		{name: "包含空白", ref: "registry.example.com/app/aicc runtime:v1"},
		// 仅镜像名没有仓库路径，无法满足客服镜像必须独立仓库管理的约束。
		{name: "缺少仓库路径", ref: "oc-manager-aigowork-aicc:v1"},
		// latest 是可变 tag，无法追溯客服运行时实际发布版本。
		{name: "使用浮动 latest 标签", ref: "registry.example.com/app/oc-manager-aigowork-aicc:latest"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAICCRuntimeImage(tc.ref)
			require.Error(t, err)
			assert.ErrorContains(t, err, "aicc.runtime_image")
		})
	}
}

// TestValidateAICCRuntimeImageAcceptsImmutableTag 验证带独立仓库与不可变版本 tag 的客服镜像可用。
func TestValidateAICCRuntimeImageAcceptsImmutableTag(t *testing.T) {
	err := ValidateAICCRuntimeImage("registry.example.com/app/oc-manager-aigowork-aicc:v1.0.0-2026-07-13-abcdef12")
	require.NoError(t, err)
}
