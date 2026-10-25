package identity

import "errors"

// Domain errors. HTTP handlers map these to status codes; they deliberately
// carry no information beyond what is safe to show across the API
// boundary (see httpserver error mapping) to avoid account enumeration and
// internal-detail leakage.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAccountLocked      = errors.New("account temporarily locked")
	ErrAccountSuspended   = errors.New("account suspended")
	ErrAccountDeactivated = errors.New("account deactivated")
	ErrEmailAlreadyExists = errors.New("an account with this email may already exist")
	ErrEmailNotVerified   = errors.New("email not verified")
	ErrInvalidOrExpired   = errors.New("invalid or expired token")
	ErrMFARequired        = errors.New("mfa verification required")
	ErrMFAAlreadyEnabled  = errors.New("mfa already enabled")
	ErrMFANotEnabled      = errors.New("mfa not enabled")
	// ErrAdminOTPRequired is wrapped by *AdminOTPChallengeError, never
	// returned bare, so callers can both errors.Is against it and pull
	// out the challenge ID via errors.As. See Login and VerifyAdminOTP.
	ErrAdminOTPRequired = errors.New("admin email otp verification required")
	ErrSessionNotFound  = errors.New("session not found")
	ErrNotFound         = errors.New("not found")
	ErrForbidden        = errors.New("forbidden")
	ErrLastOwner        = errors.New("cannot remove the last owner of an organization")
)
