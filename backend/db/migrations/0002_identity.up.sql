-- Core identity schema: users, organizations, memberships, sessions, and
-- the auxiliary single-use-token tables for email verification, password
-- reset, and MFA. Devices land in a later migration since they depend on
-- organizations/users but are a separate bounded concern.

-- Case-insensitive email comparisons without needing lower() everywhere;
-- must be created before any column uses the CITEXT type.
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email               CITEXT NOT NULL,
    password_hash       TEXT NOT NULL,
    display_name        TEXT NOT NULL,
    email_verified_at   TIMESTAMPTZ,
    status              TEXT NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active', 'suspended', 'deactivated')),
    platform_role       TEXT NOT NULL DEFAULT 'none'
                            CHECK (platform_role IN ('none', 'operator', 'admin')),
    mfa_enabled         BOOLEAN NOT NULL DEFAULT FALSE,
    failed_login_count  INTEGER NOT NULL DEFAULT 0,
    locked_until        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_email_key ON users (email);

CREATE TABLE organizations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL,
    owner_id    UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX organizations_slug_key ON organizations (slug);

CREATE TABLE memberships (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role            TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, user_id)
);
CREATE INDEX memberships_user_id_idx ON memberships (user_id);

CREATE TABLE invitations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email           CITEXT NOT NULL,
    role            TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    token_hash      TEXT NOT NULL,
    invited_by      UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    expires_at      TIMESTAMPTZ NOT NULL,
    accepted_at     TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX invitations_token_hash_key ON invitations (token_hash);
CREATE INDEX invitations_org_email_idx ON invitations (organization_id, email);

-- Refresh sessions. The opaque bearer token handed to the client is never
-- stored; only its hash is, so a stolen database dump does not yield usable
-- session tokens.
CREATE TABLE sessions (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash          TEXT NOT NULL,
    -- Refresh-token rotation: each use issues a new token and overwrites
    -- this row's hash, retaining the just-superseded hash for a short
    -- grace window below so presenting it again (replay of a stolen,
    -- already-used token) is detectable and revokes the whole family.
    previous_refresh_token_hash TEXT,
    previous_token_expires_at   TIMESTAMPTZ,
    family_id                   UUID NOT NULL,
    user_agent                  TEXT,
    ip_address                  INET,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at                  TIMESTAMPTZ NOT NULL,
    revoked_at                  TIMESTAMPTZ
);
CREATE UNIQUE INDEX sessions_refresh_token_hash_key ON sessions (refresh_token_hash);
CREATE INDEX sessions_previous_refresh_token_hash_idx ON sessions (previous_refresh_token_hash)
    WHERE previous_refresh_token_hash IS NOT NULL;
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_family_id_idx ON sessions (family_id);

CREATE TABLE email_verification_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX email_verification_tokens_token_hash_key ON email_verification_tokens (token_hash);
CREATE INDEX email_verification_tokens_user_id_idx ON email_verification_tokens (user_id);

CREATE TABLE password_reset_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX password_reset_tokens_token_hash_key ON password_reset_tokens (token_hash);
CREATE INDEX password_reset_tokens_user_id_idx ON password_reset_tokens (user_id);

-- One row per user holding the current TOTP secret. Enrollment creates a
-- row with confirmed_at NULL; confirm sets confirmed_at. Disabling MFA
-- deletes the row (recovery codes are deleted via cascade).
CREATE TABLE mfa_credentials (
    user_id         UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret_enc      BYTEA NOT NULL,
    confirmed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE mfa_recovery_codes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash   TEXT NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX mfa_recovery_codes_user_id_idx ON mfa_recovery_codes (user_id);

-- Append-only audit trail. organization_id is nullable for
-- account-level-only events (e.g. a user's own login).
CREATE TABLE audit_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    actor_user_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    event_type      TEXT NOT NULL,
    target_type     TEXT,
    target_id       UUID,
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    ip_address      INET,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_org_idx ON audit_events (organization_id, created_at DESC);
CREATE INDEX audit_events_actor_idx ON audit_events (actor_user_id, created_at DESC);
