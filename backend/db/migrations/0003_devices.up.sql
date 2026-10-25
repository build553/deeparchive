-- Device registration and public-key challenge/response authentication.
--
-- A device is owned by exactly one user and optionally scoped to one
-- organization (devices backing up an individual's own files have
-- organization_id NULL). Device "authentication" is only meaningful once
-- a challenge has actually been signed and verified against the stored
-- public key — see internal/devices for where that verification happens;
-- storing a public key alone is not authentication.

CREATE TABLE devices (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    os              TEXT NOT NULL,
    client_version  TEXT NOT NULL,
    public_key      BYTEA NOT NULL, -- raw Ed25519 public key, 32 bytes
    status          TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'active', 'revoked')),
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ
);
CREATE INDEX devices_user_id_idx ON devices (user_id);
CREATE INDEX devices_organization_id_idx ON devices (organization_id);

-- Short-lived, single-use challenges for proving possession of the
-- device's private key. A device "authenticates" by signing the
-- challenge nonce; the server verifies the signature against
-- devices.public_key before issuing device-scoped credentials.
CREATE TABLE device_challenges (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id   UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    nonce       BYTEA NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX device_challenges_device_id_idx ON device_challenges (device_id);
