package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

// draftJSON encodes a valid extracted-expense payload as a provider would
// return it from a structured-output completion.
func draftJSON(t *testing.T, title string, amountMinor int64) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"title": title, "amount_minor": amountMinor, "currency": "EUR",
		"occurred_at": "2026-03-10T12:00:00Z", "category_name": "Entertainment",
	})
	if err != nil {
		t.Fatalf("marshal draft fixture: %v", err)
	}
	return string(encoded)
}

// receiptFixture builds a small valid PNG standing in for a photographed receipt.
func receiptFixture(t *testing.T) []byte {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			source.Set(x, y, color.RGBA{R: uint8(x * 10), G: uint8(y * 10), B: 100, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, source); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return buffer.Bytes()
}

func TestExtractFromReceiptReturnsTheParsedDraft(t *testing.T) {
	t.Parallel()
	provider := memory.NewAIProvider(draftJSON(t, "Cinema", 25_00))
	intake, err := service.NewExpenseIntakeService(provider, nil)
	if err != nil {
		t.Fatalf("build intake service: %v", err)
	}
	draft, err := intake.ExtractFromReceipt(context.Background(), "owner-1", receiptFixture(t))
	if err != nil {
		t.Fatalf("ExtractFromReceipt: %v", err)
	}
	if draft.Title != "Cinema" || draft.AmountMinor != 25_00 || draft.Currency != "EUR" {
		t.Fatalf("unexpected draft %#v", draft)
	}
	if draft.OccurredAt.IsZero() {
		t.Fatal("occurred_at was not parsed")
	}

	// The image sent to the provider must be the normalised JPEG data URI, not
	// the raw PNG bytes: normalisation is also the metadata strip.
	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("provider saw %d requests, want 1", len(requests))
	}
	var imageURL string
	for _, message := range requests[0].Messages {
		if message.ImageURL != nil {
			imageURL = *message.ImageURL
		}
	}
	if !strings.HasPrefix(imageURL, "data:image/jpeg;base64,") {
		t.Fatalf("image URL = %q, want a normalised JPEG data URI", imageURL)
	}
	if requests[0].UserID != "owner-1" {
		t.Fatalf("UserID = %q, want owner-1 for audit purposes", requests[0].UserID)
	}
}

// TestExtractionInstructionsCarryTodaysDateNotAModelGuess is a regression
// test for a real production bug: the extraction instructions used to just
// say "use the current date" with no actual date, and the model — which has
// no clock, only a training cutoff — answered every date-less extraction
// with October 2023 regardless of the real date. The fix spells the date out
// explicitly so there is nothing left for the model to guess.
func TestExtractionInstructionsCarryTodaysDateNotAModelGuess(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 8, 11, 15, 4, 5, 0, time.UTC)
	provider := memory.NewAIProvider(draftJSON(t, "Cinema", 25_00))
	intake, err := service.NewExpenseIntakeService(provider, nil)
	if err != nil {
		t.Fatalf("build intake service: %v", err)
	}
	intake.WithClock(func() time.Time { return fixedNow })

	if _, err := intake.ExtractFromSentence(context.Background(), "owner-1", "cinema tickets, 25 euro"); err != nil {
		t.Fatalf("ExtractFromSentence: %v", err)
	}

	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("provider saw %d requests, want 1", len(requests))
	}
	var systemText string
	for _, message := range requests[0].Messages {
		if message.Role == domain.RoleSystem && message.Text != nil {
			systemText = *message.Text
		}
	}
	if !strings.Contains(systemText, "2026-08-11") {
		t.Fatalf("system instructions = %q, want the injected date 2026-08-11", systemText)
	}
	if strings.Contains(systemText, "2023") {
		t.Fatalf("system instructions = %q, must not mention a stale hardcoded year", systemText)
	}
}

func TestExtractedExpenseDecodesTheMerchantAddress(t *testing.T) {
	t.Parallel()
	encoded, err := json.Marshal(map[string]any{
		"title": "Groceries", "amount_minor": 4200, "currency": "EUR",
		"occurred_at": "2026-03-10T12:00:00Z", "category_name": "Food",
		"address": "12 Market Street, Springfield",
	})
	if err != nil {
		t.Fatalf("marshal draft fixture: %v", err)
	}
	provider := memory.NewAIProvider(string(encoded))
	intake, err := service.NewExpenseIntakeService(provider, nil)
	if err != nil {
		t.Fatalf("build intake service: %v", err)
	}
	draft, err := intake.ExtractFromReceipt(context.Background(), "owner-1", receiptFixture(t))
	if err != nil {
		t.Fatalf("ExtractFromReceipt: %v", err)
	}
	if draft.Address != "12 Market Street, Springfield" {
		t.Fatalf("Address = %q, want the extracted address", draft.Address)
	}
}

func TestExtractFromReceiptRejectsUnusableExtractions(t *testing.T) {
	t.Parallel()
	// A missing title/amount means the model failed to find a real expense;
	// that must surface as an error, not a zero-value draft.
	provider := memory.NewAIProvider(`{"title":"","amount_minor":0}`)
	intake, err := service.NewExpenseIntakeService(provider, nil)
	if err != nil {
		t.Fatalf("build intake service: %v", err)
	}
	if _, err := intake.ExtractFromReceipt(context.Background(), "owner-1", receiptFixture(t)); err == nil {
		t.Fatal("expected an error for an unusable extraction")
	}
}

