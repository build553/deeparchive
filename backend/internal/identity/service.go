// Package identity implements account registration, email verification,
// login, session issuance/rotation/revocation, password recovery, and MFA.
// It is the first-party identity service referenced in docs/architecture.md
// — no external IdP is used. All cryptographic primitives come from
// maintained libraries (golang.org/x/crypto/argon2, golang-jwt/jwt); no
// custom hashing, token format, or protocol is invented here.
package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/db/generated"
	"deeparchive/internal/audit"
	"deeparchive/internal/dbtypes"
	"deeparchive/internal/mailtemplates"
	"deeparchive/internal/notifications"
)

const (
	emailVerificationTTL = 15 * time.Minute
	passwordResetTTL     = 1 * time.Hour
)

type Service struct {
	pool         *pgxpool.Pool
	queries      *generated.Queries
	sender       notifications.Sender
	accessTokens *AccessTokenIssuer
	refreshTTL   time.Duration
	logger       *slog.Logger
	audit        *audit.Recorder
	// WebURL is the web dashboard origin, used for links in emails; empty
	// (CLIs, tests) just omits the buttons.
	WebURL string
}

func NewService(pool *pgxpool.Pool, sender notifications.Sender, accessTokens *AccessTokenIssuer, refreshTTL time.Duration, logger *slog.Logger) *Service {
	queries := generated.New(pool)
	return &Service{
		pool:         pool,
		queries:      queries,
		sender:       sender,
		accessTokens: accessTokens,
		refreshTTL:   refreshTTL,
		logger:       logger,
		audit:        audit.NewRecorder(queries, logger),
	}
}

// Session is what callers outside this package need after a successful
// login or refresh: the bearer credentials plus enough session metadata to
// render a "your active sessions" list without a second round trip.
type IssuedSession struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
	SessionID        uuid.UUID
}

// Register creates a new account and emits an email-verification token.
// If the email is already registered, no new account is created and no
// verification link is sent — but Register still returns success so the
// HTTP layer can give identical responses either way, preventing account
// enumeration via the registration endpoint. An audit event distinguishes
// the two outcomes for operators without exposing that distinction to the
// caller.
func (s *Service) Register(ctx context.Context, email, password, displayName string) error {
	return s.RegisterWithTerms(ctx, email, password, displayName, "")
}

// RegisterWithTerms is Register plus a record of which legal-document
// version the person agreed to (and when). An empty termsVersion records
// nothing; seed data and internal callers use that.
//
// An email whose owner never verified it within emailVerificationTTL is
// "reclaimable": signing up again with it replaces the stale, unverified
// account instead of being swallowed as a duplicate, so a typo or an
// abandoned signup never locks the address.
func (s *Service) RegisterWithTerms(ctx context.Context, email, password, displayName, termsVersion string) error {
	if err := ValidatePasswordPolicy(password); err != nil {
		return err
	}

	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}

	create := func() (generated.User, error) {
		return s.queries.CreateUser(ctx, generated.CreateUserParams{
			Email:        email,
			PasswordHash: hash,
			DisplayName:  displayName,
		})
	}
	user, err := create()
	if err != nil && isUniqueViolation(err) {
		n, delErr := s.queries.DeleteReclaimableUnverifiedUser(ctx, email)
		if delErr != nil {
			return fmt.Errorf("reclaiming unverified email: %w", delErr)
		}
		if n > 0 {
			s.logger.Info("register_reclaimed_unverified_email")
			user, err = create()
		}
	}
	if err != nil {
		if isUniqueViolation(err) {
			s.logger.Info("register_duplicate_email_suppressed")
			s.audit.Record(ctx, audit.Event{
				EventType: "auth.register.duplicate_suppressed",
				Metadata:  map[string]any{"email": email},
			})
			return nil
		}
		return fmt.Errorf("creating user: %w", err)
	}

	if termsVersion != "" {
		if err := s.queries.SetUserTermsAccepted(ctx, generated.SetUserTermsAcceptedParams{
			ID: user.ID, TermsVersion: dbtypes.Text(termsVersion),
		}); err != nil {
			_ = s.queries.DeleteUserByID(ctx, user.ID)
			return fmt.Errorf("recording terms acceptance: %w", err)
		}
	}

	userID := dbtypes.FromUUID(user.ID)
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID,
		EventType:   "auth.register",
		TargetType:  "user",
		TargetID:    &userID,
	})

	// The account only counts as created once its verification email is
	// actually out: on a send failure, remove it so the person can simply
	// sign up again instead of being stuck behind a half-made account that
	// the duplicate-email suppression above would then silently ignore.
	if err := s.issueEmailVerification(ctx, userID, user.Email); err != nil {
		if delErr := s.queries.DeleteUserByID(ctx, user.ID); delErr != nil {
			s.logger.Error("register_rollback_failed", "error", delErr)
		}
		return fmt.Errorf("sending verification email: %w", err)
	}
	return nil
}

