// Package email provides infrastructure-independent email delivery helpers.
package email

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/smtp"
	"net/textproto"
	"strings"
)

// SMTPSender sends multipart/alternative messages using a configured SMTP relay.
type SMTPSender struct{ address, username, password, from string }

// NewSMTPSender creates an SMTP sender. An empty address leaves delivery disabled.
func NewSMTPSender(address, username, password, from string) (*SMTPSender, error) {
	if address == "" || from == "" {
		return nil, fmt.Errorf("SMTP address and sender are required")
	}
	return &SMTPSender{address: address, username: username, password: password, from: from}, nil
}

// Send delivers a message carrying both a plain-text and an HTML part,
// unless the context has been cancelled.
func (s *SMTPSender) Send(ctx context.Context, to, subject, text, html string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	host, _, err := splitHost(s.address)
	if err != nil {
		return err
	}
	var auth smtp.Auth
	if s.username != "" {
		auth = smtp.PlainAuth("", s.username, s.password, host)
	}
	message, err := buildMessage(s.from, to, subject, text, html)
	if err != nil {
		return fmt.Errorf("build message: %w", err)
	}
	return smtp.SendMail(s.address, auth, s.from, []string{to}, message)
}

// buildMessage renders a multipart/alternative RFC 5322 message with a
// plain-text part first, then HTML, so a client that can't render HTML falls
// back to the text part it understands.
func buildMessage(from, to, subject, text, html string) ([]byte, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	textPart, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/plain; charset=utf-8"}})
	if err != nil {
		return nil, err
	}
	if _, err := textPart.Write([]byte(text)); err != nil {
		return nil, err
	}
	htmlPart, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/html; charset=utf-8"}})
	if err != nil {
		return nil, err
	}
	if _, err := htmlPart.Write([]byte(html)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	var message bytes.Buffer
	message.WriteString("To: " + sanitizeHeaderValue(to) + "\r\n")
	message.WriteString("From: " + sanitizeHeaderValue(from) + "\r\n")
	message.WriteString("Subject: " + sanitizeHeaderValue(subject) + "\r\n")
	message.WriteString("MIME-Version: 1.0\r\n")
	message.WriteString("Content-Type: multipart/alternative; boundary=" + writer.Boundary() + "\r\n")
	message.WriteString("\r\n")
	message.Write(body.Bytes())
	return message.Bytes(), nil
}

// sanitizeHeaderValue strips CR and LF from a value bound for a message
// header. Templated subjects can embed user-controlled text such as a budget
// name; without this, a value containing "\r\n" could inject additional
// headers (e.g. a forged Bcc) into the outgoing message.
func sanitizeHeaderValue(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	return strings.ReplaceAll(value, "\n", "")
}

func splitHost(address string) (string, string, error) {
	for i := len(address) - 1; i >= 0; i-- {
		if address[i] == ':' {
			return address[:i], address[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("SMTP address must include a port")
}
