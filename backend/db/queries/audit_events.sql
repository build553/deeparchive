-- name: CreateAuditEvent :one
INSERT INTO audit_events (organization_id, actor_user_id, event_type, target_type, target_id, metadata, ip_address)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListAuditEventsForUser :many
SELECT * FROM audit_events
WHERE actor_user_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditEventsForOrganization :many
SELECT * FROM audit_events
WHERE organization_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditEventsRecent :many
-- Global activity feed for the admin panel — unlike
-- ListAuditEventsForUser/ForOrganization, no actor/org filter.
SELECT * FROM audit_events
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;
