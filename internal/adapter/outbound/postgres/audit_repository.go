package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// AuditRepository appends and queries the immutable audit trail.
type AuditRepository struct{ pool *pgxpool.Pool }

// NewAuditRepository creates an audit adapter.
func NewAuditRepository(pool *pgxpool.Pool) *AuditRepository { return &AuditRepository{pool: pool} }

// AppendEntry records one entry. Entries carrying a source event are unique on
// it, so redelivering an event or replaying the stream never duplicates them.
func (r *AuditRepository) AppendEntry(ctx context.Context, entry domain.AuditEntry) error {
	metadata, err := json.Marshal(entry.Metadata)
	if err != nil {
		return fmt.Errorf("marshal audit metadata: %w", err)
	}
	if _, err := pgxtx.From(ctx, r.pool).Exec(ctx, `
		insert into audit.entries (id, event_id, actor_id, action, resource_type, resource_id, metadata, occurred_at)
		values (gen_random_uuid(), nullif($1, '')::uuid, nullif($2, '')::uuid, $3, $4, nullif($5, '')::uuid, $6, $7)
		on conflict (event_id) where event_id is not null do nothing`,
		entry.EventID, entry.ActorID, entry.Action, entry.ResourceType, entry.ResourceID, metadata, entry.OccurredAt); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}
	return nil
}

// ListEntries returns audit entries matching the filter, newest first.
func (r *AuditRepository) ListEntries(ctx context.Context, filter outbound.AuditFilter) ([]domain.AuditEntry, error) {
	query := `select coalesce(event_id::text, ''), coalesce(actor_id::text, ''), action, resource_type, coalesce(resource_id::text, ''), metadata, occurred_at from audit.entries where true`
	args := []any{}
	appendCondition := func(condition string, value any) {
		args = append(args, value)
		query += fmt.Sprintf(condition, len(args))
	}
	if filter.ActorID != "" {
		appendCondition(" and actor_id = $%d", filter.ActorID)
	}
	if filter.ResourceType != "" {
		appendCondition(" and resource_type = $%d", filter.ResourceType)
	}
	if filter.ResourceID != "" {
		appendCondition(" and resource_id = $%d", filter.ResourceID)
	}
	if filter.Action != "" {
		appendCondition(" and action = $%d", filter.Action)
	}
	if !filter.From.IsZero() {
		appendCondition(" and occurred_at >= $%d", filter.From)
	}
	if !filter.To.IsZero() {
		appendCondition(" and occurred_at <= $%d", filter.To)
	}
	args = append(args, filter.Limit, filter.Offset)
	query += fmt.Sprintf(" order by occurred_at desc limit $%d offset $%d", len(args)-1, len(args))

	rows, err := pgxtx.From(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit entries: %w", err)
	}
	defer rows.Close()
	entries := make([]domain.AuditEntry, 0)
	for rows.Next() {
		var entry domain.AuditEntry
		var metadata []byte
		if err := rows.Scan(&entry.EventID, &entry.ActorID, &entry.Action, &entry.ResourceType, &entry.ResourceID, &metadata, &entry.OccurredAt); err != nil {
			return nil, err
		}
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &entry.Metadata); err != nil {
				return nil, fmt.Errorf("decode audit metadata: %w", err)
			}
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