func (s *Service) issueEmailVerification(ctx context.Context, userID uuid.UUID, email string) error {
	token, err := generateOpaqueToken()
	if err != nil {
		return fmt.Errorf("generating verification token: %w", err)
	}
	_, err = s.queries.CreateEmailVerificationToken(ctx, generated.CreateEmailVerificationTokenParams{
		UserID:    dbtypes.UUID(userID),
		TokenHash: hashToken(token),
		ExpiresAt: dbtypes.Timestamptz(time.Now().Add(emailVerificationTTL)),
	})
	if err != nil {
		return fmt.Errorf("storing verification token: %w", err)
	}
	msg := mailtemplates.Verify(s.WebURL, token)
	msg.To = email
	return s.sender.Send(ctx, msg)
}

// ResendVerification behaves like Register with respect to enumeration: it
// returns nil whether or not the email belongs to a real, unverified
// account.
func (s *Service) ResendVerification(ctx context.Context, email string) error {
	user, err := s.queries.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("looking up user: %w", err)
	}
	if user.EmailVerifiedAt.Valid {
		return nil
	}
	return s.issueEmailVerification(ctx, dbtypes.FromUUID(user.ID), user.Email)
}

func (s *Service) VerifyEmail(ctx context.Context, token string) error {
	rec, err := s.queries.GetValidEmailVerificationToken(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidOrExpired
		}
		return fmt.Errorf("looking up verification token: %w", err)
	}
	if err := s.queries.MarkEmailVerificationTokenUsed(ctx, rec.ID); err != nil {
		return fmt.Errorf("marking token used: %w", err)
	}
	if err := s.queries.MarkEmailVerified(ctx, rec.UserID); err != nil {
		return fmt.Errorf("marking email verified: %w", err)
	}
	verifiedUserID := dbtypes.FromUUID(rec.UserID)
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &verifiedUserID,
		EventType:   "auth.email_verified",
		TargetType:  "user",
		TargetID:    &verifiedUserID,
	})
	// Best effort: a failed welcome email must not undo a successful
	// verification.
	if u, err := s.queries.GetUserByID(ctx, rec.UserID); err == nil {
		msg := mailtemplates.Welcome(s.WebURL, u.DisplayName)
		msg.To = u.Email
		if err := s.sender.Send(ctx, msg); err != nil {
			s.logger.Error("welcome_email_failed", "error", err)
		}
	}
	return nil
}

type LoginParams struct {
	Email      string
	Password   string
	MFACode    string // empty if not supplied
	UserAgent  string
	RemoteAddr string // IP only, no port
}

