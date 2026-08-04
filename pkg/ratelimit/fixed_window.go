package ratelimit

import (
	"context"
	"sync"
	"time"
)

// evictAfterCalls bounds how often FixedWindow sweeps expired entries, so the
// cost of eviction stays amortised across many Allow calls rather than
// happening on every one.
const evictAfterCalls = 1000

// FixedWindow limits one key to a bounded number of operations per interval.
//
// It is process-local: limits reset on restart and are independent per
// replica. That is fine for a single-node deployment or as the fallback when
// no durable Limiter is configured, but multi-replica production traffic
// wants a store-backed Limiter instead.
type FixedWindow struct {
	mu       sync.Mutex
	limit    int
	interval time.Duration
	entries  map[string]entry
	calls    uint64
}

type entry struct {
	started time.Time
	count   int
}

// NewFixedWindow creates a fixed-window limiter.
func NewFixedWindow(limit int, interval time.Duration) *FixedWindow {
	return &FixedWindow{limit: limit, interval: interval, entries: map[string]entry{}}
}

// Allow reports whether one operation may proceed for key.
func (l *FixedWindow) Allow(_ context.Context, key string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.calls++
	if l.calls%evictAfterCalls == 0 {
		l.evictExpiredLocked(now)
	}
	value := l.entries[key]
	if value.started.IsZero() || now.Sub(value.started) >= l.interval {
		l.entries[key] = entry{started: now, count: 1}
		return true, nil
	}
	if value.count >= l.limit {
		return false, nil
	}
	value.count++
	l.entries[key] = value
	return true, nil
}

// Len returns the number of keys currently tracked. It is mainly useful for
// verifying that eviction keeps memory bounded.
func (l *FixedWindow) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// evictExpiredLocked removes entries whose window has closed. A key that is
// evicted while idle simply starts a fresh window on its next Allow call, so
// this is always safe: it only ever forgets keys with nothing left to track.
func (l *FixedWindow) evictExpiredLocked(now time.Time) {
	for key, value := range l.entries {
		if now.Sub(value.started) >= l.interval {
			delete(l.entries, key)
		}
	}
}
