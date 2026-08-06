package emailtmpl

// Each kind supplies a subject (plain text/template) and a body rendered
// twice: once through text/template for the plain-text part, once through
// html/template for the HTML part, so both escape correctly for their own
// output. Missing payload keys render as empty strings rather than erroring,
// since map indexing in both template packages has no concept of a required
// field.
//
// HTML bodies are wrapped in BillPiggy's "Obsidian Vault" brand shell
// (emailShell) — a dark header band matching the app's sidebar, a light
// content card by default with a `prefers-color-scheme: dark` variant for
// clients that support it, and an accent badge colored per DESIGN.md's
// stated palette meaning: blue for account/identity actions, coral for
// spending alerts, emerald for positive/informational status. Styling is a
// mix of a <head><style> block (for clients like Apple Mail, Gmail, and
// Outlook.com that support it) and inline styles as a safe fallback, since
// HTML email clients vary wildly in CSS support.

var subjects = map[string]string{
	"invitation":     "You're invited to BillPiggy",
	"budget_alert":   "Budget alert: {{.budget_name}}",
	"report_ready":   "Your {{.period_kind}} report is ready",
	"access_changed": "Your BillPiggy account access has changed",
	"payment_due":    "{{if eq .reminder \"true\"}}Upcoming payment{{else}}Payment due{{end}}: {{.payment_title}}",
	"password_reset": "Reset your BillPiggy password",
}

// ---------------------------------------------------------------------------
// Brand tokens (mirrors the frontend's src/styles/index.css custom
// properties — kept as plain hex/rgba here since email clients don't
// reliably support CSS custom properties).
// ---------------------------------------------------------------------------

const (
	colorPageBg     = "#f4f4f5"
	colorPageBgDark = "#0e0e0e"
	colorCardBg     = "#ffffff"
	colorCardBgDark = "#1a1a1a"
	colorHeaderBg   = "#121212" // obsidian header band — constant across light/dark
	colorBorder     = "rgba(24,24,27,0.08)"
	colorBorderDark = "rgba(255,255,255,0.08)"
	colorDivider    = "rgba(140,140,140,0.25)" // hairline that reads fine on either background

	colorInk          = "#18181b" // primary text, light mode
	colorInkDark      = "#f4f4f5" // primary text, dark mode
	colorInkMuted     = "#52525b" // secondary text, light mode
	colorInkMutedDark = "#9ca3af" // secondary text, dark mode — matches --color-on-surface-variant

	// Primary (Electric Blue) — account/identity actions.
	colorBlue     = "#4a90e2"
	colorBlueInk  = "#2f6fb8" // darker for AA-legible text-on-white
	colorBlueTint = "rgba(74,144,226,0.12)"

	// Secondary (Coral/Salmon) — spending alerts, soft warnings.
	colorCoral     = "#f58a7a"
	colorCoralInk  = "#c2503c"
	colorCoralTint = "rgba(245,138,122,0.14)"

	// Tertiary (Emerald) — savings, positive/informational status.
	colorEmerald     = "#82ca9d"
	colorEmeraldInk  = "#1f7a4d"
	colorEmeraldTint = "rgba(130,202,157,0.14)"

	// Error — over-limit emphasis within body copy (badge color stays coral;
	// this is just for the specific "exceeded" phrase).
	colorErrorInk = "#b3261e"

	fontStack = "'Manrope',-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif"

	// iconBase64 is the same "savings" icon the app itself uses next to the
	// BillPiggy wordmark (Material Symbols Outlined, coral #f58a7a, matching
	// Layout.tsx's sidebar mark), rasterized to a transparent PNG since email
	// clients can't load icon fonts. Inlined as a data URI so the header logo
	// renders with zero external requests — email clients block remote images
	// by default until the user clicks "show images," and a data URI sidesteps
	// that entirely.
	iconBase64 = "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"
)