// Login authenticates a user and, on success, issues a new session. It
// returns ErrInvalidCredentials for both "no such user" and "wrong
// password" so the response is identical either way. It returns
// ErrMFARequired (distinct from invalid credentials) only after the
// password has already been verified correct, which does not help an
// attacker who does not already know the password.
func (s *Service) Login(ctx context.Context, p LoginParams) (*IssuedSession, error) {
	user, err := s.queries.GetUserByEmail(ctx, p.Email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Burn roughly the same wall-clock time as a real password
			// verification to reduce (not eliminate) timing-based user
			// enumeration.
			_, _ = VerifyPassword(decoyHash, p.Password)
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("looking up user: %w", err)
	}

	if user.LockedUntil.Valid && user.LockedUntil.Time.After(time.Now()) {
		return nil, ErrAccountLocked
	}

	ok, err := VerifyPassword(user.PasswordHash, p.Password)
	if err != nil {
		return nil, fmt.Errorf("verifying password: %w", err)
	}
	if !ok {
		if _, err := s.queries.RecordFailedLogin(ctx, user.ID); err != nil {
			s.logger.Error("record_failed_login_error", "error", err)
		}
		userID := dbtypes.FromUUID(user.ID)
		s.audit.Record(ctx, audit.Event{
			ActorUserID: &userID, EventType: "auth.login_failed", TargetType: "user", TargetID: &userID,
			Metadata: map[string]any{"reason": "wrong_password"}, IPAddress: p.RemoteAddr,
		})
		return nil, ErrInvalidCredentials
	}

	if user.Status == "suspended" {
		return nil, ErrAccountSuspended
	}
	if !user.EmailVerifiedAt.Valid {
		return nil, ErrEmailNotVerified
	}
	if user.Status == "deactivated" {
		return s.beginRecovery(ctx, user, p.RemoteAddr)
	}

	if user.MfaEnabled {
		if p.MFACode == "" {
			return nil, ErrMFARequired
		}
		valid, err := s.verifyMFACode(ctx, dbtypes.FromUUID(user.ID), p.MFACode)
		if err != nil {
			return nil, err
		}
		if !valid {
			userID := dbtypes.FromUUID(user.ID)
			s.audit.Record(ctx, audit.Event{
				ActorUserID: &userID, EventType: "auth.login_failed", TargetType: "user", TargetID: &userID,
				Metadata: map[string]any{"reason": "invalid_mfa_code"}, IPAddress: p.RemoteAddr,
			})
			return nil, ErrInvalidCredentials
		}
	}

	if err := s.queries.ResetFailedLoginCount(ctx, user.ID); err != nil {
		s.logger.Error("reset_failed_login_count_error", "error", err)
	}

	userID := dbtypes.FromUUID(user.ID)

	// Platform admins get a mandatory emailed one-time code on top of
	// whatever they just completed above (password, and TOTP if
	// enrolled) — password+TOTP alone is "something you know" twice
	// over if the authenticator is on the same phone as the inbox, so
	// the email OTP forces a genuinely separate channel for the
	// account that can reach every other account.
	if user.PlatformRole == "admin" {
		challenge, err := s.beginEmailOTP(ctx, userID, user.Email, otpPurposeAdminLogin, 0)
		if err != nil {
			return nil, err
		}
		s.audit.Record(ctx, audit.Event{
			ActorUserID: &userID, EventType: "auth.admin_otp_sent", TargetType: "user", TargetID: &userID,
			IPAddress: p.RemoteAddr,
		})
		return nil, challenge
	}

	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, EventType: "auth.login", TargetType: "user", TargetID: &userID,
		IPAddress: p.RemoteAddr,
	})

	return s.createSession(ctx, userID, uuid.New(), p.UserAgent, p.RemoteAddr)
}

// beginRecovery handles a correct password on a deactivated account. Inside
// RecoveryWindow it emails a one-time code; entering it reactivates the
// account (see VerifyLoginOTP). After the window the account stays closed.
func (s *Service) beginRecovery(ctx context.Context, user generated.User, remoteAddr string) (*IssuedSession, error) {
	if !user.DeactivatedAt.Valid {
		// Deactivated before recovery existed: treat as just now.
		user.DeactivatedAt = dbtypes.Timestamptz(time.Now())
		_ = s.queries.SetUserDeactivatedAt(ctx, generated.SetUserDeactivatedAtParams{ID: user.ID, DeactivatedAt: user.DeactivatedAt})
	}
	left := time.Until(user.DeactivatedAt.Time.Add(RecoveryWindow))
	if left <= 0 {
		return nil, ErrAccountDeactivated
	}
	days := int((left + 24*time.Hour - 1) / (24 * time.Hour))
	userID := dbtypes.FromUUID(user.ID)
	challenge, err := s.beginEmailOTP(ctx, userID, user.Email, otpPurposeRecovery, days)
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, EventType: "auth.recovery_code_sent", TargetType: "user", TargetID: &userID,
		IPAddress: remoteAddr,
	})
	return nil, challenge
}

