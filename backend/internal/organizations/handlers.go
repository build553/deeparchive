package organizations

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"deeparchive/internal/httpx"
	"deeparchive/internal/identity"
	"deeparchive/internal/observability"
)

type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// Mount registers organization routes under r. All routes require
// authentication; svc's own methods enforce membership/role authorization
// per endpoint, so handlers here only translate between HTTP and the
// service layer.
func (h *Handlers) Mount(r chi.Router, authenticate func(http.Handler) http.Handler) {
	r.Route("/organizations", func(r chi.Router) {
		r.Use(authenticate)
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Route("/{organizationID}", func(r chi.Router) {
			r.Use(h.loadOrgID)
			r.Get("/", h.get)
			r.Patch("/", h.update)
			r.Get("/members", h.listMembers)
			r.Post("/invitations", h.invite)
			r.Patch("/members/{userID}", h.updateMemberRole)
			r.Delete("/members/{userID}", h.removeMember)
			r.Post("/leave", h.leave)
		})
	})
	r.With(authenticate).Post("/invitations/accept", h.acceptInvitation)
}

type orgIDCtxKey struct{}

func (h *Handlers) loadOrgID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "organizationID"))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid organization id")
			return
		}
		ctx := contextWithOrgID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type createOrgRequest struct {
	Name string `json:"name"`
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	var req createOrgRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	org, err := h.svc.Create(r.Context(), identity.UserIDFromContext(r.Context()), req.Name)
	if err != nil {
		writeOrgError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, org)
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	orgs, err := h.svc.ListForUser(r.Context(), identity.UserIDFromContext(r.Context()))
	if err != nil {
		writeOrgError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"organizations": orgs})
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	org, role, err := h.svc.Get(r.Context(), identity.UserIDFromContext(r.Context()), orgIDFromContext(r.Context()))
	if err != nil {
		writeOrgError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"organization": org, "your_role": role})
}

type updateOrgRequest struct {
	Name string `json:"name"`
}

func (h *Handlers) update(w http.ResponseWriter, r *http.Request) {
	var req updateOrgRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	org, err := h.svc.UpdateName(r.Context(), identity.UserIDFromContext(r.Context()), orgIDFromContext(r.Context()), req.Name)
	if err != nil {
		writeOrgError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, org)
}

func (h *Handlers) listMembers(w http.ResponseWriter, r *http.Request) {
	members, err := h.svc.ListMembers(r.Context(), identity.UserIDFromContext(r.Context()), orgIDFromContext(r.Context()))
	if err != nil {
		writeOrgError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"members": members})
}

type inviteRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (h *Handlers) invite(w http.ResponseWriter, r *http.Request) {
	var req inviteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Email == "" || req.Role == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "email and role are required")
		return
	}
	err := h.svc.Invite(r.Context(), identity.UserIDFromContext(r.Context()), orgIDFromContext(r.Context()), req.Email, req.Role)
	if err != nil {
		writeOrgError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"message": "invitation sent"})
}

type acceptInvitationRequest struct {
	Token string `json:"token"`
}

func (h *Handlers) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	var req acceptInvitationRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Token == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	userID := identity.UserIDFromContext(r.Context())
	userEmail, err := h.svc.lookupUserEmail(r.Context(), userID)
	if err != nil {
		writeOrgError(w, r, err)
		return
	}
	org, err := h.svc.AcceptInvitation(r.Context(), userID, userEmail, req.Token)
	if err != nil {
		writeOrgError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, org)
}

type updateMemberRoleRequest struct {
	Role string `json:"role"`
}

func (h *Handlers) updateMemberRole(w http.ResponseWriter, r *http.Request) {
	targetUserID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid user id")
		return
	}
	var req updateMemberRoleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Role == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "role is required")
		return
	}
	err = h.svc.UpdateMemberRole(r.Context(), identity.UserIDFromContext(r.Context()), orgIDFromContext(r.Context()), targetUserID, req.Role)
	if err != nil {
		writeOrgError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "role updated"})
}

func (h *Handlers) removeMember(w http.ResponseWriter, r *http.Request) {
	targetUserID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid user id")
		return
	}
	err = h.svc.RemoveMember(r.Context(), identity.UserIDFromContext(r.Context()), orgIDFromContext(r.Context()), targetUserID)
	if err != nil {
		writeOrgError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "member removed"})
}

func (h *Handlers) leave(w http.ResponseWriter, r *http.Request) {
	err := h.svc.Leave(r.Context(), identity.UserIDFromContext(r.Context()), orgIDFromContext(r.Context()))
	if err != nil {
		writeOrgError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "left organization"})
}

func writeOrgError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "organization not found")
	case errors.Is(err, ErrForbidden):
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "you do not have permission to perform this action")
	case errors.Is(err, ErrLastOwner):
		httpx.WriteError(w, http.StatusConflict, "last_owner", err.Error())
	case errors.Is(err, ErrInvalidRole):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, ErrInvalidOrExpired):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_or_expired_token", err.Error())
	case errors.Is(err, ErrEmailMismatch):
		httpx.WriteError(w, http.StatusForbidden, "email_mismatch", err.Error())
	default:
		observability.FromContext(r.Context()).Error("organizations_internal_error", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
	}
}
