package channel

import (
	"context"
	"errors"
	"testing"
)

func TestRegistryLookup(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&fakeAdapter{kind: "wechat"}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := registry.Lookup("wechat"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if _, err := registry.Lookup("missing"); !errors.Is(err, ErrAdapterNotFound) {
		t.Fatalf("Lookup() error = %v, want ErrAdapterNotFound", err)
	}
}

func TestRegistryRejectsDuplicate(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&fakeAdapter{kind: "wechat"}); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := registry.Register(&fakeAdapter{kind: "wechat"}); err == nil {
		t.Fatalf("expected duplicate registration to fail")
	}
}

func TestRegistryRejectsNil(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(nil); err == nil {
		t.Fatalf("expected nil adapter to fail")
	}
}

func TestRegistryListTypes(t *testing.T) {
	registry := NewRegistry()
	registry.MustRegister(&fakeAdapter{kind: "wechat"})
	registry.MustRegister(&fakeAdapter{kind: "qq"})
	types := registry.Types()
	if len(types) != 2 {
		t.Fatalf("types = %+v", types)
	}
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
