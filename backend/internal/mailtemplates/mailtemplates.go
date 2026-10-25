// Package mailtemplates builds DeepArchive's transactional emails: one
// shared responsive layout (table-based, inline styles, dark-mode aware,
// same flat blue as the web app) plus a plain-text alternative for each.
//
// Plain-text bodies deliberately keep the "<Noun> token: <value>" shape
// the Dev Inbox and e2e tests parse, so the HTML is purely additive.
package mailtemplates

import (
	"bytes"
	"fmt"
	"html/template"
	"net/url"
	"strings"

	"deeparchive/internal/notifications"
)

const teamName = "Build553"

type view struct {
	Preheader  string
	Heading    string
	Paragraphs []string
	Code       string // large one-time code box, optional
	CTALabel   string
	CTAURL     string
	Steps      []string // numbered list, optional
	Note       string
}

var layout = template.Must(template.New("mail").Funcs(template.FuncMap{"inc": func(i int) int { return i + 1 }}).Parse(`<!doctype html>
<html lang="en"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark"><meta name="supported-color-schemes" content="light dark">
<style>
 :root{color-scheme:light dark}
 @media (max-width:600px){.wrap{padding:16px 12px!important}.card{padding:24px 20px!important}.h1{font-size:24px!important}.code{font-size:28px!important;letter-spacing:6px!important}.btn a{display:block!important}}
 @media (prefers-color-scheme:dark){.bg{background:#0b0d10!important}.card{background:#161a1f!important;border-color:#2a2f36!important;border-top-color:#4f8cff!important}.rule{border-top-color:#2a2f36!important}.t{color:#e6e8eb!important}.dim{color:#9aa3ad!important}.box{background:#1d2228!important;border-color:#2a2f36!important}.btn a{background:#4f8cff!important}.acc{color:#4f8cff!important}.step{background:#4f8cff!important}}
</style></head>
<body class="bg" style="margin:0;padding:0;background:#f5f6f8">
<div style="display:none;max-height:0;overflow:hidden;opacity:0">{{.Preheader}}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" class="bg" style="background:#f5f6f8"><tr><td align="center" class="wrap" style="padding:32px 16px">
 <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px">
  <tr><td style="padding:0 4px 16px">
   <table role="presentation" cellpadding="0" cellspacing="0"><tr>
    <td style="width:26px;height:26px;background:#0969da;border-radius:7px;font-size:0;line-height:0" class="step">&nbsp;</td>
    <td class="t" style="padding-left:10px;font:700 18px -apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;color:#1b1f23;letter-spacing:-0.01em">DeepArchive</td>
   </tr></table>
  </td></tr>
  <tr><td class="card" style="background:#fff;border:1px solid #e1e4e8;border-radius:16px;padding:36px 36px 32px;border-top:4px solid #0969da">
   <h1 class="h1 t" style="margin:0 0 14px;font:700 28px/1.2 -apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;color:#1b1f23;letter-spacing:-0.02em">{{.Heading}}</h1>
   {{range .Paragraphs}}<p class="dim" style="margin:0 0 16px;font:400 16px/1.55 -apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;color:#5b6572">{{.}}</p>{{end}}
   {{if .Code}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="margin:8px 0 20px"><tr><td align="center" class="box code t" style="background:#f0f1f4;border:1px solid #e1e4e8;border-radius:12px;padding:18px 12px;font:700 36px ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;letter-spacing:10px;color:#1b1f23">{{.Code}}</td></tr></table>{{end}}
   {{if .Steps}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="margin:4px 0 20px">{{range $i, $s := .Steps}}<tr><td valign="top" style="padding:0 12px 12px 0;width:28px"><div class="step" style="width:26px;height:26px;border-radius:13px;background:#0969da;color:#fff;text-align:center;font:700 13px/26px -apple-system,Helvetica,Arial,sans-serif">{{inc $i}}</div></td><td class="t" style="padding:2px 0 12px;font:400 15px/1.5 -apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;color:#1b1f23">{{$s}}</td></tr>{{end}}</table>{{end}}
   {{if .CTAURL}}<table role="presentation" cellpadding="0" cellspacing="0" class="btn" style="margin:8px 0 8px"><tr><td><a href="{{.CTAURL}}" style="display:inline-block;background:#0969da;color:#fff;text-decoration:none;font:600 16px -apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;padding:14px 28px;border-radius:10px;text-align:center">{{.CTALabel}}</a></td></tr></table>
   <p class="dim" style="margin:12px 0 0;font:400 13px/1.5 -apple-system,Helvetica,Arial,sans-serif;color:#8a9099">Button not working? Paste this link into your browser:<br><a class="acc" href="{{.CTAURL}}" style="color:#0969da;word-break:break-all">{{.CTAURL}}</a></p>{{end}}
   {{if .Note}}<p class="dim rule" style="margin:24px 0 0;padding-top:18px;border-top:1px solid #e1e4e8;font:400 13px/1.5 -apple-system,Helvetica,Arial,sans-serif;color:#8a9099">{{.Note}}</p>{{end}}
  </td></tr>
  <tr><td class="dim" style="padding:20px 8px 0;text-align:center;font:400 12px/1.6 -apple-system,Helvetica,Arial,sans-serif;color:#8a9099">Sent by the {{.Team}} team &middot; DeepArchive<br>Encrypted backups, only you hold the key.</td></tr>
 </table>
</td></tr></table></body></html>`))

func render(v view) string {
	var buf bytes.Buffer
	data := struct {
		view
		Team string
	}{v, teamName}
	if err := layout.Execute(&buf, data); err != nil {
		return "" // text alternative still goes out
	}
	return buf.String()
}

