//go:build integration

// Integration tests for internal/identity against a real, disposable
// PostgreSQL database (not mocks) per docs/testing.md and the project's
// testing policy: security-sensitive behavior — password hashing, session
// issuance/rotation/revocation, MFA, tenant/account isolation — must be
// proven against the real database, not a mocked repository.
//
// Run with: DATABASE_URL=... go test -tags=integration ./tests/integration/...
// (the schema must already be migrated; `make test-integration` does both).
package integration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"

	"deeparchive/internal/identity"
	"deeparchive/internal/notifications"
)

// capturingSender is a test-only fake (not used anywhere outside tests)
// that records sent emails so tests can extract the opaque token embedded
// in the body — exactly as a user would copy it from a real email — and
// drive the real verification/reset endpoints with it.
type capturingSender struct {
	mu   sync.Mutex
	sent []notifications.Email
	fail bool // when set, Send returns an error (simulates SMTP down)
}

func (s *capturingSender) Send(_ context.Context, email notifications.Email) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return context.DeadlineExceeded
	}
	s.sent = append(s.sent, email)
	return nil
}

func (s *capturingSender) last() notifications.Email {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent[len(s.sent)-1]
}

func (s *capturingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

func extractToken(body string) string {
	parts := strings.Fields(body)
	for i, p := range parts {
		if strings.HasSuffix(p, ":") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

const testPassword = "a-sufficiently-long-password"

type testEnv struct {
	pool   *pgxpool.Pool
	svc    *identity.Service
	sender *capturingSender
	tokens *identity.AccessTokenIssuer
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("connecting to database: %v", err)
	}
	t.Cleanup(pool.Close)

	sender := &capturingSender{}
	issuer := identity.NewAccessTokenIssuer([]byte("integration-test-signing-key-at-least-32-bytes"), 15*time.Minute)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	svc := identity.NewService(pool, sender, issuer, 30*24*time.Hour, logger)
	return &testEnv{pool: pool, svc: svc, sender: sender, tokens: issuer}
}

// userID decodes the authenticated user ID out of an issued session's
// access token, using the same signing key the test environment issued it
// with. This avoids reaching into unexported Service internals just to
// learn "whose session is this" in a test.
func (e *testEnv) userID(t *testing.T, accessToken string) uuid.UUID {
	t.Helper()
	claims, err := e.tokens.Verify(accessToken)
	if err != nil {
		t.Fatalf("decoding access token: %v", err)
	}
	return claims.UserID
}

// registerVerified signs a user up and completes email verification the way
// a person would (using the emailed token), for tests that only need a
// usable account.
func (e *testEnv) registerVerified(ctx context.Context, email, password, name string) error {
	if err := e.svc.Register(ctx, email, password, name); err != nil {
		return err
	}
	return e.svc.VerifyEmail(ctx, extractToken(e.sender.last().Body))
}

func uniqueEmail(t *testing.T) string {
	return fmt.Sprintf("test-%s-%d@example.test", strings.ToLower(t.Name()), time.Now().UnixNano())
}

func TestRegisterVerifyLogin_FullFlow(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := env.svc.Register(ctx, email, testPassword, "Test User"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if env.sender.count() != 1 {
		t.Fatalf("expected exactly one verification email, got %d", env.sender.count())
	}

	token := extractToken(env.sender.last().Body)
	if token == "" {
		t.Fatal("could not extract verification token from email body")
	}

	if err := env.svc.VerifyEmail(ctx, token); err != nil {
		t.Fatalf("verify email: %v", err)
	}
	if err := env.svc.VerifyEmail(ctx, token); err == nil {
		t.Fatal("expected reused verification token to be rejected")
	}

	session, err := env.svc.Login(ctx, identity.LoginParams{
		Email: email, Password: testPassword, UserAgent: "test-agent", RemoteAddr: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if session.AccessToken == "" || session.RefreshToken == "" {
		t.Fatal("expected non-empty tokens")
	}
}

func TestRegister_DuplicateEmail_DoesNotLeakOrSendSecondEmail(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := env.svc.Register(ctx, email, testPassword, "First"); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := env.svc.Register(ctx, email, "a-different-long-password", "Second"); err != nil {
		t.Fatalf("second register (duplicate) should not error: %v", err)
	}
	if env.sender.count() != 1 {
		t.Fatalf("expected the duplicate registration to suppress a second email, got %d emails", env.sender.count())
	}
}

func TestLogin_UnknownEmailAndWrongPassword_ReturnSameError(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := env.registerVerified(ctx, email, testPassword, "Test User"); err != nil {
		t.Fatalf("register: %v", err)
	}

	_, errUnknown := env.svc.Login(ctx, identity.LoginParams{Email: "nonexistent-" + email, Password: "whatever12345"})
	_, errWrongPw := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: "wrong-password-value"})

	if errUnknown != identity.ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials for unknown email, got %v", errUnknown)
	}
	if errWrongPw != identity.ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials for wrong password, got %v", errWrongPw)
	}
}

func TestRefresh_RotatesToken_AndDetectsReplay(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := env.registerVerified(ctx, email, testPassword, "Test User"); err != nil {
		t.Fatalf("register: %v", err)
	}
	session, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	rotated, err := env.svc.Refresh(ctx, session.RefreshToken, "", "")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if rotated.RefreshToken == session.RefreshToken {
		t.Fatal("expected a new refresh token after rotation")
	}

	if _, err := env.svc.Refresh(ctx, rotated.RefreshToken, "", ""); err != nil {
		t.Fatalf("refresh with rotated token should succeed: %v", err)
	}

	// Replaying the ORIGINAL (now-superseded) token should be detected and
	// revoke the whole session family — including the branch that
	// continued legitimately.
	if _, err := env.svc.Refresh(ctx, session.RefreshToken, "", ""); err == nil {
		t.Fatal("expected replay of superseded refresh token to fail")
	}
	if _, err := env.svc.Refresh(ctx, rotated.RefreshToken, "", ""); err == nil {
		t.Fatal("expected the whole session family to be revoked after replay detection")
	}
}

func TestLogout_RevokesSession(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := env.registerVerified(ctx, email, testPassword, "Test User"); err != nil {
		t.Fatalf("register: %v", err)
	}
	session, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	userID := env.userID(t, session.AccessToken)
	if err := env.svc.Logout(ctx, userID, session.SessionID); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := env.svc.Refresh(ctx, session.RefreshToken, "", ""); err == nil {
		t.Fatal("expected refresh to fail after logout revoked the session")
	}
}

func TestRevokeSessionForUser_CannotRevokeAnotherUsersSession(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	emailA := "a-" + uniqueEmail(t)
	emailB := "b-" + uniqueEmail(t)
	if err := env.registerVerified(ctx, emailA, testPassword, "User A"); err != nil {
		t.Fatalf("register A: %v", err)
	}
	if err := env.registerVerified(ctx, emailB, testPassword, "User B"); err != nil {
		t.Fatalf("register B: %v", err)
	}
	sessionA, err := env.svc.Login(ctx, identity.LoginParams{Email: emailA, Password: testPassword})
	if err != nil {
		t.Fatalf("login A: %v", err)
	}
	sessionB, err := env.svc.Login(ctx, identity.LoginParams{Email: emailB, Password: testPassword})
	if err != nil {
		t.Fatalf("login B: %v", err)
	}

	userBID := env.userID(t, sessionB.AccessToken)

	if err := env.svc.RevokeSessionForUser(ctx, userBID, sessionA.SessionID); err == nil {
		t.Fatal("expected cross-user session revocation to fail")
	}
	if _, err := env.svc.Refresh(ctx, sessionA.RefreshToken, "", ""); err != nil {
		t.Fatalf("user A's session should be unaffected by the denied cross-user attempt: %v", err)
	}
	if err := env.svc.RevokeSessionForUser(ctx, userBID, sessionB.SessionID); err != nil {
		t.Fatalf("user B revoking their own session should succeed: %v", err)
	}
}

func TestMFA_EnrollConfirmAndLoginRequiresCode(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := env.registerVerified(ctx, email, testPassword, "Test User"); err != nil {
		t.Fatalf("register: %v", err)
	}
	initialSession, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("login before mfa enabled: %v", err)
	}
	userID := env.userID(t, initialSession.AccessToken)

	enrollment, err := env.svc.EnrollMFA(ctx, userID, email)
	if err != nil {
		t.Fatalf("enroll mfa: %v", err)
	}
	if enrollment.Secret == "" {
		t.Fatal("expected a non-empty TOTP secret")
	}

	code, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatalf("generating totp code: %v", err)
	}
	recoveryCodes, err := env.svc.ConfirmMFA(ctx, userID, code)
	if err != nil {
		t.Fatalf("confirm mfa: %v", err)
	}
	if len(recoveryCodes) == 0 {
		t.Fatal("expected recovery codes to be returned")
	}

	if _, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword}); err != identity.ErrMFARequired {
		t.Fatalf("expected ErrMFARequired, got %v", err)
	}

	code2, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatalf("generating totp code: %v", err)
	}
	if _, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword, MFACode: code2}); err != nil {
		t.Fatalf("login with valid mfa code should succeed: %v", err)
	}

	if _, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword, MFACode: recoveryCodes[0]}); err != nil {
		t.Fatalf("login with recovery code should succeed: %v", err)
	}
	if _, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword, MFACode: recoveryCodes[0]}); err == nil {
		t.Fatal("expected a reused recovery code to be rejected")
	}
}

