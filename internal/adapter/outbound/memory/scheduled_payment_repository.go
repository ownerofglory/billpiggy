package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// postingKey identifies one handled occurrence, matching the PostgreSQL
// primary key on (scheduled_payment_id, due_at, kind).
type postingKey struct {
	paymentID string
	dueAt     int64
	kind      domain.PostingKind
}

// ScheduledPaymentRepository is a concurrency-safe in-memory scheduled
// payment projection and posting ledger.
type ScheduledPaymentRepository struct {
	mu       sync.RWMutex
	payments map[string]domain.ScheduledPayment
	postings map[postingKey]domain.ScheduledPaymentPosting
}

// NewScheduledPaymentRepository creates an empty scheduled payment projection.
func NewScheduledPaymentRepository() *ScheduledPaymentRepository {
	return &ScheduledPaymentRepository{
		payments: map[string]domain.ScheduledPayment{},
		postings: map[postingKey]domain.ScheduledPaymentPosting{},
	}
}

// CreateScheduledPayment records a new recurring payment.
func (r *ScheduledPaymentRepository) CreateScheduledPayment(_ context.Context, payment domain.ScheduledPayment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.payments[payment.ID] = payment
	return nil
}

// ListScheduledPayments returns payments owned by or shared with the viewer,
// newest first.
func (r *ScheduledPaymentRepository) ListScheduledPayments(_ context.Context, ownerID string, sharedGroupIDs []string) ([]domain.ScheduledPayment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := []domain.ScheduledPayment{}
	for _, value := range r.payments {
		if (value.OwnerID == ownerID || containsGroup(sharedGroupIDs, value.SharedGroupID)) && value.DeletedAt == nil {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].CreatedAt.After(values[j].CreatedAt) })
	return values, nil
}

// GetScheduledPayment returns a payment owned by or shared with the viewer.
func (r *ScheduledPaymentRepository) GetScheduledPayment(_ context.Context, ownerID, paymentID string, sharedGroupIDs []string) (domain.ScheduledPayment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.payments[paymentID]
	if !ok || (value.OwnerID != ownerID && !containsGroup(sharedGroupIDs, value.SharedGroupID)) || value.DeletedAt != nil {
		return domain.ScheduledPayment{}, errNotFound
	}
	return value, nil
}

// UpdateScheduledPayment replaces an owner-scoped payment.
func (r *ScheduledPaymentRepository) UpdateScheduledPayment(_ context.Context, payment domain.ScheduledPayment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.payments[payment.ID]
	if !ok || existing.OwnerID != payment.OwnerID || existing.DeletedAt != nil {
		return errNotFound
	}
	r.payments[payment.ID] = payment
	return nil
}

// DeleteScheduledPayment soft-deletes an owner-scoped payment.
func (r *ScheduledPaymentRepository) DeleteScheduledPayment(_ context.Context, ownerID, paymentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.payments[paymentID]
	if !ok || value.OwnerID != ownerID || value.DeletedAt != nil {
		return errNotFound
	}
	deletedAt := time.Now().UTC()
	value.DeletedAt = &deletedAt
	r.payments[paymentID] = value
	return nil
}

// ListDueScheduledPayments returns active payments due at or before through,
// oldest occurrence first so a backlog drains in order.
func (r *ScheduledPaymentRepository) ListDueScheduledPayments(_ context.Context, through time.Time, limit int) ([]domain.ScheduledPayment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := []domain.ScheduledPayment{}
	for _, value := range r.payments {
		if value.DeletedAt != nil || value.Paused {
			continue
		}
		if value.NextDueAt.After(through) {
			continue
		}
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].NextDueAt.Before(values[j].NextDueAt) })
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	return values, nil
}

// ClaimPosting records one handled occurrence, reporting ErrPostingExists when
// it was already claimed.
func (r *ScheduledPaymentRepository) ClaimPosting(_ context.Context, posting domain.ScheduledPaymentPosting) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := postingKey{paymentID: posting.ScheduledPaymentID, dueAt: posting.DueAt.UTC().UnixNano(), kind: posting.Kind}
	if _, exists := r.postings[key]; exists {
		return outbound.ErrPostingExists
	}
	r.postings[key] = posting
	return nil
}

// AdvanceSchedule moves a payment's cursor to its next occurrence.
func (r *ScheduledPaymentRepository) AdvanceSchedule(_ context.Context, paymentID string, nextDueAt, lastPostedAt time.Time, paused bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.payments[paymentID]
	if !ok || value.DeletedAt != nil {
		return errNotFound
	}
	posted := lastPostedAt.UTC()
	value.NextDueAt, value.LastPostedAt, value.Paused = nextDueAt, &posted, paused
	r.payments[paymentID] = value
	return nil
}

// Snapshot copies the projection and returns a function restoring it.
func (r *ScheduledPaymentRepository) Snapshot() func() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	payments := make(map[string]domain.ScheduledPayment, len(r.payments))
	for id, value := range r.payments {
		payments[id] = value
	}
	postings := make(map[postingKey]domain.ScheduledPaymentPosting, len(r.postings))
	for key, value := range r.postings {
		postings[key] = value
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.payments, r.postings = payments, postings
	}
}
