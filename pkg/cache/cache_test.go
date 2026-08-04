package cache_test

import (
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/pkg/cache"
)

func TestCacheGetSetAndMiss(t *testing.T) {
	t.Parallel()
	c := cache.New[string, int](time.Minute)
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected a miss on an empty cache")
	}
	c.Set("a", 42)
	value, ok := c.Get("a")
	if !ok || value != 42 {
		t.Fatalf("get = %d, %v, want 42, true", value, ok)
	}
}

func TestCacheEntryExpires(t *testing.T) {
	t.Parallel()
	c := cache.New[string, int](time.Millisecond)
	c.Set("a", 1)
	time.Sleep(5 * time.Millisecond)
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected the entry to have expired")
	}
	if c.Len() != 0 {
		t.Fatalf("len = %d, want 0 after the expired entry is evicted on read", c.Len())
	}
}

func TestCacheInvalidate(t *testing.T) {
	t.Parallel()
	c := cache.New[string, int](time.Minute)
	c.Set("a", 1)
	c.Invalidate("a")
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected a miss after invalidation")
	}
	// Invalidating an absent key is a no-op, not an error.
	c.Invalidate("does-not-exist")
}

func TestCacheClear(t *testing.T) {
	t.Parallel()
	c := cache.New[string, int](time.Minute)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Clear()
	if c.Len() != 0 {
		t.Fatalf("len = %d, want 0 after Clear", c.Len())
	}
}