func TestChangePassword_RevokesOtherSessions(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := env.registerVerified(ctx, email, testPassword, "Test User"); err != nil {
		t.Fatalf("register: %v", err)
	}
	sessionA, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("login A: %v", err)
	}
	sessionB, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("login B: %v", err)
	}

	userID := env.userID(t, sessionA.AccessToken)

	if err := env.svc.ChangePassword(ctx, userID, testPassword, "a-new-sufficiently-long-password", sessionA.SessionID); err != nil {
		t.Fatalf("change password: %v", err)
	}

	if _, err := env.svc.Refresh(ctx, sessionA.RefreshToken, "", ""); err != nil {
		t.Fatalf("session A (kept) should remain usable: %v", err)
	}
	if _, err := env.svc.Refresh(ctx, sessionB.RefreshToken, "", ""); err == nil {
		t.Fatal("expected session B to be revoked by the password change")
	}
}

func TestResetPassword_RevokesAllSessions(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := env.registerVerified(ctx, email, testPassword, "Test User"); err != nil {
		t.Fatalf("register: %v", err)
	}
	session, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if err := env.svc.ForgotPassword(ctx, email); err != nil {
		t.Fatalf("forgot password: %v", err)
	}
	resetToken := extractToken(env.sender.last().Body)
	if resetToken == "" {
		t.Fatal("could not extract reset token")
	}

	if err := env.svc.ResetPassword(ctx, resetToken, "a-brand-new-long-password"); err != nil {
		t.Fatalf("reset password: %v", err)
	}
	if _, err := env.svc.Refresh(ctx, session.RefreshToken, "", ""); err == nil {
		t.Fatal("expected all sessions to be revoked after password reset")
	}
	if _, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: "a-brand-new-long-password"}); err != nil {
		t.Fatalf("login with new password should succeed: %v", err)
	}
}

