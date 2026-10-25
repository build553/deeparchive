-- name: CreateAdminLoginOTPChallenge :one
INSERT INTO admin_login_otp_challenges (user_id, code_hash, expires_at, purpose)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetValidAdminLoginOTPChallenge :one
SELECT * FROM admin_login_otp_challenges
WHERE id = $1 AND used_at IS NULL AND expires_at > now();

-- name: MarkAdminLoginOTPChallengeUsed :exec
UPDATE admin_login_otp_challenges SET used_at = now() WHERE id = $1;

-- name: IncrementAdminLoginOTPAttempts :one
UPDATE admin_login_otp_challenges SET attempts = attempts + 1
WHERE id = $1
RETURNING attempts;
