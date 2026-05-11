package agent

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

func TestTokenResolver_SetAndGet(t *testing.T) {
	r := NewTokenResolver()
	r.Set("node-1", "token-a")
	got, err := r.Get("node-1")
	require.NoError(t, err)
	require.Equal(t, "token-a", got)
}

func TestTokenResolver_GetMissingReturnsErr(t *testing.T) {
	r := NewTokenResolver()
	_, err := r.Get("missing")
	require.ErrorIs(t, err, ErrTokenNotCached)
}

func TestTokenResolver_OverwriteUpdatesValue(t *testing.T) {
	r := NewTokenResolver()
	r.Set("n", "first")
	r.Set("n", "second")
	got, _ := r.Get("n")
	require.Equal(t, "second", got)
}

func TestTokenResolver_Forget(t *testing.T) {
	r := NewTokenResolver()
	r.Set("n", "x")
	r.Forget("n")
	if _, err := r.Get("n"); !errors.Is(err, ErrTokenNotCached) {
		t.Fatalf("Forget 后 Get err = %v, want ErrTokenNotCached", err)
	}
}

// stubLoader 验证 PersistentTokenLoader 路径。
type stubLoader struct {
	tokens map[string]string
	calls  int
	err    error
}

func (s *stubLoader) LoadAgentToken(_ context.Context, nodeID string) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return s.tokens[nodeID], nil
}

func TestTokenResolver_FallsBackToPersistentLoader(t *testing.T) {
	r := NewTokenResolver()
	r.SetPersistentLoader(&stubLoader{tokens: map[string]string{"n1": "loaded"}})
	got, err := r.Get("n1")
	require.NoError(t, err)
	require.Equal(t, "loaded", got)
	// 第二次应当命中 cache，不再触发 loader。
	loader := &stubLoader{tokens: map[string]string{"n1": "different"}}
	r.SetPersistentLoader(loader)
	got, _ = r.Get("n1")
	require.Equal(t, "loaded", got)
	require.Equal(t, 0, loader.calls)
}

func TestTokenResolver_LoaderEmptyReturnsErrTokenNotCached(t *testing.T) {
	r := NewTokenResolver()
	r.SetPersistentLoader(&stubLoader{tokens: map[string]string{}})
	if _, err := r.Get("missing"); !errors.Is(err, ErrTokenNotCached) {
		t.Fatalf("err = %v, want ErrTokenNotCached", err)
	}
}

func TestTokenResolver_ConcurrentSafe(t *testing.T) {
	r := NewTokenResolver()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			r.Set("n", "v")
			_, _ = r.Get("n")
		}(i)
		go func(i int) {
			defer wg.Done()
			r.Forget("n")
		}(i)
	}
	wg.Wait()
}
