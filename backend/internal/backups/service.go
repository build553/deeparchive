// Package backups implements the server side of the backup protocol: it
// tells a device which chunks it still needs to upload, verifies (against
// real object storage, never a client's say-so) that chunks it is told to
// reference actually exist, and stores/serves the encrypted snapshot
// manifest that indexes them. The server never sees plaintext file
// content, file names, or directory structure. See
// docs/backup-protocol.md and docs/encryption.md.
package backups

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/db/generated"
	"deeparchive/internal/audit"
	"deeparchive/internal/dbtypes"
	"deeparchive/internal/quotas"
	"deeparchive/internal/storage"
)

var (
	ErrNotFound = errors.New("not found")
	// ErrInvalidHash guards object storage keys built directly from a
	// client-supplied content hash: without this check a malformed hash
	// could be used to construct an object key outside the chunk
	// namespace (e.g. containing "/" or "..").
	ErrInvalidHash = errors.New("content hash must be 64 lowercase hex characters (hex SHA-256)")
	// ErrChunkNotUploaded is returned when a snapshot commit references a
	// chunk that is not already known to the server and is not actually
	// present in object storage either — i.e. the client never uploaded it.
	ErrChunkNotUploaded = errors.New("one or more referenced chunks were not found in object storage")
)

// presignExpiry bounds how long an issued upload/download URL remains
// usable. Short-lived by design: these URLs grant direct read/write access
// to one object in the shared bucket, so limiting their lifetime limits
// the blast radius of one leaking (e.g. in a log line or a proxy).
const presignExpiry = 15 * time.Minute

type Service struct {
	pool    *pgxpool.Pool
	queries *generated.Queries
	storage *storage.Client
	quotas  *quotas.Service
	audit   *audit.Recorder
	logger  *slog.Logger
}

func NewService(pool *pgxpool.Pool, storageClient *storage.Client, quotasSvc *quotas.Service, logger *slog.Logger) *Service {
	queries := generated.New(pool)
	return &Service{
		pool:    pool,
		queries: queries,
		storage: storageClient,
		quotas:  quotasSvc,
		audit:   audit.NewRecorder(queries, logger),
		logger:  logger,
	}
}

type Snapshot struct {
	ID                 uuid.UUID  `json:"id"`
	DeviceID           uuid.UUID  `json:"device_id"`
	UserID             uuid.UUID  `json:"user_id"`
	OrganizationID     *uuid.UUID `json:"organization_id,omitempty"`
	ManifestNonce      []byte     `json:"manifest_nonce"`
	ManifestCiphertext []byte     `json:"manifest_ciphertext"`
	TotalSize          int64      `json:"total_size"`
	ChunkCount         int        `json:"chunk_count"`
	CreatedAt          time.Time  `json:"created_at"`
}

func toSnapshot(s generated.Snapshot) Snapshot {
	return Snapshot{
		ID:                 dbtypes.FromUUID(s.ID),
		DeviceID:           dbtypes.FromUUID(s.DeviceID),
		UserID:             dbtypes.FromUUID(s.UserID),
		OrganizationID:     dbtypes.FromUUIDPtr(s.OrganizationID),
		ManifestNonce:      s.ManifestNonce,
		ManifestCiphertext: s.ManifestCiphertext,
		TotalSize:          s.TotalSize,
		ChunkCount:         int(s.ChunkCount),
		CreatedAt:          dbtypes.FromTimestamptz(s.CreatedAt),
	}
}

// authorizeDevice returns the device if visible to userID (owner, or
// member of the device's organization), or ErrNotFound otherwise — the
// same rule and the same error-on-non-member choice as
// devices.Service.authorize, kept consistent rather than reinvented here.
func (s *Service) authorizeDevice(ctx context.Context, userID, deviceID uuid.UUID) (generated.Device, error) {
	d, err := s.queries.GetDeviceByID(ctx, dbtypes.UUID(deviceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return generated.Device{}, ErrNotFound
		}
		return generated.Device{}, fmt.Errorf("looking up device: %w", err)
	}
	if dbtypes.FromUUID(d.UserID) == userID {
		return d, nil
	}
	if d.OrganizationID.Valid {
		if _, err := s.queries.GetMembership(ctx, generated.GetMembershipParams{
			OrganizationID: d.OrganizationID, UserID: dbtypes.UUID(userID),
		}); err == nil {
			return d, nil
		}
	}
	return generated.Device{}, ErrNotFound
}

