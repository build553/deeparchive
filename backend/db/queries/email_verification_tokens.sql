-- name: CreateEmailVerificationToken :one
INSERT INTO email_verification_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetValidEmailVerificationToken :one
SELECT * FROM email_verification_tokens
WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now();

-- name: MarkEmailVerificationTokenUsed :exec
UPDATE email_verification_tokens SET used_at = now() WHERE id = $1;

-- name: InvalidateEmailVerificationTokensForUser :exec
UPDATE email_verification_tokens SET used_at = now()
WHERE user_id = $1 AND used_at IS NULL;
