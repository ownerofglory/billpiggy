package domain

import "time"

// NotificationKind identifies the user-visible reason for an email notification.
type NotificationKind string

const (
	// NotificationInvitation is sent when an administrator invites a user.
	NotificationInvitation NotificationKind = "invitation"
	// NotificationBudgetAlert is sent when budget spending crosses a threshold.
	NotificationBudgetAlert NotificationKind = "budget_alert"
	// NotificationReportReady is sent when a periodic report is available.
	NotificationReportReady NotificationKind = "report_ready"
)

// NotificationDelivery is an asynchronous email delivery request.
type NotificationDelivery struct {
	// ID uniquely identifies the delivery request.
	ID string
	// UserID identifies the recipient user.
	UserID string
	// Kind describes the notification template.
	Kind NotificationKind
	// Payload contains template data without credentials.
	Payload map[string]string
	// CreatedAt records when the request was queued.
	CreatedAt time.Time
}
