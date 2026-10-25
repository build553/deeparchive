-- name: CreateDeviceAuthRequest :one
INSERT INTO device_auth_requests (device_code_hash, user_code, client_name, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetDeviceAuthRequestByCodeHash :one
SELECT * FROM device_auth_requests
WHERE device_code_hash = $1 AND expires_at > now();

-- name: GetPendingDeviceAuthRequestByUserCode :one
SELECT * FROM device_auth_requests
WHERE user_code = $1 AND status = 'pending' AND expires_at > now();

-- name: TouchDeviceAuthRequestPoll :exec
UPDATE device_auth_requests SET last_polled_at = now() WHERE id = $1;

-- name: ApproveDeviceAuthRequest :exec
UPDATE device_auth_requests SET status = 'approved', user_id = $2
WHERE id = $1 AND status = 'pending';

-- name: DenyDeviceAuthRequest :exec
UPDATE device_auth_requests SET status = 'denied'
WHERE id = $1 AND status = 'pending';

-- name: DeleteDeviceAuthRequest :exec
DELETE FROM device_auth_requests WHERE id = $1;
