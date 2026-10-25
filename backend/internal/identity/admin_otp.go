package identity

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"deeparchive/db/generated"
	"deeparchive/internal/audit"
	"deeparchive/internal/dbtypes"
	"deeparchive/internal/mailtemplates"
)

const (
	otpPurposeAdminLogin = "admin_login"
	otpPurposeRecovery   = "account_recovery"
	// RecoveryWindow is how long after deactivation a person can sign
	// back in and reactivate. Shown to them at deactivation time.
	RecoveryWindow = 15 * 24 * time.Hour

	adminOTPTTL         = 5 * time.Minute
	adminOTPMaxAttempts = 5
)

// AdminOTPChallengeError wraps ErrAdminOTPRequired with the challenge ID
// the client must echo back to VerifyAdminOTP. It's returned instead of
// the bare sentinel so the HTTP handler can expose ChallengeID without a
// second lookup, while errors.Is(err, ErrAdminOTPRequired) still works
// for callers that only care about the case, not the ID.
type AdminOTPChallengeError struct {
	ChallengeID uuid.UUID
	ExpiresAt   time.Time
	Purpose     string // otpPurposeAdminLogin or otpPurposeRecovery
	DaysLeft    int    // recovery only: days remaining in the recovery window
}

func (e *AdminOTPChallengeError) Error() string { return ErrAdminOTPRequired.Error() }
func (e *AdminOTPChallengeError) Unwrap() error { return ErrAdminOTPRequired }

// beginAdminOTP generates a 6-digit code, stores its hash, emails it, and
// returns the challenge error Login should return in place of a session.
// Reusing hashToken (plain SHA-256, no salt) is fine here even though a
// 6-digit code has only ~20 bits of entropy: unlike a password hash, this
// isn't the only thing standing between an attacker and the account — the
// challenge is single-use, expires in 5 minutes, and is capped at 5
// verify attempts (see VerifyAdminOTP), and anyone who can read this
// table already has database access, at which point the game is lost
// regardless of hash strength.
func (s *Service) beginEmailOTP(ctx context.Context, userID uuid.UUID, email, purpose string, daysLeft int) (*AdminOTPChallengeError, error) {
	code, err := generateNumericOTP(6)
	if err != nil {
		return nil, fmt.Errorf("generating admin otp: %w", err)
	}
	expiresAt := time.Now().Add(adminOTPTTL)
	row, err := s.queries.CreateAdminLoginOTPChallenge(ctx, generated.CreateAdminLoginOTPChallengeParams{
		UserID:    dbtypes.UUID(userID),
		CodeHash:  hashToken(code),
		ExpiresAt: dbtypes.Timestamptz(expiresAt),
		Purpose:   purpose,
	})
	if err != nil {
		return nil, fmt.Errorf("storing admin otp challenge: %w", err)
	}
	msg := mailtemplates.AdminOTP(code)
	if purpose == otpPurposeRecovery {
		msg = mailtemplates.RecoveryCode(code, daysLeft)
	}
	msg.To = email
	if err := s.sender.Send(ctx, msg); err != nil {
		return nil, fmt.Errorf("sending admin otp email: %w", err)
	}
	return &AdminOTPChallengeError{ChallengeID: dbtypes.FromUUID(row.ID), ExpiresAt: expiresAt, Purpose: purpose, DaysLeft: daysLeft}, nil
}

// VerifyLoginOTP redeems a challenge created by beginAdminOTP and, on
// success, issues a session exactly like a normal completed Login. A
// wrong code consumes one of the challenge's 5 attempts rather than
// failing the whole challenge, so a mistyped digit doesn't force the
// user to wait for a brand new email.
func (s *Service) VerifyLoginOTP(ctx context.Context, challengeID uuid.UUID, code, userAgent, remoteAddr string) (*IssuedSession, error) {
	challenge, err := s.queries.GetValidAdminLoginOTPChallenge(ctx, dbtypes.UUID(challengeID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidOrExpired
		}
		return nil, fmt.Errorf("looking up admin otp challenge: %w", err)
	}

	if challenge.Attempts >= adminOTPMaxAttempts {
		return nil, ErrInvalidOrExpired
	}

	if hashToken(code) != challenge.CodeHash {
		if _, err := s.queries.IncrementAdminLoginOTPAttempts(ctx, challenge.ID); err != nil {
			s.logger.Error("increment_admin_otp_attempts_error", "error", err)
		}
		return nil, ErrInvalidCredentials
	}

	if err := s.queries.MarkAdminLoginOTPChallengeUsed(ctx, challenge.ID); err != nil {
		return nil, fmt.Errorf("marking admin otp challenge used: %w", err)
	}

	userID := dbtypes.FromUUID(challenge.UserID)
	if challenge.Purpose == otpPurposeRecovery {
		// Proving control of the account's email reactivates it.
		if err := s.queries.UpdateUserStatus(ctx, generated.UpdateUserStatusParams{ID: challenge.UserID, Status: "active"}); err != nil {
			return nil, fmt.Errorf("reactivating account: %w", err)
		}
		if err := s.queries.SetUserDeactivatedAt(ctx, generated.SetUserDeactivatedAtParams{ID: challenge.UserID}); err != nil {
			return nil, fmt.Errorf("clearing deactivation: %w", err)
		}
		s.audit.Record(ctx, audit.Event{
			ActorUserID: &userID, EventType: "auth.account_recovered", TargetType: "user", TargetID: &userID,
			IPAddress: remoteAddr,
		})
	}
	if err := s.queries.ResetFailedLoginCount(ctx, challenge.UserID); err != nil {
		s.logger.Error("reset_failed_login_count_error", "error", err)
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, EventType: "auth.admin_otp_verified", TargetType: "user", TargetID: &userID,
		IPAddress: remoteAddr,
	})

	return s.createSession(ctx, userID, uuid.New(), userAgent, remoteAddr)
}

// generateNumericOTP returns a cryptographically random decimal code of
// the given length, zero-padded (e.g. "042918"), using crypto/rand so it
// isn't predictable the way math/rand would be.
func generateNumericOTP(digits int) (string, error) {
	max := big.NewInt(1)
	for i := 0; i < digits; i++ {
		max.Mul(max, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", digits, n), nil
}
