package email

import (
	"context"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

func TestBuildMessageProducesParsableMultipartAlternative(t *testing.T) {
	t.Parallel()
	raw, err := buildMessage("billpiggy@example.com", "user@example.com", "Hello", "plain body", "<p>html body</p>")
	if err != nil {
		t.Fatalf("build message: %v", err)
	}
	parsed, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	if got := parsed.Header.Get("Subject"); got != "Hello" {
		t.Fatalf("subject = %q, want %q", got, "Hello")
	}
	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("content-type = %q, err = %v", parsed.Header.Get("Content-Type"), err)
	}
	reader := multipart.NewReader(parsed.Body, params["boundary"])
	var textFound, htmlFound bool
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		body := make([]byte, 256)
		n, _ := part.Read(body)
		content := string(body[:n])
		switch part.Header.Get("Content-Type") {
		case "text/plain; charset=utf-8":
			textFound = content == "plain body"
		case "text/html; charset=utf-8":
			htmlFound = content == "<p>html body</p>"
		}
	}
	if !textFound || !htmlFound {
		t.Fatalf("expected both text and html parts present and intact, text=%v html=%v", textFound, htmlFound)
	}
}

func TestBuildMessageStripsCRLFFromHeaderValues(t *testing.T) {
	t.Parallel()
	maliciousSubject := "Hello\r\nBcc: attacker@evil.com\r\nX-Injected: true"
	raw, err := buildMessage("billpiggy@example.com", "user@example.com", maliciousSubject, "body", "<p>body</p>")
	if err != nil {
		t.Fatalf("build message: %v", err)
	}
	// The injected text survives as inert content folded into the Subject
	// value (CR/LF stripped, not header syntax removed) — what matters is
	// that it never becomes a second, real header of its own.
	parsed, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	if parsed.Header.Get("Bcc") != "" || parsed.Header.Get("X-Injected") != "" {
		t.Fatalf("injected header parsed as a real header: %#v", parsed.Header)
	}
	if strings.Contains(parsed.Header.Get("Subject"), "\r") || strings.Contains(parsed.Header.Get("Subject"), "\n") {
		t.Fatalf("subject still contains a line break: %q", parsed.Header.Get("Subject"))
	}
}

func TestSMTPSenderRejectsCancelledContext(t *testing.T) {
	t.Parallel()
	sender, err := NewSMTPSender("localhost:2525", "", "", "billpiggy@example.com")
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sender.Send(ctx, "user@example.com", "subject", "text", "html"); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}
