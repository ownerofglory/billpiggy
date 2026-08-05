package inbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// ScheduledPaymentService is everything an HTTP handler needs from recurring
// payment commands and queries. PostDue is deliberately absent: it belongs to
// the background scheduler, not to any request.
type ScheduledPaymentService interface {
	CreateScheduledPayment(ctx context.Context, owner domain.AppUser, payment domain.ScheduledPayment) (domain.ScheduledPayment, error)
	ListScheduledPayments(ctx context.Context, viewer domain.AppUser) ([]domain.ScheduledPayment, error)
	GetScheduledPayment(ctx context.Context, viewer domain.AppUser, paymentID string) (domain.ScheduledPayment, error)
	UpdateScheduledPayment(ctx context.Context, owner domain.AppUser, paymentID string, update domain.ScheduledPayment) (domain.ScheduledPayment, error)
	DeleteScheduledPayment(ctx context.Context, owner domain.AppUser, paymentID string) error
}