// getDevice loads a device with no ownership check — used by the
// device-token-authenticated routes (negotiate, commit), where the
// caller's identity already IS the device (proven by
// devices.AuthenticateDevice's Ed25519-derived bearer token and live
// revocation check at the HTTP layer), so there is no separate "does this
// user own this device" question to ask.
func (s *Service) getDevice(ctx context.Context, deviceID uuid.UUID) (generated.Device, error) {
	d, err := s.queries.GetDeviceByID(ctx, dbtypes.UUID(deviceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return generated.Device{}, ErrNotFound
		}
		return generated.Device{}, fmt.Errorf("looking up device: %w", err)
	}
	return d, nil
}

// authorizeSnapshot mirrors authorizeDevice for a snapshot that has
// already denormalized its owning user/organization at commit time (see
// migration 0004's comment on snapshots.user_id).
func (s *Service) authorizeSnapshot(ctx context.Context, userID uuid.UUID, snap generated.Snapshot) error {
	if dbtypes.FromUUID(snap.UserID) == userID {
		return nil
	}
	if snap.OrganizationID.Valid {
		if _, err := s.queries.GetMembership(ctx, generated.GetMembershipParams{
			OrganizationID: snap.OrganizationID, UserID: dbtypes.UUID(userID),
		}); err == nil {
			return nil
		}
	}
	return ErrNotFound
}

// objectKey is deterministic: scope + content hash. This is what lets the
// API issue presigned URLs without any server-side "upload session" state
// — see migration 0004_backups.up.sql's top-of-file comment.
func objectKey(d generated.Device, contentHash string) string {
	scope := "user/" + dbtypes.FromUUID(d.UserID).String()
	if d.OrganizationID.Valid {
		scope = "org/" + dbtypes.FromUUID(d.OrganizationID).String()
	}
	return "chunks/" + scope + "/" + contentHash
}

