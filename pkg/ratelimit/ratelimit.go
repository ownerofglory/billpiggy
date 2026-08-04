// Package ratelimit provides fixed-window request limits.
package ratelimit

import "context"

// Limiter reports whether one operation identified by key may proceed under a
// fixed-window limit.
//
// Both the in-memory FixedWindow and a durable, cross-replica implementation
// satisfy this interface, so a caller such as the AI assistant can be wired
// with either without changing its own code.
type Limiter interface {
	// Allow reports whether an operation for key is currently permitted.
	Allow(ctx context.Context, key string) (bool, error)
}