// emailShell wraps a body HTML fragment in the shared BillPiggy email
// layout: preheader, dark brand header, accent badge, light/dark-aware
// content card, and a standard footer.
func emailShell(badge, badgeInk, badgeTint, bodyHTML string) string {
	return `<!DOCTYPE html>
<html lang="en" xmlns="http://www.w3.org/1999/xhtml">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<meta name="supported-color-schemes" content="light dark">
<title>BillPiggy</title>
<style>
  body, table, td, a { font-family: ` + fontStack + `; }
  body { margin: 0; padding: 0; background-color: ` + colorPageBg + `; }
  .bp-card { background-color: ` + colorCardBg + ` !important; border-color: ` + colorBorder + ` !important; }
  .bp-ink { color: ` + colorInk + ` !important; }
  .bp-ink-muted { color: ` + colorInkMuted + ` !important; }
  @media (prefers-color-scheme: dark) {
    body { background-color: ` + colorPageBgDark + ` !important; }
    .bp-card { background-color: ` + colorCardBgDark + ` !important; border-color: ` + colorBorderDark + ` !important; }
    .bp-ink { color: ` + colorInkDark + ` !important; }
    .bp-ink-muted { color: ` + colorInkMutedDark + ` !important; }
  }
</style>
</head>
<body style="margin:0;padding:0;background-color:` + colorPageBg + `;">
  <span style="display:none;font-size:1px;line-height:1px;max-height:0;max-width:0;opacity:0;overflow:hidden;">BillPiggy</span>
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background-color:` + colorPageBg + `;">
    <tr>
      <td align="center" style="padding:32px 16px;">
        <table role="presentation" width="600" cellpadding="0" cellspacing="0" style="max-width:600px;width:100%;">
          <tr>
            <td style="background-color:` + colorHeaderBg + `;border-radius:16px 16px 0 0;padding:24px 32px;">
              <img src="data:image/png;base64,` + iconBase64 + `" width="24" height="23" alt="" style="vertical-align:middle;border:0;display:inline-block;">
              <span style="font-size:18px;font-weight:800;color:#ffffff;vertical-align:middle;margin-left:8px;">BillPiggy</span>
            </td>
          </tr>
          <tr>
            <td class="bp-card" style="background-color:` + colorCardBg + `;border:1px solid ` + colorBorder + `;border-top:none;border-radius:0 0 16px 16px;padding:32px;">
              <span style="display:inline-block;background-color:` + badgeTint + `;color:` + badgeInk + `;font-size:11px;font-weight:700;letter-spacing:0.05em;text-transform:uppercase;padding:4px 10px;border-radius:999px;margin-bottom:16px;">` + badge + `</span>
              <div class="bp-ink" style="color:` + colorInk + `;font-size:14px;line-height:1.6;">
` + bodyHTML + `
              </div>
            </td>
          </tr>
          <tr>
            <td style="padding:24px 32px 0;">
              <p class="bp-ink-muted" style="margin:0;color:` + colorInkMuted + `;font-size:12px;line-height:1.6;">This is an automated message from BillPiggy. If you weren't expecting it, you can safely ignore it.</p>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`
}

// statRow renders a label/value line, e.g. "Spent  €54.80" — used for the
// small data blocks in budget_alert, access_changed, and payment_due.
func statRow(label, value string) string {
	return `<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="margin:2px 0;">
  <tr>
    <td class="bp-ink-muted" style="color:` + colorInkMuted + `;font-size:13px;padding:8px 0;border-bottom:1px solid ` + colorDivider + `;">` + label + `</td>
    <td class="bp-ink" style="color:` + colorInk + `;font-size:13px;font-weight:700;text-align:right;padding:8px 0;border-bottom:1px solid ` + colorDivider + `;">` + value + `</td>
  </tr>
</table>`
}

