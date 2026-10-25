package notifications

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// CapturedEmail is an email InboxSender recorded, for local-dev
// inspection only. Never persisted — lost on process restart, which is
// fine for its one purpose (clicking through a registration/reset/invite
// flow in the few minutes after triggering it).
type CapturedEmail struct {
	To      string    `json:"to"`
	Subject string    `json:"subject"`
	Body    string    `json:"body"`
	HTML    string    `json:"html,omitempty"`
	SentAt  time.Time `json:"sent_at"`
}

const maxCapturedEmails = 200

// InboxSender logs exactly like DevLogSender (same line, so nothing
// already watching logs loses visibility) and additionally keeps the
// most recent emails in memory so a human can read the verification/
// reset/invitation token inside them without a real provider or direct
// database access. See internal/notifications/handlers.go for how
// they're exposed, and docs/implementation-status.md for why this must
// never be wired up outside APP_ENV=development — this type alone is not
// the safety boundary, cmd/api's choice of which sender to construct is.
type InboxSender struct {
	Logger *slog.Logger

	mu     sync.Mutex
	emails []CapturedEmail
}

func (s *InboxSender) Send(_ context.Context, email Email) error {
	s.Logger.Warn("dev_email_sender_used_not_a_real_provider",
		"to", email.To, "subject", email.Subject)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.emails = append(s.emails, CapturedEmail{
		To: email.To, Subject: email.Subject, Body: email.Body, HTML: email.HTML, SentAt: time.Now(),
	})
	if len(s.emails) > maxCapturedEmails {
		s.emails = s.emails[len(s.emails)-maxCapturedEmails:]
	}
	return nil
}

// Recent returns captured emails, newest first.
func (s *InboxSender) Recent() []CapturedEmail {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CapturedEmail, len(s.emails))
	for i, e := range s.emails {
		out[len(s.emails)-1-i] = e
	}
	return out
}
