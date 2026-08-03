package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/outbox"
)

// AnalyticsProjectionName is the durable subscription name for expense rollups.
// Changing it replays the entire stream, so it must stay stable.
const AnalyticsProjectionName = "analytics_rollups"

// AnalyticsProjection maintains category and tag rollups from expense events.
type AnalyticsProjection struct {
	repository outbound.AnalyticsRepository
}

// NewAnalyticsProjection creates the analytics outbox subscription.
func NewAnalyticsProjection(repository outbound.AnalyticsRepository) (*AnalyticsProjection, error) {
	if repository == nil {
		return nil, errors.New("analytics repository is required")
	}
	return &AnalyticsProjection{repository: repository}, nil
}

// Name returns the durable subscription name.
func (p *AnalyticsProjection) Name() string { return AnalyticsProjectionName }

// AggregateTypes limits the subscription to expense events.
func (p *AnalyticsProjection) AggregateTypes() []string { return []string{"expense"} }

// Handle reverses the previously applied contribution and applies the new one.
//
// Decoding happens first and an event that carries no contribution returns
// before anything is read or reversed. The previous implementation reversed the
// old contribution and only then discovered it did not recognise the event
// type, silently leaving the expense un-counted.
func (p *AnalyticsProjection) Handle(ctx context.Context, message outbox.Message) error {
	next, applies, err := domain.DecodeExpenseContribution(message.EventType, message.Payload)
	if err != nil {
		return err
	}
	if !applies {
		return nil
	}
	previous, found, err := p.repository.LoadContribution(ctx, next.ExpenseID)
	if err != nil {
		return err
	}
	if found && previous.Active {
		if err := p.adjust(ctx, previous, -1); err != nil {
			return fmt.Errorf("reverse contribution %s: %w", next.ExpenseID, err)
		}
	}
	if !next.Active {
		// Removal keeps a deactivated tombstone rather than deleting the row,
		// so a redelivered removal cannot reverse the same amounts twice.
		if !found {
			return nil
		}
		previous.Active = false
		return p.repository.SaveContribution(ctx, previous)
	}
	if err := validateContribution(next); err != nil {
		return err
	}
	if err := p.adjust(ctx, next, 1); err != nil {
		return fmt.Errorf("apply contribution %s: %w", next.ExpenseID, err)
	}
	return p.repository.SaveContribution(ctx, next)
}

// adjust applies a contribution to every rollup bucket it touches, with
// direction -1 to reverse and +1 to apply.
func (p *AnalyticsProjection) adjust(ctx context.Context, contribution domain.ExpenseContribution, direction int64) error {
	for _, period := range domain.RollupPeriods() {
		start := domain.RollupPeriodStart(period, contribution.OccurredAt)
		delta := domain.RollupDelta{
			OwnerID:      contribution.OwnerID,
			CategoryID:   contribution.CategoryID,
			Currency:     contribution.Currency,
			Period:       period,
			PeriodStart:  start,
			AmountMinor:  contribution.AmountMinor * direction,
			ExpenseCount: direction,
		}
		if err := p.repository.AddRollupDelta(ctx, delta); err != nil {
			return err
		}
		for _, tagID := range contribution.TagIDs {
			tagDelta := delta
			tagDelta.CategoryID, tagDelta.TagID = "", tagID
			if err := p.repository.AddRollupDelta(ctx, tagDelta); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateContribution rejects an expense that cannot be bucketed.
//
// A rejection is no longer permanent poison: the message backs off, retries,
// and dead-letters with its cause recorded, blocking only that one expense.
func validateContribution(contribution domain.ExpenseContribution) error {
	switch {
	case contribution.OwnerID == "":
		return fmt.Errorf("expense %s has no owner", contribution.ExpenseID)
	case contribution.CategoryID == "":
		return fmt.Errorf("expense %s has no category", contribution.ExpenseID)
	case contribution.Currency == "":
		return fmt.Errorf("expense %s has no currency", contribution.ExpenseID)
	case contribution.OccurredAt.IsZero():
		return fmt.Errorf("expense %s has no occurrence time", contribution.ExpenseID)
	}
	return nil
}
