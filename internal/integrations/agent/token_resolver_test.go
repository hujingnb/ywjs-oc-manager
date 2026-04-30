package agent

import (
	"errors"
	"sync"
	"testing"
)

func TestTokenResolver_SetAndGet(t *testing.T) {
	r := NewTokenResolver()
	r.Set("node-1", "token-a")
	got, err := r.Get("node-1")
	if err != nil {
		t.Fatalf("Get err = %v", err)
	}
	if got != "token-a" {
		t.Fatalf("Get = %q, want token-a", got)
	}
}

func TestTokenResolver_GetMissingReturnsErr(t *testing.T) {
	r := NewTokenResolver()
	_, err := r.Get("missing")
	if !errors.Is(err, ErrTokenNotCached) {
		t.Fatalf("Get err = %v, want ErrTokenNotCached", err)
	}
}

func TestTokenResolver_OverwriteUpdatesValue(t *testing.T) {
	r := NewTokenResolver()
	r.Set("n", "first")
	r.Set("n", "second")
	got, _ := r.Get("n")
	if got != "second" {
		t.Fatalf("Get = %q, want second", got)
	}
}

func TestTokenResolver_Forget(t *testing.T) {
	r := NewTokenResolver()
	r.Set("n", "x")
	r.Forget("n")
	if _, err := r.Get("n"); !errors.Is(err, ErrTokenNotCached) {
		t.Fatalf("Forget 后 Get err = %v, want ErrTokenNotCached", err)
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
