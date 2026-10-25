-- name: UpsertMFACredential :one
INSERT INTO mfa_credentials (user_id, secret_enc)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE SET secret_enc = $2, confirmed_at = NULL
RETURNING *;

-- name: GetMFACredential :one
SELECT * FROM mfa_credentials WHERE user_id = $1;

-- name: ConfirmMFACredential :exec
UPDATE mfa_credentials SET confirmed_at = now() WHERE user_id = $1;

-- name: DeleteMFACredential :exec
DELETE FROM mfa_credentials WHERE user_id = $1;

-- name: CreateMFARecoveryCode :one
INSERT INTO mfa_recovery_codes (user_id, code_hash)
VALUES ($1, $2)
RETURNING *;

-- name: ListUnusedMFARecoveryCodes :many
SELECT * FROM mfa_recovery_codes WHERE user_id = $1 AND used_at IS NULL;

-- name: MarkMFARecoveryCodeUsed :exec
UPDATE mfa_recovery_codes SET used_at = now() WHERE id = $1;

-- name: DeleteMFARecoveryCodesForUser :exec
DELETE FROM mfa_recovery_codes WHERE user_id = $1;
