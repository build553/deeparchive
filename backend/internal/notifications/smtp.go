package notifications

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/smtp"
	"net/textproto"
)

// SMTPSender sends real email via an SMTP provider (Gmail SMTP by
// default — see internal/config.SMTPConfig). Unlike DevLogSender and
// InboxSender, this actually delivers mail to the recipient's real
// inbox, so cmd/api only constructs it when SMTP_USERNAME/SMTP_PASSWORD
// are both set; otherwise it falls back to the dev-only senders.
type SMTPSender struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
	// FromName is the display name recipients see, e.g. "Build553 Team".
	FromName string
}

func (s SMTPSender) Send(_ context.Context, email Email) error {
	msg, err := s.build(email)
	if err != nil {
		return fmt.Errorf("building email: %w", err)
	}
	addr := fmt.Sprintf("%s:%s", s.Host, s.Port)
	auth := smtp.PlainAuth("", s.Username, s.Password, s.Host)
	// smtp.SendMail dials plaintext, then transparently upgrades via
	// STARTTLS if the server advertises it (Gmail always does on 587).
	if err := smtp.SendMail(addr, auth, s.From, []string{email.To}, msg); err != nil {
		return fmt.Errorf("sending email via smtp: %w", err)
	}
	return nil
}

// build renders a multipart/alternative message (plain text first, HTML
// last — the order clients prefer) with quoted-printable bodies so long
// HTML lines never exceed SMTP's 998-byte limit. Plain text only when
// there's no HTML.
func (s SMTPSender) build(email Email) ([]byte, error) {
	from := mail.Address{Name: s.FromName, Address: s.From}
	var buf bytes.Buffer
	hdr := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	hdr("From", from.String())
	hdr("To", email.To)
	hdr("Subject", mime.QEncoding.Encode("utf-8", email.Subject))
	hdr("MIME-Version", "1.0")

	if email.HTML == "" {
		hdr("Content-Type", `text/plain; charset="UTF-8"`)
		hdr("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		if err := writeQP(&buf, email.Body); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	boundary := make([]byte, 12)
	if _, err := rand.Read(boundary); err != nil {
		return nil, err
	}
	mw := multipart.NewWriter(&buf)
	if err := mw.SetBoundary("da" + hex.EncodeToString(boundary)); err != nil {
		return nil, err
	}
	hdr("Content-Type", "multipart/alternative; boundary="+mw.Boundary())
	buf.WriteString("\r\n")
	for _, part := range []struct{ ctype, body string }{
		{`text/plain; charset="UTF-8"`, email.Body},
		{`text/html; charset="UTF-8"`, email.HTML},
	} {
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.ctype},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		if err := writeQP(pw, part.body); err != nil {
			return nil, err
		}
	}
	// Close before Bytes(): in a single return statement Bytes() would be
	// evaluated first and miss the closing boundary.
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeQP(w interface{ Write([]byte) (int, error) }, body string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(body)); err != nil {
		return err
	}
	return qp.Close()
}
