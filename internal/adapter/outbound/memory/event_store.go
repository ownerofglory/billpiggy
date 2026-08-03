package memory

import (
	"context"
	"sync"

	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// EventStore records events for unit tests and local development.
type EventStore struct {
	mu     sync.RWMutex
	events []outbound.DomainEvent
}

// NewEventStore creates an empty append-only event collection.
func NewEventStore() *EventStore { return &EventStore{} }

// Append records an event.
func (s *EventStore) Append(_ context.Context, event outbound.DomainEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

// Events returns a copy to prevent tests from mutating the store.
func (s *EventStore) Events() []outbound.DomainEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]outbound.DomainEvent(nil), s.events...)
}
