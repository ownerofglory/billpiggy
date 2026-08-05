package outbound

import (
	"context"
	"errors"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// ErrPostingExists reports that another replica already claimed the same
// occurrence of a scheduled payment. It mirrors ErrReportExists: the unique
// constraint is the coordination primitive, so a caller treats this as "not
// mine to handle" rather than as a failure.
var ErrPostingExists = errors.New("scheduled payment posting already exists")

// ScheduledPaymentRepository owns the scheduled-payment projection and the
// ledger of occurrences already handled.
type ScheduledPaymentRepository interface {
	// CreateScheduledPayment records a new recurring payment.
	CreateScheduledPayment(ctx context.Context, payment domain.ScheduledPayment) error
	// ListScheduledPayments returns payments owned by the viewer or shared
	// with one of sharedGroupIDs, newest first.
	ListScheduledPayments(ctx context.Context, ownerID string, sharedGroupIDs []string) ([]domain.ScheduledPayment, error)
	// GetScheduledPayment returns one payment visible to the viewer.
	GetScheduledPayment(ctx context.Context, ownerID, paymentID string, sharedGroupIDs []string) (domain.ScheduledPayment, error)
	// UpdateScheduledPayment replaces an owner-scoped payment's editable fields.
	UpdateScheduledPayment(ctx context.Context, payment domain.ScheduledPayment) error
	// DeleteScheduledPayment soft-deletes an owner-scoped payment.
	DeleteScheduledPayment(ctx context.Context, ownerID, paymentID string) error

	// ListDueScheduledPayments returns active payments whose next occurrence
	// falls at or before through, across every owner, for the scheduler.
	// through is the due date plus the longest reminder lead time, so a
	// payment is returned early enough to send its advance notice.
	ListDueScheduledPayments(ctx context.Context, through time.Time, limit int) ([]domain.ScheduledPayment, error)
	// ClaimPosting records that one occurrence has been handled, returning
	// ErrPostingExists when another replica claimed it first.
	ClaimPosting(ctx context.Context, posting domain.ScheduledPaymentPosting) error
	// AdvanceSchedule moves a payment's cursor to its next occurrence, and
	// pauses it once the recurrence has passed its end date.
	AdvanceSchedule(ctx context.Context, paymentID string, nextDueAt time.Time, lastPostedAt time.Time, paused bool) error
}