func TestUpdateDisplayName_PersistsTheChange(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := env.registerVerified(ctx, email, testPassword, "Original Name"); err != nil {
		t.Fatalf("register: %v", err)
	}
	session, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	userID := env.userID(t, session.AccessToken)

	if err := env.svc.UpdateDisplayName(ctx, userID, "New Name"); err != nil {
		t.Fatalf("update display name: %v", err)
	}
	if err := env.svc.UpdateDisplayName(ctx, userID, ""); err == nil {
		t.Fatal("expected an empty display name to be rejected")
	}

	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatalf("connecting to database: %v", err)
	}
	defer pool.Close()
	var displayName string
	if err := pool.QueryRow(ctx, "SELECT display_name FROM users WHERE id = $1", userID).Scan(&displayName); err != nil {
		t.Fatalf("reading display name: %v", err)
	}
	if displayName != "New Name" {
		t.Fatalf("expected display name %q, got %q", "New Name", displayName)
	}
}

func TestDeactivateAccount_RequiresPasswordAndBlocksLoginAndRevokesSessions(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := env.registerVerified(ctx, email, testPassword, "Soon Gone"); err != nil {
		t.Fatalf("register: %v", err)
	}
	session, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	userID := env.userID(t, session.AccessToken)

	if err := env.svc.DeactivateAccount(ctx, userID, "the wrong password"); err != identity.ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials for a wrong password, got %v", err)
	}

	if err := env.svc.DeactivateAccount(ctx, userID, testPassword); err != nil {
		t.Fatalf("deactivate account: %v", err)
	}

	// Every session is revoked immediately, exactly like logout-all.
	if _, err := env.svc.Refresh(ctx, session.RefreshToken, "", ""); err == nil {
		t.Fatal("expected the session to be revoked after deactivation")
	}

	// A deactivated account cannot log straight back in: it gets an emailed
	// recovery code instead (see TestDeactivatedAccount_RecoversWithEmailedCode).
	_, err = env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	var challenge *identity.AdminOTPChallengeError
	if !errors.As(err, &challenge) || challenge.Purpose != "account_recovery" {
		t.Fatalf("expected a recovery challenge, got %v", err)
	}
}

