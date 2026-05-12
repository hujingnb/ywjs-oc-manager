// Package channel 的 registry_test 覆盖渠道 adapter 注册表的查找、重复注册和类型校验。
package channel

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
)

// TestRegistryLookup 验证注册表查找的预期行为场景。
func TestRegistryLookup(t *testing.T) {
	registry := NewRegistry()
	err := registry.Register(&fakeAdapter{kind: "wechat"})
	require.NoError(t, err)
	_, err = registry.Lookup("wechat")
	require.NoError(t, err)
	if _, err := registry.Lookup("missing"); !errors.Is(err, ErrAdapterNotFound) {
		t.Fatalf("Lookup() error = %v, want ErrAdapterNotFound", err)
	}
}

// TestRegistryRejectsDuplicate 验证注册表拒绝重复的异常或拒绝路径场景。
func TestRegistryRejectsDuplicate(t *testing.T) {
	registry := NewRegistry()
	err := registry.Register(&fakeAdapter{kind: "wechat"})
	require.NoError(t, err)
	err = registry.Register(&fakeAdapter{kind: "wechat"})
	require.Error(t, err)
}

// TestRegistryRejectsNil 验证注册表拒绝空值的异常或拒绝路径场景。
func TestRegistryRejectsNil(t *testing.T) {
	registry := NewRegistry()
	err := registry.Register(nil)
	require.Error(t, err)
}

// TestRegistryListTypes 验证注册表列表类型的预期行为场景。
func TestRegistryListTypes(t *testing.T) {
	registry := NewRegistry()
	registry.MustRegister(&fakeAdapter{kind: "wechat"})
	registry.MustRegister(&fakeAdapter{kind: "qq"})
	types := registry.Types()
	require.Len(t, types, 2)
}

type fakeAdapter struct {
	kind string
}

func (a *fakeAdapter) Type() string { return a.kind }
func (a *fakeAdapter) BeginAuth(_ context.Context, _ AuthInput) (AuthChallenge, error) {
	return AuthChallenge{}, nil
}
func (a *fakeAdapter) PollAuth(_ context.Context, _ AuthInput) (AuthProgress, error) {
	return AuthProgress{}, nil
}