func TestExtractFromSentenceParsesTheDescription(t *testing.T) {
	t.Parallel()
	provider := memory.NewAIProvider(draftJSON(t, "Cinema", 36_00))
	intake, err := service.NewExpenseIntakeService(provider, nil)
	if err != nil {
		t.Fatalf("build intake service: %v", err)
	}
	draft, err := intake.ExtractFromSentence(context.Background(), "owner-1",
		"we went to the cinema and spent 25 bucks for 2 of us for Toy Story movie and 11 euro for popcorn")
	if err != nil {
		t.Fatalf("ExtractFromSentence: %v", err)
	}
	if draft.Title != "Cinema" || draft.AmountMinor != 36_00 {
		t.Fatalf("unexpected draft %#v", draft)
	}
}

func TestExtractFromSentenceRejectsEmptyText(t *testing.T) {
	t.Parallel()
	intake, err := service.NewExpenseIntakeService(memory.NewAIProvider("unused"), nil)
	if err != nil {
		t.Fatalf("build intake service: %v", err)
	}
	if _, err := intake.ExtractFromSentence(context.Background(), "owner-1", "   "); err == nil {
		t.Fatal("expected empty text to be rejected")
	}
}

func TestExtractFromAudioTranscribesThenExtracts(t *testing.T) {
	t.Parallel()
	transcriber := memory.NewAudioTranscriber("we spent 25 euro at the cinema")
	provider := memory.NewAIProvider(draftJSON(t, "Cinema", 25_00))
	intake, err := service.NewExpenseIntakeService(provider, transcriber)
	if err != nil {
		t.Fatalf("build intake service: %v", err)
	}
	draft, transcript, err := intake.ExtractFromAudio(context.Background(), "owner-1", strings.NewReader("fake-audio"), "note.m4a", "audio/m4a")
	if err != nil {
		t.Fatalf("ExtractFromAudio: %v", err)
	}
	if transcript != "we spent 25 euro at the cinema" {
		t.Fatalf("transcript = %q", transcript)
	}
	if draft.Title != "Cinema" || draft.AmountMinor != 25_00 {
		t.Fatalf("unexpected draft %#v", draft)
	}
	// The transcript, not the raw audio, is what reaches the text-extraction
	// completion.
	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("provider saw %d requests, want 1", len(requests))
	}
	var sawTranscript bool
	for _, message := range requests[0].Messages {
		if message.Text != nil && strings.Contains(*message.Text, "cinema") {
			sawTranscript = true
		}
	}
	if !sawTranscript {
		t.Fatal("the transcript did not reach the extraction completion")
	}
}

func TestExtractFromAudioRequiresATranscriber(t *testing.T) {
	t.Parallel()
	intake, err := service.NewExpenseIntakeService(memory.NewAIProvider("unused"), nil)
	if err != nil {
		t.Fatalf("build intake service: %v", err)
	}
	if _, _, err := intake.ExtractFromAudio(context.Background(), "owner-1", strings.NewReader("audio"), "a.m4a", "audio/m4a"); err == nil {
		t.Fatal("expected an error without a configured transcriber")
	}
}

func TestExtractFromAudioSurfacesTranscriptionFailures(t *testing.T) {
	t.Parallel()
	failure := errors.New("transcription unavailable")
	transcriber := memory.NewAudioTranscriber("").WithError(failure)
	intake, err := service.NewExpenseIntakeService(memory.NewAIProvider("unused"), transcriber)
	if err != nil {
		t.Fatalf("build intake service: %v", err)
	}
	if _, _, err := intake.ExtractFromAudio(context.Background(), "owner-1", strings.NewReader("audio"), "a.m4a", "audio/m4a"); !errors.Is(err, failure) {
		t.Fatalf("ExtractFromAudio returned %v, want the transcription failure", err)
	}
}

func TestIntakeSharesOneRateLimitAcrossModes(t *testing.T) {
	t.Parallel()
	provider := memory.NewAIProvider(draftJSON(t, "Cinema", 25_00))
	intake, err := service.NewExpenseIntakeService(provider, nil)
	if err != nil {
		t.Fatalf("build intake service: %v", err)
	}
	// Exhaust the shared daily budget through sentence entry...
	for i := 0; i < service.IntakeRateLimit; i++ {
		if _, err := intake.ExtractFromSentence(context.Background(), "owner-1", "cinema 25 euro"); err != nil {
			t.Fatalf("request %d rejected early: %v", i+1, err)
		}
	}
	// ...and confirm receipt scan, a different mode, is blocked by the same budget.
	if _, err := intake.ExtractFromReceipt(context.Background(), "owner-1", receiptFixture(t)); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("ExtractFromReceipt returned %v, want ErrForbidden once the shared budget is spent", err)
	}
}

func TestIntakeRateLimitIsPerOwner(t *testing.T) {
	t.Parallel()
	provider := memory.NewAIProvider(draftJSON(t, "Cinema", 25_00))
	intake, err := service.NewExpenseIntakeService(provider, nil)
	if err != nil {
		t.Fatalf("build intake service: %v", err)
	}
	for i := 0; i < service.IntakeRateLimit; i++ {
		if _, err := intake.ExtractFromSentence(context.Background(), "owner-1", "cinema 25 euro"); err != nil {
			t.Fatalf("request %d rejected early: %v", i+1, err)
		}
	}
	if _, err := intake.ExtractFromSentence(context.Background(), "owner-2", "cinema 25 euro"); err != nil {
		t.Fatalf("a second owner was rate-limited by the first: %v", err)
	}
}
