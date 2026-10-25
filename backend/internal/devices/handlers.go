package devices

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/db/generated"
	"deeparchive/internal/dbtypes"
	"deeparchive/internal/httpx"
	"deeparchive/internal/identity"
	"deeparchive/internal/observability"
)

type Handlers struct {
	svc    *Service
	tokens *AccessTokenIssuer
	pool   *pgxpool.Pool
}

func NewHandlers(svc *Service, tokens *AccessTokenIssuer, pool *pgxpool.Pool) *Handlers {
	return &Handlers{svc: svc, tokens: tokens, pool: pool}
}

func (h *Handlers) Mount(r chi.Router, authenticate func(http.Handler) http.Handler) {
	r.Route("/devices", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(authenticate)
			r.Get("/", h.list)
			r.Post("/enroll", h.enroll)
		})

		// Challenge/authenticate are intentionally unauthenticated by user
		// session — a device proves itself via its own keypair, not a
		// user's browser session. See Service.IssueChallenge's doc comment.
		r.Post("/{deviceID}/challenge", h.challenge)
		r.Post("/{deviceID}/authenticate", h.authenticate)

		r.Route("/{deviceID}", func(r chi.Router) {
			r.Group(func(r chi.Router) {
				r.Use(authenticate)
				r.Get("/", h.get)
				r.Get("/vault-key", h.getVaultKey)
				r.Put("/vault-key", h.putVaultKey)
				r.Post("/approve", h.approve)
				r.Patch("/", h.rename)
				r.Delete("/", h.revoke)
			})
			r.With(h.AuthenticateDevice).Post("/heartbeat", h.heartbeat)
		})
	})
}

type enrollRequest struct {
	Name          string `json:"name"`
	OS            string `json:"os"`
	ClientVersion string `json:"client_version"`
	PublicKey     string `json:"public_key"` // base64-encoded raw Ed25519 public key
	// Optional passphrase-wrapped vault key (already encrypted client-side)
	// so the dashboard can unlock with only the passphrase.
	WrappedVaultKey json.RawMessage `json:"wrapped_vault_key,omitempty"`
}

func (h *Handlers) enroll(w http.ResponseWriter, r *http.Request) {
	var req enrollRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Name == "" || req.PublicKey == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "name and public_key are required")
		return
	}
	key, err := DecodePublicKey(req.PublicKey)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "public_key must be valid base64")
		return
	}
	d, err := h.svc.Enroll(r.Context(), EnrollParams{
		UserID:        identity.UserIDFromContext(r.Context()),
		Name:          req.Name,
		OS:            req.OS,
		ClientVersion: req.ClientVersion,
		PublicKey:     key,
	})
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	if len(req.WrappedVaultKey) > 0 {
		if err := h.svc.SetVaultKey(r.Context(), d.UserID, d.ID, req.WrappedVaultKey); err != nil {
			writeDeviceError(w, r, err)
			return
		}
	}
	httpx.WriteJSON(w, http.StatusCreated, d)
}

func (h *Handlers) getVaultKey(w http.ResponseWriter, r *http.Request) {
	id, err := parseDeviceID(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid device id")
		return
	}
	raw, err := h.svc.GetVaultKey(r.Context(), identity.UserIDFromContext(r.Context()), id)
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]json.RawMessage{"wrapped_vault_key": raw})
}

