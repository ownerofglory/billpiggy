package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/outbox"
)

// AuditProjectionName is the durable subscription name for the audit trail.
const AuditProjectionName = "audit_trail"

// AuditProjection records every domain event as an immutable audit entry.
type AuditProjection struct {
	repository outbound.AuditRepository
}

// NewAuditProjection creates the audit outbox subscription.
func NewAuditProjection(repository outbound.AuditRepository) (*AuditProjection, error) {
	if repository == nil {
		return nil, errors.New("audit repository is required")
	}
	return &AuditProjection{repository: repository}, nil
}

// Name returns the durable subscription name.
func (p *AuditProjection) Name() string { return AuditProjectionName }

// AggregateTypes subscribes to every aggregate type, so a new aggregate is
// audited without changing this projection.
func (p *AuditProjection) AggregateTypes() []string { return nil }

// Handle writes one audit entry per event. The entry is keyed on the source
// event, so redelivery and replay are both idempotent.
func (p *AuditProjection) Handle(ctx context.Context, message outbox.Message) error {
	entry := domain.AuditEntry{
		EventID:      message.EventID,
		ActorID:      actorFromMetadata(message.Metadata),
		Action:       message.EventType,
		ResourceType: message.AggregateType,
		ResourceID:   message.AggregateID,
		OccurredAt:   message.OccurredAt,
		Metadata: map[string]string{
			"aggregate_version": strconv.FormatInt(message.AggregateVersion, 10),
			"global_seq":        strconv.FormatInt(message.GlobalSeq, 10),
		},
	}
	return p.repository.AppendEntry(ctx, entry)
}

// actorFromMetadata reads actor_id out of an event envelope, tolerating
// metadata that is absent or malformed rather than failing the delivery.
func actorFromMetadata(metadata []byte) string {
	if len(metadata) == 0 {
		return ""
	}
	var envelope struct {
		ActorID string `json:"actor_id"`
	}
	if err := json.Unmarshal(metadata, &envelope); err != nil {
		return ""
	}
	return envelope.ActorID
}