// button renders a bulletproof-ish CTA: a colored table cell with a link
// inside (works even in clients that ignore <a> background styling),
// intended to be followed by a plain-text fallback URL in the caller.
func button(href, label, bg string) string {
	return `<table role="presentation" cellpadding="0" cellspacing="0" style="margin:20px 0;">
  <tr>
    <td style="background-color:` + bg + `;border-radius:12px;">
      <a href="` + href + `" style="display:inline-block;padding:12px 24px;font-size:14px;font-weight:700;color:#ffffff;text-decoration:none;border-radius:12px;">` + label + `</a>
    </td>
  </tr>
</table>`
}

var htmlBodies = map[string]string{
	"invitation": emailShell("Invitation", colorBlueInk, colorBlueTint, `<p style="margin:0 0 16px;font-size:16px;font-weight:700;">You're invited to BillPiggy</p>
<p style="margin:0 0 20px;">You've been invited to join BillPiggy as a <strong>{{.role}}</strong>. BillPiggy helps you track expenses, budgets, and shared bills with your household.</p>
{{if .accept_url}}`+button("{{.accept_url}}", "Accept invitation", colorBlue)+`
<p class="bp-ink-muted" style="margin:0 0 20px;color:`+colorInkMuted+`;font-size:12px;word-break:break-all;">Or paste this link into your browser: {{.accept_url}}</p>
{{else}}
<p style="margin:0 0 20px;">Your invitation code: <code style="background-color:`+colorBlueTint+`;color:`+colorBlueInk+`;padding:2px 6px;border-radius:6px;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;">{{.token}}</code></p>
{{end}}
<p class="bp-ink-muted" style="margin:0;color:`+colorInkMuted+`;font-size:12px;">This invitation expires at {{.expires_at}}.</p>`),

	"budget_alert": emailShell("Budget alert", colorCoralInk, colorCoralTint, `<p style="margin:0 0 16px;font-size:16px;font-weight:700;">Budget alert: {{.budget_name}}</p>
<p style="margin:0 0 20px;">Your "<strong>{{.budget_name}}</strong>" budget has {{if eq .exceeded "true"}}<strong style="color:`+colorErrorInk+`;">been exceeded</strong>{{else}}reached <strong>{{.percent_used}}%</strong> of its limit{{end}}.</p>
`+statRow("Spent", "{{.spent_minor}} {{.currency}}")+statRow("Limit", "{{.limit_minor}} {{.currency}}")+statRow("Period starting", "{{.period_start}}")+`
<p class="bp-ink-muted" style="margin:20px 0 0;color:`+colorInkMuted+`;font-size:12px;">Amounts shown in minor currency units.</p>`),

	"report_ready": emailShell("Report ready", colorEmeraldInk, colorEmeraldTint, `<p style="margin:0 0 16px;font-size:16px;font-weight:700;">Your {{.period_kind}} report is ready</p>
<p style="margin:0 0 4px;">Your {{.period_kind}} expense report for the period starting <strong>{{.period_start}}</strong> is ready.</p>
<p style="margin:0;">Sign in to BillPiggy and open <strong>Reports</strong> to download it.</p>`),

	"access_changed": emailShell("Account update", colorBlueInk, colorBlueTint, `<p style="margin:0 0 16px;font-size:16px;font-weight:700;">Your account access has changed</p>
<p style="margin:0 0 20px;">Your BillPiggy account access was updated by an administrator.</p>
`+statRow("Role", "{{.role}}")+statRow("Access blocked", "{{.blocked}}")+`
<p class="bp-ink-muted" style="margin:20px 0 0;color:`+colorInkMuted+`;font-size:12px;">If you did not expect this change, contact your administrator.</p>`),

	"payment_due": emailShell("Payment due", colorCoralInk, colorCoralTint, `<p style="margin:0 0 16px;font-size:16px;font-weight:700;">{{if eq .reminder "true"}}Upcoming payment{{else}}Payment due{{end}}: {{.payment_title}}</p>
<p style="margin:0 0 20px;">Your {{.frequency}} payment "<strong>{{.payment_title}}</strong>" {{if eq .reminder "true"}}is coming up on <strong>{{.due_at}}</strong>{{else}}was due on <strong>{{.due_at}}</strong>{{end}}.</p>
`+statRow("Amount", "{{.amount_minor}} {{.currency}}")+`
<p style="margin:20px 0 0;">{{if eq .auto_posted "true"}}<span style="color:`+colorEmeraldInk+`;font-weight:700;">&#10003; Already recorded</span> — BillPiggy has logged this expense for you.{{else}}Sign in to BillPiggy to record this expense once you've paid it.{{end}}</p>`),

	"password_reset": emailShell("Password reset", colorBlueInk, colorBlueTint, `<p style="margin:0 0 16px;font-size:16px;font-weight:700;">Reset your password</p>
<p style="margin:0 0 20px;">A password reset was requested for your BillPiggy account.</p>
{{if .reset_url}}`+button("{{.reset_url}}", "Reset password", colorBlue)+`
<p class="bp-ink-muted" style="margin:0 0 20px;color:`+colorInkMuted+`;font-size:12px;word-break:break-all;">Or paste this link into your browser: {{.reset_url}}</p>
{{else}}
<p style="margin:0 0 20px;">Your password reset code: <code style="background-color:`+colorBlueTint+`;color:`+colorBlueInk+`;padding:2px 6px;border-radius:6px;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;">{{.token}}</code></p>
{{end}}
<p class="bp-ink-muted" style="margin:0 0 12px;color:`+colorInkMuted+`;font-size:12px;">This link expires at {{.expires_at}}.</p>
<p class="bp-ink-muted" style="margin:0;color:`+colorInkMuted+`;font-size:12px;">If you did not request this, you can ignore this email — your password will not change.</p>`),
}

