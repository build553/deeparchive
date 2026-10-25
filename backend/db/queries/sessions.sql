-- name: CreateSession :one
INSERT INTO sessions (user_id, refresh_token_hash, family_id, user_agent, ip_address, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetSessionByRefreshTokenHash :one
SELECT * FROM sessions WHERE refresh_token_hash = $1;

-- name: GetSessionByPreviousRefreshTokenHash :one
-- Matches a refresh token that was valid but has already been rotated
-- away, within the short grace window recorded at rotation time. A hit
-- here means the presented token is a replay of a stale credential.
SELECT * FROM sessions
WHERE previous_refresh_token_hash = $1 AND previous_token_expires_at > now();

-- name: GetSessionByID :one
SELECT * FROM sessions WHERE id = $1;

-- name: ListActiveSessionsForUser :many
SELECT * FROM sessions
WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
ORDER BY last_used_at DESC;

-- name: RotateSession :one
-- Rotates a session to a new refresh-token hash on use (refresh-token
-- rotation), retaining the superseded hash for a 30-second replay-
-- detection grace window. Returns the updated row so callers can confirm
-- the rotation affected the expected session.
UPDATE sessions
SET previous_refresh_token_hash = refresh_token_hash,
    previous_token_expires_at = now() + interval '30 seconds',
    refresh_token_hash = $2,
    last_used_at = now()
WHERE id = $1 AND revoked_at IS NULL
RETURNING *;

-- name: RevokeSession :exec
UPDATE sessions SET revoked_at = now() WHERE id = $1;

-- name: RevokeSessionFamily :exec
-- Used on refresh-token replay detection: revoke every session descended
-- from the same family_id, since replay of an old token indicates the
-- token chain may have been stolen.
UPDATE sessions SET revoked_at = now()
WHERE family_id = $1 AND revoked_at IS NULL;

-- name: RevokeAllSessionsForUser :exec
UPDATE sessions SET revoked_at = now()
WHERE user_id = $1 AND revoked_at IS NULL;

-- name: RevokeAllOtherSessionsForUser :exec
UPDATE sessions SET revoked_at = now()
WHERE user_id = $1 AND id != $2 AND revoked_at IS NULL;
