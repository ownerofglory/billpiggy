package service

import (
	"context"
	"errors"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// AuditService exposes the audit trail to super-administrators.
type AuditService struct {
	repository outbound.AuditRepository
}

// NewAuditService creates an audit query service.
func NewAuditService(repository outbound.AuditRepository) (*AuditService, error) {
	if repository == nil {
		return nil, errors.New("audit repository is required")
	}
	return &AuditService{repository: repository}, nil
}

// ListEntries returns audit entries matching filter, restricted to
// super-administrators.
func (s *AuditService) ListEntries(ctx context.Context, actor domain.AppUser, filter outbound.AuditFilter) ([]domain.AuditEntry, error) {
	if !actor.Role.Allows(domain.PermissionAuditRead) {
		return nil, ErrForbidden
	}
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 50
	}
	return s.repository.ListEntries(ctx, filter)
}
