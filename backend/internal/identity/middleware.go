package identity

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"deeparchive/internal/dbtypes"
	"deeparchive/internal/observability"
)

type ctxKey string

const (
	ctxUserID    ctxKey = "identity_user_id"
	ctxSessionID ctxKey = "identity_session_id"
)

// Authenticate verifies the bearer access token's signature and expiry,
// then additionally checks the referenced session is still active in the
// database. The extra lookup (rather than trusting the stateless JWT
// alone) is deliberate: it is what makes "revoke this session" and
// "logout everywhere" take effect immediately instead of merely preventing
// future token refreshes while an already-issued access token remains
// valid for up to its TTL.
func (s *Service) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			writeAuthError(w, r)
			return
		}

		claims, err := s.accessTokens.Verify(token)
		if err != nil {
			writeAuthError(w, r)
			return
		}

		session, err := s.queries.GetSessionByID(r.Context(), dbtypes.UUID(claims.SessionID))
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				observability.FromContext(r.Context()).Error("session_lookup_error", "error", err)
			}
			writeAuthError(w, r)
			return
		}
		if session.RevokedAt.Valid || dbtypes.FromTimestamptz(session.ExpiresAt).Before(time.Now()) {
			writeAuthError(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), ctxUserID, claims.UserID)
		ctx = context.WithValue(ctx, ctxSessionID, claims.SessionID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(h, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}

func writeAuthError(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"authentication required"}}`))
}

// UserIDFromContext returns the authenticated user's ID. It panics if
// called outside Authenticate middleware — a handler mounted without that
// middleware is a programming error, not a runtime condition to handle
// gracefully, and should fail loudly in tests/CI rather than silently
// treat every caller as unauthenticated.
func UserIDFromContext(ctx context.Context) uuid.UUID {
	id, ok := ctx.Value(ctxUserID).(uuid.UUID)
	if !ok {
		panic("identity.UserIDFromContext: no authenticated user in context; is Authenticate middleware mounted?")
	}
	return id
}

func SessionIDFromContext(ctx context.Context) uuid.UUID {
	id, ok := ctx.Value(ctxSessionID).(uuid.UUID)
	if !ok {
		panic("identity.SessionIDFromContext: no authenticated session in context; is Authenticate middleware mounted?")
	}
	return id
}

// RequireAdmin must be mounted behind Authenticate. It re-checks the
// caller's platform_role from the database on every request — same
// rationale as Authenticate's session lookup: revoking admin access takes
// effect immediately, not only after the access token expires.
func (s *Service) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := UserIDFromContext(r.Context())
		user, err := s.queries.GetUserByID(r.Context(), dbtypes.UUID(userID))
		if err != nil || user.PlatformRole != "admin" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"admin access required"}}`))
			return
		}
		next.ServeHTTP(w, r.WithContext(r.Context()))
	})
}
