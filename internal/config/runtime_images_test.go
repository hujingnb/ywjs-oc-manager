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
