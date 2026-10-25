// Package admin exposes the HTTP API behind the web admin panel: a
// super-admin view over every user account and every organization on the
// platform, plus a global activity feed. It is thin by design — all of
// the actual logic (and the audit trail every mutation writes) already
// lives in identity.Service's and organizations.Service's Admin* methods,
// the same ones backend/cmd/deepctl calls. This package only adds the
// HTTP plumbing and the one thing deepctl didn't need: a
// platform_role == "admin" authorization check, enforced by
// identity.Service.RequireAdmin (mounted by the caller — see Mount).
package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"deeparchive/db/generated"
	"deeparchive/internal/audit"
	"deeparchive/internal/dbtypes"
	"deeparchive/internal/httpx"
	"deeparchive/internal/identity"
	"deeparchive/internal/observability"
	"deeparchive/internal/organizations"
	"deeparchive/internal/usage"
)

type Handlers struct {
	identity *identity.Service
	orgs     *organizations.Service
	audit    *audit.Recorder
	usage    *usage.Service
}

func NewHandlers(identitySvc *identity.Service, orgsSvc *organizations.Service, auditRecorder *audit.Recorder, usageSvc *usage.Service) *Handlers {
	return &Handlers{identity: identitySvc, orgs: orgsSvc, audit: auditRecorder, usage: usageSvc}
}

// Mount registers every admin route under /admin, behind both
// authenticate (an authenticated session) and requireAdmin
// (platform_role == "admin", re-checked on every request — see
// identity.Service.RequireAdmin).
func (h *Handlers) Mount(r chi.Router, authenticate, requireAdmin func(http.Handler) http.Handler) {
	r.Route("/admin", func(r chi.Router) {
		r.Use(authenticate, requireAdmin)

		r.Get("/stats", h.stats)

		r.Get("/users", h.listUsers)
		r.Get("/users/{userID}", h.getUser)
		r.Patch("/users/{userID}/status", h.setUserStatus)
		r.Patch("/users/{userID}/quota", h.setUserQuota)
		r.Patch("/users/{userID}/retention", h.setUserRetention)
		r.Patch("/users/{userID}/platform-role", h.setUserPlatformRole)

		r.Get("/organizations", h.listOrganizations)
		r.Get("/organizations/{orgID}", h.getOrganization)
		r.Patch("/organizations/{orgID}/quota", h.setOrgQuota)
		r.Patch("/organizations/{orgID}/retention", h.setOrgRetention)

		r.Get("/audit", h.listAudit)
	})
}

// pagination reads ?limit=&offset= with sane bounds — 25/0 default,
// capped at 200 so a careless client can't force a huge scan.
func pagination(r *http.Request) (limit, offset int32) {
	limit, offset = 25, 0
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = int32(v)
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v >= 0 {
		offset = int32(v)
	}
	return limit, offset
}

func pathUUID(r *http.Request, param string) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, param))
}

func (h *Handlers) stats(w http.ResponseWriter, r *http.Request) {
	_, userCount, err := h.identity.AdminSearchUsers(r.Context(), "", 1, 0)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	_, orgCount, err := h.orgs.AdminSearchOrganizations(r.Context(), "", 1, 0)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	totalBytes, err := h.usage.AdminTotal(r.Context())
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"total_users":         userCount,
		"total_organizations": orgCount,
		"total_storage_bytes": totalBytes,
	})
}

func (h *Handlers) listUsers(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r)
	query := r.URL.Query().Get("q")
	users, total, err := h.identity.AdminSearchUsers(r.Context(), query, limit, offset)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"users": users, "total": total})
}

func (h *Handlers) getUser(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUUID(r, "userID")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid user id")
		return
	}
	user, err := h.identity.AdminGetUserByID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		writeInternalError(w, r, err)
		return
	}
	orgs, err := h.orgs.AdminListOrganizationsForUser(r.Context(), userID)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": user, "organizations": orgs})
}

type setStatusRequest struct {
	Status string `json:"status"`
}

func (h *Handlers) setUserStatus(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUUID(r, "userID")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid user id")
		return
	}
	var req setStatusRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Status == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "status is required")
		return
	}
	if err := h.identity.AdminSetStatus(r.Context(), userID, req.Status); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "status updated"})
}

type setQuotaRequest struct {
	StorageQuotaBytes int64 `json:"storage_quota_bytes"`
}

func (h *Handlers) setUserQuota(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUUID(r, "userID")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid user id")
		return
	}
	var req setQuotaRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.StorageQuotaBytes < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "storage_quota_bytes must be >= 0")
		return
	}
	if err := h.identity.AdminSetStorageQuota(r.Context(), userID, req.StorageQuotaBytes); err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "quota updated"})
}

type setRetentionRequest struct {
	RetentionKeepSnapshots int32 `json:"retention_keep_snapshots"`
}

