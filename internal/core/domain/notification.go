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
	// Status tracks the asynchronous delivery lifecycle.
	Status NotificationStatus
}

// NotificationStatus describes the durable delivery state.
type NotificationStatus string

const (
	// NotificationPending has not yet been delivered.
	NotificationPending NotificationStatus = "pending"
	// NotificationSent was handed to the configured email provider.
	NotificationSent NotificationStatus = "sent"
	// NotificationFailed can be retried by an operator or a future worker run.
	NotificationFailed NotificationStatus = "failed"
)
