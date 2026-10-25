// Package middleware holds cross-cutting HTTP middleware shared by all
// routes: request IDs, panic recovery, structured access logs, and request
// size limits. Authn/authz middleware lives in internal/identity and
// internal/authorization, since it depends on domain services.
package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"deeparchive/internal/observability"
)

// RequestID assigns a request-scoped correlation ID, honoring an inbound
// X-Request-Id header from a trusted upstream proxy when present.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = uuid.NewString()
		}
		ctx := observability.WithRequestID(r.Context(), id)
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AccessLog logs one structured line per request. It never logs request or
// response bodies, headers, or query strings, which may carry sensitive
// tokens.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		observability.FromContext(r.Context()).Info("http_request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Recover converts a panic in a downstream handler into a 500 response
// instead of crashing the process, and logs the panic with a stack-free
// message (the stack trace goes to stderr via the runtime, not to
// structured logs, to avoid leaking internals to log aggregators that may
// have broader read access than the process itself).
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				observability.FromContext(r.Context()).Error("panic_recovered", "error", rec)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"code":"internal_error","message":"an unexpected error occurred"}}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// MaxBodyBytes rejects request bodies larger than limit to prevent
// unbounded-memory and storage-exhaustion abuse via oversized payloads.
func MaxBodyBytes(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

// TrustedProxyIP sets r.RemoteAddr to the last X-Forwarded-For entry, the
// one our own reverse proxy appended. Earlier entries and X-Real-IP are
// client-controlled, so they are ignored.
func TrustedProxyIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				r.RemoteAddr = ip
			}
		}
		next.ServeHTTP(w, r)
	})
}