func isValidContentHash(h string) bool {
	if len(h) != 64 { // hex-encoded SHA-256 digest
		return false
	}
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func (s *Service) lookupExisting(ctx context.Context, d generated.Device, hashes []string) (map[string]generated.Chunk, error) {
	var rows []generated.Chunk
	var err error
	if d.OrganizationID.Valid {
		rows, err = s.queries.ListChunksByOrgAndHashes(ctx, generated.ListChunksByOrgAndHashesParams{
			OrganizationID: d.OrganizationID, Hashes: hashes,
		})
	} else {
		rows, err = s.queries.ListChunksByUserAndHashes(ctx, generated.ListChunksByUserAndHashesParams{
			UserID: d.UserID, Hashes: hashes,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("looking up existing chunks: %w", err)
	}
	out := make(map[string]generated.Chunk, len(rows))
	for _, r := range rows {
		out[r.ContentHash] = r
	}
	return out, nil
}

func (s *Service) getByScope(ctx context.Context, d generated.Device, hash string) (generated.Chunk, error) {
	if d.OrganizationID.Valid {
		return s.queries.GetChunkByOrgAndHash(ctx, generated.GetChunkByOrgAndHashParams{
			OrganizationID: d.OrganizationID, ContentHash: hash,
		})
	}
	return s.queries.GetChunkByUserAndHash(ctx, generated.GetChunkByUserAndHashParams{
		UserID: d.UserID, ContentHash: hash,
	})
}

type ChunkStatus struct {
	Hash      string `json:"hash"`
	Exists    bool   `json:"exists"`
	ObjectKey string `json:"object_key"`
	// UploadURL is set only when Exists is false — a chunk the server
	// already has never needs (or gets) a fresh presigned PUT URL.
	UploadURL string `json:"upload_url,omitempty"`
}

// NegotiateUpload tells the caller, for each content hash it is proposing
// to back up, whether the server (within this device's dedup scope)
// already has that chunk, and if not, a presigned URL to upload it to.
// Called by the device itself (see getDevice's doc comment), not a user
// session.
func (s *Service) NegotiateUpload(ctx context.Context, deviceID uuid.UUID, hashes []string) ([]ChunkStatus, error) {
	d, err := s.getDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	for _, h := range hashes {
		if !isValidContentHash(h) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidHash, h)
		}
	}

	existing, err := s.lookupExisting(ctx, d, hashes)
	if err != nil {
		return nil, err
	}

	out := make([]ChunkStatus, 0, len(hashes))
	for _, h := range hashes {
		key := objectKey(d, h)
		if _, ok := existing[h]; ok {
			out = append(out, ChunkStatus{Hash: h, Exists: true, ObjectKey: key})
			continue
		}
		uploadURL, err := s.storage.PresignedPutURL(ctx, key, presignExpiry)
		if err != nil {
			return nil, err
		}
		out = append(out, ChunkStatus{Hash: h, Exists: false, ObjectKey: key, UploadURL: uploadURL})
	}
	return out, nil
}

type ChunkInput struct {
	Hash string
	// CiphertextSize is informational only for chunks the server already
	// knows about. For a newly referenced chunk, the server never trusts
	// this value — it re-derives the real size from object storage's own
	// Stat response. See CommitSnapshot.
	CiphertextSize int64
}

type CommitSnapshotParams struct {
	DeviceID           uuid.UUID
	ManifestNonce      []byte
	ManifestCiphertext []byte
	TotalSize          int64
	Chunks             []ChunkInput
}

// CommitSnapshot finalizes a backup: every referenced chunk not already
// known to the server is verified to actually exist in object storage
// (a real Stat call, not trust in the client) before any row is written,
// then the snapshot and its chunk links are inserted together. Called by
// the device itself (see getDevice's doc comment), not a user session.
func (s *Service) CommitSnapshot(ctx context.Context, p CommitSnapshotParams) (*Snapshot, error) {
	d, err := s.getDevice(ctx, p.DeviceID)
	if err != nil {
		return nil, err
	}
	if len(p.Chunks) == 0 {
		return nil, fmt.Errorf("a snapshot must reference at least one chunk")
	}

	hashes := make([]string, 0, len(p.Chunks))
	for _, c := range p.Chunks {
		if !isValidContentHash(c.Hash) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidHash, c.Hash)
		}
		hashes = append(hashes, c.Hash)
	}

	existing, err := s.lookupExisting(ctx, d, hashes)
	if err != nil {
		return nil, err
	}

	// Verify every not-already-known chunk actually exists in object
	// storage — and learn its real size — BEFORE writing or checking
	// anything else. This has to happen first for two reasons: the
	// quota check right after needs the real total, not a client-claimed
	// one, and a commit that would fail quota must not have persisted any
	// chunk rows up to that point.
	type newChunk struct {
		hash      string
		objectKey string
		size      int64
	}
	var newChunks []newChunk
	var additionalBytes int64
	for _, c := range p.Chunks {
		if _, ok := existing[c.Hash]; ok {
			continue
		}
		key := objectKey(d, c.Hash)
		size, err := s.storage.Stat(ctx, key)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return nil, fmt.Errorf("%w: %s", ErrChunkNotUploaded, c.Hash)
			}
			return nil, fmt.Errorf("verifying chunk %s: %w", c.Hash, err)
		}
		newChunks = append(newChunks, newChunk{hash: c.Hash, objectKey: key, size: size})
		additionalBytes += size
	}

	if err := s.quotas.EnsureCapacity(ctx, dbtypes.FromUUID(d.UserID), dbtypes.FromUUIDPtr(d.OrganizationID), additionalBytes); err != nil {
		return nil, err
	}

	chunkIDs := make([]uuid.UUID, 0, len(p.Chunks))
	for _, c := range p.Chunks {
		if row, ok := existing[c.Hash]; ok {
			chunkIDs = append(chunkIDs, dbtypes.FromUUID(row.ID))
		}
	}
	for _, nc := range newChunks {
		created, err := s.queries.CreateChunk(ctx, generated.CreateChunkParams{
			UserID:         d.UserID,
			OrganizationID: d.OrganizationID,
			ContentHash:    nc.hash,
			ObjectKey:      nc.objectKey,
			CiphertextSize: nc.size,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// ON CONFLICT DO NOTHING fired: a concurrent commit created
				// this (scope, hash) chunk first. Re-read the winner rather
				// than erroring — the dedup index already guaranteed only
				// one chunk row exists, so this is not actually a conflict
				// from the caller's point of view.
				row, err := s.getByScope(ctx, d, nc.hash)
				if err != nil {
					return nil, fmt.Errorf("re-reading chunk %s after dedup conflict: %w", nc.hash, err)
				}
				chunkIDs = append(chunkIDs, dbtypes.FromUUID(row.ID))
				continue
			}
			return nil, fmt.Errorf("creating chunk %s: %w", nc.hash, err)
		}
		chunkIDs = append(chunkIDs, dbtypes.FromUUID(created.ID))
	}

	var snapshot generated.Snapshot
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)

		var err error
		snapshot, err = q.CreateSnapshot(ctx, generated.CreateSnapshotParams{
			DeviceID:           dbtypes.UUID(p.DeviceID),
			UserID:             d.UserID,
			OrganizationID:     d.OrganizationID,
			ManifestNonce:      p.ManifestNonce,
			ManifestCiphertext: p.ManifestCiphertext,
			TotalSize:          p.TotalSize,
			ChunkCount:         int32(len(chunkIDs)),
		})
		if err != nil {
			return fmt.Errorf("creating snapshot: %w", err)
		}

		ids := make([]pgtype.UUID, len(chunkIDs))
		for i, id := range chunkIDs {
			ids[i] = dbtypes.UUID(id)
		}
		if err := q.LinkSnapshotChunks(ctx, generated.LinkSnapshotChunksParams{
			SnapshotID: snapshot.ID, ChunkIds: ids,
		}); err != nil {
			return fmt.Errorf("linking snapshot chunks: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	snapshotID := dbtypes.FromUUID(snapshot.ID)
	ownerUserID := dbtypes.FromUUID(d.UserID)
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &ownerUserID, OrganizationID: dbtypes.FromUUIDPtr(d.OrganizationID),
		EventType: "snapshot.committed", TargetType: "snapshot", TargetID: &snapshotID,
		Metadata: map[string]any{"total_size": p.TotalSize, "chunk_count": len(chunkIDs)},
	})

	result := toSnapshot(snapshot)
	return &result, nil
}