func TestRegisterRollsBackAccountWhenVerificationEmailFails(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	env.sender.fail = true
	if err := env.svc.Register(ctx, email, testPassword, "Ann"); err == nil {
		t.Fatal("expected Register to fail when the email cannot be sent")
	}
	if _, err := env.svc.AdminGetUserByEmail(ctx, email); err == nil {
		t.Fatal("account must not exist after a failed signup")
	}

	// The same email can sign up again and gets a real, working link.
	env.sender.fail = false
	if err := env.svc.Register(ctx, email, testPassword, "Ann"); err != nil {
		t.Fatal(err)
	}
	if env.sender.count() != 1 {
		t.Fatalf("expected exactly one verification email, got %d", env.sender.count())
	}
}

func TestWelcomeEmailSentOnlyAfterVerification(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := env.svc.Register(ctx, email, testPassword, "Ann"); err != nil {
		t.Fatal(err)
	}
	if env.sender.count() != 1 || env.sender.last().Subject != "Verify your DeepArchive account" {
		t.Fatal("signup must send only the verification email")
	}
	if err := env.svc.VerifyEmail(ctx, extractToken(env.sender.last().Body)); err != nil {
		t.Fatal(err)
	}
	if env.sender.count() != 2 || env.sender.last().Subject != "Welcome to DeepArchive" {
		t.Fatalf("expected a welcome email after verification, got %d emails", env.sender.count())
	}
	if !strings.Contains(env.sender.last().HTML, "Ann") {
		t.Fatal("welcome should greet the user by name")
	}
}

