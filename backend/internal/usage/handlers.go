package usage

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"deeparchive/internal/httpx"
	"deeparchive/internal/identity"
	"deeparchive/internal/observability"
)

type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

func (h *Handlers) Mount(r chi.Router, authenticate func(http.Handler) http.Handler) {
	r.With(authenticate).Get("/usage", h.get)
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	report, err := h.svc.Report(r.Context(), identity.UserIDFromContext(r.Context()))
	if err != nil {
		observability.FromContext(r.Context()).Error("usage_internal_error", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, report)
}
