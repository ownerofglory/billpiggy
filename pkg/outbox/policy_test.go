package outbox_test

import (
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/pkg/outbox"
)

func TestPolicyRetryDelayGrowsExponentiallyAndCaps(t *testing.T) {
	t.Parallel()
	policy := outbox.Policy{MaxAttempts: 10, BaseBackoff: time.Second, MaxBackoff: 8 * time.Second, LeaseTTL: time.Minute}
	for _, test := range []struct {
		attempts int
		want     time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 8 * time.Second},
		{50, 8 * time.Second},
	} {
		if got := policy.RetryDelay(test.attempts); got != test.want {
			t.Fatalf("RetryDelay(%d) = %s, want %s", test.attempts, got, test.want)
		}
	}
}

func TestPolicyRetryDelayFallsBackWhenUnset(t *testing.T) {
	t.Parallel()
	// A zero Policy must still produce a usable delay rather than busy-looping.
	if got := (outbox.Policy{}).RetryDelay(1); got != time.Second {
		t.Fatalf("zero-policy RetryDelay(1) = %s, want 1s", got)
	}
}

func TestDefaultPolicyIsConservative(t *testing.T) {
	t.Parallel()
	policy := outbox.DefaultPolicy()
	if policy.MaxAttempts != 8 || policy.BaseBackoff != time.Second || policy.MaxBackoff != 5*time.Minute || policy.LeaseTTL != time.Minute {
		t.Fatalf("unexpected default policy %#v", policy)
	}
}

func TestStatusString(t *testing.T) {
	t.Parallel()
	for status, want := range map[outbox.Status]string{
		outbox.Idle:         "idle",
		outbox.Processed:    "processed",
		outbox.Retried:      "retried",
		outbox.DeadLettered: "dead_lettered",
	} {
		if got := status.String(); got != want {
			t.Fatalf("Status(%d).String() = %q, want %q", status, got, want)
		}
	}
}