func (s *Service) createSession(ctx context.Context, userID, familyID uuid.UUID, userAgent, remoteAddr string) (*IssuedSession, error) {
	refreshToken, err := generateOpaqueToken()
	if err != nil {
		return nil, fmt.Errorf("generating refresh token: %w", err)
	}

	session, err := s.queries.CreateSession(ctx, generated.CreateSessionParams{
		UserID:           dbtypes.UUID(userID),
		RefreshTokenHash: hashToken(refreshToken),
		FamilyID:         dbtypes.UUID(familyID),
		UserAgent:        dbtypes.Text(truncate(userAgent, 512)),
		IpAddress:        parseIP(remoteAddr),
		ExpiresAt:        dbtypes.Timestamptz(time.Now().Add(s.refreshTTL)),
	})
	if err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}

	accessToken, accessExpiresAt, err := s.accessTokens.Issue(userID, dbtypes.FromUUID(session.ID))
	if err != nil {
		return nil, fmt.Errorf("issuing access token: %w", err)
	}

	return &IssuedSession{
		AccessToken:      accessToken,
		AccessExpiresAt:  accessExpiresAt,
		RefreshToken:     refreshToken,
		RefreshExpiresAt: dbtypes.FromTimestamptz(session.ExpiresAt),
		SessionID:        dbtypes.FromUUID(session.ID),
	}, nil
}

// Refresh rotates a refresh token, returning a new access/refresh token
// pair. Presenting a token that was already rotated away (replayed) within
// its short grace window revokes the entire session family, since it
// indicates the token chain may have been intercepted.
func (s *Service) Refresh(ctx context.Context, refreshToken, userAgent, remoteAddr string) (*IssuedSession, error) {
	hash := hashToken(refreshToken)

	session, err := s.queries.GetSessionByRefreshTokenHash(ctx, hash)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("looking up session: %w", err)
		}
		if replay, rerr := s.queries.GetSessionByPreviousRefreshTokenHash(ctx, dbtypes.Text(hash)); rerr == nil {
			s.logger.Warn("refresh_token_replay_detected", "family_id", dbtypes.FromUUID(replay.FamilyID))
			if err := s.queries.RevokeSessionFamily(ctx, replay.FamilyID); err != nil {
				s.logger.Error("revoke_session_family_error", "error", err)
			}
			return nil, ErrInvalidOrExpired
		}
		return nil, ErrInvalidOrExpired
	}

	if session.RevokedAt.Valid || dbtypes.FromTimestamptz(session.ExpiresAt).Before(time.Now()) {
		return nil, ErrInvalidOrExpired
	}

	newRefreshToken, err := generateOpaqueToken()
	if err != nil {
		return nil, fmt.Errorf("generating refresh token: %w", err)
	}

	rotated, err := s.queries.RotateSession(ctx, generated.RotateSessionParams{
		ID:               session.ID,
		RefreshTokenHash: hashToken(newRefreshToken),
	})
	if err != nil {
		return nil, fmt.Errorf("rotating session: %w", err)
	}

	accessToken, accessExpiresAt, err := s.accessTokens.Issue(dbtypes.FromUUID(rotated.UserID), dbtypes.FromUUID(rotated.ID))
	if err != nil {
		return nil, fmt.Errorf("issuing access token: %w", err)
	}

	return &IssuedSession{
		AccessToken:      accessToken,
		AccessExpiresAt:  accessExpiresAt,
		RefreshToken:     newRefreshToken,
		RefreshExpiresAt: dbtypes.FromTimestamptz(rotated.ExpiresAt),
		SessionID:        dbtypes.FromUUID(rotated.ID),
	}, nil
}

func (s *Service) Logout(ctx context.Context, userID, sessionID uuid.UUID) error {
	if err := s.queries.RevokeSession(ctx, dbtypes.UUID(sessionID)); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, EventType: "auth.logout", TargetType: "session", TargetID: &sessionID,
	})
	return nil
}

