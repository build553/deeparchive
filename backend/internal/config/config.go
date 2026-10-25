// Package config loads and validates process configuration from environment
// variables. No defaults are assumed for security-sensitive values; the
// process refuses to start rather than run with an insecure or ambiguous
// configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env       string // "development", "staging", "production"
	HTTP      HTTPConfig
	DB        DatabaseConfig
	Auth      AuthConfig
	S3        S3Config
	CORS      CORSConfig
	Retention RetentionConfig
	SMTP      SMTPConfig
	// WebPublicURL is the browser-facing origin of the web dashboard,
	// used only to build the device-code verification link a CLI prints
	// (e.g. "http://localhost:5173/device") — display text, not a
	// security boundary, so an unset or wrong value degrades to a wrong
	// printed link rather than anything unsafe.
	WebPublicURL string
}

// SMTPConfig is optional: when Username/Password are unset, cmd/api falls
// back to the dev-only InboxSender/DevLogSender instead of refusing to
// start — unlike DB/S3/signing-key, a missing mail provider shouldn't
// block the whole API from running.
type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	// From is the sender address shown to recipients; defaults to
	// Username (Gmail requires the From address to match the
	// authenticated account anyway).
	From string
}

type HTTPConfig struct {
	Addr            string
	ShutdownTimeout time.Duration
	// TrustProxy makes the API read the client IP from the last
	// X-Forwarded-For entry. Enable only when a reverse proxy that sets
	// that header is the sole way to reach the API.
	TrustProxy bool
}

type DatabaseConfig struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	ConnMaxLifetime time.Duration
}

type AuthConfig struct {
	// AccessTokenSigningKey signs short-lived access tokens (JWT, HS256/EdDSA).
	// Must be a high-entropy secret provisioned outside source control.
	AccessTokenSigningKey []byte
	AccessTokenTTL        time.Duration
	RefreshTokenTTL       time.Duration
	// SessionCookieSecure must be true outside local development.
	SessionCookieSecure bool
	SessionCookieDomain string
}

type S3Config struct {
	Endpoint string
	// PublicEndpoint is the storage address clients (CLI, browser) can
	// reach, used only to presign URLs; defaults to Endpoint. They differ
	// in local Docker Compose, where the API reaches MinIO as "minio:9000"
	// but a browser needs "localhost:9000".
	PublicEndpoint  string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	UsePathStyle    bool
}

type CORSConfig struct {
	AllowedOrigins []string
}

type RetentionConfig struct {
	// SweepInterval is how often cmd/worker enforces every account's
	// retention_keep_snapshots limit (internal/retention.Service.Sweep).
	// A process-level setting, unlike the per-account limits themselves
	// (stored in Postgres, not config) — how often the sweep RUNS is an
	// operational concern, not something an individual account chooses.
	SweepInterval time.Duration
}

