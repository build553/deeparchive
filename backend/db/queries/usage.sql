-- name: SumChunkSizeForUser :one
-- Current storage usage for a user's own (non-organization) dedup scope —
-- see 0004_backups.up.sql's dedup-scope comment. COALESCE(...,0) so a
-- user with no chunks yet gets 0, not NULL.
SELECT COALESCE(SUM(ciphertext_size), 0)::bigint AS total_bytes
FROM chunks WHERE user_id = $1 AND organization_id IS NULL;

-- name: SumChunkSizeForOrg :one
SELECT COALESCE(SUM(ciphertext_size), 0)::bigint AS total_bytes
FROM chunks WHERE organization_id = $1;

-- name: SumChunkSizeTotal :one
-- Platform-wide storage usage across every dedup scope — the admin
-- panel's dashboard overview.
SELECT COALESCE(SUM(ciphertext_size), 0)::bigint AS total_bytes
FROM chunks;