func (h *Handlers) setUserRetention(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUUID(r, "userID")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid user id")
		return
	}
	var req setRetentionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.RetentionKeepSnapshots < 1 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "retention_keep_snapshots must be >= 1")
		return
	}
	if err := h.identity.AdminSetRetentionLimit(r.Context(), userID, req.RetentionKeepSnapshots); err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "retention limit updated"})
}

type setPlatformRoleRequest struct {
	PlatformRole string `json:"platform_role"`
}

// setUserPlatformRole is the one admin action that can affect the acting
// admin's own access, so unlike every other handler here it refuses to
// let the caller change their own role — accidentally demoting yourself
// to "none" with no other admin present would be an unrecoverable
// lockout (short of deepctl/direct DB access).
func (h *Handlers) setUserPlatformRole(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUUID(r, "userID")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid user id")
		return
	}
	actorID := identity.UserIDFromContext(r.Context())
	if actorID == userID {
		httpx.WriteError(w, http.StatusBadRequest, "self_role_change", "you cannot change your own platform role")
		return
	}
	var req setPlatformRoleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.PlatformRole == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "platform_role is required")
		return
	}
	if err := h.identity.AdminSetPlatformRole(r.Context(), actorID, userID, req.PlatformRole); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "platform role updated"})
}

func (h *Handlers) listOrganizations(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r)
	query := r.URL.Query().Get("q")
	orgs, total, err := h.orgs.AdminSearchOrganizations(r.Context(), query, limit, offset)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"organizations": orgs, "total": total})
}

func (h *Handlers) getOrganization(w http.ResponseWriter, r *http.Request) {
	orgID, err := pathUUID(r, "orgID")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid organization id")
		return
	}
	org, err := h.orgs.AdminGetByID(r.Context(), orgID)
	if err != nil {
		if errors.Is(err, organizations.ErrNotFound) {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "organization not found")
			return
		}
		writeInternalError(w, r, err)
		return
	}
	members, err := h.orgs.AdminListMembers(r.Context(), orgID)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"organization": org, "members": members})
}

func (h *Handlers) setOrgQuota(w http.ResponseWriter, r *http.Request) {
	orgID, err := pathUUID(r, "orgID")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid organization id")
		return
	}
	var req setQuotaRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.StorageQuotaBytes < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "storage_quota_bytes must be >= 0")
		return
	}
	if err := h.orgs.AdminSetStorageQuota(r.Context(), orgID, req.StorageQuotaBytes); err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "quota updated"})
}

func (h *Handlers) setOrgRetention(w http.ResponseWriter, r *http.Request) {
	orgID, err := pathUUID(r, "orgID")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid organization id")
		return
	}
	var req setRetentionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.RetentionKeepSnapshots < 1 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "retention_keep_snapshots must be >= 1")
		return
	}
	if err := h.orgs.AdminSetRetentionLimit(r.Context(), orgID, req.RetentionKeepSnapshots); err != nil {
		writeInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "retention limit updated"})
}

// adminAuditEvent reshapes generated.AuditEvent for the admin panel: raw
// pgtype columns become plain JSON-friendly values, and Metadata —
// stored as a []byte JSON blob — is decoded rather than left as a
// []byte, which encoding/json would otherwise base64-encode instead of
// rendering as the object it actually is.
type adminAuditEvent struct {
	ID             uuid.UUID      `json:"id"`
	OrganizationID *uuid.UUID     `json:"organization_id,omitempty"`
	ActorUserID    *uuid.UUID     `json:"actor_user_id,omitempty"`
	EventType      string         `json:"event_type"`
	TargetType     string         `json:"target_type,omitempty"`
	TargetID       *uuid.UUID     `json:"target_id,omitempty"`
	Metadata       map[string]any `json:"metadata"`
	IPAddress      string         `json:"ip_address,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

func toAdminAuditEvent(e generated.AuditEvent) adminAuditEvent {
	metadata := map[string]any{}
	_ = json.Unmarshal(e.Metadata, &metadata)
	var ip string
	if e.IpAddress != nil {
		ip = e.IpAddress.String()
	}
	return adminAuditEvent{
		ID:             dbtypes.FromUUID(e.ID),
		OrganizationID: dbtypes.FromUUIDPtr(e.OrganizationID),
		ActorUserID:    dbtypes.FromUUIDPtr(e.ActorUserID),
		EventType:      e.EventType,
		TargetType:     dbtypes.FromText(e.TargetType),
		TargetID:       dbtypes.FromUUIDPtr(e.TargetID),
		Metadata:       metadata,
		IPAddress:      ip,
		CreatedAt:      dbtypes.FromTimestamptz(e.CreatedAt),
	}
}

func (h *Handlers) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r)
	events, err := h.audit.Recent(r.Context(), limit, offset)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	out := make([]adminAuditEvent, 0, len(events))
	for _, e := range events {
		out = append(out, toAdminAuditEvent(e))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"events": out})
}

func writeInternalError(w http.ResponseWriter, r *http.Request, err error) {
	observability.FromContext(r.Context()).Error("admin_internal_error", "error", err)
	httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
}
