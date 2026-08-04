package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// RateLimiter is a durable, cross-replica fixed-window limiter backed by
// PostgreSQL. Unlike pkg/ratelimit.FixedWindow, every replica shares the same
// window, and counts survive a restart.
type RateLimiter struct {
	pool     *pgxpool.Pool
	limit    int
	interval time.Duration
}

// NewRateLimiter creates a durable fixed-window limiter.
func NewRateLimiter(pool *pgxpool.Pool, limit int, interval time.Duration) *RateLimiter {
	return &RateLimiter{pool: pool, limit: limit, interval: interval}
}

// Allow reports whether one operation may proceed for key.
//
// The window boundary is computed in SQL from the interval length rather than
// passed in from Go, so concurrent callers across replicas always agree on
// which window "now" falls into.
func (l *RateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	var count int
	err := pgxtx.From(ctx, l.pool).QueryRow(ctx, `
		insert into ratelimit.windows (key, window_start, count)
		select $1,
		       to_timestamp(floor(extract(epoch from now()) / $2::double precision) * $2::double precision),
		       1
		on conflict (key, window_start) do update
		   set count = ratelimit.windows.count + 1
		returning count`, key, l.interval.Seconds()).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("increment rate limit window: %w", err)
	}
	return count <= l.limit, nil
}

// CleanupExpired deletes windows whose window_start is older than retain, so
// the table does not grow without bound. retain is measured from window_start,
// not from when a window closed, and the table does not record each key's
// interval — so callers must choose retain comfortably larger than the
// longest interval any limiter using this table is configured with, or an
// still-open long-interval window can be deleted while active. It is safe to
// call from any replica on a schedule.
func (l *RateLimiter) CleanupExpired(ctx context.Context, retain time.Duration) (int64, error) {
	command, err := l.pool.Exec(ctx, `delete from ratelimit.windows where window_start < now() - make_interval(secs => $1)`, retain.Seconds())
	if err != nil {
		return 0, fmt.Errorf("clean up rate limit windows: %w", err)
	}
	return command.RowsAffected(), nil
}
