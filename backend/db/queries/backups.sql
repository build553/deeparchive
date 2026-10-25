-- name: GetChunkByUserAndHash :one
SELECT * FROM chunks WHERE user_id = $1 AND organization_id IS NULL AND content_hash = $2;

-- name: GetChunkByOrgAndHash :one
SELECT * FROM chunks WHERE organization_id = $1 AND content_hash = $2;

-- name: ListChunksByUserAndHashes :many
SELECT * FROM chunks WHERE user_id = $1 AND organization_id IS NULL AND content_hash = ANY(sqlc.arg(hashes)::text[]);

-- name: ListChunksByOrgAndHashes :many
SELECT * FROM chunks WHERE organization_id = $1 AND content_hash = ANY(sqlc.arg(hashes)::text[]);

-- name: CreateChunk :one
-- ON CONFLICT infers the dedup-scope unique index by matching its exact
-- expressions (a plain index, not a named constraint, so "ON CONFLICT ON
-- CONSTRAINT" cannot target it) so a second concurrent upload of the same
-- content in the same scope does not error; the caller re-reads the
-- winning row (see internal/backups).
INSERT INTO chunks (user_id, organization_id, content_hash, object_key, ciphertext_size)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (COALESCE(organization_id::text, 'u:' || user_id::text), content_hash) DO NOTHING
RETURNING *;

-- name: CreateSnapshot :one
INSERT INTO snapshots (device_id, user_id, organization_id, manifest_nonce, manifest_ciphertext, total_size, chunk_count)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: LinkSnapshotChunks :exec
INSERT INTO snapshot_chunks (snapshot_id, chunk_id)
SELECT $1, unnest(sqlc.arg(chunk_ids)::uuid[]);

-- name: GetSnapshotByID :one
SELECT * FROM snapshots WHERE id = $1;

-- name: ListSnapshotsForDevice :many
SELECT * FROM snapshots WHERE device_id = $1 ORDER BY created_at DESC;

-- name: ListSnapshotsForUser :many
-- Visible snapshots: the caller's own, or belonging to an organization the
-- caller is a member of — the same visibility rule devices.ListDevicesForUser
-- uses, kept consistent rather than reinvented here.
SELECT s.* FROM snapshots s
WHERE s.user_id = $1
   OR s.organization_id IN (SELECT organization_id FROM memberships WHERE user_id = $1)
ORDER BY s.created_at DESC;

-- name: ListChunksForSnapshot :many
SELECT c.* FROM chunks c
JOIN snapshot_chunks sc ON sc.chunk_id = c.id
WHERE sc.snapshot_id = $1;

-- name: DeleteSnapshotChunks :exec
DELETE FROM snapshot_chunks WHERE snapshot_id = $1;

-- name: DeleteSnapshotByID :exec
DELETE FROM snapshots WHERE id = $1;

-- name: IsChunkReferenced :one
-- Whether any snapshot other than the one just being deleted still
-- references this chunk. Checked AFTER that snapshot's own
-- snapshot_chunks rows are deleted (DeleteSnapshotChunks), so "any row at
-- all" is the right question — not "any row for a different snapshot."
SELECT EXISTS(SELECT 1 FROM snapshot_chunks WHERE chunk_id = $1) AS referenced;

-- name: DeleteChunkByID :exec
DELETE FROM chunks WHERE id = $1;

-- name: ListDistinctDeviceIDsWithSnapshots :many
-- Every device that has ever committed at least one snapshot — what
-- internal/retention's sweep iterates to enforce each device's owning
-- account's retention_keep_snapshots limit.
SELECT DISTINCT device_id FROM snapshots;
