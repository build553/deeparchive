// Command api runs the DeepArchive HTTP API process.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/db/generated"
	"deeparchive/internal/admin"
	"deeparchive/internal/audit"
	"deeparchive/internal/backups"
	"deeparchive/internal/config"
	"deeparchive/internal/devices"
	"deeparchive/internal/health"
	"deeparchive/internal/httpserver"
	"deeparchive/internal/identity"
	"deeparchive/internal/middleware"
	"deeparchive/internal/notifications"
	"deeparchive/internal/observability"
	"deeparchive/internal/organizations"
	"deeparchive/internal/quotas"
	"deeparchive/internal/storage"
	"deeparchive/internal/usage"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger := observability.NewLogger(cfg.Env)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DB.URL)
	if err != nil {
		return fmt.Errorf("creating database pool: %w", err)
	}
	defer pool.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if err := pool.Ping(pingCtx); err != nil {
		cancel()
		return fmt.Errorf("database not reachable at startup: %w", err)
	}
	cancel()

	// DevLogSender is a placeholder: no real email provider is wired up
	// yet. See internal/notifications and docs/implementation-status.md.
	// In development only, swap in InboxSender instead, which additionally
	// captures emails in memory so docs/local-dev-walkthrough.md's flows
	// (verify/reset/invite) are clickable without a real provider. This is
	// one of two independent gates — the other is router.go only mounting
	// GET /dev/emails when a *notifications.Handlers is actually passed
	// below — so a change to either alone cannot accidentally expose this
	// outside development.
	hasSMTP := cfg.SMTP.Username != "" && cfg.SMTP.Password != ""
	smtpSender := notifications.SMTPSender{
		Host: cfg.SMTP.Host, Port: cfg.SMTP.Port,
		Username: cfg.SMTP.Username, Password: cfg.SMTP.Password, From: cfg.SMTP.From,
		FromName: "Build553 Team",
	}

	var emailSender notifications.Sender
	var devEmailsHandlers *notifications.Handlers
	switch {
	case cfg.Env == "development" && hasSMTP:
		// Real mail AND the Dev Inbox, so e2e tests and the walkthrough
		// doc keep working without a real mailbox to check.
		inbox := &notifications.InboxSender{Logger: logger}
		emailSender = notifications.FanoutSender{Primary: smtpSender, Also: []notifications.Sender{inbox}}
		devEmailsHandlers = notifications.NewHandlers(inbox)
	case cfg.Env == "development":
		inbox := &notifications.InboxSender{Logger: logger}
		emailSender = inbox
		devEmailsHandlers = notifications.NewHandlers(inbox)
	case hasSMTP:
		emailSender = smtpSender
	default:
		emailSender = notifications.DevLogSender{Logger: logger}
	}

	accessTokens := identity.NewAccessTokenIssuer(cfg.Auth.AccessTokenSigningKey, cfg.Auth.AccessTokenTTL)
	identityService := identity.NewService(pool, emailSender, accessTokens, cfg.Auth.RefreshTokenTTL, logger)
	identityService.WebURL = cfg.WebPublicURL
	identityHandlers := identity.NewHandlers(identityService, cfg.WebPublicURL)
	authRateLimit := middleware.NewRateLimiter(20, time.Minute).Middleware
	deviceTokenRateLimit := middleware.NewRateLimiter(120, time.Minute).Middleware

	organizationsService := organizations.NewService(pool, emailSender, logger)
	organizationsService.WebURL = cfg.WebPublicURL
	organizationsHandlers := organizations.NewHandlers(organizationsService)

	deviceTokens := devices.NewAccessTokenIssuer(cfg.Auth.AccessTokenSigningKey, cfg.Auth.AccessTokenTTL)
	devicesService := devices.NewService(pool, logger)
	devicesHandlers := devices.NewHandlers(devicesService, deviceTokens, pool)

	storageClient, err := storage.NewClient(cfg.S3)
	if err != nil {
		return fmt.Errorf("creating object storage client: %w", err)
	}
	usageService := usage.NewService(pool)
	quotasService := quotas.NewService(pool, usageService)
	backupsService := backups.NewService(pool, storageClient, quotasService, logger)
	backupsHandlers := backups.NewHandlers(backupsService)
	usageHandlers := usage.NewHandlers(usageService)

	adminAuditReader := audit.NewRecorder(generated.New(pool), logger)
	adminHandlers := admin.NewHandlers(identityService, organizationsService, adminAuditReader, usageService)

	router := httpserver.NewRouter(httpserver.Deps{
		Config:               cfg,
		Health:               &health.Handler{DB: dbPinger{pool}},
		Identity:             identityHandlers,
		Organizations:        organizationsHandlers,
		Devices:              devicesHandlers,
		Backups:              backupsHandlers,
		Usage:                usageHandlers,
		Admin:                adminHandlers,
		DevEmails:            devEmailsHandlers,
		AuthRateLimit:        authRateLimit,
		DeviceTokenRateLimit: deviceTokenRateLimit,
		Authenticate:         identityService.Authenticate,
		RequireAdmin:         identityService.RequireAdmin,
	})

	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("api_starting", "addr", cfg.HTTP.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("api_shutting_down")
	case err := <-serveErr:
		return fmt.Errorf("server error: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}
	logger.Info("api_stopped")
	return nil
}

type dbPinger struct{ pool *pgxpool.Pool }

func (p dbPinger) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}
