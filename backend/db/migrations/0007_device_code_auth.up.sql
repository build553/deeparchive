-- Device authorization flow (RFC 8628-style, the same shape as `gh auth
-- login`): a CLI with no browser of its own gets a device_code (kept
-- secret, polled with) and a short user_code (shown to the human, typed
-- into a page opened on a device that DOES have a browser). Approving
-- binds the request to the approving user; the CLI's next poll then
-- redeems it for a real session via identity.Service.createSession.
CREATE TABLE device_auth_requests (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_code_hash TEXT NOT NULL,
    user_code        TEXT NOT NULL,
    client_name      TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied')),
    user_id          UUID REFERENCES users(id) ON DELETE CASCADE,
    expires_at       TIMESTAMPTZ NOT NULL,
    last_polled_at   TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX device_auth_requests_device_code_hash_key ON device_auth_requests (device_code_hash);
CREATE UNIQUE INDEX device_auth_requests_user_code_key ON device_auth_requests (user_code);