// textShell adds a light, consistent header/footer to the plain-text part —
// there's no color/font to style here, just a bit of structure so the
// fallback reads as clearly "from BillPiggy" as the HTML version does.
func textShell(body string) string {
	return "BillPiggy\n—\n\n" + body + "\n\n—\nThis is an automated message from BillPiggy."
}

var textBodies = map[string]string{
	"invitation": textShell(`You've been invited to join BillPiggy as a {{.role}}.
{{if .accept_url}}Accept your invitation: {{.accept_url}}
{{else}}Your invitation code: {{.token}}
{{end}}This invitation expires at {{.expires_at}}.`),

	"budget_alert": textShell(`Your "{{.budget_name}}" budget has {{if eq .exceeded "true"}}been exceeded{{else}}reached {{.percent_used}}% of its limit{{end}}.

Spent: {{.spent_minor}} {{.currency}} (minor units)
Limit: {{.limit_minor}} {{.currency}} (minor units)
Period starting: {{.period_start}}`),

	"report_ready": textShell(`Your {{.period_kind}} expense report for the period starting {{.period_start}} is ready.

Sign in to BillPiggy and open Reports to download it.`),

	"access_changed": textShell(`Your BillPiggy account access was updated by an administrator.

Role: {{.role}}
Access blocked: {{.blocked}}

If you did not expect this change, contact your administrator.`),

	"payment_due": textShell(`Your {{.frequency}} payment "{{.payment_title}}" {{if eq .reminder "true"}}is coming up on {{.due_at}}{{else}}was due on {{.due_at}}{{end}}.

Amount: {{.amount_minor}} {{.currency}} (minor units)
{{if eq .auto_posted "true"}}
BillPiggy has already recorded this expense for you.
{{else}}
Sign in to BillPiggy to record this expense once you have paid it.
{{end}}`),

	"password_reset": textShell(`A password reset was requested for your BillPiggy account.
{{if .reset_url}}Reset your password: {{.reset_url}}
{{else}}Your password reset code: {{.token}}
{{end}}This link expires at {{.expires_at}}.

If you did not request this, you can ignore this email — your password will not change.`),
}
