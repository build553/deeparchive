package notifications

import (
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
)

func TestBuildMultipartAlternativeParses(t *testing.T) {
	s := SMTPSender{From: "team@example.com", FromName: "Build553 Team"}
	html := "<p>" + strings.Repeat("long line ", 400) + "é</p>" // >998 bytes, non-ASCII
	raw, err := s.build(Email{To: "u@example.com", Subject: "Welcome to DeepArchive", Body: "plain é", HTML: html})
	if err != nil {
		t.Fatal(err)
	}
	m, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Header.Get("From"); !strings.Contains(got, "Build553 Team") || !strings.Contains(got, "team@example.com") {
		t.Fatalf("From = %q", got)
	}
	mt, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" {
		t.Fatalf("content-type %q err %v", mt, err)
	}
	mr := multipart.NewReader(m.Body, params["boundary"])
	var bodies []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(quotedprintable.NewReader(p))
		bodies = append(bodies, string(b))
	}
	if len(bodies) != 2 || bodies[0] != "plain é" || bodies[1] != html {
		t.Fatalf("parts not round-tripped: %d parts", len(bodies))
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line of %d bytes exceeds SMTP limit", len(line))
		}
	}
}

func TestBuildPlainTextOnlyWhenNoHTML(t *testing.T) {
	raw, err := SMTPSender{From: "a@b.c"}.build(Email{To: "u@b.c", Subject: "s", Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "hello") || strings.Contains(string(raw), "multipart") {
		t.Fatalf("unexpected message:\n%s", raw)
	}
}
