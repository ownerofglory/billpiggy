package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/imageproc"
	"github.com/ownerofglory/billpiggy/pkg/ratelimit"
)

// receiptExtractionModel and sentenceExtractionModel are deliberately the
// cheapest vision-capable and text models: intake happens far more often than
// a user asks the assistant a question, so cost per call matters more here.
const (
	receiptExtractionModel  = "gpt-4o-mini"
	sentenceExtractionModel = "gpt-4o-mini"
)

// IntakeRateLimit and IntakeRateInterval bound every AI-assisted entry mode
// together — receipt scan, sentence entry, and dictation all draw from the
// same daily budget, since they are variations on the same "add an expense"
// action rather than independent workloads. Exported so a caller building a
// durable limiter with WithLimiter can match the default exactly.
const (
	IntakeRateLimit    = 30
	IntakeRateInterval = 24 * time.Hour
)

// extractionInstructions frame every intake extraction call.
const extractionInstructions = "Extract a single expense from the provided receipt or description. " +
	"Respond only with the structured fields requested. " +
	"If a total is not stated explicitly, sum the line items. " +
	"If the currency is not stated, use EUR. " +
	"If the date is not stated, use the current date."

// expenseDraftSchema is generated once: reflection is not free, and the
// schema never changes at runtime.
var expenseDraftSchema = domain.GenerateSchema[domain.ExtractedExpense]()

// expenseDraftResponseFormat constrains an extraction call to ExtractedExpense.
func expenseDraftResponseFormat() *domain.ResponseFormat {
	return &domain.ResponseFormat{
		Name:        "extracted_expense",
		Description: "A single expense extracted from the user's receipt or description",
		Schema:      expenseDraftSchema,
		Strict:      true,
	}
}

// ExpenseIntakeService turns a receipt photo, a free-text sentence, or spoken
// audio into a draft expense the user reviews before it is ever persisted.
type ExpenseIntakeService struct {
	provider    outbound.AIProvider
	transcriber outbound.AudioTranscriber
	limit       ratelimit.Limiter
}

// NewExpenseIntakeService creates an intake service. transcriber may be nil,
// which disables ExtractFromAudio while leaving receipt scan and sentence
// entry available. The default limiter is in-memory and process-local; call
// WithLimiter with a durable implementation for multi-replica deployments.
func NewExpenseIntakeService(provider outbound.AIProvider, transcriber outbound.AudioTranscriber) (*ExpenseIntakeService, error) {
	if provider == nil {
		return nil, errors.New("AI provider is required")
	}
	return &ExpenseIntakeService{
		provider:    provider,
		transcriber: transcriber,
		limit:       ratelimit.NewFixedWindow(IntakeRateLimit, IntakeRateInterval),
	}, nil
}

// WithLimiter overrides the default in-memory rate limiter.
func (s *ExpenseIntakeService) WithLimiter(limiter ratelimit.Limiter) *ExpenseIntakeService {
	s.limit = limiter
	return s
}

// ExtractFromReceipt extracts a draft expense from a photographed or scanned
// receipt image. The image is normalised — downscaled, converted to
// grayscale, re-encoded — before it reaches the model, which is also what
// strips any metadata the original carried.
func (s *ExpenseIntakeService) ExtractFromReceipt(ctx context.Context, ownerID string, image []byte) (domain.ExtractedExpense, error) {
	if err := s.checkLimit(ctx, ownerID); err != nil {
		return domain.ExtractedExpense{}, err
	}
	normalized, err := imageproc.Normalize(bytes.NewReader(image), imageproc.ReceiptOptions())
	if err != nil {
		return domain.ExtractedExpense{}, fmt.Errorf("normalize receipt image: %w", err)
	}
	dataURI := "data:" + normalized.ContentType + ";base64," + base64.StdEncoding.EncodeToString(normalized.Data)
	completion, err := s.provider.Complete(ctx, domain.CompletionRequest{
		UserID: ownerID,
		Model:  receiptExtractionModel,
		Messages: []domain.Message{
			domain.SystemMessage(extractionInstructions),
			domain.UserImageMessage("Extract the expense from this receipt.", dataURI),
		},
		ResponseFormat: expenseDraftResponseFormat(),
	})
	if err != nil {
		return domain.ExtractedExpense{}, err
	}
	return decodeExtractedExpense(completion.Content)
}

