package domain

import "time"

// PaymentFrequency is how often a scheduled payment recurs.
type PaymentFrequency string

const (
	// PaymentMonthly recurs on the same day of every month.
	PaymentMonthly PaymentFrequency = "monthly"
	// PaymentQuarterly recurs every three months.
	PaymentQuarterly PaymentFrequency = "quarterly"
	// PaymentYearly recurs on the same date every year.
	PaymentYearly PaymentFrequency = "yearly"
	// PaymentCustom recurs every CustomIntervalDays days. Day-based rather
	// than month-based on purpose: the month-based cadences already have their
	// own values above, so custom exists to express the intervals they cannot,
	// such as weekly, fortnightly, or every 45 days.
	PaymentCustom PaymentFrequency = "custom"
)

// PostingKind distinguishes the two things that can happen to one occurrence
// of a scheduled payment, each of which must happen at most once.
type PostingKind string

const (
	// PostingReminder is the advance notice sent ReminderDaysBefore the due date.
	PostingReminder PostingKind = "reminder"
	// PostingDue is the occurrence itself falling due, which posts an expense
	// when the payment is set to auto-post.
	PostingDue PostingKind = "due"
)

// ScheduledPayment is a recurring obligation such as rent, an insurance
// premium, or an internet subscription.
//
// It is a template plus a cursor: the template describes what to charge and
// how often, while NextDueAt tracks the occurrence the scheduler has not yet
// handled. StartDate stays fixed as the recurrence anchor so month-end dates
// survive short months — see NextPaymentDue.
type ScheduledPayment struct {
	// ID identifies the scheduled payment.
	ID string `json:"id"`
	// OwnerID is the user the payment belongs to.
	OwnerID string `json:"ownerID"`
	// Title is the display name, e.g. "Rent".
	Title string `json:"title"`
	// AmountMinor is the recurring amount in the currency's minor units.
	AmountMinor int64 `json:"amountMinor"`
	// Currency is the ISO 4217 currency code.
	Currency string `json:"currency"`
	// CategoryID and CategoryName classify the expenses this payment creates.
	CategoryID   string `json:"categoryID"`
	CategoryName string `json:"categoryName"`
	// TagIDs are copied onto every expense this payment creates.
	TagIDs []string `json:"tagIDs"`
	// SharedGroupID optionally shares the payment, and the expenses it
	// creates, with a user group.
	SharedGroupID string `json:"sharedGroupID"`
	// Frequency is how often the payment recurs.
	Frequency PaymentFrequency `json:"frequency"`
	// CustomIntervalDays is the gap between occurrences when Frequency is
	// PaymentCustom, and is ignored otherwise.
	CustomIntervalDays int `json:"customIntervalDays"`
	// StartDate is the first occurrence and the permanent recurrence anchor.
	StartDate time.Time `json:"startDate"`
	// EndDate optionally stops the recurrence; occurrences after it never run.
	EndDate *time.Time `json:"endDate"`
	// NextDueAt is the next occurrence the scheduler has yet to handle.
	NextDueAt time.Time `json:"nextDueAt"`
	// LastPostedAt is when an occurrence last fell due, if one ever has.
	LastPostedAt *time.Time `json:"lastPostedAt"`
	// AutoPost creates a confirmed expense automatically as each occurrence
	// falls due. When false the payment only notifies, and the user records
	// the expense themselves.
	AutoPost bool `json:"autoPost"`
	// ReminderDaysBefore sends an advance notice this many days before an
	// occurrence. Zero disables the advance notice.
	ReminderDaysBefore int `json:"reminderDaysBefore"`
	// Paused suspends the payment without deleting it.
	Paused bool `json:"paused"`
	// CreatedAt and UpdatedAt record projection timestamps.
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// DeletedAt marks a soft-deleted payment.
	DeletedAt *time.Time `json:"deletedAt"`
}

// ScheduledPaymentPosting records that one occurrence of a scheduled payment
// has been handled. It is the cross-replica idempotency key: the repository
// rejects a duplicate (ScheduledPaymentID, DueAt, Kind), so two schedulers
// racing on the same occurrence produce exactly one expense and one
// notification.
type ScheduledPaymentPosting struct {
	// ScheduledPaymentID is the payment the occurrence belongs to.
	ScheduledPaymentID string `json:"scheduledPaymentID"`
	// DueAt is the occurrence's due date.
	DueAt time.Time `json:"dueAt"`
	// Kind is which of the occurrence's two steps this row claims.
	Kind PostingKind `json:"kind"`
	// ExpenseID is the auto-posted expense, when one was created.
	ExpenseID string `json:"expenseID"`
	// PostedAt is when the scheduler handled the occurrence.
	PostedAt time.Time `json:"postedAt"`
}

