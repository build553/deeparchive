-- name: CreateDevice :one
INSERT INTO devices (user_id, organization_id, name, os, client_version, public_key)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetDeviceByID :one
SELECT * FROM devices WHERE id = $1;

-- name: ListDevicesForUser :many
-- Visible devices: owned directly by the caller, or belonging to an
-- organization the caller is a member of.
SELECT d.* FROM devices d
WHERE d.user_id = $1
   OR d.organization_id IN (SELECT organization_id FROM memberships WHERE user_id = $1)
ORDER BY d.registered_at DESC;

-- name: ApproveDevice :exec
UPDATE devices SET status = 'active' WHERE id = $1 AND status = 'pending';

-- name: RevokeDevice :exec
UPDATE devices SET status = 'revoked', revoked_at = now() WHERE id = $1;

-- name: UpdateDeviceName :exec
UPDATE devices SET name = $2 WHERE id = $1;

-- name: UpdateDeviceLastSeen :exec
UPDATE devices SET last_seen_at = now() WHERE id = $1;

-- name: CreateDeviceChallenge :one
INSERT INTO device_challenges (device_id, nonce, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetValidDeviceChallenge :one
SELECT * FROM device_challenges
WHERE id = $1 AND device_id = $2 AND used_at IS NULL AND expires_at > now();

-- name: MarkDeviceChallengeUsed :exec
UPDATE device_challenges SET used_at = now() WHERE id = $1;

-- name: SetDeviceWrappedVaultKey :exec
UPDATE devices SET wrapped_vault_key = $3 WHERE id = $1 AND user_id = $2;

-- name: GetDeviceWrappedVaultKey :one
SELECT wrapped_vault_key FROM devices WHERE id = $1 AND user_id = $2;
