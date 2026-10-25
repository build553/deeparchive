ALTER TABLE organizations
    DROP COLUMN storage_quota_bytes,
    DROP COLUMN retention_keep_snapshots;

ALTER TABLE users
    DROP COLUMN storage_quota_bytes,
    DROP COLUMN retention_keep_snapshots;
