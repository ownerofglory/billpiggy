// Package email provides infrastructure-independent email delivery helpers.
package email

import (
	"context"
	"fmt"
	"net/smtp"
)

// SMTPSender sends plain-text messages using a configured SMTP relay.
type SMTPSender struct{ address, username, password, from string }

// NewSMTPSender creates an SMTP sender. An empty address leaves delivery disabled.
func NewSMTPSender(address, username, password, from string) (*SMTPSender, error) {
	if address == "" || from == "" {
		return nil, fmt.Errorf("SMTP address and sender are required")
	}
	return &SMTPSender{address: address, username: username, password: password, from: from}, nil
}

// Send delivers a plain-text message unless the context has been cancelled.
func (s *SMTPSender) Send(ctx context.Context, to, subject, body string) error {
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
	message := []byte("To: " + to + "\r\nFrom: " + s.from + "\r\nSubject: " + subject + "\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body)
	return smtp.SendMail(s.address, auth, s.from, []string{to}, message)
}

func splitHost(address string) (string, string, error) {
	for i := len(address) - 1; i >= 0; i-- {
		if address[i] == ':' {
			return address[:i], address[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("SMTP address must include a port")
}
