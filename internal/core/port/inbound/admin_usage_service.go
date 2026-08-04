package inbound

import (
	"context"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

// AdminUsageService is everything an HTTP handler needs from the
// super-admin usage summary.
type AdminUsageService interface {
	Summarize(ctx context.Context, actor domain.AppUser, since time.Time) (service.UsageSummary, error)
}
