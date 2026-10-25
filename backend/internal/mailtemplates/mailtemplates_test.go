package mailtemplates

import (
	"strings"
	"testing"
)

func TestTemplatesRenderLinksAndKeepTokenShape(t *testing.T) {
	web := "https://app.example.com"
	cases := []struct {
		name, textPrefix, linkPath string
		mail                       func() (string, string, string) // body, html, subject
	}{
		{"verify", "Verification token: tok123 ", "/verify-email?token=tok123", func() (string, string, string) { m := Verify(web, "tok123"); return m.Body, m.HTML, m.Subject }},
		{"reset", "Reset token: tok123 ", "/reset-password?token=tok123", func() (string, string, string) { m := Reset(web, "tok123"); return m.Body, m.HTML, m.Subject }},
		{"invite", "Invitation token: tok123 ", "/organizations?token=tok123", func() (string, string, string) { m := Invite(web, "tok123"); return m.Body, m.HTML, m.Subject }},
	}
	for _, c := range cases {
		body, html, _ := c.mail()
		if !strings.HasPrefix(body, c.textPrefix) {
			t.Errorf("%s: text body must start with the parseable token line, got %q", c.name, body)
		}
		if !strings.Contains(html, web+c.linkPath) {
			t.Errorf("%s: html missing button link", c.name)
		}
	}
}

func TestOTPKeepsParseableShape(t *testing.T) {
	m := AdminOTP("123456")
	if !strings.Contains(m.Body, "code is: 123456") || !strings.Contains(m.HTML, "123456") {
		t.Fatal("otp must appear in both parts")
	}
}

func TestWelcomeEscapesDisplayName(t *testing.T) {
	m := Welcome("https://app.example.com", `<script>alert(1)</script>`)
	if strings.Contains(m.HTML, "<script>") {
		t.Fatal("display name is user-controlled and must be HTML-escaped")
	}
	if m.Subject != "Welcome to DeepArchive" || !strings.Contains(m.Body, "Build553") {
		t.Fatal("welcome must come from the Build553 team")
	}
}

func TestNoWebURLOmitsButton(t *testing.T) {
	if strings.Contains(Verify("", "tok").HTML, "<a href") {
		t.Fatal("no base URL means no link")
	}
}
