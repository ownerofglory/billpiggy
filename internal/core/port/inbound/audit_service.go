package inbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// AuditService is everything an HTTP handler needs from audit-trail queries.
type AuditService interface {
	ListEntries(ctx context.Context, actor domain.AppUser, filter outbound.AuditFilter) ([]domain.AuditEntry, error)
}
