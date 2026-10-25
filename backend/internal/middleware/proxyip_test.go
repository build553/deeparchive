package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"deeparchive/internal/middleware"
)

func TestTrustedProxyIP_UsesLastForwardedEntryAndIgnoresXRealIP(t *testing.T) {
	var got string
	h := middleware.TrustedProxyIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.RemoteAddr
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	req.Header.Set("X-Real-IP", "7.7.7.7")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got != "203.0.113.9" {
		t.Fatalf("got %q, want the proxy-appended entry", got)
	}
}
