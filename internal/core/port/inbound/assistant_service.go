package inbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// AssistantService is everything an HTTP handler needs from the AI
// assistant chat. WithModel and WithLimiter are wiring-only and excluded:
// only cmd/billpiggy calls them, at startup.
type AssistantService interface {
	AskStream(ctx context.Context, ownerID, message string) (<-chan domain.CompletionChunk, error)
}