func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID) error {
	if err := s.queries.RevokeAllSessionsForUser(ctx, dbtypes.UUID(userID)); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, EventType: "auth.logout_all", TargetType: "user", TargetID: &userID,
	})
	return nil
}

func (s *Service) RevokeOtherSessions(ctx context.Context, userID, keepSessionID uuid.UUID) error {
	err := s.queries.RevokeAllOtherSessionsForUser(ctx, generated.RevokeAllOtherSessionsForUserParams{
		UserID: dbtypes.UUID(userID),
		ID:     dbtypes.UUID(keepSessionID),
	})
	if err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, EventType: "auth.sessions_revoke_others", TargetType: "user", TargetID: &userID,
	})
	return nil
}

type SessionSummary struct {
	ID         uuid.UUID `json:"id"`
	UserAgent  string    `json:"user_agent"`
	IPAddress  string    `json:"ip_address"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (s *Service) ListSessions(ctx context.Context, userID uuid.UUID) ([]SessionSummary, error) {
	rows, err := s.queries.ListActiveSessionsForUser(ctx, dbtypes.UUID(userID))
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	out := make([]SessionSummary, 0, len(rows))
	for _, r := range rows {
		ip := ""
		if r.IpAddress != nil {
			ip = r.IpAddress.String()
		}
		out = append(out, SessionSummary{
			ID:         dbtypes.FromUUID(r.ID),
			UserAgent:  dbtypes.FromText(r.UserAgent),
			IPAddress:  ip,
			CreatedAt:  dbtypes.FromTimestamptz(r.CreatedAt),
			LastUsedAt: dbtypes.FromTimestamptz(r.LastUsedAt),
			ExpiresAt:  dbtypes.FromTimestamptz(r.ExpiresAt),
		})
	}
	return out, nil
}

// RevokeSessionForUser revokes a specific session, but only if it belongs
// to the given user — callers must never be able to revoke another user's
// session by guessing a session ID.
func (s *Service) RevokeSessionForUser(ctx context.Context, userID, sessionID uuid.UUID) error {
	session, err := s.queries.GetSessionByID(ctx, dbtypes.UUID(sessionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("looking up session: %w", err)
	}
	if dbtypes.FromUUID(session.UserID) != userID {
		return ErrSessionNotFound
	}
	if err := s.queries.RevokeSession(ctx, dbtypes.UUID(sessionID)); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, EventType: "auth.session_revoked", TargetType: "session", TargetID: &sessionID,
	})
	return nil
}

// ChangePassword requires the caller to already have verified the current
// password (enforced by the HTTP handler via reauthentication) and revokes
// every other session, since a password change is a strong signal that any
// other active session should be forced to re-authenticate.
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, currentPassword, newPassword string, keepSessionID uuid.UUID) error {
	user, err := s.queries.GetUserByID(ctx, dbtypes.UUID(userID))
	if err != nil {
		return fmt.Errorf("looking up user: %w", err)
	}
	ok, err := VerifyPassword(user.PasswordHash, currentPassword)
	if err != nil {
		return fmt.Errorf("verifying password: %w", err)
	}
	if !ok {
		return ErrInvalidCredentials
	}
	if err := ValidatePasswordPolicy(newPassword); err != nil {
		return err
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}
	if err := s.queries.UpdatePasswordHash(ctx, generated.UpdatePasswordHashParams{
		ID:           dbtypes.UUID(userID),
		PasswordHash: hash,
	}); err != nil {
		return fmt.Errorf("updating password: %w", err)
	}
	if err := s.queries.RevokeAllOtherSessionsForUser(ctx, generated.RevokeAllOtherSessionsForUserParams{
		UserID: dbtypes.UUID(userID),
		ID:     dbtypes.UUID(keepSessionID),
	}); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, EventType: "auth.password_changed", TargetType: "user", TargetID: &userID,
	})
	return nil
}

// UpdateDisplayName changes the caller's own display name. Unlike email,
// this has no verification step — display name is not used for
// authentication or as a contact point, so there's nothing to re-verify.
func (s *Service) UpdateDisplayName(ctx context.Context, userID uuid.UUID, displayName string) error {
	if displayName == "" {
		return fmt.Errorf("display name is required")
	}
	if err := s.queries.UpdateDisplayName(ctx, generated.UpdateDisplayNameParams{
		ID: dbtypes.UUID(userID), DisplayName: displayName,
	}); err != nil {
		return fmt.Errorf("updating display name: %w", err)
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, EventType: "user.display_name_updated", TargetType: "user", TargetID: &userID,
	})
	return nil
}

// DeactivateAccount is this project's "delete my account": it sets
// status='deactivated' (a value the users table already had a CHECK
// constraint for, previously unused by any code path) and revokes every
// session, rather than issuing a SQL DELETE on the users row. A hard
// delete would cascade into every device, snapshot, and chunk the
// account owns — including chunks.user_id's independent CASCADE racing
// against snapshot_chunks.chunk_id's ON DELETE RESTRICT (see
// docs/implementation-status.md's Milestone 3 limitations) — for a
// feature (irreversible account + data erasure) nothing in this project
// actually needs yet. A deactivated account can no longer log in (see
// Login's status check) but its data is not silently destroyed.
// Requires the current password, the same safeguard ChangePassword uses,
// since this is as destructive an action as this API exposes.
func (s *Service) DeactivateAccount(ctx context.Context, userID uuid.UUID, currentPassword string) error {
	user, err := s.queries.GetUserByID(ctx, dbtypes.UUID(userID))
	if err != nil {
		return fmt.Errorf("looking up user: %w", err)
	}
	ok, err := VerifyPassword(user.PasswordHash, currentPassword)
	if err != nil {
		return fmt.Errorf("verifying password: %w", err)
	}
	if !ok {
		return ErrInvalidCredentials
	}
	if err := s.queries.UpdateUserStatus(ctx, generated.UpdateUserStatusParams{
		ID: dbtypes.UUID(userID), Status: "deactivated",
	}); err != nil {
		return fmt.Errorf("deactivating account: %w", err)
	}
	if err := s.queries.SetUserDeactivatedAt(ctx, generated.SetUserDeactivatedAtParams{
		ID: dbtypes.UUID(userID), DeactivatedAt: dbtypes.Timestamptz(time.Now()),
	}); err != nil {
		return fmt.Errorf("recording deactivation time: %w", err)
	}
	if err := s.queries.RevokeAllSessionsForUser(ctx, dbtypes.UUID(userID)); err != nil {
		return fmt.Errorf("revoking sessions: %w", err)
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, EventType: "user.deactivated", TargetType: "user", TargetID: &userID,
	})
	return nil
}

// --- Operator/admin methods -------------------------------------------
//
// Everything below is called only by backend/cmd/deepctl, never by the
// HTTP API — there is no user-session authorization check because the
// caller IS the authorization boundary (whoever can run deepctl already
// has DATABASE_URL access to this database directly, the same trust
// level backend/cmd/migrate already assumes). Each still writes an audit
// event, same as every other mutating action in this codebase.

// AdminUser is the subset of a user row deepctl/the admin panel needs to
// display — deliberately not the full generated.User (never the password
// hash). JSON tags matter now that this also crosses the HTTP API (the
// admin panel), not just deepctl's plain-text tables — snake_case to
// match every other response shape in this API.
type AdminUser struct {
	ID                     uuid.UUID `json:"id"`
	Email                  string    `json:"email"`
	DisplayName            string    `json:"display_name"`
	Status                 string    `json:"status"`
	PlatformRole           string    `json:"platform_role"`
	StorageQuotaBytes      int64     `json:"storage_quota_bytes"`
	RetentionKeepSnapshots int32     `json:"retention_keep_snapshots"`
	CreatedAt              time.Time `json:"created_at"`
}

func toAdminUser(u generated.User) AdminUser {
	return AdminUser{
		ID:                     dbtypes.FromUUID(u.ID),
		Email:                  u.Email,
		DisplayName:            u.DisplayName,
		Status:                 u.Status,
		PlatformRole:           u.PlatformRole,
		StorageQuotaBytes:      u.StorageQuotaBytes,
		RetentionKeepSnapshots: u.RetentionKeepSnapshots,
		CreatedAt:              dbtypes.FromTimestamptz(u.CreatedAt),
	}
}

func (s *Service) AdminListUsers(ctx context.Context, limit, offset int32) ([]AdminUser, error) {
	rows, err := s.queries.ListUsersPage(ctx, generated.ListUsersPageParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	out := make([]AdminUser, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAdminUser(r))
	}
	return out, nil
}

func (s *Service) AdminGetUserByEmail(ctx context.Context, email string) (*AdminUser, error) {
	u, err := s.queries.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("looking up user: %w", err)
	}
	result := toAdminUser(u)
	return &result, nil
}

// AdminGetUserByID is AdminGetUserByEmail's counterpart for the web admin
// panel, which navigates by ID (from a search result row) rather than by
// a known email address.
func (s *Service) AdminGetUserByID(ctx context.Context, userID uuid.UUID) (*AdminUser, error) {
	u, err := s.queries.GetUserByID(ctx, dbtypes.UUID(userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("looking up user: %w", err)
	}
	result := toAdminUser(u)
	return &result, nil
}

// AdminSearchUsers is AdminListUsers plus an optional substring filter on
// email/display_name, with a matching total count for pagination — what
// the web admin panel's user search box needs. An empty query matches
// every row, same as AdminListUsers.
func (s *Service) AdminSearchUsers(ctx context.Context, query string, limit, offset int32) ([]AdminUser, int64, error) {
	total, err := s.queries.CountUsersMatching(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("counting users: %w", err)
	}
	rows, err := s.queries.SearchUsersPage(ctx, generated.SearchUsersPageParams{
		Column1: query, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("searching users: %w", err)
	}
	out := make([]AdminUser, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAdminUser(r))
	}
	return out, total, nil
}

// AdminSetPlatformRole changes a user's platform-wide role (none/
// operator/admin — the same CHECK-constrained values the schema defines).
// Unlike the other Admin* methods, this one is also reachable over the
// HTTP API (the admin panel), so — unlike deepctl, which trusts whoever
// already has DATABASE_URL access — callers must additionally block an
// admin from demoting themselves, which the HTTP handler enforces.
func (s *Service) AdminSetPlatformRole(ctx context.Context, actorUserID, userID uuid.UUID, role string) error {
	if role != "none" && role != "operator" && role != "admin" {
		return fmt.Errorf("role must be none, operator, or admin")
	}
	if err := s.queries.UpdateUserPlatformRole(ctx, generated.UpdateUserPlatformRoleParams{
		ID: dbtypes.UUID(userID), PlatformRole: role,
	}); err != nil {
		return fmt.Errorf("updating platform role: %w", err)
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &actorUserID, EventType: "admin.user_platform_role_set", TargetType: "user", TargetID: &userID,
		Metadata: map[string]any{"platform_role": role},
	})
	return nil
}

func (s *Service) AdminSetStorageQuota(ctx context.Context, userID uuid.UUID, bytes int64) error {
	if err := s.queries.UpdateUserStorageQuota(ctx, generated.UpdateUserStorageQuotaParams{
		ID: dbtypes.UUID(userID), StorageQuotaBytes: bytes,
	}); err != nil {
		return fmt.Errorf("updating storage quota: %w", err)
	}
	s.audit.Record(ctx, audit.Event{
		EventType: "admin.user_storage_quota_set", TargetType: "user", TargetID: &userID,
		Metadata: map[string]any{"storage_quota_bytes": bytes},
	})
	return nil
}

func (s *Service) AdminSetRetentionLimit(ctx context.Context, userID uuid.UUID, keep int32) error {
	if err := s.queries.UpdateUserRetentionLimit(ctx, generated.UpdateUserRetentionLimitParams{
		ID: dbtypes.UUID(userID), RetentionKeepSnapshots: keep,
	}); err != nil {
		return fmt.Errorf("updating retention limit: %w", err)
	}
	s.audit.Record(ctx, audit.Event{
		EventType: "admin.user_retention_limit_set", TargetType: "user", TargetID: &userID,
		Metadata: map[string]any{"retention_keep_snapshots": keep},
	})
	return nil
}

// AdminSetStatus sets a user's status directly (active/suspended/
// deactivated — the same CHECK-constrained values DeactivateAccount and
// Login's suspended check already use) and, for suspended/deactivated,
// revokes every session so the change takes effect immediately rather
// than at next token expiry.
func (s *Service) AdminSetStatus(ctx context.Context, userID uuid.UUID, status string) error {
	if status != "active" && status != "suspended" && status != "deactivated" {
		return fmt.Errorf("status must be active, suspended, or deactivated")
	}
	if err := s.queries.UpdateUserStatus(ctx, generated.UpdateUserStatusParams{
		ID: dbtypes.UUID(userID), Status: status,
	}); err != nil {
		return fmt.Errorf("updating status: %w", err)
	}
	deactivatedAt := pgtype.Timestamptz{}
	if status == "deactivated" {
		deactivatedAt = dbtypes.Timestamptz(time.Now())
	}
	if err := s.queries.SetUserDeactivatedAt(ctx, generated.SetUserDeactivatedAtParams{
		ID: dbtypes.UUID(userID), DeactivatedAt: deactivatedAt,
	}); err != nil {
		return fmt.Errorf("updating deactivation time: %w", err)
	}
	if status != "active" {
		if err := s.queries.RevokeAllSessionsForUser(ctx, dbtypes.UUID(userID)); err != nil {
			return fmt.Errorf("revoking sessions: %w", err)
		}
	}
	s.audit.Record(ctx, audit.Event{
		EventType: "admin.user_status_set", TargetType: "user", TargetID: &userID,
		Metadata: map[string]any{"status": status},
	})
	return nil
}

// ForgotPassword always returns nil regardless of whether the email
// belongs to a real account, to avoid enumeration; it only sends an email
// when the account exists.
func (s *Service) ForgotPassword(ctx context.Context, email string) error {
	user, err := s.queries.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("looking up user: %w", err)
	}
	token, err := generateOpaqueToken()
	if err != nil {
		return fmt.Errorf("generating reset token: %w", err)
	}
	if _, err := s.queries.CreatePasswordResetToken(ctx, generated.CreatePasswordResetTokenParams{
		UserID:    user.ID,
		TokenHash: hashToken(token),
		ExpiresAt: dbtypes.Timestamptz(time.Now().Add(passwordResetTTL)),
	}); err != nil {
		return fmt.Errorf("storing reset token: %w", err)
	}
	msg := mailtemplates.Reset(s.WebURL, token)
	msg.To = user.Email
	return s.sender.Send(ctx, msg)
}

// ResetPassword redeems a single-use password-reset token and revokes all
// of the account's existing sessions (any already-stolen session
// shouldn't survive a password reset).
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if err := ValidatePasswordPolicy(newPassword); err != nil {
		return err
	}
	rec, err := s.queries.GetValidPasswordResetToken(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidOrExpired
		}
		return fmt.Errorf("looking up reset token: %w", err)
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}

	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if err := q.MarkPasswordResetTokenUsed(ctx, rec.ID); err != nil {
			return err
		}
		if err := q.UpdatePasswordHash(ctx, generated.UpdatePasswordHashParams{
			ID:           rec.UserID,
			PasswordHash: hash,
		}); err != nil {
			return err
		}
		return q.RevokeAllSessionsForUser(ctx, rec.UserID)
	})
	if err != nil {
		return err
	}
	resetUserID := dbtypes.FromUUID(rec.UserID)
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &resetUserID, EventType: "auth.password_reset", TargetType: "user", TargetID: &resetUserID,
	})
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func parseIP(remoteAddr string) *netip.Addr {
	if remoteAddr == "" {
		return nil
	}
	addr, err := netip.ParseAddr(remoteAddr)
	if err != nil {
		return nil
	}
	return &addr
}

// decoyHash is a fixed, valid-format Argon2id hash with no corresponding
// real password, used only to burn comparable CPU time when a login is
// attempted against a nonexistent email (see Login).
const decoyHash = "$argon2id$v=19$m=65536,t=3,p=4$MDAwMDAwMDAwMDAwMDAwMA$MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA"
