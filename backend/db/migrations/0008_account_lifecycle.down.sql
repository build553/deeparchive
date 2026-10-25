ALTER TABLE devices DROP COLUMN IF EXISTS wrapped_vault_key;
ALTER TABLE admin_login_otp_challenges DROP COLUMN IF EXISTS purpose;
ALTER TABLE users DROP COLUMN IF EXISTS terms_version, DROP COLUMN IF EXISTS terms_accepted_at, DROP COLUMN IF EXISTS deactivated_at;
