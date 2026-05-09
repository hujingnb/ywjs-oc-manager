package channel

import (
	"context"
	"errors"
	"testing"
	"github.com/stretchr/testify/require"
)

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

func TestRegistryRejectsDuplicate(t *testing.T) {
	registry := NewRegistry()
	err := registry.Register(&fakeAdapter{kind: "wechat"})
	require.NoError(t, err)
	err = registry.Register(&fakeAdapter{kind: "wechat"})
	require.Error(t, err)
}

func TestRegistryRejectsNil(t *testing.T) {
	registry := NewRegistry()
	err := registry.Register(nil)
	require.Error(t, err)
}

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
