// Package health implements liveness and readiness checks.
//
// /healthz answers "is this process alive" and must never depend on any
// external service — a database outage must not make the process report
// itself as dead to its orchestrator, which would trigger unnecessary
// restarts.
//
// /readyz answers "can this process currently serve traffic" and checks the
// dependencies actually required to do so.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type Pinger interface {
	Ping(ctx context.Context) error
}

type Handler struct {
	DB Pinger
	// StorageCheck verifies the configured object storage endpoint is
	// reachable. Optional: nil disables the check (e.g. in unit tests).
	StorageCheck func(ctx context.Context) error
}

func (h *Handler) Live(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	checks := map[string]string{}
	ready := true

	if h.DB != nil {
		if err := h.DB.Ping(ctx); err != nil {
			checks["database"] = "unavailable"
			ready = false
		} else {
			checks["database"] = "ok"
		}
	}

	if h.StorageCheck != nil {
		if err := h.StorageCheck(ctx); err != nil {
			checks["storage"] = "unavailable"
			ready = false
		} else {
			checks["storage"] = "ok"
		}
	}

	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"status": readyLabel(ready), "checks": checks})
}

func readyLabel(ready bool) string {
	if ready {
		return "ok"
	}
	return "not_ready"
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