func link(web, path, token string) string {
	if web == "" {
		return ""
	}
	return strings.TrimRight(web, "/") + path + "?token=" + url.QueryEscape(token)
}

func textTail(u string) string {
	if u == "" {
		return ""
	}
	return "\n\nOr open: " + u
}

func Verify(web, token string) notifications.Email {
	u := link(web, "/verify-email", token)
	return notifications.Email{
		Subject: "Verify your DeepArchive account",
		Body:    fmt.Sprintf("Verification token: %s (expires in 15 minutes)%s", token, textTail(u)),
		HTML: render(view{
			Preheader:  "Confirm your email to finish setting up DeepArchive.",
			Heading:    "Confirm your email",
			Paragraphs: []string{"One quick step: confirm this is your email address so we can reach you about your backups."},
			CTALabel:   "Verify email", CTAURL: u,
			Note: "This link expires in 15 minutes. If it lapses, just sign up again. If you didn't create a DeepArchive account, you can ignore this email.",
		}),
	}
}

func Reset(web, token string) notifications.Email {
	u := link(web, "/reset-password", token)
	return notifications.Email{
		Subject: "Reset your DeepArchive password",
		Body:    fmt.Sprintf("Reset token: %s (expires in 1h)%s", token, textTail(u)),
		HTML: render(view{
			Preheader:  "Choose a new password for your DeepArchive account.",
			Heading:    "Reset your password",
			Paragraphs: []string{"We got a request to reset your password. Choose a new one with the button below."},
			CTALabel:   "Choose a new password", CTAURL: u,
			Note: "This link expires in 1 hour and works once. If you didn't ask for this, ignore this email; your password hasn't changed.",
		}),
	}
}

func Invite(web, token string) notifications.Email {
	u := link(web, "/organizations", token)
	return notifications.Email{
		Subject: "You've been invited to a DeepArchive organization",
		Body:    fmt.Sprintf("Invitation token: %s (expires in 7 days)%s", token, textTail(u)),
		HTML: render(view{
			Preheader:  "Join a DeepArchive organization.",
			Heading:    "You're invited",
			Paragraphs: []string{"You've been invited to join an organization on DeepArchive. Accept to share its backup storage."},
			CTALabel:   "Accept invitation", CTAURL: u,
			Note: "This invitation expires in 7 days. If you weren't expecting it, you can ignore this email.",
		}),
	}
}

func AdminOTP(code string) notifications.Email {
	return notifications.Email{
		Subject: "Your DeepArchive admin sign-in code",
		Body: fmt.Sprintf("Your one-time verification code is: %s\n\nThis code expires in 5 minutes and can only be used once. "+
			"If you did not just try to sign in to the DeepArchive admin panel, ignore this email and consider changing your password.", code),
		HTML: render(view{
			Preheader:  "Your one-time admin sign-in code.",
			Heading:    "Your sign-in code",
			Paragraphs: []string{"Enter this code to finish signing in to the admin panel."},
			Code:       code,
			Note:       "Expires in 5 minutes and works once. If this wasn't you, ignore this email and consider changing your password.",
		}),
	}
}

// RecoveryCode is the email for reactivating a deactivated account.
func RecoveryCode(code string, daysLeft int) notifications.Email {
	left := fmt.Sprintf("%d days", daysLeft)
	if daysLeft == 1 {
		left = "1 day"
	}
	return notifications.Email{
		Subject: "Restore your DeepArchive account",
		Body: fmt.Sprintf("Your one-time verification code is: %s\n\nEnter it to restore your account. It expires in 5 minutes and works once. "+
			"You can restore this account for %s; after that it can no longer be recovered. "+
			"If you did not try to sign in, ignore this email.", code, left),
		HTML: render(view{
			Preheader:  "Your code to restore your DeepArchive account.",
			Heading:    "Welcome back",
			Paragraphs: []string{"This account was deactivated. Enter this code to restore it and pick up where you left off.", "You can restore it for " + left + ". After that it can no longer be recovered."},
			Code:       code,
			Note:       "Expires in 5 minutes and works once. If this wasn't you, ignore this email: your account stays deactivated.",
		}),
	}
}

func Welcome(web, name string) notifications.Email {
	u := strings.TrimRight(web, "/")
	greeting := "Welcome to DeepArchive"
	if name != "" {
		greeting = "Welcome, " + name
	}
	return notifications.Email{
		Subject: "Welcome to DeepArchive",
		Body: greeting + "!\n\nYour account is ready. Three steps to your first backup:\n" +
			"1. Install deepctl and run: deepctl login\n2. Run: deepctl init\n3. Run: deepctl backup <folder>\n\n" +
			"Everything is encrypted on your machine before it leaves it.\n\nThe " + teamName + " team",
		HTML: render(view{
			Preheader:  "Your account is ready. Here's how to make your first backup.",
			Heading:    greeting,
			Paragraphs: []string{"Your account is ready. Everything you back up is encrypted on your machine before it leaves it, so only you can read it."},
			Steps: []string{
				"Install deepctl and run deepctl login. Your browser opens; no password in the terminal.",
				"Run deepctl init to enroll this machine and choose a vault passphrase.",
				"Run deepctl backup <folder>, then browse it any time from your dashboard.",
			},
			CTALabel: "Open your dashboard", CTAURL: u,
			Note: "Keep your vault passphrase safe: there is no way to recover it. Questions? Just reply to this email. The " + teamName + " team",
		}),
	}
}
