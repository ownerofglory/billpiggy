package openai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v2"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// DefaultTranscriptionModel is used when a transcription request does not
// name a model.
const DefaultTranscriptionModel = openai.AudioModelGPT4oMiniTranscribe

// extensionByContentType maps the audio MIME types browser MediaRecorder
// implementations actually produce to the file extension OpenAI's
// transcription endpoint needs to detect the codec. OpenAI infers the format
// from the filename's extension, not the multipart part's Content-Type
// header, so a client-supplied filename whose extension doesn't match the
// real content — e.g. Safari on iOS records audio/mp4, but a cross-browser
// frontend that names every upload "recording.webm" or leaves the blob
// unnamed — gets rejected as "corrupted or unsupported" even though the
// bytes are perfectly valid.
var extensionByContentType = map[string]string{
	"audio/mp4":   ".mp4",
	"audio/x-m4a": ".m4a",
	"audio/m4a":   ".m4a",
	"audio/mpeg":  ".mp3",
	"audio/mp3":   ".mp3",
	"audio/wav":   ".wav",
	"audio/x-wav": ".wav",
	"audio/webm":  ".webm",
	"audio/ogg":   ".ogg",
	"audio/flac":  ".flac",
}

// transcriptionFilename returns the filename to present to OpenAI, replacing
// its extension with the one the reported content type actually needs. The
// content type is what the browser set on the blob it recorded, so it is the
// more trustworthy signal; the filename is often client-generated and, for
// recorded audio in particular, easy to get wrong or leave off entirely.
func transcriptionFilename(filename, contentType string) string {
	base := filename
	if base == "" {
		base = "audio"
	}
	mediaType, _, _ := strings.Cut(contentType, ";")
	ext, ok := extensionByContentType[strings.ToLower(strings.TrimSpace(mediaType))]
	if !ok {
		return base
	}
	if dot := strings.LastIndexByte(base, '.'); dot > 0 {
		base = base[:dot]
	}
	return base + ext
}

// Transcribe converts spoken audio to text.
func (c *Client) Transcribe(ctx context.Context, request domain.TranscriptionRequest) (domain.Transcription, error) {
	if request.Audio == nil {
		return domain.Transcription{}, errors.New("transcription request has no audio")
	}
	model := request.Model
	if model == "" {
		model = DefaultTranscriptionModel
	}
	filename := transcriptionFilename(request.Filename, request.ContentType)
	result, err := c.client.Audio.Transcriptions.New(ctx, openai.AudioTranscriptionNewParams{
		File:  openai.File(request.Audio, filename, request.ContentType),
		Model: model,
	})
	if err != nil {
		c.logger.Error("openai transcription failed", "model", model, "error", err)
		return domain.Transcription{}, fmt.Errorf("openai transcription: %w", err)
	}
	return domain.Transcription{
		Text: result.Text,
		Usage: domain.TokenUsage{
			InputTokens:  result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens,
			TotalTokens:  result.Usage.TotalTokens,
		},
	}, nil
}
