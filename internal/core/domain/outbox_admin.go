package domain

import "time"

// DeadLetter is an outbox message abandoned after exhausting its delivery
// attempts, together with why it was abandoned and what it is holding up.
//
// The engine's own Message type deliberately carries only what a handler needs
// to apply an event. Diagnosing a stalled projection needs the opposite: the
// failure cause, when it was given up on, and the blast radius.
type DeadLetter struct {
	// OutboxID identifies the delivery row, and is what Requeue takes.
	OutboxID string `json:"outboxId"`
	// Subscription is the projection whose delivery failed.
	Subscription string `json:"subscription"`
	// EventID and EventType identify the source domain event.
	EventID   string `json:"eventId"`
	EventType string `json:"eventType"`
	// AggregateType and AggregateID identify what the event happened to.
	// Every later event for this same aggregate is blocked until this
	// delivery is resolved.
	AggregateType string `json:"aggregateType"`
	AggregateID   string `json:"aggregateId"`
	// Attempts counts the deliveries made before the message was abandoned.
	Attempts int `json:"attempts"`
	// LastError is the handler failure that caused the final attempt to fail.
	// This is the field that explains a stalled projection.
	LastError string `json:"lastError"`
	// OccurredAt is when the command produced the event.
	OccurredAt time.Time `json:"occurredAt"`
	// DeadLetteredAt is when the message was abandoned.
	DeadLetteredAt time.Time `json:"deadLetteredAt"`
	// BlockedCount is how many later messages for the same aggregate this
	// dead letter is holding up. They can never be delivered while it stays
	// dead, so this is the cost of leaving it unresolved.
	BlockedCount int `json:"blockedCount"`
}
