-- Backups: content-addressed encrypted chunks and the snapshots that
-- reference them. The server never sees plaintext bytes, file names, or
-- directory structure — chunks are opaque ciphertext and the manifest
-- (the index of what a snapshot contains) is stored encrypted end-to-end;
-- see docs/encryption.md and docs/backup-protocol.md.
--
-- There is deliberately no "upload session" table: a chunk's object storage
-- key is deterministic (scope + content hash — see internal/backups), so a
-- presigned upload URL can be regenerated at any time without server-side
-- session state. The only durable state this migration adds is "which
-- chunks exist" and "which snapshot needs which chunks" — both written
-- only once a chunk's existence in object storage has actually been
-- verified (see internal/backups.Service.CommitSnapshot), never on a
-- client's unverified say-so.

-- Dedup scope: a chunk is deduplicated against an organization's other
-- chunks if the uploading device belongs to one, otherwise against the
-- individual user's own chunks only. Every device in the same organization
-- therefore shares storage for identical content; devices in different
-- organizations (or different individual users) never do. This is a
-- deliberate choice, not an oversight: the alternative (global dedup across
-- all tenants) would leak "someone, somewhere, already has this exact
-- content" across tenant boundaries, which this product does not want to
-- expose. See docs/encryption.md for the (accepted) narrower leak this
-- still has: within one scope, repeated content is detectable as repeated.
CREATE TABLE chunks (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The user whose upload created this chunk row, kept for audit/accounting
    -- even when organization_id is set (dedup scope) — see the comment above.
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id  UUID REFERENCES organizations(id) ON DELETE CASCADE,
    content_hash     TEXT NOT NULL, -- hex SHA-256 of the PLAINTEXT chunk
    object_key       TEXT NOT NULL UNIQUE, -- object storage key of the ENCRYPTED chunk
    ciphertext_size  BIGINT NOT NULL CHECK (ciphertext_size >= 0),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Enforces the dedup scope described above: within one scope (an
-- organization if set, otherwise a specific user), a content hash maps to
-- at most one chunk. COALESCE collapses the two cases into one expression
-- so a single index (rather than two partial ones) covers both.
CREATE UNIQUE INDEX chunks_dedup_scope_hash_idx
    ON chunks (COALESCE(organization_id::text, 'u:' || user_id::text), content_hash);

CREATE TABLE snapshots (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id            UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    -- Denormalized from devices at commit time (not re-derived on every
    -- read) so a snapshot's ownership survives even if the device is later
    -- renamed/revoked; access control re-checks the device's CURRENT
    -- owner/membership regardless (see internal/backups.Service.authorize).
    user_id              UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id      UUID REFERENCES organizations(id) ON DELETE CASCADE,
    -- The manifest (file list + chunk map), encrypted client-side. Stored
    -- directly rather than as an object-storage blob: manifests are small
    -- (an index, not file content) and this avoids a second round trip on
    -- every restore. Revisit if manifests for extremely large trees turn
    -- out not to be small in practice.
    manifest_nonce       BYTEA NOT NULL,
    manifest_ciphertext  BYTEA NOT NULL,
    -- Plaintext byte total and chunk count are NOT secret (needed for
    -- storage-usage accounting and a snapshot list UI) even though file
    -- names and the chunk map are. See docs/encryption.md.
    total_size           BIGINT NOT NULL CHECK (total_size >= 0),
    chunk_count          INT NOT NULL CHECK (chunk_count >= 0),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX snapshots_device_id_idx ON snapshots (device_id);
CREATE INDEX snapshots_user_id_idx ON snapshots (user_id);
CREATE INDEX snapshots_organization_id_idx ON snapshots (organization_id);

-- Which chunks a snapshot needs. ON DELETE RESTRICT on chunk_id: a chunk
-- referenced by any snapshot must not be silently deletable out from under
-- it (there is no chunk garbage-collection implemented yet — see
-- docs/implementation-status.md — but the constraint that would make GC
-- safe is in place from the start).
CREATE TABLE snapshot_chunks (
    snapshot_id  UUID NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
    chunk_id     UUID NOT NULL REFERENCES chunks(id) ON DELETE RESTRICT,
    PRIMARY KEY (snapshot_id, chunk_id)
);
CREATE INDEX snapshot_chunks_chunk_id_idx ON snapshot_chunks (chunk_id);
