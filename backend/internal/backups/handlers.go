package backups

import (
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"deeparchive/internal/devices"
	"deeparchive/internal/httpx"
	"deeparchive/internal/identity"
	"deeparchive/internal/observability"
	"deeparchive/internal/quotas"
)

type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

// Mount wires two distinct route groups with two distinct identities:
//   - /backups and /devices/{deviceID}/backups (list) are acted on by a
//     logged-in user viewing their own or their device's backup history,
//     authenticated the same way as organizations/devices' non-challenge
//     routes.
//   - /devices/{deviceID}/backups/negotiate and the commit route are
//     acted on by the device itself (the actual backup agent, which has
//     no user session — it proves itself the same way it does for
//     heartbeat, via devices.AuthenticateDevice's Ed25519-derived bearer
//     token and live revocation check).
func (h *Handlers) Mount(r chi.Router, authenticate, authenticateDevice func(http.Handler) http.Handler) {
	r.Route("/backups", func(r chi.Router) {
		r.Use(authenticate)
		r.Get("/", h.listForUser)
		r.Get("/{snapshotID}", h.get)
		r.Delete("/{snapshotID}", h.deleteSnapshot)
	})

	r.Route("/devices/{deviceID}/backups", func(r chi.Router) {
		r.With(authenticate).Get("/", h.listForDevice)
		r.With(authenticateDevice).Post("/negotiate", h.negotiate)
		r.With(authenticateDevice).Post("/", h.commit)
	})
}

type negotiateRequest struct {
	ChunkHashes []string `json:"chunk_hashes"`
}

func (h *Handlers) negotiate(w http.ResponseWriter, r *http.Request) {
	deviceID := devices.DeviceIDFromContext(r.Context())
	var req negotiateRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || len(req.ChunkHashes) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "chunk_hashes is required")
		return
	}

	statuses, err := h.svc.NegotiateUpload(r.Context(), deviceID, req.ChunkHashes)
	if err != nil {
		writeBackupsError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"chunks": statuses})
}

type commitChunkRequest struct {
	Hash           string `json:"hash"`
	CiphertextSize int64  `json:"ciphertext_size"`
}

type commitRequest struct {
	ManifestNonce      string               `json:"manifest_nonce"`      // base64
	ManifestCiphertext string               `json:"manifest_ciphertext"` // base64
	TotalSize          int64                `json:"total_size"`
	Chunks             []commitChunkRequest `json:"chunks"`
}

func (h *Handlers) commit(w http.ResponseWriter, r *http.Request) {
	deviceID := devices.DeviceIDFromContext(r.Context())
	var req commitRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.ManifestNonce == "" || req.ManifestCiphertext == "" || len(req.Chunks) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "manifest_nonce, manifest_ciphertext, and at least one chunk are required")
		return
	}
	nonce, err := base64.StdEncoding.DecodeString(req.ManifestNonce)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "manifest_nonce must be valid base64")
		return
	}
	ciphertext, err := base64.StdEncoding.DecodeString(req.ManifestCiphertext)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "manifest_ciphertext must be valid base64")
		return
	}

	chunks := make([]ChunkInput, 0, len(req.Chunks))
	for _, c := range req.Chunks {
		chunks = append(chunks, ChunkInput{Hash: c.Hash, CiphertextSize: c.CiphertextSize})
	}

	snap, err := h.svc.CommitSnapshot(r.Context(), CommitSnapshotParams{
		DeviceID:           deviceID,
		ManifestNonce:      nonce,
		ManifestCiphertext: ciphertext,
		TotalSize:          req.TotalSize,
		Chunks:             chunks,
	})
	if err != nil {
		writeBackupsError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, snap)
}

func (h *Handlers) listForDevice(w http.ResponseWriter, r *http.Request) {
	deviceID, err := parseDeviceID(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid device id")
		return
	}
	snapshots, err := h.svc.ListForDevice(r.Context(), identity.UserIDFromContext(r.Context()), deviceID)
	if err != nil {
		writeBackupsError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"snapshots": snapshots})
}

func (h *Handlers) listForUser(w http.ResponseWriter, r *http.Request) {
	snapshots, err := h.svc.ListForUser(r.Context(), identity.UserIDFromContext(r.Context()))
	if err != nil {
		writeBackupsError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"snapshots": snapshots})
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "snapshotID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid snapshot id")
		return
	}
	detail, err := h.svc.Get(r.Context(), identity.UserIDFromContext(r.Context()), id)
	if err != nil {
		writeBackupsError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, detail)
}

func (h *Handlers) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "snapshotID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid snapshot id")
		return
	}
	if err := h.svc.DeleteSnapshot(r.Context(), identity.UserIDFromContext(r.Context()), id); err != nil {
		writeBackupsError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "snapshot deleted"})
}

func parseDeviceID(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, "deviceID"))
}

func writeBackupsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, ErrInvalidHash):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, ErrChunkNotUploaded):
		httpx.WriteError(w, http.StatusConflict, "chunk_not_uploaded", err.Error())
	case errors.Is(err, quotas.ErrQuotaExceeded):
		httpx.WriteError(w, http.StatusInsufficientStorage, "quota_exceeded", err.Error())
	default:
		observability.FromContext(r.Context()).Error("backups_internal_error", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
	}
}