// ExtractFromSentence extracts a draft expense from a free-text description,
// such as "we spent 25 euro at the cinema, 2 tickets and popcorn".
func (s *ExpenseIntakeService) ExtractFromSentence(ctx context.Context, ownerID, text string) (domain.ExtractedExpense, error) {
	if strings.TrimSpace(text) == "" {
		return domain.ExtractedExpense{}, errors.New("text is required")
	}
	if err := s.checkLimit(ctx, ownerID); err != nil {
		return domain.ExtractedExpense{}, err
	}
	return s.extractFromText(ctx, ownerID, text)
}

// ExtractFromAudio transcribes spoken audio and extracts a draft expense from
// the transcript, returning both. Only one rate-limit unit is charged for the
// whole call even though it makes two provider requests internally: dictation
// is one logical "add an expense" action from the user's perspective.
func (s *ExpenseIntakeService) ExtractFromAudio(ctx context.Context, ownerID string, audio io.Reader, filename, contentType string) (domain.ExtractedExpense, string, error) {
	if s.transcriber == nil {
		return domain.ExtractedExpense{}, "", errors.New("transcription is not configured")
	}
	if err := s.checkLimit(ctx, ownerID); err != nil {
		return domain.ExtractedExpense{}, "", err
	}
	transcription, err := s.transcriber.Transcribe(ctx, domain.TranscriptionRequest{
		UserID: ownerID, Audio: audio, Filename: filename, ContentType: contentType,
	})
	if err != nil {
		return domain.ExtractedExpense{}, "", err
	}
	if strings.TrimSpace(transcription.Text) == "" {
		return domain.ExtractedExpense{}, "", errors.New("transcription produced no text")
	}
	draft, err := s.extractFromText(ctx, ownerID, transcription.Text)
	return draft, transcription.Text, err
}

// checkLimit enforces the shared intake rate limit, namespaced so it can
// never collide with another AI workload's limiter sharing the same store.
func (s *ExpenseIntakeService) checkLimit(ctx context.Context, ownerID string) error {
	if ownerID == "" {
		return ErrForbidden
	}
	allowed, err := s.limit.Allow(ctx, "intake:"+ownerID)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

// extractFromText runs the structured-output completion shared by sentence
// entry and the post-transcription step of dictation. It does not check the
// rate limit itself; callers already have.
func (s *ExpenseIntakeService) extractFromText(ctx context.Context, ownerID, text string) (domain.ExtractedExpense, error) {
	completion, err := s.provider.Complete(ctx, domain.CompletionRequest{
		UserID: ownerID,
		Model:  sentenceExtractionModel,
		Messages: []domain.Message{
			domain.SystemMessage(extractionInstructions),
			domain.UserMessage(text),
		},
		ResponseFormat: expenseDraftResponseFormat(),
	})
	if err != nil {
		return domain.ExtractedExpense{}, err
	}
	return decodeExtractedExpense(completion.Content)
}

// decodeExtractedExpense parses and sanity-checks a structured-output
// completion, filling in the defaults the extraction instructions asked the
// model to use when it left a field unstated.
func decodeExtractedExpense(content string) (domain.ExtractedExpense, error) {
	var draft domain.ExtractedExpense
	if err := json.Unmarshal([]byte(content), &draft); err != nil {
		return domain.ExtractedExpense{}, fmt.Errorf("decode extracted expense: %w", err)
	}
	if strings.TrimSpace(draft.Title) == "" || draft.AmountMinor <= 0 {
		return domain.ExtractedExpense{}, errors.New("extraction did not produce a usable expense")
	}
	if draft.OccurredAt.IsZero() {
		draft.OccurredAt = time.Now().UTC()
	}
	if draft.Currency == "" {
		draft.Currency = "EUR"
	}
	return draft, nil
}