func (h *Handlers) putVaultKey(w http.ResponseWriter, r *http.Request) {
	id, err := parseDeviceID(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid device id")
		return
	}
	var req struct {
		WrappedVaultKey json.RawMessage `json:"wrapped_vault_key"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "wrapped_vault_key is required")
		return
	}
	if err := h.svc.SetVaultKey(r.Context(), identity.UserIDFromContext(r.Context()), id, req.WrappedVaultKey); err != nil {
		writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "saved"})
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	devices, err := h.svc.ListForUser(r.Context(), identity.UserIDFromContext(r.Context()))
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	id, err := parseDeviceID(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid device id")
		return
	}
	d, err := h.svc.Get(r.Context(), identity.UserIDFromContext(r.Context()), id)
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handlers) approve(w http.ResponseWriter, r *http.Request) {
	id, err := parseDeviceID(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid device id")
		return
	}
	if err := h.svc.Approve(r.Context(), identity.UserIDFromContext(r.Context()), id); err != nil {
		writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "device approved"})
}

type renameRequest struct {
	Name string `json:"name"`
}

func (h *Handlers) rename(w http.ResponseWriter, r *http.Request) {
	id, err := parseDeviceID(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid device id")
		return
	}
	var req renameRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	if err := h.svc.Rename(r.Context(), identity.UserIDFromContext(r.Context()), id, req.Name); err != nil {
		writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "device renamed"})
}

func (h *Handlers) revoke(w http.ResponseWriter, r *http.Request) {
	id, err := parseDeviceID(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid device id")
		return
	}
	if err := h.svc.Revoke(r.Context(), identity.UserIDFromContext(r.Context()), id); err != nil {
		writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "device revoked"})
}

func (h *Handlers) challenge(w http.ResponseWriter, r *http.Request) {
	id, err := parseDeviceID(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid device id")
		return
	}
	c, err := h.svc.IssueChallenge(r.Context(), id)
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"challenge_id": c.ChallengeID.String(),
		"nonce":        base64.StdEncoding.EncodeToString(c.Nonce),
	})
}

type authenticateRequest struct {
	ChallengeID string `json:"challenge_id"`
	Signature   string `json:"signature"` // base64-encoded Ed25519 signature over the nonce
}

func (h *Handlers) authenticate(w http.ResponseWriter, r *http.Request) {
	deviceID, err := parseDeviceID(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid device id")
		return
	}
	var req authenticateRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.ChallengeID == "" || req.Signature == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "challenge_id and signature are required")
		return
	}
	challengeID, err := uuid.Parse(req.ChallengeID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid challenge_id")
		return
	}
	sig, err := base64.StdEncoding.DecodeString(req.Signature)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "signature must be valid base64")
		return
	}

	d, err := h.svc.Authenticate(r.Context(), deviceID, challengeID, sig)
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}

	token, expiresAt, err := h.tokens.Issue(d.ID, d.UserID)
	if err != nil {
		observability.FromContext(r.Context()).Error("device_token_issue_error", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"access_token": token,
		"expires_at":   expiresAt.Format(time.RFC3339),
	})
}

func (h *Handlers) heartbeat(w http.ResponseWriter, r *http.Request) {
	deviceID := DeviceIDFromContext(r.Context())
	if err := h.svc.Heartbeat(r.Context(), deviceID); err != nil {
		writeDeviceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "ok"})
}

// AuthenticateDevice verifies a device-scoped bearer access token and
// re-checks the device's live status in the database (not revoked),
// mirroring identity.Service.Authenticate's live-revocation pattern
// rather than trusting the stateless JWT's claims alone. Exported so other
// domains whose actions are performed by the device itself rather than a
// logged-in user session (e.g. internal/backups' upload/commit routes) can
// mount it as their own authentication middleware instead of duplicating
// this check.
func (h *Handlers) AuthenticateDevice(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(authHeader, prefix) {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "device authentication required")
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(authHeader, prefix))

		claims, err := h.tokens.Verify(token)
		if err != nil || claims.DeviceID == uuid.Nil {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid device token")
			return
		}

		routeDeviceID, err := parseDeviceID(r)
		if err != nil || routeDeviceID != claims.DeviceID {
			httpx.WriteError(w, http.StatusForbidden, "forbidden", "device token does not match this device")
			return
		}

		d, err := generated.New(h.pool).GetDeviceByID(r.Context(), dbtypes.UUID(claims.DeviceID))
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				observability.FromContext(r.Context()).Error("device_lookup_error", "error", err)
			}
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid device token")
			return
		}
		if d.Status == "revoked" {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "device has been revoked")
			return
		}

		ctx := contextWithDeviceID(r.Context(), claims.DeviceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type deviceIDCtxKey struct{}

func contextWithDeviceID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, deviceIDCtxKey{}, id)
}

// DeviceIDFromContext returns the device ID that AuthenticateDevice
// verified for this request. Panics if called on a request that did not
// go through AuthenticateDevice — a programming error, not a runtime one.
func DeviceIDFromContext(ctx context.Context) uuid.UUID {
	id, ok := ctx.Value(deviceIDCtxKey{}).(uuid.UUID)
	if !ok {
		panic("devices.DeviceIDFromContext: no device id in context; is AuthenticateDevice middleware mounted?")
	}
	return id
}

func parseDeviceID(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, "deviceID"))
}

func writeDeviceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "device not found")
	case errors.Is(err, ErrNoVaultKey):
		httpx.WriteError(w, http.StatusNotFound, "no_vault_key", err.Error())
	case errors.Is(err, ErrInvalidVaultKey):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, ErrForbidden):
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "you do not have permission to perform this action")
	case errors.Is(err, ErrRevoked):
		httpx.WriteError(w, http.StatusForbidden, "device_revoked", err.Error())
	case errors.Is(err, ErrInvalidPublicKey), errors.Is(err, ErrInvalidChallenge):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, ErrInvalidSignature):
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_signature", err.Error())
	default:
		observability.FromContext(r.Context()).Error("devices_internal_error", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
	}
}
