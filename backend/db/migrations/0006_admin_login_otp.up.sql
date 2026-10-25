-- Second factor for platform_role = 'admin' logins: after password
-- verification succeeds, a 6-digit code is emailed and must be redeemed
-- here before a session is issued. Separate from mfa_credentials (TOTP)
-- because it's mandatory for admins specifically, not opt-in, and the
-- code doesn't exist until login is attempted (so it can't be a static
-- secret like TOTP, which is why it needs its own challenge/attempt
-- tracking rather than reusing verifyMFACode).
CREATE TABLE admin_login_otp_challenges (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash   TEXT NOT NULL,
    attempts    INT NOT NULL DEFAULT 0,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX admin_login_otp_challenges_user_id_idx ON admin_login_otp_challenges (user_id);
