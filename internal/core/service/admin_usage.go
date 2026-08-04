package service

import (
	"context"
	"errors"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// auditSummaryScanLimit bounds how many audit entries the usage summary
// counts, so an unbounded query never backs an admin dashboard call.
const auditSummaryScanLimit = 10000

// UsageSummary is a point-in-time snapshot of account, AI, and notification
// activity for the super-admin usage dashboard.
type UsageSummary struct {
	// Since is the inclusive start of the summarised window.
	Since time.Time
	// TotalUsers, UsersByRole, and BlockedUsers describe the current account population.
	TotalUsers   int
	UsersByRole  map[domain.UserRole]int
	BlockedUsers int
	// AI summarises AI provider usage and cost since Since.
	AI domain.AIUsageSummary
	// NotificationsByStatus tallies the notification queue's current state.
	NotificationsByStatus map[domain.NotificationStatus]int
	// AuditEntryCount counts audit entries since Since, capped at
	// auditSummaryScanLimit.
	AuditEntryCount int
}

// AdminUsageService assembles the super-admin usage summary from several
// bounded contexts' own repositories, without any of them depending on each
// other.
type AdminUsageService struct {
	identity      outbound.IdentityRepository
	aiRequests    outbound.AIRequestRepository
	notifications outbound.NotificationRepository
	audit         outbound.AuditRepository
	now           func() time.Time
}

// NewAdminUsageService creates the usage summary service.
func NewAdminUsageService(identity outbound.IdentityRepository, aiRequests outbound.AIRequestRepository, notifications outbound.NotificationRepository, audit outbound.AuditRepository) (*AdminUsageService, error) {
	if identity == nil || aiRequests == nil || notifications == nil || audit == nil {
		return nil, errors.New("identity, AI request, notification, and audit repositories are required")
	}
	return &AdminUsageService{identity: identity, aiRequests: aiRequests, notifications: notifications, audit: audit, now: time.Now}, nil
}

// Summarize returns the usage summary since the given time, defaulting to
// the last 24 hours when since is zero. Restricted to super-administrators.
func (s *AdminUsageService) Summarize(ctx context.Context, actor domain.AppUser, since time.Time) (UsageSummary, error) {
	if !actor.Role.Allows(domain.PermissionAuditRead) {
		return UsageSummary{}, ErrForbidden
	}
	if since.IsZero() {
		since = s.now().AddDate(0, 0, -1)
	}
	users, err := s.identity.ListUsers(ctx)
	if err != nil {
		return UsageSummary{}, err
	}
	summary := UsageSummary{Since: since, TotalUsers: len(users), UsersByRole: map[domain.UserRole]int{}}
	for _, user := range users {
		summary.UsersByRole[user.Role]++
		if user.AccessBlocked {
			summary.BlockedUsers++
		}
	}
	summary.AI, err = s.aiRequests.Summarize(ctx, since)
	if err != nil {
		return UsageSummary{}, err
	}
	summary.NotificationsByStatus, err = s.notifications.CountByStatus(ctx)
	if err != nil {
		return UsageSummary{}, err
	}
	entries, err := s.audit.ListEntries(ctx, outbound.AuditFilter{From: since, Limit: auditSummaryScanLimit})
	if err != nil {
		return UsageSummary{}, err
	}
	summary.AuditEntryCount = len(entries)
	return summary, nil
}
