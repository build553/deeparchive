// Package httpserver wires the HTTP router: middleware stack, health
// endpoints, and (as they are implemented) the versioned API route groups
// under /api/v1. Handlers stay thin; business logic lives in per-domain
// packages (internal/identity, internal/backups, etc.) so it is testable
// without an HTTP server.
package httpserver

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"deeparchive/internal/admin"
	"deeparchive/internal/backups"
	"deeparchive/internal/config"
	"deeparchive/internal/devices"
	"deeparchive/internal/health"
	"deeparchive/internal/identity"
	"deeparchive/internal/middleware"
	"deeparchive/internal/notifications"
	"deeparchive/internal/organizations"
	"deeparchive/internal/usage"
)

const apiVersion = "v1"

// maxRequestBodyBytes bounds JSON request bodies. Upload data itself never
// transits this API (clients upload directly to object storage), so this
// limit can stay small.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

type Deps struct {
	Config        *config.Config
	Health        *health.Handler
	Identity      *identity.Handlers
	Organizations *organizations.Handlers
	Devices       *devices.Handlers
	Backups       *backups.Handlers
	Usage         *usage.Handlers
	Admin         *admin.Handlers
	// DevEmails is non-nil only when cfg.Env == "development" — see
	// cmd/api/main.go. Exposes captured "sent" emails with no auth, so it
	// must never exist outside local dev. See
	// internal/notifications/handlers.go.
	DevEmails *notifications.Handlers
	// AuthRateLimit guards login/registration/password-recovery routes.
	// See internal/middleware/ratelimit.go for its single-instance-only
	// limitation.
	AuthRateLimit func(http.Handler) http.Handler
	// DeviceTokenRateLimit guards POST /auth/device/token specifically —
	// much more generous than AuthRateLimit because a well-behaved CLI
	// polls it every few seconds for up to 15 minutes by design (that
	// polling is self-throttled by PollDeviceAuth's own interval check;
	// this limiter exists only to bound outright abuse, not to pace
	// legitimate polling the way AuthRateLimit paces login attempts).
	DeviceTokenRateLimit func(http.Handler) http.Handler
	// Authenticate is the identity service's auth middleware, shared with
	// other domains (organizations, and later devices/backups) so every
	// protected route uses the exact same session-revocation-aware check.
	Authenticate func(http.Handler) http.Handler
	// RequireAdmin gates every /admin route behind platform_role ==
	// "admin", re-checked from the database on every request — see
	// identity.Service.RequireAdmin. Mounted after Authenticate.
	RequireAdmin func(http.Handler) http.Handler
}

func NewRouter(deps Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recover)
	r.Use(middleware.AccessLog)
	if deps.Config.HTTP.TrustProxy {
		r.Use(middleware.TrustedProxyIP)
	}
	r.Use(middleware.MaxBodyBytes(maxRequestBodyBytes))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   deps.Config.CORS.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type", "Authorization", "X-Request-Id", "X-CSRF-Token"},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	r.Use(securityHeaders)

	r.Get("/healthz", deps.Health.Live)
	r.Get("/readyz", deps.Health.Ready)

	r.Route("/api/"+apiVersion, func(r chi.Router) {
		r.Get("/status", statusHandler(deps.Config))
		if deps.Identity != nil {
			deps.Identity.Mount(r, deps.AuthRateLimit, deps.DeviceTokenRateLimit)
		}
		if deps.Organizations != nil {
			deps.Organizations.Mount(r, deps.Authenticate)
		}
		if deps.Devices != nil {
			deps.Devices.Mount(r, deps.Authenticate)
		}
		if deps.Backups != nil {
			deps.Backups.Mount(r, deps.Authenticate, deps.Devices.AuthenticateDevice)
		}
		if deps.Usage != nil {
			deps.Usage.Mount(r, deps.Authenticate)
		}
		if deps.Admin != nil {
			deps.Admin.Mount(r, deps.Authenticate, deps.RequireAdmin)
		}
		if deps.DevEmails != nil {
			deps.DevEmails.Mount(r)
		}
		// Further route groups are mounted here as each domain module is
		// implemented. See docs/implementation-status.md for what is wired
		// up so far.
	})

	return r
}

// securityHeaders sets conservative defaults appropriate for a JSON API with
// no HTML rendering. The web dashboard (a separate static-asset server) sets
// its own CSP appropriate for a React SPA.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func statusHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"service":     "deeparchive-api",
			"api_version": apiVersion,
			"environment": cfg.Env,
			"server_time": time.Now().UTC().Format(time.RFC3339),
		})
	}
}
