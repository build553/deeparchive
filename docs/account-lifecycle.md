# Account lifecycle

How sign-up, verification, deactivation, and the legal/consent checks behave.

## Sign-up

- The form needs two ticked boxes: agreement to the [Terms](../apps/web/src/features/legal/TermsPage.tsx)
  and Privacy Policy, and "I am 13 years old or older". The API enforces both
  (`400 consent_required`) and stores `users.terms_accepted_at` and
  `users.terms_version`. Bump `TermsVersion` in
  `backend/internal/identity/handlers.go` when either document changes.
- A verification link is emailed and is valid for **15 minutes**.
- You cannot log in until the email is verified (`403 email_not_verified`; the
  login page offers "Resend verification email").
- If the link lapses, the address is free again: signing up with it replaces the
  stale unverified account (`DeleteReclaimableUnverifiedUser`). A fresh resend
  keeps the account alive. A verified account's email is never reclaimed.

## Deactivation and recovery

- "Deactivate account" (Account page) or an admin status change sets
  `users.deactivated_at` and signs the account out everywhere.
- For **15 days** (`identity.RecoveryWindow`), logging in with the correct
  password emails a one-time code (`account_recovery` OTP, 5 minutes, 5 tries).
  Entering it reactivates the account and signs the person in
  (`auth.account_recovered` audit event).
- After 15 days login returns `403 account_deactivated` and the account cannot
  be restored.
- **Not implemented:** automatically erasing the data of accounts past the
  window. Their rows and backups remain until an operator removes them.

## Storage notice

The web app stores only the sign-in token, the theme, and the notice
acknowledgement in `localStorage`; there are no analytics or third-party
trackers. The banner (`CookieNotice`) is informational. Anything non-essential
added later must wait for an explicit opt-in there.

## Dashboard file browsing

`deepctl init` uploads the passphrase-wrapped vault key (`devices.wrapped_vault_key`).
`GET/PUT /api/v1/devices/{id}/vault-key` (owner only) serve it to the dashboard,
which unlocks it in the browser. Devices created before this feature can still
be opened by uploading `device.json` once and choosing "Remember this device".

## Before launch

The Terms and Privacy text is product-accurate working copy, **not**
lawyer-reviewed. Have counsel review it, and set `VITE_CONTACT_EMAIL` to a
monitored mailbox.