func (s *Service) ListForDevice(ctx context.Context, userID, deviceID uuid.UUID) ([]Snapshot, error) {
	if _, err := s.authorizeDevice(ctx, userID, deviceID); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListSnapshotsForDevice(ctx, dbtypes.UUID(deviceID))
	if err != nil {
		return nil, fmt.Errorf("listing snapshots: %w", err)
	}
	out := make([]Snapshot, 0, len(rows))
	for _, r := range rows {
		out = append(out, toSnapshot(r))
	}
	return out, nil
}

func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID) ([]Snapshot, error) {
	rows, err := s.queries.ListSnapshotsForUser(ctx, dbtypes.UUID(userID))
	if err != nil {
		return nil, fmt.Errorf("listing snapshots: %w", err)
	}
	out := make([]Snapshot, 0, len(rows))
	for _, r := range rows {
		out = append(out, toSnapshot(r))
	}
	return out, nil
}

type ChunkLocation struct {
	Hash        string `json:"hash"`
	DownloadURL string `json:"download_url"`
}

type SnapshotDetail struct {
	Snapshot
	Chunks []ChunkLocation `json:"chunks"`
}

// Get returns a snapshot's encrypted manifest plus a presigned download
// URL for each chunk it references, everything a client needs to restore.
func (s *Service) Get(ctx context.Context, userID, snapshotID uuid.UUID) (*SnapshotDetail, error) {
	snap, err := s.queries.GetSnapshotByID(ctx, dbtypes.UUID(snapshotID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("looking up snapshot: %w", err)
	}
	if err := s.authorizeSnapshot(ctx, userID, snap); err != nil {
		return nil, err
	}

	chunks, err := s.queries.ListChunksForSnapshot(ctx, snap.ID)
	if err != nil {
		return nil, fmt.Errorf("listing snapshot chunks: %w", err)
	}

	locations := make([]ChunkLocation, 0, len(chunks))
	for _, c := range chunks {
		downloadURL, err := s.storage.PresignedGetURL(ctx, c.ObjectKey, presignExpiry)
		if err != nil {
			return nil, err
		}
		locations = append(locations, ChunkLocation{Hash: c.ContentHash, DownloadURL: downloadURL})
	}

	return &SnapshotDetail{Snapshot: toSnapshot(snap), Chunks: locations}, nil
}

// DeleteSnapshot removes a snapshot the caller owns (or is an organization
// member for), garbage-collecting any chunk no longer referenced by any
// other snapshot.
func (s *Service) DeleteSnapshot(ctx context.Context, userID, snapshotID uuid.UUID) error {
	snap, err := s.queries.GetSnapshotByID(ctx, dbtypes.UUID(snapshotID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("looking up snapshot: %w", err)
	}
	if err := s.authorizeSnapshot(ctx, userID, snap); err != nil {
		return err
	}
	if err := s.deleteSnapshotAndGC(ctx, snap); err != nil {
		return err
	}

	snapID := dbtypes.FromUUID(snap.ID)
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, OrganizationID: dbtypes.FromUUIDPtr(snap.OrganizationID),
		EventType: "snapshot.deleted", TargetType: "snapshot", TargetID: &snapID,
	})
	return nil
}

