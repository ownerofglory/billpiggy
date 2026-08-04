//go:build integration

package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	postgresadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/postgres"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

func TestRateLimiterAllowsUpToTheLimitAcrossCallers(t *testing.T) {
	pool := newPool(t)
	limiter := postgresadapter.NewRateLimiter(pool, 3, time.Minute)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		allowed, err := limiter.Allow(ctx, "owner-1")
		if err != nil || !allowed {
			t.Fatalf("request %d: allowed=%v err=%v, want allowed", i+1, allowed, err)
		}
	}
	if allowed, err := limiter.Allow(ctx, "owner-1"); err != nil || allowed {
		t.Fatalf("4th request: allowed=%v err=%v, want denied", allowed, err)
	}
	// A different key is a different window, unaffected by the first.
	if allowed, err := limiter.Allow(ctx, "owner-2"); err != nil || !allowed {
		t.Fatalf("owner-2 was limited by owner-1's window: %v, %v", allowed, err)
	}
}

func TestRateLimiterIsSharedAcrossConcurrentCallers(t *testing.T) {
	pool := newPool(t)
	// This is the property FixedWindow cannot give you: every "replica" here
	// shares the same count because they all hit the same table.
	limiter := postgresadapter.NewRateLimiter(pool, 20, time.Minute)
	ctx := context.Background()

	var wg sync.WaitGroup
	allowedCount := make(chan bool, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, err := limiter.Allow(ctx, "shared-owner")
			if err != nil {
				t.Errorf("Allow: %v", err)
				return
			}
			allowedCount <- allowed
		}()
	}
	wg.Wait()
	close(allowedCount)
	granted := 0
	for allowed := range allowedCount {
		if allowed {
			granted++
		}
	}
	if granted != 20 {
		t.Fatalf("granted %d of 50 concurrent requests, want exactly the limit of 20", granted)
	}
}

func TestRateLimiterCleanupRemovesOnlyExpiredWindows(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	// A window's age is measured from window_start regardless of the interval
	// that produced it, so "stale" is seeded directly here rather than by
	// waiting on a real interval to elapse.
	if _, err := pool.Exec(ctx, `insert into ratelimit.windows (key, window_start, count) values ($1, now() - interval '2 hours', 5)`, "stale-owner"); err != nil {
		t.Fatalf("seed stale window: %v", err)
	}
	limiter := postgresadapter.NewRateLimiter(pool, 10, time.Minute)
	if _, err := limiter.Allow(ctx, "fresh-owner"); err != nil {
		t.Fatalf("Allow: %v", err)
	}

	removed, err := limiter.CleanupExpired(ctx, time.Hour)
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed %d windows, want exactly the stale one", removed)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `select count(*) from ratelimit.windows where key = 'fresh-owner'`).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("fresh window was removed by cleanup: %d rows remain", remaining)
	}
}

func TestAIRequestRepositoryRecordsUsage(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewAIRequestRepository(pool)
	owner := seedUser(t, pool, "owner@example.test")
	ctx := context.Background()

	record := domain.AIRequestRecord{
		UserID: owner, Workload: domain.AIWorkloadAssistant, Model: "gpt-5.6-luna",
		Usage:     domain.TokenUsage{InputTokens: 40, OutputTokens: 8, TotalTokens: 48},
		LatencyMS: 120, Outcome: domain.AIRequestSuccess, CreatedAt: time.Now().UTC(),
	}
	if err := repository.RecordRequest(ctx, record); err != nil {
		t.Fatalf("record request: %v", err)
	}

	var workload, model, outcome string
	var totalTokens int64
	if err := pool.QueryRow(ctx, `select workload, model, total_tokens, outcome from ai.requests where user_id = $1`, owner).
		Scan(&workload, &model, &totalTokens, &outcome); err != nil {
		t.Fatalf("read recorded request: %v", err)
	}
	if workload != "assistant" || model != "gpt-5.6-luna" || totalTokens != 48 || outcome != "success" {
		t.Fatalf("recorded row = workload=%s model=%s tokens=%d outcome=%s", workload, model, totalTokens, outcome)
	}
}

func TestIdentityRepositoryPersistsAIEnabled(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewIdentityRepository(pool)
	ctx := context.Background()

	ownerID := seedUser(t, pool, "owner@example.test")
	loaded, err := repository.GetUserByID(ctx, ownerID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if !loaded.AIEnabled {
		t.Fatal("ai_enabled default was not true for a freshly seeded user")
	}

	loaded.AIEnabled = false
	if err := repository.UpdateUser(ctx, loaded); err != nil {
		t.Fatalf("update user: %v", err)
	}
	reloaded, err := repository.GetUserByID(ctx, ownerID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if reloaded.AIEnabled {
		t.Fatal("ai_enabled=false did not persist")
	}
}
