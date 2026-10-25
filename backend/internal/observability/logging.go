// Package observability configures structured logging and request-scoped
// context values (request IDs) shared by the API and worker processes.
package observability

import (
	"context"
	"log/slog"
	"os"
)

type ctxKey string

const requestIDKey ctxKey = "request_id"

// NewLogger returns a structured JSON logger. Level is derived from LOG_LEVEL
// (debug|info|warn|error), defaulting to info.
func NewLogger(env string) *slog.Logger {
	level := slog.LevelInfo
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Defense in depth: never let an accidentally-logged field with
			// one of these keys leak a secret into log storage.
			switch a.Key {
			case "password", "access_token", "refresh_token", "signing_key",
				"recovery_code", "mfa_secret", "presigned_url", "secret_access_key":
				return slog.String(a.Key, "[REDACTED]")
			}
			return a
		},
	})
	logger := slog.New(handler).With("service", "deeparchive", "env", env)
	slog.SetDefault(logger)
	return logger
}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// FromContext returns a logger annotated with the request ID present in ctx,
// falling back to the default logger when none is set.
func FromContext(ctx context.Context) *slog.Logger {
	l := slog.Default()
	if id := RequestID(ctx); id != "" {
		l = l.With("request_id", id)
	}
	return l
}
