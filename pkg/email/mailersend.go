package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// mailerSendEndpoint is MailerSend's transactional email API.
const mailerSendEndpoint = "https://api.mailersend.com/v1/email"

// mailerSendTimeout bounds one API call so an unresponsive upstream can never
// block the notification worker indefinitely.
const mailerSendTimeout = 15 * time.Second

// MailerSendSender sends email through MailerSend's transactional email API.
type MailerSendSender struct {
	apiKey     string
	fromEmail  string
	fromName   string
	endpoint   string
	httpClient *http.Client
}

// NewMailerSendSender creates a MailerSend-backed sender. fromName is
// optional; MailerSend accepts a from address with no display name.
func NewMailerSendSender(apiKey, fromEmail, fromName string) (*MailerSendSender, error) {
	if apiKey == "" || fromEmail == "" {
		return nil, fmt.Errorf("MailerSend API key and sender address are required")
	}
	return &MailerSendSender{
		apiKey:     apiKey,
		fromEmail:  fromEmail,
		fromName:   fromName,
		endpoint:   mailerSendEndpoint,
		httpClient: &http.Client{Timeout: mailerSendTimeout},
	}, nil
}

// mailerSendAddress is the {email, name} shape MailerSend uses for both the
// sender and every recipient.
type mailerSendAddress struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// mailerSendRequest is the body of a POST /v1/email call.
type mailerSendRequest struct {
	From    mailerSendAddress   `json:"from"`
	To      []mailerSendAddress `json:"to"`
	Subject string              `json:"subject"`
	Text    string              `json:"text"`
	HTML    string              `json:"html"`
}

// mailerSendErrorResponse is the documented shape of a non-2xx response body.
type mailerSendErrorResponse struct {
	Message string              `json:"message"`
	Errors  map[string][]string `json:"errors"`
}

// Send delivers a message carrying both a plain-text and an HTML part.
func (s *MailerSendSender) Send(ctx context.Context, to, subject, text, html string) error {
	body, err := json.Marshal(mailerSendRequest{
		From:    mailerSendAddress{Email: s.fromEmail, Name: s.fromName},
		To:      []mailerSendAddress{{Email: to}},
		Subject: subject,
		Text:    text,
		HTML:    html,
	})
	if err != nil {
		return fmt.Errorf("build mailersend request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build mailersend request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+s.apiKey)

	response, err := s.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("mailersend request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return nil
	}
	return fmt.Errorf("mailersend: %w", parseMailerSendError(response))
}

// parseMailerSendError turns a non-2xx MailerSend response into a readable
// error, falling back to the raw body when it isn't the documented JSON
// error shape (e.g. an upstream proxy error page).
func parseMailerSendError(response *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	var parsed mailerSendErrorResponse
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.Message != "" {
		return fmt.Errorf("%s (status %d): %v", parsed.Message, response.StatusCode, parsed.Errors)
	}
	return fmt.Errorf("status %d: %s", response.StatusCode, string(raw))
}
