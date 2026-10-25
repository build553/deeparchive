package httpserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"deeparchive/internal/config"
	"deeparchive/internal/health"
	"deeparchive/internal/httpserver"
)

func testConfig() *config.Config {
	return &config.Config{
		Env: "development",
		CORS: config.CORSConfig{
			AllowedOrigins: []string{"http://localhost:5173"},
		},
	}
}

func TestHealthz_DoesNotRequireDependencies(t *testing.T) {
	router := httpserver.NewRouter(httpserver.Deps{
		Config: testConfig(),
		Health: &health.Handler{}, // no DB wired up
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestReadyz_FailsWhenDatabaseUnavailable(t *testing.T) {
	router := httpserver.NewRouter(httpserver.Deps{
		Config: testConfig(),
		Health: &health.Handler{DB: failingPinger{}},
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when DB ping fails, got %d", rec.Code)
	}
}

func TestStatus_ReturnsServiceInfo(t *testing.T) {
	router := httpserver.NewRouter(httpserver.Deps{
		Config: testConfig(),
		Health: &health.Handler{},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

type failingPinger struct{}

func (failingPinger) Ping(_ context.Context) error { return errors.New("database unavailable") }
