package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/pkg/outbox"
)

// fakeStore is a minimal outbox.Store the engine tests drive directly,
// avoiding a real database for behaviour that only depends on ProcessNext
// and Lag outcomes.
type fakeStore struct {
	results []outbox.Result
	err     error
	lag     int64
}

func (s *fakeStore) ProcessNext(context.Context, string, []string, outbox.Policy, string, func(context.Context, outbox.Message) error) (outbox.Result, error) {
	if s.err != nil {
		return outbox.Result{}, s.err
	}
	if len(s.results) == 0 {
		return outbox.Result{Status: outbox.Idle}, nil
	}
	result := s.results[0]
	s.results = s.results[1:]
	return result, nil
}

func (s *fakeStore) Lag(context.Context, string) (int64, error) {
	return s.lag, nil
}

type fakeHandler struct{ name string }

func (h fakeHandler) Name() string                                 { return h.name }
func (h fakeHandler) AggregateTypes() []string                     { return nil }
func (h fakeHandler) Handle(context.Context, outbox.Message) error { return nil }

// TestEngineHealthRecoversAfterADeadLetterOnceLagClears guards against a
// readiness check that, once tripped by a single dead-lettered message,
// never recovers on its own: Store.Lag only counts pending messages, so a
// dead letter (status 'dead') drops out of it immediately, and a subscription
// that keeps making progress on everything after it is not actually stuck.
func TestEngineHealthRecoversAfterADeadLetterOnceLagClears(t *testing.T) {
	t.Parallel()
	store := &fakeStore{results: []outbox.Result{
		{Status: outbox.DeadLettered, Message: outbox.Message{EventType: "poison"}, Err: errors.New("poison")},
	}}
	engine, err := outbox.NewEngine(store, fakeHandler{name: "sub"}, outbox.Options{})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := engine.Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if stats := engine.Stats(); stats.DeadLettered != 1 {
		t.Fatalf("DeadLettered = %d, want 1", stats.DeadLettered)
	}

	store.lag = 0
	if err := engine.Health(time.Minute)(context.Background()); err != nil {
		t.Fatalf("Health() = %v, want nil once lag has cleared past a dead letter", err)
	}
}

// TestEngineHealthFailsWhileLagIsStale confirms the check still does its one
// real job: a subscription with a growing backlog that hasn't progressed
// within the staleness window must fail readiness.
func TestEngineHealthFailsWhileLagIsStale(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store := &fakeStore{err: errors.New("boom"), lag: 5}
	engine, err := outbox.NewEngine(store, fakeHandler{name: "sub"}, outbox.Options{Clock: clock})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := engine.Step(context.Background()); err == nil {
		t.Fatal("want step to report the store error")
	}

	now = now.Add(5 * time.Minute)
	if err := engine.Health(time.Minute)(context.Background()); err == nil {
		t.Fatal("want Health to fail once lag has been stale past the staleness window")
	}
}
