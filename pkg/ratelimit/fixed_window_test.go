package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/pkg/ratelimit"
)

func TestFixedWindowAllowsUpToTheLimit(t *testing.T) {
	t.Parallel()
	limiter := ratelimit.NewFixedWindow(3, time.Minute)
	for i := 0; i < 3; i++ {
		allowed, err := limiter.Allow(context.Background(), "owner-1")
		if err != nil || !allowed {
			t.Fatalf("request %d: allowed=%v err=%v, want allowed", i+1, allowed, err)
		}
	}
	allowed, err := limiter.Allow(context.Background(), "owner-1")
	if err != nil || allowed {
		t.Fatalf("4th request: allowed=%v err=%v, want denied", allowed, err)
	}
}

func TestFixedWindowIsPerKey(t *testing.T) {
	t.Parallel()
	limiter := ratelimit.NewFixedWindow(1, time.Minute)
	if allowed, err := limiter.Allow(context.Background(), "owner-1"); err != nil || !allowed {
		t.Fatalf("owner-1 first request denied: %v, %v", allowed, err)
	}
	if allowed, err := limiter.Allow(context.Background(), "owner-1"); err != nil || allowed {
		t.Fatalf("owner-1 second request should be denied: %v, %v", allowed, err)
	}
	if allowed, err := limiter.Allow(context.Background(), "owner-2"); err != nil || !allowed {
		t.Fatalf("owner-2 was limited by owner-1's window: %v, %v", allowed, err)
	}
}

func TestFixedWindowEvictsExpiredEntries(t *testing.T) {
	t.Parallel()
	// A very short interval means every entry is expired by the time the
	// eviction sweep runs, so the map must shrink back down rather than grow
	// forever across many distinct keys.
	limiter := ratelimit.NewFixedWindow(1, time.Nanosecond)
	for i := 0; i < 2500; i++ {
		key := string(rune('a' + i%26))
		if _, err := limiter.Allow(context.Background(), key); err != nil {
			t.Fatalf("Allow: %v", err)
		}
	}
	if size := limiter.Len(); size > 26 {
		t.Fatalf("tracked %d entries after eviction sweeps, want at most the 26 distinct keys", size)
	}
}
