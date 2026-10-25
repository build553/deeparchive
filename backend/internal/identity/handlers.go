package identity

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"deeparchive/internal/dbtypes"
	"deeparchive/internal/httpx"
	"deeparchive/internal/observability"
)

// Handlers exposes chi-mountable HTTP handlers for every identity route.
// Each handler does request parsing/validation and status-code mapping
// only; all business logic lives in Service so it stays testable without
// an HTTP server.
type Handlers struct {
	svc          *Service
	webPublicURL string
}

func NewHandlers(svc *Service, webPublicURL string) *Handlers {
	return &Handlers{svc: svc, webPublicURL: webPublicURL}
}

// Mount registers every identity route on r. Routes requiring
// authentication are mounted in a sub-group using svc.Authenticate.
func (h *Handlers) Mount(r chi.Router, authRateLimit, deviceTokenRateLimit func(http.Handler) http.Handler) {
	r.Route("/auth", func(r chi.Router) {
		r.With(authRateLimit).Post("/register", h.register)
		r.Post("/verify-email", h.verifyEmail)
		r.With(authRateLimit).Post("/resend-verification", h.resendVerification)
		r.With(authRateLimit).Post("/login", h.login)
		r.With(authRateLimit).Post("/login/otp/verify", h.verifyAdminOTP)
		r.With(authRateLimit).Post("/login/admin-otp/verify", h.verifyAdminOTP) // legacy alias
		r.With(authRateLimit).Post("/refresh", h.refresh)
		r.With(authRateLimit).Post("/password/forgot", h.forgotPassword)
		r.With(authRateLimit).Post("/password/reset", h.resetPassword)

		r.Route("/device", func(r chi.Router) {
			r.With(authRateLimit).Post("/code", h.deviceAuthCode)
			// No authRateLimit here on purpose: a well-behaved CLI polls
			// this every few seconds for up to 15 minutes (far more calls
			// than the 20/min login budget allows), and it's already
			// self-throttled by PollDeviceAuth's own interval/slow_down
			// check — the correct rate limit for *this* endpoint, not the
			// generic per-IP one meant for login/password attempts.
			r.With(deviceTokenRateLimit).Post("/token", h.deviceAuthToken)

			r.Group(func(r chi.Router) {
				r.Use(h.svc.Authenticate)
				r.Get("/verify", h.deviceAuthVerify)
				r.Post("/approve", h.deviceAuthApprove)
				r.Post("/deny", h.deviceAuthDeny)
			})
		})

		r.Group(func(r chi.Router) {
			r.Use(h.svc.Authenticate)
			r.Post("/logout", h.logout)
			r.Post("/logout-all", h.logoutAll)
			r.Post("/password/change", h.changePassword)
			r.Post("/mfa/enroll", h.enrollMFA)
			r.Post("/mfa/confirm", h.confirmMFA)
			r.Delete("/mfa", h.disableMFA)
			r.Post("/mfa/recovery", h.redeemRecoveryCode)
			r.Get("/sessions", h.listSessions)
			r.Delete("/sessions/{sessionID}", h.revokeSession)
			r.Post("/sessions/revoke-others", h.revokeOtherSessions)
		})
	})

	r.Group(func(r chi.Router) {
		r.Use(h.svc.Authenticate)
		r.Get("/me", h.me)
		r.Patch("/me", h.updateMe)
		r.Delete("/me", h.deleteMe)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type registerRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	// Both must be true: the person agreed to the Terms and Privacy
	// Policy, and confirmed they are at least 13 years old.
	TermsAccepted bool `json:"terms_accepted"`
	AgeConfirmed  bool `json:"age_confirmed"`
}

// TermsVersion identifies the Terms/Privacy text people agree to at
// signup. Bump it whenever either document changes materially; the value
// stored on each user shows which version they accepted.
const TermsVersion = "2026-10-25"

func (h *Handlers) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	if req.Email == "" || req.Password == "" || req.DisplayName == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "email, password, and display_name are required")
		return
	}

	if !req.TermsAccepted || !req.AgeConfirmed {
		httpx.WriteError(w, http.StatusBadRequest, "consent_required", "you must accept the Terms and Privacy Policy and confirm you are at least 13 years old")
		return
	}

	if err := h.svc.RegisterWithTerms(r.Context(), req.Email, req.Password, req.DisplayName, TermsVersion); err != nil {
		if errors.Is(err, ErrPasswordPolicy) {
			httpx.WriteError(w, http.StatusBadRequest, "weak_password", err.Error())
			return
		}
		writeInternalError(w, r, err)
		return
	}

	// Identical response whether or not the email was already registered —
	// see Service.Register.
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{
		"message": "if the email is valid, a verification link has been sent",
	})
}

