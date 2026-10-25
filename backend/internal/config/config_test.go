package config_test

import (
	"os"
	"testing"

	"deeparchive/internal/config"
)

func clearEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"APP_ENV", "HTTP_ADDR", "DATABASE_URL", "ACCESS_TOKEN_SIGNING_KEY",
		"ACCESS_TOKEN_TTL", "REFRESH_TOKEN_TTL", "SESSION_COOKIE_SECURE",
		"S3_ENDPOINT", "S3_REGION", "S3_BUCKET", "S3_ACCESS_KEY_ID",
		"S3_SECRET_ACCESS_KEY", "S3_USE_PATH_STYLE", "CORS_ALLOWED_ORIGINS",
	}
	for _, k := range keys {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func validEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("S3_ENDPOINT", "http://localhost:9000")
	t.Setenv("S3_BUCKET", "test-bucket")
	t.Setenv("S3_ACCESS_KEY_ID", "key")
	t.Setenv("S3_SECRET_ACCESS_KEY", "secret")
}

func TestLoad_MissingDatabaseURL_Fails(t *testing.T) {
	clearEnv(t)
	validEnv(t)
	t.Setenv("DATABASE_URL", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when DATABASE_URL is missing")
	}
}

func TestLoad_ProductionRequiresExplicitSigningKeyAndCORS(t *testing.T) {
	clearEnv(t)
	validEnv(t)
	t.Setenv("APP_ENV", "production")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error: production must not fall back to the dev signing key or allow-all CORS")
	}
}

func TestLoad_ValidDevelopmentConfig_Succeeds(t *testing.T) {
	clearEnv(t)
	validEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Env != "development" {
		t.Errorf("expected env=development, got %q", cfg.Env)
	}
	if len(cfg.Auth.AccessTokenSigningKey) == 0 {
		t.Error("expected a non-empty dev signing key fallback")
	}
}

func TestLoad_MissingS3Credentials_Fails(t *testing.T) {
	clearEnv(t)
	validEnv(t)
	t.Setenv("S3_ACCESS_KEY_ID", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when S3 access key is missing")
	}
}

func TestLoad_MissingS3Endpoint_Fails(t *testing.T) {
	clearEnv(t)
	validEnv(t)
	t.Setenv("S3_ENDPOINT", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when S3 endpoint is missing: presigned URLs cannot be issued without it")
	}
}

func TestLoad_UnsetAppEnv_DefaultsToProduction(t *testing.T) {
	clearEnv(t)
	validEnv(t)
	t.Setenv("APP_ENV", "")

	// Production without a signing key and CORS origins must fail, proving
	// an unset APP_ENV no longer falls back to the insecure dev defaults.
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error: unset APP_ENV must behave as production")
	}
}