func TestLogin_RequiresVerifiedEmail(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	if err := env.svc.Register(ctx, email, testPassword, "Pending"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword}); err != identity.ErrEmailNotVerified {
		t.Fatalf("expected ErrEmailNotVerified, got %v", err)
	}
	// A wrong password must not reveal that the account exists unverified.
	if _, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: "wrong-password-value"}); err != identity.ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestRegister_ReclaimsEmailWhoseVerificationLapsed(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := env.svc.Register(ctx, email, testPassword, "Typo Squatter"); err != nil {
		t.Fatal(err)
	}
	// Still inside the 15-minute window: a second signup is suppressed.
	if err := env.svc.Register(ctx, email, "a-different-long-password", "Real Owner"); err != nil {
		t.Fatal(err)
	}
	if env.sender.count() != 1 {
		t.Fatalf("signup inside the window must not send a second email, got %d", env.sender.count())
	}

	if _, err := env.pool.Exec(ctx, `UPDATE email_verification_tokens SET expires_at = now() - interval '1 minute'
		WHERE user_id = (SELECT id FROM users WHERE email = $1)`, email); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.Register(ctx, email, "a-different-long-password", "Real Owner"); err != nil {
		t.Fatal(err)
	}
	if env.sender.count() != 2 {
		t.Fatalf("expected a fresh verification email after the window lapsed, got %d emails", env.sender.count())
	}
	if err := env.svc.VerifyEmail(ctx, extractToken(env.sender.last().Body)); err != nil {
		t.Fatal(err)
	}
	// The new owner's password works; the squatter's does not.
	if _, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: "a-different-long-password"}); err != nil {
		t.Fatalf("new owner login: %v", err)
	}
	if _, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword}); err != identity.ErrInvalidCredentials {
		t.Fatalf("old password must stop working, got %v", err)
	}
}

func TestRegister_VerifiedEmailIsNeverReclaimed(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	if err := env.registerVerified(ctx, email, testPassword, "Owner"); err != nil {
		t.Fatal(err)
	}
	before := env.sender.count()
	if err := env.svc.Register(ctx, email, "a-different-long-password", "Intruder"); err != nil {
		t.Fatal(err)
	}
	if env.sender.count() != before {
		t.Fatal("a verified account's email must not be re-registered")
	}
	if _, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword}); err != nil {
		t.Fatalf("owner must still log in: %v", err)
	}
}

func TestDeactivatedAccount_RecoversWithEmailedCode(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	if err := env.registerVerified(ctx, email, testPassword, "Returning"); err != nil {
		t.Fatal(err)
	}
	session, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	userID := env.userID(t, session.AccessToken)
	if err := env.svc.DeactivateAccount(ctx, userID, testPassword); err != nil {
		t.Fatal(err)
	}

	// Wrong password gets no recovery email.
	before := env.sender.count()
	if _, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: "wrong-password-value"}); err != identity.ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
	if env.sender.count() != before {
		t.Fatal("no email may be sent for a wrong password")
	}

	_, err = env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	var challenge *identity.AdminOTPChallengeError
	if !errors.As(err, &challenge) || challenge.Purpose != "account_recovery" {
		t.Fatalf("expected a recovery challenge, got %v", err)
	}
	if challenge.DaysLeft != 15 {
		t.Fatalf("expected 15 days left, got %d", challenge.DaysLeft)
	}
	mail := env.sender.last()
	if mail.Subject != "Restore your DeepArchive account" || !strings.Contains(mail.HTML, "15 days") {
		t.Fatalf("unexpected recovery email %q", mail.Subject)
	}
	code := extractToken(mail.Body)

	if _, err := env.svc.VerifyLoginOTP(ctx, challenge.ChallengeID, "000000", "", ""); err == nil {
		t.Fatal("a wrong code must not restore the account")
	}
	restored, err := env.svc.VerifyLoginOTP(ctx, challenge.ChallengeID, code, "", "")
	if err != nil {
		t.Fatalf("verify recovery code: %v", err)
	}
	if env.userID(t, restored.AccessToken) != userID {
		t.Fatal("recovery must sign in the same account")
	}
	if _, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword}); err != nil {
		t.Fatalf("restored account must log in normally: %v", err)
	}
}

func TestDeactivatedAccount_CannotRecoverAfterWindow(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	if err := env.registerVerified(ctx, email, testPassword, "Too Late"); err != nil {
		t.Fatal(err)
	}
	session, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.svc.DeactivateAccount(ctx, env.userID(t, session.AccessToken), testPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE users SET deactivated_at = now() - interval '16 days' WHERE email = $1`, email); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword}); err != identity.ErrAccountDeactivated {
		t.Fatalf("expected ErrAccountDeactivated after the window, got %v", err)
	}
}
