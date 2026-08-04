package outbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// AudioTranscriber transcribes spoken audio to text.
type AudioTranscriber interface {
	// Transcribe converts request.Audio into text.
	Transcribe(ctx context.Context, request domain.TranscriptionRequest) (domain.Transcription, error)
}
