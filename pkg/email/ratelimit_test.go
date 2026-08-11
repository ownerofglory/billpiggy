package email

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/pkg/ratelimit"
)

// recordingSender is a fake Sender that always succeeds and records who it
// was asked to send to.
type recordingSender struct{ sends []string }

func (s *recordingSender) Send(_ context.Context, to, _, _, _ string) error {
	s.sends = append(s.sends, to)
	return nil
}

func TestWithSendLimitBlocksOnceTheLimitIsReached(t *testing.T) {
	t.Parallel()
	inner := &recordingSender{}
	limited := WithSendLimit(inner, ratelimit.NewFixedWindow(2, time.Minute))

	for i := 0; i < 2; i++ {
		if err := limited.Send(context.Background(), "user@example.com", "s", "t", "h"); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	err := limited.Send(context.Background(), "user@example.com", "s", "t", "h")
	if !errors.Is(err, ErrSendLimitExceeded) {
		t.Fatalf("err = %v, want ErrSendLimitExceeded", err)
	}
	if len(inner.sends) != 2 {
		t.Fatalf("inner sends = %d, want exactly 2 (the third must never reach the underlying sender)", len(inner.sends))
	}
}

func TestWithSendLimitSharesOneCounterAcrossRecipients(t *testing.T) {
	t.Parallel()
	// The quota belongs to the provider account, not any one recipient: two
	// different addresses must still share the same budget.
	inner := &recordingSender{}
	limited := WithSendLimit(inner, ratelimit.NewFixedWindow(1, time.Minute))

	if err := limited.Send(context.Background(), "first@example.com", "s", "t", "h"); err != nil {
		t.Fatalf("first send: %v", err)
	}
	if err := limited.Send(context.Background(), "second@example.com", "s", "t", "h"); !errors.Is(err, ErrSendLimitExceeded) {
		t.Fatalf("err = %v, want ErrSendLimitExceeded for a different recipient sharing the same quota", err)
	}
}

func TestWithSendLimitPassesThroughTheUnderlyingSenderError(t *testing.T) {
	t.Parallel()
	failure := errors.New("provider unavailable")
	limited := WithSendLimit(failingSender{err: failure}, ratelimit.NewFixedWindow(10, time.Minute))
	if err := limited.Send(context.Background(), "user@example.com", "s", "t", "h"); !errors.Is(err, failure) {
		t.Fatalf("err = %v, want the underlying sender's own error", err)
	}
}

type failingSender struct{ err error }

func (s failingSender) Send(context.Context, string, string, string, string) error { return s.err }
