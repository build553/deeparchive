-- name: CreateOrganization :one
INSERT INTO organizations (name, slug, owner_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetOrganizationByID :one
SELECT * FROM organizations WHERE id = $1;

-- name: ListOrganizationsForUser :many
SELECT o.* FROM organizations o
JOIN memberships m ON m.organization_id = o.id
WHERE m.user_id = $1
ORDER BY o.created_at;

-- name: UpdateOrganizationStorageQuota :exec
UPDATE organizations SET storage_quota_bytes = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateOrganizationRetentionLimit :exec
UPDATE organizations SET retention_keep_snapshots = $2, updated_at = now()
WHERE id = $1;

-- name: ListOrganizationsPage :many
-- Admin-only listing (deepctl) — not exposed over the HTTP API.
SELECT * FROM organizations ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: SearchOrganizationsPage :many
-- Admin panel listing: an empty query matches every row, otherwise
-- substring-matches name or slug.
SELECT * FROM organizations
WHERE $1::text = '' OR name ILIKE '%' || $1::text || '%' OR slug ILIKE '%' || $1::text || '%'
ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: CountOrganizationsMatching :one
SELECT count(*) FROM organizations
WHERE $1::text = '' OR name ILIKE '%' || $1::text || '%' OR slug ILIKE '%' || $1::text || '%';

-- name: UpdateOrganizationName :one
UPDATE organizations SET name = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CreateMembership :one
INSERT INTO memberships (organization_id, user_id, role)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetMembership :one
SELECT * FROM memberships WHERE organization_id = $1 AND user_id = $2;

-- name: ListMembers :many
SELECT * FROM memberships WHERE organization_id = $1 ORDER BY created_at;

-- name: ListMembersWithUser :many
-- Same rows as ListMembers, joined with the member's email/display name —
-- a members list showing only a UUID per row is not useful to a human.
SELECT m.user_id, m.role, m.created_at, u.email, u.display_name
FROM memberships m
JOIN users u ON u.id = m.user_id
WHERE m.organization_id = $1
ORDER BY m.created_at;

-- name: CountOwners :one
SELECT count(*) FROM memberships WHERE organization_id = $1 AND role = 'owner';

-- name: UpdateMembershipRole :exec
UPDATE memberships SET role = $3, updated_at = now()
WHERE organization_id = $1 AND user_id = $2;

-- name: DeleteMembership :exec
DELETE FROM memberships WHERE organization_id = $1 AND user_id = $2;

-- name: CreateInvitation :one
INSERT INTO invitations (organization_id, email, role, token_hash, invited_by, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetValidInvitationByTokenHash :one
SELECT * FROM invitations
WHERE token_hash = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now();

-- name: MarkInvitationAccepted :exec
UPDATE invitations SET accepted_at = now() WHERE id = $1;
