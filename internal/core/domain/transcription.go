package domain

import "io"

// TranscriptionRequest is one call to transcribe spoken audio to text.
type TranscriptionRequest struct {
	// UserID identifies who the request is made on behalf of, for audit.
	UserID string
	// Model overrides the provider's configured default when set.
	Model string
	// Audio is the recording to transcribe.
	Audio io.Reader
	// Filename hints the audio format to the provider.
	Filename string
	// ContentType is the audio's media type.
	ContentType string
}

// Transcription is the text a provider produced from spoken audio.
type Transcription struct {
	// Text is the transcribed text.
	Text string
	// Usage reports token consumption when the provider reported it.
	Usage TokenUsage
}
