-- Account lifecycle + consent + browsable vaults.
--
-- deactivated_at: start of the 15-day recovery window (see identity
-- recovery flow). terms_*: which legal text a person agreed to, and when.
ALTER TABLE users
    ADD COLUMN deactivated_at   TIMESTAMPTZ,
    ADD COLUMN terms_accepted_at TIMESTAMPTZ,
    ADD COLUMN terms_version     TEXT;

-- The emailed one-time code now serves two purposes: admin sign-in and
-- reactivating a deactivated account.
ALTER TABLE admin_login_otp_challenges
    ADD COLUMN purpose TEXT NOT NULL DEFAULT 'admin_login'
    CHECK (purpose IN ('admin_login', 'account_recovery'));

-- The device's vault key, already wrapped under the person's passphrase
-- with Argon2id + AES-256-GCM on their own machine (the exact object in
-- device.json). Storing it lets the dashboard open a backup with just the
-- passphrase; the server never sees the passphrase or the unwrapped key.
ALTER TABLE devices ADD COLUMN wrapped_vault_key JSONB;
