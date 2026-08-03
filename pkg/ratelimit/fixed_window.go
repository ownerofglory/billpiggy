// Package ratelimit provides small in-memory limits for low-volume deployments.
package ratelimit

import (
	"sync"
	"time"
)

// FixedWindow limits one key to a bounded number of operations per interval.
type FixedWindow struct {
	mu       sync.Mutex
	limit    int
	interval time.Duration
	entries  map[string]entry
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
func (l *FixedWindow) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	value := l.entries[key]
	if value.started.IsZero() || now.Sub(value.started) >= l.interval {
		l.entries[key] = entry{started: now, count: 1}
		return true
	}
	if value.count >= l.limit {
		return false
	}
	value.count++
	l.entries[key] = value
	return true
}
