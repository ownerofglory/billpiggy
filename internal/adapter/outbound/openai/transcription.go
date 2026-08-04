package openai

import (
	"context"
	"errors"
	"fmt"

	"github.com/openai/openai-go/v2"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// DefaultTranscriptionModel is used when a transcription request does not
// name a model.
const DefaultTranscriptionModel = openai.AudioModelGPT4oMiniTranscribe

// Transcribe converts spoken audio to text.
func (c *Client) Transcribe(ctx context.Context, request domain.TranscriptionRequest) (domain.Transcription, error) {
	if request.Audio == nil {
		return domain.Transcription{}, errors.New("transcription request has no audio")
	}
	model := request.Model
	if model == "" {
		model = DefaultTranscriptionModel
	}
	filename := request.Filename
	if filename == "" {
		filename = "audio"
	}
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
