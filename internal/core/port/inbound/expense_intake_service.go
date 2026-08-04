package inbound

import (
	"context"
	"io"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// ExpenseIntakeService is everything an HTTP handler needs from the AI
// expense-intake modes (receipt scan, sentence entry, dictation). WithLimiter
// is wiring-only and excluded: only cmd/billpiggy calls it, at startup.
type ExpenseIntakeService interface {
	ExtractFromReceipt(ctx context.Context, ownerID string, image []byte) (domain.ExtractedExpense, error)
	ExtractFromSentence(ctx context.Context, ownerID, text string) (domain.ExtractedExpense, error)
	ExtractFromAudio(ctx context.Context, ownerID string, audio io.Reader, filename, contentType string) (domain.ExtractedExpense, string, error)
}