// ScheduledPaymentCreated is emitted once when a payment is scheduled.
type ScheduledPaymentCreated struct {
	Payment ScheduledPayment `json:"payment"`
}

// ScheduledPaymentUpdated is emitted for every authorized edit.
type ScheduledPaymentUpdated struct {
	Payment ScheduledPayment `json:"payment"`
}

// ScheduledPaymentRemoved is emitted instead of hard-deleting a payment.
type ScheduledPaymentRemoved struct {
	ScheduledPaymentID string    `json:"scheduled_payment_id"`
	OwnerID            string    `json:"owner_id"`
	RemovedAt          time.Time `json:"removed_at"`
}

// ScheduledPaymentPosted is emitted each time an occurrence falls due,
// whether or not it auto-posted an expense.
type ScheduledPaymentPosted struct {
	ScheduledPaymentID string    `json:"scheduled_payment_id"`
	OwnerID            string    `json:"owner_id"`
	ExpenseID          string    `json:"expense_id"`
	DueAt              time.Time `json:"due_at"`
	PostedAt           time.Time `json:"posted_at"`
}

// ValidPaymentFrequency reports whether frequency is one this app schedules.
func ValidPaymentFrequency(frequency PaymentFrequency) bool {
	switch frequency {
	case PaymentMonthly, PaymentQuarterly, PaymentYearly, PaymentCustom:
		return true
	}
	return false
}

// PaymentFrequencyMonths returns how many months separate two occurrences of
// a month-based frequency, and 0 for PaymentCustom, which counts in days.
func PaymentFrequencyMonths(frequency PaymentFrequency) int {
	switch frequency {
	case PaymentMonthly:
		return 1
	case PaymentQuarterly:
		return 3
	case PaymentYearly:
		return 12
	}
	return 0
}

// daysInMonth returns the number of days in the given month. Day 0 of the
// following month is the last day of this one.
func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// AddMonthsClamped adds months to at, landing on anchorDay or on the target
// month's final day when the target month is too short to contain anchorDay.
//
// This is why the anchor is passed in rather than read from at: a rent due on
// the 31st must land on Feb 28 and then return to Mar 31, which is impossible
// if each step derives its day from the previous, already-clamped one.
// time.Time.AddDate cannot express this at all — it overflows Jan 31 + 1 month
// into Mar 3.
func AddMonthsClamped(at time.Time, months, anchorDay int) time.Time {
	// time.Date normalizes an out-of-range month into the following year, so
	// month arithmetic needs no manual carry here.
	target := time.Date(at.Year(), at.Month()+time.Month(months), 1, 0, 0, 0, 0, at.Location())
	day := anchorDay
	if day < 1 {
		day = at.Day()
	}
	if last := daysInMonth(target.Year(), target.Month()); day > last {
		day = last
	}
	return time.Date(target.Year(), target.Month(), day, at.Hour(), at.Minute(), at.Second(), 0, at.Location())
}

// NextPaymentDue returns the occurrence that follows current.
//
// anchorDay is the day-of-month the recurrence is anchored to, normally
// StartDate.Day(); pass 0 to anchor on current's own day.
func NextPaymentDue(frequency PaymentFrequency, customIntervalDays, anchorDay int, current time.Time) time.Time {
	if months := PaymentFrequencyMonths(frequency); months > 0 {
		return AddMonthsClamped(current, months, anchorDay)
	}
	if frequency == PaymentCustom {
		if customIntervalDays < 1 {
			customIntervalDays = 1
		}
		return current.AddDate(0, 0, customIntervalDays)
	}
	return current
}

// NextDueFor returns the payment's occurrence following current, using the
// payment's own frequency, interval, and StartDate anchor.
func NextDueFor(payment ScheduledPayment, current time.Time) time.Time {
	return NextPaymentDue(payment.Frequency, payment.CustomIntervalDays, payment.StartDate.Day(), current)
}

// ScheduledPaymentFinished reports whether dueAt falls past the payment's end
// date, meaning the recurrence has run its course.
func ScheduledPaymentFinished(payment ScheduledPayment, dueAt time.Time) bool {
	return payment.EndDate != nil && dueAt.After(*payment.EndDate)
}

// ReminderDueAt returns when the advance notice for an occurrence should be
// sent, and whether the payment asks for one at all.
func ReminderDueAt(payment ScheduledPayment, dueAt time.Time) (time.Time, bool) {
	if payment.ReminderDaysBefore <= 0 {
		return time.Time{}, false
	}
	return dueAt.AddDate(0, 0, -payment.ReminderDaysBefore), true
}
