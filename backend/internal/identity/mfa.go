package identity

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp/totp"

	"deeparchive/db/generated"
	"deeparchive/internal/audit"
	"deeparchive/internal/dbtypes"
)

const recoveryCodeCount = 10

// mfaSecretKey derives a 32-byte AES-256 key from the access-token signing
// key so TOTP secrets are not stored in plaintext in the database. This is
// a pragmatic MVP choice, not a KMS: rotating ACCESS_TOKEN_SIGNING_KEY
// would make existing encrypted MFA secrets unrecoverable. See
// docs/encryption.md and docs/implementation-status.md, where this is
// flagged as a limitation to replace with a dedicated, independently
// rotatable key (or a real KMS) before production use.
func (s *Service) mfaSecretKey() [32]byte {
	return sha256.Sum256(append([]byte("deeparchive-mfa-secret-v1:"), s.accessTokens.signingKey...))
}

func (s *Service) encryptMFASecret(plaintext string) ([]byte, error) {
	key := s.mfaSecretKey()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func (s *Service) decryptMFASecret(ciphertext []byte) (string, error) {
	key := s.mfaSecretKey()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, body := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting mfa secret: %w", err)
	}
	return string(plaintext), nil
}

type MFAEnrollment struct {
	Secret string // base32 secret, shown once so the user can enter it manually if they can't scan a QR code
	URI    string // otpauth:// URI for QR code rendering client-side
}

// EnrollMFA generates a new TOTP secret and stores it encrypted, but does
// not enable MFA yet — ConfirmMFA must verify a live code first so a user
// can't lock themselves out with a secret their app failed to import
// correctly.
func (s *Service) EnrollMFA(ctx context.Context, userID uuid.UUID, accountEmail string) (*MFAEnrollment, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "DeepArchive",
		AccountName: accountEmail,
	})
	if err != nil {
		return nil, fmt.Errorf("generating totp secret: %w", err)
	}

	encrypted, err := s.encryptMFASecret(key.Secret())
	if err != nil {
		return nil, fmt.Errorf("encrypting mfa secret: %w", err)
	}

	if _, err := s.queries.UpsertMFACredential(ctx, generated.UpsertMFACredentialParams{
		UserID:    dbtypes.UUID(userID),
		SecretEnc: encrypted,
	}); err != nil {
		return nil, fmt.Errorf("storing mfa credential: %w", err)
	}

	return &MFAEnrollment{Secret: key.Secret(), URI: key.String()}, nil
}

// ConfirmMFA validates a live TOTP code against the just-enrolled secret,
// enables MFA on the account, and returns a fresh set of single-use
// recovery codes (shown to the user exactly once).
func (s *Service) ConfirmMFA(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	cred, err := s.queries.GetMFACredential(ctx, dbtypes.UUID(userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMFANotEnabled
		}
		return nil, fmt.Errorf("looking up mfa credential: %w", err)
	}
	secret, err := s.decryptMFASecret(cred.SecretEnc)
	if err != nil {
		return nil, err
	}
	if !totp.Validate(code, secret) {
		return nil, ErrInvalidCredentials
	}

	if err := s.queries.ConfirmMFACredential(ctx, dbtypes.UUID(userID)); err != nil {
		return nil, fmt.Errorf("confirming mfa credential: %w", err)
	}
	if err := s.queries.SetMFAEnabled(ctx, generated.SetMFAEnabledParams{
		ID:         dbtypes.UUID(userID),
		MfaEnabled: true,
	}); err != nil {
		return nil, fmt.Errorf("enabling mfa: %w", err)
	}

	codes, err := s.generateRecoveryCodes(ctx, userID)
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, EventType: "auth.mfa_enabled", TargetType: "user", TargetID: &userID,
	})
	return codes, nil
}

func (s *Service) generateRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]string, error) {
	if err := s.queries.DeleteMFARecoveryCodesForUser(ctx, dbtypes.UUID(userID)); err != nil {
		return nil, fmt.Errorf("clearing old recovery codes: %w", err)
	}
	codes := make([]string, 0, recoveryCodeCount)
	for i := 0; i < recoveryCodeCount; i++ {
		code, err := generateOpaqueToken()
		if err != nil {
			return nil, err
		}
		code = code[:10] // shorter, still ~60 bits of entropy; easier to transcribe
		if _, err := s.queries.CreateMFARecoveryCode(ctx, generated.CreateMFARecoveryCodeParams{
			UserID:   dbtypes.UUID(userID),
			CodeHash: hashToken(code),
		}); err != nil {
			return nil, fmt.Errorf("storing recovery code: %w", err)
		}
		codes = append(codes, code)
	}
	return codes, nil
}

// DisableMFA requires the caller to have already reauthenticated (enforced
// by the HTTP handler) and removes the TOTP credential and all recovery
// codes.
func (s *Service) DisableMFA(ctx context.Context, userID uuid.UUID) error {
	if err := s.queries.DeleteMFACredential(ctx, dbtypes.UUID(userID)); err != nil {
		return fmt.Errorf("deleting mfa credential: %w", err)
	}
	if err := s.queries.DeleteMFARecoveryCodesForUser(ctx, dbtypes.UUID(userID)); err != nil {
		return fmt.Errorf("deleting recovery codes: %w", err)
	}
	if err := s.queries.SetMFAEnabled(ctx, generated.SetMFAEnabledParams{
		ID:         dbtypes.UUID(userID),
		MfaEnabled: false,
	}); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, EventType: "auth.mfa_disabled", TargetType: "user", TargetID: &userID,
	})
	return nil
}

func (s *Service) verifyMFACode(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	cred, err := s.queries.GetMFACredential(ctx, dbtypes.UUID(userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("looking up mfa credential: %w", err)
	}
	secret, err := s.decryptMFASecret(cred.SecretEnc)
	if err != nil {
		return false, err
	}
	if totp.Validate(code, secret) {
		return true, nil
	}
	// Fall back to recovery codes so a user who lost their authenticator
	// app can still complete login with a single-use code.
	return s.redeemRecoveryCode(ctx, userID, code)
}

// RedeemRecoveryCode is exported for the standalone POST
// /api/v1/auth/mfa/recovery endpoint, which redeems a code outside the
// normal login flow (e.g. a session that needs to re-prove MFA).
func (s *Service) RedeemRecoveryCode(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	return s.redeemRecoveryCode(ctx, userID, code)
}

func (s *Service) redeemRecoveryCode(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	codes, err := s.queries.ListUnusedMFARecoveryCodes(ctx, dbtypes.UUID(userID))
	if err != nil {
		return false, fmt.Errorf("listing recovery codes: %w", err)
	}
	hash := hashToken(code)
	for _, c := range codes {
		if c.CodeHash == hash {
			if err := s.queries.MarkMFARecoveryCodeUsed(ctx, c.ID); err != nil {
				return false, fmt.Errorf("marking recovery code used: %w", err)
			}
			return true, nil
		}
	}
	return false, nil
}
