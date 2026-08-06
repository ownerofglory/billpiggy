package emailtmpl

// Each kind supplies a subject (plain text/template) and a body rendered
// twice: once through text/template for the plain-text part, once through
// html/template for the HTML part, so both escape correctly for their own
// output. Missing payload keys render as empty strings rather than erroring,
// since map indexing in both template packages has no concept of a required
// field.
var subjects = map[string]string{
	"invitation":     "You're invited to BillPiggy",
	"budget_alert":   "Budget alert: {{.budget_name}}",
	"report_ready":   "Your {{.period_kind}} report is ready",
	"access_changed": "Your BillPiggy account access has changed",
	"payment_due":    "{{if eq .reminder \"true\"}}Upcoming payment{{else}}Payment due{{end}}: {{.payment_title}}",
	"password_reset": "Reset your BillPiggy password",
}

var textBodies = map[string]string{
	"invitation": `You've been invited to join BillPiggy as a {{.role}}.
{{if .accept_url}}Accept your invitation: {{.accept_url}}
{{else}}Your invitation code: {{.token}}
{{end}}This invitation expires at {{.expires_at}}.`,

	"budget_alert": `Your "{{.budget_name}}" budget has {{if eq .exceeded "true"}}been exceeded{{else}}reached {{.percent_used}}% of its limit{{end}}.

Spent: {{.spent_minor}} {{.currency}} (minor units)
Limit: {{.limit_minor}} {{.currency}} (minor units)
Period starting: {{.period_start}}`,

	"report_ready": `Your {{.period_kind}} expense report for the period starting {{.period_start}} is ready.

Sign in to BillPiggy and open Reports to download it.`,

	"access_changed": `Your BillPiggy account access was updated by an administrator.

Role: {{.role}}
Access blocked: {{.blocked}}

If you did not expect this change, contact your administrator.`,

	"payment_due": `Your {{.frequency}} payment "{{.payment_title}}" {{if eq .reminder "true"}}is coming up on {{.due_at}}{{else}}was due on {{.due_at}}{{end}}.

Amount: {{.amount_minor}} {{.currency}} (minor units)
{{if eq .auto_posted "true"}}
BillPiggy has already recorded this expense for you.
{{else}}
Sign in to BillPiggy to record this expense once you have paid it.
{{end}}`,

	"password_reset": `A password reset was requested for your BillPiggy account.
{{if .reset_url}}Reset your password: {{.reset_url}}
{{else}}Your password reset code: {{.token}}
{{end}}This link expires at {{.expires_at}}.

If you did not request this, you can ignore this email — your password will not change.`,
}

var htmlBodies = map[string]string{
	"invitation": `<p>You've been invited to join BillPiggy as a <strong>{{.role}}</strong>.</p>
{{if .accept_url}}<p><a href="{{.accept_url}}">Accept your invitation</a></p>{{else}}<p>Your invitation code: <code>{{.token}}</code></p>{{end}}
<p>This invitation expires at {{.expires_at}}.</p>`,

	"budget_alert": `<p>Your "<strong>{{.budget_name}}</strong>" budget has {{if eq .exceeded "true"}}been exceeded{{else}}reached {{.percent_used}}% of its limit{{end}}.</p>
<ul>
<li>Spent: {{.spent_minor}} {{.currency}} (minor units)</li>
<li>Limit: {{.limit_minor}} {{.currency}} (minor units)</li>
<li>Period starting: {{.period_start}}</li>
</ul>`,

	"report_ready": `<p>Your {{.period_kind}} expense report for the period starting {{.period_start}} is ready.</p>
<p>Sign in to BillPiggy and open <strong>Reports</strong> to download it.</p>`,

	"access_changed": `<p>Your BillPiggy account access was updated by an administrator.</p>
<ul>
<li>Role: {{.role}}</li>
<li>Access blocked: {{.blocked}}</li>
</ul>
<p>If you did not expect this change, contact your administrator.</p>`,

	"payment_due": `<p>Your {{.frequency}} payment "<strong>{{.payment_title}}</strong>" {{if eq .reminder "true"}}is coming up on {{.due_at}}{{else}}was due on {{.due_at}}{{end}}.</p>
<ul>
<li>Amount: {{.amount_minor}} {{.currency}} (minor units)</li>
</ul>
{{if eq .auto_posted "true"}}<p>BillPiggy has already recorded this expense for you.</p>{{else}}<p>Sign in to BillPiggy to record this expense once you have paid it.</p>{{end}}`,

	"password_reset": `<p>A password reset was requested for your BillPiggy account.</p>
{{if .reset_url}}<p><a href="{{.reset_url}}">Reset your password</a></p>{{else}}<p>Your password reset code: <code>{{.token}}</code></p>{{end}}
<p>This link expires at {{.expires_at}}.</p>
<p>If you did not request this, you can ignore this email — your password will not change.</p>`,
}
