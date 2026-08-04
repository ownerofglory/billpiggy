package emailtmpl_test

import (
	"strings"
	"testing"

	"github.com/ownerofglory/billpiggy/pkg/emailtmpl"
)

func TestRenderEveryRegisteredKind(t *testing.T) {
	t.Parallel()
	cases := map[string]map[string]string{
		"invitation":     {"role": "member", "accept_url": "https://app.example.com/accept?token=abc", "token": "abc", "expires_at": "2026-08-12T00:00:00Z"},
		"budget_alert":   {"budget_name": "Groceries", "currency": "EUR", "spent_minor": "12000", "limit_minor": "10000", "percent_used": "120", "exceeded": "true", "period_start": "2026-08-01T00:00:00Z"},
		"report_ready":   {"period_kind": "week", "period_start": "2026-07-27T00:00:00Z"},
		"access_changed": {"role": "admin", "blocked": "false"},
	}
	for kind, payload := range cases {
		t.Run(kind, func(t *testing.T) {
			rendered, err := emailtmpl.Render(kind, payload)
			if err != nil {
				t.Fatalf("render %s: %v", kind, err)
			}
			if rendered.Subject == "" || rendered.Text == "" || rendered.HTML == "" {
				t.Fatalf("render %s produced an empty part: %#v", kind, rendered)
			}
		})
	}
}

func TestRenderMissingPayloadKeysAreEmptyNotErrors(t *testing.T) {
	t.Parallel()
	rendered, err := emailtmpl.Render("invitation", nil)
	if err != nil {
		t.Fatalf("render with nil payload: %v", err)
	}
	if strings.Contains(rendered.Text, "<no value>") || strings.Contains(rendered.HTML, "<no value>") {
		t.Fatalf("missing keys leaked template error text: %#v", rendered)
	}
}

func TestRenderUnknownKind(t *testing.T) {
	t.Parallel()
	if _, err := emailtmpl.Render("no-such-kind", nil); err == nil {
		t.Fatal("expected an error for an unregistered kind")
	}
}

func TestRenderEscapesHTMLPayloadValues(t *testing.T) {
	t.Parallel()
	rendered, err := emailtmpl.Render("access_changed", map[string]string{"role": `<script>alert(1)</script>`, "blocked": "false"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(rendered.HTML, "<script>") {
		t.Fatalf("html body did not escape payload value: %s", rendered.HTML)
	}
}