// Load reads configuration from the process environment. It returns an error
// describing exactly which variables are missing or invalid rather than
// falling back to insecure defaults.
func Load() (*Config, error) {
	var errs []string

	// Default to the strictest mode: a forgotten APP_ENV must never enable the
	// dev inbox or the built-in dev signing key.
	env := getenvDefault("APP_ENV", "production")
	if env != "development" && env != "staging" && env != "production" {
		errs = append(errs, fmt.Sprintf("APP_ENV must be one of development|staging|production, got %q", env))
	}

	addr := getenvDefault("HTTP_ADDR", ":8080")

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		errs = append(errs, "DATABASE_URL is required")
	}

	signingKeyRaw := os.Getenv("ACCESS_TOKEN_SIGNING_KEY")
	if env == "production" && len(signingKeyRaw) < 32 {
		errs = append(errs, "ACCESS_TOKEN_SIGNING_KEY must be set to a secret of at least 32 bytes in production")
	}
	if signingKeyRaw == "" && env != "production" {
		// Local/dev convenience only: never reached in production due to the check above.
		signingKeyRaw = "development-only-insecure-signing-key-not-for-prod"
	}

	accessTTL, err := parseDurationDefault("ACCESS_TOKEN_TTL", 15*time.Minute)
	if err != nil {
		errs = append(errs, err.Error())
	}
	refreshTTL, err := parseDurationDefault("REFRESH_TOKEN_TTL", 30*24*time.Hour)
	if err != nil {
		errs = append(errs, err.Error())
	}
	retentionSweepInterval, err := parseDurationDefault("RETENTION_SWEEP_INTERVAL", time.Hour)
	if err != nil {
		errs = append(errs, err.Error())
	}

	cookieSecure := env == "production" || env == "staging"
	if v := os.Getenv("SESSION_COOKIE_SECURE"); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, "SESSION_COOKIE_SECURE must be a boolean")
		} else {
			cookieSecure = parsed
		}
	}

	s3Endpoint := os.Getenv("S3_ENDPOINT")
	s3Region := getenvDefault("S3_REGION", "us-east-1")
	s3Bucket := os.Getenv("S3_BUCKET")
	s3AccessKey := os.Getenv("S3_ACCESS_KEY_ID")
	s3SecretKey := os.Getenv("S3_SECRET_ACCESS_KEY")
	if s3Endpoint == "" {
		errs = append(errs, "S3_ENDPOINT is required")
	}
	if s3Bucket == "" {
		errs = append(errs, "S3_BUCKET is required")
	}
	if s3AccessKey == "" || s3SecretKey == "" {
		errs = append(errs, "S3_ACCESS_KEY_ID and S3_SECRET_ACCESS_KEY are required (the API process holds short-lived-credential-issuing permissions, never end-user-facing permanent keys)")
	}

	usePathStyle := true
	if v := os.Getenv("S3_USE_PATH_STYLE"); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, "S3_USE_PATH_STYLE must be a boolean")
		} else {
			usePathStyle = parsed
		}
	}

	var allowedOrigins []string
	if v := os.Getenv("CORS_ALLOWED_ORIGINS"); v != "" {
		for _, o := range strings.Split(v, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				allowedOrigins = append(allowedOrigins, o)
			}
		}
	}
	if env == "production" && len(allowedOrigins) == 0 {
		errs = append(errs, "CORS_ALLOWED_ORIGINS is required in production")
	}

	smtpUsername := os.Getenv("SMTP_USERNAME")
	smtpPassword := os.Getenv("SMTP_PASSWORD")
	smtpFrom := getenvDefault("SMTP_FROM", smtpUsername)
	if (smtpUsername == "") != (smtpPassword == "") {
		errs = append(errs, "SMTP_USERNAME and SMTP_PASSWORD must be set together (or both left unset to use the dev-only sender)")
	}

	if len(errs) > 0 {
		return nil, errors.New("invalid configuration:\n  - " + strings.Join(errs, "\n  - "))
	}

	return &Config{
		Env: env,
		HTTP: HTTPConfig{
			Addr:            addr,
			ShutdownTimeout: 15 * time.Second,
			TrustProxy:      os.Getenv("TRUST_PROXY") == "true",
		},
		DB: DatabaseConfig{
			URL:             dbURL,
			MaxConns:        10,
			MinConns:        1,
			ConnMaxLifetime: time.Hour,
		},
		Auth: AuthConfig{
			AccessTokenSigningKey: []byte(signingKeyRaw),
			AccessTokenTTL:        accessTTL,
			RefreshTokenTTL:       refreshTTL,
			SessionCookieSecure:   cookieSecure,
			SessionCookieDomain:   os.Getenv("SESSION_COOKIE_DOMAIN"),
		},
		S3: S3Config{
			Endpoint:        s3Endpoint,
			PublicEndpoint:  getenvDefault("S3_PUBLIC_ENDPOINT", s3Endpoint),
			Region:          s3Region,
			Bucket:          s3Bucket,
			AccessKeyID:     s3AccessKey,
			SecretAccessKey: s3SecretKey,
			UsePathStyle:    usePathStyle,
		},
		WebPublicURL: getenvDefault("WEB_PUBLIC_URL", "http://localhost:5173"),
		CORS:         CORSConfig{AllowedOrigins: allowedOrigins},
		Retention:    RetentionConfig{SweepInterval: retentionSweepInterval},
		SMTP: SMTPConfig{
			Host:     getenvDefault("SMTP_HOST", "smtp.gmail.com"),
			Port:     getenvDefault("SMTP_PORT", "587"),
			Username: smtpUsername,
			Password: smtpPassword,
			From:     smtpFrom,
		},
	}, nil
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseDurationDefault(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration (e.g. 15m): %w", key, err)
	}
	return d, nil
}
