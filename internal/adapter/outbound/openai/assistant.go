// Package openai provides outbound adapters for the OpenAI Responses API.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Assistant is a small Responses API client for BillPiggy's scoped assistant.
type Assistant struct {
	apiKey, model string
	client        *http.Client
}

// NewAssistant creates a Responses API client.
func NewAssistant(apiKey, model string) (*Assistant, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}
	if model == "" {
		model = "gpt-5.6-luna"
	}
	return &Assistant{apiKey: apiKey, model: model, client: http.DefaultClient}, nil
}

// Respond sends one scoped prompt and returns the model's text output.
func (a *Assistant) Respond(ctx context.Context, instructions, input string) (string, error) {
	payload := map[string]any{"model": a.model, "instructions": instructions, "input": input, "store": false, "reasoning": map[string]string{"effort": "low"}, "max_output_tokens": 800}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/responses", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+a.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("OpenAI returned %s", response.Status)
	}
	var decoded struct {
		OutputText string `json:"output_text"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return "", err
	}
	if decoded.OutputText == "" {
		return "", fmt.Errorf("OpenAI returned no text")
	}
	return decoded.OutputText, nil
}
