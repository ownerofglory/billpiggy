// Package email provides infrastructure-independent email delivery helpers.
package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

// sendTimeout bounds one entire SMTP conversation, from connect through
// quit. net/smtp.SendMail has no timeout or context support of its own: an
// unreachable or non-responding host otherwise blocks the caller
// indefinitely, which for the notification worker means its "last
// successful pass" marker freezes and /readyz eventually fails with no way
// to recover on its own, since nothing ever unblocks the stuck goroutine.
//
// A var, not a const, so a test can shorten it rather than waiting out the
// real production timeout to prove a non-responding server doesn't hang.
var sendTimeout = 15 * time.Second

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
// unless the context has been cancelled. The whole conversation is bounded
// by sendTimeout regardless of ctx, since the underlying net/smtp dial and
// protocol exchange do not honor context cancellation themselves.
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
	return sendWithDeadline(s.address, host, auth, s.from, to, message)
}

// sendWithDeadline mirrors the steps net/smtp.SendMail performs internally
// (hello, optional STARTTLS, optional auth, mail/rcpt/data/quit), but dials
// with a timeout and puts a single deadline on the connection covering the
// whole exchange, which SendMail itself offers no way to do.
func sendWithDeadline(addr, host string, auth smtp.Auth, from, to string, message []byte) error {
	conn, err := net.DialTimeout("tcp", addr, sendTimeout)
	if err != nil {
		return fmt.Errorf("dial smtp: %w", err)
	}
	if err := conn.SetDeadline(time.Now().Add(sendTimeout)); err != nil {
		conn.Close()
		return fmt.Errorf("set smtp deadline: %w", err)
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer client.Close()
	if err := client.Hello("localhost"); err != nil {
		return fmt.Errorf("smtp hello: %w", err)
	}
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if auth != nil {
		if ok, _ := client.Extension("AUTH"); !ok {
			return fmt.Errorf("smtp: server doesn't support AUTH")
		}
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(message); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	return client.Quit()
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