type verifyEmailRequest struct {
	Token string `json:"token"`
}

func (h *Handlers) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var req verifyEmailRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Token == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	if err := h.svc.VerifyEmail(r.Context(), req.Token); err != nil {
		if errors.Is(err, ErrInvalidOrExpired) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_or_expired_token", "this verification link is invalid or has expired")
			return
		}
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "email verified"})
}

type resendVerificationRequest struct {
	Email string `json:"email"`
}

func (h *Handlers) resendVerification(w http.ResponseWriter, r *http.Request) {
	var req resendVerificationRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Email == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "email is required")
		return
	}
	if err := h.svc.ResendVerification(r.Context(), req.Email); err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{
		"message": "if the email belongs to an unverified account, a verification link has been sent",
	})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	MFACode  string `json:"mfa_code,omitempty"`
}

type sessionResponse struct {
	AccessToken      string `json:"access_token"`
	AccessExpiresAt  string `json:"access_expires_at"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresAt string `json:"refresh_expires_at"`
}

func (h *Handlers) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Email == "" || req.Password == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "email and password are required")
		return
	}

	session, err := h.svc.Login(r.Context(), LoginParams{
		Email:      req.Email,
		Password:   req.Password,
		MFACode:    req.MFACode,
		UserAgent:  r.UserAgent(),
		RemoteAddr: clientIP(r),
	})
	if err != nil {
		var otpChallenge *AdminOTPChallengeError
		switch {
		case errors.As(err, &otpChallenge):
			code, msg := "admin_otp_required", "a one-time code was emailed to this account; enter it to continue"
			if otpChallenge.Purpose == otpPurposeRecovery {
				code, msg = "account_recovery_required", "this account was deactivated; a code was emailed to restore it"
			}
			httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{
				"error":        map[string]string{"code": code, "message": msg},
				"challenge_id": otpChallenge.ChallengeID,
				"expires_at":   otpChallenge.ExpiresAt,
				"days_left":    otpChallenge.DaysLeft,
			})
		case errors.Is(err, ErrEmailNotVerified):
			httpx.WriteError(w, http.StatusForbidden, "email_not_verified", "verify your email before signing in; check your inbox or request a new link")
		case errors.Is(err, ErrMFARequired):
			httpx.WriteError(w, http.StatusUnauthorized, "mfa_required", "mfa verification code required")
		case errors.Is(err, ErrAccountLocked):
			httpx.WriteError(w, http.StatusUnauthorized, "account_locked", "account temporarily locked due to repeated failed logins")
		case errors.Is(err, ErrAccountSuspended):
			httpx.WriteError(w, http.StatusForbidden, "account_suspended", "this account has been suspended")
		case errors.Is(err, ErrAccountDeactivated):
			httpx.WriteError(w, http.StatusForbidden, "account_deactivated", "this account was deactivated more than 15 days ago and can no longer be restored")
		case errors.Is(err, ErrInvalidCredentials):
			httpx.WriteError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		default:
			writeInternalError(w, r, err)
		}
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toSessionResponse(session))
}

type adminOTPVerifyRequest struct {
	ChallengeID string `json:"challenge_id"`
	Code        string `json:"code"`
}

func (h *Handlers) verifyAdminOTP(w http.ResponseWriter, r *http.Request) {
	var req adminOTPVerifyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.ChallengeID == "" || req.Code == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "challenge_id and code are required")
		return
	}
	challengeID, err := uuid.Parse(req.ChallengeID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "challenge_id is not a valid id")
		return
	}
	session, err := h.svc.VerifyLoginOTP(r.Context(), challengeID, req.Code, r.UserAgent(), clientIP(r))
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidOrExpired):
			httpx.WriteError(w, http.StatusUnauthorized, "invalid_or_expired_otp", "this code has expired or was already used; log in again to get a new one")
		case errors.Is(err, ErrInvalidCredentials):
			httpx.WriteError(w, http.StatusUnauthorized, "invalid_otp", "incorrect code")
		default:
			writeInternalError(w, r, err)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toSessionResponse(session))
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (h *Handlers) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.RefreshToken == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}
	session, err := h.svc.Refresh(r.Context(), req.RefreshToken, r.UserAgent(), clientIP(r))
	if err != nil {
		if errors.Is(err, ErrInvalidOrExpired) {
			httpx.WriteError(w, http.StatusUnauthorized, "invalid_or_expired_token", "refresh token is invalid or expired")
			return
		}
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toSessionResponse(session))
}

type deviceAuthCodeRequest struct {
	ClientName string `json:"client_name"`
}

type deviceAuthCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

func (h *Handlers) deviceAuthCode(w http.ResponseWriter, r *http.Request) {
	var req deviceAuthCodeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.ClientName == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "client_name is required")
		return
	}
	start, err := h.svc.StartDeviceAuth(r.Context(), req.ClientName)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	verifyURI := h.webPublicURL + "/device"
	httpx.WriteJSON(w, http.StatusOK, deviceAuthCodeResponse{
		DeviceCode:              start.DeviceCode,
		UserCode:                start.UserCode,
		VerificationURI:         verifyURI,
		VerificationURIComplete: verifyURI + "?code=" + url.QueryEscape(start.UserCode),
		ExpiresIn:               int(time.Until(start.ExpiresAt).Seconds()),
		Interval:                start.Interval,
	})
}

type deviceAuthTokenRequest struct {
	DeviceCode string `json:"device_code"`
}

func (h *Handlers) deviceAuthToken(w http.ResponseWriter, r *http.Request) {
	var req deviceAuthTokenRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.DeviceCode == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "device_code is required")
		return
	}
	session, err := h.svc.PollDeviceAuth(r.Context(), req.DeviceCode, r.UserAgent(), clientIP(r))
	if err != nil {
		switch {
		case errors.Is(err, ErrDeviceAuthPending):
			httpx.WriteError(w, http.StatusBadRequest, "authorization_pending", "not yet approved — keep polling")
		case errors.Is(err, ErrDeviceAuthSlowDown):
			httpx.WriteError(w, http.StatusBadRequest, "slow_down", "polling too fast — wait for the full interval")
		case errors.Is(err, ErrDeviceAuthDenied):
			httpx.WriteError(w, http.StatusBadRequest, "access_denied", "the request was denied")
		case errors.Is(err, ErrDeviceAuthExpired):
			httpx.WriteError(w, http.StatusBadRequest, "expired_token", "this code expired or was already used — start over")
		default:
			writeInternalError(w, r, err)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toSessionResponse(session))
}

func (h *Handlers) deviceAuthVerify(w http.ResponseWriter, r *http.Request) {
	userCode := r.URL.Query().Get("user_code")
	if userCode == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "user_code is required")
		return
	}
	info, err := h.svc.VerifyDeviceUserCode(r.Context(), userCode)
	if err != nil {
		if errors.Is(err, ErrDeviceAuthExpired) {
			httpx.WriteError(w, http.StatusNotFound, "invalid_or_expired_code", "this code is invalid or has expired")
			return
		}
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"client_name": info.ClientName,
		"expires_at":  info.ExpiresAt,
	})
}

type deviceAuthDecisionRequest struct {
	UserCode string `json:"user_code"`
}

func (h *Handlers) deviceAuthApprove(w http.ResponseWriter, r *http.Request) {
	var req deviceAuthDecisionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.UserCode == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "user_code is required")
		return
	}
	if err := h.svc.ApproveDeviceAuth(r.Context(), req.UserCode, UserIDFromContext(r.Context())); err != nil {
		if errors.Is(err, ErrDeviceAuthExpired) {
			httpx.WriteError(w, http.StatusNotFound, "invalid_or_expired_code", "this code is invalid or has expired")
			return
		}
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "approved"})
}

func (h *Handlers) deviceAuthDeny(w http.ResponseWriter, r *http.Request) {
	var req deviceAuthDecisionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.UserCode == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "user_code is required")
		return
	}
	if err := h.svc.DenyDeviceAuth(r.Context(), req.UserCode, UserIDFromContext(r.Context())); err != nil {
		if errors.Is(err, ErrDeviceAuthExpired) {
			httpx.WriteError(w, http.StatusNotFound, "invalid_or_expired_code", "this code is invalid or has expired")
			return
		}
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "denied"})
}

func (h *Handlers) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Logout(r.Context(), UserIDFromContext(r.Context()), SessionIDFromContext(r.Context())); err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

func (h *Handlers) logoutAll(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.LogoutAll(r.Context(), UserIDFromContext(r.Context())); err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "all sessions revoked"})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *Handlers) changePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.CurrentPassword == "" || req.NewPassword == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "current_password and new_password are required")
		return
	}
	err := h.svc.ChangePassword(r.Context(), UserIDFromContext(r.Context()), req.CurrentPassword, req.NewPassword, SessionIDFromContext(r.Context()))
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidCredentials):
			httpx.WriteError(w, http.StatusUnauthorized, "invalid_credentials", "current password is incorrect")
		case errors.Is(err, ErrPasswordPolicy):
			httpx.WriteError(w, http.StatusBadRequest, "weak_password", err.Error())
		default:
			writeInternalError(w, r, err)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "password changed"})
}

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

func (h *Handlers) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotPasswordRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Email == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "email is required")
		return
	}
	if err := h.svc.ForgotPassword(r.Context(), req.Email); err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{
		"message": "if the email belongs to an account, a password reset link has been sent",
	})
}

type resetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

func (h *Handlers) resetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Token == "" || req.NewPassword == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "token and new_password are required")
		return
	}
	err := h.svc.ResetPassword(r.Context(), req.Token, req.NewPassword)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidOrExpired):
			httpx.WriteError(w, http.StatusBadRequest, "invalid_or_expired_token", "this reset link is invalid or has expired")
		case errors.Is(err, ErrPasswordPolicy):
			httpx.WriteError(w, http.StatusBadRequest, "weak_password", err.Error())
		default:
			writeInternalError(w, r, err)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "password reset"})
}

func (h *Handlers) enrollMFA(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	user, err := h.svc.queries.GetUserByID(r.Context(), dbtypes.UUID(userID))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	enrollment, err := h.svc.EnrollMFA(r.Context(), userID, user.Email)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"secret": enrollment.Secret,
		"uri":    enrollment.URI,
	})
}

type confirmMFARequest struct {
	Code string `json:"code"`
}

func (h *Handlers) confirmMFA(w http.ResponseWriter, r *http.Request) {
	var req confirmMFARequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Code == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "code is required")
		return
	}
	codes, err := h.svc.ConfirmMFA(r.Context(), UserIDFromContext(r.Context()), req.Code)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidCredentials):
			httpx.WriteError(w, http.StatusBadRequest, "invalid_code", "invalid mfa code")
		case errors.Is(err, ErrMFANotEnabled):
			httpx.WriteError(w, http.StatusBadRequest, "mfa_not_enrolled", "call /auth/mfa/enroll first")
		default:
			writeInternalError(w, r, err)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (h *Handlers) disableMFA(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DisableMFA(r.Context(), UserIDFromContext(r.Context())); err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "mfa disabled"})
}

type redeemRecoveryCodeRequest struct {
	Code string `json:"code"`
}

func (h *Handlers) redeemRecoveryCode(w http.ResponseWriter, r *http.Request) {
	var req redeemRecoveryCodeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Code == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "code is required")
		return
	}
	ok, err := h.svc.RedeemRecoveryCode(r.Context(), UserIDFromContext(r.Context()), req.Code)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_code", "invalid or already-used recovery code")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "recovery code accepted"})
}

func (h *Handlers) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.svc.ListSessions(r.Context(), UserIDFromContext(r.Context()))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (h *Handlers) revokeSession(w http.ResponseWriter, r *http.Request) {
	sessionID, err := uuid.Parse(chi.URLParam(r, "sessionID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid session id")
		return
	}
	err = h.svc.RevokeSessionForUser(r.Context(), UserIDFromContext(r.Context()), sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "session not found")
			return
		}
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "session revoked"})
}

func (h *Handlers) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	err := h.svc.RevokeOtherSessions(r.Context(), UserIDFromContext(r.Context()), SessionIDFromContext(r.Context()))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "other sessions revoked"})
}

func (h *Handlers) me(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	user, err := h.svc.queries.GetUserByID(r.Context(), dbtypes.UUID(userID))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"id":             userID.String(),
		"email":          user.Email,
		"display_name":   user.DisplayName,
		"email_verified": user.EmailVerifiedAt.Valid,
		"mfa_enabled":    user.MfaEnabled,
		"status":         user.Status,
		"platform_role":  user.PlatformRole,
	})
}

type updateMeRequest struct {
	DisplayName string `json:"display_name"`
}

func (h *Handlers) updateMe(w http.ResponseWriter, r *http.Request) {
	var req updateMeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.DisplayName == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "display_name is required")
		return
	}
	userID := UserIDFromContext(r.Context())
	if err := h.svc.UpdateDisplayName(r.Context(), userID, req.DisplayName); err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "updated"})
}

type deleteMeRequest struct {
	Password string `json:"password"`
}

// deleteMe deactivates the caller's own account — see
// Service.DeactivateAccount's doc comment for why this is a status
// change, not a SQL row deletion. Requires the current password,
// mirroring changePassword's safeguard for a destructive action.
func (h *Handlers) deleteMe(w http.ResponseWriter, r *http.Request) {
	var req deleteMeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Password == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "password is required")
		return
	}
	userID := UserIDFromContext(r.Context())
	if err := h.svc.DeactivateAccount(r.Context(), userID, req.Password); err != nil {
		switch {
		case errors.Is(err, ErrInvalidCredentials):
			httpx.WriteError(w, http.StatusUnauthorized, "invalid_credentials", "incorrect password")
		default:
			writeInternalError(w, r, err)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "account deactivated"})
}

func toSessionResponse(s *IssuedSession) sessionResponse {
	return sessionResponse{
		AccessToken:      s.AccessToken,
		AccessExpiresAt:  s.AccessExpiresAt.Format(httpTimeFormat),
		RefreshToken:     s.RefreshToken,
		RefreshExpiresAt: s.RefreshExpiresAt.Format(httpTimeFormat),
	}
}

const httpTimeFormat = "2006-01-02T15:04:05Z07:00"

// writeInternalError logs the real error (server-side only) and returns a
// generic message to the client — internal error detail (SQL, stack
// traces, file paths) must never cross the API boundary.
func writeInternalError(w http.ResponseWriter, r *http.Request, err error) {
	observability.FromContext(r.Context()).Error("identity_internal_error", "error", err)
	httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
}
