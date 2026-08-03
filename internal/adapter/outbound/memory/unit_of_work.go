package memory

import (
	"context"
	"sync"
)

// Snapshotter is implemented by in-memory repositories that can restore their
// state when a unit of work fails.
type Snapshotter interface {
	// Snapshot copies the current state and returns a function restoring it.
	Snapshot() func()
}

type unitContextKey struct{}

// UnitOfWork gives the in-memory adapters the same all-or-nothing semantics as
// PostgreSQL by snapshotting every participant before the callback runs and
// restoring them when it fails.
//
// Serialising units of work behind one mutex is deliberate: test data sets are
// tiny, and making the fake's rollback semantics real is what lets a service
// test assert that a failed projection write also discards its domain event.
type UnitOfWork struct {
	mu           sync.Mutex
	participants []Snapshotter
}

// NewUnitOfWork creates a unit of work over the given in-memory repositories.
func NewUnitOfWork(participants ...Snapshotter) *UnitOfWork {
	return &UnitOfWork{participants: participants}
}

// Within runs fn, restoring every participant when fn returns an error. Nested
// calls join the enclosing unit instead of taking a second snapshot, so an
// inner failure discards the whole outer unit.
func (u *UnitOfWork) Within(ctx context.Context, fn func(ctx context.Context) error) error {
	if nested, _ := ctx.Value(unitContextKey{}).(bool); nested {
		return fn(ctx)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	restores := make([]func(), 0, len(u.participants))
	for _, participant := range u.participants {
		restores = append(restores, participant.Snapshot())
	}
	if err := fn(context.WithValue(ctx, unitContextKey{}, true)); err != nil {
		for _, restore := range restores {
			restore()
		}
		return err
	}
	return nil
}
