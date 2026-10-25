-- name: CreateUser :one
INSERT INTO users (email, password_hash, display_name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: MarkEmailVerified :exec
UPDATE users SET email_verified_at = now(), updated_at = now()
WHERE id = $1;

-- name: UpdatePasswordHash :exec
UPDATE users SET password_hash = $2, updated_at = now()
WHERE id = $1;

-- name: SetMFAEnabled :exec
UPDATE users SET mfa_enabled = $2, updated_at = now()
WHERE id = $1;

-- name: RecordFailedLogin :one
UPDATE users
SET failed_login_count = failed_login_count + 1,
    locked_until = CASE
        WHEN failed_login_count + 1 >= 10 THEN now() + interval '15 minutes'
        ELSE locked_until
    END,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ResetFailedLoginCount :exec
UPDATE users SET failed_login_count = 0, locked_until = NULL, updated_at = now()
WHERE id = $1;

-- name: UpdateUserStatus :exec
UPDATE users SET status = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateDisplayName :exec
UPDATE users SET display_name = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateUserStorageQuota :exec
UPDATE users SET storage_quota_bytes = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateUserRetentionLimit :exec
UPDATE users SET retention_keep_snapshots = $2, updated_at = now()
WHERE id = $1;

-- name: ListUsersPage :many
-- Admin-only listing (deepctl) — not exposed over the HTTP API.
SELECT * FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: UpdateUserPlatformRole :exec
UPDATE users SET platform_role = $2, updated_at = now()
WHERE id = $1;

-- name: SearchUsersPage :many
-- Admin panel listing: an empty query matches every row, otherwise
-- substring-matches email or display_name.
SELECT * FROM users
WHERE $1::text = '' OR email ILIKE '%' || $1::text || '%' OR display_name ILIKE '%' || $1::text || '%'
ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: CountUsersMatching :one
SELECT count(*) FROM users
WHERE $1::text = '' OR email ILIKE '%' || $1::text || '%' OR display_name ILIKE '%' || $1::text || '%';

-- name: DeleteUserByID :exec
-- Used only to roll back a just-created account whose verification email
-- could not be sent; cascades to its tokens, audit rows keep a NULL actor.
DELETE FROM users WHERE id = $1;

-- name: SetUserTermsAccepted :exec
UPDATE users SET terms_accepted_at = now(), terms_version = $2 WHERE id = $1;

-- name: SetUserDeactivatedAt :exec
-- NULL clears it (account reactivated).
UPDATE users SET deactivated_at = $2, updated_at = now() WHERE id = $1;

-- name: DeleteReclaimableUnverifiedUser :execrows
-- An unverified account whose verification link has expired (or was never
-- sent) holds an email nobody has proven they own; free it so the real
-- owner can sign up. A fresh resent link keeps the account alive.
DELETE FROM users u
WHERE u.email = $1
  AND u.email_verified_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM email_verification_tokens t
    WHERE t.user_id = u.id AND t.used_at IS NULL AND t.expires_at > now()
  );
