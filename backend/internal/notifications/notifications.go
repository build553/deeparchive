// Package notifications defines the outbound-email abstraction used by
// identity and other domains. No real provider (SES, Postmark, etc.) is
// wired up yet; DevLogSender below is for local development only and must
// never be selected in a production configuration. See
// docs/implementation-status.md.
package notifications

import (
	"context"
	"log/slog"
)

type Email struct {
	To      string
	Subject string
	Body    string // plain text; always set
	HTML    string // optional rich alternative; clients that can't render it show Body
}

// Sender delivers an email and returns an error unless the provider
// confirmed acceptance. Callers must not treat a call to Send as having
// succeeded until it returns a nil error.
type Sender interface {
	Send(ctx context.Context, email Email) error
}

// FanoutSender sends through Primary (its error is the one that
// propagates) and additionally through Also, best-effort — used in
// development when a real SMTP sender is configured so Also can still be
// an InboxSender: real mail goes out AND the Dev Inbox keeps working for
// the walkthrough doc and e2e tests, without re-deriving tokens from a
// real inbox.
type FanoutSender struct {
	Primary Sender
	Also    []Sender
}

func (s FanoutSender) Send(ctx context.Context, email Email) error {
	err := s.Primary.Send(ctx, email)
	for _, also := range s.Also {
		_ = also.Send(ctx, email)
	}
	return err
}

// DevLogSender "sends" email by logging it. It exists so the registration
// and password-reset flows are exercisable in local development and in
// integration tests without a real provider, while making the limitation
// impossible to miss in logs and in code review.
type DevLogSender struct {
	Logger *slog.Logger
}

func (s DevLogSender) Send(_ context.Context, email Email) error {
	s.Logger.Warn("dev_email_sender_used_not_a_real_provider",
		"to", email.To, "subject", email.Subject)
	return nil
}
