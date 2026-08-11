package email

import (
	"context"
	"errors"

	"github.com/ownerofglory/billpiggy/pkg/ratelimit"
)

// Sender is anything that can deliver one rendered email. It matches
// service.EmailSender structurally.
type Sender interface {
	Send(ctx context.Context, to, subject, text, html string) error
}

// ErrSendLimitExceeded is returned once a configured send limit — typically a
// provider's free-tier monthly quota — has already been reached for the
// current window. Callers should treat this as "try again later," not as a
// failure of this particular message: sending resumes on its own once the
// window rolls over.
var ErrSendLimitExceeded = errors.New("email send limit exceeded for this period")

// rateLimitedSender wraps a Sender with a shared send-count limit, so a
// provider's quota is enforced locally instead of being discovered as a wall
// of rejected sends once it's already been exhausted.
type rateLimitedSender struct {
	next  Sender
	limit ratelimit.Limiter
}

// sendLimitKey is the single shared key every send checks against: the quota
// belongs to the provider account, not to any one recipient or notification
// kind, so there is exactly one counter, not one per key.
const sendLimitKey = "email-send"

// WithSendLimit wraps sender so that at most limit's configured count of
// emails go out per its window, shared across every recipient and
// notification kind.
func WithSendLimit(sender Sender, limit ratelimit.Limiter) Sender {
	return &rateLimitedSender{next: sender, limit: limit}
}

func (s *rateLimitedSender) Send(ctx context.Context, to, subject, text, html string) error {
	allowed, err := s.limit.Allow(ctx, sendLimitKey)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrSendLimitExceeded
	}
	return s.next.Send(ctx, to, subject, text, html)
}
