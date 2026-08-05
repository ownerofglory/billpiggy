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
	// NotificationAccessChanged is sent when an administrator changes a
	// user's role or blocks/unblocks their access.
	NotificationAccessChanged NotificationKind = "access_changed"
	// NotificationPaymentDue is sent when a scheduled payment falls due, and
	// as its advance reminder when one is configured.
	NotificationPaymentDue NotificationKind = "payment_due"
)

// NotificationDelivery is an asynchronous email delivery request.
type NotificationDelivery struct {
	// ID uniquely identifies the delivery request.
	ID string
	// UserID identifies the recipient user. Empty when the recipient has no
	// account yet (an invitation), in which case RecipientEmail is set instead.
	UserID string
	// RecipientEmail addresses a delivery directly, bypassing UserID lookup
	// and per-kind preferences — used only for invitations, whose recipient
	// is not yet a user and so has no preferences to honour.
	RecipientEmail string
	// Kind describes the notification template.
	Kind NotificationKind
	// Payload contains template data. It is cleared once a delivery reaches
	// a terminal state (Sent or dead-lettered Failed), since nothing reads it
	// afterward; an invitation's payload carries the raw invitation token
	// until then, which is the one exception to "no credentials in payload"
	// every other kind follows.
	Payload map[string]string
	// CreatedAt records when the request was queued.
	CreatedAt time.Time
	// Status tracks the asynchronous delivery lifecycle.
	Status NotificationStatus
	// Attempts counts delivery attempts made so far, including the one in
	// progress once a worker claims the delivery.
	Attempts int
}

// NotificationStatus describes the durable delivery state.
type NotificationStatus string

const (
	// NotificationPending is queued and not currently claimed by a worker.
	NotificationPending NotificationStatus = "pending"
	// NotificationProcessing is claimed by a worker attempting delivery. A
	// worker that crashes mid-attempt leaves a delivery here until the lease
	// expires, at which point another worker's claim reclaims it.
	NotificationProcessing NotificationStatus = "processing"
	// NotificationSent was handed to the configured email provider.
	NotificationSent NotificationStatus = "sent"
	// NotificationFailed has exhausted its retry attempts and will not be
	// tried again automatically.
	NotificationFailed NotificationStatus = "failed"
)
