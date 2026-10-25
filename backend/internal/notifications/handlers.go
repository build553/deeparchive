package notifications

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"deeparchive/internal/httpx"
)

// Handlers exposes InboxSender's captured emails over HTTP. Only ever
// constructed and mounted when APP_ENV=development — see cmd/api/main.go
// and httpserver/router.go. Deliberately unauthenticated: reading a
// verification email must work before the account it's for can log in.
type Handlers struct {
	inbox *InboxSender
}

func NewHandlers(inbox *InboxSender) *Handlers {
	return &Handlers{inbox: inbox}
}

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/dev/emails", h.list)
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"emails": h.inbox.Recent()})
}
