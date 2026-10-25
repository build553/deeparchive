-- Storage quota and snapshot retention limits, one row of settings per
-- account (user or organization) rather than a separate "plans" table —
-- there is exactly one limit of each kind today, and a plans table would
-- be speculative until there's a second plan tier to justify it.
--
-- Defaults are generous personal-backup values (5 GiB, keep the 10 most
-- recent snapshots per device), chosen so existing and newly created
-- accounts behave sensibly without an operator having to configure
-- anything. Both are plain mutable columns, not read from environment
-- config, specifically so a future admin surface (Milestone 7) can grant
-- a bigger quota to one account without restarting the API process.
ALTER TABLE users
    ADD COLUMN storage_quota_bytes BIGINT NOT NULL DEFAULT 5368709120 CHECK (storage_quota_bytes > 0),
    ADD COLUMN retention_keep_snapshots INT NOT NULL DEFAULT 10 CHECK (retention_keep_snapshots > 0);

ALTER TABLE organizations
    ADD COLUMN storage_quota_bytes BIGINT NOT NULL DEFAULT 5368709120 CHECK (storage_quota_bytes > 0),
    ADD COLUMN retention_keep_snapshots INT NOT NULL DEFAULT 10 CHECK (retention_keep_snapshots > 0);