// DeleteSnapshotForRetention deletes a snapshot with no per-call
// authorization check: the caller (internal/retention's sweep) has
// already decided, on its own policy authority — not a user's request —
// that this snapshot is past its retention window. There is no acting
// user to check ownership against.
func (s *Service) DeleteSnapshotForRetention(ctx context.Context, snapshotID uuid.UUID) error {
	snap, err := s.queries.GetSnapshotByID(ctx, dbtypes.UUID(snapshotID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("looking up snapshot: %w", err)
	}
	if err := s.deleteSnapshotAndGC(ctx, snap); err != nil {
		return err
	}

	snapID := dbtypes.FromUUID(snap.ID)
	ownerUserID := dbtypes.FromUUID(snap.UserID)
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &ownerUserID, OrganizationID: dbtypes.FromUUIDPtr(snap.OrganizationID),
		EventType: "snapshot.retention_deleted", TargetType: "snapshot", TargetID: &snapID,
	})
	return nil
}

// deleteSnapshotAndGC removes a snapshot's own rows (transactionally: a
// snapshot must never end up unlinked from its chunks without the
// snapshot row itself also being gone, or vice versa) and then, outside
// that transaction (this does real network I/O against object storage,
// which should not hold a DB transaction open), deletes any chunk that
// was only reachable through this snapshot — from object storage AND the
// chunks table, not just one or the other. No authorization check and no
// audit event here; both callers (DeleteSnapshot, DeleteSnapshotForRetention)
// handle that themselves since they have different actors to record.
func (s *Service) deleteSnapshotAndGC(ctx context.Context, snap generated.Snapshot) error {
	referencedChunks, err := s.queries.ListChunksForSnapshot(ctx, snap.ID)
	if err != nil {
		return fmt.Errorf("listing snapshot chunks: %w", err)
	}

	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if err := q.DeleteSnapshotChunks(ctx, snap.ID); err != nil {
			return fmt.Errorf("unlinking snapshot chunks: %w", err)
		}
		if err := q.DeleteSnapshotByID(ctx, snap.ID); err != nil {
			return fmt.Errorf("deleting snapshot: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	for _, c := range referencedChunks {
		referenced, err := s.queries.IsChunkReferenced(ctx, c.ID)
		if err != nil {
			s.logger.Error("backups_gc_check_failed", "chunk_id", dbtypes.FromUUID(c.ID), "error", err)
			continue
		}
		if referenced {
			continue
		}
		// Prefer "object removed, DB row delete failed" over the reverse:
		// an orphaned object with no DB row is a minor, recoverable
		// storage leak; a chunk row claiming to exist with no backing
		// object would be worse (other code might trust it). Log either
		// failure rather than aborting the whole sweep over one chunk.
		if err := s.storage.Remove(ctx, c.ObjectKey); err != nil {
			s.logger.Error("backups_gc_object_remove_failed", "object_key", c.ObjectKey, "error", err)
		}
		if err := s.queries.DeleteChunkByID(ctx, c.ID); err != nil {
			s.logger.Error("backups_gc_chunk_row_delete_failed", "chunk_id", dbtypes.FromUUID(c.ID), "error", err)
		}
	}
	return nil
}
