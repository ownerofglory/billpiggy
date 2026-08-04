package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// AIRequestRepository persists the AI request audit trail in PostgreSQL.
type AIRequestRepository struct{ pool *pgxpool.Pool }

// NewAIRequestRepository creates an AI request audit adapter.
func NewAIRequestRepository(pool *pgxpool.Pool) *AIRequestRepository {
	return &AIRequestRepository{pool: pool}
}

// RecordRequest appends one AI request record.
func (r *AIRequestRepository) RecordRequest(ctx context.Context, record domain.AIRequestRecord) error {
	id := record.ID
	if id == "" {
		id = uuid.NewString()
	}
	errorMessage := record.ErrorMessage
	if _, err := pgxtx.From(ctx, r.pool).Exec(ctx, `
		insert into ai.requests (id, user_id, workload, model, input_tokens, output_tokens, total_tokens, latency_ms, outcome, error_message, created_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, nullif($10, ''), $11)`,
		id, record.UserID, string(record.Workload), record.Model,
		record.Usage.InputTokens, record.Usage.OutputTokens, record.Usage.TotalTokens,
		record.LatencyMS, string(record.Outcome), errorMessage, record.CreatedAt); err != nil {
		return fmt.Errorf("record AI request: %w", err)
	}
	return nil
}

// Summarize aggregates requests by workload since the given time.
func (r *AIRequestRepository) Summarize(ctx context.Context, since time.Time) (domain.AIUsageSummary, error) {
	rows, err := pgxtx.From(ctx, r.pool).Query(ctx, `
		select workload, count(*), count(*) filter (where outcome = 'error'), coalesce(sum(input_tokens), 0), coalesce(sum(output_tokens), 0)
		from ai.requests
		where created_at >= $1
		group by workload
		order by workload`, since)
	if err != nil {
		return domain.AIUsageSummary{}, fmt.Errorf("summarize AI requests: %w", err)
	}
	defer rows.Close()
	summary := domain.AIUsageSummary{Since: since}
	for rows.Next() {
		var workload string
		var usage domain.AIWorkloadUsage
		if err := rows.Scan(&workload, &usage.RequestCount, &usage.ErrorCount, &usage.InputTokens, &usage.OutputTokens); err != nil {
			return domain.AIUsageSummary{}, err
		}
		usage.Workload = domain.AIWorkload(workload)
		summary.ByWorkload = append(summary.ByWorkload, usage)
	}
	return summary, rows.Err()
}
