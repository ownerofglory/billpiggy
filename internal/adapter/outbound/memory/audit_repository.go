package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// AuditRepository is an in-memory audit trail for local development and tests.
type AuditRepository struct {
	mu       sync.RWMutex
	entries  []domain.AuditEntry
	byEvent  map[string]struct{}
	sequence int
}

// NewAuditRepository creates an empty audit trail.
func NewAuditRepository() *AuditRepository {
	return &AuditRepository{byEvent: map[string]struct{}{}}
}

// AppendEntry records one entry, ignoring an already-recorded source event.
func (r *AuditRepository) AppendEntry(_ context.Context, entry domain.AuditEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry.EventID != "" {
		if _, exists := r.byEvent[entry.EventID]; exists {
			return nil
		}
		r.byEvent[entry.EventID] = struct{}{}
	}
	r.sequence++
	r.entries = append(r.entries, entry)
	return nil
}

// ListEntries returns audit entries matching the filter, newest first.
func (r *AuditRepository) ListEntries(_ context.Context, filter outbound.AuditFilter) ([]domain.AuditEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	matching := make([]domain.AuditEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		if filter.ActorID != "" && entry.ActorID != filter.ActorID {
			continue
		}
		if filter.ResourceType != "" && entry.ResourceType != filter.ResourceType {
			continue
		}
		if filter.ResourceID != "" && entry.ResourceID != filter.ResourceID {
			continue
		}
		if filter.Action != "" && entry.Action != filter.Action {
			continue
		}
		if !filter.From.IsZero() && entry.OccurredAt.Before(filter.From) {
			continue
		}
		if !filter.To.IsZero() && entry.OccurredAt.After(filter.To) {
			continue
		}
		matching = append(matching, entry)
	}
	sort.SliceStable(matching, func(i, j int) bool { return matching[i].OccurredAt.After(matching[j].OccurredAt) })
	start := min(filter.Offset, len(matching))
	end := len(matching)
	if filter.Limit > 0 {
		end = min(start+filter.Limit, len(matching))
	}
	return matching[start:end], nil
}

// Snapshot copies the trail and returns a function restoring it.
func (r *AuditRepository) Snapshot() func() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries := append([]domain.AuditEntry(nil), r.entries...)
	byEvent := make(map[string]struct{}, len(r.byEvent))
	for id := range r.byEvent {
		byEvent[id] = struct{}{}
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.entries, r.byEvent = entries, byEvent
	}
}
