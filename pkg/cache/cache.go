// Package cache provides a small generic TTL cache with explicit
// invalidation, for read-mostly lookups a caller wants to keep fresh only
// approximately (a user record on the auth hot path, a taxonomy list) rather
// than re-fetching on every call.
package cache

import (
	"sync"
	"time"
)

// entry pairs a cached value with when it stops being valid.
type entry[V any] struct {
	value     V
	expiresAt time.Time
}

// Cache is a concurrency-safe, generic TTL cache. The zero value is not
// usable; construct one with New.
type Cache[K comparable, V any] struct {
	mu     sync.RWMutex
	ttl    time.Duration
	values map[K]entry[V]
	now    func() time.Time
}

// New creates a cache whose entries expire ttl after they were last Set.
func New[K comparable, V any](ttl time.Duration) *Cache[K, V] {
	return &Cache[K, V]{ttl: ttl, values: map[K]entry[V]{}, now: time.Now}
}

// Get returns the cached value for key and true, or the zero value and false
// if key is absent or its entry has expired. An expired entry is dropped
// on read.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.RLock()
	value, ok := c.values[key]
	c.mu.RUnlock()
	if !ok {
		var zero V
		return zero, false
	}
	if c.now().After(value.expiresAt) {
		c.mu.Lock()
		delete(c.values, key)
		c.mu.Unlock()
		var zero V
		return zero, false
	}
	return value.value, true
}

// Set stores value under key, resetting its TTL.
func (c *Cache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = entry[V]{value: value, expiresAt: c.now().Add(c.ttl)}
}

// Invalidate removes key, if present. A write path calls this after
// successfully updating the underlying store, so a cached read never serves
// stale data past the write that changed it.
func (c *Cache[K, V]) Invalidate(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.values, key)
}

// Clear removes every entry.
func (c *Cache[K, V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values = map[K]entry[V]{}
}

// Len reports the number of entries currently stored, including any that
// have expired but have not yet been read (and so not yet evicted).
func (c *Cache[K, V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.values)
}
